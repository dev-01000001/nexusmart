-- Wave 3b-2 A6 (Phase 1): ZaloPay payment method support & payment_method_configs (UC-A13, F4, F6, F7)
-- 1. Extend CHECK constraints on `payments.method` and `payment_fee_configs.payment_method`
--    to include `'ZALOPAY'`, and seed default 0 bps fee config for `'ZALOPAY'`.
-- 2. Add `payments.pay_url`, rebuild `idx_payments_pending_expiry` to include `'ZALOPAY'`.
-- 3. Add `refunds.provider_m_refund_id` (UNIQUE) and `refunds.provider_status` with partial
--    polling index for async provider refund status checks (F4).
-- 4. Create `payment_method_configs` (single source of truth for enabled, min/max order VND,
--    expiry_minutes, and optimistic locking `version BIGINT NOT NULL DEFAULT 1` per F6/F7)
--    and seed COD (max 5,000,000 VND, expiry 0), VIETQR (unlimited, expiry 15), ZALOPAY (unlimited, expiry 15).

ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS payments_method_check;

ALTER TABLE payments
    ADD CONSTRAINT payments_method_check
    CHECK (method IN ('COD', 'VIETQR', 'ZALOPAY'));

ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS pay_url TEXT NULL;

DROP INDEX IF EXISTS idx_payments_pending_expiry;

CREATE INDEX idx_payments_pending_expiry
    ON payments (expires_at)
    WHERE status = 'pending' AND method IN ('VIETQR', 'ZALOPAY');

ALTER TABLE refunds
    ADD COLUMN IF NOT EXISTS provider_m_refund_id TEXT NULL UNIQUE,
    ADD COLUMN IF NOT EXISTS provider_status TEXT NULL
        CHECK (provider_status IS NULL OR provider_status IN ('pending', 'processing', 'succeeded', 'failed'));

CREATE INDEX IF NOT EXISTS idx_refunds_provider_polling
    ON refunds (updated_at ASC)
    WHERE provider_m_refund_id IS NOT NULL
      AND provider_status IN ('pending', 'processing');

ALTER TABLE payment_fee_configs
    DROP CONSTRAINT IF EXISTS payment_fee_configs_payment_method_check;

ALTER TABLE payment_fee_configs
    ADD CONSTRAINT payment_fee_configs_payment_method_check
    CHECK (payment_method IN ('COD', 'VIETQR', 'ZALOPAY'));

INSERT INTO payment_fee_configs (payment_method, fee_bps, effective_from, created_by, note)
SELECT 'ZALOPAY', 0, TIMESTAMPTZ '2020-01-01 00:00:00+00', 'system', 'Default baseline payment fee (0 bps)'
WHERE NOT EXISTS (
    SELECT 1 FROM payment_fee_configs WHERE payment_method = 'ZALOPAY'
);

CREATE TABLE IF NOT EXISTS payment_method_configs (
    method TEXT PRIMARY KEY CHECK (method IN ('COD', 'VIETQR', 'ZALOPAY')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    min_order_vnd BIGINT NOT NULL DEFAULT 0 CHECK (min_order_vnd >= 0),
    max_order_vnd BIGINT NOT NULL DEFAULT 0 CHECK (max_order_vnd >= 0),
    expiry_minutes INTEGER NOT NULL DEFAULT 15 CHECK (expiry_minutes >= 0 AND expiry_minutes <= 1440),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    note TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_payment_method_configs_order_bounds
        CHECK (max_order_vnd = 0 OR max_order_vnd >= min_order_vnd)
);

INSERT INTO payment_method_configs (
    method, enabled, min_order_vnd, max_order_vnd, expiry_minutes, version, note, updated_by
)
VALUES
    ('COD', TRUE, 0, 5000000, 0, 1, 'Default COD configuration (max 5,000,000 VND)', 'system'),
    ('VIETQR', TRUE, 0, 0, 15, 1, 'Default VietQR configuration (15 min expiry)', 'system'),
    ('ZALOPAY', TRUE, 0, 0, 15, 1, 'Default ZaloPay configuration (15 min expiry)', 'system')
ON CONFLICT (method) DO NOTHING;
