use std::time::Duration;

use async_nats::jetstream::{self, consumer::PullConsumer, stream, AckKind};
use async_nats::HeaderMap as NatsHeaders;
use chrono::{DateTime, Utc};
use futures_util::StreamExt;
use tokio::sync::watch;
use tokio::time::interval;
use tracing::{error, info, info_span, warn, Instrument};
use tracing_opentelemetry::OpenTelemetrySpanExt;
use uuid::Uuid;

use crate::domain::EventEnvelope;
use crate::service::PaymentService;
use crate::telemetry::{
    extract_nats_context, extract_or_generate_trace_id, inject_nats_context, redact_url_credentials,
};

const MAX_OUTBOX_RETRIES: i32 = 10;
const MAX_NATS_DELIVERIES: i64 = 5;
const DLQ_STREAM_NAME: &str = "NEXUSMART_DLQ";

#[derive(sqlx::FromRow)]
struct OutboxRow {
    id: Uuid,
    event_type: String,
    trace_id: String,
    payload: serde_json::Value,
    retry_count: i32,
    created_at: DateTime<Utc>,
}

/// Ensures the primary JetStream stream (`NEXUSMART`) and DLQ stream (`NEXUSMART_DLQ`) exist
/// per `docs/contracts.md` Section 5.
pub async fn connect_jetstream(nats_url: &str, stream_name: &str) -> Option<jetstream::Context> {
    let safe_url = redact_url_credentials(nats_url);
    let client = match async_nats::connect(nats_url).await {
        Ok(c) => c,
        Err(e) => {
            warn!(service = "payment", nats_url = %safe_url, error = %e, "NATS unavailable at startup; workers will retry");
            return None;
        }
    };

    let js = jetstream::new(client);

    if js.get_stream(stream_name).await.is_err() {
        let cfg = stream::Config {
            name: stream_name.to_string(),
            subjects: vec![
                "user.>".to_string(),
                "order.>".to_string(),
                "payment.>".to_string(),
                "shipment.>".to_string(),
            ],
            duplicate_window: Duration::from_secs(120),
            ..Default::default()
        };
        if let Err(e) = js.get_or_create_stream(cfg).await {
            warn!(stream = %stream_name, error = %e, "could not create JetStream stream yet");
        }
    }

    if js.get_stream(DLQ_STREAM_NAME).await.is_err() {
        let dlq_cfg = stream::Config {
            name: DLQ_STREAM_NAME.to_string(),
            subjects: vec!["dlq.>".to_string()],
            ..Default::default()
        };
        if let Err(e) = js.get_or_create_stream(dlq_cfg).await {
            warn!(stream = DLQ_STREAM_NAME, error = %e, "could not create DLQ JetStream stream yet");
        }
    }

    info!(service = "payment", nats_url = %safe_url, stream = %stream_name, "connected to NATS JetStream");
    Some(js)
}

