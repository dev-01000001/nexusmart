//! PostgreSQL integration tests for Phase 1 Sales Channel attribution, immutability triggers,
//! backward-compatible migration, and channel-filtered ledger & reconciliation queries.
//!
//! Runs against a dedicated database when `PAYMENT_TEST_DATABASE_URL` is set (and skips cleanly
//! in offline/CI environments where no PostgreSQL server is running).

use chrono::Utc;
use serde_json::json;
use sqlx::postgres::PgPoolOptions;
use sqlx::{Executor, Row};
use uuid::Uuid;

use nexusmart_payment::config::Config;
use nexusmart_payment::domain::{
    ChannelAttribution, CreatePaymentInput, CreateRefundInput, PaymentMethod, SalesChannel,
    ShopAllocationInput,
};
use nexusmart_payment::error::PaymentError;
use nexusmart_payment::ledger;
use nexusmart_payment::metrics::Metrics;
use nexusmart_payment::service::PaymentService;
use nexusmart_payment::webhook_security::IncomingWebhookPayload;

#[tokio::test]
async fn test_pg_migration_immutability_and_channel_filtered_ledger_and_reconciliation() {
    let Ok(db_url) = std::env::var("PAYMENT_TEST_DATABASE_URL") else {
        eprintln!(
            "skipping PG integration test (set PAYMENT_TEST_DATABASE_URL to run against Postgres)"
        );
        return;
    };

    let pool = PgPoolOptions::new()
        .max_connections(5)
        .connect(&db_url)
        .await
        .expect("connect to test Postgres");

    // Start from a clean schema to test backward-compatible migration from 0001 -> 0002
    pool.execute("DROP SCHEMA public CASCADE; CREATE SCHEMA public;")
        .await
        .expect("reset public schema");

    // 1. Apply 0001_init.sql and seed legacy pre-Phase-1 rows (before sales_channel existed)
    let init_sql = include_str!("../migrations/0001_init.sql");
    pool.execute(init_sql).await.expect("apply 0001_init.sql");

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

    // 2. Apply additive migration 0002_sales_channel.sql on top of populated tables
    let mig2_sql = include_str!("../migrations/0002_sales_channel.sql");
    pool.execute(mig2_sql)
        .await
        .expect("apply 0002_sales_channel.sql");

    // Verify legacy rows read back as MARKETPLACE / ""
    let mut cfg = Config::from_env();
    cfg.database_url = db_url;
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

    // Create a STOREFRONT COD payment, release its escrow, and issue a partial refund on STOREFRONT VietQR
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
    assert_eq!(sf_cod.payment.status, "succeeded");

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
    let today = Utc::now().date_naive();
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
}
