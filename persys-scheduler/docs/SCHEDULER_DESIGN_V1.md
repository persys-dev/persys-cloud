# Persys Scheduler

## Detailed Engineering Design Specification (V1)

## 1. Purpose

The Persys Scheduler is the control plane for Persys Compute.

Responsibilities:

- Own cluster desired state
- Make placement decisions
- Reconcile desired vs actual state
- Manage retries and backoff
- Manage node lifecycle
- Execute deterministic automation hooks
- Serve as federation boundary (future)

Non-responsibilities:

- Runtime execution (containers/VMs)
- Image building
- Log storage
- Metrics backend
- Agent-side reconciliation

## 2. Design Principles

### 2.1 Single Source of Truth

- Desired state is stored in etcd.
- In-memory state is cache only.
- Recovery from process restart is etcd-driven.

### 2.2 Declarative Control

- User/API declares desired state (`Running`, `Stopped`, `Deleted`).
- Scheduler converges actual state to desired state.

### 2.3 Idempotent Operations

- Scheduler to agent operations are idempotent using `(workload_id, revision_id)`.
- Scheduler can retry safely without duplicating runtime artifacts.

### 2.4 Reconciliation Ownership

- Scheduler owns convergence logic.
- Agents execute RPCs and report local actual state.

### 2.5 Deterministic Behavior

- Placement decision records reason.
- Reconciliation action records reason.
- Retry state is persisted.

## 3. System Architecture

```text
API Gateway
  -> Persys Scheduler
       - API Server
       - Placement Engine
       - Reconciler
       - Retry Engine
       - Node Manager
       - Automation Rules
  -> Persys Agents (gRPC mTLS)
```

## 4. Component Design

### 4.1 API Server

API server responsibilities:

- Validate request payloads
- Persist desired state into etcd
- Trigger scheduling/reconciliation workflows
- Expose read APIs for status and inventory
- Cluster-wide event streaming (gRPC server-streaming)

API server must not issue runtime execution directly. Runtime actions are emitted via scheduler workers. The API surface runs unconditionally on every scheduler replica — write paths are protected by etcd compare-and-swap, making it safe to route agent traffic to any replica.

gRPC API surface:

**Workload Management:**
- `ApplyWorkload` / `DeleteWorkload` / `GetWorkload` / `ListWorkloads`
- `RetryWorkload`

**Node Management:**
- `RegisterNode` / `Heartbeat` / `GetClusterSummary`

**Events (cluster-wide, Redis-backed):**
- `ListEvents(type, workload_id, node_id, limit)` - Historical event replay
- `WatchEvents(type, workload_id, node_id)` - Server-streaming live event feed

**Storage:**
- `CreateDisk` / `ListDisks` / `GetDisk` / `DeleteDisk` (managed volume inventory)
- `CreateBucket` / `ListBuckets` / `GetBucket` / `DeleteBucket` / `GetBucketAccess` / `ListBucketObjects` (Ceph RGW proxy)

### 4.2 State Store (etcd)

Key layout:

```text
/nodes/<node-id>
/workloads/<workload-id>
/assignments/<workload-id>
/reconciliation/<workload-id>
/events/<event-id>
/retries/<workload-id>
```

Workload record (canonical shape):

```json
{
  "id": "workload-123",
  "type": "container | compose | vm",
  "revision_id": "rev-abc",
  "desired_state": "Running | Stopped | Deleted",
  "assigned_node": "node-1",
  "spec": {},
  "resources": {
    "cpu": 2,
    "memory_mb": 2048,
    "disk_gb": 20
  },
  "retry": {
    "attempts": 1,
    "max_attempts": 5,
    "next_retry_at": "2026-02-17T00:00:00Z"
  },
  "status": {
    "actual_state": "Pending | Running | Stopped | Failed | Unknown",
    "last_updated": "2026-02-17T00:00:00Z",
    "failure_reason": ""
  },
  "metadata": {
    "created_at": "2026-02-17T00:00:00Z",
    "last_action": "Apply",
    "last_error": ""
  }
}
```

