-- 002_flash_sale_window_status.sql: Track flash sale window status & stock release (idempotent)

ALTER TABLE flash_sales
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'ENDED'));

ALTER TABLE flash_sale_items
    ADD COLUMN IF NOT EXISTS stock_released_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_flash_sales_status_ends_at
    ON flash_sales (status, ends_at);
