//! PostgreSQL integration tests for Sales Channel attribution, immutability triggers,
//! phase-0b -> main+3b-3 migration upgrade fixes (`0005` & `0012`), and channel-filtered
//! ledger & reconciliation queries.
//!
//! Runs against an isolated database when `PAYMENT_TEST_DATABASE_URL` or `TEST_PAYMENT_DB_URL`
//! is set (and skips cleanly in offline environments where no PostgreSQL server is running).

use chrono::Utc;
use serde_json::json;
use sqlx::postgres::PgPoolOptions;
use sqlx::{Executor, PgPool, Row};
use uuid::Uuid;

use nexusmart_payment::config::Config;
use nexusmart_payment::domain::{
    vn_today, ChannelAttribution, CreatePaymentInput, CreateRefundInput, PaymentMethod,
    SalesChannel, ShopAllocationInput,
};
use nexusmart_payment::error::PaymentError;
use nexusmart_payment::ledger;
use nexusmart_payment::metrics::Metrics;
use nexusmart_payment::service::PaymentService;
use nexusmart_payment::webhook_security::IncomingWebhookPayload;

const MIGRATIONS_0004_TO_0017: &[(&str, &str)] = &[
    (
        "0004_payment_money_model.sql",
        include_str!("../migrations/0004_payment_money_model.sql"),
    ),
    (
        "0005_refund_settlement_and_reconciliation.sql",
        include_str!("../migrations/0005_refund_settlement_and_reconciliation.sql"),
    ),
    (
        "0006_reconciliation_queue_unique_tx.sql",
        include_str!("../migrations/0006_reconciliation_queue_unique_tx.sql"),
    ),
    (
        "0007_reconciliation_fixes.sql",
        include_str!("../migrations/0007_reconciliation_fixes.sql"),
    ),
    (
        "0008_trim_provider_tx_ids.sql",
        include_str!("../migrations/0008_trim_provider_tx_ids.sql"),
    ),
    (
        "0009_payment_fee_configs.sql",
        include_str!("../migrations/0009_payment_fee_configs.sql"),
    ),
    (
        "0010_outbox_lease_and_fee_config_index.sql",
        include_str!("../migrations/0010_outbox_lease_and_fee_config_index.sql"),
    ),
    (
        "0011_cod_reconciliation_and_dlq.sql",
        include_str!("../migrations/0011_cod_reconciliation_and_dlq.sql"),
    ),
    (
        "0012_zalopay_and_method_configs.sql",
        include_str!("../migrations/0012_zalopay_and_method_configs.sql"),
    ),
    (
        "0013_refund_two_person_approval.sql",
        include_str!("../migrations/0013_refund_two_person_approval.sql"),
    ),
    (
        "0014_zalopay_late_payment_sweep.sql",
        include_str!("../migrations/0014_zalopay_late_payment_sweep.sql"),
    ),
    (
        "0015_p4_refund_lease_and_audit.sql",
        include_str!("../migrations/0015_p4_refund_lease_and_audit.sql"),
    ),
    (
        "0016_zalopay_last_query_state.sql",
        include_str!("../migrations/0016_zalopay_last_query_state.sql"),
    ),
    (
        "0017_w3b3_cod_fee_adjustments.sql",
        include_str!("../migrations/0017_w3b3_cod_fee_adjustments.sql"),
    ),
];

fn pg_test_db_url() -> Option<String> {
    std::env::var("PAYMENT_TEST_DATABASE_URL")
        .or_else(|_| std::env::var("TEST_PAYMENT_DB_URL"))
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
}

async fn setup_empty_isolated_db(base_db_url: &str) -> (PgPool, PgPool, String, String) {
    let admin_pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(base_db_url)
        .await
        .expect("connect to base test Postgres");

    let db_name = format!("payment_sc_{}", &Uuid::new_v4().simple().to_string()[..12]);
    admin_pool
        .execute(format!("CREATE DATABASE {db_name}").as_str())
        .await
        .expect("create isolated test database");

    let (prefix, query) = match base_db_url.split_once('?') {
        Some((p, q)) => (p, Some(q)),
        None => (base_db_url, None),
    };
    let slash_idx = prefix.rfind('/').expect("valid postgres URL with db path");
    let isolated_url = match query {
        Some(q) => format!("{}/{db_name}?{q}", &prefix[..slash_idx]),
        None => format!("{}/{db_name}", &prefix[..slash_idx]),
    };

    let pool = PgPoolOptions::new()
        .max_connections(5)
        .connect(&isolated_url)
        .await
        .expect("connect to isolated test database");

    (admin_pool, pool, db_name, isolated_url)
}

