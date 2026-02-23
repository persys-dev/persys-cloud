package store

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	automationv1 "github.com/persys-dev/persys-cloud/persys-automation/internal/automationv1"
)

var ErrPolicyNotFound = errors.New("policy not found")

type PolicyStore interface {
	CreatePolicy(ctx context.Context, req *automationv1.CreatePolicyRequest) (*automationv1.Policy, error)
	ListPolicies(ctx context.Context, includeDisabled bool) ([]*automationv1.Policy, error)
	GetPolicy(ctx context.Context, id string) (*automationv1.Policy, error)
	SetPolicyEnabled(ctx context.Context, id string, enabled bool) (*automationv1.Policy, error)
	AppendAudit(ctx context.Context, entry AuditEntry) error
	ListAudit(ctx context.Context, limit uint32) ([]AuditEntry, error)
	EnqueueSuggestion(ctx context.Context, item QueuedSuggestion) error
	ClaimQueuedSuggestions(ctx context.Context, limit int) ([]QueuedSuggestion, error)
	MarkQueuedSuggestionResult(ctx context.Context, id string, success bool, reason string) error
	Close() error
}

type AuditEntry struct {
	ID             string
	PolicyID       string
	PolicyName     string
	TargetWorkload string
	Matched        bool
	Dispatched     bool
	Reason         string
	OldState       string
	NewState       string
	Timestamp      time.Time
}

type QueuedSuggestion struct {
	ID              string
	PolicyID        string
	PolicyName      string
	TargetWorkload  string
	ActionType      string
	DesiredState    string
	DesiredReplicas int32
	ReplicaDelta    int32
	Reason          string
	Attempts        int32
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type MemoryStore struct {
	mu       sync.RWMutex
	policies map[string]*automationv1.Policy
	audit    []AuditEntry
	queue    []QueuedSuggestion
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		policies: make(map[string]*automationv1.Policy),
		audit:    make([]AuditEntry, 0, 1024),
		queue:    make([]QueuedSuggestion, 0, 1024),
	}
}

func (s *MemoryStore) CreatePolicy(_ context.Context, req *automationv1.CreatePolicyRequest) (*automationv1.Policy, error) {
	now := time.Now().UTC()
	policy := &automationv1.Policy{
		Id:                  uuid.NewString(),
		Name:                req.GetName(),
		TargetWorkload:      req.GetTargetWorkload(),
		Type:                req.GetType(),
		ConditionExpression: req.GetConditionExpression(),
		ActionExpression:    req.GetActionExpression(),
		Enabled:             true,
		CreatedAt:           toProtoTime(now),
		UpdatedAt:           toProtoTime(now),
	}
	s.mu.Lock()
	s.policies[policy.GetId()] = clonePolicy(policy)
	s.mu.Unlock()
	return policy, nil
}

func (s *MemoryStore) ListPolicies(_ context.Context, includeDisabled bool) ([]*automationv1.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*automationv1.Policy, 0, len(s.policies))
	for _, p := range s.policies {
		if !includeDisabled && !p.GetEnabled() {
			continue
		}
		out = append(out, clonePolicy(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetCreatedAt().AsTime().Before(out[j].GetCreatedAt().AsTime()) })
	return out, nil
}

func (s *MemoryStore) GetPolicy(_ context.Context, id string) (*automationv1.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.policies[id]
	if !ok {
		return nil, ErrPolicyNotFound
	}
	return clonePolicy(p), nil
}

func (s *MemoryStore) SetPolicyEnabled(_ context.Context, id string, enabled bool) (*automationv1.Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.policies[id]
	if !ok {
		return nil, ErrPolicyNotFound
	}
	p.Enabled = enabled
	p.UpdatedAt = toProtoTime(time.Now().UTC())
	return clonePolicy(p), nil
}

func (s *MemoryStore) AppendAudit(_ context.Context, entry AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.ID = uuid.NewString()
	entry.Timestamp = time.Now().UTC()
	s.audit = append(s.audit, entry)
	if len(s.audit) > 5000 {
		s.audit = s.audit[len(s.audit)-5000:]
	}
	return nil
}

func (s *MemoryStore) ListAudit(_ context.Context, limit uint32) ([]AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit == 0 || int(limit) > len(s.audit) {
		limit = uint32(len(s.audit))
	}
	start := len(s.audit) - int(limit)
	if start < 0 {
		start = 0
	}
	out := make([]AuditEntry, 0, limit)
	for i := len(s.audit) - 1; i >= start; i-- {
		out = append(out, s.audit[i])
	}
	return out, nil
}

func (s *MemoryStore) Close() error { return nil }

func (s *MemoryStore) EnqueueSuggestion(_ context.Context, item QueuedSuggestion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	item.ID = uuid.NewString()
	item.CreatedAt = now
	item.UpdatedAt = now
	s.queue = append(s.queue, item)
	return nil
}

func (s *MemoryStore) ClaimQueuedSuggestions(_ context.Context, limit int) ([]QueuedSuggestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.queue) {
		limit = len(s.queue)
	}
	out := make([]QueuedSuggestion, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, s.queue[i])
	}
	return out, nil
}

func (s *MemoryStore) MarkQueuedSuggestionResult(_ context.Context, id string, success bool, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i := range s.queue {
		if s.queue[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	if success {
		s.queue = append(s.queue[:idx], s.queue[idx+1:]...)
		return nil
	}
	s.queue[idx].Attempts++
	s.queue[idx].LastError = reason
	s.queue[idx].UpdatedAt = time.Now().UTC()
	return nil
}

func clonePolicy(in *automationv1.Policy) *automationv1.Policy {
	if in == nil {
		return nil
	}
	cp := *in
	return &cp
}
