package billing

import (
	"fmt"
	"log"
	"os"
	"time"
	"whatify/backend/internal/features"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

// FixTrialDates clears trial_ends_at for any tenant that has or previously had
// a paid subscription. Runs at startup to repair bad existing data.
func FixTrialDates() {
	res := database.DB.Model(&models.Tenant{}).
		Where("trial_ends_at IS NOT NULL AND (paytabs_token != '' OR plan_expires_at IS NOT NULL)").
		Update("trial_ends_at", nil)
	if res.RowsAffected > 0 {
		log.Printf("billing: cleared stale trial_ends_at on %d tenant(s)", res.RowsAffected)
	}
}

// SeedBuiltinPlans ensures the 3 built-in plans exist in the plan_defs table.
func SeedBuiltinPlans() {
	builtin := []models.PlanDef{
		{Name: "STARTER", Label: "Starter", PriceEGP: 599, Sessions: 1, MessagesDay: 500, Agents: 2, Flows: 2, Funnels: 1, QuickReplies: 10, Campaigns: 5, IsCustom: false, IsActive: true,
			Features: features.ToJSON(features.DefaultFeatures[models.PlanStarter])},
		{Name: "GROWTH", Label: "Growth", PriceEGP: 1499, Sessions: 5, MessagesDay: 5000, Agents: 10, Flows: 10, Funnels: 5, QuickReplies: 50, Campaigns: -1, IsCustom: false, IsActive: true,
			Features: features.ToJSON(features.DefaultFeatures[models.PlanGrowth])},
		{Name: "SCALE", Label: "Scale", PriceEGP: 2999, Sessions: 20, MessagesDay: -1, Agents: -1, Flows: -1, Funnels: -1, QuickReplies: -1, Campaigns: -1, IsCustom: false, IsActive: true,
			Features: features.ToJSON(features.DefaultFeatures[models.PlanScale])},
	}
	for _, p := range builtin {
		var existing models.PlanDef
		if err := database.DB.Where("name = ?", p.Name).First(&existing).Error; err != nil {
			database.DB.Create(&p)
		} else {
			// Always sync limit fields and features for built-in plans so that
			// newly added columns (flows, funnels, quick_replies, campaigns) get
			// the correct values even on existing databases.
			database.DB.Model(&existing).Updates(map[string]interface{}{
				"flows":         p.Flows,
				"funnels":       p.Funnels,
				"quick_replies": p.QuickReplies,
				"campaigns":     p.Campaigns,
				"features":      p.Features,
			})
		}
	}
}

type CheckoutResult struct {
	RedirectURL string `json:"redirect_url"`
	TranRef     string `json:"tran_ref"`
}

// Checkout creates a PayTabs hosted-payment-page request and returns the
// redirect URL to send the user to, tokenising the card for future renewals.
func Checkout(tenantID uuid.UUID, plan models.Plan) (*CheckoutResult, error) {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil, fmt.Errorf("tenant not found: %w", err)
	}

	var owner models.User
	database.DB.Where("tenant_id = ? AND role = ?", tenantID, models.RoleAdmin).First(&owner)

	limits := GetLimits(plan)

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	returnURL := frontendURL + "/billing/callback"
	callbackURL := os.Getenv("BACKEND_URL")
	if callbackURL == "" {
		callbackURL = "http://localhost:8080"
	}
	callbackURL += "/api/v1/billing/webhook"

	cartID := "wf_" + tenantID.String() + "_" + uuid.New().String()

	tx, err := CreatePaymentRequest(
		&tenant,
		customerDetails{tenant: &tenant, name: owner.Name, email: owner.Email},
		fmt.Sprintf("Whatify %s subscription", limits.Label),
		limits.PriceEGP,
		cartID,
		returnURL,
		callbackURL,
	)
	if err != nil {
		log.Printf("billing: CreatePaymentRequest failed for tenant %s plan %s: %v", tenantID, plan, err)
		return nil, err
	}

	// Record subscription in DB as PENDING until PayTabs confirms.
	sub := models.Subscription{
		TenantID:       tenantID,
		Plan:           plan,
		Amount:         limits.PriceEGP,
		Currency:       paytabsCurrency(),
		CartID:         cartID,
		PaytabsTranRef: tx.TranRef,
		Status:         models.SubStatusPending,
	}
	if err := database.DB.Create(&sub).Error; err != nil {
		return nil, fmt.Errorf("create subscription record: %w", err)
	}

	return &CheckoutResult{
		RedirectURL: tx.RedirectURL,
		TranRef:     tx.TranRef,
	}, nil
}

