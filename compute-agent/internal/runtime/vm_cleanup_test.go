package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestParseDiskSourceFilePaths(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<domain type="kvm">
  <devices>
    <disk type="file" device="disk">
      <source file="/var/lib/libvirt/images/vm-disk.qcow2"/>
    </disk>
    <disk type="network" device="disk">
      <source file="/should/not/be/included.qcow2"/>
    </disk>
    <disk type="file" device="cdrom">
      <source file="/var/lib/libvirt/images/vm-cloud-init.iso"/>
    </disk>
  </devices>
</domain>`

	paths, err := parseDiskSourceFilePaths(xmlData)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 file-backed disk paths, got %d", len(paths))
	}
	if paths[0] != "/var/lib/libvirt/images/vm-disk.qcow2" {
		t.Fatalf("unexpected first path: %s", paths[0])
	}
	if paths[1] != "/var/lib/libvirt/images/vm-cloud-init.iso" {
		t.Fatalf("unexpected second path: %s", paths[1])
	}
}

func TestCleanupDiskArtifacts_RemovesOnlyManagedAndCloudInit(t *testing.T) {
	tmpDir := t.TempDir()
	vmID := "vm-123"

	managedDisk := filepath.Join(tmpDir, "managed.qcow2")
	externalDisk := filepath.Join(tmpDir, "external.qcow2")
	cloudInitISO := filepath.Join(tmpDir, vmID+"-cloud-init.iso")

	for _, p := range []string{managedDisk, externalDisk, cloudInitISO} {
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatalf("failed to create test file %s: %v", p, err)
		}
	}
	if err := os.WriteFile(managedDiskMarkerPath(managedDisk), []byte("owned"), 0644); err != nil {
		t.Fatalf("failed to create marker: %v", err)
	}

	rt := &VMRuntime{logger: logrus.New().WithField("runtime", "vm-test")}
	rt.cleanupDiskArtifacts(vmID, []string{managedDisk, externalDisk, cloudInitISO})

	if _, err := os.Stat(managedDisk); !os.IsNotExist(err) {
		t.Fatalf("expected managed disk to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(managedDiskMarkerPath(managedDisk)); !os.IsNotExist(err) {
		t.Fatalf("expected managed marker to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(cloudInitISO); !os.IsNotExist(err) {
		t.Fatalf("expected cloud-init ISO to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(externalDisk); err != nil {
		t.Fatalf("expected external disk to remain, stat err=%v", err)
	}
}
