package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"
	"whatify/backend/pkg/proxy"
	"whatify/backend/pkg/whatsapp"

	"github.com/google/uuid"
	waProto "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

type QRUpdate struct {
	Code  string
	Event string // "code" | "success" | "timeout" | "error"
}

// MessageHandler is called for each WhatsApp message (incoming or sent from device).
// isFromMe=true means the message was sent by the session owner from any device (mobile, web, etc.)
type MessageHandler func(
	sessionID, tenantID uuid.UUID,
	sessionPhone, phone, pushName, waMessageID, content string,
	msgType models.MessageType,
	timestamp time.Time,
	mediaPayload []byte,
	reactionToID string,
	isFromMe bool,
	chatType models.ChatType,
	groupName, groupJID string,
)

// HistorySyncHandler is called when whatsmeow fires a HistorySync event on connect.
// sessionPhone is the business WhatsApp number for this session.
type HistorySyncHandler func(sessionID, tenantID uuid.UUID, sessionPhone string, convs []*waHistorySync.Conversation)

type Manager struct {
	mu                  sync.RWMutex
	clients             map[string]*waProto.Client
	qrSubs              map[string][]chan QRUpdate
	messageHandler      MessageHandler
	historySyncHandler  HistorySyncHandler
}

var Mgr = &Manager{
	clients: make(map[string]*waProto.Client),
	qrSubs:  make(map[string][]chan QRUpdate),
}

// SetMessageHandler registers the callback for incoming messages.
func (m *Manager) SetMessageHandler(h MessageHandler) {
	m.mu.Lock()
	m.messageHandler = h
	m.mu.Unlock()
}

// SetHistorySyncHandler registers the callback for HistorySync events.
func (m *Manager) SetHistorySyncHandler(h HistorySyncHandler) {
	m.mu.Lock()
	m.historySyncHandler = h
	m.mu.Unlock()
}

// Connect starts a new whatsmeow client for the given session and begins QR flow.
func (m *Manager) Connect(sessionID uuid.UUID) error {
	id := sessionID.String()

	var sess models.WhatsAppSession
	database.DB.First(&sess, "id = ?", sessionID)

	device := whatsapp.Container.NewDevice()
	client := waProto.NewClient(device, waLog.Noop)

	// Auto-assign a proxy from the pool if this session has none configured.
	if sess.ProxyURL == "" {
		if p := proxy.Next(); p != "" {
			sess.ProxyURL = p
			database.DB.Model(&sess).Update("proxy_url", p)
			log.Printf("connect %s: auto-assigned proxy %q from pool", id, p)
		}
	}
	if sess.ProxyURL != "" {
		if err := client.SetProxyAddress(sess.ProxyURL); err != nil {
			log.Printf("connect %s: set proxy %q: %v", id, sess.ProxyURL, err)
		}
	}

	m.mu.Lock()
	m.clients[id] = client
	m.mu.Unlock()

	client.AddEventHandler(func(evt interface{}) {
		m.handleEvent(id, evt)
	})

	qrChan, err := client.GetQRChannel(context.Background())
	if err != nil {
		return fmt.Errorf("get qr channel: %w", err)
	}

	go func() {
		if err := client.Connect(); err != nil {
			m.broadcast(id, QRUpdate{Event: "error"})
		}
	}()

	go func() {
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				m.broadcast(id, QRUpdate{Code: evt.Code, Event: "code"})
			case "success":
				// Duplicate-number guard: a WhatsApp number may only be registered on ONE
				// session across the whole platform. If this freshly-paired number already
				// belongs to another session (even in a different tenant), reject the pairing
				// instead of letting two sessions fight over the same session_phone.
				phone := ""
				if client.Store.ID != nil {
					phone = client.Store.ID.User
				}
				if phone != "" {
					var existing models.WhatsAppSession
					if err := database.DB.Where("phone = ? AND id <> ?", phone, sessionID).First(&existing).Error; err == nil {
						log.Printf("connect %s: phone %s already registered on session %s — rejecting pairing", id, phone, existing.ID)
						// Unpair this device cleanly so it leaves no lingering whatsmeow device row.
						_ = client.Logout(context.Background())
						m.Disconnect(id)
						m.broadcast(id, QRUpdate{Event: "duplicate"})
						m.updateStatus(sessionID, models.StatusDisconnected)
						continue
					}
				}
				m.broadcast(id, QRUpdate{Event: "success"})
				m.updateStatus(sessionID, models.StatusConnected)
			case "timeout":
				m.broadcast(id, QRUpdate{Event: "timeout"})
				m.updateStatus(sessionID, models.StatusDisconnected)
			}
		}
	}()

	m.updateStatus(sessionID, models.StatusConnecting)
	return nil
}

