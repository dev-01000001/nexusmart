use std::collections::{HashMap, HashSet};

use chrono::{DateTime, Duration, NaiveDate, Utc};
use serde::{Deserialize, Serialize};
use serde_json::json;
use sqlx::{PgPool, Postgres, Row, Transaction};
use tokio::sync::broadcast;
use tracing::{info, warn};
use uuid::Uuid;

use crate::config::{Config, VietQrMode};
use crate::domain::{
    allocation_buyer_payable_vnd, compute_capped_escrow_release_vnd,
    compute_post_settlement_clawback, compute_reconciliation_assign_split,
    compute_settlement_split, compute_suborder_refund_amount, decide_reconciliation_match_action,
    decide_return_allocation_action, format_seller_ledger_description,
    format_settlement_note_with_masked_account, is_payment_already_cancelled_terminal,
    late_payment_refund_key, order_cancel_refund_key, order_expiry_candidate_indices,
    plan_initial_payment, reconciliation_overpay_key, reconciliation_refund_key,
    resolve_cod_payment_status, select_payment_for_group_action, utc_to_vn_date,
    validate_allocations_sum, validate_group_can_create_payment,
    validate_payment_idempotency_match, validate_reconciliation_provider_tx_accountable,
    validate_refund_bank_account, validate_refund_eligibility, validate_refund_idempotency_match,
    validate_resolve_reconciliation_idempotency_match, validate_set_fee_config,
    validate_settle_refund_idempotency_match, validate_suborder_refund_cap, vn_day_utc_bounds,
    vn_today, AssignAmountSplit, CreatePaymentInput, CreateRefundInput, CreateRefundOutput,
    GetFeeConfigFilter, GetFeeConfigResult, GroupPaymentSelection, ListReconciliationQueueResult,
    ListRefundsFilter, ListRefundsResult, ListSellerLedgerFilter, ListSellerLedgerResult,
    PaymentFeeConfigRecord, PaymentMethod, PaymentRecord, PaymentSseEvent, PaymentWithAllocations,
    ProviderTxAccountingState, ReconciliationActionKind, ReconciliationDecision,
    ReconciliationItemRecord, ReconciliationQueueItem, ReconciliationReport,
    ReconciliationRunRecord, RefundRecord, ResolveReconciliationInput, ResolveReconciliationOutput,
    ReturnAllocationAction, SellerLedgerAccountFilter, SellerLedgerEntryRecord, SellerPayoutRecord,
    SetFeeConfigInput, SettleRefundInput, SettleRefundOutput, ShopAllocationRecord,
    SubmitRefundBankAccountInput,
};
use crate::error::{PaymentError, Result};
use crate::ledger::{
    self, build_cod_collect_tx, build_escrow_release_tx, build_escrow_settle_tx,
    build_late_payment_refund_tx, build_post_settlement_refund_tx, build_reconciliation_refund_tx,
    build_refund_settle_tx, build_refund_tx, build_seller_payout_tx, build_vietqr_confirm_tx,
    LedgerSummary, SellerBalance,
};
use crate::metrics::Metrics;
use crate::telemetry::mask_bank_account;
use crate::vietqr::{
    extract_occurred_at_from_json, parse_provider_timestamp, ProviderTransaction, VietQrAdapter,
};
use crate::webhook_security::{decide_webhook_action, IncomingWebhookPayload, WebhookDecision};

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

#[derive(Clone)]
pub struct PaymentService {
    pool: PgPool,
    config: Config,
    vietqr: VietQrAdapter,
    metrics: Metrics,
    sse_tx: broadcast::Sender<PaymentSseEvent>,
}

