package settings

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type WebhookInput struct {
	URL      string   `json:"url" binding:"required,url"`
	Events   []string `json:"events" binding:"required,min=1"`
	IsActive *bool    `json:"is_active"`
}

type WebhookResponse struct {
	ID        uuid.UUID `json:"id"`
	URL       string    `json:"url"`
	Events    string    `json:"events"`
	Secret    string    `json:"secret"`
	IsActive  bool      `json:"is_active"`
	CreatedAt string    `json:"created_at"`
}

type WebhookListItem struct {
	ID        uuid.UUID `json:"id"`
	URL       string    `json:"url"`
	Events    string    `json:"events"`
	Secret    string    `json:"secret"`
	IsActive  bool      `json:"is_active"`
	CreatedAt string    `json:"created_at"`
}

func toWebhookResponse(h models.Webhook) WebhookResponse {
	return WebhookResponse{
		ID:        h.ID,
		URL:       h.URL,
		Events:    h.Events,
		Secret:    h.Secret,
		IsActive:  h.IsActive,
		CreatedAt: h.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func toWebhookListItem(h models.Webhook) WebhookListItem {
	secret := h.Secret
	if len(secret) > 14 {
		secret = secret[:14] + "••••"
	}
	return WebhookListItem{
		ID:        h.ID,
		URL:       h.URL,
		Events:    h.Events,
		Secret:    secret,
		IsActive:  h.IsActive,
		CreatedAt: h.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func GenerateSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return "whsec_" + hex.EncodeToString(b)
}

func eventsToJSON(events []string) string {
	out := `[`
	for i, e := range events {
		if i > 0 {
			out += ","
		}
		out += `"` + e + `"`
	}
	return out + `]`
}

func listWebhooks(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var hooks []models.Webhook
	database.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&hooks)

	out := make([]WebhookListItem, len(hooks))
	for i, h := range hooks {
		out[i] = toWebhookListItem(h)
	}
	c.JSON(http.StatusOK, out)
}

func createWebhook(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var input WebhookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hook := models.Webhook{
		TenantID: tenantID,
		URL:      input.URL,
		Events:   eventsToJSON(input.Events),
		Secret:   GenerateSecret(),
		IsActive: true,
	}
	if input.IsActive != nil {
		hook.IsActive = *input.IsActive
	}

	if err := database.DB.Create(&hook).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create webhook"})
		return
	}
	c.JSON(http.StatusCreated, toWebhookResponse(hook))
}

func updateWebhook(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	hookID := c.Param("id")

	var hook models.Webhook
	if err := database.DB.Where("id = ? AND tenant_id = ?", hookID, tenantID).First(&hook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Webhook not found"})
		return
	}

	var input WebhookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hook.URL = input.URL
	hook.Events = eventsToJSON(input.Events)
	if input.IsActive != nil {
		hook.IsActive = *input.IsActive
	}

	database.DB.Save(&hook)
	c.JSON(http.StatusOK, toWebhookListItem(hook))
}

func deleteWebhook(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	hookID := c.Param("id")

	var hook models.Webhook
	if err := database.DB.Where("id = ? AND tenant_id = ?", hookID, tenantID).First(&hook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Webhook not found"})
		return
	}

	database.DB.Delete(&hook)
	c.JSON(http.StatusOK, gin.H{"message": "Webhook deleted"})
}
