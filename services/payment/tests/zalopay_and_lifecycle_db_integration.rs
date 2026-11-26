//! PostgreSQL integration tests for ZaloPay full payment lifecycle, HMAC + anti-replay IPN,
//! 10x webhook idempotency, double-entry VND ledger invariants, post-settlement refunds,
//! seller balance (pending -> available -> payout), expiry watchdog, order.cancelled auto-refund,
//! daily VietQR reconciliation (with auto-heal + sales_channel filtering), ListReconciliationReports
//! (gRPC & HTTP), and production anti-mock callback guard.
//!
//! Runs against an isolated database created on the server pointed to by `PAYMENT_TEST_DATABASE_URL`
//! or `TEST_PAYMENT_DB_URL` (and skips cleanly when neither is set).

use axum::body::Body;
use axum::http::{Method, Request, StatusCode};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use chrono::{Duration, Utc};
use hmac::{Hmac, Mac};
use http_body_util::BodyExt;
use serde_json::json;
use sha2::Sha256;
use sqlx::postgres::PgPoolOptions;
use sqlx::{Executor, PgPool};
use tonic::metadata::MetadataValue;
use tower::ServiceExt;
use uuid::Uuid;

use nexusmart_payment::auth::GrpcAuth;
use nexusmart_payment::config::{
    AppEnv, Config, VietQrMode, ZaloPayConfig, ZaloPayMode, DEV_ONLY_ZALOPAY_MOCK_KEY2,
};
use nexusmart_payment::domain::{
    vn_today, CreatePaymentInput, CreateRefundInput, PaymentMethod, SalesChannel,
    ShopAllocationInput,
};
use nexusmart_payment::error::PaymentError;
use nexusmart_payment::grpc::pb::payment_service_server::PaymentService as PaymentServiceGrpc;
use nexusmart_payment::grpc::pb::{
    CancelPaymentRequest, CreatePaymentRequest, CreateRefundRequest, GetLedgerSummaryRequest,
    GetPaymentRequest, GetSellerBalanceRequest, ListReconciliationReportsRequest,
    PaymentMethod as ProtoMethod, ReleaseEscrowRequest, RunReconciliationRequest, ShopAllocation,
};
use nexusmart_payment::grpc::PaymentGrpcServer;
use nexusmart_payment::http::build_router;
use nexusmart_payment::ledger;
use nexusmart_payment::metrics::Metrics;
use nexusmart_payment::service::{PaymentService, ZaloPayCallbackOutcome};
use nexusmart_payment::webhook_security::compute_hmac_hex;
use nexusmart_payment::zalopay::{MockBehavior, MockZaloPay, ZaloPayCallbackData};

const TEST_USER_JWT_SECRET: &str = "it-user-jwt-secret-key";
const TEST_SVC_JWT_SECRET: &str = "it-service-jwt-secret-key";

fn pg_test_db_url() -> Option<String> {
    std::env::var("PAYMENT_TEST_DATABASE_URL")
        .or_else(|_| std::env::var("TEST_PAYMENT_DB_URL"))
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
}

fn sign_test_jwt(secret: &str, claims: serde_json::Value) -> String {
    let header = URL_SAFE_NO_PAD.encode(br#"{"alg":"HS256","typ":"JWT"}"#);
    let payload = URL_SAFE_NO_PAD.encode(claims.to_string());
    let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes()).unwrap();
    mac.update(format!("{header}.{payload}").as_bytes());
    let sig = URL_SAFE_NO_PAD.encode(mac.finalize().into_bytes());
    format!("{header}.{payload}.{sig}")
}

fn user_bearer(role: &str, user_id: &str, shop_id: &str) -> String {
    let tok = sign_test_jwt(
        TEST_USER_JWT_SECRET,
        json!({
            "sub": user_id,
            "role": role,
            "shop_id": shop_id,
            "exp": Utc::now().timestamp() + 300,
        }),
    );
    format!("Bearer {tok}")
}

fn grpc_req<T>(payload: T, role: &str, user_id: &str, shop_id: &str) -> tonic::Request<T> {
    let mut req = tonic::Request::new(payload);
    let hdr = user_bearer(role, user_id, shop_id);
    req.metadata_mut()
        .insert("authorization", MetadataValue::try_from(hdr).unwrap());
    req
}

fn build_zp_callback(
    key2: &str,
    app_id: i32,
    app_trans_id: &str,
    zp_trans_id: i64,
    amount: i64,
    server_time_ms: i64,
) -> nexusmart_payment::zalopay::ZaloPayCallbackRequest {
    MockZaloPay::build_signed_callback(
        key2,
        &ZaloPayCallbackData {
            app_id,
            app_trans_id: app_trans_id.to_string(),
            app_time: server_time_ms,
            app_user: "buyer".to_string(),
            amount,
            embed_data: "{}".to_string(),
            item: "[]".to_string(),
            zp_trans_id,
            server_time: server_time_ms,
            channel: 38,
            merchant_user_id: String::new(),
            user_fee_amount: 0,
            discount_amount: 0,
        },
    )
    .expect("build signed ZaloPay callback")
}