impl PaymentService {
    pub fn new(pool: PgPool, config: Config, metrics: Metrics) -> Self {
        let vietqr = VietQrAdapter::from_config(&config);
        let (sse_tx, _) = broadcast::channel(512);
        Self {
            pool,
            config,
            vietqr,
            metrics,
            sse_tx,
        }
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
                    let allocations = fetch_allocations_pool(&self.pool, existing.id).await?;
                    return Ok(PaymentWithAllocations {
                        payment: existing,
                        allocations,
                    });
                }
            }
        }

        let payment_id = Uuid::new_v4();
        let expires_at =
            if initial_plan.create_qr {
                Some(input.expires_at.unwrap_or_else(|| {
                    Utc::now() + Duration::seconds(self.config.vietqr_expiry_secs)
                }))
            } else {
                None
            };

        let (provider, provider_order_code, qr_code, qr_url) = if initial_plan.create_qr {
            let intent = self
                .vietqr
                .create_qr_intent(
                    payment_id,
                    &order_id,
                    input.amount_vnd,
                    expires_at.unwrap_or_else(Utc::now),
                )
                .await?;
            (
                intent.provider,
                intent.provider_order_code,
                Some(intent.qr_code),
                Some(intent.qr_url),
            )
        } else {
            let provider = match input.method {
                PaymentMethod::Cod => "cod".to_string(),
                PaymentMethod::VietQr => "zero_payable".to_string(),
            };
            (
                provider,
                VietQrAdapter::generate_order_code(payment_id, &order_id),
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
                    let allocations = fetch_allocations_tx(&mut tx, existing.id).await?;
                    tx.commit().await?;
                    return Ok(PaymentWithAllocations {
                        payment: existing,
                        allocations,
                    });
                }
            }
        }

        // UC-B25 / R3 / P2-7: reject if group already succeeded/refunded, or if supersede_previous
        // targets a group without an active pending payment or with cancelled/expired/failed history.
        let existing_statuses: Vec<&str> = existing_group_payments
            .iter()
            .map(|p| p.status.as_str())
            .collect();
        validate_group_can_create_payment(&existing_statuses, input.supersede_previous)?;

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
                amount_vnd, status, provider_order_code, qr_code, qr_url,
                idempotency_key, expires_at, paid_at, trace_id
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
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
        .bind(
            input
                .idempotency_key
                .as_deref()
                .filter(|s| !s.trim().is_empty()),
        )
        .bind(expires_at)
        .bind(paid_at)
        .bind(&input.trace_id)
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
                    shipping_fee_vnd, platform_discount_vnd, escrow_status
                )
                VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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
    pub async fn cancel_payment(
        &self,
        payment_id: Option<Uuid>,
        order_group_id: Option<&str>,
        reason: &str,
        trace_id: &str,
    ) -> Result<PaymentWithAllocations> {
        let mut tx = self.pool.begin().await?;

        let payment = if let Some(pid) = payment_id {
            sqlx::query_as::<_, PaymentRecord>("SELECT * FROM payments WHERE id = $1 FOR UPDATE")
                .bind(pid)
                .fetch_optional(&mut *tx)
                .await?
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
                payment_id: parsed_payment_id,
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
        if !payload.is_failed_event() {
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

        let decision = if payment.as_ref().is_some_and(|p| p.method == "COD") {
            WebhookDecision::QueueReconciliation {
                reason: "unexpected_cod_webhook",
            }
        } else {
            decide_webhook_action(
                payment.as_ref().map(|p| p.status.as_str()),
                payment.as_ref().map(|p| p.amount_vnd),
                payload.amount_vnd,
                payload.is_failed_event(),
                order_already_succeeded,
                has_cancelled_alloc,
            )
        };

        match decision {
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
                let updated = confirm_vietqr_payment_tx(
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

    /// Creates an idempotent refund capped at `paid_amount - already_refunded` (UC-PAY04).
    /// Posts reversing double-entry ledger (`escrow_liability -> refund_payable`) and emits `payment.refunded`.
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

        // Locate & lock payment either by explicit payment_id or by order_id / allocation order_id
        let payment = if let Some(pid) = input.payment_id {
            sqlx::query_as::<_, PaymentRecord>("SELECT * FROM payments WHERE id = $1 FOR UPDATE")
                .bind(pid)
                .fetch_optional(&mut *tx)
                .await?
        } else if !input.order_id.trim().is_empty() {
            sqlx::query_as::<_, PaymentRecord>(
                r#"
                SELECT p.*
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
                FOR UPDATE OF p
                "#,
            )
            .bind(input.order_id.trim())
            .fetch_optional(&mut *tx)
            .await?
        } else {
            return Err(PaymentError::Validation(
                "payment_id or order_id is required for refund".to_string(),
            ));
        }
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

        let refund_insert = sqlx::query_as::<_, RefundRecord>(
            r#"
            INSERT INTO refunds (
                payment_id, order_id, amount_vnd, reason, status, idempotency_key,
                is_late_payment, affects_escrow
            )
            VALUES ($1, $2, $3, $4, 'pending', $5, FALSE, TRUE)
            RETURNING *
            "#,
        )
        .bind(payment.id)
        .bind(&order_id)
        .bind(input.amount_vnd)
        .bind(&input.reason)
        .bind(&input.idempotency_key)
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

        let draft = if let Some((shop_id, from_pending, from_available)) = post_settlement_clawback
        {
            build_post_settlement_refund_tx(
                refund.id,
                payment.id,
                &shop_id,
                from_pending,
                from_available,
                &refund.reason,
            )?
        } else {
            build_refund_tx(refund.id, payment.id, refund.amount_vnd, &refund.reason)
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

        if remaining_after == 0 && remaining_active_allocs == 0 {
            sqlx::query(
                "UPDATE payments SET status = 'refunded', updated_at = NOW() WHERE id = $1",
            )
            .bind(payment.id)
            .execute(&mut *tx)
            .await?;
        }

        insert_outbox_event(
            &mut tx,
            "payment.refunded",
            &order_id,
            &input.trace_id,
            json!({
                "refund_id": refund.id.to_string(),
                "payment_id": payment.id.to_string(),
                "order_id": order_id,
                "user_id": payment.user_id,
                "amount_vnd": refund.amount_vnd,
                "reason": refund.reason,
            }),
        )
        .await?;

        tx.commit().await?;
        info!(
            trace_id = %input.trace_id,
            refund_id = %refund.id,
            payment_id = %payment.id,
            amount_vnd = refund.amount_vnd,
            "pending refund created and accrued in double-entry ledger"
        );
        Ok(CreateRefundOutput {
            refund,
            already_existed: false,
        })
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
        if refund_status != "pending" {
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
        if refund.status != "pending" {
            return Err(PaymentError::Conflict(format!(
                "cannot settle refund in status '{}'",
                refund.status
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

        let draft = build_refund_settle_tx(updated.id, updated.amount_vnd);
        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
            self.metrics.record_ledger_tx("refund_settle");
        }

        let payment_user_id: String =
            sqlx::query_scalar("SELECT user_id FROM payments WHERE id = $1")
                .bind(updated.payment_id)
                .fetch_one(&mut *tx)
                .await?;

        insert_outbox_event(
            &mut tx,
            "payment.refund_settled",
            &updated.order_id,
            &input.trace_id,
            json!({
                "refund_id": updated.id.to_string(),
                "payment_id": updated.payment_id.to_string(),
                "order_id": updated.order_id,
                "user_id": payment_user_id,
                "amount_vnd": updated.amount_vnd,
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
    /// `settled_at + PAYMENT_SELLER_HOLD_HOURS <= NOW()` on PostgreSQL clock (PAY-B7 / P3-2).
    /// Safe across multiple replicas (`FOR UPDATE OF a SKIP LOCKED`), reuses
    /// `release_pending_allocation_tx`, and preserves lock order (`allocation -> seller ledger accounts`).
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

        let draft = build_seller_payout_tx(payout.id, shop_id, amount_vnd)?;
        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
            self.metrics.record_ledger_tx("payout");
        }

        tx.commit().await?;
        Ok(payout)
    }

    pub async fn get_ledger_summary(&self) -> Result<LedgerSummary> {
        ledger::get_ledger_summary(&self.pool).await
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

        let active_configs = sqlx::query_as::<_, PaymentFeeConfigRecord>(
            r#"
            SELECT DISTINCT ON (payment_method)
                id, seq, payment_method, fee_bps, effective_from, created_by, note, created_at
            FROM payment_fee_configs
            WHERE effective_from <= $1
              AND ($2::TEXT IS NULL OR payment_method = $2)
            ORDER BY payment_method ASC, effective_from DESC, seq DESC
            "#,
        )
        .bind(at_time)
        .bind(method_str)
        .fetch_all(&self.pool)
        .await?;

        let history = if filter.include_history {
            sqlx::query_as::<_, PaymentFeeConfigRecord>(
                r#"
                SELECT id, seq, payment_method, fee_bps, effective_from, created_by, note, created_at
                FROM payment_fee_configs
                WHERE ($1::TEXT IS NULL OR payment_method = $1)
                ORDER BY effective_from DESC, seq DESC
                "#,
            )
            .bind(method_str)
            .fetch_all(&self.pool)
            .await?
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

                if locked.method != "VIETQR" {
                    return Err(PaymentError::Conflict(
                        "can only assign mismatched transfer to a VIETQR payment".to_string(),
                    ));
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

                let confirmed = confirm_vietqr_payment_tx(
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
                        let draft = build_reconciliation_refund_tx(
                            &rf_key,
                            &item.provider_tx_id,
                            excess_vnd,
                            &format!(
                                "Overpayment excess {excess_vnd} VND for provider_tx_id {}",
                                item.provider_tx_id
                            ),
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
                let draft = build_reconciliation_refund_tx(
                    &rf_key,
                    &item.provider_tx_id,
                    item.actual_amount_vnd,
                    &format!(
                        "Mismatched transfer {} booked to refund_payable via reconciliation item {}",
                        item.provider_tx_id, item.id
                    ),
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

    /// Background scanner for expired pending VietQR payments (UC-PAY06).
    /// Queries provider outside any DB transaction and processes each candidate in its own
    /// short group-locked transaction (`ORDER BY id ASC FOR UPDATE`) so one conflict never
    /// rolls back the batch (review-B P1-4 / P1-5).
    pub async fn scan_expired_payments(&self) -> Result<usize> {
        let expired_candidates = sqlx::query_as::<_, PaymentRecord>(
            r#"
            SELECT * FROM payments
            WHERE status = 'pending'
              AND method = 'VIETQR'
              AND expires_at IS NOT NULL
              AND expires_at <= NOW()
            ORDER BY expires_at ASC
            LIMIT 50
            "#,
        )
        .fetch_all(&self.pool)
        .await?;

        if expired_candidates.is_empty() {
            return Ok(0);
        }

        // P1-2: Fetch SePay/Mock batch feed ONCE per scanner tick instead of N+1 per payment.
        // On feed failure, defer expiry safely and log warning.
        let batch_feed_by_code: Option<HashMap<String, Vec<ProviderTransaction>>> = match self
            .vietqr
            .list_recent_transactions_for_expiry()
            .await
        {
            Ok(Some(list)) => {
                let mut by_code: HashMap<String, Vec<ProviderTransaction>> = HashMap::new();
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
                warn!(
                    candidate_count = expired_candidates.len(),
                    error = %e,
                    "provider batch feed unreachable during expiration scan; deferring expiry for this tick"
                );
                return Ok(0);
            }
        };

        let mut expired_count = 0usize;

        for candidate in expired_candidates {
            let matching_txs: Vec<ProviderTransaction> = match &batch_feed_by_code {
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
            };

            // P1-2: Prioritize exact-amount transfers ahead of mismatched transfers, and skip
            // any already-accounted transfer so an earlier wrong/refunded transfer never hides
            // a valid subsequent payment for the same order code.
            let amounts: Vec<i64> = matching_txs.iter().map(|t| t.amount_vnd).collect();
            let ordered_indices = order_expiry_candidate_indices(&amounts, candidate.amount_vnd);

            let mut handled = false;
            for idx in ordered_indices {
                let provider_tx = &matching_txs[idx];
                let mut tx = self.pool.begin().await?;
                // P0-1: Acquire per-provider_tx_id advisory lock BEFORE payment group lock.
                lock_provider_tx_xact(&mut tx, &provider_tx.provider_tx_id).await?;
                let group =
                    lock_payment_group_tx(&mut tx, &candidate.order_id, &candidate.order_group_id)
                        .await?;
                let Some(payment) = group.iter().find(|p| p.id == candidate.id).cloned() else {
                    tx.commit().await?;
                    handled = true;
                    break;
                };
                if payment.status != "pending" {
                    tx.commit().await?;
                    handled = true;
                    break;
                }

                let acct =
                    check_provider_tx_accounting_tx(&mut tx, &provider_tx.provider_tx_id).await?;
                if acct.is_accounted() {
                    info!(
                        payment_id = %payment.id,
                        provider_tx_id = %provider_tx.provider_tx_id,
                        "candidate provider_tx_id is already accounted for; checking remaining transfers"
                    );
                    tx.commit().await?;
                    continue;
                }

                if provider_tx.amount_vnd == payment.amount_vnd {
                    let order_already_succeeded = group.iter().any(|p| {
                        p.id != payment.id && matches!(p.status.as_str(), "succeeded" | "refunded")
                    });
                    let has_cancelled_alloc =
                        has_cancelled_allocation_tx(&mut tx, payment.id).await?;
                    if order_already_succeeded || has_cancelled_alloc {
                        let next_status = if has_cancelled_alloc {
                            "cancelled"
                        } else {
                            "superseded"
                        };
                        record_late_payment_refund_tx(
                            &mut tx,
                            &payment,
                            &provider_tx.provider_tx_id,
                            provider_tx.amount_vnd,
                            Some(next_status),
                            "auto_closed:expiry_scan_late_refund",
                            &self.metrics,
                        )
                        .await?;
                        tx.commit().await?;
                    } else {
                        let confirmed = confirm_vietqr_payment_tx(
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
                )
                .await?;
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

            let updated = sqlx::query_as::<_, PaymentRecord>(
                r#"
                UPDATE payments
                SET status = 'expired', updated_at = NOW()
                WHERE id = $1 AND status = 'pending'
                RETURNING *
                "#,
            )
            .bind(payment.id)
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
                }),
            )
            .await?;

            tx.commit().await?;
            self.emit_sse(&updated);
            expired_count += 1;
        }

        if expired_count > 0 {
            info!(
                expired_count = expired_count,
                "expired pending VietQR payments"
            );
        }
        Ok(expired_count)
    }

    /// Runs reconciliation comparing local VietQR payments vs provider transaction feed (UC-PAY07 / PAY-B8).
    /// - Evaluates `run_date` strictly in `Asia/Ho_Chi_Minh` (`vn_day_utc_bounds`), rejecting future dates (400).
    /// - In `VietQrMode::Mock`, reads recorded provider transactions from `webhook_events` in Postgres (bounded by `received_at`).
    /// - On ANY error after creating the `reconciliation_runs` row, transitions `status = 'failed'` (P1-1).
    /// - Processes each matched provider transaction in its own short advisory-locked + group-locked transaction (P0-1 / P2-5).
    pub async fn run_reconciliation(&self, run_date: NaiveDate) -> Result<ReconciliationReport> {
        let today_vn = vn_today(Utc::now());
        if run_date > today_vn {
            return Err(PaymentError::Validation(format!(
                "reconciliation date {run_date} cannot be in the future (today in Asia/Ho_Chi_Minh is {today_vn})"
            )));
        }

        let provider_name = self.vietqr.mode().as_str().to_string();
        let run_id: Uuid = sqlx::query_scalar(
            r#"
            INSERT INTO reconciliation_runs (provider, run_date, status)
            VALUES ($1, $2, 'running')
            RETURNING id
            "#,
        )
        .bind(&provider_name)
        .bind(run_date)
        .fetch_one(&self.pool)
        .await?;

        match self
            .execute_reconciliation_run(run_id, &provider_name, run_date)
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
                            )
                            .await?;
                        }
                        ReconciliationDecision::AutoHealAndMatch => {
                            let healed = confirm_vietqr_payment_tx(
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
            if !matched_payment_ids.contains(&local.id) {
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
                    )
                    .await?,
                );
            }
        }

        let run = sqlx::query_as::<_, ReconciliationRunRecord>(
            r#"
            UPDATE reconciliation_runs
            SET matched_count = $1,
                missing_local_count = $2,
                missing_provider_count = $3,
                amount_mismatch_count = $4,
                auto_healed_count = $5,
                status = 'completed',
                completed_at = NOW()
            WHERE id = $6
            RETURNING *
            "#,
        )
        .bind(matched_count)
        .bind(missing_local_count)
        .bind(missing_provider_count)
        .bind(amount_mismatch_count)
        .bind(auto_healed_count)
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

        let cod_sse =
            collect_cod_order_allocations_tx(&mut tx, order_id, None, trace_id, &self.metrics)
                .await?;
        let _ = settle_order_to_pending_tx(&mut tx, order_id, None, &self.metrics).await?;
        tx.commit().await?;
        if let Some(ref u) = cod_sse {
            self.emit_sse(u);
        }
        Ok(true)
    }

    /// Atomically claims `event_id` in `processed_nats_events` and records COD cash collection
    /// on `order.delivered` (R2 / PAY-B4):
    /// - Posts Debit `cod_receivable` / Credit `escrow_liability` for this sub-order's `P`.
    /// - Transitions this sub-order's allocation `awaiting_collection -> held`.
    /// - Emits `payment.succeeded` for the delivered sub-order.
    /// - Transitions parent COD payment to `'succeeded'` once all active allocations are collected.
    pub async fn process_order_delivered_event(
        &self,
        event_id: &str,
        order_id: &str,
        trace_id: &str,
    ) -> Result<bool> {
        let mut tx = self.pool.begin().await?;
        if !claim_nats_event_tx(&mut tx, event_id, "order.delivered").await? {
            tx.commit().await?;
            return Ok(false);
        }

        let sse_updated =
            collect_cod_order_allocations_tx(&mut tx, order_id, None, trace_id, &self.metrics)
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

        let payment_opt = sqlx::query_as::<_, PaymentRecord>(
            r#"
            SELECT p.*
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
            FOR UPDATE OF p
            "#,
        )
        .bind(order_id)
        .fetch_optional(&mut *tx)
        .await?;

        let Some(payment) = payment_opt else {
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
            sse_updated =
                collect_cod_order_allocations_tx(&mut tx, order_id, None, trace_id, &self.metrics)
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
            "succeeded" if payment.method == "VIETQR" => {
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

                let total_alloc_count: i64 = sqlx::query_scalar(
                    "SELECT COUNT(*)::BIGINT FROM payment_shop_allocations WHERE payment_id = $1",
                )
                .bind(payment.id)
                .fetch_one(&mut *tx)
                .await?;

                let suborder_held_count: i64 = sqlx::query_scalar(
                    r#"
                    SELECT COUNT(*)::BIGINT
                    FROM payment_shop_allocations
                    WHERE payment_id = $1
                      AND order_id = $2
                      AND escrow_status = 'held'
                    "#,
                )
                .bind(payment.id)
                .bind(order_id)
                .fetch_one(&mut *tx)
                .await?;

                let suborder_alloc_total: i64 = sqlx::query_scalar(
                    r#"
                    SELECT COALESCE(SUM(subtotal_vnd + shipping_fee_vnd - platform_discount_vnd), 0)::BIGINT
                    FROM payment_shop_allocations
                    WHERE payment_id = $1
                      AND order_id = $2
                      AND escrow_status = 'held'
                    "#,
                )
                .bind(payment.id)
                .bind(order_id)
                .fetch_one(&mut *tx)
                .await?;

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
                    let inserted_refund = sqlx::query_as::<_, RefundRecord>(
                        r#"
                        INSERT INTO refunds (
                            payment_id, order_id, amount_vnd, reason, status, idempotency_key,
                            is_late_payment, affects_escrow
                        )
                        VALUES ($1, $2, $3, 'order_cancelled_auto_refund', 'pending', $4, FALSE, TRUE)
                        ON CONFLICT (idempotency_key) DO NOTHING
                        RETURNING *
                        "#,
                    )
                    .bind(payment.id)
                    .bind(order_id)
                    .bind(refund_amount)
                    .bind(&refund_key)
                    .fetch_optional(&mut *tx)
                    .await?;

                    if let Some(refund) = inserted_refund {
                        let draft = build_refund_tx(
                            refund.id,
                            payment.id,
                            refund.amount_vnd,
                            &refund.reason,
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
                            sqlx::query(
                                "UPDATE payments SET status = 'refunded', updated_at = NOW() WHERE id = $1",
                            )
                            .bind(payment.id)
                            .execute(&mut *tx)
                            .await?;
                        }

                        insert_outbox_event(
                            &mut tx,
                            "payment.refunded",
                            order_id,
                            trace_id,
                            json!({
                                "refund_id": refund.id.to_string(),
                                "payment_id": payment.id.to_string(),
                                "order_id": order_id,
                                "user_id": payment.user_id,
                                "amount_vnd": refund.amount_vnd,
                                "reason": refund.reason,
                            }),
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
        Ok(true)
    }
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

/// Idempotently collects COD funds (`Debit cod_receivable / Credit escrow_liability`) for
/// `awaiting_collection` allocations matching `order_id` (and optional `shop_filter`), transitions
/// allocations to `held`, emits `payment.succeeded`, and syncs parent COD payment status.
/// Shared by `process_order_delivered_event` and `release_escrow` (P0-1).
async fn collect_cod_order_allocations_tx(
    tx: &mut Transaction<'_, Postgres>,
    order_id: &str,
    shop_filter: Option<&str>,
    trace_id: &str,
    metrics: &Metrics,
) -> Result<Option<PaymentRecord>> {
    let payment_opt = sqlx::query_as::<_, PaymentRecord>(
        r#"
        SELECT p.*
        FROM payments p
        LEFT JOIN payment_shop_allocations a ON a.payment_id = p.id
        WHERE (a.order_id = $1 OR p.order_id = $1 OR p.order_group_id = $1)
          AND p.method = 'COD'
          AND p.status IN ('pending', 'succeeded')
        ORDER BY p.created_at DESC
        LIMIT 1
        FOR UPDATE OF p
        "#,
    )
    .bind(order_id)
    .fetch_optional(&mut **tx)
    .await?;

    let Some(payment) = payment_opt else {
        return Ok(None);
    };

    let awaiting_allocs = sqlx::query_as::<_, ShopAllocationRecord>(
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
    .await?;

    if awaiting_allocs.is_empty() {
        // Legacy single-order COD without shop_allocations rows
        let total_allocs: i64 = sqlx::query_scalar(
            "SELECT COUNT(*)::BIGINT FROM payment_shop_allocations WHERE payment_id = $1",
        )
        .bind(payment.id)
        .fetch_one(&mut **tx)
        .await?;

        if total_allocs == 0 && payment.status == "pending" && payment.amount_vnd > 0 {
            let draft = build_cod_collect_tx(payment.id, order_id, None, payment.amount_vnd);
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
    let mut per_order_shop_collected: Vec<(String, String, i64)> = Vec::new();
    let mut per_order_collected: Vec<(String, i64)> = Vec::new();
    for alloc in awaiting_allocs {
        let sub_payable = allocation_buyer_payable_vnd(&alloc)?;
        sqlx::query(
            r#"
            UPDATE payment_shop_allocations
            SET escrow_status = 'held'
            WHERE id = $1
            "#,
        )
        .bind(alloc.id)
        .execute(&mut **tx)
        .await?;

        if let Some(entry) = per_order_shop_collected
            .iter_mut()
            .find(|(oid, sid, _)| oid == &alloc.order_id && sid == &alloc.shop_id)
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

    for (collected_order_id, collected_shop_id, collected_vnd) in &per_order_shop_collected {
        if *collected_vnd > 0 {
            let draft = build_cod_collect_tx(
                payment.id,
                collected_order_id,
                Some(collected_shop_id),
                *collected_vnd,
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
) -> Result<()> {
    sqlx::query(
        r#"
        INSERT INTO reconciliation_queue (
            webhook_event_id, provider, provider_tx_id, expected_payment_id,
            order_ref, expected_amount_vnd, actual_amount_vnd, reason, occurred_at
        )
        SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9
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
    .execute(&mut **tx)
    .await?;
    Ok(())
}

async fn record_late_payment_refund_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment: &PaymentRecord,
    provider_tx_id: &str,
    amount_vnd: i64,
    pending_next_status: Option<&str>,
    accounting_source: &str,
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

    let ledger_draft = build_late_payment_refund_tx(provider_tx_id, amount_vnd);
    if ledger::post_transaction(tx, &ledger_draft).await?.is_some() {
        metrics.record_ledger_tx("late_payment_refund");
    }

    // review2-P P1-1: single canonical idempotency key across webhook, expiry scanner, and reconciliation.
    let refund_key = late_payment_refund_key(provider_tx_id);
    let refund = sqlx::query_as::<_, RefundRecord>(
        r#"
        INSERT INTO refunds (
            payment_id, order_id, amount_vnd, reason, status, idempotency_key,
            is_late_payment, affects_escrow
        )
        VALUES ($1, $2, $3, 'late_payment', 'pending', $4, TRUE, FALSE)
        ON CONFLICT (idempotency_key) DO UPDATE SET updated_at = NOW()
        RETURNING *
        "#,
    )
    .bind(payment.id)
    .bind(&payment.order_id)
    .bind(amount_vnd)
    .bind(&refund_key)
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

    // R3 / PAY-B6: Never emit payment.succeeded or payment.refunded for late/superseded payments.
    Ok(())
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
            WHERE order_group_id = $1 OR order_id = $1
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
async fn confirm_vietqr_payment_tx(
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
            updated_at = NOW()
        WHERE id = $3 AND status = 'pending'
        RETURNING *
        "#,
    )
    .bind(provider_tx_id)
    .bind(paid_at)
    .bind(payment.id)
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
        let draft = build_vietqr_confirm_tx(updated.id, updated.amount_vnd);
        if ledger::post_transaction(tx, &draft).await?.is_some() {
            metrics.record_ledger_tx("payment_vietqr");
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
                  AND a.settled_at > NOW() - make_interval(hours => $3)
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
        let draft = build_escrow_release_tx(&alloc.order_id, &alloc.shop_id, release_amount)?;
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
            let draft = build_escrow_settle_tx(
                &alloc.order_id,
                &alloc.shop_id,
                split.escrow_debit_vnd,
                split.platform_promo_debit_vnd,
                split.seller_net_vnd,
                split.commission_vnd,
                split.shipping_fee_vnd,
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
) -> Result<ReconciliationItemRecord> {
    let rec = sqlx::query_as::<_, ReconciliationItemRecord>(
        r#"
        INSERT INTO reconciliation_items (
            run_id, classification, payment_id, provider_tx_id,
            local_amount_vnd, provider_amount_vnd, details
        )
        VALUES ($1, $2, $3, $4, $5, $6, $7)
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
    .fetch_one(&mut **tx)
    .await?;
    Ok(rec)
}
