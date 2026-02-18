# Persys Agent Integration Guide

## Scope

This document defines how an agent integrates with the scheduler control plane using:

- `api/proto/control.proto` (`persys.control.v1.AgentControl`)
- `api/proto/agent.proto` (existing workload/runtime model used by scheduler internals)

The goal is to enable agent-side implementation with deterministic behavior aligned to scheduler design.

## Endpoints and Transport

Scheduler serves:

- gRPC control API on `GRPC_PORT` (default `8085`)
- HTTP metrics/health only on `METRICS_PORT` (default `8084`)

Security modes:

- Production: mTLS enabled by default
- Testing: start scheduler with `-insecure` to disable mTLS

Example test start:

```bash
go run ./cmd/scheduler -insecure
```

## Control Service

Service: `persys.control.v1.AgentControl`

RPCs the agent must implement against scheduler:

1. `RegisterNode`
2. `Heartbeat`
3. `ApplyWorkload`
4. `DeleteWorkload`
5. `RetryWorkload`
6. `ControlStream` (optional/future; currently unimplemented on scheduler)

## Required Agent Behavior

### 1. Node registration

Call `RegisterNode` once on agent startup and after reconnect if lease expires.

Required fields:

- `node_id`
- `capabilities.cpu_total_millicores`
- `capabilities.memory_total_mb`
- `supported_workload_types` (`container`, `compose`, `vm` as supported)
- `grpc_endpoint` (host:port reachable by scheduler for scheduler->agent workload RPCs)
- `timestamp`

Scheduler response:

- `accepted=true/false`
- `heartbeat_interval_seconds`
- `lease_expires_at`

Agent action:

- If not accepted: log and backoff retry registration
- If accepted: schedule heartbeat loop using returned interval

### 2. Heartbeats

Send `Heartbeat` periodically.

Payload:

- `node_id`
- `usage` (allocated + used cpu/memory/disk)
- `workload_statuses[]`
- `timestamp`

Scheduler response:

- `acknowledged`
- `drain_node`
- `lease_expires_at`

Agent action:

- If `drain_node=true`, stop accepting new workloads and prepare shutdown/migration mode
- Keep sending heartbeats while connected

### 3. Workload apply/delete/retry

Agent calls scheduler control API when driven by external control component using:

- `ApplyWorkload`
- `DeleteWorkload`
- `RetryWorkload`

Note: scheduler persists desired state first, then reconciliation executes scheduler->agent runtime actions.

## Workload Contract Notes

`ApplyWorkloadRequest` in `control.proto` contains:

- `workload_id`
- `spec` (`container|compose|vm` oneof)
- `revision_id` (compatibility/idempotency)
- `desired_state` (`Running|Stopped` string)

Agent-side guidance:

- Keep `workload_id` stable across retries
- Change `revision_id` only when spec/intent changes
- Preserve exact spec payload for deterministic replay

## Failure Semantics

Use `FailureReason` enum when returning apply failures:

- `IMAGE_PULL_FAILED`
- `IMAGE_NOT_FOUND`
- `INSUFFICIENT_RESOURCES`
- `INVALID_SPEC`
- `RUNTIME_ERROR`
- `NETWORK_ERROR`
- `STORAGE_ERROR`
- `VM_BOOT_FAILED`

Return `success=false` with `error_message` when failure occurs.

## Scheduler State Expectations

Scheduler treats itself as source of truth and expects:

- registration + heartbeat updates for node health
- workload transitions reported in heartbeat `workload_statuses`
- deterministic state names (`Running`, `Stopped`, `Failed`)

If heartbeat stops beyond lease window, scheduler marks node not ready and starts failover logic.

## Recommended Agent Loop

1. Start agent runtime services.
2. Register with scheduler.
3. Start heartbeat ticker with server-provided interval.
4. Report node usage + workload statuses each tick.
5. Handle reconnect/re-register on transport failure.
6. Continue serving scheduler->agent workload RPCs (`agent.proto` service) in parallel.

## Relationship to `agent.proto`

The scheduler currently uses `agent.proto` (`persys.agent.v1.AgentService`) to drive runtime convergence on the node:

- `ApplyWorkload`
- `DeleteWorkload`
- `GetWorkloadStatus`
- `ListWorkloads`
- `HealthCheck`
- `ListActions`

Therefore, the agent must expose both:

1. Client behavior to call scheduler control service (`control.proto`)
2. Server behavior to accept scheduler runtime calls (`agent.proto`)

## Minimal Implementation Checklist (Agent)

- [ ] Generate stubs for `control.proto`
- [ ] Implement scheduler client with mTLS + insecure test mode
- [ ] Implement startup `RegisterNode`
- [ ] Implement periodic `Heartbeat`
- [ ] Populate `NodeUsage` from host metrics
- [ ] Populate `WorkloadStatus` from runtime state
- [ ] Map runtime errors to `FailureReason`
- [ ] Keep stable `workload_id` and proper `revision_id`
- [ ] Expose `agent.proto` gRPC server for scheduler runtime operations
- [ ] Add integration tests for reconnect, lease expiry, and heartbeat loss

## Local Testing Tips

With scheduler running insecure:

```bash
go run ./cmd/scheduler -insecure
```

Agent can connect without TLS during bring-up, then switch to mTLS once cert flow is ready.

## Current Limitations

- `ControlStream` is defined but not implemented by scheduler yet.
- Heartbeat-driven rescheduling policy is scheduler-side and may evolve.
- Multi-cluster federation fields (`cluster_id`) are accepted but not fully enforced yet.
