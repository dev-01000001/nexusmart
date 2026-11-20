CREATE TABLE IF NOT EXISTS provinces (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    region TEXT NOT NULL,
    ghn_province_id INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS wards (
    code TEXT PRIMARY KEY,
    province_code TEXT NOT NULL REFERENCES provinces(code) ON DELETE CASCADE,
    name TEXT NOT NULL,
    ghn_district_id INT NOT NULL DEFAULT 0,
    ghn_ward_code TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_wards_province_code ON wards(province_code);

CREATE TABLE IF NOT EXISTS legacy_address_mappings (
    id BIGSERIAL PRIMARY KEY,
    legacy_province TEXT NOT NULL,
    legacy_province_norm TEXT NOT NULL,
    legacy_district TEXT NOT NULL DEFAULT '',
    legacy_district_norm TEXT NOT NULL DEFAULT '',
    legacy_ward TEXT NOT NULL DEFAULT '',
    legacy_ward_norm TEXT NOT NULL DEFAULT '',
    province_code TEXT NOT NULL REFERENCES provinces(code),
    ward_code TEXT NOT NULL REFERENCES wards(code),
    legacy_province_code TEXT NOT NULL DEFAULT '',
    legacy_district_code TEXT NOT NULL DEFAULT '',
    legacy_ward_code TEXT NOT NULL DEFAULT '',
    CONSTRAINT uq_legacy_address_norm UNIQUE (legacy_province_norm, legacy_district_norm, legacy_ward_norm)
);

CREATE INDEX IF NOT EXISTS idx_legacy_mappings_prov ON legacy_address_mappings(legacy_province_norm);

CREATE TABLE IF NOT EXISTS shipments (
    id UUID PRIMARY KEY,
    order_id TEXT NOT NULL UNIQUE,
    shop_id TEXT NOT NULL,
    carrier_code TEXT NOT NULL,
    service_code TEXT NOT NULL,
    tracking_code TEXT NOT NULL UNIQUE,
    label_url TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    fee_vnd BIGINT NOT NULL DEFAULT 0,
    cod_amount_vnd BIGINT NOT NULL DEFAULT 0,
    weight_grams INT NOT NULL DEFAULT 500,
    from_province_code TEXT NOT NULL DEFAULT '',
    from_ward_code TEXT NOT NULL DEFAULT '',
    to_province_code TEXT NOT NULL DEFAULT '',
    to_ward_code TEXT NOT NULL DEFAULT '',
    last_event_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shipments_shop_id ON shipments(shop_id);
CREATE INDEX IF NOT EXISTS idx_shipments_status ON shipments(status);

CREATE TABLE IF NOT EXISTS shipment_events (
    id UUID PRIMARY KEY,
    shipment_id UUID NOT NULL REFERENCES shipments(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    raw_status TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shipment_events_shipment_id ON shipment_events(shipment_id, occurred_at ASC);

CREATE TABLE IF NOT EXISTS webhook_events (
    event_id TEXT PRIMARY KEY,
    carrier_code TEXT NOT NULL,
    tracking_code TEXT NOT NULL,
    raw_status TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id UUID PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    subject TEXT NOT NULL,
    payload JSONB NOT NULL,
    traceparent TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox_events(created_at ASC) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS processed_events (
    event_id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dead_letter_events (
    id UUID PRIMARY KEY,
    event_id TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL,
    payload BYTEA NOT NULL,
    error_message TEXT NOT NULL,
    attempts INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
