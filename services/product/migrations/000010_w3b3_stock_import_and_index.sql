-- 000010_w3b3_stock_import_and_index.sql:
-- A8 S03: stock_history (sku_id, created_at DESC, id DESC) index.
-- A8 UC-S13 (G6): product_import_jobs (lease-based background worker) and product_import_rows.
-- B1 (G9, G11): keyset index for ListProductsForIndex on public products.

CREATE INDEX IF NOT EXISTS idx_stock_history_sku_created
    ON stock_history (sku_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS product_import_jobs (
    id TEXT PRIMARY KEY,
    shop_id TEXT NOT NULL,
    created_by TEXT NOT NULL,
    filename TEXT NOT NULL DEFAULT '',
    raw_csv BYTEA,
    status TEXT NOT NULL DEFAULT 'QUEUED'
        CHECK (status IN ('QUEUED', 'VALIDATING', 'READY', 'CONFIRMING', 'COMPLETED', 'FAILED')),
    total_rows INT NOT NULL DEFAULT 0 CHECK (total_rows >= 0),
    valid_rows INT NOT NULL DEFAULT 0 CHECK (valid_rows >= 0),
    invalid_rows INT NOT NULL DEFAULT 0 CHECK (invalid_rows >= 0),
    created_rows INT NOT NULL DEFAULT 0 CHECK (created_rows >= 0),
    failed_rows INT NOT NULL DEFAULT 0 CHECK (failed_rows >= 0),
    error_message TEXT NOT NULL DEFAULT '',
    claimed_until TIMESTAMPTZ,
    claim_token TEXT NOT NULL DEFAULT '',
    confirmed_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_product_import_jobs_claimable
    ON product_import_jobs (created_at ASC, id ASC)
    WHERE status IN ('QUEUED', 'VALIDATING', 'CONFIRMING');

CREATE INDEX IF NOT EXISTS idx_product_import_jobs_shop_created
    ON product_import_jobs (shop_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS product_import_rows (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES product_import_jobs(id) ON DELETE CASCADE,
    row_number INT NOT NULL CHECK (row_number > 0),
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'VALID', 'INVALID', 'CREATED', 'FAILED')),
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    product_name TEXT NOT NULL DEFAULT '',
    sku_code TEXT NOT NULL DEFAULT '',
    category_id TEXT NOT NULL DEFAULT '',
    price_vnd BIGINT NOT NULL DEFAULT 0,
    stock INT NOT NULL DEFAULT 0,
    weight_grams INT NOT NULL DEFAULT 0,
    created_product_id TEXT NOT NULL DEFAULT '',
    raw_row_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (job_id, row_number)
);

CREATE INDEX IF NOT EXISTS idx_product_import_rows_job_status_row
    ON product_import_rows (job_id, status, row_number ASC);

CREATE INDEX IF NOT EXISTS idx_products_public_id
    ON products (id ASC)
    WHERE deleted_at IS NULL AND status IN ('APPROVED', 'OUT_OF_STOCK');
