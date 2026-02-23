package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/features"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/inference"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/store"
)

type Service struct {
	store     store.Store
	extractor features.Extractor
	infer     *inference.Engine
	analyzer  inference.Analyzer
	metrics   *metrics.Collector

	mode          string
	minConfidence float64
	maxRisk       float64
}

type EvaluateResult struct {
	Recommendations []model.Recommendation `json:"recommendations"`
	Generated       int                    `json:"generated"`
	InferenceStatus string                 `json:"inference_status"`
}

func New(
	store store.Store,
	extractor features.Extractor,
	infer *inference.Engine,
	analyzer inference.Analyzer,
	metrics *metrics.Collector,
	mode string,
	minConfidence float64,
	maxRisk float64,
) *Service {
	return &Service{
		store:         store,
		extractor:     extractor,
		infer:         infer,
		analyzer:      analyzer,
		metrics:       metrics,
		mode:          mode,
		minConfidence: minConfidence,
		maxRisk:       maxRisk,
	}
}

func (s *Service) Query(ctx context.Context, req model.AIQueryRequest) (model.AIQueryResponse, error) {
	req.Query = strings.TrimSpace(req.Query)
	req.ResourceID = strings.TrimSpace(req.ResourceID)
	req.ContextScope = model.AIContextScope(strings.ToLower(strings.TrimSpace(string(req.ContextScope))))
	if req.Query == "" {
		return model.AIQueryResponse{}, fmt.Errorf("query is required")
	}
	switch req.ContextScope {
	case model.AIContextCluster, model.AIContextWorkload, model.AIContextVM:
	default:
		req.ContextScope = model.AIContextCluster
	}

	state, err := s.buildStateSnapshot(ctx, req)
	if err != nil {
		return model.AIQueryResponse{}, err
	}

	resp, latency, err := s.analyzer.Analyze(ctx, req, state)
	s.metrics.ObserveInferenceLatency(latency)
	if err != nil {
		s.metrics.IncInferenceFailures()
		if errors.Is(err, inference.ErrUnavailable) {
			return model.AIQueryResponse{
				Diagnosis:             "Intelligence inference is currently unavailable.",
				Confidence:            0,
				Impact:                "unknown",
				Evidence:              []string{"Model provider is unavailable."},
				RecommendedActions:    []string{"Retry later or rely on deterministic automation policies."},
				RequiresHumanApproval: false,
				InsufficientData:      true,
				InferenceStatus:       "unavailable",
				StateSnapshot:         state,
			}, nil
		}
		return model.AIQueryResponse{}, err
	}
	return resp, nil
}

func (s *Service) Evaluate(ctx context.Context, snapshots []model.FeatureSnapshot) (EvaluateResult, error) {
	if len(snapshots) == 0 {
		extracted, err := s.extractor.Extract(ctx)
		if err != nil {
			return EvaluateResult{}, err
		}
		snapshots = extracted
	}

	recs := make([]model.Recommendation, 0, len(snapshots))
	unavailableCount := 0
	failedCount := 0
	for _, snap := range snapshots {
		sanitized := sanitizeSnapshot(snap)
		out, audit, latency, err := s.infer.Infer(ctx, sanitized)
		s.metrics.ObserveInferenceLatency(latency)
		if err != nil {
			s.metrics.IncInferenceFailures()
			if errors.Is(err, inference.ErrUnavailable) {
				unavailableCount++
				continue
			}
			failedCount++
			continue
		}
		status := model.StatusPending
		decisionOutcome := "pending_review"
		if s.mode == "policy-gated-auto" {
			allowed, reason := s.policyAllow(out)
			if allowed {
				status = model.StatusApproved
				out.Explanation = strings.TrimSpace(out.Explanation + " Policy gate: approved.")
				decisionOutcome = "approved_by_policy"
			} else {
				status = model.StatusRejected
				out.Explanation = strings.TrimSpace(out.Explanation + " Policy gate: rejected - " + reason + ".")
				decisionOutcome = "rejected_by_policy: " + reason
				s.metrics.IncRejected()
			}
		}

		rec, err := s.store.Create(ctx, model.Recommendation{
			Workload:        out.TargetWorkload,
			Type:            out.RecommendationType,
			Confidence:      out.Confidence,
			RiskScore:       out.RiskScore,
			ReasonCode:      out.ReasonCode,
			Explanation:     out.Explanation,
			Parameters:      out.SuggestedParams,
			Status:          status,
			InputSnapshot:   sanitized,
			PromptHash:      audit.PromptHash,
			ModelVersion:    audit.ModelVersion,
			DecisionOutcome: decisionOutcome,
		})
		if err != nil {
			return EvaluateResult{}, err
		}
		s.metrics.IncRecommendations()
		recs = append(recs, rec)
	}

	status := "ok"
	switch {
	case len(recs) == 0 && unavailableCount > 0 && failedCount == 0:
		status = "unavailable"
	case len(recs) > 0 && (unavailableCount > 0 || failedCount > 0):
		status = "partial"
	case len(recs) == 0 && failedCount > 0:
		status = "degraded"
	}

	return EvaluateResult{
		Recommendations: recs,
		Generated:       len(recs),
		InferenceStatus: status,
	}, nil
}

