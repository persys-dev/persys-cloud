# Persys Compute Agent

The Persys Compute Agent is a production-grade node-level execution engine for the Persys Cloud platform. It manages Docker containers, Docker Compose applications, and KVM virtual machines with idempotent operations, mTLS security, and automatic crash recovery.

## Features

- **Multi-Runtime Support**: Docker containers, Docker Compose, and KVM/libvirt VMs
- **Managed Volumes**: NFS and Ceph-RBD storage provisioning with automatic lifecycle management
- **Idempotent Operations**: Revision-based tracking prevents duplicate work
- **Secure Communication**: gRPC with mutual TLS authentication
- **Persistent State**: bbolt-backed state store for crash recovery
- **Auto Reconciliation**: Periodic state synchronization and self-healing
- **Production Ready**: Comprehensive error handling, logging, and monitoring

## Architecture

```
┌─────────────────┐
│   Scheduler     │
│   (Control)     │
└────────┬────────┘
         │ gRPC/mTLS
         ▼
┌─────────────────────────────────────┐
│      Persys Compute Agent           │
│                                     │
│  ┌──────────┐    ┌──────────────┐  │
│  │  gRPC    │◄──►│  Workload    │  │
│  │  Server  │    │  Manager     │  │
│  └──────────┘    └──────┬───────┘  │
│                          │          │
│  ┌───────────────────────┴────┐    │
│  │     Runtime Manager        │    │
│  ├────────────────────────────┤    │
│  │ Docker │ Compose │   VM    │    │
│  └────────┴─────────┴─────────┘    │
│                                     │
│  ┌──────────────┐ ┌─────────────┐  │
│  │ State Store  │ │ Reconciler  │  │
│  │   (bbolt)    │ │    Loop     │  │
│  └──────────────┘ └─────────────┘  │
└─────────────────────────────────────┘
         │            │          │
         ▼            ▼          ▼
    [Docker]    [Compose]   [libvirt]
```

## Quick Start

### Prerequisites

- Go 1.21 or later
- Docker Engine
- docker-compose (optional)
- KVM/libvirt (optional)
- protoc compiler

### Installation

```bash
# Clone the repository
git clone https://github.com/persys/compute-agent.git
cd compute-agent

# Install dependencies
make deps

# Build the agent (protobuf files are pre-generated and included)
make build

# The binary will be in bin/persys-agent
```

**Note:** Protobuf files are already generated and included in `pkg/api/v1/`. You only need to run `make proto` if you modify the `.proto` file.

### Running the Agent

```bash
# Generate development certificates
make dev-certs

# Run with development configuration
PERSYS_TLS_CERT=dev/certs/agent.crt \
PERSYS_TLS_KEY=dev/certs/agent.key \
PERSYS_TLS_CA=dev/certs/ca.crt \
./bin/persys-agent
```

## Configuration

The agent is configured via environment variables:

### Server Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PERSYS_GRPC_ADDR` | `0.0.0.0` | gRPC bind address |
| `PERSYS_GRPC_PORT` | `50051` | gRPC port |
| `PERSYS_METRICS_PORT` | `8089` | Prometheus metrics port |

### TLS/mTLS Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PERSYS_TLS_ENABLED` | `true` | Enable mTLS authentication |
| `PERSYS_TLS_CERT` | `/etc/persys/certs/agent/compute-agent.pem` | Server certificate path |
| `PERSYS_TLS_KEY` | `/etc/persys/certs/agent/compute-agent-key.pem` | Server private key path |
| `PERSYS_TLS_CA` | `/etc/persys/certs/agent/ca.pem` | CA certificate path |

### Vault Certificate Manager Configuration

When enabled, the agent retrieves and rotates certificates from Vault.
Rotation occurs at 80% of certificate lifetime, and if Vault is unavailable
the agent falls back to manual certificates on disk.

