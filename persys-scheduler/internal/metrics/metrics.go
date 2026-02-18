package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	registerOnce sync.Once

	grpcServerRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "grpc_server_requests_total",
			Help:      "Total number of inbound gRPC requests to scheduler.",
		},
		[]string{"method", "code"},
	)
	grpcServerRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "grpc_server_request_duration_seconds",
			Help:      "Inbound gRPC request latency for scheduler.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "code"},
	)

	agentRPCRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "agent_rpc_requests_total",
			Help:      "Total number of outbound RPC calls from scheduler to agents.",
		},
		[]string{"rpc", "code"},
	)
	agentRPCDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "agent_rpc_duration_seconds",
			Help:      "Latency of outbound RPC calls from scheduler to agents.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"rpc", "code"},
	)

	reconciliationResultsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "reconciliation_results_total",
			Help:      "Count of reconciliation results grouped by action and success.",
		},
		[]string{"action", "success"},
	)
	reconciliationCycleDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "reconciliation_cycle_duration_seconds",
			Help:      "Duration of full reconciliation cycles.",
			Buckets:   prometheus.DefBuckets,
		},
	)
	reconciliationCyclesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "reconciliation_cycles_total",
			Help:      "Total number of reconciliation cycles.",
		},
		[]string{"result"},
	)

	nodeStatusGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "nodes_status",
			Help:      "Current number of nodes grouped by status.",
		},
		[]string{"status"},
	)
	workloadStatusGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "workloads_status",
			Help:      "Current number of workloads grouped by status.",
		},
		[]string{"status"},
	)
	workloadDesiredGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "persys",
			Subsystem: "scheduler",
			Name:      "workloads_desired_state",
			Help:      "Current number of workloads grouped by desired state.",
		},
		[]string{"desired_state"},
	)
)

var defaultNodeStatuses = []string{"ready", "active", "notready", "unknown"}
var defaultWorkloadStatuses = []string{"pending", "running", "stopped", "failed", "deleted", "updating", "unknown"}
var defaultDesiredStates = []string{"running", "stopped", "deleted", "unknown"}

func init() {
	Register()
}

func Register() {
	registerOnce.Do(func() {
		prometheus.MustRegister(
			grpcServerRequestsTotal,
			grpcServerRequestDuration,
			agentRPCRequestsTotal,
			agentRPCDuration,
			reconciliationResultsTotal,
			reconciliationCycleDuration,
			reconciliationCyclesTotal,
			nodeStatusGauge,
			workloadStatusGauge,
			workloadDesiredGauge,
		)

		for _, s := range defaultNodeStatuses {
			nodeStatusGauge.WithLabelValues(s).Set(0)
		}
		for _, s := range defaultWorkloadStatuses {
			workloadStatusGauge.WithLabelValues(s).Set(0)
		}
		for _, s := range defaultDesiredStates {
			workloadDesiredGauge.WithLabelValues(s).Set(0)
		}
	})
}

func GRPCUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err).String()
		grpcServerRequestsTotal.WithLabelValues(info.FullMethod, code).Inc()
		grpcServerRequestDuration.WithLabelValues(info.FullMethod, code).Observe(time.Since(start).Seconds())
		return resp, err
	}
}

func GRPCStreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		code := status.Code(err).String()
		grpcServerRequestsTotal.WithLabelValues(info.FullMethod, code).Inc()
		grpcServerRequestDuration.WithLabelValues(info.FullMethod, code).Observe(time.Since(start).Seconds())
		return err
	}
}

func ObserveAgentRPC(rpc string, err error, duration time.Duration) {
	code := status.Code(err).String()
	if err == nil {
		code = codes.OK.String()
	}
	agentRPCRequestsTotal.WithLabelValues(rpc, code).Inc()
	agentRPCDuration.WithLabelValues(rpc, code).Observe(duration.Seconds())
}

func ObserveReconciliationResult(action string, success bool) {
	successLabel := "false"
	if success {
		successLabel = "true"
	}
	reconciliationResultsTotal.WithLabelValues(action, successLabel).Inc()
}

func ObserveReconciliationCycle(duration time.Duration, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	reconciliationCyclesTotal.WithLabelValues(result).Inc()
	reconciliationCycleDuration.Observe(duration.Seconds())
}

func SetNodeStatusCounts(counts map[string]int) {
	for _, s := range defaultNodeStatuses {
		nodeStatusGauge.WithLabelValues(s).Set(0)
	}
	for status, count := range counts {
		nodeStatusGauge.WithLabelValues(status).Set(float64(count))
	}
}

func SetWorkloadStatusCounts(statusCounts map[string]int, desiredCounts map[string]int) {
	for _, s := range defaultWorkloadStatuses {
		workloadStatusGauge.WithLabelValues(s).Set(0)
	}
	for status, count := range statusCounts {
		workloadStatusGauge.WithLabelValues(status).Set(float64(count))
	}

	for _, s := range defaultDesiredStates {
		workloadDesiredGauge.WithLabelValues(s).Set(0)
	}
	for desired, count := range desiredCounts {
		workloadDesiredGauge.WithLabelValues(desired).Set(float64(count))
	}
}
