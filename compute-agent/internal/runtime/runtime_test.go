package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
)

type rtStub struct {
	typeName models.WorkloadType
	err      error
}

func (r *rtStub) Create(ctx context.Context, w *models.Workload) error { return nil }
func (r *rtStub) Start(ctx context.Context, id string) error           { return nil }
func (r *rtStub) Stop(ctx context.Context, id string) error            { return nil }
func (r *rtStub) Delete(ctx context.Context, id string) error          { return nil }
func (r *rtStub) Status(ctx context.Context, id string) (models.ActualState, string, error) {
	return models.ActualStateRunning, "running", nil
}
func (r *rtStub) List(ctx context.Context) ([]string, error) { return nil, nil }
func (r *rtStub) Type() models.WorkloadType                  { return r.typeName }
func (r *rtStub) Healthy(ctx context.Context) error          { return r.err }

func TestManagerRegisterAndGetRuntime(t *testing.T) {
	m := NewManager()
	m.Register(&rtStub{typeName: models.WorkloadTypeContainer})
	if _, err := m.GetRuntime(models.WorkloadTypeContainer); err != nil {
		t.Fatalf("expected runtime, got error: %v", err)
	}
}

func TestHealthCheck(t *testing.T) {
	m := NewManager()
	m.Register(&rtStub{typeName: models.WorkloadTypeContainer, err: nil})
	m.Register(&rtStub{typeName: models.WorkloadTypeVM, err: errors.New("down")})
	res := m.HealthCheck(context.Background())
	if res[string(models.WorkloadTypeContainer)] != "healthy" {
		t.Fatal("expected healthy container runtime")
	}
	if res[string(models.WorkloadTypeVM)] == "healthy" {
		t.Fatal("expected unhealthy vm runtime")
	}
}
