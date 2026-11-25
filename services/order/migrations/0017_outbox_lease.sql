-- Wave 3a (D9 / D9-bis / P3-9): Transactional outbox claim lease, claim token, monotonic sequence,
-- per-aggregate pending seq index, and checkout_groups idempotency index.
--
-- DEPLOY REQUIREMENT (D9-bis §6):
--   STOP ALL OLD order-service outbox workers BEFORE running this migration and starting new workers.
--   1. Old workers use LockPendingOutboxEvents (no claimed_until/claim_token check) and would race
--      with new lease-based workers publishing outside TX, breaking per-aggregate ordering.
--   2. ADD COLUMN seq BIGSERIAL NOT NULL has a volatile default and rewrites outbox_events under an
--      ACCESS EXCLUSIVE lock; deploy during a maintenance window with workers drained.

ALTER TABLE outbox_events
    ADD COLUMN IF NOT EXISTS seq BIGSERIAL NOT NULL,
    ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS claim_token TEXT NOT NULL DEFAULT '';

-- D9-bis §10 (P3-3): ADD COLUMN seq BIGSERIAL populates existing rows in physical heap (ctid) order,
-- which can invert created_at order when an older pending row was updated prior to migration
-- (MVCC rewrite). Reassign seq in one set-based UPDATE strictly by (created_at ASC, id ASC) for
-- unpublished rows using nextval on the exact sequence backing outbox_events.seq.
WITH ordered AS (
    SELECT id,
           nextval(pg_get_serial_sequence('outbox_events', 'seq')) AS new_seq
    FROM (
        SELECT id
        FROM outbox_events
        WHERE published_at IS NULL
        ORDER BY created_at ASC, id ASC
        OFFSET 0
    ) sub
)
UPDATE outbox_events o
SET seq = ordered.new_seq
FROM ordered
WHERE o.id = ordered.id;

-- P3-3: Drop superseded created_at pending index now replaced by idx_outbox_events_claimable (seq ASC).
DROP INDEX IF EXISTS idx_outbox_events_unpublished;

-- D9-bis §1: order strictly by monotonic seq (never created_at, which freezes at BEGIN TX).
CREATE INDEX IF NOT EXISTS idx_outbox_events_claimable
    ON outbox_events (seq ASC)
    WHERE published_at IS NULL;

-- D9-bis §2: accelerate per-aggregate NOT EXISTS lookups in ClaimPendingOutboxEvents.
CREATE INDEX IF NOT EXISTS idx_outbox_events_pending_agg_seq
    ON outbox_events ((COALESCE(NULLIF(payload_json->>'order_id', ''), id)), seq ASC)
    WHERE published_at IS NULL;

-- P3-9: accelerate GetCheckoutGroupForIdempotency across active, needs_cleanup, and FAILED-with-orders rows.
CREATE INDEX IF NOT EXISTS idx_checkout_groups_buyer_idem
    ON checkout_groups (buyer_id, idempotency_key);
