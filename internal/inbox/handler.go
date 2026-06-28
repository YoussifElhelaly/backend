package inbox

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/internal/webhooks"
	"whatify/backend/pkg/database"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)


func RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("", middleware.Auth())
	{
		g.GET("/conversations", listConversations)
		// static sub-routes must come before /:id to avoid param capture
		g.POST("/conversations/sync", handleSync)
		g.POST("/conversations/resync", handleResync)
		g.POST("/conversations/reset", handleReset)
		g.GET("/conversations/:id", getConversation)
		g.GET("/conversations/:id/messages", listMessages)
		g.POST("/conversations/:id/messages", handleSend)
		g.POST("/conversations/:id/load-older", handleLoadOlder)
		g.POST("/conversations/:id/media", handleSendMedia)
		g.POST("/conversations/:id/notes", handleNote)
		g.PUT("/conversations/:id/assign", handleAssign)
		g.PUT("/conversations/:id/status", handleStatus)
		g.POST("/conversations/:id/read", handleRead)
		g.GET("/messages/:id/media", handleGetMedia)
		// WS accepts ?token= (same middleware handles it)
		g.GET("/ws", handleWS)
	}
}

func listConversations(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	sessionPhone := c.Query("session_phone")
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 30
	}

	chatType := c.Query("chat_type")
	convs, err := getConversations(tenantID, page, limit, sessionPhone, chatType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, convs)
}

func handleSync(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	sessionPhone := c.Query("session_phone")
	go TriggerSync(tenantID, sessionPhone)
	c.JSON(http.StatusOK, gin.H{"message": "sync started"})
}

// handleResync reconnects WhatsApp sessions so missed messages are delivered,
// then pushes everything from DB to the frontend via WebSocket.
// Two paths after reconnect:
//   - Missed messages via HistorySync → handled by schedulePostSyncBroadcast (auto TriggerSync)
//   - Missed messages via events.Message → handled by HandleIncoming (auto WS broadcast)
//
// A final TriggerSync after 12s catches anything that slipped through.
func handleResync(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	sessionPhone := c.Query("session_phone")

	var sessions []models.WhatsAppSession
	q := database.DB.Where("tenant_id = ? AND phone != '' AND status IN ?", tenantID,
		[]string{string(models.StatusConnected), string(models.StatusDisconnected)})
	if sessionPhone != "" {
		q = q.Where("phone = ?", sessionPhone)
	}
	q.Find(&sessions)

	if len(sessions) == 0 {
		// No session to reconnect — just push current DB state to frontend.
		go TriggerSync(tenantID, "")
		c.JSON(http.StatusOK, gin.H{"message": "sync started"})
		return
	}

	GlobalHub.Broadcast(tenantID.String(), WSEvent{Event: "sync_start", Data: nil})

	for _, s := range sessions {
		sid := s.ID
		phone := s.Phone
		go func() {
			session.Mgr.Disconnect(sid.String())
			session.Mgr.Reconnect(sid, phone)
			// HistorySync path: schedulePostSyncBroadcast fires TriggerSync after 4s of silence.
			// events.Message path: HandleIncoming broadcasts each message via WS directly.
			// Final safety net: TriggerSync after 12s to catch everything.
			time.Sleep(12 * time.Second)

			// WhatsApp only sends HistorySync on first QR registration, not on reconnects.
			// If the DB is still empty after reconnect, bootstrap conversation stubs from the
			// whatsmeow local device store (whatsmeow_chat_settings + whatsmeow_contacts).
			var convCount int64
			database.DB.Model(&models.Conversation{}).
				Where("tenant_id = ? AND session_phone = ?", tenantID, phone).
				Count(&convCount)
			if convCount == 0 {
				log.Printf("resync: no conversations after 12s for phone=%s, bootstrapping from WA store", phone)
				BootstrapFromWAStore(sid, tenantID, phone)
			}

			TriggerSync(tenantID, "")
		}()
	}

	c.JSON(http.StatusOK, gin.H{"message": "resync started"})
}

