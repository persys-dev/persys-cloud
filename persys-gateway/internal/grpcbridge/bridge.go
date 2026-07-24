// Package grpcbridge turns any reflection-enabled gRPC service into a set
// of gin routes with zero generated-stub boilerplate: no per-RPC wrapper
// method, no per-RPC controller handler, no per-RPC route line.
//
// A Bridge can host any number of independent backends at once — each
// ServiceBinding carries its own Invoker and ReflectionSource, so a
// pool-aware, failover-capable backend (cluster control, many scheduler
// replicas) and a single-address backend (forgery, one fixed endpoint)
// register onto the same Bridge without either shape leaking into the
// other.
//
// What it deliberately does NOT do: replace connection selection, retry,
// or health tracking — that's each binding's Invoker's job. What it also
// does NOT do: bridge streaming RPCs. Any bidi/server-stream method is
// skipped during discovery and stays hand-written.
package grpcbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Invoker performs one RPC and owns everything about *how* — which
// backend to dial, retry/failover, health tracking. A pool-aware backend
// (cluster control, ranking scheduler replicas per cluster/session/
// workload) and a single-address backend (forgery) both implement this
// the same way they'd have dialed and called anyway; grpcbridge doesn't
// care which.
type Invoker interface {
	InvokeDynamic(ctx context.Context, clusterID, sessionKey, workloadKey, fullMethod string, in, out proto.Message) error
}

// ReflectionSource is anything that can open a connection for reflection
// queries. Kept separate from Invoker because descriptor discovery
// shouldn't participate in a pool's request-serving health/failover
// state — querying any one healthy replica is sufficient, since
// descriptors are identical across replicas of the same deployed version.
type ReflectionSource interface {
	DialForReflection(ctx context.Context, clusterID string) (*grpc.ClientConn, error)
}

// MethodAlias gives a friendlier, stable REST path to a method that has
// an established public API shape. Anything without an alias still gets
// the generic /rpc/<Service>/<Method> path automatically — that's what
// makes a newly deployed RPC reachable, and discoverable via /rpc/_meta,
// with zero gateway changes. Promoting it to a nicer path later is
// purely a config addition, never a new handler.
type MethodAlias struct {
	Method string // e.g. "ApplyWorkload"
	Verb   string // e.g. "POST"
	Path   string // e.g. "/workloads/schedule" (relative to GroupPrefix)

	// PathParams maps a gin path param name to the proto request field
	// it should populate, e.g. {"id": "workload_id"} for
	// "/workloads/:id". This is exactly what the old hand-written
	// controllers did implicitly (`req.WorkloadId = ctx.Param("id")`)
	// before being replaced by generic reflection-based dispatch — the
	// generic path has no other way to know a URL segment corresponds
	// to a specific field, so routes with path params MUST set this or
	// the field is silently left empty. Only string-typed top-level
	// fields are supported; nested fields need the client to send them
	// nested in the JSON body instead (see e.g. TaintNodeRequest.Taint).
	PathParams map[string]string
}

// AuthResolver lets each bound service pick its own auth requirement.
type AuthResolver func() gin.HandlerFunc

// KeyResolver extracts the cluster/session/workload affinity keys a
// pool-aware Invoker uses for candidate ranking (e.g. rendezvous hashing
// across scheduler replicas). Defaults to DefaultKeyResolver, which
// replicates the exact resolution order the gateway has always used
// (path param, then header, then cookie/query, then a hash of the
// Authorization header as a last resort) — this affects which backend
// replica a request lands on, so it's not just cosmetic and is worth
// keeping byte-for-byte consistent with prior behavior.
type KeyResolver interface {
	ResolveClusterID(c *gin.Context) string
	ResolveSessionKey(c *gin.Context) string
	ResolveWorkloadKey(c *gin.Context) string
}

type defaultKeyResolver struct{}

// DefaultKeyResolver is used by any ServiceBinding that doesn't set its
// own Keys.
var DefaultKeyResolver KeyResolver = defaultKeyResolver{}

