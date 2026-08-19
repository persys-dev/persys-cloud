package services

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	automationv1 "github.com/persys-dev/persys-cloud/pkg/automation/automationv1"
)

// AutomationService proxies gRPC calls from the gateway to persys-automation.
type AutomationService struct {
	config    *config.Config
	clientTLS *tls.Config
	timeout   time.Duration

	certMgr   *certmanager.Manager
}

// NewAutomationService builds an AutomationService, reusing the gateway's own
// mTLS client identity to dial persys-automation (same pattern as forgery).
func NewAutomationService(cfg *config.Config, clientTLS *tls.Config) (*AutomationService, error) {
	timeout, err := time.ParseDuration(cfg.Automation.RequestTimeout)
	if err != nil || timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &AutomationService{config: cfg, clientTLS: clientTLS, timeout: timeout}, nil
}

func (s *AutomationService) SetCertManager(m *certmanager.Manager) {
	s.certMgr = m
}


func (s *AutomationService) CreatePolicy(ctx context.Context, req *automationv1.CreatePolicyRequest) (*automationv1.CreatePolicyResponse, error) {
	resp, err := s.invoke(ctx, func(client automationv1.AutomationControlClient) (any, error) {
		return client.CreatePolicy(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*automationv1.CreatePolicyResponse), nil
}

func (s *AutomationService) ListPolicies(ctx context.Context, req *automationv1.ListPoliciesRequest) (*automationv1.ListPoliciesResponse, error) {
	resp, err := s.invoke(ctx, func(client automationv1.AutomationControlClient) (any, error) {
		return client.ListPolicies(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*automationv1.ListPoliciesResponse), nil
}

func (s *AutomationService) EnablePolicy(ctx context.Context, req *automationv1.EnablePolicyRequest) (*automationv1.EnablePolicyResponse, error) {
	resp, err := s.invoke(ctx, func(client automationv1.AutomationControlClient) (any, error) {
		return client.EnablePolicy(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*automationv1.EnablePolicyResponse), nil
}

func (s *AutomationService) DisablePolicy(ctx context.Context, req *automationv1.DisablePolicyRequest) (*automationv1.DisablePolicyResponse, error) {
	resp, err := s.invoke(ctx, func(client automationv1.AutomationControlClient) (any, error) {
		return client.DisablePolicy(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*automationv1.DisablePolicyResponse), nil
}

func (s *AutomationService) EvaluateNow(ctx context.Context, req *automationv1.EvaluateNowRequest) (*automationv1.EvaluateNowResponse, error) {
	resp, err := s.invoke(ctx, func(client automationv1.AutomationControlClient) (any, error) {
		return client.EvaluateNow(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*automationv1.EvaluateNowResponse), nil
}

func (s *AutomationService) ListAuditLog(ctx context.Context, req *automationv1.ListAuditLogRequest) (*automationv1.ListAuditLogResponse, error) {
	resp, err := s.invoke(ctx, func(client automationv1.AutomationControlClient) (any, error) {
		return client.ListAuditLog(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*automationv1.ListAuditLogResponse), nil
}

func (s *AutomationService) invoke(ctx context.Context, call func(automationv1.AutomationControlClient) (any, error)) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var automationTLS *tls.Config
	if s.clientTLS != nil {
		automationTLS = s.clientTLS.Clone()
	} else {
		automationTLS = &tls.Config{}
	}
	if serverName := s.config.Automation.GRPCServerName; serverName != "" {
		automationTLS.ServerName = serverName
	}

	conn, err := dialGRPCTLS(callCtx, s.config.Automation.GRPCAddr, automationTLS, s.certMgr)
	if err != nil {
		return nil, fmt.Errorf("dial automation %s: %w", s.config.Automation.GRPCAddr, err)
	}
	defer conn.Close()

	client := automationv1.NewAutomationControlClient(conn)
	resp, err := call(client)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
