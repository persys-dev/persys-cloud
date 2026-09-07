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
	nodesPrefix            = "/nodes/"
	workloadsPrefix        = "/workloads/"
	workloadSpecPrefix     = "/workloads-spec/"
	workloadStatusPrefix   = "/workloads-status/"
	volumesPrefix          = "/volumes/"
	attachmentsPrefix      = "/attachments/"
	assignmentsPrefix      = "/assignments/"
	reconciliationPrefix   = "/reconciliation/"
	retriesPrefix          = "/retries/"
	driftsPrefix           = "/drifts/"
	managedStorageStateKey = "managed_storage_state"
)

func workloadKey(workloadID string) string       { return workloadsPrefix + workloadID }
func workloadSpecKey(workloadID string) string   { return workloadSpecPrefix + workloadID }
func workloadStatusKey(workloadID string) string { return workloadStatusPrefix + workloadID }
func managedVolumeKey(volumeID string) string    { return volumesPrefix + volumeID }
func attachmentPrefix() string                   { return attachmentsPrefix }
func assignmentKey(workloadID string) string     { return assignmentsPrefix + workloadID }
func reconciliationKey(workloadID string) string { return reconciliationPrefix + workloadID }
func retryKey(workloadID string) string          { return retriesPrefix + workloadID }
func volumeAttachmentKey(nodeID, workloadID, volumeID string) string {
	return attachmentsPrefix + sanitizeKeySegment(nodeID) + "/" + sanitizeKeySegment(workloadID) + "/" + sanitizeKeySegment(volumeID)
}

func sanitizeKeySegment(in string) string {
	trimmed := strings.TrimSpace(in)
	if trimmed == "" {
		return "_"
	}
	return strings.ReplaceAll(trimmed, "/", "_")
}

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
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	if len(workloadManagedVolumeSpecs(workload)) > 0 {
		workload.Metadata[managedStorageStateKey] = "true"
	}
	// Split workload into spec and status projections
	specPayload, err := json.Marshal(workloadSpecFromWorkload(workload))
	if err != nil {
		return fmt.Errorf("marshal workload spec %s: %w", workload.ID, err)
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
	// NOTE: retry state is embedded in the status projection above
	// (workloadStatus.Retry) and read back from there — the separate
	// /retries/{id} key that used to be written here was never read by
	// anything in this codebase, so the write was pure overhead and has
	// been removed.
	if shouldSyncManagedStorage(workload) {
		if err := s.syncWorkloadManagedStorage(workload); err != nil {
			return fmt.Errorf("sync managed storage state for workload %s: %w", workload.ID, err)
		}
	}
	return nil
}

// maxWorkloadStatusCASRetries bounds how many times updateWorkloadStatusCAS
// will re-read and re-apply a mutation after losing a compare-and-swap race
// before giving up. Mirrors maxCASConflictRetries for nodes; contention on
// a single workload's status key is normally limited to that workload's own
// agent (via heartbeat) and the reconciler, so this should rarely be
// exhausted even under load.
const maxWorkloadStatusCASRetries = 5