// Reconnect connects an already-authenticated session without showing a QR code.
func (m *Manager) Reconnect(sessionID uuid.UUID, phone string) {
	id := sessionID.String()

	devices, err := whatsapp.Container.GetAllDevices(context.Background())
	if err != nil {
		log.Printf("reconnect %s: get devices: %v", id, err)
		return
	}

	var device *waStore.Device
	for _, d := range devices {
		if d.ID != nil && d.ID.User == phone {
			device = d
			break
		}
	}
	if device == nil {
		log.Printf("reconnect %s: no stored device for phone %s", id, phone)
		return
	}

	client := waProto.NewClient(device, waLog.Noop)

	var sess models.WhatsAppSession
	if err := database.DB.First(&sess, "id = ?", sessionID).Error; err == nil && sess.ProxyURL != "" {
		if err := client.SetProxyAddress(sess.ProxyURL); err != nil {
			log.Printf("reconnect %s: set proxy %q: %v", id, sess.ProxyURL, err)
		}
	}

	m.mu.Lock()
	m.clients[id] = client
	m.mu.Unlock()

	client.AddEventHandler(func(evt interface{}) {
		m.handleEvent(id, evt)
	})

	go func() {
		if err := client.Connect(); err != nil {
			log.Printf("reconnect %s: connect error: %v", id, err)
			m.updateStatus(sessionID, models.StatusDisconnected)
		}
	}()
}

// ReconnectAll reconnects all sessions that were previously connected or disconnected unexpectedly.
func (m *Manager) ReconnectAll() {
	var sessions []models.WhatsAppSession
	database.DB.Where("status IN ? AND phone != ''", []string{string(models.StatusConnected), string(models.StatusDisconnected)}).Find(&sessions)
	for _, s := range sessions {
		go m.Reconnect(s.ID, s.Phone)
	}
}

// Disconnect closes the whatsmeow client for the given session.
func (m *Manager) Disconnect(sessionID string) {
	m.mu.Lock()
	client, ok := m.clients[sessionID]
	delete(m.clients, sessionID)
	m.mu.Unlock()

	if ok && client != nil {
		client.Disconnect()
	}
}

// GetClient returns the active whatsmeow client for a session, or nil.
func (m *Manager) GetClient(sessionID string) *waProto.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[sessionID]
}

// ResolveContactLID tries to resolve a LID (WhatsApp opaque ID) to a real phone number.
// Returns the real phone number, or empty string if the input was not a known LID.
func (m *Manager) ResolveContactLID(sessionID uuid.UUID, phone string) string {
	client := m.GetClient(sessionID.String())
	if client == nil || client.Store.LIDs == nil {
		return ""
	}
	lidJID := types.NewJID(phone, types.HiddenUserServer)
	pnJID, err := client.Store.LIDs.GetPNForLID(context.Background(), lidJID)
	if err != nil || pnJID.IsEmpty() {
		return ""
	}
	return pnJID.User
}

