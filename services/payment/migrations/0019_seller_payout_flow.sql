-- Seller Payout two-step approval workflow (UC-PAY05 / Swarm P).
-- Forward-only migration extending seller_payouts with bank metadata, actor audit fields,
-- rejection/transfer references, normalized UPPERCASE statuses ('PENDING', 'COMPLETED', 'REJECTED'),
-- and a partial unique index enforcing at most one PENDING payout per shop.

ALTER TABLE seller_payouts
    ADD COLUMN IF NOT EXISTS bank_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS account_holder_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS requested_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS decided_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reject_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS transfer_ref TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS decided_at TIMESTAMPTZ NULL;

ALTER TABLE seller_payouts DROP CONSTRAINT IF EXISTS seller_payouts_status_check;

-- Normalize legacy lowercase statuses ('pending', 'completed', 'rejected') to UPPERCASE.
UPDATE seller_payouts
SET status = UPPER(status),
    decided_at = COALESCE(decided_at, completed_at)
WHERE status <> UPPER(status)
   OR (UPPER(status) IN ('COMPLETED', 'REJECTED') AND decided_at IS NULL AND completed_at IS NOT NULL);

ALTER TABLE seller_payouts
    ADD CONSTRAINT seller_payouts_status_check
    CHECK (status IN ('PENDING', 'COMPLETED', 'REJECTED'));

-- Enforce at most one open PENDING payout request per shop.
CREATE UNIQUE INDEX IF NOT EXISTS uq_seller_payouts_one_pending_per_shop
    ON seller_payouts (shop_id)
    WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_seller_payouts_status_created_at
    ON seller_payouts (status, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_seller_payouts_shop_created_at
    ON seller_payouts (shop_id, created_at DESC, id DESC);
