// Package consumer implements the Redis Streams consumer-group side of the
// meter ingestion pipeline: multiple worker goroutines read new messages via
// XREADGROUP, a background sweep reclaims messages abandoned by dead
// consumers via XAUTOCLAIM, a Redis-backed guard deduplicates redelivered
// events, and a single batching writer flushes accumulated records to the
// store before acking the corresponding stream messages.
package consumer

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/persys-meter/internal/cache"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/config"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/ingest"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/store"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// pendingRecord couples a parsed usage record with the stream message ID it
// came from, so the batch writer can ack the right IDs after a successful
// flush - and can just as easily leave them un-acked after a failed one.
type pendingRecord struct {
	messageID string
	record    ingest.UsageRecord
}

// Consumer wires together the Redis stream reader, dedup guard, and batch
// writer.
type Consumer struct {
	cfg    *config.Config
	redis  *redis.Client
	store  store.Store
	cache  *cache.LatestCache
	logger *logrus.Entry

	pending chan pendingRecord

	wg     sync.WaitGroup
	cancel context.CancelFunc
}

// New constructs a Consumer. Call Run to start it. c is the shared
// in-memory latest-usage cache (also read by the API and the Prometheus
// WorkloadCollector) - the consumer is what keeps it up to date.
func New(cfg *config.Config, redisClient *redis.Client, st store.Store, c *cache.LatestCache, logger *logrus.Entry) *Consumer {
	return &Consumer{
		cfg:    cfg,
		redis:  redisClient,
		store:  st,
		cache:  c,
		logger: logger,
		// Buffered so a slow flush cycle doesn't immediately stall workers;
		// sized generously relative to one batch so a full batch can queue
		// up while the previous one is still being sent.
		pending: make(chan pendingRecord, cfg.BatchSize*2),
	}
}

// Ready implements health.Checker: reports whether Redis is reachable.
// (ClickHouse readiness is reported by the store's own health check if the
// caller wants to compose one; keeping this narrow to Redis since that's
// what this package owns.)
func (c *Consumer) Ready(ctx context.Context) error {
	return c.redis.Ping(ctx).Err()
}

// Run ensures the consumer group exists, then starts all worker goroutines,
// the reclaim sweep, and the batch flusher. It blocks until ctx is
// cancelled, then drains in-flight work before returning.
func (c *Consumer) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	if err := c.ensureGroup(ctx); err != nil {
		cancel()
		return err
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.flushLoop(runCtx)
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.reclaimLoop(runCtx)
	}()

	for i := 0; i < c.cfg.Workers; i++ {
		c.wg.Add(1)
		go func(workerNum int) {
			defer c.wg.Done()
			c.readLoop(runCtx, workerNum)
		}(i)
	}

	<-runCtx.Done()
	c.logger.Info("consumer shutting down, waiting for in-flight work to drain")
	c.wg.Wait()
	return nil
}

// Stop signals all goroutines to exit and blocks until they do (including a
// final batch flush of anything still buffered).
func (c *Consumer) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// ensureGroup creates the consumer group (and the stream itself, via
// MKSTREAM, if it doesn't exist yet - e.g. persys-meter started before
// persys-scheduler ever published anything). Starting at "0" means a
// brand-new group sees the whole stream history; "$" would mean "only new
// messages from now on". "0" is the safer default for a metering pipeline -
// better to reprocess something already ingested (harmless: downstream
// dedup by event_id in your own consumer already guards this) than to
// silently skip the backlog.
func (c *Consumer) ensureGroup(ctx context.Context) error {
	err := c.redis.XGroupCreateMkStream(ctx, c.cfg.StreamName, c.cfg.ConsumerGroup, "0").Err()
	if err != nil && !isBusyGroupErr(err) {
		return err
	}
	c.logger.WithFields(logrus.Fields{
		"stream": c.cfg.StreamName,
		"group":  c.cfg.ConsumerGroup,
	}).Info("consumer group ready")
	return nil
}

func isBusyGroupErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}

// readLoop is the main per-worker XREADGROUP loop, reading only new ('>')
// messages. Reclaimed (previously-pending) messages are handled separately
// by reclaimLoop so a stalled worker's backlog doesn't monopolize every
// other worker's read calls.
func (c *Consumer) readLoop(ctx context.Context, workerNum int) {
	consumerName := c.cfg.ConsumerName
	if c.cfg.Workers > 1 {
		consumerName = c.cfg.ConsumerName + "-w" + strconv.Itoa(workerNum)
	}
	log := c.logger.WithField("worker", consumerName)
	log.Info("read worker starting")

	for {
		select {
		case <-ctx.Done():
			log.Info("read worker stopping")
			return
		default:
		}

		streams, err := c.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.cfg.ConsumerGroup,
			Consumer: consumerName,
			Streams:  []string{c.cfg.StreamName, ">"},
			Count:    c.cfg.ReadCount,
			Block:    c.cfg.ReadBlock,
		}).Result()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if errors.Is(err, redis.Nil) {
				// Block duration elapsed with nothing new - expected, loop again.
				continue
			}
			log.WithError(err).Warn("XREADGROUP failed, backing off")
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}

		for _, s := range streams {
			metrics.EventsConsumedTotal.WithLabelValues(s.Stream).Add(float64(len(s.Messages)))
			c.handleMessages(ctx, log, s.Stream, s.Messages)
		}
	}
}

