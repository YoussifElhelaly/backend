package billing

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	// PayPal POSTs here on subscription events — no auth
	r.POST("/billing/webhook", handleWebhook)

	g := r.Group("/billing", middleware.Auth())
	g.GET("", handleGetBilling)
	g.POST("/checkout", handleCheckout)
	g.POST("/activate", handleActivate)
	g.POST("/cancel", handleCancel)
}
