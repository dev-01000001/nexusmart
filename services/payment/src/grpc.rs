use std::time::Instant;

use chrono::{DateTime, NaiveDate, Utc};
use prost_types::Timestamp;
use tonic::{Request, Response, Status};
use tracing::{info_span, Instrument};
use tracing_opentelemetry::OpenTelemetrySpanExt;
use uuid::Uuid;

use crate::auth::GrpcAuth;
use crate::domain::{
    CreatePaymentInput, CreateRefundInput, PaymentMethod as DomainMethod, PaymentWithAllocations,
    ReconciliationItemRecord, ReconciliationRunRecord, RefundRecord, SalesChannel,
    ShopAllocationInput,
};
use crate::service::PaymentService;
use crate::telemetry::{extract_grpc_context, extract_or_generate_trace_id};

#[allow(clippy::result_large_err)]
pub mod pb {
    tonic::include_proto!("nexusmart.payment.v1");
}

use pb::payment_service_server::PaymentService as PaymentServiceTrait;
use pb::{
    AccountBalance, CancelPaymentRequest, CancelPaymentResponse, CreatePaymentRequest,
    CreatePaymentResponse, CreateRefundRequest, CreateRefundResponse, GetLedgerSummaryRequest,
    GetLedgerSummaryResponse, GetPaymentRequest, GetPaymentResponse, GetSellerBalanceRequest,
    GetSellerBalanceResponse, ListReconciliationReportsRequest, ListReconciliationReportsResponse,
    Payment, PaymentMethod, PaymentStatus, ReconciliationItem, ReconciliationReportSummary,
    ReconciliationStatus, Refund, RefundStatus, ReleaseEscrowRequest, ReleaseEscrowResponse,
    RunReconciliationRequest, RunReconciliationResponse, ShopAllocation,
};

#[derive(Debug, Clone)]
struct CallerAuth {
    role: String,
    user_id: String,
    shop_id: String,
}

impl CallerAuth {
    /// Authenticates from the verified Bearer JWT only (see crate::auth).
    #[allow(clippy::result_large_err)]
    fn from_request<T>(
        auth: &GrpcAuth,
        request: &Request<T>,
        method: &str,
    ) -> Result<Self, Status> {
        let c = auth.authenticate(request.metadata(), method)?;
        Ok(Self {
            role: c.role,
            user_id: c.user_id,
            shop_id: c.shop_id,
        })
    }

    fn is_admin(&self) -> bool {
        matches!(self.role.as_str(), "admin" | "super_admin")
    }

    #[allow(clippy::result_large_err)]
    fn require_admin(&self, op: &str) -> Result<(), Status> {
        if !self.is_admin() {
            return Err(Status::permission_denied(format!(
                "forbidden: {op} requires admin role"
            )));
        }
        Ok(())
    }

    #[allow(clippy::result_large_err)]
    fn require_payment_read_access(&self, pwa: &PaymentWithAllocations) -> Result<(), Status> {
        if self.is_admin() {
            return Ok(());
        }
        match self.role.as_str() {
            "buyer" => {
                if !self.user_id.is_empty() && pwa.payment.user_id == self.user_id {
                    return Ok(());
                }
            }
            "seller" => {
                let owns_as_buyer = !self.user_id.is_empty() && pwa.payment.user_id == self.user_id;
                let owns_shop_alloc = !self.shop_id.is_empty()
                    && pwa.allocations.iter().any(|a| a.shop_id == self.shop_id);
                if owns_as_buyer || owns_shop_alloc {
                    return Ok(());
                }
            }
            _ => {}
        }
        Err(Status::permission_denied(
            "forbidden: cannot access another user's payment",
        ))
    }

    #[allow(clippy::result_large_err)]
    fn require_payment_owner(&self, owner_user_id: &str, op: &str) -> Result<(), Status> {
        if self.is_admin() {
            return Ok(());
        }
        if !self.user_id.is_empty() && owner_user_id.trim() == self.user_id {
            return Ok(());
        }
        Err(Status::permission_denied(format!(
            "forbidden: {op} denied for another user's payment"
        )))
    }

