-- Categories

-- name: CreateCategory :one
INSERT INTO categories (id, name, slug, parent_id, commission_rate_bps, sort_order)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, name, slug, parent_id, commission_rate_bps, sort_order, created_at, updated_at;

-- name: GetCategoryByID :one
SELECT id, name, slug, parent_id, commission_rate_bps, sort_order, created_at, updated_at
FROM categories
WHERE id = $1;

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

-- name: CountProductsInCategory :one
SELECT COUNT(*)::bigint AS cnt
FROM products
WHERE category_id = $1 AND deleted_at IS NULL;

-- name: CountChildCategories :one
SELECT COUNT(*)::bigint AS cnt
FROM categories
WHERE parent_id = $1;

-- name: DeleteCategory :execrows
DELETE FROM categories
WHERE id = $1;

-- name: ListAllCategories :many
SELECT id, name, slug, parent_id, commission_rate_bps, sort_order, created_at, updated_at
FROM categories
ORDER BY sort_order ASC, name ASC, id ASC;

-- name: ListCategoriesByParent :many
SELECT id, name, slug, parent_id, commission_rate_bps, sort_order, created_at, updated_at
FROM categories
WHERE parent_id = $1
ORDER BY sort_order ASC, name ASC, id ASC;

-- name: ListRootCategories :many
SELECT id, name, slug, parent_id, commission_rate_bps, sort_order, created_at, updated_at
FROM categories
WHERE parent_id IS NULL
ORDER BY sort_order ASC, name ASC, id ASC;

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
SELECT COUNT(*)::bigint AS cnt
FROM products p
WHERE p.deleted_at IS NULL
  AND (sqlc.arg(category_id)::text = '' OR p.category_id = sqlc.arg(category_id)::text OR p.category_id IN (SELECT c.id FROM categories c WHERE c.parent_id = sqlc.arg(category_id)::text))
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
SELECT p.id, p.shop_id, p.category_id, p.name, p.description, p.images, p.status,
       p.rejection_reason, p.min_price_vnd, p.max_price_vnd, p.deleted_at, p.created_at, p.updated_at, p.slug, p.search_text
FROM products p
WHERE p.deleted_at IS NULL
  AND (sqlc.arg(category_id)::text = '' OR p.category_id = sqlc.arg(category_id)::text OR p.category_id IN (SELECT c.id FROM categories c WHERE c.parent_id = sqlc.arg(category_id)::text))
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

-- SKUs

-- name: CreateSKU :one
INSERT INTO skus (
    id, product_id, sku_code, name, attributes, price_vnd, stock,
    reserved_stock, version, weight_grams, image_url, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 1, $8, $9, $10)
RETURNING id, product_id, sku_code, name, attributes, price_vnd, stock,
          reserved_stock, version, weight_grams, image_url, status, created_at, updated_at;

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
    s.weight_grams, s.image_url, p.category_id, c.commission_rate_bps,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.product_id = ANY($1::text[])
  AND s.status <> 'INACTIVE'
ORDER BY s.created_at ASC, s.id ASC;

-- name: ListAllSKUsByProductID :many
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.version,
    s.weight_grams, s.image_url, p.category_id, c.commission_rate_bps,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.product_id = $1
ORDER BY s.created_at ASC, s.id ASC;

-- name: GetSKUsBatch :many
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.version,
    s.weight_grams, s.image_url, p.category_id, c.commission_rate_bps,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.id = ANY($1::text[])
ORDER BY s.id ASC;

-- name: GetSKUWithContext :one
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.version,
    s.weight_grams, s.image_url, p.category_id, c.commission_rate_bps,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.id = $1;

-- name: GetSKUWithContextForUpdate :one
SELECT
    s.id, s.product_id, p.shop_id, p.name AS product_name, s.sku_code, s.name,
    s.attributes, s.price_vnd, s.stock, s.reserved_stock, s.version,
    s.weight_grams, s.image_url, p.category_id, c.commission_rate_bps,
    s.status AS sku_status, p.status AS product_status, p.deleted_at AS product_deleted_at
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN categories c ON c.id = p.category_id
WHERE s.id = $1
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
