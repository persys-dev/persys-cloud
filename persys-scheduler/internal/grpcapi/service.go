package grpcapi

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	controlv1 "github.com/persys-dev/persys-cloud/persys-scheduler/internal/controlv1"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/scheduler"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Service struct {
	controlv1.UnimplementedAgentControlServer
	sched *scheduler.Scheduler
}

func NewService(sched *scheduler.Scheduler) *Service {
	return &Service{sched: sched}
}

func annotateRPC(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return
	}
	span.SetAttributes(attrs...)
}

func recordRPCError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(otelcodes.Error, err.Error())
}

func (s *Service) RegisterNode(ctx context.Context, in *controlv1.RegisterNodeRequest) (*controlv1.RegisterNodeResponse, error) {
	if in != nil {
		annotateRPC(ctx, attribute.String("scheduler.node_id", strings.TrimSpace(in.GetNodeId())))
	}
	if in == nil || strings.TrimSpace(in.GetNodeId()) == "" {
		err := status.Error(codes.InvalidArgument, "node_id is required")
		recordRPCError(ctx, err)
		return nil, err
	}

	node := models.Node{
		NodeID:                 in.GetNodeId(),
		Status:                 "Ready",
		Labels:                 in.GetLabels(),
		Timestamp:              in.GetTimestamp().AsTime().UTC().Format(time.RFC3339),
		AvailableCPU:           float64(in.GetCapabilities().GetCpuTotalMillicores()) / 1000.0,
		TotalCPU:               float64(in.GetCapabilities().GetCpuTotalMillicores()) / 1000.0,
		AvailableMemory:        in.GetCapabilities().GetMemoryTotalMb(),
		TotalMemory:            in.GetCapabilities().GetMemoryTotalMb(),
		SupportedWorkloadTypes: normalizeSupportedWorkloadTypes(in.GetCapabilities().GetSupportedWorkloadTypes()),
	}

	if endpoint := strings.TrimSpace(in.GetGrpcEndpoint()); endpoint != "" {
		node.AgentEndpoint = endpoint
		host, port, err := splitHostPort(endpoint)
		if err != nil {
			rpcErr := status.Errorf(codes.InvalidArgument, "invalid grpc_endpoint %q: %v", endpoint, err)
			recordRPCError(ctx, rpcErr)
			return nil, rpcErr
		}
		node.IPAddress = host
		node.AgentGRPCPort = port
		node.AgentPort = port
	}
	if node.IPAddress == "" {
		err := status.Error(codes.InvalidArgument, "grpc_endpoint is required")
		recordRPCError(ctx, err)
		return nil, err
	}
	if node.AgentPort == 0 {
		node.AgentPort = 50051
		node.AgentGRPCPort = 50051
	}
	if node.Username == "" {
		node.Username = "agent"
	}
	if node.Hostname == "" {
		node.Hostname = in.GetNodeId()
	}
	if node.OSName == "" {
		node.OSName = "linux"
	}
	if node.KernelVersion == "" {
		node.KernelVersion = "unknown"
	}

	if err := s.sched.RegisterNode(node); err != nil {
		return &controlv1.RegisterNodeResponse{Accepted: false, Reason: err.Error()}, nil
	}

	now := time.Now().UTC()
	interval := int32(60)
	lease := now.Add(time.Duration(interval*3) * time.Second)
	return &controlv1.RegisterNodeResponse{
		Accepted:                 true,
		Reason:                   "registered",
		HeartbeatIntervalSeconds: interval,
		LeaseExpiresAt:           timestamppb.New(lease),
	}, nil
}

