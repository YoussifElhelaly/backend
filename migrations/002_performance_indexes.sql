-- Performance indexes for Whatify v2
-- Run after initial deployment: psql -f migrations/002_performance_indexes.sql

-- Conversations: most frequent query pattern is tenant_id + session_phone + ordering by last_message_at
CREATE INDEX IF NOT EXISTS idx_conversations_tenant_session ON conversations(tenant_id, session_phone);
CREATE INDEX IF NOT EXISTS idx_conversations_tenant_contact ON conversations(tenant_id, contact_id);
CREATE INDEX IF NOT EXISTS idx_conversations_last_message ON conversations(tenant_id, last_message_at DESC NULLS LAST);

-- Messages: queried by conversation_id + timestamp, and by tenant_id + created_at (for delta sync)
CREATE INDEX IF NOT EXISTS idx_messages_conversation_ts ON messages(conversation_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_messages_tenant_created ON messages(tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id);

-- Funnel contacts: queried by contact_id + status, funnel_id + status
CREATE INDEX IF NOT EXISTS idx_funnel_contacts_contact_status ON funnel_contacts(contact_id, status);
CREATE INDEX IF NOT EXISTS idx_funnel_contacts_funnel_status ON funnel_contacts(funnel_id, status);

-- Campaign contacts: queried by campaign_id + status
CREATE INDEX IF NOT EXISTS idx_campaign_contacts_campaign_status ON campaign_contacts(campaign_id, status);

-- Activity logs: queried by tenant_id + created_at
CREATE INDEX IF NOT EXISTS idx_activity_logs_tenant_created ON activity_logs(tenant_id, created_at DESC);

-- WhatsApp sessions: queried by tenant_id + status
CREATE INDEX IF NOT EXISTS idx_whatsapp_sessions_tenant_status ON whats_app_sessions(tenant_id, status);

-- Contacts: queried by tenant_id + phone_number (unique lookup)
CREATE INDEX IF NOT EXISTS idx_contacts_tenant_phone ON contacts(tenant_id, phone_number);
