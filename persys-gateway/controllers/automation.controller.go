package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
	automationv1 "github.com/persys-dev/persys-cloud/pkg/automation/automationv1"
)

type AutomationController struct {
	automationService *services.AutomationService
}

func NewAutomationController(automationService *services.AutomationService) *AutomationController {
	return &AutomationController{automationService: automationService}
}

func (c *AutomationController) writeProxyError(ctx *gin.Context, err error) {
	ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
}

func (c *AutomationController) CreatePolicyHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := &automationv1.CreatePolicyRequest{}
		if !decodeProtoBody(ctx, req) {
			return
		}
		resp, err := c.automationService.CreatePolicy(ctx.Request.Context(), req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *AutomationController) ListPoliciesHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := &automationv1.ListPoliciesRequest{
			IncludeDisabled: ctx.Query("include_disabled") == "true",
		}
		resp, err := c.automationService.ListPolicies(ctx.Request.Context(), req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *AutomationController) EnablePolicyHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := &automationv1.EnablePolicyRequest{PolicyId: ctx.Param("id")}
		resp, err := c.automationService.EnablePolicy(ctx.Request.Context(), req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *AutomationController) DisablePolicyHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := &automationv1.DisablePolicyRequest{PolicyId: ctx.Param("id")}
		resp, err := c.automationService.DisablePolicy(ctx.Request.Context(), req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *AutomationController) EvaluateNowHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req := &automationv1.EvaluateNowRequest{PolicyId: ctx.Param("id")}
		resp, err := c.automationService.EvaluateNow(ctx.Request.Context(), req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func (c *AutomationController) ListAuditLogHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		limit := uint32(100)
		if v := ctx.Query("limit"); v != "" {
			if parsed, err := parseUint32(v); err == nil {
				limit = parsed
			}
		}
		req := &automationv1.ListAuditLogRequest{Limit: limit}
		resp, err := c.automationService.ListAuditLog(ctx.Request.Context(), req)
		if err != nil {
			c.writeProxyError(ctx, err)
			return
		}
		writeProtoJSON(ctx, http.StatusOK, resp)
	}
}

func parseUint32(s string) (uint32, error) {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(n), nil
}
