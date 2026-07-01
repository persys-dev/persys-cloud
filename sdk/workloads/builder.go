// Package workloads provides the fluent workload resource builder.
//
// Obtain a Builder from sdk.Client.Workloads() and use method chaining
// to perform workload operations:
//
//	list, err := client.Workloads().List(ctx)
//	status, err := client.Workloads().Create(ctx, sdk.Workload{Name: "web", Image: "nginx"})
//	err = client.Workloads().Delete(ctx, "my-workload-id")
package workloads

import (
	"context"

	"github.com/persys-dev/persys-cloud/sdk/types"
)

// clientIface is the subset of client.Client used by the builder.
// Defined as an interface so the builder can be tested in isolation.
type clientIface interface {
	ApplyWorkload(ctx context.Context, w *types.Workload) (*types.WorkloadStatus, error)
	DeleteWorkload(ctx context.Context, workloadID string) error
	RetryWorkload(ctx context.Context, workloadID string) (*types.WorkloadStatus, error)
	ListWorkloads(ctx context.Context, status string) ([]*types.WorkloadStatus, error)
	GetWorkload(ctx context.Context, workloadID string) (*types.WorkloadStatus, error)
}

// Builder is the fluent entry point for workload operations.
// Obtain one via sdk.Client.Workloads().
type Builder struct {
	c      clientIface
	status string
}

// NewBuilder creates a workload Builder backed by the given client.
func NewBuilder(c clientIface) *Builder {
	return &Builder{c: c}
}

// WithStatus filters list results to workloads in the given status
// (e.g. "running", "failed", "pending").
//
//	running, err := client.Workloads().WithStatus("running").List(ctx)
func (b *Builder) WithStatus(status string) *Builder {
	cp := *b
	cp.status = status
	return &cp
}

// List returns all workloads visible to the current tenant.
// Use WithStatus to filter by lifecycle state.
func (b *Builder) List(ctx context.Context) ([]*types.WorkloadStatus, error) {
	return b.c.ListWorkloads(ctx, b.status)
}

// Get returns a single workload by its ID.
func (b *Builder) Get(ctx context.Context, workloadID string) (*types.WorkloadStatus, error) {
	return b.c.GetWorkload(ctx, workloadID)
}

// Create schedules a new workload on the cluster.
//
//	status, err := client.Workloads().Create(ctx, sdk.Workload{
//	    Name:  "web",
//	    Image: "nginx:latest",
//	    Resources: types.ResourceRequirements{CPU: 0.5, MemoryMB: 256},
//	})
func (b *Builder) Create(ctx context.Context, w types.Workload) (*types.WorkloadStatus, error) {
	return b.c.ApplyWorkload(ctx, &w)
}

// Apply is an alias for Create that emphasises idempotent desired-state
// semantics (creates or updates).
func (b *Builder) Apply(ctx context.Context, w types.Workload) (*types.WorkloadStatus, error) {
	return b.c.ApplyWorkload(ctx, &w)
}

// Delete removes a workload from the cluster.
func (b *Builder) Delete(ctx context.Context, workloadID string) error {
	return b.c.DeleteWorkload(ctx, workloadID)
}

// Retry re-queues a failed workload.
func (b *Builder) Retry(ctx context.Context, workloadID string) (*types.WorkloadStatus, error) {
	return b.c.RetryWorkload(ctx, workloadID)
}
