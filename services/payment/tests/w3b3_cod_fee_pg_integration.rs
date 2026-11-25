use std::sync::Arc;
use std::time::Duration;

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use chrono::Utc;
use hmac::{Hmac, Mac};
use serde_json::{json, Value};
use sha2::Sha256;
use sqlx::postgres::PgPoolOptions;
use sqlx::{Executor, PgPool};
use tokio::sync::{Barrier, OnceCell, Semaphore};
use tonic::metadata::MetadataValue;
use uuid::Uuid;

use nexusmart_payment::auth::GrpcAuth;
use nexusmart_payment::config::{Config, VietQrMode};
use nexusmart_payment::domain::{
    CreatePaymentInput, PaymentMethod, ShopAllocationInput, ERR_COD_ALREADY_COLLECTED,
};
use nexusmart_payment::grpc::pb::payment_service_server::PaymentService as PaymentServiceGrpcTrait;
use nexusmart_payment::grpc::{pb, PaymentGrpcServer};
use nexusmart_payment::metrics::Metrics;
use nexusmart_payment::service::PaymentService;
use nexusmart_payment::webhook_security::IncomingWebhookPayload;
use nexusmart_payment::zalopay::{MockZaloPay, ZaloPayAdapter};

const TEST_USER_SECRET: &str = "w3b3-pg-user-jwt-secret";
const TEST_SERVICE_SECRET: &str = "w3b3-pg-service-jwt-secret";
const TEST_WEBHOOK_SECRET: &str = "w3b3-pg-webhook-secret";

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
                sqlx::query("SELECT pg_advisory_lock(88443311)")
                    .execute(&mut *conn)
                    .await
                    .expect("advisory lock");
                sqlx::query("CREATE EXTENSION IF NOT EXISTS pgcrypto")
                    .execute(&mut *conn)
                    .await
                    .expect("create pgcrypto");
                sqlx::query("SELECT pg_advisory_unlock(88443311)")
                    .execute(&mut *conn)
                    .await
                    .expect("advisory unlock");
                drop(conn);
                base.close().await;
            })
            .await;

        let schema = format!("payw3b3_{}", Uuid::new_v4().simple());
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
        cfg.seller_hold_hours = 0;
        let zp = ZaloPayAdapter::new_mock(MockZaloPay::new());
        PaymentService::new(self.pool.clone(), cfg, Metrics::new()).with_zalopay(zp)
    }

    fn grpc_server(&self) -> (PaymentService, PaymentGrpcServer) {
        let svc = self.service();
        let auth = GrpcAuth::new(TEST_USER_SECRET, TEST_SERVICE_SECRET).expect("test GrpcAuth");
        let grpc = PaymentGrpcServer::new(svc.clone(), auth);
        (svc, grpc)
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

fn user_jwt(role: &str, user_id: &str, shop_id: &str) -> String {
    sign_test_jwt(
        TEST_USER_SECRET,
        json!({
            "sub": user_id,
            "role": role,
            "shop_id": shop_id,
            "exp": Utc::now().timestamp() + 300,
        }),
    )
}

fn service_jwt(service_name: &str) -> String {
    sign_test_jwt(
        TEST_SERVICE_SECRET,
        json!({
            "sub": service_name,
            "role": "super_admin",
            "service": service_name,
            "exp": Utc::now().timestamp() + 300,
        }),
    )
}

fn grpc_req<T>(payload: T, token: &str) -> tonic::Request<T> {
    let mut req = tonic::Request::new(payload);
    req.metadata_mut().insert(
        "authorization",
        MetadataValue::try_from(format!("Bearer {token}")).unwrap(),
    );
    req
}

async fn count_ledger_txs(pool: &PgPool) -> i64 {
    sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM ledger_transactions")
        .fetch_one(pool)
        .await
        .expect("count ledger_transactions")
}

async fn count_cod_fee_adjustments(pool: &PgPool) -> i64 {
    sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM cod_fee_adjustments")
        .fetch_one(pool)
        .await
        .expect("count cod_fee_adjustments")
}

#[tokio::test]
async fn test_w3b3_g2_adjust_cod_shipping_fee_auth_attacks() {
    let Some(db) = TestDb::setup("test_w3b3_g2_adjust_cod_shipping_fee_auth_attacks", 4).await
    else {
        return;
    };
    let (svc, grpc) = db.grpc_server();

    let order_id = format!("ord-auth-{}", Uuid::new_v4().simple());
    let created = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-auth-{}", Uuid::new_v4().simple()),
            order_id: order_id.clone(),
            user_id: "buyer-auth-1".into(),
            amount_vnd: 230_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(order_id.clone()),
                shop_id: "shop-auth-1".into(),
                subtotal_vnd: 200_000,
                commission_vnd: 10_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{order_id}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-auth".into(),
        })
        .await
        .expect("create COD payment");

    let req_body = || pb::AdjustCodShippingFeeRequest {
        order_id: order_id.clone(),
        new_shipping_fee_vnd: 45_000,
        idempotency_key: format!("adj-auth-{}", Uuid::new_v4().simple()),
    };

    // Every end-user role (including buyer owner, seller owner, admin, super_admin) -> PermissionDenied.
    for (role, uid, sid) in [
        ("buyer", "buyer-auth-1", ""),
        ("seller", "seller-auth-1", "shop-auth-1"),
        ("admin", "admin-1", ""),
        ("super_admin", "super-1", ""),
        ("moderator", "mod-1", ""),
        ("cskh", "cskh-1", ""),
        ("accountant", "acc-1", ""),
        ("ops", "ops-1", ""),
    ] {
        let err = grpc
            .adjust_cod_shipping_fee(grpc_req(req_body(), &user_jwt(role, uid, sid)))
            .await
            .unwrap_err();
        assert_eq!(
            err.code(),
            tonic::Code::PermissionDenied,
            "end-user role={role} must be rejected"
        );
    }

    // Other service tokens (product-service, shipping-service, gateway) -> PermissionDenied.
    for other_svc in ["product-service", "shipping-service", "gateway"] {
        let err = grpc
            .adjust_cod_shipping_fee(grpc_req(req_body(), &service_jwt(other_svc)))
            .await
            .unwrap_err();
        assert_eq!(
            err.code(),
            tonic::Code::PermissionDenied,
            "service={other_svc} must be rejected"
        );
    }

    // DB remains completely unchanged.
    let after = svc
        .get_payment(Some(created.payment.id), None, None)
        .await
        .expect("reload payment");
    assert_eq!(after.payment.amount_vnd, 230_000);
    assert_eq!(after.allocations[0].shipping_fee_vnd, 30_000);
    assert_eq!(count_cod_fee_adjustments(&db.pool).await, 0);
    assert_eq!(count_ledger_txs(&db.pool).await, 0);

    db.cleanup().await;
}

