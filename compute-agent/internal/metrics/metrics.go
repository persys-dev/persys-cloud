package metrics

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// Metrics holds all prometheus metrics for the agent
type Metrics struct {
	// Workload metrics
	WorkloadCount          *prometheus.GaugeVec
	WorkloadCreatedTotal   prometheus.Counter
	WorkloadDeletedTotal   prometheus.Counter
	WorkloadFailedTotal    prometheus.Counter
	WorkloadReconcileTotal prometheus.Counter

	// Operation latency metrics
	ApplyWorkloadDuration     prometheus.Histogram
	DeleteWorkloadDuration    prometheus.Histogram
	ReconcileWorkloadDuration prometheus.Histogram

	// Runtime health metrics
	RuntimeHealthStatus *prometheus.GaugeVec

	// System resource metrics
	SystemMemoryUtilization prometheus.Gauge
	SystemCPUUtilization    prometheus.Gauge
	SystemDiskUtilization   *prometheus.GaugeVec

	// Error metrics
	ErrorCount *prometheus.CounterVec

	// Garbage collection metrics
	GCRunsTotal               prometheus.Counter
	GCDuration                prometheus.Histogram
	GCOrphanedResourcesFound  *prometheus.GaugeVec
	GCOldFailedWorkloadsFound prometheus.Gauge
	GCResourcesDeleted        *prometheus.CounterVec

	// Task queue metrics
	TaskQueueDepth    prometheus.Gauge
	TaskLatency       *prometheus.HistogramVec
	TaskExecutions    *prometheus.CounterVec
	TaskFailuresTotal *prometheus.CounterVec

	mu sync.RWMutex
}

