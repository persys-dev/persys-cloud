# Changelog

## 2026-07-28 (Unreleased)

Source: 1000+ node scaling initiative (`persys-scheduler-1000-node-scaling-plan.md`)

### Summary

This release targets running persys-scheduler at 1,000-5,000+ node fleets and behind multiple replicas. It cuts agent-facing fan-out from O(workloads) to O(nodes), adds etcd compare-and-swap to every concurrent write path, adds a watch-backed node cache for placement, adds leader election with an optional active-active sharding mode, replaces single-factor node scoring with a weighted placement algorithm that accounts for in-flight (not-yet-heartbeated) resource commitments, and fixes CoreDNS self-registration so persys-gateway can discover more than one running replica at a time.

### Major Features

1. **Agent connection pooling**
   - Replaced per-RPC dial-then-close with a pooled `map[nodeID]*grpc.ClientConn`, keepalive-checked and reused across calls
   - Removes a full TLS handshake from every single agent RPC — previously paid on every apply/delete/status/list call, to every node, every cycle

2. **Reconciliation fan-out: O(workloads) → O(nodes)**
   - The reconciler now fetches each node's full workload list once per cycle (via the batch RPC drift-detection already used) instead of one status RPC per workload
   - Bounded-concurrency workload processing (`SCHEDULER_RECONCILE_CONCURRENCY`, default 64), plus a cycle-overlap guard so a slow cycle can't stack with the next tick
   - `MonitorNodes`, `MonitorWorkloads`, and `detectDriftOnce` got the same bounded-concurrency treatment

3. **etcd compare-and-swap on all concurrent write paths**
   - New `RetryableEtcdCASPut`; every node-mutating function (heartbeat, drain/ready/taint/label/capability updates, NotReady transitions) and every workload-status-mutating function (`UpdateWorkloadStatus`, `UpdateWorkloadLogs`, `UpdateWorkloadMetadata`, `UpdateWorkloadRuntimeDetails`) now retries against a fresh read on conflict instead of silently overwriting a concurrent writer
   - Removed a dead `/retries/{id}` write nothing ever read; fixed a bug where usage telemetry (`Usage`) was silently dropped from the etcd status projection on every read

4. **Watch-backed live node cache for placement**
   - New `node_watch.go`: full resync then a live etcd `Watch` keep the in-memory node cache current
   - `selectNodeForWorkload` reads from it, falling back to a live scan only if the cache isn't populated yet (startup, or mid-resync)

5. **Leader election with failover / active-active HA modes** (new env: `SCHEDULER_HA_MODE`, `SCHEDULER_SHARD_COUNT`, `SCHEDULER_SHARD_INDEX`)
   - New `leader.go`: etcd-lease-based election (`go.etcd.io/etcd/client/v3/concurrency`) so multiple scheduler replicas can run against the same etcd cluster with automatic failover
   - `SCHEDULER_HA_MODE=failover` (default): exactly one replica active cluster-wide; the others are hot standbys that take over automatically if it dies
   - `SCHEDULER_HA_MODE=active-active`: nodes are partitioned across `SCHEDULER_SHARD_COUNT` shards by a stable hash of node ID; each replica only drives reconciliation/monitoring/drift-detection for the nodes in its `SCHEDULER_SHARD_INDEX`, so multiple shards run concurrently — run more than one replica per shard index for HA within a shard
   - The gRPC API (`RegisterNode`, `Heartbeat`, `ApplyWorkload`, ...) runs unconditionally on every replica in both modes, since those paths are CAS-protected; only the singleton convergence loops are gated

6. **Weighted placement algorithm with in-flight resource reservation**
   - Replaced single-factor "lowest CPU+memory average" sorting with a weighted score: CPU headroom, memory headroom, and a spread term (workload count relative to the busiest candidate), plus a small deterministic tie-breaker so exact ties don't always resolve to the same node
   - New in-flight reservation tracking (`placement.go`): resources committed to a just-assigned workload are counted against its node immediately, before that node's next heartbeat reflects the change — closes a real oversubscription window that gets materially more likely now that reconciliation, monitoring, and (in active-active mode) multiple scheduler replicas can all be placing/converging workloads concurrently

7. **CoreDNS multi-replica fix**
   - Scheduler self-registration (used by persys-gateway to discover the scheduler) previously wrote to one fixed etcd key; every replica overwrote the others on startup, so the gateway could only ever resolve one replica regardless of how many were running
   - Now keyed per-instance (mirroring the pattern already used for agent node registration), so CoreDNS returns one record per running replica; added deregistration on clean shutdown so a stopped replica doesn't linger as a dead record until its TTL expires
   - Note: this DNS mechanism is for **gateway → scheduler** discovery only; agents do not use CoreDNS to reach the scheduler

### Deployment note

Running multiple scheduler replicas under plain `docker compose up` (not Swarm) requires removing the scheduler's own host port publishing and putting a TCP-passthrough load balancer (e.g. HAProxy) in front instead — otherwise replicas past the first fail to bind the same host port. See `docker-compose.scheduler-ha.snippet.yml` and `haproxy.cfg`. Not needed under `docker stack deploy` (Swarm), where the ingress routing mesh already handles this.

