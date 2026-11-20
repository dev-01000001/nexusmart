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
    compute_allocation_split, compute_suborder_refund_amount, order_cancel_refund_key,
    reconciliation_channel_matches, validate_suborder_refund_cap, ChannelAttribution,
    CreatePaymentInput, CreateRefundInput, CreateRefundOutput, PaymentMethod, PaymentRecord,
    PaymentSseEvent, PaymentWithAllocations, ReconciliationItemRecord, ReconciliationQueueItem,
    ReconciliationReport, ReconciliationRunRecord, RefundRecord, SalesChannel, SellerPayoutRecord,
    ShopAllocationRecord,
};
use crate::error::{PaymentError, Result};
use crate::ledger::{
    self, build_cod_confirm_tx, build_escrow_release_tx, build_escrow_settle_tx,
    build_late_payment_refund_tx, build_refund_tx, build_seller_payout_tx, build_vietqr_confirm_tx,
    LedgerSummary, SellerBalance,
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
    /// - Idempotent when `idempotency_key` matches an existing payment.
    /// - Implements UC-B25 (retry / change method): supersedes any existing `pending` payment for the
    ///   same `order_id` or `order_group_id`; rejects if already succeeded.
    /// - For COD: immediately confirms payment, posts `cod_receivable -> escrow_liability`,
    ///   and enqueues `payment.succeeded` outbox event.
    /// - For VietQR: generates QR link/payload via VietQrAdapter with 15-minute default expiry.
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
        if input.amount_vnd <= 0 {
            return Err(PaymentError::Validation(
                "amount_vnd must be a positive integer VND".to_string(),
            ));
        }

        let channel =
            ChannelAttribution::from_parts(&input.sales_channel, &input.storefront_shop_id)?;
        channel.validate_allocations(&input.shop_allocations)?;

        // Validate shop allocations upfront
        for alloc in &input.shop_allocations {
            let alloc_order_id = alloc
                .order_id
                .as_deref()
                .filter(|s| !s.trim().is_empty())
                .unwrap_or(&order_id);
            compute_allocation_split(
                alloc_order_id,
                &alloc.shop_id,
                alloc.subtotal_vnd,
                alloc.commission_vnd,
                alloc.shipping_fee_vnd,
            )?;
        }

        // Fast idempotency check before external VietQR call
        if let Some(ref key) = input.idempotency_key {
            if !key.trim().is_empty() {
                if let Some(existing) = sqlx::query_as::<_, PaymentRecord>(
                    "SELECT * FROM payments WHERE idempotency_key = $1",
                )
                .bind(key)
                .fetch_optional(&self.pool)
                .await?
                {
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
            match input.method {
                PaymentMethod::Cod => None,
                PaymentMethod::VietQr => Some(input.expires_at.unwrap_or_else(|| {
                    Utc::now() + Duration::seconds(self.config.vietqr_expiry_secs)
                })),
            };

        let (provider, provider_order_code, qr_code, qr_url, initial_status, paid_at) =
            match input.method {
                PaymentMethod::Cod => (
                    "cod".to_string(),
                    VietQrAdapter::generate_order_code(payment_id, &order_id),
                    None,
                    None,
                    "succeeded".to_string(),
                    Some(Utc::now()),
                ),
                PaymentMethod::VietQr => {
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
                        "pending".to_string(),
                        None,
                    )
                }
            };

        let mut tx = self.pool.begin().await?;

        // Re-check idempotency inside transaction
        if let Some(ref key) = input.idempotency_key {
            if !key.trim().is_empty() {
                if let Some(existing) = sqlx::query_as::<_, PaymentRecord>(
                    "SELECT * FROM payments WHERE idempotency_key = $1",
                )
                .bind(key)
                .fetch_optional(&mut *tx)
                .await?
                {
                    let allocations = fetch_allocations_tx(&mut tx, existing.id).await?;
                    tx.commit().await?;
                    return Ok(PaymentWithAllocations {
                        payment: existing,
                        allocations,
                    });
                }
            }
        }

        // UC-B25: check if order / order_group already has a succeeded payment
        let already_succeeded: bool = sqlx::query_scalar(
            r#"
            SELECT EXISTS(
                SELECT 1 FROM payments
                WHERE (order_id = $1 OR order_group_id = $2)
                  AND status = 'succeeded'
            )
            "#,
        )
        .bind(&order_id)
        .bind(&order_group_id)
        .fetch_one(&mut *tx)
        .await?;

        if already_succeeded {
            return Err(PaymentError::Conflict(format!(
                "order {order_id} already has a succeeded payment"
            )));
        }

        // Enforce immutability of sales_channel & storefront_shop_id across payment retries
        let prior_channel: Option<(String, String)> = sqlx::query_as(
            r#"
            SELECT sales_channel, storefront_shop_id
            FROM payments
            WHERE order_id = $1 OR order_group_id = $2
            ORDER BY created_at ASC
            LIMIT 1
            "#,
        )
        .bind(&order_id)
        .bind(&order_group_id)
        .fetch_optional(&mut *tx)
        .await?;

        if let Some((prior_ch, prior_shop)) = prior_channel {
            let prior_attr = ChannelAttribution::from_db(&prior_ch, &prior_shop);
            if prior_attr != channel {
                return Err(PaymentError::Validation(format!(
                    "sales_channel and storefront_shop_id are immutable for order {order_id}"
                )));
            }
        }

        // UC-B25: supersede prior pending intents for the same order / order_group
        if input.supersede_previous {
            sqlx::query(
                r#"
                UPDATE payments
                SET status = 'superseded', updated_at = NOW()
                WHERE (order_id = $1 OR order_group_id = $2) AND status = 'pending'
                "#,
            )
            .bind(&order_id)
            .bind(&order_group_id)
            .execute(&mut *tx)
            .await?;
        } else {
            sqlx::query(
                r#"
                UPDATE payments
                SET status = 'superseded', updated_at = NOW()
                WHERE order_id = $1 AND status = 'pending'
                "#,
            )
            .bind(&order_id)
            .execute(&mut *tx)
            .await?;
        }

        let insert_res = sqlx::query_as::<_, PaymentRecord>(
            r#"
            INSERT INTO payments (
                id, order_group_id, order_id, user_id, method, provider,
                amount_vnd, status, provider_order_code, qr_code, qr_url,
                idempotency_key, expires_at, paid_at, trace_id,
                sales_channel, storefront_shop_id
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
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
        .bind(&initial_status)
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
        .bind(channel.sales_channel.as_str())
        .bind(&channel.storefront_shop_id)
        .fetch_one(&mut *tx)
        .await;

        let payment = match insert_res {
            Ok(p) => p,
            Err(err) if is_unique_violation(&err, "idempotency_key") => {
                // Concurrent duplicate idempotency_key race: roll back and return the winning row cleanly.
                let _ = tx.rollback().await;
                if let Some(ref key) = input.idempotency_key {
                    if let Some(existing) = sqlx::query_as::<_, PaymentRecord>(
                        "SELECT * FROM payments WHERE idempotency_key = $1",
                    )
                    .bind(key)
                    .fetch_optional(&self.pool)
                    .await?
                    {
                        let allocations = fetch_allocations_pool(&self.pool, existing.id).await?;
                        return Ok(PaymentWithAllocations {
                            payment: existing,
                            allocations,
                        });
                    }
                }
                return Err(PaymentError::Database(err));
            }
            Err(err) if is_unique_violation(&err, "idx_payments_one_succeeded_per_order") => {
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
                    payment_id, order_id, shop_id, subtotal_vnd, commission_vnd, shipping_fee_vnd,
                    sales_channel, storefront_shop_id
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
            .bind(channel.sales_channel.as_str())
            .bind(&channel.storefront_shop_id)
            .fetch_one(&mut *tx)
            .await?;
            allocations.push(rec);
        }

        // For COD: immediately post double-entry ledger (cod_receivable -> escrow_liability) and outbox
        if input.method == PaymentMethod::Cod {
            let ledger_draft =
                build_cod_confirm_tx(payment.id, payment.amount_vnd, channel.clone());
            if ledger::post_transaction(&mut tx, &ledger_draft)
                .await?
                .is_some()
            {
                self.metrics.record_ledger_tx("payment_cod");
            }

            insert_outbox_event(
                &mut tx,
                "payment.succeeded",
                &payment.order_group_id,
                &input.trace_id,
                json!({
                    "payment_id": payment.id.to_string(),
                    "order_group_id": payment.order_group_id,
                    "order_id": payment.order_id,
                    "user_id": payment.user_id,
                    "amount_vnd": payment.amount_vnd,
                    "method": "COD",
                    "provider_tx_id": payment.provider_order_code,
                    "sales_channel": payment.sales_channel,
                    "storefront_shop_id": payment.storefront_shop_id,
                }),
            )
            .await?;
        }

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
        } else if let Some(gid) = order_group_id.filter(|s| !s.trim().is_empty()) {
            sqlx::query_as::<_, PaymentRecord>(
                r#"
                SELECT * FROM payments
                WHERE (order_group_id = $1 OR order_id = $1) AND status = 'pending'
                ORDER BY created_at DESC
                LIMIT 1
                FOR UPDATE
                "#,
            )
            .bind(gid)
            .fetch_optional(&mut *tx)
            .await?
        } else {
            return Err(PaymentError::Validation(
                "payment_id or order_group_id is required".to_string(),
            ));
        }
        .ok_or_else(|| PaymentError::NotFound("pending payment not found".to_string()))?;

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

        // Step 2: Locate matching payment row and lock it (`FOR UPDATE`)
        let payment = find_payment_for_webhook_tx(
            &mut tx,
            parsed_payment_id,
            extracted_code.as_deref(),
            payload.order_group_id.as_deref(),
            payload.order_id.as_deref(),
        )
        .await?;

        let order_already_succeeded = if let Some(ref p) = payment {
            sqlx::query_scalar::<_, bool>(
                r#"
                SELECT EXISTS(
                    SELECT 1 FROM payments
                    WHERE (order_id = $1 OR order_group_id = $2)
                      AND status = 'succeeded'
                      AND id <> $3
                )
                "#,
            )
            .bind(&p.order_id)
            .bind(&p.order_group_id)
            .bind(p.id)
            .fetch_one(&mut *tx)
            .await?
        } else {
            false
        };

        let decision = decide_webhook_action(
            payment.as_ref().map(|p| p.status.as_str()),
            payment.as_ref().map(|p| p.amount_vnd),
            payload.amount_vnd,
            payload.is_failed_event(),
            order_already_succeeded,
        );

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
                let channel = ChannelAttribution::from_db(&p.sales_channel, &p.storefront_shop_id);

                let ledger_draft = build_late_payment_refund_tx(
                    &payload.provider_tx_id,
                    payload.amount_vnd,
                    channel,
                );
                if ledger::post_transaction(&mut tx, &ledger_draft)
                    .await?
                    .is_some()
                {
                    self.metrics.record_ledger_tx("late_payment_refund");
                }

                let refund_key = format!("late-webhook:{}", payload.provider_tx_id);
                let refund = sqlx::query_as::<_, RefundRecord>(
                    r#"
                    INSERT INTO refunds (payment_id, order_id, amount_vnd, reason, status, idempotency_key)
                    VALUES ($1, $2, $3, $4, 'pending', $5)
                    ON CONFLICT (idempotency_key) DO UPDATE SET updated_at = NOW()
                    RETURNING *
                    "#,
                )
                .bind(p.id)
                .bind(&p.order_id)
                .bind(payload.amount_vnd)
                .bind(reason)
                .bind(&refund_key)
                .fetch_one(&mut *tx)
                .await?;

                insert_outbox_event(
                    &mut tx,
                    "payment.refunded",
                    &p.order_id,
                    trace_id,
                    json!({
                        "refund_id": refund.id.to_string(),
                        "payment_id": p.id.to_string(),
                        "order_id": p.order_id,
                        "user_id": p.user_id,
                        "amount_vnd": payload.amount_vnd,
                        "reason": reason,
                        "sales_channel": p.sales_channel,
                        "storefront_shop_id": p.storefront_shop_id,
                    }),
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
                    "late/superseded webhook triggered automatic refund record"
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
                  AND p.status IN ('succeeded', 'refunded')
                ORDER BY p.created_at DESC
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

        if payment.status != "succeeded" && payment.status != "refunded" {
            return Err(PaymentError::Conflict(format!(
                "cannot refund payment in status '{}'",
                payment.status
            )));
        }

        let order_id = if input.order_id.trim().is_empty() {
            payment.order_id.clone()
        } else {
            input.order_id.trim().to_string()
        };

        let already_refunded_vnd: i64 = sqlx::query_scalar(
            r#"
            SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
            FROM refunds
            WHERE payment_id = $1 AND status IN ('pending', 'completed', 'manual_review')
            "#,
        )
        .bind(payment.id)
        .fetch_one(&mut *tx)
        .await?;

        // Enforce per-suborder allocation cap when refunding a sub-order in a multi-shop payment
        let suborder_alloc_total: i64 = sqlx::query_scalar(
            r#"
            SELECT COALESCE(SUM(subtotal_vnd + shipping_fee_vnd), 0)::BIGINT
            FROM payment_shop_allocations
            WHERE payment_id = $1 AND order_id = $2
            "#,
        )
        .bind(payment.id)
        .bind(&order_id)
        .fetch_one(&mut *tx)
        .await?;

        let suborder_already_refunded_vnd: i64 = if suborder_alloc_total > 0 {
            sqlx::query_scalar(
                r#"
                SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
                FROM refunds
                WHERE payment_id = $1
                  AND order_id = $2
                  AND status IN ('pending', 'completed', 'manual_review')
                "#,
            )
            .bind(payment.id)
            .bind(&order_id)
            .fetch_one(&mut *tx)
            .await?
        } else {
            0
        };

        let (remaining_after, suborder_remaining_after) = validate_suborder_refund_cap(
            payment.amount_vnd,
            already_refunded_vnd,
            (suborder_alloc_total > 0).then_some(suborder_alloc_total),
            suborder_already_refunded_vnd,
            input.amount_vnd,
        )?;

        let refund_insert = sqlx::query_as::<_, RefundRecord>(
            r#"
            INSERT INTO refunds (
                payment_id, order_id, amount_vnd, reason, status, idempotency_key
            )
            VALUES ($1, $2, $3, $4, 'completed', $5)
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
                // Concurrent duplicate idempotency_key race: roll back and return existing refund cleanly.
                let _ = tx.rollback().await;
                if let Some(existing) = sqlx::query_as::<_, RefundRecord>(
                    "SELECT * FROM refunds WHERE idempotency_key = $1",
                )
                .bind(&input.idempotency_key)
                .fetch_optional(&self.pool)
                .await?
                {
                    return Ok(CreateRefundOutput {
                        refund: existing,
                        already_existed: true,
                    });
                }
                return Err(PaymentError::Database(err));
            }
            Err(err) => return Err(PaymentError::Database(err)),
        };

        let channel = resolve_refund_channel_tx(&mut tx, &payment, &order_id).await?;
        let draft = build_refund_tx(
            refund.id,
            payment.id,
            refund.amount_vnd,
            &refund.reason,
            channel.clone(),
        );
        if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
            self.metrics.record_ledger_tx("refund");
        }

        // Mark only the refunded sub-order's allocation(s) as refunded when fully refunded
        // so partial refunds or sibling shops stay intact.
        if suborder_remaining_after == Some(0) || remaining_after == 0 {
            sqlx::query(
                r#"
                UPDATE payment_shop_allocations
                SET escrow_status = 'refunded'
                WHERE payment_id = $1 AND order_id = $2 AND escrow_status = 'held'
                "#,
            )
            .bind(payment.id)
            .bind(&order_id)
            .execute(&mut *tx)
            .await?;
        }

        let remaining_active_allocs: i64 = sqlx::query_scalar(
            r#"
            SELECT COUNT(*)::BIGINT
            FROM payment_shop_allocations
            WHERE payment_id = $1 AND escrow_status IN ('held', 'pending_release')
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
                "sales_channel": channel.sales_channel.as_str(),
                "storefront_shop_id": channel.storefront_shop_id,
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

        // First settle any `held` allocations for this order to `pending_release`
        let _ = settle_order_to_pending_tx(&mut tx, order_id, shop_filter, &self.metrics).await?;

        let pending_allocs = sqlx::query_as::<_, ShopAllocationRecord>(
            r#"
            SELECT a.*
            FROM payment_shop_allocations a
            JOIN payments p ON p.id = a.payment_id
            WHERE (a.order_id = $1 OR p.order_id = $1 OR p.order_group_id = $1)
              AND ($2::TEXT IS NULL OR a.shop_id = $2)
              AND p.status IN ('succeeded', 'refunded')
              AND a.escrow_status = 'pending_release'
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
            return Ok(EscrowReleaseResult {
                order_id: order_id.to_string(),
                released: already_released,
                released_amount_vnd: 0,
                already_released,
            });
        }

        let mut total_released_vnd = 0_i64;
        for alloc in pending_allocs {
            let split = compute_allocation_split(
                &alloc.order_id,
                &alloc.shop_id,
                alloc.subtotal_vnd,
                alloc.commission_vnd,
                alloc.shipping_fee_vnd,
            )?;

            if split.seller_net_vnd > 0 {
                let channel =
                    ChannelAttribution::from_db(&alloc.sales_channel, &alloc.storefront_shop_id);
                let draft = build_escrow_release_tx(
                    &alloc.order_id,
                    &alloc.shop_id,
                    split.seller_net_vnd,
                    channel,
                )?;
                if ledger::post_transaction(&mut tx, &draft).await?.is_some() {
                    self.metrics.record_ledger_tx("escrow_release");
                }
                total_released_vnd += split.seller_net_vnd;
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

    pub async fn list_reconciliation_queue(&self) -> Result<Vec<ReconciliationQueueItem>> {
        let rows = sqlx::query_as::<_, ReconciliationQueueItem>(
            "SELECT * FROM reconciliation_queue ORDER BY created_at DESC LIMIT 100",
        )
        .fetch_all(&self.pool)
        .await?;
        Ok(rows)
    }

    /// Background scanner for expired pending VietQR payments (UC-PAY06).
    /// Uses `FOR UPDATE SKIP LOCKED` so concurrent scanners never double-expire the same payment.
    pub async fn scan_expired_payments(&self) -> Result<usize> {
        let mut tx = self.pool.begin().await?;

        let expired_candidates = sqlx::query_as::<_, PaymentRecord>(
            r#"
            SELECT * FROM payments
            WHERE status = 'pending'
              AND method = 'VIETQR'
              AND expires_at IS NOT NULL
              AND expires_at <= NOW()
            ORDER BY expires_at ASC
            LIMIT 50
            FOR UPDATE SKIP LOCKED
            "#,
        )
        .fetch_all(&mut *tx)
        .await?;

        if expired_candidates.is_empty() {
            tx.commit().await?;
            return Ok(0);
        }

        let mut expired_count = 0usize;
        let mut sse_updates = Vec::new();

        for payment in expired_candidates {
            match self
                .vietqr
                .check_payment_status(&payment.provider_order_code)
                .await
            {
                Ok(Some(provider_tx)) if provider_tx.amount_vnd == payment.amount_vnd => {
                    if order_already_succeeded_tx(
                        &mut tx,
                        &payment.order_id,
                        &payment.order_group_id,
                        payment.id,
                    )
                    .await?
                    {
                        record_late_duplicate_refund_tx(
                            &mut tx,
                            &payment,
                            &provider_tx.provider_tx_id,
                            provider_tx.amount_vnd,
                            "order_already_has_succeeded_payment",
                            &payment.trace_id,
                            &self.metrics,
                        )
                        .await?;
                    } else {
                        let confirmed = confirm_vietqr_payment_tx(
                            &mut tx,
                            &payment,
                            &provider_tx.provider_tx_id,
                            &payment.trace_id,
                            &self.metrics,
                        )
                        .await?;
                        sse_updates.push(confirmed);
                    }
                    continue;
                }
                Ok(Some(_)) => {
                    continue;
                }
                Err(e) => {
                    warn!(
                        payment_id = %payment.id,
                        error = %e,
                        "provider unreachable during expiration check; deferring expiry"
                    );
                    continue;
                }
                Ok(None) => {}
            }

            let updated = sqlx::query_as::<_, PaymentRecord>(
                r#"
                UPDATE payments
                SET status = 'expired', updated_at = NOW()
                WHERE id = $1
                RETURNING *
                "#,
            )
            .bind(payment.id)
            .fetch_one(&mut *tx)
            .await?;

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

            sse_updates.push(updated);
            expired_count += 1;
        }

        tx.commit().await?;

        for u in &sse_updates {
            self.emit_sse(u);
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
    /// Supports optional `channel_filter` (`MARKETPLACE` or `STOREFRONT`) so channel-scoped runs
    /// partition local VietQR payments without misclassifying the other channel's provider transactions
    /// as `missing_local`.
    pub async fn run_reconciliation(
        &self,
        run_date: NaiveDate,
        channel_filter: Option<SalesChannel>,
    ) -> Result<ReconciliationReport> {
        let provider_name = self.vietqr.mode().as_str().to_string();
        let filter_str = channel_filter.map(SalesChannel::as_str).unwrap_or("");
        // Filter provider transactions to `run_date` to match `local_payments` (`created_at::date = $1`).
        let provider_txs: Vec<ProviderTransaction> = self
            .vietqr
            .list_provider_transactions()
            .await?
            .into_iter()
            .filter(|ptx| ptx.occurred_at.date_naive() == run_date)
            .collect();

        let mut tx = self.pool.begin().await?;

        let run_id: Uuid = sqlx::query_scalar(
            r#"
            INSERT INTO reconciliation_runs (provider, run_date, sales_channel, status)
            VALUES ($1, $2, $3, 'running')
            RETURNING id
            "#,
        )
        .bind(&provider_name)
        .bind(run_date)
        .bind(filter_str)
        .fetch_one(&mut *tx)
        .await?;

        // Load ALL local VietQR payments for `run_date` across every channel so provider txs
        // belonging to another channel are recognized and excluded rather than reported as missing_local.
        let local_payments = sqlx::query_as::<_, PaymentRecord>(
            r#"
            SELECT * FROM payments
            WHERE method = 'VIETQR'
              AND created_at::date = $1
            "#,
        )
        .bind(run_date)
        .fetch_all(&mut *tx)
        .await?;

        let mut by_tx_id: HashMap<String, &PaymentRecord> = HashMap::new();
        let mut by_order_code: HashMap<String, &PaymentRecord> = HashMap::new();
        for p in &local_payments {
            if let Some(ref ptx) = p.provider_tx_id {
                by_tx_id.insert(ptx.clone(), p);
            }
            by_order_code.insert(p.provider_order_code.clone(), p);
        }

        let mut matched_payment_ids: HashSet<Uuid> = HashSet::new();
        let mut matched_count = 0_i32;
        let mut missing_local_count = 0_i32;
        let mut missing_provider_count = 0_i32;
        let mut amount_mismatch_count = 0_i32;
        let mut auto_healed_count = 0_i32;
        let mut healed_sse = Vec::new();
        let mut items = Vec::new();

        for ptx in &provider_txs {
            let local_opt = by_tx_id
                .get(&ptx.provider_tx_id)
                .copied()
                .or_else(|| by_order_code.get(&ptx.provider_order_code).copied());

            match local_opt {
                None => {
                    // Provider-only transactions have no local channel; report only on unfiltered runs.
                    if channel_filter.is_none() {
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
                    }
                }
                Some(local) => {
                    matched_payment_ids.insert(local.id);
                    if !reconciliation_channel_matches(&local.sales_channel, channel_filter) {
                        continue;
                    }
                    let local_ch = ChannelAttribution::from_db(
                        &local.sales_channel,
                        &local.storefront_shop_id,
                    );
                    if local.amount_vnd != ptx.amount_vnd {
                        amount_mismatch_count += 1;
                        items.push(
                            insert_reconciliation_item(
                                &mut tx,
                                run_id,
                                "amount_mismatch",
                                Some(local.id),
                                Some(&ptx.provider_tx_id),
                                Some(local.amount_vnd),
                                Some(ptx.amount_vnd),
                                "amount mismatch between local payment and provider",
                                local_ch.sales_channel.as_str(),
                                &local_ch.storefront_shop_id,
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
                        .bind(local.id)
                        .bind(&local.order_id)
                        .bind(local.amount_vnd)
                        .bind(ptx.amount_vnd)
                        .execute(&mut *tx)
                        .await?;
                    } else if local.status == "pending" {
                        if order_already_succeeded_tx(
                            &mut tx,
                            &local.order_id,
                            &local.order_group_id,
                            local.id,
                        )
                        .await?
                        {
                            record_late_duplicate_refund_tx(
                                &mut tx,
                                local,
                                &ptx.provider_tx_id,
                                ptx.amount_vnd,
                                "reconciliation_late_duplicate_payment",
                                &local.trace_id,
                                &self.metrics,
                            )
                            .await?;
                            matched_count += 1;
                            items.push(
                                insert_reconciliation_item(
                                    &mut tx,
                                    run_id,
                                    "matched",
                                    Some(local.id),
                                    Some(&ptx.provider_tx_id),
                                    Some(local.amount_vnd),
                                    Some(ptx.amount_vnd),
                                    "order already succeeded; queued automatic refund for duplicate intent",
                                    local_ch.sales_channel.as_str(),
                                    &local_ch.storefront_shop_id,
                                )
                                .await?,
                            );
                        } else {
                            let healed = confirm_vietqr_payment_tx(
                                &mut tx,
                                local,
                                &ptx.provider_tx_id,
                                &local.trace_id,
                                &self.metrics,
                            )
                            .await?;
                            healed_sse.push(healed);
                            auto_healed_count += 1;
                            matched_count += 1;
                            items.push(
                                insert_reconciliation_item(
                                    &mut tx,
                                    run_id,
                                    "matched",
                                    Some(local.id),
                                    Some(&ptx.provider_tx_id),
                                    Some(local.amount_vnd),
                                    Some(ptx.amount_vnd),
                                    "auto-healed missing webhook callback",
                                    local_ch.sales_channel.as_str(),
                                    &local_ch.storefront_shop_id,
                                )
                                .await?,
                            );
                        }
                    } else {
                        matched_count += 1;
                        items.push(
                            insert_reconciliation_item(
                                &mut tx,
                                run_id,
                                "matched",
                                Some(local.id),
                                Some(&ptx.provider_tx_id),
                                Some(local.amount_vnd),
                                Some(ptx.amount_vnd),
                                "matched",
                                local_ch.sales_channel.as_str(),
                                &local_ch.storefront_shop_id,
                            )
                            .await?,
                        );
                    }
                }
            }
        }

        for local in &local_payments {
            if local.status == "succeeded"
                && !matched_payment_ids.contains(&local.id)
                && reconciliation_channel_matches(&local.sales_channel, channel_filter)
            {
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
                        "local succeeded payment absent from provider report",
                        local_ch.sales_channel.as_str(),
                        &local_ch.storefront_shop_id,
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

        for h in &healed_sse {
            self.emit_sse(h);
        }

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
    pub async fn process_order_completed_event(
        &self,
        event_id: &str,
        order_id: &str,
    ) -> Result<bool> {
        let mut tx = self.pool.begin().await?;
        let claimed: Option<String> = sqlx::query_scalar(
            r#"
            INSERT INTO processed_nats_events (event_id, event_type)
            VALUES ($1, 'order.completed')
            ON CONFLICT (event_id) DO NOTHING
            RETURNING event_id
            "#,
        )
        .bind(event_id)
        .fetch_optional(&mut *tx)
        .await?;

        if claimed.is_none() {
            tx.commit().await?;
            return Ok(false);
        }

        let _ = settle_order_to_pending_tx(&mut tx, order_id, None, &self.metrics).await?;
        tx.commit().await?;
        Ok(true)
    }

    /// Atomically claims `event_id` in `processed_nats_events` and processes `order.cancelled`
    /// in the SAME PostgreSQL transaction:
    /// - For a multi-shop VietQR payment, refunds ONLY the cancelled sub-order's allocation amount
    ///   (never sibling shops), marks that sub-order's allocation `escrow_status = 'refunded'`,
    ///   and filters active refund statuses when computing remaining refundable amount.
    /// - Only marks the parent `payments` row `'refunded'` when remaining refundable amount is 0
    ///   AND no sibling allocations remain held/pending_release.
    pub async fn process_order_cancelled_event(
        &self,
        event_id: &str,
        order_id: &str,
        trace_id: &str,
    ) -> Result<bool> {
        let mut tx = self.pool.begin().await?;
        let claimed: Option<String> = sqlx::query_scalar(
            r#"
            INSERT INTO processed_nats_events (event_id, event_type)
            VALUES ($1, 'order.cancelled')
            ON CONFLICT (event_id) DO NOTHING
            RETURNING event_id
            "#,
        )
        .bind(event_id)
        .fetch_optional(&mut *tx)
        .await?;

        if claimed.is_none() {
            tx.commit().await?;
            return Ok(false);
        }

        let payment_opt = sqlx::query_as::<_, PaymentRecord>(
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

        let mut sse_cancelled: Option<PaymentRecord> = None;

        match payment.status.as_str() {
            "pending" => {
                // Only cancel the whole pending payment if this order_id is the payment's order/group
                // OR if no sibling held allocations remain.
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

                let remaining_held: i64 = sqlx::query_scalar(
                    r#"
                    SELECT COUNT(*)::BIGINT
                    FROM payment_shop_allocations
                    WHERE payment_id = $1 AND escrow_status IN ('held', 'pending_release')
                    "#,
                )
                .bind(payment.id)
                .fetch_one(&mut *tx)
                .await?;

                if remaining_held == 0
                    || payment.order_id == order_id
                    || payment.order_group_id == order_id
                {
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
                            "reason": "order_cancelled",
                        }),
                    )
                    .await?;
                    sse_cancelled = Some(updated);
                }
            }
            "succeeded" if payment.method == "VIETQR" => {
                let already_refunded: i64 = sqlx::query_scalar(
                    r#"
                    SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
                    FROM refunds
                    WHERE payment_id = $1 AND status IN ('pending', 'completed', 'manual_review')
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

                let suborder_alloc_total: i64 = sqlx::query_scalar(
                    r#"
                    SELECT COALESCE(SUM(subtotal_vnd + shipping_fee_vnd), 0)::BIGINT
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

                let suborder_already_refunded: i64 = sqlx::query_scalar(
                    r#"
                    SELECT COALESCE(SUM(amount_vnd), 0)::BIGINT
                    FROM refunds
                    WHERE payment_id = $1
                      AND order_id = $2
                      AND status IN ('pending', 'completed', 'manual_review')
                    "#,
                )
                .bind(payment.id)
                .bind(order_id)
                .fetch_one(&mut *tx)
                .await?;

                let suborder_remaining_alloc =
                    suborder_alloc_total.saturating_sub(suborder_already_refunded);

                let refund_amount = compute_suborder_refund_amount(
                    payment.amount_vnd,
                    already_refunded,
                    total_alloc_count > 0,
                    suborder_remaining_alloc,
                );

                if refund_amount > 0 {
                    // Deterministic key matching OrderService.CancelOrder ("order-cancel-refund:<order_id>")
                    // so sync CreateRefund and async order.cancelled deduplicate regardless of order.
                    let refund_key = order_cancel_refund_key(order_id);
                    let inserted_refund = sqlx::query_as::<_, RefundRecord>(
                        r#"
                        INSERT INTO refunds (
                            payment_id, order_id, amount_vnd, reason, status, idempotency_key
                        )
                        VALUES ($1, $2, $3, 'order_cancelled_auto_refund', 'completed', $4)
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
                        let channel =
                            resolve_refund_channel_tx(&mut tx, &payment, order_id).await?;
                        let draft = build_refund_tx(
                            refund.id,
                            payment.id,
                            refund.amount_vnd,
                            &refund.reason,
                            channel.clone(),
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
                            WHERE payment_id = $1 AND escrow_status IN ('held', 'pending_release')
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
                                "sales_channel": channel.sales_channel.as_str(),
                                "storefront_shop_id": channel.storefront_shop_id,
                            }),
                        )
                        .await?;
                    }
                }
            }
            _ => {}
        }

        tx.commit().await?;
        if let Some(ref updated) = sse_cancelled {
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

async fn order_already_succeeded_tx(
    tx: &mut Transaction<'_, Postgres>,
    order_id: &str,
    order_group_id: &str,
    exclude_payment_id: Uuid,
) -> Result<bool> {
    let exists: bool = sqlx::query_scalar(
        r#"
        SELECT EXISTS(
            SELECT 1 FROM payments
            WHERE (order_id = $1 OR order_group_id = $2)
              AND status = 'succeeded'
              AND id <> $3
        )
        "#,
    )
    .bind(order_id)
    .bind(order_group_id)
    .bind(exclude_payment_id)
    .fetch_one(&mut **tx)
    .await?;
    Ok(exists)
}

#[allow(clippy::too_many_arguments)]
async fn record_late_duplicate_refund_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment: &PaymentRecord,
    provider_tx_id: &str,
    amount_vnd: i64,
    reason: &str,
    trace_id: &str,
    metrics: &Metrics,
) -> Result<()> {
    sqlx::query(
        r#"
        UPDATE payments
        SET status = 'superseded', updated_at = NOW()
        WHERE id = $1 AND status = 'pending'
        "#,
    )
    .bind(payment.id)
    .execute(&mut **tx)
    .await?;

    let channel = ChannelAttribution::from_db(&payment.sales_channel, &payment.storefront_shop_id);
    let ledger_draft = build_late_payment_refund_tx(provider_tx_id, amount_vnd, channel);
    if ledger::post_transaction(tx, &ledger_draft).await?.is_some() {
        metrics.record_ledger_tx("late_payment_refund");
    }

    let refund_key = format!("late-autoheal:{provider_tx_id}");
    let refund = sqlx::query_as::<_, RefundRecord>(
        r#"
        INSERT INTO refunds (payment_id, order_id, amount_vnd, reason, status, idempotency_key)
        VALUES ($1, $2, $3, $4, 'pending', $5)
        ON CONFLICT (idempotency_key) DO UPDATE SET updated_at = NOW()
        RETURNING *
        "#,
    )
    .bind(payment.id)
    .bind(&payment.order_id)
    .bind(amount_vnd)
    .bind(reason)
    .bind(&refund_key)
    .fetch_one(&mut **tx)
    .await?;

    insert_outbox_event(
        tx,
        "payment.refunded",
        &payment.order_id,
        trace_id,
        json!({
            "refund_id": refund.id.to_string(),
            "payment_id": payment.id.to_string(),
            "order_id": payment.order_id,
            "user_id": payment.user_id,
            "amount_vnd": amount_vnd,
            "reason": reason,
            "sales_channel": payment.sales_channel,
            "storefront_shop_id": payment.storefront_shop_id,
        }),
    )
    .await?;

    Ok(())
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

async fn find_payment_for_webhook_tx(
    tx: &mut Transaction<'_, Postgres>,
    payment_id: Option<Uuid>,
    provider_order_code: Option<&str>,
    order_group_id: Option<&str>,
    order_id: Option<&str>,
) -> Result<Option<PaymentRecord>> {
    if let Some(pid) = payment_id {
        if let Some(p) =
            sqlx::query_as::<_, PaymentRecord>("SELECT * FROM payments WHERE id = $1 FOR UPDATE")
                .bind(pid)
                .fetch_optional(&mut **tx)
                .await?
        {
            return Ok(Some(p));
        }
    }

    if let Some(code) = provider_order_code.filter(|s| !s.trim().is_empty()) {
        if let Some(p) = sqlx::query_as::<_, PaymentRecord>(
            "SELECT * FROM payments WHERE provider_order_code = $1 FOR UPDATE",
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
            FOR UPDATE
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
        WHERE id = $2
        RETURNING *
        "#,
    )
    .bind(provider_tx_id)
    .bind(payment.id)
    .fetch_one(&mut **tx)
    .await?;

    let channel = ChannelAttribution::from_db(&updated.sales_channel, &updated.storefront_shop_id);
    let draft = build_vietqr_confirm_tx(updated.id, updated.amount_vnd, channel);
    if ledger::post_transaction(tx, &draft).await?.is_some() {
        metrics.record_ledger_tx("payment_vietqr");
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
          AND p.status IN ('succeeded', 'refunded')
          AND a.escrow_status = 'held'
        FOR UPDATE OF a
        "#,
    )
    .bind(order_id)
    .bind(shop_filter)
    .fetch_all(&mut **tx)
    .await?;

    let mut settled = 0usize;
    for alloc in held_allocs {
        let split = compute_allocation_split(
            &alloc.order_id,
            &alloc.shop_id,
            alloc.subtotal_vnd,
            alloc.commission_vnd,
            alloc.shipping_fee_vnd,
        )?;

        let channel = ChannelAttribution::from_db(&alloc.sales_channel, &alloc.storefront_shop_id);
        let draft = build_escrow_settle_tx(
            &alloc.order_id,
            &alloc.shop_id,
            split.seller_net_vnd,
            split.commission_vnd,
            split.shipping_fee_vnd,
            channel,
        )?;
        if ledger::post_transaction(tx, &draft).await?.is_some() {
            metrics.record_ledger_tx("escrow_settle");
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
    sales_channel: &str,
    storefront_shop_id: &str,
) -> Result<ReconciliationItemRecord> {
    let rec = sqlx::query_as::<_, ReconciliationItemRecord>(
        r#"
        INSERT INTO reconciliation_items (
            run_id, classification, payment_id, provider_tx_id,
            local_amount_vnd, provider_amount_vnd, details,
            sales_channel, storefront_shop_id
        )
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
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
    .bind(sales_channel)
    .bind(storefront_shop_id)
    .fetch_one(&mut **tx)
    .await?;
    Ok(rec)
}
