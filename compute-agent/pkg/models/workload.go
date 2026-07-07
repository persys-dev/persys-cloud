package models

import "time"

// WorkloadType represents the type of workload
type WorkloadType string

const (
	WorkloadTypeContainer WorkloadType = "container"
	WorkloadTypeCompose   WorkloadType = "compose"
	WorkloadTypeVM        WorkloadType = "vm"
)

// DesiredState represents the desired state of a workload
type DesiredState string

const (
	DesiredStateRunning DesiredState = "running"
	DesiredStateStopped DesiredState = "stopped"
)

// ActualState represents the actual runtime state
type ActualState string

const (
	ActualStatePending ActualState = "pending"
	ActualStateRunning ActualState = "running"
	ActualStateStopped ActualState = "stopped"
	ActualStateFailed  ActualState = "failed"
	ActualStateUnknown ActualState = "unknown"
)

// Workload represents a complete workload definition
type Workload struct {
	ID           string                 `json:"id"`
	Type         WorkloadType           `json:"type"`
	RevisionID   string                 `json:"revision_id"`
	DesiredState DesiredState           `json:"desired_state"`
	Spec         map[string]interface{} `json:"spec"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

// WorkloadStatus represents the runtime status of a workload
type WorkloadStatus struct {
	ID           string            `json:"id"`
	Type         WorkloadType      `json:"type"`
	RevisionID   string            `json:"revision_id"`
	DesiredState DesiredState      `json:"desired_state"`
	ActualState  ActualState       `json:"actual_state"`
	Message      string            `json:"message"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Usage        *WorkloadUsage    `json:"usage,omitempty"`
}

// ContainerSpec defines Docker container configuration
type ContainerSpec struct {
	Image          string              `json:"image"`
	Command        []string            `json:"command,omitempty"`
	Args           []string            `json:"args,omitempty"`
	Env            map[string]string   `json:"env,omitempty"`
	Volumes        []VolumeMount       `json:"volumes,omitempty"`
	ManagedVolumes []ManagedVolumeSpec `json:"managed_volumes,omitempty"`
	Ports          []PortMapping       `json:"ports,omitempty"`
	Resources      *ResourceLimits     `json:"resources,omitempty"`
	RestartPolicy  *RestartPolicy      `json:"restart_policy,omitempty"`
	Labels         map[string]string   `json:"labels,omitempty"`
}

// ComposeSpec defines Docker Compose configuration
type ComposeSpec struct {
	ProjectName string            `json:"project_name"`
	ComposeYAML string            `json:"compose_yaml"` // base64 encoded
	Env         map[string]string `json:"env,omitempty"`
}

// VMSpec defines virtual machine configuration
type VMSpec struct {
	Name            string              `json:"name"`
	VCPUs           int                 `json:"vcpus"`
	MemoryMB        int64               `json:"memory_mb"`
	Disks           []DiskConfig        `json:"disks"`
	Networks        []NetworkConfig     `json:"networks"`
	CloudInit       string              `json:"cloud_init,omitempty"` // user-data script
	Metadata        map[string]string   `json:"metadata,omitempty"`
	CloudInitConfig *CloudInitConfig    `json:"cloud_init_config,omitempty"`
	ManagedVolumes  []ManagedVolumeSpec `json:"managed_volumes,omitempty"`
}

// CloudInitConfig defines advanced cloud-init settings
type CloudInitConfig struct {
	UserData      string `json:"user_data,omitempty"`
	MetaData      string `json:"meta_data,omitempty"`
	NetworkConfig string `json:"network_config,omitempty"`
	VendorData    string `json:"vendor_data,omitempty"`
}

// ManagedVolumeSpec describes a provider-backed volume request.
type ManagedVolumeSpec struct {
	Name         string `json:"name"`
	Driver       string `json:"driver"`
	SizeGB       int64  `json:"size_gb,omitempty"`
	AccessMode   string `json:"access_mode,omitempty"`
	FSType       string `json:"fs_type,omitempty"`
	MountPath    string `json:"mount_path,omitempty"`
	ReadOnly     bool   `json:"read_only,omitempty"`
	RetainPolicy string `json:"retain_policy,omitempty"`
}

// WorkloadUsage stores the latest per-workload utilization sample.
type WorkloadUsage struct {
	WorkloadID     string    `json:"workload_id,omitempty"`
	Type           string    `json:"type,omitempty"`
	CPUPercent     float64   `json:"cpu_percent,omitempty"`
	MemoryBytes    int64     `json:"memory_bytes,omitempty"`
	DiskReadBytes  int64     `json:"disk_read_bytes,omitempty"`
	DiskWriteBytes int64     `json:"disk_write_bytes,omitempty"`
	NetRXBytes     int64     `json:"net_rx_bytes,omitempty"`
	NetTXBytes     int64     `json:"net_tx_bytes,omitempty"`
	CollectedAt    time.Time `json:"collected_at,omitempty"`
	Source         string    `json:"source,omitempty"`
}

// VolumeMount represents a volume mount
type VolumeMount struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
	ReadOnly      bool   `json:"read_only"`
}

// PortMapping represents a port mapping
type PortMapping struct {
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"` // tcp or udp
}

// ResourceLimits defines resource constraints
type ResourceLimits struct {
	CPUShares       int64 `json:"cpu_shares,omitempty"`
	MemoryBytes     int64 `json:"memory_bytes,omitempty"`
	MemorySwapBytes int64 `json:"memory_swap_bytes,omitempty"`
}

// RestartPolicy defines container restart behavior
type RestartPolicy struct {
	Policy        string `json:"policy"` // no, always, on-failure, unless-stopped
	MaxRetryCount int    `json:"max_retry_count,omitempty"`
}

// DiskConfig defines VM disk configuration
type DiskConfig struct {
	Path   string `json:"path"`
	Device string `json:"device"` // vda, vdb, etc.
	Format string `json:"format"` // qcow2, raw, iso
	SizeGB int64  `json:"size_gb"`
	Type   string `json:"type"` // disk or cdrom
	Boot   bool   `json:"boot"` // true if boot disk/ISO
}

// DiskType constants
const (
	DiskTypeDisk  = "disk"
	DiskTypeCDROM = "cdrom"
)

// NetworkConfig defines VM network configuration
type NetworkConfig struct {
	Network    string `json:"network"`
	MACAddress string `json:"mac_address,omitempty"`
	IPAddress  string `json:"ip_address,omitempty"`
}
