use std::time::Instant;

use chrono::{DateTime, NaiveDate, Utc};
use prost_types::Timestamp;
use tonic::{Request, Response, Status};
use uuid::Uuid;

use crate::auth::{require_payment_read_access, Caller, GrpcAuth};
use crate::domain::{
    CreatePaymentInput, CreateRefundInput, PaymentMethod as DomainMethod, PaymentWithAllocations,
    ReconciliationItemRecord, RefundRecord, ShopAllocationInput,
};
use crate::service::PaymentService;
use crate::telemetry::extract_or_generate_trace_id;

#[allow(clippy::result_large_err)]
pub mod pb {
    tonic::include_proto!("nexusmart.payment.v1");
}

use pb::payment_service_server::PaymentService as PaymentServiceTrait;
use pb::{
    AccountBalance, CancelPaymentRequest, CancelPaymentResponse, CreatePaymentRequest,
    CreatePaymentResponse, CreateRefundRequest, CreateRefundResponse, GetLedgerSummaryRequest,
    GetLedgerSummaryResponse, GetPaymentRequest, GetPaymentResponse, GetSellerBalanceRequest,
    GetSellerBalanceResponse, Payment, PaymentMethod, PaymentStatus, ReconciliationItem,
    ReconciliationStatus, Refund, RefundStatus, ReleaseEscrowRequest, ReleaseEscrowResponse,
    RunReconciliationRequest, RunReconciliationResponse, ShopAllocation,
};

#[derive(Debug, Clone, Default)]
struct CallerAuth {
    caller: Caller,
}

impl CallerAuth {
    /// Authenticates from the verified Bearer JWT only (see crate::auth).
    #[allow(clippy::result_large_err)]
    fn from_request<T>(
        auth: &GrpcAuth,
        request: &Request<T>,
        method: &str,
    ) -> Result<Self, Status> {
        let caller = auth.authenticate(request.metadata(), method)?;
        Ok(Self { caller })
    }

    fn has_role(&self) -> bool {
        !self.caller.role.is_empty()
    }

    fn is_admin(&self) -> bool {
        self.caller.is_admin()
    }

    #[allow(clippy::result_large_err)]
    fn require_admin(&self, op: &str) -> Result<(), Status> {
        if self.has_role() && !self.is_admin() {
            return Err(Status::permission_denied(format!(
                "forbidden: {op} requires admin role"
            )));
        }
        Ok(())
    }

    #[allow(clippy::result_large_err)]
    fn require_payment_read_access(&self, pwa: &PaymentWithAllocations) -> Result<(), Status> {
        require_payment_read_access(&self.caller, pwa).map_err(|e| e.to_tonic_status())
    }

    #[allow(clippy::result_large_err)]
    fn require_payment_owner(&self, owner_user_id: &str, op: &str) -> Result<(), Status> {
        if !self.has_role() || self.is_admin() {
            return Ok(());
        }
        if !self.caller.user_id.is_empty() && owner_user_id == self.caller.user_id {
            return Ok(());
        }
        Err(Status::permission_denied(format!(
            "forbidden: {op} denied for another user's payment"
        )))
    }

    #[allow(clippy::result_large_err)]
    fn require_seller_shop_access(&self, target_shop_id: &str) -> Result<(), Status> {
        if !self.has_role() || self.is_admin() {
            return Ok(());
        }
        if self.caller.role == "seller"
            && !self.caller.shop_id.is_empty()
            && self.caller.shop_id == target_shop_id
        {
            return Ok(());
        }
        Err(Status::permission_denied(
            "forbidden: seller can only access own shop balance",
        ))
    }
}

#[derive(Clone)]
pub struct PaymentGrpcServer {
    service: PaymentService,
    auth: GrpcAuth,
}

impl PaymentGrpcServer {
    pub fn new(service: PaymentService, auth: GrpcAuth) -> Self {
        Self { service, auth }
    }

    pub fn into_service(self) -> pb::payment_service_server::PaymentServiceServer<Self> {
        pb::payment_service_server::PaymentServiceServer::new(self)
    }

