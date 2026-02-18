package models

import "time"

type BuildRequest struct {
	ID           string                 `json:"id,omitempty"`
	ProjectName  string                 `json:"project_name"`
	Type         string                 `json:"type"`
	Source       string                 `json:"source"`
	CommitHash   string                 `json:"commit_hash"`
	Branch       string                 `json:"branch,omitempty"`
	Pipeline     string                 `json:"pipeline,omitempty"`
	Strategy     string                 `json:"strategy"` // local, prow, operator
	PushArtifact bool                   `json:"push_artifact,omitempty"`
	NexusRepo    string                 `json:"nexus_repo,omitempty"`
	WebhookData  map[string]interface{} `json:"webhook_data,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt    time.Time              `json:"created_at,omitempty"`
}

type WebhookData struct {
	EventType   string `json:"event_type"` // push, pull_request, tag
	Repository  string `json:"repository"` // org/repo
	Sender      string `json:"sender"`     // username
	Ref         string `json:"ref"`        // refs/heads/main
	Before      string `json:"before"`     // old commit
	After       string `json:"after"`      // new commit
	PullRequest *struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
	} `json:"pull_request,omitempty"`
}

const (
	BuildTypeCompose    = "compose"
	BuildTypeDockerfile = "dockerfile"
	BuildTypePipeline   = "pipeline"
)
