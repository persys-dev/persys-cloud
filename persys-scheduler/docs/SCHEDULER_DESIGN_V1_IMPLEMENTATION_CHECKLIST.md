# Scheduler Design V1 Implementation Checklist

Source of truth reviewed: `docs/SCHEDULER_DESIGN_V1.md`

Status legend:
- `Implemented`: behavior exists in code and is wired.
- `Partial`: implemented but not fully aligned with design requirements.
- `Missing`: not implemented.

## Overall

- Implemented: 30
- Partial: 9
- Missing: 6
- Coverage estimate: ~67% fully implemented (30/45 checklist items)

## 1. Purpose

- [x] Implemented: Scheduler acts as control plane (placement + reconciliation + node management). Evidence: `internal/scheduler/scheduler.go`, `internal/scheduler/reconciler.go`, `internal/scheduler/node_control.go`.
- [x] Implemented: Scheduler does not run workloads directly; agent RPC is execution plane. Evidence: `internal/scheduler/agent_grpc.go`.

## 2. Design Principles

- [x] Implemented: Desired state persisted in etcd as primary source. Evidence: `internal/scheduler/state_store.go`, `internal/scheduler/workload_control.go`.
- [x] Implemented: Declarative desired state (`Running|Stopped|Deleted`) and reconcile loop converges. Evidence: `internal/scheduler/reconciler.go`.
- [~] Partial: Idempotency via `(workload_id, revision_id)` is passed to agent, but full enforcement depends on agent-side contract behavior. Evidence: `internal/scheduler/agent_grpc.go`, `internal/grpcapi/service.go`.
- [x] Implemented: Scheduler owns reconciliation logic. Evidence: `internal/scheduler/reconciler.go`.
- [x] Implemented: Deterministic decision/retry metadata persisted. Evidence: `/assignments`, `/reconciliation`, `/retries` in `internal/scheduler/state_store.go`.

## 3. Architecture

- [x] Implemented: Scheduler exposes gRPC control server and talks to agents over gRPC. Evidence: `cmd/scheduler/main.go`, `internal/grpcapi/service.go`, `internal/scheduler/agent_grpc.go`.
- [x] Implemented: mTLS mode exists with insecure test override. Evidence: `cmd/scheduler/main.go`.

## 4.1 API Server

- [x] Implemented: Request validation in control API methods. Evidence: `internal/grpcapi/service.go`.
- [x] Implemented: Desired state persisted before reconcile/apply flow. Evidence: `internal/grpcapi/service.go`, `internal/scheduler/workload_control.go`.
- [x] Implemented: Read APIs for inventory/status. Evidence: `ListNodes`, `GetNode`, `ListWorkloads`, `GetWorkload`, `GetClusterSummary` in `internal/grpcapi/service.go`.
- [~] Partial: Design lists REST endpoints; implementation is gRPC-only for control API. Evidence: `cmd/scheduler/main.go`, `internal/grpcapi/service.go`.

## 4.2 State Store (etcd)

- [x] Implemented: Key layout `/nodes`, `/workloads`, `/assignments`, `/reconciliation`, `/events`, `/retries`. Evidence: `internal/scheduler/state_store.go`.
- [~] Partial: Canonical workload record shape is close but not exact (`spec` is flattened into workload-specific fields). Evidence: `internal/models/models.go`, `internal/scheduler/workload_control.go`.

## 4.3 Node Manager

- [x] Implemented: Node registration. Evidence: `internal/grpcapi/service.go`, `internal/scheduler/scheduler.go`.
- [x] Implemented: Heartbeat updates node readiness and capacity fields. Evidence: `internal/grpcapi/service.go`, `internal/scheduler/node_control.go`.
- [x] Implemented: Missing heartbeat marks `NotReady`. Evidence: `internal/scheduler/scheduler.go` (`MonitorNodes`).
- [x] Implemented: Node loss triggers failover/reschedule path. Evidence: `internal/scheduler/reconciler.go` (`handleUnavailableAssignedNode`).
- [~] Partial: Node disk capacity accounting is not implemented (placeholder logic). Evidence: `internal/scheduler/scheduler.go` (`selectNodeForWorkload`).

## 4.4 Placement Engine

- [x] Implemented: Filters by readiness, CPU, memory, labels, workload type support. Evidence: `internal/scheduler/scheduler.go` (`selectNodeForWorkload`).
- [~] Partial: Disk filter required by design is not active. Evidence: `internal/scheduler/scheduler.go` (`availableDisk` placeholder).
- [x] Implemented: Sorts by utilization and picks first. Evidence: `internal/scheduler/scheduler.go` (`nodeUtilizationScore`, `sort.Slice`).
- [x] Implemented: Persists assignment with reason metadata. Evidence: `internal/scheduler/state_store.go` (`writeAssignment`), `internal/scheduler/scheduler.go` (`assignWorkload`).

## 4.5 Reconciliation Engine

- [x] Implemented: Periodic loop, configurable interval (default 5s). Evidence: `internal/scheduler/scheduler.go` (`StartReconciliation`), `internal/scheduler/reconciler.go`.
- [x] Implemented: Reconcile workflow uses desired state + agent actual state + action execution. Evidence: `internal/scheduler/reconciler.go`.
- [x] Implemented: Handles key transitions (`Running/Missing`, `Running/Failed`, `Stopped/Running`, `Deleted/*`). Evidence: `internal/scheduler/reconciler.go`.
- [x] Implemented: Transitional grace handling exists for missing state and unavailable nodes. Evidence: `internal/scheduler/reconciler.go` (`defaultMissingGracePeriod`, `defaultNodeUnavailableGrace`).
- [x] Implemented: Reconciliation metadata persisted per action. Evidence: `internal/scheduler/state_store.go` (`writeReconciliationRecord`).

