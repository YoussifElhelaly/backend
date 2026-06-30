package settings

import (
	"net/http"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CampaignSettingsResponse struct {
	CampaignDelayMin  int `json:"campaign_delay_min"`
	CampaignDelayMax  int `json:"campaign_delay_max"`
	DailyMessageLimit int `json:"daily_message_limit"`
}

type UpdateCampaignSettingsRequest struct {
	CampaignDelayMin *int `json:"campaign_delay_min"`
	CampaignDelayMax *int `json:"campaign_delay_max"`
}

func getCampaignSettings(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var tenant models.Tenant
	if err := database.DB.Select("campaign_delay_min, campaign_delay_max, daily_message_limit").
		Where("id = ?", tenantID).First(&tenant).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, CampaignSettingsResponse{
		CampaignDelayMin:  tenant.CampaignDelayMin,
		CampaignDelayMax:  tenant.CampaignDelayMax,
		DailyMessageLimit: tenant.DailyMessageLimit,
	})
}

func updateCampaignSettings(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var req UpdateCampaignSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{}

	if req.CampaignDelayMin != nil {
		v := *req.CampaignDelayMin
		if v < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "delay_min must be at least 1 second"})
			return
		}
		updates["campaign_delay_min"] = v
	}
	if req.CampaignDelayMax != nil {
		v := *req.CampaignDelayMax
		if v > 60 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "delay_max cannot exceed 60 seconds"})
			return
		}
		updates["campaign_delay_max"] = v
	}

	// Cross-field validation after both may have been set
	if len(updates) > 0 {
		var current models.Tenant
		database.DB.Select("campaign_delay_min, campaign_delay_max").
			Where("id = ?", tenantID).First(&current)

		dMin := current.CampaignDelayMin
		dMax := current.CampaignDelayMax
		if v, ok := updates["campaign_delay_min"]; ok {
			dMin = v.(int)
		}
		if v, ok := updates["campaign_delay_max"]; ok {
			dMax = v.(int)
		}
		if dMax < dMin {
			c.JSON(http.StatusBadRequest, gin.H{"error": "delay_max must be >= delay_min"})
			return
		}
	}

	if err := database.DB.Model(&models.Tenant{}).
		Where("id = ?", tenantID).
		Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var tenant models.Tenant
	database.DB.Select("campaign_delay_min, campaign_delay_max, daily_message_limit").
		Where("id = ?", tenantID).First(&tenant)

	c.JSON(http.StatusOK, CampaignSettingsResponse{
		CampaignDelayMin:  tenant.CampaignDelayMin,
		CampaignDelayMax:  tenant.CampaignDelayMax,
		DailyMessageLimit: tenant.DailyMessageLimit,
	})
}

type DailyUsageResponse struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}

func getCampaignUsage(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	sessionPhone := c.Query("session_phone")

	if sessionPhone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_phone is required"})
		return
	}

	used, limit := billing.GetDailyUsage(tenantID, sessionPhone)
	c.JSON(http.StatusOK, DailyUsageResponse{Used: used, Limit: limit})
}
