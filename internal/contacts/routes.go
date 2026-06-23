package contacts

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	group := r.Group("/contacts")
	group.Use(middleware.Auth())
	{
		group.GET("", listContacts)
		group.PUT("/:id", updateContact)
		group.POST("/delete", deleteContacts) // Use POST for delete body support
	}

	sessionGroup := r.Group("/sessions")
	sessionGroup.Use(middleware.Auth())
	{
		sessionGroup.POST("/:id/contacts/sync", syncContacts)
	}
}
