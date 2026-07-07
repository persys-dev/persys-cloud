package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/persys-dev/persysctl/internal/client"
	"github.com/spf13/cobra"
)

var (
	automationPolicySpecFile   string
	automationIncludeDisabled  bool
	automationAuditLogLimit    int
)

var automationCmd = &cobra.Command{
	Use:   "automation",
	Short: "Manage automation policies through gateway",
}

var automationCreatePolicyCmd = &cobra.Command{
	Use:   "create-policy",
	Short: "Create an automation policy using a JSON spec",
	Run: func(cmd *cobra.Command, args []string) {
		if automationPolicySpecFile == "" {
			cobra.CheckErr(fmt.Errorf("--spec-file is required"))
		}
		raw, err := os.ReadFile(automationPolicySpecFile)
		cobra.CheckErr(err)

		var req client.CreatePolicyRequestSpec
		cobra.CheckErr(json.Unmarshal(raw, &req))
		if req.Name == "" || req.TargetWorkload == "" {
			cobra.CheckErr(fmt.Errorf("name and target_workload are required in spec"))
		}

		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.CreatePolicy(req.ToProto())
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var automationListPoliciesCmd = &cobra.Command{
	Use:   "list-policies",
	Short: "List automation policies",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.ListPolicies(automationIncludeDisabled)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var automationEnablePolicyCmd = &cobra.Command{
	Use:   "enable-policy [policy-id]",
	Short: "Enable an automation policy",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.EnablePolicy(args[0])
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var automationDisablePolicyCmd = &cobra.Command{
	Use:   "disable-policy [policy-id]",
	Short: "Disable an automation policy",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.DisablePolicy(args[0])
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var automationEvaluateCmd = &cobra.Command{
	Use:   "evaluate [policy-id]",
	Short: "Trigger an immediate evaluation of an automation policy",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.EvaluatePolicyNow(args[0])
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var automationAuditLogCmd = &cobra.Command{
	Use:   "audit-log",
	Short: "List automation audit log entries",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.ListAutomationAuditLog(automationAuditLogLimit)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

func init() {
	rootCmd.AddCommand(automationCmd)
	automationCmd.AddCommand(automationCreatePolicyCmd)
	automationCmd.AddCommand(automationListPoliciesCmd)
	automationCmd.AddCommand(automationEnablePolicyCmd)
	automationCmd.AddCommand(automationDisablePolicyCmd)
	automationCmd.AddCommand(automationEvaluateCmd)
	automationCmd.AddCommand(automationAuditLogCmd)

	automationCreatePolicyCmd.Flags().StringVar(&automationPolicySpecFile, "spec-file", "", "Path to policy JSON spec")
	automationListPoliciesCmd.Flags().BoolVar(&automationIncludeDisabled, "include-disabled", false, "Include disabled policies")
	automationAuditLogCmd.Flags().IntVar(&automationAuditLogLimit, "limit", 100, "Maximum audit log entries to return")
}
