package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Plan string

const (
	PlanStarter Plan = "STARTER"
	PlanGrowth  Plan = "GROWTH"
	PlanScale   Plan = "SCALE"
)

type Role string

const (
	RoleAdmin Role = "ADMIN"
	RoleAgent Role = "AGENT"
)

type Tenant struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name          string         `gorm:"not null"                                       json:"name"`
	Plan          Plan           `gorm:"not null;default:'STARTER'"                    json:"plan"`
	PlanExpiresAt *time.Time     `json:"plan_expires_at,omitempty"`
	TrialEndsAt   *time.Time     `json:"trial_ends_at,omitempty"`
	IsSuspended   bool           `gorm:"default:false"                                  json:"is_suspended"`
	PaypalSubID   string         `gorm:"default:''"                                     json:"paypal_sub_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

type User struct {
	ID              uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID        uuid.UUID      `gorm:"type:uuid;not null"                             json:"tenant_id"`
	Tenant          Tenant         `gorm:"foreignKey:TenantID"                            json:"-"`
	Name            string         `gorm:"not null"                                       json:"name"`
	Email           string         `gorm:"not null"                                       json:"email"`
	PasswordHash    string         `gorm:"not null"                                       json:"-"`
	Role            Role           `gorm:"not null;default:'ADMIN'"                       json:"role"`
	IsSuperAdmin    bool           `gorm:"default:false"                                  json:"is_super_admin"`
	IsEmailVerified bool           `gorm:"default:false"                                  json:"is_email_verified"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

type SessionStatus string

const (
	StatusConnecting    SessionStatus = "CONNECTING"
	StatusConnected     SessionStatus = "CONNECTED"
	StatusDisconnected  SessionStatus = "DISCONNECTED"
	StatusBanned        SessionStatus = "BANNED"
)

type WhatsAppSession struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID    uuid.UUID      `gorm:"type:uuid;not null"                             json:"tenant_id"`
	Tenant      Tenant         `gorm:"foreignKey:TenantID"                            json:"-"`
	Phone       string         `gorm:"default:''"                                     json:"phone"`
	Status      SessionStatus  `gorm:"not null;default:'DISCONNECTED'"               json:"status"`
	DailyCount  int            `gorm:"default:0"                                     json:"daily_count"`
	ProxyURL    string         `gorm:"default:''"                                    json:"proxy_url,omitempty"`
	LastActive  *time.Time     `json:"last_active,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index"                                         json:"-"`
}

type MessageType string

const (
	MessageTypeText     MessageType = "TEXT"
	MessageTypeImage    MessageType = "IMAGE"
	MessageTypeAudio    MessageType = "AUDIO"
	MessageTypeVideo    MessageType = "VIDEO"
	MessageTypeDocument MessageType = "DOCUMENT"
	MessageTypeSticker  MessageType = "STICKER"
	MessageTypeLocation MessageType = "LOCATION"
	MessageTypeContact  MessageType = "CONTACT"
	MessageTypePoll     MessageType = "POLL"
	MessageTypeReaction MessageType = "REACTION"
)

type MessageDirection string

const (
	DirectionIncoming MessageDirection = "INCOMING"
	DirectionOutgoing MessageDirection = "OUTGOING"
)

type MessageStatus string

const (
	MessageStatusSent      MessageStatus = "SENT"
	MessageStatusDelivered MessageStatus = "DELIVERED"
	MessageStatusRead      MessageStatus = "READ"
	MessageStatusFailed    MessageStatus = "FAILED"
)

type ConversationStatus string

const (
	ConvStatusOpen     ConversationStatus = "OPEN"
	ConvStatusResolved ConversationStatus = "RESOLVED"
)

