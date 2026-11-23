-- Wave 1 payment money model (R1, R2, R3, R4):
-- 1. Seed platform_promo_expense (EXPENSE) account for platform-borne discounts (R1).
-- 2. Allow 0 VND payments (amount_vnd >= 0) for fully subsidized orders (R1).
-- 3. Store platform_discount_vnd on payment_shop_allocations and extend escrow_status
--    with awaiting_collection & cancelled for COD awaiting-collection lifecycle (R1, R2).
-- 4. Track is_late_payment on refunds so late/duplicate webhook refunds never reduce
--    order escrow caps or settlement math (R3, R4).
-- 5. Enforce at most ONE succeeded payment per order_group_id (R3 / UC-B25).

INSERT INTO ledger_accounts (id, account_type, shop_id)
VALUES ('platform_promo_expense', 'EXPENSE', NULL)
ON CONFLICT (id) DO NOTHING;

ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS payments_amount_vnd_check;

ALTER TABLE payments
    ADD CONSTRAINT payments_amount_vnd_check CHECK (amount_vnd >= 0);

DO $$
DECLARE
    v_dup_groups TEXT;
BEGIN
    SELECT string_agg(order_group_id, ', ' ORDER BY order_group_id)
    INTO v_dup_groups
    FROM (
        SELECT order_group_id
        FROM payments
        WHERE status = 'succeeded'
        GROUP BY order_group_id
        HAVING COUNT(*) > 1
    ) dups;

    IF v_dup_groups IS NULL THEN
        CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_one_succeeded_per_group
            ON payments (order_group_id) WHERE status = 'succeeded';
    ELSE
        RAISE WARNING 'Skipping idx_payments_one_succeeded_per_group: duplicate succeeded order_group_id(s) found for manual reconciliation: %', v_dup_groups;
    END IF;
END
$$;

ALTER TABLE payment_shop_allocations
    ADD COLUMN IF NOT EXISTS platform_discount_vnd BIGINT NOT NULL DEFAULT 0
    CHECK (platform_discount_vnd >= 0);

ALTER TABLE payment_shop_allocations
    DROP CONSTRAINT IF EXISTS payment_shop_allocations_escrow_status_check;

ALTER TABLE payment_shop_allocations
    ADD CONSTRAINT payment_shop_allocations_escrow_status_check
    CHECK (escrow_status IN ('awaiting_collection', 'held', 'pending_release', 'released', 'refunded', 'cancelled'));

ALTER TABLE refunds
    ADD COLUMN IF NOT EXISTS is_late_payment BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE refunds
SET is_late_payment = TRUE
WHERE is_late_payment = FALSE
  AND (idempotency_key LIKE 'late-%' OR reason = 'late_payment');
