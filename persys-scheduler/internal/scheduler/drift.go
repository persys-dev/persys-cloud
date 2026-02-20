package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentpb "github.com/persys-dev/persys-cloud/persys-scheduler/internal/agentpb"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	"github.com/sirupsen/logrus"
)

var driftLogger = logging.C("scheduler.drift")

func (s *Scheduler) driftDetectInterval() time.Duration {
	if s.cfg != nil && s.cfg.SchedulerDriftDetectInterval > 0 {
		return s.cfg.SchedulerDriftDetectInterval
	}
	return 300 * time.Second
}

func (s *Scheduler) StartDriftDetection(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		driftLogger.Info("drift detection disabled (interval <= 0)")
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	driftLogger.WithField("interval", interval.String()).Info("starting drift detection loop")

	for {
		select {
		case <-ctx.Done():
			driftLogger.Info("stopping drift detection loop")
			return
		case <-ticker.C:
			s.detectDriftOnce(ctx)
		}
	}
}

func (s *Scheduler) detectDriftOnce(ctx context.Context) {
	nodes, err := s.GetNodes()
	if err != nil {
		driftLogger.WithError(err).Warn("drift detection: failed to list nodes")
		return
	}
	workloads, err := s.GetWorkloads()
	if err != nil {
		driftLogger.WithError(err).Warn("drift detection: failed to list workloads")
		return
	}

	byNode := make(map[string]map[string]models.Workload)
	byID := make(map[string]models.Workload, len(workloads))
	for _, w := range workloads {
		byID[w.ID] = w
		nodeID := strings.TrimSpace(w.NodeID)
		if nodeID == "" {
			continue
		}
		if byNode[nodeID] == nil {
			byNode[nodeID] = make(map[string]models.Workload)
		}
		byNode[nodeID][w.ID] = w
	}

	// Deduplicate probes by endpoint to avoid repeatedly polling stale node IDs
	// that point to the same agent endpoint after restarts.
	selected := make(map[string]models.Node, len(nodes))
	for _, node := range nodes {
		endpoint := s.grpcAddressForNode(node)
		if endpoint == "" {
			continue
		}
		if existing, ok := selected[endpoint]; ok {
			if node.LastHeartbeat.After(existing.LastHeartbeat) {
				selected[endpoint] = node
			}
			continue
		}
		selected[endpoint] = node
	}

	for _, node := range selected {
		if ok, reason := driftProbeEligible(node); !ok {
			driftLogger.WithFields(logrus.Fields{
				"node_id":  node.NodeID,
				"endpoint": s.grpcAddressForNode(node),
				"reason":   reason,
			}).Debug("drift detection: skipping node")
			continue
		}
		agentList, err := s.listWorkloadsFromNode(ctx, node)
		if err != nil {
			driftLogger.WithError(err).WithFields(logrus.Fields{
				"node_id":  node.NodeID,
				"endpoint": s.grpcAddressForNode(node),
			}).Warn("drift detection: failed to list workloads from agent")
			continue
		}
		s.compareNodeDrift(node, byNode[node.NodeID], byID, agentList)
	}
}

func driftProbeEligible(node models.Node) (bool, string) {
	status := strings.ToLower(strings.TrimSpace(node.Status))
	if status != "ready" && status != "active" {
		return false, fmt.Sprintf("node status=%q", node.Status)
	}
	if node.LastHeartbeat.IsZero() {
		return false, "missing last heartbeat"
	}
	if time.Since(node.LastHeartbeat) > 3*time.Minute {
		return false, fmt.Sprintf("heartbeat stale last=%s", node.LastHeartbeat.UTC().Format(time.RFC3339))
	}
	return true, ""
}

