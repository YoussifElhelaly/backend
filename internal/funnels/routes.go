package funnels

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/funnels", middleware.Auth(), middleware.RequireFeature(features.Funnels))
	g.GET("", listFunnels)
	g.POST("", createFunnel)
	g.GET("/:id", getFunnel)
	g.PATCH("/:id", updateFunnel)
	g.DELETE("/:id", deleteFunnel)
	g.POST("/:id/activate", activateFunnel)
	g.POST("/:id/pause", pauseFunnel)
	g.POST("/:id/steps", addStep)
	g.PATCH("/:id/steps/:step_id", updateStep)
	g.DELETE("/:id/steps/:step_id", deleteStep)
	g.GET("/:id/pipeline", getPipeline)
	g.GET("/:id/contacts/:contact_id/history", getContactHistory)
	g.POST("/:id/launch", launchFunnel)
	g.POST("/:id/contacts/move", moveContact)
	g.PATCH("/:id/contacts/:contact_id/status", setContactStatus)
}
