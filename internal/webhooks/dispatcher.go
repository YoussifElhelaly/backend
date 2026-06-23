package webhooks

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

type eventPayload struct {
	Event     string      `json:"event"`
	TenantID  string      `json:"tenant_id"`
	Timestamp string      `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// Dispatch fires a webhook event to all active registered URLs for the tenant
// that subscribe to this event. Runs each delivery in its own goroutine.
func Dispatch(tenantID uuid.UUID, event string, data interface{}) {
	var hooks []models.Webhook
	database.DB.Where("tenant_id = ? AND is_active = true", tenantID).Find(&hooks)
	if len(hooks) == 0 {
		return
	}

	payload := eventPayload{
		Event:     event,
		TenantID:  tenantID.String(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	for _, hook := range hooks {
		if !subscribes(hook.Events, event) {
			continue
		}
		go deliver(hook.URL, hook.Secret, event, body)
	}
}

// subscribes checks whether a JSON array string contains the given event name.
func subscribes(eventsJSON, event string) bool {
	return strings.Contains(eventsJSON, `"`+event+`"`)
}

func deliver(url, secret, event string, body []byte) {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("webhooks: build request failed for %s: %v", url, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Whatify-Signature", sig)
	req.Header.Set("X-Whatify-Event", event)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("webhooks: delivery failed → %s: %v", url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("webhooks: %s returned HTTP %d for event %s", url, resp.StatusCode, event)
	}
}
