// bindings.go is the single place the gateway's entire dynamic RPC
// surface is declared. Adding a method for persysctl or the SDK to call
// is very likely a one-line addition here — a MethodAlias, or nothing at
// all if the generic /rpc/<Service>/<Method> path is fine.
//
// Any alias whose Path has a :param MUST set PathParams mapping it to
// the proto field it feeds — see grpcbridge.MethodAlias.PathParams. This
// was the source of a real bug: routes like GET /workloads/:id worked at
// the routing layer but silently sent an empty workload_id to the
// backend, because nothing was translating the URL segment into the
// request message. Every alias below with a :id/:name segment has been
// checked against the actual generated proto field name (not guessed) —
// see the field comments.
package router

import (
	"github.com/gin-gonic/gin"

	"github.com/persys-dev/persys-cloud/persys-gateway/internal/catalog"
	controlv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/controlv1"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/forgeryv1"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/grpcbridge"
)

// controlv1 and forgeryv1 are imported here (not just in services/) purely
// to supply LocalFile below — the compiled-in fallback descriptor used
// when a backend doesn't yet support gRPC reflection (see
// grpcbridge.ServiceBinding.LocalFile doc). go.mod already has
//   replace github.com/persys-dev/persys-cloud/pkg => ../pkg
// for shared code like certmanager. If/when these generated packages
// move to that shared pkg module too (so scheduler, compute-agent, and
// this gateway all build from one canonical copy instead of each
// vendoring their own), this is the only import to change — everything
// else in this file references controlv1./forgeryv1. symbols generically.

// ClusterControlBindingInvoker is satisfied by services.ClusterControlService.
type ClusterControlBindingInvoker interface {
	grpcbridge.Invoker
	grpcbridge.ReflectionSource
}

// ClusterControlBinding is the workload/node scheduling API — talks to
// AgentControl on the per-cluster scheduler pool, HA-aware failover
// preserved via ClusterControlService.InvokeDynamic (the exact same
// candidate/dial/retry loop invokeControlRPC has always used).
//
// Aliases below are the STABLE, documented paths the SDK and persysctl
// should target. Anything not listed is still fully callable at
// /clusters/:cluster_id/rpc/AgentControl/<Method> — check
// GET /clusters/:cluster_id/rpc/_meta for the live list, including
// brand-new methods that haven't been aliased yet.
func ClusterControlBinding(clusterControl ClusterControlBindingInvoker, r *Router) grpcbridge.ServiceBinding {
	return grpcbridge.ServiceBinding{
		FullyQualifiedName: "persys.control.v1.AgentControl",
		GroupPrefix:        "",
		Invoker:            clusterControl,
		Source:             clusterControl,
		LocalFile:          controlv1.File_control_proto,
		Auth:               func() gin.HandlerFunc { return r.Resolve(catalog.AuthUser) },
		Aliases: []grpcbridge.MethodAlias{
			{Method: "ApplyWorkload", Verb: "POST", Path: "/workloads/schedule"},
			{Method: "ListWorkloads", Verb: "GET", Path: "/workloads"},
			// GetWorkloadRequest.WorkloadId — json name "workload_id"
			{Method: "GetWorkload", Verb: "GET", Path: "/workloads/:id", PathParams: map[string]string{"id": "workload_id"}},
			// DeleteWorkloadRequest.WorkloadId
			{Method: "DeleteWorkload", Verb: "DELETE", Path: "/workloads/:id", PathParams: map[string]string{"id": "workload_id"}},
			// RetryWorkloadRequest.WorkloadId
			{Method: "RetryWorkload", Verb: "POST", Path: "/workloads/:id/retry", PathParams: map[string]string{"id": "workload_id"}},
			{Method: "ListNodes", Verb: "GET", Path: "/nodes"},
			// GetNodeRequest.NodeId — json name "node_id"
			{Method: "GetNode", Verb: "GET", Path: "/nodes/:id", PathParams: map[string]string{"id": "node_id"}},
			// DrainNodeRequest.NodeId (Reason comes from the JSON body)
			{Method: "DrainNode", Verb: "POST", Path: "/nodes/:id/drain", PathParams: map[string]string{"id": "node_id"}},
			// UndrainNodeRequest.NodeId (Reason comes from the JSON body)
			{Method: "UndrainNode", Verb: "POST", Path: "/nodes/:id/undrain", PathParams: map[string]string{"id": "node_id"}},
			// TaintNodeRequest.NodeId. NOTE: TaintNodeRequest.Taint is a
			// NESTED message (*NodeTaint{Key,Value,Effect}), unlike every
			// other alias here — the client must send a nested
			// {"taint":{"key":...,"value":...,"effect":...}} body, not a
			// flat one. PathParams only reaches top-level string fields.
			{Method: "TaintNode", Verb: "POST", Path: "/nodes/:id/taint", PathParams: map[string]string{"id": "node_id"}},
			// UntaintNodeRequest.NodeId (Key/Effect are flat top-level
			// fields, unlike TaintNode — a flat JSON body is correct here)
			{Method: "UntaintNode", Verb: "POST", Path: "/nodes/:id/untaint", PathParams: map[string]string{"id": "node_id"}},
			// SetNodeLabelRequest.NodeId (Key/Value are flat top-level fields)
			{Method: "SetNodeLabel", Verb: "POST", Path: "/nodes/:id/labels", PathParams: map[string]string{"id": "node_id"}},
			// DeleteNodeLabelRequest.NodeId (Key is a flat top-level field)
			{Method: "DeleteNodeLabel", Verb: "DELETE", Path: "/nodes/:id/labels", PathParams: map[string]string{"id": "node_id"}},
			{Method: "GetClusterSummary", Verb: "GET", Path: "/cluster/metrics"},
			// Standalone disks (AgentControl mTLS gRPC)
			{Method: "ListDisks", Verb: "GET", Path: "/disks"},
			{Method: "CreateDisk", Verb: "POST", Path: "/disks"},
			{Method: "GetDisk", Verb: "GET", Path: "/disks/:id", PathParams: map[string]string{"id": "disk_id"}},
			{Method: "DeleteDisk", Verb: "DELETE", Path: "/disks/:id", PathParams: map[string]string{"id": "disk_id"}},
			// Object storage (Ceph RGW / S3)
			{Method: "ListBuckets", Verb: "GET", Path: "/buckets"},
			{Method: "CreateBucket", Verb: "POST", Path: "/buckets"},
			{Method: "GetBucket", Verb: "GET", Path: "/buckets/:id", PathParams: map[string]string{"id": "bucket_id"}},
			{Method: "DeleteBucket", Verb: "DELETE", Path: "/buckets/:id", PathParams: map[string]string{"id": "bucket_id"}},
			{Method: "GetBucketAccess", Verb: "GET", Path: "/buckets/:id/access", PathParams: map[string]string{"id": "bucket_id"}},
			{Method: "ListBucketObjects", Verb: "GET", Path: "/buckets/:id/objects", PathParams: map[string]string{"id": "bucket_id"}},
			// RegisterNode, Heartbeat: intentionally NOT aliased — those
			// are compute-agent-to-scheduler internal calls, not
			// SDK/persysctl surface. Still technically reachable via
			// /rpc/AgentControl/RegisterNode if something needs it, but
			// nothing should be pointed at that path deliberately.
		},
	}
}

