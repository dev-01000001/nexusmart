use std::time::Instant;

use chrono::{DateTime, Utc};
use prost_types::Timestamp;
use tonic::{Request, Response, Status};
use tracing::{error, info, warn, Instrument};
use uuid::Uuid;

use crate::auth::{
    require_payment_read_access, resolve_payment_read_scope, seller_scoped_amount_vnd,
    seller_scoped_order_id, Caller, GrpcAuth, PaymentViewScope, CAP_ESCROW_RELEASE,
    CAP_FEES_PAYMENT_READ, CAP_FEES_PAYMENT_WRITE, CAP_LEDGER_READ, CAP_PAYMENT_METHODS_READ,
    CAP_PAYMENT_METHODS_WRITE, CAP_RECONCILIATION_WRITE, CAP_REFUNDS_APPROVE, CAP_REFUNDS_CREATE,
    CAP_REFUNDS_READ, CAP_REFUNDS_SETTLE,
};
use crate::domain::{
    normalize_cod_carrier_code, parse_and_validate_reconciliation_date,
    parse_reconciliation_run_type_filter, parse_seller_ledger_account_filter,
    validate_cod_reconciliation_rows, validate_user_refund_idempotency_key,
    CodReconciliationRowInput, CreatePaymentInput, CreateRefundInput, DecideRefundApprovalInput,
    GetFeeConfigFilter, ImportCodReconciliationInput, ListReconciliationRunsFilter,
    ListRefundsFilter, ListSellerLedgerFilter, PaymentFeeConfigRecord,
    PaymentMethod as DomainMethod, PaymentMethodConfigRecord, PaymentWithAllocations,
    ReconciliationActionKind, ReconciliationItemRecord, ReconciliationQueueItem as DomainQueueItem,
    ReconciliationRunRecord, RefundRecord, ResolveReconciliationInput, SellerLedgerEntryRecord,
    SetFeeConfigInput, SettleRefundInput, ShopAllocationInput, SubmitRefundBankAccountInput,
    UpdatePaymentMethodConfigInput,
};
use crate::service::PaymentService;
use crate::telemetry::{attach_parent_context, extract_context_from_grpc};

#[allow(clippy::result_large_err)]
pub mod pb {
    tonic::include_proto!("nexusmart.payment.v1");
}

