-- Wave 2a independent review fixes (P0-2, P0-3, P1-1, P1-2, P1-3, P1-4, P2-5).

ALTER TABLE checkout_groups
    ADD COLUMN IF NOT EXISTS pending_stock_confirm BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_checkout_groups_pending_stock_confirm
    ON checkout_groups (updated_at ASC)
    WHERE pending_stock_confirm = TRUE;

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS pending_cancel_compensation BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS orphan_shipment_needs_manual_review BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_orders_pending_cancel_comp
    ON orders (updated_at ASC)
    WHERE status = 'CANCELLED' AND pending_cancel_compensation = TRUE;

-- P2-5: Backfill delivered_at from timeline (fallback updated_at) on legacy delivered/completed/returned rows.
UPDATE orders o
SET delivered_at = COALESCE(
    (SELECT MIN(t.created_at) FROM order_timeline_events t WHERE t.order_id = o.id AND t.to_status = 'DELIVERED'),
    o.updated_at
)
WHERE o.delivered_at IS NULL
  AND o.status IN ('DELIVERED', 'COMPLETED', 'RETURN_REQUESTED', 'RETURNED', 'RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING', 'REFUNDED');

ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS previous_return_trackings_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS deadline_extension_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS escalated_from_transit_timeout BOOLEAN NOT NULL DEFAULT FALSE;
