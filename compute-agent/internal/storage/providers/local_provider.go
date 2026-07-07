package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/platform"
)

const defaultLocalVolumeRoot = "/var/lib/persys/volumes/local"

type LocalProvider struct {
	basePath string
}

func NewLocalProvider(basePath string) *LocalProvider {
	root := strings.TrimSpace(basePath)
	if root == "" {
		root = defaultLocalVolumeRoot
	}
	return &LocalProvider{basePath: filepath.Clean(root)}
}

func (p *LocalProvider) Driver() string {
	return "local"
}

func (p *LocalProvider) Validate(_ context.Context, spec platform.VolumeSpec) error {
	_, err := normalizeVolumeName(spec.Name)
	return err
}

func (p *LocalProvider) Provision(_ context.Context, spec platform.VolumeSpec) (*platform.VolumeHandle, error) {
	name, err := normalizeVolumeName(spec.Name)
	if err != nil {
		return nil, err
	}
	path, err := safeJoin(p.basePath, name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("create local volume directory: %w", err)
	}
	now := time.Now().UTC()
	return &platform.VolumeHandle{
		ID:        volumeID(p.Driver(), name),
		Name:      name,
		Driver:    p.Driver(),
		SizeGB:    spec.SizeGB,
		Device:    path,
		StagePath: path,
		Metadata: map[string]string{
			"driver":  p.Driver(),
			"fs_type": strings.TrimSpace(spec.FSType),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (p *LocalProvider) Delete(_ context.Context, handle *platform.VolumeHandle) error {
	if handle == nil || strings.TrimSpace(handle.Device) == "" {
		return nil
	}
	cleanBase := filepath.Clean(p.basePath)
	cleanPath := filepath.Clean(handle.Device)
	rel, err := filepath.Rel(cleanBase, cleanPath)
	if err != nil {
		return fmt.Errorf("validate local delete path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("refusing to delete path outside local storage root: %q", cleanPath)
	}
	path := filepath.Join(cleanBase, rel)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("delete local volume path: %w", err)
	}
	return nil
}

func (p *LocalProvider) Attach(_ context.Context, handle *platform.VolumeHandle, workloadID string, mountPath string, readOnly bool) (*platform.VolumeAttachment, error) {
	if handle == nil {
		return nil, fmt.Errorf("volume handle is required")
	}
	now := time.Now().UTC()
	return &platform.VolumeAttachment{
		ID:         fmt.Sprintf("%s:%s", handle.ID, strings.TrimSpace(workloadID)),
		VolumeID:   handle.ID,
		WorkloadID: strings.TrimSpace(workloadID),
		MountPath:  strings.TrimSpace(mountPath),
		ReadOnly:   readOnly,
		StagePath:  handle.Device,
		Metadata: map[string]string{
			"driver": p.Driver(),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (p *LocalProvider) Detach(_ context.Context, _ *platform.VolumeAttachment) error {
	return nil
}
