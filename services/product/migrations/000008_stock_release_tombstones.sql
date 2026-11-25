-- 000006_stock_release_tombstones.sql: Record release tombstones so a delayed ReserveStock
-- arriving after ReleaseStock for the same order_id cannot leak stuck reserved_stock.

CREATE TABLE IF NOT EXISTS stock_release_tombstones (
    order_id TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
