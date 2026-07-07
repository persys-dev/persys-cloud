# Persys Scheduler Integration Guide (Compute Agent)

## Purpose

This document is a handover guide for implementing Persys Compute Scheduler integration with the compute agent.

It is focused on:

- API contract usage patterns
- state machine and reconciliation semantics
- retry/idempotency behavior
- observability and debugging
- production rollout checklist

---

## 1. Integration Model

The scheduler is the control plane. The agent is the execution plane.

- Scheduler decides placement and desired state.
- Agent executes `ApplyWorkload` / `DeleteWorkload` and reports runtime state.
- Scheduler remains source of intent.
- Agent persists local workload/state for crash recovery and reconciliation.

Key behavior:

- `revision_id` provides idempotency for apply.
- Agent may accept async tasks and initially return `pending`.
- Agent reconciliation loop self-heals runtime drift (including recreate on missing runtime resources).

---

## 2. Agent API Surface

From `api/proto/agent.proto`:

- `ApplyWorkload(ApplyWorkloadRequest) -> ApplyWorkloadResponse`
- `DeleteWorkload(DeleteWorkloadRequest) -> DeleteWorkloadResponse`
- `GetWorkloadStatus(GetWorkloadStatusRequest) -> GetWorkloadStatusResponse`
- `ListWorkloads(ListWorkloadsRequest) -> ListWorkloadsResponse`
- `HealthCheck(HealthCheckRequest) -> HealthCheckResponse`
- `ListActions(ListActionsRequest) -> ListActionsResponse`

### 2.1 Recommended call ordering

For create/update:

1. Scheduler sends `ApplyWorkload`.
2. Scheduler stores request metadata (workload id, revision id, timestamp, node id).
3. If response status is `pending`, scheduler polls `GetWorkloadStatus`.
4. Scheduler optionally queries `ListActions` for traceability (`workload_id` + `newest_first=true`).
5. Scheduler commits final state in control-plane DB once terminal state is observed.

For delete:

1. Scheduler sends `DeleteWorkload`.
2. Scheduler polls `GetWorkloadStatus`.
3. Treat `status not found` as terminal deletion success.

---

## 3. Workload Identity and Idempotency

Use stable keys:

- `id`: globally unique workload instance identifier (stable across retries).
- `revision_id`: increment or content-hash on any spec change.

Agent behavior:

- Same `id` + same `revision_id` => apply is skipped (`skipped=true`).
- Same `id` + different `revision_id` => recreate path.

Scheduler rule:

- Never generate a new `id` for transient retry of the same logical workload update.
- Only change `revision_id` when intent/spec changes.

---

## 4. State Mapping (Scheduler View)

Agent `ActualState`:

- `PENDING`
- `RUNNING`
- `STOPPED`
- `FAILED`
- `UNKNOWN`

Suggested scheduler interpretation:

- `PENDING`: accepted by agent but not yet converged; continue polling.
- `RUNNING`: desired running converged.
- `STOPPED`: desired stopped converged (or runtime not started).
- `FAILED`: terminal for current attempt; scheduler policy decides retry/backoff/escalation.
- `UNKNOWN`: transitional/inspection state; continue polling and/or trigger `ListActions`.

---

## 5. Async Execution and Action Traceability

When task queue path is used, apply response returns a status with metadata:

- `metadata["task_id"]`
- `metadata["task_status"]` (initially `pending`)

Use `ListActions` to inspect execution history since agent startup.

Recommended query:

- `workload_id=<id>`
- `limit=20`
- `newest_first=true`

Store in scheduler event/audit log:

- task id
- action type
- task status
- task error
- created/started/ended timestamps

---

## 6. Failure Handling Strategy

### 6.1 Apply failures

Agent persists failed status and intent, so failed scheduling attempts are now observable and reconcilable.

Scheduler should:

1. Mark workload as `admitted_on_node=true` after successful RPC acceptance.
2. If state becomes `FAILED`, apply scheduler retry policy:
   - transient infra/runtime errors: retry with backoff
   - validation/spec errors: do not hot-loop; surface to user
3. Keep same `id`, same `revision_id` for retry unless intent changed.

