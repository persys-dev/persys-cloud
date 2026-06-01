# Persys Compute Platform Extension Design Spec

Status: Draft
Owner: Compute + Scheduler teams
Last Updated: 2026-02-23

## 1. Problem Statement

We need four production-critical capabilities:

1. A real storage layer that can provision and attach volumes from NFS/Ceph (not only host bind paths).
2. Dynamic cloud-init injection from user input (current behavior still defaults to mostly static seed generation).
3. Separation of storage/network concerns from compute runtime logic.
4. Per-workload (container/VM) utilization telemetry so scheduling, automation, and intelligence can reason about performance.

## 2. Current-State Findings (from code scan)

- `compute-agent/internal/storage/manager.go` provides an in-memory pool/allocation manager only; it is not integrated into runtime Create/Start/Delete flows.
- `compute-agent/internal/runtime/docker.go` mounts only direct host bind paths (`VolumeMount.host_path`) and has no managed volume abstraction.
- `compute-agent/internal/runtime/vm.go` generates cloud-init ISO but writes static fallback `meta-data`; `network-config`/`vendor-data` are not seeded into ISO files.
- `compute-agent/internal/runtime/runtime.go` has a runtime interface only; storage/network provider abstractions are missing.
- `compute-agent/internal/resources/monitor.go` and `compute-agent/internal/metrics/metrics.go` expose node-level metrics but not workload-level CPU/memory/io/network usage.
- Scheduler/gateway/ctl mostly pass workload specs through, but control-plane contracts do not yet model managed volume lifecycle or workload telemetry envelopes.

## 3. Goals

- Introduce pluggable volume provisioning/attachment with NFS and Ceph first-class support.
- Ensure VM cloud-init payload from users is injected faithfully (user-data/meta-data/network-config/vendor-data).
- Decouple runtime implementations from storage/network implementations with explicit interfaces.
- Publish workload utilization in agent metrics + scheduler-visible status so users can explain slow workloads.

## 4. Non-Goals (for this phase)

- Full Kubernetes CSI/CNI compatibility.
- Multi-tenant quota/billing engine.
- Long-term metrics storage in this same milestone (we expose and stream first).

## 5. Target Architecture

### 5.1 Compute-Agent Platform Layer

Add `compute-agent/internal/platform/`:

- `storage.go`: `StorageProvider`, `VolumeManager`, `VolumeAttachment`.
- `network.go`: `NetworkProvider`, `NetworkAttachment`.
- `types.go`: shared structs (`VolumeSpec`, `VolumeHandle`, `WorkloadNetSpec`).

Runtimes (`docker`, `compose`, `vm`) consume these interfaces instead of raw host paths.

### 5.2 Managed Volume Model

Add workload spec support for managed volumes:

- `name`, `driver` (`local|nfs|ceph-rbd`), `size_gb`, `access_mode`, `fs_type`, `mount_path`, `read_only`, `retain_policy`.

Lifecycle:

1. Resolve/provision volume in provider.
2. Attach/stage for workload.
3. Runtime mounts staged target.
4. Detach on stop/delete.
5. Honor retain/delete policy.

### 5.3 Cloud-Init Injection Model

For VM workloads:

- Accept and persist all cloud-init fields from API.
- Build NoCloud seed with files:
  - `user-data`
  - `meta-data`
  - `network-config` (optional)
  - `vendor-data` (optional)
- Keep deterministic seed path and checksum in workload status metadata.

### 5.4 Utilization Telemetry Model

Add workload-level usage snapshot type:

- `workload_id`, `type`, `cpu_percent`, `memory_bytes`, `disk_read_bytes`, `disk_write_bytes`, `net_rx_bytes`, `net_tx_bytes`, `collected_at`, `source`.

Sources:

- Containers/compose: Docker stats API.
- VMs: libvirt domain stats (CPU time, memory stats, interface/block stats where available).

## 6. Concrete Implementation Plan

## Phase 0: Contracts and Schema (safe-first)

1. Update API contracts.

- `compute-agent/api/proto/agent.proto`
  - Add managed volume structures and fields to container/vm specs.
  - Add optional workload telemetry response shape (or metadata envelope) for status/list.
- `persys-scheduler/api/proto/control.proto`
  - Mirror managed volume spec.
  - Extend `WorkloadStatus`/`WorkloadView` to include structured reason + optional usage snapshot.
  - Extend heartbeat with repeated workload usage snapshots.
