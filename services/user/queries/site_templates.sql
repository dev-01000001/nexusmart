-- name: ListActiveSiteTemplates :many
SELECT * FROM site_templates
WHERE is_active = TRUE
ORDER BY sort_order ASC, id ASC;

-- name: GetSiteTemplateByID :one
SELECT * FROM site_templates
WHERE is_active = TRUE
  AND (id = sqlc.arg(id_or_slug)::text OR LOWER(slug) = LOWER(sqlc.arg(id_or_slug)::text))
LIMIT 1;

-- name: UpsertSiteTemplate :one
INSERT INTO site_templates (
    id,
    slug,
    name,
    category,
    archetype,
    description,
    preview_image_url,
    tags,
    config,
    is_active,
    sort_order
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
ON CONFLICT (id) DO UPDATE SET
    slug = EXCLUDED.slug,
    name = EXCLUDED.name,
    category = EXCLUDED.category,
    archetype = EXCLUDED.archetype,
    description = EXCLUDED.description,
    preview_image_url = EXCLUDED.preview_image_url,
    tags = EXCLUDED.tags,
    config = EXCLUDED.config,
    is_active = EXCLUDED.is_active,
    sort_order = EXCLUDED.sort_order,
    updated_at = NOW()
RETURNING *;

-- name: UpdateSiteTemplateID :exec
UPDATE sites
SET template_id = $2,
    updated_at = NOW()
WHERE id = $1;