### 6.2 Missing runtime artifacts

Agent reconciliation attempts recreation when workload exists in state but runtime artifact is missing (`not found` class).

Scheduler implication:

- Do not immediately re-place workload elsewhere on first missing-runtime signal.
- Allow local reconcile window before failover policy triggers.

### 6.3 Delete failures

Delete RPC may be accepted while async cleanup is ongoing.

Scheduler should:

- poll until status is gone
- if stale for too long, read `ListActions` + health checks and escalate

---

## 7. Runtime-Specific Notes

### 7.1 Compose

- `compose_yaml` must be base64 encoded YAML.
- `project_name` should be stable and deterministic.

### 7.2 VM

- VM `disks` and `networks` must be explicit in spec.
- Cloud-init can be provided via `cloud_init` or `cloud_init_config`.
- VM delete cleanup is safe:
  - deterministic cloud-init ISO artifacts are removed
  - only agent-managed disks are auto-removed
  - user-provided external disks are preserved

---

## 8. Security and Connectivity

Agent config defaults (`internal/config/config.go`):

- gRPC: `PERSYS_GRPC_ADDR`, `PERSYS_GRPC_PORT`
- mTLS: `PERSYS_TLS_ENABLED`, `PERSYS_TLS_CERT`, `PERSYS_TLS_KEY`, `PERSYS_TLS_CA`

Production guidance:

- mTLS enabled (`PERSYS_TLS_ENABLED=true`)
- scheduler client cert rotation supported
- strict CA trust boundary per environment (dev/staging/prod)

---

## 9. Polling and Timeouts (Recommended Defaults)

Scheduler defaults:

- apply status poll interval: `1s`
- initial apply timeout: `45s` (container/compose)
- VM apply timeout: `240s`+ depending on image/bootstrap
- delete timeout: `60s`+

Backoff:

- exponential with jitter
- bounded max delay
- upper retry limit with escalation

---

## 10. Suggested Scheduler Implementation Skeleton

```text
schedule(workload):
  request := buildApplyRequest(workload.id, workload.revision, desired, spec)
  resp := agent.ApplyWorkload(request)

  if rpc error:
    mark "dispatch_failed"
    retry dispatch

  record acceptance + response metadata

  if resp.status.actual_state in {RUNNING, STOPPED}:
    mark converged
    return

  while timeout not exceeded:
    st := agent.GetWorkloadStatus(workload.id)
    if st in terminal:
      persist terminal and return
    sleep(poll_interval)

  actions := agent.ListActions(workload_id=workload.id, newest_first=true, limit=20)
  mark timeout + attach actions for diagnostics
```

---

## 11. Operational Observability

Use both:

- `GetWorkloadStatus` for current truth
- `ListActions` for execution trace

Minimum scheduler logs per operation:

- node id
- workload id
- revision id
- desired state
- agent RPC request timestamp
- task id (if present)
- terminal state
- terminal message/error

---

## 12. Rollout Plan (Recommended)

1. Enable integration in staging with read-only shadow mode (scheduler sends apply; does not route user traffic yet).
2. Validate:
   - revision idempotency
   - failure traceability via `ListActions`
   - reconcile recreate behavior
3. Turn on production for container workloads first.
4. Enable compose, then VM after storage/network validation.
5. Keep automated quick smoke tests in CI and pre-deploy checks.

---

## 13. Implementation Checklist

- [ ] gRPC client generated from `api/proto/agent.proto`
- [ ] scheduler workload model maps 1:1 to agent workload spec
- [ ] stable `id` and deterministic `revision_id`
- [ ] apply/delete orchestration with polling
- [ ] `ListActions` ingestion into scheduler audit/events
- [ ] retry/backoff policy implemented per failure class
- [ ] mTLS cert distribution and rotation wired
- [ ] production timeouts set by workload type
- [ ] dashboards/alerts for failed/pending timeout workloads

---

## 14. Reference Files

- API contract: `api/proto/agent.proto`
- Agent configuration: `internal/config/config.go`
- Architecture details: `docs/ARCHITECTURE.md`
- Example client and specs: `examples/client/main.go`, `examples/client/specs/`

