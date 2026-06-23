package flows

import (
	"net/http"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func listFlows(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var flows []models.Flow
	database.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&flows)
	c.JSON(http.StatusOK, flows)
}

func getFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var flow models.Flow
	if err := database.DB.Where("id = ? AND tenant_id = ?", c.Param("id"), tenantID).First(&flow).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		return
	}
	c.JSON(http.StatusOK, flow)
}

type FlowInput struct {
	Name         string `json:"name" binding:"required"`
	Trigger      string `json:"trigger" binding:"required"`
	Keyword      string `json:"keyword"`
	SessionPhone string `json:"session_phone"`
	Nodes        string `json:"nodes"`
	IsActive     *bool  `json:"is_active"`
}

func createFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	var input FlowInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	nodes := input.Nodes
	if nodes == "" {
		nodes = "[]"
	}
	active := true
	if input.IsActive != nil {
		active = *input.IsActive
	}

	flow := models.Flow{
		TenantID:     tenantID,
		Name:         input.Name,
		Trigger:      models.FlowTrigger(input.Trigger),
		Keyword:      input.Keyword,
		SessionPhone: input.SessionPhone,
		Nodes:        nodes,
		IsActive:     active,
	}
	if err := database.DB.Create(&flow).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create flow"})
		return
	}
	activity.Log(tenantID, &userID, "flow.created", "flow", flow.ID.String(), map[string]string{
		"name":    flow.Name,
		"trigger": string(flow.Trigger),
	})
	c.JSON(http.StatusCreated, flow)
}

func updateFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var flow models.Flow
	if err := database.DB.Where("id = ? AND tenant_id = ?", c.Param("id"), tenantID).First(&flow).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		return
	}

	var input FlowInput
	c.ShouldBindJSON(&input)

	if input.Name != "" {
		flow.Name = input.Name
	}
	if input.Trigger != "" {
		flow.Trigger = models.FlowTrigger(input.Trigger)
	}
	flow.Keyword = input.Keyword
	flow.SessionPhone = input.SessionPhone
	if input.Nodes != "" {
		flow.Nodes = input.Nodes
	}
	if input.IsActive != nil {
		flow.IsActive = *input.IsActive
	}
	database.DB.Save(&flow)
	c.JSON(http.StatusOK, flow)
}

func deleteFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	var flow models.Flow
	if err := database.DB.Where("id = ? AND tenant_id = ?", c.Param("id"), tenantID).First(&flow).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		return
	}
	database.DB.Delete(&flow)
	activity.Log(tenantID, &userID, "flow.deleted", "flow", flow.ID.String(), map[string]string{
		"name": flow.Name,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}

func toggleFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	var flow models.Flow
	if err := database.DB.Where("id = ? AND tenant_id = ?", c.Param("id"), tenantID).First(&flow).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		return
	}
	newState := !flow.IsActive
	database.DB.Model(&flow).Update("is_active", newState)
	action := "flow.enabled"
	if !newState {
		action = "flow.disabled"
	}
	activity.Log(tenantID, &userID, action, "flow", flow.ID.String(), map[string]string{
		"name": flow.Name,
	})
	c.JSON(http.StatusOK, gin.H{"is_active": newState})
}
