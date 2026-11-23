-- 002_partial_release.sql: Track per-shop partial promotion releases on checkout groups.

ALTER TABLE promotion_reservations
    ADD COLUMN IF NOT EXISTS released_shops_json JSONB NOT NULL DEFAULT '[]'::jsonb;
