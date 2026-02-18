package scheduler

import (
	"strings"

	metricspkg "github.com/persys-dev/persys-cloud/persys-scheduler/internal/metrics"
)

func normalizeLabel(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "unknown"
	}
	return v
}

// RefreshStateMetrics snapshots node/workload state into Prometheus gauges.
func (s *Scheduler) RefreshStateMetrics() error {
	nodes, err := s.GetNodes()
	if err != nil {
		return err
	}
	workloads, err := s.GetWorkloads()
	if err != nil {
		return err
	}

	nodeCounts := make(map[string]int)
	for _, n := range nodes {
		nodeCounts[normalizeLabel(n.Status)]++
	}

	workloadStatusCounts := make(map[string]int)
	workloadDesiredCounts := make(map[string]int)
	for _, w := range workloads {
		workloadStatusCounts[normalizeLabel(w.Status)]++
		workloadDesiredCounts[normalizeLabel(w.DesiredState)]++
	}

	metricspkg.SetNodeStatusCounts(nodeCounts)
	metricspkg.SetWorkloadStatusCounts(workloadStatusCounts, workloadDesiredCounts)
	return nil
}
