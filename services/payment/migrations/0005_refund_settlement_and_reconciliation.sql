-- Wave 2a payment operations (PAY-B7, PAY-B9, PAY-B11):
-- 1. Two-step refunds: buyer receiving bank account fields, settlement tracking,
--    idempotency key, and affects_escrow flag (so late/overpayment/reconciliation
--    refunds booked directly from gateway_clearing never reduce order escrow caps).
-- 2. Backfill existing 'completed' refunds that have no 'refund_settle' ledger
--    transaction and no provider_refund_id back to 'pending' so they can be properly
--    settled. Refunds with provider_refund_id IS NOT NULL (e.g. already disbursed
--    synchronously via ZaloPay on phase-0b) are preserved as 'completed' to prevent
--    double refunds.
-- 3. Reconciliation queue resolution idempotency and refund linkage.
-- 4. Partial index for the 60s seller escrow hold release scanner.

ALTER TABLE refunds
    ADD COLUMN IF NOT EXISTS bank_code TEXT NULL,
    ADD COLUMN IF NOT EXISTS bank_account_number TEXT NULL,
    ADD COLUMN IF NOT EXISTS bank_account_masked TEXT NULL,
    ADD COLUMN IF NOT EXISTS bank_account_name TEXT NULL,
    ADD COLUMN IF NOT EXISTS settled_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS settlement_note TEXT NULL,
    ADD COLUMN IF NOT EXISTS settlement_idempotency_key TEXT NULL UNIQUE,
    ADD COLUMN IF NOT EXISTS affects_escrow BOOLEAN NOT NULL DEFAULT TRUE;

UPDATE refunds
SET affects_escrow = FALSE
WHERE is_late_payment = TRUE
  AND affects_escrow = TRUE;

UPDATE refunds r
SET status = 'pending',
    settled_at = NULL,
    updated_at = NOW()
WHERE r.status = 'completed'
  AND r.provider_refund_id IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM ledger_transactions lt
      WHERE lt.reference_type = 'refund_settle'
        AND lt.reference_id = r.id::TEXT
  );

UPDATE refunds r
SET settled_at = COALESCE(r.settled_at, r.updated_at)
WHERE r.status = 'completed'
  AND r.settled_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_refunds_status_created_at
    ON refunds (status, created_at DESC);

ALTER TABLE reconciliation_queue
    ADD COLUMN IF NOT EXISTS resolution_idempotency_key TEXT NULL UNIQUE,
    ADD COLUMN IF NOT EXISTS resolution_refund_id UUID NULL REFERENCES refunds(id);

CREATE INDEX IF NOT EXISTS idx_payment_allocations_pending_release
    ON payment_shop_allocations (settled_at)
    WHERE escrow_status = 'pending_release';