| Variable | Default | Description |
|----------|---------|-------------|
| `PERSYS_VAULT_ENABLED` | `false` | Enable Vault-backed certificate manager |
| `PERSYS_VAULT_ADDR` | `http://127.0.0.1:8200` | Vault API address |
| `PERSYS_VAULT_AUTH_METHOD` | `token` | Auth mode: `token` or `approle` |
| `PERSYS_VAULT_TOKEN` | `` | Vault token for `token` auth |
| `PERSYS_VAULT_APPROLE_ROLE_ID` | `` | AppRole role_id for `approle` auth |
| `PERSYS_VAULT_APPROLE_SECRET_ID` | `` | AppRole secret_id for `approle` auth |
| `PERSYS_VAULT_PKI_MOUNT` | `pki` | Vault PKI mount path |
| `PERSYS_VAULT_PKI_ROLE` | `compute-agent` | Vault PKI role name |
| `PERSYS_VAULT_CERT_TTL` | `24h` | Requested certificate TTL |
| `PERSYS_VAULT_RETRY_INTERVAL` | `1m` | Retry interval when Vault is unavailable |
| `PERSYS_VAULT_SERVICE_NAME` | `compute-agent` | Primary service name added to DNS SANs |
| `PERSYS_VAULT_SERVICE_DOMAIN` | `` | Optional domain appended to service/host SANs |

### State Store Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PERSYS_STATE_PATH` | `/var/lib/persys/state.db` | bbolt database path |

### Runtime Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PERSYS_DOCKER_ENABLED` | `true` | Enable Docker runtime |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker socket |
| `PERSYS_COMPOSE_ENABLED` | `true` | Enable Compose runtime |
| `PERSYS_COMPOSE_BINARY` | `docker-compose` | Compose binary path |
| `PERSYS_VM_ENABLED` | `true` | Enable VM runtime |
| `PERSYS_LIBVIRT_URI` | `qemu:///system` | Libvirt connection URI |

### Reconciliation Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PERSYS_RECONCILE_ENABLED` | `true` | Enable reconciliation loop |
| `PERSYS_RECONCILE_INTERVAL` | `30s` | Reconciliation interval |

## Retry and Backoff Strategy

The compute agent uses backoff/recovery at three levels:

### 1) Scheduler control-plane reconnect backoff

When node registration/heartbeat connection to scheduler fails:

- starts at `1s`
- doubles on each failure
- capped at `30s`
- resets to `1s` after a successful registration.

### 2) Local reconcile start retry backoff

When desired state is `Running` but runtime start fails during reconcile:

- retry policy defaults:
  - `MaxAttempts=3`
  - `InitialDelay=5s`
  - `BackoffMultiplier=2`
  - `MaxDelay=2m`
  - only transient failures are retried automatically
- next retry delays follow exponential backoff (`5s`, `10s`, `20s` with default attempts).
- status metadata includes:
  - `retry_attempts`
  - `next_retry_time`
  - `failure_reason`
  - `last_error`

### 3) Pending-state recovery timeout

If a workload remains `pending` too long, the agent runs recovery:

- pending threshold: `5m`
- recovery action: stop + restart attempt
- if restart fails: delete from runtime and mark workload status `failed`
- agent then sets workload desired state to `Stopped` locally to avoid repeated restart loops on that node.
- status metadata includes:
  - `pending_recovery_action`
  - `pending_recovery_reason`
  - `pending_recovery_deleted`

### Logging Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PERSYS_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |

### Agent Metadata

| Variable | Default | Description |
|----------|---------|-------------|
| `PERSYS_NODE_ID` | hostname | Unique node identifier |
| `PERSYS_VERSION` | `dev` | Agent version |
| `PERSYS_NODE_REGION` | `` | Node region label (ex: `us-east-1`) |
| `PERSYS_NODE_ENV` | `` | Node environment label (ex: `prod`, `staging`) |
| `PERSYS_NODE_LABELS` | `` | Extra labels as `key=value,key2=value2` |
| `PERSYS_AGENT_GRPC_ENDPOINT` | auto-derived | Scheduler-reachable agent endpoint (`host:port`) |

### Scheduler Control Plane

| Variable | Default | Description |
|----------|---------|-------------|
| `PERSYS_SCHEDULER_ADDR` | `127.0.0.1:8085` | Scheduler gRPC control endpoint (`host:port`) |
| `PERSYS_SCHEDULER_INSECURE` | `false` | Disable TLS for scheduler control client (testing only) |

