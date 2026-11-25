use std::sync::Arc;
use std::time::Duration;

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use chrono::{Duration as ChronoDuration, Utc};
use hmac::{Hmac, Mac};
use serde_json::{json, Value};
use sha2::Sha256;
use sqlx::postgres::PgPoolOptions;
use sqlx::{Executor, PgPool};
use tokio::sync::{Notify, OnceCell, Semaphore};
use tonic::metadata::MetadataValue;
use uuid::Uuid;

use nexusmart_payment::auth::GrpcAuth;
use nexusmart_payment::config::{
    Config, VietQrMode, DEFAULT_ZALOPAY_MOCK_APP_ID, DEV_ONLY_ZALOPAY_MOCK_KEY2,
};
use nexusmart_payment::domain::{
    vn_offset, vn_today, CreatePaymentInput, CreateRefundInput, DecideRefundApprovalInput,
    PaymentMethod, ReconciliationActionKind, ResolveReconciliationInput, SettleRefundInput,
    ShopAllocationInput, SubmitRefundBankAccountInput,
};
use nexusmart_payment::error::PaymentError;
use nexusmart_payment::grpc::pb::payment_service_server::PaymentService as PaymentServiceGrpcTrait;
use nexusmart_payment::grpc::{pb, PaymentGrpcServer};
use nexusmart_payment::metrics::Metrics;
use nexusmart_payment::service::{PaymentService, ZaloPayCallbackOutcome};
use nexusmart_payment::vietqr::CircuitBreaker;
use nexusmart_payment::webhook_security::IncomingWebhookPayload;
use nexusmart_payment::zalopay::{
    MockBehavior, MockOperation, MockZaloPay, ZaloPayAdapter, ZaloPayCallbackData,
};

const TEST_USER_SECRET: &str = "p4-pg-user-jwt-secret";
const TEST_SERVICE_SECRET: &str = "p4-pg-service-jwt-secret";
const TEST_WEBHOOK_SECRET: &str = "p4-pg-webhook-secret";

static EXT_INIT: OnceCell<()> = OnceCell::const_new();
static SCHEMA_SEM: Semaphore = Semaphore::const_new(4);

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
                sqlx::query("SELECT pg_advisory_lock(88442298)")
                    .execute(&mut *conn)
                    .await
                    .expect("advisory lock");
                sqlx::query("CREATE EXTENSION IF NOT EXISTS pgcrypto")
                    .execute(&mut *conn)
                    .await
                    .expect("create pgcrypto");
                sqlx::query("SELECT pg_advisory_unlock(88442298)")
                    .execute(&mut *conn)
                    .await
                    .expect("advisory unlock");
                drop(conn);
                base.close().await;
            })
            .await;

        let schema = format!("payp4_{}", Uuid::new_v4().simple());
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

        let schema_hook = schema.clone();
        let pool = PgPoolOptions::new()
            .max_connections(max_conns)
            .acquire_timeout(Duration::from_secs(15))
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
        let mock = MockZaloPay::new();
        let svc = self.service_with_existing_mock(mock.clone());
        (svc, mock)
    }

    fn service_with_existing_mock(&self, mock: MockZaloPay) -> PaymentService {
        let mut cfg = Config::from_env();
        cfg.database_url = self.admin_url.clone();
        cfg.webhook_secret = TEST_WEBHOOK_SECRET.to_string();
        cfg.jwt_secret = TEST_USER_SECRET.to_string();
        cfg.service_jwt_secret = TEST_SERVICE_SECRET.to_string();
        cfg.vietqr_mode = VietQrMode::Mock;
        cfg.seller_hold_hours = 72;
        cfg.large_refund_approval_vnd = 5_000_000;
        let zp = ZaloPayAdapter::new_mock(mock)
            .with_circuit_breaker(CircuitBreaker::new(5, Duration::from_millis(20)));
        PaymentService::new(self.pool.clone(), cfg, Metrics::new()).with_zalopay(zp)
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

fn service_jwt(svc_name: &str) -> String {
    sign_test_jwt(
        TEST_SERVICE_SECRET,
        json!({
            "sub": svc_name,
            "role": "admin",
            "service": svc_name,
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

/// P4 Item A: `cancel_payment` on pending ZALOPAY calls `/v2/query` outside TX:
/// - `return_code=1` -> confirms payment normally and returns `Conflict` (so order watchdog re-reads GetPayment and confirms).
/// - `2`/`3`/network error -> cancels normally and leaves it eligible for p3 late-payment sweep.
#[tokio::test]
async fn test_pg_p4_item_a_cancel_payment_zalopay_query_before_cancel() {
    let Some(db) = TestDb::setup(
        "test_pg_p4_item_a_cancel_payment_zalopay_query_before_cancel",
        8,
    )
    .await
    else {
        return;
    };
    let (svc, mock) = db.service_with_mock();

    // Case 1: ZALOPAY pending was actually paid (`return_code=1`) when order watchdog calls CancelPayment
    let p_paid = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-a1".into(),
            order_id: "ord-p4-a1".into(),
            user_id: "buyer-a1".into(),
            amount_vnd: 350_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p4-a1", "shop-1", 350_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-a1".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-a1".into(),
        })
        .await
        .unwrap();
    mock.set_order_outcome(
        &p_paid.payment.provider_order_code,
        MockBehavior::return_code(1),
    );

    let err = svc
        .cancel_payment(Some(p_paid.payment.id), None, "Payment expired", "tr-a1-c")
        .await
        .unwrap_err();
    assert!(
        matches!(err, PaymentError::Conflict(_)),
        "paid ZALOPAY cancel_payment must confirm and return Conflict, got {err:?}"
    );
    let refreshed = svc
        .get_payment(Some(p_paid.payment.id), None, None)
        .await
        .unwrap();
    assert_eq!(refreshed.payment.status, "succeeded");
    let succ_outbox: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'payment.succeeded' AND payload->>'payment_id' = $1",
    )
    .bind(p_paid.payment.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(succ_outbox, 1);

    // Case 2: ZALOPAY pending with query error (`NetworkError` / `3` / `2`) -> cancels cleanly and stays sweepable by p3
    let p_net = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-a2".into(),
            order_id: "ord-p4-a2".into(),
            user_id: "buyer-a2".into(),
            amount_vnd: 220_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p4-a2", "shop-1", 220_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-a2".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-a2".into(),
        })
        .await
        .unwrap();
    mock.set_order_outcome(
        &p_net.payment.provider_order_code,
        MockBehavior::NetworkError("transient timeout".into()),
    );
    let cancelled = svc
        .cancel_payment(Some(p_net.payment.id), None, "Payment expired", "tr-a2-c")
        .await
        .unwrap();
    assert_eq!(cancelled.payment.status, "cancelled");

    // Later network recovers & p3 late sweep discovers payment was paid!
    tokio::time::sleep(Duration::from_millis(30)).await;
    mock.set_order_outcome(
        &p_net.payment.provider_order_code,
        MockBehavior::return_code(1),
    );
    let stats = svc.scan_unsettled_zalopay_payments().await.unwrap();
    assert_eq!(stats.late_refunds_queued, 1);

    assert!(svc.get_ledger_summary(None).await.unwrap().is_balanced);
    db.cleanup().await;
}

/// P4 Item A1 deterministic race: `scan_expired_payments` gets `return_code = 1` from `/v2/query`
/// outside DB TX, and while HTTP is in flight, `RetryPayment` supersedes the payment out of
/// `pending`. Scanner MUST NOT throw away the confirmed evidence:
/// - Exact amount -> queues 1 `late_payment` refund + 1 balanced ledger tx.
/// - Mismatched amount -> queues 1 `reconciliation_queue` row (`expired_amount_mismatch`), 0 refund.
#[tokio::test]
async fn test_pg_p4_item_a_scan_expired_races_supersede_keeps_payment_evidence() {
    let Some(db) = TestDb::setup(
        "test_pg_p4_item_a_scan_expired_races_supersede_keeps_payment_evidence",
        8,
    )
    .await
    else {
        return;
    };
    let (svc, mock) = db.service_with_mock();

    // Subcase 1: Exact amount payment superseded during `/v2/query`
    let expired_at = Utc::now() - ChronoDuration::minutes(1);
    let p1 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-a1-race".into(),
            order_id: "ord-p4-a1-race".into(),
            user_id: "buyer-a1-race".into(),
            amount_vnd: 330_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p4-a1-race", "shop-1", 330_000),
            expires_at: Some(expired_at),
            idempotency_key: Some("idem-p4-a1-race-zp".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-a1-race-1".into(),
        })
        .await
        .unwrap();
    mock.set_order_outcome(
        &p1.payment.provider_order_code,
        MockBehavior::return_code(1),
    );

    let entered = Arc::new(Notify::new());
    let proceed = Arc::new(Notify::new());
    mock.push_operation_gate(MockOperation::QueryOrder, entered.clone(), proceed.clone());

    let svc_scan = svc.clone();
    let scan_handle = tokio::spawn(async move { svc_scan.scan_expired_payments().await.unwrap() });

    // Wait until `/v2/query` returned `1` outside DB TX, then supersede P1 to COD before TX starts!
    entered.notified().await;
    let _p1_sup = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-a1-race".into(),
            order_id: "ord-p4-a1-race".into(),
            user_id: "buyer-a1-race".into(),
            amount_vnd: 330_000,
            method: PaymentMethod::Cod,
            shop_allocations: single_alloc("ord-p4-a1-race", "shop-1", 330_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-a1-race-cod".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-a1-race-2".into(),
        })
        .await
        .unwrap();
    proceed.notify_one();
    let _ = scan_handle.await.unwrap();

    let late_cnt: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM refunds WHERE payment_id = $1 AND is_late_payment = TRUE",
    )
    .bind(p1.payment.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        late_cnt, 1,
        "Item A1: scanner must route raced return_code=1 to late_payment refund, never discard"
    );
    let p1_final: Option<String> =
        sqlx::query_scalar("SELECT provider_final_status FROM payments WHERE id = $1")
            .bind(p1.payment.id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(p1_final.as_deref(), Some("late_refund_queued"));

    // Subcase 2: Mismatched amount payment cancelled during `/v2/query` -> reconciliation_queue ONCE
    let p2 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-a1-mm".into(),
            order_id: "ord-p4-a1-mm".into(),
            user_id: "buyer-a1-mm".into(),
            amount_vnd: 400_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p4-a1-mm", "shop-1", 400_000),
            expires_at: Some(expired_at),
            idempotency_key: Some("idem-p4-a1-mm".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-a1-mm".into(),
        })
        .await
        .unwrap();
    mock.set_order_outcome(
        &p2.payment.provider_order_code,
        MockBehavior::ReturnCode {
            return_code: 1,
            sub_return_code: 1,
            return_message: "ok".into(),
            amount_vnd: Some(399_000),
            zp_trans_id: Some(261_125_555_111),
            server_time: None,
            is_processing: None,
        },
    );
    let entered2 = Arc::new(Notify::new());
    let proceed2 = Arc::new(Notify::new());
    mock.push_operation_gate(
        MockOperation::QueryOrder,
        entered2.clone(),
        proceed2.clone(),
    );

    let svc_scan2 = svc.clone();
    let scan_handle2 =
        tokio::spawn(async move { svc_scan2.scan_expired_payments().await.unwrap() });
    entered2.notified().await;
    // Move P2 to `cancelled` directly (simulating concurrent cancel during HTTP flight)
    sqlx::query("UPDATE payments SET status = 'cancelled', updated_at = NOW() WHERE id = $1")
        .bind(p2.payment.id)
        .execute(&db.pool)
        .await
        .unwrap();
    proceed2.notify_one();
    let _ = scan_handle2.await.unwrap();

    let rq_mm: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM reconciliation_queue WHERE provider_tx_id = '261125555111' AND reason = 'expired_amount_mismatch'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        rq_mm, 1,
        "Item A1: raced mismatched transfer must be queued into reconciliation_queue"
    );

    assert!(svc.get_ledger_summary(None).await.unwrap().is_balanced);
    db.cleanup().await;
}

