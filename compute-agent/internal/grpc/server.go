package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/config"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/metrics"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/resources"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/runtime"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/task"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/workload"
	pb "github.com/persys-dev/persys-cloud/compute-agent/pkg/api/v1"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// Server implements the gRPC AgentService
type Server struct {
	pb.UnimplementedAgentServiceServer

	config          *config.Config
	manager         *workload.Manager
	runtimeMgr      *runtime.Manager
	logger          *logrus.Entry
	grpcServer      *grpc.Server
	resourceMonitor *resources.Monitor
	metricsInst     *metrics.Metrics
	taskQueue       *task.Queue
}

// NewServer creates a new gRPC server
func NewServer(cfg *config.Config, manager *workload.Manager, runtimeMgr *runtime.Manager, logger *logrus.Logger) (*Server, error) {
	s := &Server{
		config:     cfg,
		manager:    manager,
		runtimeMgr: runtimeMgr,
		logger:     logger.WithField("component", "grpc-server"),
	}

	var opts []grpc.ServerOption
	opts = append(opts, grpc.UnaryInterceptor(otelUnaryServerInterceptor("compute-agent")))

	// Configure mTLS if enabled
	if cfg.TLSEnabled {
		tlsConfig, err := s.loadTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS config: %w", err)
		}

		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.Creds(creds))
		s.logger.Info("mTLS authentication enabled")
	} else {
		s.logger.Warn("Running without TLS - not recommended for production")
	}

	s.grpcServer = grpc.NewServer(opts...)
	pb.RegisterAgentServiceServer(s.grpcServer, s)

	return s, nil
}

// SetResourceMonitor sets the resource monitor
func (s *Server) SetResourceMonitor(monitor *resources.Monitor) {
	s.resourceMonitor = monitor
}

// SetMetrics sets the metrics instance
func (s *Server) SetMetrics(m *metrics.Metrics) {
	s.metricsInst = m
}

// SetTaskQueue sets the task queue
func (s *Server) SetTaskQueue(q *task.Queue) {
	s.taskQueue = q
}

// Start starts the gRPC server
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.GRPCAddr, s.config.GRPCPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.logger.Infof("Starting gRPC server on %s", addr)
	return s.grpcServer.Serve(listener)
}

func otelUnaryServerInterceptor(service string) grpc.UnaryServerInterceptor {
	tr := otel.Tracer(service)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if md != nil {
			ctx = otel.GetTextMapPropagator().Extract(ctx, metadataCarrier(md))
		}
		ctx, span := tr.Start(ctx, info.FullMethod, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()

		resp, err := handler(ctx, req)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, err.Error())
		}
		return resp, err
	}
}

type metadataCarrier metadata.MD

func (m metadataCarrier) Get(key string) string {
	values := metadata.MD(m).Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (m metadataCarrier) Set(key string, value string) {
	md := metadata.MD(m)
	md.Set(strings.ToLower(key), value)
}

func (m metadataCarrier) Keys() []string {
	md := metadata.MD(m)
	keys := make([]string, 0, len(md))
	for k := range md {
		keys = append(keys, k)
	}
	return keys
}

// Stop gracefully stops the gRPC server
func (s *Server) Stop() {
	s.logger.Info("Stopping gRPC server")
	s.grpcServer.GracefulStop()
}

// loadTLSConfig loads TLS certificates for mTLS
func (s *Server) loadTLSConfig() (*tls.Config, error) {
	provider := &dynamicTLSProvider{
		certPath: s.config.TLSCertPath,
		keyPath:  s.config.TLSKeyPath,
		caPath:   s.config.TLSCAPath,
	}
	// Prime initial load so startup fails fast on invalid/missing files.
	if _, err := provider.getConfig(); err != nil {
		return nil, err
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return provider.getConfig()
		},
	}, nil
}

type dynamicTLSProvider struct {
	certPath string
	keyPath  string
	caPath   string

	mu          sync.RWMutex
	cached      *tls.Config
	certModTime time.Time
	keyModTime  time.Time
	caModTime   time.Time
}

