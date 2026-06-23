package funnels

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/funnels", middleware.Auth())
	g.GET("", listFunnels)
	g.POST("", createFunnel)
	g.GET("/:id", getFunnel)
	g.PATCH("/:id", updateFunnel)
	g.DELETE("/:id", deleteFunnel)
	g.POST("/:id/activate", activateFunnel)
	g.POST("/:id/pause", pauseFunnel)
	g.POST("/:id/steps", addStep)
	g.GET("/:id/pipeline", getPipeline)
	g.POST("/:id/launch", launchFunnel)
	g.POST("/:id/contacts/move", moveContact)
	g.PATCH("/:id/contacts/:contact_id/status", setContactStatus)
}
