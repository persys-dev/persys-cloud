package control

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/config"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/resources"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/runtime"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/workload"
	controlv1 "github.com/persys-dev/persys-cloud/compute-agent/pkg/control/v1"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultRPCTimeout       = 10 * time.Second
	defaultHeartbeatSeconds = 10
)

// Client manages scheduler control-plane communication.
type Client struct {
	cfg             *config.Config
	workloadManager *workload.Manager
	runtimeMgr      *runtime.Manager
	resourceMonitor *resources.Monitor
	logger          *logrus.Entry
}

func NewClient(cfg *config.Config, workloadManager *workload.Manager, runtimeMgr *runtime.Manager, resourceMonitor *resources.Monitor, logger *logrus.Logger) *Client {
	return &Client{
		cfg:             cfg,
		workloadManager: workloadManager,
		runtimeMgr:      runtimeMgr,
		resourceMonitor: resourceMonitor,
		logger:          logger.WithField("component", "scheduler-control-client"),
	}
}

// ApplyWorkload forwards workload intent to the scheduler control plane.
func (c *Client) ApplyWorkload(ctx context.Context, req *controlv1.ApplyWorkloadRequest) (*controlv1.ApplyWorkloadResponse, error) {
	conn, client, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rpcCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()
	return client.ApplyWorkload(rpcCtx, req)
}

// DeleteWorkload forwards workload deletion intent to the scheduler control plane.
func (c *Client) DeleteWorkload(ctx context.Context, req *controlv1.DeleteWorkloadRequest) (*controlv1.DeleteWorkloadResponse, error) {
	conn, client, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rpcCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()
	return client.DeleteWorkload(rpcCtx, req)
}

// RetryWorkload forwards retry intent to the scheduler control plane.
func (c *Client) RetryWorkload(ctx context.Context, req *controlv1.RetryWorkloadRequest) (*controlv1.RetryWorkloadResponse, error) {
	conn, client, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rpcCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()
	return client.RetryWorkload(rpcCtx, req)
}

// Run starts registration/heartbeat loop and blocks until ctx is canceled.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		conn, client, err := c.dial(ctx)
		if err != nil {
			c.logger.WithError(err).Warn("Failed to connect to scheduler control endpoint")
			if !sleepWithContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		regResp, err := c.register(ctx, client)
		if err != nil {
			c.logger.WithError(err).Warn("Node registration failed")
			_ = conn.Close()
			if !sleepWithContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		if !regResp.GetAccepted() {
			c.logger.WithField("reason", regResp.GetReason()).Warn("Scheduler rejected node registration")
			_ = conn.Close()
			if !sleepWithContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		backoff = time.Second
		interval := time.Duration(regResp.GetHeartbeatIntervalSeconds()) * time.Second
		if interval <= 0 {
			interval = defaultHeartbeatSeconds * time.Second
		}
		leaseExpiry := timestampToTime(regResp.GetLeaseExpiresAt())

		c.logger.WithFields(logrus.Fields{
			"heartbeat_interval": interval,
			"lease_expires_at":   leaseExpiry.Format(time.RFC3339),
		}).Info("Successfully registered node with scheduler")

		if _, err := c.heartbeat(ctx, client); err != nil {
			c.logger.WithError(err).Warn("Initial heartbeat failed after registration")
			_ = conn.Close()
			continue
		}

		ticker := time.NewTicker(interval)
		reconnect := false
		for !reconnect {
			select {
			case <-ctx.Done():
				ticker.Stop()
				_ = conn.Close()
				return
			case <-ticker.C:
				if !leaseExpiry.IsZero() && time.Now().After(leaseExpiry) {
					c.logger.Warn("Node lease expired; re-registering")
					reconnect = true
					continue
				}

				hbResp, err := c.heartbeat(ctx, client)
				if err != nil {
					c.logger.WithError(err).Warn("Heartbeat failed; reconnecting")
					reconnect = true
					continue
				}
				if hbResp.GetLeaseExpiresAt() != nil {
					leaseExpiry = timestampToTime(hbResp.GetLeaseExpiresAt())
				}
				if hbResp.GetDrainNode() {
					c.logger.Warn("Scheduler requested drain mode for this node")
				}
				if !hbResp.GetAcknowledged() {
					c.logger.Warn("Heartbeat was not acknowledged")
				}
			}
		}

		ticker.Stop()
		_ = conn.Close()
	}
}

func (c *Client) register(ctx context.Context, client controlv1.AgentControlClient) (*controlv1.RegisterNodeResponse, error) {
	capabilities, err := c.nodeCapabilities()
	if err != nil {
		return nil, err
	}

	rpcCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()

	supported := c.supportedWorkloadTypes()
	c.logger.WithFields(logrus.Fields{
		"node_id":                  c.cfg.NodeID,
		"grpc_endpoint":            c.agentEndpoint(),
		"supported_workload_types": strings.Join(supported, ","),
	}).Info("Registering node with scheduler")

	return client.RegisterNode(rpcCtx, &controlv1.RegisterNodeRequest{
		NodeId:       c.cfg.NodeID,
		Capabilities: capabilities,
		Labels:       c.cfg.NodeLabels,
		AgentVersion: c.cfg.Version,
		GrpcEndpoint: c.agentEndpoint(),
		Timestamp:    timestamppb.Now(),
	})
}

func (c *Client) heartbeat(ctx context.Context, client controlv1.AgentControlClient) (*controlv1.HeartbeatResponse, error) {
	usage := c.nodeUsage()
	workloadStatuses := c.workloadStatuses(ctx)
	workloadUsage := c.workloadUsage(workloadStatuses)

	rpcCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()

	return client.Heartbeat(rpcCtx, &controlv1.HeartbeatRequest{
		NodeId:           c.cfg.NodeID,
		Usage:            usage,
		WorkloadStatuses: workloadStatuses,
		WorkloadUsage:    workloadUsage,
		Timestamp:        timestamppb.Now(),
	})
}

func (c *Client) dial(ctx context.Context) (*grpc.ClientConn, controlv1.AgentControlClient, error) {
	dialCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()

	opts, err := c.dialOptions()
	if err != nil {
		return nil, nil, err
	}

	conn, err := grpc.DialContext(dialCtx, c.cfg.SchedulerAddr, opts...)
	if err != nil {
		return nil, nil, err
	}

	return conn, controlv1.NewAgentControlClient(conn), nil
}

func (c *Client) dialOptions() ([]grpc.DialOption, error) {
	if c.cfg.SchedulerInsecure || !c.cfg.SchedulerTLSEnabled {
		return []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(otelUnaryClientInterceptor("compute-agent-control")),
		}, nil
	}

	cert, err := tls.LoadX509KeyPair(c.cfg.TLSCertPath, c.cfg.TLSKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client cert/key: %w", err)
	}

	caCert, err := os.ReadFile(c.cfg.TLSCAPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      pool,
		Certificates: []tls.Certificate{cert},
	}

	return []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithUnaryInterceptor(otelUnaryClientInterceptor("compute-agent-control")),
	}, nil
}

