-- name: CreateSite :one
INSERT INTO sites (
    id, shop_id, subdomain, status, draft_config
) VALUES (
    $1, $2, LOWER($3), 'DRAFT', $4
)
RETURNING *;

-- name: UpdateSiteDraft :one
UPDATE sites
SET subdomain = LOWER($2),
    draft_config = $3,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: GetSiteByShopID :one
SELECT * FROM sites
WHERE shop_id = $1
LIMIT 1;

-- name: GetSiteByShopIDForUpdate :one
SELECT * FROM sites
WHERE shop_id = $1
LIMIT 1
FOR UPDATE;

-- name: GetSiteByID :one
SELECT * FROM sites
WHERE id = $1
LIMIT 1;

-- name: GetSiteByIDForUpdate :one
SELECT * FROM sites
WHERE id = $1
LIMIT 1
FOR UPDATE;

-- name: GetSiteBySubdomain :one
SELECT * FROM sites
WHERE LOWER(subdomain) = LOWER($1)
LIMIT 1;

-- name: GetSiteDetailByShopID :one
SELECT
    s.id,
    s.shop_id,
    sh.name AS shop_name,
    s.subdomain,
    COALESCE(d.host, '') AS primary_host,
    s.status,
    s.draft_config,
    s.published_version_id,
    COALESCE(v.version_number, 0)::int AS published_version_number,
    s.published_at,
    s.suspended_reason,
    s.created_at,
    s.updated_at
FROM sites s
JOIN shops sh ON sh.id = s.shop_id
LEFT JOIN domains d ON d.site_id = s.id AND d.is_primary = TRUE
LEFT JOIN site_versions v ON v.id = s.published_version_id
WHERE s.shop_id = $1
LIMIT 1;

-- name: GetSiteDetailByID :one
SELECT
    s.id,
    s.shop_id,
    sh.name AS shop_name,
    s.subdomain,
    COALESCE(d.host, '') AS primary_host,
    s.status,
    s.draft_config,
    s.published_version_id,
    COALESCE(v.version_number, 0)::int AS published_version_number,
    s.published_at,
    s.suspended_reason,
    s.created_at,
    s.updated_at
FROM sites s
JOIN shops sh ON sh.id = s.shop_id
LEFT JOIN domains d ON d.site_id = s.id AND d.is_primary = TRUE
LEFT JOIN site_versions v ON v.id = s.published_version_id
WHERE s.id = $1
LIMIT 1;

-- name: GetPublishedSiteByHost :one
SELECT
    s.id,
    s.shop_id,
    sh.name AS shop_name,
    sh.description AS shop_description,
    sh.status AS shop_status,
    s.subdomain,
    COALESCE(pd.host, d.host) AS primary_host,
    s.status,
    v.config AS published_config,
    s.published_version_id,
    v.version_number AS published_version_number,
    s.published_at,
    s.created_at,
    s.updated_at
FROM domains d
JOIN sites s ON s.id = d.site_id
JOIN site_versions v ON v.id = s.published_version_id
JOIN shops sh ON sh.id = s.shop_id
LEFT JOIN domains pd ON pd.site_id = s.id AND pd.is_primary = TRUE
WHERE LOWER(d.host) = LOWER($1)
  AND s.status = 'PUBLISHED'
  AND s.published_version_id IS NOT NULL
  AND sh.status = 'ACTIVE'
LIMIT 1;

-- name: GetPublishedSiteByShopID :one
SELECT
    s.id,
    s.shop_id,
    sh.name AS shop_name,
    sh.description AS shop_description,
    sh.status AS shop_status,
    s.subdomain,
    COALESCE(pd.host, '') AS primary_host,
    s.status,
    v.config AS published_config,
    s.published_version_id,
    v.version_number AS published_version_number,
    s.published_at,
    s.created_at,
    s.updated_at
FROM sites s
JOIN site_versions v ON v.id = s.published_version_id
JOIN shops sh ON sh.id = s.shop_id
LEFT JOIN domains pd ON pd.site_id = s.id AND pd.is_primary = TRUE
WHERE s.shop_id = $1
  AND s.status = 'PUBLISHED'
  AND s.published_version_id IS NOT NULL
  AND sh.status = 'ACTIVE'
LIMIT 1;

-- name: UpdateSitePublished :one
UPDATE sites
SET published_version_id = $2,
    status = 'PUBLISHED',
    published_at = NOW(),
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateSiteRollback :one
UPDATE sites
SET published_version_id = $2,
    draft_config = $3,
    status = 'PUBLISHED',
    published_at = NOW(),
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateSiteStatus :one
UPDATE sites
SET status = $2,
    suspended_reason = $3,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: ListSites :many