// GetAvatarURL fetches the profile picture URL and ID for a contact phone number.
// Returns empty strings if the session is not connected or the picture is unavailable.
func (m *Manager) GetAvatarURL(sessionID uuid.UUID, phone string) (string, string) {
	client := m.GetClient(sessionID.String())
	if client == nil {
		return "", ""
	}
	jid := types.NewJID(phone, types.DefaultUserServer)
	info, err := client.GetProfilePictureInfo(context.Background(), jid, &waProto.GetProfilePictureParams{})
	if err != nil || info == nil {
		return "", ""
	}
	return info.URL, info.ID
}

// GetContactInfo fetches the FullName and PushName from the local whatsmeow store.
func (m *Manager) GetContactInfo(sessionID uuid.UUID, phone string) (string, string) {
	client := m.GetClient(sessionID.String())
	if client == nil {
		return "", ""
	}
	jid := types.NewJID(phone, types.DefaultUserServer)
	info, err := client.Store.Contacts.GetContact(context.Background(), jid)
	if err == nil && info.Found {
		return info.FullName, info.PushName
	}
	return "", ""
}

// SendText sends a plain-text WhatsApp message and returns the WA message ID.
// If the requested session is not connected, it falls back to any available connected client.
func (m *Manager) SendText(sessionID, phone, text string) (string, error) {
	client := m.GetClient(sessionID)
	if client == nil {
		// Fallback: use any connected client
		m.mu.RLock()
		for _, c := range m.clients {
			if c != nil && c.IsConnected() {
				client = c
				break
			}
		}
		m.mu.RUnlock()
	}
	if client == nil {
		return "", fmt.Errorf("session not connected")
	}
	jid := types.NewJID(phone, types.DefaultUserServer)
	resp, err := client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: proto.String(text),
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// RequestOlderHistory asks WhatsApp to resend older messages for a single chat,
// anchored at the oldest message we currently have (on-demand history sync).
// chatJID is the full chat JID — "<phone>@s.whatsapp.net" for individuals or
// "<id>@g.us" for groups. The response arrives asynchronously as an
// *events.HistorySync of type ON_DEMAND and is ingested by the normal HistorySync
// handler. Use this to fetch messages older than the initial history-sync window.
func (m *Manager) RequestOlderHistory(sessionID, chatJID, oldestMsgID string, oldestFromMe bool, oldestTS time.Time, count int) error {
	client := m.GetClient(sessionID)
	if client == nil {
		// Fallback: use any connected client (same as SendText)
		m.mu.RLock()
		for _, c := range m.clients {
			if c != nil && c.IsConnected() {
				client = c
				break
			}
		}
		m.mu.RUnlock()
	}
	if client == nil {
		return fmt.Errorf("session not connected")
	}

	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid chat jid %q: %w", chatJID, err)
	}

	info := &types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     jid,
			IsFromMe: oldestFromMe,
			IsGroup:  jid.Server == types.GroupServer,
		},
		ID:        oldestMsgID,
		Timestamp: oldestTS,
	}
	req := client.BuildHistorySyncRequest(info, count)
	_, err = client.SendPeerMessage(context.Background(), req)
	return err
}

// oggOpusDuration extracts the duration in seconds from OGG Opus data by reading
// the granule position from the last OGG page header. For Opus the granule position
// is the total number of samples at 48 kHz.
func oggOpusDuration(data []byte) uint32 {
	const opusSampleRate = 48000
	var lastGranule int64
	offset := 0
	for offset+27 <= len(data) {
		if data[offset] != 'O' || data[offset+1] != 'g' || data[offset+2] != 'g' || data[offset+3] != 'S' {
			break
		}
		numSegments := int(data[offset+26])
		headerSize := 27 + numSegments
		if offset+headerSize > len(data) {
			break
		}
		lastGranule = int64(binary.LittleEndian.Uint64(data[offset+6 : offset+14]))
		segmentSize := 0
		for i := 0; i < numSegments; i++ {
			segmentSize += int(data[offset+27+i])
		}
		offset += headerSize + segmentSize
	}
	if lastGranule > 0 {
		return uint32(lastGranule / opusSampleRate)
	}
	return 0
}

