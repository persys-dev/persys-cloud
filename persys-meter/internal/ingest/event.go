// Package ingest defines the wire format published by persys-scheduler onto
// the Redis usage stream, and turns raw stream messages into the flat
// UsageRecord shape the store package writes to ClickHouse.
package ingest

import (
	"encoding/json"
	"fmt"
	"time"
)

// Event mirrors persys-scheduler's internal usageStreamEvent struct
// (internal/scheduler/usage_stream.go) field-for-field, including its
// snake_case JSON tags for the envelope. This is a cross-repo contract that
// isn't shared via a common package (the two services are separate Go
// modules) - if persys-scheduler's envelope shape changes, this struct must
// be updated to match by hand. Same caveat applies to UsageSnapshot below.
type Event struct {
	EventID    string         `json:"event_id"`
	NodeID     string         `json:"node_id"`
	WorkloadID string         `json:"workload_id"`
	RevisionID string         `json:"revision_id,omitempty"`
	ReportedAt string         `json:"reported_at"`
	Usage      *UsageSnapshot `json:"usage"`
}

// UsageSnapshot mirrors persys-scheduler's internal/models.WorkloadUsage
// JSON shape - NOT compute-agent's. This matters: compute-agent's
// models.WorkloadUsage uses snake_case tags (e.g. "cpu_percent"), but the
// scheduler re-serializes usage data through its *own* models.WorkloadUsage
// type before publishing, which uses camelCase tags (e.g. "cpuPercent").
// Since the scheduler is what actually writes the stream payload, its tags
// are what persys-meter must parse against.
type UsageSnapshot struct {
	WorkloadID     string    `json:"workloadId,omitempty"`
	Type           string    `json:"type,omitempty"`
	CPUPercent     float64   `json:"cpuPercent,omitempty"`
	MemoryBytes    int64     `json:"memoryBytes,omitempty"`
	DiskReadBytes  int64     `json:"diskReadBytes,omitempty"`
	DiskWriteBytes int64     `json:"diskWriteBytes,omitempty"`
	NetRXBytes     int64     `json:"netRxBytes,omitempty"`
	NetTXBytes     int64     `json:"netTxBytes,omitempty"`
	CollectedAt    time.Time `json:"collectedAt,omitempty"`
	Source         string    `json:"source,omitempty"`
}

// UsageRecord is the flat row shape written to ClickHouse - one row per
// usage sample, with the envelope and usage snapshot merged together and
// ReportedAt parsed into a real time.Time.
type UsageRecord struct {
	EventID        string
	NodeID         string
	WorkloadID     string
	RevisionID     string
	WorkloadType   string
	ReportedAt     time.Time
	CPUPercent     float64
	MemoryBytes    int64
	DiskReadBytes  int64
	DiskWriteBytes int64
	NetRXBytes     int64
	NetTXBytes     int64
	Source         string
}

// ParseStreamMessage turns a Redis XMessage's Values map into a UsageRecord.
//
// persys-scheduler publishes both a "payload" field (the full event as a
// JSON string) and individual flat fields (event_id, node_id, workload_id,
// revision_id, reported_at) on every stream entry. This function prefers
// "payload" since it's the only place the actual usage numbers live; the
// flat fields exist so the stream is at least partially inspectable with
// plain `redis-cli XRANGE` without decoding JSON.
func ParseStreamMessage(values map[string]interface{}) (*UsageRecord, error) {
	payload, ok := stringValue(values, "payload")
	if !ok || payload == "" {
		return nil, fmt.Errorf("stream message has no payload field")
	}

	var event Event
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	if event.WorkloadID == "" {
		return nil, fmt.Errorf("event payload missing workload_id")
	}
	if event.Usage == nil {
		return nil, fmt.Errorf("event payload for workload %s has no usage data", event.WorkloadID)
	}

	reportedAt, err := time.Parse(time.RFC3339Nano, event.ReportedAt)
	if err != nil {
		// Fall back to the usage snapshot's own CollectedAt, and failing
		// that, now - a malformed timestamp shouldn't drop an otherwise
		// valid usage sample.
		if !event.Usage.CollectedAt.IsZero() {
			reportedAt = event.Usage.CollectedAt
		} else {
			reportedAt = time.Now().UTC()
		}
	}

	return &UsageRecord{
		EventID:        event.EventID,
		NodeID:         event.NodeID,
		WorkloadID:     event.WorkloadID,
		RevisionID:     event.RevisionID,
		WorkloadType:   event.Usage.Type,
		ReportedAt:     reportedAt.UTC(),
		CPUPercent:     event.Usage.CPUPercent,
		MemoryBytes:    event.Usage.MemoryBytes,
		DiskReadBytes:  event.Usage.DiskReadBytes,
		DiskWriteBytes: event.Usage.DiskWriteBytes,
		NetRXBytes:     event.Usage.NetRXBytes,
		NetTXBytes:     event.Usage.NetTXBytes,
		Source:         event.Usage.Source,
	}, nil
}

// stringValue safely extracts a string field from a Redis XMessage.Values
// map. go-redis returns stream field values as `interface{}` holding a
// `string` in the common case, but is defensive here in case a future
// go-redis version or a non-Go producer writes a different underlying type.
func stringValue(values map[string]interface{}, key string) (string, bool) {
	raw, ok := values[key]
	if !ok || raw == nil {
		return "", false
	}
	switch v := raw.(type) {
	case string:
		return v, true
	case []byte:
		return string(v), true
	case fmt.Stringer:
		return v.String(), true
	default:
		return fmt.Sprintf("%v", v), true
	}
}