// getWorkloadWithStatusRevision reads a workload the same way GetWorkloadByID
// does (merging the spec and status projections into a full models.Workload),
// but also returns the etcd ModRevision of the status key so callers can
// compare-and-swap against it. modRevision is 0 if the status key doesn't
// exist yet (brand new workload) or the record was found via the legacy
// full-object compatibility path, in which case a CAS put behaves like
// "only succeed if the status key still doesn't exist."
func (s *Scheduler) getWorkloadWithStatusRevision(workloadID string) (models.Workload, int64, error) {
	specResp, err := s.RetryableEtcdGet(workloadSpecKey(workloadID))
	if err != nil {
		return models.Workload{}, 0, fmt.Errorf("failed to get workload %s: %v", workloadID, err)
	}

	if specResp == nil || len(specResp.Kvs) == 0 {
		// Legacy compatibility shim, same as GetWorkloadByID.
		resp, legacyErr := s.RetryableEtcdGet("/workloads/" + workloadID)
		if legacyErr != nil || resp == nil || len(resp.Kvs) == 0 {
			return models.Workload{}, 0, fmt.Errorf("workload %s not found", workloadID)
		}
		var legacy models.Workload
		if err := json.Unmarshal(resp.Kvs[0].Value, &legacy); err != nil {
			return models.Workload{}, 0, fmt.Errorf("failed to unmarshal legacy workload %s: %v", workloadID, err)
		}
		return legacy, 0, nil
	}

	var spec workloadSpec
	if err := json.Unmarshal(specResp.Kvs[0].Value, &spec); err != nil {
		return models.Workload{}, 0, fmt.Errorf("failed to unmarshal workload spec %s: %v", workloadID, err)
	}

	statusResp, _ := s.RetryableEtcdGet(workloadStatusKey(workloadID))
	var st workloadStatus
	var modRevision int64
	if statusResp != nil && len(statusResp.Kvs) > 0 {
		_ = json.Unmarshal(statusResp.Kvs[0].Value, &st)
		modRevision = statusResp.Kvs[0].ModRevision
	}

	workload := models.Workload{
		ID: spec.ID, Name: spec.Name, Type: spec.Type, RevisionID: spec.RevisionID, Image: spec.Image, Command: spec.Command,
		CommandList: spec.CommandList, Compose: spec.Compose, ComposeYAML: spec.ComposeYAML, ProjectName: spec.ProjectName,
		GitRepo: spec.GitRepo, GitBranch: spec.GitBranch, GitToken: spec.GitToken, EnvVars: spec.EnvVars, Resources: spec.Resources,
		DesiredState: spec.DesiredState, Labels: spec.Labels, LocalPath: spec.LocalPath, Ports: spec.Ports, Volumes: spec.Volumes,
		Network: spec.Network, RestartPolicy: spec.RestartPolicy, VM: spec.VM,
	}
	if st.ID != "" {
		workload.AssignedNode = st.AssignedNode
		workload.NodeID = st.NodeID
		workload.Status = st.Status
		workload.Logs = st.Logs
		workload.Metadata = st.Metadata
		workload.Retry = st.Retry
		workload.StatusInfo = st.StatusInfo
		workload.Usage = st.Usage
	}
	return workload, modRevision, nil
}

