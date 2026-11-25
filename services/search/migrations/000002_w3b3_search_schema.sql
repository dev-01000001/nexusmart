CREATE EXTENSION IF NOT EXISTS vector;

-- Reconcile tables already created by 000001_init.sql (phase-0b) with the 3b-3 search schema.
-- Runs cleanly on both an empty database (after 000001_init.sql) and a live phase-0b database.
CREATE TABLE IF NOT EXISTS processed_events (
    event_id     TEXT PRIMARY KEY,
    subject      TEXT NOT NULL DEFAULT '',
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE processed_events
    ALTER COLUMN subject SET DEFAULT '';

CREATE TABLE IF NOT EXISTS dead_letter_events (
    id            TEXT PRIMARY KEY,
    subject       TEXT NOT NULL,
    event_id      TEXT NOT NULL DEFAULT '',
    payload       BYTEA NOT NULL,
    error_message TEXT NOT NULL,
    attempts      INT NOT NULL DEFAULT 1,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE dead_letter_events
    ALTER COLUMN id TYPE TEXT USING id::text;

ALTER TABLE dead_letter_events
    ALTER COLUMN attempts SET DEFAULT 1;

CREATE INDEX IF NOT EXISTS idx_dead_letter_events_created_at
    ON dead_letter_events (created_at DESC);

CREATE TABLE IF NOT EXISTS search_categories (
    category_id       TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    name_folded       TEXT NOT NULL DEFAULT '',
    slug              TEXT NOT NULL,
    parent_id         TEXT NOT NULL DEFAULT '',
    sort_order        INT NOT NULL DEFAULT 0,
    source_updated_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01T00:00:00Z',
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_search_categories_slug
    ON search_categories (slug);

CREATE INDEX IF NOT EXISTS idx_search_categories_parent
    ON search_categories (parent_id);

CREATE TABLE IF NOT EXISTS search_shops (
    shop_id        TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    name_folded    TEXT NOT NULL DEFAULT '',
    slug           TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'ACTIVE',
    status_version BIGINT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_search_shops_status
    ON search_shops (status);

CREATE INDEX IF NOT EXISTS idx_search_shops_slug
    ON search_shops (slug);

CREATE TABLE IF NOT EXISTS product_embeddings (
    product_id            TEXT PRIMARY KEY,
    shop_id               TEXT NOT NULL DEFAULT '',
    category_id           TEXT NOT NULL DEFAULT '',
    category_ancestor_ids TEXT[] NOT NULL DEFAULT '{}',
    name                  TEXT NOT NULL DEFAULT '',
    name_folded           TEXT NOT NULL DEFAULT '',
    slug                  TEXT NOT NULL DEFAULT '',
    description           TEXT NOT NULL DEFAULT '',
    thumbnail_url         TEXT NOT NULL DEFAULT '',
    min_price_vnd         BIGINT NOT NULL DEFAULT 0 CHECK (min_price_vnd >= 0),
    max_price_vnd         BIGINT NOT NULL DEFAULT 0 CHECK (max_price_vnd >= 0),
    status                TEXT NOT NULL DEFAULT 'APPROVED',
    rating_avg_x100       INT NOT NULL DEFAULT 0 CHECK (rating_avg_x100 >= 0 AND rating_avg_x100 <= 500),
    review_count          INT NOT NULL DEFAULT 0 CHECK (review_count >= 0),
    in_stock              BOOL NOT NULL DEFAULT FALSE,
    sku_count             INT NOT NULL DEFAULT 0,
    sku_codes             TEXT[] NOT NULL DEFAULT '{}',
    sku_attributes_text   TEXT NOT NULL DEFAULT '',
    sku_ids               TEXT[] NOT NULL DEFAULT '{}',
    default_sku_id        TEXT NOT NULL DEFAULT '',
    colors                TEXT[] NOT NULL DEFAULT '{}',
    colors_folded         TEXT[] NOT NULL DEFAULT '{}',
    color_codes           TEXT[] NOT NULL DEFAULT '{}',
    sizes                 TEXT[] NOT NULL DEFAULT '{}',
    sizes_folded          TEXT[] NOT NULL DEFAULT '{}',
    deleted               BOOL NOT NULL DEFAULT FALSE,
    text_hash             TEXT NOT NULL DEFAULT '',
    embedding             vector(1536),
    source_updated_at     TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01T00:00:00Z',
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_product_embeddings_hnsw
    ON product_embeddings
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX IF NOT EXISTS idx_product_embeddings_category_ancestors
    ON product_embeddings
    USING GIN (category_ancestor_ids);

CREATE INDEX IF NOT EXISTS idx_product_embeddings_public_price
    ON product_embeddings (status, min_price_vnd)
    WHERE deleted = FALSE;

CREATE INDEX IF NOT EXISTS idx_product_embeddings_shop_id
    ON product_embeddings (shop_id)
    WHERE deleted = FALSE;

CREATE TABLE IF NOT EXISTS search_history (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL,
    query        TEXT NOT NULL,
    query_folded TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, query_folded)
);

CREATE INDEX IF NOT EXISTS idx_search_history_user_created
    ON search_history (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS reindex_jobs (
    job_id             TEXT PRIMARY KEY,
    status             TEXT NOT NULL,
    products_indexed   BIGINT NOT NULL DEFAULT 0,
    embeddings_updated BIGINT NOT NULL DEFAULT 0,
    last_cursor        TEXT NOT NULL DEFAULT '',
    error_message      TEXT NOT NULL DEFAULT '',
    started_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at        TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_reindex_jobs_started_at
    ON reindex_jobs (started_at DESC);