#[tokio::test]
async fn test_w3b3_g2_adjust_cod_shipping_fee_increase_decrease_multishop_and_ledger() {
    let Some(db) = TestDb::setup(
        "test_w3b3_g2_adjust_cod_shipping_fee_increase_decrease_multishop_and_ledger",
        6,
    )
    .await
    else {
        return;
    };
    let (svc, grpc) = db.grpc_server();
    let order_tok = service_jwt("order-service");

    // 1. Single-shop COD with platform discount: increase (30k -> 45k), then decrease (45k -> 15k),
    // verify 0 ledger tx before delivery, then deliver + complete -> balanced ledger with NEW amounts.
    let ord1 = format!("ord-single-{}", Uuid::new_v4().simple());
    let p1 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-single-{}", Uuid::new_v4().simple()),
            order_id: ord1.clone(),
            user_id: "buyer-1".into(),
            amount_vnd: 210_000, // 200k subtotal + 30k ship - 20k platform discount
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord1.clone()),
                shop_id: "shop-single-1".into(),
                subtotal_vnd: 200_000,
                commission_vnd: 10_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 20_000,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord1}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-single".into(),
        })
        .await
        .expect("create single COD");

    let inc_resp = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord1.clone(),
                new_shipping_fee_vnd: 45_000,
                idempotency_key: "adj-single-inc".into(),
            },
            &order_tok,
        ))
        .await
        .expect("increase COD fee")
        .into_inner();
    assert!(!inc_resp.already_applied);
    assert_eq!(inc_resp.old_shipping_fee_vnd, 30_000);
    assert_eq!(inc_resp.new_shipping_fee_vnd, 45_000);
    assert_eq!(inc_resp.new_order_payable_vnd, 225_000);
    let pay_inc = inc_resp.payment.expect("payment in response");
    assert_eq!(pay_inc.amount_vnd, 225_000);
    assert_eq!(pay_inc.shop_allocations.len(), 1);
    assert_eq!(pay_inc.shop_allocations[0].subtotal_vnd, 200_000);
    assert_eq!(pay_inc.shop_allocations[0].commission_vnd, 10_000);
    assert_eq!(pay_inc.shop_allocations[0].platform_discount_vnd, 20_000);
    assert_eq!(pay_inc.shop_allocations[0].shipping_fee_vnd, 45_000);
    assert_eq!(count_ledger_txs(&db.pool).await, 0);

    let dec_resp = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord1.clone(),
                new_shipping_fee_vnd: 15_000,
                idempotency_key: "adj-single-dec".into(),
            },
            &order_tok,
        ))
        .await
        .expect("decrease COD fee")
        .into_inner();
    assert!(!dec_resp.already_applied);
    assert_eq!(dec_resp.old_shipping_fee_vnd, 45_000);
    assert_eq!(dec_resp.new_shipping_fee_vnd, 15_000);
    assert_eq!(dec_resp.new_order_payable_vnd, 195_000);
    assert_eq!(dec_resp.payment.unwrap().amount_vnd, 195_000);
    assert_eq!(count_ledger_txs(&db.pool).await, 0);
    assert_eq!(count_cod_fee_adjustments(&db.pool).await, 2);

    // Deliver & complete single-shop COD: ledger uses adjusted 15k shipping fee / 195k payable.
    svc.process_order_delivered_event_with_shipping(
        "evt-del-single-1",
        &ord1,
        Some("TRK-SINGLE-1"),
        Some("mock_express"),
        Some(&ord1),
        "trace-del-1",
    )
    .await
    .expect("order.delivered");
    svc.process_order_completed_event("evt-cmp-single-1", &ord1, "trace-cmp-1")
        .await
        .expect("order.completed");

    let summary1 = svc
        .get_ledger_summary(None)
        .await
        .expect("ledger summary 1");
    assert!(summary1.is_balanced);
    assert_eq!(summary1.total_debits_vnd, summary1.total_credits_vnd);
    let find_bal = |acc: &str| {
        summary1
            .accounts
            .iter()
            .find(|a| a.account_id == acc)
            .map(|a| a.balance_vnd)
            .unwrap_or(0)
    };
    assert_eq!(find_bal("cod_receivable"), 195_000);
    assert_eq!(find_bal("shipping_fee_payable"), 15_000);
    assert_eq!(find_bal("platform_fee_revenue"), 10_000);
    assert_eq!(find_bal("platform_promo_expense"), 20_000);
    assert_eq!(find_bal("seller_pending:shop-single-1"), 190_000);
    assert_eq!(find_bal("escrow_liability"), 0);
    let p1_done = svc
        .get_payment(Some(p1.payment.id), None, None)
        .await
        .expect("reload p1");
    assert_eq!(p1_done.payment.status, "succeeded");
    assert_eq!(p1_done.payment.amount_vnd, 195_000);

    // 2. Multi-shop COD (3 shops: A delivered/held, B cancelled, C awaiting_collection):
    // Adjusting C updates C's shipping fee and recomputes parent amount excluding cancelled B.
    let grp_m = format!("grp-multi-{}", Uuid::new_v4().simple());
    let ord_a = format!("ord-m-a-{}", Uuid::new_v4().simple());
    let ord_b = format!("ord-m-b-{}", Uuid::new_v4().simple());
    let ord_c = format!("ord-m-c-{}", Uuid::new_v4().simple());
    let pm = svc
        .create_payment(CreatePaymentInput {
            order_group_id: grp_m.clone(),
            order_id: grp_m.clone(),
            user_id: "buyer-m".into(),
            amount_vnd: 450_000, // A: 120k, B: 100k, C: 230k
            method: PaymentMethod::Cod,
            shop_allocations: vec![
                ShopAllocationInput {
                    order_id: Some(ord_a.clone()),
                    shop_id: "shop-m-a".into(),
                    subtotal_vnd: 100_000,
                    commission_vnd: 5_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 0,
                },
                ShopAllocationInput {
                    order_id: Some(ord_b.clone()),
                    shop_id: "shop-m-b".into(),
                    subtotal_vnd: 80_000,
                    commission_vnd: 4_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 0,
                },
                ShopAllocationInput {
                    order_id: Some(ord_c.clone()),
                    shop_id: "shop-m-c".into(),
                    subtotal_vnd: 200_000,
                    commission_vnd: 10_000,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                    shipping_fee_vnd: 30_000,
                    platform_discount_vnd: 0,
                },
            ],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{grp_m}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-multi".into(),
        })
        .await
        .expect("create multi-shop COD");

    // Deliver A (held) and cancel B (cancelled); parent payment remains pending because C is awaiting.
    svc.process_order_delivered_event_with_shipping(
        "evt-del-m-a",
        &ord_a,
        Some("TRK-MA"),
        Some("mock_express"),
        Some(&ord_a),
        "trace-ma",
    )
    .await
    .expect("deliver A");
    svc.process_order_cancelled_event("evt-can-m-b", &ord_b, "trace-mb")
        .await
        .expect("cancel B");

    // Adjusting held A or cancelled B must fail with FailedPrecondition COD_ALREADY_COLLECTED.
    for (label, target_ord) in [("held A", &ord_a), ("cancelled B", &ord_b)] {
        let err = grpc
            .adjust_cod_shipping_fee(grpc_req(
                pb::AdjustCodShippingFeeRequest {
                    order_id: target_ord.clone(),
                    new_shipping_fee_vnd: 50_000,
                    idempotency_key: format!("adj-bad-{label}"),
                },
                &order_tok,
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::FailedPrecondition, "{label}");
        assert!(
            err.message().starts_with(ERR_COD_ALREADY_COLLECTED),
            "{label}: unexpected message={}",
            err.message()
        );
    }

    // Adjust awaiting C: 30k -> 55k -> new_order_payable = 255k, parent amount = A(120k) + C(255k) = 375k.
    let ledger_before_c = count_ledger_txs(&db.pool).await;
    let adj_c = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_c.clone(),
                new_shipping_fee_vnd: 55_000,
                idempotency_key: "adj-m-c-1".into(),
            },
            &order_tok,
        ))
        .await
        .expect("adjust C")
        .into_inner();
    assert!(!adj_c.already_applied);
    assert_eq!(adj_c.old_shipping_fee_vnd, 30_000);
    assert_eq!(adj_c.new_shipping_fee_vnd, 55_000);
    assert_eq!(adj_c.new_order_payable_vnd, 255_000);
    assert_eq!(adj_c.payment.unwrap().amount_vnd, 375_000);
    assert_eq!(count_ledger_txs(&db.pool).await, ledger_before_c);

    // Deliver C and complete both A and C -> total debits == total credits.
    svc.process_order_delivered_event_with_shipping(
        "evt-del-m-c",
        &ord_c,
        Some("TRK-MC"),
        Some("mock_express"),
        Some(&ord_c),
        "trace-mc",
    )
    .await
    .expect("deliver C");
    svc.process_order_completed_event("evt-cmp-m-a", &ord_a, "trace-cmp-ma")
        .await
        .expect("complete A");
    svc.process_order_completed_event("evt-cmp-m-c", &ord_c, "trace-cmp-mc")
        .await
        .expect("complete C");

    let summary_all = svc
        .get_ledger_summary(None)
        .await
        .expect("ledger summary all");
    assert!(summary_all.is_balanced);
    assert_eq!(summary_all.total_debits_vnd, summary_all.total_credits_vnd);
    let pm_done = svc
        .get_payment(Some(pm.payment.id), None, None)
        .await
        .expect("reload pm");
    assert_eq!(pm_done.payment.status, "succeeded");
    assert_eq!(pm_done.payment.amount_vnd, 375_000);

    db.cleanup().await;
}

