# Persys Cloud – Production Readiness and Delivery Roadmap

Status: Current repo reality as of 2026-08-04

This roadmap reflects the code that is already present in the repo today, not the original design backlog. The project has moved well past the initial extension spec and is now an active platform with hardening work, not greenfield feature invention.

## 1. What is already delivered

The repo now contains a substantial production baseline across the control plane, runtime, security, and gateway layers.

### 1.1 Scheduler and control plane

- 3-way scheduler HA behind HAProxy is already in the repo and documented in the scheduler changelog and README.
- etcd leader election is implemented with failover and active-active sharding modes.
- connection pooling for agent gRPC traffic is in place.
- node placement logic was rewritten to use weighted scoring with in-flight resource reservations and spread-aware balancing.
- node drain, taint, untaint, and placement exclusion paths are implemented.
- scheduler write paths use etcd CAS and are hardened against concurrent mutation.

### 1.2 Runtime matrix

- Docker and VM runtime paths are in place.
- Firecracker is now a valid runtime in compute-agent and scheduler capability logic.
- cloud-init and VM seed generation remain active and are supported by the runtime code.
- managed volume provider lifecycle is implemented, including storage capability detection and attach/detach behavior.

### 1.3 Observability and metrics

- persys-meter exists and is already consuming usage data from the scheduler.
- the scheduler publishes per-workload usage into Redis streams and the meter stores them for history and live queries.
- Prometheus metrics and live workload metrics are available through the meter service.

### 1.4 Security and trust plane

- vault-manager has become a zero-touch certificate lifecycle manager for mTLS.
- the project can bootstrap Vault, provision CA and AppRole infrastructure, and serve a runtime credential API.
- certificate issuance and rotation are no longer hand-managed at the service level.

### 1.5 Gateway and service routing

- persys-gateway was substantially reworked with a gRPC bridge and gRPC reflection.
- dynamic backend RPC discovery works without per-endpoint hard-coding.
- service catalog and route resolution are in place.
- gateway service routing is now structured around a cleaner control-plane and catalog model.

### 1.6 CLI and operator tooling

- persysctl has received new gateway routes and metrics commands.
- the work to fully move persysctl to a clean SDK wrapper is postponed for stability reasons, which is a reasonable engineering tradeoff given current platform maturity.

## 2. Current conclusion

The platform is no longer in the “big feature design phase.” It is in the “stabilize and harden the shipped control plane” phase.

The right posture is:

- keep shipping the features already in the repo
- close the remaining architecture gaps without rewriting the whole platform
- avoid a premature SDK rewrite while the system is still stabilizing

## 3. Remaining gaps

These are the real work items left, in order of priority.

### 3.1 Network abstraction completion

This remains the clearest architectural gap.

- storage abstraction is implemented
- network abstraction is scaffolded but not fully wired into runtime dependency injection
- concrete provider implementations and runtime integration are still required

### 3.2 Feature gating and rollout controls

The project needs explicit runtime gates for:

- managed volumes
- dynamic cloud-init
- workload telemetry
- network abstraction
- Firecracker runtime availability

This is necessary for safe staged rollout and for operator clarity when drivers or runtimes are unsupported.

### 3.3 Operational validation and documentation

The repo has strong code coverage around the new subsystems, but the project still needs:

- end-to-end validation for NFS and Ceph flows
- real failure-mode tests for cloud-init and telemetry pipelines
- deployment docs for operator-managed trust, routing, and scaling patterns

### 3.4 Production polish

The system is mature enough to benefit from:

- canonical config validation
- stricter health/readiness expectations
- better upgrade and rollback guidance
- documenting HA deployment topology behind HAProxy and etcd

## 4. Revised roadmap

### Phase A — Stabilize the shipped platform

Goal: turn the current working control plane into a predictable production baseline.

Scope:

- complete network abstraction and runtime dependency injection
- add feature flags and safe defaults for rollout
- validate scheduler HA with real failover drills
- validate Redis and ClickHouse telemetry lifecycle under load
- confirm node drain and taint behavior under active placement churn

Expected result:

- production-grade control-plane behavior with known safe flags and rollout boundaries

### Phase B — Runtime hardening and compatibility

Goal: make runtime support predictable across Docker, VM, and Firecracker.

Scope:

- formalize runtime capability negotiation
- finish Firecracker integration and validation
- harden dynamic cloud-init, mount generation, and cleanup semantics
- validate managed volume retention and failure cleanup paths

Expected result:

- stable runtime matrix with explicit compatibility contracts

### Phase C — Platform UX and GitOps

Goal: improve developer experience without destabilizing the current system.

Scope:

- clean up persysctl command ergonomics
- continue gateway-driven smart apply flows
- improve stack and YAML ingestion patterns
- formalize deployment templates and automation approval flows

Expected result:

- easier self-service deployments while preserving the current stable backend

### Phase D — Scale-out and operations excellence

Goal: keep the platform stable as cluster size and service count increase.

Scope:

- expand benchmark and load validation for 1000+ node scenarios
- optimize scheduler event and telemetry retention behavior
- tighten emergency and rollback procedures
- extend dashboards and alerting around node health, placement churn, and telemetry lag

Expected result:

- a predictable large-cluster operating model and stronger production observability

### Phase E — SDK modernization, deferred but planned

Goal: eventually make the SDK the foundation for future client and control-plane tooling.

This is intentionally not the immediate next move. The current recommendation is:

- keep the stable project shape intact
- do not force a broad SDK refactor while the active system is still stabilizing
- introduce SDK formalization only after rollout gates and runtime hardening are complete

## 5. Strategic decisions for the current moment

1. Treat the platform as already partially shipped rather than speculative.
2. Keep HA scheduler, Firecracker, vault-based mTLS, gateway gRPC discovery, and meter telemetry as real platform capabilities.
3. Fix the remaining gaps through stabilization and operational validation instead of a broad rewrite.
4. Defer full SDK migration until the system reaches a stable operating baseline.
5. Build future developer experience on the current runtime contracts rather than replacing them while they are still being validated.

## 6. Recommended project posture

The right message to the team is:

- The project is no longer at the “design-only” stage.
- The repo already contains strong control-plane and runtime functionality.
- The next phase is platform maturity, not speculative invention.

This is a strong position to continue from and should be reflected in all future planning, release notes, and architecture discussions.

