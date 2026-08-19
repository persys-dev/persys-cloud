package services

import (
	"context"
	"fmt"
	"io"

	controlv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/controlv1"
)

// WatchEventsForClient opens a server-streaming WatchEvents call against a
// healthy scheduler in clusterID (same candidate ranking/failover as
// invokeControlRPC in scheduler_grpc.service.go), and calls onEvent for
// each event received until ctx is cancelled, onEvent returns an error, or
// the stream ends.
//
// This exists as a hand-written method — rather than going through
// grpcbridge like the unary methods in scheduler_grpc.service.go — because
// grpcbridge explicitly does not bridge streaming RPCs (see
// internal/grpcbridge/bridge.go's package doc). It's the backing call for
// EventsController's SSE endpoint (controllers/events.controller.go).
func (s *ClusterControlService) WatchEventsForClient(ctx context.Context, clusterID string, req *controlv1.WatchEventsRequest, onEvent func(*controlv1.SchedulerEventView) error) error {
	if clusterID == "" {
		clusterID = s.schedulerPool.DefaultClusterID()
	}
	candidates, err := s.schedulerPool.OrderedSchedulers(clusterID, "", "")
	if err != nil {
		return fmt.Errorf("select scheduler candidates for cluster %q: %w", clusterID, err)
	}

	var lastErr error
	for _, target := range candidates {
		conn, dialErr := dialGRPCTLS(ctx, target.Address, s.clientTLS, s.certMgr)
		if dialErr != nil {
			s.schedulerPool.MarkUnhealthy(clusterID, target.Address)
			lastErr = dialErr
			continue
		}

		client := controlv1.NewAgentControlClient(conn)
		stream, streamErr := client.WatchEvents(injectTraceContext(ctx), req)
		if streamErr != nil {
			_ = conn.Close()
			s.schedulerPool.MarkUnhealthy(clusterID, target.Address)
			lastErr = streamErr
			continue
		}

		for {
			event, recvErr := stream.Recv()
			if recvErr != nil {
				_ = conn.Close()
				if recvErr == io.EOF || ctx.Err() != nil {
					return ctx.Err()
				}
				return recvErr
			}
			if err := onEvent(event); err != nil {
				_ = conn.Close()
				return err
			}
		}
	}

	if lastErr == nil {
		lastErr = ErrNoHealthySchedulers
	}
	return lastErr
}
