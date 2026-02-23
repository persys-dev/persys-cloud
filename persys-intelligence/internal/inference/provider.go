package inference

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
)

type Provider interface {
	Infer(ctx context.Context, snapshot model.FeatureSnapshot) (ProviderResult, error)
}

type ProviderResult struct {
	OutputJSON   []byte
	PromptHash   string
	ModelVersion string
}

type DisabledProvider struct{}

func (DisabledProvider) Infer(context.Context, model.FeatureSnapshot) (ProviderResult, error) {
	return ProviderResult{}, ErrUnavailable
}

type MockProvider struct{}

func (MockProvider) Infer(_ context.Context, snapshot model.FeatureSnapshot) (ProviderResult, error) {
	out := model.LLMOutput{
		RecommendationType: model.RecommendationNoop,
		TargetWorkload:     snapshot.Workload,
		Confidence:         0.55,
		RiskScore:          0.20,
		ReasonCode:         "stable_state",
		Explanation:        "Signals are stable and no change is recommended.",
		SuggestedParams:    map[string]any{},
	}

	if snapshot.RecentDeploy && parsePercent(snapshot.ErrorRateDelta) > 80 {
		out.RecommendationType = model.RecommendationRollback
		out.Confidence = 0.90
		out.RiskScore = 0.35
		out.ReasonCode = "deploy_regression"
		out.Explanation = "Error rates rose sharply after a recent deployment."
	}
	if strings.EqualFold(snapshot.CPU1hTrend, "increasing") && snapshot.CPU5mAvg >= 80 {
		out.RecommendationType = model.RecommendationScale
		out.Confidence = 0.82
		out.RiskScore = 0.42
		out.ReasonCode = "sustained_cpu_pressure"
		out.Explanation = "CPU has sustained high usage with an upward trend."
		out.SuggestedParams = map[string]any{"replica_delta": 2}
	}
	if strings.EqualFold(snapshot.NodePressure, "high") {
		out.RecommendationType = model.RecommendationMigrate
		out.Confidence = 0.75
		out.RiskScore = 0.55
		out.ReasonCode = "node_pressure_high"
		out.Explanation = "Node pressure is high and workload relocation is suggested."
		out.SuggestedParams = map[string]any{"strategy": "least-loaded-node"}
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return ProviderResult{}, err
	}
	return ProviderResult{
		OutputJSON:   raw,
		PromptHash:   hashPrompt(snapshot),
		ModelVersion: "mock-v1",
	}, nil
}

func parsePercent(v string) int {
	clean := strings.TrimSpace(strings.TrimSuffix(v, "%"))
	signless := strings.TrimLeft(clean, "+")
	if signless == "" {
		return 0
	}
	n := 0
	for i := 0; i < len(signless); i++ {
		ch := signless[i]
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

type OpenAICompatibleProvider struct {
	Endpoint   string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

func (p OpenAICompatibleProvider) Infer(ctx context.Context, snapshot model.FeatureSnapshot) (ProviderResult, error) {
	if strings.TrimSpace(p.Endpoint) == "" {
		return ProviderResult{}, errors.New("model endpoint is required")
	}
	if strings.TrimSpace(p.Model) == "" {
		return ProviderResult{}, errors.New("model name is required")
	}

	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	snapJSON, err := json.Marshal(snapshot)
	if err != nil {
		return ProviderResult{}, fmt.Errorf("marshal snapshot: %w", err)
	}
	systemPrompt := "You are persys-intelligence. Return only valid JSON matching this exact shape: " +
		`{"recommendation_type":"scale|rollback|migrate|noop","target_workload":"string","confidence":0.0,"risk_score":0.0,"reason_code":"string","explanation":"string","suggested_parameters":{}}. ` +
		"Do not include markdown."

	reqBody := map[string]any{
		"model": p.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": string(snapJSON)},
		},
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
	}
	rawReq, err := json.Marshal(reqBody)
	if err != nil {
		return ProviderResult{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, strings.NewReader(string(rawReq)))
	if err != nil {
		return ProviderResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ProviderResult{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("model endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return ProviderResult{}, fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return ProviderResult{}, errors.New("model response missing choices[0].message.content")
	}

	return ProviderResult{
		OutputJSON:   []byte(parsed.Choices[0].Message.Content),
		PromptHash:   hashPrompt(snapshot),
		ModelVersion: p.Model,
	}, nil
}

func hashPrompt(snapshot model.FeatureSnapshot) string {
	payload, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:])
}
