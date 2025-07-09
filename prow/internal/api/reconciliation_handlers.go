package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/prow/internal/scheduler"
)

// ReconciliationHandlers handles reconciliation-related API endpoints
type ReconciliationHandlers struct {
	scheduler *scheduler.Scheduler
}

// NewReconciliationHandlers creates a new ReconciliationHandlers instance
func NewReconciliationHandlers(scheduler *scheduler.Scheduler) *ReconciliationHandlers {
	return &ReconciliationHandlers{
		scheduler: scheduler,
	}
}

// GetReconciliationStats returns reconciliation statistics
func (h *ReconciliationHandlers) GetReconciliationStats(c *gin.Context) {
	stats, err := h.scheduler.GetReconciliationStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get reconciliation stats",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"stats":     stats,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// TriggerReconciliation manually triggers a reconciliation cycle
func (h *ReconciliationHandlers) TriggerReconciliation(c *gin.Context) {
	// Get all workloads
	workloads, err := h.scheduler.GetWorkloads()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get workloads",
			"message": err.Error(),
		})
		return
	}

	// Perform reconciliation
	results, err := h.scheduler.ReconcileAllWorkloads()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to perform reconciliation",
			"message": err.Error(),
		})
		return
	}

	// Count results
	successCount := 0
	actionCount := 0
	for _, result := range results {
		if result.Success {
			successCount++
		}
		if result.Action != "NoAction" {
			actionCount++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Reconciliation triggered successfully",
		"results": gin.H{
			"totalWorkloads": len(workloads),
			"processed":      len(results),
			"actionsTaken":   actionCount,
			"successful":     successCount,
			"timestamp":      time.Now().Format(time.RFC3339),
		},
	})
}

// ReconcileWorkload manually reconciles a specific workload
func (h *ReconciliationHandlers) ReconcileWorkload(c *gin.Context) {
	workloadID := c.Param("id")
	if workloadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Workload ID is required",
		})
		return
	}

	// Get the workload
	workload, err := h.scheduler.GetWorkloadByID(workloadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Workload not found",
			"message": err.Error(),
		})
		return
	}

	// Perform reconciliation
	result, err := h.scheduler.ReconcileWorkload(workload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to reconcile workload",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Workload reconciliation completed",
		"result":    result,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// UpdateWorkloadDesiredState updates the desired state of a workload
func (h *ReconciliationHandlers) UpdateWorkloadDesiredState(c *gin.Context) {
	workloadID := c.Param("id")
	if workloadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Workload ID is required",
		})
		return
	}

	var request struct {
		DesiredState string `json:"desiredState" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
		return
	}

	// Validate desired state
	validStates := map[string]bool{
		"Running": true,
		"Stopped": true,
		"Paused":  true,
	}

	if !validStates[request.DesiredState] {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid desired state. Must be one of: Running, Stopped, Paused",
		})
		return
	}

	// Get the workload
	workload, err := h.scheduler.GetWorkloadByID(workloadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Workload not found",
			"message": err.Error(),
		})
		return
	}

	// Update desired state
	workload.DesiredState = request.DesiredState

	// Save updated workload
	workloadJSON, err := json.Marshal(workload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to marshal workload",
			"message": err.Error(),
		})
		return
	}

	if err := h.scheduler.RetryableEtcdPut("/workloads/"+workloadID, string(workloadJSON)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to update workload",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Workload desired state updated successfully",
		"workload": gin.H{
			"id":            workload.ID,
			"name":          workload.Name,
			"desiredState":  workload.DesiredState,
			"currentStatus": workload.Status,
		},
		"timestamp": time.Now().Format(time.RFC3339),
	})
}