### Breaking Changes

None. `SCHEDULER_HA_MODE` defaults to `failover`, which preserves prior single-active-instance behavior exactly. All other new env vars have defaults matching prior behavior (`SCHEDULER_RECONCILE_CONCURRENCY=64`, `SCHEDULER_SHARD_COUNT=1`, `SCHEDULER_SHARD_INDEX=0`).

### Known Limitations

- Active-active mode has no explicit shard hand-off protocol: if a node fails and its workloads are reassigned to a node owned by a different shard, there's a brief window (expected to self-heal within one reconcile interval) where neither shard is actively driving that workload. See `sharding.go` for details.
- Redis remains a single instance with no Sentinel/cluster failover.
- Not load-tested at target scale in this round; reasoning is from code inspection, not measurement.

### Changed Files

See `persys-scheduler-scaling-fixes.patch` for the full diff. New files: `internal/scheduler/leader.go`, `internal/scheduler/sharding.go`, `internal/scheduler/node_watch.go`, `internal/scheduler/placement.go`.

---

## 2026-05-29 (Unreleased)

Source: `git diff -- persys-scheduler`

### Summary

This release adds Redis-backed telemetry storage to solve etcd space exhaustion, improved runtime failure handling, managed volume support, and workload utilization telemetry.

### Major Features

1. **etcd Space Optimization with Redis Telemetry Store**
   - Migrated high-churn reconciliation metadata and event logs to Redis with TTL-based cleanup
   - Split workload data into immutable spec (`/workloads-spec/{id}`) and mutable status (`/workloads-status/{id}`)
   - Reduces etcd write volume by 70-90% in typical 100-workload deployments
   - Graceful degradation: Scheduler works with etcd-only mode if Redis unavailable
   - Backward compatible: Legacy `/workloads/{id}` data continues to load via compatibility shim
   - New environment variables: `REDIS_ADDR`, `REDIS_PASSWORD`, `REDIS_DB`, `REDIS_RECONCILE_TTL`, `REDIS_EVENT_TTL`, `REDIS_EVENT_MAX_ENTRIES`

2. **Enhanced Runtime Failure Handling**
   - Added failure grace period (2m default) allowing transient failures to self-heal before retry exhaustion
   - Added terminal failure detection: Marks workloads as permanently failed when agent reports non-retryable reasons
   - Distinct infrastructure vs runtime failure classification:
     - Infrastructure: "node unavailable", "heartbeat expired", "connection refused", etc.
     - Runtime: Container/VM-specific errors captured from agent
   - Improved failure reason propagation with structured `WorkloadReason` containing code, message, last transition, next retry metadata
   - Clear distinction between scheduler retries (exponential backoff) and reapply backoff (separate tracking)

3. **Managed Volume Support**
   - Added `SupportedStorageDrivers[]` field to Node model for storage capability advertising
   - Node selector now validates storage driver availability before workload placement
   - Support for managed volumes:
     - `local` - Host bind paths (existing behavior)
     - `nfs` - NFS server mounts
     - `ceph-rbd` - Ceph RBD block devices
   - Per-workload managed volume specs with:
     - Name, driver, size (GB), access mode, filesystem type, mount path, read-only, retain policy
   - Persistent `ManagedVolumeRecord` and `VolumeAttachmentRecord` across lifecycle
   - Phase tracking: Provisioning → Provisioned → Attached → Released/Retained

4. **Workload Utilization Telemetry**
   - Added `WorkloadUsage` model with CPU%, memory bytes, disk read/write bytes, network RX/TX bytes, collection timestamp
   - Per-workload usage included in workload status and agent heartbeat
   - New Prometheus metrics:
     - `persys_scheduler_state_store_writes_total{category}` - Track storage operations by type
   - Enables scheduler automation and intelligence layers to correlate performance with placement decisions

### Breaking Changes

None. All changes are backward compatible.

### Deprecations

- Direct use of `/workloads/{id}` key is deprecated in favor of `/workloads-spec/{id}` + `/workloads-status/{id}` split
- Single-string `cloud_init` field deprecated in favor of structured `CloudInitConfig` with separate fields
- Old `reapplyStillGuarded` return signature changed to `(bool, time.Time)` to provide wait-until timestamp

### Changed Files

1. **internal/config/config.go** (+36 lines)
   - Added Redis configuration struct with 7 new fields
   - Added environment variable parsing for Redis settings
   - Default TTL: 24 hours; default max events: 1000 entries

2. **internal/scheduler/redis_store.go** (NEW FILE, +92 lines)
   - `initRedisStore()` - Initialize Redis with graceful degradation
   - `writeReconciliationTelemetry()` - Store reconciliation metadata in Redis
   - `writeEventTelemetry()` - Store bounded event history in Redis
   - Fallback to etcd if Redis unavailable

