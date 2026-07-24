// Package options defines SDK configuration.
package options

import (
	"fmt"
	"time"
)

const (
	// TransportHTTP uses HTTPS to the API Gateway (recommended default).
	TransportHTTP = "http"
	// TransportGRPC uses gRPC directly (advanced / internal use).
	TransportGRPC = "grpc"
)

// Options configures the Persys SDK client.
type Options struct {
	// APIEndpoint is the URL of the Persys API Gateway.
	// Default: "https://localhost:8443"
	APIEndpoint string

	// GRPCEndpoint is the address used when Transport == TransportGRPC.
	// Default: "localhost:9090"
	GRPCEndpoint string

	// Transport selects the wire protocol: TransportHTTP (default) or
	// TransportGRPC.
	Transport string

	// Timeout is the per-request deadline. Default: 30s.
	Timeout time.Duration

	// Insecure disables TLS verification. For local development only.
	Insecure bool

	// UseCertManager enables Vault-backed certificate management (default true).
	UseCertManager   bool
	VaultManagerAddr string

	// Identity holds service-identity configuration.
	Identity IdentityOptions

	// Internal cert paths (populated by certmanager integration)
	TLSCertPath string
	TLSKeyPath  string
	TLSCAPath   string
}

// IdentityOptions configures service identity.
type IdentityOptions struct {
	VaultAddr   string
	VaultToken  string
	PKIMount    string
	PKIRole     string
	ServiceName string
	TTL         string
}

// DefaultOptions returns SDK configuration suitable for local development.
func DefaultOptions() *Options {
	return &Options{
		APIEndpoint:      "https://localhost:8443",
		GRPCEndpoint:     "localhost:9090",
		Transport:        TransportHTTP,
		Timeout:          30 * time.Second,
		Insecure:         false,
		UseCertManager:   true,
		VaultManagerAddr: "localhost:50069", // default
		Identity: IdentityOptions{
			PKIMount:    "pki",
			PKIRole:     "persys-sdk",
			ServiceName: "persys-sdk-client",
			TTL:         "24h",
		},
	}
}

// Validate checks required fields.
func (o *Options) Validate() error {
	if o.APIEndpoint == "" {
		return fmt.Errorf("APIEndpoint is required")
	}
	return nil
}

// WithInsecure returns a copy with TLS verification disabled.
func WithInsecure(opts *Options) *Options {
	cp := *opts
	cp.Insecure = true
	cp.UseCertManager = false
	return &cp
}

// WithEndpoint returns a copy with APIEndpoint overridden.
func WithEndpoint(opts *Options, endpoint string) *Options {
	cp := *opts
	cp.APIEndpoint = endpoint
	return &cp
}

// WithTransport returns a copy with Transport overridden.
func WithTransport(opts *Options, transport string) *Options {
	cp := *opts
	cp.Transport = transport
	return &cp
}