// convertAudioToOggOpus converts any audio format to OGG Opus using ffmpeg.
// Returns the converted bytes and true on success; original bytes and false if
// ffmpeg is not installed or the conversion fails (caller sends as-is).
func convertAudioToOggOpus(data []byte) ([]byte, bool) {
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-vn",
		"-c:a", "libopus",
		"-b:a", "64k",
		"-ar", "48000",
		"-ac", "1",
		"-f", "ogg",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return data, false
	}
	return out, true
}

// normalizeAudioMime normalizes OGG MIME types so WhatsApp recognises them.
// Browsers send "audio/ogg;codecs=opus" (no space) but WhatsApp expects
// "audio/ogg; codecs=opus" (with space) or just "audio/ogg".
func normalizeAudioMime(mime string) string {
	lower := strings.ToLower(mime)
	if strings.HasPrefix(lower, "audio/ogg") || lower == "application/ogg" {
		if strings.Contains(lower, "opus") {
			return "audio/ogg; codecs=opus"
		}
		return "audio/ogg"
	}
	return mime
}

// SendMedia uploads media to WhatsApp and sends it to the given phone number.
// caption is an optional text shown under image/video/document media.
// Returns the WA message ID, the detected message type, and the marshaled protobuf payload.
func (m *Manager) SendMedia(sessionID, phone string, data []byte, mimeType, filename, caption string) (string, models.MessageType, []byte, error) {
	client := m.GetClient(sessionID)
	if client == nil {
		// Fallback: use any connected client (same as SendText)
		m.mu.RLock()
		for _, c := range m.clients {
			if c != nil && c.IsConnected() {
				client = c
				break
			}
		}
		m.mu.RUnlock()
	}
	if client == nil {
		return "", "", nil, fmt.Errorf("session not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	jid := types.NewJID(phone, types.DefaultUserServer)

	var waMsg *waE2E.Message
	var msgType models.MessageType
	var payload []byte

	switch {
	case strings.HasPrefix(mimeType, "image/"):
		resp, err := client.Upload(ctx, data, waProto.MediaImage)
		if err != nil {
			return "", "", nil, fmt.Errorf("upload failed: %w", err)
		}
		img := &waE2E.ImageMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(resp.FileLength),
			Mimetype:      proto.String(mimeType),
		}
		if caption != "" {
			img.Caption = proto.String(caption)
		}
		waMsg = &waE2E.Message{ImageMessage: img}
		msgType = models.MessageTypeImage
		payload, _ = proto.Marshal(img)

	case strings.HasPrefix(mimeType, "video/"):
		resp, err := client.Upload(ctx, data, waProto.MediaVideo)
		if err != nil {
			return "", "", nil, fmt.Errorf("upload failed: %w", err)
		}
		vid := &waE2E.VideoMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(resp.FileLength),
			Mimetype:      proto.String(mimeType),
		}
		if caption != "" {
			vid.Caption = proto.String(caption)
		}
		waMsg = &waE2E.Message{VideoMessage: vid}
		msgType = models.MessageTypeVideo
		payload, _ = proto.Marshal(vid)

	case strings.HasPrefix(mimeType, "audio/"), mimeType == "application/ogg":
		audioData := data
		waMime := normalizeAudioMime(mimeType)
		// WhatsApp PTT voice notes must be OGG Opus. Chrome records WebM Opus
		// (same codec, different container) — convert via ffmpeg when available.
		if !strings.HasPrefix(strings.ToLower(waMime), "audio/ogg") {
			if converted, ok := convertAudioToOggOpus(data); ok {
				audioData = converted
				waMime = "audio/ogg; codecs=opus"
				log.Printf("SendMedia: converted %s → OGG Opus (%d→%d bytes)", mimeType, len(data), len(converted))
			} else {
				log.Printf("SendMedia: ffmpeg unavailable, sending %s as-is (PTT may not deliver on some clients)", mimeType)
			}
		}
		resp, err := client.Upload(ctx, audioData, waProto.MediaAudio)
		if err != nil {
			return "", "", nil, fmt.Errorf("upload failed: %w", err)
		}
		aud := &waE2E.AudioMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(resp.FileLength),
			Mimetype:      proto.String(waMime),
			PTT:           proto.Bool(true),
			Seconds:       proto.Uint32(oggOpusDuration(audioData)),
		}
		waMsg = &waE2E.Message{AudioMessage: aud}
		msgType = models.MessageTypeAudio
		payload, _ = proto.Marshal(aud)

	default:
		resp, err := client.Upload(ctx, data, waProto.MediaDocument)
		if err != nil {
			return "", "", nil, fmt.Errorf("upload failed: %w", err)
		}
		if filename == "" {
			filename = "file"
		}
		doc := &waE2E.DocumentMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(resp.FileLength),
			Mimetype:      proto.String(mimeType),
			FileName:      proto.String(filename),
		}
		if caption != "" {
			doc.Caption = proto.String(caption)
		}
		waMsg = &waE2E.Message{DocumentMessage: doc}
		msgType = models.MessageTypeDocument
		payload, _ = proto.Marshal(doc)
	}

	sendResp, err := client.SendMessage(ctx, jid, waMsg)
	if err != nil {
		return "", "", nil, err
	}
	return sendResp.ID, msgType, payload, nil
}

