package services

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	controlv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/controlv1"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/forgeryv1"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

func (s *ProwService) ApplyWorkload(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.ApplyWorkloadRequest) (*controlv1.ApplyWorkloadResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.ApplyWorkload(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.ApplyWorkloadResponse), nil
}

func (s *ProwService) ListNodes(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.ListNodesRequest) (*controlv1.ListNodesResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.ListNodes(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.ListNodesResponse), nil
}

func (s *ProwService) ListWorkloads(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.ListWorkloadsRequest) (*controlv1.ListWorkloadsResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.ListWorkloads(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.ListWorkloadsResponse), nil
}

func (s *ProwService) GetWorkload(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.GetWorkloadRequest) (*controlv1.GetWorkloadResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.GetWorkload(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.GetWorkloadResponse), nil
}

func (s *ProwService) DeleteWorkload(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.DeleteWorkloadRequest) (*controlv1.DeleteWorkloadResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.DeleteWorkload(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.DeleteWorkloadResponse), nil
}

func (s *ProwService) RetryWorkload(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.RetryWorkloadRequest) (*controlv1.RetryWorkloadResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.RetryWorkload(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.RetryWorkloadResponse), nil
}

func (s *ProwService) GetNode(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.GetNodeRequest) (*controlv1.GetNodeResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.GetNode(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.GetNodeResponse), nil
}

func (s *ProwService) GetClusterSummary(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.GetClusterSummaryRequest) (*controlv1.GetClusterSummaryResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.GetClusterSummary(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.GetClusterSummaryResponse), nil
}

func (s *ProwService) invokeControlRPC(ctx context.Context, clusterID, sessionKey, workloadKey string, call func(controlv1.AgentControlClient) (any, error)) (any, error) {
	if clusterID == "" {
		clusterID = s.schedulerPool.DefaultClusterID()
	}

	candidates, err := s.schedulerPool.OrderedSchedulers(clusterID, sessionKey, workloadKey)
	if err != nil {
		return nil, fmt.Errorf("select scheduler candidates for cluster %q: %w", clusterID, err)
	}

	var lastErr error
	for _, target := range candidates {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		conn, dialErr := grpc.DialContext(callCtx, target.Address,
			grpc.WithTransportCredentials(credentials.NewTLS(s.clientTLS)),
			grpc.WithBlock(),
		)
		cancel()
		if dialErr != nil {
			s.schedulerPool.MarkUnhealthy(clusterID, target.Address)
			lastErr = dialErr
			continue
		}

		client := controlv1.NewAgentControlClient(conn)
		callWithTrace := injectTraceContext(ctx)
		resp, rpcErr := call(clientFromContext(client, callWithTrace))
		_ = conn.Close()
		if rpcErr != nil {
			s.schedulerPool.MarkUnhealthy(clusterID, target.Address)
			lastErr = rpcErr
			continue
		}
		return resp, nil
	}

	if lastErr == nil {
		lastErr = ErrNoHealthySchedulers
	}
	return nil, lastErr
}

