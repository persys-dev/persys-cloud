# Persys Compute

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![gRPC](https://img.shields.io/badge/gRPC-%2300B4AB.svg?logo=grpc&logoColor=white)](https://grpc.io)
[![Docker](https://img.shields.io/badge/Docker-2496ED?logo=docker&logoColor=white)](https://docker.com)

**A lightweight, community-driven distributed compute control plane for orchestrating Docker containers, Docker Compose applications, and virtual machines across heterogeneous infrastructure.**

Persys Compute brings hyperscaler-inspired orchestration to private, hybrid, and edge environments with minimal overhead and maximum reliability.

## ✨ Features

- **Multi-workload support**: Docker containers, full Docker Compose stacks, and KVM-based VMs
- **Resource-aware scheduling**: CPU, memory, disk, and label-based placement
- **Strong security**: mTLS everywhere, Vault integration for certs and secrets
- **etcd-backed state**: Persistent, highly available cluster state
- **Explicit reconciliation**: Automatic drift detection and correction
- **Observability-first**: Prometheus metrics, structured logs, health endpoints
- **Lightweight agents**: Simple, reliable node agents with local execution
- **CLI & API**: `persysctl` for easy management
- **Federation ready**: Multi-cluster and cloud offloading support

## 🚀 Quick Start

### Using Docker Compose (Recommended for dev)

```bash
# Clone the repo
git clone https://github.com/persys-dev/persys-cloud.git
cd persys-cloud

# Start the full stack
cd infra/docker
docker compose up -d --build

# Build and use CLI
cd ../../persysctl
go build -o ./bin/persysctl .

# Check cluster status
./bin/persysctl --transport http cluster list
```

See [Local Development](#local-development) for more details.

## Architecture

Persys Compute follows a strict **control-plane / data-plane** separation:

- **Scheduler**: Authoritative brain for placement and reconciliation
- **Gateway**: REST API entrypoint with auth
- **Agents**: Dumb executors on nodes
- **etcd**: Cluster state store
- **Vault**: Identity and secret management

For full details, see [docs/architecture](docs/architecture).

## Supported Workloads

### Containers
Simple image-based workloads with resource limits, volumes, ports, etc.

### Docker Compose
Git-backed or inline YAML deployments with env/secrets injection.

### Virtual Machines
Full VM provisioning with cloud-init, storage pools, and network config.

## Local Development

### Prerequisites
- Go 1.22+
- Docker & Docker Compose
- etcd, Vault (provided via compose)

### Building Components

```bash
# Scheduler
cd persys-scheduler && make build

# Agent
cd ../compute-agent && make build

# Gateway
cd ../persys-gateway && make build
```

See individual component READMEs and root `Makefile` for more.

## Contributing

We welcome contributions! Please see contributing guidelines in subdirectories and open issues/PRs.

## Roadmap

- Full storage pool management
- Advanced retry and backoff engine
- Multi-cluster federation
- Enhanced VM networking and introspection
- Secrets management integration
- Stream-based control channels

## License

MIT License - see [LICENSE](LICENSE) file.

## Links

- [compute-agent](compute-agent) - Node runtime agent
- [persys-scheduler](persys-scheduler) - Core scheduler
- [persys-gateway](persys-gateway) - API gateway
- [persysctl](persysctl) - Command line interface

---

*Engineered for production-grade private and hybrid compute with simplicity and rigor.*