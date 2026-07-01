// Package models defines the canonical Persys resource types shared across
// all SDK consumers: persysctl, operators, controllers, and automation services.
//
// These types are promoted directly from persysctl's internal/models so that
// persysctl can import them from the SDK without any conversion layer.
package models

import "time"

// Resources describes CPU and memory allocation.
type Resources struct {
	// CPU is millicores (e.g. 500 = 0.5 cores).
	CPU int `json:"cpu"`
	// Memory is mebibytes.
	Memory int `json:"memory"`
}

// Workload is the canonical representation of a Persys compute workload.
// It covers containers, Docker Compose stacks, and virtual machines.
type Workload struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`

	// Type is one of: "container", "docker-container",
	// "compose", "docker-compose", "git-compose", "vm".
	Type string `json:"type"`

	// Container fields
	Image   string `json:"image,omitempty"`
	Command string `json:"command,omitempty"`

	// Compose fields
	Compose   string `json:"compose,omitempty"`   // raw YAML or base64
	LocalPath string `json:"localPath,omitempty"` // path to a local compose file

	// Git-compose fields
	GitRepo   string `json:"gitRepo,omitempty"`
	GitBranch string `json:"gitBranch,omitempty"`
	GitToken  string `json:"gitToken,omitempty"`

	// Runtime config
	EnvVars        map[string]string  `json:"envVars,omitempty"`
	Ports          []string           `json:"ports,omitempty"`   // e.g. ["8080:80"]
	Volumes        []string           `json:"volumes,omitempty"` // e.g. ["/host:/container"]
	ManagedVolumes []ManagedVolumeSpec `json:"managedVolumes,omitempty"`
	Network        string             `json:"network,omitempty"`
	RestartPolicy  string             `json:"restartPolicy,omitempty"`
	Resources      Resources          `json:"resources,omitempty"`
	Labels         map[string]string  `json:"labels,omitempty"`
	Metadata       map[string]string  `json:"metadata,omitempty"`

	// Scheduler state (populated by responses)
	NodeID        string          `json:"nodeId,omitempty"`
	DesiredState  string          `json:"desiredState,omitempty"`
	Status        string          `json:"status"`
	RevisionID    string          `json:"revisionId,omitempty"`
	RetryAttempts int32           `json:"retryAttempts,omitempty"`
	RetryMax      int32           `json:"retryMaxAttempts,omitempty"`
	RetryNextAt   time.Time       `json:"retryNextAt,omitempty"`
	FailureReason string          `json:"failureReason,omitempty"`
	Reason        *WorkloadReason `json:"reason,omitempty"`
	Message       string          `json:"message,omitempty"`
	Usage         *WorkloadUsage  `json:"usage,omitempty"`
	CreatedAt     time.Time       `json:"createdAt,omitempty"`
	LastUpdated   time.Time       `json:"lastUpdated,omitempty"`
}

// ManagedVolumeSpec describes a platform-managed persistent volume.
type ManagedVolumeSpec struct {
	Name         string `json:"name,omitempty"`
	Driver       string `json:"driver,omitempty"`
	SizeGB       int64  `json:"sizeGb,omitempty"`
	AccessMode   string `json:"accessMode,omitempty"`
	FSType       string `json:"fsType,omitempty"`
	MountPath    string `json:"mountPath,omitempty"`
	ReadOnly     bool   `json:"readOnly,omitempty"`
	RetainPolicy string `json:"retainPolicy,omitempty"`
}

// WorkloadReason provides structured failure detail.
type WorkloadReason struct {
	Code           string    `json:"code,omitempty"`
	Message        string    `json:"message,omitempty"`
	LastTransition time.Time `json:"lastTransition,omitempty"`
	NextRetryAt    time.Time `json:"nextRetryAt,omitempty"`
	Retryable      bool      `json:"retryable,omitempty"`
}

// WorkloadUsage is a point-in-time resource usage snapshot.
type WorkloadUsage struct {
	WorkloadID     string    `json:"workloadId,omitempty"`
	Type           string    `json:"type,omitempty"`
	CPUPercent     float64   `json:"cpuPercent,omitempty"`
	MemoryBytes    int64     `json:"memoryBytes,omitempty"`
	DiskReadBytes  int64     `json:"diskReadBytes,omitempty"`
	DiskWriteBytes int64     `json:"diskWriteBytes,omitempty"`
	NetRXBytes     int64     `json:"netRxBytes,omitempty"`
	NetTXBytes     int64     `json:"netTxBytes,omitempty"`
	CollectedAt    time.Time `json:"collectedAt,omitempty"`
	Source         string    `json:"source,omitempty"`
}

// Node is a compute node registered with the Persys scheduler.
type Node struct {
	NodeID        string            `json:"nodeId"`
	IPAddress     string            `json:"ipAddress"`
	Status        string            `json:"status"`
	LastHeartbeat time.Time         `json:"lastHeartbeat"`
	Resources     Resources         `json:"resources"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// ScheduleResponse is returned by workload scheduling operations.
type ScheduleResponse struct {
	WorkloadID string `json:"workloadId"`
	NodeID     string `json:"nodeId"`
	Status     string `json:"status"`
}
