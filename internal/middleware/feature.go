package middleware

import (
	"net/http"
	"whatify/backend/internal/features"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequireFeature returns middleware that blocks the request if the tenant's
// plan does not include the specified feature.
func RequireFeature(feature string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantIDStr, exists := c.Get("tenant_id")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "missing tenant context"})
			return
		}

		var tenantID uuid.UUID
		switch v := tenantIDStr.(type) {
		case uuid.UUID:
			tenantID = v
		case string:
			parsed, err := uuid.Parse(v)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid tenant context"})
				return
			}
			tenantID = parsed
		default:
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid tenant context"})
			return
		}

		var tenant models.Tenant
		if err := database.DB.Select("plan").First(&tenant, "id = ?", tenantID).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tenant not found"})
			return
		}

		if !features.HasFeature(tenant, feature) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "feature not available in your plan",
				"feature": feature,
				"code":    "feature_not_in_plan",
			})
			return
		}

		c.Next()
	}
}
