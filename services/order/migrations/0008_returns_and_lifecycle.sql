-- Wave 2a (unified with phase-0b 0004_returns_and_complaints.sql):
-- Order auto-cancel/auto-complete timestamps, full 10-state return workflow,
-- non-destructive migration of phase-0b return_requests/complaints/orders statuses,
-- and CHECK constraints enforced via DO $$ (safe when columns already exist from 0004).

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS confirmed_at TIMESTAMPTZ;

ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS order_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS shop_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS seller_response TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS seller_evidence_urls_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS dispute_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS admin_note TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refund_amount_vnd BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS return_tracking_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS return_label_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS return_fee_vnd BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS return_fee_bearer TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS goods_received BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS should_restock BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS refund_done BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS promo_released BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS restock_done BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deadline_at TIMESTAMPTZ;

-- Enforce non-negative CHECK constraints explicitly via DO $$ because ADD COLUMN IF NOT EXISTS
-- silently skips inline CHECK constraints when refund_amount_vnd already exists from 0004.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_return_requests_refund_amount_nonneg'
          AND conrelid = 'return_requests'::regclass
    ) THEN
        ALTER TABLE return_requests
            ADD CONSTRAINT chk_return_requests_refund_amount_nonneg
            CHECK (refund_amount_vnd >= 0);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_return_requests_return_fee_nonneg'
          AND conrelid = 'return_requests'::regclass
    ) THEN
        ALTER TABLE return_requests
            ADD CONSTRAINT chk_return_requests_return_fee_nonneg
            CHECK (return_fee_vnd >= 0);
    END IF;
END $$;

-- Preserve historical complaints (table kept read-only; never dropped) by copying reason/admin_note
-- onto return_requests BEFORE deduplicating return_requests.
UPDATE return_requests r
SET dispute_reason = CASE
        WHEN r.dispute_reason = '' THEN COALESCE(
            NULLIF((SELECT c.reason FROM complaints c WHERE c.return_request_id = r.id LIMIT 1), ''),
            NULLIF((SELECT c.reason FROM complaints c WHERE c.order_id = r.order_id ORDER BY c.created_at DESC, c.id DESC LIMIT 1), ''),
            ''
        )
        ELSE r.dispute_reason
    END,
    admin_note = CASE
        WHEN r.admin_note = '' THEN COALESCE(
            NULLIF((SELECT c.admin_note FROM complaints c WHERE c.return_request_id = r.id LIMIT 1), ''),
            NULLIF((SELECT c.admin_note FROM complaints c WHERE c.order_id = r.order_id ORDER BY c.created_at DESC, c.id DESC LIMIT 1), ''),
            ''
        )
        ELSE r.admin_note
    END
WHERE EXISTS (
    SELECT 1 FROM complaints c WHERE c.return_request_id = r.id OR c.order_id = r.order_id
);

-- Drop FK ON DELETE CASCADE before deduplicating return_requests so legacy complaints history rows are never deleted.
ALTER TABLE complaints DROP CONSTRAINT IF EXISTS complaints_return_request_id_fkey;

-- Deduplicate any pre-existing return_requests per order before creating UNIQUE(order_id).
-- Prefer keeping an already-REFUNDED row first so completed refunds are never dropped.
DELETE FROM return_requests r
USING (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY order_id
               ORDER BY (status = 'REFUNDED') DESC, updated_at DESC, created_at DESC, id DESC
           ) AS rn
    FROM return_requests
) d
WHERE r.id = d.id AND d.rn > 1;

