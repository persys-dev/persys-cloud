# Persys Cloud System Flow Visualization

## System Architecture Overview

```mermaid
graph TB
    subgraph "Client Layer"
        CLI[persys-cli]
        API[External API Client]
    end
    
    subgraph "API Gateway Layer"
        AG[api-gateway]
        AG_AUTH[Auth Service]
        AG_GITHUB[GitHub Integration]
    end
    
    subgraph "Scheduler Layer"
        PROW[prow-scheduler]
        ETCD[(etcd)]
        MONITOR[Workload Monitor]
        NODE_MONITOR[Node Monitor]
    end
    
    subgraph "Agent Layer"
        AGENT1[persys-agent Node 1]
        AGENT2[persys-agent Node 2]
        AGENTN[persys-agent Node N]
    end
    
    subgraph "Infrastructure"
        DOCKER1[Docker Engine 1]
        DOCKER2[Docker Engine 2]
        DOCKERN[Docker Engine N]
        COREDNS[CoreDNS]
        PROMETHEUS[Prometheus]
        GRAFANA[Grafana]
    end
    
    CLI --> AG
    API --> AG
    AG --> PROW
    PROW --> ETCD
    PROW --> AGENT1
    PROW --> AGENT2
    PROW --> AGENTN
    AGENT1 --> DOCKER1
    AGENT2 --> DOCKER2
    AGENTN --> DOCKERN
    MONITOR --> AGENT1
    MONITOR --> AGENT2
    MONITOR --> AGENTN
    NODE_MONITOR --> ETCD
    PROW --> COREDNS
    PROMETHEUS --> PROW
    PROMETHEUS --> AGENT1
    PROMETHEUS --> AGENT2
    PROMETHEUS --> AGENTN
    GRAFANA --> PROMETHEUS
```

## Complete Workload Lifecycle Flow

### 1. Workload Creation & Scheduling

```mermaid
sequenceDiagram
    participant Client as persys-cli
    participant Gateway as api-gateway
    participant Prow as prow-scheduler
    participant Etcd as etcd
    participant Agent as persys-agent
    participant Docker as Docker Engine
    participant Monitor as Workload Monitor

    Client->>Gateway: POST /workloads/schedule
    Note over Client: {<br/>  "name": "celery-worker",<br/>  "type": "docker-container",<br/>  "image": "python:3.9",<br/>  "command": "celery worker",<br/>  "envVars": {...}<br/>}
    
    Gateway->>Prow: Forward request (mTLS)
    
    Prow->>Prow: Generate workload.ID (UUID)
    Prow->>Prow: Select suitable node
    Prow->>Etcd: Store workload FIRST
    Note over Prow,Etcd: /workloads/{workload.ID}<br/>Status: "Scheduled"
    
    Prow->>Agent: POST /docker/run (async)
    Note over Prow,Agent: {<br/>  "workloadId": "abc123-def456",<br/>  "name": "abc123-def456",<br/>  "displayName": "celery-worker",<br/>  "image": "python:3.9",<br/>  "async": true<br/>}
    
    Agent-->>Prow: 200 OK (immediate)
    Note over Agent,Prow: "Command queued for execution"
    
    Prow-->>Gateway: 200 OK
    Note over Prow,Gateway: {"node_id": "node-1"}
    
    Gateway-->>Client: 200 OK
    Note over Gateway,Client: {"node_id": "node-1"}

    par Async Container Execution
        Agent->>Docker: docker run --name abc123-def456 --label displayName=celery-worker
        Docker-->>Agent: Container started
        Note over Agent: Container execution result logged
    end

    par Monitoring Cycle (every 60s)
        Monitor->>Agent: GET /docker/list
        Agent-->>Monitor: Container list
        Monitor->>Agent: GET /docker/logs/{containerId}
        Agent-->>Monitor: Container logs
        Monitor->>Etcd: Update workload status & logs
    end
```

### 2. Node Registration & Health Monitoring

```mermaid
sequenceDiagram
    participant Agent as persys-agent
    participant Prow as prow-scheduler
    participant Etcd as etcd
    participant CoreDNS as CoreDNS
    participant NodeMonitor as Node Monitor

    Agent->>Prow: POST /nodes/register (non-mTLS)
    Note over Agent,Prow: {<br/>  "nodeId": "node-1",<br/>  "ipAddress": "192.168.1.10",<br/>  "hostname": "worker-1",<br/>  "status": "active"<br/>}
    
    Prow->>Prow: Initiate mTLS handshake
    Prow->>Etcd: Store node info
    Note over Prow,Etcd: /nodes/node-1
    Prow->>CoreDNS: Register DNS entry
    Note over Prow,CoreDNS: node-1.cluster.local.dev
    
    Prow-->>Agent: 200 OK + mTLS certs
    
    loop Heartbeat (every 30s)
        Agent->>Prow: POST /nodes/heartbeat (non-mTLS)
        Note over Agent,Prow: {<br/>  "nodeId": "node-1",<br/>  "status": "active",<br/>  "availableCpu": 2.5,<br/>  "availableMemory": 8192<br/>}
        Prow->>Etcd: Update heartbeat timestamp
    end
    
    par Node Health Monitoring (every 60s)
        NodeMonitor->>Etcd: Get all nodes
        NodeMonitor->>NodeMonitor: Check heartbeat age
        alt Heartbeat > 10 minutes
            NodeMonitor->>Etcd: Update status to "Inactive"
        end
    end
```

