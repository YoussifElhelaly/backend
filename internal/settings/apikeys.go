package settings

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CreateAPIKeyInput struct {
	Name string `json:"name" binding:"required"`
}

type APIKeyResponse struct {
	models.APIKey
	FullKey string `json:"key,omitempty"`
}

func generateAPIKey() (full, prefix, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	raw := hex.EncodeToString(b)
	full = "wfy_" + raw
	prefix = full[:12]
	sum := sha256.Sum256([]byte(full))
	hash = hex.EncodeToString(sum[:])
	return
}

func listAPIKeys(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var keys []models.APIKey
	database.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&keys)
	c.JSON(http.StatusOK, keys)
}

func createAPIKey(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var input CreateAPIKeyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	full, prefix, hash, err := generateAPIKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate key"})
		return
	}

	key := models.APIKey{
		TenantID: tenantID,
		Name:     input.Name,
		KeyHash:  hash,
		Prefix:   prefix,
	}

	if err := database.DB.Create(&key).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create API key"})
		return
	}

	c.JSON(http.StatusCreated, APIKeyResponse{APIKey: key, FullKey: full})
}

func deleteAPIKey(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	keyID := c.Param("id")

	var key models.APIKey
	if err := database.DB.Where("id = ? AND tenant_id = ?", keyID, tenantID).First(&key).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
		return
	}

	database.DB.Delete(&key)
	c.JSON(http.StatusOK, gin.H{"message": "API key deleted"})
}

// ValidateAPIKey is used by middleware to authenticate API key requests.
func ValidateAPIKey(rawKey string) (*models.APIKey, error) {
	sum := sha256.Sum256([]byte(rawKey))
	hash := hex.EncodeToString(sum[:])

	var key models.APIKey
	if err := database.DB.Where("key_hash = ?", hash).First(&key).Error; err != nil {
		return nil, fmt.Errorf("invalid api key")
	}

	database.DB.Model(&key).Update("last_used_at", "NOW()")
	return &key, nil
}
