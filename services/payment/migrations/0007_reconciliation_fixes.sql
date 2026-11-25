-- Wave 2a independent review fixes (P1-3, P3-1):
-- 1. Persist provider transfer timestamp on reconciliation_queue so ASSIGN records real
--    occurred_at into payments.paid_at even when queued from reconciliation/expiry feeds.
-- 2. Index webhook_events(received_at) for bounded VN-day mock reconciliation queries.

ALTER TABLE reconciliation_queue
    ADD COLUMN IF NOT EXISTS occurred_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_webhook_events_received_at
    ON webhook_events (received_at);
