-- NexusMart Product Service initial schema

CREATE TABLE IF NOT EXISTS categories (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    parent_id TEXT REFERENCES categories(id) ON DELETE RESTRICT,
    commission_rate_bps INT NOT NULL DEFAULT 500 CHECK (commission_rate_bps >= 0 AND commission_rate_bps <= 10000),
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_categories_parent_id ON categories(parent_id);

CREATE TABLE IF NOT EXISTS products (
    id TEXT PRIMARY KEY,
    shop_id TEXT NOT NULL,
    category_id TEXT NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    images JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'HIDDEN', 'OUT_OF_STOCK')),
    rejection_reason TEXT NOT NULL DEFAULT '',
    min_price_vnd BIGINT NOT NULL DEFAULT 0,
    max_price_vnd BIGINT NOT NULL DEFAULT 0,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_products_shop_id ON products(shop_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_products_category_id ON products(category_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_products_status ON products(status) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS skus (
    id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    sku_code TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT 'Default',
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    price_vnd BIGINT NOT NULL CHECK (price_vnd > 0),
    stock INT NOT NULL DEFAULT 0 CHECK (stock >= 0),
    reserved_stock INT NOT NULL DEFAULT 0 CHECK (reserved_stock >= 0),
    version BIGINT NOT NULL DEFAULT 1,
    weight_grams INT NOT NULL DEFAULT 0 CHECK (weight_grams >= 0),
    image_url TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'OUT_OF_STOCK', 'INACTIVE')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (reserved_stock <= stock),
    UNIQUE (product_id, sku_code)
);

CREATE INDEX IF NOT EXISTS idx_skus_product_id ON skus(product_id);
CREATE INDEX IF NOT EXISTS idx_skus_sku_code ON skus(sku_code);

CREATE TABLE IF NOT EXISTS stock_reservations (
    id TEXT PRIMARY KEY,
    reservation_group_id TEXT NOT NULL,
    order_id TEXT NOT NULL,
    sku_id TEXT NOT NULL REFERENCES skus(id) ON DELETE RESTRICT,
    quantity INT NOT NULL CHECK (quantity > 0),
    status TEXT NOT NULL DEFAULT 'RESERVED' CHECK (status IN ('RESERVED', 'CONFIRMED', 'RELEASED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (order_id, sku_id)
);

CREATE INDEX IF NOT EXISTS idx_stock_reservations_order_id ON stock_reservations(order_id);
CREATE INDEX IF NOT EXISTS idx_stock_reservations_sku_id ON stock_reservations(sku_id);

CREATE TABLE IF NOT EXISTS stock_history (
    id TEXT PRIMARY KEY,
    sku_id TEXT NOT NULL REFERENCES skus(id) ON DELETE CASCADE,
    order_id TEXT NOT NULL DEFAULT '',
    change_type TEXT NOT NULL,
    delta INT NOT NULL,
    stock_before INT NOT NULL,
    stock_after INT NOT NULL,
    reserved_before INT NOT NULL,
    reserved_after INT NOT NULL,
    actor_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_stock_history_sku_id ON stock_history(sku_id);
CREATE INDEX IF NOT EXISTS idx_stock_history_order_id ON stock_history(order_id) WHERE order_id <> '';
