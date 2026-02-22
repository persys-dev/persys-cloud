package grpcapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	vault "github.com/hashicorp/vault/api"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-forgery/internal/forgeryv1"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/queue"
	"github.com/persys-dev/persys-cloud/persys-forgery/utils"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Service struct {
	forgeryv1.UnimplementedForgeryControlServer
	cfg             *utils.Config
	db              *gorm.DB
	rdb             *redis.Client
	webhookQueueKey string
	buildQueueKey   string
}

func NewService(cfg *utils.Config, db *gorm.DB, rdb *redis.Client, webhookQueueKey, buildQueueKey string) *Service {
	return &Service{
		cfg:             cfg,
		db:              db,
		rdb:             rdb,
		webhookQueueKey: webhookQueueKey,
		buildQueueKey:   buildQueueKey,
	}
}

func (s *Service) ForwardWebhook(ctx context.Context, req *forgeryv1.ForwardWebhookRequest) (*forgeryv1.ForwardWebhookResponse, error) {
	if req == nil {
		return &forgeryv1.ForwardWebhookResponse{Accepted: false, Message: "request is required"}, nil
	}
	if !req.GetVerified() {
		return &forgeryv1.ForwardWebhookResponse{Accepted: false, Message: "webhook must be pre-verified by gateway"}, nil
	}
	if req.GetRepository() == "" {
		return &forgeryv1.ForwardWebhookResponse{Accepted: false, Message: "repository is required"}, nil
	}

	payload := map[string]interface{}{}
	if req.GetPayloadJson() != "" {
		if err := json.Unmarshal([]byte(req.GetPayloadJson()), &payload); err != nil {
			return &forgeryv1.ForwardWebhookResponse{Accepted: false, Message: fmt.Sprintf("invalid payload_json: %v", err)}, nil
		}
	}

	event := queue.VerifiedWebhookEvent{
		DeliveryID: req.GetDeliveryId(),
		EventType:  req.GetEventType(),
		Repository: req.GetRepository(),
		ClusterID:  req.GetClusterId(),
		Sender:     req.GetSender(),
		Ref:        req.GetRef(),
		Before:     req.GetBefore(),
		After:      req.GetAfter(),
		Payload:    payload,
		ReceivedAt: time.Now().UTC(),
	}

	data, err := json.Marshal(event)
	if err != nil {
		return &forgeryv1.ForwardWebhookResponse{Accepted: false, Message: fmt.Sprintf("failed to marshal event: %v", err)}, nil
	}
	if err := s.rdb.LPush(ctx, s.webhookQueueKey, data).Err(); err != nil {
		return &forgeryv1.ForwardWebhookResponse{Accepted: false, Message: fmt.Sprintf("failed to enqueue webhook: %v", err)}, nil
	}

	return &forgeryv1.ForwardWebhookResponse{Accepted: true, Message: "queued"}, nil
}

func (s *Service) UpsertProject(ctx context.Context, req *forgeryv1.UpsertProjectRequest) (*forgeryv1.ProjectResponse, error) {
	if req == nil || strings.TrimSpace(req.GetName()) == "" || strings.TrimSpace(req.GetRepoUrl()) == "" {
		return &forgeryv1.ProjectResponse{Ok: false, Message: "name and repo_url are required"}, nil
	}

	project := models.Project{
		Name:          strings.TrimSpace(req.GetName()),
		RepoURL:       strings.TrimSpace(req.GetRepoUrl()),
		DefaultBranch: defaultString(req.GetDefaultBranch(), "main"),
		ClusterID:     strings.TrimSpace(req.GetClusterId()),
		BuildType:     defaultString(req.GetBuildType(), models.BuildTypeDockerfile),
		BuildMode:     defaultString(req.GetBuildMode(), "standalone"),
		Strategy:      strings.TrimSpace(req.GetStrategy()),
		NexusRepo:     strings.TrimSpace(req.GetNexusRepo()),
		PipelineYAML:  req.GetPipelineYaml(),
		AutoDeploy:    req.GetAutoDeploy(),
		ImageName:     strings.TrimSpace(req.GetImageName()),
	}
	if project.Strategy == "" {
		if project.BuildMode == "runner" {
			project.Strategy = "operator"
		} else {
			project.Strategy = "local"
		}
	}

	if err := s.db.WithContext(ctx).
		Where("name = ?", project.Name).
		Assign(project).
		FirstOrCreate(&project).Error; err != nil {
		return &forgeryv1.ProjectResponse{Ok: false, Message: err.Error()}, nil
	}
	return &forgeryv1.ProjectResponse{
		Ok:      true,
		Message: "project upserted",
		Project: projectToProto(project),
	}, nil
}