// reclaimLoop periodically claims pending messages that have sat un-acked
// longer than ClaimMinIdle - i.e. messages delivered to a consumer that then
// crashed, was killed, or hung before ack'ing. Without this, a dead pod's
// in-flight messages would never be retried by anyone.
func (c *Consumer) reclaimLoop(ctx context.Context) {
	log := c.logger.WithField("component", "reclaim")
	ticker := time.NewTicker(c.cfg.ClaimInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		start := "0-0"
		for {
			messages, next, err := c.redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream:   c.cfg.StreamName,
				Group:    c.cfg.ConsumerGroup,
				Consumer: c.cfg.ConsumerName,
				MinIdle:  c.cfg.ClaimMinIdle,
				Start:    start,
				Count:    int64(c.cfg.BatchSize),
			}).Result()
			if err != nil {
				log.WithError(err).Warn("XAUTOCLAIM failed")
				break
			}
			if len(messages) > 0 {
				metrics.MessagesReclaimedTotal.WithLabelValues(c.cfg.StreamName).Add(float64(len(messages)))
				log.WithField("count", len(messages)).Info("reclaimed pending messages from a stalled consumer")
				c.handleMessages(ctx, log, c.cfg.StreamName, messages)
			}
			if next == "0-0" || len(messages) == 0 {
				break
			}
			start = next
		}
	}
}

// handleMessages parses, dedups, and enqueues a batch of messages for
// writing. Messages that fail to parse are logged and acked immediately
// (see the comment inline) rather than retried forever.
func (c *Consumer) handleMessages(ctx context.Context, log *logrus.Entry, streamName string, messages []redis.XMessage) {
	for _, msg := range messages {
		record, err := ingest.ParseStreamMessage(msg.Values)
		if err != nil {
			metrics.EventsParseFailedTotal.WithLabelValues(streamName).Inc()
			log.WithError(err).WithField("message_id", msg.ID).Error(
				"failed to parse stream message; acking anyway to avoid a poison-pill redelivery loop")
			// A message that can never parse would otherwise be reclaimed
			// and retried forever. We've already logged it at Error level
			// for investigation - that's the appropriate place to catch
			// this, not an infinite XAUTOCLAIM loop.
			c.ackAndLog(ctx, log, streamName, msg.ID)
			continue
		}

		if !record.ReportedAt.IsZero() {
			metrics.ConsumerLagSeconds.WithLabelValues(streamName).Observe(time.Since(record.ReportedAt).Seconds())
		}

		// Update the live cache unconditionally, even for what turns out to
		// be a duplicate below - it's the same data either way, and this is
		// what backs the per-workload Prometheus metrics and the API's live
		// endpoints, which don't care about ClickHouse-side dedup at all.
		c.cache.Set(*record)

		dup, err := c.isDuplicate(ctx, record.EventID)
		if err != nil {
			// Redis hiccup on the dedupe check - fail open (treat as not a
			// duplicate) rather than dropping a legitimate, un-deduped
			// event. Worst case is a rare double-write, which the ORDER BY
			// event_id in ClickHouse at least makes filterable later.
			log.WithError(err).WithField("event_id", record.EventID).Warn(
				"dedupe check failed, proceeding as if not a duplicate")
		}
		if dup {
			metrics.EventsDedupedTotal.WithLabelValues(streamName).Inc()
			c.ackAndLog(ctx, log, streamName, msg.ID)
			continue
		}

		select {
		case c.pending <- pendingRecord{messageID: msg.ID, record: *record}:
		case <-ctx.Done():
			return
		}
	}
}