func (defaultKeyResolver) ResolveClusterID(c *gin.Context) string {
	if v := strings.TrimSpace(c.Param("cluster_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.GetHeader("X-Persys-Cluster-ID")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Query("cluster_id")); v != "" {
		return v
	}
	return ""
}

func (defaultKeyResolver) ResolveSessionKey(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("X-Persys-Session")); v != "" {
		return v
	}
	if cookie, err := c.Cookie("persys_session"); err == nil && strings.TrimSpace(cookie) != "" {
		return cookie
	}
	authz := strings.TrimSpace(c.GetHeader("Authorization"))
	if authz == "" {
		return strings.TrimSpace(c.ClientIP())
	}
	sum := sha256.Sum256([]byte(authz))
	return hex.EncodeToString(sum[:])
}

func (defaultKeyResolver) ResolveWorkloadKey(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("X-Persys-Workload-Key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Param("id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Query("workload_id")); v != "" {
		return v
	}
	return c.Request.URL.Path
}

// ServiceBinding fully describes one backend's presence on the gateway.
type ServiceBinding struct {
	// FullyQualifiedName is the proto service name reflection reports,
	// e.g. "persys.control.v1.AgentControl".
	FullyQualifiedName string

	// GroupPrefix is where this service's routes mount, relative to
	// wherever Register is called — e.g. "" to sit directly under
	// /clusters/:cluster_id, or "/forgery" to sit under
	// /clusters/:cluster_id/forgery.
	GroupPrefix string

	// Invoker and Source are THIS binding's backend — independent per
	// binding, so different backends can have entirely different
	// connection shapes on the same Bridge.
	Invoker Invoker
	Source  ReflectionSource

	// Keys resolves cluster/session/workload affinity for Invoker. Nil
	// means DefaultKeyResolver.
	Keys KeyResolver

	Auth    AuthResolver
	Aliases []MethodAlias

	// LocalFile is the compiled-in FileDescriptor for this service — the
	// same one already embedded in the generated .pb.go this gateway
	// links against (e.g. controlv1.File_control_proto). It's the
	// backward-compatible fallback: if the backend doesn't implement
	// gRPC reflection yet (an older scheduler/forgery deployment that
	// predates reflection.Register being wired up there), the bridge
	// builds its method table from this instead of failing to discover
	// anything at all. Every method that exists in the gateway's own
	// compiled proto is covered by this path with zero dependency on the
	// backend; reflection only adds methods added to the backend's proto
	// AFTER this gateway build — which the fallback can't know about
	// until reflection becomes available or the gateway is rebuilt
	// against a newer proto.
	//
	// Required if you want the binding to work at all against a backend
	// without reflection. Leave nil only if you're certain every
	// deployment target has reflection.Register wired up.
	LocalFile protoreflect.FileDescriptor

	// RefreshInterval controls how often descriptors are re-fetched, so
	// a backend redeploy with new RPCs shows up without a gateway
	// restart. Defaults to 60s. Also governs how often a backend that
	// currently lacks reflection is re-checked — if it's upgraded later,
	// the bridge upgrades to live discovery automatically, no gateway
	// restart needed either way.
	RefreshInterval time.Duration

	// reflectionUnavailableLogged tracks whether we've already logged the
	// "using fallback" notice for this binding, so refreshLoop doesn't
	// repeat it every tick. Internal — not set by callers.
	reflectionUnavailableLogged bool
}

func (svc ServiceBinding) keys() KeyResolver {
	if svc.Keys != nil {
		return svc.Keys
	}
	return DefaultKeyResolver
}

type Bridge struct {
	mu       sync.RWMutex
	services map[string]*boundService
}

type boundService struct {
	binding ServiceBinding
	methods []methodDesc
}

type methodDesc struct {
	name       string
	fullMethod string
	input      protoreflect.MessageDescriptor
	output     protoreflect.MessageDescriptor
}

func New() *Bridge {
	return &Bridge{services: map[string]*boundService{}}
}