func (s *Scheduler) compareNodeDrift(node models.Node, expected map[string]models.Workload, expectedAll map[string]models.Workload, actual []*agentpb.WorkloadStatus) {
	if expected == nil {
		expected = map[string]models.Workload{}
	}
	if expectedAll == nil {
		expectedAll = map[string]models.Workload{}
	}

	actualByID := make(map[string]*agentpb.WorkloadStatus, len(actual))
	for _, ws := range actual {
		if ws == nil || strings.TrimSpace(ws.GetId()) == "" {
			continue
		}
		actualByID[ws.GetId()] = ws
	}

	orphan := 0
	missing := 0
	stateDrift := 0
	revisionDrift := 0

	for id, aw := range actualByID {
		sw, ok := expected[id]
		if !ok {
			orphan++
			action, actionErr := s.remediateOrphanOnAgent(node, id, aw, expectedAll)
			resolved := actionErr == nil
			s.markDrift(models.DriftRecord{
				NodeID:      node.NodeID,
				WorkloadID:  id,
				DriftType:   "orphan_on_agent",
				DetectedAt:  time.Now().UTC(),
				AgentStatus: mapActualStateToSchedulerStatus(aw.GetActualState()),
				Action:      action,
				Resolved:    resolved,
				LastError:   errString(actionErr),
			})
			fields := logrus.Fields{
				"node_id":      node.NodeID,
				"workload_id":  id,
				"agent_state":  aw.GetActualState().String(),
				"agent_reason": strings.TrimSpace(aw.GetMessage()),
				"drift_type":   "orphan_on_agent",
				"action":       action,
				"resolved":     resolved,
			}
			if actionErr != nil {
				fields["error"] = actionErr.Error()
			}
			driftLogger.WithFields(fields).Warn("drift detected")
			continue
		}

		expectedStatus := strings.ToLower(strings.TrimSpace(sw.Status))
		actualStatus := strings.ToLower(strings.TrimSpace(mapActualStateToSchedulerStatus(aw.GetActualState())))
		if expectedStatus != "" && actualStatus != "" && expectedStatus != actualStatus {
			stateDrift++
			actionErr := s.remediateStateMismatch(node, sw, aw)
			s.markDrift(models.DriftRecord{
				NodeID:          node.NodeID,
				WorkloadID:      id,
				DriftType:       "state_mismatch",
				DetectedAt:      time.Now().UTC(),
				SchedulerStatus: sw.Status,
				AgentStatus:     mapActualStateToSchedulerStatus(aw.GetActualState()),
				Action:          "align_scheduler_status",
				Resolved:        actionErr == nil,
				LastError:       errString(actionErr),
			})
			driftLogger.WithFields(logrus.Fields{
				"node_id":         node.NodeID,
				"workload_id":     id,
				"scheduler_state": sw.Status,
				"agent_state":     mapActualStateToSchedulerStatus(aw.GetActualState()),
				"drift_type":      "state_mismatch",
			}).Warn("drift detected")
		}

		if strings.TrimSpace(sw.RevisionID) != "" && strings.TrimSpace(aw.GetRevisionId()) != "" &&
			!strings.EqualFold(strings.TrimSpace(sw.RevisionID), strings.TrimSpace(aw.GetRevisionId())) {
			revisionDrift++
			actionErr := s.remediateRevisionMismatch(node, sw, aw)
			s.markDrift(models.DriftRecord{
				NodeID:          node.NodeID,
				WorkloadID:      id,
				DriftType:       "revision_mismatch",
				DetectedAt:      time.Now().UTC(),
				SchedulerStatus: sw.RevisionID,
				AgentStatus:     aw.GetRevisionId(),
				Action:          "reapply_scheduler_revision",
				Resolved:        actionErr == nil,
				LastError:       errString(actionErr),
			})
			driftLogger.WithFields(logrus.Fields{
				"node_id":            node.NodeID,
				"workload_id":        id,
				"scheduler_revision": sw.RevisionID,
				"agent_revision":     aw.GetRevisionId(),
				"drift_type":         "revision_mismatch",
			}).Warn("drift detected")
		}
	}

	for id, sw := range expected {
		if canonical, ok := expectedAll[id]; ok {
			owner := strings.TrimSpace(canonical.NodeID)
			if owner != "" && !strings.EqualFold(owner, node.NodeID) {
				// Workload was re-bound during this cycle; avoid conflicting remediation.
				continue
			}
		}
		if _, ok := actualByID[id]; ok {
			continue
		}
		desired := strings.ToLower(strings.TrimSpace(sw.DesiredState))
		if desired == "deleted" {
			continue
		}
		missing++
		actionErr := s.remediateMissingOnAgent(node, sw)
		s.markDrift(models.DriftRecord{
			NodeID:          node.NodeID,
			WorkloadID:      id,
			DriftType:       "missing_on_agent",
			DetectedAt:      time.Now().UTC(),
			SchedulerStatus: sw.Status,
			Action:          "retry_and_reconcile",
			Resolved:        actionErr == nil,
			LastError:       errString(actionErr),
		})
		driftLogger.WithFields(logrus.Fields{
			"node_id":       node.NodeID,
			"workload_id":   id,
			"scheduler":     sw.Status,
			"desired_state": sw.DesiredState,
			"drift_type":    "missing_on_agent",
		}).Warn("drift detected")
	}

	driftLogger.WithFields(logrus.Fields{
		"node_id":              node.NodeID,
		"expected_count":       len(expected),
		"agent_count":          len(actualByID),
		"orphan_on_agent":      orphan,
		"missing_on_agent":     missing,
		"state_mismatch_count": stateDrift,
		"revision_mismatch":    revisionDrift,
	}).Info("drift detection cycle complete for node")
}

