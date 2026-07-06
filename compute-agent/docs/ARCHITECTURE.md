# Architecture Deep Dive

## Overview

The Persys Compute Agent follows a control-plane driven architecture where it operates as an execution engine for workload management decisions made by the Persys Scheduler. This document details the internal architecture and design decisions.

## Core Design Principles

### 1. Control-Plane Driven
- Agent does NOT make scheduling decisions
- All workload placement controlled by scheduler
- Agent focuses solely on execution and state management

### 2. Idempotency via Revision Tracking
- Every workload has a revision ID
- Reapplying same revision is a no-op
- Revision changes trigger workload recreation
- Enables reliable retries and crash recovery

### 3. Security First
- mTLS for all gRPC communication
- Certificate-based authentication
- No direct etcd/datastore access
- Secrets injected from control plane

### 4. Persistent State
- bbolt for local state storage
- Survives agent restarts
- Enables reconciliation after crashes
- Single source of truth for local state

### 5. Runtime Abstraction
- Pluggable runtime interface
- Support for multiple backends
- Clean separation of concerns
- Easy to add new runtime types

## Component Architecture

### 1. gRPC Server

**Responsibilities:**
- Expose agent API over gRPC
- Handle mTLS authentication
- Convert protobuf to internal models
- Route requests to workload manager

**Key Features:**
- Mutual TLS authentication
- Connection pooling
- Graceful shutdown
- Request logging

**Implementation:**
```go
type Server struct {
    config     *config.Config
    manager    *workload.Manager
    runtimeMgr *runtime.Manager
    grpcServer *grpc.Server
}
```

### 2. Workload Manager

**Responsibilities:**
- Coordinate workload lifecycle
- Enforce idempotency rules
- Manage desired vs actual state
- Orchestrate runtime operations

**Key Features:**
- Revision-based idempotency
- Automatic state transitions
- Error handling and recovery
- Status reporting

**State Machine:**
```
[Apply Request] → Check Revision → Exists & Same? → Skip
                                 ↓
                            Different/New
                                 ↓
                          Create/Recreate
                                 ↓
                          Desired=Running? → Start
                                 ↓
                            Save Status
```

### 3. Runtime Manager

**Responsibilities:**
- Abstract multiple runtime backends
- Route operations to correct runtime
- Health monitoring
- Runtime registration

**Runtime Interface:**
```go
type Runtime interface {
    Create(ctx, workload) error
    Start(ctx, id) error
    Stop(ctx, id) error
    Delete(ctx, id) error
    Status(ctx, id) (state, message, error)
    List(ctx) ([]string, error)
    Type() WorkloadType
    Healthy(ctx) error
}
```

### 4. Runtime Implementations

#### Docker Runtime
- Manages container lifecycle via Docker API
- Handles image pulling
- Configures networking and volumes
- Maps container states to Persys states

#### Compose Runtime
- Manages multi-container applications
- Writes docker-compose.yml files
- Executes docker-compose commands
- Tracks project-level state

#### VM Runtime
- Manages KVM virtual machines via libvirt
- Generates domain XML
- Handles VM lifecycle (create/start/stop/delete)
- Maps libvirt states to Persys states

### 5. State Store

**Responsibilities:**
- Persist workload definitions
- Track workload status
- Survive agent restarts
- Enable crash recovery

**Storage Model:**
```
workloads bucket:
  key: workload_id
  value: {id, type, revision_id, desired_state, spec, timestamps}

status bucket:
  key: workload_id
  value: {id, type, revision_id, desired_state, actual_state, message, timestamps}
```

### 6. Reconciliation Loop

**Responsibilities:**
- Periodic state synchronization
- Crash recovery
- Drift detection
- Self-healing

**Reconciliation Algorithm:**
```
for each workload:
    actual_state = runtime.Status(workload.id)
    
    if desired_state == RUNNING && actual_state != RUNNING:
        runtime.Start(workload.id)
    
    if desired_state == STOPPED && actual_state == RUNNING:
        runtime.Stop(workload.id)
    
    save updated status
```

## Data Flow

### Workload Creation Flow

```
Scheduler → gRPC ApplyWorkload → Workload Manager
                                       ↓
                                Check Revision
                                       ↓
                        ┌──────────────┴──────────────┐
                        ↓                             ↓
                  Same Revision                 New/Different
                        ↓                             ↓
                  Return (skip)               Get Runtime
                                                     ↓
                                              Delete Old (if exists)
                                                     ↓
                                              Create Workload
                                                     ↓
                                              Start (if desired)
                                                     ↓
                                              Get Status
                                                     ↓
                                              Save to Store
                                                     ↓
                                              Return Status
```

### Status Check Flow

```
Scheduler → gRPC GetWorkloadStatus → Workload Manager
                                          ↓
                                    Load from Store
                                          ↓
                                    Get Fresh Status from Runtime
                                          ↓
                                    Update Store
                                          ↓
                                    Return Status
```

### Reconciliation Flow

