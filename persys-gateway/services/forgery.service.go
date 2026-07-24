package services

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/forgeryv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

// ForgeryService is the gateway's client to persys-forgery's single fixed
// gRPC address. Split out from ClusterControlService (formerly
// ProwService), which pools and fails over across many scheduler
// replicas per cluster — forgery has no pool to fail over between, so
// giving it that shape would have been dishonest about what it actually
// is.
type ForgeryService struct {
	cfg       *config.Config
	clientTLS *tls.Config
}

func NewForgeryService(cfg *config.Config, clientTLS *tls.Config) *ForgeryService {
	return &ForgeryService{cfg: cfg, clientTLS: clientTLS}
}

func (s *ForgeryService) dial(ctx context.Context, timeout time.Duration) (*grpc.ClientConn, error) {
	var forgeryTLS *tls.Config
	if s.clientTLS != nil {
		forgeryTLS = s.clientTLS.Clone()
	} else {
		forgeryTLS = &tls.Config{}
	}
	if serverName := s.cfg.Forgery.GRPCServerName; serverName != "" {
		forgeryTLS.ServerName = serverName
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := grpc.DialContext(callCtx, s.cfg.Forgery.GRPCAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(forgeryTLS)),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial forgery %s: %w", s.cfg.Forgery.GRPCAddr, err)
	}
	return conn, nil
}

func (s *ForgeryService) invokeForgeryRPC(ctx context.Context, call func(forgeryv1.ForgeryControlClient) (any, error)) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := s.dial(ctx, 15*time.Second)
	if err != nil {
		return nil, err
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

func (s *ForgeryService) TriggerBuild(ctx context.Context, req *forgeryv1.TriggerBuildRequest) (*forgeryv1.OperationStatus, error) {
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

func (s *ForgeryService) UpsertProject(ctx context.Context, req *forgeryv1.UpsertProjectRequest) (*forgeryv1.ProjectResponse, error) {
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

func (s *ForgeryService) ForwardWebhookTest(ctx context.Context, req *forgeryv1.ForwardWebhookRequest) (*forgeryv1.ForwardWebhookResponse, error) {
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

func (s *ForgeryService) ListPipelineStatus(ctx context.Context, req *forgeryv1.ListPipelineStatusRequest) (*forgeryv1.ListPipelineStatusResponse, error) {
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

// InvokeDynamic implements grpcbridge.Invoker. clusterID/sessionKey/
// workloadKey are accepted to satisfy the interface but unused — forgery
// has no per-cluster routing today. If that changes, this is the one
// place that grows pool logic; the bridge and its callers don't need to
// know either way.
func (s *ForgeryService) InvokeDynamic(ctx context.Context, _, _, _, fullMethod string, in, out proto.Message) error {
	conn, err := s.dial(ctx, 15*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Invoke(injectTraceContext(ctx), fullMethod, in, out)
}

// DialForReflection implements grpcbridge.ReflectionSource.
func (s *ForgeryService) DialForReflection(ctx context.Context, _ string) (*grpc.ClientConn, error) {
	return s.dial(ctx, 10*time.Second)
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
