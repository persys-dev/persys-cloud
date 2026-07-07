package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var clusterID string

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Inspect gateway cluster routing state",
}

var clusterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List clusters known by persys-gateway",
	Run: func(cmd *cobra.Command, args []string) {
		c, cfg, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		if cfg.Transport != "http" {
			cobra.CheckErr(fmt.Errorf("cluster commands require --transport http (gateway)"))
		}
		resp, err := c.GatewayClusters()
		cobra.CheckErr(err)

		data, err := json.MarshalIndent(resp, "", "  ")
		cobra.CheckErr(err)
		fmt.Println(string(data))
	},
}

var clusterGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get a single cluster by ID from persys-gateway",
	Run: func(cmd *cobra.Command, args []string) {
		c, cfg, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		if cfg.Transport != "http" {
			cobra.CheckErr(fmt.Errorf("cluster commands require --transport http (gateway)"))
		}
		resp, err := c.GatewayClusters()
		cobra.CheckErr(err)

		for _, cluster := range resp.Clusters {
			if cluster.ID != clusterID {
				continue
			}
			data, err := json.MarshalIndent(cluster, "", "  ")
			cobra.CheckErr(err)
			fmt.Println(string(data))
			return
		}
		cobra.CheckErr(fmt.Errorf("cluster %q not found", clusterID))
	},
}

func init() {
	rootCmd.AddCommand(clusterCmd)
	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterGetCmd)

	clusterGetCmd.Flags().StringVar(&clusterID, "id", "", "Cluster ID")
	cobra.CheckErr(clusterGetCmd.MarkFlagRequired("id"))
}
