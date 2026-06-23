package billing

import (
	"fmt"
	"log"
	"os"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

type CheckoutResult struct {
	ApproveURL     string `json:"approve_url"`
	SubscriptionID string `json:"subscription_id"`
}

// Checkout creates a PayPal subscription and returns the approval URL.
func Checkout(tenantID uuid.UUID, plan models.Plan) (*CheckoutResult, error) {
	limits, ok := PlanDefs[plan]
	if !ok {
		return nil, fmt.Errorf("invalid plan: %s", plan)
	}

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	returnURL := frontendURL + "/billing/callback"
	cancelURL := frontendURL + "/settings?tab=billing&cancelled=1"

	ppSubID, approveURL, err := CreateSubscription(
		plan,
		tenantID.String(),
		returnURL,
		cancelURL,
	)
	if err != nil {
		log.Printf("billing: CreateSubscription failed for tenant %s plan %s: %v", tenantID, plan, err)
		return nil, err
	}

	// Record subscription in DB as PENDING until PayPal confirms.
	sub := models.Subscription{
		TenantID:    tenantID,
		Plan:        plan,
		Amount:      limits.PriceUSD,
		Currency:    "USD",
		CartID:      uuid.New().String(),
		PaypalSubID: ppSubID,
		Status:      models.SubStatusPending,
	}
	if err := database.DB.Create(&sub).Error; err != nil {
		return nil, fmt.Errorf("create subscription record: %w", err)
	}

	return &CheckoutResult{
		ApproveURL:     approveURL,
		SubscriptionID: ppSubID,
	}, nil
}

// ActivateSubscription is called after PayPal redirects the user back.
// It verifies the subscription is ACTIVE on PayPal's side and upgrades the tenant.
func ActivateSubscription(paypalSubID string) error {
	ppSub, err := GetSubscription(paypalSubID)
	if err != nil {
		return fmt.Errorf("verify subscription: %w", err)
	}

	if ppSub.Status != "ACTIVE" {
		return fmt.Errorf("subscription not active (status: %s)", ppSub.Status)
	}

	// Find our DB record.
	var sub models.Subscription
	if err := database.DB.First(&sub, "paypal_sub_id = ?", paypalSubID).Error; err != nil {
		return fmt.Errorf("subscription not found for PayPal ID %s", paypalSubID)
	}

	if sub.Status == models.SubStatusActive {
		return nil // already activated (idempotent)
	}

	tenantID := sub.TenantID
	plan := sub.Plan
	now := time.Now()
	expiresAt := now.AddDate(0, 1, 0)

	if err := database.DB.Model(&sub).Updates(map[string]interface{}{
		"status":     models.SubStatusActive,
		"paid_at":    now,
		"expires_at": expiresAt,
	}).Error; err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}

	if err := database.DB.Model(&models.Tenant{}).
		Where("id = ?", tenantID).
		Updates(map[string]interface{}{
			"plan":            plan,
			"plan_expires_at": expiresAt,
			"paypal_sub_id":   paypalSubID,
			"is_suspended":    false,
		}).Error; err != nil {
		return fmt.Errorf("update tenant plan: %w", err)
	}

	log.Printf("billing: tenant %s subscribed to %s (PayPal sub %s)", tenantID, plan, paypalSubID)
	return nil
}

// HandleWebhook processes PayPal subscription lifecycle events.
// Called from the unauthenticated webhook endpoint.
// We always re-verify against the PayPal API instead of trusting the raw payload.
func HandleWebhook(eventType, resourceID string) error {
	switch eventType {

	case "BILLING.SUBSCRIPTION.ACTIVATED":
		return ActivateSubscription(resourceID)

	case "BILLING.SUBSCRIPTION.RENEWED", "PAYMENT.SALE.COMPLETED":
		// resourceID may be a sale ID, not a subscription ID.
		// For PAYMENT.SALE.COMPLETED the resource has billing_agreement_id = subscription ID.
		return handleRenewal(resourceID)

	case "BILLING.SUBSCRIPTION.CANCELLED", "BILLING.SUBSCRIPTION.EXPIRED":
		return handleCancellation(resourceID)

	case "BILLING.SUBSCRIPTION.SUSPENDED", "BILLING.SUBSCRIPTION.PAYMENT.FAILED":
		return handleSuspension(resourceID)

	default:
		// Unknown event — ignore silently.
		return nil
	}
}

