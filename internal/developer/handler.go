package developer

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/internal/webhooks"
	"whatify/backend/pkg/database"

	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── Messages ────────────────────────────────────────────────────────────────

type sendMessageInput struct {
	To           string `json:"to" binding:"required"`           // phone number (digits only, e.g. "201029676919")
	Message      string `json:"message" binding:"required"`     // text content
	SessionPhone string `json:"session_phone"`                   // optional: which WA number to send from (defaults to first connected)
}

func handleSendMessage(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var input sendMessageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Resolve session: use provided session_phone or find first connected.
	var sess models.WhatsAppSession
	q := database.DB.Where("tenant_id = ? AND status = ?", tenantID, models.StatusConnected)
	if input.SessionPhone != "" {
		q = q.Where("phone = ?", input.SessionPhone)
	}
	if err := q.First(&sess).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "no connected session found — connect a WhatsApp number first",
			"code":  "no_session",
		})
		return
	}

	// Check daily message limit.
	if limitErr := billing.CheckDailyMessageLimit(tenantID, sess.Phone); limitErr != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": limitErr.Error(), "code": "daily_limit_exceeded"})
		return
	}

	// Find or create contact for this phone number.
	var contact models.Contact
	if err := database.DB.Where("tenant_id = ? AND phone_number = ?", tenantID, input.To).First(&contact).Error; err != nil {
		contact = models.Contact{
			TenantID:    tenantID,
			SessionID:   sess.ID,
			PhoneNumber: input.To,
		}
		if err := database.DB.Create(&contact).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create contact"})
			return
		}
	}

	// Find or create conversation.
	var conv models.Conversation
	if err := database.DB.Where("tenant_id = ? AND contact_id = ? AND session_phone = ?",
		tenantID, contact.ID, sess.Phone).First(&conv).Error; err != nil {
		conv = models.Conversation{
			TenantID:     tenantID,
			SessionID:    sess.ID,
			SessionPhone: sess.Phone,
			ContactID:    contact.ID,
			Status:       models.ConvStatusOpen,
			ChatType:     models.ChatTypeIndividual,
		}
		if err := database.DB.Create(&conv).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create conversation"})
			return
		}
	}

	// Send via WhatsApp.
	waID, waErr := session.Mgr.SendText(sess.ID.String(), input.To, input.Message)
	if waErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "whatsapp send failed: " + waErr.Error()})
		return
	}

	// Store message in DB.
	now := time.Now()
	msg := models.Message{
		ConversationID: conv.ID,
		TenantID:       tenantID,
		Type:           models.MessageTypeText,
		Content:        input.Message,
		Direction:      models.DirectionOutgoing,
		Status:         models.MessageStatusSent,
		WaMessageID:    waID,
		Timestamp:      now,
	}
	if err := database.DB.Create(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store message"})
		return
	}

	// Update conversation timestamp.
	database.DB.Exec("UPDATE conversations SET updated_at = NOW(), last_message_at = ? WHERE id = ?", now, conv.ID)

	// Increment daily count.
	billing.IncrementDailyCount(tenantID, sess.Phone)

	// Dispatch webhook.
	go webhooks.Dispatch(tenantID, "message.sent", map[string]interface{}{
		"conversation_id": conv.ID.String(),
		"contact_phone":   input.To,
		"content":         input.Message,
	})

	c.JSON(http.StatusCreated, gin.H{
		"id":              msg.ID.String(),
		"conversation_id": conv.ID.String(),
		"contact_phone":   input.To,
		"content":         input.Message,
		"status":          string(msg.Status),
		"timestamp":       msg.Timestamp.Format(time.RFC3339),
	})
}

// ── Conversations ───────────────────────────────────────────────────────────

