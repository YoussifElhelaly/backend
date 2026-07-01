package billing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
	"whatify/backend/internal/models"
)

// payTabsClient is an HTTP client with a 30-second timeout for all PayTabs API calls.
var payTabsClient = &http.Client{Timeout: 30 * time.Second}

// ─── Config ───────────────────────────────────────────────────────────────────

// paytabsBase returns the regional PayTabs Hosted Payment Page base URL.
// Defaults to the Egypt profile since plan pricing is EGP.
func paytabsBase() string {
	switch os.Getenv("PAYTABS_REGION") {
	case "global":
		return "https://secure.paytabs.com"
	case "uae":
		return "https://secure-uae.paytabs.com"
	case "ksa", "saudi":
		return "https://secure.paytabs.sa"
	case "jordan":
		return "https://secure-jordan.paytabs.com"
	case "oman":
		return "https://secure-oman.paytabs.com"
	default:
		return "https://secure-egypt.paytabs.com"
	}
}

func paytabsProfileID() string { return os.Getenv("PAYTABS_PROFILE_ID") }
func paytabsServerKey() string { return os.Getenv("PAYTABS_SERVER_KEY") }

func paytabsCurrency() string {
	if c := os.Getenv("PAYTABS_CURRENCY"); c != "" {
		return c
	}
	return "EGP"
}

func paytabsConfigured() bool {
	return paytabsProfileID() != "" && paytabsServerKey() != ""
}

// ptPost posts a JSON body to a PayTabs endpoint, authenticated with the raw
// server key (PayTabs does not use a "Bearer" prefix).
func ptPost(path string, body map[string]interface{}) (int, []byte, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", paytabsBase()+path, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", paytabsServerKey())
	req.Header.Set("Content-Type", "application/json")

	resp, err := payTabsClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, nil
}

// ─── Payment result shapes ────────────────────────────────────────────────────

type ptPaymentResult struct {
	ResponseStatus  string `json:"response_status"` // "A" = authorized/success, "D" = declined, "E" = error
	ResponseCode    string `json:"response_code"`
	ResponseMessage string `json:"response_message"`
}

// PTTransaction is the shape PayTabs returns from /payment/request (initial),
// the IPN callback, and /payment/query — fields are populated as available.
type PTTransaction struct {
	TranRef       string          `json:"tran_ref"`
	CartID        string          `json:"cart_id"`
	RedirectURL   string          `json:"redirect_url"`
	Token         string          `json:"token"`
	PaymentResult ptPaymentResult `json:"payment_result"`
}

func (t *PTTransaction) succeeded() bool {
	return t.PaymentResult.ResponseStatus == "A"
}

// ─── Customer details ─────────────────────────────────────────────────────────

type customerDetails struct {
	tenant *models.Tenant
	name   string
	email  string
	ip     string
}

func (c customerDetails) toMap() map[string]interface{} {
	name := c.name
	if name == "" {
		name = c.tenant.Name
	}
	ip := c.ip
	if ip == "" {
		ip = "127.0.0.1"
	}
	return map[string]interface{}{
		"name":    name,
		"email":   c.email,
		"phone":   "00000000000",
		"street1": c.tenant.Name,
		"city":    "Cairo",
		"state":   "Cairo",
		"country": "EG",
		"zip":     "00000",
		"ip":      ip,
	}
}

// ─── Hosted Payment Page checkout ─────────────────────────────────────────────

