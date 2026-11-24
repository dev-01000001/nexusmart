-- 006_shop_suspension_state.sql:
-- Wave 3b-2 (F12): Track per-shop suspension state consumed from user.shop_status_changed
-- with monotonic version gating so stale out-of-order events never overwrite newer state.

CREATE TABLE IF NOT EXISTS shop_suspension_state (
    shop_id TEXT PRIMARY KEY,
    suspended BOOLEAN NOT NULL DEFAULT FALSE,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shop_suspension_state_suspended
    ON shop_suspension_state (shop_id)
    WHERE suspended = TRUE;
