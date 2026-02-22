package services

import "github.com/persys-dev/persys-cloud/persys-gateway/models"

type GithubService interface {
	SetAccessToken(user *models.DBResponse) error
	ListRepos(user *models.DBResponse) ([]map[string]interface{}, error)
	SetWebhook(user *models.DBResponse, repository string) error
}
