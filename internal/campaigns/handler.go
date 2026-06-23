package campaigns

import (
	"net/http"
	"time"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── List ─────────────────────────────────────────────────────────────────────

func listCampaigns(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	sessionPhone := c.Query("session_phone")

	var campaigns []models.Campaign
	q := database.DB.Where("tenant_id = ?", tenantID)
	if sessionPhone != "" {
		q = q.Where("session_phone = ?", sessionPhone)
	}
	q.Order("created_at DESC").Find(&campaigns)
	c.JSON(http.StatusOK, campaigns)
}

// ── Get ──────────────────────────────────────────────────────────────────────

func getCampaign(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var campaign models.Campaign
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&campaign).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Campaign not found"})
		return
	}

	var contacts []models.CampaignContact
	database.DB.Preload("Contact").Where("campaign_id = ?", campaign.ID).Find(&contacts)

	c.JSON(http.StatusOK, gin.H{"campaign": campaign, "contacts": contacts})
}

// ── Create ───────────────────────────────────────────────────────────────────

type CreateCampaignInput struct {
	Name         string       `json:"name" binding:"required"`
	SessionPhone string       `json:"session_phone" binding:"required"`
	Message      string       `json:"message" binding:"required"`
	ContactIDs   []uuid.UUID  `json:"contact_ids"`
	TagIDs       []uuid.UUID  `json:"tag_ids"`
	ScheduledAt  *time.Time   `json:"scheduled_at"`
	FunnelID     *uuid.UUID   `json:"funnel_id"`
}

func createCampaign(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)

	var input CreateCampaignInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Resolve contacts from contact_ids + tag_ids
	contactMap := map[uuid.UUID]bool{}
	if len(input.ContactIDs) > 0 {
		for _, id := range input.ContactIDs {
			contactMap[id] = true
		}
	}
	if len(input.TagIDs) > 0 {
		var tagContacts []models.Contact
		database.DB.
			Joins("JOIN contact_tags ON contacts.id = contact_tags.contact_id").
			Where("contact_tags.tag_id IN ? AND contacts.tenant_id = ?", input.TagIDs, tenantID).
			Find(&tagContacts)
		for _, ct := range tagContacts {
			contactMap[ct.ID] = true
		}
	}

	campaign := models.Campaign{
		TenantID:     tenantID,
		SessionPhone: input.SessionPhone,
		Name:         input.Name,
		Message:      input.Message,
		ScheduledAt:  input.ScheduledAt,
		FunnelID:     input.FunnelID,
		Status:       models.CampaignStatusDraft,
	}
	if err := database.DB.Create(&campaign).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create campaign"})
		return
	}

	// Bulk-insert campaign_contacts
	var ccs []models.CampaignContact
	for cid := range contactMap {
		ccs = append(ccs, models.CampaignContact{
			CampaignID: campaign.ID,
			ContactID:  cid,
			Status:     models.CampaignContactPending,
		})
	}
	if len(ccs) > 0 {
		database.DB.Create(&ccs)
	}
	database.DB.Model(&campaign).Update("total_contacts", len(ccs))
	campaign.TotalContacts = len(ccs)

	activity.Log(tenantID, &userID, "campaign.created", "campaign", campaign.ID.String(), map[string]string{
		"name": campaign.Name,
	})
	c.JSON(http.StatusCreated, campaign)
}

// ── Update ───────────────────────────────────────────────────────────────────

type UpdateCampaignInput struct {
	Name        string     `json:"name"`
	Message     string     `json:"message"`
	ScheduledAt *time.Time `json:"scheduled_at"`
}

func updateCampaign(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var campaign models.Campaign
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&campaign).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Campaign not found"})
		return
	}
	if campaign.Status != models.CampaignStatusDraft && campaign.Status != models.CampaignStatusScheduled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Can only edit DRAFT or SCHEDULED campaigns"})
		return
	}

	var input UpdateCampaignInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.Name != "" {
		campaign.Name = input.Name
	}
	if input.Message != "" {
		campaign.Message = input.Message
	}
	campaign.ScheduledAt = input.ScheduledAt
	database.DB.Save(&campaign)
	c.JSON(http.StatusOK, campaign)
}

// ── Delete ───────────────────────────────────────────────────────────────────

func deleteCampaign(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	id := c.Param("id")

	var campaign models.Campaign
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&campaign).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Campaign not found"})
		return
	}
	if campaign.Status == models.CampaignStatusRunning {
		Pause(campaign.ID)
	}
	database.DB.Where("campaign_id = ?", campaign.ID).Delete(&models.CampaignContact{})
	database.DB.Delete(&campaign)
	activity.Log(tenantID, &userID, "campaign.deleted", "campaign", id, map[string]string{
		"name": campaign.Name,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}

// ── Launch ───────────────────────────────────────────────────────────────────

func launchCampaign(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var campaign models.Campaign
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&campaign).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Campaign not found"})
		return
	}
	if campaign.Status == models.CampaignStatusRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Campaign already running"})
		return
	}
	if campaign.Status == models.CampaignStatusCompleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Campaign already completed"})
		return
	}
	if campaign.TotalContacts == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No contacts in campaign"})
		return
	}

	Launch(campaign.ID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	activity.Log(tenantID, &userID, "campaign.launched", "campaign", campaign.ID.String(), map[string]string{
		"name": campaign.Name,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Campaign launched"})
}

// ── Pause ────────────────────────────────────────────────────────────────────

func pauseCampaign(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var campaign models.Campaign
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&campaign).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Campaign not found"})
		return
	}
	if campaign.Status != models.CampaignStatusRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Campaign is not running"})
		return
	}
	Pause(campaign.ID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	activity.Log(tenantID, &userID, "campaign.paused", "campaign", campaign.ID.String(), map[string]string{
		"name": campaign.Name,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Campaign paused"})
}

// ── Stats ────────────────────────────────────────────────────────────────────

func getCampaignStats(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var campaign models.Campaign
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&campaign).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Campaign not found"})
		return
	}

	var pending, sent, failed int64
	database.DB.Model(&models.CampaignContact{}).Where("campaign_id = ? AND status = 'PENDING'", campaign.ID).Count(&pending)
	database.DB.Model(&models.CampaignContact{}).Where("campaign_id = ? AND status = 'SENT'", campaign.ID).Count(&sent)
	database.DB.Model(&models.CampaignContact{}).Where("campaign_id = ? AND status = 'FAILED'", campaign.ID).Count(&failed)

	c.JSON(http.StatusOK, gin.H{
		"total":   campaign.TotalContacts,
		"sent":    sent,
		"failed":  failed,
		"pending": pending,
	})
}

// ── Scheduled launcher (called by cron) ──────────────────────────────────────

func LaunchScheduled() {
	var campaigns []models.Campaign
	now := time.Now()
	database.DB.Where("status = ? AND scheduled_at IS NOT NULL AND scheduled_at <= ?",
		models.CampaignStatusDraft, now).Find(&campaigns)
	for _, camp := range campaigns {
		Launch(camp.ID)
	}
}
