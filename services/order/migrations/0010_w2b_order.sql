-- Wave 2b: Shop discount programs, shop freeship, payment fee snapshots,
-- line item category/shop_program tracking, and report query indexes.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS shop_program_discount_vnd BIGINT NOT NULL DEFAULT 0 CHECK (shop_program_discount_vnd >= 0),
    ADD COLUMN IF NOT EXISTS shop_freeship_discount_vnd BIGINT NOT NULL DEFAULT 0 CHECK (shop_freeship_discount_vnd >= 0),
    ADD COLUMN IF NOT EXISTS payment_fee_vnd BIGINT NOT NULL DEFAULT 0 CHECK (payment_fee_vnd >= 0),
    ADD COLUMN IF NOT EXISTS payment_fee_bps INT NOT NULL DEFAULT 0 CHECK (payment_fee_bps >= 0 AND payment_fee_bps <= 10000);

ALTER TABLE order_items
    ADD COLUMN IF NOT EXISTS category_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS shop_program_item_id TEXT NOT NULL DEFAULT '';

-- Backfill completed_at for any legacy COMPLETED orders so completed-date report queries
-- can filter directly on completed_at using partial B-tree indexes.
UPDATE orders o
SET completed_at = COALESCE(
    (SELECT MIN(t.created_at) FROM order_timeline_events t WHERE t.order_id = o.id AND t.to_status = 'COMPLETED'),
    o.updated_at
)
WHERE o.status = 'COMPLETED'
  AND o.completed_at IS NULL;

-- Indexes supporting GetSellerReport and GetPlatformReport range queries (Asia/Ho_Chi_Minh bounds passed as TIMESTAMPTZ).
CREATE INDEX IF NOT EXISTS idx_orders_created_at
    ON orders (created_at);

CREATE INDEX IF NOT EXISTS idx_orders_completed_at
    ON orders (completed_at)
    WHERE status = 'COMPLETED' AND completed_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_orders_shop_completed_at
    ON orders (shop_id, completed_at)
    WHERE status = 'COMPLETED' AND completed_at IS NOT NULL;
