package store

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	clickhouse "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/ingest"
	"github.com/sirupsen/logrus"
)

// ClickHouseConfig is the subset of config needed to construct a
// ClickHouseStore - kept narrow so this package doesn't depend on the
// top-level config package.
type ClickHouseConfig struct {
	Addr          string
	Database      string
	Username      string
	Password      string
	TLS           bool
	RetentionDays int
}

// ClickHouseStore writes usage records to a ClickHouse MergeTree table.
//
// Dedup note: this table is a plain MergeTree, not ReplacingMergeTree. We
// deliberately do NOT rely on ClickHouse-side dedup because ReplacingMergeTree
// only collapses duplicate rows during background merges, which are
// asynchronous and not guaranteed to have run before a query executes -
// queries can and do see duplicate rows before a merge. Real dedup happens
// upstream, in the consumer, via a Redis SETNX guard on event_id before a
// record ever reaches this store (see internal/consumer). This table's
// ORDER BY still includes event_id so that if a duplicate ever does slip
// through, it's at least easy to filter with `LIMIT 1 BY event_id` in
// queries, or backfilled into a ReplacingMergeTree later if needed.
type ClickHouseStore struct {
	conn          clickhouse.Conn
	database      string
	retentionDays int
	logger        *logrus.Entry
}

// NewClickHouseStore opens a connection to ClickHouse. It does not create
// the table yet - call Init for that.
func NewClickHouseStore(cfg ClickHouseConfig, logger *logrus.Entry) (*ClickHouseStore, error) {
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 90
	}

	opts := &clickhouse.Options{
		Addr: []string{cfg.Addr},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		DialTimeout: 5 * time.Second,
	}
	if cfg.TLS {
		opts.TLS = &tls.Config{}
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open clickhouse connection: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("failed to ping clickhouse at %s: %w", cfg.Addr, err)
	}

	return &ClickHouseStore{
		conn:          conn,
		database:      cfg.Database,
		retentionDays: cfg.RetentionDays,
		logger:        logger,
	}, nil
}

