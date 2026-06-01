package scheduler

import "github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"

type workloadSpec struct {
	ID            string            `json:"id,omitempty"`
	Name          string            `json:"name,omitempty"`
	Type          string            `json:"type,omitempty"`
	RevisionID    string            `json:"revisionId,omitempty"`
	Image         string            `json:"image,omitempty"`
	Command       string            `json:"command,omitempty"`
	CommandList   []string          `json:"commandList,omitempty"`
	Compose       string            `json:"compose,omitempty"`
	ComposeYAML   string            `json:"composeYaml,omitempty"`
	ProjectName   string            `json:"projectName,omitempty"`
	GitRepo       string            `json:"gitRepo,omitempty"`
	GitBranch     string            `json:"gitBranch,omitempty"`
	GitToken      string            `json:"gitToken,omitempty"`
	EnvVars       map[string]string `json:"envVars,omitempty"`
	Resources     models.Resources  `json:"resources"`
	DesiredState  string            `json:"desiredState,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	CreatedAt     interface{}       `json:"createdAt,omitempty"`
	LocalPath     string            `json:"localPath,omitempty"`
	Ports         []string          `json:"ports,omitempty"`
	Volumes       []string          `json:"volumes,omitempty"`
	Network       string            `json:"network,omitempty"`
	RestartPolicy string            `json:"restartPolicy,omitempty"`
	VM            *models.VMSpec    `json:"vm,omitempty"`
}

type workloadStatus struct {
	ID           string                    `json:"id,omitempty"`
	AssignedNode string                    `json:"assignedNode,omitempty"`
	NodeID       string                    `json:"nodeId,omitempty"`
	Status       string                    `json:"status,omitempty"`
	Logs         string                    `json:"logs,omitempty"`
	Metadata     map[string]interface{}    `json:"metadata,omitempty"`
	Retry        models.RetryState         `json:"retry"`
	StatusInfo   models.WorkloadStatusInfo `json:"statusInfo"`
}

func workloadSpecFromWorkload(w models.Workload) workloadSpec {
	return workloadSpec{
		ID:            w.ID,
		Name:          w.Name,
		Type:          w.Type,
		RevisionID:    w.RevisionID,
		Image:         w.Image,
		Command:       w.Command,
		CommandList:   w.CommandList,
		Compose:       w.Compose,
		ComposeYAML:   w.ComposeYAML,
		ProjectName:   w.ProjectName,
		GitRepo:       w.GitRepo,
		GitBranch:     w.GitBranch,
		GitToken:      w.GitToken,
		EnvVars:       w.EnvVars,
		Resources:     w.Resources,
		DesiredState:  w.DesiredState,
		Labels:        w.Labels,
		CreatedAt:     w.CreatedAt,
		LocalPath:     w.LocalPath,
		Ports:         w.Ports,
		Volumes:       w.Volumes,
		Network:       w.Network,
		RestartPolicy: w.RestartPolicy,
		VM:            w.VM,
	}
}

func workloadStatusFromWorkload(w models.Workload) workloadStatus {
	return workloadStatus{
		ID:           w.ID,
		AssignedNode: w.AssignedNode,
		NodeID:       w.NodeID,
		Status:       w.Status,
		Logs:         w.Logs,
		Metadata:     w.Metadata,
		Retry:        w.Retry,
		StatusInfo:   w.StatusInfo,
	}
}