func (s *Scheduler) remediateStateMismatch(node models.Node, sw models.Workload, aw *agentpb.WorkloadStatus) error {
	if !s.isWritable() {
		return errControlPlaneFrozen
	}
	actual := mapActualStateToSchedulerStatus(aw.GetActualState())
	if err := s.UpdateWorkloadStatus(sw.ID, actual); err != nil {
		return err
	}
	_ = s.UpdateWorkloadLogs(sw.ID, fmt.Sprintf("drift remediation: aligned scheduler state to agent state=%s", actual))
	return nil
}

func (s *Scheduler) remediateRevisionMismatch(node models.Node, sw models.Workload, aw *agentpb.WorkloadStatus) error {
	if !s.isWritable() {
		return errControlPlaneFrozen
	}
	resp, err := s.applyWorkloadOnNode(context.Background(), node, sw)
	if err != nil {
		_ = s.UpdateWorkloadRetryOnFailure(sw.ID, fmt.Sprintf("drift revision mismatch apply failed: %v", err))
		return err
	}
	if resp == nil || (!resp.GetApplied() && !resp.GetSkipped()) {
		msg := "agent rejected apply during drift remediation"
		if resp != nil && strings.TrimSpace(resp.GetMessage()) != "" {
			msg = resp.GetMessage()
		}
		_ = s.UpdateWorkloadRetryOnFailure(sw.ID, msg)
		return errors.New(msg)
	}
	_ = s.UpdateWorkloadLogs(sw.ID, "drift remediation: re-applied scheduler revision on agent")
	return nil
}

func (s *Scheduler) remediateMissingOnAgent(node models.Node, sw models.Workload) error {
	if !s.isWritable() {
		return errControlPlaneFrozen
	}
	_ = s.UpdateWorkloadRetryOnFailure(sw.ID, "drift: workload missing on agent")
	_, err := s.ReconcileWorkloadWithContext(context.Background(), sw)
	return err
}

