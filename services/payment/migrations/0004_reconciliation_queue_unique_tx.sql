-- Wave 2a review fixes (P1 double-accounting guard, P2 cross-midnight reconciliation, P3 idempotency param checks):
-- 1. Safely enforce at most one reconciliation_queue row per provider_tx_id (skipping only if legacy duplicates exist).
-- 2. Record resolution_action and resolution_payment_id on reconciliation_queue for strict idempotency replay validation.
-- 3. Index payments(paid_at) for VietQR VN-day missing_provider queries.

DO $$
DECLARE
    v_dup_txs TEXT;
BEGIN
    SELECT string_agg(provider_tx_id, ', ' ORDER BY provider_tx_id)
    INTO v_dup_txs
    FROM (
        SELECT provider_tx_id
        FROM reconciliation_queue
        GROUP BY provider_tx_id
        HAVING COUNT(*) > 1
    ) dups;

    IF v_dup_txs IS NULL THEN
        CREATE UNIQUE INDEX IF NOT EXISTS idx_reconciliation_queue_unique_provider_tx_id
            ON reconciliation_queue (provider_tx_id);
    ELSE
        RAISE WARNING 'Skipping idx_reconciliation_queue_unique_provider_tx_id: duplicate provider_tx_id(s) found: %', v_dup_txs;
    END IF;
END
$$;

ALTER TABLE reconciliation_queue
    ADD COLUMN IF NOT EXISTS resolution_action TEXT NULL,
    ADD COLUMN IF NOT EXISTS resolution_payment_id UUID NULL REFERENCES payments(id);

CREATE INDEX IF NOT EXISTS idx_payments_vietqr_paid_at
    ON payments (paid_at)
    WHERE method = 'VIETQR' AND paid_at IS NOT NULL;
