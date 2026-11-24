-- Wave 3a (D9 / D9-bis): Transactional outbox lease columns (aggregate_id, seq, claimed_until, claim_token,
-- attempts, last_error) so outbox relay claims batches in a short transaction, commits, publishes
-- outside any DB transaction, and preserves strict per-aggregate seq order.
-- DEPLOY NOTE: Stop all old shipping workers BEFORE applying this migration so legacy workers
-- (which do not respect claimed_until / claim_token / seq ordering) cannot race with the backfill
-- or publish concurrently with lease-based workers.
-- Idempotent: shipping runs all migration files on every startup; once column `seq` exists this block
-- exits immediately without taking ACCESS EXCLUSIVE or rewriting any rows.
-- Note: legacy idx_outbox_unpublished (created_at) from 000001_init.sql is kept because shipping re-runs 000001 on every startup.

DO $$
DECLARE
    seq_name TEXT;
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'outbox_events'
          AND column_name = 'seq'
    ) THEN
        ALTER TABLE outbox_events
            ADD COLUMN IF NOT EXISTS aggregate_id TEXT NOT NULL DEFAULT '',
            ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ,
            ADD COLUMN IF NOT EXISTS claim_token TEXT NOT NULL DEFAULT '',
            ADD COLUMN IF NOT EXISTS attempts INT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '',
            ADD COLUMN seq BIGSERIAL NOT NULL;

        UPDATE outbox_events
        SET aggregate_id = COALESCE(
            NULLIF(BTRIM(payload->>'shipment_id'), ''),
            NULLIF(BTRIM(payload->>'order_id'), ''),
            ''
        )
        WHERE aggregate_id = ''
          AND published_at IS NULL
          AND COALESCE(
              NULLIF(BTRIM(payload->>'shipment_id'), ''),
              NULLIF(BTRIM(payload->>'order_id'), ''),
              ''
          ) <> '';

        seq_name := pg_get_serial_sequence(format('%I.%I', current_schema(), 'outbox_events'), 'seq');
        WITH ordered AS MATERIALIZED (
            SELECT id
            FROM outbox_events
            WHERE published_at IS NULL
            ORDER BY created_at ASC, id ASC
        ),
        assigned AS (
            SELECT id, nextval(seq_name) AS new_seq
            FROM ordered
        )
        UPDATE outbox_events o
        SET seq = assigned.new_seq
        FROM assigned
        WHERE o.id = assigned.id;

        CREATE INDEX IF NOT EXISTS idx_shipping_outbox_claimable
            ON outbox_events (seq ASC)
            WHERE published_at IS NULL;

        CREATE INDEX IF NOT EXISTS idx_shipping_outbox_aggregate_unpublished
            ON outbox_events (aggregate_id, seq ASC)
            WHERE published_at IS NULL AND aggregate_id <> '';
    END IF;
END
$$;
