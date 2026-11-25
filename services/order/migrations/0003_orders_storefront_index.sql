-- 0003: buyer order history per storefront site (non-destructive, index only).
CREATE INDEX IF NOT EXISTS idx_orders_buyer_storefront_created
    ON orders (buyer_id, storefront_shop_id, created_at DESC);
