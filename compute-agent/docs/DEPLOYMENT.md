# Deployment Examples

This directory contains example configurations and deployment scenarios for the Persys Compute Agent.

## Example Configurations

### 1. Basic Configuration

**File: `.env.basic`**
```bash
# Server
PERSYS_GRPC_ADDR=0.0.0.0
PERSYS_GRPC_PORT=50051

# TLS (development mode - insecure)
PERSYS_TLS_ENABLED=false

# State
PERSYS_STATE_PATH=/var/lib/persys/state.db

# Runtimes (all enabled)
PERSYS_DOCKER_ENABLED=true
PERSYS_COMPOSE_ENABLED=true
PERSYS_VM_ENABLED=true

# Reconciliation
PERSYS_RECONCILE_ENABLED=true
PERSYS_RECONCILE_INTERVAL=30s

# Logging
PERSYS_LOG_LEVEL=info
```

### 2. Production Configuration

**File: `.env.production`**
```bash
# Server
PERSYS_GRPC_ADDR=0.0.0.0
PERSYS_GRPC_PORT=50051

# TLS (production - secure)
PERSYS_TLS_ENABLED=true
PERSYS_TLS_CERT=/etc/persys/certs/agent.crt
PERSYS_TLS_KEY=/etc/persys/certs/agent.key
PERSYS_TLS_CA=/etc/persys/certs/ca.crt

# State
PERSYS_STATE_PATH=/var/lib/persys/state.db

# Runtimes
PERSYS_DOCKER_ENABLED=true
PERSYS_COMPOSE_ENABLED=true
PERSYS_VM_ENABLED=false  # VMs on dedicated hosts only

# Reconciliation
PERSYS_RECONCILE_ENABLED=true
PERSYS_RECONCILE_INTERVAL=60s  # Less frequent in production

# Logging
PERSYS_LOG_LEVEL=warn
PERSYS_NODE_ID=prod-node-01
```

### 3. Container-Only Configuration

**File: `.env.containers-only`**
```bash
PERSYS_DOCKER_ENABLED=true
PERSYS_COMPOSE_ENABLED=true
PERSYS_VM_ENABLED=false
PERSYS_STATE_PATH=/var/lib/persys/state.db
PERSYS_LOG_LEVEL=info
```

### 4. VM-Only Configuration

**File: `.env.vms-only`**
```bash
PERSYS_DOCKER_ENABLED=false
PERSYS_COMPOSE_ENABLED=false
PERSYS_VM_ENABLED=true
PERSYS_LIBVIRT_URI=qemu:///system
PERSYS_STATE_PATH=/var/lib/persys/state.db
PERSYS_LOG_LEVEL=info
```

## Deployment Scenarios

### Scenario 1: Bare Metal with Systemd

**Prerequisites:**
- Ubuntu 22.04 or similar
- Docker installed
- Libvirt installed (optional)

**Installation:**

```bash
# 1. Install agent binary
sudo curl -L https://github.com/persys/compute-agent/releases/latest/download/persys-agent-linux-amd64 \
  -o /usr/local/bin/persys-agent
sudo chmod +x /usr/local/bin/persys-agent

# 2. Create persys user
sudo useradd -r -s /bin/false persys
sudo usermod -aG docker persys
sudo usermod -aG libvirt persys

# 3. Create directories
sudo mkdir -p /var/lib/persys /etc/persys/certs
sudo chown persys:persys /var/lib/persys

# 4. Deploy certificates
sudo cp agent.crt /etc/persys/certs/
sudo cp agent.key /etc/persys/certs/
sudo cp ca.crt /etc/persys/certs/
sudo chmod 600 /etc/persys/certs/agent.key

# 5. Create systemd service
sudo tee /etc/systemd/system/persys-agent.service << 'EOF'
[Unit]
Description=Persys Compute Agent
After=network.target docker.service libvirtd.service
Requires=docker.service

[Service]
Type=simple
User=persys
Group=persys
Environment="PERSYS_NODE_ID=%H"
Environment="PERSYS_STATE_PATH=/var/lib/persys/state.db"
Environment="PERSYS_TLS_CERT=/etc/persys/certs/agent.crt"
Environment="PERSYS_TLS_KEY=/etc/persys/certs/agent.key"
Environment="PERSYS_TLS_CA=/etc/persys/certs/ca.crt"
Environment="PERSYS_LOG_LEVEL=info"
ExecStart=/usr/local/bin/persys-agent
Restart=always
RestartSec=10
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

# 6. Enable and start
sudo systemctl daemon-reload
sudo systemctl enable persys-agent
sudo systemctl start persys-agent

# 7. Verify
sudo systemctl status persys-agent
journalctl -u persys-agent -f
```

