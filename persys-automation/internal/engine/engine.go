package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	automationv1 "github.com/persys-dev/persys-cloud/persys-automation/internal/automationv1"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/prometheus"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/scheduler"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/store"
	controlv1 "github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1"
	"github.com/robfig/cron/v3"
)

type PipelineStatusReader interface {
	LastPipelineStatus() (repository, status, message string)
}

type Engine struct {
	store          store.PolicyStore
	promClient     prometheus.Client
	schedClient    scheduler.Client
	pipelineReader PipelineStatusReader

	mu          sync.Mutex
	lastTrigger map[string]time.Time
	cronParser  cron.Parser
}

type MetricCondition struct {
	Mode      string  `json:"mode"`
	Query     string  `json:"query"`
	Operator  string  `json:"operator"`
	Threshold float64 `json:"threshold"`
}

type ClusterCondition struct {
	Mode      string  `json:"mode"`
	Field     string  `json:"field"`
	Operator  string  `json:"operator"`
	Threshold float64 `json:"threshold"`
}

type ScheduleCondition struct {
	Mode       string `json:"mode"`
	Expression string `json:"expression"`
}

type PipelineCondition struct {
	Mode       string `json:"mode"`
	Repository string `json:"repository"`
	Status     string `json:"status"`
}

type Action struct {
	Type            string `json:"type"`
	DesiredState    string `json:"desired_state"`
	DesiredReplicas int32  `json:"desired_replicas"`
	ReplicaDelta    int32  `json:"replica_delta"`
}

func New(st store.PolicyStore, promClient prometheus.Client, schedClient scheduler.Client, pipelineReader PipelineStatusReader) *Engine {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return &Engine{store: st, promClient: promClient, schedClient: schedClient, pipelineReader: pipelineReader, lastTrigger: make(map[string]time.Time), cronParser: parser}
}

func (e *Engine) EvaluatePolicies(ctx context.Context, policyID string) []*automationv1.PolicyEvaluationResult {
	policies, err := e.store.ListPolicies(ctx, true)
	if err != nil {
		return []*automationv1.PolicyEvaluationResult{{
			PolicyId:   "",
			Matched:    false,
			Dispatched: false,
			Reason:     fmt.Sprintf("failed to load policies: %v", err),
		}}
	}
	results := make([]*automationv1.PolicyEvaluationResult, 0, len(policies))

	for _, p := range policies {
		if policyID != "" && p.GetId() != policyID {
			continue
		}
		if !p.GetEnabled() {
			results = append(results, &automationv1.PolicyEvaluationResult{PolicyId: p.GetId(), Matched: false, Dispatched: false, Reason: "policy disabled"})
			continue
		}
		res := e.evaluateSingle(ctx, p)
		results = append(results, res)
	}
	return results
}

func (e *Engine) ReplayQueuedSuggestions(ctx context.Context, limit int) {
	items, err := e.store.ClaimQueuedSuggestions(ctx, limit)
	if err != nil {
		return
	}
	for _, item := range items {
		suggestion := scheduler.AutomationSuggestion{
			SuggestionID:    item.ID,
			PolicyID:        item.PolicyID,
			PolicyName:      item.PolicyName,
			TargetWorkload:  item.TargetWorkload,
			ActionType:      actionTypeFromString(item.ActionType),
			DesiredState:    item.DesiredState,
			DesiredReplicas: item.DesiredReplicas,
			ReplicaDelta:    item.ReplicaDelta,
			Reason:          item.Reason,
		}
		resp, err := e.schedClient.SubmitAutomationSuggestion(ctx, suggestion)
		if err != nil {
			_ = e.store.MarkQueuedSuggestionResult(ctx, item.ID, false, err.Error())
			continue
		}
		if resp.GetAccepted() {
			_ = e.store.MarkQueuedSuggestionResult(ctx, item.ID, true, "accepted")
			continue
		}
		_ = e.store.MarkQueuedSuggestionResult(ctx, item.ID, false, "rejected: "+resp.GetReason())
	}
}

func (e *Engine) evaluateSingle(ctx context.Context, p *automationv1.Policy) *automationv1.PolicyEvaluationResult {
	matched, reason := e.evaluateCondition(ctx, p)
	if !matched {
		if err := e.store.AppendAudit(ctx, store.AuditEntry{
			PolicyID:       p.GetId(),
			PolicyName:     p.GetName(),
			TargetWorkload: p.GetTargetWorkload(),
			Matched:        false,
			Dispatched:     false,
			Reason:         reason,
		}); err != nil {
			reason = fmt.Sprintf("%s; audit_write_failed=%v", reason, err)
		}
		recordEvaluationMetric(p.GetType(), false, false)
		return &automationv1.PolicyEvaluationResult{PolicyId: p.GetId(), Matched: false, Dispatched: false, Reason: reason}
	}

	dispatched, dispatchReason := e.dispatchAction(ctx, p)
	if err := e.store.AppendAudit(ctx, store.AuditEntry{
		PolicyID:       p.GetId(),
		PolicyName:     p.GetName(),
		TargetWorkload: p.GetTargetWorkload(),
		Matched:        true,
		Dispatched:     dispatched,
		Reason:         dispatchReason,
	}); err != nil {
		dispatchReason = fmt.Sprintf("%s; audit_write_failed=%v", dispatchReason, err)
	}
	recordEvaluationMetric(p.GetType(), true, dispatched)
	return &automationv1.PolicyEvaluationResult{PolicyId: p.GetId(), Matched: true, Dispatched: dispatched, Reason: dispatchReason}
}

