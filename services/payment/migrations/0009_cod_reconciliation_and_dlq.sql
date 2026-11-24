-- Wave 3b-1 payment schema updates (E2 dead_letter_events & E20 COD carrier reconciliation):
-- 1. Create `dead_letter_events` table matching `order` service schema so poison NATS events
--    are durably persisted locally alongside `NEXUSMART_DLQ`.
-- 2. Extend `reconciliation_runs`, `reconciliation_items`, and `reconciliation_queue` for COD
--    carrier CSV reconciliation (`run_type = 'cod_file'`, `source_type = 'cod_file'`,
--    `already_reconciled` / `not_delivered` classifications, pagination indexes).
-- 3. Extend `payment_shop_allocations` with `tracking_code`, `carrier_code`, `order_code`,
--    `cod_reconciled_at`, and `cod_reconciliation_run_id` for tracking-code lookup and
--    per-allocation COD reconciliation tracking.

CREATE TABLE IF NOT EXISTS dead_letter_events (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    event_id TEXT NOT NULL DEFAULT '',
    payload BYTEA NOT NULL,
    error_message TEXT NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dead_letter_events_created_at
    ON dead_letter_events (created_at DESC);

ALTER TABLE reconciliation_runs
    ADD COLUMN IF NOT EXISTS run_type TEXT NOT NULL DEFAULT 'vietqr'
        CHECK (run_type IN ('vietqr', 'cod_file')),
    ADD COLUMN IF NOT EXISTS file_sha256 TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS file_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS total_rows INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_matched_vnd BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS already_reconciled_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS not_delivered_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_reconciliation_runs_type_created
    ON reconciliation_runs (run_type, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_reconciliation_runs_created
    ON reconciliation_runs (created_at DESC, id DESC);

ALTER TABLE payment_shop_allocations
    ADD COLUMN IF NOT EXISTS tracking_code TEXT NULL,
    ADD COLUMN IF NOT EXISTS carrier_code TEXT NULL,
    ADD COLUMN IF NOT EXISTS order_code TEXT NULL,
    ADD COLUMN IF NOT EXISTS cod_reconciled_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS cod_reconciliation_run_id UUID NULL REFERENCES reconciliation_runs(id);

CREATE INDEX IF NOT EXISTS idx_payment_allocations_tracking_code
    ON payment_shop_allocations (tracking_code)
    WHERE tracking_code IS NOT NULL AND tracking_code <> '';

CREATE INDEX IF NOT EXISTS idx_payment_allocations_order_code
    ON payment_shop_allocations (order_code)
    WHERE order_code IS NOT NULL AND order_code <> '';

ALTER TABLE reconciliation_items
    ADD COLUMN IF NOT EXISTS tracking_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS order_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS line_number INTEGER NOT NULL DEFAULT 0;

ALTER TABLE reconciliation_items
    DROP CONSTRAINT IF EXISTS reconciliation_items_classification_check;

ALTER TABLE reconciliation_items
    ADD CONSTRAINT reconciliation_items_classification_check
    CHECK (classification IN (
        'matched',
        'missing_local',
        'missing_provider',
        'amount_mismatch',
        'already_reconciled',
        'not_delivered'
    ));

ALTER TABLE reconciliation_queue
    ADD COLUMN IF NOT EXISTS source_type TEXT NOT NULL DEFAULT 'webhook'
        CHECK (source_type IN ('webhook', 'reconciliation', 'cod_file')),
    ADD COLUMN IF NOT EXISTS tracking_code TEXT NOT NULL DEFAULT '';

UPDATE reconciliation_queue
SET source_type = 'reconciliation'
WHERE webhook_event_id IS NULL
  AND source_type = 'webhook'
  AND reason LIKE 'reconciliation_%';

CREATE INDEX IF NOT EXISTS idx_reconciliation_queue_tracking_code
    ON reconciliation_queue (tracking_code)
    WHERE tracking_code <> '';