## API Reference

### gRPC Service

The agent exposes a gRPC service with the following methods:

#### ApplyWorkload

Creates or updates a workload with idempotent revision tracking.

```protobuf
rpc ApplyWorkload(ApplyWorkloadRequest) returns (ApplyWorkloadResponse)
```

**Request:**
- `id`: Unique workload identifier
- `type`: Workload type (container, compose, vm)
- `revision_id`: Revision for idempotency
- `desired_state`: Running or Stopped
- `spec`: Runtime-specific configuration

**Response:**
- `applied`: Whether workload was applied
- `skipped`: True if revision already applied
- `status`: Current workload status

#### DeleteWorkload

Removes a workload and cleans up resources.

```protobuf
rpc DeleteWorkload(DeleteWorkloadRequest) returns (DeleteWorkloadResponse)
```

#### GetWorkloadStatus

Retrieves current status of a workload.

```protobuf
rpc GetWorkloadStatus(GetWorkloadStatusRequest) returns (GetWorkloadStatusResponse)
```

#### ListWorkloads

Lists all managed workloads, optionally filtered by type.

```protobuf
rpc ListWorkloads(ListWorkloadsRequest) returns (ListWorkloadsResponse)
```

#### HealthCheck

Returns agent health and runtime status.

```protobuf
rpc HealthCheck(HealthCheckRequest) returns (HealthCheckResponse)
```

## Workload Specifications

### Container Spec

```json
{
  "image": "nginx:latest",
  "command": ["/bin/sh"],
  "args": ["-c", "nginx -g 'daemon off;'"],
  "env": {
    "ENV_VAR": "value"
  },
  "volumes": [
    {
      "host_path": "/data",
      "container_path": "/app/data",
      "read_only": false
    }
  ],
  "ports": [
    {
      "host_port": 8080,
      "container_port": 80,
      "protocol": "tcp"
    }
  ],
  "resources": {
    "cpu_shares": 1024,
    "memory_bytes": 536870912
  },
  "restart_policy": {
    "policy": "unless-stopped",
    "max_retry_count": 3
  }
}
```

### Compose Spec

```json
{
  "project_name": "myapp",
  "compose_yaml": "<base64-encoded-docker-compose.yml>",
  "env": {
    "DATABASE_URL": "postgres://..."
  }
}
```

### VM Spec

```json
{
  "name": "web-vm",
  "vcpus": 4,
  "memory_mb": 8192,
  "disks": [
    {
      "path": "/var/lib/libvirt/images/web-vm.qcow2",
      "device": "vda",
      "format": "qcow2",
      "size_gb": 50
    }
  ],
  "networks": [
    {
      "network": "default",
      "mac_address": "52:54:00:12:34:56",
      "ip_address": "192.168.122.100"
    }
  ],
  "cloud_init": "<base64-encoded-cloud-init-user-data>"
}
```

## Development

### Project Structure

```
persys-compute-agent/
├── cmd/
│   └── agent/           # Main entry point
├── internal/
│   ├── config/          # Configuration management
│   ├── grpc/            # gRPC server implementation
│   ├── reconcile/       # Reconciliation loop
│   ├── runtime/         # Runtime implementations
│   │   ├── docker.go    # Docker runtime
│   │   ├── compose.go   # Compose runtime
│   │   └── vm.go        # VM runtime
│   ├── state/           # State store (bbolt)
│   └── workload/        # Workload manager
├── pkg/
│   ├── api/             # Generated protobuf code
│   └── models/          # Data models
├── api/
│   └── proto/           # Protobuf definitions
├── Dockerfile
├── Makefile
└── README.md
```

### Building

```bash
# Build for current platform
make build

# Build for Linux (useful for macOS/Windows dev)
make build-linux

# Run unit + end-to-end tests
make test

# Run only unit tests
make test-unit

# Run only end-to-end tests
make test-e2e

# Format code
make fmt

# Run linters
make lint
```


### CI/CD

A GitHub Actions workflow is available at `.github/workflows/ci.yml` and runs:

