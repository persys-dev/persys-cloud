package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/persys-dev/prow/internal/models"
)

const gracePeriod = 2 * time.Minute

// Reconciler handles reconciliation between desired and actual state
type Reconciler struct {
	scheduler *Scheduler
	monitor   *Monitor
}

// ReconciliationResult represents the result of a reconciliation operation
type ReconciliationResult struct {
	WorkloadID   string
	DesiredState string
	ActualState  string
	Action       string
	Success      bool
	ErrorMessage string
	RetryCount   int
	LastAttempt  time.Time
}

// NewReconciler creates a new Reconciler instance
func NewReconciler(scheduler *Scheduler, monitor *Monitor) *Reconciler {
	return &Reconciler{
		scheduler: scheduler,
		monitor:   monitor,
	}
}

// ReconcileWorkload reconciles a single workload to its desired state
func (r *Reconciler) ReconcileWorkload(workload models.Workload) (*ReconciliationResult, error) {
	result := &ReconciliationResult{
		WorkloadID:   workload.ID,
		DesiredState: workload.DesiredState,
		LastAttempt:  time.Now(),
	}

	// Get current actual state from agent
	actualState, err := r.getActualWorkloadState(workload)
	if err != nil {
		result.ActualState = "Unknown"
		result.Action = "Investigate"
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("Failed to get actual state: %v", err)
		return result, err
	}

	result.ActualState = actualState

	// Determine if reconciliation is needed
	if r.needsReconciliation(workload, actualState) {
		action, err := r.performReconciliation(workload, actualState)
		result.Action = action
		result.Success = err == nil
		if err != nil {
			result.ErrorMessage = err.Error()
		}
	} else {
		result.Action = "NoAction"
		result.Success = true
	}

	// Update workload with reconciliation result
	r.updateWorkloadReconciliationStatus(workload.ID, result)

	return result, nil
}

// getActualWorkloadState queries the agent to get the actual state of a workload
func (r *Reconciler) getActualWorkloadState(workload models.Workload) (string, error) {
	// Get the node for this workload
	node, err := r.scheduler.GetNodeByID(workload.NodeID)
	if err != nil {
		return "", fmt.Errorf("failed to get node %s: %v", workload.NodeID, err)
	}

	// Query agent for container status
	containers, err := r.monitor.getContainerList(node)
	if err != nil {
		return "", fmt.Errorf("failed to get container list: %v", err)
	}

	// Find container by workload ID
	for _, container := range containers {
		if container.Name == workload.ID {
			return r.mapContainerStatusToWorkloadState(container.Status), nil
		}
	}

	// Container not found
	return "Missing", nil
}

// mapContainerStatusToWorkloadState maps Docker container status to workload state
func (r *Reconciler) mapContainerStatusToWorkloadState(dockerStatus string) string {
	switch dockerStatus {
	case "running":
		return "Running"
	case "exited":
		return "Exited"
	case "created":
		return "Scheduled"
	case "paused":
		return "Stopped"
	case "restarting":
		return "Restarting"
	case "Pulling":
		return "Pulling"
	case "ImagePullBackOff":
		return "ImagePullBackOff"
	case "ContainerCreating":
		return "ContainerCreating"
	default:
		return "Unknown"
	}
}

// needsReconciliation determines if a workload needs reconciliation
func (r *Reconciler) needsReconciliation(workload models.Workload, actualState string) bool {
	desiredState := workload.DesiredState
	if desiredState == "" {
		desiredState = "Running"
	}

	// Grace period for Missing, Pulling, ContainerCreating
	if actualState == "Missing" || actualState == "Pulling" || actualState == "ContainerCreating" {
		if workload.Metadata != nil {
			if lastLaunchRaw, ok := workload.Metadata["lastLaunchTime"]; ok {
				var lastLaunch time.Time
				switch v := lastLaunchRaw.(type) {
				case time.Time:
					lastLaunch = v
				case string:
					t, err := time.Parse(time.RFC3339, v)
					if err == nil {
						lastLaunch = t
					}
				}
				if !lastLaunch.IsZero() && time.Since(lastLaunch) < gracePeriod {
					log.Printf("Workload %s is in %s state, within grace period (%.0fs left), skipping reconciliation", workload.ID, actualState, (gracePeriod - time.Since(lastLaunch)).Seconds())
					return false
				}
			}
		}
	}

	// Check if actual state matches desired state
	if actualState == desiredState {
		return false
	}

	// Special cases that don't need reconciliation
	if desiredState == "Stopped" && actualState == "Exited" {
		return false
	}
	if desiredState == "Running" && actualState == "Restarting" {
		return false // Give it time to restart
	}
	if actualState == "Pulling" || actualState == "ImagePullBackOff" || actualState == "ContainerCreating" {
		log.Printf("Workload %s is in state %s, waiting before reconciling", workload.ID, actualState)
		return false // Wait for image pull or container creation
	}

	return true
}

