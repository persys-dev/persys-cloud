package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/api-gateway/controllers"
)

type ProwRouteController struct {
	prowController *controllers.ProwController
}

func NewProwRouteController(prowController *controllers.ProwController) ProwRouteController {
	return ProwRouteController{prowController: prowController}
}

func (rc *ProwRouteController) ProwRoute(rg *gin.RouterGroup) {
	router := rg.Group("")

	// Health check endpoint
	router.GET("/health", rc.prowController.HealthCheckHandler())

	// Legacy endpoints for backward compatibility
	router.GET("/list", rc.prowController.ListHandler())

	// Workload management endpoints
	workloads := router.Group("/workloads")
	{
		workloads.POST("/schedule", rc.prowController.ScheduleWorkloadHandler())
		workloads.GET("", rc.prowController.ListWorkloadsHandler())
		workloads.GET("/:id", rc.prowController.GetWorkloadHandler())
	}

	// Node management endpoints
	nodes := router.Group("/nodes")
	{
		nodes.POST("/register", rc.prowController.RegisterNodeHandler())
		nodes.POST("/heartbeat", rc.prowController.NodeHeartbeatHandler())
		nodes.GET("", rc.prowController.ListNodesHandler())
	}

	// Cluster management endpoints
	cluster := router.Group("/cluster")
	{
		cluster.GET("/metrics", rc.prowController.ClusterMetricsHandler())
	}

	// Universal proxy handler for any other prow endpoints
	// This must be the last route registered to avoid conflicts
	router.Any("/proxy/*path", rc.prowController.ProxyHandler())
}
