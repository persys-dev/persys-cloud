package controllers

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/authn"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
)

// ClusterMetaController holds the handlers that were never RPC-shaped and
// so have nothing for grpcbridge to discover via reflection: health,
// list-clusters, get-cluster. Everything else that used to live on
// ProwController (workload/node CRUD, cluster metrics, forgery
// passthrough) is now served dynamically — see
// internal/router/bindings.go — since it's a straight AgentControl or
// ForgeryControl RPC with no logic of its own beyond what the dynamic
// bridge already does.
//
// Renamed from ProwController. Also dropped: ListHandler (was just an
// alias for ListWorkloadsHandler, redundant with the real /workloads
// route) and the determineAuthMethod/validateAuthentication/
// isPublicEndpoint trio, which computed an auth decision that no handler
// in this file ever actually consulted — dead code pretending to be a
// safeguard.
type ClusterMetaController struct {
	clusterControl  *services.ClusterControlService
	deploymentMode  string
	databaseEnabled bool
}

func NewClusterMetaController(clusterControl *services.ClusterControlService, deploymentMode string, databaseEnabled bool) *ClusterMetaController {
	return &ClusterMetaController{
		clusterControl:  clusterControl,
		deploymentMode:  deploymentMode,
		databaseEnabled: databaseEnabled,
	}
}

// Register implements router.Registrar. Mounted at the top level (not
// nested under /clusters/:cluster_id) since ListClustersHandler in
// particular has no single cluster to scope to.
func (c *ClusterMetaController) Register(rg *gin.RouterGroup, _ *authn.Middleware) {
	// Health is intentionally unauthenticated — it's a liveness probe,
	// not a customer-facing endpoint, in both self-hosted and managed
	// deployments.
	rg.GET("/health", c.HealthCheckHandler())
	rg.GET("/clusters", c.ListClustersHandler())
	rg.GET("/clusters/:cluster_id", c.GetClusterHandler())
}

func (c *ClusterMetaController) HealthCheckHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{
			"status":                "healthy",
			"service":               "persys-gateway",
			"deployment_mode":       c.deploymentMode,
			"database_enabled":      c.databaseEnabled,
			"legacy_proxy_enabled":  c.clusterControl.IsProxyEnabled(),
			"legacy_scheduler_addr": c.clusterControl.GetSchedulerAddress(),
		})
	}
}

func (c *ClusterMetaController) ListClustersHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusters := c.clusterControl.SnapshotClusters()
		sort.SliceStable(clusters, func(i, j int) bool { return clusters[i].ID < clusters[j].ID })
		ctx.JSON(http.StatusOK, gin.H{
			"default_cluster_id": c.clusterControl.DefaultClusterID(),
			"clusters":           buildClusterViews(clusters),
		})
	}
}

func (c *ClusterMetaController) GetClusterHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clusterID := strings.TrimSpace(ctx.Param("cluster_id"))
		if clusterID == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "cluster_id is required"})
			return
		}
		for _, cluster := range c.clusterControl.SnapshotClusters() {
			if cluster.ID != clusterID {
				continue
			}
			ctx.JSON(http.StatusOK, gin.H{
				"default_cluster_id": c.clusterControl.DefaultClusterID(),
				"cluster":            buildClusterView(cluster),
			})
			return
		}
		ctx.JSON(http.StatusNotFound, gin.H{"error": "cluster not found"})
	}
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
