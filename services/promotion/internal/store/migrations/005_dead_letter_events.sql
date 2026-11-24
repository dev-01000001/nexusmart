-- 005_dead_letter_events.sql:
-- Wave 3b-1 (E2): Persist dead-lettered JetStream messages in promotion_db
-- alongside NEXUSMART_DLQ. Matches order_db dead_letter_events columns exactly.

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
