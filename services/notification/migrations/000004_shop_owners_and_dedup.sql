CREATE TABLE IF NOT EXISTS shop_owners (
    shop_id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL,
    shop_name TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS notification_email_dedups (
    dedup_key TEXT PRIMARY KEY,
    event_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