func (d *dynamicTLSProvider) getConfig() (*tls.Config, error) {
	certInfo, err := os.Stat(d.certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat server certificate: %w", err)
	}
	keyInfo, err := os.Stat(d.keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat server key: %w", err)
	}
	caInfo, err := os.Stat(d.caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat CA certificate: %w", err)
	}

	d.mu.RLock()
	cached := d.cached
	certUnchanged := d.certModTime.Equal(certInfo.ModTime())
	keyUnchanged := d.keyModTime.Equal(keyInfo.ModTime())
	caUnchanged := d.caModTime.Equal(caInfo.ModTime())
	d.mu.RUnlock()

	if cached != nil && certUnchanged && keyUnchanged && caUnchanged {
		return cached, nil
	}

	keyPair, err := tls.LoadX509KeyPair(d.certPath, d.keyPath)
	if err != nil {
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}
	caCert, err := os.ReadFile(d.caPath)
	if err != nil {
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	updated := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{keyPair},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}

	d.mu.Lock()
	d.cached = updated
	d.certModTime = certInfo.ModTime()
	d.keyModTime = keyInfo.ModTime()
	d.caModTime = caInfo.ModTime()
	d.mu.Unlock()

	return updated, nil
}

// ApplyWorkload handles workload create/update requests
func (s *Server) ApplyWorkload(ctx context.Context, req *pb.ApplyWorkloadRequest) (*pb.ApplyWorkloadResponse, error) {
	s.logClientInfo(ctx, "ApplyWorkload", req.Id)

	// Convert proto to internal model
	workload, err := s.protoToWorkload(req)
	if err != nil {
		return &pb.ApplyWorkloadResponse{
			Applied: false,
			Message: fmt.Sprintf("invalid request: %v", err),
		}, nil
	}

	// If task queue is available, submit async task
	if s.taskQueue != nil {
		taskID := fmt.Sprintf("apply-%s-%d", req.Id, time.Now().UnixNano())
		t := &task.Task{
			ID:         taskID,
			WorkloadID: req.Id,
			Type:       task.TaskTypeApplyWorkload,
		}

		// Store workload in task for handler to access
		t.Result = workload

		err := s.taskQueue.Submit(t)
		if err != nil {
			s.logger.Warnf("Failed to submit async task: %v, falling back to sync", err)
			// Fall back to sync execution
		} else {
			return &pb.ApplyWorkloadResponse{
				Applied: true,
				Skipped: false,
				Message: "workload apply submitted for processing",
				Status: &pb.WorkloadStatus{
					Id:          req.Id,
					ActualState: pb.ActualState_ACTUAL_STATE_PENDING,
					Message:     "task pending execution",
					UpdatedAt:   time.Now().Unix(),
					Metadata: map[string]string{
						"task_id":     taskID,
						"task_status": string(task.TaskStatusPending),
					},
				},
			}, nil
		}
	}

	// Fallback: Synchronous execution (for clients that need immediate response)
	// This is blocking but ensures immediate feedback for critical operations
	status, skipped, err := s.manager.ApplyWorkload(ctx, workload)
	if err != nil {
		s.logger.Errorf("ApplyWorkload failed for %s: %v", req.Id, err)

		return &pb.ApplyWorkloadResponse{
			Applied: false,
			Skipped: false,
			Message: fmt.Sprintf("failed to apply workload: %v", err),
		}, nil
	}

	return &pb.ApplyWorkloadResponse{
		Applied: true,
		Skipped: skipped,
		Message: "workload applied successfully",
		Status:  s.statusToProto(status),
	}, nil
}

