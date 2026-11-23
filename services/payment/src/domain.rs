use std::fmt;

use chrono::{DateTime, FixedOffset, NaiveDate, TimeZone, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::error::{PaymentError, Result};
use crate::telemetry::mask_bank_account;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "UPPERCASE")]
pub enum PaymentMethod {
    Cod,
    VietQr,
}

impl PaymentMethod {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Cod => "COD",
            Self::VietQr => "VIETQR",
        }
    }

    pub fn parse(raw: &str) -> Result<Self> {
        match raw.trim().to_ascii_uppercase().as_str() {
            "COD" => Ok(Self::Cod),
            "VIETQR" | "VIET_QR" | "QR" => Ok(Self::VietQr),
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

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ShopAllocationInput {
    pub order_id: Option<String>,
    pub shop_id: String,
    pub subtotal_vnd: i64,
    pub commission_vnd: i64,
    pub shipping_fee_vnd: i64,
    #[serde(default)]
    pub platform_discount_vnd: i64,
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
    pub idempotency_key: Option<String>,
    pub expires_at: Option<DateTime<Utc>>,
    pub paid_at: Option<DateTime<Utc>>,
    pub trace_id: String,
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
    pub created_at: DateTime<Utc>,
    pub resolved_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct ReconciliationRunRecord {
    pub id: Uuid,
    pub provider: String,
    pub run_date: NaiveDate,
    pub matched_count: i32,
    pub missing_local_count: i32,
    pub missing_provider_count: i32,
    pub amount_mismatch_count: i32,
    pub auto_healed_count: i32,
    pub status: String,
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
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReconciliationReport {
    pub run: ReconciliationRunRecord,
    pub items: Vec<ReconciliationItemRecord>,
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

/// Validates that an existing refund matched by `idempotency_key` belongs to the same target
/// payment/order (and amount for non-cancel admin refunds) (review-B P2-8 / review2-P P3-8).
/// Keys prefixed with `order-cancel-refund:` skip the `amount_vnd` check so synchronous
/// `OrderService.CancelOrder` and asynchronous `order.cancelled` still deduplicate cleanly
/// after a prior partial refund.
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
    if !existing.idempotency_key.starts_with("order-cancel-refund:")
        && !existing.idempotency_key.starts_with("order-return-refund:")
        && existing.amount_vnd != requested_amount_vnd
    {
        return Err(PaymentError::Conflict(
            "idempotency_key already used with different refund amount".to_string(),
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

/// Initial status and QR creation plan for `create_payment` (R1 0 VND + R2 COD awaiting collection).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct InitialPaymentPlan {
    pub initial_status: &'static str,
    pub initial_escrow_status: &'static str,
    pub create_qr: bool,
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
            mark_paid_now: true,
        });
    }
    match method {
        PaymentMethod::Cod => Ok(InitialPaymentPlan {
            initial_status: "pending",
            initial_escrow_status: "awaiting_collection",
            create_qr: false,
            mark_paid_now: false,
        }),
        PaymentMethod::VietQr => Ok(InitialPaymentPlan {
            initial_status: "pending",
            initial_escrow_status: "awaiting_collection",
            create_qr: true,
            mark_paid_now: false,
        }),
    }
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
            idempotency_key: Some("retry-1".into()),
            expires_at: None,
            paid_at: None,
            trace_id: String::new(),
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

        // Cancel-order dedup key allows differing requested amount between sync CancelOrder and async order.cancelled
        let cancel_refund = RefundRecord {
            idempotency_key: order_cancel_refund_key("ord-1"),
            amount_vnd: 190_000,
            ..admin_refund
        };
        assert!(validate_refund_idempotency_match(
            &cancel_refund,
            Some(pid1),
            "ord-1",
            200_000,
            &[]
        )
        .is_ok());
        // Still rejects wrong order/payment on cancel key
        assert!(matches!(
            validate_refund_idempotency_match(&cancel_refund, Some(pid1), "ord-2", 190_000, &[]),
            Err(PaymentError::Conflict(_))
        ));
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
            idempotency_key: None,
            expires_at: None,
            paid_at: None,
            trace_id: String::new(),
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
}