// performReconciliation performs the actual reconciliation action
func (r *Reconciler) performReconciliation(workload models.Workload, actualState string) (string, error) {
	desiredState := workload.DesiredState
	if desiredState == "" {
		desiredState = "Running"
	}

	log.Printf("Reconciling workload %s: actual=%s, desired=%s", workload.ID, actualState, desiredState)

	switch {
	case actualState == "Missing" && desiredState == "Running":
		return r.recreateWorkload(workload)
	case actualState == "Exited" && desiredState == "Running":
		return r.restartWorkload(workload)
	case actualState == "Running" && desiredState == "Stopped":
		return r.stopWorkload(workload)
	case actualState == "Failed" && desiredState == "Running":
		return r.recreateWorkload(workload)
	case actualState == "ImagePullBackOff" && desiredState == "Running":
		log.Printf("Workload %s is in ImagePullBackOff, will retry image pull on next cycle", workload.ID)
		return "WaitForImagePull", nil
	default:
		return "NoAction", fmt.Errorf("no reconciliation action defined for actual=%s, desired=%s", actualState, desiredState)
	}
}

// recreateWorkload recreates a missing or failed workload
func (r *Reconciler) recreateWorkload(workload models.Workload) (string, error) {
	log.Printf("Recreating workload %s", workload.ID)

	// Get the node
	node, err := r.scheduler.GetNodeByID(workload.NodeID)
	if err != nil {
		return "Recreate", fmt.Errorf("failed to get node: %v", err)
	}

	// Prepare the recreation payload
	var endpoint string
	var payload interface{}

	switch workload.Type {
	case "docker-container":
		endpoint = "/docker/run"
		payload = map[string]interface{}{
			"workloadId":    workload.ID,
			"image":         workload.Image,
			"name":          workload.ID,
			"displayName":   workload.Name,
			"command":       workload.Command,
			"env":           workload.EnvVars,
			"ports":         workload.Ports,
			"volumes":       workload.Volumes,
			"network":       workload.Network,
			"restartPolicy": workload.RestartPolicy,
			"detach":        true,
			"async":         true,
		}
	case "docker-compose":
		endpoint = "/compose/run"
		payload = map[string]interface{}{
			"workloadId":   workload.ID,
			"displayName":  workload.Name,
			"composeDir":   workload.LocalPath,
			"envVariables": workload.EnvVars,
			"async":        true,
		}
	default:
		return "Recreate", fmt.Errorf("unsupported workload type for recreation: %s", workload.Type)
	}

	// Send recreation command
	_, err = r.scheduler.SendCommandToNode(node, endpoint, payload)
	if err != nil {
		return "Recreate", fmt.Errorf("failed to recreate workload: %v", err)
	}

	// Update workload status
	r.scheduler.UpdateWorkloadStatus(workload.ID, "Recreating")
	r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Workload recreated at %s", time.Now().Format(time.RFC3339)))

	// Update Metadata["lastLaunchTime"]
	if workload.Metadata == nil {
		workload.Metadata = make(map[string]interface{})
	}
	workload.Metadata["lastLaunchTime"] = time.Now()

	return "Recreate", nil
}

// restartWorkload restarts an exited workload
func (r *Reconciler) restartWorkload(workload models.Workload) (string, error) {
	log.Printf("Restarting workload %s", workload.ID)

	node, err := r.scheduler.GetNodeByID(workload.NodeID)
	if err != nil {
		return "Restart", fmt.Errorf("failed to get node: %v", err)
	}

	endpoint := fmt.Sprintf("/docker/start/%s", workload.ID)
	_, err = r.scheduler.SendCommandToNode(node, endpoint, nil)
	if err != nil {
		return "Restart", fmt.Errorf("failed to restart workload: %v", err)
	}

	r.scheduler.UpdateWorkloadStatus(workload.ID, "Restarting")
	r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Workload restarted at %s", time.Now().Format(time.RFC3339)))

	// Update Metadata["lastLaunchTime"]
	if workload.Metadata == nil {
		workload.Metadata = make(map[string]interface{})
	}
	workload.Metadata["lastLaunchTime"] = time.Now()

	return "Restart", nil
}

