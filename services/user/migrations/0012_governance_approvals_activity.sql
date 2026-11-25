-- 0012_governance_approvals_activity.sql:
-- Wave 3b-2 (A7: F10-F14):
--   1. Shop suspension metadata & monotonic status_version on shops.
--   2. Two-person shop suspension approvals (maker != checker CHECK, one PENDING per shop).
--   3. Append-only user_activity_logs with trigger blocking UPDATE and blocking DELETE for rows < 180 days old.
--   4. Lease-based transactional outbox_events table for user domain events (user.shop_status_changed).

-- 1. Extend shops with suspension columns and monotonic status_version.
ALTER TABLE shops
    ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS suspended_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_version BIGINT NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_shops_status_version_positive'
          AND conrelid = 'shops'::regclass
    ) THEN
        ALTER TABLE shops
            ADD CONSTRAINT chk_shops_status_version_positive CHECK (status_version >= 1);
    END IF;
END $$;

-- Backfill suspended_at on any pre-existing SUSPENDED shops when upgrading a populated DB.
UPDATE shops
SET suspended_at = updated_at
WHERE status = 'SUSPENDED'
  AND suspended_at IS NULL;

-- 2. Two-person approval queue for suspending active shops (F11).
CREATE TABLE IF NOT EXISTS shop_suspension_approvals (
    id UUID PRIMARY KEY,
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
    reason TEXT NOT NULL,
    requested_by UUID NOT NULL,
    requested_role TEXT NOT NULL,
    decided_by UUID,
    decided_role TEXT NOT NULL DEFAULT '',
    decided_note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ,
    CONSTRAINT chk_shop_suspension_maker_neq_checker
        CHECK (decided_by IS NULL OR decided_by <> requested_by),
    CONSTRAINT chk_shop_suspension_decision_consistency
        CHECK (
            (status = 'PENDING' AND decided_by IS NULL AND decided_at IS NULL)
            OR (status IN ('APPROVED', 'REJECTED') AND decided_by IS NOT NULL AND decided_at IS NOT NULL)
        )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_shop_suspension_approvals_one_pending_per_shop
    ON shop_suspension_approvals (shop_id)
    WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_shop_suspension_approvals_status_created
    ON shop_suspension_approvals (status, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_shop_suspension_approvals_shop_created
    ON shop_suspension_approvals (shop_id, created_at DESC, id DESC);

-- 3. User activity logs with 180-day retention guard trigger (F13).
CREATE TABLE IF NOT EXISTS user_activity_logs (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    ip_address TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_activity_logs_user_created
    ON user_activity_logs (user_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_user_activity_logs_user_cat_created
    ON user_activity_logs (user_id, category, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_user_activity_logs_created
    ON user_activity_logs (created_at ASC);

CREATE OR REPLACE FUNCTION guard_user_activity_log_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'user_activity_logs is append-only: UPDATE is forbidden';
    END IF;
    IF TG_OP = 'DELETE' THEN
        IF OLD.created_at >= NOW() - INTERVAL '180 days' THEN
            RAISE EXCEPTION 'user_activity_logs: DELETE is only allowed for rows with created_at < now() - interval ''180 days''';
        END IF;
        RETURN OLD;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_user_activity_logs_no_update ON user_activity_logs;
CREATE TRIGGER trg_user_activity_logs_no_update
    BEFORE UPDATE ON user_activity_logs
    FOR EACH ROW EXECUTE FUNCTION guard_user_activity_log_mutation();

DROP TRIGGER IF EXISTS trg_user_activity_logs_retention_delete ON user_activity_logs;
CREATE TRIGGER trg_user_activity_logs_retention_delete
    BEFORE DELETE ON user_activity_logs
    FOR EACH ROW EXECUTE FUNCTION guard_user_activity_log_mutation();

-- 4. Transactional outbox table with lease/seq ordering (same pattern as order/payment, F12).
CREATE TABLE IF NOT EXISTS outbox_events (
    id TEXT PRIMARY KEY,
    seq BIGSERIAL NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL,
    trace_id TEXT NOT NULL DEFAULT '',
    traceparent TEXT NOT NULL DEFAULT '',
    payload_json JSONB NOT NULL,
    published_at TIMESTAMPTZ,
    claimed_until TIMESTAMPTZ,
    claim_token TEXT NOT NULL DEFAULT '',
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_outbox_events_claimable
    ON outbox_events (seq ASC)
    WHERE published_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_outbox_events_pending_agg_seq
    ON outbox_events ((COALESCE(NULLIF(aggregate_id, ''), id)), seq ASC)
    WHERE published_at IS NULL;
