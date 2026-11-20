-- Phase 1: Add immutable sales_channel & storefront_shop_id to checkout_groups and orders.
-- Backward compatible: existing rows default to sales_channel = 'MARKETPLACE', storefront_shop_id = ''.

ALTER TABLE checkout_groups
    ADD COLUMN IF NOT EXISTS sales_channel TEXT NOT NULL DEFAULT 'MARKETPLACE',
    ADD COLUMN IF NOT EXISTS storefront_shop_id TEXT NOT NULL DEFAULT '';

ALTER TABLE checkout_groups
    DROP CONSTRAINT IF EXISTS chk_checkout_groups_sales_channel;

ALTER TABLE checkout_groups
    ADD CONSTRAINT chk_checkout_groups_sales_channel
        CHECK (sales_channel IN ('MARKETPLACE', 'STOREFRONT'));

ALTER TABLE checkout_groups
    DROP CONSTRAINT IF EXISTS chk_checkout_groups_storefront_shop_consistency;

ALTER TABLE checkout_groups
    ADD CONSTRAINT chk_checkout_groups_storefront_shop_consistency
        CHECK (
            (sales_channel = 'MARKETPLACE' AND storefront_shop_id = '')
            OR (sales_channel = 'STOREFRONT' AND storefront_shop_id <> '')
        );

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS sales_channel TEXT NOT NULL DEFAULT 'MARKETPLACE',
    ADD COLUMN IF NOT EXISTS storefront_shop_id TEXT NOT NULL DEFAULT '';

ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS chk_orders_sales_channel;

ALTER TABLE orders
    ADD CONSTRAINT chk_orders_sales_channel
        CHECK (sales_channel IN ('MARKETPLACE', 'STOREFRONT'));

ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS chk_orders_storefront_shop_consistency;

ALTER TABLE orders
    ADD CONSTRAINT chk_orders_storefront_shop_consistency
        CHECK (
            (sales_channel = 'MARKETPLACE' AND storefront_shop_id = '')
            OR (sales_channel = 'STOREFRONT' AND storefront_shop_id <> '' AND storefront_shop_id = shop_id)
        );

CREATE INDEX IF NOT EXISTS idx_orders_sales_channel
    ON orders (sales_channel, created_at DESC);

CREATE OR REPLACE FUNCTION prevent_order_sales_channel_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.sales_channel IS DISTINCT FROM OLD.sales_channel
       OR NEW.storefront_shop_id IS DISTINCT FROM OLD.storefront_shop_id THEN
        RAISE EXCEPTION 'sales_channel and storefront_shop_id are immutable after creation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_checkout_groups_sales_channel_immutable ON checkout_groups;
CREATE TRIGGER trg_checkout_groups_sales_channel_immutable
    BEFORE UPDATE ON checkout_groups
    FOR EACH ROW
    EXECUTE FUNCTION prevent_order_sales_channel_mutation();

DROP TRIGGER IF EXISTS trg_orders_sales_channel_immutable ON orders;
CREATE TRIGGER trg_orders_sales_channel_immutable
    BEFORE UPDATE ON orders
    FOR EACH ROW
    EXECUTE FUNCTION prevent_order_sales_channel_mutation();
