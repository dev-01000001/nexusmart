use std::collections::{HashMap, HashSet};

use serde::{Deserialize, Serialize};
use sqlx::{PgPool, Postgres, Row, Transaction};
use uuid::Uuid;

use crate::domain::{ChannelAttribution, SalesChannel};
use crate::error::{PaymentError, Result};

// Canonical system ledger account codes
pub const ACCOUNT_GATEWAY_CLEARING: &str = "gateway_clearing";
pub const ACCOUNT_COD_RECEIVABLE: &str = "cod_receivable";
pub const ACCOUNT_ESCROW_LIABILITY: &str = "escrow_liability";
pub const ACCOUNT_PLATFORM_FEE_REVENUE: &str = "platform_fee_revenue";
pub const ACCOUNT_SHIPPING_FEE_PAYABLE: &str = "shipping_fee_payable";
pub const ACCOUNT_REFUND_PAYABLE: &str = "refund_payable";

pub fn account_seller_pending(shop_id: &str) -> String {
    format!("seller_pending:{shop_id}")
}

pub fn account_seller_available(shop_id: &str) -> String {
    format!("seller_available:{shop_id}")
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "UPPERCASE")]
pub enum AccountType {
    Asset,
    Liability,
    Revenue,
    Expense,
    Equity,
}

impl AccountType {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Asset => "ASSET",
            Self::Liability => "LIABILITY",
            Self::Revenue => "REVENUE",
            Self::Expense => "EXPENSE",
            Self::Equity => "EQUITY",
        }
    }

    pub fn parse(raw: &str) -> Self {
        match raw.trim().to_ascii_uppercase().as_str() {
            "ASSET" => Self::Asset,
            "REVENUE" => Self::Revenue,
            "EXPENSE" => Self::Expense,
            "EQUITY" => Self::Equity,
            _ => Self::Liability,
        }
    }

    /// Computes normal balance sign:
    /// - ASSET / EXPENSE are debit-normal: `debits - credits`
    /// - LIABILITY / REVENUE / EQUITY are credit-normal: `credits - debits`
    pub fn signed_balance(self, total_debits: i64, total_credits: i64) -> i64 {
        match self {
            Self::Asset | Self::Expense => total_debits - total_credits,
            Self::Liability | Self::Revenue | Self::Equity => total_credits - total_debits,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LedgerLine {
    pub account_id: String,
    pub account_type: AccountType,
    pub shop_id: Option<String>,
    pub debit_vnd: i64,
    pub credit_vnd: i64,
}

impl LedgerLine {
    pub fn debit(
        account_id: impl Into<String>,
        account_type: AccountType,
        shop_id: Option<String>,
        amount_vnd: i64,
    ) -> Self {
        Self {
            account_id: account_id.into(),
            account_type,
            shop_id,
            debit_vnd: amount_vnd,
            credit_vnd: 0,
        }
    }

    pub fn credit(
        account_id: impl Into<String>,
        account_type: AccountType,
        shop_id: Option<String>,
        amount_vnd: i64,
    ) -> Self {
        Self {
            account_id: account_id.into(),
            account_type,
            shop_id,
            debit_vnd: 0,
            credit_vnd: amount_vnd,
        }
    }
}

#[derive(Debug, Clone)]
pub struct LedgerTransactionDraft {
    pub idempotency_key: String,
    pub reference_type: String,
    pub reference_id: String,
    pub description: String,
    pub channel: ChannelAttribution,
    pub lines: Vec<LedgerLine>,
}

impl LedgerTransactionDraft {
    /// Strict Rust-side double-entry validation BEFORE hitting PostgreSQL.
    /// Guarantees:
    /// 1. Non-empty idempotency key & valid channel attribution
    /// 2. At least 2 entries
    /// 3. Every entry is strictly positive on exactly one side (debit OR credit, never both/zero/negative)
    /// 4. sum(debit_vnd) == sum(credit_vnd) > 0 with overflow protection
    pub fn validate(&self) -> Result<()> {
        if self.idempotency_key.trim().is_empty() {
            return Err(PaymentError::LedgerInvariant(
                "idempotency_key must not be empty".to_string(),
            ));
        }
        self.channel.validate().map_err(|e| {
            PaymentError::LedgerInvariant(format!("invalid channel attribution: {e}"))
        })?;
        if self.lines.len() < 2 {
            return Err(PaymentError::LedgerInvariant(format!(
                "ledger transaction must have at least 2 lines, got {}",
                self.lines.len()
            )));
        }

        let mut total_debit: i64 = 0;
        let mut total_credit: i64 = 0;

        for (idx, line) in self.lines.iter().enumerate() {
            if line.account_id.trim().is_empty() {
                return Err(PaymentError::LedgerInvariant(format!(
                    "line {idx} has empty account_id"
                )));
            }
            let valid_debit = line.debit_vnd > 0 && line.credit_vnd == 0;
            let valid_credit = line.credit_vnd > 0 && line.debit_vnd == 0;
            if !valid_debit && !valid_credit {
                return Err(PaymentError::LedgerInvariant(format!(
                    "line {idx} ({}) must have strictly positive debit OR credit (got debit={}, credit={})",
                    line.account_id, line.debit_vnd, line.credit_vnd
                )));
            }
            total_debit = total_debit
                .checked_add(line.debit_vnd)
                .ok_or_else(|| PaymentError::LedgerInvariant("debit sum overflow".to_string()))?;
            total_credit = total_credit
                .checked_add(line.credit_vnd)
                .ok_or_else(|| PaymentError::LedgerInvariant("credit sum overflow".to_string()))?;
        }

        if total_debit <= 0 || total_debit != total_credit {
            return Err(PaymentError::LedgerInvariant(format!(
                "unbalanced ledger transaction '{}': sum(debit)={} != sum(credit)={}",
                self.idempotency_key, total_debit, total_credit
            )));
        }

        Ok(())
    }
}

// =============================================================================
// Domain Journal Entry Builders
// =============================================================================

/// VietQR confirmed: Debit gateway_clearing, Credit escrow_liability
pub fn build_vietqr_confirm_tx(
    payment_id: Uuid,
    amount_vnd: i64,
    channel: ChannelAttribution,
) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("vietqr-confirm:{payment_id}"),
        reference_type: "payment_vietqr".to_string(),
        reference_id: payment_id.to_string(),
        description: format!("VietQR payment confirmed for payment {payment_id}"),
        channel,
        lines: vec![
            LedgerLine::debit(
                ACCOUNT_GATEWAY_CLEARING,
                AccountType::Asset,
                None,
                amount_vnd,
            ),
            LedgerLine::credit(
                ACCOUNT_ESCROW_LIABILITY,
                AccountType::Liability,
                None,
                amount_vnd,
            ),
        ],
    }
}

