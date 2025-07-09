package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/prow/internal/models"
	"github.com/persys-dev/prow/internal/scheduler"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// NodeHeartbeat represents a heartbeat message from a node
type NodeHeartbeat struct {
	NodeID          string  `json:"nodeId"`
	Status          string  `json:"status"`
	AvailableCPU    float64 `json:"availableCpu"`
	AvailableMemory int64   `json:"availableMemory"`
}

func RegisterMTLSHandlers(r *gin.Engine, sched *scheduler.Scheduler) {
	// mTLS protected routes
	mtls := r.Group("/")
	{
		// Schedule workload
		mtls.POST("/workloads/schedule", func(c *gin.Context) {
			var workload models.Workload
			if err := c.ShouldBindJSON(&workload); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			workload.Status = "Pending"

			nodeID, err := sched.ScheduleWorkload(workload)
			if err != nil {
				log.Printf("Failed to schedule workload %s: %v", workload.ID, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"node_id": nodeID})
		})

		// List nodes
		mtls.GET("/nodes", func(c *gin.Context) {
			nodes, err := sched.GetNodes()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get nodes"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"nodes": nodes})
		})

		// List workloads
		mtls.GET("/workloads", func(c *gin.Context) {
			resp, err := sched.RetryableEtcdGet("/workloads/", clientv3.WithPrefix())
			if err != nil {
				log.Printf("Failed to get workloads: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get workloads"})
				return
			}

			var workloads []models.Workload
			for _, kv := range resp.Kvs {
				var workload models.Workload
				if err := json.Unmarshal(kv.Value, &workload); err != nil {
					continue
				}
				workloads = append(workloads, workload)
			}
			c.JSON(http.StatusOK, workloads)
		})

		// Cluster metrics
		mtls.GET("/cluster/metrics", func(c *gin.Context) {
			// Node metrics
			nodes, err := sched.GetNodes()
			if err != nil {
				log.Printf("Failed to get nodes for metrics: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get nodes"})
				return
			}

			// Workload metrics
			resp, err := sched.RetryableEtcdGet("/workloads/")
			if err != nil {
				log.Printf("Failed to get workloads for metrics: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get workloads"})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"nodes":     nodes,
				"workloads": resp.Kvs,
			})
		})

	}
}
