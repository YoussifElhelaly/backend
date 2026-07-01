package billing

import (
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.RouterGroup) {
	// PayTabs POSTs here on transaction events (IPN) — no auth, HMAC-verified
	r.POST("/billing/webhook", handleWebhook)

	// GET/checkout/activate bypass tenant-access check so trial-expired or
	// subscription-expired users can still view and purchase a plan.
	bypass := r.Group("/billing", middleware.AuthBillingBypass())
	bypass.GET("", handleGetBilling)
	bypass.POST("/checkout", handleCheckout)
	bypass.POST("/activate", handleActivate)

	// Cancel requires a fully-valid session (user must have an active sub).
	authed := r.Group("/billing", middleware.Auth())
	authed.POST("/cancel", handleCancel)
}
