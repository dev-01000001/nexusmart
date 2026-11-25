-- Wave 3b-3 (A8: G3, G5):
--   Store sender/recipient contact and street address fields on shipments for A6 batch PDF label printing.
-- Idempotent: shipping runs all migration files on every startup.

ALTER TABLE shipments
    ADD COLUMN IF NOT EXISTS from_name TEXT NOT NULL DEFAULT '';

ALTER TABLE shipments
    ADD COLUMN IF NOT EXISTS from_phone TEXT NOT NULL DEFAULT '';

ALTER TABLE shipments
    ADD COLUMN IF NOT EXISTS from_street_address TEXT NOT NULL DEFAULT '';

ALTER TABLE shipments
    ADD COLUMN IF NOT EXISTS to_name TEXT NOT NULL DEFAULT '';

ALTER TABLE shipments
    ADD COLUMN IF NOT EXISTS to_phone TEXT NOT NULL DEFAULT '';

ALTER TABLE shipments
    ADD COLUMN IF NOT EXISTS to_street_address TEXT NOT NULL DEFAULT '';
