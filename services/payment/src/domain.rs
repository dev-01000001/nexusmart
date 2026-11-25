use std::fmt;

use chrono::{DateTime, FixedOffset, NaiveDate, TimeZone, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::error::{PaymentError, Result};
use crate::telemetry::mask_bank_account;

// =============================================================================
// Internal ZaloPay & Payment Method Constants (Wave 3b-2 Phase 1, not wired to gRPC yet)
// =============================================================================

pub const PAYMENT_METHOD_ZALOPAY: &str = "ZALOPAY";
pub const PROVIDER_ZALOPAY_MOCK: &str = "zalopay_mock";
pub const PROVIDER_ZALOPAY_SANDBOX: &str = "zalopay_sandbox";

pub const DEFAULT_COD_MAX_ORDER_VND: i64 = 5_000_000;
pub const DEFAULT_ONLINE_PAYMENT_EXPIRY_MINUTES: i32 = 15;
pub const ZALOPAY_DEFAULT_EXPIRY_MINUTES: i32 = 15;
pub const ZALOPAY_MIN_EXPIRE_DURATION_SECS: i64 = 300;
pub const ZALOPAY_MAX_EXPIRE_DURATION_SECS: i64 = 2_592_000;
pub const ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS: i64 = 300;

pub const REFUND_PROVIDER_STATUS_PENDING: &str = "pending";
pub const REFUND_PROVIDER_STATUS_PROCESSING: &str = "processing";
pub const REFUND_PROVIDER_STATUS_SUCCEEDED: &str = "succeeded";
pub const REFUND_PROVIDER_STATUS_FAILED: &str = "failed";

pub const PROVIDER_FINAL_STATUS_PAID_CONFIRMED: &str = "paid_confirmed";
pub const PROVIDER_FINAL_STATUS_LATE_REFUND_QUEUED: &str = "late_refund_queued";
pub const PROVIDER_FINAL_STATUS_AMOUNT_MISMATCH: &str = "amount_mismatch";
pub const PROVIDER_FINAL_STATUS_NOT_PAID: &str = "not_paid";
pub const PROVIDER_FINAL_STATUS_MANUAL_REVIEW: &str = "manual_review";

/// Computes bounded exponential backoff between late-payment `/v2/query` retries on terminal
/// unpaid `ZALOPAY` payments (`superseded` / `expired` / `cancelled`):
/// `min(base_secs * 2^(attempts - 1), 3600)` seconds (default `base_secs = 60` -> 1m, 2m, 4m,
/// 8m, 16m, 32m, 60m cap).
pub fn zalopay_late_sweep_backoff(attempts: i32, base_secs: i64) -> chrono::Duration {
    let base = base_secs.max(1);
    let exp = attempts.saturating_sub(1).clamp(0, 10) as u32;
    let factor = 1_i64.checked_shl(exp).unwrap_or(i64::MAX);
    let secs = base.saturating_mul(factor).min(3600);
    chrono::Duration::seconds(secs)
}

/// Final conclusion for a terminal unpaid `ZALOPAY` payment when its bounded post-expiry
/// late-payment sweep window (default 24h) expires without a `return_code = 1` confirmation.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ZaloPayWindowExhaustedDecision {
    /// Final `/v2/query` returned `return_code = 2` (failed) or `return_code = 3` with
    /// `is_processing = false` (unpaid/abandoned by buyer) -> conclude `not_paid` (INFO log,
    /// do NOT enqueue `reconciliation_queue`).
    ConcludeNotPaid,
    /// Final `/v2/query` returned `return_code = 3` with `is_processing = true`, an unexpected
    /// `return_code`, or failed with a transport/network error (`None`) -> escalate to
    /// `reconciliation_queue` (`manual_review`) with ERROR log.
    EscalateManualReview,
}

/// Pure decision function for ZaloPay late-payment sweep window exhaustion derived strictly from
/// the persisted `(provider_last_query_return_code, provider_last_query_is_processing)` tuple.
pub fn decide_zalopay_window_exhausted(
    last_return_code: Option<i32>,
    last_is_processing: Option<bool>,
) -> ZaloPayWindowExhaustedDecision {
    match (last_return_code, last_is_processing) {
        (Some(2), _) | (Some(3), Some(false)) => ZaloPayWindowExhaustedDecision::ConcludeNotPaid,
        _ => ZaloPayWindowExhaustedDecision::EscalateManualReview,
    }
}

/// Returns true when the method identifier represents an online prepaid payment channel.
pub fn is_online_method(method: &str) -> bool {
    matches!(
        method.trim().to_ascii_uppercase().as_str(),
        "VIETQR" | "ZALOPAY"
    )
}

/// Backwards-compatible alias for [`is_online_method`].
pub fn is_online_payment_method_str(method: &str) -> bool {
    is_online_method(method)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "UPPERCASE")]
pub enum PaymentMethod {
    Cod,
    VietQr,
    ZaloPay,
}

impl PaymentMethod {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Cod => "COD",
            Self::VietQr => "VIETQR",
            Self::ZaloPay => "ZALOPAY",
        }
    }

    pub fn is_online(self) -> bool {
        matches!(self, Self::VietQr | Self::ZaloPay)
    }

    pub fn parse(raw: &str) -> Result<Self> {
        match raw.trim().to_ascii_uppercase().as_str() {
            "COD" => Ok(Self::Cod),
            "VIETQR" | "VIET_QR" | "QR" => Ok(Self::VietQr),
            "ZALOPAY" | "ZALO_PAY" | "ZLP" => Ok(Self::ZaloPay),
            other => Err(PaymentError::Validation(format!(
                "unsupported payment method: {other}"
            ))),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum PaymentStatus {
    Pending,
    Succeeded,
    Failed,
    Expired,
    Cancelled,
    Superseded,
    Refunded,
}

impl PaymentStatus {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::Succeeded => "succeeded",
            Self::Failed => "failed",
            Self::Expired => "expired",
            Self::Cancelled => "cancelled",
            Self::Superseded => "superseded",
            Self::Refunded => "refunded",
        }
    }

    pub fn parse(raw: &str) -> Self {
        match raw.trim().to_ascii_lowercase().as_str() {
            "succeeded" | "paid" | "completed" | "success" => Self::Succeeded,
            "failed" => Self::Failed,
            "expired" => Self::Expired,
            "cancelled" | "canceled" => Self::Cancelled,
            "superseded" => Self::Superseded,
            "refunded" => Self::Refunded,
            _ => Self::Pending,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "UPPERCASE")]
pub enum SalesChannel {
    Marketplace,
    Storefront,
}

impl SalesChannel {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Marketplace => "MARKETPLACE",
            Self::Storefront => "STOREFRONT",
        }
    }

    /// Parses a write/attribution channel. Empty defaults to `MARKETPLACE`;
    /// any unknown non-empty value returns `PaymentError::Validation` (gRPC `INVALID_ARGUMENT`).
    pub fn parse_or_default(raw: &str) -> Result<Self> {
        let trimmed = raw.trim();
        if trimmed.is_empty() {
            return Ok(Self::Marketplace);
        }
        match trimmed.to_ascii_uppercase().as_str() {
            "MARKETPLACE" => Ok(Self::Marketplace),
            "STOREFRONT" => Ok(Self::Storefront),
            other => Err(PaymentError::Validation(format!(
                "invalid sales_channel '{other}'; must be MARKETPLACE or STOREFRONT"
            ))),
        }
    }

    /// Parses a read/summary/reconciliation filter channel.
    /// Empty returns `Ok(None)` (unfiltered/all channels).
    pub fn parse_filter(raw: &str) -> Result<Option<Self>> {
        let trimmed = raw.trim();
        if trimmed.is_empty() {
            return Ok(None);
        }
        Self::parse_or_default(trimmed).map(Some)
    }
}

/// Immutable sales channel attribution persisted on payments, shop allocations,
/// and double-entry ledger transactions/entries.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ChannelAttribution {
    pub sales_channel: SalesChannel,
    pub storefront_shop_id: String,
}

impl ChannelAttribution {
    pub fn marketplace() -> Self {
        Self {
            sales_channel: SalesChannel::Marketplace,
            storefront_shop_id: String::new(),
        }
    }

    pub fn storefront(shop_id: impl Into<String>) -> Result<Self> {
        let shop = shop_id.into();
        Self::from_parts("STOREFRONT", &shop)
    }

    /// Validates and constructs an immutable channel attribution from wire/input strings.
    pub fn from_parts(raw_channel: &str, raw_storefront_shop_id: &str) -> Result<Self> {
        let sales_channel = SalesChannel::parse_or_default(raw_channel)?;
        let storefront_shop_id = raw_storefront_shop_id.trim().to_string();

        match sales_channel {
            SalesChannel::Marketplace => {
                if !storefront_shop_id.is_empty() {
                    return Err(PaymentError::Validation(
                        "storefront_shop_id must be empty when sales_channel is MARKETPLACE"
                            .to_string(),
                    ));
                }
            }
            SalesChannel::Storefront => {
                if storefront_shop_id.is_empty() {
                    return Err(PaymentError::Validation(
                        "storefront_shop_id is required when sales_channel is STOREFRONT"
                            .to_string(),
                    ));
                }
            }
        }

        Ok(Self {
            sales_channel,
            storefront_shop_id,
        })
    }

    /// Reconstructs attribution from persisted DB columns, defaulting legacy/empty values to MARKETPLACE.
    pub fn from_db(sales_channel: &str, storefront_shop_id: &str) -> Self {
        match SalesChannel::parse_or_default(sales_channel) {
            Ok(SalesChannel::Storefront) if !storefront_shop_id.trim().is_empty() => Self {
                sales_channel: SalesChannel::Storefront,
                storefront_shop_id: storefront_shop_id.trim().to_string(),
            },
            _ => Self::marketplace(),
        }
    }

    pub fn validate(&self) -> Result<()> {
        Self::from_parts(self.sales_channel.as_str(), &self.storefront_shop_id).map(|_| ())
    }

    /// Ensures every shop allocation matches the payment's sales channel and storefront shop.
    pub fn validate_allocations(&self, allocations: &[ShopAllocationInput]) -> Result<()> {
        for alloc in allocations {
            if !alloc.sales_channel.trim().is_empty() {
                let alloc_channel = SalesChannel::parse_or_default(&alloc.sales_channel)?;
                if alloc_channel != self.sales_channel {
                    return Err(PaymentError::Validation(format!(
                        "allocation sales_channel '{}' conflicts with payment sales_channel '{}'",
                        alloc_channel.as_str(),
                        self.sales_channel.as_str()
                    )));
                }
            }
            let alloc_sf_shop = alloc.storefront_shop_id.trim();
            if !alloc_sf_shop.is_empty() && alloc_sf_shop != self.storefront_shop_id {
                return Err(PaymentError::Validation(format!(
                    "allocation storefront_shop_id '{alloc_sf_shop}' conflicts with payment storefront_shop_id '{}'",
                    self.storefront_shop_id
                )));
            }
            if self.sales_channel == SalesChannel::Storefront
                && alloc.shop_id.trim() != self.storefront_shop_id
            {
                return Err(PaymentError::Validation(format!(
                    "STOREFRONT payment for shop '{}' cannot include allocation for shop '{}'",
                    self.storefront_shop_id, alloc.shop_id
                )));
            }
        }
        Ok(())
    }

    /// Structured JSON metadata stored on `ledger_transactions.metadata` and `ledger_entries.metadata`.
    pub fn metadata_json(&self) -> serde_json::Value {
        serde_json::json!({
            "sales_channel": self.sales_channel.as_str(),
            "storefront_shop_id": self.storefront_shop_id,
        })
    }
}

