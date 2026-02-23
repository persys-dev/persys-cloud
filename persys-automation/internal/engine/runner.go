package engine

import (
	"context"
	"log"
	"time"
)

func StartPeriodicEvaluation(ctx context.Context, eng *Engine, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			results := eng.EvaluatePolicies(ctx, "")
			eng.ReplayQueuedSuggestions(ctx, 50)
			for _, r := range results {
				if r.GetMatched() {
					log.Printf("policy=%s matched dispatched=%t reason=%s", r.GetPolicyId(), r.GetDispatched(), r.GetReason())
				}
			}
		}
	}
}
