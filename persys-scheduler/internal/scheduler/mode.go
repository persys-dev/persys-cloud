package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type OperatingMode string

const (
	ModeNormal   OperatingMode = "normal"
	ModeDegraded OperatingMode = "degraded"
	ModeRecovery OperatingMode = "recovery"
)

var errControlPlaneFrozen = fmt.Errorf("scheduler control plane is frozen (degraded/recovery mode)")

type FrozenState struct {
	CreatedAt   time.Time                          `json:"createdAt"`
	Reason      string                             `json:"reason"`
	Nodes       map[string]models.Node             `json:"nodes"`
	Workloads   map[string]models.Workload         `json:"workloads"`
	Assignments map[string]models.AssignmentRecord `json:"assignments"`
}

func (s *Scheduler) currentMode() OperatingMode {
	s.modeMu.RLock()
	defer s.modeMu.RUnlock()
	return s.mode
}

func (s *Scheduler) CurrentMode() OperatingMode {
	return s.currentMode()
}

func (s *Scheduler) IsWritable() bool {
	return s.isWritable()
}

func (s *Scheduler) modeReason() string {
	s.modeMu.RLock()
	defer s.modeMu.RUnlock()
	return s.modeReasonText
}

func (s *Scheduler) isWritable() bool {
	return s.currentMode() == ModeNormal
}

func (s *Scheduler) requireWritable() error {
	if s.isWritable() {
		return nil
	}
	return errControlPlaneFrozen
}

func (s *Scheduler) enterDegraded(reason string) {
	s.modeMu.Lock()
	from := s.mode
	if from == ModeDegraded {
		s.modeMu.Unlock()
		return
	}
	s.mode = ModeDegraded
	s.modeReasonText = reason
	s.modeChangedAt = time.Now().UTC()
	nodes, workloads, assignments := s.cacheCopies()
	s.frozen = &FrozenState{
		CreatedAt:   s.modeChangedAt,
		Reason:      reason,
		Nodes:       nodes,
		Workloads:   workloads,
		Assignments: assignments,
	}
	changedAt := s.modeChangedAt
	s.modeMu.Unlock()

	schedulerLogger.WithField("reason", reason).WithField("from", string(from)).WithField("to", string(ModeDegraded)).Warn("scheduler entered degraded mode")
	s.emitEvent("SchedulerModeChanged", "", "", reason, map[string]interface{}{
		"from":       string(from),
		"to":         string(ModeDegraded),
		"reason":     reason,
		"changed_at": changedAt.UTC().Format(time.RFC3339),
		"writable":   false,
	})
}

func (s *Scheduler) enterRecovery(reason string) {
	s.modeMu.Lock()
	from := s.mode
	if from == ModeRecovery {
		s.modeMu.Unlock()
		return
	}
	s.mode = ModeRecovery
	s.modeReasonText = reason
	s.modeChangedAt = time.Now().UTC()
	if s.frozen == nil {
		nodes, workloads, assignments := s.cacheCopies()
		s.frozen = &FrozenState{
			CreatedAt:   s.modeChangedAt,
			Reason:      reason,
			Nodes:       nodes,
			Workloads:   workloads,
			Assignments: assignments,
		}
	}
	changedAt := s.modeChangedAt
	s.modeMu.Unlock()

	schedulerLogger.WithField("reason", reason).WithField("from", string(from)).WithField("to", string(ModeRecovery)).Warn("scheduler entered recovery mode")
	s.emitEvent("SchedulerModeChanged", "", "", reason, map[string]interface{}{
		"from":       string(from),
		"to":         string(ModeRecovery),
		"reason":     reason,
		"changed_at": changedAt.UTC().Format(time.RFC3339),
		"writable":   false,
	})
}

func (s *Scheduler) enterNormal(reason string) {
	s.modeMu.Lock()
	from := s.mode
	if from == ModeNormal {
		s.modeMu.Unlock()
		return
	}
	s.mode = ModeNormal
	s.modeReasonText = reason
	s.modeChangedAt = time.Now().UTC()
	s.frozen = nil
	changedAt := s.modeChangedAt
	s.modeMu.Unlock()

	schedulerLogger.WithField("reason", reason).WithField("from", string(from)).WithField("to", string(ModeNormal)).Info("scheduler returned to normal mode")
	s.emitEvent("SchedulerModeChanged", "", "", reason, map[string]interface{}{
		"from":       string(from),
		"to":         string(ModeNormal),
		"reason":     reason,
		"changed_at": changedAt.UTC().Format(time.RFC3339),
		"writable":   true,
	})
}

