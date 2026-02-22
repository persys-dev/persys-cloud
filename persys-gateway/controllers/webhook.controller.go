package controllers

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
)

type WebhookController struct {
	service services.WebhookService
}

func NewWebhookController(service services.WebhookService) *WebhookController {
	return &WebhookController{service: service}
}

func (wc *WebhookController) GitHubHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		payload, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read payload"})
			return
		}
		status, msg := wc.service.HandleGitHubWebhook(c.Request.Context(), c.Request.Header, payload)
		if status != http.StatusOK {
			c.JSON(status, gin.H{"error": msg})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "accepted"})
	}
}
