package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	"github.com/sirupsen/logrus"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// Constants
const (
	etcdTimeout   = 5 * time.Second
	maxRetries    = 5
	retryWaitTime = 2 * time.Second
)

var schedulerLogger = logging.C("scheduler.core")

// Scheduler holds the state and configuration for the cluster scheduler.
type Scheduler struct {
	etcdClient *clientv3.Client
	domain     string
	monitor    *Monitor
	reconciler *Reconciler
	bgWG       sync.WaitGroup
}

// NewScheduler initializes the scheduler with an etcd client and configuration.
func NewScheduler() (*Scheduler, error) {
	// Get etcd endpoints from environment variable, with fallback
	etcdEndpoints := os.Getenv("ETCD_ENDPOINTS")
	if etcdEndpoints == "" {
		etcdEndpoints = "localhost:2379"
	}

	// Get domain from environment variable, with fallback
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "persys.local"
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split(etcdEndpoints, ","),
		DialTimeout: etcdTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %v", err)
	}

	// Verify etcd connection
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	if _, err := cli.Get(ctx, "/health"); err != nil {
		cli.Close()
		return nil, fmt.Errorf("failed to connect to etcd: %v", err)
	}

	scheduler := &Scheduler{etcdClient: cli, domain: domain}

	// Initialize monitor and reconciler
	scheduler.monitor = NewMonitor(scheduler)
	scheduler.reconciler = NewReconciler(scheduler, scheduler.monitor)

	return scheduler, nil
}

// Close shuts down the scheduler gracefully.
func (s *Scheduler) Close() error {
	if s.etcdClient != nil {
		return s.etcdClient.Close()
	}
	return nil
}

func (s *Scheduler) RegisterNode(node models.Node) error {
	if node.NodeID == "" || node.IPAddress == "" || node.AgentPort == 0 {
		return fmt.Errorf("nodeID, IPAddress, and AgentPort are required")
	}
	if node.TotalCPU <= 0 || node.TotalMemory <= 0 {
		return fmt.Errorf("totalCPU and totalMemory must be positive")
	}

	node.LastHeartbeat = time.Now()
	node.DomainName = node.NodeID + "." + s.domain
	if node.Status == "" {
		node.Status = "Ready"
	}
	node.StatusReason = "registered"
	node.StatusUpdatedBy = "register"
	node.StatusUpdatedAt = time.Now().UTC()
	if node.AvailableCPU == 0 {
		node.AvailableCPU = node.TotalCPU
	}
	if node.AvailableMemory == 0 {
		node.AvailableMemory = node.TotalMemory
	}
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}

	schedulerLogger.WithFields(logrus.Fields{
		"node_id":          node.NodeID,
		"endpoint":         node.AgentEndpoint,
		"status":           node.Status,
		"supported_types":  node.SupportedWorkloadTypes,
		"total_cpu":        node.TotalCPU,
		"total_memory_mb":  node.TotalMemory,
		"available_cpu":    node.AvailableCPU,
		"available_memory": node.AvailableMemory,
	}).Info("registering node")

	nodeJSON, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("failed to marshal node: %v", err)
	}

	if err := s.RetryableEtcdPut("/nodes/"+node.NodeID, string(nodeJSON)); err != nil {
		return fmt.Errorf("failed to register node %s: %v", node.NodeID, err)
	}
	_ = s.RetryableEtcdPut("/nodes/"+node.NodeID+"/status", node.Status)

	if err := s.UpdateCoreDNS(node); err != nil {
		schedulerLogger.WithError(err).WithField("node_id", node.NodeID).Warn("failed to update CoreDNS for node")
	}

	schedulerLogger.WithField("node_id", node.NodeID).Info("registered node")

	return nil
}

// matchesLabels checks if workload labels are a subset of node labels
func matchesLabels(workloadLabels, nodeLabels map[string]string) bool {
	if len(workloadLabels) == 0 {
		return true // No labels to match, accept node
	}
	for k, v := range workloadLabels {
		if nodeVal, ok := nodeLabels[k]; !ok || nodeVal != v {
			return false
		}
	}
	return true
}

