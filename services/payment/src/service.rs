use std::collections::{HashMap, HashSet};

use chrono::{Duration, NaiveDate, Utc};
use serde::{Deserialize, Serialize};
use serde_json::json;
use sqlx::{PgPool, Postgres, Row, Transaction};
use tokio::sync::broadcast;
use tracing::{info, warn};
use uuid::Uuid;

use crate::config::Config;
use crate::domain::{
    allocation_buyer_payable_vnd, compute_post_settlement_clawback, compute_settlement_split,
    compute_suborder_refund_amount, decide_reconciliation_match_action,
    decide_return_allocation_action, late_payment_refund_key, order_cancel_refund_key,
    plan_initial_payment, resolve_cod_payment_status, select_payment_for_group_action,
    validate_allocations_sum, validate_group_can_create_payment,
    validate_payment_idempotency_match, validate_refund_eligibility,
    validate_refund_idempotency_match, validate_suborder_refund_cap, CreatePaymentInput,
    CreateRefundInput, CreateRefundOutput, GroupPaymentSelection, PaymentMethod, PaymentRecord,
    PaymentSseEvent, PaymentWithAllocations, ReconciliationDecision, ReconciliationItemRecord,
    ReconciliationQueueItem, ReconciliationReport, ReconciliationRunRecord, RefundRecord,
    ReturnAllocationAction, SellerPayoutRecord, ShopAllocationRecord,
};
use crate::error::{PaymentError, Result};
use crate::ledger::{
    self, build_cod_collect_tx, build_escrow_release_tx, build_escrow_settle_tx,
    build_late_payment_refund_tx, build_post_settlement_refund_tx, build_refund_tx,
    build_seller_payout_tx, build_vietqr_confirm_tx, LedgerSummary, SellerBalance,
};
use crate::metrics::Metrics;
use crate::telemetry::mask_bank_account;
use crate::vietqr::{ProviderTransaction, VietQrAdapter};
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

        // Re-check idempotency inside transaction (P0-2: verify caller & params match)
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

        // Serialize concurrent create/confirm operations for this order / checkout group (P1-5).
        let existing_group_payments =
            lock_payment_group_tx(&mut tx, &order_id, &order_group_id).await?;

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
            Err(err) if is_unique_violation(&err, "idempotency_key") => {
                // Concurrent duplicate idempotency_key race: roll back and validate winning row (P0-2).
                let _ = tx.rollback().await;
                if let Some(ref key) = input.idempotency_key {
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
                return Err(PaymentError::Database(err));
            }
            Err(err)
                if is_unique_violation(&err, "idx_payments_one_succeeded_per_order")
                    || is_unique_violation(&err, "idx_payments_one_succeeded_per_group") =>
            {
                let _ = tx.rollback().await;
                return Err(PaymentError::Conflict(format!(
                    "order {order_id} already has a succeeded payment"
                )));
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

        if payment.status == "cancelled" {
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
        payload: IncomingWebhookPayload,
        raw_json: serde_json::Value,
        default_provider: &str,
        trace_id: &str,
    ) -> Result<WebhookProcessResult> {
        payload.validate()?;

        let provider = payload
            .provider
            .clone()
            .filter(|s| !s.trim().is_empty())
            .unwrap_or_else(|| default_provider.to_string());

        let parsed_payment_id = payload.parsed_payment_id();
        let extracted_code = payload.extracted_order_code();
        let order_ref = payload.effective_order_ref().map(ToString::to_string);

        let mut tx = self.pool.begin().await?;

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
                sqlx::query(
                    r#"
                    INSERT INTO reconciliation_queue (
                        webhook_event_id, provider, provider_tx_id, expected_payment_id,
                        order_ref, expected_amount_vnd, actual_amount_vnd, reason
                    )
                    VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
                    "#,
                )
                .bind(webhook_event_id)
                .bind(&provider)
                .bind(&payload.provider_tx_id)
                .bind(payment.as_ref().map(|p| p.id))
                .bind(order_ref.as_deref().or(extracted_code.as_deref()))
                .bind(payment.as_ref().map(|p| p.amount_vnd))
                .bind(payload.amount_vnd)
                .bind(reason)
                .execute(&mut *tx)
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

                    let row = sqlx::query_as::<_, PaymentRecord>(
                        r#"
                        UPDATE payments
                        SET status = 'failed', provider_tx_id = $1, updated_at = NOW()
                        WHERE id = $2
                        RETURNING *
                        "#,
                    )
                    .bind(&payload.provider_tx_id)
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

                self.vietqr.record_mock_transaction(ProviderTransaction {
                    provider_tx_id: payload.provider_tx_id.clone(),
                    provider_order_code: updated.provider_order_code.clone(),
                    amount_vnd: updated.amount_vnd,
                    status: "succeeded".to_string(),
                    occurred_at: Utc::now(),
                });

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
                payment_id, order_id, amount_vnd, reason, status, idempotency_key, is_late_payment
            )
            VALUES ($1, $2, $3, $4, 'completed', $5, FALSE)
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
            "refund created and posted to double-entry ledger"
        );
        Ok(CreateRefundOutput {
            refund,
            already_existed: false,
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

    /// Releases escrow for `order_id` (optionally scoped to `shop_id`) to `seller_available:<shop_id>`.
    /// If allocations are still `held`, settles `escrow_liability -> seller_pending:<shop_id>` first,
    /// then moves `seller_pending:<shop_id> -> seller_available:<shop_id>`. Fully idempotent.
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

        // P0-1: If sync ReleaseEscrow arrives before async `order.delivered` on a COD order,
        // idempotently collect COD (`cod-collect:{payment_id}:{order_id}` + outbox `payment.succeeded`)
        // before settling, since ReleaseEscrow is only called once an order passed DELIVERED -> COMPLETED.
        let cod_sse =
            collect_cod_order_allocations_tx(&mut tx, order_id, shop_filter, "", &self.metrics)
                .await?;

        // review2-P P2-4: Lock target allocations in ascending id order and acquire FOR UPDATE
        // locks on each shop's ledger accounts (`seller_available`, `seller_pending`) BEFORE
        // `settle_order_to_pending_tx` inserts into `ledger_entries` (which would otherwise take
        // `FOR KEY SHARE` on `seller_pending` first and deadlock on lock upgrade to `FOR UPDATE`).
        let mut target_shop_ids: Vec<String> = sqlx::query_scalar(
            r#"
            SELECT a.shop_id
            FROM payment_shop_allocations a
            JOIN payments p ON p.id = a.payment_id
            WHERE (a.order_id = $1 OR p.order_id = $1 OR p.order_group_id = $1)
              AND ($2::TEXT IS NULL OR a.shop_id = $2)
              AND (p.status IN ('succeeded', 'refunded') OR (p.method = 'COD' AND p.status = 'pending'))
              AND a.escrow_status IN ('held', 'pending_release')
            ORDER BY a.id ASC
            FOR UPDATE OF a
            "#,
        )
        .bind(order_id)
        .bind(shop_filter)
        .fetch_all(&mut *tx)
        .await?;
        target_shop_ids.sort();
        target_shop_ids.dedup();
        for shop_id in &target_shop_ids {
            let _ = ledger::lock_and_get_seller_balances_tx(&mut tx, shop_id).await?;
        }

        // First settle any `held` allocations for this order to `pending_release`
        let _ = settle_order_to_pending_tx(&mut tx, order_id, shop_filter, &self.metrics).await?;

        let pending_allocs = sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT a.*
            FROM payment_shop_allocations a
            JOIN payments p ON p.id = a.payment_id
            WHERE (a.order_id = $1 OR p.order_id = $1 OR p.order_group_id = $1)
              AND ($2::TEXT IS NULL OR a.shop_id = $2)
              AND (p.status IN ('succeeded', 'refunded') OR (p.method = 'COD' AND p.status = 'pending'))
              AND a.escrow_status = 'pending_release'
            ORDER BY a.id ASC
            FOR UPDATE OF a
            "#,
        )
        .bind(order_id)
        .bind(shop_filter)
        .fetch_all(&mut *tx)
        .await?;

        if pending_allocs.is_empty() {
            let already_released: bool = sqlx::query_scalar(
                r#"
                SELECT EXISTS(
                    SELECT 1
                    FROM payment_shop_allocations a
                    JOIN payments p ON p.id = a.payment_id
                    WHERE (a.order_id = $1 OR p.order_id = $1 OR p.order_group_id = $1)
                      AND ($2::TEXT IS NULL OR a.shop_id = $2)
                      AND a.escrow_status = 'released'
                )
                "#,
            )
            .bind(order_id)
            .bind(shop_filter)
            .fetch_one(&mut *tx)
            .await?;

            tx.commit().await?;
            if let Some(ref u) = cod_sse {
                self.emit_sse(u);
            }
            return Ok(EscrowReleaseResult {
                order_id: order_id.to_string(),
                released: already_released,
                released_amount_vnd: 0,
                already_released,
            });
        }

        let mut total_released_vnd = 0_i64;
        for alloc in pending_allocs {
            let (payment_order_id, alloc_count) =
                fetch_payment_order_meta_tx(&mut tx, alloc.payment_id).await?;
            let suborder_refunded = sum_suborder_refunded_tx(
                &mut tx,
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

            let (pending_bal, _) =
                ledger::lock_and_get_seller_balances_tx(&mut tx, &alloc.shop_id).await?;
            let release_amount = split.seller_net_vnd.min(pending_bal.max(0));

            if release_amount > 0 {
                let draft =
                    build_escrow_release_tx(&alloc.order_id, &alloc.shop_id, release_amount)?;
                if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
                    self.metrics.record_ledger_tx("escrow_release");
                }
                total_released_vnd += release_amount;
            }

            sqlx::query(
                r#"
                UPDATE payment_shop_allocations
                SET escrow_status = 'released', released_at = NOW()
                WHERE id = $1
                "#,
            )
            .bind(alloc.id)
            .execute(&mut *tx)
            .await?;
        }

        tx.commit().await?;
        if let Some(ref u) = cod_sse {
            self.emit_sse(u);
        }

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

    pub async fn list_reconciliation_queue(&self) -> Result<Vec<ReconciliationQueueItem>> {
        let rows = sqlx::query_as::<_, ReconciliationQueueItem>(
            "SELECT * FROM reconciliation_queue ORDER BY created_at DESC LIMIT 100",
        )
        .fetch_all(&self.pool)
        .await?;
        Ok(rows)
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

        let mut expired_count = 0usize;

        for candidate in expired_candidates {
            let provider_status = match self
                .vietqr
                .check_payment_status(&candidate.provider_order_code)
                .await
            {
                Ok(res) => res,
                Err(e) => {
                    warn!(
                        payment_id = %candidate.id,
                        error = %e,
                        "provider unreachable during expiration check; deferring expiry"
                    );
                    continue;
                }
            };

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

            let order_already_succeeded = group.iter().any(|p| {
                p.id != payment.id && matches!(p.status.as_str(), "succeeded" | "refunded")
            });
            let has_cancelled_alloc = has_cancelled_allocation_tx(&mut tx, payment.id).await?;

            match provider_status {
                Some(provider_tx) if provider_tx.amount_vnd == payment.amount_vnd => {
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
                            &self.metrics,
                        )
                        .await?;
                        tx.commit().await?;
                    } else {
                        let confirmed = confirm_vietqr_payment_tx(
                            &mut tx,
                            &payment,
                            &provider_tx.provider_tx_id,
                            &payment.trace_id,
                            &self.metrics,
                        )
                        .await?;
                        tx.commit().await?;
                        self.emit_sse(&confirmed);
                    }
                    continue;
                }
                Some(_) => {
                    tx.commit().await?;
                    continue;
                }
                None => {}
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

    /// Runs reconciliation comparing local VietQR payments vs provider transaction feed (UC-PAY07).
    /// review2-P P2-5: processes each matched provider transaction in its own short group-locked
    /// transaction (in deterministic `(order_group_id, payment_id)` order) so concurrent
    /// reconciliation runs cannot AB-BA deadlock across groups and one item error never rolls back
    /// the whole batch.
    pub async fn run_reconciliation(&self, run_date: NaiveDate) -> Result<ReconciliationReport> {
        let provider_name = self.vietqr.mode().as_str().to_string();
        // Filter provider transactions to `run_date` to match `local_payments` (`created_at::date = $1`).
        let provider_txs: Vec<ProviderTransaction> = self
            .vietqr
            .list_provider_transactions()
            .await?
            .into_iter()
            .filter(|ptx| ptx.occurred_at.date_naive() == run_date)
            .collect();

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

        let local_payments = sqlx::query_as::<_, PaymentRecord>(
            r#"
            SELECT * FROM payments
            WHERE method = 'VIETQR'
              AND amount_vnd > 0
              AND created_at::date = $1
            "#,
        )
        .bind(run_date)
        .fetch_all(&self.pool)
        .await?;

        let mut by_tx_id: HashMap<String, &PaymentRecord> = HashMap::new();
        let mut by_order_code: HashMap<String, &PaymentRecord> = HashMap::new();
        for p in &local_payments {
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
            match local_opt {
                None => {
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

                            sqlx::query(
                                r#"
                                INSERT INTO reconciliation_queue (
                                    provider, provider_tx_id, expected_payment_id, order_ref,
                                    expected_amount_vnd, actual_amount_vnd, reason
                                )
                                VALUES ($1, $2, $3, $4, $5, $6, 'reconciliation_amount_mismatch')
                                "#,
                            )
                            .bind(&provider_name)
                            .bind(&ptx.provider_tx_id)
                            .bind(current.id)
                            .bind(&current.order_id)
                            .bind(current.amount_vnd)
                            .bind(ptx.amount_vnd)
                            .execute(&mut *tx)
                            .await?;
                        }
                        ReconciliationDecision::AutoHealAndMatch => {
                            let healed = confirm_vietqr_payment_tx(
                                &mut tx,
                                &current,
                                &ptx.provider_tx_id,
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
        for local in &local_payments {
            if local.status == "succeeded" && !matched_payment_ids.contains(&local.id) {
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
                        "local succeeded payment absent from provider report",
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

    /// Atomically claims `event_id` in `processed_nats_events` and handles `order.returned` (R2 / P2-6):
    /// - Failed delivery (`from_status != RETURN_REQUESTED`) for uncollected COD (`awaiting_collection`)
    ///   OR zero-payable COD/VietQR (`P == 0`, `held`): marks allocation `'cancelled'` and updates parent
    ///   payment status with zero ledger postings.
    /// - Post-delivery returns (`from_status == RETURN_REQUESTED`) or already-collected paid orders:
    ///   deferred to Wave 2 (`ponytail:`) so no incorrect ledger entries are created.
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
                ReturnAllocationAction::DeferPostDeliveryReturn => {
                    // ponytail: post-delivery return refund/reversal (`RETURN_REQUESTED -> RETURNED`)
                    // is deferred to Wave 2 (Đợt 2); leave ledger untouched rather than posting wrong entries.
                    info!(
                        trace_id = %trace_id,
                        payment_id = %payment.id,
                        order_id = %order_id,
                        from_status = ?from_status,
                        escrow_status = %alloc.escrow_status,
                        "post-delivery or paid return deferred to Wave 2"
                    );
                }
            }
        }

        let mut sse_updated: Option<PaymentRecord> = None;
        if cancelled_any {
            if payment.method == "COD" && payment.status == "pending" {
                sse_updated =
                    sync_cod_payment_status_tx(&mut tx, payment.id, &payment.status).await?;
            } else if payment.status == "succeeded" {
                // review2-P P3-9 + ponytail: handles both 0 VND payment -> cancelled AND
                // multi-shop VietQR (paid sibling 100% refunded + 0 VND sub-order returned) -> refunded.
                sse_updated =
                    sync_succeeded_payment_after_alloc_cancel_tx(&mut tx, &payment).await?;
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
                            payment_id, order_id, amount_vnd, reason, status, idempotency_key, is_late_payment
                        )
                        VALUES ($1, $2, $3, 'order_cancelled_auto_refund', 'completed', $4, FALSE)
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

async fn record_late_payment_refund_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment: &PaymentRecord,
    provider_tx_id: &str,
    amount_vnd: i64,
    pending_next_status: Option<&str>,
    metrics: &Metrics,
) -> Result<()> {
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
    let _refund = sqlx::query_as::<_, RefundRecord>(
        r#"
        INSERT INTO refunds (
            payment_id, order_id, amount_vnd, reason, status, idempotency_key, is_late_payment
        )
        VALUES ($1, $2, $3, 'late_payment', 'pending', $4, TRUE)
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

async fn confirm_vietqr_payment_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment: &PaymentRecord,
    provider_tx_id: &str,
    trace_id: &str,
    metrics: &Metrics,
) -> Result<PaymentRecord> {
    let updated = sqlx::query_as::<_, PaymentRecord>(
        r#"
        UPDATE payments
        SET status = 'succeeded',
            provider_tx_id = $1,
            paid_at = NOW(),
            updated_at = NOW()
        WHERE id = $2 AND status = 'pending'
        RETURNING *
        "#,
    )
    .bind(provider_tx_id)
    .bind(payment.id)
    .fetch_optional(&mut **tx)
    .await?
    .ok_or_else(|| {
        PaymentError::Conflict(format!(
            "payment {} is no longer pending and cannot be confirmed",
            payment.id
        ))
    })?;

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