- Regenerate pb code in `compute-agent/pkg/api/v1`, `compute-agent/pkg/control/v1`, `persys-scheduler/internal/controlv1`, `persys-gateway/internal/controlv1`, `persysctl/internal/controlv1`.

2. Extend models.

- `compute-agent/pkg/models/workload.go`: add managed volume and usage structs.
- `persys-scheduler/internal/models/models.go`: add managed volume + workload usage fields.
- `persysctl/internal/models/models.go`: expose new fields for CLI output.

Acceptance:

- Backward-compatible defaults preserve old specs.
- Existing apply/list/get calls still work.

## Phase 1: Storage Provider Integration (NFS + Ceph)

1. Implement provider interfaces.

- New:
  - `compute-agent/internal/platform/storage.go`
  - `compute-agent/internal/storage/providers/local_provider.go`
  - `compute-agent/internal/storage/providers/nfs_provider.go`
  - `compute-agent/internal/storage/providers/ceph_rbd_provider.go`

2. Persist volume state.

- Extend state store with `volumes` and `attachments` buckets.
- Files:
  - `compute-agent/internal/state/store.go`
  - `compute-agent/internal/state/bolt_*.go` (new file split recommended)

3. Integrate with workload manager lifecycle.

- `compute-agent/internal/workload/manager.go`
  - Pre-create/attach managed volumes before runtime `Create`.
  - Detach/cleanup on delete based on retain policy.
  - Persist explicit failure reason (`STORAGE_ATTACH_FAILED`, `STORAGE_PROVISION_FAILED`).

4. Runtime wiring.

- `compute-agent/internal/runtime/docker.go`
  - Convert managed volume attachments into runtime mounts.
- `compute-agent/internal/runtime/vm.go`
  - Attach Ceph/NFS-backed disks through libvirt disk source definitions.

5. Scheduler capability-awareness.

- `compute-agent/internal/control/client.go` heartbeat/register: advertise storage driver capabilities.
- `persys-scheduler/internal/scheduler/scheduler.go`: only place workloads on nodes supporting requested storage driver.

Acceptance:

- Container can request NFS/Ceph volume and start with mounted volume.
- VM can boot with Ceph/NFS-backed disk where requested.
- Delete honors retain/delete policy.

## Phase 2: Dynamic Cloud-Init End-to-End

1. Preserve full cloud-init fields through control path.

- `persysctl/cmd/scheduler.go` and scheduler conversion helpers keep `user_data`, `meta_data`, `network_config`, `vendor_data` unchanged.
- Ensure no lossy conversion via reduced legacy control VM schema.

2. Cloud-init ISO builder update.

- `compute-agent/internal/runtime/vm.go`
  - `createCloudInitISO`: write `meta-data` from user payload when provided.
  - Write `network-config` and `vendor-data` files if provided.
  - Include payload checksum in status metadata.

3. Validation and safety.

- Reject oversized/invalid cloud-init payload with explicit user-visible errors.
- Redact sensitive cloud-init fields from logs while retaining hash and size.

Acceptance:

- User-provided cloud-init is faithfully applied.
- Status shows `vm.cloud_init_seed_checksum`, `vm.cloud_init_seed_path`.

## Phase 3: Storage/Network Abstraction from Runtime

1. Introduce dependency-injected runtime context.

- `compute-agent/internal/runtime/runtime.go`
  - Add optional runtime dependencies struct (`StorageProvider`, `NetworkProvider`).

2. Network provider implementation.

- New `compute-agent/internal/network/providers/` with initial:
  - Docker network provider wrapper.
  - Libvirt network resolver wrapper.

3. Refactor runtimes.

- `docker.go`, `compose.go`, `vm.go` use provider interfaces, not direct ad-hoc host/network assumptions.

4. Bootstrap wiring.

- `compute-agent/cmd/agent/main.go`: instantiate provider registry and inject into runtime constructors.
- `compute-agent/internal/config/config.go`: add provider-specific configuration (NFS mount options, Ceph pool/user/keyring, default network policies).

Acceptance:

- Runtime packages compile/test against mock providers.
- Storage/network behavior can be tested without live Docker/libvirt by mocking providers.

## Phase 4: Workload Utilization Telemetry

1. Agent collectors.

