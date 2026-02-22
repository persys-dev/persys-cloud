package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	controlv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/controlv1"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/forgeryv1"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type ProwController struct {
	prowService *services.ProwService
	authService services.AuthService
	ctx         context.Context
}

func NewProwController(prowService *services.ProwService, authService services.AuthService, ctx context.Context) *ProwController {
	return &ProwController{prowService: prowService, authService: authService, ctx: ctx}
}

func (c *ProwController) determineAuthMethod(ctx *gin.Context) string {
	if ctx.Request.TLS != nil && len(ctx.Request.TLS.PeerCertificates) > 0 {
		return "mtls"
	}
	if c.authService.IsAuthenticated(ctx) {
		return "oauth"
	}
	return "none"
}

func (c *ProwController) validateAuthentication(ctx *gin.Context, authMethod string) bool {
	switch authMethod {
	case "mtls":
		return true
	case "oauth":
		return c.authService.IsAuthenticated(ctx)
	case "none":
		return c.isPublicEndpoint(ctx.Request.URL.Path)
	default:
		return false
	}
}

func (c *ProwController) isPublicEndpoint(path string) bool {
	publicPaths := []string{"/metrics", "/health", "/ready"}
	for _, publicPath := range publicPaths {
		if path == publicPath {
			return true
		}
	}
	return false
}

func (c *ProwController) ListHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		c.ListWorkloadsHandler()(ctx)
	}
}

func (c *ProwController) ScheduleWorkloadHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := &controlv1.ApplyWorkloadRequest{}
		if !decodeProtoBody(ctx, req) {
			return
		}
		clusterID := c.resolveClusterID(ctx)
		sessionKey := c.resolveSessionKey(ctx)
		workloadKey := c.resolveWorkloadKey(ctx)

		resp, err := c.prowService.ApplyWorkload(ctx.Request.Context(), clusterID, sessionKey, workloadKey, req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *ProwController) ListWorkloadsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := c.resolveClusterID(ctx)
		sessionKey := c.resolveSessionKey(ctx)
		workloadKey := c.resolveWorkloadKey(ctx)
		req := &controlv1.ListWorkloadsRequest{Status: ctx.Query("status")}

		resp, err := c.prowService.ListWorkloads(ctx.Request.Context(), clusterID, sessionKey, workloadKey, req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *ProwController) GetWorkloadHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := c.resolveClusterID(ctx)
		sessionKey := c.resolveSessionKey(ctx)
		workloadKey := c.resolveWorkloadKey(ctx)
		req := &controlv1.GetWorkloadRequest{WorkloadId: ctx.Param("id")}

		resp, err := c.prowService.GetWorkload(ctx.Request.Context(), clusterID, sessionKey, workloadKey, req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *ProwController) DeleteWorkloadHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := c.resolveClusterID(ctx)
		sessionKey := c.resolveSessionKey(ctx)
		workloadKey := c.resolveWorkloadKey(ctx)
		req := &controlv1.DeleteWorkloadRequest{WorkloadId: ctx.Param("id")}

		resp, err := c.prowService.DeleteWorkload(ctx.Request.Context(), clusterID, sessionKey, workloadKey, req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *ProwController) RetryWorkloadHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := c.resolveClusterID(ctx)
		sessionKey := c.resolveSessionKey(ctx)
		workloadKey := c.resolveWorkloadKey(ctx)
		req := &controlv1.RetryWorkloadRequest{WorkloadId: ctx.Param("id")}

		resp, err := c.prowService.RetryWorkload(ctx.Request.Context(), clusterID, sessionKey, workloadKey, req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *ProwController) ListNodesHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := c.resolveClusterID(ctx)
		sessionKey := c.resolveSessionKey(ctx)
		workloadKey := c.resolveWorkloadKey(ctx)
		req := &controlv1.ListNodesRequest{Status: ctx.Query("status")}

		resp, err := c.prowService.ListNodes(ctx.Request.Context(), clusterID, sessionKey, workloadKey, req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *ProwController) GetNodeHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := c.resolveClusterID(ctx)
		sessionKey := c.resolveSessionKey(ctx)
		workloadKey := c.resolveWorkloadKey(ctx)
		req := &controlv1.GetNodeRequest{NodeId: ctx.Param("id")}

		resp, err := c.prowService.GetNode(ctx.Request.Context(), clusterID, sessionKey, workloadKey, req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *ProwController) ClusterMetricsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := c.resolveClusterID(ctx)
		sessionKey := c.resolveSessionKey(ctx)
		workloadKey := c.resolveWorkloadKey(ctx)

		resp, err := c.prowService.GetClusterSummary(ctx.Request.Context(), clusterID, sessionKey, workloadKey, &controlv1.GetClusterSummaryRequest{})
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

