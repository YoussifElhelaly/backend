package settings

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("", middleware.Auth())

	// Team
	g.GET("/team", listTeam)
	g.POST("/team", addMember)
	g.PUT("/team/:id", updateMemberRole)
	g.DELETE("/team/:id", removeMember)

	// API Keys
	g.GET("/api-keys", listAPIKeys)
	g.POST("/api-keys", createAPIKey)
	g.DELETE("/api-keys/:id", deleteAPIKey)

	// Webhooks
	g.GET("/webhooks", listWebhooks)
	g.POST("/webhooks", createWebhook)
	g.PUT("/webhooks/:id", updateWebhook)
	g.DELETE("/webhooks/:id", deleteWebhook)
}