- New `compute-agent/internal/telemetry/workload_usage_collector.go`.
- Poll every `N` seconds and cache latest usage per workload.

2. Metrics exposure.

- Extend `compute-agent/internal/metrics/metrics.go` with labeled gauges/counters:
  - `persys_agent_workload_cpu_percent{workload_id,type}`
  - `persys_agent_workload_memory_bytes{workload_id,type}`
  - `persys_agent_workload_disk_read_bytes_total{workload_id,type}`
  - `persys_agent_workload_disk_write_bytes_total{workload_id,type}`
  - `persys_agent_workload_network_rx_bytes_total{workload_id,type}`
  - `persys_agent_workload_network_tx_bytes_total{workload_id,type}`

3. Status and heartbeat propagation.

- Agent `GetWorkloadStatus/ListWorkloads`: include latest usage snapshot in metadata/structured field.
- `compute-agent/internal/control/client.go`: include per-workload usage in heartbeat.
- Scheduler stores latest usage and surfaces it in:
  - `persys-scheduler/internal/grpcapi/service.go` (`WorkloadView`)
  - Gateway pass-through (`persys-gateway/controllers/scheduler.controller.go` + generated pb).

4. User-facing diagnostics.

- `persysctl` output (`workload list/get`) includes utilization and last sample timestamp.
- For failed/pending/frozen workloads, show reason codes and runtime reason text from metadata.

Acceptance:

- `workload list/get` shows recent per-workload CPU/memory and at least one IO/network signal.
- Scheduler can filter/report “high CPU” or “memory pressure” candidate workloads in future automation.

## 7. Cross-Cutting Reliability Changes

- Add reason-code taxonomy (shared enum or canonical string set):
  - `STORAGE_PROVISION_FAILED`, `STORAGE_ATTACH_FAILED`, `NETWORK_ATTACH_FAILED`, `CLOUD_INIT_INVALID`, `VM_PAUSED_IO_ERROR`, `WORKLOAD_RESOURCE_STARVATION`.
- Ensure each reconcile failure writes:
  - machine-readable reason code,
  - human-readable message,
  - last transition time,
  - next retry time if retryable.

## 8. Rollout Strategy

1. Ship contracts first behind feature gates:

- `PERSYS_FEATURE_MANAGED_VOLUMES`
- `PERSYS_FEATURE_DYNAMIC_CLOUD_INIT`
- `PERSYS_FEATURE_WORKLOAD_TELEMETRY`

2. Enable in canary cluster order:

- telemetry -> cloud-init -> storage provider path.

3. Keep fallback paths:

- old host bind volume behavior remains valid.
- old cloud-init single string remains valid.

## 9. Test Plan

- Unit tests:
  - provider allocation/attach/detach behavior.
  - cloud-init ISO generation with golden fixtures.
  - runtime + provider integration via mocks.
- Integration tests:
  - NFS volume attach to container.
  - Ceph RBD attach to VM.
  - cloud-init network-config applied on VM boot.
  - telemetry visible in scheduler `GetWorkload`.
- Chaos tests:
  - NFS/Ceph outage during attach.
  - libvirt transient failures during stats collection.

## 10. Milestone Breakdown (Execution Order)

1. Milestone A (1-2 weeks): proto/model updates + compatibility + CLI/gateway regeneration.
2. Milestone B (2-3 weeks): storage provider framework + NFS driver + container integration.
3. Milestone C (2-3 weeks): Ceph RBD + VM disk attach path + state persistence.
4. Milestone D (1-2 weeks): dynamic cloud-init full payload support.
5. Milestone E (2 weeks): workload telemetry collection + exposure end-to-end.
6. Milestone F (1 week): hardening, migration docs, runbooks.

## 11. Open Decisions

- Ceph auth distribution mechanism (static keyring vs Vault-injected credentials).
- Retain policy defaults for managed volumes (`Delete` vs `Retain`).
- Whether per-workload usage history is kept in etcd or only current snapshot in control plane.

## 12. Definition of Done

- Managed volumes (NFS/Ceph) can be provisioned/attached without host-path hardcoding.
- Cloud-init user payload is injected as-is and traceable via checksum/metadata.
- Runtime packages depend on provider abstractions, not direct storage/network assumptions.
- Users can inspect per-workload utilization and precise failure reasons from scheduler/gateway/`persysctl`.
