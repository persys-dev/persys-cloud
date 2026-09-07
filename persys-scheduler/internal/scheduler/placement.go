package scheduler

import (
	"hash/fnv"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
)

// Placement scoring weights. CPU and memory headroom dominate (a node
// that's actually short on resources should never outscore one that
// isn't), with a smaller spread term to avoid piling workloads onto an
// otherwise-tied node just because it happened to sort first. These are
// constants rather than a config knob for now because retuning them
// usefully needs operational data on real workload mixes that doesn't
// exist yet — a knob nobody has good values for is worse than no knob.
const (
	placementWeightCPU    = 0.4
	placementWeightMemory = 0.4
	placementWeightSpread = 0.2

	// pendingReservationTTL bounds how long an in-flight placement
	// reservation (see below) is counted against a node before it's
	// treated as stale and dropped. Chosen as a multiple of a typical
	// heartbeat interval so a reservation reliably outlives the window
	// where the node's own reported AvailableCPU/AvailableMemory hasn't
	// caught up yet, without leaking indefinitely if a workload's status
	// updates are delayed or lost.
	pendingReservationTTL = 90 * time.Second
)

// pendingReservation tracks resources committed to a workload that was
// just assigned to a node but not yet reflected in that node's own
// heartbeat-reported availability.
type pendingReservation struct {
	cpu       float64
	memory    float64
	expiresAt time.Time
}

// reservePlacement records that workloadID has just been assigned to
// nodeID, committing the given CPU/memory. Called from assignWorkload
// immediately after a successful assignment.
//
// This exists because node.AvailableCPU/AvailableMemory are only updated
// by that node's own heartbeat, which lags behind an assignment decision.
// Without tracking commitments locally, concurrent placement decisions —
// routine now: reconciliation and monitoring run with bounded concurrency,
// and active-active mode can have multiple scheduler replicas placing
// workloads at the same time — can all read the same stale "available"
// numbers and independently pick the same node, oversubscribing it before
// any of their heartbeats catch up. Reservations are advisory (they adjust
// scoring, not a hard admission-control gate) and self-expire via
// pendingReservationTTL rather than needing an explicit clear on heartbeat
// confirmation, trading a little scoring precision for not having to hook
// into every workload-status-confirmation path.
func (s *Scheduler) reservePlacement(nodeID, workloadID string, cpu float64, memory float64) {
	if strings.TrimSpace(nodeID) == "" || strings.TrimSpace(workloadID) == "" {
		return
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pendingReservations == nil {
		s.pendingReservations = make(map[string]map[string]pendingReservation)
	}
	byWorkload, ok := s.pendingReservations[nodeID]
	if !ok {
		byWorkload = make(map[string]pendingReservation)
		s.pendingReservations[nodeID] = byWorkload
	}
	byWorkload[workloadID] = pendingReservation{
		cpu:       cpu,
		memory:    memory,
		expiresAt: time.Now().Add(pendingReservationTTL),
	}
}

// pendingReservationFor sums non-expired reservations for nodeID, pruning
// expired entries as it goes so the map doesn't grow unboundedly.
func (s *Scheduler) pendingReservationFor(nodeID string) (cpu float64, memory float64) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	byWorkload, ok := s.pendingReservations[nodeID]
	if !ok {
		return 0, 0
	}
	now := time.Now()
	for workloadID, r := range byWorkload {
		if now.After(r.expiresAt) {
			delete(byWorkload, workloadID)
			continue
		}
		cpu += r.cpu
		memory += r.memory
	}
	if len(byWorkload) == 0 {
		delete(s.pendingReservations, nodeID)
	}
	return cpu, memory
}

// workloadCountsByNode returns how many non-terminal workloads are
// currently assigned to each node, plus the highest count on any single
// node, for use as the spread term in nodePlacementScore. Computed once
// per placement decision (not once per candidate node), so it costs one
// extra GetWorkloads() call per scheduling event — negligible next to
// actual scheduling event rates, unlike the per-workload agent RPCs the
// reconciler used to make on every cycle.
func (s *Scheduler) workloadCountsByNode() (counts map[string]int, maxCount int, err error) {
	workloads, err := s.GetWorkloads()
	if err != nil {
		return nil, 0, err
	}
	counts = make(map[string]int)
	for _, w := range workloads {
		if w.Status == "Completed" || w.Status == "Deleted" {
			continue
		}
		nodeID := strings.TrimSpace(w.NodeID)
		if nodeID == "" {
			continue
		}
		counts[nodeID]++
		if counts[nodeID] > maxCount {
			maxCount = counts[nodeID]
		}
	}
	return counts, maxCount, nil
}

// nodePlacementScore scores a feasible candidate node for a workload;
// higher is better. It blends three signals rather than the single
// CPU+memory average the previous version used:
//
//   - CPU and memory headroom, adjusted by pendingReservationFor so
//     resources already committed to workloads assigned in this
//     scheduling window (but not yet reflected in the node's own
//     heartbeat) count against the node instead of being invisible.
//   - A spread term based on how many workloads are already assigned to
//     the node relative to the busiest candidate, so two nodes with
//     similar CPU/memory ratios aren't treated as identical if one is
//     already hosting far more workloads than the other.
//   - A small deterministic jitter, hashed from the workload and node ID
//     (not real randomness — reproducible for the same workload/node
//     pair), to break exact ties without always resolving them to
//     whichever node happens to sort first. Real ties are common in a
//     mostly-homogeneous fleet; always breaking them the same way is
//     itself a form of imbalance.
func nodePlacementScore(node models.Node, workload models.Workload, pendingCPU, pendingMemory float64, workloadCount, maxWorkloadCount int) float64 {
	cpuTotal := node.TotalCPU
	memTotal := float64(node.TotalMemory)
	if cpuTotal <= 0 || memTotal <= 0 {
		return -1e9
	}

	effectiveAvailableCPU := node.AvailableCPU - pendingCPU
	effectiveAvailableMemory := float64(node.AvailableMemory) - pendingMemory

	cpuHeadroom := effectiveAvailableCPU / cpuTotal
	memHeadroom := effectiveAvailableMemory / memTotal

	spreadTerm := 1.0
	if maxWorkloadCount > 0 {
		spreadTerm = 1.0 - (float64(workloadCount) / float64(maxWorkloadCount))
	}

	score := placementWeightCPU*cpuHeadroom + placementWeightMemory*memHeadroom + placementWeightSpread*spreadTerm
	score += placementTieBreakJitter(workload.ID, node.NodeID)
	return score
}

// placementTieBreakJitter returns a small, deterministic (not random)
// value in [0, 0.01) derived from the workload/node ID pair — large enough
// to reliably separate exact ties between the weighted terms above (which
// each range roughly 0-1), small enough to never override a genuine
// difference between two candidates.
func placementTieBreakJitter(workloadID, nodeID string) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(workloadID + "|" + nodeID))
	return (float64(h.Sum32()%1000) / 1000.0) * 0.01
}