/// P4 Items B, C, D:
/// - Item B: Lease duration > worst-case HTTP (default poll does NOT re-claim a 10s-old processing row);
///   duplicate `m_refund_id` error (`return_code=2`) queries `/v2/query_refund` before ever going `manual_review`.
/// - Item C: `settle_refund` returns 409 Conflict when ZaloPay refund is auto-managed (not `manual_review` + `failed`).
/// - Item D: `m_refund_id` crossing GMT+7 midnight rotates BEFORE HTTP when never sent, and only AFTER `-101`
///   `query_refund` confirmation when already sent.
#[tokio::test]
async fn test_pg_p4_item_b_c_d_zalopay_refund_lease_settle_guard_and_day_rollover() {
    let Some(db) = TestDb::setup(
        "test_pg_p4_item_b_c_d_zalopay_refund_lease_settle_guard_and_day_rollover",
        8,
    )
    .await
    else {
        return;
    };
    let (svc, mock) = db.service_with_mock();

    let confirm_zp = |ord: String, zp_tx: i64| {
        let s = svc.clone();
        async move {
            let p = s
                .create_payment(CreatePaymentInput {
                    order_group_id: format!("grp-{ord}"),
                    order_id: ord.clone(),
                    user_id: "buyer-bcd".into(),
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
            s.process_zalopay_callback(cb, "tr-cb").await.unwrap();
            p.payment
        }
    };

    // --- Item B1 + C: In-flight lease blocks 2nd replica, stale CAS cannot roll back, and SettleRefund returns 409 ---
    let p1 = confirm_zp("ord-p4-b1".into(), 261_125_100_001).await;
    let entered_ref = Arc::new(Notify::new());
    let proceed_ref = Arc::new(Notify::new());
    mock.push_behavior(MockOperation::Refund, MockBehavior::return_code(3));
    mock.push_operation_gate(
        MockOperation::Refund,
        entered_ref.clone(),
        proceed_ref.clone(),
    );

    let svc_a = svc.clone();
    let p1_id = p1.id;
    let h_a = tokio::spawn(async move {
        svc_a
            .create_refund(CreateRefundInput {
                payment_id: Some(p1_id),
                order_id: "ord-p4-b1".into(),
                amount_vnd: 250_000,
                reason: "return".into(),
                idempotency_key: "idem-p4-b1".into(),
                trace_id: "tr-p4-b1".into(),
                require_approval_if_large: false,
                requested_by: None,
                created_by_service: false,
            })
            .await
            .unwrap()
    });

    // Wait while Replica A's `/v2/refund` HTTP is actively IN FLIGHT!
    entered_ref.notified().await;

    // Replica B runs default poll AND 0-cooldown poll while Replica A holds the 90s lease -> MUST claim 0!
    assert_eq!(
        svc.poll_zalopay_refunds().await.unwrap(),
        0,
        "Item B: default poll must NOT claim an in-flight leased refund"
    );
    assert_eq!(
        svc.poll_zalopay_refunds_with_cooldown(ChronoDuration::zero())
            .await
            .unwrap(),
        0,
        "Item B: even 0-cooldown poll must NOT claim while in-flight lease is active"
    );
    // Verify lease duration > worst-case HTTP (>= 90s)
    let lease_secs: f64 = sqlx::query_scalar(
        "SELECT EXTRACT(EPOCH FROM (provider_lease_until - NOW()))::FLOAT8 FROM refunds WHERE idempotency_key = 'idem-p4-b1'",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert!(
        lease_secs >= 60.0,
        "Item B: in-flight lease must exceed worst-case HTTP duration, got {lease_secs}s"
    );

    // Let Replica A finish `/v2/refund` (return_code=3, saves provider_refund_id & clears in-flight lease)
    proceed_ref.notify_one();
    let r1 = h_a.await.unwrap();
    assert_eq!(r1.refund.status, "pending");
    assert_eq!(r1.refund.provider_status.as_deref(), Some("processing"));
    assert!(
        r1.refund.provider_refund_id.is_some(),
        "Replica A must persist provider_refund_id on return_code=3"
    );

    // Item C: Manual SettleRefund on auto-managed ZaloPay refund MUST be rejected 409 Conflict!
    let settle_err = svc
        .settle_refund(SettleRefundInput {
            refund_id: r1.refund.id,
            provider_refund_id: "BANK-MANUAL-1".into(),
            note: "manual transfer".into(),
            idempotency_key: "settle-p4-c-blocked".into(),
            trace_id: "tr-p4-c".into(),
        })
        .await
        .unwrap_err();
    assert!(
        matches!(settle_err, PaymentError::Conflict(_)),
        "Item C: manual settle on active ZaloPay refund must return Conflict, got {settle_err:?}"
    );

    // Item B1 (stale CAS protection): Replica B queries refund, receives `-101`, pauses while
    // Replica A takes a new lease / already recorded `provider_refund_id` -> Replica B's `-101`
    // MUST NOT roll `processing` back to `pending`!
    let entered_q = Arc::new(Notify::new());
    let proceed_q = Arc::new(Notify::new());
    mock.push_behavior(
        MockOperation::QueryRefund,
        MockBehavior::ReturnCode {
            return_code: 2,
            sub_return_code: -101,
            return_message: "stale not found".into(),
            amount_vnd: None,
            zp_trans_id: None,
            server_time: None,
            is_processing: None,
        },
    );
    mock.push_operation_gate(
        MockOperation::QueryRefund,
        entered_q.clone(),
        proceed_q.clone(),
    );
    let svc_b_stale = svc.clone();
    let h_b_stale = tokio::spawn(async move {
        svc_b_stale
            .poll_zalopay_refunds_with_options(
                Utc::now(),
                ChronoDuration::zero(),
                ChronoDuration::seconds(90),
            )
            .await
            .unwrap()
    });
    entered_q.notified().await;
    // Even if we clear lease or rotate lease token concurrently, `provider_refund_id IS NOT NULL`
    // AND stale lease token BOTH guard the `-101` reset!
    let new_tok = Uuid::new_v4();
    sqlx::query(
        "UPDATE refunds SET provider_lease_token = $1, provider_lease_until = NOW() + INTERVAL '90 seconds' WHERE id = $2",
    )
    .bind(new_tok)
    .bind(r1.refund.id)
    .execute(&db.pool)
    .await
    .unwrap();
    proceed_q.notify_one();
    let _ = h_b_stale.await.unwrap();

    let (st_after_stale, pst_after_stale, tok_after_stale): (String, Option<String>, Option<Uuid>) =
        sqlx::query_as(
            "SELECT status, provider_status, provider_lease_token FROM refunds WHERE id = $1",
        )
        .bind(r1.refund.id)
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(st_after_stale, "pending");
    assert_eq!(
        pst_after_stale.as_deref(),
        Some("processing"),
        "Item B: stale -101 must NEVER roll processing back to pending"
    );
    assert_eq!(tok_after_stale, Some(new_tok));

    // --- Item B2: Duplicate m_refund_id error (`return_code=2`) checks query_refund BEFORE manual_review ---
    let p2 = confirm_zp("ord-p4-b2".into(), 261_125_100_002).await;
    mock.push_behavior(
        MockOperation::Refund,
        MockBehavior::ReturnCode {
            return_code: 2,
            sub_return_code: -54,
            return_message: "duplicate m_refund_id".into(),
            amount_vnd: None,
            zp_trans_id: None,
            server_time: None,
            is_processing: None,
        },
    );
    mock.push_behavior(MockOperation::QueryRefund, MockBehavior::return_code(1));
    let r2 = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p2.id),
            order_id: "ord-p4-b2".into(),
            amount_vnd: 250_000,
            reason: "dup-check".into(),
            idempotency_key: "idem-p4-b2".into(),
            trace_id: "tr-p4-b2".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .unwrap();
    assert_eq!(
        r2.refund.status, "completed",
        "Item B: duplicate m_refund_id where query_refund=1 must settle, NOT move to manual_review"
    );

    // --- Item D: Never-sent refund with yesterday GMT+7 prefix rotates BEFORE calling /v2/refund ---
    let p3 = confirm_zp("ord-p4-d1".into(), 261_125_100_003).await;
    let r3_id = Uuid::new_v4();
    let old_m_ref = format!("250101_{}_aabbccdd11223344", DEFAULT_ZALOPAY_MOCK_APP_ID);
    sqlx::query(
        r#"
        INSERT INTO refunds (
            id, payment_id, order_id, amount_vnd, reason, status, idempotency_key,
            provider_m_refund_id, provider_status, is_late_payment, affects_escrow
        )
        VALUES ($1, $2, 'ord-p4-d1', 250000, 'late_payment', 'pending', 'late-webhook:261125100003',
                $3, 'pending', TRUE, FALSE)
        "#,
    )
    .bind(r3_id)
    .bind(p3.id)
    .bind(&old_m_ref)
    .execute(&db.pool)
    .await
    .unwrap();
    let polled_d = svc
        .poll_zalopay_refunds_with_cooldown(ChronoDuration::zero())
        .await
        .unwrap();
    assert!(polled_d >= 1);
    let (r3_st, r3_mref): (String, Option<String>) =
        sqlx::query_as("SELECT status, provider_m_refund_id FROM refunds WHERE id = $1")
            .bind(r3_id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    let today_prefix = Utc::now()
        .with_timezone(&vn_offset())
        .format("%y%m%d")
        .to_string();
    assert_eq!(r3_st, "completed");
    assert!(
        r3_mref
            .as_deref()
            .is_some_and(|m| m.starts_with(&today_prefix) && m != old_m_ref),
        "Item D: never-sent yesterday m_refund_id must rotate to today prefix {today_prefix}, got {r3_mref:?}"
    );

    // --- Item D2: Already-dispatched (`provider_dispatch_attempted=TRUE`, `processing`) crossing GMT+7 midnight ---
    // D2a: query_refund returns `-101` (not found) on yesterday's m_refund_id -> rotates to today's m_refund_id in DB TX, then next poll dispatches & settles!
    let p4 = confirm_zp("ord-p4-d2".into(), 261_125_100_004).await;
    let r4_id = Uuid::new_v4();
    let old_sent_mref = format!("250101_{}_11223344aabbccdd", DEFAULT_ZALOPAY_MOCK_APP_ID);
    sqlx::query(
        r#"
        INSERT INTO refunds (
            id, payment_id, order_id, amount_vnd, reason, status, idempotency_key,
            provider_m_refund_id, provider_status, provider_dispatch_attempted,
            is_late_payment, affects_escrow
        )
        VALUES ($1, $2, 'ord-p4-d2', 250000, 'late_payment', 'pending', 'late-webhook:261125100004',
                $3, 'processing', TRUE, TRUE, FALSE)
        "#,
    )
    .bind(r4_id)
    .bind(p4.id)
    .bind(&old_sent_mref)
    .execute(&db.pool)
    .await
    .unwrap();

    // Tick 1: MUST query_refund on yesterday's m_refund_id FIRST (NOT call /v2/refund yet!)
    let _ = svc
        .poll_zalopay_refunds_with_cooldown(ChronoDuration::zero())
        .await
        .unwrap();
    assert_eq!(
        mock.query_refund_calls(&old_sent_mref),
        1,
        "Item D2: already-sent yesterday m_refund_id must query_refund original ID first"
    );
    assert_eq!(mock.refund_calls(&old_sent_mref), 0);
    let (r4_pst_mid, r4_mref_mid): (Option<String>, Option<String>) =
        sqlx::query_as("SELECT provider_status, provider_m_refund_id FROM refunds WHERE id = $1")
            .bind(r4_id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(r4_pst_mid.as_deref(), Some("pending"));
    let rotated_mref = r4_mref_mid.expect("rotated m_refund_id persisted in DB");
    assert!(
        rotated_mref.starts_with(&today_prefix) && rotated_mref != old_sent_mref,
        "Item D2: after -101, m_refund_id must rotate to today prefix {today_prefix}, got {rotated_mref}"
    );

    // Tick 2: dispatches `/v2/refund` with the newly persisted today's m_refund_id -> completed!
    let _ = svc
        .poll_zalopay_refunds_with_cooldown(ChronoDuration::zero())
        .await
        .unwrap();
    assert_eq!(mock.refund_calls(&rotated_mref), 1);
    let r4_final_st: String = sqlx::query_scalar("SELECT status FROM refunds WHERE id = $1")
        .bind(r4_id)
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(r4_final_st, "completed");

    // D2b: Already-dispatched yesterday m_refund_id that ACTUALLY succeeded (`query_refund=1`) settles WITHOUT rotating!
    let p5 = confirm_zp("ord-p4-d3".into(), 261_125_100_005).await;
    let r5_id = Uuid::new_v4();
    let old_ok_mref = format!("250101_{}_5566778899aabbcc", DEFAULT_ZALOPAY_MOCK_APP_ID);
    sqlx::query(
        r#"
        INSERT INTO refunds (
            id, payment_id, order_id, amount_vnd, reason, status, idempotency_key,
            provider_m_refund_id, provider_status, provider_dispatch_attempted,
            is_late_payment, affects_escrow
        )
        VALUES ($1, $2, 'ord-p4-d3', 250000, 'late_payment', 'pending', 'late-webhook:261125100005',
                $3, 'processing', TRUE, TRUE, FALSE)
        "#,
    )
    .bind(r5_id)
    .bind(p5.id)
    .bind(&old_ok_mref)
    .execute(&db.pool)
    .await
    .unwrap();
    mock.set_query_refund_outcome(&old_ok_mref, MockBehavior::return_code(1));
    let _ = svc
        .poll_zalopay_refunds_with_cooldown(ChronoDuration::zero())
        .await
        .unwrap();
    let (r5_st, r5_mref): (String, Option<String>) =
        sqlx::query_as("SELECT status, provider_m_refund_id FROM refunds WHERE id = $1")
            .bind(r5_id)
            .fetch_one(&db.pool)
            .await
            .unwrap();
    assert_eq!(r5_st, "completed");
    assert_eq!(
        r5_mref.as_deref(),
        Some(old_ok_mref.as_str()),
        "Item D2b: succeeded yesterday refund must settle without rotating"
    );

    db.cleanup().await;
}

/// P4 Items E, F, J, O:
/// - Item E: `order-return-refund:` replay with different amount -> 409 Conflict;
///   `order-cancel-refund:` only skips amount check when existing row was created by service.
/// - Item F: 72h escrow hold does NOT release while allocation has a `pending_approval` refund;
///   after decision (reject), next sweep releases cleanly.
/// - Item J: `submit_refund_bank_account` accepts `manual_review` for owner (keeps ownership & validation).
/// - Item O: `payment.refunded` and `payment.refund_rejected` outbox payloads include `idempotency_key`.
#[tokio::test]
async fn test_pg_p4_item_e_f_j_o_refund_guards_escrow_hold_bank_manual_review_and_outbox_key() {
    let Some(db) = TestDb::setup(
        "test_pg_p4_item_e_f_j_o_refund_guards_escrow_hold_bank_manual_review_and_outbox_key",
        8,
    )
    .await
    else {
        return;
    };
    let (svc, _) = db.service_with_mock();
    let grpc = PaymentGrpcServer::new(svc.clone(), db.auth());

    let confirm_qr = |ord: &str, amt: i64, tx_id: &str| {
        let s = svc.clone();
        let o = ord.to_string();
        let t = tx_id.to_string();
        async move {
            let p = s
                .create_payment(CreatePaymentInput {
                    order_group_id: format!("grp-{o}"),
                    order_id: o.clone(),
                    user_id: "buyer-efjo".into(),
                    amount_vnd: amt,
                    method: PaymentMethod::VietQr,
                    shop_allocations: single_alloc(&o, "shop-efjo", amt),
                    expires_at: None,
                    idempotency_key: Some(format!("idem-{o}")),
                    supersede_previous: false,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    trace_id: format!("tr-{o}"),
                })
                .await
                .unwrap();
            s.process_verified_webhook(
                IncomingWebhookPayload {
                    provider: Some("vietqr_mock".into()),
                    provider_tx_id: t,
                    payment_id: Some(p.payment.id.to_string()),
                    order_group_id: Some(p.payment.order_group_id.clone()),
                    order_id: Some(o),
                    provider_order_code: Some(p.payment.provider_order_code.clone()),
                    reference: None,
                    amount_vnd: amt,
                    status: "SUCCEEDED".into(),
                    occurred_at: Some(Utc::now().to_rfc3339()),
                },
                json!({"ok": true}),
                "vietqr_mock",
                "tr-wh",
            )
            .await
            .unwrap();
            p.payment
        }
    };

    // --- Item E: order-return-refund: replay with different amount -> Conflict ---
    let p_e = confirm_qr("ord-p4-e", 1_000_000, "QR-P4-E").await;
    let svc_tok = service_jwt("order-service");
    let _ = grpc
        .create_refund(grpc_req_with_token(
            pb::CreateRefundRequest {
                payment_id: p_e.id.to_string(),
                order_id: "ord-p4-e".into(),
                amount_vnd: 500_000,
                reason: "return".into(),
                idempotency_key: "order-return-refund:ord-p4-e".into(),
                require_approval_if_large: false,
                requested_by: String::new(),
            },
            &svc_tok,
        ))
        .await
        .unwrap();
    let replay_diff_amt = grpc
        .create_refund(grpc_req_with_token(
            pb::CreateRefundRequest {
                payment_id: p_e.id.to_string(),
                order_id: "ord-p4-e".into(),
                amount_vnd: 400_000,
                reason: "return".into(),
                idempotency_key: "order-return-refund:ord-p4-e".into(),
                require_approval_if_large: false,
                requested_by: String::new(),
            },
            &svc_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(
        replay_diff_amt.code(),
        tonic::Code::AlreadyExists,
        "Item E: order-return-refund: must reject different amount on replay"
    );

    // Item O: Verify payment.refunded payload carries idempotency_key
    let refunded_payload: Value = sqlx::query_scalar(
        "SELECT payload FROM outbox_events WHERE event_type = 'payment.refunded' AND payload->>'order_id' = 'ord-p4-e' LIMIT 1",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        refunded_payload["idempotency_key"], "order-return-refund:ord-p4-e",
        "Item O: payment.refunded must include idempotency_key"
    );

    // Item E2: `order-cancel-refund:` created by service token ALLOWS differing amount on replay;
    // `order.cancelled` consumer also sets created_by_service=true AND emits `idempotency_key` (Item O)!
    let p_ec = confirm_qr("ord-p4-ec", 300_000, "QR-P4-EC").await;
    svc.process_order_cancelled_event("evt-p4-ec", "ord-p4-ec", "tr-p4-ec")
        .await
        .unwrap();
    let cancel_outbox: Value = sqlx::query_scalar(
        "SELECT payload FROM outbox_events WHERE event_type = 'payment.refunded' AND payload->>'order_id' = 'ord-p4-ec' LIMIT 1",
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        cancel_outbox["idempotency_key"], "order-cancel-refund:ord-p4-ec",
        "Item O: consumer payment.refunded must include idempotency_key"
    );
    let cancel_replay = grpc
        .create_refund(grpc_req_with_token(
            pb::CreateRefundRequest {
                payment_id: p_ec.id.to_string(),
                order_id: "ord-p4-ec".into(),
                amount_vnd: 250_000, // differs from 300k; allowed ONLY for service-created order-cancel-refund:
                reason: "cancel_sync".into(),
                idempotency_key: "order-cancel-refund:ord-p4-ec".into(),
                require_approval_if_large: false,
                requested_by: String::new(),
            },
            &svc_tok,
        ))
        .await
        .expect("Item E: service replay on service-created order-cancel-refund: must succeed")
        .into_inner();
    assert!(cancel_replay.already_existed);

    // Item O (approve branch): approved large refund also emits `payment.refunded` with `idempotency_key`!
    let p_oa = confirm_qr("ord-p4-oa", 6_000_000, "QR-P4-OA").await;
    let pa_ok = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p_oa.id),
            order_id: "ord-p4-oa".into(),
            amount_vnd: 6_000_000,
            reason: "approve_test".into(),
            idempotency_key: "idem-p4-oa-approve".into(),
            trace_id: "tr-p4-oa".into(),
            require_approval_if_large: true,
            requested_by: Some("cskh-maker".into()),
            created_by_service: false,
        })
        .await
        .unwrap();
    svc.decide_refund_approval(DecideRefundApprovalInput {
        refund_id: pa_ok.refund.id,
        approve: true,
        note: "approved".into(),
        checker_user_id: "acc-checker".into(),
        trace_id: "tr-p4-oa-dec".into(),
    })
    .await
    .unwrap();
    let approved_outbox: Value = sqlx::query_scalar(
        "SELECT payload FROM outbox_events WHERE event_type = 'payment.refunded' AND payload->>'refund_id' = $1",
    )
    .bind(pa_ok.refund.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        approved_outbox["idempotency_key"], "idem-p4-oa-approve",
        "Item O: approved payment.refunded must include idempotency_key"
    );

    // --- Item F + O: Matured 72h allocation blocked while pending_approval exists, releases after reject ---
    let p_f = confirm_qr("ord-p4-f", 6_000_000, "QR-P4-F").await;
    svc.settle_order_to_pending("ord-p4-f").await.unwrap();
    sqlx::query(
        "UPDATE payment_shop_allocations SET settled_at = NOW() - INTERVAL '80 hours' WHERE payment_id = $1",
    )
    .bind(p_f.id)
    .execute(&db.pool)
    .await
    .unwrap();

    let pa = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(p_f.id),
            order_id: "ord-p4-f".into(),
            amount_vnd: 6_000_000,
            reason: "large_dispute".into(),
            idempotency_key: "idem-p4-f-pa".into(),
            trace_id: "tr-p4-f-pa".into(),
            require_approval_if_large: true,
            requested_by: Some("cskh-maker".into()),
            created_by_service: false,
        })
        .await
        .unwrap();
    assert_eq!(pa.refund.status, "pending_approval");

    let released_while_pa = svc.release_matured_escrow_holds().await.unwrap();
    assert_eq!(
        released_while_pa, 0,
        "Item F: matured allocation with pending_approval refund must NOT be released"
    );
    let bal_before = svc.get_seller_balance("shop-efjo").await.unwrap();
    assert_eq!(bal_before.available_vnd, 0);

    // Reject pending_approval -> emits payment.refund_rejected WITH idempotency_key (Item O)
    svc.decide_refund_approval(DecideRefundApprovalInput {
        refund_id: pa.refund.id,
        approve: false,
        note: "rejected".into(),
        checker_user_id: "acc-checker".into(),
        trace_id: "tr-p4-f-rej".into(),
    })
    .await
    .unwrap();
    let rej_payload: Value = sqlx::query_scalar(
        "SELECT payload FROM outbox_events WHERE event_type = 'payment.refund_rejected' AND payload->>'refund_id' = $1",
    )
    .bind(pa.refund.id.to_string())
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        rej_payload["idempotency_key"], "idem-p4-f-pa",
        "Item O: payment.refund_rejected must include idempotency_key"
    );

    // Next sweep releases seller_pending -> seller_available cleanly!
    let released_after = svc.release_matured_escrow_holds().await.unwrap();
    assert_eq!(released_after, 1);
    let bal_after = svc.get_seller_balance("shop-efjo").await.unwrap();
    assert!(bal_after.available_vnd > 0);

    // --- Item J: submit_refund_bank_account accepts `manual_review` for owner ---
    let mr_id = Uuid::new_v4();
    sqlx::query(
        r#"
        INSERT INTO refunds (id, payment_id, order_id, amount_vnd, reason, status, idempotency_key)
        VALUES ($1, $2, 'ord-p4-f', 100000, 'zp_failed', 'manual_review', 'idem-p4-j-mr')
        "#,
    )
    .bind(mr_id)
    .bind(p_f.id)
    .execute(&db.pool)
    .await
    .unwrap();

    let updated_bank = svc
        .submit_refund_bank_account(SubmitRefundBankAccountInput {
            refund_id: mr_id,
            caller_user_id: "buyer-efjo".into(),
            bank_code: "VCB".into(),
            bank_account_number: "0011009988776".into(),
            bank_account_name: "NGUYEN VAN A".into(),
        })
        .await
        .expect("Item J: submit_refund_bank_account must allow manual_review for owner");
    assert_eq!(updated_bank.bank_code.as_deref(), Some("VCB"));
    assert!(updated_bank.bank_account_masked.is_some());

    // Non-owner still Forbidden on manual_review
    let stranger_err = svc
        .submit_refund_bank_account(SubmitRefundBankAccountInput {
            refund_id: mr_id,
            caller_user_id: "buyer-other".into(),
            bank_code: "VCB".into(),
            bank_account_number: "0011009988776".into(),
            bank_account_name: "NGUYEN VAN A".into(),
        })
        .await
        .unwrap_err();
    assert!(matches!(stranger_err, PaymentError::Forbidden(_)));

    db.cleanup().await;
}

