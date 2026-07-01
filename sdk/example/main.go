package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/persys-dev/persys-cloud/sdk"
	"github.com/persys-dev/persys-cloud/sdk/options"
)

func main() {
	ctx := context.Background()
	
	// === 1. Full configuration using pointer ===
	cfg := options.DefaultOptions() // *Options

	// Override key settings
	cfg.APIEndpoint = "https://localhost:8551"
	cfg.Timeout = 60 * time.Second
	cfg.UseCertManager = true

	// Identity configuration
	cfg.Identity.VaultAddr = "http://localhost:8200"
	cfg.VaultManagerAddr = "localhost:50069"
	cfg.Identity.PKIMount = "pki"
	cfg.Identity.PKIRole = "persys-sdk"
	cfg.Identity.ServiceName = "my-app-service"
	cfg.Identity.TTL = "12h"

	// Internal cert paths (optional)
	cfg.TLSCertPath = "/tmp/persys-sdk-cert.pem"
	cfg.TLSKeyPath = "/tmp/persys-sdk-key.pem"
	cfg.TLSCAPath = "/tmp/persys-sdk-ca.pem"

	// Create client
	client, err := sdk.New(
		func(o *options.Options) error {
			*o = *cfg // copy value from pointer
			return nil
		},
	)
	if err != nil {
		log.Fatalf("SDK New failed: %v", err)
	}
	defer client.Close()

	fmt.Println("✅ SDK initialized with full options")

	// 1. List workloads
	fmt.Println("\nListing workloads...")
	list, err := client.Workloads().List(ctx, "")
	if err != nil {
		log.Printf("List failed: %v", err)
	} else {
		fmt.Printf("Found %d workloads\n", len(list))
	}

	// Example cluster and forgery ops
	fmt.Println("\nCluster summary...")
	summary, err := client.Clusters().Summary(ctx)
	if err != nil {
		log.Printf("Summary: %v", err)
	} else {
		fmt.Printf("Cluster: %+v\n", summary)
	}

	fmt.Println("\nForgery example (dry-run)...")
	// forgerySpec := map[string]interface{}{"project": "demo"}
	// err = client.Forgery().UpsertProject(ctx, forgerySpec)

	// === 2. Simple functional option (recommended) ===
	prodClient, err := sdk.New(
		sdk.WithEndpoint("https://api.prod.persys.local"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer prodClient.Close()

	// === 3. Insecure development mode ===
	devClient, err := sdk.New(
		sdk.WithEndpoint("https://localhost:8443"),
		func(o *options.Options) error {
			*o = *options.WithInsecure(o)
			return nil
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	defer devClient.Close()

	fmt.Println("✅ All option patterns demonstrated successfully!")
}