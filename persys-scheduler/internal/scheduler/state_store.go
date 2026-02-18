package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	nodesPrefix          = "/nodes/"
	workloadsPrefix      = "/workloads/"
	assignmentsPrefix    = "/assignments/"
	reconciliationPrefix = "/reconciliation/"
	retriesPrefix        = "/retries/"
	eventsPrefix         = "/events/"
)

func workloadKey(workloadID string) string       { return workloadsPrefix + workloadID }
func assignmentKey(workloadID string) string     { return assignmentsPrefix + workloadID }
func reconciliationKey(workloadID string) string { return reconciliationPrefix + workloadID }
func retryKey(workloadID string) string          { return retriesPrefix + workloadID }
func eventKey(eventID string) string             { return eventsPrefix + eventID }

func defaultRetryState() models.RetryState {
	return models.RetryState{
		Attempts:    0,
		MaxAttempts: 5,
	}
}

func normalizeDesiredStateString(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "stopped", "stop":
		return "Stopped"
	case "deleted", "delete":
		return "Deleted"
	default:
		return "Running"
	}
}

func (s *Scheduler) initializeWorkloadDefaults(workload *models.Workload) {
	if workload.DesiredState == "" {
		workload.DesiredState = "Running"
	}
	workload.DesiredState = normalizeDesiredStateString(workload.DesiredState)
	if workload.Status == "" {
		workload.Status = "Pending"
	}
	if workload.CreatedAt.IsZero() {
		workload.CreatedAt = time.Now().UTC()
	}
	if workload.Retry.MaxAttempts <= 0 {
		workload.Retry = defaultRetryState()
	}
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	if _, ok := workload.Metadata["created_at"]; !ok {
		workload.Metadata["created_at"] = workload.CreatedAt.Format(time.RFC3339)
	}
	if _, ok := workload.Metadata["last_action"]; !ok {
		workload.Metadata["last_action"] = "Created"
	}
	workload.StatusInfo.LastUpdated = time.Now().UTC()
	if workload.StatusInfo.ActualState == "" {
		workload.StatusInfo.ActualState = workload.Status
	}
}

func (s *Scheduler) saveWorkload(workload models.Workload) error {
	payload, err := json.Marshal(workload)
	if err != nil {
		return fmt.Errorf("marshal workload %s: %w", workload.ID, err)
	}
	if err := s.RetryableEtcdPut(workloadKey(workload.ID), string(payload)); err != nil {
		return err
	}
	retryPayload, err := json.Marshal(workload.Retry)
	if err == nil {
		_ = s.RetryableEtcdPut(retryKey(workload.ID), string(retryPayload))
	}
	return nil
}

func (s *Scheduler) writeAssignment(workloadID, nodeID, reason string) error {
	rec := models.AssignmentRecord{
		WorkloadID: workloadID,
		NodeID:     nodeID,
		Reason:     reason,
		CreatedAt:  time.Now().UTC(),
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.RetryableEtcdPut(assignmentKey(workloadID), string(payload))
}

func (s *Scheduler) writeReconciliationRecord(workloadID, action string, success bool, reason string) {
	rec := models.ReconciliationRecord{
		WorkloadID:  workloadID,
		Action:      action,
		Success:     success,
		Reason:      reason,
		AttemptedAt: time.Now().UTC(),
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_ = s.RetryableEtcdPut(reconciliationKey(workloadID), string(payload))
}

func (s *Scheduler) emitEvent(eventType, workloadID, nodeID, reason string, details map[string]interface{}) {
	event := models.SchedulerEvent{
		ID:         uuid.NewString(),
		Type:       eventType,
		WorkloadID: workloadID,
		NodeID:     nodeID,
		Reason:     reason,
		Timestamp:  time.Now().UTC(),
		Details:    details,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	_ = s.RetryableEtcdPut(eventKey(event.ID), string(payload))
}

func (s *Scheduler) ListSchedulerEvents(limit int64) ([]models.SchedulerEvent, error) {
	opts := []clientv3.OpOption{clientv3.WithPrefix()}
	if limit > 0 {
		opts = append(opts, clientv3.WithLimit(limit))
	}
	resp, err := s.RetryableEtcdGet(eventsPrefix, opts...)
	if err != nil {
		return nil, err
	}
	events := make([]models.SchedulerEvent, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var event models.SchedulerEvent
		if err := json.Unmarshal(kv.Value, &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	return events, nil
}
