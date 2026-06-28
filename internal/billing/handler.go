package billing

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

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
		// Check for custom plan in database
		var planDef models.PlanDef
		if err := database.DB.Where("name = ? AND is_active = true", req.Plan).First(&planDef).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan"})
			return
		}
		// Use custom plan name
		_ = planDef
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
// Verifies the webhook signature using PayPal's certificate before processing.
func handleWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Verify PayPal webhook signature to prevent fake activations.
	if err := verifyPayPalWebhookSignature(c.Request.Header, body); err != nil {
		slog.Error("billing: webhook signature verification failed", "error", err)
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid webhook signature"})
		return
	}

	var payload struct {
		EventType string `json:"event_type"`
		Resource  struct {
			ID                  string `json:"id"`
			BillingAgreementID  string `json:"billing_agreement_id"`
		} `json:"resource"`
	}

	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	subID := payload.Resource.ID
	if payload.EventType == "PAYMENT.SALE.COMPLETED" && payload.Resource.BillingAgreementID != "" {
		subID = payload.Resource.BillingAgreementID
	}

	if subID == "" {
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	if err := HandleWebhook(payload.EventType, subID); err != nil {
		slog.Error("billing: webhook handling failed", "event_type", payload.EventType, "sub_id", subID, "error", err)
		c.JSON(http.StatusOK, gin.H{"status": "error", "detail": err.Error()})
		return
	}

	slog.Info("billing: webhook processed", "event_type", payload.EventType, "sub_id", subID)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ─── PayPal Webhook Signature Verification ─────────────────────────────────

// paypalCertCache caches PayPal TLS certificates to avoid fetching on every webhook.
var (
	paypalCertCache   = map[string]*x509.Certificate{}
	paypalCertCacheMu sync.Mutex
)

const paypalCertCacheTTL = 24 * time.Hour

type certCacheEntry struct {
	cert      *x509.Certificate
	fetchedAt time.Time
}

var paypalCerts = struct {
	mu    sync.Mutex
	cache map[string]certCacheEntry
}{
	cache: make(map[string]certCacheEntry),
}

func verifyPayPalWebhookSignature(header http.Header, body []byte) error {
	algo := header.Get("PayPal-Auth-Algo")
 transmissionID := header.Get("PayPal-Transmission-Id")
	sigB64 := header.Get("PayPal-Transmission-Sig")
	certURL := header.Get("PayPal-Cert-Url")
	timestamp := header.Get("PayPal-Transmission-Time")

	if algo == "" || transmissionID == "" || sigB64 == "" || certURL == "" || timestamp == "" {
		return fmt.Errorf("missing required PayPal headers")
	}

	// Only support SHA256 with RSA
	if !strings.Contains(strings.ToLower(algo), "sha256") {
		return fmt.Errorf("unsupported auth algorithm: %s", algo)
	}

	// Fetch and cache the certificate
	cert, err := fetchPayPalCert(certURL)
	if err != nil {
		return fmt.Errorf("failed to fetch PayPal cert: %w", err)
	}

	// Build the verification message: transmission_id | timestamp | body | cert_url
	msg := fmt.Sprintf("%s|%s|%s|%s", transmissionID, timestamp, string(body), certURL)
	hash := sha256.Sum256([]byte(msg))

	// Decode signature
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Verify RSA signature
	if err := rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, hash[:], sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

func fetchPayPalCert(url string) (*x509.Certificate, error) {
	paypalCertCacheMu.Lock()
	entry, ok := paypalCerts.cache[url]
	paypalCertCacheMu.Unlock()

	if ok && time.Since(entry.fetchedAt) < paypalCertCacheTTL {
		return entry.cert, nil
	}

	resp, err := payPalClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cert: %w", err)
	}
	defer resp.Body.Close()

	certPEM, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read cert body: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Verify it's a PayPal cert (CN should contain paypal)
	if !strings.Contains(strings.ToLower(cert.Subject.CommonName), "paypal") {
		return nil, fmt.Errorf("certificate does not belong to PayPal: %s", cert.Subject.CommonName)
	}

	paypalCertCacheMu.Lock()
	paypalCerts.cache[url] = certCacheEntry{cert: cert, fetchedAt: time.Now()}
	paypalCertCacheMu.Unlock()

	return cert, nil
}