func (s *Service) ListRecommendations(ctx context.Context, status string, workload string) ([]model.Recommendation, error) {
	return s.store.List(ctx, status, workload)
}

func (s *Service) ApproveRecommendation(ctx context.Context, id string) (model.Recommendation, error) {
	rec, err := s.store.UpdateStatus(ctx, id, model.StatusApproved)
	if err != nil {
		return model.Recommendation{}, err
	}
	rec.DecisionOutcome = "approved_manually"
	return s.store.Replace(ctx, rec)
}

func (s *Service) RejectRecommendation(ctx context.Context, id string) (model.Recommendation, error) {
	rec, err := s.store.UpdateStatus(ctx, id, model.StatusRejected)
	if err != nil {
		return model.Recommendation{}, err
	}
	s.metrics.IncRejected()
	rec.DecisionOutcome = "rejected_manually"
	return s.store.Replace(ctx, rec)
}

func (s *Service) ApplyRecommendation(ctx context.Context, id string) (model.Recommendation, error) {
	rec, err := s.store.UpdateStatus(ctx, id, model.StatusApplied)
	if err != nil {
		return model.Recommendation{}, err
	}
	s.metrics.IncApplied()
	rec.DecisionOutcome = "applied"
	return s.store.Replace(ctx, rec)
}

func sanitizeSnapshot(in model.FeatureSnapshot) model.FeatureSnapshot {
	out := in
	out.Workload = strings.TrimSpace(out.Workload)
	if out.Workload == "" {
		out.Workload = "unknown-workload"
	}
	out.CPU1hTrend = strings.ToLower(strings.TrimSpace(out.CPU1hTrend))
	out.NodePressure = strings.ToLower(strings.TrimSpace(out.NodePressure))
	out.ErrorRateDelta = strings.TrimSpace(out.ErrorRateDelta)
	return out
}

func (s *Service) policyAllow(out model.LLMOutput) (bool, string) {
	if out.Confidence < s.minConfidence {
		return false, fmt.Sprintf("confidence %.2f < threshold %.2f", out.Confidence, s.minConfidence)
	}
	if out.RiskScore > s.maxRisk {
		return false, fmt.Sprintf("risk %.2f > threshold %.2f", out.RiskScore, s.maxRisk)
	}
	if out.RecommendationType == model.RecommendationNoop {
		return false, "noop recommendation"
	}
	return true, ""
}

func (s *Service) buildStateSnapshot(ctx context.Context, req model.AIQueryRequest) (model.AIStateSnapshot, error) {
	snaps, err := s.extractor.Extract(ctx)
	if err != nil {
		return model.AIStateSnapshot{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	out := model.AIStateSnapshot{
		ResourceID:   req.ResourceID,
		ContextScope: string(req.ContextScope),
		RecentEvents: []string{"event history integration pending"},
		DataSources:  []string{"feature-extractor"},
		GeneratedAt:  now,
		Insufficient: true,
	}

	if len(snaps) == 0 {
		return out, nil
	}

	pick := sanitizeSnapshot(snaps[0])
	if req.ResourceID != "" {
		for _, candidate := range snaps {
			candidate = sanitizeSnapshot(candidate)
			if strings.EqualFold(candidate.Workload, req.ResourceID) {
				pick = candidate
				break
			}
		}
	}
	out.ResourceID = pick.Workload
	out.Workload = pick.Workload
	out.CPU5mAvg = pick.CPU5mAvg
	out.CPU1hTrend = pick.CPU1hTrend
	out.NodePressure = pick.NodePressure
	out.RetryCount = pick.RetryCount
	if pick.RetryCount > 0 {
		out.RestartCount = pick.RetryCount / 2
	}
	out.DesiredState = "running"
	out.Insufficient = false
	return out, nil
}
