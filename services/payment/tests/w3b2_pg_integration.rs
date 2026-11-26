use std::sync::Arc;
use std::time::Duration;

use axum::body::Body;
use axum::http::{Method, Request, StatusCode};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use chrono::{Duration as ChronoDuration, Utc};
use hmac::{Hmac, Mac};
use http_body_util::BodyExt;
use serde_json::{json, Value};
use sha2::Sha256;
use sqlx::postgres::PgPoolOptions;
use sqlx::{Executor, PgPool};
use tokio::sync::{Barrier, OnceCell, Semaphore};
use tonic::metadata::MetadataValue;
use tower::ServiceExt;
use uuid::Uuid;

use nexusmart_payment::auth::GrpcAuth;
use nexusmart_payment::config::{
    Config, VietQrMode, DEFAULT_ZALOPAY_MOCK_APP_ID, DEV_ONLY_ZALOPAY_MOCK_KEY2,
};
use nexusmart_payment::domain::{
    CreatePaymentInput, CreateRefundInput, DecideRefundApprovalInput, PaymentMethod,
    SettleRefundInput, ShopAllocationInput, UpdatePaymentMethodConfigInput,
};
use nexusmart_payment::error::PaymentError;
use nexusmart_payment::grpc::pb::payment_service_server::PaymentService as PaymentServiceGrpcTrait;
use nexusmart_payment::grpc::{pb, PaymentGrpcServer};
use nexusmart_payment::http::build_router;
use nexusmart_payment::metrics::Metrics;
use nexusmart_payment::service::{PaymentService, ZaloPayCallbackOutcome};
use nexusmart_payment::vietqr::CircuitBreaker;
use nexusmart_payment::webhook_security::IncomingWebhookPayload;
use nexusmart_payment::zalopay::{MockBehavior, MockZaloPay, ZaloPayAdapter, ZaloPayCallbackData};

const TEST_USER_SECRET: &str = "w3b2-pg-user-jwt-secret";
const TEST_SERVICE_SECRET: &str = "w3b2-pg-service-jwt-secret";
const TEST_WEBHOOK_SECRET: &str = "w3b2-pg-webhook-secret";

static EXT_INIT: OnceCell<()> = OnceCell::const_new();
static SCHEMA_SEM: Semaphore = Semaphore::const_new(6);

struct TestDb {
    pool: PgPool,
    admin_url: String,
    schema: String,
    _permit: tokio::sync::SemaphorePermit<'static>,
}

impl TestDb {
    async fn setup(test_name: &str, max_conns: u32) -> Option<Self> {
        let db_url = match std::env::var("TEST_PAYMENT_DB_URL") {
            Ok(v) if !v.trim().is_empty() => v,
            _ => {
                eprintln!("skipping {test_name}: TEST_PAYMENT_DB_URL not set");
                return None;
            }
        };

        let permit = SCHEMA_SEM.acquire().await.expect("schema semaphore");

        EXT_INIT
            .get_or_init(|| async {
                let base = PgPoolOptions::new()
                    .max_connections(2)
                    .acquire_timeout(Duration::from_secs(10))
                    .connect(&db_url)
                    .await
                    .expect("connect base TEST_PAYMENT_DB_URL");
                let mut conn = base.acquire().await.expect("acquire base conn");
                sqlx::query("SELECT pg_advisory_lock(88442299)")
                    .execute(&mut *conn)
                    .await
                    .expect("advisory lock");
                sqlx::query("CREATE EXTENSION IF NOT EXISTS pgcrypto")
                    .execute(&mut *conn)
                    .await
                    .expect("create pgcrypto");
                sqlx::query("SELECT pg_advisory_unlock(88442299)")
                    .execute(&mut *conn)
                    .await
                    .expect("advisory unlock");
                drop(conn);
                base.close().await;
            })
            .await;

        let schema = format!("payw3b2_{}", Uuid::new_v4().simple());
        let base = PgPoolOptions::new()
            .max_connections(2)
            .acquire_timeout(Duration::from_secs(10))
            .connect(&db_url)
            .await
            .expect("connect admin pool");
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&base)
            .await
            .expect("create isolated test schema");
        base.close().await;

        let schema_for_hook = schema.clone();
        let pool = PgPoolOptions::new()
            .max_connections(max_conns)
            .acquire_timeout(Duration::from_secs(15))
            .after_connect(move |conn, _meta| {
                let s = schema_for_hook.clone();
                Box::pin(async move {
                    conn.execute(format!("SET search_path TO {s}, public").as_str())
                        .await?;
                    Ok(())
                })
            })
            .connect(&db_url)
            .await
            .expect("connect isolated schema pool");

        sqlx::migrate!("./migrations")
            .run(&pool)
            .await
            .expect("run migrations in isolated schema");

        Some(Self {
            pool,
            admin_url: db_url,
            schema,
            _permit: permit,
        })
    }

    fn service_with_mock(&self) -> (PaymentService, MockZaloPay) {
        self.service_with_mock_breaker(None)
    }

    fn service_with_mock_breaker(
        &self,
        breaker: Option<CircuitBreaker>,
    ) -> (PaymentService, MockZaloPay) {
        let mut cfg = Config::from_env();
        cfg.database_url = self.admin_url.clone();
        cfg.webhook_secret = TEST_WEBHOOK_SECRET.to_string();
        cfg.jwt_secret = TEST_USER_SECRET.to_string();
        cfg.service_jwt_secret = TEST_SERVICE_SECRET.to_string();
        cfg.vietqr_mode = VietQrMode::Mock;
        cfg.seller_hold_hours = 72;
        cfg.large_refund_approval_vnd = 5_000_000;
        let mock = MockZaloPay::new();
        let mut zp = ZaloPayAdapter::new_mock(mock.clone());
        if let Some(cb) = breaker {
            zp = zp.with_circuit_breaker(cb);
        }
        let svc = PaymentService::new(self.pool.clone(), cfg, Metrics::new()).with_zalopay(zp);
        (svc, mock)
    }

    fn auth(&self) -> GrpcAuth {
        GrpcAuth::new(TEST_USER_SECRET, TEST_SERVICE_SECRET).expect("test GrpcAuth")
    }

    async fn cleanup(self) {
        self.pool.close().await;
        if let Ok(base) = PgPoolOptions::new()
            .max_connections(1)
            .acquire_timeout(Duration::from_secs(10))
            .connect(&self.admin_url)
            .await
        {
            let _ = sqlx::query(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.schema))
                .execute(&base)
                .await;
            base.close().await;
        }
    }
}

fn sign_test_jwt(secret: &str, claims: Value) -> String {
    let header = URL_SAFE_NO_PAD.encode(br#"{"alg":"HS256","typ":"JWT"}"#);
    let payload = URL_SAFE_NO_PAD.encode(claims.to_string());
    let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes()).unwrap();
    mac.update(format!("{header}.{payload}").as_bytes());
    let sig = URL_SAFE_NO_PAD.encode(mac.finalize().into_bytes());
    format!("{header}.{payload}.{sig}")
}

fn user_jwt(role: &str, user_id: &str) -> String {
    sign_test_jwt(
        TEST_USER_SECRET,
        json!({
            "sub": user_id,
            "role": role,
            "exp": Utc::now().timestamp() + 300,
        }),
    )
}

fn service_jwt() -> String {
    sign_test_jwt(
        TEST_SERVICE_SECRET,
        json!({
            "sub": "order-service",
            "role": "admin",
            "service": "order-service",
            "exp": Utc::now().timestamp() + 300,
        }),
    )
}

fn grpc_req_with_token<T>(payload: T, token: &str) -> tonic::Request<T> {
    let mut req = tonic::Request::new(payload);
    req.metadata_mut().insert(
        "authorization",
        MetadataValue::try_from(format!("Bearer {token}")).unwrap(),
    );
    req
}

fn single_alloc(order_id: &str, shop_id: &str, amount_vnd: i64) -> Vec<ShopAllocationInput> {
    vec![ShopAllocationInput {
        order_id: Some(order_id.to_string()),
        shop_id: shop_id.to_string(),
        subtotal_vnd: amount_vnd,
        commission_vnd: amount_vnd / 20,
        sales_channel: String::new(),
        storefront_shop_id: String::new(),
        shipping_fee_vnd: 0,
        platform_discount_vnd: 0,
    }]
}

fn build_signed_zp_callback(
    app_id: i32,
    app_trans_id: &str,
    amount_vnd: i64,
    zp_trans_id: i64,
    server_time: i64,
    key2: &str,
) -> nexusmart_payment::zalopay::ZaloPayCallbackRequest {
    let data = ZaloPayCallbackData {
        app_id,
        app_trans_id: app_trans_id.to_string(),
        app_time: Utc::now().timestamp_millis(),
        app_user: "buyer-1".to_string(),
        amount: amount_vnd,
        embed_data: "{}".to_string(),
        item: "[]".to_string(),
        zp_trans_id,
        server_time,
        channel: 38,
        merchant_user_id: "zp-buyer-1".to_string(),
        user_fee_amount: 0,
        discount_amount: 0,
    };
    MockZaloPay::build_signed_callback(key2, &data).expect("build signed callback")
}

