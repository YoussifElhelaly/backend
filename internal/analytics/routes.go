package analytics

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/analytics", middleware.Auth())
	g.GET("/overview", getOverview)
}