-- Backfill return_requests and map phase-0b statuses (PENDING, APPROVED, RETURNING, RETURNED,
-- REJECTED, ESCALATED, REFUNDED) to main's 10-state ReturnStatus workflow without closing unknown rows.
-- Safety guards applied before active return branches:
--   1. Terminal orders (COMPLETED, CANCELLED) and non-return-flow orders with non-REFUNDED returns
--      are closed (CLOSED, deadline_at = NULL) so watchdog never loops on invalid transitions or auto-acts.
--   2. REJECTED returns on live RETURN_REJECTED orders with an OPEN complaint map to ESCALATED
--      (deadline_at = NULL) so they remain visible in the admin queue instead of auto-closing after 3 days.
-- Preserves existing refund_amount_vnd > 0 (partial refunds), copies seller_note/reverse_tracking_code/
-- reverse_label_url, and marks REFUNDED rows with refund_done/promo_released/restock_done/goods_received.
UPDATE return_requests r
SET order_code = CASE WHEN r.order_code = '' THEN o.order_code ELSE r.order_code END,
    shop_id = CASE WHEN r.shop_id = '' THEN o.shop_id ELSE r.shop_id END,
    seller_response = CASE WHEN r.seller_response = '' THEN r.seller_note ELSE r.seller_response END,
    return_tracking_code = CASE WHEN r.return_tracking_code = '' THEN r.reverse_tracking_code ELSE r.return_tracking_code END,
    return_label_url = CASE WHEN r.return_label_url = '' THEN r.reverse_label_url ELSE r.return_label_url END,
    refund_amount_vnd = CASE
        WHEN r.refund_amount_vnd > 0 THEN r.refund_amount_vnd
        ELSE GREATEST(0, o.total_payable_vnd - o.refunded_vnd)
    END,
    status = CASE
        WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' THEN 'REFUNDED'
        WHEN o.status IN ('COMPLETED', 'CANCELLED') THEN 'CLOSED'
        WHEN o.status NOT IN ('RETURN_REQUESTED', 'RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING', 'RETURNED', 'REFUNDED') THEN 'CLOSED'
        WHEN (r.status = 'REJECTED' OR (r.status = 'PENDING' AND o.status = 'RETURN_REJECTED'))
            AND EXISTS (
                SELECT 1 FROM complaints c
                WHERE (c.return_request_id = r.id OR c.order_id = r.order_id)
                  AND c.status = 'OPEN'
            ) THEN 'ESCALATED'
        WHEN r.status = 'APPROVED' THEN 'AWAITING_SHIPMENT'
        WHEN r.status = 'RETURNING' THEN 'IN_TRANSIT'
        WHEN r.status = 'RETURNED' THEN 'RECEIVED'
        WHEN r.status = 'REJECTED' THEN 'REJECTED'
        WHEN r.status = 'ESCALATED' THEN 'ESCALATED'
        WHEN r.status = 'PENDING' AND o.status = 'RETURNED' THEN 'REFUNDED'
        WHEN r.status = 'PENDING' AND o.status = 'RETURN_APPROVED' THEN 'AWAITING_SHIPMENT'
        WHEN r.status = 'PENDING' AND o.status = 'RETURNING' THEN 'IN_TRANSIT'
        WHEN r.status = 'PENDING' AND o.status = 'RETURN_REJECTED' THEN 'REJECTED'
        WHEN r.status = 'PENDING' THEN 'PENDING'
        ELSE r.status
    END,
    goods_received = CASE
        WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' THEN TRUE
        WHEN o.status IN ('COMPLETED', 'CANCELLED') THEN r.goods_received
        WHEN o.status NOT IN ('RETURN_REQUESTED', 'RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING', 'RETURNED', 'REFUNDED') THEN r.goods_received
        WHEN r.status = 'RETURNED' OR o.status = 'RETURNED' THEN TRUE
        ELSE r.goods_received
    END,
    refund_done = CASE
        WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' OR (r.status = 'PENDING' AND o.status = 'RETURNED') THEN TRUE
        ELSE r.refund_done
    END,
    promo_released = CASE
        WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' OR (r.status = 'PENDING' AND o.status = 'RETURNED') THEN TRUE
        ELSE r.promo_released
    END,
    restock_done = CASE
        WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' OR (r.status = 'PENDING' AND o.status = 'RETURNED') THEN TRUE
        ELSE r.restock_done
    END,
    deadline_at = CASE
        WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' OR (r.status = 'PENDING' AND o.status = 'RETURNED') THEN NULL
        WHEN o.status IN ('COMPLETED', 'CANCELLED') THEN NULL
        WHEN o.status NOT IN ('RETURN_REQUESTED', 'RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING', 'RETURNED', 'REFUNDED') THEN NULL
        WHEN (r.status = 'REJECTED' OR (r.status = 'PENDING' AND o.status = 'RETURN_REJECTED'))
            AND EXISTS (
                SELECT 1 FROM complaints c
                WHERE (c.return_request_id = r.id OR c.order_id = r.order_id)
                  AND c.status = 'OPEN'
            ) THEN NULL
        WHEN r.status = 'APPROVED' OR (r.status = 'PENDING' AND o.status = 'RETURN_APPROVED') THEN COALESCE(r.deadline_at, NOW() + INTERVAL '5 days')
        WHEN r.status = 'RETURNING' OR (r.status = 'PENDING' AND o.status = 'RETURNING') THEN COALESCE(r.deadline_at, NOW() + INTERVAL '14 days')
        WHEN r.status = 'RETURNED' THEN COALESCE(r.deadline_at, NOW() + INTERVAL '3 days')
        WHEN r.status = 'REJECTED' OR (r.status = 'PENDING' AND o.status = 'RETURN_REJECTED') THEN COALESCE(r.deadline_at, NOW() + INTERVAL '3 days')
        WHEN r.status = 'ESCALATED' THEN NULL
        WHEN r.status = 'PENDING' THEN COALESCE(r.deadline_at, NOW() + INTERVAL '3 days')
        ELSE r.deadline_at
    END,
    updated_at = NOW()
FROM orders o
WHERE o.id = r.order_id;

-- Anti-double-refund sync: record orders.refunded_vnd for all already-REFUNDED returns/orders.
UPDATE orders o
SET refunded_vnd = GREATEST(o.refunded_vnd, r.refund_amount_vnd),
    updated_at = NOW()
FROM return_requests r
WHERE r.order_id = o.id
  AND r.status = 'REFUNDED';

UPDATE orders o
SET refunded_vnd = GREATEST(o.refunded_vnd, o.total_payable_vnd),
    updated_at = NOW()
WHERE o.status = 'REFUNDED'
  AND o.refunded_vnd = 0
  AND NOT EXISTS (
      SELECT 1 FROM return_requests r WHERE r.order_id = o.id
  );

-- Normalize phase-0b orders.status into main's canonical OrderStatus values before confirmed_at backfill:
--   RETURN_APPROVED / RETURN_REJECTED / RETURNING -> RETURN_REQUESTED
--   REFUNDED -> RETURNED
--   RETURNED with an un-refunded RECEIVED return request -> RETURN_REQUESTED (so seller/admin can finalize refund).
UPDATE orders o
SET status = CASE
        WHEN o.status IN ('RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING') THEN 'RETURN_REQUESTED'
        WHEN o.status = 'REFUNDED' THEN 'RETURNED'
        WHEN o.status = 'RETURNED' AND EXISTS (
            SELECT 1 FROM return_requests r WHERE r.order_id = o.id AND r.status = 'RECEIVED'
        ) THEN 'RETURN_REQUESTED'
        ELSE o.status
    END,
    updated_at = NOW()
WHERE o.status IN ('RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING', 'REFUNDED')
   OR (o.status = 'RETURNED' AND EXISTS (
       SELECT 1 FROM return_requests r WHERE r.order_id = o.id AND r.status = 'RECEIVED'
   ));

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
