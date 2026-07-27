// Package metrics exposes Prometheus metrics for persys-meter itself, in the
// same "persys" namespace / manual CounterVec style already used by
// persys-scheduler's internal/metrics package, for consistency across
// services' dashboards.
package metrics

import (
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/persys-meter/internal/cache"
	"github.com/prometheus/client_golang/prometheus"
)

var registerOnce sync.Once

var (
	EventsConsumedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "meter",
			Name:      "events_consumed_total",
			Help:      "Total number of usage events read from the Redis stream via XREADGROUP.",
		},
		[]string{"stream"},
	)

	EventsDedupedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "meter",
			Name:      "events_deduped_total",
			Help:      "Total number of events skipped because their event_id was already seen (redelivery).",
		},
		[]string{"stream"},
	)

	EventsParseFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "meter",
			Name:      "events_parse_failed_total",
			Help:      "Total number of stream messages that could not be parsed into a usage record.",
		},
		[]string{"stream"},
	)

	EventsWrittenTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "meter",
			Name:      "events_written_total",
			Help:      "Total number of usage records successfully written to the store.",
		},
		[]string{"stream"},
	)

	BatchWriteFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "meter",
			Name:      "batch_write_failed_total",
			Help:      "Total number of store batch writes that failed (records are left un-acked for retry).",
		},
		[]string{"stream"},
	)

	MessagesReclaimedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "persys",
			Subsystem: "meter",
			Name:      "messages_reclaimed_total",
			Help:      "Total number of pending messages reclaimed from stalled consumers via XAUTOCLAIM.",
		},
		[]string{"stream"},
	)

	BatchFlushDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "persys",
			Subsystem: "meter",
			Name:      "batch_flush_duration_seconds",
			Help:      "Time taken to write a batch of usage records to the store.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"stream", "result"},
	)

	BatchSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "persys",
			Subsystem: "meter",
			Name:      "batch_size",
			Help:      "Number of records in each flushed batch.",
			Buckets:   []float64{1, 10, 50, 100, 250, 500, 1000, 2500, 5000},
		},
		[]string{"stream"},
	)

	ConsumerLagSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "persys",
			Subsystem: "meter",
			Name:      "consumer_lag_seconds",
			Help:      "Time between a usage event being reported and persys-meter processing it.",
			Buckets:   []float64{.1, .5, 1, 2, 5, 10, 30, 60, 300},
		},
		[]string{"stream"},
	)
)

// Register registers all meter metrics with the default Prometheus
// registry. Safe to call more than once.
func Register() {
	registerOnce.Do(func() {
		prometheus.MustRegister(
			EventsConsumedTotal,
			EventsDedupedTotal,
			EventsParseFailedTotal,
			EventsWrittenTotal,
			BatchWriteFailedTotal,
			MessagesReclaimedTotal,
			BatchFlushDuration,
			BatchSize,
			ConsumerLagSeconds,
		)
	})
}

// WorkloadCollector emits live, per-workload usage as Prometheus metrics -
// this is what makes actual workload metrics (not just meter-pipeline
// health metrics) available for scraping/dashboards/alerting. Implemented
// as a custom prometheus.Collector (rather than a persistent GaugeVec) so
// that a workload which stops reporting simply stops appearing in scrapes,
// instead of leaving a stale, never-cleaned-up series behind forever.
type WorkloadCollector struct {
	cache  *cache.LatestCache
	maxAge time.Duration

	cpuPercent     *prometheus.Desc
	memoryBytes    *prometheus.Desc
	diskReadBytes  *prometheus.Desc
	diskWriteBytes *prometheus.Desc
	netRXBytes     *prometheus.Desc
	netTXBytes     *prometheus.Desc
	sampleAge      *prometheus.Desc
}

// NewWorkloadCollector builds a collector backed directly by the shared
// in-memory cache. maxAge bounds how long a workload keeps appearing in
// scrapes after its last reported sample - see cache.LatestCache.All.
func NewWorkloadCollector(c *cache.LatestCache, maxAge time.Duration) *WorkloadCollector {
	labels := []string{"workload_id", "node_id", "workload_type"}
	return &WorkloadCollector{
		cache:  c,
		maxAge: maxAge,
		cpuPercent: prometheus.NewDesc(
			"persys_meter_workload_cpu_percent",
			"Latest observed CPU utilization percent for a workload.",
			labels, nil,
		),
		memoryBytes: prometheus.NewDesc(
			"persys_meter_workload_memory_bytes",
			"Latest observed resident memory usage in bytes for a workload.",
			labels, nil,
		),
		diskReadBytes: prometheus.NewDesc(
			"persys_meter_workload_disk_read_bytes_total",
			"Cumulative disk bytes read, as last reported by the workload's runtime (resets if the workload restarts).",
			labels, nil,
		),
		diskWriteBytes: prometheus.NewDesc(
			"persys_meter_workload_disk_write_bytes_total",
			"Cumulative disk bytes written, as last reported by the workload's runtime (resets if the workload restarts).",
			labels, nil,
		),
		netRXBytes: prometheus.NewDesc(
			"persys_meter_workload_net_rx_bytes_total",
			"Cumulative network bytes received, as last reported by the workload's runtime (resets if the workload restarts).",
			labels, nil,
		),
		netTXBytes: prometheus.NewDesc(
			"persys_meter_workload_net_tx_bytes_total",
			"Cumulative network bytes transmitted, as last reported by the workload's runtime (resets if the workload restarts).",
			labels, nil,
		),
		sampleAge: prometheus.NewDesc(
			"persys_meter_workload_sample_age_seconds",
			"Seconds since the last usage sample for a workload was received. A consistently high value means the workload's agent may have stopped reporting.",
			labels, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *WorkloadCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.cpuPercent
	ch <- c.memoryBytes
	ch <- c.diskReadBytes
	ch <- c.diskWriteBytes
	ch <- c.netRXBytes
	ch <- c.netTXBytes
	ch <- c.sampleAge
}

// Collect implements prometheus.Collector, emitting one set of metrics per
// currently-live workload on every scrape.
func (c *WorkloadCollector) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()
	for _, e := range c.cache.All(c.maxAge) {
		labels := []string{e.Record.WorkloadID, e.Record.NodeID, e.Record.WorkloadType}
		ch <- prometheus.MustNewConstMetric(c.cpuPercent, prometheus.GaugeValue, e.Record.CPUPercent, labels...)
		ch <- prometheus.MustNewConstMetric(c.memoryBytes, prometheus.GaugeValue, float64(e.Record.MemoryBytes), labels...)
		ch <- prometheus.MustNewConstMetric(c.diskReadBytes, prometheus.CounterValue, float64(e.Record.DiskReadBytes), labels...)
		ch <- prometheus.MustNewConstMetric(c.diskWriteBytes, prometheus.CounterValue, float64(e.Record.DiskWriteBytes), labels...)
		ch <- prometheus.MustNewConstMetric(c.netRXBytes, prometheus.CounterValue, float64(e.Record.NetRXBytes), labels...)
		ch <- prometheus.MustNewConstMetric(c.netTXBytes, prometheus.CounterValue, float64(e.Record.NetTXBytes), labels...)
		ch <- prometheus.MustNewConstMetric(c.sampleAge, prometheus.GaugeValue, now.Sub(e.UpdatedAt).Seconds(), labels...)
	}
}

// RegisterWorkloadCollector registers a WorkloadCollector with the default
// registry. Separate from Register() since it needs the cache instance,
// which isn't available at package-init time.
func RegisterWorkloadCollector(c *WorkloadCollector) {
	prometheus.MustRegister(c)
}
