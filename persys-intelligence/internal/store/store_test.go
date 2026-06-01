package store

import (
	"context"
	"errors"
	"testing"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
)

func TestUpdateStatusRejectsInvalidTransition(t *testing.T) {
	s := NewMemoryStore()
	rec, err := s.Create(context.Background(), model.Recommendation{
		Workload:   "payments-api",
		Type:       model.RecommendationScale,
		Confidence: 0.9,
		RiskScore:  0.2,
		Status:     model.StatusPending,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := s.UpdateStatus(context.Background(), rec.ID, model.StatusApplied); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}
