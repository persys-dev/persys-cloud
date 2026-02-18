package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentpb "github.com/persys-dev/persys-cloud/persys-scheduler/internal/agentpb"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	metricspkg "github.com/persys-dev/persys-cloud/persys-scheduler/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	"github.com/sirupsen/logrus"
)

const defaultMissingGracePeriod = 15 * time.Second
const defaultNodeUnavailableGrace = 3 * time.Minute

var reconcilerLogger = logging.C("scheduler.reconciler")

// Reconciler handles reconciliation between desired and actual state.
type Reconciler struct {
	scheduler *Scheduler
	monitor   *Monitor
}

// ReconciliationResult represents the result of a reconciliation operation.
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

// NewReconciler creates a new Reconciler instance.
func NewReconciler(scheduler *Scheduler, monitor *Monitor) *Reconciler {
	return &Reconciler{scheduler: scheduler, monitor: monitor}
}

// ReconcileWorkload reconciles a single workload to its desired state.
func (r *Reconciler) ReconcileWorkload(ctx context.Context, workload models.Workload) (*ReconciliationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := &ReconciliationResult{
		WorkloadID:   workload.ID,
		DesiredState: workload.DesiredState,
		LastAttempt:  time.Now(),
	}
	defer func() {
		action := strings.TrimSpace(result.Action)
		if action == "" {
			action = "Unknown"
		}
		metricspkg.ObserveReconciliationResult(action, result.Success)
	}()

	// Respect retry backoff windows to avoid hammering unavailable nodes.
	if !workload.Retry.NextRetryAt.IsZero() && time.Now().UTC().Before(workload.Retry.NextRetryAt) {
		result.Action = "BackoffWait"
		result.Success = true
		return result, nil
	}

	if strings.EqualFold(workload.DesiredState, "Deleted") && workload.NodeID == "" {
		if err := r.scheduler.DeleteWorkload(workload.ID); err != nil {
			result.Action = "FinalizeDelete"
			result.Success = false
			result.ErrorMessage = err.Error()
			r.scheduler.writeReconciliationRecord(workload.ID, result.Action, false, result.ErrorMessage)
			r.updateWorkloadReconciliationStatus(workload.ID, result)
			return result, err
		}
		result.Action = "FinalizeDelete"
		result.Success = true
		r.scheduler.writeReconciliationRecord(workload.ID, result.Action, true, "Deleted from state store")
		return result, nil
	}

	if err := r.scheduler.EnsureWorkloadAssigned(&workload); err != nil && !strings.EqualFold(workload.DesiredState, "Deleted") {
		result.ActualState = "Unknown"
		result.Action = "Assign"
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("failed to assign node: %v", err)
		r.scheduler.writeReconciliationRecord(workload.ID, result.Action, false, result.ErrorMessage)
		r.updateWorkloadReconciliationStatus(workload.ID, result)
		return result, err
	}

	if r.scheduler.dueForRetry(workload) {
		workload.Retry.NextRetryAt = time.Time{}
		workload.Status = "Pending"
		if workload.Metadata == nil {
			workload.Metadata = map[string]interface{}{}
		}
		workload.Metadata["last_action"] = "RetryDue"
		_ = r.scheduler.saveWorkload(workload)
	}

	if strings.TrimSpace(workload.NodeID) != "" && !strings.EqualFold(workload.DesiredState, "Deleted") {
		waiting, failoverErr := r.handleUnavailableAssignedNode(&workload)
		if failoverErr != nil {
			result.ActualState = "Unknown"
			result.Action = "Reschedule"
			result.Success = false
			result.ErrorMessage = failoverErr.Error()
			r.scheduler.writeReconciliationRecord(workload.ID, result.Action, false, result.ErrorMessage)
			_ = r.scheduler.UpdateWorkloadRetryOnFailure(workload.ID, result.ErrorMessage)
			r.updateWorkloadReconciliationStatus(workload.ID, result)
			return result, failoverErr
		}
		if waiting {
			result.ActualState = "Unknown"
			result.Action = "AwaitFailover"
			result.Success = true
			r.scheduler.writeReconciliationRecord(workload.ID, result.Action, true, "node unavailable; waiting for retry window")
			r.updateWorkloadReconciliationStatus(workload.ID, result)
			return result, nil
		}
	}

	actualState, err := r.getActualWorkloadState(ctx, workload)
	if err != nil {
		if strings.TrimSpace(workload.NodeID) != "" && isNodeUnreachableError(err) && !strings.EqualFold(workload.DesiredState, "Deleted") {
			_ = r.scheduler.MarkNodeNotReady(workload.NodeID, err.Error())
			waiting, failoverErr := r.handleUnavailableAssignedNode(&workload)
			if failoverErr != nil {
				result.ActualState = "Unknown"
				result.Action = "Reschedule"
				result.Success = false
				result.ErrorMessage = failoverErr.Error()
				r.scheduler.writeReconciliationRecord(workload.ID, result.Action, false, result.ErrorMessage)
				_ = r.scheduler.UpdateWorkloadRetryOnFailure(workload.ID, result.ErrorMessage)
				r.updateWorkloadReconciliationStatus(workload.ID, result)
				return result, failoverErr
			}
			if waiting {
				result.ActualState = "Unknown"
				result.Action = "AwaitFailover"
				result.Success = true
				r.scheduler.writeReconciliationRecord(workload.ID, result.Action, true, "node unreachable; waiting for retry window")
				r.updateWorkloadReconciliationStatus(workload.ID, result)
				return result, nil
			}
			// Node changed; re-read actual state on newly assigned node.
			actualState, err = r.getActualWorkloadState(ctx, workload)
			if err == nil {
				result.ActualState = actualState
			}
		}
	}
	if err != nil {
		result.ActualState = "Unknown"
		result.Action = "Investigate"
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("failed to get actual state: %v", err)
		r.scheduler.writeReconciliationRecord(workload.ID, result.Action, false, result.ErrorMessage)
		r.updateWorkloadReconciliationStatus(workload.ID, result)
		return result, err
	}
	result.ActualState = actualState

	if r.needsReconciliation(workload, actualState) {
		action, err := r.performReconciliation(ctx, workload, actualState)
		result.Action = action
		result.Success = err == nil
		if err != nil {
			if handled, hErr := r.handleRuntimeUnavailable(&workload, err); handled {
				if hErr != nil {
					result.Success = false
					result.ErrorMessage = hErr.Error()
					_ = r.scheduler.UpdateWorkloadRetryOnFailure(workload.ID, result.ErrorMessage)
				} else {
					result.Action = "ReschedulePending"
					result.Success = true
					result.ErrorMessage = ""
				}
			} else {
				result.ErrorMessage = err.Error()
				_ = r.scheduler.UpdateWorkloadRetryOnFailure(workload.ID, result.ErrorMessage)
			}
		}
	} else {
		result.Action = "NoAction"
		result.Success = true
	}

	r.scheduler.writeReconciliationRecord(workload.ID, result.Action, result.Success, result.ErrorMessage)
	r.updateWorkloadReconciliationStatus(workload.ID, result)
	return result, nil
}

