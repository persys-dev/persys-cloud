package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
const workloadReapplyTimestampKey = "lastApplyRequestAt"
const workloadReapplyRevisionKey = "lastApplyRevision"
const workloadReapplyAttemptsKey = "reapplyAttempts"
const workloadReapplyNextAtKey = "reapplyNextAt"
const maxReapplyBackoff = 15 * time.Minute
const terminalRetryMetadataKey = "retry_terminal"
const terminalRetryReasonMetadataKey = "retry_terminal_reason"
const terminalFailureReasonMetadataKey = "terminal_failure_reason"

var reconcilerLogger = logging.C("scheduler.reconciler")

var nonRetryableFailureReasons = map[string]struct{}{
	"INVALID_IMAGE":         {},
	"INVALID_SPECIFICATION": {},
	"INVALID_CONFIGURATION": {},
	"PORT_BIND_CONFLICT":    {},
}

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
	if !r.scheduler.isWritable() {
		return &ReconciliationResult{
			WorkloadID:   workload.ID,
			DesiredState: workload.DesiredState,
			Action:       "Frozen",
			Success:      false,
			ErrorMessage: errControlPlaneFrozen.Error(),
			LastAttempt:  time.Now(),
		}, errControlPlaneFrozen
	}
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
	// Do not gate retries until multiple failures have occurred.
	if workload.Retry.Attempts >= minAttemptsBeforeBackoff &&
		!workload.Retry.NextRetryAt.IsZero() &&
		time.Now().UTC().Before(workload.Retry.NextRetryAt) {
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

	unavailableGrace := defaultNodeUnavailableGrace
	if r.scheduler.cfg != nil && r.scheduler.cfg.SchedulerNodeUnavailableGrace > 0 {
		unavailableGrace = r.scheduler.cfg.SchedulerNodeUnavailableGrace
	}
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
	if len(statusResp.GetMetadata()) > 0 {
		_ = r.scheduler.UpdateWorkloadMetadata(workload.ID, statusResp.GetMetadata())
	}
	if strings.EqualFold(mapActualStateToSchedulerStatus(statusResp.GetActualState()), "Failed") {
		if msg := strings.TrimSpace(statusResp.GetMessage()); msg != "" {
			_ = r.scheduler.UpdateWorkloadMetadata(workload.ID, map[string]string{"last_runtime_error": msg})
		}
	}
	return mapActualStateToSchedulerStatus(statusResp.GetActualState()), nil
}

