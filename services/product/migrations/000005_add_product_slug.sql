-- 000003_add_product_slug.sql: Add unique active slug to products for /san-pham/[slug] lookup.

ALTER TABLE products ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT '';

UPDATE products
SET slug = LOWER(id)
WHERE slug = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_products_slug_active
    ON products (slug)
    WHERE deleted_at IS NULL AND slug <> '';
