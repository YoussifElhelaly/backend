package ai

import (
	"net/http"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ── GET /ai-config ───────────────────────────────────────────────────────────

func getConfig(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var cfg models.AIConfig
	if err := database.DB.Where("tenant_id = ?", tenantID).First(&cfg).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"configured": false})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"configured": true,
		"id":         cfg.ID,
		"platform":   cfg.Platform,
		"model":      cfg.Model,
		"key_hint":   cfg.KeyHint,
		"is_active":  cfg.IsActive,
	})
}

// ── PUT /ai-config ───────────────────────────────────────────────────────────

type saveConfigInput struct {
	Platform string `json:"platform" binding:"required"`
	Model    string `json:"model" binding:"required"`
	APIKey   string `json:"api_key" binding:"required"`
}

func saveConfig(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var input saveConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	encrypted, err := Encrypt(input.APIKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption failed"})
		return
	}

	hint := ""
	if len(input.APIKey) >= 4 {
		hint = "..." + input.APIKey[len(input.APIKey)-4:]
	}

	var cfg models.AIConfig
	result := database.DB.Where("tenant_id = ?", tenantID).First(&cfg)
	if result.Error == gorm.ErrRecordNotFound {
		cfg = models.AIConfig{
			TenantID:     tenantID,
			Platform:     input.Platform,
			Model:        input.Model,
			EncryptedKey: encrypted,
			KeyHint:      hint,
			IsActive:     true,
		}
		database.DB.Create(&cfg)
	} else {
		database.DB.Model(&cfg).Updates(map[string]any{
			"platform":      input.Platform,
			"model":         input.Model,
			"encrypted_key": encrypted,
			"key_hint":      hint,
			"is_active":     true,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"platform": cfg.Platform,
		"model":    cfg.Model,
		"key_hint": hint,
	})
}

// ── DELETE /ai-config ────────────────────────────────────────────────────────

func deleteConfig(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	database.DB.Where("tenant_id = ?", tenantID).Delete(&models.AIConfig{})
	c.JSON(http.StatusOK, gin.H{"message": "AI config removed"})
}

// ── POST /ai-config/test ─────────────────────────────────────────────────────

func testConfig(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var cfg models.AIConfig
	if err := database.DB.Where("tenant_id = ?", tenantID).First(&cfg).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No AI config found"})
		return
	}

	apiKey, err := Decrypt(cfg.EncryptedKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to decrypt API key"})
		return
	}

	if err := TestConnection(cfg.Platform, cfg.Model, apiKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Connection successful"})
}

// ── POST /ai/generate-variants ───────────────────────────────────────────────

type generateInput struct {
	Message string `json:"message" binding:"required"`
	Count   int    `json:"count"`
}

func generateVariants(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var input generateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var cfg models.AIConfig
	if err := database.DB.Where("tenant_id = ? AND is_active = true", tenantID).First(&cfg).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No active AI config. Please add an API key in Settings → AI Assistant."})
		return
	}

	apiKey, err := Decrypt(cfg.EncryptedKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to decrypt API key"})
		return
	}

	variants, err := GenerateVariants(cfg.Platform, cfg.Model, apiKey, input.Message)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "AI generation failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"variants": variants})
}

// GetDecryptedKey is used internally by executors.
func GetDecryptedKey(tenantID uuid.UUID) (platform, model, apiKey string, err error) {
	var cfg models.AIConfig
	if err = database.DB.Where("tenant_id = ? AND is_active = true", tenantID).First(&cfg).Error; err != nil {
		return
	}
	apiKey, err = Decrypt(cfg.EncryptedKey)
	platform = cfg.Platform
	model = cfg.Model
	return
}
