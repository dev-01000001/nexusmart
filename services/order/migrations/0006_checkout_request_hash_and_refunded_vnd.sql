-- Add canonical checkout request hash (ORD-B4), cumulative refunded_vnd per sub-order (PAY-B6),
-- and a partial index on checkout_groups(payment_id) for RetryPayment lookup (ORD-B5).

ALTER TABLE checkout_groups
    ADD COLUMN IF NOT EXISTS request_hash TEXT NOT NULL DEFAULT '';

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS refunded_vnd BIGINT NOT NULL DEFAULT 0 CHECK (refunded_vnd >= 0);

CREATE INDEX IF NOT EXISTS idx_checkout_groups_payment_id
    ON checkout_groups (payment_id)
    WHERE payment_id <> '';
