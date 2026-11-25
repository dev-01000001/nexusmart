-- Wave 2a shipping operations: return shipments, partial unique index,
-- redelivery tracking, buyer ownership, accent-insensitive address search,
-- shop shipping settings, admin carrier configs, and DB-backed fallback rates.
-- Tracked by schema_migrations (also idempotent when upgrading an unversioned DB).

ALTER TABLE shipments ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'FORWARD';
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS return_request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS redelivery_count INT NOT NULL DEFAULT 0;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS buyer_id TEXT NOT NULL DEFAULT '';

-- Drop old single-shipment-per-order unique constraint and phase-0b non-partial unique index
-- before normalizing direction values and creating the partial active unique index
-- so cancelled shipments never block recreating a shipment and RETURN coexists with FORWARD.
ALTER TABLE shipments DROP CONSTRAINT IF EXISTS shipments_order_id_key;
DROP INDEX IF EXISTS uq_shipments_order_direction;

-- Normalize phase-0b lowercase direction ('forward'/'return') and its CHECK constraint to
-- uppercase ('FORWARD'/'RETURN') with explicit constraint name chk_shipments_direction.
DO $$
DECLARE
    con_rec RECORD;
BEGIN
    FOR con_rec IN
        SELECT DISTINCT c.conname
        FROM pg_constraint c
        JOIN pg_attribute a
          ON a.attrelid = c.conrelid
         AND a.attnum = ANY(c.conkey)
        WHERE c.conrelid = 'shipments'::regclass
          AND c.contype = 'c'
          AND a.attname = 'direction'
          AND c.conname <> 'chk_shipments_direction'
    LOOP
        EXECUTE format('ALTER TABLE shipments DROP CONSTRAINT %I', con_rec.conname);
    END LOOP;

    ALTER TABLE shipments ALTER COLUMN direction SET DEFAULT 'FORWARD';

    UPDATE shipments
       SET direction = UPPER(direction)
     WHERE direction IN ('forward', 'return');

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'shipments'::regclass
          AND conname = 'chk_shipments_direction'
    ) THEN
        ALTER TABLE shipments
            ADD CONSTRAINT chk_shipments_direction
            CHECK (direction IN ('FORWARD', 'RETURN'));
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_shipments_order_direction_active
    ON shipments (order_id, direction)
    WHERE status <> 'cancelled';

CREATE INDEX IF NOT EXISTS idx_shipments_order_direction
    ON shipments (order_id, direction);

-- Accent-insensitive search columns populated by seedAddressCatalog on startup.
ALTER TABLE provinces ADD COLUMN IF NOT EXISTS name_norm TEXT NOT NULL DEFAULT '';
ALTER TABLE wards ADD COLUMN IF NOT EXISTS name_norm TEXT NOT NULL DEFAULT '';

-- UC-SHIP06: per-shop shipping configuration.
CREATE TABLE IF NOT EXISTS shop_shipping_settings (
    shop_id TEXT PRIMARY KEY,
    enabled_carrier_codes TEXT[] NOT NULL DEFAULT ARRAY['ghn', 'ghtk', 'mock_express'],
    default_weight_grams INT NOT NULL DEFAULT 500 CHECK (default_weight_grams > 0),
    default_length_cm INT NOT NULL DEFAULT 20 CHECK (default_length_cm >= 0),
    default_width_cm INT NOT NULL DEFAULT 15 CHECK (default_width_cm >= 0),
    default_height_cm INT NOT NULL DEFAULT 10 CHECK (default_height_cm >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- UC-A12: platform-wide carrier toggle and AES-256-GCM encrypted API token.
CREATE TABLE IF NOT EXISTS carrier_configs (
    carrier_code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    encrypted_token BYTEA,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO carrier_configs (carrier_code, name, enabled)
VALUES
    ('ghn', 'Giao Hang Nhanh (GHN)', TRUE),
    ('ghtk', 'Giao Hang Tiet Kiem (GHTK)', TRUE),
    ('mock_express', 'Mock Express', TRUE)
ON CONFLICT (carrier_code) DO NOTHING;

-- UC-A12: DB-backed fallback rate table seeded with existing hardcoded VND values.
CREATE TABLE IF NOT EXISTS fallback_rates (
    carrier_code TEXT NOT NULL DEFAULT 'fallback',
    region TEXT NOT NULL CHECK (region IN ('SAME_PROVINCE', 'SAME_REGION', 'CROSS_REGION')),
    base_fee_vnd BIGINT NOT NULL CHECK (base_fee_vnd >= 0),
    base_weight_grams INT NOT NULL CHECK (base_weight_grams > 0),
    extra_fee_vnd_per_500g BIGINT NOT NULL CHECK (extra_fee_vnd_per_500g >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (carrier_code, region)
);

INSERT INTO fallback_rates (carrier_code, region, base_fee_vnd, base_weight_grams, extra_fee_vnd_per_500g)
VALUES
    ('fallback', 'SAME_PROVINCE', 25000, 1000, 6000),
    ('fallback', 'SAME_REGION',   35000, 1000, 6000),
    ('fallback', 'CROSS_REGION',  45000, 1000, 6000),
    ('ghn', 'SAME_PROVINCE', 25000, 1000, 6000),
    ('ghn', 'SAME_REGION',   35000, 1000, 6000),
    ('ghn', 'CROSS_REGION',  45000, 1000, 6000),
    ('ghtk', 'SAME_PROVINCE', 25000, 1000, 6000),
    ('ghtk', 'SAME_REGION',   35000, 1000, 6000),
    ('ghtk', 'CROSS_REGION',  45000, 1000, 6000),
    ('mock_express', 'SAME_PROVINCE', 25000, 1000, 6000),
    ('mock_express', 'SAME_REGION',   35000, 1000, 6000),
    ('mock_express', 'CROSS_REGION',  45000, 1000, 6000)
ON CONFLICT (carrier_code, region) DO NOTHING;