#[tokio::test]
async fn test_w3b3_g2_adjust_cod_shipping_fee_state_and_money_attacks() {
    let Some(db) = TestDb::setup(
        "test_w3b3_g2_adjust_cod_shipping_fee_state_and_money_attacks",
        6,
    )
    .await
    else {
        return;
    };
    let (svc, grpc) = db.grpc_server();
    let order_tok = service_jwt("order-service");

    // 1. VIETQR pending & succeeded -> FailedPrecondition COD_ALREADY_COLLECTED.
    let ord_vq = format!("ord-vq-{}", Uuid::new_v4().simple());
    let pvq = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-vq-{}", Uuid::new_v4().simple()),
            order_id: ord_vq.clone(),
            user_id: "buyer-vq".into(),
            amount_vnd: 230_000,
            method: PaymentMethod::VietQr,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_vq.clone()),
                shop_id: "shop-vq".into(),
                subtotal_vnd: 200_000,
                commission_vnd: 10_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_vq}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-vq".into(),
        })
        .await
        .expect("create VietQR");

    let err_vq_pending = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_vq.clone(),
                new_shipping_fee_vnd: 40_000,
                idempotency_key: "adj-vq-pending".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_vq_pending.code(), tonic::Code::FailedPrecondition);
    assert!(err_vq_pending
        .message()
        .starts_with(ERR_COD_ALREADY_COLLECTED));

    // Confirm VietQR via webhook and verify still rejected with COD_ALREADY_COLLECTED.
    let _ = svc
        .process_verified_webhook(
            IncomingWebhookPayload {
                provider: Some("mock".into()),
                provider_tx_id: format!("vq-tx-{}", Uuid::new_v4().simple()),
                payment_id: Some(pvq.payment.id.to_string()),
                provider_order_code: Some(pvq.payment.provider_order_code.clone()),
                order_group_id: Some(pvq.payment.order_group_id.clone()),
                order_id: Some(ord_vq.clone()),
                reference: None,
                amount_vnd: 230_000,
                status: "succeeded".into(),
                occurred_at: None,
            },
            json!({"status": "succeeded"}),
            "mock",
            "trace-vq-wh",
        )
        .await
        .expect("confirm VietQR");
    let err_vq_paid = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_vq.clone(),
                new_shipping_fee_vnd: 40_000,
                idempotency_key: "adj-vq-paid".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_vq_paid.code(), tonic::Code::FailedPrecondition);
    assert!(err_vq_paid.message().starts_with(ERR_COD_ALREADY_COLLECTED));

    // 2. ZALOPAY pending -> FailedPrecondition COD_ALREADY_COLLECTED.
    let ord_zp = format!("ord-zp-{}", Uuid::new_v4().simple());
    let _ = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-zp-{}", Uuid::new_v4().simple()),
            order_id: ord_zp.clone(),
            user_id: "buyer-zp".into(),
            amount_vnd: 230_000,
            method: PaymentMethod::ZaloPay,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_zp.clone()),
                shop_id: "shop-zp".into(),
                subtotal_vnd: 200_000,
                commission_vnd: 10_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_zp}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-zp".into(),
        })
        .await
        .expect("create ZaloPay");
    let err_zp = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_zp.clone(),
                new_shipping_fee_vnd: 40_000,
                idempotency_key: "adj-zp-pending".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_zp.code(), tonic::Code::FailedPrecondition);
    assert!(err_zp.message().starts_with(ERR_COD_ALREADY_COLLECTED));

    // 3. COD delivered (held), completed (pending_release), released -> COD_ALREADY_COLLECTED.
    let ord_del = format!("ord-del-{}", Uuid::new_v4().simple());
    let _ = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-del-{}", Uuid::new_v4().simple()),
            order_id: ord_del.clone(),
            user_id: "buyer-del".into(),
            amount_vnd: 130_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_del.clone()),
                shop_id: "shop-del".into(),
                subtotal_vnd: 100_000,
                commission_vnd: 5_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_del}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-del".into(),
        })
        .await
        .expect("create COD for deliver");
    svc.process_order_delivered_event_with_shipping(
        "evt-del-state",
        &ord_del,
        Some("TRK-STATE"),
        Some("mock_express"),
        Some(&ord_del),
        "trace-del",
    )
    .await
    .expect("deliver COD");
    let err_held = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_del.clone(),
                new_shipping_fee_vnd: 40_000,
                idempotency_key: "adj-cod-held".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_held.code(), tonic::Code::FailedPrecondition);
    assert!(err_held.message().starts_with(ERR_COD_ALREADY_COLLECTED));

    svc.process_order_completed_event("evt-cmp-state", &ord_del, "trace-cmp")
        .await
        .expect("complete COD");
    let err_pending_rel = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_del.clone(),
                new_shipping_fee_vnd: 40_000,
                idempotency_key: "adj-cod-pending-rel".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_pending_rel.code(), tonic::Code::FailedPrecondition);
    assert!(err_pending_rel
        .message()
        .starts_with(ERR_COD_ALREADY_COLLECTED));

    let _ = svc
        .release_escrow(&ord_del, Some("shop-del"))
        .await
        .expect("release escrow");
    let err_released = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_del.clone(),
                new_shipping_fee_vnd: 40_000,
                idempotency_key: "adj-cod-released".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_released.code(), tonic::Code::FailedPrecondition);
    assert!(err_released
        .message()
        .starts_with(ERR_COD_ALREADY_COLLECTED));

    // 4. COD cancelled -> FailedPrecondition COD_ALREADY_COLLECTED.
    let ord_can = format!("ord-can-{}", Uuid::new_v4().simple());
    let _ = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-can-{}", Uuid::new_v4().simple()),
            order_id: ord_can.clone(),
            user_id: "buyer-can".into(),
            amount_vnd: 130_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_can.clone()),
                shop_id: "shop-can".into(),
                subtotal_vnd: 100_000,
                commission_vnd: 5_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_can}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-can".into(),
        })
        .await
        .expect("create COD for cancel");
    svc.process_order_cancelled_event("evt-can-state", &ord_can, "trace-can")
        .await
        .expect("cancel COD");
    let err_can = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_can.clone(),
                new_shipping_fee_vnd: 40_000,
                idempotency_key: "adj-cod-can".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_can.code(), tonic::Code::FailedPrecondition);
    assert!(err_can.message().starts_with(ERR_COD_ALREADY_COLLECTED));

    // 5. Negative fee -> InvalidArgument; payable < 0 -> FailedPrecondition; unknown order -> NotFound.
    let ord_disc = format!("ord-disc-{}", Uuid::new_v4().simple());
    let _ = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-disc-{}", Uuid::new_v4().simple()),
            order_id: ord_disc.clone(),
            user_id: "buyer-disc".into(),
            amount_vnd: 10_000, // 100k + 30k - 120k = 10k
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord_disc.clone()),
                shop_id: "shop-disc".into(),
                subtotal_vnd: 100_000,
                commission_vnd: 5_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 120_000,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord_disc}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-disc".into(),
        })
        .await
        .expect("create discounted COD");

    let err_neg_fee = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_disc.clone(),
                new_shipping_fee_vnd: -1,
                idempotency_key: "adj-neg-fee".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_neg_fee.code(), tonic::Code::InvalidArgument);

    let err_neg_payable = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord_disc.clone(),
                new_shipping_fee_vnd: 10_000, // 100k + 10k - 120k = -10k < 0
                idempotency_key: "adj-neg-payable".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_neg_payable.code(), tonic::Code::FailedPrecondition);

    let err_unknown = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: "ord-does-not-exist".into(),
                new_shipping_fee_vnd: 25_000,
                idempotency_key: "adj-unknown".into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_unknown.code(), tonic::Code::NotFound);

    assert_eq!(count_cod_fee_adjustments(&db.pool).await, 0);

    db.cleanup().await;
}

