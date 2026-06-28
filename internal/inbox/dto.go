package inbox

import "github.com/google/uuid"

type SendMessageRequest struct {
	Content string `json:"content" binding:"required"`
}

type AssignRequest struct {
	AgentID *uuid.UUID `json:"agent_id"`
}

type StatusRequest struct {
	Status string `json:"status" binding:"required,oneof=OPEN RESOLVED"`
}

type NoteRequest struct {
	Content string `json:"content" binding:"required"`
}

type ConversationResponse struct {
	ID            string           `json:"id"`
	SessionID     string           `json:"session_id"`
	SessionPhone  string           `json:"session_phone"`
	Contact       ContactResponse  `json:"contact"`
	Status        string           `json:"status"`
	ChatType      string           `json:"chat_type"`
	GroupName     string           `json:"group_name,omitempty"`
	GroupJID      string           `json:"group_jid,omitempty"`
	AssignedTo    *string          `json:"assigned_to"`
	UnreadCount   int              `json:"unread_count"`
	LastMessage   *MessageResponse `json:"last_message,omitempty"`
	LastMessageAt *string          `json:"last_message_at,omitempty"`
	UpdatedAt     string           `json:"updated_at"`
	CreatedAt     string           `json:"created_at"`
}

type ContactResponse struct {
	ID          string `json:"id"`
	PhoneNumber string `json:"phone_number"`
	Name        string `json:"name"`
	PushName    string `json:"push_name"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

type MessageResponse struct {
	ID             string  `json:"id"`
	ConversationID string  `json:"conversation_id"`
	SenderID       *string `json:"sender_id,omitempty"`
	Type           string  `json:"type"`
	Content        string  `json:"content"`
	MediaURL       string  `json:"media_url,omitempty"`
	Direction      string  `json:"direction"`
	Status         string  `json:"status"`
	IsNote         bool    `json:"is_note"`
	Timestamp      string  `json:"timestamp"`
	ReactionToID   string  `json:"reaction_to_id,omitempty"`
	WaMessageID    string  `json:"wa_message_id,omitempty"`
}