// handleReset deletes conversations, messages, and contacts,
// then reconnects WhatsApp sessions so WhatsApp re-sends HistorySync with fresh data.
func handleReset(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	sessionPhone := c.Query("session_phone")

	// Delete in FK order: messages → conversations → contacts
	if sessionPhone != "" {
		database.DB.Exec(`DELETE FROM messages WHERE conversation_id IN (SELECT id FROM conversations WHERE tenant_id = ? AND session_phone = ?)`, tenantID, sessionPhone)
		database.DB.Where("tenant_id = ? AND session_phone = ?", tenantID, sessionPhone).Delete(&models.Conversation{})
		// For contacts, we can leave them or try to clean up orphaned ones later, but let's wipe contacts associated if there's a session_id link. We'll skip contact wipe per-session for safety unless specified, or we can just wipe the conversations which triggers re-fetch.
		// Let's wipe contacts where possible if they have session_id. For now, it's safer to just wipe conversations and messages so HistorySync re-adds them.
	} else {
		database.DB.Where("tenant_id = ?", tenantID).Delete(&models.Message{})
		database.DB.Where("tenant_id = ?", tenantID).Delete(&models.Conversation{})
		database.DB.Where("tenant_id = ?", tenantID).Delete(&models.Contact{})
	}

	// Reconnect active sessions to trigger WhatsApp HistorySync
	var sessions []models.WhatsAppSession
	q := database.DB.Where("tenant_id = ? AND status = ?", tenantID, models.StatusConnected)
	if sessionPhone != "" {
		q = q.Where("phone = ?", sessionPhone)
	}
	q.Find(&sessions)

	for _, s := range sessions {
		sid := s.ID
		phone := s.Phone
		go func() {
			session.Mgr.Disconnect(sid.String())
			session.Mgr.Reconnect(sid, phone)
		}()
	}

	c.JSON(http.StatusOK, gin.H{"message": "reset complete, resyncing from WhatsApp"})
}

func getConversation(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	conv, err := getConversationByID(tenantID, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, conv)
}

func listMessages(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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

	msgs, err := getMessages(tenantID, id, page, limit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, msgs)
}

func handleSend(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// look up conversation to get phone + session
	conv, err := getConversationByID(tenantID, convID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Check daily outbound message limit before hitting WhatsApp
	if conv.SessionPhone != "" {
		if limitErr := billing.CheckDailyMessageLimit(tenantID, conv.SessionPhone); limitErr != nil {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": limitErr.Error(), "code": "daily_limit_exceeded"})
			return
		}
	}

	// Resolve the current active session ID by session_phone so sends work
	// even after a session was deleted and recreated with the same number.
	sessionIDStr := conv.SessionID
	if conv.SessionPhone != "" {
		var waSession models.WhatsAppSession
		if err := database.DB.
			Where("tenant_id = ? AND phone = ? AND status = ?", tenantID, conv.SessionPhone, models.StatusConnected).
			First(&waSession).Error; err == nil {
			sessionIDStr = waSession.ID.String()
		}
	}

	// send via whatsmeow
	waID, waErr := session.Mgr.SendText(sessionIDStr, conv.Contact.PhoneNumber, req.Content)
	if waErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "whatsapp send failed: " + waErr.Error()})
		return
	}

	msg, err := sendMessage(tenantID, convID, req.Content, userID, waID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Increment daily count after a confirmed successful send
	if conv.SessionPhone != "" {
		billing.IncrementDailyCount(tenantID, conv.SessionPhone)
	}

	activity.Log(tenantID, &userID, "message.sent", "conversation", convID.String(), map[string]string{
		"contact": conv.Contact.PhoneNumber,
	})
	go webhooks.Dispatch(tenantID, "message.sent", map[string]interface{}{
		"conversation_id": convID.String(),
		"contact_phone":   conv.Contact.PhoneNumber,
		"content":         req.Content,
	})
	c.JSON(http.StatusCreated, msg)
}

// handleLoadOlder triggers an on-demand WhatsApp history sync for one conversation,
// fetching messages older than the oldest one we currently have. The fetched messages
// arrive asynchronously via the HistorySync (ON_DEMAND) handler and are broadcast over
// WS, so the client just refreshes after a few seconds. Responds 202 immediately.
func handleLoadOlder(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	conv, err := getConversationByID(tenantID, convID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Anchor at the oldest message that carries a real WhatsApp message ID.
	var oldest models.Message
	if err := database.DB.
		Where("conversation_id = ? AND wa_message_id != '' AND is_note = ?", convID, false).
		Order("timestamp asc").First(&oldest).Error; err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "no anchor message to load older history from"})
		return
	}

	// Resolve the currently-active session by phone (same as handleSend).
	sessionIDStr := conv.SessionID
	if conv.SessionPhone != "" {
		var waSession models.WhatsAppSession
		if err := database.DB.
			Where("tenant_id = ? AND phone = ? AND status = ?", tenantID, conv.SessionPhone, models.StatusConnected).
			First(&waSession).Error; err == nil {
			sessionIDStr = waSession.ID.String()
		}
	}

	// Build the chat JID: group/broadcast chats use their stored JID, individual
	// chats use "<phone>@s.whatsapp.net".
	chatJID := conv.Contact.PhoneNumber + "@s.whatsapp.net"
	if (conv.ChatType == string(models.ChatTypeGroup) || conv.ChatType == string(models.ChatTypeBroadcast)) && conv.GroupJID != "" {
		chatJID = conv.GroupJID
	}

	fromMe := oldest.Direction == models.DirectionOutgoing
	if err := session.Mgr.RequestOlderHistory(sessionIDStr, chatJID, oldest.WaMessageID, fromMe, oldest.Timestamp, 50); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "whatsapp request failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":   "requested older history from WhatsApp",
		"anchor_ts": oldest.Timestamp,
	})
}

