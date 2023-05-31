package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/api-gateway/controllers"
)

type GithubRouteController struct {
	authController   controllers.AuthController
	githubController controllers.GithubController
}

func NewGithubRouteController(githubController controllers.GithubController) GithubRouteController {
	return GithubRouteController{githubController: githubController}
}

func (rc *GithubRouteController) GithubRoute(rg *gin.RouterGroup) {
	router := rg.Group("")

	router.POST("/webhook", rc.githubController.WebhookHandler())

	private := router.Group("github")

	private.Use(rc.authController.Auth())

	private.GET("/list/repos", rc.githubController.ListRepos())
	private.GET("/set/webhook/:repoName", rc.githubController.SetWebhook())
	private.GET("/set/accessToken/:accessToken", rc.githubController.SetAccessToken())

}
