package engine

import (
	"context"
	"errors"
	"strings"

	automationv1 "github.com/persys-dev/persys-cloud/persys-automation/internal/automationv1"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type GRPCService struct {
	automationv1.UnimplementedAutomationControlServer
	store  store.PolicyStore
	engine *Engine
}

func NewGRPCService(st store.PolicyStore, eng *Engine) *GRPCService {
	return &GRPCService{store: st, engine: eng}
}

func (s *GRPCService) CreatePolicy(ctx context.Context, req *automationv1.CreatePolicyRequest) (*automationv1.CreatePolicyResponse, error) {
	if strings.TrimSpace(req.GetName()) == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if strings.TrimSpace(req.GetConditionExpression()) == "" {
		return nil, status.Error(codes.InvalidArgument, "condition_expression is required")
	}
	if strings.TrimSpace(req.GetActionExpression()) == "" {
		return nil, status.Error(codes.InvalidArgument, "action_expression is required")
	}
	if req.GetType() == automationv1.PolicyType_POLICY_TYPE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "policy type is required")
	}
	policy, err := s.store.CreatePolicy(ctx, req)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &automationv1.CreatePolicyResponse{Policy: policy}, nil
}

func (s *GRPCService) ListPolicies(ctx context.Context, req *automationv1.ListPoliciesRequest) (*automationv1.ListPoliciesResponse, error) {
	policies, err := s.store.ListPolicies(ctx, req.GetIncludeDisabled())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &automationv1.ListPoliciesResponse{Policies: policies}, nil
}

func (s *GRPCService) EnablePolicy(ctx context.Context, req *automationv1.EnablePolicyRequest) (*automationv1.EnablePolicyResponse, error) {
	policy, err := s.store.SetPolicyEnabled(ctx, req.GetPolicyId(), true)
	if err != nil {
		if errors.Is(err, store.ErrPolicyNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &automationv1.EnablePolicyResponse{Policy: policy}, nil
}

func (s *GRPCService) DisablePolicy(ctx context.Context, req *automationv1.DisablePolicyRequest) (*automationv1.DisablePolicyResponse, error) {
	policy, err := s.store.SetPolicyEnabled(ctx, req.GetPolicyId(), false)
	if err != nil {
		if errors.Is(err, store.ErrPolicyNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &automationv1.DisablePolicyResponse{Policy: policy}, nil
}

func (s *GRPCService) EvaluateNow(ctx context.Context, req *automationv1.EvaluateNowRequest) (*automationv1.EvaluateNowResponse, error) {
	results := s.engine.EvaluatePolicies(ctx, strings.TrimSpace(req.GetPolicyId()))
	return &automationv1.EvaluateNowResponse{Results: results}, nil
}

func (s *GRPCService) ListAuditLog(ctx context.Context, req *automationv1.ListAuditLogRequest) (*automationv1.ListAuditLogResponse, error) {
	entries, err := s.store.ListAudit(ctx, req.GetLimit())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := make([]*automationv1.AuditEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, &automationv1.AuditEntry{
			Id:             entry.ID,
			PolicyId:       entry.PolicyID,
			PolicyName:     entry.PolicyName,
			TargetWorkload: entry.TargetWorkload,
			Matched:        entry.Matched,
			Dispatched:     entry.Dispatched,
			Reason:         entry.Reason,
			OldState:       entry.OldState,
			NewState:       entry.NewState,
			Timestamp:      timestamppb.New(entry.Timestamp),
		})
	}
	return &automationv1.ListAuditLogResponse{Entries: out}, nil
}
