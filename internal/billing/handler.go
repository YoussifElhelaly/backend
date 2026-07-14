package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
		Plan  string `json:"plan" binding:"required"`
		Cycle string `json:"cycle"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	plan := models.Plan(req.Plan)
	limits := GetLimits(plan)
	if limits.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan"})
		return
	}

	result, err := Checkout(tenantID, plan, req.Cycle)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleActivate is called by the frontend after PayTabs redirects the user
// back. We don't trust the redirect query string — ActivateSubscription
// re-queries PayTabs directly to confirm the payment succeeded.
func handleActivate(c *gin.Context) {
	var req struct {
		TranRef string `json:"tran_ref" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := ActivateSubscription(req.TranRef); err != nil {
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

// handleWebhook receives PayTabs IPN transaction notifications.
// Unauthenticated — PayTabs POSTs directly here. Verifies the HMAC-SHA256
// "Signature" header before processing.
func handleWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	if err := verifyPaytabsSignature(c.Request.Header, body); err != nil {
		slog.Error("billing: webhook signature verification failed", "error", err)
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid webhook signature"})
		return
	}

	var tx PTTransaction
	if err := json.Unmarshal(body, &tx); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	if tx.TranRef == "" {
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	if err := HandleWebhook(&tx); err != nil {
		slog.Error("billing: webhook handling failed", "tran_ref", tx.TranRef, "error", err)
		c.JSON(http.StatusOK, gin.H{"status": "error", "detail": err.Error()})
		return
	}

	slog.Info("billing: webhook processed", "tran_ref", tx.TranRef, "status", tx.PaymentResult.ResponseStatus)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ─── PayTabs Webhook Signature Verification ────────────────────────────────
//
// PayTabs signs the raw IPN body with HMAC-SHA256 using the profile's server
// key and sends it in the "Signature" header — no external cert fetch needed
// (unlike PayPal's RSA + cert-chain scheme).
func verifyPaytabsSignature(header http.Header, body []byte) error {
	sig := header.Get("Signature")
	if sig == "" {
		return fmt.Errorf("missing Signature header")
	}

	serverKey := paytabsServerKey()
	if serverKey == "" {
		return fmt.Errorf("PAYTABS_SERVER_KEY not configured")
	}

	mac := hmac.New(sha256.New, []byte(serverKey))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
