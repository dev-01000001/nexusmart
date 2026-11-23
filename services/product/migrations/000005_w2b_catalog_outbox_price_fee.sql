-- 000005_w2b_catalog_outbox_price_fee.sql: Transactional outbox, SKU 30d price history, and category fee schedules.

CREATE TABLE IF NOT EXISTS outbox_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    trace_id TEXT NOT NULL DEFAULT '',
    traceparent TEXT NOT NULL DEFAULT '',
    payload_json JSONB NOT NULL,
    published_at TIMESTAMPTZ,
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_product_outbox_events_unpublished
    ON outbox_events (created_at ASC)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS sku_price_history (
    id TEXT PRIMARY KEY,
    seq BIGSERIAL NOT NULL,
    sku_id TEXT NOT NULL REFERENCES skus(id) ON DELETE CASCADE,
    price_vnd BIGINT NOT NULL CHECK (price_vnd > 0),
    actor_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sku_price_history_sku_recorded
    ON sku_price_history (sku_id, recorded_at DESC, seq DESC);

INSERT INTO sku_price_history (id, sku_id, price_vnd, actor_id, reason, recorded_at)
SELECT 'backfill-' || s.id, s.id, s.price_vnd, 'migration', 'backfill current price', COALESCE(s.created_at, NOW())
FROM skus s
WHERE NOT EXISTS (
    SELECT 1 FROM sku_price_history h WHERE h.sku_id = s.id
);

CREATE TABLE IF NOT EXISTS category_fee_schedules (
    id TEXT PRIMARY KEY,
    seq BIGSERIAL NOT NULL,
    category_id TEXT NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    commission_rate_bps INT NOT NULL CHECK (commission_rate_bps >= 0 AND commission_rate_bps <= 10000),
    effective_from TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_category_fee_schedules_cat_eff
    ON category_fee_schedules (category_id, effective_from DESC, created_at DESC, seq DESC);

INSERT INTO category_fee_schedules (id, category_id, commission_rate_bps, effective_from, created_by, note, created_at)
SELECT 'init-' || c.id, c.id, c.commission_rate_bps, COALESCE(c.created_at, NOW()), 'migration', 'initial category commission rate', COALESCE(c.created_at, NOW())
FROM categories c
WHERE NOT EXISTS (
    SELECT 1 FROM category_fee_schedules f WHERE f.category_id = c.id
);
