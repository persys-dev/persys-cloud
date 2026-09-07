package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	cfgpkg "github.com/persys-dev/persys-cloud/persys-scheduler/internal/config"
	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
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
	cfg                 *cfgpkg.Config
	etcdClient          *clientv3.Client
	redisClient         *redis.Client
	domain              string
	agentsDomain        string
	schedulerShard      string
	monitor             *Monitor
	reconciler          *Reconciler
	bgWG                sync.WaitGroup
	modeMu              sync.RWMutex
	mode                OperatingMode
	modeReasonText      string
	modeChangedAt       time.Time
	frozen              *FrozenState
	cacheMu             sync.RWMutex
	cacheNodes          map[string]models.Node
	cacheWorkloads      map[string]models.Workload
	cacheAssignments    map[string]models.AssignmentRecord
	agentConnMu         sync.Mutex
	agentConns          map[string]*agentConnEntry
	certMgr             *certmanager.Manager // optional; enables ForceRotate on agent TLS failures
	nodeCacheReady      atomic.Bool
	isLeader            atomic.Bool
	instanceID          string
	pendingMu           sync.Mutex
	pendingReservations map[string]map[string]pendingReservation
}

// NewScheduler initializes the scheduler with an etcd client and configuration.
func NewScheduler(cfg *cfgpkg.Config) (*Scheduler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("scheduler config cannot be nil")
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.EtcdEndpoints,
		DialTimeout: etcdTimeout,
		Logger:      zap.NewNop(),
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

	scheduler := &Scheduler{
		cfg:              cfg,
		etcdClient:       cli,
		domain:           cfg.Domain,
		agentsDomain:     cfg.AgentsDiscoveryDomain,
		schedulerShard:   cfg.SchedulerShardKey,
		mode:             ModeNormal,
		modeReasonText:   "startup",
		modeChangedAt:    time.Now().UTC(),
		cacheNodes:       map[string]models.Node{},
		cacheWorkloads:   map[string]models.Workload{},
		cacheAssignments: map[string]models.AssignmentRecord{},
		agentConns:       map[string]*agentConnEntry{},
		instanceID:       newInstanceID(),
	}

	// Initialize monitor and reconciler
	scheduler.initRedisStore()
	scheduler.monitor = NewMonitor(scheduler)
	scheduler.reconciler = NewReconciler(scheduler, scheduler.monitor)

	return scheduler, nil
}


// SetCertManager attaches the process-wide certmanager so outbound agent
// dials can ForceRotate + retry on TLS handshake failures. Safe to call
// once after NewScheduler; nil is allowed (disables cert-aware retry).
func (s *Scheduler) SetCertManager(m *certmanager.Manager) {
	s.certMgr = m
}

// Close shuts down the scheduler gracefully.
func (s *Scheduler) Close() error {
	s.closeAgentConns()
	s.DeregisterSchedulerSelfFromCoreDNS()
	if s.etcdClient != nil {
		_ = s.etcdClient.Close()
	}
	if s.redisClient != nil {
		_ = s.redisClient.Close()
	}
	return nil
}

