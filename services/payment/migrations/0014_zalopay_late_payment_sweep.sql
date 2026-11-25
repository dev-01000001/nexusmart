-- Wave 3b-2 p3 Item 1: Bounded-window multi-replica sweep for terminal unpaid ZALOPAY payments
-- (`superseded`, `expired`, `cancelled`) whose callbacks were lost or whose expiry `/v2/query`
-- failed due to transient network error / processing state.
--
-- Tracks definitive provider conclusion (`provider_final_status`), attempt counter, exponential
-- backoff schedule (`provider_next_check_at`), and last check timestamp, indexed strictly on
-- unsettled terminal ZALOPAY rows so normal queries incur zero overhead.

ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS provider_final_status TEXT NULL
        CHECK (provider_final_status IS NULL OR provider_final_status IN (
            'paid_confirmed',
            'late_refund_queued',
            'amount_mismatch',
            'not_paid',
            'manual_review'
        )),
    ADD COLUMN IF NOT EXISTS provider_check_attempts INTEGER NOT NULL DEFAULT 0
        CHECK (provider_check_attempts >= 0),
    ADD COLUMN IF NOT EXISTS provider_next_check_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS provider_last_checked_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_payments_zalopay_late_sweep
    ON payments (provider_next_check_at ASC NULLS FIRST, updated_at ASC)
    WHERE method = 'ZALOPAY'
      AND status IN ('superseded', 'expired', 'cancelled')
      AND provider_final_status IS NULL;
