package platform

import "context"

// NetworkAttachment represents a provider-specific network attachment.
type NetworkAttachment struct {
	ID         string
	WorkloadID string
	Network    string
	Interface  string
	IPAddress  string
	MAC        string
}

// NetworkProvider is reserved for runtime/network decoupling (phase 3).
type NetworkProvider interface {
	Driver() string
	Attach(ctx context.Context, workloadID string, spec WorkloadNetSpec) (*NetworkAttachment, error)
	Detach(ctx context.Context, attachment *NetworkAttachment) error
}
