package ai

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("", middleware.Auth(), middleware.RequireFeature(features.AI))

	g.GET("/ai-config", getConfig)
	g.PUT("/ai-config", saveConfig)
	g.DELETE("/ai-config", deleteConfig)
	g.POST("/ai-config/test", testConfig)
	g.POST("/ai/generate-variants", generateVariants)
}
