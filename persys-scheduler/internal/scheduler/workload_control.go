package scheduler

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
)

const (
	workloadFailureObservedAtKey = "failureObservedAt"
	workloadFailureGraceUntilKey = "failureGraceUntil"
	defaultFailureGracePeriod    = 2 * time.Minute
	minAttemptsBeforeBackoff     = 3
)

func (s *Scheduler) CreateWorkload(workload models.Workload) (models.Workload, error) {
	if err := s.requireWritable(); err != nil {
		return models.Workload{}, err
	}
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
	if err := s.requireWritable(); err != nil {
		return models.Workload{}, err
	}
	current, err := s.GetWorkloadByID(workloadID)
	if err != nil {
		return models.Workload{}, err
	}
	if current.Metadata == nil {
		current.Metadata = map[string]interface{}{}
	}
	specChanged := false
	desiredChanged := false

	if strings.TrimSpace(update.Name) != "" {
		if current.Name != update.Name {
			specChanged = true
		}
		current.Name = update.Name
	}
	if strings.TrimSpace(update.Type) != "" {
		if current.Type != update.Type {
			specChanged = true
		}
		current.Type = update.Type
	}
	if strings.TrimSpace(update.DesiredState) != "" {
		nextDesired := normalizeDesiredStateString(update.DesiredState)
		if current.DesiredState != nextDesired {
			desiredChanged = true
		}
		current.DesiredState = nextDesired
	}
	if strings.TrimSpace(update.Image) != "" {
		if current.Image != update.Image {
			specChanged = true
		}
		current.Image = update.Image
	}
	if update.Command != "" {
		if current.Command != update.Command {
			specChanged = true
		}
		current.Command = update.Command
	}
	if len(update.CommandList) > 0 {
		if !reflect.DeepEqual(current.CommandList, update.CommandList) {
			specChanged = true
		}
		current.CommandList = append([]string{}, update.CommandList...)
	}
	if update.Compose != "" {
		if current.Compose != update.Compose {
			specChanged = true
		}
		current.Compose = update.Compose
	}
	if update.ComposeYAML != "" {
		if current.ComposeYAML != update.ComposeYAML {
			specChanged = true
		}
		current.ComposeYAML = update.ComposeYAML
	}
	if update.ProjectName != "" {
		if current.ProjectName != update.ProjectName {
			specChanged = true
		}
		current.ProjectName = update.ProjectName
	}
	if len(update.EnvVars) > 0 {
		if !reflect.DeepEqual(current.EnvVars, update.EnvVars) {
			specChanged = true
		}
		current.EnvVars = update.EnvVars
	}
	if len(update.Labels) > 0 {
		if !reflect.DeepEqual(current.Labels, update.Labels) {
			specChanged = true
		}
		current.Labels = update.Labels
	}
	if len(update.Ports) > 0 {
		if !reflect.DeepEqual(current.Ports, update.Ports) {
			specChanged = true
		}
		current.Ports = update.Ports
	}
	if len(update.Volumes) > 0 {
		if !reflect.DeepEqual(current.Volumes, update.Volumes) {
			specChanged = true
		}
		current.Volumes = update.Volumes
	}
	if update.RestartPolicy != "" {
		if current.RestartPolicy != update.RestartPolicy {
			specChanged = true
		}
		current.RestartPolicy = update.RestartPolicy
	}
	if update.VM != nil {
		if !reflect.DeepEqual(current.VM, update.VM) {
			specChanged = true
		}
		current.VM = update.VM
	}

	now := time.Now().UTC()
	current.StatusInfo.LastUpdated = now
	switch {
	case specChanged:
		current.RevisionID = ""
		s.ensureWorkloadRevision(&current)
		current.Status = "Updating"
		current.StatusInfo.ActualState = "Pending"
		current.Metadata["last_action"] = "Updated"
	case desiredChanged:
		clearReapplyMetadata(&current)
		current.Metadata["last_action"] = "DesiredStateUpdated"
	default:
		current.Metadata["last_action"] = "NoopUpdate"
	}

	if err := s.saveWorkload(current); err != nil {
		return models.Workload{}, err
	}
	switch {
	case specChanged:
		s.emitEvent("WorkloadScheduled", current.ID, current.NodeID, "Workload updated", map[string]interface{}{"revision_id": current.RevisionID})
	case desiredChanged:
		s.emitEvent("WorkloadScheduled", current.ID, current.NodeID, "Desired state updated", map[string]interface{}{"desired_state": current.DesiredState})
	}
	return current, nil
}

func (s *Scheduler) MarkWorkloadDeleted(workloadID string) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
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
	if err := s.requireWritable(); err != nil {
		return models.Workload{}, err
	}
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
	workload.Retry.Attempts = 0
	clearReapplyMetadata(&workload)
	for _, key := range []string{
		terminalRetryMetadataKey,
		terminalRetryReasonMetadataKey,
		terminalFailureReasonMetadataKey,
	} {
		delete(workload.Metadata, key)
	}
	workload.Metadata["last_action"] = "RetryTriggered"
	if err := s.saveWorkload(workload); err != nil {
		return models.Workload{}, err
	}
	s.emitEvent("RetryTriggered", workload.ID, workload.NodeID, "Manual retry requested", map[string]interface{}{"attempts": workload.Retry.Attempts})
	return workload, nil
}