// DeleteWorkload handles workload deletion
func (s *Server) DeleteWorkload(ctx context.Context, req *pb.DeleteWorkloadRequest) (*pb.DeleteWorkloadResponse, error) {
	s.logClientInfo(ctx, "DeleteWorkload", req.Id)

	// If task queue is available, submit async task
	if s.taskQueue != nil {
		taskID := fmt.Sprintf("delete-%s-%d", req.Id, time.Now().UnixNano())
		t := &task.Task{
			ID:         taskID,
			WorkloadID: req.Id,
			Type:       task.TaskTypeDeleteWorkload,
			Result:     req.Id,
		}

		err := s.taskQueue.Submit(t)
		if err != nil {
			s.logger.Warnf("Failed to submit async delete task: %v, falling back to sync", err)
			// Fall back to sync execution
		} else {
			return &pb.DeleteWorkloadResponse{
				Success: true,
				Message: "workload deletion submitted for processing",
			}, nil
		}
	}

	// Fallback: Synchronous execution
	if err := s.manager.DeleteWorkload(ctx, req.Id); err != nil {
		return &pb.DeleteWorkloadResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	return &pb.DeleteWorkloadResponse{
		Success: true,
		Message: "workload deleted successfully",
	}, nil
}

// GetWorkloadStatus retrieves workload status
func (s *Server) GetWorkloadStatus(ctx context.Context, req *pb.GetWorkloadStatusRequest) (*pb.GetWorkloadStatusResponse, error) {
	s.logClientInfo(ctx, "GetWorkloadStatus", req.Id)

	status, err := s.manager.GetStatus(ctx, req.Id)
	pending := s.pendingStatusFromTasks(req.Id)
	if err != nil {
		if pending != nil {
			return &pb.GetWorkloadStatusResponse{Status: pending}, nil
		}
		return nil, err
	}

	respStatus := s.statusToProto(status)
	// Surface in-flight apply/delete task state even when a persisted status exists.
	// Without this, callers can see stale "stopped/running" and repeatedly enqueue apply.
	if pending != nil {
		taskState := strings.ToLower(strings.TrimSpace(pending.GetMetadata()["task_status"]))
		if taskState == string(task.TaskStatusPending) || taskState == string(task.TaskStatusRunning) {
			if respStatus.Metadata == nil {
				respStatus.Metadata = map[string]string{}
			}
			for k, v := range pending.GetMetadata() {
				respStatus.Metadata[k] = v
			}
			respStatus.ActualState = pb.ActualState_ACTUAL_STATE_PENDING
			respStatus.Message = pending.GetMessage()
			respStatus.UpdatedAt = time.Now().Unix()
		}
	}

	return &pb.GetWorkloadStatusResponse{
		Status: respStatus,
	}, nil
}

func (s *Server) pendingStatusFromTasks(workloadID string) *pb.WorkloadStatus {
	if s.taskQueue == nil || strings.TrimSpace(workloadID) == "" {
		return nil
	}

	snapshots := s.taskQueue.ListTaskSnapshots("")
	var latest *task.TaskSnapshot
	for i := range snapshots {
		snap := snapshots[i]
		if snap.WorkloadID != workloadID {
			continue
		}
		if latest == nil || snap.CreatedAt.After(latest.CreatedAt) {
			cp := snap
			latest = &cp
		}
	}
	if latest == nil {
		return nil
	}

	switch latest.Status {
	case task.TaskStatusPending:
		return &pb.WorkloadStatus{
			Id:          workloadID,
			ActualState: pb.ActualState_ACTUAL_STATE_PENDING,
			Message:     "task pending execution",
			UpdatedAt:   time.Now().Unix(),
			Metadata: map[string]string{
				"task_id":     latest.ID,
				"task_status": string(latest.Status),
			},
		}
	case task.TaskStatusRunning:
		return &pb.WorkloadStatus{
			Id:          workloadID,
			ActualState: pb.ActualState_ACTUAL_STATE_PENDING,
			Message:     "task running",
			UpdatedAt:   time.Now().Unix(),
			Metadata: map[string]string{
				"task_id":     latest.ID,
				"task_status": string(latest.Status),
			},
		}
	case task.TaskStatusFailed:
		return &pb.WorkloadStatus{
			Id:          workloadID,
			ActualState: pb.ActualState_ACTUAL_STATE_FAILED,
			Message:     strings.TrimSpace(latest.Error),
			UpdatedAt:   time.Now().Unix(),
			Metadata: map[string]string{
				"task_id":     latest.ID,
				"task_status": string(latest.Status),
			},
		}
	default:
		return nil
	}
}

// ListWorkloads lists all workloads
func (s *Server) ListWorkloads(ctx context.Context, req *pb.ListWorkloadsRequest) (*pb.ListWorkloadsResponse, error) {
	s.logClientInfo(ctx, "ListWorkloads", "")

	var workloadType *models.WorkloadType
	if req.Type != pb.WorkloadType_WORKLOAD_TYPE_UNSPECIFIED {
		t := s.protoToWorkloadType(req.Type)
		workloadType = &t
	}

	statuses, err := s.manager.ListWorkloads(ctx, workloadType)
	if err != nil {
		return nil, err
	}

	var protoStatuses []*pb.WorkloadStatus
	for _, status := range statuses {
		protoStatuses = append(protoStatuses, s.statusToProto(status))
	}

	return &pb.ListWorkloadsResponse{
		Workloads: protoStatuses,
	}, nil
}

// HealthCheck returns agent health status
func (s *Server) HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	runtimeStatus := s.runtimeMgr.HealthCheck(ctx)

	// Get resource utilization
	var memPercent, cpuPercent, diskPercent float64
	if s.resourceMonitor != nil {
		util, err := s.resourceMonitor.GetUtilization([]string{"/"})
		if err == nil {
			memPercent = util.MemoryPercent
			cpuPercent = util.CPUPercent
			if diskPct, ok := util.DiskPercent["/"]; ok {
				diskPercent = diskPct
			}

			// Update metrics
			if s.metricsInst != nil {
				diskMap := make(map[string]float64)
				for mount, pct := range util.DiskPercent {
					diskMap[mount] = pct
				}
				s.metricsInst.UpdateSystemMetrics(memPercent, cpuPercent, diskMap)
			}
		}
	}

	return &pb.HealthCheckResponse{
		Healthy:           true,
		Version:           s.config.Version,
		RuntimeStatus:     runtimeStatus,
		MemoryUtilization: memPercent,
		CpuUtilization:    cpuPercent,
		DiskUtilization:   diskPercent,
	}, nil
}

