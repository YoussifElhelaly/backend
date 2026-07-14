package quick_replies

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/quick-replies", middleware.Auth(), middleware.RequireFeature(features.QuickReplies))
	// Read access is shared with agents (used by the inbox "/" composer shortcut).
	g.GET("", list)

	// Template management is admin-only.
	admin := g.Group("", middleware.RequireAdmin())
	admin.POST("", create)
	admin.PUT("/:id", update)
	admin.DELETE("/:id", remove)
}
