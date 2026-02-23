package metrics

import (
	"fmt"
	"strings"
	"sync/atomic"
)

var (
	webhooksReceivedTotal atomic.Uint64
	webhooksFailedTotal   atomic.Uint64
	buildsStartedTotal    atomic.Uint64
	buildsSucceededTotal  atomic.Uint64
	buildsFailedTotal     atomic.Uint64
	pipelineEventsTotal   atomic.Uint64
	grpcRequestsTotal     atomic.Uint64
	grpcErrorsTotal       atomic.Uint64

	buildQueueDepth   atomic.Int64
	webhookQueueDepth atomic.Int64
)

func IncWebhooksReceived() { webhooksReceivedTotal.Add(1) }
func IncWebhooksFailed()   { webhooksFailedTotal.Add(1) }
func IncBuildsStarted()    { buildsStartedTotal.Add(1) }
func IncBuildsSucceeded()  { buildsSucceededTotal.Add(1) }
func IncBuildsFailed()     { buildsFailedTotal.Add(1) }
func IncPipelineEvents()   { pipelineEventsTotal.Add(1) }

func IncGRPCRequest() { grpcRequestsTotal.Add(1) }
func IncGRPCError()   { grpcErrorsTotal.Add(1) }

func SetBuildQueueDepth(v int64) {
	buildQueueDepth.Store(v)
}

func SetWebhookQueueDepth(v int64) {
	webhookQueueDepth.Store(v)
}

func RenderPrometheus() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# TYPE persys_forgery_webhooks_received_total counter\n")
	fmt.Fprintf(&b, "persys_forgery_webhooks_received_total %d\n", webhooksReceivedTotal.Load())
	fmt.Fprintf(&b, "# TYPE persys_forgery_webhooks_failed_total counter\n")
	fmt.Fprintf(&b, "persys_forgery_webhooks_failed_total %d\n", webhooksFailedTotal.Load())
	fmt.Fprintf(&b, "# TYPE persys_forgery_builds_started_total counter\n")
	fmt.Fprintf(&b, "persys_forgery_builds_started_total %d\n", buildsStartedTotal.Load())
	fmt.Fprintf(&b, "# TYPE persys_forgery_builds_succeeded_total counter\n")
	fmt.Fprintf(&b, "persys_forgery_builds_succeeded_total %d\n", buildsSucceededTotal.Load())
	fmt.Fprintf(&b, "# TYPE persys_forgery_builds_failed_total counter\n")
	fmt.Fprintf(&b, "persys_forgery_builds_failed_total %d\n", buildsFailedTotal.Load())
	fmt.Fprintf(&b, "# TYPE persys_forgery_pipeline_events_total counter\n")
	fmt.Fprintf(&b, "persys_forgery_pipeline_events_total %d\n", pipelineEventsTotal.Load())
	fmt.Fprintf(&b, "# TYPE persys_forgery_grpc_requests_total counter\n")
	fmt.Fprintf(&b, "persys_forgery_grpc_requests_total %d\n", grpcRequestsTotal.Load())
	fmt.Fprintf(&b, "# TYPE persys_forgery_grpc_errors_total counter\n")
	fmt.Fprintf(&b, "persys_forgery_grpc_errors_total %d\n", grpcErrorsTotal.Load())
	fmt.Fprintf(&b, "# TYPE persys_forgery_build_queue_depth gauge\n")
	fmt.Fprintf(&b, "persys_forgery_build_queue_depth %d\n", buildQueueDepth.Load())
	fmt.Fprintf(&b, "# TYPE persys_forgery_webhook_queue_depth gauge\n")
	fmt.Fprintf(&b, "persys_forgery_webhook_queue_depth %d\n", webhookQueueDepth.Load())
	return b.String()
}