func (s *Scheduler) ModeSnapshot() (OperatingMode, string, time.Time) {
	s.modeMu.RLock()
	defer s.modeMu.RUnlock()
	return s.mode, s.modeReasonText, s.modeChangedAt
}

func (s *Scheduler) startModeSupervisor(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.evaluateMode()
		}
	}
}

func (s *Scheduler) evaluateMode() {
	if err := s.pingEtcd(); err != nil {
		s.enterDegraded(fmt.Sprintf("etcd unreachable: %v", err))
		return
	}

	mode := s.currentMode()
	if mode == ModeNormal {
		return
	}

	hasData, err := s.etcdHasState()
	if err != nil {
		s.enterDegraded(fmt.Sprintf("etcd health probe failed: %v", err))
		return
	}
	if hasData {
		s.enterNormal("etcd recovered with persistent state intact")
		return
	}
	s.enterRecovery("etcd recovered but state is empty; waiting for restore/import")
}

func (s *Scheduler) pingEtcd() error {
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	_, err := s.etcdClient.Get(ctx, "/health")
	return err
}

func (s *Scheduler) etcdHasState() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	prefixes := []string{nodesPrefix, workloadsPrefix, assignmentsPrefix}
	for _, p := range prefixes {
		resp, err := s.etcdClient.Get(ctx, p, clientv3.WithPrefix(), clientv3.WithLimit(1))
		if err != nil {
			return false, err
		}
		if resp != nil && len(resp.Kvs) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func cloneNodeMap(in map[string]models.Node) map[string]models.Node {
	out := make(map[string]models.Node, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneWorkloadMap(in map[string]models.Workload) map[string]models.Workload {
	out := make(map[string]models.Workload, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAssignmentMap(in map[string]models.AssignmentRecord) map[string]models.AssignmentRecord {
	out := make(map[string]models.AssignmentRecord, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *Scheduler) cacheCopies() (map[string]models.Node, map[string]models.Workload, map[string]models.AssignmentRecord) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return cloneNodeMap(s.cacheNodes), cloneWorkloadMap(s.cacheWorkloads), cloneAssignmentMap(s.cacheAssignments)
}

func cacheSnapshot[T any](in map[string]T) []T {
	out := make([]T, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

// runBounded calls fn once per item, with at most concurrency goroutines in
// flight at a time, and waits for all of them to finish before returning.
// Shared by MonitorNodes, MonitorWorkloads, and detectDriftOnce so each
// doesn't reimplement the same semaphore+WaitGroup boilerplate; all three
// were originally plain sequential for-loops making one synchronous
// network call per item; this gives them the same bounded-concurrency
// treatment the reconciler already got, without changing the fan-out shape
// of what each loop iteration does.
func runBounded[T any](items []T, concurrency int, fn func(T)) {
	if concurrency <= 0 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, item := range items {
		item := item
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(item)
		}()
	}
	wg.Wait()
}

func (s *Scheduler) withCacheLock(fn func()) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	fn()
}

func (s *Scheduler) getCachedNodes() []models.Node {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return cacheSnapshot(s.cacheNodes)
}

func (s *Scheduler) getCachedWorkloads() []models.Workload {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return cacheSnapshot(s.cacheWorkloads)
}

func (s *Scheduler) getCachedNode(id string) (models.Node, bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	v, ok := s.cacheNodes[id]
	return v, ok
}

func (s *Scheduler) getCachedWorkload(id string) (models.Workload, bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	v, ok := s.cacheWorkloads[id]
	return v, ok
}

func (s *Scheduler) cacheNode(node models.Node) {
	s.withCacheLock(func() { s.cacheNodes[node.NodeID] = node })
}

func (s *Scheduler) cacheWorkload(workload models.Workload) {
	s.withCacheLock(func() { s.cacheWorkloads[workload.ID] = workload })
}

func (s *Scheduler) cacheAssignment(rec models.AssignmentRecord) {
	s.withCacheLock(func() { s.cacheAssignments[rec.WorkloadID] = rec })
}

func (s *Scheduler) removeCachedWorkload(id string) {
	s.withCacheLock(func() {
		delete(s.cacheWorkloads, id)
		delete(s.cacheAssignments, id)
	})
}
