package providers

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/platform"
)

const defaultNFSStageRoot = "/var/lib/persys/volumes/nfs"

type NFSProvider struct {
	server       string
	exportPath   string
	stageRoot    string
	mountOptions string
}

func NewNFSProvider(server, exportPath, stageRoot, mountOptions string) *NFSProvider {
	root := strings.TrimSpace(stageRoot)
	if root == "" {
		root = defaultNFSStageRoot
	}
	return &NFSProvider{
		server:       strings.TrimSpace(server),
		exportPath:   strings.TrimSpace(exportPath),
		stageRoot:    filepath.Clean(root),
		mountOptions: strings.TrimSpace(mountOptions),
	}
}

func (p *NFSProvider) Driver() string {
	return "nfs"
}

func (p *NFSProvider) Validate(_ context.Context, spec platform.VolumeSpec) error {
	if strings.TrimSpace(p.server) == "" {
		return fmt.Errorf("nfs server is required")
	}
	if strings.TrimSpace(p.exportPath) == "" {
		return fmt.Errorf("nfs export path is required")
	}
	_, err := normalizeVolumeName(spec.Name)
	return err
}

func (p *NFSProvider) Provision(_ context.Context, spec platform.VolumeSpec) (*platform.VolumeHandle, error) {
	name, err := normalizeVolumeName(spec.Name)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	exportPath := strings.TrimSpace(p.exportPath)
	remotePath := path.Join("/", exportPath, name)
	return &platform.VolumeHandle{
		ID:     volumeID(p.Driver(), name),
		Name:   name,
		Driver: p.Driver(),
		SizeGB: spec.SizeGB,
		Device: fmt.Sprintf("nfs://%s%s", p.server, remotePath),
		Metadata: map[string]string{
			"driver":        p.Driver(),
			"nfs_server":    p.server,
			"nfs_export":    p.exportPath,
			"nfs_path":      remotePath,
			"mount_options": p.mountOptions,
			"fs_type":       strings.TrimSpace(spec.FSType),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (p *NFSProvider) Delete(_ context.Context, _ *platform.VolumeHandle) error {
	// NFS volumes are assumed externally managed by the export backend for now.
	return nil
}

func (p *NFSProvider) Attach(_ context.Context, handle *platform.VolumeHandle, workloadID string, mountPath string, readOnly bool) (*platform.VolumeAttachment, error) {
	if handle == nil {
		return nil, fmt.Errorf("volume handle is required")
	}
	stageDir, err := safeJoin(p.stageRoot, filepath.Join(strings.TrimSpace(workloadID), strings.ReplaceAll(handle.ID, ":", "_")))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return nil, fmt.Errorf("create nfs stage directory: %w", err)
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
			"driver":        p.Driver(),
			"nfs_server":    p.server,
			"nfs_export":    p.exportPath,
			"mount_options": p.mountOptions,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (p *NFSProvider) Detach(_ context.Context, attachment *platform.VolumeAttachment) error {
	if attachment == nil || strings.TrimSpace(attachment.StagePath) == "" {
		return nil
	}
	return os.RemoveAll(attachment.StagePath)
}
