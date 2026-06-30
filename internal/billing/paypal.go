package billing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"
)

// payPalClient is an HTTP client with a 30-second timeout for all PayPal API calls.
var payPalClient = &http.Client{Timeout: 30 * time.Second}

// ─── Config ───────────────────────────────────────────────────────────────────

func paypalBase() string {
	if os.Getenv("PAYPAL_SANDBOX") == "true" {
		return "https://api-m.sandbox.paypal.com"
	}
	return "https://api-m.paypal.com"
}

func paypalClientID() string     { return os.Getenv("PAYPAL_CLIENT_ID") }
func paypalClientSecret() string { return os.Getenv("PAYPAL_CLIENT_SECRET") }

// planIDCache holds plan IDs after they've been resolved (env → DB → PayPal API).
var planIDCache = map[models.Plan]string{}

// planDBKey returns the DB key for a plan's PayPal plan ID.
func planDBKey(plan models.Plan) string {
	return "paypal_plan_id_" + strings.ToLower(string(plan))
}

// planPriceKey returns the DB key for the price used when creating a PayPal plan.
func planPriceKey(plan models.Plan) string {
	return "paypal_plan_price_" + strings.ToLower(string(plan))
}

// savePlanPrice stores the price that was used to create a PayPal plan.
func savePlanPrice(plan models.Plan, price float64) {
	database.DB.Save(&models.PlatformSetting{
		Key:       planPriceKey(plan),
		Value:     fmt.Sprintf("%.2f", price),
		UpdatedAt: time.Now(),
	})
}

