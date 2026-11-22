-- Partial index to efficiently locate stale IN_PROGRESS checkout_groups for cleanup.

CREATE INDEX IF NOT EXISTS idx_checkout_groups_in_progress_updated_at
    ON checkout_groups (updated_at ASC)
    WHERE status = 'IN_PROGRESS';
