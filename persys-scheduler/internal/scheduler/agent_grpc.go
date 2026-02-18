package scheduler

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	agentpb "github.com/persys-dev/persys-cloud/persys-scheduler/internal/agentpb"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	metricspkg "github.com/persys-dev/persys-cloud/persys-scheduler/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	defaultPollInterval   = time.Second
	defaultApplyTimeout   = 45 * time.Second
	defaultVMApplyTimeout = 240 * time.Second
	defaultDeleteTimeout  = 60 * time.Second
	defaultRPCTimeout     = 10 * time.Second
)

var agentLogger = logging.C("scheduler.agent_grpc")

func schedulerDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err == nil {
		return d
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return time.Duration(n) * time.Second
	}
	agentLogger.WithFields(logrus.Fields{
		"key":      key,
		"value":    raw,
		"fallback": fallback.String(),
	}).Warn("invalid duration env value; using fallback")
	return fallback
}

func (s *Scheduler) agentPollInterval() time.Duration {
	return schedulerDurationEnv("SCHEDULER_AGENT_STATUS_POLL_INTERVAL", defaultPollInterval)
}

func (s *Scheduler) applyTimeoutFor(workload models.Workload) time.Duration {
	if strings.EqualFold(workload.Type, "vm") {
		return schedulerDurationEnv("SCHEDULER_AGENT_VM_APPLY_TIMEOUT", defaultVMApplyTimeout)
	}
	return schedulerDurationEnv("SCHEDULER_AGENT_APPLY_TIMEOUT", defaultApplyTimeout)
}

func (s *Scheduler) deleteTimeout() time.Duration {
	return schedulerDurationEnv("SCHEDULER_AGENT_DELETE_TIMEOUT", defaultDeleteTimeout)
}

func (s *Scheduler) rpcTimeout() time.Duration {
	return schedulerDurationEnv("SCHEDULER_AGENT_RPC_TIMEOUT", defaultRPCTimeout)
}

func (s *Scheduler) grpcAddressForNode(node models.Node) string {
	if endpoint := strings.TrimSpace(node.AgentEndpoint); endpoint != "" {
		return endpoint
	}
	port := node.AgentPort
	if node.AgentGRPCPort > 0 {
		port = node.AgentGRPCPort
	}
	return fmt.Sprintf("%s:%d", node.IPAddress, port)
}