- Unit tests via `make test-unit`
- End-to-end tests via `make test-e2e`

Both jobs run on pull requests and pushes to the default development branches.

### Docker Build

```bash
# Build Docker image
make docker-build

# Push to registry
make docker-push
```

## Deployment

### Systemd Service

Create `/etc/systemd/system/persys-agent.service`:

```ini
[Unit]
Description=Persys Compute Agent
After=network.target docker.service libvirtd.service

[Service]
Type=simple
User=root
Environment="PERSYS_NODE_ID=%H"
Environment="PERSYS_STATE_PATH=/var/lib/persys/state.db"
Environment="PERSYS_TLS_CERT=/etc/persys/certs/agent.crt"
Environment="PERSYS_TLS_KEY=/etc/persys/certs/agent.key"
Environment="PERSYS_TLS_CA=/etc/persys/certs/ca.crt"
ExecStart=/usr/local/bin/persys-agent
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable persys-agent
sudo systemctl start persys-agent
```

### Docker Deployment

```bash
docker run -d \
  --name persys-agent \
  --privileged \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/run/libvirt:/var/run/libvirt \
  -v /var/lib/persys:/var/lib/persys \
  -v /etc/persys/certs:/etc/persys/certs:ro \
  -p 50051:50051 \
  -e PERSYS_NODE_ID=$(hostname) \
  persys/compute-agent:latest
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: persys-agent
  namespace: persys-system
spec:
  selector:
    matchLabels:
      app: persys-agent
  template:
    metadata:
      labels:
        app: persys-agent
    spec:
      hostNetwork: true
      hostPID: true
      containers:
      - name: agent
        image: persys/compute-agent:latest
        securityContext:
          privileged: true
        env:
        - name: PERSYS_NODE_ID
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        volumeMounts:
        - name: docker-socket
          mountPath: /var/run/docker.sock
        - name: libvirt-socket
          mountPath: /var/run/libvirt
        - name: state
          mountPath: /var/lib/persys
        - name: certs
          mountPath: /etc/persys/certs
          readOnly: true
      volumes:
      - name: docker-socket
        hostPath:
          path: /var/run/docker.sock
      - name: libvirt-socket
        hostPath:
          path: /var/run/libvirt
      - name: state
        hostPath:
          path: /var/lib/persys
      - name: certs
        secret:
          secretName: persys-agent-certs
```

## Security

### mTLS Authentication

The agent uses mutual TLS for secure communication:

1. **Certificate Generation**: Use CFSSL or OpenSSL to generate certificates
2. **Certificate Distribution**: Deploy certificates via secret management (Vault, etc.)
3. **Certificate Rotation**: Implement automated rotation for production

### Least Privilege

- Run agent as non-root where possible
- Use Docker socket with appropriate permissions
- Configure libvirt access control
- Implement RBAC for scheduler authentication

### Secret Management

Secrets can be injected via:
- Environment variables (from scheduler)
- Vault integration (optional)
- Kubernetes secrets
- Encrypted at rest in state store (future)

## Monitoring

### Health Check

```bash
grpcurl -plaintext localhost:50051 \
  persys.agent.v1.AgentService/HealthCheck
```

### Metrics (Future)

- Workload count by state
- Runtime operation latency
- Reconciliation cycle duration
- State store operations
- gRPC request rate/latency

### Logging

The agent uses structured logging with configurable levels:

```bash
PERSYS_LOG_LEVEL=debug ./bin/persys-agent
```

## Troubleshooting

### Agent won't start

Check logs for:
- TLS certificate issues
- State store permissions
- Runtime availability (Docker/libvirt)

### Workloads not starting

1. Check workload status: `GetWorkloadStatus`
2. Verify runtime is healthy
3. Check reconciliation logs
4. Inspect state store

### High CPU usage

- Reduce reconciliation frequency
- Check for stuck workloads
- Review runtime performance

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Run `make test` and `make lint`
6. Submit a pull request

## License

Copyright © 2024 Persys Cloud

## Support

For issues and questions:
- GitHub Issues: https://github.com/persys/compute-agent/issues
- Documentation: https://docs.persys.cloud
- Email: support@persys.cloud
