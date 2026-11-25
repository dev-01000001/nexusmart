-- Wave 2a review fixes:
-- 1. Durable retry flag on orders for orphaned forward shipment cancellation.
-- 2. Return shipment terminal flag on return_requests so AdminResolveReturn(REJECT)
--    only blocks on still-live RETURN shipments.
-- 3. Backfill deadlines on any existing IN_TRANSIT / AWAITING_SHIPMENT rows without deadline_at.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS pending_shipment_cancel BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_orders_pending_shipment_cancel
    ON orders (updated_at ASC)
    WHERE status = 'CANCELLED' AND pending_shipment_cancel = TRUE;

ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS return_shipment_terminal BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE return_requests
SET deadline_at = updated_at + INTERVAL '14 days'
WHERE status = 'IN_TRANSIT'
  AND deadline_at IS NULL;

UPDATE return_requests
SET deadline_at = updated_at + INTERVAL '5 days'
WHERE status = 'AWAITING_SHIPMENT'
  AND deadline_at IS NULL;
