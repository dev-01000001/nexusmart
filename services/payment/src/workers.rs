use std::collections::HashSet;
use std::time::Duration;

use async_nats::jetstream::{
    self,
    consumer::PullConsumer,
    context::{GetStreamErrorKind, PublishErrorKind},
    stream, AckKind,
};
use async_nats::HeaderMap as NatsHeaders;
use chrono::{DateTime, Utc};
use futures_util::StreamExt;
use tokio::sync::watch;
use tokio::time::interval;
use tracing::{error, info, warn, Instrument};
use uuid::Uuid;

use crate::domain::{vn_today, EventEnvelope};
use crate::service::PaymentService;
use crate::telemetry::{
    attach_parent_context, extract_context_from_nats, extract_context_from_trace_id,
    inject_current_span_to_nats,
};

const MAX_OUTBOX_RETRIES: i32 = 10;
const MAX_NATS_DELIVERIES: i64 = 5;
const MAX_ENSURE_STREAM_ATTEMPTS: usize = 5;
const DLQ_STREAM_NAME: &str = "NEXUSMART_DLQ";
const ORDER_CONSUMER_NAME: &str = "payment-order-events";

/// Canonical NexusMart primary JetStream stream subjects (`w2b-plan.md` §8 & `docs/contracts.md` §5).
pub const CANONICAL_PRIMARY_SUBJECTS: &[&str] = &[
    "user.>",
    "order.>",
    "payment.>",
    "shipment.>",
    "product.>",
    "promotion.>",
];

/// Order lifecycle subjects consumed by the payment service.
pub const ORDER_EVENT_SUBJECTS: &[&str] = &[
    "order.completed",
    "order.cancelled",
    "order.delivered",
    "order.returned",
];

/// Union-merges `required` subjects onto `existing`, preserving all existing subjects in order
/// and appending only missing required subjects. Returns `(merged, added)`.
pub fn merge_stream_subjects(existing: &[String], required: &[&str]) -> (Vec<String>, bool) {
    let mut seen: HashSet<String> = HashSet::with_capacity(existing.len() + required.len());
    let mut merged: Vec<String> = Vec::with_capacity(existing.len() + required.len());

    for raw in existing {
        let s = raw.trim();
        if s.is_empty() {
            continue;
        }
        if seen.insert(s.to_string()) {
            merged.push(s.to_string());
        }
    }

    let mut added = false;
    for raw in required {
        let s = raw.trim();
        if s.is_empty() {
            continue;
        }
        if seen.insert(s.to_string()) {
            merged.push(s.to_string());
            added = true;
        }
    }

    (merged, added)
}

#[tonic::async_trait]
trait JetStreamAdmin {
    async fn lookup_stream(&self, name: &str) -> anyhow::Result<Option<Box<stream::Config>>>;
    async fn create_stream_cfg(&self, cfg: stream::Config) -> anyhow::Result<()>;
    async fn update_stream_cfg(&self, cfg: stream::Config) -> anyhow::Result<()>;
}

#[tonic::async_trait]
impl JetStreamAdmin for jetstream::Context {
    async fn lookup_stream(&self, name: &str) -> anyhow::Result<Option<Box<stream::Config>>> {
        match self.get_stream(name).await {
            Ok(s) => Ok(Some(Box::new(s.cached_info().config.clone()))),
            Err(err)
                if matches!(
                    err.kind(),
                    GetStreamErrorKind::JetStream(js_err)
                        if js_err.error_code() == jetstream::ErrorCode::STREAM_NOT_FOUND
                ) =>
            {
                Ok(None)
            }
            Err(err) => Err(anyhow::Error::from(err)),
        }
    }

    async fn create_stream_cfg(&self, cfg: stream::Config) -> anyhow::Result<()> {
        let _ = self.create_stream(cfg).await?;
        Ok(())
    }

    async fn update_stream_cfg(&self, cfg: stream::Config) -> anyhow::Result<()> {
        let _ = self.update_stream(&cfg).await?;
        Ok(())
    }
}

