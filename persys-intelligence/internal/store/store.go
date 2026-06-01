package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
)

var ErrRecommendationNotFound = errors.New("recommendation not found")
var ErrInvalidTransition = errors.New("invalid status transition")

type Store interface {
	Create(ctx context.Context, rec model.Recommendation) (model.Recommendation, error)
	Get(ctx context.Context, id string) (model.Recommendation, error)
	List(ctx context.Context, status string, workload string) ([]model.Recommendation, error)
	UpdateStatus(ctx context.Context, id string, status model.RecommendationStatus) (model.Recommendation, error)
	Replace(ctx context.Context, rec model.Recommendation) (model.Recommendation, error)
}

type MemoryStore struct {
	mu      sync.RWMutex
	records map[string]model.Recommendation
	order   []string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: make(map[string]model.Recommendation),
		order:   make([]string, 0, 256),
	}
}

func (s *MemoryStore) Create(_ context.Context, rec model.Recommendation) (model.Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	rec.ID = newID()
	rec.CreatedAt = now
	rec.UpdatedAt = now
	if rec.Parameters == nil {
		rec.Parameters = map[string]any{}
	}
	s.records[rec.ID] = cloneRecommendation(rec)
	s.order = append(s.order, rec.ID)
	return cloneRecommendation(rec), nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (model.Recommendation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	if !ok {
		return model.Recommendation{}, ErrRecommendationNotFound
	}
	return cloneRecommendation(rec), nil
}

func (s *MemoryStore) List(_ context.Context, status string, workload string) ([]model.Recommendation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]model.Recommendation, 0, len(s.records))
	for i := len(s.order) - 1; i >= 0; i-- {
		id := s.order[i]
		rec := s.records[id]
		if status != "" && string(rec.Status) != status {
			continue
		}
		if workload != "" && rec.Workload != workload {
			continue
		}
		out = append(out, cloneRecommendation(rec))
	}
	return out, nil
}

func (s *MemoryStore) UpdateStatus(_ context.Context, id string, status model.RecommendationStatus) (model.Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[id]
	if !ok {
		return model.Recommendation{}, ErrRecommendationNotFound
	}
	if !isAllowedTransition(rec.Status, status) {
		return model.Recommendation{}, ErrInvalidTransition
	}
	rec.Status = status
	rec.UpdatedAt = time.Now().UTC()
	s.records[id] = rec
	return cloneRecommendation(rec), nil
}

func (s *MemoryStore) Replace(_ context.Context, rec model.Recommendation) (model.Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.records[rec.ID]; !ok {
		return model.Recommendation{}, ErrRecommendationNotFound
	}
	rec.UpdatedAt = time.Now().UTC()
	s.records[rec.ID] = cloneRecommendation(rec)
	return cloneRecommendation(rec), nil
}

func cloneRecommendation(in model.Recommendation) model.Recommendation {
	out := in
	if in.Parameters != nil {
		out.Parameters = make(map[string]any, len(in.Parameters))
		for k, v := range in.Parameters {
			out.Parameters[k] = v
		}
	}
	return out
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf)
}

func IsTerminal(status model.RecommendationStatus) bool {
	return slices.Contains([]model.RecommendationStatus{model.StatusRejected, model.StatusApplied}, status)
}

func isAllowedTransition(from model.RecommendationStatus, to model.RecommendationStatus) bool {
	switch from {
	case model.StatusPending:
		return to == model.StatusApproved || to == model.StatusRejected
	case model.StatusApproved:
		return to == model.StatusApplied || to == model.StatusRejected
	case model.StatusRejected, model.StatusApplied:
		return false
	default:
		return false
	}
}