async fn teardown_isolated_db(admin_pool: PgPool, pool: PgPool, db_name: &str) {
    pool.close().await;
    let _ = admin_pool
        .execute(format!("DROP DATABASE IF EXISTS {db_name} WITH (FORCE)").as_str())
        .await;
    admin_pool.close().await;
}

/// Non-DB invariant guard verifying the SQL fixes applied when rebasing main+3b-3 migrations
/// (`0004..0017`) on top of byte-identical phase-0b migrations (`0001..0003`).
#[test]
fn test_phase0b_to_main_migration_sql_fixes_invariants() {
    let m0001 = include_str!("../migrations/0001_init.sql");
    let m0002 = include_str!("../migrations/0002_sales_channel.sql");
    let m0003 = include_str!("../migrations/0003_zalopay.sql");
    assert!(!m0001.trim().is_empty());
    assert!(!m0002.trim().is_empty());
    assert!(!m0003.trim().is_empty());

    assert_eq!(MIGRATIONS_0004_TO_0017.len(), 14);
    for (name, sql) in MIGRATIONS_0004_TO_0017 {
        assert!(!sql.trim().is_empty(), "migration {name} must not be empty");
    }

    // 0005: must preserve already-disbursed phase-0b ZaloPay refunds (`provider_refund_id IS NOT NULL`)
    let m0005 = include_str!("../migrations/0005_refund_settlement_and_reconciliation.sql");
    assert!(
        m0005.contains("r.provider_refund_id IS NULL"),
        "0005 must guard downgrade UPDATE with r.provider_refund_id IS NULL so disbursed ZaloPay refunds stay completed"
    );

    // 0009: must be idempotent for tables/triggers/seeds
    let m0009 = include_str!("../migrations/0009_payment_fee_configs.sql");
    assert!(m0009.contains("CREATE TABLE IF NOT EXISTS payment_fee_configs"));
    assert!(m0009.contains("DROP TRIGGER IF EXISTS"));

    // 0012: must drop duplicate phase-0b CHECK (`chk_payments_method`) & index
    // (`idx_payments_pending_online_expiry`) AND backfill `pay_url` from `qr_url` for ZALOPAY rows.
    let m0012 = include_str!("../migrations/0012_zalopay_and_method_configs.sql");
    assert!(m0012.contains("DROP CONSTRAINT IF EXISTS chk_payments_method"));
    assert!(m0012.contains("DROP CONSTRAINT IF EXISTS payments_method_check"));
    assert!(m0012.contains("DROP INDEX IF EXISTS idx_payments_pending_online_expiry"));
    assert!(m0012.contains("DROP INDEX IF EXISTS idx_payments_pending_expiry"));
    assert!(m0012.contains("SET pay_url = qr_url"));
    assert!(m0012.contains("WHERE method = 'ZALOPAY'"));
}

