package models

import (
	"time"
)

type Resources struct {
	CPU    int `json:"cpu"`
	Memory int `json:"memory"`
}

type Workload struct {
	ID             string              `json:"id"`
	Name           string              `json:"name,omitempty"`
	Type           string              `json:"type"`
	Image          string              `json:"image,omitempty"`
	Command        string              `json:"command,omitempty"`
	Compose        string              `json:"compose,omitempty"`
	GitRepo        string              `json:"gitRepo,omitempty"`
	GitBranch      string              `json:"gitBranch,omitempty"`
	GitToken       string              `json:"gitToken,omitempty"`
	LocalPath      string              `json:"localPath,omitempty"`
	EnvVars        map[string]string   `json:"envVars,omitempty"`
	Ports          []string            `json:"ports,omitempty"`   // e.g., ["8080:80"]
	Volumes        []string            `json:"volumes,omitempty"` // e.g., ["/host:/container"]
	ManagedVolumes []ManagedVolumeSpec `json:"managedVolumes,omitempty"`
	Network        string              `json:"network,omitempty"`       // e.g., "bridge"
	RestartPolicy  string              `json:"restartPolicy,omitempty"` // e.g., "always"
	Resources      Resources           `json:"resources,omitempty"`
	NodeID         string              `json:"nodeId,omitempty"`
	DesiredState   string              `json:"desiredState,omitempty"`
	Status         string              `json:"status"`
	RevisionID     string              `json:"revisionId,omitempty"`
	RetryAttempts  int32               `json:"retryAttempts,omitempty"`
	RetryMax       int32               `json:"retryMaxAttempts,omitempty"`
	RetryNextAt    time.Time           `json:"retryNextAt,omitempty"`
	FailureReason  string              `json:"failureReason,omitempty"`
	Reason         *WorkloadReason     `json:"reason,omitempty"`
	Message        string              `json:"message,omitempty"`
	Usage          *WorkloadUsage      `json:"usage,omitempty"`
	Labels         map[string]string   `json:"labels,omitempty"`
	Metadata       map[string]string   `json:"metadata,omitempty"`
	CreatedAt      time.Time           `json:"createdAt,omitempty"`
	LastUpdated    time.Time           `json:"lastUpdated,omitempty"`
}

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

type WorkloadReason struct {
	Code           string    `json:"code,omitempty"`
	Message        string    `json:"message,omitempty"`
	LastTransition time.Time `json:"lastTransition,omitempty"`
	NextRetryAt    time.Time `json:"nextRetryAt,omitempty"`
	Retryable      bool      `json:"retryable,omitempty"`
}

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

type Node struct {
	NodeID        string            `json:"nodeId"`
	IPAddress     string            `json:"ipAddress"`
	Status        string            `json:"status"`
	LastHeartbeat time.Time         `json:"lastHeartbeat"`
	Resources     Resources         `json:"resources"`
	Labels        map[string]string `json:"labels,omitempty"`
}
