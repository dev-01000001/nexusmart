-- Order Service Schema (order_db)

CREATE TABLE IF NOT EXISTS checkout_groups (
    id TEXT PRIMARY KEY,
    buyer_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL,
    payment_method TEXT NOT NULL,
    payment_id TEXT NOT NULL DEFAULT '',
    payment_status TEXT NOT NULL DEFAULT '',
    qr_url TEXT NOT NULL DEFAULT '',
    qr_code TEXT NOT NULL DEFAULT '',
    payment_expires_at TIMESTAMPTZ,
    total_payable_vnd BIGINT NOT NULL DEFAULT 0,
    response_snapshot_json JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Enforce strict idempotency per (buyer_id, idempotency_key) for active/completed checkouts,
-- while preserving cancelled/failed attempts for auditability.
CREATE UNIQUE INDEX IF NOT EXISTS idx_checkout_groups_buyer_idem_active
    ON checkout_groups (buyer_id, idempotency_key)
    WHERE status <> 'FAILED';

CREATE INDEX IF NOT EXISTS idx_checkout_groups_buyer_id
    ON checkout_groups (buyer_id, created_at DESC);

CREATE TABLE IF NOT EXISTS orders (
    id TEXT PRIMARY KEY,
    order_code TEXT NOT NULL UNIQUE,
    checkout_group_id TEXT NOT NULL REFERENCES checkout_groups(id),
    buyer_id TEXT NOT NULL,
    shop_id TEXT NOT NULL,
    status TEXT NOT NULL,
    payment_method TEXT NOT NULL,
    payment_id TEXT NOT NULL DEFAULT '',
    payment_status TEXT NOT NULL DEFAULT '',
    subtotal_vnd BIGINT NOT NULL DEFAULT 0,
    flash_sale_discount_vnd BIGINT NOT NULL DEFAULT 0,
    shop_voucher_discount_vnd BIGINT NOT NULL DEFAULT 0,
    platform_voucher_discount_vnd BIGINT NOT NULL DEFAULT 0,
    freeship_discount_vnd BIGINT NOT NULL DEFAULT 0,
    coins_discount_vnd BIGINT NOT NULL DEFAULT 0,
    shipping_fee_vnd BIGINT NOT NULL DEFAULT 0,
    commission_vnd BIGINT NOT NULL DEFAULT 0,
    total_payable_vnd BIGINT NOT NULL DEFAULT 0,
    carrier_code TEXT NOT NULL DEFAULT '',
    service_code TEXT NOT NULL DEFAULT '',
    tracking_code TEXT NOT NULL DEFAULT '',
    label_url TEXT NOT NULL DEFAULT '',
    cancel_reason TEXT NOT NULL DEFAULT '',
    shipping_address_json JSONB NOT NULL,
    payment_expires_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_checkout_group_id ON orders (checkout_group_id);
CREATE INDEX IF NOT EXISTS idx_orders_buyer_id ON orders (buyer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_shop_id ON orders (shop_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_pending_expiry
    ON orders (payment_expires_at)
    WHERE status = 'PENDING' AND payment_method = 'VIETQR';

CREATE TABLE IF NOT EXISTS order_items (
    id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    sku_id TEXT NOT NULL,
    product_id TEXT NOT NULL,
    product_name TEXT NOT NULL,
    sku_name TEXT NOT NULL,
    unit_price_vnd BIGINT NOT NULL,
    effective_unit_price_vnd BIGINT NOT NULL,
    quantity INT NOT NULL,
    line_total_vnd BIGINT NOT NULL,
    weight_grams INT NOT NULL DEFAULT 0,
    flash_sale_item_id TEXT NOT NULL DEFAULT '',
    commission_rate_bps INT NOT NULL DEFAULT 500
);

CREATE INDEX IF NOT EXISTS idx_order_items_order_id ON order_items (order_id);

CREATE TABLE IF NOT EXISTS order_timeline_events (
    id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    from_status TEXT NOT NULL,
    to_status TEXT NOT NULL,
    actor_id TEXT NOT NULL DEFAULT '',
    actor_role TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_order_timeline_order_id
    ON order_timeline_events (order_id, created_at ASC);

CREATE TABLE IF NOT EXISTS return_requests (
    id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    buyer_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    evidence_urls_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'PENDING',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_return_requests_order_id ON return_requests (order_id);

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

CREATE INDEX IF NOT EXISTS idx_outbox_events_unpublished
    ON outbox_events (created_at ASC)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS processed_events (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL DEFAULT '',
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dead_letter_events (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    event_id TEXT NOT NULL DEFAULT '',
    payload BYTEA NOT NULL,
    error_message TEXT NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
