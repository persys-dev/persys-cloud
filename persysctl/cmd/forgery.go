package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/persys-dev/persysctl/internal/client"
	"github.com/spf13/cobra"
)

var (
	forgeryProjectSpecFile string
	forgeryBuildSpecFile   string
	forgeryWebhookSpecFile string
)

var forgeryCmd = &cobra.Command{
	Use:   "forgery",
	Short: "Test and operate forgery flows through gateway",
}

var forgeryTriggerBuildCmd = &cobra.Command{
	Use:   "trigger-build",
	Short: "Trigger a forgery build using a JSON spec",
	Run: func(cmd *cobra.Command, args []string) {
		if forgeryBuildSpecFile == "" {
			cobra.CheckErr(fmt.Errorf("--spec-file is required"))
		}

		raw, err := os.ReadFile(forgeryBuildSpecFile)
		cobra.CheckErr(err)

		req := client.ForgeryBuildTriggerRequest{}
		cobra.CheckErr(json.Unmarshal(raw, &req))
		if req.ProjectName == "" {
			cobra.CheckErr(fmt.Errorf("project_name is required in spec"))
		}

		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.TriggerForgeryBuild(req)
		cobra.CheckErr(err)
		out, err := json.MarshalIndent(resp, "", "  ")
		cobra.CheckErr(err)
		fmt.Println(string(out))
	},
}

var forgeryUpsertProjectCmd = &cobra.Command{
	Use:   "upsert-project",
	Short: "Create or update a forgery project using a JSON spec",
	Run: func(cmd *cobra.Command, args []string) {
		if forgeryProjectSpecFile == "" {
			cobra.CheckErr(fmt.Errorf("--spec-file is required"))
		}

		raw, err := os.ReadFile(forgeryProjectSpecFile)
		cobra.CheckErr(err)

		req := client.ForgeryUpsertProjectRequest{}
		cobra.CheckErr(json.Unmarshal(raw, &req))
		if req.Name == "" || req.RepoURL == "" {
			cobra.CheckErr(fmt.Errorf("name and repo_url are required in spec"))
		}

		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.UpsertForgeryProject(req)
		cobra.CheckErr(err)
		out, err := json.MarshalIndent(resp, "", "  ")
		cobra.CheckErr(err)
		fmt.Println(string(out))
	},
}

var forgeryTestWebhookCmd = &cobra.Command{
	Use:   "test-webhook",
	Short: "Send a test webhook payload to forgery via gateway",
	Run: func(cmd *cobra.Command, args []string) {
		if forgeryWebhookSpecFile == "" {
			cobra.CheckErr(fmt.Errorf("--spec-file is required"))
		}

		raw, err := os.ReadFile(forgeryWebhookSpecFile)
		cobra.CheckErr(err)

		req := client.ForgeryTestWebhookRequest{}
		cobra.CheckErr(json.Unmarshal(raw, &req))
		if req.Repository == "" {
			cobra.CheckErr(fmt.Errorf("repository is required in spec"))
		}

		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.SendForgeryTestWebhook(req)
		cobra.CheckErr(err)
		out, err := json.MarshalIndent(resp, "", "  ")
		cobra.CheckErr(err)
		fmt.Println(string(out))
	},
}

func init() {
	rootCmd.AddCommand(forgeryCmd)
	forgeryCmd.AddCommand(forgeryUpsertProjectCmd)
	forgeryCmd.AddCommand(forgeryTriggerBuildCmd)
	forgeryCmd.AddCommand(forgeryTestWebhookCmd)

	forgeryUpsertProjectCmd.Flags().StringVar(&forgeryProjectSpecFile, "spec-file", "", "Path to project upsert JSON spec")
	forgeryTriggerBuildCmd.Flags().StringVar(&forgeryBuildSpecFile, "spec-file", "", "Path to build trigger JSON spec")
	forgeryTestWebhookCmd.Flags().StringVar(&forgeryWebhookSpecFile, "spec-file", "", "Path to webhook JSON spec")
}