/// Background worker 1: Scans expired pending online payments (VietQR & ZaloPay, UC-PAY06).
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
        let pub_span = info_span!(
            "nats.publish",
            service = "payment",
            messaging.destination = %row.event_type,
            messaging.message_id = %row.id,
            trace_id = %row.trace_id,
            otel.kind = "producer",
        );

        let envelope = EventEnvelope {
            event_id: row.id.to_string(),
            event_type: row.event_type.clone(),
            trace_id: row.trace_id.clone(),
            occurred_at: row.created_at,
            payload: row.payload.clone(),
        };

        let bytes = serde_json::to_vec(&envelope)?;
        let mut headers = NatsHeaders::new();
        headers.insert("Nats-Msg-Id", row.id.to_string().as_str());
        {
            let _entered = pub_span.enter();
            inject_nats_context(&mut headers, &row.trace_id);
        }
        if !row.trace_id.is_empty() {
            headers.insert("x-trace-id", row.trace_id.as_str());
        }

        let publish_res = js
            .publish_with_headers(row.event_type.clone(), headers, bytes.into())
            .instrument(pub_span.clone())
            .await;

        match publish_res {
            Ok(ack_future) => match async move { ack_future.await }
                .instrument(pub_span.clone())
                .await
            {
                Ok(_) => {
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
                    let _entered = pub_span.enter();
                    info!(
                        service = "payment",
                        event_id = %row.id,
                        event_type = %row.event_type,
                        trace_id = %row.trace_id,
                        "outbox event published to NATS JetStream"
                    );
                }
                Err(e) => {
                    mark_outbox_retry(&mut tx, row.id, row.retry_count, &e.to_string()).await?;
                }
            },
            Err(e) => {
                mark_outbox_retry(&mut tx, row.id, row.retry_count, &e.to_string()).await?;
            }
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
                let today = Utc::now().date_naive();
                if let Err(e) = service.run_reconciliation(today, None).await {
                    error!(error = %e, "scheduled reconciliation failed");
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

        let consumer: PullConsumer = match stream
            .get_or_create_consumer(
                "payment-order-events",
                jetstream::consumer::pull::Config {
                    durable_name: Some("payment-order-events".to_string()),
                    filter_subjects: vec![
                        "order.completed".to_string(),
                        "order.cancelled".to_string(),
                    ],
                    max_deliver: MAX_NATS_DELIVERIES,
                    ..Default::default()
                },
            )
            .await
        {
            Ok(c) => c,
            Err(e) => {
                warn!(error = %e, "failed to create payment JetStream consumer; retrying");
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

async fn handle_order_event(
    service: &PaymentService,
    js: &jetstream::Context,
    msg: jetstream::Message,
) {
    let subject = msg.subject.to_string();
    let delivered = msg.info().map(|i| i.delivered).unwrap_or(1);
    let parent_cx = extract_nats_context(msg.headers.as_ref());
    let hdr_traceparent = msg
        .headers
        .as_ref()
        .and_then(|h| h.get("traceparent").map(|v| v.as_str()));
    let hdr_x_trace = msg
        .headers
        .as_ref()
        .and_then(|h| h.get("x-trace-id").map(|v| v.as_str()));
    let trace_id = extract_or_generate_trace_id(hdr_traceparent, hdr_x_trace);

    let span = info_span!(
        "nats.consume",
        service = "payment",
        messaging.destination = %subject,
        trace_id = %trace_id,
        otel.kind = "consumer",
    );
    span.set_parent(parent_cx);

    async move {
        let envelope: EventEnvelope = match serde_json::from_slice(&msg.payload) {
            Ok(e) => e,
            Err(e) => {
                // Permanent deserialization poison message -> straight to DLQ + ACK
                warn!(
                    service = "payment",
                    trace_id = %trace_id,
                    subject = %subject,
                    error = %e,
                    "malformed NATS event payload; routing to DLQ"
                );
                let dlq_subject = format!("dlq.{subject}");
                let mut dlq_headers = NatsHeaders::new();
                inject_nats_context(&mut dlq_headers, &trace_id);
                dlq_headers.insert("x-trace-id", trace_id.as_str());
                let _ = js
                    .publish_with_headers(dlq_subject, dlq_headers, msg.payload.clone())
                    .await;
                let _ = msg.ack().await;
                return;
            }
        };

        let effective_trace_id = if !envelope.trace_id.trim().is_empty() {
            envelope.trace_id.clone()
        } else {
            trace_id.clone()
        };

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
                .process_order_completed_event(&envelope.event_id, &order_id)
                .await
                .map(|_| ()),
            "order.cancelled" => service
                .process_order_cancelled_event(&envelope.event_id, &order_id, &effective_trace_id)
                .await
                .map(|_| ()),
            _ => Ok(()),
        };

        match res {
            Ok(()) => {
                let _ = msg.ack().await;
            }
            Err(e) => {
                if should_route_to_dlq(delivered, MAX_NATS_DELIVERIES) {
                    warn!(
                        service = "payment",
                        trace_id = %effective_trace_id,
                        event_id = %envelope.event_id,
                        event_type = %envelope.event_type,
                        order_id = %order_id,
                        delivered = delivered,
                        error = %e,
                        "max NATS delivery attempts reached; routing to DLQ"
                    );
                    let dlq_subject = format!("dlq.{subject}");
                    let mut dlq_headers = NatsHeaders::new();
                    inject_nats_context(&mut dlq_headers, &effective_trace_id);
                    dlq_headers.insert("x-trace-id", effective_trace_id.as_str());
                    let _ = js
                        .publish_with_headers(dlq_subject, dlq_headers, msg.payload.clone())
                        .await;
                    let _ = msg.ack().await;
                } else {
                    warn!(
                        service = "payment",
                        trace_id = %effective_trace_id,
                        event_id = %envelope.event_id,
                        event_type = %envelope.event_type,
                        order_id = %order_id,
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

    #[tokio::test]
    async fn test_workers_shutdown_cleanly_and_nats_fallback() {
        use crate::config::Config;
        use crate::metrics::Metrics;
        use sqlx::postgres::PgPoolOptions;

        // Unreachable NATS returns None gracefully instead of panicking
        let res = connect_jetstream("nats://127.0.0.1:1", "NEXUSMART").await;
        assert!(res.is_none());

        let mut cfg = Config::from_env();
        cfg.expiration_scan_interval = Duration::from_secs(60);
        cfg.reconciliation_interval = Duration::from_secs(60);
        cfg.outbox_poll_interval = Duration::from_secs(60);
        let pool = PgPoolOptions::new()
            .connect_lazy(&cfg.database_url)
            .expect("lazy pool");
        let svc = PaymentService::new(pool, cfg, Metrics::new());

        let (tx, rx) = watch::channel(false);
        let h_recon = tokio::spawn(run_reconciliation_scheduler(svc.clone(), rx.clone()));
        tokio::time::sleep(Duration::from_millis(10)).await;
        let _ = tx.send(true);
        h_recon.await.unwrap();

        let (tx2, rx2) = watch::channel(true);
        let _ = tx2;
        run_nats_order_consumer(svc, rx2).await;
    }
}