func (s *Scheduler) markDrift(record models.DriftRecord) {
	if s.isWritable() {
		s.writeDriftRecord(record)
	}
	s.emitEvent("DriftDetected", record.WorkloadID, record.NodeID, record.DriftType, map[string]interface{}{
		"scheduler_status": record.SchedulerStatus,
		"agent_status":     record.AgentStatus,
		"action":           record.Action,
		"resolved":         record.Resolved,
		"last_error":       record.LastError,
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Scheduler) remediateOrphanOnAgent(node models.Node, workloadID string, aw *agentpb.WorkloadStatus, expectedAll map[string]models.Workload) (string, error) {
	if !s.isWritable() {
		return "control_plane_frozen", errControlPlaneFrozen
	}

	sw, known := expectedAll[workloadID]
	if !known {
		return "delete_orphan_on_agent", s.deleteOrphanFromNode(node, workloadID)
	}

	desired := strings.ToLower(strings.TrimSpace(sw.DesiredState))
	if desired == "deleted" {
		return "delete_deleted_residual_on_agent", s.deleteOrphanFromNode(node, workloadID)
	}

	expectedNodeID := strings.TrimSpace(sw.NodeID)
	if expectedNodeID == "" || strings.EqualFold(expectedNodeID, node.NodeID) {
		if err := s.adoptOrphanedWorkload(node, sw, aw, "missing_or_stale_assignment", expectedAll); err != nil {
			return "adopt_orphan_to_reporting_node", err
		}
		return "adopt_orphan_to_reporting_node", nil
	}

	expectedNode, err := s.GetNodeByID(expectedNodeID)
	if err != nil {
		if err := s.adoptOrphanedWorkload(node, sw, aw, "assigned_node_missing", expectedAll); err != nil {
			return "reassign_from_missing_owner", err
		}
		return "reassign_from_missing_owner", nil
	}

	if sameEndpoint(s.grpcAddressForNode(expectedNode), s.grpcAddressForNode(node)) {
		if err := s.adoptOrphanedWorkload(node, sw, aw, "node_id_rotated_same_endpoint", expectedAll); err != nil {
			return "rebind_assignment_to_rotated_node_id", err
		}
		return "rebind_assignment_to_rotated_node_id", nil
	}

	statusResp, statusErr := s.getWorkloadStatusFromNode(context.Background(), expectedNode, workloadID)
	if statusErr == nil && statusResp != nil {
		return "delete_duplicate_on_reporting_node", s.deleteOrphanFromNode(node, workloadID)
	}
	if statusErr != nil && !isWorkloadStatusNotFound(statusErr) {
		if isNodeUnreachableError(statusErr) && isNodeLikelyUnavailable(expectedNode) {
			if err := s.adoptOrphanedWorkload(node, sw, aw, "owner_unreachable_and_unavailable", expectedAll); err != nil {
				return "failover_reassign_to_reporting_node", err
			}
			return "failover_reassign_to_reporting_node", nil
		}
		return "operator_investigation", fmt.Errorf("failed to validate ownership on node %s: %w", expectedNode.NodeID, statusErr)
	}

	if err := s.adoptOrphanedWorkload(node, sw, aw, "owner_missing_workload", expectedAll); err != nil {
		return "reassign_to_reporting_node", err
	}
	return "reassign_to_reporting_node", nil
}

func (s *Scheduler) deleteOrphanFromNode(node models.Node, workloadID string) error {
	_, err := s.deleteWorkloadFromNode(context.Background(), node, workloadID)
	if err != nil && !isWorkloadStatusNotFound(err) {
		return err
	}
	return nil
}

func (s *Scheduler) adoptOrphanedWorkload(node models.Node, workload models.Workload, aw *agentpb.WorkloadStatus, reason string, expectedAll map[string]models.Workload) error {
	now := time.Now().UTC()
	previousNode := strings.TrimSpace(workload.NodeID)
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}

	workload.NodeID = node.NodeID
	workload.AssignedNode = node.NodeID
	workload.Metadata["last_action"] = "DriftRemediatedOrphan"
	workload.Metadata["previous_node"] = previousNode
	workload.Metadata["drift_reason"] = reason
	workload.Metadata["drift_reconciled_at"] = now.Format(time.RFC3339)

	actual := mapActualStateToSchedulerStatus(aw.GetActualState())
	if actual != "" {
		workload.Status = actual
		workload.StatusInfo.ActualState = actual
	}
	workload.StatusInfo.LastUpdated = now

	assignReason := fmt.Sprintf("drift remediation: %s", reason)
	if err := s.saveWorkload(workload); err != nil {
		return err
	}
	if err := s.writeAssignment(workload.ID, node.NodeID, assignReason); err != nil {
		return err
	}
	_ = s.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("drift remediation: reassigned workload to node %s (%s)", node.NodeID, reason))

	if expectedAll != nil {
		expectedAll[workload.ID] = workload
	}
	return nil
}

func sameEndpoint(a, b string) bool {
	av := strings.TrimSpace(a)
	bv := strings.TrimSpace(b)
	return av != "" && bv != "" && strings.EqualFold(av, bv)
}

func isNodeLikelyUnavailable(node models.Node) bool {
	status := strings.ToLower(strings.TrimSpace(node.Status))
	if status != "ready" && status != "active" {
		return true
	}
	if node.LastHeartbeat.IsZero() {
		return true
	}
	return time.Since(node.LastHeartbeat) > 3*time.Minute
}
