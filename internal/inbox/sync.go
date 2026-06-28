package inbox

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"google.golang.org/protobuf/proto"
)

// postSyncBroadcast debounces TriggerSync calls after HistorySync batches.
// WhatsApp may send several HistorySync events in quick succession; we wait
// 4 seconds after the last one before broadcasting to the frontend.
var postSyncDebounce = struct {
	mu     sync.Mutex
	timers map[string]*time.Timer
	phones map[string]string // tenantID → sessionPhone
}{
	timers: make(map[string]*time.Timer),
	phones: make(map[string]string),
}

func schedulePostSyncBroadcast(tenantID uuid.UUID, sessionPhone string) {
	key := tenantID.String()
	postSyncDebounce.mu.Lock()
	defer postSyncDebounce.mu.Unlock()
	postSyncDebounce.phones[key] = sessionPhone
	if t, ok := postSyncDebounce.timers[key]; ok {
		t.Reset(4 * time.Second)
		return
	}
	postSyncDebounce.timers[key] = time.AfterFunc(4*time.Second, func() {
		postSyncDebounce.mu.Lock()
		delete(postSyncDebounce.timers, key)
		delete(postSyncDebounce.phones, key)
		postSyncDebounce.mu.Unlock()
		// Always sync all tenant conversations — avoids session_phone format mismatches.
		// Frontend filters by activeSessionPhone in the UI.
		TriggerSync(tenantID, "")
	})
}

