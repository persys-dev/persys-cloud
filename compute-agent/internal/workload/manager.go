package workload

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/errors"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/metrics"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/platform"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/resources"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/retry"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/runtime"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/state"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
)

const (
	pendingSinceMetadataKey           = "pending_since"
	pendingRecoveryActionMetadataKey  = "pending_recovery_action"
	pendingRecoveryReasonMetadataKey  = "pending_recovery_reason"
	pendingRecoveryDeletedMetadataKey = "pending_recovery_deleted"
	retryTerminalMetadataKey          = "retry_terminal"
	retryTerminalReasonMetadataKey    = "retry_terminal_reason"
	defaultPendingRecoveryThreshold   = 5 * time.Minute
)

// Manager handles workload lifecycle with idempotency
type Manager struct {
	store           state.Store
	volumeState     state.ManagedVolumeStore
	volumeMgr       platform.VolumeManager
	runtimeMgr      *runtime.Manager
	logger          *logrus.Entry
	metrics         *metrics.Metrics
	resourceMonitor *resources.Monitor
	workloadOpMu    sync.Mutex
	workloadOpLocks map[string]*sync.Mutex

	retryPolicy   *retry.RetryPolicy
	retryTrackers map[string]*retry.RetryTracker
	retryMu       sync.Mutex
}

// NewManager creates a new workload manager
func NewManager(store state.Store, runtimeMgr *runtime.Manager, logger *logrus.Logger) *Manager {
	var volumeState state.ManagedVolumeStore
	if casted, ok := store.(state.ManagedVolumeStore); ok {
		volumeState = casted
	}
	return &Manager{
		store:           store,
		volumeState:     volumeState,
		runtimeMgr:      runtimeMgr,
		logger:          logger.WithField("component", "workload-manager"),
		workloadOpLocks: make(map[string]*sync.Mutex),
		retryPolicy:     retry.DefaultRetryPolicy(),
		retryTrackers:   make(map[string]*retry.RetryTracker),
	}
}

// SetVolumeManager enables managed-volume lifecycle orchestration.
func (m *Manager) SetVolumeManager(volumeMgr platform.VolumeManager) {
	m.volumeMgr = volumeMgr
}

// SetMetrics sets the metrics instance for recording operational metrics
func (m *Manager) SetMetrics(metricsInst *metrics.Metrics) {
	m.metrics = metricsInst
}

// SetResourceMonitor sets the resource monitor for availability checks
func (m *Manager) SetResourceMonitor(monitor *resources.Monitor) {
	m.resourceMonitor = monitor
}

// SetRetryPolicy sets retry policy used by reconciliation when handling failed workloads
func (m *Manager) SetRetryPolicy(policy *retry.RetryPolicy) {
	if policy == nil {
		policy = retry.DefaultRetryPolicy()
	}
	m.retryMu.Lock()
	m.retryPolicy = policy
	m.retryMu.Unlock()
}

func (m *Manager) getRetryTracker(workloadID string) *retry.RetryTracker {
	m.retryMu.Lock()
	defer m.retryMu.Unlock()
	tracker, ok := m.retryTrackers[workloadID]
	if !ok {
		tracker = retry.NewRetryTracker(m.retryPolicy)
		m.retryTrackers[workloadID] = tracker
	}
	return tracker
}

func (m *Manager) resetRetryTracker(workloadID string) {
	m.retryMu.Lock()
	delete(m.retryTrackers, workloadID)
	m.retryMu.Unlock()
}

func (m *Manager) lockWorkloadOp(workloadID string) func() {
	m.workloadOpMu.Lock()
	opLock, ok := m.workloadOpLocks[workloadID]
	if !ok {
		opLock = &sync.Mutex{}
		m.workloadOpLocks[workloadID] = opLock
	}
	m.workloadOpMu.Unlock()

	opLock.Lock()
	return opLock.Unlock
}

func ensureMetadata(status *models.WorkloadStatus) {
	if status.Metadata == nil {
		status.Metadata = make(map[string]string)
	}
}

func isRetryTerminal(status *models.WorkloadStatus) bool {
	if status == nil || status.Metadata == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(status.Metadata[retryTerminalMetadataKey]), "true")
}

func markRetryTerminal(status *models.WorkloadStatus, tracker *retry.RetryTracker, reason string) {
	if status == nil {
		return
	}
	ensureMetadata(status)
	status.Metadata[retryTerminalMetadataKey] = "true"
	status.Metadata[retryTerminalReasonMetadataKey] = strings.TrimSpace(reason)
	if tracker != nil {
		status.Metadata["retry_attempts"] = fmt.Sprintf("%d", tracker.GetAttemptCount())
	}
}

func isRuntimeMissing(message string) bool {
	msg := strings.ToLower(message)
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no such") ||
		strings.Contains(msg, "does not exist")
}

func normalizeStateForDesired(desired models.DesiredState, actual models.ActualState, message string) (models.ActualState, string) {
	if desired == models.DesiredStateStopped && actual == models.ActualStateFailed {
		msg := strings.TrimSpace(message)
		if msg == "" {
			return models.ActualStateStopped, "stopped"
		}
		return models.ActualStateStopped, fmt.Sprintf("stopped (previous runtime error: %s)", msg)
	}
	return actual, message
}

func clearRetryStateMetadata(status *models.WorkloadStatus) {
	if status == nil || status.Metadata == nil {
		return
	}
	for _, key := range []string{
		"retry_attempts",
		"next_retry_time",
		"failure_reason",
		"last_error",
		retryTerminalMetadataKey,
		retryTerminalReasonMetadataKey,
	} {
		delete(status.Metadata, key)
	}
}

func (m *Manager) persistFailedStatus(workload *models.Workload, message string, err error) {
	failedStatus, statusErr := m.store.GetStatus(workload.ID)
	if statusErr != nil || failedStatus == nil {
		failedStatus = &models.WorkloadStatus{
			ID:           workload.ID,
			Type:         workload.Type,
			RevisionID:   workload.RevisionID,
			DesiredState: workload.DesiredState,
			CreatedAt:    time.Now(),
		}
	}

	failedStatus.Type = workload.Type
	failedStatus.RevisionID = workload.RevisionID
	failedStatus.DesiredState = workload.DesiredState
	failedStatus.ActualState = models.ActualStateFailed
	failedStatus.Message = message
	failedStatus.UpdatedAt = time.Now()
	ensureMetadata(failedStatus)
	failedStatus.Metadata["last_failure_at"] = time.Now().Format(time.RFC3339)
	if err != nil {
		failedStatus.Metadata["last_error"] = err.Error()
	}

	if saveErr := m.store.SaveStatus(failedStatus); saveErr != nil {
		m.logger.Warnf("Failed to persist failed status for %s: %v", workload.ID, saveErr)
	}
}

