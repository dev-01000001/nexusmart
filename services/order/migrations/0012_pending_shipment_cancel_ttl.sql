-- Wave 2a round 6 P1 fix: TTL timestamp + attempt token for pending_shipment_cancel.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS pending_shipment_cancel_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pending_shipment_cancel_token TEXT NOT NULL DEFAULT '';

-- Backfill existing active pending_shipment_cancel rows with updated_at so age checks work immediately.
UPDATE orders
SET pending_shipment_cancel_at = updated_at
WHERE pending_shipment_cancel = TRUE
  AND pending_shipment_cancel_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_orders_active_pending_shipment_cancel
    ON orders (pending_shipment_cancel_at ASC)
    WHERE status IN ('CONFIRMED', 'PREPARING') AND pending_shipment_cancel = TRUE;
