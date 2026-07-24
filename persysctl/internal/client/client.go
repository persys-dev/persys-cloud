package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	agentv1 "github.com/persys-dev/persys-cloud/pkg/agent/api/v1"
	automationv1 "github.com/persys-dev/persys-cloud/pkg/automation/automationv1"
	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	controlv1 "github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1"
	"github.com/persys-dev/persysctl/internal/config"
	"github.com/persys-dev/persysctl/internal/models"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Client struct {
	cfg             config.Config
	httpClient      *http.Client
	grpcConn        *grpc.ClientConn
	schedulerClient controlv1.AgentControlClient
	agentClient     agentv1.AgentServiceClient
	certCancel      context.CancelFunc
}

type ScheduleResponse struct {
	WorkloadID string `json:"workloadId"`
	NodeID     string `json:"nodeId"`
	Status     string `json:"status"`
}

type GatewaySchedulerInfo struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	IsLeader bool   `json:"is_leader"`
	Healthy  bool   `json:"healthy"`
	LastSeen string `json:"last_seen"`
}

type GatewayClusterInfo struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	RoutingStrategy   string                 `json:"routing_strategy"`
	TotalSchedulers   int                    `json:"total_schedulers"`
	HealthySchedulers int                    `json:"healthy_schedulers"`
	Schedulers        []GatewaySchedulerInfo `json:"schedulers"`
}

type GatewayClustersResponse struct {
	DefaultClusterID string               `json:"default_cluster_id"`
	Clusters         []GatewayClusterInfo `json:"clusters"`
}

type ForgeryBuildTriggerRequest struct {
	ProjectName string `json:"project_name"`
	Repository  string `json:"repository,omitempty"`
	ClusterID   string `json:"cluster_id,omitempty"`
	Ref         string `json:"ref,omitempty"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	Sender      string `json:"sender,omitempty"`
	Mode        string `json:"mode,omitempty"`
	EventType   string `json:"event_type,omitempty"`
}

type ForgeryUpsertProjectRequest struct {
	Name          string `json:"name"`
	RepoURL       string `json:"repo_url"`
	DefaultBranch string `json:"default_branch,omitempty"`
	ClusterID     string `json:"cluster_id,omitempty"`
	BuildType     string `json:"build_type,omitempty"`
	BuildMode     string `json:"build_mode,omitempty"`
	Strategy      string `json:"strategy,omitempty"`
	NexusRepo     string `json:"nexus_repo,omitempty"`
	PipelineYAML  string `json:"pipeline_yaml,omitempty"`
	AutoDeploy    bool   `json:"auto_deploy,omitempty"`
	ImageName     string `json:"image_name,omitempty"`
}

type ForgeryTestWebhookRequest struct {
	DeliveryID string                 `json:"delivery_id,omitempty"`
	EventType  string                 `json:"event_type,omitempty"`
	Repository string                 `json:"repository"`
	ClusterID  string                 `json:"cluster_id,omitempty"`
	Sender     string                 `json:"sender,omitempty"`
	Ref        string                 `json:"ref,omitempty"`
	Before     string                 `json:"before,omitempty"`
	After      string                 `json:"after,omitempty"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

// CreatePolicyRequestSpec is the JSON spec file format accepted by
// `persysctl automation create-policy --spec-file`.
type CreatePolicyRequestSpec struct {
	Name                string `json:"name"`
	TargetWorkload      string `json:"target_workload"`
	Type                string `json:"type"`
	ConditionExpression string `json:"condition_expression"`
	ActionExpression    string `json:"action_expression"`
}

var automationPolicyTypeByName = map[string]automationv1.PolicyType{
	"unspecified": automationv1.PolicyType_POLICY_TYPE_UNSPECIFIED,
	"scale":       automationv1.PolicyType_POLICY_TYPE_SCALE,
	"schedule":    automationv1.PolicyType_POLICY_TYPE_SCHEDULE,
	"offload":     automationv1.PolicyType_POLICY_TYPE_OFFLOAD,
	"drift":       automationv1.PolicyType_POLICY_TYPE_DRIFT,
	"redeploy":    automationv1.PolicyType_POLICY_TYPE_REDEPLOY,
}

// ToProto converts the JSON spec into the wire request. Unknown or empty
// type strings fall back to POLICY_TYPE_UNSPECIFIED.
func (s CreatePolicyRequestSpec) ToProto() *automationv1.CreatePolicyRequest {
	policyType := automationPolicyTypeByName[strings.ToLower(strings.TrimSpace(s.Type))]
	return &automationv1.CreatePolicyRequest{
		Name:                s.Name,
		TargetWorkload:      s.TargetWorkload,
		Type:                policyType,
		ConditionExpression: s.ConditionExpression,
		ActionExpression:    s.ActionExpression,
	}
}

func NewClient(cfg config.Config) (*Client, error) {
	c := &Client{cfg: cfg}

	switch cfg.Transport {
	case "http":
		httpClient, certCancel, err := newHTTPClient(cfg)
		if err != nil {
			return nil, err
		}
		c.httpClient = httpClient
		c.certCancel = certCancel
	case "grpc":
		conn, schedulerClient, agentClient, certCancel, err := newGRPCClient(cfg)
		if err != nil {
			return nil, err
		}
		c.grpcConn = conn
		c.schedulerClient = schedulerClient
		c.agentClient = agentClient
		c.certCancel = certCancel
	default:
		return nil, fmt.Errorf("unsupported transport %q (expected http or grpc)", cfg.Transport)
	}

	return c, nil
}

func (c *Client) Close() error {
	if c.certCancel != nil {
		c.certCancel()
	}
	if c.grpcConn != nil {
		return c.grpcConn.Close()
	}
	return nil
}

func newHTTPClient(cfg config.Config) (*http.Client, context.CancelFunc, error) {
	if cfg.APIEndpoint == "" {
		return nil, nil, fmt.Errorf("api_endpoint is required for http transport")
	}
	if strings.HasPrefix(cfg.APIEndpoint, "http://") {
		return &http.Client{}, nil, nil
	}
	if !strings.HasPrefix(cfg.APIEndpoint, "https://") {
		return nil, nil, fmt.Errorf("api_endpoint must start with http:// or https://, got: %s", cfg.APIEndpoint)
	}

	bindHost := hostFromURL(cfg.APIEndpoint)
	certCancel, err := ensureVaultManagedCertificates(cfg, bindHost, true)
	if err != nil {
		return nil, nil, err
	}
	tlsConfig, err := buildMTLSConfig(cfg)
	if err != nil {
		if certCancel != nil {
			certCancel()
		}
		return nil, nil, fmt.Errorf("failed to get TLS config: %w", err)
	}

	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}, certCancel, nil
}

