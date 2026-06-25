// Package client implements the Persys Cloud SDK client.
package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	controlv1 "github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1"
	"github.com/persys-dev/persys-cloud/sdk/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Options is re-exported for callers that import only the client package.
type Options = options.Options

// DefaultOptions returns SDK defaults.
func DefaultOptions() *Options { return options.DefaultOptions() }

// PersysClient is the main public API for Persys Cloud interactions.
type PersysClient interface {
	ApplyWorkload(context.Context, *controlv1.ApplyWorkloadRequest) (*controlv1.ApplyWorkloadResponse, error)
	DeleteWorkload(context.Context, *controlv1.DeleteWorkloadRequest) (*controlv1.DeleteWorkloadResponse, error)
	RetryWorkload(context.Context, *controlv1.RetryWorkloadRequest) (*controlv1.RetryWorkloadResponse, error)
	ListNodes(context.Context, *controlv1.ListNodesRequest) (*controlv1.ListNodesResponse, error)
	GetNode(context.Context, *controlv1.GetNodeRequest) (*controlv1.GetNodeResponse, error)
	ListWorkloads(context.Context, *controlv1.ListWorkloadsRequest) (*controlv1.ListWorkloadsResponse, error)
	GetWorkload(context.Context, *controlv1.GetWorkloadRequest) (*controlv1.GetWorkloadResponse, error)
	GetClusterSummary(context.Context, *controlv1.GetClusterSummaryRequest) (*controlv1.GetClusterSummaryResponse, error)
	Close() error
}

// Client is a reusable Persys Cloud client that supports gRPC and HTTP transport.
type Client struct {
	opts *options.Options
	http *http.Client
	conn *grpc.ClientConn
	grpc controlv1.AgentControlClient
}

