package admin

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	// Public super-admin login (separate from regular auth/login)
	r.POST("/admin/login", handleSuperAdminLogin)

	// Protected super-admin routes
	admin := r.Group("/admin", middleware.Auth(), middleware.RequireSuperAdmin())
	{
		admin.GET("/stats", handleStats)
		admin.GET("/tenants", handleListTenants)
		admin.GET("/tenants/:id", handleGetTenant)
		admin.PUT("/tenants/:id", handleUpdateTenant)
		admin.POST("/tenants", handleCreateTenant)
		admin.DELETE("/tenants/:id", handleDeleteTenant)
		admin.GET("/subscriptions", handleListSubscriptions)
	}
}