// Register implements router.Registrar. It performs one reflection
// query per bound service to discover methods, wires a generic handler
// per method (aliased or not), and starts a background refresh loop per
// service so newly deployed RPCs appear without restarting the gateway.
func (b *Bridge) Register(mountGroup *gin.RouterGroup, bindings ...ServiceBinding) error {
	for _, svc := range bindings {
		svc := svc
		b.mu.Lock()
		b.services[svc.FullyQualifiedName] = &boundService{binding: svc}
		b.mu.Unlock()

		if err := b.refresh(context.Background(), svc.FullyQualifiedName); err != nil {
			return fmt.Errorf("grpcbridge: discover %s: %w", svc.FullyQualifiedName, err)
		}
		b.mountRoutes(mountGroup, svc)

		interval := svc.RefreshInterval
		if interval <= 0 {
			interval = 60 * time.Second
		}
		go b.refreshLoop(svc.FullyQualifiedName, interval)
	}
	return nil
}

func (b *Bridge) refreshLoop(serviceName string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		// Errors here are deliberately swallowed past logging: a
		// transient reflection failure shouldn't take down routes that
		// are already registered and working. New methods just won't
		// appear until the next successful refresh.
		_ = b.refresh(context.Background(), serviceName)
	}
}

func (b *Bridge) refresh(ctx context.Context, serviceName string) error {
	b.mu.RLock()
	bound, ok := b.services[serviceName]
	b.mu.RUnlock()
	if !ok {
		return fmt.Errorf("grpcbridge: %s is not registered", serviceName)
	}

	svcDesc, viaReflection, err := b.discoverViaReflection(ctx, bound.binding)
	if err != nil {
		if bound.binding.LocalFile == nil {
			return fmt.Errorf("reflection failed for %s and no LocalFile fallback configured: %w", serviceName, err)
		}
		svcDesc = bound.binding.LocalFile.Services().ByName(protoreflect.Name(lastSegment(serviceName)))
		if svcDesc == nil {
			return fmt.Errorf("service %q not found in LocalFile fallback descriptor", serviceName)
		}
		if !bound.binding.reflectionUnavailableLogged {
			log.Printf("grpcbridge: %s does not support gRPC reflection (%v) — falling back to the "+
				"compiled-in proto descriptor. Every method that exists in this gateway's build is still "+
				"served; methods added to the backend's proto after this gateway was built won't appear "+
				"until either the backend adds reflection.Register or the gateway is rebuilt against the "+
				"newer proto. Retrying reflection every %s in case the backend is upgraded.",
				serviceName, err, refreshIntervalOrDefault(bound.binding.RefreshInterval))
			bound.binding.reflectionUnavailableLogged = true
		}
	} else if viaReflection && bound.binding.reflectionUnavailableLogged {
		log.Printf("grpcbridge: %s now supports gRPC reflection — switched from the compiled-in fallback "+
			"descriptor to live discovery.", serviceName)
		bound.binding.reflectionUnavailableLogged = false
	}

	discovered := methodsFromServiceDescriptor(serviceName, svcDesc)

	b.mu.Lock()
	b.services[serviceName].methods = discovered
	b.mu.Unlock()
	return nil
}

