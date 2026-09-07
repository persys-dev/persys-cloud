package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	"github.com/sirupsen/logrus"
)

var nodeLogger = logging.C("scheduler.node_control")

func (s *Scheduler) UpdateNodeHeartbeat(nodeID, status string, availableCPU float64, availableMemory int64) error {
	_, err := s.updateNodeCAS(nodeID, func(node *models.Node) {
		node.LastHeartbeat = time.Now().UTC()
		if strings.TrimSpace(status) != "" {
			previousStatus := node.Status
			if strings.EqualFold(previousStatus, "Draining") && strings.EqualFold(status, "Ready") {
				status = "Draining"
			}
			node.Status = status
			if strings.EqualFold(status, "Ready") {
				if strings.EqualFold(previousStatus, "Ready") {
					node.StatusReason = "heartbeat received"
				} else {
					node.StatusReason = fmt.Sprintf("status transition %s -> %s via heartbeat", previousStatus, status)
					nodeLogger.WithField("node_id", nodeID).Info("node recovered to Ready via heartbeat")
				}
				node.StatusUpdatedBy = "heartbeat"
				node.StatusUpdatedAt = time.Now().UTC()
			} else if !strings.EqualFold(previousStatus, status) {
				node.StatusReason = "heartbeat status transition"
				node.StatusUpdatedBy = "heartbeat"
				node.StatusUpdatedAt = time.Now().UTC()
			}
		}
		if availableCPU >= 0 {
			node.AvailableCPU = availableCPU
		}
		if availableMemory >= 0 {
			node.AvailableMemory = availableMemory
		}
	})
	if err != nil {
		return fmt.Errorf("failed to update node %s heartbeat: %w", nodeID, err)
	}
	return nil
}

func (s *Scheduler) MarkNodeDraining(nodeID, reason, source string) (int, error) {
	node, err := s.updateNode(nodeID, func(node *models.Node) {
		node.Status = "Draining"
		node.StatusReason = defaultString(reason, "node drain requested")
		node.StatusUpdatedBy = defaultString(source, "operator")
		node.StatusUpdatedAt = time.Now().UTC()
	})
	if err != nil {
		return 0, err
	}
	s.emitEvent("NodeDraining", "", nodeID, defaultString(reason, "node drain requested"), map[string]interface{}{
		"source": defaultString(source, "operator"),
	})
	relocated, err := s.RelocateWorkloadsFromNode(node.NodeID, "node draining")
	if err != nil {
		return relocated, err
	}
	return relocated, nil
}

func (s *Scheduler) MarkNodeReady(nodeID, reason, source string) error {
	_, err := s.updateNode(nodeID, func(node *models.Node) {
		node.Status = "Ready"
		node.StatusReason = defaultString(reason, "node returned to service")
		node.StatusUpdatedBy = defaultString(source, "operator")
		node.StatusUpdatedAt = time.Now().UTC()
	})
	if err != nil {
		return err
	}
	s.emitEvent("NodeReady", "", nodeID, defaultString(reason, "node returned to service"), map[string]interface{}{
		"source": defaultString(source, "operator"),
	})
	return nil
}

func (s *Scheduler) TaintNode(nodeID string, taint models.NodeTaint, source string) error {
	_, err := s.updateNode(nodeID, func(node *models.Node) {
		taint.Key = strings.TrimSpace(taint.Key)
		taint.Effect = normalizeTaintEffect(taint.Effect)
		replaced := false
		for i := range node.Taints {
			if node.Taints[i].Key == taint.Key && strings.EqualFold(node.Taints[i].Effect, taint.Effect) {
				node.Taints[i] = taint
				replaced = true
				break
			}
		}
		if !replaced {
			node.Taints = append(node.Taints, taint)
		}
		node.StatusReason = fmt.Sprintf("taint %s=%s:%s set", taint.Key, taint.Value, taint.Effect)
		node.StatusUpdatedBy = defaultString(source, "operator")
		node.StatusUpdatedAt = time.Now().UTC()
	})
	if err != nil {
		return err
	}
	s.emitEvent("NodeTainted", "", nodeID, "node taint applied", map[string]interface{}{
		"source": defaultString(source, "operator"),
		"key":    strings.TrimSpace(taint.Key),
		"effect": normalizeTaintEffect(taint.Effect),
		"value":  taint.Value,
	})
	return nil
}

