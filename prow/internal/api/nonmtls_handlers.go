package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/prow/internal/models"
	"github.com/persys-dev/prow/internal/scheduler"
)

func RegisterNonMTLSHandlers(r *gin.Engine, sched *scheduler.Scheduler) {
	r.POST("/nodes/register", func(c *gin.Context) {
		var node models.Node
		if err := c.ShouldBindJSON(&node); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Check if node already exists
		_, err := sched.RetryableEtcdGet("/nodes/" + node.NodeID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get nodes from etcd:", "err": err.Error()})
			return
		}
		if err := sched.RegisterNode(node); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to register node:", "err": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"nodeId": node.NodeID, "status": "Registered"})
	})

	r.POST("/nodes/heartbeat", func(c *gin.Context) {
		var heartbeat struct {
			NodeID          string  `json:"nodeId"`
			Status          string  `json:"status"`
			AvailableCPU    float64 `json:"availableCpu"`
			AvailableMemory int64   `json:"availableMemory"`
		}
		if err := c.ShouldBindJSON(&heartbeat); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		// Log only on error or periodically to reduce log volume
		log.Printf("Received heartbeat for node %s (Status: %s)", heartbeat.NodeID, heartbeat.Status)

		resp, err := sched.RetryableEtcdGet("/nodes/" + heartbeat.NodeID)
		if err != nil {
			log.Printf("Failed to get node %s from etcd: %v", heartbeat.NodeID, err)
			c.JSON(500, gin.H{"error": "Internal server error"})
			return
		}
		if resp == nil || len(resp.Kvs) == 0 {
			log.Printf("Node %s not found in etcd", heartbeat.NodeID)
			c.JSON(404, gin.H{"error": "Node not found"})
			return
		}

		var node models.Node
		if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
			log.Printf("Failed to unmarshal node %s: %v", heartbeat.NodeID, err)
			c.JSON(500, gin.H{"error": "Internal server error"})
			return
		}

		node.LastHeartbeat = time.Now()
		if heartbeat.Status != "" {
			node.Status = heartbeat.Status
		}
		if heartbeat.AvailableCPU > 0 {
			node.AvailableCPU = heartbeat.AvailableCPU
		}
		if heartbeat.AvailableMemory > 0 {
			node.AvailableMemory = heartbeat.AvailableMemory
		}

		updatedNodeJSON, err := json.Marshal(node)
		if err != nil {
			log.Printf("Failed to marshal node %s: %v", heartbeat.NodeID, err)
			c.JSON(500, gin.H{"error": "Internal server error"})
			return
		}

		if err := sched.RetryableEtcdPut("/nodes/"+heartbeat.NodeID, string(updatedNodeJSON)); err != nil {
			log.Printf("Failed to update node %s heartbeat: %v", heartbeat.NodeID, err)
			c.JSON(500, gin.H{"error": "Failed to update heartbeat"})
			return
		}

		c.JSON(200, gin.H{"message": "Heartbeat received"})
	})
}
