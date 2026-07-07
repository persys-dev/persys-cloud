package state

import (
	"path/filepath"
	"testing"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/platform"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
)

func TestBoltStore_WorkloadAndStatusLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := NewBoltStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	w := &models.Workload{ID: "w1", Type: models.WorkloadTypeContainer, RevisionID: "r1"}
	if err := s.SaveWorkload(w); err != nil {
		t.Fatalf("save workload failed: %v", err)
	}
	if _, err := s.GetWorkload("w1"); err != nil {
		t.Fatalf("get workload failed: %v", err)
	}

	st := &models.WorkloadStatus{ID: "w1", Type: models.WorkloadTypeContainer}
	if err := s.SaveStatus(st); err != nil {
		t.Fatalf("save status failed: %v", err)
	}
	if _, err := s.GetStatus("w1"); err != nil {
		t.Fatalf("get status failed: %v", err)
	}

	if err := s.DeleteWorkload("w1"); err != nil {
		t.Fatalf("delete workload failed: %v", err)
	}
	if _, err := s.GetWorkload("w1"); err == nil {
		t.Fatal("expected workload to be deleted")
	}
}

func TestBoltStore_VolumeAndAttachmentLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := NewBoltStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	vs, ok := s.(ManagedVolumeStore)
	if !ok {
		t.Fatalf("store does not implement ManagedVolumeStore")
	}

	volume := &platform.VolumeHandle{
		ID:     "local:vol-a",
		Name:   "vol-a",
		Driver: "local",
		Device: "/var/lib/persys/volumes/local/vol-a",
	}
	if err := vs.SaveVolume(volume); err != nil {
		t.Fatalf("save volume failed: %v", err)
	}
	if _, err := vs.GetVolume(volume.ID); err != nil {
		t.Fatalf("get volume failed: %v", err)
	}
	listedVolumes, err := vs.ListVolumes()
	if err != nil {
		t.Fatalf("list volumes failed: %v", err)
	}
	if len(listedVolumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(listedVolumes))
	}

	attachment := &platform.VolumeAttachment{
		ID:         "local:vol-a:w1",
		VolumeID:   "local:vol-a",
		WorkloadID: "w1",
		MountPath:  "/data",
	}
	if err := vs.SaveAttachment(attachment); err != nil {
		t.Fatalf("save attachment failed: %v", err)
	}
	if _, err := vs.GetAttachment(attachment.ID); err != nil {
		t.Fatalf("get attachment failed: %v", err)
	}
	listedAttachments, err := vs.ListAttachments("w1")
	if err != nil {
		t.Fatalf("list attachments failed: %v", err)
	}
	if len(listedAttachments) != 1 {
		t.Fatalf("expected 1 attachment for workload w1, got %d", len(listedAttachments))
	}

	if err := s.DeleteWorkload("w1"); err != nil {
		t.Fatalf("delete workload failed: %v", err)
	}
	listedAttachments, err = vs.ListAttachments("w1")
	if err != nil {
		t.Fatalf("list attachments after workload delete failed: %v", err)
	}
	if len(listedAttachments) != 0 {
		t.Fatalf("expected attachment cleanup on workload delete, got %d", len(listedAttachments))
	}
}
