CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY,
    user_id TEXT NOT NULL,
    category TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    link TEXT NOT NULL DEFAULT '',
    event_id TEXT NOT NULL DEFAULT '',
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_created ON notifications(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_user_unread ON notifications(user_id) WHERE NOT is_read;

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id TEXT PRIMARY KEY,
    order_in_app BOOLEAN NOT NULL DEFAULT TRUE,
    order_email BOOLEAN NOT NULL DEFAULT TRUE,
    order_push BOOLEAN NOT NULL DEFAULT TRUE,
    promotion_in_app BOOLEAN NOT NULL DEFAULT TRUE,
    promotion_email BOOLEAN NOT NULL DEFAULT TRUE,
    promotion_push BOOLEAN NOT NULL DEFAULT TRUE,
    system_in_app BOOLEAN NOT NULL DEFAULT TRUE,
    system_email BOOLEAN NOT NULL DEFAULT TRUE,
    system_push BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS processed_events (
    event_id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dead_letter_events (
    id UUID PRIMARY KEY,
    event_id TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL,
    payload BYTEA NOT NULL,
    error_message TEXT NOT NULL,
    attempts INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_contacts (
    user_id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    full_name TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS order_participants (
    order_id TEXT PRIMARY KEY,
    buyer_id TEXT NOT NULL,
    shop_id TEXT NOT NULL DEFAULT '',
    order_code TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
