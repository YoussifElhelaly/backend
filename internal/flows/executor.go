package flows

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Node types
const (
	NodeSendMessage = "send_message"
	NodeAddTag      = "add_tag"
	NodeAddToFunnel = "add_to_funnel"
	NodeAssignAgent = "assign_agent"
)

type Node struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`  // send_message
	TagID   string `json:"tag_id,omitempty"`   // add_tag
	FunnelID string `json:"funnel_id,omitempty"` // add_to_funnel
	StepID  string `json:"step_id,omitempty"`  // add_to_funnel
	AgentID string `json:"agent_id,omitempty"` // assign_agent
}

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
) {
	var flows []models.Flow
	database.DB.Where("tenant_id = ? AND is_active = true", tenantID).Find(&flows)

	for _, flow := range flows {
		if !matchesTrigger(flow, messageText, sessionPhone, isNewContact) {
			continue
		}
		go execute(flow, tenantID, sessionPhone, contactID, contact, conversationID)
	}
}

func matchesTrigger(flow models.Flow, text, sessionPhone string, isNewContact bool) bool {
	// Optional session filter
	if flow.SessionPhone != "" && flow.SessionPhone != sessionPhone {
		return false
	}

	switch flow.Trigger {
	case models.FlowTriggerAnyMessage:
		return true
	case models.FlowTriggerKeyword:
		return flow.Keyword != "" &&
			strings.Contains(strings.ToLower(text), strings.ToLower(flow.Keyword))
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
) {
	var nodes []Node
	if err := json.Unmarshal([]byte(flow.Nodes), &nodes); err != nil {
		log.Printf("flows: failed to parse nodes for flow %s: %v", flow.ID, err)
		return
	}

	for _, node := range nodes {
		switch node.Type {
		case NodeSendMessage:
			text := personalize(node.Message, contact)
			if err := sendText(tenantID, sessionPhone, contact.PhoneNumber, text); err != nil {
				log.Printf("flows: send_message failed: %v", err)
			}

		case NodeAddTag:
			tagID, err := uuid.Parse(node.TagID)
			if err != nil {
				continue
			}
			var tag models.Tag
			if database.DB.Where("id = ? AND tenant_id = ?", tagID, tenantID).First(&tag).Error == nil {
				database.DB.Exec(
					"INSERT INTO contact_tags (contact_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
					contactID, tagID,
				)
			}

		case NodeAddToFunnel:
			funnelID, err := uuid.Parse(node.FunnelID)
			if err != nil {
				continue
			}
			stepID, err := uuid.Parse(node.StepID)
			if err != nil {
				continue
			}
			// Skip if already in this funnel
			var existing models.FunnelContact
			if database.DB.Where("funnel_id = ? AND contact_id = ?", funnelID, contactID).First(&existing).Error == nil {
				continue
			}
			fc := models.FunnelContact{
				FunnelID:      funnelID,
				ContactID:     contactID,
				CurrentStepID: stepID,
				Status:        models.FunnelContactActive,
			}
			database.DB.Create(&fc)
			database.DB.Create(&models.FunnelContactHistory{
				FunnelID:  funnelID,
				ContactID: contactID,
				ToStepID:  stepID,
				Trigger:   "AUTO_FLOW",
			})

		case NodeAssignAgent:
			agentID, err := uuid.Parse(node.AgentID)
			if err != nil {
				continue
			}
			database.DB.Model(&models.Conversation{}).
				Where("id = ?", conversationID).
				Update("assigned_to", agentID)
		}
	}

	// Increment run count
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

func sendText(tenantID uuid.UUID, sessionPhone, to, text string) error {
	var sess models.WhatsAppSession
	if err := database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tenantID, sessionPhone).
		First(&sess).Error; err != nil {
		return fmt.Errorf("session not connected")
	}
	client := session.Mgr.GetClient(sess.ID.String())
	if client == nil {
		return fmt.Errorf("whatsapp client not found")
	}
	jid := types.NewJID(to, types.DefaultUserServer)
	_, err := client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: proto.String(text),
	})
	return err
}