func (s *Scheduler) UntaintNode(nodeID, key, effect, source string) error {
	key = strings.TrimSpace(key)
	effectNorm := normalizeTaintEffect(effect)
	_, err := s.updateNode(nodeID, func(node *models.Node) {
		filtered := node.Taints[:0]
		for _, t := range node.Taints {
			if t.Key == key && (effectNorm == "" || strings.EqualFold(t.Effect, effectNorm)) {
				continue
			}
			filtered = append(filtered, t)
		}
		node.Taints = filtered
		node.StatusReason = "taint removed"
		node.StatusUpdatedBy = defaultString(source, "operator")
		node.StatusUpdatedAt = time.Now().UTC()
	})
	if err != nil {
		return err
	}
	s.emitEvent("NodeUntainted", "", nodeID, "node taint removed", map[string]interface{}{
		"source": defaultString(source, "operator"),
		"key":    key,
		"effect": effectNorm,
	})
	return nil
}

func (s *Scheduler) SetNodeLabel(nodeID, key, value, source string) error {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	_, err := s.updateNode(nodeID, func(node *models.Node) {
		if node.Labels == nil {
			node.Labels = map[string]string{}
		}
		node.Labels[key] = value
		node.StatusReason = fmt.Sprintf("label %s set", key)
		node.StatusUpdatedBy = defaultString(source, "operator")
		node.StatusUpdatedAt = time.Now().UTC()
	})
	if err != nil {
		return err
	}
	s.emitEvent("NodeLabelSet", "", nodeID, fmt.Sprintf("label %s set", key), map[string]interface{}{
		"source": defaultString(source, "operator"),
		"key":    key,
		"value":  value,
	})
	return nil
}

func (s *Scheduler) DeleteNodeLabel(nodeID, key, source string) error {
	key = strings.TrimSpace(key)
	_, err := s.updateNode(nodeID, func(node *models.Node) {
		delete(node.Labels, key)
		node.StatusReason = fmt.Sprintf("label %s deleted", key)
		node.StatusUpdatedBy = defaultString(source, "operator")
		node.StatusUpdatedAt = time.Now().UTC()
	})
	if err != nil {
		return err
	}
	s.emitEvent("NodeLabelDeleted", "", nodeID, fmt.Sprintf("label %s deleted", key), map[string]interface{}{
		"source": defaultString(source, "operator"),
		"key":    key,
	})
	return nil
}

// updateNode applies mutate to the current node record and persists it.
// Kept as a thin wrapper over updateNodeCAS for the existing call sites
// (MarkNodeDraining, MarkNodeReady, TaintNode, UntaintNode, SetNodeLabel,
// DeleteNodeLabel) that don't need anything beyond "read, mutate, write
// safely."
func (s *Scheduler) updateNode(nodeID string, mutate func(*models.Node)) (models.Node, error) {
	return s.updateNodeCAS(nodeID, mutate)
}

// maxCASConflictRetries bounds how many times updateNodeCAS will re-read
// and re-apply a mutation after losing a compare-and-swap race before
// giving up. Node records are small and writers are relatively few per
// node (heartbeat, reconciler failover, node monitor, operator actions), so
// a handful of retries is expected to be enough even under load; a caller
// that exhausts this is almost certainly hitting sustained concurrent
// writes to the same node and should surface that as an error rather than
// retry forever.
const maxCASConflictRetries = 5

