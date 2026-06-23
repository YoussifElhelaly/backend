package products

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/products", middleware.Auth())
	g.GET("", list)
	g.POST("", create)
	g.PUT("/:id", update)
	g.DELETE("/:id", remove)
}
