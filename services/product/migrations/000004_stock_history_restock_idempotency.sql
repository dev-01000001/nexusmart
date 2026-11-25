-- Idempotency guard for per-sub-order SKU restocks (one RESTOCK per (order_id, sku_id)).

CREATE UNIQUE INDEX IF NOT EXISTS idx_stock_history_restock_order_sku
    ON stock_history (order_id, sku_id)
    WHERE change_type = 'RESTOCK';
