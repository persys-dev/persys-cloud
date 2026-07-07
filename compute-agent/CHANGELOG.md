# Changelog

## 2026-05-29 (Unreleased)

Source: `git diff -- compute-agent`

### Summary

This release adds support for managed volume storage orchestration, enhanced workload telemetry, and improved error handling aligned with scheduler platform extensions. Compute-agent now supports NFS and Ceph-RBD managed volumes for containers and VMs, with proper lifecycle management and storage driver capability advertisement.

### Major Features

1. **Managed Volume Support**
   - Added `ManagedVolumes[]ManagedVolumeSpec` to container and VM workload specs
   - Support for multiple storage drivers:
     - `local` - Host bind paths (existing behavior)
     - `nfs` - NFS server mounts
     - `ceph-rbd` - Ceph RBD block devices
   - Per-volume configuration: name, driver, size (GB), access mode, filesystem type, mount path, read-only, retain policy
   - Graceful fallback to host bind paths if managed volume provisioning fails

2. **Storage Driver Capability Advertisement**
   - Agent advertises supported storage drivers during node registration
   - `SupportedStorageDrivers[]` field populated in heartbeat
   - Enables scheduler to make storage-aware placement decisions

3. **Enhanced Workload Telemetry**
   - Added `WorkloadUsage` model with CPU%, memory, disk I/O, and network metrics
   - Per-workload usage collection and exposure in status responses
   - Enables performance correlation with placement decisions

4. **Improved Failure Diagnostics**
   - Added `WorkloadReason` with structured code, message, last transition, and next retry metadata
   - Terminal failure detection: prevents reapply loops for non-retryable errors
   - Failure reason propagation: infrastructure vs runtime error classification

5. **Cloud-Init Enhancement (VM)**
   - Support for structured `CloudInitConfig` with separate fields:
     - `user_data` - User-provided cloud-init script
     - `meta_data` - Cloud-init metadata
     - `network_config` - Network configuration (optional)
     - `vendor_data` - Vendor data (optional)
   - Faithful injection of all cloud-init fields into VM boot

### Breaking Changes

None. All changes are backward compatible.

### Deprecations

- Single-string `cloudInit` field deprecated in favor of structured `CloudInitConfig`
- Legacy bind-path-only volume handling will be superseded by managed volume system

### Changed Files

1. **api/proto/agent.proto** (updated)
   - Added `ManagedVolumeSpec` message type
   - Added `ManagedVolumes` field to `ContainerSpec`
   - Added `ManagedVolumes` field to `VMSpec`
   - Added `WorkloadUsage` and `WorkloadReason` message types
   - Extended heartbeat to include workload usage snapshots

2. **internal/models/workload.go** (updated)
   - Added `ManagedVolumes[]ManagedVolumeSpec` to container spec
   - Added `ManagedVolumes[]ManagedVolumeSpec` to VM spec
   - Added `WorkloadUsage` struct for telemetry
   - Added `WorkloadReason` struct for structured failures

3. **internal/control/client.go** (updated)
   - Updated node registration to advertise `SupportedStorageDrivers`
   - Enhanced heartbeat to include per-workload usage snapshots
   - Improved failure reason propagation

4. **internal/runtime/docker.go** (updated)
   - Added managed volume mount support
   - Fall back to host bind paths if managed volumes unavailable
   - Properly handle read-only and mount-path specifications

5. **internal/runtime/vm.go** (updated)
   - Added managed volume disk attachment for Ceph/NFS backends
   - Enhanced cloud-init ISO builder with structured payload support
   - Proper handling of `meta-data`, `network-config`, `vendor-data` files

6. **internal/workload/manager.go** (updated)
   - Added managed volume provisioning lifecycle
   - Pre-provision and attach volumes before runtime create
   - Cleanup volumes on workload deletion (respecting retain policy)

