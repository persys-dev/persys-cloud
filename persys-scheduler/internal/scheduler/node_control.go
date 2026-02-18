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
	resp, err := s.RetryableEtcdGet("/nodes/" + nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node %s from etcd: %w", nodeID, err)
	}
	if resp == nil || len(resp.Kvs) == 0 {
		return fmt.Errorf("node %s not found", nodeID)
	}

	var node models.Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
		return fmt.Errorf("failed to unmarshal node %s: %w", nodeID, err)
	}

	node.LastHeartbeat = time.Now().UTC()
	if strings.TrimSpace(status) != "" {
		previousStatus := node.Status
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

	updatedNodeJSON, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("failed to marshal node %s: %w", nodeID, err)
	}
	if err := s.RetryableEtcdPut("/nodes/"+nodeID, string(updatedNodeJSON)); err != nil {
		return fmt.Errorf("failed to update node %s heartbeat: %w", nodeID, err)
	}
	_ = s.RetryableEtcdPut("/nodes/"+nodeID+"/status", node.Status)
	return nil
}

func (s *Scheduler) MarkNodeNotReady(nodeID, reason string) error {
	return s.markNodeNotReady(nodeID, reason, "reconciler")
}

func (s *Scheduler) MarkNodeWorkloadTypeUnsupported(nodeID, workloadType, reason string) error {
	resp, err := s.RetryableEtcdGet("/nodes/" + nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node %s from etcd: %w", nodeID, err)
	}
	if resp == nil || len(resp.Kvs) == 0 {
		return fmt.Errorf("node %s not found", nodeID)
	}

	var node models.Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
		return fmt.Errorf("failed to unmarshal node %s: %w", nodeID, err)
	}

	want := canonicalWorkloadType(workloadType)
	if want == "" {
		return nil
	}

	if len(node.SupportedWorkloadTypes) == 0 {
		// Capability list absent on legacy node records. Only apply strict downgrade for VM.
		if want == "vm" {
			node.SupportedWorkloadTypes = []string{"container", "compose"}
		} else {
			return nil
		}
	} else {
		filtered := make([]string, 0, len(node.SupportedWorkloadTypes))
		changed := false
		for _, t := range node.SupportedWorkloadTypes {
			if canonicalWorkloadType(t) == want {
				changed = true
				continue
			}
			filtered = append(filtered, canonicalWorkloadType(t))
		}
		if !changed {
			return nil
		}
		node.SupportedWorkloadTypes = filtered
	}

	node.StatusReason = fmt.Sprintf("runtime for %s unavailable on agent: %s", want, strings.TrimSpace(reason))
	node.StatusUpdatedBy = "reconciler"
	node.StatusUpdatedAt = time.Now().UTC()

	payload, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("failed to marshal node %s: %w", nodeID, err)
	}
	if err := s.RetryableEtcdPut("/nodes/"+nodeID, string(payload)); err != nil {
		return fmt.Errorf("failed to persist workload type capability update for node %s: %w", nodeID, err)
	}
	nodeLogger.WithFields(logrus.Fields{
		"node_id":    nodeID,
		"capability": node.SupportedWorkloadTypes,
	}).Info("updated node capabilities after runtime rejection")
	return nil
}

func (s *Scheduler) markNodeNotReady(nodeID, reason, source string) error {
	resp, err := s.RetryableEtcdGet("/nodes/" + nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node %s from etcd: %w", nodeID, err)
	}
	if resp == nil || len(resp.Kvs) == 0 {
		return fmt.Errorf("node %s not found", nodeID)
	}

	var node models.Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
		return fmt.Errorf("failed to unmarshal node %s: %w", nodeID, err)
	}

	if strings.EqualFold(node.Status, "NotReady") {
		incomingReason := strings.TrimSpace(reason)
		// Keep current record if metadata is complete and reason/source did not change.
		if strings.TrimSpace(node.StatusReason) != "" &&
			strings.TrimSpace(node.StatusUpdatedBy) != "" &&
			!node.StatusUpdatedAt.IsZero() &&
			(incomingReason == "" || strings.EqualFold(strings.TrimSpace(node.StatusReason), incomingReason)) &&
			strings.EqualFold(strings.TrimSpace(node.StatusUpdatedBy), source) {
			return nil
		}
		node.StatusReason = incomingReason
		if node.StatusReason == "" {
			node.StatusReason = "node marked NotReady"
		}
		node.StatusUpdatedBy = source
		node.StatusUpdatedAt = time.Now().UTC()
		payload, err := json.Marshal(node)
		if err != nil {
			return fmt.Errorf("failed to marshal node %s: %w", nodeID, err)
		}
		if err := s.RetryableEtcdPut("/nodes/"+nodeID, string(payload)); err != nil {
			return fmt.Errorf("failed to persist NotReady metadata for node %s: %w", nodeID, err)
		}
		_ = s.RetryableEtcdPut("/nodes/"+nodeID+"/status", node.Status)
		return nil
	}
	node.Status = "NotReady"
	node.StatusReason = strings.TrimSpace(reason)
	if node.StatusReason == "" {
		node.StatusReason = "node marked NotReady"
	}
	node.StatusUpdatedBy = source
	node.StatusUpdatedAt = time.Now().UTC()
	payload, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("failed to marshal node %s: %w", nodeID, err)
	}
	if err := s.RetryableEtcdPut("/nodes/"+nodeID, string(payload)); err != nil {
		return fmt.Errorf("failed to persist NotReady for node %s: %w", nodeID, err)
	}
	_ = s.RetryableEtcdPut("/nodes/"+nodeID+"/status", node.Status)
	s.emitEvent("NodeLost", "", nodeID, reason, nil)
	nodeLogger.WithFields(logrus.Fields{
		"node_id": nodeID,
		"source":  source,
		"reason":  reason,
	}).Warn("marked node NotReady")
	return nil
}