/// P4 Items G, H, I, K:
/// - Item G: `webhook_events.provider_tx_id` collision (ZaloPay->VietQR, VietQR->ZaloPay, same-provider diff payment) -> reconciliation_queue ONCE.
/// - Item H: VietQR webhook matches ONLY `method='VIETQR'` (fallback `order_group_id` finds superseded VIETQR -> late refund); `resolve_reconciliation_item(ASSIGN)` verifies provider matches method.
/// - Item I: Mock VietQR reconciliation excludes `zalopay%` webhooks.
/// - Item K: `create_payment(supersede_previous=true)` guards (old online, same amount, no held/settled allocs, new expiry <= old expiry; valid order switches still pass).
#[tokio::test]
async fn test_pg_p4_item_g_h_i_k_collision_method_match_mock_recon_and_supersede_guards() {
    let Some(db) = TestDb::setup(
        "test_pg_p4_item_g_h_i_k_collision_method_match_mock_recon_and_supersede_guards",
        8,
    )
    .await
    else {
        return;
    };
    let (svc, _) = db.service_with_mock();

    // --- Item G: Cross-provider & cross-payment provider_tx_id collisions ---
    let p_g_zp = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-g1".into(),
            order_id: "ord-p4-g1".into(),
            user_id: "buyer-g".into(),
            amount_vnd: 250_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p4-g1", "shop-1", 250_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-g1".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-g1".into(),
        })
        .await
        .unwrap();
    let p_g_qr = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-g2".into(),
            order_id: "ord-p4-g2".into(),
            user_id: "buyer-g".into(),
            amount_vnd: 150_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p4-g2", "shop-1", 150_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-g2".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-g2".into(),
        })
        .await
        .unwrap();

    // Confirm ZaloPay P1 with zp_trans_id = 261125000777
    let cb1 = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &p_g_zp.payment.provider_order_code,
        250_000,
        261_125_000_777,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let out1 = svc.process_zalopay_callback(cb1, "tr-g-cb1").await.unwrap();
    assert!(matches!(out1, ZaloPayCallbackOutcome::Confirmed { .. }));

    // Collision 1 (ZaloPay -> VietQR): VietQR webhook arrives for P2 with SAME provider_tx_id "261125000777"!
    let wh_col = IncomingWebhookPayload {
        provider: Some("vietqr_mock".into()),
        provider_tx_id: "261125000777".into(),
        payment_id: Some(p_g_qr.payment.id.to_string()),
        order_group_id: Some("grp-p4-g2".into()),
        order_id: Some("ord-p4-g2".into()),
        provider_order_code: Some(p_g_qr.payment.provider_order_code.clone()),
        reference: None,
        amount_vnd: 150_000,
        status: "SUCCEEDED".into(),
        occurred_at: Some(Utc::now().to_rfc3339()),
    };
    let col_res1 = svc
        .process_verified_webhook(
            wh_col.clone(),
            json!({"col": 1}),
            "vietqr_mock",
            "tr-g-col1",
        )
        .await
        .unwrap();
    assert!(
        !col_res1.duplicate,
        "Item G: cross-provider collision must NOT be silent duplicate"
    );
    // Replay collision -> still strictly 1 row in reconciliation_queue
    let _ = svc
        .process_verified_webhook(wh_col, json!({"col": 1}), "vietqr_mock", "tr-g-col1-rep")
        .await
        .unwrap();
    let col_cnt1: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM reconciliation_queue WHERE expected_payment_id = $1 AND reason = 'provider_tx_id_collision'",
    )
    .bind(p_g_qr.payment.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(col_cnt1, 1, "Item G: collision must enqueue strictly once");

    // Confirm VietQR P2 with its own real tx id "261125000888", then collide from ZaloPay P3!
    svc.process_verified_webhook(
        IncomingWebhookPayload {
            provider: Some("vietqr_mock".into()),
            provider_tx_id: "261125000888".into(),
            payment_id: Some(p_g_qr.payment.id.to_string()),
            order_group_id: Some("grp-p4-g2".into()),
            order_id: Some("ord-p4-g2".into()),
            provider_order_code: Some(p_g_qr.payment.provider_order_code.clone()),
            reference: None,
            amount_vnd: 150_000,
            status: "SUCCEEDED".into(),
            occurred_at: Some(Utc::now().to_rfc3339()),
        },
        json!({"ok": true}),
        "vietqr_mock",
        "tr-g-qr-ok",
    )
    .await
    .unwrap();

    let p_g_zp2 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-g3".into(),
            order_id: "ord-p4-g3".into(),
            user_id: "buyer-g".into(),
            amount_vnd: 200_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p4-g3", "shop-1", 200_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-g3".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-g3".into(),
        })
        .await
        .unwrap();
    let cb_col2 = build_signed_zp_callback(
        DEFAULT_ZALOPAY_MOCK_APP_ID,
        &p_g_zp2.payment.provider_order_code,
        200_000,
        261_125_000_888,
        Utc::now().timestamp_millis(),
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
    );
    let out_col2 = svc
        .process_zalopay_callback(cb_col2, "tr-g-col2")
        .await
        .unwrap();
    assert!(
        matches!(
            out_col2,
            ZaloPayCallbackOutcome::QueuedReconciliation { ref reason, .. }
                if reason == "provider_tx_id_collision"
        ),
        "Item G: VietQR->ZaloPay collision must queue reconciliation, got {out_col2:?}"
    );

    // --- Item I: Mock VietQR reconciliation excludes zalopay% webhooks ---
    let recon = svc
        .run_reconciliation(vn_today(Utc::now()), None)
        .await
        .unwrap();
    assert_eq!(
        recon.run.matched_count, 1,
        "Item I: mock VietQR reconciliation must count ONLY the 1 VIETQR payment (exclude zalopay_mock)"
    );
    assert_eq!(recon.run.total_matched_vnd, 150_000);

    // --- Item H: VietQR webhook with order_group_id after VIETQR->ZALOPAY switch matches old VIETQR -> late refund ---
    let p_h_qr = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-h".into(),
            order_id: "ord-p4-h".into(),
            user_id: "buyer-h".into(),
            amount_vnd: 300_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p4-h", "shop-1", 300_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-h-qr".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-h-1".into(),
        })
        .await
        .unwrap();
    let p_h_zp = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-h".into(),
            order_id: "ord-p4-h".into(),
            user_id: "buyer-h".into(),
            amount_vnd: 300_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: single_alloc("ord-p4-h", "shop-1", 300_000),
            expires_at: p_h_qr.payment.expires_at,
            idempotency_key: Some("idem-p4-h-zp".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-h-2".into(),
        })
        .await
        .unwrap();

    // VietQR webhook arrives with ONLY order_group_id (no payment_id / order_code)
    let wh_h = svc
        .process_verified_webhook(
            IncomingWebhookPayload {
                provider: Some("vietqr_mock".into()),
                provider_tx_id: "QR-P4-H-LATE".into(),
                payment_id: None,
                order_group_id: Some("grp-p4-h".into()),
                order_id: None,
                provider_order_code: None,
                reference: None,
                amount_vnd: 300_000,
                status: "SUCCEEDED".into(),
                occurred_at: Some(Utc::now().to_rfc3339()),
            },
            json!({"late": true}),
            "vietqr_mock",
            "tr-p4-h-wh",
        )
        .await
        .unwrap();
    assert_eq!(wh_h.status, "late_payment_refund_queued");
    assert_eq!(wh_h.payment_id, Some(p_h_qr.payment.id));
    let zp_still_pending = svc
        .get_payment(Some(p_h_zp.payment.id), None, None)
        .await
        .unwrap();
    assert_eq!(
        zp_still_pending.payment.status, "pending",
        "Item H: VietQR webhook must NOT confirm ZALOPAY payment"
    );

    // Item H2: resolve_reconciliation_item(ASSIGN) rejects VietQR queue item -> ZALOPAY payment
    let recon_item_id: Uuid = sqlx::query_scalar(
        r#"
        INSERT INTO reconciliation_queue (
            provider, provider_tx_id, actual_amount_vnd, reason, source_type
        )
        VALUES ('vietqr_mock', 'QR-P4-H-MISMATCH', 300000, 'unmatched_payment', 'webhook')
        RETURNING id
        "#,
    )
    .fetch_one(&db.pool)
    .await
    .unwrap();
    let assign_err = svc
        .resolve_reconciliation_item(ResolveReconciliationInput {
            item_id: recon_item_id,
            action: ReconciliationActionKind::Assign,
            payment_id: Some(p_h_zp.payment.id),
            note: "wrong method assign".into(),
            idempotency_key: "res-p4-h-mismatch".into(),
            trace_id: "tr-p4-h-res".into(),
        })
        .await
        .unwrap_err();
    assert!(
        matches!(assign_err, PaymentError::Conflict(_)),
        "Item H: ASSIGN must reject provider/method mismatch, got {assign_err:?}"
    );

    // --- Item K: create_payment(supersede_previous=true) guards ---
    // K1: Cannot supersede COD pending payment (especially with held allocation!)
    let p_cod = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-k-cod".into(),
            order_id: "ord-p4-k-cod".into(),
            user_id: "buyer-k".into(),
            amount_vnd: 200_000,
            method: PaymentMethod::Cod,
            shop_allocations: single_alloc("ord-p4-k-cod", "shop-1", 200_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-k-cod".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-k-1".into(),
        })
        .await
        .unwrap();
    let sup_cod_err = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-k-cod".into(),
            order_id: "ord-p4-k-cod".into(),
            user_id: "buyer-k".into(),
            amount_vnd: 200_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p4-k-cod", "shop-1", 200_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-k-cod-sup".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-k-2".into(),
        })
        .await
        .unwrap_err();
    assert!(
        matches!(
            sup_cod_err,
            PaymentError::Conflict(_) | PaymentError::FailedPrecondition(_)
        ),
        "Item K: cannot supersede COD payment, got {sup_cod_err:?}"
    );
    let _ = p_cod;

    // K2: Cannot supersede online with different amount_vnd or extended expires_at
    let sup_amt_err = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-h".into(),
            order_id: "ord-p4-h".into(),
            user_id: "buyer-h".into(),
            amount_vnd: 350_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p4-h", "shop-1", 350_000),
            expires_at: p_h_zp.payment.expires_at,
            idempotency_key: Some("idem-p4-k-amt".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-k-amt".into(),
        })
        .await
        .unwrap_err();
    assert!(
        matches!(
            sup_amt_err,
            PaymentError::Conflict(_) | PaymentError::FailedPrecondition(_)
        ),
        "Item K: cannot supersede with different amount, got {sup_amt_err:?}"
    );

    let extended_exp = p_h_zp.payment.expires_at.unwrap() + ChronoDuration::minutes(5);
    let sup_exp_err = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-h".into(),
            order_id: "ord-p4-h".into(),
            user_id: "buyer-h".into(),
            amount_vnd: 300_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p4-h", "shop-1", 300_000),
            expires_at: Some(extended_exp),
            idempotency_key: Some("idem-p4-k-exp".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-k-exp".into(),
        })
        .await
        .unwrap_err();
    assert!(
        matches!(
            sup_exp_err,
            PaymentError::Conflict(_) | PaymentError::FailedPrecondition(_)
        ),
        "Item K: cannot supersede online payment with extended expires_at, got {sup_exp_err:?}"
    );

    // K3: Valid order switches still pass: ZALOPAY -> VIETQR (same amount, same expiry), then VIETQR -> COD!
    let sw_qr = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-h".into(),
            order_id: "ord-p4-h".into(),
            user_id: "buyer-h".into(),
            amount_vnd: 300_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p4-h", "shop-1", 300_000),
            expires_at: p_h_zp.payment.expires_at,
            idempotency_key: Some("idem-p4-k-ok-qr".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-k-ok-qr".into(),
        })
        .await
        .expect("Item K: valid ZALOPAY -> VIETQR switch must pass");
    assert_eq!(sw_qr.payment.method, "VIETQR");

    let sw_cod = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-h".into(),
            order_id: "ord-p4-h".into(),
            user_id: "buyer-h".into(),
            amount_vnd: 300_000,
            method: PaymentMethod::Cod,
            shop_allocations: single_alloc("ord-p4-h", "shop-1", 300_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-k-ok-cod".into()),
            supersede_previous: true,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-k-ok-cod".into(),
        })
        .await
        .expect("Item K: valid VIETQR -> COD switch must pass");
    assert_eq!(sw_cod.payment.method, "COD");

    db.cleanup().await;
}

