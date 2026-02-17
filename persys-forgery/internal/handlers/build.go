package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/build"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/db"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
)

type BuildHandler struct {
	Orchestrator *build.Orchestrator
}

func NewBuildHandler(orchestrator *build.Orchestrator) *BuildHandler {
	return &BuildHandler{Orchestrator: orchestrator}
}

// POST /build
func (h *BuildHandler) Build(c *gin.Context) {
	var req models.BuildRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	// Required field validation
	if req.ProjectName == "" || req.Type == "" || req.Source == "" || req.Strategy == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing required fields"})
		return
	}
	ctx := context.Background()
	job := models.Job{
		ProjectID:  0, // TODO: look up project by name
		Type:       req.Type,
		Status:     "pending",
		CommitHash: req.CommitHash,
	}
	db.DB.Create(&job)
	go func(jobID uint) {
		err := h.Orchestrator.BuildWithStrategy(ctx, req, req.Strategy)
		status := "success"
		if err != nil {
			status = "failed"
		}
		db.DB.Model(&models.Job{}).Where("id = ?", jobID).Update("status", status)
	}(job.ID)
	c.JSON(http.StatusAccepted, gin.H{"job_id": job.ID, "status": "pending"})
}

// GET /status/:job_id
func (h *BuildHandler) Status(c *gin.Context) {
	idStr := c.Param("job_id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}
	var job models.Job
	if err := db.DB.First(&job, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}
