package inbox

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/internal/webhooks"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func handleGetMedia(c *gin.Context) {
	msgIDStr := c.Param("id")
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid message id"})
		return
	}

	tenantID, exists := c.Get(middleware.CtxTenantID)
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var msg models.Message
	if err := database.DB.Where("id = ? AND tenant_id = ?", msgID, tenantID).First(&msg).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "message not found"})
		return
	}

	if len(msg.MediaPayload) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no media available"})
		return
	}

	var conv models.Conversation
	if err := database.DB.Where("id = ?", msg.ConversationID).First(&conv).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	client := session.Mgr.GetClient(conv.SessionID.String())
	// Fallback: session may have been recreated — look up by phone instead.
	if client == nil && conv.SessionPhone != "" {
		var waSession models.WhatsAppSession
		if err := database.DB.
			Where("tenant_id = ? AND phone = ? AND status = ?", tenantID, conv.SessionPhone, models.StatusConnected).
			First(&waSession).Error; err == nil {
			client = session.Mgr.GetClient(waSession.ID.String())
		}
	}
	if client == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "whatsapp session not connected"})
		return
	}

	var downloadable whatsmeow.DownloadableMessage
	var contentType string

	switch msg.Type {
	case models.MessageTypeImage:
		var img waE2E.ImageMessage
		if err := proto.Unmarshal(msg.MediaPayload, &img); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unmarshal image"})
			return
		}
		downloadable = &img
		contentType = img.GetMimetype()
	case models.MessageTypeVideo:
		var vid waE2E.VideoMessage
		if err := proto.Unmarshal(msg.MediaPayload, &vid); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unmarshal video"})
			return
		}
		downloadable = &vid
		contentType = vid.GetMimetype()
	case models.MessageTypeAudio:
		var aud waE2E.AudioMessage
		if err := proto.Unmarshal(msg.MediaPayload, &aud); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unmarshal audio"})
			return
		}
		downloadable = &aud
		contentType = aud.GetMimetype()
	case models.MessageTypeDocument:
		var doc waE2E.DocumentMessage
		if err := proto.Unmarshal(msg.MediaPayload, &doc); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unmarshal document"})
			return
		}
		downloadable = &doc
		contentType = doc.GetMimetype()
	case models.MessageTypeSticker:
		var stk waE2E.StickerMessage
		if err := proto.Unmarshal(msg.MediaPayload, &stk); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unmarshal sticker"})
			return
		}
		downloadable = &stk
		contentType = stk.GetMimetype()
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "not a media message"})
		return
	}

	data, err := client.Download(context.Background(), downloadable)
	if err != nil {
		// Media downloads commonly fail for historical messages whose media has
		// expired on WhatsApp's servers, or whose keys are no longer valid. This
		// is not a server fault — return 404 so the client degrades gracefully
		// (broken-image fallback) instead of flooding logs with 500s.
		log.Printf("media %s: download failed (likely expired): %v", msgID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "media no longer available"})
		return
	}

	c.Data(http.StatusOK, contentType, data)
}

// handleSendMedia accepts a multipart file upload and sends it via WhatsApp.
// Max 16 MB (WhatsApp limit for most media types).
func handleSendMedia(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := c.Request.ParseMultipartForm(16 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file too large (max 16 MB)"})
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing 'file' field"})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	// Detect MIME type from bytes; prefer the explicit Content-Type header
	mimeType := http.DetectContentType(data)
	if ct := header.Header.Get("Content-Type"); ct != "" && ct != "application/octet-stream" {
		mimeType = ct
	}
	// Go's DetectContentType returns "application/ogg" for OGG files;
	// normalise so the audio routing in SendMedia picks it up.
	if mimeType == "application/ogg" {
		mimeType = "audio/ogg; codecs=opus"
	}

	conv, err := getConversationByID(tenantID, convID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if conv.SessionPhone != "" {
		if limitErr := billing.CheckDailyMessageLimit(tenantID, conv.SessionPhone); limitErr != nil {
			var tenant models.Tenant
			database.DB.Select("created_at, daily_message_limit").Where("id = ?", tenantID).First(&tenant)
			daysSince := int(time.Since(tenant.CreatedAt).Hours() / 24)
			canContact := daysSince >= 7
			daysRemaining := 0
			if !canContact {
				daysRemaining = 7 - daysSince
			}
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":               limitErr.Error(),
				"code":                "daily_limit_reached",
				"limit":               tenant.DailyMessageLimit,
				"can_contact_support": canContact,
				"days_remaining":      daysRemaining,
			})
			return
		}
	}

	sessionIDStr := conv.SessionID
	if conv.SessionPhone != "" {
		var waSession models.WhatsAppSession
		if err := database.DB.
			Where("tenant_id = ? AND phone = ? AND status = ?", tenantID, conv.SessionPhone, models.StatusConnected).
			First(&waSession).Error; err == nil {
			sessionIDStr = waSession.ID.String()
		}
	}

	caption := c.Request.FormValue("caption")
	waID, msgType, mediaPayload, err := session.Mgr.SendMedia(sessionIDStr, conv.Contact.PhoneNumber, data, mimeType, header.Filename, caption)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "whatsapp send failed: " + err.Error()})
		return
	}

	now := time.Now()
	msg := models.Message{
		ConversationID: convID,
		TenantID:       tenantID,
		SenderID:       &userID,
		Type:           msgType,
		Content:        header.Filename,
		Direction:      models.DirectionOutgoing,
		Status:         models.MessageStatusSent,
		WaMessageID:    waID,
		MediaPayload:   mediaPayload,
		Timestamp:      now,
	}
	if msgType == models.MessageTypeAudio {
		msg.Content = "Voice Message"
	} else if caption != "" {
		msg.Content = caption
	}
	if err := database.DB.Create(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save message"})
		return
	}
	database.DB.Exec("UPDATE conversations SET updated_at = NOW(), last_message_at = ? WHERE id = ?", now, convID)

	if conv.SessionPhone != "" {
		billing.IncrementDailyCount(tenantID, conv.SessionPhone)
	}

	resp := toMsgResponse(msg)
	var fullConv models.Conversation
	database.DB.Preload("Contact").First(&fullConv, "id = ?", convID)
	fullConv.LastMessageAt = &now

	GlobalHub.Broadcast(tenantID.String(), WSEvent{
		Event: "new_message",
		Data: map[string]interface{}{
			"conversation": toConvResponse(fullConv, &msg),
			"message":      resp,
		},
	})
	go webhooks.Dispatch(tenantID, "message.sent", map[string]interface{}{
		"conversation_id": convID.String(),
		"contact_phone":   conv.Contact.PhoneNumber,
		"type":            string(msgType),
		"filename":        header.Filename,
	})

	c.JSON(http.StatusCreated, resp)
}
