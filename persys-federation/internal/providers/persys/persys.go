package providers

// Minimal constants used by legacy integration tests.
const (
	CloudActionSuccess = "success"
	CloudActionFailure = "failure"
)

// CreateCluster is currently a placeholder hook for provider-backed cluster provisioning.
func CreateCluster() error {
	return nil
}
