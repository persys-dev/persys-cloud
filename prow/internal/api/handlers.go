package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/prow/internal/models"
	"github.com/persys-dev/prow/internal/scheduler"
	"go.etcd.io/etcd/client/v3"
)

func SetupHandlers(r *gin.Engine, sched *scheduler.Scheduler) {
    
	r.POST("/nodes/register", func(c *gin.Context) {
		var node models.Node
		if err := c.ShouldBindJSON(&node); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
        resp, err := sched.RetryableEtcdGet("/nodes/" + node.NodeID)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get nodes from etcd:", "err": err.Error()})
            return
        }
        if resp.Count != 0 {
            c.JSON(http.StatusOK, gin.H{"info": "Node already registered", "NodeID": node.NodeID})
            return
        }
		if err := sched.RegisterNode(node); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to register node:", "err": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"nodeId": node.NodeID, "status": "Registered"})
	})

	r.GET("/nodes", func(c *gin.Context) {
		nodes, err := sched.GetNodes()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get nodes"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"nodes": nodes})
	})

    r.GET("/cluster/metrics", func(c *gin.Context) {
        // Node metrics
        nodes, err := sched.GetNodes()
        if err != nil {
            log.Printf("Failed to get nodes for metrics: %v", err)
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get node metrics"})
            return
        }

        nodeMetrics := make([]map[string]interface{}, 0, len(nodes))
        for _, node := range nodes {
            nodeMetrics = append(nodeMetrics, map[string]interface{}{
                "nodeId":          node.NodeID,
                "ipAddress":       node.IPAddress,
                "status":          node.Status,
                "lastHeartbeat":   node.LastHeartbeat,
                "cpuUsage":        node.Resources.CPUUsage,
                "memoryUsage":     node.Resources.MemoryUsage,
                "diskUsage":       node.Resources.DiskUsage,
                "availableCpu":    node.AvailableCPU,
                "availableMemory": node.AvailableMemory,
            })
        }

        // Workload metrics
        resp, err := sched.RetryableEtcdGet("/workloads/")
        if err != nil {
            log.Printf("Failed to get workloads for metrics: %v", err)
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get workload metrics"})
            return
        }

        workloadMetrics := make([]map[string]interface{}, 0)
        for _, kv := range resp.Kvs {
            var workload models.Workload
            if err := json.Unmarshal(kv.Value, &workload); err != nil {
                log.Printf("Failed to unmarshal workload %s for metrics: %v", string(kv.Key), err)
                continue
            }
            workloadMetrics = append(workloadMetrics, map[string]interface{}{
                "workloadId": workload.ID,
                "nodeId":     workload.NodeID,
                "status":     workload.Status,
                "command":    workload.Command,
                "image":      workload.Image,
                "createdAt":  workload.CreatedAt,
            })
        }

        c.JSON(http.StatusOK, gin.H{
            "nodes":     nodeMetrics,
            "workloads": workloadMetrics,
        })
    })

	r.POST("/workloads/schedule", func(c *gin.Context) {
        var workload models.Workload
        if err := c.ShouldBindJSON(&workload); err != nil {
            log.Printf("Invalid workload request from %s: %v", c.ClientIP(), err)
            c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
            return
        }

        if workload.Type != "docker-container" && workload.Type != "docker-compose" && workload.Type != "git-compose" {
            log.Printf("Invalid workload type '%s' from %s", workload.Type, c.ClientIP())
            c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workload type"})
            return
        }

        workload.CreatedAt = time.Now()
        workload.Status = "Pending"

        nodeID, err := sched.ScheduleWorkload(workload)
        if err != nil {
            log.Printf("Failed to schedule workload %s: %v", workload.ID, err)
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to schedule workload", "details": err.Error()})
            return
        }

        workload.NodeID = nodeID
        workload.Status = "Scheduled"
        log.Printf("Workload %s scheduled on node %s", workload.ID, nodeID)
        c.JSON(http.StatusOK, gin.H{"workloadId": workload.ID, "nodeId": nodeID, "status": "Scheduled"})
    })

	r.GET("/workloads", func(c *gin.Context) {
		resp, err := sched.RetryableEtcdGet("/workloads/", clientv3.WithPrefix())
		if err != nil {
			log.Printf("Failed to get workloads: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get workloads"})
			return
		}

		log.Printf("Retrieved %d keys from etcd for /workloads/", len(resp.Kvs))
		var workloads []models.Workload
		for _, kv := range resp.Kvs {
			var workload models.Workload
			if err := json.Unmarshal(kv.Value, &workload); err != nil {
				log.Printf("Failed to unmarshal workload %s: %v", string(kv.Key), err)
				continue
			}
			log.Printf("Found workload: %+v", workload)
			workloads = append(workloads, workload)
		}
		c.JSON(http.StatusOK, gin.H{"workloads": workloads})
	})

	r.POST("/nodes/heartbeat", func(c *gin.Context) {
        var heartbeat struct {
            NodeID string `json:"nodeId"`
            Status string `json:"status"`
            AvailableCPU float64 `json:"availableCpu"`
            AvailableMemory int64 `json:"availableMemory"`
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

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token != "valid-token" { // Replace with real validation
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		c.Next()
	}
}