/// Upgrade path test on REAL Postgres: apply `0001..0009`, insert pre-existing populated data,
/// then apply `0010` and `0011` and verify all new constraints, seeds, indexes, and DB-level
/// maker != checker CHECK.
#[tokio::test]
async fn test_pg_w3b2_migration_upgrade_0009_to_0010_to_0011_on_populated_db() {
    let db_url = match std::env::var("TEST_PAYMENT_DB_URL") {
        Ok(v) if !v.trim().is_empty() => v,
        _ => {
            eprintln!("skipping test_pg_w3b2_migration_upgrade_0009_to_0010_to_0011_on_populated_db: TEST_PAYMENT_DB_URL not set");
            return;
        }
    };

    let permit = SCHEMA_SEM.acquire().await.expect("schema semaphore");
    let schema = format!("payupg0011_{}", Uuid::new_v4().simple());
    let base = PgPoolOptions::new()
        .max_connections(2)
        .connect(&db_url)
        .await
        .expect("connect admin");
    sqlx::query("CREATE EXTENSION IF NOT EXISTS pgcrypto")
        .execute(&base)
        .await
        .ok();
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&base)
        .await
        .expect("create schema");
    base.close().await;

    let schema_hook = schema.clone();
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .after_connect(move |conn, _| {
            let s = schema_hook.clone();
            Box::pin(async move {
                conn.execute(format!("SET search_path TO {s}, public").as_str())
                    .await?;
                Ok(())
            })
        })
        .connect(&db_url)
        .await
        .expect("connect schema pool");

    // 1. Apply migrations 0001..0011 (including phase-0b 0002..0003)
    let pre_migrations = [
        include_str!("../migrations/0001_init.sql"),
        include_str!("../migrations/0002_sales_channel.sql"),
        include_str!("../migrations/0003_zalopay.sql"),
        include_str!("../migrations/0004_payment_money_model.sql"),
        include_str!("../migrations/0005_refund_settlement_and_reconciliation.sql"),
        include_str!("../migrations/0006_reconciliation_queue_unique_tx.sql"),
        include_str!("../migrations/0007_reconciliation_fixes.sql"),
        include_str!("../migrations/0008_trim_provider_tx_ids.sql"),
        include_str!("../migrations/0009_payment_fee_configs.sql"),
        include_str!("../migrations/0010_outbox_lease_and_fee_config_index.sql"),
        include_str!("../migrations/0011_cod_reconciliation_and_dlq.sql"),
    ];
    for (idx, sql) in pre_migrations.iter().enumerate() {
        sqlx::raw_sql(sql)
            .execute(&pool)
            .await
            .unwrap_or_else(|e| panic!("apply migration {}: {e}", idx + 1));
    }

    // 2. Seed pre-existing populated data before 0012 & 0013
    let existing_pay_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO payments (
            order_group_id, order_id, user_id, method, provider,
            amount_vnd, status, provider_order_code, trace_id
        )
        VALUES ('grp-legacy', 'ord-legacy', 'buyer-legacy', 'VIETQR', 'vietqr_mock',
                450000, 'succeeded', 'NMLEGACY01', 'trace-legacy')
        RETURNING id
        "#,
    )
    .fetch_one(&pool)
    .await
    .expect("insert legacy payment");

    let existing_refund_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO refunds (payment_id, order_id, amount_vnd, reason, status, idempotency_key)
        VALUES ($1, 'ord-legacy', 100000, 'legacy_partial', 'pending', 'legacy-ref-1')
        RETURNING id
        "#,
    )
    .bind(existing_pay_id)
    .fetch_one(&pool)
    .await
    .expect("insert legacy refund");

    // 3. Apply 0012 then 0013 on populated DB
    sqlx::raw_sql(include_str!(
        "../migrations/0012_zalopay_and_method_configs.sql"
    ))
    .execute(&pool)
    .await
    .expect("apply 0012 on populated DB");

    sqlx::raw_sql(include_str!(
        "../migrations/0013_refund_two_person_approval.sql"
    ))
    .execute(&pool)
    .await
    .expect("apply 0013 on populated DB");

    // 4. Verify legacy rows preserved with NULL new columns
    let (legacy_status, legacy_req_by): (String, Option<String>) =
        sqlx::query_as("SELECT status, requested_by FROM refunds WHERE id = $1")
            .bind(existing_refund_id)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(legacy_status, "pending");
    assert_eq!(legacy_req_by, None);

    // 5. Verify 0012 seeds (`payment_method_configs` + ZALOPAY 0 bps fee)
    let method_count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM payment_method_configs")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(method_count, 3);

    // 6. Verify 0013 status CHECK accepts `pending_approval` and `rejected`, and DB CHECK rejects maker == checker
    let pa_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO refunds (
            payment_id, order_id, amount_vnd, reason, status, idempotency_key, requested_by
        )
        VALUES ($1, 'ord-legacy', 5000000, 'large_manual', 'pending_approval', 'pa-1', 'maker-1')
        RETURNING id
        "#,
    )
    .bind(existing_pay_id)
    .fetch_one(&pool)
    .await
    .expect("insert pending_approval refund");

    let self_approve_err = sqlx::query(
        "UPDATE refunds SET status = 'pending', approved_by = 'maker-1', approved_at = NOW() WHERE id = $1",
    )
    .bind(pa_id)
    .execute(&pool)
    .await
    .expect_err("DB CHECK chk_refunds_maker_ne_checker must reject approved_by == requested_by");
    assert!(
        self_approve_err
            .to_string()
            .contains("chk_refunds_maker_ne_checker"),
        "unexpected error: {self_approve_err}"
    );

    // Different checker succeeds at DB level
    sqlx::query(
        "UPDATE refunds SET status = 'rejected', approved_by = 'checker-2', approved_at = NOW(), approval_note = 'no' WHERE id = $1",
    )
    .bind(pa_id)
    .execute(&pool)
    .await
    .expect("different checker must pass chk_refunds_maker_ne_checker");

    // 7. Apply 0014 (p3 Item 1 ZaloPay late-payment sweep) on populated DB
    sqlx::raw_sql(include_str!(
        "../migrations/0014_zalopay_late_payment_sweep.sql"
    ))
    .execute(&pool)
    .await
    .expect("apply 0014 on populated DB");

    let (legacy_final, legacy_attempts): (Option<String>, i32) = sqlx::query_as(
        "SELECT provider_final_status, provider_check_attempts FROM payments WHERE id = $1",
    )
    .bind(existing_pay_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(legacy_final, None);
    assert_eq!(legacy_attempts, 0);

    let bad_final =
        sqlx::query("UPDATE payments SET provider_final_status = 'bogus_status' WHERE id = $1")
            .bind(existing_pay_id)
            .execute(&pool)
            .await;
    assert!(
        bad_final.is_err(),
        "0014 CHECK must reject unknown provider_final_status"
    );

    // 8. Apply 0015 (p4 refund lease/CAS + created_by_service) on populated DB
    sqlx::query(
        r#"
        INSERT INTO refunds (payment_id, order_id, amount_vnd, reason, status, idempotency_key)
        VALUES ($1, 'ord-legacy', 50000, 'order_cancelled_auto_refund', 'pending', 'order-cancel-refund:ord-legacy')
        "#,
    )
    .bind(existing_pay_id)
    .execute(&pool)
    .await
    .expect("insert pre-0015 auto cancel refund");

    sqlx::raw_sql(include_str!(
        "../migrations/0015_p4_refund_lease_and_audit.sql"
    ))
    .execute(&pool)
    .await
    .expect("apply 0015 on populated DB");

    let cancel_by_svc: bool = sqlx::query_scalar(
        "SELECT created_by_service FROM refunds WHERE idempotency_key = 'order-cancel-refund:ord-legacy'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    let legacy_by_svc: bool =
        sqlx::query_scalar("SELECT created_by_service FROM refunds WHERE id = $1")
            .bind(existing_refund_id)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(
        cancel_by_svc,
        "0015 backfill must mark legacy auto order-cancel-refund rows as created_by_service=true"
    );
    assert!(!legacy_by_svc);

    // 9. Apply 0016 (p5 durable last /v2/query state), 0017 (w3b3 COD fee adjustments), and
    //    0018 (phase-0b ZaloPay refund_settle backfill, no-op on main DB) on populated DB
    sqlx::raw_sql(include_str!(
        "../migrations/0016_zalopay_last_query_state.sql"
    ))
    .execute(&pool)
    .await
    .expect("apply 0016 on populated DB");

    sqlx::raw_sql(include_str!(
        "../migrations/0017_w3b3_cod_fee_adjustments.sql"
    ))
    .execute(&pool)
    .await
    .expect("apply 0017 on populated DB");

    sqlx::raw_sql(include_str!(
        "../migrations/0018_backfill_phase0b_zalopay_refund_settle.sql"
    ))
    .execute(&pool)
    .await
    .expect("apply 0018 on populated DB");

    let backfill_settle_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE reference_type = 'refund_settle'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        backfill_settle_count, 0,
        "0018 must be a no-op on a main DB with no legacy unsettled phase-0b ZaloPay refunds"
    );

    let (legacy_last_rc, legacy_last_proc): (Option<i32>, Option<bool>) = sqlx::query_as(
        "SELECT provider_last_query_return_code, provider_last_query_is_processing FROM payments WHERE id = $1",
    )
    .bind(existing_pay_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(legacy_last_rc, None);
    assert_eq!(legacy_last_proc, None);

    pool.close().await;
    if let Ok(base) = PgPoolOptions::new()
        .max_connections(1)
        .connect(&db_url)
        .await
    {
        let _ = sqlx::query(&format!("DROP SCHEMA IF EXISTS {schema} CASCADE"))
            .execute(&base)
            .await;
        base.close().await;
    }
    drop(permit);
}

/// F2 & F2-bis attack & replay test on real Postgres:
/// - 1-byte tampered data -> 401 return_code=-1, 0 DB rows written
/// - wrong app_id / unknown app_trans_id -> 400 return_code=-1, 0 DB rows written
/// - server_time skewed 301s (both seconds and ms) -> calls `/v2/query` authoritatively
/// - amount mismatch -> queued in `reconciliation_queue`, 0 confirm
/// - 10x replay (5 concurrent + 5 sequential) same `zp_trans_id` -> exactly 1 ledger tx, balanced.
#[tokio::test]
async fn test_pg_w3b2_f2_zalopay_ipn_attacks_replay_skew_and_amount_mismatch() {
    let Some(db) = TestDb::setup(
        "test_pg_w3b2_f2_zalopay_ipn_attacks_replay_skew_and_amount_mismatch",
        10,
    )
    .await
    else {
        return;
    };
    let (svc, mock) = db.service_with_mock();
    let app = build_router(svc.clone(), db.auth());

    // Create 3 ZALOPAY payments for distinct attack/replay scenarios
    let p_replay = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-zp-replay".into(),
            order_id: "ord-zp-replay".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 250_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-zp-replay", "shop-1", 250_000),
            expires_at: None,
            idempotency_key: Some("idem-zp-replay".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-zp-1".into(),
        })
        .await
        .expect("create zalopay payment");
    assert!(p_replay
        .payment
        .pay_url
        .as_deref()
        .is_some_and(|u| u.starts_with("/thanh-toan/mock-zalopay?")));

    let p_skew = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-zp-skew".into(),
            order_id: "ord-zp-skew".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 180_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-zp-skew", "shop-1", 180_000),
            expires_at: None,
            idempotency_key: Some("idem-zp-skew".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-zp-2".into(),
        })
        .await
        .expect("create zalopay skew payment");

    let p_mismatch = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-zp-mm".into(),
            order_id: "ord-zp-mm".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 300_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-zp-mm", "shop-1", 300_000),
            expires_at: None,
            idempotency_key: Some("idem-zp-mm".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-zp-3".into(),
        })
        .await
        .expect("create zalopay mismatch payment");

    // 1. Tamper 1 byte in `data` -> HTTP 401, return_code = -1, ZERO rows in webhook_events
    let mut tampered = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &p_replay.payment.provider_order_code,
        250_000,
        261_124_000_001,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    tampered.data = tampered.data.replacen("250000", "250001", 1);
    let resp = app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/zalopay")
                .header("content-type", "application/json")
                .body(Body::from(serde_json::to_string(&tampered).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::UNAUTHORIZED);
    let body: Value =
        serde_json::from_slice(&resp.into_body().collect().await.unwrap().to_bytes()).unwrap();
    assert_eq!(body["return_code"], -1);

    let wh_count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM webhook_events")
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(wh_count, 0, "invalid MAC must write ZERO DB rows");

    // 2. Wrong app_id & unknown app_trans_id -> HTTP 400, return_code = -1, ZERO DB rows
    let wrong_app = build_signed_zp_callback(
        999_999,
        &p_replay.payment.provider_order_code,
        250_000,
        261_124_000_002,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let resp = app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/zalopay")
                .header("content-type", "application/json")
                .body(Body::from(serde_json::to_string(&wrong_app).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::BAD_REQUEST);
    let body: Value =
        serde_json::from_slice(&resp.into_body().collect().await.unwrap().to_bytes()).unwrap();
    assert_eq!(body["return_code"], -1);

    let unknown_trans = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        "261124_does_not_exist",
        250_000,
        261_124_000_003,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let resp = app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/zalopay")
                .header("content-type", "application/json")
                .body(Body::from(serde_json::to_string(&unknown_trans).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::BAD_REQUEST);
    let wh_count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM webhook_events")
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(wh_count, 0);

    // 3. server_time skewed 301s (both seconds and ms!) -> MUST NOT trust body, MUST call /v2/query
    // 3a. Seconds skewed -301s while /v2/query returns 2 (fail) -> ignored (return_code=1, 0 confirm)
    mock.set_order_outcome(
        &p_skew.payment.provider_order_code,
        MockBehavior::return_code(2),
    );
    let stale_sec_cb = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &p_skew.payment.provider_order_code,
        180_000,
        261_124_000_010,
        Utc::now().timestamp() - 301,
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let out = svc
        .process_zalopay_callback(stale_sec_cb, "tr-skew-sec")
        .await
        .unwrap();
    assert!(matches!(out, ZaloPayCallbackOutcome::Ignored { .. }));
    assert_eq!(
        svc.get_payment(Some(p_skew.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "pending"
    );

    // 3b. Milliseconds skewed -301_000ms while /v2/query returns 3 (processing) -> return_code = 0 (retry)
    mock.set_order_outcome(
        &p_skew.payment.provider_order_code,
        MockBehavior::return_code(3),
    );
    let stale_ms_cb = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &p_skew.payment.provider_order_code,
        180_000,
        261_124_000_010,
        Utc::now().timestamp_millis() - 301_000,
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let out = svc
        .process_zalopay_callback(stale_ms_cb.clone(), "tr-skew-ms-3")
        .await
        .unwrap();
    assert!(matches!(out, ZaloPayCallbackOutcome::RetryableError { .. }));
    assert_eq!(out.zalopay_return_code().0, 0);

    // 3c. Now /v2/query returns 1 -> stale callback confirms authoritatively!
    mock.set_order_outcome(
        &p_skew.payment.provider_order_code,
        MockBehavior::return_code(1),
    );
    let out = svc
        .process_zalopay_callback(stale_ms_cb, "tr-skew-ms-1")
        .await
        .unwrap();
    assert!(matches!(out, ZaloPayCallbackOutcome::Confirmed { .. }));
    assert_eq!(
        svc.get_payment(Some(p_skew.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "succeeded"
    );

    // 4. Amount mismatch -> queued in reconciliation_queue, 0 confirm
    let mismatch_cb = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &p_mismatch.payment.provider_order_code,
        299_999, // 1 VND short!
        261_124_000_020,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let out = svc
        .process_zalopay_callback(mismatch_cb, "tr-mm")
        .await
        .unwrap();
    assert!(matches!(
        out,
        ZaloPayCallbackOutcome::QueuedReconciliation { .. }
    ));
    assert_eq!(out.zalopay_return_code().0, 1);
    assert_eq!(
        svc.get_payment(Some(p_mismatch.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "pending"
    );
    let rq_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM reconciliation_queue WHERE provider_tx_id = '261124000020' AND reason = 'amount_mismatch'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(rq_count, 1);

    // 5. 10x replay (5 concurrent + 5 sequential) with same zp_trans_id -> exactly 1 ledger tx, balanced
    let replay_zp_tx = 261_124_000_099_i64;
    let replay_cb = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &p_replay.payment.provider_order_code,
        250_000,
        replay_zp_tx,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let replay_body = serde_json::to_string(&replay_cb).unwrap();
    let barrier = Arc::new(Barrier::new(5));
    let mut handles = Vec::new();
    for _ in 0..5 {
        let app_c = app.clone();
        let body_c = replay_body.clone();
        let b = barrier.clone();
        handles.push(tokio::spawn(async move {
            b.wait().await;
            let resp = app_c
                .oneshot(
                    Request::builder()
                        .method(Method::POST)
                        .uri("/webhooks/zalopay")
                        .header("content-type", "application/json")
                        .body(Body::from(body_c))
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(resp.status(), StatusCode::OK);
            let v: Value =
                serde_json::from_slice(&resp.into_body().collect().await.unwrap().to_bytes())
                    .unwrap();
            assert_eq!(v["return_code"], 1);
        }));
    }
    for h in handles {
        h.await.unwrap();
    }
    for _ in 0..5 {
        let resp = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/webhooks/zalopay")
                    .header("content-type", "application/json")
                    .body(Body::from(replay_body.clone()))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        let v: Value =
            serde_json::from_slice(&resp.into_body().collect().await.unwrap().to_bytes()).unwrap();
        assert_eq!(v["return_code"], 1);
    }

    let ledger_count_for_replay: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM ledger_transactions WHERE reference_id = $1")
            .bind(p_replay.payment.id.to_string())
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(ledger_count_for_replay, 1);

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary.is_balanced);
    assert_eq!(summary.total_debits_vnd, summary.total_credits_vnd);
    // p_skew (180k) + p_replay (250k) = 430k
    assert_eq!(summary.total_debits_vnd, 430_000);

    db.cleanup().await;
}

/// F5 / UC-B25 race:
/// (A) Late ZALOPAY IPN on superseded payment racing new VIETQR IPN -> exactly 1 `succeeded`,
///     superseded ZALOPAY produces exactly 1 `late_payment` refund, `debit == credit`.
/// (B) Late ZALOPAY IPN after switching to COD -> COD stays awaiting collection, superseded
///     ZALOPAY produces 1 `late_payment` refund without confirming order.
#[tokio::test]
async fn test_pg_w3b2_f5_b25_zalopay_superseded_late_ipn_race() {
    let Some(db) = TestDb::setup("test_pg_w3b2_f5_b25_zalopay_superseded_late_ipn_race", 10).await
    else {
        return;
    };
    let (svc, _) = db.service_with_mock();

    // Case A: ZALOPAY -> supersede with VIETQR -> race late ZALOPAY IPN vs new VIETQR IPN
    let p_zp = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-b25-a".into(),
            order_id: "ord-b25-a".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 400_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-b25-a", "shop-1", 400_000),
            expires_at: None,
            idempotency_key: Some("idem-b25-zp".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-b25-1".into(),
        })
        .await
        .unwrap();

    let p_qr = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-b25-a".into(),
            order_id: "ord-b25-a".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 400_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-b25-a", "shop-1", 400_000),
            expires_at: p_zp.payment.expires_at,
            idempotency_key: Some("idem-b25-qr".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-b25-2".into(),
        })
        .await
        .unwrap();

    let late_zp_cb = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &p_zp.payment.provider_order_code,
        400_000,
        261_124_555_001,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let qr_payload = IncomingWebhookPayload {
        provider: Some("vietqr_mock".into()),
        provider_tx_id: "QR-B25-001".into(),
        payment_id: Some(p_qr.payment.id.to_string()),
        order_group_id: Some("grp-b25-a".into()),
        order_id: Some("ord-b25-a".into()),
        provider_order_code: Some(p_qr.payment.provider_order_code.clone()),
        reference: None,
        amount_vnd: 400_000,
        status: "SUCCEEDED".into(),
        occurred_at: Some(Utc::now().to_rfc3339()),
    };

    let barrier = Arc::new(Barrier::new(2));
    let svc1 = svc.clone();
    let b1 = barrier.clone();
    let h_zp = tokio::spawn(async move {
        b1.wait().await;
        svc1.process_zalopay_callback(late_zp_cb, "tr-race-zp")
            .await
            .unwrap()
    });
    let svc2 = svc.clone();
    let b2 = barrier.clone();
    let h_qr = tokio::spawn(async move {
        b2.wait().await;
        svc2.process_verified_webhook(
            qr_payload,
            json!({"race": "qr"}),
            "vietqr_mock",
            "tr-race-qr",
        )
        .await
        .unwrap()
    });

    let (zp_out, qr_out) = tokio::join!(h_zp, h_qr);
    assert!(matches!(
        zp_out.unwrap(),
        ZaloPayCallbackOutcome::LateRefundQueued { .. }
    ));
    assert_eq!(qr_out.unwrap().status, "processed");

    let succeeded_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM payments WHERE order_group_id = 'grp-b25-a' AND status = 'succeeded'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(succeeded_count, 1);

    let late_refund_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM refunds WHERE payment_id = $1 AND is_late_payment = TRUE AND provider_m_refund_id IS NOT NULL",
    )
    .bind(p_zp.payment.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(late_refund_count, 1);

    // Case B: ZALOPAY -> supersede with COD -> late ZALOPAY IPN triggers late_payment refund, does NOT confirm COD
    let p_zp2 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-b25-b".into(),
            order_id: "ord-b25-b".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 200_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-b25-b", "shop-1", 200_000),
            expires_at: None,
            idempotency_key: Some("idem-b25-zp2".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-b25-3".into(),
        })
        .await
        .unwrap();

    let p_cod = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-b25-b".into(),
            order_id: "ord-b25-b".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 200_000,
            method: PaymentMethod::Cod,
            shop_allocations: single_alloc("ord-b25-b", "shop-1", 200_000),
            expires_at: None,
            idempotency_key: Some("idem-b25-cod".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-b25-4".into(),
        })
        .await
        .unwrap();

    let late_zp2_cb = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &p_zp2.payment.provider_order_code,
        200_000,
        261_124_555_002,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let out2 = svc
        .process_zalopay_callback(late_zp2_cb, "tr-race-cod")
        .await
        .unwrap();
    assert!(matches!(
        out2,
        ZaloPayCallbackOutcome::LateRefundQueued { .. }
    ));
    assert_eq!(
        svc.get_payment(Some(p_cod.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "pending"
    );

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary.is_balanced);
    assert_eq!(summary.total_debits_vnd, summary.total_credits_vnd);

    db.cleanup().await;
}

/// F3 expiry scanner: `/v2/query` returns 1 -> confirm; returns 2, 3, or network error -> expire normally
/// (and a late callback arriving afterwards creates a `late_payment` refund).
#[tokio::test]
async fn test_pg_w3b2_f3_expiry_scanner_zalopay_query_1_2_3_and_network_error() {
    let Some(db) = TestDb::setup(
        "test_pg_w3b2_f3_expiry_scanner_zalopay_query_1_2_3_and_network_error",
        6,
    )
    .await
    else {
        return;
    };
    let (svc, mock) = db.service_with_mock();

    let past = Utc::now() - ChronoDuration::minutes(20);
    let mut created = Vec::new();
    for i in 1..=4 {
        let ord = format!("ord-f3-{i}");
        let p = svc
            .create_payment(CreatePaymentInput {
                order_group_id: format!("grp-f3-{i}"),
                order_id: ord.clone(),
                user_id: "buyer-1".into(),
                amount_vnd: 150_000,
                method: PaymentMethod::ZaloPay,
                shop_allocations: single_alloc(&ord, "shop-1", 150_000),
                expires_at: Some(past),
                idempotency_key: Some(format!("idem-f3-{i}")),
                supersede_previous: false,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                trace_id: format!("tr-f3-{i}"),
            })
            .await
            .unwrap();
        created.push(p.payment);
    }

    // 1 -> confirm; 2 -> expire; 3 -> expire; network error -> expire per F3
    mock.set_order_outcome(
        &created[0].provider_order_code,
        MockBehavior::return_code(1),
    );
    mock.set_order_outcome(
        &created[1].provider_order_code,
        MockBehavior::return_code(2),
    );
    mock.set_order_outcome(
        &created[2].provider_order_code,
        MockBehavior::return_code(3),
    );
    mock.set_order_outcome(
        &created[3].provider_order_code,
        MockBehavior::network_error("zalopay unreachable"),
    );

    let expired_cnt = svc.scan_expired_payments().await.unwrap();
    assert_eq!(expired_cnt, 3, "payments 2, 3, and 4 must expire");

    assert_eq!(
        svc.get_payment(Some(created[0].id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "succeeded"
    );
    for (idx, p) in created.iter().enumerate().skip(1) {
        assert_eq!(
            svc.get_payment(Some(p.id), None, None)
                .await
                .unwrap()
                .payment
                .status,
            "expired",
            "payment {} must be expired",
            idx + 1
        );
    }

    // Late IPN arriving for expired payment #4 goes to late_payment refund without confirming order.
    let late_cb = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &created[3].provider_order_code,
        150_000,
        261_124_777_004,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let out = svc
        .process_zalopay_callback(late_cb, "tr-f3-late")
        .await
        .unwrap();
    assert!(matches!(
        out,
        ZaloPayCallbackOutcome::LateRefundQueued { .. }
    ));

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary.is_balanced);

    db.cleanup().await;
}

/// F4 async ZaloPay wallet refund + multi-replica `SKIP LOCKED` poller race + permanent error -> `manual_review`
/// + `order.cancelled` auto-refund on paid ZALOPAY (`is_online_method()`).
#[tokio::test]
async fn test_pg_w3b2_f4_zalopay_wallet_refund_async_skip_locked_race_and_manual_review() {
    let Some(db) = TestDb::setup(
        "test_pg_w3b2_f4_zalopay_wallet_refund_async_skip_locked_race_and_manual_review",
        10,
    )
    .await
    else {
        return;
    };
    let (svc, mock) = db.service_with_mock();

    // Helper: create and confirm a ZALOPAY payment
    let confirm_zp = |ord: String, zp_tx: i64| {
        let svc_c = svc.clone();
        async move {
            let p = svc_c
                .create_payment(CreatePaymentInput {
                    order_group_id: format!("grp-{ord}"),
                    order_id: ord.clone(),
                    user_id: "buyer-1".into(),
                    amount_vnd: 250_000,
                    method: PaymentMethod::ZaloPay,
                    shop_allocations: single_alloc(&ord, "shop-1", 250_000),
                    expires_at: None,
                    idempotency_key: Some(format!("idem-{ord}")),
                    supersede_previous: false,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    trace_id: format!("tr-{ord}"),
                })
                .await
                .unwrap();
            let cb = build_signed_zp_callback(
                DEFAULT_ZALOPAY_MOCK_APP_ID,
                &p.payment.provider_order_code,
                250_000,
                zp_tx,
                Utc::now().timestamp_millis(),
                DEV_ONLY_ZALOPAY_MOCK_KEY2,
            );
            let out = svc_c
                .process_zalopay_callback(cb, &format!("tr-cb-{ord}"))
                .await
                .unwrap();
            assert!(matches!(out, ZaloPayCallbackOutcome::Confirmed { .. }));
            p.payment
        }
    };

    // Case 1: `/v2/refund` returns 3 (processing), then 2 concurrent pollers race with SKIP LOCKED when query_refund=1
    let p1 = confirm_zp("ord-f4-async".into(), 261_124_888_001).await;
    mock.push_behavior(
        nexusmart_payment::zalopay::MockOperation::Refund,
        MockBehavior::return_code(3),
    );
    let r1 = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p1.id),
            order_id: "ord-f4-async".into(),
            amount_vnd: 250_000,
            reason: "buyer_return".into(),
            idempotency_key: "idem-ref-f4-1".into(),
            trace_id: "tr-ref-f4-1".into(),
            require_approval_if_large: true,
            requested_by: Some("cskh-1".into()),
            created_by_service: false,
        })
        .await
        .unwrap();
    assert_eq!(r1.refund.status, "pending");
    assert_eq!(r1.refund.provider_status.as_deref(), Some("processing"));
    let m_ref_1 = r1
        .refund
        .provider_m_refund_id
        .clone()
        .expect("provider_m_refund_id must be persisted before calling /v2/refund");

    // Flip mock query_refund to 1 and run 2 concurrent multi-replica pollers behind Barrier
    mock.set_query_refund_outcome(&m_ref_1, MockBehavior::return_code(1));
    let barrier = Arc::new(Barrier::new(2));
    let s1 = svc.clone();
    let b1 = barrier.clone();
    let h1 = tokio::spawn(async move {
        b1.wait().await;
        s1.poll_zalopay_refunds_with_cooldown(ChronoDuration::zero())
            .await
            .unwrap()
    });
    let s2 = svc.clone();
    let b2 = barrier.clone();
    let h2 = tokio::spawn(async move {
        b2.wait().await;
        s2.poll_zalopay_refunds_with_cooldown(ChronoDuration::zero())
            .await
            .unwrap()
    });
    let (c1, c2) = tokio::join!(h1, h2);
    assert!(c1.unwrap() + c2.unwrap() >= 1);

    let (st1, pst1): (String, Option<String>) =
        sqlx::query_as("SELECT status, provider_status FROM refunds WHERE id = $1")
            .bind(r1.refund.id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(st1, "completed");
    assert_eq!(pst1.as_deref(), Some("succeeded"));

    let settle_outbox_cnt: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'payment.refund_settled' AND payload->>'refund_id' = $1",
    )
    .bind(r1.refund.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        settle_outbox_cnt, 1,
        "exactly 1 payment.refund_settled outbox event"
    );

    // Case 2: Permanent `/v2/refund` error (`return_code=2, sub_return_code=-14`) -> `manual_review`, 0 settle ledger
    let p2 = confirm_zp("ord-f4-perm".into(), 261_124_888_002).await;
    mock.push_behavior(
        nexusmart_payment::zalopay::MockOperation::Refund,
        MockBehavior::ReturnCode {
            return_code: 2,
            sub_return_code: -14,
            return_message: "refund window expired".into(),
            amount_vnd: None,
            zp_trans_id: None,
            server_time: None,
            is_processing: None,
        },
    );
    let r2 = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p2.id),
            order_id: "ord-f4-perm".into(),
            amount_vnd: 250_000,
            reason: "late_return".into(),
            idempotency_key: "idem-ref-f4-2".into(),
            trace_id: "tr-ref-f4-2".into(),
            require_approval_if_large: true,
            requested_by: Some("cskh-1".into()),
            created_by_service: false,
        })
        .await
        .unwrap();
    assert_eq!(r2.refund.status, "manual_review");
    assert_eq!(r2.refund.provider_status.as_deref(), Some("failed"));
    let settle_tx_r2: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM ledger_transactions WHERE idempotency_key = $1")
            .bind(format!("refund-settle:{}", r2.refund.id))
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(settle_tx_r2, 0, "manual_review must post 0 settle ledger");

    // Manual accounting settlement of `manual_review` refund succeeds cleanly
    let manual_settled = svc
        .settle_refund(SettleRefundInput {
            refund_id: r2.refund.id,
            provider_refund_id: "MANUAL-BANK-FT-99".into(),
            note: "Settled manually via bank transfer after ZaloPay wallet refund expired".into(),
            idempotency_key: "settle-manual-r2".into(),
            trace_id: "tr-settle-r2".into(),
        })
        .await
        .unwrap();
    assert_eq!(manual_settled.refund.status, "completed");

    // Case 3: `order.cancelled` on paid ZALOPAY auto-creates refund via `is_online_method()` and settles via ZaloPay
    let p3 = confirm_zp("ord-f4-cancel".into(), 261_124_888_003).await;
    let processed = svc
        .process_order_cancelled_event("evt-cancel-zp-1", "ord-f4-cancel", "tr-cancel-zp")
        .await
        .unwrap();
    assert!(processed);
    let (c_st, c_mref): (String, Option<String>) =
        sqlx::query_as("SELECT status, provider_m_refund_id FROM refunds WHERE payment_id = $1")
            .bind(p3.id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert!(c_mref.is_some());
    assert_eq!(c_st, "completed");

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary.is_balanced);

    db.cleanup().await;
}

/// F6 & F7 config & races:
/// - Public vs `payment_methods.read` projection over real gRPC + Postgres
/// - Disabling ZALOPAY rejects new checkouts (`PAYMENT_METHOD_DISABLED`) while in-flight ZALOPAY
///   payment still confirms via IPN
/// - Race 1: 2 concurrent `UpdatePaymentMethodConfig` with same `expected_version` -> 1 ok, 1 `VERSION_CONFLICT`
/// - Race 2: 2 concurrent disables of last 2 enabled methods -> 1 ok, 1 `InvalidArgument` (at least 1 stays enabled).
#[tokio::test]
async fn test_pg_w3b2_f6_f7_method_configs_disable_inflight_ipn_and_concurrent_version_races() {
    let Some(db) = TestDb::setup(
        "test_pg_w3b2_f6_f7_method_configs_disable_inflight_ipn_and_concurrent_version_races",
        10,
    )
    .await
    else {
        return;
    };
    let (svc, _) = db.service_with_mock();
    let grpc = PaymentGrpcServer::new(svc.clone(), db.auth());

    // 1. Public caller vs `payment_methods.read` (`ops`) via gRPC
    let pub_resp = grpc
        .get_payment_method_configs(tonic::Request::new(pb::GetPaymentMethodConfigsRequest {
            method: pb::PaymentMethod::Unspecified as i32,
        }))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(pub_resp.configs.len(), 3);
    for c in &pub_resp.configs {
        assert!(!c.credentials_configured);
        assert!(c.credential_preview.is_empty());
        assert!(c.updated_by.is_empty());
        assert!(c.note.is_empty());
    }

    let ops_resp = grpc
        .get_payment_method_configs(grpc_req_with_token(
            pb::GetPaymentMethodConfigsRequest {
                method: pb::PaymentMethod::Zalopay as i32,
            },
            &user_jwt("ops", "ops-1"),
        ))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(ops_resp.configs.len(), 1);
    assert!(ops_resp.configs[0].credentials_configured);
    assert!(ops_resp.configs[0]
        .credential_preview
        .contains("[REDACTED]"));
    assert_eq!(ops_resp.configs[0].version, 1);

    // 2. Create an in-flight ZALOPAY payment BEFORE disabling ZALOPAY
    let inflight = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-f7-inflight".into(),
            order_id: "ord-f7-inflight".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 220_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-f7-inflight", "shop-1", 220_000),
            expires_at: None,
            idempotency_key: Some("idem-f7-inflight".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-f7-1".into(),
        })
        .await
        .unwrap();

    // 3. Race 1: Two concurrent updates on ZALOPAY with expected_version = 1 -> 1 ok, 1 VERSION_CONFLICT
    let barrier = Arc::new(Barrier::new(2));
    let mut handles = Vec::new();
    for i in 0..2 {
        let s = svc.clone();
        let b = barrier.clone();
        handles.push(tokio::spawn(async move {
            b.wait().await;
            s.update_payment_method_config(UpdatePaymentMethodConfigInput {
                method: PaymentMethod::ZaloPay,
                enabled: false,
                min_order_vnd: 0,
                max_order_vnd: 0,
                expiry_minutes: 15,
                note: format!("disable-race-{i}"),
                expected_version: 1,
                updated_by: format!("ops-{i}"),
            })
            .await
        }));
    }
    let mut ok_cnt = 0;
    let mut conflict_cnt = 0;
    for h in handles {
        match h.await.unwrap() {
            Ok(cfg) => {
                assert_eq!(cfg.version, 2);
                assert!(!cfg.enabled);
                ok_cnt += 1;
            }
            Err(PaymentError::FailedPrecondition(msg)) if msg.contains("VERSION_CONFLICT") => {
                conflict_cnt += 1;
            }
            Err(other) => panic!("unexpected error in version conflict race: {other:?}"),
        }
    }
    assert_eq!(ok_cnt, 1);
    assert_eq!(conflict_cnt, 1);

    // 4. Disabled ZALOPAY blocks NEW checkout (`PAYMENT_METHOD_DISABLED`), while IN-FLIGHT IPN succeeds!
    let new_err = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-f7-blocked".into(),
            order_id: "ord-f7-blocked".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 220_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-f7-blocked", "shop-1", 220_000),
            expires_at: None,
            idempotency_key: Some("idem-f7-blocked".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-f7-2".into(),
        })
        .await
        .unwrap_err();
    assert!(matches!(
        new_err,
        PaymentError::FailedPrecondition(ref m) if m.starts_with("PAYMENT_METHOD_DISABLED")
    ));

    let inflight_cb = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &inflight.payment.provider_order_code,
        220_000,
        261_124_666_001,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let cb_out = svc
        .process_zalopay_callback(inflight_cb, "tr-f7-inflight-ipn")
        .await
        .unwrap();
    assert!(matches!(cb_out, ZaloPayCallbackOutcome::Confirmed { .. }));

    // 5. COD max_order_vnd (5,000,000 seeded) enforced on CreatePayment
    let cod_over = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-f6-cod-over".into(),
            order_id: "ord-f6-cod-over".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 5_000_001,
            method: PaymentMethod::Cod,
            shop_allocations: single_alloc("ord-f6-cod-over", "shop-1", 5_000_001),
            expires_at: None,
            idempotency_key: Some("idem-f6-cod-over".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-f6-cod".into(),
        })
        .await
        .unwrap_err();
    assert!(matches!(
        cod_over,
        PaymentError::FailedPrecondition(ref m) if m.starts_with("COD_LIMIT_EXCEEDED")
    ));

    // 6. Race 2: ZALOPAY is disabled; race disabling the last 2 enabled methods (COD & VIETQR)
    //    simultaneously -> exactly 1 succeeds, 1 rejected with Validation (InvalidArgument)!
    let barrier2 = Arc::new(Barrier::new(2));
    let s_cod = svc.clone();
    let b_cod = barrier2.clone();
    let h_cod = tokio::spawn(async move {
        b_cod.wait().await;
        s_cod
            .update_payment_method_config(UpdatePaymentMethodConfigInput {
                method: PaymentMethod::Cod,
                enabled: false,
                min_order_vnd: 0,
                max_order_vnd: 5_000_000,
                expiry_minutes: 0,
                note: "disable cod".into(),
                expected_version: 1,
                updated_by: "ops-1".into(),
            })
            .await
    });
    let s_qr = svc.clone();
    let b_qr = barrier2.clone();
    let h_qr = tokio::spawn(async move {
        b_qr.wait().await;
        s_qr.update_payment_method_config(UpdatePaymentMethodConfigInput {
            method: PaymentMethod::VietQr,
            enabled: false,
            min_order_vnd: 0,
            max_order_vnd: 0,
            expiry_minutes: 15,
            note: "disable vietqr".into(),
            expected_version: 1,
            updated_by: "ops-2".into(),
        })
        .await
    });
    let (r_cod, r_qr) = tokio::join!(h_cod, h_qr);
    let results = [r_cod.unwrap(), r_qr.unwrap()];
    let ok_disables = results.iter().filter(|r| r.is_ok()).count();
    let val_disables = results
        .iter()
        .filter(|r| matches!(r, Err(PaymentError::Validation(_))))
        .count();
    assert_eq!(ok_disables, 1);
    assert_eq!(val_disables, 1);

    let remaining_enabled: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM payment_method_configs WHERE enabled = TRUE")
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(remaining_enabled, 1, "at least 1 method must stay enabled");

    db.cleanup().await;
}