func (r *Reconciler) handleRuntimeUnavailable(workload *models.Workload, applyErr error) (bool, error) {
	if workload == nil || strings.TrimSpace(workload.NodeID) == "" || applyErr == nil {
		return false, nil
	}
	msg := strings.ToLower(applyErr.Error())
	if !strings.Contains(msg, "runtime not available") {
		return false, nil
	}

	oldNode := workload.NodeID
	if err := r.scheduler.MarkNodeWorkloadTypeUnsupported(oldNode, workload.Type, applyErr.Error()); err != nil {
		return true, err
	}

	// Force reassignment on next reconciliation cycle.
	workload.NodeID = ""
	workload.AssignedNode = ""
	workload.Status = "Pending"
	workload.StatusInfo.LastUpdated = time.Now().UTC()
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	workload.Metadata["last_action"] = "RuntimeUnsupportedReschedulePending"
	workload.Metadata["last_error"] = applyErr.Error()

	if err := r.scheduler.saveWorkload(*workload); err != nil {
		return true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	_, _ = r.scheduler.etcdClient.Delete(ctx, assignmentKey(workload.ID))
	_ = r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Runtime unavailable on node %s; scheduling on another node", oldNode))
	r.scheduler.emitEvent("Rescheduled", workload.ID, oldNode, "runtime unsupported on assigned node", map[string]interface{}{"workload_type": workload.Type})
	reconcilerLogger.WithFields(logrus.Fields{
		"workload_id": workload.ID,
		"old_node_id": oldNode,
	}).Warn("workload marked for reassignment after runtime unavailable")
	return true, nil
}

func (r *Reconciler) handleUnavailableAssignedNode(workload *models.Workload) (bool, error) {
	node, err := r.scheduler.GetNodeByID(workload.NodeID)
	if err != nil {
		return false, nil
	}

	unavailableGrace := schedulerDurationEnv("SCHEDULER_NODE_UNAVAILABLE_GRACE", defaultNodeUnavailableGrace)
	nodeUnavailable := (!strings.EqualFold(node.Status, "Ready") && !strings.EqualFold(node.Status, "Active")) ||
		time.Since(node.LastHeartbeat) > unavailableGrace
	if !nodeUnavailable {
		return false, nil
	}

	oldNode := workload.NodeID
	nextNode, reason, selErr := r.scheduler.selectNodeForWorkload(*workload)
	if selErr != nil {
		reconcilerLogger.WithError(selErr).WithFields(logrus.Fields{
			"workload_id": workload.ID,
			"old_node_id": oldNode,
		}).Warn("failover pending: no suitable node available")
		if err := r.scheduler.UpdateWorkloadRetryOnFailure(workload.ID, fmt.Sprintf("node %s unavailable; no failover target: %v", oldNode, selErr)); err != nil {
			return false, err
		}
		return true, nil
	}

	if strings.EqualFold(nextNode.NodeID, oldNode) {
		reconcilerLogger.WithFields(logrus.Fields{
			"workload_id": workload.ID,
			"node_id":     oldNode,
		}).Warn("failover deferred: selected same node")
		if err := r.scheduler.UpdateWorkloadRetryOnFailure(workload.ID, fmt.Sprintf("node %s unavailable; selected same node", oldNode)); err != nil {
			return false, err
		}
		return true, nil
	}

	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	workload.Metadata["previous_node"] = oldNode
	workload.Metadata["last_action"] = "Rescheduled"
	workload.Retry.Attempts = 0
	workload.Retry.NextRetryAt = time.Time{}

	failoverReason := fmt.Sprintf("node %s unavailable; %s", oldNode, reason)
	if err := r.scheduler.assignWorkload(workload, nextNode, failoverReason); err != nil {
		return false, err
	}
	reconcilerLogger.WithFields(logrus.Fields{
		"workload_id": workload.ID,
		"from_node":   oldNode,
		"to_node":     nextNode.NodeID,
	}).Info("rescheduled workload")
	_ = r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Rescheduled from node %s to %s", oldNode, nextNode.NodeID))
	r.scheduler.emitEvent("Rescheduled", workload.ID, nextNode.NodeID, failoverReason, map[string]interface{}{"previous_node": oldNode})
	return false, nil
}

// getActualWorkloadState queries the agent to get the actual state of a workload.
func (r *Reconciler) getActualWorkloadState(ctx context.Context, workload models.Workload) (string, error) {
	node, err := r.scheduler.GetNodeByID(workload.NodeID)
	if err != nil {
		return "", fmt.Errorf("failed to get node %s: %v", workload.NodeID, err)
	}

	statusResp, err := r.scheduler.getWorkloadStatusFromNode(ctx, node, workload.ID)
	if err != nil {
		if isWorkloadStatusNotFound(err) {
			return "Missing", nil
		}
		return "", fmt.Errorf("failed to get workload status: %v", err)
	}
	if statusResp == nil {
		return "Unknown", nil
	}
	return mapActualStateToSchedulerStatus(statusResp.GetActualState()), nil
}

// needsReconciliation determines if a workload needs reconciliation.
func (r *Reconciler) needsReconciliation(workload models.Workload, actualState string) bool {
	desiredState := strings.TrimSpace(workload.DesiredState)
	if desiredState == "" {
		desiredState = "Running"
	}

	if actualState == "Missing" || actualState == "Pending" {
		if workload.Metadata != nil {
			if lastLaunchRaw, ok := workload.Metadata["lastLaunchTime"]; ok {
				grace := schedulerDurationEnv("SCHEDULER_MISSING_GRACE_PERIOD", defaultMissingGracePeriod)
				switch v := lastLaunchRaw.(type) {
				case string:
					if t, err := time.Parse(time.RFC3339, v); err == nil && time.Since(t) < grace {
						return false
					}
				case time.Time:
					if time.Since(v) < grace {
						return false
					}
				}
			}
		}
	}

	if strings.EqualFold(actualState, desiredState) {
		return false
	}
	if strings.EqualFold(desiredState, "Stopped") && strings.EqualFold(actualState, "Stopped") {
		return false
	}
	return true
}

// performReconciliation performs the actual reconciliation action.
func (r *Reconciler) performReconciliation(ctx context.Context, workload models.Workload, actualState string) (string, error) {
	desiredState := strings.TrimSpace(workload.DesiredState)
	if desiredState == "" {
		desiredState = "Running"
	}

	reconcilerLogger.WithFields(logrus.Fields{
		"workload_id":   workload.ID,
		"actual_state":  actualState,
		"desired_state": desiredState,
	}).Debug("reconciling workload")

	switch {
	case strings.EqualFold(desiredState, "Deleted"):
		return r.deleteDesiredWorkload(ctx, workload)
	case strings.EqualFold(desiredState, "Running") && (strings.EqualFold(actualState, "Missing") || strings.EqualFold(actualState, "Stopped") || strings.EqualFold(actualState, "Failed") || strings.EqualFold(actualState, "Unknown")):
		return r.applyDesiredState(ctx, workload, agentpb.DesiredState_DESIRED_STATE_RUNNING, "ReapplyRunning")
	case strings.EqualFold(desiredState, "Stopped") && (strings.EqualFold(actualState, "Running") || strings.EqualFold(actualState, "Pending") || strings.EqualFold(actualState, "Unknown")):
		return r.applyDesiredState(ctx, workload, agentpb.DesiredState_DESIRED_STATE_STOPPED, "ApplyStopped")
	default:
		return "NoAction", nil
	}
}

func (r *Reconciler) applyDesiredState(ctx context.Context, workload models.Workload, desired agentpb.DesiredState, action string) (string, error) {
	node, err := r.scheduler.GetNodeByID(workload.NodeID)
	if err != nil {
		return action, fmt.Errorf("failed to get node: %v", err)
	}

	if desired == agentpb.DesiredState_DESIRED_STATE_STOPPED {
		workload.DesiredState = "Stopped"
	} else {
		workload.DesiredState = "Running"
	}

	resp, err := r.scheduler.applyWorkloadOnNode(ctx, node, workload)
	if err != nil {
		return action, fmt.Errorf("failed to apply desired state: %v", err)
	}
	if resp == nil {
		return action, fmt.Errorf("apply response was nil")
	}
	if !resp.GetApplied() && !resp.GetSkipped() {
		return action, fmt.Errorf("agent rejected apply: %s", strings.TrimSpace(resp.GetMessage()))
	}

	if resp.Status != nil {
		_ = r.scheduler.UpdateWorkloadStatus(workload.ID, mapActualStateToSchedulerStatus(resp.Status.ActualState))
	}
	_ = r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Reconciler action %s at %s", action, time.Now().Format(time.RFC3339)))
	r.scheduler.emitEvent("WorkloadScheduled", workload.ID, workload.NodeID, action, nil)

	if workload.Metadata == nil {
		workload.Metadata = make(map[string]interface{})
	}
	workload.Metadata["lastLaunchTime"] = time.Now().Format(time.RFC3339)

	workloadJSON, marshalErr := json.Marshal(workload)
	if marshalErr == nil {
		_ = r.scheduler.RetryableEtcdPut("/workloads/"+workload.ID, string(workloadJSON))
	}

	return action, nil
}

func (r *Reconciler) deleteDesiredWorkload(ctx context.Context, workload models.Workload) (string, error) {
	if workload.NodeID != "" {
		node, err := r.scheduler.GetNodeByID(workload.NodeID)
		if err == nil {
			if _, err := r.scheduler.deleteWorkloadFromNode(ctx, node, workload.ID); err != nil && !isWorkloadStatusNotFound(err) {
				return "Delete", fmt.Errorf("failed deleting workload on node: %v", err)
			}
		}
	}
	if err := r.scheduler.DeleteWorkloadWithContext(ctx, workload.ID); err != nil {
		return "Delete", err
	}
	r.scheduler.emitEvent("Rescheduled", workload.ID, workload.NodeID, "Deleted workload", nil)
	return "Delete", nil
}

// updateWorkloadReconciliationStatus updates the workload with reconciliation metadata.
func (r *Reconciler) updateWorkloadReconciliationStatus(workloadID string, result *ReconciliationResult) {
	workload, err := r.scheduler.GetWorkloadByID(workloadID)
	if err != nil {
		reconcilerLogger.WithError(err).WithField("workload_id", workloadID).Warn("failed to fetch workload for reconciliation status update")
		return
	}

	if workload.Metadata == nil {
		workload.Metadata = make(map[string]interface{})
	}
	workload.Metadata["lastReconciliation"] = result.LastAttempt.Format(time.RFC3339)
	workload.Metadata["lastReconciliationAction"] = result.Action
	workload.Metadata["lastReconciliationSuccess"] = result.Success
	workload.Metadata["reconciliationRetryCount"] = result.RetryCount

	workloadJSON, err := json.Marshal(workload)
	if err != nil {
		reconcilerLogger.WithError(err).WithField("workload_id", workloadID).Warn("failed to marshal workload during reconciliation status update")
		return
	}
	if err := r.scheduler.RetryableEtcdPut("/workloads/"+workloadID, string(workloadJSON)); err != nil {
		reconcilerLogger.WithError(err).WithField("workload_id", workloadID).Warn("failed to persist reconciliation status")
	}
}

// ReconcileAllWorkloads reconciles all workloads in the system.
func (r *Reconciler) ReconcileAllWorkloads(ctx context.Context) ([]*ReconciliationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	workloads, err := r.scheduler.GetWorkloads()
	if err != nil {
		return nil, fmt.Errorf("failed to get workloads: %v", err)
	}

	var results []*ReconciliationResult
	for _, workload := range workloads {
		if workload.Status == "Completed" || workload.Status == "Deleted" {
			continue
		}
		result, err := r.ReconcileWorkload(ctx, workload)
		if err != nil {
			reconcilerLogger.WithError(err).WithField("workload_id", workload.ID).Warn("failed to reconcile workload")
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// StartReconciliationLoop starts the continuous reconciliation loop.
func (r *Reconciler) StartReconciliationLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	reconcilerLogger.WithField("interval", interval.String()).Info("starting reconciliation loop")
	for {
		select {
		case <-ctx.Done():
			reconcilerLogger.Info("stopping reconciliation loop")
			return
		case <-ticker.C:
			cycleStart := time.Now()
			results, err := r.ReconcileAllWorkloads(ctx)
			metricspkg.ObserveReconciliationCycle(time.Since(cycleStart), err)
			if err != nil {
				reconcilerLogger.WithError(err).Error("reconciliation cycle failed")
				continue
			}
			successCount := 0
			actionCount := 0
			failureCount := 0
			for _, result := range results {
				if result.Success {
					successCount++
				} else {
					failureCount++
				}
				if result.Action != "NoAction" {
					actionCount++
				}
			}
			if actionCount > 0 || failureCount > 0 {
				reconcilerLogger.WithFields(logrus.Fields{
					"workloads_processed": len(results),
					"actions":             actionCount,
					"successful":          successCount,
					"failed":              failureCount,
				}).Info("reconciliation cycle summary")
			}
			if err := r.scheduler.RefreshStateMetrics(); err != nil {
				reconcilerLogger.WithError(err).Warn("failed to refresh scheduler state metrics")
			}
		}
	}
}

// GetReconciliationStats returns statistics about reconciliation performance.
func (r *Reconciler) GetReconciliationStats() (map[string]interface{}, error) {
	workloads, err := r.scheduler.GetWorkloads()
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"totalWorkloads":        len(workloads),
		"needingReconciliation": 0,
		"reconciliationErrors":  0,
		"lastReconciliation":    "",
	}

	latest := time.Time{}
	for _, workload := range workloads {
		if workload.Metadata != nil {
			if lastRecon, ok := workload.Metadata["lastReconciliation"]; ok {
				if s, ok := lastRecon.(string); ok {
					if t, err := time.Parse(time.RFC3339, s); err == nil && t.After(latest) {
						latest = t
					}
				}
			}
			if success, ok := workload.Metadata["lastReconciliationSuccess"]; ok {
				if s, ok := success.(bool); ok && !s {
					stats["reconciliationErrors"] = stats["reconciliationErrors"].(int) + 1
				}
			}
		}
		actualState, err := r.getActualWorkloadState(context.Background(), workload)
		if err == nil && r.needsReconciliation(workload, actualState) {
			stats["needingReconciliation"] = stats["needingReconciliation"].(int) + 1
		}
	}
	if !latest.IsZero() {
		stats["lastReconciliation"] = latest.Format(time.RFC3339)
	}

	return stats, nil
}
