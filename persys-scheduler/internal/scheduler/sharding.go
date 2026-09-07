package scheduler

import (
	"fmt"
	"hash/crc32"
	"strings"
)

// haModeActiveActive is the SCHEDULER_HA_MODE value that enables sharding.
// Any other value (including empty/unset) is treated as "failover", the
// existing single-active-instance behavior from the previous round of
// this work.
const haModeActiveActive = "active-active"

// isActiveActive reports whether this instance is configured for
// active-active (sharded) mode rather than failover mode.
func (s *Scheduler) isActiveActive() bool {
	return s.cfg != nil && strings.EqualFold(strings.TrimSpace(s.cfg.SchedulerHAMode), haModeActiveActive)
}

// shardTopology returns the effective (count, index) for this instance,
// normalizing invalid configuration (count < 1, index out of range) down
// to the safe single-shard default rather than silently misbehaving.
func (s *Scheduler) shardTopology() (count, index int) {
	count = 1
	index = 0
	if s.cfg == nil {
		return
	}
	if s.cfg.SchedulerShardCount > 1 {
		count = s.cfg.SchedulerShardCount
	}
	if s.cfg.SchedulerShardIndex > 0 && s.cfg.SchedulerShardIndex < count {
		index = s.cfg.SchedulerShardIndex
	}
	return
}

// ownsNode reports whether this scheduler instance is responsible for
// driving reconciliation/monitoring/drift-detection for the given node.
//
// In failover mode (the default), exactly one scheduler instance is ever
// active cluster-wide (see leader.go), so it owns every node — this always
// returns true, matching pre-sharding behavior exactly.
//
// In active-active mode, nodes are partitioned across SCHEDULER_SHARD_COUNT
// shards by a stable hash of the node ID, and this instance only owns the
// nodes that hash to its own SCHEDULER_SHARD_INDEX. Placement itself
// (selectNodeForWorkload) is NOT gated by this — any replica can accept an
// ApplyWorkload call and assign a workload to any node cluster-wide; only
// the ongoing convergence loops (reconciliation, node/workload monitoring,
// drift detection) are partitioned, so a workload's steady-state upkeep is
// driven by whichever shard owns the node it landed on.
//
// Known caveat, worth having in mind before enabling active-active mode:
// if a node fails and RelocateWorkloadsFromNode reassigns its workloads to
// a node owned by a *different* shard, ownership of those workloads
// follows their new node immediately, but there's no explicit hand-off
// protocol between shards — the old shard simply stops seeing them on its
// next cycle (the workload's NodeID changed) and the new shard picks them
// up on its own next cycle. This is expected to self-heal within one
// reconcile interval, not a correctness bug, but it does mean a brief
// window where neither shard is actively driving that specific workload.
func (s *Scheduler) ownsNode(nodeID string) bool {
	if !s.isActiveActive() {
		return true
	}
	count, index := s.shardTopology()
	if count <= 1 {
		return true
	}
	h := crc32.ChecksumIEEE([]byte(strings.TrimSpace(nodeID)))
	return int(h%uint32(count)) == index
}

// electionKey returns the etcd key this instance campaigns on for
// leadership (see leader.go). In failover mode every replica contends on
// the same global key, so exactly one is ever active. In active-active
// mode each shard gets its own independent key, so replicas configured
// with different SCHEDULER_SHARD_INDEX values never contend with each
// other and can be active simultaneously; replicas sharing the same shard
// index still only elect one active owner for that shard, giving you HA
// within a shard if you run more than one replica per index.
func (s *Scheduler) electionKey() string {
	if !s.isActiveActive() {
		return leaderElectionKey
	}
	_, index := s.shardTopology()
	return fmt.Sprintf("%s/shard-%d", leaderElectionKey, index)
}