### 4.3 Node Manager and Node Watch

**Node Manager** is responsible for:
- Node registration (RegisterNode)
- Heartbeat processing (Heartbeat)
- Node status transitions (Ready/NotReady/Draining)
- Node taints and labels (operator control)
- Capacity accounting (used/available CPU, memory)

**Node Watch** is a leader-elected singleton (like reconciliation) that maintains a live in-memory cache of all nodes:
- Performs full resync from etcd (read all `/nodes/*`)
- Establishes live etcd watch stream on `/nodes/*` prefix
- Applies incoming watch events to in-memory map
- Falls back to resync on watch stream errors
- Serves as source of truth for placement decisions (via `candidateNodeSnapshot`)

**Why Node Watch matters:**
- Placement algorithm needs to scan all nodes to pick candidates
- Without a live cache, every placement decision = O(nodes) etcd scan
- With live cache, placement = O(1) in-memory read of pre-populated map
- Cache is rebuilt on leader failover (new leader starts node watch immediately)

**Fallback:**
- If node cache is not yet ready (startup, resync in progress), placement falls back to live etcd scan
- Graceful degradation: scheduling works even during leadership transitions

### 4.4 Placement Engine

The placement engine makes deterministic workload-to-node assignments based on resource availability, labels, and workload-type capabilities.

**Inputs:**
- Workload resource requests (CPU, memory, disk)
- Workload type (container, compose, vm, etc.)
- Workload label constraints
- Node available capacity (from heartbeats, adjusted for in-flight reservations)
- Node status (Ready/NotReady/Draining)
- Node labels and supported workload types

**Algorithm:**

1. **Filter**: Eliminate ineligible nodes:
   - Status is not `Ready` (skip draining, not-ready nodes)
   - Insufficient CPU/memory/disk capacity
   - Labels don't match workload requirements
   - Node doesn't support workload type (container, vm, etc.)
   - Storage driver requirements not met

2. **Score**: For each candidate, compute weighted score:
   - CPU headroom factor (weight 0.4): (available_cpu - in_flight_cpu) / total_cpu
   - Memory headroom factor (weight 0.4): (available_memory - in_flight_memory) / total_memory
   - Spread factor (weight 0.2): how loaded relative to busiest candidate (workload_count / busiest_count)
   - Deterministic tie-breaker: hash(workload_id + node_id) for stable ordering when tied

3. **Assign**: Pick the highest-scoring node, record assignment in etcd, reserve resources in-flight

4. **Reserve**: Immediately record in-memory reservation for just-assigned workload (CPU, memory, expires 90s)
   - Prevents concurrent placement decisions from all reading stale "available" numbers
   - Advisory (soft limit); self-expires via TTL rather than explicit confirmation

**In-Flight Reservations:**

Node `AvailableCPU` and `AvailableMemory` only update via that node's heartbeat, which lags behind placement decisions. When multiple placement decisions (reconciliation, monitoring, multiple replicas in active-active mode) happen concurrently, they can all read the same stale "available" numbers and pick the same "least loaded" node, causing oversubscription before any heartbeat catches up.

In-flight reservations track resources committed to just-assigned workloads and subtract them from a node's effective capacity during scoring. They self-expire after 90 seconds (multiple heartbeat intervals), trading small scoring precision loss for not needing to hook into every status-confirmation path.

### 4.5 Reconciliation Engine

The reconciliation engine continuously converges workload desired state to actual state as reported by agents.

**Execution Model:**

- Leader-elected single instance in failover mode, or shard-partitioned replicas in active-active mode (see section 8)
- Runs every `SCHEDULER_RECONCILE_INTERVAL` (default 5s)
- Bounded concurrency: processes at most `SCHEDULER_RECONCILE_CONCURRENCY` workloads concurrently (default 64)
- Cycle-overlap guard: prevents a slow cycle from stacking with the next tick

**Optimization: O(nodes) instead of O(workloads) agent communication:**

Traditional approach: one `GetWorkloadStatus` RPC per workload, per cycle = O(workloads) fan-out.

