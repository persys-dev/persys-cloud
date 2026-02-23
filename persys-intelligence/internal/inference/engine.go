package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
)

var (
	ErrUnavailable = errors.New("inference provider unavailable")
	ErrRateLimited = errors.New("inference rate limited")
	ErrCircuitOpen = errors.New("inference circuit breaker open")
)

type EngineConfig struct {
	Timeout          time.Duration
	MinInterval      time.Duration
	FailureThreshold int
	Cooldown         time.Duration
}

type Engine struct {
	provider Provider
	cfg      EngineConfig

	mu          sync.Mutex
	lastRequest time.Time
	failures    int
	openUntil   time.Time
}

type AuditMetadata struct {
	PromptHash   string
	ModelVersion string
}

func New(provider Provider, cfg EngineConfig) *Engine {
	return &Engine{
		provider: provider,
		cfg:      cfg,
	}
}

func (e *Engine) Infer(ctx context.Context, snapshot model.FeatureSnapshot) (model.LLMOutput, AuditMetadata, time.Duration, error) {
	e.mu.Lock()
	now := time.Now()
	if now.Before(e.openUntil) {
		e.mu.Unlock()
		return model.LLMOutput{}, AuditMetadata{}, 0, ErrCircuitOpen
	}
	if !e.lastRequest.IsZero() && now.Sub(e.lastRequest) < e.cfg.MinInterval {
		e.mu.Unlock()
		return model.LLMOutput{}, AuditMetadata{}, 0, ErrRateLimited
	}
	e.lastRequest = now
	e.mu.Unlock()

	runCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()

	start := time.Now()
	providerResult, err := e.provider.Infer(runCtx, snapshot)
	latency := time.Since(start)
	if err != nil {
		e.recordFailure()
		return model.LLMOutput{}, AuditMetadata{}, latency, err
	}

	output, err := parseStrictOutput(providerResult.OutputJSON)
	if err != nil {
		e.recordFailure()
		return model.LLMOutput{}, AuditMetadata{}, latency, err
	}
	e.recordSuccess()
	return output, AuditMetadata{
		PromptHash:   providerResult.PromptHash,
		ModelVersion: providerResult.ModelVersion,
	}, latency, nil
}

func (e *Engine) recordFailure() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failures++
	if e.failures >= e.cfg.FailureThreshold {
		e.openUntil = time.Now().Add(e.cfg.Cooldown)
	}
}

func (e *Engine) recordSuccess() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failures = 0
	e.openUntil = time.Time{}
}

func parseStrictOutput(raw []byte) (model.LLMOutput, error) {
	var out model.LLMOutput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return model.LLMOutput{}, fmt.Errorf("decode llm output: %w", err)
	}
	if out.TargetWorkload == "" {
		return model.LLMOutput{}, fmt.Errorf("target_workload is required")
	}
	switch out.RecommendationType {
	case model.RecommendationScale, model.RecommendationRollback, model.RecommendationMigrate, model.RecommendationNoop:
	default:
		return model.LLMOutput{}, fmt.Errorf("unsupported recommendation_type: %s", out.RecommendationType)
	}
	if out.Confidence < 0 || out.Confidence > 1 {
		return model.LLMOutput{}, fmt.Errorf("confidence must be in [0,1]")
	}
	if out.RiskScore < 0 || out.RiskScore > 1 {
		return model.LLMOutput{}, fmt.Errorf("risk_score must be in [0,1]")
	}
	if out.SuggestedParams == nil {
		out.SuggestedParams = map[string]any{}
	}
	return out, nil
}
