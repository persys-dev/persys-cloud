package controllers

import (
	"context"
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	webhook "github.com/go-playground/webhooks/v6/github"
	"github.com/google/go-github/github"
	"github.com/persys-dev/persys-cloud/api-gateway/config"
	"github.com/persys-dev/persys-cloud/api-gateway/services"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/oauth2"
)

type GithubController struct {
	authService   services.AuthService
	githubService services.GithubService
	//userService services.UserService
	ctx        context.Context
	collection *mongo.Collection
	config     *config.Config
}

func NewGithubController(authService services.AuthService, ctx context.Context, githubService services.GithubService, collection *mongo.Collection, cfg *config.Config) GithubController {
	return GithubController{
		authService:   authService,
		githubService: githubService,
		ctx:           ctx,
		collection:    collection,
		config:        cfg,
	}
}

func (gc *GithubController) WebhookHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		log.Println("Received webhook...")

		hook, err := webhook.New(webhook.Options.Secret(gc.config.GitHub.WebHookSecret))
		if err != nil {
			c.AbortWithError(500, err)
			return
		}

		payload, e := hook.Parse(c.Request, webhook.PushEvent)
		if e != nil {
			log.Println("Error parsing", e)
			c.AbortWithError(400, e)
			return
		}

		switch event := payload.(type) {
		case webhook.PushPayload:
			owner := event.Repository.Owner.Login
			eventID := "int64(idGen())"

			events := bson.M{
				"owner":   owner,
				"userID":  "user.UserID",
				"repo":    event.Repository.Name,
				"eventID": eventID,
				"webhook": event,
			}

			// &models.Repos{
			// 	RepoID:      0,
			// 	GitURL:      event.Repository.GitURL,
			// 	Name:        event.Repository.Name,
			// 	Owner:       event.Repository.Owner.Login,
			// 	UserID:      0,
			// 	Private:     event.Repository.Private,
			// 	AccessToken: "",
			// 	WebhookURL:  "",
			// 	EventID:     0,
			// 	CreatedAt:   time.Now().String(),
			// }

			fmt.Println(events)
		}
		c.Status(200)
	}
}

func (gc *GithubController) SetAccessToken() gin.HandlerFunc {
	return func(c *gin.Context) {

	}
}

func (gc *GithubController) SetWebhook() gin.HandlerFunc {
	return func(c *gin.Context) {
		repoName := c.Param("repoName")
		user, _ := gc.authService.ReadUserData(c)

		name := "web"
		active := true

		client := gc.getGithubClient(c)

		_, _, err := client.Repositories.CreateHook(context.Background(), user.Login, repoName, &github.Hook{
			Name:   &name,
			Events: []string{"push"},
			Active: &active,
			Config: map[string]interface{}{
				"url":          gc.config.GitHub.WebHookURL,
				"content-type": "json",
				"insecure-ssl": "0",
				"secret":       gc.config.GitHub.WebHookSecret,
			},
		})
		if err != nil {
			fmt.Println(err)
			c.AbortWithStatus(400)
			return
		}

		gc.collection.FindOneAndUpdate(gc.ctx, bson.M{"name": repoName},
			bson.M{"$set": bson.M{
				"webhookURL": gc.config.GitHub.WebHookURL,
			}})

		c.JSON(200, "your repository webhook was set")
	}
}

func (gc *GithubController) ListRepos() gin.HandlerFunc {
	return func(c *gin.Context) {
		data := c.Copy()
		//fmt.Println(data.Request.Header)
		user, errp := gc.authService.ReadUserData(data)

		if errp != nil {
			c.AbortWithError(500, errp)
			return
		}

		fmt.Println(user.Name)

		fmt.Printf("context: %v", c.Request.Header)

		result, err := gc.githubService.ListRepos(user)

		if err != nil {
			c.AbortWithError(500, err)
		}
		c.JSON(200, result)
	}
}

func (gc *GithubController) getGithubClient(ctx *gin.Context) *github.Client {
	user, _ := gc.authService.ReadUserData(ctx)

	tok := user.GithubToken
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: tok},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)
	return client
}