### Scenario 2: Docker Container

**docker-compose.yml:**

```yaml
version: '3.8'

services:
  persys-agent:
    image: persys/compute-agent:latest
    container_name: persys-agent
    privileged: true
    network_mode: host
    restart: unless-stopped
    
    volumes:
      # Docker socket for container management
      - /var/run/docker.sock:/var/run/docker.sock
      
      # Libvirt socket for VM management
      - /var/run/libvirt:/var/run/libvirt
      
      # State persistence
      - ./data:/var/lib/persys
      
      # TLS certificates
      - ./certs:/etc/persys/certs:ro
    
    environment:
      - PERSYS_NODE_ID=${HOSTNAME}
      - PERSYS_STATE_PATH=/var/lib/persys/state.db
      - PERSYS_TLS_CERT=/etc/persys/certs/agent.crt
      - PERSYS_TLS_KEY=/etc/persys/certs/agent.key
      - PERSYS_TLS_CA=/etc/persys/certs/ca.crt
      - PERSYS_LOG_LEVEL=info
      - PERSYS_DOCKER_ENABLED=true
      - PERSYS_COMPOSE_ENABLED=true
      - PERSYS_VM_ENABLED=true
```

**Deployment:**

```bash
# 1. Create directories
mkdir -p data certs

# 2. Copy certificates
cp /path/to/agent.crt certs/
cp /path/to/agent.key certs/
cp /path/to/ca.crt certs/

# 3. Start
docker-compose up -d

# 4. View logs
docker-compose logs -f persys-agent

# 5. Check health
docker exec persys-agent /usr/local/bin/persys-agent --help
```

### Scenario 3: Kubernetes DaemonSet

**namespace.yaml:**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: persys-system
```

**certificates-secret.yaml:**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: persys-agent-certs
  namespace: persys-system
type: Opaque
data:
  agent.crt: <base64-encoded-cert>
  agent.key: <base64-encoded-key>
  ca.crt: <base64-encoded-ca>
```

**daemonset.yaml:**

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: persys-agent
  namespace: persys-system
  labels:
    app: persys-agent
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
      
      serviceAccountName: persys-agent
      
      containers:
      - name: agent
        image: persys/compute-agent:latest
        imagePullPolicy: Always
        
        securityContext:
          privileged: true
        
        env:
        - name: PERSYS_NODE_ID
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: PERSYS_STATE_PATH
          value: /var/lib/persys/state.db
        - name: PERSYS_TLS_CERT
          value: /etc/persys/certs/agent.crt
        - name: PERSYS_TLS_KEY
          value: /etc/persys/certs/agent.key
        - name: PERSYS_TLS_CA
          value: /etc/persys/certs/ca.crt
        - name: PERSYS_LOG_LEVEL
          value: info
        
        ports:
        - name: grpc
          containerPort: 50051
          protocol: TCP
        
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
        
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        
        livenessProbe:
          exec:
            command:
            - /bin/sh
            - -c
            - pgrep persys-agent
          initialDelaySeconds: 30
          periodSeconds: 10
        
        readinessProbe:
          tcpSocket:
            port: 50051
          initialDelaySeconds: 10
          periodSeconds: 5
      
      volumes:
      - name: docker-socket
        hostPath:
          path: /var/run/docker.sock
          type: Socket
      - name: libvirt-socket
        hostPath:
          path: /var/run/libvirt
          type: Directory
      - name: state
        hostPath:
          path: /var/lib/persys
          type: DirectoryOrCreate
      - name: certs
        secret:
          secretName: persys-agent-certs
          defaultMode: 0400
      
      tolerations:
      - effect: NoSchedule
        operator: Exists
      - effect: NoExecute
        operator: Exists
```

**service-account.yaml:**

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: persys-agent
  namespace: persys-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: persys-agent
rules:
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: persys-agent
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: persys-agent
subjects:
- kind: ServiceAccount
  name: persys-agent
  namespace: persys-system
```

**Deployment:**

```bash
# 1. Create namespace
kubectl apply -f namespace.yaml

# 2. Create certificates secret
# First, base64 encode your certificates
cat agent.crt | base64 -w 0
cat agent.key | base64 -w 0
cat ca.crt | base64 -w 0

# Edit certificates-secret.yaml with the base64 values
kubectl apply -f certificates-secret.yaml

# 3. Create service account and RBAC
kubectl apply -f service-account.yaml

# 4. Deploy DaemonSet
kubectl apply -f daemonset.yaml

# 5. Verify deployment
kubectl -n persys-system get daemonset
kubectl -n persys-system get pods
kubectl -n persys-system logs -l app=persys-agent -f
```

