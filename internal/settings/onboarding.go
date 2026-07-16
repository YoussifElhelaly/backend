package settings

import (
	"net/http"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type UpdateOnboardingRequest struct {
	HistorySyncDays int `json:"history_sync_days" binding:"required,min=1,max=3650"`
}

func updateOnboardingSettings(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var req UpdateOnboardingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := database.DB.Model(&models.Tenant{}).Where("id = ?", tenantID).
		Updates(map[string]interface{}{
			"history_sync_days":    req.HistorySyncDays,
			"onboarding_completed": true,
		}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save onboarding settings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
