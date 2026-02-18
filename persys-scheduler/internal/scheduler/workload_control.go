package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
)

func (s *Scheduler) CreateWorkload(workload models.Workload) (models.Workload, error) {
	if strings.TrimSpace(workload.ID) == "" {
		workload.ID = uuid.NewString()
	}
	s.ensureWorkloadRevision(&workload)
	s.initializeWorkloadDefaults(&workload)
	workload.Status = "Pending"
	workload.StatusInfo.ActualState = "Pending"
	workload.Metadata["last_action"] = "Created"

	if err := s.saveWorkload(workload); err != nil {
		return models.Workload{}, err
	}
	s.emitEvent("WorkloadScheduled", workload.ID, "", "Workload created", map[string]interface{}{"desired_state": workload.DesiredState})
	return workload, nil
}

func (s *Scheduler) UpdateWorkloadSpec(workloadID string, update models.Workload) (models.Workload, error) {
	current, err := s.GetWorkloadByID(workloadID)
	if err != nil {
		return models.Workload{}, err
	}
	if current.Metadata == nil {
		current.Metadata = map[string]interface{}{}
	}

	if strings.TrimSpace(update.Name) != "" {
		current.Name = update.Name
	}
	if strings.TrimSpace(update.Type) != "" {
		current.Type = update.Type
	}
	if strings.TrimSpace(update.DesiredState) != "" {
		current.DesiredState = normalizeDesiredStateString(update.DesiredState)
	}
	if strings.TrimSpace(update.Image) != "" {
		current.Image = update.Image
	}
	if update.Command != "" {
		current.Command = update.Command
	}
	if update.Compose != "" {
		current.Compose = update.Compose
	}
	if update.ComposeYAML != "" {
		current.ComposeYAML = update.ComposeYAML
	}
	if update.ProjectName != "" {
		current.ProjectName = update.ProjectName
	}
	if len(update.EnvVars) > 0 {
		current.EnvVars = update.EnvVars
	}
	if len(update.Labels) > 0 {
		current.Labels = update.Labels
	}
	if len(update.Ports) > 0 {
		current.Ports = update.Ports
	}
	if len(update.Volumes) > 0 {
		current.Volumes = update.Volumes
	}
	if update.RestartPolicy != "" {
		current.RestartPolicy = update.RestartPolicy
	}
	if update.VM != nil {
		current.VM = update.VM
	}

	current.RevisionID = ""
	s.ensureWorkloadRevision(&current)
	current.Status = "Updating"
	current.StatusInfo.ActualState = "Pending"
	current.StatusInfo.LastUpdated = time.Now().UTC()
	current.Metadata["last_action"] = "Updated"

	if err := s.saveWorkload(current); err != nil {
		return models.Workload{}, err
	}
	s.emitEvent("WorkloadScheduled", current.ID, current.NodeID, "Workload updated", map[string]interface{}{"revision_id": current.RevisionID})
	return current, nil
}

func (s *Scheduler) MarkWorkloadDeleted(workloadID string) error {
	workload, err := s.GetWorkloadByID(workloadID)
	if err != nil {
		return err
	}
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	workload.DesiredState = "Deleted"
	workload.Status = "Deleting"
	workload.StatusInfo.LastUpdated = time.Now().UTC()
	workload.Metadata["last_action"] = "MarkedDeleted"
	if err := s.saveWorkload(workload); err != nil {
		return err
	}
	s.emitEvent("WorkloadScheduled", workload.ID, workload.NodeID, "Marked for deletion", nil)
	return nil
}

func (s *Scheduler) TriggerWorkloadRetry(workloadID string) (models.Workload, error) {
	workload, err := s.GetWorkloadByID(workloadID)
	if err != nil {
		return models.Workload{}, err
	}
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	if workload.Retry.MaxAttempts <= 0 {
		workload.Retry.MaxAttempts = 5
	}
	workload.Retry.NextRetryAt = time.Now().UTC()
	workload.Metadata["last_action"] = "RetryTriggered"
	if err := s.saveWorkload(workload); err != nil {
		return models.Workload{}, err
	}
	s.emitEvent("RetryTriggered", workload.ID, workload.NodeID, "Manual retry requested", map[string]interface{}{"attempts": workload.Retry.Attempts})
	return workload, nil
}

func (s *Scheduler) UpdateWorkloadRetryOnFailure(workloadID string, reason string) error {
	workload, err := s.GetWorkloadByID(workloadID)
	if err != nil {
		return err
	}
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	if workload.Retry.MaxAttempts <= 0 {
		workload.Retry.MaxAttempts = 5
	}
	workload.Retry.Attempts++
	if workload.Retry.Attempts >= workload.Retry.MaxAttempts {
		workload.Status = "Failed"
		workload.StatusInfo.ActualState = "Failed"
		workload.StatusInfo.FailureReason = reason
		workload.StatusInfo.LastUpdated = time.Now().UTC()
		workload.Metadata["last_error"] = reason
		workload.Metadata["last_action"] = "RetryExhausted"
		s.emitEvent("WorkloadFailed", workload.ID, workload.NodeID, reason, map[string]interface{}{"attempts": workload.Retry.Attempts})
		return s.saveWorkload(workload)
	}

	backoff := retryBackoff(workload.Retry.Attempts)
	workload.Retry.NextRetryAt = time.Now().UTC().Add(backoff)
	workload.Status = "RetryPending"
	workload.StatusInfo.LastUpdated = time.Now().UTC()
	workload.StatusInfo.FailureReason = reason
	workload.Metadata["last_error"] = reason
	workload.Metadata["last_action"] = "RetryScheduled"
	s.emitEvent("RetryTriggered", workload.ID, workload.NodeID, reason, map[string]interface{}{"attempts": workload.Retry.Attempts, "next_retry_at": workload.Retry.NextRetryAt.Format(time.RFC3339)})
	return s.saveWorkload(workload)
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	backoff := 5 * time.Second
	for i := 1; i < attempt; i++ {
		backoff *= 2
	}
	if backoff > 2*time.Minute {
		return 2 * time.Minute
	}
	return backoff
}

func (s *Scheduler) dueForRetry(workload models.Workload) bool {
	if workload.Retry.Attempts >= workload.Retry.MaxAttempts && workload.Retry.MaxAttempts > 0 {
		return false
	}
	if workload.Retry.NextRetryAt.IsZero() {
		return false
	}
	return !time.Now().UTC().Before(workload.Retry.NextRetryAt)
}

func (s *Scheduler) DumpWorkloadRecord(workloadID string) (map[string]interface{}, error) {
	workload, err := s.GetWorkloadByID(workloadID)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{}
	raw, err := json.Marshal(workload)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Scheduler) EnsureWorkloadAssigned(workload *models.Workload) error {
	if workload.NodeID != "" {
		return nil
	}
	node, reason, err := s.selectNodeForWorkload(*workload)
	if err != nil {
		return err
	}
	if err := s.assignWorkload(workload, node, reason); err != nil {
		return fmt.Errorf("assign workload: %w", err)
	}
	return nil
}
