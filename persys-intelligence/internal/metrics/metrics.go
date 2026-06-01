package metrics

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Collector struct {
	recommendationsTotal        atomic.Uint64
	recommendationsAppliedTotal atomic.Uint64
	recommendationsRejected     atomic.Uint64
	inferenceFailures           atomic.Uint64

	mu                 sync.Mutex
	inferenceLatencyNS uint64
	inferenceCalls     uint64
}

func New() *Collector {
	return &Collector{}
}

func (c *Collector) IncRecommendations() {
	c.recommendationsTotal.Add(1)
}

func (c *Collector) IncApplied() {
	c.recommendationsAppliedTotal.Add(1)
}

func (c *Collector) IncRejected() {
	c.recommendationsRejected.Add(1)
}

func (c *Collector) IncInferenceFailures() {
	c.inferenceFailures.Add(1)
}

func (c *Collector) ObserveInferenceLatency(d time.Duration) {
	c.mu.Lock()
	c.inferenceLatencyNS += uint64(d.Nanoseconds())
	c.inferenceCalls++
	c.mu.Unlock()
}

func (c *Collector) RenderPrometheus() string {
	c.mu.Lock()
	latencySum := c.inferenceLatencyNS
	latencyCount := c.inferenceCalls
	c.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "recommendations_total %d\n", c.recommendationsTotal.Load())
	fmt.Fprintf(&b, "recommendations_applied_total %d\n", c.recommendationsAppliedTotal.Load())
	fmt.Fprintf(&b, "recommendations_rejected_total %d\n", c.recommendationsRejected.Load())
	fmt.Fprintf(&b, "inference_failures %d\n", c.inferenceFailures.Load())
	fmt.Fprintf(&b, "inference_latency_seconds_sum %.9f\n", float64(latencySum)/float64(time.Second))
	fmt.Fprintf(&b, "inference_latency_seconds_count %d\n", latencyCount)
	return b.String()
}
