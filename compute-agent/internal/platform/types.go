package platform

import (
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
)

// VolumeSpec describes a managed volume request independent of runtime backend.
type VolumeSpec struct {
	Name         string
	Driver       string
	SizeGB       int64
	AccessMode   string
	FSType       string
	MountPath    string
	ReadOnly     bool
	RetainPolicy string
}

// VolumeHandle is a provider-managed volume identity and staging metadata.
type VolumeHandle struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Driver    string            `json:"driver"`
	SizeGB    int64             `json:"size_gb,omitempty"`
	Device    string            `json:"device,omitempty"`
	StagePath string            `json:"stage_path,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
}

// VolumeAttachment is a provider-managed attachment for a workload.
type VolumeAttachment struct {
	ID         string            `json:"id"`
	VolumeID   string            `json:"volume_id"`
	WorkloadID string            `json:"workload_id"`
	MountPath  string            `json:"mount_path"`
	ReadOnly   bool              `json:"read_only,omitempty"`
	StagePath  string            `json:"stage_path,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"created_at,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at,omitempty"`
}

// WorkloadNetSpec describes a runtime-agnostic network request.
type WorkloadNetSpec struct {
	Network   string
	MAC       string
	IPAddress string
}

// VolumeSpecFromModel converts API/workload managed volume to platform spec.
func VolumeSpecFromModel(in models.ManagedVolumeSpec) VolumeSpec {
	return VolumeSpec{
		Name:         strings.TrimSpace(in.Name),
		Driver:       strings.TrimSpace(in.Driver),
		SizeGB:       in.SizeGB,
		AccessMode:   strings.TrimSpace(in.AccessMode),
		FSType:       strings.TrimSpace(in.FSType),
		MountPath:    strings.TrimSpace(in.MountPath),
		ReadOnly:     in.ReadOnly,
		RetainPolicy: strings.TrimSpace(in.RetainPolicy),
	}
}
