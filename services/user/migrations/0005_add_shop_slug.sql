-- 0005_add_shop_slug.sql: Add unique slug to shops for /gian-hang/[slug] lookup.
-- Backfill prefers sites.subdomain (when present and non-colliding) before falling back to LOWER(shops.id::text).

ALTER TABLE shops ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT '';

UPDATE shops sh
SET slug = COALESCE(
    (
        SELECT NULLIF(LOWER(BTRIM(s.subdomain)), '')
        FROM sites s
        WHERE s.shop_id = sh.id
          AND NOT EXISTS (
              SELECT 1 FROM shops other
              WHERE other.id <> sh.id
                AND other.slug = LOWER(BTRIM(s.subdomain))
          )
        LIMIT 1
    ),
    LOWER(sh.id::text)
)
WHERE sh.slug = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_shops_slug_nonempty
    ON shops (slug)
    WHERE slug <> '';