func (s *Service) Heartbeat(ctx context.Context, in *controlv1.HeartbeatRequest) (*controlv1.HeartbeatResponse, error) {
	if in != nil {
		annotateRPC(ctx, attribute.String("scheduler.node_id", strings.TrimSpace(in.GetNodeId())))
	}
	if in == nil || strings.TrimSpace(in.GetNodeId()) == "" {
		err := status.Error(codes.InvalidArgument, "node_id is required")
		recordRPCError(ctx, err)
		return nil, err
	}

	currentNode, _ := s.sched.GetNodeByID(in.GetNodeId())

	availableCPU := currentNode.AvailableCPU
	availableMemory := currentNode.AvailableMemory
	if in.GetUsage() != nil {
		totalMillicores := int64(currentNode.TotalCPU * 1000.0)
		if totalMillicores > 0 {
			availableCPU = float64(maxInt64(0, totalMillicores-in.GetUsage().GetCpuUsedMillicores())) / 1000.0
		}
		if currentNode.TotalMemory > 0 {
			availableMemory = maxInt64(0, currentNode.TotalMemory-in.GetUsage().GetMemoryUsedMb())
		}
	}

	if err := s.sched.UpdateNodeHeartbeat(in.GetNodeId(), "Ready", availableCPU, availableMemory); err != nil {
		rpcErr := status.Error(codes.Internal, err.Error())
		recordRPCError(ctx, rpcErr)
		return nil, rpcErr
	}

	for _, ws := range in.GetWorkloadStatuses() {
		if strings.TrimSpace(ws.GetWorkloadId()) == "" {
			continue
		}
		_ = s.sched.UpdateWorkloadStatus(ws.GetWorkloadId(), ws.GetState())
		if msg := strings.TrimSpace(ws.GetMessage()); msg != "" {
			_ = s.sched.UpdateWorkloadLogs(ws.GetWorkloadId(), msg)
		}
	}

	lease := time.Now().UTC().Add(3 * time.Minute)
	return &controlv1.HeartbeatResponse{Acknowledged: true, DrainNode: false, LeaseExpiresAt: timestamppb.New(lease)}, nil
}

func (s *Service) ApplyWorkload(ctx context.Context, in *controlv1.ApplyWorkloadRequest) (*controlv1.ApplyWorkloadResponse, error) {
	if in != nil {
		annotateRPC(ctx,
			attribute.String("scheduler.workload_id", strings.TrimSpace(in.GetWorkloadId())),
			attribute.String("scheduler.workload_desired_state", strings.TrimSpace(in.GetDesiredState())),
		)
	}
	if in == nil || strings.TrimSpace(in.GetWorkloadId()) == "" {
		err := status.Error(codes.InvalidArgument, "workload_id is required")
		recordRPCError(ctx, err)
		return nil, err
	}
	workload, err := controlApplyToModel(in)
	if err != nil {
		annotateRPC(ctx, attribute.String("scheduler.workload_type", strings.TrimSpace(in.GetSpec().GetType())))
		return &controlv1.ApplyWorkloadResponse{Success: false, FailureReason: controlv1.FailureReason_INVALID_SPEC, ErrorMessage: err.Error()}, nil
	}
	annotateRPC(ctx, attribute.String("scheduler.workload_type", strings.TrimSpace(workload.Type)))

	var persisted models.Workload
	if _, err := s.sched.GetWorkloadByID(workload.ID); err == nil {
		updated, err := s.sched.UpdateWorkloadSpec(workload.ID, workload)
		if err != nil {
			return &controlv1.ApplyWorkloadResponse{Success: false, FailureReason: controlv1.FailureReason_RUNTIME_ERROR, ErrorMessage: err.Error()}, nil
		}
		persisted = updated
	} else {
		created, err := s.sched.CreateWorkload(workload)
		if err != nil {
			return &controlv1.ApplyWorkloadResponse{Success: false, FailureReason: controlv1.FailureReason_RUNTIME_ERROR, ErrorMessage: err.Error()}, nil
		}
		persisted = created
	}

	// Trigger reconciliation immediately so ApplyWorkload performs scheduling now,
	// instead of waiting for the periodic reconciliation tick.
	if _, err := s.sched.ReconcileWorkloadWithContext(ctx, persisted); err != nil {
		return &controlv1.ApplyWorkloadResponse{Success: false, FailureReason: controlv1.FailureReason_RUNTIME_ERROR, ErrorMessage: err.Error()}, nil
	}
	return &controlv1.ApplyWorkloadResponse{Success: true}, nil
}

