package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/persys-dev/persysctl/internal/client"
	"github.com/spf13/cobra"
)

var aiRecommendationStatus string
var aiRecommendationWorkload string

var aiCmd = &cobra.Command{
	Use:   "ai",
	Short: "AI-powered cluster reasoning",
}

var aiQueryCmd = &cobra.Command{
	Use:   "query [question]",
	Short: "Ask intelligence a question",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.AIQuery(client.AIQueryRequest{
			Query:        args[0],
			ContextScope: "cluster",
		})
		cobra.CheckErr(err)
		printJSON(resp)
	},
}

var aiRecommendationsCmd = &cobra.Command{
	Use:   "recommendations",
	Short: "List AI recommendations",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		var (
			recs []client.Recommendation
			err2 error
		)
		if aiRecommendationStatus == "pending" {
			recs, err2 = c.ListPendingRecommendations(aiRecommendationWorkload)
		} else {
			recs, err2 = c.ListRecommendations(aiRecommendationStatus, aiRecommendationWorkload)
		}
		cobra.CheckErr(err2)
		printJSON(recs)
	},
}

var aiApproveRecommendationCmd = &cobra.Command{
	Use:   "approve [recommendation-id]",
	Short: "Approve a pending AI recommendation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		rec, err := c.ApproveRecommendation(args[0])
		cobra.CheckErr(err)
		printJSON(rec)
	},
}

var aiRejectRecommendationCmd = &cobra.Command{
	Use:   "reject [recommendation-id]",
	Short: "Reject a pending AI recommendation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		rec, err := c.RejectRecommendation(args[0])
		cobra.CheckErr(err)
		printJSON(rec)
	},
}

var aiApplyRecommendationCmd = &cobra.Command{
	Use:   "apply [recommendation-id]",
	Short: "Apply an approved AI recommendation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		rec, err := c.ApplyRecommendation(args[0])
		cobra.CheckErr(err)
		printJSON(rec)
	},
}

func printJSON(v interface{}) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println("{}")
		return
	}
	fmt.Println(string(out))
}

func init() {
	rootCmd.AddCommand(aiCmd)
	aiCmd.AddCommand(aiQueryCmd)
	aiCmd.AddCommand(aiRecommendationsCmd)
	aiCmd.AddCommand(aiApproveRecommendationCmd)
	aiCmd.AddCommand(aiRejectRecommendationCmd)
	aiCmd.AddCommand(aiApplyRecommendationCmd)

	aiRecommendationsCmd.Flags().StringVar(&aiRecommendationStatus, "status", "", "Filter by status (e.g. pending, approved, rejected, applied)")
	aiRecommendationsCmd.Flags().StringVar(&aiRecommendationWorkload, "workload", "", "Filter by workload")
}
