-- Wave 3b-3 (A8: G1-G3, G8): Seller carrier filter index and durable order_address_changes intent table.

-- G8 (S04/UC-S15): Composite index for seller order listing filtered by carrier_code ordered newest-first.
CREATE INDEX IF NOT EXISTS idx_orders_shop_carrier_created
    ON orders (shop_id, carrier_code, created_at DESC);

-- G3 (UC-B26): Durable intent & forward-recovery state machine for buyer shipping address updates.
-- Enforces at most one open intent per order, idempotency per (order_id, idempotency_key),
-- multi-pod watchdog lease claiming, and safe shipment cancellation / COD adjustment / recreation convergence.
CREATE TABLE IF NOT EXISTS order_address_changes (
    id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    buyer_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL DEFAULT '',
    request_hash TEXT NOT NULL DEFAULT '',
    new_address_json JSONB NOT NULL,
    carrier_code TEXT NOT NULL DEFAULT '',
    service_code TEXT NOT NULL DEFAULT '',
    old_carrier_code TEXT NOT NULL DEFAULT '',
    old_service_code TEXT NOT NULL DEFAULT '',
    old_tracking_code TEXT NOT NULL DEFAULT '',
    had_shipment BOOLEAN NOT NULL DEFAULT FALSE,
    shipment_cancelled BOOLEAN NOT NULL DEFAULT FALSE,
    old_shipping_fee_vnd BIGINT NOT NULL DEFAULT 0 CHECK (old_shipping_fee_vnd >= 0),
    new_shipping_fee_vnd BIGINT NOT NULL DEFAULT 0 CHECK (new_shipping_fee_vnd >= 0),
    applied_shipping_fee_vnd BIGINT NOT NULL DEFAULT 0 CHECK (applied_shipping_fee_vnd >= 0),
    old_total_payable_vnd BIGINT NOT NULL DEFAULT 0 CHECK (old_total_payable_vnd >= 0),
    new_total_payable_vnd BIGINT NOT NULL DEFAULT 0 CHECK (new_total_payable_vnd >= 0),
    fee_adjusted BOOLEAN NOT NULL DEFAULT FALSE,
    needs_cod_adjust BOOLEAN NOT NULL DEFAULT FALSE,
    cod_adjusted BOOLEAN NOT NULL DEFAULT FALSE,
    state TEXT NOT NULL CHECK (state IN (
        'OPEN',
        'SHIPMENT_CANCELLED',
        'COD_ADJUSTED',
        'APPLIED',
        'REVERTING_COD',
        'RESTORING_SHIPMENT',
        'COMPLETED',
        'ABORTED'
    )),
    shipment_recreated BOOLEAN NOT NULL DEFAULT FALSE,
    shipment_queued BOOLEAN NOT NULL DEFAULT FALSE,
    shipment_job_id TEXT NOT NULL DEFAULT '',
    new_tracking_code TEXT NOT NULL DEFAULT '',
    outbox_emitted BOOLEAN NOT NULL DEFAULT FALSE,
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_until TIMESTAMPTZ,
    claim_token TEXT NOT NULL DEFAULT '',
    abort_code TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    traceparent TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- At most one open (non-terminal) address-change intent per order at any time (G3).
CREATE UNIQUE INDEX IF NOT EXISTS idx_order_address_changes_one_open_per_order
    ON order_address_changes (order_id)
    WHERE state NOT IN ('COMPLETED', 'ABORTED');

-- Idempotency per (order_id, idempotency_key) when caller supplies an Idempotency-Key (G3).
CREATE UNIQUE INDEX IF NOT EXISTS idx_order_address_changes_order_idem
    ON order_address_changes (order_id, idempotency_key)
    WHERE idempotency_key <> '';

-- Watchdog index for claiming due open intents via FOR UPDATE SKIP LOCKED.
CREATE INDEX IF NOT EXISTS idx_order_address_changes_due
    ON order_address_changes (next_attempt_at ASC, created_at ASC)
    WHERE state NOT IN ('COMPLETED', 'ABORTED');

-- Fast lookup to suppress delayed shipment.updated events for tracking codes cancelled by address changes (G3).
CREATE INDEX IF NOT EXISTS idx_order_address_changes_old_tracking
    ON order_address_changes (order_id, old_tracking_code)
    WHERE old_tracking_code <> '' AND shipment_cancelled = TRUE;
