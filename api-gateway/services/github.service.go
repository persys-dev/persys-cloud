package services

import (
	"github.com/google/go-github/github"
)

type GithubService interface {
	GetRepos(client *github.Client) error
	SetWebhook()
	SetAccessToken()
	ListRepos()
}
