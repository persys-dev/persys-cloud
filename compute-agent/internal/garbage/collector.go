package garbage

import (
	"context"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/metrics"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/runtime"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/state"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
)

// CollectorConfig defines garbage collection configuration
type CollectorConfig struct {
	// Enabled toggles garbage collection
	Enabled bool
	// Interval between garbage collection runs
	Interval time.Duration
	// FailedWorkloadTTL is how long to keep failed workloads before garbage collecting them
	FailedWorkloadTTL time.Duration
	// MaxOrphanedResourceAge is the maximum age of an orphaned resource before cleanup
	MaxOrphanedResourceAge time.Duration
}

// DefaultConfig returns sensible defaults for garbage collection
func DefaultConfig() *CollectorConfig {
	return &CollectorConfig{
		Enabled:                true,
		Interval:               5 * time.Minute,
		FailedWorkloadTTL:      1 * time.Hour,
		MaxOrphanedResourceAge: 30 * time.Minute,
	}
}

// Collector handles garbage collection of orphaned resources and failed workloads
type Collector struct {
	config     *CollectorConfig
	store      state.Store
	runtimeMgr *runtime.Manager
	logger     *logrus.Entry
	stopCh     chan struct{}
	metrics    *metrics.Metrics
}

// NewCollector creates a new garbage collector
func NewCollector(config *CollectorConfig, store state.Store, runtimeMgr *runtime.Manager, logger *logrus.Logger) *Collector {
	if config == nil {
		config = DefaultConfig()
	}
	return &Collector{
		config:     config,
		store:      store,
		runtimeMgr: runtimeMgr,
		logger:     logger.WithField("component", "garbage-collector"),
		stopCh:     make(chan struct{}),
	}
}

// SetMetrics sets the metrics instance for recording GC statistics
func (c *Collector) SetMetrics(m *metrics.Metrics) {
	c.metrics = m
}

// Start begins the garbage collection loop
func (c *Collector) Start(ctx context.Context) {
	if !c.config.Enabled {
		c.logger.Info("Garbage collection is disabled")
		return
	}

	c.logger.Infof("Starting garbage collector (interval: %s, failed-workload-ttl: %s)",
		c.config.Interval, c.config.FailedWorkloadTTL)

	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()

	// Run initial collection
	c.collect(ctx)

	for {
		select {
		case <-ticker.C:
			c.collect(ctx)
		case <-c.stopCh:
			c.logger.Info("Stopping garbage collector")
			return
		case <-ctx.Done():
			c.logger.Info("Context cancelled, stopping garbage collector")
			return
		}
	}
}

// Stop stops the garbage collector
func (c *Collector) Stop() {
	close(c.stopCh)
}

// collect performs the actual garbage collection
func (c *Collector) collect(ctx context.Context) {
	startTime := time.Now()
	c.logger.Debug("Starting garbage collection cycle")

	// Record GC run start and get callback for duration
	var recordDuration func()
	if c.metrics != nil {
		recordDuration = c.metrics.RecordGCRunStart()
	}

	// 1. Collect orphaned resources (exist in runtime but not in state store)
	orphanedCount := c.collectOrphanedResources(ctx)

	// 2. Collect failed workloads older than TTL
	failedCount := c.collectOldFailedWorkloads()

	// 3. Clean up zombie resources
	zombieCount := c.cleanupZombies()

	// Record metrics for this cycle
	if c.metrics != nil {
		c.metrics.RecordGCOrphanedResources("total", orphanedCount)
		c.metrics.RecordGCOldFailedWorkloads(failedCount)
		recordDuration()
	}

	duration := time.Since(startTime)

	if orphanedCount+failedCount+zombieCount > 0 {
		c.logger.Infof("Garbage collection completed in %s (orphaned: %d, old-failed: %d, zombies: %d)",
			duration, orphanedCount, failedCount, zombieCount)
	} else {
		c.logger.Debug("Garbage collection completed with no items to clean")
	}
}

// collectOrphanedResources finds and removes resources that exist in runtime but not in state
func (c *Collector) collectOrphanedResources(ctx context.Context) int {
	count := 0

	// Check each runtime for orphaned resources
	for _, workloadType := range []models.WorkloadType{
		models.WorkloadTypeContainer,
		models.WorkloadTypeCompose,
		models.WorkloadTypeVM,
	} {
		// VM runtime lists all domains on the host; without an ownership marker query,
		// deleting "orphaned" VMs is unsafe and may remove non-agent resources.
		if workloadType == models.WorkloadTypeVM {
			c.logger.Debug("Skipping VM orphaned-resource GC due to unknown ownership scope")
			continue
		}

		rt, err := c.runtimeMgr.GetRuntime(workloadType)
		if err != nil {
			continue // Runtime not available
		}

		// List all resources in runtime
		runtimeResources, err := rt.List(ctx)
		if err != nil {
			c.logger.Warnf("Failed to list %s resources: %v", workloadType, err)
			continue
		}

		// Get all workload IDs from state
		workloads, err := c.store.ListWorkloads()
		if err != nil {
			c.logger.Warnf("Failed to list workloads: %v", err)
			continue
		}

		stateIDs := make(map[string]bool)
		for _, w := range workloads {
			stateIDs[w.ID] = true
		}

		// Find orphaned resources and remove them
		for _, runtimeID := range runtimeResources {
			if !stateIDs[runtimeID] {
				c.logger.Infof("Found orphaned %s resource: %s, removing", workloadType, runtimeID)

				if err := rt.Delete(ctx, runtimeID); err != nil {
					c.logger.Warnf("Failed to delete orphaned resource %s: %v", runtimeID, err)
				} else {
					count++
				}
			}
		}
	}

	return count
}

