package billing

import (
	"net/http"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func handleGetBilling(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	info, err := GetBillingInfo(tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, info)
}

func handleCheckout(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var req struct {
		Plan string `json:"plan" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	plan := models.Plan(req.Plan)
	if _, ok := PlanDefs[plan]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan"})
		return
	}

	result, err := Checkout(tenantID, plan)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleActivate is called by the frontend after PayPal redirects the user back.
// PayPal appends ?subscription_id=I-XXX&ba_token=BA-XXX to the return URL.
func handleActivate(c *gin.Context) {
	var req struct {
		SubscriptionID string `json:"subscription_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := ActivateSubscription(req.SubscriptionID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "active"})
}

// handleCancel lets a user cancel their active subscription.
func handleCancel(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Reason == "" {
		req.Reason = "User requested cancellation"
	}

	if err := CancelTenantSubscription(tenantID, req.Reason); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

// handleWebhook receives PayPal subscription lifecycle events.
// Unauthenticated — PayPal POSTs directly here.
// We verify by re-fetching the subscription from PayPal instead of signature checks.
func handleWebhook(c *gin.Context) {
	var payload struct {
		EventType string `json:"event_type"`
		Resource  struct {
			// For subscription events: id = subscription ID
			ID string `json:"id"`
			// For PAYMENT.SALE.COMPLETED: billing_agreement_id = subscription ID
			BillingAgreementID string `json:"billing_agreement_id"`
		} `json:"resource"`
	}

	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// Resolve the subscription ID regardless of event shape.
	subID := payload.Resource.ID
	if payload.EventType == "PAYMENT.SALE.COMPLETED" && payload.Resource.BillingAgreementID != "" {
		subID = payload.Resource.BillingAgreementID
	}

	if subID == "" {
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	if err := HandleWebhook(payload.EventType, subID); err != nil {
		// Log but return 200 so PayPal doesn't keep retrying.
		c.JSON(http.StatusOK, gin.H{"status": "error", "detail": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
