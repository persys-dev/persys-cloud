package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/secrets"
)

type SecretHandler struct {
	Manager *secrets.Manager
}

func NewSecretHandler(manager *secrets.Manager) *SecretHandler {
	return &SecretHandler{Manager: manager}
}

// POST /secrets
func (h *SecretHandler) SetSecret(c *gin.Context) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := h.Manager.Set("global", req.Key, req.Value); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "ok"})
}

// GET /secrets/:key
func (h *SecretHandler) GetSecret(c *gin.Context) {
	key := c.Param("key")
	val, err := h.Manager.Get("global", key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "value": val})
}

// POST /secrets/:project
func (h *SecretHandler) SetProjectSecret(c *gin.Context) {
	project := c.Param("project")
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := h.Manager.Set(project, req.Key, req.Value); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// GET /secrets/:project/:key
func (h *SecretHandler) GetProjectSecret(c *gin.Context) {
	project := c.Param("project")
	key := c.Param("key")
	val, err := h.Manager.Get(project, key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "value": val})
}

// GET /secrets/:project
func (h *SecretHandler) ListProjectSecrets(c *gin.Context) {
	project := c.Param("project")
	keys, err := h.Manager.List(project)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}
