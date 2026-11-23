-- Review-A P0-3: Track latest retry idempotency key and superseded payment_id so sequential
-- RetryPayment calls with the same Idempotency-Key return the new payment idempotently.
ALTER TABLE checkout_groups
    ADD COLUMN IF NOT EXISTS retry_idempotency_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS retry_from_payment_id TEXT NOT NULL DEFAULT '';
