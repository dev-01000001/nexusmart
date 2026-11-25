-- Wave 3b-1 (E18, E19): Async shipment creation job status on orders and indexed COD rejection rate lookup.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS shipment_job_status TEXT NOT NULL DEFAULT '';

-- E19: Single indexed query for buyer terminal COD order rejection rate (status IN ('COMPLETED', 'RETURNED')).
-- Covers (buyer_id, status) with partial predicate payment_method = 'COD'; INCLUDE (delivered_at) allows
-- distinguishing delivery refusal (delivered_at IS NULL) from post-delivery returns via index-only scan.
CREATE INDEX IF NOT EXISTS idx_orders_buyer_cod_status
    ON orders (buyer_id, status)
    INCLUDE (delivered_at)
    WHERE payment_method = 'COD';
