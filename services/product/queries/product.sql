-- Categories

-- name: LockCategoryTree :exec
SELECT pg_advisory_xact_lock(7956455656534110821);

-- name: CreateCategory :one
INSERT INTO categories (id, name, slug, parent_id, commission_rate_bps, sort_order)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, name, slug, parent_id, commission_rate_bps, sort_order, created_at, updated_at;

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
    c.sort_order, c.created_at, c.updated_at
FROM categories c
WHERE c.id = sqlc.arg(id)::text;

-- name: GetCategoryBySlug :one
SELECT id, name, slug, parent_id, commission_rate_bps, sort_order, created_at, updated_at
FROM categories
WHERE slug = $1;

-- name: UpdateCategory :one
UPDATE categories
SET name = $2,
    slug = $3,
    parent_id = $4,
    commission_rate_bps = $5,
    sort_order = $6,
    updated_at = NOW()
WHERE id = $1
RETURNING id, name, slug, parent_id, commission_rate_bps, sort_order, created_at, updated_at;

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
    c.sort_order, c.created_at, c.updated_at
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
    c.sort_order, c.created_at, c.updated_at
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
    c.sort_order, c.created_at, c.updated_at
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
    rejection_reason, min_price_vnd, max_price_vnd, slug, search_text
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING id, shop_id, category_id, name, description, images, status,
          rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at, slug, search_text;

-- name: GetProductByID :one
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at, slug, search_text
FROM products
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetProductByIDForUpdate :one
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at, slug, search_text
FROM products
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: GetProductBySlug :one
SELECT id, shop_id, category_id, name, description, images, status,
       rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at, slug, search_text
FROM products
WHERE slug = $1 AND deleted_at IS NULL;

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
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, shop_id, category_id, name, description, images, status,
          rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at, slug, search_text;

-- name: UpdateProductReview :one
UPDATE products
SET status = $2,
    rejection_reason = $3,
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, shop_id, category_id, name, description, images, status,
          rejection_reason, min_price_vnd, max_price_vnd, deleted_at, created_at, updated_at, slug, search_text;

-- name: UpdateProductStatus :exec
UPDATE products
SET status = $2,
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteProduct :execrows
UPDATE products
SET deleted_at = NOW(),
    status = 'HIDDEN',
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

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
  AND (sqlc.arg(keyword)::text = '' OR p.search_text LIKE '%' || sqlc.arg(keyword)::text || '%' ESCAPE '\')
  AND (sqlc.arg(shop_id)::text = '' OR p.shop_id = sqlc.arg(shop_id)::text)
  AND (NOT sqlc.arg(public_only)::bool OR p.status IN ('APPROVED', 'OUT_OF_STOCK'))
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
       p.rejection_reason, p.min_price_vnd, p.max_price_vnd, p.deleted_at, p.created_at, p.updated_at, p.slug, p.search_text
FROM products p
WHERE p.deleted_at IS NULL
  AND (sqlc.arg(category_id)::text = '' OR p.category_id IN (SELECT id FROM cat_tree))
  AND (sqlc.arg(keyword)::text = '' OR p.search_text LIKE '%' || sqlc.arg(keyword)::text || '%' ESCAPE '\')
  AND (sqlc.arg(shop_id)::text = '' OR p.shop_id = sqlc.arg(shop_id)::text)
  AND (NOT sqlc.arg(public_only)::bool OR p.status IN ('APPROVED', 'OUT_OF_STOCK'))
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
       p.rejection_reason, p.min_price_vnd, p.max_price_vnd, p.deleted_at, p.created_at, p.updated_at, p.slug, p.search_text
FROM matched_products mp
JOIN products p ON p.id = mp.product_id
WHERE p.deleted_at IS NULL
  AND p.status IN ('APPROVED', 'OUT_OF_STOCK')
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
    reserved_stock, version, weight_grams, image_url, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 1, $8, $9, $10)
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at;

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
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at;

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
  AND $6 >= reserved_stock
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at;

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
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.version,
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
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.product_id = ANY(sqlc.arg(product_ids)::text[])
  AND s.status <> 'INACTIVE'
ORDER BY s.created_at ASC, s.id ASC;

-- name: ListAllSKUsByProductID :many
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.version,
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
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.product_id = sqlc.arg(product_id)::text
ORDER BY s.created_at ASC, s.id ASC;

-- name: GetSKUsBatch :many
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.version,
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
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.id = ANY(sqlc.arg(sku_ids)::text[])
ORDER BY s.id ASC;

-- name: GetSKUWithContext :one
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.version,
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
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.id = sqlc.arg(id)::text;

-- name: GetSKUWithContextForUpdate :one
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.version,
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
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
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
  AND $1 >= reserved_stock
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at;

-- name: ReserveSKUOptimistic :one
UPDATE skus
SET reserved_stock = reserved_stock + $1,
    version = version + 1,
    updated_at = NOW()
WHERE id = $2
  AND version = $3
  AND (stock - reserved_stock) >= $1
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at;

-- name: ReleaseSKUOptimistic :one
UPDATE skus
SET reserved_stock = GREATEST(0, reserved_stock - $1),
    version = version + 1,
    updated_at = NOW()
WHERE id = $2
  AND version = $3
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at;

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
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at;

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
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at;

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
SELECT id, reservation_group_id, order_id, sku_id, quantity, status, created_at, updated_at
FROM stock_reservations
WHERE order_id = $1
ORDER BY sku_id ASC;

-- name: ListReservationsByOrderIDForUpdate :many
SELECT id, reservation_group_id, order_id, sku_id, quantity, status, created_at, updated_at
FROM stock_reservations
WHERE order_id = $1
ORDER BY sku_id ASC
FOR UPDATE;

-- name: CreateStockReservation :one
INSERT INTO stock_reservations (id, reservation_group_id, order_id, sku_id, quantity, status)
VALUES ($1, $2, $3, $4, $5, 'RESERVED')
RETURNING id, reservation_group_id, order_id, sku_id, quantity, status, created_at, updated_at;

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
