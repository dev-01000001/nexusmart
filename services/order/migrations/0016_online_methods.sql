-- Wave 3b-2 (A6: F3/F5/F6): Online payment methods ('VIETQR', 'ZALOPAY') and checkout_groups.pay_url.
-- Forward-only, safe on databases with existing COD/VIETQR rows.

ALTER TABLE checkout_groups
    ADD COLUMN IF NOT EXISTS pay_url TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_checkout_groups_payment_method'
          AND conrelid = 'checkout_groups'::regclass
    ) THEN
        ALTER TABLE checkout_groups
            ADD CONSTRAINT chk_checkout_groups_payment_method
            CHECK (payment_method IN ('COD', 'VIETQR', 'ZALOPAY'));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_orders_payment_method'
          AND conrelid = 'orders'::regclass
    ) THEN
        ALTER TABLE orders
            ADD CONSTRAINT chk_orders_payment_method
            CHECK (payment_method IN ('COD', 'VIETQR', 'ZALOPAY'));
    END IF;
END $$;

DROP INDEX IF EXISTS idx_orders_pending_expiry;
CREATE INDEX idx_orders_pending_expiry
    ON orders (payment_expires_at)
    WHERE status = 'PENDING' AND payment_method IN ('VIETQR', 'ZALOPAY');
