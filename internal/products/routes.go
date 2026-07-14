package products

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/products", middleware.Auth(), middleware.RequireFeature(features.Products))
	// Read access is shared with agents (used by the inbox product picker).
	g.GET("", list)
	// Auth via ?token= so <img> tags can load it directly
	g.GET("/:id/image", getImage)

	// Catalog management is admin-only.
	admin := g.Group("", middleware.RequireAdmin())
	admin.POST("", create)
	admin.PUT("/:id", update)
	admin.DELETE("/:id", remove)
}
