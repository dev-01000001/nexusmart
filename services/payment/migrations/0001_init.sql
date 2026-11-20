-- NexusMart Payment Service initial schema
-- Money is strictly integer VND (BIGINT). Never floats.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- =============================================================================
-- 1. Double-Entry Ledger
-- =============================================================================

CREATE TABLE ledger_accounts (
    id TEXT PRIMARY KEY,
    account_type TEXT NOT NULL CHECK (account_type IN ('ASSET', 'LIABILITY', 'REVENUE', 'EXPENSE', 'EQUITY')),
    shop_id TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_accounts_shop_id ON ledger_accounts (shop_id) WHERE shop_id IS NOT NULL;

INSERT INTO ledger_accounts (id, account_type, shop_id) VALUES
    ('gateway_clearing', 'ASSET', NULL),
    ('cod_receivable', 'ASSET', NULL),
    ('escrow_liability', 'LIABILITY', NULL),
    ('platform_fee_revenue', 'REVENUE', NULL),
    ('shipping_fee_payable', 'LIABILITY', NULL),
    ('refund_payable', 'LIABILITY', NULL)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE ledger_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key TEXT NOT NULL UNIQUE,
    reference_type TEXT NOT NULL,
    reference_id TEXT NOT NULL,
    description TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_transactions_ref ON ledger_transactions (reference_type, reference_id);

CREATE TABLE ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    account_id TEXT NOT NULL REFERENCES ledger_accounts(id) ON DELETE RESTRICT,
    debit_vnd BIGINT NOT NULL DEFAULT 0 CHECK (debit_vnd >= 0),
    credit_vnd BIGINT NOT NULL DEFAULT 0 CHECK (credit_vnd >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_ledger_entry_single_sided CHECK (
        (debit_vnd > 0 AND credit_vnd = 0) OR
        (credit_vnd > 0 AND debit_vnd = 0)
    )
);

CREATE INDEX idx_ledger_entries_tx_id ON ledger_entries (transaction_id);
CREATE INDEX idx_ledger_entries_account_id ON ledger_entries (account_id);

-- Enforce double-entry balance at COMMIT time inside PostgreSQL
CREATE OR REPLACE FUNCTION enforce_ledger_balance() RETURNS TRIGGER AS $$
DECLARE
    v_tx_id UUID;
    v_total_debit BIGINT;
    v_total_credit BIGINT;
    v_count INTEGER;
BEGIN
    v_tx_id := COALESCE(NEW.transaction_id, OLD.transaction_id);

    SELECT
        COALESCE(SUM(debit_vnd), 0),
        COALESCE(SUM(credit_vnd), 0),
        COUNT(*)
    INTO v_total_debit, v_total_credit, v_count
    FROM ledger_entries
    WHERE transaction_id = v_tx_id;

    IF v_count < 2 THEN
        RAISE EXCEPTION 'Ledger transaction % must have at least 2 entries (found %)', v_tx_id, v_count;
    END IF;

    IF v_total_debit <= 0 OR v_total_debit <> v_total_credit THEN
        RAISE EXCEPTION 'Ledger transaction % is unbalanced: debit=% credit=%', v_tx_id, v_total_debit, v_total_credit;
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_ledger_entries_balanced
AFTER INSERT OR UPDATE OR DELETE ON ledger_entries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION enforce_ledger_balance();

-- Append-only ledger immutability
CREATE OR REPLACE FUNCTION prevent_ledger_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Ledger is append-only; updates and deletes are forbidden';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ledger_entries_immutable
BEFORE UPDATE OR DELETE ON ledger_entries
FOR EACH ROW
EXECUTE FUNCTION prevent_ledger_mutation();

CREATE TRIGGER trg_ledger_transactions_immutable
BEFORE UPDATE OR DELETE ON ledger_transactions
FOR EACH ROW
EXECUTE FUNCTION prevent_ledger_mutation();

-- =============================================================================
-- 2. Payments & Shop Allocations
-- =============================================================================

CREATE TABLE payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_group_id TEXT NOT NULL,
    order_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    method TEXT NOT NULL CHECK (method IN ('COD', 'VIETQR')),
    provider TEXT NOT NULL,
    amount_vnd BIGINT NOT NULL CHECK (amount_vnd > 0),
    status TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed', 'expired', 'cancelled', 'superseded', 'refunded')),
    provider_tx_id TEXT NULL UNIQUE,
    provider_order_code TEXT NOT NULL UNIQUE,
    qr_code TEXT NULL,
    qr_url TEXT NULL,
    idempotency_key TEXT NULL UNIQUE,
    expires_at TIMESTAMPTZ NULL,
    paid_at TIMESTAMPTZ NULL,
    trace_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- UC-B25 invariant: at most ONE succeeded payment per order
CREATE UNIQUE INDEX idx_payments_one_succeeded_per_order
    ON payments (order_id) WHERE status = 'succeeded';

CREATE INDEX idx_payments_order_id ON payments (order_id);
CREATE INDEX idx_payments_order_group_id ON payments (order_group_id);
CREATE INDEX idx_payments_pending_expiry ON payments (expires_at) WHERE status = 'pending' AND method = 'VIETQR';

CREATE TABLE payment_shop_allocations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    order_id TEXT NOT NULL,
    shop_id TEXT NOT NULL,
    subtotal_vnd BIGINT NOT NULL CHECK (subtotal_vnd >= 0),
    commission_vnd BIGINT NOT NULL CHECK (commission_vnd >= 0),
    shipping_fee_vnd BIGINT NOT NULL CHECK (shipping_fee_vnd >= 0),
    escrow_status TEXT NOT NULL DEFAULT 'held' CHECK (escrow_status IN ('held', 'pending_release', 'released', 'refunded')),
    settled_at TIMESTAMPTZ NULL,
    released_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_allocations_payment_id ON payment_shop_allocations (payment_id);
CREATE INDEX idx_payment_allocations_order_id ON payment_shop_allocations (order_id);
CREATE INDEX idx_payment_allocations_shop_id ON payment_shop_allocations (shop_id);

-- =============================================================================
-- 3. Webhooks (Strict Idempotency & Replay Protection)
-- =============================================================================

CREATE TABLE webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,
    provider_tx_id TEXT NOT NULL UNIQUE,
    payment_id UUID NULL REFERENCES payments(id),
    order_ref TEXT NULL,
    amount_vnd BIGINT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('processed', 'mismatched', 'late_refund_queued', 'ignored')),
    raw_payload JSONB NOT NULL,
    signature_verified BOOLEAN NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ NULL
);

