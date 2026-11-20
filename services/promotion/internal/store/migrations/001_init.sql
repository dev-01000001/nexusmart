-- 001_init.sql: NexusMart Promotion Service Schema (promotion_db)

CREATE TABLE IF NOT EXISTS vouchers (
    id                TEXT PRIMARY KEY,
    code              TEXT NOT NULL UNIQUE,
    name              TEXT NOT NULL,
    scope             TEXT NOT NULL CHECK (scope IN ('PLATFORM', 'SHOP', 'FREESHIP')),
    shop_id           TEXT NOT NULL DEFAULT '',
    discount_type     TEXT NOT NULL CHECK (discount_type IN ('FIXED', 'PERCENT')),
    value_vnd         BIGINT NOT NULL DEFAULT 0 CHECK (value_vnd >= 0),
    discount_percent  INTEGER NOT NULL DEFAULT 0 CHECK (discount_percent >= 0 AND discount_percent <= 100),
    max_discount_vnd  BIGINT NOT NULL DEFAULT 0 CHECK (max_discount_vnd >= 0),
    min_order_vnd     BIGINT NOT NULL DEFAULT 0 CHECK (min_order_vnd >= 0),
    quota             INTEGER NOT NULL CHECK (quota > 0),
    used_count        INTEGER NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    per_user_limit    INTEGER NOT NULL DEFAULT 1 CHECK (per_user_limit > 0),
    budget_vnd        BIGINT NOT NULL DEFAULT 0 CHECK (budget_vnd >= 0),
    used_budget_vnd   BIGINT NOT NULL DEFAULT 0 CHECK (used_budget_vnd >= 0),
    starts_at         TIMESTAMPTZ NOT NULL,
    ends_at           TIMESTAMPTZ NOT NULL,
    is_active         BOOLEAN NOT NULL DEFAULT TRUE,
    created_by        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (ends_at > starts_at)
);

CREATE INDEX IF NOT EXISTS idx_vouchers_scope_shop ON vouchers (scope, shop_id);
CREATE INDEX IF NOT EXISTS idx_vouchers_active_window ON vouchers (is_active, starts_at, ends_at);

CREATE TABLE IF NOT EXISTS user_vouchers (
    user_id     TEXT NOT NULL,
    voucher_id  TEXT NOT NULL REFERENCES vouchers(id) ON DELETE CASCADE,
    saved_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, voucher_id)
);

CREATE INDEX IF NOT EXISTS idx_user_vouchers_user ON user_vouchers (user_id);

CREATE TABLE IF NOT EXISTS flash_sales (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    starts_at   TIMESTAMPTZ NOT NULL,
    ends_at     TIMESTAMPTZ NOT NULL,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (ends_at > starts_at)
);

CREATE INDEX IF NOT EXISTS idx_flash_sales_window ON flash_sales (starts_at, ends_at);

CREATE TABLE IF NOT EXISTS flash_sale_items (
    id                TEXT PRIMARY KEY,
    flash_sale_id     TEXT NOT NULL REFERENCES flash_sales(id) ON DELETE CASCADE,
    shop_id           TEXT NOT NULL,
    sku_id            TEXT NOT NULL,
    flash_price_vnd   BIGINT NOT NULL CHECK (flash_price_vnd > 0),
    total_slots       INTEGER NOT NULL CHECK (total_slots > 0),
    remaining_slots   INTEGER NOT NULL CHECK (remaining_slots >= 0),
    per_user_limit    INTEGER NOT NULL DEFAULT 1 CHECK (per_user_limit > 0),
    status            TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
    rejection_reason  TEXT NOT NULL DEFAULT '',
    approved_by       TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_flash_sale_items_sale ON flash_sale_items (flash_sale_id);
CREATE INDEX IF NOT EXISTS idx_flash_sale_items_sku_status ON flash_sale_items (sku_id, status);

CREATE TABLE IF NOT EXISTS coin_accounts (
    user_id     TEXT PRIMARY KEY,
    balance     BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS coin_transactions (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES coin_accounts(user_id),
    delta         BIGINT NOT NULL CHECK (delta <> 0),
    reason        TEXT NOT NULL,
    reference_id  TEXT NOT NULL UNIQUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_coin_tx_user_created ON coin_transactions (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS promotion_reservations (
    order_id               TEXT PRIMARY KEY,
    user_id                TEXT NOT NULL,
    status                 TEXT NOT NULL CHECK (status IN ('RESERVED', 'RELEASED')),
    coins_used             BIGINT NOT NULL DEFAULT 0 CHECK (coins_used >= 0),
    vouchers_json          JSONB NOT NULL DEFAULT '[]'::jsonb,
    flash_sale_items_json  JSONB NOT NULL DEFAULT '[]'::jsonb,
    breakdown_json         JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    released_at            TIMESTAMPTZ,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_promo_res_user ON promotion_reservations (user_id);

CREATE TABLE IF NOT EXISTS processed_events (
    event_id      TEXT PRIMARY KEY,
    subject       TEXT NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
