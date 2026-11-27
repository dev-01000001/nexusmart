use std::sync::Arc;
use std::time::Duration;

use axum::body::Body;
use axum::http::{header, Method, Request, StatusCode};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use chrono::{DateTime, Duration as ChronoDuration, Utc};
use futures_util::StreamExt;
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
use nexusmart_payment::config::{Config, VietQrMode};
use nexusmart_payment::domain::{
    vn_today, CodReconciliationRowInput, CreatePaymentInput, CreateRefundInput, GetFeeConfigFilter,
    ImportCodReconciliationInput, ListRefundsFilter, ListSellerLedgerFilter, PaymentMethod,
    ReconciliationActionKind, ResolveReconciliationInput, SellerLedgerAccountFilter,
    SetFeeConfigInput, SettleRefundInput, ShopAllocationInput, SubmitRefundBankAccountInput,
};
use nexusmart_payment::error::PaymentError;
use nexusmart_payment::grpc::pb::payment_service_server::PaymentService as PaymentServiceGrpcTrait;
use nexusmart_payment::grpc::{pb, PaymentGrpcServer};
use nexusmart_payment::http::build_router;
use nexusmart_payment::metrics::Metrics;
use nexusmart_payment::service::{
    PaymentService, FEE_CONFIG_ACTIVE_ALL_SQL, FEE_CONFIG_ACTIVE_BY_METHOD_SQL,
    FEE_CONFIG_HISTORY_BY_METHOD_SQL,
};
use nexusmart_payment::webhook_security::{compute_hmac_hex, IncomingWebhookPayload};