/// F11 two-person approval attacks & races on real Postgres:
/// - Anti-splitting (concurrent 2 × 3,000,000 VND on same payment within 24h) -> 1 pending, 1 pending_approval
/// - 24h sum counts `pending_approval` + executed manual refunds, EXCLUDES `rejected`
/// - Service token `require_approval_if_large=false` (saga cancel 8M even with spoofed requested_by) bypasses approval
/// - Service token `require_approval_if_large=true` (AdminResolveReturn 6M) enters `pending_approval`
/// - Maker self-approve -> `PermissionDenied` `MAKER_CHECKER_CONFLICT`
/// - Unauthorized / demoted checker (`cskh`, `ops`, `moderator`) -> `PermissionDenied`
/// - 2 checkers approve concurrently -> 1 succeeds (`pending`, 1 ledger tx, 1 outbox), 1 `FailedPrecondition`.
#[tokio::test]
async fn test_pg_w3b2_f11_two_person_refund_approval_attacks_and_races() {
    let Some(db) = TestDb::setup(
        "test_pg_w3b2_f11_two_person_refund_approval_attacks_and_races",
        10,
    )
    .await
    else {
        return;
    };
    let (svc, _) = db.service_with_mock();
    let grpc = PaymentGrpcServer::new(svc.clone(), db.auth());

    // Helper: create and confirm a 10,000,000 VND VietQR payment
    let confirm_qr = |ord: String, tx_id: String, amt: i64| {
        let svc_c = svc.clone();
        async move {
            let p = svc_c
                .create_payment(CreatePaymentInput {
                    order_group_id: format!("grp-{ord}"),
                    order_id: ord.clone(),
                    user_id: "buyer-1".into(),
                    amount_vnd: amt,
                    method: PaymentMethod::VietQr,
                    shop_allocations: single_alloc(&ord, "shop-1", amt),
                    expires_at: None,
                    idempotency_key: Some(format!("idem-{ord}")),
                    supersede_previous: false,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    trace_id: format!("tr-{ord}"),
                })
                .await
                .unwrap();
            let wh = IncomingWebhookPayload {
                provider: Some("vietqr_mock".into()),
                provider_tx_id: tx_id,
                payment_id: Some(p.payment.id.to_string()),
                order_group_id: Some(p.payment.order_group_id.clone()),
                order_id: Some(ord),
                provider_order_code: Some(p.payment.provider_order_code.clone()),
                reference: None,
                amount_vnd: amt,
                status: "SUCCEEDED".into(),
                occurred_at: Some(Utc::now().to_rfc3339()),
            };
            svc_c
                .process_verified_webhook(wh, json!({"ok": true}), "vietqr_mock", "tr-wh")
                .await
                .unwrap();
            p.payment
        }
    };

    // 1. Anti-splitting race: 2 concurrent 3,000,000 VND manual refunds on same 10M payment
    let p_split = confirm_qr("ord-f11-split".into(), "QR-F11-1".into(), 10_000_000).await;
    let barrier = Arc::new(Barrier::new(2));
    let mut handles = Vec::new();
    for i in 1..=2 {
        let s = svc.clone();
        let b = barrier.clone();
        let pid = p_split.id;
        handles.push(tokio::spawn(async move {
            b.wait().await;
            s.create_refund(CreateRefundInput {
                payment_id: Some(pid),
                order_id: "ord-f11-split".into(),
                amount_vnd: 3_000_000,
                reason: format!("split-part-{i}"),
                idempotency_key: format!("idem-f11-split-{i}"),
                trace_id: format!("tr-f11-split-{i}"),
                require_approval_if_large: true,
                requested_by: Some("cskh-1".into()),
                created_by_service: false,
            })
            .await
            .unwrap()
        }));
    }
    let r_a = handles.remove(0).await.unwrap();
    let r_b = handles.remove(0).await.unwrap();
    let mut statuses = [r_a.refund.status.as_str(), r_b.refund.status.as_str()];
    statuses.sort();
    assert_eq!(
        statuses,
        ["pending", "pending_approval"],
        "concurrent 2x3M split must yield 1 pending and 1 pending_approval"
    );
    let pa_refund = if r_a.refund.status == "pending_approval" {
        r_a.refund.clone()
    } else {
        r_b.refund.clone()
    };

    // Pending approval row MUST have 0 ledger transactions & 0 payment.refunded outbox
    let pa_ledger_cnt: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM ledger_transactions WHERE reference_id = $1")
            .bind(pa_refund.id.to_string())
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(pa_ledger_cnt, 0);
    let pa_outbox_cnt: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'payment.refunded' AND payload->>'refund_id' = $1",
    )
    .bind(pa_refund.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(pa_outbox_cnt, 0);

    // 2. 24h sum counts `pending_approval` and EXCLUDES `rejected`
    let p_sum = confirm_qr("ord-f11-sum".into(), "QR-F11-2".into(), 10_000_000).await;
    // Create 5M -> pending_approval
    let r_big = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p_sum.id),
            order_id: "ord-f11-sum".into(),
            amount_vnd: 5_000_000,
            reason: "big-req".into(),
            idempotency_key: "idem-f11-big".into(),
            trace_id: "tr-big".into(),
            require_approval_if_large: true,
            requested_by: Some("cskh-1".into()),
            created_by_service: false,
        })
        .await
        .unwrap();
    assert_eq!(r_big.refund.status, "pending_approval");

    // While 5M is pending_approval, a 1M refund on same payment MUST ALSO enter pending_approval (5M + 1M >= 5M)
    let r_small_while_pa = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p_sum.id),
            order_id: "ord-f11-sum".into(),
            amount_vnd: 1_000_000,
            reason: "small-while-pa".into(),
            idempotency_key: "idem-f11-small-pa".into(),
            trace_id: "tr-small-pa".into(),
            require_approval_if_large: true,
            requested_by: Some("cskh-1".into()),
            created_by_service: false,
        })
        .await
        .unwrap();
    assert_eq!(r_small_while_pa.refund.status, "pending_approval");

    // Reject both pending_approval rows -> rejected MUST NOT count in 24h sum!
    svc.decide_refund_approval(DecideRefundApprovalInput {
        refund_id: r_big.refund.id,
        approve: false,
        note: "reject big".into(),
        checker_user_id: "accountant-1".into(),
        trace_id: "tr-rej-1".into(),
    })
    .await
    .unwrap();
    svc.decide_refund_approval(DecideRefundApprovalInput {
        refund_id: r_small_while_pa.refund.id,
        approve: false,
        note: "reject small".into(),
        checker_user_id: "accountant-1".into(),
        trace_id: "tr-rej-2".into(),
    })
    .await
    .unwrap();

    // Now a 2M manual refund on p_sum goes straight to `pending` (rejected ones excluded!)
    let r_after_rej = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p_sum.id),
            order_id: "ord-f11-sum".into(),
            amount_vnd: 2_000_000,
            reason: "after-reject".into(),
            idempotency_key: "idem-f11-after-rej".into(),
            trace_id: "tr-after-rej".into(),
            require_approval_if_large: true,
            requested_by: Some("cskh-1".into()),
            created_by_service: false,
        })
        .await
        .unwrap();
    assert_eq!(r_after_rej.refund.status, "pending");

    // 3. Service token rules via gRPC:
    // 3a. Saga cancel (require_approval_if_large=false) for 8M bypasses approval & ignores requested_by
    let p_saga = confirm_qr("ord-f11-saga".into(), "QR-F11-3".into(), 8_000_000).await;
    let saga_resp = grpc
        .create_refund(grpc_req_with_token(
            pb::CreateRefundRequest {
                payment_id: p_saga.id.to_string(),
                order_id: "ord-f11-saga".into(),
                amount_vnd: 8_000_000,
                reason: "saga_cancel".into(),
                idempotency_key: "idem-f11-saga".into(),
                require_approval_if_large: false,
                requested_by: "should-be-ignored".into(),
            },
            &service_jwt(),
        ))
        .await
        .unwrap()
        .into_inner();
    let saga_ref = saga_resp.refund.unwrap();
    assert_eq!(saga_ref.status, pb::RefundStatus::Pending as i32);
    assert!(
        saga_ref.requested_by.is_empty(),
        "service token with require_approval_if_large=false must ignore requested_by"
    );

    // 3b. AdminResolveReturn via service token (require_approval_if_large=true, requested_by="accountant-maker")
    let p_ret = confirm_qr("ord-f11-ret".into(), "QR-F11-4".into(), 6_000_000).await;
    let ret_resp = grpc
        .create_refund(grpc_req_with_token(
            pb::CreateRefundRequest {
                payment_id: p_ret.id.to_string(),
                order_id: "ord-f11-ret".into(),
                amount_vnd: 6_000_000,
                reason: "return_approved".into(),
                idempotency_key: "idem-f11-ret".into(),
                require_approval_if_large: true,
                requested_by: "accountant-maker".into(),
            },
            &service_jwt(),
        ))
        .await
        .unwrap()
        .into_inner();
    let ret_ref = ret_resp.refund.unwrap();
    assert_eq!(ret_ref.status, pb::RefundStatus::PendingApproval as i32);
    assert_eq!(ret_ref.requested_by, "accountant-maker");

    // 4. Attack: Maker self-approves via gRPC -> PermissionDenied MAKER_CHECKER_CONFLICT
    let self_err = grpc
        .decide_refund_approval(grpc_req_with_token(
            pb::DecideRefundApprovalRequest {
                refund_id: ret_ref.refund_id.clone(),
                approve: true,
                note: "self approve".into(),
            },
            &user_jwt("accountant", "accountant-maker"),
        ))
        .await
        .unwrap_err();
    assert_eq!(self_err.code(), tonic::Code::PermissionDenied);
    assert!(
        self_err.message().contains("MAKER_CHECKER_CONFLICT"),
        "got: {}",
        self_err.message()
    );

    // 5. Attack: Demoted / non-approver roles (`cskh`, `ops`, `moderator`) -> PermissionDenied at decision time
    for role in ["cskh", "ops", "moderator"] {
        let err = grpc
            .decide_refund_approval(grpc_req_with_token(
                pb::DecideRefundApprovalRequest {
                    refund_id: ret_ref.refund_id.clone(),
                    approve: true,
                    note: "unauthorized".into(),
                },
                &user_jwt(role, &format!("u-{role}")),
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied, "role={role}");
    }

    // 6. Race: 2 authorized checkers approve concurrently -> 1 succeeds (pending, 1 ledger tx, 1 outbox), 1 FailedPrecondition
    let barrier2 = Arc::new(Barrier::new(2));
    let mut c_handles = Vec::new();
    for (role, uid) in [("accountant", "checker-acc"), ("admin", "checker-adm")] {
        let g = grpc.clone();
        let b = barrier2.clone();
        let rid = ret_ref.refund_id.clone();
        let tok = user_jwt(role, uid);
        c_handles.push(tokio::spawn(async move {
            b.wait().await;
            g.decide_refund_approval(grpc_req_with_token(
                pb::DecideRefundApprovalRequest {
                    refund_id: rid,
                    approve: true,
                    note: "approved".into(),
                },
                &tok,
            ))
            .await
        }));
    }
    let mut approve_ok = 0;
    let mut approve_precond = 0;
    for h in c_handles {
        match h.await.unwrap() {
            Ok(resp) => {
                let rf = resp.into_inner().refund.unwrap();
                assert_eq!(rf.status, pb::RefundStatus::Pending as i32);
                assert!(!rf.approved_by.is_empty());
                assert!(rf.approved_at.is_some());
                approve_ok += 1;
            }
            Err(status) if status.code() == tonic::Code::FailedPrecondition => {
                approve_precond += 1;
            }
            Err(other) => panic!("unexpected concurrent checker error: {other:?}"),
        }
    }
    assert_eq!(approve_ok, 1);
    assert_eq!(approve_precond, 1);

    let approved_ledger_cnt: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM ledger_transactions WHERE reference_id = $1")
            .bind(&ret_ref.refund_id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(approved_ledger_cnt, 1);

    let approved_outbox_cnt: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'payment.refunded' AND payload->>'refund_id' = $1",
    )
    .bind(&ret_ref.refund_id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(approved_outbox_cnt, 1);

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary.is_balanced);

    db.cleanup().await;
}

