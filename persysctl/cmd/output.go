package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/persys-dev/persysctl/internal/client"
	"github.com/persys-dev/persysctl/internal/config"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func printProto(msg proto.Message) {
	if msg == nil {
		fmt.Println("{}")
		return
	}
	b, err := protojson.MarshalOptions{Indent: "  "}.Marshal(msg)
	if err != nil {
		fmt.Println("{}")
		return
	}
	fmt.Println(string(b))
}

func newClientWithTrace() (*client.Client, config.Config, error) {
	cfg := config.GetConfig()
	c, err := client.NewClient(cfg)
	if err != nil {
		return nil, cfg, err
	}
	printRouteTrace(cfg, c)
	return c, cfg, nil
}

func printRouteTrace(cfg config.Config, c *client.Client) {
	if cfg.Transport == "http" {
		clusterID := "unknown"
		if clusters, err := c.GatewayClusters(); err == nil && strings.TrimSpace(clusters.DefaultClusterID) != "" {
			clusterID = strings.TrimSpace(clusters.DefaultClusterID)
		}
		_, _ = fmt.Fprintf(os.Stderr, "trace: target=gateway endpoint=%s cluster=%s\n", cfg.APIEndpoint, clusterID)
		return
	}
	if cfg.Transport == "grpc" {
		target := strings.TrimSpace(cfg.GRPCTarget)
		if target == "" {
			target = "scheduler"
		}
		_, _ = fmt.Fprintf(os.Stderr, "trace: target=%s endpoint=%s\n", target, cfg.GRPCEndpoint)
	}
}