// stopWorkload stops a running workload
func (r *Reconciler) stopWorkload(workload models.Workload) (string, error) {
	log.Printf("Stopping workload %s", workload.ID)

	node, err := r.scheduler.GetNodeByID(workload.NodeID)
	if err != nil {
		return "Stop", fmt.Errorf("failed to get node: %v", err)
	}

	endpoint := fmt.Sprintf("/docker/stop/%s", workload.ID)
	_, err = r.scheduler.SendCommandToNode(node, endpoint, nil)
	if err != nil {
		return "Stop", fmt.Errorf("failed to stop workload: %v", err)
	}

	r.scheduler.UpdateWorkloadStatus(workload.ID, "Stopping")
	r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Workload stopped at %s", time.Now().Format(time.RFC3339)))

	// Update Metadata["lastLaunchTime"]
	if workload.Metadata == nil {
		workload.Metadata = make(map[string]interface{})
	}
	workload.Metadata["lastLaunchTime"] = time.Now()

	return "Stop", nil
}

// updateWorkloadReconciliationStatus updates the workload with reconciliation metadata
func (r *Reconciler) updateWorkloadReconciliationStatus(workloadID string, result *ReconciliationResult) {
	workload, err := r.scheduler.GetWorkloadByID(workloadID)
	if err != nil {
		log.Printf("Failed to get workload %s for reconciliation status update: %v", workloadID, err)
		return
	}

	// Add reconciliation metadata
	if workload.Metadata == nil {
		workload.Metadata = make(map[string]interface{})
	}
	workload.Metadata["lastReconciliation"] = result.LastAttempt
	workload.Metadata["lastReconciliationAction"] = result.Action
	workload.Metadata["lastReconciliationSuccess"] = result.Success
	workload.Metadata["reconciliationRetryCount"] = result.RetryCount

	// Update the workload
	workloadJSON, err := json.Marshal(workload)
	if err != nil {
		log.Printf("Failed to marshal workload %s: %v", workloadID, err)
		return
	}

	if err := r.scheduler.RetryableEtcdPut("/workloads/"+workloadID, string(workloadJSON)); err != nil {
		log.Printf("Failed to update workload %s reconciliation status: %v", workloadID, err)
	}
}

// ReconcileAllWorkloads reconciles all workloads in the system
func (r *Reconciler) ReconcileAllWorkloads() ([]*ReconciliationResult, error) {
	workloads, err := r.scheduler.GetWorkloads()
	if err != nil {
		return nil, fmt.Errorf("failed to get workloads: %v", err)
	}

	var results []*ReconciliationResult
	for _, workload := range workloads {
		// Skip workloads that are in terminal states
		if workload.Status == "Completed" || workload.Status == "Deleted" {
			continue
		}

		result, err := r.ReconcileWorkload(workload)
		if err != nil {
			log.Printf("Failed to reconcile workload %s: %v", workload.ID, err)
			continue
		}
		results = append(results, result)
	}

	return results, nil
}

// StartReconciliationLoop starts the continuous reconciliation loop
func (r *Reconciler) StartReconciliationLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("Starting reconciliation loop with interval %v", interval)

	for {
		select {
		case <-ctx.Done():
			log.Printf("Stopping reconciliation loop")
			return
		case <-ticker.C:
			log.Printf("Starting reconciliation cycle")
			results, err := r.ReconcileAllWorkloads()
			if err != nil {
				log.Printf("Reconciliation cycle failed: %v", err)
				continue
			}

			// Log reconciliation summary
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

			log.Printf("Reconciliation cycle completed: %d workloads processed, %d actions taken, %d successful",
				len(results), actionCount, successCount)
		}
	}
}

// GetReconciliationStats returns statistics about reconciliation performance
func (r *Reconciler) GetReconciliationStats() (map[string]interface{}, error) {
	workloads, err := r.scheduler.GetWorkloads()
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"totalWorkloads":        len(workloads),
		"needingReconciliation": 0,
		"reconciliationErrors":  0,
		"lastReconciliation":    time.Time{},
	}

	for _, workload := range workloads {
		if workload.Metadata != nil {
			if lastRecon, ok := workload.Metadata["lastReconciliation"]; ok {
				if lastReconTime, ok := lastRecon.(time.Time); ok {
					if lastReconTime.After(stats["lastReconciliation"].(time.Time)) {
						stats["lastReconciliation"] = lastReconTime
					}
				}
			}

			if success, ok := workload.Metadata["lastReconciliationSuccess"]; ok {
				if !success.(bool) {
					stats["reconciliationErrors"] = stats["reconciliationErrors"].(int) + 1
				}
			}
		}

		// Check if workload needs reconciliation
		actualState, err := r.getActualWorkloadState(workload)
		if err == nil && r.needsReconciliation(workload, actualState) {
			stats["needingReconciliation"] = stats["needingReconciliation"].(int) + 1
		}
	}

	return stats, nil
}
