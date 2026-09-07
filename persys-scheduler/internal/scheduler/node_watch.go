package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	clientv3 "go.etcd.io/etcd/client/v3"
)

var nodeWatchLogger = logging.C("scheduler.node_watch")

// nodeWatchResyncBackoff is how long StartNodeWatch waits before retrying
// after the watch stream ends (connection loss, etcd compaction ahead of
// our revision, etc.) or a resync attempt fails outright.
const nodeWatchResyncBackoff = 2 * time.Second

// StartNodeWatch runs a loop that keeps the in-memory node cache
// (Scheduler.cacheNodes) continuously up to date via an etcd watch on the
// node prefix, instead of the cache only being populated as a side effect
// of whatever GetNodes()/GetNodeByID() calls happen to occur. This is what
// candidateNodeSnapshot (used by selectNodeForWorkload) relies on to avoid
// a full etcd scan + unmarshal-every-node on every single placement
// decision.
//
// Like the scheduler's other Start* background loops (StartDriftDetection,
// Reconciler.StartReconciliationLoop), this call blocks until ctx is
// cancelled — callers run it in its own goroutine, tracked by the same
// wait group as the other background loops. It resyncs and re-watches
// automatically if the underlying watch stream ends for any reason.
func (s *Scheduler) StartNodeWatch(ctx context.Context) {
	nodeWatchLogger.Info("starting node watch")
	for {
		select {
		case <-ctx.Done():
			nodeWatchLogger.Info("stopping node watch")
			return
		default:
		}

		if err := s.watchNodesOnce(ctx); err != nil && ctx.Err() == nil {
			nodeWatchLogger.WithError(err).Warn("node watch stream ended, will resync")
		}
		// The cache is now stale relative to reality until the next
		// successful resync below; candidateNodeSnapshot falls back to a
		// live scan while nodeCacheReady is false, so placement keeps
		// working (just without the fast path) during the gap.
		s.nodeCacheReady.Store(false)

		select {
		case <-ctx.Done():
			nodeWatchLogger.Info("stopping node watch")
			return
		case <-time.After(nodeWatchResyncBackoff):
		}
	}
}

// watchNodesOnce does a full resync of the node cache from etcd, then
// watches from that revision forward, applying each event to the cache as
// it arrives. Returns when the watch channel closes or errors; the caller
// (runNodeWatch) handles backoff and resync.
func (s *Scheduler) watchNodesOnce(ctx context.Context) error {
	resp, err := s.RetryableEtcdGet(nodesPrefix, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("initial node resync failed: %w", err)
	}

	fresh := make(map[string]models.Node, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		if isNodeStatusSubKey(string(kv.Key)) {
			continue
		}
		var node models.Node
		if err := json.Unmarshal(kv.Value, &node); err != nil {
			nodeWatchLogger.WithError(err).WithField("key", string(kv.Key)).Warn("failed to unmarshal node during resync")
			continue
		}
		fresh[node.NodeID] = node
	}
	s.withCacheLock(func() {
		// Replace wholesale rather than merge: this is a full resync, so
		// any node that no longer exists in etcd should no longer exist
		// in the cache either.
		s.cacheNodes = fresh
	})
	s.nodeCacheReady.Store(true)
	nodeWatchLogger.WithField("node_count", len(fresh)).Debug("node cache resynced")

	watchCh := s.etcdClient.Watch(ctx, nodesPrefix, clientv3.WithPrefix(), clientv3.WithRev(resp.Header.Revision+1))
	for wresp := range watchCh {
		if err := wresp.Err(); err != nil {
			return fmt.Errorf("node watch error: %w", err)
		}
		for _, ev := range wresp.Events {
			key := string(ev.Kv.Key)
			if isNodeStatusSubKey(key) {
				continue
			}
			nodeID := strings.TrimPrefix(key, nodesPrefix)
			switch ev.Type {
			case clientv3.EventTypePut:
				var node models.Node
				if err := json.Unmarshal(ev.Kv.Value, &node); err != nil {
					nodeWatchLogger.WithError(err).WithField("node_id", nodeID).Warn("failed to unmarshal node watch event")
					continue
				}
				s.cacheNode(node)
			case clientv3.EventTypeDelete:
				s.withCacheLock(func() { delete(s.cacheNodes, nodeID) })
			}
		}
	}
	// Channel closed without an error surfaced through wresp.Err() — treat
	// as a stream end like any other, so the caller resyncs.
	return fmt.Errorf("node watch channel closed")
}