/// P3 Item 2: `DecideRefundApproval(approve=false)` writes `payment.refund_rejected` to
/// `outbox_events` with `aggregate_id = order_group_id` and exact contracts §5 payload inside
/// the SAME DB transaction, emitted **exactly once** under concurrent + repeated rejections.
#[tokio::test]
async fn test_pg_p3_item2_decide_refund_approval_reject_emits_outbox_once_under_race() {
    let Some(db) = TestDb::setup(
        "test_pg_p3_item2_decide_refund_approval_reject_emits_outbox_once_under_race",
        8,
    )
    .await
    else {
        return;
    };
    let (svc, _) = db.service_with_mock();
    let grpc = PaymentGrpcServer::new(svc.clone(), db.auth());

    let p = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-rej-1".into(),
            order_id: "ord-p3-rej-1".into(),
            user_id: "buyer-rej-1".into(),
            amount_vnd: 6_000_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p3-rej-1", "shop-1", 6_000_000),
            expires_at: None,
            idempotency_key: Some("idem-p3-rej-pay-1".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-rej-pay".into(),
        })
        .await
        .unwrap();
    svc.process_verified_webhook(
        IncomingWebhookPayload {
            provider: Some("vietqr_mock".into()),
            provider_tx_id: "QR-P3-REJ-1".into(),
            payment_id: Some(p.payment.id.to_string()),
            order_group_id: Some("grp-p3-rej-1".into()),
            order_id: Some("ord-p3-rej-1".into()),
            provider_order_code: Some(p.payment.provider_order_code.clone()),
            reference: None,
            amount_vnd: 6_000_000,
            status: "SUCCEEDED".into(),
            occurred_at: Some(Utc::now().to_rfc3339()),
        },
        json!({"ok": true}),
        "vietqr_mock",
        "tr-p3-rej-wh",
    )
    .await
    .unwrap();

    // Create a large refund (6M >= 5M) -> `pending_approval`
    let created = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p.payment.id),
            order_id: "ord-p3-rej-1".into(),
            amount_vnd: 6_000_000,
            reason: "return_approved_admin".into(),
            idempotency_key: "idem-p3-rej-ref-1".into(),
            trace_id: "tr-p3-rej-ref".into(),
            require_approval_if_large: true,
            requested_by: Some("cskh-maker-1".into()),
            created_by_service: false,
        })
        .await
        .unwrap();
    assert_eq!(created.refund.status, "pending_approval");

    // Race 2 authorized checkers rejecting concurrently via gRPC + 1 sequential replay
    let barrier = Arc::new(Barrier::new(2));
    let mut handles = Vec::new();
    for (role, uid) in [
        ("accountant", "checker-acc-rej"),
        ("admin", "checker-adm-rej"),
    ] {
        let g = grpc.clone();
        let b = barrier.clone();
        let rid = created.refund.id.to_string();
        let tok = user_jwt(role, uid);
        handles.push(tokio::spawn(async move {
            b.wait().await;
            g.decide_refund_approval(grpc_req_with_token(
                pb::DecideRefundApprovalRequest {
                    refund_id: rid,
                    approve: false,
                    note: "thieu chung tu".into(),
                },
                &tok,
            ))
            .await
        }));
    }

    let mut winner: Option<String> = None;
    let mut precond_cnt = 0;
    for h in handles {
        match h.await.unwrap() {
            Ok(resp) => {
                let rf = resp.into_inner().refund.unwrap();
                assert_eq!(rf.status, pb::RefundStatus::Rejected as i32);
                winner = Some(rf.approved_by);
            }
            Err(status) if status.code() == tonic::Code::FailedPrecondition => {
                precond_cnt += 1;
            }
            Err(other) => panic!("unexpected concurrent reject error: {other:?}"),
        }
    }
    let winning_checker = winner.expect("exactly one concurrent checker must win rejection");
    assert_eq!(precond_cnt, 1);

    // Repeated decision after rejection -> FailedPrecondition, 0 extra outbox events
    let replay_err = grpc
        .decide_refund_approval(grpc_req_with_token(
            pb::DecideRefundApprovalRequest {
                refund_id: created.refund.id.to_string(),
                approve: false,
                note: "retry reject".into(),
            },
            &user_jwt("super_admin", "checker-sadmin"),
        ))
        .await
        .unwrap_err();
    assert_eq!(replay_err.code(), tonic::Code::FailedPrecondition);

    // Verify 0 ledger entries posted for the rejected refund
    let ledger_cnt: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM ledger_transactions WHERE reference_id = $1")
            .bind(created.refund.id.to_string())
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(ledger_cnt, 0);

    // Verify EXACTLY ONE `payment.refund_rejected` outbox row with exact contracts §5 schema
    let outbox_rows: Vec<(String, Value)> = sqlx::query_as(
        r#"
        SELECT aggregate_id, payload
        FROM outbox_events
        WHERE event_type = 'payment.refund_rejected'
        ORDER BY seq ASC
        "#,
    )
    .fetch_all(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        outbox_rows.len(),
        1,
        "must emit payment.refund_rejected exactly once"
    );
    let (agg_id, payload) = &outbox_rows[0];
    assert_eq!(agg_id, "grp-p3-rej-1");
    assert_eq!(payload["refund_id"], created.refund.id.to_string());
    assert_eq!(payload["payment_id"], p.payment.id.to_string());
    assert_eq!(payload["order_id"], "ord-p3-rej-1");
    assert_eq!(payload["user_id"], "buyer-rej-1");
    assert_eq!(payload["amount_vnd"], 6_000_000);
    assert_eq!(payload["reason"], "thieu chung tu");
    assert_eq!(payload["rejected_by"], winning_checker);

    // Empty note fallback: verify payload.reason still populated cleanly
    let created2 = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p.payment.id),
            order_id: "ord-p3-rej-1".into(),
            amount_vnd: 5_000_000,
            reason: "return_retry".into(),
            idempotency_key: "idem-p3-rej-ref-2".into(),
            trace_id: "tr-p3-rej-ref-2".into(),
            require_approval_if_large: true,
            requested_by: Some("cskh-maker-1".into()),
            created_by_service: false,
        })
        .await
        .unwrap();
    svc.decide_refund_approval(DecideRefundApprovalInput {
        refund_id: created2.refund.id,
        approve: false,
        note: "   ".into(),
        checker_user_id: "checker-acc-2".into(),
        trace_id: "tr-p3-rej-empty-note".into(),
    })
    .await
    .unwrap();
    let payload2: Value = sqlx::query_scalar(
        "SELECT payload FROM outbox_events WHERE event_type = 'payment.refund_rejected' AND payload->>'refund_id' = $1",
    )
    .bind(created2.refund.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(payload2["reason"], "refund_approval_rejected");
    assert_eq!(payload2["rejected_by"], "checker-acc-2");

    db.cleanup().await;
}