// discoverViaReflection queries the backend's gRPC reflection service. The
// second return value is false (with a nil error) only in impossible
// call patterns — callers should treat any non-nil error as "reflection
// unavailable, use the fallback" without needing to distinguish further
// (a genuinely broken connection and a backend with reflection.Register
// simply not called both surface the same way from the client's side).
func (b *Bridge) discoverViaReflection(ctx context.Context, binding ServiceBinding) (protoreflect.ServiceDescriptor, bool, error) {
	conn, err := binding.Source.DialForReflection(ctx, "")
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()

	client := reflectionpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, false, err
	}
	defer stream.CloseSend()

	if err := stream.Send(&reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: binding.FullyQualifiedName,
		},
	}); err != nil {
		return nil, false, err
	}
	resp, err := stream.Recv()
	if err != nil {
		// Unimplemented is the expected status when reflection.Register
		// was never called on the backend — this is the normal,
		// backward-compatible case for an older deployment, not a bug.
		if status.Code(err) == codes.Unimplemented {
			return nil, false, fmt.Errorf("backend does not implement gRPC reflection: %w", err)
		}
		return nil, false, err
	}
	fdResp := resp.GetFileDescriptorResponse()
	if fdResp == nil {
		return nil, false, fmt.Errorf("no file descriptor for service %q", binding.FullyQualifiedName)
	}

	var fdProtos []*descriptorpb.FileDescriptorProto
	for _, raw := range fdResp.FileDescriptorProto {
		fdp := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(raw, fdp); err != nil {
			return nil, false, err
		}
		fdProtos = append(fdProtos, fdp)
	}
	files, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: fdProtos})
	if err != nil {
		return nil, false, err
	}

	var svcDesc protoreflect.ServiceDescriptor
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if sd := fd.Services().ByName(protoreflect.Name(lastSegment(binding.FullyQualifiedName))); sd != nil {
			svcDesc = sd
			return false
		}
		return true
	})
	if svcDesc == nil {
		return nil, false, fmt.Errorf("service %q not found in reflection response", binding.FullyQualifiedName)
	}
	return svcDesc, true, nil
}

func methodsFromServiceDescriptor(serviceName string, svcDesc protoreflect.ServiceDescriptor) []methodDesc {
	var discovered []methodDesc
	methods := svcDesc.Methods()
	for i := 0; i < methods.Len(); i++ {
		m := methods.Get(i)
		if m.IsStreamingClient() || m.IsStreamingServer() {
			continue // streaming stays hand-written, see package doc
		}
		discovered = append(discovered, methodDesc{
			name:       string(m.Name()),
			fullMethod: fmt.Sprintf("/%s/%s", serviceName, m.Name()),
			input:      m.Input(),
			output:     m.Output(),
		})
	}
	return discovered
}

func refreshIntervalOrDefault(d time.Duration) time.Duration {
	if d <= 0 {
		return 60 * time.Second
	}
	return d
}

func (b *Bridge) mountRoutes(mountGroup *gin.RouterGroup, svc ServiceBinding) {
	group := mountGroup.Group(svc.GroupPrefix)
	if svc.Auth != nil {
		group.Use(svc.Auth())
	}

	aliasByMethod := map[string]MethodAlias{}
	for _, a := range svc.Aliases {
		aliasByMethod[a.Method] = a
	}
	for _, a := range svc.Aliases {
		a := a
		group.Handle(a.Verb, a.Path, b.handlerFor(svc, a.Method, a.PathParams))
	}

	// Discovery endpoint: lets persysctl and the Go SDK ask "what can I
	// call here" instead of hardcoding paths.
	group.GET("/rpc/_meta", func(c *gin.Context) {
		b.mu.RLock()
		methods := b.services[svc.FullyQualifiedName].methods
		b.mu.RUnlock()

		type methodInfo struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Verb   string `json:"verb"`
			Input  string `json:"input_type"`
			Output string `json:"output_type"`
		}
		out := make([]methodInfo, 0, len(methods))
		for _, m := range methods {
			info := methodInfo{
				Method: m.name,
				Verb:   "POST",
				Path:   svc.GroupPrefix + "/rpc/" + lastSegment(svc.FullyQualifiedName) + "/" + m.name,
				Input:  string(m.input.FullName()),
				Output: string(m.output.FullName()),
			}
			if a, ok := aliasByMethod[m.name]; ok {
				info.Verb = a.Verb
				info.Path = svc.GroupPrefix + a.Path
			}
			out = append(out, info)
		}
		c.JSON(http.StatusOK, gin.H{"service": svc.FullyQualifiedName, "methods": out})
	})

	// Generic fallback: <GroupPrefix>/rpc/<ServiceLastSegment>/<Method>.
	group.POST("/rpc/"+lastSegment(svc.FullyQualifiedName)+"/:method", func(c *gin.Context) {
		method := c.Param("method")
		if _, aliased := aliasByMethod[method]; aliased {
			c.JSON(http.StatusGone, gin.H{"error": "use the dedicated path for this method", "method": method})
			return
		}
		b.handlerFor(svc, method, nil)(c)
	})
}