type Tag struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID  uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	Name      string         `gorm:"not null"                                       json:"name"`
	Color     string         `gorm:"default:'#3B82F6'"                              json:"color"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

type Contact struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID    uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	SessionID   uuid.UUID      `gorm:"type:uuid;not null"                             json:"session_id"`
	PhoneNumber string         `gorm:"not null"                                       json:"phone_number"`
	Name        string         `gorm:"default:''"                                     json:"name"`
	PushName    string         `gorm:"default:''"                                     json:"push_name"`
	WaID        string         `gorm:"default:''"                                     json:"wa_id"`
	AvatarURL   string         `gorm:"default:''"                                     json:"avatar_url"`
	AvatarID    string         `gorm:"default:''"                                     json:"avatar_id"`
	Tags        []Tag          `gorm:"many2many:contact_tags;"                        json:"tags"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

type Conversation struct {
	ID            uuid.UUID          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID      uuid.UUID          `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	SessionID     uuid.UUID          `gorm:"type:uuid;not null"                             json:"session_id"`
	SessionPhone  string             `gorm:"default:''"                                     json:"session_phone"`
	ContactID     uuid.UUID          `gorm:"type:uuid;not null"                             json:"contact_id"`
	Contact       Contact            `gorm:"foreignKey:ContactID"                           json:"contact"`
	Status        ConversationStatus `gorm:"not null;default:'OPEN'"                        json:"status"`
	AssignedTo    *uuid.UUID         `gorm:"type:uuid"                                      json:"assigned_to"`
	UnreadCount   int                `gorm:"default:0"                                      json:"unread_count"`
	LastMessageAt *time.Time         `gorm:"index"                                          json:"last_message_at"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	DeletedAt     gorm.DeletedAt     `gorm:"index"                                          json:"-"`
}

type Message struct {
	ID             uuid.UUID        `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	ConversationID uuid.UUID        `gorm:"type:uuid;not null;index"                       json:"conversation_id"`
	TenantID       uuid.UUID        `gorm:"type:uuid;not null"                             json:"tenant_id"`
	SenderID       *uuid.UUID       `gorm:"type:uuid"                                      json:"sender_id,omitempty"`
	Type           MessageType      `gorm:"not null;default:'TEXT'"                        json:"type"`
	Content        string           `gorm:"type:text;default:''"                           json:"content"`
	MediaURL       string           `gorm:"default:''"                                     json:"media_url,omitempty"`
	MediaPayload   []byte           `gorm:"type:bytea"                                     json:"-"`
	ReactionToID   string           `gorm:"default:''"                                     json:"reaction_to_id,omitempty"`
	Direction      MessageDirection `gorm:"not null"                                       json:"direction"`
	Status         MessageStatus    `gorm:"not null;default:'SENT'"                        json:"status"`
	WaMessageID    string           `gorm:"default:''"                                     json:"wa_message_id,omitempty"`
	IsNote         bool             `gorm:"default:false"                                  json:"is_note"`
	Timestamp      time.Time        `json:"timestamp"`
	CreatedAt      time.Time        `json:"created_at"`
	DeletedAt      gorm.DeletedAt   `gorm:"index"                                          json:"-"`
}

func (s *WhatsAppSession) BeforeCreate(tx *gorm.DB) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return nil
}

func (t *Tenant) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}

func (c *Contact) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

func (c *Conversation) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

func (m *Message) BeforeCreate(tx *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	return nil
}

// ─── Campaigns ───────────────────────────────────────────────────────────────

type CampaignStatus string

const (
	CampaignStatusDraft     CampaignStatus = "DRAFT"
	CampaignStatusScheduled CampaignStatus = "SCHEDULED"
	CampaignStatusRunning   CampaignStatus = "RUNNING"
	CampaignStatusPaused    CampaignStatus = "PAUSED"
	CampaignStatusCompleted CampaignStatus = "COMPLETED"
	CampaignStatusFailed    CampaignStatus = "FAILED"
)

type Campaign struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID      uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	SessionPhone  string         `gorm:"not null"                                       json:"session_phone"`
	Name          string         `gorm:"not null"                                       json:"name"`
	Message       string         `gorm:"type:text;not null"                             json:"message"`
	Status        CampaignStatus `gorm:"not null;default:'DRAFT'"                       json:"status"`
	ScheduledAt   *time.Time     `json:"scheduled_at,omitempty"`
	StartedAt     *time.Time     `json:"started_at,omitempty"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	TotalContacts int            `gorm:"default:0"                                      json:"total_contacts"`
	SentCount     int            `gorm:"default:0"                                      json:"sent_count"`
	FailedCount   int            `gorm:"default:0"                                      json:"failed_count"`
	FunnelID      *uuid.UUID     `gorm:"type:uuid"                                      json:"funnel_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

type CampaignContactStatus string

const (
	CampaignContactPending CampaignContactStatus = "PENDING"
	CampaignContactSent    CampaignContactStatus = "SENT"
	CampaignContactFailed  CampaignContactStatus = "FAILED"
)

type CampaignContact struct {
	ID         uuid.UUID             `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CampaignID uuid.UUID             `gorm:"type:uuid;not null;index"                       json:"campaign_id"`
	ContactID  uuid.UUID             `gorm:"type:uuid;not null"                             json:"contact_id"`
	Contact    Contact               `gorm:"foreignKey:ContactID"                           json:"contact"`
	Status     CampaignContactStatus `gorm:"not null;default:'PENDING'"                     json:"status"`
	SentAt     *time.Time            `json:"sent_at,omitempty"`
	ErrorMsg   string                `gorm:"default:''"                                     json:"error_msg,omitempty"`
	CreatedAt  time.Time             `json:"created_at"`
	DeletedAt  gorm.DeletedAt        `gorm:"index"                                          json:"-"`
}

