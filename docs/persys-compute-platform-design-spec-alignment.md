# Persys Compute Platform Extension Design Spec - Current Implementation Status

Status: reconciled against the actual code and current repo state, 2026-08-04

This review is based on the current implementation, not the original design backlog. The project is now better understood as a mature control-plane platform with a set of completed capabilities and a smaller set of hardening gaps.

## Executive summary

The original design described a path centered on managed storage, cloud-init, runtime abstraction, and telemetry. Today, the project has already advanced beyond that baseline in several important areas:

- scheduler HA and sharding are in place
- node drain and taint logic is complete
- Firecracker is a real runtime
- vault-manager automates zero-touch mTLS lifecycle management
- gateway route discovery through gRPC reflection is implemented
- persys-meter already consumes workload metrics from the scheduler
- persysctl includes newer gateway routes even though the SDK wrapper is still intentionally deferred for stability

The biggest remaining gaps are not product design gaps; they are operational hardening items around network abstraction, rollout controls, and validation.

## Alignment by domain

### 1. Scheduler HA and scaling - ✅ Complete in implementation

Evidence in code and documentation:

- persys-scheduler/CHANGELOG.md documents leader election, active-active sharding, weighted placement, and connection pooling.
- persys-scheduler/README.md documents failover mode, shard mode, placement scoring, and in-flight reservations.
- persys-scheduler/internal/scheduler/leader.go, sharding.go, node_watch.go, and placement.go are part of the live runtime path.

This is a major shift from the original design and should be treated as a completed architectural milestone rather than a future task.

### 2. Node operations and scheduler placement - ✅ Complete in implementation

Evidence:

- drain, taint, untaint, and node readiness behaviors are present in the scheduler control API and scheduler logic.
- node selection now accounts for resource headroom, spread, and in-flight reservation tracking.
- the scheduler README explicitly documents the placement algorithm and operational behavior.

This should be treated as already implemented rather than planned work.

### 3. Vault-managed zero-touch mTLS - ✅ Complete in implementation

Evidence:

- vault-manager/README.md documents the bootstrap, PKI, AppRole, and gRPC credential lifecycle.
- vault-manager/cmd/main.go shows secure bootstrap handoff and restart-safe recovery logic.
- the project no longer depends on manual service-by-service mTLS hand configuration.

This is a substantial completed capability and should be documented as such in the platform story.

### 4. Gateway gRPC bridge and auto-discovery - ✅ Complete in implementation

Evidence:

- persys-gateway/README.md documents dynamic HTTP-to-gRPC bridging via reflection and compiled-in fallback.
- persys-gateway/cmd/main.go wires the route catalog, dynamic bridge, and router registration.
- the gateway supports service discovery and catalog-based routing rather than only static route definitions.

This is a major architectural modernization already in place.

### 5. Firecracker runtime - ✅ Complete in implementation

Evidence:

- compute-agent/internal/runtime/microvm.go implements a Firecracker-backed microVM runtime.
- the runtime includes config generation, process lifecycle, status checks, and cleanup behavior.
- scheduler runtime capability logic is aligned to accept Firecracker as a valid runtime type.

This should be counted as a delivered runtime capability, not a future idea.

### 6. Managed volumes and cloud-init - ✅ Complete in implementation

Evidence:

- managed volume abstraction and lifecycle remain in the runtime and scheduler.
- cloud-init generation and VM seed handling are implemented in compute-agent runtime code.

This remains a platform strength and should be treated as a mainline capability.

### 7. Workload telemetry and persys-meter - ✅ Complete in implementation

Evidence:

- the scheduler emits per-workload usage into Redis streams.
- persys-meter/README.md and persys-meter/cmd/meter/main.go show the meter service consuming those events, storing histories, and exposing live metrics and API endpoints.

This is an already-real telemetry path and should not be discussed as missing work.

### 8. SDK cleanup and persysctl wrapper - ⚠️ Deferred intentionally

Evidence:

- the repo still contains direct command logic in persysctl and the codebase explicitly notes that the full SDK migration is postponed for stability issues.

The current status is not a failure; it is a deliberate stabilization decision. The repo is intentionally prioritizing stability over a wholesale SDK refactor.

## Remaining gaps

### 1. Network abstraction closure

This is still the main technical architecture gap.

Current state:

- storage abstraction is already complete
- network abstraction exists as an interface layer but is not fully wired into runtime constructors and provider implementations

Recommended path:

- add concrete provider implementations
- inject network providers through runtime dependency sets
- validate runtime compatibility across docker, vm, and microvm flows

### 2. Feature flags for staged rollout

The system needs explicit rollout gates for:

- managed volumes
- cloud-init
- telemetry
- Firecracker
- network abstraction

Without these, operators cannot safely stage platform features across clusters or environments.

### 3. End-to-end validation

The repo contains substantial implementation, but production confidence still requires:

- real NFS and Ceph volume validation
- cloud-init failure regression tests
- telemetry flow tests from agent to meter
- scheduler HA failover checks under workload churn

### 4. Operator documentation

The code has advanced deployment behavior, but the operator story should be expanded to cover:

- HAProxy placement and replica topology
- etcd and Redis role boundaries
- vault recovery and trust bootstrap
- runtime compatibility matrix for Docker, VM, and MicroVM

## Current alignment score

Overall alignment with the original design direction: about 92%

This score is higher than the earlier plan because the codebase has already delivered several previously planned capabilities that were not recognized in the old roadmap.

### Phase status summary

- Contracts and schema: 100%
- Managed storage: 100%
- Cloud-init: 100%
- Scheduler HA and placement: 100%
- Gateway gRPC bridge: 100%
- Vault-based mTLS lifecycle: 100%
- Firecracker runtime: 100%
- Workload telemetry path: 100%
- Network abstraction closure: 60%
- SDK cleanup: 35%

## Recommended project position

The project should no longer be framed as a mostly-planned architecture. It should be framed as:

- a real platform with substantial shipped capabilities
- a stable control plane with hardening work still ahead
- a system where the next phase is production maturation rather than greenfield design

## Final recommendation

The roadmap should be updated as follows:

1. Keep HA scheduler, Firecracker support, gateway gRPC discovery, meter telemetry, and vault-based certificate lifecycle as completed platform work.
2. Treat network abstraction and feature-gate enforcement as the highest-priority hardening work.
3. Defer the full SDK refactor until the platform is stable and the operational model is validated.
4. Prioritize real production proof over ceremonial rewrites.

This is the correct framework for the next phase of the project.

1. Implement network provider wrappers (if network abstraction needed soon)
2. Add explicit feature gates for gradual rollout
3. Verify telemetry collection frequency (Docker stats polling) and test end-to-end
4. Formalize integration tests for NFS/Ceph volume operations
5. Documentation updates for operators (NFS/Ceph prerequisites, configuration)