// ListActions returns task/action history tracked since agent startup.
func (s *Server) ListActions(ctx context.Context, req *pb.ListActionsRequest) (*pb.ListActionsResponse, error) {
	s.logClientInfo(ctx, "ListActions", req.GetWorkloadId())

	if s.taskQueue == nil {
		return &pb.ListActionsResponse{Actions: []*pb.AgentAction{}}, nil
	}

	snapshots := s.taskQueue.ListTaskSnapshots("")

	workloadFilter := strings.TrimSpace(req.GetWorkloadId())
	typeFilter := strings.TrimSpace(req.GetActionType())
	statusFilter := strings.TrimSpace(req.GetStatus())
	newestFirst := true
	if !req.GetNewestFirst() {
		newestFirst = false
	}

	filtered := make([]task.TaskSnapshot, 0, len(snapshots))
	for _, item := range snapshots {
		if workloadFilter != "" && item.WorkloadID != workloadFilter {
			continue
		}
		if typeFilter != "" && string(item.Type) != typeFilter {
			continue
		}
		if statusFilter != "" && string(item.Status) != statusFilter {
			continue
		}
		filtered = append(filtered, item)
	}

	sort.Slice(filtered, func(i, j int) bool {
		ti := filtered[i].CreatedAt
		tj := filtered[j].CreatedAt
		if newestFirst {
			return ti.After(tj)
		}
		return ti.Before(tj)
	})

	if req.GetLimit() > 0 && int(req.GetLimit()) < len(filtered) {
		filtered = filtered[:req.GetLimit()]
	}

	actions := make([]*pb.AgentAction, 0, len(filtered))
	for _, item := range filtered {
		actions = append(actions, &pb.AgentAction{
			TaskId:     item.ID,
			WorkloadId: item.WorkloadID,
			ActionType: string(item.Type),
			Status:     string(item.Status),
			Error:      item.Error,
			CreatedAt:  item.CreatedAt.Unix(),
			StartedAt:  item.StartedAt.Unix(),
			EndedAt:    item.EndedAt.Unix(),
		})
	}

	return &pb.ListActionsResponse{Actions: actions}, nil
}

// Helper conversion functions

func (s *Server) protoToWorkload(req *pb.ApplyWorkloadRequest) (*models.Workload, error) {
	workloadType := s.protoToWorkloadType(req.Type)
	desiredState := s.protoToDesiredState(req.DesiredState)

	// Convert spec to map based on the spec type
	var specMap map[string]interface{}

	if req.Spec.GetContainer() != nil {
		container := req.Spec.GetContainer()
		specData, err := json.Marshal(container)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal container spec: %w", err)
		}
		if err := json.Unmarshal(specData, &specMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal container spec: %w", err)
		}
	} else if req.Spec.GetCompose() != nil {
		compose := req.Spec.GetCompose()
		specData, err := json.Marshal(compose)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal compose spec: %w", err)
		}
		if err := json.Unmarshal(specData, &specMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal compose spec: %w", err)
		}
	} else if req.Spec.GetVm() != nil {
		vm := req.Spec.GetVm()
		specData, err := json.Marshal(vm)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal vm spec: %w", err)
		}
		if err := json.Unmarshal(specData, &specMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal vm spec: %w", err)
		}
	} else {
		return nil, fmt.Errorf("spec must contain container, compose, or vm specification")
	}

	return &models.Workload{
		ID:           req.Id,
		Type:         workloadType,
		RevisionID:   req.RevisionId,
		DesiredState: desiredState,
		Spec:         specMap,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}, nil
}

func (s *Server) protoToWorkloadType(t pb.WorkloadType) models.WorkloadType {
	switch t {
	case pb.WorkloadType_WORKLOAD_TYPE_CONTAINER:
		return models.WorkloadTypeContainer
	case pb.WorkloadType_WORKLOAD_TYPE_COMPOSE:
		return models.WorkloadTypeCompose
	case pb.WorkloadType_WORKLOAD_TYPE_VM:
		return models.WorkloadTypeVM
	default:
		return models.WorkloadTypeContainer
	}
}