type triggerBuildPayload struct {
	ProjectName string `json:"project_name" binding:"required"`
	Repository  string `json:"repository"`
	ClusterID   string `json:"cluster_id"`
	Ref         string `json:"ref"`
	CommitSHA   string `json:"commit_sha"`
	Sender      string `json:"sender"`
	Mode        string `json:"mode"`
	EventType   string `json:"event_type"`
}

func (c *ProwController) TriggerBuildHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var reqBody triggerBuildPayload
		if err := ctx.ShouldBindJSON(&reqBody); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid build request payload"})
			return
		}
		clusterID := c.resolveClusterID(ctx)
		if clusterID == "" {
			clusterID = reqBody.ClusterID
		}

		resp, err := c.prowService.TriggerBuild(ctx.Request.Context(), &forgeryv1.TriggerBuildRequest{
			ProjectName: strings.TrimSpace(reqBody.ProjectName),
			Repository:  strings.TrimSpace(reqBody.Repository),
			ClusterId:   strings.TrimSpace(clusterID),
			Ref:         strings.TrimSpace(reqBody.Ref),
			CommitSha:   strings.TrimSpace(reqBody.CommitSHA),
			Sender:      strings.TrimSpace(reqBody.Sender),
			Mode:        strings.TrimSpace(reqBody.Mode),
			EventType:   strings.TrimSpace(reqBody.EventType),
		})
		if err != nil {
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

type upsertProjectPayload struct {
	Name          string `json:"name" binding:"required"`
	RepoURL       string `json:"repo_url" binding:"required"`
	DefaultBranch string `json:"default_branch"`
	ClusterID     string `json:"cluster_id"`
	BuildType     string `json:"build_type"`
	BuildMode     string `json:"build_mode"`
	Strategy      string `json:"strategy"`
	NexusRepo     string `json:"nexus_repo"`
	PipelineYAML  string `json:"pipeline_yaml"`
	AutoDeploy    bool   `json:"auto_deploy"`
	ImageName     string `json:"image_name"`
}

func (c *ProwController) UpsertProjectHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var reqBody upsertProjectPayload
		if err := ctx.ShouldBindJSON(&reqBody); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid project payload"})
			return
		}
		clusterID := c.resolveClusterID(ctx)
		if clusterID == "" {
			clusterID = reqBody.ClusterID
		}

		resp, err := c.prowService.UpsertProject(ctx.Request.Context(), &forgeryv1.UpsertProjectRequest{
			Name:          strings.TrimSpace(reqBody.Name),
			RepoUrl:       strings.TrimSpace(reqBody.RepoURL),
			DefaultBranch: strings.TrimSpace(reqBody.DefaultBranch),
			ClusterId:     strings.TrimSpace(clusterID),
			BuildType:     strings.TrimSpace(reqBody.BuildType),
			BuildMode:     strings.TrimSpace(reqBody.BuildMode),
			Strategy:      strings.TrimSpace(reqBody.Strategy),
			NexusRepo:     strings.TrimSpace(reqBody.NexusRepo),
			PipelineYaml:  reqBody.PipelineYAML,
			AutoDeploy:    reqBody.AutoDeploy,
			ImageName:     strings.TrimSpace(reqBody.ImageName),
		})
		if err != nil {
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

type webhookTestPayload struct {
	DeliveryID string `json:"delivery_id"`
	EventType  string `json:"event_type"`
	Repository string `json:"repository" binding:"required"`
	ClusterID  string `json:"cluster_id"`
	Sender     string `json:"sender"`
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Payload    any    `json:"payload"`
}

func (c *ProwController) TestWebhookHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var reqBody webhookTestPayload
		if err := ctx.ShouldBindJSON(&reqBody); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook test payload"})
			return
		}
		clusterID := c.resolveClusterID(ctx)
		if clusterID == "" {
			clusterID = reqBody.ClusterID
		}

		if strings.TrimSpace(reqBody.DeliveryID) == "" {
			reqBody.DeliveryID = uuid.NewString()
		}
		if strings.TrimSpace(reqBody.EventType) == "" {
			reqBody.EventType = "push"
		}

		payloadJSON := "{}"
		if reqBody.Payload != nil {
			if marshaled, err := json.Marshal(reqBody.Payload); err == nil {
				payloadJSON = string(marshaled)
			}
		}

		resp, err := c.prowService.ForwardWebhookTest(ctx.Request.Context(), &forgeryv1.ForwardWebhookRequest{
			DeliveryId:  reqBody.DeliveryID,
			EventType:   strings.TrimSpace(reqBody.EventType),
			Repository:  strings.TrimSpace(reqBody.Repository),
			ClusterId:   strings.TrimSpace(clusterID),
			Sender:      strings.TrimSpace(reqBody.Sender),
			Ref:         strings.TrimSpace(reqBody.Ref),
			Before:      strings.TrimSpace(reqBody.Before),
			After:       strings.TrimSpace(reqBody.After),
			PayloadJson: payloadJSON,
			Verified:    true,
		})
		if err != nil {
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *ProwController) HealthCheckHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "persys-gateway", "prow_proxy_enabled": c.prowService.IsProxyEnabled(), "prow_scheduler": c.prowService.GetSchedulerAddress()})
	}
}