/// Real PostgreSQL upgrade test from a populated phase-0b schema (`0001..0003`) through
/// `0004..0017`, specifically asserting:
/// 1. `0005` keeps `completed` ZaloPay refunds with `provider_refund_id IS NOT NULL` as `completed`
///    while downgrading legacy `completed` refunds with `provider_refund_id IS NULL` (no `refund_settle`
///    ledger tx) to `pending`.
/// 2. `0012` removes duplicate `chk_payments_method` & `idx_payments_pending_online_expiry` while
///    preserving `payments_method_check` & `idx_payments_pending_expiry`, AND backfills `pay_url`
///    from `qr_url` on legacy phase-0b `ZALOPAY` payments without tripping the immutability trigger.
#[tokio::test]
async fn test_pg_phase0b_upgrade_0005_zalopay_refund_and_0012_dedup_backfill() {
    let Some(base_db_url) = pg_test_db_url() else {
        eprintln!(
            "skipping test_pg_phase0b_upgrade_0005_zalopay_refund_and_0012_dedup_backfill: neither PAYMENT_TEST_DATABASE_URL nor TEST_PAYMENT_DB_URL set"
        );
        return;
    };

    let (admin_pool, pool, db_name, _isolated_url) = setup_empty_isolated_db(&base_db_url).await;

    // 1. Apply byte-identical phase-0b migrations 0001..0003
    for (name, sql) in [
        ("0001_init.sql", include_str!("../migrations/0001_init.sql")),
        (
            "0002_sales_channel.sql",
            include_str!("../migrations/0002_sales_channel.sql"),
        ),
        (
            "0003_zalopay.sql",
            include_str!("../migrations/0003_zalopay.sql"),
        ),
    ] {
        sqlx::raw_sql(sql)
            .execute(&pool)
            .await
            .unwrap_or_else(|e| panic!("apply {name}: {e}"));
    }

    // 2. Seed populated phase-0b ZaloPay payment (with order_url stored in qr_url) + 2 completed refunds
    let legacy_zp_pay_id = Uuid::new_v4();
    let legacy_zp_url = "https://sbgateway.zalopay.vn/openinapp?order=P0BLEGACY01";
    sqlx::query(
        r#"
        INSERT INTO payments (
            id, order_group_id, order_id, user_id, method, provider,
            amount_vnd, status, provider_tx_id, provider_order_code,
            qr_code, qr_url, paid_at, sales_channel, storefront_shop_id
        )
        VALUES (
            $1, 'grp-p0b-zp', 'ord-p0b-zp', 'buyer-p0b', 'ZALOPAY', 'zalopay_mock',
            250000, 'succeeded', '26112100008888', '261121_NMP0B001',
            'ZPTOKP0B', $2, NOW(), 'STOREFRONT', 'shop-p0b'
        )
        "#,
    )
    .bind(legacy_zp_pay_id)
    .bind(legacy_zp_url)
    .execute(&pool)
    .await
    .expect("seed phase-0b ZaloPay payment");

    // Disbursed phase-0b ZaloPay refund: status='completed', provider_refund_id IS NOT NULL, no refund_settle ledger tx
    let disbursed_zp_refund_id = Uuid::new_v4();
    sqlx::query(
        r#"
        INSERT INTO refunds (
            id, payment_id, order_id, amount_vnd, reason, status,
            idempotency_key, provider_refund_id
        )
        VALUES (
            $1, $2, 'ord-p0b-zp', 100000, 'p0b_zalopay_disbursed', 'completed',
            'idem-p0b-zp-disbursed', 'zpmock_ref_2554_already_paid'
        )
        "#,
    )
    .bind(disbursed_zp_refund_id)
    .bind(legacy_zp_pay_id)
    .execute(&pool)
    .await
    .expect("seed disbursed phase-0b ZaloPay refund");

    // Legacy un-disbursed refund: status='completed', provider_refund_id IS NULL, no refund_settle ledger tx
    let legacy_null_refund_id = Uuid::new_v4();
    sqlx::query(
        r#"
        INSERT INTO refunds (
            id, payment_id, order_id, amount_vnd, reason, status,
            idempotency_key, provider_refund_id
        )
        VALUES (
            $1, $2, 'ord-p0b-zp', 50000, 'legacy_unsettled', 'completed',
            'idem-p0b-legacy-null', NULL
        )
        "#,
    )
    .bind(legacy_null_refund_id)
    .bind(legacy_zp_pay_id)
    .execute(&pool)
    .await
    .expect("seed legacy null-provider_refund_id refund");

    // 3. Apply 0004..0017 on top of populated phase-0b DB
    for (name, sql) in MIGRATIONS_0004_TO_0017 {
        sqlx::raw_sql(sql)
            .execute(&pool)
            .await
            .unwrap_or_else(|e| panic!("apply {name} on populated phase-0b DB: {e}"));
    }

    // 4. Verify 0005 fix: disbursed ZaloPay refund stays 'completed'; null-provider refund downgraded to 'pending'
    let zp_refund_status: String = sqlx::query_scalar("SELECT status FROM refunds WHERE id = $1")
        .bind(disbursed_zp_refund_id)
        .fetch_one(&pool)
        .await
        .expect("query disbursed ZaloPay refund status");
    assert_eq!(
        zp_refund_status, "completed",
        "0005 MUST NOT downgrade already-disbursed ZaloPay refund (provider_refund_id IS NOT NULL) to pending"
    );

    let legacy_null_status: String = sqlx::query_scalar("SELECT status FROM refunds WHERE id = $1")
        .bind(legacy_null_refund_id)
        .fetch_one(&pool)
        .await
        .expect("query legacy null-provider refund status");
    assert_eq!(
        legacy_null_status, "pending",
        "0005 MUST still downgrade legacy completed refund without provider_refund_id and without refund_settle ledger tx"
    );

    // 5. Verify 0012 fix: duplicate CHECK and index removed; canonical CHECK & index present
    let dup_check_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM pg_constraint WHERE conname = 'chk_payments_method'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        dup_check_count, 0,
        "0012 must drop duplicate chk_payments_method constraint from 0003"
    );

    let canonical_check_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM pg_constraint WHERE conname = 'payments_method_check'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        canonical_check_count, 1,
        "0012 must keep canonical payments_method_check constraint"
    );

    let dup_idx_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'idx_payments_pending_online_expiry'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        dup_idx_count, 0,
        "0012 must drop duplicate idx_payments_pending_online_expiry index from 0003"
    );

    let canonical_idx_def: String = sqlx::query_scalar(
        "SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'idx_payments_pending_expiry'",
    )
    .fetch_one(&pool)
    .await
    .expect("0012 must create canonical idx_payments_pending_expiry");
    assert!(canonical_idx_def.contains("VIETQR") && canonical_idx_def.contains("ZALOPAY"));

    // 6. Verify 0012 backfill: pay_url backfilled from qr_url without mutating immutable sales_channel
    let (pay_url, sales_channel, storefront_shop_id): (Option<String>, String, String) =
        sqlx::query_as(
            "SELECT pay_url, sales_channel, storefront_shop_id FROM payments WHERE id = $1",
        )
        .bind(legacy_zp_pay_id)
        .fetch_one(&pool)
        .await
        .expect("read backfilled phase-0b ZaloPay payment");
    assert_eq!(pay_url.as_deref(), Some(legacy_zp_url));
    assert_eq!(sales_channel, "STOREFRONT");
    assert_eq!(storefront_shop_id, "shop-p0b");

    teardown_isolated_db(admin_pool, pool, &db_name).await;
}

