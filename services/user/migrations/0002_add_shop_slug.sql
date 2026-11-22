-- 0002_add_shop_slug.sql: Add unique slug to shops for /gian-hang/[slug] lookup.

ALTER TABLE shops ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT '';

UPDATE shops
SET slug = LOWER(id::text)
WHERE slug = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_shops_slug_nonempty
    ON shops (slug)
    WHERE slug <> '';