// SubscribeQR registers a channel to receive QR updates for a session.
func (m *Manager) SubscribeQR(sessionID string) chan QRUpdate {
	ch := make(chan QRUpdate, 10)
	m.mu.Lock()
	m.qrSubs[sessionID] = append(m.qrSubs[sessionID], ch)
	m.mu.Unlock()
	return ch
}

// UnsubscribeQR removes a QR subscriber channel.
func (m *Manager) UnsubscribeQR(sessionID string, ch chan QRUpdate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	subs := m.qrSubs[sessionID]
	for i, s := range subs {
		if s == ch {
			m.qrSubs[sessionID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	close(ch)
}

func (m *Manager) broadcast(sessionID string, update QRUpdate) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ch := range m.qrSubs[sessionID] {
		select {
		case ch <- update:
		default:
		}
	}
}

func (m *Manager) handleEvent(sessionID string, evt interface{}) {
	id, err := uuid.Parse(sessionID)
	if err != nil {
		return
	}
	switch e := evt.(type) {
	case *events.Connected:
		_ = e
		m.updateStatus(id, models.StatusConnected)

	case *events.Disconnected:
		m.updateStatus(id, models.StatusDisconnected)

	case *events.LoggedOut:
		m.updateStatus(id, models.StatusDisconnected)

	case *events.Picture:
		phone := e.JID.User
		if e.Remove {
			database.DB.Model(&models.Contact{}).
				Where("session_id = ? AND phone_number = ?", id, phone).
				Updates(map[string]interface{}{"avatar_url": "", "avatar_id": ""})
			return
		}
		go func() {
			m.mu.RLock()
			client, ok := m.clients[id.String()]
			m.mu.RUnlock()
			if !ok || client == nil {
				return
			}
			info, err := client.GetProfilePictureInfo(context.Background(), e.JID, &waProto.GetProfilePictureParams{})
			if err != nil || info == nil {
				return
			}
			database.DB.Model(&models.Contact{}).
				Where("session_id = ? AND phone_number = ?", id, phone).
				Updates(map[string]interface{}{"avatar_url": info.URL, "avatar_id": info.ID})
		}()

	case *events.HistorySync:
		m.mu.RLock()
		hsHandler := m.historySyncHandler
		// Get phone from the live client — available immediately after QR scan,
		// before updateStatus() has had a chance to write it to the DB.
		client := m.clients[id.String()]
		m.mu.RUnlock()
		if hsHandler == nil {
			return
		}

		sessionPhone := ""
		if client != nil && client.Store.ID != nil {
			sessionPhone = client.Store.ID.User
		}

		var sess models.WhatsAppSession
		if err := database.DB.Select("tenant_id, phone").Where("id = ?", id).First(&sess).Error; err != nil {
			return
		}
		// If the client's Store.ID wasn't ready, fall back to the DB value
		if sessionPhone == "" {
			sessionPhone = sess.Phone
		}

		// Observe full-history-sync progress so we can tell it's still running
		// (the initial full sync arrives in several batches over a few minutes).
		log.Printf("HistorySync phone=%s type=%s progress=%d%% conversations=%d",
			sessionPhone, e.Data.GetSyncType(), e.Data.GetProgress(), len(e.Data.GetConversations()))
		// Also ensure the DB phone is up to date right now
		if sessionPhone != "" && sess.Phone != sessionPhone {
			database.DB.Model(&models.WhatsAppSession{}).
				Where("id = ?", id).Update("phone", sessionPhone)
		}

		go hsHandler(id, sess.TenantID, sessionPhone, e.Data.GetConversations())

	case *events.Message:
		// Determine chat type. Skip status broadcasts and newsletters.
		chatServer := e.Info.Chat.Server
		var chatType models.ChatType
		var groupName, groupJID string
		switch chatServer {
		case types.DefaultUserServer, types.HiddenUserServer:
			chatType = models.ChatTypeIndividual
		case types.GroupServer:
			chatType = models.ChatTypeGroup
			groupJID = e.Info.Chat.String()
		case types.BroadcastServer:
			if e.Info.Chat.User == "status" {
				return // skip status stories
			}
			chatType = models.ChatTypeBroadcast
			groupJID = e.Info.Chat.String()
		default:
			return // skip newsletters and anything else
		}

		m.mu.RLock()
		handler := m.messageHandler
		m.mu.RUnlock()
		if handler == nil {
			return
		}

		// look up tenant + our phone number for this session
		var sess models.WhatsAppSession
		if err := database.DB.Select("tenant_id, phone").Where("id = ?", id).First(&sess).Error; err != nil {
			return
		}
		// Prefer live client phone over DB (in case DB hasn't been written yet)
		sessionPhone := sess.Phone
		m.mu.RLock()
		liveClient := m.clients[id.String()]
		m.mu.RUnlock()
		if sessionPhone == "" && liveClient != nil && liveClient.Store.ID != nil {
			sessionPhone = liveClient.Store.ID.User
		}

		isFromMe := e.Info.IsFromMe
		waID := e.Info.ID
		timestamp := e.Info.Timestamp
		pushName := e.Info.PushName

		// For groups/broadcasts: the "contact" is the chat (group/list) itself, not the sender.
		// For individual chats: the contact is the sender (or chat partner for isFromMe).
		var phone string
		if chatType == models.ChatTypeIndividual {
			contactJID := e.Info.Sender
			if isFromMe {
				contactJID = e.Info.Chat
			}
			phone = contactJID.User
			// Resolve LID → real phone number if needed.
			if contactJID.Server == types.HiddenUserServer || contactJID.Server == "hosted.lid" {
				if liveClient != nil && liveClient.Store.LIDs != nil {
					if pnJID, err := liveClient.Store.LIDs.GetPNForLID(context.Background(), contactJID); err == nil && !pnJID.IsEmpty() {
						phone = pnJID.User
					}
				}
			}
		} else {
			// Use the group/broadcast JID user part as the contact identifier.
			phone = e.Info.Chat.User
			// For group messages, try to look up the group name.
			if chatType == models.ChatTypeGroup && liveClient != nil {
				if info, err := liveClient.GetGroupInfo(context.Background(), e.Info.Chat); err == nil {
					groupName = info.Name
				}
			}
			if groupName == "" {
				groupName = e.Info.Chat.User
			}
		}

		var content string
		var msgType models.MessageType
		var mediaPayload []byte
		var reactionToID string

		switch {
		case e.Message.GetConversation() != "":
			content = e.Message.GetConversation()
			msgType = models.MessageTypeText
		case e.Message.GetExtendedTextMessage() != nil:
			content = e.Message.GetExtendedTextMessage().GetText()
			msgType = models.MessageTypeText
		case e.Message.GetImageMessage() != nil:
			content = e.Message.GetImageMessage().GetCaption()
			msgType = models.MessageTypeImage
			mediaPayload, _ = proto.Marshal(e.Message.GetImageMessage())
		case e.Message.GetVideoMessage() != nil:
			content = e.Message.GetVideoMessage().GetCaption()
			msgType = models.MessageTypeVideo
			mediaPayload, _ = proto.Marshal(e.Message.GetVideoMessage())
		case e.Message.GetAudioMessage() != nil:
			msgType = models.MessageTypeAudio
			mediaPayload, _ = proto.Marshal(e.Message.GetAudioMessage())
		case e.Message.GetDocumentMessage() != nil:
			content = e.Message.GetDocumentMessage().GetFileName()
			msgType = models.MessageTypeDocument
			mediaPayload, _ = proto.Marshal(e.Message.GetDocumentMessage())
		case e.Message.GetStickerMessage() != nil:
			msgType = models.MessageTypeSticker
			mediaPayload, _ = proto.Marshal(e.Message.GetStickerMessage())
		case e.Message.GetReactionMessage() != nil:
			msgType = models.MessageTypeReaction
			content = e.Message.GetReactionMessage().GetText()
			reactionToID = e.Message.GetReactionMessage().GetKey().GetID()
		case e.Message.GetLocationMessage() != nil:
			loc := e.Message.GetLocationMessage()
			content = fmt.Sprintf(`{"lat":%f,"lng":%f,"name":"%s","address":"%s"}`,
				loc.GetDegreesLatitude(), loc.GetDegreesLongitude(),
				loc.GetName(), loc.GetAddress())
			msgType = models.MessageTypeLocation
		case e.Message.GetContactMessage() != nil:
			cm := e.Message.GetContactMessage()
			content = fmt.Sprintf(`{"name":"%s","vcard":"%s"}`,
				cm.GetDisplayName(), escapeJSON(cm.GetVcard()))
			msgType = models.MessageTypeContact
		default:
			msgType = models.MessageTypeText
		}

		handler(id, sess.TenantID, sessionPhone, phone, pushName, waID, content, msgType, timestamp, mediaPayload, reactionToID, isFromMe, chatType, groupName, groupJID)
	}
}

func (m *Manager) updateStatus(sessionID uuid.UUID, status models.SessionStatus) {
	database.DB.Model(&models.WhatsAppSession{}).
		Where("id = ?", sessionID).
		Update("status", status)

	if status == models.StatusConnected {
		m.mu.RLock()
		client, ok := m.clients[sessionID.String()]
		m.mu.RUnlock()
		if ok && client != nil && client.Store.ID != nil {
			phone := client.Store.ID.User
			database.DB.Model(&models.WhatsAppSession{}).
				Where("id = ?", sessionID).
				Update("phone", phone)

			var sess models.WhatsAppSession
			if database.DB.First(&sess, "id = ?", sessionID).Error == nil {
				activity.Log(sess.TenantID, nil, "session.connected", "session", sessionID.String(), map[string]string{
					"phone": phone,
				})
			}
		}
	}

	if status == models.StatusDisconnected {
		var sess models.WhatsAppSession
		if database.DB.First(&sess, "id = ?", sessionID).Error == nil {
			activity.Log(sess.TenantID, nil, "session.disconnected", "session", sessionID.String(), map[string]string{
				"phone": sess.Phone,
			})
		}
	}
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}
