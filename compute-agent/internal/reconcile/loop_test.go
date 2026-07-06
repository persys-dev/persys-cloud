package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/runtime"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/state"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/workload"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
)

type storeStub struct{}

func (s *storeStub) SaveWorkload(workload *models.Workload) error { return nil }
func (s *storeStub) GetWorkload(id string) (*models.Workload, error) {
	return nil, errors.New("not found")
}
func (s *storeStub) DeleteWorkload(id string) error                 { return nil }
func (s *storeStub) ListWorkloads() ([]*models.Workload, error)     { return []*models.Workload{}, nil }
func (s *storeStub) SaveStatus(status *models.WorkloadStatus) error { return nil }
func (s *storeStub) GetStatus(id string) (*models.WorkloadStatus, error) {
	return nil, errors.New("not found")
}
func (s *storeStub) ListStatuses() ([]*models.WorkloadStatus, error) {
	return []*models.WorkloadStatus{}, nil
}
func (s *storeStub) Close() error { return nil }

var _ state.Store = (*storeStub)(nil)

func TestReconcileNow_NoWorkloads(t *testing.T) {
	logger := logrus.New()
	st := &storeStub{}
	mgr := workload.NewManager(st, runtime.NewManager(), logger)
	loop := NewLoop(st, mgr, 0, logger)
	loop.ReconcileNow(context.Background())
}
