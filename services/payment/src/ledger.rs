use std::collections::{HashMap, HashSet};

use serde::{Deserialize, Serialize};
use sqlx::{PgPool, Postgres, Row, Transaction};
use uuid::Uuid;

use crate::error::{PaymentError, Result};

// Canonical system ledger account codes
pub const ACCOUNT_GATEWAY_CLEARING: &str = "gateway_clearing";
pub const ACCOUNT_COD_RECEIVABLE: &str = "cod_receivable";
pub const ACCOUNT_ESCROW_LIABILITY: &str = "escrow_liability";
pub const ACCOUNT_PLATFORM_FEE_REVENUE: &str = "platform_fee_revenue";
pub const ACCOUNT_PLATFORM_PROMO_EXPENSE: &str = "platform_promo_expense";
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
    pub lines: Vec<LedgerLine>,
}

impl LedgerTransactionDraft {
    /// Strict Rust-side double-entry validation BEFORE hitting PostgreSQL.
    /// Guarantees:
    /// 1. Non-empty idempotency key & reference fields
    /// 2. At least 2 entries
    /// 3. Every entry is strictly positive on exactly one side (debit OR credit, never both/zero/negative)
    /// 4. sum(debit_vnd) == sum(credit_vnd) > 0 with overflow protection
    pub fn validate(&self) -> Result<()> {
        if self.idempotency_key.trim().is_empty() {
            return Err(PaymentError::LedgerInvariant(
                "idempotency_key must not be empty".to_string(),
            ));
        }
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
pub fn build_vietqr_confirm_tx(payment_id: Uuid, amount_vnd: i64) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("vietqr-confirm:{payment_id}"),
        reference_type: "payment_vietqr".to_string(),
        reference_id: payment_id.to_string(),
        description: format!("VietQR payment confirmed for payment {payment_id}"),
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

/// COD collected upon sub-order delivery (`order.delivered`) or completion:
/// Debit cod_receivable, Credit escrow_liability.
/// Scoped per `(payment_id, order_id, shop_id)` when `shop_id` is present so multi-shop
/// COD allocations sharing a single `order_id` never collide on `idempotency_key` (review2-P P2-7).
pub fn build_cod_collect_tx(
    payment_id: Uuid,
    order_id: &str,
    shop_id: Option<&str>,
    amount_vnd: i64,
) -> LedgerTransactionDraft {
    let idempotency_key = match shop_id.map(str::trim).filter(|s| !s.is_empty()) {
        Some(shop) => format!("cod-collect:{payment_id}:{order_id}:{shop}"),
        None => format!("cod-collect:{payment_id}:{order_id}"),
    };
    LedgerTransactionDraft {
        idempotency_key,
        reference_type: "payment_cod".to_string(),
        reference_id: payment_id.to_string(),
        description: format!(
            "COD collected on delivery for order {order_id} (payment {payment_id})"
        ),
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
pub fn build_cod_reconcile_tx(payment_id: Uuid, amount_vnd: i64) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("cod-reconcile:{payment_id}"),
        reference_type: "cod_reconcile".to_string(),
        reference_id: payment_id.to_string(),
        description: format!("COD collected from carrier for payment {payment_id}"),
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
/// Reverses escrow liability into refund_payable.
pub fn build_refund_tx(
    refund_id: Uuid,
    payment_id: Uuid,
    amount_vnd: i64,
    reason: &str,
) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("refund-accrual:{refund_id}"),
        reference_type: "refund".to_string(),
        reference_id: refund_id.to_string(),
        description: format!("Refund {refund_id} for payment {payment_id}: {reason}"),
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

/// Refund disbursement settled out via gateway/bank:
/// Debit refund_payable, Credit gateway_clearing.
pub fn build_refund_settle_tx(refund_id: Uuid, amount_vnd: i64) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key: format!("refund-settle:{refund_id}"),
        reference_type: "refund_settle".to_string(),
        reference_id: refund_id.to_string(),
        description: format!("Refund {refund_id} disbursed to buyer"),
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
) -> LedgerTransactionDraft {
    build_gateway_clearing_refund_tx(
        format!("late-webhook-refund:{provider_tx_id}"),
        "late_payment_refund",
        provider_tx_id,
        format!("Late/superseded webhook {provider_tx_id} booked to refund_payable"),
        amount_vnd,
    )
}

/// Reconciliation overpayment excess or manual mismatched-transfer refund (UC-A14 / PAY-B9):
/// Debit gateway_clearing, Credit refund_payable (never touches escrow_liability).
pub fn build_reconciliation_refund_tx(
    idempotency_key: &str,
    reference_id: &str,
    amount_vnd: i64,
    description: &str,
) -> LedgerTransactionDraft {
    build_gateway_clearing_refund_tx(
        idempotency_key.to_string(),
        "reconciliation_refund",
        reference_id,
        description.to_string(),
        amount_vnd,
    )
}

fn build_gateway_clearing_refund_tx(
    idempotency_key: String,
    reference_type: &str,
    reference_id: &str,
    description: String,
    amount_vnd: i64,
) -> LedgerTransactionDraft {
    LedgerTransactionDraft {
        idempotency_key,
        reference_type: reference_type.to_string(),
        reference_id: reference_id.to_string(),
        description,
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

/// Post-settlement refund (R4 / PAY-B2):
/// Claws back `seller_pending:<shop_id>` first, then `seller_available:<shop_id>`, crediting `refund_payable`.
/// Never touches `escrow_liability`.
pub fn build_post_settlement_refund_tx(
    refund_id: Uuid,
    payment_id: Uuid,
    shop_id: &str,
    from_pending_vnd: i64,
    from_available_vnd: i64,
    reason: &str,
) -> Result<LedgerTransactionDraft> {
    if from_pending_vnd < 0 || from_available_vnd < 0 {
        return Err(PaymentError::LedgerInvariant(
            "post-settlement refund clawback amounts must be non-negative".to_string(),
        ));
    }
    let total = from_pending_vnd
        .checked_add(from_available_vnd)
        .ok_or_else(|| {
            PaymentError::LedgerInvariant("post-settlement refund overflow".to_string())
        })?;

    let mut lines = Vec::with_capacity(3);
    if from_pending_vnd > 0 {
        lines.push(LedgerLine::debit(
            account_seller_pending(shop_id),
            AccountType::Liability,
            Some(shop_id.to_string()),
            from_pending_vnd,
        ));
    }
    if from_available_vnd > 0 {
        lines.push(LedgerLine::debit(
            account_seller_available(shop_id),
            AccountType::Liability,
            Some(shop_id.to_string()),
            from_available_vnd,
        ));
    }
    lines.push(LedgerLine::credit(
        ACCOUNT_REFUND_PAYABLE,
        AccountType::Liability,
        None,
        total,
    ));

    let draft = LedgerTransactionDraft {
        idempotency_key: format!("refund-accrual:{refund_id}"),
        reference_type: "refund".to_string(),
        reference_id: refund_id.to_string(),
        description: format!(
            "Post-settlement refund {refund_id} for payment {payment_id} shop {shop_id}: {reason}"
        ),
        lines,
    };
    draft.validate()?;
    Ok(draft)
}

/// Order completed -> Debit escrow_liability (P - refunded) + Debit platform_promo_expense (D),
/// Credit seller_pending:<shop_id>, platform_fee_revenue, shipping_fee_payable.
pub fn build_escrow_settle_tx(
    order_id: &str,
    shop_id: &str,
    escrow_debit_vnd: i64,
    platform_promo_debit_vnd: i64,
    seller_net_vnd: i64,
    commission_vnd: i64,
    shipping_fee_vnd: i64,
) -> Result<LedgerTransactionDraft> {
    if escrow_debit_vnd < 0
        || platform_promo_debit_vnd < 0
        || seller_net_vnd < 0
        || commission_vnd < 0
        || shipping_fee_vnd < 0
    {
        return Err(PaymentError::LedgerInvariant(
            "escrow settle amounts must be non-negative".to_string(),
        ));
    }

    let mut lines = Vec::with_capacity(5);
    if escrow_debit_vnd > 0 {
        lines.push(LedgerLine::debit(
            ACCOUNT_ESCROW_LIABILITY,
            AccountType::Liability,
            None,
            escrow_debit_vnd,
        ));
    }
    if platform_promo_debit_vnd > 0 {
        lines.push(LedgerLine::debit(
            ACCOUNT_PLATFORM_PROMO_EXPENSE,
            AccountType::Expense,
            None,
            platform_promo_debit_vnd,
        ));
    }

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
) -> Result<LedgerTransactionDraft> {
    let draft = LedgerTransactionDraft {
        idempotency_key: format!("escrow-release:{order_id}:{shop_id}"),
        reference_type: "escrow_release".to_string(),
        reference_id: order_id.to_string(),
        description: format!(
            "Release escrow to available balance for order {order_id} shop {shop_id}"
        ),
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
) -> Result<LedgerTransactionDraft> {
    let draft = LedgerTransactionDraft {
        idempotency_key: format!("seller-payout:{payout_id}"),
        reference_type: "payout".to_string(),
        reference_id: payout_id.to_string(),
        description: format!("Payout {payout_id} to shop {shop_id}"),
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

    let inserted_id: Option<Uuid> = sqlx::query_scalar(
        r#"
        INSERT INTO ledger_transactions (idempotency_key, reference_type, reference_id, description)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (idempotency_key) DO NOTHING
        RETURNING id
        "#,
    )
    .bind(&draft.idempotency_key)
    .bind(&draft.reference_type)
    .bind(&draft.reference_id)
    .bind(&draft.description)
    .fetch_optional(&mut **tx)
    .await?;

    let Some(tx_id) = inserted_id else {
        return Ok(None);
    };

    for line in &draft.lines {
        sqlx::query(
            r#"
            INSERT INTO ledger_entries (transaction_id, account_id, debit_vnd, credit_vnd)
            VALUES ($1, $2, $3, $4)
            "#,
        )
        .bind(tx_id)
        .bind(&line.account_id)
        .bind(line.debit_vnd)
        .bind(line.credit_vnd)
        .execute(&mut **tx)
        .await?;
    }

    Ok(Some(tx_id))
}

/// Queries global ledger integrity and per-account balances from PostgreSQL.
pub async fn get_ledger_summary(pool: &PgPool) -> Result<LedgerSummary> {
    let totals = sqlx::query(
        r#"
        SELECT
            COALESCE(SUM(debit_vnd), 0)::BIGINT AS total_debits_vnd,
            COALESCE(SUM(credit_vnd), 0)::BIGINT AS total_credits_vnd
        FROM ledger_entries
        "#,
    )
    .fetch_one(pool)
    .await?;

    let total_debits_vnd: i64 = totals.try_get("total_debits_vnd")?;
    let total_credits_vnd: i64 = totals.try_get("total_credits_vnd")?;

    let transaction_count: i64 =
        sqlx::query_scalar("SELECT COUNT(*)::BIGINT FROM ledger_transactions")
            .fetch_one(pool)
            .await?;

    let unbalanced_transactions: i64 = sqlx::query_scalar(
        r#"
        SELECT COUNT(*)::BIGINT FROM (
            SELECT transaction_id
            FROM ledger_entries
            GROUP BY transaction_id
            HAVING SUM(debit_vnd) <> SUM(credit_vnd) OR COUNT(*) < 2
        ) unbalanced
        "#,
    )
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
        LEFT JOIN ledger_entries e ON e.account_id = a.id
        GROUP BY a.id, a.account_type
        ORDER BY a.id ASC
        "#,
    )
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
        total_debits_vnd,
        total_credits_vnd,
        is_balanced,
        unbalanced_transactions,
        transaction_count,
        accounts,
    })
}

/// Computes ledger-backed pending, available, and net total earned VND balances for a shop (UC-S17 / PAY-B12).
/// `total_earned_vnd` equals total settled credits into `seller_pending:<shop>` minus any
/// post-settlement refund clawbacks (`reference_type = 'refund'`) debited from seller accounts;
/// releases to available and payouts do not reduce `total_earned_vnd`.
pub async fn get_seller_balance(pool: &PgPool, shop_id: &str) -> Result<SellerBalance> {
    let pending_acct = account_seller_pending(shop_id);
    let available_acct = account_seller_available(shop_id);

    let row = sqlx::query(
        r#"
        SELECT
            COALESCE(SUM(CASE WHEN e.account_id = $1 THEN e.credit_vnd - e.debit_vnd ELSE 0 END), 0)::BIGINT AS pending_vnd,
            COALESCE(SUM(CASE WHEN e.account_id = $2 THEN e.credit_vnd - e.debit_vnd ELSE 0 END), 0)::BIGINT AS available_vnd,
            (
                COALESCE(SUM(CASE WHEN e.account_id = $1 THEN e.credit_vnd ELSE 0 END), 0)
                - COALESCE(SUM(CASE WHEN lt.reference_type = 'refund' THEN e.debit_vnd ELSE 0 END), 0)
            )::BIGINT AS total_earned_vnd
        FROM ledger_entries e
        JOIN ledger_transactions lt ON lt.id = e.transaction_id
        WHERE e.account_id IN ($1, $2)
        "#,
    )
    .bind(&pending_acct)
    .bind(&available_acct)
    .fetch_one(pool)
    .await?;

    let pending_vnd: i64 = row.try_get("pending_vnd")?;
    let available_vnd: i64 = row.try_get("available_vnd")?;
    let total_earned_vnd: i64 = row.try_get("total_earned_vnd")?;

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

/// Locks both `seller_available:<shop_id>` and `seller_pending:<shop_id>` in deterministic
/// alphabetical order inside an active DB transaction and returns `(pending_vnd, available_vnd)`.
pub async fn lock_and_get_seller_balances_tx(
    tx: &mut Transaction<'_, Postgres>,
    shop_id: &str,
) -> Result<(i64, i64)> {
    let available_acct = account_seller_available(shop_id);
    let pending_acct = account_seller_pending(shop_id);

    // Ensure both accounts exist before locking in alphabetical order (available < pending).
    for acct in [&available_acct, &pending_acct] {
        sqlx::query(
            r#"
            INSERT INTO ledger_accounts (id, account_type, shop_id)
            VALUES ($1, 'LIABILITY', $2)
            ON CONFLICT (id) DO NOTHING
            "#,
        )
        .bind(acct)
        .bind(shop_id)
        .execute(&mut **tx)
        .await?;
    }

    sqlx::query(
        r#"
        SELECT id FROM ledger_accounts
        WHERE id IN ($1, $2)
        ORDER BY id ASC
        FOR UPDATE
        "#,
    )
    .bind(&available_acct)
    .bind(&pending_acct)
    .fetch_all(&mut **tx)
    .await?;

    let pending_vnd: i64 = sqlx::query_scalar(
        r#"
        SELECT COALESCE(SUM(credit_vnd) - SUM(debit_vnd), 0)::BIGINT
        FROM ledger_entries
        WHERE account_id = $1
        "#,
    )
    .bind(&pending_acct)
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

    Ok((pending_vnd, available_vnd))
}

// =============================================================================
// In-Memory Reference Ledger (for exhaustive unit testing of all accounting flows)
// =============================================================================

#[derive(Debug, Default)]
pub struct InMemoryLedger {
    seen_keys: HashSet<String>,
    accounts: HashMap<String, (AccountType, i64, i64)>,
    refund_debits_by_account: HashMap<String, i64>,
    tx_count: i64,
}

impl InMemoryLedger {
    pub fn post(&mut self, draft: &LedgerTransactionDraft) -> Result<bool> {
        draft.validate()?;
        if !self.seen_keys.insert(draft.idempotency_key.clone()) {
            return Ok(false);
        }
        for line in &draft.lines {
            let entry =
                self.accounts
                    .entry(line.account_id.clone())
                    .or_insert((line.account_type, 0, 0));
            entry.1 += line.debit_vnd;
            entry.2 += line.credit_vnd;
            if draft.reference_type == "refund" && line.debit_vnd > 0 {
                *self
                    .refund_debits_by_account
                    .entry(line.account_id.clone())
                    .or_insert(0) += line.debit_vnd;
            }
        }
        self.tx_count += 1;
        Ok(true)
    }

    pub fn seller_balance(&self, shop_id: &str) -> SellerBalance {
        let pending_acct = account_seller_pending(shop_id);
        let available_acct = account_seller_available(shop_id);
        let pending_vnd = self.balance_of(&pending_acct);
        let available_vnd = self.balance_of(&available_acct);
        let settled_credits = self
            .accounts
            .get(&pending_acct)
            .map(|(_, _, c)| *c)
            .unwrap_or(0);
        let refund_clawbacks = self
            .refund_debits_by_account
            .get(&pending_acct)
            .copied()
            .unwrap_or(0)
            + self
                .refund_debits_by_account
                .get(&available_acct)
                .copied()
                .unwrap_or(0);
        SellerBalance {
            shop_id: shop_id.to_string(),
            pending_vnd,
            available_vnd,
            total_earned_vnd: settled_credits - refund_clawbacks,
        }
    }

    pub fn balance_of(&self, account_id: &str) -> i64 {
        self.accounts
            .get(account_id)
            .map(|(ty, d, c)| ty.signed_balance(*d, *c))
            .unwrap_or(0)
    }

    pub fn summary(&self) -> LedgerSummary {
        let mut total_debits = 0_i64;
        let mut total_credits = 0_i64;
        let mut accounts: Vec<AccountBalance> = self
            .accounts
            .iter()
            .map(|(id, (ty, d, c))| {
                total_debits += *d;
                total_credits += *c;
                AccountBalance {
                    account_id: id.clone(),
                    account_type: ty.as_str().to_string(),
                    total_debits_vnd: *d,
                    total_credits_vnd: *c,
                    balance_vnd: ty.signed_balance(*d, *c),
                }
            })
            .collect();
        accounts.sort_by(|a, b| a.account_id.cmp(&b.account_id));

        LedgerSummary {
            total_debits_vnd: total_debits,
            total_credits_vnd: total_credits,
            is_balanced: total_debits == total_credits,
            unbalanced_transactions: 0,
            transaction_count: self.tx_count,
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

        // 1. VietQR payment confirmed: 330,000 VND (shop subtotal 300,000, commission 15,000, shipping 30,000)
        let pay_qr = Uuid::new_v4();
        assert!(ledger
            .post(&build_vietqr_confirm_tx(pay_qr, 330_000))
            .unwrap());
        // Duplicate posting with same idempotency_key is a no-op
        assert!(!ledger
            .post(&build_vietqr_confirm_tx(pay_qr, 330_000))
            .unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 330_000);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 330_000);

        // 2. COD payment collected on delivery: 200,000 VND
        let pay_cod = Uuid::new_v4();
        assert!(ledger
            .post(&build_cod_collect_tx(
                pay_cod,
                "ord-cod-life",
                Some("shop-B"),
                200_000
            ))
            .unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_COD_RECEIVABLE), 200_000);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 530_000);

        // 3. COD reconciled from shipping carrier
        assert!(ledger
            .post(&build_cod_reconcile_tx(pay_cod, 200_000))
            .unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_COD_RECEIVABLE), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 530_000);

        // 4. Order completes -> escrow settled to seller_pending + platform_fee_revenue + shipping_fee_payable
        let settle_tx =
            build_escrow_settle_tx("ord-qr-1", "shop-A", 330_000, 0, 285_000, 15_000, 30_000)
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
        let release_tx = build_escrow_release_tx("ord-qr-1", "shop-A", 285_000).unwrap();
        assert!(ledger.post(&release_tx).unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(&account_seller_pending("shop-A")), 0);
        assert_eq!(
            ledger.balance_of(&account_seller_available("shop-A")),
            285_000
        );

        // 6. Seller payout of 200,000 VND
        let payout_id = Uuid::new_v4();
        let payout_tx = build_seller_payout_tx(payout_id, "shop-A", 200_000).unwrap();
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
                "partial return"
            ))
            .unwrap());
        assert!(ledger
            .post(&build_refund_settle_tx(refund_id, 50_000))
            .unwrap());
        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 150_000);

        // 8. Late webhook on expired payment -> booked to refund_payable
        assert!(ledger
            .post(&build_late_payment_refund_tx("BANK-TX-LATE-1", 120_000))
            .unwrap());

        let summary = ledger.summary();
        assert!(summary.is_balanced);
        assert_eq!(summary.total_debits_vnd, summary.total_credits_vnd);
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 120_000);
    }

    #[test]
    fn test_r1_settlement_with_platform_promo_expense_exact_example() {
        // Brief example: G=229,000, voucher san=30,000, commission=11,450, Ship=26,000 => P=225,000
        // Seller MUST receive 217,550 and platform_promo_expense = 30,000.
        let mut ledger = InMemoryLedger::default();
        let pay_id = Uuid::new_v4();

        assert!(ledger
            .post(&build_vietqr_confirm_tx(pay_id, 225_000))
            .unwrap());
        let settle =
            build_escrow_settle_tx("ord-r1", "shop-1", 225_000, 30_000, 217_550, 11_450, 26_000)
                .unwrap();
        assert!(ledger.post(&settle).unwrap());

        let summary = ledger.summary();
        assert!(summary.is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_PLATFORM_PROMO_EXPENSE), 30_000);
        assert_eq!(
            ledger.balance_of(&account_seller_pending("shop-1")),
            217_550
        );
        assert_eq!(ledger.balance_of(ACCOUNT_PLATFORM_FEE_REVENUE), 11_450);
        assert_eq!(ledger.balance_of(ACCOUNT_SHIPPING_FEE_PAYABLE), 26_000);
    }

    #[test]
    fn test_r1_zero_payable_order_settles_without_gateway_clearing() {
        // 0 VND order: R=200,000, C=10,000, Ship=20,000, D=220,000, P=0.
        // No gateway_clearing entry; settlement debits platform_promo_expense 220,000 and pays seller 190,000.
        let mut ledger = InMemoryLedger::default();
        let settle =
            build_escrow_settle_tx("ord-zero", "shop-z", 0, 220_000, 190_000, 10_000, 20_000)
                .unwrap();
        assert!(ledger.post(&settle).unwrap());
        let release = build_escrow_release_tx("ord-zero", "shop-z", 190_000).unwrap();
        assert!(ledger.post(&release).unwrap());

        assert!(ledger.summary().is_balanced);
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_PLATFORM_PROMO_EXPENSE), 220_000);
        assert_eq!(
            ledger.balance_of(&account_seller_available("shop-z")),
            190_000
        );
    }

    #[test]
    fn test_r2_cod_awaiting_to_delivered_and_cancel_leaves_zero_entries() {
        // Cancelled COD before delivery -> 0 transactions posted, cod_receivable = 0, escrow = 0.
        let cancelled_ledger = InMemoryLedger::default();
        assert_eq!(cancelled_ledger.balance_of(ACCOUNT_COD_RECEIVABLE), 0);
        assert_eq!(cancelled_ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 0);
        assert_eq!(cancelled_ledger.summary().transaction_count, 0);

        // Multi-shop COD: sub-order 1 delivers -> posts cod_receivable -> escrow_liability for P1 only.
        let mut ledger = InMemoryLedger::default();
        let pay_id = Uuid::new_v4();
        let collect1 = build_cod_collect_tx(pay_id, "ord-cod-1", Some("shop-1"), 256_500);
        assert!(ledger.post(&collect1).unwrap());
        // Idempotent replay of order.delivered for ord-cod-1 is a no-op
        assert!(!ledger.post(&collect1).unwrap());
        assert_eq!(ledger.balance_of(ACCOUNT_COD_RECEIVABLE), 256_500);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 256_500);
        assert!(ledger.summary().is_balanced);

        // review2-P P2-7: Two COD allocations sharing the same `order_id` with distinct `shop_id`
        // must both post to cod_receivable + escrow_liability without colliding on idempotency_key.
        let shared_pay = Uuid::new_v4();
        assert!(ledger
            .post(&build_cod_collect_tx(
                shared_pay,
                "grp-shared",
                Some("shop-A"),
                100_000
            ))
            .unwrap());
        assert!(ledger
            .post(&build_cod_collect_tx(
                shared_pay,
                "grp-shared",
                Some("shop-B"),
                200_000
            ))
            .unwrap());
        assert_eq!(ledger.balance_of(ACCOUNT_COD_RECEIVABLE), 556_500);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 556_500);
        assert!(ledger.summary().is_balanced);
    }

    #[test]
    fn test_r4_partial_refund_before_and_after_settlement_never_overdraws_escrow() {
        let mut ledger = InMemoryLedger::default();
        let pay_id = Uuid::new_v4();

        // 2-shop VietQR payment: Shop1 P1=255,000 (R=229k, C=11.45k, Ship=26k), Shop2 P2=463,000 (R=429k, C=21.45k, Ship=34k)
        assert!(ledger
            .post(&build_vietqr_confirm_tx(pay_id, 718_000))
            .unwrap());

        // Case A: Partial refund 10,000 on Shop 2 BEFORE settlement
        let rf_before = Uuid::new_v4();
        assert!(ledger
            .post(&build_refund_tx(rf_before, pay_id, 10_000, "partial"))
            .unwrap());
        // Settle Shop 2 with escrow_debit = 463,000 - 10,000 = 453,000; seller_net = 407,550 - 10,000 = 397,550
        let settle2 =
            build_escrow_settle_tx("ord-2", "shop-2", 453_000, 0, 397_550, 21_450, 34_000).unwrap();
        assert!(ledger.post(&settle2).unwrap());
        assert!(ledger
            .post(&build_escrow_release_tx("ord-2", "shop-2", 397_550).unwrap())
            .unwrap());
        // Escrow remaining MUST equal exactly Shop 1's P1 = 255,000 (not 245,000!)
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 255_000);
        assert_eq!(
            ledger.balance_of(&account_seller_available("shop-2")),
            397_550
        );

        // Case B: Settle + release Shop 1 (217,550 -> available), then post-settlement refund 5,000
        let settle1 =
            build_escrow_settle_tx("ord-1", "shop-1", 255_000, 0, 217_550, 11_450, 26_000).unwrap();
        assert!(ledger.post(&settle1).unwrap());
        assert!(ledger
            .post(&build_escrow_release_tx("ord-1", "shop-1", 217_550).unwrap())
            .unwrap());
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 0);

        let rf_after = Uuid::new_v4();
        let clawback = build_post_settlement_refund_tx(
            rf_after,
            pay_id,
            "shop-1",
            0,
            5_000,
            "post-settle refund",
        )
        .unwrap();
        assert!(ledger.post(&clawback).unwrap());

        assert!(ledger.summary().is_balanced);
        // Escrow stays at 0 (NEVER negative!) and shop-1 available dropped by 5,000
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 0);
        assert_eq!(
            ledger.balance_of(&account_seller_available("shop-1")),
            212_550
        );
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 15_000);
    }

    #[test]
    fn test_w2_two_step_refund_settlement_and_reconciliation_ledger_balance() {
        let mut ledger = InMemoryLedger::default();
        let pay_id = Uuid::new_v4();

        // 1. Confirm VietQR payment of 250,000 VND
        assert!(ledger
            .post(&build_vietqr_confirm_tx(pay_id, 250_000))
            .unwrap());

        // 2. Two-step refund: CreateRefund -> Credit refund_payable, SettleRefund (2x) -> only 1 posting, refund_payable returns to 0
        let rf_id = Uuid::new_v4();
        assert!(ledger
            .post(&build_refund_tx(rf_id, pay_id, 100_000, "partial"))
            .unwrap());
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 100_000);
        assert!(ledger
            .post(&build_refund_settle_tx(rf_id, 100_000))
            .unwrap());
        assert!(!ledger
            .post(&build_refund_settle_tx(rf_id, 100_000))
            .unwrap());
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 150_000);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 150_000);

        // 3. Reconciliation ASSIGN overpay: expected 200k, actual 230k (excess 30k)
        let pay_over = Uuid::new_v4();
        assert!(ledger
            .post(&build_vietqr_confirm_tx(pay_over, 200_000))
            .unwrap());
        assert!(ledger
            .post(&build_reconciliation_refund_tx(
                "recon-overpay:item-1",
                "TX-OVER-1",
                30_000,
                "overpayment excess",
            ))
            .unwrap());
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 350_000);
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 30_000);
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 380_000);

        let overpay_rf_id = Uuid::new_v4();
        assert!(ledger
            .post(&build_refund_settle_tx(overpay_rf_id, 30_000))
            .unwrap());
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 350_000);

        // 4. Reconciliation REFUND (mismatched transfer 90k returned to buyer)
        assert!(ledger
            .post(&build_reconciliation_refund_tx(
                "recon-refund:item-2",
                "TX-MISMATCH-2",
                90_000,
                "mismatched refund",
            ))
            .unwrap());
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 90_000);
        assert_eq!(ledger.balance_of(ACCOUNT_ESCROW_LIABILITY), 350_000);

        let mismatch_rf_id = Uuid::new_v4();
        assert!(ledger
            .post(&build_refund_settle_tx(mismatch_rf_id, 90_000))
            .unwrap());
        assert_eq!(ledger.balance_of(ACCOUNT_REFUND_PAYABLE), 0);
        assert_eq!(ledger.balance_of(ACCOUNT_GATEWAY_CLEARING), 350_000);
        assert!(ledger.summary().is_balanced);
    }

    #[test]
    fn test_w2_seller_balance_total_earned_reflects_post_settlement_clawback() {
        let mut ledger = InMemoryLedger::default();
        let pay_id = Uuid::new_v4();
        assert!(ledger
            .post(&build_vietqr_confirm_tx(pay_id, 255_000))
            .unwrap());
        assert!(ledger
            .post(
                &build_escrow_settle_tx("ord-1", "shop-x", 255_000, 0, 217_550, 11_450, 26_000)
                    .unwrap()
            )
            .unwrap());

        let b1 = ledger.seller_balance("shop-x");
        assert_eq!(b1.pending_vnd, 217_550);
        assert_eq!(b1.available_vnd, 0);
        assert_eq!(b1.total_earned_vnd, 217_550);

        // Release to available does not change total_earned_vnd
        assert!(ledger
            .post(&build_escrow_release_tx("ord-1", "shop-x", 217_550).unwrap())
            .unwrap());
        let b2 = ledger.seller_balance("shop-x");
        assert_eq!(b2.pending_vnd, 0);
        assert_eq!(b2.available_vnd, 217_550);
        assert_eq!(b2.total_earned_vnd, 217_550);

        // Payout does not reduce total_earned_vnd
        assert!(ledger
            .post(&build_seller_payout_tx(Uuid::new_v4(), "shop-x", 100_000).unwrap())
            .unwrap());
        let b3 = ledger.seller_balance("shop-x");
        assert_eq!(b3.available_vnd, 117_550);
        assert_eq!(b3.total_earned_vnd, 217_550);

        // Post-settlement refund clawback reduces both available_vnd and total_earned_vnd
        assert!(ledger
            .post(
                &build_post_settlement_refund_tx(
                    Uuid::new_v4(),
                    pay_id,
                    "shop-x",
                    0,
                    17_550,
                    "post-settlement refund",
                )
                .unwrap()
            )
            .unwrap());
        let b4 = ledger.seller_balance("shop-x");
        assert_eq!(b4.available_vnd, 100_000);
        assert_eq!(b4.total_earned_vnd, 200_000);
        assert!(ledger.summary().is_balanced);
    }
}
