package controllers

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/api-gateway/services"
)

type ProwController struct {
	prowService *services.ProwService
	authService services.AuthService
	ctx         context.Context
}

func NewProwController(prowService *services.ProwService, authService services.AuthService, ctx context.Context) *ProwController {
	return &ProwController{
		prowService: prowService,
		authService: authService,
		ctx:         ctx,
	}
}

// ProxyHandler handles all prow-scheduler requests
func (c *ProwController) ProxyHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Check if prow proxy is enabled
		if !c.prowService.IsProxyEnabled() {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "Prow proxy is disabled"})
			return
		}

		// Determine authentication method
		authMethod := c.determineAuthMethod(ctx)

		// Validate authentication based on method
		if !c.validateAuthentication(ctx, authMethod) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			return
		}

		// Get request body
		var body io.Reader
		if ctx.Request.Body != nil {
			body = ctx.Request.Body
		}

		// Proxy the request to prow-scheduler
		resp, err := c.prowService.ProxyRequest(
			ctx.Request.Context(),
			ctx.Request.Method,
			ctx.Request.URL.Path,
			body,
			ctx.Request.Header,
		)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer resp.Body.Close()

		// Copy response back to client
		if err := c.prowService.CopyResponse(resp, ctx); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to copy response"})
			return
		}
	}
}

// determineAuthMethod determines the appropriate authentication method
func (c *ProwController) determineAuthMethod(ctx *gin.Context) string {
	// Check for mTLS certificate first
	if len(ctx.Request.TLS.PeerCertificates) > 0 {
		return "mtls"
	}

	// Check for GitHub OAuth token
	if c.authService.IsAuthenticated(ctx) {
		return "oauth"
	}

	return "none"
}

// validateAuthentication validates the request based on authentication method
func (c *ProwController) validateAuthentication(ctx *gin.Context, authMethod string) bool {
	switch authMethod {
	case "mtls":
		// mTLS is already validated by TLS handshake
		return true
	case "oauth":
		// OAuth is validated by auth service
		return c.authService.IsAuthenticated(ctx)
	case "none":
		// For public endpoints like metrics
		return c.isPublicEndpoint(ctx.Request.URL.Path)
	default:
		return false
	}
}

// isPublicEndpoint checks if the endpoint is public (no auth required)
func (c *ProwController) isPublicEndpoint(path string) bool {
	publicPaths := []string{
		"/metrics",
		"/health",
		"/ready",
	}

	for _, publicPath := range publicPaths {
		if path == publicPath {
			return true
		}
	}
	return false
}

// Specific endpoint handlers for backward compatibility


func (c *ProwController) ListHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Redirect to proxy handler
		ctx.Request.URL.Path = "/list"
		c.ProxyHandler()(ctx)
	}
}

// WorkloadHandlers for specific workload operations

func (c *ProwController) ScheduleWorkloadHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Redirect to proxy handler
		ctx.Request.URL.Path = "/workloads/schedule"
		c.ProxyHandler()(ctx)
	}
}

func (c *ProwController) ListWorkloadsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Redirect to proxy handler
		ctx.Request.URL.Path = "/workloads"
		c.ProxyHandler()(ctx)
	}
}

func (c *ProwController) GetWorkloadHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		workloadID := ctx.Param("id")
		// Redirect to proxy handler
		ctx.Request.URL.Path = "/workloads/" + workloadID
		c.ProxyHandler()(ctx)
	}
}

// NodeHandlers for node operations

func (c *ProwController) RegisterNodeHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Redirect to proxy handler
		ctx.Request.URL.Path = "/nodes/register"
		c.ProxyHandler()(ctx)
	}
}

func (c *ProwController) NodeHeartbeatHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Redirect to proxy handler
		ctx.Request.URL.Path = "/nodes/heartbeat"
		c.ProxyHandler()(ctx)
	}
}

func (c *ProwController) ListNodesHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Redirect to proxy handler
		ctx.Request.URL.Path = "/nodes"
		c.ProxyHandler()(ctx)
	}
}

// ClusterHandlers for cluster operations

func (c *ProwController) ClusterMetricsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Redirect to proxy handler
		ctx.Request.URL.Path = "/cluster/metrics"
		c.ProxyHandler()(ctx)
	}
}

// HealthCheckHandler for API gateway health
func (c *ProwController) HealthCheckHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{
			"status":             "healthy",
			"service":            "api-gateway",
			"prow_proxy_enabled": c.prowService.IsProxyEnabled(),
			"prow_scheduler":     c.prowService.GetSchedulerAddress(),
		})
	}
}
