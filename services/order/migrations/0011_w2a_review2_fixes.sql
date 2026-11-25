-- Wave 2a independent review round 2 fixes (P1-2, P1-3, P2-3, P2-4).

-- P1-3: Durable compensation flag + partial index for SHIPPING -> RETURNED (forward delivery failure).
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS pending_return_compensation BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_orders_pending_return_comp
    ON orders (updated_at ASC)
    WHERE status = 'RETURNED' AND pending_return_compensation = TRUE;

-- P2-4: Partial index for manual-review orphan shipments (UI/proto deferred to Wave 2b).
CREATE INDEX IF NOT EXISTS idx_orders_orphan_manual_review
    ON orders (updated_at ASC)
    WHERE orphan_shipment_needs_manual_review = TRUE;

-- P1-2(b): Track auto-escalation after max AWAITING_SHIPMENT deadline extensions so admin REJECT can close cleanly.
ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS escalated_from_cancel_failure BOOLEAN NOT NULL DEFAULT FALSE;

-- P2-3: Undo mistaken delivered_at backfill from 0007 on forward-delivery-failure orders (SHIPPING -> RETURNED)
-- that were never actually delivered (no DELIVERED timeline event and no post-delivery return_requests row).
UPDATE orders o
SET delivered_at = NULL
WHERE o.status = 'RETURNED'
  AND o.delivered_at IS NOT NULL
  AND NOT EXISTS (
      SELECT 1
      FROM order_timeline_events t
      WHERE t.order_id = o.id
        AND t.to_status = 'DELIVERED'
  )
  AND NOT EXISTS (
      SELECT 1
      FROM return_requests r
      WHERE r.order_id = o.id
  );
