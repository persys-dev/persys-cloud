package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/persys-dev/prow/internal/models"
)

// Monitor handles container status monitoring
type Monitor struct {
	scheduler *Scheduler
}

// ContainerInfo represents a container's info from /docker/list
// Add Reason field for detailed status
type ContainerInfo struct {
	ID     string `json:"id"`
	Name   string `json:"names"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// ContainerLogs represents container logs from /docker/logs
type ContainerLogs struct {
	ContainerID string `json:"containerId"`
	Logs        string `json:"logs"`
}

// dockerListResponse matches the /docker/list JSON structure
type dockerListResponse struct {
	Result []ContainerInfo `json:"result"`
}

// NewMonitor creates a new Monitor instance
func NewMonitor(scheduler *Scheduler) *Monitor {
	return &Monitor{scheduler: scheduler}
}

// getContainerList queries the node's /docker/list endpoint
func (m *Monitor) getContainerList(node models.Node) ([]ContainerInfo, error) {
	endpoint := "/docker/list"
	response, err := m.scheduler.SendCommandToNode(node, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s on node %s: %v", endpoint, node.NodeID, err)
	}

	log.Printf("Raw /docker/list response from node %s: %s", node.NodeID, response)

	var dockerResp dockerListResponse
	if err := json.Unmarshal([]byte(response), &dockerResp); err != nil {
		return nil, fmt.Errorf("failed to decode %s response from node %s: %v", endpoint, node.NodeID, err)
	}

	log.Printf("Retrieved %d containers from node %s", len(dockerResp.Result), node.NodeID)
	return dockerResp.Result, nil
}

// getContainerLogs queries the node's /docker/logs endpoint for a specific container
func (m *Monitor) getContainerLogs(node models.Node, containerID string) (string, error) {
	endpoint := fmt.Sprintf("/docker/logs/%s", containerID)
	response, err := m.scheduler.SendCommandToNode(node, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to query %s on node %s: %v", endpoint, node.NodeID, err)
	}

	// Parse the response to extract logs
	var logsResp struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(response), &logsResp); err != nil {
		return "", fmt.Errorf("failed to decode logs response from node %s: %v", node.NodeID, err)
	}

	return logsResp.Result, nil
}

// mapContainerStatus maps Docker container status to workload status
func mapContainerStatus(dockerStatus string) string {
	dockerStatus = strings.ToLower(dockerStatus)
	switch {
	case strings.Contains(dockerStatus, "pulling"):
		return "Pulling"
	case strings.Contains(dockerStatus, "imagepullbackoff"):
		return "ImagePullBackOff"
	case strings.Contains(dockerStatus, "containercreating"):
		return "ContainerCreating"
	case strings.Contains(dockerStatus, "up"):
		return "Running"
	case strings.Contains(dockerStatus, "exited"):
		return "Exited"
	case strings.Contains(dockerStatus, "created"):
		return "Scheduled"
	case strings.Contains(dockerStatus, "paused"):
		return "Stopped"
	default:
		return "Unknown"
	}
}

// MonitorWorkloads periodically checks container statuses and updates etcd
func (m *Monitor) MonitorWorkloads(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Stopping workload monitoring")
			return
		case <-ticker.C:
			log.Printf("Starting workload monitoring cycle")
			nodes, err := m.scheduler.GetNodes()
			if err != nil {
				log.Printf("Failed to get nodes for monitoring: %v", err)
				continue
			}

			for _, node := range nodes {
				if node.Status != "active" {
					log.Printf("Skipping inactive node %s (status: %s)", node.NodeID, node.Status)
					continue
				}

				// Get workloads for node
				workloads, err := m.scheduler.GetWorkloadsByNode(node.NodeID)
				if err != nil {
					log.Printf("Failed to get workloads for node %s: %v", node.NodeID, err)
					continue
				}

				// Get container list from agent
				containers, err := m.getContainerList(node)
				if err != nil {
					log.Printf("Failed to get container list for node %s: %v", node.NodeID, err)
					continue
				}

				// Create map of container names to info
				containerMap := make(map[string]ContainerInfo)
				for _, container := range containers {
					containerMap[container.Name] = container
				}

				// Update workload statuses and capture logs
				for _, workload := range workloads {
					container, exists := containerMap[workload.ID] // Match by workload ID instead of name
					newStatus := workload.Status
					statusChanged := false
					var reason string
					if !exists {
						if workload.Status != "Missing" {
							newStatus = "Missing"
							statusChanged = true
							log.Printf("Workload %s (name: %s) on node %s not found in container list, setting status to %s", workload.ID, workload.Name, node.NodeID, newStatus)
						}
					} else {
						mappedStatus := mapContainerStatus(container.Status)
						if mappedStatus != workload.Status {
							newStatus = mappedStatus
							statusChanged = true
							log.Printf("Workload %s (name: %s) on node %s status changed from %s to %s", workload.ID, workload.Name, node.NodeID, workload.Status, newStatus)
						}
						// If container has a Reason field, propagate it
						if (mappedStatus == "Pulling" || mappedStatus == "ImagePullBackOff" || mappedStatus == "ContainerCreating") && container.Reason != "" {
							reason = container.Reason
						}
					}
					if statusChanged {
						// Update status
						if err := m.scheduler.UpdateWorkloadStatus(workload.ID, newStatus); err != nil {
							log.Printf("Failed to update status for workload %s: %v", workload.ID, err)
						}
						// Update logs with reason if present
						if reason != "" {
							if err := m.scheduler.UpdateWorkloadLogs(workload.ID, "Reason: "+reason); err != nil {
								log.Printf("Failed to update logs for workload %s: %v", workload.ID, err)
							}
						}
						// Capture logs if container exists and status indicates it has run
						if exists && (newStatus == "Running" || newStatus == "Exited" || newStatus == "Failed") {
							logs, err := m.getContainerLogs(node, container.ID)
							if err != nil {
								log.Printf("Failed to get logs for container %s (workload %s): %v", container.ID, workload.ID, err)
							} else if logs != "" {
								if err := m.scheduler.UpdateWorkloadLogs(workload.ID, fmt.Sprintf("Status changed to %s. Container logs:\n%s", newStatus, logs)); err != nil {
									log.Printf("Failed to update logs for workload %s: %v", workload.ID, err)
								}
							}
						}
					}
				}
			}
			log.Printf("Completed workload monitoring cycle")
		}
	}
}

// StartMonitoring initializes the workload monitoring routine
func (m *Monitor) StartMonitoring() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go m.MonitorWorkloads(ctx, 60*time.Second)
	return cancel
}
