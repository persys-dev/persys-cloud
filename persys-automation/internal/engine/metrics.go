package engine

import (
	"fmt"
	"sync/atomic"

	automationv1 "github.com/persys-dev/persys-cloud/persys-automation/internal/automationv1"
)

var (
	evaluationsTotal uint64
	matchesTotal     uint64
	dispatchesTotal  uint64
)

func recordEvaluationMetric(_ automationv1.PolicyType, matched, dispatched bool) {
	atomic.AddUint64(&evaluationsTotal, 1)
	if matched {
		atomic.AddUint64(&matchesTotal, 1)
	}
	if dispatched {
		atomic.AddUint64(&dispatchesTotal, 1)
	}
}

func MetricsText() string {
	evals := atomic.LoadUint64(&evaluationsTotal)
	matches := atomic.LoadUint64(&matchesTotal)
	dispatches := atomic.LoadUint64(&dispatchesTotal)

	return fmt.Sprintf(
		"# TYPE persys_automation_policy_evaluations_total counter\n"+
			"persys_automation_policy_evaluations_total %d\n"+
			"# TYPE persys_automation_policy_matches_total counter\n"+
			"persys_automation_policy_matches_total %d\n"+
			"# TYPE persys_automation_policy_dispatches_total counter\n"+
			"persys_automation_policy_dispatches_total %d\n",
		evals, matches, dispatches,
	)
}
