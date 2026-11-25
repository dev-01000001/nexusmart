-- Wave 3b-3 A8 (G2): Idempotent audit trail for COD shipping fee adjustments
-- prior to cash collection (`payment.method = 'COD'`, `payment.status = 'pending'`,
-- `allocation.escrow_status = 'awaiting_collection'`).
-- No `parent_order_id` or prepaid surcharge columns (explicitly out of scope per G2).

CREATE TABLE IF NOT EXISTS cod_fee_adjustments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key TEXT UNIQUE NOT NULL,
    payment_id UUID NOT NULL REFERENCES payments(id),
    order_id TEXT NOT NULL,
    old_shipping_fee_vnd BIGINT NOT NULL CHECK (old_shipping_fee_vnd >= 0),
    new_shipping_fee_vnd BIGINT NOT NULL CHECK (new_shipping_fee_vnd >= 0),
    new_order_payable_vnd BIGINT NOT NULL CHECK (new_order_payable_vnd >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cod_fee_adjustments_payment_id
    ON cod_fee_adjustments (payment_id);

CREATE INDEX IF NOT EXISTS idx_cod_fee_adjustments_order_id
    ON cod_fee_adjustments (order_id);