/// P4 Item L (real Postgres gRPC): `CancelPayment` denies `cskh`, `accountant`, `ops`, `moderator`
/// on another user's payment, while allowing owner, `admin`, `super_admin`, and `order-service`.
#[tokio::test]
async fn test_pg_p4_item_l_cancel_payment_owner_rbac() {
    let Some(db) = TestDb::setup("test_pg_p4_item_l_cancel_payment_owner_rbac", 6).await else {
        return;
    };
    let (svc, _) = db.service_with_mock();
    let grpc = PaymentGrpcServer::new(svc.clone(), db.auth());

    let p = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-p4-l".into(),
            order_id: "ord-p4-l".into(),
            user_id: "buyer-owner-l".into(),
            amount_vnd: 120_000,
            method: PaymentMethod::VietQr,
            shop_allocations: single_alloc("ord-p4-l", "shop-1", 120_000),
            expires_at: None,
            idempotency_key: Some("idem-p4-l".into()),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "tr-p4-l".into(),
        })
        .await
        .unwrap();

    for role in ["cskh", "accountant", "ops", "moderator"] {
        let err = grpc
            .cancel_payment(grpc_req_with_token(
                pb::CancelPaymentRequest {
                    payment_id: p.payment.id.to_string(),
                    order_group_id: String::new(),
                    reason: "unauthorized cancel".into(),
                },
                &user_jwt(role, &format!("staff-{role}")),
            ))
            .await
            .unwrap_err();
        assert_eq!(
            err.code(),
            tonic::Code::PermissionDenied,
            "Item L: role={role} must be denied CancelPayment on another user's payment"
        );
    }

    // Owner can cancel their own payment
    let ok = grpc
        .cancel_payment(grpc_req_with_token(
            pb::CancelPaymentRequest {
                payment_id: p.payment.id.to_string(),
                order_group_id: String::new(),
                reason: "buyer cancel".into(),
            },
            &user_jwt("buyer", "buyer-owner-l"),
        ))
        .await
        .expect("owner must be allowed to cancel own payment");
    assert_eq!(
        ok.into_inner().payment.unwrap().status,
        pb::PaymentStatus::Cancelled as i32
    );

    db.cleanup().await;
}