// HandleHistorySync processes a HistorySync event from whatsmeow and upserts historical
// conversations and messages into the DB. Called automatically when a session connects.
// sessionPhone is the business WhatsApp number (our side) for this session.
func HandleHistorySync(sessionID, tenantID uuid.UUID, sessionPhone string, convs []*waHistorySync.Conversation) {
	total := len(convs)
	log.Printf("history sync: session=%s phone=%s total=%d", sessionID, sessionPhone, total)
	var countIndividual, countGroup, countBroadcast, countSkipped int
	defer func() {
		log.Printf("history sync done: individual=%d groups=%d broadcasts=%d skipped=%d", countIndividual, countGroup, countBroadcast, countSkipped)
	}()

	for _, wac := range convs {
		jid := wac.GetID()
		var phone string
		var chatType models.ChatType
		var groupName, groupJID string
		isLID := false

		switch {
		case strings.HasSuffix(jid, "@s.whatsapp.net"):
			phone = strings.TrimSuffix(jid, "@s.whatsapp.net")
			chatType = models.ChatTypeIndividual
		case strings.HasSuffix(jid, "@lid"):
			isLID = true
			phone = strings.TrimSuffix(jid, "@lid")
			chatType = models.ChatTypeIndividual
		case strings.HasSuffix(jid, "@g.us"):
			phone = strings.TrimSuffix(jid, "@g.us")
			chatType = models.ChatTypeGroup
			groupJID = jid
			groupName = wac.GetName()
			log.Printf("history sync: group jid=%s name=%q msgs=%d", jid, groupName, len(wac.GetMessages()))
		case strings.HasSuffix(jid, "@broadcast") && !strings.HasPrefix(jid, "status@"):
			phone = strings.TrimSuffix(jid, "@broadcast")
			chatType = models.ChatTypeBroadcast
			groupJID = jid
			groupName = wac.GetName()
			log.Printf("history sync: broadcast jid=%s name=%q msgs=%d", jid, groupName, len(wac.GetMessages()))
		default:
			countSkipped++
			continue // skip status@broadcast, newsletters, stories, etc.
		}

		// For LID contacts, try to resolve to real phone number immediately.
		// If we can't resolve it now, skip it — it'll be handled when a real
		// message arrives (manager.go already resolves LIDs on incoming messages).
		if isLID {
			if ResolveContactLID == nil {
				continue
			}
			realPhone := ResolveContactLID(sessionID, phone)
			if realPhone == "" {
				log.Printf("history sync: skipping unresolvable LID %s", phone)
				continue
			}
			phone = realPhone
		}

		// skip archived chats
		if wac.GetArchived() {
			continue
		}

		// For groups/broadcasts use the JID user-part as the contact phone number.
		// This keeps contacts unique per group/broadcast list.
		contactName := wac.GetName()
		if chatType == models.ChatTypeIndividual {
			contactName = wac.GetName()
		}

		contact := findOrCreateContact(tenantID, sessionID, phone, contactName)
		if contact == nil {
			continue
		}

		conv := findOrCreateConversation(tenantID, sessionID, contact.ID, sessionPhone, chatType, groupName, groupJID)
		if conv == nil {
			countSkipped++
			continue
		}
		switch chatType {
		case models.ChatTypeGroup:
			countGroup++
		case models.ChatTypeBroadcast:
			countBroadcast++
		default:
			countIndividual++
		}

		// Seed last_message_at from WhatsApp's own conversation timestamp.
		// This ensures correct sort order even when we have no stored messages.
		var lastMsgTime time.Time
		if ts := wac.GetConversationTimestamp(); ts > 0 {
			lastMsgTime = time.Unix(int64(ts), 0)
		}

		storedCount := 0
		for _, syncMsg := range wac.GetMessages() {
			webMsg := syncMsg.GetMessage()
			if webMsg == nil {
				continue
			}

			key := webMsg.GetKey()
			waMessageID := key.GetID()
			if waMessageID == "" {
				continue
			}

			// Track message timestamp regardless of whether it's a duplicate,
			// so last_message_at reflects the real last-message time.
			if msgTs := webMsg.GetMessageTimestamp(); msgTs > 0 {
				if t := time.Unix(int64(msgTs), 0); t.After(lastMsgTime) {
					lastMsgTime = t
				}
			}

			// skip duplicates
			var existing models.Message
			if database.DB.Where("wa_message_id = ?", waMessageID).First(&existing).Error == nil {
				continue
			}

			waMsg := webMsg.GetMessage()
			if waMsg == nil {
				continue
			}

			var content string
			var msgType models.MessageType
			var mediaPayload []byte
			var reactionToID string

			switch {
			case waMsg.GetConversation() != "":
				content = waMsg.GetConversation()
				msgType = models.MessageTypeText
			case waMsg.GetExtendedTextMessage() != nil:
				content = waMsg.GetExtendedTextMessage().GetText()
				msgType = models.MessageTypeText
			case waMsg.GetImageMessage() != nil:
				content = waMsg.GetImageMessage().GetCaption()
				msgType = models.MessageTypeImage
				mediaPayload, _ = proto.Marshal(waMsg.GetImageMessage())
			case waMsg.GetVideoMessage() != nil:
				content = waMsg.GetVideoMessage().GetCaption()
				msgType = models.MessageTypeVideo
				mediaPayload, _ = proto.Marshal(waMsg.GetVideoMessage())
			case waMsg.GetAudioMessage() != nil:
				msgType = models.MessageTypeAudio
				mediaPayload, _ = proto.Marshal(waMsg.GetAudioMessage())
			case waMsg.GetDocumentMessage() != nil:
				content = waMsg.GetDocumentMessage().GetFileName()
				msgType = models.MessageTypeDocument
				mediaPayload, _ = proto.Marshal(waMsg.GetDocumentMessage())
			case waMsg.GetStickerMessage() != nil:
				msgType = models.MessageTypeSticker
				mediaPayload, _ = proto.Marshal(waMsg.GetStickerMessage())
			case waMsg.GetReactionMessage() != nil:
				msgType = models.MessageTypeReaction
				content = waMsg.GetReactionMessage().GetText()
				reactionToID = waMsg.GetReactionMessage().GetKey().GetID()
			case waMsg.GetLocationMessage() != nil:
				loc := waMsg.GetLocationMessage()
				content = fmt.Sprintf(`{"lat":%f,"lng":%f,"name":"%s","address":"%s"}`,
					loc.GetDegreesLatitude(), loc.GetDegreesLongitude(),
					loc.GetName(), loc.GetAddress())
				msgType = models.MessageTypeLocation
			case waMsg.GetContactMessage() != nil:
				cm := waMsg.GetContactMessage()
				content = fmt.Sprintf(`{"name":"%s","vcard":"%s"}`,
					cm.GetDisplayName(), escapeJSON(cm.GetVcard()))
				msgType = models.MessageTypeContact
			default:
				msgType = models.MessageTypeText
			}

			direction := models.DirectionIncoming
			if key.GetFromMe() {
				direction = models.DirectionOutgoing
			}

			// update push name from history if available
			pushName := webMsg.GetPushName()
			if !key.GetFromMe() && pushName != "" && contact.PushName != pushName {
				database.DB.Model(contact).Update("push_name", pushName)
				contact.PushName = pushName
			}

			ts := time.Unix(int64(webMsg.GetMessageTimestamp()), 0)
			if ts.After(lastMsgTime) {
				lastMsgTime = ts
			}

			m := models.Message{
				ConversationID: conv.ID,
				TenantID:       tenantID,
				Type:           msgType,
				Content:        content,
				Direction:      direction,
				Status:         models.MessageStatusDelivered,
				WaMessageID:    waMessageID,
				IsNote:         false,
				Timestamp:      ts,
				MediaPayload:   mediaPayload,
				ReactionToID:   reactionToID,
			}
			if err := database.DB.Create(&m).Error; err != nil {
				log.Printf("history sync: store msg %s: %v", waMessageID, err)
				continue
			}
			storedCount++
		}

		// Always update last_message_at when we have a valid timestamp,
		// even if no new messages were inserted (all duplicates).
		if !lastMsgTime.IsZero() {
			database.DB.Exec("UPDATE conversations SET last_message_at = ? WHERE id = ?", lastMsgTime, conv.ID)
		}
		_ = storedCount
	}

	// After processing all conversations, resolve any contacts that were stored
	// with LID-format phone numbers before the LID-resolution fix was in place.
	go ResolveLIDContacts(sessionID, tenantID)

	// Schedule a broadcast after all HistorySync batches settle (debounced 4s).
	// This covers the Reset flow where WhatsApp re-sends full HistorySync.
	schedulePostSyncBroadcast(tenantID, sessionPhone)
}