// needsReconciliation determines if a workload needs reconciliation.
func (r *Reconciler) needsReconciliation(workload models.Workload, actualState string) bool {
	desiredState := strings.TrimSpace(workload.DesiredState)
	if desiredState == "" {
		desiredState = "Running"
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
	case strings.EqualFold(desiredState, "Running") && (strings.EqualFold(actualState, "Missing") || strings.EqualFold(actualState, "Stopped") || strings.EqualFold(actualState, "Failed")):
		if halt, reason := r.shouldHaltReapply(workload, actualState); halt {
			lastAction, _ := metadataString(workload.Metadata, "last_action")
			if !strings.EqualFold(strings.TrimSpace(lastAction), "TerminalFailureNoReapply") {
				reconcilerLogger.WithFields(logrus.Fields{
					"workload_id":   workload.ID,
					"node_id":       workload.NodeID,
					"actual_state":  actualState,
					"desired_state": desiredState,
					"reason":        reason,
				}).Warn("reapply halted due to terminal workload failure")

				if workload.Metadata == nil {
					workload.Metadata = map[string]interface{}{}
				}
				workload.Status = "Failed"
				workload.StatusInfo.ActualState = "Failed"
				workload.StatusInfo.FailureReason = reason
				workload.StatusInfo.LastUpdated = time.Now().UTC()
				workload.Metadata["last_action"] = "TerminalFailureNoReapply"
				workload.Metadata[terminalFailureReasonMetadataKey] = reason
				clearReapplyMetadata(&workload)
				_ = r.scheduler.saveWorkload(workload)
				_ = r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Reapply halted due to terminal failure: %s", reason))
			}
			return "NoAction", nil
		}

		if guarded, wait := r.reapplyStillGuarded(workload); guarded {
			reconcilerLogger.WithFields(logrus.Fields{
				"workload_id":   workload.ID,
				"node_id":       workload.NodeID,
				"actual_state":  actualState,
				"desired_state": desiredState,
				"wait_until":    wait.Format(time.RFC3339),
			}).Warn("reapply deferred by exponential backoff")
			_ = r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Reapply deferred by backoff until %s (state=%s desired=%s)", wait.Format(time.RFC3339), actualState, desiredState))
			return "ReapplyBackoffWait", nil
		}
		lastApplyAt, _ := metadataString(workload.Metadata, workloadReapplyTimestampKey)
		lastApplyRevision, _ := metadataString(workload.Metadata, workloadReapplyRevisionKey)
		lastAction, _ := metadataString(workload.Metadata, "last_action")
		lastReconAction, _ := metadataString(workload.Metadata, "lastReconciliationAction")
		reconcilerLogger.WithFields(logrus.Fields{
			"workload_id":                         workload.ID,
			"node_id":                             workload.NodeID,
			"workload_type":                       workload.Type,
			"desired_state":                       desiredState,
			"actual_state":                        actualState,
			"workload_status":                     workload.Status,
			"reason":                              reapplyReason(actualState),
			"revision_id":                         strings.TrimSpace(workload.RevisionID),
			"last_apply_request_at":               strings.TrimSpace(lastApplyAt),
			"last_apply_revision":                 strings.TrimSpace(lastApplyRevision),
			"retry_attempts":                      workload.Retry.Attempts,
			"retry_next_at":                       workload.Retry.NextRetryAt.UTC().Format(time.RFC3339),
			"metadata_last_action":                strings.TrimSpace(lastAction),
			"metadata_last_reconciliation_action": strings.TrimSpace(lastReconAction),
		}).Warn("reconciliation decided to reapply workload to reach desired running state")
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

	// Confirm current runtime state before issuing an apply to avoid hammering
	// long-running operations (e.g. VM install/boot) after transient control-plane issues.
	statusResp, statusErr := r.scheduler.getWorkloadStatusFromNode(ctx, node, workload.ID)
	if statusErr != nil && !isWorkloadStatusNotFound(statusErr) {
		return action, fmt.Errorf("pre-apply status check failed: %v", statusErr)
	}
	if statusResp != nil {
		current := mapActualStateToSchedulerStatus(statusResp.GetActualState())
		if desired == agentpb.DesiredState_DESIRED_STATE_RUNNING && strings.EqualFold(current, "Running") {
			_ = r.scheduler.UpdateWorkloadStatus(workload.ID, current)
			if len(statusResp.GetMetadata()) > 0 {
				_ = r.scheduler.UpdateWorkloadMetadata(workload.ID, statusResp.GetMetadata())
			}
			r.resetReapplyBackoff(&workload)
			return "NoAction", nil
		}
		if desired == agentpb.DesiredState_DESIRED_STATE_STOPPED && strings.EqualFold(current, "Stopped") {
			_ = r.scheduler.UpdateWorkloadStatus(workload.ID, current)
			if len(statusResp.GetMetadata()) > 0 {
				_ = r.scheduler.UpdateWorkloadMetadata(workload.ID, statusResp.GetMetadata())
			}
			r.resetReapplyBackoff(&workload)
			return "NoAction", nil
		}
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
		if len(resp.Status.GetMetadata()) > 0 {
			_ = r.scheduler.UpdateWorkloadMetadata(workload.ID, resp.Status.GetMetadata())
		}
	}
	_ = r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Reconciler action %s at %s", action, time.Now().Format(time.RFC3339)))
	r.scheduler.emitEvent("WorkloadScheduled", workload.ID, workload.NodeID, action, nil)

	if workload.Metadata == nil {
		workload.Metadata = make(map[string]interface{})
	}
	if desired == agentpb.DesiredState_DESIRED_STATE_RUNNING {
		now := time.Now().UTC().Format(time.RFC3339)
		workload.Metadata[workloadReapplyTimestampKey] = now
		workload.Metadata[workloadReapplyRevisionKey] = strings.TrimSpace(workload.RevisionID)
		workload.Metadata["lastLaunchTime"] = now
		nextAttempt, nextAt := r.nextReapplyAttempt(workload)
		workload.Metadata[workloadReapplyAttemptsKey] = nextAttempt
		workload.Metadata[workloadReapplyNextAtKey] = nextAt.Format(time.RFC3339)
		_ = r.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Reapply attempt %d recorded; next retry allowed after %s", nextAttempt, nextAt.Format(time.RFC3339)))
	} else {
		// Stop/delete actions must not arm running-state reapply backoff windows.
		for _, key := range []string{
			workloadReapplyAttemptsKey,
			workloadReapplyNextAtKey,
			workloadReapplyTimestampKey,
			workloadReapplyRevisionKey,
			"lastLaunchTime",
		} {
			delete(workload.Metadata, key)
		}
	}

	_, marshalErr := json.Marshal(workload)
	if marshalErr == nil {
		if err := r.scheduler.saveWorkload(workload); err != nil {
			return action, fmt.Errorf("persist apply metadata: %w", err)
		}
	}

	return action, nil
}

func (r *Reconciler) reapplyStillGuarded(workload models.Workload) (bool, time.Time) {
	if attempts, ok := metadataInt(workload.Metadata, workloadReapplyAttemptsKey); !ok || attempts < minAttemptsBeforeBackoff {
		return false, time.Time{}
	}

	currentRevision := strings.TrimSpace(workload.RevisionID)
	lastRevision, hasRevision := metadataString(workload.Metadata, workloadReapplyRevisionKey)
	if hasRevision && currentRevision != "" && !strings.EqualFold(strings.TrimSpace(lastRevision), currentRevision) {
		return false, time.Time{}
	}
	if t, ok := metadataTimestamp(workload.Metadata, workloadReapplyNextAtKey); ok {
		if time.Now().UTC().Before(t) {
			return true, t
		}
		return false, time.Time{}
	}

	// Backward compatibility with older metadata: static guard from last apply.
	guard := r.baseReapplyBackoff(workload)
	if t, ok := metadataTimestamp(workload.Metadata, workloadReapplyTimestampKey); ok && time.Since(t) < guard {
		return true, t.Add(guard)
	}
	if t, ok := metadataTimestamp(workload.Metadata, "lastLaunchTime"); ok && time.Since(t) < guard {
		return true, t.Add(guard)
	}
	return false, time.Time{}
}

func metadataTimestamp(metadata map[string]interface{}, key string) (time.Time, bool) {
	if metadata == nil {
		return time.Time{}, false
	}
	raw, ok := metadata[key]
	if !ok {
		return time.Time{}, false
	}
	switch v := raw.(type) {
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, false
		}
		return t, true
	case time.Time:
		return v, true
	default:
		return time.Time{}, false
	}
}