    fn record_metrics<T>(&self, method: &str, start: Instant, res: &Result<Response<T>, Status>) {
        let elapsed = start.elapsed().as_secs_f64();
        match res {
            Ok(_) => self
                .service
                .metrics()
                .record_grpc(method, "OK", false, elapsed),
            Err(status) => {
                let code = format!("{:?}", status.code());
                self.service
                    .metrics()
                    .record_grpc(method, &code, true, elapsed);
            }
        }
    }
}

#[tonic::async_trait]
impl PaymentServiceTrait for PaymentGrpcServer {
    async fn create_payment(
        &self,
        request: Request<CreatePaymentRequest>,
    ) -> Result<Response<CreatePaymentResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let caller = CallerAuth::from_request(&self.auth, &request, "CreatePayment");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_payment_owner(&req.user_id, "CreatePayment")?;

            let method =
                match PaymentMethod::try_from(req.method).unwrap_or(PaymentMethod::Unspecified) {
                    PaymentMethod::Cod => DomainMethod::Cod,
                    PaymentMethod::Vietqr => DomainMethod::VietQr,
                    PaymentMethod::Unspecified => {
                        return Err(Status::invalid_argument(
                            "method must be PAYMENT_METHOD_COD or PAYMENT_METHOD_VIETQR",
                        ));
                    }
                };

            let shop_allocations = req
                .shop_allocations
                .into_iter()
                .map(|a| ShopAllocationInput {
                    order_id: if a.order_id.trim().is_empty() {
                        None
                    } else {
                        Some(a.order_id)
                    },
                    shop_id: a.shop_id,
                    subtotal_vnd: a.subtotal_vnd,
                    commission_vnd: a.commission_vnd,
                    shipping_fee_vnd: a.shipping_fee_vnd,
                })
                .collect();

            let expires_at = req.expires_at.and_then(proto_to_chrono);
            let idempotency_key = if req.idempotency_key.trim().is_empty() {
                None
            } else {
                Some(req.idempotency_key)
            };

            let created = self
                .service
                .create_payment(CreatePaymentInput {
                    order_group_id: req.order_group_id,
                    order_id: req.order_id,
                    user_id: req.user_id,
                    amount_vnd: req.amount_vnd,
                    method,
                    shop_allocations,
                    expires_at,
                    idempotency_key,
                    supersede_previous: req.supersede_previous,
                    trace_id,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(CreatePaymentResponse {
                payment: Some(to_proto_payment(created)),
            }))
        }
        .await;

