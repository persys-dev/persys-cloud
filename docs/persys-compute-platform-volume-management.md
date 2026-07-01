Based on the codebase, here's the allocation responsibility breakdown:

## **Volume Allocation: Compute-Agent + Storage Providers**

The **compute-agent's workload manager** is the primary orchestrator:

```
Provider Flow:
Workload Spec (ManagedVolumes) 
    ↓
compute-agent/internal/workload/manager.go
    ├── prepareManagedStorageForContainer()  [line 411]
    ├── prepareManagedStorageForVM()          [line 473]
    └── prepareManagedStorage()               [dispatcher]
         ↓
compute-agent/internal/platform/storage.go
    └── VolumeManager interface
         ├── Provision() → Creates volume in backend
         ├── Attach()    → Stages volume for workload
         └── Detach()    → Cleanup on deletion
         
compute-agent/internal/storage/providers/
    ├── local_provider.go       → Host bind paths
    ├── nfs_provider.go         → NFS mount points
    └── ceph_rbd_provider.go    → RBD block devices
```

**Key files handling allocation**:
- manager.go - Main orchestrator
- storage.go - Provider interface
- providers - Actual drivers

---

## **Network Allocation: Runtime-Specific**

Networks are configured **per-runtime**, not through a centralized provider (yet):

### **Containers (Docker)**
- docker.go - Docker API configures networking
- Handled in `Create()` via container config
- Networks at runtime level, not pre-provisioned

### **VMs (libvirt)**
- vm.go - `generateDomainXML()`
- Converts `NetworkConfig` specs to libvirt domain XML
- Networks defined in VM spec (bridge, DHCP, static IP)
- Applied via libvirt at VM creation time

### **Docker Compose**
- Managed by docker-compose - networks defined in compose file

---

## **Scheduler's Role (Control Plane)**

The **scheduler does capability matching, not allocation**:

```
persys-scheduler/internal/scheduler/state_store.go
    └── syncWorkloadManagedStorage()
         ├── Tracks volume lifecycle phases:
         │   (Provisioning → Provisioned → Attached → Released/Retained)
         ├── Validates node has storage driver capability
         └── Refuses placement if driver unavailable
```

- Scheduler advertises node capabilities (NFS, Ceph, local support)
- Only places workloads on nodes with matching storage drivers
- **Does NOT provision volumes** - that's compute-agent's job

---

## **Allocation Sequence**

```
1. User submits workload spec with:
   - managed_volumes: [{driver: "nfs", name: "data", ...}]
   - networks: [{bridge: "br0", dhcp: true}]

2. Scheduler receives request
   ├── Validates storage driver support on nodes
   └── Places workload on compatible node

3. Compute-Agent receives workload
   ├── Volume allocation:
   │   ├── prepareManagedStorage()
   │   ├── m.volumeMgr.Provision()    ← Creates in NFS/Ceph/etc
   │   ├── m.volumeMgr.Attach()       ← Stages volume
   │   └── Saves to state store (bbolt)
   │
   ├── Network allocation:
   │   ├── Docker: Container created with network config
   │   └── VM: Domain XML generated with network interfaces
   │
   └── Runtime Create() called with fully-provisioned resources
```

---

## **Key Insight**

**Volumes are explicitly provisioned by compute-agent**; **Networks are configured at runtime creation**.

- **Volumes**: Pre-allocated before workload starts (managed lifecycle)
- **Networks**: Configured as part of workload spec (no explicit provisioning step yet)

Networks have a scaffolded `NetworkProvider` interface that mirrors the storage pattern, but it's **not currently wired in** (low-priority per the design spec).