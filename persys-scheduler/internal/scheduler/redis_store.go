package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

var redisLogger = logging.C("scheduler.redis")

func (s *Scheduler) initRedisStore() {
	if s.cfg == nil || s.cfg.RedisAddr == "" {
		return
	}
	client := redis.NewClient(&redis.Options{
		Addr:     s.cfg.RedisAddr,
		Password: s.cfg.RedisPassword,
		DB:       s.cfg.RedisDB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		redisLogger.WithError(err).Warn("redis configured but unavailable; falling back to etcd for reconciliation telemetry")
		_ = client.Close()
		return
	}
	s.redisClient = client
	redisLogger.WithField("addr", s.cfg.RedisAddr).Info("redis telemetry store enabled")
}

func (s *Scheduler) writeReconciliationTelemetry(workloadID, action string, success bool, reason string, attemptedAt time.Time) {
	rec := map[string]interface{}{
		"workloadId":  workloadID,
		"action":      action,
		"success":     success,
		"reason":      reason,
		"attemptedAt": attemptedAt.UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if s.redisClient != nil {
		ttl := 24 * time.Hour
		if s.cfg != nil && s.cfg.RedisReconcileTTL > 0 {
			ttl = s.cfg.RedisReconcileTTL
		}
		key := fmt.Sprintf("reconciliation:%s", workloadID)
		historyKey := "reconciliation:history"
		if err := s.redisClient.Set(context.Background(), key, payload, ttl).Err(); err == nil {
			maxEntries := int64(2000)
			if s.cfg != nil && s.cfg.RedisEventMaxEntries > 0 {
				maxEntries = s.cfg.RedisEventMaxEntries
			}
			pipe := s.redisClient.TxPipeline()
			pipe.LPush(context.Background(), historyKey, payload)
			pipe.LTrim(context.Background(), historyKey, 0, maxEntries-1)
			pipe.Expire(context.Background(), historyKey, ttl)
			_, _ = pipe.Exec(context.Background())
			return
		}
		redisLogger.WithError(err).WithFields(logrus.Fields{"key": key}).Warn("failed writing reconciliation telemetry to redis")
	}
	_ = s.RetryableEtcdPut(reconciliationKey(workloadID), string(payload))
}

// schedulerEventsStreamKey is the single Redis Stream every scheduler
// replica writes cluster-wide events to and reads/watches from. Because
// all replicas share the same Redis instance, this is exactly as
// "cluster-wide" as the etcd-backed approach it replaced: an event
// emitted by whichever replica handled the triggering request is visible
// to every replica's WatchEvents callers, not just the one that wrote it.
const schedulerEventsStreamKey = "scheduler:events"

// eventRetentionTTL / eventMaxEntries return the configured Redis event
// retention knobs, defaulting sensibly if unset.
func (s *Scheduler) eventRetentionTTL() time.Duration {
	if s.cfg != nil && s.cfg.RedisEventTTL > 0 {
		return s.cfg.RedisEventTTL
	}
	return 24 * time.Hour
}

func (s *Scheduler) eventMaxEntries() int64 {
	if s.cfg != nil && s.cfg.RedisEventMaxEntries > 0 {
		return s.cfg.RedisEventMaxEntries
	}
	return 1000
}

// writeEventToStream appends one event to the shared Redis Stream,
// trimming to eventMaxEntries (approximate — MAXLEN ~ is O(1) amortized,
// unlike a scan-and-delete sweep) and refreshing the stream key's TTL on
// every write so an idle cluster's event history eventually expires
// entirely rather than persisting forever.
//
// Returns the assigned stream ID on success. Cluster-wide events are
// explicitly NOT written to etcd (see the doc comment on emitEvent in
// state_store.go for why) — if Redis is unavailable, the event is
// dropped. This is a deliberate tradeoff: events are high-churn,
// ephemeral, observability-oriented data, not state the cluster's
// correctness depends on, and coupling them to etcd would reintroduce
// exactly the problem this design avoids.
func (s *Scheduler) writeEventToStream(payload []byte) (string, error) {
	if s.redisClient == nil {
		return "", fmt.Errorf("redis is not configured")
	}
	ctx := context.Background()
	id, err := s.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: schedulerEventsStreamKey,
		MaxLen: s.eventMaxEntries(),
		Approx: true,
		Values: map[string]interface{}{"data": payload},
	}).Result()
	if err != nil {
		return "", err
	}
	// Best-effort TTL refresh; a failure here just means the stream lives
	// a bit longer than configured, not a correctness problem.
	_ = s.redisClient.Expire(ctx, schedulerEventsStreamKey, s.eventRetentionTTL()).Err()
	return id, nil
}