// updateNodeCAS reads a node record, applies mutate, and writes it back
// using an etcd compare-and-swap on ModRevision, re-reading and re-applying
// the mutation if another writer updated the record in between (rather than
// silently overwriting whatever that writer just changed). This closes the
// lost-update window that a plain get-then-put has whenever more than one
// goroutine can touch the same node record around the same time — which is
// routine here: heartbeats, the parallelized reconciler's failover path,
// and the node monitor can all race on the same NotReady/Ready node.
func (s *Scheduler) updateNodeCAS(nodeID string, mutate func(*models.Node)) (models.Node, error) {
	if err := s.requireWritable(); err != nil {
		return models.Node{}, err
	}
	key := "/nodes/" + nodeID
	var lastConflictErr error
	for attempt := 0; attempt < maxCASConflictRetries; attempt++ {
		resp, err := s.RetryableEtcdGet(key)
		if err != nil {
			return models.Node{}, fmt.Errorf("failed to get node %s from etcd: %w", nodeID, err)
		}
		if resp == nil || len(resp.Kvs) == 0 {
			return models.Node{}, fmt.Errorf("node %s not found", nodeID)
		}
		var node models.Node
		if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
			return models.Node{}, fmt.Errorf("failed to unmarshal node %s: %w", nodeID, err)
		}
		modRevision := resp.Kvs[0].ModRevision

		mutate(&node)
		payload, err := json.Marshal(node)
		if err != nil {
			return models.Node{}, fmt.Errorf("failed to marshal node %s: %w", nodeID, err)
		}

		ok, err := s.RetryableEtcdCASPut(key, string(payload), modRevision)
		if err != nil {
			return models.Node{}, fmt.Errorf("failed to persist node %s: %w", nodeID, err)
		}
		if !ok {
			lastConflictErr = fmt.Errorf("node %s changed concurrently (attempt %d)", nodeID, attempt+1)
			nodeLogger.WithFields(logrus.Fields{
				"node_id": nodeID,
				"attempt": attempt + 1,
			}).Debug("node CAS conflict, retrying with fresh read")
			continue
		}
		_ = s.RetryableEtcdPut(key+"/status", node.Status)
		s.cacheNode(node)
		return node, nil
	}
	return models.Node{}, fmt.Errorf("node %s: too many concurrent update conflicts: %w", nodeID, lastConflictErr)
}

func normalizeTaintEffect(effect string) string {
	effect = strings.TrimSpace(effect)
	if effect == "" {
		return "NoSchedule"
	}
	return effect
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func (s *Scheduler) MarkNodeNotReady(nodeID, reason string) error {
	return s.markNodeNotReady(nodeID, reason, "reconciler")
}

func (s *Scheduler) MarkNodeWorkloadTypeUnsupported(nodeID, workloadType, reason string) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	want := canonicalWorkloadType(workloadType)
	if want == "" {
		return nil
	}

	// Pre-check against a snapshot read so we can skip the write (and log)
	// entirely when this node's capability list already reflects the
	// rejection. updateNodeCAS below re-derives the same decision against
	// a fresh read, so a race here just costs one extra CAS attempt, never
	// a missed update.
	resp, err := s.RetryableEtcdGet("/nodes/" + nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node %s from etcd: %w", nodeID, err)
	}
	if resp == nil || len(resp.Kvs) == 0 {
		return fmt.Errorf("node %s not found", nodeID)
	}
	var snapshot models.Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &snapshot); err != nil {
		return fmt.Errorf("failed to unmarshal node %s: %w", nodeID, err)
	}
	if !workloadTypeCapabilityNeedsUpdate(snapshot, want) {
		return nil
	}

	var updatedCapabilities []string
	node, err := s.updateNodeCAS(nodeID, func(node *models.Node) {
		if !workloadTypeCapabilityNeedsUpdate(*node, want) {
			return
		}
		if len(node.SupportedWorkloadTypes) == 0 {
			// Capability list absent on legacy node records. Only apply strict downgrade for VM.
			node.SupportedWorkloadTypes = []string{"container", "compose"}
		} else {
			filtered := make([]string, 0, len(node.SupportedWorkloadTypes))
			for _, t := range node.SupportedWorkloadTypes {
				if canonicalWorkloadType(t) == want {
					continue
				}
				filtered = append(filtered, canonicalWorkloadType(t))
			}
			node.SupportedWorkloadTypes = filtered
		}
		node.StatusReason = fmt.Sprintf("runtime for %s unavailable on agent: %s", want, strings.TrimSpace(reason))
		node.StatusUpdatedBy = "reconciler"
		node.StatusUpdatedAt = time.Now().UTC()
		updatedCapabilities = node.SupportedWorkloadTypes
	})
	if err != nil {
		return fmt.Errorf("failed to persist workload type capability update for node %s: %w", nodeID, err)
	}
	if updatedCapabilities == nil {
		// mutate ran (possibly via a CAS retry) but found nothing left to
		// change on the freshest read.
		return nil
	}
	nodeLogger.WithFields(logrus.Fields{
		"node_id":    nodeID,
		"capability": node.SupportedWorkloadTypes,
	}).Info("updated node capabilities after runtime rejection")
	return nil
}

