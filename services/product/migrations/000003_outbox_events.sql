-- NexusMart Product Service: Transactional Outbox & Monotonic Event Version

CREATE SEQUENCE IF NOT EXISTS product_event_version_seq;

CREATE TABLE IF NOT EXISTS outbox_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    trace_id TEXT NOT NULL DEFAULT '',
    traceparent TEXT NOT NULL DEFAULT '',
    payload_json JSONB NOT NULL,
    published_at TIMESTAMPTZ,
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_product_outbox_unpublished
    ON outbox_events (created_at ASC)
    WHERE published_at IS NULL;
