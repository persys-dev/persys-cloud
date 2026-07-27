// Package store defines where ingested usage records end up. ClickHouse is
// the only implementation today (per the architecture decision to use it for
// this workload), but consumer code depends only on this interface so a
// second backend (e.g. Postgres for smaller deployments) can be added later
// without touching the consumer/ingest packages.
package store

import (
	"context"
	"time"

	"github.com/persys-dev/persys-cloud/persys-meter/internal/ingest"
)

// Store persists batches of usage records.
type Store interface {
	// Init prepares the backend for writes (e.g. creating tables). Called
	// once at startup; must be safe to call repeatedly (idempotent).
	Init(ctx context.Context) error

	// Ping reports whether the backend is currently reachable, for use in
	// readiness checks. Should be cheap - implementations should NOT run
	// DDL or anything beyond a lightweight connectivity check.
	Ping(ctx context.Context) error

	// WriteBatch durably writes every record in the batch. It must either
	// fully succeed or return an error - callers rely on all-or-nothing
	// semantics to decide whether to ack the corresponding stream messages.
	WriteBatch(ctx context.Context, records []ingest.UsageRecord) error

	// Close releases any underlying connections.
	Close() error
}

// QueryStore is the read side: historical lookups against durably stored
// usage data. Kept as a separate interface from Store (rather than adding
// these methods to it) since not every future write backend necessarily
// wants to implement rich queries - a single concrete type is free to
// implement both, as ClickHouseStore does.
type QueryStore interface {
	// History returns raw samples for a workload within [from, to],
	// newest first, capped at limit rows.
	History(ctx context.Context, workloadID string, from, to time.Time, limit int) ([]ingest.UsageRecord, error)

	// Summary aggregates a workload's usage over [from, to] - this is the
	// primitive a billing/quota-enforcement caller is expected to build on:
	// "how much did this workload consume in this window", not a policy
	// decision about limits or what to do when they're exceeded.
	Summary(ctx context.Context, workloadID string, from, to time.Time) (*UsageSummary, error)
}

// UsageSummary aggregates a workload's usage samples over a time window.
type UsageSummary struct {
	WorkloadID string    `json:"workload_id"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	// SampleCount is how many raw usage_events rows fell in the window. A
	// summary with SampleCount 0 means no data was reported in this window
	// at all (not that usage was zero) - callers should treat these
	// differently, and this struct's zero-valued fields alongside
	// SampleCount == 0 make that distinguishable.
	SampleCount uint64 `json:"sample_count"`

	AvgCPUPercent float64 `json:"avg_cpu_percent"`
	MaxCPUPercent float64 `json:"max_cpu_percent"`

	AvgMemoryBytes float64 `json:"avg_memory_bytes"`
	MaxMemoryBytes int64   `json:"max_memory_bytes"`

	// *BytesDelta are computed as max(counter) - min(counter) within the
	// window. Since these source counters are cumulative-since-runtime-start
	// (see ingest.UsageRecord), this is only correct if the workload didn't
	// restart during the window - a restart resets the runtime's counter to
	// zero, which would make a delta look artificially small or even
	// negative. This is a known limitation, not silently "handled" -
	// something a real billing pipeline should account for (e.g. by
	// tracking restarts explicitly and summing deltas between them).
	DiskReadBytesDelta  int64 `json:"disk_read_bytes_delta"`
	DiskWriteBytesDelta int64 `json:"disk_write_bytes_delta"`
	NetRXBytesDelta     int64 `json:"net_rx_bytes_delta"`
	NetTXBytesDelta     int64 `json:"net_tx_bytes_delta"`

	FirstSample time.Time `json:"first_sample"`
	LastSample  time.Time `json:"last_sample"`

	// EstimatedCPUCoreSeconds approximates CPU-core-seconds consumed over
	// the window as avg(cpu_percent)/100 * window_seconds. This is a
	// deliberately simple approximation, not a rigorous integral over
	// irregularly spaced samples (that would need trapezoidal integration
	// between consecutive points) - it assumes usage sampling is roughly
	// uniform across the window. Good enough for a first cut; revisit if
	// billing accuracy requirements need more precision than this gives.
	EstimatedCPUCoreSeconds float64 `json:"estimated_cpu_core_seconds"`
}