async fn setup_isolated_db(base_db_url: &str) -> (PgPool, PgPool, String, String) {
    let admin_pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(base_db_url)
        .await
        .expect("connect to base test Postgres");

    let db_name = format!("payment_it_{}", &Uuid::new_v4().simple().to_string()[..12]);
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

    sqlx::migrate!("./migrations")
        .run(&pool)
        .await
        .expect("apply embedded sqlx migrations (0001..0018)");

    (admin_pool, pool, db_name, isolated_url)
}

async fn teardown_isolated_db(admin_pool: PgPool, pool: PgPool, db_name: &str) {
    pool.close().await;
    let _ = admin_pool
        .execute(format!("DROP DATABASE IF EXISTS {db_name} WITH (FORCE)").as_str())
        .await;
    admin_pool.close().await;
}

#[tokio::test]
async fn test_pg_zalopay_full_lifecycle_refunds_seller_balance_and_reconciliation_reports() {
    let Some(base_db_url) = pg_test_db_url() else {
        eprintln!(
            "skipping test_pg_zalopay_full_lifecycle_refunds_seller_balance_and_reconciliation_reports: neither PAYMENT_TEST_DATABASE_URL nor TEST_PAYMENT_DB_URL set"
        );
        return;
    };

    let (admin_pool, pool, db_name, isolated_url) = setup_isolated_db(&base_db_url).await;

    let mut cfg = Config::from_env();
    cfg.database_url = isolated_url;
    cfg.webhook_secret = "it-vietqr-webhook-secret".to_string();
    cfg.jwt_secret = TEST_USER_JWT_SECRET.to_string();
    cfg.service_jwt_secret = TEST_SVC_JWT_SECRET.to_string();
    cfg.zalopay = ZaloPayConfig::mock_default();
    cfg.zalopay.mode = ZaloPayMode::Mock;
    cfg.zalopay.app_id = Some(2554);
    cfg.zalopay.key1 = "it-zalopay-key1".to_string();
    cfg.zalopay.key2 = "it-zalopay-key2".to_string();

    let svc = PaymentService::new(pool.clone(), cfg.clone(), Metrics::new());
    let auth = GrpcAuth::new(TEST_USER_JWT_SECRET, TEST_SVC_JWT_SECRET).unwrap();
    let app = build_router(svc.clone(), auth.clone());
    let grpc = PaymentGrpcServer::new(svc.clone(), auth);

    // -------------------------------------------------------------------------
    // 1. Create ZaloPay payment via gRPC (PAYMENT_METHOD_ZALOPAY) + idempotency
    // -------------------------------------------------------------------------
    let create_req = CreatePaymentRequest {
        order_group_id: "grp-zp-1".into(),
        order_id: "ord-zp-1".into(),
        user_id: "buyer-zp-1".into(),
        amount_vnd: 330_000,
        method: ProtoMethod::Zalopay as i32,
        shop_allocations: vec![ShopAllocation {
            order_id: "ord-zp-1".into(),
            shop_id: "shop-zp-1".into(),
            subtotal_vnd: 300_000,
            commission_vnd: 15_000,
            shipping_fee_vnd: 30_000,
            platform_discount_vnd: 0,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
        }],
        expires_at: None,
        idempotency_key: "idem-zp-1".into(),
        supersede_previous: false,
        sales_channel: "MARKETPLACE".into(),
        storefront_shop_id: String::new(),
    };
    let created_pb = grpc
        .create_payment(grpc_req(create_req.clone(), "buyer", "buyer-zp-1", ""))
        .await
        .expect("gRPC CreatePayment ZALOPAY")
        .into_inner()
        .payment
        .expect("payment present");
    assert_eq!(created_pb.method, ProtoMethod::Zalopay as i32);
    assert_eq!(created_pb.provider, "zalopay_mock");
    assert!(!created_pb.provider_ref.is_empty());
    assert!(!created_pb.pay_url.is_empty());
    assert!(created_pb.expires_at.is_some());

    // Duplicate idempotency_key returns exact same payment ID
    let created_pb_dup = grpc
        .create_payment(grpc_req(create_req, "buyer", "buyer-zp-1", ""))
        .await
        .expect("idempotent CreatePayment")
        .into_inner()
        .payment
        .unwrap();
    assert_eq!(created_pb_dup.payment_id, created_pb.payment_id);

    let zp1_id = Uuid::parse_str(&created_pb.payment_id).unwrap();
    let zp1_app_trans_id = created_pb.provider_ref.clone();

    // -------------------------------------------------------------------------
    // 2. HTTP /webhooks/zalopay: invalid MAC (401), then 10x duplicate valid
    //    callback -> ledger posted ONCE!
    // -------------------------------------------------------------------------
    let invalid_cb = build_zp_callback(
        "wrong-key2",
        2554,
        &zp1_app_trans_id,
        26112100001001,
        330_000,
        Utc::now().timestamp_millis(),
    );
    let bad_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/zalopay")
                .header("content-type", "application/json")
                .body(Body::from(serde_json::to_vec(&invalid_cb).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(bad_resp.status(), StatusCode::UNAUTHORIZED);

    let valid_cb = build_zp_callback(
        "it-zalopay-key2",
        2554,
        &zp1_app_trans_id,
        26112100001001,
        330_000,
        Utc::now().timestamp_millis(),
    );
    let valid_cb_body = serde_json::to_vec(&valid_cb).unwrap();

    for _ in 0..10 {
        let resp = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/webhooks/zalopay")
                    .header("content-type", "application/json")
                    .body(Body::from(valid_cb_body.clone()))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        let bytes = resp.into_body().collect().await.unwrap().to_bytes();
        let json_body: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(json_body["return_code"], 1);
        assert_eq!(json_body["return_message"], "success");
    }

    // Exactly ONE double-entry ledger transaction posted for payment_zalopay
    let zp_ledger_tx_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM ledger_transactions WHERE reference_type = 'payment_zalopay' AND reference_id = $1",
    )
    .bind(zp1_id.to_string())
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(zp_ledger_tx_count, 1);

    // Outbox has payment.succeeded with method = ZALOPAY and sales_channel = MARKETPLACE
    let outbox_meta: (String, String) = sqlx::query_as(
        "SELECT payload->>'method', payload->>'sales_channel' FROM outbox_events WHERE event_type = 'payment.succeeded' AND aggregate_id = 'grp-zp-1' LIMIT 1",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(outbox_meta.0, "ZALOPAY");
    assert_eq!(outbox_meta.1, "MARKETPLACE");

    // GetPayment via gRPC (owner allowed, other buyer forbidden)
    let got = grpc
        .get_payment(grpc_req(
            GetPaymentRequest {
                payment_id: zp1_id.to_string(),
                order_group_id: String::new(),
                order_id: String::new(),
            },
            "buyer",
            "buyer-zp-1",
            "",
        ))
        .await
        .expect("owner GetPayment")
        .into_inner()
        .payment
        .unwrap();
    assert_eq!(got.provider_ref, "26112100001001");

    let denied = grpc
        .get_payment(grpc_req(
            GetPaymentRequest {
                payment_id: zp1_id.to_string(),
                order_group_id: String::new(),
                order_id: String::new(),
            },
            "buyer",
            "other-buyer",
            "",
        ))
        .await
        .unwrap_err();
    assert_eq!(denied.code(), tonic::Code::PermissionDenied);

    // -------------------------------------------------------------------------
    // 3. Order completed -> seller_pending -> release_escrow -> seller_available -> payout
    // -------------------------------------------------------------------------
    assert!(svc
        .process_order_completed_event("evt-complete-zp-1", "ord-zp-1", "trace-comp-zp-1")
        .await
        .unwrap());
    // Duplicate NATS event ignored idempotently
    assert!(!svc
        .process_order_completed_event("evt-complete-zp-1", "ord-zp-1", "trace-comp-zp-1")
        .await
        .unwrap());

    let bal_pending = grpc
        .get_seller_balance(grpc_req(
            GetSellerBalanceRequest {
                shop_id: "shop-zp-1".into(),
            },
            "seller",
            "seller-zp-1",
            "shop-zp-1",
        ))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(bal_pending.pending_vnd, 285_000);
    assert_eq!(bal_pending.available_vnd, 0);
    assert_eq!(bal_pending.total_earned_vnd, 285_000);

    // Release escrow via gRPC
    let rel1 = grpc
        .release_escrow(grpc_req(
            ReleaseEscrowRequest {
                order_id: "ord-zp-1".into(),
                shop_id: "shop-zp-1".into(),
            },
            "admin",
            "admin-1",
            "",
        ))
        .await
        .unwrap()
        .into_inner();
    assert!(rel1.released);
    assert_eq!(rel1.released_amount_vnd, 285_000);
    assert!(!rel1.already_released);

    // Idempotent second release
    let rel2 = svc
        .release_escrow("ord-zp-1", Some("shop-zp-1"))
        .await
        .unwrap();
    assert!(rel2.already_released);
    assert_eq!(rel2.released_amount_vnd, 0);

    let bal_avail = svc.get_seller_balance("shop-zp-1").await.unwrap();
    assert_eq!(bal_avail.pending_vnd, 0);
    assert_eq!(bal_avail.available_vnd, 285_000);

    // Seller payout: over-withdrawal rejected, valid partial payout succeeds & idempotent
    let over_err = svc
        .request_seller_payout("shop-zp-1", 285_001, "19034567890123", "payout-over")
        .await
        .unwrap_err();
    assert!(matches!(over_err, PaymentError::InsufficientBalance { .. }));

    let payout1 = svc
        .request_seller_payout("shop-zp-1", 100_000, "19034567890123", "payout-zp-1")
        .await
        .unwrap();
    assert_eq!(payout1.bank_account_masked, "****0123");
    let payout1_dup = svc
        .request_seller_payout("shop-zp-1", 100_000, "19034567890123", "payout-zp-1")
        .await
        .unwrap();
    assert_eq!(payout1_dup.id, payout1.id);

    let bal_after_payout = svc.get_seller_balance("shop-zp-1").await.unwrap();
    assert_eq!(bal_after_payout.available_vnd, 185_000);

    // -------------------------------------------------------------------------
    // 4. Post-settlement Return Refund on ZaloPay (main clawback model:
    //    claws back from seller_pending then seller_available)
    // -------------------------------------------------------------------------
    // 4a. Partial return refund (165,000 VND) on RELEASED allocation ord-zp-1
    let ref_released_resp = grpc
        .create_refund(grpc_req(
            CreateRefundRequest {
                payment_id: zp1_id.to_string(),
                order_id: "ord-zp-1".into(),
                amount_vnd: 165_000,
                reason: "partial post-release return".into(),
                idempotency_key: "refund-zp-released-1".into(),
                require_approval_if_large: false,
                requested_by: String::new(),
            },
            "admin",
            "admin-1",
            "",
        ))
        .await
        .expect("CreateRefund on released ZaloPay allocation")
        .into_inner();
    assert!(!ref_released_resp.already_existed);
    let ref_released = ref_released_resp.refund.unwrap();
    assert_eq!(ref_released.amount_vnd, 165_000);

    // Verify provider_refund_id saved in DB after ZaloPay refund dispatch
    let provider_ref_id: Option<String> =
        sqlx::query_scalar("SELECT provider_refund_id FROM refunds WHERE id = $1")
            .bind(Uuid::parse_str(&ref_released.refund_id).unwrap())
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(
        provider_ref_id.as_deref().is_some_and(|s| !s.is_empty()),
        "expected ZaloPay mock refund provider_refund_id, got {provider_ref_id:?}"
    );

    // Seller available balance decreases by clawed-back 165,000 VND (185,000 -> 20,000)
    let bal_after_ret = svc.get_seller_balance("shop-zp-1").await.unwrap();
    assert_eq!(bal_after_ret.available_vnd, 20_000);

    // 4b. Second ZaloPay payment (STOREFRONT) settled to `pending_release`, then clawed back!
    let zp2 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-zp-2".into(),
            order_id: "ord-zp-2".into(),
            user_id: "buyer-zp-2".into(),
            amount_vnd: 230_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some("ord-zp-2".into()),
                shop_id: "shop-zp-2".into(),
                subtotal_vnd: 200_000,
                commission_vnd: 10_000,
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
                sales_channel: "STOREFRONT".into(),
                storefront_shop_id: "shop-zp-2".into(),
            }],
            expires_at: None,
            idempotency_key: Some("idem-zp-2".into()),
            supersede_previous: false,
            sales_channel: "STOREFRONT".into(),
            storefront_shop_id: "shop-zp-2".into(),
            trace_id: "trace-zp-2".into(),
        })
        .await
        .unwrap();
    let cb2 = build_zp_callback(
        "it-zalopay-key2",
        2554,
        &zp2.payment.provider_order_code,
        26112100001002,
        230_000,
        Utc::now().timestamp_millis(),
    );
    let cb2_out = svc.process_zalopay_callback(cb2, "t2").await.unwrap();
    assert!(matches!(cb2_out, ZaloPayCallbackOutcome::Confirmed { .. }));
    assert_eq!(svc.settle_order_to_pending("ord-zp-2").await.unwrap(), 1);
    assert_eq!(
        svc.get_seller_balance("shop-zp-2")
            .await
            .unwrap()
            .pending_vnd,
        190_000
    );

    let ret_pending = svc
        .create_refund(CreateRefundInput {
            payment_id: Some(zp2.payment.id),
            order_id: "ord-zp-2".into(),
            amount_vnd: 190_000,
            reason: "return while pending_release".into(),
            idempotency_key: "refund-zp-pending-2".into(),
            trace_id: "trace-ref-2".into(),
            require_approval_if_large: false,
            requested_by: None,
            created_by_service: false,
        })
        .await
        .unwrap();
    assert!(ret_pending.refund.provider_refund_id.is_some());
    assert_eq!(
        svc.get_seller_balance("shop-zp-2")
            .await
            .unwrap()
            .pending_vnd,
        0
    );

    // -------------------------------------------------------------------------
    // 5. ZaloPay expiry watchdog + late webhook auto-refund + order.cancelled
    // -------------------------------------------------------------------------
    let zp_exp_unpaid = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-zp-exp-1".into(),
            order_id: "ord-zp-exp-1".into(),
            user_id: "buyer-exp".into(),
            amount_vnd: 120_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: vec![],
            expires_at: Some(Utc::now() - Duration::minutes(1)),
            idempotency_key: Some("idem-zp-exp-1".into()),
            supersede_previous: false,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            trace_id: "trace-exp-1".into(),
        })
        .await
        .unwrap();

    let zp_exp_paid = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-zp-exp-2".into(),
            order_id: "ord-zp-exp-2".into(),
            user_id: "buyer-exp".into(),
            amount_vnd: 140_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: vec![],
            expires_at: Some(Utc::now() - Duration::minutes(1)),
            idempotency_key: Some("idem-zp-exp-2".into()),
            supersede_previous: false,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            trace_id: "trace-exp-2".into(),
        })
        .await
        .unwrap();

    // Configure mock /v2/query: zp_exp_unpaid -> unpaid (2), zp_exp_paid -> paid (1)
    let mock_zp = svc.zalopay().mock().expect("mock zalopay");
    mock_zp.set_order_outcome(
        &zp_exp_unpaid.payment.provider_order_code,
        MockBehavior::unpaid(),
    );
    mock_zp.set_order_outcome(
        &zp_exp_paid.payment.provider_order_code,
        MockBehavior::return_code(1),
    );

    let expired_count = svc.scan_expired_payments().await.unwrap();
    assert_eq!(expired_count, 1);
    assert_eq!(
        svc.get_payment(Some(zp_exp_unpaid.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "expired"
    );
    assert_eq!(
        svc.get_payment(Some(zp_exp_paid.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "succeeded"
    );

    // Late ZaloPay callback on expired payment -> LateRefundQueued!
    let late_cb = build_zp_callback(
        "it-zalopay-key2",
        2554,
        &zp_exp_unpaid.payment.provider_order_code,
        26112100001088,
        120_000,
        Utc::now().timestamp_millis(),
    );
    let late_out = svc
        .process_zalopay_callback(late_cb, "t-late")
        .await
        .unwrap();
    assert!(
        matches!(late_out, ZaloPayCallbackOutcome::LateRefundQueued { .. }),
        "expected LateRefundQueued, got {late_out:?}"
    );

    // order.cancelled on succeeded ZaloPay payment -> auto-refund!
    assert!(svc
        .process_order_cancelled_event("evt-cancel-zp-exp-paid", "ord-zp-exp-2", "t-cancel")
        .await
        .unwrap());
    assert!(!svc
        .process_order_cancelled_event("evt-cancel-zp-exp-paid", "ord-zp-exp-2", "t-cancel")
        .await
        .unwrap());
    assert_eq!(
        svc.get_payment(Some(zp_exp_paid.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "refunded"
    );

    // Cancel pending ZaloPay payment via gRPC (mock /v2/query reports unpaid -> cancel proceeds)
    let zp_cancel = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-zp-cancel".into(),
            order_id: "ord-zp-cancel".into(),
            user_id: "buyer-cancel".into(),
            amount_vnd: 90_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: vec![],
            expires_at: None,
            idempotency_key: Some("idem-zp-cancel".into()),
            supersede_previous: false,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            trace_id: "trace-cancel".into(),
        })
        .await
        .unwrap();
    mock_zp.set_order_outcome(
        &zp_cancel.payment.provider_order_code,
        MockBehavior::unpaid(),
    );
    let cancelled_pb = grpc
        .cancel_payment(grpc_req(
            CancelPaymentRequest {
                payment_id: zp_cancel.payment.id.to_string(),
                order_group_id: String::new(),
                reason: "buyer changed mind".into(),
            },
            "buyer",
            "buyer-cancel",
            "",
        ))
        .await
        .unwrap()
        .into_inner()
        .payment
        .unwrap();
    assert_eq!(cancelled_pb.payment_id, zp_cancel.payment.id.to_string());

    // -------------------------------------------------------------------------
    // 6. Daily Reconciliation (VietQR-only + Auto-Heal + sales_channel filter)
    //    + ListReconciliationReports (gRPC & HTTP)
    // -------------------------------------------------------------------------
    let vq_mp = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-vq-recon-mp".into(),
            order_id: "ord-vq-recon-mp".into(),
            user_id: "buyer-vq".into(),
            amount_vnd: 200_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![],
            expires_at: None,
            idempotency_key: Some("idem-vq-recon-mp".into()),
            supersede_previous: false,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            trace_id: "trace-vq-mp".into(),
        })
        .await
        .unwrap();
    let vq_sf = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-vq-recon-sf".into(),
            order_id: "ord-vq-recon-sf".into(),
            user_id: "buyer-vq".into(),
            amount_vnd: 250_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some("ord-vq-recon-sf".into()),
                shop_id: "shop-sf-recon".into(),
                subtotal_vnd: 220_000,
                commission_vnd: 11_000,
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
                sales_channel: "STOREFRONT".into(),
                storefront_shop_id: "shop-sf-recon".into(),
            }],
            expires_at: None,
            idempotency_key: Some("idem-vq-recon-sf".into()),
            supersede_previous: false,
            sales_channel: "STOREFRONT".into(),
            storefront_shop_id: "shop-sf-recon".into(),
            trace_id: "trace-vq-sf".into(),
        })
        .await
        .unwrap();

    for (pwa, tx_id) in [(&vq_mp, "VQ-TX-RECON-MP-1"), (&vq_sf, "VQ-TX-RECON-SF-1")] {
        let (vq_body, vq_sig) = svc.vietqr().build_mock_webhook_signed(
            Some(pwa.payment.id),
            Some(pwa.payment.order_id.clone()),
            Some(pwa.payment.provider_order_code.clone()),
            tx_id.into(),
            pwa.payment.amount_vnd,
        );
        let vq_resp = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/webhooks/vietqr")
                    .header("content-type", "application/json")
                    .header("x-signature", vq_sig)
                    .header("x-timestamp", Utc::now().timestamp().to_string())
                    .body(Body::from(vq_body))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(vq_resp.status(), StatusCode::OK);
    }

    // Pending VietQR payment whose webhook callback was missed in local state but IS present in
    // durable mock provider feed (`webhook_events`) -> auto-healed!
    let vq_heal = svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-vq-heal".into(),
            order_id: "ord-vq-heal".into(),
            user_id: "buyer-heal".into(),
            amount_vnd: 175_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![],
            expires_at: None,
            idempotency_key: Some("idem-vq-heal".into()),
            supersede_previous: false,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            trace_id: "trace-heal".into(),
        })
        .await
        .unwrap();
    sqlx::query(
        r#"
        INSERT INTO webhook_events (
            provider, provider_tx_id, payment_id, order_ref, amount_vnd,
            raw_payload, signature_verified, status
        )
        VALUES ('vietqr_mock', 'VQ-TX-HEAL-9900', $1, $2, 175000, $3, TRUE, 'ignored')
        "#,
    )
    .bind(vq_heal.payment.id)
    .bind(&vq_heal.payment.provider_order_code)
    .bind(json!({
        "provider_tx_id": "VQ-TX-HEAL-9900",
        "provider_order_code": vq_heal.payment.provider_order_code,
        "amount_vnd": 175000,
        "status": "SUCCEEDED",
        "occurred_at": Utc::now().to_rfc3339(),
    }))
    .execute(&pool)
    .await
    .expect("seed missed VietQR provider tx for auto-heal");

    // Run reconciliation via gRPC -> verifies auto_healed_count == 1 & VietQR matched!
    let today_str = vn_today(Utc::now()).format("%Y-%m-%d").to_string();
    let recon_pb = grpc
        .run_reconciliation(grpc_req(
            RunReconciliationRequest {
                date: today_str.clone(),
                sales_channel: String::new(),
            },
            "admin",
            "admin-1",
            "",
        ))
        .await
        .expect("gRPC RunReconciliation")
        .into_inner();
    assert_eq!(recon_pb.auto_healed_count, 1);
    assert_eq!(recon_pb.matched_count, 3);

    // Also run a STOREFRONT-scoped reconciliation via HTTP /internal/reconciliation/run
    let admin_hdr = user_bearer("admin", "admin-1", "");
    let http_run_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri(format!(
                    "/internal/reconciliation/run?date={today_str}&sales_channel=STOREFRONT"
                ))
                .header("authorization", &admin_hdr)
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(http_run_resp.status(), StatusCode::OK);

    // ListReconciliationReports via gRPC (unfiltered -> 2 runs, STOREFRONT -> 1 run)
    let all_reports_pb = grpc
        .list_reconciliation_reports(grpc_req(
            ListReconciliationReportsRequest {
                limit: 10,
                sales_channel: String::new(),
            },
            "admin",
            "admin-1",
            "",
        ))
        .await
        .expect("gRPC ListReconciliationReports")
        .into_inner();
    assert_eq!(all_reports_pb.reports.len(), 2);
    assert_eq!(all_reports_pb.reports[0].provider, "vietqr_mock");
    assert_eq!(all_reports_pb.reports[0].status, "completed");

    let sf_reports_pb = grpc
        .list_reconciliation_reports(grpc_req(
            ListReconciliationReportsRequest {
                limit: 10,
                sales_channel: "STOREFRONT".into(),
            },
            "admin",
            "admin-1",
            "",
        ))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(sf_reports_pb.reports.len(), 1);
    assert_eq!(sf_reports_pb.reports[0].sales_channel, "STOREFRONT");

    // GET /internal/reconciliation/reports via HTTP
    let http_reports_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::GET)
                .uri("/internal/reconciliation/reports?limit=5")
                .header("authorization", &admin_hdr)
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(http_reports_resp.status(), StatusCode::OK);
    let reports_bytes = http_reports_resp
        .into_body()
        .collect()
        .await
        .unwrap()
        .to_bytes();
    let reports_json: serde_json::Value = serde_json::from_slice(&reports_bytes).unwrap();
    assert_eq!(reports_json["reports"].as_array().unwrap().len(), 2);

    // Check /readyz, /internal/ledger/summary, /internal/reconciliation/queue, and SSE /payments/{id}/events
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

    let ledger_http_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/internal/ledger/summary")
                .header("authorization", &admin_hdr)
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(ledger_http_resp.status(), StatusCode::OK);

    let queue_http_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/internal/reconciliation/queue")
                .header("authorization", &admin_hdr)
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(queue_http_resp.status(), StatusCode::OK);

    let sse_owner_resp = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/payments/{zp1_id}/events"))
                .header("authorization", user_bearer("buyer", "buyer-zp-1", ""))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(sse_owner_resp.status(), StatusCode::OK);

    let sse_other_resp = app
        .oneshot(
            Request::builder()
                .uri(format!("/payments/{zp1_id}/events"))
                .header("authorization", user_bearer("buyer", "intruder", ""))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(sse_other_resp.status(), StatusCode::FORBIDDEN);

    // -------------------------------------------------------------------------
    // 7. Global & Channel-Filtered Double-Entry Ledger Integrity Gate (100% balanced)
    // -------------------------------------------------------------------------
    let grpc_ledger = grpc
        .get_ledger_summary(grpc_req(
            GetLedgerSummaryRequest {
                sales_channel: String::new(),
            },
            "admin",
            "admin-1",
            "",
        ))
        .await
        .unwrap()
        .into_inner();
    assert!(grpc_ledger.is_balanced);
    assert_eq!(grpc_ledger.total_debits_vnd, grpc_ledger.total_credits_vnd);
    assert!(grpc_ledger.total_debits_vnd > 0);

    let mp_summary = ledger::get_ledger_summary(&pool, Some(SalesChannel::Marketplace))
        .await
        .unwrap();
    let sf_summary = ledger::get_ledger_summary(&pool, Some(SalesChannel::Storefront))
        .await
        .unwrap();
    assert!(mp_summary.is_balanced);
    assert!(sf_summary.is_balanced);
    assert_eq!(mp_summary.unbalanced_transactions, 0);
    assert_eq!(sf_summary.unbalanced_transactions, 0);
    assert_eq!(
        mp_summary.total_debits_vnd + sf_summary.total_debits_vnd,
        grpc_ledger.total_debits_vnd
    );

    teardown_isolated_db(admin_pool, pool, &db_name).await;
}

