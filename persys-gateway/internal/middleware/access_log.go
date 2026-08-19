package middleware

import (

	"github.com/gin-gonic/gin"
)

// AccessLogSkipPaths are high-churn probes that should not fill access logs.
// Used with gin.LoggerWithConfig.
var AccessLogSkipPaths = []string{
	"/metrics",
	"/health",
	"/healthz",
	"/ready",
	"/readyz",
	"/livez",
	"/favicon.ico",
}

// AccessLogger is gin.Logger that skips metrics/health scrape noise.
func AccessLogger() gin.HandlerFunc {
	return gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: AccessLogSkipPaths,
		// Also skip paths that only differ by trailing slash or cluster prefix noise.
		
	})
}