func (c *Campaign) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

func (cc *CampaignContact) BeforeCreate(tx *gorm.DB) error {
	if cc.ID == uuid.Nil {
		cc.ID = uuid.New()
	}
	return nil
}

// ─── Funnels ─────────────────────────────────────────────────────────────────

type FunnelStatus string

const (
	FunnelStatusDraft    FunnelStatus = "DRAFT"
	FunnelStatusActive   FunnelStatus = "ACTIVE"
	FunnelStatusPaused   FunnelStatus = "PAUSED"
	FunnelStatusArchived FunnelStatus = "ARCHIVED"
)

type FunnelStepType string

const (
	FunnelStepEntry        FunnelStepType = "ENTRY"
	FunnelStepReplyTrigger FunnelStepType = "REPLY_TRIGGER"
	FunnelStepManual       FunnelStepType = "MANUAL"
)

type FunnelContactStatus string

const (
	FunnelContactActive    FunnelContactStatus = "ACTIVE"
	FunnelContactConverted FunnelContactStatus = "CONVERTED"
	FunnelContactDropped   FunnelContactStatus = "DROPPED"
)

type Funnel struct {
	ID               uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID         uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	SessionPhone     string         `gorm:"not null"                                       json:"session_phone"`
	Name             string         `gorm:"not null"                                       json:"name"`
	Description      string         `gorm:"default:''"                                     json:"description,omitempty"`
	Status           FunnelStatus   `gorm:"not null;default:'DRAFT'"                       json:"status"`
	ReplyWindowHours int            `gorm:"default:48"                                     json:"reply_window_hours"`
	Steps            []FunnelStep   `gorm:"foreignKey:FunnelID"                            json:"steps,omitempty"`
	ContactCount     int            `gorm:"-"                                              json:"contact_count,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

type FunnelStep struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	FunnelID  uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"funnel_id"`
	Order     int            `gorm:"not null"                                       json:"order"`
	Name      string         `gorm:"not null"                                       json:"name"`
	Type      FunnelStepType `gorm:"not null"                                       json:"type"`
	Message   string         `gorm:"type:text;default:''"                           json:"message,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

type FunnelContact struct {
	ID            uuid.UUID           `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	FunnelID      uuid.UUID           `gorm:"type:uuid;not null;index"                       json:"funnel_id"`
	ContactID     uuid.UUID           `gorm:"type:uuid;not null"                             json:"contact_id"`
	Contact       Contact             `gorm:"foreignKey:ContactID"                           json:"contact"`
	CurrentStepID uuid.UUID           `gorm:"type:uuid;not null"                             json:"current_step_id"`
	CurrentStep   FunnelStep          `gorm:"foreignKey:CurrentStepID"                       json:"current_step"`
	Status        FunnelContactStatus `gorm:"not null;default:'ACTIVE'"                      json:"status"`
	EnteredAt     time.Time           `json:"entered_at"`
	LastMovedAt   time.Time           `json:"last_moved_at"`
	DeletedAt     gorm.DeletedAt      `gorm:"index"                                          json:"-"`
}