/// P3 Item 3 (real Postgres + gRPC + HTTP SSE): `moderator` (and other roles without
/// `refunds.read`/`ledger.read`) CAN access their OWN payment, SSE stream, and refunds,
/// while remaining strictly forbidden (403 / PermissionDenied) from another user's payment,
/// SSE stream, or refunds.
#[tokio::test]
async fn test_pg_p3_item3_moderator_own_payment_sse_and_refunds_vs_others_forbidden() {
    let Some(db) = TestDb::setup(
        "test_pg_p3_item3_moderator_own_payment_sse_and_refunds_vs_others_forbidden",
        6,
    )
    .await
    else {
        return;
    };
    let (svc, _) = db.service_with_mock();
    let grpc = PaymentGrpcServer::new(svc.clone(), db.auth());
    let app = build_router(svc.clone(), db.auth());

    let create_paid_with_refund = |ord: &str, uid: &str, tx_id: &str| {
        let s = svc.clone();
        let ord_s = ord.to_string();
        let uid_s = uid.to_string();
        let tx_s = tx_id.to_string();
        async move {
            let p = s
                .create_payment(CreatePaymentInput {
                    order_group_id: format!("grp-{ord_s}"),
                    order_id: ord_s.clone(),
                    user_id: uid_s.clone(),
                    amount_vnd: 300_000,
                    method: PaymentMethod::VietQr,
                    shop_allocations: single_alloc(&ord_s, "shop-1", 300_000),
                    expires_at: None,
                    idempotency_key: Some(format!("idem-{ord_s}")),
                    supersede_previous: false,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    trace_id: format!("tr-{ord_s}"),
                })
                .await
                .unwrap();
            s.process_verified_webhook(
                IncomingWebhookPayload {
                    provider: Some("vietqr_mock".into()),
                    provider_tx_id: tx_s,
                    payment_id: Some(p.payment.id.to_string()),
                    order_group_id: Some(p.payment.order_group_id.clone()),
                    order_id: Some(ord_s.clone()),
                    provider_order_code: Some(p.payment.provider_order_code.clone()),
                    reference: None,
                    amount_vnd: 300_000,
                    status: "SUCCEEDED".into(),
                    occurred_at: Some(Utc::now().to_rfc3339()),
                },
                json!({"ok": true}),
                "vietqr_mock",
                "tr-wh",
            )
            .await
            .unwrap();
            let rf = s
                .create_refund(CreateRefundInput {
                    payment_id: Some(p.payment.id),
                    order_id: ord_s.clone(),
                    amount_vnd: 100_000,
                    reason: "partial".into(),
                    idempotency_key: format!("idem-rf-{ord_s}"),
                    trace_id: format!("tr-rf-{ord_s}"),
                    require_approval_if_large: false,
                    requested_by: None,
                    created_by_service: false,
                })
                .await
                .unwrap();
            (p.payment, rf.refund)
        }
    };

    let (p_mod, r_mod) = create_paid_with_refund("ord-mod-own", "mod-user-1", "QR-MOD-1").await;
    let (p_other, _r_other) =
        create_paid_with_refund("ord-other-usr", "buyer-other-1", "QR-OTH-1").await;

    let mod_tok = user_jwt("moderator", "mod-user-1");

    // 1. Moderator accessing OWN payment via gRPC GetPayment -> OK (BuyerOwner: commission_vnd == 0)
    let own_pay = grpc
        .get_payment(grpc_req_with_token(
            pb::GetPaymentRequest {
                payment_id: p_mod.id.to_string(),
                order_group_id: String::new(),
                order_id: String::new(),
            },
            &mod_tok,
        ))
        .await
        .expect("moderator must be able to read own payment")
        .into_inner()
        .payment
        .unwrap();
    assert_eq!(own_pay.payment_id, p_mod.id.to_string());
    assert_eq!(own_pay.shop_allocations[0].commission_vnd, 0);

    // 2. Moderator subscribing to OWN payment SSE -> 200 OK
    let sse_own = app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::GET)
                .uri(format!("/payments/{}/events", p_mod.id))
                .header("authorization", format!("Bearer {mod_tok}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(sse_own.status(), StatusCode::OK);

    // 3. Moderator listing OWN refunds (both by order_id and unfiltered) -> sees ONLY own refund, bank masked
    let own_ref_by_ord = grpc
        .list_refunds(grpc_req_with_token(
            pb::ListRefundsRequest {
                status: 0,
                order_id: "ord-mod-own".into(),
                page: 1,
                page_size: 20,
            },
            &mod_tok,
        ))
        .await
        .expect("moderator must list own order refunds")
        .into_inner();
    assert_eq!(own_ref_by_ord.total, 1);
    assert_eq!(own_ref_by_ord.refunds[0].refund_id, r_mod.id.to_string());
    assert!(own_ref_by_ord.refunds[0].bank_account_number.is_empty());

    let own_ref_all = grpc
        .list_refunds(grpc_req_with_token(
            pb::ListRefundsRequest {
                status: 0,
                order_id: String::new(),
                page: 1,
                page_size: 20,
            },
            &mod_tok,
        ))
        .await
        .expect("moderator unfiltered list_refunds must return only own refunds")
        .into_inner();
    assert_eq!(own_ref_all.total, 1);
    assert_eq!(own_ref_all.refunds[0].refund_id, r_mod.id.to_string());

    // 4. Negative tests: every role WITHOUT `refunds.read`/`ledger.read` accessing ANOTHER user's
    //    payment/SSE/refunds -> strictly 403 / PermissionDenied.
    for (role, uid) in [
        ("moderator", "mod-user-1"),
        ("buyer", "buyer-stranger"),
        ("seller", "seller-stranger"),
        ("auditor", "auditor-no-cap"),
    ] {
        let tok = sign_test_jwt(
            TEST_USER_SECRET,
            json!({
                "sub": uid,
                "role": role,
                "shop_id": if role == "seller" { "shop-other" } else { "" },
                "exp": Utc::now().timestamp() + 300,
            }),
        );

        let err_pay = grpc
            .get_payment(grpc_req_with_token(
                pb::GetPaymentRequest {
                    payment_id: p_other.id.to_string(),
                    order_group_id: String::new(),
                    order_id: String::new(),
                },
                &tok,
            ))
            .await
            .unwrap_err();
        assert_eq!(
            err_pay.code(),
            tonic::Code::PermissionDenied,
            "GetPayment other role={role}"
        );

        let sse_other = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(Method::GET)
                    .uri(format!("/payments/{}/events", p_other.id))
                    .header("authorization", format!("Bearer {tok}"))
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(
            sse_other.status(),
            StatusCode::FORBIDDEN,
            "SSE other role={role}"
        );

        let err_ref = grpc
            .list_refunds(grpc_req_with_token(
                pb::ListRefundsRequest {
                    status: 0,
                    order_id: "ord-other-usr".into(),
                    page: 1,
                    page_size: 20,
                },
                &tok,
            ))
            .await
            .unwrap_err();
        assert_eq!(
            err_ref.code(),
            tonic::Code::PermissionDenied,
            "ListRefunds other role={role}"
        );
    }

    // 5. Positive matrix check: all 5 staff roles WITH `refunds.read` in capabilities.json
    //    (`cskh`, `ops`, `accountant`, `admin`, `super_admin`) can read another user's payment/SSE/refunds.
    for role in ["cskh", "ops", "accountant", "admin", "super_admin"] {
        let tok = user_jwt(role, &format!("staff-{role}"));
        let ok_pay = grpc
            .get_payment(grpc_req_with_token(
                pb::GetPaymentRequest {
                    payment_id: p_other.id.to_string(),
                    order_group_id: String::new(),
                    order_id: String::new(),
                },
                &tok,
            ))
            .await
            .unwrap_or_else(|e| panic!("role={role} with refunds.read must read payment: {e}"))
            .into_inner()
            .payment
            .unwrap();
        assert!(ok_pay.shop_allocations[0].commission_vnd > 0, "role={role}");

        let ok_ref = grpc
            .list_refunds(grpc_req_with_token(
                pb::ListRefundsRequest {
                    status: 0,
                    order_id: "ord-other-usr".into(),
                    page: 1,
                    page_size: 20,
                },
                &tok,
            ))
            .await
            .unwrap_or_else(|e| panic!("role={role} with refunds.read must list refunds: {e}"))
            .into_inner();
        assert_eq!(ok_ref.total, 1, "role={role}");
    }

    db.cleanup().await;
}

/// P3 Item 1 (core scenarios on real Postgres):
/// 1. `superseded` ZALOPAY where all 3 callbacks were lost (`return_code=1`): discovered by
///    `scan_expired_payments`, routed through the exact late-IPN path (1 balanced ledger tx,
///    1 `late_payment` refund with `provider_m_refund_id`, `provider_final_status='late_refund_queued'`),
///    not re-queried on subsequent ticks, and settles via `poll_zalopay_refunds`.
/// 2. `cancelled` ZALOPAY returning `return_code=2`: concluded `not_paid` and stopped sweeping.
/// 3. `expired` ZALOPAY where expiry `/v2/query` failed with `NetworkError`, then retry also fails
///    with `NetworkError` (exponential backoff enforced), then recovers with `return_code=1` ->
///    1 `late_payment` refund + balanced ledger.
#[tokio::test]
async fn test_pg_p3_item1_zalopay_lost_callbacks_discovered_by_scanner() {
    let Some(db) = TestDb::setup(
        "test_pg_p3_item1_zalopay_lost_callbacks_discovered_by_scanner",
        8,
    )
    .await
    else {
        return;
    };
    let (svc, mock) =
        db.service_with_mock_breaker(Some(CircuitBreaker::new(5, Duration::from_millis(20))));
    let p_zp = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-lost-1".into(),
            order_id: "ord-p3-lost-1".into(),
            user_id: "buyer-lost-1".into(),
            amount_vnd: 320_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p3-lost-1", "shop-1", 320_000),
            expires_at: None,
            idempotency_key: Some("idem-p3-lost-zp".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-lost-1".into(),
        })
        .await
        .unwrap();

    let _p_qr = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-lost-1".into(),
            order_id: "ord-p3-lost-1".into(),
            user_id: "buyer-lost-1".into(),
            amount_vnd: 320_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p3-lost-1", "shop-1", 320_000),
            expires_at: p_zp.payment.expires_at,
            idempotency_key: Some("idem-p3-lost-qr".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-lost-2".into(),
        })
        .await
        .unwrap();

    mock.set_order_outcome(
        &p_zp.payment.provider_order_code,
        MockBehavior::return_code(1),
    );

    // Existing expiration scanner runs even with 0 pending expired payments and sweeps superseded ZALOPAY.
    let _ = svc.scan_expired_payments().await.unwrap();

    let late_refunds: Vec<(Uuid, Option<String>, Option<String>, i64)> = sqlx::query_as(
        "SELECT id, provider_m_refund_id, provider_status, amount_vnd FROM refunds WHERE payment_id = $1 AND is_late_payment = TRUE",
    )
    .bind(p_zp.payment.id)
    .fetch_all(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        late_refunds.len(),
        1,
        "lost-callback paid superseded ZALOPAY payment must be discovered and queued as late_payment refund"
    );
    assert!(
        late_refunds[0].1.is_some(),
        "provider_m_refund_id must be generated"
    );
    assert_eq!(late_refunds[0].2.as_deref(), Some("pending"));
    assert_eq!(late_refunds[0].3, 320_000);

    let final_st: Option<String> =
        sqlx::query_scalar("SELECT provider_final_status FROM payments WHERE id = $1")
            .bind(p_zp.payment.id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(final_st.as_deref(), Some("late_refund_queued"));
    assert_eq!(mock.query_order_calls(&p_zp.payment.provider_order_code), 1);

    // Subsequent scanner tick MUST NOT re-query the concluded payment.
    let _ = svc.scan_expired_payments().await.unwrap();
    assert_eq!(mock.query_order_calls(&p_zp.payment.provider_order_code), 1);

    // Never emit `payment.succeeded` for late-discovered superseded payment.
    let succ_outbox: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'payment.succeeded' AND payload->>'payment_id' = $1",
    )
    .bind(p_zp.payment.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(succ_outbox, 0);

    // Existing refund poller dispatches `/v2/refund` and settles the late refund cleanly.
    let polled = svc
        .poll_zalopay_refunds_with_cooldown(ChronoDuration::zero())
        .await
        .unwrap();
    assert_eq!(polled, 1);
    let settled_st: String = sqlx::query_scalar("SELECT status FROM refunds WHERE id = $1")
        .bind(late_refunds[0].0)
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(settled_st, "completed");

    // --- Case 2: Cancelled ZALOPAY with return_code=2 -> concluded `not_paid`, stops sweeping ---
    let p_cancel = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-cancel-2".into(),
            order_id: "ord-p3-cancel-2".into(),
            user_id: "buyer-cancel-2".into(),
            amount_vnd: 180_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p3-cancel-2", "shop-1", 180_000),
            expires_at: None,
            idempotency_key: Some("idem-p3-cancel-2".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-cancel-2".into(),
        })
        .await
        .unwrap();
    svc.process_order_cancelled_event("evt-p3-cancel-2", "ord-p3-cancel-2", "tr-p3-cancel-evt")
        .await
        .unwrap();
    mock.set_order_outcome(
        &p_cancel.payment.provider_order_code,
        MockBehavior::return_code(2),
    );

    let st_c1 = svc.scan_unsettled_zalopay_payments().await.unwrap();
    assert_eq!(st_c1.claimed, 1);
    assert_eq!(st_c1.concluded_not_paid, 1);
    let cancel_final: Option<String> =
        sqlx::query_scalar("SELECT provider_final_status FROM payments WHERE id = $1")
            .bind(p_cancel.payment.id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(cancel_final.as_deref(), Some("not_paid"));
    assert_eq!(
        mock.query_order_calls(&p_cancel.payment.provider_order_code),
        1
    );

    // Next sweep skips concluded `not_paid` row completely.
    let st_c2 = svc.scan_unsettled_zalopay_payments().await.unwrap();
    assert_eq!(st_c2.claimed, 0);
    assert_eq!(
        mock.query_order_calls(&p_cancel.payment.provider_order_code),
        1
    );

    // --- Case 3: Expired ZALOPAY where expiry query had NetworkError -> backoff retry -> recovers to 1 ---
    let p_exp = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-exp-err".into(),
            order_id: "ord-p3-exp-err".into(),
            user_id: "buyer-exp-err".into(),
            amount_vnd: 410_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p3-exp-err", "shop-1", 410_000),
            expires_at: Some(Utc::now() - ChronoDuration::minutes(2)),
            idempotency_key: Some("idem-p3-exp-err".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-exp-err".into(),
        })
        .await
        .unwrap();
    mock.set_order_outcome(
        &p_exp.payment.provider_order_code,
        MockBehavior::NetworkError("simulated zalopay timeout".into()),
    );

    // Expiry tick: network error expires payment normally, sets attempt=1 + next_check=+60s (NOT final)
    let exp_cnt = svc.scan_expired_payments().await.unwrap();
    assert_eq!(exp_cnt, 1);
    let (exp_status, exp_final, exp_attempts, exp_next): (
        String,
        Option<String>,
        i32,
        Option<chrono::DateTime<Utc>>,
    ) = sqlx::query_as(
        "SELECT status, provider_final_status, provider_check_attempts, provider_next_check_at FROM payments WHERE id = $1",
    )
    .bind(p_exp.payment.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(exp_status, "expired");
    assert_eq!(exp_final, None);
    assert_eq!(exp_attempts, 1);
    let next_at_1 = exp_next.expect("provider_next_check_at must be scheduled");

    // Before backoff elapses -> skipped (0 claimed)
    let before = svc
        .scan_unsettled_zalopay_payments_with_options(
            next_at_1 - ChronoDuration::seconds(5),
            ChronoDuration::hours(24),
            25,
            60,
        )
        .await
        .unwrap();
    assert_eq!(before.claimed, 0);

    // At next_at_1 + 1s: still NetworkError -> attempt=2, backoff doubles to 120s
    let t2 = next_at_1 + ChronoDuration::seconds(1);
    let retry_err = svc
        .scan_unsettled_zalopay_payments_with_options(t2, ChronoDuration::hours(24), 25, 60)
        .await
        .unwrap();
    assert_eq!(retry_err.claimed, 1);
    assert_eq!(retry_err.retried, 1);
    let (attempts_2, next_at_2): (i32, Option<chrono::DateTime<Utc>>) = sqlx::query_as(
        "SELECT provider_check_attempts, provider_next_check_at FROM payments WHERE id = $1",
    )
    .bind(p_exp.payment.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(attempts_2, 2);
    let next_at_2 = next_at_2.unwrap();
    assert_eq!((next_at_2 - t2).num_seconds(), 120);

    // Network recovers & circuit breaker cools down -> ZaloPay reports return_code=1 -> late_payment refund queued once!
    tokio::time::sleep(Duration::from_millis(30)).await;
    mock.set_order_outcome(
        &p_exp.payment.provider_order_code,
        MockBehavior::return_code(1),
    );
    let recovered = svc
        .scan_unsettled_zalopay_payments_with_options(
            next_at_2 + ChronoDuration::seconds(1),
            ChronoDuration::hours(24),
            25,
            60,
        )
        .await
        .unwrap();
    assert_eq!(recovered.claimed, 1);
    assert_eq!(recovered.late_refunds_queued, 1);

    let exp_late_cnt: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM refunds WHERE payment_id = $1 AND is_late_payment = TRUE",
    )
    .bind(p_exp.payment.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(exp_late_cnt, 1);

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary.is_balanced);

    db.cleanup().await;
}