func (m *Manager) persistManagedStorageFailureStatus(workload *models.Workload, code, message string, err error) {
	status, statusErr := m.store.GetStatus(workload.ID)
	if statusErr != nil || status == nil {
		status = &models.WorkloadStatus{
			ID:           workload.ID,
			Type:         workload.Type,
			RevisionID:   workload.RevisionID,
			DesiredState: workload.DesiredState,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
	}
	status.ActualState = models.ActualStateFailed
	status.Message = strings.TrimSpace(message)
	status.UpdatedAt = time.Now()
	ensureMetadata(status)
	status.Metadata["failure_reason"] = strings.TrimSpace(code)
	status.Metadata["failure_message"] = strings.TrimSpace(message)
	if err != nil {
		status.Metadata["last_error"] = err.Error()
	}
	if saveErr := m.store.SaveStatus(status); saveErr != nil {
		m.logger.Warnf("Failed to persist managed-storage failure status for %s: %v", workload.ID, saveErr)
	}
}

func failureReasonCodeFromRuntimeError(err error) string {
	if err == nil {
		return ""
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "cloud-init-invalid"):
		return "CLOUD_INIT_INVALID"
	default:
		return ""
	}
}

func (m *Manager) annotateFailureReason(workloadID, code, message string) {
	if strings.TrimSpace(workloadID) == "" || strings.TrimSpace(code) == "" {
		return
	}
	status, err := m.store.GetStatus(workloadID)
	if err != nil || status == nil {
		return
	}
	ensureMetadata(status)
	status.Metadata["failure_reason"] = strings.TrimSpace(code)
	if strings.TrimSpace(message) != "" {
		status.Metadata["failure_message"] = strings.TrimSpace(message)
	}
	if saveErr := m.store.SaveStatus(status); saveErr != nil {
		m.logger.Warnf("Failed to annotate failure reason for %s: %v", workloadID, saveErr)
	}
}

func (m *Manager) enrichStatusWithRuntimeMetadata(ctx context.Context, rt runtime.Runtime, workloadID string, status *models.WorkloadStatus) {
	provider, ok := rt.(runtime.StatusMetadataProvider)
	if !ok || status == nil {
		return
	}

	metadata, err := provider.StatusMetadata(ctx, workloadID)
	if err != nil {
		m.logger.Debugf("No runtime metadata available for %s: %v", workloadID, err)
		return
	}

	if len(metadata) == 0 {
		return
	}

	ensureMetadata(status)
	for k, v := range metadata {
		status.Metadata[k] = v
	}
}

// enrichStatusWithUsage populates status.Usage with a fresh per-workload
// resource utilization sample when the runtime supports it. Any error (e.g.
// the workload isn't currently running) is logged at debug level and simply
// leaves status.Usage unset - callers already treat a nil Usage as "no sample
// available" throughout the rest of the pipeline (heartbeat, gRPC responses).
func (m *Manager) enrichStatusWithUsage(ctx context.Context, rt runtime.Runtime, workloadID string, status *models.WorkloadStatus) {
	provider, ok := rt.(runtime.UsageStatsProvider)
	if !ok || status == nil {
		return
	}

	usage, err := provider.UsageStats(ctx, workloadID)
	if err != nil {
		m.logger.Debugf("No usage stats available for %s: %v", workloadID, err)
		return
	}
	if usage == nil {
		return
	}

	status.Usage = usage
}

type managedStorageAllocation struct {
	spec       models.ManagedVolumeSpec
	handle     *platform.VolumeHandle
	attachment *platform.VolumeAttachment
}

func (m *Manager) hasManagedStorage(workload *models.Workload) bool {
	if workload == nil {
		return false
	}
	if workload.Type == models.WorkloadTypeVM {
		vmSpec, err := parseVMSpec(workload.Spec)
		return err == nil && len(vmSpec.ManagedVolumes) > 0
	}
	containerSpec, err := parseContainerSpec(workload.Spec)
	return err == nil && len(containerSpec.ManagedVolumes) > 0
}

