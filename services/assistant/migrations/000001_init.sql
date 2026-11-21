CREATE TABLE IF NOT EXISTS assistant_conversations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL DEFAULT '',
    shop_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_assistant_conversations_user_updated
    ON assistant_conversations (user_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_assistant_conversations_shop_updated
    ON assistant_conversations (shop_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS assistant_messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES assistant_conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    tool_calls_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    retrieved_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_assistant_messages_conv_created
    ON assistant_messages (conversation_id, created_at ASC);

CREATE TABLE IF NOT EXISTS assistant_feedback (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL DEFAULT '',
    message_id TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL DEFAULT '',
    helpful BOOLEAN NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_assistant_feedback_conv_created
    ON assistant_feedback (conversation_id, created_at DESC);

CREATE TABLE IF NOT EXISTS assistant_reviews (
    id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    shop_id TEXT NOT NULL DEFAULT '',
    rating INT NOT NULL CHECK (rating >= 1 AND rating <= 5),
    comment TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_assistant_reviews_product_created
    ON assistant_reviews (product_id, created_at DESC);

CREATE TABLE IF NOT EXISTS assistant_review_summaries (
    product_id TEXT PRIMARY KEY,
    shop_id TEXT NOT NULL DEFAULT '',
    review_count INT NOT NULL DEFAULT 0,
    average_rating DOUBLE PRECISION NOT NULL DEFAULT 0,
    summary TEXT NOT NULL DEFAULT '',
    pros_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    cons_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS assistant_handoff_tickets (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    shop_id TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT '',
    chat_conversation_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_assistant_handoff_user_created
    ON assistant_handoff_tickets (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_assistant_handoff_shop_created
    ON assistant_handoff_tickets (shop_id, created_at DESC);

CREATE TABLE IF NOT EXISTS assistant_usage_logs (
    id TEXT PRIMARY KEY,
    trace_id TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL DEFAULT '',
    shop_id TEXT NOT NULL DEFAULT '',
    endpoint TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    input_tokens INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    cost_vnd BIGINT NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    ttft_ms BIGINT NOT NULL DEFAULT 0,
    cache_hit BOOLEAN NOT NULL DEFAULT FALSE,
    blocked_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_assistant_usage_logs_created
    ON assistant_usage_logs (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_assistant_usage_logs_user_created
    ON assistant_usage_logs (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS assistant_processed_events (
    event_id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
