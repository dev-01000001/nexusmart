ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS dedup_key TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS uq_notifications_dedup_key
    ON notifications(dedup_key)
    WHERE dedup_key <> '';

CREATE INDEX IF NOT EXISTS idx_notifications_event_user
    ON notifications(event_id, user_id)
    WHERE event_id <> '';

CREATE INDEX IF NOT EXISTS idx_notifications_user_category_created
    ON notifications(user_id, category, created_at DESC);
