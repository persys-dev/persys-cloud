// Package types defines the SDK resource model.
//
// Applications should work with these types rather than constructing
// controlv1 protobuf messages directly. The SDK client translates them
// to the appropriate API requests internally.
package types

// WorkloadType identifies the kind of compute resource to schedule.
type WorkloadType string

const (
	// WorkloadContainer schedules a single Docker container.
	WorkloadContainer WorkloadType = "container"
	// WorkloadCompose schedules a Docker Compose stack.
	WorkloadCompose WorkloadType = "compose"
	// WorkloadVM provisions a virtual machine.
	WorkloadVM WorkloadType = "vm"
)

// Workload describes a compute resource to be scheduled by Persys.
//
// Example — a simple container:
//
//	w := sdk.Workload{Name: "web", Image: "nginx:latest"}
//
// Example — a Docker Compose stack from Git:
//
//	w := sdk.Workload{
//	    Name: "app",
//	    Type: types.WorkloadCompose,
//	    Git:  &GitSource{URL: "https://github.com/myorg/app.git", Ref: "main"},
//	}
type Workload struct {
	// Name is the unique workload identifier within the tenant.
	Name string `json:"name"`

	// Type is the workload kind. Defaults to WorkloadContainer.
	Type WorkloadType `json:"type,omitempty"`

	// Image is the container image reference. Required for WorkloadContainer.
	Image string `json:"image,omitempty"`

	// Env holds environment variables injected into the workload.
	Env map[string]string `json:"env,omitempty"`

	// Labels are arbitrary key/value pairs used for placement and filtering.
	Labels map[string]string `json:"labels,omitempty"`

	// Resources specifies compute resource limits.
	Resources ResourceRequirements `json:"resources,omitempty"`

	// Ports maps container ports to host ports.
	Ports []PortMapping `json:"ports,omitempty"`

	// Volumes defines volume mounts.
	Volumes []VolumeMount `json:"volumes,omitempty"`

	// RestartPolicy controls restart behaviour. e.g. "always", "on-failure".
	RestartPolicy string `json:"restartPolicy,omitempty"`

	// Privileged runs the container with elevated privileges (use with care).
	Privileged bool `json:"privileged,omitempty"`

	// Git specifies a Git source for Compose or manifest deployments.
	Git *GitSource `json:"git,omitempty"`

	// ComposeSpec is an inline Docker Compose YAML string.
	// Used when Type == WorkloadCompose and Git is nil.
	ComposeSpec string `json:"composeSpec,omitempty"`

	// VM holds virtual machine configuration. Required for WorkloadVM.
	VM *VMSpec `json:"vm,omitempty"`

	// NodeSelector constrains scheduling to nodes matching these labels.
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// ResourceRequirements expresses CPU, memory, and disk limits.
type ResourceRequirements struct {
	// CPU is the number of CPU cores (fractional values are supported).
	CPU float64 `json:"cpu,omitempty"`

	// MemoryMB is the memory limit in mebibytes.
	MemoryMB int64 `json:"memoryMb,omitempty"`

	// DiskMB is the disk allocation in mebibytes.
	DiskMB int64 `json:"diskMb,omitempty"`
}

// PortMapping maps a container port to a host port.
type PortMapping struct {
	// Host is the host port number.
	Host int32 `json:"host"`
	// Container is the container port number.
	Container int32 `json:"container"`
	// Protocol is "tcp" or "udp". Default: "tcp".
	Protocol string `json:"protocol,omitempty"`
}

// VolumeMount describes a volume attached to a workload.
type VolumeMount struct {
	// Name is the volume name or host path.
	Name string `json:"name"`
	// MountPath is the path inside the container.
	MountPath string `json:"mountPath"`
	// ReadOnly mounts the volume read-only.
	ReadOnly bool `json:"readOnly,omitempty"`
}

// GitSource references a Git repository for source-driven deployments.
type GitSource struct {
	// URL is the repository clone URL.
	URL string `json:"url"`
	// Ref is the branch, tag, or commit SHA to check out. Default: "main".
	Ref string `json:"ref,omitempty"`
	// Path is a subdirectory within the repo. Default: repo root.
	Path string `json:"path,omitempty"`
}

// VMSpec configures a virtual machine workload.
type VMSpec struct {
	// VCPUs is the number of virtual CPUs.
	VCPUs int32 `json:"vcpus"`
	// MemoryMB is the RAM allocation in mebibytes.
	MemoryMB int64 `json:"memoryMb"`
	// DiskGB is the root disk size in gibibytes.
	DiskGB int32 `json:"diskGb"`
	// CloudInit is a cloud-init user-data string injected at boot.
	CloudInit string `json:"cloudInit,omitempty"`
	// Network holds network configuration for the VM.
	Network *VMNetwork `json:"network,omitempty"`
}

// VMNetwork configures VM networking.
type VMNetwork struct {
	// Bridge is the host bridge interface name.
	Bridge string `json:"bridge,omitempty"`
	// MACAddress is the VM MAC address. Randomly assigned if empty.
	MACAddress string `json:"macAddress,omitempty"`
}

// Node represents a compute node registered with the Persys scheduler.
type Node struct {
	// ID is the unique node identifier.
	ID string `json:"id"`
	// Address is the node's network address.
	Address string `json:"address"`
	// Status is the node liveness status (e.g. "healthy", "draining").
	Status string `json:"status"`
	// Labels are the node's label set used for placement decisions.
	Labels map[string]string `json:"labels,omitempty"`
	// Resources describes the node's total and available capacity.
	Resources NodeResources `json:"resources"`
}

// NodeResources describes a node's capacity.
type NodeResources struct {
	// TotalCPU is the total number of CPU cores.
	TotalCPU float64 `json:"totalCpu"`
	// AvailableCPU is the currently unallocated CPU.
	AvailableCPU float64 `json:"availableCpu"`
	// TotalMemoryMB is the total memory in mebibytes.
	TotalMemoryMB int64 `json:"totalMemoryMb"`
	// AvailableMemoryMB is the currently unallocated memory.
	AvailableMemoryMB int64 `json:"availableMemoryMb"`
}

// WorkloadStatus describes the runtime state of a scheduled workload.
type WorkloadStatus struct {
	// WorkloadID is the unique identifier assigned by the scheduler.
	WorkloadID string `json:"workloadId"`
	// Name is the workload's user-supplied name.
	Name string `json:"name"`
	// Status is the lifecycle status (e.g. "running", "failed", "pending").
	Status string `json:"status"`
	// NodeID is the node the workload is assigned to.
	NodeID string `json:"nodeId,omitempty"`
	// Message is a human-readable status description.
	Message string `json:"message,omitempty"`
}

// ClusterSummary is a high-level view of cluster health.
type ClusterSummary struct {
	// TotalNodes is the count of registered nodes.
	TotalNodes int32 `json:"totalNodes"`
	// HealthyNodes is the count of nodes in a healthy liveness state.
	HealthyNodes int32 `json:"healthyNodes"`
	// TotalWorkloads is the count of scheduled workloads.
	TotalWorkloads int32 `json:"totalWorkloads"`
	// RunningWorkloads is the count of workloads in a running state.
	RunningWorkloads int32 `json:"runningWorkloads"`
}
