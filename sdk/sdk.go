// Package sdk provides the public Persys Cloud Go SDK entry points.
package sdk

import (
	"github.com/persys-dev/persys-cloud/sdk/client"
	"github.com/persys-dev/persys-cloud/sdk/options"
)

// Options configures a Persys SDK client.
type Options = options.Options

// Client is the reusable Persys Cloud client.
type Client = client.Client

// DefaultOptions returns default SDK options.
func DefaultOptions() *Options { return options.DefaultOptions() }

// New creates a Persys Cloud SDK client.
func New(opts *Options) (*Client, error) { return client.New(opts) }
