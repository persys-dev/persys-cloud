package operator

import (
	"context"
	"fmt"
)

type Client struct {
	// TODO: Add k8s client-go fields
}

type BuildCRD struct {
	ProjectName string
	Image       string
	Source      string
	CommitHash  string
	Pipeline    string
}

func (c *Client) CreateBuildCRD(ctx context.Context, crd BuildCRD) error {
	// TODO: Use client-go to create the CRD in the cluster
	fmt.Printf("[stub] Creating Build CRD: %+v\n", crd)
	return nil
}
