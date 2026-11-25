-- NexusMart Product Service Phase 2b: Flash Sale Stock Reservations

ALTER TABLE skus
    ADD COLUMN IF NOT EXISTS flash_sale_reserved_stock INT NOT NULL DEFAULT 0 CHECK (flash_sale_reserved_stock >= 0);

ALTER TABLE skus
    DROP CONSTRAINT IF EXISTS skus_total_reserved_le_stock;
ALTER TABLE skus
    ADD CONSTRAINT skus_total_reserved_le_stock CHECK (reserved_stock + flash_sale_reserved_stock <= stock);

ALTER TABLE stock_reservations
    ADD COLUMN IF NOT EXISTS flash_sale_item_id TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS flash_sale_stock_reservations (
    flash_sale_item_id TEXT PRIMARY KEY,
    sku_id             TEXT NOT NULL REFERENCES skus(id) ON DELETE RESTRICT,
    reserved_qty       INT NOT NULL CHECK (reserved_qty > 0),
    consumed_qty       INT NOT NULL DEFAULT 0 CHECK (consumed_qty >= 0 AND consumed_qty <= reserved_qty),
    status             TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RELEASED')),
    ends_at            TIMESTAMPTZ NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_flash_sale_stock_res_sku_status
    ON flash_sale_stock_reservations (sku_id, status);

CREATE INDEX IF NOT EXISTS idx_flash_sale_stock_res_active_ends_at
    ON flash_sale_stock_reservations (ends_at)
    WHERE status = 'ACTIVE';