func handleListConversations(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	sessionPhone := c.Query("session_phone")
	status := c.Query("status") // OPEN or RESOLVED
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 30
	}

	offset := (page - 1) * limit
	q := database.DB.Preload("Contact").Where("tenant_id = ?", tenantID)
	if sessionPhone != "" {
		q = q.Where("session_phone = ?", sessionPhone)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}

	var total int64
	q.Model(&models.Conversation{}).Count(&total)

	var convs []models.Conversation
	if err := q.Order("last_message_at DESC NULLS LAST").Limit(limit).Offset(offset).Find(&convs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Batch-load last messages.
	convIDs := make([]interface{}, len(convs))
	for i, c := range convs {
		convIDs[i] = c.ID
	}
	type lastMsgRow struct {
		ConversationID uuid.UUID
		ID             uuid.UUID
		Content        string
		Type           string
		Direction      string
		Timestamp      time.Time
	}
	var lastMsgs []lastMsgRow
	if len(convIDs) > 0 {
		database.DB.Raw(`
			SELECT DISTINCT ON (conversation_id)
				conversation_id, id, content, type, direction, timestamp
			FROM messages
			WHERE conversation_id IN ?
			ORDER BY conversation_id, timestamp DESC
		`, convIDs).Find(&lastMsgs)
	}
	lastMsgMap := map[uuid.UUID]*models.Message{}
	for _, row := range lastMsgs {
		lastMsgMap[row.ConversationID] = &models.Message{
			ID:             row.ID,
			ConversationID: row.ConversationID,
			Content:        row.Content,
			Type:           models.MessageType(row.Type),
			Direction:      models.MessageDirection(row.Direction),
			Timestamp:      row.Timestamp,
		}
	}

	type convResp struct {
		ID            string      `json:"id"`
		SessionPhone  string      `json:"session_phone"`
		Contact       contactResp `json:"contact"`
		Status        string      `json:"status"`
		ChatType      string      `json:"chat_type"`
		UnreadCount   int         `json:"unread_count"`
		LastMessage   *msgResp    `json:"last_message,omitempty"`
		LastMessageAt *string     `json:"last_message_at,omitempty"`
		CreatedAt     string      `json:"created_at"`
	}

	out := make([]convResp, len(convs))
	for i, c := range convs {
		cr := convResp{
			ID:           c.ID.String(),
			SessionPhone: c.SessionPhone,
			Contact: contactResp{
				ID:          c.Contact.ID.String(),
				PhoneNumber: c.Contact.PhoneNumber,
				Name:        c.Contact.Name,
				PushName:    c.Contact.PushName,
			},
			Status:      string(c.Status),
			ChatType:    string(c.ChatType),
			UnreadCount: c.UnreadCount,
			CreatedAt:   c.CreatedAt.Format(time.RFC3339),
		}
		if c.LastMessageAt != nil {
			s := c.LastMessageAt.Format(time.RFC3339)
			cr.LastMessageAt = &s
		}
		if lm, ok := lastMsgMap[c.ID]; ok {
			cr.LastMessage = &msgResp{
				ID:        lm.ID.String(),
				Content:   lm.Content,
				Type:      string(lm.Type),
				Direction: string(lm.Direction),
				Timestamp: lm.Timestamp.Format(time.RFC3339),
			}
		}
		out[i] = cr
	}

	c.JSON(http.StatusOK, gin.H{
		"conversations": out,
		"total":         total,
		"page":          page,
		"limit":         limit,
	})
}

func handleGetConversation(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
		return
	}

	var conv models.Conversation
	if err := database.DB.Preload("Contact").Where("id = ? AND tenant_id = ?", id, tenantID).First(&conv).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	var lastMsg models.Message
	var lastMsgPtr *msgResp
	if res := database.DB.Where("conversation_id = ?", conv.ID).Order("timestamp desc").Limit(1).First(&lastMsg); res.Error == nil {
		lastMsgPtr = &msgResp{
			ID:        lastMsg.ID.String(),
			Content:   lastMsg.Content,
			Type:      string(lastMsg.Type),
			Direction: string(lastMsg.Direction),
			Timestamp: lastMsg.Timestamp.Format(time.RFC3339),
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":            conv.ID.String(),
		"session_phone": conv.SessionPhone,
		"contact": contactResp{
			ID:          conv.Contact.ID.String(),
			PhoneNumber: conv.Contact.PhoneNumber,
			Name:        conv.Contact.Name,
			PushName:    conv.Contact.PushName,
		},
		"status":        string(conv.Status),
		"chat_type":     string(conv.ChatType),
		"unread_count":  conv.UnreadCount,
		"last_message":  lastMsgPtr,
		"created_at":    conv.CreatedAt.Format(time.RFC3339),
	})
}