// ForgeryBindingInvoker is satisfied by services.ForgeryService.
type ForgeryBindingInvoker interface {
	grpcbridge.Invoker
	grpcbridge.ReflectionSource
}

// ForgeryBinding is the CI/CD API — talks to ForgeryControl on
// persys-forgery's single fixed address, no pool.
func ForgeryBinding(forgery ForgeryBindingInvoker, r *Router) grpcbridge.ServiceBinding {
	return grpcbridge.ServiceBinding{
		FullyQualifiedName: "persys.forgery.v1.ForgeryControl",
		GroupPrefix:        "/forgery",
		Invoker:            forgery,
		Source:             forgery,
		LocalFile:          forgeryv1.File_forgery_proto,
		Auth:               func() gin.HandlerFunc { return r.Resolve(catalog.AuthUser) },
		Aliases: []grpcbridge.MethodAlias{
			{Method: "UpsertProject", Verb: "POST", Path: "/projects/upsert"},
			// GetProjectRequest field is Name, NOT a "project_id" — do
			// not assume REST-style ID naming without checking the proto.
			{Method: "GetProject", Verb: "GET", Path: "/projects/:id", PathParams: map[string]string{"id": "name"}},
			{Method: "ListProjects", Verb: "GET", Path: "/projects"},
			// DeleteProjectRequest field is also Name.
			{Method: "DeleteProject", Verb: "DELETE", Path: "/projects/:id", PathParams: map[string]string{"id": "name"}},
			{Method: "TriggerBuild", Verb: "POST", Path: "/builds/trigger"},
			{Method: "ListPipelineStatus", Verb: "GET", Path: "/pipeline/status"},
			{Method: "ForwardWebhook", Verb: "POST", Path: "/webhooks/test"},
			{Method: "RegisterWebhook", Verb: "POST", Path: "/webhooks/register"},
			{Method: "ListUserRepositories", Verb: "GET", Path: "/repositories"},
			// StoreGitHubCredential: NOT aliased — takes a raw token in
			// the request body. Worth a deliberate look at whether it
			// belongs behind RequireUser only (never RequireMTLSOrUser)
			// before giving it a stable path at all.
		},
	}
}
