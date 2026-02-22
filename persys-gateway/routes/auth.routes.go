package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/controllers"
)

var (
	ctx    *gin.Context
	scopes = []string{
		"repo",
		"write:repo_hook",
		"user",
		// You have to select your own scope from here -> https://developer.github.com/v3/oauth/#scopes
	}
)

type AuthRouteController struct {
	authController controllers.AuthController
	redirectURI    string
}

func NewAuthRouteController(authController controllers.AuthController, redirectURI string) AuthRouteController {
	return AuthRouteController{authController: authController, redirectURI: redirectURI}
}

func (rc *AuthRouteController) AuthRoute(rg *gin.RouterGroup) {
	router := rg.Group("/auth")

	rc.authController.Setup(rc.redirectURI, scopes)

	router.GET("/login", rc.authController.LoginHandler())
	router.POST("/cli", rc.authController.Cli())

	private := router.Group("")

	private.Use(rc.authController.Auth())

	private.GET("/", func(context *gin.Context) {

	})

}
