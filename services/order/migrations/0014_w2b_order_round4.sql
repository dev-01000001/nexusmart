-- Wave 2b Round 4:
-- 1. Snapshot pre-settlement refunded amount on order completion so reports match payment's compute_settlement_split waterfall.
-- 2. Track pre-persist compensation failures on IN_PROGRESS checkout groups so ScanStaleCheckoutGroups recovers them immediately.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS settlement_refunded_vnd BIGINT NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_orders_settlement_refunded_nonneg'
          AND conrelid = 'orders'::regclass
    ) THEN
        ALTER TABLE orders
            ADD CONSTRAINT chk_orders_settlement_refunded_nonneg CHECK (settlement_refunded_vnd >= 0);
    END IF;
END $$;

-- Backfill legacy COMPLETED orders: treat existing refunded_vnd as pre-settlement refund
-- so historical reports align with payment escrow settlement waterfall instead of producing
-- negative seller net or overstated fees when pre-settlement partial refunds existed.
UPDATE orders
SET settlement_refunded_vnd = refunded_vnd
WHERE status = 'COMPLETED'
  AND settlement_refunded_vnd = 0
  AND refunded_vnd > 0;

ALTER TABLE checkout_groups
    ADD COLUMN IF NOT EXISTS needs_cleanup BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_checkout_groups_in_progress_updated_at
    ON checkout_groups (updated_at)
    WHERE status = 'IN_PROGRESS';
