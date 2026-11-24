-- Wave 3a payment schema updates (D9 / D9-bis lease-based outbox relay & aligned payment_fee_configs index):
-- 1. Add monotonic `seq` + lease columns (`claimed_until`, `claim_token`) and partial `seq`-ordered
--    indexes to `outbox_events` so the outbox relay claims batches in a short transaction ordered
--    strictly by `seq` (never `created_at`), commits, publishes outside any DB transaction, and
--    finalizes rows guarded by `claim_token`.
-- 2. Replace the misaligned `idx_payment_fee_configs_method_effective` index (which had
--    `created_at DESC` ahead of `seq DESC`) with `(payment_method ASC, effective_from DESC, seq DESC)`
--    matching `GetFeeConfig` queries with zero Sort.
--
-- DEPLOY NOTE: Stop all old payment workers BEFORE applying this migration. Legacy workers do not
-- know about `claimed_until` / `claim_token` / `seq` ordering and would race with lease-based workers.

ALTER TABLE outbox_events
    ADD COLUMN IF NOT EXISTS seq BIGSERIAL NOT NULL,
    ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS claim_token UUID NULL;

-- D9-bis point 10: `ADD COLUMN seq BIGSERIAL` assigns values in physical heap (`ctid`) order,
-- which can invert older updated rows behind newer inserts. Re-assign `seq` for still-pending rows
-- strictly in `(created_at ASC, id ASC)` order using `nextval` on the column's real sequence.
WITH sorted AS MATERIALIZED (
    SELECT id
    FROM outbox_events
    WHERE status = 'pending'
    ORDER BY created_at ASC, id ASC
),
numbered AS (
    SELECT id,
           nextval(pg_get_serial_sequence('outbox_events', 'seq')) AS new_seq
    FROM sorted
)
UPDATE outbox_events o
SET seq = n.new_seq
FROM numbered n
WHERE o.id = n.id;

CREATE INDEX IF NOT EXISTS idx_outbox_events_pending_claim
    ON outbox_events (seq ASC)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_outbox_events_pending_aggregate
    ON outbox_events (aggregate_id, seq ASC)
    WHERE status = 'pending' AND aggregate_id <> '';

CREATE INDEX IF NOT EXISTS idx_payment_fee_configs_method_effective_seq
    ON payment_fee_configs (payment_method ASC, effective_from DESC, seq DESC);

DROP INDEX IF EXISTS idx_payment_fee_configs_method_effective;
