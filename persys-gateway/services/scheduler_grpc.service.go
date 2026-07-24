package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	controlv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/controlv1"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func (s *ClusterControlService) ApplyWorkload(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.ApplyWorkloadRequest) (*controlv1.ApplyWorkloadResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.ApplyWorkload(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.ApplyWorkloadResponse), nil
}

func (s *ClusterControlService) ListNodes(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.ListNodesRequest) (*controlv1.ListNodesResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.ListNodes(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.ListNodesResponse), nil
}

func (s *ClusterControlService) ListWorkloads(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.ListWorkloadsRequest) (*controlv1.ListWorkloadsResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.ListWorkloads(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.ListWorkloadsResponse), nil
}

func (s *ClusterControlService) GetWorkload(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.GetWorkloadRequest) (*controlv1.GetWorkloadResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.GetWorkload(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.GetWorkloadResponse), nil
}

func (s *ClusterControlService) DeleteWorkload(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.DeleteWorkloadRequest) (*controlv1.DeleteWorkloadResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.DeleteWorkload(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.DeleteWorkloadResponse), nil
}

func (s *ClusterControlService) RetryWorkload(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.RetryWorkloadRequest) (*controlv1.RetryWorkloadResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.RetryWorkload(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.RetryWorkloadResponse), nil
}

func (s *ClusterControlService) DrainNode(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.DrainNodeRequest) (*controlv1.DrainNodeResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.DrainNode(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.DrainNodeResponse), nil
}

func (s *ClusterControlService) UndrainNode(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.UndrainNodeRequest) (*controlv1.UndrainNodeResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.UndrainNode(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.UndrainNodeResponse), nil
}

func (s *ClusterControlService) TaintNode(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.TaintNodeRequest) (*controlv1.TaintNodeResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.TaintNode(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.TaintNodeResponse), nil
}

func (s *ClusterControlService) UntaintNode(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.UntaintNodeRequest) (*controlv1.UntaintNodeResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.UntaintNode(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.UntaintNodeResponse), nil
}

func (s *ClusterControlService) SetNodeLabel(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.SetNodeLabelRequest) (*controlv1.SetNodeLabelResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.SetNodeLabel(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.SetNodeLabelResponse), nil
}

func (s *ClusterControlService) DeleteNodeLabel(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.DeleteNodeLabelRequest) (*controlv1.DeleteNodeLabelResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.DeleteNodeLabel(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.DeleteNodeLabelResponse), nil
}

func (s *ClusterControlService) GetNode(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.GetNodeRequest) (*controlv1.GetNodeResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.GetNode(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.GetNodeResponse), nil
}

func (s *ClusterControlService) GetClusterSummary(ctx context.Context, clusterID, sessionKey, workloadKey string, req *controlv1.GetClusterSummaryRequest) (*controlv1.GetClusterSummaryResponse, error) {
	resp, err := s.invokeControlRPC(ctx, clusterID, sessionKey, workloadKey, func(client controlv1.AgentControlClient) (any, error) {
		return client.GetClusterSummary(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*controlv1.GetClusterSummaryResponse), nil
}

func (s *ClusterControlService) invokeControlRPC(ctx context.Context, clusterID, sessionKey, workloadKey string, call func(controlv1.AgentControlClient) (any, error)) (any, error) {
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

// InvokeDynamic implements grpcbridge.Invoker. It reuses the exact same
// candidate ranking, dial, and failover logic as invokeControlRPC above —
// this is deliberately NOT a separate connection-selection path. The only
// difference from the typed methods (ApplyWorkload, ListNodes, etc.) is
// that the request/response are dynamicpb messages built from reflection
// instead of generated Go types, so the call goes through conn.Invoke
// with a full method name string rather than a generated client method.
func (s *ClusterControlService) InvokeDynamic(ctx context.Context, clusterID, sessionKey, workloadKey, fullMethod string, in, out proto.Message) error {
	if clusterID == "" {
		clusterID = s.schedulerPool.DefaultClusterID()
	}

	candidates, err := s.schedulerPool.OrderedSchedulers(clusterID, sessionKey, workloadKey)
	if err != nil {
		return fmt.Errorf("select scheduler candidates for cluster %q: %w", clusterID, err)
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

		callWithTrace := injectTraceContext(ctx)
		rpcErr := conn.Invoke(callWithTrace, fullMethod, in, out)
		_ = conn.Close()
		if rpcErr != nil {
			s.schedulerPool.MarkUnhealthy(clusterID, target.Address)
			lastErr = rpcErr
			continue
		}
		return nil
	}

	if lastErr == nil {
		lastErr = ErrNoHealthySchedulers
	}
	return lastErr
}

// DialForReflection implements grpcbridge.ReflectionSource. Any one
// healthy candidate is sufficient for descriptor discovery — reflection
// responses are identical across replicas of the same deployed version —
// so this deliberately skips the retry loop above: a failed reflection
// dial just means "try again on the next refresh tick" (see
// grpcbridge.Bridge.refreshLoop), not "fail the request."
func (s *ClusterControlService) DialForReflection(ctx context.Context, clusterID string) (*grpc.ClientConn, error) {
	if clusterID == "" {
		clusterID = s.schedulerPool.DefaultClusterID()
	}
	candidates, err := s.schedulerPool.OrderedSchedulers(clusterID, "", "")
	if err != nil || len(candidates) == 0 {
		return nil, fmt.Errorf("no scheduler candidates for cluster %q: %w", clusterID, err)
	}
	return grpc.DialContext(ctx, candidates[0].Address,
		grpc.WithTransportCredentials(credentials.NewTLS(s.clientTLS)),
		grpc.WithBlock(),
	)
}

// Forgery methods (TriggerBuild, UpsertProject, ForwardWebhookTest,
// ListPipelineStatus, invokeForgeryRPC) moved to services/forgery.service.go
// as ForgeryService: forgery is a single fixed address with no pool or
// failover, and sharing ProwService/ClusterControlService's shape here
// was never honest about that difference.

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

// forgeryClientWithContext moved to services/forgery.service.go.
