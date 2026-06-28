package admin

import (
	"time"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	// Public super-admin login (separate from regular auth/login) — rate-limited to prevent brute-force.
	r.POST("/admin/login", middleware.RateLimit(10, time.Minute), handleSuperAdminLogin)

	// Protected super-admin routes
	admin := r.Group("/admin", middleware.Auth(), middleware.RequireSuperAdmin())
	{
		admin.GET("/stats", handleStats)
		admin.GET("/charts", handleCharts)
		admin.GET("/health", handleSystemHealth)
		admin.GET("/tenants", handleListTenants)
		admin.GET("/tenants/:id", handleGetTenant)
		admin.PUT("/tenants/:id", handleUpdateTenant)
		admin.POST("/tenants", handleCreateTenant)
		admin.DELETE("/tenants/:id", handleDeleteTenant)
		admin.POST("/tenants/bulk-plan", handleBulkPlanChange)
		admin.GET("/subscriptions", handleListAllSubscriptions)
		admin.PUT("/subscriptions/:id", handleUpdateSubscription)
		admin.POST("/subscriptions/:id/cancel", handleCancelSubscription)
		admin.POST("/subscriptions/:id/extend", handleExtendSubscription)
		admin.GET("/sessions", handleListAdminSessions)
		admin.POST("/sessions/:id/disconnect", handleDisconnectSession)
		admin.GET("/activity", handleAdminActivity)

		// Plan management
		admin.GET("/plans", handleListPlans)
		admin.POST("/plans", handleCreatePlan)
		admin.PUT("/plans/:id", handleUpdatePlan)
		admin.DELETE("/plans/:id", handleDeletePlan)
	}
}