const TEST_USER_SECRET: &str = "pg-test-user-jwt-secret";
const TEST_SERVICE_SECRET: &str = "pg-test-service-jwt-secret";
const TEST_WEBHOOK_SECRET: &str = "pg-test-webhook-secret";

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

        // Ensure pgcrypto extension exists once under an advisory lock so parallel schemas never race.
        EXT_INIT
            .get_or_init(|| async {
                let base = PgPoolOptions::new()
                    .max_connections(2)
                    .acquire_timeout(Duration::from_secs(10))
                    .connect(&db_url)
                    .await
                    .expect("connect base TEST_PAYMENT_DB_URL");
                let mut conn = base.acquire().await.expect("acquire base conn");
                sqlx::query("SELECT pg_advisory_lock(88442211)")
                    .execute(&mut *conn)
                    .await
                    .expect("advisory lock");
                sqlx::query("CREATE EXTENSION IF NOT EXISTS pgcrypto")
                    .execute(&mut *conn)
                    .await
                    .expect("create pgcrypto");
                sqlx::query("SELECT pg_advisory_unlock(88442211)")
                    .execute(&mut *conn)
                    .await
                    .expect("advisory unlock");
                drop(conn);
                base.close().await;
            })
            .await;

        let schema = format!("paytest_{}", Uuid::new_v4().simple());
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

    fn service(&self) -> PaymentService {
        let mut cfg = Config::from_env();
        cfg.database_url = self.admin_url.clone();
        cfg.webhook_secret = TEST_WEBHOOK_SECRET.to_string();
        cfg.jwt_secret = TEST_USER_SECRET.to_string();
        cfg.service_jwt_secret = TEST_SERVICE_SECRET.to_string();
        cfg.vietqr_mode = VietQrMode::Mock;
        cfg.seller_hold_hours = 72;
        PaymentService::new(self.pool.clone(), cfg, Metrics::new()).with_bank_provider(
            std::sync::Arc::new(
                nexusmart_payment::user_bank::StaticPayoutBankProvider::ready(
                    "Vietcombank",
                    "****0123",
                    "NGUYEN VAN SELLER",
                ),
            ),
        )
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

fn grpc_req_with_token<T>(payload: T, token: &str) -> tonic::Request<T> {
    let mut req = tonic::Request::new(payload);
    req.metadata_mut().insert(
        "authorization",
        MetadataValue::try_from(format!("Bearer {token}")).unwrap(),
    );
    req
}

/// Deterministically waits until PostgreSQL reports at least `expected_waiters` active backend(s)
/// blocked specifically on `blocker_pid` (`pg_blocking_pids(pid)`) with `wait_event_type = 'Lock'`
/// and query matching `query_substr` (isolated per-blocker PID so parallel tests never interfere).
async fn wait_for_blocked_by_pid(
    pool: &PgPool,
    blocker_pid: i32,
    query_substr: &str,
    expected_waiters: usize,
) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
    let pattern = format!("%{query_substr}%");
    loop {
        let count: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM pg_stat_activity
            WHERE $1 = ANY(pg_blocking_pids(pid))
              AND wait_event_type = 'Lock'
              AND query ILIKE $2
            "#,
        )
        .bind(blocker_pid)
        .bind(&pattern)
        .fetch_one(pool)
        .await
        .expect("poll pg_stat_activity for lock waiters");
        if count as usize >= expected_waiters {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "timed out waiting for {expected_waiters} Postgres Lock waiter(s) blocked by pid={blocker_pid} matching {query_substr:?} (last count={count})"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

#[allow(clippy::too_many_arguments)]
fn sample_vietqr_input(
    group_id: &str,
    order_id: &str,
    user_id: &str,
    shop_id: &str,
    subtotal_vnd: i64,
    commission_vnd: i64,
    shipping_fee_vnd: i64,
    platform_discount_vnd: i64,
    idempotency_key: Option<String>,
) -> CreatePaymentInput {
    let amount_vnd = subtotal_vnd + shipping_fee_vnd - platform_discount_vnd;
    CreatePaymentInput {
        order_group_id: group_id.to_string(),
        order_id: order_id.to_string(),
        user_id: user_id.to_string(),
        amount_vnd,
        method: PaymentMethod::VietQr,
        shop_allocations: vec![ShopAllocationInput {
            order_id: Some(order_id.to_string()),
            shop_id: shop_id.to_string(),
            subtotal_vnd,
            commission_vnd,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            shipping_fee_vnd,
            platform_discount_vnd,
        }],
        expires_at: Some(Utc::now() + ChronoDuration::minutes(15)),
        idempotency_key,
        supersede_previous: false,
        sales_channel: String::new(),
        storefront_shop_id: String::new(),
        trace_id: Uuid::new_v4().simple().to_string(),
    }
}

fn succeeded_webhook(
    payment_id: Uuid,
    group_id: &str,
    provider_tx_id: &str,
    amount_vnd: i64,
) -> (IncomingWebhookPayload, Value) {
    let raw = json!({
        "provider": "vietqr_mock",
        "provider_tx_id": provider_tx_id,
        "payment_id": payment_id.to_string(),
        "order_group_id": group_id,
        "amount_vnd": amount_vnd,
        "status": "SUCCEEDED",
        "occurred_at": Utc::now().to_rfc3339(),
    });
    let payload: IncomingWebhookPayload = serde_json::from_value(raw.clone()).unwrap();
    (payload, raw)
}

async fn fetch_outbox_payloads(pool: &PgPool, event_type: &str) -> Vec<Value> {
    sqlx::query_scalar::<_, Value>(
        "SELECT payload FROM outbox_events WHERE event_type = $1 ORDER BY seq ASC",
    )
    .bind(event_type)
    .fetch_all(pool)
    .await
    .expect("fetch outbox payloads")
}

// =============================================================================
// 1. D10 (N11): payment.refunded includes accurate is_post_settlement on all paths
// =============================================================================

#[tokio::test]
async fn test_pg_d10_payment_refunded_is_post_settlement_all_paths() {
    let Some(db) = TestDb::setup(
        "test_pg_d10_payment_refunded_is_post_settlement_all_paths",
        8,
    )
    .await
    else {
        return;
    };
    let svc = db.service();

    // Path 1: Pre-settlement admin refund (`held`) -> is_post_settlement == false
    // Path 2: Return refund (`order-return-refund:<order_id>`) before settlement -> is_post_settlement == false
    // Path 3: Post-settlement refund on `pending_release` -> is_post_settlement == true
    // Path 4: Post-settlement refund on `released` -> is_post_settlement == true
    // Path 5: Auto-cancel refund via `order.cancelled` (`held`) -> is_post_settlement == false
    // Path 6: Late webhook refund -> MUST NOT emit `payment.refunded`

    // --- Payment A (2 shops): Shop1 tests pre-settle admin refund + post-settle pending_release + post-settle released;
    //     Shop2 tests return refund (`order-return-refund:*`) pre-settle ---
    let grp_a = format!("grp-d10-a-{}", Uuid::new_v4());
    let ord_a1 = format!("ord-d10-a1-{}", Uuid::new_v4());
    let ord_a2 = format!("ord-d10-a2-{}", Uuid::new_v4());
    let pwa_a = svc
        .create_payment(CreatePaymentInput {
            order_group_id: grp_a.clone(),
            order_id: grp_a.clone(),
            user_id: "buyer-d10".into(),
            amount_vnd: 500_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![
                ShopAllocationInput {
                    order_id: Some(ord_a1.clone()),
                    shop_id: "shop-d10-1".into(),
                    subtotal_vnd: 280_000,
                    commission_vnd: 20_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 0,
                },
                ShopAllocationInput {
                    order_id: Some(ord_a2.clone()),
                    shop_id: "shop-d10-2".into(),
                    subtotal_vnd: 180_000,
                    commission_vnd: 10_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 0,
                },
            ],
            expires_at: Some(Utc::now() + ChronoDuration::minutes(15)),
            idempotency_key: Some(format!("idem-d10-a-{}", Uuid::new_v4())),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: Uuid::new_v4().simple().to_string(),
        })
        .await
        .expect("create payment A");

    let (wh_a, raw_a) = succeeded_webhook(
        pwa_a.payment.id,
        &grp_a,
        &format!("TX-D10-A-{}", Uuid::new_v4()),
        500_000,
    );
    svc.process_verified_webhook(wh_a, raw_a, "vietqr_mock", "trace-d10")
        .await
        .expect("confirm payment A");

    // 1. Pre-settlement admin partial refund on ord_a1 (30,000 VND)
    let rf_pre_admin = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(pwa_a.payment.id),
            order_id: ord_a1.clone(),
            amount_vnd: 30_000,
            reason: "admin_pre_settlement".into(),
            idempotency_key: format!("admin-pre-{}", Uuid::new_v4()),
            trace_id: "trace-d10-1".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .expect("pre-settlement admin refund");

    // 2. Return refund on ord_a2 (`order-return-refund:<ord_a2>`) while still `held`
    let rf_return = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(pwa_a.payment.id),
            order_id: ord_a2.clone(),
            amount_vnd: 200_000,
            reason: "return_approved".into(),
            idempotency_key: format!("order-return-refund:{ord_a2}"),
            trace_id: "trace-d10-2".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .expect("return refund");

    // Settle ord_a1 -> `pending_release` (seller_pending gets 280k - 20k - 30k = 230k)
    svc.process_order_completed_event(
        &format!("evt-comp-{}", Uuid::new_v4()),
        &ord_a1,
        "trace-d10",
    )
    .await
    .expect("settle ord_a1");

    // 3. Post-settlement refund while `pending_release` (40,000 VND)
    let rf_post_pending = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(pwa_a.payment.id),
            order_id: ord_a1.clone(),
            amount_vnd: 40_000,
            reason: "admin_post_pending_release".into(),
            idempotency_key: format!("admin-post-pending-{}", Uuid::new_v4()),
            trace_id: "trace-d10-3".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .expect("post-settlement pending_release refund");

    // Release escrow -> `released` (remaining 190k moves to seller_available)
    svc.release_escrow(&ord_a1, Some("shop-d10-1"))
        .await
        .expect("release escrow ord_a1");

    // 4. Post-settlement refund while `released` (50,000 VND)
    let rf_post_released = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(pwa_a.payment.id),
            order_id: ord_a1.clone(),
            amount_vnd: 50_000,
            reason: "admin_post_released".into(),
            idempotency_key: format!("admin-post-released-{}", Uuid::new_v4()),
            trace_id: "trace-d10-4".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .expect("post-settlement released refund");

    // 5. Auto-cancel refund via `order.cancelled` on succeeded VietQR payment B
    let grp_b = format!("grp-d10-b-{}", Uuid::new_v4());
    let ord_b = format!("ord-d10-b-{}", Uuid::new_v4());
    let pwa_b = svc
        .create_payment(sample_vietqr_input(
            &grp_b,
            &ord_b,
            "buyer-d10",
            "shop-d10-3",
            150_000,
            7_500,
            20_000,
            0,
            Some(format!("idem-d10-b-{}", Uuid::new_v4())),
        ))
        .await
        .expect("create payment B");
    let (wh_b, raw_b) = succeeded_webhook(
        pwa_b.payment.id,
        &grp_b,
        &format!("TX-D10-B-{}", Uuid::new_v4()),
        170_000,
    );
    svc.process_verified_webhook(wh_b, raw_b, "vietqr_mock", "trace-d10-b")
        .await
        .expect("confirm payment B");
    svc.process_order_cancelled_event(
        &format!("evt-can-{}", Uuid::new_v4()),
        &ord_b,
        "trace-d10-5",
    )
    .await
    .expect("auto cancel refund");

    // 6. Late webhook on already-succeeded payment B (different provider_tx_id) -> late_payment refund, NO payment.refunded!
    let (wh_late, raw_late) = succeeded_webhook(
        pwa_b.payment.id,
        &grp_b,
        &format!("TX-D10-LATE-{}", Uuid::new_v4()),
        170_000,
    );
    let late_res = svc
        .process_verified_webhook(wh_late, raw_late, "vietqr_mock", "trace-d10-late")
        .await
        .expect("late webhook refund");
    assert_eq!(late_res.status, "late_payment_refund_queued");

    let events = fetch_outbox_payloads(&db.pool, "payment.refunded").await;
    assert_eq!(
        events.len(),
        5,
        "expected exactly 5 payment.refunded events (late webhook must NOT emit payment.refunded); got {events:?}"
    );

    let find_evt = |refund_id: Uuid| {
        events
            .iter()
            .find(|e| e["refund_id"].as_str() == Some(refund_id.to_string().as_str()))
            .cloned()
            .unwrap_or_else(|| panic!("missing payment.refunded event for refund {refund_id}"))
    };

    assert_eq!(
        find_evt(rf_pre_admin.refund.id)["is_post_settlement"],
        json!(false),
        "pre-settlement admin refund must emit is_post_settlement=false"
    );
    assert_eq!(
        find_evt(rf_return.refund.id)["is_post_settlement"],
        json!(false),
        "pre-settlement return refund must emit is_post_settlement=false"
    );
    assert_eq!(
        find_evt(rf_post_pending.refund.id)["is_post_settlement"],
        json!(true),
        "post-settlement pending_release refund must emit is_post_settlement=true"
    );
    assert_eq!(
        find_evt(rf_post_released.refund.id)["is_post_settlement"],
        json!(true),
        "post-settlement released refund must emit is_post_settlement=true"
    );

    let auto_cancel_evt = events
        .iter()
        .find(|e| e["order_id"].as_str() == Some(ord_b.as_str()))
        .expect("auto-cancel payment.refunded event");
    assert_eq!(
        auto_cancel_evt["is_post_settlement"],
        json!(false),
        "auto-cancel refund must emit is_post_settlement=false"
    );

    let summary = svc.get_ledger_summary(None).await.expect("ledger summary");
    assert!(
        summary.is_balanced,
        "ledger must stay balanced: {summary:?}"
    );

    db.cleanup().await;
}

// =============================================================================
// 2. payment_fee_configs SQL tie-break, boundaries, immutability & EXPLAIN index
// =============================================================================

#[tokio::test]
async fn test_pg_fee_config_index_explain_and_sql_tiebreak() {
    let Some(db) = TestDb::setup("test_pg_fee_config_index_explain_and_sql_tiebreak", 6).await
    else {
        return;
    };
    let svc = db.service();

    // Seed 3,000 rows across COD & VIETQR including tied effective_from timestamps and
    // deliberate NTP clock-skew (earlier seq has a LATER created_at than higher seq!).
    let base_ts: DateTime<Utc> = DateTime::parse_from_rfc3339("2025-01-01T00:00:00Z")
        .unwrap()
        .with_timezone(&Utc);
    sqlx::query(
        r#"
        INSERT INTO payment_fee_configs (payment_method, fee_bps, effective_from, created_by, note, created_at)
        SELECT
            CASE WHEN g % 2 = 0 THEN 'VIETQR' ELSE 'COD' END,
            (g % 500)::INTEGER,
            $1 + make_interval(mins => (g / 3)::INTEGER),
            'seed-admin',
            'bulk-row-' || g,
            $1 + make_interval(mins => g)
        FROM generate_series(1, 3000) AS g
        "#,
    )
    .bind(base_ts)
    .execute(&db.pool)
    .await
    .expect("bulk insert 3000 payment_fee_configs");

    // Insert NTP clock-skew tie-break pair at exact timestamp t_tie:
    // Row 1 (lower seq, 111 bps) has created_at = NOW() + 1 hour (future clock skew!)
    // Row 2 (higher seq, 222 bps) has created_at = NOW() - 1 hour (earlier wall-clock!)
    // SQL MUST pick Row 2 (222 bps) strictly by (effective_from DESC, seq DESC).
    let t_tie: DateTime<Utc> = DateTime::parse_from_rfc3339("2026-06-01T00:00:00Z")
        .unwrap()
        .with_timezone(&Utc);
    sqlx::query(
        r#"
        INSERT INTO payment_fee_configs (payment_method, fee_bps, effective_from, created_by, note, created_at)
        VALUES
            ('VIETQR', 111, $1, 'admin-a', 'older-seq-newer-clock', $1 + INTERVAL '1 hour'),
            ('VIETQR', 222, $1, 'admin-b', 'newer-seq-older-clock', $1 - INTERVAL '1 hour')
        "#,
    )
    .bind(t_tie)
    .execute(&db.pool)
    .await
    .expect("insert NTP skew tie-break rows");

    // Scheduled future rate via service API
    let future_at = Utc::now() + ChronoDuration::days(30);
    let scheduled = svc
        .set_fee_config(SetFeeConfigInput {
            method: PaymentMethod::VietQr,
            fee_bps: 333,
            effective_from: Some(future_at),
            created_by: "admin-future".into(),
            note: "future rate".into(),
        })
        .await
        .expect("set future fee config");
    assert_eq!(scheduled.fee_bps, 333);

    // Real SQL query at t_tie MUST pick 222 bps (higher seq wins over later created_at)
    let at_tie = svc
        .get_fee_config(GetFeeConfigFilter {
            method: Some(PaymentMethod::VietQr),
            include_history: false,
            at_time: Some(t_tie),
        })
        .await
        .expect("get_fee_config at t_tie");
    assert_eq!(at_tie.active_configs.len(), 1);
    assert_eq!(at_tie.active_configs[0].fee_bps, 222);
    assert_eq!(at_tie.active_configs[0].note, "newer-seq-older-clock");

    // 1 second before future_at -> still 222 bps; at future_at -> 333 bps
    let before_future = svc
        .get_fee_config(GetFeeConfigFilter {
            method: Some(PaymentMethod::VietQr),
            include_history: false,
            at_time: Some(future_at - ChronoDuration::seconds(1)),
        })
        .await
        .expect("get_fee_config before future");
    assert_eq!(before_future.active_configs[0].fee_bps, 222);

    let at_future = svc
        .get_fee_config(GetFeeConfigFilter {
            method: Some(PaymentMethod::VietQr),
            include_history: true,
            at_time: Some(future_at),
        })
        .await
        .expect("get_fee_config at future");
    assert_eq!(at_future.active_configs[0].fee_bps, 333);
    assert!(at_future.history.len() > 1500);
    assert!(at_future
        .history
        .iter()
        .all(|r| r.payment_method == "VIETQR"));
    assert_eq!(at_future.history[0].fee_bps, 333);

    // P2-3: Exercise `method = None` (all methods) with both `include_history = false/true`
    // and pre-2020 boundary returning empty active_configs.
    let all_at_tie = svc
        .get_fee_config(GetFeeConfigFilter {
            method: None,
            include_history: false,
            at_time: Some(t_tie),
        })
        .await
        .expect("get_fee_config method=None at t_tie");
    assert_eq!(all_at_tie.active_configs.len(), 3);
    assert_eq!(all_at_tie.active_configs[0].payment_method, "COD");
    assert_eq!(all_at_tie.active_configs[1].payment_method, "VIETQR");
    assert_eq!(all_at_tie.active_configs[1].fee_bps, 222);
    assert_eq!(all_at_tie.active_configs[2].payment_method, "ZALOPAY");
    assert_eq!(all_at_tie.active_configs[2].fee_bps, 0);
    assert!(all_at_tie.history.is_empty());

    let all_with_history = svc
        .get_fee_config(GetFeeConfigFilter {
            method: None,
            include_history: true,
            at_time: Some(future_at),
        })
        .await
        .expect("get_fee_config method=None include_history=true");
    assert_eq!(all_with_history.active_configs.len(), 3);
    assert_eq!(all_with_history.active_configs[1].fee_bps, 333);
    assert_eq!(all_with_history.active_configs[2].payment_method, "ZALOPAY");
    assert_eq!(all_with_history.active_configs[2].fee_bps, 0);
    assert!(all_with_history.history.len() > 3000);
    assert!(all_with_history
        .history
        .iter()
        .any(|r| r.payment_method == "COD"));
    assert!(all_with_history
        .history
        .iter()
        .any(|r| r.payment_method == "VIETQR"));
    assert!(all_with_history
        .history
        .iter()
        .any(|r| r.payment_method == "ZALOPAY"));
    assert_eq!(all_with_history.history[0].fee_bps, 333);

    let before_2020: DateTime<Utc> = DateTime::parse_from_rfc3339("2019-01-01T00:00:00Z")
        .unwrap()
        .with_timezone(&Utc);
    let empty_all = svc
        .get_fee_config(GetFeeConfigFilter {
            method: None,
            include_history: false,
            at_time: Some(before_2020),
        })
        .await
        .expect("get_fee_config method=None before 2020");
    assert!(empty_all.active_configs.is_empty());
    assert!(empty_all.history.is_empty());

    // Immutability trigger blocks UPDATE and DELETE in Postgres
    let upd_err = sqlx::query("UPDATE payment_fee_configs SET fee_bps = 999 WHERE id = $1")
        .bind(scheduled.id)
        .execute(&db.pool)
        .await
        .unwrap_err();
    assert!(
        upd_err.to_string().contains("append-only"),
        "expected append-only trigger error, got {upd_err}"
    );
    let del_err = sqlx::query("DELETE FROM payment_fee_configs WHERE id = $1")
        .bind(scheduled.id)
        .execute(&db.pool)
        .await
        .unwrap_err();
    assert!(
        del_err.to_string().contains("append-only"),
        "expected append-only trigger error, got {del_err}"
    );

    // P2-3 & P3-11: ANALYZE and verify EXPLAIN (under `force_generic_plan` prepared statements
    // against the EXACT SQL constants used by `PaymentService::get_fee_config`) uses
    // `idx_payment_fee_configs_method_effective_seq` with ZERO Sort for single-method active,
    // all-methods DISTINCT ON active, and single-method history!
    sqlx::query("ANALYZE payment_fee_configs")
        .execute(&db.pool)
        .await
        .expect("analyze payment_fee_configs");

    let mut explain_conn = db.pool.acquire().await.expect("acquire explain conn");
    sqlx::query("SET plan_cache_mode = force_generic_plan")
        .execute(&mut *explain_conn)
        .await
        .expect("force generic plan");

    // 1. Single-method active query (`FEE_CONFIG_ACTIVE_BY_METHOD_SQL`)
    sqlx::query(&format!(
        "PREPARE fee_active_one(TEXT, TIMESTAMPTZ) AS {FEE_CONFIG_ACTIVE_BY_METHOD_SQL}"
    ))
    .execute(&mut *explain_conn)
    .await
    .expect("prepare fee_active_one");
    let plan_single: Vec<String> = sqlx::query_scalar(&format!(
        "EXPLAIN (ANALYZE, FORMAT TEXT) EXECUTE fee_active_one('VIETQR', '{}')",
        t_tie.to_rfc3339()
    ))
    .fetch_all(&mut *explain_conn)
    .await
    .expect("explain single method");
    let plan_single_text = plan_single.join("\n");
    eprintln!("=== EXPLAIN fee_active_one (generic) ===\n{plan_single_text}");
    assert!(
        plan_single_text.contains("idx_payment_fee_configs_method_effective_seq"),
        "single-method active query must use aligned index under generic plan:\n{plan_single_text}"
    );
    assert!(
        !plan_single_text.contains("Sort"),
        "single-method active query must not require Sort:\n{plan_single_text}"
    );

    // 2. All-methods DISTINCT ON active query (`FEE_CONFIG_ACTIVE_ALL_SQL`)
    // Runs on all methods without LIMIT; on a 3,000-row table where 99.9% of rows satisfy
    // `effective_from <= $1`, Postgres's cost model legitimately selects `Unique -> Sort -> Seq Scan`
    // (or `Index Scan` when `enable_sort = off`), returning exactly 2 rows (`COD`, `VIETQR`).
    sqlx::query(&format!(
        "PREPARE fee_active_all(TIMESTAMPTZ) AS {FEE_CONFIG_ACTIVE_ALL_SQL}"
    ))
    .execute(&mut *explain_conn)
    .await
    .expect("prepare fee_active_all");
    let plan_all: Vec<String> = sqlx::query_scalar(&format!(
        "EXPLAIN (ANALYZE, FORMAT TEXT) EXECUTE fee_active_all('{}')",
        t_tie.to_rfc3339()
    ))
    .fetch_all(&mut *explain_conn)
    .await
    .expect("explain all methods");
    let plan_all_text = plan_all.join("\n");
    eprintln!("=== EXPLAIN fee_active_all (generic, default costs) ===\n{plan_all_text}");
    assert!(
        plan_all_text.contains("Unique") && plan_all_text.contains("rows=3"),
        "all-methods DISTINCT ON query must plan Unique returning 3 rows (COD, VIETQR, ZALOPAY):\n{plan_all_text}"
    );

    // 3. Single-method history query (`FEE_CONFIG_HISTORY_BY_METHOD_SQL` — P3-11):
    // Under `force_generic_plan`, because `payment_method` has 50% selectivity across 2 methods on
    // a 3k-row table, Postgres's cost model prefers Seq Scan + Sort at default costs, so disable
    // seq/bitmap scan locally to prove the generic plan supports `Index Cond: (payment_method = $1)`
    // with ZERO Sort (impossible under the old `($1::TEXT IS NULL OR payment_method = $1)` query).
    sqlx::query("SET enable_seqscan = off")
        .execute(&mut *explain_conn)
        .await
        .expect("disable seqscan");
    sqlx::query("SET enable_bitmapscan = off")
        .execute(&mut *explain_conn)
        .await
        .expect("disable bitmapscan");
    sqlx::query(&format!(
        "PREPARE fee_history_one(TEXT) AS {FEE_CONFIG_HISTORY_BY_METHOD_SQL}"
    ))
    .execute(&mut *explain_conn)
    .await
    .expect("prepare fee_history_one");
    let plan_hist: Vec<String> =
        sqlx::query_scalar("EXPLAIN (ANALYZE, FORMAT TEXT) EXECUTE fee_history_one('VIETQR')")
            .fetch_all(&mut *explain_conn)
            .await
            .expect("explain single method history");
    sqlx::query("RESET enable_seqscan")
        .execute(&mut *explain_conn)
        .await
        .expect("reset enable_seqscan");
    sqlx::query("RESET enable_bitmapscan")
        .execute(&mut *explain_conn)
        .await
        .expect("reset enable_bitmapscan");
    sqlx::query("RESET plan_cache_mode")
        .execute(&mut *explain_conn)
        .await
        .expect("reset plan_cache_mode");
    let plan_hist_text = plan_hist.join("\n");
    eprintln!("=== EXPLAIN fee_history_one (generic, index plan) ===\n{plan_hist_text}");
    assert!(
        plan_hist_text.contains("idx_payment_fee_configs_method_effective_seq")
            && plan_hist_text.contains("Index Cond: (payment_method = $1)"),
        "single-method history query must support Index Cond (payment_method = $1) under generic plan:\n{plan_hist_text}"
    );
    assert!(
        !plan_hist_text.contains("Sort"),
        "single-method history index plan must not require Sort:\n{plan_hist_text}"
    );
    drop(explain_conn);

    db.cleanup().await;
}

// =============================================================================
// 3. CreatePayment 23505 races: same group, both succeeded unique indexes, 0 VND
// =============================================================================

#[tokio::test]
async fn test_pg_create_payment_23505_races_and_zero_vnd() {
    let Some(db) = TestDb::setup("test_pg_create_payment_23505_races_and_zero_vnd", 16).await
    else {
        return;
    };
    let svc = db.service();

    // --- Case A: Deterministic 23505 on `payments_idempotency_key_key` (non-0 VND) ---
    // Hold an uncommitted INSERT with the same idempotency_key so create_payment bypasses both
    // pre-INSERT reads and blocks inside Postgres INSERT until commit -> guaranteed 23505!
    {
        let grp = format!("grp-23505-idem-{}", Uuid::new_v4());
        let ord = format!("ord-23505-idem-{}", Uuid::new_v4());
        let idem = format!("idem-23505-{}", Uuid::new_v4());
        let winning_id = Uuid::new_v4();

        let mut blocker = db.pool.begin().await.unwrap();
        let blocker_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
            .fetch_one(&mut *blocker)
            .await
            .unwrap();
        sqlx::query(
            r#"
            INSERT INTO payments (
                id, order_group_id, order_id, user_id, method, provider,
                amount_vnd, status, provider_order_code, idempotency_key, trace_id
            )
            VALUES ($1, $2, $3, 'buyer-race', 'VIETQR', 'vietqr_mock', 250000, 'pending', $4, $5, 't1')
            "#,
        )
        .bind(winning_id)
        .bind(&grp)
        .bind(&ord)
        .bind(format!("NM{}", &winning_id.simple().to_string()[..10]))
        .bind(&idem)
        .execute(&mut *blocker)
        .await
        .unwrap();
        sqlx::query(
            r#"
            INSERT INTO payment_shop_allocations (
                payment_id, order_id, shop_id, subtotal_vnd, commission_vnd, shipping_fee_vnd, platform_discount_vnd, escrow_status
            )
            VALUES ($1, $2, 'shop-race', 220000, 11000, 30000, 0, 'awaiting_collection')
            "#,
        )
        .bind(winning_id)
        .bind(&ord)
        .execute(&mut *blocker)
        .await
        .unwrap();

        let svc_match = svc.clone();
        let input_match = sample_vietqr_input(
            &grp,
            &ord,
            "buyer-race",
            "shop-race",
            220_000,
            11_000,
            30_000,
            0,
            Some(idem.clone()),
        );
        let match_handle = tokio::spawn(async move { svc_match.create_payment(input_match).await });

        let svc_mismatch = svc.clone();
        let input_mismatch = sample_vietqr_input(
            &grp,
            &ord,
            "other-buyer-attacker",
            "shop-race",
            220_000,
            11_000,
            30_000,
            0,
            Some(idem.clone()),
        );
        let mismatch_handle =
            tokio::spawn(async move { svc_mismatch.create_payment(input_mismatch).await });

        // P2-4: Deterministically wait until Postgres confirms BOTH tasks are blocked inside
        // `INSERT INTO payments` on `blocker_pid`'s uncommitted unique index lock -> guaranteed 23505!
        wait_for_blocked_by_pid(&db.pool, blocker_pid, "INSERT INTO payments", 2).await;
        blocker.commit().await.unwrap();

        let matched = match_handle
            .await
            .unwrap()
            .expect("matching 23505 race must recover and return winning payment");
        assert_eq!(matched.payment.id, winning_id);
        assert_eq!(matched.allocations.len(), 1);

        let mismatch_err = mismatch_handle
            .await
            .unwrap()
            .expect_err("mismatched caller on 23505 must return Conflict");
        assert!(
            matches!(mismatch_err, PaymentError::Conflict(_)),
            "expected Conflict, got {mismatch_err:?}"
        );
    }

    // --- Case B: Deterministic 23505 on BOTH partial unique succeeded indexes (0 VND) ---
    // B1: `idx_payments_one_succeeded_per_order` (same order_id, different order_group_id & idempotency_key)
    {
        let ord = format!("ord-uniq-order-{}", Uuid::new_v4());
        let grp1 = format!("grp-uniq-o1-{}", Uuid::new_v4());
        let grp2 = format!("grp-uniq-o2-{}", Uuid::new_v4());
        let win_id = Uuid::new_v4();

        let mut blocker = db.pool.begin().await.unwrap();
        let blocker_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
            .fetch_one(&mut *blocker)
            .await
            .unwrap();
        sqlx::query(
            r#"
            INSERT INTO payments (
                id, order_group_id, order_id, user_id, method, provider,
                amount_vnd, status, provider_order_code, idempotency_key, paid_at, trace_id
            )
            VALUES ($1, $2, $3, 'buyer-z', 'VIETQR', 'zero_payable', 0, 'succeeded', $4, $5, NOW(), 'tz')
            "#,
        )
        .bind(win_id)
        .bind(&grp1)
        .bind(&ord)
        .bind(format!("NM{}", &win_id.simple().to_string()[..10]))
        .bind(format!("idem-z1-{}", Uuid::new_v4()))
        .execute(&mut *blocker)
        .await
        .unwrap();

        let svc_c = svc.clone();
        let input2 = sample_vietqr_input(
            &grp2,
            &ord, // collides on idx_payments_one_succeeded_per_order!
            "buyer-z",
            "shop-z",
            100_000,
            5_000,
            20_000,
            120_000, // P = 0 VND -> status='succeeded'
            Some(format!("idem-z2-{}", Uuid::new_v4())),
        );
        let h = tokio::spawn(async move { svc_c.create_payment(input2).await });

        wait_for_blocked_by_pid(&db.pool, blocker_pid, "INSERT INTO payments", 1).await;
        blocker.commit().await.unwrap();

        let err = h
            .await
            .unwrap()
            .expect_err("idx_payments_one_succeeded_per_order 23505 must return Conflict");
        assert!(
            matches!(err, PaymentError::Conflict(_)),
            "expected Conflict (not Database 500), got {err:?}"
        );
    }

    // B2: `idx_payments_one_succeeded_per_group` (same order_group_id, different order_id & idempotency_key)
    {
        let grp = format!("grp-uniq-group-{}", Uuid::new_v4());
        let ord1 = format!("ord-uniq-g1-{}", Uuid::new_v4());
        let ord2 = format!("ord-uniq-g2-{}", Uuid::new_v4());
        let win_id = Uuid::new_v4();

        let mut blocker = db.pool.begin().await.unwrap();
        let blocker_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
            .fetch_one(&mut *blocker)
            .await
            .unwrap();
        sqlx::query(
            r#"
            INSERT INTO payments (
                id, order_group_id, order_id, user_id, method, provider,
                amount_vnd, status, provider_order_code, idempotency_key, paid_at, trace_id
            )
            VALUES ($1, $2, $3, 'buyer-z', 'VIETQR', 'zero_payable', 0, 'succeeded', $4, $5, NOW(), 'tz')
            "#,
        )
        .bind(win_id)
        .bind(&grp)
        .bind(&ord1)
        .bind(format!("NM{}", &win_id.simple().to_string()[..10]))
        .bind(format!("idem-zg1-{}", Uuid::new_v4()))
        .execute(&mut *blocker)
        .await
        .unwrap();

        let svc_c = svc.clone();
        let input2 = sample_vietqr_input(
            &grp, // collides on idx_payments_one_succeeded_per_group!
            &ord2,
            "buyer-z",
            "shop-z",
            100_000,
            5_000,
            20_000,
            120_000, // P = 0 VND -> status='succeeded'
            Some(format!("idem-zg2-{}", Uuid::new_v4())),
        );
        let h = tokio::spawn(async move { svc_c.create_payment(input2).await });

        wait_for_blocked_by_pid(&db.pool, blocker_pid, "INSERT INTO payments", 1).await;
        blocker.commit().await.unwrap();

        let err = h
            .await
            .unwrap()
            .expect_err("idx_payments_one_succeeded_per_group 23505 must return Conflict");
        assert!(
            matches!(err, PaymentError::Conflict(_)),
            "expected Conflict (not Database 500), got {err:?}"
        );
    }

    // --- Case C: 10 concurrent 0 VND CreatePayment calls (same idempotency_key) -> all return same succeeded payment ---
    {
        let grp = format!("grp-z-same-{}", Uuid::new_v4());
        let ord = format!("ord-z-same-{}", Uuid::new_v4());
        let idem = format!("checkout:{grp}");
        let barrier = Arc::new(Barrier::new(10));
        let mut handles = Vec::with_capacity(10);

        for _ in 0..10 {
            let s = svc.clone();
            let b = barrier.clone();
            let inp = sample_vietqr_input(
                &grp,
                &ord,
                "buyer-z-conc",
                "shop-z",
                200_000,
                10_000,
                20_000,
                220_000,
                Some(idem.clone()),
            );
            handles.push(tokio::spawn(async move {
                b.wait().await;
                s.create_payment(inp).await
            }));
        }

        let mut ids = Vec::new();
        for h in handles {
            let res = h
                .await
                .unwrap()
                .expect("concurrent 0-VND same idempotency_key must succeed");
            assert_eq!(res.payment.status, "succeeded");
            assert_eq!(res.payment.amount_vnd, 0);
            assert_eq!(res.allocations.len(), 1);
            ids.push(res.payment.id);
        }
        assert!(ids.iter().all(|id| *id == ids[0]));

        let row_count: i64 =
            sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM payments WHERE order_group_id = $1")
                .bind(&grp)
                .fetch_one(&db.pool)
                .await
                .unwrap();
        assert_eq!(row_count, 1);
    }

    // --- Case D: 10 concurrent 0 VND CreatePayment calls (DIFFERENT idempotency_keys) -> strictly 1 succeeds, 9 Conflict ---
    {
        let grp = format!("grp-z-diff-{}", Uuid::new_v4());
        let ord = format!("ord-z-diff-{}", Uuid::new_v4());
        let barrier = Arc::new(Barrier::new(10));
        let mut handles = Vec::with_capacity(10);

        for i in 0..10 {
            let s = svc.clone();
            let b = barrier.clone();
            let inp = sample_vietqr_input(
                &grp,
                &ord,
                "buyer-z-diff",
                "shop-z",
                200_000,
                10_000,
                20_000,
                220_000,
                Some(format!("idem-z-diff-{i}-{}", Uuid::new_v4())),
            );
            handles.push(tokio::spawn(async move {
                b.wait().await;
                s.create_payment(inp).await
            }));
        }

        let mut ok_count = 0usize;
        let mut conflict_count = 0usize;
        for h in handles {
            match h.await.unwrap() {
                Ok(pwa) => {
                    assert_eq!(pwa.payment.status, "succeeded");
                    ok_count += 1;
                }
                Err(PaymentError::Conflict(_)) => conflict_count += 1,
                Err(other) => panic!("unexpected error on concurrent 0-VND race: {other:?}"),
            }
        }
        assert_eq!(ok_count, 1, "strictly 1 winner on concurrent 0-VND group");
        assert_eq!(conflict_count, 9, "all 9 losers must get Conflict");
    }

    db.cleanup().await;
}

// =============================================================================
// 4. 20x parallel identical webhooks -> strictly 1 ledger transaction & balanced
// =============================================================================

#[tokio::test]
async fn test_pg_webhook_20x_parallel_single_ledger_tx() {
    let Some(db) = TestDb::setup("test_pg_webhook_20x_parallel_single_ledger_tx", 24).await else {
        return;
    };
    let svc = db.service();

    let grp = format!("grp-wh20-{}", Uuid::new_v4());
    let ord = format!("ord-wh20-{}", Uuid::new_v4());
    let pwa = svc
        .create_payment(sample_vietqr_input(
            &grp,
            &ord,
            "buyer-wh20",
            "shop-wh20",
            220_000,
            11_000,
            30_000,
            0,
            Some(format!("idem-wh20-{}", Uuid::new_v4())),
        ))
        .await
        .expect("create pending VietQR");

    let tx_id = format!("TX-20X-{}", Uuid::new_v4());
    let barrier = Arc::new(Barrier::new(20));
    let mut handles = Vec::with_capacity(20);

    for i in 0..20 {
        let s = svc.clone();
        let b = barrier.clone();
        let pid = pwa.payment.id;
        let g = grp.clone();
        let t = tx_id.clone();
        handles.push(tokio::spawn(async move {
            let (payload, raw) = succeeded_webhook(pid, &g, &t, 250_000);
            b.wait().await;
            s.process_verified_webhook(payload, raw, "vietqr_mock", &format!("trace-wh20-{i}"))
                .await
        }));
    }

    let mut fresh = 0usize;
    let mut dup = 0usize;
    for h in handles {
        let res = h
            .await
            .unwrap()
            .expect("all 20 parallel webhooks must return Ok");
        if res.duplicate {
            assert_eq!(res.status, "duplicate_ignored");
            dup += 1;
        } else {
            assert_eq!(res.status, "processed");
            fresh += 1;
        }
    }
    assert_eq!(fresh, 1, "strictly 1 fresh webhook processing");
    assert_eq!(dup, 19, "strictly 19 idempotent duplicates");

    let wh_rows: i64 = sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM webhook_events")
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(wh_rows, 1);

    let ledger_tx_rows: i64 =
        sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM ledger_transactions")
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(ledger_tx_rows, 1);

    let outbox_rows: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM outbox_events WHERE event_type = 'payment.succeeded'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(outbox_rows, 1);

    let summary = svc.get_ledger_summary(None).await.expect("ledger summary");
    assert!(summary.is_balanced);
    assert_eq!(summary.total_debits_vnd, 250_000);
    assert_eq!(summary.total_credits_vnd, 250_000);
    assert_eq!(summary.transaction_count, 1);

    // Also test 2 concurrent webhooks with DIFFERENT provider_tx_ids racing on a single pending payment:
    // strictly 1 confirms, 1 queues automatic late_payment refund, zero double confirm!
    let grp2 = format!("grp-wh2diff-{}", Uuid::new_v4());
    let ord2 = format!("ord-wh2diff-{}", Uuid::new_v4());
    let pwa2 = svc
        .create_payment(sample_vietqr_input(
            &grp2,
            &ord2,
            "buyer-wh2",
            "shop-wh2",
            100_000,
            5_000,
            20_000,
            0,
            Some(format!("idem-wh2-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let b2 = Arc::new(Barrier::new(2));
    let mut h2 = Vec::new();
    for i in 0..2 {
        let s = svc.clone();
        let b = b2.clone();
        let pid = pwa2.payment.id;
        let g = grp2.clone();
        h2.push(tokio::spawn(async move {
            let (p, raw) =
                succeeded_webhook(pid, &g, &format!("TX-DIFF-{i}-{}", Uuid::new_v4()), 120_000);
            b.wait().await;
            s.process_verified_webhook(p, raw, "vietqr_mock", "trace-diff")
                .await
        }));
    }
    let mut statuses: Vec<String> = Vec::new();
    for h in h2 {
        statuses.push(h.await.unwrap().unwrap().status);
    }
    statuses.sort();
    assert_eq!(statuses, vec!["late_payment_refund_queued", "processed"]);

    let summary2 = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary2.is_balanced);

    db.cleanup().await;
}

// =============================================================================
// 5. Concurrent refund races (idempotency, cap, post-settlement, cancel vs settle)
// =============================================================================

#[tokio::test]
async fn test_pg_concurrent_refund_races() {
    let Some(db) = TestDb::setup("test_pg_concurrent_refund_races", 16).await else {
        return;
    };
    let svc = db.service();

    // --- Race A: 10 concurrent CreateRefund with SAME idempotency_key -> 1 fresh + 9 replays ---
    let grp_a = format!("grp-rf-idem-{}", Uuid::new_v4());
    let ord_a = format!("ord-rf-idem-{}", Uuid::new_v4());
    let pwa_a = svc
        .create_payment(sample_vietqr_input(
            &grp_a,
            &ord_a,
            "buyer-rf",
            "shop-rf-a",
            200_000,
            10_000,
            0,
            0,
            Some(format!("idem-p-a-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let (wh, raw) = succeeded_webhook(
        pwa_a.payment.id,
        &grp_a,
        &format!("TX-RF-A-{}", Uuid::new_v4()),
        200_000,
    );
    svc.process_verified_webhook(wh, raw, "vietqr_mock", "trace-rf")
        .await
        .unwrap();

    let shared_rf_key = format!("rf-shared-{}", Uuid::new_v4());
    let b_idem = Arc::new(Barrier::new(10));
    let mut h_idem = Vec::new();
    for i in 0..10 {
        let s = svc.clone();
        let b = b_idem.clone();
        let pid = pwa_a.payment.id;
        let oid = ord_a.clone();
        let key = shared_rf_key.clone();
        h_idem.push(tokio::spawn(async move {
            b.wait().await;
            s.create_refund(CreateRefundInput {
                payment_id: Some(pid),
                order_id: oid,
                amount_vnd: 50_000,
                reason: "race_same_key".into(),
                idempotency_key: key,
                trace_id: format!("tr-{i}"),
                require_approval_if_large: false,
                requested_by: None,
                created_by_service: false,
            })
            .await
        }));
    }
    let mut created_fresh = 0usize;
    let mut existed = 0usize;
    let mut rf_ids = Vec::new();
    for h in h_idem {
        let out = h.await.unwrap().expect("same-key refund race must succeed");
        rf_ids.push(out.refund.id);
        if out.already_existed {
            existed += 1;
        } else {
            created_fresh += 1;
        }
    }
    assert_eq!(created_fresh, 1);
    assert_eq!(existed, 9);
    assert!(rf_ids.iter().all(|id| *id == rf_ids[0]));

    // --- Race B: 8 concurrent CreateRefund with DISTINCT keys each asking 60,000 VND (remaining cap = 150,000 VND)
    //     -> strictly 2 succeed (120,000 VND), 6 fail with RefundExceedsPaid! ---
    let b_cap = Arc::new(Barrier::new(8));
    let mut h_cap = Vec::new();
    for i in 0..8 {
        let s = svc.clone();
        let b = b_cap.clone();
        let pid = pwa_a.payment.id;
        let oid = ord_a.clone();
        h_cap.push(tokio::spawn(async move {
            b.wait().await;
            s.create_refund(CreateRefundInput {
                payment_id: Some(pid),
                order_id: oid,
                amount_vnd: 60_000,
                reason: "race_cap".into(),
                idempotency_key: format!("rf-cap-{i}-{}", Uuid::new_v4()),
                trace_id: format!("tr-cap-{i}"),
                require_approval_if_large: false,
                requested_by: None,
                created_by_service: false,
            })
            .await
        }));
    }
    let mut cap_ok = 0usize;
    let mut cap_exceeded = 0usize;
    for h in h_cap {
        match h.await.unwrap() {
            Ok(_) => cap_ok += 1,
            Err(PaymentError::RefundExceedsPaid { .. }) => cap_exceeded += 1,
            Err(e) => panic!("unexpected error in cap race: {e:?}"),
        }
    }
    assert_eq!(cap_ok, 2, "only 2x60k=120k fits within remaining 150k");
    assert_eq!(cap_exceeded, 6);

    // --- Race C: Concurrent post-settlement refund vs seller balance (pending = 90,000 VND; 6x 50,000 VND)
    //     -> strictly 1 succeeds, 5 fail with InsufficientBalance! ---
    let grp_c = format!("grp-rf-post-{}", Uuid::new_v4());
    let ord_c = format!("ord-rf-post-{}", Uuid::new_v4());
    let shop_c = format!("shop-rf-post-{}", Uuid::new_v4());
    let pwa_c = svc
        .create_payment(sample_vietqr_input(
            &grp_c,
            &ord_c,
            "buyer-rf-c",
            &shop_c,
            100_000,
            10_000,
            20_000,
            0,
            Some(format!("idem-p-c-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let (wh_c, raw_c) = succeeded_webhook(
        pwa_c.payment.id,
        &grp_c,
        &format!("TX-RF-C-{}", Uuid::new_v4()),
        120_000,
    );
    svc.process_verified_webhook(wh_c, raw_c, "vietqr_mock", "trace-c")
        .await
        .unwrap();
    svc.process_order_completed_event(&format!("evt-comp-c-{}", Uuid::new_v4()), &ord_c, "trace-c")
        .await
        .unwrap();

    let b_post = Arc::new(Barrier::new(6));
    let mut h_post = Vec::new();
    for i in 0..6 {
        let s = svc.clone();
        let b = b_post.clone();
        let pid = pwa_c.payment.id;
        let oid = ord_c.clone();
        h_post.push(tokio::spawn(async move {
            b.wait().await;
            s.create_refund(CreateRefundInput {
                payment_id: Some(pid),
                order_id: oid,
                amount_vnd: 50_000,
                reason: "race_post_settlement".into(),
                idempotency_key: format!("rf-post-{i}-{}", Uuid::new_v4()),
                trace_id: format!("tr-post-{i}"),
                require_approval_if_large: false,
                requested_by: None,
                created_by_service: false,
            })
            .await
        }));
    }
    let mut post_ok = 0usize;
    let mut post_insuff = 0usize;
    for h in h_post {
        match h.await.unwrap() {
            Ok(_) => post_ok += 1,
            Err(PaymentError::InsufficientBalance { .. }) => post_insuff += 1,
            Err(e) => panic!("unexpected error in post-settlement race: {e:?}"),
        }
    }
    assert_eq!(post_ok, 1, "seller_pending=90k can only cover 1x50k refund");
    assert_eq!(post_insuff, 5);
    let bal_c = svc.get_seller_balance(&shop_c).await.unwrap();
    assert_eq!(bal_c.pending_vnd, 40_000);
    assert_eq!(bal_c.total_earned_vnd, 40_000);

    // --- Race D: Concurrent CreateRefund vs order.completed settlement -> is_post_settlement matches ledger! ---
    let grp_d = format!("grp-rf-settle-{}", Uuid::new_v4());
    let ord_d = format!("ord-rf-settle-{}", Uuid::new_v4());
    let shop_d = format!("shop-rf-settle-{}", Uuid::new_v4());
    let pwa_d = svc
        .create_payment(sample_vietqr_input(
            &grp_d,
            &ord_d,
            "buyer-rf-d",
            &shop_d,
            200_000,
            10_000,
            20_000,
            0,
            Some(format!("idem-p-d-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let (wh_d, raw_d) = succeeded_webhook(
        pwa_d.payment.id,
        &grp_d,
        &format!("TX-RF-D-{}", Uuid::new_v4()),
        220_000,
    );
    svc.process_verified_webhook(wh_d, raw_d, "vietqr_mock", "trace-d")
        .await
        .unwrap();

    let b_race = Arc::new(Barrier::new(2));
    let s1 = svc.clone();
    let b1 = b_race.clone();
    let pid_d = pwa_d.payment.id;
    let oid_d1 = ord_d.clone();
    let h_rf = tokio::spawn(async move {
        b1.wait().await;
        s1.create_refund(CreateRefundInput {
            payment_id: Some(pid_d),
            order_id: oid_d1,
            amount_vnd: 30_000,
            reason: "race_with_settle".into(),
            idempotency_key: format!("rf-race-settle-{}", Uuid::new_v4()),
            trace_id: "tr-race-rf".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
    });
    let s2 = svc.clone();
    let b2 = b_race.clone();
    let oid_d2 = ord_d.clone();
    let h_comp = tokio::spawn(async move {
        b2.wait().await;
        s2.process_order_completed_event(
            &format!("evt-race-comp-{}", Uuid::new_v4()),
            &oid_d2,
            "tr-race-comp",
        )
        .await
    });
    let rf_res = h_rf
        .await
        .unwrap()
        .expect("refund in settle race must succeed");
    h_comp
        .await
        .unwrap()
        .expect("order.completed in settle race must succeed");

    // Verify outbox `is_post_settlement` matches the ACTUAL ledger debit account (escrow vs seller_pending)!
    let debit_acct: String = sqlx::query_scalar(
        r#"
        SELECT e.account_id
        FROM ledger_entries e
        JOIN ledger_transactions lt ON lt.id = e.transaction_id
        WHERE lt.reference_type = 'refund'
          AND lt.reference_id = $1
          AND e.debit_vnd > 0
        LIMIT 1
        "#,
    )
    .bind(rf_res.refund.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    let rf_evts = fetch_outbox_payloads(&db.pool, "payment.refunded").await;
    let d_evt = rf_evts
        .iter()
        .find(|e| e["refund_id"].as_str() == Some(rf_res.refund.id.to_string().as_str()))
        .expect("outbox event for race refund");
    let expected_post = debit_acct.starts_with("seller_");
    assert_eq!(
        d_evt["is_post_settlement"],
        json!(expected_post),
        "is_post_settlement must match actual ledger debit account ({debit_acct})"
    );
    // Either way, seller pending balance MUST be 200k - 10k - 30k = 160k and global ledger balanced!
    let bal_d = svc.get_seller_balance(&shop_d).await.unwrap();
    assert_eq!(bal_d.pending_vnd, 160_000);

    // --- Race E (P2-2): `process_order_cancelled_event` vs `order.completed` settling allocation ---
    // Hold `FOR UPDATE` on the allocation (simulating `settle_order_to_pending_tx` in flight),
    // spawn `process_order_cancelled_event`, verify it BLOCKS on `payment_shop_allocations FOR UPDATE`,
    // then commit `escrow_status = 'pending_release'`. Once unblocked, `order.cancelled` MUST see
    // `pending_release`, create ZERO refunds, and emit ZERO `payment.refunded` events!
    let grp_e = format!("grp-cancel-settle-{}", Uuid::new_v4());
    let ord_e = format!("ord-cancel-settle-{}", Uuid::new_v4());
    let shop_e = format!("shop-cancel-settle-{}", Uuid::new_v4());
    let pwa_e = svc
        .create_payment(sample_vietqr_input(
            &grp_e,
            &ord_e,
            "buyer-rf-e",
            &shop_e,
            180_000,
            9_000,
            20_000,
            0,
            Some(format!("idem-p-e-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let (wh_e, raw_e) = succeeded_webhook(
        pwa_e.payment.id,
        &grp_e,
        &format!("TX-RF-E-{}", Uuid::new_v4()),
        200_000,
    );
    svc.process_verified_webhook(wh_e, raw_e, "vietqr_mock", "trace-e")
        .await
        .unwrap();

    let alloc_e_id = pwa_e.allocations[0].id;
    let mut tx_settle = db.pool.begin().await.unwrap();
    let settle_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *tx_settle)
        .await
        .unwrap();
    let _: Uuid =
        sqlx::query_scalar("SELECT id FROM payment_shop_allocations WHERE id = $1 FOR UPDATE")
            .bind(alloc_e_id)
            .fetch_one(&mut *tx_settle)
            .await
            .unwrap();

    let svc_cancel = svc.clone();
    let ord_e_clone = ord_e.clone();
    let cancel_handle = tokio::spawn(async move {
        svc_cancel
            .process_order_cancelled_event(
                &format!("evt-cancel-race-e-{}", Uuid::new_v4()),
                &ord_e_clone,
                "tr-cancel-e",
            )
            .await
    });

    // Must block specifically on `payment_shop_allocations` row lock held by `tx_settle`!
    wait_for_blocked_by_pid(&db.pool, settle_pid, "payment_shop_allocations", 1).await;
    assert!(
        !cancel_handle.is_finished(),
        "process_order_cancelled_event must wait for FOR UPDATE on payment_shop_allocations"
    );

    // Finish settlement inside `tx_settle` (`held -> pending_release`) and commit.
    sqlx::query(
        "UPDATE payment_shop_allocations SET escrow_status = 'pending_release', settled_at = NOW() WHERE id = $1",
    )
    .bind(alloc_e_id)
    .execute(&mut *tx_settle)
    .await
    .unwrap();
    tx_settle.commit().await.unwrap();

    let processed = cancel_handle
        .await
        .unwrap()
        .expect("order.cancelled completes cleanly after settlement commit");
    assert!(processed);

    let rf_count_e: i64 =
        sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM refunds WHERE payment_id = $1")
            .bind(pwa_e.payment.id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(
        rf_count_e, 0,
        "order.cancelled must NOT create a pre-settlement refund after allocation settled to pending_release"
    );
    let outbox_rf_e: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM outbox_events WHERE event_type = 'payment.refunded' AND payload->>'order_id' = $1",
    )
    .bind(&ord_e)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        outbox_rf_e, 0,
        "order.cancelled must NOT emit payment.refunded after allocation settled"
    );

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(summary.is_balanced);

    db.cleanup().await;
}

// =============================================================================
// 6. Real Postgres SSE handler (initial + live broadcast redaction) & gRPC privacy
// =============================================================================

#[tokio::test]
async fn test_pg_sse_handler_and_grpc_privacy_real_db() {
    let Some(db) = TestDb::setup("test_pg_sse_handler_and_grpc_privacy_real_db", 8).await else {
        return;
    };
    let svc = db.service();
    let auth = db.auth();
    let app = build_router(svc.clone(), auth.clone());
    let grpc = PaymentGrpcServer::new(svc.clone(), auth);

    let grp = format!("grp-priv-{}", Uuid::new_v4());
    let ord_a = format!("ord-priv-a-{}", Uuid::new_v4());
    let ord_b = format!("ord-priv-b-{}", Uuid::new_v4());
    let pwa = svc
        .create_payment(CreatePaymentInput {
            order_group_id: grp.clone(),
            order_id: ord_b.clone(),
            user_id: "buyer-owner".into(),
            amount_vnd: 740_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![
                ShopAllocationInput {
                    order_id: Some(ord_a.clone()),
                    shop_id: "shop-a".into(),
                    subtotal_vnd: 200_000,
                    commission_vnd: 12_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 10_000,
                },
                ShopAllocationInput {
                    order_id: Some(ord_b.clone()),
                    shop_id: "shop-b".into(),
                    subtotal_vnd: 500_000,
                    commission_vnd: 35_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 30_000,
                    platform_discount_vnd: 0,
                },
            ],
            expires_at: Some(Utc::now() + ChronoDuration::minutes(15)),
            idempotency_key: Some(format!("idem-priv-{}", Uuid::new_v4())),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: Uuid::new_v4().simple().to_string(),
        })
        .await
        .unwrap();

    let now = Utc::now().timestamp();
    let buyer_tok = sign_test_jwt(
        TEST_USER_SECRET,
        json!({"sub": "buyer-owner", "role": "buyer", "exp": now + 300}),
    );
    let other_buyer_tok = sign_test_jwt(
        TEST_USER_SECRET,
        json!({"sub": "buyer-other", "role": "buyer", "exp": now + 300}),
    );
    let seller_a_tok = sign_test_jwt(
        TEST_USER_SECRET,
        json!({"sub": "seller-u1", "role": "seller", "shop_id": "shop-a", "exp": now + 300}),
    );
    let seller_c_tok = sign_test_jwt(
        TEST_USER_SECRET,
        json!({"sub": "seller-u3", "role": "seller", "shop_id": "shop-c", "exp": now + 300}),
    );
    let admin_tok = sign_test_jwt(
        TEST_USER_SECRET,
        json!({"sub": "admin-1", "role": "admin", "exp": now + 300}),
    );

    // 1. gRPC GetPayment end-to-end privacy checks against real Postgres
    let buyer_pb = grpc
        .get_payment(grpc_req_with_token(
            pb::GetPaymentRequest {
                payment_id: pwa.payment.id.to_string(),
                order_group_id: String::new(),
                order_id: String::new(),
            },
            &buyer_tok,
        ))
        .await
        .unwrap()
        .into_inner()
        .payment
        .unwrap();
    assert_eq!(buyer_pb.amount_vnd, 740_000);
    assert_eq!(buyer_pb.shop_allocations.len(), 2);
    assert!(buyer_pb
        .shop_allocations
        .iter()
        .all(|a| a.commission_vnd == 0));

    let seller_a_pb = grpc
        .get_payment(grpc_req_with_token(
            pb::GetPaymentRequest {
                payment_id: pwa.payment.id.to_string(),
                order_group_id: String::new(),
                order_id: String::new(),
            },
            &seller_a_tok,
        ))
        .await
        .unwrap()
        .into_inner()
        .payment
        .unwrap();
    assert_eq!(seller_a_pb.amount_vnd, 210_000);
    assert_eq!(seller_a_pb.order_id, ord_a);
    assert!(seller_a_pb.qr_url.is_empty());
    assert!(seller_a_pb.qr_code.is_empty());
    assert!(seller_a_pb.provider_ref.is_empty());
    assert_eq!(seller_a_pb.shop_allocations.len(), 1);
    assert_eq!(seller_a_pb.shop_allocations[0].shop_id, "shop-a");
    assert_eq!(seller_a_pb.shop_allocations[0].commission_vnd, 12_000);

    assert_eq!(
        grpc.get_payment(grpc_req_with_token(
            pb::GetPaymentRequest {
                payment_id: pwa.payment.id.to_string(),
                order_group_id: String::new(),
                order_id: String::new(),
            },
            &seller_c_tok,
        ))
        .await
        .unwrap_err()
        .code(),
        tonic::Code::PermissionDenied
    );

    // 2. HTTP SSE route: non-UUID -> 400, unknown UUID -> 404, unauthorized -> 403
    let r_bad = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/payments/not-a-uuid/events")
                .header(header::AUTHORIZATION, format!("Bearer {admin_tok}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r_bad.status(), StatusCode::BAD_REQUEST);

    let r_404 = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/payments/{}/events", Uuid::new_v4()))
                .header(header::AUTHORIZATION, format!("Bearer {buyer_tok}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r_404.status(), StatusCode::NOT_FOUND);

    for bad_tok in [&other_buyer_tok, &seller_c_tok] {
        let r_403 = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/payments/{}/events", pwa.payment.id))
                    .header(header::AUTHORIZATION, format!("Bearer {bad_tok}"))
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(r_403.status(), StatusCode::FORBIDDEN);
    }

    // 3. HTTP SSE live stream end-to-end (P3-8 & P3-10): open streams for BOTH seller_a AND buyer,
    //    then fire signed webhook via HTTP router!
    let seller_sse_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/payments/{}/events", pwa.payment.id))
                .header(header::AUTHORIZATION, format!("Bearer {seller_a_tok}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(seller_sse_resp.status(), StatusCode::OK);
    let mut seller_stream = seller_sse_resp.into_body().into_data_stream();

    let buyer_sse_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/payments/{}/events", pwa.payment.id))
                .header(header::AUTHORIZATION, format!("Bearer {buyer_tok}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(buyer_sse_resp.status(), StatusCode::OK);
    let mut buyer_stream = buyer_sse_resp.into_body().into_data_stream();

    // First frame on seller_a: initial payment.pending, already redacted for seller_a
    let first_chunk = tokio::time::timeout(Duration::from_secs(3), seller_stream.next())
        .await
        .expect("initial SSE frame timeout")
        .expect("initial SSE frame present")
        .expect("initial SSE bytes");
    let first_text = String::from_utf8_lossy(&first_chunk);
    assert!(
        first_text.contains("event: payment.pending"),
        "{first_text}"
    );
    assert!(first_text.contains("\"amount_vnd\":210000"), "{first_text}");
    assert!(first_text.contains(&ord_a), "{first_text}");
    assert!(!first_text.contains(&ord_b), "{first_text}");

    // First frame on buyer: initial payment.pending with full amount_vnd=740000
    let buyer_first_chunk = tokio::time::timeout(Duration::from_secs(3), buyer_stream.next())
        .await
        .expect("buyer initial SSE frame timeout")
        .expect("buyer initial SSE frame present")
        .expect("buyer initial SSE bytes");
    let buyer_first_text = String::from_utf8_lossy(&buyer_first_chunk);
    assert!(
        buyer_first_text.contains("event: payment.pending"),
        "{buyer_first_text}"
    );
    assert!(
        buyer_first_text.contains("\"amount_vnd\":740000"),
        "{buyer_first_text}"
    );

    // Send real signed HTTP webhook through `build_router` -> confirms payment & broadcasts live SSE event!
    let tx_secret_id = format!("TX-SSE-LIVE-{}", Uuid::new_v4());
    let wh_body = serde_json::to_vec(&json!({
        "provider": "vietqr_mock",
        "provider_tx_id": tx_secret_id,
        "payment_id": pwa.payment.id.to_string(),
        "order_group_id": grp,
        "amount_vnd": 740_000,
        "status": "SUCCEEDED",
        "occurred_at": Utc::now().to_rfc3339(),
    }))
    .unwrap();
    let sig = compute_hmac_hex(TEST_WEBHOOK_SECRET, &wh_body);
    let ts = Utc::now().timestamp().to_string();
    let wh_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/vietqr")
                .header(header::CONTENT_TYPE, "application/json")
                .header("x-signature", sig)
                .header("x-timestamp", ts)
                .body(Body::from(wh_body))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(wh_resp.status(), StatusCode::OK);

    // Live frame on seller_a's stream MUST be `payment.succeeded`, redacted (amount=210k, no provider_tx_id, no ord_b!)
    let live_chunk = tokio::time::timeout(Duration::from_secs(3), seller_stream.next())
        .await
        .expect("live SSE frame timeout")
        .expect("live SSE frame present")
        .expect("live SSE bytes");
    let live_text = String::from_utf8_lossy(&live_chunk);
    assert!(
        live_text.contains("event: payment.succeeded"),
        "{live_text}"
    );
    assert!(live_text.contains("\"amount_vnd\":210000"), "{live_text}");
    assert!(
        !live_text.contains(&tx_secret_id),
        "seller live SSE MUST redact provider_tx_id: {live_text}"
    );
    assert!(
        !live_text.contains(&ord_b),
        "seller live SSE MUST NOT leak sibling order_id: {live_text}"
    );

    // Live frame on buyer's stream MUST be `payment.succeeded` with FULL amount_vnd=740000 and tx_secret_id!
    let buyer_live_chunk = tokio::time::timeout(Duration::from_secs(3), buyer_stream.next())
        .await
        .expect("buyer live SSE frame timeout")
        .expect("buyer live SSE frame present")
        .expect("buyer live SSE bytes");
    let buyer_live_text = String::from_utf8_lossy(&buyer_live_chunk);
    assert!(
        buyer_live_text.contains("event: payment.succeeded"),
        "{buyer_live_text}"
    );
    assert!(
        buyer_live_text.contains("\"amount_vnd\":740000"),
        "buyer live SSE must include full amount: {buyer_live_text}"
    );
    assert!(
        buyer_live_text.contains(&tx_secret_id),
        "buyer live SSE must include provider_tx_id: {buyer_live_text}"
    );

    // P3-8: Both SSE streams MUST close cleanly right after emitting terminal `payment.succeeded`.
    let seller_eof = tokio::time::timeout(Duration::from_secs(2), seller_stream.next())
        .await
        .expect("seller SSE terminal close timeout");
    assert!(
        seller_eof.is_none(),
        "seller SSE stream must end after terminal payment.succeeded"
    );
    let buyer_eof = tokio::time::timeout(Duration::from_secs(2), buyer_stream.next())
        .await
        .expect("buyer SSE terminal close timeout");
    assert!(
        buyer_eof.is_none(),
        "buyer SSE stream must end after terminal payment.succeeded"
    );

    // Check /readyz and /internal/ledger/summary with admin JWT against live Postgres
    let ready_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/readyz")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(ready_resp.status(), StatusCode::OK);

    let ledger_http = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/internal/ledger/summary")
                .header(header::AUTHORIZATION, format!("Bearer {admin_tok}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(ledger_http.status(), StatusCode::OK);
    let ledger_bytes = ledger_http.into_body().collect().await.unwrap().to_bytes();
    let ledger_json: Value = serde_json::from_slice(&ledger_bytes).unwrap();
    assert_eq!(ledger_json["is_balanced"], json!(true));
    assert_eq!(ledger_json["total_debits_vnd"], json!(740_000));

    // P3-8: Verify `emit_sse` fires `refunded` on BOTH `create_refund` and `process_order_cancelled_event`,
    // AND verify a short-lived JWT closes a pending SSE stream upon `exp`.
    {
        let mut sse_rx = svc.subscribe_sse();
        let grp_r1 = format!("grp-sse-rf1-{}", Uuid::new_v4());
        let p_r1 = svc
            .create_payment(sample_vietqr_input(
                &grp_r1,
                &grp_r1,
                "buyer-owner",
                "shop-a",
                100_000,
                5_000,
                0,
                0,
                Some(format!("idem-sse-rf1-{}", Uuid::new_v4())),
            ))
            .await
            .unwrap();
        let (w_r1, raw_r1) = succeeded_webhook(
            p_r1.payment.id,
            &grp_r1,
            &format!("TX-SSE-RF1-{}", Uuid::new_v4()),
            100_000,
        );
        svc.process_verified_webhook(w_r1, raw_r1, "vietqr_mock", "tr-sse-rf1")
            .await
            .unwrap();
        // Drain pending + succeeded events
        while sse_rx.try_recv().is_ok() {}
        svc.create_refund(CreateRefundInput {
            payment_id: Some(p_r1.payment.id),
            order_id: grp_r1.clone(),
            amount_vnd: 100_000,
            reason: "full_refund_sse".into(),
            idempotency_key: format!("rf-sse-1-{}", Uuid::new_v4()),
            trace_id: "tr-rf-sse-1".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .unwrap();
        let rf_evt = tokio::time::timeout(Duration::from_secs(2), sse_rx.recv())
            .await
            .expect("create_refund SSE timeout")
            .expect("create_refund SSE event");
        assert_eq!(rf_evt.payment_id, p_r1.payment.id);
        assert_eq!(rf_evt.status, "refunded");

        let grp_r2 = format!("grp-sse-rf2-{}", Uuid::new_v4());
        let p_r2 = svc
            .create_payment(sample_vietqr_input(
                &grp_r2,
                &grp_r2,
                "buyer-owner",
                "shop-a",
                100_000,
                5_000,
                0,
                0,
                Some(format!("idem-sse-rf2-{}", Uuid::new_v4())),
            ))
            .await
            .unwrap();
        let (w_r2, raw_r2) = succeeded_webhook(
            p_r2.payment.id,
            &grp_r2,
            &format!("TX-SSE-RF2-{}", Uuid::new_v4()),
            100_000,
        );
        svc.process_verified_webhook(w_r2, raw_r2, "vietqr_mock", "tr-sse-rf2")
            .await
            .unwrap();
        while sse_rx.try_recv().is_ok() {}
        svc.process_order_cancelled_event(
            &format!("evt-sse-rf2-{}", Uuid::new_v4()),
            &grp_r2,
            "tr-sse-rf2-cancel",
        )
        .await
        .unwrap();
        let cancel_rf_evt = tokio::time::timeout(Duration::from_secs(2), sse_rx.recv())
            .await
            .expect("order.cancelled refunded SSE timeout")
            .expect("order.cancelled refunded SSE event");
        assert_eq!(cancel_rf_evt.payment_id, p_r2.payment.id);
        assert_eq!(cancel_rf_evt.status, "refunded");

        // Short-lived JWT closes a pending SSE stream cleanly when `exp` elapses.
        let grp_exp = format!("grp-sse-exp-{}", Uuid::new_v4());
        let p_exp = svc
            .create_payment(sample_vietqr_input(
                &grp_exp,
                &grp_exp,
                "buyer-owner",
                "shop-a",
                100_000,
                5_000,
                0,
                0,
                Some(format!("idem-sse-exp-{}", Uuid::new_v4())),
            ))
            .await
            .unwrap();
        let short_tok = sign_test_jwt(
            TEST_USER_SECRET,
            json!({"sub": "buyer-owner", "role": "buyer", "exp": Utc::now().timestamp() + 1}),
        );
        let exp_resp = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/payments/{}/events", p_exp.payment.id))
                    .header(header::AUTHORIZATION, format!("Bearer {short_tok}"))
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(exp_resp.status(), StatusCode::OK);
        let mut exp_stream = exp_resp.into_body().into_data_stream();
        let _initial = tokio::time::timeout(Duration::from_secs(2), exp_stream.next())
            .await
            .expect("initial frame")
            .expect("initial frame present")
            .expect("initial bytes");
        let after_exp = tokio::time::timeout(Duration::from_secs(3), exp_stream.next())
            .await
            .expect("SSE stream must terminate when JWT exp elapses");
        assert!(
            after_exp.is_none(),
            "SSE stream must close cleanly on JWT expiry"
        );
    }

    db.cleanup().await;
}

// =============================================================================
// 7. Full money/COD/reconciliation/two-step-refund/seller-ledger/hold-protection SQL
// =============================================================================

#[tokio::test]
async fn test_pg_full_money_cod_reconciliation_and_hold_protection_sql() {
    let Some(db) = TestDb::setup(
        "test_pg_full_money_cod_reconciliation_and_hold_protection_sql",
        8,
    )
    .await
    else {
        return;
    };
    let svc = db.service();

    // --- Part A: Unmatured hold protection SQL CTE (`release_matured_escrow_holds`) + `list_seller_ledger` + `request_seller_payout` ---
    let shop_hold = format!("shop-hold-{}", Uuid::new_v4());
    let ord_h1 = format!("ord-h1-{}", Uuid::new_v4());
    let ord_h2 = format!("ord-h2-{}", Uuid::new_v4());

    // Create, confirm, and settle ord_h1 (seller_net = 100,000 VND)
    let p_h1 = svc
        .create_payment(sample_vietqr_input(
            &ord_h1,
            &ord_h1,
            "buyer-h",
            &shop_hold,
            110_000,
            10_000,
            20_000,
            0,
            Some(format!("idem-h1-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let (w1, r1) = succeeded_webhook(
        p_h1.payment.id,
        &ord_h1,
        &format!("TX-H1-{}", Uuid::new_v4()),
        130_000,
    );
    svc.process_verified_webhook(w1, r1, "vietqr_mock", "tr-h1")
        .await
        .unwrap();

    // release_escrow MUST reject while allocation is still `held`
    let early_rel = svc.release_escrow(&ord_h1, Some(&shop_hold)).await;
    assert!(matches!(early_rel, Err(PaymentError::Conflict(_))));

    svc.settle_order_to_pending(&ord_h1).await.unwrap();
    // Post-settlement clawback of 40,000 VND from seller_pending (leaves 60k in pending)
    let rf_h1 = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p_h1.payment.id),
            order_id: ord_h1.clone(),
            amount_vnd: 40_000,
            reason: "post_settle_clawback".into(),
            idempotency_key: format!("rf-h1-{}", Uuid::new_v4()),
            trace_id: "tr-h1-rf".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .unwrap();

    // Buyer submits receiving bank account -> admin settles refund -> replay idempotent -> different key 409
    assert!(matches!(
        svc.submit_refund_bank_account(SubmitRefundBankAccountInput {
            refund_id: rf_h1.refund.id,
            caller_user_id: "wrong-buyer".into(),
            bank_code: "VCB".into(),
            bank_account_number: "19034567890123".into(),
            bank_account_name: "WRONG".into(),
        })
        .await,
        Err(PaymentError::Forbidden(_))
    ));
    let banked = svc
        .submit_refund_bank_account(SubmitRefundBankAccountInput {
            refund_id: rf_h1.refund.id,
            caller_user_id: "buyer-h".into(),
            bank_code: "VCB".into(),
            bank_account_number: "19034567890123".into(),
            bank_account_name: "NGUYEN VAN H".into(),
        })
        .await
        .unwrap();
    assert_eq!(banked.bank_account_masked.as_deref(), Some("****0123"));

    // List refunds: wrong buyer -> 403, owner -> sees masked, admin -> sees all
    assert!(matches!(
        svc.list_refunds(ListRefundsFilter {
            caller_user_id: Some("wrong-buyer".into()),
            is_admin: false,
            status: None,
            order_id: Some(ord_h1.clone()),
            page: 1,
            page_size: 20,
        })
        .await,
        Err(PaymentError::Forbidden(_))
    ));
    let buyer_rfs = svc
        .list_refunds(ListRefundsFilter {
            caller_user_id: Some("buyer-h".into()),
            is_admin: false,
            status: Some("pending".into()),
            order_id: Some(ord_h1.clone()),
            page: 1,
            page_size: 20,
        })
        .await
        .unwrap();
    assert_eq!(buyer_rfs.total, 1);

    let settle_key = format!("settle-h1-{}", Uuid::new_v4());
    let settled1 = svc
        .settle_refund(SettleRefundInput {
            refund_id: rf_h1.refund.id,
            provider_refund_id: "FT-H1-001".into(),
            note: "manual bank transfer".into(),
            idempotency_key: settle_key.clone(),
            trace_id: "tr-settle-1".into(),
        })
        .await
        .unwrap();
    assert!(!settled1.already_settled);
    assert_eq!(settled1.refund.status, "completed");

    let settled_replay = svc
        .settle_refund(SettleRefundInput {
            refund_id: rf_h1.refund.id,
            provider_refund_id: "FT-H1-001".into(),
            note: "replay".into(),
            idempotency_key: settle_key,
            trace_id: "tr-settle-2".into(),
        })
        .await
        .unwrap();
    assert!(settled_replay.already_settled);

    assert!(matches!(
        svc.settle_refund(SettleRefundInput {
            refund_id: rf_h1.refund.id,
            provider_refund_id: "FT-H1-OTHER".into(),
            note: "dup".into(),
            idempotency_key: format!("settle-h1-dup-{}", Uuid::new_v4()),
            trace_id: "tr-settle-3".into(),
        })
        .await,
        Err(PaymentError::Conflict(_))
    ));
    // Post-settle bank edit rejected 409
    assert!(matches!(
        svc.submit_refund_bank_account(SubmitRefundBankAccountInput {
            refund_id: rf_h1.refund.id,
            caller_user_id: "buyer-h".into(),
            bank_code: "MB".into(),
            bank_account_number: "999988887777".into(),
            bank_account_name: "LATE".into(),
        })
        .await,
        Err(PaymentError::Conflict(_))
    ));

    // Create, confirm, and settle ord_h2 (unmatured: settled_at = NOW(), seller_net = 100k -> pending = 160k)
    let p_h2 = svc
        .create_payment(sample_vietqr_input(
            &ord_h2,
            &ord_h2,
            "buyer-h",
            &shop_hold,
            110_000,
            10_000,
            20_000,
            0,
            Some(format!("idem-h2-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let (w2, r2) = succeeded_webhook(
        p_h2.payment.id,
        &ord_h2,
        &format!("TX-H2-{}", Uuid::new_v4()),
        130_000,
    );
    svc.process_verified_webhook(w2, r2, "vietqr_mock", "tr-h2")
        .await
        .unwrap();
    svc.settle_order_to_pending(&ord_h2).await.unwrap();

    // Backdate ONLY ord_h1's settled_at by 100 hours so ord_h1 is matured (>72h) while ord_h2 stays unmatured!
    sqlx::query(
        "UPDATE payment_shop_allocations SET settled_at = NOW() - INTERVAL '100 hours' WHERE order_id = $1",
    )
    .bind(&ord_h1)
    .execute(&db.pool)
    .await
    .unwrap();

    let released_cnt = svc.release_matured_escrow_holds().await.unwrap();
    assert_eq!(released_cnt, 1);

    // CRITICAL INVARIANT: pending=160k, unmatured ord_h2=100k -> ord_h1 releases ONLY 60k (NOT 100k!)
    let bal_hold = svc.get_seller_balance(&shop_hold).await.unwrap();
    assert_eq!(bal_hold.available_vnd, 60_000);
    assert_eq!(bal_hold.pending_vnd, 100_000);
    assert_eq!(bal_hold.total_earned_vnd, 160_000);

    // Seller payout: over-withdraw rejected, valid 50k payout succeeds (PENDING) & idempotent replay works,
    // reject restores available balance, then re-request and approve completes disbursement.
    assert!(matches!(
        svc.request_seller_payout(nexusmart_payment::domain::RequestSellerPayoutInput {
            shop_id: shop_hold.clone(),
            amount_vnd: 60_001,
            idempotency_key: "po-over".into(),
            requested_by_user_id: "seller-hold".into(),
        })
        .await,
        Err(PaymentError::InsufficientBalance { .. })
    ));
    let po_key = format!("po-{}", Uuid::new_v4());
    let po = svc
        .request_seller_payout(nexusmart_payment::domain::RequestSellerPayoutInput {
            shop_id: shop_hold.clone(),
            amount_vnd: 50_000,
            idempotency_key: po_key.clone(),
            requested_by_user_id: "seller-hold".into(),
        })
        .await
        .unwrap();
    assert_eq!(po.status, "PENDING");
    assert_eq!(po.bank_account_masked, "****0123");
    let po_replay = svc
        .request_seller_payout(nexusmart_payment::domain::RequestSellerPayoutInput {
            shop_id: shop_hold.clone(),
            amount_vnd: 50_000,
            idempotency_key: po_key.clone(),
            requested_by_user_id: "seller-hold".into(),
        })
        .await
        .unwrap();
    assert_eq!(po_replay.id, po.id);

    // Second PENDING for same shop rejected.
    assert!(matches!(
        svc.request_seller_payout(nexusmart_payment::domain::RequestSellerPayoutInput {
            shop_id: shop_hold.clone(),
            amount_vnd: 5_000,
            idempotency_key: format!("po-dup-{}", Uuid::new_v4()),
            requested_by_user_id: "seller-hold".into(),
        })
        .await,
        Err(PaymentError::PayoutPendingExists(_))
    ));

    // Rejecting restores available balance from 10k -> 60k and zeroes payout_pending_vnd.
    let po_rejected = svc
        .decide_seller_payout(nexusmart_payment::domain::DecideSellerPayoutInput {
            payout_id: po.id,
            decision: nexusmart_payment::domain::PayoutDecision::Reject,
            reason: "Kiểm tra lại thông tin".into(),
            decided_by_user_id: "admin-hold".into(),
        })
        .await
        .unwrap();
    assert_eq!(po_rejected.status, "REJECTED");
    assert_eq!(
        svc.get_seller_balance(&shop_hold)
            .await
            .unwrap()
            .available_vnd,
        60_000
    );
    assert_eq!(
        svc.get_seller_balance(&shop_hold)
            .await
            .unwrap()
            .payout_pending_vnd,
        0
    );

    // Re-request 50k and approve so downstream ledger assertions see available=10k, pending=100k.
    let po2 = svc
        .request_seller_payout(nexusmart_payment::domain::RequestSellerPayoutInput {
            shop_id: shop_hold.clone(),
            amount_vnd: 50_000,
            idempotency_key: format!("po-approve-{}", Uuid::new_v4()),
            requested_by_user_id: "seller-hold".into(),
        })
        .await
        .unwrap();
    let po2_approved = svc
        .decide_seller_payout(nexusmart_payment::domain::DecideSellerPayoutInput {
            payout_id: po2.id,
            decision: nexusmart_payment::domain::PayoutDecision::Approve,
            reason: String::new(),
            decided_by_user_id: "admin-hold".into(),
        })
        .await
        .unwrap();
    assert_eq!(po2_approved.status, "COMPLETED");
    assert!(po2_approved.transfer_ref.starts_with("MOCKBANK-"));

    // ListSellerLedger real SQL matches GetSellerBalance for pending, available, and combined!
    let bal_after_po = svc.get_seller_balance(&shop_hold).await.unwrap();
    assert_eq!(bal_after_po.available_vnd, 10_000);
    assert_eq!(bal_after_po.pending_vnd, 100_000);
    assert_eq!(bal_after_po.payout_pending_vnd, 0);
    let led_all = svc
        .list_seller_ledger(ListSellerLedgerFilter {
            shop_id: Some(shop_hold.clone()),
            account_filter: None,
            reference_type: None,
            page: 1,
            page_size: 50,
        })
        .await
        .unwrap();
    let led_pending = svc
        .list_seller_ledger(ListSellerLedgerFilter {
            shop_id: Some(shop_hold.clone()),
            account_filter: Some(SellerLedgerAccountFilter::Pending),
            reference_type: None,
            page: 1,
            page_size: 50,
        })
        .await
        .unwrap();
    let led_avail = svc
        .list_seller_ledger(ListSellerLedgerFilter {
            shop_id: Some(shop_hold.clone()),
            account_filter: Some(SellerLedgerAccountFilter::Available),
            reference_type: None,
            page: 1,
            page_size: 50,
        })
        .await
        .unwrap();
    assert_eq!(
        led_pending.entries.iter().map(|e| e.delta_vnd).sum::<i64>(),
        bal_after_po.pending_vnd
    );
    assert_eq!(
        led_avail.entries.iter().map(|e| e.delta_vnd).sum::<i64>(),
        bal_after_po.available_vnd
    );
    assert_eq!(
        led_all.entries.iter().map(|e| e.delta_vnd).sum::<i64>(),
        bal_after_po.pending_vnd + bal_after_po.available_vnd
    );
    assert!(led_all
        .entries
        .iter()
        .any(|e| e.reference_type == "escrow_settle" && e.description.contains("phí sàn 10000")));

    // --- Part B: Multi-shop COD lifecycle (deliver sub1, fail-deliver return sub2, post-delivery return collect COD) ---
    let grp_cod = format!("grp-cod-{}", Uuid::new_v4());
    let ord_c1 = format!("ord-c1-{}", Uuid::new_v4());
    let ord_c2 = format!("ord-c2-{}", Uuid::new_v4());
    let p_cod = svc
        .create_payment(CreatePaymentInput {
            order_group_id: grp_cod.clone(),
            order_id: grp_cod.clone(),
            user_id: "buyer-cod".into(),
            amount_vnd: 350_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![
                ShopAllocationInput {
                    order_id: Some(ord_c1.clone()),
                    shop_id: "shop-cod-1".into(),
                    subtotal_vnd: 180_000,
                    commission_vnd: 9_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 0,
                },
                ShopAllocationInput {
                    order_id: Some(ord_c2.clone()),
                    shop_id: "shop-cod-2".into(),
                    subtotal_vnd: 130_000,
                    commission_vnd: 6_500,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 0,
                },
            ],
            expires_at: None,
            idempotency_key: Some(format!("idem-cod-{}", Uuid::new_v4())),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-cod".into(),
        })
        .await
        .unwrap();
    assert_eq!(p_cod.payment.status, "pending");

    // Refund uncollected COD rejected 409
    assert!(matches!(
        svc.create_refund(CreateRefundInput {
            payment_id: Some(p_cod.payment.id),
            order_id: ord_c1.clone(),
            amount_vnd: 10_000,
            reason: "uncollected".into(),
            idempotency_key: format!("rf-cod-uncol-{}", Uuid::new_v4()),
            trace_id: "tr-cod".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await,
        Err(PaymentError::Conflict(_))
    ));

    // Deliver ord_c1 -> parent stays pending while ord_c2 awaits collection
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-del-{}", Uuid::new_v4()),
        &ord_c1,
        None,
        None,
        None,
        "tr-cod",
    )
    .await
    .unwrap();
    let after_del1 = svc
        .get_payment(Some(p_cod.payment.id), None, None)
        .await
        .unwrap();
    assert_eq!(after_del1.payment.status, "pending");

    // Failed delivery on ord_c2 (`order.returned` from `SHIPPING`) -> cancels ord_c2 alloc, parent becomes `succeeded`!
    svc.process_order_returned_event(
        &format!("evt-ret-{}", Uuid::new_v4()),
        &ord_c2,
        Some("SHIPPING"),
        "tr-cod",
    )
    .await
    .unwrap();
    let after_ret2 = svc
        .get_payment(Some(p_cod.payment.id), None, None)
        .await
        .unwrap();
    assert_eq!(after_ret2.payment.status, "succeeded");

    // --- Part C: Reconciliation queue (underpay -> ASSIGN 409 -> SUSPEND -> REFUND; overpay -> ASSIGN + excess refund) & run_reconciliation & expiry ---
    let grp_u = format!("grp-under-{}", Uuid::new_v4());
    let p_under = svc
        .create_payment(sample_vietqr_input(
            &grp_u,
            &grp_u,
            "buyer-rec",
            "shop-rec",
            200_000,
            10_000,
            0,
            0,
            Some(format!("idem-u-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let tx_u = format!("TX-UNDER-{}", Uuid::new_v4());
    let (wu, ru) = succeeded_webhook(p_under.payment.id, &grp_u, &tx_u, 150_000);
    let wh_u_res = svc
        .process_verified_webhook(wu, ru, "vietqr_mock", "tr-u")
        .await
        .unwrap();
    assert!(wh_u_res.status.contains("queued_for_reconciliation"));

    let q_list = svc
        .list_reconciliation_queue(Some("pending"), 1, 50)
        .await
        .unwrap();
    let q_u = q_list
        .items
        .iter()
        .find(|i| i.provider_tx_id == tx_u)
        .expect("underpay item in queue");

    // ASSIGN underpay -> 409
    assert!(matches!(
        svc.resolve_reconciliation_item(ResolveReconciliationInput {
            item_id: q_u.id,
            action: ReconciliationActionKind::Assign,
            payment_id: Some(p_under.payment.id),
            note: "bad".into(),
            idempotency_key: format!("rq-assign-u-{}", Uuid::new_v4()),
            trace_id: "tr-u".into(),
        })
        .await,
        Err(PaymentError::Conflict(_))
    ));
    // SUSPEND -> ok, then REFUND -> ok + idempotent replay
    let susp = svc
        .resolve_reconciliation_item(ResolveReconciliationInput {
            item_id: q_u.id,
            action: ReconciliationActionKind::Suspend,
            payment_id: None,
            note: "checking with buyer".into(),
            idempotency_key: format!("rq-susp-u-{}", Uuid::new_v4()),
            trace_id: "tr-u".into(),
        })
        .await
        .unwrap();
    assert_eq!(susp.item.resolution_status, "suspended");

    let rq_rf_key = format!("rq-rf-u-{}", Uuid::new_v4());
    let rq_rf = svc
        .resolve_reconciliation_item(ResolveReconciliationInput {
            item_id: q_u.id,
            action: ReconciliationActionKind::Refund,
            payment_id: Some(p_under.payment.id),
            note: "refund underpay".into(),
            idempotency_key: rq_rf_key.clone(),
            trace_id: "tr-u".into(),
        })
        .await
        .unwrap();
    assert_eq!(rq_rf.item.resolution_status, "refunded");
    assert_eq!(rq_rf.refund.as_ref().unwrap().amount_vnd, 150_000);
    let rq_rf_replay = svc
        .resolve_reconciliation_item(ResolveReconciliationInput {
            item_id: q_u.id,
            action: ReconciliationActionKind::Refund,
            payment_id: Some(p_under.payment.id),
            note: "replay".into(),
            idempotency_key: rq_rf_key,
            trace_id: "tr-u".into(),
        })
        .await
        .unwrap();
    assert!(rq_rf_replay.already_resolved);

    // Overpay -> ASSIGN confirms payment + creates pending excess refund
    let grp_o = format!("grp-over-{}", Uuid::new_v4());
    let p_over = svc
        .create_payment(sample_vietqr_input(
            &grp_o,
            &grp_o,
            "buyer-rec",
            "shop-rec",
            200_000,
            10_000,
            0,
            0,
            Some(format!("idem-o-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let tx_o = format!("TX-OVER-{}", Uuid::new_v4());
    let (wo, ro) = succeeded_webhook(p_over.payment.id, &grp_o, &tx_o, 225_000);
    svc.process_verified_webhook(wo, ro, "vietqr_mock", "tr-o")
        .await
        .unwrap();
    let q_o = svc
        .list_reconciliation_queue(Some("pending"), 1, 50)
        .await
        .unwrap()
        .items
        .into_iter()
        .find(|i| i.provider_tx_id == tx_o)
        .expect("overpay item");
    let assigned = svc
        .resolve_reconciliation_item(ResolveReconciliationInput {
            item_id: q_o.id,
            action: ReconciliationActionKind::Assign,
            payment_id: Some(p_over.payment.id),
            note: "assign overpay".into(),
            idempotency_key: format!("rq-as-o-{}", Uuid::new_v4()),
            trace_id: "tr-o".into(),
        })
        .await
        .unwrap();
    assert_eq!(assigned.item.resolution_status, "assigned");
    assert_eq!(assigned.refund.as_ref().unwrap().amount_vnd, 25_000);

    // Expiration scanner expires overdue pending VietQR payment
    let grp_exp = format!("grp-exp-{}", Uuid::new_v4());
    let mut exp_inp = sample_vietqr_input(
        &grp_exp,
        &grp_exp,
        "buyer-exp",
        "shop-exp",
        100_000,
        5_000,
        0,
        0,
        Some(format!("idem-exp-{}", Uuid::new_v4())),
    );
    exp_inp.expires_at = Some(Utc::now() - ChronoDuration::minutes(1));
    let p_exp = svc.create_payment(exp_inp).await.unwrap();
    let expired_cnt = svc.scan_expired_payments().await.unwrap();
    assert_eq!(expired_cnt, 1);
    let p_exp_after = svc
        .get_payment(Some(p_exp.payment.id), None, None)
        .await
        .unwrap();
    assert_eq!(p_exp_after.payment.status, "expired");

    // CancelPayment: idempotent on already-expired payment, cancels pending VietQR, rejects paid with 409
    let cancel_exp = svc
        .cancel_payment(Some(p_exp.payment.id), None, "already expired", "tr-can")
        .await
        .unwrap();
    assert_eq!(cancel_exp.payment.status, "expired");
    assert!(matches!(
        svc.cancel_payment(Some(p_over.payment.id), None, "already paid", "tr-can")
            .await,
        Err(PaymentError::Conflict(_))
    ));
    let grp_can = format!("grp-can-{}", Uuid::new_v4());
    let p_can = svc
        .create_payment(sample_vietqr_input(
            &grp_can,
            &grp_can,
            "buyer-can",
            "shop-can",
            100_000,
            5_000,
            0,
            0,
            Some(format!("idem-can-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let cancelled = svc
        .cancel_payment(None, Some(&grp_can), "user_cancel", "tr-can")
        .await
        .unwrap();
    assert_eq!(cancelled.payment.status, "cancelled");
    assert_eq!(cancelled.payment.id, p_can.payment.id);

    // Durable mock reconciliation run for VN today
    let report = svc
        .run_reconciliation(vn_today(Utc::now()), None)
        .await
        .unwrap();
    assert_eq!(report.run.status, "completed");
    assert!(report.run.matched_count >= 3);
    assert_eq!(report.run.missing_provider_count, 0);

    let summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(
        summary.is_balanced,
        "final ledger must be balanced: {summary:?}"
    );

    db.cleanup().await;
}

// =============================================================================
// 8. D9-bis point 9: Interleaved transactions on same payment group hold the
//    same `lock_payment_group_tx` lock BEFORE outbox `seq` allocation.
// =============================================================================

#[tokio::test]
async fn test_pg_d9bis_interleaved_txs_same_group_strict_seq_order() {
    let Some(db) = TestDb::setup(
        "test_pg_d9bis_interleaved_txs_same_group_strict_seq_order",
        8,
    )
    .await
    else {
        return;
    };
    let svc = db.service();

    // --- Subcase A: `settle_refund(R1)` interleaved with an active checkout-group transaction ---
    // Previously `settle_refund` only locked `refunds(id)` and did not lock `payments`, so a
    // concurrent `create_refund(R2)` / group TX holding `payments FOR UPDATE` could allocate a
    // HIGHER outbox `seq` than `settle_refund` even when the group TX ran first.
    let grp_a = format!("grp-d9bis-a-{}", Uuid::new_v4());
    let created_a = svc
        .create_payment(sample_vietqr_input(
            &grp_a,
            &grp_a,
            "buyer-d9a",
            "shop-d9a",
            100_000,
            5_000,
            0,
            0,
            Some(format!("idem-d9a-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let (wh, raw) = succeeded_webhook(
        created_a.payment.id,
        &grp_a,
        "TX-D9A-1",
        created_a.payment.amount_vnd,
    );
    svc.process_verified_webhook(wh, raw, "vietqr_mock", "tr-d9a-wh")
        .await
        .unwrap();

    let r1 = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(created_a.payment.id),
            order_id: grp_a.clone(),
            amount_vnd: 30_000,
            reason: "partial_r1".into(),
            idempotency_key: format!("idem-r1-{}", Uuid::new_v4()),
            trace_id: "tr-d9a-r1".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .unwrap();
    svc.submit_refund_bank_account(SubmitRefundBankAccountInput {
        refund_id: r1.refund.id,
        caller_user_id: "buyer-d9a".into(),
        bank_code: "VCB".into(),
        bank_account_number: "0123456789".into(),
        bank_account_name: "NGUYEN VAN A".into(),
    })
    .await
    .unwrap();

    // Hold lock on the checkout group's payment row (simulating TX-1 in flight before emitting its
    // outbox event, e.g. `create_refund(R2)` or group action).
    let mut tx_a = db.pool.begin().await.unwrap();
    let _: Uuid = sqlx::query_scalar("SELECT id FROM payments WHERE id = $1 FOR UPDATE")
        .bind(created_a.payment.id)
        .fetch_one(&mut *tx_a)
        .await
        .unwrap();

    let svc_clone = svc.clone();
    let settle_handle = tokio::spawn(async move {
        svc_clone
            .settle_refund(SettleRefundInput {
                refund_id: r1.refund.id,
                provider_refund_id: "PR-D9A-1".into(),
                note: "manual transfer".into(),
                idempotency_key: format!("idem-settle-{}", Uuid::new_v4()),
                trace_id: "tr-d9a-settle".into(),
            })
            .await
    });

    // Give `settle_refund` plenty of time to hit `lock_payment_group_tx`; it MUST block on TX-A
    // and MUST NOT insert `payment.refund_settled` ahead of TX-A's outbox event.
    tokio::time::sleep(Duration::from_millis(120)).await;
    assert!(
        !settle_handle.is_finished(),
        "settle_refund must block on parent payment group lock while TX-A holds payments FOR UPDATE"
    );

    let seq_tx_a: i64 = sqlx::query_scalar(
        r#"
        INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload)
        VALUES ('payment.refunded', $1, 'tr-d9a-tx1', '{"marker":"tx_a"}'::jsonb)
        RETURNING seq
        "#,
    )
    .bind(&grp_a)
    .fetch_one(&mut *tx_a)
    .await
    .unwrap();
    tx_a.commit().await.unwrap();

    let settled = settle_handle
        .await
        .expect("settle_refund task joined")
        .expect("settle_refund succeeded after TX-A committed");
    assert_eq!(settled.refund.status, "completed");

    let (seq_settle, agg_settle): (i64, String) = sqlx::query_as(
        r#"
        SELECT seq, aggregate_id
        FROM outbox_events
        WHERE event_type = 'payment.refund_settled'
          AND payload->>'refund_id' = $1
        "#,
    )
    .bind(r1.refund.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(agg_settle, grp_a);
    assert!(
        seq_tx_a < seq_settle,
        "TX-A holding checkout group lock must allocate lower outbox seq than blocked settle_refund ({seq_tx_a} vs {seq_settle})"
    );

    // --- Subcase B: Two sibling payments (P1 and P2) of the SAME checkout group ---
    // `cancel_payment(Some(P1.id))` previously locked only P1 (not P2), allowing it to allocate
    // `payment.failed` ahead of a concurrent TX holding P2 (`confirm_vietqr_payment_tx(P2)` or
    // `create_refund(P2)`) on the exact same `aggregate_id = order_group_id`.
    let grp_b = format!("grp-d9bis-b-{}", Uuid::new_v4());
    let p1 = svc
        .create_payment(sample_vietqr_input(
            &grp_b,
            &grp_b,
            "buyer-d9b",
            "shop-d9b",
            100_000,
            5_000,
            0,
            0,
            Some(format!("idem-d9b-p1-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let p2_id = Uuid::new_v4();
    sqlx::query(
        r#"
        INSERT INTO payments (
            id, order_id, order_group_id, user_id, method, status, amount_vnd,
            provider, provider_order_code, idempotency_key, expires_at
        )
        VALUES ($1, $2, $3, 'buyer-d9b', 'VIETQR', 'pending', 100000, 'vietqr_mock', $4, $5, NOW() + INTERVAL '15 minutes')
        "#,
    )
    .bind(p2_id)
    .bind(&grp_b)
    .bind(&grp_b)
    .bind(format!("NMD9B{}", &p2_id.simple().to_string()[..10]))
    .bind(format!("idem-d9b-p2-{}", Uuid::new_v4()))
    .execute(&db.pool)
    .await
    .unwrap();

    // TX-B locks ONLY P2 (the sibling payment in the same checkout group).
    let mut tx_b = db.pool.begin().await.unwrap();
    let _: Uuid = sqlx::query_scalar("SELECT id FROM payments WHERE id = $1 FOR UPDATE")
        .bind(p2_id)
        .fetch_one(&mut *tx_b)
        .await
        .unwrap();

    let svc_clone2 = svc.clone();
    let p1_id = p1.payment.id;
    let cancel_handle = tokio::spawn(async move {
        svc_clone2
            .cancel_payment(Some(p1_id), None, "retry_cancel_p1", "tr-d9b-cancel")
            .await
    });

    tokio::time::sleep(Duration::from_millis(120)).await;
    assert!(
        !cancel_handle.is_finished(),
        "cancel_payment(Some(P1.id)) must lock entire checkout group and block on sibling P2"
    );

    let seq_tx_b: i64 = sqlx::query_scalar(
        r#"
        INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload)
        VALUES ('payment.succeeded', $1, 'tr-d9b-txb', '{"marker":"tx_b_p2"}'::jsonb)
        RETURNING seq
        "#,
    )
    .bind(&grp_b)
    .fetch_one(&mut *tx_b)
    .await
    .unwrap();
    tx_b.commit().await.unwrap();

    let cancelled_p1 = cancel_handle
        .await
        .expect("cancel_payment task joined")
        .expect("cancel_payment succeeded after TX-B committed");
    assert_eq!(cancelled_p1.payment.status, "cancelled");

    let (seq_cancel, agg_cancel): (i64, String) = sqlx::query_as(
        r#"
        SELECT seq, aggregate_id
        FROM outbox_events
        WHERE event_type = 'payment.failed'
          AND payload->>'payment_id' = $1
        "#,
    )
    .bind(p1_id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(agg_cancel, grp_b);
    assert!(
        seq_tx_b < seq_cancel,
        "TX-B holding sibling P2 must allocate lower outbox seq than cancel_payment(P1) ({seq_tx_b} vs {seq_cancel})"
    );

    db.cleanup().await;
}

// =============================================================================
// 9. P1-1: Webhook status allowlist (PENDING/PROCESSING ignored, REVERSED queued)
// =============================================================================

#[tokio::test]
async fn test_pg_p1_1_webhook_status_allowlist_non_final_and_unexpected() {
    let Some(db) = TestDb::setup(
        "test_pg_p1_1_webhook_status_allowlist_non_final_and_unexpected",
        6,
    )
    .await
    else {
        return;
    };
    let svc = db.service();
    let app = build_router(svc.clone(), db.auth());

    let send_signed_webhook = |body: Value| {
        let app = app.clone();
        async move {
            let bytes = serde_json::to_vec(&body).unwrap();
            let sig = compute_hmac_hex(TEST_WEBHOOK_SECRET, &bytes);
            let ts = Utc::now().timestamp().to_string();
            let resp = app
                .oneshot(
                    Request::builder()
                        .method(Method::POST)
                        .uri("/webhooks/vietqr")
                        .header(header::CONTENT_TYPE, "application/json")
                        .header("x-signature", sig)
                        .header("x-timestamp", ts)
                        .body(Body::from(bytes))
                        .unwrap(),
                )
                .await
                .unwrap();
            let status = resp.status();
            let raw = resp.into_body().collect().await.unwrap().to_bytes();
            let json_body: Value = serde_json::from_slice(&raw).unwrap();
            (status, json_body)
        }
    };

    // --- Part A: PENDING -> PROCESSING -> SUCCEEDED with the EXACT SAME provider_tx_id ---
    let grp1 = format!("grp-p1-1-a-{}", Uuid::new_v4());
    let p1 = svc
        .create_payment(sample_vietqr_input(
            &grp1,
            &grp1,
            "buyer-p11",
            "shop-p11",
            250_000,
            12_500,
            0,
            0,
            Some(format!("idem-p11-a-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let shared_tx_id = format!("TX-P11-SHARED-{}", Uuid::new_v4());

    for non_final in ["PENDING", "PROCESSING"] {
        let (code, body) = send_signed_webhook(json!({
            "provider": "vietqr_mock",
            "provider_tx_id": shared_tx_id,
            "payment_id": p1.payment.id.to_string(),
            "order_group_id": grp1,
            "amount_vnd": 250_000,
            "status": non_final,
        }))
        .await;
        assert_eq!(code, StatusCode::OK, "status={non_final} body={body}");
        assert_eq!(
            body["status"],
            json!("ignored"),
            "{non_final} must return ignored"
        );
        assert_eq!(body["duplicate"], json!(false));

        let wh_count: i64 = sqlx::query_scalar(
            "SELECT COUNT(*)::BIGINT FROM webhook_events WHERE provider_tx_id = $1",
        )
        .bind(&shared_tx_id)
        .fetch_one(&db.pool)
        .await
        .unwrap();
        assert_eq!(
            wh_count, 0,
            "{non_final} must NOT insert into webhook_events / claim provider_tx_id"
        );
    }

    let p1_mid = svc
        .get_payment(Some(p1.payment.id), None, None)
        .await
        .unwrap();
    assert_eq!(p1_mid.payment.status, "pending");

    // Now the real SUCCEEDED arrives with the exact same provider_tx_id -> confirms ONCE!
    let (code_ok, body_ok) = send_signed_webhook(json!({
        "provider": "vietqr_mock",
        "provider_tx_id": shared_tx_id,
        "payment_id": p1.payment.id.to_string(),
        "order_group_id": grp1,
        "amount_vnd": 250_000,
        "status": "SUCCEEDED",
    }))
    .await;
    assert_eq!(code_ok, StatusCode::OK);
    assert_eq!(body_ok["status"], json!("processed"));
    assert_eq!(body_ok["duplicate"], json!(false));

    // Replaying SUCCEEDED is still deduplicated idempotently.
    let (code_dup, body_dup) = send_signed_webhook(json!({
        "provider": "vietqr_mock",
        "provider_tx_id": shared_tx_id,
        "payment_id": p1.payment.id.to_string(),
        "order_group_id": grp1,
        "amount_vnd": 250_000,
        "status": "SUCCEEDED",
    }))
    .await;
    assert_eq!(code_dup, StatusCode::OK);
    assert_eq!(body_dup["status"], json!("duplicate_ignored"));
    assert_eq!(body_dup["duplicate"], json!(true));

    let p1_after = svc
        .get_payment(Some(p1.payment.id), None, None)
        .await
        .unwrap();
    assert_eq!(p1_after.payment.status, "succeeded");
    let p1_ledger_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE reference_id = $1",
    )
    .bind(p1.payment.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(p1_ledger_count, 1);
    let p1_outbox_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM outbox_events WHERE aggregate_id = $1 AND event_type = 'payment.succeeded'",
    )
    .bind(&grp1)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(p1_outbox_count, 1);

    // --- Part B: Unexpected status (REVERSED) -> QueueReconciliation(unexpected_webhook_status),
    //     zero ledger postings, zero outbox events ---
    let grp2 = format!("grp-p1-1-b-{}", Uuid::new_v4());
    let p2 = svc
        .create_payment(sample_vietqr_input(
            &grp2,
            &grp2,
            "buyer-p11",
            "shop-p11",
            300_000,
            15_000,
            0,
            0,
            Some(format!("idem-p11-b-{}", Uuid::new_v4())),
        ))
        .await
        .unwrap();
    let rev_tx_id = format!("TX-P11-REV-{}", Uuid::new_v4());

    let (code_rev, body_rev) = send_signed_webhook(json!({
        "provider": "vietqr_mock",
        "provider_tx_id": rev_tx_id,
        "payment_id": p2.payment.id.to_string(),
        "order_group_id": grp2,
        "amount_vnd": 300_000,
        "status": "REVERSED",
    }))
    .await;
    assert_eq!(code_rev, StatusCode::OK);
    assert_eq!(
        body_rev["status"],
        json!("queued_for_reconciliation:unexpected_webhook_status")
    );

    let p2_after = svc
        .get_payment(Some(p2.payment.id), None, None)
        .await
        .unwrap();
    assert_eq!(
        p2_after.payment.status, "pending",
        "REVERSED webhook must NOT confirm payment"
    );

    let recon_reason: String =
        sqlx::query_scalar("SELECT reason FROM reconciliation_queue WHERE provider_tx_id = $1")
            .bind(&rev_tx_id)
            .fetch_one(&db.pool)
            .await
            .expect("REVERSED must enqueue reconciliation_queue row");
    assert_eq!(recon_reason, "unexpected_webhook_status");

    let p2_ledger_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE reference_id = $1",
    )
    .bind(p2.payment.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        p2_ledger_count, 0,
        "REVERSED must post zero ledger transactions"
    );

    let p2_outbox_count: i64 =
        sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM outbox_events WHERE aggregate_id = $1")
            .bind(&grp2)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(p2_outbox_count, 0, "REVERSED must emit zero outbox events");

    db.cleanup().await;
}

// =============================================================================
// 10. P2-7: No AB-BA deadlock (40P01) between `release_escrow` & multi-row alloc UPDATE
// =============================================================================

#[tokio::test]
async fn test_pg_p2_7_release_escrow_no_abba_deadlock_with_multi_alloc_update() {
    let Some(db) = TestDb::setup(
        "test_pg_p2_7_release_escrow_no_abba_deadlock_with_multi_alloc_update",
        8,
    )
    .await
    else {
        return;
    };
    let svc = db.service();

    let grp = format!("grp-p2-7-{}", Uuid::new_v4());
    let ord1 = format!("ord-p2-7-1-{}", Uuid::new_v4());
    let ord2 = format!("ord-p2-7-2-{}", Uuid::new_v4());
    let pwa = svc
        .create_payment(CreatePaymentInput {
            order_group_id: grp.clone(),
            order_id: grp.clone(),
            user_id: "buyer-p27".into(),
            amount_vnd: 400_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![
                ShopAllocationInput {
                    order_id: Some(ord1.clone()),
                    shop_id: "shop-p27-1".into(),
                    subtotal_vnd: 200_000,
                    commission_vnd: 10_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 0,
                    platform_discount_vnd: 0,
                },
                ShopAllocationInput {
                    order_id: Some(ord2.clone()),
                    shop_id: "shop-p27-2".into(),
                    subtotal_vnd: 200_000,
                    commission_vnd: 10_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 0,
                    platform_discount_vnd: 0,
                },
            ],
            expires_at: Some(Utc::now() + ChronoDuration::minutes(15)),
            idempotency_key: Some(format!("idem-p27-{}", Uuid::new_v4())),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p27".into(),
        })
        .await
        .unwrap();

    // Sort the two allocations by UUID so `a1_id < a2_id`.
    let mut alloc_ids: Vec<Uuid> = pwa.allocations.iter().map(|a| a.id).collect();
    alloc_ids.sort();
    let (a1_id, a2_id) = (alloc_ids[0], alloc_ids[1]);
    assert!(a1_id < a2_id);

    // Deterministic AB-BA interleaving:
    // TX1 simulates `confirm_vietqr_payment_tx` having locked `payments` and visiting `a2` (higher
    // UUID, e.g. lower physical ctid) before `a1`.
    // - Without P2-7 fix: `release_escrow(&grp, None)` does NOT lock `payments`, locks `a1` first
    //   (`ORDER BY a.id ASC FOR UPDATE OF a`), then blocks on `a2` held by TX1; when TX1 next runs
    //   `UPDATE payment_shop_allocations ... WHERE payment_id = $1`, PostgreSQL aborts with `40P01`.
    // - With P2-7 fix: `release_escrow` locks `payments` first (`lock_payment_group_tx`), blocks
    //   cleanly on `payments` before touching `a1`, TX1 updates both `a1` & `a2` and commits, and
    //   `release_escrow` returns clean `PaymentError::Conflict` with zero `40P01`!
    let mut tx_confirm = db.pool.begin().await.unwrap();
    let confirm_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *tx_confirm)
        .await
        .unwrap();
    let _: Uuid = sqlx::query_scalar("SELECT id FROM payments WHERE id = $1 FOR UPDATE")
        .bind(pwa.payment.id)
        .fetch_one(&mut *tx_confirm)
        .await
        .unwrap();
    let _: Uuid =
        sqlx::query_scalar("SELECT id FROM payment_shop_allocations WHERE id = $1 FOR UPDATE")
            .bind(a2_id)
            .fetch_one(&mut *tx_confirm)
            .await
            .unwrap();

    let svc_rel = svc.clone();
    let grp_rel = grp.clone();
    let rel_handle = tokio::spawn(async move { svc_rel.release_escrow(&grp_rel, None).await });

    // Wait until `release_escrow` is blocked by `tx_confirm` (on `payments` with fix, or on `a2`
    // after locking `a1` without fix).
    wait_for_blocked_by_pid(&db.pool, confirm_pid, "", 1).await;

    // Now TX1 runs the exact multi-row UPDATE from `confirm_vietqr_payment_tx`: must NOT hit 40P01!
    let update_res = sqlx::query(
        r#"
        UPDATE payment_shop_allocations
        SET escrow_status = 'held'
        WHERE payment_id = $1 AND escrow_status = 'awaiting_collection'
        "#,
    )
    .bind(pwa.payment.id)
    .execute(&mut *tx_confirm)
    .await;
    assert!(
        update_res.is_ok(),
        "TX1 multi-row allocation UPDATE must NOT deadlock (40P01) against release_escrow: {update_res:?}"
    );
    tx_confirm.commit().await.unwrap();

    let rel_res = rel_handle.await.unwrap();
    assert!(
        matches!(rel_res, Err(PaymentError::Conflict(_))),
        "release_escrow on held allocations must return clean Conflict (never 40P01 Database error), got: {rel_res:?}"
    );

    db.cleanup().await;
}

// =============================================================================
// 11. Wave 3b-1 (E20 / E20-bis): COD Carrier CSV Reconciliation, Concurrency,
//     order.delivered Ordering, Bad-Row Isolation, Ledger Entry Counts & Pagination
// =============================================================================

#[tokio::test]
async fn test_pg_w3b_e20_cod_reconciliation_lifecycle_race_and_list_runs() {
    let Some(db) = TestDb::setup(
        "test_pg_w3b_e20_cod_reconciliation_lifecycle_race_and_list_runs",
        16,
    )
    .await
    else {
        return;
    };
    let svc = db.service();
    let grpc = PaymentGrpcServer::new(svc.clone(), db.auth());
    let today = vn_today(Utc::now());

    let admin_tok = sign_test_jwt(
        TEST_USER_SECRET,
        json!({"sub": "admin-w3b", "role": "admin", "exp": Utc::now().timestamp() + 300}),
    );

    // -------------------------------------------------------------------------
    // Scenario 1: Multi-shop COD payment + order.delivered persisting tracking
    //             + order.completed unchanged + mixed CSV (matched, dup same-file
    //             exact, dup same-file 1-VND off, missing_local, bad empty row)
    //             + re-upload same file -> 0 new ledger entries, exact counts!
    // -------------------------------------------------------------------------
    let grp_ms = format!("grp-cod-ms-{}", Uuid::new_v4().simple());
    let ord_a = format!("ord-cod-a-{}", Uuid::new_v4().simple());
    let ord_b = format!("ord-cod-b-{}", Uuid::new_v4().simple());
    let shop_a = format!("shop-cod-a-{}", Uuid::new_v4().simple());
    let shop_b = format!("shop-cod-b-{}", Uuid::new_v4().simple());

    // shop-A payable: 200_000 + 20_000 - 0 = 220_000 (commission 10_000)
    // shop-B payable: 300_000 + 30_000 - 10_000 = 320_000 (commission 15_000)
    let pwa_ms = svc
        .create_payment(CreatePaymentInput {
            order_group_id: grp_ms.clone(),
            order_id: grp_ms.clone(),
            user_id: "buyer-cod-ms".into(),
            amount_vnd: 540_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![
                ShopAllocationInput {
                    order_id: Some(ord_a.clone()),
                    shop_id: shop_a.clone(),
                    subtotal_vnd: 200_000,
                    commission_vnd: 10_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 0,
                },
                ShopAllocationInput {
                    order_id: Some(ord_b.clone()),
                    shop_id: shop_b.clone(),
                    subtotal_vnd: 300_000,
                    commission_vnd: 15_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 30_000,
                    platform_discount_vnd: 10_000,
                },
            ],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{grp_ms}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-cod-ms".into(),
        })
        .await
        .unwrap();

    // Deliver ord_a (HELD) and deliver + complete ord_b (PENDING_RELEASE)
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-del-a-{}", Uuid::new_v4()),
        &ord_a,
        Some("TRK-COD-A"),
        Some("GHN"),
        Some("NM-ORD-A"),
        "tr-del-a",
    )
    .await
    .unwrap();
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-del-b-{}", Uuid::new_v4()),
        &ord_b,
        Some("TRK-COD-B"),
        Some("ghn"),
        Some("NM-ORD-B"),
        "tr-del-b",
    )
    .await
    .unwrap();
    svc.process_order_completed_event(
        &format!("evt-comp-b-{}", Uuid::new_v4()),
        &ord_b,
        "tr-comp-b",
    )
    .await
    .unwrap();

    let bal_b_before = svc.get_seller_balance(&shop_b).await.unwrap();
    assert_eq!(bal_b_before.pending_vnd, 285_000);

    // Import mixed CSV including:
    // line 1: empty tracking_code (bad row — MUST NOT block lines 2..6!)
    // line 2: TRK-COD-A exact 220_000 -> matched
    // line 3: TRK-COD-B exact 320_000 -> matched
    // line 4: TRK-COD-A dup exact 220_000 -> already_reconciled (E20-bis)
    // line 5: TRK-COD-B dup 1 VND off 319_999 -> amount_mismatch + queue (E20-bis)
    // line 6: TRK-UNKNOWN 99_000 -> missing_local + queue
    // line 7: TRK-FAKE with order_code=ord_a (ord_a ALREADY has TRK-COD-A -> MUST NOT fallback!) -> missing_local
    let csv_rows_1 = vec![
        CodReconciliationRowInput {
            line_number: 1,
            tracking_code: "   ".into(),
            cod_amount_vnd: 50_000,
            delivered_at: "not-a-date".into(),
            order_code: String::new(),
        },
        CodReconciliationRowInput {
            line_number: 2,
            tracking_code: "TRK-COD-A".into(),
            cod_amount_vnd: 220_000,
            delivered_at: "2026-11-24T08:00:00Z".into(),
            order_code: String::new(),
        },
        CodReconciliationRowInput {
            line_number: 3,
            tracking_code: "TRK-COD-B".into(),
            cod_amount_vnd: 320_000,
            delivered_at: "2026-11-24T09:00:00Z".into(),
            order_code: String::new(),
        },
        CodReconciliationRowInput {
            line_number: 4,
            tracking_code: "TRK-COD-A".into(),
            cod_amount_vnd: 220_000,
            delivered_at: "2026-11-24T08:00:00Z".into(),
            order_code: String::new(),
        },
        CodReconciliationRowInput {
            line_number: 5,
            tracking_code: "TRK-COD-B".into(),
            cod_amount_vnd: 319_999,
            delivered_at: "2026-11-24T09:00:00Z".into(),
            order_code: String::new(),
        },
        CodReconciliationRowInput {
            line_number: 6,
            tracking_code: "TRK-UNKNOWN".into(),
            cod_amount_vnd: 99_000,
            delivered_at: "2026-11-24T10:00:00Z".into(),
            order_code: String::new(),
        },
        CodReconciliationRowInput {
            line_number: 7,
            tracking_code: "TRK-FAKE".into(),
            cod_amount_vnd: 220_000,
            delivered_at: "2026-11-24T10:00:00Z".into(),
            order_code: ord_a.clone(),
        },
    ];

    let rep1 = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "GHN".into(),
            file_name: "ghn_batch_1.csv".into(),
            file_sha256: "sha256-batch-1".into(),
            run_date: today,
            created_by: "admin-w3b".into(),
            rows: csv_rows_1.clone(),
        })
        .await
        .unwrap();

    assert_eq!(rep1.run.run_type, "cod_file");
    assert_eq!(rep1.run.provider, "ghn");
    assert_eq!(rep1.run.status, "completed");
    assert_eq!(rep1.run.total_rows, 7);
    assert_eq!(rep1.run.matched_count, 2);
    assert_eq!(rep1.run.already_reconciled_count, 1);
    assert_eq!(rep1.run.amount_mismatch_count, 1);
    assert_eq!(rep1.run.missing_local_count, 3); // empty row + TRK-UNKNOWN + TRK-FAKE
    assert_eq!(rep1.run.not_delivered_count, 0);
    assert_eq!(rep1.run.total_matched_vnd, 540_000);
    assert_eq!(rep1.items.len(), 7);
    assert_eq!(rep1.items[0].classification, "missing_local");
    assert_eq!(rep1.items[1].classification, "matched");
    assert_eq!(rep1.items[2].classification, "matched");
    assert_eq!(rep1.items[3].classification, "already_reconciled");
    assert_eq!(rep1.items[4].classification, "amount_mismatch");
    assert_eq!(rep1.items[5].classification, "missing_local");
    assert_eq!(rep1.items[6].classification, "missing_local");

    // Verify EXACT ledger transaction count for cod_reconcile == 2 (one per sub-order/shop)
    let cod_recon_tx_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE reference_type = 'cod_reconcile'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(cod_recon_tx_count, 2);

    // Verify seller_pending for shop-B was NOT double-credited or prematurely released (E20)
    let bal_b_after = svc.get_seller_balance(&shop_b).await.unwrap();
    assert_eq!(bal_b_after.pending_vnd, 285_000);
    assert_eq!(bal_b_after.available_vnd, 0);

    // Verify ledger is strictly balanced and cod_receivable is cleared to 0
    let sum1 = svc.get_ledger_summary(None).await.unwrap();
    assert!(sum1.is_balanced);
    assert_eq!(sum1.total_debits_vnd, sum1.total_credits_vnd);
    let cod_recv_1 = sum1
        .accounts
        .iter()
        .find(|a| a.account_id == "cod_receivable")
        .unwrap()
        .balance_vnd;
    let gw_clr_1 = sum1
        .accounts
        .iter()
        .find(|a| a.account_id == "gateway_clearing")
        .unwrap()
        .balance_vnd;
    assert_eq!(cod_recv_1, 0);
    assert_eq!(gw_clr_1, 540_000);

    // Verify mismatch & missing rows entered reconciliation_queue with source_type = "cod_file"
    let q_resp = grpc
        .list_reconciliation_queue(grpc_req_with_token(
            pb::ListReconciliationQueueRequest {
                status: "pending".into(),
                page: 1,
                page_size: 50,
            },
            &admin_tok,
        ))
        .await
        .unwrap()
        .into_inner();
    assert!(q_resp.items.iter().any(|it| it.source_type == "cod_file"
        && it.tracking_code == "TRK-COD-B"
        && it.reason == "cod_amount_mismatch"));
    assert!(q_resp.items.iter().any(|it| it.source_type == "cod_file"
        && it.tracking_code == "TRK-UNKNOWN"
        && it.reason == "cod_missing_local"));

    // Money-safety guard: VietQR REFUND/ASSIGN must be rejected on cod_file queue items, while SUSPEND works
    let cod_unknown_q_id = Uuid::parse_str(
        &q_resp
            .items
            .iter()
            .find(|it| it.source_type == "cod_file" && it.tracking_code == "TRK-UNKNOWN")
            .unwrap()
            .id,
    )
    .unwrap();
    let refund_cod_q_err = svc
        .resolve_reconciliation_item(ResolveReconciliationInput {
            item_id: cod_unknown_q_id,
            action: ReconciliationActionKind::Refund,
            payment_id: Some(pwa_ms.payment.id),
            note: "must reject".into(),
            idempotency_key: format!("cod-q-ref-{}", Uuid::new_v4()),
            trace_id: "tr-cod-q".into(),
        })
        .await
        .unwrap_err();
    assert!(
        matches!(refund_cod_q_err, PaymentError::Conflict(_)),
        "VietQR REFUND on cod_file queue row must fail with Conflict, got: {refund_cod_q_err:?}"
    );
    let susp_cod_q = svc
        .resolve_reconciliation_item(ResolveReconciliationInput {
            item_id: cod_unknown_q_id,
            action: ReconciliationActionKind::Suspend,
            payment_id: None,
            note: "waiting for carrier confirmation".into(),
            idempotency_key: format!("cod-q-susp-{}", Uuid::new_v4()),
            trace_id: "tr-cod-q".into(),
        })
        .await
        .unwrap();
    assert_eq!(susp_cod_q.item.resolution_status, "suspended");

    // Re-uploading the EXACT SAME CSV file -> matched lines now `already_reconciled`, 0 new ledger txs!
    let rep1_replay = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghn".into(),
            file_name: "ghn_batch_1.csv".into(),
            file_sha256: "sha256-batch-1".into(),
            run_date: today,
            created_by: "admin-w3b".into(),
            rows: csv_rows_1,
        })
        .await
        .unwrap();
    assert_eq!(rep1_replay.run.matched_count, 0);
    assert_eq!(rep1_replay.run.already_reconciled_count, 3); // lines 2, 3, 4
    assert_eq!(rep1_replay.run.amount_mismatch_count, 1); // line 5
    let cod_recon_tx_count_after_replay: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE reference_type = 'cod_reconcile'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        cod_recon_tx_count_after_replay, 2,
        "re-importing same file must post 0 new cod_reconcile ledger entries"
    );

    // -------------------------------------------------------------------------
    // Scenario 2: order.delivered arrives AFTER CSV import (E20 + E20-bis)
    //             + legacy order_code fallback when tracking_code is empty
    // -------------------------------------------------------------------------
    let ord_late = format!("ord-cod-late-{}", Uuid::new_v4().simple());
    let shop_late = format!("shop-cod-late-{}", Uuid::new_v4().simple());
    let _pwa_late = svc
        .create_payment(CreatePaymentInput {
            order_group_id: ord_late.clone(),
            order_id: ord_late.clone(),
            user_id: "buyer-late".into(),
            amount_vnd: 180_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_late.clone()),
                shop_id: shop_late.clone(),
                subtotal_vnd: 160_000,
                commission_vnd: 8_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 20_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_late}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-late".into(),
        })
        .await
        .unwrap();

    // Legacy order delivered WITHOUT tracking_code (tests order_code fallback)
    let ord_legacy = format!("ord-cod-leg-{}", Uuid::new_v4().simple());
    let shop_legacy = format!("shop-cod-leg-{}", Uuid::new_v4().simple());
    let _pwa_legacy = svc
        .create_payment(CreatePaymentInput {
            order_group_id: ord_legacy.clone(),
            order_id: ord_legacy.clone(),
            user_id: "buyer-leg".into(),
            amount_vnd: 150_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_legacy.clone()),
                shop_id: shop_legacy.clone(),
                subtotal_vnd: 130_000,
                commission_vnd: 6_500,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 20_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_legacy}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-leg".into(),
        })
        .await
        .unwrap();
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-del-leg-{}", Uuid::new_v4()),
        &ord_legacy,
        None,
        None,
        None,
        "tr-del-leg",
    )
    .await
    .unwrap();

    let late_file_rows = vec![
        CodReconciliationRowInput {
            line_number: 1,
            tracking_code: "TRK-LATE-DELIVER".into(),
            cod_amount_vnd: 180_000,
            delivered_at: "2026-11-24T11:00:00Z".into(),
            order_code: ord_late.clone(),
        },
        CodReconciliationRowInput {
            line_number: 2,
            tracking_code: "TRK-LEGACY-FB".into(),
            cod_amount_vnd: 150_000,
            delivered_at: "2026-11-24T11:00:00Z".into(),
            order_code: ord_legacy.clone(),
        },
    ];

    // Upload #1 BEFORE order.delivered arrives for ord_late -> ord_late is NOT_DELIVERED + queued,
    // while ord_legacy matches via order_code fallback!
    let rep_late_1 = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghn".into(),
            file_name: "ghn_late.csv".into(),
            file_sha256: "sha256-same-file-reupload".into(),
            run_date: today,
            created_by: "admin-w3b".into(),
            rows: late_file_rows.clone(),
        })
        .await
        .unwrap();
    assert_eq!(rep_late_1.run.not_delivered_count, 1);
    assert_eq!(rep_late_1.run.matched_count, 1);
    assert_eq!(rep_late_1.items[0].classification, "not_delivered");
    assert_eq!(rep_late_1.items[1].classification, "matched");

    let late_q_status_before: String = sqlx::query_scalar(
        "SELECT resolution_status FROM reconciliation_queue WHERE provider_tx_id = 'cod:ghn:TRK-LATE-DELIVER'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(late_q_status_before, "pending");

    // Now order.delivered arrives AFTER file import! Queue entry must still exist (not lost).
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-del-late-{}", Uuid::new_v4()),
        &ord_late,
        Some("TRK-LATE-DELIVER"),
        Some("ghn"),
        Some("NM-LATE-1"),
        "tr-del-late",
    )
    .await
    .unwrap();
    let late_q_still_pending: String = sqlx::query_scalar(
        "SELECT resolution_status FROM reconciliation_queue WHERE provider_tx_id = 'cod:ghn:TRK-LATE-DELIVER'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(late_q_still_pending, "pending");

    // E20-bis: Re-upload the EXACT SAME file (identical file_sha256!) -> MUST NOT skip!
    // ord_late now matches and auto-closes the old queue item; ord_legacy is already_reconciled.
    let rep_late_2 = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghn".into(),
            file_name: "ghn_late.csv".into(),
            file_sha256: "sha256-same-file-reupload".into(),
            run_date: today,
            created_by: "admin-w3b".into(),
            rows: late_file_rows,
        })
        .await
        .unwrap();
    assert_eq!(rep_late_2.run.matched_count, 1);
    assert_eq!(rep_late_2.run.already_reconciled_count, 1);
    assert_eq!(rep_late_2.run.not_delivered_count, 0);
    assert_eq!(rep_late_2.items[0].classification, "matched");
    assert_eq!(rep_late_2.items[1].classification, "already_reconciled");

    let (late_q_status_after, late_q_resolved_at): (String, Option<DateTime<Utc>>) =
        sqlx::query_as(
            "SELECT resolution_status, resolved_at FROM reconciliation_queue WHERE provider_tx_id = 'cod:ghn:TRK-LATE-DELIVER'",
        )
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(
        late_q_status_after, "assigned",
        "E20-bis: old NOT_DELIVERED queue item must auto-close when row later matches"
    );
    assert!(late_q_resolved_at.is_some());

    // -------------------------------------------------------------------------
    // Scenario 3: 8x PARALLEL concurrent imports of the exact same COD file
    //             -> strictly ONE ledger transaction posted, 7 already_reconciled!
    // -------------------------------------------------------------------------
    let ord_race = format!("ord-cod-race-{}", Uuid::new_v4().simple());
    let shop_race = format!("shop-cod-race-{}", Uuid::new_v4().simple());
    let pwa_race = svc
        .create_payment(CreatePaymentInput {
            order_group_id: ord_race.clone(),
            order_id: ord_race.clone(),
            user_id: "buyer-race".into(),
            amount_vnd: 250_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_race.clone()),
                shop_id: shop_race.clone(),
                subtotal_vnd: 220_000,
                commission_vnd: 11_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_race}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-race".into(),
        })
        .await
        .unwrap();
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-del-race-{}", Uuid::new_v4()),
        &ord_race,
        Some("TRK-RACE-PARALLEL"),
        Some("ghtk"),
        None,
        "tr-del-race",
    )
    .await
    .unwrap();

    let n_parallel = 8usize;
    let barrier = Arc::new(Barrier::new(n_parallel));
    let mut handles = Vec::with_capacity(n_parallel);
    for worker_idx in 0..n_parallel {
        let svc_c = svc.clone();
        let bar_c = barrier.clone();
        handles.push(tokio::spawn(async move {
            bar_c.wait().await;
            svc_c
                .import_cod_reconciliation(ImportCodReconciliationInput {
                    carrier_code: "ghtk".into(),
                    file_name: format!("ghtk_race_{worker_idx}.csv"),
                    file_sha256: "sha256-race".into(),
                    run_date: today,
                    created_by: "admin-race".into(),
                    rows: vec![CodReconciliationRowInput {
                        line_number: 1,
                        tracking_code: "TRK-RACE-PARALLEL".into(),
                        cod_amount_vnd: 250_000,
                        delivered_at: "2026-11-24T12:00:00Z".into(),
                        order_code: String::new(),
                    }],
                })
                .await
        }));
    }

    let mut total_matched_parallel = 0_i32;
    let mut total_already_parallel = 0_i32;
    for h in handles {
        let rep = h.await.unwrap().expect("parallel import must succeed");
        total_matched_parallel += rep.run.matched_count;
        total_already_parallel += rep.run.already_reconciled_count;
    }
    assert_eq!(
        total_matched_parallel, 1,
        "parallel imports of same COD file must match exactly ONCE"
    );
    assert_eq!(
        total_already_parallel,
        (n_parallel - 1) as i32,
        "remaining parallel imports must report already_reconciled"
    );

    let race_ledger_key = format!(
        "cod-reconcile:{}:{ord_race}:{shop_race}",
        pwa_race.payment.id
    );
    let race_tx_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE idempotency_key = $1",
    )
    .bind(&race_ledger_key)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(race_tx_count, 1);

    // Total cod_reconcile ledger transactions across ALL scenarios:
    // ord_a + ord_b + ord_legacy + ord_late + ord_race = 5 strictly!
    let total_cod_txs: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE reference_type = 'cod_reconcile'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(total_cod_txs, 5);

    // Final ledger balance check across the entire schema
    let final_summary = svc.get_ledger_summary(None).await.unwrap();
    assert!(final_summary.is_balanced);
    assert_eq!(
        final_summary.total_debits_vnd,
        final_summary.total_credits_vnd
    );
    let final_cod_recv = final_summary
        .accounts
        .iter()
        .find(|a| a.account_id == "cod_receivable")
        .unwrap()
        .balance_vnd;
    assert_eq!(final_cod_recv, 0);

    // -------------------------------------------------------------------------
    // Scenario 4: ListReconciliationRuns pagination & run_type filtering via gRPC
    // -------------------------------------------------------------------------
    let _vietqr_run = svc.run_reconciliation(today, None).await.unwrap();

    // All runs paginated (we have 12 cod_file runs + 1 vietqr run = 13 total)
    let page1 = grpc
        .list_reconciliation_runs(grpc_req_with_token(
            pb::ListReconciliationRunsRequest {
                run_type: String::new(),
                page: 1,
                page_size: 5,
                sales_channel: String::new(),
            },
            &admin_tok,
        ))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(page1.total, 13);
    assert_eq!(page1.runs.len(), 5);

    let page2 = grpc
        .list_reconciliation_runs(grpc_req_with_token(
            pb::ListReconciliationRunsRequest {
                run_type: String::new(),
                page: 2,
                page_size: 5,
                sales_channel: String::new(),
            },
            &admin_tok,
        ))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(page2.total, 13);
    assert_eq!(page2.runs.len(), 5);

    let page3 = grpc
        .list_reconciliation_runs(grpc_req_with_token(
            pb::ListReconciliationRunsRequest {
                run_type: String::new(),
                page: 3,
                page_size: 5,
                sales_channel: String::new(),
            },
            &admin_tok,
        ))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(page3.total, 13);
    assert_eq!(page3.runs.len(), 3);

    // Verify no duplicate run_ids across pages
    let mut seen_ids = std::collections::HashSet::new();
    for r in page1
        .runs
        .iter()
        .chain(page2.runs.iter())
        .chain(page3.runs.iter())
    {
        assert!(
            seen_ids.insert(r.run_id.clone()),
            "duplicate run_id across pages"
        );
    }
    assert_eq!(seen_ids.len(), 13);

    // Filtered by run_type = "vietqr"
    let vietqr_only = grpc
        .list_reconciliation_runs(grpc_req_with_token(
            pb::ListReconciliationRunsRequest {
                run_type: "vietqr".into(),
                page: 1,
                page_size: 10,
                sales_channel: String::new(),
            },
            &admin_tok,
        ))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(vietqr_only.total, 1);
    assert_eq!(vietqr_only.runs.len(), 1);
    assert_eq!(vietqr_only.runs[0].run_type, "vietqr");

    // Filtered by run_type = "cod_file"
    let cod_only = grpc
        .list_reconciliation_runs(grpc_req_with_token(
            pb::ListReconciliationRunsRequest {
                run_type: "cod_file".into(),
                page: 1,
                page_size: 50,
                sales_channel: String::new(),
            },
            &admin_tok,
        ))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(cod_only.total, 12);
    assert_eq!(cod_only.runs.len(), 12);
    assert!(cod_only.runs.iter().all(|r| r.run_type == "cod_file"));

    // Verify gRPC ImportCodReconciliation returns populated run & items fields
    let grpc_imp = grpc
        .import_cod_reconciliation(grpc_req_with_token(
            pb::ImportCodReconciliationRequest {
                carrier_code: "ghn".into(),
                file_name: "grpc_replay.csv".into(),
                file_sha256: "sha-grpc".into(),
                rows: vec![pb::CodReconciliationRow {
                    line_number: 1,
                    tracking_code: "TRK-COD-A".into(),
                    cod_amount_vnd: 220_000,
                    delivered_at: "2026-11-24T08:00:00Z".into(),
                    order_code: String::new(),
                }],
                run_date: today.format("%Y-%m-%d").to_string(),
            },
            &admin_tok,
        ))
        .await
        .unwrap()
        .into_inner();
    let run_pb = grpc_imp.run.expect("run summary");
    assert_eq!(run_pb.run_type, "cod_file");
    assert_eq!(run_pb.provider, "ghn");
    assert_eq!(run_pb.file_name, "grpc_replay.csv");
    assert_eq!(run_pb.created_by, "admin-w3b");
    assert_eq!(run_pb.already_reconciled_count, 1);
    assert_eq!(grpc_imp.items.len(), 1);
    assert_eq!(
        grpc_imp.items[0].status,
        pb::ReconciliationStatus::AlreadyReconciled as i32
    );
    assert_eq!(grpc_imp.items[0].payment_id, pwa_ms.payment.id.to_string());
    assert_eq!(grpc_imp.items[0].tracking_code, "TRK-COD-A");
    assert_eq!(grpc_imp.items[0].order_id, ord_a);
    assert_eq!(grpc_imp.items[0].line_number, 1);

    db.cleanup().await;
}

// =============================================================================
// 12. Wave 3b-1 Review Fixes (4 items):
//     (1) cod_carrier_mismatch on tracking_code & order_code paths (empty carrier still matches)
//     (2) AlreadyReconciled never closes cod_amount_mismatch across 4 identical re-uploads
//     (3) Dropping/cancelling import_cod_reconciliation future mid-run still completes the run
// =============================================================================

#[tokio::test]
async fn test_pg_w3b_r2_cod_carrier_mismatch_queue_persist_and_rpc_cancel() {
    let Some(db) = TestDb::setup(
        "test_pg_w3b_r2_cod_carrier_mismatch_queue_persist_and_rpc_cancel",
        16,
    )
    .await
    else {
        return;
    };
    let svc = db.service();
    let today = vn_today(Utc::now());

    // -------------------------------------------------------------------------
    // (1) Carrier mismatch: tracking_code path + order_code fallback path,
    //     while empty allocation carrier_code still matches cleanly.
    // -------------------------------------------------------------------------
    let ord_cm_track = format!("ord-cm-trk-{}", Uuid::new_v4().simple());
    let shop_cm_track = format!("shop-cm-trk-{}", Uuid::new_v4().simple());
    let _p1 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: ord_cm_track.clone(),
            order_id: ord_cm_track.clone(),
            user_id: "buyer-cm-1".into(),
            amount_vnd: 210_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_cm_track.clone()),
                shop_id: shop_cm_track.clone(),
                subtotal_vnd: 190_000,
                commission_vnd: 9_500,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 20_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_cm_track}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-cm-1".into(),
        })
        .await
        .unwrap();
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-cm-1-{}", Uuid::new_v4()),
        &ord_cm_track,
        Some("TRK-CM-GHN"),
        Some("ghn"),
        Some("NM-CM-1"),
        "tr-cm-1",
    )
    .await
    .unwrap();

    // Order with carrier_code = "ghn" but empty tracking_code -> order_code fallback must ALSO reject wrong carrier!
    let ord_cm_fb = format!("ord-cm-fb-{}", Uuid::new_v4().simple());
    let shop_cm_fb = format!("shop-cm-fb-{}", Uuid::new_v4().simple());
    let _p2 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: ord_cm_fb.clone(),
            order_id: ord_cm_fb.clone(),
            user_id: "buyer-cm-2".into(),
            amount_vnd: 170_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_cm_fb.clone()),
                shop_id: shop_cm_fb.clone(),
                subtotal_vnd: 150_000,
                commission_vnd: 7_500,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 20_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_cm_fb}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-cm-2".into(),
        })
        .await
        .unwrap();
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-cm-2-{}", Uuid::new_v4()),
        &ord_cm_fb,
        None,
        Some("ghn"),
        Some("NM-CM-2"),
        "tr-cm-2",
    )
    .await
    .unwrap();

    // Order with empty carrier_code -> must still match any carrier file as before
    let ord_no_car = format!("ord-nocar-{}", Uuid::new_v4().simple());
    let shop_no_car = format!("shop-nocar-{}", Uuid::new_v4().simple());
    let _p3 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: ord_no_car.clone(),
            order_id: ord_no_car.clone(),
            user_id: "buyer-cm-3".into(),
            amount_vnd: 130_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_no_car.clone()),
                shop_id: shop_no_car.clone(),
                subtotal_vnd: 110_000,
                commission_vnd: 5_500,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 20_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_no_car}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-cm-3".into(),
        })
        .await
        .unwrap();
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-cm-3-{}", Uuid::new_v4()),
        &ord_no_car,
        Some("TRK-NO-CARRIER"),
        None,
        None,
        "tr-cm-3",
    )
    .await
    .unwrap();

    // Import under WRONG carrier "ghtk" for GHN orders + empty-carrier order
    let rep_cm = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghtk".into(),
            file_name: "ghtk_wrong_carrier.csv".into(),
            file_sha256: "sha-cm-1".into(),
            run_date: today,
            created_by: "admin-r2".into(),
            rows: vec![
                CodReconciliationRowInput {
                    line_number: 1,
                    tracking_code: "TRK-CM-GHN".into(),
                    cod_amount_vnd: 210_000,
                    delivered_at: "2026-11-24T08:00:00Z".into(),
                    order_code: String::new(),
                },
                CodReconciliationRowInput {
                    line_number: 2,
                    tracking_code: "TRK-CM-FB-NEW".into(),
                    cod_amount_vnd: 170_000,
                    delivered_at: "2026-11-24T08:00:00Z".into(),
                    order_code: "NM-CM-2".into(),
                },
                CodReconciliationRowInput {
                    line_number: 3,
                    tracking_code: "TRK-NO-CARRIER".into(),
                    cod_amount_vnd: 130_000,
                    delivered_at: "2026-11-24T08:00:00Z".into(),
                    order_code: String::new(),
                },
            ],
        })
        .await
        .unwrap();

    assert_eq!(
        rep_cm.run.matched_count, 1,
        "only empty-carrier allocation should match"
    );
    assert_eq!(
        rep_cm.run.missing_local_count, 2,
        "both wrong-carrier rows must classify missing_local"
    );
    assert_eq!(rep_cm.items[0].classification, "missing_local");
    let d0 = rep_cm.items[0].details.as_deref().unwrap_or("");
    assert!(
        d0.contains("ghn") && d0.contains("ghtk"),
        "tracking_code carrier mismatch details must mention both carriers, got: {d0}"
    );
    assert_eq!(rep_cm.items[1].classification, "missing_local");
    let d1 = rep_cm.items[1].details.as_deref().unwrap_or("");
    assert!(
        d1.contains("ghn") && d1.contains("ghtk"),
        "order_code fallback carrier mismatch details must mention both carriers, got: {d1}"
    );
    assert_eq!(rep_cm.items[2].classification, "matched");

    // Verify both carrier-mismatch items entered reconciliation_queue with reason = 'cod_carrier_mismatch'
    let cm_reasons: Vec<(String, String, String)> = sqlx::query_as(
        r#"
        SELECT provider_tx_id, reason, resolution_status
        FROM reconciliation_queue
        WHERE provider_tx_id IN ('cod:ghtk:TRK-CM-GHN', 'cod:ghtk:TRK-CM-FB-NEW')
        ORDER BY provider_tx_id ASC
        "#,
    )
    .fetch_all(&db.pool)
    .await
    .unwrap();
    assert_eq!(cm_reasons.len(), 2);
    assert_eq!(
        cm_reasons[0],
        (
            "cod:ghtk:TRK-CM-FB-NEW".to_string(),
            "cod_carrier_mismatch".to_string(),
            "pending".to_string()
        )
    );
    assert_eq!(
        cm_reasons[1],
        (
            "cod:ghtk:TRK-CM-GHN".to_string(),
            "cod_carrier_mismatch".to_string(),
            "pending".to_string()
        )
    );

    // Confirm zero cod_reconcile ledger entries posted for the two GHN allocations
    let cm_ledger_cnt: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE reference_type = 'cod_reconcile'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        cm_ledger_cnt, 1,
        "only ord_no_car should have posted a cod_reconcile entry"
    );

    // -------------------------------------------------------------------------
    // (2) Re-uploading same file 4 times (1 exact row + 1 mismatched row for
    //     the SAME tracking_code): cod_amount_mismatch MUST stay `pending` on
    //     all 4 uploads, with strictly 1 ledger transaction!
    // -------------------------------------------------------------------------
    let ord_m4 = format!("ord-m4-{}", Uuid::new_v4().simple());
    let shop_m4 = format!("shop-m4-{}", Uuid::new_v4().simple());
    let p_m4 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: ord_m4.clone(),
            order_id: ord_m4.clone(),
            user_id: "buyer-m4".into(),
            amount_vnd: 250_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_m4.clone()),
                shop_id: shop_m4.clone(),
                subtotal_vnd: 230_000,
                commission_vnd: 11_500,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 20_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_m4}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-m4".into(),
        })
        .await
        .unwrap();
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-m4-{}", Uuid::new_v4()),
        &ord_m4,
        Some("TRK-MISMATCH-4X"),
        Some("ghn"),
        None,
        "tr-m4",
    )
    .await
    .unwrap();

    let rows_4x = vec![
        CodReconciliationRowInput {
            line_number: 1,
            tracking_code: "TRK-MISMATCH-4X".into(),
            cod_amount_vnd: 250_000,
            delivered_at: "2026-11-24T09:00:00Z".into(),
            order_code: String::new(),
        },
        CodReconciliationRowInput {
            line_number: 2,
            tracking_code: "TRK-MISMATCH-4X".into(),
            cod_amount_vnd: 249_000,
            delivered_at: "2026-11-24T09:00:00Z".into(),
            order_code: String::new(),
        },
    ];

    for upload_no in 1..=4 {
        let rep = svc
            .import_cod_reconciliation(ImportCodReconciliationInput {
                carrier_code: "ghn".into(),
                file_name: "ghn_mixed_4x.csv".into(),
                file_sha256: "sha-4x".into(),
                run_date: today,
                created_by: "admin-r2".into(),
                rows: rows_4x.clone(),
            })
            .await
            .unwrap();
        if upload_no == 1 {
            assert_eq!(rep.run.matched_count, 1);
            assert_eq!(rep.run.already_reconciled_count, 0);
        } else {
            assert_eq!(rep.run.matched_count, 0, "upload {upload_no}");
            assert_eq!(rep.run.already_reconciled_count, 1, "upload {upload_no}");
        }
        assert_eq!(rep.run.amount_mismatch_count, 1, "upload {upload_no}");

        let pending_mismatch_cnt: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM reconciliation_queue
            WHERE source_type = 'cod_file'
              AND tracking_code = 'TRK-MISMATCH-4X'
              AND reason = 'cod_amount_mismatch'
              AND resolution_status = 'pending'
            "#,
        )
        .fetch_one(&db.pool)
        .await
        .unwrap();
        assert_eq!(
            pending_mismatch_cnt, 1,
            "upload {upload_no}: cod_amount_mismatch queue row MUST remain pending and never be auto-closed by AlreadyReconciled"
        );
    }

    let m4_key = format!("cod-reconcile:{}:{ord_m4}:{shop_m4}", p_m4.payment.id);
    let m4_tx_cnt: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE idempotency_key = $1",
    )
    .bind(&m4_key)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(m4_tx_cnt, 1);

    // -------------------------------------------------------------------------
    // (3) Caller drops/cancels import_cod_reconciliation future mid-run:
    //     spawned task MUST continue processing all rows and mark run `completed`!
    // -------------------------------------------------------------------------
    let ord_d1 = format!("ord-drop-1-{}", Uuid::new_v4().simple());
    let ord_d2 = format!("ord-drop-2-{}", Uuid::new_v4().simple());
    let shop_d = format!("shop-drop-{}", Uuid::new_v4().simple());
    for (oid, trk, amt) in [
        (&ord_d1, "TRK-DROP-1", 190_000_i64),
        (&ord_d2, "TRK-DROP-2", 240_000_i64),
    ] {
        svc.create_payment(CreatePaymentInput {
            order_group_id: oid.clone(),
            order_id: oid.clone(),
            user_id: "buyer-drop".into(),
            amount_vnd: amt,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(oid.clone()),
                shop_id: shop_d.clone(),
                subtotal_vnd: amt - 20_000,
                commission_vnd: 5_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 20_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{oid}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-drop".into(),
        })
        .await
        .unwrap();
        svc.process_order_delivered_event_with_shipping(
            &format!("evt-drop-{}", Uuid::new_v4()),
            oid,
            Some(trk),
            Some("ghn"),
            None,
            "tr-drop",
        )
        .await
        .unwrap();
    }

    // Hold a FOR UPDATE lock on ord_d1's allocation so the import pauses deterministically after creating the run.
    let mut tx_hold = db.pool.begin().await.unwrap();
    let blocker_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *tx_hold)
        .await
        .unwrap();
    let _: Uuid = sqlx::query_scalar(
        "SELECT id FROM payment_shop_allocations WHERE order_id = $1 FOR UPDATE",
    )
    .bind(&ord_d1)
    .fetch_one(&mut *tx_hold)
    .await
    .unwrap();

    let svc_drop = svc.clone();
    let cancel_file = format!("ghn_cancel_{}.csv", Uuid::new_v4().simple());
    let cancel_file_q = cancel_file.clone();
    let caller_handle = tokio::spawn(async move {
        svc_drop
            .import_cod_reconciliation(ImportCodReconciliationInput {
                carrier_code: "ghn".into(),
                file_name: cancel_file,
                file_sha256: "sha-cancel".into(),
                run_date: today,
                created_by: "admin-cancel".into(),
                rows: vec![
                    CodReconciliationRowInput {
                        line_number: 1,
                        tracking_code: "TRK-DROP-1".into(),
                        cod_amount_vnd: 190_000,
                        delivered_at: "2026-11-24T10:00:00Z".into(),
                        order_code: String::new(),
                    },
                    CodReconciliationRowInput {
                        line_number: 2,
                        tracking_code: "TRK-DROP-2".into(),
                        cod_amount_vnd: 240_000,
                        delivered_at: "2026-11-24T10:00:00Z".into(),
                        order_code: String::new(),
                    },
                ],
            })
            .await
    });

    // Wait until the import's row-1 query is deterministically blocked on `tx_hold`.
    wait_for_blocked_by_pid(&db.pool, blocker_pid, "payment_shop_allocations", 1).await;

    // Simulate gRPC client disconnect / gateway timeout by aborting (dropping) the caller future!
    caller_handle.abort();
    let _ = caller_handle.await;

    // Release the lock: the detached background task must still finish BOTH rows and mark the run `completed`.
    tx_hold.rollback().await.unwrap();

    let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
    let final_run: (String, i32, i64) = loop {
        let row: Option<(String, i32, i64)> = sqlx::query_as(
            r#"
            SELECT status, matched_count, total_matched_vnd
            FROM reconciliation_runs
            WHERE file_name = $1
            LIMIT 1
            "#,
        )
        .bind(&cancel_file_q)
        .fetch_optional(&db.pool)
        .await
        .unwrap();
        if let Some((st, matched, vnd)) = row.as_ref() {
            if st == "completed" {
                break (st.clone(), *matched, *vnd);
            }
        }
        if tokio::time::Instant::now() >= deadline {
            panic!("run stuck after caller future was dropped! last observed={row:?}");
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    };

    assert_eq!(
        final_run,
        ("completed".to_string(), 2, 430_000),
        "detached COD reconciliation run must complete both rows even after caller disconnects"
    );

    let sum = svc.get_ledger_summary(None).await.unwrap();
    assert!(sum.is_balanced);
    assert_eq!(sum.total_debits_vnd, sum.total_credits_vnd);

    db.cleanup().await;
}

// =============================================================================
// 13. Wave 3b-1 Independent Review Fixes (P1 + P2 + P3):
//     (1a) [mismatch, match] same file keeps current-run mismatch pending;
//          previous-file mismatch auto-closes on later-file match
//     (1b) wrong-carrier file then right-carrier file keeps cod_carrier_mismatch pending
//     (2)  late order.delivered after full COD refund populates tracking metadata with 0 extra collect tx
//     (3)  stale running cod_file run > 10m -> failed + late task completion does not overwrite to completed
//     (4)  two different mismatch amounts for same tracking_code preserved + idempotent replay + legacy key
// =============================================================================

#[tokio::test]
async fn test_pg_w3b_r3_indep_review_cod_queue_refunded_stale_and_multi_mismatch() {
    let Some(db) = TestDb::setup(
        "test_pg_w3b_r3_indep_review_cod_queue_refunded_stale_and_multi_mismatch",
        16,
    )
    .await
    else {
        return;
    };
    let svc = db.service();
    let today = vn_today(Utc::now());

    // Helper to create & deliver a single-shop COD order
    let create_and_deliver_cod =
        |oid: String, shop: String, amt: i64, trk: &'static str, carrier: &'static str| {
            let svc_c = svc.clone();
            async move {
                let pwa = svc_c
                    .create_payment(CreatePaymentInput {
                        order_group_id: oid.clone(),
                        order_id: oid.clone(),
                        user_id: format!("buyer-{oid}"),
                        amount_vnd: amt,
                        method: PaymentMethod::Cod,
                        shop_allocations: vec![ShopAllocationInput {
                            order_id: Some(oid.clone()),
                            shop_id: shop,
                            subtotal_vnd: amt - 20_000,
                            commission_vnd: 5_000,
                            sales_channel: String::new(),
                            storefront_shop_id: String::new(),
                            shipping_fee_vnd: 20_000,
                            platform_discount_vnd: 0,
                        }],
                        expires_at: None,
                        idempotency_key: Some(format!("checkout:{oid}")),
                        supersede_previous: false,
                        sales_channel: String::new(),
                        storefront_shop_id: String::new(),
                        trace_id: format!("tr-{oid}"),
                    })
                    .await
                    .unwrap();
                svc_c
                    .process_order_delivered_event_with_shipping(
                        &format!("evt-del-{}", Uuid::new_v4()),
                        &oid,
                        Some(trk),
                        Some(carrier),
                        Some(&format!("NM-{trk}")),
                        "tr-del",
                    )
                    .await
                    .unwrap();
                pwa
            }
        };

    // -------------------------------------------------------------------------
    // (1a) [mismatch, match] in the SAME file: Line 1 mismatch MUST stay pending!
    // -------------------------------------------------------------------------
    let ord_sf = format!("ord-sf-{}", Uuid::new_v4().simple());
    create_and_deliver_cod(ord_sf.clone(), "shop-sf".into(), 200_000, "TRK-SF-1", "ghn").await;

    let rep_sf = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghn".into(),
            file_name: "same_file_mm_then_match.csv".into(),
            file_sha256: "sha-sf-1".into(),
            run_date: today,
            created_by: "admin-r3".into(),
            rows: vec![
                CodReconciliationRowInput {
                    line_number: 1,
                    tracking_code: "TRK-SF-1".into(),
                    cod_amount_vnd: 190_000,
                    delivered_at: "2026-11-24T08:00:00Z".into(),
                    order_code: String::new(),
                },
                CodReconciliationRowInput {
                    line_number: 2,
                    tracking_code: "TRK-SF-1".into(),
                    cod_amount_vnd: 200_000,
                    delivered_at: "2026-11-24T08:00:00Z".into(),
                    order_code: String::new(),
                },
            ],
        })
        .await
        .unwrap();
    assert_eq!(rep_sf.run.amount_mismatch_count, 1);
    assert_eq!(rep_sf.run.matched_count, 1);

    let sf_pending_mm: i64 = sqlx::query_scalar(
        r#"
        SELECT COUNT(*)::BIGINT
        FROM reconciliation_queue
        WHERE source_type = 'cod_file'
          AND tracking_code = 'TRK-SF-1'
          AND reason = 'cod_amount_mismatch'
          AND resolution_status = 'pending'
        "#,
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        sf_pending_mm, 1,
        "(1a) Line 1 mismatch created in the SAME run must stay pending after Line 2 matches"
    );

    // -------------------------------------------------------------------------
    // (1a-bis) Mismatch in File 1, then exact match in File 2 -> File 1 mismatch MUST auto-close!
    // -------------------------------------------------------------------------
    let ord_f12 = format!("ord-f12-{}", Uuid::new_v4().simple());
    create_and_deliver_cod(
        ord_f12.clone(),
        "shop-f12".into(),
        210_000,
        "TRK-F12-1",
        "ghn",
    )
    .await;

    let rep_f1 = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghn".into(),
            file_name: "file1_mismatch.csv".into(),
            file_sha256: "sha-f1".into(),
            run_date: today,
            created_by: "admin-r3".into(),
            rows: vec![CodReconciliationRowInput {
                line_number: 1,
                tracking_code: "TRK-F12-1".into(),
                cod_amount_vnd: 205_000,
                delivered_at: "2026-11-24T08:00:00Z".into(),
                order_code: String::new(),
            }],
        })
        .await
        .unwrap();
    assert_eq!(rep_f1.run.amount_mismatch_count, 1);

    let rep_f2 = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghn".into(),
            file_name: "file2_fixed.csv".into(),
            file_sha256: "sha-f2".into(),
            run_date: today,
            created_by: "admin-r3".into(),
            rows: vec![CodReconciliationRowInput {
                line_number: 1,
                tracking_code: "TRK-F12-1".into(),
                cod_amount_vnd: 210_000,
                delivered_at: "2026-11-24T09:00:00Z".into(),
                order_code: String::new(),
            }],
        })
        .await
        .unwrap();
    assert_eq!(rep_f2.run.matched_count, 1);

    let (f12_status, f12_resolved): (String, Option<DateTime<Utc>>) = sqlx::query_as(
        r#"
        SELECT resolution_status, resolved_at
        FROM reconciliation_queue
        WHERE source_type = 'cod_file'
          AND tracking_code = 'TRK-F12-1'
          AND reason = 'cod_amount_mismatch'
        LIMIT 1
        "#,
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        f12_status, "assigned",
        "(1a-bis) mismatch from earlier File 1 must auto-close when File 2 matches"
    );
    assert!(f12_resolved.is_some());

    // -------------------------------------------------------------------------
    // (1b) Wrong-carrier file (ghtk -> cod_carrier_mismatch) then right-carrier file (ghn -> match):
    //      cod_carrier_mismatch MUST stay pending!
    // -------------------------------------------------------------------------
    let ord_car = format!("ord-car-{}", Uuid::new_v4().simple());
    create_and_deliver_cod(
        ord_car.clone(),
        "shop-car".into(),
        230_000,
        "TRK-CAR-1",
        "ghn",
    )
    .await;

    let rep_wrong_car = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghtk".into(),
            file_name: "wrong_carrier_ghtk.csv".into(),
            file_sha256: "sha-wc".into(),
            run_date: today,
            created_by: "admin-r3".into(),
            rows: vec![CodReconciliationRowInput {
                line_number: 1,
                tracking_code: "TRK-CAR-1".into(),
                cod_amount_vnd: 230_000,
                delivered_at: "2026-11-24T08:00:00Z".into(),
                order_code: String::new(),
            }],
        })
        .await
        .unwrap();
    assert_eq!(rep_wrong_car.run.missing_local_count, 1);

    let rep_right_car = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghn".into(),
            file_name: "right_carrier_ghn.csv".into(),
            file_sha256: "sha-rc".into(),
            run_date: today,
            created_by: "admin-r3".into(),
            rows: vec![CodReconciliationRowInput {
                line_number: 1,
                tracking_code: "TRK-CAR-1".into(),
                cod_amount_vnd: 230_000,
                delivered_at: "2026-11-24T09:00:00Z".into(),
                order_code: String::new(),
            }],
        })
        .await
        .unwrap();
    assert_eq!(rep_right_car.run.matched_count, 1);

    let cm_status: String = sqlx::query_scalar(
        r#"
        SELECT resolution_status
        FROM reconciliation_queue
        WHERE provider_tx_id = 'cod:ghtk:TRK-CAR-1'
          AND reason = 'cod_carrier_mismatch'
        "#,
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        cm_status, "pending",
        "(1b) right-carrier ghn match must NEVER close wrong-carrier ghtk cod_carrier_mismatch"
    );

    // -------------------------------------------------------------------------
    // (2) Late order.delivered arriving AFTER COD order was returned & fully refunded
    //     (payments.status = 'refunded'): must populate tracking_code/carrier_code/order_code,
    //     post 0 extra collect ledger txs, then CSV import clears cod_receivable cleanly!
    // -------------------------------------------------------------------------
    let ord_ref = format!("ord-cod-ref-{}", Uuid::new_v4().simple());
    let shop_ref = format!("shop-cod-ref-{}", Uuid::new_v4().simple());
    let pwa_ref = svc
        .create_payment(CreatePaymentInput {
            order_group_id: ord_ref.clone(),
            order_id: ord_ref.clone(),
            user_id: "buyer-cod-ref".into(),
            amount_vnd: 260_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_ref.clone()),
                shop_id: shop_ref.clone(),
                subtotal_vnd: 240_000,
                commission_vnd: 12_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 20_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_ref}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-cod-ref".into(),
        })
        .await
        .unwrap();

    // Post-delivery return arrives BEFORE order.delivered -> collects COD without tracking_code
    svc.process_order_returned_event(
        &format!("evt-ret-{}", Uuid::new_v4()),
        &ord_ref,
        Some("RETURN_REQUESTED"),
        "tr-ret-ref",
    )
    .await
    .unwrap();

    // Full refund transitions payment.status -> 'refunded' and alloc.escrow_status -> 'refunded'
    svc.create_refund(CreateRefundInput {
        payment_id: Some(pwa_ref.payment.id),
        order_id: ord_ref.clone(),
        amount_vnd: 260_000,
        reason: "buyer returned COD order".into(),
        idempotency_key: format!("order-return-refund:{ord_ref}"),
        trace_id: "tr-rf-ref".into(),
        require_approval_if_large: false,
        requested_by: None,
        created_by_service: false,
    })
    .await
    .unwrap();

    let pay_status_before: String = sqlx::query_scalar("SELECT status FROM payments WHERE id = $1")
        .bind(pwa_ref.payment.id)
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(pay_status_before, "refunded");

    let ledger_cnt_before_late_del: i64 =
        sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM ledger_transactions")
            .fetch_one(&db.pool)
            .await
            .unwrap();
    let succ_outbox_before_late_del: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM outbox_events WHERE event_type = 'payment.succeeded'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();

    // Delayed order.delivered arrives now with tracking_code/carrier_code/order_code!
    svc.process_order_delivered_event_with_shipping(
        &format!("evt-late-del-ref-{}", Uuid::new_v4()),
        &ord_ref,
        Some("TRK-LATE-REFUNDED"),
        Some("ghn"),
        Some("NM-LATE-REF"),
        "tr-late-del-ref",
    )
    .await
    .unwrap();

    let ledger_cnt_after_late_del: i64 =
        sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM ledger_transactions")
            .fetch_one(&db.pool)
            .await
            .unwrap();
    let succ_outbox_after_late_del: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM outbox_events WHERE event_type = 'payment.succeeded'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        ledger_cnt_after_late_del, ledger_cnt_before_late_del,
        "(2) late order.delivered on refunded COD payment must post 0 extra ledger transactions"
    );
    assert_eq!(
        succ_outbox_after_late_del, succ_outbox_before_late_del,
        "(2) late order.delivered on refunded COD payment must emit 0 extra payment.succeeded events"
    );

    let (saved_trk, saved_car, saved_oc): (Option<String>, Option<String>, Option<String>) =
        sqlx::query_as(
            "SELECT tracking_code, carrier_code, order_code FROM payment_shop_allocations WHERE order_id = $1",
        )
        .bind(&ord_ref)
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(saved_trk.as_deref(), Some("TRK-LATE-REFUNDED"));
    assert_eq!(saved_car.as_deref(), Some("ghn"));
    assert_eq!(saved_oc.as_deref(), Some("NM-LATE-REF"));

    // Carrier COD CSV import now finds TRK-LATE-REFUNDED and clears cod_receivable!
    let rep_ref = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghn".into(),
            file_name: "ghn_refunded_cod.csv".into(),
            file_sha256: "sha-ref".into(),
            run_date: today,
            created_by: "admin-r3".into(),
            rows: vec![CodReconciliationRowInput {
                line_number: 1,
                tracking_code: "TRK-LATE-REFUNDED".into(),
                cod_amount_vnd: 260_000,
                delivered_at: "2026-11-24T10:00:00Z".into(),
                order_code: String::new(),
            }],
        })
        .await
        .unwrap();
    assert_eq!(rep_ref.run.matched_count, 1);

    // -------------------------------------------------------------------------
    // (4) Two DIFFERENT mismatch amounts for the same tracking_code (before match)
    //     must BOTH persist without overwriting each other, stay idempotent on replay,
    //     and legacy base-key mismatch rows must also be handled cleanly.
    // -------------------------------------------------------------------------
    let ord_mm2 = format!("ord-mm2-{}", Uuid::new_v4().simple());
    create_and_deliver_cod(
        ord_mm2.clone(),
        "shop-mm2".into(),
        300_000,
        "TRK-MM-TWO",
        "ghn",
    )
    .await;

    let mm2_rows = vec![
        CodReconciliationRowInput {
            line_number: 1,
            tracking_code: "TRK-MM-TWO".into(),
            cod_amount_vnd: 290_000,
            delivered_at: "2026-11-24T08:00:00Z".into(),
            order_code: String::new(),
        },
        CodReconciliationRowInput {
            line_number: 2,
            tracking_code: "TRK-MM-TWO".into(),
            cod_amount_vnd: 280_000,
            delivered_at: "2026-11-24T08:00:00Z".into(),
            order_code: String::new(),
        },
    ];

    for upload in 1..=2 {
        let rep = svc
            .import_cod_reconciliation(ImportCodReconciliationInput {
                carrier_code: "ghn".into(),
                file_name: "two_mismatches.csv".into(),
                file_sha256: "sha-mm2".into(),
                run_date: today,
                created_by: "admin-r3".into(),
                rows: mm2_rows.clone(),
            })
            .await
            .unwrap();
        assert_eq!(rep.run.amount_mismatch_count, 2, "upload {upload}");

        let mm2_entries: Vec<(String, i64)> = sqlx::query_as(
            r#"
            SELECT provider_tx_id, actual_amount_vnd
            FROM reconciliation_queue
            WHERE source_type = 'cod_file'
              AND tracking_code = 'TRK-MM-TWO'
              AND reason = 'cod_amount_mismatch'
              AND resolution_status = 'pending'
            ORDER BY actual_amount_vnd ASC
            "#,
        )
        .fetch_all(&db.pool)
        .await
        .unwrap();
        assert_eq!(
            mm2_entries,
            vec![
                ("cod:ghn:TRK-MM-TWO:mismatch:280000".to_string(), 280_000),
                ("cod:ghn:TRK-MM-TWO:mismatch:290000".to_string(), 290_000),
            ],
            "(4) upload {upload}: both distinct mismatch amounts must persist without overwriting or duplicating"
        );
    }

    // Legacy base-key mismatch row (`cod:ghn:TRK-LEG-MM`, 270_000) + replay same amount + new amount 260_000 + later match 300_000
    let ord_leg_mm = format!("ord-legmm-{}", Uuid::new_v4().simple());
    let pwa_leg_mm = create_and_deliver_cod(
        ord_leg_mm.clone(),
        "shop-legmm".into(),
        300_000,
        "TRK-LEG-MM",
        "ghn",
    )
    .await;
    sqlx::query(
        r#"
        INSERT INTO reconciliation_queue (
            provider, provider_tx_id, expected_payment_id, order_ref,
            expected_amount_vnd, actual_amount_vnd, reason, source_type, tracking_code,
            created_at
        )
        VALUES ('ghn', 'cod:ghn:TRK-LEG-MM', $1, $2, 300000, 270000, 'cod_amount_mismatch', 'cod_file', 'TRK-LEG-MM', NOW() - INTERVAL '5 minutes')
        "#,
    )
    .bind(pwa_leg_mm.payment.id)
    .bind(&ord_leg_mm)
    .execute(&db.pool)
    .await
    .unwrap();

    svc.import_cod_reconciliation(ImportCodReconciliationInput {
        carrier_code: "ghn".into(),
        file_name: "legacy_mm_replay.csv".into(),
        file_sha256: "sha-legmm-1".into(),
        run_date: today,
        created_by: "admin-r3".into(),
        rows: vec![
            CodReconciliationRowInput {
                line_number: 1,
                tracking_code: "TRK-LEG-MM".into(),
                cod_amount_vnd: 270_000,
                delivered_at: "2026-11-24T08:00:00Z".into(),
                order_code: String::new(),
            },
            CodReconciliationRowInput {
                line_number: 2,
                tracking_code: "TRK-LEG-MM".into(),
                cod_amount_vnd: 260_000,
                delivered_at: "2026-11-24T08:00:00Z".into(),
                order_code: String::new(),
            },
        ],
    })
    .await
    .unwrap();

    let leg_mm_entries: Vec<(String, i64)> = sqlx::query_as(
        r#"
        SELECT provider_tx_id, actual_amount_vnd
        FROM reconciliation_queue
        WHERE source_type = 'cod_file'
          AND tracking_code = 'TRK-LEG-MM'
          AND reason = 'cod_amount_mismatch'
          AND resolution_status = 'pending'
        ORDER BY actual_amount_vnd ASC
        "#,
    )
    .fetch_all(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        leg_mm_entries,
        vec![
            ("cod:ghn:TRK-LEG-MM:mismatch:260000".to_string(), 260_000),
            ("cod:ghn:TRK-LEG-MM".to_string(), 270_000),
        ],
        "(4) legacy base-key mismatch must be deduplicated in-place while distinct amount inserts separately"
    );

    // Later file matches both TRK-MM-TWO and TRK-LEG-MM -> auto-closes all prior mismatch rows!
    let rep_close = svc
        .import_cod_reconciliation(ImportCodReconciliationInput {
            carrier_code: "ghn".into(),
            file_name: "close_multi_mm.csv".into(),
            file_sha256: "sha-close-mm".into(),
            run_date: today,
            created_by: "admin-r3".into(),
            rows: vec![
                CodReconciliationRowInput {
                    line_number: 1,
                    tracking_code: "TRK-MM-TWO".into(),
                    cod_amount_vnd: 300_000,
                    delivered_at: "2026-11-24T11:00:00Z".into(),
                    order_code: String::new(),
                },
                CodReconciliationRowInput {
                    line_number: 2,
                    tracking_code: "TRK-LEG-MM".into(),
                    cod_amount_vnd: 300_000,
                    delivered_at: "2026-11-24T11:00:00Z".into(),
                    order_code: String::new(),
                },
            ],
        })
        .await
        .unwrap();
    assert_eq!(rep_close.run.matched_count, 2);

    let still_open_mm: i64 = sqlx::query_scalar(
        r#"
        SELECT COUNT(*)::BIGINT
        FROM reconciliation_queue
        WHERE source_type = 'cod_file'
          AND tracking_code IN ('TRK-MM-TWO', 'TRK-LEG-MM')
          AND resolution_status IN ('pending', 'suspended')
        "#,
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        still_open_mm, 0,
        "all prior mismatch rows (both legacy base-key and :mismatch:<amt>) must auto-close on later-file match"
    );

    // -------------------------------------------------------------------------
    // (3) Stale `running` cod_file runs > 10 minutes -> swept to `failed` + `completed_at`,
    //     and a late-finishing task must NOT silently overwrite `failed` back to `completed`!
    // -------------------------------------------------------------------------
    let stale_cod_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO reconciliation_runs (
            provider, run_date, status, run_type, file_sha256, file_name,
            total_rows, created_by, created_at
        )
        VALUES ('ghn', $1, 'running', 'cod_file', 'sha-stale-old', 'stale_old.csv', 1, 'admin-r3', NOW() - INTERVAL '11 minutes')
        RETURNING id
        "#,
    )
    .bind(today)
    .fetch_one(&db.pool)
    .await
    .unwrap();

    let fresh_cod_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO reconciliation_runs (
            provider, run_date, status, run_type, file_sha256, file_name,
            total_rows, created_by, created_at
        )
        VALUES ('ghn', $1, 'running', 'cod_file', 'sha-stale-fresh', 'fresh.csv', 1, 'admin-r3', NOW() - INTERVAL '2 minutes')
        RETURNING id
        "#,
    )
    .bind(today)
    .fetch_one(&db.pool)
    .await
    .unwrap();

    let old_vietqr_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO reconciliation_runs (
            provider, run_date, status, run_type, file_sha256, file_name,
            total_rows, created_by, created_at
        )
        VALUES ('vietqr', $1, 'running', 'vietqr', '', '', 0, 'system', NOW() - INTERVAL '15 minutes')
        RETURNING id
        "#,
    )
    .bind(today)
    .fetch_one(&db.pool)
    .await
    .unwrap();

    let swept = svc
        .fail_stale_cod_reconciliation_runs(chrono::Duration::minutes(10))
        .await
        .unwrap();
    assert_eq!(
        swept, 1,
        "(3a) sweeper must fail exactly the stale >10m cod_file run"
    );

    let (stale_st, stale_done): (String, Option<DateTime<Utc>>) =
        sqlx::query_as("SELECT status, completed_at FROM reconciliation_runs WHERE id = $1")
            .bind(stale_cod_id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(stale_st, "failed");
    assert!(stale_done.is_some());

    let fresh_st: String =
        sqlx::query_scalar("SELECT status FROM reconciliation_runs WHERE id = $1")
            .bind(fresh_cod_id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(
        fresh_st, "running",
        "(3a) fresh cod_file run must stay running"
    );

    let vietqr_st: String =
        sqlx::query_scalar("SELECT status FROM reconciliation_runs WHERE id = $1")
            .bind(old_vietqr_id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(
        vietqr_st, "running",
        "(3a) non-cod_file vietqr run must not be touched by cod_file sweeper"
    );

    // (3b) Late-finishing task guard: pause an in-flight cod_file run behind a row lock,
    //      age its created_at > 10m, sweep it to `failed`, then unblock it -> MUST stay `failed`!
    let ord_stale = format!("ord-stale-{}", Uuid::new_v4().simple());
    create_and_deliver_cod(
        ord_stale.clone(),
        "shop-stale".into(),
        160_000,
        "TRK-STALE-1",
        "ghn",
    )
    .await;

    let mut tx_hold = db.pool.begin().await.unwrap();
    let blocker_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *tx_hold)
        .await
        .unwrap();
    let _: Uuid = sqlx::query_scalar(
        "SELECT id FROM payment_shop_allocations WHERE order_id = $1 FOR UPDATE",
    )
    .bind(&ord_stale)
    .fetch_one(&mut *tx_hold)
    .await
    .unwrap();

    let svc_stale = svc.clone();
    let late_handle = tokio::spawn(async move {
        svc_stale
            .import_cod_reconciliation(ImportCodReconciliationInput {
                carrier_code: "ghn".into(),
                file_name: "late_finish_guard.csv".into(),
                file_sha256: "sha-late-finish-guard".into(),
                run_date: today,
                created_by: "admin-r3".into(),
                rows: vec![CodReconciliationRowInput {
                    line_number: 1,
                    tracking_code: "TRK-STALE-1".into(),
                    cod_amount_vnd: 160_000,
                    delivered_at: "2026-11-24T12:00:00Z".into(),
                    order_code: String::new(),
                }],
            })
            .await
    });

    wait_for_blocked_by_pid(&db.pool, blocker_pid, "payment_shop_allocations", 1).await;

    let late_run_id: Uuid = sqlx::query_scalar(
        "SELECT id FROM reconciliation_runs WHERE file_sha256 = 'sha-late-finish-guard' AND status = 'running'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE reconciliation_runs SET created_at = NOW() - INTERVAL '11 minutes' WHERE id = $1",
    )
    .bind(late_run_id)
    .execute(&db.pool)
    .await
    .unwrap();

    let swept_late = svc
        .fail_stale_cod_reconciliation_runs(chrono::Duration::minutes(10))
        .await
        .unwrap();
    assert_eq!(swept_late, 1);

    // Release lock so the late-finishing task finishes its row and reaches finalization.
    tx_hold.rollback().await.unwrap();
    let late_rep = late_handle.await.unwrap().unwrap();
    assert_eq!(
        late_rep.run.status, "failed",
        "(3b) late-finishing COD run must not silently overwrite failed status back to completed"
    );
    assert!(late_rep.run.completed_at.is_some());

    let (db_late_status, db_late_completed): (String, Option<DateTime<Utc>>) =
        sqlx::query_as("SELECT status, completed_at FROM reconciliation_runs WHERE id = $1")
            .bind(late_run_id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(
        db_late_status, "failed",
        "(3b) DB status must remain failed after late-finishing task completes"
    );
    assert!(db_late_completed.is_some());

    // Final global ledger integrity check: cod_receivable == 0 & balanced!
    let sum = svc.get_ledger_summary(None).await.unwrap();
    assert!(sum.is_balanced);
    assert_eq!(sum.total_debits_vnd, sum.total_credits_vnd);
    let cod_recv_final = sum
        .accounts
        .iter()
        .find(|a| a.account_id == "cod_receivable")
        .unwrap()
        .balance_vnd;
    assert_eq!(cod_recv_final, 0);

    db.cleanup().await;
}

// =============================================================================
// Wave 3b-2 Phase 1: Migration 0010 upgrade from 0009 with existing data
// =============================================================================

#[tokio::test]
async fn test_pg_migration_0010_upgrade_from_0009_with_existing_data() {
    let db_url = match std::env::var("TEST_PAYMENT_DB_URL") {
        Ok(v) if !v.trim().is_empty() => v,
        _ => {
            eprintln!(
                "skipping test_pg_migration_0010_upgrade_from_0009_with_existing_data: TEST_PAYMENT_DB_URL not set"
            );
            return;
        }
    };

    let _permit = SCHEMA_SEM.acquire().await.expect("schema semaphore");

    EXT_INIT
        .get_or_init(|| async {
            let base = PgPoolOptions::new()
                .max_connections(2)
                .acquire_timeout(Duration::from_secs(10))
                .connect(&db_url)
                .await
                .expect("connect base TEST_PAYMENT_DB_URL");
            let mut conn = base.acquire().await.expect("acquire base conn");
            sqlx::query("SELECT pg_advisory_lock(88442211)")
                .execute(&mut *conn)
                .await
                .expect("advisory lock");
            sqlx::query("CREATE EXTENSION IF NOT EXISTS pgcrypto")
                .execute(&mut *conn)
                .await
                .expect("create pgcrypto");
            sqlx::query("SELECT pg_advisory_unlock(88442211)")
                .execute(&mut *conn)
                .await
                .expect("advisory unlock");
            drop(conn);
            base.close().await;
        })
        .await;

    let schema = format!("paymig10_{}", Uuid::new_v4().simple());
    let base = PgPoolOptions::new()
        .max_connections(2)
        .acquire_timeout(Duration::from_secs(10))
        .connect(&db_url)
        .await
        .expect("connect admin pool");
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&base)
        .await
        .expect("create isolated schema");
    base.close().await;

    let schema_for_hook = schema.clone();
    let pool = PgPoolOptions::new()
        .max_connections(4)
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

    // 1. Apply migrations 0001..0011 only (pre-0012 live database state, including phase-0b 0002..0003).
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
    for sql in pre_migrations {
        pool.execute(sql).await.expect("apply pre-0012 migration");
    }

    // 2. Seed existing data (COD + VIETQR + legacy phase-0b ZALOPAY payments, multiple refunds).
    let existing_vietqr_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO payments (
            order_group_id, order_id, user_id, method, provider, amount_vnd,
            status, provider_order_code, expires_at
        )
        VALUES (
            'grp-pre10-vqr', 'ord-pre10-vqr', 'buyer-pre10', 'VIETQR', 'vietqr_mock',
            250000, 'succeeded', 'NMPRE10VQR', NOW() + INTERVAL '15 minutes'
        )
        RETURNING id
        "#,
    )
    .fetch_one(&pool)
    .await
    .expect("insert existing vietqr payment");

    let existing_cod_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO payments (
            order_group_id, order_id, user_id, method, provider, amount_vnd,
            status, provider_order_code
        )
        VALUES (
            'grp-pre10-cod', 'ord-pre10-cod', 'buyer-pre10', 'COD', 'cod',
            400000, 'pending', 'NMPRE10COD'
        )
        RETURNING id
        "#,
    )
    .fetch_one(&pool)
    .await
    .expect("insert existing cod payment");

    let existing_p0b_zlp_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO payments (
            order_group_id, order_id, user_id, method, provider, amount_vnd,
            status, provider_order_code, qr_url
        )
        VALUES (
            'grp-pre12-zlp', 'ord-pre12-zlp', 'buyer-pre10', 'ZALOPAY', 'zalopay_mock',
            100000, 'pending', 'ZLP_PRE12', 'https://sbgateway.zalopay.vn/openinapp?order=PRE12'
        )
        RETURNING id
        "#,
    )
    .fetch_one(&pool)
    .await
    .expect("insert legacy phase-0b ZALOPAY payment with qr_url");

    sqlx::query(
        r#"
        INSERT INTO refunds (payment_id, order_id, amount_vnd, reason, status, idempotency_key)
        VALUES
            ($1, 'ord-pre10-vqr', 100000, 'legacy-ref-1', 'pending', 'idem-pre10-ref-1'),
            ($1, 'ord-pre10-vqr', 50000, 'legacy-ref-2', 'pending', 'idem-pre10-ref-2')
        "#,
    )
    .bind(existing_vietqr_id)
    .execute(&pool)
    .await
    .expect("insert existing refunds");

    // Pre-0012 payment_fee_configs CHECK must reject ZALOPAY (0009 only allowed COD/VIETQR).
    assert!(
        sqlx::query(
            r#"
            INSERT INTO payment_fee_configs (payment_method, fee_bps, effective_from, created_by, note)
            VALUES ('ZALOPAY', 0, NOW(), 'test', 'pre-0012')
            "#,
        )
        .execute(&pool)
        .await
        .is_err(),
        "pre-0012 payment_fee_configs CHECK must reject ZALOPAY"
    );

    // 3. Apply migration 0012 on the populated database.
    pool.execute(include_str!(
        "../migrations/0012_zalopay_and_method_configs.sql"
    ))
    .await
    .expect("apply migration 0012 on populated 0011 schema");

    // 4. Verify pre-existing data survived cleanly, pay_url backfilled for legacy ZALOPAY,
    //    and duplicate 0003 CHECK/index removed.
    let existing_pay_url: Option<String> =
        sqlx::query_scalar("SELECT pay_url FROM payments WHERE id = $1")
            .bind(existing_cod_id)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(existing_pay_url, None);

    let backfilled_zlp_pay_url: Option<String> =
        sqlx::query_scalar("SELECT pay_url FROM payments WHERE id = $1")
            .bind(existing_p0b_zlp_id)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(
        backfilled_zlp_pay_url.as_deref(),
        Some("https://sbgateway.zalopay.vn/openinapp?order=PRE12")
    );

    let legacy_refund_nulls: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM refunds WHERE provider_m_refund_id IS NULL AND provider_status IS NULL",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(legacy_refund_nulls, 2);

    // 5. Verify ZALOPAY payment + pay_url accepted, invalid method still rejected.
    let zlp_payment_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO payments (
            order_group_id, order_id, user_id, method, provider, amount_vnd,
            status, provider_order_code, pay_url, expires_at
        )
        VALUES (
            'grp-post10-zlp', 'ord-post10-zlp', 'buyer-post10', 'ZALOPAY', 'zalopay_mock',
            320000, 'pending', '261124_zlp001', '/thanh-toan/mock-zalopay?app_trans_id=261124_zlp001',
            NOW() + INTERVAL '15 minutes'
        )
        RETURNING id
        "#,
    )
    .fetch_one(&pool)
    .await
    .expect("post-0010 must accept ZALOPAY payment with pay_url");

    assert!(
        sqlx::query(
            r#"
            INSERT INTO payments (
                order_group_id, order_id, user_id, method, provider, amount_vnd,
                status, provider_order_code
            )
            VALUES ('grp-bad', 'ord-bad', 'u1', 'UNKNOWN_METHOD', 'x', 100000, 'pending', 'BAD_M')
            "#,
        )
        .execute(&pool)
        .await
        .is_err(),
        "payments CHECK must still reject unknown methods"
    );

    // Verify rebuilt partial expiry index includes both VIETQR and ZALOPAY.
    let expiry_idx_def: String = sqlx::query_scalar(
        "SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND indexname = 'idx_payments_pending_expiry'",
    )
    .bind(&schema)
    .fetch_one(&pool)
    .await
    .expect("idx_payments_pending_expiry must exist");
    assert!(
        expiry_idx_def.contains("VIETQR") && expiry_idx_def.contains("ZALOPAY"),
        "idx_payments_pending_expiry must cover VIETQR and ZALOPAY, got: {expiry_idx_def}"
    );

    // 6. Verify refunds.provider_m_refund_id UNIQUE and provider_status CHECK.
    sqlx::query(
        r#"
        INSERT INTO refunds (
            payment_id, order_id, amount_vnd, reason, status, idempotency_key,
            provider_m_refund_id, provider_status
        )
        VALUES (
            $1, 'ord-post10-zlp', 100000, 'zlp-refund', 'pending', 'idem-zlp-ref-1',
            '261124_100001_mref01', 'processing'
        )
        "#,
    )
    .bind(zlp_payment_id)
    .execute(&pool)
    .await
    .expect("insert zalopay refund with provider_m_refund_id and provider_status");

    // Duplicate provider_m_refund_id must fail UNIQUE.
    assert!(
        sqlx::query(
            r#"
            INSERT INTO refunds (
                payment_id, order_id, amount_vnd, reason, status, idempotency_key,
                provider_m_refund_id, provider_status
            )
            VALUES (
                $1, 'ord-post10-zlp', 50000, 'zlp-refund-dup', 'pending', 'idem-zlp-ref-2',
                '261124_100001_mref01', 'pending'
            )
            "#,
        )
        .bind(zlp_payment_id)
        .execute(&pool)
        .await
        .is_err(),
        "duplicate provider_m_refund_id must violate UNIQUE"
    );

    // Invalid provider_status must fail CHECK.
    assert!(
        sqlx::query(
            r#"
            INSERT INTO refunds (
                payment_id, order_id, amount_vnd, reason, status, idempotency_key,
                provider_m_refund_id, provider_status
            )
            VALUES (
                $1, 'ord-post10-zlp', 50000, 'zlp-refund-bad', 'pending', 'idem-zlp-ref-3',
                '261124_100001_mref02', 'invalid_status'
            )
            "#,
        )
        .bind(zlp_payment_id)
        .execute(&pool)
        .await
        .is_err(),
        "invalid refunds.provider_status must violate CHECK"
    );

    // 7. Verify payment_fee_configs seeded ZALOPAY at 0 bps and accepts new ZALOPAY rows.
    let zlp_fee_bps: i32 = sqlx::query_scalar(
        "SELECT fee_bps FROM payment_fee_configs WHERE payment_method = 'ZALOPAY' ORDER BY seq DESC LIMIT 1",
    )
    .fetch_one(&pool)
    .await
    .expect("seeded ZALOPAY fee config");
    assert_eq!(zlp_fee_bps, 0);

    // 8. Verify payment_method_configs seed rows, version=1 (F7), bounds, and optimistic lock.
    let seeded_configs: Vec<(String, bool, i64, i64, i32, i64)> = sqlx::query_as(
        r#"
        SELECT method, enabled, min_order_vnd, max_order_vnd, expiry_minutes, version
        FROM payment_method_configs
        ORDER BY method ASC
        "#,
    )
    .fetch_all(&pool)
    .await
    .expect("query payment_method_configs");

    assert_eq!(
        seeded_configs,
        vec![
            ("COD".to_string(), true, 0, 5_000_000, 0, 1),
            ("VIETQR".to_string(), true, 0, 0, 15, 1),
            ("ZALOPAY".to_string(), true, 0, 0, 15, 1),
        ]
    );

    // Optimistic lock update (F7): expected version=1 succeeds -> bumps to 2; stale version=1 -> 0 rows.
    let updated_v1 = sqlx::query(
        r#"
        UPDATE payment_method_configs
        SET max_order_vnd = 3000000, version = version + 1, updated_by = 'admin-1', updated_at = NOW()
        WHERE method = 'COD' AND version = 1
        "#,
    )
    .execute(&pool)
    .await
    .unwrap()
    .rows_affected();
    assert_eq!(updated_v1, 1);

    let stale_update = sqlx::query(
        r#"
        UPDATE payment_method_configs
        SET max_order_vnd = 2000000, version = version + 1, updated_by = 'admin-2', updated_at = NOW()
        WHERE method = 'COD' AND version = 1
        "#,
    )
    .execute(&pool)
    .await
    .unwrap()
    .rows_affected();
    assert_eq!(stale_update, 0, "stale version update must affect 0 rows");

    // Bounds CHECK constraints: max < min (when max > 0), version < 1, expiry > 1440 all fail.
    assert!(
        sqlx::query(
            "UPDATE payment_method_configs SET min_order_vnd = 500000, max_order_vnd = 100000 WHERE method = 'VIETQR'",
        )
        .execute(&pool)
        .await
        .is_err(),
        "max_order_vnd < min_order_vnd (with max > 0) must fail CHECK"
    );
    assert!(
        sqlx::query("UPDATE payment_method_configs SET version = 0 WHERE method = 'VIETQR'")
            .execute(&pool)
            .await
            .is_err(),
        "version = 0 must fail CHECK"
    );
    assert!(
        sqlx::query(
            "UPDATE payment_method_configs SET expiry_minutes = 1441 WHERE method = 'ZALOPAY'"
        )
        .execute(&pool)
        .await
        .is_err(),
        "expiry_minutes > 1440 must fail CHECK"
    );

    // Cleanup isolated schema.
    pool.close().await;
    if let Ok(admin) = PgPoolOptions::new()
        .max_connections(1)
        .acquire_timeout(Duration::from_secs(10))
        .connect(&db_url)
        .await
    {
        let _ = sqlx::query(&format!("DROP SCHEMA IF EXISTS {schema} CASCADE"))
            .execute(&admin)
            .await;
        admin.close().await;
    }
}