### 3. Workload Status Monitoring & Log Capture

```mermaid
sequenceDiagram
    participant Monitor as Workload Monitor
    participant Etcd as etcd
    participant Agent as persys-agent
    participant Docker as Docker Engine

    loop Every 60 seconds
        Monitor->>Etcd: Get all workloads
        Etcd-->>Monitor: Workload list
        
        Monitor->>Etcd: Get all active nodes
        Etcd-->>Monitor: Node list
        
        loop For each active node
            Monitor->>Agent: GET /docker/list
            Agent->>Docker: docker ps -a --format
            Docker-->>Agent: Container list
            Agent-->>Monitor: JSON container list
            
            loop For each workload on node
                Monitor->>Monitor: Match container by workload.ID
                
                alt Container found
                    Monitor->>Monitor: Map Docker status to workload status
                    alt Status changed
                        Monitor->>Etcd: Update workload status
                        
                        alt Status is Running/Exited/Failed
                            Monitor->>Agent: GET /docker/logs/{containerId}
                            Agent->>Docker: docker logs {containerId}
                            Docker-->>Agent: Container logs
                            Agent-->>Monitor: Container logs
                            Monitor->>Etcd: Update workload logs
                        end
                    end
                else Container not found
                    Monitor->>Etcd: Set status to "Missing"
                end
            end
        end
    end
```

## Data Flow Architecture

### 1. Request Flow (mTLS vs non-mTLS)

```mermaid
graph LR
    subgraph "Client Requests"
        CLI[persys-cli]
    end
    
    subgraph "Prow Scheduler"
        MTLS[Port 8085<br/>mTLS Router]
        NON_MTLS[Port 8084<br/>non-mTLS Router]
    end
    
    subgraph "Route Types"
        MTLS_ROUTES["mTLS Routes:<br/>• /workloads/schedule<br/>• /nodes<br/>• /workloads<br/>• /cluster/metrics"]
        NON_MTLS_ROUTES["non-mTLS Routes:<br/>• /nodes/register<br/>• /nodes/heartbeat<br/>• /metrics"]
    end
    
    CLI --> MTLS
    CLI --> NON_MTLS
    MTLS --> MTLS_ROUTES
    NON_MTLS --> NON_MTLS_ROUTES
```

### 2. etcd Data Structure

```mermaid
graph TD
    subgraph "etcd Key-Value Store"
        NODES["/nodes/<br/>• node-1: {node data}<br/>• node-2: {node data}"]
        WORKLOADS["/workloads/<br/>• abc123-def456: {workload data}<br/>• xyz789-abc123: {workload data}"]
        DNS["/skydns/<br/>• Reverse DNS entries<br/>• Service discovery"]
    end
    
    subgraph "Workload Data Structure"
        WORKLOAD_DATA["{<br/>  id: 'abc123-def456',<br/>  name: 'celery-worker',<br/>  type: 'docker-container',<br/>  image: 'python:3.9',<br/>  nodeId: 'node-1',<br/>  status: 'Running',<br/>  logs: 'Container logs...',<br/>  createdAt: '2024-01-01T10:00:00Z'<br/>}"]
    end
    
    WORKLOADS --> WORKLOAD_DATA
```

### 3. Container Naming & Labeling Strategy

```mermaid
graph LR
    subgraph "Workload Request"
        REQ["Client Request:<br/>name: 'celery-worker'"]
    end
    
    subgraph "Prow Processing"
        UUID["Generated UUID:<br/>abc123-def456-7890"]
        PAYLOAD["Agent Payload:<br/>• name: abc123-def456<br/>• displayName: celery-worker<br/>• workloadId: abc123-def456"]
    end
    
    subgraph "Docker Container"
        CONTAINER["Container:<br/>• Name: abc123-def456<br/>• Labels:<br/>  - displayName=celery-worker<br/>  - workloadId=abc123-def456"]
    end
    
    REQ --> UUID
    UUID --> PAYLOAD
    PAYLOAD --> CONTAINER
```

## Monitoring & Observability

### 1. Metrics Collection

