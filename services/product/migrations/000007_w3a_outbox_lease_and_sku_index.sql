-- 000007_w3a_outbox_lease_and_sku_index.sql:
-- D9 / D9-bis: outbox lease columns (aggregate_id, seq, claimed_until, claim_token) for out-of-tx publish
-- and strict seq-based per-aggregate ordering.
-- DEPLOY NOTE: Stop all old product workers BEFORE applying this migration so legacy workers
-- (which do not respect claimed_until / claim_token / seq ordering) cannot race with the backfill
-- or publish concurrently with lease-based workers.

ALTER TABLE outbox_events
    ADD COLUMN IF NOT EXISTS aggregate_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS seq BIGSERIAL NOT NULL,
    ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS claim_token TEXT NOT NULL DEFAULT '';

UPDATE outbox_events
SET aggregate_id = BTRIM(COALESCE(payload_json->>'product_id', ''))
WHERE aggregate_id = ''
  AND published_at IS NULL
  AND BTRIM(COALESCE(payload_json->>'product_id', '')) <> '';

-- Reassign monotonic seq for unpublished rows in strict (created_at, id) order so rows whose
-- physical ctid shifted from pre-3a UPDATEs never invert ordering after migration.
WITH ordered AS MATERIALIZED (
    SELECT id
    FROM outbox_events
    WHERE published_at IS NULL
    ORDER BY created_at ASC, id ASC
),
assigned AS (
    SELECT id, nextval(pg_get_serial_sequence('outbox_events', 'seq')) AS new_seq
    FROM ordered
)
UPDATE outbox_events o
SET seq = assigned.new_seq
FROM assigned
WHERE o.id = assigned.id;

DROP INDEX IF EXISTS idx_product_outbox_events_unpublished;

CREATE INDEX IF NOT EXISTS idx_product_outbox_events_claimable
    ON outbox_events (seq ASC)
    WHERE published_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_product_outbox_events_aggregate_unpublished
    ON outbox_events (aggregate_id, seq ASC)
    WHERE published_at IS NULL AND aggregate_id <> '';
