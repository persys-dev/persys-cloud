# persys-meter

Consumes per-workload resource usage events published by `persys-scheduler`
onto a Redis Stream, stores them durably in ClickHouse, and exposes them
three ways: live Prometheus metrics, a JSON query API, and raw historical
rows in ClickHouse. It's the piece that makes historical/aggregated
per-workload usage queryable at all - `compute-agent` and `persys-scheduler`
only ever expose the *latest* sample.

## Where this fits

```
compute-agent  --(heartbeat, real per-workload usage)-->  persys-scheduler
                                                                  |
                                                     XADD persys:usage:stream
                                                                  |
                                                                  v
                                                            persys-meter
                                                       (this service)
                                                    /        |         \
                                      Prometheus  /metrics   |   JSON API
                                      (live, per-workload)   |   (:9092)
                                                              v
                                                          ClickHouse
                                                      (durable history)
```

`persys-meter` is a Redis Streams **consumer group**: run as many replicas
as you want with the same `METER_CONSUMER_GROUP`, and Redis hands each one a
different slice of the stream automatically. Nothing here talks directly to
`compute-agent` or `persys-scheduler` beyond reading the stream they already
publish to.

## What this service is - and isn't

**Is:** the accurate, queryable record of what every workload has actually
consumed, live and historical. This is the necessary input to billing and
quota enforcement.

**Isn't:** a billing engine or a quota enforcer. It doesn't define price
tiers, quota limits, or what happens when a limit is exceeded (deny new
workloads? throttle? alert?) - those are product decisions that belong in
whatever service makes admission/billing decisions (`persys-scheduler`, or a
dedicated billing service), consuming this service's API. Baking a specific
quota policy in here would mean guessing at a business model this service
has no way to know.

## Running it

Needs a reachable Redis (the same one `persys-scheduler` publishes to) and a
ClickHouse server/cluster.

```bash
export REDIS_ADDR=localhost:6379
export CLICKHOUSE_ADDR=localhost:9000
make run
```

The `usage_events` table is created automatically on startup if it doesn't
exist (see `internal/store/clickhouse.go`).

### Docker

```bash
make docker          # builds persys-dev/persys-meter:latest
docker run --rm \
  -e REDIS_ADDR=redis:6379 \
  -e CLICKHOUSE_ADDR=clickhouse:9000 \
  -p 9091:9091 -p 9092:9092 \
  persys-dev/persys-meter:latest
```

## Configuration

Everything is an env var; every one has a default suitable for local dev
against `localhost` services.

| Variable | Default | Notes |
|---|---|---|
| `REDIS_ADDR` | `127.0.0.1:6379` | Must be the same Redis `persys-scheduler` publishes to |
| `REDIS_PASSWORD` | _(empty)_ | |
| `REDIS_DB` | `0` | |
| `METER_REDIS_STREAM` | `persys:usage:stream` | **Must match** persys-scheduler's `METER_REDIS_STREAM` |
| `METER_CONSUMER_GROUP` | `persys-meter` | Shared across all replicas |
| `METER_CONSUMER_NAME` | `<hostname>-<pid>` | Should be unique per running instance |
| `METER_READ_COUNT` | `200` | XREADGROUP COUNT (messages per read) |
| `METER_READ_BLOCK` | `5s` | XREADGROUP BLOCK duration |
| `METER_CLAIM_MIN_IDLE` | `30s` | XAUTOCLAIM: how long a message can sit pending before being reclaimed |
| `METER_CLAIM_INTERVAL` | `15s` | How often the reclaim sweep runs |
| `METER_WORKERS` | `4` | Concurrent XREADGROUP goroutines |
| `METER_DEDUPE_TTL` | `24h` | How long an event_id is remembered to catch redeliveries |
| `METER_BATCH_SIZE` | `500` | Max records per ClickHouse insert |
| `METER_BATCH_FLUSH_INTERVAL` | `2s` | Max time a partial batch waits before flushing |
| `CLICKHOUSE_ADDR` | `127.0.0.1:9000` | Native protocol port |
| `CLICKHOUSE_DATABASE` | `persys` | |
| `CLICKHOUSE_USERNAME` | `default` | |
| `CLICKHOUSE_PASSWORD` | _(empty)_ | |
| `CLICKHOUSE_TLS` | `false` | |
| `METER_RETENTION_DAYS` | `90` | TTL on `usage_events` - **tune this to your actual billing/audit retention needs**, 90 is a placeholder |
| `METER_HEALTH_ADDR` | `:9091` | Serves `/healthz`, `/readyz`, `/metrics` |
| `METER_API_ADDR` | `:9092` | Serves the JSON query API (see below) |
| `METER_API_TOKEN` | _(empty)_ | If set, required as `Authorization: Bearer <token>` on every API request. Empty = no auth |
| `METER_CACHE_MAX_AGE` | `5m` | How stale a workload's cached sample can be and still be considered "live" (affects both the API and Prometheus metrics) |
| `METER_CACHE_PRUNE_INTERVAL` | `2m` | How often stale cache entries are actually freed from memory |

## Metrics (`GET :9091/metrics`)

Two distinct sets, both under the `persys_meter_*` namespace:

**Actual workload metrics** - what you almost certainly want for dashboards
and alerting, one series per currently-live workload:

| Metric | Type | Meaning |
|---|---|---|
| `persys_meter_workload_cpu_percent{workload_id,node_id,workload_type}` | gauge | Latest CPU utilization % |
| `persys_meter_workload_memory_bytes{...}` | gauge | Latest resident memory usage |
| `persys_meter_workload_disk_read_bytes_total{...}` | counter | Cumulative disk bytes read (resets if the workload restarts) |
| `persys_meter_workload_disk_write_bytes_total{...}` | counter | Cumulative disk bytes written |
| `persys_meter_workload_net_rx_bytes_total{...}` | counter | Cumulative network bytes received |
| `persys_meter_workload_net_tx_bytes_total{...}` | counter | Cumulative network bytes transmitted |
| `persys_meter_workload_sample_age_seconds{...}` | gauge | Seconds since the last sample - alert on this being persistently high to catch an agent that's stopped reporting |

