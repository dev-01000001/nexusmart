-- 0004: Extended return requests, reverse shipping, complaints, and ZaloPay online expiry index.

ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS shop_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS items_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS seller_note TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reverse_tracking_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reverse_label_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refund_amount_vnd BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS refund_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_return_requests_shop_id
    ON return_requests (shop_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_return_requests_status
    ON return_requests (status, created_at DESC);

CREATE TABLE IF NOT EXISTS complaints (
    id TEXT PRIMARY KEY,
    return_request_id TEXT NOT NULL UNIQUE REFERENCES return_requests(id) ON DELETE CASCADE,
    order_id TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    buyer_id TEXT NOT NULL,
    shop_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    evidence_urls_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'OPEN',
    resolution TEXT NOT NULL DEFAULT '',
    admin_note TEXT NOT NULL DEFAULT '',
    resolved_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_complaints_status
    ON complaints (status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_complaints_order_id
    ON complaints (order_id);

CREATE INDEX IF NOT EXISTS idx_complaints_shop_id
    ON complaints (shop_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_orders_pending_online_expiry
    ON orders (payment_expires_at)
    WHERE status = 'PENDING' AND payment_method IN ('VIETQR', 'ZALOPAY');