/// ZaloPay confirmed: Debit gateway_clearing, Credit escrow_liability
pub fn build_zalopay_confirm_tx(
    payment_id: Uuid,
    amount_vnd: i64,
    channel: ChannelAttribution,
) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("zalopay-confirm:{payment_id}"),
        reference_type: "payment_zalopay".to_string(),
        reference_id: payment_id.to_string(),
        description: format!("ZaloPay payment confirmed for payment {payment_id}"),
        channel,
        lines: vec![
            LedgerLine::debit(
                ACCOUNT_GATEWAY_CLEARING,
                AccountType::Asset,
                None,
                amount_vnd,
            ),
            LedgerLine::credit(
                ACCOUNT_ESCROW_LIABILITY,
                AccountType::Liability,
                None,
                amount_vnd,
            ),
        ],
    }
}

/// COD confirmed: Debit cod_receivable, Credit escrow_liability
pub fn build_cod_confirm_tx(
    payment_id: Uuid,
    amount_vnd: i64,
    channel: ChannelAttribution,
) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("cod-confirm:{payment_id}"),
        reference_type: "payment_cod".to_string(),
        reference_id: payment_id.to_string(),
        description: format!("COD receivable recorded for payment {payment_id}"),
        channel,
        lines: vec![
            LedgerLine::debit(ACCOUNT_COD_RECEIVABLE, AccountType::Asset, None, amount_vnd),
            LedgerLine::credit(
                ACCOUNT_ESCROW_LIABILITY,
                AccountType::Liability,
                None,
                amount_vnd,
            ),
        ],
    }
}

/// COD cash reconciled from shipping carrier: Debit gateway_clearing, Credit cod_receivable
pub fn build_cod_reconcile_tx(
    payment_id: Uuid,
    amount_vnd: i64,
    channel: ChannelAttribution,
) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("cod-reconcile:{payment_id}"),
        reference_type: "cod_reconcile".to_string(),
        reference_id: payment_id.to_string(),
        description: format!("COD collected from carrier for payment {payment_id}"),
        channel,
        lines: vec![
            LedgerLine::debit(
                ACCOUNT_GATEWAY_CLEARING,
                AccountType::Asset,
                None,
                amount_vnd,
            ),
            LedgerLine::credit(ACCOUNT_COD_RECEIVABLE, AccountType::Asset, None, amount_vnd),
        ],
    }
}

/// Refund on a succeeded payment (UC-PAY04):
/// Reverses escrow liability into refund_payable under the original payment/allocation channel.
pub fn build_refund_tx(
    refund_id: Uuid,
    payment_id: Uuid,
    amount_vnd: i64,
    reason: &str,
    channel: ChannelAttribution,
) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("refund-accrual:{refund_id}"),
        reference_type: "refund".to_string(),
        reference_id: refund_id.to_string(),
        description: format!("Refund {refund_id} for payment {payment_id}: {reason}"),
        channel,
        lines: vec![
            LedgerLine::debit(
                ACCOUNT_ESCROW_LIABILITY,
                AccountType::Liability,
                None,
                amount_vnd,
            ),
            LedgerLine::credit(
                ACCOUNT_REFUND_PAYABLE,
                AccountType::Liability,
                None,
                amount_vnd,
            ),
        ],
    }
}

