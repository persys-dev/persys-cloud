// Package resources provides high-level resource builders.
package resources

import (
	"context"

	"github.com/persys-dev/persys-cloud/sdk/client"
	"github.com/persys-dev/persys-cloud/sdk/types"
)

// Workloads is a convenience wrapper (delegates to workloads.Builder)
type Workloads struct {
	c *client.Client
}

func NewWorkloads(c *client.Client) *Workloads {
	return &Workloads{c: c}
}

func (w *Workloads) Create(ctx context.Context, workload types.Workload) (*types.WorkloadStatus, error) {
	// Delegate to client
	return w.c.ApplyWorkload(ctx, &workload) // assuming you added this method
}

func (w *Workloads) List(ctx context.Context, status string) ([]*types.WorkloadStatus, error) {
	return w.c.ListWorkloads(ctx, status)
}

func (w *Workloads) Get(ctx context.Context, id string) (*types.WorkloadStatus, error) {
	return w.c.GetWorkload(ctx, id)
}

func (w *Workloads) Delete(ctx context.Context, id string) error {
	return w.c.DeleteWorkload(ctx, id)
}

// Nodes provides fluent node operations
type Nodes struct {
	c *client.Client
}

func NewNodes(c *client.Client) *Nodes {
	return &Nodes{c: c}
}

func (n *Nodes) List(ctx context.Context, status string) ([]*types.Node, error) {
	return n.c.ListNodes(ctx, status)
}

func (n *Nodes) Get(ctx context.Context, id string) (*types.Node, error) {
	return n.c.GetNode(ctx, id)
}

func (n *Nodes) Drain(ctx context.Context, nodeID, reason string) error {
	return n.c.DrainNode(ctx, nodeID, reason)
}

func (n *Nodes) Undrain(ctx context.Context, nodeID, reason string) error {
	return n.c.UndrainNode(ctx, nodeID, reason)
}

func (n *Nodes) Taint(ctx context.Context, nodeID, key, value, effect string) error {
	return n.c.TaintNode(ctx, nodeID, key, value, effect)
}

func (n *Nodes) Untaint(ctx context.Context, nodeID, key, effect string) error {
	return n.c.UntaintNode(ctx, nodeID, key, effect)
}

func (n *Nodes) SetLabel(ctx context.Context, nodeID, key, value string) error {
	return n.c.SetNodeLabel(ctx, nodeID, key, value)
}

func (n *Nodes) DeleteLabel(ctx context.Context, nodeID, key string) error {
	return n.c.DeleteNodeLabel(ctx, nodeID, key)
}

// Clusters (basic)
type Clusters struct {
	c *client.Client
}

func NewClusters(c *client.Client) *Clusters {
	return &Clusters{c: c}
}

func (cl *Clusters) Summary(ctx context.Context) (map[string]interface{}, error) {
	return cl.c.GetClusterSummary(ctx)
}

func (cl *Clusters) List(ctx context.Context) ([]map[string]interface{}, error) {
	return cl.c.ListClusters(ctx)
}

func (cl *Clusters) Metrics(ctx context.Context) (map[string]interface{}, error) {
	return cl.c.GetClusterMetrics(ctx)
}

// Forgery provides CI/CD related operations
type Forgery struct {
	c *client.Client
}

func NewForgery(c *client.Client) *Forgery {
	return &Forgery{c: c}
}

func (f *Forgery) UpsertProject(ctx context.Context, spec any) error {
	return f.c.ForgeryUpsertProject(ctx, spec)
}

func (f *Forgery) TriggerBuild(ctx context.Context, spec any) error {
	return f.c.ForgeryTriggerBuild(ctx, spec)
}

func (f *Forgery) TestWebhook(ctx context.Context, spec any) error {
	return f.c.ForgeryTestWebhook(ctx, spec)
}