// BootstrapFromWAStore populates conversations from whatsmeow's local device store.
// Used when the app DB is empty (e.g. after Reset) but the WA device is still registered.
// WhatsApp won't resend HistorySync for already-registered devices, so we read
// whatsmeow_chat_settings + whatsmeow_contacts directly to rebuild conversation stubs.
func BootstrapFromWAStore(sessionID, tenantID uuid.UUID, sessionPhone string) {
	ourJID := sessionPhone + "@s.whatsapp.net"
	log.Printf("bootstrap: reading whatsmeow store for %s", ourJID)

	type chatRow struct {
		ChatJID  string `gorm:"column:chat_jid"`
		Archived bool   `gorm:"column:archived"`
	}
	var chats []chatRow
	if err := database.DB.Raw(
		`SELECT chat_jid, archived FROM whatsmeow_chat_settings WHERE our_jid = ? AND archived = false`,
		ourJID,
	).Scan(&chats).Error; err != nil {
		log.Printf("bootstrap: read chat_settings failed: %v", err)
		return
	}
	log.Printf("bootstrap: %d non-archived chats in whatsmeow store", len(chats))

	type contactRow struct {
		FullName  string `gorm:"column:full_name"`
		PushName  string `gorm:"column:push_name"`
		FirstName string `gorm:"column:first_name"`
	}

	count := 0
	for _, chat := range chats {
		if !strings.HasSuffix(chat.ChatJID, "@s.whatsapp.net") {
			continue // skip groups, broadcasts, etc.
		}
		phone := strings.TrimSuffix(chat.ChatJID, "@s.whatsapp.net")
		if phone == "" || phone == sessionPhone {
			continue // skip self
		}

		var cr contactRow
		database.DB.Raw(
			`SELECT full_name, push_name, first_name FROM whatsmeow_contacts WHERE our_jid = ? AND their_jid = ?`,
			ourJID, chat.ChatJID,
		).Scan(&cr)

		name := cr.FullName
		if name == "" {
			name = cr.PushName
		}
		if name == "" {
			name = cr.FirstName
		}

		contact := findOrCreateContact(tenantID, sessionID, phone, name)
		if contact == nil {
			continue
		}
		findOrCreateConversation(tenantID, sessionID, contact.ID, sessionPhone, models.ChatTypeIndividual, "", "")
		count++
	}
	log.Printf("bootstrap: created/found %d conversations from WA store", count)
}

// TriggerSync broadcasts all conversations + messages to every connected client.
// Called after HistorySync (new QR connect) or manual resync.
// Delta sync handles the "client was offline briefly" case separately.
func TriggerSync(tenantID uuid.UUID, sessionPhone string) {
	log.Printf("TriggerSync: full broadcast for tenant=%s sessionPhone=%s", tenantID, sessionPhone)

	GlobalHub.Broadcast(tenantID.String(), WSEvent{Event: "sync_start", Data: nil})

	convs, err := getConversations(tenantID, 1, 1000, sessionPhone, "")
	if err != nil {
		log.Printf("sync error fetching convs: %v", err)
		return
	}
	GlobalHub.Broadcast(tenantID.String(), WSEvent{Event: "sync_chunk_convs", Data: convs})

	limit := 500
	page := 1
	for {
		offset := (page - 1) * limit
		var msgs []models.Message
		q := database.DB.Where("messages.tenant_id = ?", tenantID)
		if sessionPhone != "" {
			q = q.Joins("JOIN conversations ON conversations.id = messages.conversation_id").
				Where("conversations.session_phone = ?", sessionPhone).
				Select("messages.*")
		}
		if err := q.Order("messages.timestamp asc").Limit(limit).Offset(offset).Find(&msgs).Error; err != nil {
			break
		}
		if len(msgs) == 0 {
			break
		}
		out := make([]MessageResponse, len(msgs))
		for i, m := range msgs {
			out[i] = toMsgResponse(m)
		}
		GlobalHub.Broadcast(tenantID.String(), WSEvent{Event: "sync_chunk_msgs", Data: out})
		if len(msgs) < limit {
			break
		}
		page++
	}

	GlobalHub.Broadcast(tenantID.String(), WSEvent{Event: "sync_complete", Data: nil})
}

// escapeJSON escapes special characters for safe embedding in JSON string values.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}
