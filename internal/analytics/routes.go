package analytics

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/analytics", middleware.Auth(), middleware.RequireFeature(features.Analytics))
	g.GET("/overview", getOverview)
}