        self.record_metrics("CreatePayment", start, &result);
        result
    }

    async fn get_payment(
        &self,
        request: Request<GetPaymentRequest>,
    ) -> Result<Response<GetPaymentResponse>, Status> {
        let start = Instant::now();
        let caller = CallerAuth::from_request(&self.auth, &request, "GetPayment");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            let payment_id = parse_optional_uuid(&req.payment_id, "payment_id")
                .map_err(|e| e.to_tonic_status())?;
            let order_group_id = non_empty(&req.order_group_id);
            let order_id = non_empty(&req.order_id);

            let found = self
                .service
                .get_payment(payment_id, order_group_id, order_id)
                .await
                .map_err(|e| e.to_tonic_status())?;

            caller.require_payment_read_access(&found)?;

            Ok(Response::new(GetPaymentResponse {
                payment: Some(to_proto_payment(found)),
            }))
        }
        .await;

        self.record_metrics("GetPayment", start, &result);
        result
    }

    async fn cancel_payment(
        &self,
        request: Request<CancelPaymentRequest>,
    ) -> Result<Response<CancelPaymentResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let caller = CallerAuth::from_request(&self.auth, &request, "CancelPayment");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            let payment_id = parse_optional_uuid(&req.payment_id, "payment_id")
                .map_err(|e| e.to_tonic_status())?;
            let order_group_id = non_empty(&req.order_group_id);

            // Defense-in-depth ownership check BEFORE cancelling when caller role metadata is present.
            if caller.has_role() && !caller.is_admin() {
                if caller.caller.user_id.is_empty() {
                    return Err(Status::permission_denied(
                        "forbidden: missing x-user-id for CancelPayment",
                    ));
                }
                let existing = self
                    .service
                    .get_payment(payment_id, order_group_id, None)
                    .await
                    .map_err(|e| e.to_tonic_status())?;
                caller.require_payment_owner(&existing.payment.user_id, "CancelPayment")?;
            }

            let cancelled = self
                .service
                .cancel_payment(payment_id, order_group_id, &req.reason, &trace_id)
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(CancelPaymentResponse {
                payment: Some(to_proto_payment(cancelled)),
            }))
        }
        .await;

        self.record_metrics("CancelPayment", start, &result);
        result
    }

    async fn create_refund(
        &self,
        request: Request<CreateRefundRequest>,
    ) -> Result<Response<CreateRefundResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let caller = CallerAuth::from_request(&self.auth, &request, "CreateRefund");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_admin("CreateRefund")?;

            let payment_id = parse_optional_uuid(&req.payment_id, "payment_id")
                .map_err(|e| e.to_tonic_status())?;
            let out = self
                .service
                .create_refund(CreateRefundInput {
                    payment_id,
                    order_id: req.order_id,
                    amount_vnd: req.amount_vnd,
                    reason: req.reason,
                    idempotency_key: req.idempotency_key,
                    trace_id,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(CreateRefundResponse {
                refund: Some(to_proto_refund(out.refund)),
                already_existed: out.already_existed,
            }))
        }
        .await;

        self.record_metrics("CreateRefund", start, &result);
        result
    }

    async fn get_ledger_summary(
        &self,
        request: Request<GetLedgerSummaryRequest>,
    ) -> Result<Response<GetLedgerSummaryResponse>, Status> {
        let start = Instant::now();
        let caller = CallerAuth::from_request(&self.auth, &request, "GetLedgerSummary");

        let result = async {
            let caller = caller?;
            caller.require_admin("GetLedgerSummary")?;

            let summary = self
                .service
                .get_ledger_summary()
                .await
                .map_err(|e| e.to_tonic_status())?;

            let accounts = summary
                .accounts
                .into_iter()
                .map(|a| AccountBalance {
                    account_code: a.account_id,
                    account_type: a.account_type,
                    debit_total_vnd: a.total_debits_vnd,
                    credit_total_vnd: a.total_credits_vnd,
                    balance_vnd: a.balance_vnd,
                })
                .collect();

            Ok(Response::new(GetLedgerSummaryResponse {
                total_debits_vnd: summary.total_debits_vnd,
                total_credits_vnd: summary.total_credits_vnd,
                is_balanced: summary.is_balanced,
                accounts,
            }))
        }
        .await;

        self.record_metrics("GetLedgerSummary", start, &result);
        result
    }

    async fn get_seller_balance(
        &self,
        request: Request<GetSellerBalanceRequest>,
    ) -> Result<Response<GetSellerBalanceResponse>, Status> {
        let start = Instant::now();
        let caller = CallerAuth::from_request(&self.auth, &request, "GetSellerBalance");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_seller_shop_access(req.shop_id.trim())?;

            let bal = self
                .service
                .get_seller_balance(&req.shop_id)
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(GetSellerBalanceResponse {
                shop_id: bal.shop_id,
                pending_vnd: bal.pending_vnd,
                available_vnd: bal.available_vnd,
                total_earned_vnd: bal.total_earned_vnd,
            }))
        }
        .await;

        self.record_metrics("GetSellerBalance", start, &result);
        result
    }

    async fn release_escrow(
        &self,
        request: Request<ReleaseEscrowRequest>,
    ) -> Result<Response<ReleaseEscrowResponse>, Status> {
        let start = Instant::now();
        let caller = CallerAuth::from_request(&self.auth, &request, "ReleaseEscrow");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_admin("ReleaseEscrow")?;
            let res = self
                .service
                .release_escrow(&req.order_id, non_empty(&req.shop_id))
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(ReleaseEscrowResponse {
                released: res.released,
                released_amount_vnd: res.released_amount_vnd,
                already_released: res.already_released,
            }))
        }
        .await;

        self.record_metrics("ReleaseEscrow", start, &result);
        result
    }

    async fn run_reconciliation(
        &self,
        request: Request<RunReconciliationRequest>,
    ) -> Result<Response<RunReconciliationResponse>, Status> {
        let start = Instant::now();
        let caller = CallerAuth::from_request(&self.auth, &request, "RunReconciliation");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_admin("RunReconciliation")?;

            let run_date = if req.date.trim().is_empty() {
                Utc::now().date_naive()
            } else {
                NaiveDate::parse_from_str(req.date.trim(), "%Y-%m-%d")
                    .map_err(|_| Status::invalid_argument("date must be formatted as YYYY-MM-DD"))?
            };

            let report = self
                .service
                .run_reconciliation(run_date)
                .await
                .map_err(|e| e.to_tonic_status())?;

            let items = report
                .items
                .into_iter()
                .map(to_proto_reconciliation_item)
                .collect();

            Ok(Response::new(RunReconciliationResponse {
                run_id: report.run.id.to_string(),
                matched_count: report.run.matched_count,
                missing_local_count: report.run.missing_local_count,
                missing_provider_count: report.run.missing_provider_count,
                amount_mismatch_count: report.run.amount_mismatch_count,
                items,
            }))
        }
        .await;

        self.record_metrics("RunReconciliation", start, &result);
        result
    }
}

