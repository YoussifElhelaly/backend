package flows

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/types"
)

// Node types
const (
	NodeSendMessage = "send_message"
	NodeSendMedia   = "send_media"
	NodeAddTag      = "add_tag"
	NodeRemoveTag   = "remove_tag"
	NodeAddToFunnel = "add_to_funnel"
	NodeAssignAgent = "assign_agent"
	NodeDelay       = "delay"
)

type Node struct {
	Type         string   `json:"type"`
	Message      string   `json:"message,omitempty"`   // send_message
	Caption      string   `json:"caption,omitempty"`   // send_media caption
	Variants     []string `json:"variants,omitempty"`  // send_message — AI-approved clones
	TagID        string   `json:"tag_id,omitempty"`    // add_tag / remove_tag
	FunnelID     string   `json:"funnel_id,omitempty"` // add_to_funnel
	StepID       string   `json:"step_id,omitempty"`   // add_to_funnel
	AgentID      string   `json:"agent_id,omitempty"`  // assign_agent
	DelaySeconds int      `json:"delay_seconds,omitempty"` // delay
}

type actionResult struct {
	Type   string `json:"type"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status"` // ok | error
	Error  string `json:"error,omitempty"`
}

// ── Cooldown ──────────────────────────────────────────────────────────────────

var (
	cooldownMu sync.Mutex
	cooldowns  = map[string]time.Time{}
)

func init() {
	// Clean up stale cooldown entries every hour
	go func() {
		for {
			time.Sleep(time.Hour)
			cutoff := time.Now().Add(-24 * time.Hour)
			cooldownMu.Lock()
			for k, t := range cooldowns {
				if t.Before(cutoff) {
					delete(cooldowns, k)
				}
			}
			cooldownMu.Unlock()
		}
	}()
}

func cooldownAllowed(tenantID, flowID uuid.UUID, contactPhone string, windowSec int) bool {
	if windowSec <= 0 {
		return true
	}
	key := fmt.Sprintf("%s:%s:%s", tenantID, flowID, contactPhone)
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	if last, ok := cooldowns[key]; ok && time.Since(last) < time.Duration(windowSec)*time.Second {
		return false
	}
	cooldowns[key] = time.Now()
	return true
}

// ── HandleIncoming ────────────────────────────────────────────────────────────

// HandleIncoming is called for every inbound message.
// It finds matching flows and executes their actions.
func HandleIncoming(
	tenantID uuid.UUID,
	sessionPhone string,
	contactID uuid.UUID,
	contact *models.Contact,
	conversationID uuid.UUID,
	messageText string,
	isNewContact bool,
	waMessageID string,
) {
	var flows []models.Flow
	database.DB.Where("tenant_id = ? AND is_active = true", tenantID).Find(&flows)

	for _, flow := range flows {
		if !matchesTrigger(flow, messageText, sessionPhone, isNewContact) {
			continue
		}
		if !cooldownAllowed(tenantID, flow.ID, contact.PhoneNumber, flow.CooldownSeconds) {
			slog.Debug("flows: skipped — cooldown active", "flow_id", flow.ID, "contact", contact.PhoneNumber)
			continue
		}
		f := flow // capture for goroutine
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("flows: PANIC recovered in execute", "flow_id", f.ID, "panic", r)
				}
			}()
			execute(f, tenantID, sessionPhone, contactID, contact, conversationID, messageText, waMessageID)
		}()
	}
}

func matchesTrigger(flow models.Flow, text, sessionPhone string, isNewContact bool) bool {
	if flow.SessionPhone != "" && flow.SessionPhone != sessionPhone {
		return false
	}

	switch flow.Trigger {
	case models.FlowTriggerAnyMessage:
		return true
	case models.FlowTriggerKeyword:
		if flow.Keyword == "" {
			return false
		}
		lower := strings.ToLower(text)
		kw := strings.ToLower(flow.Keyword)
		switch flow.KeywordMatchType {
		case "exact":
			return lower == kw
		case "starts_with":
			return strings.HasPrefix(lower, kw)
		default: // "contains"
			return strings.Contains(lower, kw)
		}
	case models.FlowTriggerNewContact:
		return isNewContact
	}
	return false
}

