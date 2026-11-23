-- 003_w2b_promotions.sql: Wave 2b voucher lifecycle & category scope, flash sale metadata,
-- shop discount programs (UC-S18), and voucher expiry notification tracking.
-- Fully idempotent because promotion executes all migration SQL files on every startup.

-- 1. Vouchers: lifecycle status (ACTIVE/PAUSED/DELETED), category scope, soft-delete timestamp
ALTER TABLE vouchers
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'ACTIVE';

ALTER TABLE vouchers
    ADD COLUMN IF NOT EXISTS category_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE vouchers
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_vouchers_status'
    ) THEN
        ALTER TABLE vouchers
            ADD CONSTRAINT chk_vouchers_status CHECK (status IN ('ACTIVE', 'PAUSED', 'DELETED'));
    END IF;
END $$;

UPDATE vouchers
SET status = 'PAUSED'
WHERE is_active = FALSE AND status = 'ACTIVE' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_vouchers_status ON vouchers (status, scope, shop_id);

-- 2. User vouchers: track voucher expiring notification sent at most once per (user_id, voucher_id)
ALTER TABLE user_vouchers
    ADD COLUMN IF NOT EXISTS expiry_notified_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_user_vouchers_unnotified
    ON user_vouchers (voucher_id)
    WHERE expiry_notified_at IS NULL;

-- 3. Flash sales & items: campaign minimum discount, category restriction, SKU original price snapshot
ALTER TABLE flash_sales
    ADD COLUMN IF NOT EXISTS min_discount_percent INTEGER NOT NULL DEFAULT 0;

ALTER TABLE flash_sales
    ADD COLUMN IF NOT EXISTS category_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_flash_sales_min_discount'
    ) THEN
        ALTER TABLE flash_sales
            ADD CONSTRAINT chk_flash_sales_min_discount CHECK (min_discount_percent >= 0 AND min_discount_percent <= 99);
    END IF;
END $$;

ALTER TABLE flash_sale_items
    ADD COLUMN IF NOT EXISTS original_price_vnd BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_flash_sale_items_status_shop
    ON flash_sale_items (status, shop_id, created_at DESC);

-- 4. Shop discount programs (UC-S18)
CREATE TABLE IF NOT EXISTS shop_discount_programs (
    id          TEXT PRIMARY KEY,
    shop_id     TEXT NOT NULL,
    name        TEXT NOT NULL,
    starts_at   TIMESTAMPTZ NOT NULL,
    ends_at     TIMESTAMPTZ NOT NULL,
    status      TEXT NOT NULL DEFAULT 'SCHEDULED' CHECK (status IN ('SCHEDULED', 'ACTIVE', 'ENDED')),
    ended_at    TIMESTAMPTZ,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (ends_at > starts_at)
);

CREATE INDEX IF NOT EXISTS idx_shop_discount_programs_shop_window
    ON shop_discount_programs (shop_id, status, starts_at, ends_at);

CREATE TABLE IF NOT EXISTS shop_discount_program_items (
    id                      TEXT PRIMARY KEY,
    program_id              TEXT NOT NULL REFERENCES shop_discount_programs(id) ON DELETE CASCADE,
    shop_id                 TEXT NOT NULL,
    sku_id                  TEXT NOT NULL,
    original_price_vnd      BIGINT NOT NULL CHECK (original_price_vnd > 0),
    discounted_price_vnd    BIGINT NOT NULL CHECK (discounted_price_vnd > 0 AND discounted_price_vnd < original_price_vnd),
    discount_percent        INTEGER NOT NULL CHECK (discount_percent >= 0 AND discount_percent <= 99),
    per_user_limit          INTEGER NOT NULL DEFAULT 0 CHECK (per_user_limit >= 0),
    price_inflation_flagged BOOLEAN NOT NULL DEFAULT FALSE,
    warning                 TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (program_id, sku_id)
);

CREATE INDEX IF NOT EXISTS idx_shop_discount_items_sku
    ON shop_discount_program_items (sku_id, shop_id);

CREATE INDEX IF NOT EXISTS idx_shop_discount_items_program
    ON shop_discount_program_items (program_id);

-- 5. Reservations: persist reserved shop discount program items (backward compatible with legacy rows)
ALTER TABLE promotion_reservations
    ADD COLUMN IF NOT EXISTS shop_program_items_json JSONB NOT NULL DEFAULT '[]'::jsonb;
