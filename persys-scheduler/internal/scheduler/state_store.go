package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	metricspkg "github.com/persys-dev/persys-cloud/persys-scheduler/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	nodesPrefix          = "/nodes/"
	workloadsPrefix      = "/workloads/"
	workloadSpecPrefix   = "/workloads-spec/"
	workloadStatusPrefix = "/workloads-status/"
	assignmentsPrefix    = "/assignments/"
	reconciliationPrefix = "/reconciliation/"
	retriesPrefix        = "/retries/"
	driftsPrefix         = "/drifts/"
	eventsPrefix         = "/events/"
)

func workloadKey(workloadID string) string       { return workloadsPrefix + workloadID }
func workloadSpecKey(workloadID string) string   { return workloadSpecPrefix + workloadID }
func workloadStatusKey(workloadID string) string { return workloadStatusPrefix + workloadID }
func assignmentKey(workloadID string) string     { return assignmentsPrefix + workloadID }
func reconciliationKey(workloadID string) string { return reconciliationPrefix + workloadID }
func retryKey(workloadID string) string          { return retriesPrefix + workloadID }
func eventKey(eventID string) string             { return eventsPrefix + eventID }
func driftKey(nodeID, workloadID, driftType string) string {
	nodeID = strings.ReplaceAll(strings.TrimSpace(nodeID), "/", "_")
	workloadID = strings.ReplaceAll(strings.TrimSpace(workloadID), "/", "_")
	driftType = strings.ReplaceAll(strings.TrimSpace(driftType), "/", "_")
	if workloadID == "" {
		workloadID = "_unknown"
	}
	return driftsPrefix + nodeID + "/" + workloadID + "/" + driftType
}

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
	specPayload, err := json.Marshal(workloadSpecFromWorkload(workload))
	if err != nil {
		return fmt.Errorf("marshal workload %s: %w", workload.ID, err)
	}
	statusPayload, err := json.Marshal(workloadStatusFromWorkload(workload))
	if err != nil {
		return fmt.Errorf("marshal workload status %s: %w", workload.ID, err)
	}
	if err := s.RetryableEtcdPut(workloadSpecKey(workload.ID), string(specPayload)); err != nil {
		return err
	}
	metricspkg.IncStateStoreWrite("spec")
	if err := s.RetryableEtcdPut(workloadStatusKey(workload.ID), string(statusPayload)); err != nil {
		return err
	}
	metricspkg.IncStateStoreWrite("status")
	s.cacheWorkload(workload)
	retryPayload, err := json.Marshal(workload.Retry)
	if err == nil {
		_ = s.RetryableEtcdPut(retryKey(workload.ID), string(retryPayload))
		metricspkg.IncStateStoreWrite("retry")
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
	if err := s.RetryableEtcdPut(assignmentKey(workloadID), string(payload)); err != nil {
		return err
	}
	metricspkg.IncStateStoreWrite("assignment")
	s.cacheAssignment(rec)
	return nil
}

func (s *Scheduler) writeReconciliationRecord(workloadID, action string, success bool, reason string) {
	s.writeReconciliationTelemetry(workloadID, action, success, reason, time.Now().UTC())
	metricspkg.IncStateStoreWrite("reconciliation")
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
	if s.writeEventTelemetry(payload) {
		metricspkg.IncStateStoreWrite("event")
		return
	}
	_ = s.RetryableEtcdPut(eventKey(event.ID), string(payload))
	metricspkg.IncStateStoreWrite("event")
}

func (s *Scheduler) writeDriftRecord(record models.DriftRecord) {
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}
	_ = s.RetryableEtcdPut(driftKey(record.NodeID, record.WorkloadID, record.DriftType), string(payload))
}

func (s *Scheduler) clearDriftRecord(nodeID, workloadID, driftType string) {
	_ = s.RetryableEtcdDelete(driftKey(nodeID, workloadID, driftType))
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