/// Refund on a sub-order allocation that was ALREADY settled (`pending_release` or `released`):
/// Proportionally reverses `seller_pending:<shop_id>` (or `seller_available:<shop_id>` when
/// `from_available` is true), `platform_fee_revenue`, and `shipping_fee_payable` into
/// `refund_payable` using strictly balanced integer VND arithmetic.
#[allow(clippy::too_many_arguments)]
pub fn build_settled_refund_tx(
    refund_id: Uuid,
    payment_id: Uuid,
    shop_id: &str,
    from_available: bool,
    seller_net_vnd: i64,
    commission_vnd: i64,
    shipping_fee_vnd: i64,
    refund_amount_vnd: i64,
    reason: &str,
    channel: ChannelAttribution,
) -> Result<LedgerTransactionDraft> {
    if shop_id.trim().is_empty() {
        return Err(PaymentError::LedgerInvariant(
            "shop_id must not be empty for settled refund".to_string(),
        ));
    }
    if seller_net_vnd < 0 || commission_vnd < 0 || shipping_fee_vnd < 0 {
        return Err(PaymentError::LedgerInvariant(
            "allocation split amounts must be non-negative".to_string(),
        ));
    }
    let alloc_total = seller_net_vnd
        .checked_add(commission_vnd)
        .and_then(|v| v.checked_add(shipping_fee_vnd))
        .ok_or_else(|| {
            PaymentError::LedgerInvariant("settled refund allocation sum overflow".to_string())
        })?;
    if alloc_total <= 0 || refund_amount_vnd <= 0 {
        return Err(PaymentError::LedgerInvariant(
            "settled refund allocation total and refund_amount_vnd must be positive".to_string(),
        ));
    }
    if refund_amount_vnd > alloc_total {
        return Err(PaymentError::LedgerInvariant(format!(
            "settled refund amount ({refund_amount_vnd}) exceeds allocation total ({alloc_total})"
        )));
    }

    let (rev_seller, rev_commission, rev_shipping) = if refund_amount_vnd == alloc_total {
        (seller_net_vnd, commission_vnd, shipping_fee_vnd)
    } else {
        let total128 = alloc_total as i128;
        let ref128 = refund_amount_vnd as i128;
        let mut s = ((seller_net_vnd as i128 * ref128) / total128) as i64;
        let mut c = ((commission_vnd as i128 * ref128) / total128) as i64;
        let mut sh = ((shipping_fee_vnd as i128 * ref128) / total128) as i64;
        let mut rem = refund_amount_vnd - s - c - sh;

        let add_s = rem.min(seller_net_vnd - s);
        s += add_s;
        rem -= add_s;

        let add_c = rem.min(commission_vnd - c);
        c += add_c;
        rem -= add_c;

        let add_sh = rem.min(shipping_fee_vnd - sh);
        sh += add_sh;
        rem -= add_sh;

        debug_assert_eq!(rem, 0);
        (s, c, sh)
    };

    let seller_acct = if from_available {
        account_seller_available(shop_id)
    } else {
        account_seller_pending(shop_id)
    };

    let mut lines = Vec::with_capacity(4);
    if rev_seller > 0 {
        lines.push(LedgerLine::debit(
            seller_acct,
            AccountType::Liability,
            Some(shop_id.to_string()),
            rev_seller,
        ));
    }
    if rev_commission > 0 {
        lines.push(LedgerLine::debit(
            ACCOUNT_PLATFORM_FEE_REVENUE,
            AccountType::Revenue,
            None,
            rev_commission,
        ));
    }
    if rev_shipping > 0 {
        lines.push(LedgerLine::debit(
            ACCOUNT_SHIPPING_FEE_PAYABLE,
            AccountType::Liability,
            None,
            rev_shipping,
        ));
    }
    lines.push(LedgerLine::credit(
        ACCOUNT_REFUND_PAYABLE,
        AccountType::Liability,
        None,
        refund_amount_vnd,
    ));

    let draft = LedgerTransactionDraft {
        idempotency_key: format!("refund-accrual:{refund_id}"),
        reference_type: "refund".to_string(),
        reference_id: refund_id.to_string(),
        description: format!(
            "Settled refund {refund_id} for payment {payment_id} shop {shop_id}: {reason}"
        ),
        channel,
        lines,
    };
    draft.validate()?;
    Ok(draft)
}

/// Refund disbursement settled out via gateway/bank:
/// Debit refund_payable, Credit gateway_clearing.
pub fn build_refund_settle_tx(
    refund_id: Uuid,
    amount_vnd: i64,
    channel: ChannelAttribution,
) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("refund-settle:{refund_id}"),
        reference_type: "refund_settle".to_string(),
        reference_id: refund_id.to_string(),
        description: format!("Refund {refund_id} disbursed to buyer"),
        channel,
        lines: vec![
            LedgerLine::debit(
                ACCOUNT_REFUND_PAYABLE,
                AccountType::Liability,
                None,
                amount_vnd,
            ),
            LedgerLine::credit(
                ACCOUNT_GATEWAY_CLEARING,
                AccountType::Asset,
                None,
                amount_vnd,
            ),
        ],
    }
}

/// Late/duplicate webhook received for an expired/cancelled/superseded payment (UC-PAY01 / UC-B25):
/// Money arrived in gateway_clearing and is immediately owed back to buyer in refund_payable.
pub fn build_late_payment_refund_tx(
    provider_tx_id: &str,
    amount_vnd: i64,
    channel: ChannelAttribution,
) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("late-webhook-refund:{provider_tx_id}"),
        reference_type: "late_payment_refund".to_string(),
        reference_id: provider_tx_id.to_string(),
        description: format!("Late/superseded webhook {provider_tx_id} booked to refund_payable"),
        channel,
        lines: vec![
            LedgerLine::debit(
                ACCOUNT_GATEWAY_CLEARING,
                AccountType::Asset,
                None,
                amount_vnd,
            ),
            LedgerLine::credit(
                ACCOUNT_REFUND_PAYABLE,
                AccountType::Liability,
                None,
                amount_vnd,
            ),
        ],
    }
}

