package routes

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	"github.com/persys-dev/persys-cloud/persys-gateway/utils"
)

type IntelligenceRouteController struct {
	cfg *config.Config
}

func NewIntelligenceRouteController(cfg *config.Config) IntelligenceRouteController {
	return IntelligenceRouteController{cfg: cfg}
}

func (rc *IntelligenceRouteController) IntelligenceRoute(rg *gin.RouterGroup) {
	ai := rg.Group("/ai")
	{
		ai.POST("/query", rc.proxy("/ai/query"))
		ai.POST("/internal/evaluate", rc.proxy("/internal/evaluate"))
		ai.GET("/recommendations", rc.proxy("/recommendations"))
		ai.GET("/recommendations/pending", rc.proxy("/recommendations/pending"))
		ai.POST("/recommendations/:id/approve", rc.proxyDynamic())
		ai.POST("/recommendations/:id/reject", rc.proxyDynamic())
		ai.POST("/recommendations/:id/apply", rc.proxyDynamic())
	}
}

func (rc *IntelligenceRouteController) timeout() time.Duration {
	d, err := time.ParseDuration(rc.cfg.Intelligence.RequestTimeout)
	if err != nil || d <= 0 {
		return 10 * time.Second
	}
	return d
}

// proxy forwards to a fixed upstream path on persys-intelligence.
func (rc *IntelligenceRouteController) proxy(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := rc.cfg.Intelligence.HTTPAddr + path
		utils.ProxyRequest(c, target, &utils.ProxyOptions{
			Timeout: rc.timeout(),
			Headers: map[string]string{"X-Forwarded-For": c.ClientIP()},
		})
	}
}

// proxyDynamic forwards to persys-intelligence by stripping the gateway's
// "/ai" prefix from the inbound path (e.g. /ai/recommendations/:id/approve
// -> upstream /recommendations/:id/approve), since the upstream handler
// parses the id and action out of the URL itself.
func (rc *IntelligenceRouteController) proxyDynamic() gin.HandlerFunc {
	return func(c *gin.Context) {
		upstreamPath := strings.TrimPrefix(c.Request.URL.Path, "/ai")
		target := rc.cfg.Intelligence.HTTPAddr + upstreamPath
		utils.ProxyRequest(c, target, &utils.ProxyOptions{
			Timeout: rc.timeout(),
			Headers: map[string]string{"X-Forwarded-For": c.ClientIP()},
		})
	}
}
