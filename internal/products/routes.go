package products

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/products", middleware.Auth(), middleware.RequireFeature(features.Products))
	g.GET("", list)
	g.POST("", create)
	g.PUT("/:id", update)
	g.DELETE("/:id", remove)
	// Auth via ?token= so <img> tags can load it directly
	g.GET("/:id/image", getImage)
}