```
Timer Tick → Reconciliation Loop
                   ↓
         List All Workloads from Store
                   ↓
         for each workload:
                   ↓
         Get Current Status from Runtime
                   ↓
         Compare with Desired State
                   ↓
         Take Action if Mismatch
                   ↓
         Update Status in Store
```

## Security Model

### mTLS Authentication

```
Client                          Agent
  |                               |
  |--- ClientHello (cert) -----→ |
  |                               | Verify client cert
  |                               | with CA
  |← ServerHello (cert) ---------|
  | Verify server cert            |
  | with CA                       |
  |                               |
  |=== Encrypted Channel ========|
```

### Certificate Requirements

**Server Certificate:**
- Common Name: agent hostname or node ID
- SAN: DNS names and IPs
- Extended Key Usage: Server Authentication

**Client Certificate (Scheduler):**
- Common Name: scheduler identity
- Extended Key Usage: Client Authentication

**CA Certificate:**
- Used to verify both client and server certs
- Must be trusted by both parties

### Secret Injection

Secrets flow from scheduler to agent:
1. Scheduler retrieves secrets from Vault/etc
2. Scheduler includes secrets in workload spec
3. Agent injects as environment variables
4. Agent does NOT persist secrets to state store

## Performance Considerations

### Scalability

**Per-Node Limits:**
- 100s of containers per node
- 10s of VMs per node
- 1000s of status checks per minute

**Optimization Techniques:**
- Connection pooling (Docker/libvirt)
- Batch operations where possible
- Async status updates
- Efficient state store queries

### Resource Management

**CPU:**
- Reconciliation loop: 1-5% baseline
- gRPC handlers: burst to 10-20%
- Runtime operations: variable

**Memory:**
- State store: ~10MB + (workloads * 1KB)
- gRPC connections: ~1MB per connection
- Runtime clients: ~5MB per runtime

**Disk:**
- State store: grows with workload count
- Compose project files: ~100KB per project
- Logs: configure rotation

## Failure Scenarios

### Agent Crash

**Recovery:**
1. Agent restarts
2. Loads state from bbolt
3. Reconciliation loop starts
4. Compares desired vs actual state
5. Corrects any drift

**Example:**
```
Before crash: 10 workloads running
After crash:  7 still running, 3 stopped
Reconciliation: Restart the 3 stopped workloads
```

### Runtime Failure

**Docker Daemon Crash:**
- Agent continues running
- Docker health checks fail
- Containers not accessible
- Reconciliation retries once Docker recovers

**Libvirt Daemon Crash:**
- Similar to Docker
- VMs may continue running
- Management API unavailable
- Reconciliation resumes on recovery

### Network Partition

**Scheduler Unreachable:**
- Agent continues managing local workloads
- Cannot receive new workload specs
- Reconciliation maintains existing state
- Resumes normal operation on reconnection

## Extension Points

### Adding New Runtime

1. Implement `Runtime` interface
2. Register with `RuntimeManager`
3. Add workload type to proto
4. Update configuration

Example:
```go
type FirecrackerRuntime struct {
    // implementation
}

func (f *FirecrackerRuntime) Create(ctx context.Context, workload *models.Workload) error {
    // create Firecracker microVM
}

// ... implement other interface methods
```

### Custom State Store

Replace bbolt with alternative:
```go
type CustomStore struct {
    // implementation
}

func (s *CustomStore) SaveWorkload(workload *models.Workload) error {
    // save to custom backend
}

// ... implement Store interface
```

### Metrics Integration

Add Prometheus metrics:
```go
var (
    workloadCount = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "persys_workloads_total",
            Help: "Total number of workloads by state",
        },
        []string{"state"},
    )
)

// Update in reconciliation loop
workloadCount.WithLabelValues("running").Set(float64(runningCount))
```

## Future Enhancements

### Planned Features

1. **Storage Pools**
   - Managed disk provisioning
   - Snapshot support
   - Migration capabilities

2. **Advanced Networking**
   - Custom bridge networks
   - SR-IOV support
   - eBPF integration

3. **GPU Support**
   - GPU passthrough
   - vGPU allocation
   - Resource quotas

4. **Multi-Runtime**
   - containerd support
   - Firecracker support
   - Kata containers

5. **Enhanced Security**
   - Vault integration
   - Secret encryption at rest
   - Audit logging

6. **Observability**
   - Prometheus metrics
   - Distributed tracing
   - Performance profiling

## Testing Strategy

### Unit Tests
- Mock runtime interfaces
- Test idempotency logic
- Validate state transitions

### Integration Tests
- Real Docker/libvirt
- End-to-end workflows
- Crash recovery scenarios

### Performance Tests
- Load testing (100s of workloads)
- Latency measurements
- Resource profiling

## Conclusion

The Persys Compute Agent provides a robust, production-ready platform for workload execution with strong consistency guarantees, security, and reliability. Its modular architecture enables easy extension while maintaining simplicity and performance.