// getPayPalPlanID returns the PayPal plan ID for a given subscription plan.
// Order of resolution: in-memory cache → env var → DB → create via PayPal API.
func getPayPalPlanID(plan models.Plan) (string, error) {
	// 1. In-memory cache (fastest)
	if id, ok := planIDCache[plan]; ok && id != "" {
		return id, nil
	}

	// 2. DB
	var setting models.PlatformSetting
	if err := database.DB.First(&setting, "key = ?", planDBKey(plan)).Error; err == nil && setting.Value != "" {
		// Verify the plan is still ACTIVE on PayPal's side (guards against stale DB entries).
		if verifyPlanActive(setting.Value) {
			planIDCache[plan] = setting.Value
			return setting.Value, nil
		}
		// Plan is not active — delete from DB so SetupPayPalPlans recreates it.
		slog.Warn("billing: plan not ACTIVE on PayPal, will recreate", "plan", plan, "plan_id", setting.Value)
		database.DB.Delete(&setting)
	}

	// 3. Env var (fallback)
	envKey := "PAYPAL_PLAN_" + strings.ToUpper(string(plan))
	if id := os.Getenv(envKey); id != "" {
		planIDCache[plan] = id
		return id, nil
	}

	// 4. Not found anywhere
	return "", fmt.Errorf("PayPal plan ID not configured for %s — set %s in .env or restart the server (it will auto-create plans if PAYPAL_CLIENT_ID is set)", plan, envKey)
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

func getAccessToken() (string, error) {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")

	req, _ := http.NewRequest("POST", paypalBase()+"/v1/oauth2/token", strings.NewReader(data.Encode()))
	req.SetBasicAuth(paypalClientID(), paypalClientSecret())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := payPalClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("paypal auth: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("paypal auth decode: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("paypal auth failed: %s", result.Error)
	}
	return result.AccessToken, nil
}

func ppPost(token, path string, body interface{}, out interface{}) (int, []byte, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", paypalBase()+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := payPalClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode, raw, nil
}

func ppGet(token, path string, out interface{}) (int, []byte, error) {
	req, _ := http.NewRequest("GET", paypalBase()+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := payPalClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode, raw, nil
}

// ─── Plan Setup (one-time) ────────────────────────────────────────────────────

// SetupPayPalPlans creates the PayPal product and billing plans if they don't
// already exist. Call at server startup. IDs are logged so operators can
// persist them as PAYPAL_PLAN_* env vars for subsequent restarts.
// SetupPayPalPlans ensures all three PayPal billing plan IDs exist.
// Resolution order per plan: env var → DB → create via PayPal API → save to DB.
// Safe to call at every startup — idempotent.
func SetupPayPalPlans() {
	if paypalClientID() == "" || paypalClientSecret() == "" {
		slog.Warn("billing: PAYPAL_CLIENT_ID/SECRET not set — PayPal billing disabled")
		return
	}
	slog.Info("billing: PayPal configured", "sandbox", os.Getenv("PAYPAL_SANDBOX"), "client_id", paypalClientID())

	type planDef struct {
		plan          models.Plan
		envKey        string
		name          string
		priceUSD      float64
		intervalCount int
		period        string
	}
	var allPlans []models.PlanDef
	if err := database.DB.Where("is_active = true").Find(&allPlans).Error; err != nil {
		slog.Error("billing: failed to load plans from db", "error", err)
		return
	}

	defs := make([]planDef, 0, len(allPlans))
	for _, p := range allPlans {
		defs = append(defs, planDef{
			plan:          models.Plan(p.Name),
			envKey:        "PAYPAL_PLAN_" + strings.ToUpper(p.Name),
			name:          "Whatify " + p.Label,
			priceUSD:      p.PriceUSD,
			intervalCount: p.IntervalCount,
			period:        p.Period,
		})
	}

	// Check which plans are already resolved, and detect price changes.
	missing := make([]planDef, 0)
	for _, d := range defs {
		// Check if price or interval changed since last PayPal plan creation.
		var priceSetting models.PlatformSetting
		priceEntryExists := database.DB.First(&priceSetting, "key = ?", planPriceKey(d.plan)).Error == nil
		
		var intervalSetting models.PlatformSetting
		intervalEntryExists := database.DB.First(&intervalSetting, "key = ?", "paypal_plan_interval_"+string(d.plan)).Error == nil

		if priceEntryExists {
			var storedPrice float64
			fmt.Sscanf(priceSetting.Value, "%f", &storedPrice)
			
			var storedInterval int
			if intervalEntryExists {
				fmt.Sscanf(intervalSetting.Value, "%d", &storedInterval)
			}
			if storedInterval == 0 {
				storedInterval = 1 // default for old plans
			}

			if (storedPrice > 0 && storedPrice != d.priceUSD) || storedInterval != d.intervalCount {
				slog.Warn("billing: price or interval changed, will recreate PayPal plan",
					"plan", d.plan, "old_price", storedPrice, "new_price", d.priceUSD, "old_interval", storedInterval, "new_interval", d.intervalCount)
				database.DB.Where("key = ?", planDBKey(d.plan)).Delete(&models.PlatformSetting{})
				database.DB.Where("key = ?", planPriceKey(d.plan)).Delete(&models.PlatformSetting{})
				database.DB.Where("key = ?", "paypal_plan_interval_"+string(d.plan)).Delete(&models.PlatformSetting{})
				delete(planIDCache, d.plan)
				missing = append(missing, d)
				continue
			}
		}

		id, err := getPayPalPlanID(d.plan)
		if err != nil || id == "" {
			missing = append(missing, d)
		} else {
			// No price entry yet (first run after deploy) — check PayPal API price.
			if !priceEntryExists {
				ppPrice := getPayPalPlanPrice(id)
				if ppPrice > 0 && ppPrice != d.priceUSD {
					slog.Warn("billing: PayPal plan price mismatch, will recreate",
						"plan", d.plan, "paypal_price", ppPrice, "expected_price", d.priceUSD)
					database.DB.Where("key = ?", planDBKey(d.plan)).Delete(&models.PlatformSetting{})
					delete(planIDCache, d.plan)
					missing = append(missing, d)
					continue
				}
				// Price matches (or can't check) — save settings for future comparisons.
				savePlanPrice(d.plan, d.priceUSD)
				database.DB.Save(&models.PlatformSetting{Key: "paypal_plan_interval_" + string(d.plan), Value: fmt.Sprintf("%d", d.intervalCount), UpdatedAt: time.Now()})
			}
			slog.Info("billing: plan resolved", "plan", d.plan, "plan_id", id)
		}
	}

	if len(missing) == 0 {
		slog.Info("billing: all PayPal plans ready")
		return
	}

	// Need to create missing plans via PayPal API.
	slog.Info("billing: creating missing PayPal plans", "count", len(missing))

	token, err := getAccessToken()
	if err != nil {
		slog.Error("billing: PayPal auth failed — billing disabled", "error", err)
		return
	}

	// Create (or reuse) a product.
	productID := getOrCreateProduct(token)
	if productID == "" {
		slog.Warn("billing: could not get/create PayPal product — billing disabled")
		return
	}

	for _, d := range missing {
		id, err := createPlan(token, productID, d.name, d.priceUSD, d.intervalCount, d.period)
		if err != nil {
			slog.Error("billing: failed to create plan", "plan", d.plan, "error", err)
			continue
		}
		// Save to DB so it survives restarts.
		savePlanID(d.plan, id)
		savePlanPrice(d.plan, d.priceUSD)
		database.DB.Save(&models.PlatformSetting{Key: "paypal_plan_interval_" + string(d.plan), Value: fmt.Sprintf("%d", d.intervalCount), UpdatedAt: time.Now()})
		planIDCache[d.plan] = id
		slog.Info("billing: plan created and saved to DB", "plan", d.plan, "plan_id", id, "price", d.priceUSD)
	}
}

func getOrCreateProduct(token string) string {
	// Try to load an existing product ID from DB first.
	var setting models.PlatformSetting
	if err := database.DB.First(&setting, "key = ?", "paypal_product_id").Error; err == nil && setting.Value != "" {
		return setting.Value
	}

	// Create a new product.
	var product struct {
		ID string `json:"id"`
	}
	status, raw, err := ppPost(token, "/v1/catalogs/products", map[string]string{
		"name":        "Whatify Subscription",
		"description": "Whatify WhatsApp SaaS",
		"type":        "SERVICE",
		"category":    "SOFTWARE",
	}, &product)
	if err != nil || product.ID == "" {
		slog.Error("billing: PayPal product creation failed", "status", status, "response", string(raw), "error", err)
		return ""
	}

	// Persist product ID.
	database.DB.Save(&models.PlatformSetting{Key: "paypal_product_id", Value: product.ID, UpdatedAt: time.Now()})
	slog.Info("billing: PayPal product created", "product_id", product.ID)
	return product.ID
}

func createPlan(token, productID, name string, priceUSD float64, intervalCount int, period string) (string, error) {
	type cycle struct {
		Frequency     map[string]interface{} `json:"frequency"`
		TenureType    string                 `json:"tenure_type"`
		Sequence      int                    `json:"sequence"`
		TotalCycles   int                    `json:"total_cycles"`
		PricingScheme map[string]interface{} `json:"pricing_scheme"`
	}

	intervalUnit := "MONTH"
	if period == "yr" || period == "year" {
		intervalUnit = "YEAR"
	}
	if intervalCount < 1 {
		intervalCount = 1
	}

	var result struct {
		ID string `json:"id"`
	}
	s, r, e := ppPost(token, "/v1/billing/plans", map[string]interface{}{
		"product_id":  productID,
		"name":        name,
		"description": fmt.Sprintf("%s — %d %s", name, intervalCount, intervalUnit),
		"status":      "ACTIVE",
		"billing_cycles": []cycle{{
			Frequency:   map[string]interface{}{"interval_unit": intervalUnit, "interval_count": intervalCount},
			TenureType:  "REGULAR",
			Sequence:    1,
			TotalCycles: 0,
			PricingScheme: map[string]interface{}{
				"fixed_price": map[string]string{
					"value":         fmt.Sprintf("%.2f", priceUSD),
					"currency_code": "USD",
				},
			},
		}},
		"payment_preferences": map[string]interface{}{
			"auto_bill_outstanding":     true,
			"setup_fee_failure_action":  "CONTINUE",
			"payment_failure_threshold": 3,
		},
	}, &result)
	if e != nil || result.ID == "" {
		return "", fmt.Errorf("status %d: %s %v", s, r, e)
	}

	// PayPal sometimes creates plans in CREATED status even when ACTIVE is requested.
	// Explicitly activate to be safe.
	activateStatus, _, _ := ppPost(token, "/v1/billing/plans/"+result.ID+"/activate", map[string]string{}, nil)
	if activateStatus != http.StatusNoContent && activateStatus != http.StatusOK {
		slog.Warn("billing: plan activate returned non-success status (may already be active)", "plan_id", result.ID, "status", activateStatus)
	}

	return result.ID, nil
}

func verifyPlanActive(planID string) bool {
	token, err := getAccessToken()
	if err != nil {
		return true // assume ok if we can't check
	}
	var result struct {
		Status string `json:"status"`
	}
	statusCode, _, _ := ppGet(token, "/v1/billing/plans/"+planID, &result)
	if statusCode != http.StatusOK {
		return false
	}
	return result.Status == "ACTIVE"
}

// getPayPalPlanPrice fetches the price of an existing PayPal plan.
func getPayPalPlanPrice(planID string) float64 {
	token, err := getAccessToken()
	if err != nil {
		return 0
	}
	var result struct {
		BillingCycles []struct {
			PricingScheme struct {
				FixedPrice struct {
					Value string `json:"value"`
				} `json:"fixed_price"`
			} `json:"pricing_scheme"`
		} `json:"billing_cycles"`
	}
	statusCode, _, _ := ppGet(token, "/v1/billing/plans/"+planID, &result)
	if statusCode != http.StatusOK {
		return 0
	}
	if len(result.BillingCycles) > 0 {
		var price float64
		fmt.Sscanf(result.BillingCycles[0].PricingScheme.FixedPrice.Value, "%f", &price)
		return price
	}
	return 0
}

func savePlanID(plan models.Plan, id string) {
	database.DB.Save(&models.PlatformSetting{
		Key:       planDBKey(plan),
		Value:     id,
		UpdatedAt: time.Now(),
	})
}

// ─── Subscriptions ────────────────────────────────────────────────────────────

type PPSubscription struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	PlanID     string `json:"plan_id"`
	CustomID   string `json:"custom_id"` // we store tenantID here
	SubscriberName  string `json:"-"`
	BillingInfo struct {
		NextBillingTime string `json:"next_billing_time"`
		LastPayment     struct {
			Amount struct {
				Value string `json:"value"`
			} `json:"amount"`
			Time string `json:"time"`
		} `json:"last_payment"`
	} `json:"billing_info"`
}

// CreateSubscription creates a PayPal subscription and returns the subscription
// ID and the PayPal approval URL to redirect the user to.
func CreateSubscription(plan models.Plan, tenantID, returnURL, cancelURL string) (string, string, error) {
	planID, err := getPayPalPlanID(plan)
	if err != nil {
		return "", "", err
	}

	token, err := getAccessToken()
	if err != nil {
		return "", "", err
	}

	var result struct {
		ID    string `json:"id"`
		Links []struct {
			Href string `json:"href"`
			Rel  string `json:"rel"`
		} `json:"links"`
		Message string `json:"message"` // error field
	}

	status, raw, err := ppPost(token, "/v1/billing/subscriptions", map[string]interface{}{
		"plan_id":   planID,
		"custom_id": tenantID,
		"application_context": map[string]string{
			"brand_name":          "Whatify",
			"return_url":          returnURL,
			"cancel_url":          cancelURL,
			"user_action":         "SUBSCRIBE_NOW",
			"shipping_preference": "NO_SHIPPING",
		},
	}, &result)
	if err != nil {
		return "", "", fmt.Errorf("paypal create subscription: %w", err)
	}
	if result.ID == "" {
		return "", "", fmt.Errorf("paypal create subscription failed (status %d): %s", status, raw)
	}

	var approveURL string
	for _, l := range result.Links {
		if l.Rel == "approve" {
			approveURL = l.Href
			break
		}
	}
	if approveURL == "" {
		return "", "", fmt.Errorf("paypal: no approve link in response")
	}

	return result.ID, approveURL, nil
}

// GetSubscription fetches a subscription's current status from PayPal.
func GetSubscription(subID string) (*PPSubscription, error) {
	token, err := getAccessToken()
	if err != nil {
		return nil, err
	}

	var sub PPSubscription
	statusCode, raw, err := ppGet(token, "/v1/billing/subscriptions/"+subID, &sub)
	if err != nil {
		return nil, fmt.Errorf("paypal get subscription: %w", err)
	}
	if statusCode != http.StatusOK || sub.ID == "" {
		return nil, fmt.Errorf("paypal get subscription failed (status %d): %s", statusCode, raw)
	}
	return &sub, nil
}

// CancelSubscription cancels a PayPal subscription immediately.
func CancelSubscription(subID, reason string) error {
	token, err := getAccessToken()
	if err != nil {
		return err
	}

	statusCode, raw, err := ppPost(token, "/v1/billing/subscriptions/"+subID+"/cancel",
		map[string]string{"reason": reason}, nil)
	if err != nil {
		return fmt.Errorf("paypal cancel subscription: %w", err)
	}
	// 204 = success
	if statusCode != http.StatusNoContent && statusCode != http.StatusOK {
		return fmt.Errorf("paypal cancel subscription failed (status %d): %s", statusCode, raw)
	}
	return nil
}
