-- Wave 2b Round 5 (N7):
-- Persist promotion warnings on checkout_groups atomically with order batch persistence
-- so forward-recovery paths (watchdog / concurrent idempotent retry) preserve CreateCheckoutResponse.warnings.

ALTER TABLE checkout_groups
    ADD COLUMN IF NOT EXISTS warnings_json JSONB NOT NULL DEFAULT '[]'::jsonb;