func (e *Engine) evaluateCondition(ctx context.Context, p *automationv1.Policy) (bool, string) {
	var modeProbe struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(p.GetConditionExpression()), &modeProbe); err != nil {
		return false, fmt.Sprintf("invalid condition_expression JSON: %v", err)
	}

	switch strings.ToLower(strings.TrimSpace(modeProbe.Mode)) {
	case "promql":
		var c MetricCondition
		if err := json.Unmarshal([]byte(p.GetConditionExpression()), &c); err != nil {
			return false, fmt.Sprintf("invalid promql condition: %v", err)
		}
		value, err := e.promClient.Query(ctx, c.Query)
		if err != nil {
			return false, fmt.Sprintf("prometheus query failed: %v", err)
		}
		if compare(value, c.Operator, c.Threshold) {
			return true, fmt.Sprintf("condition matched: %0.3f %s %0.3f", value, c.Operator, c.Threshold)
		}
		return false, fmt.Sprintf("condition not matched: %0.3f %s %0.3f", value, c.Operator, c.Threshold)
	case "cluster_summary":
		var c ClusterCondition
		if err := json.Unmarshal([]byte(p.GetConditionExpression()), &c); err != nil {
			return false, fmt.Sprintf("invalid cluster condition: %v", err)
		}
		summary, err := e.schedClient.GetClusterSummary(ctx)
		if err != nil {
			return false, fmt.Sprintf("scheduler summary failed: %v", err)
		}
		value, ok := clusterField(summary, c.Field)
		if !ok {
			return false, fmt.Sprintf("unknown cluster_summary field %q", c.Field)
		}
		if compare(value, c.Operator, c.Threshold) {
			return true, fmt.Sprintf("condition matched: %s=%0.3f", c.Field, value)
		}
		return false, fmt.Sprintf("condition not matched: %s=%0.3f", c.Field, value)
	case "cron":
		var c ScheduleCondition
		if err := json.Unmarshal([]byte(p.GetConditionExpression()), &c); err != nil {
			return false, fmt.Sprintf("invalid cron condition: %v", err)
		}
		schedule, err := e.cronParser.Parse(c.Expression)
		if err != nil {
			return false, fmt.Sprintf("invalid cron expression: %v", err)
		}
		now := time.Now().UTC()
		windowStart := now.Add(-1 * time.Minute)
		next := schedule.Next(windowStart)
		if next.After(now) {
			return false, "cron not due"
		}
		if e.alreadyTriggered(p.GetId(), next) {
			return false, "cron already triggered"
		}
		e.markTriggered(p.GetId(), next)
		return true, "cron schedule due"
	case "pipeline_status":
		if e.pipelineReader == nil {
			return false, "pipeline status collector not configured"
		}
		var c PipelineCondition
		if err := json.Unmarshal([]byte(p.GetConditionExpression()), &c); err != nil {
			return false, fmt.Sprintf("invalid pipeline condition: %v", err)
		}
		repo, status, msg := e.pipelineReader.LastPipelineStatus()
		if strings.EqualFold(strings.TrimSpace(repo), strings.TrimSpace(c.Repository)) && strings.EqualFold(strings.TrimSpace(status), strings.TrimSpace(c.Status)) {
			return true, fmt.Sprintf("pipeline status matched repo=%s status=%s", repo, status)
		}
		return false, fmt.Sprintf("pipeline status not matched latest_repo=%s latest_status=%s msg=%s", repo, status, msg)
	default:
		return false, fmt.Sprintf("unsupported condition mode %q", modeProbe.Mode)
	}
}

func (e *Engine) dispatchAction(ctx context.Context, p *automationv1.Policy) (bool, string) {
	var action Action
	if err := json.Unmarshal([]byte(p.GetActionExpression()), &action); err != nil {
		return false, fmt.Sprintf("invalid action_expression JSON: %v", err)
	}

	target := strings.TrimSpace(p.GetTargetWorkload())
	if target == "" {
		return false, "target_workload is empty"
	}

	suggestion, err := buildSuggestionFromAction(p, action)
	if err != nil {
		return false, err.Error()
	}
	resp, err := e.schedClient.SubmitAutomationSuggestion(ctx, suggestion)
	if err != nil {
		queueErr := e.store.EnqueueSuggestion(ctx, store.QueuedSuggestion{
			PolicyID:        p.GetId(),
			PolicyName:      p.GetName(),
			TargetWorkload:  target,
			ActionType:      suggestion.ActionType.String(),
			DesiredState:    suggestion.DesiredState,
			DesiredReplicas: suggestion.DesiredReplicas,
			ReplicaDelta:    suggestion.ReplicaDelta,
			Reason:          suggestion.Reason,
		})
		if queueErr != nil {
			return false, fmt.Sprintf("scheduler suggestion failed: %v; queue enqueue failed: %v", err, queueErr)
		}
		return false, fmt.Sprintf("scheduler unavailable; suggestion queued for retry: %v", err)
	}
	if resp.GetAccepted() {
		return true, fmt.Sprintf("scheduler accepted suggestion: %s", strings.TrimSpace(resp.GetAppliedAction()))
	}
	return false, fmt.Sprintf("scheduler rejected suggestion: %s", strings.TrimSpace(resp.GetReason()))
}