func newGRPCClient(cfg config.Config) (*grpc.ClientConn, controlv1.AgentControlClient, agentv1.AgentServiceClient, context.CancelFunc, error) {
	if cfg.GRPCEndpoint == "" {
		return nil, nil, nil, nil, fmt.Errorf("grpc_endpoint is required for grpc transport")
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.RPCTimeoutSeconds)*time.Second)
	defer cancel()

	var certCancel context.CancelFunc
	dialOpts := []grpc.DialOption{grpc.WithBlock()}
	if cfg.GRPCInsecure {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		bindHost := hostFromDialTarget(cfg.GRPCEndpoint)
		var err error
		certCancel, err = ensureVaultManagedCertificates(cfg, bindHost, true)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		tlsConfig, err := buildMTLSConfig(cfg)
		if err != nil {
			if certCancel != nil {
				certCancel()
			}
			return nil, nil, nil, nil, err
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	}

	conn, err := grpc.DialContext(dialCtx, cfg.GRPCEndpoint, dialOpts...)
	if err != nil {
		if certCancel != nil {
			certCancel()
		}
		return nil, nil, nil, nil, fmt.Errorf("failed to connect to gRPC endpoint %s: %w", cfg.GRPCEndpoint, err)
	}

	return conn, controlv1.NewAgentControlClient(conn), agentv1.NewAgentServiceClient(conn), certCancel, nil
}

func ensureVaultManagedCertificates(cfg config.Config, bindHost string, tlsEnabled bool) (context.CancelFunc, error) {
	certCfg := certmanager.Config{
		TLSEnabled: tlsEnabled,

		TLSCertPath: cfg.CertPath,
		TLSKeyPath:  cfg.KeyPath,
		TLSCAPath:   cfg.CACertPath,

		VaultEnabled:       cfg.VaultEnabled,
		VaultManagerAddr:   cfg.VaultManagerAddr,
		VaultAddr:          cfg.VaultAddr,
		VaultAuthMethod:    cfg.VaultAuthMethod,
		VaultToken:         cfg.VaultToken,
		VaultAppRoleID:     cfg.VaultAppRoleID,
		VaultAppSecretID:   cfg.VaultAppSecretID,
		VaultPKIMount:      cfg.VaultPKIMount,
		VaultPKIRole:       cfg.VaultPKIRole,
		VaultCertTTL:       cfg.VaultCertTTL,
		VaultServiceName:   cfg.VaultServiceName,
		VaultServiceDomain: cfg.VaultServiceDomain,
		VaultRetryInterval: cfg.VaultRetryInterval,

		BindHost: bindHost,
	}

	logger := logrus.New()
	logger.SetOutput(os.Stderr)
	logger.SetFormatter(&logrus.TextFormatter{})
	certMgr := certmanager.NewManager(certCfg, logger)

	certCtx, cancel := context.WithCancel(context.Background())
	if err := certMgr.Start(certCtx); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize certificate manager: %w", err)
	}
	return cancel, nil
}

func hostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Hostname())
}

func hostFromDialTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(target)
	if err == nil {
		return host
	}
	return target
}

func buildMTLSConfig(cfg config.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate/key: %w", err)
	}

	caPEM, err := os.ReadFile(cfg.CACertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      pool,
		Certificates: []tls.Certificate{cert},
	}, nil
}

func (c *Client) rpcContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(c.cfg.RPCTimeoutSeconds)*time.Second)
}

func (c *Client) requireSchedulerGRPC() error {
	if c.cfg.Transport != "grpc" {
		return fmt.Errorf("this operation requires gRPC transport (set --transport grpc)")
	}
	if c.cfg.GRPCTarget != "scheduler" {
		return fmt.Errorf("this operation requires scheduler target (set --grpc-target scheduler)")
	}
	return nil
}

func (c *Client) requireAgentGRPC() error {
	if c.cfg.Transport != "grpc" {
		return fmt.Errorf("this operation requires gRPC transport (set --transport grpc)")
	}
	if c.cfg.GRPCTarget != "agent" {
		return fmt.Errorf("this operation requires compute-agent target (set --grpc-target agent)")
	}
	return nil
}

// clusterPath resolves a cluster-scoped gateway route (workloads, nodes,
// cluster metrics, forgery) according to c.cfg.APIVersion:
//
//   - "v2": prefixes suffix with /clusters/{cluster_id}, using
//     c.cfg.ClusterID. Fails with a clear error if ClusterID is unset —
//     silently guessing a cluster would be worse than refusing.
//   - anything else, including unset/"v1"/unrecognized: returns suffix
//     unchanged. This is the ORIGINAL flat route shape every
//     persys-gateway has always served (e.g. /workloads/schedule,
//     /nodes/{id}/drain, /cluster/metrics, /forgery/builds/trigger),
//     which resolves to the gateway's default cluster server-side. This
//     is the default specifically so that existing persysctl configs —
//     which predate api_version entirely — keep working with zero
//     changes required.
//
// Not used for /clusters (lists all clusters, inherently cluster-
// agnostic) or /automation, /ai (never gained cluster-scoped variants on
// the gateway side).
func (c *Client) clusterPath(suffix string) (string, error) {
	if c.cfg.APIVersion != "v2" {
		return suffix, nil
	}
	clusterID := strings.TrimSpace(c.cfg.ClusterID)
	if clusterID == "" {
		return "", fmt.Errorf("api_version is \"v2\" but cluster_id is not set (use --cluster-id or set cluster_id in config)")
	}
	return "/clusters/" + url.PathEscape(clusterID) + suffix, nil
}

func (c *Client) ScheduleWorkload(workload models.Workload) (*ScheduleResponse, error) {
	switch c.cfg.Transport {
	case "grpc":
		return c.scheduleWorkloadGRPC(workload)
	case "http":
		return c.scheduleWorkloadHTTP(workload)
	default:
		return nil, fmt.Errorf("unsupported transport %q", c.cfg.Transport)
	}
}

func (c *Client) scheduleWorkloadGRPC(workload models.Workload) (*ScheduleResponse, error) {
	ctx, cancel := c.rpcContext()
	defer cancel()

	switch c.cfg.GRPCTarget {
	case "scheduler":
		req, workloadID, err := toSchedulerApplyRequest(workload)
		if err != nil {
			return nil, err
		}
		resp, err := c.schedulerClient.ApplyWorkload(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("scheduler apply workload failed: %w", err)
		}
		status := "applied"
		if !resp.GetSuccess() {
			status = "failed"
		}
		return &ScheduleResponse{WorkloadID: workloadID, NodeID: "scheduler-managed", Status: status}, nil
	case "agent":
		req, workloadID, err := toAgentApplyRequest(workload)
		if err != nil {
			return nil, err
		}
		resp, err := c.agentClient.ApplyWorkload(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("compute-agent apply workload failed: %w", err)
		}
		status := "applied"
		if !resp.GetApplied() {
			status = "failed"
		}
		return &ScheduleResponse{WorkloadID: workloadID, NodeID: "standalone-agent", Status: status}, nil
	default:
		return nil, fmt.Errorf("unsupported grpc_target %q (expected scheduler or agent)", c.cfg.GRPCTarget)
	}
}