/// P3 Item 1 concurrency & multi-replica races (real Postgres):
/// 1. Sweep || Callback concurrent race on the SAME superseded ZALOPAY payment -> exactly 1
///    `late_payment` refund, exactly 1 balanced `late_payment_refund` ledger tx, callback returns
///    `return_code=1` (`LateRefundQueued` or `Duplicate`).
/// 2. 2-replica concurrent sweep on an unsettled ZALOPAY payment -> `FOR UPDATE SKIP LOCKED` +
///    atomic lease ensures total `claimed == 1`, `/v2/query` called ONCE, exactly 1 late refund.
#[tokio::test]
async fn test_pg_p3_item1_zalopay_late_sweep_races_callback_and_two_replicas() {
    let Some(db) = TestDb::setup(
        "test_pg_p3_item1_zalopay_late_sweep_races_callback_and_two_replicas",
        10,
    )
    .await
    else {
        return;
    };
    let (svc, mock) = db.service_with_mock();

    // --- Race A: Sweep || Callback concurrently on same superseded ZALOPAY payment ---
    let p_race_cb = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-race-cb".into(),
            order_id: "ord-p3-race-cb".into(),
            user_id: "buyer-race-cb".into(),
            amount_vnd: 275_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p3-race-cb", "shop-1", 275_000),
            expires_at: None,
            idempotency_key: Some("idem-p3-race-cb-zp".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-race-cb-1".into(),
        })
        .await
        .unwrap();
    let _ = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-race-cb".into(),
            order_id: "ord-p3-race-cb".into(),
            user_id: "buyer-race-cb".into(),
            amount_vnd: 275_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p3-race-cb", "shop-1", 275_000),
            expires_at: p_race_cb.payment.expires_at,
            idempotency_key: Some("idem-p3-race-cb-qr".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-race-cb-2".into(),
        })
        .await
        .unwrap();

    let app_trans_cb = p_race_cb.payment.provider_order_code.clone();
    let zp_tx_cb = MockZaloPay::deterministic_zp_trans_id(&app_trans_cb);
    mock.set_order_outcome(&app_trans_cb, MockBehavior::return_code(1));
    let signed_cb = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &app_trans_cb,
        275_000,
        zp_tx_cb,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );

    let barrier_cb = Arc::new(Barrier::new(2));
    let svc_sw = svc.clone();
    let b_sw = barrier_cb.clone();
    let h_sw = tokio::spawn(async move {
        b_sw.wait().await;
        svc_sw
            .scan_unsettled_zalopay_payments_with_options(
                Utc::now(),
                ChronoDuration::hours(24),
                25,
                60,
            )
            .await
            .unwrap()
    });
    let svc_cb = svc.clone();
    let b_cb = barrier_cb.clone();
    let h_cb = tokio::spawn(async move {
        b_cb.wait().await;
        svc_cb
            .process_zalopay_callback(signed_cb, "tr-p3-race-cb-wh")
            .await
            .unwrap()
    });
    let (_sw_stats, cb_out) = tokio::join!(h_sw, h_cb);
    let cb_out = cb_out.unwrap();
    assert!(
        matches!(
            cb_out,
            ZaloPayCallbackOutcome::LateRefundQueued { .. }
                | ZaloPayCallbackOutcome::Duplicate { .. }
        ),
        "racing callback must return LateRefundQueued or Duplicate (return_code=1), got {cb_out:?}"
    );

    let ref_cnt_cb: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM refunds WHERE payment_id = $1 AND is_late_payment = TRUE",
    )
    .bind(p_race_cb.payment.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        ref_cnt_cb, 1,
        "exactly 1 late_payment refund under sweep||callback race"
    );

    let ledger_cnt_cb: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM ledger_transactions WHERE idempotency_key = $1")
            .bind(format!("late-webhook-refund:{zp_tx_cb}"))
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(
        ledger_cnt_cb, 1,
        "exactly 1 ledger tx under sweep||callback race"
    );

    let final_cb: Option<String> =
        sqlx::query_scalar("SELECT provider_final_status FROM payments WHERE id = $1")
            .bind(p_race_cb.payment.id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(final_cb.as_deref(), Some("late_refund_queued"));

    // --- Race B: 2-replica concurrent sweep on unsettled superseded ZALOPAY payment ---
    let p_rep = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-2rep".into(),
            order_id: "ord-p3-2rep".into(),
            user_id: "buyer-2rep".into(),
            amount_vnd: 195_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p3-2rep", "shop-1", 195_000),
            expires_at: None,
            idempotency_key: Some("idem-p3-2rep-zp".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-2rep-1".into(),
        })
        .await
        .unwrap();
    let _ = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-2rep".into(),
            order_id: "ord-p3-2rep".into(),
            user_id: "buyer-2rep".into(),
            amount_vnd: 195_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p3-2rep", "shop-1", 195_000),
            expires_at: p_rep.payment.expires_at,
            idempotency_key: Some("idem-p3-2rep-qr".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-2rep-2".into(),
        })
        .await
        .unwrap();
    let app_trans_rep = p_rep.payment.provider_order_code.clone();
    mock.set_order_outcome(&app_trans_rep, MockBehavior::return_code(1));

    let barrier_rep = Arc::new(Barrier::new(2));
    let now_rep = Utc::now();
    let mut rep_handles = Vec::new();
    for _ in 0..2 {
        let s = svc.clone();
        let b = barrier_rep.clone();
        rep_handles.push(tokio::spawn(async move {
            b.wait().await;
            s.scan_unsettled_zalopay_payments_with_options(
                now_rep,
                ChronoDuration::hours(24),
                25,
                60,
            )
            .await
            .unwrap()
        }));
    }
    let st1 = rep_handles.remove(0).await.unwrap();
    let st2 = rep_handles.remove(0).await.unwrap();
    assert_eq!(
        st1.claimed + st2.claimed,
        1,
        "2 replicas racing must claim the row strictly once (got {} + {})",
        st1.claimed,
        st2.claimed
    );
    assert_eq!(st1.late_refunds_queued + st2.late_refunds_queued, 1);
    assert_eq!(
        mock.query_order_calls(&app_trans_rep),
        1,
        "outbound /v2/query must fire strictly once across 2 racing replicas"
    );

    let (rep_attempts, rep_final): (i32, Option<String>) = sqlx::query_as(
        "SELECT provider_check_attempts, provider_final_status FROM payments WHERE id = $1",
    )
    .bind(p_rep.payment.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(rep_attempts, 1);
    assert_eq!(rep_final.as_deref(), Some("late_refund_queued"));

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary.is_balanced);

    db.cleanup().await;
}

