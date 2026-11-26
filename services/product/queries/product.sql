-- Categories

-- name: LockCategoryTree :exec
SELECT pg_advisory_xact_lock(7956455656534110821);

-- name: CreateCategory :one
INSERT INTO categories (id, name, slug, parent_id, commission_rate_bps, sort_order, is_prohibited)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, name, slug, parent_id, commission_rate_bps, sort_order, is_prohibited, created_at, updated_at;

-- name: GetCategoryByID :one
SELECT
    c.id, c.name, c.slug, c.parent_id,
    COALESCE(
        (
            SELECT f.commission_rate_bps
            FROM category_fee_schedules f
            WHERE f.category_id = c.id
              AND f.effective_from <= sqlc.arg(as_of)::timestamptz
            ORDER BY f.effective_from DESC, f.seq DESC
            LIMIT 1
        ),
        c.commission_rate_bps
    )::int AS commission_rate_bps,
    c.sort_order, c.is_prohibited, c.created_at, c.updated_at
FROM categories c
WHERE c.id = sqlc.arg(id)::text;

-- name: GetCategoryBySlug :one
SELECT id, name, slug, parent_id, commission_rate_bps, sort_order, is_prohibited, created_at, updated_at
FROM categories
WHERE slug = $1;

-- name: UpdateCategory :one
UPDATE categories
SET name = $2,
    slug = $3,
    parent_id = $4,
    commission_rate_bps = $5,
    sort_order = $6,
    is_prohibited = $7,
    updated_at = NOW()
WHERE id = $1
RETURNING id, name, slug, parent_id, commission_rate_bps, sort_order, is_prohibited, created_at, updated_at;

-- name: UpdateCategoryCommissionRate :exec
UPDATE categories
SET commission_rate_bps = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: CountProductsInCategory :one
SELECT
    COUNT(*)::bigint AS total_cnt,
    COUNT(*) FILTER (WHERE deleted_at IS NULL)::bigint AS active_cnt
FROM products
WHERE category_id = $1;

-- name: CountChildCategories :one
SELECT COUNT(*)::bigint AS cnt
FROM categories
WHERE parent_id = $1;

-- name: DeleteCategory :execrows
DELETE FROM categories
WHERE id = $1;

-- name: ListAllCategories :many
SELECT
    c.id, c.name, c.slug, c.parent_id,
    COALESCE(
        (
            SELECT f.commission_rate_bps
            FROM category_fee_schedules f
            WHERE f.category_id = c.id
              AND f.effective_from <= sqlc.arg(as_of)::timestamptz
            ORDER BY f.effective_from DESC, f.seq DESC
            LIMIT 1
        ),
        c.commission_rate_bps
    )::int AS commission_rate_bps,
    c.sort_order, c.is_prohibited, c.created_at, c.updated_at
FROM categories c
ORDER BY c.sort_order ASC, c.name ASC, c.id ASC;

-- name: ListCategoriesByParent :many
SELECT
    c.id, c.name, c.slug, c.parent_id,
    COALESCE(
        (
            SELECT f.commission_rate_bps
            FROM category_fee_schedules f
            WHERE f.category_id = c.id
              AND f.effective_from <= sqlc.arg(as_of)::timestamptz
            ORDER BY f.effective_from DESC, f.seq DESC
            LIMIT 1
        ),
        c.commission_rate_bps
    )::int AS commission_rate_bps,
    c.sort_order, c.is_prohibited, c.created_at, c.updated_at
FROM categories c
WHERE c.parent_id = sqlc.arg(parent_id)
ORDER BY c.sort_order ASC, c.name ASC, c.id ASC;

-- name: ListRootCategories :many
SELECT
    c.id, c.name, c.slug, c.parent_id,
    COALESCE(
        (
            SELECT f.commission_rate_bps
            FROM category_fee_schedules f
            WHERE f.category_id = c.id
              AND f.effective_from <= sqlc.arg(as_of)::timestamptz
            ORDER BY f.effective_from DESC, f.seq DESC
            LIMIT 1
        ),
        c.commission_rate_bps
    )::int AS commission_rate_bps,
    c.sort_order, c.is_prohibited, c.created_at, c.updated_at
FROM categories c
WHERE c.parent_id IS NULL
ORDER BY c.sort_order ASC, c.name ASC, c.id ASC;

-- name: CategoryHasAncestor :one
WITH RECURSIVE ancestors AS (
    SELECT c.id, c.parent_id
    FROM categories c
    WHERE c.id = sqlc.arg(start_id)::text
    UNION
    SELECT p.id, p.parent_id
    FROM categories p
    JOIN ancestors a ON p.id = a.parent_id
)
SELECT EXISTS (
    SELECT 1 FROM ancestors WHERE id = sqlc.arg(target_ancestor_id)::text
)::bool AS has_ancestor;

-- name: IsCategoryProhibited :one
WITH RECURSIVE ancestors AS (
    SELECT c.id, c.parent_id, c.is_prohibited
    FROM categories c
    WHERE c.id = sqlc.arg(category_id)::text
    UNION
    SELECT p.id, p.parent_id, p.is_prohibited
    FROM categories p
    JOIN ancestors a ON p.id = a.parent_id
)
SELECT EXISTS (
    SELECT 1 FROM ancestors WHERE is_prohibited = TRUE
)::bool AS is_prohibited;

-- name: ListProhibitedCategoryIDs :many
WITH RECURSIVE prohibited_tree AS (
    SELECT c.id
    FROM categories c
    WHERE c.is_prohibited = TRUE
    UNION
    SELECT child.id
    FROM categories child
    JOIN prohibited_tree pt ON child.parent_id = pt.id
)
SELECT id::text AS id
FROM prohibited_tree
ORDER BY id ASC;

-- name: GetCategoryDescendants :many
WITH RECURSIVE cat_tree AS (
    SELECT DISTINCT id
    FROM unnest(sqlc.arg(category_ids)::text[]) AS t(id)
    UNION
    SELECT child.id
    FROM categories child
    JOIN cat_tree ct ON child.parent_id = ct.id
)
SELECT id::text AS id
FROM cat_tree
ORDER BY id ASC;

-- Category Fee Schedules