func loadClientTLSConfigFromEnv() (*tls.Config, error) {
	caPath := strings.TrimSpace(os.Getenv("PERSYS_TLS_CA"))
	certPath := strings.TrimSpace(os.Getenv("PERSYS_TLS_CERT"))
	keyPath := strings.TrimSpace(os.Getenv("PERSYS_TLS_KEY"))

	if caPath == "" {
		return nil, fmt.Errorf("PERSYS_TLS_CA must be set when PERSYS_TLS_ENABLED=true")
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA file %s: %w", caPath, err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("invalid CA PEM in %s", caPath)
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: caPool}
	if certPath != "" && keyPath != "" {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load client key pair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}

func (s *Scheduler) newAgentClient(node models.Node) (agentpb.AgentServiceClient, *grpc.ClientConn, error) {
	addr := s.grpcAddressForNode(node)
	if strings.TrimSpace(addr) == "" {
		return nil, nil, fmt.Errorf("node %s has invalid grpc address", node.NodeID)
	}

	var dialOpts []grpc.DialOption
	if strings.EqualFold(os.Getenv("PERSYS_TLS_ENABLED"), "true") {
		tlsCfg, err := loadClientTLSConfigFromEnv()
		if err != nil {
			return nil, nil, err
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	dialOpts = append(dialOpts, grpc.WithStatsHandler(otelgrpc.NewClientHandler()))

	ctx, cancel := context.WithTimeout(context.Background(), s.rpcTimeout())
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr, dialOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("dial agent %s (%s): %w", node.NodeID, addr, err)
	}
	return agentpb.NewAgentServiceClient(conn), conn, nil
}

func normalizeDesiredState(v string) agentpb.DesiredState {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "running":
		return agentpb.DesiredState_DESIRED_STATE_RUNNING
	case "stopped", "stop":
		return agentpb.DesiredState_DESIRED_STATE_STOPPED
	default:
		return agentpb.DesiredState_DESIRED_STATE_RUNNING
	}
}

func (s *Scheduler) ensureWorkloadRevision(workload *models.Workload) {
	if strings.TrimSpace(workload.RevisionID) != "" {
		return
	}
	clone := *workload
	clone.CreatedAt = time.Time{}
	clone.Logs = ""
	clone.Status = ""
	clone.Metadata = nil
	payload, _ := json.Marshal(clone)
	h := sha256.Sum256(payload)
	workload.RevisionID = hex.EncodeToString(h[:12])
}

func parsePortMapping(port string) (*agentpb.PortMapping, error) {
	if strings.TrimSpace(port) == "" {
		return nil, fmt.Errorf("empty port mapping")
	}
	parts := strings.Split(port, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid port mapping %q", port)
	}
	hostPortRaw := parts[0]
	containerSpec := parts[1]
	protocol := "tcp"
	if strings.Contains(containerSpec, "/") {
		specParts := strings.SplitN(containerSpec, "/", 2)
		containerSpec = specParts[0]
		if specParts[1] != "" {
			protocol = strings.ToLower(specParts[1])
		}
	}
	hostPort, err := strconv.Atoi(hostPortRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid host port in %q: %w", port, err)
	}
	containerPort, err := strconv.Atoi(containerSpec)
	if err != nil {
		return nil, fmt.Errorf("invalid container port in %q: %w", port, err)
	}
	return &agentpb.PortMapping{HostPort: int32(hostPort), ContainerPort: int32(containerPort), Protocol: protocol}, nil
}

func parseVolumeMount(volume string) (*agentpb.VolumeMount, error) {
	if strings.TrimSpace(volume) == "" {
		return nil, fmt.Errorf("empty volume mapping")
	}
	parts := strings.Split(volume, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid volume mapping %q", volume)
	}
	readOnly := false
	if len(parts) > 2 {
		readOnly = strings.EqualFold(parts[2], "ro")
	}
	return &agentpb.VolumeMount{HostPath: parts[0], ContainerPath: parts[1], ReadOnly: readOnly}, nil
}

func normalizeProjectName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return "workload"
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "workload"
	}
	return out
}

