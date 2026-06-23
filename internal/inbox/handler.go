package inbox

import (
	"context"
	"net/http"
	"strconv"
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

	convs, err := getConversations(tenantID, page, limit, sessionPhone)
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
func handleWS(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	ws, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
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
		if _, _, err := ws.Read(ctx); err != nil {
			break
		}
	}
}
