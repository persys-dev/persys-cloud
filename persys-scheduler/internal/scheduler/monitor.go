package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/sirupsen/logrus"
)

var monitorLogger = logging.C("scheduler.monitor")

// Monitor handles workload status monitoring.
type Monitor struct {
	scheduler *Scheduler
}

// NewMonitor creates a new Monitor instance.
func NewMonitor(scheduler *Scheduler) *Monitor {
	return &Monitor{scheduler: scheduler}
}

func (m *Monitor) syncWorkloadStatus(workloadID string) error {
	workload, err := m.scheduler.GetWorkloadByID(workloadID)
	if err != nil {
		return err
	}
	if workload.NodeID == "" {
		return nil
	}

	node, err := m.scheduler.GetNodeByID(workload.NodeID)
	if err != nil {
		return fmt.Errorf("resolve node %s: %w", workload.NodeID, err)
	}
	if !strings.EqualFold(node.Status, "active") {
		return nil
	}

	statusResp, err := m.scheduler.getWorkloadStatusFromNode(context.Background(), node, workload.ID)
	if err != nil {
		if isWorkloadStatusNotFound(err) {
			if workload.Status != "Deleted" {
				_ = m.scheduler.UpdateWorkloadStatus(workload.ID, "Deleted")
			}
			return nil
		}
		return fmt.Errorf("GetWorkloadStatus failed for workload %s on node %s: %w", workload.ID, node.NodeID, err)
	}
	if statusResp == nil {
		return nil
	}

	newStatus := mapActualStateToSchedulerStatus(statusResp.GetActualState())
	if newStatus != workload.Status {
		if err := m.scheduler.UpdateWorkloadStatus(workload.ID, newStatus); err != nil {
			return err
		}
	}
	if msg := strings.TrimSpace(statusResp.GetMessage()); msg != "" {
		_ = m.scheduler.UpdateWorkloadLogs(workload.ID, msg)
	}
	return nil
}

// MonitorWorkloads periodically checks workload statuses and updates etcd.
func (m *Monitor) MonitorWorkloads(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			monitorLogger.Info("stopping workload monitoring")
			return
		case <-ticker.C:
			workloads, err := m.scheduler.GetWorkloads()
			if err != nil {
				monitorLogger.WithError(err).Error("failed to get workloads for monitoring")
				continue
			}
			for _, workload := range workloads {
				if workload.Status == "Deleted" {
					continue
				}
				if err := m.syncWorkloadStatus(workload.ID); err != nil {
					monitorLogger.WithError(err).WithFields(logrus.Fields{
						"workload_id": workload.ID,
					}).Warn("failed to sync workload")
				}
			}
			if err := m.scheduler.RefreshStateMetrics(); err != nil {
				monitorLogger.WithError(err).Warn("failed to refresh scheduler state metrics")
			}
		}
	}
}

// StartMonitoring initializes the workload monitoring routine using caller-managed context.
func (m *Monitor) StartMonitoring(ctx context.Context) {
	go m.MonitorWorkloads(ctx, 60*time.Second)
}