func nodeUtilizationScore(node models.Node) float64 {
	cpuTotal := node.TotalCPU
	memTotal := float64(node.TotalMemory)
	if cpuTotal <= 0 || memTotal <= 0 {
		return 1e9
	}
	cpuUsed := cpuTotal - node.AvailableCPU
	memUsed := memTotal - float64(node.AvailableMemory)
	cpuRatio := cpuUsed / cpuTotal
	memRatio := memUsed / memTotal
	return (cpuRatio + memRatio) / 2.0
}

func canonicalWorkloadType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "docker-container", "container":
		return "container"
	case "docker-compose", "compose":
		return "compose"
	case "vm":
		return "vm"
	default:
		return strings.ToLower(strings.TrimSpace(t))
	}
}

func nodeSupportsWorkloadType(node models.Node, workloadType string) bool {
	want := canonicalWorkloadType(workloadType)
	if want == "" {
		return true
	}

	if len(node.SupportedWorkloadTypes) > 0 {
		for _, t := range node.SupportedWorkloadTypes {
			if canonicalWorkloadType(t) == want {
				return true
			}
		}
		return false
	}

	// Backward compatibility for nodes registered before capability wiring.
	// VM scheduling is strict: require explicit hypervisor signal when capability list is absent.
	if want == "vm" {
		return strings.TrimSpace(node.Hypervisor.Type) != "" ||
			strings.EqualFold(node.Hypervisor.Status, "active") ||
			strings.EqualFold(node.Hypervisor.Status, "ready")
	}
	return true
}

func isNodeStatusSubKey(key string) bool {
	return strings.HasSuffix(key, "/status")
}

func (s *Scheduler) selectNodeForWorkload(workload models.Workload) (models.Node, string, error) {
	resp, err := s.RetryableEtcdGet(nodesPrefix, clientv3.WithPrefix())
	if err != nil {
		return models.Node{}, "", fmt.Errorf("failed to get nodes for scheduling: %v", err)
	}
	if len(resp.Kvs) == 0 {
		return models.Node{}, "", fmt.Errorf("no nodes available")
	}

	candidates := make([]models.Node, 0)
	rejections := make([]string, 0)
	for _, kv := range resp.Kvs {
		if isNodeStatusSubKey(string(kv.Key)) {
			continue
		}
		var node models.Node
		if err := json.Unmarshal(kv.Value, &node); err != nil {
			schedulerLogger.WithError(err).WithField("key", string(kv.Key)).Warn("failed to unmarshal node data")
			continue
		}
		if !strings.EqualFold(node.Status, "active") && !strings.EqualFold(node.Status, "ready") {
			reason := strings.TrimSpace(node.StatusReason)
			if reason == "" {
				reason = "unspecified"
			}
			rejections = append(rejections, fmt.Sprintf("%s: status=%s reason=%q by=%s at=%s", node.NodeID, node.Status, reason, node.StatusUpdatedBy, node.StatusUpdatedAt.UTC().Format(time.RFC3339)))
			continue
		}
		if time.Since(node.LastHeartbeat) > 10*time.Minute {
			rejections = append(rejections, fmt.Sprintf("%s: heartbeat_stale last=%s", node.NodeID, node.LastHeartbeat.UTC().Format(time.RFC3339)))
			continue
		}
		if !matchesLabels(workload.Labels, node.Labels) {
			rejections = append(rejections, fmt.Sprintf("%s: label_mismatch", node.NodeID))
			continue
		}
		if !nodeSupportsWorkloadType(node, workload.Type) {
			rejections = append(rejections, fmt.Sprintf("%s: workload_type_unsupported need=%s supports=%v", node.NodeID, canonicalWorkloadType(workload.Type), node.SupportedWorkloadTypes))
			continue
		}
		if workload.Resources.CPUUsage > 0 && node.AvailableCPU < workload.Resources.CPUUsage {
			rejections = append(rejections, fmt.Sprintf("%s: cpu_insufficient need=%.3f have=%.3f", node.NodeID, workload.Resources.CPUUsage, node.AvailableCPU))
			continue
		}
		if workload.Resources.MemoryUsage > 0 && float64(node.AvailableMemory) < workload.Resources.MemoryUsage {
			rejections = append(rejections, fmt.Sprintf("%s: memory_insufficient need=%.0fMB have=%dMB", node.NodeID, workload.Resources.MemoryUsage, node.AvailableMemory))
			continue
		}
		if workload.Resources.DiskUsage >= 0 {
			availableDisk := int(node.TotalMemory - node.AvailableMemory) // placeholder until explicit disk tracking exists on node model
			_ = availableDisk
		}
		candidates = append(candidates, node)
	}
	if len(candidates) == 0 {
		if len(rejections) > 0 {
			return models.Node{}, "", fmt.Errorf("no suitable node available (%s)", strings.Join(rejections, "; "))
		}
		return models.Node{}, "", fmt.Errorf("no suitable node available")
	}

	sort.Slice(candidates, func(i, j int) bool {
		return nodeUtilizationScore(candidates[i]) < nodeUtilizationScore(candidates[j])
	})

	reason := fmt.Sprintf("selected by lowest utilization score %.4f", nodeUtilizationScore(candidates[0]))
	return candidates[0], reason, nil
}

