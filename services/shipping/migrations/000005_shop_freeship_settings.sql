-- Wave 2b (UC-SHIP06): per-shop freeship policy thresholds on shop_shipping_settings.
-- Idempotent: shipping runs all migration files on every startup.

ALTER TABLE shop_shipping_settings
    ADD COLUMN IF NOT EXISTS free_ship_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE shop_shipping_settings
    ADD COLUMN IF NOT EXISTS free_ship_min_order_vnd BIGINT NOT NULL DEFAULT 0
        CHECK (free_ship_min_order_vnd >= 0);

ALTER TABLE shop_shipping_settings
    ADD COLUMN IF NOT EXISTS free_ship_max_vnd BIGINT NOT NULL DEFAULT 0
        CHECK (free_ship_max_vnd >= 0);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'chk_shop_shipping_free_ship_max_positive'
          AND conrelid = 'shop_shipping_settings'::regclass
    ) THEN
        ALTER TABLE shop_shipping_settings
            ADD CONSTRAINT chk_shop_shipping_free_ship_max_positive
            CHECK (NOT free_ship_enabled OR free_ship_max_vnd > 0);
    END IF;
END
$$;