// NewMetrics creates and registers prometheus metrics
func NewMetrics(logger *logrus.Logger) (*Metrics, error) {
	m := &Metrics{
		// Workload metrics with labels for status
		WorkloadCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "persys_agent",
				Subsystem: "workload",
				Name:      "count",
				Help:      "Total number of workloads by state and type",
			},
			[]string{"state", "type"},
		),
		WorkloadCreatedTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "persys_agent",
				Subsystem: "workload",
				Name:      "created_total",
				Help:      "Total number of workloads created",
			},
		),
		WorkloadDeletedTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "persys_agent",
				Subsystem: "workload",
				Name:      "deleted_total",
				Help:      "Total number of workloads deleted",
			},
		),
		WorkloadFailedTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "persys_agent",
				Subsystem: "workload",
				Name:      "failed_total",
				Help:      "Total number of workloads that failed",
			},
		),
		WorkloadReconcileTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "persys_agent",
				Subsystem: "workload",
				Name:      "reconcile_total",
				Help:      "Total number of reconciliation cycles",
			},
		),

		// Operation latency metrics
		ApplyWorkloadDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "persys_agent",
				Subsystem: "workload",
				Name:      "apply_duration_seconds",
				Help:      "Time spent applying workloads",
				Buckets:   prometheus.DefBuckets,
			},
		),
		DeleteWorkloadDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "persys_agent",
				Subsystem: "workload",
				Name:      "delete_duration_seconds",
				Help:      "Time spent deleting workloads",
				Buckets:   prometheus.DefBuckets,
			},
		),
		ReconcileWorkloadDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "persys_agent",
				Subsystem: "workload",
				Name:      "reconcile_duration_seconds",
				Help:      "Time spent reconciling a workload",
				Buckets:   prometheus.DefBuckets,
			},
		),

		// Runtime health status (1 = healthy, 0 = unhealthy)
		RuntimeHealthStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "persys_agent",
				Subsystem: "runtime",
				Name:      "health_status",
				Help:      "Health status of each runtime (1 = healthy, 0 = unhealthy)",
			},
			[]string{"runtime_type"},
		),

		// System resource metrics
		SystemMemoryUtilization: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "persys_agent",
				Subsystem: "system",
				Name:      "memory_utilization_percent",
				Help:      "System memory utilization percentage",
			},
		),
		SystemCPUUtilization: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "persys_agent",
				Subsystem: "system",
				Name:      "cpu_utilization_percent",
				Help:      "System CPU utilization percentage",
			},
		),
		SystemDiskUtilization: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "persys_agent",
				Subsystem: "system",
				Name:      "disk_utilization_percent",
				Help:      "System disk utilization percentage by mount point",
			},
			[]string{"mount_point"},
		),

		// Error metrics by type
		ErrorCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "persys_agent",
				Subsystem: "errors",
				Name:      "total",
				Help:      "Total errors by error code",
			},
			[]string{"error_code"},
		),

		// Garbage collection metrics
		GCRunsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "persys_agent",
				Subsystem: "garbage_collection",
				Name:      "runs_total",
				Help:      "Total number of garbage collection cycles run",
			},
		),
		GCDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "persys_agent",
				Subsystem: "garbage_collection",
				Name:      "duration_seconds",
				Help:      "Time spent performing garbage collection",
				Buckets:   prometheus.DefBuckets,
			},
		),
		GCOrphanedResourcesFound: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "persys_agent",
				Subsystem: "garbage_collection",
				Name:      "orphaned_resources_found",
				Help:      "Number of orphaned resources found by type",
			},
			[]string{"resource_type"},
		),
		GCOldFailedWorkloadsFound: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "persys_agent",
				Subsystem: "garbage_collection",
				Name:      "old_failed_workloads_found",
				Help:      "Number of old failed workloads found for cleanup",
			},
		),
		GCResourcesDeleted: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "persys_agent",
				Subsystem: "garbage_collection",
				Name:      "resources_deleted_total",
				Help:      "Total resources deleted by garbage collection by type",
			},
			[]string{"resource_type"},
		),

		TaskQueueDepth: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "persys_agent",
				Subsystem: "task",
				Name:      "queue_depth",
				Help:      "Current number of tasks waiting in queue channel.",
			},
		),
		TaskLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "persys_agent",
				Subsystem: "task",
				Name:      "latency_seconds",
				Help:      "Task execution latency by task type and status.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"type", "status"},
		),
		TaskExecutions: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "persys_agent",
				Subsystem: "task",
				Name:      "executions_total",
				Help:      "Total task executions by type and final status.",
			},
			[]string{"type", "status"},
		),
		TaskFailuresTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "persys_agent",
				Subsystem: "task",
				Name:      "failures_total",
				Help:      "Total failed task executions by type.",
			},
			[]string{"type"},
		),
	}

	// Register all metrics
	prometheus.MustRegister(
		m.WorkloadCount,
		m.WorkloadCreatedTotal,
		m.WorkloadDeletedTotal,
		m.WorkloadFailedTotal,
		m.WorkloadReconcileTotal,
		m.ApplyWorkloadDuration,
		m.DeleteWorkloadDuration,
		m.ReconcileWorkloadDuration,
		m.RuntimeHealthStatus,
		m.SystemMemoryUtilization,
		m.SystemCPUUtilization,
		m.SystemDiskUtilization,
		m.ErrorCount,
		m.GCRunsTotal,
		m.GCDuration,
		m.GCOrphanedResourcesFound,
		m.GCOldFailedWorkloadsFound,
		m.GCResourcesDeleted,
		m.TaskQueueDepth,
		m.TaskLatency,
		m.TaskExecutions,
		m.TaskFailuresTotal,
	)

	logger.Info("Metrics initialized successfully")
	return m, nil
}

// Server provides HTTP endpoint for Prometheus metrics
type Server struct {
	address string
	logger  *logrus.Entry
	server  *http.Server
	metrics *Metrics
}

// NewServer creates a new metrics HTTP server
func NewServer(address string, logger *logrus.Logger, metrics *Metrics) *Server {
	return &Server{
		address: address,
		logger:  logger.WithField("component", "metrics-server"),
		metrics: metrics,
	}
}