SELECT
    s.id,
    s.shop_id,
    sh.name AS shop_name,
    s.subdomain,
    COALESCE(d.host, '') AS primary_host,
    s.status,
    s.draft_config,
    s.published_version_id,
    COALESCE(v.version_number, 0)::int AS published_version_number,
    s.published_at,
    s.suspended_reason,
    s.created_at,
    s.updated_at
FROM sites s
JOIN shops sh ON sh.id = s.shop_id
LEFT JOIN domains d ON d.site_id = s.id AND d.is_primary = TRUE
LEFT JOIN site_versions v ON v.id = s.published_version_id
WHERE (sqlc.arg(status)::text = '' OR s.status = sqlc.arg(status)::text)
  AND (
      sqlc.arg(query)::text = ''
      OR s.subdomain ILIKE '%' || sqlc.arg(query)::text || '%'
      OR sh.name ILIKE '%' || sqlc.arg(query)::text || '%'
      OR COALESCE(d.host, '') ILIKE '%' || sqlc.arg(query)::text || '%'
  )
ORDER BY s.updated_at DESC, s.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountSites :one
SELECT COUNT(*)::bigint
FROM sites s
JOIN shops sh ON sh.id = s.shop_id
LEFT JOIN domains d ON d.site_id = s.id AND d.is_primary = TRUE
WHERE (sqlc.arg(status)::text = '' OR s.status = sqlc.arg(status)::text)
  AND (
      sqlc.arg(query)::text = ''
      OR s.subdomain ILIKE '%' || sqlc.arg(query)::text || '%'
      OR sh.name ILIKE '%' || sqlc.arg(query)::text || '%'
      OR COALESCE(d.host, '') ILIKE '%' || sqlc.arg(query)::text || '%'
  );

-- name: GetMaxSiteVersionNumber :one
SELECT COALESCE(MAX(version_number), 0)::int
FROM site_versions
WHERE site_id = $1;

-- name: CreateSiteVersion :one
INSERT INTO site_versions (
    id, site_id, version_number, config, published_by, note
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: GetSiteVersionByID :one
SELECT * FROM site_versions
WHERE id = $1
LIMIT 1;

-- name: ListSiteVersions :many
SELECT * FROM site_versions
WHERE site_id = $1
ORDER BY version_number DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountSiteVersions :one
SELECT COUNT(*)::bigint
FROM site_versions
WHERE site_id = $1;

-- name: GetDomainByHost :one
SELECT * FROM domains
WHERE LOWER(host) = LOWER($1)
LIMIT 1;

-- name: UpsertSiteSubdomainDomain :one
INSERT INTO domains (
    id, site_id, host, kind, is_primary, verified_at, tls_status
) VALUES (
    $1, $2, LOWER($3), 'SUBDOMAIN', TRUE, NOW(), 'MANAGED'
)
ON CONFLICT (site_id) WHERE kind = 'SUBDOMAIN'
DO UPDATE SET
    host = EXCLUDED.host,
    verified_at = NOW(),
    updated_at = NOW()
RETURNING *;

-- name: ListPublishedSites :many
-- Public directory: only PUBLISHED sites of ACTIVE shops, fields from the published snapshot only.
SELECT
    s.id,
    s.shop_id,
    sh.name AS shop_name,
    s.subdomain,
    COALESCE(pd.host, '') AS primary_host,
    COALESCE(v.config->'theme'->>'logo_url', '')::text AS logo_url,
    COALESCE(v.config->'theme'->>'banner_url', '')::text AS cover_url,
    COALESCE(NULLIF(v.config->'theme'->>'tagline', ''), sh.description, '')::text AS tagline,
    s.published_at
FROM sites s
JOIN site_versions v ON v.id = s.published_version_id
JOIN shops sh ON sh.id = s.shop_id
LEFT JOIN domains pd ON pd.site_id = s.id AND pd.is_primary = TRUE
WHERE s.status = 'PUBLISHED'
  AND s.published_version_id IS NOT NULL
  AND sh.status = 'ACTIVE'
ORDER BY s.published_at DESC NULLS LAST, s.id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountPublishedSites :one
SELECT COUNT(*)::bigint
FROM sites s
JOIN shops sh ON sh.id = s.shop_id
WHERE s.status = 'PUBLISHED'
  AND s.published_version_id IS NOT NULL
  AND sh.status = 'ACTIVE';
