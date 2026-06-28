package settings

import (
	"net/http"
	"whatify/backend/internal/features"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func getFeatures(c *gin.Context) {
	tenantIDVal, _ := c.Get("tenant_id")
	tenantID, ok := tenantIDVal.(uuid.UUID)
	if !ok {
		if s, ok2 := tenantIDVal.(string); ok2 {
			tenantID, _ = uuid.Parse(s)
		}
	}

	var tenant models.Tenant
	if err := database.DB.Select("plan").First(&tenant, "id = ?", tenantID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	planFeatures := features.GetPlanFeatures(tenant.Plan)

	// Build a map for easy lookup on frontend
	featureMap := make(map[string]bool, len(features.AllFeatures))
	for _, f := range features.AllFeatures {
		featureMap[f] = false
	}
	for _, f := range planFeatures {
		featureMap[f] = true
	}

	c.JSON(http.StatusOK, gin.H{
		"plan":     string(tenant.Plan),
		"features": featureMap,
		"labels":   features.FeatureLabels,
	})
}