// Start starts the metrics HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"healthy"}`)
	})
	mux.Handle("/debug/pprof/", http.DefaultServeMux)
	s.server = &http.Server{
		Addr:         s.address,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.logger.Infof("Starting metrics server on %s", s.address)

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Errorf("Metrics server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully stops the metrics server
func (s *Server) Stop() error {
	if s.server != nil {
		s.logger.Info("Stopping metrics server")
		return s.server.Close()
	}
	return nil
}

// RecordWorkloadCreated increments the workload created counter
func (m *Metrics) RecordWorkloadCreated() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WorkloadCreatedTotal.Inc()
}

// RecordWorkloadDeleted increments the workload deleted counter
func (m *Metrics) RecordWorkloadDeleted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WorkloadDeletedTotal.Inc()
}

// RecordWorkloadFailed increments the workload failed counter
func (m *Metrics) RecordWorkloadFailed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WorkloadFailedTotal.Inc()
}

// RecordWorkloadCount updates the workload count gauge
func (m *Metrics) RecordWorkloadCount(state, workloadType string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WorkloadCount.WithLabelValues(state, workloadType).Set(float64(count))
}

// RecordApplyWorkloadDuration records operation duration
func (m *Metrics) RecordApplyWorkloadDuration(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ApplyWorkloadDuration.Observe(duration.Seconds())
}

// RecordDeleteWorkloadDuration records operation duration
func (m *Metrics) RecordDeleteWorkloadDuration(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteWorkloadDuration.Observe(duration.Seconds())
}

// RecordReconcileWorkloadDuration records operation duration
func (m *Metrics) RecordReconcileWorkloadDuration(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReconcileWorkloadDuration.Observe(duration.Seconds())
	m.WorkloadReconcileTotal.Inc()
}

// RecordRuntimeHealth updates runtime health status
func (m *Metrics) RecordRuntimeHealth(runtimeType string, healthy bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := 0.0
	if healthy {
		status = 1.0
	}
	m.RuntimeHealthStatus.WithLabelValues(runtimeType).Set(status)
}

// RecordError increments error counter for error code
func (m *Metrics) RecordError(errorCode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ErrorCount.WithLabelValues(errorCode).Inc()
}

// UpdateSystemMetrics updates system resource utilization metrics
func (m *Metrics) UpdateSystemMetrics(memPercent, cpuPercent float64, diskPercents map[string]float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SystemMemoryUtilization.Set(memPercent)
	m.SystemCPUUtilization.Set(cpuPercent)
	for mountPoint, percent := range diskPercents {
		m.SystemDiskUtilization.WithLabelValues(mountPoint).Set(percent)
	}
}

// RecordGCRunStart records the start of a garbage collection cycle and returns a function to record completion
func (m *Metrics) RecordGCRunStart() func() {
	m.mu.Lock()
	m.GCRunsTotal.Inc()
	m.mu.Unlock()

	startTime := time.Now()
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.GCDuration.Observe(time.Since(startTime).Seconds())
	}
}

// RecordGCOrphanedResources records the count of orphaned resources found by type
func (m *Metrics) RecordGCOrphanedResources(resourceType string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GCOrphanedResourcesFound.WithLabelValues(resourceType).Set(float64(count))
}

// RecordGCOldFailedWorkloads records the count of old failed workloads found
func (m *Metrics) RecordGCOldFailedWorkloads(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GCOldFailedWorkloadsFound.Set(float64(count))
}

// RecordGCResourceDeleted records a resource deleted by garbage collection
func (m *Metrics) RecordGCResourceDeleted(resourceType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GCResourcesDeleted.WithLabelValues(resourceType).Inc()
}

// SetTaskQueueDepth updates async task queue depth.
func (m *Metrics) SetTaskQueueDepth(depth int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TaskQueueDepth.Set(float64(depth))
}

// ObserveTaskExecution records execution latency and counters.
func (m *Metrics) ObserveTaskExecution(taskType string, duration time.Duration, failed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := "completed"
	if failed {
		status = "failed"
		m.TaskFailuresTotal.WithLabelValues(taskType).Inc()
	}
	m.TaskExecutions.WithLabelValues(taskType, status).Inc()
	m.TaskLatency.WithLabelValues(taskType, status).Observe(duration.Seconds())
}
