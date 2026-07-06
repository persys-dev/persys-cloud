package garbage

import (
	"context"
	"testing"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/runtime"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Enabled {
		t.Fatal("expected gc enabled by default")
	}
	if cfg.Interval <= 0 || cfg.FailedWorkloadTTL <= 0 {
		t.Fatal("expected positive gc intervals")
	}
}

func TestCollectOrphanedResources_SkipsVMRuntimeDeletion(t *testing.T) {
	rtMgr := runtime.NewManager()
	vmStub := &gcRuntimeStub{
		typ:   models.WorkloadTypeVM,
		items: []string{"external-vm"},
	}
	rtMgr.Register(vmStub)

	store := &gcStoreStub{}
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	collector := NewCollector(&CollectorConfig{
		Enabled:                true,
		Interval:               time.Minute,
		FailedWorkloadTTL:      time.Hour,
		MaxOrphanedResourceAge: time.Hour,
	}, store, rtMgr, logger)

	deleted := collector.collectOrphanedResources(context.Background())
	if deleted != 0 {
		t.Fatalf("expected no orphaned deletions for VM runtime, got %d", deleted)
	}
	if len(vmStub.deletedIDs) != 0 {
		t.Fatalf("expected VM delete not called, got deletions: %v", vmStub.deletedIDs)
	}
}

type gcStoreStub struct{}

func (s *gcStoreStub) SaveWorkload(workload *models.Workload) error { return nil }
func (s *gcStoreStub) GetWorkload(id string) (*models.Workload, error) {
	return nil, stateErrNotFound
}
func (s *gcStoreStub) DeleteWorkload(id string) error { return nil }
func (s *gcStoreStub) ListWorkloads() ([]*models.Workload, error) {
	return []*models.Workload{}, nil
}
func (s *gcStoreStub) SaveStatus(status *models.WorkloadStatus) error { return nil }
func (s *gcStoreStub) GetStatus(id string) (*models.WorkloadStatus, error) {
	return nil, stateErrNotFound
}
func (s *gcStoreStub) ListStatuses() ([]*models.WorkloadStatus, error) {
	return []*models.WorkloadStatus{}, nil
}
func (s *gcStoreStub) Close() error { return nil }

var stateErrNotFound = stateNotFoundError("not found")

type stateNotFoundError string

func (e stateNotFoundError) Error() string { return string(e) }

type gcRuntimeStub struct {
	typ        models.WorkloadType
	items      []string
	deletedIDs []string
}

func (r *gcRuntimeStub) Create(ctx context.Context, workload *models.Workload) error { return nil }
func (r *gcRuntimeStub) Start(ctx context.Context, id string) error                  { return nil }
func (r *gcRuntimeStub) Stop(ctx context.Context, id string) error                   { return nil }
func (r *gcRuntimeStub) Delete(ctx context.Context, id string) error {
	r.deletedIDs = append(r.deletedIDs, id)
	return nil
}
func (r *gcRuntimeStub) Status(ctx context.Context, id string) (models.ActualState, string, error) {
	return models.ActualStateRunning, "running", nil
}
func (r *gcRuntimeStub) List(ctx context.Context) ([]string, error) { return r.items, nil }
func (r *gcRuntimeStub) Type() models.WorkloadType                  { return r.typ }
func (r *gcRuntimeStub) Healthy(ctx context.Context) error          { return nil }