// workloadTypeCapabilityNeedsUpdate reports whether node's capability list
// still needs to be updated to reflect that want is unsupported.
func workloadTypeCapabilityNeedsUpdate(node models.Node, want string) bool {
	if len(node.SupportedWorkloadTypes) == 0 {
		return want == "vm"
	}
	for _, t := range node.SupportedWorkloadTypes {
		if canonicalWorkloadType(t) == want {
			return true
		}
	}
	return false
}

func (s *Scheduler) markNodeNotReady(nodeID, reason, source string) error {
	if err := s.requireWritable(); err != nil {
		return err
	}

	// Cheap pre-check to avoid a needless write (and etcd revision bump)
	// when the record already reflects this exact NotReady state. If this
	// races with a concurrent writer, the worst case is one extra CAS
	// write below — updateNodeCAS re-reads internally, so we never skip a
	// write that was actually needed.
	if resp, err := s.RetryableEtcdGet("/nodes/" + nodeID); err == nil && resp != nil && len(resp.Kvs) > 0 {
		var current models.Node
		if json.Unmarshal(resp.Kvs[0].Value, &current) == nil && strings.EqualFold(current.Status, "NotReady") {
			incomingReason := strings.TrimSpace(reason)
			if strings.TrimSpace(current.StatusReason) != "" &&
				strings.TrimSpace(current.StatusUpdatedBy) != "" &&
				!current.StatusUpdatedAt.IsZero() &&
				(incomingReason == "" || strings.EqualFold(strings.TrimSpace(current.StatusReason), incomingReason)) &&
				strings.EqualFold(strings.TrimSpace(current.StatusUpdatedBy), source) {
				return nil
			}
		}
	}

	wasReady := false
	_, err := s.updateNodeCAS(nodeID, func(node *models.Node) {
		wasReady = !strings.EqualFold(node.Status, "NotReady")
		node.Status = "NotReady"
		node.StatusReason = strings.TrimSpace(reason)
		if node.StatusReason == "" {
			node.StatusReason = "node marked NotReady"
		}
		node.StatusUpdatedBy = source
		node.StatusUpdatedAt = time.Now().UTC()
	})
	if err != nil {
		return fmt.Errorf("failed to persist NotReady for node %s: %w", nodeID, err)
	}
	if wasReady {
		s.emitEvent("NodeLost", "", nodeID, reason, map[string]interface{}{
			"status": "NotReady",
			"source": source,
		})
		nodeLogger.WithFields(logrus.Fields{
			"node_id": nodeID,
			"source":  source,
			"reason":  reason,
		}).Warn("marked node NotReady")
	}
	return nil
}