func parseContainerSpec(specMap map[string]interface{}) (*models.ContainerSpec, error) {
	payload, err := json.Marshal(specMap)
	if err != nil {
		return nil, err
	}
	var spec models.ContainerSpec
	if err := json.Unmarshal(payload, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

func parseVMSpec(specMap map[string]interface{}) (*models.VMSpec, error) {
	payload, err := json.Marshal(specMap)
	if err != nil {
		return nil, err
	}
	var spec models.VMSpec
	if err := json.Unmarshal(payload, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

func encodeSpecToMap(spec interface{}) (map[string]interface{}, error) {
	payload, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	out := make(map[string]interface{})
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeManagedVolumeSpec(spec models.ManagedVolumeSpec, idx int) (models.ManagedVolumeSpec, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return spec, fmt.Errorf("managed volume at index %d is missing name", idx)
	}
	if strings.TrimSpace(spec.Driver) == "" {
		spec.Driver = "local"
	}
	if strings.TrimSpace(spec.MountPath) == "" {
		return spec, fmt.Errorf("managed volume %q is missing mount_path", spec.Name)
	}
	if strings.TrimSpace(spec.RetainPolicy) == "" {
		spec.RetainPolicy = "Delete"
	}
	return spec, nil
}

func nextVMManagedDiskDevice(existing []models.DiskConfig) string {
	used := make(map[string]struct{}, len(existing))
	for _, disk := range existing {
		device := strings.ToLower(strings.TrimSpace(disk.Device))
		if device == "" {
			continue
		}
		used[device] = struct{}{}
	}
	for letter := 'b'; letter <= 'z'; letter++ {
		candidate := fmt.Sprintf("vd%c", letter)
		if _, ok := used[candidate]; !ok {
			return candidate
		}
	}
	return fmt.Sprintf("vdx%d", time.Now().UnixNano()%10000)
}

func volumeHandleIDFromAttachment(attachment *platform.VolumeAttachment) string {
	if attachment == nil {
		return ""
	}
	if strings.TrimSpace(attachment.VolumeID) != "" {
		return attachment.VolumeID
	}
	parts := strings.Split(strings.TrimSpace(attachment.ID), ":")
	if len(parts) >= 2 {
		return parts[0] + ":" + parts[1]
	}
	return ""
}

func (m *Manager) saveManagedVolumeHandle(handle *platform.VolumeHandle) {
	if m.volumeState == nil || handle == nil {
		return
	}
	if err := m.volumeState.SaveVolume(handle); err != nil {
		m.logger.Warnf("Failed to save managed volume handle %s: %v", handle.ID, err)
	}
}

func (m *Manager) saveManagedVolumeAttachment(attachment *platform.VolumeAttachment) {
	if m.volumeState == nil || attachment == nil {
		return
	}
	if err := m.volumeState.SaveAttachment(attachment); err != nil {
		m.logger.Warnf("Failed to save managed volume attachment %s: %v", attachment.ID, err)
	}
}

func (m *Manager) prepareManagedStorageForContainer(ctx context.Context, workload *models.Workload) ([]managedStorageAllocation, error) {
	containerSpec, err := parseContainerSpec(workload.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to parse container spec for managed volumes: %w", err)
	}
	if len(containerSpec.ManagedVolumes) == 0 {
		return nil, nil
	}
	if m.volumeMgr == nil {
		return nil, fmt.Errorf("managed volume requested but volume manager is not configured")
	}

	allocations := make([]managedStorageAllocation, 0, len(containerSpec.ManagedVolumes))
	for idx, rawSpec := range containerSpec.ManagedVolumes {
		spec, err := normalizeManagedVolumeSpec(rawSpec, idx)
		if err != nil {
			return nil, err
		}
		platformSpec := platform.VolumeSpecFromModel(spec)
		handle, err := m.volumeMgr.Provision(ctx, platformSpec)
		if err != nil {
			return nil, fmt.Errorf("provision volume %q (%s): %w", spec.Name, spec.Driver, err)
		}
		if handle.Metadata == nil {
			handle.Metadata = map[string]string{}
		}
		handle.Metadata["retain_policy"] = strings.TrimSpace(spec.RetainPolicy)
		handle.Metadata["mount_path"] = strings.TrimSpace(spec.MountPath)
		m.saveManagedVolumeHandle(handle)

		attachment, err := m.volumeMgr.Attach(ctx, platformSpec, handle, workload.ID)
		if err != nil {
			return nil, fmt.Errorf("attach volume %q (%s): %w", spec.Name, spec.Driver, err)
		}
		if attachment.Metadata == nil {
			attachment.Metadata = map[string]string{}
		}
		attachment.Metadata["driver"] = strings.TrimSpace(spec.Driver)
		attachment.Metadata["retain_policy"] = strings.TrimSpace(spec.RetainPolicy)
		m.saveManagedVolumeAttachment(attachment)

		volumeMount := models.VolumeMount{
			HostPath:      attachment.StagePath,
			ContainerPath: spec.MountPath,
			ReadOnly:      spec.ReadOnly,
		}
		containerSpec.Volumes = append(containerSpec.Volumes, volumeMount)
		allocations = append(allocations, managedStorageAllocation{
			spec:       spec,
			handle:     handle,
			attachment: attachment,
		})
	}

	updatedMap, err := encodeSpecToMap(containerSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to encode updated container spec: %w", err)
	}
	workload.Spec = updatedMap
	return allocations, nil
}

func (m *Manager) prepareManagedStorageForVM(ctx context.Context, workload *models.Workload) ([]managedStorageAllocation, error) {
	vmSpec, err := parseVMSpec(workload.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to parse vm spec for managed volumes: %w", err)
	}
	if len(vmSpec.ManagedVolumes) == 0 {
		return nil, nil
	}
	if m.volumeMgr == nil {
		return nil, fmt.Errorf("managed volume requested but volume manager is not configured")
	}

	allocations := make([]managedStorageAllocation, 0, len(vmSpec.ManagedVolumes))
	for idx, rawSpec := range vmSpec.ManagedVolumes {
		spec, err := normalizeManagedVolumeSpec(rawSpec, idx)
		if err != nil {
			return nil, err
		}
		platformSpec := platform.VolumeSpecFromModel(spec)
		handle, err := m.volumeMgr.Provision(ctx, platformSpec)
		if err != nil {
			return nil, fmt.Errorf("provision volume %q (%s): %w", spec.Name, spec.Driver, err)
		}
		if handle.Metadata == nil {
			handle.Metadata = map[string]string{}
		}
		handle.Metadata["retain_policy"] = strings.TrimSpace(spec.RetainPolicy)
		handle.Metadata["mount_path"] = strings.TrimSpace(spec.MountPath)
		m.saveManagedVolumeHandle(handle)

		attachment, err := m.volumeMgr.Attach(ctx, platformSpec, handle, workload.ID)
		if err != nil {
			return nil, fmt.Errorf("attach volume %q (%s): %w", spec.Name, spec.Driver, err)
		}
		if attachment.Metadata == nil {
			attachment.Metadata = map[string]string{}
		}
		attachment.Metadata["driver"] = strings.TrimSpace(spec.Driver)
		attachment.Metadata["retain_policy"] = strings.TrimSpace(spec.RetainPolicy)
		m.saveManagedVolumeAttachment(attachment)

		diskPath := strings.TrimSpace(handle.Device)
		if diskPath == "" {
			diskPath = strings.TrimSpace(attachment.StagePath)
		}
		if diskPath == "" {
			return nil, fmt.Errorf("managed volume %q did not provide attachable disk path", spec.Name)
		}
		vmSpec.Disks = append(vmSpec.Disks, models.DiskConfig{
			Path:   diskPath,
			Device: nextVMManagedDiskDevice(vmSpec.Disks),
			Format: "raw",
			SizeGB: spec.SizeGB,
			Type:   models.DiskTypeDisk,
			Boot:   false,
		})
		allocations = append(allocations, managedStorageAllocation{
			spec:       spec,
			handle:     handle,
			attachment: attachment,
		})
	}

	updatedMap, err := encodeSpecToMap(vmSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to encode updated vm spec: %w", err)
	}
	workload.Spec = updatedMap
	return allocations, nil
}

func (m *Manager) cleanupManagedStorageAllocations(ctx context.Context, allocations []managedStorageAllocation, deleteHandles bool) {
	if len(allocations) == 0 || m.volumeMgr == nil {
		return
	}
	for i := len(allocations) - 1; i >= 0; i-- {
		allocation := allocations[i]
		if allocation.attachment != nil {
			if err := m.volumeMgr.Detach(ctx, allocation.attachment); err != nil {
				m.logger.Warnf("Failed to rollback attachment %s: %v", allocation.attachment.ID, err)
			}
			if m.volumeState != nil {
				_ = m.volumeState.DeleteAttachment(allocation.attachment.ID)
			}
		}
		if deleteHandles && allocation.handle != nil {
			retainPolicy := strings.ToLower(strings.TrimSpace(allocation.spec.RetainPolicy))
			if retainPolicy == "retain" {
				continue
			}
			if err := m.volumeMgr.Delete(ctx, allocation.handle); err != nil {
				m.logger.Warnf("Failed to rollback volume %s: %v", allocation.handle.ID, err)
			}
			if m.volumeState != nil {
				_ = m.volumeState.DeleteVolume(allocation.handle.ID)
			}
		}
	}
}

func (m *Manager) prepareManagedStorage(ctx context.Context, workload *models.Workload) ([]managedStorageAllocation, error) {
	if workload == nil {
		return nil, nil
	}
	switch workload.Type {
	case models.WorkloadTypeContainer:
		return m.prepareManagedStorageForContainer(ctx, workload)
	case models.WorkloadTypeVM:
		return m.prepareManagedStorageForVM(ctx, workload)
	default:
		return nil, nil
	}
}

func (m *Manager) releaseManagedStorageForWorkload(ctx context.Context, workloadID string) {
	if m.volumeMgr == nil || m.volumeState == nil {
		return
	}
	attachments, err := m.volumeState.ListAttachments(workloadID)
	if err != nil {
		m.logger.Warnf("Failed to list managed volume attachments for workload %s: %v", workloadID, err)
		return
	}
	if len(attachments) == 0 {
		return
	}
	sort.SliceStable(attachments, func(i, j int) bool {
		return attachments[i].CreatedAt.After(attachments[j].CreatedAt)
	})
	visitedVolumes := make(map[string]struct{})
	for _, attachment := range attachments {
		if attachment == nil {
			continue
		}
		if err := m.volumeMgr.Detach(ctx, attachment); err != nil {
			m.logger.Warnf("Failed to detach managed volume attachment %s: %v", attachment.ID, err)
		}
		_ = m.volumeState.DeleteAttachment(attachment.ID)

		volumeID := volumeHandleIDFromAttachment(attachment)
		if volumeID == "" {
			continue
		}
		if _, exists := visitedVolumes[volumeID]; exists {
			continue
		}
		visitedVolumes[volumeID] = struct{}{}

		handle, err := m.volumeState.GetVolume(volumeID)
		if err != nil || handle == nil {
			continue
		}
		retainPolicy := ""
		if handle.Metadata != nil {
			retainPolicy = strings.ToLower(strings.TrimSpace(handle.Metadata["retain_policy"]))
		}
		if retainPolicy == "retain" {
			continue
		}
		if err := m.volumeMgr.Delete(ctx, handle); err != nil {
			m.logger.Warnf("Failed to delete managed volume %s: %v", handle.ID, err)
			continue
		}
		_ = m.volumeState.DeleteVolume(handle.ID)
	}
}

// ApplyWorkload applies a workload with revision-based idempotency
func (m *Manager) ApplyWorkload(ctx context.Context, workload *models.Workload) (*models.WorkloadStatus, bool, error) {
	unlock := m.lockWorkloadOp(workload.ID)
	defer unlock()

	m.logger.Infof("Applying workload: %s (type: %s, revision: %s)", workload.ID, workload.Type, workload.RevisionID)

	// Check if workload already exists with same revision (idempotency)
	existing, err := m.store.GetWorkload(workload.ID)
	if err == nil && existing.RevisionID == workload.RevisionID {
		status, statusErr := m.store.GetStatus(workload.ID)
		if statusErr != nil || status == nil {
			if existing.DesiredState == workload.DesiredState {
				m.logger.Warnf(
					"Workload %s revision matches but status unavailable, preserving idempotent skip: %v",
					workload.ID,
					statusErr,
				)
				status = &models.WorkloadStatus{
					ID:           workload.ID,
					Type:         workload.Type,
					RevisionID:   workload.RevisionID,
					DesiredState: workload.DesiredState,
					ActualState:  models.ActualStateUnknown,
					Message:      "status not found",
				}
				return status, true, nil // skipped=true
			}
			status = &models.WorkloadStatus{
				ID:           workload.ID,
				Type:         workload.Type,
				RevisionID:   workload.RevisionID,
				DesiredState: existing.DesiredState,
				ActualState:  models.ActualStateUnknown,
				Message:      "status not found",
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
			}
		}

		missingFromRuntime := status.ActualState == models.ActualStateUnknown && isRuntimeMissing(status.Message)
		desiredChanged := existing.DesiredState != workload.DesiredState
		desiredNeedsAction := desiredChanged ||
			(workload.DesiredState == models.DesiredStateRunning && status.ActualState != models.ActualStateRunning && !missingFromRuntime) ||
			(workload.DesiredState == models.DesiredStateStopped && (status.ActualState == models.ActualStateRunning || status.ActualState == models.ActualStatePending))

		if status.ActualState != models.ActualStateFailed && !missingFromRuntime {
			if desiredNeedsAction {
				existing.DesiredState = workload.DesiredState
				existing.UpdatedAt = time.Now()
				if err := m.store.SaveWorkload(existing); err != nil {
					return nil, false, fmt.Errorf("failed to persist desired state transition: %w", err)
				}
				updatedStatus, err := m.applyDesiredStateTransition(ctx, existing, status)
				if err != nil {
					return nil, false, err
				}
				return updatedStatus, false, nil
			}

			m.logger.Infof("Workload %s already at revision %s, skipping", workload.ID, workload.RevisionID)
			return status, true, nil // skipped=true
		}

		m.logger.Infof(
			"Workload %s at revision %s has non-healthy state (%s), retrying apply",
			workload.ID,
			workload.RevisionID,
			status.ActualState,
		)
	}

	// Get runtime for this workload type
	rt, err := m.runtimeMgr.GetRuntime(workload.Type)
	if err != nil {
		return nil, false, fmt.Errorf("runtime not available: %w", err)
	}

	// Check resource availability before persisting workload state or creating runtime resources
	if m.resourceMonitor != nil {
		available, issues, err := m.resourceMonitor.IsResourceAvailable()
		if err != nil {
			return nil, false, fmt.Errorf("failed to validate resource availability: %w", err)
		}

		if !available {
			if m.metrics != nil {
				m.metrics.RecordWorkloadFailed()
				m.metrics.RecordError(string(errors.ErrCodeResourceQuotaExceeded))
			}

			if err := m.store.SaveWorkload(workload); err != nil {
				m.logger.Warnf("Failed to persist resource-constrained workload %s: %v", workload.ID, err)
			}

			m.persistFailedStatus(
				workload,
				fmt.Sprintf("admission rejected due to resource constraints: %v", issues),
				nil,
			)

			workloadErr := errors.NewWorkloadError(
				errors.ErrCodeResourceQuotaExceeded,
				"Insufficient system resources to admit workload",
				workload.ID,
				string(workload.Type),
				nil,
			)
			workloadErr.WithDetail("issues", issues)
			workloadErr.WithDetail("revision", workload.RevisionID)

			return nil, false, workloadErr
		}
	}

	// If revision changed, we need to recreate
	if existing != nil && existing.RevisionID != workload.RevisionID {
		m.logger.Infof("Workload %s revision changed from %s to %s, recreating",
			workload.ID, existing.RevisionID, workload.RevisionID)

		// Delete old workload
		if err := rt.Delete(ctx, workload.ID); err != nil {
			m.logger.Warnf("Failed to delete old workload: %v", err)
		}
	}

	// Save workload to state store
	if err := m.store.SaveWorkload(workload); err != nil {
		return nil, false, fmt.Errorf("failed to save workload: %w", err)
	}

	// Save initial pending status to indicate creation is in progress
	initialStatus := &models.WorkloadStatus{
		ID:           workload.ID,
		Type:         workload.Type,
		RevisionID:   workload.RevisionID,
		DesiredState: workload.DesiredState,
		ActualState:  models.ActualStatePending,
		Message:      "creating workload",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := m.store.SaveStatus(initialStatus); err != nil {
		m.logger.Warnf("Failed to save initial status: %v", err)
	}

	// Provision/attach managed volumes before runtime create.
	managedAllocations, err := m.prepareManagedStorage(ctx, workload)
	if err != nil {
		reasonCode := "STORAGE_PROVISION_FAILED"
		if strings.Contains(strings.ToLower(err.Error()), "attach") {
			reasonCode = "STORAGE_ATTACH_FAILED"
		}
		m.persistManagedStorageFailureStatus(workload, reasonCode, fmt.Sprintf("managed storage setup failed: %v", err), err)
		workloadErr := errors.NewWorkloadError(
			errors.ErrCodeVolumeMountFailed,
			"Failed to setup managed storage for workload",
			workload.ID,
			string(workload.Type),
			err,
		)
		workloadErr.WithDetail("failure_reason", reasonCode)
		return nil, false, workloadErr
	}
	if len(managedAllocations) > 0 {
		if err := m.store.SaveWorkload(workload); err != nil {
			m.cleanupManagedStorageAllocations(ctx, managedAllocations, true)
			return nil, false, fmt.Errorf("failed to persist workload with managed volume attachments: %w", err)
		}
	}

	// Create the workload in the runtime
	startTime := time.Now()
	if err := rt.Create(ctx, workload); err != nil {
		m.logger.Errorf("Failed to create workload %s: %v", workload.ID, err)
		m.cleanupManagedStorageAllocations(ctx, managedAllocations, true)

		// Record failed workload metric
		if m.metrics != nil {
			m.metrics.RecordWorkloadFailed()
		}

		m.persistFailedStatus(workload, fmt.Sprintf("create failed: %v", err), err)
		m.annotateFailureReason(workload.ID, failureReasonCodeFromRuntimeError(err), err.Error())

		// Create detailed error
		workloadErr := errors.NewWorkloadError(
			errors.ErrCodeCreateFailed,
			"Failed to create workload in runtime",
			workload.ID,
			string(workload.Type),
			err,
		)
		workloadErr.WithDetail("type", workload.Type)
		workloadErr.WithDetail("revision", workload.RevisionID)

		return nil, false, workloadErr
	}

	// Record apply duration
	if m.metrics != nil {
		m.metrics.RecordApplyWorkloadDuration(time.Since(startTime))
	}

	// Start if desired state is running
	if workload.DesiredState == models.DesiredStateRunning {
		if err := rt.Start(ctx, workload.ID); err != nil {
			m.logger.Errorf("Failed to start workload %s: %v, leaving desired state for reconciliation", workload.ID, err)
			m.cleanupManagedStorageAllocations(ctx, managedAllocations, true)

			m.persistFailedStatus(workload, fmt.Sprintf("start failed: %v", err), err)
			m.annotateFailureReason(workload.ID, failureReasonCodeFromRuntimeError(err), err.Error())

			workloadErr := errors.NewWorkloadError(
				errors.ErrCodeStartFailed,
				"Failed to start workload",
				workload.ID,
				string(workload.Type),
				err,
			)
			workloadErr.WithDetail("type", workload.Type)
			workloadErr.WithDetail("revision", workload.RevisionID)

			return nil, false, workloadErr
		}
	}

	// Get final status
	actualState, message, err := rt.Status(ctx, workload.ID)
	if err != nil {
		m.logger.Warnf("Failed to get status for %s: %v", workload.ID, err)
		actualState = models.ActualStateUnknown
		message = fmt.Sprintf("status check failed: %v", err)
	}

	status := &models.WorkloadStatus{
		ID:           workload.ID,
		Type:         workload.Type,
		RevisionID:   workload.RevisionID,
		DesiredState: workload.DesiredState,
		ActualState:  actualState,
		Message:      message,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	m.enrichStatusWithRuntimeMetadata(ctx, rt, workload.ID, status)

	if err := m.store.SaveStatus(status); err != nil {
		return status, false, fmt.Errorf("failed to save status: %w", err)
	}

	m.resetRetryTracker(workload.ID)

	// Record successful workload creation
	if m.metrics != nil {
		m.metrics.RecordWorkloadCreated()
	}

	m.logger.Infof("Successfully applied workload %s", workload.ID)
	return status, false, nil
}

func (m *Manager) applyDesiredStateTransition(ctx context.Context, workload *models.Workload, status *models.WorkloadStatus) (*models.WorkloadStatus, error) {
	rt, err := m.runtimeMgr.GetRuntime(workload.Type)
	if err != nil {
		return nil, fmt.Errorf("runtime not available: %w", err)
	}

	now := time.Now()
	if status == nil {
		status = &models.WorkloadStatus{
			ID:        workload.ID,
			Type:      workload.Type,
			CreatedAt: now,
		}
	}
	status.Type = workload.Type
	status.RevisionID = workload.RevisionID
	status.DesiredState = workload.DesiredState

	actualState, message, err := rt.Status(ctx, workload.ID)
	if err != nil {
		status.ActualState = models.ActualStateFailed
		status.Message = fmt.Sprintf("runtime status failed: %v", err)
		status.UpdatedAt = now
		ensureMetadata(status)
		status.Metadata["last_error"] = status.Message
		_ = m.store.SaveStatus(status)
		return nil, fmt.Errorf("runtime status check failed: %w", err)
	}
	actualState, message = normalizeStateForDesired(workload.DesiredState, actualState, message)

	switch workload.DesiredState {
	case models.DesiredStateRunning:
		if actualState != models.ActualStateRunning {
			if err := rt.Start(ctx, workload.ID); err != nil {
				m.persistFailedStatus(workload, fmt.Sprintf("start failed: %v", err), err)
				return nil, errors.NewWorkloadError(
					errors.ErrCodeStartFailed,
					"Failed to start workload",
					workload.ID,
					string(workload.Type),
					err,
				)
			}
		}
	case models.DesiredStateStopped:
		if actualState == models.ActualStateRunning || actualState == models.ActualStatePending {
			if err := rt.Stop(ctx, workload.ID); err != nil {
				status.ActualState = models.ActualStateFailed
				status.Message = fmt.Sprintf("stop failed: %v", err)
				status.UpdatedAt = time.Now()
				ensureMetadata(status)
				status.Metadata["last_error"] = err.Error()
				_ = m.store.SaveStatus(status)
				return nil, fmt.Errorf("failed to stop workload: %w", err)
			}
		}
	}

	actualState, message, err = rt.Status(ctx, workload.ID)
	if err != nil {
		actualState = models.ActualStateUnknown
		message = fmt.Sprintf("status check failed: %v", err)
	}
	actualState, message = normalizeStateForDesired(workload.DesiredState, actualState, message)
	status.ActualState = actualState
	status.Message = message
	status.UpdatedAt = time.Now()
	if workload.DesiredState == models.DesiredStateStopped {
		clearRetryStateMetadata(status)
	}
	m.enrichStatusWithRuntimeMetadata(ctx, rt, workload.ID, status)
	if err := m.store.SaveStatus(status); err != nil {
		return nil, fmt.Errorf("failed to save status: %w", err)
	}
	m.resetRetryTracker(workload.ID)
	return status, nil
}

// DeleteWorkload removes a workload
func (m *Manager) DeleteWorkload(ctx context.Context, id string) error {
	unlock := m.lockWorkloadOp(id)
	defer unlock()

	m.logger.Infof("Deleting workload: %s", id)

	startTime := time.Now()

	// Get workload from store
	workload, err := m.store.GetWorkload(id)
	if err != nil {
		return fmt.Errorf("workload not found: %w", err)
	}

	// Get runtime
	rt, err := m.runtimeMgr.GetRuntime(workload.Type)
	if err != nil {
		return fmt.Errorf("runtime not available: %w", err)
	}

	// Delete from runtime
	if err := rt.Delete(ctx, id); err != nil {
		m.logger.Warnf("Failed to delete workload from runtime: %v", err)
	}

	// Detach/delete managed storage allocations for this workload.
	m.releaseManagedStorageForWorkload(ctx, id)

	// Remove from state store
	if err := m.store.DeleteWorkload(id); err != nil {
		return fmt.Errorf("failed to delete from state: %w", err)
	}

	// Record deletion metrics
	if m.metrics != nil {
		m.metrics.RecordWorkloadDeleted()
		m.metrics.RecordDeleteWorkloadDuration(time.Since(startTime))
	}

	m.logger.Infof("Successfully deleted workload %s", id)
	return nil
}

// GetStatus returns the current status of a workload
func (m *Manager) GetStatus(ctx context.Context, id string) (*models.WorkloadStatus, error) {
	// Get from store first
	status, err := m.store.GetStatus(id)
	if err != nil {
		return nil, fmt.Errorf("status not found: %w", err)
	}

	// Optionally refresh from runtime
	workload, err := m.store.GetWorkload(id)
	if err == nil {
		rt, err := m.runtimeMgr.GetRuntime(workload.Type)
		if err == nil {
			actualState, message, err := rt.Status(ctx, id)
			if err == nil {
				actualState, message = normalizeStateForDesired(workload.DesiredState, actualState, message)
				status.ActualState = actualState
				status.Message = message
				status.UpdatedAt = time.Now()
				if workload.DesiredState == models.DesiredStateStopped {
					m.resetRetryTracker(id)
					clearRetryStateMetadata(status)
				}
				m.enrichStatusWithRuntimeMetadata(ctx, rt, id, status)
				m.enrichStatusWithUsage(ctx, rt, id, status)
				m.store.SaveStatus(status)
			}
		}
	}

	return status, nil
}

// ListWorkloads returns all workloads
func (m *Manager) ListWorkloads(ctx context.Context, workloadType *models.WorkloadType) ([]*models.WorkloadStatus, error) {
	statuses, err := m.store.ListStatuses()
	if err != nil {
		return nil, fmt.Errorf("failed to list statuses: %w", err)
	}

	if ctx == nil {
		ctx = context.Background()
	}

	refreshed := make([]*models.WorkloadStatus, 0, len(statuses))
	for _, status := range statuses {
		if status == nil {
			continue
		}
		if workloadType != nil && status.Type != *workloadType {
			continue
		}

		current, refreshErr := m.GetStatus(ctx, status.ID)
		if refreshErr != nil {
			m.logger.Warnf("Failed to refresh status for %s during list: %v", status.ID, refreshErr)
			refreshed = append(refreshed, status)
			continue
		}
		refreshed = append(refreshed, current)
	}

	return refreshed, nil
}

// ReconcileWorkload ensures workload state matches desired state
func (m *Manager) ReconcileWorkload(ctx context.Context, id string) error {
	unlock := m.lockWorkloadOp(id)
	defer unlock()

	workload, err := m.store.GetWorkload(id)
	if err != nil {
		return fmt.Errorf("workload not found: %w", err)
	}

	status, err := m.store.GetStatus(id)
	if err != nil {
		return fmt.Errorf("status not found: %w", err)
	}

	rt, err := m.runtimeMgr.GetRuntime(workload.Type)
	if err != nil {
		return fmt.Errorf("runtime not available: %w", err)
	}

	// Get current actual state
	actualState, message, err := rt.Status(ctx, id)
	if err != nil {
		m.logger.Warnf("Failed to get status for %s: %v", id, err)
		status.ActualState = models.ActualStateFailed
		status.Message = fmt.Sprintf("runtime status failed: %v", err)
		status.UpdatedAt = time.Now()
		return m.store.SaveStatus(status)
	}
	actualState, message = normalizeStateForDesired(workload.DesiredState, actualState, message)

	// Update status
	status.ActualState = actualState
	status.Message = message
	status.UpdatedAt = time.Now()
	if workload.DesiredState == models.DesiredStateStopped {
		m.resetRetryTracker(id)
		clearRetryStateMetadata(status)
	}

	// Reconcile state
	needsAction := false

	// Don't reconcile if workload is in a transient state (unknown or pending indicates creation/deletion in progress)
	missingFromRuntime := actualState == models.ActualStateUnknown && isRuntimeMissing(message)
	if actualState == models.ActualStatePending {
		if workload.DesiredState == models.DesiredStateRunning {
			if handled, err := m.handlePendingRecovery(ctx, workload, status, rt); handled {
				return err
			}
		}
		m.logger.Debugf("Skipping reconciliation for %s: workload is in transient state (%s)", id, actualState)
		needsAction = false
	} else if actualState == models.ActualStateUnknown && !missingFromRuntime {
		m.logger.Debugf("Skipping reconciliation for %s: workload state unknown (%s)", id, message)
		needsAction = false
	} else if workload.DesiredState == models.DesiredStateRunning && (actualState == models.ActualStateFailed || missingFromRuntime) {
		if isRetryTerminal(status) {
			status.ActualState = models.ActualStateFailed
			status.UpdatedAt = time.Now()
			reason := strings.TrimSpace(status.Metadata[retryTerminalReasonMetadataKey])
			if reason != "" {
				status.Message = fmt.Sprintf("retry halted: %s", reason)
			}
			m.logger.Infof("Skipping retry for %s: terminal retry state set (%s)", id, reason)
			return m.store.SaveStatus(status)
		}

		if missingFromRuntime {
			m.logger.Infof("Reconciling %s: workload missing from runtime, attempting recreate...", id)
			status.ActualState = models.ActualStateFailed
			if status.Message == "" {
				status.Message = message
			}
			ensureMetadata(status)
			status.Metadata["failure_reason"] = "RUNTIME_RESOURCE_MISSING"
			status.Metadata["last_error"] = status.Message
		} else {
			tracker := m.getRetryTracker(id)
			if tracker.GetAttemptCount() > 0 && !tracker.CanRetryNow() {
				ensureMetadata(status)
				status.Metadata["retry_attempts"] = fmt.Sprintf("%d", tracker.GetAttemptCount())
				status.Metadata["next_retry_time"] = tracker.GetNextRetryTime().Format(time.RFC3339)
				m.logger.Debugf("Skipping retry for %s until %s", id, tracker.GetNextRetryTime().Format(time.RFC3339))
				return m.store.SaveStatus(status)
			}

			reason := retry.ClassifyError(stdErrors.New(status.Message))
			retryResult, recordErr := tracker.RecordFailure(reason, status.Message)
			if recordErr != nil {
				m.logger.Warnf("Failed to record retry metadata for %s: %v", id, recordErr)
			}

			ensureMetadata(status)
			status.Metadata["retry_attempts"] = fmt.Sprintf("%d", tracker.GetAttemptCount())
			status.Metadata["failure_reason"] = string(reason)
			status.Metadata["last_error"] = status.Message

			if retryResult != nil && retryResult.Retryable {
				status.Metadata["next_retry_time"] = retryResult.NextRetryTime.Format(time.RFC3339)
				if !tracker.CanRetryNow() {
					m.logger.Debugf("Skipping retry for %s until %s", id, retryResult.NextRetryTime.Format(time.RFC3339))
					return m.store.SaveStatus(status)
				}
			} else {
				markRetryTerminal(status, tracker, fmt.Sprintf("non-retryable failure (%s)", reason))
				status.ActualState = models.ActualStateFailed
				status.UpdatedAt = time.Now()
				if strings.TrimSpace(status.Message) == "" {
					status.Message = fmt.Sprintf("retry halted due to non-retryable failure: %s", reason)
				} else {
					status.Message = fmt.Sprintf("retry halted due to non-retryable failure (%s): %s", reason, status.Message)
				}
				m.logger.Infof("Not retrying failed workload %s (reason: %s)", id, reason)
				return m.store.SaveStatus(status)
			}
		}

		// Retry failed workloads when policy allows
		m.logger.Infof("Reconciling %s: status=failed, attempting retry recreate...", id)
		if m.resourceMonitor != nil {
			available, issues, checkErr := m.resourceMonitor.IsResourceAvailable()
			if checkErr != nil {
				status.ActualState = models.ActualStateFailed
				status.Message = fmt.Sprintf("recreate admission check failed: %v", checkErr)
				status.UpdatedAt = time.Now()
				ensureMetadata(status)
				status.Metadata["last_error"] = status.Message
				return m.store.SaveStatus(status)
			}
			if !available {
				status.ActualState = models.ActualStateFailed
				status.Message = fmt.Sprintf("recreate deferred due to resource constraints: %v", issues)
				status.UpdatedAt = time.Now()
				ensureMetadata(status)
				status.Metadata["failure_reason"] = string(retry.FailureReasonResourceQuotaExceeded)
				status.Metadata["last_error"] = status.Message
				status.Metadata["resource_issues"] = strings.Join(issues, "; ")
				m.logger.Infof("Reconciling %s: retry blocked by resource constraints: %v", id, issues)
				return m.store.SaveStatus(status)
			}
		}

		// Delete the failed workload first
		if err := rt.Delete(ctx, id); err != nil {
			m.logger.Warnf("Failed to delete failed workload %s: %v", id, err)
		}

		// Try to recreate
		if err := rt.Create(ctx, workload); err != nil {
			m.logger.Errorf("Failed to recreate %s during reconciliation: %v", id, err)
			status.ActualState = models.ActualStateFailed
			status.Message = fmt.Sprintf("recreate failed: %v", err)
		} else {
			m.logger.Infof("Successfully recreated %s during reconciliation", id)
			// Start if desired state is running
			if workload.DesiredState == models.DesiredStateRunning {
				if err := rt.Start(ctx, id); err != nil {
					m.logger.Errorf("Failed to start recreated %s: %v", id, err)
					status.ActualState = models.ActualStateFailed
					status.Message = fmt.Sprintf("start after recreate failed: %v", err)
				} else {
					status.ActualState = models.ActualStateRunning
					status.Message = "running"
					m.resetRetryTracker(id)
					delete(status.Metadata, "retry_attempts")
					delete(status.Metadata, "next_retry_time")
					delete(status.Metadata, "failure_reason")
					delete(status.Metadata, "last_error")
				}
			} else {
				status.ActualState = models.ActualStateStopped
				status.Message = "recreated and left stopped"
				m.resetRetryTracker(id)
			}
		}
		needsAction = true
	} else if workload.DesiredState == models.DesiredStateRunning && actualState != models.ActualStateRunning {
		tracker := m.getRetryTracker(id)
		if isRetryTerminal(status) {
			status.ActualState = models.ActualStateFailed
			status.UpdatedAt = time.Now()
			reason := strings.TrimSpace(status.Metadata[retryTerminalReasonMetadataKey])
			if reason != "" {
				status.Message = fmt.Sprintf("retry halted: %s", reason)
			}
			m.logger.Infof("Skipping start reconcile for %s: terminal retry state set (%s)", id, reason)
			return m.store.SaveStatus(status)
		}
		if tracker.GetAttemptCount() > 0 && !tracker.CanRetryNow() {
			ensureMetadata(status)
			status.Metadata["retry_attempts"] = fmt.Sprintf("%d", tracker.GetAttemptCount())
			status.Metadata["next_retry_time"] = tracker.GetNextRetryTime().Format(time.RFC3339)
			status.Metadata["failure_reason"] = string(retry.ClassifyError(stdErrors.New(status.Message)))
			status.UpdatedAt = time.Now()
			status.Message = fmt.Sprintf("reconcile start deferred until %s", tracker.GetNextRetryTime().Format(time.RFC3339))
			m.logger.Debugf("Deferring start reconcile for %s until %s", id, tracker.GetNextRetryTime().Format(time.RFC3339))
			return m.store.SaveStatus(status)
		}

		m.logger.Infof("Reconciling %s: desired=running, actual=%s, starting...", id, actualState)
		if err := rt.Start(ctx, id); err != nil {
			m.logger.Errorf("Failed to start %s during reconciliation: %v", id, err)
			reason := retry.ClassifyError(err)
			retryResult, recordErr := tracker.RecordFailure(reason, err.Error())
			if recordErr != nil {
				m.logger.Warnf("Failed to record retry metadata for %s: %v", id, recordErr)
			}

			status.ActualState = models.ActualStateFailed
			status.Message = fmt.Sprintf("reconcile start failed: %v", err)
			status.UpdatedAt = time.Now()
			ensureMetadata(status)
			status.Metadata["failure_reason"] = string(reason)
			status.Metadata["last_error"] = status.Message
			status.Metadata["last_runtime_error"] = err.Error()
			status.Metadata["retry_attempts"] = fmt.Sprintf("%d", tracker.GetAttemptCount())
			if retryResult != nil && retryResult.Retryable {
				status.Metadata["next_retry_time"] = retryResult.NextRetryTime.Format(time.RFC3339)
				status.Message = fmt.Sprintf("reconcile start failed; retry scheduled at %s: %v", retryResult.NextRetryTime.Format(time.RFC3339), err)
			} else {
				markRetryTerminal(status, tracker, fmt.Sprintf("start failure is non-retryable (%s)", reason))
				status.Message = fmt.Sprintf("reconcile start failed; retries halted (%s): %v", reason, err)
				delete(status.Metadata, "next_retry_time")
			}
			return m.store.SaveStatus(status)
		}
		m.resetRetryTracker(id)
		if status.Metadata != nil {
			delete(status.Metadata, "retry_attempts")
			delete(status.Metadata, "next_retry_time")
			delete(status.Metadata, "failure_reason")
			delete(status.Metadata, "last_error")
			delete(status.Metadata, retryTerminalMetadataKey)
			delete(status.Metadata, retryTerminalReasonMetadataKey)
		}
		needsAction = true
	} else if workload.DesiredState == models.DesiredStateStopped && actualState == models.ActualStateRunning {
		m.logger.Infof("Reconciling %s: desired=stopped, actual=running, stopping...", id)
		if err := rt.Stop(ctx, id); err != nil {
			m.logger.Errorf("Failed to stop %s during reconciliation: %v", id, err)
			status.ActualState = models.ActualStateFailed
			status.Message = fmt.Sprintf("reconcile stop failed: %v", err)
		}
		needsAction = true
	}

	if needsAction {
		// Recheck status after action
		actualState, message, err = rt.Status(ctx, id)
		if err == nil {
			status.ActualState = actualState
			status.Message = message
		}
	}

	// Enrich with runtime metadata when available
	m.enrichStatusWithRuntimeMetadata(ctx, rt, id, status)
	m.enrichStatusWithUsage(ctx, rt, id, status)

	// Save updated status
	return m.store.SaveStatus(status)
}

func (m *Manager) handlePendingRecovery(ctx context.Context, workload *models.Workload, status *models.WorkloadStatus, rt runtime.Runtime) (bool, error) {
	if workload == nil || status == nil {
		return false, nil
	}
	ensureMetadata(status)

	now := time.Now()
	pendingSince := now
	if raw := strings.TrimSpace(status.Metadata[pendingSinceMetadataKey]); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			pendingSince = parsed
		}
	}
	if status.Metadata[pendingSinceMetadataKey] == "" {
		status.Metadata[pendingSinceMetadataKey] = now.UTC().Format(time.RFC3339)
		status.Message = "workload pending; waiting for startup completion"
		status.UpdatedAt = now
		return true, m.store.SaveStatus(status)
	}

	if now.Sub(pendingSince) < defaultPendingRecoveryThreshold {
		return false, nil
	}

	m.logger.Warnf("Workload %s pending for %s, attempting recovery restart", workload.ID, now.Sub(pendingSince).Round(time.Second))
	status.Metadata[pendingRecoveryActionMetadataKey] = "restart"
	status.Metadata[pendingRecoveryReasonMetadataKey] = fmt.Sprintf("pending for more than %s", defaultPendingRecoveryThreshold)

	_ = rt.Stop(ctx, workload.ID)
	startErr := rt.Start(ctx, workload.ID)
	if startErr == nil {
		status.Metadata[pendingSinceMetadataKey] = now.UTC().Format(time.RFC3339)
		status.Message = "recovered from prolonged pending via restart"
		status.UpdatedAt = now
		return true, m.store.SaveStatus(status)
	}

	m.logger.Errorf("Pending recovery start failed for %s: %v", workload.ID, startErr)
	deleteErr := rt.Delete(ctx, workload.ID)
	if deleteErr != nil {
		m.logger.Errorf("Pending recovery delete failed for %s: %v", workload.ID, deleteErr)
	}

	status.ActualState = models.ActualStateFailed
	status.UpdatedAt = now
	status.Metadata[pendingRecoveryDeletedMetadataKey] = "true"
	status.Metadata["last_error"] = fmt.Sprintf("pending recovery restart failed: %v", startErr)
	if deleteErr != nil {
		status.Metadata["last_delete_error"] = deleteErr.Error()
		status.Message = fmt.Sprintf("pending timeout exceeded; restart failed (%v); delete also failed (%v); manual intervention required", startErr, deleteErr)
	} else {
		status.Message = fmt.Sprintf("pending timeout exceeded; restart failed (%v); workload deleted from runtime", startErr)
	}

	// Prevent local reconcile loop from repeatedly attempting restart on this node.
	workload.DesiredState = models.DesiredStateStopped
	if err := m.store.SaveWorkload(workload); err != nil {
		m.logger.Warnf("Failed to persist desired-state stop after pending recovery delete for %s: %v", workload.ID, err)
	}
	return true, m.store.SaveStatus(status)
}

// UpdateWorkloadMetrics updates gauge metrics with current workload counts
func (m *Manager) UpdateWorkloadMetrics() error {
	if m.metrics == nil {
		return nil
	}

	statuses, err := m.store.ListStatuses()
	if err != nil {
		return fmt.Errorf("failed to list statuses: %w", err)
	}

	// Count by state and type
	counts := make(map[string]map[string]int) // [state][type] = count

	for _, status := range statuses {
		state := string(status.ActualState)
		wType := string(status.Type)

		if counts[state] == nil {
			counts[state] = make(map[string]int)
		}
		counts[state][wType]++
	}

	// Record metrics for each state and type combination
	for state, types := range counts {
		for wType, count := range types {
			m.metrics.RecordWorkloadCount(state, wType, count)
		}
	}

	return nil
}
