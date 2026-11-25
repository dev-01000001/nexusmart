-- Wave 3b-2 p4:
-- 1. Item B (F4): Lease token & expiry on `refunds` for multi-replica CAS safety across ZaloPay
--    wallet refund dispatch/poll calls (lease > worst-case HTTP timeout).
-- 2. Item D (F4): Track whether `/v2/refund` dispatch was actually attempted so GMT+7 midnight
--    rollover rotates `provider_m_refund_id` before first send, or strictly after `query_refund`
--    confirms non-existence (`-101`).
-- 3. Item E (F11): Persist `created_by_service` so `order-cancel-refund:` amount-check exemption
--    applies strictly to refunds created by trusted service identity / internal cancel consumer.

ALTER TABLE refunds
    ADD COLUMN IF NOT EXISTS provider_lease_token UUID NULL,
    ADD COLUMN IF NOT EXISTS provider_lease_until TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS provider_dispatch_attempted BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS created_by_service BOOLEAN NOT NULL DEFAULT FALSE;

-- Preserve pre-0013 automatic order-cancel refunds (created without a human maker).
UPDATE refunds
SET created_by_service = TRUE
WHERE created_by_service = FALSE
  AND LOWER(BTRIM(idempotency_key)) LIKE 'order-cancel-refund:%'
  AND (requested_by IS NULL OR BTRIM(requested_by) = '');

CREATE INDEX IF NOT EXISTS idx_refunds_zalopay_poll_lease
    ON refunds (provider_lease_until ASC NULLS FIRST, updated_at ASC)
    WHERE status = 'pending'
      AND provider_m_refund_id IS NOT NULL
      AND provider_status IN ('pending', 'processing');
