package services

import (
	"github.com/google/go-github/github"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/models"
	"go.mongodb.org/mongo-driver/bson"
)

type GithubService interface {
	GetRepos(client *github.Client, user *models.UserInput) error
	SetWebhook()
	SetAccessToken()
	ListRepos(user *models.DBResponse) (*bson.M, error)
}
