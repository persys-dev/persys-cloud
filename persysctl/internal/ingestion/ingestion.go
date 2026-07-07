// Package ingestion converts external configuration into Persys SDK resources.
//
// Supported sources:
//   - YAML manifests
//   - JSON manifests
//   - Docker Compose files
//   - Git repository URLs (detected automatically)
//
// Flow:
//
//	Configuration (YAML/JSON/Compose/Git)
//	        │
//	        ▼
//	    Ingestion
//	        │
//	        ▼
//	  Persys Resources (sdk/types)
//	        │
//	        ▼
//	   SDK Client
//	        │
//	        ▼
//	  Persys API Gateway
package ingestion

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/persys-dev/persysctl/internal/types"
)

// Source identifies the format of an ingestion input.
type Source string

const (
	SourceYAML    Source = "yaml"
	SourceJSON    Source = "json"
	SourceCompose Source = "compose"
	SourceGit     Source = "git"
)

// manifest is the intermediate YAML/JSON structure for a Persys workload
// manifest file.
type manifest struct {
	APIVersion string         `yaml:"apiVersion" json:"apiVersion"`
	Kind       string         `yaml:"kind"       json:"kind"`
	Metadata   manifestMeta   `yaml:"metadata"   json:"metadata"`
	Spec       types.Workload `yaml:"spec"       json:"spec"`
}

type manifestMeta struct {
	Name   string            `yaml:"name"   json:"name"`
	Labels map[string]string `yaml:"labels" json:"labels"`
}

// FromYAML parses a Persys workload manifest from YAML bytes.
//
//	w, err := ingestion.FromYAML(data)
func FromYAML(data []byte) (*types.Workload, error) {
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("ingestion: parse YAML: %w", err)
	}
	return assembleWorkload(&m), nil
}

// FromJSON parses a Persys workload manifest from JSON bytes.
func FromJSON(data []byte) (*types.Workload, error) {
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("ingestion: parse JSON: %w", err)
	}
	return assembleWorkload(&m), nil
}

// FromCompose converts a Docker Compose YAML document into a Persys workload
// with Type == WorkloadCompose.
//
// The compose spec is stored verbatim; the scheduler's compose runner
// interprets it during deployment.
func FromCompose(data []byte) (*types.Workload, error) {
	// Validate that it parses as YAML before storing.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("ingestion: invalid Compose YAML: %w", err)
	}
	name := ""
	if services, ok := raw["services"].(map[string]any); ok {
		for k := range services {
			name = k
			break
		}
	}
	return &types.Workload{
		Name:        name,
		Type:        types.WorkloadCompose,
		ComposeSpec: string(data),
	}, nil
}

// FromGitURL creates a workload that sources its configuration from a Git
// repository. The ref defaults to "main" when omitted.
//
//	w, err := ingestion.FromGitURL("https://github.com/myorg/app.git", "v1.2.3", "deploy/")
func FromGitURL(repoURL, ref, path string) (*types.Workload, error) {
	if !IsGitURL(repoURL) {
		return nil, fmt.Errorf("ingestion: %q does not look like a Git URL", repoURL)
	}
	if ref == "" {
		ref = "main"
	}
	name := gitBaseName(repoURL)
	return &types.Workload{
		Name: name,
		Git: &types.GitSource{
			URL:  repoURL,
			Ref:  ref,
			Path: path,
		},
	}, nil
}

// Detect returns the likely Source format of the provided bytes or URL string.
func Detect(input string) Source {
	if IsGitURL(input) {
		return SourceGit
	}
	trimmed := strings.TrimSpace(input)
	if strings.HasPrefix(trimmed, "{") {
		return SourceJSON
	}
	if strings.Contains(trimmed, "services:") {
		return SourceCompose
	}
	return SourceYAML
}

// IsGitURL reports whether s looks like a Git remote URL.
func IsGitURL(s string) bool {
	return strings.HasPrefix(s, "https://") && strings.HasSuffix(s, ".git") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasPrefix(s, "ssh://")
}

// assembleWorkload merges manifest metadata into the spec workload.
func assembleWorkload(m *manifest) *types.Workload {
	w := m.Spec
	if w.Name == "" {
		w.Name = m.Metadata.Name
	}
	if w.Labels == nil {
		w.Labels = m.Metadata.Labels
	} else {
		for k, v := range m.Metadata.Labels {
			if _, exists := w.Labels[k]; !exists {
				w.Labels[k] = v
			}
		}
	}
	return &w
}

// gitBaseName derives a workload name from a Git URL.
func gitBaseName(repoURL string) string {
	parts := strings.Split(strings.TrimRight(repoURL, "/"), "/")
	if len(parts) == 0 {
		return "workload"
	}
	base := parts[len(parts)-1]
	return strings.TrimSuffix(base, ".git")
}
