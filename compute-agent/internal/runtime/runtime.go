package runtime

import (
	"context"
	"fmt"

	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
)

// Runtime defines the interface for workload execution
type Runtime interface {
	// Create creates a new workload
	Create(ctx context.Context, workload *models.Workload) error

	// Start starts a workload
	Start(ctx context.Context, id string) error

	// Stop stops a workload
	Stop(ctx context.Context, id string) error

	// Delete removes a workload
	Delete(ctx context.Context, id string) error

	// Status retrieves workload status
	Status(ctx context.Context, id string) (models.ActualState, string, error)

	// List returns all workloads managed by this runtime
	List(ctx context.Context) ([]string, error)

	// Type returns the runtime type
	Type() models.WorkloadType

	// Healthy checks if the runtime is operational
	Healthy(ctx context.Context) error
}

// StatusMetadataProvider is an optional runtime extension for surfacing additional status details.
// Implementations can expose metadata such as VM IP addresses, interfaces, or credentials.
type StatusMetadataProvider interface {
	StatusMetadata(ctx context.Context, id string) (map[string]string, error)
}

// UsageStatsProvider is an optional runtime extension for surfacing per-workload
// resource utilization (CPU, memory, disk I/O, network I/O). Runtimes that can
// cheaply sample point-in-time usage from the underlying engine (Docker stats,
// libvirt domain stats, etc.) should implement this so WorkloadStatus.Usage can
// be populated for callers (heartbeat, ListWorkloads, GetWorkloadStatus).
//
// Implementations should return an error (rather than a zero-value snapshot)
// when usage cannot be determined, e.g. the workload isn't currently running -
// callers treat this as "no usage sample available" and leave Usage unset.
type UsageStatsProvider interface {
	UsageStats(ctx context.Context, id string) (*models.WorkloadUsage, error)
}

// Manager coordinates multiple runtimes
type Manager struct {
	runtimes map[models.WorkloadType]Runtime
}

// NewManager creates a new runtime manager
func NewManager() *Manager {
	return &Manager{
		runtimes: make(map[models.WorkloadType]Runtime),
	}
}

// Register adds a runtime to the manager
func (m *Manager) Register(runtime Runtime) {
	m.runtimes[runtime.Type()] = runtime
}

// GetRuntime returns the runtime for a given workload type
func (m *Manager) GetRuntime(workloadType models.WorkloadType) (Runtime, error) {
	runtime, exists := m.runtimes[workloadType]
	if !exists {
		return nil, fmt.Errorf("runtime not available for type: %s", workloadType)
	}
	return runtime, nil
}

// IsEnabled checks if a runtime is registered
func (m *Manager) IsEnabled(workloadType models.WorkloadType) bool {
	_, exists := m.runtimes[workloadType]
	return exists
}

// HealthCheck checks all registered runtimes
func (m *Manager) HealthCheck(ctx context.Context) map[string]string {
	results := make(map[string]string)

	for typ, runtime := range m.runtimes {
		if err := runtime.Healthy(ctx); err != nil {
			results[string(typ)] = fmt.Sprintf("unhealthy: %v", err)
		} else {
			results[string(typ)] = "healthy"
		}
	}

	return results
}