New approach:
1. Prefetch snapshots: call `GetWorkloads` once per node (batched list) = O(nodes) fan-out
2. Cache result for the cycle duration
3. Within the cycle, `getActualWorkloadState` consults the snapshot before falling back to a live per-workload RPC
4. Dramatically reduces agent load when workload count >> node count

**Loop interval:** default 5 seconds (configurable via `SCHEDULER_RECONCILE_INTERVAL`).

**For each workload (with bounded concurrency):**

1. Load desired state from etcd (spec + retry metadata)
2. Query agent actual state:
   - First check node snapshot from prefetch (if available)
   - Fall back to live `GetWorkloadStatus` RPC if not in snapshot
3. Compare desired vs actual
4. Choose action (see decision matrix below)
5. Execute action (etcd CAS write + agent RPC)
6. Persist result (status, timestamps, metrics)

**Decision matrix:**

| Desired | Actual  | Action   |
|---------|---------|----------|
| Running | Missing | Apply    |
| Running | Stopped | Apply    |
| Running | Failed  | Recreate |
| Stopped | Running | ApplyStopped |
| Deleted | Exists  | Delete   |
| Running | Running | NoAction |

**Concurrency Control:**

All etcd writes use compare-and-swap (`RetryableEtcdCASPut`):
- Heartbeat updates
- Node drain/ready/taint/label transitions
- Workload status updates (status, logs, metadata, runtime details)
- On conflict, reload from etcd and retry (not silent overwrite)

**Rules:**

- Do not execute before grace period expires for transitional states (`SCHEDULER_MISSING_GRACE_PERIOD`, default 15s)
- Persist reconciliation metadata for every action
- No tight retry loops inside one cycle
- Metrics: track per-workload attempt count, backoff timer, failure reason
- Failed workloads with terminal failure reasons skip retry and transition to `Failed` state

### 4.6 Retry Engine

Retry state is persisted under workload/retry record.

Backoff schedule:

- Base: 5s
- Sequence: 5s, 10s, 20s, 40s
- Cap: 120s

On failure:

- `attempts += 1`
- `next_retry_at = now + backoff`
- stop when `attempts >= max_attempts`
- mark workload `Failed`
- emit failure event

### 4.7 Event System

Events are cluster-wide, immutable, append-only records of state transitions and significant control-plane occurrences. They are **stored in a Redis Stream, not etcd**, to avoid etcd fan-out issues at scale.

**Storage:**
- Single shared Redis Stream across all scheduler replicas
- TTL-based retention (configurable via `REDIS_EVENT_TTL`, default 24h)
- Size-based trimming (approximate, configurable via `REDIS_EVENT_MAX_ENTRIES`, default 1000 entries)

**Why Redis instead of etcd:**
- Events are high-churn data with well-defined retention windows
- etcd is optimized for mutable state; event-only workloads stress etcd unnecessarily
- Redis Streams are purpose-built for event logging with automatic TTL cleanup
- Graceful degradation: if Redis is down, events are dropped (logged) but scheduler continues; if etcd is down, scheduler enters degraded mode

**Event Types:**

Topology:
- `NodeJoined`: agent registered / re-registered
- `NodeLost`: heartbeat timeout, marked NotReady
- `NodeLeft`: deregistered (graceful)

Workload Lifecycle:
- `WorkloadScheduled`: placed on a node
- `WorkloadFailed`: reached terminal failure (max retries exceeded)
- `DriftDetected`: agent state diverged from desired
- `RetryTriggered`: retry backoff timer expired, retrying
- `Rescheduled`: workload moved to different node (same retry attempt)
- `Relocated`: workload moved due to node drain/failure

Operator Control:
- `NodeDraining`: operator requested drain
- `NodeReady`: operator cleared drain
- `NodeTainted`: operator applied taint
- `NodeUntainted`: operator removed taint
- `NodeLabelSet`: operator set label
- `NodeLabelDeleted`: operator deleted label

Control-Plane:
- `SchedulerModeChanged`: transitioned between normal/degraded/recovery
- `LeaderElected`: this instance won leader election
- `LeaderLost`: this instance lost leader election

