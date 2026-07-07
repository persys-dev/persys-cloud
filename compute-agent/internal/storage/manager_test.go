package storage

import "testing"

func TestCreatePool_ValidatesTypeConfigAndDefaults(t *testing.T) {
	m := NewManager()

	err := m.CreatePool(&Pool{
		Name:        "local-a",
		Type:        PoolTypeLocal,
		TotalSizeGB: 100,
		Config: map[string]string{
			"path": "/data/pool-a",
		},
	})
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}

	p, err := m.GetPool("local-a")
	if err != nil {
		t.Fatalf("get pool failed: %v", err)
	}

	if p.WarningThreshold != 80 {
		t.Fatalf("expected default warning threshold 80, got %d", p.WarningThreshold)
	}
	if !p.Active || !p.Healthy || p.Status == "" {
		t.Fatalf("expected pool defaults active+healthy+status, got active=%v healthy=%v status=%q", p.Active, p.Healthy, p.Status)
	}
	if p.UUID == "" {
		t.Fatalf("expected UUID to be set")
	}
}

func TestCreatePool_MissingRequiredConfigFails(t *testing.T) {
	m := NewManager()

	err := m.CreatePool(&Pool{
		Name:        "nfs-a",
		Type:        PoolTypeNFS,
		TotalSizeGB: 100,
		Config: map[string]string{
			"server": "10.0.0.10",
		},
	})
	if err == nil {
		t.Fatalf("expected error for missing nfs export config")
	}
}

func TestAllocateDisk_SetsPathAndDeletePoolMissingFails(t *testing.T) {
	m := NewManager()
	if err := m.CreatePool(&Pool{
		Name:        "local-b",
		Type:        PoolTypeLocal,
		TotalSizeGB: 50,
		Config: map[string]string{
			"path": "/var/lib/persys/storage",
		},
	}); err != nil {
		t.Fatalf("create pool failed: %v", err)
	}

	alloc, err := m.AllocateDisk("local-b", 10, "")
	if err != nil {
		t.Fatalf("allocate disk failed: %v", err)
	}
	if alloc.Path == "" {
		t.Fatalf("expected allocation path to be populated")
	}
	if alloc.Format != "qcow2" {
		t.Fatalf("expected default format qcow2, got %s", alloc.Format)
	}

	if err := m.DeletePool("does-not-exist"); err == nil {
		t.Fatalf("expected deleting a missing pool to fail")
	}
}