func (s *Scheduler) RegisterNode(node models.Node) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	if node.NodeID == "" || node.IPAddress == "" || node.AgentPort == 0 {
		return fmt.Errorf("nodeID, IPAddress, and AgentPort are required")
	}
	if node.TotalCPU <= 0 || node.TotalMemory <= 0 {
		return fmt.Errorf("totalCPU and totalMemory must be positive")
	}

	// Agent-advertised labels from this registration request.
	agentLabels := node.Labels
	if agentLabels == nil {
		agentLabels = map[string]string{}
	}

	// Load existing etcd record so operator-managed fields survive reconnect.
	// Without this merge, RegisterNode does a full put and drops SetNodeLabel keys
	// (UI / persysctl → SetNodeLabel → etcd), which is what agents see vanish after
	// reconnect / re-register.
	var existing *models.Node
	if prev, err := s.GetNodeByID(node.NodeID); err == nil {
		existing = &prev
	}

	node.LastHeartbeat = time.Now()
	node.DomainName = node.NodeID + "." + s.domain

	// Preserve operator drain/taint state across re-registration.
	if existing != nil {
		if strings.EqualFold(existing.Status, "Draining") || strings.EqualFold(existing.Status, "Drained") {
			node.Status = existing.Status
			node.StatusReason = existing.StatusReason
			node.StatusUpdatedBy = existing.StatusUpdatedBy
			node.StatusUpdatedAt = existing.StatusUpdatedAt
		}
		if len(existing.Taints) > 0 {
			node.Taints = existing.Taints
		}
	}
	if node.Status == "" {
		node.Status = "Ready"
	}
	if existing == nil || (!strings.EqualFold(node.Status, "Draining") && !strings.EqualFold(node.Status, "Drained")) {
		if node.StatusReason == "" {
			node.StatusReason = "registered"
		}
		if node.StatusUpdatedBy == "" {
			node.StatusUpdatedBy = "register"
		}
		node.StatusUpdatedAt = time.Now().UTC()
	}
	if node.AvailableCPU == 0 {
		node.AvailableCPU = node.TotalCPU
	}
	if node.AvailableMemory == 0 {
		node.AvailableMemory = node.TotalMemory
	}

	// Label merge:
	//   1) start with existing etcd labels (includes operator SetNodeLabel keys)
	//   2) overlay agent-reported labels (agent is source of truth for keys it sends)
	//   3) always stamp scheduler_shard
	// Keys only present on the operator side are preserved when the agent omits them.
	merged := make(map[string]string)
	if existing != nil && existing.Labels != nil {
		for k, v := range existing.Labels {
			merged[k] = v
		}
	}
	for k, v := range agentLabels {
		merged[k] = v
	}
	merged["scheduler_shard"] = s.schedulerShard
	node.Labels = merged

	schedulerLogger.WithFields(logrus.Fields{
		"node_id":           node.NodeID,
		"endpoint":          node.AgentEndpoint,
		"status":            node.Status,
		"scheduler_shard":   s.cfg.SchedulerShardKey,
		"supported_types":   node.SupportedWorkloadTypes,
		"supported_storage": node.SupportedStorageDrivers,
		"total_cpu":         node.TotalCPU,
		"total_memory_mb":   node.TotalMemory,
		"available_cpu":     node.AvailableCPU,
		"available_memory":  node.AvailableMemory,
		"label_count":       len(node.Labels),
		"re_register":       existing != nil,
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
	} else {
		schedulerLogger.WithField("node_id", node.NodeID).Info("updated CoreDNS record for node")
	}
	s.cacheNode(node)
	s.emitEvent("NodeJoined", "", node.NodeID, "node registered", map[string]interface{}{
		"status":          node.Status,
		"agent_endpoint":  node.AgentEndpoint,
		"scheduler_shard": node.Labels["scheduler_shard"],
	})

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

func canonicalWorkloadType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "docker-container", "container":
		return "container"
	case "docker-compose", "compose":
		return "compose"
	case "vm":
		return "vm"
	case "microvm", "micro-vm", "firecracker":
		return "microvm"
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

func requiredStorageDrivers(workload models.Workload) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	add := func(driver string) {
		canon := strings.ToLower(strings.TrimSpace(driver))
		switch canon {
		case "":
			return
		case "ceph_rbd":
			canon = "ceph-rbd"
		}
		if _, ok := seen[canon]; ok {
			return
		}
		seen[canon] = struct{}{}
		out = append(out, canon)
	}
	for _, mv := range workload.ManagedVolumes {
		add(mv.Driver)
	}
	if workload.VM != nil {
		for _, mv := range workload.VM.ManagedVolumes {
			add(mv.Driver)
		}
	}
	return out
}

func nodeSupportsStorageDrivers(node models.Node, required []string) bool {
	if len(required) == 0 {
		return true
	}
	supported := make(map[string]struct{}, len(node.SupportedStorageDrivers)+1)
	supported["local"] = struct{}{}
	for _, driver := range node.SupportedStorageDrivers {
		canon := strings.ToLower(strings.TrimSpace(driver))
		if canon == "" {
			continue
		}
		supported[canon] = struct{}{}
	}
	for k, v := range node.Labels {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(k)), "storage.") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(v), "true") {
			continue
		}
		driver := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(k)), "storage.")
		switch driver {
		case "ceph_rbd":
			driver = "ceph-rbd"
		}
		if driver != "" {
			supported[driver] = struct{}{}
		}
	}

	for _, needed := range required {
		if _, ok := supported[strings.ToLower(strings.TrimSpace(needed))]; !ok {
			return false
		}
	}
	return true
}

func isNodeStatusSubKey(key string) bool {
	return strings.HasSuffix(key, "/status")
}