**Event API:**

- `ListEvents(type, workload_id, node_id, limit)` - Query historical events (oldest-first)
- `WatchEvents(type, workload_id, node_id)` - Server-streaming, replays recent history then follows live events

**Consumption:**
- Dashboard via SSE (HTTP gateway endpoint)
- Alerting systems (watch for WorkloadFailed, NodeLost)
- Audit trails
- Observability: correlate events with metrics/traces

**Rules:**

- Events are immutable after creation
- Events are append-only (no deletion or replay)
- Every event includes: id (UUID), type, workload_id (optional), node_id (optional), reason, timestamp, details (string map)
- Redis stream ID ensures exactly-once delivery across replay-to-live boundary (no gap, no duplicate)

### 4.8 Automation Hooks

Deterministic rule engine only.

Examples:

- If node CPU > 85% for 5 minutes -> emit scale-out webhook
- If workload fails 3 times -> emit alert event

No LLM inference or probabilistic actions.

## 5. Workload Lifecycle

### 5.1 Create

1. API request accepted
2. Validate spec
3. Persist workload (`desired_state=Running`)
4. Placement assigns node
5. Reconciler executes `ApplyWorkload`
6. Persist status transition

### 5.2 Update

1. Persist new `spec` and `revision_id`
2. Mark status `Updating`
3. Reconciler applies update
4. Persist terminal state

### 5.3 Delete

1. Set `desired_state=Deleted`
2. Reconciler sends `DeleteWorkload`
3. Wait until `GetWorkloadStatus` returns not found
4. Remove workload record

## 6. Failure Handling

Agent unreachable:

- Mark node `NotReady`
- Start grace timer
- Evict/reschedule after grace window

Workload failure:

- Persist exact failure reason
- Trigger retry policy
- Mark terminal `Failed` after max retries

Node crash:

- Detect via heartbeat timeout
- Mark workloads for rescheduling
- Re-run placement

## 7. Concurrency Model

The scheduler is designed for safe concurrent operation across multiple replicas.

**Within Single Instance:**
- Background loops (reconciliation, monitoring, drift detection, node watch) use goroutines with context cancellation
- Bounded concurrency for workload/node processing (default 64) prevents resource exhaustion
- Cycle-overlap guard ensures sequential cycles (no interleaving)
- In-memory state (caches, reservations) protected by sync.RWMutex
- etcd is single source of truth; cache is rebuilt on restart

**Across Multiple Replicas:**
- All etcd writes use compare-and-swap (`RetryableEtcdCASPut`)
  - On conflict, reload fresh state and retry, rather than silent overwrite
  - Safe for concurrent writer elimination (only one successfully commits each CAS)
- gRPC API runs on every replica (stateless, safe)
- Background singleton loops (reconciliation, drift, monitoring, node watch) are gated by leader election
  - In failover mode: exactly one replica active cluster-wide
  - In active-active mode: replicas are partitioned by shard; each shard has its own leader

**Agent Communication (Connection Pooling):**
- gRPC connections to agents are pooled (map[nodeID]*grpc.ClientConn)
- Connections are reused across RPCs with keepalive checks
- TLS handshake happens once per unique node (cached until connection failure)
- On TLS errors, cert manager can force rotation + retry automatically
- Eliminates O(RPCs) TLS handshakes that were previously paid on every apply/delete/status call

**Monitoring & Observability:**
- All mutations log comprehensively (node, workload, event)
- Metrics track: attempts, failures, retry counts, latencies
- Traces link placement → reconciliation → agent RPC

## 8. High Availability and Leader Election

Multiple scheduler replicas can run against the same etcd cluster, with automatic failover. Leadership determines which replica drives cluster-wide singleton background loops.

### 8.1 Leader Election

- Uses etcd-lease-based election (`go.etcd.io/etcd/client/v3/concurrency`)
- Session TTL: 15 seconds (balance between fast failover and transient GC/network resilience)
- Winner is determined by etcd atomically
- Lost leader automatically campaigns again after 2s backoff