/// P3 Item 1 edge cases on real Postgres:
/// 1. Amount mismatch from `/v2/query` (`return_code=1` with wrong amount) -> queued in
///    `reconciliation_queue` (`reason='amount_mismatch'`), 0 refund, 0 ledger tx,
///    `provider_final_status='amount_mismatch'`, stops sweeping.
/// 2. Finite 24h window exhausted with `return_code=3` OR `NetworkError` -> escalated to
///    `reconciliation_queue` (`reason='zalopay_late_sweep_window_exhausted'`) +
///    `provider_final_status='manual_review'`, stops sweeping.
/// 3. Batch limit caps rows claimed per tick.
#[tokio::test]
async fn test_pg_p3_item1_zalopay_late_sweep_amount_mismatch_window_exhausted_and_batch_limit() {
    let Some(db) = TestDb::setup(
        "test_pg_p3_item1_zalopay_late_sweep_amount_mismatch_window_exhausted_and_batch_limit",
        8,
    )
    .await
    else {
        return;
    };
    let (svc, mock) = db.service_with_mock();

    // --- 1. Amount mismatch ---
    let p_mm = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-mm".into(),
            order_id: "ord-p3-mm".into(),
            user_id: "buyer-mm".into(),
            amount_vnd: 500_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p3-mm", "shop-1", 500_000),
            expires_at: None,
            idempotency_key: Some("idem-p3-mm-zp".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-mm-1".into(),
        })
        .await
        .unwrap();
    let _ = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-mm".into(),
            order_id: "ord-p3-mm".into(),
            user_id: "buyer-mm".into(),
            amount_vnd: 500_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p3-mm", "shop-1", 500_000),
            expires_at: p_mm.payment.expires_at,
            idempotency_key: Some("idem-p3-mm-qr".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-mm-2".into(),
        })
        .await
        .unwrap();

    mock.set_order_outcome(
        &p_mm.payment.provider_order_code,
        MockBehavior::ReturnCode {
            return_code: 1,
            sub_return_code: 1,
            return_message: "ok".into(),
            amount_vnd: Some(499_000),
            zp_trans_id: Some(261_124_777_999),
            server_time: None,
            is_processing: None,
        },
    );

    let st_mm = svc
        .scan_unsettled_zalopay_payments_with_options(Utc::now(), ChronoDuration::hours(24), 25, 60)
        .await
        .unwrap();
    assert_eq!(st_mm.claimed, 1);
    assert_eq!(st_mm.amount_mismatches, 1);
    assert_eq!(st_mm.late_refunds_queued, 0);

    let (rq_reason, rq_exp, rq_act): (String, Option<i64>, i64) = sqlx::query_as(
        "SELECT reason, expected_amount_vnd, actual_amount_vnd FROM reconciliation_queue WHERE provider_tx_id = '261124777999'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(rq_reason, "amount_mismatch");
    assert_eq!(rq_exp, Some(500_000));
    assert_eq!(rq_act, 499_000);

    let mm_ref_cnt: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM refunds WHERE payment_id = $1")
        .bind(p_mm.payment.id)
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(mm_ref_cnt, 0);

    let mm_final: Option<String> =
        sqlx::query_scalar("SELECT provider_final_status FROM payments WHERE id = $1")
            .bind(p_mm.payment.id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(mm_final.as_deref(), Some("amount_mismatch"));

    // --- 2. Finite 24h window exhausted (`return_code=3` and `NetworkError`) -> manual_review ---
    let expired_at = Utc::now() - ChronoDuration::hours(25);
    let p_win3 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-win3".into(),
            order_id: "ord-p3-win3".into(),
            user_id: "buyer-win3".into(),
            amount_vnd: 210_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p3-win3", "shop-1", 210_000),
            expires_at: Some(expired_at),
            idempotency_key: Some("idem-p3-win3".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-win3".into(),
        })
        .await
        .unwrap();
    svc.process_order_cancelled_event("evt-p3-win3", "ord-p3-win3", "tr-p3-win3-c")
        .await
        .unwrap();
    mock.set_order_outcome(
        &p_win3.payment.provider_order_code,
        MockBehavior::processing(),
    );

    let p_win_net = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p3-winnet".into(),
            order_id: "ord-p3-winnet".into(),
            user_id: "buyer-winnet".into(),
            amount_vnd: 220_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p3-winnet", "shop-1", 220_000),
            expires_at: Some(expired_at),
            idempotency_key: Some("idem-p3-winnet".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p3-winnet".into(),
        })
        .await
        .unwrap();
    svc.process_order_cancelled_event("evt-p3-winnet", "ord-p3-winnet", "tr-p3-winnet-c")
        .await
        .unwrap();
    mock.set_order_outcome(
        &p_win_net.payment.provider_order_code,
        MockBehavior::NetworkError("persistent zalopay outage".into()),
    );

    let st_win = svc
        .scan_unsettled_zalopay_payments_with_options(Utc::now(), ChronoDuration::hours(24), 25, 60)
        .await
        .unwrap();
    assert_eq!(st_win.claimed, 2);
    assert_eq!(st_win.escalated_manual_review, 2);
    assert_eq!(st_win.retried, 0);

    for pid in [p_win3.payment.id, p_win_net.payment.id] {
        let fst: Option<String> =
            sqlx::query_scalar("SELECT provider_final_status FROM payments WHERE id = $1")
                .bind(pid)
                .fetch_one(&db.pool)
                .await
                .unwrap();
        assert_eq!(fst.as_deref(), Some("manual_review"));

        let rq_cnt: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM reconciliation_queue WHERE expected_payment_id = $1 AND reason = 'zalopay_late_sweep_window_exhausted' AND resolution_status = 'pending'",
        )
        .bind(pid)
        .fetch_one(&db.pool)
        .await
        .unwrap();
        assert_eq!(rq_cnt, 1);
    }

    // Subsequent tick must skip escalated rows.
    let st_after = svc
        .scan_unsettled_zalopay_payments_with_options(
            Utc::now() + ChronoDuration::hours(2),
            ChronoDuration::hours(24),
            25,
            60,
        )
        .await
        .unwrap();
    assert_eq!(st_after.claimed, 0);

    // --- 3. Batch limit enforcement ---
    for i in 1..=3 {
        let p = svc
            .create_payment(CreatePaymentInput {
                order_group_id: format!("grp-p3-lim-{i}"),
                order_id: format!("ord-p3-lim-{i}"),
                user_id: "buyer-lim".into(),
                amount_vnd: 100_000,
                method: PaymentMethod::ZaloPay,
                shop_allocations: single_alloc(&format!("ord-p3-lim-{i}"), "shop-1", 100_000),
                expires_at: None,
                idempotency_key: Some(format!("idem-p3-lim-{i}")),
                supersede_previous: false,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                trace_id: format!("tr-p3-lim-{i}"),
            })
            .await
            .unwrap();
        svc.process_order_cancelled_event(
            &format!("evt-p3-lim-{i}"),
            &format!("ord-p3-lim-{i}"),
            &format!("tr-p3-lim-c-{i}"),
        )
        .await
        .unwrap();
        mock.set_order_outcome(&p.payment.provider_order_code, MockBehavior::return_code(2));
    }

    let b1 = svc
        .scan_unsettled_zalopay_payments_with_options(Utc::now(), ChronoDuration::hours(24), 2, 60)
        .await
        .unwrap();
    assert_eq!(b1.claimed, 2);
    assert_eq!(b1.concluded_not_paid, 2);

    let b2 = svc
        .scan_unsettled_zalopay_payments_with_options(Utc::now(), ChronoDuration::hours(24), 2, 60)
        .await
        .unwrap();
    assert_eq!(b2.claimed, 1);
    assert_eq!(b2.concluded_not_paid, 1);

    let b3 = svc
        .scan_unsettled_zalopay_payments_with_options(Utc::now(), ChronoDuration::hours(24), 2, 60)
        .await
        .unwrap();
    assert_eq!(b3.claimed, 0);

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary.is_balanced);

    db.cleanup().await;
}
