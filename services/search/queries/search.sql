-- name: IsEventProcessed :one
SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)::bool;

-- name: TryInsertProcessedEvent :execrows
INSERT INTO processed_events (event_id, subject)
VALUES ($1, $2)
ON CONFLICT (event_id) DO NOTHING;

-- name: InsertDeadLetterEvent :exec
INSERT INTO dead_letter_events (id, subject, event_id, payload, error_message, attempts)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: CountDeadLetterEvents :one
SELECT COUNT(*) FROM dead_letter_events;

-- name: UpsertCategory :exec
INSERT INTO search_categories (category_id, name, name_folded, slug, parent_id, sort_order, source_updated_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
ON CONFLICT (category_id) DO UPDATE SET
    name = EXCLUDED.name,
    name_folded = EXCLUDED.name_folded,
    slug = EXCLUDED.slug,
    parent_id = EXCLUDED.parent_id,
    sort_order = EXCLUDED.sort_order,
    source_updated_at = EXCLUDED.source_updated_at,
    updated_at = NOW()
WHERE search_categories.source_updated_at <= EXCLUDED.source_updated_at;

-- name: DeleteCategory :exec
DELETE FROM search_categories
WHERE category_id = $1
  AND source_updated_at <= $2;

-- name: GetCategoryByID :one
SELECT category_id, name, name_folded, slug, parent_id, sort_order, source_updated_at, updated_at
FROM search_categories
WHERE category_id = $1;

-- name: GetCategoryBySlug :one
SELECT category_id, name, name_folded, slug, parent_id, sort_order, source_updated_at, updated_at
FROM search_categories
WHERE slug = $1;

-- name: ListAllCategories :many
SELECT category_id, name, name_folded, slug, parent_id, sort_order, source_updated_at, updated_at
FROM search_categories
ORDER BY sort_order ASC, name ASC;

-- name: ListCategoriesByIDs :many
SELECT category_id, name, name_folded, slug, parent_id, sort_order, source_updated_at, updated_at
FROM search_categories
WHERE category_id = ANY($1::text[]);

-- name: SuggestCategories :many
SELECT category_id, name, slug
FROM search_categories
WHERE ($1::text = '' OR name_folded LIKE '%' || $1::text || '%' ESCAPE '\' OR slug LIKE '%' || $1::text || '%' ESCAPE '\')
ORDER BY
    CASE WHEN $1::text <> '' AND name_folded LIKE $1::text || '%' ESCAPE '\' THEN 0 ELSE 1 END,
    sort_order ASC,
    name ASC
LIMIT $2;

-- name: SuggestCategoriesByShop :many
WITH shop_cat_sets AS (
    SELECT DISTINCT pe.category_id, pe.category_ancestor_ids
    FROM product_embeddings pe
    WHERE pe.shop_id = $1
      AND pe.deleted = FALSE
      AND pe.status IN ('APPROVED', 'OUT_OF_STOCK')
),
shop_cats AS (
    SELECT DISTINCT u.cat_id
    FROM shop_cat_sets s
    CROSS JOIN LATERAL unnest(array_append(s.category_ancestor_ids, s.category_id)) AS u(cat_id)
)
SELECT c.category_id, c.name, c.slug
FROM search_categories c
JOIN shop_cats sc ON sc.cat_id = c.category_id
WHERE ($2::text = '' OR c.name_folded LIKE '%' || $2::text || '%' ESCAPE '\' OR c.slug LIKE '%' || $2::text || '%' ESCAPE '\')
ORDER BY
    CASE WHEN $2::text <> '' AND c.name_folded LIKE $2::text || '%' ESCAPE '\' THEN 0 ELSE 1 END,
    c.sort_order ASC,
    c.name ASC
LIMIT $3;

-- name: ListCategoriesByShop :many
WITH shop_cat_sets AS (
    SELECT DISTINCT pe.category_id, pe.category_ancestor_ids
    FROM product_embeddings pe
    WHERE pe.shop_id = $1
      AND pe.deleted = FALSE
      AND pe.status IN ('APPROVED', 'OUT_OF_STOCK')
),
shop_cats AS (
    SELECT DISTINCT u.cat_id
    FROM shop_cat_sets s
    CROSS JOIN LATERAL unnest(array_append(s.category_ancestor_ids, s.category_id)) AS u(cat_id)
)
SELECT c.category_id, c.name, c.name_folded, c.slug, c.parent_id, c.sort_order, c.source_updated_at, c.updated_at
FROM search_categories c
JOIN shop_cats sc ON sc.cat_id = c.category_id
ORDER BY c.sort_order ASC, c.name ASC;

-- name: UpsertShopMetadata :exec
INSERT INTO search_shops (shop_id, name, name_folded, slug, status, status_version, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (shop_id) DO UPDATE SET
    name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE search_shops.name END,
    name_folded = CASE WHEN EXCLUDED.name_folded <> '' THEN EXCLUDED.name_folded ELSE search_shops.name_folded END,
    slug = CASE WHEN EXCLUDED.slug <> '' THEN EXCLUDED.slug ELSE search_shops.slug END,
    status = CASE
        WHEN EXCLUDED.status_version > search_shops.status_version THEN EXCLUDED.status
        WHEN search_shops.status_version = 0 AND EXCLUDED.status_version = 0 THEN EXCLUDED.status
        ELSE search_shops.status
    END,
    status_version = GREATEST(search_shops.status_version, EXCLUDED.status_version),
    updated_at = NOW();

-- name: SeedSuspendedShopIfMissing :exec
INSERT INTO search_shops (shop_id, name, name_folded, slug, status, status_version, updated_at)
VALUES ($1, '', '', '', 'SUSPENDED', 0, NOW())
ON CONFLICT (shop_id) DO NOTHING;

-- name: UpsertShopStatus :execrows
INSERT INTO search_shops (shop_id, name, name_folded, slug, status, status_version, updated_at)
VALUES ($1, $2, $3, '', $4, $5, NOW())
ON CONFLICT (shop_id) DO UPDATE SET
    status = EXCLUDED.status,
    status_version = EXCLUDED.status_version,
    name = CASE WHEN search_shops.name = '' AND EXCLUDED.name <> '' THEN EXCLUDED.name ELSE search_shops.name END,
    name_folded = CASE WHEN search_shops.name_folded = '' AND EXCLUDED.name_folded <> '' THEN EXCLUDED.name_folded ELSE search_shops.name_folded END,
    updated_at = NOW()
WHERE EXCLUDED.status_version > search_shops.status_version;

-- name: DeleteShop :exec
DELETE FROM search_shops WHERE shop_id = $1;

-- name: GetShopByID :one
SELECT shop_id, name, name_folded, slug, status, status_version, updated_at
FROM search_shops
WHERE shop_id = $1;

-- name: GetShopBySlug :one
SELECT shop_id, name, name_folded, slug, status, status_version, updated_at
FROM search_shops
WHERE slug = $1;

-- name: ListAllShops :many
SELECT shop_id, name, name_folded, slug, status, status_version, updated_at
FROM search_shops;

-- name: ListShopsByIDs :many
SELECT shop_id, name, name_folded, slug, status, status_version, updated_at
FROM search_shops
WHERE shop_id = ANY($1::text[]);

-- name: ListSuspendedShopIDs :many
SELECT shop_id
FROM search_shops
WHERE status = 'SUSPENDED'
ORDER BY shop_id ASC;

-- name: SuggestActiveShops :many
SELECT shop_id, name, slug
FROM search_shops
WHERE status = 'ACTIVE'
  AND name <> ''
  AND ($1::text = '' OR name_folded LIKE '%' || $1::text || '%' ESCAPE '\' OR slug LIKE '%' || $1::text || '%' ESCAPE '\')
ORDER BY
    CASE WHEN $1::text <> '' AND name_folded LIKE $1::text || '%' ESCAPE '\' THEN 0 ELSE 1 END,
    name ASC
LIMIT $2;

-- name: GetProductEmbeddingState :one
SELECT product_id, text_hash, deleted, source_updated_at, (embedding IS NOT NULL)::bool AS has_embedding
FROM product_embeddings
WHERE product_id = $1;

-- name: GetProductEmbeddingStatesBatch :many
SELECT product_id, text_hash, deleted, source_updated_at, (embedding IS NOT NULL)::bool AS has_embedding
FROM product_embeddings
WHERE product_id = ANY($1::text[]);

-- name: GetProductEmbeddingForSimilar :one
SELECT
    pe.product_id,
    pe.shop_id,
    pe.status,
    pe.deleted,
    pe.embedding,
    COALESCE(s.status, 'ACTIVE')::text AS shop_status
FROM product_embeddings pe
LEFT JOIN search_shops s ON s.shop_id = pe.shop_id
WHERE pe.product_id = $1;

-- name: UpsertProductFromEvent :execrows
INSERT INTO product_embeddings (
    product_id, shop_id, category_id, category_ancestor_ids,
    name, name_folded, slug, description, thumbnail_url,
    min_price_vnd, max_price_vnd, status,
    rating_avg_x100, review_count, in_stock,
    sku_count, sku_codes, sku_attributes_text, sku_ids, default_sku_id,
    colors, colors_folded, color_codes, sizes, sizes_folded,
    deleted, text_hash, embedding, source_updated_at, updated_at
)
VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15,
    $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25,
    FALSE,
    CASE WHEN $27::vector IS NOT NULL THEN $26::text ELSE '' END,
    $27, $28, NOW()
)
ON CONFLICT (product_id) DO UPDATE SET
    shop_id = EXCLUDED.shop_id,
    category_id = EXCLUDED.category_id,
    category_ancestor_ids = EXCLUDED.category_ancestor_ids,
    name = EXCLUDED.name,
    name_folded = EXCLUDED.name_folded,
    slug = EXCLUDED.slug,
    description = EXCLUDED.description,
    thumbnail_url = EXCLUDED.thumbnail_url,
    min_price_vnd = EXCLUDED.min_price_vnd,
    max_price_vnd = EXCLUDED.max_price_vnd,
    status = EXCLUDED.status,
    rating_avg_x100 = EXCLUDED.rating_avg_x100,
    review_count = EXCLUDED.review_count,
    in_stock = EXCLUDED.in_stock,
    sku_count = EXCLUDED.sku_count,
    sku_codes = EXCLUDED.sku_codes,
    sku_attributes_text = EXCLUDED.sku_attributes_text,
    sku_ids = EXCLUDED.sku_ids,
    default_sku_id = EXCLUDED.default_sku_id,
    colors = EXCLUDED.colors,
    colors_folded = EXCLUDED.colors_folded,
    color_codes = EXCLUDED.color_codes,
    sizes = EXCLUDED.sizes,
    sizes_folded = EXCLUDED.sizes_folded,
    deleted = FALSE,
    text_hash = CASE
        WHEN EXCLUDED.embedding IS NOT NULL THEN EXCLUDED.text_hash
        ELSE product_embeddings.text_hash
    END,
    embedding = COALESCE(EXCLUDED.embedding, product_embeddings.embedding),
    source_updated_at = EXCLUDED.source_updated_at,
    updated_at = NOW()
WHERE (product_embeddings.deleted = FALSE AND product_embeddings.source_updated_at <= EXCLUDED.source_updated_at)
   OR (product_embeddings.deleted = TRUE AND EXCLUDED.source_updated_at > product_embeddings.source_updated_at);

-- name: UpsertProductFromReindex :execrows
INSERT INTO product_embeddings (
    product_id, shop_id, category_id, category_ancestor_ids,
    name, name_folded, slug, description, thumbnail_url,
    min_price_vnd, max_price_vnd, status,
    rating_avg_x100, review_count, in_stock,
    sku_count, sku_codes, sku_attributes_text, sku_ids, default_sku_id,
    colors, colors_folded, color_codes, sizes, sizes_folded,
    deleted, text_hash, embedding, source_updated_at, updated_at
)
VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15,
    $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25,
    FALSE,
    CASE WHEN $27::vector IS NOT NULL THEN $26::text ELSE '' END,
    $27, $28, NOW()
)
ON CONFLICT (product_id) DO UPDATE SET
    shop_id = EXCLUDED.shop_id,
    category_id = EXCLUDED.category_id,
    category_ancestor_ids = EXCLUDED.category_ancestor_ids,
    name = EXCLUDED.name,
    name_folded = EXCLUDED.name_folded,
    slug = EXCLUDED.slug,
    description = EXCLUDED.description,
    thumbnail_url = EXCLUDED.thumbnail_url,
    min_price_vnd = EXCLUDED.min_price_vnd,
    max_price_vnd = EXCLUDED.max_price_vnd,
    status = EXCLUDED.status,
    rating_avg_x100 = EXCLUDED.rating_avg_x100,
    review_count = EXCLUDED.review_count,
    in_stock = EXCLUDED.in_stock,
    sku_count = EXCLUDED.sku_count,
    sku_codes = EXCLUDED.sku_codes,
    sku_attributes_text = EXCLUDED.sku_attributes_text,
    sku_ids = EXCLUDED.sku_ids,
    default_sku_id = EXCLUDED.default_sku_id,
    colors = EXCLUDED.colors,
    colors_folded = EXCLUDED.colors_folded,
    color_codes = EXCLUDED.color_codes,
    sizes = EXCLUDED.sizes,
    sizes_folded = EXCLUDED.sizes_folded,
    deleted = FALSE,
    text_hash = CASE
        WHEN EXCLUDED.embedding IS NOT NULL THEN EXCLUDED.text_hash
        ELSE product_embeddings.text_hash
    END,
    embedding = COALESCE(EXCLUDED.embedding, product_embeddings.embedding),
    source_updated_at = EXCLUDED.source_updated_at,
    updated_at = NOW()
WHERE (product_embeddings.deleted = FALSE AND product_embeddings.source_updated_at <= EXCLUDED.source_updated_at)
   OR (product_embeddings.deleted = TRUE AND EXCLUDED.source_updated_at > product_embeddings.source_updated_at);

-- name: UpdateProductEmbeddingOnly :execrows
UPDATE product_embeddings
SET text_hash = $2,
    embedding = $3,
    updated_at = NOW()
WHERE product_id = $1
  AND deleted = FALSE
  AND source_updated_at <= $4
  AND $3::vector IS NOT NULL;

-- name: TombstoneProduct :execrows
INSERT INTO product_embeddings (product_id, shop_id, deleted, source_updated_at, updated_at)
VALUES ($1, $2, TRUE, $3, NOW())
ON CONFLICT (product_id) DO UPDATE SET
    deleted = TRUE,
    source_updated_at = GREATEST(product_embeddings.source_updated_at, EXCLUDED.source_updated_at),
    updated_at = NOW()
WHERE product_embeddings.source_updated_at <= EXCLUDED.source_updated_at;

-- name: ListMissingLiveProductIDsForSweep :many
SELECT product_id
FROM product_embeddings
WHERE deleted = FALSE
  AND source_updated_at < $1
  AND NOT (product_id = ANY($2::text[]))
ORDER BY product_id ASC
LIMIT $3;

-- name: TombstoneStaleProductsBatch :many
UPDATE product_embeddings
SET deleted = TRUE,
    source_updated_at = GREATEST(source_updated_at, $2),
    updated_at = NOW()
WHERE product_id = ANY($1::text[])
  AND deleted = FALSE
  AND source_updated_at < $2
RETURNING product_id;

-- name: ListTombstonedProductIDsBatch :many
SELECT product_id
FROM product_embeddings
WHERE deleted = TRUE
  AND ($1::text = '' OR product_id > $1::text)
ORDER BY product_id ASC
LIMIT $2;

-- name: LockTombstonedProductIDs :many
SELECT product_id
FROM product_embeddings
WHERE product_id = ANY($1::text[])
  AND deleted = TRUE
ORDER BY product_id ASC
FOR SHARE;

-- name: ListProductsByCategoryBranch :many
SELECT
    product_id, shop_id, category_id, category_ancestor_ids,
    name, name_folded, slug, description, thumbnail_url,
    min_price_vnd, max_price_vnd, status,
    rating_avg_x100, review_count, in_stock,
    sku_count, sku_codes, sku_attributes_text, sku_ids, default_sku_id,
    colors, colors_folded, color_codes, sizes, sizes_folded,
    deleted, text_hash, source_updated_at, updated_at
FROM product_embeddings
WHERE deleted = FALSE
  AND status IN ('APPROVED', 'OUT_OF_STOCK')
  AND (category_id = $1 OR category_ancestor_ids @> ARRAY[$1::text])
ORDER BY product_id ASC
FOR UPDATE;

-- name: UpdateProductCategoryAncestors :exec
UPDATE product_embeddings
SET category_ancestor_ids = $2,
    updated_at = NOW()
WHERE product_id = $1
  AND deleted = FALSE;

-- name: UpsertSearchHistory :exec
INSERT INTO search_history (id, user_id, query, query_folded, created_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (user_id, query_folded) DO UPDATE SET
    query = EXCLUDED.query,
    created_at = NOW();

-- name: TrimSearchHistory :exec
DELETE FROM search_history sh
WHERE sh.user_id = $1
  AND sh.id NOT IN (
      SELECT sub.id
      FROM search_history sub
      WHERE sub.user_id = $1
      ORDER BY sub.created_at DESC, sub.id DESC
      LIMIT $2
  );

-- name: ListSearchHistory :many
SELECT id, user_id, query, query_folded, created_at
FROM search_history
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: DeleteSearchHistoryItem :execrows
DELETE FROM search_history
WHERE id = $1 AND user_id = $2;

-- name: ClearSearchHistory :exec
DELETE FROM search_history
WHERE user_id = $1;

-- name: FailStaleRunningReindexJobs :exec
UPDATE reindex_jobs
SET status = 'FAILED',
    error_message = 'interrupted by previous shutdown or crash',
    finished_at = NOW()
WHERE status = 'RUNNING';

-- name: CreateReindexJob :exec
INSERT INTO reindex_jobs (job_id, status, started_at)
VALUES ($1, 'RUNNING', NOW());

-- name: UpdateReindexJobProgress :exec
UPDATE reindex_jobs
SET products_indexed = $2,
    embeddings_updated = $3,
    last_cursor = $4
WHERE job_id = $1;

-- name: FinishReindexJob :exec
UPDATE reindex_jobs
SET status = $2,
    products_indexed = $3,
    embeddings_updated = $4,
    error_message = $5,
    finished_at = NOW()
WHERE job_id = $1;

-- name: GetReindexJob :one
SELECT job_id, status, products_indexed, embeddings_updated, last_cursor, error_message, started_at, finished_at
FROM reindex_jobs
WHERE job_id = $1;