/// Idempotently creates or union-updates a JetStream stream so all `required_subjects` are
/// present without ever dropping existing subjects or clobbering existing stream settings.
/// Retries read-merge-update safely when concurrent services race during startup (`w2b-plan.md` §8).
async fn ensure_stream_subjects<A: JetStreamAdmin>(
    admin: &A,
    default_cfg: stream::Config,
    required_subjects: &[&str],
) -> anyhow::Result<()> {
    let stream_name = default_cfg.name.clone();
    let (canonical_subjects, _) = merge_stream_subjects(&[], required_subjects);
    let mut create_cfg = default_cfg;
    create_cfg.subjects = canonical_subjects;

    let mut last_err: Option<anyhow::Error> = None;
    for attempt in 0..MAX_ENSURE_STREAM_ATTEMPTS {
        if attempt > 0 {
            tokio::time::sleep(Duration::from_millis((attempt as u64) * 20)).await;
        }

        match admin.lookup_stream(&stream_name).await {
            Ok(None) => match admin.create_stream_cfg(create_cfg.clone()).await {
                Ok(()) => return Ok(()),
                Err(e) => {
                    // Another service may have created the stream concurrently; re-read & merge on next loop.
                    last_err = Some(e);
                    continue;
                }
            },
            Ok(Some(mut existing_cfg)) => {
                let (merged, added) =
                    merge_stream_subjects(&existing_cfg.subjects, required_subjects);
                if !added {
                    return Ok(());
                }
                info!(
                    stream = %stream_name,
                    existing_subjects = ?existing_cfg.subjects,
                    merged_subjects = ?merged,
                    "updating JetStream stream to union-merge missing subjects"
                );
                existing_cfg.subjects = merged;
                match admin.update_stream_cfg(*existing_cfg).await {
                    Ok(()) => return Ok(()),
                    Err(e) => {
                        // Concurrent UpdateStream conflict -> re-read latest subjects and retry merge.
                        last_err = Some(e);
                        continue;
                    }
                }
            }
            Err(e) => {
                last_err = Some(e);
                continue;
            }
        }
    }

    Err(last_err.unwrap_or_else(|| anyhow::anyhow!("failed to ensure stream {stream_name}")))
}

#[derive(sqlx::FromRow)]
struct OutboxRow {
    id: Uuid,
    event_type: String,
    trace_id: String,
    payload: serde_json::Value,
    retry_count: i32,
    created_at: DateTime<Utc>,
}

