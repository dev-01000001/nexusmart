-- 0009_shop_verified_owner_name_and_otp_publish.sql:
-- 1) Snapshot verified_owner_name on shops upon admin approval so UpdateProfile cannot bypass bank holder review.
-- 2) Track otp_publish_failed on shop_bank_accounts so failed NATS publishes do not consume the 5/hour rate limit.

ALTER TABLE shops
    ADD COLUMN IF NOT EXISTS verified_owner_name TEXT NOT NULL DEFAULT '';

UPDATE shops s
SET verified_owner_name = u.full_name
FROM users u
WHERE s.owner_user_id = u.id
  AND s.status IN ('ACTIVE', 'SUSPENDED')
  AND s.verified_owner_name = '';

ALTER TABLE shop_bank_accounts
    ADD COLUMN IF NOT EXISTS otp_publish_failed BOOLEAN NOT NULL DEFAULT FALSE;