func (b *Bridge) handlerFor(svc ServiceBinding, method string, pathParams map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		b.mu.RLock()
		bound := b.services[svc.FullyQualifiedName]
		b.mu.RUnlock()

		var md *methodDesc
		for i := range bound.methods {
			if bound.methods[i].name == method {
				md = &bound.methods[i]
				break
			}
		}
		if md == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown method", "method": method})
			return
		}

		in := dynamicpb.NewMessage(md.input)
		if c.Request.ContentLength != 0 {
			body := map[string]any{}
			if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
				return
			}
			raw, _ := json.Marshal(body)
			if err := protojson.Unmarshal(raw, in); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "request does not match " + string(md.input.FullName()), "detail": err.Error()})
				return
			}
		}

		// Applied AFTER the body decode, so the URL is always
		// authoritative for these fields even if a caller's JSON body
		// redundantly (or incorrectly) also sets them — e.g.
		// DELETE /workloads/:id should delete the workload named by the
		// URL, full stop, regardless of body contents.
		for ginParam, fieldName := range pathParams {
			value := c.Param(ginParam)
			if value == "" {
				continue
			}
			fd := md.input.Fields().ByName(protoreflect.Name(fieldName))
			if fd == nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": fmt.Sprintf("gateway misconfiguration: %s has no field %q for path param %q", md.input.FullName(), fieldName, ginParam),
				})
				return
			}
			if fd.Kind() != protoreflect.StringKind {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": fmt.Sprintf("gateway misconfiguration: field %q is not a string field, path-param injection only supports strings", fieldName),
				})
				return
			}
			in.Set(fd, protoreflect.ValueOfString(value))
		}

		out := dynamicpb.NewMessage(md.output)
		keys := svc.keys()
		clusterID := keys.ResolveClusterID(c)
		sessionKey := keys.ResolveSessionKey(c)
		workloadKey := keys.ResolveWorkloadKey(c)

		err := bound.binding.Invoker.InvokeDynamic(c.Request.Context(), clusterID, sessionKey, workloadKey, md.fullMethod, in, out)
		if err != nil {
			writeInvokeError(c, err)
			return
		}

		data, err := protojson.Marshal(out)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal response"})
			return
		}
		c.Data(http.StatusOK, "application/json", data)
	}
}

// writeInvokeError maps known failure modes to sensible status codes,
// same distinctions the old ProwController.writeProxyError made.
func writeInvokeError(c *gin.Context, err error) {
	switch {
	case isUnknownCluster(err):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case isSchedulerUnavailable(err):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no healthy scheduler available"})
	default:
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	}
}

// ErrChecker lets an Invoker's package-specific sentinel errors
// (services.ErrUnknownCluster, services.ErrNoHealthySchedulers) map to
// the right HTTP status without grpcbridge importing services and
// creating a cycle. Set via RegisterErrorClassifiers at startup.
var (
	isUnknownCluster       = func(error) bool { return false }
	isSchedulerUnavailable = func(error) bool { return false }
)

// RegisterErrorClassifiers lets main.go wire services.IsUnknownCluster /
// services.IsSchedulerUnavailable in without grpcbridge depending on the
// services package.
func RegisterErrorClassifiers(unknownCluster, schedulerUnavailable func(error) bool) {
	if unknownCluster != nil {
		isUnknownCluster = unknownCluster
	}
	if schedulerUnavailable != nil {
		isSchedulerUnavailable = schedulerUnavailable
	}
}

func lastSegment(fqName string) string {
	for i := len(fqName) - 1; i >= 0; i-- {
		if fqName[i] == '.' {
			return fqName[i+1:]
		}
	}
	return fqName
}