7. **pkg/api/v1/** (regenerated)
   - Protobuf code generation for new message types

### Resource Impact

**Storage Overhead**:
- Minimal: managed volume metadata tracked in control plane
- Agent reports only node-level storage capabilities

**Network**:
- Heartbeat size increases by ~200-500 bytes per workload (usage metrics)
- One-time increase during node registration (~100 bytes)

**Backward Compatibility**:
- Old workload specs without `ManagedVolumes` continue to work
- Agent falls back to host bind paths automatically
- Single-string `CloudInit` still supported alongside structured config

### Migration Notes

1. Optional: Update scheduler to populate managed volume specs
2. Optional: Configure NFS/Ceph providers on agent nodes
3. Agent advertises capabilities automatically
4. Workloads requesting managed volumes fail gracefully if drivers unavailable

### Known Issues

None documented at this time.

### Testing

All changes follow compute platform extension specification exactly.

### Upgrading

1. Deploy updated compute-agent binary
2. No configuration changes required (backward compatible)
3. Optional: Configure managed storage backend (NFS/Ceph)
4. Optional: Update workload specs to request managed volumes
5. Monitor agent logs for managed volume provisioning status

## 2026-02-23 (Unreleased)

Source: `git diff` inside `compute-agent`

### Incremental Update (Latest)

#### Summary

- Made metrics server port configurable via `PERSYS_METRICS_PORT` (default `8089`) instead of a hardcoded port.
- Added `compute-agent/sample.env` with baseline runtime, TLS, scheduler, and metrics environment variables.
- Fixed stale terminal-retry state for workloads whose desired state is `Stopped`:
  - normalize `Failed` runtime state to `Stopped` when desired is stopped,
  - clear retry/terminal metadata and reset retry tracker for stopped workloads,
  - prevent failed/terminal retry branch from running unless desired state is `Running`.
- Updated README config table to document `PERSYS_METRICS_PORT`.

#### Changed Files (Latest)

1. `compute-agent/internal/config/config.go`

- Added `MetricsPort` to config model.
- Added env parsing: `PERSYS_METRICS_PORT` with default `8089`.
- Added config validation for metrics port range.

1. `compute-agent/cmd/agent/main.go`

- Replaced hardcoded metrics server address with `fmt.Sprintf(":%d", cfg.MetricsPort)`.

1. `compute-agent/sample.env` (new file)

- Added sample environment values for:
  - server ports (`PERSYS_GRPC_PORT`, `PERSYS_METRICS_PORT`),
  - TLS/Vault,
  - runtime toggles,
  - scheduler endpoint and node metadata.

1. `compute-agent/internal/workload/manager.go`

- Added `normalizeStateForDesired(...)` for desired-state-aware status normalization.
- Added `clearRetryStateMetadata(...)` to remove stale retry/terminal keys.
- Applied normalization in `GetStatus`, `ReconcileWorkload`, and desired-state transition path.
- Cleared retry state/reset tracker when workload desired state is `Stopped`.
- Scoped failed-recovery reconciliation branch to `desired=running` only.

1. `compute-agent/README.md`

- Documented `PERSYS_METRICS_PORT` in configuration table.

### Summary

- Added agent-side telemetry initialization and gRPC trace context propagation.
- Added queue-level metrics hooks and Prometheus task metrics.
- Improved status reporting for in-flight tasks to reduce stale-state reapply loops.
- Improved VM status diagnostics (state/reason mapping + metadata).
- Added pending-state recovery policy (restart then delete fallback).
- Added reconcile start retry metadata/backoff deferral behavior.
- Fixed restart safety path: same-revision desired-state transitions now do runtime start/stop without recreate.
- Expanded proto generation outputs to shared package paths.
- Documented retry/backoff behavior in README.

### Changed Files (exact)

1. `compute-agent/Makefile` (+10 / -0)

- Updated `proto` target to also generate stubs into shared packages:
  - `../pkg/agent/api/v1`
  - `../pkg/agent/control/v1`

1. `compute-agent/README.md` (+43 / -0)

- Added `Retry and Backoff Strategy` section documenting:
  - scheduler reconnect backoff,
  - local reconcile retry policy,
  - pending-timeout recovery flow.

1. `compute-agent/cmd/agent/main.go` (+15 / -0)

- Added OpenTelemetry setup on startup via `internal/telemetry.Setup`.
- Added OTel shutdown on process exit with timeout.
- Wired task queue metrics observer when metrics are enabled.

1. `compute-agent/go.mod` (+8 / -2)

- Added direct OTel dependencies:
  - `go.opentelemetry.io/otel`
  - `go.opentelemetry.io/otel/sdk`
  - `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`
- Added/updated related indirect deps for OTLP and grpc-gateway proto stack.

1. `compute-agent/internal/control/client.go` (+60 / -2)

- Added unary gRPC client interceptor for OpenTelemetry spans.
- Added trace context injection into gRPC metadata.
- Applied interceptor to both insecure and TLS dial paths.

1. `compute-agent/internal/grpc/server.go` (+69 / -2)

- Added unary gRPC server interceptor for OpenTelemetry spans.
- Added inbound trace context extraction from gRPC metadata.
- Updated `GetWorkloadStatus` to overlay pending/running task metadata on top of stored status:
  - returns `ACTUAL_STATE_PENDING` for in-flight task states,
  - carries task metadata/message/updated_at so callers see live in-flight state.

1. `compute-agent/internal/metrics/metrics.go` (+67 / -0)

- Added task queue metrics:
  - `persys_agent_task_queue_depth` gauge,
  - `persys_agent_task_latency_seconds` histogram,
  - `persys_agent_task_executions_total` counter,
  - `persys_agent_task_failures_total` counter.
- Added helper methods:
  - `SetTaskQueueDepth`
  - `ObserveTaskExecution`

1. `compute-agent/internal/runtime/vm.go` (+162 / -10)

- VM `Status` now reports richer messages with domain state reasons.
- Paused domains now map to `ActualStatePending` (instead of `Stopped`) to avoid destructive upper-layer reactions.
- Added detailed VM metadata in `StatusMetadata`:
  - `vm.domain_state`, `vm.domain_state_code`,
  - `vm.domain_reason`, `vm.domain_reason_code`,
  - network lookup error metadata when interface discovery fails.
- Added helper mappings:
  - `vmDomainStateName`
  - `vmDomainReasonText` (libvirt reason enums for paused/shutoff/running/shutdown/crashed/etc.)

1. `compute-agent/internal/task/queue.go` (+39 / -0)

- Added `MetricsObserver` interface.
- Added observer wiring to queue lifecycle:
  - emit queue depth on start/submit/worker consume,
  - emit per-task duration + failure/success on completion.

1. `compute-agent/internal/workload/manager.go` (+110 / -4)

- Added pending recovery constants and metadata keys:
  - `pending_since`, `pending_recovery_action`, `pending_recovery_reason`, `pending_recovery_deleted`
  - threshold `5m`.
- Removed immediate delete-on-start-failure from `ApplyWorkload` path.
- Added `handlePendingRecovery` in reconcile path:
  - if pending > 5m: attempt stop+start,
  - if restart fails: delete runtime workload, mark failed, persist desired state `Stopped` to avoid loop.
- Added retry deferral for failed starts in reconciliation:
  - records `retry_attempts`, `next_retry_time`, `failure_reason`, `last_error`,
  - defers retry until tracker allows,
  - clears retry metadata on successful start.
- Updated same-revision apply behavior:
  - no longer blindly skips when desired state changed,
  - applies desired-state transitions with runtime `Start`/`Stop`,
  - avoids recreate/delete for running<->stopped transitions (critical restart safety fix).

1. `compute-agent/internal/workload/manager_test.go` (+new tests)

- Added regression tests proving same-revision desired-state transitions do not call recreate/delete:
  - `TestApplyWorkload_SameRevision_DesiredStateChange_StopWithoutRecreate`
  - `TestApplyWorkload_SameRevision_DesiredStateChange_StartWithoutRecreate`

1. `compute-agent/examples/client/specs/vm-spec.json` (+1 / -1)

- Changed disk `size_gb` from `10` to `5`.

1. `compute-agent/internal/telemetry/otel.go` (new file, +76)

- New OpenTelemetry bootstrap package:
  - reads exporter endpoint from env,
  - configures OTLP HTTP trace exporter,
  - sets tracer provider and W3C trace/baggage propagators,
  - returns shutdown function for graceful termination.

### Diff Stats

- Total changed tracked files: 11
- Total added lines in tracked files: 584
- Total removed lines in tracked files: 21
- New untracked file: 1 (`internal/telemetry/otel.go`, 76 lines)
