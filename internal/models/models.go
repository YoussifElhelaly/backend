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
	ID                uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name              string         `gorm:"not null"                                       json:"name"`
	Plan              Plan           `gorm:"not null;default:'STARTER'"                    json:"plan"`
	PlanExpiresAt     *time.Time     `json:"plan_expires_at,omitempty"`
	TrialEndsAt       *time.Time     `json:"trial_ends_at,omitempty"`
	IsSuspended       bool           `gorm:"default:false"                                  json:"is_suspended"`
	PaytabsToken      string         `gorm:"default:''"                                     json:"-"`
	CampaignDelayMin  int            `gorm:"default:3"                                      json:"campaign_delay_min"`
	CampaignDelayMax  int            `gorm:"default:8"                                      json:"campaign_delay_max"`
	DailyMessageLimit int            `gorm:"default:25"                                     json:"daily_message_limit"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `gorm:"index"                                          json:"-"`
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
	StatusConnecting   SessionStatus = "CONNECTING"
	StatusConnected    SessionStatus = "CONNECTED"
	StatusDisconnected SessionStatus = "DISCONNECTED"
	StatusBanned       SessionStatus = "BANNED"
)

type WhatsAppSession struct {
	ID         uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID   uuid.UUID      `gorm:"type:uuid;not null"                             json:"tenant_id"`
	Tenant     Tenant         `gorm:"foreignKey:TenantID"                            json:"-"`
	Phone      string         `gorm:"default:''"                                     json:"phone"`
	Status     SessionStatus  `gorm:"not null;default:'DISCONNECTED'"               json:"status"`
	DailyCount int            `gorm:"default:0"                                     json:"daily_count"`
	ProxyURL   string         `gorm:"default:''"                                    json:"proxy_url,omitempty"`
	LastActive *time.Time     `json:"last_active,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index"                                         json:"-"`
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

type ChatType string

const (
	ChatTypeIndividual ChatType = "individual"
	ChatTypeGroup      ChatType = "group"
	ChatTypeBroadcast  ChatType = "broadcast"
)

type Tag struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID  uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	Name      string         `gorm:"not null"                                       json:"name"`
	Color     string         `gorm:"default:'#3B82F6'"                              json:"color"`
	Contacts  []Contact      `gorm:"many2many:contact_tags;"                        json:"contacts,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

