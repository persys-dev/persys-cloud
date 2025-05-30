package models

import (
	"net/http"
	"sync"
	"time"

	"go.etcd.io/etcd/client/v3"
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
	NodeID          string            `json:"nodeId" binding:"required"`
	IPAddress       string            `json:"ipAddress" binding:"required"`
	Username        string            `json:"username" binding:"required"`
	Hostname        string            `json:"hostname" binding:"required"`
	OSName          string            `json:"osName" binding:"required"`
	KernelVersion   string            `json:"kernelVersion" binding:"required"`
	Status          string            `json:"status" binding:"required"`
	Timestamp       string            `json:"timestamp" binding:"required"`
	Resources       Resources         `json:"resources"`
	TotalCPU         float64           `json:"totalCpu"`
    TotalMemory      int64             `json:"totalMemory"`
    AvailableCPU     float64           `json:"availableCpu"`
    AvailableMemory  int64             `json:"availableMemory"`
	Hypervisor      Hypervisor        `json:"hypervisor" binding:"required"`
	ContainerEngine ContainerEngine   `json:"containerEngine" binding:"required"`
	DockerSwarm     DockerSwarm       `json:"dockerSwarm"`
	LastHeartbeat   time.Time         `json:"lastHeartbeat"`
	Labels          map[string]string `json:"labels,omitempty"`
	AgentPort       int               `json:"agentPort"` // Added for agent communication
	DomainName      string            `json:"domainName,omitempty"` // Added field
	AuthConfig      AuthConfig        `json:"authConfig"` // Authentication configuration
}

// Workload represents a scheduled task
type Workload struct {
    ID          string            `json:"id,omitempty"`
    Name        string            `json:"name" binding:"required"`
    Type        string            `json:"type" binding:"required"` // "docker-container", "docker-compose", "git-compose"
    Image       string            `json:"image" binding:"required"`         // For docker-container
    Command     string            `json:"command,omitempty"`       // For docker-container
    Compose     string            `json:"compose,omitempty"`       // Base64-encoded Compose content (optional)
    GitRepo     string            `json:"gitRepo,omitempty"`       // Git URL for git-compose
    GitBranch   string            `json:"gitBranch,omitempty"`     // Git branch for git-compose
    GitToken    string            `json:"gitToken,omitempty"`      // Optional Git auth token
    EnvVars     map[string]string `json:"envVars,omitempty"`       // Environment variables
    Resources   Resources         `json:"resources"`
    NodeID      string            `json:"nodeId,omitempty"`
    Status      string            `json:"status"`
    Labels      map[string]string `json:"labels,omitempty"`
    CreatedAt   time.Time         `json:"createdAt"`
    LocalPath   string            `json:"localPath,omitempty"`     // Local path for docker-compose
	Ports         []string        `json:"ports,omitempty"`    // e.g., ["8080:80"]
    Volumes       []string        `json:"volumes,omitempty"`  // e.g., ["/host:/container"]
    Network       string          `json:"network,omitempty"`  // e.g., "bridge"
    RestartPolicy string          `json:"restartPolicy,omitempty"` // e.g., "always"
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