use pb::payment_service_server::PaymentService as PaymentServiceTrait;
use pb::{
    AccountBalance, CancelPaymentRequest, CancelPaymentResponse, CreatePaymentRequest,
    CreatePaymentResponse, CreateRefundRequest, CreateRefundResponse, DecideRefundApprovalRequest,
    DecideRefundApprovalResponse, GetFeeConfigRequest, GetFeeConfigResponse,
    GetLedgerSummaryRequest, GetLedgerSummaryResponse, GetPaymentMethodConfigsRequest,
    GetPaymentMethodConfigsResponse, GetPaymentRequest, GetPaymentResponse,
    GetSellerBalanceRequest, GetSellerBalanceResponse, ImportCodReconciliationRequest,
    ImportCodReconciliationResponse, ListReconciliationQueueRequest,
    ListReconciliationQueueResponse, ListReconciliationRunsRequest, ListReconciliationRunsResponse,
    ListRefundsRequest, ListRefundsResponse, ListSellerLedgerRequest, ListSellerLedgerResponse,
    Payment, PaymentFeeConfigItem, PaymentMethod, PaymentMethodConfig, PaymentStatus,
    ReconciliationAction, ReconciliationItem, ReconciliationQueueItem, ReconciliationRunSummary,
    ReconciliationStatus, Refund, RefundStatus, ReleaseEscrowRequest, ReleaseEscrowResponse,
    ResolveReconciliationItemRequest, ResolveReconciliationItemResponse, RunReconciliationRequest,
    RunReconciliationResponse, SellerLedgerEntry, SetFeeConfigRequest, SetFeeConfigResponse,
    SettleRefundRequest, SettleRefundResponse, ShopAllocation, SubmitRefundBankAccountRequest,
    SubmitRefundBankAccountResponse, UpdatePaymentMethodConfigRequest,
    UpdatePaymentMethodConfigResponse,
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

    fn has_capability(&self, capability: &str) -> bool {
        self.caller.has_capability(capability)
    }

    #[allow(clippy::result_large_err)]
    fn require_capability(&self, capability: &str, op: &str) -> Result<(), Status> {
        if !self.has_capability(capability) {
            return Err(Status::permission_denied(format!(
                "forbidden: {op} requires capability '{capability}'"
            )));
        }
        Ok(())
    }

    #[allow(clippy::result_large_err)]
    fn require_capability_or_service(&self, capability: &str, op: &str) -> Result<(), Status> {
        if !self.caller.is_service && !self.has_capability(capability) {
            return Err(Status::permission_denied(format!(
                "forbidden: {op} requires capability '{capability}' or service identity"
            )));
        }
        Ok(())
    }

    #[allow(clippy::result_large_err)]
    fn require_payment_read_access(&self, pwa: &PaymentWithAllocations) -> Result<(), Status> {
        require_payment_read_access(&self.caller, pwa).map_err(|e| e.to_tonic_status())
    }

    /// Payment lifecycle ownership guard for non-§4.11 RPCs (`CreatePayment`, `CancelPayment`):
    /// strictly allows owner, verified service token (`order-service`), or `admin`/`super_admin`
    /// (denies narrower staff roles `cskh`/`accountant`/`ops`/`moderator` on another user's payment) — p4 Item L.
    #[allow(clippy::result_large_err)]
    fn require_payment_owner(&self, owner_user_id: &str, op: &str) -> Result<(), Status> {
        if self.caller.is_service
            || self.caller.is_admin_role()
            || (!self.caller.user_id.is_empty() && owner_user_id == self.caller.user_id)
        {
            return Ok(());
        }
        Err(Status::permission_denied(format!(
            "forbidden: {op} denied for another user's payment"
        )))
    }

    #[allow(clippy::result_large_err)]
    fn require_seller_shop_access(&self, target_shop_id: &str) -> Result<(), Status> {
        if self.has_capability(CAP_LEDGER_READ) {
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

    fn finish_rpc<T>(
        &self,
        span: &tracing::Span,
        method: &str,
        start: Instant,
        trace_id: &str,
        res: &Result<Response<T>, Status>,
    ) {
        let elapsed = start.elapsed().as_secs_f64();
        let duration_ms = (elapsed * 1000.0 * 1000.0).round() / 1000.0;
        match res {
            Ok(_) => {
                self.service
                    .metrics()
                    .record_grpc(method, "OK", false, elapsed);
                span.in_scope(|| {
                    info!(
                        service = "payment",
                        method = method,
                        code = "OK",
                        duration_ms = duration_ms,
                        trace_id = %trace_id,
                        "grpc request"
                    );
                });
            }
            Err(status) => {
                let code = format!("{:?}", status.code());
                self.service
                    .metrics()
                    .record_grpc(method, &code, true, elapsed);
                span.in_scope(|| {
                    if matches!(
                        status.code(),
                        tonic::Code::Internal
                            | tonic::Code::Unknown
                            | tonic::Code::DataLoss
                            | tonic::Code::Unavailable
                            | tonic::Code::DeadlineExceeded
                            | tonic::Code::Unimplemented
                    ) {
                        error!(
                            service = "payment",
                            method = method,
                            code = %code,
                            duration_ms = duration_ms,
                            trace_id = %trace_id,
                            "grpc request"
                        );
                    } else {
                        warn!(
                            service = "payment",
                            method = method,
                            code = %code,
                            duration_ms = duration_ms,
                            trace_id = %trace_id,
                            "grpc request"
                        );
                    }
                });
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
        let (span, trace_id) = start_rpc_span("CreatePayment", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "CreatePayment");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_payment_owner(&req.user_id, "CreatePayment")?;
            if caller.caller.is_service {
                validate_service_create_payment(&req)?;
            }

            let method =
                match PaymentMethod::try_from(req.method).unwrap_or(PaymentMethod::Unspecified) {
                    PaymentMethod::Cod => DomainMethod::Cod,
                    PaymentMethod::Vietqr => DomainMethod::VietQr,
                    PaymentMethod::Zalopay => DomainMethod::ZaloPay,
                    PaymentMethod::Unspecified => {
                        return Err(Status::invalid_argument(
                            "method must be PAYMENT_METHOD_COD, PAYMENT_METHOD_VIETQR, or PAYMENT_METHOD_ZALOPAY",
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
                    platform_discount_vnd: a.platform_discount_vnd,
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
                    trace_id: trace_id.clone(),
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(CreatePaymentResponse {
                payment: Some(to_proto_payment_for_caller(created, &caller.caller)),
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "CreatePayment", start, &trace_id, &result);
        result
    }

    async fn get_payment(
        &self,
        request: Request<GetPaymentRequest>,
    ) -> Result<Response<GetPaymentResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("GetPayment", &request);
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
                payment: Some(to_proto_payment_for_caller(found, &caller.caller)),
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "GetPayment", start, &trace_id, &result);
        result
    }

    async fn cancel_payment(
        &self,
        request: Request<CancelPaymentRequest>,
    ) -> Result<Response<CancelPaymentResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("CancelPayment", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "CancelPayment");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            let payment_id = parse_optional_uuid(&req.payment_id, "payment_id")
                .map_err(|e| e.to_tonic_status())?;
            let order_group_id = non_empty(&req.order_group_id);

            if !caller.caller.is_service && !caller.caller.is_admin_role() {
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
                payment: Some(to_proto_payment_for_caller(cancelled, &caller.caller)),
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "CancelPayment", start, &trace_id, &result);
        result
    }

    async fn create_refund(
        &self,
        request: Request<CreateRefundRequest>,
    ) -> Result<Response<CreateRefundResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("CreateRefund", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "CreateRefund");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability_or_service(CAP_REFUNDS_CREATE, "CreateRefund")?;

            // F11 / p4 Item E: End-user callers cannot supply reserved internal idempotency prefixes.
            if !caller.caller.is_service {
                validate_user_refund_idempotency_key(&req.idempotency_key)
                    .map_err(|e| e.to_tonic_status())?;
            }

            // F11: End-user JWT always applies large-refund approval policy with maker = JWT sub.
            // Service token ONLY honors `requested_by` when `require_approval_if_large == true`;
            // otherwise ignores `requested_by` and bypasses approval (automatic saga cancel).
            let (require_approval_if_large, requested_by) = if caller.caller.is_service {
                if req.require_approval_if_large {
                    let maker = non_empty(&req.requested_by).ok_or_else(|| {
                        Status::invalid_argument(
                            "requested_by is required when require_approval_if_large is true",
                        )
                    })?;
                    (true, Some(maker.to_string()))
                } else {
                    (false, None)
                }
            } else {
                (true, Some(caller.caller.user_id.clone()))
            };

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
                    trace_id: trace_id.clone(),
                    require_approval_if_large,
                    requested_by,
                    created_by_service: caller.caller.is_service,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            let include_full_bank =
                caller.caller.is_service || caller.has_capability(CAP_REFUNDS_READ);
            Ok(Response::new(CreateRefundResponse {
                refund: Some(to_proto_refund(out.refund, include_full_bank)),
                already_existed: out.already_existed,
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "CreateRefund", start, &trace_id, &result);
        result
    }

    async fn get_ledger_summary(
        &self,
        request: Request<GetLedgerSummaryRequest>,
    ) -> Result<Response<GetLedgerSummaryResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("GetLedgerSummary", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "GetLedgerSummary");

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_LEDGER_READ, "GetLedgerSummary")?;

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
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "GetLedgerSummary", start, &trace_id, &result);
        result
    }

    async fn get_seller_balance(
        &self,
        request: Request<GetSellerBalanceRequest>,
    ) -> Result<Response<GetSellerBalanceResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("GetSellerBalance", &request);
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
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "GetSellerBalance", start, &trace_id, &result);
        result
    }

    async fn release_escrow(
        &self,
        request: Request<ReleaseEscrowRequest>,
    ) -> Result<Response<ReleaseEscrowResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("ReleaseEscrow", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "ReleaseEscrow");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_ESCROW_RELEASE, "ReleaseEscrow")?;
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
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "ReleaseEscrow", start, &trace_id, &result);
        result
    }

    async fn run_reconciliation(
        &self,
        request: Request<RunReconciliationRequest>,
    ) -> Result<Response<RunReconciliationResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("RunReconciliation", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "RunReconciliation");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_RECONCILIATION_WRITE, "RunReconciliation")?;

            let run_date = parse_and_validate_reconciliation_date(&req.date, Utc::now())
                .map_err(|e| e.to_tonic_status())?;

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
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "RunReconciliation", start, &trace_id, &result);
        result
    }

    async fn list_refunds(
        &self,
        request: Request<ListRefundsRequest>,
    ) -> Result<Response<ListRefundsResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("ListRefunds", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "ListRefunds");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            let status_filter =
                match RefundStatus::try_from(req.status).unwrap_or(RefundStatus::Unspecified) {
                    RefundStatus::Unspecified => None,
                    RefundStatus::Pending => Some("pending".to_string()),
                    RefundStatus::Done => Some("completed".to_string()),
                    RefundStatus::Failed => Some("failed".to_string()),
                    RefundStatus::PendingApproval => Some("pending_approval".to_string()),
                    RefundStatus::Rejected => Some("rejected".to_string()),
                    RefundStatus::ManualReview => Some("manual_review".to_string()),
                };

            // Staff with `refunds.read` see all matching refunds + full bank details.
            // All other authenticated users (buyer, seller, or staff without `refunds.read` such
            // as `moderator`) are strictly owner-scoped to their own payments in `list_refunds`
            // (`is_admin = false`, masked bank details, 403 on another user's `order_id`).
            let include_full_bank = caller.has_capability(CAP_REFUNDS_READ);
            let out = self
                .service
                .list_refunds(ListRefundsFilter {
                    caller_user_id: non_empty(&caller.caller.user_id).map(ToString::to_string),
                    is_admin: include_full_bank,
                    status: status_filter,
                    order_id: non_empty(&req.order_id).map(ToString::to_string),
                    page: req.page,
                    page_size: req.page_size,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            let refunds = out
                .refunds
                .into_iter()
                .map(|r| to_proto_refund(r, include_full_bank))
                .collect();

            Ok(Response::new(ListRefundsResponse {
                refunds,
                total: out.total,
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "ListRefunds", start, &trace_id, &result);
        result
    }

    async fn submit_refund_bank_account(
        &self,
        request: Request<SubmitRefundBankAccountRequest>,
    ) -> Result<Response<SubmitRefundBankAccountResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("SubmitRefundBankAccount", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "SubmitRefundBankAccount");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            let refund_id = parse_required_uuid(&req.refund_id, "refund_id")
                .map_err(|e| e.to_tonic_status())?;

            let updated = self
                .service
                .submit_refund_bank_account(SubmitRefundBankAccountInput {
                    refund_id,
                    caller_user_id: caller.caller.user_id,
                    bank_code: req.bank_code,
                    bank_account_number: req.bank_account_number,
                    bank_account_name: req.bank_account_name,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            // Always mask full bank account number in buyer response.
            Ok(Response::new(SubmitRefundBankAccountResponse {
                refund: Some(to_proto_refund(updated, false)),
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "SubmitRefundBankAccount", start, &trace_id, &result);
        result
    }

    async fn settle_refund(
        &self,
        request: Request<SettleRefundRequest>,
    ) -> Result<Response<SettleRefundResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("SettleRefund", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "SettleRefund");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_REFUNDS_SETTLE, "SettleRefund")?;

            let refund_id = parse_required_uuid(&req.refund_id, "refund_id")
                .map_err(|e| e.to_tonic_status())?;

            let out = self
                .service
                .settle_refund(SettleRefundInput {
                    refund_id,
                    provider_refund_id: req.provider_refund_id,
                    note: req.note,
                    idempotency_key: req.idempotency_key,
                    trace_id: trace_id.clone(),
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(SettleRefundResponse {
                refund: Some(to_proto_refund(out.refund, true)),
                already_settled: out.already_settled,
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "SettleRefund", start, &trace_id, &result);
        result
    }

    async fn list_reconciliation_queue(
        &self,
        request: Request<ListReconciliationQueueRequest>,
    ) -> Result<Response<ListReconciliationQueueResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("ListReconciliationQueue", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "ListReconciliationQueue");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_LEDGER_READ, "ListReconciliationQueue")?;

            let out = self
                .service
                .list_reconciliation_queue(non_empty(&req.status), req.page, req.page_size)
                .await
                .map_err(|e| e.to_tonic_status())?;

            let items = out
                .items
                .into_iter()
                .map(to_proto_reconciliation_queue_item)
                .collect();

            Ok(Response::new(ListReconciliationQueueResponse {
                items,
                total: out.total,
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "ListReconciliationQueue", start, &trace_id, &result);
        result
    }

    async fn resolve_reconciliation_item(
        &self,
        request: Request<ResolveReconciliationItemRequest>,
    ) -> Result<Response<ResolveReconciliationItemResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("ResolveReconciliationItem", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "ResolveReconciliationItem");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_RECONCILIATION_WRITE, "ResolveReconciliationItem")?;

            let item_id =
                parse_required_uuid(&req.item_id, "item_id").map_err(|e| e.to_tonic_status())?;
            let payment_id = parse_optional_uuid(&req.payment_id, "payment_id")
                .map_err(|e| e.to_tonic_status())?;
            let action = match ReconciliationAction::try_from(req.action)
                .unwrap_or(ReconciliationAction::Unspecified)
            {
                ReconciliationAction::Assign => ReconciliationActionKind::Assign,
                ReconciliationAction::Refund => ReconciliationActionKind::Refund,
                ReconciliationAction::Suspend => ReconciliationActionKind::Suspend,
                ReconciliationAction::Unspecified => {
                    return Err(Status::invalid_argument(
                        "action must be ASSIGN, REFUND, or SUSPEND",
                    ));
                }
            };

            let out = self
                .service
                .resolve_reconciliation_item(ResolveReconciliationInput {
                    item_id,
                    action,
                    payment_id,
                    note: req.note,
                    idempotency_key: req.idempotency_key,
                    trace_id: trace_id.clone(),
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(ResolveReconciliationItemResponse {
                item: Some(to_proto_reconciliation_queue_item(out.item)),
                refund: out.refund.map(|r| to_proto_refund(r, true)),
                already_resolved: out.already_resolved,
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(
            &span,
            "ResolveReconciliationItem",
            start,
            &trace_id,
            &result,
        );
        result
    }

    async fn get_fee_config(
        &self,
        request: Request<GetFeeConfigRequest>,
    ) -> Result<Response<GetFeeConfigResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("GetFeeConfig", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "GetFeeConfig");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability_or_service(CAP_FEES_PAYMENT_READ, "GetFeeConfig")?;

            let method = match PaymentMethod::try_from(req.payment_method) {
                Ok(PaymentMethod::Unspecified) => None,
                Ok(PaymentMethod::Cod) => Some(DomainMethod::Cod),
                Ok(PaymentMethod::Vietqr) => Some(DomainMethod::VietQr),
                Ok(PaymentMethod::Zalopay) => Some(DomainMethod::ZaloPay),
                Err(_) => {
                    return Err(Status::invalid_argument(
                        "invalid payment_method; expected UNSPECIFIED, COD, VIETQR, or ZALOPAY",
                    ));
                }
            };

            let at_time = parse_optional_proto_timestamp(req.at_time, "at_time")
                .map_err(|e| e.to_tonic_status())?;

            let out = self
                .service
                .get_fee_config(GetFeeConfigFilter {
                    method,
                    include_history: req.include_history,
                    at_time,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(GetFeeConfigResponse {
                active_configs: out
                    .active_configs
                    .into_iter()
                    .map(to_proto_fee_config_item)
                    .collect(),
                history: out
                    .history
                    .into_iter()
                    .map(to_proto_fee_config_item)
                    .collect(),
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "GetFeeConfig", start, &trace_id, &result);
        result
    }

    async fn set_fee_config(
        &self,
        request: Request<SetFeeConfigRequest>,
    ) -> Result<Response<SetFeeConfigResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("SetFeeConfig", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "SetFeeConfig");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_FEES_PAYMENT_WRITE, "SetFeeConfig")?;

            let method = match PaymentMethod::try_from(req.payment_method) {
                Ok(PaymentMethod::Cod) => DomainMethod::Cod,
                Ok(PaymentMethod::Vietqr) => DomainMethod::VietQr,
                Ok(PaymentMethod::Zalopay) => DomainMethod::ZaloPay,
                Ok(PaymentMethod::Unspecified) | Err(_) => {
                    return Err(Status::invalid_argument(
                        "payment_method must be PAYMENT_METHOD_COD, PAYMENT_METHOD_VIETQR, or PAYMENT_METHOD_ZALOPAY",
                    ));
                }
            };

            let effective_from =
                parse_optional_proto_timestamp(req.effective_from, "effective_from")
                    .map_err(|e| e.to_tonic_status())?;

            let created = self
                .service
                .set_fee_config(SetFeeConfigInput {
                    method,
                    fee_bps: req.fee_bps,
                    effective_from,
                    created_by: caller.caller.user_id,
                    note: req.note,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(SetFeeConfigResponse {
                config: Some(to_proto_fee_config_item(created)),
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "SetFeeConfig", start, &trace_id, &result);
        result
    }

    async fn list_seller_ledger(
        &self,
        request: Request<ListSellerLedgerRequest>,
    ) -> Result<Response<ListSellerLedgerResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("ListSellerLedger", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "ListSellerLedger");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            let req_shop_id = non_empty(&req.shop_id);

            let target_shop_id = if caller.has_capability(CAP_LEDGER_READ) {
                req_shop_id.map(ToString::to_string)
            } else if caller.caller.role == "seller" && !caller.caller.shop_id.is_empty() {
                if let Some(requested) = req_shop_id {
                    if requested != caller.caller.shop_id {
                        return Err(Status::permission_denied(
                            "forbidden: seller can only access own shop ledger",
                        ));
                    }
                }
                Some(caller.caller.shop_id.clone())
            } else {
                return Err(Status::permission_denied(
                    "forbidden: ListSellerLedger requires seller or ledger.read capability",
                ));
            };

            let account_filter = parse_seller_ledger_account_filter(&req.account_filter)
                .map_err(|e| e.to_tonic_status())?;
            let reference_type = non_empty(&req.reference_type).map(ToString::to_string);

            let out = self
                .service
                .list_seller_ledger(ListSellerLedgerFilter {
                    shop_id: target_shop_id,
                    account_filter,
                    reference_type,
                    page: req.page,
                    page_size: req.page_size,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(ListSellerLedgerResponse {
                entries: out
                    .entries
                    .into_iter()
                    .map(to_proto_seller_ledger_entry)
                    .collect(),
                total: out.total,
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "ListSellerLedger", start, &trace_id, &result);
        result
    }

    async fn import_cod_reconciliation(
        &self,
        request: Request<ImportCodReconciliationRequest>,
    ) -> Result<Response<ImportCodReconciliationResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("ImportCodReconciliation", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "ImportCodReconciliation");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_RECONCILIATION_WRITE, "ImportCodReconciliation")?;

            let carrier_code =
                normalize_cod_carrier_code(&req.carrier_code).map_err(|e| e.to_tonic_status())?;
            let run_date = parse_and_validate_reconciliation_date(&req.run_date, Utc::now())
                .map_err(|e| e.to_tonic_status())?;
            let rows: Vec<CodReconciliationRowInput> = req
                .rows
                .into_iter()
                .map(|r| CodReconciliationRowInput {
                    line_number: r.line_number,
                    tracking_code: r.tracking_code,
                    cod_amount_vnd: r.cod_amount_vnd,
                    delivered_at: r.delivered_at,
                    order_code: r.order_code,
                })
                .collect();
            validate_cod_reconciliation_rows(&rows).map_err(|e| e.to_tonic_status())?;

            let report = self
                .service
                .import_cod_reconciliation(ImportCodReconciliationInput {
                    carrier_code,
                    file_name: req.file_name,
                    file_sha256: req.file_sha256,
                    run_date,
                    created_by: caller.caller.user_id,
                    rows,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(ImportCodReconciliationResponse {
                run: Some(to_proto_reconciliation_run_summary(report.run)),
                items: report
                    .items
                    .into_iter()
                    .map(to_proto_reconciliation_item)
                    .collect(),
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "ImportCodReconciliation", start, &trace_id, &result);
        result
    }

    async fn list_reconciliation_runs(
        &self,
        request: Request<ListReconciliationRunsRequest>,
    ) -> Result<Response<ListReconciliationRunsResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("ListReconciliationRuns", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "ListReconciliationRuns");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_LEDGER_READ, "ListReconciliationRuns")?;

            let run_type = parse_reconciliation_run_type_filter(&req.run_type)
                .map_err(|e| e.to_tonic_status())?;

            let out = self
                .service
                .list_reconciliation_runs(ListReconciliationRunsFilter {
                    run_type,
                    page: req.page,
                    page_size: req.page_size,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(ListReconciliationRunsResponse {
                runs: out
                    .runs
                    .into_iter()
                    .map(to_proto_reconciliation_run_summary)
                    .collect(),
                total: out.total,
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "ListReconciliationRuns", start, &trace_id, &result);
        result
    }

    async fn get_payment_method_configs(
        &self,
        request: Request<GetPaymentMethodConfigsRequest>,
    ) -> Result<Response<GetPaymentMethodConfigsResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("GetPaymentMethodConfigs", &request);
        let caller = self
            .auth
            .authenticate_optional(request.metadata(), "GetPaymentMethodConfigs");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            let include_admin_fields = caller
                .as_ref()
                .is_some_and(|c| c.has_capability(CAP_PAYMENT_METHODS_READ));

            let method_filter = match PaymentMethod::try_from(req.method) {
                Ok(PaymentMethod::Unspecified) => None,
                Ok(PaymentMethod::Cod) => Some(DomainMethod::Cod),
                Ok(PaymentMethod::Vietqr) => Some(DomainMethod::VietQr),
                Ok(PaymentMethod::Zalopay) => Some(DomainMethod::ZaloPay),
                Err(_) => {
                    return Err(Status::invalid_argument(
                        "invalid method; expected UNSPECIFIED, COD, VIETQR, or ZALOPAY",
                    ));
                }
            };

            let rows = self
                .service
                .get_payment_method_configs(method_filter)
                .await
                .map_err(|e| e.to_tonic_status())?;

            let configs = rows
                .into_iter()
                .map(|rec| to_proto_method_config(&self.service, rec, include_admin_fields))
                .collect();

            Ok(Response::new(GetPaymentMethodConfigsResponse { configs }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "GetPaymentMethodConfigs", start, &trace_id, &result);
        result
    }

    async fn update_payment_method_config(
        &self,
        request: Request<UpdatePaymentMethodConfigRequest>,
    ) -> Result<Response<UpdatePaymentMethodConfigResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("UpdatePaymentMethodConfig", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "UpdatePaymentMethodConfig");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_capability(CAP_PAYMENT_METHODS_WRITE, "UpdatePaymentMethodConfig")?;

            let method = match PaymentMethod::try_from(req.method) {
                Ok(PaymentMethod::Cod) => DomainMethod::Cod,
                Ok(PaymentMethod::Vietqr) => DomainMethod::VietQr,
                Ok(PaymentMethod::Zalopay) => DomainMethod::ZaloPay,
                Ok(PaymentMethod::Unspecified) | Err(_) => {
                    return Err(Status::invalid_argument(
                        "method must be PAYMENT_METHOD_COD, PAYMENT_METHOD_VIETQR, or PAYMENT_METHOD_ZALOPAY",
                    ));
                }
            };

            let updated = self
                .service
                .update_payment_method_config(UpdatePaymentMethodConfigInput {
                    method,
                    enabled: req.enabled,
                    min_order_vnd: req.min_order_vnd,
                    max_order_vnd: req.max_order_vnd,
                    expiry_minutes: req.expiry_minutes,
                    note: req.note,
                    expected_version: req.expected_version,
                    updated_by: caller.caller.user_id,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(UpdatePaymentMethodConfigResponse {
                config: Some(to_proto_method_config(&self.service, updated, true)),
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(
            &span,
            "UpdatePaymentMethodConfig",
            start,
            &trace_id,
            &result,
        );
        result
    }

    async fn decide_refund_approval(
        &self,
        request: Request<DecideRefundApprovalRequest>,
    ) -> Result<Response<DecideRefundApprovalResponse>, Status> {
        let start = Instant::now();
        let (span, trace_id) = start_rpc_span("DecideRefundApproval", &request);
        let caller = CallerAuth::from_request(&self.auth, &request, "DecideRefundApproval");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            // F11: Re-check checker's `refunds.approve` capability at decision time.
            caller.require_capability(CAP_REFUNDS_APPROVE, "DecideRefundApproval")?;

            let refund_id = parse_required_uuid(&req.refund_id, "refund_id")
                .map_err(|e| e.to_tonic_status())?;

            let decided = self
                .service
                .decide_refund_approval(DecideRefundApprovalInput {
                    refund_id,
                    approve: req.approve,
                    note: req.note,
                    checker_user_id: caller.caller.user_id,
                    trace_id: trace_id.clone(),
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(DecideRefundApprovalResponse {
                refund: Some(to_proto_refund(decided, true)),
            }))
        }
        .instrument(span.clone())
        .await;

        self.finish_rpc(&span, "DecideRefundApproval", start, &trace_id, &result);
        result
    }
}

// =============================================================================
// Protobuf Conversion Helpers
// =============================================================================

fn start_rpc_span<T>(method: &str, request: &Request<T>) -> (tracing::Span, String) {
    let md = request.metadata();
    let parent_cx = extract_context_from_grpc(md);
    let x_trace_id = md.get("x-trace-id").and_then(|v| v.to_str().ok());
    let span = tracing::info_span!(
        "grpc.request",
        otel.name = method,
        otel.kind = "server",
        rpc.system = "grpc",
        rpc.method = method,
        trace_id = tracing::field::Empty,
    );
    let trace_id = attach_parent_context(&span, parent_cx, x_trace_id);
    span.record("trace_id", trace_id.as_str());
    (span, trace_id)
}

/// Additional invariants enforced when `CreatePayment` is called with an internal service token
/// (used exclusively by `order` watchdog forward recovery):
/// - `supersede_previous = true` is forbidden (`PermissionDenied`; retry-payment is buyer-only);
/// - `user_id` must be non-empty (`InvalidArgument`);
/// - `order_group_id` and `idempotency_key` are required with exact format
///   `idempotency_key == "checkout:" + order_group_id` (`InvalidArgument`).
#[allow(clippy::result_large_err)]
fn validate_service_create_payment(req: &CreatePaymentRequest) -> Result<(), Status> {
    if req.supersede_previous {
        return Err(Status::permission_denied(
            "forbidden: service identity cannot supersede an existing payment",
        ));
    }
    if req.user_id.trim().is_empty() {
        return Err(Status::invalid_argument("user_id is required"));
    }
    let order_group_id = req.order_group_id.trim();
    if order_group_id.is_empty() || order_group_id != req.order_group_id {
        return Err(Status::invalid_argument(
            "order_group_id is required for service CreatePayment",
        ));
    }
    let expected_key = format!("checkout:{order_group_id}");
    if req.idempotency_key.is_empty() || req.idempotency_key != expected_key {
        return Err(Status::invalid_argument(
            "service CreatePayment requires idempotency_key == \"checkout:<order_group_id>\"",
        ));
    }
    Ok(())
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

fn parse_required_uuid(raw: &str, field: &str) -> crate::error::Result<Uuid> {
    parse_optional_uuid(raw, field)?
        .ok_or_else(|| crate::error::PaymentError::Validation(format!("{field} is required")))
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

fn parse_optional_proto_timestamp(
    ts: Option<Timestamp>,
    field: &str,
) -> crate::error::Result<Option<DateTime<Utc>>> {
    match ts {
        None => Ok(None),
        Some(t) => {
            if !(0..1_000_000_000).contains(&t.nanos) {
                return Err(crate::error::PaymentError::Validation(format!(
                    "invalid timestamp nanos for {field}"
                )));
            }
            DateTime::from_timestamp(t.seconds, t.nanos as u32)
                .map(Some)
                .ok_or_else(|| {
                    crate::error::PaymentError::Validation(format!(
                        "out-of-range timestamp for {field}"
                    ))
                })
        }
    }
}

fn to_proto_method_config(
    service: &PaymentService,
    rec: PaymentMethodConfigRecord,
    include_admin_fields: bool,
) -> PaymentMethodConfig {
    let method = match rec.method.as_str() {
        "COD" => PaymentMethod::Cod as i32,
        "VIETQR" => PaymentMethod::Vietqr as i32,
        "ZALOPAY" => PaymentMethod::Zalopay as i32,
        _ => PaymentMethod::Unspecified as i32,
    };
    if include_admin_fields {
        let (credentials_configured, credential_preview) =
            service.method_credentials_info(&rec.method);
        PaymentMethodConfig {
            method,
            enabled: rec.enabled,
            min_order_vnd: rec.min_order_vnd,
            max_order_vnd: rec.max_order_vnd,
            expiry_minutes: rec.expiry_minutes,
            credentials_configured,
            credential_preview,
            updated_by: rec.updated_by,
            updated_at: Some(chrono_to_proto(rec.updated_at)),
            note: rec.note,
            version: rec.version,
        }
    } else {
        // Public / service projection: strip admin/credential metadata per F6/F8.
        PaymentMethodConfig {
            method,
            enabled: rec.enabled,
            min_order_vnd: rec.min_order_vnd,
            max_order_vnd: rec.max_order_vnd,
            expiry_minutes: rec.expiry_minutes,
            credentials_configured: false,
            credential_preview: String::new(),
            updated_by: String::new(),
            updated_at: None,
            note: String::new(),
            version: rec.version,
        }
    }
}

fn to_proto_fee_config_item(rec: PaymentFeeConfigRecord) -> PaymentFeeConfigItem {
    let payment_method = match rec.payment_method.as_str() {
        "COD" => PaymentMethod::Cod as i32,
        "VIETQR" => PaymentMethod::Vietqr as i32,
        "ZALOPAY" => PaymentMethod::Zalopay as i32,
        _ => PaymentMethod::Unspecified as i32,
    };
    PaymentFeeConfigItem {
        id: rec.id.to_string(),
        payment_method,
        fee_bps: rec.fee_bps,
        effective_from: Some(chrono_to_proto(rec.effective_from)),
        created_by: rec.created_by,
        note: rec.note,
        created_at: Some(chrono_to_proto(rec.created_at)),
    }
}

fn to_proto_seller_ledger_entry(rec: SellerLedgerEntryRecord) -> SellerLedgerEntry {
    SellerLedgerEntry {
        id: rec.id.to_string(),
        transaction_id: rec.transaction_id.to_string(),
        account_code: rec.account_code,
        reference_type: rec.reference_type,
        reference_id: rec.reference_id,
        description: rec.description,
        debit_vnd: rec.debit_vnd,
        credit_vnd: rec.credit_vnd,
        delta_vnd: rec.delta_vnd,
        created_at: Some(chrono_to_proto(rec.created_at)),
    }
}

fn to_proto_payment_for_caller(pwa: PaymentWithAllocations, caller: &Caller) -> Payment {
    let scope = resolve_payment_read_scope(caller, &pwa).ok().or_else(|| {
        if !caller.user_id.is_empty() && pwa.payment.user_id == caller.user_id {
            Some(PaymentViewScope::BuyerOwner)
        } else if caller.role == "seller" && !caller.shop_id.is_empty() {
            Some(PaymentViewScope::SellerAllocation {
                shop_id: caller.shop_id.clone(),
            })
        } else {
            None
        }
    });

    let seller_amount = match &scope {
        Some(PaymentViewScope::SellerAllocation { shop_id }) => {
            Some(seller_scoped_amount_vnd(&pwa, shop_id))
        }
        _ => None,
    };
    let seller_order_id = match &scope {
        Some(PaymentViewScope::SellerAllocation { shop_id }) => {
            Some(seller_scoped_order_id(&pwa, shop_id))
        }
        _ => None,
    };

    let p = pwa.payment;
    let method = match p.method.as_str() {
        "COD" => PaymentMethod::Cod as i32,
        "VIETQR" => PaymentMethod::Vietqr as i32,
        "ZALOPAY" => PaymentMethod::Zalopay as i32,
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
    let full_provider_ref = p
        .provider_tx_id
        .clone()
        .unwrap_or_else(|| p.provider_order_code.clone());

    let (amount_vnd, order_id, qr_url, qr_code, pay_url, provider_ref, shop_allocations) =
        match scope {
            Some(PaymentViewScope::Full) => {
                let allocs = pwa
                    .allocations
                    .into_iter()
                    .map(|a| ShopAllocation {
                        order_id: a.order_id,
                        shop_id: a.shop_id,
                        subtotal_vnd: a.subtotal_vnd,
                        commission_vnd: a.commission_vnd,
                        shipping_fee_vnd: a.shipping_fee_vnd,
                        platform_discount_vnd: a.platform_discount_vnd,
                    })
                    .collect();
                (
                    p.amount_vnd,
                    p.order_id,
                    p.qr_url.unwrap_or_default(),
                    p.qr_code.unwrap_or_default(),
                    p.pay_url.unwrap_or_default(),
                    full_provider_ref,
                    allocs,
                )
            }
            Some(PaymentViewScope::BuyerOwner) => {
                let allocs = pwa
                    .allocations
                    .into_iter()
                    .map(|a| ShopAllocation {
                        order_id: a.order_id,
                        shop_id: a.shop_id,
                        subtotal_vnd: a.subtotal_vnd,
                        commission_vnd: 0,
                        shipping_fee_vnd: a.shipping_fee_vnd,
                        platform_discount_vnd: a.platform_discount_vnd,
                    })
                    .collect();
                (
                    p.amount_vnd,
                    p.order_id,
                    p.qr_url.unwrap_or_default(),
                    p.qr_code.unwrap_or_default(),
                    p.pay_url.unwrap_or_default(),
                    full_provider_ref,
                    allocs,
                )
            }
            Some(PaymentViewScope::SellerAllocation { shop_id }) => {
                let allocs = pwa
                    .allocations
                    .into_iter()
                    .filter(|a| a.shop_id == shop_id)
                    .map(|a| ShopAllocation {
                        order_id: a.order_id,
                        shop_id: a.shop_id,
                        subtotal_vnd: a.subtotal_vnd,
                        commission_vnd: a.commission_vnd,
                        shipping_fee_vnd: a.shipping_fee_vnd,
                        platform_discount_vnd: a.platform_discount_vnd,
                    })
                    .collect();
                (
                    seller_amount.unwrap_or(0),
                    seller_order_id.unwrap_or_default(),
                    String::new(),
                    String::new(),
                    String::new(),
                    String::new(),
                    allocs,
                )
            }
            None => (
                0,
                String::new(),
                String::new(),
                String::new(),
                String::new(),
                String::new(),
                Vec::new(),
            ),
        };

    Payment {
        payment_id: p.id.to_string(),
        order_group_id: p.order_group_id,
        order_id,
        user_id: p.user_id,
        amount_vnd,
        method,
        status,
        qr_url,
        qr_code,
        provider: p.provider,
        provider_ref,
        shop_allocations,
        expires_at: p.expires_at.map(chrono_to_proto),
        created_at: Some(chrono_to_proto(p.created_at)),
        updated_at: Some(chrono_to_proto(p.updated_at)),
        pay_url,
    }
}

fn to_proto_refund(r: RefundRecord, include_bank_account_number: bool) -> Refund {
    let status = match r.status.as_str() {
        "pending" => RefundStatus::Pending as i32,
        "completed" | "done" => RefundStatus::Done as i32,
        "failed" => RefundStatus::Failed as i32,
        "pending_approval" => RefundStatus::PendingApproval as i32,
        "rejected" => RefundStatus::Rejected as i32,
        "manual_review" => RefundStatus::ManualReview as i32,
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
        bank_code: r.bank_code.unwrap_or_default(),
        bank_account_masked: r.bank_account_masked.unwrap_or_default(),
        bank_account_name: r.bank_account_name.unwrap_or_default(),
        bank_account_number: if include_bank_account_number {
            r.bank_account_number.unwrap_or_default()
        } else {
            String::new()
        },
        provider_refund_id: r.provider_refund_id.unwrap_or_default(),
        is_late_payment: r.is_late_payment,
        settled_at: r.settled_at.map(chrono_to_proto),
        settlement_note: r.settlement_note.unwrap_or_default(),
        requested_by: r.requested_by.unwrap_or_default(),
        approved_by: r.approved_by.unwrap_or_default(),
        approved_at: r.approved_at.map(chrono_to_proto),
        approval_note: r.approval_note.unwrap_or_default(),
        provider_m_refund_id: r.provider_m_refund_id.unwrap_or_default(),
        provider_status: r.provider_status.unwrap_or_default(),
    }
}

fn to_proto_reconciliation_queue_item(item: DomainQueueItem) -> ReconciliationQueueItem {
    ReconciliationQueueItem {
        id: item.id.to_string(),
        webhook_event_id: item
            .webhook_event_id
            .map(|u| u.to_string())
            .unwrap_or_default(),
        provider: item.provider,
        provider_tx_id: item.provider_tx_id,
        expected_payment_id: item
            .expected_payment_id
            .map(|u| u.to_string())
            .unwrap_or_default(),
        order_ref: item.order_ref.unwrap_or_default(),
        expected_amount_vnd: item.expected_amount_vnd.unwrap_or(0),
        actual_amount_vnd: item.actual_amount_vnd,
        reason: item.reason,
        resolution_status: item.resolution_status,
        notes: item.notes.unwrap_or_default(),
        created_at: Some(chrono_to_proto(item.created_at)),
        resolved_at: item.resolved_at.map(chrono_to_proto),
        source_type: item.source_type,
        tracking_code: item.tracking_code,
    }
}

fn to_proto_reconciliation_run_summary(run: ReconciliationRunRecord) -> ReconciliationRunSummary {
    let run_type = if run.run_type.is_empty() {
        "vietqr".to_string()
    } else {
        run.run_type
    };
    ReconciliationRunSummary {
        run_id: run.id.to_string(),
        run_type,
        provider: run.provider,
        run_date: run.run_date.format("%Y-%m-%d").to_string(),
        file_name: run.file_name,
        total_rows: run.total_rows,
        matched_count: run.matched_count,
        already_reconciled_count: run.already_reconciled_count,
        missing_local_count: run.missing_local_count,
        missing_provider_count: run.missing_provider_count,
        amount_mismatch_count: run.amount_mismatch_count,
        total_matched_vnd: run.total_matched_vnd,
        status: run.status,
        created_by: run.created_by,
        created_at: Some(chrono_to_proto(run.created_at)),
        not_delivered_count: run.not_delivered_count,
    }
}

fn to_proto_reconciliation_item(item: ReconciliationItemRecord) -> ReconciliationItem {
    let status = match item.classification.as_str() {
        "matched" => ReconciliationStatus::Matched as i32,
        "missing_local" => ReconciliationStatus::MissingLocal as i32,
        "missing_provider" => ReconciliationStatus::MissingProvider as i32,
        "amount_mismatch" => ReconciliationStatus::AmountMismatch as i32,
        "already_reconciled" => ReconciliationStatus::AlreadyReconciled as i32,
        "not_delivered" => ReconciliationStatus::NotDelivered as i32,
        _ => ReconciliationStatus::Unspecified as i32,
    };

    ReconciliationItem {
        payment_id: item.payment_id.map(|u| u.to_string()).unwrap_or_default(),
        provider_tx_id: item.provider_tx_id.unwrap_or_default(),
        local_amount_vnd: item.local_amount_vnd.unwrap_or(0),
        provider_amount_vnd: item.provider_amount_vnd.unwrap_or(0),
        status,
        note: item.details.unwrap_or_default(),
        tracking_code: item.tracking_code,
        order_id: item.order_id,
        line_number: item.line_number,
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
        crate::telemetry::init_test_tracing();
        let mut cfg = Config::from_env();
        cfg.database_url = "postgres://unit:unit@127.0.0.1:1/unit_test_db".to_string();
        let pool = PgPoolOptions::new()
            .acquire_timeout(std::time::Duration::from_millis(200))
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
                    require_approval_if_large: false,
                    requested_by: String::new(),
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

    #[tokio::test]
    async fn test_w2_new_rpcs_rbac_and_validation() {
        let (server, _) = test_grpc_server();

        // Future reconciliation date -> InvalidArgument (400)
        let err = server
            .run_reconciliation(with_auth(
                RunReconciliationRequest {
                    date: "2099-12-31".into(),
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        // Buyer cannot call SettleRefund, ListReconciliationQueue, ResolveReconciliationItem
        let err = server
            .settle_refund(with_auth(
                SettleRefundRequest {
                    refund_id: Uuid::new_v4().to_string(),
                    provider_refund_id: "REF-1".into(),
                    note: "manual".into(),
                    idempotency_key: "k-1".into(),
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        let err = server
            .list_reconciliation_queue(with_auth(
                ListReconciliationQueueRequest {
                    status: "pending".into(),
                    page: 1,
                    page_size: 20,
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        let err = server
            .resolve_reconciliation_item(with_auth(
                ResolveReconciliationItemRequest {
                    item_id: Uuid::new_v4().to_string(),
                    action: pb::ReconciliationAction::Suspend as i32,
                    payment_id: String::new(),
                    note: "hold".into(),
                    idempotency_key: "k-2".into(),
                },
                "seller",
                "u-2",
                "shop-1",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // SubmitRefundBankAccount validates bank account digits (6-19) before DB
        let err = server
            .submit_refund_bank_account(with_auth(
                SubmitRefundBankAccountRequest {
                    refund_id: Uuid::new_v4().to_string(),
                    bank_code: "VCB".into(),
                    bank_account_number: "12345".into(), // too short (<6)
                    bank_account_name: "NGUYEN VAN A".into(),
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        // Unauthenticated ListRefunds -> Unauthenticated
        let err = server
            .list_refunds(Request::new(ListRefundsRequest {
                status: 0,
                order_id: String::new(),
                page: 1,
                page_size: 20,
            }))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::Unauthenticated);

        // Service token is rejected on ReleaseEscrow and all new W2a RPCs
        let svc_tok = crate::auth::sign_hs256(
            "grpc-test-service-key",
            serde_json::json!({
                "sub": "order-service",
                "role": "admin",
                "service": "order-service",
                "exp": Utc::now().timestamp() + 60,
            }),
        );
        let mut req_release = Request::new(ReleaseEscrowRequest {
            order_id: "ord-1".into(),
            shop_id: "shop-1".into(),
        });
        req_release.metadata_mut().insert(
            "authorization",
            MetadataValue::try_from(format!("Bearer {svc_tok}")).unwrap(),
        );
        assert_eq!(
            server.release_escrow(req_release).await.unwrap_err().code(),
            tonic::Code::PermissionDenied
        );

        // Admin ResolveReconciliationItem validates action, idempotency_key, suspend note before DB
        let err = server
            .resolve_reconciliation_item(with_auth(
                ResolveReconciliationItemRequest {
                    item_id: Uuid::new_v4().to_string(),
                    action: ReconciliationAction::Unspecified as i32,
                    payment_id: String::new(),
                    note: String::new(),
                    idempotency_key: "k-3".into(),
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        let err = server
            .resolve_reconciliation_item(with_auth(
                ResolveReconciliationItemRequest {
                    item_id: Uuid::new_v4().to_string(),
                    action: ReconciliationAction::Suspend as i32,
                    payment_id: String::new(),
                    note: "   ".into(),
                    idempotency_key: "k-4".into(),
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        // Buyer response masks full bank account number; admin response includes it
        let now = Utc::now();
        let rec = RefundRecord {
            id: Uuid::new_v4(),
            payment_id: Uuid::new_v4(),
            order_id: "ord-1".into(),
            amount_vnd: 100_000,
            reason: "return".into(),
            status: "pending".into(),
            idempotency_key: "rf-1".into(),
            provider_refund_id: None,
            provider_m_refund_id: None,
            provider_status: None,
            provider_lease_token: None,
            provider_lease_until: None,
            provider_dispatch_attempted: false,
            created_by_service: false,
            requested_by: None,
            approved_by: None,
            approved_at: None,
            approval_note: None,
            is_late_payment: false,
            bank_code: Some("VCB".into()),
            bank_account_number: Some("19034567890123".into()),
            bank_account_masked: Some("****0123".into()),
            bank_account_name: Some("NGUYEN VAN A".into()),
            settled_at: None,
            settlement_note: None,
            settlement_idempotency_key: None,
            affects_escrow: true,
            created_at: now,
            updated_at: now,
        };
        let buyer_pb = to_proto_refund(rec.clone(), false);
        assert_eq!(buyer_pb.bank_account_masked, "****0123");
        assert!(buyer_pb.bank_account_number.is_empty());

        let admin_pb = to_proto_refund(rec, true);
        assert_eq!(admin_pb.bank_account_masked, "****0123");
        assert_eq!(admin_pb.bank_account_number, "19034567890123");
    }

    #[tokio::test]
    async fn test_grpc_rpc_extracts_w3c_traceparent_into_child_otel_span() {
        crate::telemetry::init_test_tracing();

        use std::sync::{Arc, Mutex};

        use opentelemetry::trace::TracerProvider as _;
        use opentelemetry_sdk::export::trace::SpanData;
        use opentelemetry_sdk::trace::{SpanProcessor, TracerProvider};
        use tracing_subscriber::layer::SubscriberExt;

        #[derive(Debug, Default, Clone)]
        struct CaptureProcessor {
            spans: Arc<Mutex<Vec<SpanData>>>,
        }

        impl SpanProcessor for CaptureProcessor {
            fn on_start(
                &self,
                _span: &mut opentelemetry_sdk::trace::Span,
                _cx: &opentelemetry::Context,
            ) {
            }
            fn on_end(&self, span: SpanData) {
                self.spans.lock().unwrap().push(span);
            }
            fn force_flush(&self) -> opentelemetry::trace::TraceResult<()> {
                Ok(())
            }
            fn shutdown(&self) -> opentelemetry::trace::TraceResult<()> {
                Ok(())
            }
        }

        let processor = CaptureProcessor::default();
        let captured = processor.spans.clone();
        let tp = TracerProvider::builder()
            .with_span_processor(processor)
            .build();
        let tracer = tp.tracer("payment-grpc-test");
        let subscriber =
            tracing_subscriber::registry().with(tracing_opentelemetry::layer().with_tracer(tracer));

        const TRACE_ID: &str = "bb11cc22dd33ee44ff556677889900aa";
        const PARENT_SPAN_ID: &str = "1122334455667788";
        let tp_hdr = format!("00-{TRACE_ID}-{PARENT_SPAN_ID}-01");

        let (server, _) = test_grpc_server();
        let mut req = with_auth(
            GetPaymentRequest {
                payment_id: "invalid-uuid".into(),
                order_group_id: String::new(),
                order_id: String::new(),
            },
            "admin",
            "admin-1",
            "",
        );
        req.metadata_mut()
            .insert("traceparent", MetadataValue::try_from(tp_hdr).unwrap());

        let dispatch = tracing::Dispatch::new(subscriber);
        let (span, tid) = tracing::dispatcher::with_default(&dispatch, || {
            let (s, t) = start_rpc_span("GetPayment", &req);
            (s, t)
        });
        assert_eq!(tid, TRACE_ID);
        drop(span);

        let err =
            tracing::instrument::WithSubscriber::with_subscriber(server.get_payment(req), dispatch)
                .await
                .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        let spans = captured.lock().unwrap();
        assert!(
            spans.len() >= 2,
            "expected at least 2 child spans, got {}",
            spans.len()
        );
        for sd in spans.iter() {
            assert_eq!(sd.span_context.trace_id().to_string(), TRACE_ID);
            assert_eq!(sd.parent_span_id.to_string(), PARENT_SPAN_ID);
            assert_ne!(sd.span_context.span_id().to_string(), PARENT_SPAN_ID);
        }
    }

    #[tokio::test]
    async fn test_w2b_fee_config_and_seller_ledger_rbac_and_validation() {
        let (server, _) = test_grpc_server();

        let svc_tok = crate::auth::sign_hs256(
            "grpc-test-service-key",
            serde_json::json!({
                "sub": "order-service",
                "role": "admin",
                "service": "order-service",
                "exp": Utc::now().timestamp() + 60,
            }),
        );
        fn with_svc<T>(tok: &str, payload: T) -> Request<T> {
            let mut req = Request::new(payload);
            req.metadata_mut().insert(
                "authorization",
                MetadataValue::try_from(format!("Bearer {tok}")).unwrap(),
            );
            req
        }

        // --- SetFeeConfig RBAC ---
        // Unauthenticated -> Unauthenticated
        let err = server
            .set_fee_config(Request::new(SetFeeConfigRequest {
                payment_method: PaymentMethod::Vietqr as i32,
                fee_bps: 150,
                effective_from: None,
                note: String::new(),
            }))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::Unauthenticated);

        // Service token -> PermissionDenied
        let err = server
            .set_fee_config(with_svc(
                &svc_tok,
                SetFeeConfigRequest {
                    payment_method: PaymentMethod::Vietqr as i32,
                    fee_bps: 150,
                    effective_from: None,
                    note: String::new(),
                },
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Buyer & seller -> PermissionDenied
        for (role, uid, sid) in [("buyer", "u-1", ""), ("seller", "u-2", "shop-1")] {
            let err = server
                .set_fee_config(with_auth(
                    SetFeeConfigRequest {
                        payment_method: PaymentMethod::Cod as i32,
                        fee_bps: 100,
                        effective_from: None,
                        note: String::new(),
                    },
                    role,
                    uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_eq!(err.code(), tonic::Code::PermissionDenied, "role={role}");
        }

        // --- SetFeeConfig validation (admin) ---
        // Unspecified method -> InvalidArgument
        let err = server
            .set_fee_config(with_auth(
                SetFeeConfigRequest {
                    payment_method: PaymentMethod::Unspecified as i32,
                    fee_bps: 100,
                    effective_from: None,
                    note: String::new(),
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        // fee_bps out of [0, 2000] -> InvalidArgument
        for bad_bps in [-1, 2001] {
            let err = server
                .set_fee_config(with_auth(
                    SetFeeConfigRequest {
                        payment_method: PaymentMethod::Vietqr as i32,
                        fee_bps: bad_bps,
                        effective_from: None,
                        note: String::new(),
                    },
                    "admin",
                    "admin-1",
                    "",
                ))
                .await
                .unwrap_err();
            assert_eq!(err.code(), tonic::Code::InvalidArgument, "bps={bad_bps}");
        }

        // Past effective_from -> InvalidArgument
        let err = server
            .set_fee_config(with_auth(
                SetFeeConfigRequest {
                    payment_method: PaymentMethod::Vietqr as i32,
                    fee_bps: 150,
                    effective_from: Some(chrono_to_proto(
                        Utc::now() - chrono::Duration::seconds(60),
                    )),
                    note: "past".into(),
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        // --- GetFeeConfig RBAC & validation ---
        // Unauthenticated -> Unauthenticated
        let err = server
            .get_fee_config(Request::new(GetFeeConfigRequest {
                payment_method: PaymentMethod::Unspecified as i32,
                include_history: false,
                at_time: None,
            }))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::Unauthenticated);

        // Buyer / seller -> PermissionDenied unconditionally (both include_history=false and true)
        for (role, uid, sid) in [("buyer", "u-1", ""), ("seller", "u-2", "shop-1")] {
            for include_history in [false, true] {
                let err = server
                    .get_fee_config(with_auth(
                        GetFeeConfigRequest {
                            payment_method: PaymentMethod::Unspecified as i32,
                            include_history,
                            at_time: None,
                        },
                        role,
                        uid,
                        sid,
                    ))
                    .await
                    .unwrap_err();
                assert_eq!(
                    err.code(),
                    tonic::Code::PermissionDenied,
                    "role={role} include_history={include_history}"
                );
            }
        }

        // Unknown payment_method enum value -> InvalidArgument (for service token & admin)
        let err = server
            .get_fee_config(with_svc(
                &svc_tok,
                GetFeeConfigRequest {
                    payment_method: 99,
                    include_history: false,
                    at_time: None,
                },
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        let err = server
            .get_fee_config(with_auth(
                GetFeeConfigRequest {
                    payment_method: 99,
                    include_history: false,
                    at_time: None,
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        // --- ListSellerLedger RBAC & validation ---
        // Unauthenticated -> Unauthenticated
        let err = server
            .list_seller_ledger(Request::new(ListSellerLedgerRequest {
                shop_id: String::new(),
                account_filter: String::new(),
                reference_type: String::new(),
                page: 1,
                page_size: 20,
            }))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::Unauthenticated);

        // Service token -> PermissionDenied
        let err = server
            .list_seller_ledger(with_svc(
                &svc_tok,
                ListSellerLedgerRequest {
                    shop_id: "shop-1".into(),
                    account_filter: String::new(),
                    reference_type: String::new(),
                    page: 1,
                    page_size: 20,
                },
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Buyer & demoted buyer with stale shop_id -> PermissionDenied
        for (uid, sid) in [("u-buyer", ""), ("u-demoted", "shop-1")] {
            let err = server
                .list_seller_ledger(with_auth(
                    ListSellerLedgerRequest {
                        shop_id: sid.into(),
                        account_filter: String::new(),
                        reference_type: String::new(),
                        page: 1,
                        page_size: 20,
                    },
                    "buyer",
                    uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_eq!(err.code(), tonic::Code::PermissionDenied, "uid={uid}");
        }

        // Seller without shop_id in claims -> PermissionDenied
        let err = server
            .list_seller_ledger(with_auth(
                ListSellerLedgerRequest {
                    shop_id: String::new(),
                    account_filter: String::new(),
                    reference_type: String::new(),
                    page: 1,
                    page_size: 20,
                },
                "seller",
                "u-seller",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Seller trying to read another shop's ledger -> PermissionDenied
        let err = server
            .list_seller_ledger(with_auth(
                ListSellerLedgerRequest {
                    shop_id: "shop-victim".into(),
                    account_filter: String::new(),
                    reference_type: String::new(),
                    page: 1,
                    page_size: 20,
                },
                "seller",
                "u-seller",
                "shop-mine",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::PermissionDenied);

        // Invalid account_filter -> InvalidArgument (for both seller and admin)
        let err = server
            .list_seller_ledger(with_auth(
                ListSellerLedgerRequest {
                    shop_id: String::new(),
                    account_filter: "gateway_clearing".into(),
                    reference_type: String::new(),
                    page: 1,
                    page_size: 20,
                },
                "seller",
                "u-seller",
                "shop-mine",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        let err = server
            .list_seller_ledger(with_auth(
                ListSellerLedgerRequest {
                    shop_id: String::new(),
                    account_filter: "escrow_liability".into(),
                    reference_type: String::new(),
                    page: 1,
                    page_size: 20,
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);
    }

    #[tokio::test]
    async fn test_w2b_r4_get_fee_config_requires_admin_or_service_unconditionally() {
        let (server, _) = test_grpc_server();

        // Unauthenticated -> Unauthenticated
        let err = server
            .get_fee_config(Request::new(GetFeeConfigRequest {
                payment_method: PaymentMethod::Vietqr as i32,
                include_history: false,
                at_time: None,
            }))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::Unauthenticated);

        // Buyer & seller MUST be rejected with PermissionDenied unconditionally
        // (even when include_history=false and at_time=None).
        for (role, uid, sid) in [("buyer", "u-1", ""), ("seller", "u-2", "shop-1")] {
            for include_history in [false, true] {
                let err = server
                    .get_fee_config(with_auth(
                        GetFeeConfigRequest {
                            payment_method: PaymentMethod::Vietqr as i32,
                            include_history,
                            at_time: None,
                        },
                        role,
                        uid,
                        sid,
                    ))
                    .await
                    .unwrap_err();
                assert_eq!(
                    err.code(),
                    tonic::Code::PermissionDenied,
                    "role={role} include_history={include_history} must be denied"
                );
            }
        }

        // Admin, super_admin, and service token (role="admin" or role="service") MUST pass RBAC
        // (reaches lazy DB -> Internal, NOT PermissionDenied/Unauthenticated).
        let future_ts = chrono_to_proto(Utc::now() + chrono::Duration::hours(2));
        for (role, uid) in [("admin", "admin-1"), ("super_admin", "sadmin-1")] {
            let err = server
                .get_fee_config(with_auth(
                    GetFeeConfigRequest {
                        payment_method: PaymentMethod::Vietqr as i32,
                        include_history: true,
                        at_time: Some(future_ts),
                    },
                    role,
                    uid,
                    "",
                ))
                .await
                .unwrap_err();
            assert_ne!(err.code(), tonic::Code::PermissionDenied, "role={role}");
            assert_ne!(err.code(), tonic::Code::Unauthenticated, "role={role}");
            assert_ne!(err.code(), tonic::Code::InvalidArgument, "role={role}");
        }

        for svc_role in ["admin", "service"] {
            let svc_tok = crate::auth::sign_hs256(
                "grpc-test-service-key",
                serde_json::json!({
                    "sub": "order-service",
                    "role": svc_role,
                    "service": "order-service",
                    "exp": Utc::now().timestamp() + 60,
                }),
            );
            let mut req = Request::new(GetFeeConfigRequest {
                payment_method: PaymentMethod::Vietqr as i32,
                include_history: false,
                at_time: Some(future_ts),
            });
            req.metadata_mut().insert(
                "authorization",
                MetadataValue::try_from(format!("Bearer {svc_tok}")).unwrap(),
            );
            let err = server.get_fee_config(req).await.unwrap_err();
            assert_ne!(
                err.code(),
                tonic::Code::PermissionDenied,
                "svc_role={svc_role}"
            );
            assert_ne!(
                err.code(),
                tonic::Code::Unauthenticated,
                "svc_role={svc_role}"
            );
            assert_ne!(
                err.code(),
                tonic::Code::InvalidArgument,
                "svc_role={svc_role}"
            );
        }

        // Admin/service callers receive full metadata (created_by & note).
        let now = Utc::now();
        let rec = PaymentFeeConfigRecord {
            id: Uuid::new_v4(),
            seq: 10,
            payment_method: "VIETQR".into(),
            fee_bps: 150,
            effective_from: now,
            created_by: "admin-user-1".into(),
            note: "internal Q4 fee adjustment".into(),
            created_at: now,
        };
        let pb = to_proto_fee_config_item(rec);
        assert_eq!(pb.fee_bps, 150);
        assert_eq!(pb.created_by, "admin-user-1");
        assert_eq!(pb.note, "internal Q4 fee adjustment");
    }

    #[test]
    fn test_w2b_r4_payment_projection_filters_by_caller_role() {
        use crate::domain::{PaymentRecord, ShopAllocationRecord};

        let now = Utc::now();
        let payment_id = Uuid::new_v4();
        let make_pwa = || PaymentWithAllocations {
            payment: PaymentRecord {
                id: payment_id,
                order_group_id: "grp-multi-1".into(),
                order_id: "ord-shop-b".into(), // worst-case: top-level order_id points to shop-b
                user_id: "buyer-owner".into(),
                method: "VIETQR".into(),
                provider: "vietqr_mock".into(),
                amount_vnd: 740_000, // shop-a 210,000 + shop-b 530,000
                status: "succeeded".into(),
                provider_tx_id: Some("TX-SECRET-999".into()),
                provider_order_code: "NM-ORDER-999".into(),
                qr_code: Some("000201010212-QR-PAYLOAD".into()),
                qr_url: Some("https://qr.example/secret-1".into()),
                pay_url: Some("https://sbgateway.zalopay.vn/openinapp?order=secret".into()),
                idempotency_key: Some("idem-1".into()),
                expires_at: None,
                paid_at: Some(now),
                trace_id: "trace-1".into(),
                created_at: now,
                updated_at: now,
            },
            allocations: vec![
                ShopAllocationRecord {
                    id: Uuid::new_v4(),
                    payment_id,
                    order_id: "ord-shop-a".into(),
                    shop_id: "shop-a".into(),
                    subtotal_vnd: 200_000,
                    commission_vnd: 12_000,
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 10_000,
                    escrow_status: "held".into(),
                    settled_at: Some(now),
                    released_at: None,
                    tracking_code: None,
                    carrier_code: None,
                    order_code: None,
                    cod_reconciled_at: None,
                    cod_reconciliation_run_id: None,
                    created_at: now,
                },
                ShopAllocationRecord {
                    id: Uuid::new_v4(),
                    payment_id,
                    order_id: "ord-shop-b".into(),
                    shop_id: "shop-b".into(),
                    subtotal_vnd: 500_000,
                    commission_vnd: 35_000,
                    shipping_fee_vnd: 30_000,
                    platform_discount_vnd: 0,
                    escrow_status: "held".into(),
                    settled_at: Some(now),
                    released_at: None,
                    tracking_code: None,
                    carrier_code: None,
                    order_code: None,
                    cod_reconciled_at: None,
                    cod_reconciliation_run_id: None,
                    created_at: now,
                },
            ],
        };

        // 1. Admin, super_admin, and service tokens (role="admin" or role="service"): full view.
        for caller in [
            Caller {
                role: "admin".into(),
                user_id: "admin-1".into(),
                ..Default::default()
            },
            Caller {
                role: "super_admin".into(),
                user_id: "sadmin-1".into(),
                ..Default::default()
            },
            Caller {
                role: "admin".into(),
                user_id: "order-service".into(),
                is_service: true,
                ..Default::default()
            },
            Caller {
                role: "service".into(),
                user_id: "order-service".into(),
                is_service: true,
                ..Default::default()
            },
        ] {
            let pb = to_proto_payment_for_caller(make_pwa(), &caller);
            assert_eq!(pb.amount_vnd, 740_000);
            assert_eq!(pb.qr_url, "https://qr.example/secret-1");
            assert_eq!(pb.qr_code, "000201010212-QR-PAYLOAD");
            assert_eq!(pb.provider_ref, "TX-SECRET-999");
            assert_eq!(pb.shop_allocations.len(), 2);
            assert_eq!(pb.shop_allocations[0].commission_vnd, 12_000);
            assert_eq!(pb.shop_allocations[1].commission_vnd, 35_000);
        }

        // 2. Buyer owner AND seller acting as buyer owner: all allocations, commission_vnd == 0,
        //    buyer QR / provider_ref / full amount_vnd preserved.
        for caller in [
            Caller {
                role: "buyer".into(),
                user_id: "buyer-owner".into(),
                ..Default::default()
            },
            Caller {
                role: "seller".into(),
                user_id: "buyer-owner".into(),
                shop_id: "shop-other".into(),
                ..Default::default()
            },
            Caller {
                role: "seller".into(),
                user_id: "buyer-owner".into(),
                shop_id: "shop-a".into(),
                ..Default::default()
            },
        ] {
            let pb = to_proto_payment_for_caller(make_pwa(), &caller);
            assert_eq!(pb.amount_vnd, 740_000);
            assert_eq!(pb.qr_url, "https://qr.example/secret-1");
            assert_eq!(pb.qr_code, "000201010212-QR-PAYLOAD");
            assert_eq!(pb.provider_ref, "TX-SECRET-999");
            assert_eq!(pb.shop_allocations.len(), 2);
            for a in &pb.shop_allocations {
                assert_eq!(
                    a.commission_vnd, 0,
                    "buyer view must mask commission_vnd to 0 for caller={caller:?}"
                );
            }
            assert_eq!(pb.shop_allocations[0].subtotal_vnd, 200_000);
            assert_eq!(pb.shop_allocations[0].platform_discount_vnd, 10_000);
            assert_eq!(pb.shop_allocations[1].subtotal_vnd, 500_000);
        }

        // 3. Seller who ONLY owns shop-a allocation (not buyer owner):
        //    only shop-a allocation (with real commission_vnd), redacted QR/provider_ref,
        //    shop-scoped amount_vnd = 200_000 + 20_000 - 10_000 = 210_000,
        //    order_id must NOT leak shop-b's order_id.
        let seller_a = Caller {
            role: "seller".into(),
            user_id: "seller-u1".into(),
            shop_id: "shop-a".into(),
            ..Default::default()
        };
        let pb_seller = to_proto_payment_for_caller(make_pwa(), &seller_a);
        assert_eq!(pb_seller.user_id, "buyer-owner");
        assert_eq!(pb_seller.order_group_id, "grp-multi-1");
        assert_eq!(pb_seller.order_id, "ord-shop-a");
        assert_eq!(pb_seller.amount_vnd, 210_000);
        assert!(pb_seller.qr_url.is_empty(), "qr_url must be redacted");
        assert!(pb_seller.qr_code.is_empty(), "qr_code must be redacted");
        assert!(
            pb_seller.provider_ref.is_empty(),
            "provider_ref must be redacted"
        );
        assert_eq!(pb_seller.shop_allocations.len(), 1);
        assert_eq!(pb_seller.shop_allocations[0].shop_id, "shop-a");
        assert_eq!(pb_seller.shop_allocations[0].order_id, "ord-shop-a");
        assert_eq!(pb_seller.shop_allocations[0].subtotal_vnd, 200_000);
        assert_eq!(pb_seller.shop_allocations[0].commission_vnd, 12_000);
        assert_eq!(pb_seller.shop_allocations[0].shipping_fee_vnd, 20_000);
        assert_eq!(pb_seller.shop_allocations[0].platform_discount_vnd, 10_000);

        // 3b. Seller B on the same 2-shop payment sees ONLY shop-b (and checkout-group order_id is kept).
        let mut pwa_grp = make_pwa();
        pwa_grp.payment.order_id = "grp-multi-1".into();
        let seller_b = Caller {
            role: "seller".into(),
            user_id: "seller-u2".into(),
            shop_id: "shop-b".into(),
            ..Default::default()
        };
        let pb_seller_b = to_proto_payment_for_caller(pwa_grp, &seller_b);
        assert_eq!(pb_seller_b.order_id, "grp-multi-1");
        assert_eq!(pb_seller_b.amount_vnd, 530_000);
        assert!(pb_seller_b.qr_url.is_empty());
        assert!(pb_seller_b.qr_code.is_empty());
        assert!(pb_seller_b.provider_ref.is_empty());
        assert_eq!(pb_seller_b.shop_allocations.len(), 1);
        assert_eq!(pb_seller_b.shop_allocations[0].shop_id, "shop-b");
        assert_eq!(pb_seller_b.shop_allocations[0].commission_vnd, 35_000);

        // 4. Defensive fail-closed for unauthorized caller (should already be blocked by RBAC):
        let stranger = Caller {
            role: "buyer".into(),
            user_id: "stranger".into(),
            ..Default::default()
        };
        let pb_stranger = to_proto_payment_for_caller(make_pwa(), &stranger);
        assert!(pb_stranger.shop_allocations.is_empty());
        assert_eq!(pb_stranger.amount_vnd, 0);
        assert!(pb_stranger.qr_url.is_empty());
        assert!(pb_stranger.qr_code.is_empty());
        assert!(pb_stranger.provider_ref.is_empty());
    }

    #[test]
    fn test_w2b_r5_validate_service_create_payment_rules() {
        let base = || CreatePaymentRequest {
            order_group_id: "grp-123".into(),
            order_id: "grp-123".into(),
            user_id: "buyer-1".into(),
            amount_vnd: 150_000,
            method: PaymentMethod::Cod as i32,
            shop_allocations: vec![],
            expires_at: None,
            idempotency_key: "checkout:grp-123".into(),
            supersede_previous: false,
        };

        // Valid service forward-recovery request -> Ok
        assert!(validate_service_create_payment(&base()).is_ok());

        // Rule 1: missing or untrimmed order_group_id -> InvalidArgument
        for bad_group in ["", "   ", " grp-123 "] {
            let mut req = base();
            req.order_group_id = bad_group.into();
            req.idempotency_key = format!("checkout:{bad_group}");
            let err = validate_service_create_payment(&req).unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::InvalidArgument,
                "bad_group={bad_group:?}"
            );
        }

        // Rule 1: missing or mismatched idempotency_key -> InvalidArgument
        for bad_key in [
            "",
            "   ",
            "retry:grp-123",
            "checkout:grp-other",
            " checkout:grp-123 ",
            "checkout:",
        ] {
            let mut req = base();
            req.idempotency_key = bad_key.into();
            let err = validate_service_create_payment(&req).unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::InvalidArgument,
                "bad_key={bad_key:?}"
            );
        }

        // Rule 2: supersede_previous = true -> PermissionDenied (even with valid or mismatched key)
        for key in ["checkout:grp-123", "retry-grp-123", ""] {
            let mut req = base();
            req.idempotency_key = key.into();
            req.supersede_previous = true;
            let err = validate_service_create_payment(&req).unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::PermissionDenied,
                "supersede key={key:?}"
            );
        }

        // Rule 3: empty user_id -> InvalidArgument
        for bad_uid in ["", "   "] {
            let mut req = base();
            req.user_id = bad_uid.into();
            let err = validate_service_create_payment(&req).unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::InvalidArgument,
                "bad_uid={bad_uid:?}"
            );
        }
    }

    #[tokio::test]
    async fn test_w2b_r5_grpc_create_payment_with_real_service_token() {
        let (server, _) = test_grpc_server();

        let sign_svc = |role: &str| {
            crate::auth::sign_hs256(
                "grpc-test-service-key",
                serde_json::json!({
                    "sub": "order-service",
                    "role": role,
                    "service": "order-service",
                    "exp": Utc::now().timestamp() + 60,
                }),
            )
        };
        let with_svc = |tok: &str, payload: CreatePaymentRequest| {
            let mut req = Request::new(payload);
            req.metadata_mut().insert(
                "authorization",
                MetadataValue::try_from(format!("Bearer {tok}")).unwrap(),
            );
            req
        };
        let valid_req = || CreatePaymentRequest {
            order_group_id: "grp-rec-1".into(),
            order_id: "grp-rec-1".into(),
            user_id: "buyer-owner-1".into(),
            amount_vnd: 200_000,
            method: PaymentMethod::Cod as i32,
            shop_allocations: vec![],
            expires_at: None,
            idempotency_key: "checkout:grp-rec-1".into(),
            supersede_previous: false,
        };

        for svc_role in ["admin", "service"] {
            let tok = sign_svc(svc_role);

            // Service token with supersede_previous=true -> PermissionDenied
            let mut sup_req = valid_req();
            sup_req.supersede_previous = true;
            let err = server
                .create_payment(with_svc(&tok, sup_req))
                .await
                .unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::PermissionDenied,
                "svc_role={svc_role} supersede_previous=true"
            );

            // Service token with mismatched idempotency_key -> InvalidArgument
            let mut bad_key = valid_req();
            bad_key.idempotency_key = "checkout:other".into();
            let err = server
                .create_payment(with_svc(&tok, bad_key))
                .await
                .unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::InvalidArgument,
                "svc_role={svc_role} mismatched key"
            );

            // Service token with empty order_group_id -> InvalidArgument
            let mut no_grp = valid_req();
            no_grp.order_group_id = String::new();
            let err = server
                .create_payment(with_svc(&tok, no_grp))
                .await
                .unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::InvalidArgument,
                "svc_role={svc_role} empty order_group_id"
            );

            // Service token with empty user_id -> InvalidArgument
            let mut no_uid = valid_req();
            no_uid.user_id = "  ".into();
            let err = server
                .create_payment(with_svc(&tok, no_uid))
                .await
                .unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::InvalidArgument,
                "svc_role={svc_role} empty user_id"
            );

            // Valid service token CreatePayment passes auth + service guard and reaches lazy DB
            // (Internal/Unavailable, NOT Unauthenticated / PermissionDenied / InvalidArgument).
            let err = server
                .create_payment(with_svc(&tok, valid_req()))
                .await
                .unwrap_err();
            assert_ne!(
                err.code(),
                tonic::Code::Unauthenticated,
                "svc_role={svc_role}"
            );
            assert_ne!(
                err.code(),
                tonic::Code::PermissionDenied,
                "svc_role={svc_role}"
            );
            assert_ne!(
                err.code(),
                tonic::Code::InvalidArgument,
                "svc_role={svc_role}"
            );
        }

        // End-user buyer JWT behavior unchanged: buyer can still retry with supersede_previous=true
        // and custom retry idempotency_key (reaches lazy DB, not blocked by service-only rules).
        let mut buyer_retry = valid_req();
        buyer_retry.idempotency_key = "retry-pay-1".into();
        buyer_retry.supersede_previous = true;
        let err = server
            .create_payment(with_auth(buyer_retry, "buyer", "buyer-owner-1", ""))
            .await
            .unwrap_err();
        assert_ne!(err.code(), tonic::Code::Unauthenticated);
        assert_ne!(err.code(), tonic::Code::PermissionDenied);
        assert_ne!(err.code(), tonic::Code::InvalidArgument);
    }

    #[tokio::test]
    async fn test_w3b_e20_import_cod_reconciliation_and_list_runs_rbac_and_validation() {
        let (server, metrics) = test_grpc_server();

        let sample_import_req = || ImportCodReconciliationRequest {
            carrier_code: "ghn".into(),
            file_name: "cod_20261124.csv".into(),
            file_sha256: "abc123".into(),
            rows: vec![pb::CodReconciliationRow {
                line_number: 1,
                tracking_code: "TRK-001".into(),
                cod_amount_vnd: 250_000,
                delivered_at: "2026-11-24T08:00:00Z".into(),
                order_code: String::new(),
            }],
            run_date: "2026-11-24".into(),
        };

        // 1. Missing JWT (even with spoofed x-user-role) -> Unauthenticated
        let mut unauth_import = Request::new(sample_import_req());
        unauth_import
            .metadata_mut()
            .insert("x-user-role", MetadataValue::from_static("admin"));
        assert_eq!(
            server
                .import_cod_reconciliation(unauth_import)
                .await
                .unwrap_err()
                .code(),
            tonic::Code::Unauthenticated
        );
        let mut unauth_list = Request::new(ListReconciliationRunsRequest {
            run_type: String::new(),
            page: 1,
            page_size: 20,
        });
        unauth_list
            .metadata_mut()
            .insert("x-user-role", MetadataValue::from_static("admin"));
        assert_eq!(
            server
                .list_reconciliation_runs(unauth_list)
                .await
                .unwrap_err()
                .code(),
            tonic::Code::Unauthenticated
        );

        // 2. Buyer and seller JWTs -> PermissionDenied
        for (role, uid, sid) in [("buyer", "buyer-1", ""), ("seller", "seller-1", "shop-1")] {
            assert_eq!(
                server
                    .import_cod_reconciliation(with_auth(sample_import_req(), role, uid, sid))
                    .await
                    .unwrap_err()
                    .code(),
                tonic::Code::PermissionDenied,
                "ImportCodReconciliation role={role}"
            );
            assert_eq!(
                server
                    .list_reconciliation_runs(with_auth(
                        ListReconciliationRunsRequest {
                            run_type: "cod_file".into(),
                            page: 1,
                            page_size: 20,
                        },
                        role,
                        uid,
                        sid,
                    ))
                    .await
                    .unwrap_err()
                    .code(),
                tonic::Code::PermissionDenied,
                "ListReconciliationRuns role={role}"
            );
        }

        // 3. Service token -> PermissionDenied (not in SERVICE_METHODS)
        let svc_tok = crate::auth::sign_hs256(
            "grpc-test-service-key",
            serde_json::json!({
                "sub": "order-service",
                "role": "admin",
                "service": "order-service",
                "exp": Utc::now().timestamp() + 60,
            }),
        );
        let mut svc_import = Request::new(sample_import_req());
        svc_import.metadata_mut().insert(
            "authorization",
            MetadataValue::try_from(format!("Bearer {svc_tok}")).unwrap(),
        );
        assert_eq!(
            server
                .import_cod_reconciliation(svc_import)
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
        let mut svc_list = Request::new(ListReconciliationRunsRequest {
            run_type: String::new(),
            page: 1,
            page_size: 20,
        });
        svc_list.metadata_mut().insert(
            "authorization",
            MetadataValue::try_from(format!("Bearer {svc_tok}")).unwrap(),
        );
        assert_eq!(
            server
                .list_reconciliation_runs(svc_list)
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );

        // 4. Admin validation before DB: empty rows, >5000 rows, future run_date, bad run_type
        let mut empty_rows = sample_import_req();
        empty_rows.rows.clear();
        assert_eq!(
            server
                .import_cod_reconciliation(with_auth(empty_rows, "admin", "admin-1", ""))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::InvalidArgument
        );

        let mut too_many = sample_import_req();
        too_many.rows = (1..=5001)
            .map(|n| pb::CodReconciliationRow {
                line_number: n,
                tracking_code: format!("TRK-{n}"),
                cod_amount_vnd: 100_000,
                delivered_at: String::new(),
                order_code: String::new(),
            })
            .collect();
        assert_eq!(
            server
                .import_cod_reconciliation(with_auth(too_many, "admin", "admin-1", ""))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::InvalidArgument
        );

        let mut future_date = sample_import_req();
        future_date.run_date = "2099-12-31".into();
        assert_eq!(
            server
                .import_cod_reconciliation(with_auth(future_date, "admin", "admin-1", ""))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::InvalidArgument
        );

        assert_eq!(
            server
                .list_reconciliation_runs(with_auth(
                    ListReconciliationRunsRequest {
                        run_type: "invalid_type".into(),
                        page: 1,
                        page_size: 20,
                    },
                    "admin",
                    "admin-1",
                    "",
                ))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::InvalidArgument
        );

        // 5. Admin & super_admin pass RBAC and validation (hit lazy DB, NOT Unimplemented/Auth/InvalidArgument)
        for role in ["admin", "super_admin"] {
            let err_imp = server
                .import_cod_reconciliation(with_auth(sample_import_req(), role, "a-1", ""))
                .await
                .unwrap_err();
            assert_ne!(err_imp.code(), tonic::Code::Unimplemented, "role={role}");
            assert_ne!(err_imp.code(), tonic::Code::Unauthenticated, "role={role}");
            assert_ne!(err_imp.code(), tonic::Code::PermissionDenied, "role={role}");
            assert_ne!(err_imp.code(), tonic::Code::InvalidArgument, "role={role}");

            let err_list = server
                .list_reconciliation_runs(with_auth(
                    ListReconciliationRunsRequest {
                        run_type: "cod_file".into(),
                        page: 1,
                        page_size: 10,
                    },
                    role,
                    "a-1",
                    "",
                ))
                .await
                .unwrap_err();
            assert_ne!(err_list.code(), tonic::Code::Unimplemented, "role={role}");
            assert_ne!(err_list.code(), tonic::Code::Unauthenticated, "role={role}");
            assert_ne!(
                err_list.code(),
                tonic::Code::PermissionDenied,
                "role={role}"
            );
            assert_ne!(err_list.code(), tonic::Code::InvalidArgument, "role={role}");
        }

        let prom = metrics.render_prometheus();
        assert!(prom.contains("method=\"ImportCodReconciliation\""));
        assert!(prom.contains("method=\"ListReconciliationRuns\""));
    }

    #[tokio::test]
    async fn test_w3b2_f9_all_admin_rpcs_enforce_capability_matrix_across_all_6_staff_roles() {
        let (server, _) = test_grpc_server();
        let all_staff = [
            "super_admin",
            "admin",
            "moderator",
            "cskh",
            "accountant",
            "ops",
        ];

        fn with_svc<T>(payload: T) -> Request<T> {
            let tok = crate::auth::sign_hs256(
                "grpc-test-service-key",
                serde_json::json!({
                    "sub": "order-service",
                    "role": "super_admin",
                    "service": "order-service",
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

        let assert_rbac = |op: &str, role: &str, allowed: bool, status: Status| {
            if allowed {
                assert_ne!(
                    status.code(),
                    tonic::Code::Unauthenticated,
                    "{op} role={role} must pass auth"
                );
                assert_ne!(
                    status.code(),
                    tonic::Code::PermissionDenied,
                    "{op} role={role} must be ALLOWED"
                );
            } else {
                assert_eq!(
                    status.code(),
                    tonic::Code::PermissionDenied,
                    "{op} role={role} must be DENIED, got {status:?}"
                );
            }
        };

        for role in all_staff
            .iter()
            .copied()
            .chain(["buyer", "seller"].into_iter())
        {
            let uid = format!("u-{role}");
            let sid = if role == "seller" { "shop-1" } else { "" };

            // 1. GetLedgerSummary -> ledger.read (super_admin, admin, accountant, ops)
            let allowed = matches!(role, "super_admin" | "admin" | "accountant" | "ops");
            let err = server
                .get_ledger_summary(with_auth(GetLedgerSummaryRequest {}, role, &uid, sid))
                .await
                .unwrap_err();
            assert_rbac("GetLedgerSummary", role, allowed, err);

            // 2. ReleaseEscrow -> escrow.release (super_admin, admin, accountant)
            let allowed = matches!(role, "super_admin" | "admin" | "accountant");
            let err = server
                .release_escrow(with_auth(
                    ReleaseEscrowRequest {
                        order_id: "ord-1".into(),
                        shop_id: String::new(),
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("ReleaseEscrow", role, allowed, err);

            // 3. RunReconciliation -> reconciliation.write (super_admin, admin, accountant)
            let allowed = matches!(role, "super_admin" | "admin" | "accountant");
            let err = server
                .run_reconciliation(with_auth(
                    RunReconciliationRequest {
                        date: "bad-date".into(),
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("RunReconciliation", role, allowed, err);

            // 4. ListReconciliationQueue -> ledger.read (super_admin, admin, accountant, ops)
            let allowed = matches!(role, "super_admin" | "admin" | "accountant" | "ops");
            let err = server
                .list_reconciliation_queue(with_auth(
                    ListReconciliationQueueRequest {
                        status: String::new(),
                        page: 1,
                        page_size: 10,
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("ListReconciliationQueue", role, allowed, err);

            // 5. ResolveReconciliationItem -> reconciliation.write (super_admin, admin, accountant)
            let allowed = matches!(role, "super_admin" | "admin" | "accountant");
            let err = server
                .resolve_reconciliation_item(with_auth(
                    ResolveReconciliationItemRequest {
                        item_id: "not-a-uuid".into(),
                        action: ReconciliationAction::Suspend as i32,
                        payment_id: String::new(),
                        note: String::new(),
                        idempotency_key: "k1".into(),
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("ResolveReconciliationItem", role, allowed, err);

            // 6. GetFeeConfig -> fees.payment.read (super_admin, admin, accountant, ops)
            let allowed = matches!(role, "super_admin" | "admin" | "accountant" | "ops");
            let err = server
                .get_fee_config(with_auth(
                    GetFeeConfigRequest {
                        payment_method: PaymentMethod::Zalopay as i32,
                        include_history: false,
                        at_time: None,
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("GetFeeConfig", role, allowed, err);

            // 7. SetFeeConfig -> fees.payment.write (super_admin, admin, accountant)
            let allowed = matches!(role, "super_admin" | "admin" | "accountant");
            let err = server
                .set_fee_config(with_auth(
                    SetFeeConfigRequest {
                        payment_method: PaymentMethod::Unspecified as i32,
                        fee_bps: 100,
                        effective_from: None,
                        note: String::new(),
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("SetFeeConfig", role, allowed, err);

            // 8. CreateRefund -> refunds.create (super_admin, admin, cskh, accountant)
            let allowed = matches!(role, "super_admin" | "admin" | "cskh" | "accountant");
            let err = server
                .create_refund(with_auth(
                    CreateRefundRequest {
                        payment_id: "bad-uuid".into(),
                        order_id: "ord-1".into(),
                        amount_vnd: 100_000,
                        reason: "manual".into(),
                        idempotency_key: "k1".into(),
                        require_approval_if_large: false,
                        requested_by: String::new(),
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("CreateRefund", role, allowed, err);

            // 9. DecideRefundApproval -> refunds.approve (super_admin, admin, accountant)
            let allowed = matches!(role, "super_admin" | "admin" | "accountant");
            let err = server
                .decide_refund_approval(with_auth(
                    DecideRefundApprovalRequest {
                        refund_id: "bad-uuid".into(),
                        approve: true,
                        note: "ok".into(),
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("DecideRefundApproval", role, allowed, err);

            // 10. SettleRefund -> refunds.settle (super_admin, admin, accountant)
            let allowed = matches!(role, "super_admin" | "admin" | "accountant");
            let err = server
                .settle_refund(with_auth(
                    SettleRefundRequest {
                        refund_id: "bad-uuid".into(),
                        provider_refund_id: "FT1".into(),
                        note: "ok".into(),
                        idempotency_key: "sk1".into(),
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("SettleRefund", role, allowed, err);

            // 11. ListRefunds -> staff without `refunds.read` (`moderator`) must NOT be blocked
            //     up-front so they can view their OWN refunds (`is_admin=false, caller_user_id=sub`);
            //     access to another user's order_id is blocked with PermissionDenied inside service.
            if role == "moderator" {
                let err = server
                    .list_refunds(with_auth(
                        ListRefundsRequest {
                            status: 0,
                            order_id: String::new(),
                            page: 1,
                            page_size: 10,
                        },
                        role,
                        &uid,
                        sid,
                    ))
                    .await
                    .unwrap_err();
                assert_ne!(
                    err.code(),
                    tonic::Code::PermissionDenied,
                    "moderator listing own refunds must pass gRPC gate to owner-scoped DB query"
                );
            }

            // 12. UpdatePaymentMethodConfig -> payment_methods.write (super_admin, admin, ops)
            let allowed = matches!(role, "super_admin" | "admin" | "ops");
            let err = server
                .update_payment_method_config(with_auth(
                    UpdatePaymentMethodConfigRequest {
                        method: PaymentMethod::Unspecified as i32,
                        enabled: true,
                        min_order_vnd: 0,
                        max_order_vnd: 0,
                        expiry_minutes: 15,
                        note: String::new(),
                        expected_version: 1,
                    },
                    role,
                    &uid,
                    sid,
                ))
                .await
                .unwrap_err();
            assert_rbac("UpdatePaymentMethodConfig", role, allowed, err);
        }

        // Service token MUST be denied on UpdatePaymentMethodConfig & DecideRefundApproval.
        assert_eq!(
            server
                .update_payment_method_config(with_svc(UpdatePaymentMethodConfigRequest {
                    method: PaymentMethod::Zalopay as i32,
                    enabled: true,
                    min_order_vnd: 0,
                    max_order_vnd: 0,
                    expiry_minutes: 15,
                    note: String::new(),
                    expected_version: 1,
                }))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
        assert_eq!(
            server
                .decide_refund_approval(with_svc(DecideRefundApprovalRequest {
                    refund_id: Uuid::new_v4().to_string(),
                    approve: true,
                    note: "svc".into(),
                }))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );

        // Service token on CreateRefund with require_approval_if_large=true and empty requested_by -> InvalidArgument!
        let err_missing_maker = server
            .create_refund(with_svc(CreateRefundRequest {
                payment_id: String::new(),
                order_id: "ord-1".into(),
                amount_vnd: 6_000_000,
                reason: "return".into(),
                idempotency_key: "k-ret-1".into(),
                require_approval_if_large: true,
                requested_by: "   ".into(),
            }))
            .await
            .unwrap_err();
        assert_eq!(err_missing_maker.code(), tonic::Code::InvalidArgument);

        // Method config projection strips admin fields for public/service, keeps masked credentials for admin.
        let rec = PaymentMethodConfigRecord {
            method: "ZALOPAY".into(),
            enabled: true,
            min_order_vnd: 0,
            max_order_vnd: 0,
            expiry_minutes: 15,
            version: 3,
            note: "secret-admin-note".into(),
            updated_by: "ops-1".into(),
            updated_at: Utc::now(),
        };
        let pub_pb = to_proto_method_config(&server.service, rec.clone(), false);
        assert_eq!(pub_pb.method, PaymentMethod::Zalopay as i32);
        assert!(pub_pb.enabled);
        assert_eq!(pub_pb.version, 3);
        assert!(!pub_pb.credentials_configured);
        assert!(pub_pb.credential_preview.is_empty());
        assert!(pub_pb.updated_by.is_empty());
        assert!(pub_pb.updated_at.is_none());
        assert!(pub_pb.note.is_empty());

        let adm_pb = to_proto_method_config(&server.service, rec, true);
        assert!(adm_pb.credentials_configured);
        assert!(adm_pb.credential_preview.contains("[REDACTED]"));
        assert!(!adm_pb
            .credential_preview
            .contains(crate::config::DEV_ONLY_ZALOPAY_MOCK_KEY1));
        assert!(!adm_pb
            .credential_preview
            .contains(crate::config::DEV_ONLY_ZALOPAY_MOCK_KEY2));
        assert_eq!(adm_pb.updated_by, "ops-1");
        assert_eq!(adm_pb.note, "secret-admin-note");
        assert_eq!(adm_pb.version, 3);
    }

    #[tokio::test]
    async fn test_p4_item_l_and_e_owner_rbac_and_reserved_idempotency_prefixes() {
        let (server, _) = test_grpc_server();

        // Item L: cskh, accountant, ops, moderator MUST NOT pass `require_payment_owner` for another user.
        for role in ["cskh", "accountant", "ops", "moderator"] {
            let err = server
                .create_payment(with_auth(
                    CreatePaymentRequest {
                        order_group_id: "grp-victim".into(),
                        order_id: "ord-victim".into(),
                        user_id: "buyer-victim".into(),
                        amount_vnd: 100_000,
                        method: PaymentMethod::Vietqr as i32,
                        shop_allocations: vec![],
                        expires_at: None,
                        idempotency_key: "idem-staff-attack".into(),
                        supersede_previous: false,
                    },
                    role,
                    &format!("staff-{role}"),
                    "",
                ))
                .await
                .unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::PermissionDenied,
                "Item L: role={role} must be denied CreatePayment for another user"
            );
        }

        // Item E: End-user CreateRefund MUST reject all 7 reserved internal prefixes (trimmed, case-insensitive).
        for bad_key in [
            "order-cancel-refund:ord-1",
            "  ORDER-CANCEL-REFUND:ord-1  ",
            "order-return-refund:ord-1",
            "Order-Return-Refund:ord-1:r1",
            "superseded-late-refund:p1:ord-1",
            "late-webhook:2611240001",
            "  LATE-WEBHOOK:2611240001",
            "recon-refund:tx-1",
            "recon-overpay:tx-1",
            "refund-settle:r-1",
        ] {
            let err = server
                .create_refund(with_auth(
                    CreateRefundRequest {
                        payment_id: String::new(),
                        order_id: "ord-1".into(),
                        amount_vnd: 1_000,
                        reason: "spoof".into(),
                        idempotency_key: bad_key.into(),
                        require_approval_if_large: false,
                        requested_by: String::new(),
                    },
                    "cskh",
                    "cskh-1",
                    "",
                ))
                .await
                .unwrap_err();
            assert_eq!(
                err.code(),
                tonic::Code::InvalidArgument,
                "Item E: user CreateRefund must reject reserved key={bad_key:?}"
            );
        }
    }
}
