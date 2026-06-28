package developer

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes registers all developer API endpoints under /dev.
// All endpoints are authenticated via API key (X-API-Key header).
func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/dev", middleware.APIKeyAuth())
	{
		// Messages
		g.POST("/messages", handleSendMessage)

		// Conversations
		g.GET("/conversations", handleListConversations)
		g.GET("/conversations/:id", handleGetConversation)
		g.GET("/conversations/:id/messages", handleListMessages)

		// Contacts
		g.GET("/contacts", handleListContacts)
		g.GET("/contacts/:id", handleGetContact)

		// Sessions
		g.GET("/sessions", handleListSessions)

		// Webhooks
		g.POST("/webhooks", handleCreateWebhook)
		g.GET("/webhooks", handleListWebhooks)
		g.DELETE("/webhooks/:id", handleDeleteWebhook)
	}
}
