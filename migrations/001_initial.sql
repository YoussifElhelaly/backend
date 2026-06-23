-- Whatify v2 — Initial Schema

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Tenants (companies)
CREATE TABLE IF NOT EXISTS tenants (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name        VARCHAR(255) NOT NULL,
    plan        VARCHAR(50)  NOT NULL DEFAULT 'STARTER', -- STARTER | GROWTH | SCALE
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Users
CREATE TABLE IF NOT EXISTS users (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id      UUID         NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name           VARCHAR(255) NOT NULL,
    email          VARCHAR(255) NOT NULL,
    password_hash  VARCHAR(255) NOT NULL,
    role           VARCHAR(50)  NOT NULL DEFAULT 'ADMIN', -- ADMIN | AGENT
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(email, tenant_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_unique ON users(email);

-- WhatsApp Sessions
CREATE TABLE IF NOT EXISTS whatsapp_sessions (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id    UUID         NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    phone        VARCHAR(50),
    status       VARCHAR(50)  NOT NULL DEFAULT 'DISCONNECTED', -- CONNECTING | CONNECTED | DISCONNECTED | BANNED
    proxy_url    VARCHAR(500),
    daily_count  INT          NOT NULL DEFAULT 0,
    last_active  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
