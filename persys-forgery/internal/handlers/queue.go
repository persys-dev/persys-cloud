package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GET /queue
func (h *BuildHandler) Queue(c *gin.Context) {
	// TODO: Implement queue inspection logic
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented"})
}
