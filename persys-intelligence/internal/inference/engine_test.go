package inference

import (
	"context"
	"testing"
	"time"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
)

func TestEngineInferMockProvider(t *testing.T) {
	engine := New(MockProvider{}, EngineConfig{
		Timeout:          2 * time.Second,
		MinInterval:      0,
		FailureThreshold: 2,
		Cooldown:         time.Second,
	})

	out, _, _, err := engine.Infer(context.Background(), model.FeatureSnapshot{
		Workload:       "payments-api",
		CPU5mAvg:       90,
		CPU1hTrend:     "increasing",
		ErrorRateDelta: "+5%",
	})
	if err != nil {
		t.Fatalf("Infer returned error: %v", err)
	}
	if out.RecommendationType != model.RecommendationScale {
		t.Fatalf("expected scale recommendation, got %s", out.RecommendationType)
	}
	if out.TargetWorkload != "payments-api" {
		t.Fatalf("expected workload payments-api, got %s", out.TargetWorkload)
	}
}

func TestParseStrictOutputRejectsUnknownFields(t *testing.T) {
	raw := []byte(`{"recommendation_type":"noop","target_workload":"a","confidence":0.5,"risk_score":0.2,"reason_code":"stable","explanation":"ok","suggested_parameters":{},"unexpected":true}`)
	if _, err := parseStrictOutput(raw); err == nil {
		t.Fatalf("expected parseStrictOutput to fail on unknown field")
	}
}