/// Order completed -> credit seller_pending:<shop_id>, platform_fee_revenue, shipping_fee_payable
/// while debiting escrow_liability for the exact sum.
pub fn build_escrow_settle_tx(
    order_id: &str,
    shop_id: &str,
    seller_net_vnd: i64,
    commission_vnd: i64,
    shipping_fee_vnd: i64,
    channel: ChannelAttribution,
) -> Result<LedgerTransactionDraft> {
    let total_debit = seller_net_vnd
        .checked_add(commission_vnd)
        .and_then(|v| v.checked_add(shipping_fee_vnd))
        .ok_or_else(|| PaymentError::LedgerInvariant("escrow settle sum overflow".to_string()))?;

    let mut lines = vec![LedgerLine::debit(
        ACCOUNT_ESCROW_LIABILITY,
        AccountType::Liability,
        None,
        total_debit,
    )];

    if seller_net_vnd > 0 {
        lines.push(LedgerLine::credit(
            account_seller_pending(shop_id),
            AccountType::Liability,
            Some(shop_id.to_string()),
            seller_net_vnd,
        ));
    }
    if commission_vnd > 0 {
        lines.push(LedgerLine::credit(
            ACCOUNT_PLATFORM_FEE_REVENUE,
            AccountType::Revenue,
            None,
            commission_vnd,
        ));
    }
    if shipping_fee_vnd > 0 {
        lines.push(LedgerLine::credit(
            ACCOUNT_SHIPPING_FEE_PAYABLE,
            AccountType::Liability,
            None,
            shipping_fee_vnd,
        ));
    }

    let draft = LedgerTransactionDraft {
        idempotency_key: format!("escrow-settle:{order_id}:{shop_id}"),
        reference_type: "escrow_settle".to_string(),
        reference_id: order_id.to_string(),
        description: format!("Settle completed order {order_id} escrow for shop {shop_id}"),
        channel,
        lines,
    };
    draft.validate()?;
    Ok(draft)
}

/// Escrow release (UC-PAY05): move seller_pending:<shop_id> -> seller_available:<shop_id>.
pub fn build_escrow_release_tx(
    order_id: &str,
    shop_id: &str,
    seller_net_vnd: i64,
    channel: ChannelAttribution,
) -> Result<LedgerTransactionDraft> {
    let draft = LedgerTransactionDraft {
        idempotency_key: format!("escrow-release:{order_id}:{shop_id}"),
        reference_type: "escrow_release".to_string(),
        reference_id: order_id.to_string(),
        description: format!(
            "Release escrow to available balance for order {order_id} shop {shop_id}"
        ),
        channel,
        lines: vec![
            LedgerLine::debit(
                account_seller_pending(shop_id),
                AccountType::Liability,
                Some(shop_id.to_string()),
                seller_net_vnd,
            ),
            LedgerLine::credit(
                account_seller_available(shop_id),
                AccountType::Liability,
                Some(shop_id.to_string()),
                seller_net_vnd,
            ),
        ],
    };
    draft.validate()?;
    Ok(draft)
}

/// Seller payout (UC-PAY05): Debit seller_available:<shop_id>, Credit gateway_clearing.
pub fn build_seller_payout_tx(
    payout_id: Uuid,
    shop_id: &str,
    amount_vnd: i64,
    channel: ChannelAttribution,
) -> Result<LedgerTransactionDraft> {
    let draft = LedgerTransactionDraft {
        idempotency_key: format!("seller-payout:{payout_id}"),
        reference_type: "payout".to_string(),
        reference_id: payout_id.to_string(),
        description: format!("Payout {payout_id} to shop {shop_id}"),
        channel,
        lines: vec![
            LedgerLine::debit(
                account_seller_available(shop_id),
                AccountType::Liability,
                Some(shop_id.to_string()),
                amount_vnd,
            ),
            LedgerLine::credit(
                ACCOUNT_GATEWAY_CLEARING,
                AccountType::Asset,
                None,
                amount_vnd,
            ),
        ],
    };
    draft.validate()?;
    Ok(draft)
}

