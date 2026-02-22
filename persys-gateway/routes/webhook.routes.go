package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/controllers"
)

type WebhookRouteController struct {
	webhookController *controllers.WebhookController
}

func NewWebhookRouteController(webhookController *controllers.WebhookController) WebhookRouteController {
	return WebhookRouteController{webhookController: webhookController}
}

func (rc *WebhookRouteController) WebhookRoute(rg *gin.RouterGroup, publicPath string) {
	router := rg.Group("")
	router.POST(publicPath, rc.webhookController.GitHubHandler())
}