// ActivateSubscription is called after PayTabs redirects the user back (or via
// the IPN webhook). It re-queries PayTabs directly rather than trusting the
// redirect query string, then upgrades the tenant and stores the card token.
func ActivateSubscription(tranRef string) error {
	tx, err := QueryTransaction(tranRef)
	if err != nil {
		return fmt.Errorf("verify transaction: %w", err)
	}
	if !tx.succeeded() {
		return fmt.Errorf("payment not successful (status: %s)", tx.PaymentResult.ResponseStatus)
	}

	var sub models.Subscription
	if err := database.DB.First(&sub, "paytabs_tran_ref = ?", tranRef).Error; err != nil {
		return fmt.Errorf("subscription not found for tran_ref %s", tranRef)
	}

	if sub.Status == models.SubStatusActive {
		return nil // already activated (idempotent)
	}

	tenantID := sub.TenantID
	plan := sub.Plan
	now := time.Now()

	expiresAt := now.AddDate(0, 1, 0)
	var planDef models.PlanDef
	if err := database.DB.First(&planDef, "name = ?", string(plan)).Error; err == nil {
		if planDef.Period == "yr" || planDef.Period == "year" {
			expiresAt = now.AddDate(planDef.IntervalCount, 0, 0)
		} else {
			expiresAt = now.AddDate(0, planDef.IntervalCount, 0)
		}
	}

	if err := database.DB.Model(&sub).Updates(map[string]interface{}{
		"status":        models.SubStatusActive,
		"paid_at":       now,
		"expires_at":    expiresAt,
		"paytabs_token": tx.Token,
	}).Error; err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}

	if err := database.DB.Model(&models.Tenant{}).
		Where("id = ?", tenantID).
		Updates(map[string]interface{}{
			"plan":            plan,
			"plan_expires_at": expiresAt,
			"paytabs_token":   tx.Token,
			"is_suspended":    false,
			"trial_ends_at":   nil, // clear trial once user subscribes
		}).Error; err != nil {
		return fmt.Errorf("update tenant plan: %w", err)
	}

	log.Printf("billing: tenant %s subscribed to %s (PayTabs tran_ref %s)", tenantID, plan, tranRef)
	return nil
}

// HandleWebhook processes a verified PayTabs IPN payload. Called from the
// webhook endpoint after the HMAC signature has already been checked.
func HandleWebhook(tx *PTTransaction) error {
	var sub models.Subscription
	err := database.DB.First(&sub, "paytabs_tran_ref = ? OR cart_id = ?", tx.TranRef, tx.CartID).Error

	if !tx.succeeded() {
		if err == nil {
			database.DB.Model(&sub).Update("status", models.SubStatusFailed)
			database.DB.Model(&models.Tenant{}).Where("id = ?", sub.TenantID).Update("is_suspended", true)
			log.Printf("billing: transaction %s failed/declined, tenant %s suspended", tx.TranRef, sub.TenantID)
		}
		return nil
	}

	if err != nil {
		// Renewal charges (fired by the renewal worker) insert their own
		// subscription row directly, so a missing record here just means the
		// initial checkout hasn't been activated yet — handled by /billing/activate.
		return nil
	}

	if sub.Status == models.SubStatusActive && sub.PaidAt != nil {
		return nil // already processed (idempotent)
	}

	return ActivateSubscription(tx.TranRef)
}

// CancelTenantSubscription clears the tenant's stored card token so the
// renewal worker stops auto-charging. Access remains until plan_expires_at —
// there's no remote subscription object to cancel with PayTabs.
func CancelTenantSubscription(tenantID uuid.UUID, reason string) error {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return err
	}
	if tenant.PaytabsToken == "" {
		return fmt.Errorf("no active subscription to cancel")
	}

	database.DB.Model(&models.Tenant{}).Where("id = ?", tenantID).Update("paytabs_token", "")
	database.DB.Model(&models.Subscription{}).
		Where("tenant_id = ? AND status = ?", tenantID, models.SubStatusActive).
		Update("status", models.SubStatusCancelled)

	log.Printf("billing: tenant %s cancelled subscription (%s)", tenantID, reason)
	return nil
}

// ─── Billing Info ──────────────────────────────────────────────────────────────

type BillingInfo struct {
	Plan          models.Plan `json:"plan"`
	PlanExpiresAt *time.Time  `json:"plan_expires_at,omitempty"`
	TrialEndsAt   *time.Time  `json:"trial_ends_at,omitempty"`
	IsInTrial     bool        `json:"is_in_trial"`
	TrialDaysLeft int         `json:"trial_days_left"`
	HasActiveSub  bool        `json:"has_active_sub"`
	// SubCancelled is true when the user has cancelled (token cleared) but
	// plan_expires_at is still in the future (they still have access).
	SubCancelled bool                  `json:"sub_cancelled"`
	CancelsAt    *time.Time            `json:"cancels_at,omitempty"`
	IsSuspended  bool                  `json:"is_suspended"`
	Limits       PlanLimits            `json:"limits"`
	Usage        UsageStats            `json:"usage"`
	Transactions []models.Subscription `json:"transactions"`
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

	now := time.Now()

	// In trial only when: trial still running + never paid (plan_expires_at nil) + no active sub
	isInTrial := tenant.TrialEndsAt != nil &&
		now.Before(*tenant.TrialEndsAt) &&
		tenant.PaytabsToken == "" &&
		tenant.PlanExpiresAt == nil
	trialDaysLeft := 0
	if isInTrial {
		trialDaysLeft = int(time.Until(*tenant.TrialEndsAt).Hours()/24) + 1
	}

	hasActiveSub := tenant.PaytabsToken != ""

	// SubCancelled: no stored token but plan_expires_at is still in the future.
	// This means they cancelled but still have access until the period ends.
	subCancelled := !hasActiveSub && tenant.PlanExpiresAt != nil && now.Before(*tenant.PlanExpiresAt) && tenant.Plan != models.PlanStarter
	var cancelsAt *time.Time
	if subCancelled {
		cancelsAt = tenant.PlanExpiresAt
	}

	return &BillingInfo{
		Plan:          tenant.Plan,
		PlanExpiresAt: tenant.PlanExpiresAt,
		TrialEndsAt:   tenant.TrialEndsAt,
		IsInTrial:     isInTrial,
		TrialDaysLeft: trialDaysLeft,
		HasActiveSub:  hasActiveSub,
		SubCancelled:  subCancelled,
		CancelsAt:     cancelsAt,
		IsSuspended:   tenant.IsSuspended,
		Limits:        limits,
		Usage: UsageStats{
			Sessions: int(sessionCount),
			Agents:   int(agentCount),
		},
		Transactions: transactions,
	}, nil
}