// isDuplicate uses SETNX with a TTL as a lightweight, explicit dedup guard:
// the first consumer to see a given event_id "claims" it; any redelivery of
// the same event_id (at-least-once delivery guarantees this can happen)
// finds the key already set and is treated as a duplicate.
//
// The TTL bound (config.DedupeTTL) means a duplicate arriving after the TTL
// expires would slip through - that's an accepted tradeoff, not a bug: it
// bounds how much memory the dedupe keyspace uses, and redeliveries in
// practice happen within seconds to minutes (XAUTOCLAIM's MinIdle), not
// hours or days.
func (c *Consumer) isDuplicate(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		// No event_id to dedupe on - treat as unique rather than blocking
		// ingestion entirely on a missing field.
		return false, nil
	}
	key := "persys:meter:dedupe:" + eventID
	set, err := c.redis.SetNX(ctx, key, 1, c.cfg.DedupeTTL).Result()
	if err != nil {
		return false, err
	}
	return !set, nil
}

// releaseDedupeGuard removes the dedupe key for a record whose batch write
// failed, so a future redelivery (or the next reclaim sweep) isn't
// incorrectly treated as an already-processed duplicate.
func (c *Consumer) releaseDedupeGuard(ctx context.Context, eventID string) {
	if eventID == "" {
		return
	}
	if err := c.redis.Del(ctx, "persys:meter:dedupe:"+eventID).Err(); err != nil {
		c.logger.WithError(err).WithField("event_id", eventID).Warn(
			"failed to release dedupe guard after a failed write; it will simply expire via TTL instead")
	}
}

func (c *Consumer) ackAndLog(ctx context.Context, log *logrus.Entry, streamName, messageID string) {
	if err := c.redis.XAck(ctx, streamName, c.cfg.ConsumerGroup, messageID).Err(); err != nil {
		log.WithError(err).WithField("message_id", messageID).Warn("failed to ack message")
	}
}

// flushLoop is the single writer goroutine: it accumulates pendingRecords
// off the shared channel and flushes them to the store whenever either the
// configured batch size or flush interval is reached, whichever comes
// first. Centralizing writes in one goroutine (rather than having each
// worker write its own small batches) means bigger, more efficient
// ClickHouse inserts regardless of how many read workers are configured.
func (c *Consumer) flushLoop(ctx context.Context) {
	log := c.logger.WithField("component", "flusher")
	batch := make([]pendingRecord, 0, c.cfg.BatchSize)
	ticker := time.NewTicker(c.cfg.BatchFlushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		c.writeBatch(context.Background(), log, batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Drain whatever's left in the channel (non-blocking) before
			// the final flush, so a shutdown doesn't drop already-read
			// records that were waiting on the channel.
			for {
				select {
				case pr := <-c.pending:
					batch = append(batch, pr)
					if len(batch) >= c.cfg.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}

		case pr := <-c.pending:
			batch = append(batch, pr)
			if len(batch) >= c.cfg.BatchSize {
				flush()
			}

		case <-ticker.C:
			flush()
		}
	}
}

// writeBatch sends one batch to the store, then acks (on success) or
// releases the dedupe guard and leaves messages pending for retry (on
// failure).
func (c *Consumer) writeBatch(ctx context.Context, log *logrus.Entry, batch []pendingRecord) {
	records := make([]ingest.UsageRecord, len(batch))
	ids := make([]string, len(batch))
	for i, pr := range batch {
		records[i] = pr.record
		ids[i] = pr.messageID
	}

	start := time.Now()
	err := c.store.WriteBatch(ctx, records)
	elapsed := time.Since(start)

	if err != nil {
		metrics.BatchWriteFailedTotal.WithLabelValues(c.cfg.StreamName).Inc()
		metrics.BatchFlushDuration.WithLabelValues(c.cfg.StreamName, "failure").Observe(elapsed.Seconds())
		log.WithError(err).WithFields(logrus.Fields{
			"batch_size": len(batch),
			"elapsed_ms": elapsed.Milliseconds(),
		}).Error("batch write failed; leaving messages pending for retry")

		// Let a future redelivery try again rather than silently losing
		// these events to the dedupe guard.
		for _, pr := range batch {
			c.releaseDedupeGuard(ctx, pr.record.EventID)
		}
		return
	}

	metrics.EventsWrittenTotal.WithLabelValues(c.cfg.StreamName).Add(float64(len(batch)))
	metrics.BatchSize.WithLabelValues(c.cfg.StreamName).Observe(float64(len(batch)))
	metrics.BatchFlushDuration.WithLabelValues(c.cfg.StreamName, "success").Observe(elapsed.Seconds())

	if err := c.redis.XAck(ctx, c.cfg.StreamName, c.cfg.ConsumerGroup, ids...).Err(); err != nil {
		log.WithError(err).WithField("batch_size", len(batch)).Warn(
			"batch written successfully but XACK failed; messages may be redelivered and will be deduped")
	}

	log.WithFields(logrus.Fields{
		"batch_size": len(batch),
		"elapsed_ms": elapsed.Milliseconds(),
	}).Debug("batch flushed")
}