type Contact struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID    uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	SessionID   uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"session_id"`
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
	SessionID     uuid.UUID          `gorm:"type:uuid;not null;index"                       json:"session_id"`
	SessionPhone  string             `gorm:"default:''"                                     json:"session_phone"`
	ContactID     uuid.UUID          `gorm:"type:uuid;not null;index"                       json:"contact_id"`
	Contact       Contact            `gorm:"foreignKey:ContactID;constraint:OnDelete:CASCADE" json:"contact"`
	Status        ConversationStatus `gorm:"not null;default:'OPEN'"                        json:"status"`
	ChatType      ChatType           `gorm:"default:'individual'"                           json:"chat_type"`
	GroupName     string             `gorm:"default:''"                                     json:"group_name"`
	GroupJID      string             `gorm:"default:''"                                     json:"group_jid"`
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
	Sender         *User            `gorm:"foreignKey:SenderID"                            json:"-"`
	Type           MessageType      `gorm:"not null;default:'TEXT'"                        json:"type"`
	Content        string           `gorm:"type:text;default:''"                           json:"content"`
	MediaURL       string           `gorm:"default:''"                                     json:"media_url,omitempty"`
	MediaPayload   []byte           `gorm:"type:bytea"                                     json:"-"`
	ReactionToID   string           `gorm:"default:''"                                     json:"reaction_to_id,omitempty"`
	Direction      MessageDirection `gorm:"not null;index"                                 json:"direction"`
	Status         MessageStatus    `gorm:"not null;default:'SENT'"                        json:"status"`
	WaMessageID    string           `gorm:"default:'';index"                               json:"wa_message_id,omitempty"`
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
	Variants      string         `gorm:"type:text;default:'[]'"                         json:"variants"` // JSON []string — AI-generated clones
	MediaPayload  []byte         `gorm:"type:bytea"                                     json:"-"`
	MediaMime     string         `gorm:"default:''"                                     json:"media_mime,omitempty"`
	MediaName     string         `gorm:"default:''"                                     json:"media_name,omitempty"`
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

type FunnelTimeoutAction string

const (
	FunnelTimeoutNone     FunnelTimeoutAction = "NONE"
	FunnelTimeoutAutoDrop FunnelTimeoutAction = "AUTO_DROP"
	FunnelTimeoutFollowUp FunnelTimeoutAction = "FOLLOW_UP"
)

type Funnel struct {
	ID               uuid.UUID           `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID         uuid.UUID           `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	SessionPhone     string              `gorm:"not null"                                       json:"session_phone"`
	Name             string              `gorm:"not null"                                       json:"name"`
	Description      string              `gorm:"default:''"                                     json:"description,omitempty"`
	Status           FunnelStatus        `gorm:"not null;default:'DRAFT'"                       json:"status"`
	ReplyWindowHours int                 `gorm:"default:48"                                     json:"reply_window_hours"`
	TimeoutAction    FunnelTimeoutAction `gorm:"not null;default:'NONE'"                       json:"timeout_action"`
	FollowUpMessage  string              `gorm:"type:text;default:''"                           json:"follow_up_message,omitempty"`
	Steps            []FunnelStep        `gorm:"foreignKey:FunnelID"                            json:"steps,omitempty"`
	ContactCount     int                 `gorm:"-"                                              json:"contact_count,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
	DeletedAt        gorm.DeletedAt      `gorm:"index"                                          json:"-"`
}

type FunnelStep struct {
	ID              uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	FunnelID        uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"funnel_id"`
	Order           int            `gorm:"not null"                                    json:"order"`
	Name            string         `gorm:"not null"                                    json:"name"`
	Type            FunnelStepType `gorm:"not null"                                    json:"type"`
	Message         string         `gorm:"type:text;default:''"                        json:"message,omitempty"`
	Variants        string         `gorm:"type:text;default:'[]'"                      json:"variants"` // JSON []string
	MediaPayload    []byte         `gorm:"type:bytea"                                  json:"-"`
	MediaMime       string         `gorm:"default:''"                                  json:"media_mime,omitempty"`
	MediaName       string         `gorm:"default:''"                                  json:"media_name,omitempty"`
	DelayHours      int            `gorm:"not null;default:0"                          json:"delay_hours"`
	DelayFromStepID *uuid.UUID     `gorm:"type:uuid"                                   json:"delay_from_step_id,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index"                                       json:"-"`
}

type FunnelContact struct {
	ID            uuid.UUID           `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	FunnelID      uuid.UUID           `gorm:"type:uuid;not null;index"                       json:"funnel_id"`
	Funnel        Funnel              `gorm:"foreignKey:FunnelID"                            json:"-"`
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
	ID         uuid.UUID   `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	FunnelID   uuid.UUID   `gorm:"type:uuid;not null;index"                       json:"funnel_id"`
	ContactID  uuid.UUID   `gorm:"type:uuid;not null"                             json:"contact_id"`
	FromStepID *uuid.UUID  `gorm:"type:uuid"                                      json:"from_step_id,omitempty"`
	FromStep   *FunnelStep `gorm:"foreignKey:FromStepID"                         json:"from_step,omitempty"`
	ToStepID   uuid.UUID   `gorm:"type:uuid;not null"                             json:"to_step_id"`
	ToStep     FunnelStep  `gorm:"foreignKey:ToStepID"                            json:"to_step"`
	Trigger    string      `gorm:"not null;default:'MANUAL'"                      json:"trigger"`
	MovedBy    *uuid.UUID  `gorm:"type:uuid"                                      json:"moved_by,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
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
	FlowTriggerAnyMessage FlowTrigger = "any_message"
	FlowTriggerKeyword    FlowTrigger = "keyword"
	FlowTriggerNewContact FlowTrigger = "new_contact"
)

