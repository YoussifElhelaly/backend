package tags

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	group := r.Group("/tags")
	group.Use(middleware.Auth())
	{
		group.GET("", listTags)
		group.POST("", createTag)
		group.PUT("/:id", updateTag)
		group.DELETE("/:id", deleteTag)
		group.POST("/:id/contacts", addContactsToTag)
		group.DELETE("/:id/contacts", removeContactsFromTag)
	}
}