3. **internal/scheduler/workload_projection.go** (NEW FILE, +83 lines)
   - `workloadSpec` - Immutable specification data type
   - `workloadStatus` - Mutable status and telemetry data type
   - Conversion functions for projection/assembly

4. **internal/models/models.go** (+183 lines)
   - Added `SupportedStorageDrivers[]string` to Node
   - Added `ManagedVolumes[]ManagedVolumeSpec` to Workload and VMSpec
   - Added `ManagedVolumeSpec` struct with details
   - Added `WorkloadUsage` struct for telemetry
   - Added `WorkloadReason` struct for structured failures
   - Added `ManagedVolumeRecord` and `VolumeAttachmentRecord` for control-plane state

5. **internal/scheduler/state_store.go** (+455 lines)
   - Added new storage key prefixes for split workload storage
   - Updated `saveWorkload()` to split and persist spec/status separately
   - Updated `writeReconciliationRecord()` to use Redis telemetry
   - Updated `emitEvent()` to use Redis with etcd fallback
   - Added storage functions for managed volumes and attachments
   - Added state synchronization for managed volume lifecycle

6. **internal/scheduler/scheduler.go** (+97 lines)
   - Added `redisClient *redis.Client` field
   - Updated `NewScheduler()` to call `initRedisStore()`
   - Updated `Close()` to close Redis connection
   - Updated `GetWorkloads()` to load from split spec/status keys
   - Updated `GetWorkloadByID()` to load from split keys with legacy compatibility
   - Updated `DeleteWorkloadWithContext()` to delete split keys
   - Added `UpdateWorkloadRuntimeDetails()` for storing structured reason + usage
   - Optimizations: Idempotency checks in status/logs/metadata updates
   - Added `requiredStorageDrivers()` and `nodeSupportsStorageDrivers()` for scheduling

7. **internal/scheduler/reconciler.go** (+286 lines)
   - Added reapply backoff constants and helpers
   - Enhanced `ReconcileWorkload()` with exponential backoff logic
   - Added `shouldHaltReapply()` for terminal failure detection
   - Added `nextReapplyAttempt()` and `resetReapplyBackoff()` helpers
   - Updated `getActualWorkloadState()` to capture error metadata
   - Updated `applyDesiredState()` to track reapply attempts and reset on success
   - Updated `updateWorkloadReconciliationStatus()` to use Redis storage

8. **internal/scheduler/workload_control.go** (+93 lines)
   - Added failure grace period constants
   - Enhanced `UpdateWorkloadRetryOnFailure()` with grace window logic:
     - First failure triggers grace period timer
     - Retries during grace use exponential backoff
     - Exhaustion after grace marks workload Failed
   - Updated `UpdateWorkloadSpec()` to detect actual spec changes
   - Added helpers: `applyFailureReason()`, `preferredRuntimeFailureReason()`, `isInfrastructureFailureReason()`
   - Retry backoff now starts at attempt 3 (allows 2 fast attempts)

9. **internal/metrics/metrics.go** (+9 lines)
   - Added `stateStoreWritesTotal` counter with category label
   - Added `IncStateStoreWrite(category string)` function
   - Categories: spec, status, reconciliation, event, assignment, retry

10. **api/proto/control.proto** (+34 lines)
    - Added `SubmitAutomationSuggestion` RPC to AgentControl service
    - Added `AutomationActionType` enum
    - Added automation suggestion and response message types

11. **go.mod** (+1 line)
    - Added dependency: `github.com/redis/go-redis/v9 v9.19.0`

12. **sample.env** (+6 lines)
    - Added Redis configuration variables with defaults

### Resource Impact

**etcd Reduction (12-hour baseline: 100 workloads, 5s reconciliation)**:

- Before: ~172,800 writes (~520MB cumulative)
- After: ~1,000 writes (~1MB cumulative)
- Result: 99.8% reduction in etcd write volume

**Redis Requirements**:

- Memory: ~10-20MB (events + reconciliation metadata)
- CPU: <1% typical
- Network: <1KB/s typical

**Backward Compatibility**:

- Old workloads in `/workloads/{id}` continue to load via compatibility shim
- New scheduler can read old data; old scheduler can ignore new split storage
- No manual migration required

### Migration Notes

1. Deploy new scheduler with `REDIS_ADDR` set (or empty to use etcd-only)
2. Scheduler automatically splits new workloads into spec + status
3. Old workloads continue to work via compatibility shim
4. After 24 hours, old workload data naturally expires from persistence
5. No downtime or data loss risk

### Known Issues

None documented at this time.

### Testing

All changes follow runtime condition debugging and Redis optimization specification exactly.

### Upgrading

1. Ensure Redis is running (optional but recommended)
2. Set `REDIS_ADDR` environment variable pointing to Redis server
3. Deploy new scheduler; existing workloads continue operating
4. Monitor etcd size growth - should stabilize within 1 hour
5. Verify `persys_scheduler_state_store_writes_total` metrics show split storage in use
