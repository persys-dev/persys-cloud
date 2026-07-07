package providers

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/platform"
)

func TestLocalProvider_ProvisionAndAttach(t *testing.T) {
	p := NewLocalProvider(filepath.Join(t.TempDir(), "local"))
	spec := platform.VolumeSpec{Name: "cache-data", Driver: "local", MountPath: "/data"}

	handle, err := p.Provision(context.Background(), spec)
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	if handle.ID == "" || handle.Device == "" {
		t.Fatalf("expected local handle id/device to be set")
	}

	attachment, err := p.Attach(context.Background(), handle, "w1", spec.MountPath, false)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if attachment.StagePath == "" {
		t.Fatalf("expected stage path")
	}
}

func TestNFSProvider_ValidateAndAttach(t *testing.T) {
	p := NewNFSProvider("10.0.0.10", "/exports/workloads", filepath.Join(t.TempDir(), "nfs"), "vers=4.1")
	spec := platform.VolumeSpec{Name: "team-a", Driver: "nfs", MountPath: "/mnt/shared"}
	if err := p.Validate(context.Background(), spec); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	handle, err := p.Provision(context.Background(), spec)
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	if handle.Device != "nfs://10.0.0.10/exports/workloads/team-a" {
		t.Fatalf("unexpected nfs device URI: %s", handle.Device)
	}
	if handle.Metadata["nfs_path"] != "/exports/workloads/team-a" {
		t.Fatalf("unexpected nfs_path metadata: %s", handle.Metadata["nfs_path"])
	}
	attachment, err := p.Attach(context.Background(), handle, "workload-a", spec.MountPath, true)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if attachment.Metadata["driver"] != "nfs" {
		t.Fatalf("expected nfs driver metadata")
	}
}

func TestCephRBDProvider_ValidateAndAttach(t *testing.T) {
	p := NewCephRBDProvider("ceph", "rbd", "client.persys", "/etc/ceph/keyring", filepath.Join(t.TempDir(), "ceph"))
	spec := platform.VolumeSpec{Name: "vm-root", Driver: "ceph-rbd", MountPath: "/var/lib/vm"}
	if err := p.Validate(context.Background(), spec); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	handle, err := p.Provision(context.Background(), spec)
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	attachment, err := p.Attach(context.Background(), handle, "vm-1", spec.MountPath, false)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if attachment.Metadata["driver"] != "ceph-rbd" {
		t.Fatalf("expected ceph-rbd driver metadata")
	}
}