func otelUnaryClientInterceptor(service string) grpc.UnaryClientInterceptor {
	tr := otel.Tracer(service)
	return func(ctx context.Context, method string, req interface{}, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx, span := tr.Start(ctx, method, trace.WithSpanKind(trace.SpanKindClient))
		defer span.End()

		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}
		otel.GetTextMapPropagator().Inject(ctx, metadataCarrier(md))
		ctx = metadata.NewOutgoingContext(ctx, md)

		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, err.Error())
		}
		return err
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

func (c *Client) nodeCapabilities() (*controlv1.NodeCapabilities, error) {
	cpuCount, err := cpu.Counts(true)
	if err != nil {
		return nil, fmt.Errorf("failed to get CPU count: %w", err)
	}
	memStats, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory stats: %w", err)
	}
	storagePools := []*controlv1.StoragePool{}
	if diskStats, err := disk.Usage("/"); err == nil {
		storagePools = append(storagePools, &controlv1.StoragePool{
			Name:    "root",
			Type:    "local",
			TotalGb: int64(diskStats.Total / 1024 / 1024 / 1024),
		})
	}

	return &controlv1.NodeCapabilities{
		CpuTotalMillicores:      int64(cpuCount * 1000),
		MemoryTotalMb:           int64(memStats.Total / 1024 / 1024),
		StoragePools:            storagePools,
		SupportedWorkloadTypes:  c.supportedWorkloadTypes(),
		SupportedStorageDrivers: c.supportedStorageDrivers(),
	}, nil
}

func (c *Client) nodeUsage() *controlv1.NodeUsage {
	var cpuTotalMillicores int64
	if count, err := cpu.Counts(true); err == nil {
		cpuTotalMillicores = int64(count * 1000)
	}

	var cpuUsedMillicores int64
	var memoryUsedMB int64
	var diskUsedGB int64

	if c.resourceMonitor != nil {
		if util, err := c.resourceMonitor.GetUtilization([]string{"/"}); err == nil {
			cpuUsedMillicores = int64(math.Round(float64(cpuTotalMillicores) * (util.CPUPercent / 100.0)))
			if usage, ok := util.DiskPercent["/"]; ok {
				if diskStats, err := disk.Usage("/"); err == nil {
					diskUsedGB = int64(math.Round(float64(diskStats.Total/1024/1024/1024) * (usage / 100.0)))
				}
			}
		}
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		memoryUsedMB = int64(vm.Used / 1024 / 1024)
	}
	if diskStats, err := disk.Usage("/"); err == nil && diskUsedGB == 0 {
		diskUsedGB = int64(diskStats.Used / 1024 / 1024 / 1024)
	}

	return &controlv1.NodeUsage{
		CpuAllocatedMillicores: 0,
		CpuUsedMillicores:      cpuUsedMillicores,
		MemoryAllocatedMb:      0,
		MemoryUsedMb:           memoryUsedMB,
		DiskAllocatedGb:        0,
		DiskUsedGb:             diskUsedGB,
	}
}

