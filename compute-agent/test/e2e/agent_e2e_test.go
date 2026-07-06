package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cfg "github.com/persys-dev/persys-cloud/compute-agent/internal/config"
	agentgrpc "github.com/persys-dev/persys-cloud/compute-agent/internal/grpc"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/runtime"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/state"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/workload"
	pb "github.com/persys-dev/persys-cloud/compute-agent/pkg/api/v1"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
)

type fakeRuntime struct {
	typeName models.WorkloadType
	items    map[string]models.ActualState
}

func newFakeRuntime(t models.WorkloadType) *fakeRuntime {
	return &fakeRuntime{typeName: t, items: make(map[string]models.ActualState)}
}

func (f *fakeRuntime) Create(ctx context.Context, workload *models.Workload) error {
	f.items[workload.ID] = models.ActualStateStopped
	return nil
}
func (f *fakeRuntime) Start(ctx context.Context, id string) error {
	f.items[id] = models.ActualStateRunning
	return nil
}
func (f *fakeRuntime) Stop(ctx context.Context, id string) error {
	f.items[id] = models.ActualStateStopped
	return nil
}
func (f *fakeRuntime) Delete(ctx context.Context, id string) error {
	delete(f.items, id)
	return nil
}
func (f *fakeRuntime) Status(ctx context.Context, id string) (models.ActualState, string, error) {
	st, ok := f.items[id]
	if !ok {
		return models.ActualStateUnknown, "not found", nil
	}
	return st, string(st), nil
}
func (f *fakeRuntime) List(ctx context.Context) ([]string, error) {
	ids := make([]string, 0, len(f.items))
	for id := range f.items {
		ids = append(ids, id)
	}
	return ids, nil
}
func (f *fakeRuntime) Type() models.WorkloadType         { return f.typeName }
func (f *fakeRuntime) Healthy(ctx context.Context) error { return nil }

func newTestServer(t *testing.T) *agentgrpc.Server {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "state.db")

	store, err := state.NewBoltStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger := logrus.New()
	logger.SetOutput(os.Stdout)
	logger.SetLevel(logrus.ErrorLevel)

	runtimeMgr := runtime.NewManager()
	runtimeMgr.Register(newFakeRuntime(models.WorkloadTypeContainer))
	runtimeMgr.Register(newFakeRuntime(models.WorkloadTypeCompose))
	runtimeMgr.Register(newFakeRuntime(models.WorkloadTypeVM))

	mgr := workload.NewManager(store, runtimeMgr, logger)
	conf := &cfg.Config{NodeID: "e2e-node", GRPCAddr: "127.0.0.1", GRPCPort: 50051, TLSEnabled: false, DockerEnabled: true, ComposeEnabled: true, VMEnabled: true}

	srv, err := agentgrpc.NewServer(conf, mgr, runtimeMgr, logger)
	if err != nil {
		t.Fatalf("failed to create grpc server: %v", err)
	}
	return srv
}

func assertLifecycle(t *testing.T, server *agentgrpc.Server, id string, wType pb.WorkloadType, spec *pb.WorkloadSpec) {
	t.Helper()
	ctx := context.Background()

	applyResp, err := server.ApplyWorkload(ctx, &pb.ApplyWorkloadRequest{Id: id, Type: wType, RevisionId: "rev-1", DesiredState: pb.DesiredState_DESIRED_STATE_RUNNING, Spec: spec})
	if err != nil {
		t.Fatalf("apply workload rpc failed: %v", err)
	}
	if !applyResp.Applied || applyResp.Status == nil || applyResp.Status.ActualState != pb.ActualState_ACTUAL_STATE_RUNNING {
		t.Fatalf("unexpected apply response: %+v", applyResp)
	}

	statusResp, err := server.GetWorkloadStatus(ctx, &pb.GetWorkloadStatusRequest{Id: id})
	if err != nil || statusResp.Status == nil || statusResp.Status.Id != id {
		t.Fatalf("unexpected status response: %+v err=%v", statusResp, err)
	}

	listResp, err := server.ListWorkloads(ctx, &pb.ListWorkloadsRequest{Type: wType})
	if err != nil {
		t.Fatalf("list workloads rpc failed: %v", err)
	}
	if len(listResp.Workloads) != 1 {
		t.Fatalf("expected one workload for type %s, got %d", wType, len(listResp.Workloads))
	}

	delResp, err := server.DeleteWorkload(ctx, &pb.DeleteWorkloadRequest{Id: id})
	if err != nil || !delResp.Success {
		t.Fatalf("unexpected delete response: %+v err=%v", delResp, err)
	}

	if _, err := server.GetWorkloadStatus(ctx, &pb.GetWorkloadStatusRequest{Id: id}); err == nil {
		t.Fatal("expected error fetching deleted workload status")
	}
}

func TestAgentGRPCEndToEnd_WorkloadLifecycle_AllRuntimes(t *testing.T) {
	server := newTestServer(t)

	assertLifecycle(t, server, "e2e-container", pb.WorkloadType_WORKLOAD_TYPE_CONTAINER,
		&pb.WorkloadSpec{Spec: &pb.WorkloadSpec_Container{Container: &pb.ContainerSpec{Image: "nginx:latest"}}},
	)

	assertLifecycle(t, server, "e2e-compose", pb.WorkloadType_WORKLOAD_TYPE_COMPOSE,
		&pb.WorkloadSpec{Spec: &pb.WorkloadSpec_Compose{Compose: &pb.ComposeSpec{ProjectName: "e2e", ComposeYaml: "services: {}"}}},
	)

	assertLifecycle(t, server, "e2e-vm", pb.WorkloadType_WORKLOAD_TYPE_VM,
		&pb.WorkloadSpec{Spec: &pb.WorkloadSpec_Vm{Vm: &pb.VMSpec{Name: "vm-e2e", Vcpus: 1, MemoryMb: 512}}},
	)

	healthResp, err := server.HealthCheck(context.Background(), &pb.HealthCheckRequest{})
	if err != nil || !healthResp.Healthy {
		t.Fatalf("health check failed: resp=%+v err=%v", healthResp, err)
	}
	for _, rType := range []models.WorkloadType{models.WorkloadTypeContainer, models.WorkloadTypeCompose, models.WorkloadTypeVM} {
		if healthResp.RuntimeStatus[string(rType)] == "" {
			t.Fatalf("expected runtime status for %s", rType)
		}
	}
}
