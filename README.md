# Persys Compute

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Language](https://img.shields.io/badge/language-Go-00ADD8)
![Architecture](https://img.shields.io/badge/architecture-control--plane-orange)
![Transport](https://img.shields.io/badge/transport-gRPC%20%2B%20mTLS-green)

Persys Compute is a scheduler-driven distributed compute control plane
designed to orchestrate containers, Docker Compose applications, and
virtual machines across heterogeneous infrastructure.

It is built with a clear architectural philosophy:

- Centralized scheduler (authoritative desired state)
- Dumb but reliable agents (local execution only)
- Strongly typed gRPC contracts
- mTLS everywhere
- etcd-backed persistent cluster state
- Explicit reconciliation loops
- Resource-aware placement
- Lease-based node liveness model

This repository currently lives under `persys-cloud`, but the product
name is **Persys Compute**.

------------------------------------------------------------------------

# Vision

Persys Compute is not just a container orchestrator.

It is a programmable compute control plane designed to:

- Run Docker containers
- Run Docker Compose stacks (from Git or inline spec)
- Provision and manage virtual machines
- Enforce CPU, memory, disk limits
- Reconcile drift automatically
- Scale from a single bare-metal server to multi-node clusters
- Support hybrid and federated deployments

The long-term goal is hyperscaler-style infrastructure for private and
hybrid environments --- without unnecessary abstraction or
overengineering.

------------------------------------------------------------------------

# Core Architecture

Persys Compute follows a strict control-plane model.

## Northbound (User → Control Plane)

User → Gateway (HTTP + mTLS) → Scheduler

- REST APIs for platform access
- Authentication & service identity
- Workload submission
- Administrative operations

## Southbound (Control Plane → Nodes)

Scheduler ⇄ Agent (gRPC + mTLS)

- Node registration
- Heartbeat & lease model
- Workload lifecycle execution
- Status reporting

Agents never schedule themselves. Scheduler owns all placement
decisions.

------------------------------------------------------------------------

# Control Plane Components

## persys-scheduler

Authoritative cluster brain.

Responsibilities:

- Node registration & lease management
- Workload scheduling
- Resource-aware placement
- Desired-state reconciliation
- Retry & backoff policies
- etcd state persistence
- Event emission
- Health & metrics endpoints

## persys-gateway

Platform entrypoint.

Responsibilities:

- REST APIs
- mTLS authentication
- Request routing to scheduler
- Webhook ingestion (future use)
- Platform-level authorization

## persys-cfssl

Certificate authority bootstrap.

- Service identity issuance
- mTLS trust chain
- Agent certificate provisioning

## persys-federation

Future hybrid/multi-cloud integration layer.

- Connect to AWS/GCP/other providers
- Offload workloads
- Aggregate compute resources

## persys-agent (runtime node agent)

Responsibilities:

- Register with scheduler
- Maintain heartbeat
- Enforce resource limits
- Execute containers / compose / VMs
- Report workload status
- Perform local garbage collection
- Expose Prometheus metrics

Agent is intentionally simple and execution-focused.

------------------------------------------------------------------------

# Scheduling Model

1. Agent boots
2. Agent registers with scheduler via gRPC
3. Scheduler stores node in etcd and issues lease
4. Agent sends periodic heartbeat
5. Scheduler tracks node liveness
6. User submits workload
7. Scheduler selects node based on:
    - CPU availability
    - Memory availability
    - Disk pools
    - Labels
    - Capability matching
8. Scheduler sends ApplyWorkload
9. Agent executes and reports status
10. Reconciler enforces desired state

------------------------------------------------------------------------

# Supported Workload Types

## Containers

- Image-based
- Resource limits
- Env variables
- Volumes
- Ports
- Restart policies
- Privileged mode (optional)

## Docker Compose

- Git-based deployments
- Inline YAML support
- Environment injection
- Secret injection (future: Vault integration)
- Mixed public/private images

## Virtual Machines

- vCPU & memory specification
- Disk provisioning via storage pools
- Cloud-init injection
- Network configuration
- Login credential provisioning
- Future: IP reporting back to scheduler

------------------------------------------------------------------------

# Cluster State Model (etcd)

Scheduler persists:

- /nodes/`<node-id>`{=html}
- /nodes/`<node-id>`{=html}/lease
- /workloads/`<workload-id>`{=html}
- /assignments/`<workload-id>`{=html}
- /events/`<timestamp>`{=html}
- /retries/`<workload-id>`{=html}

This enables:

- Auditability
- Recovery after scheduler restart
- Drift detection
- Retry tracking
- Failure history

------------------------------------------------------------------------

# Resource Enforcement

Agent enforces:

- CPU limits
- Memory limits
- Disk allocation
- System threshold rejection (default 80% utilization)
- Orphan resource cleanup
- Zombie workload detection

Workloads are rejected early if capacity is insufficient.

------------------------------------------------------------------------

# Reliability Model

Persys Compute uses:

- Lease-based node liveness
- Heartbeat TTL enforcement
- Idempotent workload operations
- Explicit failure reasons (enum-based)
- Garbage collection loops
- Structured error propagation
- Metrics-first observability

No ghost workloads. No silent failures. No hidden retries.

------------------------------------------------------------------------

# Observability

Each component exposes:

- /metrics (Prometheus)
- /health
- Structured logs

Metrics include:

- Workload counts
- Failure reasons
- Apply duration
- Resource utilization
- Runtime health status
- GC statistics

------------------------------------------------------------------------

# Local Development

Prerequisites:

- Go 1.24+
- Docker

Start etcd:

``` bash
./hack/start-etcd-docker.sh
```

Run scheduler (dev mode):

``` bash
cd persys-scheduler
go run ./cmd/scheduler -insecure
```

Build services:

``` bash
cd persys-scheduler && go build ./cmd/scheduler
cd persys-gateway && go build ./cmd/main.go
```

------------------------------------------------------------------------

# Project Philosophy

Persys Compute is built around:

- Explicit contracts (protobuf-first design)
- Control-plane correctness
- Minimal but powerful abstraction
- Hyperscaler-inspired architecture
- Avoiding unnecessary AI buzz
- Strong separation of concerns
- Scalability from day one

This is infrastructure designed to scale --- without rewriting the
system when adding more nodes.

------------------------------------------------------------------------

# Roadmap Highlights

- Storage pool full implementation
- VM network introspection & IP reporting
- Retry engine with exponential backoff
- Federation workload offloading
- Secrets integration (Vault)
- Stream-based control channel
- Multi-cluster support

------------------------------------------------------------------------

# License

MIT