func buildSuggestionFromAction(p *automationv1.Policy, action Action) (scheduler.AutomationSuggestion, error) {
	target := strings.TrimSpace(p.GetTargetWorkload())
	suggestion := scheduler.AutomationSuggestion{
		SuggestionID:   "",
		PolicyID:       p.GetId(),
		PolicyName:     p.GetName(),
		TargetWorkload: target,
		Reason:         fmt.Sprintf("policy=%s condition/action matched", p.GetName()),
	}
	if target == "" {
		return suggestion, fmt.Errorf("target_workload is empty")
	}

	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "set_desired_state":
		desired := strings.TrimSpace(action.DesiredState)
		if desired == "" {
			return suggestion, fmt.Errorf("desired_state required")
		}
		suggestion.ActionType = controlv1.AutomationActionType_AUTOMATION_ACTION_SET_DESIRED_STATE
		suggestion.DesiredState = desired
	case "retry_workload":
		suggestion.ActionType = controlv1.AutomationActionType_AUTOMATION_ACTION_RETRY_WORKLOAD
	case "delete_workload":
		suggestion.ActionType = controlv1.AutomationActionType_AUTOMATION_ACTION_DELETE_WORKLOAD
	case "scale_replicas":
		suggestion.ActionType = controlv1.AutomationActionType_AUTOMATION_ACTION_SCALE_REPLICAS
		suggestion.DesiredReplicas = action.DesiredReplicas
		suggestion.ReplicaDelta = action.ReplicaDelta
	default:
		return suggestion, fmt.Errorf("unsupported action type %q", action.Type)
	}
	return suggestion, nil
}

func actionTypeFromString(v string) controlv1.AutomationActionType {
	switch strings.TrimSpace(v) {
	case controlv1.AutomationActionType_AUTOMATION_ACTION_SET_DESIRED_STATE.String():
		return controlv1.AutomationActionType_AUTOMATION_ACTION_SET_DESIRED_STATE
	case controlv1.AutomationActionType_AUTOMATION_ACTION_RETRY_WORKLOAD.String():
		return controlv1.AutomationActionType_AUTOMATION_ACTION_RETRY_WORKLOAD
	case controlv1.AutomationActionType_AUTOMATION_ACTION_DELETE_WORKLOAD.String():
		return controlv1.AutomationActionType_AUTOMATION_ACTION_DELETE_WORKLOAD
	case controlv1.AutomationActionType_AUTOMATION_ACTION_SCALE_REPLICAS.String():
		return controlv1.AutomationActionType_AUTOMATION_ACTION_SCALE_REPLICAS
	default:
		return controlv1.AutomationActionType_AUTOMATION_ACTION_TYPE_UNSPECIFIED
	}
}

func compare(value float64, op string, threshold float64) bool {
	switch strings.TrimSpace(op) {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	default:
		return false
	}
}

func clusterField(summary interface {
	GetTotalNodes() int32
	GetReadyNodes() int32
	GetNotReadyNodes() int32
	GetTotalWorkloads() int32
	GetRunningWorkloads() int32
	GetPendingWorkloads() int32
	GetFailedWorkloads() int32
	GetDeletedWorkloads() int32
}, field string) (float64, bool) {
	switch strings.TrimSpace(field) {
	case "total_nodes":
		return float64(summary.GetTotalNodes()), true
	case "ready_nodes":
		return float64(summary.GetReadyNodes()), true
	case "not_ready_nodes":
		return float64(summary.GetNotReadyNodes()), true
	case "total_workloads":
		return float64(summary.GetTotalWorkloads()), true
	case "running_workloads":
		return float64(summary.GetRunningWorkloads()), true
	case "pending_workloads":
		return float64(summary.GetPendingWorkloads()), true
	case "failed_workloads":
		return float64(summary.GetFailedWorkloads()), true
	case "deleted_workloads":
		return float64(summary.GetDeletedWorkloads()), true
	default:
		return 0, false
	}
}

func (e *Engine) alreadyTriggered(policyID string, ts time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	last, ok := e.lastTrigger[policyID]
	if !ok {
		return false
	}
	return last.Equal(ts)
}

func (e *Engine) markTriggered(policyID string, ts time.Time) {
	e.mu.Lock()
	e.lastTrigger[policyID] = ts
	e.mu.Unlock()
}
