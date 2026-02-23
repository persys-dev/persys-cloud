package scheduler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"time"

	controlv1 "github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type Client interface {
	GetClusterSummary(ctx context.Context) (*controlv1.GetClusterSummaryResponse, error)
	SubmitAutomationSuggestion(ctx context.Context, suggestion AutomationSuggestion) (*controlv1.SubmitAutomationSuggestionResponse, error)
	Close() error
}

type AutomationSuggestion struct {
	SuggestionID    string
	PolicyID        string
	PolicyName      string
	TargetWorkload  string
	ActionType      controlv1.AutomationActionType
	DesiredState    string
	DesiredReplicas int32
	ReplicaDelta    int32
	Reason          string
}

type GRPCClient struct {
	conn   *grpc.ClientConn
	client controlv1.AgentControlClient
}

type Config struct {
	Address          string
	TLSEnabled       bool
	CAPath           string
	ClientCertPath   string
	ClientKeyPath    string
	InsecureSkipCert bool
}

func New(cfg Config) (*GRPCClient, error) {
	if cfg.Address == "" {
		return nil, ErrSchedulerAddressRequired
	}

	dialOpts := []grpc.DialOption{grpc.WithBlock()}
	if cfg.TLSEnabled {
		tlsCfg, err := loadClientTLS(cfg)
		if err != nil {
			return nil, err
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, cfg.Address, dialOpts...)
	if err != nil {
		return nil, err
	}

	return &GRPCClient{conn: conn, client: controlv1.NewAgentControlClient(conn)}, nil
}

func (c *GRPCClient) GetClusterSummary(ctx context.Context) (*controlv1.GetClusterSummaryResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.client.GetClusterSummary(ctx, &controlv1.GetClusterSummaryRequest{})
}

func (c *GRPCClient) SubmitAutomationSuggestion(ctx context.Context, suggestion AutomationSuggestion) (*controlv1.SubmitAutomationSuggestionResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resp, err := c.client.SubmitAutomationSuggestion(ctx, &controlv1.SubmitAutomationSuggestionRequest{
		Suggestion: &controlv1.AutomationSuggestion{
			SuggestionId:    suggestion.SuggestionID,
			PolicyId:        suggestion.PolicyID,
			PolicyName:      suggestion.PolicyName,
			TargetWorkload:  suggestion.TargetWorkload,
			ActionType:      suggestion.ActionType,
			DesiredState:    suggestion.DesiredState,
			DesiredReplicas: suggestion.DesiredReplicas,
			ReplicaDelta:    suggestion.ReplicaDelta,
			Reason:          suggestion.Reason,
		},
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *GRPCClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func loadClientTLS(cfg Config) (*tls.Config, error) {
	keyPair, err := tls.LoadX509KeyPair(cfg.ClientCertPath, cfg.ClientKeyPath)
	if err != nil {
		return nil, err
	}
	caBytes, err := os.ReadFile(cfg.CAPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, ErrAppendCAFailed
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		Certificates:       []tls.Certificate{keyPair},
		RootCAs:            pool,
		InsecureSkipVerify: cfg.InsecureSkipCert,
	}, nil
}