// =============================================================================
// Ledger Summary & Seller Balance Views
// =============================================================================

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct AccountBalance {
    pub account_id: String,
    pub account_type: String,
    pub total_debits_vnd: i64,
    pub total_credits_vnd: i64,
    pub balance_vnd: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct LedgerSummary {
    pub sales_channel: String,
    pub total_debits_vnd: i64,
    pub total_credits_vnd: i64,
    pub is_balanced: bool,
    pub unbalanced_transactions: i64,
    pub transaction_count: i64,
    pub accounts: Vec<AccountBalance>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct SellerBalance {
    pub shop_id: String,
    pub pending_vnd: i64,
    pub available_vnd: i64,
    pub total_earned_vnd: i64,
}

// =============================================================================
// PostgreSQL Persistence Functions
// =============================================================================

/// Posts a double-entry transaction inside an active PostgreSQL transaction.
/// Returns `Ok(Some(tx_id))` if newly posted, or `Ok(None)` if `idempotency_key` already existed.
pub async fn post_transaction(
    tx: &mut Transaction<'_, Postgres>,
    draft: &LedgerTransactionDraft,
) -> Result<Option<Uuid>> {
    draft.validate()?;

    // Ensure every referenced account exists (creates seller_pending/seller_available lazily).
    for line in &draft.lines {
        sqlx::query(
            r#"
            INSERT INTO ledger_accounts (id, account_type, shop_id)
            VALUES ($1, $2, $3)
            ON CONFLICT (id) DO NOTHING
            "#,
        )
        .bind(&line.account_id)
        .bind(line.account_type.as_str())
        .bind(&line.shop_id)
        .execute(&mut **tx)
        .await?;
    }

    let sales_channel = draft.channel.sales_channel.as_str();
    let storefront_shop_id = &draft.channel.storefront_shop_id;
    let metadata = draft.channel.metadata_json();

    let inserted_id: Option<Uuid> = sqlx::query_scalar(
        r#"
        INSERT INTO ledger_transactions (
            idempotency_key, reference_type, reference_id, description,
            sales_channel, storefront_shop_id, metadata
        )
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT (idempotency_key) DO NOTHING
        RETURNING id
        "#,
    )
    .bind(&draft.idempotency_key)
    .bind(&draft.reference_type)
    .bind(&draft.reference_id)
    .bind(&draft.description)
    .bind(sales_channel)
    .bind(storefront_shop_id)
    .bind(&metadata)
    .fetch_optional(&mut **tx)
    .await?;

    let Some(tx_id) = inserted_id else {
        return Ok(None);
    };

    for line in &draft.lines {
        sqlx::query(
            r#"
            INSERT INTO ledger_entries (
                transaction_id, account_id, debit_vnd, credit_vnd,
                sales_channel, storefront_shop_id, metadata
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            "#,
        )
        .bind(tx_id)
        .bind(&line.account_id)
        .bind(line.debit_vnd)
        .bind(line.credit_vnd)
        .bind(sales_channel)
        .bind(storefront_shop_id)
        .bind(&metadata)
        .execute(&mut **tx)
        .await?;
    }

    Ok(Some(tx_id))
}

/// Queries global or channel-filtered ledger integrity and per-account balances from PostgreSQL.
pub async fn get_ledger_summary(
    pool: &PgPool,
    channel_filter: Option<SalesChannel>,
) -> Result<LedgerSummary> {
    let filter_str = channel_filter.map(SalesChannel::as_str);

    let totals = sqlx::query(
        r#"
        SELECT
            COALESCE(SUM(debit_vnd), 0)::BIGINT AS total_debits_vnd,
            COALESCE(SUM(credit_vnd), 0)::BIGINT AS total_credits_vnd
        FROM ledger_entries
        WHERE ($1::TEXT IS NULL OR sales_channel = $1)
        "#,
    )
    .bind(filter_str)
    .fetch_one(pool)
    .await?;

    let total_debits_vnd: i64 = totals.try_get("total_debits_vnd")?;
    let total_credits_vnd: i64 = totals.try_get("total_credits_vnd")?;

    let transaction_count: i64 = sqlx::query_scalar(
        r#"
        SELECT COUNT(*)::BIGINT
        FROM ledger_transactions
        WHERE ($1::TEXT IS NULL OR sales_channel = $1)
        "#,
    )
    .bind(filter_str)
    .fetch_one(pool)
    .await?;

    let unbalanced_transactions: i64 = sqlx::query_scalar(
        r#"
        SELECT COUNT(*)::BIGINT FROM (
            SELECT transaction_id
            FROM ledger_entries
            WHERE ($1::TEXT IS NULL OR sales_channel = $1)
            GROUP BY transaction_id
            HAVING SUM(debit_vnd) <> SUM(credit_vnd) OR COUNT(*) < 2
        ) unbalanced
        "#,
    )
    .bind(filter_str)
    .fetch_one(pool)
    .await?;

    let rows = sqlx::query(
        r#"
        SELECT
            a.id AS account_id,
            a.account_type AS account_type,
            COALESCE(SUM(e.debit_vnd), 0)::BIGINT AS total_debits_vnd,
            COALESCE(SUM(e.credit_vnd), 0)::BIGINT AS total_credits_vnd
        FROM ledger_accounts a
        LEFT JOIN ledger_entries e
            ON e.account_id = a.id
           AND ($1::TEXT IS NULL OR e.sales_channel = $1)
        GROUP BY a.id, a.account_type
        ORDER BY a.id ASC
        "#,
    )
    .bind(filter_str)
    .fetch_all(pool)
    .await?;

    let mut accounts = Vec::with_capacity(rows.len());
    for row in rows {
        let account_id: String = row.try_get("account_id")?;
        let account_type_str: String = row.try_get("account_type")?;
        let debits: i64 = row.try_get("total_debits_vnd")?;
        let credits: i64 = row.try_get("total_credits_vnd")?;
        let account_type = AccountType::parse(&account_type_str);
        accounts.push(AccountBalance {
            account_id,
            account_type: account_type_str,
            total_debits_vnd: debits,
            total_credits_vnd: credits,
            balance_vnd: account_type.signed_balance(debits, credits),
        });
    }

    let is_balanced = total_debits_vnd == total_credits_vnd && unbalanced_transactions == 0;

    Ok(LedgerSummary {
        sales_channel: filter_str.unwrap_or("").to_string(),
        total_debits_vnd,
        total_credits_vnd,
        is_balanced,
        unbalanced_transactions,
        transaction_count,
        accounts,
    })
}

/// Computes ledger-backed pending, available, and total earned VND balances for a shop (UC-S17).
pub async fn get_seller_balance(pool: &PgPool, shop_id: &str) -> Result<SellerBalance> {
    let pending_acct = account_seller_pending(shop_id);
    let available_acct = account_seller_available(shop_id);

    let pending_row = sqlx::query(
        r#"
        SELECT
            COALESCE(SUM(credit_vnd) - SUM(debit_vnd), 0)::BIGINT AS pending_vnd,
            COALESCE(SUM(credit_vnd), 0)::BIGINT AS total_earned_vnd
        FROM ledger_entries
        WHERE account_id = $1
        "#,
    )
    .bind(&pending_acct)
    .fetch_one(pool)
    .await?;

    let pending_vnd: i64 = pending_row.try_get("pending_vnd")?;
    let total_earned_vnd: i64 = pending_row.try_get("total_earned_vnd")?;

    let available_vnd: i64 = sqlx::query_scalar(
        r#"
        SELECT COALESCE(SUM(credit_vnd) - SUM(debit_vnd), 0)::BIGINT
        FROM ledger_entries
        WHERE account_id = $1
        "#,
    )
    .bind(&available_acct)
    .fetch_one(pool)
    .await?;

    Ok(SellerBalance {
        shop_id: shop_id.to_string(),
        pending_vnd,
        available_vnd,
        total_earned_vnd,
    })
}

/// Queries available balance inside an active DB transaction (locking the account row to prevent
/// concurrent over-withdrawal race conditions).
pub async fn get_seller_available_balance_tx(
    tx: &mut Transaction<'_, Postgres>,
    shop_id: &str,
) -> Result<i64> {
    let available_acct = account_seller_available(shop_id);

    // Ensure account exists and acquire row lock for this shop's available account
    sqlx::query(
        r#"
        INSERT INTO ledger_accounts (id, account_type, shop_id)
        VALUES ($1, 'LIABILITY', $2)
        ON CONFLICT (id) DO NOTHING
        "#,
    )
    .bind(&available_acct)
    .bind(shop_id)
    .execute(&mut **tx)
    .await?;

    sqlx::query("SELECT id FROM ledger_accounts WHERE id = $1 FOR UPDATE")
        .bind(&available_acct)
        .fetch_one(&mut **tx)
        .await?;

    let available_vnd: i64 = sqlx::query_scalar(
        r#"
        SELECT COALESCE(SUM(credit_vnd) - SUM(debit_vnd), 0)::BIGINT
        FROM ledger_entries
        WHERE account_id = $1
        "#,
    )
    .bind(&available_acct)
    .fetch_one(&mut **tx)
    .await?;

    Ok(available_vnd)
}

// =============================================================================
// In-Memory Reference Ledger (for exhaustive unit testing of all accounting flows)
// =============================================================================

#[derive(Debug, Clone)]
pub struct InMemoryLedgerTx {
    pub idempotency_key: String,
    pub reference_type: String,
    pub reference_id: String,
    pub channel: ChannelAttribution,
    pub metadata: serde_json::Value,
    pub lines: Vec<LedgerLine>,
}

#[derive(Debug, Default)]
pub struct InMemoryLedger {
    seen_keys: HashSet<String>,
    account_types: HashMap<String, AccountType>,
    transactions: Vec<InMemoryLedgerTx>,
}

impl InMemoryLedger {
    pub fn post(&mut self, draft: &LedgerTransactionDraft) -> Result<bool> {
        draft.validate()?;
        if !self.seen_keys.insert(draft.idempotency_key.clone()) {
            return Ok(false);
        }
        for line in &draft.lines {
            self.account_types
                .entry(line.account_id.clone())
                .or_insert(line.account_type);
        }
        self.transactions.push(InMemoryLedgerTx {
            idempotency_key: draft.idempotency_key.clone(),
            reference_type: draft.reference_type.clone(),
            reference_id: draft.reference_id.clone(),
            channel: draft.channel.clone(),
            metadata: draft.channel.metadata_json(),
            lines: draft.lines.clone(),
        });
        Ok(true)
    }

    pub fn transactions(&self) -> &[InMemoryLedgerTx] {
        &self.transactions
    }

    pub fn balance_of(&self, account_id: &str) -> i64 {
        let Some(&ty) = self.account_types.get(account_id) else {
            return 0;
        };
        let mut debits = 0_i64;
        let mut credits = 0_i64;
        for tx in &self.transactions {
            for line in &tx.lines {
                if line.account_id == account_id {
                    debits += line.debit_vnd;
                    credits += line.credit_vnd;
                }
            }
        }
        ty.signed_balance(debits, credits)
    }

    pub fn summary(&self) -> LedgerSummary {
        self.summary_for_channel(None)
    }

    pub fn summary_for_channel(&self, channel_filter: Option<SalesChannel>) -> LedgerSummary {
        let mut per_account: HashMap<String, (AccountType, i64, i64)> = self
            .account_types
            .iter()
            .map(|(id, ty)| (id.clone(), (*ty, 0, 0)))
            .collect();
        let mut tx_count = 0_i64;

        for tx in &self.transactions {
            if let Some(ch) = channel_filter {
                if tx.channel.sales_channel != ch {
                    continue;
                }
            }
            tx_count += 1;
            for line in &tx.lines {
                let entry =
                    per_account
                        .entry(line.account_id.clone())
                        .or_insert((line.account_type, 0, 0));
                entry.1 += line.debit_vnd;
                entry.2 += line.credit_vnd;
            }
        }

        let mut total_debits = 0_i64;
        let mut total_credits = 0_i64;
        let mut accounts: Vec<AccountBalance> = per_account
            .into_iter()
            .map(|(id, (ty, d, c))| {
                total_debits += d;
                total_credits += c;
                AccountBalance {
                    account_id: id,
                    account_type: ty.as_str().to_string(),
                    total_debits_vnd: d,
                    total_credits_vnd: c,
                    balance_vnd: ty.signed_balance(d, c),
                }
            })
            .collect();
        accounts.sort_by(|a, b| a.account_id.cmp(&b.account_id));

        LedgerSummary {
            sales_channel: channel_filter
                .map(SalesChannel::as_str)
                .unwrap_or("")
                .to_string(),
            total_debits_vnd: total_debits,
            total_credits_vnd: total_credits,
            is_balanced: total_debits == total_credits,
            unbalanced_transactions: 0,
            transaction_count: tx_count,
            accounts,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_unbalanced_transaction_rejected() {
        let draft = LedgerTransactionDraft {
            idempotency_key: "bad-tx-1".into(),
            reference_type: "test".into(),
            reference_id: "1".into(),
            description: "unbalanced".into(),
            channel: ChannelAttribution::marketplace(),
            lines: vec![
                LedgerLine::debit(ACCOUNT_GATEWAY_CLEARING, AccountType::Asset, None, 100_000),
                LedgerLine::credit(
                    ACCOUNT_ESCROW_LIABILITY,
                    AccountType::Liability,
                    None,
                    99_999,
                ),
            ],
        };
        assert!(matches!(
            draft.validate(),
            Err(PaymentError::LedgerInvariant(_))
        ));
    }

    #[test]
    fn test_zero_or_negative_or_single_line_rejected() {
        let zero_line = LedgerTransactionDraft {
            idempotency_key: "bad-zero".into(),
            reference_type: "test".into(),
            reference_id: "1".into(),
            description: "zero".into(),
            channel: ChannelAttribution::marketplace(),
            lines: vec![
                LedgerLine::debit(ACCOUNT_GATEWAY_CLEARING, AccountType::Asset, None, 0),
                LedgerLine::credit(ACCOUNT_ESCROW_LIABILITY, AccountType::Liability, None, 0),
            ],
        };
        assert!(zero_line.validate().is_err());

        let negative_line = LedgerTransactionDraft {
            idempotency_key: "bad-neg".into(),
            reference_type: "test".into(),
            reference_id: "1".into(),
            description: "neg".into(),
            channel: ChannelAttribution::marketplace(),
            lines: vec![
                LedgerLine::debit(ACCOUNT_GATEWAY_CLEARING, AccountType::Asset, None, -5000),
                LedgerLine::credit(
                    ACCOUNT_ESCROW_LIABILITY,
                    AccountType::Liability,
                    None,
                    -5000,
                ),
            ],
        };
        assert!(negative_line.validate().is_err());

        let single_line = LedgerTransactionDraft {
            idempotency_key: "bad-single".into(),
            reference_type: "test".into(),
            reference_id: "1".into(),
            description: "single".into(),
            channel: ChannelAttribution::marketplace(),
            lines: vec![LedgerLine::debit(
                ACCOUNT_GATEWAY_CLEARING,
                AccountType::Asset,
                None,
                10_000,
            )],
        };
        assert!(single_line.validate().is_err());
    }

    #[test]
    fn test_full_lifecycle_ledger_stays_balanced_across_all_scenarios() {
        let mut ledger = InMemoryLedger::default();
        let mp = ChannelAttribution::marketplace();

        // 1. VietQR payment confirmed: 330,000 VND (shop subtotal 300,000, commission 15,000, shipping 30,000)
        let pay_qr = Uuid::new_v4();
        assert!(ledger
            .post(&build_vietqr_confirm_tx(pay_qr, 330_000, mp.clone()))
            .unwrap());
        // Duplicate posting with same idempotency_key is a no-op
        assert!(!ledger
            .post(&build_vietqr_confirm_tx(pay_qr, 330_000, mp.clone()))
            .unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 330_000);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 330_000);

        // 2. COD payment confirmed: 200,000 VND
        let pay_cod = Uuid::new_v4();
        assert!(ledger
            .post(&build_cod_confirm_tx(pay_cod, 200_000, mp.clone()))
            .unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_COD_RECEIVABLE), 200_000);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 530_000);

        // 3. COD reconciled from shipping carrier
        assert!(ledger
            .post(&build_cod_reconcile_tx(pay_cod, 200_000, mp.clone()))
            .unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_COD_RECEIVABLE), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 530_000);

        // 4. Order completes -> escrow settled to seller_pending + platform_fee_revenue + shipping_fee_payable
        let settle_tx =
            build_escrow_settle_tx("ord-qr-1", "shop-A", 285_000, 15_000, 30_000, mp.clone())
                .unwrap();
        assert!(ledger.post(&settle_tx).unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(
            ledger.balance_of(&account_seller_pending("shop-A")),
            285_000
        );
        assert_eq!(ledger.balance_of(ACCOUNT_PLATFORM_FEE_REVENUE), 15_000);
        assert_eq!(ledger.balance_of(ACCOUNT_SHIPPING_FEE_PAYABLE), 30_000);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 200_000);

        // 5. Escrow released -> seller_pending moves to seller_available
        let release_tx =
            build_escrow_release_tx("ord-qr-1", "shop-A", 285_000, mp.clone()).unwrap();
        assert!(ledger.post(&release_tx).unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(&account_seller_pending("shop-A")), 0);
        assert_eq!(
            ledger.balance_of(&account_seller_available("shop-A")),
            285_000
        );

        // 6. Seller payout of 200,000 VND
        let payout_id = Uuid::new_v4();
        let payout_tx = build_seller_payout_tx(payout_id, "shop-A", 200_000, mp.clone()).unwrap();
        assert!(ledger.post(&payout_tx).unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(
            ledger.balance_of(&account_seller_available("shop-A")),
            85_000
        );
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 330_000);

        // 7. Partial refund on COD order (50,000 VND) + disburse
        let refund_id = Uuid::new_v4();
        assert!(ledger
            .post(&build_refund_tx(
                refund_id,
                pay_cod,
                50_000,
                "partial return",
                mp.clone()
            ))
            .unwrap());
        assert!(ledger
            .post(&build_refund_settle_tx(refund_id, 50_000, mp.clone()))
            .unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 150_000);

        // 8. Late webhook on expired payment -> booked to refund_payable
        assert!(ledger
            .post(&build_late_payment_refund_tx("BANK-TX-LATE-1", 120_000, mp))
            .unwrap());

        let summary = ledger.summary();
        assert!(summary.is_balanced);
        assert_eq!(summary.total_debits_vnd, summary.total_credits_vnd);
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 120_000);
    }

    #[test]
    fn test_sales_channel_ledger_partitioning_matches_unfiltered_100_percent() {
        let mut ledger = InMemoryLedger::default();
        let mp = ChannelAttribution::marketplace();
        let sf_a = ChannelAttribution::storefront("shop-A").unwrap();

        // MARKETPLACE VietQR + escrow settle + release
        let pay_mp = Uuid::new_v4();
        ledger
            .post(&build_vietqr_confirm_tx(pay_mp, 330_000, mp.clone()))
            .unwrap();
        ledger
            .post(
                &build_escrow_settle_tx("ord-mp-1", "shop-A", 285_000, 15_000, 30_000, mp.clone())
                    .unwrap(),
            )
            .unwrap();
        ledger
            .post(&build_escrow_release_tx("ord-mp-1", "shop-A", 285_000, mp).unwrap())
            .unwrap();

        // STOREFRONT VietQR + refund + COD
        let pay_sf_qr = Uuid::new_v4();
        let pay_sf_cod = Uuid::new_v4();
        let refund_sf = Uuid::new_v4();
        ledger
            .post(&build_vietqr_confirm_tx(pay_sf_qr, 250_000, sf_a.clone()))
            .unwrap();
        ledger
            .post(&build_refund_tx(
                refund_sf,
                pay_sf_qr,
                100_000,
                "storefront partial refund",
                sf_a.clone(),
            ))
            .unwrap();
        ledger
            .post(&build_cod_confirm_tx(pay_sf_cod, 180_000, sf_a.clone()))
            .unwrap();

        // Verify refund metadata retained original STOREFRONT attribution
        let refund_tx = ledger
            .transactions()
            .iter()
            .find(|t| t.reference_type == "refund")
            .unwrap();
        assert_eq!(refund_tx.channel, sf_a);
        assert_eq!(
            refund_tx.metadata,
            serde_json::json!({"sales_channel": "STOREFRONT", "storefront_shop_id": "shop-A"})
        );

        let all = ledger.summary_for_channel(None);
        let only_mp = ledger.summary_for_channel(Some(SalesChannel::Marketplace));
        let only_sf = ledger.summary_for_channel(Some(SalesChannel::Storefront));

        assert!(all.is_balanced);
        assert!(only_mp.is_balanced);
        assert!(only_sf.is_balanced);

        assert_eq!(all.sales_channel, "");
        assert_eq!(only_mp.sales_channel, "MARKETPLACE");
        assert_eq!(only_sf.sales_channel, "STOREFRONT");

        assert_eq!(
            only_mp.total_debits_vnd + only_sf.total_debits_vnd,
            all.total_debits_vnd
        );
        assert_eq!(
            only_mp.total_credits_vnd + only_sf.total_credits_vnd,
            all.total_credits_vnd
        );
        assert_eq!(
            only_mp.transaction_count + only_sf.transaction_count,
            all.transaction_count
        );

        // Every account's debits, credits, and signed balance add up to 100% of unfiltered
        assert_eq!(only_mp.accounts.len(), all.accounts.len());
        assert_eq!(only_sf.accounts.len(), all.accounts.len());
        for i in 0..all.accounts.len() {
            assert_eq!(all.accounts[i].account_id, only_mp.accounts[i].account_id);
            assert_eq!(all.accounts[i].account_id, only_sf.accounts[i].account_id);
            assert_eq!(
                only_mp.accounts[i].total_debits_vnd + only_sf.accounts[i].total_debits_vnd,
                all.accounts[i].total_debits_vnd
            );
            assert_eq!(
                only_mp.accounts[i].total_credits_vnd + only_sf.accounts[i].total_credits_vnd,
                all.accounts[i].total_credits_vnd
            );
            assert_eq!(
                only_mp.accounts[i].balance_vnd + only_sf.accounts[i].balance_vnd,
                all.accounts[i].balance_vnd
            );
        }
    }
}
