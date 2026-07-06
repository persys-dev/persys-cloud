package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/persys-dev/persysctl/internal/config"
	controlv1 "github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1"
	"github.com/persys-dev/persysctl/internal/models"
	agentv1 "github.com/persys-dev/persys-cloud/pkg/agent/api/v1"
	"github.com/spf13/cobra"
)

var (
	workloadListStatus string
	workloadListNodeID string
	workloadGetID      string
	workloadDeleteID   string
	workloadRetryID    string
	workloadStartID    string
	workloadStopID     string
	workloadRestartID  string
	workloadSpecFile   string
	workloadRevision   string
	workloadDesired    string
)

var workloadCmd = &cobra.Command{
	Use:   "workload",
	Short: "Manage workloads",
}

var workloadScheduleCmd = &cobra.Command{
	Use:   "schedule [flags] [file]",
	Short: "Schedule/apply a workload",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.GetConfig()
		var workload models.Workload
		if len(args) > 0 {
			data, err := os.ReadFile(args[0])
			cobra.CheckErr(err)
			cobra.CheckErr(json.Unmarshal(data, &workload))
		} else {
			workload.ID, _ = cmd.Flags().GetString("id")
			workload.Name, _ = cmd.Flags().GetString("name")
			workload.Type, _ = cmd.Flags().GetString("type")
			workload.Image, _ = cmd.Flags().GetString("image")
			workload.Command, _ = cmd.Flags().GetString("command")
			workload.Compose, _ = cmd.Flags().GetString("compose")
			workload.GitRepo, _ = cmd.Flags().GetString("git-repo")
			workload.GitBranch, _ = cmd.Flags().GetString("git-branch")
			workload.GitToken, _ = cmd.Flags().GetString("git-token")
			workload.LocalPath, _ = cmd.Flags().GetString("local-path")
			workload.Ports, _ = cmd.Flags().GetStringSlice("ports")
			workload.Volumes, _ = cmd.Flags().GetStringSlice("volumes")
			workload.Network, _ = cmd.Flags().GetString("network")
			workload.RestartPolicy, _ = cmd.Flags().GetString("restart-policy")
			envStr, _ := cmd.Flags().GetString("env")
			if envStr != "" {
				workload.EnvVars = parseEnvVars(envStr)
			}
		}

		if workload.Type == "" {
			cobra.CheckErr(fmt.Errorf("type is required"))
		}
		if !strings.Contains("docker-container,docker-compose,git-compose,container,compose,vm", workload.Type) {
			cobra.CheckErr(fmt.Errorf("type must be docker-container, docker-compose, git-compose, container, compose, or vm"))
		}

		// Spec-file mode allows schedule syntax for scheduler/agent apply.
		if workloadSpecFile != "" {
			if workload.ID == "" {
				cobra.CheckErr(fmt.Errorf("--id is required when using --spec-file"))
			}
			if workload.Type == "" {
				cobra.CheckErr(fmt.Errorf("--type is required when using --spec-file"))
			}
		} else {
			if workload.Type == "docker-container" && workload.Image == "" {
				cobra.CheckErr(fmt.Errorf("image is required for docker-container"))
			}
		}

		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		if workloadSpecFile != "" {
			target := strings.TrimSpace(cfg.GRPCTarget)
			if target == "" {
				target = "scheduler"
			}
			switch target {
			case "scheduler":
				spec, err := buildSchedulerWorkloadSpec(workload.Type, workloadSpecFile)
				cobra.CheckErr(err)
				resp, err := c.ApplySchedulerWorkload(&controlv1.ApplyWorkloadRequest{
					WorkloadId:   workload.ID,
					RevisionId:   workloadRevision,
					DesiredState: normalizeDesiredState(workloadDesired),
					Spec:         spec,
				})
				cobra.CheckErr(err)
				out := map[string]any{
					"target":      "scheduler",
					"transport":   cfg.Transport,
					"workload_id": workload.ID,
					"accepted":    resp.GetSuccess(),
				}
				if !resp.GetSuccess() {
					out["error_message"] = resp.GetErrorMessage()
					out["failure_reason"] = resp.GetFailureReason().String()
				}
				if cfg.Transport == "http" {
					if clusters, err := c.GatewayClusters(); err == nil && strings.TrimSpace(clusters.DefaultClusterID) != "" {
						out["cluster_id"] = strings.TrimSpace(clusters.DefaultClusterID)
					}
				}
				if getResp, err := c.GetWorkload(workload.ID); err == nil && getResp.GetWorkload() != nil {
					w := getResp.GetWorkload()
					out["status"] = w.GetStatus()
					out["assigned_node_id"] = w.GetAssignedNodeId()
					out["revision_id"] = w.GetRevisionId()
				}
				data, err := json.MarshalIndent(out, "", "  ")
				cobra.CheckErr(err)
				fmt.Println(string(data))
				return
			case "agent":
				if cfg.Transport != "grpc" {
					cobra.CheckErr(fmt.Errorf("--spec-file with --grpc-target agent requires --transport grpc"))
				}
				req, err := buildAgentApplyRequestFromSpec(workload.ID, workload.Type, workloadSpecFile, workloadRevision, workloadDesired)
				cobra.CheckErr(err)
				resp, err := c.ApplyAgentWorkload(req)
				cobra.CheckErr(err)
				out := map[string]any{
					"target":      "agent",
					"transport":   cfg.Transport,
					"workload_id": workload.ID,
					"applied":     resp.GetApplied(),
				}
				if !resp.GetApplied() {
					out["message"] = resp.GetMessage()
				}
				data, err := json.MarshalIndent(out, "", "  ")
				cobra.CheckErr(err)
				fmt.Println(string(data))
				return
			default:
				cobra.CheckErr(fmt.Errorf("unsupported grpc target %q (expected scheduler or agent)", cfg.GRPCTarget))
			}
		}

		resp, err := c.ScheduleWorkload(workload)
		cobra.CheckErr(err)
		fmt.Printf("Workload %s scheduled on %s (status: %s)\n", resp.WorkloadID, resp.NodeID, resp.Status)
	},
}

var workloadListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workloads",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		workloads, err := c.ListWorkloads(workloadListNodeID, workloadListStatus)
		cobra.CheckErr(err)
		data, err := json.MarshalIndent(formatWorkloadsForOutput(workloads), "", "  ")
		cobra.CheckErr(err)
		fmt.Println(string(data))
	},
}

var workloadGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get workload details (scheduler gRPC)",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.GetWorkload(workloadGetID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var workloadDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete workload (scheduler gRPC)",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.DeleteWorkload(workloadDeleteID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var workloadRetryCmd = &cobra.Command{
	Use:   "retry",
	Short: "Retry workload (scheduler gRPC)",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.RetryWorkload(workloadRetryID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var workloadStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Set desired state to running",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()
		resp, err := c.ApplySchedulerWorkload(&controlv1.ApplyWorkloadRequest{
			WorkloadId:   workloadStartID,
			DesiredState: "Running",
		})
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var workloadStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Set desired state to stopped",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()
		resp, err := c.ApplySchedulerWorkload(&controlv1.ApplyWorkloadRequest{
			WorkloadId:   workloadStopID,
			DesiredState: "Stopped",
		})
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var workloadRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Stop then start workload",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()
		stopResp, err := c.ApplySchedulerWorkload(&controlv1.ApplyWorkloadRequest{
			WorkloadId:   workloadRestartID,
			DesiredState: "Stopped",
		})
		cobra.CheckErr(err)
		if !stopResp.GetSuccess() {
			printProto(stopResp)
			return
		}
		cobra.CheckErr(waitForSchedulerWorkloadStatus(c, workloadRestartID, "Stopped", 90*time.Second))
		startResp, err := c.ApplySchedulerWorkload(&controlv1.ApplyWorkloadRequest{
			WorkloadId:   workloadRestartID,
			DesiredState: "Running",
		})
		cobra.CheckErr(err)
		printProto(startResp)
	},
}

func init() {
	rootCmd.AddCommand(workloadCmd)
	workloadCmd.AddCommand(workloadScheduleCmd)
	workloadCmd.AddCommand(workloadListCmd)
	workloadCmd.AddCommand(workloadGetCmd)
	workloadCmd.AddCommand(workloadDeleteCmd)
	workloadCmd.AddCommand(workloadRetryCmd)
	workloadCmd.AddCommand(workloadStartCmd)
	workloadCmd.AddCommand(workloadStopCmd)
	workloadCmd.AddCommand(workloadRestartCmd)

	workloadScheduleCmd.Flags().String("id", "", "Workload ID")
	workloadScheduleCmd.Flags().String("name", "", "Workload name")
	workloadScheduleCmd.Flags().String("type", "", "Workload type")
	workloadScheduleCmd.Flags().String("image", "", "Docker image")
	workloadScheduleCmd.Flags().String("command", "", "Command")
	workloadScheduleCmd.Flags().String("compose", "", "Compose content (raw YAML or base64)")
	workloadScheduleCmd.Flags().String("git-repo", "", "Git repository URL")
	workloadScheduleCmd.Flags().String("git-branch", "main", "Git branch")
	workloadScheduleCmd.Flags().String("git-token", "", "Git auth token")
	workloadScheduleCmd.Flags().String("local-path", "", "Local Compose path")
	workloadScheduleCmd.Flags().String("env", "", "Environment variables (key1=value1,key2=value2)")
	workloadScheduleCmd.Flags().StringArray("ports", []string{}, "Ports to expose (e.g., 8080:80)")
	workloadScheduleCmd.Flags().StringArray("volumes", []string{}, "Volumes to mount (e.g., /host/path:/container/path)")
	workloadScheduleCmd.Flags().String("network", "", "Network for the container")
	workloadScheduleCmd.Flags().String("restart-policy", "no", "Restart policy")
	workloadScheduleCmd.Flags().StringVar(&workloadSpecFile, "spec-file", "", "Path to JSON spec file (gRPC mode)")
	workloadScheduleCmd.Flags().StringVar(&workloadRevision, "revision", "rev-1", "Workload revision ID (spec-file mode)")
	workloadScheduleCmd.Flags().StringVar(&workloadDesired, "desired-state", "running", "Desired state: running|stopped (spec-file mode)")

	workloadListCmd.Flags().StringVar(&workloadListStatus, "status", "", "Filter by status (scheduler target)")
	workloadListCmd.Flags().StringVar(&workloadListNodeID, "node-id", "", "Filter by node id (scheduler target)")

	workloadGetCmd.Flags().StringVar(&workloadGetID, "id", "", "Workload ID")
	workloadDeleteCmd.Flags().StringVar(&workloadDeleteID, "id", "", "Workload ID")
	workloadRetryCmd.Flags().StringVar(&workloadRetryID, "id", "", "Workload ID")
	workloadStartCmd.Flags().StringVar(&workloadStartID, "id", "", "Workload ID")
	workloadStopCmd.Flags().StringVar(&workloadStopID, "id", "", "Workload ID")
	workloadRestartCmd.Flags().StringVar(&workloadRestartID, "id", "", "Workload ID")
	cobra.CheckErr(workloadGetCmd.MarkFlagRequired("id"))
	cobra.CheckErr(workloadDeleteCmd.MarkFlagRequired("id"))
	cobra.CheckErr(workloadRetryCmd.MarkFlagRequired("id"))
	cobra.CheckErr(workloadStartCmd.MarkFlagRequired("id"))
	cobra.CheckErr(workloadStopCmd.MarkFlagRequired("id"))
	cobra.CheckErr(workloadRestartCmd.MarkFlagRequired("id"))
}

func waitForSchedulerWorkloadStatus(
	c interface {
		GetWorkload(workloadID string) (*controlv1.GetWorkloadResponse, error)
	},
	workloadID string,
	expectedStatus string,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	lastStatus := "unknown"
	for time.Now().Before(deadline) {
		resp, err := c.GetWorkload(workloadID)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if w := resp.GetWorkload(); w != nil {
			lastStatus = strings.TrimSpace(w.GetStatus())
			if strings.EqualFold(lastStatus, expectedStatus) {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for workload %s status=%s (last status=%s)", workloadID, expectedStatus, lastStatus)
}

func buildAgentApplyRequestFromSpec(id, typ, specFile, revision, desired string) (*agentv1.ApplyWorkloadRequest, error) {
	specData, err := os.ReadFile(specFile)
	if err != nil {
		return nil, err
	}

	req := &agentv1.ApplyWorkloadRequest{
		Id:           id,
		RevisionId:   revision,
		DesiredState: desiredState(desired),
		Spec:         &agentv1.WorkloadSpec{},
	}

	switch strings.ToLower(typ) {
	case "container", "docker-container":
		req.Type = agentv1.WorkloadType_WORKLOAD_TYPE_CONTAINER
		container := &agentv1.ContainerSpec{}
		if err := json.Unmarshal(specData, container); err != nil {
			return nil, err
		}
		req.Spec.Spec = &agentv1.WorkloadSpec_Container{Container: container}
	case "compose", "docker-compose":
		req.Type = agentv1.WorkloadType_WORKLOAD_TYPE_COMPOSE
		compose := &agentv1.ComposeSpec{}
		if err := json.Unmarshal(specData, compose); err != nil {
			return nil, err
		}
		req.Spec.Spec = &agentv1.WorkloadSpec_Compose{Compose: compose}
	case "vm":
		req.Type = agentv1.WorkloadType_WORKLOAD_TYPE_VM
		vm := &agentv1.VMSpec{}
		if err := json.Unmarshal(specData, vm); err != nil {
			return nil, err
		}
		req.Spec.Spec = &agentv1.WorkloadSpec_Vm{Vm: vm}
	default:
		return nil, fmt.Errorf("unsupported --type %q, use container|compose|vm", typ)
	}

	return req, nil
}

func parseEnvVars(envStr string) map[string]string {
	envVars := make(map[string]string)
	pairs := strings.Split(envStr, ",")
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			envVars[kv[0]] = kv[1]
		}
	}
	return envVars
}

func formatWorkloadsForOutput(workloads []models.Workload) []map[string]any {
	out := make([]map[string]any, 0, len(workloads))
	for _, w := range workloads {
		item := map[string]any{
			"id":     w.ID,
			"type":   w.Type,
			"status": w.Status,
		}
		if strings.TrimSpace(w.Name) != "" {
			item["name"] = w.Name
		}
		if strings.TrimSpace(w.NodeID) != "" {
			item["nodeId"] = w.NodeID
		}
		if strings.TrimSpace(w.DesiredState) != "" {
			item["desiredState"] = w.DesiredState
		}
		if strings.TrimSpace(w.RevisionID) != "" {
			item["revisionId"] = w.RevisionID
		}
		if w.RetryAttempts > 0 || w.RetryMax > 0 || !w.RetryNextAt.IsZero() {
			retry := map[string]any{
				"attempts": w.RetryAttempts,
				"max":      w.RetryMax,
			}
			if !w.RetryNextAt.IsZero() {
				retry["nextAt"] = w.RetryNextAt.UTC().Format(time.RFC3339)
			}
			item["retry"] = retry
		}
		if strings.TrimSpace(w.FailureReason) != "" {
			item["failureReason"] = w.FailureReason
		}
		if w.Reason != nil {
			reason := map[string]any{}
			if strings.TrimSpace(w.Reason.Code) != "" {
				reason["code"] = w.Reason.Code
			}
			if strings.TrimSpace(w.Reason.Message) != "" {
				reason["message"] = w.Reason.Message
			}
			if !w.Reason.LastTransition.IsZero() {
				reason["lastTransition"] = w.Reason.LastTransition.UTC().Format(time.RFC3339)
			}
			if !w.Reason.NextRetryAt.IsZero() {
				reason["nextRetryAt"] = w.Reason.NextRetryAt.UTC().Format(time.RFC3339)
			}
			if w.Reason.Retryable {
				reason["retryable"] = true
			}
			if len(reason) > 0 {
				item["reason"] = reason
			}
		}
		if w.Usage != nil {
			usage := map[string]any{
				"cpuPercent":     w.Usage.CPUPercent,
				"memoryBytes":    w.Usage.MemoryBytes,
				"diskReadBytes":  w.Usage.DiskReadBytes,
				"diskWriteBytes": w.Usage.DiskWriteBytes,
				"netRxBytes":     w.Usage.NetRXBytes,
				"netTxBytes":     w.Usage.NetTXBytes,
			}
			if strings.TrimSpace(w.Usage.WorkloadID) != "" {
				usage["workloadId"] = w.Usage.WorkloadID
			}
			if strings.TrimSpace(w.Usage.Type) != "" {
				usage["type"] = w.Usage.Type
			}
			if strings.TrimSpace(w.Usage.Source) != "" {
				usage["source"] = w.Usage.Source
			}
			if !w.Usage.CollectedAt.IsZero() {
				usage["collectedAt"] = w.Usage.CollectedAt.UTC().Format(time.RFC3339)
			}
			item["usage"] = usage
		}
		if strings.TrimSpace(w.Message) != "" {
			item["message"] = w.Message
		}
		if !w.CreatedAt.IsZero() {
			item["createdAt"] = w.CreatedAt.UTC().Format(time.RFC3339)
		}
		if !w.LastUpdated.IsZero() {
			item["lastUpdated"] = w.LastUpdated.UTC().Format(time.RFC3339)
		}
		if len(w.Metadata) > 0 {
			item["metadata"] = w.Metadata
		}
		out = append(out, item)
	}
	return out
}