/// Captures tracing log levels emitted during an async future (zero external test deps).
#[derive(Clone, Default)]
struct LogLevelCapture {
    entries: Arc<std::sync::Mutex<Vec<(tracing::Level, String)>>>,
}

impl LogLevelCapture {
    fn count_level(&self, level: tracing::Level) -> usize {
        self.entries
            .lock()
            .unwrap()
            .iter()
            .filter(|(lvl, _)| *lvl == level)
            .count()
    }

    fn clear(&self) {
        self.entries.lock().unwrap().clear();
    }
}

struct MessageVisitor(String);

impl tracing::field::Visit for MessageVisitor {
    fn record_debug(&mut self, field: &tracing::field::Field, value: &dyn std::fmt::Debug) {
        if field.name() == "message" {
            self.0 = format!("{value:?}");
        }
    }
}

impl<S: tracing::Subscriber> tracing_subscriber::Layer<S> for LogLevelCapture {
    fn on_event(
        &self,
        event: &tracing::Event<'_>,
        _ctx: tracing_subscriber::layer::Context<'_, S>,
    ) {
        let mut v = MessageVisitor(String::new());
        event.record(&mut v);
        self.entries
            .lock()
            .unwrap()
            .push((*event.metadata().level(), v.0));
    }
}

/// P5 (real Postgres): Post-expiry ZaloPay late sweep window-exhausted distinction:
/// - Default unpaid MockZaloPay returns `return_code=3, is_processing=false`.
/// - Inside the 24h window: `(3, false)` does NOT conclude early; retries on backoff & persists
///   `(provider_last_query_return_code=3, provider_last_query_is_processing=false)` in Postgres.
/// - After process restart / new replica handoff when 24h window expires:
///   * `(3, false)` -> concludes `not_paid`, 0 `reconciliation_queue`, INFO log (0 ERROR logs).
///   * `(3, true)` -> escalates `manual_review`, 1 `reconciliation_queue`, ERROR log.
///   * `NetworkError` (even after an earlier in-window `(3, false)`) -> clears persisted query
///     observation to `(NULL, NULL)`, escalates `manual_review`, 1 `reconciliation_queue`, ERROR.
///   * Unknown `return_code` (e.g. `99`) -> escalates `manual_review`, 1 `reconciliation_queue`, ERROR.
///   * Late payment (`return_code=1`) arriving after an earlier in-window `(3, false)` is still
///     recovered via `late_payment` refund (proves why in-window `(3, false)` must not close early).
#[tokio::test]
async fn test_pg_p5_zalopay_window_exhausted_unpaid_vs_processing_and_restart_persistence() {
    use tracing::instrument::WithSubscriber;
    use tracing_subscriber::layer::SubscriberExt;

    let Some(db) = TestDb::setup(
        "test_pg_p5_zalopay_window_exhausted_unpaid_vs_processing_and_restart_persistence",
        8,
    )
    .await
    else {
        return;
    };
    let (svc_replica_a, mock) = db.service_with_mock();

    let base_expiry = Utc::now() - ChronoDuration::hours(2);
    let make_cancelled_zp = |svc: &PaymentService, tag: &str, expires_at| {
        let svc = svc.clone();
        let tag = tag.to_string();
        async move {
            let p = svc
                .create_payment(CreatePaymentInput {
                    order_group_id: format!("grp-p5-{tag}"),
                    order_id: format!("ord-p5-{tag}"),
                    user_id: format!("buyer-p5-{tag}"),
                    amount_vnd: 180_000,
                    method: PaymentMethod::ZaloPay,
                    shop_allocations: single_alloc(&format!("ord-p5-{tag}"), "shop-1", 180_000),
                    expires_at: Some(expires_at),
                    idempotency_key: Some(format!("idem-p5-{tag}")),
                    supersede_previous: false,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    trace_id: format!("tr-p5-{tag}"),
                })
                .await
                .unwrap();
            svc.process_order_cancelled_event(
                &format!("evt-p5-cancel-{tag}"),
                &format!("ord-p5-{tag}"),
                &format!("tr-p5-cancel-{tag}"),
            )
            .await
            .unwrap();
            p.payment
        }
    };

    // 1. Default unpaid MockZaloPay order (NO override set): inside 24h window must NOT conclude early!
    let p_unpaid = make_cancelled_zp(&svc_replica_a, "unpaid", base_expiry).await;
    let p_net_after_unpaid =
        make_cancelled_zp(&svc_replica_a, "net-after-unpaid", base_expiry).await;
    let p_late_paid = make_cancelled_zp(&svc_replica_a, "late-paid", base_expiry).await;

    let t_inside = base_expiry + ChronoDuration::hours(2);
    let st_inside = svc_replica_a
        .scan_unsettled_zalopay_payments_with_options(t_inside, ChronoDuration::hours(24), 25, 60)
        .await
        .unwrap();
    assert_eq!(st_inside.claimed, 3);
    assert_eq!(
        st_inside.retried, 3,
        "inside 24h window, return_code=3 is_processing=false must retry and NOT conclude early"
    );
    assert_eq!(st_inside.concluded_not_paid, 0);
    assert_eq!(st_inside.escalated_manual_review, 0);

    // Verify Postgres persisted last query state (3, false) and left provider_final_status NULL.
    for pid in [p_unpaid.id, p_net_after_unpaid.id, p_late_paid.id] {
        let (final_st, next_at, last_rc, last_proc): (
            Option<String>,
            Option<chrono::DateTime<Utc>>,
            Option<i32>,
            Option<bool>,
        ) = sqlx::query_as(
            "SELECT provider_final_status, provider_next_check_at, provider_last_query_return_code, provider_last_query_is_processing FROM payments WHERE id = $1",
        )
        .bind(pid)
        .fetch_one(&db.pool)
        .await
        .unwrap();
        assert_eq!(final_st, None, "must not conclude inside 24h window");
        assert!(next_at.is_some(), "must schedule next backoff retry");
        assert_eq!(last_rc, Some(3));
        assert_eq!(
            last_proc,
            Some(false),
            "default unpaid MockZaloPay must persist is_processing=false"
        );
    }

    // 2. Simulate full process restart / replica handoff: drop Replica A, build brand-new Replica B!
    drop(svc_replica_a);
    let svc_replica_b = db.service_with_existing_mock(mock.clone());

    let t_after_window = base_expiry + ChronoDuration::hours(25);

    // 2a. Unpaid order (`return_code=3, is_processing=false`) at window exhaustion:
    //     MUST conclude `not_paid`, ZERO `reconciliation_queue`, INFO log and ZERO ERROR logs!
    // Temporarily move the other two payments out of due window so we inspect log levels cleanly.
    sqlx::query("UPDATE payments SET provider_next_check_at = $1 WHERE id IN ($2, $3)")
        .bind(t_after_window + ChronoDuration::hours(1))
        .bind(p_net_after_unpaid.id)
        .bind(p_late_paid.id)
        .execute(&db.pool)
        .await
        .unwrap();

    let capture = LogLevelCapture::default();
    let subscriber = tracing::Dispatch::new(tracing_subscriber::registry().with(capture.clone()));
    let st_unpaid_end = svc_replica_b
        .scan_unsettled_zalopay_payments_with_options(
            t_after_window,
            ChronoDuration::hours(24),
            25,
            60,
        )
        .with_subscriber(subscriber.clone())
        .await
        .unwrap();
    assert_eq!(st_unpaid_end.claimed, 1);
    assert_eq!(
        st_unpaid_end.concluded_not_paid, 1,
        "window exhausted on (3, false) must conclude not_paid"
    );
    assert_eq!(st_unpaid_end.escalated_manual_review, 0);
    assert_eq!(
        capture.count_level(tracing::Level::ERROR),
        0,
        "window exhausted on unpaid (3, false) must NOT log ERROR"
    );
    assert!(
        capture.count_level(tracing::Level::INFO) >= 1,
        "window exhausted on unpaid (3, false) must log INFO"
    );

    let (unpaid_final, unpaid_next, unpaid_rc, unpaid_proc): (
        Option<String>,
        Option<chrono::DateTime<Utc>>,
        Option<i32>,
        Option<bool>,
    ) = sqlx::query_as(
        "SELECT provider_final_status, provider_next_check_at, provider_last_query_return_code, provider_last_query_is_processing FROM payments WHERE id = $1",
    )
    .bind(p_unpaid.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(unpaid_final.as_deref(), Some("not_paid"));
    assert_eq!(unpaid_next, None);
    assert_eq!(unpaid_rc, Some(3));
    assert_eq!(unpaid_proc, Some(false));

    let unpaid_rq: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM reconciliation_queue WHERE expected_payment_id = $1",
    )
    .bind(p_unpaid.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(
        unpaid_rq, 0,
        "unpaid (3, false) at window end must NOT enqueue reconciliation_queue"
    );

    // 2b. Network error at 24h (even though Replica A earlier persisted `(3, false)`!) ->
    //     MUST clear persisted query state to `(NULL, NULL)`, escalate `manual_review`, log ERROR.
    mock.set_order_outcome(
        &p_net_after_unpaid.provider_order_code,
        MockBehavior::network_error("outage at 24h"),
    );
    sqlx::query("UPDATE payments SET provider_next_check_at = $1 WHERE id = $2")
        .bind(t_after_window - ChronoDuration::seconds(1))
        .bind(p_net_after_unpaid.id)
        .execute(&db.pool)
        .await
        .unwrap();

    capture.clear();
    let st_net = svc_replica_b
        .scan_unsettled_zalopay_payments_with_options(
            t_after_window,
            ChronoDuration::hours(24),
            25,
            60,
        )
        .with_subscriber(subscriber.clone())
        .await
        .unwrap();
    assert_eq!(st_net.claimed, 1);
    assert_eq!(st_net.escalated_manual_review, 1);
    assert_eq!(st_net.concluded_not_paid, 0);
    assert!(
        capture.count_level(tracing::Level::ERROR) >= 1,
        "window exhausted on network error must log ERROR"
    );

    let (net_final, net_rc, net_proc): (Option<String>, Option<i32>, Option<bool>) =
        sqlx::query_as(
            "SELECT provider_final_status, provider_last_query_return_code, provider_last_query_is_processing FROM payments WHERE id = $1",
        )
        .bind(p_net_after_unpaid.id)
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(net_final.as_deref(), Some("manual_review"));
    assert_eq!(net_rc, None);
    assert_eq!(net_proc, None);

    let net_rq: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM reconciliation_queue WHERE expected_payment_id = $1 AND reason = 'zalopay_late_sweep_window_exhausted'",
    )
    .bind(p_net_after_unpaid.id)
    .fetch_one(&db.pool)
    .await
    .unwrap();
    assert_eq!(net_rq, 1);

    // 2c. Late payment (`return_code=1`) arriving after earlier `(3, false)` -> recovered!
    // Allow circuit breaker to cool down after the simulated outage above.
    tokio::time::sleep(Duration::from_millis(30)).await;
    mock.set_order_outcome(
        &p_late_paid.provider_order_code,
        MockBehavior::return_code(1),
    );
    sqlx::query("UPDATE payments SET provider_next_check_at = $1 WHERE id = $2")
        .bind(t_after_window - ChronoDuration::seconds(1))
        .bind(p_late_paid.id)
        .execute(&db.pool)
        .await
        .unwrap();
    let st_paid = svc_replica_b
        .scan_unsettled_zalopay_payments_with_options(
            t_after_window,
            ChronoDuration::hours(24),
            25,
            60,
        )
        .await
        .unwrap();
    assert_eq!(st_paid.claimed, 1);
    assert_eq!(st_paid.late_refunds_queued, 1);

    // 2d. `return_code=3, is_processing=true` AND unknown `return_code=99` at window end ->
    //     BOTH escalate to `reconciliation_queue` + `manual_review` + ERROR log.
    let p_proc = make_cancelled_zp(&svc_replica_b, "still-proc", base_expiry).await;
    let p_unknown = make_cancelled_zp(&svc_replica_b, "unknown-rc", base_expiry).await;
    mock.set_order_outcome(&p_proc.provider_order_code, MockBehavior::return_code(3));
    mock.set_order_outcome(
        &p_unknown.provider_order_code,
        MockBehavior::return_code(99),
    );

    capture.clear();
    let st_esc = svc_replica_b
        .scan_unsettled_zalopay_payments_with_options(
            t_after_window,
            ChronoDuration::hours(24),
            25,
            60,
        )
        .with_subscriber(subscriber)
        .await
        .unwrap();
    assert_eq!(st_esc.claimed, 2);
    assert_eq!(st_esc.escalated_manual_review, 2);
    assert_eq!(st_esc.concluded_not_paid, 0);
    assert!(
        capture.count_level(tracing::Level::ERROR) >= 2,
        "both is_processing=true and unknown return_code at window end must log ERROR"
    );

    let (proc_final, proc_rc, proc_flag): (Option<String>, Option<i32>, Option<bool>) =
        sqlx::query_as(
            "SELECT provider_final_status, provider_last_query_return_code, provider_last_query_is_processing FROM payments WHERE id = $1",
        )
        .bind(p_proc.id)
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(proc_final.as_deref(), Some("manual_review"));
    assert_eq!(proc_rc, Some(3));
    assert_eq!(proc_flag, Some(true));

    let (unk_final, unk_rc, unk_flag): (Option<String>, Option<i32>, Option<bool>) =
        sqlx::query_as(
            "SELECT provider_final_status, provider_last_query_return_code, provider_last_query_is_processing FROM payments WHERE id = $1",
        )
        .bind(p_unknown.id)
        .fetch_one(&db.pool)
        .await
        .unwrap();
    assert_eq!(unk_final.as_deref(), Some("manual_review"));
    assert_eq!(unk_rc, Some(99));
    assert_eq!(unk_flag, Some(false));

    for pid in [p_proc.id, p_unknown.id] {
        let rq_cnt: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM reconciliation_queue WHERE expected_payment_id = $1 AND reason = 'zalopay_late_sweep_window_exhausted'",
        )
        .bind(pid)
        .fetch_one(&db.pool)
        .await
        .unwrap();
        assert_eq!(rq_cnt, 1);
    }

    assert!(
        svc_replica_b
            .get_ledger_summary(None)
            .await
            .unwrap()
            .is_balanced
    );
    db.cleanup().await;
}
