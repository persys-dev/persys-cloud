### Persys Cloud – Master Development Plan

**Status**: Final Consolidated Plan **Date**: 2026-06-25 **Goal**: Evolve Persys into a production-grade, developer-friendly, scalable lightweight compute platform.

#### 1\. Strategic Foundations

**1.1 Go SDK First (Mandatory Phase 0)**

- Create a clean sdk/ module at repository root.
- Move all business logic (client, ingestion, gitops, types) into the SDK.
- Make persysctl a **thin Cobra wrapper** only (fix the current "dirty quick & dirty" state).
- All future features (GitOps, Stack, Metrics, etc.) must go through the SDK.

**Key Packages**:

- sdk/client/ — HTTP + gRPC transport, mTLS, retry, tracing
- sdk/types/ — Shared models

#### 2\. Major Features to Implement

**2.1 Developer Experience & GitOps**

- Full YAML support with rich validation.
- Docker Compose support (-f docker-compose.yml + base64 encoded compose).
- persysctl init, interactive wizard, templates.
- PersysStack declarative format (workloads + volumes + automation + basic infra).
- persysctl gitops watch \<local-path | git-url> (polling + fsnotify, support persys-\*.yml and compose files).
- Smart persysctl apply that auto-detects format (stack, compose, single workload, git).

**2.2 Operational Controls**

- Node Drain & Taint (full RPCs, scheduler logic, placement exclusion, eviction, persysctl commands).
- Heartbeat fix to respect Draining status.

**2.3 Observability**

- Per-workload utilization telemetry (CPU, memory, disk I/O, network).
- Implement persys-meter service.
- Metrics path: Agent → Scheduler → persys-meter.
- persysctl workload metrics command.
- Enhance Prometheus labels in compute-agent.

**2.4 Gateway Enhancements**

- Smart /apply endpoint using SDK ingestion.
- Deep integration with existing GitHub login + OAuth.
- Enhanced GitHub webhooks (POST /webhooks/github) to trigger GitOps apply on push/PR.
- New routes for stacks and gitops operations.

**2.5 Scaling (1000+ nodes)**

- Scheduler: etcd-based leader election + read replicas.
- Workload & node sharding (consistent hashing).
- Event-driven reconciliation (etcd watches + Redis streams instead of full scans).
- Hot state caching + indexing.
- Performance testing harness.

**2.6 Runtime & Storage**

- Full managed volume integration in agent runtime (NFS + Ceph-RBD).
- Dynamic cloud-init for VMs (full user-data, meta-data, network-config, vendor-data).
- Runtime abstraction (storage & network providers).
- VM / Firecracker symmetric UX inside PersysStack (lower priority).

**2.7 Ecosystem**

- Terraform / OpenTofu Provider.
- Better documentation and examples.

#### 3\. Prioritized Roadmap

**Phase 0: Foundation (2–3 weeks)**

- Go SDK creation + persysctl refactor (highest priority).

**Phase 1: Operational Excellence (3–4 weeks)**

- Node Drain & Taint.
- persys-meter + per-workload metrics.
- Smart ingestion + basic UX (YAML, Compose, base64, wizard, init).

**Phase 2: GitOps & PersysStack (3–4 weeks)**

- PersysStack kind + reconciliation.
- gitops watch (local + remote Git).
- Gateway GitHub webhook & smart apply enhancements.

**Phase 3: Production Scaling (3–5 weeks)**

- Leader election, sharding, event-driven reconciler.
- Load testing for 1000+ nodes.

**Phase 4: Runtime & Advanced Features (3–5 weeks)**

- Full managed volumes in agent + dynamic cloud-init.
- Firecracker runtime (lower priority).
- Terraform provider.

**Phase 5: Polish & Future**

- Standalone volumes, quotas, RBAC, etc.

#### 4\. Cross-Cutting Requirements

- **Proto-first** for all new APIs.
- **Backward compatibility** everywhere.
- **Feature flags** for gradual rollout.
- **Observability** — every component must expose Prometheus metrics.
- **Testing** — Unit + Integration + GitHub Actions (minimal local resource usage).

#### 5\. Key Architectural Decisions

- persysctl = thin wrapper around SDK.
- Gateway = smart ingestion + GitHub integration layer.
- Scheduler = leader + sharded + event-driven.
- GitOps = first-class citizen (gitops watch + webhooks).
- UX = PersysStack + Git-first workflows.


### Strategic Decision (Agreed)

1. First, build a clean **Go Client SDK** (persys-go-sdk).
2. Refactor persysctl to be a thin, high-quality wrapper around the SDK.
3. Then implement all new features (GitOps, PersysStack, etc.) on top of the clean SDK.

