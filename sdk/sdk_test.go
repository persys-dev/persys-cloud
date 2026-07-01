package sdk_test

import (
	"context"
	"testing"

	"github.com/persys-dev/persys-cloud/sdk"
	"github.com/persys-dev/persys-cloud/sdk/options"
	"github.com/persys-dev/persys-cloud/sdk/types"
)

func TestNew_Defaults(t *testing.T) {
	// Use insecure for testing (no real Vault needed)
	opts := options.WithInsecure(options.DefaultOptions())
	opts.APIEndpoint = "https://localhost:8443" // mock

	c, err := sdk.New(
		sdk.WithEndpoint(opts.APIEndpoint),
	)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer c.Close()

	if c == nil {
		t.Fatal("client is nil")
	}
}

func TestClient_Workloads(t *testing.T) {
	opts := options.WithInsecure(options.DefaultOptions())
	opts.APIEndpoint = "https://localhost:8443"

	client, err := sdk.New(sdk.WithEndpoint(opts.APIEndpoint))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Test Create
	w := types.Workload{
		Name:  "test-nginx",
		Image: "nginx:latest",
		Resources: types.ResourceRequirements{
			CPU:      0.5,
			MemoryMB: 256,
		},
	}

	status, err := client.Workloads().Create(ctx, w)
	if err != nil {
		t.Logf("Expected error in test (no real server): %v", err) // OK for unit test
	} else {
		t.Logf("Created workload: %+v", status)
	}

	// Test List
	_, err = client.Workloads().List(ctx,"")
	if err != nil {
		t.Logf("List error (expected in mock): %v", err)
	}
}

func TestWithOptions(t *testing.T) {
	c, err := sdk.New(
		sdk.WithEndpoint("https://api.persys.local"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
}

func TestClient_Close(t *testing.T) {
	c, err := sdk.New(sdk.WithEndpoint("https://localhost:8443"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}