-- 0013_pickup_address_guard_index.sql: Cover ACTIVE, SUSPENDED, and PENDING shops in
-- pickup_address_id deletion guard so suspended/pending shops cannot delete their
-- pickup address while in-flight orders or shop review still reference it (F12).

CREATE INDEX IF NOT EXISTS idx_shops_in_use_pickup_address
    ON shops (pickup_address_id)
    WHERE status IN ('ACTIVE', 'SUSPENDED', 'PENDING') AND pickup_address_id <> '';

DROP INDEX IF EXISTS idx_shops_active_pickup_address;