func (s *Scheduler) buildApplyWorkloadRequest(workload models.Workload) (*agentpb.ApplyWorkloadRequest, error) {
	s.ensureWorkloadRevision(&workload)

	req := &agentpb.ApplyWorkloadRequest{
		Id:           workload.ID,
		RevisionId:   workload.RevisionID,
		DesiredState: normalizeDesiredState(workload.DesiredState),
		Spec:         &agentpb.WorkloadSpec{},
	}

	switch strings.ToLower(strings.TrimSpace(workload.Type)) {
	case "docker-container", "container":
		req.Type = agentpb.WorkloadType_WORKLOAD_TYPE_CONTAINER
		containerSpec := &agentpb.ContainerSpec{
			Image:  workload.Image,
			Env:    workload.EnvVars,
			Labels: workload.Labels,
			RestartPolicy: &agentpb.RestartPolicy{
				Policy: workload.RestartPolicy,
			},
		}
		if cmd := strings.TrimSpace(workload.Command); cmd != "" {
			containerSpec.Command = strings.Fields(cmd)
		}
		for _, port := range workload.Ports {
			pm, err := parsePortMapping(port)
			if err != nil {
				return nil, err
			}
			containerSpec.Ports = append(containerSpec.Ports, pm)
		}
		for _, volume := range workload.Volumes {
			vm, err := parseVolumeMount(volume)
			if err != nil {
				return nil, err
			}
			containerSpec.Volumes = append(containerSpec.Volumes, vm)
		}
		req.Spec.Spec = &agentpb.WorkloadSpec_Container{Container: containerSpec}

	case "docker-compose", "compose":
		req.Type = agentpb.WorkloadType_WORKLOAD_TYPE_COMPOSE
		composeYAML := strings.TrimSpace(workload.ComposeYAML)
		if composeYAML == "" {
			composeYAML = strings.TrimSpace(workload.Compose)
		}
		if composeYAML == "" {
			return nil, fmt.Errorf("compose_yaml is required for compose workloads")
		}
		projectName := strings.TrimSpace(workload.ProjectName)
		if projectName == "" {
			projectName = normalizeProjectName(workload.Name)
		}
		req.Spec.Spec = &agentpb.WorkloadSpec_Compose{Compose: &agentpb.ComposeSpec{
			ProjectName: projectName,
			ComposeYaml: composeYAML,
			Env:         workload.EnvVars,
		}}

	case "vm":
		req.Type = agentpb.WorkloadType_WORKLOAD_TYPE_VM
		if workload.VM == nil {
			return nil, fmt.Errorf("vm spec is required for vm workloads")
		}
		vmSpec := &agentpb.VMSpec{
			Name:      workload.VM.Name,
			Vcpus:     workload.VM.VCPUs,
			MemoryMb:  workload.VM.MemoryMB,
			CloudInit: workload.VM.CloudInit,
			Metadata:  workload.VM.Metadata,
		}
		if workload.VM.CloudInitConfig != nil {
			vmSpec.CloudInitConfig = &agentpb.CloudInitConfig{
				UserData:      workload.VM.CloudInitConfig.UserData,
				MetaData:      workload.VM.CloudInitConfig.MetaData,
				NetworkConfig: workload.VM.CloudInitConfig.NetworkConfig,
				VendorData:    workload.VM.CloudInitConfig.VendorData,
			}
		}
		for _, disk := range workload.VM.Disks {
			vmSpec.Disks = append(vmSpec.Disks, &agentpb.DiskConfig{
				Path:   disk.Path,
				Device: disk.Device,
				Format: disk.Format,
				SizeGb: disk.SizeGB,
				Type:   disk.Type,
				Boot:   disk.Boot,
			})
		}
		for _, network := range workload.VM.Networks {
			vmSpec.Networks = append(vmSpec.Networks, &agentpb.NetworkConfig{
				Network:    network.Network,
				MacAddress: network.MAC,
				IpAddress:  network.IPAddress,
			})
		}
		req.Spec.Spec = &agentpb.WorkloadSpec_Vm{Vm: vmSpec}

	default:
		return nil, fmt.Errorf("unsupported workload type: %s", workload.Type)
	}

	return req, nil
}

func (s *Scheduler) applyWorkloadOnNode(ctx context.Context, node models.Node, workload models.Workload) (resp *agentpb.ApplyWorkloadResponse, err error) {
	start := time.Now()
	defer func() {
		metricspkg.ObserveAgentRPC("ApplyWorkload", err, time.Since(start))
	}()

	client, conn, err := s.newAgentClient(node)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	req, err := s.buildApplyWorkloadRequest(workload)
	if err != nil {
		return nil, err
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, s.rpcTimeout())
	defer cancel()
	resp, err = client.ApplyWorkload(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Scheduler) getWorkloadStatusFromNode(ctx context.Context, node models.Node, workloadID string) (resp *agentpb.WorkloadStatus, err error) {
	start := time.Now()
	defer func() {
		metricspkg.ObserveAgentRPC("GetWorkloadStatus", err, time.Since(start))
	}()

	client, conn, err := s.newAgentClient(node)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, s.rpcTimeout())
	defer cancel()
	statusResp, err := client.GetWorkloadStatus(ctx, &agentpb.GetWorkloadStatusRequest{Id: workloadID})
	if err != nil {
		return nil, err
	}
	resp = statusResp.GetStatus()
	return resp, nil
}