func (c *ProwController) ListClustersHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusters := c.prowService.SnapshotClusters()
		sort.SliceStable(clusters, func(i, j int) bool { return clusters[i].ID < clusters[j].ID })
		ctx.JSON(http.StatusOK, gin.H{
			"default_cluster_id": c.prowService.DefaultClusterID(),
			"clusters":           buildClusterViews(clusters),
		})
	}
}

func (c *ProwController) GetClusterHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := strings.TrimSpace(ctx.Param("cluster_id"))
		if clusterID == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "cluster_id is required"})
			return
		}
		for _, cluster := range c.prowService.SnapshotClusters() {
			if cluster.ID != clusterID {
				continue
			}
			ctx.JSON(http.StatusOK, gin.H{
				"default_cluster_id": c.prowService.DefaultClusterID(),
				"cluster":            buildClusterView(cluster),
			})
			return
		}
		ctx.JSON(http.StatusNotFound, gin.H{"error": "cluster not found"})
	}
}

func (c *ProwController) resolveClusterID(ctx *gin.Context) string {
	if clusterID := strings.TrimSpace(ctx.Param("cluster_id")); clusterID != "" {
		return clusterID
	}
	if clusterID := strings.TrimSpace(ctx.GetHeader("X-Persys-Cluster-ID")); clusterID != "" {
		return clusterID
	}
	if clusterID := strings.TrimSpace(ctx.Query("cluster_id")); clusterID != "" {
		return clusterID
	}
	return ""
}

func (c *ProwController) resolveSessionKey(ctx *gin.Context) string {
	if s := strings.TrimSpace(ctx.GetHeader("X-Persys-Session")); s != "" {
		return s
	}
	if cookie, err := ctx.Cookie("persys_session"); err == nil && strings.TrimSpace(cookie) != "" {
		return cookie
	}
	authz := strings.TrimSpace(ctx.GetHeader("Authorization"))
	if authz == "" {
		return strings.TrimSpace(ctx.ClientIP())
	}
	sum := sha256.Sum256([]byte(authz))
	return hex.EncodeToString(sum[:])
}

func (c *ProwController) resolveWorkloadKey(ctx *gin.Context) string {
	if key := strings.TrimSpace(ctx.GetHeader("X-Persys-Workload-Key")); key != "" {
		return key
	}
	if id := strings.TrimSpace(ctx.Param("id")); id != "" {
		return id
	}
	if wid := strings.TrimSpace(ctx.Query("workload_id")); wid != "" {
		return wid
	}
	return ctx.Request.URL.Path
}

func (c *ProwController) writeProxyError(ctx *gin.Context, err error) {
	if services.IsUnknownCluster(err) {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if services.IsSchedulerUnavailable(err) {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "no healthy scheduler available"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func decodeProtoBody(ctx *gin.Context, msg proto.Message) bool {
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "request body is required"})
		return false
	}
	unmarshal := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := unmarshal.Unmarshal(body, msg); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload"})
		return false
	}
	return true
}

func writeProtoJSON(ctx *gin.Context, status int, msg proto.Message) {
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode response"})
		return
	}
	ctx.Data(status, "application/json", data)
}

func buildClusterViews(clusters []services.Cluster) []gin.H {
	out := make([]gin.H, 0, len(clusters))
	for _, cluster := range clusters {
		out = append(out, buildClusterView(cluster))
	}
	return out
}

func buildClusterView(cluster services.Cluster) gin.H {
	schedulers := make([]gin.H, 0, len(cluster.Schedulers))
	healthy := 0
	for _, s := range cluster.Schedulers {
		if s.Healthy {
			healthy++
		}
		schedulers = append(schedulers, gin.H{
			"id":        s.ID,
			"address":   s.Address,
			"is_leader": s.IsLeader,
			"healthy":   s.Healthy,
			"last_seen": s.LastSeen.UTC().Format(time.RFC3339),
		})
	}
	return gin.H{
		"id":                 cluster.ID,
		"name":               cluster.Name,
		"routing_strategy":   string(cluster.RoutingStrategy),
		"total_schedulers":   len(cluster.Schedulers),
		"healthy_schedulers": healthy,
		"schedulers":         schedulers,
	}
}
