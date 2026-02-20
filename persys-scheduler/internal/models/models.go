package models

import (
	"net/http"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// Hypervisor represents virtualization technology details
type Hypervisor struct {
	Type    string `json:"type" binding:"required"`
	Status  string `json:"status" binding:"required"`
	Version string `json:"version,omitempty"`
}

// ContainerEngine represents container runtime details
type ContainerEngine struct {
	Type    string `json:"type" binding:"required"`
	Status  string `json:"status" binding:"required"`
	Version string `json:"version,omitempty"`
}

// Resources represents system resource usage
type Resources struct {
	CPUUsage    float64 `json:"cpu_usage,omitempty"`
	MemoryUsage float64 `json:"memory_usage,omitempty"`
	DiskUsage   int     `json:"disk_usage,omitempty"`
}

// DockerSwarm represents swarm-specific information
type DockerSwarm struct {
	Active         bool   `json:"active"`
	NodeID         string `json:"nodeId,omitempty"`
	Role           string `json:"role,omitempty"`
	Status         string `json:"status,omitempty"`
	ManagerAddress string `json:"managerAddress,omitempty"`
}

// Node represents a registered node
type Node struct {
	NodeID                 string            `json:"nodeId" binding:"required"`
	IPAddress              string            `json:"ipAddress" binding:"required"`
	Username               string            `json:"username" binding:"required"`
	Hostname               string            `json:"hostname" binding:"required"`
	OSName                 string            `json:"osName" binding:"required"`
	KernelVersion          string            `json:"kernelVersion" binding:"required"`
	Status                 string            `json:"status" binding:"required"`
	Timestamp              string            `json:"timestamp" binding:"required"`
	Resources              Resources         `json:"resources"`
	TotalCPU               float64           `json:"totalCpu"`
	TotalMemory            int64             `json:"totalMemory"`
	AvailableCPU           float64           `json:"availableCpu"`
	AvailableMemory        int64             `json:"availableMemory"`
	Hypervisor             Hypervisor        `json:"hypervisor" binding:"required"`
	ContainerEngine        ContainerEngine   `json:"containerEngine" binding:"required"`
	DockerSwarm            DockerSwarm       `json:"dockerSwarm"`
	LastHeartbeat          time.Time         `json:"lastHeartbeat"`
	StatusReason           string            `json:"statusReason,omitempty"`
	StatusUpdatedBy        string            `json:"statusUpdatedBy,omitempty"`
	StatusUpdatedAt        time.Time         `json:"statusUpdatedAt,omitempty"`
	Labels                 map[string]string `json:"labels,omitempty"`
	AgentPort              int               `json:"agentPort"` // Added for agent communication
	AgentGRPCPort          int               `json:"agentGrpcPort,omitempty"`
	AgentEndpoint          string            `json:"agentEndpoint,omitempty"`
	SupportedWorkloadTypes []string          `json:"supportedWorkloadTypes,omitempty"`
	DomainName             string            `json:"domainName,omitempty"` // Added field
}

// Workload represents a scheduled task
type Workload struct {
	ID            string                 `json:"id,omitempty"`
	Name          string                 `json:"name" binding:"required"`
	Type          string                 `json:"type" binding:"required"` // "docker-container", "docker-compose", "compose", "vm"
	RevisionID    string                 `json:"revisionId,omitempty"`    // stable revision for idempotent apply
	AssignedNode  string                 `json:"assignedNode,omitempty"`
	Image         string                 `json:"image,omitempty"`       // For docker-container
	Command       string                 `json:"command,omitempty"`     // For docker-container
	CommandList   []string               `json:"commandList,omitempty"` // Preserves tokenized command/args for container workloads
	Compose       string                 `json:"compose,omitempty"`     // Base64-encoded Compose content (optional)
	ComposeYAML   string                 `json:"composeYaml,omitempty"` // Base64-encoded Compose YAML for compute-agent compose spec
	ProjectName   string                 `json:"projectName,omitempty"` // Deterministic compose project name
	GitRepo       string                 `json:"gitRepo,omitempty"`     // Git URL for git-compose
	GitBranch     string                 `json:"gitBranch,omitempty"`   // Git branch for git-compose
	GitToken      string                 `json:"gitToken,omitempty"`    // Optional Git auth token
	EnvVars       map[string]string      `json:"envVars,omitempty"`     // Environment variables
	Resources     Resources              `json:"resources"`
	NodeID        string                 `json:"nodeId,omitempty"`
	Status        string                 `json:"status"`
	DesiredState  string                 `json:"desiredState,omitempty"` // Desired state for reconciliation
	Labels        map[string]string      `json:"labels,omitempty"`
	CreatedAt     time.Time              `json:"createdAt"`
	LocalPath     string                 `json:"localPath,omitempty"`     // Local path for docker-compose
	Ports         []string               `json:"ports,omitempty"`         // e.g., ["8080:80"]
	Volumes       []string               `json:"volumes,omitempty"`       // e.g., ["/host:/container"]
	Network       string                 `json:"network,omitempty"`       // e.g., "bridge"
	RestartPolicy string                 `json:"restartPolicy,omitempty"` // e.g., "always"
	Logs          string                 `json:"logs,omitempty"`          // Execution logs and output
	Metadata      map[string]interface{} `json:"metadata,omitempty"`      // Reconciliation metadata
	Retry         RetryState             `json:"retry"`
	StatusInfo    WorkloadStatusInfo     `json:"statusInfo"`
	VM            *VMSpec                `json:"vm,omitempty"` // VM workload spec
}

type RetryState struct {
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"maxAttempts"`
	NextRetryAt time.Time `json:"nextRetryAt,omitempty"`
}

type WorkloadStatusInfo struct {
	ActualState   string    `json:"actualState,omitempty"`
	LastUpdated   time.Time `json:"lastUpdated,omitempty"`
	FailureReason string    `json:"failureReason,omitempty"`
}

type AssignmentRecord struct {
	WorkloadID string    `json:"workloadId"`
	NodeID     string    `json:"nodeId"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"createdAt"`
}

type ReconciliationRecord struct {
	WorkloadID  string    `json:"workloadId"`
	Action      string    `json:"action"`
	Success     bool      `json:"success"`
	Reason      string    `json:"reason,omitempty"`
	AttemptedAt time.Time `json:"attemptedAt"`
}

type SchedulerEvent struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	WorkloadID string                 `json:"workloadId,omitempty"`
	NodeID     string                 `json:"nodeId,omitempty"`
	Reason     string                 `json:"reason,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
	Details    map[string]interface{} `json:"details,omitempty"`
}

type DriftRecord struct {
	NodeID          string    `json:"nodeId"`
	WorkloadID      string    `json:"workloadId"`
	DriftType       string    `json:"driftType"`
	DetectedAt      time.Time `json:"detectedAt"`
	SchedulerStatus string    `json:"schedulerStatus,omitempty"`
	AgentStatus     string    `json:"agentStatus,omitempty"`
	Action          string    `json:"action,omitempty"`
	Resolved        bool      `json:"resolved"`
	LastError       string    `json:"lastError,omitempty"`
}

// VMSpec defines VM-specific fields for scheduler API and persistence.
type VMSpec struct {
	Name            string            `json:"name,omitempty"`
	VCPUs           int32             `json:"vcpus,omitempty"`
	MemoryMB        int64             `json:"memoryMb,omitempty"`
	Disks           []VMDiskConfig    `json:"disks,omitempty"`
	Networks        []VMNetworkConfig `json:"networks,omitempty"`
	CloudInit       string            `json:"cloudInit,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CloudInitConfig *CloudInitConfig  `json:"cloudInitConfig,omitempty"`
}

type VMDiskConfig struct {
	Path   string `json:"path,omitempty"`
	Device string `json:"device,omitempty"`
	Format string `json:"format,omitempty"`
	SizeGB int64  `json:"sizeGb,omitempty"`
	Type   string `json:"type,omitempty"`
	Boot   bool   `json:"boot,omitempty"`
}

type VMNetworkConfig struct {
	Network   string `json:"network,omitempty"`
	MAC       string `json:"macAddress,omitempty"`
	IPAddress string `json:"ipAddress,omitempty"`
}

type CloudInitConfig struct {
	UserData      string `json:"userData,omitempty"`
	MetaData      string `json:"metaData,omitempty"`
	NetworkConfig string `json:"networkConfig,omitempty"`
	VendorData    string `json:"vendorData,omitempty"`
}

// AgentCommand represents a command payload for the agent API
type AgentCommand struct {
	Command string `json:"command"`
	Image   string `json:"image,omitempty"`
}

// Scheduler manages nodes and workloads
type Scheduler struct {
	etcdClient *clientv3.Client
	mu         sync.Mutex
	httpClient *http.Client
	domain     string // Added domain field for DNS
}
