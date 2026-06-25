package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/controllers"
)

type ProwRouteController struct {
	prowController *controllers.ProwController
}

func NewProwRouteController(prowController *controllers.ProwController) ProwRouteController {
	return ProwRouteController{prowController: prowController}
}

func (rc *ProwRouteController) ProwRoute(rg *gin.RouterGroup) {
	router := rg.Group("")

	router.GET("/health", rc.prowController.HealthCheckHandler())
	router.GET("/list", rc.prowController.ListHandler())
	router.GET("/clusters", rc.prowController.ListClustersHandler())
	router.GET("/clusters/:cluster_id", rc.prowController.GetClusterHandler())

	workloads := router.Group("/workloads")
	{
		workloads.POST("/schedule", rc.prowController.ScheduleWorkloadHandler())
		workloads.GET("", rc.prowController.ListWorkloadsHandler())
		workloads.GET("/:id", rc.prowController.GetWorkloadHandler())
		workloads.DELETE("/:id", rc.prowController.DeleteWorkloadHandler())
		workloads.POST("/:id/retry", rc.prowController.RetryWorkloadHandler())
	}

	forgery := router.Group("/forgery")
	{
		forgery.POST("/projects/upsert", rc.prowController.UpsertProjectHandler())
		forgery.POST("/builds/trigger", rc.prowController.TriggerBuildHandler())
		forgery.POST("/webhooks/test", rc.prowController.TestWebhookHandler())
		forgery.GET("/pipeline/status", rc.prowController.ListPipelineStatusHandler())
	}

	nodes := router.Group("/nodes")
	{
		nodes.GET("", rc.prowController.ListNodesHandler())
		nodes.GET("/:id", rc.prowController.GetNodeHandler())
		nodes.POST("/:id/drain", rc.prowController.DrainNodeHandler())
		nodes.POST("/:id/undrain", rc.prowController.UndrainNodeHandler())
		nodes.POST("/:id/taint", rc.prowController.TaintNodeHandler())
		nodes.POST("/:id/untaint", rc.prowController.UntaintNodeHandler())
		nodes.POST("/:id/labels", rc.prowController.SetNodeLabelHandler())
		nodes.DELETE("/:id/labels", rc.prowController.DeleteNodeLabelHandler())
	}

	cluster := router.Group("/cluster")
	{
		cluster.GET("/metrics", rc.prowController.ClusterMetricsHandler())
	}

	clusters := router.Group("/clusters/:cluster_id")
	{
		clusters.POST("/workloads/schedule", rc.prowController.ScheduleWorkloadHandler())
		clusters.GET("/workloads", rc.prowController.ListWorkloadsHandler())
		clusters.GET("/workloads/:id", rc.prowController.GetWorkloadHandler())
		clusters.DELETE("/workloads/:id", rc.prowController.DeleteWorkloadHandler())
		clusters.POST("/workloads/:id/retry", rc.prowController.RetryWorkloadHandler())
		clusters.GET("/nodes", rc.prowController.ListNodesHandler())
		clusters.GET("/nodes/:id", rc.prowController.GetNodeHandler())
		clusters.POST("/nodes/:id/drain", rc.prowController.DrainNodeHandler())
		clusters.POST("/nodes/:id/undrain", rc.prowController.UndrainNodeHandler())
		clusters.POST("/nodes/:id/taint", rc.prowController.TaintNodeHandler())
		clusters.POST("/nodes/:id/untaint", rc.prowController.UntaintNodeHandler())
		clusters.POST("/nodes/:id/labels", rc.prowController.SetNodeLabelHandler())
		clusters.DELETE("/nodes/:id/labels", rc.prowController.DeleteNodeLabelHandler())
		clusters.GET("/cluster/metrics", rc.prowController.ClusterMetricsHandler())
		clusters.POST("/forgery/projects/upsert", rc.prowController.UpsertProjectHandler())
		clusters.POST("/forgery/builds/trigger", rc.prowController.TriggerBuildHandler())
		clusters.POST("/forgery/webhooks/test", rc.prowController.TestWebhookHandler())
		clusters.GET("/forgery/pipeline/status", rc.prowController.ListPipelineStatusHandler())
	}
}