func metadataString(metadata map[string]interface{}, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	raw, ok := metadata[key]
	if !ok {
		return "", false
	}
	switch v := raw.(type) {
	case string:
		return v, true
	default:
		return "", false
	}
}

func metadataInt(metadata map[string]interface{}, key string) (int, bool) {
	if metadata == nil {
		return 0, false
	}
	raw, ok := metadata[key]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func (r *Reconciler) baseReapplyBackoff(workload models.Workload) time.Duration {
	guard := r.scheduler.applyTimeoutFor(workload)
	if r.scheduler.cfg != nil && r.scheduler.cfg.SchedulerReapplyGuard > 0 {
		guard = r.scheduler.cfg.SchedulerReapplyGuard
	}
	if guard < defaultMissingGracePeriod {
		guard = defaultMissingGracePeriod
	}
	return guard
}

func (r *Reconciler) nextReapplyAttempt(workload models.Workload) (int, time.Time) {
	attempt, ok := metadataInt(workload.Metadata, workloadReapplyAttemptsKey)
	if !ok || attempt < 0 {
		attempt = 0
	}
	attempt++

	base := r.baseReapplyBackoff(workload)
	effectiveAttempt := attempt - (minAttemptsBeforeBackoff - 1)
	if effectiveAttempt < 1 {
		effectiveAttempt = 1
	}
	multiplier := math.Pow(2, float64(effectiveAttempt-1))
	backoff := time.Duration(float64(base) * multiplier)
	if backoff > maxReapplyBackoff {
		backoff = maxReapplyBackoff
	}
	return attempt, time.Now().UTC().Add(backoff)
}

func (r *Reconciler) resetReapplyBackoff(workload *models.Workload) {
	if workload == nil || workload.Metadata == nil {
		return
	}
	changed := false
	for _, key := range []string{workloadReapplyAttemptsKey, workloadReapplyNextAtKey, workloadReapplyTimestampKey, workloadReapplyRevisionKey} {
		if _, exists := workload.Metadata[key]; exists {
			delete(workload.Metadata, key)
			changed = true
		}
	}
	if !changed {
		return
	}
	workload.StatusInfo.LastUpdated = time.Now().UTC()
	_ = r.scheduler.saveWorkload(*workload)
}

func reapplyReason(actualState string) string {
	switch {
	case strings.EqualFold(actualState, "Missing"):
		return "agent reported workload missing"
	case strings.EqualFold(actualState, "Stopped"):
		return "agent reported workload stopped while desired running"
	case strings.EqualFold(actualState, "Failed"):
		return "agent reported workload failed while desired running"
	default:
		return "desired/actual state mismatch"
	}
}

func (r *Reconciler) shouldHaltReapply(workload models.Workload, actualState string) (bool, string) {
	if !strings.EqualFold(strings.TrimSpace(workload.DesiredState), "Running") {
		return false, ""
	}
	if !(strings.EqualFold(actualState, "Failed") || strings.EqualFold(actualState, "Stopped") || strings.EqualFold(actualState, "Missing")) {
		return false, ""
	}

	if isMetadataTrue(workload.Metadata, terminalRetryMetadataKey) {
		reason := strings.TrimSpace(runtimeFailureReason(workload.Metadata))
		if reason == "" {
			reason = "agent marked workload as terminal/non-retryable"
		}
		return true, reason
	}

	if failureCode, ok := metadataString(workload.Metadata, "failure_reason"); ok {
		if _, terminal := nonRetryableFailureReasons[strings.ToUpper(strings.TrimSpace(failureCode))]; terminal {
			reason := strings.TrimSpace(runtimeFailureReason(workload.Metadata))
			if reason == "" {
				reason = fmt.Sprintf("non-retryable failure reason: %s", strings.TrimSpace(failureCode))
			}
			return true, reason
		}
	}

	if workload.Retry.MaxAttempts > 0 &&
		workload.Retry.Attempts >= workload.Retry.MaxAttempts &&
		(strings.EqualFold(actualState, "Failed") || strings.EqualFold(workload.Status, "Failed")) {
		reason := strings.TrimSpace(runtimeFailureReason(workload.Metadata))
		if reason == "" {
			reason = fmt.Sprintf("scheduler retry limit reached (%d/%d)", workload.Retry.Attempts, workload.Retry.MaxAttempts)
		}
		return true, reason
	}

	return false, ""
}

func runtimeFailureReason(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	for _, key := range []string{
		terminalRetryReasonMetadataKey,
		terminalFailureReasonMetadataKey,
		"container.stderr",
		"last_runtime_error",
		"container.runtime_error",
		"last_error",
		"failure_reason",
	} {
		if v, ok := metadataString(metadata, key); ok {
			clean := strings.TrimSpace(v)
			if clean != "" {
				return clean
			}
		}
	}
	return ""
}

func isMetadataTrue(metadata map[string]interface{}, key string) bool {
	if metadata == nil {
		return false
	}
	raw, ok := metadata[key]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
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

	// High-churn reconciliation metadata should live in Redis when available.
	if r.scheduler.redisClient != nil {
		ttl := 24 * time.Hour
		if r.scheduler.cfg != nil && r.scheduler.cfg.RedisReconcileTTL > 0 {
			ttl = r.scheduler.cfg.RedisReconcileTTL
		}
		statusKey := fmt.Sprintf("workload:%s:reconcile_status", workloadID)
		if payload, mErr := json.Marshal(workload.Metadata); mErr == nil {
			if err := r.scheduler.redisClient.Set(context.Background(), statusKey, payload, ttl).Err(); err == nil {
				return
			}
		}
	}

	currentLastAction, _ := workload.Metadata["last_action"].(string)
	if result.Action == "NoAction" && currentLastAction == "NoAction" {
		return
	}
	workload.Metadata["last_action"] = result.Action

	if err := r.scheduler.saveWorkload(workload); err != nil {
		reconcilerLogger.WithError(err).WithField("workload_id", workloadID).Warn("failed to persist reconciliation status")
		return
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
			if !r.scheduler.isWritable() {
				continue
			}
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