```mermaid
graph TB
    subgraph "Metrics Sources"
        PROW_METRICS["Prow Metrics:<br/>• HTTP requests<br/>• Workload operations<br/>• Node operations"]
        AGENT_METRICS["Agent Metrics:<br/>• Docker operations<br/>• Container stats<br/>• System resources"]
    end
    
    subgraph "Prometheus"
        PROM[Prometheus Server]
        SCRAPE[Scraping Jobs]
    end
    
    subgraph "Visualization"
        GRAFANA[Grafana Dashboards]
        ALERTS[Alert Manager]
    end
    
    PROW_METRICS --> PROM
    AGENT_METRICS --> PROM
    SCRAPE --> PROW_METRICS
    SCRAPE --> AGENT_METRICS
    PROM --> GRAFANA
    PROM --> ALERTS
```

### 2. Log Flow

```mermaid
graph LR
    subgraph "Log Sources"
        PROW_LOGS["Prow Logs:<br/>• Scheduling decisions<br/>• Node management<br/>• Error handling"]
        AGENT_LOGS["Agent Logs:<br/>• Docker commands<br/>• Container execution<br/>• System events"]
        CONTAINER_LOGS["Container Logs:<br/>• Application output<br/>• Error messages<br/>• Runtime logs"]
    end
    
    subgraph "Log Processing"
        COLLECTION[Log Collection]
        STORAGE[Log Storage]
        SEARCH[Log Search]
    end
    
    PROW_LOGS --> COLLECTION
    AGENT_LOGS --> COLLECTION
    CONTAINER_LOGS --> COLLECTION
    COLLECTION --> STORAGE
    STORAGE --> SEARCH
```

## Error Handling & Recovery

### 1. Failure Scenarios

```mermaid
graph TD
    subgraph "Failure Points"
        A[Agent Unreachable]
        B[Docker Command Fails]
        C[Image Pull Fails]
        D[Container Crashes]
        E[Node Goes Offline]
    end
    
    subgraph "Recovery Mechanisms"
        A1[Monitor detects missing container]
        B1[Logs captured in workload]
        C1[Status updated to 'Failed']
        D1[Container restart policy]
        E1[Node marked inactive]
    end
    
    A --> A1
    B --> B1
    C --> B1
    D --> D1
    E --> E1
    A1 --> C1
    B1 --> C1
```

### 2. Timeout Prevention

```mermaid
graph LR
    subgraph "Before (Synchronous)"
        SYNC_REQ[Client Request]
        SYNC_WAIT[Wait for Docker]
        SYNC_TIMEOUT[HTTP Timeout]
        SYNC_FAIL[Request Fails]
    end
    
    subgraph "After (Asynchronous)"
        ASYNC_REQ[Client Request]
        ASYNC_STORE[Store in etcd]
        ASYNC_RETURN[Return immediately]
        ASYNC_BG[Background execution]
        ASYNC_MONITOR[Monitor tracks progress]
    end
    
    SYNC_REQ --> SYNC_WAIT
    SYNC_WAIT --> SYNC_TIMEOUT
    SYNC_TIMEOUT --> SYNC_FAIL
    
    ASYNC_REQ --> ASYNC_STORE
    ASYNC_STORE --> ASYNC_RETURN
    ASYNC_RETURN --> ASYNC_BG
    ASYNC_BG --> ASYNC_MONITOR
```

## Security Model

### 1. Authentication & Authorization

```mermaid
graph TB
    subgraph "Security Layers"
        MTLS_AUTH["mTLS Authentication:<br/>• Certificate-based<br/>• Port 8085<br/>• Sensitive operations"]
        NON_MTLS_AUTH["Non-mTLS Authentication:<br/>• Basic auth<br/>• Port 8084<br/>• Node registration"]
        SECRET_AUTH["Shared Secret:<br/>• AGENT_SECRET env var<br/>• TOFU fallback"]
    end
    
    subgraph "Protected Resources"
        SENSITIVE["Sensitive Operations:<br/>• Workload scheduling<br/>• Node management<br/>• Cluster metrics"]
        PUBLIC["Public Operations:<br/>• Node registration<br/>• Heartbeat<br/>• Metrics endpoint"]
    end
    
    MTLS_AUTH --> SENSITIVE
    NON_MTLS_AUTH --> PUBLIC
    SECRET_AUTH --> NON_MTLS_AUTH
```

## Key Benefits of This Architecture

1. **Reliability**: Workloads stored in etcd before execution
2. **Scalability**: Asynchronous execution prevents timeouts
3. **Observability**: Comprehensive monitoring and logging
4. **Security**: Multi-layered authentication model
5. **Consistency**: Unique workload IDs throughout the system
6. **Recovery**: Automatic detection and status updates
7. **Performance**: Non-blocking operations with background monitoring

This architecture ensures that the persys-cloud system can handle complex container orchestration while maintaining reliability, security, and observability at scale. 