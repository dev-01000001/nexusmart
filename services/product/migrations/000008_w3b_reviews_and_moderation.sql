-- 000008_w3b_reviews_and_moderation.sql:
-- E6/E14: Product reviews + aggregate rating columns on products.
-- E10: Prohibited categories flag + banned_keywords + flagged_keywords on products.
-- E13: first_approved_at DB evidence for discontinued slug stubs.

ALTER TABLE categories
    ADD COLUMN IF NOT EXISTS is_prohibited BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE products
    ADD COLUMN IF NOT EXISTS rating_sum BIGINT NOT NULL DEFAULT 0 CHECK (rating_sum >= 0),
    ADD COLUMN IF NOT EXISTS review_count INT NOT NULL DEFAULT 0 CHECK (review_count >= 0),
    ADD COLUMN IF NOT EXISTS flagged_keywords JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS first_approved_at TIMESTAMPTZ;

-- Backfill first_approved_at from DB evidence: earliest APPROVE product.reviewed outbox record,
-- or current live public status for pre-outbox approved/seeded products.
-- Never-approved PENDING/REJECTED/deleted products remain NULL (E13).
UPDATE products p
SET first_approved_at = COALESCE(
    (
        SELECT MIN(o.created_at)
        FROM outbox_events o
        WHERE o.event_type = 'product.reviewed'
          AND o.payload_json->>'product_id' = p.id
          AND o.payload_json->>'decision' = 'APPROVE'
    ),
    CASE
        WHEN p.status IN ('APPROVED', 'OUT_OF_STOCK') AND p.deleted_at IS NULL
        THEN COALESCE(p.updated_at, p.created_at, NOW())
        ELSE NULL
    END
)
WHERE p.first_approved_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_products_slug_discontinued
    ON products (slug, deleted_at DESC)
    WHERE deleted_at IS NOT NULL AND first_approved_at IS NOT NULL AND slug <> '';

CREATE TABLE IF NOT EXISTS product_reviews (
    id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    shop_id TEXT NOT NULL,
    order_id TEXT NOT NULL,
    order_item_id TEXT NOT NULL UNIQUE,
    sku_id TEXT NOT NULL DEFAULT '',
    sku_name TEXT NOT NULL DEFAULT '',
    buyer_id TEXT NOT NULL,
    buyer_name TEXT NOT NULL DEFAULT '',
    rating SMALLINT NOT NULL CHECK (rating >= 1 AND rating <= 5),
    comment TEXT NOT NULL DEFAULT '',
    image_urls JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'PUBLISHED' CHECK (status IN ('PUBLISHED', 'HIDDEN')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_product_reviews_product_created
    ON product_reviews (product_id, created_at DESC, id DESC)
    WHERE status = 'PUBLISHED';

CREATE INDEX IF NOT EXISTS idx_product_reviews_product_rating
    ON product_reviews (product_id, rating)
    WHERE status = 'PUBLISHED';

CREATE INDEX IF NOT EXISTS idx_product_reviews_order_buyer
    ON product_reviews (order_id, buyer_id);

CREATE TABLE IF NOT EXISTS banned_keywords (
    id TEXT PRIMARY KEY,
    term TEXT NOT NULL,
    normalized_term TEXT NOT NULL UNIQUE,
    category TEXT NOT NULL DEFAULT 'GENERAL',
    action TEXT NOT NULL CHECK (action IN ('BLOCK', 'REVIEW')),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_banned_keywords_active
    ON banned_keywords (action)
    WHERE is_active = TRUE;