func (c *Client) ListWorkloads(nodeID, status string) ([]models.Workload, error) {
	switch c.cfg.Transport {
	case "http":
		return c.listWorkloadsHTTP(nodeID, status)
	case "grpc":
		ctx, cancel := c.rpcContext()
		defer cancel()

		if c.cfg.GRPCTarget == "scheduler" {
			resp, err := c.schedulerClient.ListWorkloads(ctx, &controlv1.ListWorkloadsRequest{NodeId: nodeID, Status: status})
			if err != nil {
				return nil, fmt.Errorf("scheduler list workloads failed: %w", err)
			}
			out := make([]models.Workload, 0, len(resp.GetWorkloads()))
			for _, w := range resp.GetWorkloads() {
				lastUpdated := time.Time{}
				if w.GetLastUpdated() != nil {
					lastUpdated = w.GetLastUpdated().AsTime()
				}
				retryNext := time.Time{}
				if w.GetRetryNextAt() != nil {
					retryNext = w.GetRetryNextAt().AsTime()
				}
				createdAt := time.Time{}
				if w.GetCreatedAt() != nil {
					createdAt = w.GetCreatedAt().AsTime()
				}
				out = append(out, models.Workload{
					ID:            w.GetWorkloadId(),
					Type:          w.GetType(),
					NodeID:        w.GetAssignedNodeId(),
					DesiredState:  w.GetDesiredState(),
					Status:        w.GetStatus(),
					RevisionID:    w.GetRevisionId(),
					RetryAttempts: w.GetRetryAttempts(),
					RetryMax:      w.GetRetryMaxAttempts(),
					RetryNextAt:   retryNext,
					FailureReason: w.GetFailureReason(),
					Reason:        toModelReason(w.GetReason()),
					Usage:         toModelUsage(w.GetUsage()),
					CreatedAt:     createdAt,
					LastUpdated:   lastUpdated,
				})
			}
			return out, nil
		}

		resp, err := c.agentClient.ListWorkloads(ctx, &agentv1.ListWorkloadsRequest{})
		if err != nil {
			return nil, fmt.Errorf("compute-agent list workloads failed: %w", err)
		}
		out := make([]models.Workload, 0, len(resp.GetWorkloads()))
		for _, w := range resp.GetWorkloads() {
			createdAt := time.Time{}
			if ts := w.GetCreatedAt(); ts > 0 {
				createdAt = time.Unix(ts, 0).UTC()
			}
			updatedAt := time.Time{}
			if ts := w.GetUpdatedAt(); ts > 0 {
				updatedAt = time.Unix(ts, 0).UTC()
			}
			out = append(out, models.Workload{
				ID:           w.GetId(),
				Type:         workloadTypeToString(w.GetType()),
				RevisionID:   w.GetRevisionId(),
				DesiredState: strings.ToLower(strings.TrimPrefix(w.GetDesiredState().String(), "DESIRED_STATE_")),
				Status:       strings.ToLower(strings.TrimPrefix(w.GetActualState().String(), "ACTUAL_STATE_")),
				Message:      w.GetMessage(),
				Metadata:     w.GetMetadata(),
				CreatedAt:    createdAt,
				LastUpdated:  updatedAt,
			})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported transport %q", c.cfg.Transport)
	}
}

func (c *Client) GetWorkload(workloadID string) (*controlv1.GetWorkloadResponse, error) {
	if c.cfg.Transport == "http" {
		return c.getWorkloadHTTP(workloadID)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.GetWorkload(ctx, &controlv1.GetWorkloadRequest{WorkloadId: workloadID})
}

func (c *Client) DeleteWorkload(workloadID string) (*controlv1.DeleteWorkloadResponse, error) {
	if c.cfg.Transport == "http" {
		return c.deleteWorkloadHTTP(workloadID)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.DeleteWorkload(ctx, &controlv1.DeleteWorkloadRequest{WorkloadId: workloadID})
}

func (c *Client) RetryWorkload(workloadID string) (*controlv1.RetryWorkloadResponse, error) {
	if c.cfg.Transport == "http" {
		return c.retryWorkloadHTTP(workloadID)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.RetryWorkload(ctx, &controlv1.RetryWorkloadRequest{WorkloadId: workloadID})
}

func (c *Client) ListNodes(status string) ([]models.Node, error) {
	switch c.cfg.Transport {
	case "http":
		return c.listNodesHTTP(status)
	case "grpc":
		if err := c.requireSchedulerGRPC(); err != nil {
			return nil, err
		}
		ctx, cancel := c.rpcContext()
		defer cancel()
		resp, err := c.schedulerClient.ListNodes(ctx, &controlv1.ListNodesRequest{Status: status})
		if err != nil {
			return nil, fmt.Errorf("scheduler list nodes failed: %w", err)
		}

		nodes := make([]models.Node, 0, len(resp.GetNodes()))
		for _, n := range resp.GetNodes() {
			nodes = append(nodes, models.Node{
				NodeID:    n.GetNodeId(),
				IPAddress: n.GetGrpcEndpoint(),
				Status:    n.GetStatus(),
				Resources: models.Resources{
					CPU:    int(math.Round(n.GetTotalCpuCores() * 1000)),
					Memory: int(n.GetTotalMemoryMb()),
				},
				Labels: n.GetLabels(),
			})
		}
		return nodes, nil
	default:
		return nil, fmt.Errorf("unsupported transport %q", c.cfg.Transport)
	}
}

func (c *Client) GetNode(nodeID string) (*controlv1.GetNodeResponse, error) {
	if c.cfg.Transport == "http" {
		return c.getNodeHTTP(nodeID)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.GetNode(ctx, &controlv1.GetNodeRequest{NodeId: nodeID})
}

func (c *Client) DrainNode(nodeID, reason string) (*controlv1.DrainNodeResponse, error) {
	if c.cfg.Transport == "http" {
		return c.drainNodeHTTP(nodeID, reason)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.DrainNode(ctx, &controlv1.DrainNodeRequest{NodeId: nodeID, Reason: reason})
}

func (c *Client) UndrainNode(nodeID, reason string) (*controlv1.UndrainNodeResponse, error) {
	if c.cfg.Transport == "http" {
		return c.undrainNodeHTTP(nodeID, reason)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.UndrainNode(ctx, &controlv1.UndrainNodeRequest{NodeId: nodeID, Reason: reason})
}

func (c *Client) TaintNode(nodeID, key, value, effect string) (*controlv1.TaintNodeResponse, error) {
	if c.cfg.Transport == "http" {
		return c.taintNodeHTTP(nodeID, key, value, effect)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.TaintNode(ctx, &controlv1.TaintNodeRequest{
		NodeId: nodeID,
		Taint:  &controlv1.NodeTaint{Key: key, Value: value, Effect: effect},
	})
}

func (c *Client) UntaintNode(nodeID, key, effect string) (*controlv1.UntaintNodeResponse, error) {
	if c.cfg.Transport == "http" {
		return c.untaintNodeHTTP(nodeID, key, effect)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.UntaintNode(ctx, &controlv1.UntaintNodeRequest{NodeId: nodeID, Key: key, Effect: effect})
}

func (c *Client) SetNodeLabel(nodeID, key, value string) (*controlv1.SetNodeLabelResponse, error) {
	if c.cfg.Transport == "http" {
		return c.setNodeLabelHTTP(nodeID, key, value)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.SetNodeLabel(ctx, &controlv1.SetNodeLabelRequest{NodeId: nodeID, Key: key, Value: value})
}

func (c *Client) DeleteNodeLabel(nodeID, key string) (*controlv1.DeleteNodeLabelResponse, error) {
	if c.cfg.Transport == "http" {
		return c.deleteNodeLabelHTTP(nodeID, key)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.DeleteNodeLabel(ctx, &controlv1.DeleteNodeLabelRequest{NodeId: nodeID, Key: key})
}

func (c *Client) SchedulerListNodes(status string) (*controlv1.ListNodesResponse, error) {
	if c.cfg.Transport == "http" {
		return c.schedulerListNodesHTTP(status)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.ListNodes(ctx, &controlv1.ListNodesRequest{Status: status})
}

func (c *Client) SchedulerListWorkloads(nodeID, status string) (*controlv1.ListWorkloadsResponse, error) {
	if c.cfg.Transport == "http" {
		return c.schedulerListWorkloadsHTTP(nodeID, status)
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.ListWorkloads(ctx, &controlv1.ListWorkloadsRequest{NodeId: nodeID, Status: status})
}

func (c *Client) GetClusterSummary() (*controlv1.GetClusterSummaryResponse, error) {
	if c.cfg.Transport == "http" {
		return c.getClusterSummaryHTTP()
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.GetClusterSummary(ctx, &controlv1.GetClusterSummaryRequest{})
}

func (c *Client) RegisterNode(req *controlv1.RegisterNodeRequest) (*controlv1.RegisterNodeResponse, error) {
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.RegisterNode(ctx, req)
}

func (c *Client) Heartbeat(req *controlv1.HeartbeatRequest) (*controlv1.HeartbeatResponse, error) {
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.Heartbeat(ctx, req)
}

func (c *Client) ControlStreamSend(msg *controlv1.ControlMessage) (*controlv1.ControlMessage, error) {
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()

	stream, err := c.schedulerClient.ControlStream(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(msg); err != nil {
		return nil, err
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}

	resp, err := stream.Recv()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) ApplySchedulerWorkload(req *controlv1.ApplyWorkloadRequest) (*controlv1.ApplyWorkloadResponse, error) {
	if c.cfg.Transport == "http" {
		path, err := c.clusterPath("/workloads/schedule")
		if err != nil {
			return nil, err
		}
		resp := &controlv1.ApplyWorkloadResponse{}
		if err := c.httpProtoRequest("POST", path, req, resp); err != nil {
			return nil, err
		}
		return resp, nil
	}
	if err := c.requireSchedulerGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.schedulerClient.ApplyWorkload(ctx, req)
}

func (c *Client) ApplyAgentWorkload(req *agentv1.ApplyWorkloadRequest) (*agentv1.ApplyWorkloadResponse, error) {
	if err := c.requireAgentGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.agentClient.ApplyWorkload(ctx, req)
}

func (c *Client) AgentHealthCheck() (*agentv1.HealthCheckResponse, error) {
	if err := c.requireAgentGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.agentClient.HealthCheck(ctx, &agentv1.HealthCheckRequest{})
}

func (c *Client) AgentGetWorkloadStatus(workloadID string) (*agentv1.GetWorkloadStatusResponse, error) {
	if err := c.requireAgentGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.agentClient.GetWorkloadStatus(ctx, &agentv1.GetWorkloadStatusRequest{Id: workloadID})
}

func (c *Client) AgentDeleteWorkload(workloadID string) (*agentv1.DeleteWorkloadResponse, error) {
	if err := c.requireAgentGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.agentClient.DeleteWorkload(ctx, &agentv1.DeleteWorkloadRequest{Id: workloadID})
}

func (c *Client) AgentListActions(workloadID, actionType, status string, limit int32, newestFirst bool) (*agentv1.ListActionsResponse, error) {
	if err := c.requireAgentGRPC(); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcContext()
	defer cancel()
	return c.agentClient.ListActions(ctx, &agentv1.ListActionsRequest{
		WorkloadId:  workloadID,
		ActionType:  actionType,
		Status:      status,
		Limit:       limit,
		NewestFirst: newestFirst,
	})
}

func (c *Client) GetMetrics() (map[string]interface{}, error) {
	if c.cfg.Transport == "grpc" && c.cfg.GRPCTarget == "scheduler" {
		summary, err := c.GetClusterSummary()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"totalNodes":       summary.GetTotalNodes(),
			"readyNodes":       summary.GetReadyNodes(),
			"notReadyNodes":    summary.GetNotReadyNodes(),
			"totalWorkloads":   summary.GetTotalWorkloads(),
			"runningWorkloads": summary.GetRunningWorkloads(),
			"pendingWorkloads": summary.GetPendingWorkloads(),
			"failedWorkloads":  summary.GetFailedWorkloads(),
			"deletedWorkloads": summary.GetDeletedWorkloads(),
			"generatedAt":      summary.GetGeneratedAt().AsTime().Format(time.RFC3339),
		}, nil
	}
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("metrics are available only with scheduler gRPC or http transport")
	}

	path, err := c.clusterPath("/cluster/metrics")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("GET", c.cfg.APIEndpoint+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var metrics map[string]interface{}
	if err := json.Unmarshal(body, &metrics); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}
	return metrics, nil
}

func (c *Client) scheduleWorkloadHTTP(workload models.Workload) (*ScheduleResponse, error) {
	req, workloadID, err := toSchedulerApplyRequest(workload)
	if err != nil {
		return nil, err
	}
	path, err := c.clusterPath("/workloads/schedule")
	if err != nil {
		return nil, err
	}
	resp := &controlv1.ApplyWorkloadResponse{}
	if err := c.httpProtoRequest("POST", path, req, resp); err != nil {
		return nil, err
	}
	scheduleResp := ScheduleResponse{WorkloadID: workloadID, NodeID: "scheduler-managed", Status: "applied"}
	if !resp.GetSuccess() {
		scheduleResp.Status = "failed"
	}
	return &scheduleResp, nil
}

func (c *Client) listWorkloadsHTTP(nodeID, status string) ([]models.Workload, error) {
	q := make(url.Values)
	if strings.TrimSpace(nodeID) != "" {
		q.Set("node_id", strings.TrimSpace(nodeID))
	}
	if strings.TrimSpace(status) != "" {
		q.Set("status", strings.TrimSpace(status))
	}
	path, err := c.clusterPath("/workloads")
	if err != nil {
		return nil, err
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	protoResp := &controlv1.ListWorkloadsResponse{}
	if err := c.httpProtoRequest("GET", path, nil, protoResp); err != nil {
		return nil, err
	}

	workloads := make([]models.Workload, 0, len(protoResp.GetWorkloads()))
	for _, w := range protoResp.GetWorkloads() {
		lastUpdated := time.Time{}
		if w.GetLastUpdated() != nil {
			lastUpdated = w.GetLastUpdated().AsTime()
		}
		retryNext := time.Time{}
		if w.GetRetryNextAt() != nil {
			retryNext = w.GetRetryNextAt().AsTime()
		}
		createdAt := time.Time{}
		if w.GetCreatedAt() != nil {
			createdAt = w.GetCreatedAt().AsTime()
		}
		workloads = append(workloads, models.Workload{
			ID:            w.GetWorkloadId(),
			Type:          w.GetType(),
			NodeID:        w.GetAssignedNodeId(),
			DesiredState:  w.GetDesiredState(),
			Status:        w.GetStatus(),
			RevisionID:    w.GetRevisionId(),
			RetryAttempts: w.GetRetryAttempts(),
			RetryMax:      w.GetRetryMaxAttempts(),
			RetryNextAt:   retryNext,
			FailureReason: w.GetFailureReason(),
			Reason:        toModelReason(w.GetReason()),
			Usage:         toModelUsage(w.GetUsage()),
			CreatedAt:     createdAt,
			LastUpdated:   lastUpdated,
		})
	}
	return workloads, nil
}

func (c *Client) listNodesHTTP(status string) ([]models.Node, error) {
	path, err := c.clusterPath("/nodes")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(status) != "" {
		path += "?status=" + url.QueryEscape(strings.TrimSpace(status))
	}
	protoResp := &controlv1.ListNodesResponse{}
	if err := c.httpProtoRequest("GET", path, nil, protoResp); err != nil {
		return nil, err
	}

	nodes := make([]models.Node, 0, len(protoResp.GetNodes()))
	for _, n := range protoResp.GetNodes() {
		nodes = append(nodes, models.Node{
			NodeID:    n.GetNodeId(),
			IPAddress: n.GetGrpcEndpoint(),
			Status:    n.GetStatus(),
			Resources: models.Resources{
				CPU:    int(math.Round(n.GetTotalCpuCores() * 1000)),
				Memory: int(n.GetTotalMemoryMb()),
			},
			Labels: n.GetLabels(),
		})
	}
	return nodes, nil
}

func (c *Client) makeRequest(method, path string, body io.Reader) (*http.Response, error) {
	url := c.cfg.APIEndpoint + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	return resp, nil
}

func (c *Client) GatewayClusters() (*GatewayClustersResponse, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("gateway cluster API is available only with http transport")
	}
	// Deliberately not routed through clusterPath: this endpoint lists
	// every cluster and is inherently cluster-agnostic. It has never had
	// a cluster-scoped variant on the gateway side, in either v1 or v2.
	resp, err := c.makeRequest("GET", "/clusters", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	var out GatewayClustersResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &out, nil
}

func (c *Client) TriggerForgeryBuild(req ForgeryBuildTriggerRequest) (map[string]interface{}, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("forgery trigger-build is available only with http transport")
	}
	path, err := c.clusterPath("/forgery/builds/trigger")
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := c.httpJSONRequest("POST", path, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) UpsertForgeryProject(req ForgeryUpsertProjectRequest) (map[string]interface{}, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("forgery upsert-project is available only with http transport")
	}
	path, err := c.clusterPath("/forgery/projects/upsert")
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := c.httpJSONRequest("POST", path, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) SendForgeryTestWebhook(req ForgeryTestWebhookRequest) (map[string]interface{}, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("forgery test-webhook is available only with http transport")
	}
	path, err := c.clusterPath("/forgery/webhooks/test")
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := c.httpJSONRequest("POST", path, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) httpProtoRequest(method, path string, reqMsg proto.Message, respMsg proto.Message) error {
	var body io.Reader
	if reqMsg != nil {
		payload, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(reqMsg)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		body = bytes.NewBuffer(payload)
	}
	resp, err := c.makeRequest(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}
	if respMsg == nil {
		return nil
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(respBody, respMsg); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

func (c *Client) httpJSONRequest(method, path string, reqBody interface{}, respBody interface{}) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	resp, err := c.makeRequest(method, path, bodyReader)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	if respBody == nil {
		return nil
	}
	if err := json.Unmarshal(body, respBody); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

func (c *Client) getWorkloadHTTP(workloadID string) (*controlv1.GetWorkloadResponse, error) {
	path, err := c.clusterPath("/workloads/" + url.PathEscape(workloadID))
	if err != nil {
		return nil, err
	}
	resp := &controlv1.GetWorkloadResponse{}
	if err := c.httpProtoRequest("GET", path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) deleteWorkloadHTTP(workloadID string) (*controlv1.DeleteWorkloadResponse, error) {
	path, err := c.clusterPath("/workloads/" + url.PathEscape(workloadID))
	if err != nil {
		return nil, err
	}
	resp := &controlv1.DeleteWorkloadResponse{}
	if err := c.httpProtoRequest("DELETE", path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) retryWorkloadHTTP(workloadID string) (*controlv1.RetryWorkloadResponse, error) {
	path, err := c.clusterPath("/workloads/" + url.PathEscape(workloadID) + "/retry")
	if err != nil {
		return nil, err
	}
	resp := &controlv1.RetryWorkloadResponse{}
	if err := c.httpProtoRequest("POST", path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) getNodeHTTP(nodeID string) (*controlv1.GetNodeResponse, error) {
	path, err := c.clusterPath("/nodes/" + url.PathEscape(nodeID))
	if err != nil {
		return nil, err
	}
	resp := &controlv1.GetNodeResponse{}
	if err := c.httpProtoRequest("GET", path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) drainNodeHTTP(nodeID, reason string) (*controlv1.DrainNodeResponse, error) {
	path, err := c.clusterPath("/nodes/" + url.PathEscape(nodeID) + "/drain")
	if err != nil {
		return nil, err
	}
	resp := &controlv1.DrainNodeResponse{}
	body := map[string]string{"reason": reason}
	if err := c.httpJSONBodyProtoResponse("POST", path, body, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) undrainNodeHTTP(nodeID, reason string) (*controlv1.UndrainNodeResponse, error) {
	path, err := c.clusterPath("/nodes/" + url.PathEscape(nodeID) + "/undrain")
	if err != nil {
		return nil, err
	}
	resp := &controlv1.UndrainNodeResponse{}
	body := map[string]string{"reason": reason}
	if err := c.httpJSONBodyProtoResponse("POST", path, body, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) taintNodeHTTP(nodeID, key, value, effect string) (*controlv1.TaintNodeResponse, error) {
	path, err := c.clusterPath("/nodes/" + url.PathEscape(nodeID) + "/taint")
	if err != nil {
		return nil, err
	}
	resp := &controlv1.TaintNodeResponse{}
	// TaintNodeRequest.Taint is a NESTED message (*NodeTaint{Key,Value,
	// Effect}), unlike every other node-management request here (which
	// have Key/Value/Effect as flat top-level fields). The gateway
	// decodes this body with protojson against the real proto shape, so
	// it must be nested to match — a flat {"key":...} body silently
	// produces an empty Taint and was a real bug before this fix.
	body := map[string]any{"taint": map[string]string{"key": key, "value": value, "effect": effect}}
	if err := c.httpJSONBodyProtoResponse("POST", path, body, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) untaintNodeHTTP(nodeID, key, effect string) (*controlv1.UntaintNodeResponse, error) {
	path, err := c.clusterPath("/nodes/" + url.PathEscape(nodeID) + "/untaint")
	if err != nil {
		return nil, err
	}
	resp := &controlv1.UntaintNodeResponse{}
	body := map[string]string{"key": key, "effect": effect}
	if err := c.httpJSONBodyProtoResponse("POST", path, body, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) setNodeLabelHTTP(nodeID, key, value string) (*controlv1.SetNodeLabelResponse, error) {
	path, err := c.clusterPath("/nodes/" + url.PathEscape(nodeID) + "/labels")
	if err != nil {
		return nil, err
	}
	resp := &controlv1.SetNodeLabelResponse{}
	body := map[string]string{"key": key, "value": value}
	if err := c.httpJSONBodyProtoResponse("POST", path, body, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) deleteNodeLabelHTTP(nodeID, key string) (*controlv1.DeleteNodeLabelResponse, error) {
	path, err := c.clusterPath("/nodes/" + url.PathEscape(nodeID) + "/labels")
	if err != nil {
		return nil, err
	}
	resp := &controlv1.DeleteNodeLabelResponse{}
	body := map[string]string{"key": key}
	if err := c.httpJSONBodyProtoResponse("DELETE", path, body, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) schedulerListNodesHTTP(status string) (*controlv1.ListNodesResponse, error) {
	path, err := c.clusterPath("/nodes")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(status) != "" {
		path += "?status=" + url.QueryEscape(strings.TrimSpace(status))
	}
	resp := &controlv1.ListNodesResponse{}
	if err := c.httpProtoRequest("GET", path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) schedulerListWorkloadsHTTP(nodeID, status string) (*controlv1.ListWorkloadsResponse, error) {
	q := make(url.Values)
	if strings.TrimSpace(nodeID) != "" {
		q.Set("node_id", strings.TrimSpace(nodeID))
	}
	if strings.TrimSpace(status) != "" {
		q.Set("status", strings.TrimSpace(status))
	}
	path, err := c.clusterPath("/workloads")
	if err != nil {
		return nil, err
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp := &controlv1.ListWorkloadsResponse{}
	if err := c.httpProtoRequest("GET", path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// --- Automation (persys-automation, proxied via gateway /automation/*) ---
//
// Not routed through clusterPath: the gateway never gained a cluster-
// scoped variant of these routes, in either v1 or v2 — they stayed flat
// throughout the gateway's routing refactor.

func (c *Client) CreatePolicy(req *automationv1.CreatePolicyRequest) (*automationv1.CreatePolicyResponse, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("automation create-policy is available only with http transport")
	}
	resp := &automationv1.CreatePolicyResponse{}
	if err := c.httpProtoRequest("POST", "/automation/policies", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) ListPolicies(includeDisabled bool) (*automationv1.ListPoliciesResponse, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("automation list-policies is available only with http transport")
	}
	path := "/automation/policies"
	if includeDisabled {
		path += "?include_disabled=true"
	}
	resp := &automationv1.ListPoliciesResponse{}
	if err := c.httpProtoRequest("GET", path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) EnablePolicy(policyID string) (*automationv1.EnablePolicyResponse, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("automation enable-policy is available only with http transport")
	}
	resp := &automationv1.EnablePolicyResponse{}
	if err := c.httpProtoRequest("POST", "/automation/policies/"+url.PathEscape(policyID)+"/enable", nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) DisablePolicy(policyID string) (*automationv1.DisablePolicyResponse, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("automation disable-policy is available only with http transport")
	}
	resp := &automationv1.DisablePolicyResponse{}
	if err := c.httpProtoRequest("POST", "/automation/policies/"+url.PathEscape(policyID)+"/disable", nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) EvaluatePolicyNow(policyID string) (*automationv1.EvaluateNowResponse, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("automation evaluate-now is available only with http transport")
	}
	resp := &automationv1.EvaluateNowResponse{}
	if err := c.httpProtoRequest("POST", "/automation/policies/"+url.PathEscape(policyID)+"/evaluate", nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) ListAutomationAuditLog(limit int) (*automationv1.ListAuditLogResponse, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("automation audit-log is available only with http transport")
	}
	path := "/automation/audit-log"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	resp := &automationv1.ListAuditLogResponse{}
	if err := c.httpProtoRequest("GET", path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// --- Intelligence (persys-intelligence, proxied via gateway /ai/*) ---
//
// Not routed through clusterPath: same reasoning as automation above.

type AIQueryRequest struct {
	Query        string `json:"query"`
	ContextScope string `json:"context_scope,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
}

type AIStateSnapshot struct {
	ResourceID   string   `json:"resource_id"`
	ContextScope string   `json:"context_scope"`
	Workload     string   `json:"workload,omitempty"`
	Node         string   `json:"node,omitempty"`
	DesiredState string   `json:"desired_state,omitempty"`
	CPU5mAvg     int32    `json:"cpu_5m_avg,omitempty"`
	CPU1hTrend   string   `json:"cpu_1h_trend,omitempty"`
	MemoryUsage  int32    `json:"memory_usage,omitempty"`
	RetryCount   int32    `json:"retry_count,omitempty"`
	RestartCount int32    `json:"restart_count,omitempty"`
	NodePressure string   `json:"node_pressure,omitempty"`
	RecentEvents []string `json:"recent_events,omitempty"`
	DataSources  []string `json:"data_sources,omitempty"`
	Insufficient bool     `json:"insufficient_data"`
	GeneratedAt  string   `json:"generated_at"`
}

type AIQueryResponse struct {
	Diagnosis             string          `json:"diagnosis"`
	Confidence            float64         `json:"confidence"`
	Impact                string          `json:"impact"`
	Evidence              []string        `json:"evidence"`
	RecommendedActions    []string        `json:"recommended_actions"`
	RequiresHumanApproval bool            `json:"requires_human_approval"`
	InsufficientData      bool            `json:"insufficient_data"`
	InferenceStatus       string          `json:"inference_status"`
	StateSnapshot         AIStateSnapshot `json:"state_snapshot"`
}

type FeatureSnapshot struct {
	Workload        string `json:"workload"`
	CPU5mAvg        int32  `json:"cpu_5m_avg"`
	CPU1hTrend      string `json:"cpu_1h_trend"`
	ErrorRateDelta  string `json:"error_rate_delta"`
	RecentDeploy    bool   `json:"recent_deploy"`
	RetryCount      int32  `json:"retry_count"`
	NodePressure    string `json:"node_pressure"`
	GeneratedSource string `json:"generated_source,omitempty"`
}

type Recommendation struct {
	ID              string          `json:"id"`
	Workload        string          `json:"workload"`
	Type            string          `json:"type"`
	Confidence      float64         `json:"confidence"`
	RiskScore       float64         `json:"risk_score"`
	ReasonCode      string          `json:"reason_code,omitempty"`
	Explanation     string          `json:"explanation"`
	Parameters      map[string]any  `json:"parameters"`
	Status          string          `json:"status"`
	InputSnapshot   FeatureSnapshot `json:"input_snapshot"`
	PromptHash      string          `json:"prompt_hash,omitempty"`
	ModelVersion    string          `json:"model_version,omitempty"`
	DecisionOutcome string          `json:"decision_outcome,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

func (c *Client) AIQuery(req AIQueryRequest) (*AIQueryResponse, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("ai query is available only with http transport")
	}
	if req.ContextScope == "" {
		req.ContextScope = "cluster"
	}
	resp := &AIQueryResponse{}
	if err := c.httpJSONRequest("POST", "/ai/query", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) ListRecommendations(status, workload string) ([]Recommendation, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("ai recommendations is available only with http transport")
	}
	q := make(url.Values)
	if strings.TrimSpace(status) != "" {
		q.Set("status", strings.TrimSpace(status))
	}
	if strings.TrimSpace(workload) != "" {
		q.Set("workload", strings.TrimSpace(workload))
	}
	path := "/ai/recommendations"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out []Recommendation
	if err := c.httpJSONRequest("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) ListPendingRecommendations(workload string) ([]Recommendation, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("ai recommendations is available only with http transport")
	}
	path := "/ai/recommendations/pending"
	if strings.TrimSpace(workload) != "" {
		path += "?workload=" + url.QueryEscape(strings.TrimSpace(workload))
	}
	var out []Recommendation
	if err := c.httpJSONRequest("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) ApproveRecommendation(id string) (*Recommendation, error) {
	return c.actOnRecommendation(id, "approve")
}

func (c *Client) RejectRecommendation(id string) (*Recommendation, error) {
	return c.actOnRecommendation(id, "reject")
}

func (c *Client) ApplyRecommendation(id string) (*Recommendation, error) {
	return c.actOnRecommendation(id, "apply")
}

func (c *Client) actOnRecommendation(id, action string) (*Recommendation, error) {
	if c.cfg.Transport != "http" {
		return nil, fmt.Errorf("ai recommendations is available only with http transport")
	}
	var out Recommendation
	path := "/ai/recommendations/" + url.PathEscape(id) + "/" + action
	if err := c.httpJSONRequest("POST", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// httpJSONBodyProtoResponse sends a plain JSON body (matching the gateway's
// flat handler payload, e.g. {"reason":"..."} or {"key":"...","value":"..."})
// and decodes the response with protojson into a proto.Message. Used for the
// node-management endpoints where the gateway does not accept the full
// nested proto request as-is.
func (c *Client) httpJSONBodyProtoResponse(method, path string, jsonBody interface{}, respMsg proto.Message) error {
	var body io.Reader
	if jsonBody != nil {
		payload, err := json.Marshal(jsonBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		body = bytes.NewBuffer(payload)
	}
	resp, err := c.makeRequest(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}
	if respMsg == nil {
		return nil
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(respBody, respMsg); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

func (c *Client) getClusterSummaryHTTP() (*controlv1.GetClusterSummaryResponse, error) {
	path, err := c.clusterPath("/cluster/metrics")
	if err != nil {
		return nil, err
	}
	resp := &controlv1.GetClusterSummaryResponse{}
	if err := c.httpProtoRequest("GET", path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func workloadID(w models.Workload) string {
	if w.ID != "" {
		return w.ID
	}
	if w.Name != "" {
		return w.Name
	}
	return fmt.Sprintf("workload-%d", time.Now().Unix())
}

func toModelReason(in *controlv1.ReasonDetail) *models.WorkloadReason {
	if in == nil {
		return nil
	}
	out := &models.WorkloadReason{
		Code:      strings.TrimSpace(in.GetCode()),
		Message:   strings.TrimSpace(in.GetMessage()),
		Retryable: in.GetRetryable(),
	}
	if ts := in.GetLastTransition(); ts != nil {
		out.LastTransition = ts.AsTime().UTC()
	}
	if ts := in.GetNextRetryAt(); ts != nil {
		out.NextRetryAt = ts.AsTime().UTC()
	}
	if out.Code == "" && out.Message == "" && out.LastTransition.IsZero() && out.NextRetryAt.IsZero() && !out.Retryable {
		return nil
	}
	return out
}

func toModelUsage(in *controlv1.WorkloadUsageSnapshot) *models.WorkloadUsage {
	if in == nil {
		return nil
	}
	out := &models.WorkloadUsage{
		WorkloadID:     strings.TrimSpace(in.GetWorkloadId()),
		Type:           strings.TrimSpace(in.GetType()),
		CPUPercent:     in.GetCpuPercent(),
		MemoryBytes:    in.GetMemoryBytes(),
		DiskReadBytes:  in.GetDiskReadBytes(),
		DiskWriteBytes: in.GetDiskWriteBytes(),
		NetRXBytes:     in.GetNetRxBytes(),
		NetTXBytes:     in.GetNetTxBytes(),
		Source:         strings.TrimSpace(in.GetSource()),
	}
	if ts := in.GetCollectedAt(); ts != nil {
		out.CollectedAt = ts.AsTime().UTC()
	}
	if out.WorkloadID == "" && out.Type == "" && out.CPUPercent == 0 && out.MemoryBytes == 0 &&
		out.DiskReadBytes == 0 && out.DiskWriteBytes == 0 && out.NetRXBytes == 0 &&
		out.NetTXBytes == 0 && out.CollectedAt.IsZero() && out.Source == "" {
		return nil
	}
	return out
}

func toSchedulerApplyRequest(w models.Workload) (*controlv1.ApplyWorkloadRequest, string, error) {
	id := workloadID(w)
	spec, err := toSchedulerWorkloadSpec(w)
	if err != nil {
		return nil, "", err
	}

	revision := fmt.Sprintf("rev-%d", time.Now().Unix())
	if w.ID != "" {
		revision = w.ID + "-rev"
	}

	return &controlv1.ApplyWorkloadRequest{
		WorkloadId:   id,
		RevisionId:   revision,
		DesiredState: "Running",
		Spec:         spec,
	}, id, nil
}

func toSchedulerWorkloadSpec(w models.Workload) (*controlv1.WorkloadSpec, error) {
	spec := &controlv1.WorkloadSpec{
		Resources: &controlv1.ResourceRequirements{
			CpuMillicores: int64(w.Resources.CPU),
			MemoryMb:      int64(w.Resources.Memory),
		},
		Metadata: w.Labels,
	}

	switch w.Type {
	case "docker-container", "container":
		spec.Type = "container"
		spec.Workload = &controlv1.WorkloadSpec_Container{Container: &controlv1.ContainerSpec{
			Image:         w.Image,
			Command:       parseCommand(w.Command),
			Env:           w.EnvVars,
			Volumes:       parseControlVolumes(w.Volumes),
			Ports:         parseControlPorts(w.Ports),
			RestartPolicy: w.RestartPolicy,
		}}
	case "docker-compose", "git-compose", "compose":
		rawCompose, _, err := resolveCompose(w)
		if err != nil {
			return nil, err
		}
		spec.Type = "compose"
		compose := &controlv1.ComposeSpec{Env: w.EnvVars}
		if w.Type == "git-compose" && w.GitRepo != "" {
			compose.SourceType = "git"
			compose.GitRepo = w.GitRepo
			compose.GitRef = w.GitBranch
		} else {
			compose.SourceType = "inline"
			compose.InlineYaml = rawCompose
		}
		spec.Workload = &controlv1.WorkloadSpec_Compose{Compose: compose}
	default:
		return nil, fmt.Errorf("unsupported workload type for scheduler grpc: %s", w.Type)
	}

	return spec, nil
}

func toAgentApplyRequest(w models.Workload) (*agentv1.ApplyWorkloadRequest, string, error) {
	id := workloadID(w)
	req := &agentv1.ApplyWorkloadRequest{
		Id:           id,
		RevisionId:   fmt.Sprintf("rev-%d", time.Now().Unix()),
		DesiredState: agentv1.DesiredState_DESIRED_STATE_RUNNING,
		Spec:         &agentv1.WorkloadSpec{},
	}

	switch w.Type {
	case "docker-container", "container":
		req.Type = agentv1.WorkloadType_WORKLOAD_TYPE_CONTAINER
		req.Spec.Spec = &agentv1.WorkloadSpec_Container{Container: &agentv1.ContainerSpec{
			Image:   w.Image,
			Command: parseCommand(w.Command),
			Env:     w.EnvVars,
			Volumes: parseAgentVolumes(w.Volumes),
			Ports:   parseAgentPorts(w.Ports),
			RestartPolicy: &agentv1.RestartPolicy{
				Policy: w.RestartPolicy,
			},
			Labels: w.Labels,
		}}
	case "docker-compose", "compose":
		_, base64Compose, err := resolveCompose(w)
		if err != nil {
			return nil, "", err
		}
		req.Type = agentv1.WorkloadType_WORKLOAD_TYPE_COMPOSE
		req.Spec.Spec = &agentv1.WorkloadSpec_Compose{Compose: &agentv1.ComposeSpec{ProjectName: w.Name, ComposeYaml: base64Compose, Env: w.EnvVars}}
	case "git-compose":
		return nil, "", fmt.Errorf("git-compose is not supported in standalone compute-agent mode; use docker-compose with inline/local compose content")
	default:
		return nil, "", fmt.Errorf("unsupported workload type for compute-agent grpc: %s", w.Type)
	}

	return req, id, nil
}

func resolveCompose(w models.Workload) (raw string, encoded string, err error) {
	if w.LocalPath != "" {
		b, readErr := os.ReadFile(w.LocalPath)
		if readErr != nil {
			return "", "", fmt.Errorf("failed to read local compose file %q: %w", w.LocalPath, readErr)
		}
		raw = string(b)
		encoded = base64.StdEncoding.EncodeToString(b)
		return raw, encoded, nil
	}
	if w.Compose == "" {
		return "", "", fmt.Errorf("compose content is required (set compose or local-path)")
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(w.Compose)
	if decodeErr == nil {
		return string(decoded), w.Compose, nil
	}
	return w.Compose, base64.StdEncoding.EncodeToString([]byte(w.Compose)), nil
}

func parseCommand(cmd string) []string {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	return strings.Fields(cmd)
}

func parseControlVolumes(in []string) []*controlv1.VolumeMount {
	out := make([]*controlv1.VolumeMount, 0, len(in))
	for _, v := range in {
		parts := strings.Split(v, ":")
		if len(parts) < 2 {
			continue
		}
		vm := &controlv1.VolumeMount{HostPath: parts[0], ContainerPath: parts[1]}
		if len(parts) >= 3 {
			vm.ReadOnly = parts[2] == "ro"
		}
		out = append(out, vm)
	}
	return out
}

func parseControlPorts(in []string) []*controlv1.Port {
	out := make([]*controlv1.Port, 0, len(in))
	for _, p := range in {
		host, container, proto := parsePort(p)
		if host == 0 || container == 0 {
			continue
		}
		out = append(out, &controlv1.Port{HostPort: host, ContainerPort: container, Protocol: proto})
	}
	return out
}

func parseAgentVolumes(in []string) []*agentv1.VolumeMount {
	out := make([]*agentv1.VolumeMount, 0, len(in))
	for _, v := range in {
		parts := strings.Split(v, ":")
		if len(parts) < 2 {
			continue
		}
		vm := &agentv1.VolumeMount{HostPath: parts[0], ContainerPath: parts[1]}
		if len(parts) >= 3 {
			vm.ReadOnly = parts[2] == "ro"
		}
		out = append(out, vm)
	}
	return out
}

func parseAgentPorts(in []string) []*agentv1.PortMapping {
	out := make([]*agentv1.PortMapping, 0, len(in))
	for _, p := range in {
		host, container, proto := parsePort(p)
		if host == 0 || container == 0 {
			continue
		}
		out = append(out, &agentv1.PortMapping{HostPort: host, ContainerPort: container, Protocol: proto})
	}
	return out
}

func parsePort(in string) (host int32, container int32, proto string) {
	proto = "tcp"
	parts := strings.SplitN(in, "/", 2)
	if len(parts) == 2 && parts[1] != "" {
		proto = strings.ToLower(parts[1])
	}
	mapping := strings.Split(parts[0], ":")
	if len(mapping) != 2 {
		return 0, 0, proto
	}
	hostInt, err := strconv.Atoi(mapping[0])
	if err != nil {
		return 0, 0, proto
	}
	containerInt, err := strconv.Atoi(mapping[1])
	if err != nil {
		return 0, 0, proto
	}
	return int32(hostInt), int32(containerInt), proto
}

func workloadTypeToString(t agentv1.WorkloadType) string {
	switch t {
	case agentv1.WorkloadType_WORKLOAD_TYPE_CONTAINER:
		return "container"
	case agentv1.WorkloadType_WORKLOAD_TYPE_COMPOSE:
		return "compose"
	case agentv1.WorkloadType_WORKLOAD_TYPE_VM:
		return "vm"
	default:
		return "unknown"
	}
}
