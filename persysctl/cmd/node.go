package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	nodeListStatus string
	nodeGetID      string

	nodeDrainReason   string
	nodeUndrainReason string

	nodeTaintKey    string
	nodeTaintValue  string
	nodeTaintEffect string

	nodeUntaintKey    string
	nodeUntaintEffect string

	nodeLabelKey   string
	nodeLabelValue string

	nodeDeleteLabelKey string
)

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Manage nodes",
}

var nodeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List nodes",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		nodes, err := c.ListNodes(nodeListStatus)
		cobra.CheckErr(err)
		data, err := json.MarshalIndent(nodes, "", "  ")
		cobra.CheckErr(err)
		fmt.Println(string(data))
	},
}

var nodeGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get node details (scheduler gRPC)",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.GetNode(nodeGetID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var nodeDrainCmd = &cobra.Command{
	Use:   "drain [node-id]",
	Short: "Drain a node (mark it draining and relocate its workloads)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.DrainNode(args[0], nodeDrainReason)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var nodeUndrainCmd = &cobra.Command{
	Use:   "undrain [node-id]",
	Short: "Undrain a node (mark it ready again)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.UndrainNode(args[0], nodeUndrainReason)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var nodeTaintCmd = &cobra.Command{
	Use:   "taint [node-id]",
	Short: "Add a taint to a node",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if nodeTaintKey == "" {
			cobra.CheckErr(fmt.Errorf("--key is required"))
		}
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.TaintNode(args[0], nodeTaintKey, nodeTaintValue, nodeTaintEffect)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var nodeUntaintCmd = &cobra.Command{
	Use:   "untaint [node-id]",
	Short: "Remove a taint from a node",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if nodeUntaintKey == "" {
			cobra.CheckErr(fmt.Errorf("--key is required"))
		}
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.UntaintNode(args[0], nodeUntaintKey, nodeUntaintEffect)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var nodeSetLabelCmd = &cobra.Command{
	Use:   "set-label [node-id]",
	Short: "Set a label on a node",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if nodeLabelKey == "" {
			cobra.CheckErr(fmt.Errorf("--key is required"))
		}
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.SetNodeLabel(args[0], nodeLabelKey, nodeLabelValue)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var nodeDeleteLabelCmd = &cobra.Command{
	Use:   "delete-label [node-id]",
	Short: "Delete a label from a node",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if nodeDeleteLabelKey == "" {
			cobra.CheckErr(fmt.Errorf("--key is required"))
		}
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.DeleteNodeLabel(args[0], nodeDeleteLabelKey)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

func init() {
	rootCmd.AddCommand(nodeCmd)
	nodeCmd.AddCommand(nodeListCmd)
	nodeCmd.AddCommand(nodeGetCmd)
	nodeCmd.AddCommand(nodeDrainCmd)
	nodeCmd.AddCommand(nodeUndrainCmd)
	nodeCmd.AddCommand(nodeTaintCmd)
	nodeCmd.AddCommand(nodeUntaintCmd)
	nodeCmd.AddCommand(nodeSetLabelCmd)
	nodeCmd.AddCommand(nodeDeleteLabelCmd)

	nodeListCmd.Flags().StringVar(&nodeListStatus, "status", "", "Filter by status: Ready|NotReady|Draining")

	nodeGetCmd.Flags().StringVar(&nodeGetID, "id", "", "Node ID")
	cobra.CheckErr(nodeGetCmd.MarkFlagRequired("id"))

	nodeDrainCmd.Flags().StringVar(&nodeDrainReason, "reason", "", "Reason for draining the node")
	nodeUndrainCmd.Flags().StringVar(&nodeUndrainReason, "reason", "", "Reason for undraining the node")

	nodeTaintCmd.Flags().StringVar(&nodeTaintKey, "key", "", "Taint key (required)")
	nodeTaintCmd.Flags().StringVar(&nodeTaintValue, "value", "", "Taint value")
	nodeTaintCmd.Flags().StringVar(&nodeTaintEffect, "effect", "NoSchedule", "Taint effect: NoSchedule|PreferNoSchedule")

	nodeUntaintCmd.Flags().StringVar(&nodeUntaintKey, "key", "", "Taint key to remove (required)")
	nodeUntaintCmd.Flags().StringVar(&nodeUntaintEffect, "effect", "", "Taint effect to match")

	nodeSetLabelCmd.Flags().StringVar(&nodeLabelKey, "key", "", "Label key (required)")
	nodeSetLabelCmd.Flags().StringVar(&nodeLabelValue, "value", "", "Label value")

	nodeDeleteLabelCmd.Flags().StringVar(&nodeDeleteLabelKey, "key", "", "Label key to delete (required)")
}
