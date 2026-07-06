# Getting Started with Persys Compute Agent

This guide will help you get the Persys Compute Agent up and running quickly.

## Prerequisites

### Required
- Go 1.21+ (for building from source)
- Docker Engine 20.10+ (for container workloads)

### Optional
- docker-compose 2.0+ (for compose workloads)
- KVM/libvirt (for VM workloads)
- protoc compiler (for development)

## Quick Start (5 minutes)

### 1. Download or Build

**Option A: Download Release (Recommended)**

```bash
# Download latest release
curl -L https://github.com/persys/compute-agent/releases/latest/download/persys-agent-linux-amd64 \
  -o persys-agent
chmod +x persys-agent
```

**Option B: Build from Source**

```bash
# Clone repository
git clone https://github.com/persys/compute-agent.git
cd compute-agent

# Install dependencies and build
make deps
make proto
make build

# Binary will be in bin/persys-agent
```

### 2. Generate Development Certificates

For development/testing, you can run without TLS or generate self-signed certificates:

```bash
# Option A: Run without TLS (development only)
export PERSYS_TLS_ENABLED=false

# Option B: Generate self-signed certificates
mkdir -p dev/certs
cd dev/certs

# Generate CA
openssl req -x509 -newkey rsa:4096 -keyout ca.key -out ca.crt -days 365 -nodes \
  -subj "/CN=Persys Dev CA"

# Generate agent certificate
openssl req -newkey rsa:2048 -keyout agent.key -out agent.csr -nodes \
  -subj "/CN=localhost"
openssl x509 -req -in agent.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out agent.crt -days 365 -sha256

cd ../..
```

### 3. Run the Agent

```bash
# With TLS disabled (development only)
PERSYS_TLS_ENABLED=false \
PERSYS_STATE_PATH=./dev/state.db \
./bin/persys-agent

# Or with TLS
PERSYS_TLS_CERT=dev/certs/agent.crt \
PERSYS_TLS_KEY=dev/certs/agent.key \
PERSYS_TLS_CA=dev/certs/ca.crt \
PERSYS_STATE_PATH=./dev/state.db \
./bin/persys-agent
```

You should see output like:

```
INFO[0000] Starting Persys Compute Agent v1.0.0
INFO[0000] Node ID: your-hostname
INFO[0000] Initializing state store...
INFO[0000] Initializing runtimes...
INFO[0000] Docker runtime enabled
INFO[0000] Starting gRPC server on 0.0.0.0:50051
INFO[0000] Persys Compute Agent is ready
```

### 4. Test the Agent

In another terminal, use grpcurl to test:

```bash
# Install grpcurl if needed
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# Health check (without TLS)
grpcurl -plaintext localhost:50051 persys.agent.v1.AgentService/HealthCheck

# With TLS
grpcurl \
  -cert dev/certs/agent.crt \
  -key dev/certs/agent.key \
  -cacert dev/certs/ca.crt \
  localhost:50051 persys.agent.v1.AgentService/HealthCheck
```

## Your First Workload

### Create a Container

```bash
# Using the example client
cd examples/client
go run main.go -insecure -action apply -id test-nginx

# Or using grpcurl
grpcurl -plaintext -d '{
  "id": "test-nginx",
  "type": "WORKLOAD_TYPE_CONTAINER",
  "revision_id": "rev-1",
  "desired_state": "DESIRED_STATE_RUNNING",
  "spec": {
    "container": {
      "image": "nginx:latest",
      "ports": [
        {
          "host_port": 8080,
          "container_port": 80,
          "protocol": "tcp"
        }
      ]
    }
  }
}' localhost:50051 persys.agent.v1.AgentService/ApplyWorkload
```

### Check Status

```bash
# List all workloads
grpcurl -plaintext localhost:50051 persys.agent.v1.AgentService/ListWorkloads

# Get specific workload status
grpcurl -plaintext -d '{"id": "test-nginx"}' \
  localhost:50051 persys.agent.v1.AgentService/GetWorkloadStatus
```

### Verify Container is Running

```bash
# Check Docker
docker ps | grep test-nginx

# Test nginx
curl http://localhost:8080
```

### Delete Workload

```bash
grpcurl -plaintext -d '{"id": "test-nginx"}' \
  localhost:50051 persys.agent.v1.AgentService/DeleteWorkload
```

## Configuration

The agent is configured via environment variables. Here are the most important ones:

### Essential Configuration