    #[allow(clippy::result_large_err)]
    fn require_seller_shop_access(&self, target_shop_id: &str) -> Result<(), Status> {
        if self.is_admin() {
            return Ok(());
        }
        if self.role == "seller"
            && !self.shop_id.is_empty()
            && !target_shop_id.is_empty()
            && self.shop_id == target_shop_id
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
        let full_method = format!("/nexusmart.payment.v1.PaymentService/{method}");
        match res {
            Ok(_) => self
                .service
                .metrics()
                .record_grpc(&full_method, "OK", false, elapsed),
            Err(status) => {
                let code = format!("{:?}", status.code());
                self.service
                    .metrics()
                    .record_grpc(&full_method, &code, true, elapsed);
            }
        }
    }
}

fn start_grpc_span<T>(method: &str, request: &Request<T>, trace_id: &str) -> tracing::Span {
    let full_method = format!("/nexusmart.payment.v1.PaymentService/{method}");
    let parent_cx = extract_grpc_context(request.metadata());
    let span = info_span!(
        "grpc.server",
        service = "payment",
        rpc.system = "grpc",
        rpc.method = %full_method,
        trace_id = %trace_id,
        otel.kind = "server",
    );
    span.set_parent(parent_cx);
    span
}

#[tonic::async_trait]
impl PaymentServiceTrait for PaymentGrpcServer {
    async fn create_payment(
        &self,
        request: Request<CreatePaymentRequest>,
    ) -> Result<Response<CreatePaymentResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let span = start_grpc_span("CreatePayment", &request, &trace_id);
        let caller = CallerAuth::from_request(&self.auth, &request, "CreatePayment");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_payment_owner(&req.user_id, "CreatePayment")?;

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
                    sales_channel: a.sales_channel,
                    storefront_shop_id: a.storefront_shop_id,
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
                    sales_channel: req.sales_channel,
                    storefront_shop_id: req.storefront_shop_id,
                    trace_id,
                })
                .await
                .map_err(|e| e.to_tonic_status())?;

            Ok(Response::new(CreatePaymentResponse {
                payment: Some(to_proto_payment(created)),
            }))
        }
        .instrument(span)
        .await;

        self.record_metrics("CreatePayment", start, &result);
        result
    }

    async fn get_payment(
        &self,
        request: Request<GetPaymentRequest>,
    ) -> Result<Response<GetPaymentResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let span = start_grpc_span("GetPayment", &request, &trace_id);
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
        .instrument(span)
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
        let span = start_grpc_span("CancelPayment", &request, &trace_id);
        let caller = CallerAuth::from_request(&self.auth, &request, "CancelPayment");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            let payment_id = parse_optional_uuid(&req.payment_id, "payment_id")
                .map_err(|e| e.to_tonic_status())?;
            let order_group_id = non_empty(&req.order_group_id);

            // Ownership check BEFORE cancelling for non-admin callers.
            if !caller.is_admin() {
                if caller.user_id.is_empty() {
                    return Err(Status::permission_denied(
                        "forbidden: missing caller subject for CancelPayment",
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
        .instrument(span)
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
        let span = start_grpc_span("CreateRefund", &request, &trace_id);
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
        .instrument(span)
        .await;

        self.record_metrics("CreateRefund", start, &result);
        result
    }

    async fn get_ledger_summary(
        &self,
        request: Request<GetLedgerSummaryRequest>,
    ) -> Result<Response<GetLedgerSummaryResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let span = start_grpc_span("GetLedgerSummary", &request, &trace_id);
        let caller = CallerAuth::from_request(&self.auth, &request, "GetLedgerSummary");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_admin("GetLedgerSummary")?;

            let channel_filter =
                SalesChannel::parse_filter(&req.sales_channel).map_err(|e| e.to_tonic_status())?;

            let summary = self
                .service
                .get_ledger_summary(channel_filter)
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
                sales_channel: summary.sales_channel,
            }))
        }
        .instrument(span)
        .await;

        self.record_metrics("GetLedgerSummary", start, &result);
        result
    }

    async fn get_seller_balance(
        &self,
        request: Request<GetSellerBalanceRequest>,
    ) -> Result<Response<GetSellerBalanceResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let span = start_grpc_span("GetSellerBalance", &request, &trace_id);
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
        .instrument(span)
        .await;

        self.record_metrics("GetSellerBalance", start, &result);
        result
    }

    async fn release_escrow(
        &self,
        request: Request<ReleaseEscrowRequest>,
    ) -> Result<Response<ReleaseEscrowResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let span = start_grpc_span("ReleaseEscrow", &request, &trace_id);
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
        .instrument(span)
        .await;

        self.record_metrics("ReleaseEscrow", start, &result);
        result
    }

    async fn run_reconciliation(
        &self,
        request: Request<RunReconciliationRequest>,
    ) -> Result<Response<RunReconciliationResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let span = start_grpc_span("RunReconciliation", &request, &trace_id);
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

            let channel_filter =
                SalesChannel::parse_filter(&req.sales_channel).map_err(|e| e.to_tonic_status())?;

            let report = self
                .service
                .run_reconciliation(run_date, channel_filter)
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
                sales_channel: report.run.sales_channel,
                auto_healed_count: report.run.auto_healed_count,
            }))
        }
        .instrument(span)
        .await;

        self.record_metrics("RunReconciliation", start, &result);
        result
    }

    async fn list_reconciliation_reports(
        &self,
        request: Request<ListReconciliationReportsRequest>,
    ) -> Result<Response<ListReconciliationReportsResponse>, Status> {
        let start = Instant::now();
        let trace_id = extract_trace_from_metadata(&request);
        let span = start_grpc_span("ListReconciliationReports", &request, &trace_id);
        let caller = CallerAuth::from_request(&self.auth, &request, "ListReconciliationReports");
        let req = request.into_inner();

        let result = async {
            let caller = caller?;
            caller.require_admin("ListReconciliationReports")?;

            let channel_filter =
                SalesChannel::parse_filter(&req.sales_channel).map_err(|e| e.to_tonic_status())?;

            let rows = self
                .service
                .list_reconciliation_reports(req.limit, channel_filter)
                .await
                .map_err(|e| e.to_tonic_status())?;

            let reports = rows
                .into_iter()
                .map(to_proto_reconciliation_report_summary)
                .collect();

            Ok(Response::new(ListReconciliationReportsResponse { reports }))
        }
        .instrument(span)
        .await;

        self.record_metrics("ListReconciliationReports", start, &result);
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
    let provider_ref = p
        .provider_tx_id
        .clone()
        .unwrap_or_else(|| p.provider_order_code.clone());

    let payment_channel = if p.sales_channel.trim().is_empty() {
        SalesChannel::Marketplace.as_str().to_string()
    } else {
        p.sales_channel
    };

    let shop_allocations = pwa
        .allocations
        .into_iter()
        .map(|a| {
            let alloc_channel = if a.sales_channel.trim().is_empty() {
                SalesChannel::Marketplace.as_str().to_string()
            } else {
                a.sales_channel
            };
            ShopAllocation {
                order_id: a.order_id,
                shop_id: a.shop_id,
                subtotal_vnd: a.subtotal_vnd,
                commission_vnd: a.commission_vnd,
                shipping_fee_vnd: a.shipping_fee_vnd,
                sales_channel: alloc_channel,
                storefront_shop_id: a.storefront_shop_id,
            }
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
        sales_channel: payment_channel,
        storefront_shop_id: p.storefront_shop_id,
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
        sales_channel: item.sales_channel,
        storefront_shop_id: item.storefront_shop_id,
    }
}

fn to_proto_reconciliation_report_summary(
    r: ReconciliationRunRecord,
) -> ReconciliationReportSummary {
    ReconciliationReportSummary {
        run_id: r.id.to_string(),
        provider: r.provider,
        run_date: r.run_date.format("%Y-%m-%d").to_string(),
        sales_channel: r.sales_channel,
        matched_count: r.matched_count,
        missing_local_count: r.missing_local_count,
        missing_provider_count: r.missing_provider_count,
        amount_mismatch_count: r.amount_mismatch_count,
        auto_healed_count: r.auto_healed_count,
        status: r.status,
        created_at: Some(chrono_to_proto(r.created_at)),
        completed_at: r.completed_at.map(chrono_to_proto),
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
    const TEST_SVC_KEY: &str = "grpc-test-service-key";

    fn test_grpc_server() -> (PaymentGrpcServer, Metrics) {
        let cfg = Config::from_env();
        let pool = PgPoolOptions::new()
            .connect_lazy(&cfg.database_url)
            .expect("lazy pool");
        let metrics = Metrics::new();
        let svc = PaymentService::new(pool, cfg, metrics.clone());
        let auth = GrpcAuth::new(TEST_USER_KEY, TEST_SVC_KEY).unwrap();
        (PaymentGrpcServer::new(svc, auth), metrics)
    }

    fn with_service_auth<T>(payload: T) -> Request<T> {
        let tok = crate::auth::sign_hs256(
            TEST_SVC_KEY,
            serde_json::json!({
                "sub": "order-service",
                "role": "admin",
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

    fn with_spoofed_metadata<T>(payload: T) -> Request<T> {
        let mut req = Request::new(payload);
        for (k, v) in [
            ("x-user-id", "attacker"),
            ("x-user-role", "admin"),
            ("x-shop-id", "shop-b"),
        ] {
            req.metadata_mut()
                .insert(k, MetadataValue::try_from(v).unwrap());
        }
        req
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
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
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
                    sales_channel: String::new(),
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
        assert!(prom.contains("method=\"/nexusmart.payment.v1.PaymentService/CreatePayment\""));
    }

    #[tokio::test]
    async fn test_grpc_defense_in_depth_rbac_enforcement() {
        let (server, _) = test_grpc_server();

        // Buyer cannot read global ledger summary
        let err = server
            .get_ledger_summary(with_auth(
                GetLedgerSummaryRequest {
                    sales_channel: String::new(),
                },
                "buyer",
                "u-1",
                "",
            ))
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
                    sales_channel: String::new(),
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

        // Seller with empty shop_id in JWT cannot bypass by sending empty shop_id
        let err = server
            .get_seller_balance(with_auth(
                GetSellerBalanceRequest {
                    shop_id: String::new(),
                },
                "seller",
                "u-2",
                "",
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
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
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
    async fn test_b1_all_rpcs_reject_anonymous_and_spoofed_metadata_and_disallowed_service_tokens()
    {
        let (server, _) = test_grpc_server();

        // Every single RPC MUST reject anonymous callers AND spoofed x-user-* metadata without JWT
        for req in [
            Request::new(CreatePaymentRequest {
                order_group_id: "grp-1".into(),
                order_id: "ord-1".into(),
                user_id: "u-1".into(),
                amount_vnd: 100_000,
                method: PaymentMethod::Cod as i32,
                shop_allocations: vec![],
                expires_at: None,
                idempotency_key: String::new(),
                supersede_previous: false,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
            }),
            with_spoofed_metadata(CreatePaymentRequest {
                order_group_id: "grp-1".into(),
                order_id: "ord-1".into(),
                user_id: "u-1".into(),
                amount_vnd: 100_000,
                method: PaymentMethod::Cod as i32,
                shop_allocations: vec![],
                expires_at: None,
                idempotency_key: String::new(),
                supersede_previous: false,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
            }),
        ] {
            assert_eq!(
                server.create_payment(req).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
        }

        for req in [
            Request::new(GetPaymentRequest {
                payment_id: String::new(),
                order_group_id: "grp-1".into(),
                order_id: String::new(),
            }),
            with_spoofed_metadata(GetPaymentRequest {
                payment_id: String::new(),
                order_group_id: "grp-1".into(),
                order_id: String::new(),
            }),
        ] {
            assert_eq!(
                server.get_payment(req).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
        }

        for req in [
            Request::new(CancelPaymentRequest {
                payment_id: String::new(),
                order_group_id: "grp-1".into(),
                reason: "test".into(),
            }),
            with_spoofed_metadata(CancelPaymentRequest {
                payment_id: String::new(),
                order_group_id: "grp-1".into(),
                reason: "test".into(),
            }),
        ] {
            assert_eq!(
                server.cancel_payment(req).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
        }

        for req in [
            Request::new(CreateRefundRequest {
                payment_id: String::new(),
                order_id: "ord-1".into(),
                amount_vnd: 10_000,
                reason: "test".into(),
                idempotency_key: "k1".into(),
            }),
            with_spoofed_metadata(CreateRefundRequest {
                payment_id: String::new(),
                order_id: "ord-1".into(),
                amount_vnd: 10_000,
                reason: "test".into(),
                idempotency_key: "k1".into(),
            }),
        ] {
            assert_eq!(
                server.create_refund(req).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
        }

        for req in [
            Request::new(GetLedgerSummaryRequest {
                sales_channel: String::new(),
            }),
            with_spoofed_metadata(GetLedgerSummaryRequest {
                sales_channel: String::new(),
            }),
        ] {
            assert_eq!(
                server.get_ledger_summary(req).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
        }

        for req in [
            Request::new(GetSellerBalanceRequest {
                shop_id: "shop-1".into(),
            }),
            with_spoofed_metadata(GetSellerBalanceRequest {
                shop_id: "shop-1".into(),
            }),
        ] {
            assert_eq!(
                server.get_seller_balance(req).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
        }

        for req in [
            Request::new(ReleaseEscrowRequest {
                order_id: "ord-1".into(),
                shop_id: "shop-1".into(),
            }),
            with_spoofed_metadata(ReleaseEscrowRequest {
                order_id: "ord-1".into(),
                shop_id: "shop-1".into(),
            }),
        ] {
            assert_eq!(
                server.release_escrow(req).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
        }

        for req in [
            Request::new(RunReconciliationRequest {
                date: "2026-11-20".into(),
                sales_channel: String::new(),
            }),
            with_spoofed_metadata(RunReconciliationRequest {
                date: "2026-11-20".into(),
                sales_channel: String::new(),
            }),
        ] {
            assert_eq!(
                server.run_reconciliation(req).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
        }

        // Valid service tokens MUST be rejected with PermissionDenied on non-SERVICE_METHODS RPCs
        assert_eq!(
            server
                .create_payment(with_service_auth(CreatePaymentRequest {
                    order_group_id: "grp-1".into(),
                    order_id: "ord-1".into(),
                    user_id: "u-1".into(),
                    amount_vnd: 100_000,
                    method: PaymentMethod::Cod as i32,
                    shop_allocations: vec![],
                    expires_at: None,
                    idempotency_key: String::new(),
                    supersede_previous: false,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                }))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
        assert_eq!(
            server
                .get_ledger_summary(with_service_auth(GetLedgerSummaryRequest {
                    sales_channel: String::new(),
                }))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
        assert_eq!(
            server
                .get_seller_balance(with_service_auth(GetSellerBalanceRequest {
                    shop_id: "shop-1".into(),
                }))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
        assert_eq!(
            server
                .run_reconciliation(with_service_auth(RunReconciliationRequest {
                    date: "2026-11-20".into(),
                    sales_channel: String::new(),
                }))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
    }

    #[tokio::test]
    async fn test_grpc_sales_channel_validation_on_create_summary_and_reconcile() {
        let (server, _) = test_grpc_server();

        // Unknown sales_channel -> InvalidArgument
        let err = server
            .create_payment(with_auth(
                CreatePaymentRequest {
                    order_group_id: "grp-1".into(),
                    order_id: "ord-1".into(),
                    user_id: "u-1".into(),
                    amount_vnd: 100_000,
                    method: PaymentMethod::Cod as i32,
                    shop_allocations: vec![],
                    expires_at: None,
                    idempotency_key: String::new(),
                    supersede_previous: false,
                    sales_channel: "DIRECT".into(),
                    storefront_shop_id: String::new(),
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        // STOREFRONT without storefront_shop_id -> InvalidArgument
        let err = server
            .create_payment(with_auth(
                CreatePaymentRequest {
                    order_group_id: "grp-1".into(),
                    order_id: "ord-1".into(),
                    user_id: "u-1".into(),
                    amount_vnd: 100_000,
                    method: PaymentMethod::Cod as i32,
                    shop_allocations: vec![],
                    expires_at: None,
                    idempotency_key: String::new(),
                    supersede_previous: false,
                    sales_channel: "STOREFRONT".into(),
                    storefront_shop_id: String::new(),
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        // STOREFRONT with cross-shop allocation -> InvalidArgument
        let err = server
            .create_payment(with_auth(
                CreatePaymentRequest {
                    order_group_id: "grp-1".into(),
                    order_id: "ord-1".into(),
                    user_id: "u-1".into(),
                    amount_vnd: 100_000,
                    method: PaymentMethod::Cod as i32,
                    shop_allocations: vec![ShopAllocation {
                        order_id: "sub-1".into(),
                        shop_id: "shop-B".into(),
                        subtotal_vnd: 80_000,
                        commission_vnd: 4_000,
                        shipping_fee_vnd: 20_000,
                        sales_channel: "STOREFRONT".into(),
                        storefront_shop_id: "shop-A".into(),
                    }],
                    expires_at: None,
                    idempotency_key: String::new(),
                    supersede_previous: false,
                    sales_channel: "STOREFRONT".into(),
                    storefront_shop_id: "shop-A".into(),
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        // Invalid sales_channel filter on GetLedgerSummary & RunReconciliation -> InvalidArgument
        let err = server
            .get_ledger_summary(with_auth(
                GetLedgerSummaryRequest {
                    sales_channel: "UNKNOWN".into(),
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);

        let err = server
            .run_reconciliation(with_auth(
                RunReconciliationRequest {
                    date: "2026-11-20".into(),
                    sales_channel: "UNKNOWN".into(),
                },
                "admin",
                "admin-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);
    }

    #[test]
    fn test_to_proto_payment_legacy_default_and_storefront_mapping() {
        let now = Utc::now();
        let pid = Uuid::new_v4();
        let legacy = to_proto_payment(PaymentWithAllocations {
            payment: crate::domain::PaymentRecord {
                id: pid,
                order_group_id: "grp-legacy".into(),
                order_id: "ord-legacy".into(),
                user_id: "u-1".into(),
                method: "COD".into(),
                provider: "cod".into(),
                amount_vnd: 100_000,
                status: "succeeded".into(),
                provider_tx_id: None,
                provider_order_code: "NM0000000001".into(),
                qr_code: None,
                qr_url: None,
                idempotency_key: None,
                expires_at: None,
                paid_at: Some(now),
                trace_id: String::new(),
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                created_at: now,
                updated_at: now,
            },
            allocations: vec![crate::domain::ShopAllocationRecord {
                id: Uuid::new_v4(),
                payment_id: pid,
                order_id: "sub-legacy".into(),
                shop_id: "shop-1".into(),
                subtotal_vnd: 80_000,
                commission_vnd: 4_000,
                shipping_fee_vnd: 20_000,
                escrow_status: "held".into(),
                settled_at: None,
                released_at: None,
                sales_channel: String::new(),
                storefront_shop_id: String::new(),
                created_at: now,
            }],
        });
        assert_eq!(legacy.sales_channel, "MARKETPLACE");
        assert_eq!(legacy.storefront_shop_id, "");
        assert_eq!(legacy.shop_allocations[0].sales_channel, "MARKETPLACE");
        assert_eq!(legacy.shop_allocations[0].storefront_shop_id, "");

        let sf = to_proto_payment(PaymentWithAllocations {
            payment: crate::domain::PaymentRecord {
                id: pid,
                order_group_id: "grp-sf".into(),
                order_id: "ord-sf".into(),
                user_id: "u-1".into(),
                method: "VIETQR".into(),
                provider: "vietqr_mock".into(),
                amount_vnd: 120_000,
                status: "pending".into(),
                provider_tx_id: None,
                provider_order_code: "NM0000000002".into(),
                qr_code: None,
                qr_url: None,
                idempotency_key: None,
                expires_at: None,
                paid_at: None,
                trace_id: String::new(),
                sales_channel: "STOREFRONT".into(),
                storefront_shop_id: "shop-sf".into(),
                created_at: now,
                updated_at: now,
            },
            allocations: vec![crate::domain::ShopAllocationRecord {
                id: Uuid::new_v4(),
                payment_id: pid,
                order_id: "sub-sf".into(),
                shop_id: "shop-sf".into(),
                subtotal_vnd: 100_000,
                commission_vnd: 5_000,
                shipping_fee_vnd: 20_000,
                escrow_status: "held".into(),
                settled_at: None,
                released_at: None,
                sales_channel: "STOREFRONT".into(),
                storefront_shop_id: "shop-sf".into(),
                created_at: now,
            }],
        });
        assert_eq!(sf.sales_channel, "STOREFRONT");
        assert_eq!(sf.storefront_shop_id, "shop-sf");
        assert_eq!(sf.shop_allocations[0].sales_channel, "STOREFRONT");
        assert_eq!(sf.shop_allocations[0].storefront_shop_id, "shop-sf");

        // ZALOPAY mapping & proto helpers roundtrip
        let zp = to_proto_payment(PaymentWithAllocations {
            payment: crate::domain::PaymentRecord {
                id: pid,
                order_group_id: "grp-zp".into(),
                order_id: "ord-zp".into(),
                user_id: "u-zp".into(),
                method: "ZALOPAY".into(),
                provider: "zalopay_mock".into(),
                amount_vnd: 250_000,
                status: "refunded".into(),
                provider_tx_id: Some("ZP-TX-99".into()),
                provider_order_code: "261121_NM1122334455".into(),
                qr_code: Some("ZPTOK123".into()),
                qr_url: Some("https://sbgateway.zalopay.vn/openinapp?order=ZPTOK123".into()),
                idempotency_key: None,
                expires_at: Some(now),
                paid_at: Some(now),
                trace_id: String::new(),
                sales_channel: "MARKETPLACE".into(),
                storefront_shop_id: String::new(),
                created_at: now,
                updated_at: now,
            },
            allocations: vec![],
        });
        assert_eq!(zp.method, PaymentMethod::Zalopay as i32);
        assert_eq!(zp.status, PaymentStatus::Refunded as i32);
        assert_eq!(zp.provider_ref, "ZP-TX-99");
        assert_eq!(zp.qr_code, "ZPTOK123");

        let refund_pb = to_proto_refund(RefundRecord {
            id: Uuid::new_v4(),
            payment_id: pid,
            order_id: "ord-zp".into(),
            amount_vnd: 50_000,
            reason: "return".into(),
            status: "completed".into(),
            idempotency_key: "ref-k1".into(),
            provider_refund_id: Some("zpmock_ref_1".into()),
            created_at: now,
            updated_at: now,
        });
        assert_eq!(refund_pb.status, RefundStatus::Done as i32);
        assert_eq!(refund_pb.amount_vnd, 50_000);

        let item_pb = to_proto_reconciliation_item(ReconciliationItemRecord {
            id: Uuid::new_v4(),
            run_id: Uuid::new_v4(),
            classification: "amount_mismatch".into(),
            payment_id: Some(pid),
            provider_tx_id: Some("ZP-TX-99".into()),
            local_amount_vnd: Some(250_000),
            provider_amount_vnd: Some(240_000),
            details: Some("mismatch".into()),
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            created_at: now,
        });
        assert_eq!(item_pb.status, ReconciliationStatus::AmountMismatch as i32);
        assert_eq!(item_pb.local_amount_vnd, 250_000);
        assert_eq!(item_pb.provider_amount_vnd, 240_000);

        let run_id = Uuid::new_v4();
        let report_pb = to_proto_reconciliation_report_summary(ReconciliationRunRecord {
            id: run_id,
            provider: "vietqr_mock+zalopay_mock".into(),
            run_date: NaiveDate::from_ymd_opt(2026, 11, 21).unwrap(),
            sales_channel: "MARKETPLACE".into(),
            matched_count: 5,
            missing_local_count: 1,
            missing_provider_count: 2,
            amount_mismatch_count: 1,
            auto_healed_count: 3,
            status: "completed".into(),
            created_at: now,
            completed_at: Some(now),
        });
        assert_eq!(report_pb.run_id, run_id.to_string());
        assert_eq!(report_pb.provider, "vietqr_mock+zalopay_mock");
        assert_eq!(report_pb.run_date, "2026-11-21");
        assert_eq!(report_pb.auto_healed_count, 3);
        assert_eq!(report_pb.matched_count, 5);
        assert!(report_pb.created_at.is_some());
        assert!(report_pb.completed_at.is_some());
    }

    #[tokio::test]
    async fn test_grpc_list_reconciliation_reports_rbac_and_zalopay_create_validation() {
        let (server, _) = test_grpc_server();

        // Anonymous & spoofed metadata -> Unauthenticated
        for req in [
            Request::new(ListReconciliationReportsRequest {
                limit: 10,
                sales_channel: String::new(),
            }),
            with_spoofed_metadata(ListReconciliationReportsRequest {
                limit: 10,
                sales_channel: String::new(),
            }),
        ] {
            assert_eq!(
                server
                    .list_reconciliation_reports(req)
                    .await
                    .unwrap_err()
                    .code(),
                tonic::Code::Unauthenticated
            );
        }

        // Buyer, Seller, and Service tokens -> PermissionDenied
        assert_eq!(
            server
                .list_reconciliation_reports(with_auth(
                    ListReconciliationReportsRequest {
                        limit: 10,
                        sales_channel: String::new(),
                    },
                    "buyer",
                    "u-1",
                    "",
                ))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
        assert_eq!(
            server
                .list_reconciliation_reports(with_auth(
                    ListReconciliationReportsRequest {
                        limit: 10,
                        sales_channel: String::new(),
                    },
                    "seller",
                    "u-2",
                    "shop-1",
                ))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
        assert_eq!(
            server
                .list_reconciliation_reports(with_service_auth(ListReconciliationReportsRequest {
                    limit: 10,
                    sales_channel: String::new(),
                }))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );

        // Admin with invalid sales_channel filter -> InvalidArgument
        assert_eq!(
            server
                .list_reconciliation_reports(with_auth(
                    ListReconciliationReportsRequest {
                        limit: 10,
                        sales_channel: "INVALID".into(),
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

        // CreatePayment with PAYMENT_METHOD_ZALOPAY validates positive VND amount before DB
        let err = server
            .create_payment(with_auth(
                CreatePaymentRequest {
                    order_group_id: "grp-zp".into(),
                    order_id: "ord-zp".into(),
                    user_id: "u-1".into(),
                    amount_vnd: 0,
                    method: PaymentMethod::Zalopay as i32,
                    shop_allocations: vec![],
                    expires_at: None,
                    idempotency_key: String::new(),
                    supersede_previous: false,
                    sales_channel: String::new(),
                    storefront_shop_id: String::new(),
                },
                "buyer",
                "u-1",
                "",
            ))
            .await
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::InvalidArgument);
    }
}