## 4.6 Retry Engine

- [x] Implemented: Retry state persisted and updated on failures. Evidence: `internal/scheduler/workload_control.go`, `internal/scheduler/state_store.go`.
- [x] Implemented: Backoff schedule doubles from 5s and caps at 120s. Evidence: `internal/scheduler/workload_control.go` (`retryBackoff`).
- [x] Implemented: Stops at max attempts and marks workload failed. Evidence: `internal/scheduler/workload_control.go` (`UpdateWorkloadRetryOnFailure`).

## 4.7 Event System

- [x] Implemented: Immutable append-only events under `/events/<id>`. Evidence: `internal/scheduler/state_store.go` (`emitEvent`).
- [x] Implemented: Event types used include `WorkloadScheduled`, `WorkloadFailed`, `NodeLost`, `RetryTriggered`, `Rescheduled`. Evidence: `internal/scheduler/*.go`.

## 4.8 Automation Hooks

- [ ] Missing: Deterministic rule engine for policy hooks (CPU threshold, repeated-failure alerts, webhooks) is not implemented.

## 5. Workload Lifecycle

- [x] Implemented: Create flow persists, assigns, reconciles, applies. Evidence: `internal/grpcapi/service.go` (`ApplyWorkload`), `internal/scheduler/workload_control.go`, `internal/scheduler/reconciler.go`.
- [x] Implemented: Update flow sets `Updating`, recomputes revision, reconciles. Evidence: `internal/scheduler/workload_control.go`, `internal/grpcapi/service.go`.
- [x] Implemented: Delete flow marks deleted, reconciler sends delete, and removes workload when agent returns not found. Evidence: `internal/scheduler/workload_control.go`, `internal/scheduler/reconciler.go`, `internal/scheduler/scheduler.go`.

## 6. Failure Handling

- [x] Implemented: Agent unreachable marks node `NotReady` and enters failover path. Evidence: `internal/scheduler/node_control.go`, `internal/scheduler/reconciler.go`.
- [x] Implemented: Retry policy with persisted reason and terminal failure. Evidence: `internal/scheduler/workload_control.go`.
- [x] Implemented: Heartbeat timeout detection and workload reschedule attempts. Evidence: `internal/scheduler/scheduler.go` (`MonitorNodes`), `internal/scheduler/reconciler.go`.

## 7. Concurrency Model

- [ ] Missing: Reconciliation worker pool is not implemented (loop is sequential per cycle).
- [x] Implemented: Context cancellation for long-running loops and shutdown. Evidence: `cmd/scheduler/main.go`, `internal/scheduler/reconciler.go`, `internal/scheduler/monitor.go`.
- [ ] Missing: Optimistic etcd concurrency (transaction compare-and-swap) is not implemented.
- [~] Partial: Global lock avoidance mostly true, but no explicit worker-parallelism controls.

## 8. High Availability (Future)

- [ ] Missing: Leader election and single active reconciler across replicas is not implemented.

## 9. Security

- [x] Implemented: mTLS for control plane/server and scheduler->agent path (with test insecure option). Evidence: `cmd/scheduler/main.go`, `internal/scheduler/agent_grpc.go`.
- [x] Implemented: CFSSL certificate issuance path exists. Evidence: `internal/auth/certmanager.go`.
- [~] Partial: Certificate rotation policy lifecycle is basic (`EnsureCertificate`) and not fully operationalized/documented.
- [ ] Missing: RBAC enforcement is not implemented in scheduler service (expected at API gateway).

## 10. Observability

- [x] Implemented: `/metrics` and `/health` HTTP endpoints. Evidence: `cmd/scheduler/main.go`.
- [ ] Missing: Design-named core scheduler counters are not implemented (`scheduling_attempts_total`, `reconciliation_actions_total`, `retry_total`, `node_unhealthy_total`).

## 11. Scale Targets

- [~] Partial: Architecture can handle moderate scale, but no explicit load/scale validation or capacity tests in repo.

## 12. Out of Scope

- [x] Implemented: Scheduler does not build images.
- [x] Implemented: Scheduler does not execute runtimes directly.
- [x] Implemented: Scheduler does not store centralized full runtime logs backend (only per-workload status/log snippets in etcd model).
- [x] Implemented: Agent-side reconciliation is not used as cluster control mechanism.

## 13. Known Gaps (V1)

- [x] Implemented as known gap: Event streaming API still missing.
- [x] Implemented as known gap: Topology-aware placement still missing.
- [x] Implemented as known gap: Federation controls still missing.
- [x] Implemented as known gap: Tenant quotas/rate-limits still missing.
- [~] Partial: Retry RPC exists (`RetryWorkload`) but persistent retry API formalization/versioning is still basic.

## 14. Acceptance Criteria Check

- [x] Implemented: Workload state persisted before runtime action execution.
- [~] Partial: Scheduler->agent idempotency by `revision_id` is wired, but full guarantee depends on strict agent enforcement.
- [x] Implemented: Reconciliation avoids hot-loop behavior through interval + retry windows.
- [x] Implemented: Retry state survives restart via persisted workload retry fields and `/retries` key.
- [x] Implemented: Node loss triggers deterministic reschedule/failover path.
- [~] Partial: Most actions emit structured events, but metric/event coverage is not yet complete for all observability requirements.

## Priority Gap List (Recommended Next)

- [ ] Implement disk-aware scheduling filter and node disk accounting.
- [ ] Add reconciliation worker pool with bounded concurrency.
- [ ] Add optimistic etcd writes (`Txn` compare-and-swap) for conflict-safe updates.
- [ ] Implement core Prometheus counters listed in design.
- [ ] Implement automation hooks/rule engine.
- [ ] Define/implement leader election for HA reconciler ownership.