func (s *Server) protoToDesiredState(state pb.DesiredState) models.DesiredState {
	switch state {
	case pb.DesiredState_DESIRED_STATE_RUNNING:
		return models.DesiredStateRunning
	case pb.DesiredState_DESIRED_STATE_STOPPED:
		return models.DesiredStateStopped
	default:
		return models.DesiredStateRunning
	}
}

func (s *Server) statusToProto(status *models.WorkloadStatus) *pb.WorkloadStatus {
	return &pb.WorkloadStatus{
		Id:           status.ID,
		Type:         s.workloadTypeToProto(status.Type),
		RevisionId:   status.RevisionID,
		DesiredState: s.desiredStateToProto(status.DesiredState),
		ActualState:  s.actualStateToProto(status.ActualState),
		Message:      status.Message,
		CreatedAt:    status.CreatedAt.Unix(),
		UpdatedAt:    status.UpdatedAt.Unix(),
		Metadata:     status.Metadata,
		Usage:        statusUsageToProto(status),
	}
}

func statusUsageToProto(status *models.WorkloadStatus) *pb.WorkloadUsageSnapshot {
	if status == nil || status.Usage == nil {
		return nil
	}

	collectedAt := status.Usage.CollectedAt.Unix()
	if collectedAt < 0 {
		collectedAt = 0
	}

	return &pb.WorkloadUsageSnapshot{
		WorkloadId:     status.Usage.WorkloadID,
		Type:           sWorkloadType(status.Usage.Type),
		CpuPercent:     status.Usage.CPUPercent,
		MemoryBytes:    status.Usage.MemoryBytes,
		DiskReadBytes:  status.Usage.DiskReadBytes,
		DiskWriteBytes: status.Usage.DiskWriteBytes,
		NetRxBytes:     status.Usage.NetRXBytes,
		NetTxBytes:     status.Usage.NetTXBytes,
		CollectedAt:    collectedAt,
		Source:         status.Usage.Source,
	}
}

func sWorkloadType(t string) pb.WorkloadType {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "container":
		return pb.WorkloadType_WORKLOAD_TYPE_CONTAINER
	case "compose":
		return pb.WorkloadType_WORKLOAD_TYPE_COMPOSE
	case "vm":
		return pb.WorkloadType_WORKLOAD_TYPE_VM
	default:
		return pb.WorkloadType_WORKLOAD_TYPE_UNSPECIFIED
	}
}

func (s *Server) workloadTypeToProto(t models.WorkloadType) pb.WorkloadType {
	switch t {
	case models.WorkloadTypeContainer:
		return pb.WorkloadType_WORKLOAD_TYPE_CONTAINER
	case models.WorkloadTypeCompose:
		return pb.WorkloadType_WORKLOAD_TYPE_COMPOSE
	case models.WorkloadTypeVM:
		return pb.WorkloadType_WORKLOAD_TYPE_VM
	default:
		return pb.WorkloadType_WORKLOAD_TYPE_UNSPECIFIED
	}
}

func (s *Server) desiredStateToProto(state models.DesiredState) pb.DesiredState {
	switch state {
	case models.DesiredStateRunning:
		return pb.DesiredState_DESIRED_STATE_RUNNING
	case models.DesiredStateStopped:
		return pb.DesiredState_DESIRED_STATE_STOPPED
	default:
		return pb.DesiredState_DESIRED_STATE_UNSPECIFIED
	}
}

func (s *Server) actualStateToProto(state models.ActualState) pb.ActualState {
	switch state {
	case models.ActualStatePending:
		return pb.ActualState_ACTUAL_STATE_PENDING
	case models.ActualStateRunning:
		return pb.ActualState_ACTUAL_STATE_RUNNING
	case models.ActualStateStopped:
		return pb.ActualState_ACTUAL_STATE_STOPPED
	case models.ActualStateFailed:
		return pb.ActualState_ACTUAL_STATE_FAILED
	case models.ActualStateUnknown:
		return pb.ActualState_ACTUAL_STATE_UNKNOWN
	default:
		return pb.ActualState_ACTUAL_STATE_UNSPECIFIED
	}
}

func (s *Server) logClientInfo(ctx context.Context, method, id string) {
	peerInfo, ok := peer.FromContext(ctx)
	if ok {
		if id != "" {
			s.logger.Infof("%s called by %s for workload %s", method, peerInfo.Addr, id)
		} else {
			s.logger.Infof("%s called by %s", method, peerInfo.Addr)
		}
	}
}
