package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
)

type Analyzer interface {
	Analyze(ctx context.Context, req model.AIQueryRequest, state model.AIStateSnapshot) (model.AIQueryResponse, time.Duration, error)
}

type DisabledAnalyzer struct{}

func (DisabledAnalyzer) Analyze(context.Context, model.AIQueryRequest, model.AIStateSnapshot) (model.AIQueryResponse, time.Duration, error) {
	return model.AIQueryResponse{}, 0, ErrUnavailable
}

type MockAnalyzer struct{}

func (MockAnalyzer) Analyze(_ context.Context, _ model.AIQueryRequest, state model.AIStateSnapshot) (model.AIQueryResponse, time.Duration, error) {
	start := time.Now()
	resp := model.AIQueryResponse{
		Diagnosis:             "No critical issue detected from current structured state.",
		Confidence:            0.56,
		Impact:                "low",
		Evidence:              []string{"No severe pressure indicators in current snapshot."},
		RecommendedActions:    []string{"Continue monitoring and review trend changes."},
		RequiresHumanApproval: false,
		InsufficientData:      state.Insufficient,
		InferenceStatus:       "ok",
		StateSnapshot:         state,
	}

	if state.CPU5mAvg >= 90 && strings.EqualFold(state.CPU1hTrend, "increasing") {
		resp.Diagnosis = "Workload latency is likely driven by sustained CPU saturation."
		resp.Confidence = 0.81
		resp.Impact = "high"
		resp.Evidence = []string{
			fmt.Sprintf("CPU usage is %d%% with increasing trend.", state.CPU5mAvg),
			fmt.Sprintf("Node pressure reported as %s.", state.NodePressure),
		}
		resp.RecommendedActions = []string{
			"Scale workload replicas with policy gate checks.",
			"Move workload to less saturated nodes.",
			"Inspect noisy-neighbor pressure on assigned node.",
		}
		resp.RequiresHumanApproval = true
	}
	if state.RetryCount >= 3 || state.RestartCount >= 3 {
		resp.Diagnosis = "The workload appears unstable due to repeated retries/restarts."
		resp.Confidence = 0.74
		resp.Impact = "medium"
		resp.Evidence = []string{
			fmt.Sprintf("Retry count: %d.", state.RetryCount),
			fmt.Sprintf("Restart count: %d.", state.RestartCount),
		}
		resp.RecommendedActions = []string{
			"Inspect recent deployment changes and rollback candidates.",
			"Review startup health checks and dependency readiness.",
		}
		resp.RequiresHumanApproval = true
	}

	if state.Insufficient {
		resp.InsufficientData = true
		resp.Impact = "unknown"
		resp.RecommendedActions = append(resp.RecommendedActions, "Collect scheduler event history and recent workload status before action.")
	}
	return resp, time.Since(start), nil
}

type OpenAIAnalyzer struct {
	Endpoint   string
	APIKey     string
	Model      string
	HTTPClient *http.Client
	Timeout    time.Duration
}

func (a OpenAIAnalyzer) Analyze(ctx context.Context, req model.AIQueryRequest, state model.AIStateSnapshot) (model.AIQueryResponse, time.Duration, error) {
	if strings.TrimSpace(a.Endpoint) == "" || strings.TrimSpace(a.Model) == "" {
		return model.AIQueryResponse{}, 0, errors.New("analyzer endpoint/model are required")
	}
	client := a.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	payload, err := json.Marshal(map[string]any{
		"query": req.Query,
		"scope": req.ContextScope,
		"id":    req.ResourceID,
		"state": state,
	})
	if err != nil {
		return model.AIQueryResponse{}, 0, fmt.Errorf("marshal analysis payload: %w", err)
	}

	systemPrompt := "You are persys-intelligence read-only analyst. Use only provided structured state. " +
		"Return strict JSON with fields: diagnosis(string), confidence(0..1), impact(low|medium|high|unknown), " +
		"evidence(string[]), recommended_actions(string[]), requires_human_approval(boolean), insufficient_data(boolean)."
	reqBody := map[string]any{
		"model": a.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": string(payload)},
		},
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
	}
	rawReq, err := json.Marshal(reqBody)
	if err != nil {
		return model.AIQueryResponse{}, 0, fmt.Errorf("marshal analyzer request: %w", err)
	}

	runCtx := ctx
	cancel := func() {}
	if a.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, a.Timeout)
	}
	defer cancel()

	httpReq, err := http.NewRequestWithContext(runCtx, http.MethodPost, a.Endpoint, bytes.NewReader(rawReq))
	if err != nil {
		return model.AIQueryResponse{}, 0, fmt.Errorf("build analyzer request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(a.APIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.APIKey)
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	latency := time.Since(start)
	if err != nil {
		return model.AIQueryResponse{}, latency, fmt.Errorf("analyzer request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return model.AIQueryResponse{}, latency, fmt.Errorf("read analyzer response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.AIQueryResponse{}, latency, fmt.Errorf("analyzer endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return model.AIQueryResponse{}, latency, fmt.Errorf("decode analyzer response: %w", err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return model.AIQueryResponse{}, latency, errors.New("analyzer response missing choices[0].message.content")
	}

	out, err := parseStrictAnalysisOutput([]byte(parsed.Choices[0].Message.Content))
	if err != nil {
		return model.AIQueryResponse{}, latency, err
	}
	out.StateSnapshot = state
	out.InferenceStatus = "ok"
	return out, latency, nil
}

func NewAnalyzer(provider, endpoint, apiKey, modelName string, timeout time.Duration) Analyzer {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "disabled":
		return DisabledAnalyzer{}
	case "openai", "local", "fine-tuned":
		return OpenAIAnalyzer{
			Endpoint: endpoint,
			APIKey:   apiKey,
			Model:    modelName,
			Timeout:  timeout,
		}
	default:
		return MockAnalyzer{}
	}
}

func parseStrictAnalysisOutput(raw []byte) (model.AIQueryResponse, error) {
	var out model.AIQueryResponse
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return model.AIQueryResponse{}, fmt.Errorf("decode analysis output: %w", err)
	}
	out.Diagnosis = strings.TrimSpace(out.Diagnosis)
	if out.Diagnosis == "" {
		return model.AIQueryResponse{}, errors.New("diagnosis is required")
	}
	if out.Confidence < 0 || out.Confidence > 1 {
		return model.AIQueryResponse{}, errors.New("confidence must be in [0,1]")
	}
	switch strings.ToLower(strings.TrimSpace(out.Impact)) {
	case "low", "medium", "high", "unknown":
	default:
		return model.AIQueryResponse{}, fmt.Errorf("impact must be one of low|medium|high|unknown")
	}
	if out.Evidence == nil {
		out.Evidence = []string{}
	}
	if out.RecommendedActions == nil {
		out.RecommendedActions = []string{}
	}
	return out, nil
}
