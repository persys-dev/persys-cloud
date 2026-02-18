package build

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/operator"
)

type OperatorClient interface {
	CreateBuildCRD(ctx context.Context, crd operator.BuildCRD) error
}

type Orchestrator struct {
	Docker         DockerClient
	OperatorClient OperatorClient
	WorkspaceDir   string
}

type DockerClient interface {
	Build(ctx context.Context, req models.BuildRequest) error
}

// NewOrchestrator creates a new orchestrator with initialized clients
func NewOrchestrator(workspaceDir string) *Orchestrator {
	if workspaceDir == "" {
		workspaceDir = "/tmp/forge-builds"
	}

	return &Orchestrator{
		WorkspaceDir: workspaceDir,
	}
}

// SetDockerClient sets the Docker client for the orchestrator
func (o *Orchestrator) SetDockerClient(client DockerClient) {
	o.Docker = client
}

// SetOperatorClient sets the operator client for the orchestrator
func (o *Orchestrator) SetOperatorClient(client OperatorClient) {
	o.OperatorClient = client
}

// BuildWithStrategy executes a build using the specified strategy
func (o *Orchestrator) BuildWithStrategy(ctx context.Context, req models.BuildRequest, strategy string) error {
	// Use strategy from request if not explicitly provided
	if strategy == "" {
		strategy = req.Strategy
	}

	// Validate required fields
	if err := o.validateBuildRequest(req); err != nil {
		return fmt.Errorf("invalid build request: %w", err)
	}

	// Create workspace for this build
	workspace := o.createWorkspace(req)
	defer o.cleanupWorkspace(workspace)

	log.Printf("Starting build for project %s with strategy %s", req.ProjectName, strategy)

	// Execute build based on strategy
	switch strategy {
	case "local":
		return o.executeLocalBuild(ctx, req, workspace)
	case "operator":
		return o.executeOperatorBuild(ctx, req)
	case "prow":
		return o.executeProwBuild(ctx, req)
	default:
		return fmt.Errorf("unknown build strategy: %s", strategy)
	}
}

// validateBuildRequest validates the build request
func (o *Orchestrator) validateBuildRequest(req models.BuildRequest) error {
	if req.ProjectName == "" {
		return errors.New("project name is required")
	}
	if req.Type == "" {
		return errors.New("build type is required")
	}
	if req.Source == "" {
		return errors.New("source repository is required")
	}
	if req.CommitHash == "" {
		return errors.New("commit hash is required")
	}
	if req.Strategy == "" {
		return errors.New("build strategy is required")
	}

	// Validate build type
	validTypes := map[string]bool{
		models.BuildTypeDockerfile: true,
		models.BuildTypeCompose:    true,
		models.BuildTypePipeline:   true,
	}
	if !validTypes[req.Type] {
		return fmt.Errorf("invalid build type: %s", req.Type)
	}

	return nil
}

// createWorkspace creates a workspace directory for the build
func (o *Orchestrator) createWorkspace(req models.BuildRequest) string {
	timestamp := time.Now().UnixNano()
	workspace := filepath.Join(o.WorkspaceDir, fmt.Sprintf("%s-%d", req.ProjectName, timestamp))

	if err := os.MkdirAll(workspace, 0755); err != nil {
		log.Printf("Warning: failed to create workspace %s: %v", workspace, err)
	}

	return workspace
}

// cleanupWorkspace cleans up the workspace directory
func (o *Orchestrator) cleanupWorkspace(workspace string) {
	if err := os.RemoveAll(workspace); err != nil {
		log.Printf("Warning: failed to cleanup workspace %s: %v", workspace, err)
	}
}

// executeLocalBuild executes a local build using Docker
func (o *Orchestrator) executeLocalBuild(ctx context.Context, req models.BuildRequest, workspace string) error {
	if o.Docker == nil {
		return errors.New("Docker client not initialized")
	}

	log.Printf("Executing local build for %s in workspace %s", req.ProjectName, workspace)

	// Initialize metadata if nil
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}

	// Update request with workspace path
	req.Metadata["workspace"] = workspace

	return o.Docker.Build(ctx, req)
}

// executeOperatorBuild executes a build using Kubernetes operator
func (o *Orchestrator) executeOperatorBuild(ctx context.Context, req models.BuildRequest) error {
	if o.OperatorClient == nil {
		return errors.New("operator client not initialized")
	}

	log.Printf("Executing operator build for %s", req.ProjectName)

	// Create BuildCRD for the operator
	crd := operator.BuildCRD{
		ProjectName: req.ProjectName,
		Image:       o.generateImageName(req),
		Source:      req.Source,
		CommitHash:  req.CommitHash,
		Pipeline:    req.Pipeline,
	}

	return o.OperatorClient.CreateBuildCRD(ctx, crd)
}

// executeProwBuild executes a build using Prow (placeholder for future implementation)
func (o *Orchestrator) executeProwBuild(ctx context.Context, req models.BuildRequest) error {
	log.Printf("Executing Prow build for %s", req.ProjectName)

	// TODO: Implement Prow build strategy
	// This would involve creating a Prow job configuration
	// and submitting it to the Prow cluster

	return fmt.Errorf("prow build strategy not yet implemented")
}

// generateImageName generates an image name for the build
func (o *Orchestrator) generateImageName(req models.BuildRequest) string {
	// Use Nexus repo if specified, otherwise use project name
	if req.NexusRepo != "" {
		return fmt.Sprintf("%s/%s:latest", req.NexusRepo, req.ProjectName)
	}
	return fmt.Sprintf("%s:latest", req.ProjectName)
}

// GetBuildStatus returns the status of a build (placeholder for future implementation)
func (o *Orchestrator) GetBuildStatus(ctx context.Context, buildID string) (string, error) {
	// TODO: Implement build status tracking
	// This would involve querying the build status from the appropriate backend
	return "unknown", fmt.Errorf("build status tracking not yet implemented")
}