func (c *Client) workloadStatuses(ctx context.Context) []*controlv1.WorkloadStatus {
	if c.workloadManager == nil {
		return nil
	}
	statuses, err := c.workloadManager.ListWorkloads(ctx, nil)
	if err != nil {
		c.logger.WithError(err).Warn("Failed to list workload statuses for heartbeat")
		return nil
	}

	out := make([]*controlv1.WorkloadStatus, 0, len(statuses))
	for _, status := range statuses {
		if status == nil {
			continue
		}

		out = append(out, &controlv1.WorkloadStatus{
			WorkloadId:     status.ID,
			State:          normalizedState(string(status.ActualState)),
			FailureReason:  mapFailureReason(status),
			Message:        status.Message,
			LastTransition: timestamppb.New(nonZeroTime(status.UpdatedAt)),
			Reason:         reasonDetail(status),
			Usage:          usageSnapshot(status),
		})
	}

	return out
}

func (c *Client) workloadUsage(statuses []*controlv1.WorkloadStatus) []*controlv1.WorkloadUsageSnapshot {
	if len(statuses) == 0 {
		return nil
	}
	out := make([]*controlv1.WorkloadUsageSnapshot, 0, len(statuses))
	for _, status := range statuses {
		if status == nil || status.GetUsage() == nil {
			continue
		}
		out = append(out, status.GetUsage())
	}
	return out
}

func (c *Client) supportedWorkloadTypes() []string {
	types := make([]string, 0, 3)
	if c.runtimeMgr != nil && c.runtimeMgr.IsEnabled(models.WorkloadTypeContainer) {
		types = append(types, "container")
	}
	if c.runtimeMgr != nil && c.runtimeMgr.IsEnabled(models.WorkloadTypeCompose) {
		types = append(types, "compose")
	}
	if c.runtimeMgr != nil && c.runtimeMgr.IsEnabled(models.WorkloadTypeVM) {
		types = append(types, "vm")
	}

	// Fallback to config flags if runtime manager is unavailable.
	if c.runtimeMgr == nil {
		if c.cfg.DockerEnabled {
			types = append(types, "container")
		}
		if c.cfg.ComposeEnabled {
			types = append(types, "compose")
		}
		if c.cfg.VMEnabled {
			types = append(types, "vm")
		}
	}
	return types
}

func (c *Client) supportedStorageDrivers() []string {
	drivers := []string{"local"}
	seen := map[string]struct{}{"local": {}}
	if strings.TrimSpace(c.cfg.StorageNFSServer) != "" && strings.TrimSpace(c.cfg.StorageNFSExport) != "" {
		drivers = appendUniqueDriver(drivers, seen, "nfs")
	}
	if strings.TrimSpace(c.cfg.StorageCephPool) != "" {
		drivers = appendUniqueDriver(drivers, seen, "ceph-rbd")
	}
	for _, labelKey := range []string{"storage.nfs", "storage.ceph_rbd", "storage.ceph-rbd"} {
		if strings.EqualFold(strings.TrimSpace(c.cfg.NodeLabels[labelKey]), "true") {
			switch labelKey {
			case "storage.nfs":
				drivers = appendUniqueDriver(drivers, seen, "nfs")
			default:
				drivers = appendUniqueDriver(drivers, seen, "ceph-rbd")
			}
		}
	}
	return drivers
}

func appendUniqueDriver(drivers []string, seen map[string]struct{}, driver string) []string {
	normalized := strings.ToLower(strings.TrimSpace(driver))
	if normalized == "" {
		return drivers
	}
	if _, ok := seen[normalized]; ok {
		return drivers
	}
	seen[normalized] = struct{}{}
	return append(drivers, normalized)
}

func (c *Client) agentEndpoint() string {
	if c.cfg.AgentGRPCEndpoint != "" {
		return c.cfg.AgentGRPCEndpoint
	}

	host := c.cfg.GRPCAddr
	if isWildcardHost(host) || strings.EqualFold(host, "localhost") {
		if ip, err := outboundLocalIP(c.cfg.SchedulerAddr); err == nil && ip != "" {
			host = ip
		} else if ip, err := firstNonLoopbackIP(); err == nil && ip != "" {
			host = ip
		} else {
			host = "127.0.0.1"
		}
	}

	return net.JoinHostPort(host, strconv.Itoa(c.cfg.GRPCPort))
}