These are emitted by a custom Prometheus collector reading the shared
in-memory cache (`internal/cache`), so a workload that stops reporting
simply disappears from scrapes after `METER_CACHE_MAX_AGE` rather than
leaving a stale series behind forever.

**Pipeline health metrics** - about persys-meter itself, not workloads:

| Metric | Meaning |
|---|---|
| `persys_meter_events_consumed_total{stream}` | Messages read via XREADGROUP |
| `persys_meter_events_deduped_total{stream}` | Skipped as a redelivery |
| `persys_meter_events_parse_failed_total{stream}` | Malformed payloads (logged at Error level too) |
| `persys_meter_events_written_total{stream}` | Successfully written to ClickHouse |
| `persys_meter_batch_write_failed_total{stream}` | Failed batch writes (left un-acked for retry) |
| `persys_meter_messages_reclaimed_total{stream}` | Recovered via XAUTOCLAIM from a stalled consumer |
| `persys_meter_batch_flush_duration_seconds{stream,result}` | ClickHouse write latency |
| `persys_meter_batch_size{stream}` | Records per flushed batch |
| `persys_meter_consumer_lag_seconds{stream}` | Time between an event being reported and processed |

## Query API (`:9092`)

JSON over HTTP. Set `METER_API_TOKEN` and send `Authorization: Bearer
<token>` in production - unauthenticated by default for local dev only.

| Endpoint | Description |
|---|---|
| `GET /v1/workloads` | Every live workload's latest sample. `?workload_type=container\|vm` to filter |
| `GET /v1/workloads/{id}` | Latest sample for one workload. `404` if none within `METER_CACHE_MAX_AGE` |
| `GET /v1/workloads/{id}/history?from=&to=&limit=` | Raw historical samples from ClickHouse, newest first. `from`/`to` are RFC3339, default to the last 7 days |
| `GET /v1/workloads/{id}/summary?from=&to=` | Aggregated usage over a window - see below |
| `GET /v1/nodes/{id}/workloads` | Live workloads on a given node |

### Example: usage summary

```
GET /v1/workloads/wl-abc123/summary?from=2026-07-01T00:00:00Z&to=2026-07-08T00:00:00Z
```

```json
{
  "workload_id": "wl-abc123",
  "from": "2026-07-01T00:00:00Z",
  "to": "2026-07-08T00:00:00Z",
  "sample_count": 40320,
  "avg_cpu_percent": 12.4,
  "max_cpu_percent": 87.1,
  "avg_memory_bytes": 268435456,
  "max_memory_bytes": 402653184,
  "disk_read_bytes_delta": 1073741824,
  "disk_write_bytes_delta": 536870912,
  "net_rx_bytes_delta": 209715200,
  "net_tx_bytes_delta": 104857600,
  "first_sample": "2026-07-01T00:00:03Z",
  "last_sample": "2026-07-07T23:59:58Z",
  "estimated_cpu_core_seconds": 75277.44
}
```

**Read the caveats before wiring this into anything that charges money:**

- `*_bytes_delta` fields are `max(counter) - min(counter)` over the window.
  Since the underlying counters are cumulative *since the workload's runtime
  started* (see `compute-agent`), a restart during the window resets them to
  zero, which understates (or even negatives) the delta. This isn't
  silently patched over - it's a known limitation documented on
  `store.UsageSummary`. A more correct version would track restarts
  explicitly and sum deltas between them.
- `estimated_cpu_core_seconds` is `avg(cpu_percent)/100 * window_seconds` -
  a simple approximation, not a rigorous integral over irregularly spaced
  samples. Fine as a first cut; revisit if billing needs tighter accuracy.
- `sample_count: 0` means no data was reported in the window at all - it
  does **not** mean usage was zero. Every other field will also be
  zero-valued in that case; check `sample_count` first.

## Known limitations / things to revisit

- **Cross-repo schema coupling.** `internal/ingest/event.go` hand-mirrors
  the JSON shape `persys-scheduler` publishes (`internal/scheduler/usage_stream.go`
  and its `models.WorkloadUsage`). The two aren't shared via a common
  package (separate Go modules) - if the scheduler's shape changes, this
  file needs a matching manual update, and nothing will catch a drift at
  compile time.
- **Dedup is TTL-bounded** (`METER_DEDUPE_TTL`, default 24h). A duplicate
  delivery arriving after the TTL expires would be double-counted. In
  practice redeliveries happen within seconds-to-minutes of
  `METER_CLAIM_MIN_IDLE`, not hours, so this is a deliberate, bounded
  tradeoff, not an oversight.
- **Retention default (90 days) is a placeholder** - set
  `METER_RETENTION_DAYS` to match your actual billing/audit cycle.
- **Not compiled in the environment that generated it.** Every non-trivial
  third-party API call here (`go-redis` streams commands, `clickhouse-go`'s
  `Query`/`QueryRow`/`PrepareBatch`, `client_golang`'s custom Collector) was
  checked against the real tagged source rather than written from memory,
  but this has not been run through `go build`/`go vet`. Run `make tidy &&
  make build && make vet` before deploying.

## Development

```bash
make help    # list targets
make tidy    # resolve dependencies, generate go.sum (needed once, and after any go.mod edit)
make build   # compile to bin/persys-meter
make test    # go test ./... -race -cover
make lint    # gofmt + go vet
```