#[tokio::test]
async fn test_w3b3_g2_adjust_cod_shipping_fee_idempotency_5x_and_conflict() {
    let Some(db) = TestDb::setup(
        "test_w3b3_g2_adjust_cod_shipping_fee_idempotency_5x_and_conflict",
        8,
    )
    .await
    else {
        return;
    };
    let (svc, grpc) = db.grpc_server();
    let order_tok = service_jwt("order-service");

    let ord1 = format!("ord-idem-1-{}", Uuid::new_v4().simple());
    let p1 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-idem-1-{}", Uuid::new_v4().simple()),
            order_id: ord1.clone(),
            user_id: "buyer-idem".into(),
            amount_vnd: 230_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord1.clone()),
                shop_id: "shop-idem-1".into(),
                subtotal_vnd: 200_000,
                commission_vnd: 10_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord1}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-idem-1".into(),
        })
        .await
        .expect("create COD 1");

    // 1. Sequential 5x replay with same idempotency_key -> first applies, next 4 already_applied=true.
    let key_seq = "adj-idem-seq-5x";
    for i in 0..5 {
        let resp = grpc
            .adjust_cod_shipping_fee(grpc_req(
                pb::AdjustCodShippingFeeRequest {
                    order_id: ord1.clone(),
                    new_shipping_fee_vnd: 48_000,
                    idempotency_key: key_seq.into(),
                },
                &order_tok,
            ))
            .await
            .unwrap_or_else(|e| panic!("sequential call #{i} failed: {e}"))
            .into_inner();
        assert_eq!(resp.already_applied, i > 0, "call #{i}");
        assert_eq!(resp.old_shipping_fee_vnd, 30_000);
        assert_eq!(resp.new_shipping_fee_vnd, 48_000);
        assert_eq!(resp.new_order_payable_vnd, 248_000);
        assert_eq!(resp.payment.unwrap().amount_vnd, 248_000);
    }
    assert_eq!(count_cod_fee_adjustments(&db.pool).await, 1);

    // Same idempotency_key with different new_shipping_fee_vnd or different order_id -> FailedPrecondition.
    let err_diff_fee = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord1.clone(),
                new_shipping_fee_vnd: 50_000,
                idempotency_key: key_seq.into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_diff_fee.code(), tonic::Code::FailedPrecondition);

    let err_diff_order = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: "ord-some-other".into(),
                new_shipping_fee_vnd: 48_000,
                idempotency_key: key_seq.into(),
            },
            &order_tok,
        ))
        .await
        .unwrap_err();
    assert_eq!(err_diff_order.code(), tonic::Code::FailedPrecondition);

    // 2. Concurrent 5x burst with same idempotency_key via Barrier -> exactly 1 fresh, 4 already_applied.
    let ord2 = format!("ord-idem-2-{}", Uuid::new_v4().simple());
    let p2 = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-idem-2-{}", Uuid::new_v4().simple()),
            order_id: ord2.clone(),
            user_id: "buyer-idem-2".into(),
            amount_vnd: 180_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord2.clone()),
                shop_id: "shop-idem-2".into(),
                subtotal_vnd: 150_000,
                commission_vnd: 7_500,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord2}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-idem-2".into(),
        })
        .await
        .expect("create COD 2");

    let barrier = Arc::new(Barrier::new(5));
    let mut handles = Vec::with_capacity(5);
    for _ in 0..5 {
        let g = grpc.clone();
        let tok = order_tok.clone();
        let oid = ord2.clone();
        let b = barrier.clone();
        handles.push(tokio::spawn(async move {
            b.wait().await;
            g.adjust_cod_shipping_fee(grpc_req(
                pb::AdjustCodShippingFeeRequest {
                    order_id: oid,
                    new_shipping_fee_vnd: 22_000,
                    idempotency_key: "adj-idem-conc-5x".into(),
                },
                &tok,
            ))
            .await
            .expect("concurrent idempotent call")
            .into_inner()
        }));
    }

    let mut fresh_count = 0usize;
    let mut replay_count = 0usize;
    for h in handles {
        let r = h.await.expect("join concurrent idempotent task");
        if r.already_applied {
            replay_count += 1;
        } else {
            fresh_count += 1;
        }
        assert_eq!(r.old_shipping_fee_vnd, 30_000);
        assert_eq!(r.new_shipping_fee_vnd, 22_000);
        assert_eq!(r.new_order_payable_vnd, 172_000);
        assert_eq!(r.payment.unwrap().amount_vnd, 172_000);
    }
    assert_eq!(fresh_count, 1);
    assert_eq!(replay_count, 4);
    assert_eq!(count_cod_fee_adjustments(&db.pool).await, 2);

    // 3. Concurrent cross-order idempotency_key collision: two distinct COD orders sharing one
    // key via Barrier -> exactly 1 succeeds and 1 fails with FailedPrecondition (loser rolled back).
    let ord3a = format!("ord-idem-3a-{}", Uuid::new_v4().simple());
    let ord3b = format!("ord-idem-3b-{}", Uuid::new_v4().simple());
    let p3a = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-idem-3a-{}", Uuid::new_v4().simple()),
            order_id: ord3a.clone(),
            user_id: "buyer-3a".into(),
            amount_vnd: 130_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord3a.clone()),
                shop_id: "shop-3a".into(),
                subtotal_vnd: 100_000,
                commission_vnd: 5_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord3a}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-3a".into(),
        })
        .await
        .expect("create COD 3a");
    let p3b = svc
        .create_payment(CreatePaymentInput {
            order_group_id: format!("grp-idem-3b-{}", Uuid::new_v4().simple()),
            order_id: ord3b.clone(),
            user_id: "buyer-3b".into(),
            amount_vnd: 130_000,
            method: PaymentMethod::Cod,
            shop_allocations: vec![ShopAllocationInput {
                order_id: Some(ord3b.clone()),
                shop_id: "shop-3b".into(),
                subtotal_vnd: 100_000,
                commission_vnd: 5_000,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
            }],
            expires_at: None,
            idempotency_key: Some(format!("checkout:{ord3b}")),
            supersede_previous: false,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
            trace_id: "trace-3b".into(),
        })
        .await
        .expect("create COD 3b");

    let clash_barrier = Arc::new(Barrier::new(2));
    let clash_key = "adj-idem-cross-order-clash";
    let (h3a, h3b) = {
        let ga = grpc.clone();
        let gb = grpc.clone();
        let ta = order_tok.clone();
        let tb = order_tok.clone();
        let oa = ord3a.clone();
        let ob = ord3b.clone();
        let ba = clash_barrier.clone();
        let bb = clash_barrier.clone();
        (
            tokio::spawn(async move {
                ba.wait().await;
                ga.adjust_cod_shipping_fee(grpc_req(
                    pb::AdjustCodShippingFeeRequest {
                        order_id: oa,
                        new_shipping_fee_vnd: 45_000,
                        idempotency_key: clash_key.into(),
                    },
                    &ta,
                ))
                .await
            }),
            tokio::spawn(async move {
                bb.wait().await;
                gb.adjust_cod_shipping_fee(grpc_req(
                    pb::AdjustCodShippingFeeRequest {
                        order_id: ob,
                        new_shipping_fee_vnd: 45_000,
                        idempotency_key: clash_key.into(),
                    },
                    &tb,
                ))
                .await
            }),
        )
    };
    let (r3a, r3b) = tokio::join!(h3a, h3b);
    let r3a = r3a.expect("join 3a");
    let r3b = r3b.expect("join 3b");
    let p3a_after = svc
        .get_payment(Some(p3a.payment.id), None, None)
        .await
        .unwrap();
    let p3b_after = svc
        .get_payment(Some(p3b.payment.id), None, None)
        .await
        .unwrap();
    match (r3a, r3b) {
        (Ok(_), Err(e)) => {
            assert_eq!(e.code(), tonic::Code::FailedPrecondition);
            assert_eq!(p3a_after.payment.amount_vnd, 145_000);
            assert_eq!(p3b_after.payment.amount_vnd, 130_000);
        }
        (Err(e), Ok(_)) => {
            assert_eq!(e.code(), tonic::Code::FailedPrecondition);
            assert_eq!(p3a_after.payment.amount_vnd, 130_000);
            assert_eq!(p3b_after.payment.amount_vnd, 145_000);
        }
        other => panic!("expected exactly one Ok and one FailedPrecondition, got {other:?}"),
    }
    assert_eq!(count_cod_fee_adjustments(&db.pool).await, 3);

    // 4. Replaying an already-applied idempotency_key AFTER order.delivered still returns already_applied=true.
    svc.process_order_delivered_event_with_shipping(
        "evt-del-idem-1",
        &ord1,
        Some("TRK-IDEM-1"),
        Some("mock_express"),
        Some(&ord1),
        "trace-del-idem-1",
    )
    .await
    .expect("deliver ord1");
    let post_del_replay = grpc
        .adjust_cod_shipping_fee(grpc_req(
            pb::AdjustCodShippingFeeRequest {
                order_id: ord1.clone(),
                new_shipping_fee_vnd: 48_000,
                idempotency_key: key_seq.into(),
            },
            &order_tok,
        ))
        .await
        .expect("post-delivery idempotent replay")
        .into_inner();
    assert!(post_del_replay.already_applied);
    assert_eq!(post_del_replay.new_order_payable_vnd, 248_000);

    // Final DB state matches single application per order.
    let p1_final = svc
        .get_payment(Some(p1.payment.id), None, None)
        .await
        .unwrap();
    let p2_final = svc
        .get_payment(Some(p2.payment.id), None, None)
        .await
        .unwrap();
    assert_eq!(p1_final.payment.amount_vnd, 248_000);
    assert_eq!(p2_final.payment.amount_vnd, 172_000);

    db.cleanup().await;
}

