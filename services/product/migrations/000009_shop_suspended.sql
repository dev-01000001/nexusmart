-- 000009_shop_suspended.sql:
-- F12: Per-shop suspension flag on products (preserves seller products.status),
-- monotonic shop_suspension_state, and consumer idempotency / DLQ tables (E1/F12).

ALTER TABLE products
    ADD COLUMN IF NOT EXISTS shop_suspended BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_products_public_visible
    ON products (created_at DESC, id DESC)
    WHERE deleted_at IS NULL AND shop_suspended = FALSE AND status IN ('APPROVED', 'OUT_OF_STOCK');

CREATE INDEX IF NOT EXISTS idx_products_shop_suspended
    ON products (shop_id)
    WHERE deleted_at IS NULL AND shop_suspended = TRUE;

CREATE TABLE IF NOT EXISTS shop_suspension_state (
    shop_id TEXT PRIMARY KEY,
    suspended BOOLEAN NOT NULL DEFAULT FALSE,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS processed_events (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL DEFAULT '',
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dead_letter_events (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    event_id TEXT NOT NULL DEFAULT '',
    payload BYTEA NOT NULL,
    error_message TEXT NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