### 8.2 Failover Mode (Default)

Configuration: `SCHEDULER_HA_MODE=failover` (default)

Behavior:
- All replicas contend for the single `leaderElectionKey` in etcd
- Exactly one replica is ever elected leader cluster-wide
- Leader drives:
  - Reconciliation loops (ReconcileAllWorkloads)
  - Node/workload monitoring (MonitorNodes, MonitorWorkloads)
  - Drift detection (StartDriftDetection)
  - Node watch (StartNodeWatch) - live cache updates
- Standbys (non-leaders) are hot spares:
  - Still run `StartMonitoring()` for per-replica health checks
  - Can handle gRPC API calls (reads, writes with CAS)
  - Will take over if leader dies/loses session

**Example deployment:** 3 replicas, 1 active, 2 standbys. Leader dies → standby wins election within 15s TTL.

### 8.3 Active-Active Mode (Optional Sharding)

Configuration: `SCHEDULER_HA_MODE=active-active`, `SCHEDULER_SHARD_COUNT=N`, `SCHEDULER_SHARD_INDEX=0..N-1`

Behavior:
- Nodes are partitioned by `crc32(nodeID) % SCHEDULER_SHARD_COUNT` hash
- Each replica is assigned a shard index and owns nodes that hash to that index
- Replicas with different shard indices never contend with each other (independent elections per shard)
- Replicas sharing same shard index do elect one leader (for HA within shard)
- Each shard processes its nodes' reconciliation/monitoring/drift independently
- Multiple shards make progress concurrently (unlike failover where only one is active)

**Example deployment:** 5 replicas, 3 shards, 2 replicas per shard index:
- Shard 0: replicas A, B → A elected leader, B is standby for shard 0
- Shard 1: replicas C, D → C elected leader, D is standby for shard 1
- Shard 2: replica E → E is leader of shard 2
- Nodes are distributed: nodes 0,3,6,... → shard 0; nodes 1,4,7,... → shard 1; nodes 2,5,8,... → shard 2
- Three shards process workloads concurrently; higher throughput than failover

**Tradeoff:** Active-active trades slightly higher latency (per-shard overhead) for higher total throughput. Best for large fleets with many nodes.

**Known limitation:** If a node fails and its workloads are reassigned to a node owned by a different shard, there's a brief window (expected to self-heal within one reconcile interval) where neither shard is actively driving that workload. This is a scheduling-latency gap, not a correctness bug. See `sharding.go` for details.

### 8.4 API Availability in Both Modes

The gRPC API (`RegisterNode`, `Heartbeat`, `ApplyWorkload`, etc.) runs unconditionally on every replica in both modes:
- Write paths are protected by etcd compare-and-swap
- Safe for external load balancer to route to any replica
- No leader-gating on API calls
- Scaling: N replicas can handle N× the API throughput (if load-balanced properly)

## 9. Security

- Agent communication via mTLS
- Certificates issued by CFSSL
- Certificate rotation supported
- RBAC enforced at API gateway

## 10. Observability

Endpoints:

- `/metrics`
- `/health`

Core metrics:

- `scheduling_attempts_total`
- `scheduling_failures_total`
- `reconciliation_actions_total`
- `retry_total`
- `node_unhealthy_total`

## 11. Scale Targets

V1 target envelope:

- hundreds of nodes
- thousands of workloads

Scale methods:

- scheduler HA replicas
- etcd clustering
- future sharding/federation

## 12. Out of Scope

Scheduler does not:

- build images
- execute workloads directly
- persist full logs
- reconcile from agent side

## 13. Known Gaps (V1)

- Persistent retry RPC API formalization
- Event streaming API
- Topology-aware placement
- Federation controls
- Tenant-specific rate limiting and quotas

## 14. Acceptance Criteria

A V1 implementation is accepted when:

- all workload actions are persisted before execution
- every scheduler->agent action is idempotent by `revision_id`
- reconciliation converges without hot-looping
- retry state survives restart
- node loss triggers deterministic reschedule flow
- all actions produce structured events