func (s *Service) GetProject(ctx context.Context, req *forgeryv1.GetProjectRequest) (*forgeryv1.ProjectResponse, error) {
	var project models.Project
	if err := s.db.WithContext(ctx).Where("name = ?", strings.TrimSpace(req.GetName())).First(&project).Error; err != nil {
		return &forgeryv1.ProjectResponse{Ok: false, Message: err.Error()}, nil
	}
	return &forgeryv1.ProjectResponse{Ok: true, Project: projectToProto(project)}, nil
}

func (s *Service) ListProjects(ctx context.Context, _ *forgeryv1.ListProjectsRequest) (*forgeryv1.ListProjectsResponse, error) {
	var projects []models.Project
	if err := s.db.WithContext(ctx).Order("name asc").Find(&projects).Error; err != nil {
		return &forgeryv1.ListProjectsResponse{}, err
	}
	resp := &forgeryv1.ListProjectsResponse{Projects: make([]*forgeryv1.Project, 0, len(projects))}
	for _, p := range projects {
		resp.Projects = append(resp.Projects, projectToProto(p))
	}
	return resp, nil
}

func (s *Service) DeleteProject(ctx context.Context, req *forgeryv1.DeleteProjectRequest) (*forgeryv1.OperationStatus, error) {
	if err := s.db.WithContext(ctx).Where("name = ?", strings.TrimSpace(req.GetName())).Delete(&models.Project{}).Error; err != nil {
		return &forgeryv1.OperationStatus{Ok: false, Message: err.Error()}, nil
	}
	return &forgeryv1.OperationStatus{Ok: true, Message: "project deleted"}, nil
}

func (s *Service) StoreGitHubCredential(ctx context.Context, req *forgeryv1.StoreGitHubCredentialRequest) (*forgeryv1.OperationStatus, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" || strings.TrimSpace(req.GetAccessToken()) == "" {
		return &forgeryv1.OperationStatus{Ok: false, Message: "user_id and access_token are required"}, nil
	}
	if err := s.storeToken(ctx, userID, req.GetAccessToken()); err != nil {
		return &forgeryv1.OperationStatus{Ok: false, Message: err.Error()}, nil
	}
	cred := models.GitHubCredential{
		UserID:     userID,
		UserLogin:  strings.TrimSpace(req.GetUserLogin()),
		ScopeCSV:   strings.Join(req.GetScopes(), ","),
		SecretPath: fmt.Sprintf("%s/%s", strings.TrimRight(s.cfg.Vault.KVPathPrefix, "/"), userID),
	}
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Assign(cred).FirstOrCreate(&cred).Error; err != nil {
		return &forgeryv1.OperationStatus{Ok: false, Message: err.Error()}, nil
	}
	return &forgeryv1.OperationStatus{Ok: true, Message: "credential stored"}, nil
}

func (s *Service) ListUserRepositories(ctx context.Context, req *forgeryv1.ListUserRepositoriesRequest) (*forgeryv1.ListUserRepositoriesResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return &forgeryv1.ListUserRepositoriesResponse{Ok: false, Message: "user_id is required"}, nil
	}
	token, err := s.readToken(ctx, userID)
	if err != nil {
		return &forgeryv1.ListUserRepositoriesResponse{Ok: false, Message: err.Error()}, nil
	}

	repos, err := s.githubListRepos(ctx, token)
	if err != nil {
		return &forgeryv1.ListUserRepositoriesResponse{Ok: false, Message: err.Error()}, nil
	}

	resp := &forgeryv1.ListUserRepositoriesResponse{Ok: true, Repositories: make([]*forgeryv1.Repository, 0, len(repos))}
	for _, repo := range repos {
		r := &forgeryv1.Repository{
			FullName: repo.FullName,
			Name:     repo.Name,
			CloneUrl: repo.CloneURL,
			Private:  repo.Private,
		}
		r.Owner = repo.Owner.Login
		resp.Repositories = append(resp.Repositories, r)
	}
	return resp, nil
}

