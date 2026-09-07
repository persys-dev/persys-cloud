package scheduler

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"go.etcd.io/etcd/client/v3/concurrency"
)

var leaderLogger = logging.C("scheduler.leader")

// leaderElectionKey is the etcd key prefix campaigned on. In failover mode
// (the default), every scheduler replica pointed at the same etcd cluster
// contends directly on this key, so exactly one of them holds it at a
// time. In active-active mode, this is used as a prefix — see
// Scheduler.electionKey in sharding.go — with each shard campaigning on
// its own derived key instead.
const leaderElectionKey = "/persys/scheduler/leader"

// leaderSessionTTL controls how quickly a dead leader's session (and thus
// its lease on the election key) expires, letting a standby take over.
// Shorter TTLs fail over faster but are more sensitive to transient GC
// pauses/network blips causing an unnecessary handover; 15s is a
// reasonable middle ground and matches common etcd lease-based election
// examples.
const leaderSessionTTL = 15 // seconds

// instanceID identifies this scheduler process in the election (visible in
// etcd as the campaign value, useful for "who's the leader right now"
// debugging) and is generated once at construction time.
func newInstanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s-%s", host, uuid.NewString()[:8])
}

// IsLeader reports whether this scheduler instance currently holds the
// leader election. Background singleton loops (reconciliation, drift
// detection, node/workload monitoring, the node-cache watch) only run while
// this is true; the gRPC API (RegisterNode, Heartbeat, ApplyWorkload, ...)
// runs on every replica regardless, since those paths write directly to
// etcd (with CAS where it matters) and are safe to serve from any replica.
func (s *Scheduler) IsLeader() bool {
	return s.isLeader.Load()
}

// RunWithLeaderElection blocks until ctx is cancelled, campaigning for
// leadership on leaderElectionKey and invoking runAsLeader (in the calling
// goroutine) for as long as this instance holds it. If leadership is lost —
// session/lease expiry from a GC pause, network partition, process being
// too slow to renew, or a clean loss on shutdown — runAsLeader's context is
// cancelled and this re-campaigns from scratch. Callers should make
// runAsLeader itself react to context cancellation the way the existing
// Start* background loops already do.
//
// This is what turns the scheduler from "exactly one process, full stop"
// into "exactly one *active* process, with automatic failover" — multiple
// replicas can run the same binary pointed at the same etcd cluster, and
// only the elected one drives reconciliation/monitoring/drift detection at
// any given moment, so a crashed or restarting leader doesn't pause
// cluster-wide reconciliation for longer than one election round.
func (s *Scheduler) RunWithLeaderElection(ctx context.Context, runAsLeader func(leaderCtx context.Context)) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.runOneElectionTerm(ctx, runAsLeader); err != nil {
			leaderLogger.WithError(err).Warn("leader election term ended with error, retrying")
		}
		s.isLeader.Store(false)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// runOneElectionTerm campaigns once, and if elected, runs runAsLeader until
// either ctx is cancelled or leadership is lost (session expiry / observed
// change of leader). Returns when this instance is no longer leader (or
// never became leader due to an error), so the caller can decide whether to
// retry.
func (s *Scheduler) runOneElectionTerm(ctx context.Context, runAsLeader func(leaderCtx context.Context)) error {
	session, err := concurrency.NewSession(s.etcdClient, concurrency.WithTTL(leaderSessionTTL), concurrency.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to create election session: %w", err)
	}
	defer session.Close()

	election := concurrency.NewElection(session, s.electionKey())

	leaderLogger.WithFields(map[string]interface{}{
		"instance_id":  s.instanceID,
		"election_key": s.electionKey(),
	}).Info("campaigning for scheduler leadership")
	if err := election.Campaign(ctx, s.instanceID); err != nil {
		return fmt.Errorf("campaign failed: %w", err)
	}

	s.isLeader.Store(true)
	leaderLogger.WithField("instance_id", s.instanceID).Info("elected scheduler leader")
	s.emitEvent("LeaderElected", "", "", "scheduler instance elected leader", map[string]interface{}{
		"instance_id":  s.instanceID,
		"election_key": s.electionKey(),
	})

	leaderCtx, cancelLeader := context.WithCancel(ctx)
	defer cancelLeader()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runAsLeader(leaderCtx)
	}()

	// Give up leadership if: the caller's context is cancelled (normal
	// shutdown), the etcd session expires (we failed to renew our lease in
	// time — e.g. a long GC pause or network partition), or runAsLeader
	// itself returns (shouldn't normally happen since it's built from
	// blocking Start*-style loops, but don't hang forever if it does).
	select {
	case <-ctx.Done():
	case <-session.Done():
		leaderLogger.Warn("leader election session expired, relinquishing leadership")
	case <-done:
		leaderLogger.Warn("leader-only work returned unexpectedly, relinquishing leadership")
	}

	cancelLeader()
	<-done // wait for runAsLeader to actually stop before we resign/close the session
	wasLeader := s.isLeader.Swap(false)
	if wasLeader {
		s.emitEvent("LeaderLost", "", "", "scheduler instance relinquished leadership", map[string]interface{}{
			"instance_id": s.instanceID,
		})
	}

	resignCtx, resignCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer resignCancel()
	_ = election.Resign(resignCtx)

	return nil
}