// =============================================================================
// Protobuf Conversion Helpers
// =============================================================================

fn extract_trace_from_metadata<T>(request: &Request<T>) -> String {
    let md = request.metadata();
    let traceparent = md.get("traceparent").and_then(|v| v.to_str().ok());
    let x_trace_id = md.get("x-trace-id").and_then(|v| v.to_str().ok());
    extract_or_generate_trace_id(traceparent, x_trace_id)
}

fn non_empty(s: &str) -> Option<&str> {
    let trimmed = s.trim();
    if trimmed.is_empty() {
        None
    } else {
        Some(trimmed)
    }
}

fn parse_optional_uuid(raw: &str, field: &str) -> crate::error::Result<Option<Uuid>> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Ok(None);
    }
    Uuid::parse_str(trimmed)
        .map(Some)
        .map_err(|_| crate::error::PaymentError::Validation(format!("invalid UUID for {field}")))
}

fn chrono_to_proto(dt: DateTime<Utc>) -> Timestamp {
    Timestamp {
        seconds: dt.timestamp(),
        nanos: dt.timestamp_subsec_nanos() as i32,
    }
}

fn proto_to_chrono(ts: Timestamp) -> Option<DateTime<Utc>> {
    DateTime::from_timestamp(ts.seconds, ts.nanos.max(0) as u32)
}

fn to_proto_payment(pwa: PaymentWithAllocations) -> Payment {
    let p = pwa.payment;
    let method = match p.method.as_str() {
        "COD" => PaymentMethod::Cod as i32,
        "VIETQR" => PaymentMethod::Vietqr as i32,
        _ => PaymentMethod::Unspecified as i32,
    };
    let status = match p.status.as_str() {
        "pending" => PaymentStatus::Pending as i32,
        "succeeded" => PaymentStatus::Succeeded as i32,
        "failed" => PaymentStatus::Failed as i32,
        "expired" => PaymentStatus::Expired as i32,
        "cancelled" | "superseded" => PaymentStatus::Cancelled as i32,
        "refunded" => PaymentStatus::Refunded as i32,
        _ => PaymentStatus::Unspecified as i32,
    };
    let provider_ref = p
        .provider_tx_id
        .clone()
        .unwrap_or_else(|| p.provider_order_code.clone());

    let shop_allocations = pwa
        .allocations
        .into_iter()
        .map(|a| ShopAllocation {
            order_id: a.order_id,
            shop_id: a.shop_id,
            subtotal_vnd: a.subtotal_vnd,
            commission_vnd: a.commission_vnd,
            shipping_fee_vnd: a.shipping_fee_vnd,
        })
        .collect();

    Payment {
        payment_id: p.id.to_string(),
        order_group_id: p.order_group_id,
        order_id: p.order_id,
        user_id: p.user_id,
        amount_vnd: p.amount_vnd,
        method,
        status,
        qr_url: p.qr_url.unwrap_or_default(),
        qr_code: p.qr_code.unwrap_or_default(),
        provider: p.provider,
        provider_ref,
        shop_allocations,
        expires_at: p.expires_at.map(chrono_to_proto),
        created_at: Some(chrono_to_proto(p.created_at)),
        updated_at: Some(chrono_to_proto(p.updated_at)),
    }
}

