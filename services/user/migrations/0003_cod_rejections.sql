-- 0003_cod_rejections.sql: Idempotent tracking of COD delivery rejections per order_id (R6)
-- and index for active shop pickup address deletion guard (USR-B6).

CREATE TABLE IF NOT EXISTS cod_rejections (
    order_id TEXT PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cod_rejections_user_id ON cod_rejections (user_id);

CREATE INDEX IF NOT EXISTS idx_shops_active_pickup_address
    ON shops (pickup_address_id)
    WHERE status = 'ACTIVE' AND pickup_address_id <> '';
