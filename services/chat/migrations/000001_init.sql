-- Chat service initial schema (chat_db).
-- Idempotent DDL so Migrate can safely run on every startup.

CREATE TABLE IF NOT EXISTS chat_conversations (
    id                       UUID PRIMARY KEY,
    buyer_id                 TEXT NOT NULL,
    shop_id                  TEXT NOT NULL,
    site_host                TEXT NOT NULL DEFAULT '',
    last_message_id          UUID,
    last_message_seq         BIGINT NOT NULL DEFAULT 0,
    last_message_preview     TEXT NOT NULL DEFAULT '',
    last_message_sender_role TEXT NOT NULL DEFAULT '',
    last_message_at          TIMESTAMPTZ,
    buyer_last_read_seq      BIGINT NOT NULL DEFAULT 0,
    buyer_last_read_at       TIMESTAMPTZ,
    seller_last_read_seq     BIGINT NOT NULL DEFAULT 0,
    seller_last_read_at      TIMESTAMPTZ,
    last_auto_reply_at       TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_chat_conversations_buyer_shop UNIQUE (buyer_id, shop_id)
);

CREATE INDEX IF NOT EXISTS idx_chat_conversations_buyer_activity
    ON chat_conversations (buyer_id, COALESCE(last_message_at, created_at) DESC);

CREATE INDEX IF NOT EXISTS idx_chat_conversations_shop_activity
    ON chat_conversations (shop_id, COALESCE(last_message_at, created_at) DESC);

CREATE TABLE IF NOT EXISTS chat_messages (
    seq             BIGSERIAL PRIMARY KEY,
    id              UUID NOT NULL UNIQUE,
    conversation_id UUID NOT NULL REFERENCES chat_conversations (id) ON DELETE CASCADE,
    sender_id       TEXT NOT NULL,
    sender_role     TEXT NOT NULL,
    kind            TEXT NOT NULL,
    body            TEXT NOT NULL DEFAULT '',
    ref_id          TEXT NOT NULL DEFAULT '',
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_auto_reply   BOOLEAN NOT NULL DEFAULT FALSE,
    client_msg_id   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_chat_messages_client_msg
    ON chat_messages (conversation_id, sender_id, client_msg_id)
    WHERE client_msg_id <> '';

CREATE INDEX IF NOT EXISTS idx_chat_messages_conv_seq
    ON chat_messages (conversation_id, seq DESC);

CREATE TABLE IF NOT EXISTS chat_shop_auto_replies (
    shop_id          TEXT PRIMARY KEY,
    enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    greeting_message TEXT NOT NULL DEFAULT '',
    offline_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
    offline_message  TEXT NOT NULL DEFAULT '',
    work_start_hour  SMALLINT NOT NULL DEFAULT 8,
    work_end_hour    SMALLINT NOT NULL DEFAULT 22,
    timezone         TEXT NOT NULL DEFAULT 'Asia/Ho_Chi_Minh',
    cooldown_minutes INT NOT NULL DEFAULT 30,
    keyword_rules    JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
