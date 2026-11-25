-- Wave 3b-1 (E15, E16, E18):
--   - wards.cod_supported (default TRUE; FALSE for Hoang Sa & Truong Sa) and wards.is_active
--     (legacy sample ward codes absent from QD 19/2025 stay in DB with is_active = FALSE).
--   - address_catalog_meta checksum table so restarts skip full catalog upsert when unchanged.
--   - shipment_create_jobs queue table for transient 5xx/timeout/circuit-open shipment retries.
-- Idempotent: shipping runs all migration files on every startup.

ALTER TABLE wards
    ADD COLUMN IF NOT EXISTS cod_supported BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE wards
    ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX IF NOT EXISTS idx_wards_province_active
    ON wards (province_code, is_active, code);

CREATE TABLE IF NOT EXISTS address_catalog_meta (
    key TEXT PRIMARY KEY,
    checksum TEXT NOT NULL,
    ward_count INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS shipment_create_jobs (
    id UUID PRIMARY KEY,
    order_id TEXT NOT NULL,
    shop_id TEXT NOT NULL DEFAULT '',
    direction TEXT NOT NULL DEFAULT 'FORWARD',
    carrier_code TEXT NOT NULL DEFAULT '',
    service_code TEXT NOT NULL DEFAULT '',
    client_order_code TEXT NOT NULL,
    request_json JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'cancelled')),
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INT NOT NULL DEFAULT 8 CHECK (max_attempts > 0),
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_until TIMESTAMPTZ,
    claim_token TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_shipment_create_jobs_order_direction_active
    ON shipment_create_jobs (order_id, direction)
    WHERE status IN ('pending', 'processing');

CREATE INDEX IF NOT EXISTS idx_shipment_create_jobs_claimable
    ON shipment_create_jobs (next_retry_at ASC, created_at ASC)
    WHERE status IN ('pending', 'processing');
