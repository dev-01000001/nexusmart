-- name: CreateUser :one
INSERT INTO users (
    id, full_name, email, phone, password_hash, role, status, phone_verified
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1
LIMIT 1;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE LOWER(email) = LOWER($1)
LIMIT 1;

-- name: GetUserByPhone :one
SELECT * FROM users
WHERE phone = $1 AND phone <> ''
LIMIT 1;

-- name: ActivateUser :one
UPDATE users
SET status = 'ACTIVE',
    phone_verified = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateUserProfile :one
UPDATE users
SET full_name = $2,
    phone = $3,
    avatar_url = $4,
    phone_verified = $5,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: InsertCODRejection :execrows
INSERT INTO cod_rejections (order_id, user_id, reason)
VALUES ($1, $2, $3)
ON CONFLICT (order_id) DO NOTHING;

-- name: IncrementUserCODRejectedCount :one
UPDATE users
SET cod_rejected_count = cod_rejected_count + 1,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateUserStatus :one
UPDATE users
SET status = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateUserRole :one
UPDATE users
SET role = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: GrantUserSellerShop :one
UPDATE users
SET role = CASE WHEN role = 'buyer' THEN 'seller' ELSE role END,
    shop_id = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: CountActiveAdmins :one
SELECT COUNT(*)::bigint FROM users
WHERE role IN ('admin', 'super_admin')
  AND status = 'ACTIVE';

-- name: ListUsers :many
SELECT * FROM users
WHERE (sqlc.arg(query)::text = '' OR full_name ILIKE '%' || sqlc.arg(query)::text || '%' OR email ILIKE '%' || sqlc.arg(query)::text || '%' OR phone ILIKE '%' || sqlc.arg(query)::text || '%')
  AND (sqlc.arg(role)::text = '' OR role = sqlc.arg(role)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountUsers :one
SELECT COUNT(*)::bigint FROM users
WHERE (sqlc.arg(query)::text = '' OR full_name ILIKE '%' || sqlc.arg(query)::text || '%' OR email ILIKE '%' || sqlc.arg(query)::text || '%' OR phone ILIKE '%' || sqlc.arg(query)::text || '%')
  AND (sqlc.arg(role)::text = '' OR role = sqlc.arg(role)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text);

-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (
    id, user_id, family_id, token_hash, expires_at
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1
LIMIT 1;

-- name: RevokeRefreshTokenWithReplacement :execrows
UPDATE refresh_tokens
SET revoked_at = NOW(),
    replaced_by_id = $2
WHERE id = $1
  AND revoked_at IS NULL;

-- name: RevokeRefreshTokenByHash :exec
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE token_hash = $1
  AND revoked_at IS NULL;

-- name: RevokeRefreshTokenFamily :exec
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE family_id = $1
  AND revoked_at IS NULL;

-- name: RevokeAllUserRefreshTokens :exec
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE user_id = $1
  AND revoked_at IS NULL;

-- name: CreateShop :one
INSERT INTO shops (
    id, owner_user_id, name, description, pickup_address_id, contact_phone, business_license_doc, status, slug
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
RETURNING *;

-- name: GetShopByID :one
SELECT * FROM shops
WHERE id = $1
LIMIT 1;

-- name: GetShopBySlug :one
SELECT * FROM shops
WHERE slug = $1
LIMIT 1;

-- name: GetShopByOwnerID :one
SELECT * FROM shops
WHERE owner_user_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: GetShopByName :one
SELECT * FROM shops
WHERE LOWER(name) = LOWER($1)
LIMIT 1;

-- name: ListShops :many
SELECT * FROM shops
WHERE (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountShops :one
SELECT COUNT(*)::bigint FROM shops
WHERE (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text);

-- name: ResubmitShop :one
UPDATE shops
SET name = $2,
    slug = $3,
    description = $4,
    pickup_address_id = $5,
    contact_phone = $6,
    business_license_doc = $7,
    status = 'PENDING',
    rejection_reason = '',
    updated_at = NOW()
WHERE id = $1
  AND (status = 'REJECTED' OR (status = 'PENDING' AND btrim(rejection_reason) <> ''))
RETURNING *;

-- name: UpdateShopReview :one
UPDATE shops
SET status = $2,
    rejection_reason = $3,
    updated_at = NOW()
WHERE id = $1
  AND status = 'PENDING'
RETURNING *;

-- name: ExistsActiveShopByPickupAddressID :one
SELECT EXISTS (
    SELECT 1 FROM shops
    WHERE pickup_address_id = $1
      AND status = 'ACTIVE'
)::boolean AS used;

-- name: CreateAddress :one
INSERT INTO addresses (
    id, user_id, recipient_name, recipient_phone,
    province_code, province_name, ward_code, ward_name,
    street_address, legacy_province_code, legacy_district_code, legacy_ward_code,
    is_default
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8,
    $9, $10, $11, $12,
    $13
)
RETURNING *;

-- name: ClearUserDefaultAddresses :exec
UPDATE addresses
SET is_default = FALSE,
    updated_at = NOW()
WHERE user_id = $1 AND is_default = TRUE;

-- name: ListAddressesByUserID :many
SELECT * FROM addresses
WHERE user_id = $1
ORDER BY is_default DESC, created_at DESC;

-- name: GetAddressByID :one
SELECT * FROM addresses
WHERE id = $1
LIMIT 1;

-- name: UpdateAddress :one
UPDATE addresses
SET recipient_name = $2,
    recipient_phone = $3,
    province_code = $4,
    province_name = $5,
    ward_code = $6,
    ward_name = $7,
    street_address = $8,
    is_default = $9,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteAddress :exec
DELETE FROM addresses
WHERE id = $1;

-- name: InsertAuditLog :one
INSERT INTO audit_logs (
    id, actor_id, actor_role, action, target_type, target_id,
    before_json, after_json, reason, ip_address
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10
)
RETURNING *;

-- name: ListAuditLogs :many
SELECT * FROM audit_logs
WHERE (sqlc.arg(actor_id)::text = '' OR actor_id = sqlc.arg(actor_id)::text)
  AND (sqlc.arg(target_type)::text = '' OR target_type = sqlc.arg(target_type)::text)
  AND (sqlc.arg(target_id)::text = '' OR target_id = sqlc.arg(target_id)::text)
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR created_at <= sqlc.narg(to_time)::timestamptz)
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAuditLogs :one
SELECT COUNT(*)::bigint FROM audit_logs
WHERE (sqlc.arg(actor_id)::text = '' OR actor_id = sqlc.arg(actor_id)::text)
  AND (sqlc.arg(target_type)::text = '' OR target_type = sqlc.arg(target_type)::text)
  AND (sqlc.arg(target_id)::text = '' OR target_id = sqlc.arg(target_id)::text)
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR created_at <= sqlc.narg(to_time)::timestamptz);