func handleNote(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req NoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	note, err := createNote(tenantID, convID, req.Content, userID)
	if err == nil {
		activity.Log(tenantID, &userID, "note.created", "conversation", convID.String(), nil)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, note)
}

func handleAssign(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req AssignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := assignConversation(tenantID, convID, req.AgentID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	meta := map[string]string{"conversation_id": convID.String()}
	if req.AgentID != nil {
		meta["agent_id"] = req.AgentID.String()
	}
	activity.Log(tenantID, &userID, "conversation.assigned", "conversation", convID.String(), meta)
	go webhooks.Dispatch(tenantID, "conversation.assigned", meta)
	c.JSON(http.StatusOK, gin.H{"message": "assigned"})
}

func handleStatus(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req StatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := updateConvStatus(tenantID, convID, models.ConversationStatus(req.Status)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	action := "conversation.resolved"
	if req.Status == "OPEN" {
		action = "conversation.reopened"
	}
	activity.Log(tenantID, &userID, action, "conversation", convID.String(), nil)
	go webhooks.Dispatch(tenantID, action, map[string]string{"conversation_id": convID.String()})
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func handleRead(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := markRead(tenantID, convID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "marked read"})
}

// handleWS upgrades the connection to WebSocket and registers it with the hub.
// Auth is via ?token= query param (middleware already handles this).
// Clients can send { "event": "request_delta", "last_synced_at": "<RFC3339>" }
// and the server will stream only the messages/conversations newer than that timestamp.
func handleWS(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	// Build allowed origins list from FRONTEND_URL and ALLOWED_ORIGINS env vars.
	var allowedOrigins []string
	if fe := os.Getenv("FRONTEND_URL"); fe != "" {
		allowedOrigins = append(allowedOrigins, fe)
	}
	if ao := os.Getenv("ALLOWED_ORIGINS"); ao != "" {
		for _, o := range strings.Split(ao, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				allowedOrigins = append(allowedOrigins, o)
			}
		}
	}

	acceptOpts := &websocket.AcceptOptions{}
	if len(allowedOrigins) > 0 {
		acceptOpts.OriginPatterns = allowedOrigins
	} else if os.Getenv("GIN_MODE") == "release" {
		// In production, refuse to accept WebSocket connections when no origins are configured.
		c.JSON(http.StatusForbidden, gin.H{"error": "ALLOWED_ORIGINS must be set for WebSocket in production"})
		return
	} else {
		// Dev fallback: allow all origins only in non-release mode.
		acceptOpts.InsecureSkipVerify = true
	}

	ws, err := websocket.Accept(c.Writer, c.Request, acceptOpts)
	if err != nil {
		slog.Error("websocket accept failed", "error", err)
		return
	}

	conn := &wsConn{
		ws:       ws,
		tenantID: tenantID.String(),
	}
	GlobalHub.register(conn)
	defer func() {
		GlobalHub.unregister(conn)
		ws.Close(websocket.StatusNormalClosure, "")
	}()

	ctx := context.Background()
	for {
		_, msg, err := ws.Read(ctx)
		if err != nil {
			break
		}

		var req struct {
			Event        string `json:"event"`
			LastSyncedAt string `json:"last_synced_at"`
		}
		if jsonErr := json.Unmarshal(msg, &req); jsonErr != nil {
			continue
		}
		if req.Event != "request_delta" || req.LastSyncedAt == "" {
			continue
		}

		since, parseErr := time.Parse(time.RFC3339, req.LastSyncedAt)
		if parseErr != nil {
			continue
		}

		slog.Info("delta sync requested", "tenant_id", tenantID, "since", since.Format(time.RFC3339))
		go streamDelta(conn, tenantID, since)
	}
}

// streamDelta sends new conversations + messages since `since` to one specific connection.
func streamDelta(conn *wsConn, tenantID uuid.UUID, since time.Time) {
	const chunkSize = 200
	page := 1
	totalMsgs := 0

	GlobalHub.SendTo(conn, WSEvent{Event: "delta_start", Data: nil})

	for {
		convs, msgs, err := getDelta(tenantID, since, page, chunkSize)
		if err != nil {
			log.Printf("delta sync error: %v", err)
			break
		}

		if len(msgs) == 0 && page == 1 {
			// Nothing new — still send delta_complete so the client updates its cursor
			break
		}

		GlobalHub.SendTo(conn, WSEvent{
			Event: "delta_chunk",
			Data: map[string]interface{}{
				"conversations": convs,
				"messages":      msgs,
				"page":          page,
			},
		})

		totalMsgs += len(msgs)
		if len(msgs) < chunkSize {
			break
		}
		page++
	}

	log.Printf("delta sync done: tenant=%s messages=%d pages=%d", tenantID, totalMsgs, page)
	GlobalHub.SendTo(conn, WSEvent{Event: "delta_complete", Data: map[string]interface{}{
		"synced_at": time.Now().UTC().Format(time.RFC3339),
		"count":     totalMsgs,
	}})
}