func (s *Service) RegisterWebhook(ctx context.Context, req *forgeryv1.RegisterWebhookRequest) (*forgeryv1.OperationStatus, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" || strings.TrimSpace(req.GetRepository()) == "" {
		return &forgeryv1.OperationStatus{Ok: false, Message: "user_id and repository are required"}, nil
	}
	token, err := s.readToken(ctx, userID)
	if err != nil {
		return &forgeryv1.OperationStatus{Ok: false, Message: err.Error()}, nil
	}
	owner, repo, parseErr := parseRepo(req.GetRepository(), req.GetUserLogin())
	if parseErr != nil {
		return &forgeryv1.OperationStatus{Ok: false, Message: parseErr.Error()}, nil
	}
	webhookURL := strings.TrimSpace(req.GetWebhookUrl())
	if webhookURL == "" {
		webhookURL = strings.TrimSpace(s.cfg.GitHub.WebhookURL)
	}
	if webhookURL == "" {
		return &forgeryv1.OperationStatus{Ok: false, Message: "webhook URL is not configured"}, nil
	}
	webhookSecret := strings.TrimSpace(req.GetWebhookSecret())
	if webhookSecret == "" {
		webhookSecret = strings.TrimSpace(s.cfg.GitHub.DefaultSecret)
	}

	err = s.githubRegisterWebhook(ctx, token, owner, repo, webhookURL, webhookSecret)
	if err != nil {
		return &forgeryv1.OperationStatus{Ok: false, Message: err.Error()}, nil
	}
	return &forgeryv1.OperationStatus{Ok: true, Message: "webhook registered"}, nil
}

func (s *Service) TriggerBuild(ctx context.Context, req *forgeryv1.TriggerBuildRequest) (*forgeryv1.OperationStatus, error) {
	projectName := strings.TrimSpace(req.GetProjectName())
	if projectName == "" {
		return &forgeryv1.OperationStatus{Ok: false, Message: "project_name is required"}, nil
	}
	var project models.Project
	err := s.db.WithContext(ctx).Where("name = ?", projectName).First(&project).Error
	if err != nil {
		return &forgeryv1.OperationStatus{Ok: false, Message: fmt.Sprintf("project lookup failed: %v", err)}, nil
	}

	mode := defaultString(req.GetMode(), project.BuildMode)
	strategy := project.Strategy
	if mode == "runner" {
		strategy = "operator"
	} else if strategy == "" {
		strategy = "local"
	}

	buildType := defaultString(project.BuildType, models.BuildTypeDockerfile)
	branch := strings.TrimPrefix(req.GetRef(), "refs/heads/")
	buildReq := models.BuildRequest{
		ID:           fmt.Sprintf("%s-%d", project.Name, time.Now().UnixNano()),
		ProjectName:  project.Name,
		Type:         buildType,
		Source:       project.RepoURL,
		CommitHash:   req.GetCommitSha(),
		Branch:       branch,
		Pipeline:     project.PipelineYAML,
		Strategy:     strategy,
		PushArtifact: true,
		NexusRepo:    project.NexusRepo,
		WebhookData: map[string]interface{}{
			"event_type": req.GetEventType(),
			"repository": req.GetRepository(),
			"sender":     req.GetSender(),
			"ref":        req.GetRef(),
		},
		Metadata: map[string]interface{}{
			"cluster_id":  defaultString(req.GetClusterId(), project.ClusterID),
			"build_mode":  mode,
			"auto_deploy": project.AutoDeploy,
			"image_name":  defaultString(project.ImageName, project.Name),
		},
		CreatedAt: time.Now().UTC(),
	}

	data, marshalErr := json.Marshal(buildReq)
	if marshalErr != nil {
		return &forgeryv1.OperationStatus{Ok: false, Message: marshalErr.Error()}, nil
	}
	if err := s.rdb.LPush(ctx, s.buildQueueKey, data).Err(); err != nil {
		return &forgeryv1.OperationStatus{Ok: false, Message: err.Error()}, nil
	}
	return &forgeryv1.OperationStatus{Ok: true, Message: "build queued"}, nil
}

