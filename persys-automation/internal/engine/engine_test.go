package engine

import (
	"context"
	"errors"
	"testing"

	automationv1 "github.com/persys-dev/persys-cloud/persys-automation/internal/automationv1"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/scheduler"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/store"
	controlv1 "github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1"
)

type fakePrometheus struct {
	value float64
	err   error
}

func (f *fakePrometheus) Query(_ context.Context, _ string) (float64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.value, nil
}

type fakeScheduler struct {
	summary *controlv1.GetClusterSummaryResponse
	err     error
	actions []string
}

func (f *fakeScheduler) GetClusterSummary(_ context.Context) (*controlv1.GetClusterSummaryResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.summary == nil {
		return &controlv1.GetClusterSummaryResponse{}, nil
	}
	return f.summary, nil
}

func (f *fakeScheduler) SubmitAutomationSuggestion(_ context.Context, suggestion scheduler.AutomationSuggestion) (*controlv1.SubmitAutomationSuggestionResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.actions = append(f.actions, suggestion.TargetWorkload+":"+suggestion.ActionType.String()+":"+suggestion.DesiredState)
	return &controlv1.SubmitAutomationSuggestionResponse{Accepted: true, AppliedAction: suggestion.ActionType.String()}, nil
}

func (f *fakeScheduler) Close() error { return nil }

func TestPromQLPolicyDispatchesDesiredState(t *testing.T) {
	st := store.NewMemoryStore()
	prom := &fakePrometheus{value: 86}
	sched := &fakeScheduler{}
	eng := New(st, prom, sched, nil)

	p, err := st.CreatePolicy(context.Background(), &automationv1.CreatePolicyRequest{
		Name:                "cpu-guard",
		TargetWorkload:      "payments-api",
		Type:                automationv1.PolicyType_POLICY_TYPE_SCALE,
		ConditionExpression: `{"mode":"promql","query":"cpu","operator":">","threshold":75}`,
		ActionExpression:    `{"type":"set_desired_state","desired_state":"Running"}`,
	})
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}

	results := eng.EvaluatePolicies(context.Background(), p.GetId())
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if !results[0].GetMatched() {
		t.Fatalf("expected policy match")
	}
	if !results[0].GetDispatched() {
		t.Fatalf("expected policy dispatch")
	}
	if len(sched.actions) != 1 || sched.actions[0] != "payments-api:AUTOMATION_ACTION_SET_DESIRED_STATE:Running" {
		t.Fatalf("unexpected actions: %+v", sched.actions)
	}
}

func TestClusterSummaryConditionCanTriggerRetry(t *testing.T) {
	st := store.NewMemoryStore()
	prom := &fakePrometheus{}
	sched := &fakeScheduler{summary: &controlv1.GetClusterSummaryResponse{FailedWorkloads: 4}}
	eng := New(st, prom, sched, nil)

	p, err := st.CreatePolicy(context.Background(), &automationv1.CreatePolicyRequest{
		Name:                "retry-failed",
		TargetWorkload:      "checkout-api",
		Type:                automationv1.PolicyType_POLICY_TYPE_DRIFT,
		ConditionExpression: `{"mode":"cluster_summary","field":"failed_workloads","operator":">=","threshold":3}`,
		ActionExpression:    `{"type":"retry_workload"}`,
	})
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}

	results := eng.EvaluatePolicies(context.Background(), p.GetId())
	if len(results) != 1 || !results[0].GetMatched() || !results[0].GetDispatched() {
		t.Fatalf("unexpected results: %+v", results)
	}
	if len(sched.actions) != 1 || sched.actions[0] != "checkout-api:AUTOMATION_ACTION_RETRY_WORKLOAD:" {
		t.Fatalf("expected retry action for checkout-api, got %+v", sched.actions)
	}
}

func TestPolicyFailureIsAudited(t *testing.T) {
	st := store.NewMemoryStore()
	prom := &fakePrometheus{err: errors.New("prometheus unavailable")}
	sched := &fakeScheduler{}
	eng := New(st, prom, sched, nil)

	p, err := st.CreatePolicy(context.Background(), &automationv1.CreatePolicyRequest{
		Name:                "degraded",
		TargetWorkload:      "checkout-api",
		Type:                automationv1.PolicyType_POLICY_TYPE_SCALE,
		ConditionExpression: `{"mode":"promql","query":"cpu","operator":">","threshold":75}`,
		ActionExpression:    `{"type":"set_desired_state","desired_state":"Running"}`,
	})
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}

	results := eng.EvaluatePolicies(context.Background(), p.GetId())
	if len(results) != 1 || results[0].GetMatched() {
		t.Fatalf("expected non-match due to prometheus error, got %+v", results)
	}
	audit, err := st.ListAudit(context.Background(), 1)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audit) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(audit))
	}
	if audit[0].Dispatched {
		t.Fatalf("expected no dispatch in audit")
	}
}