func (s *Service) DeleteWorkload(ctx context.Context, in *controlv1.DeleteWorkloadRequest) (*controlv1.DeleteWorkloadResponse, error) {
	if in != nil {
		annotateRPC(ctx, attribute.String("scheduler.workload_id", strings.TrimSpace(in.GetWorkloadId())))
	}
	if in == nil || strings.TrimSpace(in.GetWorkloadId()) == "" {
		err := status.Error(codes.InvalidArgument, "workload_id is required")
		recordRPCError(ctx, err)
		return nil, err
	}
	if err := s.sched.MarkWorkloadDeleted(in.GetWorkloadId()); err != nil {
		return &controlv1.DeleteWorkloadResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	return &controlv1.DeleteWorkloadResponse{Success: true}, nil
}

func (s *Service) RetryWorkload(ctx context.Context, in *controlv1.RetryWorkloadRequest) (*controlv1.RetryWorkloadResponse, error) {
	if in != nil {
		annotateRPC(ctx, attribute.String("scheduler.workload_id", strings.TrimSpace(in.GetWorkloadId())))
	}
	if in == nil || strings.TrimSpace(in.GetWorkloadId()) == "" {
		err := status.Error(codes.InvalidArgument, "workload_id is required")
		recordRPCError(ctx, err)
		return nil, err
	}
	if _, err := s.sched.TriggerWorkloadRetry(in.GetWorkloadId()); err != nil {
		return &controlv1.RetryWorkloadResponse{Accepted: false}, nil
	}
	return &controlv1.RetryWorkloadResponse{Accepted: true}, nil
}

func (s *Service) ListNodes(ctx context.Context, in *controlv1.ListNodesRequest) (*controlv1.ListNodesResponse, error) {
	if in != nil {
		annotateRPC(ctx, attribute.String("scheduler.filter_status", strings.TrimSpace(in.GetStatus())))
	}
	nodes, err := s.sched.GetNodes()
	if err != nil {
		rpcErr := status.Error(codes.Internal, err.Error())
		recordRPCError(ctx, rpcErr)
		return nil, rpcErr
	}

	filterStatus := strings.ToLower(strings.TrimSpace(in.GetStatus()))
	out := make([]*controlv1.NodeView, 0, len(nodes))
	for _, node := range nodes {
		if filterStatus != "" && strings.ToLower(strings.TrimSpace(node.Status)) != filterStatus {
			continue
		}
		out = append(out, nodeToView(node))
	}
	return &controlv1.ListNodesResponse{Nodes: out}, nil
}

func (s *Service) GetNode(ctx context.Context, in *controlv1.GetNodeRequest) (*controlv1.GetNodeResponse, error) {
	if in != nil {
		annotateRPC(ctx, attribute.String("scheduler.node_id", strings.TrimSpace(in.GetNodeId())))
	}
	if in == nil || strings.TrimSpace(in.GetNodeId()) == "" {
		err := status.Error(codes.InvalidArgument, "node_id is required")
		recordRPCError(ctx, err)
		return nil, err
	}
	node, err := s.sched.GetNodeByID(in.GetNodeId())
	if err != nil {
		rpcErr := status.Errorf(codes.NotFound, "node %q not found", in.GetNodeId())
		recordRPCError(ctx, rpcErr)
		return nil, rpcErr
	}
	return &controlv1.GetNodeResponse{Node: nodeToView(node)}, nil
}

func (s *Service) ListWorkloads(ctx context.Context, in *controlv1.ListWorkloadsRequest) (*controlv1.ListWorkloadsResponse, error) {
	if in != nil {
		annotateRPC(ctx,
			attribute.String("scheduler.filter_node_id", strings.TrimSpace(in.GetNodeId())),
			attribute.String("scheduler.filter_status", strings.TrimSpace(in.GetStatus())),
		)
	}
	workloads, err := s.sched.GetWorkloads()
	if err != nil {
		rpcErr := status.Error(codes.Internal, err.Error())
		recordRPCError(ctx, rpcErr)
		return nil, rpcErr
	}

	filterNodeID := strings.TrimSpace(in.GetNodeId())
	filterStatus := strings.ToLower(strings.TrimSpace(in.GetStatus()))
	out := make([]*controlv1.WorkloadView, 0, len(workloads))
	for _, workload := range workloads {
		if filterNodeID != "" && assignedNodeID(workload) != filterNodeID {
			continue
		}
		if filterStatus != "" && strings.ToLower(strings.TrimSpace(workload.Status)) != filterStatus {
			continue
		}
		out = append(out, workloadToView(workload))
	}
	return &controlv1.ListWorkloadsResponse{Workloads: out}, nil
}

func (s *Service) GetWorkload(ctx context.Context, in *controlv1.GetWorkloadRequest) (*controlv1.GetWorkloadResponse, error) {
	if in != nil {
		annotateRPC(ctx, attribute.String("scheduler.workload_id", strings.TrimSpace(in.GetWorkloadId())))
	}
	if in == nil || strings.TrimSpace(in.GetWorkloadId()) == "" {
		err := status.Error(codes.InvalidArgument, "workload_id is required")
		recordRPCError(ctx, err)
		return nil, err
	}
	workload, err := s.sched.GetWorkloadByID(in.GetWorkloadId())
	if err != nil {
		rpcErr := status.Errorf(codes.NotFound, "workload %q not found", in.GetWorkloadId())
		recordRPCError(ctx, rpcErr)
		return nil, rpcErr
	}
	return &controlv1.GetWorkloadResponse{Workload: workloadToView(workload)}, nil
}

func (s *Service) GetClusterSummary(ctx context.Context, _ *controlv1.GetClusterSummaryRequest) (*controlv1.GetClusterSummaryResponse, error) {
	nodes, err := s.sched.GetNodes()
	if err != nil {
		rpcErr := status.Error(codes.Internal, err.Error())
		recordRPCError(ctx, rpcErr)
		return nil, rpcErr
	}
	workloads, err := s.sched.GetWorkloads()
	if err != nil {
		rpcErr := status.Error(codes.Internal, err.Error())
		recordRPCError(ctx, rpcErr)
		return nil, rpcErr
	}

	resp := &controlv1.GetClusterSummaryResponse{
		TotalNodes:     int32(len(nodes)),
		TotalWorkloads: int32(len(workloads)),
		GeneratedAt:    timestamppb.Now(),
	}

	for _, node := range nodes {
		switch strings.ToLower(strings.TrimSpace(node.Status)) {
		case "ready":
			resp.ReadyNodes++
		default:
			resp.NotReadyNodes++
		}
	}

	for _, workload := range workloads {
		statusValue := strings.ToLower(strings.TrimSpace(workload.Status))
		desiredState := strings.ToLower(strings.TrimSpace(workload.DesiredState))
		switch statusValue {
		case "running":
			resp.RunningWorkloads++
		case "failed":
			resp.FailedWorkloads++
		case "pending", "unknown", "updating":
			resp.PendingWorkloads++
		}
		if desiredState == "deleted" || statusValue == "deleted" {
			resp.DeletedWorkloads++
		}
	}

	return resp, nil
}

func (s *Service) ControlStream(stream controlv1.AgentControl_ControlStreamServer) error {
	err := status.Error(codes.Unimplemented, "ControlStream is not implemented yet")
	recordRPCError(stream.Context(), err)
	return err
}

func nodeToView(node models.Node) *controlv1.NodeView {
	return &controlv1.NodeView{
		NodeId:                 node.NodeID,
		Status:                 node.Status,
		StatusReason:           node.StatusReason,
		StatusUpdatedBy:        node.StatusUpdatedBy,
		StatusUpdatedAt:        timestampPtr(node.StatusUpdatedAt),
		LastHeartbeat:          timestampPtr(node.LastHeartbeat),
		GrpcEndpoint:           node.AgentEndpoint,
		TotalCpuCores:          node.TotalCPU,
		AvailableCpuCores:      node.AvailableCPU,
		TotalMemoryMb:          node.TotalMemory,
		AvailableMemoryMb:      node.AvailableMemory,
		SupportedWorkloadTypes: append([]string(nil), node.SupportedWorkloadTypes...),
		Labels:                 copyStringMap(node.Labels),
	}
}

func workloadToView(workload models.Workload) *controlv1.WorkloadView {
	return &controlv1.WorkloadView{
		WorkloadId:       workload.ID,
		Type:             workload.Type,
		DesiredState:     workload.DesiredState,
		Status:           workload.Status,
		AssignedNodeId:   assignedNodeID(workload),
		RevisionId:       workload.RevisionID,
		RetryAttempts:    int32(workload.Retry.Attempts),
		RetryMaxAttempts: int32(workload.Retry.MaxAttempts),
		RetryNextAt:      timestampPtr(workload.Retry.NextRetryAt),
		FailureReason:    workload.StatusInfo.FailureReason,
		LastUpdated:      workloadLastUpdated(workload),
	}
}

func assignedNodeID(workload models.Workload) string {
	if strings.TrimSpace(workload.NodeID) != "" {
		return workload.NodeID
	}
	return workload.AssignedNode
}

func workloadLastUpdated(workload models.Workload) *timestamppb.Timestamp {
	if !workload.StatusInfo.LastUpdated.IsZero() {
		return timestamppb.New(workload.StatusInfo.LastUpdated.UTC())
	}
	if !workload.CreatedAt.IsZero() {
		return timestamppb.New(workload.CreatedAt.UTC())
	}
	return nil
}

func timestampPtr(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t.UTC())
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func splitHostPort(endpoint string) (string, int, error) {
	hostPort := strings.TrimSpace(endpoint)
	parts := strings.Split(hostPort, ":")
	if len(parts) < 2 {
		return "", 0, fmt.Errorf("invalid grpc endpoint %q", endpoint)
	}
	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return "", 0, err
	}
	host := strings.Join(parts[:len(parts)-1], ":")
	host = strings.Trim(host, "[]")
	if host == "" {
		host = "127.0.0.1"
	}
	return host, port, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func controlApplyToModel(in *controlv1.ApplyWorkloadRequest) (models.Workload, error) {
	if in.GetSpec() == nil {
		return models.Workload{}, fmt.Errorf("spec is required")
	}

	w := models.Workload{
		ID:           in.GetWorkloadId(),
		Type:         strings.ToLower(strings.TrimSpace(in.GetSpec().GetType())),
		RevisionID:   in.GetRevisionId(),
		DesiredState: normalizeDesiredStateString(in.GetDesiredState()),
		Metadata:     map[string]interface{}{},
		EnvVars:      map[string]string{},
		Labels:       map[string]string{},
	}
	if w.DesiredState == "" {
		w.DesiredState = "Running"
	}

	if r := in.GetSpec().GetResources(); r != nil {
		w.Resources = models.Resources{
			CPUUsage:    float64(r.GetCpuMillicores()) / 1000.0,
			MemoryUsage: float64(r.GetMemoryMb()),
			DiskUsage:   int(r.GetDiskGb()),
		}
	}

	for k, v := range in.GetSpec().GetMetadata() {
		w.Metadata[k] = v
	}

	switch strings.ToLower(in.GetSpec().GetType()) {
	case "container":
		cs := in.GetSpec().GetContainer()
		if cs == nil {
			return models.Workload{}, fmt.Errorf("container spec required")
		}
		w.Image = cs.GetImage()
		w.Command = strings.Join(cs.GetCommand(), " ")
		w.EnvVars = cs.GetEnv()
		w.RestartPolicy = cs.GetRestartPolicy()
		for _, p := range cs.GetPorts() {
			proto := p.GetProtocol()
			if proto == "" {
				proto = "tcp"
			}
			w.Ports = append(w.Ports, fmt.Sprintf("%d:%d/%s", p.GetHostPort(), p.GetContainerPort(), proto))
		}
		for _, v := range cs.GetVolumes() {
			vol := fmt.Sprintf("%s:%s", v.GetHostPath(), v.GetContainerPath())
			if v.GetReadOnly() {
				vol += ":ro"
			}
			w.Volumes = append(w.Volumes, vol)
		}
	case "compose":
		cp := in.GetSpec().GetCompose()
		if cp == nil {
			return models.Workload{}, fmt.Errorf("compose spec required")
		}
		w.Type = "compose"
		w.EnvVars = cp.GetEnv()
		if strings.EqualFold(cp.GetSourceType(), "git") {
			w.GitRepo = cp.GetGitRepo()
			w.GitBranch = cp.GetGitRef()
		} else {
			w.ComposeYAML = cp.GetInlineYaml()
		}
	case "vm":
		vm := in.GetSpec().GetVm()
		if vm == nil {
			return models.Workload{}, fmt.Errorf("vm spec required")
		}
		w.VM = &models.VMSpec{
			VCPUs:     vm.GetVcpus(),
			MemoryMB:  vm.GetMemoryMb(),
			CloudInit: "",
		}
		if ci := vm.GetCloudInit(); ci != nil {
			w.VM.CloudInitConfig = &models.CloudInitConfig{
				UserData:      ci.GetUserData(),
				MetaData:      ci.GetMetaData(),
				NetworkConfig: ci.GetNetworkConfig(),
			}
		}
		for _, d := range vm.GetDisks() {
			w.VM.Disks = append(w.VM.Disks, models.VMDiskConfig{Type: d.GetPoolName(), SizeGB: d.GetSizeGb(), Device: d.GetMountPoint()})
		}
		for _, n := range vm.GetNetworks() {
			w.VM.Networks = append(w.VM.Networks, models.VMNetworkConfig{Network: n.GetBridge(), IPAddress: n.GetStaticIp()})
		}
	default:
		return models.Workload{}, fmt.Errorf("unsupported workload type %q", in.GetSpec().GetType())
	}

	return w, nil
}

func normalizeDesiredStateString(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "stopped", "stop":
		return "Stopped"
	case "deleted", "delete":
		return "Deleted"
	default:
		return "Running"
	}
}

func normalizeSupportedWorkloadTypes(types []string) []string {
	if len(types) == 0 {
		return nil
	}
	out := make([]string, 0, len(types))
	seen := make(map[string]struct{}, len(types))
	for _, t := range types {
		canon := strings.ToLower(strings.TrimSpace(t))
		switch canon {
		case "docker-container":
			canon = "container"
		case "docker-compose":
			canon = "compose"
		}
		if canon == "" {
			continue
		}
		if _, ok := seen[canon]; ok {
			continue
		}
		seen[canon] = struct{}{}
		out = append(out, canon)
	}
	return out
}
