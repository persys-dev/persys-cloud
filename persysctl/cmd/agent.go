package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	agentv1 "github.com/persys-dev/persys-cloud/pkg/agent/api/v1"
	"github.com/spf13/cobra"
)

var (
	agentApplyID          string
	agentApplyType        string
	agentApplySpecFile    string
	agentApplyRevisionID  string
	agentApplyDesired     string
	agentStatusID         string
	agentDeleteID         string
	agentActionWorkloadID string
	agentActionType       string
	agentActionStatus     string
	agentActionLimit      int32
	agentActionNewest     bool
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Standalone compute-agent RPCs",
}

var agentHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check compute-agent health",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.AgentHealthCheck()
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var agentApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply workload to standalone compute-agent from a spec file",
	Run: func(cmd *cobra.Command, args []string) {
		specData, err := os.ReadFile(agentApplySpecFile)
		cobra.CheckErr(err)

		req := &agentv1.ApplyWorkloadRequest{
			Id:           agentApplyID,
			RevisionId:   agentApplyRevisionID,
			DesiredState: desiredState(agentApplyDesired),
			Spec:         &agentv1.WorkloadSpec{},
		}

		switch strings.ToLower(agentApplyType) {
		case "container", "docker-container":
			req.Type = agentv1.WorkloadType_WORKLOAD_TYPE_CONTAINER
			container := &agentv1.ContainerSpec{}
			cobra.CheckErr(json.Unmarshal(specData, container))
			req.Spec.Spec = &agentv1.WorkloadSpec_Container{Container: container}
		case "compose", "docker-compose":
			req.Type = agentv1.WorkloadType_WORKLOAD_TYPE_COMPOSE
			compose := &agentv1.ComposeSpec{}
			cobra.CheckErr(json.Unmarshal(specData, compose))
			req.Spec.Spec = &agentv1.WorkloadSpec_Compose{Compose: compose}
		case "vm":
			req.Type = agentv1.WorkloadType_WORKLOAD_TYPE_VM
			vm := &agentv1.VMSpec{}
			cobra.CheckErr(json.Unmarshal(specData, vm))
			req.Spec.Spec = &agentv1.WorkloadSpec_Vm{Vm: vm}
		default:
			cobra.CheckErr(fmt.Errorf("unsupported --type %q, use container|compose|vm", agentApplyType))
		}

		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.ApplyAgentWorkload(req)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var agentStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get workload status from standalone compute-agent",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.AgentGetWorkloadStatus(agentStatusID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workloads from standalone compute-agent",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		workloads, err := c.ListWorkloads("", "")
		cobra.CheckErr(err)
		data, err := json.MarshalIndent(workloads, "", "  ")
		cobra.CheckErr(err)
		fmt.Println(string(data))
	},
}

var agentDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete workload from standalone compute-agent",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.AgentDeleteWorkload(agentDeleteID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var agentListActionsCmd = &cobra.Command{
	Use:   "list-actions",
	Short: "List compute-agent action/task history",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.AgentListActions(agentActionWorkloadID, agentActionType, agentActionStatus, agentActionLimit, agentActionNewest)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.AddCommand(agentHealthCmd)
	agentCmd.AddCommand(agentApplyCmd)
	agentCmd.AddCommand(agentStatusCmd)
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentDeleteCmd)
	agentCmd.AddCommand(agentListActionsCmd)

	agentApplyCmd.Flags().StringVar(&agentApplyID, "id", "", "Workload ID")
	agentApplyCmd.Flags().StringVar(&agentApplyType, "type", "container", "Workload type: container|compose|vm")
	agentApplyCmd.Flags().StringVar(&agentApplySpecFile, "spec-file", "", "Path to JSON spec file")
	agentApplyCmd.Flags().StringVar(&agentApplyRevisionID, "revision", "rev-1", "Workload revision ID")
	agentApplyCmd.Flags().StringVar(&agentApplyDesired, "desired-state", "running", "Desired state: running|stopped")
	cobra.CheckErr(agentApplyCmd.MarkFlagRequired("id"))
	cobra.CheckErr(agentApplyCmd.MarkFlagRequired("spec-file"))

	agentStatusCmd.Flags().StringVar(&agentStatusID, "id", "", "Workload ID")
	agentDeleteCmd.Flags().StringVar(&agentDeleteID, "id", "", "Workload ID")
	cobra.CheckErr(agentStatusCmd.MarkFlagRequired("id"))
	cobra.CheckErr(agentDeleteCmd.MarkFlagRequired("id"))

	agentListActionsCmd.Flags().StringVar(&agentActionWorkloadID, "workload-id", "", "Filter by workload ID")
	agentListActionsCmd.Flags().StringVar(&agentActionType, "action-type", "", "Filter by action type")
	agentListActionsCmd.Flags().StringVar(&agentActionStatus, "action-status", "", "Filter by action status")
	agentListActionsCmd.Flags().Int32Var(&agentActionLimit, "action-limit", 0, "Limit results (0 = all)")
	agentListActionsCmd.Flags().BoolVar(&agentActionNewest, "newest-first", true, "Sort by newest first")
}

func desiredState(s string) agentv1.DesiredState {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "stopped":
		return agentv1.DesiredState_DESIRED_STATE_STOPPED
	default:
		return agentv1.DesiredState_DESIRED_STATE_RUNNING
	}
}