```bash
# Server
export PERSYS_GRPC_PORT=50051

# Scheduler control plane
export PERSYS_SCHEDULER_ADDR=127.0.0.1:8085
export PERSYS_SCHEDULER_INSECURE=true  # use only for local testing

# TLS
export PERSYS_TLS_ENABLED=true
export PERSYS_TLS_CERT=/path/to/agent.crt
export PERSYS_TLS_KEY=/path/to/agent.key
export PERSYS_TLS_CA=/path/to/ca.crt

# State
export PERSYS_STATE_PATH=/var/lib/persys/state.db

# Logging
export PERSYS_LOG_LEVEL=info

# Optional node metadata/labels advertised during RegisterNode
export PERSYS_NODE_REGION=us-east-1
export PERSYS_NODE_ENV=staging
export PERSYS_NODE_LABELS=team=platform,zone=use1-az2
```

### Runtime Configuration

```bash
# Docker
export PERSYS_DOCKER_ENABLED=true
export DOCKER_HOST=unix:///var/run/docker.sock

# Compose
export PERSYS_COMPOSE_ENABLED=true
export PERSYS_COMPOSE_BINARY=docker-compose

# VMs
export PERSYS_VM_ENABLED=true
export PERSYS_LIBVIRT_URI=qemu:///system
```

### Reconciliation

```bash
export PERSYS_RECONCILE_ENABLED=true
export PERSYS_RECONCILE_INTERVAL=30s
```

## Common Tasks

### Run as Systemd Service

```bash
# Copy binary
sudo cp bin/persys-agent /usr/local/bin/

# Create systemd service
sudo tee /etc/systemd/system/persys-agent.service << 'EOF'
[Unit]
Description=Persys Compute Agent
After=network.target docker.service

[Service]
Type=simple
User=root
Environment="PERSYS_STATE_PATH=/var/lib/persys/state.db"
ExecStart=/usr/local/bin/persys-agent
Restart=always

[Install]
WantedBy=multi-user.target
EOF

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable persys-agent
sudo systemctl start persys-agent

# Check status
sudo systemctl status persys-agent
journalctl -u persys-agent -f
```

### Run in Docker

```bash
docker run -d \
  --name persys-agent \
  --privileged \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/persys:/var/lib/persys \
  -p 50051:50051 \
  -e PERSYS_TLS_ENABLED=false \
  persys/compute-agent:latest
```

### Enable Debugging

```bash
# Set log level to debug
export PERSYS_LOG_LEVEL=debug

# Or for running instance
sudo systemctl edit persys-agent
# Add: Environment="PERSYS_LOG_LEVEL=debug"
sudo systemctl restart persys-agent
```

## Development Workflow

### 1. Make Code Changes

```bash
# Edit files in internal/, pkg/, etc.
vim internal/runtime/docker.go
```

### 2. Run Tests

```bash
make test
```

### 3. Format and Lint

```bash
make fmt
make lint
```

### 4. Build

```bash
make build
```

### 5. Test Locally

```bash
PERSYS_TLS_ENABLED=false ./bin/persys-agent
```

## Next Steps

- **Architecture**: Read [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) for detailed architecture
- **Deployment**: See [docs/DEPLOYMENT.md](../docs/DEPLOYMENT.md) for production deployment
- **API Reference**: Check [api/proto/agent.proto](../api/proto/agent.proto) for full API
- **Contributing**: Read [CONTRIBUTING.md](../CONTRIBUTING.md) to contribute

## Troubleshooting

### Agent Won't Start

**Check permissions:**
```bash
# Docker socket
ls -l /var/run/docker.sock
sudo usermod -aG docker $USER

# Libvirt socket
ls -l /var/run/libvirt/libvirt-sock
sudo usermod -aG libvirt $USER
```

**Check logs:**
```bash
# If running as systemd
journalctl -u persys-agent -n 50

# If running in foreground
# Look at terminal output
```

### Workload Won't Start

**Check workload status:**
```bash
grpcurl -plaintext -d '{"id": "your-workload"}' \
  localhost:50051 persys.agent.v1.AgentService/GetWorkloadStatus
```

**Check runtime:**
```bash
# For containers
docker ps -a
docker logs <container-id>

# For VMs
virsh list --all
virsh dominfo <vm-name>
```

### TLS Errors

**Verify certificates:**
```bash
# Check certificate
openssl x509 -in agent.crt -text -noout

# Verify with CA
openssl verify -CAfile ca.crt agent.crt

# Test connection
openssl s_client -connect localhost:50051 \
  -cert agent.crt -key agent.key -CAfile ca.crt
```

## Getting Help

- **Documentation**: Check docs/ directory
- **Examples**: See examples/ directory
- **Issues**: https://github.com/persys/compute-agent/issues
- **Discussions**: https://github.com/persys/compute-agent/discussions
- **Email**: support@persys.cloud

## Resources

- [Main README](../README.md)
- [Architecture Guide](../docs/ARCHITECTURE.md)
- [Deployment Guide](../docs/DEPLOYMENT.md)
- [Contributing Guide](../CONTRIBUTING.md)
- [API Documentation](../api/proto/agent.proto)