async fn ensure_required_streams<A: JetStreamAdmin>(admin: &A, stream_name: &str) -> bool {
    let primary_cfg = stream::Config {
        name: stream_name.to_string(),
        duplicate_window: Duration::from_secs(120),
        ..Default::default()
    };
    if let Err(e) = ensure_stream_subjects(admin, primary_cfg, CANONICAL_PRIMARY_SUBJECTS).await {
        warn!(stream = %stream_name, error = %e, "could not ensure primary JetStream stream; worker will retry");
        return false;
    }

    let dlq_cfg = stream::Config {
        name: DLQ_STREAM_NAME.to_string(),
        ..Default::default()
    };
    if let Err(e) = ensure_stream_subjects(admin, dlq_cfg, &["dlq.>"]).await {
        warn!(stream = DLQ_STREAM_NAME, error = %e, "could not ensure DLQ JetStream stream; worker will retry");
        return false;
    }

    true
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum OutboxPublishErrorAction {
    /// Infrastructure/broker/stream error: stop batch, trigger reconnect + stream re-ensure,
    /// and leave row `pending` without incrementing `retry_count` (never drops event).
    ReconnectWithoutCountingRetry,
    /// Row-specific error: count toward `MAX_OUTBOX_RETRIES`.
    CountRowRetry,
}

fn classify_publish_error(kind: PublishErrorKind) -> OutboxPublishErrorAction {
    match kind {
        PublishErrorKind::StreamNotFound
        | PublishErrorKind::TimedOut
        | PublishErrorKind::BrokenPipe
        | PublishErrorKind::Other => OutboxPublishErrorAction::ReconnectWithoutCountingRetry,
        PublishErrorKind::WrongLastMessageId | PublishErrorKind::WrongLastSequence => {
            OutboxPublishErrorAction::CountRowRetry
        }
    }
}

/// Ensures the primary JetStream stream (`NEXUSMART`) and DLQ stream (`NEXUSMART_DLQ`) exist
/// per `docs/contracts.md` Section 5.
pub async fn connect_jetstream(nats_url: &str, stream_name: &str) -> Option<jetstream::Context> {
    let client = match async_nats::connect(nats_url).await {
        Ok(c) => c,
        Err(e) => {
            warn!(nats_url = %nats_url, error = %e, "NATS unavailable at startup; workers will retry");
            return None;
        }
    };

    let js = jetstream::new(client);

    if !ensure_required_streams(&js, stream_name).await {
        return None;
    }

    info!(nats_url = %nats_url, stream = %stream_name, "connected to NATS JetStream");
    Some(js)
}

/// Background worker 1: Scans expired pending VietQR payments (UC-PAY06).
pub async fn run_expiration_scanner(service: PaymentService, mut shutdown: watch::Receiver<bool>) {
    let mut ticker = interval(service.config().expiration_scan_interval);
    info!(
        interval_secs = service.config().expiration_scan_interval.as_secs(),
        "started payment expiration scanner"
    );

    loop {
        tokio::select! {
            _ = shutdown.changed() => {
                info!("stopping payment expiration scanner");
                break;
            }
            _ = ticker.tick() => {
                if let Err(e) = service.scan_expired_payments().await {
                    error!(error = %e, "payment expiration scan error");
                }
            }
        }
    }
}

/// Background worker 2: Transactional Outbox relay -> NATS JetStream (`NEXUSMART` stream).
pub async fn run_outbox_relay(service: PaymentService, mut shutdown: watch::Receiver<bool>) {
    let mut ticker = interval(service.config().outbox_poll_interval);
    let mut js_opt: Option<jetstream::Context> = None;

    info!(
        poll_ms = service.config().outbox_poll_interval.as_millis() as u64,
        "started NATS JetStream outbox relay worker"
    );

    loop {
        tokio::select! {
            _ = shutdown.changed() => {
                info!("stopping outbox relay worker");
                break;
            }
            _ = ticker.tick() => {
                if js_opt.is_none() {
                    js_opt = connect_jetstream(
                        &service.config().nats_url,
                        &service.config().nats_stream,
                    )
                    .await;
                }

                let Some(ref js) = js_opt else {
                    continue;
                };

                if let Err(e) = relay_outbox_batch(&service, js).await {
                    warn!(error = %e, "outbox relay batch encountered error");
                    js_opt = None;
                }
            }
        }
    }
}

async fn relay_outbox_batch(
    service: &PaymentService,
    js: &jetstream::Context,
) -> anyhow::Result<()> {
    let mut tx = service.pool().begin().await?;

    let rows = sqlx::query_as::<_, OutboxRow>(
        r#"
        SELECT id, event_type, trace_id, payload, retry_count, created_at
        FROM outbox_events
        WHERE status = 'pending'
        ORDER BY created_at ASC
        LIMIT 50
        FOR UPDATE SKIP LOCKED
        "#,
    )
    .fetch_all(&mut *tx)
    .await?;

    if rows.is_empty() {
        tx.commit().await?;
        return Ok(());
    }

    for row in rows {
        // ponytail: outbox_events only persists 32-hex trace_id (not full W3C traceparent), so
        // extract_context_from_trace_id synthesizes a random parent_span_id that was never emitted;
        // trace UIs keep the same trace_id but may flag a missing parent span. Upgrade path: add a
        // nullable traceparent column to outbox_events in a future migration.
        let parent_cx = extract_context_from_trace_id(&row.trace_id);
        let pub_span = tracing::info_span!(
            "nats.publish",
            otel.name = %format!("nats.publish {}", row.event_type),
            otel.kind = "producer",
            messaging.destination = %row.event_type,
            event_id = %row.id,
            trace_id = tracing::field::Empty,
        );
        let fallback_tid = if row.trace_id.is_empty() {
            None
        } else {
            Some(row.trace_id.as_str())
        };
        let active_trace_id = attach_parent_context(&pub_span, parent_cx, fallback_tid);
        pub_span.record("trace_id", active_trace_id.as_str());

        if row.event_type.trim().is_empty() {
            mark_outbox_retry(&mut tx, row.id, row.retry_count, "empty event_type").await?;
            continue;
        }

        let envelope = EventEnvelope {
            event_id: row.id.to_string(),
            event_type: row.event_type.clone(),
            trace_id: active_trace_id.clone(),
            occurred_at: row.created_at,
            payload: row.payload.clone(),
        };

        let bytes = match serde_json::to_vec(&envelope) {
            Ok(b) => b,
            Err(e) => {
                mark_outbox_retry(&mut tx, row.id, row.retry_count, &e.to_string()).await?;
                continue;
            }
        };
        let mut headers = NatsHeaders::new();
        headers.insert("Nats-Msg-Id", row.id.to_string().as_str());
        pub_span.in_scope(|| {
            inject_current_span_to_nats(&mut headers, &active_trace_id);
        });

        let publish_outcome = async {
            match js
                .publish_with_headers(row.event_type.clone(), headers, bytes.into())
                .await
            {
                Ok(ack_future) => match ack_future.await {
                    Ok(_) => Ok(()),
                    Err(e) => Err(e),
                },
                Err(e) => Err(e),
            }
        }
        .instrument(pub_span.clone())
        .await;

        match publish_outcome {
            Ok(()) => {
                sqlx::query(
                    r#"
                    UPDATE outbox_events
                    SET status = 'sent', sent_at = NOW(), last_error = NULL
                    WHERE id = $1
                    "#,
                )
                .bind(row.id)
                .execute(&mut *tx)
                .await?;
                pub_span.in_scope(|| {
                    info!(
                        service = "payment",
                        event_id = %row.id,
                        event_type = %row.event_type,
                        trace_id = %active_trace_id,
                        "outbox event published to NATS JetStream"
                    );
                });
            }
            Err(e) => match classify_publish_error(e.kind()) {
                OutboxPublishErrorAction::ReconnectWithoutCountingRetry => {
                    let err_msg = e.to_string();
                    sqlx::query("UPDATE outbox_events SET last_error = $1 WHERE id = $2")
                        .bind(&err_msg)
                        .bind(row.id)
                        .execute(&mut *tx)
                        .await?;
                    // Commit earlier rows marked `sent` in this batch while leaving this row
                    // `pending` with unchanged `retry_count`; return Err so caller drops js_opt.
                    tx.commit().await?;
                    return Err(anyhow::anyhow!(
                        "transient NATS publish error for event {}: {}",
                        row.id,
                        err_msg
                    ));
                }
                OutboxPublishErrorAction::CountRowRetry => {
                    mark_outbox_retry(&mut tx, row.id, row.retry_count, &e.to_string()).await?;
                }
            },
        }
    }

    tx.commit().await?;
    Ok(())
}

async fn mark_outbox_retry(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    id: Uuid,
    current_retries: i32,
    err_msg: &str,
) -> Result<(), sqlx::Error> {
    let next_retries = current_retries + 1;
    let next_status = if next_retries >= MAX_OUTBOX_RETRIES {
        "failed"
    } else {
        "pending"
    };
    sqlx::query(
        r#"
        UPDATE outbox_events
        SET retry_count = $1, status = $2, last_error = $3
        WHERE id = $4
        "#,
    )
    .bind(next_retries)
    .bind(next_status)
    .bind(err_msg)
    .bind(id)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

/// Background worker 3: Daily reconciliation scheduler (UC-PAY07).
pub async fn run_reconciliation_scheduler(
    service: PaymentService,
    mut shutdown: watch::Receiver<bool>,
) {
    let mut ticker = interval(service.config().reconciliation_interval);
    ticker.tick().await;

    info!(
        interval_secs = service.config().reconciliation_interval.as_secs(),
        "started scheduled payment reconciliation worker"
    );

    loop {
        tokio::select! {
            _ = shutdown.changed() => {
                info!("stopping reconciliation scheduler");
                break;
            }
            _ = ticker.tick() => {
                let today = vn_today(Utc::now());
                if let Err(e) = service.run_reconciliation(today).await {
                    error!(error = %e, "scheduled reconciliation failed");
                }
            }
        }
    }
}

/// Background worker 5: Releases matured seller escrow holds (`pending_release -> released`)
/// every 60 seconds once `settled_at + PAYMENT_SELLER_HOLD_HOURS` has elapsed (PAY-B7 / ORD-B9).
pub async fn run_escrow_hold_releaser(
    service: PaymentService,
    mut shutdown: watch::Receiver<bool>,
) {
    let mut ticker = interval(service.config().escrow_hold_scan_interval);
    info!(
        interval_secs = service.config().escrow_hold_scan_interval.as_secs(),
        hold_hours = service.config().seller_hold_hours,
        "started seller escrow hold release worker"
    );

    loop {
        tokio::select! {
            _ = shutdown.changed() => {
                info!("stopping seller escrow hold release worker");
                break;
            }
            _ = ticker.tick() => {
                if let Err(e) = service.release_matured_escrow_holds().await {
                    error!(error = %e, "seller escrow hold release scan error");
                }
            }
        }
    }
}

/// Pure retry vs DLQ decision matching `docs/contracts.md:269` (`max_deliver = 5`).
pub fn should_route_to_dlq(delivered_count: i64, max_deliver: i64) -> bool {
    delivered_count >= max_deliver
}

/// Background worker 4: Transactional idempotent NATS JetStream consumer for
/// `order.completed` and `order.cancelled`.
/// - Persists `processed_nats_events` in the EXACT SAME PostgreSQL transaction as state updates.
/// - Retries transient failures via NAK until `delivered >= max_deliver` (5), then routes to
///   `dlq.<original_subject>` and ACKs per `docs/contracts.md:269`.
pub async fn run_nats_order_consumer(service: PaymentService, mut shutdown: watch::Receiver<bool>) {
    info!("started NATS order event consumer");

    loop {
        if *shutdown.borrow() {
            break;
        }

        let Some(js) =
            connect_jetstream(&service.config().nats_url, &service.config().nats_stream).await
        else {
            tokio::select! {
                _ = shutdown.changed() => break,
                _ = tokio::time::sleep(Duration::from_secs(5)) => continue,
            }
        };

        let Ok(stream) = js.get_stream(&service.config().nats_stream).await else {
            tokio::select! {
                _ = shutdown.changed() => break,
                _ = tokio::time::sleep(Duration::from_secs(5)) => continue,
            }
        };

        let desired_subjects: Vec<String> = ORDER_EVENT_SUBJECTS
            .iter()
            .map(|s| (*s).to_string())
            .collect();
        let desired_cfg = jetstream::consumer::pull::Config {
            durable_name: Some(ORDER_CONSUMER_NAME.to_string()),
            filter_subjects: desired_subjects.clone(),
            max_deliver: MAX_NATS_DELIVERIES,
            ..Default::default()
        };

        let consumer: PullConsumer = match ensure_order_consumer(
            &stream,
            desired_cfg,
            &desired_subjects,
        )
        .await
        {
            Ok(c) => c,
            Err(e) => {
                warn!(error = %e, "failed to create/update payment JetStream consumer; retrying");
                tokio::select! {
                    _ = shutdown.changed() => break,
                    _ = tokio::time::sleep(Duration::from_secs(5)) => continue,
                }
            }
        };

        let Ok(mut messages) = consumer.messages().await else {
            tokio::time::sleep(Duration::from_secs(5)).await;
            continue;
        };

        loop {
            tokio::select! {
                _ = shutdown.changed() => {
                    info!("stopping NATS order event consumer");
                    return;
                }
                maybe_msg = messages.next() => {
                    let Some(Ok(msg)) = maybe_msg else {
                        break;
                    };
                    handle_order_event(&service, &js, msg).await;
                }
            }
        }
    }
}

async fn ensure_order_consumer(
    stream: &jetstream::stream::Stream,
    cfg: jetstream::consumer::pull::Config,
    desired_subjects: &[String],
) -> anyhow::Result<PullConsumer> {
    match stream.consumer_info(ORDER_CONSUMER_NAME).await {
        Ok(info) => {
            let has_all = desired_subjects
                .iter()
                .all(|s| info.config.filter_subjects.contains(s));
            if has_all {
                return Ok(stream
                    .get_or_create_consumer(ORDER_CONSUMER_NAME, cfg)
                    .await?);
            }
            info!(
                consumer = ORDER_CONSUMER_NAME,
                existing_subjects = ?info.config.filter_subjects,
                desired_subjects = ?desired_subjects,
                "updating JetStream consumer filter_subjects"
            );
            match stream.update_consumer(cfg.clone()).await {
                Ok(c) => Ok(c),
                Err(e) => {
                    warn!(
                        consumer = ORDER_CONSUMER_NAME,
                        error = %e,
                        "update_consumer failed; recreating consumer with updated filter_subjects"
                    );
                    let _ = stream.delete_consumer(ORDER_CONSUMER_NAME).await;
                    Ok(stream.create_consumer(cfg).await?)
                }
            }
        }
        Err(_) => Ok(stream.create_consumer(cfg).await?),
    }
}

async fn handle_order_event(
    service: &PaymentService,
    js: &jetstream::Context,
    msg: jetstream::Message,
) {
    let subject = msg.subject.to_string();
    let delivered = msg.info().map(|i| i.delivered).unwrap_or(1);

    let envelope: EventEnvelope = match serde_json::from_slice(&msg.payload) {
        Ok(e) => e,
        Err(e) => {
            let parent_cx = extract_context_from_nats(msg.headers.as_ref(), None);
            let span = tracing::info_span!(
                "nats.consume",
                otel.name = %format!("nats.consume {subject}"),
                otel.kind = "consumer",
                messaging.destination = %subject,
                trace_id = tracing::field::Empty,
            );
            let trace_id = attach_parent_context(&span, parent_cx, None);
            span.record("trace_id", trace_id.as_str());
            // Permanent deserialization poison message -> straight to DLQ + ACK
            span.in_scope(|| {
                warn!(
                    service = "payment",
                    subject = %subject,
                    trace_id = %trace_id,
                    error = %e,
                    "malformed NATS event payload; routing to DLQ"
                );
            });
            let dlq_subject = format!("dlq.{subject}");
            let _ = js.publish(dlq_subject, msg.payload.clone()).await;
            let _ = msg.ack().await;
            return;
        }
    };

    let fallback_tid = if envelope.trace_id.is_empty() {
        None
    } else {
        Some(envelope.trace_id.as_str())
    };
    let parent_cx = extract_context_from_nats(msg.headers.as_ref(), fallback_tid);
    let span = tracing::info_span!(
        "nats.consume",
        otel.name = %format!("nats.consume {subject}"),
        otel.kind = "consumer",
        messaging.destination = %subject,
        event_id = %envelope.event_id,
        trace_id = tracing::field::Empty,
    );
    let trace_id = attach_parent_context(&span, parent_cx, fallback_tid);
    span.record("trace_id", trace_id.as_str());

    async {
        let order_id = envelope
            .payload
            .get("order_id")
            .and_then(|v| v.as_str())
            .unwrap_or("")
            .to_string();

        if order_id.is_empty() {
            let _ = msg.ack().await;
            return;
        }

        // Both `processed_nats_events` claim AND business state mutation execute in ONE DB transaction.
        let res = match envelope.event_type.as_str() {
            "order.completed" => service
                .process_order_completed_event(&envelope.event_id, &order_id, &trace_id)
                .await
                .map(|_| ()),
            "order.cancelled" => service
                .process_order_cancelled_event(&envelope.event_id, &order_id, &trace_id)
                .await
                .map(|_| ()),
            "order.delivered" => service
                .process_order_delivered_event(&envelope.event_id, &order_id, &trace_id)
                .await
                .map(|_| ()),
            "order.returned" => {
                let from_status = envelope.payload.get("from_status").and_then(|v| v.as_str());
                service
                    .process_order_returned_event(
                        &envelope.event_id,
                        &order_id,
                        from_status,
                        &trace_id,
                    )
                    .await
                    .map(|_| ())
            }
            _ => Ok(()),
        };

        match res {
            Ok(()) => {
                info!(
                    service = "payment",
                    subject = %subject,
                    event_id = %envelope.event_id,
                    event_type = %envelope.event_type,
                    order_id = %order_id,
                    trace_id = %trace_id,
                    "processed NATS order event"
                );
                let _ = msg.ack().await;
            }
            Err(e) => {
                if should_route_to_dlq(delivered, MAX_NATS_DELIVERIES) {
                    warn!(
                        service = "payment",
                        event_id = %envelope.event_id,
                        event_type = %envelope.event_type,
                        order_id = %order_id,
                        trace_id = %trace_id,
                        delivered = delivered,
                        error = %e,
                        "max NATS delivery attempts reached; routing to DLQ"
                    );
                    let dlq_subject = format!("dlq.{subject}");
                    let _ = js.publish(dlq_subject, msg.payload.clone()).await;
                    let _ = msg.ack().await;
                } else {
                    warn!(
                        service = "payment",
                        event_id = %envelope.event_id,
                        event_type = %envelope.event_type,
                        order_id = %order_id,
                        trace_id = %trace_id,
                        delivered = delivered,
                        max_deliver = MAX_NATS_DELIVERIES,
                        error = %e,
                        "transient error processing order event; NAKing for retry"
                    );
                    let backoff = Duration::from_secs(delivered.clamp(1, 5) as u64);
                    let _ = msg.ack_with(AckKind::Nak(Some(backoff))).await;
                }
            }
        }
    }
    .instrument(span)
    .await;
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_nats_retry_until_max_deliver_then_dlq() {
        assert!(!should_route_to_dlq(1, MAX_NATS_DELIVERIES));
        assert!(!should_route_to_dlq(2, MAX_NATS_DELIVERIES));
        assert!(!should_route_to_dlq(4, MAX_NATS_DELIVERIES));
        assert!(should_route_to_dlq(5, MAX_NATS_DELIVERIES));
        assert!(should_route_to_dlq(6, MAX_NATS_DELIVERIES));
    }

    #[test]
    fn test_order_consumer_subscribes_delivered_and_returned() {
        assert!(ORDER_EVENT_SUBJECTS.contains(&"order.completed"));
        assert!(ORDER_EVENT_SUBJECTS.contains(&"order.cancelled"));
        assert!(ORDER_EVENT_SUBJECTS.contains(&"order.delivered"));
        assert!(ORDER_EVENT_SUBJECTS.contains(&"order.returned"));
    }

    #[test]
    fn test_w2b_canonical_primary_subjects_and_union_merge() {
        assert_eq!(
            CANONICAL_PRIMARY_SUBJECTS,
            &[
                "user.>",
                "order.>",
                "payment.>",
                "shipment.>",
                "product.>",
                "promotion.>"
            ]
        );

        let existing = vec![
            "user.>".to_string(),
            "order.>".to_string(),
            "payment.>".to_string(),
            "shipment.>".to_string(),
            "audit.>".to_string(),
        ];
        let (merged, added) = merge_stream_subjects(&existing, CANONICAL_PRIMARY_SUBJECTS);
        assert!(added);
        assert_eq!(
            merged,
            vec![
                "user.>",
                "order.>",
                "payment.>",
                "shipment.>",
                "audit.>",
                "product.>",
                "promotion.>"
            ]
        );

        // Second merge is a no-op
        let (merged2, added2) = merge_stream_subjects(&merged, CANONICAL_PRIMARY_SUBJECTS);
        assert!(!added2);
        assert_eq!(merged2, merged);
    }

    #[tokio::test]
    async fn test_w2b_ensure_stream_subjects_merges_and_retries_concurrent_conflicts() {
        use std::sync::{Arc, Mutex};

        #[derive(Default)]
        struct FakeState {
            cfg: Option<stream::Config>,
            create_calls: usize,
            update_calls: usize,
            fail_create_race: bool,
            fail_first_update_conflict: bool,
        }

        struct FakeAdmin(Arc<Mutex<FakeState>>);

        #[tonic::async_trait]
        impl JetStreamAdmin for FakeAdmin {
            async fn lookup_stream(
                &self,
                _name: &str,
            ) -> anyhow::Result<Option<Box<stream::Config>>> {
                let st = self.0.lock().unwrap();
                Ok(st.cfg.clone().map(Box::new))
            }

            async fn create_stream_cfg(&self, cfg: stream::Config) -> anyhow::Result<()> {
                let mut st = self.0.lock().unwrap();
                st.create_calls += 1;
                if st.fail_create_race {
                    st.fail_create_race = false;
                    // Sibling service created stream with old 4 subjects + custom max_bytes
                    st.cfg = Some(stream::Config {
                        name: cfg.name,
                        subjects: vec![
                            "user.>".into(),
                            "order.>".into(),
                            "payment.>".into(),
                            "shipment.>".into(),
                        ],
                        max_bytes: 1_048_576,
                        ..Default::default()
                    });
                    anyhow::bail!("stream name already in use");
                }
                st.cfg = Some(cfg);
                Ok(())
            }

            async fn update_stream_cfg(&self, cfg: stream::Config) -> anyhow::Result<()> {
                let mut st = self.0.lock().unwrap();
                st.update_calls += 1;
                if st.fail_first_update_conflict {
                    st.fail_first_update_conflict = false;
                    // Sibling concurrently added product.> and custom.>
                    if let Some(ref mut cur) = st.cfg {
                        cur.subjects = vec![
                            "user.>".into(),
                            "order.>".into(),
                            "payment.>".into(),
                            "shipment.>".into(),
                            "product.>".into(),
                            "custom.>".into(),
                        ];
                    }
                    anyhow::bail!("concurrent stream update conflict");
                }
                st.cfg = Some(cfg);
                Ok(())
            }
        }

        // Case 1: Fresh creation includes all 6 canonical subjects
        let fresh_state = Arc::new(Mutex::new(FakeState::default()));
        let fresh = FakeAdmin(fresh_state.clone());
        ensure_stream_subjects(
            &fresh,
            stream::Config {
                name: "NEXUSMART".into(),
                duplicate_window: Duration::from_secs(120),
                ..Default::default()
            },
            CANONICAL_PRIMARY_SUBJECTS,
        )
        .await
        .unwrap();
        {
            let st = fresh_state.lock().unwrap();
            assert_eq!(st.create_calls, 1);
            assert_eq!(st.update_calls, 0);
            let got = st.cfg.as_ref().unwrap();
            assert_eq!(got.subjects.len(), 6);
            assert!(got.subjects.contains(&"product.>".to_string()));
            assert!(got.subjects.contains(&"promotion.>".to_string()));
        }

        // Calling again when all subjects exist is a strict no-op (0 extra updates)
        ensure_stream_subjects(
            &fresh,
            stream::Config {
                name: "NEXUSMART".into(),
                ..Default::default()
            },
            CANONICAL_PRIMARY_SUBJECTS,
        )
        .await
        .unwrap();
        assert_eq!(fresh_state.lock().unwrap().update_calls, 0);

        // Case 2: Concurrent create race + concurrent update conflict -> re-reads, merges, preserves custom.> and max_bytes
        let race_state = Arc::new(Mutex::new(FakeState {
            fail_create_race: true,
            fail_first_update_conflict: true,
            ..Default::default()
        }));
        let race = FakeAdmin(race_state.clone());
        ensure_stream_subjects(
            &race,
            stream::Config {
                name: "NEXUSMART".into(),
                duplicate_window: Duration::from_secs(120),
                ..Default::default()
            },
            CANONICAL_PRIMARY_SUBJECTS,
        )
        .await
        .unwrap();
        let st = race_state.lock().unwrap();
        assert_eq!(st.create_calls, 1);
        assert_eq!(st.update_calls, 2);
        let got = st.cfg.as_ref().unwrap();
        assert_eq!(got.max_bytes, 1_048_576, "must not clobber stream config");
        assert_eq!(
            got.subjects,
            vec![
                "user.>",
                "order.>",
                "payment.>",
                "shipment.>",
                "product.>",
                "custom.>",
                "promotion.>"
            ]
        );
    }

    #[tokio::test]
    async fn test_w2b_r3_ensure_required_streams_returns_false_on_primary_or_dlq_failure() {
        struct FailingAdmin {
            fail_stream: &'static str,
        }

        #[tonic::async_trait]
        impl JetStreamAdmin for FailingAdmin {
            async fn lookup_stream(
                &self,
                name: &str,
            ) -> anyhow::Result<Option<Box<stream::Config>>> {
                if name == self.fail_stream {
                    anyhow::bail!("simulated stream failure for {name}");
                }
                Ok(None)
            }

            async fn create_stream_cfg(&self, _cfg: stream::Config) -> anyhow::Result<()> {
                Ok(())
            }

            async fn update_stream_cfg(&self, _cfg: stream::Config) -> anyhow::Result<()> {
                Ok(())
            }
        }

        assert!(
            !ensure_required_streams(
                &FailingAdmin {
                    fail_stream: "NEXUSMART"
                },
                "NEXUSMART"
            )
            .await,
            "primary stream failure must cause connect_jetstream to return None and retry"
        );
        assert!(
            !ensure_required_streams(
                &FailingAdmin {
                    fail_stream: DLQ_STREAM_NAME,
                },
                "NEXUSMART"
            )
            .await,
            "DLQ stream failure must cause connect_jetstream to return None and retry"
        );
        assert!(
            ensure_required_streams(&FailingAdmin { fail_stream: "" }, "NEXUSMART").await,
            "succeeding both primary and DLQ streams must return true"
        );
    }

    #[test]
    fn test_w2b_r3_publish_error_classification_protects_outbox_on_infra_failure() {
        for infra_kind in [
            PublishErrorKind::StreamNotFound,
            PublishErrorKind::TimedOut,
            PublishErrorKind::BrokenPipe,
            PublishErrorKind::Other,
        ] {
            assert_eq!(
                classify_publish_error(infra_kind),
                OutboxPublishErrorAction::ReconnectWithoutCountingRetry
            );
        }
        for row_kind in [
            PublishErrorKind::WrongLastMessageId,
            PublishErrorKind::WrongLastSequence,
        ] {
            assert_eq!(
                classify_publish_error(row_kind),
                OutboxPublishErrorAction::CountRowRetry
            );
        }
    }
}
