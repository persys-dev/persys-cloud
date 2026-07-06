package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "View Persys Compute metrics",
	Long:  `Retrieves node and workload metrics from Persys Compute.`,
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()
		metrics, err := c.GetMetrics()
		cobra.CheckErr(err)
		data, err := json.MarshalIndent(metrics, "", "  ")
		cobra.CheckErr(err)
		fmt.Println(string(data))
	},
}

func init() {
	rootCmd.AddCommand(metricsCmd)
}