type Flow struct {
	ID               uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID         uuid.UUID      `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	SessionPhone     string         `gorm:"default:''"                                     json:"session_phone"`
	Name             string         `gorm:"not null"                                       json:"name"`
	Trigger          FlowTrigger    `gorm:"not null"                                       json:"trigger"`
	Keyword          string         `gorm:"default:''"                                     json:"keyword,omitempty"`
	KeywordMatchType string         `gorm:"default:'contains'"                             json:"keyword_match_type"` // contains | exact | starts_with
	CooldownSeconds  int            `gorm:"default:0"                                      json:"cooldown_seconds"`   // 0 = no cooldown
	Nodes            string         `gorm:"type:text;default:'[]'"                         json:"nodes"`
	MediaPayload     []byte         `gorm:"type:bytea"                                     json:"-"`
	MediaMime        string         `gorm:"default:''"                                     json:"media_mime,omitempty"`
	MediaName        string         `gorm:"default:''"                                     json:"media_name,omitempty"`
	IsActive         bool           `gorm:"default:true"                                   json:"is_active"`
	RunCount         int            `gorm:"default:0"                                      json:"run_count"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

func (f *Flow) BeforeCreate(tx *gorm.DB) error {
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	return nil
}

// ─── Flow Runs ────────────────────────────────────────────────────────────────

type FlowRun struct {
	ID           uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	FlowID       uuid.UUID  `gorm:"type:uuid;not null;index"                        json:"flow_id"`
	TenantID     uuid.UUID  `gorm:"type:uuid;not null;index"                        json:"tenant_id"`
	ContactID    *uuid.UUID `gorm:"type:uuid"                                        json:"contact_id"`
	ContactName  string     `gorm:"-"                                                json:"contact_name"`
	ContactPhone string     `gorm:"-"                                                json:"contact_phone"`
	TriggerMsg   string     `gorm:"column:trigger_message;type:text"                 json:"trigger_message"`
	Status       string     `gorm:"not null;default:'completed'"                     json:"status"`  // completed | failed | partial
	Actions      string     `gorm:"type:text;default:'[]'"                           json:"actions"` // JSON [{type,detail,status,error}]
	ExecutedAt   time.Time  `gorm:"not null;index"                                   json:"executed_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (f *FlowRun) BeforeCreate(tx *gorm.DB) error {
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
	ImageData   []byte         `gorm:"type:bytea"                                     json:"-"`
	ImageMime   string         `gorm:"default:''"                                     json:"-"`
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
	SubStatusPending   SubscriptionStatus = "PENDING"   // awaiting PayTabs confirmation
	SubStatusActive    SubscriptionStatus = "ACTIVE"    // recurring subscription live
	SubStatusCancelled SubscriptionStatus = "CANCELLED" // user or gateway cancelled
	SubStatusFailed    SubscriptionStatus = "FAILED"    // payment failed
	// legacy — kept for backwards-compat with old one-time payment records
	SubStatusPaid SubscriptionStatus = "PAID"
)

type Subscription struct {
	ID             uuid.UUID          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID       uuid.UUID          `gorm:"type:uuid;not null;index"                       json:"tenant_id"`
	Plan           Plan               `gorm:"not null"                                       json:"plan"`
	Amount         float64            `gorm:"not null"                                       json:"amount"`
	Currency       string             `gorm:"not null;default:'EGP'"                         json:"currency"`
	CartID         string             `gorm:"not null;uniqueIndex"                           json:"cart_id"`
	PaytabsTranRef string             `gorm:"default:'';index"                               json:"paytabs_tran_ref,omitempty"`
	PaytabsToken   string             `gorm:"default:''"                                     json:"-"`
	BillingCycle   string             `gorm:"default:'mo'"                                   json:"billing_cycle"` // mo | 6mo | 12mo
	Status         SubscriptionStatus `gorm:"not null;default:'PENDING'"                     json:"status"`
	PaidAt         *time.Time         `json:"paid_at,omitempty"`
	ExpiresAt      *time.Time         `json:"expires_at,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

func (s *Subscription) BeforeCreate(tx *gorm.DB) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return nil
}

// PlatformSetting is a simple key-value store for platform-wide configuration
// that must persist across server restarts.
type PlatformSetting struct {
	Key       string    `gorm:"primaryKey"    json:"key"`
	Value     string    `gorm:"not null"      json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ─── AI Configuration ─────────────────────────────────────────────────────────

type AIConfig struct {
	ID           uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID     uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex"                 json:"tenant_id"`
	Platform     string         `gorm:"not null;default:'openai'"                      json:"platform"` // openai | anthropic | gemini
	Model        string         `gorm:"not null;default:'gpt-4o-mini'"                 json:"model"`
	EncryptedKey string         `gorm:"type:text;not null;default:''"                  json:"-"`
	KeyHint      string         `gorm:"default:''"                                     json:"key_hint"` // last 4 chars shown in UI
	IsActive     bool           `gorm:"default:true"                                   json:"is_active"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index"                                          json:"-"`
}

func (a *AIConfig) BeforeCreate(tx *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

// ─── Plan Definitions (Admin-manageable) ─────────────────────────────────────

type PlanDef struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name             string    `gorm:"not null;uniqueIndex"                          json:"name"`
	Label            string    `gorm:"not null"                                      json:"label"`
	PriceEGP         float64   `gorm:"not null"                                      json:"price_egp"`
	OriginalPriceEGP float64   `gorm:"not null;default:0"                            json:"original_price_egp"`
	Price6moEGP      float64   `gorm:"column:price_6mo_egp;not null;default:0"        json:"price_6mo_egp"`  // total for 6-month billing; 0 = not offered
	Price12moEGP     float64   `gorm:"column:price_12mo_egp;not null;default:0"       json:"price_12mo_egp"` // total for 12-month billing; 0 = not offered
	Period           string    `gorm:"not null;default:'mo'"                         json:"period"`
	IntervalCount    int       `gorm:"not null;default:1"                            json:"interval_count"`
	Desc             string    `gorm:"type:text;default:''"                          json:"desc"`
	Badge            string    `gorm:"default:''"                                    json:"badge"`
	CTA              string    `gorm:"not null;default:'Start free'"                 json:"cta"`
	SortOrder        int       `gorm:"default:0"                                     json:"sort_order"`
	Sessions         int       `gorm:"not null;default:1"                            json:"sessions"`
	MessagesDay      int       `gorm:"not null;default:500"                          json:"messages_day"`
	Agents           int       `gorm:"not null;default:2"                            json:"agents"`
	Flows            int       `gorm:"not null;default:-1"                           json:"flows"`
	Funnels          int       `gorm:"not null;default:-1"                           json:"funnels"`
	QuickReplies     int       `gorm:"not null;default:-1"                           json:"quick_replies"`
	Campaigns        int       `gorm:"not null;default:-1"                           json:"campaigns"`
	Features         string    `gorm:"type:text;default:'[]'"                        json:"features"`
	IsCustom         bool      `gorm:"default:false"                                 json:"is_custom"`
	IsActive         bool      `gorm:"default:true"                                  json:"is_active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (p *PlanDef) BeforeCreate(tx *gorm.DB) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return nil
}

// ─── Leads (landing page form submissions) ────────────────────────────────────

type Lead struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name      string    `gorm:"not null"                                       json:"name"`
	Email     string    `gorm:"not null;index"                                 json:"email"`
	Phone     string    `gorm:"default:''"                                     json:"phone"`
	Source    string    `gorm:"default:'landing'"                              json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

func (l *Lead) BeforeCreate(tx *gorm.DB) error {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	return nil
}
