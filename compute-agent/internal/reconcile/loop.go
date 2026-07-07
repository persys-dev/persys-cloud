package reconcile

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/state"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/workload"
	"github.com/sirupsen/logrus"
)

// Loop handles periodic reconciliation of workload state
type Loop struct {
	store    state.Store
	manager  *workload.Manager
	interval time.Duration
	stopCh   chan struct{}
	logger   *logrus.Entry
	stopOnce sync.Once
}

// NewLoop creates a new reconciliation loop
func NewLoop(store state.Store, manager *workload.Manager, interval time.Duration, logger *logrus.Logger) *Loop {
	return &Loop{
		store:    store,
		manager:  manager,
		interval: interval,
		stopCh:   make(chan struct{}),
		logger:   logger.WithField("component", "reconciler"),
	}
}

// Start begins the reconciliation loop
func (l *Loop) Start(ctx context.Context) {
	l.logger.Infof("Starting reconciliation loop (interval: %s)", l.interval)

	// Run initial reconciliation
	l.reconcileAll(ctx)

	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.reconcileAll(ctx)
		case <-l.stopCh:
			l.logger.Info("Stopping reconciliation loop")
			return
		case <-ctx.Done():
			l.logger.Info("Context cancelled, stopping reconciliation loop")
			return
		}
	}
}

// Stop stops the reconciliation loop
func (l *Loop) Stop() {
	l.stopOnce.Do(func() {
		close(l.stopCh)
	})
}

// reconcileAll reconciles all workloads
func (l *Loop) reconcileAll(ctx context.Context) {
	startTime := time.Now()
	l.logger.Debug("Starting reconciliation cycle")

	workloads, err := l.store.ListWorkloads()
	if err != nil {
		l.logger.Errorf("Failed to list workloads for reconciliation: %v", err)
		return
	}

	if len(workloads) == 0 {
		l.logger.Debug("No workloads to reconcile")
		return
	}

	successCount := 0
	failCount := 0
	var failures []string

	for _, workload := range workloads {
		if err := l.manager.ReconcileWorkload(ctx, workload.ID); err != nil {
			failCount++
			failures = append(failures, fmt.Sprintf("%s: %v", workload.ID, err))
		} else {
			successCount++
		}
	}

	duration := time.Since(startTime)

	// Only log if there were failures or successful actions
	if failCount > 0 {
		l.logger.Warnf("Reconciliation cycle completed in %s (success: %d, failed: %d, total: %d)",
			duration, successCount, failCount, len(workloads))
		for _, failure := range failures {
			l.logger.Warnf("  - %s", failure)
		}
	} else if successCount > 0 {
		l.logger.Infof("Reconciliation cycle completed in %s (reconciled %d/%d workloads)",
			duration, successCount, len(workloads))
	} else {
		l.logger.Debug("Reconciliation cycle completed with no changes needed")
	}

	// Update workload count metrics
	if err := l.manager.UpdateWorkloadMetrics(); err != nil {
		l.logger.Warnf("Failed to update workload metrics: %v", err)
	}
}

// ReconcileNow triggers an immediate reconciliation
func (l *Loop) ReconcileNow(ctx context.Context) {
	l.logger.Info("Triggered immediate reconciliation")
	l.reconcileAll(ctx)
}