func execute(
	flow models.Flow,
	tenantID uuid.UUID,
	sessionPhone string,
	contactID uuid.UUID,
	contact *models.Contact,
	conversationID uuid.UUID,
	triggerMsg string,
	waMessageID string,
) {
	var nodes []Node
	if err := json.Unmarshal([]byte(flow.Nodes), &nodes); err != nil {
		slog.Error("flows: failed to parse nodes", "flow_id", flow.ID, "error", err)
		return
	}

	results := make([]actionResult, 0, len(nodes))
	runStatus := "completed"

	// Emulate read receipt
	if waMessageID != "" {
		var sess models.WhatsAppSession
		if err := database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tenantID, sessionPhone).First(&sess).Error; err == nil {
			chatJID := types.NewJID(contact.PhoneNumber, types.DefaultUserServer)
			_ = session.Mgr.MarkMessageAsRead(sess.ID.String(), chatJID, chatJID, waMessageID)
			// Small delay after reading before typing
			time.Sleep(1 * time.Second)
		}
	}

	for _, node := range nodes {
		res := actionResult{Type: node.Type, Status: "ok"}

		switch node.Type {

		case NodeSendMessage:
			msg := node.Message
			if len(node.Variants) > 0 {
				msg = node.Variants[rand.Intn(len(node.Variants))]
			}
			text := personalize(msg, contact)
			if err := doSendText(tenantID, sessionPhone, contact.PhoneNumber, text); err != nil {
				res.Status = "error"
				res.Error = err.Error()
				runStatus = "partial"
				slog.Error("flows: send_message failed", "flow_id", flow.ID, "error", err)
			} else {
				if len(text) > 60 {
					res.Detail = text[:60] + "…"
				} else {
					res.Detail = text
				}
			}

		case NodeSendMedia:
			if len(flow.MediaPayload) == 0 {
				res.Status = "error"
				res.Error = "no media attached to flow"
				runStatus = "partial"
				break
			}
			caption := personalize(node.Caption, contact)
			if err := doSendMedia(tenantID, sessionPhone, contact.PhoneNumber, flow.MediaPayload, flow.MediaMime, flow.MediaName, caption); err != nil {
				res.Status = "error"
				res.Error = err.Error()
				runStatus = "partial"
				slog.Error("flows: send_media failed", "flow_id", flow.ID, "error", err)
			} else {
				res.Detail = flow.MediaName
			}

		case NodeAddTag:
			tagID, err := uuid.Parse(node.TagID)
			if err != nil {
				res.Status = "error"
				res.Error = "invalid tag_id"
				runStatus = "partial"
				break
			}
			var tag models.Tag
			if database.DB.Where("id = ? AND tenant_id = ?", tagID, tenantID).First(&tag).Error == nil {
				database.DB.Exec(
					"INSERT INTO contact_tags (contact_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
					contactID, tagID,
				)
				res.Detail = tag.Name
			}

		case NodeRemoveTag:
			tagID, err := uuid.Parse(node.TagID)
			if err != nil {
				res.Status = "error"
				res.Error = "invalid tag_id"
				runStatus = "partial"
				break
			}
			var tag models.Tag
			if database.DB.Where("id = ? AND tenant_id = ?", tagID, tenantID).First(&tag).Error == nil {
				database.DB.Exec("DELETE FROM contact_tags WHERE contact_id = ? AND tag_id = ?", contactID, tagID)
				res.Detail = tag.Name
			}

		case NodeAddToFunnel:
			funnelID, err := uuid.Parse(node.FunnelID)
			if err != nil {
				res.Status = "error"
				res.Error = "invalid funnel_id"
				runStatus = "partial"
				break
			}
			stepID, err := uuid.Parse(node.StepID)
			if err != nil {
				res.Status = "error"
				res.Error = "invalid step_id"
				runStatus = "partial"
				break
			}
			var existing models.FunnelContact
			if database.DB.Where("funnel_id = ? AND contact_id = ?", funnelID, contactID).First(&existing).Error == nil {
				res.Detail = "already in funnel"
				break
			}
			var inOtherFunnel models.FunnelContact
			if database.DB.Where("contact_id = ? AND status = 'ACTIVE' AND funnel_id != ?", contactID, funnelID).First(&inOtherFunnel).Error == nil {
				res.Detail = "skipped: active in another funnel"
				break
			}
			now := time.Now()
			fc := models.FunnelContact{
				FunnelID:      funnelID,
				ContactID:     contactID,
				CurrentStepID: stepID,
				Status:        models.FunnelContactActive,
				EnteredAt:     now,
				LastMovedAt:   now,
			}
			database.DB.Create(&fc)
			database.DB.Create(&models.FunnelContactHistory{
				FunnelID:  funnelID,
				ContactID: contactID,
				ToStepID:  stepID,
				Trigger:   "AUTO_FLOW",
			})
			res.Detail = "added to funnel"

		case NodeAssignAgent:
			agentID, err := uuid.Parse(node.AgentID)
			if err != nil {
				res.Status = "error"
				res.Error = "invalid agent_id"
				runStatus = "partial"
				break
			}
			if err := database.DB.Model(&models.Conversation{}).
				Where("id = ?", conversationID).
				Update("assigned_to", agentID).Error; err != nil {
				res.Status = "error"
				res.Error = err.Error()
				runStatus = "partial"
			} else {
				res.Detail = agentID.String()
			}

		case NodeDelay:
			secs := node.DelaySeconds
			if secs < 1 {
				secs = 1
			}
			if secs > 300 {
				secs = 300 // max 5 minutes per delay node
			}
			time.Sleep(time.Duration(secs) * time.Second)
			res.Detail = fmt.Sprintf("%ds", secs)
		}

		results = append(results, res)
	}

	// Save run record
	msg := triggerMsg
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	actionsJSON, _ := json.Marshal(results)
	run := models.FlowRun{
		FlowID:     flow.ID,
		TenantID:   tenantID,
		ContactID:  &contactID,
		TriggerMsg: msg,
		Status:     runStatus,
		Actions:    string(actionsJSON),
		ExecutedAt: time.Now(),
	}
	database.DB.Create(&run)

	database.DB.Model(&flow).Update("run_count", flow.RunCount+1)
}

func personalize(template string, contact *models.Contact) string {
	name := contact.Name
	if name == "" {
		name = contact.PushName
	}
	if name == "" {
		name = contact.PhoneNumber
	}
	return strings.ReplaceAll(template, "{name}", name)
}

func doSendText(tenantID uuid.UUID, sessionPhone, to, text string) error {
	var sess models.WhatsAppSession
	if err := database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tenantID, sessionPhone).
		First(&sess).Error; err != nil {
		return fmt.Errorf("session not connected")
	}
	// session.Mgr.SendTextWithTyping
	_, err := session.Mgr.SendTextWithTyping(sess.ID.String(), to, text)
	return err
}

func doSendMedia(tenantID uuid.UUID, sessionPhone, to string, data []byte, mime, name, caption string) error {
	var sess models.WhatsAppSession
	if err := database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tenantID, sessionPhone).
		First(&sess).Error; err != nil {
		return fmt.Errorf("session not connected")
	}
	_, _, _, err := session.Mgr.SendMedia(sess.ID.String(), to, data, mime, name, caption)
	return err
}
