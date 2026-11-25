-- Wave 2b payment fee configs (UC-A09) & seller ledger pagination index (UC-S17):
-- 1. Append-only immutable payment_fee_configs table with effective_from and deterministic seq tie-breaker.
-- 2. Seed default 0 bps baseline rows for COD and VIETQR effective from 2020-01-01T00:00:00Z.
-- 3. Composite index on ledger_entries(account_id, created_at DESC, id DESC) for paginated seller ledger queries.

CREATE TABLE IF NOT EXISTS payment_fee_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seq BIGSERIAL NOT NULL,
    payment_method TEXT NOT NULL CHECK (payment_method IN ('COD', 'VIETQR')),
    fee_bps INTEGER NOT NULL CHECK (fee_bps >= 0 AND fee_bps <= 2000),
    effective_from TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_payment_fee_configs_method_effective
    ON payment_fee_configs (payment_method ASC, effective_from DESC, created_at DESC, seq DESC);

CREATE OR REPLACE FUNCTION prevent_payment_fee_config_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'payment_fee_configs is append-only; updates and deletes are forbidden';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_payment_fee_configs_immutable ON payment_fee_configs;
CREATE TRIGGER trg_payment_fee_configs_immutable
BEFORE UPDATE OR DELETE ON payment_fee_configs
FOR EACH ROW
EXECUTE FUNCTION prevent_payment_fee_config_mutation();

INSERT INTO payment_fee_configs (payment_method, fee_bps, effective_from, created_by, note)
SELECT 'COD', 0, TIMESTAMPTZ '2020-01-01 00:00:00+00', 'system', 'Default baseline payment fee (0 bps)'
WHERE NOT EXISTS (
    SELECT 1 FROM payment_fee_configs WHERE payment_method = 'COD'
);

INSERT INTO payment_fee_configs (payment_method, fee_bps, effective_from, created_by, note)
SELECT 'VIETQR', 0, TIMESTAMPTZ '2020-01-01 00:00:00+00', 'system', 'Default baseline payment fee (0 bps)'
WHERE NOT EXISTS (
    SELECT 1 FROM payment_fee_configs WHERE payment_method = 'VIETQR'
);

CREATE INDEX IF NOT EXISTS idx_ledger_entries_account_created
    ON ledger_entries (account_id, created_at DESC, id DESC);