// candidateNodeSnapshot returns the node set placement decisions are made
// against. When the watch-backed node cache (node_watch.go) is populated
// and healthy, it's served from there — an in-memory map read instead of
// an etcd round-trip plus a fresh unmarshal of every node on every single
// placement decision. If the cache isn't ready yet (scheduler just
// started, or the watch stream is down and hasn't resynced), this falls
// back transparently to the original live etcd prefix scan, so placement
// never silently runs against stale or empty data.
func (s *Scheduler) candidateNodeSnapshot() ([]models.Node, error) {
	if s.nodeCacheReady.Load() {
		if nodes := s.getCachedNodes(); len(nodes) > 0 {
			return nodes, nil
		}
	}
	resp, err := s.RetryableEtcdGet(nodesPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes for scheduling: %v", err)
	}
	nodes := make([]models.Node, 0, len(resp.Kvs))
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

func (s *Scheduler) selectNodeForWorkload(workload models.Workload) (models.Node, string, error) {
	if !s.isWritable() {
		return models.Node{}, "", errControlPlaneFrozen
	}
	nodes, err := s.candidateNodeSnapshot()
	if err != nil {
		return models.Node{}, "", err
	}
	if len(nodes) == 0 {
		return models.Node{}, "", fmt.Errorf("no nodes available")
	}

	candidates := make([]models.Node, 0)
	rejections := make([]string, 0)
	neededStorageDrivers := requiredStorageDrivers(workload)
	for _, node := range nodes {
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
		if blocksScheduling(node.Taints) {
			rejections = append(rejections, fmt.Sprintf("%s: tainted taints=%v", node.NodeID, node.Taints))
			continue
		}
		if !nodeSupportsWorkloadType(node, workload.Type) {
			rejections = append(rejections, fmt.Sprintf("%s: workload_type_unsupported need=%s supports=%v", node.NodeID, canonicalWorkloadType(workload.Type), node.SupportedWorkloadTypes))
			continue
		}
		if !nodeSupportsStorageDrivers(node, neededStorageDrivers) {
			rejections = append(rejections, fmt.Sprintf("%s: storage_driver_unsupported need=%v supports=%v", node.NodeID, neededStorageDrivers, node.SupportedStorageDrivers))
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

	counts, maxCount, err := s.workloadCountsByNode()
	if err != nil {
		// Non-fatal: fall back to treating spread as uniform (score
		// degrades gracefully to the CPU/memory terms only) rather than
		// failing the whole placement decision over a monitoring-adjacent
		// read.
		schedulerLogger.WithError(err).Warn("failed to compute workload counts for placement spread scoring; continuing without it")
		counts = map[string]int{}
		maxCount = 0
	}

	sort.Slice(candidates, func(i, j int) bool {
		pendingCPUI, pendingMemI := s.pendingReservationFor(candidates[i].NodeID)
		pendingCPUJ, pendingMemJ := s.pendingReservationFor(candidates[j].NodeID)
		scoreI := nodePlacementScore(candidates[i], workload, pendingCPUI, pendingMemI, counts[candidates[i].NodeID], maxCount)
		scoreJ := nodePlacementScore(candidates[j], workload, pendingCPUJ, pendingMemJ, counts[candidates[j].NodeID], maxCount)
		return scoreI > scoreJ // descending: highest score (most preferred) first
	})

	pendingCPU, pendingMem := s.pendingReservationFor(candidates[0].NodeID)
	bestScore := nodePlacementScore(candidates[0], workload, pendingCPU, pendingMem, counts[candidates[0].NodeID], maxCount)
	reason := fmt.Sprintf("selected by placement score %.4f (cpu/mem headroom + spread, node has %d workloads)", bestScore, counts[candidates[0].NodeID])
	return candidates[0], reason, nil
}

func blocksScheduling(taints []models.NodeTaint) bool {
	for _, taint := range taints {
		effect := strings.TrimSpace(taint.Effect)
		if effect == "" || strings.EqualFold(effect, "NoSchedule") {
			return true
		}
	}
	return false
}

func (s *Scheduler) RelocateWorkloadsFromNode(nodeID, reason string) (int, error) {
	if err := s.requireWritable(); err != nil {
		return 0, err
	}
	workloads, err := s.GetWorkloadsByNode(nodeID)
	if err != nil {
		return 0, err
	}
	relocated := 0
	for i := range workloads {
		workload := workloads[i]
		if isNodeLocalWorkload(workload) {
			schedulerLogger.WithFields(logrus.Fields{
				"workload_id": workload.ID,
				"node_id":     nodeID,
			}).Info("skip relocate: node-local workload hard-pinned")
			s.emitEvent("RescheduleSkipped", workload.ID, nodeID, "node-local; not moving local data", map[string]interface{}{
				"persistence_class": workloadPersistenceClass(workload),
			})
			continue
		}
		nextNode, selectionReason, selErr := s.selectNodeForWorkload(workload)
		if selErr != nil {
			_ = s.UpdateWorkloadRetryOnFailure(workload.ID, fmt.Sprintf("%s; no relocation target: %v", reason, selErr))
			continue
		}
		if strings.EqualFold(nextNode.NodeID, nodeID) {
			_ = s.UpdateWorkloadRetryOnFailure(workload.ID, fmt.Sprintf("%s; selected same node", reason))
			continue
		}
		if workload.Metadata == nil {
			workload.Metadata = map[string]interface{}{}
		}
		workload.Metadata["previous_node"] = nodeID
		workload.Metadata["last_action"] = "Relocated"
		workload.Retry.Attempts = 0
		workload.Retry.NextRetryAt = time.Time{}
		if err := s.assignWorkload(&workload, nextNode, fmt.Sprintf("%s; %s", reason, selectionReason)); err != nil {
			return relocated, err
		}
		relocated++
		_ = s.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Relocated from node %s to %s: %s", nodeID, nextNode.NodeID, reason))
		s.emitEvent("Relocated", workload.ID, nextNode.NodeID, reason, map[string]interface{}{"previous_node": nodeID})
	}
	return relocated, nil
}

func (s *Scheduler) assignWorkload(workload *models.Workload, node models.Node, reason string) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
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
	s.reservePlacement(node.NodeID, workload.ID, workload.Resources.CPUUsage, workload.Resources.MemoryUsage)
	s.emitEvent("WorkloadScheduled", workload.ID, node.NodeID, reason, nil)
	return nil
}

// ScheduleWorkload assigns a workload to a suitable node and sends a command via the agent API.
func (s *Scheduler) ScheduleWorkload(workload models.Workload) (string, error) {
	if err := s.requireWritable(); err != nil {
		return "", err
	}
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
	// Agent apply is async by design. Persist tracking metadata and return quickly.
	if workload.Metadata == nil {
		workload.Metadata = map[string]interface{}{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	workload.Metadata[workloadReapplyTimestampKey] = now
	workload.Metadata[workloadReapplyRevisionKey] = strings.TrimSpace(workload.RevisionID)
	workload.Metadata["lastLaunchTime"] = now
	workload.Metadata["last_action"] = "ApplyAcceptedAsync"
	if applyResp != nil && applyResp.Status != nil && len(applyResp.Status.GetMetadata()) > 0 {
		for k, v := range applyResp.Status.GetMetadata() {
			workload.Metadata[k] = strings.TrimSpace(v)
		}
		if taskID := strings.TrimSpace(applyResp.Status.GetMetadata()["task_id"]); taskID != "" {
			_ = s.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Tracking async apply task: %s", taskID))
		}
	}
	if err := s.saveWorkload(workload); err != nil {
		return "", fmt.Errorf("persist async apply guard metadata: %w", err)
	}
	s.writeReconciliationRecord(workload.ID, "Apply", true, "Apply accepted by agent (async tracking)")

	schedulerLogger.WithFields(logrus.Fields{
		"workload_id": workload.ID,
		"node_id":     selectedNode.NodeID,
	}).Info("workload apply accepted")
	return selectedNode.NodeID, nil
}

// GetNodes retrieves all nodes from etcd.
func (s *Scheduler) GetNodes() ([]models.Node, error) {
	resp, err := s.RetryableEtcdGet("/nodes/", clientv3.WithPrefix())
	if err != nil {
		if s.currentMode() != ModeNormal {
			return s.getCachedNodes(), nil
		}
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

	s.withCacheLock(func() {
		s.cacheNodes = map[string]models.Node{}
		for _, n := range nodes {
			s.cacheNodes[n.NodeID] = n
		}
	})
	return nodes, nil
}

// GetNodeByID retrieves a specific node by ID.
func (s *Scheduler) GetNodeByID(nodeID string) (models.Node, error) {
	resp, err := s.RetryableEtcdGet("/nodes/" + nodeID)
	if err != nil {
		if s.currentMode() != ModeNormal {
			if node, ok := s.getCachedNode(nodeID); ok {
				return node, nil
			}
		}
		return models.Node{}, fmt.Errorf("failed to get node %s: %v", nodeID, err)
	}

	if len(resp.Kvs) == 0 {
		return models.Node{}, fmt.Errorf("node %s not found", nodeID)
	}

	var node models.Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
		return models.Node{}, fmt.Errorf("failed to unmarshal node %s: %v", nodeID, err)
	}
	s.cacheNode(node)

	return node, nil
}

// DeleteNode removes a node from etcd and CoreDNS.
func (s *Scheduler) DeleteNode(nodeID string) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	if err := s.RetryableEtcdDelete("/nodes/" + nodeID); err != nil {
		return fmt.Errorf("failed to delete node %s: %v", nodeID, err)
	}
	_ = s.RetryableEtcdDelete("/nodes/" + nodeID + "/status")

	// Remove from CoreDNS
	shard := strings.TrimSpace(s.schedulerShard)
	if shard == "" {
		shard = "default"
	}
	key := fmt.Sprintf("/skydns/%s/%s/%s", reverseDomain(s.agentsDomain), shard, nodeID)
	if err := s.RetryableEtcdDelete(key); err != nil {
		schedulerLogger.WithError(err).WithField("node_id", nodeID).Warn("failed to remove CoreDNS entry")
	}

	s.emitEvent("NodeLeft", "", nodeID, "node removed from cluster", map[string]interface{}{
		"status": "Removed",
	})

	schedulerLogger.WithField("node_id", nodeID).Info("deleted node")
	return nil
}

// GetWorkloads retrieves all workloads from etcd.
func (s *Scheduler) GetWorkloads() ([]models.Workload, error) {
	specResp, err := s.RetryableEtcdGet(workloadSpecPrefix, clientv3.WithPrefix())
	if err != nil {
		if s.currentMode() != ModeNormal {
			return s.getCachedWorkloads(), nil
		}
		return nil, fmt.Errorf("failed to get workloads: %v", err)
	}

	workloads := make([]models.Workload, 0)
	if specResp == nil {
		return workloads, nil
	}

	statusResp, _ := s.RetryableEtcdGet(workloadStatusPrefix, clientv3.WithPrefix())
	statusMap := map[string]workloadStatus{}
	if statusResp != nil {
		for _, kv := range statusResp.Kvs {
			var st workloadStatus
			if err := json.Unmarshal(kv.Value, &st); err == nil {
				statusMap[st.ID] = st
			}
		}
	}

	for _, kv := range specResp.Kvs {
		var spec workloadSpec
		if err := json.Unmarshal(kv.Value, &spec); err != nil {
			schedulerLogger.WithError(err).WithField("key", string(kv.Key)).Warn("failed to unmarshal workload spec data")
			continue
		}
		workload := models.Workload{
			ID: spec.ID, Name: spec.Name, Type: spec.Type, RevisionID: spec.RevisionID, Image: spec.Image, Command: spec.Command,
			CommandList: spec.CommandList, Compose: spec.Compose, ComposeYAML: spec.ComposeYAML, ProjectName: spec.ProjectName,
			GitRepo: spec.GitRepo, GitBranch: spec.GitBranch, GitToken: spec.GitToken, EnvVars: spec.EnvVars, Resources: spec.Resources,
			DesiredState: spec.DesiredState, Labels: spec.Labels, LocalPath: spec.LocalPath, Ports: spec.Ports, Volumes: spec.Volumes,
			Network: spec.Network, RestartPolicy: spec.RestartPolicy, VM: spec.VM,
		}
		if st, ok := statusMap[workload.ID]; ok {
			workload.AssignedNode = st.AssignedNode
			workload.NodeID = st.NodeID
			workload.Status = st.Status
			workload.Logs = st.Logs
			workload.Metadata = st.Metadata
			workload.Retry = st.Retry
			workload.StatusInfo = st.StatusInfo
			workload.Usage = st.Usage
		}
		workloads = append(workloads, workload)
	}
	if s.currentMode() != ModeNormal && len(workloads) == 0 {
		// In recovery mode etcd may be reachable but state still empty.
		// Preserve and serve the frozen cache snapshot for operator visibility.
		return s.getCachedWorkloads(), nil
	}

	s.withCacheLock(func() {
		s.cacheWorkloads = map[string]models.Workload{}
		for _, w := range workloads {
			s.cacheWorkloads[w.ID] = w
		}
	})
	return workloads, nil
}

// GetWorkloadByID retrieves a specific workload by ID.
func (s *Scheduler) GetWorkloadByID(workloadID string) (models.Workload, error) {
	specResp, err := s.RetryableEtcdGet(workloadSpecKey(workloadID))
	if err != nil {
		if s.currentMode() != ModeNormal {
			if workload, ok := s.getCachedWorkload(workloadID); ok {
				return workload, nil
			}
		}
		return models.Workload{}, fmt.Errorf("failed to get workload %s: %v", workloadID, err)
	}

	if specResp == nil || len(specResp.Kvs) == 0 {
		// compatibility shim for legacy persisted full objects.
		resp, legacyErr := s.RetryableEtcdGet("/workloads/" + workloadID)
		if legacyErr != nil || len(resp.Kvs) == 0 {
			return models.Workload{}, fmt.Errorf("workload %s not found", workloadID)
		}
		var legacy models.Workload
		if err := json.Unmarshal(resp.Kvs[0].Value, &legacy); err != nil {
			return models.Workload{}, fmt.Errorf("failed to unmarshal legacy workload %s: %v", workloadID, err)
		}
		s.cacheWorkload(legacy)
		return legacy, nil
	}

	var spec workloadSpec
	if err := json.Unmarshal(specResp.Kvs[0].Value, &spec); err != nil {
		return models.Workload{}, fmt.Errorf("failed to unmarshal workload spec %s: %v", workloadID, err)
	}

	statusResp, _ := s.RetryableEtcdGet(workloadStatusKey(workloadID))
	var st workloadStatus
	if statusResp != nil && len(statusResp.Kvs) > 0 {
		_ = json.Unmarshal(statusResp.Kvs[0].Value, &st)
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
	s.cacheWorkload(workload)

	return workload, nil
}

// DeleteWorkload removes a workload from etcd.
func (s *Scheduler) DeleteWorkloadWithContext(ctx context.Context, workloadID string) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workload, err := s.GetWorkloadByID(workloadID)
	if err == nil {
		if shouldSyncManagedStorage(workload) {
			if err := s.cleanupManagedStorageForWorkload(workloadID); err != nil {
				return fmt.Errorf("failed to cleanup managed storage state for workload %s: %v", workloadID, err)
			}
		}
	}
	if err == nil && workload.NodeID != "" {
		node, nodeErr := s.GetNodeByID(workload.NodeID)
		if nodeErr == nil {
			if _, delErr := s.deleteWorkloadFromNode(ctx, node, workloadID); delErr != nil {
				if isNodeUnreachableError(delErr) || isWorkloadStatusNotFound(delErr) {
					_ = s.UpdateWorkloadLogs(workloadID, fmt.Sprintf("best-effort delete: node %s unreachable, removing scheduler state", node.NodeID))
				} else {
					return fmt.Errorf("delete workload on node %s: %v", node.NodeID, delErr)
				}
			}

			deadline := time.Now().Add(s.deleteTimeout())
			for {
				_, stErr := s.getWorkloadStatusFromNode(ctx, node, workloadID)
				if stErr != nil && isWorkloadStatusNotFound(stErr) {
					break
				}
				if stErr != nil && isNodeUnreachableError(stErr) {
					_ = s.UpdateWorkloadLogs(workloadID, fmt.Sprintf("delete verification skipped: node %s unreachable", node.NodeID))
					break
				}
				if time.Now().After(deadline) {
					_ = s.UpdateWorkloadLogs(workloadID, fmt.Sprintf("delete verification timed out on node %s; removing scheduler state", node.NodeID))
					break
				}
				time.Sleep(s.agentPollInterval())
			}
		}
	}

	if err := s.RetryableEtcdDelete(workloadSpecKey(workloadID)); err != nil {
		return fmt.Errorf("failed to delete workload %s: %v", workloadID, err)
	}
	_ = s.RetryableEtcdDelete(workloadStatusKey(workloadID))
	_ = s.RetryableEtcdDelete("/workloads/" + workloadID)
	_ = s.RetryableEtcdDelete(assignmentKey(workloadID))
	_ = s.RetryableEtcdDelete(retryKey(workloadID))
	_ = s.RetryableEtcdDelete(reconciliationKey(workloadID))
	s.emitEvent("Rescheduled", workloadID, "", "Workload state removed", nil)
	s.removeCachedWorkload(workloadID)
	schedulerLogger.WithField("workload_id", workloadID).Info("deleted workload")
	return nil
}

// DeleteWorkload removes a workload from etcd.
func (s *Scheduler) DeleteWorkload(workloadID string) error {
	return s.DeleteWorkloadWithContext(context.Background(), workloadID)
}

// UpdateWorkloadStatus updates the status of a workload.
func (s *Scheduler) UpdateWorkloadStatus(workloadID, status string) error {
	_, err := s.updateWorkloadStatusCAS(workloadID, func(workload *models.Workload) bool {
		if strings.EqualFold(workload.Status, status) && strings.EqualFold(workload.StatusInfo.ActualState, status) {
			return false
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
		return true
	})
	if err != nil {
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
	if strings.TrimSpace(logs) == "" {
		return nil
	}
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logEntry := fmt.Sprintf("[%s] %s\n", timestamp, logs)

	_, err := s.updateWorkloadStatusCAS(workloadID, func(workload *models.Workload) bool {
		if workload.Logs == "" {
			workload.Logs = logEntry
		} else {
			workload.Logs += logEntry
		}
		workload.StatusInfo.LastUpdated = time.Now().UTC()
		return true
	})
	if err != nil {
		return fmt.Errorf("failed to update workload %s logs: %v", workloadID, err)
	}

	schedulerLogger.WithField("workload_id", workloadID).Debug("updated workload logs")
	return nil
}

// UpdateWorkloadMetadata merges runtime metadata into the workload record.
func (s *Scheduler) UpdateWorkloadMetadata(workloadID string, metadata map[string]string) error {
	if len(metadata) == 0 {
		return nil
	}
	_, err := s.updateWorkloadStatusCAS(workloadID, func(workload *models.Workload) bool {
		if workload.Metadata == nil {
			workload.Metadata = map[string]interface{}{}
		}
		changed := false
		for k, v := range metadata {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			cleanVal := strings.TrimSpace(v)
			if existing, ok := workload.Metadata[key]; !ok || fmt.Sprintf("%v", existing) != cleanVal {
				changed = true
			}
			workload.Metadata[key] = cleanVal
			if key == "container.stderr" || key == "container.runtime_error" {
				if cleanVal != "" {
					workload.Metadata["last_runtime_error"] = cleanVal
					if strings.TrimSpace(workload.StatusInfo.FailureReason) == "" || isInfrastructureFailureReason(workload.StatusInfo.FailureReason) {
						workload.StatusInfo.FailureReason = cleanVal
					}
				}
			}
		}
		if !changed {
			return false
		}
		workload.StatusInfo.LastUpdated = time.Now().UTC()
		return true
	})
	if err != nil {
		return fmt.Errorf("failed to update workload %s metadata: %v", workloadID, err)
	}
	return nil
}

// UpdateWorkloadRuntimeDetails stores structured reason and latest usage for a workload.
// nodeID identifies the agent that reported this sample (from the heartbeat
// envelope) and is used only for the meter usage-stream event; it is not
// persisted on the workload record.
func (s *Scheduler) UpdateWorkloadRuntimeDetails(workloadID, nodeID string, reason *models.WorkloadReason, usage *models.WorkloadUsage) error {
	workload, err := s.updateWorkloadStatusCAS(workloadID, func(workload *models.Workload) bool {
		if workload.Metadata == nil {
			workload.Metadata = map[string]interface{}{}
		}

		if reason != nil {
			copied := *reason
			workload.StatusInfo.Reason = &copied
			if strings.TrimSpace(copied.Message) != "" {
				workload.StatusInfo.FailureReason = copied.Message
			}
			if strings.TrimSpace(copied.Code) != "" {
				workload.Metadata["reason_code"] = copied.Code
			}
			if strings.TrimSpace(copied.Message) != "" {
				workload.Metadata["reason_message"] = copied.Message
			}
			if !copied.LastTransition.IsZero() {
				workload.Metadata["reason_last_transition"] = copied.LastTransition.UTC().Format(time.RFC3339)
			}
			if !copied.NextRetryAt.IsZero() {
				workload.Metadata["reason_next_retry_at"] = copied.NextRetryAt.UTC().Format(time.RFC3339)
			}
			workload.Metadata["reason_retryable"] = fmt.Sprintf("%t", copied.Retryable)
		}

		if usage != nil {
			copied := *usage
			workload.Usage = &copied
			workload.Metadata["usage_cpu_percent"] = fmt.Sprintf("%.4f", copied.CPUPercent)
			workload.Metadata["usage_memory_bytes"] = fmt.Sprintf("%d", copied.MemoryBytes)
			workload.Metadata["usage_disk_read_bytes"] = fmt.Sprintf("%d", copied.DiskReadBytes)
			workload.Metadata["usage_disk_write_bytes"] = fmt.Sprintf("%d", copied.DiskWriteBytes)
			workload.Metadata["usage_net_rx_bytes"] = fmt.Sprintf("%d", copied.NetRXBytes)
			workload.Metadata["usage_net_tx_bytes"] = fmt.Sprintf("%d", copied.NetTXBytes)
			if !copied.CollectedAt.IsZero() {
				workload.Metadata["usage_collected_at"] = copied.CollectedAt.UTC().Format(time.RFC3339)
			}
			if strings.TrimSpace(copied.Source) != "" {
				workload.Metadata["usage_source"] = copied.Source
			}
		}

		workload.StatusInfo.LastUpdated = time.Now().UTC()
		return true
	})
	if err != nil {
		return fmt.Errorf("failed to update workload %s runtime details: %v", workloadID, err)
	}

	if usage != nil {
		reportingNode := strings.TrimSpace(nodeID)
		if reportingNode == "" {
			reportingNode = workload.NodeID
		}
		s.publishUsageEvent(reportingNode, workload.ID, workload.RevisionID, usage)
	}

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
// backgroundLoopConcurrency returns the concurrency cap used by
// MonitorNodes, MonitorWorkloads, and detectDriftOnce. Shares the
// SCHEDULER_RECONCILE_CONCURRENCY setting with the reconciler
// (Reconciler.reconcileConcurrency) rather than introducing a separate
// knob — these loops make the same shape of per-item network call the
// reconciler does, just less frequently, so the same concurrency budget
// applies.
func (s *Scheduler) backgroundLoopConcurrency() int {
	if s.cfg != nil && s.cfg.SchedulerReconcileConcurrency > 0 {
		return s.cfg.SchedulerReconcileConcurrency
	}
	return defaultReconcileConcurrency
}

func (s *Scheduler) MonitorNodes(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			schedulerLogger.Info("stopping node monitoring")
			return
		case <-ticker.C:
			if !s.isWritable() {
				continue
			}
			nodes, err := s.GetNodes()
			if err != nil {
				schedulerLogger.WithError(err).Error("node monitoring cycle failed")
				continue
			}
			owned := make([]models.Node, 0, len(nodes))
			for _, node := range nodes {
				if s.ownsNode(node.NodeID) {
					owned = append(owned, node)
				}
			}
			runBounded(owned, s.backgroundLoopConcurrency(), func(node models.Node) {
				if time.Since(node.LastHeartbeat) > 3*time.Minute {
					reason := fmt.Sprintf("heartbeat expired: last heartbeat %s", node.LastHeartbeat.UTC().Format(time.RFC3339))
					if err := s.markNodeNotReady(node.NodeID, reason, "monitor"); err != nil {
						schedulerLogger.WithError(err).WithField("node_id", node.NodeID).Warn("failed to update node status")
					}
				}
			})
		}
	}
}

// StartMonitoring starts the per-replica supervisory loop that tracks this
// instance's own etcd connectivity (used by requireWritable/isWritable
// checks throughout, including in gRPC handlers that must run on every
// replica regardless of leadership). Cluster-wide singleton work — node
// watch, node/workload monitoring, drift detection, reconciliation — is
// started separately via StartLeaderElectedBackgroundLoops, gated on
// leader election so it runs on exactly one replica at a time.
func (s *Scheduler) StartMonitoring(ctx context.Context) {
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		s.startModeSupervisor(ctx)
	}()
}

// StartLeaderElectedBackgroundLoops campaigns for scheduler leadership (see
// leader.go) and, for as long as this instance holds it, runs every
// cluster-wide singleton background loop: the watch-backed node cache,
// node monitoring, workload monitoring, drift detection, and
// reconciliation. Only one scheduler replica runs these at a time; if the
// current leader dies or its etcd session lapses, another replica takes
// over automatically. Safe to call on every replica — non-leaders simply
// block campaigning until they win an election (e.g. after the previous
// leader stops).
func (s *Scheduler) StartLeaderElectedBackgroundLoops(ctx context.Context) {
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		s.RunWithLeaderElection(ctx, s.runLeaderOnlyBackgroundLoops)
	}()
}

// runLeaderOnlyBackgroundLoops runs every singleton background loop and
// blocks until leaderCtx is cancelled — i.e. until this instance loses
// leadership (session expiry, or normal shutdown). Called by
// RunWithLeaderElection; not meant to be called directly.
func (s *Scheduler) runLeaderOnlyBackgroundLoops(leaderCtx context.Context) {
	var wg sync.WaitGroup

	// Watch-backed node cache used by placement (candidateNodeSnapshot /
	// selectNodeForWorkload) to avoid a full etcd scan per scheduling
	// decision.
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.StartNodeWatch(leaderCtx)
	}()

	// Node monitoring
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.MonitorNodes(leaderCtx)
	}()

	// Workload monitoring
	if s.monitor != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.monitor.MonitorWorkloads(leaderCtx, 60*time.Second)
		}()
	}

	// Drift detection (agent state vs scheduler state)
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.StartDriftDetection(leaderCtx, s.driftDetectInterval())
	}()

	// Reconciliation
	if s.reconciler != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.reconciler.StartReconciliationLoop(leaderCtx, s.cfg.SchedulerReconcileInterval)
		}()
	}

	wg.Wait()
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
