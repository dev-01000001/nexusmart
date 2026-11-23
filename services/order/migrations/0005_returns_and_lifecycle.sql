-- Wave 2a: Order auto-cancel/auto-complete timestamps and full end-to-end return workflow.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS confirmed_at TIMESTAMPTZ;

UPDATE orders o
SET confirmed_at = COALESCE(
    (SELECT MIN(t.created_at) FROM order_timeline_events t WHERE t.order_id = o.id AND t.to_status = 'CONFIRMED'),
    o.updated_at
)
WHERE o.confirmed_at IS NULL
  AND o.status IN ('CONFIRMED', 'PREPARING', 'SHIPPING', 'DELIVERED', 'COMPLETED', 'RETURN_REQUESTED', 'RETURNED');

CREATE INDEX IF NOT EXISTS idx_orders_confirmed_timeout
    ON orders (COALESCE(confirmed_at, updated_at))
    WHERE status = 'CONFIRMED';

CREATE INDEX IF NOT EXISTS idx_orders_delivered_autocomplete
    ON orders (delivered_at)
    WHERE status = 'DELIVERED' AND delivered_at IS NOT NULL;

ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS order_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS shop_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS seller_response TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS seller_evidence_urls_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS dispute_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS admin_note TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refund_amount_vnd BIGINT NOT NULL DEFAULT 0 CHECK (refund_amount_vnd >= 0),
    ADD COLUMN IF NOT EXISTS return_tracking_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS return_label_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS return_fee_vnd BIGINT NOT NULL DEFAULT 0 CHECK (return_fee_vnd >= 0),
    ADD COLUMN IF NOT EXISTS return_fee_bearer TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS goods_received BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS should_restock BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS refund_done BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS promo_released BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS restock_done BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deadline_at TIMESTAMPTZ;

-- Deduplicate any pre-existing return_requests per order before adding UNIQUE(order_id).
DELETE FROM return_requests r
USING (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY order_id ORDER BY updated_at DESC, created_at DESC, id DESC) AS rn
    FROM return_requests
) d
WHERE r.id = d.id AND d.rn > 1;

-- Backfill existing return_requests: RETURN_REQUESTED orders keep PENDING with a fresh 3-day deadline;
-- already-RETURNED orders become REFUNDED; any other order state closes the return request.
UPDATE return_requests r
SET order_code = o.order_code,
    shop_id = o.shop_id,
    refund_amount_vnd = GREATEST(0, o.total_payable_vnd - o.refunded_vnd),
    status = CASE
        WHEN o.status = 'RETURN_REQUESTED' THEN 'PENDING'
        WHEN o.status = 'RETURNED' THEN 'REFUNDED'
        ELSE 'CLOSED'
    END,
    goods_received = CASE
        WHEN o.status = 'RETURNED' THEN TRUE
        ELSE r.goods_received
    END,
    deadline_at = CASE
        WHEN o.status = 'RETURN_REQUESTED' THEN NOW() + INTERVAL '3 days'
        ELSE NULL
    END,
    updated_at = NOW()
FROM orders o
WHERE o.id = r.order_id;

DROP INDEX IF EXISTS idx_return_requests_order_id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_return_requests_order_id_unique
    ON return_requests (order_id);

CREATE INDEX IF NOT EXISTS idx_return_requests_status_deadline
    ON return_requests (status, deadline_at)
    WHERE deadline_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_return_requests_status_updated
    ON return_requests (status, updated_at ASC);

CREATE INDEX IF NOT EXISTS idx_return_requests_buyer_id
    ON return_requests (buyer_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_return_requests_shop_id
    ON return_requests (shop_id, created_at DESC);

CREATE TABLE IF NOT EXISTS return_events (
    id TEXT PRIMARY KEY,
    return_request_id TEXT NOT NULL REFERENCES return_requests(id) ON DELETE CASCADE,
    from_status TEXT NOT NULL,
    to_status TEXT NOT NULL,
    actor_id TEXT NOT NULL DEFAULT '',
    actor_role TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_return_events_request_id
    ON return_events (return_request_id, created_at ASC, id ASC);

INSERT INTO return_events (id, return_request_id, from_status, to_status, actor_id, actor_role, note, created_at)
SELECT r.id || '-init', r.id, 'UNSPECIFIED', r.status, r.buyer_id, 'buyer', r.reason, r.created_at
FROM return_requests r
WHERE NOT EXISTS (
    SELECT 1 FROM return_events e WHERE e.return_request_id = r.id
);
