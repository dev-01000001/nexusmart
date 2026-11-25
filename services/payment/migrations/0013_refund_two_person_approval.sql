-- Wave 3b-2 A7 (F11): Two-person approval (Maker-Checker) for large manual & return refunds.
-- 1. Extend `refunds.status` CHECK to include `'pending_approval'` and `'rejected'`.
-- 2. Add maker/checker metadata columns (`requested_by`, `approved_by`, `approved_at`, `approval_note`)
--    and DB-level CHECK enforcing `approved_by <> requested_by`.
-- 3. Add partial indexes for the pending approval queue and the 24-hour per-payment manual refund sum.

ALTER TABLE refunds
    DROP CONSTRAINT IF EXISTS refunds_status_check;

ALTER TABLE refunds
    ADD CONSTRAINT refunds_status_check
    CHECK (status IN (
        'pending_approval',
        'pending',
        'completed',
        'failed',
        'manual_review',
        'rejected'
    ));

ALTER TABLE refunds
    ADD COLUMN IF NOT EXISTS requested_by TEXT NULL,
    ADD COLUMN IF NOT EXISTS approved_by TEXT NULL,
    ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS approval_note TEXT NULL;

ALTER TABLE refunds
    DROP CONSTRAINT IF EXISTS chk_refunds_maker_ne_checker;

ALTER TABLE refunds
    ADD CONSTRAINT chk_refunds_maker_ne_checker
    CHECK (approved_by IS NULL OR requested_by IS NULL OR approved_by <> requested_by);

CREATE INDEX IF NOT EXISTS idx_refunds_pending_approval
    ON refunds (created_at DESC)
    WHERE status = 'pending_approval';

CREATE INDEX IF NOT EXISTS idx_refunds_payment_manual_24h
    ON refunds (payment_id, created_at DESC)
    WHERE requested_by IS NOT NULL
      AND requested_by <> ''
      AND NOT is_late_payment;
