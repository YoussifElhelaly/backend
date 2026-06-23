package activity

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("", middleware.Auth())
	{
		g.GET("/activity", listActivity)
	}
}
