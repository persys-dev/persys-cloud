// Package ingestion converts user manifests into SDK/protobuf-ready structures.
package ingestion

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/persys-dev/persys-cloud/sdk/types"
	"gopkg.in/yaml.v3"
)

// Document is a normalized ingestion result.
type Document struct {
	Stack     *types.PersysStack
	Raw       map[string]interface{}
	GitSource *types.GitSource
}

// Convert parses YAML, JSON, Docker Compose, or Git URL input.
func Convert(data []byte) (*Document, error) {
	text := strings.TrimSpace(string(data))
	if IsGitURL(text) {
		return &Document{GitSource: &types.GitSource{URL: text}}, nil
	}
	var raw map[string]interface{}
	if json.Unmarshal(data, &raw) != nil {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
	}
	doc := &Document{Raw: raw}
	if kind, _ := raw["kind"].(string); strings.EqualFold(kind, "PersysStack") || raw["services"] != nil {
		stack := &types.PersysStack{}
		buf, _ := json.Marshal(raw)
		_ = json.Unmarshal(buf, stack)
		doc.Stack = stack
	}
	return doc, nil
}

// IsGitURL reports whether value looks like a remote Git repository URL.
func IsGitURL(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "git@") || strings.HasSuffix(value, ".git") {
		return true
	}
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "ssh") && strings.Contains(u.Path, ".git")
}

// EncodeCompose returns the base64 representation persysctl historically sent for compose files.
func EncodeCompose(data []byte) string { return base64.StdEncoding.EncodeToString(data) }