// updateWorkloadStatusCAS reads a workload, lets mutate modify the in-memory
// copy, and persists only the status projection (/workloads-status/{id})
// via an etcd compare-and-swap on that key's ModRevision — re-reading and
// re-applying the mutation (up to maxWorkloadStatusCASRetries times) if
// another writer updated the status key in between, instead of silently
// overwriting whatever that writer just changed. This is the workload-side
// counterpart to updateNodeCAS: the concurrent writers here are typically a
// workload's own agent (via heartbeat) and the reconciler, both of which
// can touch the same workload's status around the same time.
//
// mutate returns false to signal "nothing to change" after inspecting the
// freshest read (mirroring the no-op-skip checks the four callers already
// had) — in that case this returns without writing.
func (s *Scheduler) updateWorkloadStatusCAS(workloadID string, mutate func(*models.Workload) bool) (models.Workload, error) {
	if err := s.requireWritable(); err != nil {
		return models.Workload{}, err
	}
	statusKey := workloadStatusKey(workloadID)
	var lastConflictErr error
	for attempt := 0; attempt < maxWorkloadStatusCASRetries; attempt++ {
		workload, modRevision, err := s.getWorkloadWithStatusRevision(workloadID)
		if err != nil {
			return models.Workload{}, err
		}
		if !mutate(&workload) {
			return workload, nil
		}
		payload, err := json.Marshal(workloadStatusFromWorkload(workload))
		if err != nil {
			return models.Workload{}, fmt.Errorf("marshal workload status %s: %w", workloadID, err)
		}
		ok, err := s.RetryableEtcdCASPut(statusKey, string(payload), modRevision)
		if err != nil {
			return models.Workload{}, err
		}
		if !ok {
			lastConflictErr = fmt.Errorf("workload %s status changed concurrently (attempt %d)", workloadID, attempt+1)
			continue
		}
		metricspkg.IncStateStoreWrite("status")
		s.cacheWorkload(workload)
		return workload, nil
	}
	return models.Workload{}, fmt.Errorf("workload %s: too many concurrent status update conflicts: %w", workloadID, lastConflictErr)
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

// emitEvent records a cluster-wide scheduler event (node lost, workload
// scheduled, drift detected, etc) to the shared Redis Stream
// (schedulerEventsStreamKey, redis_store.go).
//
// This intentionally does NOT write to etcd. Events are high-churn,
// ephemeral, observability-oriented data — exactly the kind of data etcd
// is a poor fit for at scale: every event write would be an etcd PUT (and,
// with a watch-based consumer, a watch-fanout notification) landing on the
// same datastore that holds authoritative cluster state, competing for
// write throughput with heartbeats and CAS-retried reconciliation writes
// at precisely the moments (incidents, node flapping, mass retries) when
// event volume — and etcd load from everything else — is highest. Redis
// Streams give bounded, O(1)-amortized retention (MAXLEN ~) for free,
// versus the scan-and-delete sweep an etcd-backed version would need.
//
// If Redis is unavailable, the event is dropped (logged, not retried,
// no etcd fallback) — acceptable for best-effort observability data that
// nothing else in the scheduler depends on for correctness.
func (s *Scheduler) emitEvent(eventType, workloadID, nodeID, reason string, details map[string]interface{}) {
	if eventType == "" || !isKnownSchedulerEventType(eventType) {
		redisLogger.WithField("event_type", eventType).Warn("unknown scheduler event type emitted")
	}
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
	if _, err := s.writeEventToStream(payload); err != nil {
		redisLogger.WithError(err).WithField("event_type", eventType).Warn("failed to emit cluster event to redis; event dropped")
		metricspkg.IncStateStoreWrite("event_dropped")
		return
	}
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

// ListSchedulerEvents returns the most recent cluster-wide events, newest
// first, from the shared Redis Stream (see emitEvent/writeEventToStream).
// limit <= 0 returns up to the configured max-entries retention bound.
// Returns an empty slice (never an error) if Redis is unavailable — see
// readEventsFromStream's doc comment for why this degrades gracefully
// instead of failing the caller.
func (s *Scheduler) ListSchedulerEvents(limit int64) ([]models.SchedulerEvent, error) {
	events, _ := s.readEventsFromStream(limit)
	return events, nil
}

func workloadManagedVolumeSpecs(workload models.Workload) []models.ManagedVolumeSpec {
	out := make([]models.ManagedVolumeSpec, 0, len(workload.ManagedVolumes)+4)
	out = append(out, workload.ManagedVolumes...)
	if workload.VM != nil {
		out = append(out, workload.VM.ManagedVolumes...)
	}
	return out
}

func metadataStringValue(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	raw, ok := metadata[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", raw))
}

func managedStorageStateEnabled(workload models.Workload) bool {
	return strings.EqualFold(metadataStringValue(workload.Metadata, managedStorageStateKey), "true")
}

func shouldSyncManagedStorage(workload models.Workload) bool {
	if len(workloadManagedVolumeSpecs(workload)) > 0 {
		return true
	}
	if managedStorageStateEnabled(workload) {
		return true
	}
	return false
}

func canonicalStorageDriver(in string) string {
	driver := strings.ToLower(strings.TrimSpace(in))
	switch driver {
	case "ceph_rbd":
		return "ceph-rbd"
	case "":
		return "local"
	default:
		return driver
	}
}

func managedVolumeID(spec models.ManagedVolumeSpec) string {
	driver := canonicalStorageDriver(spec.Driver)
	name := strings.TrimSpace(spec.Name)
	return driver + ":" + name
}

func normalizeRetainPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "retain":
		return "Retain"
	default:
		return "Delete"
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, target string) []string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return values
	}
	if containsString(values, trimmed) {
		return values
	}
	return append(values, trimmed)
}