func (s *Service) storeToken(ctx context.Context, userID, token string) error {
	client, err := s.vaultClient()
	if err != nil {
		return err
	}
	path := fmt.Sprintf("%s/%s", strings.TrimRight(s.cfg.Vault.KVPathPrefix, "/"), userID)
	_, err = client.Logical().Write(path, map[string]interface{}{
		"data": map[string]interface{}{
			"access_token": token,
		},
	})
	return err
}

func (s *Service) readToken(ctx context.Context, userID string) (string, error) {
	client, err := s.vaultClient()
	if err != nil {
		return "", err
	}
	path := fmt.Sprintf("%s/%s", strings.TrimRight(s.cfg.Vault.KVPathPrefix, "/"), userID)
	secret, err := client.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return "", err
	}
	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("github credential not found in vault")
	}
	rawData, ok := secret.Data["data"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid vault kv response")
	}
	token, _ := rawData["access_token"].(string)
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("empty access token in vault")
	}
	return token, nil
}

func (s *Service) vaultClient() (*vault.Client, error) {
	if !s.cfg.Vault.Enabled {
		return nil, fmt.Errorf("vault is disabled")
	}
	conf := vault.DefaultConfig()
	conf.Address = s.cfg.Vault.Addr
	client, err := vault.NewClient(conf)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(s.cfg.Vault.AuthMethod)) {
	case "token":
		client.SetToken(s.cfg.Vault.Token)
	default:
		return nil, fmt.Errorf("unsupported vault auth_method %q", s.cfg.Vault.AuthMethod)
	}
	return client, nil
}

func parseRepo(repo, fallbackOwner string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	if len(parts) == 1 && strings.TrimSpace(fallbackOwner) != "" {
		return fallbackOwner, parts[0], nil
	}
	return "", "", fmt.Errorf("repository must be <owner>/<name>")
}

func projectToProto(p models.Project) *forgeryv1.Project {
	return &forgeryv1.Project{
		Name:          p.Name,
		RepoUrl:       p.RepoURL,
		DefaultBranch: p.DefaultBranch,
		ClusterId:     p.ClusterID,
		BuildType:     p.BuildType,
		BuildMode:     p.BuildMode,
		Strategy:      p.Strategy,
		NexusRepo:     p.NexusRepo,
		PipelineYaml:  p.PipelineYAML,
		AutoDeploy:    p.AutoDeploy,
		ImageName:     p.ImageName,
	}
}

func defaultString(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

type githubRepository struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	CloneURL string `json:"clone_url"`
	Private  bool   `json:"private"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

func (s *Service) githubListRepos(ctx context.Context, accessToken string) ([]githubRepository, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/repos?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github list repos failed: %s", strings.TrimSpace(string(body)))
	}
	var repos []githubRepository
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, err
	}
	return repos, nil
}

func (s *Service) githubRegisterWebhook(ctx context.Context, accessToken, owner, repo, webhookURL, webhookSecret string) error {
	payload := map[string]interface{}{
		"name":   "web",
		"active": true,
		"events": []string{"push", "pull_request"},
		"config": map[string]string{
			"url":          webhookURL,
			"content_type": "json",
			"insecure_ssl": "0",
			"secret":       webhookSecret,
		},
	}
	body, _ := json.Marshal(payload)
	reqURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/hooks", url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github register webhook failed: %s", strings.TrimSpace(string(raw)))
	}
	return nil
}