/// Returns whether a local payment's `sales_channel` matches an optional reconciliation filter.
pub fn reconciliation_channel_matches(
    local_channel: &str,
    channel_filter: Option<SalesChannel>,
) -> bool {
    match channel_filter {
        None => true,
        Some(expected) => SalesChannel::parse_or_default(local_channel)
            .map(|c| c == expected)
            .unwrap_or(false),
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ShopAllocationInput {
    pub order_id: Option<String>,
    pub shop_id: String,
    pub subtotal_vnd: i64,
    pub commission_vnd: i64,
    pub shipping_fee_vnd: i64,
    #[serde(default)]
    pub platform_discount_vnd: i64,
    #[serde(default)]
    pub sales_channel: String,
    #[serde(default)]
    pub storefront_shop_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreatePaymentInput {
    pub order_group_id: String,
    pub order_id: String,
    pub user_id: String,
    pub amount_vnd: i64,
    pub method: PaymentMethod,
    pub shop_allocations: Vec<ShopAllocationInput>,
    pub expires_at: Option<DateTime<Utc>>,
    pub idempotency_key: Option<String>,
    pub supersede_previous: bool,
    #[serde(default)]
    pub sales_channel: String,
    #[serde(default)]
    pub storefront_shop_id: String,
    pub trace_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct PaymentRecord {
    pub id: Uuid,
    pub order_group_id: String,
    pub order_id: String,
    pub user_id: String,
    pub method: String,
    pub provider: String,
    pub amount_vnd: i64,
    pub status: String,
    pub provider_tx_id: Option<String>,
    pub provider_order_code: String,
    pub qr_code: Option<String>,
    pub qr_url: Option<String>,
    #[serde(default)]
    pub pay_url: Option<String>,
    pub idempotency_key: Option<String>,
    pub expires_at: Option<DateTime<Utc>>,
    pub paid_at: Option<DateTime<Utc>>,
    pub trace_id: String,
    pub sales_channel: String,
    pub storefront_shop_id: String,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct ShopAllocationRecord {
    pub id: Uuid,
    pub payment_id: Uuid,
    pub order_id: String,
    pub shop_id: String,
    pub subtotal_vnd: i64,
    pub commission_vnd: i64,
    pub shipping_fee_vnd: i64,
    #[serde(default)]
    pub platform_discount_vnd: i64,
    pub escrow_status: String,
    pub settled_at: Option<DateTime<Utc>>,
    pub released_at: Option<DateTime<Utc>>,
    #[serde(default)]
    pub tracking_code: Option<String>,
    #[serde(default)]
    pub carrier_code: Option<String>,
    #[serde(default)]
    pub order_code: Option<String>,
    #[serde(default)]
    pub cod_reconciled_at: Option<DateTime<Utc>>,
    #[serde(default)]
    pub cod_reconciliation_run_id: Option<Uuid>,
    #[serde(default)]
    pub sales_channel: String,
    #[serde(default)]
    pub storefront_shop_id: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PaymentWithAllocations {
    pub payment: PaymentRecord,
    pub allocations: Vec<ShopAllocationRecord>,
}

fn default_true() -> bool {
    true
}

#[derive(Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct RefundRecord {
    pub id: Uuid,
    pub payment_id: Uuid,
    pub order_id: String,
    pub amount_vnd: i64,
    pub reason: String,
    pub status: String,
    pub idempotency_key: String,
    pub provider_refund_id: Option<String>,
    #[serde(default)]
    pub provider_m_refund_id: Option<String>,
    #[serde(default)]
    pub provider_status: Option<String>,
    #[serde(default)]
    #[sqlx(default)]
    pub provider_lease_token: Option<Uuid>,
    #[serde(default)]
    #[sqlx(default)]
    pub provider_lease_until: Option<DateTime<Utc>>,
    #[serde(default)]
    #[sqlx(default)]
    pub provider_dispatch_attempted: bool,
    #[serde(default)]
    #[sqlx(default)]
    pub created_by_service: bool,
    #[serde(default)]
    pub requested_by: Option<String>,
    #[serde(default)]
    pub approved_by: Option<String>,
    #[serde(default)]
    pub approved_at: Option<DateTime<Utc>>,
    #[serde(default)]
    pub approval_note: Option<String>,
    #[serde(default)]
    pub is_late_payment: bool,
    #[serde(default)]
    pub bank_code: Option<String>,
    #[serde(default, skip_serializing)]
    pub bank_account_number: Option<String>,
    #[serde(default)]
    pub bank_account_masked: Option<String>,
    #[serde(default)]
    pub bank_account_name: Option<String>,
    #[serde(default)]
    pub settled_at: Option<DateTime<Utc>>,
    #[serde(default)]
    pub settlement_note: Option<String>,
    #[serde(default)]
    pub settlement_idempotency_key: Option<String>,
    #[serde(default = "default_true")]
    pub affects_escrow: bool,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

/// Custom Debug redacting full `bank_account_number` so it can never leak into logs.
impl fmt::Debug for RefundRecord {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("RefundRecord")
            .field("id", &self.id)
            .field("payment_id", &self.payment_id)
            .field("order_id", &self.order_id)
            .field("amount_vnd", &self.amount_vnd)
            .field("reason", &self.reason)
            .field("status", &self.status)
            .field("idempotency_key", &self.idempotency_key)
            .field("provider_refund_id", &self.provider_refund_id)
            .field("provider_m_refund_id", &self.provider_m_refund_id)
            .field("provider_status", &self.provider_status)
            .field("requested_by", &self.requested_by)
            .field("approved_by", &self.approved_by)
            .field("approved_at", &self.approved_at)
            .field("approval_note", &self.approval_note)
            .field("is_late_payment", &self.is_late_payment)
            .field("bank_code", &self.bank_code)
            .field(
                "bank_account_number",
                &self.bank_account_number.as_ref().map(|_| "[REDACTED]"),
            )
            .field("bank_account_masked", &self.bank_account_masked)
            .field("bank_account_name", &self.bank_account_name)
            .field("settled_at", &self.settled_at)
            .field("settlement_note", &self.settlement_note)
            .field("affects_escrow", &self.affects_escrow)
            .field("created_at", &self.created_at)
            .field("updated_at", &self.updated_at)
            .finish()
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateRefundInput {
    pub payment_id: Option<Uuid>,
    pub order_id: String,
    pub amount_vnd: i64,
    pub reason: String,
    pub idempotency_key: String,
    pub trace_id: String,
    /// When true, applies the F11 two-person large-refund approval policy (single refund or 24h
    /// per-payment manual refund sum >= threshold enters `pending_approval`) and requires a
    /// non-empty `requested_by`. When false (automatic saga cancel / late payment), `requested_by`
    /// is ignored and approval is bypassed.
    #[serde(default)]
    pub require_approval_if_large: bool,
    /// Maker user ID for manual/return-admin refunds (verified JWT `sub`, or forwarded from
    /// trusted service caller strictly when `require_approval_if_large == true`).
    #[serde(default)]
    pub requested_by: Option<String>,
    /// True strictly when invoked via a verified internal service token (`order-service`).
    #[serde(default)]
    pub created_by_service: bool,
}

#[derive(Debug, Clone)]
pub struct DecideRefundApprovalInput {
    pub refund_id: Uuid,
    pub approve: bool,
    pub note: String,
    pub checker_user_id: String,
    pub trace_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct PaymentMethodConfigRecord {
    pub method: String,
    pub enabled: bool,
    pub min_order_vnd: i64,
    pub max_order_vnd: i64,
    pub expiry_minutes: i32,
    pub version: i64,
    pub note: String,
    pub updated_by: String,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone)]
pub struct UpdatePaymentMethodConfigInput {
    pub method: PaymentMethod,
    pub enabled: bool,
    pub min_order_vnd: i64,
    pub max_order_vnd: i64,
    pub expiry_minutes: i32,
    pub note: String,
    pub expected_version: i64,
    pub updated_by: String,
}

/// Error prefix returned on `FailedPrecondition` when COD shipping fee adjustment is attempted
/// on a non-COD, non-pending, cancelled, or already-collected payment/allocation (G2).
pub const ERR_COD_ALREADY_COLLECTED: &str = "COD_ALREADY_COLLECTED";

#[derive(Debug, Clone)]
pub struct AdjustCodShippingFeeInput {
    pub order_id: String,
    pub new_shipping_fee_vnd: i64,
    pub idempotency_key: String,
    pub trace_id: String,
}

#[derive(Debug, Clone)]
pub struct AdjustCodShippingFeeOutput {
    pub payment: PaymentWithAllocations,
    pub old_shipping_fee_vnd: i64,
    pub new_shipping_fee_vnd: i64,
    pub new_order_payable_vnd: i64,
    pub already_applied: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct CodFeeAdjustmentRecord {
    pub id: Uuid,
    pub idempotency_key: String,
    pub payment_id: Uuid,
    pub order_id: String,
    pub old_shipping_fee_vnd: i64,
    pub new_shipping_fee_vnd: i64,
    pub new_order_payable_vnd: i64,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CodShippingFeeAdjustmentPlan {
    pub allocation_id: Uuid,
    pub old_shipping_fee_vnd: i64,
    pub new_shipping_fee_vnd: i64,
    pub new_order_payable_vnd: i64,
    pub new_payment_amount_vnd: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateRefundOutput {
    pub refund: RefundRecord,
    pub already_existed: bool,
}

#[derive(Debug, Clone)]
pub struct ListRefundsFilter {
    pub caller_user_id: Option<String>,
    pub is_admin: bool,
    pub status: Option<String>,
    pub order_id: Option<String>,
    pub page: i32,
    pub page_size: i32,
}

#[derive(Debug, Clone)]
pub struct ListRefundsResult {
    pub refunds: Vec<RefundRecord>,
    pub total: i64,
}

#[derive(Clone)]
pub struct SubmitRefundBankAccountInput {
    pub refund_id: Uuid,
    pub caller_user_id: String,
    pub bank_code: String,
    pub bank_account_number: String,
    pub bank_account_name: String,
}

impl fmt::Debug for SubmitRefundBankAccountInput {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("SubmitRefundBankAccountInput")
            .field("refund_id", &self.refund_id)
            .field("caller_user_id", &self.caller_user_id)
            .field("bank_code", &self.bank_code)
            .field("bank_account_number", &"[REDACTED]")
            .field("bank_account_name", &self.bank_account_name)
            .finish()
    }
}

#[derive(Debug, Clone)]
pub struct SettleRefundInput {
    pub refund_id: Uuid,
    pub provider_refund_id: String,
    pub note: String,
    pub idempotency_key: String,
    pub trace_id: String,
}

#[derive(Debug, Clone)]
pub struct SettleRefundOutput {
    pub refund: RefundRecord,
    pub already_settled: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReconciliationActionKind {
    Assign,
    Refund,
    Suspend,
}

impl ReconciliationActionKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Assign => "ASSIGN",
            Self::Refund => "REFUND",
            Self::Suspend => "SUSPEND",
        }
    }
}

#[derive(Debug, Clone)]
pub struct ListReconciliationQueueResult {
    pub items: Vec<ReconciliationQueueItem>,
    pub total: i64,
}

#[derive(Debug, Clone)]
pub struct ResolveReconciliationInput {
    pub item_id: Uuid,
    pub action: ReconciliationActionKind,
    pub payment_id: Option<Uuid>,
    pub note: String,
    pub idempotency_key: String,
    pub trace_id: String,
}

#[derive(Debug, Clone)]
pub struct ResolveReconciliationOutput {
    pub item: ReconciliationQueueItem,
    pub refund: Option<RefundRecord>,
    pub already_resolved: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct ReconciliationQueueItem {
    pub id: Uuid,
    pub webhook_event_id: Option<Uuid>,
    pub provider: String,
    pub provider_tx_id: String,
    pub expected_payment_id: Option<Uuid>,
    pub order_ref: Option<String>,
    pub expected_amount_vnd: Option<i64>,
    pub actual_amount_vnd: i64,
    pub reason: String,
    pub resolution_status: String,
    pub notes: Option<String>,
    #[serde(default)]
    pub resolution_idempotency_key: Option<String>,
    #[serde(default)]
    pub resolution_refund_id: Option<Uuid>,
    #[serde(default)]
    pub resolution_action: Option<String>,
    #[serde(default)]
    pub resolution_payment_id: Option<Uuid>,
    #[serde(default)]
    pub occurred_at: Option<DateTime<Utc>>,
    #[serde(default)]
    pub source_type: String,
    #[serde(default)]
    pub tracking_code: String,
    pub created_at: DateTime<Utc>,
    pub resolved_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct ReconciliationRunRecord {
    pub id: Uuid,
    pub provider: String,
    pub run_date: NaiveDate,
    pub sales_channel: String,
    pub matched_count: i32,
    pub missing_local_count: i32,
    pub missing_provider_count: i32,
    pub amount_mismatch_count: i32,
    pub auto_healed_count: i32,
    pub status: String,
    #[serde(default)]
    pub run_type: String,
    #[serde(default)]
    pub file_sha256: String,
    #[serde(default)]
    pub file_name: String,
    #[serde(default)]
    pub total_rows: i32,
    #[serde(default)]
    pub total_matched_vnd: i64,
    #[serde(default)]
    pub already_reconciled_count: i32,
    #[serde(default)]
    pub not_delivered_count: i32,
    #[serde(default)]
    pub created_by: String,
    pub created_at: DateTime<Utc>,
    pub completed_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct ReconciliationItemRecord {
    pub id: Uuid,
    pub run_id: Uuid,
    pub classification: String,
    pub payment_id: Option<Uuid>,
    pub provider_tx_id: Option<String>,
    pub local_amount_vnd: Option<i64>,
    pub provider_amount_vnd: Option<i64>,
    pub details: Option<String>,
    #[serde(default)]
    pub tracking_code: String,
    #[serde(default)]
    pub order_id: String,
    #[serde(default)]
    pub line_number: i32,
    #[serde(default)]
    pub sales_channel: String,
    #[serde(default)]
    pub storefront_shop_id: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReconciliationReport {
    pub run: ReconciliationRunRecord,
    pub items: Vec<ReconciliationItemRecord>,
}

/// Maximum number of CSV rows accepted by a single `ImportCodReconciliation` RPC (E20).
pub const MAX_COD_RECONCILIATION_ROWS: usize = 5000;

#[derive(Debug, Clone)]
pub struct CodReconciliationRowInput {
    pub line_number: i32,
    pub tracking_code: String,
    pub cod_amount_vnd: i64,
    pub delivered_at: String,
    pub order_code: String,
}

#[derive(Debug, Clone)]
pub struct ImportCodReconciliationInput {
    pub carrier_code: String,
    pub file_name: String,
    pub file_sha256: String,
    pub run_date: NaiveDate,
    pub created_by: String,
    pub rows: Vec<CodReconciliationRowInput>,
}

#[derive(Debug, Clone)]
pub struct ListReconciliationRunsFilter {
    pub run_type: Option<String>,
    pub sales_channel: Option<SalesChannel>,
    pub page: i32,
    pub page_size: i32,
}

#[derive(Debug, Clone)]
pub struct ListReconciliationRunsResult {
    pub runs: Vec<ReconciliationRunRecord>,
    pub total: i64,
}

/// Validates and normalizes `ListReconciliationRunsRequest.run_type` filter:
/// allows `""`, `"vietqr"`, or `"cod_file"`.
pub fn parse_reconciliation_run_type_filter(raw: &str) -> Result<Option<String>> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "" => Ok(None),
        "vietqr" => Ok(Some("vietqr".to_string())),
        "cod_file" => Ok(Some("cod_file".to_string())),
        other => Err(PaymentError::Validation(format!(
            "invalid run_type '{other}'; expected '', 'vietqr', or 'cod_file'"
        ))),
    }
}

/// Validates and normalizes `carrier_code` for COD carrier CSV imports (E20).
pub fn normalize_cod_carrier_code(raw: &str) -> Result<String> {
    let norm = raw.trim().to_ascii_lowercase();
    if norm.is_empty() {
        return Err(PaymentError::Validation(
            "carrier_code is required".to_string(),
        ));
    }
    if norm.chars().count() > 64 {
        return Err(PaymentError::Validation(
            "carrier_code must not exceed 64 characters".to_string(),
        ));
    }
    Ok(norm)
}

/// Validates request-level row count bounds for `ImportCodReconciliation` (1..=5000, E20).
pub fn validate_cod_reconciliation_rows(rows: &[CodReconciliationRowInput]) -> Result<()> {
    if rows.is_empty() {
        return Err(PaymentError::Validation(
            "rows must contain at least 1 row".to_string(),
        ));
    }
    if rows.len() > MAX_COD_RECONCILIATION_ROWS {
        return Err(PaymentError::Validation(format!(
            "rows exceeds maximum of {MAX_COD_RECONCILIATION_ROWS} rows (got {})",
            rows.len()
        )));
    }
    Ok(())
}

/// Canonical `provider_tx_id` for a COD reconciliation row, namespaced under `cod:` so it never
/// collides with VietQR bank transfer IDs in `reconciliation_queue` or advisory locks.
pub fn cod_reconcile_provider_tx_id(carrier_code: &str, tracking_code: &str) -> String {
    format!(
        "cod:{}:{}",
        carrier_code.trim().to_ascii_lowercase(),
        tracking_code.trim()
    )
}

/// Disambiguated `provider_tx_id` used only when enqueuing a subsequent `amount_mismatch` for a
/// tracking code whose base `cod:<carrier>:<tracking>` queue row was already finalized (`assigned`).
pub fn cod_reconcile_mismatch_provider_tx_id(
    carrier_code: &str,
    tracking_code: &str,
    actual_amount_vnd: i64,
) -> String {
    format!(
        "cod:{}:{}:mismatch:{actual_amount_vnd}",
        carrier_code.trim().to_ascii_lowercase(),
        tracking_code.trim()
    )
}

/// Pure E20 / E20-bis classification decision for a matched COD allocation set.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CodAllocationMatchDecision {
    /// Delivered/completed, exact amount, not yet reconciled -> post ledger + close open queue.
    MatchAndPost,
    /// Delivered/completed, exact amount, already reconciled -> 0 ledger entries.
    AlreadyReconciled,
    /// Allocation not yet delivered (`awaiting_collection`) or cancelled -> 0 ledger entries, queue.
    NotDelivered { cancelled: bool },
    /// Delivered/completed but `cod_amount_vnd != expected_payable_vnd` (even 1 VND) -> 0 ledger entries, queue.
    AmountMismatch,
}

/// Pure E20 / E20-bis decision function for a located COD allocation group:
/// - `awaiting_collection` or `cancelled` -> `NotDelivered` (never posts ledger).
/// - `cod_amount_vnd != expected_payable_vnd` (checked BEFORE `already_reconciled` per E20-bis so
///   a duplicate tracking code with a wrong amount in the same or later file surfaces as
///   `amount_mismatch`) -> `AmountMismatch`.
/// - `already_reconciled` -> `AlreadyReconciled`.
/// - Otherwise -> `MatchAndPost`.
pub fn decide_cod_allocation_reconciliation(
    escrow_statuses: &[&str],
    expected_payable_vnd: i64,
    cod_amount_vnd: i64,
    all_already_reconciled: bool,
) -> CodAllocationMatchDecision {
    if escrow_statuses.is_empty() {
        return CodAllocationMatchDecision::NotDelivered { cancelled: false };
    }
    if escrow_statuses.contains(&"cancelled") {
        return CodAllocationMatchDecision::NotDelivered { cancelled: true };
    }
    if escrow_statuses
        .iter()
        .any(|s| !matches!(*s, "held" | "pending_release" | "released" | "refunded"))
    {
        return CodAllocationMatchDecision::NotDelivered { cancelled: false };
    }
    if cod_amount_vnd <= 0 || expected_payable_vnd <= 0 || cod_amount_vnd != expected_payable_vnd {
        return CodAllocationMatchDecision::AmountMismatch;
    }
    if all_already_reconciled {
        return CodAllocationMatchDecision::AlreadyReconciled;
    }
    CodAllocationMatchDecision::MatchAndPost
}

/// Checks whether any located COD allocation already records a non-empty `carrier_code` that
/// differs (case-insensitively) from the imported CSV file's `carrier_code`.
/// Allocations with empty/NULL `carrier_code` remain eligible to match.
pub fn find_cod_carrier_mismatch(
    stored_carriers: &[Option<&str>],
    file_carrier_code: &str,
) -> Option<String> {
    let expected = file_carrier_code.trim().to_ascii_lowercase();
    for raw in stored_carriers {
        if let Some(stored) = raw.map(str::trim).filter(|s| !s.is_empty()) {
            let norm = stored.to_ascii_lowercase();
            if norm != expected {
                return Some(norm);
            }
        }
    }
    None
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct SellerPayoutRecord {
    pub id: Uuid,
    pub shop_id: String,
    pub amount_vnd: i64,
    pub bank_account_masked: String,
    pub status: String,
    pub idempotency_key: String,
    pub created_at: DateTime<Utc>,
    pub completed_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PaymentSseEvent {
    pub payment_id: Uuid,
    pub order_group_id: String,
    pub order_id: String,
    pub status: String,
    pub amount_vnd: i64,
    pub provider_tx_id: Option<String>,
    pub updated_at: DateTime<Utc>,
}

/// Standard NexusMart NATS JetStream event envelope (`docs/contracts.md` Section 5).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EventEnvelope {
    pub event_id: String,
    pub event_type: String,
    pub trace_id: String,
    pub occurred_at: DateTime<Utc>,
    pub payload: serde_json::Value,
}

// =============================================================================
// Wave 2b Domain Types & Helpers (Payment Fee Configs, Seller Ledger)
// =============================================================================

/// Maximum configurable payment fee in basis points (20.00%).
pub const MAX_PAYMENT_FEE_BPS: i32 = 2000;
const MAX_FEE_CONFIG_NOTE_LEN: usize = 500;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, sqlx::FromRow)]
pub struct PaymentFeeConfigRecord {
    pub id: Uuid,
    pub seq: i64,
    pub payment_method: String,
    pub fee_bps: i32,
    pub effective_from: DateTime<Utc>,
    pub created_by: String,
    pub note: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone)]
pub struct SetFeeConfigInput {
    pub method: PaymentMethod,
    pub fee_bps: i32,
    pub effective_from: Option<DateTime<Utc>>,
    pub created_by: String,
    pub note: String,
}

#[derive(Debug, Clone)]
pub struct GetFeeConfigFilter {
    pub method: Option<PaymentMethod>,
    pub include_history: bool,
    pub at_time: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone)]
pub struct GetFeeConfigResult {
    pub active_configs: Vec<PaymentFeeConfigRecord>,
    pub history: Vec<PaymentFeeConfigRecord>,
}

/// Validates `SetFeeConfig` parameters against `now` (UC-A09):
/// - `fee_bps` must be in `0..=2000`.
/// - `effective_from` defaults to `now` when omitted; when provided it MUST be `>= now`.
/// - `note` is trimmed and capped at 500 characters.
pub fn validate_set_fee_config(
    fee_bps: i32,
    effective_from: Option<DateTime<Utc>>,
    note: &str,
    now: DateTime<Utc>,
) -> Result<(DateTime<Utc>, String)> {
    if !(0..=MAX_PAYMENT_FEE_BPS).contains(&fee_bps) {
        return Err(PaymentError::Validation(format!(
            "fee_bps must be between 0 and {MAX_PAYMENT_FEE_BPS} (got {fee_bps})"
        )));
    }
    let resolved_effective_from = match effective_from {
        Some(ts) => {
            if ts < now {
                return Err(PaymentError::Validation(
                    "effective_from cannot be in the past".to_string(),
                ));
            }
            ts
        }
        None => now,
    };
    let trimmed_note = note.trim();
    if trimmed_note.chars().count() > MAX_FEE_CONFIG_NOTE_LEN {
        return Err(PaymentError::Validation(format!(
            "note must not exceed {MAX_FEE_CONFIG_NOTE_LEN} characters"
        )));
    }
    Ok((resolved_effective_from, trimmed_note.to_string()))
}

/// Pure reference resolution of active payment fee configs at `at_time`:
/// picks the latest entry per payment method where `effective_from <= at_time`,
/// breaking ties strictly by `(effective_from DESC, seq DESC)`.
#[cfg(test)]
fn resolve_active_fee_configs(
    entries: &[PaymentFeeConfigRecord],
    method_filter: Option<PaymentMethod>,
    at_time: DateTime<Utc>,
) -> Vec<PaymentFeeConfigRecord> {
    let mut best_by_method: std::collections::BTreeMap<&str, &PaymentFeeConfigRecord> =
        std::collections::BTreeMap::new();

    for entry in entries {
        if let Some(m) = method_filter {
            if entry.payment_method != m.as_str() {
                continue;
            }
        }
        if entry.effective_from > at_time {
            continue;
        }
        let key = (entry.effective_from, entry.seq);
        match best_by_method.get(entry.payment_method.as_str()) {
            Some(prev) if (prev.effective_from, prev.seq) >= key => {}
            _ => {
                best_by_method.insert(entry.payment_method.as_str(), entry);
            }
        }
    }

    best_by_method.into_values().cloned().collect()
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SellerLedgerAccountFilter {
    Pending,
    Available,
}

pub fn parse_seller_ledger_account_filter(raw: &str) -> Result<Option<SellerLedgerAccountFilter>> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "" => Ok(None),
        "pending" => Ok(Some(SellerLedgerAccountFilter::Pending)),
        "available" => Ok(Some(SellerLedgerAccountFilter::Available)),
        other => Err(PaymentError::Validation(format!(
            "account_filter must be 'pending', 'available', or empty (got '{other}')"
        ))),
    }
}

#[derive(Debug, Clone)]
pub struct ListSellerLedgerFilter {
    pub shop_id: Option<String>,
    pub account_filter: Option<SellerLedgerAccountFilter>,
    pub reference_type: Option<String>,
    pub page: i32,
    pub page_size: i32,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SellerLedgerEntryRecord {
    pub id: Uuid,
    pub transaction_id: Uuid,
    pub account_code: String,
    pub reference_type: String,
    pub reference_id: String,
    pub description: String,
    pub debit_vnd: i64,
    pub credit_vnd: i64,
    pub delta_vnd: i64,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone)]
pub struct ListSellerLedgerResult {
    pub entries: Vec<SellerLedgerEntryRecord>,
    pub total: i64,
}

/// Formats a human-readable Vietnamese ledger description for seller accounts
/// (`seller_pending:<shop>` / `seller_available:<shop>`) by `reference_type`.
pub fn format_seller_ledger_description(
    reference_type: &str,
    reference_id: &str,
    account_code: &str,
    platform_fee_vnd: i64,
) -> String {
    let ref_id = reference_id.trim();
    match reference_type.trim() {
        "escrow_settle" => {
            if platform_fee_vnd > 0 {
                format!("Tiền đơn hàng {ref_id} (đã trừ phí sàn {platform_fee_vnd} VND)")
            } else {
                format!("Tiền đơn hàng {ref_id}")
            }
        }
        "escrow_release" => {
            if account_code.starts_with("seller_pending:") {
                format!("Giải ngân đơn hàng {ref_id} (chuyển sang số dư khả dụng)")
            } else {
                format!("Giải ngân đơn hàng {ref_id} vào số dư khả dụng")
            }
        }
        "refund" => format!("Hoàn tiền cho giao dịch {ref_id}"),
        "payout" => format!("Rút tiền về tài khoản ngân hàng ({ref_id})"),
        other => format!("Giao dịch {other} ({ref_id})"),
    }
}

/// Validates that an existing payment matched by `idempotency_key` belongs to the exact same
/// caller and parameters (`user_id`, `order_group_id`, `amount_vnd`, `method`).
/// Prevents cross-user IDOR and cross-order payment corruption on `idempotency_key` collisions (review-B P0-2).
pub fn validate_payment_idempotency_match(
    existing: &PaymentRecord,
    user_id: &str,
    order_group_id: &str,
    amount_vnd: i64,
    method: PaymentMethod,
) -> Result<()> {
    if existing.user_id != user_id
        || existing.order_group_id != order_group_id
        || existing.amount_vnd != amount_vnd
        || existing.method != method.as_str()
    {
        return Err(PaymentError::Conflict(
            "idempotency_key already used with different payment parameters".to_string(),
        ));
    }
    Ok(())
}

/// Reserved internal refund `idempotency_key` prefixes that end-user callers MUST NOT use
/// (F11 / p4 Item E): prevents staff from pre-claiming cancel/return/late/reconciliation keys.
pub const RESERVED_INTERNAL_REFUND_IDEMPOTENCY_PREFIXES: &[&str] = &[
    "order-cancel-refund:",
    "order-return-refund:",
    "superseded-late-refund:",
    "late-webhook:",
    "recon-refund:",
    "recon-overpay:",
    "refund-settle:",
];

/// Rejects end-user refund `idempotency_key` values carrying any reserved internal prefix
/// (trimmed, ASCII case-insensitive) — p4 Item E.
pub fn validate_user_refund_idempotency_key(raw: &str) -> Result<()> {
    let normalized = raw.trim().to_ascii_lowercase();
    for prefix in RESERVED_INTERNAL_REFUND_IDEMPOTENCY_PREFIXES {
        if normalized.starts_with(prefix) {
            return Err(PaymentError::Validation(format!(
                "idempotency_key cannot use reserved internal prefix '{prefix}'"
            )));
        }
    }
    Ok(())
}

/// Validates that an existing refund matched by `idempotency_key` belongs to the same target
/// payment/order and amount (review-B P2-8 / review2-P P3-8 / p4 Item E).
///
/// Only `order-cancel-refund:` rows created by a trusted service identity (`created_by_service == true`)
/// skip the `amount_vnd` check so synchronous `OrderService.CancelOrder` and asynchronous
/// `order.cancelled` still deduplicate cleanly after a prior partial refund.
/// `order-return-refund:` and all other keys MUST match `amount_vnd` strictly.
///
/// `accepted_order_aliases` allows single-shop payment replays that pass `order_group_id`
/// (or `payment.order_id`) while the stored refund records `alloc.order_id`.
pub fn validate_refund_idempotency_match(
    existing: &RefundRecord,
    requested_payment_id: Option<Uuid>,
    requested_order_id: &str,
    requested_amount_vnd: i64,
    accepted_order_aliases: &[&str],
) -> Result<()> {
    if let Some(pid) = requested_payment_id {
        if existing.payment_id != pid {
            return Err(PaymentError::Conflict(
                "idempotency_key already used for a different payment".to_string(),
            ));
        }
    }
    let trimmed_order = requested_order_id.trim();
    if !trimmed_order.is_empty()
        && existing.order_id != trimmed_order
        && !accepted_order_aliases.contains(&trimmed_order)
    {
        return Err(PaymentError::Conflict(
            "idempotency_key already used for a different order".to_string(),
        ));
    }
    let skip_amount_check = existing.created_by_service
        && existing
            .idempotency_key
            .trim()
            .to_ascii_lowercase()
            .starts_with("order-cancel-refund:");
    if !skip_amount_check && existing.amount_vnd != requested_amount_vnd {
        return Err(PaymentError::Conflict(
            "idempotency_key already used with different refund amount".to_string(),
        ));
    }
    Ok(())
}

/// Verifies that a `reconciliation_queue` item's `provider` matches the target payment's `method`
/// before `ASSIGN` (p4 Item H):
/// - `zalopay*` providers match ONLY `ZALOPAY`.
/// - `cod*` providers cannot be assigned as online transfers.
/// - All other online providers (`vietqr_mock`, `sepay`, `payos`, etc.) match ONLY `VIETQR`.
pub fn reconciliation_provider_matches_method(provider: &str, method: &str) -> bool {
    let p = provider.trim().to_ascii_lowercase();
    let m = method.trim().to_ascii_uppercase();
    if p.starts_with("zalopay") {
        m == "ZALOPAY"
    } else if p.starts_with("cod") {
        false
    } else {
        m == "VIETQR"
    }
}

/// Defense-in-depth guards for `create_payment(supersede_previous = true)` (F5 / p4 Item K):
/// - Every superseded pending payment MUST be an online method (`VIETQR`/`ZALOPAY`), never `COD`.
/// - `amount_vnd` MUST match the superseded payment's `amount_vnd`.
/// - No allocation on any superseded payment may be in `'held' | 'pending_release' | 'released' | 'refunded'`.
/// - When the replacement payment is also online, its `expires_at` MUST NOT exceed the old payment's `expires_at`.
pub fn validate_supersede_previous_payments(
    old_pending: &[PaymentRecord],
    old_alloc_statuses: &[&str],
    new_method: PaymentMethod,
    new_amount_vnd: i64,
    new_expires_at: Option<DateTime<Utc>>,
) -> Result<()> {
    if old_pending.is_empty() {
        return Err(PaymentError::Conflict(
            "cannot supersede: no pending payment exists for checkout group".to_string(),
        ));
    }
    for old in old_pending {
        if !is_online_method(&old.method) {
            return Err(PaymentError::Conflict(format!(
                "cannot supersede non-online payment {} (method='{}')",
                old.id, old.method
            )));
        }
        if old.amount_vnd != new_amount_vnd {
            return Err(PaymentError::Conflict(format!(
                "cannot supersede payment {}: amount_vnd {} does not match existing {}",
                old.id, new_amount_vnd, old.amount_vnd
            )));
        }
        if is_online_method(new_method.as_str()) {
            if let (Some(old_exp), Some(new_exp)) = (old.expires_at, new_expires_at) {
                if new_exp > old_exp {
                    return Err(PaymentError::FailedPrecondition(format!(
                        "cannot extend payment expiry beyond original deadline when superseding payment {}",
                        old.id
                    )));
                }
            }
        }
    }
    if old_alloc_statuses
        .iter()
        .any(|s| matches!(*s, "held" | "pending_release" | "released" | "refunded"))
    {
        return Err(PaymentError::Conflict(
            "cannot supersede payment whose shop allocation is already held, settled, or refunded"
                .to_string(),
        ));
    }
    Ok(())
}

/// Canonical ingress normalization for `provider_tx_id` (review2 P0-1):
/// trims leading/trailing whitespace and rejects empty/blank identifiers once at the boundary.
pub fn normalize_provider_tx_id(raw: &str) -> Result<String> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Err(PaymentError::Validation(
            "provider_tx_id is required".to_string(),
        ));
    }
    Ok(trimmed.to_string())
}

/// Returns true iff `status` is already a terminal unpaid state so `CancelPayment` can succeed
/// idempotently without stranding order cancel compensation watchdogs (review2 P1-3).
/// Paid states (`succeeded`, `refunded`) return false and remain `409 Conflict`.
pub fn is_payment_already_cancelled_terminal(status: &str) -> bool {
    matches!(status, "cancelled" | "expired" | "failed" | "superseded")
}

/// Orders candidate provider transaction indices for an expired pending payment (review2 P1-2):
/// exact-amount transfers come first (preserving feed order), followed by mismatched transfers,
/// so an earlier wrong-amount transfer never masks a subsequent exact-amount payment.
pub fn order_expiry_candidate_indices(amounts: &[i64], expected_amount_vnd: i64) -> Vec<usize> {
    let mut exact = Vec::new();
    let mut mismatched = Vec::new();
    for (idx, &amt) in amounts.iter().enumerate() {
        if amt == expected_amount_vnd {
            exact.push(idx);
        } else {
            mismatched.push(idx);
        }
    }
    exact.extend(mismatched);
    exact
}

/// Canonical idempotency key for late/duplicate payment refunds across ALL paths
/// (webhook, expiration scanner, and reconciliation) so a single `provider_tx_id`
/// never creates duplicate `refunds` rows (review2-P P1-1).
pub fn late_payment_refund_key(provider_tx_id: &str) -> String {
    format!("late-webhook:{}", provider_tx_id.trim())
}

/// Canonical idempotency key for reconciliation queue `REFUND` keyed by `provider_tx_id`
/// (never by `item.id`) so a single provider transfer can never be refunded twice.
pub fn reconciliation_refund_key(provider_tx_id: &str) -> String {
    format!("recon-refund:{}", provider_tx_id.trim())
}

/// Canonical idempotency key for reconciliation queue `ASSIGN` overpayment excess refund
/// keyed by `provider_tx_id` (never by `item.id`).
pub fn reconciliation_overpay_key(provider_tx_id: &str) -> String {
    format!("recon-overpay:{}", provider_tx_id.trim())
}

/// Unified provider-transfer accounting status across all `gateway_clearing` debit paths (P0-1):
/// a `provider_tx_id` is already accounted for if it appears on `payments.provider_tx_id`,
/// on a finalized (`assigned`/`refunded`) `reconciliation_queue` row, or on a tx-scoped refund
/// (`late-webhook:`, `recon-refund:`, `recon-overpay:`).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct ProviderTxAccountingState {
    pub on_payment: bool,
    pub recon_queue_finalized: bool,
    pub has_tx_refund: bool,
}

impl ProviderTxAccountingState {
    pub fn is_accounted(self) -> bool {
        self.on_payment || self.recon_queue_finalized || self.has_tx_refund
    }
}

/// Rejects reconciliation resolution if `provider_tx_id` has already been accounted for by
/// another finalized queue item, a confirmed payment, or an existing provider-tx refund.
pub fn validate_reconciliation_provider_tx_accountable(
    provider_tx_id: &str,
    state: ProviderTxAccountingState,
) -> Result<()> {
    if state.recon_queue_finalized {
        return Err(PaymentError::Conflict(format!(
            "another reconciliation item for provider_tx_id '{provider_tx_id}' is already finalized"
        )));
    }
    if state.on_payment {
        return Err(PaymentError::Conflict(format!(
            "provider_tx_id '{provider_tx_id}' is already recorded on a payment"
        )));
    }
    if state.has_tx_refund {
        return Err(PaymentError::Conflict(format!(
            "provider_tx_id '{provider_tx_id}' already has an accrued refund"
        )));
    }
    Ok(())
}

/// Computes the maximum VND releasable from `seller_pending` for a `pending_release` allocation
/// without releasing funds reserved for other still-unmatured `pending_release` allocations of
/// the same shop after a post-settlement clawback drained `seller_pending` (review2 P2-4).
pub fn compute_capped_escrow_release_vnd(
    alloc_seller_net_vnd: i64,
    pending_balance_vnd: i64,
    unmatured_hold_vnd: i64,
) -> i64 {
    let releasable_pool = pending_balance_vnd
        .saturating_sub(unmatured_hold_vnd.max(0))
        .max(0);
    alloc_seller_net_vnd.max(0).min(releasable_pool)
}

/// Validates that a `SettleRefund` replay with the same `Idempotency-Key` matches the
/// original `refund_id` and `provider_refund_id` (409 on mismatch).
pub fn validate_settle_refund_idempotency_match(
    existing: &RefundRecord,
    requested_refund_id: Uuid,
    requested_provider_refund_id: &str,
) -> Result<()> {
    if existing.id != requested_refund_id {
        return Err(PaymentError::Conflict(
            "idempotency_key already used to settle a different refund".to_string(),
        ));
    }
    let existing_ref = existing
        .provider_refund_id
        .as_deref()
        .map(str::trim)
        .unwrap_or("");
    if existing_ref != requested_provider_refund_id.trim() {
        return Err(PaymentError::Conflict(
            "idempotency_key already used with a different provider_refund_id".to_string(),
        ));
    }
    Ok(())
}

/// Validates that a `ResolveReconciliationItem` replay with the same `Idempotency-Key` matches
/// the original `item_id`, `action`, and `payment_id` (409 on mismatch).
pub fn validate_resolve_reconciliation_idempotency_match(
    existing: &ReconciliationQueueItem,
    requested_item_id: Uuid,
    requested_action: ReconciliationActionKind,
    requested_payment_id: Option<Uuid>,
) -> Result<()> {
    if existing.id != requested_item_id {
        return Err(PaymentError::Conflict(
            "idempotency_key already used for a different reconciliation item".to_string(),
        ));
    }
    let stored_action = existing.resolution_action.as_deref().unwrap_or(
        match existing.resolution_status.as_str() {
            "assigned" => "ASSIGN",
            "refunded" => "REFUND",
            "suspended" => "SUSPEND",
            _ => "",
        },
    );
    if !stored_action.eq_ignore_ascii_case(requested_action.as_str()) {
        return Err(PaymentError::Conflict(
            "idempotency_key already used with a different reconciliation action".to_string(),
        ));
    }
    if existing.resolution_payment_id != requested_payment_id {
        return Err(PaymentError::Conflict(
            "idempotency_key already used with a different payment_id".to_string(),
        ));
    }
    Ok(())
}

/// Snapshots the buyer's masked bank account into `settlement_note` at settlement time so
/// the audit trail records the exact destination account used without exposing raw digits.
///
/// ponytail: keeps `SettleRefundRequest` proto unchanged (no contract bump); upgrade path is
/// adding an optional `expected_bank_account_masked` or `expected_updated_at` concurrency token
/// to `SettleRefundRequest` so settlement rejects if buyer edited account after admin viewed it.
pub fn format_settlement_note_with_masked_account(
    note: &str,
    bank_code: Option<&str>,
    bank_account_masked: Option<&str>,
) -> Option<String> {
    let trimmed_note = note.trim();
    let masked = bank_account_masked.map(str::trim).filter(|s| !s.is_empty());
    let code = bank_code.map(str::trim).filter(|s| !s.is_empty());

    let snapshot = match (code, masked) {
        (Some(c), Some(m)) => Some(format!("[settled_to: {c} {m}]")),
        (None, Some(m)) => Some(format!("[settled_to: {m}]")),
        _ => None,
    };

    match (trimmed_note.is_empty(), snapshot) {
        (true, None) => None,
        (true, Some(snap)) => Some(snap),
        (false, None) => Some(trimmed_note.to_string()),
        (false, Some(snap)) => Some(format!("{trimmed_note} {snap}")),
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GroupPaymentSelection {
    Empty,
    /// Every visible row in the current Read Committed snapshot is `superseded`,
    /// indicating a concurrent `create_payment(supersede_previous=true)` inserted a
    /// successor intent after the snapshot started (review2-P P1-3).
    AllSupersededRetry,
    Found(usize),
}

/// Selects the authoritative payment index in a locked checkout group:
/// prioritizes `succeeded` > `pending` > newest (`created_at DESC`).
/// Returns `AllSupersededRetry` if every visible row is `superseded`.
pub fn select_payment_for_group_action(group: &[PaymentRecord]) -> GroupPaymentSelection {
    if group.is_empty() {
        return GroupPaymentSelection::Empty;
    }
    if group.iter().all(|p| p.status == "superseded") {
        return GroupPaymentSelection::AllSupersededRetry;
    }
    let best_idx = group
        .iter()
        .enumerate()
        .min_by_key(|(_, p)| {
            let prio = match p.status.as_str() {
                "succeeded" => 1_u8,
                "pending" => 2_u8,
                _ => 3_u8,
            };
            (prio, std::cmp::Reverse(p.created_at))
        })
        .map(|(i, _)| i)
        .expect("non-empty group");
    GroupPaymentSelection::Found(best_idx)
}

/// Defense-in-depth group guard for `create_payment` (R3 / review-B P2-7):
/// - Rejects if the group already has any `succeeded` or `refunded` payment.
/// - When `supersede_previous == true`, also rejects if the group has any terminal unpaid
///   payment (`cancelled`, `expired`, `failed`) or has no active `pending` payment to supersede.
pub fn validate_group_can_create_payment(
    existing_statuses: &[&str],
    supersede_previous: bool,
) -> Result<()> {
    if existing_statuses
        .iter()
        .any(|s| matches!(*s, "succeeded" | "refunded"))
    {
        return Err(PaymentError::Conflict(
            "order already has a succeeded payment".to_string(),
        ));
    }
    if supersede_previous {
        if existing_statuses
            .iter()
            .any(|s| matches!(*s, "cancelled" | "expired" | "failed"))
        {
            return Err(PaymentError::Conflict(
                "cannot supersede payment for a cancelled, expired, or failed order group"
                    .to_string(),
            ));
        }
        if !existing_statuses.contains(&"pending") {
            return Err(PaymentError::Conflict(
                "no pending payment exists in order group to supersede".to_string(),
            ));
        }
    }
    Ok(())
}

/// Validates refund amount against paid amount minus already refunded amount (UC-PAY04).
pub fn validate_refund_cap(
    paid_amount_vnd: i64,
    already_refunded_vnd: i64,
    requested_vnd: i64,
) -> Result<i64> {
    if requested_vnd <= 0 {
        return Err(PaymentError::Validation(
            "refund amount_vnd must be positive".to_string(),
        ));
    }
    if paid_amount_vnd <= 0 || already_refunded_vnd < 0 {
        return Err(PaymentError::Validation(
            "invalid paid or refunded amount state".to_string(),
        ));
    }
    let remaining_vnd = paid_amount_vnd.saturating_sub(already_refunded_vnd);
    if requested_vnd > remaining_vnd {
        return Err(PaymentError::RefundExceedsPaid {
            remaining_vnd,
            requested_vnd,
        });
    }
    Ok(remaining_vnd - requested_vnd)
}

/// Deterministic idempotency key shared by both synchronous `OrderService.CancelOrder`
/// (`CreateRefund`) and asynchronous `order.cancelled` NATS consumer so concurrent or
/// out-of-order cancellation paths never double-refund a sub-order.
pub fn order_cancel_refund_key(order_id: &str) -> String {
    format!("order-cancel-refund:{}", order_id.trim())
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SettlementSplit {
    pub order_id: String,
    pub shop_id: String,
    pub escrow_debit_vnd: i64,
    pub platform_promo_debit_vnd: i64,
    pub seller_net_vnd: i64,
    pub commission_vnd: i64,
    pub shipping_fee_vnd: i64,
}

impl SettlementSplit {
    pub fn total_debit_vnd(&self) -> i64 {
        self.escrow_debit_vnd + self.platform_promo_debit_vnd
    }
}

/// Computes buyer-payable VND for one shop allocation:
/// `P = subtotal_vnd (R) + shipping_fee_vnd (Ship) - platform_discount_vnd (D)`.
pub fn compute_allocation_buyer_payable_vnd(
    subtotal_vnd: i64,
    shipping_fee_vnd: i64,
    platform_discount_vnd: i64,
) -> Result<i64> {
    if subtotal_vnd < 0 || shipping_fee_vnd < 0 || platform_discount_vnd < 0 {
        return Err(PaymentError::Validation(
            "allocation VND amounts must be non-negative".to_string(),
        ));
    }
    let gross = subtotal_vnd.checked_add(shipping_fee_vnd).ok_or_else(|| {
        PaymentError::Validation("overflow computing allocation total".to_string())
    })?;
    if platform_discount_vnd > gross {
        return Err(PaymentError::Validation(format!(
            "platform_discount_vnd ({platform_discount_vnd}) exceeds subtotal_vnd + shipping_fee_vnd ({gross})"
        )));
    }
    Ok(gross - platform_discount_vnd)
}

/// Computes buyer-payable VND from a persisted `ShopAllocationRecord`.
pub fn allocation_buyer_payable_vnd(alloc: &ShopAllocationRecord) -> Result<i64> {
    compute_allocation_buyer_payable_vnd(
        alloc.subtotal_vnd,
        alloc.shipping_fee_vnd,
        alloc.platform_discount_vnd,
    )
}

/// Validates payment amount and shop allocations according to the R1 money model:
/// - `amount_vnd >= 0` (0 VND fully-subsidized orders are valid)
/// - Each allocation has non-negative `R`, `C`, `Ship`, `D`, with `C <= R` and `D <= R + Ship`
/// - When allocations are provided, `sum(R + Ship - D) == amount_vnd`
pub fn validate_allocations_sum(
    amount_vnd: i64,
    allocations: &[ShopAllocationInput],
) -> Result<()> {
    if amount_vnd < 0 {
        return Err(PaymentError::Validation(
            "amount_vnd must be a non-negative integer VND".to_string(),
        ));
    }
    if allocations.is_empty() {
        return Ok(());
    }

    let mut total_payable = 0_i64;
    for alloc in allocations {
        if alloc.shop_id.trim().is_empty() {
            return Err(PaymentError::Validation(
                "shop_id must not be empty".to_string(),
            ));
        }
        if alloc.commission_vnd < 0 {
            return Err(PaymentError::Validation(
                "commission_vnd must be non-negative".to_string(),
            ));
        }
        if alloc.commission_vnd > alloc.subtotal_vnd {
            return Err(PaymentError::Validation(format!(
                "commission_vnd ({}) exceeds subtotal_vnd ({})",
                alloc.commission_vnd, alloc.subtotal_vnd
            )));
        }
        let sub_payable = compute_allocation_buyer_payable_vnd(
            alloc.subtotal_vnd,
            alloc.shipping_fee_vnd,
            alloc.platform_discount_vnd,
        )?;
        total_payable = total_payable
            .checked_add(sub_payable)
            .ok_or_else(|| PaymentError::Validation("overflow summing allocations".to_string()))?;
    }

    if total_payable != amount_vnd {
        return Err(PaymentError::Validation(format!(
            "sum of shop allocations ({total_payable}) does not match payment amount_vnd ({amount_vnd})"
        )));
    }
    Ok(())
}

/// Computes balanced integer-VND escrow settlement split for a sub-order (R1 + R4):
/// - Debits: `escrow_liability = P - refunded`, `platform_promo_expense = D`
/// - Credits: `seller_pending = R - C` minus refunded portion (waterfall: seller -> commission -> shipping)
///
/// Always guarantees `escrow_debit_vnd + platform_promo_debit_vnd == seller_net_vnd + commission_vnd + shipping_fee_vnd`.
pub fn compute_settlement_split(
    order_id: &str,
    shop_id: &str,
    subtotal_vnd: i64,
    commission_vnd: i64,
    shipping_fee_vnd: i64,
    platform_discount_vnd: i64,
    already_refunded_vnd: i64,
) -> Result<SettlementSplit> {
    if shop_id.trim().is_empty() {
        return Err(PaymentError::Validation(
            "shop_id must not be empty".to_string(),
        ));
    }
    if commission_vnd < 0 || already_refunded_vnd < 0 {
        return Err(PaymentError::Validation(
            "allocation VND amounts must be non-negative".to_string(),
        ));
    }
    if commission_vnd > subtotal_vnd {
        return Err(PaymentError::Validation(format!(
            "commission_vnd ({commission_vnd}) exceeds subtotal_vnd ({subtotal_vnd})"
        )));
    }

    let buyer_payable_vnd = compute_allocation_buyer_payable_vnd(
        subtotal_vnd,
        shipping_fee_vnd,
        platform_discount_vnd,
    )?;

    if already_refunded_vnd > buyer_payable_vnd {
        return Err(PaymentError::LedgerInvariant(format!(
            "already_refunded_vnd ({already_refunded_vnd}) exceeds sub-order buyer payable ({buyer_payable_vnd})"
        )));
    }

    let escrow_debit_vnd = buyer_payable_vnd - already_refunded_vnd;
    let raw_seller_net = subtotal_vnd - commission_vnd;

    // Deduct refunded portion from credits in priority order: seller net -> commission -> shipping.
    let seller_deduct = already_refunded_vnd.min(raw_seller_net);
    let seller_net_vnd = raw_seller_net - seller_deduct;
    let rem_after_seller = already_refunded_vnd - seller_deduct;

    let comm_deduct = rem_after_seller.min(commission_vnd);
    let eff_commission_vnd = commission_vnd - comm_deduct;
    let rem_after_comm = rem_after_seller - comm_deduct;

    let ship_deduct = rem_after_comm.min(shipping_fee_vnd);
    let eff_shipping_fee_vnd = shipping_fee_vnd - ship_deduct;

    Ok(SettlementSplit {
        order_id: order_id.to_string(),
        shop_id: shop_id.to_string(),
        escrow_debit_vnd,
        platform_promo_debit_vnd: platform_discount_vnd,
        seller_net_vnd,
        commission_vnd: eff_commission_vnd,
        shipping_fee_vnd: eff_shipping_fee_vnd,
    })
}

/// Initial status and provider creation plan for `create_payment` (R1 0 VND + R2 COD + F1 ZaloPay).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct InitialPaymentPlan {
    pub initial_status: &'static str,
    pub initial_escrow_status: &'static str,
    pub create_qr: bool,
    pub create_zalopay: bool,
    pub mark_paid_now: bool,
}

pub fn plan_initial_payment(method: PaymentMethod, amount_vnd: i64) -> Result<InitialPaymentPlan> {
    if amount_vnd < 0 {
        return Err(PaymentError::Validation(
            "amount_vnd must be a non-negative integer VND".to_string(),
        ));
    }
    if amount_vnd == 0 {
        return Ok(InitialPaymentPlan {
            initial_status: "succeeded",
            initial_escrow_status: "held",
            create_qr: false,
            create_zalopay: false,
            mark_paid_now: true,
        });
    }
    match method {
        PaymentMethod::Cod => Ok(InitialPaymentPlan {
            initial_status: "pending",
            initial_escrow_status: "awaiting_collection",
            create_qr: false,
            create_zalopay: false,
            mark_paid_now: false,
        }),
        PaymentMethod::VietQr => Ok(InitialPaymentPlan {
            initial_status: "pending",
            initial_escrow_status: "awaiting_collection",
            create_qr: true,
            create_zalopay: false,
            mark_paid_now: false,
        }),
        PaymentMethod::ZaloPay => Ok(InitialPaymentPlan {
            initial_status: "pending",
            initial_escrow_status: "awaiting_collection",
            create_qr: false,
            create_zalopay: true,
            mark_paid_now: false,
        }),
    }
}

/// Validates an incoming `UpdatePaymentMethodConfigInput` before acquiring DB locks (F6/F7).
pub fn validate_update_payment_method_config(input: &UpdatePaymentMethodConfigInput) -> Result<()> {
    if input.expected_version < 1 {
        return Err(PaymentError::Validation(
            "expected_version must be >= 1".to_string(),
        ));
    }
    if input.min_order_vnd < 0 {
        return Err(PaymentError::Validation(
            "min_order_vnd must be >= 0".to_string(),
        ));
    }
    if input.max_order_vnd < 0 {
        return Err(PaymentError::Validation(
            "max_order_vnd must be >= 0".to_string(),
        ));
    }
    if input.max_order_vnd > 0 && input.max_order_vnd < input.min_order_vnd {
        return Err(PaymentError::Validation(
            "max_order_vnd must be 0 (unlimited) or >= min_order_vnd".to_string(),
        ));
    }
    match input.method {
        PaymentMethod::Cod => {
            if input.expiry_minutes != 0 {
                return Err(PaymentError::Validation(
                    "COD expiry_minutes must be 0".to_string(),
                ));
            }
        }
        PaymentMethod::VietQr => {
            if !(1..=1440).contains(&input.expiry_minutes) {
                return Err(PaymentError::Validation(
                    "VIETQR expiry_minutes must be between 1 and 1440".to_string(),
                ));
            }
        }
        PaymentMethod::ZaloPay => {
            if !(5..=1440).contains(&input.expiry_minutes) {
                return Err(PaymentError::Validation(
                    "ZALOPAY expiry_minutes must be between 5 and 1440".to_string(),
                ));
            }
        }
    }
    if input.note.chars().count() > 500 {
        return Err(PaymentError::Validation(
            "note must be at most 500 characters".to_string(),
        ));
    }
    Ok(())
}

/// Enforces `payment_method_configs` enabled status and min/max order amount bounds (F6/F7).
/// Zero-payable orders (`amount_vnd == 0`) still require the method to be enabled, while skipping
/// min/max bounds (handled by zero-payable fast path).
pub fn validate_payment_method_eligibility(
    cfg: &PaymentMethodConfigRecord,
    method: PaymentMethod,
    amount_vnd: i64,
) -> Result<()> {
    if !cfg.enabled {
        return Err(PaymentError::FailedPrecondition(format!(
            "PAYMENT_METHOD_DISABLED: payment method {} is currently disabled",
            method.as_str()
        )));
    }
    if amount_vnd <= 0 {
        return Ok(());
    }
    if cfg.min_order_vnd > 0 && amount_vnd < cfg.min_order_vnd {
        return Err(PaymentError::FailedPrecondition(format!(
            "ORDER_AMOUNT_OUT_OF_RANGE: amount {} VND is below minimum {} VND for {}",
            amount_vnd,
            cfg.min_order_vnd,
            method.as_str()
        )));
    }
    if cfg.max_order_vnd > 0 && amount_vnd > cfg.max_order_vnd {
        if method == PaymentMethod::Cod {
            return Err(PaymentError::FailedPrecondition(format!(
                "COD_LIMIT_EXCEEDED: amount {} VND exceeds maximum {} VND for COD",
                amount_vnd, cfg.max_order_vnd
            )));
        }
        return Err(PaymentError::FailedPrecondition(format!(
            "ORDER_AMOUNT_OUT_OF_RANGE: amount {} VND exceeds maximum {} VND for {}",
            amount_vnd,
            cfg.max_order_vnd,
            method.as_str()
        )));
    }
    Ok(())
}

/// Returns true when a manual/return-admin refund requires two-person approval under F11:
/// either this single refund `amount_vnd >= threshold_vnd`, OR the 24-hour rolling manual refund
/// sum on the same `payment_id` (including `pending_approval` + `pending` + `completed` +
/// `manual_review`, excluding `rejected`) plus `amount_vnd` reaches `>= threshold_vnd`.
pub fn should_require_large_refund_approval(
    amount_vnd: i64,
    rolling_24h_manual_sum_vnd: i64,
    threshold_vnd: i64,
) -> bool {
    if threshold_vnd <= 0 || amount_vnd <= 0 {
        return false;
    }
    let effective_rolling = rolling_24h_manual_sum_vnd.max(0);
    amount_vnd >= threshold_vnd || effective_rolling.saturating_add(amount_vnd) >= threshold_vnd
}

/// Validates whether a payment / sub-order allocation has actually collected funds from the buyer
/// and is eligible for refund (R2 COD + R4).
pub fn validate_refund_eligibility(
    method: &str,
    payment_status: &str,
    payment_amount_vnd: i64,
    target_escrow_status: Option<&str>,
) -> Result<()> {
    if payment_amount_vnd <= 0 {
        return Err(PaymentError::Conflict(
            "cannot refund zero-payable payment".to_string(),
        ));
    }
    if method.eq_ignore_ascii_case("COD") {
        match target_escrow_status {
            Some("held" | "pending_release" | "released")
                if matches!(payment_status, "pending" | "succeeded" | "refunded") =>
            {
                return Ok(());
            }
            Some("refunded") => {
                return Err(PaymentError::Conflict(
                    "sub-order allocation is already fully refunded".to_string(),
                ));
            }
            Some(_) => {
                return Err(PaymentError::Conflict(
                    "cannot refund uncollected COD payment".to_string(),
                ));
            }
            None => {
                if matches!(payment_status, "succeeded" | "refunded") {
                    return Ok(());
                }
                return Err(PaymentError::Conflict(
                    "cannot refund uncollected COD payment".to_string(),
                ));
            }
        }
    }

    if !matches!(payment_status, "succeeded" | "refunded") {
        return Err(PaymentError::Conflict(format!(
            "cannot refund payment in status '{payment_status}'"
        )));
    }
    match target_escrow_status {
        Some("awaiting_collection" | "cancelled") => Err(PaymentError::Conflict(
            "cannot refund uncollected or cancelled sub-order allocation".to_string(),
        )),
        Some("refunded") => Err(PaymentError::Conflict(
            "sub-order allocation is already fully refunded".to_string(),
        )),
        _ => Ok(()),
    }
}

/// Computes post-settlement refund clawback split: drains `seller_pending` first, then `seller_available`.
/// Rejects with `InsufficientBalance` (HTTP 409) if combined shop balance is insufficient (R4 / PAY-B2).
pub fn compute_post_settlement_clawback(
    pending_vnd: i64,
    available_vnd: i64,
    requested_vnd: i64,
) -> Result<(i64, i64)> {
    if requested_vnd <= 0 {
        return Err(PaymentError::Validation(
            "refund amount_vnd must be positive".to_string(),
        ));
    }
    let eff_pending = pending_vnd.max(0);
    let eff_available = available_vnd.max(0);
    let total_avail = eff_pending.saturating_add(eff_available);
    if requested_vnd > total_avail {
        return Err(PaymentError::InsufficientBalance {
            available_vnd: total_avail,
            requested_vnd,
        });
    }
    let from_pending = requested_vnd.min(eff_pending);
    let from_available = requested_vnd - from_pending;
    Ok((from_pending, from_available))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReturnAllocationAction {
    /// Cancel uncollected COD on failed delivery (`SHIPPING`) or 0 VND allocation.
    CancelAllocation,
    /// Post-delivery return (`RETURN_REQUESTED`) on uncollected COD (`awaiting_collection`, `P > 0`):
    /// buyer paid cash upon delivery (`order.delivered` may be delayed/DLQ), so collect COD into
    /// `held` idempotently so `order`'s `CreateRefund` succeeds.
    CollectCodForRefund,
    /// Paid collected order (`held`, `P > 0`) or already `refunded`/`cancelled`: no-op in consumer.
    DeferPostDeliveryReturn,
}

/// Decides how `order.returned` affects an allocation (R2 / Wave 2a review fix):
/// - Zero-payable sub-order (`buyer_payable_vnd == 0`) in `held`/`awaiting_collection`: `CancelAllocation`.
/// - Failed delivery (`from_status != RETURN_REQUESTED`) on uncollected COD (`awaiting_collection`): `CancelAllocation`.
/// - Post-delivery return (`from_status == RETURN_REQUESTED`) on uncollected COD (`awaiting_collection`, `P > 0`):
///   `CollectCodForRefund` (collect COD into `held` idempotently so `order`'s `CreateRefund` succeeds).
/// - Paid collected orders (`held`, `P > 0`) or already `refunded`/`cancelled`: `DeferPostDeliveryReturn`.
pub fn decide_return_allocation_action(
    method: &str,
    from_status: Option<&str>,
    escrow_status: &str,
    buyer_payable_vnd: i64,
) -> ReturnAllocationAction {
    if buyer_payable_vnd == 0 && matches!(escrow_status, "held" | "awaiting_collection") {
        return ReturnAllocationAction::CancelAllocation;
    }
    if method.eq_ignore_ascii_case("COD") && escrow_status == "awaiting_collection" {
        let is_post_delivery = from_status
            .map(|s| s.trim().eq_ignore_ascii_case("RETURN_REQUESTED"))
            .unwrap_or(false);
        return if is_post_delivery {
            ReturnAllocationAction::CollectCodForRefund
        } else {
            ReturnAllocationAction::CancelAllocation
        };
    }
    ReturnAllocationAction::DeferPostDeliveryReturn
}

// =============================================================================
// Wave 2a Pure Domain Helpers (Hold Period, Bank Account, Assign Split, VN Date)
// =============================================================================

/// Fixed UTC+07:00 offset for `Asia/Ho_Chi_Minh` (Vietnam has no DST).
pub fn vn_offset() -> FixedOffset {
    FixedOffset::east_opt(7 * 3600).expect("valid +07:00 offset")
}

/// Converts a UTC timestamp to its calendar date in `Asia/Ho_Chi_Minh`.
pub fn utc_to_vn_date(dt: DateTime<Utc>) -> NaiveDate {
    dt.with_timezone(&vn_offset()).date_naive()
}

/// Returns today's calendar date in `Asia/Ho_Chi_Minh` for a given UTC clock reading.
pub fn vn_today(now: DateTime<Utc>) -> NaiveDate {
    utc_to_vn_date(now)
}

/// Converts a Vietnam calendar date (`Asia/Ho_Chi_Minh`) to the half-open UTC interval `[start, end)`.
/// Example: `2026-11-23` -> `[2026-11-22T17:00:00Z, 2026-11-23T17:00:00Z)`.
pub fn vn_day_utc_bounds(vn_date: NaiveDate) -> (DateTime<Utc>, DateTime<Utc>) {
    let vn = vn_offset();
    let start_local = vn_date
        .and_hms_opt(0, 0, 0)
        .expect("00:00:00 is always valid");
    let next_day = vn_date.succ_opt().expect("next date in valid range");
    let end_local = next_day
        .and_hms_opt(0, 0, 0)
        .expect("00:00:00 is always valid");
    let start_utc = vn
        .from_local_datetime(&start_local)
        .single()
        .expect("unambiguous +07:00")
        .with_timezone(&Utc);
    let end_utc = vn
        .from_local_datetime(&end_local)
        .single()
        .expect("unambiguous +07:00")
        .with_timezone(&Utc);
    (start_utc, end_utc)
}

/// Parses and validates an optional `YYYY-MM-DD` reconciliation date against `Asia/Ho_Chi_Minh` today.
/// Empty input defaults to VN today; invalid format or future date returns `Validation` (400).
pub fn parse_and_validate_reconciliation_date(raw: &str, now: DateTime<Utc>) -> Result<NaiveDate> {
    let today_vn = vn_today(now);
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Ok(today_vn);
    }
    let parsed = NaiveDate::parse_from_str(trimmed, "%Y-%m-%d").map_err(|_| {
        PaymentError::Validation("date must be formatted as YYYY-MM-DD".to_string())
    })?;
    if parsed.format("%Y-%m-%d").to_string() != trimmed {
        return Err(PaymentError::Validation(
            "date must be formatted as YYYY-MM-DD".to_string(),
        ));
    }
    if parsed > today_vn {
        return Err(PaymentError::Validation(format!(
            "reconciliation date {parsed} cannot be in the future (today in Asia/Ho_Chi_Minh is {today_vn})"
        )));
    }
    Ok(parsed)
}

/// Validates buyer refund receiving bank account details (PAY-B11 / UC-PAY04).
/// - `bank_code`: non-empty, 2..=32 ASCII alphanumeric / `_` / `-` / space
/// - `bank_account_number`: strictly ASCII digits, 6..=19 characters
/// - `bank_account_name`: non-empty, 2..=120 chars
///
/// Returns `(bank_code, bank_account_number, bank_account_masked, bank_account_name)`.
pub fn validate_refund_bank_account(
    bank_code: &str,
    account_number: &str,
    account_name: &str,
) -> Result<(String, String, String, String)> {
    let code = bank_code.trim();
    if code.len() < 2
        || code.len() > 32
        || !code
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_' | ' '))
    {
        return Err(PaymentError::Validation(
            "bank_code must be 2-32 ASCII alphanumeric characters".to_string(),
        ));
    }

    let number = account_number.trim();
    if !(6..=19).contains(&number.len()) || !number.chars().all(|c| c.is_ascii_digit()) {
        return Err(PaymentError::Validation(
            "bank_account_number must contain 6 to 19 ASCII digits".to_string(),
        ));
    }

    let name = account_name.trim();
    let name_len = name.chars().count();
    if !(2..=120).contains(&name_len) {
        return Err(PaymentError::Validation(
            "bank_account_name must be between 2 and 120 characters".to_string(),
        ));
    }

    let masked = mask_bank_account(number);
    Ok((
        code.to_string(),
        number.to_string(),
        masked,
        name.to_string(),
    ))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AssignAmountSplit {
    Exact { confirm_vnd: i64 },
    Overpaid { confirm_vnd: i64, excess_vnd: i64 },
}

/// Splits a mismatched transfer amount when resolving a reconciliation queue item with `ASSIGN` (PAY-B9):
/// - `actual < expected` -> `409 Conflict` (underpayment cannot confirm order)
/// - `actual == expected` -> `Exact { confirm_vnd }`
/// - `actual > expected` -> `Overpaid { confirm_vnd, excess_vnd }` (confirm + pending refund for excess)
pub fn compute_reconciliation_assign_split(
    expected_amount_vnd: i64,
    actual_amount_vnd: i64,
) -> Result<AssignAmountSplit> {
    if expected_amount_vnd <= 0 || actual_amount_vnd <= 0 {
        return Err(PaymentError::Validation(
            "expected and actual amounts must be positive VND".to_string(),
        ));
    }
    if actual_amount_vnd < expected_amount_vnd {
        return Err(PaymentError::Conflict(format!(
            "underpayment cannot be assigned: actual_vnd={actual_amount_vnd} < expected_vnd={expected_amount_vnd}"
        )));
    }
    if actual_amount_vnd == expected_amount_vnd {
        Ok(AssignAmountSplit::Exact {
            confirm_vnd: expected_amount_vnd,
        })
    } else {
        Ok(AssignAmountSplit::Overpaid {
            confirm_vnd: expected_amount_vnd,
            excess_vnd: actual_amount_vnd - expected_amount_vnd,
        })
    }
}

/// Computes parent COD payment status from allocation counts (R2 multi-shop COD / review-B P2-9):
/// - Any allocation still awaiting collection -> `"pending"`
/// - Zero awaiting and at least one non-refunded collected allocation -> `"succeeded"`
/// - Zero awaiting, zero active collected, and at least one refunded allocation -> `"refunded"`
/// - Zero awaiting, zero collected, zero refunded (all cancelled) -> `"cancelled"`
pub fn resolve_cod_payment_status(
    remaining_awaiting: i64,
    collected_unrefunded: i64,
    refunded_count: i64,
) -> &'static str {
    if remaining_awaiting > 0 {
        "pending"
    } else if collected_unrefunded > 0 {
        "succeeded"
    } else if refunded_count > 0 {
        "refunded"
    } else {
        "cancelled"
    }
}

/// Validates refund amount against BOTH the payment-wide cap (`paid - total_refunded`)
/// AND (when `order_id` maps to a sub-order allocation in a multi-shop checkout) the
/// sub-order's own allocation cap (`suborder_alloc_vnd - suborder_already_refunded_vnd`),
/// so one sub-order can never consume sibling shops' escrow.
///
/// Returns `(payment_remaining_after_vnd, suborder_remaining_after_vnd)`.
pub fn validate_suborder_refund_cap(
    paid_amount_vnd: i64,
    payment_already_refunded_vnd: i64,
    suborder_alloc_vnd: Option<i64>,
    suborder_already_refunded_vnd: i64,
    requested_vnd: i64,
) -> Result<(i64, Option<i64>)> {
    let payment_remaining_after =
        validate_refund_cap(paid_amount_vnd, payment_already_refunded_vnd, requested_vnd)?;

    if let Some(alloc_total) = suborder_alloc_vnd {
        let suborder_remaining = alloc_total
            .max(0)
            .saturating_sub(suborder_already_refunded_vnd.max(0));
        if requested_vnd > suborder_remaining {
            return Err(PaymentError::RefundExceedsPaid {
                remaining_vnd: suborder_remaining,
                requested_vnd,
            });
        }
        return Ok((
            payment_remaining_after,
            Some(suborder_remaining - requested_vnd),
        ));
    }

    Ok((payment_remaining_after, None))
}

/// Computes the safe integer VND refund amount for cancelling a single sub-order
/// inside a (potentially multi-shop) checkout payment.
/// - If the payment has shop allocations: refunds ONLY the cancelled sub-order's allocation
///   total (capped at `payment_amount_vnd - already_refunded_vnd`), never sibling shops.
/// - If the payment has NO shop allocations (legacy/single-order): refunds remaining balance.
pub fn compute_suborder_refund_amount(
    payment_amount_vnd: i64,
    already_refunded_vnd: i64,
    payment_has_allocations: bool,
    cancelled_suborder_alloc_vnd: i64,
) -> i64 {
    let remaining = payment_amount_vnd.saturating_sub(already_refunded_vnd.max(0));
    if remaining <= 0 {
        return 0;
    }
    if payment_has_allocations {
        cancelled_suborder_alloc_vnd.max(0).min(remaining)
    } else {
        remaining
    }
}

/// Classifies a reconciliation pair between local payment state and provider transaction (UC-PAY07).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReconciliationDecision {
    Matched,
    AutoHealAndMatch,
    LatePaymentRefund,
    MissingLocal,
    MissingProvider,
    AmountMismatch,
}

pub fn classify_reconciliation_pair(
    local_status_and_amount: Option<(&str, i64)>,
    provider_amount_vnd: Option<i64>,
) -> ReconciliationDecision {
    match (local_status_and_amount, provider_amount_vnd) {
        (None, Some(_)) => ReconciliationDecision::MissingLocal,
        (Some(_), None) => ReconciliationDecision::MissingProvider,
        (Some((status, local_amt)), Some(prov_amt)) => {
            if local_amt != prov_amt {
                ReconciliationDecision::AmountMismatch
            } else if status == "pending" {
                ReconciliationDecision::AutoHealAndMatch
            } else if matches!(status, "cancelled" | "expired" | "failed" | "superseded") {
                ReconciliationDecision::LatePaymentRefund
            } else {
                ReconciliationDecision::Matched
            }
        }
        (None, None) => ReconciliationDecision::Matched,
    }
}

/// Decides reconciliation action once the payment group is locked and sibling/allocation state is loaded
/// (review-B P1-4 / P0-3, review2-P P2-6).
pub fn decide_reconciliation_match_action(
    local_status: &str,
    local_amount_vnd: i64,
    provider_amount_vnd: i64,
    local_provider_tx_id: Option<&str>,
    provider_tx_id: &str,
    order_already_succeeded: bool,
    has_cancelled_allocation: bool,
) -> ReconciliationDecision {
    match classify_reconciliation_pair(
        Some((local_status, local_amount_vnd)),
        Some(provider_amount_vnd),
    ) {
        ReconciliationDecision::AutoHealAndMatch
            if order_already_succeeded || has_cancelled_allocation =>
        {
            ReconciliationDecision::LatePaymentRefund
        }
        ReconciliationDecision::Matched
            if matches!(local_status, "succeeded" | "refunded")
                && local_provider_tx_id != Some(provider_tx_id) =>
        {
            ReconciliationDecision::LatePaymentRefund
        }
        other => other,
    }
}

pub fn validate_adjust_cod_shipping_fee_input<'a>(
    order_id: &'a str,
    idempotency_key: &'a str,
    new_shipping_fee_vnd: i64,
) -> Result<(&'a str, &'a str)> {
    let order_id = order_id.trim();
    if order_id.is_empty() {
        return Err(PaymentError::Validation("order_id is required".to_string()));
    }
    let idempotency_key = idempotency_key.trim();
    if idempotency_key.is_empty() {
        return Err(PaymentError::Validation(
            "idempotency_key is required".to_string(),
        ));
    }
    if new_shipping_fee_vnd < 0 {
        return Err(PaymentError::Validation(
            "new_shipping_fee_vnd must be non-negative".to_string(),
        ));
    }
    Ok((order_id, idempotency_key))
}

pub fn validate_cod_fee_adjustment_replay(
    existing: &CodFeeAdjustmentRecord,
    order_id: &str,
    new_shipping_fee_vnd: i64,
) -> Result<()> {
    if existing.order_id != order_id || existing.new_shipping_fee_vnd != new_shipping_fee_vnd {
        return Err(PaymentError::FailedPrecondition(format!(
            "idempotency_key '{}' was already used with different parameters",
            existing.idempotency_key
        )));
    }
    Ok(())
}

pub fn plan_cod_shipping_fee_adjustment(
    payment: &PaymentRecord,
    allocations: &[ShopAllocationRecord],
    order_id: &str,
    new_shipping_fee_vnd: i64,
) -> Result<CodShippingFeeAdjustmentPlan> {
    if new_shipping_fee_vnd < 0 {
        return Err(PaymentError::Validation(
            "new_shipping_fee_vnd must be non-negative".to_string(),
        ));
    }
    if payment.method != "COD" {
        return Err(PaymentError::FailedPrecondition(format!(
            "{ERR_COD_ALREADY_COLLECTED}: payment method {} does not support COD shipping fee adjustment",
            payment.method
        )));
    }
    if payment.status != "pending" {
        return Err(PaymentError::FailedPrecondition(format!(
            "{ERR_COD_ALREADY_COLLECTED}: payment status is {} (must be pending)",
            payment.status
        )));
    }

    let mut matched_alloc: Option<&ShopAllocationRecord> = None;
    for alloc in allocations {
        if alloc.order_id == order_id {
            if matched_alloc.is_some() {
                return Err(PaymentError::FailedPrecondition(format!(
                    "multiple COD allocations found for order {order_id}"
                )));
            }
            matched_alloc = Some(alloc);
        }
    }
    let target = matched_alloc.ok_or_else(|| {
        PaymentError::NotFound(format!("COD allocation not found for order {order_id}"))
    })?;

    if target.escrow_status != "awaiting_collection" {
        return Err(PaymentError::FailedPrecondition(format!(
            "{ERR_COD_ALREADY_COLLECTED}: allocation escrow_status is {} (must be awaiting_collection)",
            target.escrow_status
        )));
    }

    let gross = target
        .subtotal_vnd
        .checked_add(new_shipping_fee_vnd)
        .ok_or_else(|| {
            PaymentError::FailedPrecondition("overflow computing new_order_payable_vnd".to_string())
        })?;
    if target.platform_discount_vnd > gross {
        return Err(PaymentError::FailedPrecondition(format!(
            "new_order_payable_vnd must be non-negative: subtotal ({}) + new_shipping_fee ({}) - platform_discount ({}) < 0",
            target.subtotal_vnd, new_shipping_fee_vnd, target.platform_discount_vnd
        )));
    }
    let new_order_payable_vnd = gross - target.platform_discount_vnd;

    let mut new_payment_amount_vnd = 0_i64;
    for alloc in allocations {
        if alloc.escrow_status == "cancelled" {
            continue;
        }
        let alloc_payable = if alloc.id == target.id {
            new_order_payable_vnd
        } else {
            allocation_buyer_payable_vnd(alloc)?
        };
        new_payment_amount_vnd = new_payment_amount_vnd
            .checked_add(alloc_payable)
            .ok_or_else(|| {
                PaymentError::FailedPrecondition(
                    "overflow computing parent payment amount_vnd".to_string(),
                )
            })?;
    }
    if new_payment_amount_vnd < 0 {
        return Err(PaymentError::FailedPrecondition(
            "new payment amount_vnd must be non-negative".to_string(),
        ));
    }

    Ok(CodShippingFeeAdjustmentPlan {
        allocation_id: target.id,
        old_shipping_fee_vnd: target.shipping_fee_vnd,
        new_shipping_fee_vnd,
        new_order_payable_vnd,
        new_payment_amount_vnd,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_multi_shop_single_suborder_cancel_refund_scope() {
        // 2-shop VietQR payment: total 335,000 VND (Shop 1 = 220,000, Shop 2 = 115,000)
        // Cancelling Shop 1 refunds ONLY 220,000 VND, leaving 115,000 VND for Shop 2!
        let shop1_refund = compute_suborder_refund_amount(335_000, 0, true, 220_000);
        assert_eq!(shop1_refund, 220_000);

        // Re-cancelling Shop 1 when its allocation is already refunded (0 held) -> 0 refund
        assert_eq!(compute_suborder_refund_amount(335_000, 220_000, true, 0), 0);

        // If Shop 2 later cancels too, refunds remaining 115,000 VND
        let shop2_refund = compute_suborder_refund_amount(335_000, 220_000, true, 115_000);
        assert_eq!(shop2_refund, 115_000);

        // Platform voucher reduced buyer payment to 300,000 VND (nominal subtotals 220k + 115k):
        // Shop 1 refund is 220,000; Shop 2 later cancel is capped to remaining 80,000 VND.
        assert_eq!(
            compute_suborder_refund_amount(300_000, 0, true, 220_000),
            220_000
        );
        assert_eq!(
            compute_suborder_refund_amount(300_000, 220_000, true, 115_000),
            80_000
        );

        // Single-order payment with no allocations -> refunds full remaining
        assert_eq!(
            compute_suborder_refund_amount(150_000, 0, false, 0),
            150_000
        );
    }

    #[test]
    fn test_per_suborder_refund_cap_prevents_multi_shop_double_refund() {
        // Multi-shop payment: Shop A = 200,000 VND, Shop B = 300,000 VND (Total = 500,000 VND)
        // 1. First cancel refund for Shop A (200k) succeeds -> payment remaining 300k, suborder remaining 0
        let (pay_rem, sub_rem) =
            validate_suborder_refund_cap(500_000, 0, Some(200_000), 0, 200_000).unwrap();
        assert_eq!(pay_rem, 300_000);
        assert_eq!(sub_rem, Some(0));

        // 2. Second refund attempt for Shop A (even 1 VND or 200k) MUST FAIL despite 300k remaining for Shop B
        let dup_err =
            validate_suborder_refund_cap(500_000, 200_000, Some(200_000), 200_000, 200_000)
                .unwrap_err();
        assert!(matches!(
            dup_err,
            PaymentError::RefundExceedsPaid {
                remaining_vnd: 0,
                requested_vnd: 200_000
            }
        ));

        // 3. Partial refunds on Shop B (100k + 200k) succeed, then 1 VND more fails
        let (pay_rem_b1, sub_rem_b1) =
            validate_suborder_refund_cap(500_000, 200_000, Some(300_000), 0, 100_000).unwrap();
        assert_eq!(pay_rem_b1, 200_000);
        assert_eq!(sub_rem_b1, Some(200_000));

        let (pay_rem_b2, sub_rem_b2) =
            validate_suborder_refund_cap(500_000, 300_000, Some(300_000), 100_000, 200_000)
                .unwrap();
        assert_eq!(pay_rem_b2, 0);
        assert_eq!(sub_rem_b2, Some(0));

        assert!(validate_suborder_refund_cap(500_000, 500_000, Some(300_000), 300_000, 1).is_err());

        // 4. Deterministic key matches OrderService.CancelOrder ("order-cancel-refund:<order_id>")
        assert_eq!(
            order_cancel_refund_key("ord-shop-A"),
            "order-cancel-refund:ord-shop-A"
        );
    }

    #[test]
    fn test_reconciliation_classifications() {
        assert_eq!(
            classify_reconciliation_pair(Some(("succeeded", 250_000)), Some(250_000)),
            ReconciliationDecision::Matched
        );
        assert_eq!(
            classify_reconciliation_pair(Some(("pending", 250_000)), Some(250_000)),
            ReconciliationDecision::AutoHealAndMatch
        );
        assert_eq!(
            classify_reconciliation_pair(Some(("succeeded", 250_000)), Some(240_000)),
            ReconciliationDecision::AmountMismatch
        );
        assert_eq!(
            classify_reconciliation_pair(None, Some(250_000)),
            ReconciliationDecision::MissingLocal
        );
        assert_eq!(
            classify_reconciliation_pair(Some(("succeeded", 250_000)), None),
            ReconciliationDecision::MissingProvider
        );
    }

    #[test]
    fn test_p1_4_reconciliation_terminal_unpaid_payment_is_not_matched() {
        for status in ["cancelled", "expired", "failed", "superseded"] {
            assert_eq!(
                classify_reconciliation_pair(Some((status, 250_000)), Some(250_000)),
                ReconciliationDecision::LatePaymentRefund,
                "status={status} with provider payment must trigger late payment refund, not Matched"
            );
            assert_eq!(
                decide_reconciliation_match_action(
                    status, 250_000, 250_000, None, "TX-1", false, false
                ),
                ReconciliationDecision::LatePaymentRefund
            );
        }
        // Pending payment with sibling already succeeded or cancelled allocation -> LatePaymentRefund
        assert_eq!(
            decide_reconciliation_match_action(
                "pending", 250_000, 250_000, None, "TX-1", true, false
            ),
            ReconciliationDecision::LatePaymentRefund
        );
        assert_eq!(
            decide_reconciliation_match_action(
                "pending", 250_000, 250_000, None, "TX-1", false, true
            ),
            ReconciliationDecision::LatePaymentRefund
        );
        assert_eq!(
            decide_reconciliation_match_action(
                "pending", 250_000, 250_000, None, "TX-1", false, false
            ),
            ReconciliationDecision::AutoHealAndMatch
        );
    }

    #[test]
    fn test_review2_p2_6_reconciliation_duplicate_provider_tx_triggers_late_refund() {
        // Same provider_tx_id on succeeded/refunded payment -> Matched
        assert_eq!(
            decide_reconciliation_match_action(
                "succeeded",
                250_000,
                250_000,
                Some("TX-1"),
                "TX-1",
                false,
                false
            ),
            ReconciliationDecision::Matched
        );
        assert_eq!(
            decide_reconciliation_match_action(
                "refunded",
                250_000,
                250_000,
                Some("TX-1"),
                "TX-1",
                false,
                false
            ),
            ReconciliationDecision::Matched
        );
        // Different provider_tx_id on already-succeeded or refunded payment -> LatePaymentRefund
        assert_eq!(
            decide_reconciliation_match_action(
                "succeeded",
                250_000,
                250_000,
                Some("TX-1"),
                "TX-2",
                false,
                false
            ),
            ReconciliationDecision::LatePaymentRefund
        );
        assert_eq!(
            decide_reconciliation_match_action(
                "refunded",
                250_000,
                250_000,
                Some("TX-1"),
                "TX-2",
                false,
                false
            ),
            ReconciliationDecision::LatePaymentRefund
        );
        assert_eq!(
            decide_reconciliation_match_action(
                "succeeded",
                250_000,
                250_000,
                None,
                "TX-2",
                false,
                false
            ),
            ReconciliationDecision::LatePaymentRefund
        );
    }

    #[test]
    fn test_refund_cap_enforced() {
        assert_eq!(validate_refund_cap(500_000, 0, 500_000).unwrap(), 0);
        assert_eq!(validate_refund_cap(500_000, 0, 200_000).unwrap(), 300_000);
        assert_eq!(validate_refund_cap(500_000, 200_000, 300_000).unwrap(), 0);
        let err = validate_refund_cap(500_000, 300_000, 200_001).unwrap_err();
        assert!(matches!(
            err,
            PaymentError::RefundExceedsPaid {
                remaining_vnd: 200_000,
                requested_vnd: 200_001
            }
        ));
        assert!(validate_refund_cap(500_000, 0, 0).is_err());
        assert!(validate_refund_cap(500_000, 0, -1000).is_err());
    }

    #[test]
    fn test_zero_suborder_cap_blocks_refund_from_sibling_escrow() {
        // Multi-shop payment where Shop A had P=0 (100% subsidized) and Shop B had P=500,000.
        // Refunding Shop A MUST fail with RefundExceedsPaid(remaining=0), not draw from Shop B.
        let err = validate_suborder_refund_cap(500_000, 0, Some(0), 0, 10_000).unwrap_err();
        assert!(matches!(
            err,
            PaymentError::RefundExceedsPaid {
                remaining_vnd: 0,
                requested_vnd: 10_000
            }
        ));
    }

    #[test]
    fn test_validate_allocations_sum_with_platform_discount() {
        // R1: G=229,000, voucher san D=30,000, C=11,450, Ship=26,000 => P=225,000.
        let alloc = ShopAllocationInput {
            order_id: Some("ord-1".into()),
            shop_id: "shop-1".into(),
            subtotal_vnd: 229_000,
            commission_vnd: 11_450,
            shipping_fee_vnd: 26_000,
            platform_discount_vnd: 30_000,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
        };
        assert!(validate_allocations_sum(225_000, std::slice::from_ref(&alloc)).is_ok());
        // Sum mismatch (ignoring D) MUST fail
        assert!(validate_allocations_sum(255_000, std::slice::from_ref(&alloc)).is_err());

        // Multi-shop sum with platform discounts
        let alloc2 = ShopAllocationInput {
            order_id: Some("ord-2".into()),
            shop_id: "shop-2".into(),
            subtotal_vnd: 200_000,
            commission_vnd: 10_000,
            shipping_fee_vnd: 20_000,
            platform_discount_vnd: 220_000, // 0 VND sub-order
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
        };
        assert!(validate_allocations_sum(225_000, &[alloc, alloc2.clone()]).is_ok());

        // 0 VND full order passes validation
        assert!(validate_allocations_sum(0, &[alloc2]).is_ok());

        // D > R + Ship rejected
        let bad_d = ShopAllocationInput {
            order_id: Some("ord-bad".into()),
            shop_id: "shop-1".into(),
            subtotal_vnd: 100_000,
            commission_vnd: 5_000,
            shipping_fee_vnd: 10_000,
            platform_discount_vnd: 110_001,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
        };
        assert!(validate_allocations_sum(0, &[bad_d]).is_err());
        assert!(validate_allocations_sum(-1, &[]).is_err());
    }

    #[test]
    fn test_settlement_split_with_platform_discount_and_partial_refund() {
        // Example from w1-payment.md: G=229,000, voucher san=30,000, commission=11,450, Ship=0 -> seller=217,550, promo=30,000
        let s = compute_settlement_split("ord-1", "shop-1", 229_000, 11_450, 0, 30_000, 0).unwrap();
        assert_eq!(s.escrow_debit_vnd, 199_000);
        assert_eq!(s.platform_promo_debit_vnd, 30_000);
        assert_eq!(s.seller_net_vnd, 217_550);
        assert_eq!(s.commission_vnd, 11_450);
        assert_eq!(s.shipping_fee_vnd, 0);
        assert_eq!(
            s.escrow_debit_vnd + s.platform_promo_debit_vnd,
            s.seller_net_vnd + s.commission_vnd + s.shipping_fee_vnd
        );

        // Partial refund 10,000 on P=463,000 (R=429,000, C=21,450, Ship=34,000, D=0)
        let p = compute_settlement_split("ord-2", "shop-2", 429_000, 21_450, 34_000, 0, 10_000)
            .unwrap();
        assert_eq!(p.escrow_debit_vnd, 453_000);
        assert_eq!(p.seller_net_vnd, 397_550);
        assert_eq!(p.commission_vnd, 21_450);
        assert_eq!(p.shipping_fee_vnd, 34_000);
        assert_eq!(
            p.escrow_debit_vnd + p.platform_promo_debit_vnd,
            p.seller_net_vnd + p.commission_vnd + p.shipping_fee_vnd
        );

        // 0 VND fully subsidized order (P=0, R=200,000, C=10,000, Ship=20,000, D=220,000)
        let z = compute_settlement_split("ord-0", "shop-0", 200_000, 10_000, 20_000, 220_000, 0)
            .unwrap();
        assert_eq!(z.escrow_debit_vnd, 0);
        assert_eq!(z.platform_promo_debit_vnd, 220_000);
        assert_eq!(z.seller_net_vnd, 190_000);
        assert_eq!(z.commission_vnd, 10_000);
        assert_eq!(z.shipping_fee_vnd, 20_000);
    }

    #[test]
    fn test_initial_payment_plan_zero_cod_and_vietqr() {
        // Amount 0 -> succeeded immediately, no QR, allocation held for settlement
        let zero_qr = plan_initial_payment(PaymentMethod::VietQr, 0).unwrap();
        assert_eq!(zero_qr.initial_status, "succeeded");
        assert_eq!(zero_qr.initial_escrow_status, "held");
        assert!(!zero_qr.create_qr);
        assert!(zero_qr.mark_paid_now);

        let zero_cod = plan_initial_payment(PaymentMethod::Cod, 0).unwrap();
        assert_eq!(zero_cod.initial_status, "succeeded");
        assert_eq!(zero_cod.initial_escrow_status, "held");
        assert!(!zero_cod.create_qr);

        // COD > 0 -> pending, awaiting_collection, no QR, NOT marked paid
        let cod = plan_initial_payment(PaymentMethod::Cod, 250_000).unwrap();
        assert_eq!(cod.initial_status, "pending");
        assert_eq!(cod.initial_escrow_status, "awaiting_collection");
        assert!(!cod.create_qr);
        assert!(!cod.mark_paid_now);

        // VietQR > 0 -> pending, awaiting_collection, creates QR
        let qr = plan_initial_payment(PaymentMethod::VietQr, 250_000).unwrap();
        assert_eq!(qr.initial_status, "pending");
        assert_eq!(qr.initial_escrow_status, "awaiting_collection");
        assert!(qr.create_qr);
        assert!(!qr.mark_paid_now);
    }

    #[test]
    fn test_refund_eligibility_blocks_uncollected_cod_and_zero_payment() {
        // Uncollected COD (pending or cancelled) -> Conflict (409)
        assert!(matches!(
            validate_refund_eligibility("COD", "pending", 250_000, Some("awaiting_collection")),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_refund_eligibility("COD", "cancelled", 250_000, Some("cancelled")),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_refund_eligibility("COD", "pending", 250_000, None),
            Err(PaymentError::Conflict(_))
        ));

        // Delivered COD sub-order (even while sibling is still awaiting_collection -> payment pending) -> allowed
        assert!(validate_refund_eligibility("COD", "pending", 500_000, Some("held")).is_ok());
        assert!(validate_refund_eligibility("COD", "succeeded", 250_000, Some("released")).is_ok());

        // 0 VND payment -> Conflict (buyer paid 0)
        assert!(matches!(
            validate_refund_eligibility("VIETQR", "succeeded", 0, Some("held")),
            Err(PaymentError::Conflict(_))
        ));

        // Pending/cancelled VietQR -> Conflict
        assert!(matches!(
            validate_refund_eligibility("VIETQR", "pending", 250_000, Some("awaiting_collection")),
            Err(PaymentError::Conflict(_))
        ));
        assert!(validate_refund_eligibility("VIETQR", "succeeded", 250_000, Some("held")).is_ok());
    }

    #[test]
    fn test_post_settlement_clawback_drains_pending_then_available_or_rejects() {
        // Pending covers full refund
        assert_eq!(
            compute_post_settlement_clawback(100_000, 50_000, 60_000).unwrap(),
            (60_000, 0)
        );
        // Pending drained first, remainder from available
        assert_eq!(
            compute_post_settlement_clawback(40_000, 100_000, 75_000).unwrap(),
            (40_000, 35_000)
        );
        // Pending is 0 -> full amount from available
        assert_eq!(
            compute_post_settlement_clawback(0, 217_550, 5_000).unwrap(),
            (0, 5_000)
        );
        // Insufficient combined balance -> 409 InsufficientBalance
        let err = compute_post_settlement_clawback(10_000, 20_000, 30_001).unwrap_err();
        assert!(matches!(
            err,
            PaymentError::InsufficientBalance {
                available_vnd: 30_000,
                requested_vnd: 30_001
            }
        ));
    }

    #[test]
    fn test_return_allocation_and_cod_group_status_transitions() {
        // Failed delivery (SHIPPING) on awaiting_collection COD -> cancel allocation
        assert_eq!(
            decide_return_allocation_action(
                "COD",
                Some("SHIPPING"),
                "awaiting_collection",
                250_000
            ),
            ReturnAllocationAction::CancelAllocation
        );
        // P2-6: Failed delivery (SHIPPING) on 0 VND held order (COD and VietQR) -> cancel allocation
        assert_eq!(
            decide_return_allocation_action("COD", Some("SHIPPING"), "held", 0),
            ReturnAllocationAction::CancelAllocation
        );
        assert_eq!(
            decide_return_allocation_action("VIETQR", Some("SHIPPING"), "held", 0),
            ReturnAllocationAction::CancelAllocation
        );
        // Post-delivery return (RETURN_REQUESTED):
        // - Paid collected order (P > 0, held) -> handled by order's CreateRefund (DeferPostDeliveryReturn)
        // - 0 VND held order -> CancelAllocation (no CreateRefund called by order)
        // - Uncollected COD (awaiting_collection) -> CancelAllocation
        assert_eq!(
            decide_return_allocation_action("COD", Some("RETURN_REQUESTED"), "held", 250_000),
            ReturnAllocationAction::DeferPostDeliveryReturn
        );
        assert_eq!(
            decide_return_allocation_action("VIETQR", Some("RETURN_REQUESTED"), "held", 250_000),
            ReturnAllocationAction::DeferPostDeliveryReturn
        );
        assert_eq!(
            decide_return_allocation_action("VIETQR", Some("RETURN_REQUESTED"), "held", 0),
            ReturnAllocationAction::CancelAllocation
        );
        assert_eq!(
            decide_return_allocation_action("COD", Some("RETURN_REQUESTED"), "held", 0),
            ReturnAllocationAction::CancelAllocation
        );
        assert_eq!(
            decide_return_allocation_action(
                "COD",
                Some("RETURN_REQUESTED"),
                "awaiting_collection",
                250_000
            ),
            ReturnAllocationAction::CollectCodForRefund,
            "RETURN_REQUESTED on P > 0 uncollected COD must collect COD for refund, NOT cancel"
        );
        assert_eq!(
            decide_return_allocation_action(
                "COD",
                Some("RETURN_REQUESTED"),
                "awaiting_collection",
                0
            ),
            ReturnAllocationAction::CancelAllocation
        );
        // Already refunded allocation on RETURN_REQUESTED -> no-op (no double booking)
        assert_eq!(
            decide_return_allocation_action(
                "VIETQR",
                Some("RETURN_REQUESTED"),
                "refunded",
                250_000
            ),
            ReturnAllocationAction::DeferPostDeliveryReturn
        );
        // Paid VietQR > 0 on SHIPPING -> deferred (needs Wave 2 refund flow, not silent cancel)
        assert_eq!(
            decide_return_allocation_action("VIETQR", Some("SHIPPING"), "held", 100_000),
            ReturnAllocationAction::DeferPostDeliveryReturn
        );

        // Multi-shop COD parent payment status resolution (R2 + P2-9)
        assert_eq!(resolve_cod_payment_status(1, 0, 0), "pending");
        assert_eq!(resolve_cod_payment_status(1, 1, 0), "pending");
        assert_eq!(resolve_cod_payment_status(1, 0, 1), "pending");
        assert_eq!(resolve_cod_payment_status(0, 1, 0), "succeeded");
        assert_eq!(resolve_cod_payment_status(0, 1, 1), "succeeded");
        assert_eq!(resolve_cod_payment_status(0, 0, 1), "refunded");
        assert_eq!(resolve_cod_payment_status(0, 0, 0), "cancelled");
    }

    #[test]
    fn test_p0_2_payment_idempotency_match_rejects_cross_user_or_param_mismatch() {
        let now = chrono::Utc::now();
        let existing = PaymentRecord {
            id: Uuid::new_v4(),
            order_group_id: "grp-a".into(),
            order_id: "ord-a".into(),
            user_id: "user-a".into(),
            method: "VIETQR".into(),
            provider: "mock".into(),
            amount_vnd: 250_000,
            status: "pending".into(),
            provider_tx_id: None,
            provider_order_code: "NM123".into(),
            qr_code: None,
            qr_url: None,
            pay_url: None,
            idempotency_key: Some("retry-1".into()),
            expires_at: None,
            paid_at: None,
            trace_id: String::new(),
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            created_at: now,
            updated_at: now,
        };
        assert!(validate_payment_idempotency_match(
            &existing,
            "user-a",
            "grp-a",
            250_000,
            PaymentMethod::VietQr
        )
        .is_ok());
        // Replaying the same idempotency_key when the persisted payment is already terminal
        // (`expired` / `cancelled` / `succeeded`) must still match so `create_payment` returns
        // the existing row with its real status instead of resurrecting or failing.
        for terminal_status in ["expired", "cancelled", "succeeded"] {
            let mut terminal = existing.clone();
            terminal.status = terminal_status.into();
            assert!(
                validate_payment_idempotency_match(
                    &terminal,
                    "user-a",
                    "grp-a",
                    250_000,
                    PaymentMethod::VietQr
                )
                .is_ok(),
                "status={terminal_status} must replay existing row"
            );
        }
        assert!(matches!(
            validate_payment_idempotency_match(
                &existing,
                "user-b",
                "grp-a",
                250_000,
                PaymentMethod::VietQr
            ),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_payment_idempotency_match(
                &existing,
                "user-a",
                "grp-b",
                250_000,
                PaymentMethod::VietQr
            ),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_payment_idempotency_match(
                &existing,
                "user-a",
                "grp-a",
                300_000,
                PaymentMethod::VietQr
            ),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_payment_idempotency_match(
                &existing,
                "user-a",
                "grp-a",
                250_000,
                PaymentMethod::Cod
            ),
            Err(PaymentError::Conflict(_))
        ));
    }

    #[test]
    fn test_p2_7_validate_group_can_create_payment_guards_supersede() {
        // Fresh group creation allowed
        assert!(validate_group_can_create_payment(&[], false).is_ok());
        // Already succeeded/refunded always blocked
        assert!(matches!(
            validate_group_can_create_payment(&["succeeded"], false),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_group_can_create_payment(&["refunded"], true),
            Err(PaymentError::Conflict(_))
        ));
        // supersede_previous=true requires an active pending payment and no terminal cancelled/expired/failed
        assert!(validate_group_can_create_payment(&["pending"], true).is_ok());
        assert!(validate_group_can_create_payment(&["superseded", "pending"], true).is_ok());
        assert!(matches!(
            validate_group_can_create_payment(&["cancelled"], true),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_group_can_create_payment(&["expired"], true),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_group_can_create_payment(&["failed"], true),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_group_can_create_payment(&["superseded"], true),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_group_can_create_payment(&[], true),
            Err(PaymentError::Conflict(_))
        ));
    }

    #[test]
    fn test_p2_8_refund_idempotency_match_checks_target_and_preserves_cancel_dedup() {
        let now = chrono::Utc::now();
        let pid1 = Uuid::new_v4();
        let pid2 = Uuid::new_v4();
        let admin_refund = RefundRecord {
            id: Uuid::new_v4(),
            payment_id: pid1,
            order_id: "ord-1".into(),
            amount_vnd: 50_000,
            reason: "admin_partial".into(),
            status: "pending".into(),
            idempotency_key: "admin-key-1".into(),
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
            bank_code: None,
            bank_account_number: None,
            bank_account_masked: None,
            bank_account_name: None,
            settled_at: None,
            settlement_note: None,
            settlement_idempotency_key: None,
            affects_escrow: true,
            created_at: now,
            updated_at: now,
        };
        assert!(
            validate_refund_idempotency_match(&admin_refund, Some(pid1), "ord-1", 50_000, &[])
                .is_ok()
        );
        // review2-P P3-8: single-shop replay passing order_group_id alias is accepted
        assert!(validate_refund_idempotency_match(
            &admin_refund,
            Some(pid1),
            "grp-1",
            50_000,
            &["grp-1"]
        )
        .is_ok());
        // Unrelated order still rejected even when aliases are provided
        assert!(matches!(
            validate_refund_idempotency_match(
                &admin_refund,
                Some(pid1),
                "ord-other",
                50_000,
                &["grp-1"]
            ),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_refund_idempotency_match(&admin_refund, Some(pid2), "ord-1", 50_000, &[]),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_refund_idempotency_match(&admin_refund, Some(pid1), "ord-2", 50_000, &[]),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_refund_idempotency_match(&admin_refund, Some(pid1), "ord-1", 60_000, &[]),
            Err(PaymentError::Conflict(_))
        ));

        // Cancel-order dedup key allows differing requested amount ONLY when created_by_service == true (p4 Item E)
        let cancel_refund_service = RefundRecord {
            idempotency_key: order_cancel_refund_key("ord-1"),
            amount_vnd: 190_000,
            created_by_service: true,
            ..admin_refund.clone()
        };
        assert!(validate_refund_idempotency_match(
            &cancel_refund_service,
            Some(pid1),
            "ord-1",
            200_000,
            &[]
        )
        .is_ok());
        // Non-service record with cancel prefix must STILL enforce exact amount
        let cancel_refund_user = RefundRecord {
            created_by_service: false,
            ..cancel_refund_service.clone()
        };
        assert!(matches!(
            validate_refund_idempotency_match(
                &cancel_refund_user,
                Some(pid1),
                "ord-1",
                200_000,
                &[]
            ),
            Err(PaymentError::Conflict(_))
        ));
        // order-return-refund: MUST enforce exact amount even when created_by_service == true
        let return_refund_service = RefundRecord {
            idempotency_key: "order-return-refund:ord-1".into(),
            amount_vnd: 190_000,
            created_by_service: true,
            ..admin_refund
        };
        assert!(matches!(
            validate_refund_idempotency_match(
                &return_refund_service,
                Some(pid1),
                "ord-1",
                200_000,
                &[]
            ),
            Err(PaymentError::Conflict(_))
        ));
        // Still rejects wrong order/payment on cancel key
        assert!(matches!(
            validate_refund_idempotency_match(
                &cancel_refund_service,
                Some(pid1),
                "ord-2",
                190_000,
                &[]
            ),
            Err(PaymentError::Conflict(_))
        ));

        // p4 Item E user key validation & Item H provider-method matching
        assert!(validate_user_refund_idempotency_key("admin-refund-42").is_ok());
        for bad in [
            "order-cancel-refund:o1",
            "  ORDER-RETURN-REFUND:o1",
            "superseded-late-refund:p:o",
            "Late-Webhook:tx",
            "recon-refund:tx",
            "recon-overpay:tx",
            "refund-settle:r1",
        ] {
            assert!(validate_user_refund_idempotency_key(bad).is_err(), "{bad}");
        }
        assert!(reconciliation_provider_matches_method(
            "zalopay_mock",
            "ZALOPAY"
        ));
        assert!(!reconciliation_provider_matches_method(
            "zalopay_mock",
            "VIETQR"
        ));
        assert!(reconciliation_provider_matches_method(
            "vietqr_mock",
            "VIETQR"
        ));
        assert!(!reconciliation_provider_matches_method(
            "vietqr_mock",
            "ZALOPAY"
        ));
        assert!(!reconciliation_provider_matches_method("cod_ghn", "COD"));
    }

    #[test]
    fn test_review2_p1_1_and_p1_3_late_refund_key_and_group_selection() {
        // P1-1: single canonical key format matching migration 0002 `late-` prefix
        assert_eq!(late_payment_refund_key("TX-999"), "late-webhook:TX-999");
        assert_eq!(late_payment_refund_key("  TX-999  "), "late-webhook:TX-999");

        // P1-3: group selection detects superseded-only EPQ snapshots
        assert_eq!(
            select_payment_for_group_action(&[]),
            GroupPaymentSelection::Empty
        );
        let now = chrono::Utc::now();
        let make_rec = |status: &str, offset_secs: i64| PaymentRecord {
            id: Uuid::new_v4(),
            order_group_id: "grp-1".into(),
            order_id: "grp-1".into(),
            user_id: "u1".into(),
            method: "VIETQR".into(),
            provider: "mock".into(),
            amount_vnd: 150_000,
            status: status.into(),
            provider_tx_id: None,
            provider_order_code: "NM1".into(),
            qr_code: None,
            qr_url: None,
            pay_url: None,
            idempotency_key: None,
            expires_at: None,
            paid_at: None,
            trace_id: String::new(),
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            created_at: now + chrono::Duration::seconds(offset_secs),
            updated_at: now + chrono::Duration::seconds(offset_secs),
        };
        assert_eq!(
            select_payment_for_group_action(&[make_rec("superseded", 0)]),
            GroupPaymentSelection::AllSupersededRetry
        );
        assert_eq!(
            select_payment_for_group_action(&[
                make_rec("superseded", 0),
                make_rec("superseded", 1)
            ]),
            GroupPaymentSelection::AllSupersededRetry
        );
        // Once successor pending P2 is visible, selects P2
        assert_eq!(
            select_payment_for_group_action(&[make_rec("superseded", 0), make_rec("pending", 1)]),
            GroupPaymentSelection::Found(1)
        );
        // Succeeded beats pending and superseded
        assert_eq!(
            select_payment_for_group_action(&[
                make_rec("superseded", 0),
                make_rec("succeeded", 1),
                make_rec("pending", 2)
            ]),
            GroupPaymentSelection::Found(1)
        );
    }

    #[test]
    fn test_w2_reconciliation_assign_split_exact_overpaid_underpaid() {
        assert_eq!(
            compute_reconciliation_assign_split(250_000, 250_000).unwrap(),
            AssignAmountSplit::Exact {
                confirm_vnd: 250_000
            }
        );
        assert_eq!(
            compute_reconciliation_assign_split(250_000, 275_000).unwrap(),
            AssignAmountSplit::Overpaid {
                confirm_vnd: 250_000,
                excess_vnd: 25_000
            }
        );
        // Underpayment -> 409 Conflict
        assert!(matches!(
            compute_reconciliation_assign_split(250_000, 249_999),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            compute_reconciliation_assign_split(250_000, 100_000),
            Err(PaymentError::Conflict(_))
        ));
        // Non-positive amounts -> 400 Validation
        assert!(matches!(
            compute_reconciliation_assign_split(0, 100_000),
            Err(PaymentError::Validation(_))
        ));
        assert!(matches!(
            compute_reconciliation_assign_split(100_000, 0),
            Err(PaymentError::Validation(_))
        ));
    }

    #[test]
    fn test_w2_capped_escrow_release_protects_unmatured_holds() {
        // Review P2-4 scenario:
        // ord-1 (100k) matured after clawback drained pending to 0, then ord-2 (100k, unmatured)
        // settled so pending = 100k. Releasing ord-1 MUST yield 0 so ord-2 stays held 72h.
        assert_eq!(
            compute_capped_escrow_release_vnd(100_000, 100_000, 100_000),
            0
        );
        // Partial clawback (40k): pending=160k, ord-2 unmatured=100k -> ord-1 can release 60k.
        assert_eq!(
            compute_capped_escrow_release_vnd(100_000, 160_000, 100_000),
            60_000
        );
        // Normal case (no clawback): pending=200k, ord-2 unmatured=100k -> ord-1 releases full 100k.
        assert_eq!(
            compute_capped_escrow_release_vnd(100_000, 200_000, 100_000),
            100_000
        );
        // Once ord-2 matures (unmatured=0), full pending=100k releases.
        assert_eq!(
            compute_capped_escrow_release_vnd(100_000, 100_000, 0),
            100_000
        );
        // Negative / underflow guard
        assert_eq!(
            compute_capped_escrow_release_vnd(100_000, 50_000, 120_000),
            0
        );
        assert_eq!(compute_capped_escrow_release_vnd(100_000, -10_000, 0), 0);
    }

    #[test]
    fn test_w2_refund_bank_account_validation_and_redaction() {
        // Valid 6-digit and 19-digit account numbers
        let (code, num, masked, name) =
            validate_refund_bank_account(" VCB ", " 123456 ", " NGUYEN VAN A ").unwrap();
        assert_eq!(code, "VCB");
        assert_eq!(num, "123456");
        assert_eq!(masked, "****3456");
        assert_eq!(name, "NGUYEN VAN A");

        let (_, num19, masked19, _) =
            validate_refund_bank_account("MB", "1234567890123456789", "TRAN THI B").unwrap();
        assert_eq!(num19.len(), 19);
        assert_eq!(masked19, "****6789");

        // Invalid account numbers: too short, too long, non-digit, spaces inside
        for bad_num in [
            "",
            "12345",
            "12345678901234567890",
            "12345A",
            "123 456",
            "123-456",
        ] {
            assert!(
                matches!(
                    validate_refund_bank_account("VCB", bad_num, "NGUYEN VAN A"),
                    Err(PaymentError::Validation(_))
                ),
                "expected Validation error for account_number={bad_num:?}"
            );
        }
        // Invalid bank_code / account_name
        assert!(validate_refund_bank_account("", "123456", "NGUYEN VAN A").is_err());
        assert!(validate_refund_bank_account("V", "123456", "NGUYEN VAN A").is_err());
        assert!(validate_refund_bank_account("VCB", "123456", "").is_err());
        assert!(validate_refund_bank_account("VCB", "123456", "A").is_err());

        // Custom Debug on RefundRecord never leaks bank_account_number
        let now = Utc::now();
        let rec = RefundRecord {
            id: Uuid::new_v4(),
            payment_id: Uuid::new_v4(),
            order_id: "ord-1".into(),
            amount_vnd: 50_000,
            reason: "test".into(),
            status: "pending".into(),
            idempotency_key: "k1".into(),
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
        let dbg = format!("{rec:?}");
        assert!(!dbg.contains("19034567890123"));
        assert!(dbg.contains("[REDACTED]"));
        assert!(dbg.contains("****0123"));
    }

    #[test]
    fn test_w2_vn_date_utc_bounds_and_00_to_07_boundary() {
        let d22 = NaiveDate::from_ymd_opt(2026, 11, 22).unwrap();
        let d23 = NaiveDate::from_ymd_opt(2026, 11, 23).unwrap();
        let d24 = NaiveDate::from_ymd_opt(2026, 11, 24).unwrap();

        // 2026-11-22T16:59:59Z == 2026-11-22 23:59:59 +07:00 -> VN date 2026-11-22
        let t_before = DateTime::parse_from_rfc3339("2026-11-22T16:59:59Z")
            .unwrap()
            .with_timezone(&Utc);
        assert_eq!(utc_to_vn_date(t_before), d22);

        // 2026-11-22T17:00:00Z == 2026-11-23 00:00:00 +07:00 -> VN date 2026-11-23 (00:00 VN)
        let t_midnight_vn = DateTime::parse_from_rfc3339("2026-11-22T17:00:00Z")
            .unwrap()
            .with_timezone(&Utc);
        assert_eq!(utc_to_vn_date(t_midnight_vn), d23);

        // 2026-11-22T18:22:00Z == 2026-11-23 01:22:00 +07:00 (exact PAY-B8 bug time!) -> VN date 2026-11-23
        let t_0122_vn = DateTime::parse_from_rfc3339("2026-11-22T18:22:00Z")
            .unwrap()
            .with_timezone(&Utc);
        assert_eq!(utc_to_vn_date(t_0122_vn), d23);

        // 2026-11-22T23:59:59Z == 2026-11-23 06:59:59 +07:00 -> VN date 2026-11-23
        let t_0659_vn = DateTime::parse_from_rfc3339("2026-11-22T23:59:59Z")
            .unwrap()
            .with_timezone(&Utc);
        assert_eq!(utc_to_vn_date(t_0659_vn), d23);

        // 2026-11-23T00:00:00Z == 2026-11-23 07:00:00 +07:00 -> VN date 2026-11-23
        let t_0700_vn = DateTime::parse_from_rfc3339("2026-11-23T00:00:00Z")
            .unwrap()
            .with_timezone(&Utc);
        assert_eq!(utc_to_vn_date(t_0700_vn), d23);

        // 2026-11-23T17:00:00Z == 2026-11-24 00:00:00 +07:00 -> VN date 2026-11-24
        let t_next_midnight_vn = DateTime::parse_from_rfc3339("2026-11-23T17:00:00Z")
            .unwrap()
            .with_timezone(&Utc);
        assert_eq!(utc_to_vn_date(t_next_midnight_vn), d24);

        let (start, end) = vn_day_utc_bounds(d23);
        assert_eq!(start, t_midnight_vn);
        assert_eq!(end, t_next_midnight_vn);
        assert!(t_before < start);
        assert!(t_midnight_vn >= start && t_midnight_vn < end);
        assert!(t_0122_vn >= start && t_0122_vn < end);
        assert!(t_0659_vn >= start && t_0659_vn < end);
        assert!(t_0700_vn >= start && t_0700_vn < end);
        assert!(t_next_midnight_vn >= end);

        // Empty date at 2026-11-22T18:22:00Z defaults to VN today (2026-11-23), NOT UTC 2026-11-22
        assert_eq!(
            parse_and_validate_reconciliation_date("", t_0122_vn).unwrap(),
            d23
        );
        // Same-day VN date allowed, past date allowed, tomorrow VN date -> 400 Validation
        assert_eq!(
            parse_and_validate_reconciliation_date("2026-11-23", t_0122_vn).unwrap(),
            d23
        );
        assert_eq!(
            parse_and_validate_reconciliation_date("2026-11-22", t_0122_vn).unwrap(),
            d22
        );
        assert!(matches!(
            parse_and_validate_reconciliation_date("2026-11-24", t_0122_vn),
            Err(PaymentError::Validation(_))
        ));
        assert!(matches!(
            parse_and_validate_reconciliation_date("23-11-2026", t_0122_vn),
            Err(PaymentError::Validation(_))
        ));
        assert!(matches!(
            parse_and_validate_reconciliation_date("2026-02-30", t_0122_vn),
            Err(PaymentError::Validation(_))
        ));
    }

    #[test]
    fn test_w2_review_fixes_keys_guards_idempotency_and_note_snapshot() {
        // Point 2 / P0-1: provider_tx_id-scoped keys and unified accounting state guard
        assert_eq!(reconciliation_refund_key(" TX-1 "), "recon-refund:TX-1");
        assert_eq!(reconciliation_overpay_key(" TX-1 "), "recon-overpay:TX-1");
        let clean = ProviderTxAccountingState::default();
        assert!(!clean.is_accounted());
        assert!(validate_reconciliation_provider_tx_accountable("TX-1", clean).is_ok());

        for state in [
            ProviderTxAccountingState {
                recon_queue_finalized: true,
                ..Default::default()
            },
            ProviderTxAccountingState {
                on_payment: true,
                ..Default::default()
            },
            ProviderTxAccountingState {
                has_tx_refund: true,
                ..Default::default()
            },
        ] {
            assert!(state.is_accounted());
            assert!(matches!(
                validate_reconciliation_provider_tx_accountable("TX-1", state),
                Err(PaymentError::Conflict(_))
            ));
        }

        // Point 4: SettleRefund and ResolveReconciliationItem idempotency parameter mismatch -> 409
        let now = Utc::now();
        let rf_id = Uuid::new_v4();
        let rf = RefundRecord {
            id: rf_id,
            payment_id: Uuid::new_v4(),
            order_id: "ord-1".into(),
            amount_vnd: 50_000,
            reason: "r".into(),
            status: "completed".into(),
            idempotency_key: "rf-k".into(),
            provider_refund_id: Some("FT-1".into()),
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
            bank_account_name: Some("BUYER".into()),
            settled_at: Some(now),
            settlement_note: None,
            settlement_idempotency_key: Some("settle-k".into()),
            affects_escrow: true,
            created_at: now,
            updated_at: now,
        };
        assert!(validate_settle_refund_idempotency_match(&rf, rf_id, "FT-1").is_ok());
        assert!(matches!(
            validate_settle_refund_idempotency_match(&rf, rf_id, "FT-2"),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_settle_refund_idempotency_match(&rf, Uuid::new_v4(), "FT-1"),
            Err(PaymentError::Conflict(_))
        ));

        let item_id = Uuid::new_v4();
        let p1 = Uuid::new_v4();
        let p2 = Uuid::new_v4();
        let qi = ReconciliationQueueItem {
            id: item_id,
            webhook_event_id: None,
            provider: "vietqr_mock".into(),
            provider_tx_id: "TX-1".into(),
            expected_payment_id: Some(p1),
            order_ref: Some("grp-1".into()),
            expected_amount_vnd: Some(100_000),
            actual_amount_vnd: 90_000,
            reason: "amount_mismatch".into(),
            resolution_status: "refunded".into(),
            notes: None,
            resolution_idempotency_key: Some("rq-k".into()),
            resolution_refund_id: Some(rf_id),
            resolution_action: Some("REFUND".into()),
            resolution_payment_id: Some(p1),
            occurred_at: Some(now),
            source_type: "webhook".into(),
            tracking_code: String::new(),
            created_at: now,
            resolved_at: Some(now),
        };
        assert!(validate_resolve_reconciliation_idempotency_match(
            &qi,
            item_id,
            ReconciliationActionKind::Refund,
            Some(p1)
        )
        .is_ok());
        assert!(matches!(
            validate_resolve_reconciliation_idempotency_match(
                &qi,
                item_id,
                ReconciliationActionKind::Assign,
                Some(p1)
            ),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_resolve_reconciliation_idempotency_match(
                &qi,
                item_id,
                ReconciliationActionKind::Refund,
                Some(p2)
            ),
            Err(PaymentError::Conflict(_))
        ));
        assert!(matches!(
            validate_resolve_reconciliation_idempotency_match(
                &qi,
                item_id,
                ReconciliationActionKind::Refund,
                None
            ),
            Err(PaymentError::Conflict(_))
        ));

        // Point 5: settlement_note snapshots masked destination bank account
        assert_eq!(
            format_settlement_note_with_masked_account("Manual FT", Some("VCB"), Some("****0123")),
            Some("Manual FT [settled_to: VCB ****0123]".to_string())
        );
        assert_eq!(
            format_settlement_note_with_masked_account("  ", Some("MB"), Some("****9999")),
            Some("[settled_to: MB ****9999]".to_string())
        );
        assert_eq!(
            format_settlement_note_with_masked_account("Cash counter", None, None),
            Some("Cash counter".to_string())
        );
    }

    #[test]
    fn test_w2_review2_normalize_tx_id_cancel_terminal_and_expiry_ordering() {
        // P0-1: normalize_provider_tx_id
        assert_eq!(normalize_provider_tx_id("  TX-99 \t\n").unwrap(), "TX-99");
        assert!(matches!(
            normalize_provider_tx_id(""),
            Err(PaymentError::Validation(_))
        ));
        assert!(matches!(
            normalize_provider_tx_id("   \t  "),
            Err(PaymentError::Validation(_))
        ));

        // P1-3: terminal unpaid statuses are idempotently cancelled; paid states stay Conflict
        for st in ["cancelled", "expired", "failed", "superseded"] {
            assert!(
                is_payment_already_cancelled_terminal(st),
                "expected {st} to be idempotently cancelled"
            );
        }
        for st in ["pending", "succeeded", "refunded"] {
            assert!(
                !is_payment_already_cancelled_terminal(st),
                "expected {st} NOT to be treated as already-cancelled"
            );
        }

        // P1-2: exact-amount transfer prioritized ahead of earlier wrong-amount transfer
        let ordered =
            order_expiry_candidate_indices(&[150_000, 200_000, 250_000, 200_000], 200_000);
        assert_eq!(ordered, vec![1, 3, 0, 2]);
        assert_eq!(order_expiry_candidate_indices(&[150_000], 200_000), vec![0]);
        assert!(order_expiry_candidate_indices(&[], 200_000).is_empty());
    }

    #[test]
    fn test_w2b_fee_config_validation_and_time_boundaries() {
        let now = DateTime::parse_from_rfc3339("2026-11-23T10:00:00Z")
            .unwrap()
            .with_timezone(&Utc);

        // bps bounds: 0 and 2000 allowed, -1 and 2001 rejected
        assert_eq!(
            validate_set_fee_config(0, None, "  free  ", now).unwrap(),
            (now, "free".to_string())
        );
        assert_eq!(
            validate_set_fee_config(2000, Some(now), "", now).unwrap(),
            (now, String::new())
        );
        let future = now + chrono::Duration::seconds(1);
        assert_eq!(
            validate_set_fee_config(150, Some(future), "scheduled", now).unwrap(),
            (future, "scheduled".to_string())
        );
        for bad_bps in [-1, -100, 2001, 10_000] {
            assert!(matches!(
                validate_set_fee_config(bad_bps, None, "", now),
                Err(PaymentError::Validation(_))
            ));
        }

        // Past effective_from (even 1ms before now) rejected with error containing effective_from + past
        let past_1ms = now - chrono::Duration::milliseconds(1);
        let err = validate_set_fee_config(100, Some(past_1ms), "", now).unwrap_err();
        let msg = err.to_string().to_ascii_lowercase();
        assert!(
            msg.contains("effective_from") && msg.contains("past"),
            "{msg}"
        );

        // Overlong note rejected
        let long_note = "a".repeat(501);
        assert!(matches!(
            validate_set_fee_config(100, None, &long_note, now),
            Err(PaymentError::Validation(_))
        ));

        // Time boundary resolution with mock clock
        let t0 = DateTime::parse_from_rfc3339("2026-01-01T00:00:00Z")
            .unwrap()
            .with_timezone(&Utc);
        let t1 = DateTime::parse_from_rfc3339("2026-11-23T10:00:00Z")
            .unwrap()
            .with_timezone(&Utc);
        let t2 = DateTime::parse_from_rfc3339("2026-12-01T00:00:00Z")
            .unwrap()
            .with_timezone(&Utc);

        let mk = |seq: i64, method: &str, bps: i32, eff: DateTime<Utc>, created: DateTime<Utc>| {
            PaymentFeeConfigRecord {
                id: Uuid::new_v4(),
                seq,
                payment_method: method.to_string(),
                fee_bps: bps,
                effective_from: eff,
                created_by: "admin-1".to_string(),
                note: String::new(),
                created_at: created,
            }
        };

        let rows = vec![
            mk(1, "COD", 0, t0, t0),
            mk(2, "VIETQR", 0, t0, t0),
            // Earlier seq with later wall-clock created_at (NTP skew): higher seq MUST still win!
            mk(3, "VIETQR", 120, t1, t1 + chrono::Duration::seconds(30)),
            mk(4, "VIETQR", 150, t1, t1),
            // Scheduled future rate at t2
            mk(5, "VIETQR", 250, t2, t1 + chrono::Duration::seconds(5)),
            mk(6, "COD", 200, t2, t1 + chrono::Duration::seconds(5)),
        ];

        // Strictly before t0 -> no active config
        let before_t0 =
            resolve_active_fee_configs(&rows, None, t0 - chrono::Duration::nanoseconds(1));
        assert!(before_t0.is_empty());

        // Exactly at t0 -> baseline 0 bps for both COD and VIETQR
        let at_t0 = resolve_active_fee_configs(&rows, None, t0);
        assert_eq!(at_t0.len(), 2);
        assert_eq!(at_t0[0].payment_method, "COD");
        assert_eq!(at_t0[0].fee_bps, 0);
        assert_eq!(at_t0[1].payment_method, "VIETQR");
        assert_eq!(at_t0[1].fee_bps, 0);

        // 1 nanosecond before t1 -> still baseline 0 bps
        let before_t1 = resolve_active_fee_configs(
            &rows,
            Some(PaymentMethod::VietQr),
            t1 - chrono::Duration::nanoseconds(1),
        );
        assert_eq!(before_t1.len(), 1);
        assert_eq!(before_t1[0].fee_bps, 0);

        // Exactly at t1 -> seq=4 (150 bps) wins tie-break over seq=3 (120 bps)
        let at_t1 = resolve_active_fee_configs(&rows, Some(PaymentMethod::VietQr), t1);
        assert_eq!(at_t1.len(), 1);
        assert_eq!(at_t1[0].fee_bps, 150);
        assert_eq!(at_t1[0].seq, 4);

        // 1 second before t2 -> future rates not active yet
        let before_t2 = resolve_active_fee_configs(&rows, None, t2 - chrono::Duration::seconds(1));
        assert_eq!(before_t2[0].fee_bps, 0); // COD
        assert_eq!(before_t2[1].fee_bps, 150); // VIETQR

        // Exactly at t2 -> future rates activate for both COD and VIETQR
        let at_t2 = resolve_active_fee_configs(&rows, None, t2);
        assert_eq!(at_t2[0].payment_method, "COD");
        assert_eq!(at_t2[0].fee_bps, 200);
        assert_eq!(at_t2[1].payment_method, "VIETQR");
        assert_eq!(at_t2[1].fee_bps, 250);
    }

    #[test]
    fn test_w2b_seller_ledger_account_filter_and_vn_descriptions() {
        assert_eq!(parse_seller_ledger_account_filter("").unwrap(), None);
        assert_eq!(parse_seller_ledger_account_filter("   ").unwrap(), None);
        assert_eq!(
            parse_seller_ledger_account_filter("pending").unwrap(),
            Some(SellerLedgerAccountFilter::Pending)
        );
        assert_eq!(
            parse_seller_ledger_account_filter("  AVAILABLE ").unwrap(),
            Some(SellerLedgerAccountFilter::Available)
        );
        for bad in ["escrow", "gateway_clearing", "seller_pending:shop-1"] {
            assert!(matches!(
                parse_seller_ledger_account_filter(bad),
                Err(PaymentError::Validation(_))
            ));
        }

        let settle_with_fee =
            format_seller_ledger_description("escrow_settle", "ord-1", "seller_pending:s1", 15_000);
        assert!(settle_with_fee.contains("Tiền đơn hàng ord-1"));
        assert!(settle_with_fee.contains("phí sàn 15000"));

        let settle_no_fee =
            format_seller_ledger_description("escrow_settle", "ord-2", "seller_pending:s1", 0);
        assert_eq!(settle_no_fee, "Tiền đơn hàng ord-2");

        let rel_pending =
            format_seller_ledger_description("escrow_release", "ord-1", "seller_pending:s1", 0);
        assert!(rel_pending.contains("Giải ngân"));
        let rel_avail =
            format_seller_ledger_description("escrow_release", "ord-1", "seller_available:s1", 0);
        assert!(rel_avail.contains("Giải ngân"));

        let refund_desc =
            format_seller_ledger_description("refund", "rf-1", "seller_available:s1", 0);
        assert!(refund_desc.contains("Hoàn tiền"));

        let payout_desc =
            format_seller_ledger_description("payout", "po-1", "seller_available:s1", 0);
        assert!(payout_desc.contains("Rút tiền"));

        let fallback_desc =
            format_seller_ledger_description("adjustment", "adj-3", "seller_available:s1", 0);
        assert_eq!(fallback_desc, "Giao dịch adjustment (adj-3)");
    }

    #[test]
    fn test_w2b_money_model_with_shop_program_shop_freeship_and_payment_fee() {
        // w2b-plan.md §1:
        // G=500k, F=0, P_shop=50k, V_shop=20k, Ship=35k, S_shop=20k
        // => R = 500k - 50k - 20k - 20k = 410k
        // V_plat=30k, S_free=15k, X=5k => D = 50k
        // P = R + Ship - D = 410k + 35k - 50k = 395k (P + D == R + Ship == 445k)
        // C_cat=21,500, C_pay=5,925 (150 bps on 395k) => C_total = 27,425 <= R
        let alloc = ShopAllocationInput {
            order_id: Some("ord-w2b-1".into()),
            shop_id: "shop-1".into(),
            subtotal_vnd: 410_000,
            commission_vnd: 27_425,
            shipping_fee_vnd: 35_000,
            platform_discount_vnd: 50_000,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
        };
        assert!(validate_allocations_sum(395_000, &[alloc]).is_ok());

        // Full settlement without refund
        let split =
            compute_settlement_split("ord-w2b-1", "shop-1", 410_000, 27_425, 35_000, 50_000, 0)
                .unwrap();
        assert_eq!(split.escrow_debit_vnd, 395_000);
        assert_eq!(split.platform_promo_debit_vnd, 50_000);
        assert_eq!(split.seller_net_vnd, 382_575);
        assert_eq!(split.commission_vnd, 27_425);
        assert_eq!(split.shipping_fee_vnd, 35_000);
        assert_eq!(
            split.total_debit_vnd(),
            split.seller_net_vnd + split.commission_vnd + split.shipping_fee_vnd
        );

        // Partial refund of 100,000 before settlement stays balanced
        let split_partial = compute_settlement_split(
            "ord-w2b-1",
            "shop-1",
            410_000,
            27_425,
            35_000,
            50_000,
            100_000,
        )
        .unwrap();
        assert_eq!(split_partial.escrow_debit_vnd, 295_000);
        assert_eq!(split_partial.seller_net_vnd, 282_575);
        assert_eq!(split_partial.commission_vnd, 27_425);
        assert_eq!(
            split_partial.total_debit_vnd(),
            split_partial.seller_net_vnd
                + split_partial.commission_vnd
                + split_partial.shipping_fee_vnd
        );

        // Cap boundary: C_total == R (commission + payment_fee capped at R) => seller_net = 0
        let capped = ShopAllocationInput {
            order_id: Some("ord-w2b-cap".into()),
            shop_id: "shop-2".into(),
            subtotal_vnd: 10_000,
            commission_vnd: 10_000,
            shipping_fee_vnd: 30_000,
            platform_discount_vnd: 0,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
        };
        assert!(validate_allocations_sum(40_000, &[capped]).is_ok());
        let split_cap =
            compute_settlement_split("ord-w2b-cap", "shop-2", 10_000, 10_000, 30_000, 0, 0)
                .unwrap();
        assert_eq!(split_cap.seller_net_vnd, 0);
        assert_eq!(split_cap.commission_vnd, 10_000);
        assert_eq!(split_cap.shipping_fee_vnd, 30_000);

        // C_total > R MUST be rejected
        let over_cap = ShopAllocationInput {
            order_id: Some("ord-w2b-bad".into()),
            shop_id: "shop-2".into(),
            subtotal_vnd: 10_000,
            commission_vnd: 10_001,
            shipping_fee_vnd: 30_000,
            platform_discount_vnd: 0,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
        };
        assert!(validate_allocations_sum(40_000, &[over_cap]).is_err());

        // Trimmed S_shop boundary: R == 0, C_total == 0, Ship = 30k, D = 0 => P = 30k
        let zero_r = ShopAllocationInput {
            order_id: Some("ord-w2b-r0".into()),
            shop_id: "shop-3".into(),
            subtotal_vnd: 0,
            commission_vnd: 0,
            shipping_fee_vnd: 30_000,
            platform_discount_vnd: 0,
            sales_channel: String::new(),
            storefront_shop_id: String::new(),
        };
        assert!(validate_allocations_sum(30_000, &[zero_r]).is_ok());
        let split_r0 =
            compute_settlement_split("ord-w2b-r0", "shop-3", 0, 0, 30_000, 0, 0).unwrap();
        assert_eq!(split_r0.escrow_debit_vnd, 30_000);
        assert_eq!(split_r0.seller_net_vnd, 0);
        assert_eq!(split_r0.shipping_fee_vnd, 30_000);
    }

    #[test]
    fn test_w3b_e20_cod_reconciliation_classification_and_validators() {
        // run_type filter validation
        assert_eq!(parse_reconciliation_run_type_filter("").unwrap(), None);
        assert_eq!(parse_reconciliation_run_type_filter("  ").unwrap(), None);
        assert_eq!(
            parse_reconciliation_run_type_filter("VIETQR").unwrap(),
            Some("vietqr".to_string())
        );
        assert_eq!(
            parse_reconciliation_run_type_filter("cod_file").unwrap(),
            Some("cod_file".to_string())
        );
        assert!(parse_reconciliation_run_type_filter("unknown").is_err());

        // carrier_code validation
        assert_eq!(normalize_cod_carrier_code(" GHN ").unwrap(), "ghn");
        assert!(normalize_cod_carrier_code("   ").is_err());
        assert!(normalize_cod_carrier_code(&"a".repeat(65)).is_err());

        // row count validation (1..=5000)
        assert!(validate_cod_reconciliation_rows(&[]).is_err());
        let one = vec![CodReconciliationRowInput {
            line_number: 1,
            tracking_code: "TRK-1".into(),
            cod_amount_vnd: 100_000,
            delivered_at: String::new(),
            order_code: String::new(),
        }];
        assert!(validate_cod_reconciliation_rows(&one).is_ok());
        let too_many = vec![one[0].clone(); MAX_COD_RECONCILIATION_ROWS + 1];
        assert!(validate_cod_reconciliation_rows(&too_many).is_err());

        // Provider tx keys
        assert_eq!(
            cod_reconcile_provider_tx_id("GHN", " TRK-1 "),
            "cod:ghn:TRK-1"
        );
        assert_eq!(
            cod_reconcile_mismatch_provider_tx_id("GHN", "TRK-1", 99_999),
            "cod:ghn:TRK-1:mismatch:99999"
        );

        // Classification decisions (E20 & E20-bis)
        assert_eq!(
            decide_cod_allocation_reconciliation(&["awaiting_collection"], 200_000, 200_000, false),
            CodAllocationMatchDecision::NotDelivered { cancelled: false }
        );
        assert_eq!(
            decide_cod_allocation_reconciliation(&["cancelled"], 200_000, 200_000, false),
            CodAllocationMatchDecision::NotDelivered { cancelled: true }
        );
        for st in ["held", "pending_release", "released", "refunded"] {
            assert_eq!(
                decide_cod_allocation_reconciliation(&[st], 200_000, 200_000, false),
                CodAllocationMatchDecision::MatchAndPost,
                "status={st}"
            );
            assert_eq!(
                decide_cod_allocation_reconciliation(&[st], 200_000, 200_000, true),
                CodAllocationMatchDecision::AlreadyReconciled,
                "status={st}"
            );
            // 1 VND off -> AmountMismatch even if already reconciled (E20-bis)
            assert_eq!(
                decide_cod_allocation_reconciliation(&[st], 200_000, 199_999, true),
                CodAllocationMatchDecision::AmountMismatch,
                "status={st}"
            );
            assert_eq!(
                decide_cod_allocation_reconciliation(&[st], 200_000, 200_001, false),
                CodAllocationMatchDecision::AmountMismatch,
                "status={st}"
            );
            assert_eq!(
                decide_cod_allocation_reconciliation(&[st], 200_000, 0, false),
                CodAllocationMatchDecision::AmountMismatch,
                "status={st}"
            );
            assert_eq!(
                decide_cod_allocation_reconciliation(&[st], 200_000, -100, false),
                CodAllocationMatchDecision::AmountMismatch,
                "status={st}"
            );
        }

        // Carrier mismatch helper (empty/NULL carrier still matches; differing carrier mismatches)
        assert_eq!(
            find_cod_carrier_mismatch(&[None, Some(""), Some("  ")], "ghn"),
            None
        );
        assert_eq!(find_cod_carrier_mismatch(&[Some("GHN")], "ghn"), None);
        assert_eq!(
            find_cod_carrier_mismatch(&[Some("ghn")], "ghtk"),
            Some("ghn".to_string())
        );
        assert_eq!(
            find_cod_carrier_mismatch(&[None, Some(" VTP ")], "ghn"),
            Some("vtp".to_string())
        );
    }

    #[test]
    fn test_w3b2_domain_is_online_method_config_validation_and_approval_threshold() {
        // is_online_method covers VIETQR & ZALOPAY (case-insensitive) and excludes COD/unknown.
        assert!(is_online_method("VIETQR"));
        assert!(is_online_method("vietqr"));
        assert!(is_online_method("ZALOPAY"));
        assert!(is_online_method(" zalopay "));
        assert!(!is_online_method("COD"));
        assert!(!is_online_method(""));

        // PaymentMethod::parse & is_online
        assert_eq!(
            PaymentMethod::parse("ZALOPAY").unwrap(),
            PaymentMethod::ZaloPay
        );
        assert_eq!(
            PaymentMethod::parse("zalo_pay").unwrap(),
            PaymentMethod::ZaloPay
        );
        assert!(PaymentMethod::ZaloPay.is_online());
        assert!(PaymentMethod::VietQr.is_online());
        assert!(!PaymentMethod::Cod.is_online());

        // plan_initial_payment for ZALOPAY (positive & 0 VND)
        let zp_plan = plan_initial_payment(PaymentMethod::ZaloPay, 250_000).unwrap();
        assert_eq!(zp_plan.initial_status, "pending");
        assert!(!zp_plan.create_qr);
        assert!(zp_plan.create_zalopay);
        assert!(!zp_plan.mark_paid_now);

        let zp_zero = plan_initial_payment(PaymentMethod::ZaloPay, 0).unwrap();
        assert_eq!(zp_zero.initial_status, "succeeded");
        assert!(!zp_zero.create_qr);
        assert!(!zp_zero.create_zalopay);
        assert!(zp_zero.mark_paid_now);

        // validate_update_payment_method_config edge cases
        let base_update = UpdatePaymentMethodConfigInput {
            method: PaymentMethod::ZaloPay,
            enabled: true,
            min_order_vnd: 10_000,
            max_order_vnd: 50_000_000,
            expiry_minutes: 15,
            note: "ok".into(),
            expected_version: 1,
            updated_by: "ops-1".into(),
        };
        assert!(validate_update_payment_method_config(&base_update).is_ok());
        assert!(
            validate_update_payment_method_config(&UpdatePaymentMethodConfigInput {
                expected_version: 0,
                ..base_update.clone()
            })
            .is_err()
        );
        assert!(
            validate_update_payment_method_config(&UpdatePaymentMethodConfigInput {
                min_order_vnd: -1,
                ..base_update.clone()
            })
            .is_err()
        );
        assert!(
            validate_update_payment_method_config(&UpdatePaymentMethodConfigInput {
                min_order_vnd: 500_000,
                max_order_vnd: 100_000,
                ..base_update.clone()
            })
            .is_err()
        );
        assert!(
            validate_update_payment_method_config(&UpdatePaymentMethodConfigInput {
                expiry_minutes: 4, // ZALOPAY min is 5 min (300s)
                ..base_update.clone()
            })
            .is_err()
        );
        assert!(
            validate_update_payment_method_config(&UpdatePaymentMethodConfigInput {
                method: PaymentMethod::Cod,
                expiry_minutes: 15, // COD must be 0
                ..base_update.clone()
            })
            .is_err()
        );

        // validate_payment_method_eligibility
        let cod_cfg = PaymentMethodConfigRecord {
            method: "COD".into(),
            enabled: true,
            min_order_vnd: 20_000,
            max_order_vnd: 5_000_000,
            expiry_minutes: 0,
            version: 1,
            note: String::new(),
            updated_by: "system".into(),
            updated_at: Utc::now(),
        };
        assert!(
            validate_payment_method_eligibility(&cod_cfg, PaymentMethod::Cod, 5_000_000).is_ok()
        );
        assert!(validate_payment_method_eligibility(&cod_cfg, PaymentMethod::Cod, 0).is_ok());
        let over_cod = validate_payment_method_eligibility(&cod_cfg, PaymentMethod::Cod, 5_000_001)
            .unwrap_err();
        assert!(
            matches!(over_cod, PaymentError::FailedPrecondition(ref m) if m.starts_with("COD_LIMIT_EXCEEDED"))
        );
        let below_min =
            validate_payment_method_eligibility(&cod_cfg, PaymentMethod::Cod, 10_000).unwrap_err();
        assert!(
            matches!(below_min, PaymentError::FailedPrecondition(ref m) if m.starts_with("ORDER_AMOUNT_OUT_OF_RANGE"))
        );
        let mut disabled_cfg = cod_cfg;
        disabled_cfg.enabled = false;
        let disabled_err =
            validate_payment_method_eligibility(&disabled_cfg, PaymentMethod::Cod, 100_000)
                .unwrap_err();
        assert!(
            matches!(disabled_err, PaymentError::FailedPrecondition(ref m) if m.starts_with("PAYMENT_METHOD_DISABLED"))
        );

        // F11 should_require_large_refund_approval (single & 24h anti-splitting)
        assert!(!should_require_large_refund_approval(
            4_999_999, 0, 5_000_000
        ));
        assert!(should_require_large_refund_approval(
            5_000_000, 0, 5_000_000
        ));
        assert!(!should_require_large_refund_approval(
            2_000_000, 2_999_999, 5_000_000
        ));
        assert!(should_require_large_refund_approval(
            2_000_000, 3_000_000, 5_000_000
        ));
        assert!(should_require_large_refund_approval(
            3_000_000, 3_000_000, 5_000_000
        ));

        // p3 Item 1: exponential backoff capped at 3600s (1 hour)
        assert_eq!(zalopay_late_sweep_backoff(1, 60).num_seconds(), 60);
        assert_eq!(zalopay_late_sweep_backoff(2, 60).num_seconds(), 120);
        assert_eq!(zalopay_late_sweep_backoff(3, 60).num_seconds(), 240);
        assert_eq!(zalopay_late_sweep_backoff(4, 60).num_seconds(), 480);
        assert_eq!(zalopay_late_sweep_backoff(6, 60).num_seconds(), 1920);
        assert_eq!(zalopay_late_sweep_backoff(7, 60).num_seconds(), 3600);
        assert_eq!(zalopay_late_sweep_backoff(20, 60).num_seconds(), 3600);

        // p5: window-exhausted decision table from persisted (return_code, is_processing)
        assert_eq!(
            decide_zalopay_window_exhausted(Some(3), Some(false)),
            ZaloPayWindowExhaustedDecision::ConcludeNotPaid
        );
        assert_eq!(
            decide_zalopay_window_exhausted(Some(2), Some(false)),
            ZaloPayWindowExhaustedDecision::ConcludeNotPaid
        );
        assert_eq!(
            decide_zalopay_window_exhausted(Some(2), None),
            ZaloPayWindowExhaustedDecision::ConcludeNotPaid
        );
        assert_eq!(
            decide_zalopay_window_exhausted(Some(3), Some(true)),
            ZaloPayWindowExhaustedDecision::EscalateManualReview
        );
        assert_eq!(
            decide_zalopay_window_exhausted(Some(3), None),
            ZaloPayWindowExhaustedDecision::EscalateManualReview
        );
        assert_eq!(
            decide_zalopay_window_exhausted(None, None),
            ZaloPayWindowExhaustedDecision::EscalateManualReview
        );
        assert_eq!(
            decide_zalopay_window_exhausted(Some(1), Some(false)),
            ZaloPayWindowExhaustedDecision::EscalateManualReview
        );
        assert_eq!(
            decide_zalopay_window_exhausted(Some(99), Some(false)),
            ZaloPayWindowExhaustedDecision::EscalateManualReview
        );
    }

    #[test]
    fn test_w3b3_g2_cod_shipping_fee_adjustment_domain_rules() {
        let now = Utc::now();
        let pid = Uuid::new_v4();
        let alloc_a_id = Uuid::new_v4();
        let alloc_b_id = Uuid::new_v4();
        let alloc_c_id = Uuid::new_v4();

        let make_payment = |method: &str, status: &str, amount_vnd: i64| PaymentRecord {
            id: pid,
            order_group_id: "grp-cod-1".into(),
            order_id: "grp-cod-1".into(),
            user_id: "buyer-1".into(),
            method: method.into(),
            provider: if method == "COD" { "cod" } else { "mock" }.into(),
            amount_vnd,
            status: status.into(),
            provider_tx_id: None,
            provider_order_code: "NM-COD-1".into(),
            qr_code: None,
            qr_url: None,
            pay_url: None,
            idempotency_key: Some("checkout:grp-cod-1".into()),
            expires_at: None,
            paid_at: None,
            trace_id: "trace-1".into(),
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            created_at: now,
            updated_at: now,
        };
        let make_alloc = |id: Uuid,
                          order_id: &str,
                          shop_id: &str,
                          subtotal: i64,
                          commission: i64,
                          ship: i64,
                          discount: i64,
                          escrow: &str| ShopAllocationRecord {
            id,
            payment_id: pid,
            order_id: order_id.into(),
            shop_id: shop_id.into(),
            subtotal_vnd: subtotal,
            commission_vnd: commission,
            shipping_fee_vnd: ship,
            platform_discount_vnd: discount,
            escrow_status: escrow.into(),
            settled_at: None,
            released_at: None,
            tracking_code: None,
            carrier_code: None,
            order_code: None,
            cod_reconciled_at: None,
            cod_reconciliation_run_id: None,
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            created_at: now,
        };

        // Input validation
        assert_eq!(
            validate_adjust_cod_shipping_fee_input("  ord-1  ", "  key-1  ", 25_000).unwrap(),
            ("ord-1", "key-1")
        );
        assert!(matches!(
            validate_adjust_cod_shipping_fee_input("   ", "key-1", 25_000),
            Err(PaymentError::Validation(_))
        ));
        assert!(matches!(
            validate_adjust_cod_shipping_fee_input("ord-1", "   ", 25_000),
            Err(PaymentError::Validation(_))
        ));
        assert!(matches!(
            validate_adjust_cod_shipping_fee_input("ord-1", "key-1", -1),
            Err(PaymentError::Validation(_))
        ));

        // Increase & decrease on multi-shop COD with one held sibling and one cancelled sibling.
        let payment = make_payment("COD", "pending", 420_000);
        let allocs = vec![
            make_alloc(
                alloc_a_id, "ord-a", "shop-a", 100_000, 5_000, 20_000, 0, "held",
            ),
            make_alloc(
                alloc_b_id,
                "ord-b",
                "shop-b",
                80_000,
                4_000,
                20_000,
                0,
                "cancelled",
            ),
            make_alloc(
                alloc_c_id,
                "ord-c",
                "shop-c",
                200_000,
                10_000,
                30_000,
                30_000,
                "awaiting_collection",
            ),
        ];

        // Increase shipping fee on ord-c (30k -> 50k): new_payable = 200k + 50k - 30k = 220k.
        // Parent amount excludes cancelled ord-b: held ord-a (120k) + ord-c (220k) = 340k.
        let inc = plan_cod_shipping_fee_adjustment(&payment, &allocs, "ord-c", 50_000).unwrap();
        assert_eq!(
            inc,
            CodShippingFeeAdjustmentPlan {
                allocation_id: alloc_c_id,
                old_shipping_fee_vnd: 30_000,
                new_shipping_fee_vnd: 50_000,
                new_order_payable_vnd: 220_000,
                new_payment_amount_vnd: 340_000,
            }
        );

        // Decrease shipping fee on ord-c (30k -> 10k): new_payable = 200k + 10k - 30k = 180k.
        let dec = plan_cod_shipping_fee_adjustment(&payment, &allocs, "ord-c", 10_000).unwrap();
        assert_eq!(dec.new_order_payable_vnd, 180_000);
        assert_eq!(dec.new_payment_amount_vnd, 300_000);

        // Payable < 0 (discount exceeds subtotal + new_shipping_fee) -> FailedPrecondition.
        let heavy_discount = vec![make_alloc(
            alloc_c_id,
            "ord-c",
            "shop-c",
            100_000,
            5_000,
            30_000,
            120_000,
            "awaiting_collection",
        )];
        assert!(matches!(
            plan_cod_shipping_fee_adjustment(&payment, &heavy_discount, "ord-c", 10_000),
            Err(PaymentError::FailedPrecondition(_))
        ));
        // Exactly 0 payable is valid (100k + 20k - 120k = 0).
        let zero_pay =
            plan_cod_shipping_fee_adjustment(&payment, &heavy_discount, "ord-c", 20_000).unwrap();
        assert_eq!(zero_pay.new_order_payable_vnd, 0);
        assert_eq!(zero_pay.new_payment_amount_vnd, 0);

        // Non-COD (VIETQR / ZALOPAY) -> FailedPrecondition with COD_ALREADY_COLLECTED prefix.
        for bad_method in ["VIETQR", "ZALOPAY"] {
            let p = make_payment(bad_method, "pending", 200_000);
            let err = plan_cod_shipping_fee_adjustment(&p, &allocs, "ord-c", 40_000).unwrap_err();
            assert!(
                matches!(err, PaymentError::FailedPrecondition(ref msg) if msg.starts_with(ERR_COD_ALREADY_COLLECTED)),
                "method={bad_method} got {err:?}"
            );
        }

        // Non-pending payment status -> FailedPrecondition with COD_ALREADY_COLLECTED prefix.
        for bad_status in ["succeeded", "cancelled", "refunded", "expired", "failed"] {
            let p = make_payment("COD", bad_status, 200_000);
            let err = plan_cod_shipping_fee_adjustment(&p, &allocs, "ord-c", 40_000).unwrap_err();
            assert!(
                matches!(err, PaymentError::FailedPrecondition(ref msg) if msg.starts_with(ERR_COD_ALREADY_COLLECTED)),
                "status={bad_status} got {err:?}"
            );
        }

        // Non-awaiting_collection allocation -> FailedPrecondition with COD_ALREADY_COLLECTED prefix.
        for bad_escrow in [
            "held",
            "pending_release",
            "released",
            "refunded",
            "cancelled",
        ] {
            let one = vec![make_alloc(
                alloc_c_id, "ord-c", "shop-c", 100_000, 5_000, 20_000, 0, bad_escrow,
            )];
            let err =
                plan_cod_shipping_fee_adjustment(&payment, &one, "ord-c", 35_000).unwrap_err();
            assert!(
                matches!(err, PaymentError::FailedPrecondition(ref msg) if msg.starts_with(ERR_COD_ALREADY_COLLECTED)),
                "escrow={bad_escrow} got {err:?}"
            );
        }

        // Idempotency replay match vs conflict.
        let rec = CodFeeAdjustmentRecord {
            id: Uuid::new_v4(),
            idempotency_key: "adj-1".into(),
            payment_id: pid,
            order_id: "ord-c".into(),
            old_shipping_fee_vnd: 30_000,
            new_shipping_fee_vnd: 50_000,
            new_order_payable_vnd: 220_000,
            created_at: now,
        };
        assert!(validate_cod_fee_adjustment_replay(&rec, "ord-c", 50_000).is_ok());
        assert!(matches!(
            validate_cod_fee_adjustment_replay(&rec, "ord-c", 40_000),
            Err(PaymentError::FailedPrecondition(_))
        ));
        assert!(matches!(
            validate_cod_fee_adjustment_replay(&rec, "ord-other", 50_000),
            Err(PaymentError::FailedPrecondition(_))
        ));
    }

    #[test]
    fn test_sales_channel_validation_and_defaults() {
        // Legacy / empty defaults to MARKETPLACE
        assert_eq!(
            SalesChannel::parse_or_default("").unwrap(),
            SalesChannel::Marketplace
        );
        assert_eq!(
            SalesChannel::parse_or_default("   ").unwrap(),
            SalesChannel::Marketplace
        );
        assert_eq!(
            SalesChannel::parse_or_default("MARKETPLACE").unwrap(),
            SalesChannel::Marketplace
        );
        assert_eq!(
            SalesChannel::parse_or_default("STOREFRONT").unwrap(),
            SalesChannel::Storefront
        );
        assert!(matches!(
            SalesChannel::parse_or_default("DIRECT"),
            Err(PaymentError::Validation(_))
        ));

        // Filter parsing: empty -> None (all), valid -> Some, invalid -> Validation error
        assert_eq!(SalesChannel::parse_filter("").unwrap(), None);
        assert_eq!(
            SalesChannel::parse_filter("MARKETPLACE").unwrap(),
            Some(SalesChannel::Marketplace)
        );
        assert_eq!(
            SalesChannel::parse_filter("STOREFRONT").unwrap(),
            Some(SalesChannel::Storefront)
        );
        assert!(matches!(
            SalesChannel::parse_filter("SOCIAL"),
            Err(PaymentError::Validation(_))
        ));

        // ChannelAttribution consistency rules
        let mp = ChannelAttribution::from_parts("", "").unwrap();
        assert_eq!(mp.sales_channel, SalesChannel::Marketplace);
        assert_eq!(mp.storefront_shop_id, "");
        assert_eq!(
            mp.metadata_json(),
            serde_json::json!({"sales_channel": "MARKETPLACE", "storefront_shop_id": ""})
        );

        assert!(ChannelAttribution::from_parts("MARKETPLACE", "shop-1").is_err());
        assert!(ChannelAttribution::from_parts("STOREFRONT", "").is_err());
        assert!(ChannelAttribution::from_parts("STOREFRONT", "   ").is_err());

        let sf = ChannelAttribution::from_parts("STOREFRONT", "shop-42").unwrap();
        assert_eq!(sf.sales_channel, SalesChannel::Storefront);
        assert_eq!(sf.storefront_shop_id, "shop-42");
        assert_eq!(
            sf.metadata_json(),
            serde_json::json!({"sales_channel": "STOREFRONT", "storefront_shop_id": "shop-42"})
        );

        // Legacy DB fallback reads empty as MARKETPLACE
        assert_eq!(
            ChannelAttribution::from_db("", ""),
            ChannelAttribution::marketplace()
        );
    }

    #[test]
    fn test_storefront_allocation_validation_rejects_cross_shop() {
        let sf = ChannelAttribution::storefront("shop-A").unwrap();
        let valid_alloc = ShopAllocationInput {
            order_id: Some("ord-1".into()),
            shop_id: "shop-A".into(),
            subtotal_vnd: 200_000,
            commission_vnd: 10_000,
            shipping_fee_vnd: 30_000,
            platform_discount_vnd: 0,
            sales_channel: "STOREFRONT".into(),
            storefront_shop_id: "shop-A".into(),
        };
        assert!(sf
            .validate_allocations(std::slice::from_ref(&valid_alloc))
            .is_ok());

        // Cross-shop allocation must be rejected
        let cross_shop = ShopAllocationInput {
            shop_id: "shop-B".into(),
            ..valid_alloc.clone()
        };
        assert!(matches!(
            sf.validate_allocations(&[cross_shop]),
            Err(PaymentError::Validation(_))
        ));

        // Conflicting allocation channel must be rejected
        let wrong_channel = ShopAllocationInput {
            sales_channel: "MARKETPLACE".into(),
            storefront_shop_id: String::new(),
            ..valid_alloc.clone()
        };
        assert!(matches!(
            sf.validate_allocations(&[wrong_channel]),
            Err(PaymentError::Validation(_))
        ));

        // Conflicting storefront_shop_id on allocation must be rejected
        let wrong_sf_shop = ShopAllocationInput {
            storefront_shop_id: "shop-B".into(),
            ..valid_alloc
        };
        assert!(matches!(
            sf.validate_allocations(&[wrong_sf_shop]),
            Err(PaymentError::Validation(_))
        ));
    }

    #[test]
    fn test_reconciliation_channel_matches_partitions_100_percent() {
        assert!(reconciliation_channel_matches("MARKETPLACE", None));
        assert!(reconciliation_channel_matches("STOREFRONT", None));
        assert!(reconciliation_channel_matches("", None));

        assert!(reconciliation_channel_matches(
            "MARKETPLACE",
            Some(SalesChannel::Marketplace)
        ));
        assert!(reconciliation_channel_matches(
            "",
            Some(SalesChannel::Marketplace)
        ));
        assert!(!reconciliation_channel_matches(
            "STOREFRONT",
            Some(SalesChannel::Marketplace)
        ));
        assert!(reconciliation_channel_matches(
            "STOREFRONT",
            Some(SalesChannel::Storefront)
        ));
        assert!(!reconciliation_channel_matches(
            "MARKETPLACE",
            Some(SalesChannel::Storefront)
        ));
    }

    #[test]
    fn test_payment_method_and_status_parsing_including_zalopay() {
        assert_eq!(PaymentMethod::parse("COD").unwrap(), PaymentMethod::Cod);
        assert_eq!(
            PaymentMethod::parse("vietqr").unwrap(),
            PaymentMethod::VietQr
        );
        assert_eq!(
            PaymentMethod::parse("VIET_QR").unwrap(),
            PaymentMethod::VietQr
        );
        assert_eq!(PaymentMethod::parse("qr").unwrap(), PaymentMethod::VietQr);
        assert_eq!(
            PaymentMethod::parse("ZALOPAY").unwrap(),
            PaymentMethod::ZaloPay
        );
        assert_eq!(
            PaymentMethod::parse("zalo_pay").unwrap(),
            PaymentMethod::ZaloPay
        );
        assert_eq!(PaymentMethod::parse("zlp").unwrap(), PaymentMethod::ZaloPay);
        assert!(PaymentMethod::parse("BITCOIN").is_err());

        assert_eq!(PaymentMethod::Cod.as_str(), "COD");
        assert_eq!(PaymentMethod::VietQr.as_str(), "VIETQR");
        assert_eq!(PaymentMethod::ZaloPay.as_str(), "ZALOPAY");
        assert!(!PaymentMethod::Cod.is_online());
        assert!(PaymentMethod::VietQr.is_online());
        assert!(PaymentMethod::ZaloPay.is_online());

        for (raw, expected, s) in [
            ("pending", PaymentStatus::Pending, "pending"),
            ("succeeded", PaymentStatus::Succeeded, "succeeded"),
            ("paid", PaymentStatus::Succeeded, "succeeded"),
            ("failed", PaymentStatus::Failed, "failed"),
            ("expired", PaymentStatus::Expired, "expired"),
            ("cancelled", PaymentStatus::Cancelled, "cancelled"),
            ("canceled", PaymentStatus::Cancelled, "cancelled"),
            ("superseded", PaymentStatus::Superseded, "superseded"),
            ("refunded", PaymentStatus::Refunded, "refunded"),
            ("other", PaymentStatus::Pending, "pending"),
        ] {
            let parsed = PaymentStatus::parse(raw);
            assert_eq!(parsed, expected);
            assert_eq!(parsed.as_str(), s);
        }

        // Additional edge-case validations
        assert!(compute_settlement_split("ord-1", " ", 100, 0, 0, 0, 0).is_err());
        assert!(compute_settlement_split("ord-1", "s1", -1, 0, 0, 0, 0).is_err());
        assert!(compute_settlement_split("ord-1", "s1", 100, 101, 0, 0, 0).is_err());
        assert!(validate_refund_cap(0, 0, 100).is_err());
        assert!(validate_refund_cap(100, -1, 50).is_err());
        assert_eq!(
            classify_reconciliation_pair(None, None),
            ReconciliationDecision::Matched
        );
    }
}