func (s *Scheduler) assignWorkload(workload *models.Workload, node models.Node, reason string) error {
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	workload.NodeID = node.NodeID
	workload.AssignedNode = node.NodeID
	workload.Status = "Scheduled"
	workload.StatusInfo.ActualState = "Pending"
	workload.StatusInfo.LastUpdated = time.Now().UTC()
	workload.Metadata["last_action"] = "Assigned"
	workload.Metadata["assignment_reason"] = reason

	if err := s.saveWorkload(*workload); err != nil {
		return err
	}
	if err := s.writeAssignment(workload.ID, node.NodeID, reason); err != nil {
		return err
	}
	s.emitEvent("WorkloadScheduled", workload.ID, node.NodeID, reason, nil)
	return nil
}

// ScheduleWorkload assigns a workload to a suitable node and sends a command via the agent API.
func (s *Scheduler) ScheduleWorkload(workload models.Workload) (string, error) {
	if workload.ID == "" {
		workload.ID = uuid.New().String()
		schedulerLogger.WithField("workload_id", workload.ID).Debug("generated workload ID")
	}
	s.ensureWorkloadRevision(&workload)
	s.initializeWorkloadDefaults(&workload)

	selectedNode, reason, err := s.selectNodeForWorkload(workload)
	if err != nil {
		s.emitEvent("WorkloadFailed", workload.ID, "", err.Error(), nil)
		return "", err
	}

	if err := s.assignWorkload(&workload, selectedNode, reason); err != nil {
		return "", fmt.Errorf("failed assigning workload %s: %w", workload.ID, err)
	}

	applyResp, err := s.applyWorkloadOnNode(context.Background(), selectedNode, workload)
	if err != nil {
		_ = s.UpdateWorkloadStatus(workload.ID, "Failed")
		_ = s.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("ApplyWorkload failed: %v", err))
		s.emitEvent("WorkloadFailed", workload.ID, selectedNode.NodeID, err.Error(), nil)
		return "", fmt.Errorf("apply workload on node %s: %v", selectedNode.NodeID, err)
	}
	if applyResp == nil || (!applyResp.GetApplied() && !applyResp.GetSkipped()) {
		msg := "agent rejected apply request"
		if applyResp != nil && strings.TrimSpace(applyResp.GetMessage()) != "" {
			msg = applyResp.GetMessage()
		}
		_ = s.UpdateWorkloadStatus(workload.ID, "Failed")
		_ = s.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("ApplyWorkload rejected: %s", msg))
		s.emitEvent("WorkloadFailed", workload.ID, selectedNode.NodeID, msg, nil)
		return "", fmt.Errorf("apply workload on node %s rejected: %s", selectedNode.NodeID, msg)
	}

	if applyResp != nil && applyResp.Status != nil {
		_ = s.UpdateWorkloadStatus(workload.ID, mapActualStateToSchedulerStatus(applyResp.Status.ActualState))
	}
	if applyResp != nil {
		_ = s.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Apply response: applied=%t skipped=%t message=%s", applyResp.Applied, applyResp.Skipped, applyResp.Message))
	}

	terminalStatus, actions, waitErr := s.waitForWorkloadTerminalStatus(context.Background(), selectedNode, workload, s.applyTimeoutFor(workload))
	if waitErr != nil {
		_ = s.UpdateWorkloadStatus(workload.ID, "Unknown")
		_ = s.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Timed out waiting for terminal state: %v", waitErr))
		if len(actions) > 0 {
			_ = s.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Recent actions: %d action(s) captured", len(actions)))
		}
		return "", waitErr
	}

	if terminalStatus != nil {
		_ = s.UpdateWorkloadStatus(workload.ID, mapActualStateToSchedulerStatus(terminalStatus.GetActualState()))
		if terminalStatus.GetMessage() != "" {
			_ = s.UpdateWorkloadLogs(workload.ID, terminalStatus.GetMessage())
		}
	}
	s.writeReconciliationRecord(workload.ID, "Apply", true, "Converged")

	schedulerLogger.WithFields(logrus.Fields{
		"workload_id": workload.ID,
		"node_id":     selectedNode.NodeID,
	}).Info("workload converged")
	return selectedNode.NodeID, nil
}