type FunnelContactHistory struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	FunnelID   uuid.UUID  `gorm:"type:uuid;not null;index"                       json:"funnel_id"`
	ContactID  uuid.UUID  `gorm:"type:uuid;not null"                             json:"contact_id"`
	FromStepID *uuid.UUID `gorm:"type:uuid"                                      json:"from_step_id,omitempty"`
	ToStepID   uuid.UUID  `gorm:"type:uuid;not null"                             json:"to_step_id"`
	Trigger    string     `gorm:"not null;default:'MANUAL'"                      json:"trigger"`
	MovedBy    *uuid.UUID `gorm:"type:uuid"                                      json:"moved_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (f *Funnel) BeforeCreate(tx *gorm.DB) error {
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	return nil
}

func (fs *FunnelStep) BeforeCreate(tx *gorm.DB) error {
	if fs.ID == uuid.Nil {
		fs.ID = uuid.New()
	}
	return nil
}

func (fc *FunnelContact) BeforeCreate(tx *gorm.DB) error {
	if fc.ID == uuid.Nil {
		fc.ID = uuid.New()
	}
	return nil
}

func (fh *FunnelContactHistory) BeforeCreate(tx *gorm.DB) error {
	if fh.ID == uuid.Nil {
		fh.ID = uuid.New()
	}
	return nil
}

// ─── Flows ────────────────────────────────────────────────────────────────────

type FlowTrigger string

const (
	FlowTriggerAnyMessage  FlowTrigger = "any_message"
	FlowTriggerKeyword     FlowTrigger = "keyword"
	FlowTriggerNewContact  FlowTrigger = "new_contact"
)

type Flow struct {
	ID           uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID     uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	SessionPhone string         `gorm:"default:''"                                     json:"session_phone"`
	Name         string         `gorm:"not null"                                       json:"name"`
	Trigger      FlowTrigger    `gorm:"not null"                                       json:"trigger"`
	Keyword      string         `gorm:"default:''"                                     json:"keyword,omitempty"`
	Nodes        string         `gorm:"type:text;default:'[]'"                         json:"nodes"`
	IsActive     bool           `gorm:"default:true"                                   json:"is_active"`
	RunCount     int            `gorm:"default:0"                                      json:"run_count"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

func (f *Flow) BeforeCreate(tx *gorm.DB) error {
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	return nil
}

// ─── Activity Log ─────────────────────────────────────────────────────────────

