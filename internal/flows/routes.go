package flows

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/flows", middleware.Auth())
	g.GET("", listFlows)
	g.POST("", createFlow)
	g.GET("/:id", getFlow)
	g.PUT("/:id", updateFlow)
	g.DELETE("/:id", deleteFlow)
	g.POST("/:id/toggle", toggleFlow)
}
