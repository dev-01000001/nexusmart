-- Wave 2a shipping operations: return shipments, partial unique index,
-- redelivery tracking, buyer ownership, accent-insensitive address search,
-- shop shipping settings, admin carrier configs, and DB-backed fallback rates.
-- Idempotent: shipping runs all migration files on every startup.

ALTER TABLE shipments ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'FORWARD';
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS return_request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS redelivery_count INT NOT NULL DEFAULT 0;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS buyer_id TEXT NOT NULL DEFAULT '';

-- Drop old single-shipment-per-order unique constraint and replace with partial unique index
-- so cancelled shipments never block recreating a shipment and RETURN coexists with FORWARD.
ALTER TABLE shipments DROP CONSTRAINT IF EXISTS shipments_order_id_key;

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
    enabled_carrier_codes TEXT[] NOT NULL DEFAULT ARRAY['ghn', 'mock_express'],
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
    ('mock_express', 'SAME_PROVINCE', 25000, 1000, 6000),
    ('mock_express', 'SAME_REGION',   35000, 1000, 6000),
    ('mock_express', 'CROSS_REGION',  45000, 1000, 6000)
ON CONFLICT (carrier_code, region) DO NOTHING;