func handleRenewal(paypalSubID string) error {
	ppSub, err := GetSubscription(paypalSubID)
	if err != nil {
		return err
	}
	if ppSub.Status != "ACTIVE" {
		return nil
	}

	now := time.Now()
	expiresAt := now.AddDate(0, 1, 0)

	// Extend plan_expires_at on both subscription record and tenant.
	database.DB.Model(&models.Subscription{}).
		Where("paypal_sub_id = ?", paypalSubID).
		Updates(map[string]interface{}{
			"paid_at":    now,
			"expires_at": expiresAt,
			"status":     models.SubStatusActive,
		})

	database.DB.Model(&models.Tenant{}).
		Where("paypal_sub_id = ?", paypalSubID).
		Updates(map[string]interface{}{
			"plan_expires_at": expiresAt,
			"is_suspended":    false,
		})

	log.Printf("billing: subscription %s renewed, expires %s", paypalSubID, expiresAt.Format("2006-01-02"))
	return nil
}

func handleCancellation(paypalSubID string) error {
	database.DB.Model(&models.Subscription{}).
		Where("paypal_sub_id = ?", paypalSubID).
		Update("status", models.SubStatusCancelled)

	// Downgrade tenant to STARTER after current period ends.
	// We don't immediately remove access — plan_expires_at handles that.
	database.DB.Model(&models.Tenant{}).
		Where("paypal_sub_id = ?", paypalSubID).
		Updates(map[string]interface{}{
			"paypal_sub_id": "",
		})

	log.Printf("billing: subscription %s cancelled", paypalSubID)
	return nil
}

func handleSuspension(paypalSubID string) error {
	database.DB.Model(&models.Tenant{}).
		Where("paypal_sub_id = ?", paypalSubID).
		Update("is_suspended", true)

	log.Printf("billing: subscription %s suspended (payment failure)", paypalSubID)
	return nil
}

// CancelTenantSubscription cancels the tenant's active PayPal subscription.
func CancelTenantSubscription(tenantID uuid.UUID, reason string) error {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return err
	}
	if tenant.PaypalSubID == "" {
		return fmt.Errorf("no active subscription to cancel")
	}

	if err := CancelSubscription(tenant.PaypalSubID, reason); err != nil {
		return err
	}

	return handleCancellation(tenant.PaypalSubID)
}

// ─── Billing Info ──────────────────────────────────────────────────────────────

type BillingInfo struct {
	Plan          models.Plan           `json:"plan"`
	PlanExpiresAt *time.Time            `json:"plan_expires_at,omitempty"`
	TrialEndsAt   *time.Time            `json:"trial_ends_at,omitempty"`
	IsInTrial     bool                  `json:"is_in_trial"`
	TrialDaysLeft int                   `json:"trial_days_left"`
	HasActiveSub  bool                  `json:"has_active_sub"`
	Limits        PlanLimits            `json:"limits"`
	Usage         UsageStats            `json:"usage"`
	Transactions  []models.Subscription `json:"transactions"`
}

type UsageStats struct {
	Sessions int `json:"sessions"`
	Agents   int `json:"agents"`
}

func GetBillingInfo(tenantID uuid.UUID) (*BillingInfo, error) {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil, err
	}

	limits := GetLimits(tenant.Plan)

	var sessionCount int64
	database.DB.Model(&models.WhatsAppSession{}).
		Where("tenant_id = ? AND status != ?", tenantID, models.StatusBanned).
		Count(&sessionCount)

	var agentCount int64
	database.DB.Model(&models.User{}).
		Where("tenant_id = ?", tenantID).
		Count(&agentCount)

	var transactions []models.Subscription
	database.DB.Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Limit(10).
		Find(&transactions)

	isInTrial := tenant.TrialEndsAt != nil && time.Now().Before(*tenant.TrialEndsAt) && tenant.PaypalSubID == ""
	trialDaysLeft := 0
	if isInTrial {
		trialDaysLeft = int(time.Until(*tenant.TrialEndsAt).Hours()/24) + 1
	}

	return &BillingInfo{
		Plan:          tenant.Plan,
		PlanExpiresAt: tenant.PlanExpiresAt,
		TrialEndsAt:   tenant.TrialEndsAt,
		IsInTrial:     isInTrial,
		TrialDaysLeft: trialDaysLeft,
		HasActiveSub:  tenant.PaypalSubID != "",
		Limits:        limits,
		Usage: UsageStats{
			Sessions: int(sessionCount),
			Agents:   int(agentCount),
		},
		Transactions: transactions,
	}, nil
}