func removeString(values []string, target string) []string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" || len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == trimmed {
			continue
		}
		out = append(out, value)
	}
	return out
}

func workloadStatusToAttachmentPhase(status string, deleting bool) string {
	if deleting {
		return "Detaching"
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "Attached"
	case "failed":
		return "Error"
	case "deleting":
		return "Detaching"
	default:
		return "Attaching"
	}
}

func workloadStatusToVolumePhase(workload models.Workload, assignedNode string, deleting bool) string {
	if deleting {
		return "Released"
	}
	if strings.TrimSpace(assignedNode) == "" {
		return "Provisioned"
	}
	switch strings.ToLower(strings.TrimSpace(workload.Status)) {
	case "running":
		return "Attached"
	case "failed":
		return "Error"
	default:
		return "Provisioned"
	}
}

func (s *Scheduler) getManagedVolumeRecord(volumeID string) (*models.ManagedVolumeRecord, bool, error) {
	resp, err := s.RetryableEtcdGet(managedVolumeKey(volumeID))
	if err != nil {
		return nil, false, err
	}
	if len(resp.Kvs) == 0 {
		return nil, false, nil
	}
	var record models.ManagedVolumeRecord
	if err := json.Unmarshal(resp.Kvs[0].Value, &record); err != nil {
		return nil, false, err
	}
	return &record, true, nil
}

