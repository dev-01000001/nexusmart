use std::collections::{HashMap, HashSet};

use chrono::{DateTime, Duration, NaiveDate, Utc};
use serde::{Deserialize, Serialize};
use serde_json::json;
use sqlx::{PgPool, Postgres, Row, Transaction};
use tokio::sync::broadcast;
use tracing::{error, info, warn};
use uuid::Uuid;

use crate::config::{Config, VietQrMode, ZaloPayMode};
use crate::domain::{
    allocation_buyer_payable_vnd, cod_reconcile_mismatch_provider_tx_id,
    cod_reconcile_provider_tx_id, compute_capped_escrow_release_vnd,
    compute_post_settlement_clawback, compute_reconciliation_assign_split,
    compute_settlement_split, compute_suborder_refund_amount, decide_cod_allocation_reconciliation,
    decide_reconciliation_match_action, decide_return_allocation_action,
    decide_zalopay_window_exhausted, find_cod_carrier_mismatch, format_seller_ledger_description,
    format_settlement_note_with_masked_account, is_online_method,
    is_payment_already_cancelled_terminal, late_payment_refund_key, normalize_cod_carrier_code,
    order_cancel_refund_key, order_expiry_candidate_indices, plan_cod_shipping_fee_adjustment,
    plan_initial_payment, reconciliation_channel_matches, reconciliation_overpay_key,
    reconciliation_provider_matches_method, reconciliation_refund_key, resolve_cod_payment_status,
    select_payment_for_group_action, should_require_large_refund_approval, utc_to_vn_date,
    validate_adjust_cod_shipping_fee_input, validate_allocations_sum,
    validate_cod_fee_adjustment_replay, validate_cod_reconciliation_rows,
    validate_group_can_create_payment, validate_payment_idempotency_match,
    validate_payment_method_eligibility, validate_reconciliation_provider_tx_accountable,
    validate_refund_bank_account, validate_refund_eligibility, validate_refund_idempotency_match,
    validate_resolve_reconciliation_idempotency_match, validate_set_fee_config,
    validate_settle_refund_idempotency_match, validate_suborder_refund_cap,
    validate_supersede_previous_payments, validate_update_payment_method_config, vn_day_utc_bounds,
    vn_today, zalopay_late_sweep_backoff, AdjustCodShippingFeeInput, AdjustCodShippingFeeOutput,
    AssignAmountSplit, ChannelAttribution, CodAllocationMatchDecision, CodFeeAdjustmentRecord,
    CodReconciliationRowInput, CreatePaymentInput, CreateRefundInput, CreateRefundOutput,
    DecideRefundApprovalInput, GetFeeConfigFilter, GetFeeConfigResult, GroupPaymentSelection,
    ImportCodReconciliationInput, ListReconciliationQueueResult, ListReconciliationRunsFilter,
    ListReconciliationRunsResult, ListRefundsFilter, ListRefundsResult, ListSellerLedgerFilter,
    ListSellerLedgerResult, PaymentFeeConfigRecord, PaymentMethod, PaymentMethodConfigRecord,
    PaymentRecord, PaymentSseEvent, PaymentWithAllocations, ProviderTxAccountingState,
    ReconciliationActionKind, ReconciliationDecision, ReconciliationItemRecord,
    ReconciliationQueueItem, ReconciliationReport, ReconciliationRunRecord, RefundRecord,
    ResolveReconciliationInput, ResolveReconciliationOutput, ReturnAllocationAction, SalesChannel,
    SellerLedgerAccountFilter, SellerLedgerEntryRecord, SellerPayoutRecord, SetFeeConfigInput,
    SettleRefundInput, SettleRefundOutput, ShopAllocationRecord, SubmitRefundBankAccountInput,
    UpdatePaymentMethodConfigInput, ZaloPayWindowExhaustedDecision, ERR_COD_ALREADY_COLLECTED,
    PROVIDER_FINAL_STATUS_AMOUNT_MISMATCH, PROVIDER_FINAL_STATUS_LATE_REFUND_QUEUED,
    PROVIDER_FINAL_STATUS_MANUAL_REVIEW, PROVIDER_FINAL_STATUS_NOT_PAID,
    PROVIDER_FINAL_STATUS_PAID_CONFIRMED, REFUND_PROVIDER_STATUS_FAILED,
    REFUND_PROVIDER_STATUS_PENDING, REFUND_PROVIDER_STATUS_PROCESSING,
    REFUND_PROVIDER_STATUS_SUCCEEDED, ZALOPAY_DEFAULT_EXPIRY_MINUTES,
};
use crate::error::{PaymentError, Result};
use crate::ledger::{
    self, build_cod_collect_tx, build_cod_reconcile_tx, build_escrow_release_tx,
    build_escrow_settle_tx, build_late_payment_refund_tx, build_post_settlement_refund_tx,
    build_reconciliation_refund_tx, build_refund_settle_tx, build_refund_tx,
    build_seller_payout_tx, build_vietqr_confirm_tx, build_zalopay_confirm_tx, LedgerSummary,
    SellerBalance,
};
use crate::metrics::Metrics;
use crate::telemetry::{mask_bank_account, mask_secret};
use crate::vietqr::{
    extract_occurred_at_from_json, parse_provider_timestamp, ProviderTransaction, VietQrAdapter,
};
use crate::webhook_security::{
    decide_webhook_action, IncomingWebhookPayload, WebhookDecision, WebhookEventStatus,
};
use crate::zalopay::{
    check_server_time_window, generate_app_trans_id, generate_m_refund_id,
    is_zalopay_refund_transient_sub_code, m_refund_id_matches_vn_day,
    rotate_m_refund_id_for_vn_day, validate_zalopay_pay_url, verify_callback, ZaloPayAdapter,
    ZaloPayCallbackData, ZaloPayCallbackRequest, ZaloPayCreateOrderInput, ZaloPayRefundInput,
    ZALOPAY_CALLBACK_RETURN_INVALID_MAC, ZALOPAY_CALLBACK_RETURN_RETRY,
    ZALOPAY_CALLBACK_RETURN_SUCCESS, ZALOPAY_RETURN_FAIL, ZALOPAY_RETURN_PROCESSING,
    ZALOPAY_RETURN_SUCCESS,
};

pub const FEE_CONFIG_ACTIVE_BY_METHOD_SQL: &str = r#"
SELECT id, seq, payment_method, fee_bps, effective_from, created_by, note, created_at
FROM payment_fee_configs
WHERE payment_method = $1
  AND effective_from <= $2
ORDER BY payment_method ASC, effective_from DESC, seq DESC
LIMIT 1
"#;

pub const FEE_CONFIG_ACTIVE_ALL_SQL: &str = r#"
SELECT DISTINCT ON (payment_method)
    id, seq, payment_method, fee_bps, effective_from, created_by, note, created_at
FROM payment_fee_configs
WHERE effective_from <= $1
ORDER BY payment_method ASC, effective_from DESC, seq DESC
"#;

pub const FEE_CONFIG_HISTORY_BY_METHOD_SQL: &str = r#"
SELECT id, seq, payment_method, fee_bps, effective_from, created_by, note, created_at
FROM payment_fee_configs
WHERE payment_method = $1
ORDER BY payment_method ASC, effective_from DESC, seq DESC
"#;

pub const FEE_CONFIG_HISTORY_ALL_SQL: &str = r#"
SELECT id, seq, payment_method, fee_bps, effective_from, created_by, note, created_at
FROM payment_fee_configs
ORDER BY effective_from DESC, seq DESC
"#;

/// Timeout (10 minutes) after which a `cod_file` reconciliation run still in `'running'`
/// state is considered stranded (e.g. abrupt process shutdown) and swept to `'failed'`.
pub const STALE_COD_RECONCILIATION_RUN_TIMEOUT_SECS: i64 = 600;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebhookProcessResult {
    pub status: String,
    pub provider_tx_id: String,
    pub payment_id: Option<Uuid>,
    pub duplicate: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EscrowReleaseResult {
    pub order_id: String,
    pub released: bool,
    pub released_amount_vnd: i64,
    pub already_released: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct ZaloPayLateSweepStats {
    pub claimed: usize,
    pub late_refunds_queued: usize,
    pub amount_mismatches: usize,
    pub concluded_not_paid: usize,
    pub retried: usize,
    pub escalated_manual_review: usize,
}

#[derive(Debug, Clone, sqlx::FromRow)]
struct ZaloPayLateSweepRow {
    id: Uuid,
    order_group_id: String,
    order_id: String,
    provider: String,
    amount_vnd: i64,
    provider_order_code: String,
    expires_at: Option<DateTime<Utc>>,
    created_at: DateTime<Utc>,
    provider_check_attempts: i32,
}

/// Result of processing a `POST /webhooks/zalopay` callback (F2, F2-bis, F5).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ZaloPayCallbackOutcome {
    Confirmed {
        payment_id: Uuid,
        provider_tx_id: String,
    },
    Duplicate {
        payment_id: Option<Uuid>,
        provider_tx_id: String,
    },
    QueuedReconciliation {
        payment_id: Option<Uuid>,
        provider_tx_id: String,
        reason: String,
    },
    LateRefundQueued {
        payment_id: Uuid,
        provider_tx_id: String,
        reason: String,
    },
    Ignored {
        reason: String,
    },
    InvalidMac,
    Rejected {
        reason: String,
    },
    RetryableError {
        reason: String,
    },
}

impl ZaloPayCallbackOutcome {
    /// Returns the exact ZaloPay callback response `(return_code, return_message)` per F2-bis:
    /// `1` (success / duplicate / queued / terminal ignore), `-1` (invalid MAC / rejected),
    /// `0` (temporary error / processing so ZaloPay retries up to 3x).
    pub fn zalopay_return_code(&self) -> (i32, &'static str) {
        match self {
            Self::Confirmed { .. } => (ZALOPAY_CALLBACK_RETURN_SUCCESS, "success"),
            Self::Duplicate { .. } => (ZALOPAY_CALLBACK_RETURN_SUCCESS, "success"),
            Self::QueuedReconciliation { .. } => (ZALOPAY_CALLBACK_RETURN_SUCCESS, "success"),
            Self::LateRefundQueued { .. } => (ZALOPAY_CALLBACK_RETURN_SUCCESS, "success"),
            Self::Ignored { .. } => (ZALOPAY_CALLBACK_RETURN_SUCCESS, "success"),
            Self::InvalidMac => (ZALOPAY_CALLBACK_RETURN_INVALID_MAC, "mac not equal"),
            Self::Rejected { .. } => (ZALOPAY_CALLBACK_RETURN_INVALID_MAC, "rejected"),
            Self::RetryableError { .. } => (ZALOPAY_CALLBACK_RETURN_RETRY, "retry"),
        }
    }
}

#[derive(Clone)]
pub struct PaymentService {
    pool: PgPool,
    config: Config,
    vietqr: VietQrAdapter,
    zalopay: ZaloPayAdapter,
    metrics: Metrics,
    sse_tx: broadcast::Sender<PaymentSseEvent>,
}

impl PaymentService {
    pub fn new(pool: PgPool, config: Config, metrics: Metrics) -> Self {
        let vietqr = VietQrAdapter::from_config(&config);
        let zalopay = ZaloPayAdapter::from_config(&config)
            .expect("valid ZaloPayConfig when constructing PaymentService");
        let (sse_tx, _) = broadcast::channel(512);
        Self {
            pool,
            config,
            vietqr,
            zalopay,
            metrics,
            sse_tx,
        }
    }

    /// Replaces the ZaloPay adapter (used by tests to inject controllable `MockZaloPay` outcomes).
    pub fn with_zalopay(mut self, zalopay: ZaloPayAdapter) -> Self {
        self.zalopay = zalopay;
        self
    }

    pub fn pool(&self) -> &PgPool {
        &self.pool
    }

    pub fn config(&self) -> &Config {
        &self.config
    }

    pub fn metrics(&self) -> &Metrics {
        &self.metrics
    }

    pub fn vietqr(&self) -> &VietQrAdapter {
        &self.vietqr
    }

    pub fn zalopay(&self) -> &ZaloPayAdapter {
        &self.zalopay
    }

    /// Returns safe, redacted credential status for admin payment-method config views (F1/F6).
    /// Never exposes plaintext keys, tokens, or secrets.
    pub fn method_credentials_info(&self, method: &str) -> (bool, String) {
        match method.trim().to_ascii_uppercase().as_str() {
            "COD" => (true, String::new()),
            "VIETQR" => match self.config.vietqr_mode {
                VietQrMode::Mock => (
                    true,
                    format!(
                        "mode=mock bank={} acc={}",
                        self.config.vietqr_bank_bin,
                        mask_bank_account(&self.config.vietqr_account_no)
                    ),
                ),
                VietQrMode::PayOs => {
                    let configured = !self.config.payos_client_id.trim().is_empty()
                        && !self.config.payos_api_key.trim().is_empty()
                        && !self.config.payos_checksum_key.trim().is_empty();
                    let preview = if configured {
                        format!(
                            "mode=payos client_id={}",
                            mask_secret(&self.config.payos_client_id)
                        )
                    } else {
                        "mode=payos (unconfigured)".to_string()
                    };
                    (configured, preview)
                }
                VietQrMode::Sepay => {
                    let configured = !self.config.sepay_api_token.trim().is_empty();
                    let preview = if configured {
                        format!(
                            "mode=sepay bank={} token={}",
                            self.config.sepay_bank_code,
                            mask_secret(&self.config.sepay_api_token)
                        )
                    } else {
                        "mode=sepay (unconfigured)".to_string()
                    };
                    (configured, preview)
                }
            },
            "ZALOPAY" => match self.zalopay.mode() {
                ZaloPayMode::Mock => (
                    true,
                    format!(
                        "mode=mock app_id={} key1={} key2={}",
                        self.zalopay.config().effective_app_id().unwrap_or(1),
                        mask_secret(self.zalopay.effective_key1()),
                        mask_secret(self.zalopay.effective_key2())
                    ),
                ),
                ZaloPayMode::Sandbox => {
                    let configured = self.zalopay.config().credentials_configured();
                    let preview = if configured {
                        format!(
                            "mode=sandbox {}",
                            self.zalopay.config().credential_preview()
                        )
                    } else {
                        "mode=sandbox (unconfigured)".to_string()
                    };
                    (configured, preview)
                }
            },
            _ => (false, String::new()),
        }
    }

    /// Reads `payment_method_configs` directly from PostgreSQL without caching (F6).
    pub async fn get_payment_method_configs(
        &self,
        method_filter: Option<PaymentMethod>,
    ) -> Result<Vec<PaymentMethodConfigRecord>> {
        let rows = if let Some(method) = method_filter {
            sqlx::query_as::<_, PaymentMethodConfigRecord>(
                r#"
                SELECT method, enabled, min_order_vnd, max_order_vnd, expiry_minutes,
                       version, note, updated_by, updated_at
                FROM payment_method_configs
                WHERE method = $1
                "#,
            )
            .bind(method.as_str())
            .fetch_all(&self.pool)
            .await?
        } else {
            sqlx::query_as::<_, PaymentMethodConfigRecord>(
                r#"
                SELECT method, enabled, min_order_vnd, max_order_vnd, expiry_minutes,
                       version, note, updated_by, updated_at
                FROM payment_method_configs
                ORDER BY
                    CASE method
                        WHEN 'COD' THEN 1
                        WHEN 'VIETQR' THEN 2
                        WHEN 'ZALOPAY' THEN 3
                        ELSE 99
                    END
                "#,
            )
            .fetch_all(&self.pool)
            .await?
        };
        Ok(rows)
    }

    /// Updates a payment method configuration with optimistic locking (`expected_version`) and
    /// prevents disabling the last remaining enabled payment method (F6/F7).
    pub async fn update_payment_method_config(
        &self,
        input: UpdatePaymentMethodConfigInput,
    ) -> Result<PaymentMethodConfigRecord> {
        validate_update_payment_method_config(&input)?;
        let target_method = input.method.as_str();
        let note = input.note.trim().to_string();
        let updated_by = input.updated_by.trim().to_string();

        let mut tx = self.pool.begin().await?;

        // Lock all payment_method_configs rows in deterministic alphabetical order so two
        // concurrent disables of the last two enabled methods can NEVER both succeed (F7).
        let all_configs = sqlx::query_as::<_, PaymentMethodConfigRecord>(
            r#"
            SELECT method, enabled, min_order_vnd, max_order_vnd, expiry_minutes,
                   version, note, updated_by, updated_at
            FROM payment_method_configs
            ORDER BY method ASC
            FOR UPDATE
            "#,
        )
        .fetch_all(&mut *tx)
        .await?;

        let Some(current) = all_configs.iter().find(|c| c.method == target_method) else {
            return Err(PaymentError::NotFound(format!(
                "payment method config '{target_method}' not found"
            )));
        };

        if current.version != input.expected_version {
            return Err(PaymentError::FailedPrecondition(format!(
                "VERSION_CONFLICT: expected version {}, current version {} for {}",
                input.expected_version, current.version, target_method
            )));
        }

        if !input.enabled {
            let other_enabled_count = all_configs
                .iter()
                .filter(|c| c.method != target_method && c.enabled)
                .count();
            if other_enabled_count == 0 {
                return Err(PaymentError::Validation(
                    "cannot disable the last enabled payment method".to_string(),
                ));
            }
        }

        let updated = sqlx::query_as::<_, PaymentMethodConfigRecord>(
            r#"
            UPDATE payment_method_configs
            SET enabled = $1,
                min_order_vnd = $2,
                max_order_vnd = $3,
                expiry_minutes = $4,
                note = $5,
                updated_by = $6,
                updated_at = NOW(),
                version = version + 1
            WHERE method = $7 AND version = $8
            RETURNING method, enabled, min_order_vnd, max_order_vnd, expiry_minutes,
                      version, note, updated_by, updated_at
            "#,
        )
        .bind(input.enabled)
        .bind(input.min_order_vnd)
        .bind(input.max_order_vnd)
        .bind(input.expiry_minutes)
        .bind(&note)
        .bind(&updated_by)
        .bind(target_method)
        .bind(input.expected_version)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or_else(|| {
            PaymentError::FailedPrecondition(format!(
                "VERSION_CONFLICT: concurrent modification for {target_method}"
            ))
        })?;

        tx.commit().await?;
        info!(
            method = target_method,
            enabled = updated.enabled,
            min_order_vnd = updated.min_order_vnd,
            max_order_vnd = updated.max_order_vnd,
            expiry_minutes = updated.expiry_minutes,
            version = updated.version,
            updated_by = %updated.updated_by,
            "payment method configuration updated"
        );
        Ok(updated)
    }

    pub fn subscribe_sse(&self) -> broadcast::Receiver<PaymentSseEvent> {
        self.sse_tx.subscribe()
    }

    pub async fn check_ready(&self) -> Result<()> {
        sqlx::query_scalar::<_, i32>("SELECT 1")
            .fetch_one(&self.pool)
            .await?;
        Ok(())
    }

    fn emit_sse(&self, payment: &PaymentRecord) {
        let _ = self.sse_tx.send(PaymentSseEvent {
            payment_id: payment.id,
            order_group_id: payment.order_group_id.clone(),
            order_id: payment.order_id.clone(),
            status: payment.status.clone(),
            amount_vnd: payment.amount_vnd,
            provider_tx_id: payment.provider_tx_id.clone(),
            updated_at: payment.updated_at,
        });
    }

    /// Creates a COD or VietQR payment intent.
    /// - Idempotent when `idempotency_key` matches an existing payment with identical caller & params.
    /// - Implements UC-B25 (retry / change method): supersedes any existing `pending` payment for the
    ///   same `order_id` or `order_group_id`; rejects if already succeeded/refunded or terminal.
    /// - For COD (`amount_vnd > 0`): starts in `pending` / `awaiting_collection` with NO ledger
    ///   posting until delivery (`order.delivered`) or completion (`order.completed` / `ReleaseEscrow`).
    /// - For 0 VND fully-subsidized orders: starts in `succeeded` / `held` with no QR and no clearing tx.
    /// - For VietQR (`amount_vnd > 0`): generates QR link/payload via VietQrAdapter with 15-minute default expiry.
    pub async fn create_payment(
        &self,
        input: CreatePaymentInput,
    ) -> Result<PaymentWithAllocations> {
        let order_group_id = if !input.order_group_id.trim().is_empty() {
            input.order_group_id.trim().to_string()
        } else {
            input.order_id.trim().to_string()
        };
        let order_id = if !input.order_id.trim().is_empty() {
            input.order_id.trim().to_string()
        } else {
            order_group_id.clone()
        };

        if order_id.is_empty() {
            return Err(PaymentError::Validation(
                "order_group_id or order_id is required".to_string(),
            ));
        }
        if input.user_id.trim().is_empty() {
            return Err(PaymentError::Validation("user_id is required".to_string()));
        }

        // Validate R1 money model: amount_vnd >= 0 and sum(R + Ship - D) == amount_vnd
        validate_allocations_sum(input.amount_vnd, &input.shop_allocations)?;
        let channel =
            ChannelAttribution::from_parts(&input.sales_channel, &input.storefront_shop_id)?;
        channel.validate_allocations(&input.shop_allocations)?;
        let initial_plan = plan_initial_payment(input.method, input.amount_vnd)?;

        // Fast idempotency check before external VietQR call (P0-2: verify caller & params match)
        if let Some(ref key) = input.idempotency_key {
            if !key.trim().is_empty() {
                if let Some(existing) = sqlx::query_as::<_, PaymentRecord>(
                    "SELECT * FROM payments WHERE idempotency_key = $1",
                )
                .bind(key)
                .fetch_optional(&self.pool)
                .await?
                {
                    validate_payment_idempotency_match(
                        &existing,
                        &input.user_id,
                        &order_group_id,
                        input.amount_vnd,
                        input.method,
                    )?;
                    if ChannelAttribution::from_db(
                        &existing.sales_channel,
                        &existing.storefront_shop_id,
                    ) != channel
                    {
                        return Err(PaymentError::Conflict(
                            "idempotency_key already used with different payment parameters"
                                .to_string(),
                        ));
                    }
                    let allocations = fetch_allocations_pool(&self.pool, existing.id).await?;
                    return Ok(PaymentWithAllocations {
                        payment: existing,
                        allocations,
                    });
                }
            }
        }

        // F6/F7: Load payment_method_configs directly from DB (fail closed if unreadable/missing).
        let method_cfg = sqlx::query_as::<_, PaymentMethodConfigRecord>(
            r#"
            SELECT method, enabled, min_order_vnd, max_order_vnd, expiry_minutes,
                   version, note, updated_by, updated_at
            FROM payment_method_configs
            WHERE method = $1
            "#,
        )
        .bind(input.method.as_str())
        .fetch_optional(&self.pool)
        .await?
        .ok_or_else(|| {
            PaymentError::Internal(format!(
                "payment method config '{}' missing from payment_method_configs",
                input.method.as_str()
            ))
        })?;
        validate_payment_method_eligibility(&method_cfg, input.method, input.amount_vnd)?;

        let payment_id = Uuid::new_v4();
        let now = Utc::now();
        let expires_at = if initial_plan.create_qr {
            Some(input.expires_at.unwrap_or_else(|| {
                if method_cfg.expiry_minutes > 0 {
                    now + Duration::minutes(i64::from(method_cfg.expiry_minutes))
                } else {
                    now + Duration::seconds(self.config.vietqr_expiry_secs)
                }
            }))
        } else if initial_plan.create_zalopay {
            let mins = if method_cfg.expiry_minutes >= 5 {
                method_cfg.expiry_minutes
            } else {
                ZALOPAY_DEFAULT_EXPIRY_MINUTES
            };
            Some(
                input
                    .expires_at
                    .unwrap_or_else(|| now + Duration::minutes(i64::from(mins))),
            )
        } else {
            None
        };

        let (provider, provider_order_code, qr_code, qr_url, pay_url) = if initial_plan.create_qr {
            let intent = self
                .vietqr
                .create_qr_intent(
                    payment_id,
                    &order_id,
                    input.amount_vnd,
                    expires_at.unwrap_or(now),
                )
                .await?;
            (
                intent.provider,
                intent.provider_order_code,
                Some(intent.qr_code),
                Some(intent.qr_url),
                None,
            )
        } else if initial_plan.create_zalopay {
            let app_trans_id = generate_app_trans_id(payment_id, now);
            let exp_dt = expires_at.unwrap_or_else(|| {
                now + Duration::minutes(i64::from(ZALOPAY_DEFAULT_EXPIRY_MINUTES))
            });
            let expire_duration_seconds = (exp_dt - now).num_seconds();
            let resp = self
                .zalopay
                .create_order(&ZaloPayCreateOrderInput {
                    app_trans_id: app_trans_id.clone(),
                    app_user: input.user_id.clone(),
                    amount_vnd: input.amount_vnd,
                    description: format!("NexusMart thanh toan don {order_id}"),
                    embed_data: None,
                    item: None,
                    callback_url: None,
                    expire_duration_seconds: Some(expire_duration_seconds),
                    bank_code: None,
                    sub_app_id: None,
                    app_time: Some(now),
                })
                .await?;
            if resp.return_code != ZALOPAY_RETURN_SUCCESS {
                return Err(PaymentError::Provider(format!(
                    "zalopay create_order failed: return_code={} sub_return_code={} msg={}",
                    resp.return_code, resp.sub_return_code, resp.return_message
                )));
            }
            let safe_pay_url = validate_zalopay_pay_url(&resp.order_url, self.zalopay.mode())?;
            let qr_code = (!resp.qr_code.trim().is_empty()).then_some(resp.qr_code);
            (
                self.zalopay.mode().provider_name().to_string(),
                app_trans_id,
                qr_code,
                None,
                Some(safe_pay_url),
            )
        } else {
            let provider = match input.method {
                PaymentMethod::Cod => "cod".to_string(),
                PaymentMethod::VietQr | PaymentMethod::ZaloPay => "zero_payable".to_string(),
            };
            (
                provider,
                VietQrAdapter::generate_order_code(payment_id, &order_id),
                None,
                None,
                None,
            )
        };
        let initial_status = initial_plan.initial_status;
        let paid_at = initial_plan.mark_paid_now.then(Utc::now);

        let mut tx = self.pool.begin().await?;

        // Serialize concurrent create/confirm operations for this order / checkout group (P1-5).
        // Acquiring the group lock before the in-TX idempotency check prevents a concurrent
        // buyer/service CreatePayment (e.g. 0-VND payment starting in `succeeded`) that commits
        // between the fast check and the lock from tripping `validate_group_can_create_payment`.
        let existing_group_payments =
            lock_payment_group_tx(&mut tx, &order_id, &order_group_id).await?;

        // Re-check idempotency inside transaction after acquiring group lock (P0-2 / W2b-r5):
        // returns the existing payment verbatim with its real status (including expired/cancelled/
        // succeeded) without resurrecting or superseding it.
        if let Some(ref key) = input.idempotency_key {
            if !key.trim().is_empty() {
                if let Some(existing) = sqlx::query_as::<_, PaymentRecord>(
                    "SELECT * FROM payments WHERE idempotency_key = $1",
                )
                .bind(key)
                .fetch_optional(&mut *tx)
                .await?
                {
                    validate_payment_idempotency_match(
                        &existing,
                        &input.user_id,
                        &order_group_id,
                        input.amount_vnd,
                        input.method,
                    )?;
                    if ChannelAttribution::from_db(
                        &existing.sales_channel,
                        &existing.storefront_shop_id,
                    ) != channel
                    {
                        return Err(PaymentError::Conflict(
                            "idempotency_key already used with different payment parameters"
                                .to_string(),
                        ));
                    }
                    let allocations = fetch_allocations_tx(&mut tx, existing.id).await?;
                    tx.commit().await?;
                    return Ok(PaymentWithAllocations {
                        payment: existing,
                        allocations,
                    });
                }
            }
        }

        // F6/F7: Re-verify method config under FOR SHARE inside the transaction so a concurrent
        // admin disable/bounds update cannot race past the pre-call check.
        let locked_method_cfg = sqlx::query_as::<_, PaymentMethodConfigRecord>(
            r#"
            SELECT method, enabled, min_order_vnd, max_order_vnd, expiry_minutes,
                   version, note, updated_by, updated_at
            FROM payment_method_configs
            WHERE method = $1
            FOR SHARE
            "#,
        )
        .bind(input.method.as_str())
        .fetch_optional(&mut *tx)
        .await?
        .ok_or_else(|| {
            PaymentError::Internal(format!(
                "payment method config '{}' missing from payment_method_configs",
                input.method.as_str()
            ))
        })?;
        validate_payment_method_eligibility(&locked_method_cfg, input.method, input.amount_vnd)?;

        // UC-B25 / R3 / P2-7: reject if group already succeeded/refunded, or if supersede_previous
        // targets a group without an active pending payment or with cancelled/expired/failed history.
        let existing_statuses: Vec<&str> = existing_group_payments
            .iter()
            .map(|p| p.status.as_str())
            .collect();
        validate_group_can_create_payment(&existing_statuses, input.supersede_previous)?;

        // Enforce immutability of sales_channel & storefront_shop_id across payment retries/supersedes.
        if existing_group_payments.iter().any(|p| {
            ChannelAttribution::from_db(&p.sales_channel, &p.storefront_shop_id) != channel
        }) {
            return Err(PaymentError::Validation(format!(
                "sales_channel and storefront_shop_id are immutable for order {order_id}"
            )));
        }

        // F5 / p4 Item K: Defense-in-depth guards for `supersede_previous = true`:
        // old payment must be online, same `amount_vnd`, zero held/settled/refunded allocations,
        // and new online expiry must not exceed old expiry.
        if input.supersede_previous {
            let old_pending: Vec<PaymentRecord> = existing_group_payments
                .iter()
                .filter(|p| p.status == "pending")
                .cloned()
                .collect();
            let old_pending_ids: Vec<Uuid> = old_pending.iter().map(|p| p.id).collect();
            let old_alloc_statuses: Vec<String> = if old_pending_ids.is_empty() {
                Vec::new()
            } else {
                sqlx::query_scalar(
                    r#"
                    SELECT escrow_status
                    FROM payment_shop_allocations
                    WHERE payment_id = ANY($1)
                    ORDER BY id ASC
                    FOR UPDATE
                    "#,
                )
                .bind(&old_pending_ids)
                .fetch_all(&mut *tx)
                .await?
            };
            let status_refs: Vec<&str> = old_alloc_statuses.iter().map(String::as_str).collect();
            validate_supersede_previous_payments(
                &old_pending,
                &status_refs,
                input.method,
                input.amount_vnd,
                expires_at,
            )?;
        }

        // UC-B25: supersede prior pending intents for the same order / order_group
        let superseded_ids: Vec<Uuid> = if input.supersede_previous {
            sqlx::query_scalar(
                r#"
                UPDATE payments
                SET status = 'superseded', updated_at = NOW()
                WHERE (order_id = $1 OR order_group_id = $2) AND status = 'pending'
                RETURNING id
                "#,
            )
            .bind(&order_id)
            .bind(&order_group_id)
            .fetch_all(&mut *tx)
            .await?
        } else {
            sqlx::query_scalar(
                r#"
                UPDATE payments
                SET status = 'superseded', updated_at = NOW()
                WHERE order_id = $1 AND status = 'pending'
                RETURNING id
                "#,
            )
            .bind(&order_id)
            .fetch_all(&mut *tx)
            .await?
        };

        for old_id in superseded_ids {
            sqlx::query(
                r#"
                UPDATE payment_shop_allocations
                SET escrow_status = 'cancelled'
                WHERE payment_id = $1 AND escrow_status IN ('awaiting_collection', 'held')
                "#,
            )
            .bind(old_id)
            .execute(&mut *tx)
            .await?;
        }

        let insert_res = sqlx::query_as::<_, PaymentRecord>(
            r#"
            INSERT INTO payments (
                id, order_group_id, order_id, user_id, method, provider,
                amount_vnd, status, provider_order_code, qr_code, qr_url, pay_url,
                idempotency_key, expires_at, paid_at, trace_id,
                sales_channel, storefront_shop_id
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
            RETURNING *
            "#,
        )
        .bind(payment_id)
        .bind(&order_group_id)
        .bind(&order_id)
        .bind(&input.user_id)
        .bind(input.method.as_str())
        .bind(&provider)
        .bind(input.amount_vnd)
        .bind(initial_status)
        .bind(&provider_order_code)
        .bind(&qr_code)
        .bind(&qr_url)
        .bind(&pay_url)
        .bind(
            input
                .idempotency_key
                .as_deref()
                .filter(|s| !s.trim().is_empty()),
        )
        .bind(expires_at)
        .bind(paid_at)
        .bind(&input.trace_id)
        .bind(channel.sales_channel.as_str())
        .bind(&channel.storefront_shop_id)
        .fetch_one(&mut *tx)
        .await;

        let payment = match insert_res {
            Ok(p) => p,
            Err(err)
                if is_unique_violation(&err, "idempotency_key")
                    || is_unique_violation(&err, "idx_payments_one_succeeded_per_order")
                    || is_unique_violation(&err, "idx_payments_one_succeeded_per_group") =>
            {
                // Concurrent duplicate idempotency_key race (including 0-VND payments where a
                // partial succeeded unique index can fire alongside payments_idempotency_key_key):
                // roll back and return the winning row if its idempotency_key & params match.
                let _ = tx.rollback().await;
                if let Some(ref key) = input.idempotency_key {
                    if !key.trim().is_empty() {
                        if let Some(existing) = sqlx::query_as::<_, PaymentRecord>(
                            "SELECT * FROM payments WHERE idempotency_key = $1",
                        )
                        .bind(key)
                        .fetch_optional(&self.pool)
                        .await?
                        {
                            validate_payment_idempotency_match(
                                &existing,
                                &input.user_id,
                                &order_group_id,
                                input.amount_vnd,
                                input.method,
                            )?;
                            if ChannelAttribution::from_db(
                                &existing.sales_channel,
                                &existing.storefront_shop_id,
                            ) != channel
                            {
                                return Err(PaymentError::Conflict(
                                    "idempotency_key already used with different payment parameters"
                                        .to_string(),
                                ));
                            }
                            let allocations =
                                fetch_allocations_pool(&self.pool, existing.id).await?;
                            return Ok(PaymentWithAllocations {
                                payment: existing,
                                allocations,
                            });
                        }
                    }
                }
                if is_unique_violation(&err, "idx_payments_one_succeeded_per_order")
                    || is_unique_violation(&err, "idx_payments_one_succeeded_per_group")
                {
                    return Err(PaymentError::Conflict(format!(
                        "order {order_id} already has a succeeded payment"
                    )));
                }
                return Err(PaymentError::Database(err));
            }
            Err(err) => return Err(PaymentError::Database(err)),
        };

        let mut allocations = Vec::with_capacity(input.shop_allocations.len());
        for alloc in &input.shop_allocations {
            let alloc_order_id = alloc
                .order_id
                .as_deref()
                .filter(|s| !s.trim().is_empty())
                .unwrap_or(&order_id);
            let rec = sqlx::query_as::<_, ShopAllocationRecord>(
                r#"
                INSERT INTO payment_shop_allocations (
                    payment_id, order_id, shop_id, subtotal_vnd, commission_vnd,
                    shipping_fee_vnd, platform_discount_vnd, escrow_status,
                    sales_channel, storefront_shop_id
                )
                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
                RETURNING *
                "#,
            )
            .bind(payment.id)
            .bind(alloc_order_id)
            .bind(&alloc.shop_id)
            .bind(alloc.subtotal_vnd)
            .bind(alloc.commission_vnd)
            .bind(alloc.shipping_fee_vnd)
            .bind(alloc.platform_discount_vnd)
            .bind(initial_plan.initial_escrow_status)
            .bind(channel.sales_channel.as_str())
            .bind(&channel.storefront_shop_id)
            .fetch_one(&mut *tx)
            .await?;
            allocations.push(rec);
        }

        // Note (R2 COD & R1 0 VND):
        // - COD starts in `pending` / `awaiting_collection` with NO ledger posting until `order.delivered`.
        // - 0 VND starts in `succeeded` / `held` with NO QR and NO `gateway_clearing` posting.

        tx.commit().await?;
        self.emit_sse(&payment);

        info!(
            trace_id = %input.trace_id,
            payment_id = %payment.id,
            order_group_id = %payment.order_group_id,
            order_id = %payment.order_id,
            method = %payment.method,
            amount_vnd = payment.amount_vnd,
            status = %payment.status,
            "payment intent created"
        );

        Ok(PaymentWithAllocations {
            payment,
            allocations,
        })
    }

    /// Adjusts `shipping_fee_vnd` (increase or decrease) on a single sub-order's uncollected COD
    /// allocation (`payment.method == "COD"`, `payment.status == "pending"`,
    /// `allocation.escrow_status == "awaiting_collection"`) and recomputes parent `payments.amount_vnd`
    /// over all non-cancelled allocations in a single ACID transaction with zero ledger postings (G2).
    pub async fn adjust_cod_shipping_fee(
        &self,
        input: AdjustCodShippingFeeInput,
    ) -> Result<AdjustCodShippingFeeOutput> {
        let (order_id, idempotency_key) = validate_adjust_cod_shipping_fee_input(
            &input.order_id,
            &input.idempotency_key,
            input.new_shipping_fee_vnd,
        )?;

        let mut tx = self.pool.begin().await?;

        if let Some(existing) = fetch_cod_fee_adjustment_by_key_tx(&mut tx, idempotency_key).await?
        {
            let out = build_cod_adjustment_replay_tx(
                &mut tx,
                &existing,
                order_id,
                input.new_shipping_fee_vnd,
            )
            .await?;
            tx.commit().await?;
            return Ok(out);
        }

        let group_keys: Option<(String, String)> = sqlx::query_as(
            r#"
            SELECT p.order_id, p.order_group_id
            FROM payments p
            LEFT JOIN payment_shop_allocations a ON a.payment_id = p.id
            WHERE a.order_id = $1 OR p.order_id = $1
            ORDER BY
                CASE p.status
                    WHEN 'succeeded' THEN 1
                    WHEN 'pending' THEN 2
                    ELSE 3
                END,
                p.created_at DESC
            LIMIT 1
            "#,
        )
        .bind(order_id)
        .fetch_optional(&mut *tx)
        .await?;

        let Some((pay_order_id, pay_group_id)) = group_keys else {
            return Err(PaymentError::NotFound(format!(
                "payment not found for order {order_id}"
            )));
        };

        // Lock checkout payment group in deterministic `ORDER BY id ASC FOR UPDATE` order
        // (matching `collect_cod_order_allocations_tx` / `order.delivered` to prevent deadlocks).
        let mut group = lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;
        if select_payment_for_group_action(&group) == GroupPaymentSelection::AllSupersededRetry {
            group = lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;
        }
        let payment = match select_payment_for_group_action(&group) {
            GroupPaymentSelection::Found(idx) => group.swap_remove(idx),
            GroupPaymentSelection::Empty => {
                return Err(PaymentError::NotFound(format!(
                    "payment not found for order {order_id}"
                )));
            }
            GroupPaymentSelection::AllSupersededRetry => {
                return Err(PaymentError::FailedPrecondition(format!(
                    "{ERR_COD_ALREADY_COLLECTED}: payment was superseded"
                )));
            }
        };

        // Re-check idempotency after acquiring the payment row lock so concurrent duplicate calls
        // serialize cleanly into `already_applied = true` even if `order.delivered` followed.
        if let Some(existing) = fetch_cod_fee_adjustment_by_key_tx(&mut tx, idempotency_key).await?
        {
            let out = build_cod_adjustment_replay_tx(
                &mut tx,
                &existing,
                order_id,
                input.new_shipping_fee_vnd,
            )
            .await?;
            tx.commit().await?;
            return Ok(out);
        }

        let locked_allocs = sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT * FROM payment_shop_allocations
            WHERE payment_id = $1
            ORDER BY id ASC
            FOR UPDATE
            "#,
        )
        .bind(payment.id)
        .fetch_all(&mut *tx)
        .await?;

        let plan = plan_cod_shipping_fee_adjustment(
            &payment,
            &locked_allocs,
            order_id,
            input.new_shipping_fee_vnd,
        )?;

        sqlx::query(
            r#"
            UPDATE payment_shop_allocations
            SET shipping_fee_vnd = $1
            WHERE id = $2
            "#,
        )
        .bind(plan.new_shipping_fee_vnd)
        .bind(plan.allocation_id)
        .execute(&mut *tx)
        .await?;

        let updated_payment = sqlx::query_as::<_, PaymentRecord>(
            r#"
            UPDATE payments
            SET amount_vnd = $1, updated_at = NOW()
            WHERE id = $2
            RETURNING *
            "#,
        )
        .bind(plan.new_payment_amount_vnd)
        .bind(payment.id)
        .fetch_one(&mut *tx)
        .await?;

        let adj_insert = sqlx::query(
            r#"
            INSERT INTO cod_fee_adjustments (
                idempotency_key, payment_id, order_id,
                old_shipping_fee_vnd, new_shipping_fee_vnd, new_order_payable_vnd
            )
            VALUES ($1, $2, $3, $4, $5, $6)
            "#,
        )
        .bind(idempotency_key)
        .bind(payment.id)
        .bind(order_id)
        .bind(plan.old_shipping_fee_vnd)
        .bind(plan.new_shipping_fee_vnd)
        .bind(plan.new_order_payable_vnd)
        .execute(&mut *tx)
        .await;

        if let Err(err) = adj_insert {
            if is_unique_violation(&err, "idempotency_key") {
                let _ = tx.rollback().await;
                if let Some(existing) = sqlx::query_as::<_, CodFeeAdjustmentRecord>(
                    "SELECT * FROM cod_fee_adjustments WHERE idempotency_key = $1",
                )
                .bind(idempotency_key)
                .fetch_optional(&self.pool)
                .await?
                {
                    validate_cod_fee_adjustment_replay(
                        &existing,
                        order_id,
                        input.new_shipping_fee_vnd,
                    )?;
                    let replay_payment =
                        sqlx::query_as::<_, PaymentRecord>("SELECT * FROM payments WHERE id = $1")
                            .bind(existing.payment_id)
                            .fetch_one(&self.pool)
                            .await?;
                    let replay_allocs =
                        fetch_allocations_pool(&self.pool, replay_payment.id).await?;
                    return Ok(AdjustCodShippingFeeOutput {
                        payment: PaymentWithAllocations {
                            payment: replay_payment,
                            allocations: replay_allocs,
                        },
                        old_shipping_fee_vnd: existing.old_shipping_fee_vnd,
                        new_shipping_fee_vnd: existing.new_shipping_fee_vnd,
                        new_order_payable_vnd: existing.new_order_payable_vnd,
                        already_applied: true,
                    });
                }
            }
            return Err(PaymentError::Database(err));
        }

        let allocations = fetch_allocations_tx(&mut tx, updated_payment.id).await?;
        tx.commit().await?;
        self.emit_sse(&updated_payment);

        info!(
            trace_id = %input.trace_id,
            payment_id = %updated_payment.id,
            order_id = %order_id,
            old_shipping_fee_vnd = plan.old_shipping_fee_vnd,
            new_shipping_fee_vnd = plan.new_shipping_fee_vnd,
            new_order_payable_vnd = plan.new_order_payable_vnd,
            new_payment_amount_vnd = updated_payment.amount_vnd,
            "adjusted uncollected COD shipping fee (0 ledger postings)"
        );

        Ok(AdjustCodShippingFeeOutput {
            payment: PaymentWithAllocations {
                payment: updated_payment,
                allocations,
            },
            old_shipping_fee_vnd: plan.old_shipping_fee_vnd,
            new_shipping_fee_vnd: plan.new_shipping_fee_vnd,
            new_order_payable_vnd: plan.new_order_payable_vnd,
            already_applied: false,
        })
    }

    /// Retrieves a payment and its shop allocations by payment_id, order_group_id, or order_id
    /// (including sub-order IDs in `payment_shop_allocations`).
    pub async fn get_payment(
        &self,
        payment_id: Option<Uuid>,
        order_group_id: Option<&str>,
        order_id: Option<&str>,
    ) -> Result<PaymentWithAllocations> {
        let maybe = if let Some(pid) = payment_id {
            sqlx::query_as::<_, PaymentRecord>("SELECT * FROM payments WHERE id = $1")
                .bind(pid)
                .fetch_optional(&self.pool)
                .await?
        } else if let Some(gid) = order_group_id.filter(|s| !s.trim().is_empty()) {
            sqlx::query_as::<_, PaymentRecord>(
                r#"
                SELECT * FROM payments
                WHERE order_group_id = $1 OR order_id = $1
                ORDER BY
                    CASE status
                        WHEN 'succeeded' THEN 1
                        WHEN 'pending' THEN 2
                        ELSE 3
                    END,
                    created_at DESC
                LIMIT 1
                "#,
            )
            .bind(gid)
            .fetch_optional(&self.pool)
            .await?
        } else if let Some(oid) = order_id.filter(|s| !s.trim().is_empty()) {
            sqlx::query_as::<_, PaymentRecord>(
                r#"
                SELECT p.*
                FROM payments p
                LEFT JOIN payment_shop_allocations a ON a.payment_id = p.id
                WHERE p.order_id = $1 OR p.order_group_id = $1 OR a.order_id = $1
                ORDER BY
                    CASE p.status
                        WHEN 'succeeded' THEN 1
                        WHEN 'pending' THEN 2
                        ELSE 3
                    END,
                    p.created_at DESC
                LIMIT 1
                "#,
            )
            .bind(oid)
            .fetch_optional(&self.pool)
            .await?
        } else {
            return Err(PaymentError::Validation(
                "payment_id, order_group_id, or order_id must be provided".to_string(),
            ));
        };

        let payment =
            maybe.ok_or_else(|| PaymentError::NotFound("payment not found".to_string()))?;
        let allocations = fetch_allocations_pool(&self.pool, payment.id).await?;
        Ok(PaymentWithAllocations {
            payment,
            allocations,
        })
    }

    /// Cancels a pending payment intent by `payment_id` or `order_group_id`.
    ///
    /// Per F3 / p4 Item A: when the target payment is a `pending` `ZALOPAY` intent, queries
    /// `/v2/query` outside any DB transaction (short timeout) before cancelling:
    /// - `return_code == 1`: confirms the payment on the standard online path (or queues late
    ///   refund / amount mismatch) and returns `Conflict` as if already `succeeded`, so `order`'s
    ///   expiry watchdog re-reads `GetPayment` and confirms the order instead of cancelling it.
    /// - `2` / `3` / network error / timeout: proceeds to cancel normally and leaves the row
    ///   eligible for the p3 bounded late-payment sweep.
    pub async fn cancel_payment(
        &self,
        payment_id: Option<Uuid>,
        order_group_id: Option<&str>,
        reason: &str,
        trace_id: &str,
    ) -> Result<PaymentWithAllocations> {
        if payment_id.is_none() && order_group_id.is_none_or(|s| s.trim().is_empty()) {
            return Err(PaymentError::Validation(
                "payment_id or order_group_id is required".to_string(),
            ));
        }

        // p4 Item A: Read-only peek outside DB TX so pending ZALOPAY payments check `/v2/query`
        // before cancelling (protects against order expiry watchdog racing a lost callback).
        let zalopay_peek: Option<PaymentRecord> = if let Some(pid) = payment_id {
            sqlx::query_as::<_, PaymentRecord>(
                "SELECT * FROM payments WHERE id = $1 AND method = 'ZALOPAY' AND status = 'pending'",
            )
            .bind(pid)
            .fetch_optional(&self.pool)
            .await?
        } else if let Some(gid) = order_group_id.filter(|s| !s.trim().is_empty()) {
            sqlx::query_as::<_, PaymentRecord>(
                r#"
                SELECT * FROM payments
                WHERE (order_id = $1 OR order_group_id = $1)
                  AND method = 'ZALOPAY'
                  AND status = 'pending'
                ORDER BY created_at DESC
                LIMIT 1
                "#,
            )
            .bind(gid)
            .fetch_optional(&self.pool)
            .await?
        } else {
            None
        };

        let mut cancel_zalopay_last_query: Option<(Option<i32>, Option<bool>)> = None;

        if let Some(zp_cand) = zalopay_peek {
            let query_timeout = self
                .config
                .zalopay
                .http_timeout
                .min(std::time::Duration::from_secs(5));
            match tokio::time::timeout(
                query_timeout,
                self.zalopay.query_order(&zp_cand.provider_order_code),
            )
            .await
            {
                Ok(Ok(resp))
                    if resp.return_code == ZALOPAY_RETURN_SUCCESS
                        && resp.zp_trans_id > 0
                        && resp.amount > 0 =>
                {
                    cancel_zalopay_last_query =
                        Some((Some(resp.return_code), Some(resp.is_processing)));
                    let provider_tx_id = resp.zp_trans_id.to_string();
                    let occurred_at = resp
                        .normalized_server_time()
                        .ok()
                        .flatten()
                        .or_else(|| Some(Utc::now()));
                    let zalopay_app_id = self.zalopay.effective_app_id().ok();

                    let mut tx = self.pool.begin().await?;
                    // Strict global lock ordering: provider_tx advisory lock BEFORE payment group lock.
                    lock_provider_tx_xact(&mut tx, &provider_tx_id).await?;
                    let group =
                        lock_payment_group_tx(&mut tx, &zp_cand.order_id, &zp_cand.order_group_id)
                            .await?;
                    if let Some(locked) = group.iter().find(|p| p.id == zp_cand.id).cloned() {
                        if matches!(locked.status.as_str(), "succeeded" | "refunded") {
                            tx.commit().await?;
                            return Err(PaymentError::Conflict(format!(
                                "cannot cancel payment in status '{}'",
                                locked.status
                            )));
                        }
                        if locked.status == "pending" {
                            let acct =
                                check_provider_tx_accounting_tx(&mut tx, &provider_tx_id).await?;
                            if !acct.is_accounted() {
                                if resp.amount == locked.amount_vnd {
                                    let order_already_succeeded = group.iter().any(|p| {
                                        p.id != locked.id
                                            && matches!(p.status.as_str(), "succeeded" | "refunded")
                                    });
                                    let has_cancelled_alloc =
                                        has_cancelled_allocation_tx(&mut tx, locked.id).await?;
                                    if order_already_succeeded || has_cancelled_alloc {
                                        let next_status = if has_cancelled_alloc {
                                            "cancelled"
                                        } else {
                                            "superseded"
                                        };
                                        record_late_payment_refund_tx(
                                            &mut tx,
                                            &locked,
                                            &provider_tx_id,
                                            resp.amount,
                                            Some(next_status),
                                            "auto_closed:cancel_query_late_refund",
                                            zalopay_app_id,
                                            &self.metrics,
                                        )
                                        .await?;
                                        let updated = sqlx::query_as::<_, PaymentRecord>(
                                            "SELECT * FROM payments WHERE id = $1",
                                        )
                                        .bind(locked.id)
                                        .fetch_one(&mut *tx)
                                        .await?;
                                        let allocations =
                                            fetch_allocations_tx(&mut tx, updated.id).await?;
                                        tx.commit().await?;
                                        return Ok(PaymentWithAllocations {
                                            payment: updated,
                                            allocations,
                                        });
                                    } else {
                                        let confirmed = confirm_online_payment_tx(
                                            &mut tx,
                                            &locked,
                                            &provider_tx_id,
                                            occurred_at,
                                            Some("auto_closed:cancel_query_confirm"),
                                            trace_id,
                                            &self.metrics,
                                        )
                                        .await?;
                                        tx.commit().await?;
                                        self.emit_sse(&confirmed);
                                        info!(
                                            trace_id = %trace_id,
                                            payment_id = %confirmed.id,
                                            provider_tx_id = %provider_tx_id,
                                            "ZaloPay /v2/query returned 1 during cancel_payment; confirmed instead of cancelling"
                                        );
                                        return Err(PaymentError::Conflict(
                                            "cannot cancel payment in status 'succeeded'"
                                                .to_string(),
                                        ));
                                    }
                                } else {
                                    // Mismatched amount: enqueue reconciliation once and mark amount_mismatch
                                    // before cancelling below so the order does not confirm on wrong amount.
                                    enqueue_reconciliation_item_tx(
                                        &mut tx,
                                        None,
                                        &locked.provider,
                                        &provider_tx_id,
                                        Some(locked.id),
                                        Some(&locked.provider_order_code),
                                        Some(locked.amount_vnd),
                                        resp.amount,
                                        "amount_mismatch",
                                        occurred_at,
                                        "reconciliation",
                                        None,
                                    )
                                    .await?;
                                    sqlx::query(
                                        r#"
                                        UPDATE payments
                                        SET provider_final_status = COALESCE(provider_final_status, $1),
                                            provider_next_check_at = NULL,
                                            updated_at = NOW()
                                        WHERE id = $2
                                        "#,
                                    )
                                    .bind(PROVIDER_FINAL_STATUS_AMOUNT_MISMATCH)
                                    .bind(locked.id)
                                    .execute(&mut *tx)
                                    .await?;
                                }
                            }
                        }
                    }
                    tx.commit().await?;
                }
                Ok(Ok(resp)) => {
                    cancel_zalopay_last_query =
                        Some((Some(resp.return_code), Some(resp.is_processing)));
                    info!(
                        trace_id = %trace_id,
                        payment_id = %zp_cand.id,
                        app_trans_id = %zp_cand.provider_order_code,
                        return_code = resp.return_code,
                        is_processing = resp.is_processing,
                        "ZaloPay /v2/query returned non-success during cancel_payment; cancelling and leaving for p3 late sweep"
                    );
                }
                Ok(Err(e)) => {
                    cancel_zalopay_last_query = Some((None, None));
                    warn!(
                        trace_id = %trace_id,
                        payment_id = %zp_cand.id,
                        app_trans_id = %zp_cand.provider_order_code,
                        error = %e,
                        "ZaloPay /v2/query error during cancel_payment; cancelling and leaving for p3 late sweep"
                    );
                }
                Err(_) => {
                    cancel_zalopay_last_query = Some((None, None));
                    warn!(
                        trace_id = %trace_id,
                        payment_id = %zp_cand.id,
                        app_trans_id = %zp_cand.provider_order_code,
                        "ZaloPay /v2/query timed out during cancel_payment; cancelling and leaving for p3 late sweep"
                    );
                }
            }
        }

        let mut tx = self.pool.begin().await?;

        let payment = if let Some(pid) = payment_id {
            // D9-bis point 9: lock the entire checkout payment group in ascending id order before
            // emitting `payment.failed` with `aggregate_id = order_group_id` so concurrent actions
            // on sibling attempts (e.g., retry VietQR P1 vs P2) never invert outbox `seq`.
            let (pay_order_id, pay_group_id): (String, String) =
                sqlx::query_as("SELECT order_id, order_group_id FROM payments WHERE id = $1")
                    .bind(pid)
                    .fetch_optional(&mut *tx)
                    .await?
                    .ok_or_else(|| {
                        PaymentError::NotFound("pending payment not found".to_string())
                    })?;
            let group = lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;
            group
                .into_iter()
                .find(|p| p.id == pid)
                .ok_or_else(|| PaymentError::NotFound("pending payment not found".to_string()))?
        } else if let Some(gid) = order_group_id.filter(|s| !s.trim().is_empty()) {
            // review2-P P1-3: lock group in ascending id order and retry once on an all-superseded
            // Read Committed EPQ snapshot so concurrent `create_payment(supersede_previous=true)`
            // never causes `cancel_payment(order_group_id)` to miss the newly inserted P2.
            let mut group = lock_payment_group_tx(&mut tx, gid, gid).await?;
            if select_payment_for_group_action(&group) == GroupPaymentSelection::AllSupersededRetry
            {
                group = lock_payment_group_tx(&mut tx, gid, gid).await?;
            }
            match select_payment_for_group_action(&group) {
                GroupPaymentSelection::Found(idx) => group.swap_remove(idx),
                GroupPaymentSelection::Empty => {
                    return Err(PaymentError::NotFound(
                        "pending payment not found".to_string(),
                    ));
                }
                GroupPaymentSelection::AllSupersededRetry => {
                    return Err(PaymentError::Conflict(
                        "payment group was concurrently superseded; retry cancellation".to_string(),
                    ));
                }
            }
        } else {
            return Err(PaymentError::Validation(
                "payment_id or order_group_id is required".to_string(),
            ));
        };

        // P1-3: Terminal unpaid states (`cancelled`, `expired`, `failed`, `superseded`) are
        // already cancelled (idempotent success); paid states (`succeeded`, `refunded`) stay Conflict.
        if is_payment_already_cancelled_terminal(&payment.status) {
            let allocations = fetch_allocations_tx(&mut tx, payment.id).await?;
            tx.commit().await?;
            return Ok(PaymentWithAllocations {
                payment,
                allocations,
            });
        }
        if payment.status != "pending" {
            return Err(PaymentError::Conflict(format!(
                "cannot cancel payment in status '{}'",
                payment.status
            )));
        }

        if payment.method == "COD" {
            sqlx::query(
                r#"
                UPDATE payment_shop_allocations
                SET escrow_status = 'cancelled'
                WHERE payment_id = $1 AND escrow_status = 'awaiting_collection'
                "#,
            )
            .bind(payment.id)
            .execute(&mut *tx)
            .await?;

            let updated = sync_cod_payment_status_tx(&mut tx, payment.id, &payment.status)
                .await?
                .unwrap_or(payment);
            let allocations = fetch_allocations_tx(&mut tx, updated.id).await?;
            tx.commit().await?;
            self.emit_sse(&updated);
            return Ok(PaymentWithAllocations {
                payment: updated,
                allocations,
            });
        }

        sqlx::query(
            r#"
            UPDATE payment_shop_allocations
            SET escrow_status = 'cancelled'
            WHERE payment_id = $1 AND escrow_status IN ('awaiting_collection', 'held')
            "#,
        )
        .bind(payment.id)
        .execute(&mut *tx)
        .await?;

        let (has_cancel_zp_query, cancel_zp_rc, cancel_zp_proc) = match cancel_zalopay_last_query {
            Some((rc, is_proc)) if payment.method == "ZALOPAY" => (true, rc, is_proc),
            _ => (false, None, None),
        };
        let updated = sqlx::query_as::<_, PaymentRecord>(
            r#"
            UPDATE payments
            SET status = 'cancelled',
                provider_last_query_return_code = CASE
                    WHEN $2 THEN $3
                    ELSE provider_last_query_return_code
                END,
                provider_last_query_is_processing = CASE
                    WHEN $2 THEN $4
                    ELSE provider_last_query_is_processing
                END,
                updated_at = NOW()
            WHERE id = $1
            RETURNING *
            "#,
        )
        .bind(payment.id)
        .bind(has_cancel_zp_query)
        .bind(cancel_zp_rc)
        .bind(cancel_zp_proc)
        .fetch_one(&mut *tx)
        .await?;

        let effective_reason = if reason.trim().is_empty() {
            "cancelled"
        } else {
            reason.trim()
        };

        insert_outbox_event(
            &mut tx,
            "payment.failed",
            &updated.order_group_id,
            trace_id,
            json!({
                "payment_id": updated.id.to_string(),
                "order_group_id": updated.order_group_id,
                "order_id": updated.order_id,
                "user_id": updated.user_id,
                "reason": effective_reason,
                "sales_channel": updated.sales_channel,
                "storefront_shop_id": updated.storefront_shop_id,
            }),
        )
        .await?;

        let allocations = fetch_allocations_tx(&mut tx, updated.id).await?;
        tx.commit().await?;
        self.emit_sse(&updated);
        Ok(PaymentWithAllocations {
            payment: updated,
            allocations,
        })
    }

    /// Processes a signature-verified webhook payload inside a strict ACID PostgreSQL transaction.
    /// Gate guarantee: sending the same `provider_tx_id` 10 times processes & ledger-posts ONCE,
    /// returning `Ok(WebhookProcessResult { duplicate: true, .. })` on all repeats.
    pub async fn process_verified_webhook(
        &self,
        mut payload: IncomingWebhookPayload,
        raw_json: serde_json::Value,
        default_provider: &str,
        trace_id: &str,
    ) -> Result<WebhookProcessResult> {
        payload.normalize_and_validate()?;

        let webhook_status = payload.classify_status();
        if webhook_status == WebhookEventStatus::NonFinal {
            // P1-1: Non-final statuses (PENDING/PROCESSING) must NOT insert into webhook_events
            // or claim provider_tx_id, so a subsequent SUCCEEDED webhook with the same tx ID
            // processes normally instead of being treated as duplicate.
            self.metrics.record_webhook("ignored");
            info!(
                trace_id = %trace_id,
                provider_tx_id = %payload.provider_tx_id,
                webhook_status = %payload.status,
                "non-final webhook status ignored without recording provider_tx_id"
            );
            let payment_id = payload.parsed_payment_id();
            return Ok(WebhookProcessResult {
                status: "ignored".to_string(),
                provider_tx_id: payload.provider_tx_id,
                payment_id,
                duplicate: false,
            });
        }

        let provider = payload
            .provider
            .clone()
            .filter(|s| !s.trim().is_empty())
            .unwrap_or_else(|| default_provider.to_string());

        let parsed_payment_id = payload.parsed_payment_id();
        let extracted_code = payload.extracted_order_code();
        let order_ref = payload.effective_order_ref().map(ToString::to_string);
        let webhook_occurred_at = payload
            .occurred_at
            .as_deref()
            .and_then(parse_provider_timestamp);

        let mut tx = self.pool.begin().await?;

        // P0-1: Serialize all gateway_clearing accounting paths per provider_tx_id BEFORE acquiring
        // any table/group row locks to guarantee zero AB-BA deadlocks and zero double debits.
        lock_provider_tx_xact(&mut tx, &payload.provider_tx_id).await?;

        // Step 1: Atomic DB-level deduplication by unique `provider_tx_id`
        let inserted_event_id: Option<Uuid> = sqlx::query_scalar(
            r#"
            INSERT INTO webhook_events (
                provider, provider_tx_id, order_ref, amount_vnd, status, raw_payload, signature_verified
            )
            VALUES ($1, $2, $3, $4, 'ignored', $5, TRUE)
            ON CONFLICT (provider_tx_id) DO NOTHING
            RETURNING id
            "#,
        )
        .bind(&provider)
        .bind(&payload.provider_tx_id)
        .bind(order_ref.as_deref().or(extracted_code.as_deref()))
        .bind(payload.amount_vnd)
        .bind(&raw_json)
        .fetch_optional(&mut *tx)
        .await?;

        let Some(webhook_event_id) = inserted_event_id else {
            // p4 Item G: Verify whether the existing `webhook_events.provider_tx_id` belongs to the
            // same provider and payment/order_ref. Cross-provider or cross-payment collisions must
            // NEVER be silently dropped as duplicates — enqueue once for reconciliation + log ERROR.
            let existing_row: Option<(Uuid, String, Option<Uuid>, Option<String>)> =
                sqlx::query_as(
                    "SELECT id, provider, payment_id, order_ref FROM webhook_events WHERE provider_tx_id = $1",
                )
                .bind(&payload.provider_tx_id)
                .fetch_optional(&mut *tx)
                .await?;

            let incoming_candidate = find_payment_for_webhook_tx(
                &mut tx,
                parsed_payment_id,
                extracted_code.as_deref(),
                payload.order_group_id.as_deref(),
                payload.order_id.as_deref(),
            )
            .await?;
            let incoming_payment_id = incoming_candidate
                .as_ref()
                .map(|p| p.id)
                .or(parsed_payment_id);
            let incoming_order_ref = order_ref.as_deref().or(extracted_code.as_deref());

            if let Some((existing_id, existing_provider, existing_payment_id, existing_order_ref)) =
                existing_row
            {
                if !is_same_webhook_target(
                    &existing_provider,
                    existing_payment_id,
                    existing_order_ref.as_deref(),
                    &provider,
                    incoming_payment_id,
                    incoming_order_ref,
                ) {
                    let target_key = incoming_payment_id
                        .map(|id| id.to_string())
                        .or_else(|| incoming_order_ref.map(ToString::to_string))
                        .unwrap_or_else(|| "unknown".to_string());
                    let collision_tx_id = format!(
                        "tx-collision:{}:{}:{}",
                        provider, payload.provider_tx_id, target_key
                    );
                    enqueue_reconciliation_item_tx(
                        &mut tx,
                        Some(existing_id),
                        &provider,
                        &collision_tx_id,
                        incoming_payment_id,
                        incoming_order_ref,
                        incoming_candidate.as_ref().map(|p| p.amount_vnd),
                        payload.amount_vnd,
                        "provider_tx_id_collision",
                        webhook_occurred_at,
                        "webhook",
                        None,
                    )
                    .await?;
                    tx.commit().await?;
                    self.metrics.record_webhook("provider_tx_id_collision");
                    error!(
                        trace_id = %trace_id,
                        provider_tx_id = %payload.provider_tx_id,
                        existing_provider = %existing_provider,
                        incoming_provider = %provider,
                        existing_payment_id = ?existing_payment_id,
                        incoming_payment_id = ?incoming_payment_id,
                        existing_order_ref = ?existing_order_ref,
                        incoming_order_ref = ?incoming_order_ref,
                        "webhook provider_tx_id collision across providers or payments; queued for manual reconciliation"
                    );
                    return Ok(WebhookProcessResult {
                        status: "provider_tx_id_collision".to_string(),
                        provider_tx_id: payload.provider_tx_id,
                        payment_id: incoming_payment_id,
                        duplicate: false,
                    });
                }
            }

            tx.commit().await?;
            self.metrics.record_webhook("duplicate_ignored");
            info!(
                trace_id = %trace_id,
                provider_tx_id = %payload.provider_tx_id,
                "duplicate webhook ignored idempotently"
            );
            return Ok(WebhookProcessResult {
                status: "duplicate_ignored".to_string(),
                provider_tx_id: payload.provider_tx_id,
                payment_id: incoming_payment_id,
                duplicate: true,
            });
        };

        // Step 2: Locate matching payment read-only first, then lock the entire checkout group
        // in strictly ascending `id` order (`ORDER BY id ASC FOR UPDATE`) to eliminate AB-BA
        // deadlocks with `create_payment` or sibling webhooks (P1-5).
        let candidate = find_payment_for_webhook_tx(
            &mut tx,
            parsed_payment_id,
            extracted_code.as_deref(),
            payload.order_group_id.as_deref(),
            payload.order_id.as_deref(),
        )
        .await?;

        let (payment, order_already_succeeded, has_cancelled_alloc) = if let Some(found) = candidate
        {
            let group =
                lock_payment_group_tx(&mut tx, &found.order_id, &found.order_group_id).await?;
            let locked = group
                .iter()
                .find(|p| p.id == found.id)
                .cloned()
                .unwrap_or(found);
            let already_succeeded = group.iter().any(|p| {
                p.id != locked.id && matches!(p.status.as_str(), "succeeded" | "refunded")
            });
            let cancelled_alloc = has_cancelled_allocation_tx(&mut tx, locked.id).await?;
            (Some(locked), already_succeeded, cancelled_alloc)
        } else {
            (None, false, false)
        };

        // P0-1: If this provider_tx_id was already accounted for by reconciliation/expiry/queue
        // (on payments, finalized reconciliation_queue, or tx-scoped refunds), skip double debit.
        if webhook_status == WebhookEventStatus::Succeeded {
            let acct = check_provider_tx_accounting_tx(&mut tx, &payload.provider_tx_id).await?;
            if acct.is_accounted() {
                sqlx::query(
                    r#"
                    UPDATE webhook_events
                    SET payment_id = $1, status = 'processed', processed_at = NOW()
                    WHERE id = $2
                    "#,
                )
                .bind(payment.as_ref().map(|p| p.id))
                .bind(webhook_event_id)
                .execute(&mut *tx)
                .await?;

                tx.commit().await?;
                self.metrics.record_webhook("already_accounted");
                info!(
                    trace_id = %trace_id,
                    provider_tx_id = %payload.provider_tx_id,
                    "webhook skipped idempotently because provider_tx_id is already accounted for"
                );
                return Ok(WebhookProcessResult {
                    status: "already_accounted".to_string(),
                    provider_tx_id: payload.provider_tx_id,
                    payment_id: payment.map(|p| p.id),
                    duplicate: true,
                });
            }
        }

        let decision = if webhook_status == WebhookEventStatus::Unexpected {
            WebhookDecision::QueueReconciliation {
                reason: "unexpected_webhook_status",
            }
        } else if let Some(p) = payment.as_ref().filter(|p| p.method != "VIETQR") {
            WebhookDecision::QueueReconciliation {
                reason: if p.method == "COD" {
                    "unexpected_cod_webhook"
                } else {
                    "webhook_method_mismatch"
                },
            }
        } else {
            decide_webhook_action(
                payment.as_ref().map(|p| p.status.as_str()),
                payment.as_ref().map(|p| p.amount_vnd),
                payload.amount_vnd,
                webhook_status,
                order_already_succeeded,
                has_cancelled_alloc,
            )
        };

        match decision {
            WebhookDecision::IgnoreNonFinal => {
                unreachable!("non-final webhook returns before DB transaction")
            }
            WebhookDecision::QueueReconciliation { reason } => {
                // P1-1: Deduplicate via advisory lock + WHERE NOT EXISTS + ON CONFLICT DO NOTHING
                // (never aborts tx with 25P02, works with or without unique index on provider_tx_id).
                enqueue_reconciliation_item_tx(
                    &mut tx,
                    Some(webhook_event_id),
                    &provider,
                    &payload.provider_tx_id,
                    payment.as_ref().map(|p| p.id),
                    order_ref.as_deref().or(extracted_code.as_deref()),
                    payment.as_ref().map(|p| p.amount_vnd),
                    payload.amount_vnd,
                    reason,
                    webhook_occurred_at,
                    "webhook",
                    None,
                )
                .await?;

                sqlx::query(
                    r#"
                    UPDATE webhook_events
                    SET payment_id = $1, status = 'mismatched', processed_at = NOW()
                    WHERE id = $2
                    "#,
                )
                .bind(payment.as_ref().map(|p| p.id))
                .bind(webhook_event_id)
                .execute(&mut *tx)
                .await?;

                tx.commit().await?;
                self.metrics.record_webhook(reason);
                warn!(
                    trace_id = %trace_id,
                    provider_tx_id = %payload.provider_tx_id,
                    reason = reason,
                    actual_amount_vnd = payload.amount_vnd,
                    expected_amount_vnd = ?payment.as_ref().map(|p| p.amount_vnd),
                    "webhook queued for manual reconciliation"
                );

                Ok(WebhookProcessResult {
                    status: format!("queued_for_reconciliation:{reason}"),
                    provider_tx_id: payload.provider_tx_id,
                    payment_id: payment.map(|p| p.id),
                    duplicate: false,
                })
            }

            WebhookDecision::MarkFailed => {
                let p = payment.expect("payment present for MarkFailed");
                let updated = if p.status == "pending" {
                    sqlx::query(
                        r#"
                        UPDATE payment_shop_allocations
                        SET escrow_status = 'cancelled'
                        WHERE payment_id = $1 AND escrow_status IN ('awaiting_collection', 'held')
                        "#,
                    )
                    .bind(p.id)
                    .execute(&mut *tx)
                    .await?;

                    // P1-1: Do NOT store provider_tx_id on failed payments so failed webhooks
                    // never occupy payments.provider_tx_id UNIQUE or mask a subsequent real transfer.
                    let row = sqlx::query_as::<_, PaymentRecord>(
                        r#"
                        UPDATE payments
                        SET status = 'failed', updated_at = NOW()
                        WHERE id = $1
                        RETURNING *
                        "#,
                    )
                    .bind(p.id)
                    .fetch_one(&mut *tx)
                    .await?;

                    insert_outbox_event(
                        &mut tx,
                        "payment.failed",
                        &row.order_group_id,
                        trace_id,
                        json!({
                            "payment_id": row.id.to_string(),
                            "order_group_id": row.order_group_id,
                            "order_id": row.order_id,
                            "user_id": row.user_id,
                            "reason": payload.status,
                            "sales_channel": row.sales_channel,
                            "storefront_shop_id": row.storefront_shop_id,
                        }),
                    )
                    .await?;
                    Some(row)
                } else {
                    None
                };

                sqlx::query(
                    r#"
                    UPDATE webhook_events
                    SET payment_id = $1, status = 'processed', processed_at = NOW()
                    WHERE id = $2
                    "#,
                )
                .bind(p.id)
                .bind(webhook_event_id)
                .execute(&mut *tx)
                .await?;

                tx.commit().await?;
                self.metrics.record_webhook("failed");
                if let Some(ref u) = updated {
                    self.emit_sse(u);
                }

                Ok(WebhookProcessResult {
                    status: "payment_marked_failed".to_string(),
                    provider_tx_id: payload.provider_tx_id,
                    payment_id: Some(p.id),
                    duplicate: false,
                })
            }

            WebhookDecision::LatePaymentRefund { reason } => {
                let p = payment.expect("payment present for LatePaymentRefund");
                let pending_next_status = if reason == "payment_has_cancelled_allocation" {
                    Some("cancelled")
                } else if reason == "order_already_has_succeeded_payment" {
                    Some("superseded")
                } else {
                    None
                };
                record_late_payment_refund_tx(
                    &mut tx,
                    &p,
                    &payload.provider_tx_id,
                    payload.amount_vnd,
                    pending_next_status,
                    "auto_closed:webhook_late_refund",
                    None,
                    &self.metrics,
                )
                .await?;

                sqlx::query(
                    r#"
                    UPDATE webhook_events
                    SET payment_id = $1, status = 'late_refund_queued', processed_at = NOW()
                    WHERE id = $2
                    "#,
                )
                .bind(p.id)
                .bind(webhook_event_id)
                .execute(&mut *tx)
                .await?;

                tx.commit().await?;
                self.metrics.record_webhook("late_refund_queued");
                warn!(
                    trace_id = %trace_id,
                    payment_id = %p.id,
                    order_id = %p.order_id,
                    provider_tx_id = %payload.provider_tx_id,
                    reason = reason,
                    "late/superseded webhook triggered automatic pending late_payment refund"
                );

                Ok(WebhookProcessResult {
                    status: "late_payment_refund_queued".to_string(),
                    provider_tx_id: payload.provider_tx_id,
                    payment_id: Some(p.id),
                    duplicate: false,
                })
            }

            WebhookDecision::ConfirmPayment => {
                let p = payment.expect("payment present for ConfirmPayment");
                let updated = confirm_online_payment_tx(
                    &mut tx,
                    &p,
                    &payload.provider_tx_id,
                    webhook_occurred_at,
                    Some("auto_closed:webhook_confirm"),
                    trace_id,
                    &self.metrics,
                )
                .await?;

                sqlx::query(
                    r#"
                    UPDATE webhook_events
                    SET payment_id = $1, status = 'processed', processed_at = NOW()
                    WHERE id = $2
                    "#,
                )
                .bind(p.id)
                .bind(webhook_event_id)
                .execute(&mut *tx)
                .await?;

                tx.commit().await?;

                self.metrics.record_webhook("confirmed");
                self.emit_sse(&updated);
                info!(
                    trace_id = %trace_id,
                    payment_id = %updated.id,
                    order_group_id = %updated.order_group_id,
                    order_id = %updated.order_id,
                    provider_tx_id = %payload.provider_tx_id,
                    amount_vnd = updated.amount_vnd,
                    "VietQR payment confirmed via webhook"
                );

                Ok(WebhookProcessResult {
                    status: "processed".to_string(),
                    provider_tx_id: payload.provider_tx_id,
                    payment_id: Some(updated.id),
                    duplicate: false,
                })
            }
        }
    }

    /// Processes an incoming ZaloPay v2 callback (`POST /webhooks/zalopay`) per F2, F2-bis & F5.
    ///
    /// Strict verification order (F2 / F2-bis):
    /// 1. Constant-time MAC verification `HMAC-SHA256(key2, raw_data)` — fail returns `InvalidMac`
    ///    (`return_code = -1`) with **zero DB writes**; never logs keys, full MAC, or raw body.
    /// 2. Parse `data` & verify `app_id` and `app_trans_id` match one of our `ZALOPAY` payments.
    /// 3. Normalize `server_time` (seconds vs milliseconds, threshold `1e11`); if `|now - server_time| > 300s`,
    ///    NEVER confirm from callback body — query `/v2/query` and process strictly from the
    ///    authoritative query response.
    /// 4. Check `amount` (mismatch -> `reconciliation_queue`, 0 confirm).
    /// 5. Idempotent deduplication by `zp_trans_id` (`webhook_events.provider_tx_id UNIQUE`);
    ///    late/superseded payments create a `late_payment` refund without confirming the order (F5).
    pub async fn process_zalopay_callback(
        &self,
        req: ZaloPayCallbackRequest,
        trace_id: &str,
    ) -> Result<ZaloPayCallbackOutcome> {
        // Production safety guard: refuse mock mode or placeholder key2 in production.
        self.zalopay.ensure_callback_usable()?;

        // Step 1: Constant-time MAC verification FIRST (0 DB reads/writes on failure).
        if verify_callback(&req.data, &req.mac, self.zalopay.effective_key2()).is_err() {
            self.metrics.record_webhook("zalopay_rejected_signature");
            warn!(
                trace_id = %trace_id,
                callback_type = req.callback_type,
                "rejected ZaloPay callback due to invalid MAC"
            );
            return Ok(ZaloPayCallbackOutcome::InvalidMac);
        }

        if req.callback_type != 1 {
            self.metrics.record_webhook("zalopay_unsupported_type");
            warn!(
                trace_id = %trace_id,
                callback_type = req.callback_type,
                "rejected unsupported ZaloPay callback type"
            );
            return Ok(ZaloPayCallbackOutcome::Rejected {
                reason: "unsupported callback type".to_string(),
            });
        }

        // Step 2: Parse callback payload and verify app_id + app_trans_id match our ZALOPAY payment.
        let data: ZaloPayCallbackData = match serde_json::from_str(&req.data) {
            Ok(d) => d,
            Err(_) => {
                self.metrics.record_webhook("zalopay_invalid_data");
                warn!(
                    trace_id = %trace_id,
                    "rejected ZaloPay callback with malformed data JSON"
                );
                return Ok(ZaloPayCallbackOutcome::Rejected {
                    reason: "invalid callback data".to_string(),
                });
            }
        };

        let expected_app_id = self.zalopay.effective_app_id()?;
        if data.app_id != expected_app_id {
            self.metrics.record_webhook("zalopay_app_id_mismatch");
            warn!(
                trace_id = %trace_id,
                received_app_id = data.app_id,
                expected_app_id = expected_app_id,
                "rejected ZaloPay callback due to app_id mismatch"
            );
            return Ok(ZaloPayCallbackOutcome::Rejected {
                reason: "app_id mismatch".to_string(),
            });
        }

        let app_trans_id = data.app_trans_id.trim();
        if app_trans_id.is_empty() || data.zp_trans_id <= 0 || data.amount <= 0 {
            self.metrics.record_webhook("zalopay_invalid_fields");
            return Ok(ZaloPayCallbackOutcome::Rejected {
                reason: "missing or invalid app_trans_id, zp_trans_id, or amount".to_string(),
            });
        }

        let Some(found_payment) = sqlx::query_as::<_, PaymentRecord>(
            "SELECT * FROM payments WHERE provider_order_code = $1 AND method = 'ZALOPAY'",
        )
        .bind(app_trans_id)
        .fetch_optional(&self.pool)
        .await?
        else {
            self.metrics.record_webhook("zalopay_unknown_app_trans_id");
            warn!(
                trace_id = %trace_id,
                app_trans_id = app_trans_id,
                "rejected ZaloPay callback for unknown app_trans_id"
            );
            return Ok(ZaloPayCallbackOutcome::Rejected {
                reason: "app_trans_id not found".to_string(),
            });
        };

        // Step 3: Normalize server_time (s vs ms per F2-bis) and enforce 300s replay window.
        let time_check = match check_server_time_window(
            data.server_time,
            Utc::now(),
            self.config.webhook_replay_window_secs,
        ) {
            Ok(c) => c,
            Err(_) => {
                self.metrics.record_webhook("zalopay_invalid_server_time");
                return Ok(ZaloPayCallbackOutcome::Rejected {
                    reason: "invalid server_time".to_string(),
                });
            }
        };

        let (effective_amount_vnd, effective_zp_trans_id, effective_paid_at, verified_via_query) =
            if time_check.within_window {
                (
                    data.amount,
                    data.zp_trans_id,
                    Some(time_check.normalized_at),
                    false,
                )
            } else {
                warn!(
                    trace_id = %trace_id,
                    payment_id = %found_payment.id,
                    app_trans_id = app_trans_id,
                    drift_secs = time_check.drift_secs,
                    "ZaloPay callback server_time outside replay window; querying /v2/query authoritatively"
                );
                let query_resp = match self.zalopay.query_order(app_trans_id).await {
                    Ok(q) => q,
                    Err(e) => {
                        self.metrics.record_webhook("zalopay_query_retry");
                        warn!(
                            trace_id = %trace_id,
                            payment_id = %found_payment.id,
                            app_trans_id = app_trans_id,
                            error = %e,
                            "ZaloPay /v2/query failed during stale callback verification; requesting retry"
                        );
                        return Ok(ZaloPayCallbackOutcome::RetryableError {
                            reason: "query_order temporarily unavailable".to_string(),
                        });
                    }
                };

                match query_resp.return_code {
                    ZALOPAY_RETURN_SUCCESS => {
                        if query_resp.zp_trans_id <= 0 || query_resp.amount <= 0 {
                            self.metrics.record_webhook("zalopay_query_incomplete");
                            return Ok(ZaloPayCallbackOutcome::RetryableError {
                                reason: "incomplete query_order result".to_string(),
                            });
                        }
                        let q_paid_at = query_resp
                            .normalized_server_time()
                            .ok()
                            .flatten()
                            .or(Some(time_check.normalized_at));
                        (query_resp.amount, query_resp.zp_trans_id, q_paid_at, true)
                    }
                    ZALOPAY_RETURN_PROCESSING => {
                        self.metrics.record_webhook("zalopay_query_processing");
                        return Ok(ZaloPayCallbackOutcome::RetryableError {
                            reason: "order still processing".to_string(),
                        });
                    }
                    _ => {
                        self.metrics.record_webhook("zalopay_query_not_paid");
                        info!(
                            trace_id = %trace_id,
                            payment_id = %found_payment.id,
                            app_trans_id = app_trans_id,
                            query_return_code = query_resp.return_code,
                            "stale ZaloPay callback ignored because /v2/query reported non-success"
                        );
                        return Ok(ZaloPayCallbackOutcome::Ignored {
                            reason: "query_order reported payment not succeeded".to_string(),
                        });
                    }
                }
            };

        let provider_tx_id = effective_zp_trans_id.to_string();
        let provider_name = self.zalopay.mode().provider_name().to_string();
        // Safe structured metadata only — NEVER persist raw MAC or signing keys.
        let safe_raw_payload = json!({
            "app_id": data.app_id,
            "app_trans_id": app_trans_id,
            "zp_trans_id": effective_zp_trans_id,
            "amount": effective_amount_vnd,
            "server_time": data.server_time,
            "verified_via_query": verified_via_query,
        });

        let mut tx = self.pool.begin().await?;

        // Step 4 & 5: Advisory lock -> idempotent webhook_events insert (UNIQUE provider_tx_id) ->
        // lock payment group -> amount check & confirmation / mismatch queue / late refund.
        lock_provider_tx_xact(&mut tx, &provider_tx_id).await?;

        let inserted_event_id: Option<Uuid> = sqlx::query_scalar(
            r#"
            INSERT INTO webhook_events (
                provider, provider_tx_id, order_ref, amount_vnd, status, raw_payload, signature_verified
            )
            VALUES ($1, $2, $3, $4, 'ignored', $5, TRUE)
            ON CONFLICT (provider_tx_id) DO NOTHING
            RETURNING id
            "#,
        )
        .bind(&provider_name)
        .bind(&provider_tx_id)
        .bind(app_trans_id)
        .bind(effective_amount_vnd)
        .bind(&safe_raw_payload)
        .fetch_optional(&mut *tx)
        .await?;

        let Some(webhook_event_id) = inserted_event_id else {
            // p4 Item G: Distinguish true duplicate (same provider + same payment/order_ref) from
            // cross-provider (VietQR <-> ZaloPay) or cross-payment `provider_tx_id` collision.
            let existing_row: Option<(Uuid, String, Option<Uuid>, Option<String>)> =
                sqlx::query_as(
                    "SELECT id, provider, payment_id, order_ref FROM webhook_events WHERE provider_tx_id = $1",
                )
                .bind(&provider_tx_id)
                .fetch_optional(&mut *tx)
                .await?;

            if let Some((existing_id, existing_provider, existing_payment_id, existing_order_ref)) =
                existing_row
            {
                if !is_same_webhook_target(
                    &existing_provider,
                    existing_payment_id,
                    existing_order_ref.as_deref(),
                    &provider_name,
                    Some(found_payment.id),
                    Some(app_trans_id),
                ) {
                    let collision_tx_id = format!(
                        "tx-collision:{}:{}:{}",
                        provider_name, provider_tx_id, found_payment.id
                    );
                    enqueue_reconciliation_item_tx(
                        &mut tx,
                        Some(existing_id),
                        &provider_name,
                        &collision_tx_id,
                        Some(found_payment.id),
                        Some(app_trans_id),
                        Some(found_payment.amount_vnd),
                        effective_amount_vnd,
                        "provider_tx_id_collision",
                        effective_paid_at,
                        "webhook",
                        None,
                    )
                    .await?;
                    tx.commit().await?;
                    self.metrics.record_webhook("zalopay_tx_collision");
                    error!(
                        trace_id = %trace_id,
                        provider_tx_id = %provider_tx_id,
                        existing_provider = %existing_provider,
                        incoming_provider = %provider_name,
                        existing_payment_id = ?existing_payment_id,
                        incoming_payment_id = %found_payment.id,
                        existing_order_ref = ?existing_order_ref,
                        incoming_order_ref = %app_trans_id,
                        "ZaloPay callback provider_tx_id collision across providers or payments; queued for manual reconciliation"
                    );
                    return Ok(ZaloPayCallbackOutcome::QueuedReconciliation {
                        payment_id: Some(found_payment.id),
                        provider_tx_id: collision_tx_id,
                        reason: "provider_tx_id_collision".to_string(),
                    });
                }
            }

            tx.commit().await?;
            self.metrics.record_webhook("zalopay_duplicate_ignored");
            info!(
                trace_id = %trace_id,
                provider_tx_id = %provider_tx_id,
                app_trans_id = app_trans_id,
                "duplicate ZaloPay callback ignored idempotently"
            );
            return Ok(ZaloPayCallbackOutcome::Duplicate {
                payment_id: Some(found_payment.id),
                provider_tx_id,
            });
        };

        let group = lock_payment_group_tx(
            &mut tx,
            &found_payment.order_id,
            &found_payment.order_group_id,
        )
        .await?;
        let locked = group
            .iter()
            .find(|p| p.id == found_payment.id)
            .cloned()
            .unwrap_or(found_payment);
        let order_already_succeeded = group
            .iter()
            .any(|p| p.id != locked.id && matches!(p.status.as_str(), "succeeded" | "refunded"));
        let has_cancelled_alloc = has_cancelled_allocation_tx(&mut tx, locked.id).await?;

        let acct = check_provider_tx_accounting_tx(&mut tx, &provider_tx_id).await?;
        if acct.is_accounted() {
            sqlx::query(
                r#"
                UPDATE webhook_events
                SET payment_id = $1, status = 'processed', processed_at = NOW()
                WHERE id = $2
                "#,
            )
            .bind(locked.id)
            .bind(webhook_event_id)
            .execute(&mut *tx)
            .await?;

            tx.commit().await?;
            self.metrics.record_webhook("zalopay_already_accounted");
            return Ok(ZaloPayCallbackOutcome::Duplicate {
                payment_id: Some(locked.id),
                provider_tx_id,
            });
        }

        let decision = decide_webhook_action(
            Some(locked.status.as_str()),
            Some(locked.amount_vnd),
            effective_amount_vnd,
            WebhookEventStatus::Succeeded,
            order_already_succeeded,
            has_cancelled_alloc,
        );

        match decision {
            WebhookDecision::QueueReconciliation { reason } => {
                enqueue_reconciliation_item_tx(
                    &mut tx,
                    Some(webhook_event_id),
                    &provider_name,
                    &provider_tx_id,
                    Some(locked.id),
                    Some(app_trans_id),
                    Some(locked.amount_vnd),
                    effective_amount_vnd,
                    reason,
                    effective_paid_at,
                    "webhook",
                    None,
                )
                .await?;

                sqlx::query(
                    r#"
                    UPDATE payments
                    SET provider_final_status = COALESCE(provider_final_status, $1),
                        provider_next_check_at = NULL,
                        updated_at = NOW()
                    WHERE id = $2
                    "#,
                )
                .bind(PROVIDER_FINAL_STATUS_AMOUNT_MISMATCH)
                .bind(locked.id)
                .execute(&mut *tx)
                .await?;

                sqlx::query(
                    r#"
                    UPDATE webhook_events
                    SET payment_id = $1, status = 'mismatched', processed_at = NOW()
                    WHERE id = $2
                    "#,
                )
                .bind(locked.id)
                .bind(webhook_event_id)
                .execute(&mut *tx)
                .await?;

                tx.commit().await?;
                self.metrics.record_webhook(reason);
                warn!(
                    trace_id = %trace_id,
                    payment_id = %locked.id,
                    provider_tx_id = %provider_tx_id,
                    reason = reason,
                    expected_amount_vnd = locked.amount_vnd,
                    actual_amount_vnd = effective_amount_vnd,
                    "ZaloPay callback queued for manual reconciliation"
                );
                Ok(ZaloPayCallbackOutcome::QueuedReconciliation {
                    payment_id: Some(locked.id),
                    provider_tx_id,
                    reason: reason.to_string(),
                })
            }
            WebhookDecision::LatePaymentRefund { reason } => {
                let pending_next_status = if reason == "payment_has_cancelled_allocation" {
                    Some("cancelled")
                } else if reason == "order_already_has_succeeded_payment" {
                    Some("superseded")
                } else {
                    None
                };
                record_late_payment_refund_tx(
                    &mut tx,
                    &locked,
                    &provider_tx_id,
                    effective_amount_vnd,
                    pending_next_status,
                    "auto_closed:zalopay_late_refund",
                    Some(expected_app_id),
                    &self.metrics,
                )
                .await?;

                sqlx::query(
                    r#"
                    UPDATE webhook_events
                    SET payment_id = $1, status = 'late_refund_queued', processed_at = NOW()
                    WHERE id = $2
                    "#,
                )
                .bind(locked.id)
                .bind(webhook_event_id)
                .execute(&mut *tx)
                .await?;

                tx.commit().await?;
                self.metrics.record_webhook("zalopay_late_refund_queued");
                warn!(
                    trace_id = %trace_id,
                    payment_id = %locked.id,
                    order_id = %locked.order_id,
                    provider_tx_id = %provider_tx_id,
                    reason = reason,
                    "late/superseded ZaloPay callback queued automatic late_payment refund"
                );
                Ok(ZaloPayCallbackOutcome::LateRefundQueued {
                    payment_id: locked.id,
                    provider_tx_id,
                    reason: reason.to_string(),
                })
            }
            WebhookDecision::ConfirmPayment => {
                let updated = confirm_online_payment_tx(
                    &mut tx,
                    &locked,
                    &provider_tx_id,
                    effective_paid_at,
                    Some("auto_closed:zalopay_callback_confirm"),
                    trace_id,
                    &self.metrics,
                )
                .await?;

                sqlx::query(
                    r#"
                    UPDATE webhook_events
                    SET payment_id = $1, status = 'processed', processed_at = NOW()
                    WHERE id = $2
                    "#,
                )
                .bind(locked.id)
                .bind(webhook_event_id)
                .execute(&mut *tx)
                .await?;

                tx.commit().await?;
                self.metrics.record_webhook("zalopay_confirmed");
                self.emit_sse(&updated);
                info!(
                    trace_id = %trace_id,
                    payment_id = %updated.id,
                    order_group_id = %updated.order_group_id,
                    order_id = %updated.order_id,
                    provider_tx_id = %provider_tx_id,
                    amount_vnd = updated.amount_vnd,
                    verified_via_query = verified_via_query,
                    "ZaloPay payment confirmed via callback"
                );
                Ok(ZaloPayCallbackOutcome::Confirmed {
                    payment_id: updated.id,
                    provider_tx_id,
                })
            }
            WebhookDecision::IgnoreNonFinal | WebhookDecision::MarkFailed => {
                tx.commit().await?;
                Ok(ZaloPayCallbackOutcome::Ignored {
                    reason: "non-confirmable callback decision".to_string(),
                })
            }
        }
    }

    /// Creates an idempotent refund capped at `paid_amount - already_refunded` (UC-PAY04 / F4 / F11).
    /// - When `require_approval_if_large == true` (manual admin refunds and `AdminResolveReturn`):
    ///   requires non-empty `requested_by`; if `amount_vnd >= PAYMENT_LARGE_REFUND_APPROVAL_VND` OR
    ///   the 24-hour rolling sum of manual refunds (`pending_approval`, `pending`, `completed`,
    ///   `manual_review`; excluding `rejected`) on the same `payment_id` plus `amount_vnd` reaches
    ///   the threshold, saves `status = 'pending_approval'` with **0 ledger postings and 0 outbox
    ///   events** until a different checker approves via [`Self::decide_refund_approval`].
    /// - When `require_approval_if_large == false` (automatic saga cancel / late payment):
    ///   ignores `requested_by` and bypasses two-person approval.
    /// - For `ZALOPAY` payments entering `pending`: persists `provider_m_refund_id` and
    ///   `provider_status = 'pending'` in the DB transaction BEFORE dispatching `/v2/refund` (F4).
    pub async fn create_refund(&self, input: CreateRefundInput) -> Result<CreateRefundOutput> {
        if input.idempotency_key.trim().is_empty() {
            return Err(PaymentError::Validation(
                "idempotency_key is required for refunds".to_string(),
            ));
        }
        if input.amount_vnd <= 0 {
            return Err(PaymentError::Validation(
                "refund amount_vnd must be positive".to_string(),
            ));
        }

        // F11: `requested_by` is ONLY accepted when `require_approval_if_large == true`;
        // automatic saga calls ignore any caller-supplied `requested_by`.
        let effective_requested_by: Option<String> = if input.require_approval_if_large {
            let trimmed = input
                .requested_by
                .as_deref()
                .map(str::trim)
                .filter(|s| !s.is_empty())
                .ok_or_else(|| {
                    PaymentError::Validation(
                        "requested_by is required when require_approval_if_large is true"
                            .to_string(),
                    )
                })?;
            Some(trimmed.to_string())
        } else {
            None
        };

        let mut tx = self.pool.begin().await?;

        if let Some(existing) =
            sqlx::query_as::<_, RefundRecord>("SELECT * FROM refunds WHERE idempotency_key = $1")
                .bind(&input.idempotency_key)
                .fetch_optional(&mut *tx)
                .await?
        {
            validate_existing_refund_replay(&mut *tx, &existing, &input).await?;
            tx.commit().await?;
            return Ok(CreateRefundOutput {
                refund: existing,
                already_existed: true,
            });
        }

        // D9-bis point 9: locate target payment read-only first, then lock the entire checkout
        // payment group in ascending id order (`lock_payment_group_tx`) BEFORE locking allocations
        // and inserting `payment.refunded` with `aggregate_id = payment.order_group_id`.
        let target_meta: Option<(Uuid, String, String)> = if let Some(pid) = input.payment_id {
            sqlx::query_as("SELECT id, order_id, order_group_id FROM payments WHERE id = $1")
                .bind(pid)
                .fetch_optional(&mut *tx)
                .await?
        } else if !input.order_id.trim().is_empty() {
            sqlx::query_as(
                r#"
                SELECT p.id, p.order_id, p.order_group_id
                FROM payments p
                LEFT JOIN payment_shop_allocations a ON a.payment_id = p.id
                WHERE (p.order_id = $1 OR p.order_group_id = $1 OR a.order_id = $1)
                ORDER BY
                    CASE p.status
                        WHEN 'succeeded' THEN 1
                        WHEN 'refunded' THEN 2
                        WHEN 'pending' THEN 3
                        ELSE 4
                    END,
                    p.created_at DESC
                LIMIT 1
                "#,
            )
            .bind(input.order_id.trim())
            .fetch_optional(&mut *tx)
            .await?
        } else {
            return Err(PaymentError::Validation(
                "payment_id or order_id is required for refund".to_string(),
            ));
        };

        let Some((target_payment_id, pay_order_id, pay_group_id)) = target_meta else {
            return Err(PaymentError::NotFound(
                "payment not found for refund".to_string(),
            ));
        };

        let group = lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;
        let payment = group
            .into_iter()
            .find(|p| p.id == target_payment_id)
            .ok_or_else(|| PaymentError::NotFound("payment not found for refund".to_string()))?;

        let allocations = sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT * FROM payment_shop_allocations
            WHERE payment_id = $1
            ORDER BY id ASC
            FOR UPDATE
            "#,
        )
        .bind(payment.id)
        .fetch_all(&mut *tx)
        .await?;

        let requested_order_id = input.order_id.trim();
        let target_alloc: Option<&ShopAllocationRecord> = if allocations.is_empty() {
            None
        } else if let Some(a) = allocations
            .iter()
            .find(|a| a.order_id == requested_order_id)
        {
            Some(a)
        } else if allocations.len() == 1
            && (requested_order_id.is_empty()
                || requested_order_id == payment.order_id
                || requested_order_id == payment.order_group_id)
        {
            Some(&allocations[0])
        } else {
            return Err(PaymentError::Validation(format!(
                "order_id '{requested_order_id}' does not match any sub-order allocation on payment {}",
                payment.id
            )));
        };

        validate_refund_eligibility(
            &payment.method,
            &payment.status,
            payment.amount_vnd,
            target_alloc.map(|a| a.escrow_status.as_str()),
        )?;

        let order_id = if let Some(alloc) = target_alloc {
            alloc.order_id.clone()
        } else if requested_order_id.is_empty() {
            payment.order_id.clone()
        } else {
            requested_order_id.to_string()
        };

        // Total funds actually collected into escrow for this payment.
        // For multi-shop COD, only delivered/collected allocations have been booked to escrow.
        let payment_collected_vnd = if payment.method == "COD" && !allocations.is_empty() {
            let mut sum = 0_i64;
            for a in &allocations {
                if matches!(
                    a.escrow_status.as_str(),
                    "held" | "pending_release" | "released" | "refunded"
                ) {
                    sum = sum
                        .checked_add(allocation_buyer_payable_vnd(a)?)
                        .ok_or_else(|| {
                            PaymentError::LedgerInvariant("collected COD sum overflow".to_string())
                        })?;
                }
            }
            sum
        } else {
            payment.amount_vnd
        };

        let already_refunded_vnd: i64 = sqlx::query_scalar(
            r#"
            SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
            FROM refunds
            WHERE payment_id = $1
              AND status IN ('pending', 'completed', 'manual_review')
              AND affects_escrow
              AND NOT is_late_payment
            "#,
        )
        .bind(payment.id)
        .fetch_one(&mut *tx)
        .await?;

        let suborder_alloc_payable = target_alloc.map(allocation_buyer_payable_vnd).transpose()?;

        let suborder_already_refunded_vnd: i64 = if let Some(alloc) = target_alloc {
            sum_suborder_refunded_tx(
                &mut tx,
                payment.id,
                &alloc.order_id,
                &payment.order_id,
                allocations.len() == 1,
            )
            .await?
        } else {
            0
        };

        let (remaining_after, suborder_remaining_after) = validate_suborder_refund_cap(
            payment_collected_vnd,
            already_refunded_vnd,
            suborder_alloc_payable,
            suborder_already_refunded_vnd,
            input.amount_vnd,
        )?;

        // F11: Under the payment group lock (strictly serializing concurrent split-refund races),
        // evaluate 24-hour rolling manual refund total for this payment_id (counting pending_approval
        // + executed manual refunds, excluding rejected).
        if input.require_approval_if_large {
            let rolling_24h_manual_sum_vnd: i64 = sqlx::query_scalar(
                r#"
                SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
                FROM refunds
                WHERE payment_id = $1
                  AND requested_by IS NOT NULL
                  AND requested_by <> ''
                  AND NOT is_late_payment
                  AND status IN ('pending_approval', 'pending', 'completed', 'manual_review')
                  AND created_at >= NOW() - INTERVAL '24 hours'
                "#,
            )
            .bind(payment.id)
            .fetch_one(&mut *tx)
            .await?;

            if should_require_large_refund_approval(
                input.amount_vnd,
                rolling_24h_manual_sum_vnd,
                self.config.large_refund_approval_vnd,
            ) {
                let pending_approval_insert = sqlx::query_as::<_, RefundRecord>(
                    r#"
                    INSERT INTO refunds (
                        payment_id, order_id, amount_vnd, reason, status, idempotency_key,
                        requested_by, created_by_service, is_late_payment, affects_escrow
                    )
                    VALUES ($1, $2, $3, $4, 'pending_approval', $5, $6, $7, FALSE, TRUE)
                    RETURNING *
                    "#,
                )
                .bind(payment.id)
                .bind(&order_id)
                .bind(input.amount_vnd)
                .bind(&input.reason)
                .bind(&input.idempotency_key)
                .bind(effective_requested_by.as_deref())
                .bind(input.created_by_service)
                .fetch_one(&mut *tx)
                .await;

                let refund = match pending_approval_insert {
                    Ok(r) => r,
                    Err(err) if is_unique_violation(&err, "idempotency_key") => {
                        let _ = tx.rollback().await;
                        if let Some(existing) = sqlx::query_as::<_, RefundRecord>(
                            "SELECT * FROM refunds WHERE idempotency_key = $1",
                        )
                        .bind(&input.idempotency_key)
                        .fetch_optional(&self.pool)
                        .await?
                        {
                            validate_existing_refund_replay(&self.pool, &existing, &input).await?;
                            return Ok(CreateRefundOutput {
                                refund: existing,
                                already_existed: true,
                            });
                        }
                        return Err(PaymentError::Database(err));
                    }
                    Err(err) => return Err(PaymentError::Database(err)),
                };

                tx.commit().await?;
                info!(
                    trace_id = %input.trace_id,
                    refund_id = %refund.id,
                    payment_id = %payment.id,
                    amount_vnd = refund.amount_vnd,
                    rolling_24h_manual_sum_vnd = rolling_24h_manual_sum_vnd,
                    threshold_vnd = self.config.large_refund_approval_vnd,
                    requested_by = ?refund.requested_by,
                    "large refund queued in pending_approval (0 ledger postings, 0 outbox events)"
                );
                return Ok(CreateRefundOutput {
                    refund,
                    already_existed: false,
                });
            }
        }

        // If allocation has already been settled (`pending_release` or `released`), claw back from
        // `seller_pending:<shop>` then `seller_available:<shop>` (409 if insufficient) without
        // touching `escrow_liability` (R4 / PAY-B2).
        let post_settlement_clawback: Option<(String, i64, i64)> = match target_alloc {
            Some(alloc)
                if matches!(alloc.escrow_status.as_str(), "pending_release" | "released") =>
            {
                let (pending_bal, available_bal) =
                    ledger::lock_and_get_seller_balances_tx(&mut tx, &alloc.shop_id).await?;
                let (from_pending, from_available) =
                    compute_post_settlement_clawback(pending_bal, available_bal, input.amount_vnd)?;
                Some((alloc.shop_id.clone(), from_pending, from_available))
            }
            _ => None,
        };

        // F4: For ZALOPAY payments, persist deterministic `provider_m_refund_id` and
        // `provider_status = 'pending'` BEFORE calling `/v2/refund` so retries never double-refund.
        let refund_id = Uuid::new_v4();
        let (provider_m_refund_id, provider_status) = if payment.method == "ZALOPAY" {
            let app_id = self.zalopay.effective_app_id()?;
            (
                Some(generate_m_refund_id(app_id, refund_id, Utc::now())),
                Some(REFUND_PROVIDER_STATUS_PENDING),
            )
        } else {
            (None, None)
        };

        let refund_insert = sqlx::query_as::<_, RefundRecord>(
            r#"
            INSERT INTO refunds (
                id, payment_id, order_id, amount_vnd, reason, status, idempotency_key,
                provider_m_refund_id, provider_status, requested_by, created_by_service,
                is_late_payment, affects_escrow
            )
            VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $8, $9, $10, FALSE, TRUE)
            RETURNING *
            "#,
        )
        .bind(refund_id)
        .bind(payment.id)
        .bind(&order_id)
        .bind(input.amount_vnd)
        .bind(&input.reason)
        .bind(&input.idempotency_key)
        .bind(provider_m_refund_id.as_deref())
        .bind(provider_status)
        .bind(effective_requested_by.as_deref())
        .bind(input.created_by_service)
        .fetch_one(&mut *tx)
        .await;

        let refund = match refund_insert {
            Ok(r) => r,
            Err(err) if is_unique_violation(&err, "idempotency_key") => {
                // Concurrent duplicate idempotency_key race: roll back and validate winning refund (P2-8 / P3-8).
                let _ = tx.rollback().await;
                if let Some(existing) = sqlx::query_as::<_, RefundRecord>(
                    "SELECT * FROM refunds WHERE idempotency_key = $1",
                )
                .bind(&input.idempotency_key)
                .fetch_optional(&self.pool)
                .await?
                {
                    validate_existing_refund_replay(&self.pool, &existing, &input).await?;
                    return Ok(CreateRefundOutput {
                        refund: existing,
                        already_existed: true,
                    });
                }
                return Err(PaymentError::Database(err));
            }
            Err(err) => return Err(PaymentError::Database(err)),
        };

        // D10 (N11): Determine `is_post_settlement` inside the SAME transaction where `allocations`
        // are locked `FOR UPDATE`, matching the exact ledger path posted below.
        let is_post_settlement = post_settlement_clawback.is_some();
        let channel = resolve_refund_channel_tx(&mut tx, &payment, &order_id).await?;

        let draft = if let Some((shop_id, from_pending, from_available)) = post_settlement_clawback
        {
            build_post_settlement_refund_tx(
                refund.id,
                payment.id,
                &shop_id,
                from_pending,
                from_available,
                &refund.reason,
                channel,
            )?
        } else {
            build_refund_tx(
                refund.id,
                payment.id,
                refund.amount_vnd,
                &refund.reason,
                channel,
            )
        };
        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
            self.metrics.record_ledger_tx("refund");
        }

        // Mark only the refunded sub-order's allocation as refunded when fully refunded
        // (including post-settlement `pending_release`/`released` clawback — P2-9) so partial
        // refunds or sibling shops stay intact.
        if suborder_remaining_after == Some(0) || remaining_after == 0 {
            if let Some(alloc) = target_alloc {
                sqlx::query(
                    r#"
                    UPDATE payment_shop_allocations
                    SET escrow_status = 'refunded'
                    WHERE id = $1 AND escrow_status IN ('held', 'pending_release', 'released')
                    "#,
                )
                .bind(alloc.id)
                .execute(&mut *tx)
                .await?;
            }
        }

        let remaining_active_allocs: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM payment_shop_allocations
            WHERE payment_id = $1
              AND escrow_status IN ('awaiting_collection', 'held', 'pending_release')
            "#,
        )
        .bind(payment.id)
        .fetch_one(&mut *tx)
        .await?;

        let sse_updated = if remaining_after == 0 && remaining_active_allocs == 0 {
            sqlx::query_as::<_, PaymentRecord>(
                "UPDATE payments SET status = 'refunded', updated_at = NOW() WHERE id = $1 RETURNING *",
            )
            .bind(payment.id)
            .fetch_optional(&mut *tx)
            .await?
        } else {
            None
        };

        insert_outbox_event(
            &mut tx,
            "payment.refunded",
            &payment.order_group_id,
            &input.trace_id,
            build_payment_refunded_payload(&refund, &payment, &order_id, is_post_settlement),
        )
        .await?;

        tx.commit().await?;
        if let Some(ref updated) = sse_updated {
            self.emit_sse(updated);
        }

        // F4: Dispatch ZaloPay wallet refund outside the DB transaction (m_refund_id already saved).
        let final_refund = if payment.method == "ZALOPAY" {
            self.dispatch_zalopay_refund_best_effort(refund.id, &input.trace_id)
                .await
                .unwrap_or(refund)
        } else {
            refund
        };

        info!(
            trace_id = %input.trace_id,
            refund_id = %final_refund.id,
            payment_id = %payment.id,
            amount_vnd = final_refund.amount_vnd,
            status = %final_refund.status,
            "refund created and accrued in double-entry ledger"
        );
        Ok(CreateRefundOutput {
            refund: final_refund,
            already_existed: false,
        })
    }

    /// Approves or rejects a `pending_approval` large refund request (F11 Maker-Checker).
    /// - Enforces `approved_by <> requested_by` in application logic (`MAKER_CHECKER_CONFLICT`,
    ///   HTTP 403 / gRPC `PermissionDenied`) AND at DB CHECK level (`chk_refunds_maker_ne_checker`).
    /// - Serializes concurrent checker approvals via `lock_payment_group_tx` + `FOR UPDATE` on the
    ///   refund row; a second concurrent checker sees non-`pending_approval` and gets
    ///   `FailedPrecondition`.
    /// - On `approve == true`: re-checks refund eligibility and remaining refundable cap under lock,
    ///   transitions `pending_approval -> pending`, posts the double-entry ledger transaction, and
    ///   writes outbox `payment.refunded` inside ONE DB transaction (then dispatches ZaloPay wallet
    ///   refund if `ZALOPAY`).
    /// - On `approve == false`: transitions `pending_approval -> rejected` with 0 ledger postings
    ///   and writes outbox `payment.refund_rejected` (`aggregate_id = order_group_id`) inside the
    ///   SAME DB transaction so `order` can reopen an associated `REFUNDING` return (F11).
    pub async fn decide_refund_approval(
        &self,
        input: DecideRefundApprovalInput,
    ) -> Result<RefundRecord> {
        let checker = input.checker_user_id.trim();
        if checker.is_empty() {
            return Err(PaymentError::Unauthorized(
                "authenticated checker identity is required".to_string(),
            ));
        }
        let approval_note = input.note.trim();

        let mut tx = self.pool.begin().await?;

        let meta: (Uuid, String, String) = sqlx::query_as(
            r#"
            SELECT p.id, p.order_id, p.order_group_id
            FROM refunds r
            JOIN payments p ON p.id = r.payment_id
            WHERE r.id = $1
            "#,
        )
        .bind(input.refund_id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or_else(|| PaymentError::NotFound("refund not found".to_string()))?;

        let (payment_id, pay_order_id, pay_group_id) = meta;
        let group = lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;
        let payment = group
            .into_iter()
            .find(|p| p.id == payment_id)
            .ok_or_else(|| PaymentError::NotFound("payment not found for refund".to_string()))?;

        let refund =
            sqlx::query_as::<_, RefundRecord>("SELECT * FROM refunds WHERE id = $1 FOR UPDATE")
                .bind(input.refund_id)
                .fetch_optional(&mut *tx)
                .await?
                .ok_or_else(|| PaymentError::NotFound("refund not found".to_string()))?;

        if refund.status != "pending_approval" {
            return Err(PaymentError::FailedPrecondition(format!(
                "refund {} is not pending approval (status='{}')",
                refund.id, refund.status
            )));
        }

        if refund.requested_by.as_deref().map(str::trim) == Some(checker) {
            return Err(PaymentError::Forbidden(
                "MAKER_CHECKER_CONFLICT: refund requester cannot approve or reject their own request"
                    .to_string(),
            ));
        }

        if !input.approve {
            let rejected = sqlx::query_as::<_, RefundRecord>(
                r#"
                UPDATE refunds
                SET status = 'rejected',
                    approved_by = $1,
                    approved_at = NOW(),
                    approval_note = $2,
                    updated_at = NOW()
                WHERE id = $3 AND status = 'pending_approval'
                RETURNING *
                "#,
            )
            .bind(checker)
            .bind((!approval_note.is_empty()).then_some(approval_note))
            .bind(refund.id)
            .fetch_optional(&mut *tx)
            .await?
            .ok_or_else(|| {
                PaymentError::FailedPrecondition(format!(
                    "refund {} was already decided concurrently",
                    refund.id
                ))
            })?;

            let reject_reason = if !approval_note.is_empty() {
                approval_note
            } else {
                "refund_approval_rejected"
            };
            insert_outbox_event(
                &mut tx,
                "payment.refund_rejected",
                &payment.order_group_id,
                &input.trace_id,
                json!({
                    "refund_id": rejected.id.to_string(),
                    "payment_id": rejected.payment_id.to_string(),
                    "order_id": rejected.order_id,
                    "user_id": payment.user_id,
                    "amount_vnd": rejected.amount_vnd,
                    "reason": reject_reason,
                    "rejected_by": checker,
                    "idempotency_key": rejected.idempotency_key,
                    "sales_channel": payment.sales_channel,
                    "storefront_shop_id": payment.storefront_shop_id,
                }),
            )
            .await?;

            tx.commit().await?;
            info!(
                trace_id = %input.trace_id,
                refund_id = %rejected.id,
                payment_id = %payment.id,
                approved_by = checker,
                "large refund approval request rejected and payment.refund_rejected queued in outbox (0 ledger postings)"
            );
            return Ok(rejected);
        }

        // Approve branch: lock allocations, re-validate eligibility + remaining refundable cap +
        // post-settlement clawback, transition to `pending`, post ledger, and insert outbox in 1 TX.
        let allocations = sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT * FROM payment_shop_allocations
            WHERE payment_id = $1
            ORDER BY id ASC
            FOR UPDATE
            "#,
        )
        .bind(payment.id)
        .fetch_all(&mut *tx)
        .await?;

        let target_alloc: Option<&ShopAllocationRecord> = if allocations.is_empty() {
            None
        } else if let Some(a) = allocations.iter().find(|a| a.order_id == refund.order_id) {
            Some(a)
        } else if allocations.len() == 1
            && (refund.order_id == payment.order_id || refund.order_id == payment.order_group_id)
        {
            Some(&allocations[0])
        } else {
            return Err(PaymentError::Conflict(format!(
                "order_id '{}' does not match any sub-order allocation on payment {}",
                refund.order_id, payment.id
            )));
        };

        validate_refund_eligibility(
            &payment.method,
            &payment.status,
            payment.amount_vnd,
            target_alloc.map(|a| a.escrow_status.as_str()),
        )?;

        let payment_collected_vnd = if payment.method == "COD" && !allocations.is_empty() {
            let mut sum = 0_i64;
            for a in &allocations {
                if matches!(
                    a.escrow_status.as_str(),
                    "held" | "pending_release" | "released" | "refunded"
                ) {
                    sum = sum
                        .checked_add(allocation_buyer_payable_vnd(a)?)
                        .ok_or_else(|| {
                            PaymentError::LedgerInvariant("collected COD sum overflow".to_string())
                        })?;
                }
            }
            sum
        } else {
            payment.amount_vnd
        };

        let already_refunded_vnd: i64 = sqlx::query_scalar(
            r#"
            SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
            FROM refunds
            WHERE payment_id = $1
              AND status IN ('pending', 'completed', 'manual_review')
              AND affects_escrow
              AND NOT is_late_payment
            "#,
        )
        .bind(payment.id)
        .fetch_one(&mut *tx)
        .await?;

        let suborder_alloc_payable = target_alloc.map(allocation_buyer_payable_vnd).transpose()?;
        let suborder_already_refunded_vnd: i64 = if let Some(alloc) = target_alloc {
            sum_suborder_refunded_tx(
                &mut tx,
                payment.id,
                &alloc.order_id,
                &payment.order_id,
                allocations.len() == 1,
            )
            .await?
        } else {
            0
        };

        let (remaining_after, suborder_remaining_after) = validate_suborder_refund_cap(
            payment_collected_vnd,
            already_refunded_vnd,
            suborder_alloc_payable,
            suborder_already_refunded_vnd,
            refund.amount_vnd,
        )?;

        let post_settlement_clawback: Option<(String, i64, i64)> = match target_alloc {
            Some(alloc)
                if matches!(alloc.escrow_status.as_str(), "pending_release" | "released") =>
            {
                let (pending_bal, available_bal) =
                    ledger::lock_and_get_seller_balances_tx(&mut tx, &alloc.shop_id).await?;
                let (from_pending, from_available) = compute_post_settlement_clawback(
                    pending_bal,
                    available_bal,
                    refund.amount_vnd,
                )?;
                Some((alloc.shop_id.clone(), from_pending, from_available))
            }
            _ => None,
        };

        let (provider_m_refund_id, provider_status) = if payment.method == "ZALOPAY" {
            let app_id = self.zalopay.effective_app_id()?;
            (
                Some(generate_m_refund_id(app_id, refund.id, Utc::now())),
                Some(REFUND_PROVIDER_STATUS_PENDING),
            )
        } else {
            (None, None)
        };

        let approved = sqlx::query_as::<_, RefundRecord>(
            r#"
            UPDATE refunds
            SET status = 'pending',
                approved_by = $1,
                approved_at = NOW(),
                approval_note = $2,
                provider_m_refund_id = COALESCE(provider_m_refund_id, $3),
                provider_status = COALESCE(provider_status, $4),
                updated_at = NOW()
            WHERE id = $5 AND status = 'pending_approval'
            RETURNING *
            "#,
        )
        .bind(checker)
        .bind((!approval_note.is_empty()).then_some(approval_note))
        .bind(provider_m_refund_id.as_deref())
        .bind(provider_status)
        .bind(refund.id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or_else(|| {
            PaymentError::FailedPrecondition(format!(
                "refund {} was already decided concurrently",
                refund.id
            ))
        })?;

        let is_post_settlement = post_settlement_clawback.is_some();
        let channel = resolve_refund_channel_tx(&mut tx, &payment, &approved.order_id).await?;
        let draft = if let Some((shop_id, from_pending, from_available)) = post_settlement_clawback
        {
            build_post_settlement_refund_tx(
                approved.id,
                payment.id,
                &shop_id,
                from_pending,
                from_available,
                &approved.reason,
                channel,
            )?
        } else {
            build_refund_tx(
                approved.id,
                payment.id,
                approved.amount_vnd,
                &approved.reason,
                channel,
            )
        };
        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
            self.metrics.record_ledger_tx("refund");
        }

        if suborder_remaining_after == Some(0) || remaining_after == 0 {
            if let Some(alloc) = target_alloc {
                sqlx::query(
                    r#"
                    UPDATE payment_shop_allocations
                    SET escrow_status = 'refunded'
                    WHERE id = $1 AND escrow_status IN ('held', 'pending_release', 'released')
                    "#,
                )
                .bind(alloc.id)
                .execute(&mut *tx)
                .await?;
            }
        }

        let remaining_active_allocs: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM payment_shop_allocations
            WHERE payment_id = $1
              AND escrow_status IN ('awaiting_collection', 'held', 'pending_release')
            "#,
        )
        .bind(payment.id)
        .fetch_one(&mut *tx)
        .await?;

        let sse_updated = if remaining_after == 0 && remaining_active_allocs == 0 {
            sqlx::query_as::<_, PaymentRecord>(
                "UPDATE payments SET status = 'refunded', updated_at = NOW() WHERE id = $1 RETURNING *",
            )
            .bind(payment.id)
            .fetch_optional(&mut *tx)
            .await?
        } else {
            None
        };

        insert_outbox_event(
            &mut tx,
            "payment.refunded",
            &payment.order_group_id,
            &input.trace_id,
            build_payment_refunded_payload(
                &approved,
                &payment,
                &approved.order_id,
                is_post_settlement,
            ),
        )
        .await?;

        tx.commit().await?;
        if let Some(ref updated) = sse_updated {
            self.emit_sse(updated);
        }

        let final_refund = if payment.method == "ZALOPAY" {
            self.dispatch_zalopay_refund_best_effort(approved.id, &input.trace_id)
                .await
                .unwrap_or(approved)
        } else {
            approved
        };

        info!(
            trace_id = %input.trace_id,
            refund_id = %final_refund.id,
            payment_id = %payment.id,
            approved_by = checker,
            amount_vnd = final_refund.amount_vnd,
            status = %final_refund.status,
            "large refund approved and posted to double-entry ledger"
        );
        Ok(final_refund)
    }

    /// Worst-case HTTP duration across retries plus safety margin for ZaloPay wallet refund leases
    /// (F4 / p4 Item B): always >= 90s so a sibling replica never collides with an in-flight call.
    pub fn zalopay_refund_lease_duration(&self) -> Duration {
        let max_attempts = (self.config.zalopay.max_retries as i64).saturating_add(1);
        let http_secs = self.config.zalopay.http_timeout.as_secs() as i64;
        let computed = http_secs.saturating_mul(max_attempts).saturating_add(30);
        Duration::seconds(computed.max(90))
    }

    /// Best-effort immediate dispatch of a newly created/approved ZaloPay wallet refund (F4).
    /// Claims the row with `FOR UPDATE SKIP LOCKED`, persists a unique lease token + lease expiry
    /// (`> worst-case HTTP`), rotates `m_refund_id` if never sent and GMT+7 day rolled over
    /// (p4 Item D), and calls `/v2/refund` strictly outside any DB transaction.
    pub async fn dispatch_zalopay_refund_best_effort(
        &self,
        refund_id: Uuid,
        trace_id: &str,
    ) -> Result<RefundRecord> {
        let now = Utc::now();
        let lease_token = Uuid::new_v4();
        let lease_until = now + self.zalopay_refund_lease_duration();

        let mut tx = self.pool.begin().await?;
        let claimed: Option<ZaloPayRefundClaimRow> = sqlx::query_as(
            r#"
            SELECT r.id, r.payment_id, r.order_id, r.amount_vnd, r.reason, r.idempotency_key,
                   r.provider_m_refund_id, r.provider_status, r.provider_dispatch_attempted,
                   p.provider_tx_id, p.order_id AS payment_order_id,
                   p.order_group_id AS payment_order_group_id, p.user_id AS payment_user_id,
                   p.sales_channel AS payment_sales_channel,
                   p.storefront_shop_id AS payment_storefront_shop_id
            FROM refunds r
            JOIN payments p ON p.id = r.payment_id
            WHERE r.id = $1
              AND r.status = 'pending'
              AND r.provider_m_refund_id IS NOT NULL
              AND r.provider_status = 'pending'
              AND (r.provider_lease_token IS NULL OR r.provider_lease_until <= $2)
            FOR UPDATE OF r SKIP LOCKED
            "#,
        )
        .bind(refund_id)
        .bind(now)
        .fetch_optional(&mut *tx)
        .await?;

        let Some(mut row) = claimed else {
            tx.commit().await?;
            return sqlx::query_as::<_, RefundRecord>("SELECT * FROM refunds WHERE id = $1")
                .bind(refund_id)
                .fetch_one(&self.pool)
                .await
                .map_err(PaymentError::Database);
        };

        // p4 Item D: Never-sent refund crossing GMT+7 midnight rotates `m_refund_id` inside the
        // claim transaction BEFORE calling `/v2/refund`.
        if !row.provider_dispatch_attempted {
            if let Some(ref current_mref) = row.provider_m_refund_id {
                if !m_refund_id_matches_vn_day(current_mref, now) {
                    if let Ok(app_id) = self.zalopay.effective_app_id() {
                        let rotated =
                            rotate_m_refund_id_for_vn_day(app_id, row.id, current_mref, now);
                        row.provider_m_refund_id = Some(rotated);
                    }
                }
            }
        }

        sqlx::query(
            r#"
            UPDATE refunds
            SET provider_m_refund_id = $1,
                provider_status = $2,
                provider_dispatch_attempted = TRUE,
                provider_lease_token = $3,
                provider_lease_until = $4,
                updated_at = NOW()
            WHERE id = $5
            "#,
        )
        .bind(row.provider_m_refund_id.as_deref())
        .bind(REFUND_PROVIDER_STATUS_PROCESSING)
        .bind(lease_token)
        .bind(lease_until)
        .bind(row.id)
        .execute(&mut *tx)
        .await?;
        tx.commit().await?;

        self.execute_claimed_zalopay_refund(&row, true, lease_token, now, trace_id)
            .await?;

        sqlx::query_as::<_, RefundRecord>("SELECT * FROM refunds WHERE id = $1")
            .bind(refund_id)
            .fetch_one(&self.pool)
            .await
            .map_err(PaymentError::Database)
    }

    /// Background poller for ZaloPay wallet refunds (`pending` -> `/v2/refund`, `processing` ->
    /// `/v2/query_refund`) using `FOR UPDATE OF r SKIP LOCKED` + lease/CAS tokens (F4 / p4 Item B).
    pub async fn poll_zalopay_refunds(&self) -> Result<usize> {
        self.poll_zalopay_refunds_with_options(
            Utc::now(),
            Duration::seconds(5),
            self.zalopay_refund_lease_duration(),
        )
        .await
    }

    /// Same as [`Self::poll_zalopay_refunds`] with an explicit post-HTTP re-poll cooldown
    /// (in-flight HTTP calls remain protected by the full lease duration >= 90s).
    pub async fn poll_zalopay_refunds_with_cooldown(&self, cooldown: Duration) -> Result<usize> {
        self.poll_zalopay_refunds_with_options(
            Utc::now(),
            cooldown,
            self.zalopay_refund_lease_duration(),
        )
        .await
    }

    /// Deterministic entrypoint for [`Self::poll_zalopay_refunds`] with explicit clock `now`,
    /// post-HTTP `cooldown`, and in-flight `lease_duration` (used by p4 B/D race & midnight tests).
    pub async fn poll_zalopay_refunds_with_options(
        &self,
        now: DateTime<Utc>,
        cooldown: Duration,
        lease_duration: Duration,
    ) -> Result<usize> {
        let cutoff = now - cooldown;
        let lease_until = now + lease_duration;
        let mut tx = self.pool.begin().await?;

        let claimed: Vec<ZaloPayRefundClaimRow> = sqlx::query_as(
            r#"
            SELECT r.id, r.payment_id, r.order_id, r.amount_vnd, r.reason, r.idempotency_key,
                   r.provider_m_refund_id, r.provider_status, r.provider_dispatch_attempted,
                   p.provider_tx_id, p.order_id AS payment_order_id,
                   p.order_group_id AS payment_order_group_id, p.user_id AS payment_user_id,
                   p.sales_channel AS payment_sales_channel,
                   p.storefront_shop_id AS payment_storefront_shop_id
            FROM refunds r
            JOIN payments p ON p.id = r.payment_id
            WHERE r.status = 'pending'
              AND r.provider_m_refund_id IS NOT NULL
              AND r.provider_status IN ('pending', 'processing')
              AND (
                  (r.provider_lease_token IS NULL AND (r.provider_status = 'pending' OR r.updated_at <= $1))
                  OR (r.provider_lease_until IS NOT NULL AND r.provider_lease_until <= $2)
              )
            ORDER BY r.provider_lease_until ASC NULLS FIRST, r.updated_at ASC
            LIMIT 20
            FOR UPDATE OF r SKIP LOCKED
            "#,
        )
        .bind(cutoff)
        .bind(now)
        .fetch_all(&mut *tx)
        .await?;

        if claimed.is_empty() {
            tx.commit().await?;
            return Ok(0);
        }

        let zalopay_app_id = self.zalopay.effective_app_id().ok();
        let mut work_items: Vec<(ZaloPayRefundClaimRow, bool, Uuid)> =
            Vec::with_capacity(claimed.len());

        for mut row in claimed {
            let needs_create =
                row.provider_status.as_deref() == Some(REFUND_PROVIDER_STATUS_PENDING);
            let lease_token = Uuid::new_v4();

            // p4 Item D: Only rotate `m_refund_id` at claim time when `/v2/refund` was NEVER sent
            // before (`provider_status == 'pending' && !provider_dispatch_attempted`). If already
            // dispatched before, we MUST query_refund with the original `m_refund_id` first.
            if needs_create && !row.provider_dispatch_attempted {
                if let (Some(app_id), Some(current_mref)) =
                    (zalopay_app_id, row.provider_m_refund_id.as_deref())
                {
                    if !m_refund_id_matches_vn_day(current_mref, now) {
                        let rotated =
                            rotate_m_refund_id_for_vn_day(app_id, row.id, current_mref, now);
                        row.provider_m_refund_id = Some(rotated);
                    }
                }
            }

            sqlx::query(
                r#"
                UPDATE refunds
                SET provider_m_refund_id = $1,
                    provider_status = $2,
                    provider_dispatch_attempted = CASE WHEN $3 THEN TRUE ELSE provider_dispatch_attempted END,
                    provider_lease_token = $4,
                    provider_lease_until = $5,
                    updated_at = NOW()
                WHERE id = $6
                "#,
            )
            .bind(row.provider_m_refund_id.as_deref())
            .bind(REFUND_PROVIDER_STATUS_PROCESSING)
            .bind(needs_create)
            .bind(lease_token)
            .bind(lease_until)
            .bind(row.id)
            .execute(&mut *tx)
            .await?;

            work_items.push((row, needs_create, lease_token));
        }

        tx.commit().await?;

        let mut processed = 0usize;
        for (row, needs_create, lease_token) in work_items {
            if let Err(e) = self
                .execute_claimed_zalopay_refund(
                    &row,
                    needs_create,
                    lease_token,
                    now,
                    "zalopay-refund-poller",
                )
                .await
            {
                warn!(
                    refund_id = %row.id,
                    error = %e,
                    "failed processing claimed ZaloPay refund; will retry on next poll"
                );
            } else {
                processed += 1;
            }
        }
        Ok(processed)
    }

    async fn release_zalopay_refund_lease_cas(
        &self,
        refund_id: Uuid,
        lease_token: Uuid,
        provider_refund_id: Option<&str>,
    ) -> Result<bool> {
        let res = sqlx::query(
            r#"
            UPDATE refunds
            SET provider_refund_id = COALESCE(provider_refund_id, $1),
                provider_lease_token = NULL,
                provider_lease_until = NULL,
                updated_at = NOW()
            WHERE id = $2
              AND status = 'pending'
              AND provider_status = 'processing'
              AND provider_lease_token = $3
            "#,
        )
        .bind(provider_refund_id)
        .bind(refund_id)
        .bind(lease_token)
        .execute(&self.pool)
        .await?;
        Ok(res.rows_affected() > 0)
    }

    async fn execute_claimed_zalopay_refund(
        &self,
        row: &ZaloPayRefundClaimRow,
        needs_create: bool,
        lease_token: Uuid,
        now: DateTime<Utc>,
        trace_id: &str,
    ) -> Result<()> {
        let Some(ref m_refund_id) = row.provider_m_refund_id else {
            return Ok(());
        };

        if needs_create {
            let Some(zp_trans_id) =
                resolve_zalopay_zp_trans_id(row.provider_tx_id.as_deref(), &row.idempotency_key)
            else {
                self.finalize_zalopay_refund_manual_review(
                    row,
                    lease_token,
                    "missing zp_trans_id for ZaloPay wallet refund",
                )
                .await?;
                return Ok(());
            };

            let resp = match self
                .zalopay
                .refund(&ZaloPayRefundInput {
                    m_refund_id: m_refund_id.clone(),
                    zp_trans_id,
                    amount_vnd: row.amount_vnd,
                    refund_fee_amount_vnd: None,
                    description: row.reason.clone(),
                    timestamp: Some(now),
                })
                .await
            {
                Ok(r) => r,
                Err(e) => {
                    warn!(
                        refund_id = %row.id,
                        m_refund_id = %m_refund_id,
                        error = %e,
                        "ZaloPay /v2/refund transport error; keeping provider_status='processing' for query_refund poll"
                    );
                    let _ = self
                        .release_zalopay_refund_lease_cas(row.id, lease_token, None)
                        .await;
                    return Ok(());
                }
            };

            let provider_ref_str = if resp.refund_id > 0 {
                resp.refund_id.to_string()
            } else {
                m_refund_id.clone()
            };

            match resp.return_code {
                ZALOPAY_RETURN_SUCCESS => {
                    self.finalize_zalopay_refund_settled(
                        row,
                        m_refund_id,
                        &provider_ref_str,
                        trace_id,
                    )
                    .await?;
                }
                ZALOPAY_RETURN_PROCESSING => {
                    let pid = (resp.refund_id > 0).then_some(provider_ref_str.as_str());
                    let _ = self
                        .release_zalopay_refund_lease_cas(row.id, lease_token, pid)
                        .await;
                }
                ZALOPAY_RETURN_FAIL
                    if is_zalopay_refund_transient_sub_code(resp.sub_return_code) =>
                {
                    warn!(
                        refund_id = %row.id,
                        m_refund_id = %m_refund_id,
                        sub_return_code = resp.sub_return_code,
                        "transient ZaloPay /v2/refund error; will verify via query_refund"
                    );
                    let _ = self
                        .release_zalopay_refund_lease_cas(row.id, lease_token, None)
                        .await;
                }
                _ => {
                    // p4 Item B: Before escalating ANY non-transient `/v2/refund` error (especially
                    // duplicate `m_refund_id`) to `manual_review`, ALWAYS query `/v2/query_refund`
                    // first so an already-accepted/settled refund is NEVER double-refunded manually.
                    match self.zalopay.query_refund(m_refund_id, Some(now)).await {
                        Ok(q_resp) if q_resp.return_code == ZALOPAY_RETURN_SUCCESS => {
                            self.finalize_zalopay_refund_settled(
                                row,
                                m_refund_id,
                                m_refund_id,
                                trace_id,
                            )
                            .await?;
                        }
                        Ok(q_resp)
                            if q_resp.return_code == ZALOPAY_RETURN_PROCESSING
                                || (q_resp.return_code == ZALOPAY_RETURN_FAIL
                                    && is_zalopay_refund_transient_sub_code(
                                        q_resp.sub_return_code,
                                    )) =>
                        {
                            warn!(
                                refund_id = %row.id,
                                m_refund_id = %m_refund_id,
                                refund_sub_code = resp.sub_return_code,
                                query_return_code = q_resp.return_code,
                                query_sub_code = q_resp.sub_return_code,
                                "ZaloPay /v2/refund failed but /v2/query_refund reports active/transient state; keeping processing"
                            );
                            let _ = self
                                .release_zalopay_refund_lease_cas(row.id, lease_token, None)
                                .await;
                        }
                        Err(e) => {
                            warn!(
                                refund_id = %row.id,
                                m_refund_id = %m_refund_id,
                                error = %e,
                                "ZaloPay /v2/query_refund transport error after /v2/refund failure; keeping processing instead of manual_review"
                            );
                            let _ = self
                                .release_zalopay_refund_lease_cas(row.id, lease_token, None)
                                .await;
                        }
                        Ok(q_resp) => {
                            let note = format!(
                                "zalopay refund permanent error (verified via query_refund rc={} sub={}): return_code={} sub_return_code={} msg={}",
                                q_resp.return_code,
                                q_resp.sub_return_code,
                                resp.return_code,
                                resp.sub_return_code,
                                resp.return_message
                            );
                            self.finalize_zalopay_refund_manual_review(row, lease_token, &note)
                                .await?;
                        }
                    }
                }
            }
            return Ok(());
        }

        // Row was already in `processing` -> poll `/v2/query_refund` with the persisted `m_refund_id`.
        let q_resp = match self.zalopay.query_refund(m_refund_id, Some(now)).await {
            Ok(r) => r,
            Err(e) => {
                warn!(
                    refund_id = %row.id,
                    m_refund_id = %m_refund_id,
                    error = %e,
                    "ZaloPay /v2/query_refund transport error; keeping processing for retry"
                );
                let _ = self
                    .release_zalopay_refund_lease_cas(row.id, lease_token, None)
                    .await;
                return Ok(());
            }
        };

        match q_resp.return_code {
            ZALOPAY_RETURN_SUCCESS => {
                self.finalize_zalopay_refund_settled(row, m_refund_id, m_refund_id, trace_id)
                    .await?;
            }
            ZALOPAY_RETURN_PROCESSING => {
                let _ = self
                    .release_zalopay_refund_lease_cas(row.id, lease_token, None)
                    .await;
            }
            ZALOPAY_RETURN_FAIL if q_resp.sub_return_code == -101 => {
                // M_REFUND_ID_NOT_FOUND: confirmed not present at ZaloPay.
                // p4 Item B: Strict CAS by `provider_lease_token` AND `provider_refund_id IS NULL`
                // so a stale replica can NEVER roll back a refund that another replica already accepted.
                // p4 Item D: If GMT+7 day rolled over, rotate `provider_m_refund_id` in this DB TX
                // BEFORE any subsequent `/v2/refund` HTTP call.
                let next_m_refund_id = if !m_refund_id_matches_vn_day(m_refund_id, now) {
                    if let Ok(app_id) = self.zalopay.effective_app_id() {
                        rotate_m_refund_id_for_vn_day(app_id, row.id, m_refund_id, now)
                    } else {
                        m_refund_id.clone()
                    }
                } else {
                    m_refund_id.clone()
                };

                sqlx::query(
                    r#"
                    UPDATE refunds
                    SET provider_m_refund_id = $1,
                        provider_status = $2,
                        provider_dispatch_attempted = FALSE,
                        provider_lease_token = NULL,
                        provider_lease_until = NULL,
                        updated_at = NOW()
                    WHERE id = $3
                      AND status = 'pending'
                      AND provider_status = 'processing'
                      AND provider_lease_token = $4
                      AND provider_refund_id IS NULL
                    "#,
                )
                .bind(&next_m_refund_id)
                .bind(REFUND_PROVIDER_STATUS_PENDING)
                .bind(row.id)
                .bind(lease_token)
                .execute(&self.pool)
                .await?;
            }
            ZALOPAY_RETURN_FAIL if is_zalopay_refund_transient_sub_code(q_resp.sub_return_code) => {
                warn!(
                    refund_id = %row.id,
                    m_refund_id = %m_refund_id,
                    sub_return_code = q_resp.sub_return_code,
                    "transient ZaloPay /v2/query_refund error; will retry later"
                );
                let _ = self
                    .release_zalopay_refund_lease_cas(row.id, lease_token, None)
                    .await;
            }
            _ => {
                let note = format!(
                    "zalopay query_refund permanent error: return_code={} sub_return_code={} msg={}",
                    q_resp.return_code, q_resp.sub_return_code, q_resp.return_message
                );
                self.finalize_zalopay_refund_manual_review(row, lease_token, &note)
                    .await?;
            }
        }
        Ok(())
    }

    async fn finalize_zalopay_refund_settled(
        &self,
        row: &ZaloPayRefundClaimRow,
        m_refund_id: &str,
        provider_refund_id: &str,
        trace_id: &str,
    ) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let _locked_group =
            lock_payment_group_tx(&mut tx, &row.payment_order_id, &row.payment_order_group_id)
                .await?;

        let Some(refund) =
            sqlx::query_as::<_, RefundRecord>("SELECT * FROM refunds WHERE id = $1 FOR UPDATE")
                .bind(row.id)
                .fetch_optional(&mut *tx)
                .await?
        else {
            tx.commit().await?;
            return Ok(());
        };

        if refund.status != "pending" {
            tx.commit().await?;
            return Ok(());
        }

        let settle_key = format!("zalopay-refund-settle:{m_refund_id}");
        let effective_provider_refund_id = refund
            .provider_refund_id
            .as_deref()
            .filter(|s| !s.trim().is_empty())
            .unwrap_or(provider_refund_id);

        let updated = sqlx::query_as::<_, RefundRecord>(
            r#"
            UPDATE refunds
            SET status = 'completed',
                provider_status = $1,
                provider_refund_id = $2,
                provider_lease_token = NULL,
                provider_lease_until = NULL,
                settlement_idempotency_key = COALESCE(settlement_idempotency_key, $3),
                settlement_note = COALESCE(settlement_note, $4),
                settled_at = NOW(),
                updated_at = NOW()
            WHERE id = $5 AND status = 'pending'
            RETURNING *
            "#,
        )
        .bind(REFUND_PROVIDER_STATUS_SUCCEEDED)
        .bind(effective_provider_refund_id)
        .bind(&settle_key)
        .bind(format!("ZaloPay wallet refund settled ({m_refund_id})"))
        .bind(refund.id)
        .fetch_optional(&mut *tx)
        .await?;

        let Some(updated) = updated else {
            tx.commit().await?;
            return Ok(());
        };

        let channel = ChannelAttribution::from_db(
            &row.payment_sales_channel,
            &row.payment_storefront_shop_id,
        );
        let draft = build_refund_settle_tx(updated.id, updated.amount_vnd, channel);
        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
            self.metrics.record_ledger_tx("refund_settle");
        }

        insert_outbox_event(
            &mut tx,
            "payment.refund_settled",
            &row.payment_order_group_id,
            trace_id,
            json!({
                "refund_id": updated.id.to_string(),
                "payment_id": updated.payment_id.to_string(),
                "order_id": updated.order_id,
                "user_id": row.payment_user_id,
                "amount_vnd": updated.amount_vnd,
                "sales_channel": row.payment_sales_channel,
                "storefront_shop_id": row.payment_storefront_shop_id,
            }),
        )
        .await?;

        tx.commit().await?;
        info!(
            trace_id = %trace_id,
            refund_id = %updated.id,
            payment_id = %updated.payment_id,
            m_refund_id = %m_refund_id,
            amount_vnd = updated.amount_vnd,
            "ZaloPay wallet refund settled in double-entry ledger"
        );
        Ok(())
    }

    async fn finalize_zalopay_refund_manual_review(
        &self,
        row: &ZaloPayRefundClaimRow,
        lease_token: Uuid,
        note: &str,
    ) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let _locked_group =
            lock_payment_group_tx(&mut tx, &row.payment_order_id, &row.payment_order_group_id)
                .await?;

        let res = sqlx::query(
            r#"
            UPDATE refunds
            SET status = 'manual_review',
                provider_status = $1,
                provider_lease_token = NULL,
                provider_lease_until = NULL,
                settlement_note = $2,
                updated_at = NOW()
            WHERE id = $3
              AND status = 'pending'
              AND provider_status = 'processing'
              AND provider_lease_token = $4
            "#,
        )
        .bind(REFUND_PROVIDER_STATUS_FAILED)
        .bind(note)
        .bind(row.id)
        .bind(lease_token)
        .execute(&mut *tx)
        .await?;

        tx.commit().await?;
        if res.rows_affected() > 0 {
            warn!(
                refund_id = %row.id,
                payment_id = %row.payment_id,
                note = %note,
                "ZaloPay wallet refund moved to manual_review (0 settlement ledger postings)"
            );
        }
        Ok(())
    }

    /// Lists refunds with pagination.
    /// - Admins see all matching refunds.
    /// - Non-admins (buyers) are strictly scoped to payments owned by `caller_user_id`; querying an
    ///   `order_id` belonging solely to another user returns `Forbidden` (403).
    pub async fn list_refunds(&self, filter: ListRefundsFilter) -> Result<ListRefundsResult> {
        let page = filter.page.max(1);
        let page_size = if filter.page_size <= 0 {
            20
        } else {
            filter.page_size.min(100)
        };
        let offset = i64::from(page - 1) * i64::from(page_size);

        let status_filter = filter
            .status
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty());
        let order_filter = filter
            .order_id
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty());

        let owner_filter: Option<&str> = if filter.is_admin {
            None
        } else {
            let uid = filter
                .caller_user_id
                .as_deref()
                .map(str::trim)
                .filter(|s| !s.is_empty())
                .ok_or_else(|| {
                    PaymentError::Unauthorized("authenticated user_id required".to_string())
                })?;

            // Defense-in-depth: if buyer queries a specific order_id belonging to another user, reject 403.
            if let Some(oid) = order_filter {
                let row = sqlx::query(
                    r#"
                    SELECT
                        COALESCE(BOOL_OR(p.user_id = $2), FALSE) AS owns_any,
                        COUNT(*)::BIGINT AS total_matches
                    FROM payments p
                    LEFT JOIN payment_shop_allocations a ON a.payment_id = p.id
                    WHERE p.order_id = $1 OR p.order_group_id = $1 OR a.order_id = $1
                    "#,
                )
                .bind(oid)
                .bind(uid)
                .fetch_one(&self.pool)
                .await?;
                let owns_any: bool = row.try_get("owns_any")?;
                let total_matches: i64 = row.try_get("total_matches")?;
                if total_matches > 0 && !owns_any {
                    return Err(PaymentError::Forbidden(
                        "cannot access refunds for another user's order".to_string(),
                    ));
                }
            }
            Some(uid)
        };

        let total: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM refunds r
            JOIN payments p ON p.id = r.payment_id
            WHERE ($1::TEXT IS NULL OR p.user_id = $1)
              AND ($2::TEXT IS NULL OR r.status = $2)
              AND (
                  $3::TEXT IS NULL
                  OR r.order_id = $3
                  OR p.order_group_id = $3
                  OR (
                      (r.is_late_payment OR NOT r.affects_escrow)
                      AND EXISTS (
                          SELECT 1
                          FROM payment_shop_allocations a
                          WHERE a.payment_id = p.id AND a.order_id = $3
                      )
                  )
              )
            "#,
        )
        .bind(owner_filter)
        .bind(status_filter)
        .bind(order_filter)
        .fetch_one(&self.pool)
        .await?;

        let refunds = sqlx::query_as::<_, RefundRecord>(
            r#"
            SELECT r.*
            FROM refunds r
            JOIN payments p ON p.id = r.payment_id
            WHERE ($1::TEXT IS NULL OR p.user_id = $1)
              AND ($2::TEXT IS NULL OR r.status = $2)
              AND (
                  $3::TEXT IS NULL
                  OR r.order_id = $3
                  OR p.order_group_id = $3
                  OR (
                      (r.is_late_payment OR NOT r.affects_escrow)
                      AND EXISTS (
                          SELECT 1
                          FROM payment_shop_allocations a
                          WHERE a.payment_id = p.id AND a.order_id = $3
                      )
                  )
              )
            ORDER BY r.created_at DESC, r.id DESC
            LIMIT $4 OFFSET $5
            "#,
        )
        .bind(owner_filter)
        .bind(status_filter)
        .bind(order_filter)
        .bind(i64::from(page_size))
        .bind(offset)
        .fetch_all(&self.pool)
        .await?;

        Ok(ListRefundsResult { refunds, total })
    }

    /// Saves or updates the buyer's receiving bank account on a `pending` refund (PAY-B11 / UC-PAY04).
    /// Strictly enforces payment ownership (`payment.user_id == caller_user_id`), editable until settled.
    pub async fn submit_refund_bank_account(
        &self,
        input: SubmitRefundBankAccountInput,
    ) -> Result<RefundRecord> {
        let caller_uid = input.caller_user_id.trim();
        if caller_uid.is_empty() {
            return Err(PaymentError::Unauthorized(
                "authenticated user_id is required".to_string(),
            ));
        }
        let (bank_code, bank_account_number, bank_account_masked, bank_account_name) =
            validate_refund_bank_account(
                &input.bank_code,
                &input.bank_account_number,
                &input.bank_account_name,
            )?;

        let mut tx = self.pool.begin().await?;

        let row = sqlx::query(
            r#"
            SELECT r.status AS refund_status, p.user_id AS payment_user_id
            FROM refunds r
            JOIN payments p ON p.id = r.payment_id
            WHERE r.id = $1
            FOR UPDATE OF r
            "#,
        )
        .bind(input.refund_id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or_else(|| PaymentError::NotFound("refund not found".to_string()))?;

        let refund_status: String = row.try_get("refund_status")?;
        let payment_user_id: String = row.try_get("payment_user_id")?;

        if payment_user_id != caller_uid {
            return Err(PaymentError::Forbidden(
                "cannot submit bank account for another user's refund".to_string(),
            ));
        }
        if !matches!(refund_status.as_str(), "pending" | "manual_review") {
            return Err(PaymentError::Conflict(format!(
                "cannot update bank account for refund in status '{refund_status}'"
            )));
        }

        let updated = sqlx::query_as::<_, RefundRecord>(
            r#"
            UPDATE refunds
            SET bank_code = $1,
                bank_account_number = $2,
                bank_account_masked = $3,
                bank_account_name = $4,
                updated_at = NOW()
            WHERE id = $5
            RETURNING *
            "#,
        )
        .bind(&bank_code)
        .bind(&bank_account_number)
        .bind(&bank_account_masked)
        .bind(&bank_account_name)
        .bind(input.refund_id)
        .fetch_one(&mut *tx)
        .await?;

        tx.commit().await?;
        info!(
            refund_id = %updated.id,
            bank_code = %bank_code,
            bank_account_masked = %bank_account_masked,
            "buyer submitted refund receiving bank account"
        );
        Ok(updated)
    }

    /// Settles a `pending` refund after bank transfer disbursement (PAY-B11 / UC-PAY04).
    /// Admin-only; idempotent by `idempotency_key` and refund state; posts Debit `refund_payable`
    /// / Credit `gateway_clearing` (`build_refund_settle_tx`) and emits `payment.refund_settled`.
    pub async fn settle_refund(&self, input: SettleRefundInput) -> Result<SettleRefundOutput> {
        let provider_refund_id = input.provider_refund_id.trim();
        let idempotency_key = input.idempotency_key.trim();
        let note = input.note.trim();

        if idempotency_key.is_empty() {
            return Err(PaymentError::Validation(
                "idempotency_key is required to settle refund".to_string(),
            ));
        }
        if provider_refund_id.is_empty() {
            return Err(PaymentError::Validation(
                "provider_refund_id (transfer reference) is required".to_string(),
            ));
        }

        let mut tx = self.pool.begin().await?;

        if let Some(existing) = sqlx::query_as::<_, RefundRecord>(
            "SELECT * FROM refunds WHERE settlement_idempotency_key = $1 FOR UPDATE",
        )
        .bind(idempotency_key)
        .fetch_optional(&mut *tx)
        .await?
        {
            validate_settle_refund_idempotency_match(
                &existing,
                input.refund_id,
                provider_refund_id,
            )?;
            tx.commit().await?;
            return Ok(SettleRefundOutput {
                refund: existing,
                already_settled: true,
            });
        }

        // D9-bis point 9: lock the parent payment's checkout group (`payments ORDER BY id ASC`)
        // BEFORE locking the refund row so `payment.refund_settled` is strictly serialized with
        // `payment.refunded` / `payment.succeeded` on the same `order_group_id` without AB-BA deadlock.
        let refund_payment_meta: (Uuid, String, String, String, String, String) = sqlx::query_as(
            r#"
            SELECT p.id, p.order_id, p.order_group_id, p.user_id,
                   p.sales_channel, p.storefront_shop_id
            FROM refunds r
            JOIN payments p ON p.id = r.payment_id
            WHERE r.id = $1
            "#,
        )
        .bind(input.refund_id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or_else(|| PaymentError::NotFound("refund not found".to_string()))?;

        let (
            _,
            pay_order_id,
            pay_group_id,
            payment_user_id,
            pay_sales_channel,
            pay_storefront_shop_id,
        ) = refund_payment_meta;
        let _locked_group = lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;

        let refund =
            sqlx::query_as::<_, RefundRecord>("SELECT * FROM refunds WHERE id = $1 FOR UPDATE")
                .bind(input.refund_id)
                .fetch_optional(&mut *tx)
                .await?
                .ok_or_else(|| PaymentError::NotFound("refund not found".to_string()))?;

        if refund.settlement_idempotency_key.as_deref() == Some(idempotency_key) {
            validate_settle_refund_idempotency_match(&refund, input.refund_id, provider_refund_id)?;
            tx.commit().await?;
            return Ok(SettleRefundOutput {
                refund,
                already_settled: true,
            });
        }
        if refund.status == "completed" {
            return Err(PaymentError::Conflict(format!(
                "refund {} is already settled with a different idempotency_key",
                refund.id
            )));
        }
        if !matches!(refund.status.as_str(), "pending" | "manual_review") {
            return Err(PaymentError::Conflict(format!(
                "cannot settle refund in status '{}'",
                refund.status
            )));
        }
        // F4 / p4 Item C: Reject manual bank settlement while a ZaloPay wallet refund is still
        // managed automatically (only allow manual settlement after permanent failure into
        // `manual_review` with `provider_status = 'failed'`).
        if refund
            .provider_m_refund_id
            .as_deref()
            .is_some_and(|s| !s.trim().is_empty())
            && !(refund.status == "manual_review"
                && refund.provider_status.as_deref() == Some(REFUND_PROVIDER_STATUS_FAILED))
        {
            return Err(PaymentError::Conflict(format!(
                "ZALOPAY_REFUND_AUTO_MANAGED: refund {} is being processed automatically by ZaloPay (status='{}', provider_status={:?}); manual settlement is only allowed after failure in manual_review",
                refund.id, refund.status, refund.provider_status
            )));
        }

        let has_bank_account = refund
            .bank_account_number
            .as_deref()
            .is_some_and(|s| !s.trim().is_empty());
        if !has_bank_account && note.is_empty() {
            return Err(PaymentError::Conflict(
                "buyer receiving bank account or manual transfer note is required before settling refund"
                    .to_string(),
            ));
        }

        let effective_settlement_note = format_settlement_note_with_masked_account(
            note,
            refund.bank_code.as_deref(),
            refund.bank_account_masked.as_deref(),
        );

        let update_res = sqlx::query_as::<_, RefundRecord>(
            r#"
            UPDATE refunds
            SET status = 'completed',
                provider_refund_id = $1,
                settlement_note = $2,
                settlement_idempotency_key = $3,
                settled_at = NOW(),
                updated_at = NOW()
            WHERE id = $4
            RETURNING *
            "#,
        )
        .bind(provider_refund_id)
        .bind(effective_settlement_note.as_deref())
        .bind(idempotency_key)
        .bind(refund.id)
        .fetch_one(&mut *tx)
        .await;

        let updated = match update_res {
            Ok(r) => r,
            Err(err) if is_unique_violation(&err, "settlement_idempotency_key") => {
                let _ = tx.rollback().await;
                if let Some(existing) = sqlx::query_as::<_, RefundRecord>(
                    "SELECT * FROM refunds WHERE settlement_idempotency_key = $1",
                )
                .bind(idempotency_key)
                .fetch_optional(&self.pool)
                .await?
                {
                    validate_settle_refund_idempotency_match(
                        &existing,
                        input.refund_id,
                        provider_refund_id,
                    )?;
                    return Ok(SettleRefundOutput {
                        refund: existing,
                        already_settled: true,
                    });
                }
                return Err(PaymentError::Database(err));
            }
            Err(err) => return Err(PaymentError::Database(err)),
        };

        let channel = ChannelAttribution::from_db(&pay_sales_channel, &pay_storefront_shop_id);
        let draft = build_refund_settle_tx(updated.id, updated.amount_vnd, channel);
        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
            self.metrics.record_ledger_tx("refund_settle");
        }

        insert_outbox_event(
            &mut tx,
            "payment.refund_settled",
            &pay_group_id,
            &input.trace_id,
            json!({
                "refund_id": updated.id.to_string(),
                "payment_id": updated.payment_id.to_string(),
                "order_id": updated.order_id,
                "user_id": payment_user_id,
                "amount_vnd": updated.amount_vnd,
                "sales_channel": pay_sales_channel,
                "storefront_shop_id": pay_storefront_shop_id,
            }),
        )
        .await?;

        tx.commit().await?;
        info!(
            trace_id = %input.trace_id,
            refund_id = %updated.id,
            payment_id = %updated.payment_id,
            amount_vnd = updated.amount_vnd,
            provider_refund_id = %provider_refund_id,
            "refund settled and posted to double-entry ledger"
        );
        Ok(SettleRefundOutput {
            refund: updated,
            already_settled: false,
        })
    }

    /// Settles a completed order from `escrow_liability` into `seller_pending:<shop_id>`,
    /// `platform_fee_revenue`, and `shipping_fee_payable` (UC-PAY05 / UC-S17). Idempotent.
    pub async fn settle_order_to_pending(&self, order_id: &str) -> Result<usize> {
        let mut tx = self.pool.begin().await?;
        let settled = settle_order_to_pending_tx(&mut tx, order_id, None, &self.metrics).await?;
        tx.commit().await?;
        Ok(settled)
    }

    /// Releases escrow for `order_id` (optionally scoped to `shop_id`) from `seller_pending:<shop_id>`
    /// to `seller_available:<shop_id>` (early release override for admin).
    /// Review P1-4: ONLY allows allocations already settled to `pending_release` (after `order.completed`);
    /// rejects with `409 Conflict` if any active allocation is still `awaiting_collection` or `held`.
    pub async fn release_escrow(
        &self,
        order_id: &str,
        shop_id_filter: Option<&str>,
    ) -> Result<EscrowReleaseResult> {
        if order_id.trim().is_empty() {
            return Err(PaymentError::Validation("order_id is required".to_string()));
        }
        let shop_filter = shop_id_filter.map(str::trim).filter(|s| !s.is_empty());

        let mut tx = self.pool.begin().await?;

        // P2-7: Lock the checkout payment group (`payments ORDER BY id ASC`) BEFORE locking
        // `payment_shop_allocations` so `release_escrow` matches the global lock order
        // (`payments -> payment_shop_allocations -> ledger_accounts`) and cannot AB-BA deadlock
        // with multi-row `UPDATE payment_shop_allocations` in confirm/cancel/expire paths.
        let group_keys: Option<(String, String)> = sqlx::query_as(
            r#"
            SELECT p.order_id, p.order_group_id
            FROM payments p
            LEFT JOIN payment_shop_allocations a ON a.payment_id = p.id
            WHERE (a.order_id = $1 OR p.order_id = $1 OR p.order_group_id = $1)
              AND p.status NOT IN ('cancelled', 'expired', 'failed', 'superseded')
            ORDER BY p.id ASC
            LIMIT 1
            "#,
        )
        .bind(order_id)
        .fetch_optional(&mut *tx)
        .await?;
        if let Some((pay_order_id, pay_group_id)) = group_keys {
            let _locked_group =
                lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;
        }

        let active_allocs = sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT a.*
            FROM payment_shop_allocations a
            JOIN payments p ON p.id = a.payment_id
            WHERE (a.order_id = $1 OR p.order_id = $1 OR p.order_group_id = $1)
              AND ($2::TEXT IS NULL OR a.shop_id = $2)
              AND p.status NOT IN ('cancelled', 'expired', 'failed', 'superseded')
            ORDER BY a.id ASC
            FOR UPDATE OF a
            "#,
        )
        .bind(order_id)
        .bind(shop_filter)
        .fetch_all(&mut *tx)
        .await?;

        if let Some(unsettled) = active_allocs
            .iter()
            .find(|a| matches!(a.escrow_status.as_str(), "awaiting_collection" | "held"))
        {
            return Err(PaymentError::Conflict(format!(
                "cannot release escrow for order {order_id}: allocation {} is in status '{}' (order must be completed and settled to pending_release first)",
                unsettled.id, unsettled.escrow_status
            )));
        }

        let pending_allocs: Vec<ShopAllocationRecord> = active_allocs
            .iter()
            .filter(|a| a.escrow_status == "pending_release")
            .cloned()
            .collect();

        if pending_allocs.is_empty() {
            let already_released = active_allocs.iter().any(|a| a.escrow_status == "released");
            tx.commit().await?;
            return Ok(EscrowReleaseResult {
                order_id: order_id.to_string(),
                released: already_released,
                released_amount_vnd: 0,
                already_released,
            });
        }

        // F11 / p4 Item F: Never release `seller_pending -> seller_available` while any
        // `pending_approval` refund exists on a target allocation.
        for alloc in &pending_allocs {
            let (pay_order_id, alloc_count) =
                fetch_payment_order_meta_tx(&mut tx, alloc.payment_id).await?;
            if has_pending_approval_refund_for_alloc_tx(
                &mut tx,
                alloc.payment_id,
                &alloc.order_id,
                &pay_order_id,
                alloc_count == 1,
            )
            .await?
            {
                return Err(PaymentError::Conflict(format!(
                    "cannot release escrow for order {}: allocation {} has a refund pending approval",
                    alloc.order_id, alloc.id
                )));
            }
        }

        // Pre-lock each shop's ledger accounts in deterministic sorted order to prevent AB-BA deadlocks.
        let mut target_shop_ids: Vec<String> =
            pending_allocs.iter().map(|a| a.shop_id.clone()).collect();
        target_shop_ids.sort();
        target_shop_ids.dedup();
        for shop_id in &target_shop_ids {
            let _ = ledger::lock_and_get_seller_balances_tx(&mut tx, shop_id).await?;
        }

        let mut total_released_vnd = 0_i64;
        for alloc in pending_allocs {
            total_released_vnd += release_pending_allocation_tx(
                &mut tx,
                &alloc,
                self.config.seller_hold_hours,
                &self.metrics,
            )
            .await?;
        }

        tx.commit().await?;

        info!(
            order_id = %order_id,
            total_released_vnd = total_released_vnd,
            "escrow released to seller available balance"
        );

        Ok(EscrowReleaseResult {
            order_id: order_id.to_string(),
            released: true,
            released_amount_vnd: total_released_vnd,
            already_released: false,
        })
    }

    /// Background sweep releasing matured `pending_release` allocations whose
    /// `settled_at + PAYMENT_SELLER_HOLD_HOURS <= NOW()` on PostgreSQL clock (PAY-B7 / P3-2 / p4 Item F).
    /// Defers allocations with any open `pending_approval` refund until the checker decides.
    /// Safe across multiple replicas (`FOR UPDATE SKIP LOCKED`), reuses
    /// `release_pending_allocation_tx`, and preserves global lock order (`payments -> allocation -> ledger`).
    pub async fn release_matured_escrow_holds(&self) -> Result<usize> {
        let hold_hours_i32 = clamp_hold_hours(self.config.seller_hold_hours);
        let candidate_ids: Vec<Uuid> = sqlx::query_scalar(
            r#"
            SELECT a.id
            FROM payment_shop_allocations a
            JOIN payments p ON p.id = a.payment_id
            WHERE a.escrow_status = 'pending_release'
              AND a.settled_at IS NOT NULL
              AND a.settled_at <= NOW() - make_interval(hours => $1)
              AND (p.status IN ('succeeded', 'refunded') OR (p.method = 'COD' AND p.status = 'pending'))
              AND NOT EXISTS (
                  SELECT 1
                  FROM refunds r
                  WHERE r.payment_id = a.payment_id
                    AND (
                        r.order_id = a.order_id
                        OR (
                            (SELECT COUNT(*) FROM payment_shop_allocations a2 WHERE a2.payment_id = a.payment_id) = 1
                            AND r.order_id = p.order_id
                        )
                    )
                    AND r.status = 'pending_approval'
                    AND r.affects_escrow
                    AND NOT r.is_late_payment
              )
            ORDER BY a.settled_at ASC, a.id ASC
            LIMIT 50
            "#,
        )
        .bind(hold_hours_i32)
        .fetch_all(&self.pool)
        .await?;

        if candidate_ids.is_empty() {
            return Ok(0);
        }

        let mut released_count = 0usize;
        for alloc_id in candidate_ids {
            match self
                .release_single_matured_allocation(alloc_id, hold_hours_i32)
                .await
            {
                Ok(true) => released_count += 1,
                Ok(false) => {}
                Err(e) => {
                    warn!(
                        allocation_id = %alloc_id,
                        error = %e,
                        "failed releasing matured escrow hold allocation; will retry next tick"
                    );
                }
            }
        }

        if released_count > 0 {
            info!(
                released_count = released_count,
                hold_hours = self.config.seller_hold_hours,
                "released matured seller escrow holds"
            );
        }
        Ok(released_count)
    }

    async fn release_single_matured_allocation(
        &self,
        alloc_id: Uuid,
        hold_hours_i32: i32,
    ) -> Result<bool> {
        let mut tx = self.pool.begin().await?;
        // Lock the parent payment row first (`SKIP LOCKED`) to match global lock ordering
        // (`payments -> payment_shop_allocations -> ledger_accounts`) and serialize cleanly
        // with concurrent `create_refund` creating a `pending_approval` row (p4 Item F).
        let locked_pay: Option<(Uuid, String)> = sqlx::query_as(
            r#"
            SELECT p.id, p.order_id
            FROM payments p
            JOIN payment_shop_allocations a ON a.payment_id = p.id
            WHERE a.id = $1
            FOR UPDATE OF p SKIP LOCKED
            "#,
        )
        .bind(alloc_id)
        .fetch_optional(&mut *tx)
        .await?;
        let Some((payment_id, payment_order_id)) = locked_pay else {
            tx.commit().await?;
            return Ok(false);
        };

        let alloc_opt = sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT a.*
            FROM payment_shop_allocations a
            JOIN payments p ON p.id = a.payment_id
            WHERE a.id = $1
              AND a.escrow_status = 'pending_release'
              AND a.settled_at IS NOT NULL
              AND a.settled_at <= NOW() - make_interval(hours => $2)
              AND (p.status IN ('succeeded', 'refunded') OR (p.method = 'COD' AND p.status = 'pending'))
            FOR UPDATE OF a SKIP LOCKED
            "#,
        )
        .bind(alloc_id)
        .bind(hold_hours_i32)
        .fetch_optional(&mut *tx)
        .await?;

        let Some(alloc) = alloc_opt else {
            tx.commit().await?;
            return Ok(false);
        };

        let alloc_count: i64 = sqlx::query_scalar(
            "SELECT COUNT(*)::BIGINT FROM payment_shop_allocations WHERE payment_id = $1",
        )
        .bind(payment_id)
        .fetch_one(&mut *tx)
        .await?;
        if has_pending_approval_refund_for_alloc_tx(
            &mut tx,
            payment_id,
            &alloc.order_id,
            &payment_order_id,
            alloc_count == 1,
        )
        .await?
        {
            tx.commit().await?;
            return Ok(false);
        }

        let released_vnd = release_pending_allocation_tx(
            &mut tx,
            &alloc,
            self.config.seller_hold_hours,
            &self.metrics,
        )
        .await?;
        tx.commit().await?;
        info!(
            allocation_id = %alloc.id,
            order_id = %alloc.order_id,
            shop_id = %alloc.shop_id,
            released_vnd = released_vnd,
            "matured escrow hold released to seller available balance"
        );
        Ok(true)
    }

    /// Creates a seller payout from `seller_available:<shop_id>` (UC-PAY05).
    /// Rejects if requested amount exceeds available ledger balance.
    pub async fn request_seller_payout(
        &self,
        shop_id: &str,
        amount_vnd: i64,
        bank_account: &str,
        idempotency_key: &str,
    ) -> Result<SellerPayoutRecord> {
        if shop_id.trim().is_empty() || idempotency_key.trim().is_empty() {
            return Err(PaymentError::Validation(
                "shop_id and idempotency_key are required".to_string(),
            ));
        }
        if amount_vnd <= 0 {
            return Err(PaymentError::Validation(
                "payout amount_vnd must be positive".to_string(),
            ));
        }

        let mut tx = self.pool.begin().await?;

        if let Some(existing) = sqlx::query_as::<_, SellerPayoutRecord>(
            "SELECT * FROM seller_payouts WHERE idempotency_key = $1",
        )
        .bind(idempotency_key)
        .fetch_optional(&mut *tx)
        .await?
        {
            tx.commit().await?;
            return Ok(existing);
        }

        let available_vnd = ledger::get_seller_available_balance_tx(&mut tx, shop_id).await?;
        if amount_vnd > available_vnd {
            return Err(PaymentError::InsufficientBalance {
                available_vnd,
                requested_vnd: amount_vnd,
            });
        }

        let masked = mask_bank_account(bank_account);
        let payout = sqlx::query_as::<_, SellerPayoutRecord>(
            r#"
            INSERT INTO seller_payouts (shop_id, amount_vnd, bank_account_masked, status, idempotency_key, completed_at)
            VALUES ($1, $2, $3, 'completed', $4, NOW())
            RETURNING *
            "#,
        )
        .bind(shop_id)
        .bind(amount_vnd)
        .bind(&masked)
        .bind(idempotency_key)
        .fetch_one(&mut *tx)
        .await?;

        let draft = build_seller_payout_tx(
            payout.id,
            shop_id,
            amount_vnd,
            ChannelAttribution::marketplace(),
        )?;
        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
            self.metrics.record_ledger_tx("payout");
        }

        tx.commit().await?;
        Ok(payout)
    }

    pub async fn get_ledger_summary(
        &self,
        channel_filter: Option<SalesChannel>,
    ) -> Result<LedgerSummary> {
        ledger::get_ledger_summary(&self.pool, channel_filter).await
    }

    pub async fn get_seller_balance(&self, shop_id: &str) -> Result<SellerBalance> {
        if shop_id.trim().is_empty() {
            return Err(PaymentError::Validation("shop_id is required".to_string()));
        }
        ledger::get_seller_balance(&self.pool, shop_id).await
    }

    /// Queries effective payment fee configurations at `at_time` (defaults to now), optionally
    /// including full append-only history & scheduled future entries (UC-A09).
    pub async fn get_fee_config(&self, filter: GetFeeConfigFilter) -> Result<GetFeeConfigResult> {
        let at_time = filter.at_time.unwrap_or_else(Utc::now);
        let method_str: Option<&str> = filter.method.map(PaymentMethod::as_str);

        let active_configs = if let Some(method) = method_str {
            sqlx::query_as::<_, PaymentFeeConfigRecord>(FEE_CONFIG_ACTIVE_BY_METHOD_SQL)
                .bind(method)
                .bind(at_time)
                .fetch_all(&self.pool)
                .await?
        } else {
            sqlx::query_as::<_, PaymentFeeConfigRecord>(FEE_CONFIG_ACTIVE_ALL_SQL)
                .bind(at_time)
                .fetch_all(&self.pool)
                .await?
        };

        let history = if filter.include_history {
            if let Some(method) = method_str {
                sqlx::query_as::<_, PaymentFeeConfigRecord>(FEE_CONFIG_HISTORY_BY_METHOD_SQL)
                    .bind(method)
                    .fetch_all(&self.pool)
                    .await?
            } else {
                sqlx::query_as::<_, PaymentFeeConfigRecord>(FEE_CONFIG_HISTORY_ALL_SQL)
                    .fetch_all(&self.pool)
                    .await?
            }
        } else {
            Vec::new()
        };

        Ok(GetFeeConfigResult {
            active_configs,
            history,
        })
    }

    /// Appends a new immutable payment fee configuration entry (UC-A09).
    /// `effective_from` defaults to now when omitted; past timestamps and bps outside 0..=2000 are rejected.
    pub async fn set_fee_config(&self, input: SetFeeConfigInput) -> Result<PaymentFeeConfigRecord> {
        let now = Utc::now();
        let (effective_from, note) =
            validate_set_fee_config(input.fee_bps, input.effective_from, &input.note, now)?;
        let created_by = input.created_by.trim();

        let rec = sqlx::query_as::<_, PaymentFeeConfigRecord>(
            r#"
            INSERT INTO payment_fee_configs (payment_method, fee_bps, effective_from, created_by, note)
            VALUES ($1, $2, $3, $4, $5)
            RETURNING id, seq, payment_method, fee_bps, effective_from, created_by, note, created_at
            "#,
        )
        .bind(input.method.as_str())
        .bind(input.fee_bps)
        .bind(effective_from)
        .bind(created_by)
        .bind(&note)
        .fetch_one(&self.pool)
        .await?;

        info!(
            fee_config_id = %rec.id,
            payment_method = %rec.payment_method,
            fee_bps = rec.fee_bps,
            effective_from = %rec.effective_from,
            created_by = %rec.created_by,
            "payment fee config scheduled"
        );

        Ok(rec)
    }

    /// Lists paginated double-entry ledger entries on `seller_pending:<shop>` and
    /// `seller_available:<shop>` accounts with Vietnamese descriptions (UC-S17).
    pub async fn list_seller_ledger(
        &self,
        filter: ListSellerLedgerFilter,
    ) -> Result<ListSellerLedgerResult> {
        let page = filter.page.max(1);
        let page_size = if filter.page_size <= 0 {
            20
        } else {
            filter.page_size.min(100)
        };
        let offset = i64::from(page - 1) * i64::from(page_size);

        let ref_type_filter = filter
            .reference_type
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty());

        let shop_filter: Option<&str> = match filter.shop_id.as_deref() {
            Some(raw) => {
                let sid = raw.trim();
                if sid.is_empty() {
                    return Err(PaymentError::Validation("shop_id is required".to_string()));
                }
                Some(sid)
            }
            None => None,
        };
        let acct_mode = match filter.account_filter {
            None => "all",
            Some(SellerLedgerAccountFilter::Pending) => "pending",
            Some(SellerLedgerAccountFilter::Available) => "available",
        };

        let total: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM ledger_accounts a
            JOIN ledger_entries e ON e.account_id = a.id
            JOIN ledger_transactions lt ON lt.id = e.transaction_id
            WHERE a.shop_id IS NOT NULL
              AND ($1::TEXT IS NULL OR a.shop_id = $1)
              AND (
                  ($2 = 'all' AND (a.id = 'seller_pending:' || a.shop_id OR a.id = 'seller_available:' || a.shop_id))
                  OR ($2 = 'pending' AND a.id = 'seller_pending:' || a.shop_id)
                  OR ($2 = 'available' AND a.id = 'seller_available:' || a.shop_id)
              )
              AND ($3::TEXT IS NULL OR lt.reference_type = $3)
            "#,
        )
        .bind(shop_filter)
        .bind(acct_mode)
        .bind(ref_type_filter)
        .fetch_one(&self.pool)
        .await?;

        let rows = sqlx::query(
            r#"
            WITH page_entries AS (
                SELECT
                    e.id,
                    e.transaction_id,
                    e.account_id,
                    lt.reference_type,
                    lt.reference_id,
                    e.debit_vnd,
                    e.credit_vnd,
                    e.created_at
                FROM ledger_accounts a
                JOIN ledger_entries e ON e.account_id = a.id
                JOIN ledger_transactions lt ON lt.id = e.transaction_id
                WHERE a.shop_id IS NOT NULL
                  AND ($1::TEXT IS NULL OR a.shop_id = $1)
                  AND (
                      ($2 = 'all' AND (a.id = 'seller_pending:' || a.shop_id OR a.id = 'seller_available:' || a.shop_id))
                      OR ($2 = 'pending' AND a.id = 'seller_pending:' || a.shop_id)
                      OR ($2 = 'available' AND a.id = 'seller_available:' || a.shop_id)
                  )
                  AND ($3::TEXT IS NULL OR lt.reference_type = $3)
                ORDER BY e.created_at DESC, e.id DESC
                LIMIT $4 OFFSET $5
            )
            SELECT
                pe.id,
                pe.transaction_id,
                pe.account_id,
                pe.reference_type,
                pe.reference_id,
                pe.debit_vnd,
                pe.credit_vnd,
                pe.created_at,
                COALESCE((
                    SELECT SUM(fe.credit_vnd)::BIGINT
                    FROM ledger_entries fe
                    WHERE fe.transaction_id = pe.transaction_id
                      AND fe.account_id = 'platform_fee_revenue'
                ), 0)::BIGINT AS platform_fee_vnd
            FROM page_entries pe
            ORDER BY pe.created_at DESC, pe.id DESC
            "#,
        )
        .bind(shop_filter)
        .bind(acct_mode)
        .bind(ref_type_filter)
        .bind(i64::from(page_size))
        .bind(offset)
        .fetch_all(&self.pool)
        .await?;

        let mut entries = Vec::with_capacity(rows.len());
        for row in rows {
            let id: Uuid = row.try_get("id")?;
            let transaction_id: Uuid = row.try_get("transaction_id")?;
            let account_code: String = row.try_get("account_id")?;
            let reference_type: String = row.try_get("reference_type")?;
            let reference_id: String = row.try_get("reference_id")?;
            let debit_vnd: i64 = row.try_get("debit_vnd")?;
            let credit_vnd: i64 = row.try_get("credit_vnd")?;
            let created_at: DateTime<Utc> = row.try_get("created_at")?;
            let platform_fee_vnd: i64 = row.try_get("platform_fee_vnd")?;
            let description = format_seller_ledger_description(
                &reference_type,
                &reference_id,
                &account_code,
                platform_fee_vnd,
            );
            entries.push(SellerLedgerEntryRecord {
                id,
                transaction_id,
                account_code,
                reference_type,
                reference_id,
                description,
                debit_vnd,
                credit_vnd,
                delta_vnd: credit_vnd - debit_vnd,
                created_at,
            });
        }

        Ok(ListSellerLedgerResult { entries, total })
    }

    pub async fn list_reconciliation_queue(
        &self,
        status_filter: Option<&str>,
        page: i32,
        page_size: i32,
    ) -> Result<ListReconciliationQueueResult> {
        let normalized_status = match status_filter.map(str::trim).filter(|s| !s.is_empty()) {
            None => None,
            Some(raw) => {
                let lower = raw.to_ascii_lowercase();
                if !matches!(
                    lower.as_str(),
                    "pending" | "assigned" | "refunded" | "suspended"
                ) {
                    return Err(PaymentError::Validation(format!(
                        "invalid reconciliation queue status '{raw}'; expected pending|assigned|refunded|suspended"
                    )));
                }
                Some(lower)
            }
        };

        let page = page.max(1);
        let page_size = if page_size <= 0 {
            20
        } else {
            page_size.min(100)
        };
        let offset = i64::from(page - 1) * i64::from(page_size);

        let total: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM reconciliation_queue
            WHERE ($1::TEXT IS NULL OR resolution_status = $1)
            "#,
        )
        .bind(normalized_status.as_deref())
        .fetch_one(&self.pool)
        .await?;

        let items = sqlx::query_as::<_, ReconciliationQueueItem>(
            r#"
            SELECT *
            FROM reconciliation_queue
            WHERE ($1::TEXT IS NULL OR resolution_status = $1)
            ORDER BY created_at DESC, id DESC
            LIMIT $2 OFFSET $3
            "#,
        )
        .bind(normalized_status.as_deref())
        .bind(i64::from(page_size))
        .bind(offset)
        .fetch_all(&self.pool)
        .await?;

        Ok(ListReconciliationQueueResult { items, total })
    }

    /// Resolves a mismatched transaction in `reconciliation_queue` (PAY-B9 / UC-A14):
    /// - `ASSIGN`: validates payment group (same rules/lock order as webhook), rejects underpayment
    ///   or cancelled/already-paid group with 409, confirms exact amount, or confirms + creates a
    ///   pending excess refund (`Debit gateway_clearing / Credit refund_payable`) on overpayment.
    /// - `REFUND`: requires target `payment_id` (explicit or `expected_payment_id`), posts
    ///   `Debit gateway_clearing / Credit refund_payable` and creates a `pending` refund.
    /// - `SUSPEND`: marks item `suspended` with required note; may be resolved again later.
    ///
    /// Idempotent by `idempotency_key`; finalized (`assigned`/`refunded`) items reject new attempts with 409.
    pub async fn resolve_reconciliation_item(
        &self,
        input: ResolveReconciliationInput,
    ) -> Result<ResolveReconciliationOutput> {
        let idempotency_key = input.idempotency_key.trim();
        let note = input.note.trim();

        if idempotency_key.is_empty() {
            return Err(PaymentError::Validation(
                "idempotency_key is required to resolve reconciliation item".to_string(),
            ));
        }
        if input.action == ReconciliationActionKind::Suspend && note.is_empty() {
            return Err(PaymentError::Validation(
                "note is required when suspending a reconciliation item".to_string(),
            ));
        }

        let mut tx = self.pool.begin().await?;

        // Read-only idempotency lookup BEFORE acquiring advisory/row locks so already-committed
        // replays return immediately without blocking or creating lock-order inversions.
        if let Some(existing) = sqlx::query_as::<_, ReconciliationQueueItem>(
            "SELECT * FROM reconciliation_queue WHERE resolution_idempotency_key = $1",
        )
        .bind(idempotency_key)
        .fetch_optional(&mut *tx)
        .await?
        {
            validate_resolve_reconciliation_idempotency_match(
                &existing,
                input.item_id,
                input.action,
                input.payment_id,
            )?;
            let refund = match existing.resolution_refund_id {
                Some(rid) => {
                    sqlx::query_as::<_, RefundRecord>("SELECT * FROM refunds WHERE id = $1")
                        .bind(rid)
                        .fetch_optional(&mut *tx)
                        .await?
                }
                None => None,
            };
            tx.commit().await?;
            return Ok(ResolveReconciliationOutput {
                item: existing,
                refund,
                already_resolved: true,
            });
        }

        // Locate provider_tx_id read-only first, then acquire the transaction-scoped advisory
        // lock per provider_tx_id BEFORE locking reconciliation_queue or payments rows (P0-1).
        let target_provider_tx_id: String =
            sqlx::query_scalar("SELECT provider_tx_id FROM reconciliation_queue WHERE id = $1")
                .bind(input.item_id)
                .fetch_optional(&mut *tx)
                .await?
                .ok_or_else(|| {
                    PaymentError::NotFound("reconciliation queue item not found".to_string())
                })?;

        lock_provider_tx_xact(&mut tx, &target_provider_tx_id).await?;

        let locked_siblings = sqlx::query_as::<_, ReconciliationQueueItem>(
            r#"
            SELECT * FROM reconciliation_queue
            WHERE provider_tx_id = $1
            ORDER BY id ASC
            FOR UPDATE
            "#,
        )
        .bind(&target_provider_tx_id)
        .fetch_all(&mut *tx)
        .await?;

        let item = locked_siblings
            .iter()
            .find(|row| row.id == input.item_id)
            .cloned()
            .ok_or_else(|| {
                PaymentError::NotFound("reconciliation queue item not found".to_string())
            })?;

        // Handles concurrent identical retry that raced past the initial idempotency lookup
        // before the winning transaction committed.
        if item.resolution_idempotency_key.as_deref() == Some(idempotency_key) {
            validate_resolve_reconciliation_idempotency_match(
                &item,
                input.item_id,
                input.action,
                input.payment_id,
            )?;
            let refund = match item.resolution_refund_id {
                Some(rid) => {
                    sqlx::query_as::<_, RefundRecord>("SELECT * FROM refunds WHERE id = $1")
                        .bind(rid)
                        .fetch_optional(&mut *tx)
                        .await?
                }
                None => None,
            };
            tx.commit().await?;
            return Ok(ResolveReconciliationOutput {
                item,
                refund,
                already_resolved: true,
            });
        }

        if !matches!(item.resolution_status.as_str(), "pending" | "suspended") {
            return Err(PaymentError::Conflict(format!(
                "reconciliation item {} is already finalized with status '{}'",
                item.id, item.resolution_status
            )));
        }

        let acct = check_provider_tx_accounting_tx(&mut tx, &item.provider_tx_id).await?;
        validate_reconciliation_provider_tx_accountable(&item.provider_tx_id, acct)?;

        match input.action {
            ReconciliationActionKind::Assign | ReconciliationActionKind::Refund
                if item.source_type == "cod_file" =>
            {
                Err(PaymentError::Conflict(
                    "COD file reconciliation items cannot be resolved via VietQR ASSIGN/REFUND; re-import carrier file after fixing or use SUSPEND".to_string(),
                ))
            }
            ReconciliationActionKind::Suspend => {
                // P1-5: Validate optional payment_id against payments before writing FK column.
                if let Some(pid) = input.payment_id {
                    let exists: bool =
                        sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM payments WHERE id = $1)")
                            .bind(pid)
                            .fetch_one(&mut *tx)
                            .await?;
                    if !exists {
                        return Err(PaymentError::NotFound(
                            "target payment not found".to_string(),
                        ));
                    }
                }

                let updated = sqlx::query_as::<_, ReconciliationQueueItem>(
                    r#"
                    UPDATE reconciliation_queue
                    SET resolution_status = 'suspended',
                        notes = $1,
                        resolution_idempotency_key = $2,
                        resolution_action = $3,
                        resolution_payment_id = $4
                    WHERE id = $5
                    RETURNING *
                    "#,
                )
                .bind(note)
                .bind(idempotency_key)
                .bind(input.action.as_str())
                .bind(input.payment_id)
                .bind(item.id)
                .fetch_one(&mut *tx)
                .await
                .map_err(map_recon_idempotency_conflict)?;

                tx.commit().await?;
                info!(
                    trace_id = %input.trace_id,
                    item_id = %updated.id,
                    "reconciliation queue item suspended"
                );
                Ok(ResolveReconciliationOutput {
                    item: updated,
                    refund: None,
                    already_resolved: false,
                })
            }

            ReconciliationActionKind::Assign => {
                let target_payment_id =
                    input
                        .payment_id
                        .or(item.expected_payment_id)
                        .ok_or_else(|| {
                            PaymentError::Validation(
                                "payment_id is required for ASSIGN".to_string(),
                            )
                        })?;

                let found =
                    sqlx::query_as::<_, PaymentRecord>("SELECT * FROM payments WHERE id = $1")
                        .bind(target_payment_id)
                        .fetch_optional(&mut *tx)
                        .await?
                        .ok_or_else(|| {
                            PaymentError::NotFound("target payment not found".to_string())
                        })?;

                // Lock entire checkout group in ascending id order (identical to webhook confirmation path).
                let group =
                    lock_payment_group_tx(&mut tx, &found.order_id, &found.order_group_id).await?;
                let locked = group
                    .iter()
                    .find(|p| p.id == found.id)
                    .cloned()
                    .unwrap_or(found);

                if !is_online_method(&locked.method) {
                    return Err(PaymentError::Conflict(
                        "can only assign mismatched transfer to an online payment (VIETQR or ZALOPAY)".to_string(),
                    ));
                }
                // F2 / p4 Item H: Ensure reconciliation item's provider matches target payment's method.
                if !reconciliation_provider_matches_method(&item.provider, &locked.method) {
                    return Err(PaymentError::Conflict(format!(
                        "cannot assign '{}' transfer to payment {} with method '{}'",
                        item.provider, locked.id, locked.method
                    )));
                }
                let order_already_succeeded = group.iter().any(|p| {
                    p.id != locked.id && matches!(p.status.as_str(), "succeeded" | "refunded")
                });
                let group_has_terminal_unpaid = group
                    .iter()
                    .any(|p| matches!(p.status.as_str(), "cancelled" | "expired" | "failed"));
                let has_cancelled_alloc = has_cancelled_allocation_tx(&mut tx, locked.id).await?;

                if locked.status != "pending"
                    || order_already_succeeded
                    || group_has_terminal_unpaid
                    || has_cancelled_alloc
                {
                    return Err(PaymentError::Conflict(format!(
                        "cannot assign transfer to payment {} (status='{}', group_cancelled={}, already_succeeded={})",
                        locked.id,
                        locked.status,
                        group_has_terminal_unpaid || has_cancelled_alloc,
                        order_already_succeeded
                    )));
                }

                let split =
                    compute_reconciliation_assign_split(locked.amount_vnd, item.actual_amount_vnd)?;

                // P1-3: Preserve actual transfer timestamp (item.occurred_at -> webhook_events -> item.created_at)
                // so assigning a prior-day transfer on the next day does not trigger false missing_provider.
                let transfer_paid_at = match item.occurred_at {
                    Some(ts) => Some(ts),
                    None => sqlx::query(
                        r#"
                        SELECT raw_payload, received_at
                        FROM webhook_events
                        WHERE ($1::UUID IS NOT NULL AND id = $1)
                           OR provider_tx_id = $2
                        ORDER BY CASE WHEN id = $1 THEN 0 ELSE 1 END
                        LIMIT 1
                        "#,
                    )
                    .bind(item.webhook_event_id)
                    .bind(&item.provider_tx_id)
                    .fetch_optional(&mut *tx)
                    .await?
                    .map(|row| {
                        let raw_payload: serde_json::Value =
                            row.try_get("raw_payload").unwrap_or_default();
                        let received_at: DateTime<Utc> =
                            row.try_get("received_at").unwrap_or(item.created_at);
                        extract_occurred_at_from_json(&raw_payload).unwrap_or(received_at)
                    })
                    .or(Some(item.created_at)),
                };

                let confirmed = confirm_online_payment_tx(
                    &mut tx,
                    &locked,
                    &item.provider_tx_id,
                    transfer_paid_at,
                    None,
                    &input.trace_id,
                    &self.metrics,
                )
                .await?;

                let excess_refund = match split {
                    AssignAmountSplit::Exact { .. } => None,
                    AssignAmountSplit::Overpaid { excess_vnd, .. } => {
                        let rf_key = reconciliation_overpay_key(&item.provider_tx_id);
                        let rf_reason = if note.is_empty() {
                            "reconciliation_overpayment".to_string()
                        } else {
                            format!("reconciliation_overpayment: {note}")
                        };
                        let channel = ChannelAttribution::from_db(
                            &locked.sales_channel,
                            &locked.storefront_shop_id,
                        );
                        let draft = build_reconciliation_refund_tx(
                            &rf_key,
                            &item.provider_tx_id,
                            excess_vnd,
                            &format!(
                                "Overpayment excess {excess_vnd} VND for provider_tx_id {}",
                                item.provider_tx_id
                            ),
                            channel,
                        );
                        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
                            self.metrics.record_ledger_tx("reconciliation_refund");
                        }

                        let rf = sqlx::query_as::<_, RefundRecord>(
                            r#"
                            INSERT INTO refunds (
                                payment_id, order_id, amount_vnd, reason, status, idempotency_key,
                                is_late_payment, affects_escrow
                            )
                            VALUES ($1, $2, $3, $4, 'pending', $5, FALSE, FALSE)
                            ON CONFLICT (idempotency_key) DO UPDATE SET updated_at = NOW()
                            RETURNING *
                            "#,
                        )
                        .bind(locked.id)
                        .bind(&locked.order_id)
                        .bind(excess_vnd)
                        .bind(&rf_reason)
                        .bind(&rf_key)
                        .fetch_one(&mut *tx)
                        .await?;
                        Some(rf)
                    }
                };

                let updated_item = sqlx::query_as::<_, ReconciliationQueueItem>(
                    r#"
                    UPDATE reconciliation_queue
                    SET resolution_status = 'assigned',
                        expected_payment_id = $1,
                        expected_amount_vnd = $2,
                        notes = COALESCE(NULLIF($3, ''), notes),
                        resolution_idempotency_key = $4,
                        resolution_refund_id = $5,
                        resolution_action = $6,
                        resolution_payment_id = $7,
                        resolved_at = NOW()
                    WHERE id = $8
                    RETURNING *
                    "#,
                )
                .bind(confirmed.id)
                .bind(confirmed.amount_vnd)
                .bind(note)
                .bind(idempotency_key)
                .bind(excess_refund.as_ref().map(|r| r.id))
                .bind(input.action.as_str())
                .bind(input.payment_id)
                .bind(item.id)
                .fetch_one(&mut *tx)
                .await
                .map_err(map_recon_idempotency_conflict)?;

                tx.commit().await?;
                self.emit_sse(&confirmed);
                info!(
                    trace_id = %input.trace_id,
                    item_id = %updated_item.id,
                    payment_id = %confirmed.id,
                    excess_refund_id = ?excess_refund.as_ref().map(|r| r.id),
                    "reconciliation queue item resolved via ASSIGN"
                );
                Ok(ResolveReconciliationOutput {
                    item: updated_item,
                    refund: excess_refund,
                    already_resolved: false,
                })
            }

            ReconciliationActionKind::Refund => {
                let target_payment_id =
                    input
                        .payment_id
                        .or(item.expected_payment_id)
                        .ok_or_else(|| {
                            PaymentError::Validation(
                                "payment_id is required for REFUND".to_string(),
                            )
                        })?;
                if item.actual_amount_vnd <= 0 {
                    return Err(PaymentError::Conflict(
                        "cannot refund reconciliation item with non-positive actual_amount_vnd"
                            .to_string(),
                    ));
                }

                let target_payment =
                    sqlx::query_as::<_, PaymentRecord>("SELECT * FROM payments WHERE id = $1")
                        .bind(target_payment_id)
                        .fetch_optional(&mut *tx)
                        .await?
                        .ok_or_else(|| {
                            PaymentError::NotFound("target payment not found".to_string())
                        })?;

                let rf_key = reconciliation_refund_key(&item.provider_tx_id);
                let rf_reason = if note.is_empty() {
                    "reconciliation_refund".to_string()
                } else {
                    note.to_string()
                };
                let channel = ChannelAttribution::from_db(
                    &target_payment.sales_channel,
                    &target_payment.storefront_shop_id,
                );
                let draft = build_reconciliation_refund_tx(
                    &rf_key,
                    &item.provider_tx_id,
                    item.actual_amount_vnd,
                    &format!(
                        "Mismatched transfer {} booked to refund_payable via reconciliation item {}",
                        item.provider_tx_id, item.id
                    ),
                    channel,
                );
                if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
                    self.metrics.record_ledger_tx("reconciliation_refund");
                }

                let refund = sqlx::query_as::<_, RefundRecord>(
                    r#"
                    INSERT INTO refunds (
                        payment_id, order_id, amount_vnd, reason, status, idempotency_key,
                        is_late_payment, affects_escrow
                    )
                    VALUES ($1, $2, $3, $4, 'pending', $5, FALSE, FALSE)
                    ON CONFLICT (idempotency_key) DO UPDATE SET updated_at = NOW()
                    RETURNING *
                    "#,
                )
                .bind(target_payment.id)
                .bind(&target_payment.order_id)
                .bind(item.actual_amount_vnd)
                .bind(&rf_reason)
                .bind(&rf_key)
                .fetch_one(&mut *tx)
                .await?;

                let updated_item = sqlx::query_as::<_, ReconciliationQueueItem>(
                    r#"
                    UPDATE reconciliation_queue
                    SET resolution_status = 'refunded',
                        expected_payment_id = $1,
                        notes = COALESCE(NULLIF($2, ''), notes),
                        resolution_idempotency_key = $3,
                        resolution_refund_id = $4,
                        resolution_action = $5,
                        resolution_payment_id = $6,
                        resolved_at = NOW()
                    WHERE id = $7
                    RETURNING *
                    "#,
                )
                .bind(target_payment.id)
                .bind(note)
                .bind(idempotency_key)
                .bind(refund.id)
                .bind(input.action.as_str())
                .bind(input.payment_id)
                .bind(item.id)
                .fetch_one(&mut *tx)
                .await
                .map_err(map_recon_idempotency_conflict)?;

                tx.commit().await?;
                info!(
                    trace_id = %input.trace_id,
                    item_id = %updated_item.id,
                    payment_id = %target_payment.id,
                    refund_id = %refund.id,
                    amount_vnd = refund.amount_vnd,
                    "reconciliation queue item resolved via REFUND"
                );
                Ok(ResolveReconciliationOutput {
                    item: updated_item,
                    refund: Some(refund),
                    already_resolved: false,
                })
            }
        }
    }

    /// Background scanner for expired pending online payments (VietQR & ZaloPay, UC-PAY06 / F3).
    /// Queries provider outside any DB transaction and processes each candidate in its own
    /// short group-locked transaction (`ORDER BY id ASC FOR UPDATE`) so one conflict never
    /// rolls back the batch (review-B P1-4 / P1-5).
    ///
    /// Per F3: for `ZALOPAY`, calls `/v2/query` before cancelling (`return_code == 1` confirms
    /// instead of expiring; `2`, `3`, or network error expires normally so late money goes through
    /// the existing `late_payment` refund path).
    pub async fn scan_expired_payments(&self) -> Result<usize> {
        let expired_candidates = sqlx::query_as::<_, PaymentRecord>(
            r#"
            SELECT * FROM payments
            WHERE status = 'pending'
              AND method IN ('VIETQR', 'ZALOPAY')
              AND expires_at IS NOT NULL
              AND expires_at <= NOW()
            ORDER BY expires_at ASC
            LIMIT 50
            "#,
        )
        .fetch_all(&self.pool)
        .await?;

        let mut expired_count = 0usize;

        if !expired_candidates.is_empty() {
            // P1-2: Fetch VietQR (SePay/Mock) batch feed ONCE per tick only when VietQR candidates
            // exist. If VietQR feed fails, defer only VietQR candidates without blocking ZaloPay (F3).
            let has_vietqr_candidates = expired_candidates.iter().any(|c| c.method == "VIETQR");
            let mut vietqr_feed_failed = false;
            let batch_feed_by_code: Option<HashMap<String, Vec<ProviderTransaction>>> =
                if has_vietqr_candidates {
                    match self.vietqr.list_recent_transactions_for_expiry().await {
                        Ok(Some(list)) => {
                            let mut by_code: HashMap<String, Vec<ProviderTransaction>> =
                                HashMap::new();
                            for ptx in list {
                                if !ptx.provider_order_code.is_empty() {
                                    by_code
                                        .entry(ptx.provider_order_code.clone())
                                        .or_default()
                                        .push(ptx);
                                }
                            }
                            Some(by_code)
                        }
                        Ok(None) => None, // PayOS: per-payment status endpoint
                        Err(e) => {
                            vietqr_feed_failed = true;
                            warn!(
                                candidate_count = expired_candidates.len(),
                                error = %e,
                                "VietQR batch feed unreachable during expiration scan; deferring VietQR expiry for this tick"
                            );
                            None
                        }
                    }
                } else {
                    None
                };

            let zalopay_app_id = self.zalopay.effective_app_id().ok();

            for candidate in expired_candidates {
                let mut zalopay_concluded_not_paid = false;
                let mut zalopay_last_query: Option<(Option<i32>, Option<bool>)> = None;
                let matching_txs: Vec<ProviderTransaction> = if candidate.method == "ZALOPAY" {
                    // F3: Query ZaloPay `/v2/query` before expiring.
                    // `return_code == 1` -> confirm; `2` -> expire & mark `not_paid`;
                    // `3` (both `is_processing=false` and `true`) or network error -> expire &
                    // schedule bounded late-payment sweep retries (never conclude early inside window).
                    match self
                        .zalopay
                        .query_order(&candidate.provider_order_code)
                        .await
                    {
                        Ok(resp)
                            if resp.return_code == ZALOPAY_RETURN_SUCCESS
                                && resp.zp_trans_id > 0
                                && resp.amount > 0 =>
                        {
                            zalopay_last_query =
                                Some((Some(resp.return_code), Some(resp.is_processing)));
                            let occurred_at = resp
                                .normalized_server_time()
                                .ok()
                                .flatten()
                                .unwrap_or_else(Utc::now);
                            vec![ProviderTransaction {
                                provider_tx_id: resp.zp_trans_id.to_string(),
                                provider_order_code: candidate.provider_order_code.clone(),
                                amount_vnd: resp.amount,
                                status: "SUCCEEDED".to_string(),
                                occurred_at,
                            }]
                        }
                        Ok(resp) => {
                            zalopay_last_query =
                                Some((Some(resp.return_code), Some(resp.is_processing)));
                            if resp.return_code == ZALOPAY_RETURN_FAIL {
                                zalopay_concluded_not_paid = true;
                            }
                            info!(
                                payment_id = %candidate.id,
                                app_trans_id = %candidate.provider_order_code,
                                return_code = resp.return_code,
                                is_processing = resp.is_processing,
                                "ZaloPay /v2/query returned non-success at expiry; proceeding to expire"
                            );
                            Vec::new()
                        }
                        Err(e) => {
                            zalopay_last_query = Some((None, None));
                            warn!(
                                payment_id = %candidate.id,
                                app_trans_id = %candidate.provider_order_code,
                                error = %e,
                                "ZaloPay /v2/query network error at expiry; proceeding to expire per F3"
                            );
                            Vec::new()
                        }
                    }
                } else {
                    if vietqr_feed_failed {
                        continue;
                    }
                    match &batch_feed_by_code {
                        Some(by_code) => by_code
                            .get(&candidate.provider_order_code)
                            .cloned()
                            .unwrap_or_default(),
                        None => match self
                            .vietqr
                            .check_payment_status(&candidate.provider_order_code)
                            .await
                        {
                            Ok(Some(ptx)) => vec![ptx],
                            Ok(None) => Vec::new(),
                            Err(e) => {
                                warn!(
                                    payment_id = %candidate.id,
                                    error = %e,
                                    "provider unreachable during expiration check; deferring expiry"
                                );
                                continue;
                            }
                        },
                    }
                };

                // P1-2: Prioritize exact-amount transfers ahead of mismatched transfers, and skip
                // any already-accounted transfer so an earlier wrong/refunded transfer never hides
                // a valid subsequent payment for the same order code.
                let amounts: Vec<i64> = matching_txs.iter().map(|t| t.amount_vnd).collect();
                let ordered_indices =
                    order_expiry_candidate_indices(&amounts, candidate.amount_vnd);

                let mut handled = false;
                for idx in ordered_indices {
                    let provider_tx = &matching_txs[idx];
                    let mut tx = self.pool.begin().await?;
                    // P0-1: Acquire per-provider_tx_id advisory lock BEFORE payment group lock.
                    lock_provider_tx_xact(&mut tx, &provider_tx.provider_tx_id).await?;
                    let group = lock_payment_group_tx(
                        &mut tx,
                        &candidate.order_id,
                        &candidate.order_group_id,
                    )
                    .await?;
                    let Some(payment) = group.iter().find(|p| p.id == candidate.id).cloned() else {
                        tx.commit().await?;
                        handled = true;
                        break;
                    };

                    let acct =
                        check_provider_tx_accounting_tx(&mut tx, &provider_tx.provider_tx_id)
                            .await?;
                    if acct.is_accounted() {
                        info!(
                            payment_id = %payment.id,
                            provider_tx_id = %provider_tx.provider_tx_id,
                            "candidate provider_tx_id is already accounted for; checking remaining transfers"
                        );
                        tx.commit().await?;
                        if payment.status != "pending" {
                            handled = true;
                            break;
                        }
                        continue;
                    }

                    // F3 / p4 Item A: Payment left `pending` (e.g. concurrent RetryPayment or
                    // CancelPayment) while `/v2/query` or feed lookup was in flight — NEVER drop
                    // the confirmed payment evidence; route exact-amount to `record_late_payment_refund_tx`
                    // or mismatched-amount to `reconciliation_queue` strictly once.
                    if payment.status != "pending" {
                        if provider_tx.amount_vnd == payment.amount_vnd {
                            let zp_app = if payment.method == "ZALOPAY" {
                                zalopay_app_id
                            } else {
                                None
                            };
                            record_late_payment_refund_tx(
                                &mut tx,
                                &payment,
                                &provider_tx.provider_tx_id,
                                provider_tx.amount_vnd,
                                None,
                                "auto_closed:expiry_scan_raced_late_refund",
                                zp_app,
                                &self.metrics,
                            )
                            .await?;
                            tx.commit().await?;
                            warn!(
                                payment_id = %payment.id,
                                status = %payment.status,
                                provider_tx_id = %provider_tx.provider_tx_id,
                                amount_vnd = provider_tx.amount_vnd,
                                "payment left pending during expiry query; routed confirmed transfer to late_payment refund"
                            );
                            handled = true;
                            break;
                        } else {
                            enqueue_reconciliation_item_tx(
                                &mut tx,
                                None,
                                &payment.provider,
                                &provider_tx.provider_tx_id,
                                Some(payment.id),
                                Some(&payment.order_id),
                                Some(payment.amount_vnd),
                                provider_tx.amount_vnd,
                                "expired_amount_mismatch",
                                Some(provider_tx.occurred_at),
                                "reconciliation",
                                None,
                            )
                            .await?;
                            if payment.method == "ZALOPAY" {
                                sqlx::query(
                                    r#"
                                    UPDATE payments
                                    SET provider_final_status = COALESCE(provider_final_status, $1),
                                        provider_next_check_at = NULL,
                                        updated_at = NOW()
                                    WHERE id = $2
                                    "#,
                                )
                                .bind(PROVIDER_FINAL_STATUS_AMOUNT_MISMATCH)
                                .bind(payment.id)
                                .execute(&mut *tx)
                                .await?;
                            }
                            tx.commit().await?;
                            warn!(
                                payment_id = %payment.id,
                                status = %payment.status,
                                provider_tx_id = %provider_tx.provider_tx_id,
                                expected_amount_vnd = payment.amount_vnd,
                                actual_amount_vnd = provider_tx.amount_vnd,
                                "payment left pending during expiry query with mismatched transfer; queued for reconciliation"
                            );
                            handled = true;
                            continue;
                        }
                    }

                    if provider_tx.amount_vnd == payment.amount_vnd {
                        let order_already_succeeded = group.iter().any(|p| {
                            p.id != payment.id
                                && matches!(p.status.as_str(), "succeeded" | "refunded")
                        });
                        let has_cancelled_alloc =
                            has_cancelled_allocation_tx(&mut tx, payment.id).await?;
                        if order_already_succeeded || has_cancelled_alloc {
                            let next_status = if has_cancelled_alloc {
                                "cancelled"
                            } else {
                                "superseded"
                            };
                            let zp_app = if payment.method == "ZALOPAY" {
                                zalopay_app_id
                            } else {
                                None
                            };
                            record_late_payment_refund_tx(
                                &mut tx,
                                &payment,
                                &provider_tx.provider_tx_id,
                                provider_tx.amount_vnd,
                                Some(next_status),
                                "auto_closed:expiry_scan_late_refund",
                                zp_app,
                                &self.metrics,
                            )
                            .await?;
                            tx.commit().await?;
                        } else {
                            let confirmed = confirm_online_payment_tx(
                                &mut tx,
                                &payment,
                                &provider_tx.provider_tx_id,
                                Some(provider_tx.occurred_at),
                                Some("auto_closed:expiry_scan_confirm"),
                                &payment.trace_id,
                                &self.metrics,
                            )
                            .await?;
                            tx.commit().await?;
                            self.emit_sse(&confirmed);
                        }
                        handled = true;
                        break;
                    }

                    // P2-7: Unaccounted mismatched transfer — enqueue once into reconciliation_queue
                    // and continue checking (will expire below if no exact transfer matches).
                    enqueue_reconciliation_item_tx(
                        &mut tx,
                        None,
                        &payment.provider,
                        &provider_tx.provider_tx_id,
                        Some(payment.id),
                        Some(&payment.order_id),
                        Some(payment.amount_vnd),
                        provider_tx.amount_vnd,
                        "expired_amount_mismatch",
                        Some(provider_tx.occurred_at),
                        "reconciliation",
                        None,
                    )
                    .await?;
                    if payment.method == "ZALOPAY" {
                        sqlx::query(
                            r#"
                            UPDATE payments
                            SET provider_final_status = COALESCE(provider_final_status, $1),
                                provider_next_check_at = NULL,
                                updated_at = NOW()
                            WHERE id = $2
                            "#,
                        )
                        .bind(PROVIDER_FINAL_STATUS_AMOUNT_MISMATCH)
                        .bind(payment.id)
                        .execute(&mut *tx)
                        .await?;
                    }
                    tx.commit().await?;
                    warn!(
                        payment_id = %payment.id,
                        provider_tx_id = %provider_tx.provider_tx_id,
                        expected_amount_vnd = payment.amount_vnd,
                        actual_amount_vnd = provider_tx.amount_vnd,
                        "expired payment had mismatched provider transfer; queued for reconciliation"
                    );
                }

                if handled {
                    continue;
                }

                let mut tx = self.pool.begin().await?;
                let group =
                    lock_payment_group_tx(&mut tx, &candidate.order_id, &candidate.order_group_id)
                        .await?;
                let Some(payment) = group.iter().find(|p| p.id == candidate.id).cloned() else {
                    tx.commit().await?;
                    continue;
                };
                if payment.status != "pending" {
                    tx.commit().await?;
                    continue;
                }

                sqlx::query(
                    r#"
                    UPDATE payment_shop_allocations
                    SET escrow_status = 'cancelled'
                    WHERE payment_id = $1 AND escrow_status IN ('awaiting_collection', 'held')
                    "#,
                )
                .bind(payment.id)
                .execute(&mut *tx)
                .await?;

                let (exp_final_status, exp_attempts, exp_next_check) =
                    if payment.method == "ZALOPAY" {
                        if zalopay_concluded_not_paid {
                            (Some(PROVIDER_FINAL_STATUS_NOT_PAID), 1_i32, None)
                        } else {
                            (
                                None,
                                1_i32,
                                Some(Utc::now() + zalopay_late_sweep_backoff(1, 60)),
                            )
                        }
                    } else {
                        (None, 0_i32, None)
                    };

                let (has_exp_zp_query, exp_zp_rc, exp_zp_proc) = match zalopay_last_query {
                    Some((rc, is_proc)) if payment.method == "ZALOPAY" => (true, rc, is_proc),
                    _ => (false, None, None),
                };

                let updated = sqlx::query_as::<_, PaymentRecord>(
                    r#"
                    UPDATE payments
                    SET status = 'expired',
                        provider_final_status = COALESCE(provider_final_status, $2),
                        provider_check_attempts = GREATEST(provider_check_attempts, $3),
                        provider_last_checked_at = CASE WHEN $3 > 0 THEN NOW() ELSE provider_last_checked_at END,
                        provider_next_check_at = CASE
                            WHEN COALESCE(provider_final_status, $2) IS NOT NULL THEN NULL
                            ELSE COALESCE(provider_next_check_at, $4)
                        END,
                        provider_last_query_return_code = CASE
                            WHEN $5 THEN $6
                            ELSE provider_last_query_return_code
                        END,
                        provider_last_query_is_processing = CASE
                            WHEN $5 THEN $7
                            ELSE provider_last_query_is_processing
                        END,
                        updated_at = NOW()
                    WHERE id = $1 AND status = 'pending'
                    RETURNING *
                    "#,
                )
                .bind(payment.id)
                .bind(exp_final_status)
                .bind(exp_attempts)
                .bind(exp_next_check)
                .bind(has_exp_zp_query)
                .bind(exp_zp_rc)
                .bind(exp_zp_proc)
                .fetch_optional(&mut *tx)
                .await?;

                let Some(updated) = updated else {
                    tx.commit().await?;
                    continue;
                };

                insert_outbox_event(
                    &mut tx,
                    "payment.expired",
                    &updated.order_group_id,
                    &updated.trace_id,
                    json!({
                        "payment_id": updated.id.to_string(),
                        "order_group_id": updated.order_group_id,
                        "order_id": updated.order_id,
                        "user_id": updated.user_id,
                        "reason": "payment_timeout_expired",
                        "sales_channel": updated.sales_channel,
                        "storefront_shop_id": updated.storefront_shop_id,
                    }),
                )
                .await?;

                tx.commit().await?;
                self.emit_sse(&updated);
                expired_count += 1;
            }
        }

        if expired_count > 0 {
            info!(
                expired_count = expired_count,
                "expired pending online payments"
            );
        }

        // p3 Item 1: Bounded-window multi-replica sweep for terminal unpaid ZALOPAY payments
        // (`superseded`, `expired`, `cancelled`) whose callbacks were lost or whose expiry query
        // was inconclusive (`3` / network error).
        if let Err(e) = self.scan_unsettled_zalopay_payments().await {
            error!(error = %e, "unsettled ZaloPay late-payment sweep error");
        }

        Ok(expired_count)
    }

    /// Sweeps terminal unpaid `ZALOPAY` payments (`superseded`, `expired`, `cancelled`) that have
    /// not yet received a definitive provider conclusion within the configured post-expiry window
    /// (default 24h, exponential backoff, batch-limited, multi-replica safe via `FOR UPDATE SKIP LOCKED`)
    /// — p3 Item 1.
    pub async fn scan_unsettled_zalopay_payments(&self) -> Result<ZaloPayLateSweepStats> {
        let window = Duration::from_std(self.config.zalopay_late_sweep_window)
            .unwrap_or_else(|_| Duration::hours(24));
        self.scan_unsettled_zalopay_payments_with_options(
            Utc::now(),
            window,
            self.config.zalopay_late_sweep_batch_limit,
            60,
        )
        .await
    }

    /// Deterministic entrypoint for [`Self::scan_unsettled_zalopay_payments`] with explicit clock,
    /// finite window duration, batch limit, and base retry backoff (seconds) for race/backoff tests.
    pub async fn scan_unsettled_zalopay_payments_with_options(
        &self,
        now: DateTime<Utc>,
        window: Duration,
        batch_limit: i64,
        base_backoff_secs: i64,
    ) -> Result<ZaloPayLateSweepStats> {
        let limit = batch_limit.clamp(1, 200);
        let mut stats = ZaloPayLateSweepStats::default();

        // Phase 1: Short multi-replica claim TX (`FOR UPDATE SKIP LOCKED`).
        // Atomically increments `provider_check_attempts` and sets a forward lease in
        // `provider_next_check_at` BEFORE releasing locks so a sibling replica running at the same
        // instant skips these rows and never duplicates outbound `/v2/query` calls.
        let mut claim_tx = self.pool.begin().await?;
        let claimed: Vec<ZaloPayLateSweepRow> = sqlx::query_as(
            r#"
            SELECT id, order_group_id, order_id, provider, amount_vnd,
                   provider_order_code, expires_at, created_at, provider_check_attempts
            FROM payments
            WHERE method = 'ZALOPAY'
              AND status IN ('superseded', 'expired', 'cancelled')
              AND provider_final_status IS NULL
              AND (provider_next_check_at IS NULL OR provider_next_check_at <= $1)
            ORDER BY provider_next_check_at ASC NULLS FIRST, updated_at ASC
            LIMIT $2
            FOR UPDATE SKIP LOCKED
            "#,
        )
        .bind(now)
        .bind(limit)
        .fetch_all(&mut *claim_tx)
        .await?;

        if claimed.is_empty() {
            claim_tx.commit().await?;
            return Ok(stats);
        }

        for row in &claimed {
            let new_attempts = row.provider_check_attempts.saturating_add(1);
            let claim_lease = zalopay_late_sweep_backoff(new_attempts, base_backoff_secs)
                .max(Duration::seconds(30));
            sqlx::query(
                r#"
                UPDATE payments
                SET provider_check_attempts = $1,
                    provider_last_checked_at = $2,
                    provider_next_check_at = $3,
                    updated_at = NOW()
                WHERE id = $4 AND provider_final_status IS NULL
                "#,
            )
            .bind(new_attempts)
            .bind(now)
            .bind(now + claim_lease)
            .bind(row.id)
            .execute(&mut *claim_tx)
            .await?;
        }
        claim_tx.commit().await?;

        stats.claimed = claimed.len();
        let zalopay_app_id = self.zalopay.effective_app_id().ok();

        // Phase 2: Query ZaloPay `/v2/query` outside any DB transaction, then apply each outcome
        // in its own short advisory-locked + group-locked transaction.
        for row in claimed {
            let new_attempts = row.provider_check_attempts.saturating_add(1);
            let window_anchor = row.expires_at.unwrap_or(row.created_at);
            let window_exhausted = now >= window_anchor + window;

            let query_res = self.zalopay.query_order(&row.provider_order_code).await;
            let (last_rc, last_is_proc) = match &query_res {
                Ok(resp) => (Some(resp.return_code), Some(resp.is_processing)),
                Err(_) => (None, None),
            };

            match query_res {
                Ok(resp)
                    if resp.return_code == ZALOPAY_RETURN_SUCCESS
                        && resp.zp_trans_id > 0
                        && resp.amount > 0 =>
                {
                    let provider_tx_id = resp.zp_trans_id.to_string();
                    let occurred_at = resp.normalized_server_time().ok().flatten().or(Some(now));

                    let mut tx = self.pool.begin().await?;
                    // Exact same lock ordering as `process_zalopay_callback`:
                    // advisory lock on `zp_trans_id` BEFORE checkout payment group lock.
                    lock_provider_tx_xact(&mut tx, &provider_tx_id).await?;
                    let group =
                        lock_payment_group_tx(&mut tx, &row.order_id, &row.order_group_id).await?;
                    let Some(locked) = group.iter().find(|p| p.id == row.id).cloned() else {
                        tx.commit().await?;
                        continue;
                    };

                    if locked.status == "succeeded" {
                        sqlx::query(
                            r#"
                            UPDATE payments
                            SET provider_final_status = $1,
                                provider_next_check_at = NULL,
                                provider_last_query_return_code = $2,
                                provider_last_query_is_processing = $3,
                                updated_at = NOW()
                            WHERE id = $4
                            "#,
                        )
                        .bind(PROVIDER_FINAL_STATUS_PAID_CONFIRMED)
                        .bind(last_rc)
                        .bind(last_is_proc)
                        .bind(locked.id)
                        .execute(&mut *tx)
                        .await?;
                        tx.commit().await?;
                        continue;
                    }

                    let acct = check_provider_tx_accounting_tx(&mut tx, &provider_tx_id).await?;
                    if acct.is_accounted() {
                        sqlx::query(
                            r#"
                            UPDATE payments
                            SET provider_final_status = COALESCE(provider_final_status, $1),
                                provider_next_check_at = NULL,
                                provider_last_query_return_code = $2,
                                provider_last_query_is_processing = $3,
                                updated_at = NOW()
                            WHERE id = $4
                            "#,
                        )
                        .bind(PROVIDER_FINAL_STATUS_LATE_REFUND_QUEUED)
                        .bind(last_rc)
                        .bind(last_is_proc)
                        .bind(locked.id)
                        .execute(&mut *tx)
                        .await?;
                        tx.commit().await?;
                        continue;
                    }

                    let order_already_succeeded = group.iter().any(|p| {
                        p.id != locked.id && matches!(p.status.as_str(), "succeeded" | "refunded")
                    });
                    let has_cancelled_alloc =
                        has_cancelled_allocation_tx(&mut tx, locked.id).await?;

                    // Identical decision matrix as `process_zalopay_callback` (F2 / F5).
                    let decision = decide_webhook_action(
                        Some(locked.status.as_str()),
                        Some(locked.amount_vnd),
                        resp.amount,
                        WebhookEventStatus::Succeeded,
                        order_already_succeeded,
                        has_cancelled_alloc,
                    );

                    match decision {
                        WebhookDecision::QueueReconciliation { reason } => {
                            enqueue_reconciliation_item_tx(
                                &mut tx,
                                None,
                                &row.provider,
                                &provider_tx_id,
                                Some(locked.id),
                                Some(&row.provider_order_code),
                                Some(locked.amount_vnd),
                                resp.amount,
                                reason,
                                occurred_at,
                                "reconciliation",
                                None,
                            )
                            .await?;
                            sqlx::query(
                                r#"
                                UPDATE payments
                                SET provider_final_status = $1,
                                    provider_next_check_at = NULL,
                                    provider_last_query_return_code = $2,
                                    provider_last_query_is_processing = $3,
                                    updated_at = NOW()
                                WHERE id = $4
                                "#,
                            )
                            .bind(PROVIDER_FINAL_STATUS_AMOUNT_MISMATCH)
                            .bind(last_rc)
                            .bind(last_is_proc)
                            .bind(locked.id)
                            .execute(&mut *tx)
                            .await?;
                            tx.commit().await?;
                            stats.amount_mismatches += 1;
                            warn!(
                                payment_id = %locked.id,
                                app_trans_id = %row.provider_order_code,
                                provider_tx_id = %provider_tx_id,
                                expected_amount_vnd = locked.amount_vnd,
                                actual_amount_vnd = resp.amount,
                                reason = reason,
                                "ZaloPay late sweep discovered paid transaction with mismatched amount; queued for manual reconciliation"
                            );
                        }
                        WebhookDecision::LatePaymentRefund { reason } => {
                            let pending_next_status =
                                if reason == "payment_has_cancelled_allocation" {
                                    Some("cancelled")
                                } else if reason == "order_already_has_succeeded_payment" {
                                    Some("superseded")
                                } else {
                                    None
                                };
                            record_late_payment_refund_tx(
                                &mut tx,
                                &locked,
                                &provider_tx_id,
                                resp.amount,
                                pending_next_status,
                                "auto_closed:zalopay_late_sweep_refund",
                                zalopay_app_id,
                                &self.metrics,
                            )
                            .await?;
                            sqlx::query(
                                r#"
                                UPDATE payments
                                SET provider_last_query_return_code = $1,
                                    provider_last_query_is_processing = $2
                                WHERE id = $3
                                "#,
                            )
                            .bind(last_rc)
                            .bind(last_is_proc)
                            .bind(locked.id)
                            .execute(&mut *tx)
                            .await?;
                            tx.commit().await?;
                            stats.late_refunds_queued += 1;
                            warn!(
                                payment_id = %locked.id,
                                order_id = %locked.order_id,
                                app_trans_id = %row.provider_order_code,
                                provider_tx_id = %provider_tx_id,
                                amount_vnd = resp.amount,
                                reason = reason,
                                "ZaloPay late sweep recovered lost-callback payment and queued automatic late_payment refund"
                            );
                        }
                        WebhookDecision::ConfirmPayment
                        | WebhookDecision::IgnoreNonFinal
                        | WebhookDecision::MarkFailed => {
                            // Defensive fallback: terminal unpaid payments never hit ConfirmPayment.
                            tx.commit().await?;
                        }
                    }
                }
                Ok(resp) if resp.return_code == ZALOPAY_RETURN_FAIL => {
                    // Conclusive unpaid/failed at ZaloPay -> mark final and stop sweeping.
                    sqlx::query(
                        r#"
                        UPDATE payments
                        SET provider_final_status = $1,
                            provider_next_check_at = NULL,
                            provider_last_query_return_code = $2,
                            provider_last_query_is_processing = $3,
                            updated_at = NOW()
                        WHERE id = $4 AND provider_final_status IS NULL
                        "#,
                    )
                    .bind(PROVIDER_FINAL_STATUS_NOT_PAID)
                    .bind(last_rc)
                    .bind(last_is_proc)
                    .bind(row.id)
                    .execute(&self.pool)
                    .await?;
                    stats.concluded_not_paid += 1;
                    info!(
                        payment_id = %row.id,
                        app_trans_id = %row.provider_order_code,
                        sub_return_code = resp.sub_return_code,
                        "ZaloPay late sweep concluded payment not_paid (return_code=2); stopped sweeping"
                    );
                }
                res if window_exhausted => {
                    // p5: Finite 24h window elapsed without return_code=1/2.
                    // Persist `(provider_last_query_return_code, provider_last_query_is_processing)`
                    // in Postgres and decide strictly from the persisted tuple (zero in-memory state):
                    // - `return_code=3, is_processing=false` -> buyer abandoned unpaid order ->
                    //   conclude `not_paid` with INFO log, do NOT enqueue `reconciliation_queue`.
                    // - `is_processing=true`, unknown return_code, or network error -> escalate to
                    //   `reconciliation_queue` + `provider_final_status = 'manual_review'` + ERROR log.
                    let query_err_detail = res.err().map(|e| e.to_string());
                    let mut tx = self.pool.begin().await?;
                    let current_final: Option<String> = sqlx::query_scalar(
                        "SELECT provider_final_status FROM payments WHERE id = $1 FOR UPDATE",
                    )
                    .bind(row.id)
                    .fetch_optional(&mut *tx)
                    .await?
                    .flatten();

                    if current_final.is_none() {
                        match decide_zalopay_window_exhausted(last_rc, last_is_proc) {
                            ZaloPayWindowExhaustedDecision::ConcludeNotPaid => {
                                sqlx::query(
                                    r#"
                                    UPDATE payments
                                    SET provider_final_status = $1,
                                        provider_next_check_at = NULL,
                                        provider_last_query_return_code = $2,
                                        provider_last_query_is_processing = $3,
                                        updated_at = NOW()
                                    WHERE id = $4 AND provider_final_status IS NULL
                                    "#,
                                )
                                .bind(PROVIDER_FINAL_STATUS_NOT_PAID)
                                .bind(last_rc)
                                .bind(last_is_proc)
                                .bind(row.id)
                                .execute(&mut *tx)
                                .await?;
                                tx.commit().await?;
                                stats.concluded_not_paid += 1;
                                info!(
                                    payment_id = %row.id,
                                    order_id = %row.order_id,
                                    order_group_id = %row.order_group_id,
                                    app_trans_id = %row.provider_order_code,
                                    last_return_code = last_rc,
                                    last_is_processing = last_is_proc,
                                    attempts = new_attempts,
                                    window_hours = window.num_hours(),
                                    "ZaloPay late-payment sweep exhausted finite window on unpaid order (return_code=3, is_processing=false); concluded not_paid"
                                );
                            }
                            ZaloPayWindowExhaustedDecision::EscalateManualReview => {
                                let unresolved_tx_id =
                                    format!("zalopay-unresolved:{}", row.provider_order_code);
                                enqueue_reconciliation_item_tx(
                                    &mut tx,
                                    None,
                                    &row.provider,
                                    &unresolved_tx_id,
                                    Some(row.id),
                                    Some(&row.provider_order_code),
                                    Some(row.amount_vnd),
                                    0,
                                    "zalopay_late_sweep_window_exhausted",
                                    Some(now),
                                    "reconciliation",
                                    None,
                                )
                                .await?;
                                sqlx::query(
                                    r#"
                                    UPDATE payments
                                    SET provider_final_status = $1,
                                        provider_next_check_at = NULL,
                                        provider_last_query_return_code = $2,
                                        provider_last_query_is_processing = $3,
                                        updated_at = NOW()
                                    WHERE id = $4 AND provider_final_status IS NULL
                                    "#,
                                )
                                .bind(PROVIDER_FINAL_STATUS_MANUAL_REVIEW)
                                .bind(last_rc)
                                .bind(last_is_proc)
                                .bind(row.id)
                                .execute(&mut *tx)
                                .await?;
                                tx.commit().await?;
                                stats.escalated_manual_review += 1;
                                error!(
                                    payment_id = %row.id,
                                    order_id = %row.order_id,
                                    order_group_id = %row.order_group_id,
                                    app_trans_id = %row.provider_order_code,
                                    last_return_code = last_rc,
                                    last_is_processing = last_is_proc,
                                    error = query_err_detail.as_deref(),
                                    attempts = new_attempts,
                                    window_hours = window.num_hours(),
                                    "ZaloPay late-payment sweep exhausted finite window without unpaid conclusion; escalated to reconciliation_queue (manual_review)"
                                );
                            }
                        }
                    } else {
                        tx.commit().await?;
                    }
                }
                Ok(resp) => {
                    // Inside finite window: never conclude early (including `3, is_processing=false`);
                    // persist the latest observation to Postgres and retry on exponential backoff.
                    let retry_delay = zalopay_late_sweep_backoff(new_attempts, base_backoff_secs);
                    sqlx::query(
                        r#"
                        UPDATE payments
                        SET provider_next_check_at = $1,
                            provider_last_query_return_code = $2,
                            provider_last_query_is_processing = $3,
                            updated_at = NOW()
                        WHERE id = $4 AND provider_final_status IS NULL
                        "#,
                    )
                    .bind(now + retry_delay)
                    .bind(last_rc)
                    .bind(last_is_proc)
                    .bind(row.id)
                    .execute(&self.pool)
                    .await?;
                    stats.retried += 1;
                    if resp.return_code == ZALOPAY_RETURN_PROCESSING && !resp.is_processing {
                        info!(
                            payment_id = %row.id,
                            app_trans_id = %row.provider_order_code,
                            return_code = resp.return_code,
                            is_processing = resp.is_processing,
                            attempts = new_attempts,
                            next_retry_secs = retry_delay.num_seconds(),
                            "ZaloPay late sweep saw unpaid order inside finite window; scheduled exponential backoff retry"
                        );
                    } else {
                        warn!(
                            payment_id = %row.id,
                            app_trans_id = %row.provider_order_code,
                            return_code = resp.return_code,
                            is_processing = resp.is_processing,
                            attempts = new_attempts,
                            next_retry_secs = retry_delay.num_seconds(),
                            "ZaloPay late sweep got non-final return_code; scheduled exponential backoff retry"
                        );
                    }
                }
                Err(e) => {
                    // Transient network/timeout/CB error inside finite window -> clear stale
                    // persisted query state to NULL and schedule exponential backoff retry.
                    let retry_delay = zalopay_late_sweep_backoff(new_attempts, base_backoff_secs);
                    sqlx::query(
                        r#"
                        UPDATE payments
                        SET provider_next_check_at = $1,
                            provider_last_query_return_code = NULL,
                            provider_last_query_is_processing = NULL,
                            updated_at = NOW()
                        WHERE id = $2 AND provider_final_status IS NULL
                        "#,
                    )
                    .bind(now + retry_delay)
                    .bind(row.id)
                    .execute(&self.pool)
                    .await?;
                    stats.retried += 1;
                    warn!(
                        payment_id = %row.id,
                        app_trans_id = %row.provider_order_code,
                        attempts = new_attempts,
                        next_retry_secs = retry_delay.num_seconds(),
                        error = %e,
                        "ZaloPay late sweep /v2/query transport error; scheduled exponential backoff retry"
                    );
                }
            }
        }

        Ok(stats)
    }

    /// Runs reconciliation comparing local VietQR payments vs provider transaction feed (UC-PAY07 / PAY-B8).
    /// - Evaluates `run_date` strictly in `Asia/Ho_Chi_Minh` (`vn_day_utc_bounds`), rejecting future dates (400).
    /// - In `VietQrMode::Mock`, reads recorded provider transactions from `webhook_events` in Postgres (bounded by `received_at`).
    /// - On ANY error after creating the `reconciliation_runs` row, transitions `status = 'failed'` (P1-1).
    /// - Processes each matched provider transaction in its own short advisory-locked + group-locked transaction (P0-1 / P2-5).
    /// - Supports optional `channel_filter` (`MARKETPLACE` | `STOREFRONT`): channel-scoped runs inspect all local
    ///   VietQR payments so the other channel's provider transfers are skipped rather than misclassified as `missing_local`.
    pub async fn run_reconciliation(
        &self,
        run_date: NaiveDate,
        channel_filter: Option<SalesChannel>,
    ) -> Result<ReconciliationReport> {
        let today_vn = vn_today(Utc::now());
        if run_date > today_vn {
            return Err(PaymentError::Validation(format!(
                "reconciliation date {run_date} cannot be in the future (today in Asia/Ho_Chi_Minh is {today_vn})"
            )));
        }

        let provider_name = self.vietqr.mode().as_str().to_string();
        let filter_str = channel_filter.map(SalesChannel::as_str).unwrap_or("");
        let run_id: Uuid = sqlx::query_scalar(
            r#"
            INSERT INTO reconciliation_runs (provider, run_date, status, run_type, sales_channel)
            VALUES ($1, $2, 'running', 'vietqr', $3)
            RETURNING id
            "#,
        )
        .bind(&provider_name)
        .bind(run_date)
        .bind(filter_str)
        .fetch_one(&self.pool)
        .await?;

        match self
            .execute_reconciliation_run(run_id, &provider_name, run_date, channel_filter)
            .await
        {
            Ok(report) => Ok(report),
            Err(err) => {
                let _ = sqlx::query(
                    "UPDATE reconciliation_runs SET status = 'failed', completed_at = NOW() WHERE id = $1 AND status = 'running'",
                )
                .bind(run_id)
                .execute(&self.pool)
                .await;
                warn!(
                    run_id = %run_id,
                    provider = %provider_name,
                    run_date = %run_date,
                    error = %err,
                    "reconciliation run failed; marked status='failed'"
                );
                Err(err)
            }
        }
    }

    async fn execute_reconciliation_run(
        &self,
        run_id: Uuid,
        provider_name: &str,
        run_date: NaiveDate,
        channel_filter: Option<SalesChannel>,
    ) -> Result<ReconciliationReport> {
        let provider_txs: Vec<ProviderTransaction> = match self.vietqr.mode() {
            VietQrMode::Mock => {
                self.load_mock_provider_transactions_for_vn_date(run_date)
                    .await?
            }
            VietQrMode::PayOs | VietQrMode::Sepay => self
                .vietqr
                .list_provider_transactions(run_date)
                .await?
                .into_iter()
                .filter(|ptx| utc_to_vn_date(ptx.occurred_at) == run_date)
                .collect(),
        };

        let (start_utc, end_utc) = vn_day_utc_bounds(run_date);

        // Manager review P2-3 (cross-midnight) + independent review P2-1 & P3-3:
        // 1. Look up local payments matching the provider feed by `provider_tx_id` or
        //    `provider_order_code` WITHOUT restricting `created_at`.
        // 2. For `missing_provider`, check BOTH `succeeded` and `refunded` VietQR payments
        //    using an index-aligned split predicate (`idx_payments_vietqr_paid_at`).
        let feed_tx_ids: Vec<String> = provider_txs
            .iter()
            .map(|t| t.provider_tx_id.clone())
            .filter(|s| !s.is_empty())
            .collect();
        let feed_order_codes: Vec<String> = provider_txs
            .iter()
            .map(|t| t.provider_order_code.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect();

        // Intentionally scoped to `method = 'VIETQR'`: VietQR batch bank-statement reconciliation
        // runs (`run_type = 'vietqr'`) compare against the bank transfer statement feed; ZaloPay v2
        // has no batch daily statement API and reconciles per-payment via `/v2/query` & queue (a6 §7).
        let matched_candidates = if feed_tx_ids.is_empty() && feed_order_codes.is_empty() {
            Vec::new()
        } else {
            sqlx::query_as::<_, PaymentRecord>(
                r#"
                SELECT * FROM payments
                WHERE method = 'VIETQR'
                  AND amount_vnd > 0
                  AND (
                      provider_tx_id = ANY($1)
                      OR provider_order_code = ANY($2)
                  )
                "#,
            )
            .bind(&feed_tx_ids)
            .bind(&feed_order_codes)
            .fetch_all(&self.pool)
            .await?
        };

        let local_paid_payments = sqlx::query_as::<_, PaymentRecord>(
            r#"
            SELECT * FROM payments
            WHERE method = 'VIETQR'
              AND amount_vnd > 0
              AND status IN ('succeeded', 'refunded')
              AND (
                  (paid_at >= $1 AND paid_at < $2)
                  OR (paid_at IS NULL AND created_at >= $1 AND created_at < $2)
              )
            "#,
        )
        .bind(start_utc)
        .bind(end_utc)
        .fetch_all(&self.pool)
        .await?;

        let mut by_tx_id: HashMap<String, &PaymentRecord> = HashMap::new();
        let mut by_order_code: HashMap<String, &PaymentRecord> = HashMap::new();
        for p in &matched_candidates {
            if let Some(ref ptx) = p.provider_tx_id {
                by_tx_id.insert(ptx.clone(), p);
            }
            by_order_code.insert(p.provider_order_code.clone(), p);
        }

        // Deterministic sort by `(order_group_id, payment_id, provider_tx_id)` (review2-P P2-5).
        let mut ordered_txs: Vec<(&ProviderTransaction, Option<&PaymentRecord>)> = provider_txs
            .iter()
            .map(|ptx| {
                let local_opt = by_tx_id
                    .get(&ptx.provider_tx_id)
                    .copied()
                    .or_else(|| by_order_code.get(&ptx.provider_order_code).copied());
                (ptx, local_opt)
            })
            .collect();
        ordered_txs.sort_by(|(a_tx, a_loc), (b_tx, b_loc)| {
            let a_key = a_loc.map(|p| (p.order_group_id.as_str(), p.id));
            let b_key = b_loc.map(|p| (p.order_group_id.as_str(), p.id));
            a_key
                .cmp(&b_key)
                .then_with(|| a_tx.provider_tx_id.cmp(&b_tx.provider_tx_id))
        });

        let mut matched_payment_ids: HashSet<Uuid> = HashSet::new();
        let mut matched_count = 0_i32;
        let mut missing_local_count = 0_i32;
        let mut missing_provider_count = 0_i32;
        let mut amount_mismatch_count = 0_i32;
        let mut auto_healed_count = 0_i32;
        let mut items = Vec::new();

        for (ptx, local_opt) in ordered_txs {
            if let Some(local) = local_opt {
                if !reconciliation_channel_matches(&local.sales_channel, channel_filter) {
                    continue;
                }
            } else if channel_filter.is_some() {
                // Provider-only transactions have no local channel; report only on unfiltered runs.
                continue;
            }

            let mut tx = self.pool.begin().await?;
            // P0-1: Serialize per provider_tx_id BEFORE acquiring payment group locks.
            lock_provider_tx_xact(&mut tx, &ptx.provider_tx_id).await?;

            match local_opt {
                None => {
                    let acct =
                        check_provider_tx_accounting_tx(&mut tx, &ptx.provider_tx_id).await?;
                    if acct.is_accounted() {
                        matched_count += 1;
                        items.push(
                            insert_reconciliation_item(
                                &mut tx,
                                run_id,
                                "matched",
                                None,
                                Some(&ptx.provider_tx_id),
                                None,
                                Some(ptx.amount_vnd),
                                "already accounted for via manual reconciliation or refund",
                                "",
                                "",
                            )
                            .await?,
                        );
                    } else {
                        missing_local_count += 1;
                        items.push(
                            insert_reconciliation_item(
                                &mut tx,
                                run_id,
                                "missing_local",
                                None,
                                Some(&ptx.provider_tx_id),
                                None,
                                Some(ptx.amount_vnd),
                                "present in provider feed but missing in local payments",
                                "",
                                "",
                            )
                            .await?,
                        );
                        // P2-3(a): Enqueue missing_local into manual reconciliation_queue.
                        let order_ref = if ptx.provider_order_code.trim().is_empty() {
                            None
                        } else {
                            Some(ptx.provider_order_code.as_str())
                        };
                        enqueue_reconciliation_item_tx(
                            &mut tx,
                            None,
                            provider_name,
                            &ptx.provider_tx_id,
                            None,
                            order_ref,
                            None,
                            ptx.amount_vnd,
                            "reconciliation_missing_local",
                            Some(ptx.occurred_at),
                            "reconciliation",
                            None,
                        )
                        .await?;
                    }
                    tx.commit().await?;
                }
                Some(local) => {
                    // P1-4 / P1-5 / P2-5: lock ONLY this payment's checkout group in ascending id
                    // order inside a short per-item transaction.
                    let group =
                        lock_payment_group_tx(&mut tx, &local.order_id, &local.order_group_id)
                            .await?;
                    let current = group
                        .iter()
                        .find(|p| p.id == local.id)
                        .cloned()
                        .unwrap_or_else(|| local.clone());
                    let local_ch = ChannelAttribution::from_db(
                        &current.sales_channel,
                        &current.storefront_shop_id,
                    );

                    // P0-1 & P2-2: If this provider_tx_id is ALREADY accounted for (recorded on a
                    // payment, finalized assigned/refunded in reconciliation_queue, or refunded
                    // via late-webhook/recon-refund/recon-overpay), skip all gateway_clearing writes
                    // and classify as matched instead of repeating amount_mismatch or double-booking.
                    let acct =
                        check_provider_tx_accounting_tx(&mut tx, &ptx.provider_tx_id).await?;
                    if acct.is_accounted() {
                        let matches_local_confirmed_tx =
                            current.provider_tx_id.as_deref() == Some(ptx.provider_tx_id.as_str());
                        if matches_local_confirmed_tx
                            || !matches!(current.status.as_str(), "succeeded" | "refunded")
                        {
                            matched_payment_ids.insert(local.id);
                        }
                        matched_count += 1;
                        let detail =
                            if matches_local_confirmed_tx && current.amount_vnd == ptx.amount_vnd {
                                "matched"
                            } else {
                                "already accounted for (manual reconciliation or refund)"
                            };
                        items.push(
                            insert_reconciliation_item(
                                &mut tx,
                                run_id,
                                "matched",
                                Some(current.id),
                                Some(&ptx.provider_tx_id),
                                Some(current.amount_vnd),
                                Some(ptx.amount_vnd),
                                detail,
                                local_ch.sales_channel.as_str(),
                                &local_ch.storefront_shop_id,
                            )
                            .await?,
                        );
                        tx.commit().await?;
                        continue;
                    }

                    let order_already_succeeded = group.iter().any(|p| {
                        p.id != current.id && matches!(p.status.as_str(), "succeeded" | "refunded")
                    });
                    let has_cancelled_alloc =
                        has_cancelled_allocation_tx(&mut tx, current.id).await?;

                    let decision = decide_reconciliation_match_action(
                        &current.status,
                        current.amount_vnd,
                        ptx.amount_vnd,
                        current.provider_tx_id.as_deref(),
                        &ptx.provider_tx_id,
                        order_already_succeeded,
                        has_cancelled_alloc,
                    );

                    // Do not let a duplicate transfer (`TX-2` != `current.provider_tx_id`) mask
                    // a missing original `TX-1` for an already-succeeded/refunded payment (P2-6).
                    if !(decision == ReconciliationDecision::LatePaymentRefund
                        && matches!(current.status.as_str(), "succeeded" | "refunded"))
                    {
                        matched_payment_ids.insert(local.id);
                    }

                    let mut healed_sse: Option<PaymentRecord> = None;

                    match decision {
                        ReconciliationDecision::AmountMismatch => {
                            amount_mismatch_count += 1;
                            items.push(
                                insert_reconciliation_item(
                                    &mut tx,
                                    run_id,
                                    "amount_mismatch",
                                    Some(current.id),
                                    Some(&ptx.provider_tx_id),
                                    Some(current.amount_vnd),
                                    Some(ptx.amount_vnd),
                                    "amount mismatch between local payment and provider",
                                    local_ch.sales_channel.as_str(),
                                    &local_ch.storefront_shop_id,
                                )
                                .await?,
                            );

                            enqueue_reconciliation_item_tx(
                                &mut tx,
                                None,
                                provider_name,
                                &ptx.provider_tx_id,
                                Some(current.id),
                                Some(&current.order_id),
                                Some(current.amount_vnd),
                                ptx.amount_vnd,
                                "reconciliation_amount_mismatch",
                                Some(ptx.occurred_at),
                                "reconciliation",
                                None,
                            )
                            .await?;
                        }
                        ReconciliationDecision::AutoHealAndMatch => {
                            let healed = confirm_online_payment_tx(
                                &mut tx,
                                &current,
                                &ptx.provider_tx_id,
                                Some(ptx.occurred_at),
                                Some("auto_closed:reconciliation_auto_heal"),
                                &current.trace_id,
                                &self.metrics,
                            )
                            .await?;
                            healed_sse = Some(healed);
                            auto_healed_count += 1;
                            matched_count += 1;
                            items.push(
                                insert_reconciliation_item(
                                    &mut tx,
                                    run_id,
                                    "matched",
                                    Some(current.id),
                                    Some(&ptx.provider_tx_id),
                                    Some(current.amount_vnd),
                                    Some(ptx.amount_vnd),
                                    "auto-healed missing webhook callback",
                                    local_ch.sales_channel.as_str(),
                                    &local_ch.storefront_shop_id,
                                )
                                .await?,
                            );
                        }
                        ReconciliationDecision::LatePaymentRefund => {
                            let pending_next_status = if current.status == "pending" {
                                Some(if has_cancelled_alloc {
                                    "cancelled"
                                } else {
                                    "superseded"
                                })
                            } else {
                                None
                            };
                            record_late_payment_refund_tx(
                                &mut tx,
                                &current,
                                &ptx.provider_tx_id,
                                ptx.amount_vnd,
                                pending_next_status,
                                "auto_closed:reconciliation_late_refund",
                                None,
                                &self.metrics,
                            )
                            .await?;
                            matched_count += 1;
                            items.push(
                                insert_reconciliation_item(
                                    &mut tx,
                                    run_id,
                                    "matched",
                                    Some(current.id),
                                    Some(&ptx.provider_tx_id),
                                    Some(current.amount_vnd),
                                    Some(ptx.amount_vnd),
                                    "late, duplicate, or terminal unpaid payment in provider feed; queued automatic late_payment refund",
                                    local_ch.sales_channel.as_str(),
                                    &local_ch.storefront_shop_id,
                                )
                                .await?,
                            );
                        }
                        _ => {
                            matched_count += 1;
                            items.push(
                                insert_reconciliation_item(
                                    &mut tx,
                                    run_id,
                                    "matched",
                                    Some(current.id),
                                    Some(&ptx.provider_tx_id),
                                    Some(current.amount_vnd),
                                    Some(ptx.amount_vnd),
                                    "matched",
                                    local_ch.sales_channel.as_str(),
                                    &local_ch.storefront_shop_id,
                                )
                                .await?,
                            );
                        }
                    }

                    tx.commit().await?;
                    if let Some(ref h) = healed_sse {
                        self.emit_sse(h);
                    }
                }
            }
        }

        let mut tx = self.pool.begin().await?;
        for local in &local_paid_payments {
            if !reconciliation_channel_matches(&local.sales_channel, channel_filter) {
                continue;
            }
            if !matched_payment_ids.contains(&local.id) {
                let local_ch =
                    ChannelAttribution::from_db(&local.sales_channel, &local.storefront_shop_id);
                missing_provider_count += 1;
                items.push(
                    insert_reconciliation_item(
                        &mut tx,
                        run_id,
                        "missing_provider",
                        Some(local.id),
                        local.provider_tx_id.as_deref(),
                        Some(local.amount_vnd),
                        None,
                        "local succeeded/refunded payment absent from provider report",
                        local_ch.sales_channel.as_str(),
                        &local_ch.storefront_shop_id,
                    )
                    .await?,
                );
            }
        }

        let total_rows =
            matched_count + missing_local_count + missing_provider_count + amount_mismatch_count;
        let total_matched_vnd: i64 = items
            .iter()
            .filter(|it| it.classification == "matched")
            .fold(0_i64, |acc, it| {
                acc.saturating_add(it.provider_amount_vnd.or(it.local_amount_vnd).unwrap_or(0))
            });

        let run = sqlx::query_as::<_, ReconciliationRunRecord>(
            r#"
            UPDATE reconciliation_runs
            SET matched_count = $1,
                missing_local_count = $2,
                missing_provider_count = $3,
                amount_mismatch_count = $4,
                auto_healed_count = $5,
                total_rows = $6,
                total_matched_vnd = $7,
                status = 'completed',
                completed_at = NOW()
            WHERE id = $8
            RETURNING *
            "#,
        )
        .bind(matched_count)
        .bind(missing_local_count)
        .bind(missing_provider_count)
        .bind(amount_mismatch_count)
        .bind(auto_healed_count)
        .bind(total_rows)
        .bind(total_matched_vnd)
        .bind(run_id)
        .fetch_one(&mut *tx)
        .await?;

        tx.commit().await?;

        info!(
            run_id = %run.id,
            matched = run.matched_count,
            missing_local = run.missing_local_count,
            missing_provider = run.missing_provider_count,
            amount_mismatch = run.amount_mismatch_count,
            auto_healed = run.auto_healed_count,
            "reconciliation run completed"
        );

        Ok(ReconciliationReport { run, items })
    }

    /// Reads durable mock provider transactions from `webhook_events` for the given VN date (PAY-B8 / P3-1).
    /// Bounds SQL scan on `w.received_at` by `vn_day_utc_bounds(run_date)` ± 1 day before filtering exact `occurred_at`.
    async fn load_mock_provider_transactions_for_vn_date(
        &self,
        run_date: NaiveDate,
    ) -> Result<Vec<ProviderTransaction>> {
        let (start_utc, end_utc) = vn_day_utc_bounds(run_date);
        let rows = sqlx::query(
            r#"
            SELECT
                w.provider_tx_id,
                w.amount_vnd,
                w.order_ref,
                w.raw_payload,
                w.received_at,
                p.provider_order_code AS payment_order_code
            FROM webhook_events w
            LEFT JOIN payments p ON p.id = w.payment_id
            WHERE w.signature_verified = TRUE
              AND LOWER(w.provider) NOT LIKE 'zalopay%'
              AND (p.id IS NULL OR p.method = 'VIETQR')
              AND w.amount_vnd > 0
              AND w.received_at >= $1 - INTERVAL '1 day'
              AND w.received_at < $2 + INTERVAL '1 day'
              AND COALESCE(LOWER(w.raw_payload->>'status'), 'succeeded')
                  NOT IN ('failed', 'cancelled', 'canceled', 'expired')
            ORDER BY w.received_at ASC
            "#,
        )
        .bind(start_utc)
        .bind(end_utc)
        .fetch_all(&self.pool)
        .await?;

        let mut out = Vec::new();
        for row in rows {
            let provider_tx_id: String = row.try_get("provider_tx_id")?;
            let amount_vnd: i64 = row.try_get("amount_vnd")?;
            let order_ref: Option<String> = row.try_get("order_ref")?;
            let raw_payload: serde_json::Value = row.try_get("raw_payload")?;
            let received_at: chrono::DateTime<Utc> = row.try_get("received_at")?;
            let payment_order_code: Option<String> = row.try_get("payment_order_code")?;

            let occurred_at = extract_occurred_at_from_json(&raw_payload).unwrap_or(received_at);
            if utc_to_vn_date(occurred_at) != run_date {
                continue;
            }

            let provider_order_code = payment_order_code
                .filter(|s| !s.trim().is_empty())
                .or_else(|| {
                    serde_json::from_value::<IncomingWebhookPayload>(raw_payload.clone())
                        .ok()
                        .and_then(|p| p.extracted_order_code())
                })
                .or(order_ref)
                .unwrap_or_default();

            if let Ok(ptx) = ProviderTransaction::new(
                provider_tx_id,
                provider_order_code,
                amount_vnd,
                "succeeded".to_string(),
                occurred_at,
            ) {
                out.push(ptx);
            }
        }
        Ok(out)
    }

    /// Imports a carrier COD settlement CSV batch row-by-row (E20 / E20-bis):
    /// - Records a `reconciliation_runs` row with `run_type = 'cod_file'` and `file_sha256`
    ///   (NEVER skips re-importing based on `file_sha256`: re-uploading after `order.delivered`
    ///   must reconcile previously `not_delivered` rows and auto-close their queue entries).
    /// - Processes each row in its OWN short transaction serialized via
    ///   `lock_provider_tx_xact("cod:<carrier>:<tracking>")` + `FOR UPDATE` on matching COD
    ///   `payment_shop_allocations` so one bad row never blocks others and concurrent imports
    ///   of the same file post strictly one ledger transaction per `(payment_id, order_id, shop_id)`.
    /// - Does NOT alter the `order.completed -> seller_pending` settlement flow.
    pub async fn import_cod_reconciliation(
        &self,
        input: ImportCodReconciliationInput,
    ) -> Result<ReconciliationReport> {
        let carrier_code = normalize_cod_carrier_code(&input.carrier_code)?;
        validate_cod_reconciliation_rows(&input.rows)?;
        let today_vn = vn_today(Utc::now());
        if input.run_date > today_vn {
            return Err(PaymentError::Validation(format!(
                "reconciliation date {} cannot be in the future (today in Asia/Ho_Chi_Minh is {today_vn})",
                input.run_date
            )));
        }

        let file_name = input.file_name.trim();
        let file_sha256 = input.file_sha256.trim();
        let created_by = input.created_by.trim();
        let total_rows = input.rows.len() as i32;

        let (run_id, run_created_at): (Uuid, DateTime<Utc>) = sqlx::query_as(
            r#"
            INSERT INTO reconciliation_runs (
                provider, run_date, status, run_type, file_sha256, file_name,
                total_rows, created_by
            )
            VALUES ($1, $2, 'running', 'cod_file', $3, $4, $5, $6)
            RETURNING id, created_at
            "#,
        )
        .bind(&carrier_code)
        .bind(input.run_date)
        .bind(file_sha256)
        .bind(file_name)
        .bind(total_rows)
        .bind(created_by)
        .fetch_one(&self.pool)
        .await?;

        // Run row processing in a detached-safe Tokio task so client/gateway cancellation
        // (dropping the tonic RPC future) never strands `reconciliation_runs.status = 'running'`.
        let svc = self.clone();
        let rows = input.rows;
        let handle = tokio::spawn(async move {
            svc.execute_cod_reconciliation_run(
                run_id,
                run_created_at,
                carrier_code,
                total_rows,
                rows,
            )
            .await
        });

        match handle.await {
            Ok(res) => res,
            Err(join_err) => {
                let _ = sqlx::query(
                    "UPDATE reconciliation_runs SET status = 'failed', completed_at = NOW() WHERE id = $1 AND status = 'running'",
                )
                .bind(run_id)
                .execute(&self.pool)
                .await;
                Err(PaymentError::Internal(format!(
                    "COD reconciliation task failed: {join_err}"
                )))
            }
        }
    }

    async fn execute_cod_reconciliation_run(
        &self,
        run_id: Uuid,
        run_created_at: DateTime<Utc>,
        carrier_code: String,
        total_rows: i32,
        rows: Vec<CodReconciliationRowInput>,
    ) -> Result<ReconciliationReport> {
        let mut matched_count = 0_i32;
        let mut already_reconciled_count = 0_i32;
        let mut missing_local_count = 0_i32;
        let mut amount_mismatch_count = 0_i32;
        let mut not_delivered_count = 0_i32;
        let mut total_matched_vnd = 0_i64;
        let mut items = Vec::with_capacity(rows.len());

        for (idx, row) in rows.iter().enumerate() {
            let line_number = if row.line_number > 0 {
                row.line_number
            } else {
                (idx as i32) + 1
            };
            match self
                .process_single_cod_reconciliation_row(
                    run_id,
                    run_created_at,
                    &carrier_code,
                    line_number,
                    row,
                )
                .await
            {
                Ok(CodRowOutcome::Matched { item, matched_vnd }) => {
                    matched_count += 1;
                    total_matched_vnd = total_matched_vnd.saturating_add(matched_vnd);
                    items.push(item);
                }
                Ok(CodRowOutcome::AlreadyReconciled(item)) => {
                    already_reconciled_count += 1;
                    items.push(item);
                }
                Ok(CodRowOutcome::MissingLocal(item)) => {
                    missing_local_count += 1;
                    items.push(item);
                }
                Ok(CodRowOutcome::AmountMismatch(item)) => {
                    amount_mismatch_count += 1;
                    items.push(item);
                }
                Ok(CodRowOutcome::NotDelivered(item)) => {
                    not_delivered_count += 1;
                    items.push(item);
                }
                Err(row_err) => {
                    warn!(
                        run_id = %run_id,
                        carrier = %carrier_code,
                        line_number = line_number,
                        tracking_code = %row.tracking_code.trim(),
                        error = %row_err,
                        "isolated COD reconciliation row error; continuing remaining rows"
                    );
                    missing_local_count += 1;
                    let fallback_tx_id =
                        format!("cod:{carrier_code}:row-error:{run_id}:{line_number}");
                    let details = format!("row processing error: {row_err}");
                    if let Ok(mut err_tx) = self.pool.begin().await {
                        let _ = enqueue_cod_reconciliation_item_tx(
                            &mut err_tx,
                            &carrier_code,
                            &fallback_tx_id,
                            row.tracking_code.trim(),
                            None,
                            (!row.order_code.trim().is_empty()).then_some(row.order_code.trim()),
                            None,
                            row.cod_amount_vnd,
                            "cod_row_error",
                            None,
                        )
                        .await;
                        if let Ok(rec) = insert_reconciliation_item_ext(
                            &mut err_tx,
                            run_id,
                            "missing_local",
                            None,
                            Some(&fallback_tx_id),
                            None,
                            Some(row.cod_amount_vnd),
                            &details,
                            row.tracking_code.trim(),
                            row.order_code.trim(),
                            line_number,
                            "",
                            "",
                        )
                        .await
                        {
                            if err_tx.commit().await.is_ok() {
                                items.push(rec);
                                continue;
                            }
                        }
                    }
                    // If DB itself failed even to record the fallback error item, mark run failed.
                    let _ = sqlx::query(
                        "UPDATE reconciliation_runs SET status = 'failed', completed_at = NOW() WHERE id = $1 AND status = 'running'",
                    )
                    .bind(run_id)
                    .execute(&self.pool)
                    .await;
                    return Err(row_err);
                }
            }
        }

        let run = match sqlx::query_as::<_, ReconciliationRunRecord>(
            r#"
            UPDATE reconciliation_runs
            SET matched_count = $1,
                already_reconciled_count = $2,
                missing_local_count = $3,
                missing_provider_count = 0,
                amount_mismatch_count = $4,
                not_delivered_count = $5,
                total_rows = $6,
                total_matched_vnd = $7,
                status = 'completed',
                completed_at = NOW()
            WHERE id = $8
              AND status = 'running'
            RETURNING *
            "#,
        )
        .bind(matched_count)
        .bind(already_reconciled_count)
        .bind(missing_local_count)
        .bind(amount_mismatch_count)
        .bind(not_delivered_count)
        .bind(total_rows)
        .bind(total_matched_vnd)
        .bind(run_id)
        .fetch_optional(&self.pool)
        .await
        {
            Ok(Some(r)) => r,
            Ok(None) => {
                warn!(
                    run_id = %run_id,
                    carrier = %carrier_code,
                    total_rows = total_rows,
                    matched = matched_count,
                    already_reconciled = already_reconciled_count,
                    missing_local = missing_local_count,
                    amount_mismatch = amount_mismatch_count,
                    not_delivered = not_delivered_count,
                    total_matched_vnd = total_matched_vnd,
                    "COD reconciliation run was no longer 'running' at finalization (likely timed out to 'failed'); skipping overwrite to 'completed'"
                );
                let existing = sqlx::query_as::<_, ReconciliationRunRecord>(
                    "SELECT * FROM reconciliation_runs WHERE id = $1",
                )
                .bind(run_id)
                .fetch_optional(&self.pool)
                .await?
                .ok_or_else(|| {
                    PaymentError::NotFound(format!("reconciliation run {run_id} not found"))
                })?;
                return Ok(ReconciliationReport {
                    run: existing,
                    items,
                });
            }
            Err(err) => {
                let _ = sqlx::query(
                    "UPDATE reconciliation_runs SET status = 'failed', completed_at = NOW() WHERE id = $1 AND status = 'running'",
                )
                .bind(run_id)
                .execute(&self.pool)
                .await;
                return Err(err.into());
            }
        };

        info!(
            run_id = %run.id,
            carrier = %carrier_code,
            total_rows = run.total_rows,
            matched = run.matched_count,
            already_reconciled = run.already_reconciled_count,
            missing_local = run.missing_local_count,
            amount_mismatch = run.amount_mismatch_count,
            not_delivered = run.not_delivered_count,
            total_matched_vnd = run.total_matched_vnd,
            "COD file reconciliation run completed"
        );

        Ok(ReconciliationReport { run, items })
    }

    async fn process_single_cod_reconciliation_row(
        &self,
        run_id: Uuid,
        run_created_at: DateTime<Utc>,
        carrier_code: &str,
        line_number: i32,
        row: &CodReconciliationRowInput,
    ) -> Result<CodRowOutcome> {
        let tracking_code = row.tracking_code.trim();
        let order_code = row.order_code.trim();
        let delivered_at_raw = row.delivered_at.trim();
        let delivered_at = (!delivered_at_raw.is_empty())
            .then(|| parse_provider_timestamp(delivered_at_raw))
            .flatten();

        if tracking_code.is_empty() {
            let mut tx = self.pool.begin().await?;
            let invalid_tx_id = format!("cod:{carrier_code}:invalid:{run_id}:{line_number}");
            enqueue_cod_reconciliation_item_tx(
                &mut tx,
                carrier_code,
                &invalid_tx_id,
                "",
                None,
                (!order_code.is_empty()).then_some(order_code),
                None,
                row.cod_amount_vnd,
                "cod_invalid_row",
                delivered_at,
            )
            .await?;
            let item = insert_reconciliation_item_ext(
                &mut tx,
                run_id,
                "missing_local",
                None,
                Some(&invalid_tx_id),
                None,
                Some(row.cod_amount_vnd),
                "invalid row: tracking_code is empty",
                "",
                order_code,
                line_number,
                "",
                "",
            )
            .await?;
            tx.commit().await?;
            return Ok(CodRowOutcome::MissingLocal(item));
        }

        let cod_tx_id = cod_reconcile_provider_tx_id(carrier_code, tracking_code);
        let mut tx = self.pool.begin().await?;

        // Serialize concurrent imports / queue resolutions for this (carrier, tracking_code)
        // BEFORE locking payment_shop_allocations so the global lock order remains strictly acyclic.
        lock_provider_tx_xact(&mut tx, &cod_tx_id).await?;

        // Primary lookup strictly by tracking_code on live COD allocations.
        let mut allocs = sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT a.*
            FROM payment_shop_allocations a
            JOIN payments p ON p.id = a.payment_id
            WHERE p.method = 'COD'
              AND p.status <> 'superseded'
              AND a.tracking_code = $1
            ORDER BY a.id ASC
            FOR UPDATE OF a
            "#,
        )
        .bind(tracking_code)
        .fetch_all(&mut *tx)
        .await?;

        // E20 fallback: `order_code` is used ONLY when the COD allocation does not yet store a tracking_code.
        if allocs.is_empty() && !order_code.is_empty() {
            allocs = sqlx::query_as::<_, ShopAllocationRecord>(
                r#"
                SELECT a.*
                FROM payment_shop_allocations a
                JOIN payments p ON p.id = a.payment_id
                WHERE p.method = 'COD'
                  AND p.status <> 'superseded'
                  AND COALESCE(BTRIM(a.tracking_code), '') = ''
                  AND (a.order_id = $1 OR a.order_code = $1)
                ORDER BY a.id ASC
                FOR UPDATE OF a
                "#,
            )
            .bind(order_code)
            .fetch_all(&mut *tx)
            .await?;
        }

        if allocs.is_empty() {
            enqueue_cod_reconciliation_item_tx(
                &mut tx,
                carrier_code,
                &cod_tx_id,
                tracking_code,
                None,
                (!order_code.is_empty()).then_some(order_code),
                None,
                row.cod_amount_vnd,
                "cod_missing_local",
                delivered_at,
            )
            .await?;
            let item = insert_reconciliation_item_ext(
                &mut tx,
                run_id,
                "missing_local",
                None,
                Some(&cod_tx_id),
                None,
                Some(row.cod_amount_vnd),
                "tracking_code not found in local COD allocations",
                tracking_code,
                order_code,
                line_number,
                "",
                "",
            )
            .await?;
            tx.commit().await?;
            return Ok(CodRowOutcome::MissingLocal(item));
        }

        let mut expected_payable_vnd = 0_i64;
        for alloc in &allocs {
            let sub_payable = allocation_buyer_payable_vnd(alloc)?;
            expected_payable_vnd =
                expected_payable_vnd
                    .checked_add(sub_payable)
                    .ok_or_else(|| {
                        PaymentError::LedgerInvariant("COD expected payable overflow".to_string())
                    })?;
        }
        let primary_payment_id = allocs[0].payment_id;
        let primary_order_id = allocs[0].order_id.clone();
        let primary_ch =
            ChannelAttribution::from_db(&allocs[0].sales_channel, &allocs[0].storefront_shop_id);

        // Reject carrier mismatch when the allocation already records a non-empty `carrier_code`
        // differing from the uploaded file's carrier (applies to both tracking_code and order_code).
        let stored_carriers: Vec<Option<&str>> =
            allocs.iter().map(|a| a.carrier_code.as_deref()).collect();
        if let Some(alloc_carrier) = find_cod_carrier_mismatch(&stored_carriers, carrier_code) {
            let details = format!(
                "carrier mismatch: allocation carrier='{alloc_carrier}' vs file carrier='{carrier_code}'"
            );
            enqueue_cod_reconciliation_item_tx(
                &mut tx,
                carrier_code,
                &cod_tx_id,
                tracking_code,
                Some(primary_payment_id),
                Some(&primary_order_id),
                Some(expected_payable_vnd),
                row.cod_amount_vnd,
                "cod_carrier_mismatch",
                delivered_at,
            )
            .await?;
            let item = insert_reconciliation_item_ext(
                &mut tx,
                run_id,
                "missing_local",
                Some(primary_payment_id),
                Some(&cod_tx_id),
                Some(expected_payable_vnd),
                Some(row.cod_amount_vnd),
                &details,
                tracking_code,
                &primary_order_id,
                line_number,
                primary_ch.sales_channel.as_str(),
                &primary_ch.storefront_shop_id,
            )
            .await?;
            tx.commit().await?;
            return Ok(CodRowOutcome::MissingLocal(item));
        }

        let escrow_statuses: Vec<&str> = allocs.iter().map(|a| a.escrow_status.as_str()).collect();
        let all_already_reconciled = allocs.iter().all(|a| a.cod_reconciled_at.is_some());

        match decide_cod_allocation_reconciliation(
            &escrow_statuses,
            expected_payable_vnd,
            row.cod_amount_vnd,
            all_already_reconciled,
        ) {
            CodAllocationMatchDecision::NotDelivered { cancelled } => {
                let (reason, details) = if cancelled {
                    (
                        "cod_order_cancelled",
                        "COD order allocation is cancelled; cannot reconcile",
                    )
                } else {
                    (
                        "cod_not_delivered",
                        "COD order not yet delivered (awaiting_collection)",
                    )
                };
                enqueue_cod_reconciliation_item_tx(
                    &mut tx,
                    carrier_code,
                    &cod_tx_id,
                    tracking_code,
                    Some(primary_payment_id),
                    Some(&primary_order_id),
                    Some(expected_payable_vnd),
                    row.cod_amount_vnd,
                    reason,
                    delivered_at,
                )
                .await?;
                let item = insert_reconciliation_item_ext(
                    &mut tx,
                    run_id,
                    "not_delivered",
                    Some(primary_payment_id),
                    Some(&cod_tx_id),
                    Some(expected_payable_vnd),
                    Some(row.cod_amount_vnd),
                    details,
                    tracking_code,
                    &primary_order_id,
                    line_number,
                    primary_ch.sales_channel.as_str(),
                    &primary_ch.storefront_shop_id,
                )
                .await?;
                tx.commit().await?;
                Ok(CodRowOutcome::NotDelivered(item))
            }
            CodAllocationMatchDecision::AmountMismatch => {
                // Always key `cod_amount_mismatch` with the reported amount so multiple distinct
                // mismatch amounts for the same tracking_code never overwrite each other, while
                // keeping replays idempotent and deduplicating any legacy open base-key mismatch row.
                let mismatch_tx_id = cod_reconcile_mismatch_provider_tx_id(
                    carrier_code,
                    tracking_code,
                    row.cod_amount_vnd,
                );
                let has_legacy_open_same_amount: bool = sqlx::query_scalar(
                    r#"
                    SELECT EXISTS(
                        SELECT 1
                        FROM reconciliation_queue
                        WHERE provider_tx_id = $1
                          AND reason = 'cod_amount_mismatch'
                          AND actual_amount_vnd = $2
                          AND resolution_status IN ('pending', 'suspended')
                    )
                    "#,
                )
                .bind(&cod_tx_id)
                .bind(row.cod_amount_vnd)
                .fetch_one(&mut *tx)
                .await?;
                let queue_tx_id = if has_legacy_open_same_amount {
                    &cod_tx_id
                } else {
                    &mismatch_tx_id
                };
                let details = format!(
                    "COD amount mismatch: local={} VND vs carrier={} VND",
                    expected_payable_vnd, row.cod_amount_vnd
                );
                enqueue_cod_reconciliation_item_tx(
                    &mut tx,
                    carrier_code,
                    queue_tx_id,
                    tracking_code,
                    Some(primary_payment_id),
                    Some(&primary_order_id),
                    Some(expected_payable_vnd),
                    row.cod_amount_vnd,
                    "cod_amount_mismatch",
                    delivered_at,
                )
                .await?;
                let item = insert_reconciliation_item_ext(
                    &mut tx,
                    run_id,
                    "amount_mismatch",
                    Some(primary_payment_id),
                    Some(queue_tx_id),
                    Some(expected_payable_vnd),
                    Some(row.cod_amount_vnd),
                    &details,
                    tracking_code,
                    &primary_order_id,
                    line_number,
                    primary_ch.sales_channel.as_str(),
                    &primary_ch.storefront_shop_id,
                )
                .await?;
                tx.commit().await?;
                Ok(CodRowOutcome::AmountMismatch(item))
            }
            CodAllocationMatchDecision::AlreadyReconciled => {
                // Only auto-close stale pre-delivery / missing_local queue entries created BEFORE
                // this run; NEVER close `cod_amount_mismatch` or `cod_carrier_mismatch` rows.
                auto_close_cod_reconciliation_queue_tx(
                    &mut tx,
                    carrier_code,
                    &cod_tx_id,
                    tracking_code,
                    primary_payment_id,
                    expected_payable_vnd,
                    "auto_closed:cod_already_reconciled",
                    Some(&["cod_not_delivered", "cod_missing_local"]),
                    run_created_at,
                )
                .await?;
                let item = insert_reconciliation_item_ext(
                    &mut tx,
                    run_id,
                    "already_reconciled",
                    Some(primary_payment_id),
                    Some(&cod_tx_id),
                    Some(expected_payable_vnd),
                    Some(row.cod_amount_vnd),
                    "COD allocation already reconciled previously",
                    tracking_code,
                    &primary_order_id,
                    line_number,
                    primary_ch.sales_channel.as_str(),
                    &primary_ch.storefront_shop_id,
                )
                .await?;
                tx.commit().await?;
                Ok(CodRowOutcome::AlreadyReconciled(item))
            }
            CodAllocationMatchDecision::MatchAndPost => {
                // Aggregate per `(payment_id, order_id, shop_id)` so multi-shop COD orders post
                // deterministic `cod-reconcile:{payment_id}:{order_id}:{shop_id}` entries without collisions.
                let mut grouped: Vec<(Uuid, String, String, i64, ChannelAttribution)> = Vec::new();
                for alloc in &allocs {
                    let sub_payable = allocation_buyer_payable_vnd(alloc)?;
                    if let Some(entry) = grouped.iter_mut().find(|(pid, oid, sid, _, _)| {
                        *pid == alloc.payment_id && oid == &alloc.order_id && sid == &alloc.shop_id
                    }) {
                        entry.3 = entry.3.checked_add(sub_payable).ok_or_else(|| {
                            PaymentError::LedgerInvariant("COD reconcile overflow".to_string())
                        })?;
                    } else {
                        grouped.push((
                            alloc.payment_id,
                            alloc.order_id.clone(),
                            alloc.shop_id.clone(),
                            sub_payable,
                            ChannelAttribution::from_db(
                                &alloc.sales_channel,
                                &alloc.storefront_shop_id,
                            ),
                        ));
                    }
                }

                let mut posted_any = false;
                let mut posted_vnd = 0_i64;
                for (pid, oid, sid, amt_vnd, ch) in &grouped {
                    if *amt_vnd > 0 {
                        let draft = build_cod_reconcile_tx(*pid, oid, sid, *amt_vnd, ch.clone());
                        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
                            self.metrics.record_ledger_tx("cod_reconcile");
                            posted_any = true;
                            posted_vnd = posted_vnd.saturating_add(*amt_vnd);
                        }
                    }
                }

                for alloc in &allocs {
                    sqlx::query(
                        r#"
                        UPDATE payment_shop_allocations
                        SET cod_reconciled_at = COALESCE(cod_reconciled_at, NOW()),
                            cod_reconciliation_run_id = COALESCE(cod_reconciliation_run_id, $1),
                            tracking_code = COALESCE(NULLIF(BTRIM(tracking_code), ''), $2),
                            carrier_code = COALESCE(NULLIF(BTRIM(carrier_code), ''), $3)
                        WHERE id = $4
                        "#,
                    )
                    .bind(run_id)
                    .bind(tracking_code)
                    .bind(carrier_code)
                    .bind(alloc.id)
                    .execute(&mut *tx)
                    .await?;
                }

                // E20-bis: automatically close open COD queue rows for this (carrier, tracking_code)
                // created strictly BEFORE the current run (rows emitted by earlier lines of the
                // same file stay `pending`, and `cod_carrier_mismatch` is never auto-closed).
                let close_reasons: Option<&[&str]> = if posted_any {
                    None
                } else {
                    Some(&["cod_not_delivered", "cod_missing_local"])
                };
                auto_close_cod_reconciliation_queue_tx(
                    &mut tx,
                    carrier_code,
                    &cod_tx_id,
                    tracking_code,
                    primary_payment_id,
                    expected_payable_vnd,
                    if posted_any {
                        "auto_closed:cod_file_matched"
                    } else {
                        "auto_closed:cod_already_reconciled"
                    },
                    close_reasons,
                    run_created_at,
                )
                .await?;

                if posted_any {
                    let item = insert_reconciliation_item_ext(
                        &mut tx,
                        run_id,
                        "matched",
                        Some(primary_payment_id),
                        Some(&cod_tx_id),
                        Some(expected_payable_vnd),
                        Some(row.cod_amount_vnd),
                        "matched and cleared cod_receivable into gateway_clearing",
                        tracking_code,
                        &primary_order_id,
                        line_number,
                        primary_ch.sales_channel.as_str(),
                        &primary_ch.storefront_shop_id,
                    )
                    .await?;
                    tx.commit().await?;
                    Ok(CodRowOutcome::Matched {
                        item,
                        matched_vnd: posted_vnd,
                    })
                } else {
                    let item = insert_reconciliation_item_ext(
                        &mut tx,
                        run_id,
                        "already_reconciled",
                        Some(primary_payment_id),
                        Some(&cod_tx_id),
                        Some(expected_payable_vnd),
                        Some(row.cod_amount_vnd),
                        "COD ledger entry already posted previously",
                        tracking_code,
                        &primary_order_id,
                        line_number,
                        primary_ch.sales_channel.as_str(),
                        &primary_ch.storefront_shop_id,
                    )
                    .await?;
                    tx.commit().await?;
                    Ok(CodRowOutcome::AlreadyReconciled(item))
                }
            }
        }
    }

    /// Lists paginated reconciliation runs filtered optionally by `run_type` (`"vietqr"` | `"cod_file"`)
    /// and `sales_channel` (`"MARKETPLACE"` | `"STOREFRONT"`).
    pub async fn list_reconciliation_runs(
        &self,
        filter: ListReconciliationRunsFilter,
    ) -> Result<ListReconciliationRunsResult> {
        let page = filter.page.max(1);
        let page_size = if filter.page_size <= 0 {
            20
        } else {
            filter.page_size.min(100)
        };
        let offset = i64::from(page - 1) * i64::from(page_size);
        let channel_str = filter.sales_channel.map(SalesChannel::as_str);

        let total: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM reconciliation_runs
            WHERE ($1::TEXT IS NULL OR run_type = $1)
              AND ($2::TEXT IS NULL OR sales_channel = $2)
            "#,
        )
        .bind(filter.run_type.as_deref())
        .bind(channel_str)
        .fetch_one(&self.pool)
        .await?;

        let runs = sqlx::query_as::<_, ReconciliationRunRecord>(
            r#"
            SELECT *
            FROM reconciliation_runs
            WHERE ($1::TEXT IS NULL OR run_type = $1)
              AND ($2::TEXT IS NULL OR sales_channel = $2)
            ORDER BY created_at DESC, id DESC
            LIMIT $3 OFFSET $4
            "#,
        )
        .bind(filter.run_type.as_deref())
        .bind(channel_str)
        .bind(i64::from(page_size))
        .bind(offset)
        .fetch_all(&self.pool)
        .await?;

        Ok(ListReconciliationRunsResult { runs, total })
    }

    /// Lists historical reconciliation runs ordered newest-first (thin alias kept for phase-0b clients).
    pub async fn list_reconciliation_reports(
        &self,
        limit: i32,
        channel_filter: Option<SalesChannel>,
    ) -> Result<Vec<ReconciliationRunRecord>> {
        let effective_limit = if limit <= 0 { 30 } else { limit.min(100) } as i64;
        let filter_str = channel_filter.map(SalesChannel::as_str);
        let rows = sqlx::query_as::<_, ReconciliationRunRecord>(
            r#"
            SELECT *
            FROM reconciliation_runs
            WHERE ($1::TEXT IS NULL OR sales_channel = $1)
            ORDER BY created_at DESC, id DESC
            LIMIT $2
            "#,
        )
        .bind(filter_str)
        .bind(effective_limit)
        .fetch_all(&self.pool)
        .await?;
        Ok(rows)
    }

    /// Marks stale `cod_file` reconciliation runs stuck in `'running'` longer than `stale_after`
    /// (e.g. after an abrupt process shutdown) as `'failed'` with `completed_at = NOW()`.
    pub async fn fail_stale_cod_reconciliation_runs(&self, stale_after: Duration) -> Result<u64> {
        let timeout_secs = stale_after.num_seconds().max(0) as f64;
        let res = sqlx::query(
            r#"
            UPDATE reconciliation_runs
            SET status = 'failed',
                completed_at = NOW()
            WHERE run_type = 'cod_file'
              AND status = 'running'
              AND created_at <= NOW() - make_interval(secs => $1)
            "#,
        )
        .bind(timeout_secs)
        .execute(&self.pool)
        .await?;

        let affected = res.rows_affected();
        if affected > 0 {
            warn!(
                stale_runs_failed = affected,
                timeout_secs = stale_after.num_seconds().max(0),
                "marked stale running COD reconciliation runs as failed"
            );
        }
        Ok(affected)
    }

    /// Atomically claims `event_id` in `processed_nats_events` and settles `order.completed`
    /// escrow to `seller_pending:<shop_id>` in the SAME PostgreSQL transaction (docs/contracts.md:269).
    /// review2-P P1-2: because `order.completed` is only emitted after `DELIVERED -> COMPLETED`,
    /// idempotently collects any still-`awaiting_collection` COD allocations right before settling
    /// so lost/delayed `order.delivered` + failed best-effort `ReleaseEscrow` never strand seller funds in DLQ.
    pub async fn process_order_completed_event(
        &self,
        event_id: &str,
        order_id: &str,
        trace_id: &str,
    ) -> Result<bool> {
        let mut tx = self.pool.begin().await?;
        if !claim_nats_event_tx(&mut tx, event_id, "order.completed").await? {
            tx.commit().await?;
            return Ok(false);
        }

        let cod_sse = collect_cod_order_allocations_tx(
            &mut tx,
            order_id,
            None,
            None,
            trace_id,
            &self.metrics,
        )
        .await?;
        let _ = settle_order_to_pending_tx(&mut tx, order_id, None, &self.metrics).await?;
        tx.commit().await?;
        if let Some(ref u) = cod_sse {
            self.emit_sse(u);
        }
        Ok(true)
    }

    /// Atomically claims `event_id` in `processed_nats_events` and records COD cash collection
    /// on `order.delivered` (R2 / PAY-B4), while persisting `tracking_code`, `carrier_code`,
    /// and `order_code` onto `payment_shop_allocations` for E20 COD carrier reconciliation:
    /// - Posts Debit `cod_receivable` / Credit `escrow_liability` for this sub-order's `P`.
    /// - Transitions this sub-order's allocation `awaiting_collection -> held`.
    /// - Emits `payment.succeeded` for the delivered sub-order.
    /// - Transitions parent COD payment to `'succeeded'` once all active allocations are collected.
    pub async fn process_order_delivered_event_with_shipping(
        &self,
        event_id: &str,
        order_id: &str,
        tracking_code: Option<&str>,
        carrier_code: Option<&str>,
        order_code: Option<&str>,
        trace_id: &str,
    ) -> Result<bool> {
        let mut tx = self.pool.begin().await?;
        if !claim_nats_event_tx(&mut tx, event_id, "order.delivered").await? {
            tx.commit().await?;
            return Ok(false);
        }

        let shipping_meta = CodShippingMeta::from_opts(tracking_code, carrier_code, order_code);
        let sse_updated = collect_cod_order_allocations_tx(
            &mut tx,
            order_id,
            None,
            shipping_meta.as_ref(),
            trace_id,
            &self.metrics,
        )
        .await?;

        tx.commit().await?;
        if let Some(ref u) = sse_updated {
            self.emit_sse(u);
        }
        Ok(true)
    }

    /// Atomically claims `event_id` in `processed_nats_events` and handles `order.returned` (R2 / Wave 2a):
    /// - Uncollected COD (`awaiting_collection`) or zero-payable (`P == 0`, `held`) allocations for
    ///   both `SHIPPING` and `RETURN_REQUESTED`: marks allocation `'cancelled'` and syncs parent
    ///   payment status with zero ledger postings.
    /// - Paid collected allocations (`P > 0`, `held`) or already-`refunded` allocations: no-op
    ///   (for `RETURN_REQUESTED`, `order` calls idempotent `CreateRefund` for remaining `P - refunded_vnd`,
    ///   moving allocation to `'refunded'`, so no double booking occurs regardless of arrival order).
    pub async fn process_order_returned_event(
        &self,
        event_id: &str,
        order_id: &str,
        from_status: Option<&str>,
        trace_id: &str,
    ) -> Result<bool> {
        let mut tx = self.pool.begin().await?;
        if !claim_nats_event_tx(&mut tx, event_id, "order.returned").await? {
            tx.commit().await?;
            return Ok(false);
        }

        let target_meta: Option<(Uuid, String, String)> = sqlx::query_as(
            r#"
            SELECT p.id, p.order_id, p.order_group_id
            FROM payments p
            LEFT JOIN payment_shop_allocations a ON a.payment_id = p.id
            WHERE (a.order_id = $1 OR p.order_id = $1)
              AND p.status IN ('pending', 'succeeded')
            ORDER BY
                CASE p.status
                    WHEN 'succeeded' THEN 1
                    WHEN 'pending' THEN 2
                    ELSE 3
                END,
                p.created_at DESC
            LIMIT 1
            "#,
        )
        .bind(order_id)
        .fetch_optional(&mut *tx)
        .await?;

        let Some((target_pid, pay_order_id, pay_group_id)) = target_meta else {
            tx.commit().await?;
            return Ok(true);
        };

        let group = lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;
        let Some(payment) = group
            .into_iter()
            .find(|p| p.id == target_pid && matches!(p.status.as_str(), "pending" | "succeeded"))
        else {
            tx.commit().await?;
            return Ok(true);
        };

        let allocs = sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT * FROM payment_shop_allocations
            WHERE payment_id = $1 AND order_id = $2
            ORDER BY id ASC
            FOR UPDATE
            "#,
        )
        .bind(payment.id)
        .bind(order_id)
        .fetch_all(&mut *tx)
        .await?;

        let mut cancelled_any = false;
        let mut need_cod_collect = false;
        let mut sse_updated: Option<PaymentRecord> = None;

        if allocs.is_empty() && payment.method == "COD" && payment.status == "pending" {
            // P2-6: Legacy single-order COD without payment_shop_allocations rows.
            let total_allocs: i64 = sqlx::query_scalar(
                "SELECT COUNT(*)::BIGINT FROM payment_shop_allocations WHERE payment_id = $1",
            )
            .bind(payment.id)
            .fetch_one(&mut *tx)
            .await?;

            if total_allocs == 0 {
                match decide_return_allocation_action(
                    &payment.method,
                    from_status,
                    "awaiting_collection",
                    payment.amount_vnd,
                ) {
                    ReturnAllocationAction::CollectCodForRefund => {
                        need_cod_collect = true;
                    }
                    ReturnAllocationAction::CancelAllocation => {
                        sse_updated = sqlx::query_as::<_, PaymentRecord>(
                            r#"
                            UPDATE payments
                            SET status = 'cancelled', updated_at = NOW()
                            WHERE id = $1 AND status = 'pending'
                            RETURNING *
                            "#,
                        )
                        .bind(payment.id)
                        .fetch_optional(&mut *tx)
                        .await?;
                    }
                    ReturnAllocationAction::DeferPostDeliveryReturn => {}
                }
            }
        }

        for alloc in allocs {
            let buyer_payable = allocation_buyer_payable_vnd(&alloc)?;
            match decide_return_allocation_action(
                &payment.method,
                from_status,
                &alloc.escrow_status,
                buyer_payable,
            ) {
                ReturnAllocationAction::CancelAllocation => {
                    sqlx::query(
                        r#"
                        UPDATE payment_shop_allocations
                        SET escrow_status = 'cancelled'
                        WHERE id = $1 AND escrow_status IN ('awaiting_collection', 'held')
                        "#,
                    )
                    .bind(alloc.id)
                    .execute(&mut *tx)
                    .await?;
                    cancelled_any = true;
                }
                ReturnAllocationAction::CollectCodForRefund => {
                    // Manager review P1-1: post-delivery return (`RETURN_REQUESTED`) on P > 0 COD
                    // means buyer already paid cash (`order.delivered` delayed/DLQ); collect COD
                    // into `held` idempotently so `order`'s `CreateRefund` succeeds.
                    need_cod_collect = true;
                }
                ReturnAllocationAction::DeferPostDeliveryReturn => {
                    // Paid collected order (`P > 0`, handled by `order`'s idempotent `CreateRefund`)
                    // or already refunded/cancelled allocation -> no-op to avoid double booking.
                    info!(
                        trace_id = %trace_id,
                        payment_id = %payment.id,
                        order_id = %order_id,
                        from_status = ?from_status,
                        escrow_status = %alloc.escrow_status,
                        "order.returned requires no additional consumer mutation for allocation"
                    );
                }
            }
        }

        if need_cod_collect {
            sse_updated = collect_cod_order_allocations_tx(
                &mut tx,
                order_id,
                None,
                None,
                trace_id,
                &self.metrics,
            )
            .await?;
        }
        if cancelled_any {
            if payment.method == "COD" && payment.status == "pending" {
                let current_status = sse_updated
                    .as_ref()
                    .map(|p| p.status.as_str())
                    .unwrap_or(payment.status.as_str());
                if let Some(u) =
                    sync_cod_payment_status_tx(&mut tx, payment.id, current_status).await?
                {
                    sse_updated = Some(u);
                }
            } else if payment.status == "succeeded" {
                // review2-P P3-9 + ponytail: handles both 0 VND payment -> cancelled AND
                // multi-shop VietQR (paid sibling 100% refunded + 0 VND sub-order returned) -> refunded.
                if let Some(u) =
                    sync_succeeded_payment_after_alloc_cancel_tx(&mut tx, &payment).await?
                {
                    sse_updated = Some(u);
                }
            }
        }

        tx.commit().await?;
        if let Some(ref u) = sse_updated {
            self.emit_sse(u);
        }
        Ok(true)
    }

    /// Atomically claims `event_id` in `processed_nats_events` and processes `order.cancelled`
    /// in the SAME PostgreSQL transaction:
    /// - Pending COD: cancels uncollected allocation (`awaiting_collection -> cancelled`) with zero
    ///   ledger entries; transitions payment to `'cancelled'`, `'succeeded'`, or `'refunded'` (P2-9).
    /// - Pending VietQR: cancels ALL awaiting/held allocations and transitions payment -> `'cancelled'`
    ///   immediately on the first `order.cancelled` (P0-3) because a VietQR intent collects the whole
    ///   checkout group as one lump sum and cannot be partially confirmed.
    /// - Succeeded 0 VND: marks cancelled sub-order allocation `'cancelled'` so it cannot be settled.
    /// - Succeeded VietQR: refunds ONLY the cancelled sub-order's buyer-payable amount (`R + Ship - D`),
    ///   never sibling shops and excluding late-payment refunds.
    pub async fn process_order_cancelled_event(
        &self,
        event_id: &str,
        order_id: &str,
        trace_id: &str,
    ) -> Result<bool> {
        let mut tx = self.pool.begin().await?;
        if !claim_nats_event_tx(&mut tx, event_id, "order.cancelled").await? {
            tx.commit().await?;
            return Ok(false);
        }

        // review2-P P1-3: locate checkout group read-only first, then lock the entire group in
        // ascending `id` order (`lock_payment_group_tx`). If all visible rows are `superseded`
        // (Read Committed EPQ race with concurrent `create_payment(supersede_previous=true)`),
        // re-lock once with a fresh statement snapshot to pick up the newly inserted `P2`, or
        // return `Conflict` so `processed_nats_events` rolls back and JetStream retries.
        let group_keys: Option<(String, String)> = sqlx::query_as(
            r#"
            SELECT p.order_id, p.order_group_id
            FROM payments p
            LEFT JOIN payment_shop_allocations a ON a.payment_id = p.id
            WHERE p.order_id = $1 OR p.order_group_id = $1 OR a.order_id = $1
            ORDER BY p.created_at DESC
            LIMIT 1
            "#,
        )
        .bind(order_id)
        .fetch_optional(&mut *tx)
        .await?;

        let Some((pay_order_id, pay_group_id)) = group_keys else {
            tx.commit().await?;
            return Ok(true);
        };

        let mut group = lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;
        if select_payment_for_group_action(&group) == GroupPaymentSelection::AllSupersededRetry {
            group = lock_payment_group_tx(&mut tx, &pay_order_id, &pay_group_id).await?;
        }
        let payment = match select_payment_for_group_action(&group) {
            GroupPaymentSelection::Found(idx) => group.swap_remove(idx),
            GroupPaymentSelection::Empty => {
                tx.commit().await?;
                return Ok(true);
            }
            GroupPaymentSelection::AllSupersededRetry => {
                return Err(PaymentError::Conflict(format!(
                    "payment for order {order_id} was concurrently superseded; retrying cancellation"
                )));
            }
        };

        let mut sse_updated: Option<PaymentRecord> = None;

        let mut zalopay_auto_refund_id: Option<Uuid> = None;
        match payment.status.as_str() {
            "pending" if payment.method == "COD" => {
                if payment.order_id == order_id || payment.order_group_id == order_id {
                    sqlx::query(
                        r#"
                        UPDATE payment_shop_allocations
                        SET escrow_status = 'cancelled'
                        WHERE payment_id = $1 AND escrow_status = 'awaiting_collection'
                        "#,
                    )
                    .bind(payment.id)
                    .execute(&mut *tx)
                    .await?;
                } else {
                    sqlx::query(
                        r#"
                        UPDATE payment_shop_allocations
                        SET escrow_status = 'cancelled'
                        WHERE payment_id = $1
                          AND order_id = $2
                          AND escrow_status = 'awaiting_collection'
                        "#,
                    )
                    .bind(payment.id)
                    .bind(order_id)
                    .execute(&mut *tx)
                    .await?;
                }

                sse_updated =
                    sync_cod_payment_status_tx(&mut tx, payment.id, &payment.status).await?;
            }
            "pending" => {
                // P0-3: Pending VIETQR collects the whole group as one lump sum (`P1 + P2`), so
                // cancelling any sub-order cancels the entire pending QR intent immediately so a
                // racing webhook `SUCCEEDED` triggers `LatePaymentRefund` for the full amount.
                sqlx::query(
                    r#"
                    UPDATE payment_shop_allocations
                    SET escrow_status = 'cancelled'
                    WHERE payment_id = $1
                      AND escrow_status IN ('awaiting_collection', 'held')
                    "#,
                )
                .bind(payment.id)
                .execute(&mut *tx)
                .await?;

                let updated = sqlx::query_as::<_, PaymentRecord>(
                    r#"
                    UPDATE payments
                    SET status = 'cancelled', updated_at = NOW()
                    WHERE id = $1
                    RETURNING *
                    "#,
                )
                .bind(payment.id)
                .fetch_one(&mut *tx)
                .await?;
                sse_updated = Some(updated);
            }
            "succeeded" if payment.amount_vnd == 0 => {
                // 0 VND fully-subsidized order cancelled: cancel allocation so it cannot settle.
                sqlx::query(
                    r#"
                    UPDATE payment_shop_allocations
                    SET escrow_status = 'cancelled'
                    WHERE payment_id = $1 AND order_id = $2 AND escrow_status = 'held'
                    "#,
                )
                .bind(payment.id)
                .bind(order_id)
                .execute(&mut *tx)
                .await?;

                sse_updated =
                    sync_succeeded_payment_after_alloc_cancel_tx(&mut tx, &payment).await?;
            }
            "succeeded" if is_online_method(&payment.method) => {
                let already_refunded: i64 = sqlx::query_scalar(
                    r#"
                    SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
                    FROM refunds
                    WHERE payment_id = $1
                      AND status IN ('pending', 'completed', 'manual_review')
                      AND affects_escrow
                      AND NOT is_late_payment
                    "#,
                )
                .bind(payment.id)
                .fetch_one(&mut *tx)
                .await?;

                // D10 (N11): Lock allocations FOR UPDATE inside the same transaction so settlement
                // status cannot race with `order.completed` while deciding pre-settlement auto-refund.
                let locked_allocs = sqlx::query_as::<_, ShopAllocationRecord>(
                    r#"
                    SELECT *
                    FROM payment_shop_allocations
                    WHERE payment_id = $1
                    ORDER BY id ASC
                    FOR UPDATE
                    "#,
                )
                .bind(payment.id)
                .fetch_all(&mut *tx)
                .await?;

                let total_alloc_count = locked_allocs.len() as i64;
                let mut suborder_held_count: i64 = 0;
                let mut suborder_alloc_total: i64 = 0;
                for alloc in &locked_allocs {
                    if alloc.order_id == order_id && alloc.escrow_status == "held" {
                        suborder_held_count += 1;
                        let payable = allocation_buyer_payable_vnd(alloc)?;
                        suborder_alloc_total =
                            suborder_alloc_total.checked_add(payable).ok_or_else(|| {
                                PaymentError::LedgerInvariant(
                                    "suborder allocation total overflow".to_string(),
                                )
                            })?;
                    }
                }

                let suborder_already_refunded = sum_suborder_refunded_tx(
                    &mut tx,
                    payment.id,
                    order_id,
                    &payment.order_id,
                    total_alloc_count == 1,
                )
                .await?;

                let suborder_remaining_alloc =
                    suborder_alloc_total.saturating_sub(suborder_already_refunded);

                let refund_amount = compute_suborder_refund_amount(
                    payment.amount_vnd,
                    already_refunded,
                    total_alloc_count > 0,
                    suborder_remaining_alloc,
                );

                if refund_amount == 0 && suborder_held_count > 0 {
                    // 0 VND buyer-payable sub-order inside a multi-shop payment -> cancel allocation.
                    sqlx::query(
                        r#"
                        UPDATE payment_shop_allocations
                        SET escrow_status = 'cancelled'
                        WHERE payment_id = $1 AND order_id = $2 AND escrow_status = 'held'
                        "#,
                    )
                    .bind(payment.id)
                    .bind(order_id)
                    .execute(&mut *tx)
                    .await?;

                    sse_updated =
                        sync_succeeded_payment_after_alloc_cancel_tx(&mut tx, &payment).await?;
                } else if refund_amount > 0 {
                    // Deterministic key matching OrderService.CancelOrder ("order-cancel-refund:<order_id>")
                    // so sync CreateRefund and async order.cancelled deduplicate regardless of order.
                    let refund_key = order_cancel_refund_key(order_id);
                    let refund_id = Uuid::new_v4();
                    let (provider_m_refund_id, provider_status) = if payment.method == "ZALOPAY" {
                        let app_id = self.zalopay.effective_app_id()?;
                        (
                            Some(generate_m_refund_id(app_id, refund_id, Utc::now())),
                            Some(REFUND_PROVIDER_STATUS_PENDING),
                        )
                    } else {
                        (None, None)
                    };
                    let inserted_refund = sqlx::query_as::<_, RefundRecord>(
                        r#"
                        INSERT INTO refunds (
                            id, payment_id, order_id, amount_vnd, reason, status, idempotency_key,
                            provider_m_refund_id, provider_status,
                            created_by_service, is_late_payment, affects_escrow
                        )
                        VALUES ($1, $2, $3, $4, 'order_cancelled_auto_refund', 'pending', $5, $6, $7, TRUE, FALSE, TRUE)
                        ON CONFLICT (idempotency_key) DO NOTHING
                        RETURNING *
                        "#,
                    )
                    .bind(refund_id)
                    .bind(payment.id)
                    .bind(order_id)
                    .bind(refund_amount)
                    .bind(&refund_key)
                    .bind(provider_m_refund_id.as_deref())
                    .bind(provider_status)
                    .fetch_optional(&mut *tx)
                    .await?;

                    if let Some(refund) = inserted_refund {
                        if payment.method == "ZALOPAY" {
                            zalopay_auto_refund_id = Some(refund.id);
                        }
                        let channel =
                            resolve_refund_channel_tx(&mut tx, &payment, order_id).await?;
                        let draft = build_refund_tx(
                            refund.id,
                            payment.id,
                            refund.amount_vnd,
                            &refund.reason,
                            channel,
                        );
                        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
                            self.metrics.record_ledger_tx("refund");
                        }

                        // Mark ONLY the cancelled sub-order's allocation as refunded
                        sqlx::query(
                            r#"
                            UPDATE payment_shop_allocations
                            SET escrow_status = 'refunded'
                            WHERE payment_id = $1 AND order_id = $2 AND escrow_status = 'held'
                            "#,
                        )
                        .bind(payment.id)
                        .bind(order_id)
                        .execute(&mut *tx)
                        .await?;

                        let remaining_active_allocs: i64 = sqlx::query_scalar(
                            r#"
                            SELECT COUNT(*)::BIGINT
                            FROM payment_shop_allocations
                            WHERE payment_id = $1
                              AND escrow_status IN ('awaiting_collection', 'held', 'pending_release')
                            "#,
                        )
                        .bind(payment.id)
                        .fetch_one(&mut *tx)
                        .await?;

                        let remaining_after = payment
                            .amount_vnd
                            .saturating_sub(already_refunded.saturating_add(refund.amount_vnd));

                        if remaining_after == 0 && remaining_active_allocs == 0 {
                            sse_updated = sqlx::query_as::<_, PaymentRecord>(
                                "UPDATE payments SET status = 'refunded', updated_at = NOW() WHERE id = $1 RETURNING *",
                            )
                            .bind(payment.id)
                            .fetch_optional(&mut *tx)
                            .await?;
                        }

                        // Auto-cancel refund only touches `held` (pre-settlement) escrow or legacy
                        // unsettled payments, verified under `FOR UPDATE` above -> `is_post_settlement = false`.
                        insert_outbox_event(
                            &mut tx,
                            "payment.refunded",
                            &payment.order_group_id,
                            trace_id,
                            build_payment_refunded_payload(&refund, &payment, order_id, false),
                        )
                        .await?;
                    }
                }
            }
            _ => {}
        }

        tx.commit().await?;
        if let Some(ref updated) = sse_updated {
            self.emit_sse(updated);
        }
        if let Some(refund_id) = zalopay_auto_refund_id {
            let _ = self
                .dispatch_zalopay_refund_best_effort(refund_id, trace_id)
                .await;
        }
        Ok(true)
    }
}

#[derive(Debug, Clone, sqlx::FromRow)]
struct ZaloPayRefundClaimRow {
    id: Uuid,
    payment_id: Uuid,
    amount_vnd: i64,
    reason: String,
    idempotency_key: String,
    provider_m_refund_id: Option<String>,
    provider_status: Option<String>,
    provider_dispatch_attempted: bool,
    provider_tx_id: Option<String>,
    payment_order_id: String,
    payment_order_group_id: String,
    payment_user_id: String,
    payment_sales_channel: String,
    payment_storefront_shop_id: String,
}

/// Checks whether a duplicate `webhook_events.provider_tx_id` belongs to the SAME provider and
/// target payment/order_ref (true replay) vs a cross-provider or cross-payment collision (p4 Item G).
fn is_same_webhook_target(
    existing_provider: &str,
    existing_payment_id: Option<Uuid>,
    existing_order_ref: Option<&str>,
    incoming_provider: &str,
    incoming_payment_id: Option<Uuid>,
    incoming_order_ref: Option<&str>,
) -> bool {
    if !existing_provider
        .trim()
        .eq_ignore_ascii_case(incoming_provider.trim())
    {
        return false;
    }
    match (existing_payment_id, incoming_payment_id) {
        (Some(ep), Some(ip)) => return ep == ip,
        (Some(_), None) => {
            // Existing event bound to a payment, incoming failed to match a payment:
            // treat as duplicate only when order_ref matches (or incoming omitted order_ref).
            if let (Some(er), Some(ir)) = (
                existing_order_ref.map(str::trim).filter(|s| !s.is_empty()),
                incoming_order_ref.map(str::trim).filter(|s| !s.is_empty()),
            ) {
                return er.eq_ignore_ascii_case(ir);
            }
            return incoming_order_ref.map(str::trim).is_none_or(str::is_empty);
        }
        (None, Some(_)) => {
            if let (Some(er), Some(ir)) = (
                existing_order_ref.map(str::trim).filter(|s| !s.is_empty()),
                incoming_order_ref.map(str::trim).filter(|s| !s.is_empty()),
            ) {
                return er.eq_ignore_ascii_case(ir);
            }
            return false;
        }
        (None, None) => {}
    }
    match (
        existing_order_ref.map(str::trim).filter(|s| !s.is_empty()),
        incoming_order_ref.map(str::trim).filter(|s| !s.is_empty()),
    ) {
        (Some(er), Some(ir)) => er.eq_ignore_ascii_case(ir),
        (None, None) => true,
        _ => false,
    }
}

fn resolve_zalopay_zp_trans_id(
    payment_provider_tx_id: Option<&str>,
    refund_idempotency_key: &str,
) -> Option<String> {
    if let Some(late_tx) = refund_idempotency_key.strip_prefix("late-webhook:") {
        let trimmed = late_tx.trim();
        if !trimmed.is_empty() {
            return Some(trimmed.to_string());
        }
    }
    payment_provider_tx_id
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(ToString::to_string)
}

// =============================================================================
// Internal Transaction Helpers
// =============================================================================

fn is_unique_violation(err: &sqlx::Error, constraint_substr: &str) -> bool {
    if let sqlx::Error::Database(db_err) = err {
        if db_err.code().as_deref() == Some("23505") {
            return db_err
                .constraint()
                .map(|c| c.contains(constraint_substr))
                .unwrap_or(true);
        }
    }
    false
}

fn map_recon_idempotency_conflict(err: sqlx::Error) -> PaymentError {
    if is_unique_violation(&err, "resolution_idempotency_key") {
        PaymentError::Conflict(
            "idempotency_key already used for a different reconciliation item".to_string(),
        )
    } else {
        PaymentError::Database(err)
    }
}

async fn claim_nats_event_tx(
    tx: &mut Transaction<'_, Postgres>,
    event_id: &str,
    event_type: &str,
) -> Result<bool> {
    let claimed: Option<String> = sqlx::query_scalar(
        r#"
        INSERT INTO processed_nats_events (event_id, event_type)
        VALUES ($1, $2)
        ON CONFLICT (event_id) DO NOTHING
        RETURNING event_id
        "#,
    )
    .bind(event_id)
    .bind(event_type)
    .fetch_optional(&mut **tx)
    .await?;
    Ok(claimed.is_some())
}

async fn lock_payment_group_tx(
    tx: &mut Transaction<'_, Postgres>,
    order_id: &str,
    order_group_id: &str,
) -> Result<Vec<PaymentRecord>> {
    let rows = sqlx::query_as::<_, PaymentRecord>(
        r#"
        SELECT * FROM payments
        WHERE order_id = $1 OR order_group_id = $2
        ORDER BY id ASC
        FOR UPDATE
        "#,
    )
    .bind(order_id)
    .bind(order_group_id)
    .fetch_all(&mut **tx)
    .await?;
    Ok(rows)
}

async fn has_cancelled_allocation_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment_id: Uuid,
) -> Result<bool> {
    let exists: bool = sqlx::query_scalar(
        r#"
        SELECT EXISTS(
            SELECT 1 FROM payment_shop_allocations
            WHERE payment_id = $1 AND escrow_status = 'cancelled'
        )
        "#,
    )
    .bind(payment_id)
    .fetch_one(&mut **tx)
    .await?;
    Ok(exists)
}

/// Validates an idempotent refund replay against the existing row (review-B P2-8 / review2-P P3-8).
/// For single-shop payments, allows `requested_order_id` to match `payment.order_group_id` or
/// `payment.order_id` even though `refunds.order_id` stores `alloc.order_id`.
async fn validate_existing_refund_replay<'e, E>(
    executor: E,
    existing: &RefundRecord,
    input: &CreateRefundInput,
) -> Result<()>
where
    E: sqlx::Executor<'e, Database = Postgres>,
{
    if validate_refund_idempotency_match(
        existing,
        input.payment_id,
        &input.order_id,
        input.amount_vnd,
        &[],
    )
    .is_ok()
    {
        return Ok(());
    }

    let aliases: Option<(String, String)> = sqlx::query_as(
        r#"
        SELECT p.order_id, p.order_group_id
        FROM payments p
        WHERE p.id = $1
          AND (SELECT COUNT(*)::BIGINT FROM payment_shop_allocations WHERE payment_id = p.id) <= 1
        "#,
    )
    .bind(existing.payment_id)
    .fetch_optional(executor)
    .await?;

    if let Some((pay_order_id, pay_group_id)) = aliases {
        return validate_refund_idempotency_match(
            existing,
            input.payment_id,
            &input.order_id,
            input.amount_vnd,
            &[pay_order_id.as_str(), pay_group_id.as_str()],
        );
    }

    validate_refund_idempotency_match(
        existing,
        input.payment_id,
        &input.order_id,
        input.amount_vnd,
        &[],
    )
}

/// Syncs a `succeeded` payment after a 0 VND allocation is cancelled (by `order.cancelled` or
/// failed-delivery `order.returned`) (review2-P P3-9 + ponytail):
/// - 0 VND full payment with 0 active allocations -> `'cancelled'`.
/// - Paid multi-shop payment with 0 active allocations and 100% refunded -> `'refunded'`.
async fn sync_succeeded_payment_after_alloc_cancel_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment: &PaymentRecord,
) -> Result<Option<PaymentRecord>> {
    if payment.amount_vnd == 0 {
        let remaining_active: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM payment_shop_allocations
            WHERE payment_id = $1
              AND escrow_status IN ('awaiting_collection', 'held', 'pending_release', 'released')
            "#,
        )
        .bind(payment.id)
        .fetch_one(&mut **tx)
        .await?;

        if remaining_active == 0 {
            let updated = sqlx::query_as::<_, PaymentRecord>(
                "UPDATE payments SET status = 'cancelled', updated_at = NOW() WHERE id = $1 RETURNING *",
            )
            .bind(payment.id)
            .fetch_one(&mut **tx)
            .await?;
            return Ok(Some(updated));
        }
        return Ok(None);
    }

    let remaining_active_allocs: i64 = sqlx::query_scalar(
        r#"
        SELECT COUNT(*)::BIGINT
        FROM payment_shop_allocations
        WHERE payment_id = $1
          AND escrow_status IN ('awaiting_collection', 'held', 'pending_release')
        "#,
    )
    .bind(payment.id)
    .fetch_one(&mut **tx)
    .await?;

    if remaining_active_allocs == 0 {
        let already_refunded: i64 = sqlx::query_scalar(
            r#"
            SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
            FROM refunds
            WHERE payment_id = $1
              AND status IN ('pending', 'completed', 'manual_review')
              AND affects_escrow
              AND NOT is_late_payment
            "#,
        )
        .bind(payment.id)
        .fetch_one(&mut **tx)
        .await?;

        if already_refunded >= payment.amount_vnd && already_refunded > 0 {
            let updated = sqlx::query_as::<_, PaymentRecord>(
                "UPDATE payments SET status = 'refunded', updated_at = NOW() WHERE id = $1 RETURNING *",
            )
            .bind(payment.id)
            .fetch_one(&mut **tx)
            .await?;
            return Ok(Some(updated));
        }
    }
    Ok(None)
}

/// Recomputes and updates parent COD payment status from its allocations in a single query.
/// Returns `Some(updated)` if status changed, or `None` if unchanged (R2 / P2-9 / ponytail).
async fn sync_cod_payment_status_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment_id: Uuid,
    current_status: &str,
) -> Result<Option<PaymentRecord>> {
    let (remaining_awaiting, collected_unrefunded, refunded_count): (i64, i64, i64) =
        sqlx::query_as(
            r#"
            SELECT
                COUNT(*) FILTER (WHERE escrow_status = 'awaiting_collection')::BIGINT,
                COUNT(*) FILTER (WHERE escrow_status IN ('held', 'pending_release', 'released'))::BIGINT,
                COUNT(*) FILTER (WHERE escrow_status = 'refunded')::BIGINT
            FROM payment_shop_allocations
            WHERE payment_id = $1
            "#,
        )
        .bind(payment_id)
        .fetch_one(&mut **tx)
        .await?;

    let next_status =
        resolve_cod_payment_status(remaining_awaiting, collected_unrefunded, refunded_count);
    if next_status == current_status {
        return Ok(None);
    }

    let updated = sqlx::query_as::<_, PaymentRecord>(
        r#"
        UPDATE payments
        SET status = $1,
            paid_at = CASE WHEN $1 = 'succeeded' THEN COALESCE(paid_at, NOW()) ELSE paid_at END,
            updated_at = NOW()
        WHERE id = $2
        RETURNING *
        "#,
    )
    .bind(next_status)
    .bind(payment_id)
    .fetch_one(&mut **tx)
    .await?;
    Ok(Some(updated))
}

enum CodRowOutcome {
    Matched {
        item: ReconciliationItemRecord,
        matched_vnd: i64,
    },
    AlreadyReconciled(ReconciliationItemRecord),
    MissingLocal(ReconciliationItemRecord),
    AmountMismatch(ReconciliationItemRecord),
    NotDelivered(ReconciliationItemRecord),
}

struct CodShippingMeta {
    tracking_code: Option<String>,
    carrier_code: Option<String>,
    order_code: Option<String>,
}

impl CodShippingMeta {
    fn from_opts(
        tracking_code: Option<&str>,
        carrier_code: Option<&str>,
        order_code: Option<&str>,
    ) -> Option<Self> {
        let tracking_code = tracking_code
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(ToString::to_string);
        let carrier_code = carrier_code
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(|s| s.to_ascii_lowercase());
        let order_code = order_code
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(ToString::to_string);
        if tracking_code.is_none() && carrier_code.is_none() && order_code.is_none() {
            None
        } else {
            Some(Self {
                tracking_code,
                carrier_code,
                order_code,
            })
        }
    }
}

/// Idempotently collects COD funds (`Debit cod_receivable / Credit escrow_liability`) for
/// `awaiting_collection` allocations matching `order_id` (and optional `shop_filter`), transitions
/// allocations to `held`, persists optional shipping tracking metadata (`E20`), emits
/// `payment.succeeded`, and syncs parent COD payment status.
/// Shared by `process_order_delivered_event_with_shipping`, `process_order_completed_event`,
/// `process_order_returned_event`, and `release_escrow` (P0-1).
/// Also accepts `payments.status = 'refunded'` purely to backfill shipping tracking metadata
/// when a delayed `order.delivered` arrives after a COD order was already returned & fully refunded,
/// without posting any collection ledger entry or `payment.succeeded` outbox event.
async fn collect_cod_order_allocations_tx(
    tx: &mut Transaction<'_, Postgres>,
    order_id: &str,
    shop_filter: Option<&str>,
    shipping_meta: Option<&CodShippingMeta>,
    trace_id: &str,
    metrics: &Metrics,
) -> Result<Option<PaymentRecord>> {
    let target_meta: Option<(Uuid, String, String)> = sqlx::query_as(
        r#"
        SELECT p.id, p.order_id, p.order_group_id
        FROM payments p
        LEFT JOIN payment_shop_allocations a ON a.payment_id = p.id
        WHERE (a.order_id = $1 OR p.order_id = $1 OR p.order_group_id = $1)
          AND p.method = 'COD'
          AND p.status IN ('pending', 'succeeded', 'refunded')
        ORDER BY p.created_at DESC
        LIMIT 1
        "#,
    )
    .bind(order_id)
    .fetch_optional(&mut **tx)
    .await?;

    let Some((target_pid, pay_order_id, pay_group_id)) = target_meta else {
        return Ok(None);
    };

    // D9-bis point 9: lock entire checkout payment group before inserting `payment.succeeded`.
    let group = lock_payment_group_tx(tx, &pay_order_id, &pay_group_id).await?;
    let Some(payment) = group.into_iter().find(|p| {
        p.id == target_pid
            && p.method == "COD"
            && matches!(p.status.as_str(), "pending" | "succeeded" | "refunded")
    }) else {
        return Ok(None);
    };

    let track_opt = shipping_meta.and_then(|m| m.tracking_code.as_deref());
    let carrier_opt = shipping_meta.and_then(|m| m.carrier_code.as_deref());
    let order_code_opt = shipping_meta.and_then(|m| m.order_code.as_deref());

    // Never collect additional cash or emit `payment.succeeded` when the payment is already
    // fully refunded; only allow shipping metadata backfill below.
    let awaiting_allocs = if payment.status == "refunded" {
        Vec::new()
    } else {
        sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT * FROM payment_shop_allocations
            WHERE payment_id = $1
              AND (order_id = $2 OR $2 = $3 OR $2 = $4)
              AND ($5::TEXT IS NULL OR shop_id = $5)
              AND escrow_status = 'awaiting_collection'
            ORDER BY id ASC
            FOR UPDATE
            "#,
        )
        .bind(payment.id)
        .bind(order_id)
        .bind(&payment.order_id)
        .bind(&payment.order_group_id)
        .bind(shop_filter)
        .fetch_all(&mut **tx)
        .await?
    };

    if awaiting_allocs.is_empty() {
        // E20: Even if allocation was already collected (e.g. `order.completed` arrived before
        // `order.delivered` or 0-VND COD), still persist tracking_code/carrier_code/order_code.
        if shipping_meta.is_some() {
            sqlx::query(
                r#"
                UPDATE payment_shop_allocations
                SET tracking_code = COALESCE(NULLIF(BTRIM($6), ''), tracking_code),
                    carrier_code = COALESCE(NULLIF(BTRIM($7), ''), carrier_code),
                    order_code = COALESCE(NULLIF(BTRIM($8), ''), order_code)
                WHERE payment_id = $1
                  AND (order_id = $2 OR $2 = $3 OR $2 = $4)
                  AND ($5::TEXT IS NULL OR shop_id = $5)
                  AND escrow_status IN ('held', 'pending_release', 'released', 'refunded')
                "#,
            )
            .bind(payment.id)
            .bind(order_id)
            .bind(&payment.order_id)
            .bind(&payment.order_group_id)
            .bind(shop_filter)
            .bind(track_opt)
            .bind(carrier_opt)
            .bind(order_code_opt)
            .execute(&mut **tx)
            .await?;
        }

        // Legacy single-order COD without shop_allocations rows
        let total_allocs: i64 = sqlx::query_scalar(
            "SELECT COUNT(*)::BIGINT FROM payment_shop_allocations WHERE payment_id = $1",
        )
        .bind(payment.id)
        .fetch_one(&mut **tx)
        .await?;

        if total_allocs == 0 && payment.status == "pending" && payment.amount_vnd > 0 {
            let channel =
                ChannelAttribution::from_db(&payment.sales_channel, &payment.storefront_shop_id);
            let draft =
                build_cod_collect_tx(payment.id, order_id, None, payment.amount_vnd, channel);
            if ledger::post_transaction(tx, &draft).await?.is_some() {
                metrics.record_ledger_tx("payment_cod");
            }
            let updated = sqlx::query_as::<_, PaymentRecord>(
                r#"
                UPDATE payments
                SET status = 'succeeded', paid_at = COALESCE(paid_at, NOW()), updated_at = NOW()
                WHERE id = $1
                RETURNING *
                "#,
            )
            .bind(payment.id)
            .fetch_one(&mut **tx)
            .await?;

            insert_outbox_event(
                tx,
                "payment.succeeded",
                &updated.order_group_id,
                trace_id,
                json!({
                    "payment_id": updated.id.to_string(),
                    "order_group_id": updated.order_group_id,
                    "order_id": order_id,
                    "user_id": updated.user_id,
                    "amount_vnd": updated.amount_vnd,
                    "method": "COD",
                    "provider_tx_id": format!("{}:{order_id}", updated.provider_order_code),
                    "sales_channel": updated.sales_channel,
                    "storefront_shop_id": updated.storefront_shop_id,
                }),
            )
            .await?;
            return Ok(Some(updated));
        }

        return Ok(None);
    }

    // review2-P P2-7: aggregate per `(order_id, shop_id)` before posting `cod-collect` ledger
    // transactions (scoped with `shop_id`) so multiple allocations sharing an `order_id` never
    // collide on idempotency_key and drop collected cash.
    let mut per_order_shop_collected: Vec<(String, String, i64, ChannelAttribution)> = Vec::new();
    let mut per_order_collected: Vec<(String, i64)> = Vec::new();
    for alloc in awaiting_allocs {
        let sub_payable = allocation_buyer_payable_vnd(&alloc)?;
        sqlx::query(
            r#"
            UPDATE payment_shop_allocations
            SET escrow_status = 'held',
                tracking_code = COALESCE(NULLIF(BTRIM($2), ''), tracking_code),
                carrier_code = COALESCE(NULLIF(BTRIM($3), ''), carrier_code),
                order_code = COALESCE(NULLIF(BTRIM($4), ''), order_code)
            WHERE id = $1
            "#,
        )
        .bind(alloc.id)
        .bind(track_opt)
        .bind(carrier_opt)
        .bind(order_code_opt)
        .execute(&mut **tx)
        .await?;

        if let Some(entry) = per_order_shop_collected
            .iter_mut()
            .find(|(oid, sid, _, _)| oid == &alloc.order_id && sid == &alloc.shop_id)
        {
            entry.2 = entry
                .2
                .checked_add(sub_payable)
                .ok_or_else(|| PaymentError::LedgerInvariant("COD collect overflow".to_string()))?;
        } else {
            per_order_shop_collected.push((
                alloc.order_id.clone(),
                alloc.shop_id.clone(),
                sub_payable,
                ChannelAttribution::from_db(&alloc.sales_channel, &alloc.storefront_shop_id),
            ));
        }

        if let Some(entry) = per_order_collected
            .iter_mut()
            .find(|(oid, _)| oid == &alloc.order_id)
        {
            entry.1 = entry
                .1
                .checked_add(sub_payable)
                .ok_or_else(|| PaymentError::LedgerInvariant("COD collect overflow".to_string()))?;
        } else {
            per_order_collected.push((alloc.order_id, sub_payable));
        }
    }

    for (collected_order_id, collected_shop_id, collected_vnd, ch) in &per_order_shop_collected {
        if *collected_vnd > 0 {
            let draft = build_cod_collect_tx(
                payment.id,
                collected_order_id,
                Some(collected_shop_id),
                *collected_vnd,
                ch.clone(),
            );
            if ledger::post_transaction(tx, &draft).await?.is_some() {
                metrics.record_ledger_tx("payment_cod");
            }
        }
    }

    let sse_updated = sync_cod_payment_status_tx(tx, payment.id, &payment.status).await?;

    for (collected_order_id, collected_vnd) in &per_order_collected {
        insert_outbox_event(
            tx,
            "payment.succeeded",
            &payment.order_group_id,
            trace_id,
            json!({
                "payment_id": payment.id.to_string(),
                "order_group_id": payment.order_group_id,
                "order_id": collected_order_id,
                "user_id": payment.user_id,
                "amount_vnd": collected_vnd,
                "method": "COD",
                "provider_tx_id": format!("{}:{collected_order_id}", payment.provider_order_code),
                "sales_channel": payment.sales_channel,
                "storefront_shop_id": payment.storefront_shop_id,
            }),
        )
        .await?;

        info!(
            trace_id = %trace_id,
            payment_id = %payment.id,
            order_id = %collected_order_id,
            collected_order_vnd = collected_vnd,
            "COD delivery collected into escrow_liability"
        );
    }

    Ok(sse_updated)
}

fn clamp_hold_hours(hours: i64) -> i32 {
    hours.clamp(0, 24 * 365 * 10) as i32
}

/// P0-1: Transaction-scoped PostgreSQL advisory lock per normalized `provider_tx_id`.
/// MUST always be acquired at the start of the transaction BEFORE locking `reconciliation_queue`
/// or `payments` group rows so the global lock graph remains strictly acyclic.
async fn lock_provider_tx_xact(
    tx: &mut Transaction<'_, Postgres>,
    provider_tx_id: &str,
) -> Result<()> {
    if provider_tx_id.is_empty() {
        return Ok(());
    }
    sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 7071))")
        .bind(provider_tx_id)
        .execute(&mut **tx)
        .await?;
    Ok(())
}

/// P0-1 & P1-1: Single canonical check across ALL `gateway_clearing` debit paths whether
/// `provider_tx_id` has already been accounted for on paid `payments` (`succeeded`/`refunded`),
/// finalized `reconciliation_queue` (`assigned`/`refunded`), or tx-scoped `refunds`
/// (`late-webhook:`, `recon-refund:`, `recon-overpay:`).
async fn check_provider_tx_accounting_tx(
    tx: &mut Transaction<'_, Postgres>,
    provider_tx_id: &str,
) -> Result<ProviderTxAccountingState> {
    if provider_tx_id.is_empty() {
        return Ok(ProviderTxAccountingState::default());
    }
    let row = sqlx::query(
        r#"
        SELECT
            EXISTS(
                SELECT 1 FROM payments
                WHERE provider_tx_id = $1
                  AND status IN ('succeeded', 'refunded')
            ) AS on_payment,
            EXISTS(
                SELECT 1 FROM reconciliation_queue
                WHERE provider_tx_id = $1
                  AND resolution_status IN ('assigned', 'refunded')
            ) AS recon_queue_finalized,
            EXISTS(
                SELECT 1 FROM refunds
                WHERE idempotency_key IN ($2, $3, $4)
            ) AS has_tx_refund
        "#,
    )
    .bind(provider_tx_id)
    .bind(late_payment_refund_key(provider_tx_id))
    .bind(reconciliation_refund_key(provider_tx_id))
    .bind(reconciliation_overpay_key(provider_tx_id))
    .fetch_one(&mut **tx)
    .await?;

    Ok(ProviderTxAccountingState {
        on_payment: row.try_get("on_payment")?,
        recon_queue_finalized: row.try_get("recon_queue_finalized")?,
        has_tx_refund: row.try_get("has_tx_refund")?,
    })
}

/// P2-1: Auto-closes any open (`pending`/`suspended`) `reconciliation_queue` rows for
/// `provider_tx_id` inside the SAME advisory-locked transaction when the transfer is accounted
/// via webhook, reconciliation, or expiration scanner so queue rows never stay stuck in `pending`.
#[allow(clippy::too_many_arguments)]
async fn auto_close_reconciliation_queue_tx(
    tx: &mut Transaction<'_, Postgres>,
    provider_tx_id: &str,
    resolution_status: &str,
    payment_id: Uuid,
    expected_amount_vnd: i64,
    refund_id: Option<Uuid>,
    source_note: &str,
) -> Result<()> {
    let action = if resolution_status == "assigned" {
        "ASSIGN"
    } else {
        "REFUND"
    };
    sqlx::query(
        r#"
        UPDATE reconciliation_queue
        SET resolution_status = $1,
            expected_payment_id = COALESCE(expected_payment_id, $2),
            expected_amount_vnd = COALESCE(expected_amount_vnd, $3),
            resolution_refund_id = COALESCE(resolution_refund_id, $4),
            resolution_action = $5,
            resolution_payment_id = COALESCE(resolution_payment_id, $2),
            notes = CASE
                WHEN COALESCE(BTRIM(notes), '') = '' THEN $6
                ELSE notes || ' | ' || $6
            END,
            resolved_at = NOW()
        WHERE provider_tx_id = $7
          AND resolution_status IN ('pending', 'suspended')
        "#,
    )
    .bind(resolution_status)
    .bind(payment_id)
    .bind(expected_amount_vnd)
    .bind(refund_id)
    .bind(action)
    .bind(source_note)
    .bind(provider_tx_id)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

/// P1-1: Inserts a `reconciliation_queue` row idempotently per `provider_tx_id` using
/// `WHERE NOT EXISTS (...) ON CONFLICT DO NOTHING` (never triggers `25P02` tx abort, requires no
/// `SAVEPOINT`, and works safely even if the conditional unique index in 0004 was skipped).
#[allow(clippy::too_many_arguments)]
async fn enqueue_reconciliation_item_tx(
    tx: &mut Transaction<'_, Postgres>,
    webhook_event_id: Option<Uuid>,
    provider: &str,
    provider_tx_id: &str,
    expected_payment_id: Option<Uuid>,
    order_ref: Option<&str>,
    expected_amount_vnd: Option<i64>,
    actual_amount_vnd: i64,
    reason: &str,
    occurred_at: Option<DateTime<Utc>>,
    source_type: &str,
    tracking_code: Option<&str>,
) -> Result<()> {
    let tracking = tracking_code.map(str::trim).unwrap_or("");
    sqlx::query(
        r#"
        INSERT INTO reconciliation_queue (
            webhook_event_id, provider, provider_tx_id, expected_payment_id,
            order_ref, expected_amount_vnd, actual_amount_vnd, reason, occurred_at,
            source_type, tracking_code
        )
        SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
        WHERE NOT EXISTS (
            SELECT 1 FROM reconciliation_queue WHERE provider_tx_id = $3
        )
        ON CONFLICT DO NOTHING
        "#,
    )
    .bind(webhook_event_id)
    .bind(provider)
    .bind(provider_tx_id)
    .bind(expected_payment_id)
    .bind(order_ref)
    .bind(expected_amount_vnd)
    .bind(actual_amount_vnd)
    .bind(reason)
    .bind(occurred_at)
    .bind(source_type)
    .bind(tracking)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

/// E20 / E20-bis COD file queue upsert helper:
/// - If an open (`pending`/`suspended`) row already exists for `provider_tx_id`, refreshes
///   its reason/amounts/order_ref in place (idempotent across file re-uploads).
/// - If a finalized (`assigned`/`refunded`/`ignored`) row already occupies `provider_tx_id`,
///   leaves it closed without duplicating.
/// - Otherwise inserts a new `pending` queue row with `source_type = 'cod_file'`.
#[allow(clippy::too_many_arguments)]
async fn enqueue_cod_reconciliation_item_tx(
    tx: &mut Transaction<'_, Postgres>,
    carrier_code: &str,
    provider_tx_id: &str,
    tracking_code: &str,
    expected_payment_id: Option<Uuid>,
    order_ref: Option<&str>,
    expected_amount_vnd: Option<i64>,
    actual_amount_vnd: i64,
    reason: &str,
    occurred_at: Option<DateTime<Utc>>,
) -> Result<()> {
    let existing_status: Option<String> = sqlx::query_scalar(
        "SELECT resolution_status FROM reconciliation_queue WHERE provider_tx_id = $1 LIMIT 1",
    )
    .bind(provider_tx_id)
    .fetch_optional(&mut **tx)
    .await?;

    match existing_status.as_deref() {
        Some("pending" | "suspended") => {
            sqlx::query(
                r#"
                UPDATE reconciliation_queue
                SET expected_payment_id = COALESCE($1, expected_payment_id),
                    order_ref = COALESCE($2, order_ref),
                    expected_amount_vnd = COALESCE($3, expected_amount_vnd),
                    actual_amount_vnd = $4,
                    reason = $5,
                    occurred_at = COALESCE($6, occurred_at),
                    source_type = 'cod_file',
                    tracking_code = COALESCE(NULLIF(BTRIM($7), ''), tracking_code)
                WHERE provider_tx_id = $8
                  AND resolution_status IN ('pending', 'suspended')
                "#,
            )
            .bind(expected_payment_id)
            .bind(order_ref)
            .bind(expected_amount_vnd)
            .bind(actual_amount_vnd)
            .bind(reason)
            .bind(occurred_at)
            .bind(tracking_code)
            .bind(provider_tx_id)
            .execute(&mut **tx)
            .await?;
            Ok(())
        }
        Some(_) => Ok(()),
        None => {
            enqueue_reconciliation_item_tx(
                tx,
                None,
                carrier_code,
                provider_tx_id,
                expected_payment_id,
                order_ref,
                expected_amount_vnd,
                actual_amount_vnd,
                reason,
                occurred_at,
                "cod_file",
                Some(tracking_code),
            )
            .await
        }
    }
}

/// E20-bis: Auto-closes open (`pending`/`suspended`) COD reconciliation queue entries for
/// `(carrier_code, tracking_code)` created strictly BEFORE `created_before` (`run_created_at`)
/// inside the SAME transaction when the row matches.
/// - Items created by the current run (`created_at >= created_before`) stay `pending`.
/// - `cod_carrier_mismatch` is NEVER auto-closed, and the `tracking_code` branch strictly filters
///   by `LOWER(BTRIM(provider)) = carrier_code` so one carrier's match never closes another carrier's queue item.
/// - When `only_reasons` is `Some(reasons)`, restricts closing strictly to those `reason` values.
#[allow(clippy::too_many_arguments)]
async fn auto_close_cod_reconciliation_queue_tx(
    tx: &mut Transaction<'_, Postgres>,
    carrier_code: &str,
    cod_provider_tx_id: &str,
    tracking_code: &str,
    payment_id: Uuid,
    expected_amount_vnd: i64,
    source_note: &str,
    only_reasons: Option<&[&str]>,
    created_before: DateTime<Utc>,
) -> Result<()> {
    let reason_filter: Option<Vec<String>> =
        only_reasons.map(|list| list.iter().map(|s| (*s).to_string()).collect());
    sqlx::query(
        r#"
        UPDATE reconciliation_queue
        SET resolution_status = 'assigned',
            expected_payment_id = COALESCE(expected_payment_id, $1),
            expected_amount_vnd = COALESCE(expected_amount_vnd, $2),
            resolution_action = 'ASSIGN',
            resolution_payment_id = COALESCE(resolution_payment_id, $1),
            notes = CASE
                WHEN COALESCE(BTRIM(notes), '') = '' THEN $3
                ELSE notes || ' | ' || $3
            END,
            resolved_at = NOW()
        WHERE resolution_status IN ('pending', 'suspended')
          AND created_at < $7
          AND reason <> 'cod_carrier_mismatch'
          AND (
              provider_tx_id = $4
              OR (
                  source_type = 'cod_file'
                  AND LOWER(BTRIM(provider)) = $8
                  AND tracking_code <> ''
                  AND tracking_code = $5
              )
          )
          AND ($6::TEXT[] IS NULL OR reason = ANY($6))
        "#,
    )
    .bind(payment_id)
    .bind(expected_amount_vnd)
    .bind(source_note)
    .bind(cod_provider_tx_id)
    .bind(tracking_code)
    .bind(reason_filter)
    .bind(created_before)
    .bind(carrier_code)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

#[allow(clippy::too_many_arguments)]
async fn record_late_payment_refund_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment: &PaymentRecord,
    provider_tx_id: &str,
    amount_vnd: i64,
    pending_next_status: Option<&str>,
    accounting_source: &str,
    zalopay_app_id: Option<i32>,
    metrics: &Metrics,
) -> Result<()> {
    // P0-1 defense-in-depth: never debit gateway_clearing or insert a second refund if this
    // provider_tx_id was already accounted for by payments, reconciliation_queue, or refunds.
    let acct = check_provider_tx_accounting_tx(tx, provider_tx_id).await?;
    if acct.is_accounted() {
        return Ok(());
    }

    if let Some(next_status) = pending_next_status {
        sqlx::query(
            r#"
            UPDATE payments
            SET status = $1, updated_at = NOW()
            WHERE id = $2 AND status = 'pending'
            "#,
        )
        .bind(next_status)
        .bind(payment.id)
        .execute(&mut **tx)
        .await?;

        sqlx::query(
            r#"
            UPDATE payment_shop_allocations
            SET escrow_status = 'cancelled'
            WHERE payment_id = $1 AND escrow_status IN ('awaiting_collection', 'held')
            "#,
        )
        .bind(payment.id)
        .execute(&mut **tx)
        .await?;
    }

    let channel = ChannelAttribution::from_db(&payment.sales_channel, &payment.storefront_shop_id);
    let ledger_draft = build_late_payment_refund_tx(provider_tx_id, amount_vnd, channel);
    if ledger::post_transaction(tx, &ledger_draft).await?.is_some() {
        metrics.record_ledger_tx("late_payment_refund");
    }

    // review2-P P1-1: single canonical idempotency key across webhook, expiry scanner, and reconciliation.
    let refund_key = late_payment_refund_key(provider_tx_id);
    let refund_id = Uuid::new_v4();
    let (provider_m_refund_id, provider_status) = if payment.method == "ZALOPAY" {
        if let Some(app_id) = zalopay_app_id {
            (
                Some(generate_m_refund_id(app_id, refund_id, Utc::now())),
                Some(REFUND_PROVIDER_STATUS_PENDING),
            )
        } else {
            (None, None)
        }
    } else {
        (None, None)
    };
    let refund = sqlx::query_as::<_, RefundRecord>(
        r#"
        INSERT INTO refunds (
            id, payment_id, order_id, amount_vnd, reason, status, idempotency_key,
            provider_m_refund_id, provider_status,
            is_late_payment, affects_escrow
        )
        VALUES ($1, $2, $3, $4, 'late_payment', 'pending', $5, $6, $7, TRUE, FALSE)
        ON CONFLICT (idempotency_key) DO UPDATE SET updated_at = NOW()
        RETURNING *
        "#,
    )
    .bind(refund_id)
    .bind(payment.id)
    .bind(&payment.order_id)
    .bind(amount_vnd)
    .bind(&refund_key)
    .bind(provider_m_refund_id.as_deref())
    .bind(provider_status)
    .fetch_one(&mut **tx)
    .await?;

    // P2-1: Auto-close any open reconciliation_queue item for this provider_tx_id in same tx.
    auto_close_reconciliation_queue_tx(
        tx,
        provider_tx_id,
        "refunded",
        payment.id,
        payment.amount_vnd,
        Some(refund.id),
        accounting_source,
    )
    .await?;

    if payment.method == "ZALOPAY" {
        sqlx::query(
            r#"
            UPDATE payments
            SET provider_final_status = $1,
                provider_next_check_at = NULL,
                updated_at = NOW()
            WHERE id = $2
            "#,
        )
        .bind(PROVIDER_FINAL_STATUS_LATE_REFUND_QUEUED)
        .bind(payment.id)
        .execute(&mut **tx)
        .await?;
    }

    // R3 / PAY-B6: Never emit payment.succeeded or payment.refunded for late/superseded payments.
    Ok(())
}

async fn fetch_cod_fee_adjustment_by_key_tx(
    tx: &mut Transaction<'_, Postgres>,
    idempotency_key: &str,
) -> Result<Option<CodFeeAdjustmentRecord>> {
    let row = sqlx::query_as::<_, CodFeeAdjustmentRecord>(
        "SELECT * FROM cod_fee_adjustments WHERE idempotency_key = $1",
    )
    .bind(idempotency_key)
    .fetch_optional(&mut **tx)
    .await?;
    Ok(row)
}

async fn build_cod_adjustment_replay_tx(
    tx: &mut Transaction<'_, Postgres>,
    existing: &CodFeeAdjustmentRecord,
    order_id: &str,
    new_shipping_fee_vnd: i64,
) -> Result<AdjustCodShippingFeeOutput> {
    validate_cod_fee_adjustment_replay(existing, order_id, new_shipping_fee_vnd)?;
    let payment = sqlx::query_as::<_, PaymentRecord>("SELECT * FROM payments WHERE id = $1")
        .bind(existing.payment_id)
        .fetch_one(&mut **tx)
        .await?;
    let allocations = fetch_allocations_tx(tx, payment.id).await?;
    Ok(AdjustCodShippingFeeOutput {
        payment: PaymentWithAllocations {
            payment,
            allocations,
        },
        old_shipping_fee_vnd: existing.old_shipping_fee_vnd,
        new_shipping_fee_vnd: existing.new_shipping_fee_vnd,
        new_order_payable_vnd: existing.new_order_payable_vnd,
        already_applied: true,
    })
}

async fn fetch_allocations_pool(
    pool: &PgPool,
    payment_id: Uuid,
) -> Result<Vec<ShopAllocationRecord>> {
    let rows = sqlx::query_as::<_, ShopAllocationRecord>(
        "SELECT * FROM payment_shop_allocations WHERE payment_id = $1 ORDER BY created_at ASC",
    )
    .bind(payment_id)
    .fetch_all(pool)
    .await?;
    Ok(rows)
}

async fn fetch_allocations_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment_id: Uuid,
) -> Result<Vec<ShopAllocationRecord>> {
    let rows = sqlx::query_as::<_, ShopAllocationRecord>(
        "SELECT * FROM payment_shop_allocations WHERE payment_id = $1 ORDER BY created_at ASC",
    )
    .bind(payment_id)
    .fetch_all(&mut **tx)
    .await?;
    Ok(rows)
}

/// Read-only lookup for incoming webhooks. Caller MUST lock the checkout group via
/// `lock_payment_group_tx` (`ORDER BY id ASC FOR UPDATE`) immediately after (P1-5).
async fn find_payment_for_webhook_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment_id: Option<Uuid>,
    provider_order_code: Option<&str>,
    order_group_id: Option<&str>,
    order_id: Option<&str>,
) -> Result<Option<PaymentRecord>> {
    if let Some(pid) = payment_id {
        if let Some(p) = sqlx::query_as::<_, PaymentRecord>("SELECT * FROM payments WHERE id = $1")
            .bind(pid)
            .fetch_optional(&mut **tx)
            .await?
        {
            return Ok(Some(p));
        }
    }

    if let Some(code) = provider_order_code.filter(|s| !s.trim().is_empty()) {
        if let Some(p) = sqlx::query_as::<_, PaymentRecord>(
            "SELECT * FROM payments WHERE provider_order_code = $1",
        )
        .bind(code)
        .fetch_optional(&mut **tx)
        .await?
        {
            return Ok(Some(p));
        }
    }

    for candidate in [order_group_id, order_id].into_iter().flatten() {
        let trimmed = candidate.trim();
        if trimmed.is_empty() {
            continue;
        }
        if let Some(p) = sqlx::query_as::<_, PaymentRecord>(
            r#"
            SELECT * FROM payments
            WHERE (order_group_id = $1 OR order_id = $1)
              AND method = 'VIETQR'
            ORDER BY
                CASE status
                    WHEN 'pending' THEN 1
                    WHEN 'succeeded' THEN 2
                    ELSE 3
                END,
                created_at DESC
            LIMIT 1
            "#,
        )
        .bind(trimmed)
        .fetch_optional(&mut **tx)
        .await?
        {
            return Ok(Some(p));
        }
    }

    Ok(None)
}

#[allow(clippy::too_many_arguments)]
async fn confirm_online_payment_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment: &PaymentRecord,
    provider_tx_id: &str,
    paid_at: Option<DateTime<Utc>>,
    accounting_source: Option<&str>,
    trace_id: &str,
    metrics: &Metrics,
) -> Result<PaymentRecord> {
    // P0-1 defense-in-depth: refuse to debit gateway_clearing for a provider_tx_id already
    // accounted for on payments, finalized reconciliation_queue, or tx-scoped refunds.
    let acct = check_provider_tx_accounting_tx(tx, provider_tx_id).await?;
    if acct.is_accounted() {
        return Err(PaymentError::Conflict(format!(
            "provider_tx_id '{provider_tx_id}' is already accounted for"
        )));
    }

    let updated = match sqlx::query_as::<_, PaymentRecord>(
        r#"
        UPDATE payments
        SET status = 'succeeded',
            provider_tx_id = $1,
            paid_at = COALESCE($2, NOW()),
            provider_final_status = $4,
            provider_next_check_at = NULL,
            updated_at = NOW()
        WHERE id = $3 AND status = 'pending'
        RETURNING *
        "#,
    )
    .bind(provider_tx_id)
    .bind(paid_at)
    .bind(payment.id)
    .bind(PROVIDER_FINAL_STATUS_PAID_CONFIRMED)
    .fetch_optional(&mut **tx)
    .await
    {
        Ok(Some(row)) => row,
        Ok(None) => {
            return Err(PaymentError::Conflict(format!(
                "payment {} is no longer pending and cannot be confirmed",
                payment.id
            )));
        }
        Err(err)
            if is_unique_violation(&err, "provider_tx_id")
                || is_unique_violation(&err, "idx_payments_one_succeeded_per_order")
                || is_unique_violation(&err, "idx_payments_one_succeeded_per_group") =>
        {
            return Err(PaymentError::Conflict(format!(
                "payment {} conflicts with an existing succeeded payment or provider_tx_id",
                payment.id
            )));
        }
        Err(err) => return Err(PaymentError::Database(err)),
    };

    sqlx::query(
        r#"
        UPDATE payment_shop_allocations
        SET escrow_status = 'held'
        WHERE payment_id = $1 AND escrow_status = 'awaiting_collection'
        "#,
    )
    .bind(updated.id)
    .execute(&mut **tx)
    .await?;

    if updated.amount_vnd > 0 {
        let channel =
            ChannelAttribution::from_db(&updated.sales_channel, &updated.storefront_shop_id);
        let (draft, metric_label) = if updated.method == "ZALOPAY" {
            (
                build_zalopay_confirm_tx(updated.id, updated.amount_vnd, channel),
                "payment_zalopay",
            )
        } else {
            (
                build_vietqr_confirm_tx(updated.id, updated.amount_vnd, channel),
                "payment_vietqr",
            )
        };
        if ledger::post_transaction(tx, &draft).await?.is_some() {
            metrics.record_ledger_tx(metric_label);
        }
    }

    // P2-1: Auto-close any open reconciliation_queue item for this provider_tx_id in same tx.
    if let Some(source) = accounting_source {
        auto_close_reconciliation_queue_tx(
            tx,
            provider_tx_id,
            "assigned",
            updated.id,
            updated.amount_vnd,
            None,
            source,
        )
        .await?;
    }

    insert_outbox_event(
        tx,
        "payment.succeeded",
        &updated.order_group_id,
        trace_id,
        json!({
            "payment_id": updated.id.to_string(),
            "order_group_id": updated.order_group_id,
            "order_id": updated.order_id,
            "user_id": updated.user_id,
            "amount_vnd": updated.amount_vnd,
            "method": updated.method,
            "provider_tx_id": provider_tx_id,
            "sales_channel": updated.sales_channel,
            "storefront_shop_id": updated.storefront_shop_id,
        }),
    )
    .await?;

    Ok(updated)
}

async fn fetch_payment_order_meta_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment_id: Uuid,
) -> Result<(String, i64)> {
    let row = sqlx::query(
        r#"
        SELECT
            p.order_id AS payment_order_id,
            (SELECT COUNT(*)::BIGINT FROM payment_shop_allocations WHERE payment_id = p.id) AS alloc_count
        FROM payments p
        WHERE p.id = $1
        "#,
    )
    .bind(payment_id)
    .fetch_one(&mut **tx)
    .await?;
    Ok((
        row.try_get("payment_order_id")?,
        row.try_get("alloc_count")?,
    ))
}

async fn has_pending_approval_refund_for_alloc_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment_id: Uuid,
    alloc_order_id: &str,
    payment_order_id: &str,
    is_single_allocation: bool,
) -> Result<bool> {
    let exists: bool = sqlx::query_scalar(
        r#"
        SELECT EXISTS(
            SELECT 1
            FROM refunds
            WHERE payment_id = $1
              AND (order_id = $2 OR ($4 AND order_id = $3))
              AND status = 'pending_approval'
              AND affects_escrow
              AND NOT is_late_payment
        )
        "#,
    )
    .bind(payment_id)
    .bind(alloc_order_id)
    .bind(payment_order_id)
    .bind(is_single_allocation)
    .fetch_one(&mut **tx)
    .await?;
    Ok(exists)
}

async fn sum_suborder_refunded_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment_id: Uuid,
    alloc_order_id: &str,
    payment_order_id: &str,
    is_single_allocation: bool,
) -> Result<i64> {
    let sum: i64 = sqlx::query_scalar(
        r#"
        SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
        FROM refunds
        WHERE payment_id = $1
          AND (order_id = $2 OR ($4 AND order_id = $3))
          AND status IN ('pending', 'completed', 'manual_review')
          AND affects_escrow
          AND NOT is_late_payment
        "#,
    )
    .bind(payment_id)
    .bind(alloc_order_id)
    .bind(payment_order_id)
    .bind(is_single_allocation)
    .fetch_one(&mut **tx)
    .await?;
    Ok(sum)
}

/// Shared release helper for a single locked `pending_release` allocation:
/// acquires deterministic shop ledger account locks (`seller_available`, `seller_pending`),
/// protects other still-unmatured `pending_release` allocations of the same shop (P2-4),
/// posts `build_escrow_release_tx`, and marks the allocation `'released'`.
async fn release_pending_allocation_tx(
    tx: &mut Transaction<'_, Postgres>,
    alloc: &ShopAllocationRecord,
    hold_hours: i64,
    metrics: &Metrics,
) -> Result<i64> {
    let (payment_order_id, alloc_count) = fetch_payment_order_meta_tx(tx, alloc.payment_id).await?;
    let suborder_refunded = sum_suborder_refunded_tx(
        tx,
        alloc.payment_id,
        &alloc.order_id,
        &payment_order_id,
        alloc_count == 1,
    )
    .await?;

    let split = compute_settlement_split(
        &alloc.order_id,
        &alloc.shop_id,
        alloc.subtotal_vnd,
        alloc.commission_vnd,
        alloc.shipping_fee_vnd,
        alloc.platform_discount_vnd,
        suborder_refunded,
    )?;

    let (pending_bal, _) = ledger::lock_and_get_seller_balances_tx(tx, &alloc.shop_id).await?;

    // P2-4 & P3-1: Sum seller_net of OTHER still-unmatured `pending_release` allocations for
    // this shop in ONE aggregated SQL query (no N+1 while holding ledger_accounts FOR UPDATE).
    let hold_hours_i32 = clamp_hold_hours(hold_hours);
    let unmatured_hold_vnd = if hold_hours_i32 > 0 {
        let rows = sqlx::query(
            r#"
            WITH unmatured AS (
                SELECT
                    a.id,
                    a.payment_id,
                    a.order_id,
                    a.shop_id,
                    a.subtotal_vnd,
                    a.commission_vnd,
                    a.shipping_fee_vnd,
                    a.platform_discount_vnd,
                    p.order_id AS payment_order_id,
                    (SELECT COUNT(*)::BIGINT FROM payment_shop_allocations a2 WHERE a2.payment_id = a.payment_id) AS alloc_count
                FROM payment_shop_allocations a
                JOIN payments p ON p.id = a.payment_id
                WHERE a.shop_id = $1
                  AND a.id <> $2
                  AND a.escrow_status = 'pending_release'
                  AND a.settled_at IS NOT NULL
                  AND (
                      a.settled_at > NOW() - make_interval(hours => $3)
                      OR EXISTS (
                          SELECT 1
                          FROM refunds r_pa
                          WHERE r_pa.payment_id = a.payment_id
                            AND (
                                r_pa.order_id = a.order_id
                                OR (
                                    (SELECT COUNT(*) FROM payment_shop_allocations a3 WHERE a3.payment_id = a.payment_id) = 1
                                    AND r_pa.order_id = p.order_id
                                )
                            )
                            AND r_pa.status = 'pending_approval'
                            AND r_pa.affects_escrow
                            AND NOT r_pa.is_late_payment
                      )
                  )
                  AND (p.status IN ('succeeded', 'refunded') OR (p.method = 'COD' AND p.status = 'pending'))
            )
            SELECT
                u.order_id,
                u.shop_id,
                u.subtotal_vnd,
                u.commission_vnd,
                u.shipping_fee_vnd,
                u.platform_discount_vnd,
                COALESCE(SUM(r.amount_vnd), 0)::BIGINT AS refunded_vnd
            FROM unmatured u
            LEFT JOIN refunds r
                ON r.payment_id = u.payment_id
               AND (r.order_id = u.order_id OR (u.alloc_count = 1 AND r.order_id = u.payment_order_id))
               AND r.status IN ('pending', 'completed', 'manual_review')
               AND r.affects_escrow
               AND NOT r.is_late_payment
            GROUP BY
                u.id, u.order_id, u.shop_id, u.subtotal_vnd, u.commission_vnd, u.shipping_fee_vnd, u.platform_discount_vnd
            "#,
        )
        .bind(&alloc.shop_id)
        .bind(alloc.id)
        .bind(hold_hours_i32)
        .fetch_all(&mut **tx)
        .await?;

        let mut reserved = 0_i64;
        for row in rows {
            let sib_order_id: String = row.try_get("order_id")?;
            let sib_shop_id: String = row.try_get("shop_id")?;
            let sib_subtotal: i64 = row.try_get("subtotal_vnd")?;
            let sib_commission: i64 = row.try_get("commission_vnd")?;
            let sib_shipping: i64 = row.try_get("shipping_fee_vnd")?;
            let sib_discount: i64 = row.try_get("platform_discount_vnd")?;
            let sib_refunded: i64 = row.try_get("refunded_vnd")?;
            let sib_split = compute_settlement_split(
                &sib_order_id,
                &sib_shop_id,
                sib_subtotal,
                sib_commission,
                sib_shipping,
                sib_discount,
                sib_refunded,
            )?;
            reserved = reserved.saturating_add(sib_split.seller_net_vnd.max(0));
        }
        reserved
    } else {
        0
    };

    let release_amount =
        compute_capped_escrow_release_vnd(split.seller_net_vnd, pending_bal, unmatured_hold_vnd);

    if release_amount > 0 {
        let channel = ChannelAttribution::from_db(&alloc.sales_channel, &alloc.storefront_shop_id);
        let draft =
            build_escrow_release_tx(&alloc.order_id, &alloc.shop_id, release_amount, channel)?;
        if ledger::post_transaction(tx, &draft).await?.is_some() {
            metrics.record_ledger_tx("escrow_release");
        }
    }

    sqlx::query(
        r#"
        UPDATE payment_shop_allocations
        SET escrow_status = 'released', released_at = NOW()
        WHERE id = $1
        "#,
    )
    .bind(alloc.id)
    .execute(&mut **tx)
    .await?;

    Ok(release_amount)
}

async fn settle_order_to_pending_tx(
    tx: &mut Transaction<'_, Postgres>,
    order_id: &str,
    shop_filter: Option<&str>,
    metrics: &Metrics,
) -> Result<usize> {
    let held_allocs = sqlx::query_as::<_, ShopAllocationRecord>(
        r#"
        SELECT a.*
        FROM payment_shop_allocations a
        JOIN payments p ON p.id = a.payment_id
        WHERE (a.order_id = $1 OR p.order_id = $1 OR p.order_group_id = $1)
          AND ($2::TEXT IS NULL OR a.shop_id = $2)
          AND (p.status IN ('succeeded', 'refunded') OR (p.method = 'COD' AND p.status = 'pending'))
          AND a.escrow_status = 'held'
        ORDER BY a.id ASC
        FOR UPDATE OF a
        "#,
    )
    .bind(order_id)
    .bind(shop_filter)
    .fetch_all(&mut **tx)
    .await?;

    let mut settled = 0usize;
    for alloc in held_allocs {
        let (payment_order_id, alloc_count) =
            fetch_payment_order_meta_tx(tx, alloc.payment_id).await?;
        let suborder_refunded = sum_suborder_refunded_tx(
            tx,
            alloc.payment_id,
            &alloc.order_id,
            &payment_order_id,
            alloc_count == 1,
        )
        .await?;

        let split = compute_settlement_split(
            &alloc.order_id,
            &alloc.shop_id,
            alloc.subtotal_vnd,
            alloc.commission_vnd,
            alloc.shipping_fee_vnd,
            alloc.platform_discount_vnd,
            suborder_refunded,
        )?;

        if split.total_debit_vnd() > 0 {
            let channel =
                ChannelAttribution::from_db(&alloc.sales_channel, &alloc.storefront_shop_id);
            let draft = build_escrow_settle_tx(
                &alloc.order_id,
                &alloc.shop_id,
                split.escrow_debit_vnd,
                split.platform_promo_debit_vnd,
                split.seller_net_vnd,
                split.commission_vnd,
                split.shipping_fee_vnd,
                channel,
            )?;
            if ledger::post_transaction(tx, &draft).await?.is_some() {
                metrics.record_ledger_tx("escrow_settle");
            }
        }

        sqlx::query(
            r#"
            UPDATE payment_shop_allocations
            SET escrow_status = 'pending_release', settled_at = NOW()
            WHERE id = $1
            "#,
        )
        .bind(alloc.id)
        .execute(&mut **tx)
        .await?;

        settled += 1;
    }

    Ok(settled)
}

async fn resolve_refund_channel_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment: &PaymentRecord,
    order_id: &str,
) -> Result<ChannelAttribution> {
    let alloc_channel: Option<(String, String)> = sqlx::query_as(
        r#"
        SELECT sales_channel, storefront_shop_id
        FROM payment_shop_allocations
        WHERE payment_id = $1 AND order_id = $2
        ORDER BY created_at ASC
        LIMIT 1
        "#,
    )
    .bind(payment.id)
    .bind(order_id)
    .fetch_optional(&mut **tx)
    .await?;

    Ok(match alloc_channel {
        Some((ch, shop)) => ChannelAttribution::from_db(&ch, &shop),
        None => ChannelAttribution::from_db(&payment.sales_channel, &payment.storefront_shop_id),
    })
}

fn build_payment_refunded_payload(
    refund: &RefundRecord,
    payment: &PaymentRecord,
    order_id: &str,
    is_post_settlement: bool,
) -> serde_json::Value {
    json!({
        "refund_id": refund.id.to_string(),
        "payment_id": payment.id.to_string(),
        "order_id": order_id,
        "user_id": payment.user_id,
        "amount_vnd": refund.amount_vnd,
        "reason": refund.reason,
        "is_post_settlement": is_post_settlement,
        "idempotency_key": refund.idempotency_key,
        "sales_channel": payment.sales_channel,
        "storefront_shop_id": payment.storefront_shop_id,
    })
}

async fn insert_outbox_event(
    tx: &mut Transaction<'_, Postgres>,
    event_type: &str,
    aggregate_id: &str,
    trace_id: &str,
    payload: serde_json::Value,
) -> Result<Uuid> {
    let row = sqlx::query(
        r#"
        INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload)
        VALUES ($1, $2, $3, $4)
        RETURNING id
        "#,
    )
    .bind(event_type)
    .bind(aggregate_id)
    .bind(trace_id)
    .bind(payload)
    .fetch_one(&mut **tx)
    .await?;
    Ok(row.try_get("id")?)
}

#[allow(clippy::too_many_arguments)]
async fn insert_reconciliation_item(
    tx: &mut Transaction<'_, Postgres>,
    run_id: Uuid,
    classification: &str,
    payment_id: Option<Uuid>,
    provider_tx_id: Option<&str>,
    local_amount_vnd: Option<i64>,
    provider_amount_vnd: Option<i64>,
    details: &str,
    sales_channel: &str,
    storefront_shop_id: &str,
) -> Result<ReconciliationItemRecord> {
    insert_reconciliation_item_ext(
        tx,
        run_id,
        classification,
        payment_id,
        provider_tx_id,
        local_amount_vnd,
        provider_amount_vnd,
        details,
        "",
        "",
        0,
        sales_channel,
        storefront_shop_id,
    )
    .await
}

#[allow(clippy::too_many_arguments)]
async fn insert_reconciliation_item_ext(
    tx: &mut Transaction<'_, Postgres>,
    run_id: Uuid,
    classification: &str,
    payment_id: Option<Uuid>,
    provider_tx_id: Option<&str>,
    local_amount_vnd: Option<i64>,
    provider_amount_vnd: Option<i64>,
    details: &str,
    tracking_code: &str,
    order_id: &str,
    line_number: i32,
    sales_channel: &str,
    storefront_shop_id: &str,
) -> Result<ReconciliationItemRecord> {
    let rec = sqlx::query_as::<_, ReconciliationItemRecord>(
        r#"
        INSERT INTO reconciliation_items (
            run_id, classification, payment_id, provider_tx_id,
            local_amount_vnd, provider_amount_vnd, details,
            tracking_code, order_id, line_number,
            sales_channel, storefront_shop_id
        )
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
        RETURNING *
        "#,
    )
    .bind(run_id)
    .bind(classification)
    .bind(payment_id)
    .bind(provider_tx_id)
    .bind(local_amount_vnd)
    .bind(provider_amount_vnd)
    .bind(details)
    .bind(tracking_code)
    .bind(order_id)
    .bind(line_number)
    .bind(sales_channel)
    .bind(storefront_shop_id)
    .fetch_one(&mut **tx)
    .await?;
    Ok(rec)
}
