# Persys Compute

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![gRPC](https://img.shields.io/badge/gRPC-%2300B4AB.svg?logo=grpc&logoColor=white)](https://grpc.io)
[![Docker](https://img.shields.io/badge/Docker-2496ED?logo=docker&logoColor=white)](https://docker.com)

**An explainable, scheduler-driven distributed compute control plane that orchestrates containers, Docker Compose applications, and virtual machines across heterogeneous infrastructure.**

Inspired by hyperscaler architectures, Persys provides a single programmable system to manage compute from a bare-metal server to multi-node clusters — unifying fragmented infrastructure under one control plane with explainable decisions.

## The Problem

Modern infrastructure is fragmented: containers, Docker Compose apps, VMs, physical servers, and cloud instances are managed with separate tools. This increases operational complexity across deployment, debugging, scaling, resource management, and failure recovery.

Persys unifies these under **one programmable API and explainable scheduler**, reducing toolchain entropy.

## What Kind of Cloud?

Persys is an **IaaS foundation** with layered ambitions:

- **IaaS**: Compute primitives and VM lifecycle.
- **CaaS**: Container orchestration + Docker Compose.
- **Roadmap → PaaS**: Managed features like GKE/EKS.
- **Roadmap → DBaaS**: Automated database deployments.

**Vision**: A programmable compute platform exposing compute, platform, and data services through one explainable control plane.

## ✨ Core Features

- **Multi-workload**: Docker containers, full Compose stacks (Git/inline), KVM VMs.
- **Resource-aware scheduling**: CPU/memory/disk/labels/capabilities.
- **Explainable decisions**: Persys Intelligence layer with Semi-RAG reasoning.
- **Strong security**: mTLS, Vault for identity/secrets.
- **State management**: etcd (persistent) + Redis (operational).
- **Self-healing**: Leases, heartbeats, reconciliation, auto-reschedule.
- **Storage**: Ceph-backed VM disks with replication.
- **Automation**: persys-automation for scaling/remediation based on intelligence.
- **Observability**: Prometheus, structured logs, health endpoints.
- **CLI**: `persysctl` for management.

## 🚀 Quick Start

```bash
git clone https://github.com/persys-dev/persys-cloud.git && cd persys-cloud
cd infra/docker && docker compose up -d --build
cd ../../persysctl && go build -o bin/persysctl .
./bin/persysctl --transport http cluster list
```

## Architecture

**Centralized control-plane model**:

1. **persysctl** → **API Gateway** (REST, auth, routing, CoreDNS).
2. **Scheduler** (persys-scheduler): Authoritative placement, reconciliation, state.
3. **Agents** (compute-agent): Lightweight executors (no scheduling).

**Cluster**: Logical pool of nodes with telemetry; scheduler treats uniformly.

See `docs/architecture/` for diagrams.

## Scheduling

Telemetry-driven evaluation (utilization, labels, topology) → select optimal node → deploy + reconcile.

Example: "Run PostgreSQL" → ranks nodes by resources → assigns + reports.

## Cluster State

- **etcd**: Persistent memory (`/nodes/`, `/workloads/`, `/assignments/`, desired state).
- **Redis**: High-churn (events, retries, failures).

Enables crash recovery and efficient operations.

## Reliability & Self-Healing

- Heartbeats (~30s) + lease-based liveness.
- Transient partition tolerance.
- Automatic reschedule on node loss.
- Idempotent ops + explicit failures.

**VM Resilience**: Ceph 3x replication decouples storage from compute.

## Persys Intelligence & Automation

- **Intelligence**: Explainable reasoning over snapshots (state/metrics/events) → recommendations, explanations, forecasts (Semi-RAG, no direct mutation).
- **Automation**: Acts on recommendations for autoscaling, rebalancing, remediation.

**Layered Brains**:
1. Execution (Compute).
2. Reasoning (Intelligence).
3. Automation.

## Local Development

Prerequisites: Go 1.22+, Docker.

Build: Use per-component Makefiles or root `Makefile`.

Full stack via `infra/docker`.

## Contributing & Roadmap

See subdir guidelines. Priorities: storage pools, federation, secrets, advanced intelligence.

## License

MIT.

## Links

- [compute-agent](compute-agent/), [persys-scheduler](persys-scheduler/), [persys-gateway](persys-gateway/), [persys-intelligence](persys-intelligence/), [persys-automation](persys-automation/)

---

*Engineered for production-grade, explainable private/hybrid compute.*