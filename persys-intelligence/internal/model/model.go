package model

import "time"

type RecommendationType string

const (
	RecommendationScale    RecommendationType = "scale"
	RecommendationRollback RecommendationType = "rollback"
	RecommendationMigrate  RecommendationType = "migrate"
	RecommendationNoop     RecommendationType = "noop"
)

type RecommendationStatus string

const (
	StatusPending  RecommendationStatus = "pending"
	StatusApproved RecommendationStatus = "approved"
	StatusRejected RecommendationStatus = "rejected"
	StatusApplied  RecommendationStatus = "applied"
)

type AIContextScope string

const (
	AIContextCluster  AIContextScope = "cluster"
	AIContextWorkload AIContextScope = "workload"
	AIContextVM       AIContextScope = "vm"
)

type FeatureSnapshot struct {
	Workload        string `json:"workload"`
	CPU5mAvg        int32  `json:"cpu_5m_avg"`
	CPU1hTrend      string `json:"cpu_1h_trend"`
	ErrorRateDelta  string `json:"error_rate_delta"`
	RecentDeploy    bool   `json:"recent_deploy"`
	RetryCount      int32  `json:"retry_count"`
	NodePressure    string `json:"node_pressure"`
	GeneratedSource string `json:"generated_source,omitempty"`
}

type LLMOutput struct {
	RecommendationType RecommendationType `json:"recommendation_type"`
	TargetWorkload     string             `json:"target_workload"`
	Confidence         float64            `json:"confidence"`
	RiskScore          float64            `json:"risk_score"`
	ReasonCode         string             `json:"reason_code"`
	Explanation        string             `json:"explanation"`
	SuggestedParams    map[string]any     `json:"suggested_parameters"`
}

type AIQueryRequest struct {
	Query        string         `json:"query"`
	ContextScope AIContextScope `json:"context_scope"`
	ResourceID   string         `json:"resource_id,omitempty"`
}

type AIStateSnapshot struct {
	ResourceID   string   `json:"resource_id"`
	ContextScope string   `json:"context_scope"`
	Workload     string   `json:"workload,omitempty"`
	Node         string   `json:"node,omitempty"`
	DesiredState string   `json:"desired_state,omitempty"`
	CPU5mAvg     int32    `json:"cpu_5m_avg,omitempty"`
	CPU1hTrend   string   `json:"cpu_1h_trend,omitempty"`
	MemoryUsage  int32    `json:"memory_usage,omitempty"`
	RetryCount   int32    `json:"retry_count,omitempty"`
	RestartCount int32    `json:"restart_count,omitempty"`
	NodePressure string   `json:"node_pressure,omitempty"`
	RecentEvents []string `json:"recent_events,omitempty"`
	DataSources  []string `json:"data_sources,omitempty"`
	Insufficient bool     `json:"insufficient_data"`
	GeneratedAt  string   `json:"generated_at"`
}

type AIQueryResponse struct {
	Diagnosis             string          `json:"diagnosis"`
	Confidence            float64         `json:"confidence"`
	Impact                string          `json:"impact"`
	Evidence              []string        `json:"evidence"`
	RecommendedActions    []string        `json:"recommended_actions"`
	RequiresHumanApproval bool            `json:"requires_human_approval"`
	InsufficientData      bool            `json:"insufficient_data"`
	InferenceStatus       string          `json:"inference_status"`
	StateSnapshot         AIStateSnapshot `json:"state_snapshot"`
}

type Recommendation struct {
	ID              string               `json:"id"`
	Workload        string               `json:"workload"`
	Type            RecommendationType   `json:"type"`
	Confidence      float64              `json:"confidence"`
	RiskScore       float64              `json:"risk_score"`
	ReasonCode      string               `json:"reason_code,omitempty"`
	Explanation     string               `json:"explanation"`
	Parameters      map[string]any       `json:"parameters"`
	Status          RecommendationStatus `json:"status"`
	InputSnapshot   FeatureSnapshot      `json:"input_snapshot"`
	PromptHash      string               `json:"prompt_hash,omitempty"`
	ModelVersion    string               `json:"model_version,omitempty"`
	DecisionOutcome string               `json:"decision_outcome,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}