// New creates a Persys SDK client from options.
func New(opts *options.Options) (*Client, error) {
	if opts == nil {
		opts = options.DefaultOptions()
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	c := &Client{opts: opts, http: &http.Client{Timeout: opts.Timeout}}
	switch opts.Transport {
	case "", options.TransportGRPC:
		dialOpts := []grpc.DialOption{}
		if opts.Insecure {
			dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		} else {
			tlsCfg, err := LoadTLSConfig(opts)
			if err != nil {
				return nil, err
			}
			dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
		}
		conn, err := grpc.DialContext(context.Background(), opts.GRPCEndpoint, dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("create grpc client: %w", err)
		}
		c.conn = conn
		c.grpc = controlv1.NewAgentControlClient(conn)
	case options.TransportHTTP:
		if strings.TrimSpace(opts.APIEndpoint) == "" {
			return nil, fmt.Errorf("api endpoint is required for http transport")
		}
	default:
		return nil, fmt.Errorf("unsupported transport %q", opts.Transport)
	}
	return c, nil
}

func (c *Client) httpURL(path string, query map[string]string) (string, error) {
	if c == nil || c.opts == nil {
		return "", fmt.Errorf("client is not configured")
	}
	base, err := url.Parse(strings.TrimRight(c.opts.APIEndpoint, "/"))
	if err != nil {
		return "", fmt.Errorf("parse api endpoint: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	values := base.Query()
	for k, v := range query {
		if strings.TrimSpace(v) != "" {
			values.Set(k, v)
		}
	}
	base.RawQuery = values.Encode()
	return base.String(), nil
}

func (c *Client) doProtoHTTP(ctx context.Context, method, path string, in proto.Message, out proto.Message, query map[string]string) error {
	endpoint, err := c.httpURL(path, query)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create http request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send http request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read http response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %s %s failed with status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// Close releases client resources.
func (c *Client) Close() error {
	if c != nil && c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Client) requireGRPC() (controlv1.AgentControlClient, error) {
	if c == nil || c.grpc == nil {
		return nil, fmt.Errorf("grpc transport is not configured")
	}
	return c.grpc, nil
}

// ApplyWorkload applies or updates a workload.
func (c *Client) ApplyWorkload(ctx context.Context, req *controlv1.ApplyWorkloadRequest) (*controlv1.ApplyWorkloadResponse, error) {
	if c != nil && c.opts != nil && c.opts.Transport == options.TransportHTTP {
		resp := &controlv1.ApplyWorkloadResponse{}
		if err := c.doProtoHTTP(ctx, http.MethodPost, "/workloads/schedule", req, resp, nil); err != nil {
			return nil, fmt.Errorf("apply workload: %w", err)
		}
		return resp, nil
	}
	gc, err := c.requireGRPC()
	if err != nil {
		return nil, err
	}
	resp, err := gc.ApplyWorkload(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("apply workload: %w", err)
	}
	return resp, nil
}

// DeleteWorkload deletes a workload.
func (c *Client) DeleteWorkload(ctx context.Context, req *controlv1.DeleteWorkloadRequest) (*controlv1.DeleteWorkloadResponse, error) {
	if c != nil && c.opts != nil && c.opts.Transport == options.TransportHTTP {
		resp := &controlv1.DeleteWorkloadResponse{}
		if err := c.doProtoHTTP(ctx, http.MethodDelete, "/workloads/"+url.PathEscape(req.GetWorkloadId()), nil, resp, nil); err != nil {
			return nil, fmt.Errorf("delete workload: %w", err)
		}
		return resp, nil
	}
	gc, err := c.requireGRPC()
	if err != nil {
		return nil, err
	}
	resp, err := gc.DeleteWorkload(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("delete workload: %w", err)
	}
	return resp, nil
}

// RetryWorkload retries a workload.
func (c *Client) RetryWorkload(ctx context.Context, req *controlv1.RetryWorkloadRequest) (*controlv1.RetryWorkloadResponse, error) {
	if c != nil && c.opts != nil && c.opts.Transport == options.TransportHTTP {
		resp := &controlv1.RetryWorkloadResponse{}
		if err := c.doProtoHTTP(ctx, http.MethodPost, "/workloads/"+url.PathEscape(req.GetWorkloadId())+"/retry", nil, resp, nil); err != nil {
			return nil, fmt.Errorf("retry workload: %w", err)
		}
		return resp, nil
	}
	gc, err := c.requireGRPC()
	if err != nil {
		return nil, err
	}
	resp, err := gc.RetryWorkload(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("retry workload: %w", err)
	}
	return resp, nil
}

// ListNodes lists scheduler nodes.
func (c *Client) ListNodes(ctx context.Context, req *controlv1.ListNodesRequest) (*controlv1.ListNodesResponse, error) {
	if c != nil && c.opts != nil && c.opts.Transport == options.TransportHTTP {
		resp := &controlv1.ListNodesResponse{}
		if err := c.doProtoHTTP(ctx, http.MethodGet, "/nodes", nil, resp, map[string]string{"status": req.GetStatus()}); err != nil {
			return nil, fmt.Errorf("list nodes: %w", err)
		}
		return resp, nil
	}
	gc, err := c.requireGRPC()
	if err != nil {
		return nil, err
	}
	resp, err := gc.ListNodes(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	return resp, nil
}

// GetNode gets a node.
func (c *Client) GetNode(ctx context.Context, req *controlv1.GetNodeRequest) (*controlv1.GetNodeResponse, error) {
	if c != nil && c.opts != nil && c.opts.Transport == options.TransportHTTP {
		resp := &controlv1.GetNodeResponse{}
		if err := c.doProtoHTTP(ctx, http.MethodGet, "/nodes/"+url.PathEscape(req.GetNodeId()), nil, resp, nil); err != nil {
			return nil, fmt.Errorf("get node: %w", err)
		}
		return resp, nil
	}
	gc, err := c.requireGRPC()
	if err != nil {
		return nil, err
	}
	resp, err := gc.GetNode(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get node: %w", err)
	}
	return resp, nil
}

// ListWorkloads lists workloads.
func (c *Client) ListWorkloads(ctx context.Context, req *controlv1.ListWorkloadsRequest) (*controlv1.ListWorkloadsResponse, error) {
	if c != nil && c.opts != nil && c.opts.Transport == options.TransportHTTP {
		resp := &controlv1.ListWorkloadsResponse{}
		if err := c.doProtoHTTP(ctx, http.MethodGet, "/workloads", nil, resp, map[string]string{"status": req.GetStatus()}); err != nil {
			return nil, fmt.Errorf("list workloads: %w", err)
		}
		return resp, nil
	}
	gc, err := c.requireGRPC()
	if err != nil {
		return nil, err
	}
	resp, err := gc.ListWorkloads(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("list workloads: %w", err)
	}
	return resp, nil
}

// GetWorkload gets a workload.
func (c *Client) GetWorkload(ctx context.Context, req *controlv1.GetWorkloadRequest) (*controlv1.GetWorkloadResponse, error) {
	if c != nil && c.opts != nil && c.opts.Transport == options.TransportHTTP {
		resp := &controlv1.GetWorkloadResponse{}
		if err := c.doProtoHTTP(ctx, http.MethodGet, "/workloads/"+url.PathEscape(req.GetWorkloadId()), nil, resp, nil); err != nil {
			return nil, fmt.Errorf("get workload: %w", err)
		}
		return resp, nil
	}
	gc, err := c.requireGRPC()
	if err != nil {
		return nil, err
	}
	resp, err := gc.GetWorkload(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get workload: %w", err)
	}
	return resp, nil
}

// GetClusterSummary gets cluster status.
func (c *Client) GetClusterSummary(ctx context.Context, req *controlv1.GetClusterSummaryRequest) (*controlv1.GetClusterSummaryResponse, error) {
	if c != nil && c.opts != nil && c.opts.Transport == options.TransportHTTP {
		resp := &controlv1.GetClusterSummaryResponse{}
		if err := c.doProtoHTTP(ctx, http.MethodGet, "/cluster/metrics", nil, resp, nil); err != nil {
			return nil, fmt.Errorf("get cluster summary: %w", err)
		}
		return resp, nil
	}
	gc, err := c.requireGRPC()
	if err != nil {
		return nil, err
	}
	resp, err := gc.GetClusterSummary(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get cluster summary: %w", err)
	}
	return resp, nil
}
