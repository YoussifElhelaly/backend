package billing

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

// StartRenewalWorker runs the monthly renewal loop PayPal used to handle for
// us. PayTabs has no native recurring-subscription engine — instead we saved
// a card token at checkout (tokenise=2) and charge it here once the tenant's
// current period ends. Requires "Recurring" mode to be enabled on the PayTabs
// profile (contact PayTabs support to turn it on).
func StartRenewalWorker(ctx context.Context) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("billing: PANIC recovered in renewal worker", "panic", r)
			}
		}()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				processRenewals()
			}
		}
	}()
}

func processRenewals() {
	var tenants []models.Tenant
	if err := database.DB.
		Where("plan_expires_at IS NOT NULL AND plan_expires_at <= ? AND paytabs_token != '' AND is_suspended = false", time.Now()).
		Find(&tenants).Error; err != nil {
		slog.Error("billing: renewal worker query failed", "error", err)
		return
	}

	for _, tenant := range tenants {
		if err := renewTenant(&tenant); err != nil {
			slog.Error("billing: renewal failed", "tenant_id", tenant.ID, "error", err)
			database.DB.Model(&models.Tenant{}).Where("id = ?", tenant.ID).Update("is_suspended", true)
		}
	}
}

func renewTenant(tenant *models.Tenant) error {
	// Anchor the new subscription row to the tenant's last one to get the
	// previous tran_ref PayTabs requires for a token-based recurring charge,
	// and to carry over the billing cycle the tenant last chose.
	var priorSub models.Subscription
	if err := database.DB.Where("tenant_id = ? AND paytabs_tran_ref != ''", tenant.ID).
		Order("created_at DESC").First(&priorSub).Error; err != nil {
		return fmt.Errorf("no prior transaction to anchor renewal: %w", err)
	}

	cycle := normalizeCycle(priorSub.BillingCycle)
	amount, months, planLabel, err := resolveCyclePrice(tenant.Plan, cycle)
	if err != nil {
		// The chosen cycle is no longer offered — fall back to monthly.
		cycle = CycleMonthly
		amount, months, planLabel, _ = resolveCyclePrice(tenant.Plan, cycle)
	}
	if months < 1 {
		months = 1
	}

	cartID := "wf_renew_" + tenant.ID.String() + "_" + uuid.New().String()
	tx, err := ChargeToken(
		tenant,
		fmt.Sprintf("Whatify %s subscription renewal", planLabel),
		amount,
		tenant.PaytabsToken,
		priorSub.PaytabsTranRef,
		cartID,
	)
	if err != nil {
		return err
	}
	if !tx.succeeded() {
		return fmt.Errorf("renewal charge declined (status: %s)", tx.PaymentResult.ResponseStatus)
	}

	now := time.Now()
	expiresAt := now.AddDate(0, months, 0)

	sub := models.Subscription{
		TenantID:       tenant.ID,
		Plan:           tenant.Plan,
		Amount:         amount,
		Currency:       paytabsCurrency(),
		CartID:         cartID,
		PaytabsTranRef: tx.TranRef,
		PaytabsToken:   tenant.PaytabsToken,
		BillingCycle:   cycle,
		Status:         models.SubStatusActive,
		PaidAt:         &now,
		ExpiresAt:      &expiresAt,
	}
	if err := database.DB.Create(&sub).Error; err != nil {
		return fmt.Errorf("record renewal subscription: %w", err)
	}

	database.DB.Model(&models.Tenant{}).Where("id = ?", tenant.ID).
		Update("plan_expires_at", expiresAt)

	slog.Info("billing: renewed subscription", "tenant_id", tenant.ID, "expires_at", expiresAt.Format("2006-01-02"))
	return nil
}