-- name: InsertCategoryFeeSchedule :one
INSERT INTO category_fee_schedules (
    id, category_id, commission_rate_bps, effective_from, created_by, note, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, seq, category_id, commission_rate_bps, effective_from, created_by, note, created_at;

-- name: CountCategoryFeeSchedules :one
SELECT COUNT(*)::bigint AS cnt
FROM category_fee_schedules
WHERE (sqlc.arg(category_id)::text = '' OR category_id = sqlc.arg(category_id)::text);

-- name: ListCategoryFeeSchedules :many
SELECT id, seq, category_id, commission_rate_bps, effective_from, created_by, note, created_at
FROM category_fee_schedules
WHERE (sqlc.arg(category_id)::text = '' OR category_id = sqlc.arg(category_id)::text)
ORDER BY effective_from DESC, seq DESC
LIMIT sqlc.arg(limit_val)::int
OFFSET sqlc.arg(offset_val)::int;

-- Products

-- name: CreateProduct :one
INSERT INTO products (
    id, shop_id, category_id, name, description, images, status,
    rejection_reason, min_price_vnd, max_price_vnd, slug, search_text, flagged_keywords, shop_suspended
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING id, shop_id, category_id, name, description, images, status,
          rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
          slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended;

-- name: GetProductByID :one
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
       slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended
FROM products
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetProductByIDForUpdate :one
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
       slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended
FROM products
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: GetProductByIDForUpdateAny :one
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
       slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended
FROM products
WHERE id = $1
FOR UPDATE;

-- name: GetProductBySlug :one
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
       slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended
FROM products
WHERE slug = $1 AND deleted_at IS NULL;

-- name: GetDeletedApprovedProductBySlug :one
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
       slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended
FROM products
WHERE slug = $1
  AND deleted_at IS NOT NULL
  AND first_approved_at IS NOT NULL
ORDER BY deleted_at DESC, id DESC
LIMIT 1;

-- name: UpdateProduct :one
UPDATE products
SET category_id = $2,
    name = $3,
    description = $4,
    images = $5,
    status = $6,
    rejection_reason = $7,
    min_price_vnd = $8,
    max_price_vnd = $9,
    slug = $10,
    search_text = $11,
    flagged_keywords = $12,
    first_approved_at = CASE
        WHEN $6 IN ('APPROVED', 'OUT_OF_STOCK') AND first_approved_at IS NULL THEN NOW()
        ELSE first_approved_at
    END,
    updated_at = GREATEST(clock_timestamp(), updated_at + INTERVAL '1 microsecond')
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, shop_id, category_id, name, description, images, status,
          rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
          slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended;

-- name: UpdateProductReview :one
UPDATE products
SET status = $2,
    rejection_reason = $3,
    flagged_keywords = $4,
    first_approved_at = CASE
        WHEN $2 IN ('APPROVED', 'OUT_OF_STOCK') AND first_approved_at IS NULL THEN NOW()
        ELSE first_approved_at
    END,
    updated_at = GREATEST(clock_timestamp(), updated_at + INTERVAL '1 microsecond')
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, shop_id, category_id, name, description, images, status,
          rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
          slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended;

-- name: IncrementProductRating :one
UPDATE products
SET rating_sum = rating_sum + sqlc.arg(rating_delta)::bigint,
    review_count = review_count + 1
WHERE id = sqlc.arg(id)::text
RETURNING rating_sum, review_count;

-- name: ListProductsForModerationScan :many
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
       slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended
FROM products
WHERE deleted_at IS NULL
  AND (sqlc.arg(cursor_id)::text = '' OR id > sqlc.arg(cursor_id)::text)
ORDER BY id ASC
LIMIT sqlc.arg(limit_val)::int;

-- name: UpdateProductStatus :exec
UPDATE products
SET status = $2,
    updated_at = GREATEST(clock_timestamp(), updated_at + INTERVAL '1 microsecond')
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteProduct :one
UPDATE products
SET deleted_at = NOW(),
    status = 'HIDDEN',
    updated_at = GREATEST(clock_timestamp(), updated_at + INTERVAL '1 microsecond')
WHERE id = $1 AND deleted_at IS NULL
RETURNING updated_at;

-- name: CountProductsFiltered :one
WITH RECURSIVE cat_tree AS (
    SELECT c.id
    FROM categories c
    WHERE sqlc.arg(category_id)::text <> '' AND c.id = sqlc.arg(category_id)::text
    UNION
    SELECT child.id
    FROM categories child
    JOIN cat_tree ct ON child.parent_id = ct.id
)
SELECT COUNT(*)::bigint AS cnt
FROM products p
WHERE p.deleted_at IS NULL
  AND (sqlc.arg(category_id)::text = '' OR p.category_id IN (SELECT id FROM cat_tree))
  AND (
    (sqlc.arg(keyword)::text = '' AND sqlc.arg(sku_keyword)::text = '')
    OR (sqlc.arg(keyword)::text <> '' AND p.search_text LIKE '%' || sqlc.arg(keyword)::text || '%' ESCAPE '\')
    OR (
      sqlc.arg(sku_keyword)::text <> ''
      AND EXISTS (
        SELECT 1 FROM skus s
        WHERE s.product_id = p.id
          AND s.status <> 'INACTIVE'
          AND LOWER(s.sku_code) LIKE '%' || sqlc.arg(sku_keyword)::text || '%' ESCAPE '\'
      )
    )
  )
  AND (sqlc.arg(shop_id)::text = '' OR p.shop_id = sqlc.arg(shop_id)::text)
  AND (NOT sqlc.arg(public_only)::bool OR (p.status IN ('APPROVED', 'OUT_OF_STOCK') AND p.shop_suspended = FALSE))
  AND (sqlc.arg(status_filter)::text = '' OR p.status = sqlc.arg(status_filter)::text)
  AND (
    NOT sqlc.arg(low_stock)::bool
    OR (
      p.status IN ('APPROVED', 'OUT_OF_STOCK')
      AND EXISTS (
        SELECT 1 FROM skus s
        WHERE s.product_id = p.id
          AND s.status <> 'INACTIVE'
          AND (s.stock - s.reserved_stock - s.flash_sale_reserved_stock) <= 5
      )
    )
  )
  AND (
    (sqlc.arg(min_price_vnd)::bigint <= 0 AND sqlc.arg(max_price_vnd)::bigint <= 0)
    OR EXISTS (
      SELECT 1 FROM skus s
      WHERE s.product_id = p.id
        AND s.status <> 'INACTIVE'
        AND (sqlc.arg(min_price_vnd)::bigint <= 0 OR s.price_vnd >= sqlc.arg(min_price_vnd)::bigint)
        AND (sqlc.arg(max_price_vnd)::bigint <= 0 OR s.price_vnd <= sqlc.arg(max_price_vnd)::bigint)
    )
  );

-- name: ListProductsFiltered :many
WITH RECURSIVE cat_tree AS (
    SELECT c.id
    FROM categories c
    WHERE sqlc.arg(category_id)::text <> '' AND c.id = sqlc.arg(category_id)::text
    UNION
    SELECT child.id
    FROM categories child
    JOIN cat_tree ct ON child.parent_id = ct.id
)
SELECT p.id, p.shop_id, p.category_id, p.name, p.description, p.images, p.status,
       p.rejection_reason, p.min_price_vnd, p.max_price_vnd, p.deleted_at, p.created_at, p.updated_at,
       p.slug, p.search_text, p.rating_sum, p.review_count, p.flagged_keywords, p.first_approved_at, p.shop_suspended
FROM products p
WHERE p.deleted_at IS NULL
  AND (sqlc.arg(category_id)::text = '' OR p.category_id IN (SELECT id FROM cat_tree))
  AND (
    (sqlc.arg(keyword)::text = '' AND sqlc.arg(sku_keyword)::text = '')
    OR (sqlc.arg(keyword)::text <> '' AND p.search_text LIKE '%' || sqlc.arg(keyword)::text || '%' ESCAPE '\')
    OR (
      sqlc.arg(sku_keyword)::text <> ''
      AND EXISTS (
        SELECT 1 FROM skus s
        WHERE s.product_id = p.id
          AND s.status <> 'INACTIVE'
          AND LOWER(s.sku_code) LIKE '%' || sqlc.arg(sku_keyword)::text || '%' ESCAPE '\'
      )
    )
  )
  AND (sqlc.arg(shop_id)::text = '' OR p.shop_id = sqlc.arg(shop_id)::text)
  AND (NOT sqlc.arg(public_only)::bool OR (p.status IN ('APPROVED', 'OUT_OF_STOCK') AND p.shop_suspended = FALSE))
  AND (sqlc.arg(status_filter)::text = '' OR p.status = sqlc.arg(status_filter)::text)
  AND (
    NOT sqlc.arg(low_stock)::bool
    OR (
      p.status IN ('APPROVED', 'OUT_OF_STOCK')
      AND EXISTS (
        SELECT 1 FROM skus s
        WHERE s.product_id = p.id
          AND s.status <> 'INACTIVE'
          AND (s.stock - s.reserved_stock - s.flash_sale_reserved_stock) <= 5
      )
    )
  )
  AND (
    (sqlc.arg(min_price_vnd)::bigint <= 0 AND sqlc.arg(max_price_vnd)::bigint <= 0)
    OR EXISTS (
      SELECT 1 FROM skus s
      WHERE s.product_id = p.id
        AND s.status <> 'INACTIVE'
        AND (sqlc.arg(min_price_vnd)::bigint <= 0 OR s.price_vnd >= sqlc.arg(min_price_vnd)::bigint)
        AND (sqlc.arg(max_price_vnd)::bigint <= 0 OR s.price_vnd <= sqlc.arg(max_price_vnd)::bigint)
    )
  )
ORDER BY
  CASE WHEN sqlc.arg(sort_by)::text = 'price_asc' THEN p.min_price_vnd END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'price_desc' THEN p.max_price_vnd END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'name_asc' THEN p.name END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'oldest' THEN p.created_at END ASC,
  p.created_at DESC,
  p.id DESC
LIMIT sqlc.arg(limit_val)::int
OFFSET sqlc.arg(offset_val)::int;

-- name: CountProductsBySKUIDs :one
WITH RECURSIVE cat_tree AS (
    SELECT c.id
    FROM categories c
    WHERE sqlc.arg(category_id)::text <> '' AND c.id = sqlc.arg(category_id)::text
    UNION
    SELECT child.id
    FROM categories child
    JOIN cat_tree ct ON child.parent_id = ct.id
),
matched_products AS (
    SELECT DISTINCT s.product_id
    FROM skus s
    WHERE s.id = ANY(sqlc.arg(sku_ids)::text[])
      AND s.status <> 'INACTIVE'
)
SELECT COUNT(*)::bigint AS cnt
FROM matched_products mp
JOIN products p ON p.id = mp.product_id
WHERE p.deleted_at IS NULL
  AND p.status IN ('APPROVED', 'OUT_OF_STOCK')
  AND p.shop_suspended = FALSE
  AND (sqlc.arg(category_id)::text = '' OR p.category_id IN (SELECT id FROM cat_tree))
  AND (sqlc.arg(keyword)::text = '' OR p.search_text LIKE '%' || sqlc.arg(keyword)::text || '%' ESCAPE '\')
  AND (sqlc.arg(shop_id)::text = '' OR p.shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(status_filter)::text = '' OR p.status = sqlc.arg(status_filter)::text)
  AND (
    (sqlc.arg(min_price_vnd)::bigint <= 0 AND sqlc.arg(max_price_vnd)::bigint <= 0)
    OR EXISTS (
      SELECT 1 FROM skus s
      WHERE s.product_id = p.id
        AND s.status <> 'INACTIVE'
        AND (sqlc.arg(min_price_vnd)::bigint <= 0 OR s.price_vnd >= sqlc.arg(min_price_vnd)::bigint)
        AND (sqlc.arg(max_price_vnd)::bigint <= 0 OR s.price_vnd <= sqlc.arg(max_price_vnd)::bigint)
    )
  );

-- name: ListProductsBySKUIDs :many
WITH RECURSIVE cat_tree AS (
    SELECT c.id
    FROM categories c
    WHERE sqlc.arg(category_id)::text <> '' AND c.id = sqlc.arg(category_id)::text
    UNION
    SELECT child.id
    FROM categories child
    JOIN cat_tree ct ON child.parent_id = ct.id
),
matched_products AS (
    SELECT DISTINCT s.product_id
    FROM skus s
    WHERE s.id = ANY(sqlc.arg(sku_ids)::text[])
      AND s.status <> 'INACTIVE'
)
SELECT p.id, p.shop_id, p.category_id, p.name, p.description, p.images, p.status,
       p.rejection_reason, p.min_price_vnd, p.max_price_vnd, p.deleted_at, p.created_at, p.updated_at,
       p.slug, p.search_text, p.rating_sum, p.review_count, p.flagged_keywords, p.first_approved_at, p.shop_suspended
FROM matched_products mp
JOIN products p ON p.id = mp.product_id
WHERE p.deleted_at IS NULL
  AND p.status IN ('APPROVED', 'OUT_OF_STOCK')
  AND p.shop_suspended = FALSE
  AND (sqlc.arg(category_id)::text = '' OR p.category_id IN (SELECT id FROM cat_tree))
  AND (sqlc.arg(keyword)::text = '' OR p.search_text LIKE '%' || sqlc.arg(keyword)::text || '%' ESCAPE '\')
  AND (sqlc.arg(shop_id)::text = '' OR p.shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(status_filter)::text = '' OR p.status = sqlc.arg(status_filter)::text)
  AND (
    (sqlc.arg(min_price_vnd)::bigint <= 0 AND sqlc.arg(max_price_vnd)::bigint <= 0)
    OR EXISTS (
      SELECT 1 FROM skus s
      WHERE s.product_id = p.id
        AND s.status <> 'INACTIVE'
        AND (sqlc.arg(min_price_vnd)::bigint <= 0 OR s.price_vnd >= sqlc.arg(min_price_vnd)::bigint)
        AND (sqlc.arg(max_price_vnd)::bigint <= 0 OR s.price_vnd <= sqlc.arg(max_price_vnd)::bigint)
    )
  )
ORDER BY
  CASE WHEN sqlc.arg(sort_by)::text = 'price_asc' THEN p.min_price_vnd END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'price_desc' THEN p.max_price_vnd END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'name_asc' THEN p.name END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'oldest' THEN p.created_at END ASC,
  p.created_at DESC,
  p.id DESC
LIMIT sqlc.arg(limit_val)::int
OFFSET sqlc.arg(offset_val)::int;

-- SKUs

-- name: CreateSKU :one
INSERT INTO skus (
    id, product_id, sku_code, name, attributes, price_vnd, stock,
    reserved_stock, flash_sale_reserved_stock, version, weight_grams, image_url, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 0, 1, $8, $9, $10)
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at, flash_sale_reserved_stock;

-- name: InsertSKUPriceHistory :exec
INSERT INTO sku_price_history (id, sku_id, price_vnd, actor_id, reason, recorded_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpdateSKUMetadata :one
UPDATE skus
SET sku_code = $2,
    name = $3,
    attributes = $4,
    price_vnd = $5,
    weight_grams = $6,
    image_url = $7,
    updated_at = NOW()
WHERE id = $1
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at, flash_sale_reserved_stock;

-- name: ReactivateSKUOptimistic :one
UPDATE skus
SET sku_code = $2,
    name = $3,
    attributes = $4,
    price_vnd = $5,
    stock = $6,
    weight_grams = $7,
    image_url = $8,
    status = CASE
        WHEN $6 = 0 THEN 'OUT_OF_STOCK'
        ELSE 'ACTIVE'
    END,
    version = version + 1,
    updated_at = NOW()
WHERE id = $1
  AND version = $9
  AND status = 'INACTIVE'
  AND $6 >= (reserved_stock + flash_sale_reserved_stock)
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at, flash_sale_reserved_stock;

-- name: DeactivateSKUsByProduct :exec
UPDATE skus
SET status = 'INACTIVE',
    updated_at = NOW()
WHERE product_id = $1;

-- name: DeactivateSKU :exec
UPDATE skus
SET status = 'INACTIVE',
    updated_at = NOW()
WHERE id = $1;

-- name: ListSKUsByProductIDs :many
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.flash_sale_reserved_stock, s.version,
    s.weight_grams, s.image_url, p.category_id,
    COALESCE(
        (
            SELECT f.commission_rate_bps
            FROM category_fee_schedules f
            WHERE f.category_id = c.id
              AND f.effective_from <= sqlc.arg(as_of)::timestamptz
            ORDER BY f.effective_from DESC, f.seq DESC
            LIMIT 1
        ),
        c.commission_rate_bps
    )::int AS commission_rate_bps,
    COALESCE(
        LEAST(
            s.price_vnd,
            (
                SELECT MIN(h.price_vnd)
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at > sqlc.arg(cutoff_30d)::timestamptz
                  AND h.recorded_at <= sqlc.arg(as_of)::timestamptz
            ),
            (
                SELECT h.price_vnd
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at <= sqlc.arg(cutoff_30d)::timestamptz
                ORDER BY h.recorded_at DESC, h.seq DESC
                LIMIT 1
            )
        ),
        s.price_vnd
    )::bigint AS min_price_30d_vnd,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at, p.shop_suspended AS product_shop_suspended
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.product_id = ANY(sqlc.arg(product_ids)::text[])
  AND s.status <> 'INACTIVE'
ORDER BY s.created_at ASC, s.id ASC;

-- name: ListModerationSKUsByProductIDs :many
SELECT product_id, name, attributes
FROM skus
WHERE product_id = ANY(sqlc.arg(product_ids)::text[])
  AND status <> 'INACTIVE'
ORDER BY created_at ASC, id ASC;

-- name: ListAllSKUsByProductID :many
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.flash_sale_reserved_stock, s.version,
    s.weight_grams, s.image_url, p.category_id,
    COALESCE(
        (
            SELECT f.commission_rate_bps
            FROM category_fee_schedules f
            WHERE f.category_id = c.id
              AND f.effective_from <= sqlc.arg(as_of)::timestamptz
            ORDER BY f.effective_from DESC, f.seq DESC
            LIMIT 1
        ),
        c.commission_rate_bps
    )::int AS commission_rate_bps,
    COALESCE(
        LEAST(
            s.price_vnd,
            (
                SELECT MIN(h.price_vnd)
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at > sqlc.arg(cutoff_30d)::timestamptz
                  AND h.recorded_at <= sqlc.arg(as_of)::timestamptz
            ),
            (
                SELECT h.price_vnd
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at <= sqlc.arg(cutoff_30d)::timestamptz
                ORDER BY h.recorded_at DESC, h.seq DESC
                LIMIT 1
            )
        ),
        s.price_vnd
    )::bigint AS min_price_30d_vnd,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at, p.shop_suspended AS product_shop_suspended
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.product_id = sqlc.arg(product_id)::text
ORDER BY s.created_at ASC, s.id ASC;

-- name: GetSKUsBatch :many
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.flash_sale_reserved_stock, s.version,
    s.weight_grams, s.image_url, p.category_id,
    COALESCE(
        (
            SELECT f.commission_rate_bps
            FROM category_fee_schedules f
            WHERE f.category_id = c.id
              AND f.effective_from <= sqlc.arg(as_of)::timestamptz
            ORDER BY f.effective_from DESC, f.seq DESC
            LIMIT 1
        ),
        c.commission_rate_bps
    )::int AS commission_rate_bps,
    COALESCE(
        LEAST(
            s.price_vnd,
            (
                SELECT MIN(h.price_vnd)
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at > sqlc.arg(cutoff_30d)::timestamptz
                  AND h.recorded_at <= sqlc.arg(as_of)::timestamptz
            ),
            (
                SELECT h.price_vnd
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at <= sqlc.arg(cutoff_30d)::timestamptz
                ORDER BY h.recorded_at DESC, h.seq DESC
                LIMIT 1
            )
        ),
        s.price_vnd
    )::bigint AS min_price_30d_vnd,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at, p.shop_suspended AS product_shop_suspended
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.id = ANY(sqlc.arg(sku_ids)::text[])
ORDER BY s.id ASC;

-- name: GetSKUWithContext :one
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.flash_sale_reserved_stock, s.version,
    s.weight_grams, s.image_url, p.category_id,
    COALESCE(
        (
            SELECT f.commission_rate_bps
            FROM category_fee_schedules f
            WHERE f.category_id = c.id
              AND f.effective_from <= sqlc.arg(as_of)::timestamptz
            ORDER BY f.effective_from DESC, f.seq DESC
            LIMIT 1
        ),
        c.commission_rate_bps
    )::int AS commission_rate_bps,
    COALESCE(
        LEAST(
            s.price_vnd,
            (
                SELECT MIN(h.price_vnd)
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at > sqlc.arg(cutoff_30d)::timestamptz
                  AND h.recorded_at <= sqlc.arg(as_of)::timestamptz
            ),
            (
                SELECT h.price_vnd
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at <= sqlc.arg(cutoff_30d)::timestamptz
                ORDER BY h.recorded_at DESC, h.seq DESC
                LIMIT 1
            )
        ),
        s.price_vnd
    )::bigint AS min_price_30d_vnd,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at, p.shop_suspended AS product_shop_suspended
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.id = sqlc.arg(id)::text;

-- name: GetSKUWithContextForUpdate :one
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.flash_sale_reserved_stock, s.version,
    s.weight_grams, s.image_url, p.category_id,
    COALESCE(
        (
            SELECT f.commission_rate_bps
            FROM category_fee_schedules f
            WHERE f.category_id = c.id
              AND f.effective_from <= sqlc.arg(as_of)::timestamptz
            ORDER BY f.effective_from DESC, f.seq DESC
            LIMIT 1
        ),
        c.commission_rate_bps
    )::int AS commission_rate_bps,
    COALESCE(
        LEAST(
            s.price_vnd,
            (
                SELECT MIN(h.price_vnd)
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at > sqlc.arg(cutoff_30d)::timestamptz
                  AND h.recorded_at <= sqlc.arg(as_of)::timestamptz
            ),
            (
                SELECT h.price_vnd
                FROM sku_price_history h
                WHERE h.sku_id = s.id
                  AND h.recorded_at <= sqlc.arg(cutoff_30d)::timestamptz
                ORDER BY h.recorded_at DESC, h.seq DESC
                LIMIT 1
            )
        ),
        s.price_vnd
    )::bigint AS min_price_30d_vnd,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at, p.shop_suspended AS product_shop_suspended
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.id = sqlc.arg(id)::text
FOR UPDATE OF s;

-- name: CountActiveInStockSKUsByProduct :one
SELECT COUNT(*)::bigint AS cnt
FROM skus
WHERE product_id = $1 AND status <> 'INACTIVE' AND stock > 0;

-- Stock optimistic locking

-- name: UpdateSKUStockOptimistic :one
UPDATE skus
SET stock = $1,
    status = CASE
        WHEN $1 = 0 THEN 'OUT_OF_STOCK'
        ELSE 'ACTIVE'
    END,
    version = version + 1,
    updated_at = NOW()
WHERE id = $2
  AND version = $3
  AND status <> 'INACTIVE'
  AND $1 >= (reserved_stock + flash_sale_reserved_stock)
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at, flash_sale_reserved_stock;

-- name: ReserveSKUOptimistic :one
UPDATE skus
SET reserved_stock = reserved_stock + $1,
    version = version + 1,
    updated_at = NOW()
WHERE id = $2
  AND version = $3
  AND (stock - reserved_stock - flash_sale_reserved_stock) >= $1
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at, flash_sale_reserved_stock;

-- name: ReleaseSKUOptimistic :one
UPDATE skus
SET reserved_stock = GREATEST(0, reserved_stock - $1),
    version = version + 1,
    updated_at = NOW()
WHERE id = $2
  AND version = $3
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at, flash_sale_reserved_stock;

-- name: ConfirmSKUOptimistic :one
UPDATE skus
SET stock = GREATEST(0, stock - $1),
    reserved_stock = GREATEST(0, reserved_stock - $1),
    status = CASE
        WHEN status = 'INACTIVE' THEN 'INACTIVE'
        WHEN GREATEST(0, stock - $1) = 0 THEN 'OUT_OF_STOCK'
        ELSE status
    END,
    version = version + 1,
    updated_at = NOW()
WHERE id = $2
  AND version = $3
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at, flash_sale_reserved_stock;

-- name: RestockSKUAtomic :one
UPDATE skus
SET stock = stock + $1,
    status = CASE
        WHEN status = 'INACTIVE' THEN 'INACTIVE'
        WHEN stock + $1 > 0 THEN 'ACTIVE'
        ELSE status
    END,
    version = version + 1,
    updated_at = NOW()
WHERE id = $2
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at, flash_sale_reserved_stock;

-- Stock reservations

-- name: LockStockOrder :exec
SELECT pg_advisory_xact_lock(1937010539, hashtext($1));

-- name: InsertStockReleaseTombstone :execrows
INSERT INTO stock_release_tombstones (order_id)
VALUES ($1)
ON CONFLICT (order_id) DO NOTHING;

-- name: HasStockReleaseTombstone :one
SELECT EXISTS (
    SELECT 1 FROM stock_release_tombstones WHERE order_id = $1
)::bool AS exists;

-- name: ListReservationsByOrderID :many
SELECT id, reservation_group_id, order_id, sku_id, quantity, status, created_at, updated_at, flash_sale_item_id
FROM stock_reservations
WHERE order_id = $1
ORDER BY sku_id ASC;

-- name: ListReservationsByOrderIDForUpdate :many
SELECT id, reservation_group_id, order_id, sku_id, quantity, status, created_at, updated_at, flash_sale_item_id
FROM stock_reservations
WHERE order_id = $1
ORDER BY sku_id ASC
FOR UPDATE;

-- name: CreateStockReservation :one
INSERT INTO stock_reservations (id, reservation_group_id, order_id, sku_id, quantity, status, flash_sale_item_id)
VALUES ($1, $2, $3, $4, $5, 'RESERVED', $6)
RETURNING id, reservation_group_id, order_id, sku_id, quantity, status, created_at, updated_at, flash_sale_item_id;

-- name: TransitionStockReservation :execrows
UPDATE stock_reservations
SET status = $2,
    updated_at = NOW()
WHERE id = $1 AND status = $3;

-- Stock history

-- name: InsertStockHistory :exec
INSERT INTO stock_history (
    id, sku_id, order_id, change_type, delta,
    stock_before, stock_after, reserved_before, reserved_after,
    actor_id, reason
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: TryInsertRestockHistory :execrows
INSERT INTO stock_history (
    id, sku_id, order_id, change_type, delta,
    stock_before, stock_after, reserved_before, reserved_after,
    actor_id, reason
)
VALUES ($1, $2, $3, 'RESTOCK', $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (order_id, sku_id) WHERE change_type = 'RESTOCK' DO NOTHING;

-- Outbox events

-- name: NextProductEventVersion :one
SELECT nextval('product_event_version_seq')::bigint AS version;

-- name: LockOutboxClaim :exec
SELECT pg_advisory_xact_lock(7956455656534110829);

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (id, event_type, aggregate_id, trace_id, traceparent, payload_json, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- ponytail: LIMIT applies on locked before eligible; if an aggregate's head row is briefly row-locked
-- (e.g. concurrent MarkPublished) with >=50 queued rows behind it, one 250ms poll may claim 0 rows and
-- recover on the next tick once the short update commits (accepted D9-bis ceiling).
-- name: ClaimPendingOutboxEvents :many
WITH locked AS (
    SELECT o.id, o.aggregate_id, o.seq
    FROM outbox_events o
    WHERE o.published_at IS NULL
      AND (o.claimed_until IS NULL OR o.claimed_until <= NOW())
      AND (
          o.aggregate_id = ''
          OR NOT EXISTS (
              SELECT 1
              FROM outbox_events earlier
              WHERE earlier.aggregate_id = o.aggregate_id
                AND earlier.published_at IS NULL
                AND earlier.claimed_until > NOW()
                AND earlier.seq < o.seq
          )
      )
    ORDER BY o.seq ASC
    LIMIT sqlc.arg(batch_limit)::int
    FOR UPDATE OF o SKIP LOCKED
),
eligible AS (
    SELECT l.id
    FROM locked l
    WHERE l.aggregate_id = ''
       OR NOT EXISTS (
           SELECT 1
           FROM outbox_events earlier
           WHERE earlier.aggregate_id = l.aggregate_id
             AND earlier.published_at IS NULL
             AND earlier.seq < l.seq
             AND NOT EXISTS (SELECT 1 FROM locked l2 WHERE l2.id = earlier.id)
       )
)
UPDATE outbox_events o
SET claimed_until = NOW() + (sqlc.arg(lease_ms)::bigint * INTERVAL '1 millisecond'),
    claim_token = sqlc.arg(claim_token)::text
FROM eligible e
WHERE o.id = e.id
RETURNING o.id, o.event_type, o.aggregate_id, o.trace_id, o.traceparent, o.payload_json,
          o.published_at, o.attempts, o.last_error, o.created_at, o.seq, o.claimed_until, o.claim_token;

-- name: MarkOutboxEventPublished :execrows
UPDATE outbox_events
SET published_at = NOW(),
    attempts = attempts + 1,
    last_error = '',
    claimed_until = NULL,
    claim_token = ''
WHERE id = $1
  AND claim_token = $2
  AND published_at IS NULL;

-- name: MarkOutboxEventAttemptFailed :one
UPDATE outbox_events
SET attempts = attempts + 1,
    last_error = sqlc.arg(last_error)::text,
    claimed_until = CASE
        WHEN sqlc.arg(backoff_ms)::bigint > 0
        THEN NOW() + (sqlc.arg(backoff_ms)::bigint * INTERVAL '1 millisecond')
        ELSE NULL
    END,
    claim_token = ''
WHERE id = sqlc.arg(id)::text
  AND claim_token = sqlc.arg(claim_token)::text
  AND published_at IS NULL
RETURNING attempts;

-- name: ReleaseOutboxEventClaims :execrows
UPDATE outbox_events
SET claimed_until = NULL,
    claim_token = ''
WHERE id = ANY(sqlc.arg(ids)::text[])
  AND claim_token = sqlc.arg(claim_token)::text
  AND published_at IS NULL;

-- Product Reviews (E5/E6/E7/E8)

-- name: CreateProductReview :one
INSERT INTO product_reviews (
    id, product_id, shop_id, order_id, order_item_id, sku_id, sku_name,
    buyer_id, buyer_name, rating, comment, image_urls, status, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
RETURNING id, product_id, shop_id, order_id, order_item_id, sku_id, sku_name,
          buyer_id, buyer_name, rating, comment, image_urls, status, created_at, updated_at;

-- name: CountProductReviews :one
SELECT COUNT(*)::bigint AS cnt
FROM product_reviews
WHERE product_id = sqlc.arg(product_id)::text
  AND status = 'PUBLISHED'
  AND (sqlc.arg(rating_filter)::int <= 0 OR rating = sqlc.arg(rating_filter)::int)
  AND (NOT sqlc.arg(has_images)::bool OR jsonb_array_length(image_urls) > 0);

-- name: ListProductReviews :many
SELECT id, product_id, shop_id, order_id, order_item_id, sku_id, sku_name,
       buyer_id, buyer_name, rating, comment, image_urls, status, created_at, updated_at
FROM product_reviews
WHERE product_id = sqlc.arg(product_id)::text
  AND status = 'PUBLISHED'
  AND (sqlc.arg(rating_filter)::int <= 0 OR rating = sqlc.arg(rating_filter)::int)
  AND (NOT sqlc.arg(has_images)::bool OR jsonb_array_length(image_urls) > 0)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_val)::int
OFFSET sqlc.arg(offset_val)::int;

-- name: GetProductReviewRatingCounts :many
SELECT rating::int AS rating, COUNT(*)::int AS cnt
FROM product_reviews
WHERE product_id = $1
  AND status = 'PUBLISHED'
GROUP BY rating;

-- name: ListReviewsByOrderAndBuyer :many
SELECT id, product_id, shop_id, order_id, order_item_id, sku_id, sku_name,
       buyer_id, buyer_name, rating, comment, image_urls, status, created_at, updated_at
FROM product_reviews
WHERE order_id = $1
  AND buyer_id = $2
ORDER BY created_at ASC, id ASC;

-- Banned Keywords (E9/E10/E11)

-- name: CreateBannedKeyword :one
INSERT INTO banned_keywords (
    id, term, normalized_term, category, action, is_active, created_by, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING id, term, normalized_term, category, action, is_active, created_by, created_at, updated_at;

-- name: GetBannedKeywordByID :one
SELECT id, term, normalized_term, category, action, is_active, created_by, created_at, updated_at
FROM banned_keywords
WHERE id = $1;

-- name: UpdateBannedKeyword :one
UPDATE banned_keywords
SET term = $2,
    normalized_term = $3,
    category = $4,
    action = $5,
    is_active = $6,
    updated_at = NOW()
WHERE id = $1
RETURNING id, term, normalized_term, category, action, is_active, created_by, created_at, updated_at;

-- name: DeleteBannedKeyword :execrows
DELETE FROM banned_keywords
WHERE id = $1;

-- name: CountBannedKeywords :one
SELECT COUNT(*)::bigint AS cnt
FROM banned_keywords
WHERE (sqlc.arg(include_inactive)::bool OR is_active = TRUE)
  AND (sqlc.arg(action_filter)::text = '' OR action = sqlc.arg(action_filter)::text)
  AND (
      sqlc.arg(query)::text = ''
      OR LOWER(term) LIKE '%' || sqlc.arg(query)::text || '%' ESCAPE '\'
      OR normalized_term LIKE '%' || sqlc.arg(query)::text || '%' ESCAPE '\'
  );

-- name: ListBannedKeywords :many
SELECT id, term, normalized_term, category, action, is_active, created_by, created_at, updated_at
FROM banned_keywords
WHERE (sqlc.arg(include_inactive)::bool OR is_active = TRUE)
  AND (sqlc.arg(action_filter)::text = '' OR action = sqlc.arg(action_filter)::text)
  AND (
      sqlc.arg(query)::text = ''
      OR LOWER(term) LIKE '%' || sqlc.arg(query)::text || '%' ESCAPE '\'
      OR normalized_term LIKE '%' || sqlc.arg(query)::text || '%' ESCAPE '\'
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_val)::int
OFFSET sqlc.arg(offset_val)::int;

-- name: ListActiveBannedKeywords :many
SELECT id, term, normalized_term, category, action, is_active, created_by, created_at, updated_at
FROM banned_keywords
WHERE is_active = TRUE
ORDER BY id ASC;

-- Shop suspension state & event consumption (E1/F12)

-- name: LockShopSuspension :exec
SELECT pg_advisory_xact_lock(1937010547, hashtext($1));

-- name: GetShopSuspensionState :one
SELECT shop_id, suspended, version, updated_at
FROM shop_suspension_state
WHERE shop_id = $1;

-- name: UpsertShopSuspensionState :one
INSERT INTO shop_suspension_state (shop_id, suspended, version, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (shop_id) DO UPDATE
SET suspended = EXCLUDED.suspended,
    version = EXCLUDED.version,
    updated_at = NOW()
WHERE shop_suspension_state.version < EXCLUDED.version
RETURNING shop_id, suspended, version, updated_at;

-- name: SetProductsShopSuspendedByShop :exec
WITH target AS (
    SELECT pr.id
    FROM products pr
    WHERE pr.shop_id = $1
      AND pr.shop_suspended IS DISTINCT FROM $2
    ORDER BY pr.id ASC
    FOR UPDATE OF pr
)
UPDATE products p
SET shop_suspended = $2
FROM target t
WHERE p.id = t.id;

-- name: TryInsertProcessedEvent :execrows
INSERT INTO processed_events (event_id, event_type)
VALUES ($1, $2)
ON CONFLICT (event_id) DO NOTHING;

-- name: InsertDeadLetterEvent :exec
INSERT INTO dead_letter_events (id, subject, event_id, payload, error_message, attempts)
VALUES ($1, $2, $3, $4, $5, $6);

-- Stock History Query (S03/G8)

-- name: GetProductShopIDAny :one
SELECT shop_id
FROM products
WHERE id = $1;

-- name: CountStockHistory :one
SELECT COUNT(*)::bigint AS cnt
FROM stock_history sh
JOIN skus s ON s.id = sh.sku_id
JOIN products p ON p.id = s.product_id
WHERE (sqlc.arg(shop_id)::text = '' OR p.shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(product_id)::text = '' OR s.product_id = sqlc.arg(product_id)::text)
  AND (sqlc.arg(sku_id)::text = '' OR sh.sku_id = sqlc.arg(sku_id)::text)
  AND (sqlc.arg(change_type)::text = '' OR sh.change_type = sqlc.arg(change_type)::text)
  AND (NOT sqlc.arg(has_from_time)::bool OR sh.created_at >= sqlc.arg(from_time)::timestamptz)
  AND (NOT sqlc.arg(has_to_time)::bool OR sh.created_at <= sqlc.arg(to_time)::timestamptz);

-- name: ListStockHistory :many
SELECT
    sh.id, sh.sku_id, s.product_id, p.shop_id,
    p.name AS product_name, s.sku_code, s.name AS sku_name,
    sh.order_id, sh.change_type, sh.delta,
    sh.stock_before, sh.stock_after,
    sh.reserved_before, sh.reserved_after,
    sh.actor_id, sh.reason, sh.created_at
FROM stock_history sh
JOIN skus s ON s.id = sh.sku_id
JOIN products p ON p.id = s.product_id
WHERE (sqlc.arg(shop_id)::text = '' OR p.shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(product_id)::text = '' OR s.product_id = sqlc.arg(product_id)::text)
  AND (sqlc.arg(sku_id)::text = '' OR sh.sku_id = sqlc.arg(sku_id)::text)
  AND (sqlc.arg(change_type)::text = '' OR sh.change_type = sqlc.arg(change_type)::text)
  AND (NOT sqlc.arg(has_from_time)::bool OR sh.created_at >= sqlc.arg(from_time)::timestamptz)
  AND (NOT sqlc.arg(has_to_time)::bool OR sh.created_at <= sqlc.arg(to_time)::timestamptz)
ORDER BY sh.created_at DESC, sh.id DESC
LIMIT sqlc.arg(limit_val)::int
OFFSET sqlc.arg(offset_val)::int;

-- Product CSV Import Jobs & Rows (UC-S13/G6)

-- name: ListExistingShopSKUCodes :many
SELECT DISTINCT s.sku_code
FROM skus s
JOIN products p ON p.id = s.product_id
WHERE p.shop_id = sqlc.arg(shop_id)::text
  AND p.deleted_at IS NULL
  AND s.status <> 'INACTIVE'
  AND s.sku_code = ANY(sqlc.arg(sku_codes)::text[]);

-- name: CreateProductImportJob :one
INSERT INTO product_import_jobs (
    id, shop_id, created_by, filename, raw_csv, status, total_rows, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, 'QUEUED', $6, $7, $7)
RETURNING id, shop_id, created_by, filename, raw_csv, status,
          total_rows, valid_rows, invalid_rows, created_rows, failed_rows,
          error_message, claimed_until, claim_token, confirmed_at, finished_at, created_at, updated_at;

-- name: HasActiveProductImportJob :one
SELECT EXISTS (
    SELECT 1
    FROM product_import_jobs
    WHERE shop_id = sqlc.arg(shop_id)::text
      AND status IN ('QUEUED', 'VALIDATING', 'CONFIRMING')
      AND (sqlc.arg(exclude_job_id)::text = '' OR id <> sqlc.arg(exclude_job_id)::text)
);

-- name: GetProductImportJobByID :one
SELECT id, shop_id, created_by, filename, raw_csv, status,
       total_rows, valid_rows, invalid_rows, created_rows, failed_rows,
       error_message, claimed_until, claim_token, confirmed_at, finished_at, created_at, updated_at
FROM product_import_jobs
WHERE id = $1;

-- name: GetProductImportJobByIDForUpdate :one
SELECT id, shop_id, created_by, filename, raw_csv, status,
       total_rows, valid_rows, invalid_rows, created_rows, failed_rows,
       error_message, claimed_until, claim_token, confirmed_at, finished_at, created_at, updated_at
FROM product_import_jobs
WHERE id = $1
FOR UPDATE;

-- name: ClaimNextProductImportJob :one
WITH candidate AS (
    SELECT id
    FROM product_import_jobs
    WHERE status IN ('QUEUED', 'VALIDATING', 'CONFIRMING')
      AND (claimed_until IS NULL OR claimed_until <= NOW())
    ORDER BY created_at ASC, id ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE product_import_jobs j
SET status = CASE WHEN j.status = 'QUEUED' THEN 'VALIDATING' ELSE j.status END,
    claimed_until = NOW() + (sqlc.arg(lease_ms)::bigint * INTERVAL '1 millisecond'),
    claim_token = sqlc.arg(claim_token)::text,
    updated_at = NOW()
FROM candidate c
WHERE j.id = c.id
RETURNING j.id, j.shop_id, j.created_by, j.filename, j.raw_csv, j.status,
          j.total_rows, j.valid_rows, j.invalid_rows, j.created_rows, j.failed_rows,
          j.error_message, j.claimed_until, j.claim_token, j.confirmed_at, j.finished_at, j.created_at, j.updated_at;

-- name: MarkProductImportJobReady :one
UPDATE product_import_jobs
SET status = 'READY',
    total_rows = $3,
    valid_rows = $4,
    invalid_rows = $5,
    raw_csv = NULL,
    claimed_until = NULL,
    claim_token = '',
    error_message = '',
    updated_at = NOW()
WHERE id = $1
  AND claim_token = $2
  AND status = 'VALIDATING'
RETURNING id, shop_id, created_by, filename, raw_csv, status,
          total_rows, valid_rows, invalid_rows, created_rows, failed_rows,
          error_message, claimed_until, claim_token, confirmed_at, finished_at, created_at, updated_at;

-- name: MarkProductImportJobFailed :one
UPDATE product_import_jobs
SET status = 'FAILED',
    error_message = $3,
    raw_csv = NULL,
    claimed_until = NULL,
    claim_token = '',
    finished_at = NOW(),
    updated_at = NOW()
WHERE id = $1
  AND claim_token = $2
  AND status IN ('VALIDATING', 'CONFIRMING')
RETURNING id, shop_id, created_by, filename, raw_csv, status,
          total_rows, valid_rows, invalid_rows, created_rows, failed_rows,
          error_message, claimed_until, claim_token, confirmed_at, finished_at, created_at, updated_at;

-- name: TransitionProductImportJobConfirming :one
UPDATE product_import_jobs
SET status = 'CONFIRMING',
    confirmed_at = NOW(),
    claimed_until = NULL,
    claim_token = '',
    updated_at = NOW()
WHERE id = $1
  AND status = 'READY'
  AND valid_rows > 0
RETURNING id, shop_id, created_by, filename, raw_csv, status,
          total_rows, valid_rows, invalid_rows, created_rows, failed_rows,
          error_message, claimed_until, claim_token, confirmed_at, finished_at, created_at, updated_at;

-- name: UpdateProductImportJobProgress :execrows
UPDATE product_import_jobs
SET created_rows = created_rows + sqlc.arg(created_delta)::int,
    failed_rows = failed_rows + sqlc.arg(failed_delta)::int,
    claimed_until = NOW() + (sqlc.arg(lease_ms)::bigint * INTERVAL '1 millisecond'),
    updated_at = NOW()
WHERE id = sqlc.arg(id)::text
  AND claim_token = sqlc.arg(claim_token)::text
  AND status = 'CONFIRMING';

-- name: MarkProductImportJobCompleted :one
UPDATE product_import_jobs
SET status = 'COMPLETED',
    created_rows = $3,
    failed_rows = $4,
    raw_csv = NULL,
    claimed_until = NULL,
    claim_token = '',
    finished_at = NOW(),
    updated_at = NOW()
WHERE id = $1
  AND claim_token = $2
  AND status = 'CONFIRMING'
RETURNING id, shop_id, created_by, filename, raw_csv, status,
          total_rows, valid_rows, invalid_rows, created_rows, failed_rows,
          error_message, claimed_until, claim_token, confirmed_at, finished_at, created_at, updated_at;

-- name: DeleteProductImportRowsByJob :exec
DELETE FROM product_import_rows
WHERE job_id = $1;

-- name: InsertProductImportRow :exec
INSERT INTO product_import_rows (
    id, job_id, row_number, status, error_code, error_message,
    product_name, sku_code, category_id, price_vnd, stock, weight_grams,
    created_product_id, raw_row_json
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: CountProductImportRows :one
SELECT COUNT(*)::bigint AS cnt
FROM product_import_rows
WHERE job_id = sqlc.arg(job_id)::text
  AND (NOT sqlc.arg(errors_only)::bool OR status IN ('INVALID', 'FAILED'))
  AND (sqlc.arg(status_filter)::text = '' OR status = sqlc.arg(status_filter)::text);

-- name: ListProductImportRows :many
SELECT id, job_id, row_number, status, error_code, error_message,
       product_name, sku_code, category_id, price_vnd, stock, weight_grams,
       created_product_id, raw_row_json
FROM product_import_rows
WHERE job_id = sqlc.arg(job_id)::text
  AND (NOT sqlc.arg(errors_only)::bool OR status IN ('INVALID', 'FAILED'))
  AND (sqlc.arg(status_filter)::text = '' OR status = sqlc.arg(status_filter)::text)
ORDER BY row_number ASC
LIMIT sqlc.arg(limit_val)::int
OFFSET sqlc.arg(offset_val)::int;

-- name: ListValidProductImportRows :many
SELECT id, job_id, row_number, status, error_code, error_message,
       product_name, sku_code, category_id, price_vnd, stock, weight_grams,
       created_product_id, raw_row_json
FROM product_import_rows
WHERE job_id = $1
  AND status = 'VALID'
ORDER BY row_number ASC;

-- name: GetProductImportRowByIDForUpdate :one
SELECT id, job_id, row_number, status, error_code, error_message,
       product_name, sku_code, category_id, price_vnd, stock, weight_grams,
       created_product_id, raw_row_json
FROM product_import_rows
WHERE id = $1
FOR UPDATE;

-- name: UpdateProductImportRowOutcome :execrows
UPDATE product_import_rows
SET status = $2,
    error_code = $3,
    error_message = $4,
    created_product_id = $5
WHERE id = $1
  AND status = 'VALID';

-- name: GetProductImportRowOutcomeCounts :one
SELECT
    COUNT(*) FILTER (WHERE status = 'VALID')::int AS remaining_valid,
    COUNT(*) FILTER (WHERE status = 'CREATED')::int AS created_cnt,
    COUNT(*) FILTER (WHERE status = 'FAILED')::int AS failed_cnt
FROM product_import_rows
WHERE job_id = $1;

-- Search Indexing & Category Ancestors (B1/G9/G11)

-- name: ListProductsForIndex :many
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at,
       slug, search_text, rating_sum, review_count, flagged_keywords, first_approved_at, shop_suspended
FROM products
WHERE deleted_at IS NULL
  AND status IN ('APPROVED', 'OUT_OF_STOCK')
  AND (sqlc.arg(after_id)::text = '' OR id > sqlc.arg(after_id)::text)
ORDER BY id ASC
LIMIT sqlc.arg(limit_val)::int;

-- name: GetCategoryAncestors :many
WITH RECURSIVE anc AS (
    SELECT c.id, c.parent_id, 0 AS depth
    FROM categories c
    WHERE c.id = sqlc.arg(category_id)::text
    UNION ALL
    SELECT p.id, p.parent_id, a.depth + 1
    FROM categories p
    JOIN anc a ON p.id = a.parent_id
    WHERE a.depth < 32
)
SELECT id::text AS id
FROM anc
ORDER BY depth ASC;