#[tokio::test]
async fn test_pg_migration_immutability_and_channel_filtered_ledger_and_reconciliation() {
    let Some(base_db_url) = pg_test_db_url() else {
        eprintln!(
            "skipping test_pg_migration_immutability_and_channel_filtered_ledger_and_reconciliation: neither PAYMENT_TEST_DATABASE_URL nor TEST_PAYMENT_DB_URL set"
        );
        return;
    };

    let (admin_pool, pool, db_name, isolated_url) = setup_empty_isolated_db(&base_db_url).await;

    // 1. Apply 0001_init.sql and seed legacy pre-Phase-1 rows (before sales_channel existed)
    let init_sql = include_str!("../migrations/0001_init.sql");
    sqlx::raw_sql(init_sql)
        .execute(&pool)
        .await
        .expect("apply 0001_init.sql");

    let legacy_pay_id = Uuid::new_v4();
    sqlx::query(
        r#"
        INSERT INTO payments (
            id, order_group_id, order_id, user_id, method, provider,
            amount_vnd, status, provider_order_code, paid_at
        )
        VALUES ($1, 'grp-legacy', 'ord-legacy', 'buyer-legacy', 'COD', 'cod', 150000, 'succeeded', 'NMLEGACY0001', NOW())
        "#,
    )
    .bind(legacy_pay_id)
    .execute(&pool)
    .await
    .expect("insert legacy payment");

    let legacy_alloc_id = Uuid::new_v4();
    sqlx::query(
        r#"
        INSERT INTO payment_shop_allocations (
            id, payment_id, order_id, shop_id, subtotal_vnd, commission_vnd, shipping_fee_vnd
        )
        VALUES ($1, $2, 'ord-legacy', 'shop-legacy', 120000, 6000, 30000)
        "#,
    )
    .bind(legacy_alloc_id)
    .bind(legacy_pay_id)
    .execute(&pool)
    .await
    .expect("insert legacy allocation");

    // Insert a legacy balanced ledger transaction (trigger checked at COMMIT)
    let mut legacy_tx = pool.begin().await.expect("begin legacy ledger tx");
    let legacy_ltx_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO ledger_transactions (idempotency_key, reference_type, reference_id, description)
        VALUES ('legacy-cod-1', 'payment_cod', $1, 'Legacy pre-Phase-1 COD payment')
        RETURNING id
        "#,
    )
    .bind(legacy_pay_id.to_string())
    .fetch_one(&mut *legacy_tx)
    .await
    .expect("insert legacy ledger tx");

    sqlx::query(
        "INSERT INTO ledger_entries (transaction_id, account_id, debit_vnd, credit_vnd) VALUES ($1, 'cod_receivable', 150000, 0), ($1, 'escrow_liability', 0, 150000)",
    )
    .bind(legacy_ltx_id)
    .execute(&mut *legacy_tx)
    .await
    .expect("insert legacy ledger entries");
    legacy_tx.commit().await.expect("commit legacy ledger tx");

    // 2. Apply additive migrations 0002..0017 on top of populated tables
    sqlx::raw_sql(include_str!("../migrations/0002_sales_channel.sql"))
        .execute(&pool)
        .await
        .expect("apply 0002_sales_channel.sql");
    sqlx::raw_sql(include_str!("../migrations/0003_zalopay.sql"))
        .execute(&pool)
        .await
        .expect("apply 0003_zalopay.sql");
    for (name, sql) in MIGRATIONS_0004_TO_0017 {
        sqlx::raw_sql(sql)
            .execute(&pool)
            .await
            .unwrap_or_else(|e| panic!("apply {name}: {e}"));
    }

    // Verify legacy rows read back as MARKETPLACE / ""
    let mut cfg = Config::from_env();
    cfg.database_url = isolated_url;
    let svc = PaymentService::new(pool.clone(), cfg, Metrics::new());

    let legacy_pwa = svc
        .get_payment(Some(legacy_pay_id), None, None)
        .await
        .expect("read legacy payment");
    assert_eq!(legacy_pwa.payment.sales_channel, "MARKETPLACE");
    assert_eq!(legacy_pwa.payment.storefront_shop_id, "");
    assert_eq!(legacy_pwa.allocations.len(), 1);
    assert_eq!(legacy_pwa.allocations[0].sales_channel, "MARKETPLACE");
    assert_eq!(legacy_pwa.allocations[0].storefront_shop_id, "");

    let legacy_entry_meta: (String, String, serde_json::Value) = sqlx::query_as(
        "SELECT sales_channel, storefront_shop_id, metadata FROM ledger_entries WHERE transaction_id = $1 LIMIT 1",
    )
    .bind(legacy_ltx_id)
    .fetch_one(&pool)
    .await
    .expect("read legacy ledger entry");
    assert_eq!(legacy_entry_meta.0, "MARKETPLACE");
    assert_eq!(legacy_entry_meta.1, "");
    assert_eq!(
        legacy_entry_meta.2,
        json!({"sales_channel": "MARKETPLACE", "storefront_shop_id": ""})
    );

    // 3. Create MARKETPLACE & STOREFRONT payments (COD + VietQR) and verify immutability
    let mp_qr = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-mp-qr".into(),
            order_id: "grp-mp-qr".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 230_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some("sub-mp-1".into()),
                shop_id: "shop-A".into(),
                subtotal_vnd: 200_000,
                commission_vnd: 10_000,
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
                sales_channel: "MARKETPLACE".into(),
                storefront_shop_id: String::new(),
            }],
            expires_at: None,
            idempotency_key: Some("idem-mp-qr".into()),
            supersede_previous: false,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            trace_id: "trace-mp-qr".into(),
        })
        .await
        .expect("create MARKETPLACE VietQR payment");
    assert_eq!(mp_qr.payment.sales_channel, "MARKETPLACE");

    let sf_qr = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-sf-qr".into(),
            order_id: "grp-sf-qr".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 330_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some("sub-sf-1".into()),
                shop_id: "shop-SF".into(),
                subtotal_vnd: 300_000,
                commission_vnd: 15_000,
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
                sales_channel: "STOREFRONT".into(),
                storefront_shop_id: "shop-SF".into(),
            }],
            expires_at: None,
            idempotency_key: Some("idem-sf-qr".into()),
            supersede_previous: false,
            sales_channel: "STOREFRONT".into(),
            storefront_shop_id: "shop-SF".into(),
            trace_id: "trace-sf-qr".into(),
        })
        .await
        .expect("create STOREFRONT VietQR payment");
    assert_eq!(sf_qr.payment.sales_channel, "STOREFRONT");
    assert_eq!(sf_qr.payment.storefront_shop_id, "shop-SF");
    assert_eq!(sf_qr.allocations[0].sales_channel, "STOREFRONT");
    assert_eq!(sf_qr.allocations[0].storefront_shop_id, "shop-SF");

    // Service-level immutability: retrying pending STOREFRONT payment as MARKETPLACE MUST FAIL
    let mutate_retry_err = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-sf-qr".into(),
            order_id: "grp-sf-qr".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 330_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some("sub-sf-1".into()),
                shop_id: "shop-SF".into(),
                subtotal_vnd: 300_000,
                commission_vnd: 15_000,
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
                sales_channel: "MARKETPLACE".into(),
                storefront_shop_id: String::new(),
            }],
            expires_at: None,
            idempotency_key: Some("idem-sf-qr-mutate-attempt".into()),
            supersede_previous: true,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            trace_id: "trace-mutate".into(),
        })
        .await
        .unwrap_err();
    assert!(matches!(mutate_retry_err, PaymentError::Validation(_)));

    // DB-level immutability triggers: attempting to UPDATE sales_channel or storefront_shop_id MUST FAIL
    let err_upd_pay_ch = sqlx::query(
        "UPDATE payments SET sales_channel = 'MARKETPLACE', storefront_shop_id = '' WHERE id = $1",
    )
    .bind(sf_qr.payment.id)
    .execute(&pool)
    .await
    .unwrap_err();
    assert!(
        err_upd_pay_ch.to_string().contains("immutable"),
        "expected immutability trigger error, got: {err_upd_pay_ch}"
    );

    let err_upd_alloc_shop = sqlx::query(
        "UPDATE payment_shop_allocations SET storefront_shop_id = 'shop-other' WHERE id = $1",
    )
    .bind(sf_qr.allocations[0].id)
    .execute(&pool)
    .await
    .unwrap_err();
    assert!(
        err_upd_alloc_shop.to_string().contains("immutable"),
        "expected immutability trigger error, got: {err_upd_alloc_shop}"
    );

    let err_upd_ledger_tx =
        sqlx::query("UPDATE ledger_transactions SET sales_channel = 'STOREFRONT' WHERE id = $1")
            .bind(legacy_ltx_id)
            .execute(&pool)
            .await
            .unwrap_err();
    assert!(err_upd_ledger_tx.to_string().contains("append-only"));

    let err_upd_ledger_entry =
        sqlx::query("UPDATE ledger_entries SET metadata = '{}'::jsonb WHERE transaction_id = $1")
            .bind(legacy_ltx_id)
            .execute(&pool)
            .await
            .unwrap_err();
    assert!(err_upd_ledger_entry.to_string().contains("append-only"));

    // Confirm both VietQR payments via webhook (also populates mock provider transactions)
    for (pwa, tx_id) in [(&mp_qr, "BANK-TX-MP-1"), (&sf_qr, "BANK-TX-SF-1")] {
        let payload = IncomingWebhookPayload {
            provider_tx_id: tx_id.into(),
            payment_id: Some(pwa.payment.id.to_string()),
            order_group_id: Some(pwa.payment.order_group_id.clone()),
            order_id: Some(pwa.payment.order_id.clone()),
            provider_order_code: Some(pwa.payment.provider_order_code.clone()),
            reference: None,
            amount_vnd: pwa.payment.amount_vnd,
            status: "SUCCEEDED".into(),
            provider: Some("vietqr_mock".into()),
            occurred_at: Some(Utc::now().to_rfc3339()),
        };
        let raw = serde_json::to_value(&payload).unwrap();
        let res = svc
            .process_verified_webhook(payload, raw, "vietqr_mock", "trace-wh")
            .await
            .expect("confirm VietQR via webhook");
        assert_eq!(res.status, "processed");
    }

    // Create a STOREFRONT COD payment (R2: starts pending/awaiting_collection -> delivered -> completed -> release_escrow)
    let sf_cod = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-sf-cod".into(),
            order_id: "grp-sf-cod".into(),
            user_id: "buyer-2".into(),
            amount_vnd: 180_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some("sub-sf-cod".into()),
                shop_id: "shop-SF".into(),
                subtotal_vnd: 150_000,
                commission_vnd: 7_500,
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
                sales_channel: "STOREFRONT".into(),
                storefront_shop_id: "shop-SF".into(),
            }],
            expires_at: None,
            idempotency_key: Some("idem-sf-cod".into()),
            supersede_previous: false,
            sales_channel: "STOREFRONT".into(),
            storefront_shop_id: "shop-SF".into(),
            trace_id: "trace-sf-cod".into(),
        })
        .await
        .expect("create STOREFRONT COD payment");
    assert_eq!(sf_cod.payment.status, "pending");

    assert!(svc
        .process_order_delivered_event_with_shipping(
            "evt-del-sf-cod",
            "sub-sf-cod",
            None,
            None,
            None,
            "trace-del"
        )
        .await
        .expect("deliver STOREFRONT COD"));
    assert!(svc
        .process_order_completed_event("evt-comp-sf-cod", "sub-sf-cod", "trace-comp")
        .await
        .expect("complete STOREFRONT COD"));

    // Release escrow for STOREFRONT COD (normal escrow_status update succeeds despite immutability trigger!)
    let rel = svc
        .release_escrow("sub-sf-cod", Some("shop-SF"))
        .await
        .expect("release escrow on STOREFRONT COD");
    assert!(rel.released);
    assert_eq!(rel.released_amount_vnd, 142_500);

    // Partial refund on STOREFRONT VietQR -> must be recorded under STOREFRONT in ledger metadata
    let refund_out = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(sf_qr.payment.id),
            order_id: "sub-sf-1".into(),
            amount_vnd: 50_000,
            reason: "storefront refund attribution check".into(),
            idempotency_key: "refund-sf-1".into(),
            trace_id: "trace-refund-sf".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .expect("create STOREFRONT refund");

    let refund_ledger_row = sqlx::query(
        r#"
        SELECT sales_channel, storefront_shop_id, metadata
        FROM ledger_transactions
        WHERE reference_type = 'refund' AND reference_id = $1
        "#,
    )
    .bind(refund_out.refund.id.to_string())
    .fetch_one(&pool)
    .await
    .expect("read refund ledger tx");
    assert_eq!(
        refund_ledger_row
            .try_get::<String, _>("sales_channel")
            .unwrap(),
        "STOREFRONT"
    );
    assert_eq!(
        refund_ledger_row
            .try_get::<String, _>("storefront_shop_id")
            .unwrap(),
        "shop-SF"
    );
    assert_eq!(
        refund_ledger_row
            .try_get::<serde_json::Value, _>("metadata")
            .unwrap(),
        ChannelAttribution::storefront("shop-SF")
            .unwrap()
            .metadata_json()
    );

    // 4. Verify Ledger Summary channel filtering adds up to 100% of unfiltered & stays balanced
    let all_ledger = ledger::get_ledger_summary(&pool, None)
        .await
        .expect("unfiltered ledger summary");
    let mp_ledger = ledger::get_ledger_summary(&pool, Some(SalesChannel::Marketplace))
        .await
        .expect("MARKETPLACE ledger summary");
    let sf_ledger = ledger::get_ledger_summary(&pool, Some(SalesChannel::Storefront))
        .await
        .expect("STOREFRONT ledger summary");

    assert!(all_ledger.is_balanced);
    assert!(mp_ledger.is_balanced);
    assert!(sf_ledger.is_balanced);
    assert!(mp_ledger.total_debits_vnd > 0);
    assert!(sf_ledger.total_debits_vnd > 0);
    assert_eq!(
        mp_ledger.total_debits_vnd + sf_ledger.total_debits_vnd,
        all_ledger.total_debits_vnd
    );
    assert_eq!(
        mp_ledger.total_credits_vnd + sf_ledger.total_credits_vnd,
        all_ledger.total_credits_vnd
    );
    assert_eq!(
        mp_ledger.transaction_count + sf_ledger.transaction_count,
        all_ledger.transaction_count
    );
    assert_eq!(mp_ledger.accounts.len(), all_ledger.accounts.len());
    assert_eq!(sf_ledger.accounts.len(), all_ledger.accounts.len());
    for i in 0..all_ledger.accounts.len() {
        assert_eq!(
            mp_ledger.accounts[i].total_debits_vnd + sf_ledger.accounts[i].total_debits_vnd,
            all_ledger.accounts[i].total_debits_vnd
        );
        assert_eq!(
            mp_ledger.accounts[i].total_credits_vnd + sf_ledger.accounts[i].total_credits_vnd,
            all_ledger.accounts[i].total_credits_vnd
        );
        assert_eq!(
            mp_ledger.accounts[i].balance_vnd + sf_ledger.accounts[i].balance_vnd,
            all_ledger.accounts[i].balance_vnd
        );
    }

    // 5. Verify Reconciliation channel filtering adds up to 100% of unfiltered (0 false missing_local)
    let today = vn_today(Utc::now());
    let all_recon = svc
        .run_reconciliation(today, None)
        .await
        .expect("unfiltered reconciliation");
    let mp_recon = svc
        .run_reconciliation(today, Some(SalesChannel::Marketplace))
        .await
        .expect("MARKETPLACE reconciliation");
    let sf_recon = svc
        .run_reconciliation(today, Some(SalesChannel::Storefront))
        .await
        .expect("STOREFRONT reconciliation");

    assert_eq!(all_recon.run.sales_channel, "");
    assert_eq!(mp_recon.run.sales_channel, "MARKETPLACE");
    assert_eq!(sf_recon.run.sales_channel, "STOREFRONT");

    assert_eq!(all_recon.run.matched_count, 2);
    assert_eq!(mp_recon.run.matched_count, 1);
    assert_eq!(sf_recon.run.matched_count, 1);
    assert_eq!(mp_recon.run.missing_local_count, 0);
    assert_eq!(sf_recon.run.missing_local_count, 0);
    assert_eq!(all_recon.run.missing_local_count, 0);
    assert_eq!(
        mp_recon.run.matched_count + sf_recon.run.matched_count,
        all_recon.run.matched_count
    );
    assert_eq!(
        mp_recon.run.missing_provider_count + sf_recon.run.missing_provider_count,
        all_recon.run.missing_provider_count
    );
    assert_eq!(
        mp_recon.run.amount_mismatch_count + sf_recon.run.amount_mismatch_count,
        all_recon.run.amount_mismatch_count
    );

    let sum_local = |items: &[nexusmart_payment::domain::ReconciliationItemRecord]| -> i64 {
        items.iter().filter_map(|i| i.local_amount_vnd).sum()
    };
    let sum_prov = |items: &[nexusmart_payment::domain::ReconciliationItemRecord]| -> i64 {
        items.iter().filter_map(|i| i.provider_amount_vnd).sum()
    };
    assert_eq!(
        sum_local(&mp_recon.items) + sum_local(&sf_recon.items),
        sum_local(&all_recon.items)
    );
    assert_eq!(
        sum_prov(&mp_recon.items) + sum_prov(&sf_recon.items),
        sum_prov(&all_recon.items)
    );
    assert_eq!(mp_recon.items[0].sales_channel, "MARKETPLACE");
    assert_eq!(mp_recon.items[0].storefront_shop_id, "");
    assert_eq!(sf_recon.items[0].sales_channel, "STOREFRONT");
    assert_eq!(sf_recon.items[0].storefront_shop_id, "shop-SF");

    // 6. Verify embedded sqlx::migrate!("./migrations") applies cleanly from scratch
    pool.execute("DROP SCHEMA public CASCADE; CREATE SCHEMA public;")
        .await
        .expect("reset schema for sqlx::migrate");
    sqlx::migrate!("./migrations")
        .run(&pool)
        .await
        .expect("sqlx::migrate!(\"./migrations\") must apply cleanly");

    teardown_isolated_db(admin_pool, pool, &db_name).await;
}
