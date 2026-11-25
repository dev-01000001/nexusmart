-- NexusMart Payment Service: ZaloPay online payment method support
-- Additive & backward-compatible: widens method CHECK constraint to allow ZALOPAY
-- and adds partial expiry index covering all online payment methods (VIETQR, ZALOPAY).

ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS payments_method_check,
    DROP CONSTRAINT IF EXISTS chk_payments_method;

ALTER TABLE payments
    ADD CONSTRAINT chk_payments_method
        CHECK (method IN ('COD', 'VIETQR', 'ZALOPAY'));

CREATE INDEX IF NOT EXISTS idx_payments_pending_online_expiry
    ON payments (expires_at)
    WHERE status = 'pending' AND method IN ('VIETQR', 'ZALOPAY');
