package pipeline

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
	"gopkg.in/yaml.v3"
)

type Runner struct{}

type Pipeline struct {
	Steps []PipelineStep `yaml:"steps"`
}

type PipelineStep struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Script string `yaml:"script"`
}

func (r *Runner) Run(ctx context.Context, pipelineYAML string) error {
	var pipeline Pipeline
	if err := yaml.Unmarshal([]byte(pipelineYAML), &pipeline); err != nil {
		return fmt.Errorf("failed to parse pipeline yaml: %w", err)
	}
	for _, step := range pipeline.Steps {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", step.Script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("pipeline step '%s' failed: %v, output: %s", step.Name, err, string(out))
		}
	}
	return nil
}

// DockerClient is an interface for running Docker builds
// This avoids import cycles
type DockerClient interface {
	Build(ctx context.Context, req models.BuildRequest) error
}

func RunPipeline(ctx context.Context, req models.BuildRequest, docker DockerClient) error {
	if req.Pipeline != "" {
		r := &Runner{}
		if err := r.Run(ctx, req.Pipeline); err == nil {
			return nil
		}
		// If pipeline fails, fallback
	}
	// Fallback to Docker Compose or Dockerfile
	return docker.Build(ctx, req)
}