// Init creates the usage_events table if it doesn't already exist.
// Idempotent - safe to call on every startup.
func (s *ClickHouseStore) Init(ctx context.Context) error {
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.usage_events
(
	event_id         String,
	node_id          String,
	workload_id      String,
	revision_id      String,
	workload_type    LowCardinality(String),
	reported_at      DateTime64(9, 'UTC'),
	ingested_at      DateTime64(3, 'UTC') DEFAULT now64(3),
	cpu_percent      Float64,
	memory_bytes     Int64,
	disk_read_bytes  Int64,
	disk_write_bytes Int64,
	net_rx_bytes     Int64,
	net_tx_bytes     Int64,
	source           LowCardinality(String)
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(reported_at)
ORDER BY (workload_id, reported_at, event_id)
TTL toDateTime(reported_at) + INTERVAL %d DAY
`, s.database, s.retentionDays)

	if err := s.conn.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("failed to create usage_events table: %w", err)
	}

	s.logger.WithFields(logrus.Fields{
		"database":       s.database,
		"retention_days": s.retentionDays,
	}).Info("usage_events table ready")
	return nil
}

// WriteBatch inserts every record in a single ClickHouse batch insert.
// All-or-nothing: if Send() fails partway, ClickHouse discards the whole
// batch, and the caller (consumer) is expected to leave the corresponding
// stream messages un-acked so they're retried.
func (s *ClickHouseStore) WriteBatch(ctx context.Context, records []ingest.UsageRecord) error {
	if len(records) == 0 {
		return nil
	}

	// Columns are listed explicitly (rather than a bare "INSERT INTO
	// usage_events") specifically to exclude ingested_at, which has a
	// DEFAULT now64(3) clause and is meant to be filled in by the server on
	// every insert - it isn't one of the values Append() provides below.
	insertQuery := fmt.Sprintf(`INSERT INTO %s.usage_events (
		event_id, node_id, workload_id, revision_id, workload_type,
		reported_at, cpu_percent, memory_bytes, disk_read_bytes,
		disk_write_bytes, net_rx_bytes, net_tx_bytes, source
	)`, s.database)

	batch, err := s.conn.PrepareBatch(ctx, insertQuery)
	if err != nil {
		return fmt.Errorf("failed to prepare clickhouse batch: %w", err)
	}
	// Safe to call even after a successful Send(): Close() on an
	// already-sent batch is a no-op per the driver's contract.
	defer batch.Abort()

	for _, r := range records {
		err := batch.Append(
			r.EventID,
			r.NodeID,
			r.WorkloadID,
			r.RevisionID,
			r.WorkloadType,
			r.ReportedAt,
			r.CPUPercent,
			r.MemoryBytes,
			r.DiskReadBytes,
			r.DiskWriteBytes,
			r.NetRXBytes,
			r.NetTXBytes,
			r.Source,
		)
		if err != nil {
			return fmt.Errorf("failed to append record (workload=%s, event=%s) to batch: %w", r.WorkloadID, r.EventID, err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("failed to send clickhouse batch of %d records: %w", len(records), err)
	}

	return nil
}

// Ping reports whether ClickHouse is currently reachable. Cheap by design -
// used for readiness probes, so it must not run DDL or touch usage_events.
func (s *ClickHouseStore) Ping(ctx context.Context) error {
	return s.conn.Ping(ctx)
}

const maxHistoryLimit = 10000

// History returns raw usage samples for a workload within [from, to],
// newest first, capped at limit rows (limit <= 0 defaults to 1000, and
// anything above maxHistoryLimit is capped there - this is meant for
// "show me recent samples", not bulk export).
func (s *ClickHouseStore) History(ctx context.Context, workloadID string, from, to time.Time, limit int) ([]ingest.UsageRecord, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > maxHistoryLimit {
		limit = maxHistoryLimit
	}

	query := fmt.Sprintf(`
		SELECT event_id, node_id, workload_id, revision_id, workload_type, reported_at,
		       cpu_percent, memory_bytes, disk_read_bytes, disk_write_bytes,
		       net_rx_bytes, net_tx_bytes, source
		FROM %s.usage_events
		WHERE workload_id = ? AND reported_at >= ? AND reported_at <= ?
		ORDER BY reported_at DESC
		LIMIT ?
	`, s.database)

	rows, err := s.conn.Query(ctx, query, workloadID, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query usage history for workload %s: %w", workloadID, err)
	}
	defer rows.Close()

	records := make([]ingest.UsageRecord, 0, limit)
	for rows.Next() {
		var r ingest.UsageRecord
		if err := rows.Scan(
			&r.EventID, &r.NodeID, &r.WorkloadID, &r.RevisionID, &r.WorkloadType, &r.ReportedAt,
			&r.CPUPercent, &r.MemoryBytes, &r.DiskReadBytes, &r.DiskWriteBytes,
			&r.NetRXBytes, &r.NetTXBytes, &r.Source,
		); err != nil {
			return nil, fmt.Errorf("failed to scan usage history row for workload %s: %w", workloadID, err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating usage history rows for workload %s: %w", workloadID, err)
	}

	return records, nil
}

// Summary aggregates a workload's usage over [from, to]. See the
// EstimatedCPUCoreSeconds and *BytesDelta doc comments on UsageSummary for
// important accuracy caveats before wiring this into real billing.
func (s *ClickHouseStore) Summary(ctx context.Context, workloadID string, from, to time.Time) (*UsageSummary, error) {
	summary := &UsageSummary{WorkloadID: workloadID, From: from, To: to}

	// Check for zero rows first and return early: ClickHouse's avg()/min()/
	// max() all return NULL over an empty set, and scanning NULL into a
	// non-pointer Go float64/int64 would error. Easier to special-case
	// "no data" explicitly than to scan everything as nullable types.
	countQuery := fmt.Sprintf(`
		SELECT count() FROM %s.usage_events
		WHERE workload_id = ? AND reported_at >= ? AND reported_at <= ?
	`, s.database)
	if err := s.conn.QueryRow(ctx, countQuery, workloadID, from, to).Scan(&summary.SampleCount); err != nil {
		return nil, fmt.Errorf("failed to count usage samples for workload %s: %w", workloadID, err)
	}
	if summary.SampleCount == 0 {
		return summary, nil
	}

	aggQuery := fmt.Sprintf(`
		SELECT
			avg(cpu_percent),
			max(cpu_percent),
			avg(memory_bytes),
			max(memory_bytes),
			max(disk_read_bytes) - min(disk_read_bytes),
			max(disk_write_bytes) - min(disk_write_bytes),
			max(net_rx_bytes) - min(net_rx_bytes),
			max(net_tx_bytes) - min(net_tx_bytes),
			min(reported_at),
			max(reported_at)
		FROM %s.usage_events
		WHERE workload_id = ? AND reported_at >= ? AND reported_at <= ?
	`, s.database)

	row := s.conn.QueryRow(ctx, aggQuery, workloadID, from, to)
	if err := row.Scan(
		&summary.AvgCPUPercent, &summary.MaxCPUPercent,
		&summary.AvgMemoryBytes, &summary.MaxMemoryBytes,
		&summary.DiskReadBytesDelta, &summary.DiskWriteBytesDelta,
		&summary.NetRXBytesDelta, &summary.NetTXBytesDelta,
		&summary.FirstSample, &summary.LastSample,
	); err != nil {
		return nil, fmt.Errorf("failed to aggregate usage summary for workload %s: %w", workloadID, err)
	}

	summary.EstimatedCPUCoreSeconds = (summary.AvgCPUPercent / 100.0) * to.Sub(from).Seconds()

	return summary, nil
}

// Close closes the underlying ClickHouse connection.
func (s *ClickHouseStore) Close() error {
	return s.conn.Close()
}
