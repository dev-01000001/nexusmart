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

-- name: GetUserByIDForShare :one
SELECT * FROM users
WHERE id = $1
LIMIT 1
FOR SHARE;

-- name: GetUserByIDForUpdate :one
SELECT * FROM users
WHERE id = $1
LIMIT 1
FOR UPDATE;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE LOWER(email) = LOWER($1)
LIMIT 1;

-- name: GetUserByPhone :one
-- E19-bis: looks up the verified owner of a canonical non-empty phone number.
SELECT * FROM users
WHERE phone = $1 AND phone <> '' AND phone_verified = TRUE
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

-- name: MarkUserPhoneVerified :one
UPDATE users
SET phone = $2,
    phone_verified = TRUE,
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
    lock_reason = $3,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateUserPassword :one
UPDATE users
SET password_hash = $2,
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

-- name: ListUsers :many
SELECT * FROM users
WHERE (
    sqlc.arg(query)::text = ''
    OR CASE
        WHEN sqlc.arg(query)::text ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN id = (sqlc.arg(query)::text)::uuid
        ELSE (
            full_name ILIKE '%' || sqlc.arg(query)::text || '%'
            OR email ILIKE '%' || sqlc.arg(query)::text || '%'
            OR phone ILIKE '%' || sqlc.arg(query)::text || '%'
        )
    END
)
  AND (sqlc.arg(role)::text = '' OR role = sqlc.arg(role)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountUsers :one
SELECT COUNT(*)::bigint FROM users
WHERE (
    sqlc.arg(query)::text = ''
    OR CASE
        WHEN sqlc.arg(query)::text ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN id = (sqlc.arg(query)::text)::uuid
        ELSE (
            full_name ILIKE '%' || sqlc.arg(query)::text || '%'
            OR email ILIKE '%' || sqlc.arg(query)::text || '%'
            OR phone ILIKE '%' || sqlc.arg(query)::text || '%'
        )
    END
)
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

-- name: InvalidateUserPasswordResets :exec
UPDATE password_resets
SET used_at = $2
WHERE user_id = $1
  AND used_at IS NULL;

-- name: CreatePasswordReset :one
INSERT INTO password_resets (
    id, user_id, token_hash, expires_at
) VALUES (
    $1, $2, $3, $4
)
RETURNING *;

-- name: GetPasswordResetByHashForUpdate :one
SELECT * FROM password_resets
WHERE token_hash = $1
LIMIT 1
FOR UPDATE;

-- name: MarkPasswordResetUsed :execrows
UPDATE password_resets
SET used_at = $2
WHERE id = $1
  AND used_at IS NULL;

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

-- name: AdminListShops :many
SELECT
    s.id,
    s.owner_user_id,
    s.name,
    s.description,
    s.pickup_address_id,
    s.contact_phone,
    s.business_license_doc,
    s.status,
    s.rejection_reason,
    s.created_at,
    s.updated_at,
    s.slug,
    s.suspended_at,
    s.suspended_reason,
    s.status_version,
    EXISTS (
        SELECT 1 FROM shop_bank_accounts sba
        WHERE sba.shop_id = s.id
          AND sba.status = 'PENDING_ADMIN_REVIEW'
    )::boolean AS bank_review_pending
FROM shops s
WHERE (sqlc.arg(status)::text = '' OR s.status = sqlc.arg(status)::text)
  AND (
      NOT sqlc.arg(bank_review_pending_only)::boolean
      OR EXISTS (
          SELECT 1 FROM shop_bank_accounts sba
          WHERE sba.shop_id = s.id
            AND sba.status = 'PENDING_ADMIN_REVIEW'
      )
  )
ORDER BY s.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: AdminCountShops :one
SELECT COUNT(*)::bigint FROM shops s
WHERE (sqlc.arg(status)::text = '' OR s.status = sqlc.arg(status)::text)
  AND (
      NOT sqlc.arg(bank_review_pending_only)::boolean
      OR EXISTS (
          SELECT 1 FROM shop_bank_accounts sba
          WHERE sba.shop_id = s.id
            AND sba.status = 'PENDING_ADMIN_REVIEW'
      )
  );

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
    verified_owner_name = '',
    status_version = CASE WHEN status <> 'PENDING' THEN status_version + 1 ELSE status_version END,
    updated_at = NOW()
WHERE id = $1
  AND (status = 'REJECTED' OR (status = 'PENDING' AND btrim(rejection_reason) <> ''))
RETURNING *;

-- name: UpdateShopReview :one
UPDATE shops
SET status = $2,
    rejection_reason = $3,
    verified_owner_name = CASE WHEN $2 = 'ACTIVE' THEN $4 ELSE verified_owner_name END,
    status_version = CASE WHEN status <> $2 THEN status_version + 1 ELSE status_version END,
    updated_at = NOW()
WHERE id = $1
  AND status = 'PENDING'
RETURNING *;

-- name: SuspendShop :one
UPDATE shops
SET status = 'SUSPENDED',
    suspended_at = $2,
    suspended_reason = $3,
    status_version = status_version + 1,
    updated_at = $4
WHERE id = $1
  AND status = 'ACTIVE'
RETURNING *;

-- name: UnsuspendShop :one
UPDATE shops
SET status = 'ACTIVE',
    suspended_at = NULL,
    suspended_reason = '',
    status_version = status_version + 1,
    updated_at = $2
WHERE id = $1
  AND status = 'SUSPENDED'
RETURNING *;

-- name: ExistsPendingBankReviewByShopID :one
SELECT EXISTS (
    SELECT 1 FROM shop_bank_accounts
    WHERE shop_id = $1
      AND status = 'PENDING_ADMIN_REVIEW'
)::boolean AS pending;

-- name: ExistsActiveShopByPickupAddressID :one
SELECT EXISTS (
    SELECT 1 FROM shops
    WHERE pickup_address_id = $1
      AND status IN ('ACTIVE', 'SUSPENDED', 'PENDING')
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

-- name: GetShopByIDForUpdate :one
SELECT * FROM shops
WHERE id = $1
LIMIT 1
FOR UPDATE;

-- name: SupersedePendingOTPShopBankAccounts :exec
UPDATE shop_bank_accounts
SET status = 'SUPERSEDED',
    otp_hash = '',
    otp_expires_at = NULL,
    updated_at = $2
WHERE shop_id = $1
  AND status IN ('PENDING_OTP', 'REJECTED');

-- name: SupersedePendingAdminReviewShopBankAccounts :exec
UPDATE shop_bank_accounts
SET status = 'SUPERSEDED',
    otp_hash = '',
    otp_expires_at = NULL,
    updated_at = $2
WHERE shop_id = $1
  AND status IN ('PENDING_ADMIN_REVIEW', 'REJECTED');

-- name: CreateShopBankAccount :one
INSERT INTO shop_bank_accounts (
    id, shop_id, bank_code, bank_name, account_number, account_number_enc, account_number_masked,
    account_holder_name, holder_name_matched, status, review_note,
    otp_hash, otp_expires_at, otp_attempts, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, '', $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14, $15
)
RETURNING *;

-- name: CountShopBankAccountsCreatedSince :one
SELECT COUNT(*)::bigint FROM shop_bank_accounts
WHERE shop_id = $1
  AND created_at > $2
  AND NOT otp_publish_failed;

-- name: MarkShopBankAccountPublishFailed :exec
UPDATE shop_bank_accounts
SET status = 'SUPERSEDED',
    otp_hash = '',
    otp_expires_at = NULL,
    otp_publish_failed = TRUE,
    updated_at = $2
WHERE id = $1
  AND status = 'PENDING_OTP';

-- name: GetPendingOTPShopBankAccountForUpdate :one
SELECT * FROM shop_bank_accounts
WHERE shop_id = $1
  AND status = 'PENDING_OTP'
ORDER BY created_at DESC
LIMIT 1
FOR UPDATE;

-- name: RecordShopBankAccountOTPFailure :one
UPDATE shop_bank_accounts
SET status = $2,
    otp_attempts = $3,
    otp_hash = $4,
    otp_expires_at = $5,
    updated_at = $6
WHERE id = $1
RETURNING *;

-- name: GetVerifiedShopBankAccountByShopID :one
SELECT * FROM shop_bank_accounts
WHERE shop_id = $1
  AND status = 'VERIFIED'
LIMIT 1;

-- name: SupersedeShopBankAccount :exec
UPDATE shop_bank_accounts
SET status = 'SUPERSEDED',
    otp_hash = '',
    otp_expires_at = NULL,
    updated_at = $2
WHERE id = $1
  AND status <> 'SUPERSEDED';

-- name: CompleteShopBankAccountOTPVerification :one
UPDATE shop_bank_accounts
SET status = $2,
    holder_name_matched = $3,
    otp_hash = '',
    otp_expires_at = NULL,
    payout_locked_until = $4,
    verified_at = $5,
    updated_at = $6
WHERE id = $1
  AND status = 'PENDING_OTP'
RETURNING *;

-- name: GetLatestShopBankAccountByShopID :one
SELECT * FROM shop_bank_accounts
WHERE shop_id = $1
  AND status <> 'SUPERSEDED'
  AND (
      status <> 'PENDING_OTP'
      OR (otp_expires_at IS NOT NULL AND otp_expires_at > sqlc.arg(now_time)::timestamptz)
  )
ORDER BY
    CASE WHEN sqlc.arg(prefer_admin_review)::boolean AND status = 'PENDING_ADMIN_REVIEW' THEN 0 ELSE 1 END ASC,
    created_at DESC,
    CASE status
        WHEN 'PENDING_OTP' THEN 0
        WHEN 'PENDING_ADMIN_REVIEW' THEN 1
        WHEN 'REJECTED' THEN 2
        ELSE 3
    END ASC
LIMIT 1;

-- name: GetShopBankAccountByIDForUpdate :one
SELECT * FROM shop_bank_accounts
WHERE id = $1
LIMIT 1
FOR UPDATE;

-- name: UpdateShopBankAccountAdminReview :one
UPDATE shop_bank_accounts
SET status = $2,
    review_note = $3,
    payout_locked_until = $4,
    verified_at = $5,
    updated_at = $6
WHERE id = $1
  AND status = 'PENDING_ADMIN_REVIEW'
RETURNING *;

-- name: GetUserPlatformCounts :one
SELECT
    COUNT(*)::bigint AS total_users,
    COUNT(*) FILTER (WHERE status = 'ACTIVE')::bigint AS active_users
FROM users;

-- name: GetShopPlatformCounts :one
SELECT
    COUNT(*)::bigint AS total_shops,
    COUNT(*) FILTER (WHERE status = 'ACTIVE')::bigint AS active_shops,
    COUNT(*) FILTER (WHERE status = 'PENDING')::bigint AS pending_shops
FROM shops;

-- name: ListDailyNewUsersVN :many
SELECT
    to_char(created_at AT TIME ZONE 'Asia/Ho_Chi_Minh', 'YYYY-MM-DD')::text AS vn_date,
    COUNT(*)::bigint AS cnt
FROM users
WHERE created_at >= sqlc.arg(start_time)::timestamptz
  AND created_at < sqlc.arg(end_time)::timestamptz
GROUP BY 1
ORDER BY 1 ASC;

-- name: ListDailyNewShopsVN :many
SELECT
    to_char(created_at AT TIME ZONE 'Asia/Ho_Chi_Minh', 'YYYY-MM-DD')::text AS vn_date,
    COUNT(*)::bigint AS cnt
FROM shops
WHERE created_at >= sqlc.arg(start_time)::timestamptz
  AND created_at < sqlc.arg(end_time)::timestamptz
GROUP BY 1
ORDER BY 1 ASC;

-- name: CreateShopSuspensionApproval :one
INSERT INTO shop_suspension_approvals (
    id, shop_id, status, reason, requested_by, requested_role, created_at
) VALUES (
    $1, $2, 'PENDING', $3, $4, $5, $6
)
RETURNING *;

-- name: GetShopSuspensionApprovalByID :one
SELECT * FROM shop_suspension_approvals
WHERE id = $1
LIMIT 1;

-- name: GetShopSuspensionApprovalByIDForUpdate :one
SELECT * FROM shop_suspension_approvals
WHERE id = $1
LIMIT 1
FOR UPDATE;

-- name: GetPendingShopSuspensionApprovalByShopID :one
SELECT * FROM shop_suspension_approvals
WHERE shop_id = $1
  AND status = 'PENDING'
LIMIT 1;

-- name: DecideShopSuspensionApproval :one
UPDATE shop_suspension_approvals
SET status = $2,
    decided_by = $3,
    decided_role = $4,
    decided_note = $5,
    decided_at = $6
WHERE id = $1
  AND status = 'PENDING'
RETURNING *;

-- name: ListShopSuspensionApprovals :many
SELECT
    a.id,
    a.shop_id,
    a.status,
    a.reason,
    a.requested_by,
    a.requested_role,
    a.decided_by,
    a.decided_role,
    a.decided_note,
    a.created_at,
    a.decided_at,
    s.name AS shop_name
FROM shop_suspension_approvals a
JOIN shops s ON s.id = a.shop_id
WHERE (sqlc.arg(status)::text = '' OR a.status = sqlc.arg(status)::text)
  AND (sqlc.narg(shop_id)::uuid IS NULL OR a.shop_id = sqlc.narg(shop_id)::uuid)
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountShopSuspensionApprovals :one
SELECT COUNT(*)::bigint FROM shop_suspension_approvals
WHERE (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.narg(shop_id)::uuid IS NULL OR shop_id = sqlc.narg(shop_id)::uuid);

-- name: InsertUserActivityLog :one
INSERT INTO user_activity_logs (
    id, user_id, event_type, category, ip_address, metadata_json, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: ListUserActivityLogs :many
SELECT * FROM user_activity_logs
WHERE user_id = sqlc.arg(user_id)::uuid
  AND (sqlc.arg(category)::text = '' OR category = sqlc.arg(category)::text)
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR created_at <= sqlc.narg(to_time)::timestamptz)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountUserActivityLogs :one
SELECT COUNT(*)::bigint FROM user_activity_logs
WHERE user_id = sqlc.arg(user_id)::uuid
  AND (sqlc.arg(category)::text = '' OR category = sqlc.arg(category)::text)
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR created_at <= sqlc.narg(to_time)::timestamptz);

-- name: DeleteExpiredUserActivityLogs :execrows
DELETE FROM user_activity_logs
WHERE id IN (
    SELECT id FROM user_activity_logs
    WHERE created_at < sqlc.arg(cutoff)::timestamptz
      AND created_at < NOW() - INTERVAL '180 days'
    ORDER BY created_at ASC
    LIMIT sqlc.arg(batch_limit)::int
);

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (
    id, aggregate_id, event_type, trace_id, traceparent, payload_json
) VALUES (
    $1, $2, $3, $4, $5, $6
);

-- name: ClaimPendingOutboxEvents :many
WITH locked AS (
    SELECT e.id,
           COALESCE(NULLIF(e.aggregate_id, ''), e.id) AS agg_id,
           e.seq
    FROM outbox_events e
    WHERE e.published_at IS NULL
      AND (e.claimed_until IS NULL OR e.claimed_until <= NOW())
      AND NOT EXISTS (
          SELECT 1
          FROM outbox_events prev
          WHERE prev.published_at IS NULL
            AND COALESCE(NULLIF(prev.aggregate_id, ''), prev.id) = COALESCE(NULLIF(e.aggregate_id, ''), e.id)
            AND prev.seq < e.seq
            AND prev.claimed_until IS NOT NULL
            AND prev.claimed_until > NOW()
      )
    ORDER BY e.seq ASC
    LIMIT sqlc.arg(batch_limit)::int
    FOR UPDATE OF e SKIP LOCKED
),
eligible AS (
    SELECT l.id
    FROM locked l
    WHERE NOT EXISTS (
        SELECT 1
        FROM outbox_events prev
        WHERE prev.published_at IS NULL
          AND COALESCE(NULLIF(prev.aggregate_id, ''), prev.id) = l.agg_id
          AND prev.seq < l.seq
          AND NOT EXISTS (SELECT 1 FROM locked l2 WHERE l2.id = prev.id)
    )
)
UPDATE outbox_events o
SET claimed_until = NOW() + (sqlc.arg(lease_ms)::bigint * INTERVAL '1 millisecond'),
    claim_token = sqlc.arg(claim_token)::text
FROM eligible
WHERE o.id = eligible.id
RETURNING o.*;

-- name: MarkOutboxEventPublished :execrows
UPDATE outbox_events
SET published_at = NOW(),
    attempts = attempts + 1,
    last_error = '',
    claimed_until = NULL,
    claim_token = ''
WHERE id = $1
  AND published_at IS NULL
  AND claim_token = $2;

-- name: MarkOutboxEventAttemptFailed :one
UPDATE outbox_events
SET attempts = attempts + 1,
    last_error = $2,
    claimed_until = CASE
        WHEN sqlc.arg(apply_backoff)::boolean THEN
            NOW() + (LEAST(POWER(2, LEAST(attempts + 1, 6)), 60)::bigint * INTERVAL '1 second')
        ELSE NULL
    END,
    claim_token = ''
WHERE id = $1
  AND published_at IS NULL
  AND claim_token = $3
RETURNING attempts;

-- name: ReleaseOutboxEventClaims :execrows
UPDATE outbox_events
SET claimed_until = NULL,
    claim_token = ''
WHERE id = ANY(sqlc.arg(ids)::text[])
  AND published_at IS NULL
  AND claim_token = sqlc.arg(claim_token)::text;
