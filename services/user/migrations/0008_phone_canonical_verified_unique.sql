-- 0008_phone_canonical_verified_unique.sql:
-- E19-bis: Canonicalize valid Vietnamese users.phone values to local 0-prefixed digits (0xxxxxxxxx),
-- deduplicate verified accounts sharing the same canonical phone (keeping the earliest updated_at/created_at
-- verified and resetting later duplicates to phone_verified=FALSE without clearing phone),
-- and replace idx_users_phone_nonempty with a verified-only partial unique index plus a regular lookup index.

DROP INDEX IF EXISTS idx_users_phone_nonempty;

WITH normalized AS (
    SELECT
        id,
        replace(replace(replace(regexp_replace(phone, '^\s+|\s+$', '', 'g'), ' ', ''), '-', ''), '.', '') AS norm_phone
    FROM users
    WHERE phone <> ''
),
canonical AS (
    SELECT
        id,
        CASE
            WHEN left(norm_phone, 3) = '+84' THEN '0' || substr(norm_phone, 4)
            WHEN left(norm_phone, 2) = '84' THEN '0' || substr(norm_phone, 3)
            ELSE norm_phone
        END AS canon_phone
    FROM normalized
    WHERE norm_phone ~ '^(0|\+?84)([35789][0-9]{8}|2[0-9]{9})$'
)
UPDATE users u
SET phone = c.canon_phone
FROM canonical c
WHERE u.id = c.id
  AND u.phone <> c.canon_phone;

WITH ranked AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY phone
            ORDER BY updated_at ASC, created_at ASC, id ASC
        ) AS rn
    FROM users
    WHERE phone_verified = TRUE
      AND phone <> ''
)
UPDATE users u
SET phone_verified = FALSE
FROM ranked r
WHERE u.id = r.id
  AND r.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_phone_verified_unique
    ON users (phone)
    WHERE phone_verified AND phone <> '';

CREATE INDEX IF NOT EXISTS idx_users_phone
    ON users (phone);
