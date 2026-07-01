// Package sdk is the main entrypoint for the Persys Go SDK.
package sdk

import (
	"context"
	"fmt"

	"github.com/persys-dev/persys-cloud/sdk/client"
	"github.com/persys-dev/persys-cloud/sdk/identity"
	"github.com/persys-dev/persys-cloud/sdk/options"
	"github.com/persys-dev/persys-cloud/sdk/resources"
)

type Client struct {
	client   *client.Client
	identity identity.Provider
}

type Option func(*options.Options) error

// New creates a new Persys SDK client.
func New(opts ...Option) (*Client, error) {
	cfg := options.DefaultOptions()

	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
	}

	// Identity provider (backed by pkg/certmanager)
	provider, err := identity.NewProvider(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("identity provider: %w", err)
	}

	// Create the HTTP Gateway client
	apiClient, err := client.New(client.Options{
		BaseURL:  cfg.APIEndpoint,
		Identity: provider,
		Timeout:  cfg.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	return &Client{
		client:   apiClient,
		identity: provider,
	}, nil
}

func DefaultOptions() options.Options {
	return *options.DefaultOptions()
}

func WithEndpoint(endpoint string) Option {
	return func(o *options.Options) error {
		o.APIEndpoint = endpoint
		return nil
	}
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.identity != nil {
		_ = c.identity.Close()
	}
	return c.client.Close()
}

// Resource builders (matches README)
func (c *Client) Workloads() *resources.Workloads {
	return resources.NewWorkloads(c.client)
}

func (c *Client) Nodes() *resources.Nodes {
	return resources.NewNodes(c.client)
}

func (c *Client) Clusters() *resources.Clusters {
	return resources.NewClusters(c.client)
}

// Forgery provides access to CI/CD forgery operations.
func (c *Client) Forgery() *resources.Forgery {
	return resources.NewForgery(c.client)
}