func handleListMessages(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}

	// Verify conversation belongs to this tenant.
	var conv models.Conversation
	if err := database.DB.Where("id = ? AND tenant_id = ?", convID, tenantID).First(&conv).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	offset := (page - 1) * limit
	var msgs []models.Message
	if err := database.DB.
		Where("conversation_id = ?", convID).
		Order("timestamp asc").
		Limit(limit).Offset(offset).
		Find(&msgs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	out := make([]msgResp, len(msgs))
	for i, m := range msgs {
		out[i] = msgResp{
			ID:             m.ID.String(),
			ConversationID: m.ConversationID.String(),
			Type:           string(m.Type),
			Content:        m.Content,
			Direction:      string(m.Direction),
			Status:         string(m.Status),
			IsNote:         m.IsNote,
			Timestamp:      m.Timestamp.Format(time.RFC3339),
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"messages": out,
		"page":     page,
		"limit":    limit,
	})
}

// ── Contacts ────────────────────────────────────────────────────────────────

func handleListContacts(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	search := c.Query("q")
	sessionPhone := c.Query("session_phone")

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset := (page - 1) * limit

	query := database.DB.Model(&models.Contact{}).Where("tenant_id = ?", tenantID)

	if sessionPhone != "" {
		sub := "id IN (SELECT contact_id FROM conversations WHERE tenant_id = ? AND session_phone = ? AND deleted_at IS NULL)"
		var sess models.WhatsAppSession
		if err := database.DB.Where("tenant_id = ? AND phone = ?", tenantID, sessionPhone).First(&sess).Error; err == nil {
			query = query.Where("session_id = ? OR "+sub, sess.ID, tenantID, sessionPhone)
		} else {
			query = query.Where(sub, tenantID, sessionPhone)
		}
	}

	if search != "" {
		query = query.Where("name ILIKE ? OR push_name ILIKE ? OR phone_number ILIKE ?",
			"%"+search+"%", "%"+search+"%", "%"+search+"%")
	}

	var total int64
	query.Count(&total)

	var contacts []models.Contact
	if err := query.Preload("Tags").Order("name ASC").Offset(offset).Limit(limit).Find(&contacts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type contactWithTag struct {
		ID          string      `json:"id"`
		PhoneNumber string      `json:"phone_number"`
		Name        string      `json:"name"`
		PushName    string      `json:"push_name"`
		AvatarURL   string      `json:"avatar_url,omitempty"`
		Tags        []tagResp   `json:"tags"`
		CreatedAt   string      `json:"created_at"`
	}

	out := make([]contactWithTag, len(contacts))
	for i, ct := range contacts {
		tags := make([]tagResp, len(ct.Tags))
		for j, t := range ct.Tags {
			tags[j] = tagResp{ID: t.ID.String(), Name: t.Name, Color: t.Color}
		}
		out[i] = contactWithTag{
			ID:          ct.ID.String(),
			PhoneNumber: ct.PhoneNumber,
			Name:        ct.Name,
			PushName:    ct.PushName,
			AvatarURL:   ct.AvatarURL,
			Tags:        tags,
			CreatedAt:   ct.CreatedAt.Format(time.RFC3339),
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"contacts": out,
		"total":    total,
		"page":     page,
		"limit":    limit,
	})
}

func handleGetContact(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contact id"})
		return
	}

	var contact models.Contact
	if err := database.DB.Preload("Tags").Where("id = ? AND tenant_id = ?", id, tenantID).First(&contact).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contact not found"})
		return
	}

	tags := make([]tagResp, len(contact.Tags))
	for i, t := range contact.Tags {
		tags[i] = tagResp{ID: t.ID.String(), Name: t.Name, Color: t.Color}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":           contact.ID.String(),
		"phone_number": contact.PhoneNumber,
		"name":         contact.Name,
		"push_name":    contact.PushName,
		"avatar_url":   contact.AvatarURL,
		"tags":         tags,
		"created_at":   contact.CreatedAt.Format(time.RFC3339),
	})
}

// ── Sessions ────────────────────────────────────────────────────────────────

func handleListSessions(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var sessions []models.WhatsAppSession
	if err := database.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&sessions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type sessResp struct {
		ID         string  `json:"id"`
		Phone      string  `json:"phone"`
		Status     string  `json:"status"`
		DailyCount int     `json:"daily_count"`
		LastActive *string `json:"last_active,omitempty"`
		CreatedAt  string  `json:"created_at"`
	}

	out := make([]sessResp, len(sessions))
	for i, s := range sessions {
		r := sessResp{
			ID:         s.ID.String(),
			Phone:      s.Phone,
			Status:     string(s.Status),
			DailyCount: s.DailyCount,
			CreatedAt:  s.CreatedAt.Format(time.RFC3339),
		}
		if s.LastActive != nil {
			la := s.LastActive.Format(time.RFC3339)
			r.LastActive = &la
		}
		out[i] = r
	}

	c.JSON(http.StatusOK, out)
}