func (s *Scheduler) getWorkloadActionsFromNode(ctx context.Context, node models.Node, workloadID string, limit int32) (actions []*agentpb.AgentAction, err error) {
	start := time.Now()
	defer func() {
		metricspkg.ObserveAgentRPC("ListActions", err, time.Since(start))
	}()

	client, conn, err := s.newAgentClient(node)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, s.rpcTimeout())
	defer cancel()
	resp, err := client.ListActions(ctx, &agentpb.ListActionsRequest{WorkloadId: workloadID, NewestFirst: true, Limit: limit})
	if err != nil {
		return nil, err
	}
	actions = resp.GetActions()
	return actions, nil
}

func (s *Scheduler) deleteWorkloadFromNode(ctx context.Context, node models.Node, workloadID string) (resp *agentpb.DeleteWorkloadResponse, err error) {
	start := time.Now()
	defer func() {
		metricspkg.ObserveAgentRPC("DeleteWorkload", err, time.Since(start))
	}()

	client, conn, err := s.newAgentClient(node)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, s.rpcTimeout())
	defer cancel()
	resp, err = client.DeleteWorkload(ctx, &agentpb.DeleteWorkloadRequest{Id: workloadID})
	return resp, err
}

func mapActualStateToSchedulerStatus(actual agentpb.ActualState) string {
	switch actual {
	case agentpb.ActualState_ACTUAL_STATE_PENDING:
		return "Pending"
	case agentpb.ActualState_ACTUAL_STATE_RUNNING:
		return "Running"
	case agentpb.ActualState_ACTUAL_STATE_STOPPED:
		return "Stopped"
	case agentpb.ActualState_ACTUAL_STATE_FAILED:
		return "Failed"
	case agentpb.ActualState_ACTUAL_STATE_UNKNOWN:
		return "Unknown"
	default:
		return "Unknown"
	}
}

func isTerminalActualState(actual agentpb.ActualState) bool {
	return actual == agentpb.ActualState_ACTUAL_STATE_RUNNING || actual == agentpb.ActualState_ACTUAL_STATE_STOPPED || actual == agentpb.ActualState_ACTUAL_STATE_FAILED
}

func grpcStatusCode(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	return st.Code().String()
}

func isWorkloadStatusNotFound(err error) bool {
	if err == nil {
		return false
	}
	if strings.EqualFold(grpcStatusCode(err), "not_found") {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "status not found") ||
		strings.Contains(msg, "workload not found") ||
		strings.Contains(msg, "not found")
}

func isNodeUnreachableError(err error) bool {
	if err == nil {
		return false
	}
	code := strings.ToLower(grpcStatusCode(err))
	if code == "unavailable" || code == "deadline_exceeded" {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "transport: error while dialing") ||
		strings.Contains(msg, "connection error")
}

func (s *Scheduler) waitForWorkloadTerminalStatus(ctx context.Context, node models.Node, workload models.Workload, timeout time.Duration) (*agentpb.WorkloadStatus, []*agentpb.AgentAction, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(timeout)
	for {
		statusResp, err := s.getWorkloadStatusFromNode(ctx, node, workload.ID)
		if err == nil {
			if statusResp != nil && isTerminalActualState(statusResp.GetActualState()) {
				return statusResp, nil, nil
			}
		} else {
			if isWorkloadStatusNotFound(err) {
				return nil, nil, err
			}
		}

		if time.Now().After(deadline) {
			actions, aErr := s.getWorkloadActionsFromNode(ctx, node, workload.ID, 20)
			if aErr != nil {
				return nil, nil, fmt.Errorf("timeout waiting for workload terminal state; also failed to query actions: %v", aErr)
			}
			return nil, actions, fmt.Errorf("timeout waiting for workload terminal state")
		}
		time.Sleep(s.agentPollInterval())
	}
}
