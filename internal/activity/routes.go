package activity

import (
	"whatify/backend/internal/features"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("", middleware.Auth(), middleware.RequireFeature(features.Activity))
	{
		g.GET("/activity", listActivity)
	}
}