fn to_proto_refund(r: RefundRecord) -> Refund {
    let status = match r.status.as_str() {
        "pending" => RefundStatus::Pending as i32,
        "completed" | "done" => RefundStatus::Done as i32,
        "failed" => RefundStatus::Failed as i32,
        _ => RefundStatus::Pending as i32,
    };

    Refund {
        refund_id: r.id.to_string(),
        payment_id: r.payment_id.to_string(),
        order_id: r.order_id,
        amount_vnd: r.amount_vnd,
        status,
        reason: r.reason,
        idempotency_key: r.idempotency_key,
        created_at: Some(chrono_to_proto(r.created_at)),
    }
}

fn to_proto_reconciliation_item(item: ReconciliationItemRecord) -> ReconciliationItem {
    let status = match item.classification.as_str() {
        "matched" => ReconciliationStatus::Matched as i32,
        "missing_local" => ReconciliationStatus::MissingLocal as i32,
        "missing_provider" => ReconciliationStatus::MissingProvider as i32,
        "amount_mismatch" => ReconciliationStatus::AmountMismatch as i32,
        _ => ReconciliationStatus::Unspecified as i32,
    };

    ReconciliationItem {
        payment_id: item.payment_id.map(|u| u.to_string()).unwrap_or_default(),
        provider_tx_id: item.provider_tx_id.unwrap_or_default(),
        local_amount_vnd: item.local_amount_vnd.unwrap_or(0),
        provider_amount_vnd: item.provider_amount_vnd.unwrap_or(0),
        status,
        note: item.details.unwrap_or_default(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::Config;
    use crate::metrics::Metrics;
    use sqlx::postgres::PgPoolOptions;
    use tonic::metadata::MetadataValue;

    const TEST_USER_KEY: &str = "grpc-test-user-key";

    fn test_grpc_server() -> (PaymentGrpcServer, Metrics) {
        let cfg = Config::from_env();
        let pool = PgPoolOptions::new()
            .connect_lazy(&cfg.database_url)
            .expect("lazy pool");
        let metrics = Metrics::new();
        let svc = PaymentService::new(pool, cfg, metrics.clone());
        let auth = GrpcAuth::new(TEST_USER_KEY, "grpc-test-service-key").unwrap();
        (PaymentGrpcServer::new(svc, auth), metrics)
    }

    /// Request carrying a valid end-user JWT (the only identity the server trusts).
    fn with_auth<T>(payload: T, role: &str, user_id: &str, shop_id: &str) -> Request<T> {
        let tok = crate::auth::sign_hs256(
            TEST_USER_KEY,
            serde_json::json!({
                "sub": user_id, "role": role, "shop_id": shop_id,
                "exp": Utc::now().timestamp() + 60,
            }),
        );
        let mut req = Request::new(payload);
        req.metadata_mut().insert(
            "authorization",
            MetadataValue::try_from(format!("Bearer {tok}")).unwrap(),
        );
        req
    }

    #[test]
    fn test_proto_timestamp_roundtrip() {
        let now = Utc::now();
        let ts = chrono_to_proto(now);
        let back = proto_to_chrono(ts).unwrap();
        assert_eq!(now.timestamp(), back.timestamp());
    }

    #[test]
    fn test_parse_optional_uuid_validation() {
        assert!(parse_optional_uuid("", "payment_id").unwrap().is_none());
        let id = Uuid::new_v4();
        assert_eq!(
            parse_optional_uuid(&id.to_string(), "payment_id").unwrap(),
            Some(id)
        );
        assert!(parse_optional_uuid("not-a-uuid", "payment_id").is_err());
    }

    #[tokio::test]
    async fn test_grpc_validation_and_red_metrics_recorded() {
        let (server, metrics) = test_grpc_server();

        let err = server
            .create_payment(with_auth(
                CreatePaymentRequest {
                    order_group_id: "grp-1".into(),
                    order_id: "ord-1".into(),
                    user_id: "u-1".into(),
                    amount_vnd: 100_000,
                    method: PaymentMethod::Unspecified as i32,
                    shop_allocations: vec![],
                    expires_at: None,
                    idempotency_key: String::new(),
                    supersede_previous: false,
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        let err_date = server
            .run_reconciliation(with_auth(
                RunReconciliationRequest {
                    date: "20-11-2026".into(),
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err_date.code(), tonic::Code::InvalidArgument);

        let err_uuid = server
            .get_payment(with_auth(
                GetPaymentRequest {
                    payment_id: "invalid-uuid".into(),
                    order_group_id: String::new(),
                    order_id: String::new(),
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err_uuid.code(), tonic::Code::InvalidArgument);

        let prom = metrics.render_prometheus();
        assert!(prom.contains("grpc_requests_total"));
        assert!(prom.contains("grpc_errors_total"));
        assert!(prom.contains("grpc_request_duration_seconds"));
        assert!(prom.contains("method=\"CreatePayment\""));
    }

    #[tokio::test]
    async fn test_grpc_defense_in_depth_rbac_enforcement() {
        let (server, _) = test_grpc_server();

        // Buyer cannot read global ledger summary
        let err = server
            .get_ledger_summary(with_auth(GetLedgerSummaryRequest {}, "buyer", "u-1", ""))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Seller cannot trigger refund
        let err = server
            .create_refund(with_auth(
                CreateRefundRequest {
                    payment_id: String::new(),
                    order_id: "ord-1".into(),
                    amount_vnd: 50_000,
                    reason: "test".into(),
                    idempotency_key: "k1".into(),
                },
                "seller",
                "u-2",
                "shop-1",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Buyer cannot trigger reconciliation
        let err = server
            .run_reconciliation(with_auth(
                RunReconciliationRequest {
                    date: "2026-11-20".into(),
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Seller cannot query ANOTHER shop's balance
        let err = server
            .get_seller_balance(with_auth(
                GetSellerBalanceRequest {
                    shop_id: "shop-other".into(),
                },
                "seller",
                "u-2",
                "shop-mine",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Buyer cannot query any seller balance
        let err = server
            .get_seller_balance(with_auth(
                GetSellerBalanceRequest {
                    shop_id: "shop-1".into(),
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Buyer cannot create payment for a different user_id
        let err = server
            .create_payment(with_auth(
                CreatePaymentRequest {
                    order_group_id: "grp-1".into(),
                    order_id: "ord-1".into(),
                    user_id: "u-victim".into(),
                    amount_vnd: 100_000,
                    method: PaymentMethod::Cod as i32,
                    shop_allocations: vec![],
                    expires_at: None,
                    idempotency_key: String::new(),
                    supersede_previous: false,
                },
                "buyer",
                "u-attacker",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Buyer token without a subject is not a valid identity
        let err = server
            .cancel_payment(with_auth(
                CancelPaymentRequest {
                    payment_id: String::new(),
                    order_group_id: "grp-1".into(),
                    reason: "cancel".into(),
                },
                "buyer",
                "",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::Unauthenticated);

        // Buyer cannot release escrow
        let err = server
            .release_escrow(with_auth(
                ReleaseEscrowRequest {
                    order_id: "ord-1".into(),
                    shop_id: "shop-1".into(),
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);
    }

    #[tokio::test]
    async fn test_grpc_spoofed_metadata_without_jwt_is_unauthenticated() {
        let (server, _) = test_grpc_server();
        let mut req = Request::new(GetLedgerSummaryRequest {});
        for (k, v) in [
            ("x-user-id", "attacker"),
            ("x-user-role", "admin"),
            ("x-shop-id", "shop-b"),
        ] {
            req.metadata_mut()
                .insert(k, MetadataValue::try_from(v).unwrap());
        }
        let err = server.get_ledger_summary(req).await.unwrap_err();
        assert_eq!(err.code(), tonic::Code::Unauthenticated);

        let err = server
            .release_escrow(Request::new(ReleaseEscrowRequest {
                order_id: "ord-1".into(),
                shop_id: "shop-1".into(),
            }))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::Unauthenticated);
    }
}
