package services

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	controlv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/controlv1"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/forgeryv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
		resp, rpcErr := call(client)
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
	resp, err := call(client)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
