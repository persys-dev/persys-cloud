// Package options defines configuration values for the Persys Cloud SDK.
package options

import "time"

const (
	// TransportHTTP sends requests through the Persys HTTP gateway.
	TransportHTTP = "http"
	// TransportGRPC sends requests directly to the Persys scheduler gRPC API.
	TransportGRPC = "grpc"
)

// Options contains user-configurable SDK settings.
type Options struct {
	Transport      string        `yaml:"transport"`
	APIEndpoint    string        `yaml:"api_endpoint"`
	GRPCEndpoint   string        `yaml:"grpc_endpoint"`
	Timeout        time.Duration `yaml:"timeout"`
	Insecure       bool          `yaml:"insecure"`
	UseCertManager bool          `yaml:"use_cert_manager"`

	TLSCertPath string `yaml:"tls_cert_path"`
	TLSKeyPath  string `yaml:"tls_key_path"`
	TLSCAPath   string `yaml:"tls_ca_path"`
}

// DefaultOptions returns SDK defaults compatible with persysctl conventions.
func DefaultOptions() *Options {
	return &Options{Transport: TransportGRPC, APIEndpoint: "http://localhost:8080", GRPCEndpoint: "localhost:50051", Timeout: 30 * time.Second, UseCertManager: false}
}
