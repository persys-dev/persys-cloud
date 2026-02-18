package middleware

import "github.com/gin-gonic/gin"

func ServiceIdentityHeader(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("X-Service-Name", serviceName)
		c.Next()
	}
}
