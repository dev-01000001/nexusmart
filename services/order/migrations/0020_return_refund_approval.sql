-- Wave 3b-2 (A7: F11): Persist large-refund two-person approval metadata on return_requests
-- so watchdog retries preserve maker identity (refund_requested_by) and do not re-create
-- refunds while awaiting checker approval. Forward-only and safe on existing databases.

ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS require_approval_if_large BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS refund_requested_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refund_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refund_approval_status TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refund_attempt INT NOT NULL DEFAULT 0 CHECK (refund_attempt >= 0);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_return_requests_refund_approval_status'
          AND conrelid = 'return_requests'::regclass
    ) THEN
        ALTER TABLE return_requests
            ADD CONSTRAINT chk_return_requests_refund_approval_status
            CHECK (refund_approval_status IN ('', 'PENDING_APPROVAL', 'APPROVED', 'REJECTED'));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_return_requests_no_refund_done_while_unapproved'
          AND conrelid = 'return_requests'::regclass
    ) THEN
        ALTER TABLE return_requests
            ADD CONSTRAINT chk_return_requests_no_refund_done_while_unapproved
            CHECK (NOT (refund_done AND refund_approval_status IN ('PENDING_APPROVAL', 'REJECTED')));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_return_requests_no_refunded_status_while_unapproved'
          AND conrelid = 'return_requests'::regclass
    ) THEN
        ALTER TABLE return_requests
            ADD CONSTRAINT chk_return_requests_no_refunded_status_while_unapproved
            CHECK (NOT (status = 'REFUNDED' AND refund_approval_status IN ('PENDING_APPROVAL', 'REJECTED')));
    END IF;
END $$;