// GetNodes retrieves all nodes from etcd.
func (s *Scheduler) GetNodes() ([]models.Node, error) {
	resp, err := s.RetryableEtcdGet("/nodes/", clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %v", err)
	}

	nodes := make([]models.Node, 0)
	if resp == nil {
		return nodes, nil
	}

	for _, kv := range resp.Kvs {
		if isNodeStatusSubKey(string(kv.Key)) {
			continue
		}
		var node models.Node
		if err := json.Unmarshal(kv.Value, &node); err != nil {
			schedulerLogger.WithError(err).WithField("key", string(kv.Key)).Warn("failed to unmarshal node data")
			continue
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// GetNodeByID retrieves a specific node by ID.
func (s *Scheduler) GetNodeByID(nodeID string) (models.Node, error) {
	resp, err := s.RetryableEtcdGet("/nodes/" + nodeID)
	if err != nil {
		return models.Node{}, fmt.Errorf("failed to get node %s: %v", nodeID, err)
	}

	if len(resp.Kvs) == 0 {
		return models.Node{}, fmt.Errorf("node %s not found", nodeID)
	}

	var node models.Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
		return models.Node{}, fmt.Errorf("failed to unmarshal node %s: %v", nodeID, err)
	}

	return node, nil
}

// DeleteNode removes a node from etcd and CoreDNS.
func (s *Scheduler) DeleteNode(nodeID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	_, err := s.etcdClient.Delete(ctx, "/nodes/"+nodeID)
	if err != nil {
		return fmt.Errorf("failed to delete node %s: %v", nodeID, err)
	}
	_, _ = s.etcdClient.Delete(ctx, "/nodes/"+nodeID+"/status")

	// Remove from CoreDNS
	key := fmt.Sprintf("/skydns/%s/%s", reverseDomain(s.domain), nodeID)
	_, err = s.etcdClient.Delete(ctx, key)
	if err != nil {
		schedulerLogger.WithError(err).WithField("node_id", nodeID).Warn("failed to remove CoreDNS entry")
	}

	schedulerLogger.WithField("node_id", nodeID).Info("deleted node")
	return nil
}

// GetWorkloads retrieves all workloads from etcd.
func (s *Scheduler) GetWorkloads() ([]models.Workload, error) {
	resp, err := s.RetryableEtcdGet("/workloads/", clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get workloads: %v", err)
	}

	workloads := make([]models.Workload, 0)
	if resp == nil {
		return workloads, nil
	}

	for _, kv := range resp.Kvs {
		var workload models.Workload
		if err := json.Unmarshal(kv.Value, &workload); err != nil {
			schedulerLogger.WithError(err).WithField("key", string(kv.Key)).Warn("failed to unmarshal workload data")
			continue
		}
		workloads = append(workloads, workload)
	}

	return workloads, nil
}

// GetWorkloadByID retrieves a specific workload by ID.
func (s *Scheduler) GetWorkloadByID(workloadID string) (models.Workload, error) {
	resp, err := s.RetryableEtcdGet("/workloads/" + workloadID)
	if err != nil {
		return models.Workload{}, fmt.Errorf("failed to get workload %s: %v", workloadID, err)
	}

	if len(resp.Kvs) == 0 {
		return models.Workload{}, fmt.Errorf("workload %s not found", workloadID)
	}

	var workload models.Workload
	if err := json.Unmarshal(resp.Kvs[0].Value, &workload); err != nil {
		return models.Workload{}, fmt.Errorf("failed to unmarshal workload %s: %v", workloadID, err)
	}

	return workload, nil
}

// DeleteWorkload removes a workload from etcd.
func (s *Scheduler) DeleteWorkloadWithContext(ctx context.Context, workloadID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	workload, err := s.GetWorkloadByID(workloadID)
	if err == nil && workload.NodeID != "" {
		node, nodeErr := s.GetNodeByID(workload.NodeID)
		if nodeErr == nil {
			if _, delErr := s.deleteWorkloadFromNode(ctx, node, workloadID); delErr != nil {
				return fmt.Errorf("delete workload on node %s: %v", node.NodeID, delErr)
			}

			deadline := time.Now().Add(s.deleteTimeout())
			for {
				_, stErr := s.getWorkloadStatusFromNode(ctx, node, workloadID)
				if stErr != nil && isWorkloadStatusNotFound(stErr) {
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("timeout waiting for workload %s deletion on node %s", workloadID, node.NodeID)
				}
				time.Sleep(s.agentPollInterval())
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	_, err = s.etcdClient.Delete(ctx, "/workloads/"+workloadID)
	if err != nil {
		return fmt.Errorf("failed to delete workload %s: %v", workloadID, err)
	}
	_, _ = s.etcdClient.Delete(ctx, assignmentKey(workloadID))
	_, _ = s.etcdClient.Delete(ctx, retryKey(workloadID))
	_, _ = s.etcdClient.Delete(ctx, reconciliationKey(workloadID))
	s.emitEvent("Rescheduled", workloadID, "", "Workload state removed", nil)
	schedulerLogger.WithField("workload_id", workloadID).Info("deleted workload")
	return nil
}

// DeleteWorkload removes a workload from etcd.
func (s *Scheduler) DeleteWorkload(workloadID string) error {
	return s.DeleteWorkloadWithContext(context.Background(), workloadID)
}

// UpdateWorkloadStatus updates the status of a workload.
func (s *Scheduler) UpdateWorkloadStatus(workloadID, status string) error {
	workload, err := s.GetWorkloadByID(workloadID)
	if err != nil {
		return err
	}

	workload.Status = status
	workload.StatusInfo.ActualState = status
	workload.StatusInfo.LastUpdated = time.Now().UTC()
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	if strings.EqualFold(status, "failed") {
		workload.Metadata["last_action"] = "Failed"
	}
	if err := s.saveWorkload(workload); err != nil {
		return fmt.Errorf("failed to update workload %s status: %v", workloadID, err)
	}

	schedulerLogger.WithFields(logrus.Fields{
		"workload_id": workloadID,
		"status":      status,
	}).Debug("updated workload status")
	return nil
}

// UpdateWorkloadLogs updates the logs of a workload.
func (s *Scheduler) UpdateWorkloadLogs(workloadID, logs string) error {
	workload, err := s.GetWorkloadByID(workloadID)
	if err != nil {
		return err
	}

	// Append logs with timestamp
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logEntry := fmt.Sprintf("[%s] %s\n", timestamp, logs)

	if workload.Logs == "" {
		workload.Logs = logEntry
	} else {
		workload.Logs += logEntry
	}
	workload.StatusInfo.LastUpdated = time.Now().UTC()
	if err := s.saveWorkload(workload); err != nil {
		return fmt.Errorf("failed to update workload %s logs: %v", workloadID, err)
	}

	schedulerLogger.WithField("workload_id", workloadID).Debug("updated workload logs")
	return nil
}

// GetWorkloadsByNode retrieves all workloads assigned to a specific node.
func (s *Scheduler) GetWorkloadsByNode(nodeID string) ([]models.Workload, error) {
	workloads, err := s.GetWorkloads()
	if err != nil {
		return nil, err
	}

	nodeWorkloads := make([]models.Workload, 0)
	for _, workload := range workloads {
		if workload.NodeID == nodeID {
			nodeWorkloads = append(nodeWorkloads, workload)
		}
	}

	return nodeWorkloads, nil
}

// MonitorNodes periodically checks node health and updates status.
func (s *Scheduler) MonitorNodes(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			schedulerLogger.Info("stopping node monitoring")
			return
		case <-ticker.C:
			nodes, err := s.GetNodes()
			if err != nil {
				schedulerLogger.WithError(err).Error("node monitoring cycle failed")
				continue
			}
			for _, node := range nodes {
				if time.Since(node.LastHeartbeat) > 3*time.Minute {
					reason := fmt.Sprintf("heartbeat expired: last heartbeat %s", node.LastHeartbeat.UTC().Format(time.RFC3339))
					if err := s.markNodeNotReady(node.NodeID, reason, "monitor"); err != nil {
						schedulerLogger.WithError(err).WithField("node_id", node.NodeID).Warn("failed to update node status")
					}
				}
			}
		}
	}
}

// StartMonitoring starts both node monitoring and workload monitoring
func (s *Scheduler) StartMonitoring(ctx context.Context) {
	// Start node monitoring
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		s.MonitorNodes(ctx)
	}()

	// Start workload monitoring
	if s.monitor != nil {
		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			s.monitor.MonitorWorkloads(ctx, 60*time.Second)
		}()
	}
}

// StartReconciliation starts the reconciliation loop
func (s *Scheduler) StartReconciliation(ctx context.Context) {
	if s.reconciler != nil {
		interval := schedulerDurationEnv("SCHEDULER_RECONCILE_INTERVAL", 5*time.Second)
		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			s.reconciler.StartReconciliationLoop(ctx, interval)
		}()
	}
}

