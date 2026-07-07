package ingestion_test

import (
	"testing"

	"github.com/persys-dev/persysctl/internal/ingestion"
	"github.com/persys-dev/persysctl/internal/types"
)

var simpleYAML = []byte(`
apiVersion: persys.io/v1
kind: Workload
metadata:
  name: web
spec:
  image: nginx:latest
  resources:
    cpu: 0.5
    memoryMb: 256
`)

var simpleJSON = []byte(`{
  "apiVersion": "persys.io/v1",
  "kind": "Workload",
  "metadata": {"name": "api"},
  "spec": {"image": "myapp:v1"}
}`)

var composeYAML = []byte(`
services:
  web:
    image: nginx:latest
    ports:
      - "80:80"
`)

func TestFromYAML_ParsesWorkload(t *testing.T) {
	w, err := ingestion.FromYAML(simpleYAML)
	if err != nil {
		t.Fatalf("FromYAML: %v", err)
	}
	if w.Name != "web" {
		t.Errorf("expected name %q, got %q", "web", w.Name)
	}
	if w.Image != "nginx:latest" {
		t.Errorf("expected image %q, got %q", "nginx:latest", w.Image)
	}
}

func TestFromJSON_ParsesWorkload(t *testing.T) {
	w, err := ingestion.FromJSON(simpleJSON)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if w.Name != "api" {
		t.Errorf("expected name %q, got %q", "api", w.Name)
	}
}

func TestFromCompose_SetsType(t *testing.T) {
	w, err := ingestion.FromCompose(composeYAML)
	if err != nil {
		t.Fatalf("FromCompose: %v", err)
	}
	if w.Type != types.WorkloadCompose {
		t.Errorf("expected type %q, got %q", types.WorkloadCompose, w.Type)
	}
	if w.ComposeSpec == "" {
		t.Error("expected ComposeSpec to be populated")
	}
}

func TestFromGitURL_ValidURL(t *testing.T) {
	w, err := ingestion.FromGitURL("https://github.com/myorg/app.git", "v1.0", "")
	if err != nil {
		t.Fatalf("FromGitURL: %v", err)
	}
	if w.Git == nil {
		t.Fatal("expected Git source to be set")
	}
	if w.Git.Ref != "v1.0" {
		t.Errorf("expected ref %q, got %q", "v1.0", w.Git.Ref)
	}
	if w.Name != "app" {
		t.Errorf("expected name %q, got %q", "app", w.Name)
	}
}

func TestFromGitURL_DefaultsRefToMain(t *testing.T) {
	w, err := ingestion.FromGitURL("https://github.com/myorg/svc.git", "", "")
	if err != nil {
		t.Fatalf("FromGitURL: %v", err)
	}
	if w.Git.Ref != "main" {
		t.Errorf("expected default ref %q, got %q", "main", w.Git.Ref)
	}
}

func TestIsGitURL(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"https://github.com/org/repo.git", true},
		{"git@github.com:org/repo.git", true},
		{"ssh://git@host/repo.git", true},
		{"https://example.com/page", false},
		{"not-a-url", false},
	}
	for _, tc := range cases {
		got := ingestion.IsGitURL(tc.input)
		if got != tc.want {
			t.Errorf("IsGitURL(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
