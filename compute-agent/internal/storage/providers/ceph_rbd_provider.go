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

const (
	defaultCephRBDPool      = "rbd"
	defaultCephRBDStageRoot = "/var/lib/persys/volumes/ceph-rbd"
)

type CephRBDProvider struct {
	cluster   string
	pool      string
	user      string
	keyring   string
	stageRoot string
}

func NewCephRBDProvider(cluster, pool, user, keyring, stageRoot string) *CephRBDProvider {
	resolvedPool := strings.TrimSpace(pool)
	if resolvedPool == "" {
		resolvedPool = defaultCephRBDPool
	}
	root := strings.TrimSpace(stageRoot)
	if root == "" {
		root = defaultCephRBDStageRoot
	}
	return &CephRBDProvider{
		cluster:   strings.TrimSpace(cluster),
		pool:      resolvedPool,
		user:      strings.TrimSpace(user),
		keyring:   strings.TrimSpace(keyring),
		stageRoot: filepath.Clean(root),
	}
}

func (p *CephRBDProvider) Driver() string {
	return "ceph-rbd"
}

func (p *CephRBDProvider) Validate(_ context.Context, spec platform.VolumeSpec) error {
	if strings.TrimSpace(p.pool) == "" {
		return fmt.Errorf("ceph pool is required")
	}
	_, err := normalizeVolumeName(spec.Name)
	return err
}

func (p *CephRBDProvider) Provision(_ context.Context, spec platform.VolumeSpec) (*platform.VolumeHandle, error) {
	name, err := normalizeVolumeName(spec.Name)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &platform.VolumeHandle{
		ID:     volumeID(p.Driver(), name),
		Name:   name,
		Driver: p.Driver(),
		SizeGB: spec.SizeGB,
		Device: fmt.Sprintf("rbd:%s/%s", p.pool, name),
		Metadata: map[string]string{
			"driver":  p.Driver(),
			"cluster": p.cluster,
			"pool":    p.pool,
			"user":    p.user,
			"keyring": p.keyring,
			"fs_type": strings.TrimSpace(spec.FSType),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (p *CephRBDProvider) Delete(_ context.Context, _ *platform.VolumeHandle) error {
	// RBD image lifecycle hooks are introduced with runtime wiring in a later slice.
	return nil
}

func (p *CephRBDProvider) Attach(_ context.Context, handle *platform.VolumeHandle, workloadID string, mountPath string, readOnly bool) (*platform.VolumeAttachment, error) {
	if handle == nil {
		return nil, fmt.Errorf("volume handle is required")
	}
	stageDir, err := safeJoin(p.stageRoot, filepath.Join(strings.TrimSpace(workloadID), strings.ReplaceAll(handle.ID, ":", "_")))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return nil, fmt.Errorf("create ceph-rbd stage directory: %w", err)
	}
	now := time.Now().UTC()
	return &platform.VolumeAttachment{
		ID:         fmt.Sprintf("%s:%s", handle.ID, strings.TrimSpace(workloadID)),
		VolumeID:   handle.ID,
		WorkloadID: strings.TrimSpace(workloadID),
		MountPath:  strings.TrimSpace(mountPath),
		ReadOnly:   readOnly,
		StagePath:  stageDir,
		Metadata: map[string]string{
			"driver":  p.Driver(),
			"cluster": p.cluster,
			"pool":    p.pool,
			"user":    p.user,
			"keyring": p.keyring,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (p *CephRBDProvider) Detach(_ context.Context, attachment *platform.VolumeAttachment) error {
	if attachment == nil || strings.TrimSpace(attachment.StagePath) == "" {
		return nil
	}
	return os.RemoveAll(attachment.StagePath)
}