#[tokio::test]
async fn test_pg_production_rejects_mock_key_callback_while_dev_confirms() {
    let Some(base_db_url) = pg_test_db_url() else {
        eprintln!(
            "skipping test_pg_production_rejects_mock_key_callback_while_dev_confirms: neither PAYMENT_TEST_DATABASE_URL nor TEST_PAYMENT_DB_URL set"
        );
        return;
    };

    let (admin_pool, pool, db_name, isolated_url) = setup_isolated_db(&base_db_url).await;

    // 1. Dev config with default mock ZaloPay key2 (`DEV_ONLY_ZALOPAY_MOCK_KEY2`)
    let mut dev_cfg = Config::from_env();
    dev_cfg.app_env = AppEnv::Development;
    dev_cfg.database_url = isolated_url.clone();
    dev_cfg.webhook_secret = "dev-only-payment-webhook-hmac-secret".to_string();
    dev_cfg.jwt_secret = TEST_USER_JWT_SECRET.to_string();
    dev_cfg.service_jwt_secret = TEST_SVC_JWT_SECRET.to_string();
    dev_cfg.vietqr_mode = VietQrMode::Mock;
    dev_cfg.zalopay = ZaloPayConfig::mock_default();

    let auth = GrpcAuth::new(TEST_USER_JWT_SECRET, TEST_SVC_JWT_SECRET).unwrap();
    let dev_svc = PaymentService::new(pool.clone(), dev_cfg.clone(), Metrics::new());
    let dev_app = build_router(dev_svc.clone(), auth.clone());

    // Create pending ZaloPay payment & pending VietQR payment
    let zp_pay = dev_svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-sec-zp".into(),
            order_id: "ord-sec-zp".into(),
            user_id: "buyer-sec".into(),
            amount_vnd: 450_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: vec![],
            expires_at: None,
            idempotency_key: Some("idem-sec-zp".into()),
            supersede_previous: false,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            trace_id: "trace-sec-zp".into(),
        })
        .await
        .unwrap();

    let vq_pay = dev_svc
        .create_payment(CreatePaymentInput {
            order_group_id: "grp-sec-vq".into(),
            order_id: "ord-sec-vq".into(),
            user_id: "buyer-sec".into(),
            amount_vnd: 220_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![],
            expires_at: None,
            idempotency_key: Some("idem-sec-vq".into()),
            supersede_previous: false,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            trace_id: "trace-sec-vq".into(),
        })
        .await
        .unwrap();

    // Attacker builds callback signed with public repo dev mock key2
    let dev_app_id = dev_svc.zalopay().effective_app_id().unwrap();
    let forged_zp_cb = serde_json::to_vec(&build_zp_callback(
        DEV_ONLY_ZALOPAY_MOCK_KEY2,
        dev_app_id,
        &zp_pay.payment.provider_order_code,
        900100001,
        450_000,
        Utc::now().timestamp_millis(),
    ))
    .unwrap();

    // 2. Production config (valid sandbox keys) MUST reject forged callback (401) and keep payment pending
    let mut prod_cfg = dev_cfg.clone();
    prod_cfg.app_env = AppEnv::Production;
    prod_cfg.zalopay.mode = ZaloPayMode::Sandbox;
    prod_cfg.zalopay.app_id = Some(dev_app_id);
    prod_cfg.zalopay.key1 = "prod-real-key1-987654321".to_string();
    prod_cfg.zalopay.key2 = "prod-real-key2-abcdef123".to_string();
    prod_cfg.vietqr_mode = VietQrMode::PayOs;
    prod_cfg.webhook_secret = "prod-real-webhook-secret-998877".to_string();
    prod_cfg.payos_client_id = "prod-payos-cid".to_string();
    prod_cfg.payos_api_key = "prod-payos-api-key".to_string();
    prod_cfg.payos_checksum_key = "prod-payos-checksum-key".to_string();
    assert!(prod_cfg.validate().is_ok());

    let prod_svc = PaymentService::new(pool.clone(), prod_cfg, Metrics::new());
    let prod_app = build_router(prod_svc, auth.clone());

    let prod_zp_resp = prod_app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/zalopay")
                .header("content-type", "application/json")
                .body(Body::from(forged_zp_cb.clone()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(prod_zp_resp.status(), StatusCode::UNAUTHORIZED);

    // Also verify forged VietQR webhook signed with `.env.example` secret is rejected (401)
    let forged_vq_body = serde_json::to_vec(&json!({
        "provider_tx_id": "VQ-FORGED-9001",
        "payment_id": vq_pay.payment.id.to_string(),
        "order_id": "ord-sec-vq",
        "provider_order_code": vq_pay.payment.provider_order_code,
        "amount_vnd": 220_000,
        "status": "SUCCEEDED",
    }))
    .unwrap();
    let forged_vq_sig = compute_hmac_hex("dev-only-payment-webhook-hmac-secret", &forged_vq_body);
    let ts_now = Utc::now().timestamp().to_string();
    let prod_vq_resp = prod_app
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/vietqr")
                .header("content-type", "application/json")
                .header("x-signature", &forged_vq_sig)
                .header("x-timestamp", &ts_now)
                .body(Body::from(forged_vq_body.clone()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(prod_vq_resp.status(), StatusCode::UNAUTHORIZED);

    // 3. Misconfigured production router (mock / repo key) fails validate() AND rejects with 503
    let mut mis_prod_cfg = dev_cfg.clone();
    mis_prod_cfg.app_env = AppEnv::Production;
    mis_prod_cfg.zalopay.key2 = DEV_ONLY_ZALOPAY_MOCK_KEY2.to_string();
    assert!(mis_prod_cfg.validate().is_err());

    let mis_prod_app = build_router(
        PaymentService::new(pool.clone(), mis_prod_cfg, Metrics::new()),
        auth,
    );
    let mis_zp_resp = mis_prod_app
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/zalopay")
                .header("content-type", "application/json")
                .body(Body::from(forged_zp_cb.clone()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(mis_zp_resp.status(), StatusCode::SERVICE_UNAVAILABLE);

    // Both payments MUST remain pending after all production rejection attempts
    assert_eq!(
        dev_svc
            .get_payment(Some(zp_pay.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "pending"
    );
    assert_eq!(
        dev_svc
            .get_payment(Some(vq_pay.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "pending"
    );

    // 4. Dev router STILL accepts dev mock key2 and dev VietQR webhook -> 200 OK & succeeded
    let dev_zp_resp = dev_app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/zalopay")
                .header("content-type", "application/json")
                .body(Body::from(forged_zp_cb))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(dev_zp_resp.status(), StatusCode::OK);
    assert_eq!(
        dev_svc
            .get_payment(Some(zp_pay.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "succeeded"
    );

    let dev_vq_resp = dev_app
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/webhooks/vietqr")
                .header("content-type", "application/json")
                .header("x-signature", &forged_vq_sig)
                .header("x-timestamp", &ts_now)
                .body(Body::from(forged_vq_body))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(dev_vq_resp.status(), StatusCode::OK);
    assert_eq!(
        dev_svc
            .get_payment(Some(vq_pay.payment.id), None, None)
            .await
            .unwrap()
            .payment
            .status,
        "succeeded"
    );

    teardown_isolated_db(admin_pool, pool, &db_name).await;
}
