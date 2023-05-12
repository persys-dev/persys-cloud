package services

import "github.com/miladhzzzz/milx-cloud-init/api-gateway/models"

type AuthService interface {
	SignInUser(user *models.UserInput) (*models.DBResponse, error)
	CliLogin(req *models.CliReq) (*models.DBResponse, error)
	ReadUserData() *models.DBResponse
	CheckUser()
}
