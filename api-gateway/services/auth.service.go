package services

import (
	"github.com/gin-gonic/gin"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/models"
)

type AuthService interface {
	SignInUser(user *models.UserInput) (*models.DBResponse, error)
	CliLogin(req *models.CliReq) (*models.DBResponse, error)
	ReadUserData(ctx *gin.Context) (*models.DBResponse, error)
	CheckUser()
}