// CreatePaymentRequest builds a PayTabs Hosted Payment Page request that also
// tokenises the card for future recurring charges. Returns the redirect URL
// the frontend sends the user to, plus the tran_ref/cart_id we track.
func CreatePaymentRequest(tenant *models.Tenant, cust customerDetails, description string, amount float64, cartID, returnURL, callbackURL string) (*PTTransaction, error) {
	if !paytabsConfigured() {
		return nil, fmt.Errorf("PayTabs is not configured — set PAYTABS_PROFILE_ID and PAYTABS_SERVER_KEY")
	}

	body := map[string]interface{}{
		"profile_id":       paytabsProfileID(),
		"tran_type":        "sale",
		"tran_class":       "ecom",
		"cart_id":          cartID,
		"cart_description": description,
		"cart_currency":    paytabsCurrency(),
		"cart_amount":      amount,
		"tokenise":         "2",
		"hide_shipping":    true,
		"return":           returnURL,
		"callback":         callbackURL,
		"customer_details": cust.toMap(),
	}

	status, raw, err := ptPost("/payment/request", body)
	if err != nil {
		return nil, fmt.Errorf("paytabs create payment request: %w", err)
	}

	var tx PTTransaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		return nil, fmt.Errorf("paytabs decode response (status %d): %w", status, err)
	}
	if tx.RedirectURL == "" || tx.TranRef == "" {
		return nil, fmt.Errorf("paytabs create payment request failed (status %d): %s", status, raw)
	}

	return &tx, nil
}

// ─── Token-based recurring charge ─────────────────────────────────────────────

// ChargeToken charges a previously-saved card token for a renewal. Only usable
// once "Recurring" mode has been enabled on the PayTabs profile.
func ChargeToken(tenant *models.Tenant, description string, amount float64, token, priorTranRef, cartID string) (*PTTransaction, error) {
	if !paytabsConfigured() {
		return nil, fmt.Errorf("PayTabs is not configured — set PAYTABS_PROFILE_ID and PAYTABS_SERVER_KEY")
	}

	body := map[string]interface{}{
		"profile_id":       paytabsProfileID(),
		"tran_type":        "sale",
		"tran_class":       "recurring",
		"cart_id":          cartID,
		"cart_description": description,
		"cart_currency":    paytabsCurrency(),
		"cart_amount":      amount,
		"token":            token,
		"tran_ref":         priorTranRef,
	}

	status, raw, err := ptPost("/payment/request", body)
	if err != nil {
		return nil, fmt.Errorf("paytabs charge token: %w", err)
	}

	var tx PTTransaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		return nil, fmt.Errorf("paytabs decode charge response (status %d): %w", status, err)
	}
	if tx.TranRef == "" {
		return nil, fmt.Errorf("paytabs charge token failed (status %d): %s", status, raw)
	}
	return &tx, nil
}

// ─── Query / Refund ────────────────────────────────────────────────────────────

// QueryTransaction fetches the authoritative status of a transaction from
// PayTabs. Used to confirm success server-side instead of trusting redirect
// query params or an unverified webhook body.
func QueryTransaction(tranRef string) (*PTTransaction, error) {
	if !paytabsConfigured() {
		return nil, fmt.Errorf("PayTabs is not configured")
	}

	status, raw, err := ptPost("/payment/query", map[string]interface{}{
		"profile_id": paytabsProfileID(),
		"tran_ref":   tranRef,
	})
	if err != nil {
		return nil, fmt.Errorf("paytabs query transaction: %w", err)
	}

	var tx PTTransaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		return nil, fmt.Errorf("paytabs decode query response (status %d): %w", status, err)
	}
	if tx.TranRef == "" {
		return nil, fmt.Errorf("paytabs query transaction failed (status %d): %s", status, raw)
	}
	return &tx, nil
}

// RefundTransaction issues a refund against a prior transaction.
func RefundTransaction(tranRef string, amount float64, description string) error {
	if !paytabsConfigured() {
		return fmt.Errorf("PayTabs is not configured")
	}

	status, raw, err := ptPost("/payment/request", map[string]interface{}{
		"profile_id":       paytabsProfileID(),
		"tran_type":        "refund",
		"tran_class":       "ecom",
		"tran_ref":         tranRef,
		"cart_id":          tranRef + "-refund",
		"cart_description": description,
		"cart_currency":    paytabsCurrency(),
		"cart_amount":      amount,
	})
	if err != nil {
		return fmt.Errorf("paytabs refund: %w", err)
	}

	var tx PTTransaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		return fmt.Errorf("paytabs decode refund response (status %d): %w", status, err)
	}
	if !tx.succeeded() {
		return fmt.Errorf("paytabs refund failed (status %d): %s", status, raw)
	}
	return nil
}
