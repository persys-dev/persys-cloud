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

API server must not issue runtime execution directly. Runtime actions are emitted via scheduler workers.

Minimum API surface:

- `POST /workloads`
- `PUT /workloads/{id}`
- `DELETE /workloads/{id}`
- `GET /workloads/{id}`
- `GET /workloads`
- `GET /nodes`
- `POST /workloads/{id}/retry`

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

### 4.3 Node Manager

Responsibilities:

- Node registration
- Heartbeat processing
- Capacity accounting
- Node readiness transitions
- Eviction trigger when node is unhealthy

Node record (canonical shape):

```json
{
  "id": "node-abc",
  "status": "Ready | NotReady | Draining",
  "resources": {
    "cpu_total": 16,
    "cpu_allocated": 8,
    "memory_total": 32768,
    "memory_allocated": 16000,
    "disk_total": 500,
    "disk_allocated": 200
  },
  "last_heartbeat": "2026-02-17T00:00:00Z",
  "labels": {
    "rack": "rack-1",
    "zone": "zone-a"
  }
}
```

Health policy:

- Missing heartbeat for 3 intervals -> `NotReady`
- `NotReady` beyond grace -> workload eviction/reschedule

### 4.4 Placement Engine

Inputs:

- Workload resource requests
- Node available capacity
- Node labels and readiness

Algorithm (V1):

1. Filter nodes by:
   - `Ready`
   - CPU capacity
   - Memory capacity
   - Disk capacity
   - Label constraints
2. Sort by ascending utilization
3. Choose first node
4. Persist assignment and decision metadata

### 4.5 Reconciliation Engine

Loop interval: default 5 seconds (configurable).

For each workload:

1. Load desired state from etcd
2. Query agent actual state (`GetWorkloadStatus`)
3. Compare desired vs actual
4. Choose action
5. Execute action (`ApplyWorkload`/`DeleteWorkload`)
6. Persist result and timestamps

Decision matrix:

| Desired | Actual  | Action   |
|---------|---------|----------|
| Running | Missing | Apply    |
| Running | Stopped | Apply    |
| Running | Failed  | Recreate |
| Stopped | Running | ApplyStopped |
| Deleted | Exists  | Delete   |
| Running | Running | NoAction |

Rules:

- Do not execute before grace period expires for transitional states.
- Persist reconciliation metadata for every action.
- No tight retry loops inside one cycle.

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

Event types:

- `WorkloadScheduled`
- `WorkloadFailed`
- `NodeLost`
- `RetryTriggered`
- `Rescheduled`

Rules:

- Events are immutable
- Events are append-only
- Include workload id, node id, reason, timestamp

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

- Worker pool for reconciliation actions
- Context cancellation for all loops and RPCs
- Optimistic etcd writes where feasible
- Avoid global locks across reconciliation workers

## 8. High Availability (Future)

- Multiple stateless scheduler replicas
- Shared etcd state
- Leader election via etcd lease
- Only leader executes reconciliation and retries

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