#[tokio::test]
async fn test_w3b3_g2_adjust_cod_shipping_fee_race_with_order_delivered() {
    let Some(db) = TestDb::setup(
        "test_w3b3_g2_adjust_cod_shipping_fee_race_with_order_delivered",
        10,
    )
    .await
    else {
        return;
    };
    let (svc, grpc) = db.grpc_server();
    let order_tok = service_jwt("order-service");

    let mut adjust_won_count = 0usize;
    let mut deliver_won_count = 0usize;
    let mut expected_total_cod_receivable = 0_i64;
    let mut expected_total_shipping_payable = 0_i64;

    // Run 8 back-to-back concurrent races inside this test (test itself is also repeated 50x).
    for round in 0..8 {
        let ord = format!("ord-race-{round}-{}", Uuid::new_v4().simple());
        let old_fee = 30_000_i64;
        let new_fee = if round % 2 == 0 {
            47_000_i64
        } else {
            18_000_i64
        };
        let subtotal = 200_000_i64;
        let commission = 10_000_i64;
        let discount = 15_000_i64;
        let old_payable = subtotal + old_fee - discount;
        let new_payable = subtotal + new_fee - discount;

        let created = svc
            .create_payment(CreatePaymentInput {
                order_group_id: format!("grp-race-{round}-{}", Uuid::new_v4().simple()),
                order_id: ord.clone(),
                user_id: format!("buyer-race-{round}"),
                amount_vnd: old_payable,
                method: PaymentMethod::Cod,
                shop_allocations: vec![ShopAllocationInput {
                    order_id: Some(ord.clone()),
                    shop_id: format!("shop-race-{round}"),
                    subtotal_vnd: subtotal,
                    commission_vnd: commission,
                    shipping_fee_vnd: old_fee,
                    platform_discount_vnd: discount,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                }],
                expires_at: None,
                idempotency_key: Some(format!("checkout:{ord}")),
                supersede_previous: false,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                trace_id: format!("trace-race-{round}"),
            })
            .await
            .expect("create COD for race");

        let barrier = Arc::new(Barrier::new(2));
        let g = grpc.clone();
        let tok = order_tok.clone();
        let ord_adj = ord.clone();
        let b_adj = barrier.clone();
        let adj_handle = tokio::spawn(async move {
            b_adj.wait().await;
            g.adjust_cod_shipping_fee(grpc_req(
                pb::AdjustCodShippingFeeRequest {
                    order_id: ord_adj,
                    new_shipping_fee_vnd: new_fee,
                    idempotency_key: format!("adj-race-{round}"),
                },
                &tok,
            ))
            .await
        });

        let s = svc.clone();
        let ord_del = ord.clone();
        let b_del = barrier.clone();
        let del_handle = tokio::spawn(async move {
            b_del.wait().await;
            s.process_order_delivered_event_with_shipping(
                &format!("evt-del-race-{round}"),
                &ord_del,
                Some(&format!("TRK-RACE-{round}")),
                Some("mock_express"),
                Some(&ord_del),
                &format!("trace-del-race-{round}"),
            )
            .await
        });

        let (adj_res, del_res) = tokio::join!(adj_handle, del_handle);
        let adj_res = adj_res.expect("join adjust task");
        del_res
            .expect("join deliver task")
            .expect("order.delivered must succeed in both orderings");

        svc.process_order_completed_event(
            &format!("evt-cmp-race-{round}"),
            &ord,
            &format!("trace-cmp-race-{round}"),
        )
        .await
        .expect("order.completed");

        let final_pay = svc
            .get_payment(Some(created.payment.id), None, None)
            .await
            .expect("reload raced payment");
        assert_eq!(final_pay.payment.status, "succeeded");
        assert_eq!(final_pay.allocations[0].escrow_status, "pending_release");

        match adj_res {
            Ok(resp) => {
                adjust_won_count += 1;
                let inner = resp.into_inner();
                assert!(!inner.already_applied);
                assert_eq!(inner.new_shipping_fee_vnd, new_fee);
                assert_eq!(inner.new_order_payable_vnd, new_payable);
                assert_eq!(final_pay.payment.amount_vnd, new_payable);
                assert_eq!(final_pay.allocations[0].shipping_fee_vnd, new_fee);
                expected_total_cod_receivable += new_payable;
                expected_total_shipping_payable += new_fee;
            }
            Err(status) => {
                deliver_won_count += 1;
                assert_eq!(
                    status.code(),
                    tonic::Code::FailedPrecondition,
                    "round {round}: unexpected error {status:?}"
                );
                assert!(
                    status.message().starts_with(ERR_COD_ALREADY_COLLECTED),
                    "round {round}: message={}",
                    status.message()
                );
                assert_eq!(final_pay.payment.amount_vnd, old_payable);
                assert_eq!(final_pay.allocations[0].shipping_fee_vnd, old_fee);
                expected_total_cod_receivable += old_payable;
                expected_total_shipping_payable += old_fee;
            }
        }
    }

    assert_eq!(adjust_won_count + deliver_won_count, 8);
    let summary = svc
        .get_ledger_summary(None)
        .await
        .expect("ledger summary race");
    assert!(summary.is_balanced);
    assert_eq!(summary.total_debits_vnd, summary.total_credits_vnd);
    let cod_rec = summary
        .accounts
        .iter()
        .find(|a| a.account_id == "cod_receivable")
        .map(|a| a.balance_vnd)
        .unwrap_or(0);
    let ship_pay = summary
        .accounts
        .iter()
        .find(|a| a.account_id == "shipping_fee_payable")
        .map(|a| a.balance_vnd)
        .unwrap_or(0);
    let escrow_bal = summary
        .accounts
        .iter()
        .find(|a| a.account_id == "escrow_liability")
        .map(|a| a.balance_vnd)
        .unwrap_or(0);
    assert_eq!(cod_rec, expected_total_cod_receivable);
    assert_eq!(ship_pay, expected_total_shipping_payable);
    assert_eq!(escrow_bal, 0);

    db.cleanup().await;
}
