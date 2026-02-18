package build

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"archive/tar"
	"bytes"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
)

type Docker struct{}

func (d *Docker) Build(ctx context.Context, req models.BuildRequest) error {
	// Get workspace from metadata (set by orchestrator)
	workspace := "/tmp/forge-builds" // default fallback
	if req.Metadata != nil {
		if ws, ok := req.Metadata["workspace"].(string); ok {
			workspace = ws
		}
	}

	// 1. Clone or pull repo
	repoURL := req.Source
	if _, err := os.Stat(filepath.Join(workspace, ".git")); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, "git", "clone", repoURL, workspace)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone failed: %v, output: %s", err, string(out))
		}
	} else {
		cmd := exec.CommandContext(ctx, "git", "pull")
		cmd.Dir = workspace
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git pull failed: %v, output: %s", err, string(out))
		}
	}

	// 2. Checkout commit if specified
	if req.CommitHash != "" {
		cmd := exec.CommandContext(ctx, "git", "checkout", req.CommitHash)
		cmd.Dir = workspace
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout failed: %v, output: %s", err, string(out))
		}
	}

	// 3. Build/Run
	switch req.Type {
	case models.BuildTypeCompose:
		cmd := exec.CommandContext(ctx, "docker-compose", "up", "--build", "-d")
		cmd.Dir = workspace
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("docker-compose up failed: %v, output: %s", err, string(out))
		}
	case models.BuildTypeDockerfile:
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			return fmt.Errorf("failed to create Docker client: %w", err)
		}
		// Create tar archive of workspace as build context
		buf := new(bytes.Buffer)
		tarWriter := tar.NewWriter(buf)
		err = filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			relPath, err := filepath.Rel(workspace, path)
			if err != nil {
				return err
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = relPath
			if err := tarWriter.WriteHeader(hdr); err != nil {
				return err
			}
			if !info.IsDir() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				if _, err := io.Copy(tarWriter, f); err != nil {
					return err
				}
			}
			return nil
		})
		tarWriter.Close()
		if err != nil {
			return fmt.Errorf("failed to create build context: %w", err)
		}
		imageName := req.ProjectName
		buildResp, err := cli.ImageBuild(ctx, buf, types.ImageBuildOptions{
			Tags:       []string{imageName},
			Remove:     true,
			Dockerfile: "Dockerfile",
		})
		if err != nil {
			return fmt.Errorf("docker build failed: %w", err)
		}
		io.Copy(io.Discard, buildResp.Body)
		buildResp.Body.Close()

		// Only push if PushArtifact is true
		if req.PushArtifact {
			// Tag for Nexus
			nexusRepo := os.Getenv("FORGE_NEXUS_DOCKER_REPO")
			if req.NexusRepo != "" {
				nexusRepo = req.NexusRepo
			}
			if nexusRepo != "" {
				imageTag := fmt.Sprintf("%s/%s:latest", nexusRepo, req.ProjectName)
				if err := cli.ImageTag(ctx, imageName, imageTag); err != nil {
					return fmt.Errorf("docker tag failed: %w", err)
				}
				// Login to Nexus
				nexusUser := os.Getenv("FORGE_NEXUS_USER")
				nexusPass := os.Getenv("FORGE_NEXUS_PASSWORD")
				_, err = cli.RegistryLogin(ctx, registry.AuthConfig{
					Username:      nexusUser,
					Password:      nexusPass,
					ServerAddress: nexusRepo,
				})
				if err != nil {
					return fmt.Errorf("docker login failed: %w", err)
				}
				// Push image
				authConfig := registry.AuthConfig{
					Username:      nexusUser,
					Password:      nexusPass,
					ServerAddress: nexusRepo,
				}
				authBytes, _ := json.Marshal(authConfig)
				encodedAuth := base64.URLEncoding.EncodeToString(authBytes)
				pushResp, err := cli.ImagePush(ctx, imageTag, image.PushOptions{
					RegistryAuth: encodedAuth,
				})
				if err != nil {
					return fmt.Errorf("docker push failed: %w", err)
				}
				io.Copy(io.Discard, pushResp)
				pushResp.Close()
			}
		}

		// Run container if specified
		if req.Metadata != nil {
			if runContainer, ok := req.Metadata["run_container"].(bool); ok && runContainer {
				imageToRun := imageName
				if req.NexusRepo != "" {
					imageToRun = fmt.Sprintf("%s/%s:latest", req.NexusRepo, req.ProjectName)
				}
				_, err = cli.ContainerCreate(ctx, &container.Config{
					Image: imageToRun,
					Cmd:   strslice.StrSlice{},
				}, nil, nil, nil, req.ProjectName)
				if err != nil {
					return fmt.Errorf("docker run failed: %w", err)
				}
				if err := cli.ContainerStart(ctx, req.ProjectName, container.StartOptions{}); err != nil {
					return fmt.Errorf("docker start failed: %w", err)
				}
			}
		}
	case models.BuildTypePipeline:
		return fmt.Errorf("pipeline type not supported in Docker client")
	default:
		return fmt.Errorf("unknown build type: %s", req.Type)
	}
	return nil
}
