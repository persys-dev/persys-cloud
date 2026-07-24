package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/controllers"
)

type GithubRouteController struct {
	authController   controllers.AuthController
	githubController controllers.GithubController
}

// NewGithubRouteController now actually takes the AuthController it
// mounts behind. Previously it didn't — authController was left at its
// zero value, and Auth() only appeared to work because it silently
// depended on package-level globals set by the *other* auth controller's
// Setup() call. That accidental dependency is gone now that AuthController
// no longer uses package-level globals at all (see auth.controller.go).
func NewGithubRouteController(authController controllers.AuthController, githubController controllers.GithubController) GithubRouteController {
	return GithubRouteController{authController: authController, githubController: githubController}
}

func (rc *GithubRouteController) GithubRoute(rg *gin.RouterGroup) {
	router := rg.Group("")

	private := router.Group("github")

	private.Use(rc.authController.Auth())

	private.GET("/list/repos", rc.githubController.ListRepos())
	private.GET("/set/webhook/:repoName", rc.githubController.SetWebhook())
	private.GET("/set/accessToken/:accessToken", rc.githubController.SetAccessToken())
}