### Scenario 4: High Availability Setup

**Load-balanced multiple nodes:**

```
┌─────────────┐
│  Scheduler  │
│   (HA)      │
└──────┬──────┘
       │
   Load Balancer
       │
       ├─────────────┬─────────────┬─────────────┐
       ↓             ↓             ↓             ↓
   ┌────────┐   ┌────────┐   ┌────────┐   ┌────────┐
   │ Agent  │   │ Agent  │   │ Agent  │   │ Agent  │
   │ Node 1 │   │ Node 2 │   │ Node 3 │   │ Node N │
   └────────┘   └────────┘   └────────┘   └────────┘
```

**Configuration:**

Each node runs independently with:
- Unique NODE_ID
- Same CA certificate
- Individual node certificates
- Local state store
- Independent reconciliation

**Scheduler configuration:**
- Maintains registry of all agents
- Uses node IDs for workload placement
- Implements retry logic for failures
- Monitors agent health via HealthCheck

## Certificate Management

### Generate Certificates with CFSSL

**ca-config.json:**

```json
{
  "signing": {
    "default": {
      "expiry": "8760h"
    },
    "profiles": {
      "persys": {
        "usages": [
          "signing",
          "key encipherment",
          "server auth",
          "client auth"
        ],
        "expiry": "8760h"
      }
    }
  }
}
```

**ca-csr.json:**

```json
{
  "CN": "Persys CA",
  "key": {
    "algo": "rsa",
    "size": 4096
  },
  "names": [
    {
      "C": "US",
      "ST": "California",
      "L": "San Francisco",
      "O": "Persys Cloud",
      "OU": "Platform"
    }
  ]
}
```

**agent-csr.json:**

```json
{
  "CN": "persys-agent",
  "hosts": [
    "localhost",
    "127.0.0.1",
    "*.persys.internal",
    "node01.persys.internal"
  ],
  "key": {
    "algo": "rsa",
    "size": 2048
  },
  "names": [
    {
      "C": "US",
      "ST": "California",
      "L": "San Francisco",
      "O": "Persys Cloud",
      "OU": "Agent"
    }
  ]
}
```

**Generate:**

```bash
# Generate CA
cfssl gencert -initca ca-csr.json | cfssljson -bare ca

# Generate agent certificate
cfssl gencert \
  -ca=ca.pem \
  -ca-key=ca-key.pem \
  -config=ca-config.json \
  -profile=persys \
  agent-csr.json | cfssljson -bare agent

# Result:
# ca.pem (CA certificate)
# agent.pem (agent certificate)
# agent-key.pem (agent private key)
```

## Monitoring Examples

### Prometheus Configuration

**prometheus.yml:**

```yaml
scrape_configs:
  - job_name: 'persys-agents'
    static_configs:
      - targets:
          - 'node01:50051'
          - 'node02:50051'
          - 'node03:50051'
    metrics_path: '/metrics'
    scheme: 'https'
    tls_config:
      ca_file: '/etc/prometheus/ca.crt'
      cert_file: '/etc/prometheus/client.crt'
      key_file: '/etc/prometheus/client.key'
```

### Grafana Dashboard

**Example queries:**

```promql
# Workload count by state
sum by (state) (persys_workloads_total)

# gRPC request rate
rate(persys_grpc_requests_total[5m])

# Reconciliation duration
histogram_quantile(0.95, persys_reconcile_duration_seconds)

# Error rate
rate(persys_errors_total[5m])
```

## Troubleshooting

### Debug Mode

```bash
# Enable debug logging
PERSYS_LOG_LEVEL=debug ./persys-agent

# Or for systemd
sudo systemctl edit persys-agent

# Add:
[Service]
Environment="PERSYS_LOG_LEVEL=debug"

sudo systemctl daemon-reload
sudo systemctl restart persys-agent
```

### Common Issues

**1. TLS Certificate Errors**

```bash
# Verify certificates
openssl verify -CAfile ca.crt agent.crt

# Check certificate details
openssl x509 -in agent.crt -text -noout

# Test connection
openssl s_client -connect localhost:50051 \
  -cert agent.crt -key agent.key -CAfile ca.crt
```

**2. Permission Issues**

```bash
# Fix Docker socket permissions
sudo usermod -aG docker persys
sudo systemctl restart persys-agent

# Fix libvirt permissions
sudo usermod -aG libvirt persys
sudo systemctl restart persys-agent

# Fix state store permissions
sudo chown -R persys:persys /var/lib/persys
```

**3. Runtime Not Available**

```bash
# Check Docker
docker ps

# Check libvirt
virsh list --all

# Check agent logs
journalctl -u persys-agent -n 100
```
