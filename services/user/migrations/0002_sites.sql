-- 0002_sites.sql: Storefront subdomain sites, immutable published versions, and domain mappings

CREATE TABLE IF NOT EXISTS sites (
    id UUID PRIMARY KEY,
    shop_id UUID NOT NULL UNIQUE REFERENCES shops(id),
    subdomain TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'PUBLISHED', 'SUSPENDED')),
    draft_config JSONB NOT NULL,
    published_version_id UUID NULL,
    published_at TIMESTAMPTZ NULL,
    suspended_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_sites_subdomain_lower ON sites (LOWER(subdomain));
CREATE INDEX IF NOT EXISTS idx_sites_status ON sites (status);

CREATE TABLE IF NOT EXISTS site_versions (
    id UUID PRIMARY KEY,
    site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    version_number INT NOT NULL,
    config JSONB NOT NULL,
    published_by UUID NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (site_id, version_number)
);

CREATE INDEX IF NOT EXISTS idx_site_versions_site_version ON site_versions (site_id, version_number DESC);

-- Enforce strict immutability on published site_versions snapshots.
CREATE OR REPLACE FUNCTION prevent_site_version_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'site_versions is immutable: % is forbidden', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_site_versions_immutable ON site_versions;
CREATE TRIGGER trg_site_versions_immutable
    BEFORE UPDATE OR DELETE ON site_versions
    FOR EACH ROW EXECUTE FUNCTION prevent_site_version_mutation();

ALTER TABLE sites
    ADD CONSTRAINT fk_sites_published_version
    FOREIGN KEY (published_version_id) REFERENCES site_versions(id);

CREATE TABLE IF NOT EXISTS domains (
    id UUID PRIMARY KEY,
    site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    host TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'SUBDOMAIN' CHECK (kind IN ('SUBDOMAIN', 'CUSTOM')),
    is_primary BOOLEAN NOT NULL DEFAULT TRUE,
    verified_at TIMESTAMPTZ NULL,
    tls_status TEXT NOT NULL DEFAULT 'MANAGED',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_domains_host_lower ON domains (LOWER(host));
CREATE UNIQUE INDEX IF NOT EXISTS idx_domains_site_subdomain ON domains (site_id) WHERE kind = 'SUBDOMAIN';
CREATE INDEX IF NOT EXISTS idx_domains_site_id ON domains (site_id);
