package service

import (
	"context"
	"testing"
	"time"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/features"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/inference"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/store"
)

func TestEvaluatePolicyGatedAuto(t *testing.T) {
	svc := New(
		store.NewMemoryStore(),
		features.NewStaticExtractor("default"),
		inference.New(inference.MockProvider{}, inference.EngineConfig{
			Timeout:          time.Second,
			MinInterval:      0,
			FailureThreshold: 2,
			Cooldown:         time.Second,
		}),
		inference.MockAnalyzer{},
		metrics.New(),
		"policy-gated-auto",
		0.70,
		0.60,
	)

	result, err := svc.Evaluate(context.Background(), []model.FeatureSnapshot{
		{
			Workload:       "payments-api",
			CPU5mAvg:       92,
			CPU1hTrend:     "increasing",
			ErrorRateDelta: "+10%",
			RecentDeploy:   false,
			RetryCount:     2,
			NodePressure:   "normal",
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if len(result.Recommendations) != 1 {
		t.Fatalf("expected one recommendation, got %d", len(result.Recommendations))
	}
	if result.Recommendations[0].Status != model.StatusApproved {
		t.Fatalf("expected approved recommendation, got %s", result.Recommendations[0].Status)
	}
	if result.InferenceStatus != "ok" {
		t.Fatalf("expected inference status ok, got %s", result.InferenceStatus)
	}
	if result.Recommendations[0].DecisionOutcome != "approved_by_policy" {
		t.Fatalf("expected approved_by_policy decision outcome, got %s", result.Recommendations[0].DecisionOutcome)
	}
}

func TestQueryReturnsStructuredResponse(t *testing.T) {
	svc := New(
		store.NewMemoryStore(),
		features.NewStaticExtractor("demo-c2"),
		inference.New(inference.MockProvider{}, inference.EngineConfig{
			Timeout:          time.Second,
			MinInterval:      0,
			FailureThreshold: 2,
			Cooldown:         time.Second,
		}),
		inference.MockAnalyzer{},
		metrics.New(),
		"advisory",
		0.7,
		0.6,
	)

	resp, err := svc.Query(context.Background(), model.AIQueryRequest{
		Query:        "why is workload demo-c2 slow?",
		ContextScope: model.AIContextWorkload,
		ResourceID:   "demo-c2",
	})
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}
	if resp.Diagnosis == "" {
		t.Fatalf("expected diagnosis")
	}
	if resp.StateSnapshot.ResourceID == "" {
		t.Fatalf("expected state snapshot resource id")
	}
	if resp.InferenceStatus == "" {
		t.Fatalf("expected inference status")
	}
}