// ── Webhooks ────────────────────────────────────────────────────────────────

type webhookInput struct {
	URL    string   `json:"url" binding:"required,url"`
	Events []string `json:"events" binding:"required,min=1"`
}

func handleCreateWebhook(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var input webhookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate events.
	validEvents := map[string]bool{
		"message.incoming": true, "message.sent": true,
		"conversation.assigned": true, "conversation.resolved": true, "conversation.reopened": true,
		"contact.updated": true,
	}
	for _, e := range input.Events {
		if !validEvents[e] {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("invalid event: %s — valid events: message.incoming, message.sent, conversation.assigned, conversation.resolved, conversation.reopened, contact.updated", e),
			})
			return
		}
	}

	// Build events JSON.
	eventsJSON := "["
	for i, e := range input.Events {
		if i > 0 {
			eventsJSON += ","
		}
		eventsJSON += `"` + e + `"`
	}
	eventsJSON += "]"

	// Generate secret.
	b := make([]byte, 20)
	rand.Read(b)
	secret := "whsec_" + hex.EncodeToString(b)

	hook := models.Webhook{
		TenantID: tenantID,
		URL:      input.URL,
		Events:   eventsJSON,
		Secret:   secret,
		IsActive: true,
	}

	if err := database.DB.Create(&hook).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create webhook"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        hook.ID.String(),
		"url":       hook.URL,
		"events":    input.Events,
		"secret":    secret,
		"is_active": hook.IsActive,
		"created_at": hook.CreatedAt.Format(time.RFC3339),
	})
}

func handleListWebhooks(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var hooks []models.Webhook
	database.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&hooks)

	type hookResp struct {
		ID        string   `json:"id"`
		URL       string   `json:"url"`
		Events    []string `json:"events"`
		Secret    string   `json:"secret"`
		IsActive  bool     `json:"is_active"`
		CreatedAt string   `json:"created_at"`
	}

	out := make([]hookResp, len(hooks))
	for i, h := range hooks {
		var events []string
		e := h.Events
		if len(e) > 2 {
			e = e[1 : len(e)-1]
			events = splitEvents(e)
		}
		secret := h.Secret
		if len(secret) > 14 {
			secret = secret[:14] + "••••"
		}
		out[i] = hookResp{
			ID:        h.ID.String(),
			URL:       h.URL,
			Events:    events,
			Secret:    secret,
			IsActive:  h.IsActive,
			CreatedAt: h.CreatedAt.Format(time.RFC3339),
		}
	}

	c.JSON(http.StatusOK, out)
}

func handleDeleteWebhook(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	hookID := c.Param("id")

	var hook models.Webhook
	if err := database.DB.Where("id = ? AND tenant_id = ?", hookID, tenantID).First(&hook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
		return
	}

	database.DB.Delete(&hook)
	c.JSON(http.StatusOK, gin.H{"message": "webhook deleted"})
}

// ── Helpers ─────────────────────────────────────────────────────────────────

type contactResp struct {
	ID          string `json:"id"`
	PhoneNumber string `json:"phone_number"`
	Name        string `json:"name"`
	PushName    string `json:"push_name"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

type msgResp struct {
	ID             string  `json:"id"`
	ConversationID string  `json:"conversation_id,omitempty"`
	Type           string  `json:"type"`
	Content        string  `json:"content"`
	Direction      string  `json:"direction"`
	Status         string  `json:"status,omitempty"`
	IsNote         bool    `json:"is_note,omitempty"`
	Timestamp      string  `json:"timestamp"`
}

type tagResp struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// splitEvents is a naive splitter for the events JSON array string.
// e.g. `"message.incoming","message.sent"` → ["message.incoming", "message.sent"]
func splitEvents(s string) []string {
	var out []string
	current := ""
	for _, ch := range s {
		if ch == ',' {
			out = append(out, trimQuotes(current))
			current = ""
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		out = append(out, trimQuotes(current))
	}
	return out
}

func trimQuotes(s string) string {
	s = strconvTrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func strconvTrimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
}
