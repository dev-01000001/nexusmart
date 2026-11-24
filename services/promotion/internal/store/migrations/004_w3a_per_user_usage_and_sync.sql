-- 004_w3a_per_user_usage_and_sync.sql:
-- Wave 3a: Authoritative per-user usage tracking in PostgreSQL (survives Redis loss/eviction),
-- voucher rule versioning for UpdateVoucher vs ReservePromotion concurrency, and Redis reconciliation versioning.
-- Migrate() tracks applied migrations in promotion_schema_migrations so 004 runs once per database.
-- Manual re-run is idempotent and resets promotion_user_usage (including used_qty = 0 for RELEASED
-- reservations so drifted rows converge cleanly), but MUST ONLY be
-- executed manually when writers are stopped (offline/maintenance) because backfill upserts
-- overwrite concurrent in-flight counter updates.

CREATE TABLE IF NOT EXISTS promotion_user_usage (
    kind        TEXT NOT NULL CHECK (kind IN ('VOUCHER', 'FLASH', 'SHOP_PROGRAM')),
    target_id   TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    used_qty    INTEGER NOT NULL DEFAULT 0 CHECK (used_qty >= 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (kind, target_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_promotion_user_usage_user
    ON promotion_user_usage (user_id, kind, target_id);

-- Backfill promotion_user_usage from BOTH RESERVED and RELEASED reservations so legacy/drifted
-- RELEASED rows converge to used_qty = 0 (Redis :user: keys use 120s TTL and reads take max(Redis, PG)).
-- Ordered alphabetically by kind (FLASH -> SHOP_PROGRAM -> VOUCHER) to match CommitReservation/ReleaseReservation lock order.
-- 1. Flash sale items
INSERT INTO promotion_user_usage (kind, target_id, user_id, used_qty, updated_at)
SELECT
    'FLASH',
    elem->>'flash_sale_item_id',
    pr.user_id,
    COALESCE(SUM(GREATEST(COALESCE((elem->>'quantity')::INTEGER, 0), 0)) FILTER (
        WHERE pr.status = 'RESERVED'
          AND NOT (
              COALESCE(NULLIF(elem->>'shop_id', ''), fsi.shop_id, '') <> ''
              AND COALESCE(pr.released_shops_json, '[]'::jsonb) ? COALESCE(NULLIF(elem->>'shop_id', ''), fsi.shop_id, '')
          )
    ), 0)::INTEGER,
    NOW()
FROM promotion_reservations pr
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(pr.flash_sale_items_json, '[]'::jsonb)) AS elem
LEFT JOIN flash_sale_items fsi ON fsi.id = elem->>'flash_sale_item_id'
WHERE pr.status IN ('RESERVED', 'RELEASED')
  AND pr.user_id <> ''
  AND COALESCE(elem->>'flash_sale_item_id', '') <> ''
  AND COALESCE((elem->>'quantity')::INTEGER, 0) > 0
GROUP BY elem->>'flash_sale_item_id', pr.user_id
ORDER BY elem->>'flash_sale_item_id', pr.user_id
ON CONFLICT (kind, target_id, user_id) DO UPDATE
    SET used_qty = EXCLUDED.used_qty,
        updated_at = NOW();

-- 2. Shop discount program items
INSERT INTO promotion_user_usage (kind, target_id, user_id, used_qty, updated_at)
SELECT
    'SHOP_PROGRAM',
    elem->>'shop_discount_program_item_id',
    pr.user_id,
    COALESCE(SUM(GREATEST(COALESCE((elem->>'quantity')::INTEGER, 0), 0)) FILTER (
        WHERE pr.status = 'RESERVED'
          AND NOT (
              COALESCE(NULLIF(elem->>'shop_id', ''), sdi.shop_id, '') <> ''
              AND COALESCE(pr.released_shops_json, '[]'::jsonb) ? COALESCE(NULLIF(elem->>'shop_id', ''), sdi.shop_id, '')
          )
    ), 0)::INTEGER,
    NOW()
FROM promotion_reservations pr
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(pr.shop_program_items_json, '[]'::jsonb)) AS elem
LEFT JOIN shop_discount_program_items sdi ON sdi.id = elem->>'shop_discount_program_item_id'
WHERE pr.status IN ('RESERVED', 'RELEASED')
  AND pr.user_id <> ''
  AND COALESCE(elem->>'shop_discount_program_item_id', '') <> ''
  AND COALESCE((elem->>'quantity')::INTEGER, 0) > 0
GROUP BY elem->>'shop_discount_program_item_id', pr.user_id
ORDER BY elem->>'shop_discount_program_item_id', pr.user_id
ON CONFLICT (kind, target_id, user_id) DO UPDATE
    SET used_qty = EXCLUDED.used_qty,
        updated_at = NOW();

-- 3. Vouchers
INSERT INTO promotion_user_usage (kind, target_id, user_id, used_qty, updated_at)
SELECT
    'VOUCHER',
    elem->>'voucher_id',
    pr.user_id,
    COUNT(*) FILTER (
        WHERE pr.status = 'RESERVED'
          AND NOT (
              COALESCE(NULLIF(elem->>'scope', ''), v.scope, '') = 'SHOP'
              AND COALESCE(NULLIF(elem->>'shop_id', ''), v.shop_id, '') <> ''
              AND COALESCE(pr.released_shops_json, '[]'::jsonb) ? COALESCE(NULLIF(elem->>'shop_id', ''), v.shop_id, '')
          )
    )::INTEGER,
    NOW()
FROM promotion_reservations pr
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(pr.vouchers_json, '[]'::jsonb)) AS elem
LEFT JOIN vouchers v ON v.id = elem->>'voucher_id'
WHERE pr.status IN ('RESERVED', 'RELEASED')
  AND pr.user_id <> ''
  AND COALESCE(elem->>'voucher_id', '') <> ''
GROUP BY elem->>'voucher_id', pr.user_id
ORDER BY elem->>'voucher_id', pr.user_id
ON CONFLICT (kind, target_id, user_id) DO UPDATE
    SET used_qty = EXCLUDED.used_qty,
        updated_at = NOW();

CREATE INDEX IF NOT EXISTS idx_promo_res_reserved_vouchers
    ON promotion_reservations USING GIN (vouchers_json)
    WHERE status = 'RESERVED';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'vouchers' AND column_name = 'rules_version'
    ) THEN
        ALTER TABLE vouchers ADD COLUMN rules_version INTEGER NOT NULL DEFAULT 1;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'vouchers' AND column_name = 'redis_sync_version'
    ) THEN
        ALTER TABLE vouchers ADD COLUMN redis_sync_version BIGINT NOT NULL DEFAULT 0;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'vouchers' AND column_name = 'redis_synced_version'
    ) THEN
        ALTER TABLE vouchers ADD COLUMN redis_synced_version BIGINT NOT NULL DEFAULT 0;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_vouchers_redis_sync_pending
    ON vouchers (id)
    WHERE redis_synced_version < redis_sync_version;