CREATE INDEX idx_webhook_events_payment_id ON webhook_events (payment_id);

-- =============================================================================
-- 4. Refunds (UC-PAY04)
-- =============================================================================

CREATE TABLE refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id UUID NOT NULL REFERENCES payments(id),
    order_id TEXT NOT NULL,
    amount_vnd BIGINT NOT NULL CHECK (amount_vnd > 0),
    reason TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'failed', 'manual_review')),
    idempotency_key TEXT NOT NULL UNIQUE,
    provider_refund_id TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_refunds_payment_id ON refunds (payment_id);
CREATE INDEX idx_refunds_order_id ON refunds (order_id);

-- =============================================================================
-- 5. Manual Reconciliation Queue (UC-A14) & Scheduled Reconciliation (UC-PAY07)
-- =============================================================================

CREATE TABLE reconciliation_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_event_id UUID NULL REFERENCES webhook_events(id),
    provider TEXT NOT NULL,
    provider_tx_id TEXT NOT NULL,
    expected_payment_id UUID NULL REFERENCES payments(id),
    order_ref TEXT NULL,
    expected_amount_vnd BIGINT NULL,
    actual_amount_vnd BIGINT NOT NULL,
    reason TEXT NOT NULL,
    resolution_status TEXT NOT NULL DEFAULT 'pending' CHECK (resolution_status IN ('pending', 'assigned', 'refunded', 'suspended')),
    notes TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ NULL
);

CREATE INDEX idx_reconciliation_queue_status ON reconciliation_queue (resolution_status, created_at);

CREATE TABLE reconciliation_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,
    run_date DATE NOT NULL,
    matched_count INTEGER NOT NULL DEFAULT 0,
    missing_local_count INTEGER NOT NULL DEFAULT 0,
    missing_provider_count INTEGER NOT NULL DEFAULT 0,
    amount_mismatch_count INTEGER NOT NULL DEFAULT 0,
    auto_healed_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NULL
);

CREATE TABLE reconciliation_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES reconciliation_runs(id) ON DELETE CASCADE,
    classification TEXT NOT NULL CHECK (classification IN ('matched', 'missing_local', 'missing_provider', 'amount_mismatch')),
    payment_id UUID NULL REFERENCES payments(id),
    provider_tx_id TEXT NULL,
    local_amount_vnd BIGINT NULL,
    provider_amount_vnd BIGINT NULL,
    details TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_reconciliation_items_run_id ON reconciliation_items (run_id);

-- =============================================================================
-- 6. Seller Payouts (UC-PAY05 / UC-S17)
-- =============================================================================

CREATE TABLE seller_payouts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id TEXT NOT NULL,
    amount_vnd BIGINT NOT NULL CHECK (amount_vnd > 0),
    bank_account_masked TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'rejected')),
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NULL
);

CREATE INDEX idx_seller_payouts_shop_id ON seller_payouts (shop_id);

-- =============================================================================
-- 7. Transactional Outbox & Idempotent NATS Consumer Tracking
-- =============================================================================

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    trace_id TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ NULL
);

CREATE INDEX idx_outbox_events_pending ON outbox_events (created_at) WHERE status = 'pending';

CREATE TABLE processed_nats_events (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
