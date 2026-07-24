// Package client implements the Persys SDK API Gateway client.
//
// The client communicates only with the Persys API Gateway.
// Internal services (scheduler, compute-agent, etc) are not exposed here.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/sdk/identity"
	"github.com/persys-dev/persys-cloud/sdk/types"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
}

// Options configures the API client.
type Options struct {
	BaseURL  string
	Identity identity.Provider
	Timeout  time.Duration
}

type APIError struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api error %d: %s", e.StatusCode, e.Body)
}

// New creates a new Persys API client.
func New(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("api endpoint is required")
	}
	if opts.Identity == nil {
		return nil, fmt.Errorf("identity provider is required")
	}

	tlsConfig, err := opts.Identity.TLSConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("create tls config: %w", err)
	}

	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}

	httpClient := &http.Client{
		Timeout: opts.Timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	return &Client{
		baseURL:    strings.TrimRight(opts.BaseURL, "/"),
		httpClient: httpClient,
		timeout:    opts.Timeout,
	}, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
	return nil
}

// request is the core helper
func (c *Client) request(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("api request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(data)}
	}

	if out == nil || len(data) == 0 {
		return nil
	}

	return json.Unmarshal(data, out)
}

// --- Convenience methods ---
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.request(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.request(ctx, http.MethodPost, path, body, out)
}

func (c *Client) Delete(ctx context.Context, path string, out any) error {
	return c.request(ctx, http.MethodDelete, path, nil, out)
}

func EncodePath(value string) string {
	return url.PathEscape(value)
}

func (c *Client) ApplyWorkload(ctx context.Context, w *types.Workload) (*types.WorkloadStatus, error) {
	var status types.WorkloadStatus
	err := c.Post(ctx, "/workloads", w, &status)
	return &status, err
}

func (c *Client) DeleteWorkload(ctx context.Context, workloadID string) error {
	return c.Delete(ctx, "/workloads/"+EncodePath(workloadID), nil)
}

func (c *Client) RetryWorkload(ctx context.Context, workloadID string) (*types.WorkloadStatus, error) {
	var status types.WorkloadStatus
	err := c.Post(ctx, "/workloads/"+EncodePath(workloadID)+"/retry", nil, &status)
	return &status, err
}

func (c *Client) ListWorkloads(ctx context.Context, status string) ([]*types.WorkloadStatus, error) {
	path := "/workloads"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var list []*types.WorkloadStatus
	err := c.Get(ctx, path, &list)
	return list, err
}

func (c *Client) GetWorkload(ctx context.Context, workloadID string) (*types.WorkloadStatus, error) {
	var status types.WorkloadStatus
	err := c.Get(ctx, "/workloads/"+EncodePath(workloadID), &status)
	return &status, err
}

// Node operations
func (c *Client) ListNodes(ctx context.Context, status string) ([]*types.Node, error) {
	path := "/nodes"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var nodes []*types.Node
	err := c.Get(ctx, path, &nodes)
	return nodes, err
}

func (c *Client) GetNode(ctx context.Context, nodeID string) (*types.Node, error) {
	var node types.Node
	err := c.Get(ctx, "/nodes/"+EncodePath(nodeID), &node)
	return &node, err
}

// Node management from PR #25
func (c *Client) DrainNode(ctx context.Context, nodeID, reason string) error {
	payload := map[string]string{"reason": reason}
	return c.Post(ctx, "/nodes/"+EncodePath(nodeID)+"/drain", payload, nil)
}

func (c *Client) UndrainNode(ctx context.Context, nodeID, reason string) error {
	payload := map[string]string{"reason": reason}
	return c.Post(ctx, "/nodes/"+EncodePath(nodeID)+"/undrain", payload, nil)
}

func (c *Client) TaintNode(ctx context.Context, nodeID, key, value, effect string) error {
	payload := map[string]string{
		"key":    key,
		"value":  value,
		"effect": effect,
	}
	return c.Post(ctx, "/nodes/"+EncodePath(nodeID)+"/taint", payload, nil)
}

func (c *Client) UntaintNode(ctx context.Context, nodeID, key, effect string) error {
	payload := map[string]string{
		"key":    key,
		"effect": effect,
	}
	return c.Post(ctx, "/nodes/"+EncodePath(nodeID)+"/untaint", payload, nil)
}

func (c *Client) SetNodeLabel(ctx context.Context, nodeID, key, value string) error {
	payload := map[string]string{"key": key, "value": value}
	return c.Post(ctx, "/nodes/"+EncodePath(nodeID)+"/labels", payload, nil)
}

func (c *Client) DeleteNodeLabel(ctx context.Context, nodeID, key string) error {
	return c.Delete(ctx, "/nodes/"+EncodePath(nodeID)+"/labels/"+EncodePath(key), nil)
}

// Cluster operations (basic)
func (c *Client) GetClusterSummary(ctx context.Context) (map[string]interface{}, error) {
	var summary map[string]interface{}
	err := c.Get(ctx, "/cluster/summary", &summary)
	return summary, err
}

// ListClusters returns cluster routing/state info from gateway.
func (c *Client) ListClusters(ctx context.Context) ([]map[string]interface{}, error) {
	var clusters []map[string]interface{}
	err := c.Get(ctx, "/clusters", &clusters)
	return clusters, err
}

// GetClusterMetrics returns cluster-wide metrics.
func (c *Client) GetClusterMetrics(ctx context.Context) (map[string]interface{}, error) {
	var metrics map[string]interface{}
	err := c.Get(ctx, "/cluster/metrics", &metrics)
	return metrics, err
}

// Forgery operations for CI/CD pipelines.
func (c *Client) ForgeryUpsertProject(ctx context.Context, spec any) error {
	return c.Post(ctx, "/forgery/projects/upsert", spec, nil)
}

func (c *Client) ForgeryTriggerBuild(ctx context.Context, spec any) error {
	return c.Post(ctx, "/forgery/builds/trigger", spec, nil)
}

func (c *Client) ForgeryTestWebhook(ctx context.Context, spec any) error {
	return c.Post(ctx, "/forgery/webhooks/test", spec, nil)
}

// ScheduleWorkload provides explicit scheduling endpoint if separate from Apply.
func (c *Client) ScheduleWorkload(ctx context.Context, w *types.Workload) (*types.WorkloadStatus, error) {
	var status types.WorkloadStatus
	err := c.Post(ctx, "/workloads/schedule", w, &status)
	return &status, err
}
