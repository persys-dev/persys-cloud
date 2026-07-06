package scheduler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// defaultMeterStreamName is used if configuration doesn't specify one.
const defaultMeterStreamName = "persys:usage:stream"

// usageStreamEvent is the wire format published to the meter ingestion stream.
// persys-meter (or any other consumer) reads this via a Redis Streams
// consumer group (XREADGROUP) and is expected to deduplicate on EventID, or
// on the (NodeID, WorkloadID, ReportedAt) tuple as a fallback.
type usageStreamEvent struct {
	EventID    string                `json:"event_id"`
	NodeID     string                `json:"node_id"`
	WorkloadID string                `json:"workload_id"`
	RevisionID string                `json:"revision_id,omitempty"`
	ReportedAt string                `json:"reported_at"`
	Usage      *models.WorkloadUsage `json:"usage"`
}

// publishUsageEvent publishes a per-workload usage snapshot onto the Redis
// Stream consumed by persys-meter.
//
// This is intentionally best-effort and never blocks or fails the caller
// (heartbeat processing): if Redis isn't configured, unreachable, or the
// stream feature is disabled, the event is silently dropped after a warning
// log. The scheduler's own state - queried live via ListWorkloads/GetWorkload
// - remains the source of truth regardless of whether this publish succeeds,
// so no usage data is lost from the scheduler's perspective; only the
// meter's near-real-time feed would miss a sample.
func (s *Scheduler) publishUsageEvent(nodeID, workloadID, revisionID string, usage *models.WorkloadUsage) {
	if usage == nil || workloadID == "" {
		return
	}
	if s.redisClient == nil || s.cfg == nil || !s.cfg.MeterStreamEnabled {
		return
	}

	streamName := s.cfg.MeterStreamName
	if streamName == "" {
		streamName = defaultMeterStreamName
	}

	reportedAt := usage.CollectedAt
	if reportedAt.IsZero() {
		reportedAt = time.Now().UTC()
	}
	reportedAtText := reportedAt.UTC().Format(time.RFC3339Nano)

	event := usageStreamEvent{
		EventID:    uuid.NewString(),
		NodeID:     nodeID,
		WorkloadID: workloadID,
		RevisionID: revisionID,
		ReportedAt: reportedAtText,
		Usage:      usage,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		redisLogger.WithError(err).WithFields(logrus.Fields{
			"workload_id": workloadID,
		}).Warn("failed to marshal usage event for meter stream; dropping sample")
		return
	}

	maxLen := int64(100000)
	if s.cfg.MeterStreamMaxLen > 0 {
		maxLen = s.cfg.MeterStreamMaxLen
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// MaxLen+Approx trims the stream approximately (cheap, no full scan) so it
	// can't grow unbounded if persys-meter is behind or offline; consumer
	// groups still get at-least-once delivery for anything not yet trimmed.
	err = s.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: streamName,
		MaxLen: maxLen,
		Approx: true,
		Values: map[string]interface{}{
			"event_id":    event.EventID,
			"node_id":     event.NodeID,
			"workload_id": event.WorkloadID,
			"revision_id": event.RevisionID,
			"reported_at": event.ReportedAt,
			"payload":     string(payload),
		},
	}).Err()
	if err != nil {
		redisLogger.WithError(err).WithFields(logrus.Fields{
			"stream":      streamName,
			"workload_id": workloadID,
		}).Warn("failed to publish usage event to meter stream")
	}
}