func normalizedState(in string) string {
	if in == "" {
		return "Unknown"
	}
	s := strings.ToLower(strings.TrimSpace(in))
	switch s {
	case "running":
		return "Running"
	case "stopped":
		return "Stopped"
	case "failed":
		return "Failed"
	case "pending":
		return "Pending"
	default:
		return "Unknown"
	}
}

func mapFailureReason(status *models.WorkloadStatus) controlv1.FailureReason {
	if status == nil {
		return controlv1.FailureReason_FAILURE_REASON_UNSPECIFIED
	}
	if strings.EqualFold(string(status.ActualState), "failed") {
		return controlv1.FailureReason_RUNTIME_ERROR
	}
	return controlv1.FailureReason_FAILURE_REASON_UNSPECIFIED
}

func reasonDetail(status *models.WorkloadStatus) *controlv1.ReasonDetail {
	if status == nil {
		return nil
	}
	code := ""
	message := ""
	retryable := false
	nextRetry := time.Time{}
	if status.Metadata != nil {
		code = strings.TrimSpace(status.Metadata["failure_reason"])
		message = strings.TrimSpace(status.Metadata["failure_message"])
		retryable = strings.EqualFold(strings.TrimSpace(status.Metadata["retryable"]), "true")
		if rawNext := strings.TrimSpace(status.Metadata["retry_next_at"]); rawNext != "" {
			if parsed, err := time.Parse(time.RFC3339, rawNext); err == nil {
				nextRetry = parsed
			}
		}
	}
	if code == "" && message == "" && nextRetry.IsZero() && !retryable {
		return nil
	}
	reason := &controlv1.ReasonDetail{
		Code:           code,
		Message:        message,
		LastTransition: timestamppb.New(nonZeroTime(status.UpdatedAt)),
		Retryable:      retryable,
	}
	if !nextRetry.IsZero() {
		reason.NextRetryAt = timestamppb.New(nextRetry.UTC())
	}
	return reason
}

func usageSnapshot(status *models.WorkloadStatus) *controlv1.WorkloadUsageSnapshot {
	if status == nil || status.Usage == nil {
		return nil
	}
	collected := status.Usage.CollectedAt
	if collected.IsZero() {
		collected = nonZeroTime(status.UpdatedAt)
	}
	return &controlv1.WorkloadUsageSnapshot{
		WorkloadId:     strings.TrimSpace(status.ID),
		Type:           strings.TrimSpace(status.Usage.Type),
		CpuPercent:     status.Usage.CPUPercent,
		MemoryBytes:    status.Usage.MemoryBytes,
		DiskReadBytes:  status.Usage.DiskReadBytes,
		DiskWriteBytes: status.Usage.DiskWriteBytes,
		NetRxBytes:     status.Usage.NetRXBytes,
		NetTxBytes:     status.Usage.NetTXBytes,
		CollectedAt:    timestamppb.New(collected.UTC()),
		Source:         strings.TrimSpace(status.Usage.Source),
	}
}

func nonZeroTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

func timestampToTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > 30*time.Second {
		return 30 * time.Second
	}
	return next
}

func isWildcardHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	return h == "" || h == "0.0.0.0" || h == "::" || h == "[::]"
}

// outboundLocalIP picks the local source IP for traffic toward the scheduler.
// This avoids advertising hostnames that are not resolvable from scheduler.
func outboundLocalIP(schedulerAddr string) (string, error) {
	target := schedulerAddr
	if _, _, err := net.SplitHostPort(target); err != nil {
		// Handle bare host/IP values without explicit port.
		if strings.Contains(err.Error(), "missing port in address") {
			target = net.JoinHostPort(target, "80")
		} else {
			return "", err
		}
	}

	conn, err := net.Dial("udp", target)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	udpAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || udpAddr.IP == nil {
		return "", fmt.Errorf("could not determine local UDP address")
	}
	if !udpAddr.IP.IsGlobalUnicast() {
		return "", fmt.Errorf("local IP is not global unicast: %s", udpAddr.IP.String())
	}
	return udpAddr.IP.String(), nil
}

func firstNonLoopbackIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	var ipv6Candidate string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil || !ipNet.IP.IsGlobalUnicast() {
				continue
			}
			if v4 := ipNet.IP.To4(); v4 != nil {
				return v4.String(), nil
			}
			if ipv6Candidate == "" {
				ipv6Candidate = ipNet.IP.String()
			}
		}
	}

	if ipv6Candidate != "" {
		return ipv6Candidate, nil
	}
	return "", fmt.Errorf("no non-loopback IP address found")
}