func (s *Scheduler) UpdateWorkloadRetryOnFailure(workloadID string, reason string) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
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

	now := time.Now().UTC()
	firstObserved, ok := metadataTimestamp(workload.Metadata, workloadFailureObservedAtKey)
	if !ok {
		firstObserved = now
		workload.Metadata[workloadFailureObservedAtKey] = firstObserved.Format(time.RFC3339)
	}
	graceUntil := firstObserved.Add(defaultFailureGracePeriod)

	if now.Before(graceUntil) {
		workload.Metadata[workloadFailureGraceUntilKey] = graceUntil.Format(time.RFC3339)
		if workload.Retry.Attempts < workload.Retry.MaxAttempts-1 {
			workload.Retry.Attempts++
		}
		backoff := retryBackoff(workload.Retry.Attempts)
		workload.Retry.NextRetryAt = now.Add(backoff)
		workload.Status = "RetryPending"
		workload.StatusInfo.ActualState = "Pending"
		workload.StatusInfo.LastUpdated = now
		applyFailureReason(&workload, reason)
		workload.Metadata["last_action"] = "RetryGracePeriod"
		s.emitEvent("RetryTriggered", workload.ID, workload.NodeID, "failure observed within grace period", map[string]interface{}{
			"attempts":            workload.Retry.Attempts,
			"next_retry_at":       workload.Retry.NextRetryAt.Format(time.RFC3339),
			"failure_grace_until": graceUntil.Format(time.RFC3339),
		})
		return s.saveWorkload(workload)
	}

	workload.Retry.Attempts++
	if workload.Retry.Attempts >= workload.Retry.MaxAttempts {
		workload.Status = "Failed"
		workload.StatusInfo.ActualState = "Failed"
		workload.StatusInfo.LastUpdated = now
		applyFailureReason(&workload, reason)
		workload.Metadata["last_action"] = "RetryExhausted"
		delete(workload.Metadata, workloadFailureObservedAtKey)
		delete(workload.Metadata, workloadFailureGraceUntilKey)
		s.emitEvent("WorkloadFailed", workload.ID, workload.NodeID, reason, map[string]interface{}{"attempts": workload.Retry.Attempts})
		return s.saveWorkload(workload)
	}

	backoff := retryBackoff(workload.Retry.Attempts)
	workload.Retry.NextRetryAt = now.Add(backoff)
	workload.Status = "RetryPending"
	workload.StatusInfo.LastUpdated = now
	applyFailureReason(&workload, reason)
	workload.Metadata["last_action"] = "RetryScheduled"
	s.emitEvent("RetryTriggered", workload.ID, workload.NodeID, reason, map[string]interface{}{"attempts": workload.Retry.Attempts, "next_retry_at": workload.Retry.NextRetryAt.Format(time.RFC3339)})
	return s.saveWorkload(workload)
}

func applyFailureReason(workload *models.Workload, reason string) {
	if workload == nil {
		return
	}
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	cleanReason := strings.TrimSpace(reason)
	workload.Metadata["scheduler_last_error"] = cleanReason

	runtimeReason := preferredRuntimeFailureReason(workload.Metadata)
	if runtimeReason != "" && isInfrastructureFailureReason(cleanReason) {
		workload.StatusInfo.FailureReason = runtimeReason
		workload.Metadata["last_error"] = runtimeReason
		return
	}

	workload.StatusInfo.FailureReason = cleanReason
	workload.Metadata["last_error"] = cleanReason
}

func preferredRuntimeFailureReason(metadata map[string]interface{}) string {
	for _, key := range []string{"container.stderr", "last_runtime_error", "container.runtime_error"} {
		if v, ok := metadataString(metadata, key); ok {
			clean := strings.TrimSpace(v)
			if clean != "" {
				return clean
			}
		}
	}
	return ""
}

func isInfrastructureFailureReason(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "" {
		return false
	}
	return (strings.Contains(lower, "node") && strings.Contains(lower, "unavailable")) ||
		strings.Contains(lower, "no failover target") ||
		strings.Contains(lower, "heartbeat expired") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "transport: error while dialing")
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt < minAttemptsBeforeBackoff {
		return 0
	}
	effectiveAttempt := attempt - (minAttemptsBeforeBackoff - 1)
	if effectiveAttempt < 1 {
		effectiveAttempt = 1
	}
	backoff := 5 * time.Second
	for i := 1; i < effectiveAttempt; i++ {
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

func clearReapplyMetadata(workload *models.Workload) {
	if workload == nil || workload.Metadata == nil {
		return
	}
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

func (s *Scheduler) EnsureWorkloadAssigned(workload *models.Workload) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
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
