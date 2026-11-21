-- 0003_domains_cloudflare.sql: Extend domains for Cloudflare DNS & Custom Hostnames (Phase 5)

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'ACTIVE'
    CHECK (status IN ('PENDING', 'ACTIVE', 'FAILED', 'REMOVING'));

ALTER TABLE domains ADD COLUMN IF NOT EXISTS cf_record_id TEXT NULL;
ALTER TABLE domains ADD COLUMN IF NOT EXISTS cf_custom_hostname_id TEXT NULL;
ALTER TABLE domains ADD COLUMN IF NOT EXISTS verification JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE domains ADD COLUMN IF NOT EXISTS last_checked_at TIMESTAMPTZ NULL;
ALTER TABLE domains ADD COLUMN IF NOT EXISTS last_error TEXT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_domains_site_custom
    ON domains (site_id) WHERE kind = 'CUSTOM';

CREATE UNIQUE INDEX IF NOT EXISTS idx_domains_site_primary
    ON domains (site_id) WHERE is_primary = TRUE;

CREATE INDEX IF NOT EXISTS idx_domains_poll_status
    ON domains (status, created_at) WHERE status IN ('PENDING', 'REMOVING');
