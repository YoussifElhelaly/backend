package settings

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("", middleware.Auth())

	// Plan features
	g.GET("/settings/features", getFeatures)

	// Team (requires team feature)
	team := g.Group("/team", middleware.RequireFeature(features.Team))
	team.GET("", listTeam)
	team.POST("", addMember)
	team.PUT("/:id", updateMemberRole)
	team.DELETE("/:id", removeMember)

	// API Keys (requires api_keys feature)
	apikeys := g.Group("/api-keys", middleware.RequireFeature(features.APIKeys))
	apikeys.GET("", listAPIKeys)
	apikeys.POST("", createAPIKey)
	apikeys.DELETE("/:id", deleteAPIKey)

	// Webhooks (requires webhooks feature)
	webhooks := g.Group("/webhooks", middleware.RequireFeature(features.Webhooks))
	webhooks.GET("", listWebhooks)
	webhooks.POST("", createWebhook)
	webhooks.PUT("/:id", updateWebhook)
	webhooks.DELETE("/:id", deleteWebhook)

	// Campaign rate-limit settings
	g.GET("/settings/campaign", getCampaignSettings)
	g.PUT("/settings/campaign", updateCampaignSettings)
	g.GET("/settings/campaign/usage", getCampaignUsage)
}
