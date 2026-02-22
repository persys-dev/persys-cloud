package services

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/forgeryv1"
	"github.com/persys-dev/persys-cloud/persys-gateway/models"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type GithubServiceImpl struct {
	cfg       *config.Config
	tlsClient *tls.Config
}

func NewGithubService(_ *mongo.Collection, _ context.Context, cfg *config.Config, tlsClient *tls.Config) GithubService {
	return &GithubServiceImpl{cfg: cfg, tlsClient: tlsClient}
}

func (g *GithubServiceImpl) SetAccessToken(user *models.DBResponse) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, conn, err := g.forgeryClient(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = client.StoreGitHubCredential(ctx, &forgeryv1.StoreGitHubCredentialRequest{
		UserId:      fmt.Sprintf("%d", user.UserID),
		UserLogin:   user.Login,
		AccessToken: user.GithubToken,
	})
	return err
}

func (g *GithubServiceImpl) ListRepos(user *models.DBResponse) ([]map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, conn, err := g.forgeryClient(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	resp, err := client.ListUserRepositories(ctx, &forgeryv1.ListUserRepositoriesRequest{
		UserId:    fmt.Sprintf("%d", user.UserID),
		UserLogin: user.Login,
	})
	if err != nil {
		return nil, err
	}
	if !resp.GetOk() {
		return nil, fmt.Errorf(resp.GetMessage())
	}
	out := make([]map[string]interface{}, 0, len(resp.GetRepositories()))
	for _, repo := range resp.GetRepositories() {
		out = append(out, map[string]interface{}{
			"full_name": repo.GetFullName(),
			"owner":     repo.GetOwner(),
			"name":      repo.GetName(),
			"clone_url": repo.GetCloneUrl(),
			"private":   repo.GetPrivate(),
		})
	}
	return out, nil
}

func (g *GithubServiceImpl) SetWebhook(user *models.DBResponse, repository string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, conn, err := g.forgeryClient(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := client.RegisterWebhook(ctx, &forgeryv1.RegisterWebhookRequest{
		UserId:        fmt.Sprintf("%d", user.UserID),
		UserLogin:     user.Login,
		Repository:    strings.TrimSpace(repository),
		WebhookUrl:    g.cfg.GitHub.WebHookURL,
		WebhookSecret: g.cfg.GitHub.DefaultSecret,
	})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf(resp.GetMessage())
	}
	return nil
}

func (g *GithubServiceImpl) forgeryClient(ctx context.Context) (forgeryv1.ForgeryControlClient, *grpc.ClientConn, error) {
	conn, err := grpc.DialContext(ctx, g.cfg.Forgery.GRPCAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(g.tlsClient)),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, nil, err
	}
	return forgeryv1.NewForgeryControlClient(conn), conn, nil
}
