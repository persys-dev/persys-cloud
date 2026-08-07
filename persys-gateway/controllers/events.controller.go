package controllers

import (
	"context"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	controlv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/controlv1"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
)

// EventsController exposes cluster-wide scheduler events to browser-based
// dashboard clients over Server-Sent Events. WatchEvents is a
// server-streaming gRPC RPC, which grpcbridge explicitly does not
// auto-bridge (see internal/grpcbridge/bridge.go) — this hand-written
// endpoint is the browser-facing equivalent. A gRPC-native client (e.g.
// persysctl) should call the scheduler's WatchEvents RPC directly instead
// of going through this endpoint.
type EventsController struct {
	clusterControl *services.ClusterControlService
}

func NewEventsController(clusterControl *services.ClusterControlService) *EventsController {
	return &EventsController{clusterControl: clusterControl}
}

// Register mounts /events/watch on rg,  — this
// is dashboard-user-facing mTLS-gated SSE endpoint for cluster-wide scheduler events. A gRPC-native client.
func (c *EventsController) Register(rg *gin.RouterGroup) {
	rg.GET("/events/watch", c.WatchHandler())
}

// WatchHandler streams cluster-wide scheduler events to the client as
// Server-Sent Events, replaying recent history first (same semantics as
// the underlying WatchEvents RPC). Supports the same optional filters as
// query params: ?type=&workload_id=&node_id=&cluster_id=
func (c *EventsController) WatchHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := strings.TrimSpace(ctx.Query("cluster_id"))
		req := &controlv1.WatchEventsRequest{
			Type:       strings.TrimSpace(ctx.Query("type")),
			WorkloadId: strings.TrimSpace(ctx.Query("workload_id")),
			NodeId:     strings.TrimSpace(ctx.Query("node_id")),
		}

		ctx.Header("Content-Type", "text/event-stream")
		ctx.Header("Cache-Control", "no-cache")
		ctx.Header("Connection", "keep-alive")
		// Disable response buffering if this gateway ever sits behind
		// nginx — otherwise SSE events sit in a buffer instead of
		// reaching the browser as they arrive.
		ctx.Header("X-Accel-Buffering", "no")

		streamCtx, cancel := context.WithCancel(ctx.Request.Context())
		defer cancel()

		events := make(chan *controlv1.SchedulerEventView, 16)
		go func() {
			defer close(events)
			_ = c.clusterControl.WatchEventsForClient(streamCtx, clusterID, req, func(event *controlv1.SchedulerEventView) error {
				select {
				case events <- event:
					return nil
				case <-streamCtx.Done():
					return streamCtx.Err()
				}
			})
			// Errors here (scheduler unreachable, stream ended, etc) are
			// intentionally swallowed rather than surfaced to the HTTP
			// response: by the time an error could occur, headers are
			// already flushed and the client is mid-stream — the
			// connection simply closes, and a well-behaved EventSource
			// client on the dashboard side reconnects on its own.
		}()

		ctx.Stream(func(w io.Writer) bool {
			select {
			case event, ok := <-events:
				if !ok {
					return false
				}
				ctx.SSEvent("event", event)
				return true
			case <-streamCtx.Done():
				return false
			}
		})
	}
}