// collectOldFailedWorkloads removes failed workloads older than TTL
func (c *Collector) collectOldFailedWorkloads() int {
	count := 0

	statuses, err := c.store.ListStatuses()
	if err != nil {
		c.logger.Warnf("Failed to list statuses: %v", err)
		return count
	}

	now := time.Now()
	for _, status := range statuses {
		// Only collect failed workloads
		if status.ActualState != models.ActualStateFailed {
			continue
		}

		// Check if old enough
		age := now.Sub(status.UpdatedAt)
		if age > c.config.FailedWorkloadTTL {
			c.logger.Infof("Collecting old failed workload %s (age: %v, TTL: %v)",
				status.ID, age, c.config.FailedWorkloadTTL)

			// Delete from state
			if err := c.store.DeleteWorkload(status.ID); err != nil {
				c.logger.Warnf("Failed to delete old failed workload %s: %v", status.ID, err)
			} else {
				count++
			}
		}
	}

	return count
}

// cleanupZombies handles ghost workloads (state exists but runtime doesn't, or vice versa)
func (c *Collector) cleanupZombies() int {
	count := 0

	workloads, err := c.store.ListWorkloads()
	if err != nil {
		c.logger.Warnf("Failed to list workloads: %v", err)
		return count
	}

	for _, workload := range workloads {
		rt, err := c.runtimeMgr.GetRuntime(workload.Type)
		if err != nil {
			continue
		}

		// Check if workload exists in runtime
		ctx := context.Background()
		_, msg, err := rt.Status(ctx, workload.ID)
		if err != nil {
			// Resource doesn't exist in runtime, remove from state
			c.logger.Warnf("Found zombie workload %s (exists in state but not in runtime), removing",
				workload.ID)

			if delErr := c.store.DeleteWorkload(workload.ID); delErr != nil {
				c.logger.Warnf("Failed to delete zombie workload %s: %v", workload.ID, delErr)
			} else {
				count++
			}
		} else {
			// Resource exists - update status
			status, _ := c.store.GetStatus(workload.ID)
			if status != nil {
				status.Message = msg
				status.UpdatedAt = time.Now()
				c.store.SaveStatus(status)
			}
		}
	}

	return count
}

// CleanupNow triggers immediate garbage collection
func (c *Collector) CleanupNow(ctx context.Context) {
	c.logger.Info("Triggered immediate garbage collection")
	c.collect(ctx)
}

// GetStats returns garbage collection statistics
type Stats struct {
	OrphanedResources  int
	OldFailedWorkloads int
	Zombies            int
	LastCollectionTime time.Time
}

// GetStats provides statistics about potential garbage items
func (c *Collector) GetStats(ctx context.Context) *Stats {
	stats := &Stats{
		LastCollectionTime: time.Now(),
	}

	// Count orphaned resources
	for _, workloadType := range []models.WorkloadType{
		models.WorkloadTypeContainer,
		models.WorkloadTypeCompose,
		models.WorkloadTypeVM,
	} {
		if workloadType == models.WorkloadTypeVM {
			continue
		}

		rt, err := c.runtimeMgr.GetRuntime(workloadType)
		if err != nil {
			continue
		}

		runtimeResources, err := rt.List(ctx)
		if err != nil {
			continue
		}

		workloads, err := c.store.ListWorkloads()
		if err != nil {
			continue
		}

		stateIDs := make(map[string]bool)
		for _, w := range workloads {
			stateIDs[w.ID] = true
		}

		for _, runtimeID := range runtimeResources {
			if !stateIDs[runtimeID] {
				stats.OrphanedResources++
			}
		}
	}

	// Count old failed workloads
	statuses, _ := c.store.ListStatuses()
	now := time.Now()
	for _, status := range statuses {
		if status.ActualState == models.ActualStateFailed {
			age := now.Sub(status.UpdatedAt)
			if age > c.config.FailedWorkloadTTL {
				stats.OldFailedWorkloads++
			}
		}
	}

	return stats
}
