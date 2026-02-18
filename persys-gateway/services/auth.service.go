package services

import (
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/models"
)

type AuthService interface {
	SignInUser(user *models.UserInput) (*models.DBResponse, error)
	CliLogin(req *models.CliReq) (*models.DBResponse, error)
	ReadUserData(ctx *gin.Context) (*models.DBResponse, error)
	IsAuthenticated(ctx *gin.Context) bool
	CheckUser()
}
