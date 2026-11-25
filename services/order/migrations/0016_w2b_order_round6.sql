-- Wave 2b Round 6 (M3):
-- Partial index supporting claim-first stale checkout cleanup (including FAILED + needs_cleanup = TRUE).

CREATE INDEX IF NOT EXISTS idx_checkout_groups_needs_cleanup
    ON checkout_groups (updated_at)
    WHERE needs_cleanup = TRUE;