type ActivityLog struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID   uuid.UUID  `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	UserID     *uuid.UUID `gorm:"type:uuid"                                      json:"user_id,omitempty"`
	UserName   string     `gorm:"-"                                              json:"user_name,omitempty"`
	Action     string     `gorm:"not null"                                       json:"action"`
	EntityType string     `gorm:"not null;default:''"                            json:"entity_type"`
	EntityID   string     `gorm:"default:''"                                     json:"entity_id"`
	Metadata   string     `gorm:"type:text;default:'{}'"                         json:"metadata"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (a *ActivityLog) BeforeCreate(tx *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

// ─── Products ─────────────────────────────────────────────────────────────────

type Product struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID    uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	Name        string         `gorm:"not null"                                       json:"name"`
	Price       float64        `gorm:"default:0"                                      json:"price"`
	Description string         `gorm:"type:text;default:''"                           json:"description"`
	ImageURL    string         `gorm:"default:''"                                     json:"image_url"`
	Link        string         `gorm:"default:''"                                     json:"link"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

func (p *Product) BeforeCreate(tx *gorm.DB) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return nil
}

// ─── Quick Replies ─────────────────────────────────────────────────────────────

type QuickReply struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID  uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	Title     string         `gorm:"not null"                                       json:"title"`
	Content   string         `gorm:"type:text;not null"                             json:"content"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

func (q *QuickReply) BeforeCreate(tx *gorm.DB) error {
	if q.ID == uuid.Nil {
		q.ID = uuid.New()
	}
	return nil
}

// ─── API Keys ─────────────────────────────────────────────────────────────────

type APIKey struct {
	ID         uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID   uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	Name       string         `gorm:"not null"                                       json:"name"`
	KeyHash    string         `gorm:"not null"                                       json:"-"`
	Prefix     string         `gorm:"not null"                                       json:"prefix"`
	LastUsedAt *time.Time     `json:"last_used_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

func (k *APIKey) BeforeCreate(tx *gorm.DB) error {
	if k.ID == uuid.Nil {
		k.ID = uuid.New()
	}
	return nil
}

// ─── Webhooks ─────────────────────────────────────────────────────────────────

type Webhook struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID  uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	URL       string         `gorm:"not null"                                       json:"url"`
	Events    string         `gorm:"type:text;not null;default:'[]'"                json:"events"`
	Secret    string         `gorm:"not null"                                       json:"secret"`
	IsActive  bool           `gorm:"default:true"                                   json:"is_active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

func (w *Webhook) BeforeCreate(tx *gorm.DB) error {
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	return nil
}

// ─── Auth Tokens ──────────────────────────────────────────────────────────────

type EmailVerificationToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index"                       json:"user_id"`
	Token     string    `gorm:"not null;uniqueIndex"                           json:"-"`
	ExpiresAt time.Time `gorm:"not null"                                       json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func (t *EmailVerificationToken) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}

type PasswordResetToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index"                       json:"user_id"`
	Token     string    `gorm:"not null;uniqueIndex"                           json:"-"`
	ExpiresAt time.Time `gorm:"not null"                                       json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func (t *PasswordResetToken) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}

type RefreshToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index"                       json:"user_id"`
	Token     string    `gorm:"not null;uniqueIndex"                           json:"-"`
	ExpiresAt time.Time `gorm:"not null"                                       json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func (t *RefreshToken) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}

// ─── Billing / Subscriptions ──────────────────────────────────────────────────

type SubscriptionStatus string

const (
	SubStatusPending   SubscriptionStatus = "PENDING"    // awaiting PayPal approval
	SubStatusActive    SubscriptionStatus = "ACTIVE"     // recurring subscription live
	SubStatusCancelled SubscriptionStatus = "CANCELLED"  // user or PayPal cancelled
	SubStatusFailed    SubscriptionStatus = "FAILED"     // payment failed
	// legacy — kept for backwards-compat with old one-time payment records
	SubStatusPaid SubscriptionStatus = "PAID"
)

type Subscription struct {
	ID          uuid.UUID          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID    uuid.UUID          `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	Plan        Plan               `gorm:"not null"                                       json:"plan"`
	Amount      float64            `gorm:"not null"                                       json:"amount"`
	Currency    string             `gorm:"not null;default:'USD'"                         json:"currency"`
	CartID      string             `gorm:"not null;uniqueIndex"                           json:"cart_id"`
	PaypalSubID string             `gorm:"default:'';index"                               json:"paypal_sub_id,omitempty"`
	Status      SubscriptionStatus `gorm:"not null;default:'PENDING'"                     json:"status"`
	PaidAt      *time.Time         `json:"paid_at,omitempty"`
	ExpiresAt   *time.Time         `json:"expires_at,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

func (s *Subscription) BeforeCreate(tx *gorm.DB) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return nil
}

// PlatformSetting is a simple key-value store for platform-wide configuration
// (e.g. PayPal plan IDs) that must persist across server restarts.
type PlatformSetting struct {
	Key       string    `gorm:"primaryKey"    json:"key"`
	Value     string    `gorm:"not null"      json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}
