package campaigns

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/campaigns", middleware.Auth(), middleware.RequireFeature(features.Campaigns))
	g.GET("", listCampaigns)
	g.POST("", createCampaign)
	g.GET("/:id", getCampaign)
	g.PUT("/:id", updateCampaign)
	g.DELETE("/:id", deleteCampaign)
	g.POST("/:id/launch", launchCampaign)
	g.POST("/:id/pause", pauseCampaign)
	g.GET("/:id/stats", getCampaignStats)
}
