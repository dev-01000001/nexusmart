-- NexusMart Payment Service: Phase 1 Sales Channel attribution & immutability
-- Additive & backward-compatible: all legacy rows default to 'MARKETPLACE' with storefront_shop_id = ''.

-- =============================================================================
-- 1. Payments: immutable sales_channel & storefront_shop_id
-- =============================================================================

ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS sales_channel TEXT NOT NULL DEFAULT 'MARKETPLACE',
    ADD COLUMN IF NOT EXISTS storefront_shop_id TEXT NOT NULL DEFAULT '';

ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS chk_payments_sales_channel,
    DROP CONSTRAINT IF EXISTS chk_payments_storefront_shop_consistency;

ALTER TABLE payments
    ADD CONSTRAINT chk_payments_sales_channel
        CHECK (sales_channel IN ('MARKETPLACE', 'STOREFRONT')),
    ADD CONSTRAINT chk_payments_storefront_shop_consistency
        CHECK (
            (sales_channel = 'MARKETPLACE' AND storefront_shop_id = '') OR
            (sales_channel = 'STOREFRONT' AND BTRIM(storefront_shop_id) <> '')
        );

CREATE INDEX IF NOT EXISTS idx_payments_sales_channel
    ON payments (sales_channel, created_at);

-- =============================================================================
-- 2. Payment Shop Allocations: immutable sales_channel & storefront_shop_id
-- =============================================================================

ALTER TABLE payment_shop_allocations
    ADD COLUMN IF NOT EXISTS sales_channel TEXT NOT NULL DEFAULT 'MARKETPLACE',
    ADD COLUMN IF NOT EXISTS storefront_shop_id TEXT NOT NULL DEFAULT '';

ALTER TABLE payment_shop_allocations
    DROP CONSTRAINT IF EXISTS chk_payment_allocations_sales_channel,
    DROP CONSTRAINT IF EXISTS chk_payment_allocations_storefront_shop_consistency;

ALTER TABLE payment_shop_allocations
    ADD CONSTRAINT chk_payment_allocations_sales_channel
        CHECK (sales_channel IN ('MARKETPLACE', 'STOREFRONT')),
    ADD CONSTRAINT chk_payment_allocations_storefront_shop_consistency
        CHECK (
            (sales_channel = 'MARKETPLACE' AND storefront_shop_id = '') OR
            (sales_channel = 'STOREFRONT' AND BTRIM(storefront_shop_id) <> '' AND storefront_shop_id = shop_id)
        );

CREATE INDEX IF NOT EXISTS idx_payment_allocations_sales_channel
    ON payment_shop_allocations (sales_channel, shop_id);

-- Enforce immutability of sales_channel and storefront_shop_id on updates
CREATE OR REPLACE FUNCTION prevent_sales_channel_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.sales_channel IS DISTINCT FROM OLD.sales_channel
        OR NEW.storefront_shop_id IS DISTINCT FROM OLD.storefront_shop_id THEN
        RAISE EXCEPTION 'sales_channel and storefront_shop_id are immutable after creation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_payments_sales_channel_immutable ON payments;
CREATE TRIGGER trg_payments_sales_channel_immutable
BEFORE UPDATE ON payments
FOR EACH ROW
EXECUTE FUNCTION prevent_sales_channel_mutation();

DROP TRIGGER IF EXISTS trg_payment_allocations_sales_channel_immutable ON payment_shop_allocations;
CREATE TRIGGER trg_payment_allocations_sales_channel_immutable
BEFORE UPDATE ON payment_shop_allocations
FOR EACH ROW
EXECUTE FUNCTION prevent_sales_channel_mutation();

-- =============================================================================
-- 3. Double-Entry Ledger: channel attribution + JSONB metadata
--    (ledger_transactions and ledger_entries are already append-only immutable
--     via trg_ledger_transactions_immutable and trg_ledger_entries_immutable.)
-- =============================================================================

ALTER TABLE ledger_transactions
    ADD COLUMN IF NOT EXISTS sales_channel TEXT NOT NULL DEFAULT 'MARKETPLACE',
    ADD COLUMN IF NOT EXISTS storefront_shop_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{"sales_channel":"MARKETPLACE","storefront_shop_id":""}'::jsonb;

ALTER TABLE ledger_transactions
    DROP CONSTRAINT IF EXISTS chk_ledger_transactions_sales_channel,
    DROP CONSTRAINT IF EXISTS chk_ledger_transactions_storefront_shop_consistency;

ALTER TABLE ledger_transactions
    ADD CONSTRAINT chk_ledger_transactions_sales_channel
        CHECK (sales_channel IN ('MARKETPLACE', 'STOREFRONT')),
    ADD CONSTRAINT chk_ledger_transactions_storefront_shop_consistency
        CHECK (
            (sales_channel = 'MARKETPLACE' AND storefront_shop_id = '') OR
            (sales_channel = 'STOREFRONT' AND BTRIM(storefront_shop_id) <> '')
        );

CREATE INDEX IF NOT EXISTS idx_ledger_transactions_sales_channel
    ON ledger_transactions (sales_channel);

ALTER TABLE ledger_entries
    ADD COLUMN IF NOT EXISTS sales_channel TEXT NOT NULL DEFAULT 'MARKETPLACE',
    ADD COLUMN IF NOT EXISTS storefront_shop_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{"sales_channel":"MARKETPLACE","storefront_shop_id":""}'::jsonb;

ALTER TABLE ledger_entries
    DROP CONSTRAINT IF EXISTS chk_ledger_entries_sales_channel,
    DROP CONSTRAINT IF EXISTS chk_ledger_entries_storefront_shop_consistency;

ALTER TABLE ledger_entries
    ADD CONSTRAINT chk_ledger_entries_sales_channel
        CHECK (sales_channel IN ('MARKETPLACE', 'STOREFRONT')),
    ADD CONSTRAINT chk_ledger_entries_storefront_shop_consistency
        CHECK (
            (sales_channel = 'MARKETPLACE' AND storefront_shop_id = '') OR
            (sales_channel = 'STOREFRONT' AND BTRIM(storefront_shop_id) <> '')
        );

CREATE INDEX IF NOT EXISTS idx_ledger_entries_sales_channel
    ON ledger_entries (sales_channel, account_id);

-- =============================================================================
-- 4. Reconciliation runs & items: channel filter & item attribution
-- =============================================================================

ALTER TABLE reconciliation_runs
    ADD COLUMN IF NOT EXISTS sales_channel TEXT NOT NULL DEFAULT '';

ALTER TABLE reconciliation_runs
    DROP CONSTRAINT IF EXISTS chk_reconciliation_runs_sales_channel;

ALTER TABLE reconciliation_runs
    ADD CONSTRAINT chk_reconciliation_runs_sales_channel
        CHECK (sales_channel IN ('', 'MARKETPLACE', 'STOREFRONT'));

ALTER TABLE reconciliation_items
    ADD COLUMN IF NOT EXISTS sales_channel TEXT NOT NULL DEFAULT 'MARKETPLACE',
    ADD COLUMN IF NOT EXISTS storefront_shop_id TEXT NOT NULL DEFAULT '';

ALTER TABLE reconciliation_items
    DROP CONSTRAINT IF EXISTS chk_reconciliation_items_sales_channel;

ALTER TABLE reconciliation_items
    ADD CONSTRAINT chk_reconciliation_items_sales_channel
        CHECK (sales_channel IN ('', 'MARKETPLACE', 'STOREFRONT'));