// WaitForBackground blocks until scheduler background workers stop or timeout elapses.
func (s *Scheduler) WaitForBackground(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		s.bgWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// GetReconciliationStats returns reconciliation statistics
func (s *Scheduler) GetReconciliationStats() (map[string]interface{}, error) {
	if s.reconciler != nil {
		return s.reconciler.GetReconciliationStats()
	}
	return nil, fmt.Errorf("reconciler not initialized")
}

// ReconcileAllWorkloads performs reconciliation on all workloads
func (s *Scheduler) ReconcileAllWorkloads() ([]*ReconciliationResult, error) {
	if s.reconciler != nil {
		return s.reconciler.ReconcileAllWorkloads(context.Background())
	}
	return nil, fmt.Errorf("reconciler not initialized")
}

// ReconcileWorkload performs reconciliation on a specific workload
func (s *Scheduler) ReconcileWorkload(workload models.Workload) (*ReconciliationResult, error) {
	return s.ReconcileWorkloadWithContext(context.Background(), workload)
}

// ReconcileWorkloadWithContext performs reconciliation with caller context propagation.
func (s *Scheduler) ReconcileWorkloadWithContext(ctx context.Context, workload models.Workload) (*ReconciliationResult, error) {
	if s.reconciler != nil {
		return s.reconciler.ReconcileWorkload(ctx, workload)
	}
	return nil, fmt.Errorf("reconciler not initialized")
}
