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

	// Team (requires team feature) — admin-only, agents don't manage teammates
	team := g.Group("/team", middleware.RequireFeature(features.Team), middleware.RequireAdmin())
	team.GET("", listTeam)
	team.POST("", addMember)
	team.PUT("/:id", updateMemberRole)
	team.DELETE("/:id", removeMember)

	// API Keys (requires api_keys feature) — admin-only
	apikeys := g.Group("/api-keys", middleware.RequireFeature(features.APIKeys), middleware.RequireAdmin())
	apikeys.GET("", listAPIKeys)
	apikeys.POST("", createAPIKey)
	apikeys.DELETE("/:id", deleteAPIKey)

	// Webhooks (requires webhooks feature) — admin-only
	webhooks := g.Group("/webhooks", middleware.RequireFeature(features.Webhooks), middleware.RequireAdmin())
	webhooks.GET("", listWebhooks)
	webhooks.POST("", createWebhook)
	webhooks.PUT("/:id", updateWebhook)
	webhooks.DELETE("/:id", deleteWebhook)

	// Campaign rate-limit settings — admin-only
	campaignSettings := g.Group("/settings/campaign", middleware.RequireAdmin())
	campaignSettings.GET("", getCampaignSettings)
	campaignSettings.PUT("", updateCampaignSettings)
	campaignSettings.GET("/usage", getCampaignUsage)
}
