package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for persys-meter, loaded from
// environment variables. Every field has a sane default so the service can
// run against a local Redis + ClickHouse with zero configuration.
type Config struct {
	// Redis - must point at the same Redis instance persys-scheduler
	// publishes usage events to.
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// Stream / consumer group. StreamName MUST match persys-scheduler's
	// METER_REDIS_STREAM (default on both sides: "persys:usage:stream").
	StreamName    string
	ConsumerGroup string
	// ConsumerName should be unique per running instance/pod. Defaults to
	// "<hostname>-<pid>" if not set, which is normally enough for k8s
	// deployments (pod hostname is already unique).
	ConsumerName string

	// XREADGROUP tuning.
	ReadCount int64         // COUNT: max messages to pull per read
	ReadBlock time.Duration // BLOCK: how long to wait for new messages

	// XAUTOCLAIM tuning - recovers messages left pending by a consumer that
	// crashed or hung before ack'ing (e.g. a killed pod).
	ClaimMinIdle  time.Duration
	ClaimInterval time.Duration

	// Number of concurrent XREADGROUP worker goroutines.
	Workers int

	// Dedup guard TTL. Each event_id gets a Redis SETNX key with this TTL
	// before being handed to the batch writer; a redelivered/duplicate event
	// is recognized and acked without being written twice.
	DedupeTTL time.Duration

	// Batch writer tuning - ClickHouse strongly prefers batched inserts over
	// one-row-at-a-time. A batch flushes when either threshold is hit first.
	BatchSize          int
	BatchFlushInterval time.Duration

	// ClickHouse connection.
	ClickHouseAddr     string
	ClickHouseDatabase string
	ClickHouseUsername string
	ClickHousePassword string
	ClickHouseTLS      bool
	// RetentionDays controls the TTL clause on the usage_events table.
	// Metering/billing use cases typically need longer retention than raw
	// ops telemetry - tune this to your billing cycle / audit requirements.
	RetentionDays int

	// HTTP server exposing /healthz, /readyz, and /metrics (Prometheus).
	HealthAddr string

	// HTTP query API - GET /v1/workloads, /v1/workloads/{id}/history, etc.
	// Deliberately a separate address/port from HealthAddr: health/metrics
	// are typically scraped by cluster infra, while this API is meant to be
	// called by application logic (a billing service, or persys-scheduler
	// making a quota decision) - keeping them separate lets you apply
	// different network policy/auth to each.
	APIAddr string
	// APIToken, if set, is required as "Authorization: Bearer <token>" on
	// every API request. Empty disables auth entirely - fine for local
	// dev, but put this behind your existing internal network/mTLS
	// boundary (or set a token) for anything else. See README.
	APIToken string

	// CacheMaxAge bounds how stale the in-memory latest-usage cache entry
	// for a workload can be and still be considered "live" - used by both
	// the API's live endpoints and the per-workload Prometheus metrics, so
	// the two agree on what counts as live.
	CacheMaxAge time.Duration
	// CachePruneInterval controls how often stale cache entries are
	// actually evicted (freeing memory for workloads that will never
	// report again, e.g. deleted ones) - independent of CacheMaxAge, which
	// only controls what's *returned*, not what's *retained*.
	CachePruneInterval time.Duration
}

// Load reads configuration from the environment, applying defaults for
// anything unset.
func Load() (*Config, error) {
	consumerName := envOr("METER_CONSUMER_NAME", "")
	if consumerName == "" {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "persys-meter"
		}
		consumerName = fmt.Sprintf("%s-%d", host, os.Getpid())
	}

	cfg := &Config{
		RedisAddr:     envOr("REDIS_ADDR", "redis:6379"),
		RedisPassword: envOr("REDIS_PASSWORD", ""),
		RedisDB:       envIntOr("REDIS_DB", 1),

		StreamName:    envOr("METER_REDIS_STREAM", "persys:usage:stream"),
		ConsumerGroup: envOr("METER_CONSUMER_GROUP", "persys-meter"),
		ConsumerName:  consumerName,

		ReadCount: int64(envIntOr("METER_READ_COUNT", 200)),
		ReadBlock: envDurationOr("METER_READ_BLOCK", 5*time.Second),

		ClaimMinIdle:  envDurationOr("METER_CLAIM_MIN_IDLE", 30*time.Second),
		ClaimInterval: envDurationOr("METER_CLAIM_INTERVAL", 15*time.Second),

		Workers: envIntOr("METER_WORKERS", 4),

		DedupeTTL: envDurationOr("METER_DEDUPE_TTL", 24*time.Hour),

		BatchSize:          envIntOr("METER_BATCH_SIZE", 500),
		BatchFlushInterval: envDurationOr("METER_BATCH_FLUSH_INTERVAL", 2*time.Second),

		ClickHouseAddr:     envOr("CLICKHOUSE_ADDR", "clickhouse:9000"),
		ClickHouseDatabase: envOr("CLICKHOUSE_DATABASE", "persys"),
		ClickHouseUsername: envOr("CLICKHOUSE_USERNAME", "default"),
		ClickHousePassword: envOr("CLICKHOUSE_PASSWORD", "persys"),
		ClickHouseTLS:      envBoolOr("CLICKHOUSE_TLS", false),
		RetentionDays:      envIntOr("METER_RETENTION_DAYS", 90),

		HealthAddr: envOr("METER_HEALTH_ADDR", ":9091"),

		APIAddr:  envOr("METER_API_ADDR", ":9092"),
		APIToken: envOr("METER_API_TOKEN", ""),

		CacheMaxAge:        envDurationOr("METER_CACHE_MAX_AGE", 5*time.Minute),
		CachePruneInterval: envDurationOr("METER_CACHE_PRUNE_INTERVAL", 2*time.Minute),
	}

	if cfg.Workers < 1 {
		return nil, fmt.Errorf("METER_WORKERS must be >= 1, got %d", cfg.Workers)
	}
	if cfg.BatchSize < 1 {
		return nil, fmt.Errorf("METER_BATCH_SIZE must be >= 1, got %d", cfg.BatchSize)
	}
	if strings.TrimSpace(cfg.StreamName) == "" {
		return nil, fmt.Errorf("METER_REDIS_STREAM must not be empty")
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func envBoolOr(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
