# Persys Scheduler Reconciliation and Scheduling Deep Dive

## Scope

This document describes the **current implemented behavior** of scheduling and reconciliation in `persys-scheduler`.

It covers:

- control-plane APIs and state writes
- node registration and health lifecycle
- placement decisions
- reconciliation loop decisions and actions
- failover and retry behavior
- runtime mismatch handling
- known implementation gaps

## Components and Responsibilities

- `internal/grpcapi/service.go`
  - Accepts control-plane RPCs (`RegisterNode`, `Heartbeat`, `ApplyWorkload`, `DeleteWorkload`, `RetryWorkload`)
  - Converts API payloads to internal models
  - Persists desired state via scheduler services
  - Triggers immediate reconciliation on `ApplyWorkload`
- `internal/scheduler/scheduler.go`
  - Node/workload CRUD against etcd
  - Node selection and assignment
  - Node health monitor loop
  - Reconciliation loop startup
- `internal/scheduler/reconciler.go`
  - Compares desired vs actual state
  - Handles apply/stop/delete decisions
  - Handles failover and runtime mismatch recovery
- `internal/scheduler/agent_grpc.go`
  - Scheduler -> Agent gRPC client (apply/delete/status/actions)
  - Dial policy and error classification
- `internal/scheduler/node_control.go`
  - Heartbeat updates
  - NotReady transitions and status provenance fields

## etcd State Layout

State keys:

- `/nodes/<node-id>`
- `/nodes/<node-id>/status`
- `/workloads/<workload-id>`
- `/assignments/<workload-id>`
- `/reconciliation/<workload-id>`
- `/retries/<workload-id>`
- `/events/<event-id>`

Where mapping lives:

- Primary: workload record fields `nodeId` and `assignedNode`
- Index: assignment record in `/assignments/<workload-id>`

## Node Lifecycle

### Registration

RPC: `RegisterNode`

Behavior:

- Requires `node_id` and now requires valid `grpc_endpoint` (`host:port`)
- Stores endpoint in node model as `agentEndpoint`
- Stores capabilities including `supported_workload_types`
- Initializes status metadata:
  - `status=Ready`
  - `statusReason=registered`
  - `statusUpdatedBy=register`
  - `statusUpdatedAt=<now>`
- Persists node and `/status` shadow key

### Heartbeat

RPC: `Heartbeat`

Behavior:

- Updates `lastHeartbeat`
- Sets status to `Ready`
- Updates node availability from usage (`total - used`)
- Updates status provenance on transition/recovery:
  - `statusReason=heartbeat received` or transition string
  - `statusUpdatedBy=heartbeat`
  - `statusUpdatedAt=<now>`

### Node health monitor

Loop: every 1 minute in `MonitorNodes`

Behavior:

- If `time.Since(lastHeartbeat) > 3m`, marks node `NotReady`
- Writes status provenance:
  - `statusReason=heartbeat expired: last heartbeat <ts>`
  - `statusUpdatedBy=monitor`
  - `statusUpdatedAt=<now>`

## Scheduling Logic

Entry points:

- Immediate path: `ApplyWorkload` RPC calls `Create/Update` then `ReconcileWorkload`
- Periodic path: reconciliation loop every `SCHEDULER_RECONCILE_INTERVAL` (default 5s)

### Placement (`selectNodeForWorkload`)

Node filters in order:

- Skip `/status` subkeys
- `status` must be `Ready` or `Active`
- heartbeat freshness <= 10 minutes
- labels must match workload labels
- node must support workload type
  - Uses `supportedWorkloadTypes` if present
  - VM strict fallback for legacy records: requires hypervisor signal
- CPU availability
- memory availability
- disk check currently placeholder (not enforced)

Selection:

- Sort by lowest utilization score: average of CPU and memory used ratios
- Pick first candidate

If no candidate:

- returns detailed rejection reasons per node

### Assignment (`assignWorkload`)

Effects:

- Sets workload `nodeId` and `assignedNode`
- Status becomes `Scheduled`/`Pending`
- Writes workload record
- Writes `/assignments/<workload-id>`
- Emits `WorkloadScheduled` event

## Reconciliation Logic

Loop:

- Reads all workloads from etcd
- Skips `Completed` and `Deleted` status entries
- Reconciles each workload independently

### Per-workload flow (`ReconcileWorkload`)

1. Respect retry backoff

- If `nextRetryAt` is in future, action is `BackoffWait`

2. Finalize deletion when desired `Deleted` and no assigned node

3. Ensure assignment exists (unless deleting)

- If unassigned, run placement and assign

4. Retry due handling

- If retry timer elapsed, clear `nextRetryAt`, set workload `Pending`

5. Assigned node availability pre-check

- If assigned node is unavailable (`NotReady/!Active` or stale heartbeat), attempt failover

6. Fetch actual runtime state from agent (`GetWorkloadStatus`)

- `NotFound`-class errors map to `Missing`
- Unreachable errors (dial/DNS/unavailable) trigger:
  - mark assigned node `NotReady`
  - failover attempt in same cycle

7. Compare desired vs actual

- Desired `Running` and actual in `{Missing,Stopped,Failed,Unknown}` -> apply running
- Desired `Stopped` and actual in `{Running,Pending,Unknown}` -> apply stopped
- Desired `Deleted` -> delete path
- Otherwise no action

8. Persist reconciliation metadata

- Writes `/reconciliation/<workload-id>`
- Updates workload metadata fields (`lastReconciliation*`)

### Missing/pending grace

For actual `{Missing,Pending}`, reconciliation can defer action briefly if `lastLaunchTime` is recent.

- Config: `SCHEDULER_MISSING_GRACE_PERIOD`
- Default: 15s

## Retry Engine

On failures via `UpdateWorkloadRetryOnFailure`:

- `attempts++`
- backoff: 5s, 10s, 20s, 40s, ... capped at 2m
- when `attempts >= maxAttempts`:
  - mark workload `Failed`
  - stop retrying

Manual retry:

- `RetryWorkload` sets `nextRetryAt=now`

## Failover Behavior

If assigned node unavailable:

- select another eligible node
- if none available:
  - workload marked for retry backoff (`AwaitFailover` path)
- if new node selected:
  - workload reassigned
  - reschedule event/log written

## Runtime Mismatch Recovery

If apply fails with `runtime not available`:

- scheduler marks assigned node as unsupported for that workload type
- clears workload assignment (`nodeId`/`assignedNode`)
- deletes stale `/assignments/<workload-id>` index
- workload status set to `Pending` for reschedule
- emits reschedule event

## Scheduler -> Agent Routing

Dial target preference:

- First: `node.agentEndpoint` (exact registered `grpc_endpoint`)
- Fallback: `node.ipAddress:agentGrpcPort`

Implication:

- Use routable `grpc_endpoint` (prefer explicit IP) to avoid DNS surprises

## Observability Behavior

Node status provenance fields:

- `statusReason`
- `statusUpdatedBy`
- `statusUpdatedAt`

Reconciliation loop logging:

- Logs cycle summary only when there are actions or failures

Placement rejection logs include exact per-node reason.

## Known Gaps (Current)

- Disk capacity filter is placeholder and not enforcing real disk constraints
- Workload update paths use read-modify-write without etcd CAS; concurrent updates can overwrite fields
- No stream of reconciliation decisions; mostly log/event + etcd inspection
- No tenant quota/rate-limit scheduling policy

## Operational Recommendations

- Ensure every agent sends heartbeat continuously (not just register)
- Register truthful `supported_workload_types`
  - VM-only nodes: include `vm`
  - non-VM nodes: do not include `vm`
- Always register routable `grpc_endpoint` values
- Monitor:
  - node status transitions (`statusReason/by/at`)
  - failover pending reasons
  - workload retry counters and terminal failures

