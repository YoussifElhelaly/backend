package quick_replies

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/quick-replies", middleware.Auth(), middleware.RequireFeature(features.QuickReplies))
	g.GET("", list)
	g.POST("", create)
	g.PUT("/:id", update)
	g.DELETE("/:id", remove)
}
