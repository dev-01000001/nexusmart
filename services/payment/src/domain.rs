use chrono::{DateTime, NaiveDate, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::error::{PaymentError, Result};

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

#[derive(Debug, Clone, Serialize, Deserialize, sqlx::FromRow)]
pub struct RefundRecord {
    pub id: Uuid,
    pub payment_id: Uuid,
    pub order_id: String,
    pub amount_vnd: i64,
    pub reason: String,
    pub status: String,
    pub idempotency_key: String,
    pub provider_refund_id: Option<String>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
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

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AllocationSplit {
    pub order_id: String,
    pub shop_id: String,
    pub seller_net_vnd: i64,
    pub commission_vnd: i64,
    pub shipping_fee_vnd: i64,
    pub total_escrow_debit_vnd: i64,
}

/// Validates and computes integer VND split for settling escrow into seller_pending,
/// platform_fee_revenue, and shipping_fee_payable.
pub fn compute_allocation_split(
    order_id: &str,
    shop_id: &str,
    subtotal_vnd: i64,
    commission_vnd: i64,
    shipping_fee_vnd: i64,
) -> Result<AllocationSplit> {
    if shop_id.trim().is_empty() {
        return Err(PaymentError::Validation(
            "shop_id must not be empty".to_string(),
        ));
    }
    if subtotal_vnd < 0 || commission_vnd < 0 || shipping_fee_vnd < 0 {
        return Err(PaymentError::Validation(
            "allocation VND amounts must be non-negative".to_string(),
        ));
    }
    if commission_vnd > subtotal_vnd {
        return Err(PaymentError::Validation(format!(
            "commission_vnd ({commission_vnd}) exceeds subtotal_vnd ({subtotal_vnd})"
        )));
    }

    let seller_net_vnd = subtotal_vnd
        .checked_sub(commission_vnd)
        .ok_or_else(|| PaymentError::Validation("underflow computing seller net".to_string()))?;

    let total_escrow_debit_vnd = subtotal_vnd
        .checked_add(shipping_fee_vnd)
        .ok_or_else(|| PaymentError::Validation("overflow computing escrow total".to_string()))?;

    if total_escrow_debit_vnd <= 0 {
        return Err(PaymentError::Validation(
            "allocation total must be positive".to_string(),
        ));
    }

    Ok(AllocationSplit {
        order_id: order_id.to_string(),
        shop_id: shop_id.to_string(),
        seller_net_vnd,
        commission_vnd,
        shipping_fee_vnd,
        total_escrow_debit_vnd,
    })
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

    if let Some(alloc_total) = suborder_alloc_vnd.filter(|&v| v > 0) {
        let suborder_remaining = alloc_total.saturating_sub(suborder_already_refunded_vnd.max(0));
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
            } else {
                ReconciliationDecision::Matched
            }
        }
        (None, None) => ReconciliationDecision::Matched,
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
    fn test_allocation_split_exact_vnd() {
        let split = compute_allocation_split("ord-1", "shop-42", 250_000, 12_500, 30_000).unwrap();
        assert_eq!(split.seller_net_vnd, 237_500);
        assert_eq!(split.commission_vnd, 12_500);
        assert_eq!(split.shipping_fee_vnd, 30_000);
        assert_eq!(split.total_escrow_debit_vnd, 280_000);
        assert_eq!(
            split.seller_net_vnd + split.commission_vnd + split.shipping_fee_vnd,
            split.total_escrow_debit_vnd
        );
    }

    #[test]
    fn test_allocation_rejects_excessive_commission() {
        assert!(compute_allocation_split("ord-1", "shop-1", 100_000, 100_001, 0).is_err());
    }
}