func (s *ProwService) TriggerBuild(ctx context.Context, req *forgeryv1.TriggerBuildRequest) (*forgeryv1.OperationStatus, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	resp, err := s.invokeForgeryRPC(ctx, func(client forgeryv1.ForgeryControlClient) (any, error) {
		return client.TriggerBuild(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*forgeryv1.OperationStatus), nil
}

func (s *ProwService) UpsertProject(ctx context.Context, req *forgeryv1.UpsertProjectRequest) (*forgeryv1.ProjectResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	resp, err := s.invokeForgeryRPC(ctx, func(client forgeryv1.ForgeryControlClient) (any, error) {
		return client.UpsertProject(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*forgeryv1.ProjectResponse), nil
}

func (s *ProwService) ForwardWebhookTest(ctx context.Context, req *forgeryv1.ForwardWebhookRequest) (*forgeryv1.ForwardWebhookResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	req.Verified = true
	resp, err := s.invokeForgeryRPC(ctx, func(client forgeryv1.ForgeryControlClient) (any, error) {
		return client.ForwardWebhook(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*forgeryv1.ForwardWebhookResponse), nil
}

func (s *ProwService) ListPipelineStatus(ctx context.Context, req *forgeryv1.ListPipelineStatusRequest) (*forgeryv1.ListPipelineStatusResponse, error) {
	if req == nil {
		req = &forgeryv1.ListPipelineStatusRequest{}
	}
	resp, err := s.invokeForgeryRPC(ctx, func(client forgeryv1.ForgeryControlClient) (any, error) {
		return client.ListPipelineStatus(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*forgeryv1.ListPipelineStatusResponse), nil
}

func (s *ProwService) invokeForgeryRPC(ctx context.Context, call func(forgeryv1.ForgeryControlClient) (any, error)) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var forgeryTLS *tls.Config
	if s.clientTLS != nil {
		forgeryTLS = s.clientTLS.Clone()
	} else {
		forgeryTLS = &tls.Config{}
	}
	if serverName := s.config.Forgery.GRPCServerName; serverName != "" {
		forgeryTLS.ServerName = serverName
	}

	conn, err := grpc.DialContext(callCtx, s.config.Forgery.GRPCAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(forgeryTLS)),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial forgery %s: %w", s.config.Forgery.GRPCAddr, err)
	}
	defer conn.Close()

	client := forgeryv1.NewForgeryControlClient(conn)
	callWithTrace := injectTraceContext(ctx)
	resp, err := call(forgeryClientFromContext(client, callWithTrace))
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func injectTraceContext(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}
	carrier := metadataCarrier(md)
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return metadata.NewOutgoingContext(ctx, md)
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
	key = strings.ToLower(key)
	md := metadata.MD(m)
	md.Set(key, value)
}

func (m metadataCarrier) Keys() []string {
	md := metadata.MD(m)
	out := make([]string, 0, len(md))
	for k := range md {
		out = append(out, k)
	}
	return out
}

type controlClientWithContext struct {
	controlv1.AgentControlClient
	ctx context.Context
}

func clientFromContext(client controlv1.AgentControlClient, ctx context.Context) controlv1.AgentControlClient {
	return &controlClientWithContext{AgentControlClient: client, ctx: ctx}
}

func (c *controlClientWithContext) ApplyWorkload(_ context.Context, req *controlv1.ApplyWorkloadRequest, opts ...grpc.CallOption) (*controlv1.ApplyWorkloadResponse, error) {
	return c.AgentControlClient.ApplyWorkload(c.ctx, req, opts...)
}
func (c *controlClientWithContext) ListNodes(_ context.Context, req *controlv1.ListNodesRequest, opts ...grpc.CallOption) (*controlv1.ListNodesResponse, error) {
	return c.AgentControlClient.ListNodes(c.ctx, req, opts...)
}
func (c *controlClientWithContext) ListWorkloads(_ context.Context, req *controlv1.ListWorkloadsRequest, opts ...grpc.CallOption) (*controlv1.ListWorkloadsResponse, error) {
	return c.AgentControlClient.ListWorkloads(c.ctx, req, opts...)
}
func (c *controlClientWithContext) GetWorkload(_ context.Context, req *controlv1.GetWorkloadRequest, opts ...grpc.CallOption) (*controlv1.GetWorkloadResponse, error) {
	return c.AgentControlClient.GetWorkload(c.ctx, req, opts...)
}
func (c *controlClientWithContext) DeleteWorkload(_ context.Context, req *controlv1.DeleteWorkloadRequest, opts ...grpc.CallOption) (*controlv1.DeleteWorkloadResponse, error) {
	return c.AgentControlClient.DeleteWorkload(c.ctx, req, opts...)
}
func (c *controlClientWithContext) RetryWorkload(_ context.Context, req *controlv1.RetryWorkloadRequest, opts ...grpc.CallOption) (*controlv1.RetryWorkloadResponse, error) {
	return c.AgentControlClient.RetryWorkload(c.ctx, req, opts...)
}
func (c *controlClientWithContext) GetNode(_ context.Context, req *controlv1.GetNodeRequest, opts ...grpc.CallOption) (*controlv1.GetNodeResponse, error) {
	return c.AgentControlClient.GetNode(c.ctx, req, opts...)
}
func (c *controlClientWithContext) GetClusterSummary(_ context.Context, req *controlv1.GetClusterSummaryRequest, opts ...grpc.CallOption) (*controlv1.GetClusterSummaryResponse, error) {
	return c.AgentControlClient.GetClusterSummary(c.ctx, req, opts...)
}
func (c *controlClientWithContext) RegisterNode(_ context.Context, req *controlv1.RegisterNodeRequest, opts ...grpc.CallOption) (*controlv1.RegisterNodeResponse, error) {
	return c.AgentControlClient.RegisterNode(c.ctx, req, opts...)
}
func (c *controlClientWithContext) Heartbeat(_ context.Context, req *controlv1.HeartbeatRequest, opts ...grpc.CallOption) (*controlv1.HeartbeatResponse, error) {
	return c.AgentControlClient.Heartbeat(c.ctx, req, opts...)
}

type forgeryClientWithContext struct {
	forgeryv1.ForgeryControlClient
	ctx context.Context
}

func forgeryClientFromContext(client forgeryv1.ForgeryControlClient, ctx context.Context) forgeryv1.ForgeryControlClient {
	return &forgeryClientWithContext{ForgeryControlClient: client, ctx: ctx}
}

func (c *forgeryClientWithContext) ForwardWebhook(_ context.Context, req *forgeryv1.ForwardWebhookRequest, opts ...grpc.CallOption) (*forgeryv1.ForwardWebhookResponse, error) {
	return c.ForgeryControlClient.ForwardWebhook(c.ctx, req, opts...)
}
func (c *forgeryClientWithContext) UpsertProject(_ context.Context, req *forgeryv1.UpsertProjectRequest, opts ...grpc.CallOption) (*forgeryv1.ProjectResponse, error) {
	return c.ForgeryControlClient.UpsertProject(c.ctx, req, opts...)
}
func (c *forgeryClientWithContext) GetProject(_ context.Context, req *forgeryv1.GetProjectRequest, opts ...grpc.CallOption) (*forgeryv1.ProjectResponse, error) {
	return c.ForgeryControlClient.GetProject(c.ctx, req, opts...)
}
func (c *forgeryClientWithContext) ListProjects(_ context.Context, req *forgeryv1.ListProjectsRequest, opts ...grpc.CallOption) (*forgeryv1.ListProjectsResponse, error) {
	return c.ForgeryControlClient.ListProjects(c.ctx, req, opts...)
}
func (c *forgeryClientWithContext) DeleteProject(_ context.Context, req *forgeryv1.DeleteProjectRequest, opts ...grpc.CallOption) (*forgeryv1.OperationStatus, error) {
	return c.ForgeryControlClient.DeleteProject(c.ctx, req, opts...)
}
func (c *forgeryClientWithContext) StoreGitHubCredential(_ context.Context, req *forgeryv1.StoreGitHubCredentialRequest, opts ...grpc.CallOption) (*forgeryv1.OperationStatus, error) {
	return c.ForgeryControlClient.StoreGitHubCredential(c.ctx, req, opts...)
}
func (c *forgeryClientWithContext) ListUserRepositories(_ context.Context, req *forgeryv1.ListUserRepositoriesRequest, opts ...grpc.CallOption) (*forgeryv1.ListUserRepositoriesResponse, error) {
	return c.ForgeryControlClient.ListUserRepositories(c.ctx, req, opts...)
}
func (c *forgeryClientWithContext) RegisterWebhook(_ context.Context, req *forgeryv1.RegisterWebhookRequest, opts ...grpc.CallOption) (*forgeryv1.OperationStatus, error) {
	return c.ForgeryControlClient.RegisterWebhook(c.ctx, req, opts...)
}
func (c *forgeryClientWithContext) TriggerBuild(_ context.Context, req *forgeryv1.TriggerBuildRequest, opts ...grpc.CallOption) (*forgeryv1.OperationStatus, error) {
	return c.ForgeryControlClient.TriggerBuild(c.ctx, req, opts...)
}
func (c *forgeryClientWithContext) ListPipelineStatus(_ context.Context, req *forgeryv1.ListPipelineStatusRequest, opts ...grpc.CallOption) (*forgeryv1.ListPipelineStatusResponse, error) {
	return c.ForgeryControlClient.ListPipelineStatus(c.ctx, req, opts...)
}