func (s *Scheduler) saveManagedVolumeRecord(record models.ManagedVolumeRecord) error {
	if strings.TrimSpace(record.ID) == "" {
		return fmt.Errorf("managed volume id is required")
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.RetryableEtcdPut(managedVolumeKey(record.ID), string(payload))
}

func (s *Scheduler) deleteManagedVolumeRecord(volumeID string) error {
	return s.RetryableEtcdDelete(managedVolumeKey(volumeID))
}

func (s *Scheduler) listManagedVolumeRecords() ([]models.ManagedVolumeRecord, error) {
	resp, err := s.RetryableEtcdGet(volumesPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	out := make([]models.ManagedVolumeRecord, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var record models.ManagedVolumeRecord
		if err := json.Unmarshal(kv.Value, &record); err != nil {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *Scheduler) saveVolumeAttachmentRecord(record models.VolumeAttachmentRecord) error {
	if strings.TrimSpace(record.ID) == "" {
		record.ID = volumeAttachmentKey(record.NodeID, record.WorkloadID, record.VolumeID)
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if record.LastTransition.IsZero() {
		record.LastTransition = now
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.RetryableEtcdPut(record.ID, string(payload))
}

func (s *Scheduler) deleteVolumeAttachmentRecord(key string) error {
	return s.RetryableEtcdDelete(key)
}

func (s *Scheduler) listVolumeAttachmentsByWorkload(workloadID string) ([]models.VolumeAttachmentRecord, error) {
	resp, err := s.RetryableEtcdGet(attachmentPrefix(), clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	filter := strings.TrimSpace(workloadID)
	out := make([]models.VolumeAttachmentRecord, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var record models.VolumeAttachmentRecord
		if err := json.Unmarshal(kv.Value, &record); err != nil {
			continue
		}
		if filter != "" && record.WorkloadID != filter {
			continue
		}
		if strings.TrimSpace(record.ID) == "" {
			record.ID = string(kv.Key)
		}
		out = append(out, record)
	}
	return out, nil
}

func volumeLastError(workload models.Workload) string {
	if strings.TrimSpace(workload.StatusInfo.FailureReason) != "" {
		return strings.TrimSpace(workload.StatusInfo.FailureReason)
	}
	if workload.Metadata != nil {
		if raw, ok := workload.Metadata["reason_message"]; ok {
			msg := strings.TrimSpace(fmt.Sprintf("%v", raw))
			if msg != "" {
				return msg
			}
		}
	}
	return ""
}

func (s *Scheduler) syncWorkloadManagedStorage(workload models.Workload) error {
	workloadID := strings.TrimSpace(workload.ID)
	if workloadID == "" {
		return nil
	}

	specs := workloadManagedVolumeSpecs(workload)
	required := make(map[string]models.ManagedVolumeSpec, len(specs))
	for _, spec := range specs {
		if strings.TrimSpace(spec.Name) == "" {
			continue
		}
		volumeID := managedVolumeID(spec)
		required[volumeID] = spec
	}

	deleting := strings.EqualFold(strings.TrimSpace(workload.DesiredState), "deleted")
	assignedNode := strings.TrimSpace(workload.NodeID)
	if assignedNode == "" {
		assignedNode = strings.TrimSpace(workload.AssignedNode)
	}

	existingAttachments, err := s.listVolumeAttachmentsByWorkload(workloadID)
	if err != nil {
		return err
	}
	for _, attachment := range existingAttachments {
		volumeSpec, stillRequired := required[attachment.VolumeID]
		if deleting || !stillRequired || attachment.NodeID != assignedNode || assignedNode == "" {
			_ = s.deleteVolumeAttachmentRecord(attachment.ID)
			record, exists, getErr := s.getManagedVolumeRecord(attachment.VolumeID)
			if getErr == nil && exists {
				record.AttachedNodes = removeString(record.AttachedNodes, attachment.NodeID)
				record.WorkloadRefs = removeString(record.WorkloadRefs, workloadID)
				if len(record.WorkloadRefs) == 0 && normalizeRetainPolicy(record.RetainPolicy) == "Delete" {
					_ = s.deleteManagedVolumeRecord(record.ID)
				} else {
					if normalizeRetainPolicy(record.RetainPolicy) == "Retain" && deleting {
						record.Phase = "Retained"
					} else {
						record.Phase = "Released"
					}
					_ = s.saveManagedVolumeRecord(*record)
				}
			}
			delete(required, attachment.VolumeID)
			_ = volumeSpec
		}
	}

	lastErr := volumeLastError(workload)
	for volumeID, spec := range required {
		record, exists, err := s.getManagedVolumeRecord(volumeID)
		if err != nil {
			return err
		}
		if !exists || record == nil {
			record = &models.ManagedVolumeRecord{
				ID:        volumeID,
				Name:      strings.TrimSpace(spec.Name),
				Driver:    canonicalStorageDriver(spec.Driver),
				CreatedAt: time.Now().UTC(),
				Phase:     "Provisioning",
			}
		}

		record.Name = strings.TrimSpace(spec.Name)
		record.Driver = canonicalStorageDriver(spec.Driver)
		record.SizeGB = spec.SizeGB
		record.AccessMode = strings.TrimSpace(spec.AccessMode)
		record.FSType = strings.TrimSpace(spec.FSType)
		record.RetainPolicy = normalizeRetainPolicy(spec.RetainPolicy)
		record.LastError = ""

		if deleting {
			record.WorkloadRefs = removeString(record.WorkloadRefs, workloadID)
			record.AttachedNodes = removeString(record.AttachedNodes, assignedNode)
			if len(record.WorkloadRefs) == 0 && record.RetainPolicy == "Delete" {
				record.Phase = "Deleting"
				if err := s.saveManagedVolumeRecord(*record); err != nil {
					return err
				}
				if err := s.deleteManagedVolumeRecord(record.ID); err != nil {
					return err
				}
				continue
			}
			record.Phase = "Retained"
			if err := s.saveManagedVolumeRecord(*record); err != nil {
				return err
			}
			continue
		}

		record.WorkloadRefs = appendUniqueString(record.WorkloadRefs, workloadID)
		if assignedNode != "" {
			record.AttachedNodes = appendUniqueString(record.AttachedNodes, assignedNode)
		}
		record.Phase = workloadStatusToVolumePhase(workload, assignedNode, false)
		if strings.EqualFold(record.Phase, "Error") {
			record.LastError = lastErr
		}
		if err := s.saveManagedVolumeRecord(*record); err != nil {
			return err
		}

		if assignedNode == "" {
			continue
		}
		attachment := models.VolumeAttachmentRecord{
			ID:             volumeAttachmentKey(assignedNode, workloadID, volumeID),
			VolumeID:       volumeID,
			WorkloadID:     workloadID,
			NodeID:         assignedNode,
			Driver:         canonicalStorageDriver(spec.Driver),
			MountPath:      strings.TrimSpace(spec.MountPath),
			ReadOnly:       spec.ReadOnly,
			Phase:          workloadStatusToAttachmentPhase(workload.Status, false),
			LastTransition: time.Now().UTC(),
		}
		if strings.EqualFold(attachment.Phase, "Error") {
			attachment.LastError = lastErr
		}
		if err := s.saveVolumeAttachmentRecord(attachment); err != nil {
			return err
		}
	}

	volumeRecords, err := s.listManagedVolumeRecords()
	if err != nil {
		return err
	}
	for _, record := range volumeRecords {
		if !containsString(record.WorkloadRefs, workloadID) {
			continue
		}
		if _, ok := required[record.ID]; ok && !deleting {
			continue
		}
		record.WorkloadRefs = removeString(record.WorkloadRefs, workloadID)
		record.AttachedNodes = removeString(record.AttachedNodes, assignedNode)
		if len(record.WorkloadRefs) == 0 && normalizeRetainPolicy(record.RetainPolicy) == "Delete" {
			if err := s.deleteManagedVolumeRecord(record.ID); err != nil {
				return err
			}
			continue
		}
		if deleting && normalizeRetainPolicy(record.RetainPolicy) == "Retain" {
			record.Phase = "Retained"
		} else {
			record.Phase = "Released"
		}
		if err := s.saveManagedVolumeRecord(record); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) cleanupManagedStorageForWorkload(workloadID string) error {
	workloadID = strings.TrimSpace(workloadID)
	if workloadID == "" {
		return nil
	}

	attachments, err := s.listVolumeAttachmentsByWorkload(workloadID)
	if err != nil {
		return err
	}
	seenVolumes := make(map[string]struct{}, len(attachments))
	for _, attachment := range attachments {
		_ = s.deleteVolumeAttachmentRecord(attachment.ID)
		if strings.TrimSpace(attachment.VolumeID) != "" {
			seenVolumes[attachment.VolumeID] = struct{}{}
		}
	}

	volumes, err := s.listManagedVolumeRecords()
	if err != nil {
		return err
	}
	for _, volume := range volumes {
		if !containsString(volume.WorkloadRefs, workloadID) {
			if _, ok := seenVolumes[volume.ID]; !ok {
				continue
			}
		}
		volume.WorkloadRefs = removeString(volume.WorkloadRefs, workloadID)
		for _, attachment := range attachments {
			if attachment.VolumeID == volume.ID {
				volume.AttachedNodes = removeString(volume.AttachedNodes, attachment.NodeID)
			}
		}
		if len(volume.WorkloadRefs) == 0 && normalizeRetainPolicy(volume.RetainPolicy) == "Delete" {
			if err := s.deleteManagedVolumeRecord(volume.ID); err != nil {
				return err
			}
			continue
		}
		if len(volume.WorkloadRefs) == 0 && normalizeRetainPolicy(volume.RetainPolicy) == "Retain" {
			volume.Phase = "Retained"
		} else if len(volume.WorkloadRefs) == 0 {
			volume.Phase = "Released"
		}
		if err := s.saveManagedVolumeRecord(volume); err != nil {
			return err
		}
	}
	return nil
}