// readEventsFromStream returns the most recent limit events (or up to
// eventMaxEntries if limit <= 0), newest first — matching the ordering
// ListSchedulerEvents has always returned — plus the Redis stream ID of
// the newest entry returned (empty if there were none). The ID lets
// WatchEvents resume live-watching from exactly where a replay left off,
// with no gap and no duplicate delivery.
//
// Returns an empty slice (not an error) if Redis is unavailable or the
// stream doesn't exist yet, since this backs read paths (ListEvents,
// WatchEvents replay) that should degrade gracefully rather than fail a
// CLI/dashboard request outright over what is, by design, best-effort
// observability data.
func (s *Scheduler) readEventsFromStream(limit int64) (events []models.SchedulerEvent, newestID string) {
	if s.redisClient == nil {
		return nil, ""
	}
	if limit <= 0 {
		limit = s.eventMaxEntries()
	}
	msgs, err := s.redisClient.XRevRangeN(context.Background(), schedulerEventsStreamKey, "+", "-", limit).Result()
	if err != nil {
		redisLogger.WithError(err).Warn("failed to read events from redis stream")
		return nil, ""
	}
	events = make([]models.SchedulerEvent, 0, len(msgs))
	for i, msg := range msgs {
		raw, ok := msg.Values["data"]
		if !ok {
			continue
		}
		var payload []byte
		switch v := raw.(type) {
		case string:
			payload = []byte(v)
		case []byte:
			payload = v
		default:
			continue
		}
		var event models.SchedulerEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			continue
		}
		events = append(events, event)
		if i == 0 {
			newestID = msg.ID
		}
	}
	return events, newestID
}

// watchEventStream blocks, delivering events newly added to the stream
// after fromID to onEvent, until ctx is cancelled, onEvent returns an
// error, or an unrecoverable Redis error occurs. fromID should be the
// highest ID already delivered to the caller (e.g. from a prior replay
// via readEventsFromStream, or a previous call to this function), or "$"
// to start from only new events with no replay.
//
// Always returns the highest ID actually delivered to onEvent before
// returning (even on error), so a caller retrying after a transient
// failure can resume from exactly that point instead of re-delivering
// everything from fromID again.
func (s *Scheduler) watchEventStream(ctx context.Context, fromID string, onEvent func(models.SchedulerEvent) error) (lastDeliveredID string, err error) {
	if s.redisClient == nil {
		return fromID, fmt.Errorf("redis is not configured")
	}
	lastID := fromID
	for {
		if ctx.Err() != nil {
			return lastID, ctx.Err()
		}
		res, readErr := s.redisClient.XRead(ctx, &redis.XReadArgs{
			Streams: []string{schedulerEventsStreamKey, lastID},
			Block:   0, // block indefinitely until data arrives or ctx is cancelled
			Count:   0,
		}).Result()
		if readErr != nil {
			if readErr == redis.Nil || ctx.Err() != nil {
				continue
			}
			return lastID, fmt.Errorf("redis XREAD failed: %w", readErr)
		}
		for _, stream := range res {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				raw, ok := msg.Values["data"]
				if !ok {
					continue
				}
				var payload []byte
				switch v := raw.(type) {
				case string:
					payload = []byte(v)
				case []byte:
					payload = v
				default:
					continue
				}
				var event models.SchedulerEvent
				if err := json.Unmarshal(payload, &event); err != nil {
					continue
				}
				if err := onEvent(event); err != nil {
					return lastID, err
				}
			}
		}
	}
}
