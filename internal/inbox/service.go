package inbox

import (
	"fmt"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/internal/webhooks"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

// MigrateSessionPhone backfills session_phone on existing conversations that were
// created before the multi-session isolation fix. Safe to call on every startup.
func MigrateSessionPhone() {
	database.DB.Exec(`
		UPDATE conversations c
		SET session_phone = s.phone
		FROM whats_app_sessions s
		WHERE c.session_id = s.id
		  AND (c.session_phone = '' OR c.session_phone IS NULL)
		  AND s.phone != ''
	`)
}

// FunnelReplyHandler is wired from the funnels package to avoid import cycles.
var FunnelReplyHandler func(contactID, tenantID uuid.UUID)

// FlowHandler is wired from the flows package to avoid import cycles.
var FlowHandler func(tenantID uuid.UUID, sessionPhone string, contactID uuid.UUID, contact *models.Contact, conversationID uuid.UUID, text string, isNew bool)

// HandleIncoming is called by the session manager for each inbound WhatsApp message.
// HandleIncoming processes a WhatsApp message — either incoming from a contact or
// sent by the session owner from their own device (mobile/web). isFromMe=true means
// the session owner sent it from outside Whatify (no duplicate if already saved via API).
func HandleIncoming(
	sessionID, tenantID uuid.UUID,
	sessionPhone, phone, pushName, waMessageID, content string,
	msgType models.MessageType,
	timestamp time.Time,
	mediaPayload []byte,
	reactionToID string,
	isFromMe bool,
) {
	// Skip duplicate — message already saved via Whatify send API.
	if isFromMe && waMessageID != "" {
		var existing models.Message
		if database.DB.Where("wa_message_id = ?", waMessageID).First(&existing).Error == nil {
			return
		}
	}

	isNewContact := false
	contact := findOrCreateContactTracked(tenantID, sessionID, phone, pushName, &isNewContact)
	if contact == nil {
		return
	}

	conv := findOrCreateConversation(tenantID, sessionID, contact.ID, sessionPhone)
	if conv == nil {
		return
	}

	direction := models.DirectionIncoming
	if isFromMe {
		direction = models.DirectionOutgoing
	}

	msg := models.Message{
		ConversationID: conv.ID,
		TenantID:       tenantID,
		Type:           msgType,
		Content:        content,
		Direction:      direction,
		Status:         models.MessageStatusDelivered,
		WaMessageID:    waMessageID,
		IsNote:         false,
		Timestamp:      timestamp,
		MediaPayload:   mediaPayload,
		ReactionToID:   reactionToID,
	}
	if err := database.DB.Create(&msg).Error; err != nil {
		return
	}

	updateExpr := "UPDATE conversations SET updated_at = NOW(), last_message_at = ? WHERE id = ?"
	if !isFromMe {
		updateExpr = "UPDATE conversations SET updated_at = NOW(), unread_count = unread_count + 1, last_message_at = ? WHERE id = ?"
	}
	database.DB.Exec(updateExpr, timestamp, conv.ID)

	var fullConv models.Conversation
	database.DB.Preload("Contact").First(&fullConv, "id = ?", conv.ID)

	GlobalHub.Broadcast(tenantID.String(), WSEvent{
		Event: "new_message",
		Data: map[string]interface{}{
			"conversation": toConvResponse(fullConv, &msg),
			"message":      toMsgResponse(msg),
		},
	})

	if isFromMe {
		return
	}

	// Dispatch webhook event for incoming message
	go webhooks.Dispatch(tenantID, "message.incoming", map[string]interface{}{
		"conversation_id": conv.ID.String(),
		"contact_phone":   phone,
		"content":         content,
		"type":            string(msgType),
		"timestamp":       timestamp.Format(time.RFC3339),
	})

	// Trigger funnel auto-advance if contact is in an active funnel
	if FunnelReplyHandler != nil {
		go FunnelReplyHandler(contact.ID, tenantID)
	}

	// Trigger flow automation
	if FlowHandler != nil {
		go FlowHandler(tenantID, sessionPhone, contact.ID, contact, conv.ID, content, isNewContact)
	}
}

func findOrCreateContact(tenantID, sessionID uuid.UUID, phone, pushName string) *models.Contact {
	isNew := false
	return findOrCreateContactTracked(tenantID, sessionID, phone, pushName, &isNew)
}

func findOrCreateContactTracked(tenantID, sessionID uuid.UUID, phone, pushName string, isNew *bool) *models.Contact {
	var c models.Contact
	if err := database.DB.Where("tenant_id = ? AND phone_number = ?", tenantID, phone).First(&c).Error; err == nil {
		if pushName != "" && c.PushName != pushName {
			database.DB.Model(&c).Update("push_name", pushName)
			c.PushName = pushName
		}
		return &c
	}
	*isNew = true
	c = models.Contact{
		TenantID:    tenantID,
		SessionID:   sessionID,
		PhoneNumber: phone,
		PushName:    pushName,
		WaID:        phone + "@s.whatsapp.net",
	}
	if err := database.DB.Create(&c).Error; err != nil {
		return nil
	}
	tryFetchAvatar(sessionID, &c)
	return &c
}

func findOrCreateConversation(tenantID, sessionID uuid.UUID, contactID uuid.UUID, sessionPhone string) *models.Conversation {
	var conv models.Conversation
	// Conversations are scoped to a specific business phone number so that
	// two different sessions (numbers) each get their own conversation thread
	// with the same customer. session_phone never changes even if the session
	// record is deleted and recreated; session_id is refreshed if it drifted.
	if err := database.DB.Where("tenant_id = ? AND contact_id = ? AND session_phone = ?", tenantID, contactID, sessionPhone).First(&conv).Error; err == nil {
		if conv.SessionID != sessionID {
			database.DB.Model(&conv).Update("session_id", sessionID)
			conv.SessionID = sessionID
		}
		return &conv
	}
	conv = models.Conversation{
		TenantID:     tenantID,
		SessionID:    sessionID,
		SessionPhone: sessionPhone,
		ContactID:    contactID,
		Status:       models.ConvStatusOpen,
	}
	if err := database.DB.Create(&conv).Error; err != nil {
		return nil
	}
	return &conv
}

func getConversations(tenantID uuid.UUID, page, limit int, sessionPhone string) ([]ConversationResponse, error) {
	offset := (page - 1) * limit
	var convs []models.Conversation
	q := database.DB.Preload("Contact").Where("tenant_id = ?", tenantID)
	if sessionPhone != "" {
		q = q.Where("session_phone = ?", sessionPhone)
	}
	if err := q.
		Order("last_message_at DESC NULLS LAST").
		Limit(limit).Offset(offset).
		Find(&convs).Error; err != nil {
		return nil, err
	}

	out := make([]ConversationResponse, len(convs))
	for i, c := range convs {
		var lastMsg models.Message
		var lastMsgPtr *models.Message
		if res := database.DB.Where("conversation_id = ?", c.ID).Order("timestamp desc").Limit(1).First(&lastMsg); res.Error == nil {
			lastMsgPtr = &lastMsg
		}
		out[i] = toConvResponse(c, lastMsgPtr)
	}
	return out, nil
}

func getConversationByID(tenantID, conversationID uuid.UUID) (*ConversationResponse, error) {
	var conv models.Conversation
	if err := database.DB.Preload("Contact").Where("id = ? AND tenant_id = ?", conversationID, tenantID).First(&conv).Error; err != nil {
		return nil, fmt.Errorf("conversation not found")
	}
	var lastMsg models.Message
	var lastMsgPtr *models.Message
	if res := database.DB.Where("conversation_id = ?", conv.ID).Order("timestamp desc").Limit(1).First(&lastMsg); res.Error == nil {
		lastMsgPtr = &lastMsg
	}
	r := toConvResponse(conv, lastMsgPtr)
	return &r, nil
}

func getMessages(tenantID, conversationID uuid.UUID, page, limit int) ([]MessageResponse, error) {
	var conv models.Conversation
	if err := database.DB.Where("id = ? AND tenant_id = ?", conversationID, tenantID).First(&conv).Error; err != nil {
		return nil, fmt.Errorf("conversation not found")
	}

	offset := (page - 1) * limit
	var msgs []models.Message
	if err := database.DB.
		Where("conversation_id = ?", conversationID).
		Order("timestamp asc").
		Limit(limit).Offset(offset).
		Find(&msgs).Error; err != nil {
		return nil, err
	}

	out := make([]MessageResponse, len(msgs))
	for i, m := range msgs {
		out[i] = toMsgResponse(m)
	}
	return out, nil
}

// sendMessage stores a message in DB and broadcasts it. Actual WA sending is done by the caller.
func sendMessage(tenantID, conversationID uuid.UUID, content string, senderID uuid.UUID, waMessageID string) (*MessageResponse, error) {
	var conv models.Conversation
	if err := database.DB.Preload("Contact").Where("id = ? AND tenant_id = ?", conversationID, tenantID).First(&conv).Error; err != nil {
		return nil, fmt.Errorf("conversation not found")
	}

	msg := models.Message{
		ConversationID: conv.ID,
		TenantID:       tenantID,
		SenderID:       &senderID,
		Type:           models.MessageTypeText,
		Content:        content,
		Direction:      models.DirectionOutgoing,
		Status:         models.MessageStatusSent,
		WaMessageID:    waMessageID,
		IsNote:         false,
		Timestamp:      time.Now(),
	}
	if err := database.DB.Create(&msg).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	database.DB.Exec("UPDATE conversations SET updated_at = NOW(), last_message_at = ? WHERE id = ?", now, conv.ID)

	conv.UpdatedAt = now
	conv.LastMessageAt = &now

	resp := toMsgResponse(msg)
	GlobalHub.Broadcast(tenantID.String(), WSEvent{
		Event: "new_message",
		Data: map[string]interface{}{
			"conversation": toConvResponse(conv, &msg),
			"message":      resp,
		},
	})

	return &resp, nil
}

func createNote(tenantID, conversationID uuid.UUID, content string, senderID uuid.UUID) (*MessageResponse, error) {
	var conv models.Conversation
	if err := database.DB.Where("id = ? AND tenant_id = ?", conversationID, tenantID).First(&conv).Error; err != nil {
		return nil, fmt.Errorf("conversation not found")
	}

	msg := models.Message{
		ConversationID: conv.ID,
		TenantID:       tenantID,
		SenderID:       &senderID,
		Type:           models.MessageTypeText,
		Content:        content,
		Direction:      models.DirectionOutgoing,
		Status:         models.MessageStatusSent,
		IsNote:         true,
		Timestamp:      time.Now(),
	}
	if err := database.DB.Create(&msg).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	database.DB.Exec("UPDATE conversations SET updated_at = NOW(), last_message_at = ? WHERE id = ?", now, conv.ID)

	conv.UpdatedAt = now
	conv.LastMessageAt = &now

	resp := toMsgResponse(msg)
	GlobalHub.Broadcast(tenantID.String(), WSEvent{
		Event: "new_note",
		Data: map[string]interface{}{
			"conversation": toConvResponse(conv, &msg),
			"message":      resp,
		},
	})

	return &resp, nil
}

func assignConversation(tenantID, conversationID uuid.UUID, agentID *uuid.UUID) error {
	return database.DB.Model(&models.Conversation{}).
		Where("id = ? AND tenant_id = ?", conversationID, tenantID).
		Update("assigned_to", agentID).Error
}

func updateConvStatus(tenantID, conversationID uuid.UUID, status models.ConversationStatus) error {
	return database.DB.Model(&models.Conversation{}).
		Where("id = ? AND tenant_id = ?", conversationID, tenantID).
		Update("status", status).Error
}

func markRead(tenantID, conversationID uuid.UUID) error {
	return database.DB.Model(&models.Conversation{}).
		Where("id = ? AND tenant_id = ?", conversationID, tenantID).
		Update("unread_count", 0).Error
}

func toConvResponse(c models.Conversation, lastMsg *models.Message) ConversationResponse {
	r := ConversationResponse{
		ID:           c.ID.String(),
		SessionID:    c.SessionID.String(),
		SessionPhone: c.SessionPhone,
		Status:       string(c.Status),
		UnreadCount:  c.UnreadCount,
		UpdatedAt:    c.UpdatedAt.Format(time.RFC3339),
		CreatedAt:    c.CreatedAt.Format(time.RFC3339),
		Contact: ContactResponse{
			ID:          c.Contact.ID.String(),
			PhoneNumber: c.Contact.PhoneNumber,
			Name:        c.Contact.Name,
			PushName:    c.Contact.PushName,
			AvatarURL:   c.Contact.AvatarURL,
		},
	}
	if c.LastMessageAt != nil {
		s := c.LastMessageAt.Format(time.RFC3339)
		r.LastMessageAt = &s
	}
	if c.AssignedTo != nil {
		s := c.AssignedTo.String()
		r.AssignedTo = &s
	}
	if lastMsg != nil && lastMsg.ID != uuid.Nil {
		mr := toMsgResponse(*lastMsg)
		r.LastMessage = &mr
	}
	return r
}

func toMsgResponse(m models.Message) MessageResponse {
	r := MessageResponse{
		ID:             m.ID.String(),
		ConversationID: m.ConversationID.String(),
		Type:           string(m.Type),
		Content:        m.Content,
		MediaURL:       m.MediaURL,
		Direction:      string(m.Direction),
		Status:         string(m.Status),
		IsNote:         m.IsNote,
		Timestamp:      m.Timestamp.Format(time.RFC3339),
		ReactionToID:   m.ReactionToID,
		WaMessageID:    m.WaMessageID,
	}
	if m.SenderID != nil {
		s := m.SenderID.String()
		r.SenderID = &s
	}
	return r
}
