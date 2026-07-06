package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/controllers"
)

type AutomationRouteController struct {
	automationController *controllers.AutomationController
}

func NewAutomationRouteController(automationController *controllers.AutomationController) AutomationRouteController {
	return AutomationRouteController{automationController: automationController}
}

func (rc *AutomationRouteController) AutomationRoute(rg *gin.RouterGroup) {
	automation := rg.Group("/automation")
	{
		policies := automation.Group("/policies")
		{
			policies.POST("", rc.automationController.CreatePolicyHandler())
			policies.GET("", rc.automationController.ListPoliciesHandler())
			policies.POST("/:id/enable", rc.automationController.EnablePolicyHandler())
			policies.POST("/:id/disable", rc.automationController.DisablePolicyHandler())
			policies.POST("/:id/evaluate", rc.automationController.EvaluateNowHandler())
		}
		automation.GET("/audit-log", rc.automationController.ListAuditLogHandler())
	}
}
