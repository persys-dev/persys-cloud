package controllers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
)

type GithubController struct {
	authService   services.AuthService
	githubService services.GithubService
	ctx           context.Context
	config        *config.Config
}

// NewGithubController previously took an unused *mongo.Collection
// parameter — dropped. Nothing in this controller ever read or wrote it;
// every handler goes through authService/githubService instead.
func NewGithubController(authService services.AuthService, ctx context.Context, githubService services.GithubService, cfg *config.Config) GithubController {
	return GithubController{
		authService:   authService,
		githubService: githubService,
		ctx:           ctx,
		config:        cfg,
	}
}

func (gc *GithubController) WebhookHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusGone, gin.H{"error": "legacy github webhook handler removed; use /webhooks/github"})
	}
}

func (gc *GithubController) SetAccessToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := gc.authService.ReadUserData(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unable to resolve user session"})
			return
		}
		if err := gc.githubService.SetAccessToken(user); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "stored"})
	}
}

func (gc *GithubController) SetWebhook() gin.HandlerFunc {
	return func(c *gin.Context) {
		repoName := c.Param("repoName")
		user, err := gc.authService.ReadUserData(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unable to resolve user session"})
			return
		}
		if err := gc.githubService.SetWebhook(user, repoName); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "webhook configured"})
	}
}

func (gc *GithubController) ListRepos() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := gc.authService.ReadUserData(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unable to resolve user session"})
			return
		}
		result, err := gc.githubService.ListRepos(user)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
