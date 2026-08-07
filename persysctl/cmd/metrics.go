package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var (
	metricsFrom      string
	metricsTo        string
	meterWorkloadID  string
	meterNodeID      string
	meterType        string
	meterLimit       int
	eventsType       string
	eventsWorkloadID string
	eventsNodeID     string
	eventsLimit      int
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

var meterCmd = &cobra.Command{
	Use:   "meter",
	Short: "Inspect workload usage via the gateway meter API",
}

var meterSummaryCmd = &cobra.Command{
	Use:   "summary [workload-id]",
	Short: "Get usage summary for a workload",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()
		from, err := parseTimeFlag(metricsFrom)
		cobra.CheckErr(err)
		to, err := parseTimeFlag(metricsTo)
		cobra.CheckErr(err)
		resp, err := c.MeterSummary(args[0], from, to)
		cobra.CheckErr(err)
		printJSON(resp)
	},
}

var meterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List current workload usage samples",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()
		resp, err := c.MeterListWorkloads(meterType)
		cobra.CheckErr(err)
		printJSON(resp)
	},
}

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Watch scheduler events through the gateway SSE endpoint",
}

var eventsWatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Stream scheduler events matching optional filters",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()
		resp, err := c.WatchGatewayEvents(eventsType, eventsWorkloadID, eventsNodeID, eventsLimit)
		cobra.CheckErr(err)
		printJSON(resp)
	},
}

func parseTimeFlag(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

func init() {
	rootCmd.AddCommand(metricsCmd)
	rootCmd.AddCommand(meterCmd)
	rootCmd.AddCommand(eventsCmd)
	meterCmd.AddCommand(meterSummaryCmd)
	meterCmd.AddCommand(meterListCmd)
	eventsCmd.AddCommand(eventsWatchCmd)

	meterSummaryCmd.Flags().StringVar(&metricsFrom, "from", "", "Start time in RFC3339 format")
	meterSummaryCmd.Flags().StringVar(&metricsTo, "to", "", "End time in RFC3339 format")
	meterListCmd.Flags().StringVar(&meterType, "type", "", "Workload type filter")
	eventsWatchCmd.Flags().StringVar(&eventsType, "type", "", "Event type filter")
	eventsWatchCmd.Flags().StringVar(&eventsWorkloadID, "workload-id", "", "Filter by workload ID")
	eventsWatchCmd.Flags().StringVar(&eventsNodeID, "node-id", "", "Filter by node ID")
	eventsWatchCmd.Flags().IntVar(&eventsLimit, "limit", 20, "Maximum number of events to print before exiting")
}
