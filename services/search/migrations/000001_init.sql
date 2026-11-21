CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS product_documents (
    product_id TEXT PRIMARY KEY,
    shop_id TEXT NOT NULL,
    category_id TEXT NOT NULL,
    category_slug TEXT NOT NULL DEFAULT '',
    category_name TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    images JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'APPROVED',
    min_price_vnd BIGINT NOT NULL DEFAULT 0,
    max_price_vnd BIGINT NOT NULL DEFAULT 0,
    total_stock INT NOT NULL DEFAULT 0,
    in_stock BOOLEAN NOT NULL DEFAULT FALSE,
    version BIGINT NOT NULL DEFAULT 0,
    embedding vector(256) NOT NULL,
    product_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    product_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    indexed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_product_documents_shop_id ON product_documents (shop_id);
CREATE INDEX IF NOT EXISTS idx_product_documents_category_slug ON product_documents (category_slug);
CREATE INDEX IF NOT EXISTS idx_product_documents_category_id ON product_documents (category_id);
CREATE INDEX IF NOT EXISTS idx_product_documents_min_price_vnd ON product_documents (min_price_vnd);
CREATE INDEX IF NOT EXISTS idx_product_documents_embedding_hnsw
    ON product_documents USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

CREATE TABLE IF NOT EXISTS processed_events (
    event_id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dead_letter_events (
    id UUID PRIMARY KEY,
    event_id TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL,
    payload BYTEA NOT NULL,
    error_message TEXT NOT NULL,
    attempts INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