---

### Phase 0: Foundation – Clean SDK + persysctl Refactor (2–3 weeks)

**Goal**: Eliminate the current "dirty quick & dirty" persysctl.

**Tasks**:

1. **Create persys-go-sdk**
	- New directory / module at root: sdk/
		- Package structure:
		- sdk/client/ – Core client with HTTP + gRPC transport
				- sdk/types/ – All models & request/response structs (generated from proto where possible)
				- sdk/ingestion/ – Format converters (YAML, JSON, Compose, base64, Git)
				- sdk/gitops/ – GitOps primitives
				- sdk/options/ – Configuration, auth, retry, tracing
		- Full support for mTLS, dual transport, context, pagination, dry-run, etc.
2. **Refactor persysctl**
	- Make persysctl **thin wrapper** only (Cobra commands + output formatting).
		- Move all business logic into the SDK.
		- Update persysctl/internal/client/ → delegate to SDK.
		- Clean up command structure (workload, stack, gitops, vm, node, etc.).

**Key Files**:

- sdk/client/client.go
- persysctl/cmd/\*.go (major cleanup)
- persysctl/internal/config/

---

### Phase 1: Operational Excellence & UX (3–5 weeks)

**1.1 Node Drain & Taint**

- Extend control.proto (DrainNode, TaintNode, etc.)
- Scheduler: node\_control.go + placement + heartbeat fix
- Gateway: New routes + handlers
- SDK + persysctl: node drain, node taint, node untaint

**1.2 Metrics & persys-meter**

- Implement persys-meter service (use previous design doc)
- Add GetWorkloadMetrics RPC
- Agent → Scheduler → persys-meter push
- SDK + persysctl: workload metrics command

**1.3 Smart Workload Ingestion & UX**

- Full YAML support + validation
- Docker Compose support (-f docker-compose.yml)
- Base64 encoded compose support
- Interactive wizard (--interactive)
- persysctl init command
- Templates system

---

### Phase 2: GitOps & PersysStack (3–4 weeks)

**2.1 PersysStack Kind**

- Add PersysStack to control.proto
- Scheduler support for stack-level reconciliation, dependencies, automation rules
- etcd paths: /stacks/{name}/

**2.2 GitOps Capabilities**

- persysctl gitops watch <local-path|git-url>
- Support for persys-\*.yml, docker-compose.yml, VM specs
- Polling + fsnotify + git pull logic (in SDK)
- Dry-run, rollback, commit tracking

**2.3 VM / Firecracker UX**

- Symmetric YAML experience inside PersysStack
- persysctl vm create, templates, cloud-init support

---

### Phase 3: Runtime & Scaling (4–6 weeks)

**3.1 Firecracker VM Runtime**

- VM runtime abstraction in compute-agent
- Firecracker backend implementation
- Scheduler capability matching
- Integration with managed volumes + metrics

**3.2 Horizontal Scaling**

- Scheduler: etcd leader election + leases + write forwarding
- Gateway: Enhance CoreDNS usage for leader-aware routing
- Multi-replica support in docker-compose + CI

---

### Phase 4: Ecosystem (3–5 weeks)

- **Terraform / OpenTofu Provider**
	- terraform-provider-persys
		- Resources for Stack, Workload, Node, Volume
- Documentation & Examples
	- Full persys-stack.yaml reference
		- GitOps guides

---

### Phase 5: Advanced (Ongoing)

- Standalone volumes
- Quota / auto-scaling (via persys-meter)
- RBAC / multi-tenancy
- Advanced Firecracker features
- persys-operator improvements
- Marketplace / template registry

---

### Priority Order (Recommended Execution)

| Priority | Phase / Feature | Estimated Effort | Blocking |
| --- | --- | --- | --- |
| 1 | Phase 0: Go SDK + persysctl refactor | 2–3 weeks | All future work |
| 2 | Phase 1.1: Node Drain/Taint | 1–2 weeks | Operational |
| 3 | Phase 1.2: persys-meter + metrics | 2 weeks | Observability |
| 4 | Phase 1.3: Smart Ingestion + UX | 2 weeks | User experience |
| 5 | Phase 2: GitOps + PersysStack | 3–4 weeks | Major UX win |
| 6 | Phase 3.1: Firecracker | 3–4 weeks | Runtime strength |
| 7 | Phase 3.2: Scheduler Scaling | 4 weeks | Production readiness |
| 8 | Phase 4: Terraform Provider | 3 weeks | Enterprise |

---

### Immediate Next Steps (This Week)

1. Initialize the sdk/ module and move core client logic from persysctl.
2. Define clean interfaces in the SDK.
3. Start with YAML + Compose support in the SDK.
