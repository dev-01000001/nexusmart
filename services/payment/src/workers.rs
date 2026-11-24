use std::collections::HashSet;
use std::time::{Duration, Instant};

use async_nats::jetstream::{
    self,
    consumer::PullConsumer,
    context::{GetStreamErrorKind, PublishErrorKind},
    stream, AckKind,
};
use async_nats::HeaderMap as NatsHeaders;
use chrono::{DateTime, Utc};
use futures_util::StreamExt;
use sqlx::PgPool;
use tokio::sync::watch;
use tokio::time::interval;
use tracing::{debug, error, info, warn, Instrument};
use uuid::Uuid;

use crate::domain::{vn_today, EventEnvelope};
use crate::service::PaymentService;
use crate::telemetry::{
    attach_parent_context, extract_context_from_nats, extract_context_from_trace_id,
    inject_current_span_to_nats,
};

const OUTBOX_POISON_LOG_RETRY: i32 = 10;
const OUTBOX_LEASE_DURATION: Duration = Duration::from_secs(30);
const OUTBOX_PUBLISH_TIMEOUT: Duration = Duration::from_secs(5);
const OUTBOX_RELAY_BACKOFF_INITIAL: Duration = Duration::from_millis(250);
const OUTBOX_RELAY_BACKOFF_MAX: Duration = Duration::from_secs(5);
const OUTBOX_ROW_BACKOFF_MAX_SECS: i64 = 60;
const OUTBOX_BATCH_LIMIT: i64 = 50;
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

#[derive(Debug, Clone, sqlx::FromRow)]
struct OutboxRow {
    id: Uuid,
    seq: i64,
    event_type: String,
    aggregate_id: String,
    trace_id: String,
    payload: serde_json::Value,
    retry_count: i32,
    created_at: DateTime<Utc>,
}

#[derive(Debug)]
struct OutboxPublishFailure {
    action: OutboxPublishErrorAction,
    message: String,
}

#[tonic::async_trait]
trait OutboxPublisher: Send + Sync {
    async fn publish_outbox_message(
        &self,
        subject: String,
        headers: NatsHeaders,
        payload: Vec<u8>,
    ) -> Result<(), OutboxPublishFailure>;
}

#[tonic::async_trait]
impl OutboxPublisher for jetstream::Context {
    async fn publish_outbox_message(
        &self,
        subject: String,
        headers: NatsHeaders,
        payload: Vec<u8>,
    ) -> Result<(), OutboxPublishFailure> {
        let ack_future = self
            .publish_with_headers(subject, headers, payload.into())
            .await
            .map_err(|e| OutboxPublishFailure {
                action: classify_publish_error(&e),
                message: e.to_string(),
            })?;
        ack_future.await.map_err(|e| OutboxPublishFailure {
            action: classify_publish_error(&e),
            message: e.to_string(),
        })?;
        Ok(())
    }
}

async fn ensure_required_streams_result<A: JetStreamAdmin>(
    admin: &A,
    stream_name: &str,
) -> anyhow::Result<()> {
    let primary_cfg = stream::Config {
        name: stream_name.to_string(),
        duplicate_window: Duration::from_secs(120),
        ..Default::default()
    };
    ensure_stream_subjects(admin, primary_cfg, CANONICAL_PRIMARY_SUBJECTS)
        .await
        .map_err(|e| anyhow::anyhow!("primary stream {stream_name}: {e}"))?;

    let dlq_cfg = stream::Config {
        name: DLQ_STREAM_NAME.to_string(),
        ..Default::default()
    };
    ensure_stream_subjects(admin, dlq_cfg, &["dlq.>"])
        .await
        .map_err(|e| anyhow::anyhow!("DLQ stream {DLQ_STREAM_NAME}: {e}"))?;

    Ok(())
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum OutboxPublishErrorAction {
    /// Infrastructure/broker/stream error: stop batch, trigger reconnect + stream re-ensure,
    /// and leave row `pending` without incrementing `retry_count` (never drops event).
    ReconnectWithoutCountingRetry,
    /// Non-infrastructure row error: increment `retry_count`, back off this row via
    /// `claimed_until = NOW() + min(2^retry, 60)s` while keeping `status = 'pending'`, and block
    /// later events of the same aggregate without dropping or reordering.
    CountRowRetry,
}

fn classify_publish_error_kind(
    kind: PublishErrorKind,
    js_api_status_code: Option<usize>,
) -> OutboxPublishErrorAction {
    match kind {
        PublishErrorKind::StreamNotFound
        | PublishErrorKind::TimedOut
        | PublishErrorKind::BrokenPipe => OutboxPublishErrorAction::ReconnectWithoutCountingRetry,
        PublishErrorKind::WrongLastMessageId | PublishErrorKind::WrongLastSequence => {
            OutboxPublishErrorAction::CountRowRetry
        }
        PublishErrorKind::Other => {
            // JetStream server rejections arrive wrapped in `PublishErrorKind::Other` with
            // `async_nats::jetstream::Error` as `source()`: 5xx (>= 500, e.g. 503 cluster
            // unavailable) is transient infra (D9-bis point 11); 4xx (e.g. 400
            // STREAM_MESSAGE_EXCEEDS_MAXIMUM 10054 / BAD_REQUEST) is a per-row rejection so one
            // oversized/malformed event does not stall unrelated aggregates.
            if let Some(code) = js_api_status_code {
                return if code >= 500 {
                    OutboxPublishErrorAction::ReconnectWithoutCountingRetry
                } else {
                    OutboxPublishErrorAction::CountRowRetry
                };
            }
            // ponytail: `async-nats 0.38` JetStream `send_publish` uses `Command::Request` without
            // pre-checking core NATS `client.max_payload`; payloads exceeding core NATS `max_payload`
            // drop the connection (`BrokenPipe`) or hit closed sender (`Other` with no
            // `jetstream::Error` source), so non-JetStream `Other` errors stay infra reconnect.
            OutboxPublishErrorAction::ReconnectWithoutCountingRetry
        }
    }
}

fn classify_publish_error(err: &jetstream::context::PublishError) -> OutboxPublishErrorAction {
    let js_code = std::error::Error::source(err)
        .and_then(|src| src.downcast_ref::<jetstream::Error>())
        .map(jetstream::Error::code);
    classify_publish_error_kind(err.kind(), js_code)
}

/// D9-bis per-row exponential backoff for non-infrastructure outbox publish failures:
/// `min(2^retry_count, 60)` seconds.
fn outbox_row_retry_backoff(retry_count: i32) -> Duration {
    if retry_count <= 0 {
        return Duration::from_secs(1);
    }
    if retry_count >= 6 {
        return Duration::from_secs(OUTBOX_ROW_BACKOFF_MAX_SECS as u64);
    }
    let secs = (1_i64 << retry_count).min(OUTBOX_ROW_BACKOFF_MAX_SECS);
    Duration::from_secs(secs as u64)
}

/// Pure exponential backoff step for outbox relay infrastructure/connect failures:
/// starts at 250ms, doubles on each consecutive failure, capped at 5s.
fn next_outbox_relay_backoff(current: Option<Duration>) -> Duration {
    match current {
        None => OUTBOX_RELAY_BACKOFF_INITIAL,
        Some(prev) if prev.is_zero() => OUTBOX_RELAY_BACKOFF_INITIAL,
        Some(prev) => prev.saturating_mul(2).min(OUTBOX_RELAY_BACKOFF_MAX),
    }
}

/// Pure state machine for `run_outbox_relay` backoff & outage warn throttling (D9-bis).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct OutboxRelayBackoff {
    poll_interval: Duration,
    current_backoff: Option<Duration>,
    consecutive_failures: u32,
}

impl OutboxRelayBackoff {
    fn new(poll_interval: Duration) -> Self {
        let effective_poll = if poll_interval.is_zero() {
            OUTBOX_RELAY_BACKOFF_INITIAL
        } else {
            poll_interval
        };
        Self {
            poll_interval: effective_poll,
            current_backoff: None,
            consecutive_failures: 0,
        }
    }

    /// Records an infrastructure/connect error and returns `(wait_duration, should_log_warn)`.
    /// Waits `max(poll_interval, backoff)` (D9-bis point 11) so an outage never polls faster than
    /// the healthy cadence. Logs `WARN` on failure #1 and every 12 consecutive failures (~60s at
    /// the 5s cap) so a prolonged NATS outage does not spam `WARN` every tick.
    fn on_infra_error(&mut self) -> (Duration, bool) {
        let next = next_outbox_relay_backoff(self.current_backoff);
        self.current_backoff = Some(next);
        self.consecutive_failures = self.consecutive_failures.saturating_add(1);
        let should_warn =
            self.consecutive_failures == 1 || self.consecutive_failures.is_multiple_of(12);
        (next.max(self.poll_interval), should_warn)
    }

    /// Resets backoff on a successful tick and returns `(poll_interval, recovered_after_failures)`.
    fn on_success(&mut self) -> (Duration, u32) {
        let recovered = self.consecutive_failures;
        self.current_backoff = None;
        self.consecutive_failures = 0;
        (self.poll_interval, recovered)
    }
}

async fn connect_jetstream_result(
    nats_url: &str,
    stream_name: &str,
) -> anyhow::Result<jetstream::Context> {
    let client = async_nats::connect(nats_url)
        .await
        .map_err(|e| anyhow::anyhow!("connect NATS ({nats_url}): {e}"))?;
    let js = jetstream::new(client);
    ensure_required_streams_result(&js, stream_name).await?;
    Ok(js)
}

/// Ensures the primary JetStream stream (`NEXUSMART`) and DLQ stream (`NEXUSMART_DLQ`) exist
/// per `docs/contracts.md` Section 5.
pub async fn connect_jetstream(nats_url: &str, stream_name: &str) -> Option<jetstream::Context> {
    match connect_jetstream_result(nats_url, stream_name).await {
        Ok(js) => {
            info!(nats_url = %nats_url, stream = %stream_name, "connected to NATS JetStream");
            Some(js)
        }
        Err(e) => {
            warn!(nats_url = %nats_url, error = %e, "NATS unavailable; workers will retry");
            None
        }
    }
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

#[derive(Debug)]
enum RelayBatchError {
    Db(sqlx::Error),
    Nats(String),
}

impl std::fmt::Display for RelayBatchError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Db(err) => write!(f, "outbox DB error: {err}"),
            Self::Nats(msg) => f.write_str(msg),
        }
    }
}

impl From<sqlx::Error> for RelayBatchError {
    fn from(err: sqlx::Error) -> Self {
        Self::Db(err)
    }
}

/// Background worker 2: Transactional Outbox relay -> NATS JetStream (`NEXUSMART` stream).
/// Backs off exponentially (250ms -> 5s, >= poll_interval) on NATS connect/publish/DB infra errors
/// without spamming `WARN` every tick, preserves the NATS connection on DB-only errors (P3-9),
/// and returns immediately to `outbox_poll_interval` upon recovery (D9-bis).
pub async fn run_outbox_relay(service: PaymentService, mut shutdown: watch::Receiver<bool>) {
    let poll_interval = service.config().outbox_poll_interval;
    let mut backoff = OutboxRelayBackoff::new(poll_interval);
    let mut js_opt: Option<jetstream::Context> = None;

    info!(
        poll_ms = poll_interval.as_millis() as u64,
        "started NATS JetStream outbox relay worker"
    );

    loop {
        if *shutdown.borrow() {
            info!("stopping outbox relay worker");
            break;
        }

        let mut tick_err: Option<String> = None;
        if js_opt.is_none() {
            match connect_jetstream_result(
                &service.config().nats_url,
                &service.config().nats_stream,
            )
            .await
            {
                Ok(js) => {
                    info!(
                        nats_url = %service.config().nats_url,
                        stream = %service.config().nats_stream,
                        "outbox relay connected to NATS JetStream"
                    );
                    js_opt = Some(js);
                }
                Err(e) => tick_err = Some(e.to_string()),
            }
        }

        if tick_err.is_none() {
            let js = js_opt.as_ref().expect("connected JetStream context");
            match relay_outbox_batch_with_publisher(
                service.pool(),
                js,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                OUTBOX_BATCH_LIMIT,
            )
            .await
            {
                Ok(()) => {}
                Err(RelayBatchError::Nats(msg)) => {
                    js_opt = None;
                    tick_err = Some(msg);
                }
                Err(RelayBatchError::Db(err)) => {
                    // P3-9: DB-only error must not tear down a healthy NATS connection.
                    tick_err = Some(err.to_string());
                }
            }
        }

        let wait = match tick_err {
            None => {
                let (next_wait, recovered) = backoff.on_success();
                if recovered > 0 {
                    info!(
                        service = "payment",
                        recovered_after_failures = recovered,
                        poll_ms = next_wait.as_millis() as u64,
                        "outbox relay recovered; restored normal poll interval"
                    );
                }
                next_wait
            }
            Some(err) => {
                let (next_wait, should_warn) = backoff.on_infra_error();
                if should_warn {
                    warn!(
                        service = "payment",
                        error = %err,
                        consecutive_failures = backoff.consecutive_failures,
                        backoff_ms = next_wait.as_millis() as u64,
                        "outbox relay encountered infrastructure error; backing off"
                    );
                } else {
                    debug!(
                        service = "payment",
                        error = %err,
                        consecutive_failures = backoff.consecutive_failures,
                        backoff_ms = next_wait.as_millis() as u64,
                        "outbox relay still backing off after infrastructure error"
                    );
                }
                next_wait
            }
        };

        tokio::select! {
            _ = shutdown.changed() => {
                info!("stopping outbox relay worker");
                break;
            }
            _ = tokio::time::sleep(wait) => {}
        }
    }
}

/// Phase 1 of D9-bis lease-based outbox relay:
/// Claims a batch of eligible pending outbox rows in a short transaction using `FOR UPDATE SKIP LOCKED`
/// ordered strictly by monotonic `seq` (never `created_at`) and commits immediately before any network I/O.
///
/// Two-layer aggregate ordering invariants enforced at claim time:
/// - Layer 1 (`locked`): never locks a row whose `aggregate_id` has an earlier `pending` row
///   (`prev.seq < o.seq`) currently leased or in per-row backoff (`prev.claimed_until > NOW()`).
/// - Layer 2 (`eligible`): never claims a row whose `aggregate_id` has an earlier `pending` row
///   (`prev.seq < l.seq`) not included in this same worker's `locked` set (e.g., skipped due to
///   concurrent `SKIP LOCKED` or batch limit truncation).
async fn claim_outbox_batch(
    pool: &PgPool,
    lease: Duration,
    batch_limit: i64,
) -> Result<(Uuid, Vec<OutboxRow>), sqlx::Error> {
    let claim_token = Uuid::new_v4();
    let mut tx = pool.begin().await?;

    let mut rows = sqlx::query_as::<_, OutboxRow>(
        r#"
        WITH locked AS MATERIALIZED (
            SELECT o.id, o.seq, o.aggregate_id
            FROM outbox_events o
            WHERE o.status = 'pending'
              AND (o.claimed_until IS NULL OR o.claimed_until <= NOW())
              AND (
                  o.aggregate_id = ''
                  OR NOT EXISTS (
                      SELECT 1
                      FROM outbox_events prev
                      WHERE prev.aggregate_id = o.aggregate_id
                        AND prev.status = 'pending'
                        AND prev.claimed_until > NOW()
                        AND prev.seq < o.seq
                  )
              )
            ORDER BY o.seq ASC
            LIMIT $3
            FOR UPDATE OF o SKIP LOCKED
        ),
        eligible AS (
            SELECT l.id
            FROM locked l
            WHERE l.aggregate_id = ''
               OR NOT EXISTS (
                   SELECT 1
                   FROM outbox_events prev
                   WHERE prev.aggregate_id = l.aggregate_id
                     AND prev.status = 'pending'
                     AND prev.seq < l.seq
                     AND NOT EXISTS (
                         SELECT 1 FROM locked l2 WHERE l2.id = prev.id
                     )
               )
        )
        UPDATE outbox_events o
        SET claimed_until = NOW() + make_interval(secs => $1),
            claim_token = $2
        FROM eligible e
        WHERE o.id = e.id
        RETURNING o.id, o.seq, o.event_type, o.aggregate_id, o.trace_id, o.payload, o.retry_count, o.created_at
        "#,
    )
    .bind(lease.as_secs_f64())
    .bind(claim_token)
    .bind(batch_limit)
    .fetch_all(&mut *tx)
    .await?;

    tx.commit().await?;
    rows.sort_by_key(|r| r.seq);
    Ok((claim_token, rows))
}

async fn release_batch_tail(
    pool: &PgPool,
    deferred_release: &mut Vec<Uuid>,
    tail: &[OutboxRow],
    claim_token: Uuid,
) {
    deferred_release.extend(tail.iter().map(|r| r.id));
    release_outbox_leases_best_effort(pool, deferred_release, claim_token).await;
}

/// D9-bis outbox relay:
/// 1. Captures a conservative monotonic `lease_deadline = Instant::now() + lease` BEFORE claiming,
///    claims a batch in a short transaction ordered strictly by `seq`, and commits immediately.
/// 2. Before each publish, verifies remaining lease budget `>= publish_timeout`; otherwise releases
///    deferred + remaining rows of the batch and stops cleanly.
/// 3. Publishes each event OUTSIDE any DB transaction with `Nats-Msg-Id = event_id`, bounded by
///    `tokio::time::timeout(publish_timeout, ...)` covering both send and PubAck.
/// 4. Non-infra row failures apply per-row backoff (`claimed_until = NOW() + min(2^retry, 60)s`,
///    keeping `status = 'pending'`), skip & defer-release later rows of the same `aggregate_id`,
///    and continue publishing independent aggregates in the same batch.
/// 5. Infra/timeout errors, lost leases (`0` rows affected), or mid-batch DB errors release all
///    deferred + remaining rows best-effort and stop the batch immediately.
async fn relay_outbox_batch_with_publisher<P: OutboxPublisher>(
    pool: &PgPool,
    publisher: &P,
    lease: Duration,
    publish_timeout: Duration,
    batch_limit: i64,
) -> Result<(), RelayBatchError> {
    let lease_deadline = Instant::now() + lease;
    let (claim_token, rows) = claim_outbox_batch(pool, lease, batch_limit).await?;
    if rows.is_empty() {
        return Ok(());
    }

    let mut failed_aggregates: HashSet<String> = HashSet::new();
    let mut deferred_release: Vec<Uuid> = Vec::new();

    for (idx, row) in rows.iter().enumerate() {
        let remaining = lease_deadline.saturating_duration_since(Instant::now());
        if remaining < publish_timeout {
            release_batch_tail(pool, &mut deferred_release, &rows[idx..], claim_token).await;
            warn!(
                service = "payment",
                remaining_ms = remaining.as_millis() as u64,
                publish_timeout_ms = publish_timeout.as_millis() as u64,
                released_count = deferred_release.len(),
                "outbox lease remaining time below publish timeout; releasing remaining rows and stopping batch"
            );
            return Ok(());
        }

        if !row.aggregate_id.is_empty() && failed_aggregates.contains(&row.aggregate_id) {
            deferred_release.push(row.id);
            continue;
        }

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
        let fallback_tid = (!row.trace_id.is_empty()).then_some(row.trace_id.as_str());
        let active_trace_id = attach_parent_context(&pub_span, parent_cx, fallback_tid);
        pub_span.record("trace_id", active_trace_id.as_str());

        let publish_outcome = if row.event_type.trim().is_empty() {
            Err(OutboxPublishFailure {
                action: OutboxPublishErrorAction::CountRowRetry,
                message: "empty event_type".to_string(),
            })
        } else {
            let envelope = EventEnvelope {
                event_id: row.id.to_string(),
                event_type: row.event_type.clone(),
                trace_id: active_trace_id.clone(),
                occurred_at: row.created_at,
                payload: row.payload.clone(),
            };
            match serde_json::to_vec(&envelope) {
                Err(e) => Err(OutboxPublishFailure {
                    action: OutboxPublishErrorAction::CountRowRetry,
                    message: e.to_string(),
                }),
                Ok(bytes) => {
                    let mut headers = NatsHeaders::new();
                    headers.insert("Nats-Msg-Id", row.id.to_string().as_str());
                    pub_span.in_scope(|| {
                        inject_current_span_to_nats(&mut headers, &active_trace_id);
                    });
                    match tokio::time::timeout(
                        publish_timeout,
                        publisher.publish_outbox_message(row.event_type.clone(), headers, bytes),
                    )
                    .instrument(pub_span.clone())
                    .await
                    {
                        Ok(res) => res,
                        Err(_) => Err(OutboxPublishFailure {
                            action: OutboxPublishErrorAction::ReconnectWithoutCountingRetry,
                            message: format!(
                                "publish/ack timed out after {}ms",
                                publish_timeout.as_millis()
                            ),
                        }),
                    }
                }
            }
        };

        match publish_outcome {
            Ok(()) => {
                let affected = match mark_outbox_sent_claimed(pool, row.id, claim_token).await {
                    Ok(n) => n,
                    Err(db_err) => {
                        release_batch_tail(
                            pool,
                            &mut deferred_release,
                            &rows[idx + 1..],
                            claim_token,
                        )
                        .await;
                        return Err(db_err.into());
                    }
                };
                if affected == 0 {
                    release_batch_tail(pool, &mut deferred_release, &rows[idx + 1..], claim_token)
                        .await;
                    pub_span.in_scope(|| {
                        warn!(
                            service = "payment",
                            event_id = %row.id,
                            event_type = %row.event_type,
                            trace_id = %active_trace_id,
                            released_count = deferred_release.len(),
                            "outbox claim lease lost before mark_sent; releasing remaining batch rows and stopping"
                        );
                    });
                    return Ok(());
                }
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
            Err(err) => match err.action {
                OutboxPublishErrorAction::ReconnectWithoutCountingRetry => {
                    let release_from = match release_outbox_on_infra_error(
                        pool,
                        row.id,
                        claim_token,
                        &err.message,
                    )
                    .await
                    {
                        Ok(_) => idx + 1,
                        Err(db_err) => {
                            warn!(
                                service = "payment",
                                event_id = %row.id,
                                error = %db_err,
                                "failed to record infra error on outbox row; releasing remaining batch leases best-effort"
                            );
                            idx
                        }
                    };
                    release_batch_tail(
                        pool,
                        &mut deferred_release,
                        &rows[release_from..],
                        claim_token,
                    )
                    .await;
                    return Err(RelayBatchError::Nats(format!(
                        "transient NATS publish error for event {}: {}",
                        row.id, err.message
                    )));
                }
                OutboxPublishErrorAction::CountRowRetry => {
                    match mark_outbox_retry_claimed(pool, row, claim_token, &err.message).await {
                        Ok(false) => {
                            release_batch_tail(
                                pool,
                                &mut deferred_release,
                                &rows[idx + 1..],
                                claim_token,
                            )
                            .await;
                            return Ok(());
                        }
                        Ok(true) => {}
                        Err(db_err) => {
                            release_batch_tail(
                                pool,
                                &mut deferred_release,
                                &rows[idx..],
                                claim_token,
                            )
                            .await;
                            return Err(db_err.into());
                        }
                    }
                    if !row.aggregate_id.is_empty() {
                        failed_aggregates.insert(row.aggregate_id.clone());
                    }
                }
            },
        }
    }

    release_batch_tail(pool, &mut deferred_release, &[], claim_token).await;
    Ok(())
}

async fn mark_outbox_sent_claimed(
    pool: &PgPool,
    id: Uuid,
    claim_token: Uuid,
) -> Result<u64, sqlx::Error> {
    let res = sqlx::query(
        r#"
        UPDATE outbox_events
        SET status = 'sent',
            sent_at = NOW(),
            last_error = NULL,
            claimed_until = NULL,
            claim_token = NULL
        WHERE id = $1
          AND claim_token = $2
          AND status = 'pending'
        "#,
    )
    .bind(id)
    .bind(claim_token)
    .execute(pool)
    .await?;
    Ok(res.rows_affected())
}

async fn mark_outbox_retry_claimed(
    pool: &PgPool,
    row: &OutboxRow,
    claim_token: Uuid,
    err_msg: &str,
) -> Result<bool, sqlx::Error> {
    let next_retry_estimate = row.retry_count.saturating_add(1);
    let backoff_secs = outbox_row_retry_backoff(next_retry_estimate).as_secs_f64();
    let new_retry: Option<i32> = sqlx::query_scalar(
        r#"
        UPDATE outbox_events
        SET retry_count = retry_count + 1,
            last_error = $1,
            claimed_until = NOW() + make_interval(secs => $2),
            claim_token = NULL
        WHERE id = $3
          AND claim_token = $4
          AND status = 'pending'
        RETURNING retry_count
        "#,
    )
    .bind(err_msg)
    .bind(backoff_secs)
    .bind(row.id)
    .bind(claim_token)
    .fetch_optional(pool)
    .await?;
    let Some(new_retry_count) = new_retry else {
        return Ok(false);
    };
    if new_retry_count >= OUTBOX_POISON_LOG_RETRY {
        error!(
            service = "payment",
            event_id = %row.id,
            event_type = %row.event_type,
            retry_count = new_retry_count,
            "outbox event repeatedly failed non-infra publish; keeping aggregate in backoff"
        );
    }
    Ok(true)
}

async fn release_outbox_on_infra_error(
    pool: &PgPool,
    id: Uuid,
    claim_token: Uuid,
    err_msg: &str,
) -> Result<u64, sqlx::Error> {
    let res = sqlx::query(
        r#"
        UPDATE outbox_events
        SET last_error = $1,
            claimed_until = NULL,
            claim_token = NULL
        WHERE id = $2
          AND claim_token = $3
          AND status = 'pending'
        "#,
    )
    .bind(err_msg)
    .bind(id)
    .bind(claim_token)
    .execute(pool)
    .await?;
    Ok(res.rows_affected())
}

async fn release_outbox_leases(
    pool: &PgPool,
    ids: &[Uuid],
    claim_token: Uuid,
) -> Result<u64, sqlx::Error> {
    if ids.is_empty() {
        return Ok(0);
    }
    let res = sqlx::query(
        r#"
        UPDATE outbox_events
        SET claimed_until = NULL,
            claim_token = NULL
        WHERE id = ANY($1)
          AND claim_token = $2
          AND status = 'pending'
        "#,
    )
    .bind(ids)
    .bind(claim_token)
    .execute(pool)
    .await?;
    Ok(res.rows_affected())
}

async fn release_outbox_leases_best_effort(pool: &PgPool, ids: &[Uuid], claim_token: Uuid) {
    if ids.is_empty() {
        return;
    }
    if let Err(e) = release_outbox_leases(pool, ids, claim_token).await {
        warn!(
            service = "payment",
            count = ids.len(),
            error = %e,
            "best-effort release of remaining outbox leases failed; leases will expire automatically"
        );
    }
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
            ensure_required_streams_result(
                &FailingAdmin {
                    fail_stream: "NEXUSMART"
                },
                "NEXUSMART"
            )
            .await
            .is_err(),
            "primary stream failure must cause connect_jetstream to return None and retry"
        );
        assert!(
            ensure_required_streams_result(
                &FailingAdmin {
                    fail_stream: DLQ_STREAM_NAME,
                },
                "NEXUSMART"
            )
            .await
            .is_err(),
            "DLQ stream failure must cause connect_jetstream to return None and retry"
        );
        assert!(
            ensure_required_streams_result(&FailingAdmin { fail_stream: "" }, "NEXUSMART")
                .await
                .is_ok(),
            "succeeding both primary and DLQ streams must return Ok"
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
                classify_publish_error_kind(infra_kind, None),
                OutboxPublishErrorAction::ReconnectWithoutCountingRetry
            );
        }
        for row_kind in [
            PublishErrorKind::WrongLastMessageId,
            PublishErrorKind::WrongLastSequence,
        ] {
            assert_eq!(
                classify_publish_error_kind(row_kind, None),
                OutboxPublishErrorAction::CountRowRetry
            );
        }
        // D9-bis point 11 + JetStream APIError classification under PublishErrorKind::Other:
        // 5xx (>= 500) -> infra reconnect; 4xx (e.g. 400 STREAM_MESSAGE_EXCEEDS_MAXIMUM) -> row backoff.
        assert_eq!(
            classify_publish_error_kind(PublishErrorKind::Other, Some(500)),
            OutboxPublishErrorAction::ReconnectWithoutCountingRetry
        );
        assert_eq!(
            classify_publish_error_kind(PublishErrorKind::Other, Some(503)),
            OutboxPublishErrorAction::ReconnectWithoutCountingRetry
        );
        assert_eq!(
            classify_publish_error_kind(PublishErrorKind::Other, Some(400)),
            OutboxPublishErrorAction::CountRowRetry
        );
        assert_eq!(
            classify_publish_error_kind(PublishErrorKind::Other, Some(413)),
            OutboxPublishErrorAction::CountRowRetry
        );
    }

    #[test]
    fn test_d9bis_outbox_relay_backoff_and_row_retry_backoff() {
        // Pure relay backoff schedule: 250ms -> 500ms -> 1s -> 2s -> 4s -> 5s (capped).
        let mut cur = None;
        let expected_ms = [250_u64, 500, 1000, 2000, 4000, 5000, 5000, 5000];
        for exp in expected_ms {
            let next = next_outbox_relay_backoff(cur);
            assert_eq!(next, Duration::from_millis(exp));
            cur = Some(next);
        }
        assert_eq!(
            next_outbox_relay_backoff(Some(Duration::ZERO)),
            Duration::from_millis(250)
        );

        // Stateful OutboxRelayBackoff (poll=250ms): doubles 250ms -> 5s, throttles WARN, resets on success.
        let poll = Duration::from_millis(250);
        let mut state = OutboxRelayBackoff::new(poll);
        let mut warn_ticks = Vec::new();
        let mut delays_ms = Vec::new();
        for step in 1..=15_u32 {
            let (delay, should_warn) = state.on_infra_error();
            delays_ms.push(delay.as_millis() as u64);
            if should_warn {
                warn_ticks.push(step);
            }
        }
        assert_eq!(
            &delays_ms[..7],
            &[250, 500, 1000, 2000, 4000, 5000, 5000],
            "relay backoff must double from 250ms up to 5s cap"
        );
        assert_eq!(
            warn_ticks,
            vec![1, 12],
            "outage must warn on first failure and periodic milestones only, not every tick"
        );

        // Recovery immediately restores configured poll_interval and resets failure counter.
        let (wait_after_ok, recovered) = state.on_success();
        assert_eq!(wait_after_ok, poll);
        assert_eq!(recovered, 15);
        assert_eq!(state.on_success(), (poll, 0));

        // Subsequent outage restarts cleanly from 250ms with immediate WARN.
        assert_eq!(state.on_infra_error(), (Duration::from_millis(250), true));

        // D9-bis point 11: wait is max(poll_interval, backoff) when poll_interval > 250ms.
        let mut slow_poll_state = OutboxRelayBackoff::new(Duration::from_millis(1000));
        let slow_waits: Vec<u64> = (0..6)
            .map(|_| slow_poll_state.on_infra_error().0.as_millis() as u64)
            .collect();
        assert_eq!(
            slow_waits,
            vec![1000, 1000, 1000, 2000, 4000, 5000],
            "relay wait must be max(poll_interval, backoff)"
        );

        // Pure per-row non-infra backoff: min(2^retry, 60) seconds.
        assert_eq!(outbox_row_retry_backoff(0), Duration::from_secs(1));
        assert_eq!(outbox_row_retry_backoff(1), Duration::from_secs(2));
        assert_eq!(outbox_row_retry_backoff(2), Duration::from_secs(4));
        assert_eq!(outbox_row_retry_backoff(3), Duration::from_secs(8));
        assert_eq!(outbox_row_retry_backoff(4), Duration::from_secs(16));
        assert_eq!(outbox_row_retry_backoff(5), Duration::from_secs(32));
        assert_eq!(outbox_row_retry_backoff(6), Duration::from_secs(60));
        assert_eq!(outbox_row_retry_backoff(10), Duration::from_secs(60));
        assert_eq!(outbox_row_retry_backoff(100), Duration::from_secs(60));
    }

    mod pg_outbox_tests {
        use super::*;
        use sqlx::postgres::PgPoolOptions;
        use sqlx::Executor;
        use std::collections::HashMap;
        use std::sync::{Arc, Mutex};
        use tokio::sync::{Barrier, OnceCell, Semaphore};

        static EXT_INIT: OnceCell<()> = OnceCell::const_new();
        static SCHEMA_SEM: Semaphore = Semaphore::const_new(4);

        struct OutboxTestDb {
            pool: PgPool,
            admin_url: String,
            schema: String,
            _permit: tokio::sync::SemaphorePermit<'static>,
        }

        impl OutboxTestDb {
            async fn setup(test_name: &str, max_conns: u32) -> Option<Self> {
                Self::setup_with_migrations(test_name, max_conns, true).await
            }

            async fn setup_with_migrations(
                test_name: &str,
                max_conns: u32,
                run_migrations: bool,
            ) -> Option<Self> {
                let db_url = match std::env::var("TEST_PAYMENT_DB_URL") {
                    Ok(v) if !v.trim().is_empty() => v,
                    _ => {
                        eprintln!("skipping {test_name}: TEST_PAYMENT_DB_URL not set");
                        return None;
                    }
                };

                let permit = SCHEMA_SEM.acquire().await.expect("schema semaphore");

                EXT_INIT
                    .get_or_init(|| async {
                        let base = PgPoolOptions::new()
                            .max_connections(2)
                            .acquire_timeout(Duration::from_secs(10))
                            .connect(&db_url)
                            .await
                            .expect("connect base TEST_PAYMENT_DB_URL");
                        let mut conn = base.acquire().await.expect("acquire base conn");
                        sqlx::query("SELECT pg_advisory_lock(88442211)")
                            .execute(&mut *conn)
                            .await
                            .expect("advisory lock");
                        sqlx::query("CREATE EXTENSION IF NOT EXISTS pgcrypto")
                            .execute(&mut *conn)
                            .await
                            .expect("create pgcrypto");
                        sqlx::query("SELECT pg_advisory_unlock(88442211)")
                            .execute(&mut *conn)
                            .await
                            .expect("advisory unlock");
                        drop(conn);
                        base.close().await;
                    })
                    .await;

                let schema = format!("payoutbox_{}", Uuid::new_v4().simple());
                let base = PgPoolOptions::new()
                    .max_connections(2)
                    .acquire_timeout(Duration::from_secs(10))
                    .connect(&db_url)
                    .await
                    .expect("connect admin pool");
                sqlx::query(&format!("CREATE SCHEMA {schema}"))
                    .execute(&base)
                    .await
                    .expect("create isolated outbox schema");
                base.close().await;

                let schema_for_hook = schema.clone();
                let pool = PgPoolOptions::new()
                    .max_connections(max_conns)
                    .acquire_timeout(Duration::from_secs(15))
                    .after_connect(move |conn, _meta| {
                        let s = schema_for_hook.clone();
                        Box::pin(async move {
                            conn.execute(format!("SET search_path TO {s}, public").as_str())
                                .await?;
                            Ok(())
                        })
                    })
                    .connect(&db_url)
                    .await
                    .expect("connect isolated outbox pool");

                if run_migrations {
                    sqlx::migrate!("./migrations")
                        .run(&pool)
                        .await
                        .expect("run migrations on isolated outbox schema");
                }

                Some(Self {
                    pool,
                    admin_url: db_url,
                    schema,
                    _permit: permit,
                })
            }

            async fn teardown(self) {
                self.pool.close().await;
                if let Ok(base) = PgPoolOptions::new()
                    .max_connections(1)
                    .acquire_timeout(Duration::from_secs(10))
                    .connect(&self.admin_url)
                    .await
                {
                    let _ = sqlx::query(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.schema))
                        .execute(&base)
                        .await;
                    base.close().await;
                }
            }
        }

        async fn insert_event(
            pool: &PgPool,
            event_type: &str,
            aggregate_id: &str,
            created_at: DateTime<Utc>,
            payload: serde_json::Value,
        ) -> Uuid {
            sqlx::query_scalar(
                r#"
                INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload, created_at)
                VALUES ($1, $2, $3, $4, $5)
                RETURNING id
                "#,
            )
            .bind(event_type)
            .bind(aggregate_id)
            .bind("0123456789abcdef0123456789abcdef")
            .bind(payload)
            .bind(created_at)
            .fetch_one(pool)
            .await
            .expect("insert outbox event")
        }

        #[derive(Debug, Clone)]
        struct PublishedRecord {
            event_id: Uuid,
            subject: String,
            nats_msg_id: String,
            has_trace_header: bool,
            aggregate_marker: String,
        }

        struct RecordingPublisher {
            pool: PgPool,
            verify_unlocked: bool,
            fail_by_id: Arc<Mutex<HashMap<Uuid, (OutboxPublishErrorAction, String)>>>,
            published: Arc<Mutex<Vec<PublishedRecord>>>,
        }

        impl RecordingPublisher {
            fn new(pool: PgPool, verify_unlocked: bool) -> Self {
                Self {
                    pool,
                    verify_unlocked,
                    fail_by_id: Arc::new(Mutex::new(HashMap::new())),
                    published: Arc::new(Mutex::new(Vec::new())),
                }
            }

            fn fail_event(&self, id: Uuid, action: OutboxPublishErrorAction, msg: &str) {
                self.fail_by_id
                    .lock()
                    .unwrap()
                    .insert(id, (action, msg.to_string()));
            }

            fn clear_failure(&self, id: Uuid) {
                self.fail_by_id.lock().unwrap().remove(&id);
            }

            fn records(&self) -> Vec<PublishedRecord> {
                self.published.lock().unwrap().clone()
            }
        }

        #[tonic::async_trait]
        impl OutboxPublisher for RecordingPublisher {
            async fn publish_outbox_message(
                &self,
                subject: String,
                headers: NatsHeaders,
                payload: Vec<u8>,
            ) -> Result<(), OutboxPublishFailure> {
                let envelope: EventEnvelope =
                    serde_json::from_slice(&payload).expect("valid EventEnvelope JSON");
                let event_id = Uuid::parse_str(&envelope.event_id).expect("event_id UUID");
                let nats_msg_id = headers
                    .get("Nats-Msg-Id")
                    .map(|v| v.as_str().to_string())
                    .unwrap_or_default();
                let has_trace_header =
                    headers.get("traceparent").is_some() || headers.get("x-trace-id").is_some();

                if self.verify_unlocked {
                    // D9 core proof: acquire a FOR UPDATE NOWAIT lock on this exact outbox row from
                    // a separate transaction DURING publish. If the relay held a DB transaction open
                    // across NATS publish, Postgres immediately fails with 55P03 lock_not_available.
                    let mut probe_tx = self.pool.begin().await.expect("begin probe tx");
                    let (status, claim_token, lease_active): (String, Option<Uuid>, bool) =
                        sqlx::query_as(
                            r#"
                            SELECT status, claim_token, (claimed_until IS NOT NULL AND claimed_until > NOW())
                            FROM outbox_events
                            WHERE id = $1
                            FOR UPDATE NOWAIT
                            "#,
                        )
                        .bind(event_id)
                        .fetch_one(&mut *probe_tx)
                        .await
                        .expect("D9 violation: outbox row is STILL locked by a DB transaction during NATS publish!");

                    assert_eq!(status, "pending");
                    assert!(
                        claim_token.is_some(),
                        "claim_token must be committed before NATS publish"
                    );
                    assert!(
                        lease_active,
                        "claimed_until lease must be active during NATS publish"
                    );
                    probe_tx.rollback().await.expect("rollback probe tx");
                }

                if let Some((action, message)) = self.fail_by_id.lock().unwrap().get(&event_id) {
                    return Err(OutboxPublishFailure {
                        action: *action,
                        message: message.clone(),
                    });
                }

                let aggregate_marker = envelope
                    .payload
                    .get("agg")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string();
                self.published.lock().unwrap().push(PublishedRecord {
                    event_id,
                    subject,
                    nats_msg_id,
                    has_trace_header,
                    aggregate_marker,
                });
                Ok(())
            }
        }

        #[tokio::test]
        async fn test_pg_d9_outbox_publishes_outside_db_tx_and_preserves_order() {
            let Some(db) = OutboxTestDb::setup(
                "test_pg_d9_outbox_publishes_outside_db_tx_and_preserves_order",
                4,
            )
            .await
            else {
                return;
            };

            let t0 = Utc::now() - chrono::Duration::seconds(10);
            let e1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-A",
                t0,
                serde_json::json!({"agg": "agg-A", "seq": 1}),
            )
            .await;
            let e2 = insert_event(
                &db.pool,
                "payment.refunded",
                "agg-A",
                t0 + chrono::Duration::milliseconds(100),
                serde_json::json!({"agg": "agg-A", "seq": 2}),
            )
            .await;
            let e3 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-B",
                t0 + chrono::Duration::milliseconds(200),
                serde_json::json!({"agg": "agg-B", "seq": 1}),
            )
            .await;

            let publisher = RecordingPublisher::new(db.pool.clone(), true);
            relay_outbox_batch_with_publisher(
                &db.pool,
                &publisher,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .expect("relay batch");

            let records = publisher.records();
            let sent_ids: Vec<Uuid> = records.iter().map(|r| r.event_id).collect();
            assert_eq!(sent_ids, vec![e1, e2, e3], "must preserve seq order");
            for rec in &records {
                assert_eq!(
                    rec.nats_msg_id,
                    rec.event_id.to_string(),
                    "Nats-Msg-Id must equal event_id"
                );
                assert!(rec.has_trace_header, "must inject trace header");
            }
            assert_eq!(records[0].subject, "payment.succeeded");
            assert_eq!(records[1].subject, "payment.refunded");

            #[derive(sqlx::FromRow)]
            struct FinalizedOutboxRow {
                status: String,
                sent_at: Option<DateTime<Utc>>,
                claimed_until: Option<DateTime<Utc>>,
                claim_token: Option<Uuid>,
            }

            let rows = sqlx::query_as::<_, FinalizedOutboxRow>(
                r#"
                SELECT status, sent_at, claimed_until, claim_token
                FROM outbox_events
                ORDER BY seq ASC
                "#,
            )
            .fetch_all(&db.pool)
            .await
            .expect("verify finalized rows");
            assert_eq!(rows.len(), 3);
            for row in rows {
                assert_eq!(row.status, "sent");
                assert!(row.sent_at.is_some());
                assert!(row.claimed_until.is_none(), "lease must be cleared on sent");
                assert!(
                    row.claim_token.is_none(),
                    "claim_token must be cleared on sent"
                );
            }

            // Subsequent batch on empty pending table is a clean no-op.
            relay_outbox_batch_with_publisher(
                &db.pool,
                &publisher,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .expect("empty batch");
            assert_eq!(publisher.records().len(), 3);

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_d9_outbox_lease_expiry_reclaim_and_stale_token_guard() {
            let Some(db) = OutboxTestDb::setup(
                "test_pg_d9_outbox_lease_expiry_reclaim_and_stale_token_guard",
                4,
            )
            .await
            else {
                return;
            };

            let e1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-lease",
                Utc::now() - chrono::Duration::seconds(5),
                serde_json::json!({"agg": "agg-lease"}),
            )
            .await;

            // Worker 1 claims e1 and stalls/crashes without publishing.
            let (token1, claimed1) = claim_outbox_batch(&db.pool, OUTBOX_LEASE_DURATION, 50)
                .await
                .expect("worker 1 claim");
            assert_eq!(claimed1.len(), 1);
            assert_eq!(claimed1[0].id, e1);

            // While lease is active, Worker 2 claims nothing.
            let (_, claimed_while_active) = claim_outbox_batch(&db.pool, OUTBOX_LEASE_DURATION, 50)
                .await
                .expect("worker 2 active-lease check");
            assert!(
                claimed_while_active.is_empty(),
                "active lease must prevent another worker from claiming"
            );

            // Simulate lease expiration after worker 1 crash.
            sqlx::query("UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '5 seconds' WHERE id = $1")
                .bind(e1)
                .execute(&db.pool)
                .await
                .expect("expire lease");

            // Worker 2 reclaims e1 with a brand-new claim_token.
            let (token2, claimed2) = claim_outbox_batch(&db.pool, OUTBOX_LEASE_DURATION, 50)
                .await
                .expect("worker 2 reclaim");
            assert_eq!(claimed2.len(), 1);
            assert_eq!(claimed2[0].id, e1);
            assert_ne!(token1, token2);

            // Stale Worker 1 wakes up while Worker 2 holds token2: every token-guarded mutation
            // with stale token1 must affect 0 rows and leave Worker 2's lease untouched.
            assert_eq!(
                mark_outbox_sent_claimed(&db.pool, e1, token1)
                    .await
                    .expect("stale mark_sent"),
                0
            );
            assert!(
                !mark_outbox_retry_claimed(&db.pool, &claimed1[0], token1, "stale err")
                    .await
                    .expect("stale mark_retry")
            );
            assert_eq!(
                release_outbox_leases(&db.pool, &[e1], token1)
                    .await
                    .expect("stale release"),
                0
            );

            let (status_mid, retry_mid, cur_token): (String, i32, Option<Uuid>) = sqlx::query_as(
                "SELECT status, retry_count, claim_token FROM outbox_events WHERE id = $1",
            )
            .bind(e1)
            .fetch_one(&db.pool)
            .await
            .expect("read mid state");
            assert_eq!(status_mid, "pending");
            assert_eq!(retry_mid, 0);
            assert_eq!(cur_token, Some(token2));

            // Worker 2 finishes sending using token2; stale token1 still affects 0 rows.
            assert_eq!(
                mark_outbox_sent_claimed(&db.pool, e1, token2)
                    .await
                    .expect("worker 2 mark_sent"),
                1
            );
            assert_eq!(
                mark_outbox_sent_claimed(&db.pool, e1, token1)
                    .await
                    .expect("stale post-sent"),
                0
            );

            // End-to-end redelivery after lease expiry: redelivery is legal and MUST carry the
            // exact same `Nats-Msg-Id = event_id` so JetStream / consumers deduplicate cleanly.
            let e_redeliver = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-redeliver",
                Utc::now() - chrono::Duration::seconds(2),
                serde_json::json!({"agg": "agg-redeliver"}),
            )
            .await;
            let seen_msg_ids = Arc::new(Mutex::new(Vec::<String>::new()));

            struct CrashBeforeMarkSentPublisher {
                pool: PgPool,
                expire_once: std::sync::atomic::AtomicBool,
                seen_msg_ids: Arc<Mutex<Vec<String>>>,
            }
            #[tonic::async_trait]
            impl OutboxPublisher for CrashBeforeMarkSentPublisher {
                async fn publish_outbox_message(
                    &self,
                    _subject: String,
                    headers: NatsHeaders,
                    payload: Vec<u8>,
                ) -> Result<(), OutboxPublishFailure> {
                    let msg_id = headers
                        .get("Nats-Msg-Id")
                        .map(|v| v.as_str().to_string())
                        .unwrap_or_default();
                    self.seen_msg_ids.lock().unwrap().push(msg_id);
                    if self
                        .expire_once
                        .swap(false, std::sync::atomic::Ordering::SeqCst)
                    {
                        let env: EventEnvelope = serde_json::from_slice(&payload).unwrap();
                        let eid = Uuid::parse_str(&env.event_id).unwrap();
                        // Simulate worker 1 lease expiring & worker 2 reclaiming before mark_sent.
                        sqlx::query(
                            "UPDATE outbox_events SET claim_token = $1, claimed_until = NOW() - INTERVAL '1 second' WHERE id = $2",
                        )
                        .bind(Uuid::new_v4())
                        .bind(eid)
                        .execute(&self.pool)
                        .await
                        .unwrap();
                    }
                    Ok(())
                }
            }

            let redeliver_pub = CrashBeforeMarkSentPublisher {
                pool: db.pool.clone(),
                expire_once: std::sync::atomic::AtomicBool::new(true),
                seen_msg_ids: seen_msg_ids.clone(),
            };
            // Worker 1 publishes, loses lease before mark_sent -> stops cleanly.
            relay_outbox_batch_with_publisher(
                &db.pool,
                &redeliver_pub,
                Duration::from_secs(3),
                Duration::from_secs(1),
                50,
            )
            .await
            .expect("worker 1 stops on lost lease");
            // Worker 2 reclaims expired row and republishes with identical Nats-Msg-Id.
            relay_outbox_batch_with_publisher(
                &db.pool,
                &redeliver_pub,
                Duration::from_secs(3),
                Duration::from_secs(1),
                50,
            )
            .await
            .expect("worker 2 reclaims and completes");

            let ids = seen_msg_ids.lock().unwrap().clone();
            assert_eq!(
                ids,
                vec![e_redeliver.to_string(), e_redeliver.to_string()],
                "redelivery after lease expiry must carry the exact same Nats-Msg-Id"
            );

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_d9_outbox_same_aggregate_failure_blocks_later_sibling_only() {
            let Some(db) = OutboxTestDb::setup(
                "test_pg_d9_outbox_same_aggregate_failure_blocks_later_sibling_only",
                4,
            )
            .await
            else {
                return;
            };

            let t0 = Utc::now() - chrono::Duration::seconds(10);
            let a1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-A",
                t0,
                serde_json::json!({"agg": "agg-A", "step": 1}),
            )
            .await;
            let a2 = insert_event(
                &db.pool,
                "payment.refunded",
                "agg-A",
                t0 + chrono::Duration::milliseconds(100),
                serde_json::json!({"agg": "agg-A", "step": 2}),
            )
            .await;
            let b1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-B",
                t0 + chrono::Duration::milliseconds(200),
                serde_json::json!({"agg": "agg-B", "step": 1}),
            )
            .await;
            let e_empty = insert_event(
                &db.pool,
                "   ",
                "agg-empty",
                t0 + chrono::Duration::milliseconds(300),
                serde_json::json!({"agg": "agg-empty"}),
            )
            .await;

            // Pre-existing legacy `failed` row (if any from before D9-bis) must remain untouched
            // for operator inspection and must never be replayed.
            let legacy_failed_id: Uuid = sqlx::query_scalar(
                r#"
                INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload, status, retry_count)
                VALUES ('payment.succeeded', 'agg-legacy', '0123456789abcdef0123456789abcdef', '{"agg":"agg-legacy"}'::jsonb, 'failed', 10)
                RETURNING id
                "#,
            )
            .fetch_one(&db.pool)
            .await
            .unwrap();

            let publisher = RecordingPublisher::new(db.pool.clone(), true);
            publisher.fail_event(
                a1,
                OutboxPublishErrorAction::CountRowRetry,
                "wrong last msg id",
            );

            // Batch 1: a1 fails (retry=1, backoff claimed_until > NOW(), stays pending),
            // a2 MUST NOT be published (same agg-A, lease released), b1 IS published (agg-B),
            // e_empty fails validation and gets per-row backoff (stays pending, claimed_until > NOW()).
            relay_outbox_batch_with_publisher(
                &db.pool,
                &publisher,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .expect("batch 1 completes without infra error");

            let sent_after_b1: Vec<Uuid> = publisher.records().iter().map(|r| r.event_id).collect();
            assert_eq!(
                sent_after_b1,
                vec![b1],
                "only agg-B may publish when agg-A head a1 fails; a2 must NOT jump ahead"
            );

            let (s1, r1, c1, in_backoff1): (String, i32, Option<Uuid>, bool) = sqlx::query_as(
                "SELECT status, retry_count, claim_token, (claimed_until IS NOT NULL AND claimed_until > NOW()) FROM outbox_events WHERE id = $1",
            )
            .bind(a1)
            .fetch_one(&db.pool)
            .await
            .unwrap();
            assert_eq!((s1.as_str(), r1, c1), ("pending", 1, None));
            assert!(
                in_backoff1,
                "non-infra failure must set future backoff claimed_until > NOW() on Postgres clock"
            );

            let (s2, r2, c2, u2): (String, i32, Option<Uuid>, Option<DateTime<Utc>>) =
                sqlx::query_as(
                    "SELECT status, retry_count, claim_token, claimed_until FROM outbox_events WHERE id = $1",
                )
                .bind(a2)
                .fetch_one(&db.pool)
                .await
                .unwrap();
            assert_eq!(
                (s2.as_str(), r2, c2, u2),
                ("pending", 0, None, None),
                "skipped sibling a2 must have retry_count=0 and released lease"
            );

            let (s_emp, r_emp, c_emp, in_backoff_emp): (String, i32, Option<Uuid>, bool) =
                sqlx::query_as(
                    "SELECT status, retry_count, claim_token, (claimed_until IS NOT NULL AND claimed_until > NOW()) FROM outbox_events WHERE id = $1",
                )
                .bind(e_empty)
                .fetch_one(&db.pool)
                .await
                .unwrap();
            assert_eq!((s_emp.as_str(), r_emp, c_emp), ("pending", 1, None));
            assert!(
                in_backoff_emp,
                "empty event_type must also apply per-row backoff claimed_until > NOW() on Postgres clock"
            );

            // While a1 is still in backoff (`claimed_until > NOW()`), a new event b2 for agg-B
            // MUST still publish immediately, while a2 MUST stay blocked by Layer 1 of claim!
            let b2 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-B",
                Utc::now(),
                serde_json::json!({"agg": "agg-B", "step": 2}),
            )
            .await;
            relay_outbox_batch_with_publisher(
                &db.pool,
                &publisher,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .expect("batch while a1 in backoff");
            assert_eq!(
                publisher
                    .records()
                    .iter()
                    .map(|r| r.event_id)
                    .collect::<Vec<_>>(),
                vec![b1, b2],
                "independent agg-B must continue while agg-A is held in backoff"
            );

            // Keep failing a1 through OUTBOX_POISON_LOG_RETRY (10) consecutive attempts by fast-forwarding
            // a1's backoff window each round: a1 MUST remain `pending` (NEVER `failed`), reach 60s cap,
            // log ERROR at retry >= 10, and NEVER allow a2 to overtake a1!
            for _ in 1..OUTBOX_POISON_LOG_RETRY {
                sqlx::query(
                    "UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = $1",
                )
                .bind(a1)
                .execute(&db.pool)
                .await
                .unwrap();
                relay_outbox_batch_with_publisher(
                    &db.pool,
                    &publisher,
                    OUTBOX_LEASE_DURATION,
                    OUTBOX_PUBLISH_TIMEOUT,
                    50,
                )
                .await
                .expect("retry batch");
            }

            let (s1_after10, r1_after10, c1_after10, backoff_secs): (
                String,
                i32,
                Option<Uuid>,
                f64,
            ) = sqlx::query_as(
                "SELECT status, retry_count, claim_token, EXTRACT(EPOCH FROM (claimed_until - NOW()))::DOUBLE PRECISION FROM outbox_events WHERE id = $1",
            )
            .bind(a1)
            .fetch_one(&db.pool)
            .await
            .unwrap();
            assert_eq!(
                (s1_after10.as_str(), r1_after10, c1_after10),
                ("pending", OUTBOX_POISON_LOG_RETRY, None),
                "D9-bis: must NEVER mark outbox event as 'failed' after 10 retries"
            );
            assert!(
                (45.0..=65.0).contains(&backoff_secs),
                "retry >= 6 backoff must cap at 60s (got {backoff_secs}s)"
            );
            assert_eq!(
                publisher
                    .records()
                    .iter()
                    .map(|r| r.event_id)
                    .collect::<Vec<_>>(),
                vec![b1, b2],
                "a2 must NEVER be published ahead of failing a1 even after 10 retries"
            );

            // Legacy `failed` row remains untouched (`status = 'failed'`, never replayed).
            let legacy_status: String =
                sqlx::query_scalar("SELECT status FROM outbox_events WHERE id = $1")
                    .bind(legacy_failed_id)
                    .fetch_one(&db.pool)
                    .await
                    .unwrap();
            assert_eq!(legacy_status, "failed");

            // Once a1 recovers and its backoff expires, a1 and a2 publish strictly in order [a1, a2].
            publisher.clear_failure(a1);
            sqlx::query(
                "UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = $1",
            )
            .bind(a1)
            .execute(&db.pool)
            .await
            .unwrap();
            relay_outbox_batch_with_publisher(
                &db.pool,
                &publisher,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .expect("recovery batch for agg-A");
            assert_eq!(
                publisher
                    .records()
                    .iter()
                    .map(|r| r.event_id)
                    .collect::<Vec<_>>(),
                vec![b1, b2, a1, a2],
                "after a1 recovers, agg-A must drain strictly in order [a1, a2]"
            );

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_d9_outbox_infra_error_preserves_retry_count_and_releases_remaining_leases()
        {
            let Some(db) = OutboxTestDb::setup(
                "test_pg_d9_outbox_infra_error_preserves_retry_count_and_releases_remaining_leases",
                4,
            )
            .await
            else {
                return;
            };

            let t0 = Utc::now() - chrono::Duration::seconds(10);
            let e1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-1",
                t0,
                serde_json::json!({"agg": "agg-1"}),
            )
            .await;
            let e2 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-2",
                t0 + chrono::Duration::milliseconds(100),
                serde_json::json!({"agg": "agg-2"}),
            )
            .await;
            let e3 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-3",
                t0 + chrono::Duration::milliseconds(200),
                serde_json::json!({"agg": "agg-3"}),
            )
            .await;

            let publisher = RecordingPublisher::new(db.pool.clone(), true);
            publisher.fail_event(
                e2,
                OutboxPublishErrorAction::ReconnectWithoutCountingRetry,
                "nats connection broken pipe",
            );

            let err = relay_outbox_batch_with_publisher(
                &db.pool,
                &publisher,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .expect_err("infra error must return Err to trigger reconnect");
            assert!(
                matches!(&err, RelayBatchError::Nats(msg) if msg.contains("transient NATS publish error")),
                "expected RelayBatchError::Nats, got {err:?}"
            );

            // e1 stays sent; e2 stays pending with retry_count=0 + last_error + released lease;
            // e3 stays pending with retry_count=0 + released lease so next reconnect claims immediately.
            let row1: (String, i32, Option<Uuid>) = sqlx::query_as(
                "SELECT status, retry_count, claim_token FROM outbox_events WHERE id = $1",
            )
            .bind(e1)
            .fetch_one(&db.pool)
            .await
            .unwrap();
            assert_eq!((row1.0.as_str(), row1.1, row1.2), ("sent", 0, None));

            let row2: (String, i32, Option<String>, Option<DateTime<Utc>>, Option<Uuid>) =
                sqlx::query_as(
                    "SELECT status, retry_count, last_error, claimed_until, claim_token FROM outbox_events WHERE id = $1",
                )
                .bind(e2)
                .fetch_one(&db.pool)
                .await
                .unwrap();
            assert_eq!(row2.0, "pending");
            assert_eq!(row2.1, 0, "infra error must NOT increment retry_count");
            assert!(row2.2.unwrap_or_default().contains("broken pipe"));
            assert!(row2.3.is_none());
            assert!(row2.4.is_none());

            let row3: (String, i32, Option<DateTime<Utc>>, Option<Uuid>) = sqlx::query_as(
                "SELECT status, retry_count, claimed_until, claim_token FROM outbox_events WHERE id = $1",
            )
            .bind(e3)
            .fetch_one(&db.pool)
            .await
            .unwrap();
            assert_eq!(row3.0, "pending");
            assert_eq!(row3.1, 0);
            assert!(row3.2.is_none(), "remaining row lease must be released");
            assert!(row3.3.is_none());

            // After reconnect succeeds, e2 and e3 publish immediately in order.
            publisher.clear_failure(e2);
            relay_outbox_batch_with_publisher(
                &db.pool,
                &publisher,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .expect("recovery batch");
            assert_eq!(
                publisher
                    .records()
                    .iter()
                    .map(|r| r.event_id)
                    .collect::<Vec<_>>(),
                vec![e1, e2, e3]
            );

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_d9_outbox_concurrent_workers_disjoint_and_ordered() {
            let Some(db) = OutboxTestDb::setup(
                "test_pg_d9_outbox_concurrent_workers_disjoint_and_ordered",
                8,
            )
            .await
            else {
                return;
            };

            let t0 = Utc::now() - chrono::Duration::seconds(30);
            // Part A: Explicit cross-worker head-of-aggregate guard.
            let a1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-head",
                t0,
                serde_json::json!({"agg": "agg-head", "seq": 1}),
            )
            .await;
            let a2 = insert_event(
                &db.pool,
                "payment.refunded",
                "agg-head",
                t0 + chrono::Duration::milliseconds(10),
                serde_json::json!({"agg": "agg-head", "seq": 2}),
            )
            .await;
            let b1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-other",
                t0 + chrono::Duration::milliseconds(20),
                serde_json::json!({"agg": "agg-other", "seq": 1}),
            )
            .await;

            // Worker 1 claims only a1 (limit=1).
            let (tok_w1, w1_rows) = claim_outbox_batch(&db.pool, OUTBOX_LEASE_DURATION, 1)
                .await
                .unwrap();
            assert_eq!(w1_rows.len(), 1);
            assert_eq!(w1_rows[0].id, a1);

            // Worker 2 runs with large limit=50: MUST skip a2 (because a1 of agg-head is still
            // pending & leased by Worker 1), MUST still claim b1 (independent aggregate)!
            let (tok_w2, w2_rows) = claim_outbox_batch(&db.pool, OUTBOX_LEASE_DURATION, 50)
                .await
                .unwrap();
            assert_eq!(
                w2_rows.iter().map(|r| r.id).collect::<Vec<_>>(),
                vec![b1],
                "worker 2 must NOT claim a2 while a1 is leased, but must claim b1"
            );

            // Clean up Part A rows.
            assert_eq!(
                mark_outbox_sent_claimed(&db.pool, a1, tok_w1)
                    .await
                    .unwrap(),
                1
            );
            assert_eq!(
                mark_outbox_sent_claimed(&db.pool, b1, tok_w2)
                    .await
                    .unwrap(),
                1
            );
            let pub_a = RecordingPublisher::new(db.pool.clone(), true);
            relay_outbox_batch_with_publisher(
                &db.pool,
                &pub_a,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .unwrap();
            assert_eq!(
                pub_a
                    .records()
                    .iter()
                    .map(|r| r.event_id)
                    .collect::<Vec<_>>(),
                vec![a2]
            );

            // Part B: 4 concurrent workers racing over 20 events (5 aggregates x 4 ordered events)
            // with small batch_limit=3 to maximize contention across claim transactions.
            let mut expected_per_agg: HashMap<String, Vec<Uuid>> = HashMap::new();
            let mut seq_ms = 1000_i64;
            for step in 0..4 {
                for agg_idx in 0..5 {
                    let agg = format!("race-agg-{agg_idx}");
                    let id = insert_event(
                        &db.pool,
                        "payment.succeeded",
                        &agg,
                        t0 + chrono::Duration::milliseconds(seq_ms),
                        serde_json::json!({"agg": agg, "step": step}),
                    )
                    .await;
                    expected_per_agg.entry(agg).or_default().push(id);
                    seq_ms += 10;
                }
            }

            // Part B races 4 workers concurrently on `claim_outbox_batch`; disable per-row
            // `FOR UPDATE NOWAIT` probing here so sibling workers' transient PostgreSQL EPQ tuple
            // locks during concurrent `SKIP LOCKED` scans don't cause spurious 55P03 errors
            // (out-of-tx lock freedom is strictly asserted via `verify_unlocked = true` in Part A
            // and all single-worker D9 tests).
            let publisher = Arc::new(RecordingPublisher::new(db.pool.clone(), false));
            let barrier = Arc::new(Barrier::new(4));
            let mut handles = Vec::new();
            for _ in 0..4 {
                let pool = db.pool.clone();
                let publisher = publisher.clone();
                let barrier = barrier.clone();
                handles.push(tokio::spawn(async move {
                    barrier.wait().await;
                    loop {
                        let remaining: i64 = sqlx::query_scalar(
                            "SELECT COUNT(*)::BIGINT FROM outbox_events WHERE status = 'pending'",
                        )
                        .fetch_one(&pool)
                        .await
                        .unwrap();
                        if remaining == 0 {
                            break;
                        }
                        relay_outbox_batch_with_publisher(
                            &pool,
                            publisher.as_ref(),
                            OUTBOX_LEASE_DURATION,
                            OUTBOX_PUBLISH_TIMEOUT,
                            3,
                        )
                        .await
                        .unwrap();
                        tokio::task::yield_now().await;
                    }
                }));
            }
            for h in handles {
                h.await.expect("worker task joined cleanly");
            }

            let recs = publisher.records();
            assert_eq!(
                recs.len(),
                20,
                "every event must be published strictly once"
            );

            let mut actual_per_agg: HashMap<String, Vec<Uuid>> = HashMap::new();
            for r in recs {
                actual_per_agg
                    .entry(r.aggregate_marker)
                    .or_default()
                    .push(r.event_id);
            }
            assert_eq!(
                actual_per_agg, expected_per_agg,
                "every aggregate's 4 events must be published in strict creation order under 4 concurrent workers"
            );

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_d9_outbox_lease_budget_and_publish_timeout_prevent_overlap() {
            use tokio::sync::Notify;

            let Some(db) = OutboxTestDb::setup(
                "test_pg_d9_outbox_lease_budget_and_publish_timeout_prevent_overlap",
                6,
            )
            .await
            else {
                return;
            };

            // Part A: Second-scale lease (5s, timeout 1s -> 4s budget) + slow publisher (400ms)
            // + 2 workers on the same aggregate (D9-bis point 7, P2-6).
            // 12 events x 400ms = 4.8s > 4s budget (budget check runs BEFORE each publish, so
            // Worker 1 stops after <= 10 events leaving >= 2 for Worker 2, while tolerating slow
            // claim latency on busy hosts). Any redelivery after lease expiry is allowed as long
            // as it carries the exact same `Nats-Msg-Id`.
            let t0 = Utc::now() - chrono::Duration::seconds(10);
            let mut expected_ids = Vec::new();
            for i in 0..12 {
                let id = insert_event(
                    &db.pool,
                    "payment.succeeded",
                    "agg-slow",
                    t0 + chrono::Duration::milliseconds(i * 10),
                    serde_json::json!({"agg": "agg-slow", "step": i}),
                )
                .await;
                expected_ids.push(id);
            }

            #[derive(Debug, Clone)]
            struct TimedPublish {
                worker: &'static str,
                event_id: Uuid,
                nats_msg_id: String,
                started_at: Instant,
            }

            struct SlowWorkerPublisher {
                worker: &'static str,
                delay: Duration,
                wake_other: Option<Arc<Notify>>,
                log: Arc<Mutex<Vec<TimedPublish>>>,
            }

            #[tonic::async_trait]
            impl OutboxPublisher for SlowWorkerPublisher {
                async fn publish_outbox_message(
                    &self,
                    _subject: String,
                    headers: NatsHeaders,
                    payload: Vec<u8>,
                ) -> Result<(), OutboxPublishFailure> {
                    if let Some(ref notify) = self.wake_other {
                        notify.notify_one();
                    }
                    let env: EventEnvelope = serde_json::from_slice(&payload).unwrap();
                    let event_id = Uuid::parse_str(&env.event_id).unwrap();
                    let nats_msg_id = headers
                        .get("Nats-Msg-Id")
                        .map(|v| v.as_str().to_string())
                        .unwrap_or_default();
                    let started_at = Instant::now();
                    tokio::time::sleep(self.delay).await;
                    self.log.lock().unwrap().push(TimedPublish {
                        worker: self.worker,
                        event_id,
                        nats_msg_id,
                        started_at,
                    });
                    Ok(())
                }
            }

            let log = Arc::new(Mutex::new(Vec::new()));
            let wake_w2 = Arc::new(Notify::new());
            let w1_lease = Duration::from_secs(5);
            let w1_timeout = Duration::from_secs(1);

            let pub_w1 = SlowWorkerPublisher {
                worker: "w1",
                delay: Duration::from_millis(400),
                wake_other: Some(wake_w2.clone()),
                log: log.clone(),
            };
            let pub_w2 = SlowWorkerPublisher {
                worker: "w2",
                delay: Duration::from_millis(20),
                wake_other: None,
                log: log.clone(),
            };

            let pool_w2 = db.pool.clone();
            let wake_w2_task = wake_w2.clone();
            let w2_handle = tokio::spawn(async move {
                wake_w2_task.notified().await;
                loop {
                    let pending: i64 = sqlx::query_scalar(
                        "SELECT COUNT(*)::BIGINT FROM outbox_events WHERE status = 'pending'",
                    )
                    .fetch_one(&pool_w2)
                    .await
                    .unwrap();
                    if pending == 0 {
                        break;
                    }
                    relay_outbox_batch_with_publisher(
                        &pool_w2,
                        &pub_w2,
                        Duration::from_secs(30),
                        Duration::from_secs(5),
                        50,
                    )
                    .await
                    .unwrap();
                    tokio::time::sleep(Duration::from_millis(100)).await;
                }
            });

            let w1_deadline = Instant::now() + w1_lease;
            relay_outbox_batch_with_publisher(&db.pool, &pub_w1, w1_lease, w1_timeout, 50)
                .await
                .expect("worker 1 stops cleanly on lease budget exhaustion");
            w2_handle.await.expect("worker 2 finishes remainder");

            let entries = log.lock().unwrap().clone();
            // Every publish MUST carry `Nats-Msg-Id == event_id`, and deduplicating by
            // `Nats-Msg-Id` (first-seen order) MUST match `[e1..e6]` in strict seq order.
            let mut dedup_seen = HashSet::new();
            let mut unique_ordered_ids = Vec::new();
            for e in &entries {
                assert_eq!(
                    e.nats_msg_id,
                    e.event_id.to_string(),
                    "every publish (including any redelivery) must carry Nats-Msg-Id = event_id"
                );
                if dedup_seen.insert(e.nats_msg_id.clone()) {
                    unique_ordered_ids.push(e.event_id);
                }
            }
            assert_eq!(
                unique_ordered_ids, expected_ids,
                "aggregate events must be delivered in strict seq order across handoff"
            );

            let w1_entries: Vec<&TimedPublish> =
                entries.iter().filter(|e| e.worker == "w1").collect();
            let w2_entries: Vec<&TimedPublish> =
                entries.iter().filter(|e| e.worker == "w2").collect();
            assert!(
                !w1_entries.is_empty() && w1_entries.len() < expected_ids.len(),
                "worker 1 must stop mid-batch before lease budget expires (got w1={})",
                w1_entries.len()
            );
            assert!(
                !w2_entries.is_empty(),
                "worker 2 must pick up released remainder"
            );
            for e in &w1_entries {
                assert!(
                    e.started_at < w1_deadline,
                    "worker 1 must NEVER start a publish after its lease deadline (event={})",
                    e.event_id
                );
            }

            // Part B: Hung publish exceeding `publish_timeout` is cancelled by tokio::time::timeout,
            // treated as infra error (retry_count=0), releases remaining batch leases, and returns Err.
            let h1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-hang-1",
                Utc::now(),
                serde_json::json!({"agg": "agg-hang-1"}),
            )
            .await;
            let h2 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-hang-2",
                Utc::now() + chrono::Duration::milliseconds(10),
                serde_json::json!({"agg": "agg-hang-2"}),
            )
            .await;

            use std::sync::atomic::{AtomicBool, Ordering};
            struct HangingPublisher {
                completed_without_cancel: Arc<AtomicBool>,
            }
            #[tonic::async_trait]
            impl OutboxPublisher for HangingPublisher {
                async fn publish_outbox_message(
                    &self,
                    _subject: String,
                    _headers: NatsHeaders,
                    _payload: Vec<u8>,
                ) -> Result<(), OutboxPublishFailure> {
                    tokio::time::sleep(Duration::from_secs(5)).await;
                    self.completed_without_cancel.store(true, Ordering::SeqCst);
                    Ok(())
                }
            }

            let completed_without_cancel = Arc::new(AtomicBool::new(false));
            let t_start = Instant::now();
            let err = relay_outbox_batch_with_publisher(
                &db.pool,
                &HangingPublisher {
                    completed_without_cancel: completed_without_cancel.clone(),
                },
                Duration::from_secs(5),
                Duration::from_millis(400),
                50,
            )
            .await
            .expect_err("hung publish must time out as transient infra error");
            assert!(
                !completed_without_cancel.load(Ordering::SeqCst),
                "tokio::time::timeout must cancel the hung publish future before its 5s sleep finishes"
            );
            assert!(
                t_start.elapsed() < Duration::from_secs(4),
                "timeout must fire well before the 5s hang (elapsed={:?})",
                t_start.elapsed()
            );
            assert!(
                matches!(&err, RelayBatchError::Nats(msg) if msg.contains("timed out")),
                "expected RelayBatchError::Nats timeout error, got: {err:?}"
            );

            let h1_row: (String, i32, Option<String>, Option<DateTime<Utc>>, Option<Uuid>) =
                sqlx::query_as(
                    "SELECT status, retry_count, last_error, claimed_until, claim_token FROM outbox_events WHERE id = $1",
                )
                .bind(h1)
                .fetch_one(&db.pool)
                .await
                .unwrap();
            assert_eq!(h1_row.0, "pending");
            assert_eq!(
                h1_row.1, 0,
                "publish timeout must NOT increment retry_count"
            );
            assert!(h1_row.2.unwrap_or_default().contains("timed out"));
            assert!(h1_row.3.is_none());
            assert!(h1_row.4.is_none());

            let h2_row: (String, i32, Option<DateTime<Utc>>, Option<Uuid>) = sqlx::query_as(
                "SELECT status, retry_count, claimed_until, claim_token FROM outbox_events WHERE id = $1",
            )
            .bind(h2)
            .fetch_one(&db.pool)
            .await
            .unwrap();
            assert_eq!(h2_row.0, "pending");
            assert_eq!(h2_row.1, 0);
            assert!(
                h2_row.2.is_none() && h2_row.3.is_none(),
                "remaining row lease must be released on publish timeout"
            );

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_d9_outbox_lost_lease_on_mark_sent_stops_batch_and_releases_rest() {
            let Some(db) = OutboxTestDb::setup(
                "test_pg_d9_outbox_lost_lease_on_mark_sent_stops_batch_and_releases_rest",
                4,
            )
            .await
            else {
                return;
            };

            let t0 = Utc::now() - chrono::Duration::seconds(5);
            let e1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-lost-1",
                t0,
                serde_json::json!({"agg": "agg-lost-1"}),
            )
            .await;
            let e2 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-lost-2",
                t0 + chrono::Duration::milliseconds(10),
                serde_json::json!({"agg": "agg-lost-2"}),
            )
            .await;
            let e3 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-lost-3",
                t0 + chrono::Duration::milliseconds(20),
                serde_json::json!({"agg": "agg-lost-3"}),
            )
            .await;

            let other_worker_token = Uuid::new_v4();
            let published_ids = Arc::new(Mutex::new(Vec::new()));

            struct LeaseStealingBatchPublisher {
                pool: PgPool,
                e2: Uuid,
                other_token: Uuid,
                published: Arc<Mutex<Vec<Uuid>>>,
            }

            #[tonic::async_trait]
            impl OutboxPublisher for LeaseStealingBatchPublisher {
                async fn publish_outbox_message(
                    &self,
                    _subject: String,
                    _headers: NatsHeaders,
                    payload: Vec<u8>,
                ) -> Result<(), OutboxPublishFailure> {
                    let env: EventEnvelope = serde_json::from_slice(&payload).unwrap();
                    let eid = Uuid::parse_str(&env.event_id).unwrap();
                    self.published.lock().unwrap().push(eid);
                    // Simulate lease takeover mid-publish on e1:
                    // - e1 already marked sent by another worker (so mark_outbox_sent_claimed -> 0 rows)
                    // - e2 already reclaimed by another worker with `other_token`
                    // - e3 still holds Worker 1's claim_token and must be released by Worker 1
                    sqlx::query(
                        "UPDATE outbox_events SET status = 'sent', sent_at = NOW(), claim_token = NULL, claimed_until = NULL WHERE id = $1",
                    )
                    .bind(eid)
                    .execute(&self.pool)
                    .await
                    .unwrap();
                    sqlx::query(
                        "UPDATE outbox_events SET claim_token = $1, claimed_until = NOW() + INTERVAL '30 seconds' WHERE id = $2",
                    )
                    .bind(self.other_token)
                    .bind(self.e2)
                    .execute(&self.pool)
                    .await
                    .unwrap();
                    Ok(())
                }
            }

            relay_outbox_batch_with_publisher(
                &db.pool,
                &LeaseStealingBatchPublisher {
                    pool: db.pool.clone(),
                    e2,
                    other_token: other_worker_token,
                    published: published_ids.clone(),
                },
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .expect("lost lease stops batch cleanly without error");

            assert_eq!(
                *published_ids.lock().unwrap(),
                vec![e1],
                "worker must stop immediately when mark_outbox_sent_claimed affects 0 rows and MUST NOT publish e2 or e3"
            );

            let (e2_status, e2_token): (String, Option<Uuid>) =
                sqlx::query_as("SELECT status, claim_token FROM outbox_events WHERE id = $1")
                    .bind(e2)
                    .fetch_one(&db.pool)
                    .await
                    .unwrap();
            assert_eq!(e2_status, "pending");
            assert_eq!(
                e2_token,
                Some(other_worker_token),
                "token-guarded release must NOT disturb another worker's reclaimed lease on e2"
            );

            let (e3_status, e3_token, e3_until): (String, Option<Uuid>, Option<DateTime<Utc>>) =
                sqlx::query_as(
                    "SELECT status, claim_token, claimed_until FROM outbox_events WHERE id = $1",
                )
                .bind(e3)
                .fetch_one(&db.pool)
                .await
                .unwrap();
            assert_eq!(e3_status, "pending");
            assert!(
                e3_token.is_none() && e3_until.is_none(),
                "worker must release remaining still-owned rows (e3) when stopping after lease loss"
            );

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_d9bis_outbox_orders_strictly_by_seq_even_when_created_at_inverted() {
            let Some(db) = OutboxTestDb::setup(
                "test_pg_d9bis_outbox_orders_strictly_by_seq_even_when_created_at_inverted",
                4,
            )
            .await
            else {
                return;
            };

            // Part A (D9-bis point 1): Two events for the SAME aggregate where the earlier `seq`
            // row has a LATER `created_at` (simulating two transactions whose NOW() timestamps
            // invert relative to lock/insert order). Must claim & publish strictly by `seq`!
            let t_earlier = Utc::now() - chrono::Duration::seconds(30);
            let t_later = t_earlier + chrono::Duration::seconds(10);

            let (inv_first_id, inv_seq1): (Uuid, i64) = sqlx::query_as(
                r#"
                INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload, created_at)
                VALUES ('payment.succeeded', 'agg-inverted-ts', '0123456789abcdef0123456789abcdef', '{"step":1}'::jsonb, $1)
                RETURNING id, seq
                "#,
            )
            .bind(t_later)
            .fetch_one(&db.pool)
            .await
            .unwrap();
            let (inv_second_id, inv_seq2): (Uuid, i64) = sqlx::query_as(
                r#"
                INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload, created_at)
                VALUES ('payment.refunded', 'agg-inverted-ts', '0123456789abcdef0123456789abcdef', '{"step":2}'::jsonb, $1)
                RETURNING id, seq
                "#,
            )
            .bind(t_earlier)
            .fetch_one(&db.pool)
            .await
            .unwrap();
            assert!(inv_seq1 < inv_seq2);
            assert!(t_later > t_earlier);

            // Cross-worker head guard must claim inv_first_id (smaller seq, even though larger
            // created_at) and block inv_second_id from worker 2.
            let (tok_inv, w1_inv) = claim_outbox_batch(&db.pool, OUTBOX_LEASE_DURATION, 1)
                .await
                .unwrap();
            assert_eq!(w1_inv.len(), 1);
            assert_eq!(
                w1_inv[0].id, inv_first_id,
                "must claim smaller seq first even when its created_at is larger"
            );
            let (_, w2_inv) = claim_outbox_batch(&db.pool, OUTBOX_LEASE_DURATION, 50)
                .await
                .unwrap();
            assert!(
                w2_inv.is_empty(),
                "worker 2 must not claim higher-seq sibling while lower-seq head is leased"
            );
            release_outbox_leases(&db.pool, &[inv_first_id], tok_inv)
                .await
                .unwrap();

            let pub_inv = RecordingPublisher::new(db.pool.clone(), true);
            relay_outbox_batch_with_publisher(
                &db.pool,
                &pub_inv,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .unwrap();
            assert_eq!(
                pub_inv
                    .records()
                    .iter()
                    .map(|r| r.event_id)
                    .collect::<Vec<_>>(),
                vec![inv_first_id, inv_second_id],
                "must publish strictly in seq order even when created_at is inverted"
            );

            // Part B: Two events for the SAME aggregate inside ONE transaction with default NOW()
            // (identical `created_at`) and reversed UUIDs (`first_id > second_id`) also order strictly by `seq`.
            let first_id = Uuid::parse_str("ffffffff-ffff-4fff-8fff-ffffffffffff").unwrap();
            let second_id = Uuid::parse_str("00000000-0000-4000-8000-000000000001").unwrap();
            assert!(first_id > second_id);

            let mut tx = db.pool.begin().await.unwrap();
            let (seq1, ts1): (i64, DateTime<Utc>) = sqlx::query_as(
                r#"
                INSERT INTO outbox_events (id, event_type, aggregate_id, trace_id, payload)
                VALUES ($1, 'payment.succeeded', 'same-tx-agg', '0123456789abcdef0123456789abcdef', '{"step":1}'::jsonb)
                RETURNING seq, created_at
                "#,
            )
            .bind(first_id)
            .fetch_one(&mut *tx)
            .await
            .unwrap();
            let (seq2, ts2): (i64, DateTime<Utc>) = sqlx::query_as(
                r#"
                INSERT INTO outbox_events (id, event_type, aggregate_id, trace_id, payload)
                VALUES ($1, 'payment.succeeded', 'same-tx-agg', '0123456789abcdef0123456789abcdef', '{"step":2}'::jsonb)
                RETURNING seq, created_at
                "#,
            )
            .bind(second_id)
            .fetch_one(&mut *tx)
            .await
            .unwrap();
            tx.commit().await.unwrap();

            assert_eq!(ts1, ts2);
            assert!(seq1 < seq2);

            let pub_same = RecordingPublisher::new(db.pool.clone(), true);
            relay_outbox_batch_with_publisher(
                &db.pool,
                &pub_same,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .unwrap();
            assert_eq!(
                pub_same
                    .records()
                    .iter()
                    .map(|r| r.event_id)
                    .collect::<Vec<_>>(),
                vec![first_id, second_id],
                "same-timestamp outbox events must publish strictly in seq order"
            );

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_d9bis_migration_0008_backfills_pending_seq_despite_ctid_inversion() {
            let Some(db) = OutboxTestDb::setup_with_migrations(
                "test_pg_d9bis_migration_0008_backfills_pending_seq_despite_ctid_inversion",
                4,
                false,
            )
            .await
            else {
                return;
            };
            let pool = &db.pool;

            // Apply migrations 0001..0007 ONLY (pre-0008 schema without `seq`).
            let pre_migrations = [
                include_str!("../migrations/0001_init.sql"),
                include_str!("../migrations/0002_payment_money_model.sql"),
                include_str!("../migrations/0003_refund_settlement_and_reconciliation.sql"),
                include_str!("../migrations/0004_reconciliation_queue_unique_tx.sql"),
                include_str!("../migrations/0005_reconciliation_fixes.sql"),
                include_str!("../migrations/0006_trim_provider_tx_ids.sql"),
                include_str!("../migrations/0007_payment_fee_configs.sql"),
            ];
            for sql in pre_migrations {
                pool.execute(sql).await.expect("apply pre-0008 migration");
            }

            // Insert E1 (older) then E2 (newer) for the same aggregate, plus a sent row.
            let t0 = Utc::now() - chrono::Duration::seconds(60);
            let e1: Uuid = sqlx::query_scalar(
                r#"
                INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload, created_at)
                VALUES ('payment.succeeded', 'agg-mig', '0123456789abcdef0123456789abcdef', '{"agg":"agg-mig","step":1}'::jsonb, $1)
                RETURNING id
                "#,
            )
            .bind(t0)
            .fetch_one(pool)
            .await
            .unwrap();
            let e2: Uuid = sqlx::query_scalar(
                r#"
                INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload, created_at)
                VALUES ('payment.refunded', 'agg-mig', '0123456789abcdef0123456789abcdef', '{"agg":"agg-mig","step":2}'::jsonb, $1)
                RETURNING id
                "#,
            )
            .bind(t0 + chrono::Duration::seconds(10))
            .fetch_one(pool)
            .await
            .unwrap();
            let e_sent: Uuid = sqlx::query_scalar(
                r#"
                INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload, status, created_at, sent_at)
                VALUES ('payment.succeeded', 'agg-mig', '0123456789abcdef0123456789abcdef', '{"agg":"agg-mig","step":0}'::jsonb, 'sent', $1, NOW())
                RETURNING id
                "#,
            )
            .bind(t0 - chrono::Duration::seconds(10))
            .fetch_one(pool)
            .await
            .unwrap();

            // UPDATE E1 before migration so PostgreSQL MVCC places E1's live tuple at a higher
            // physical `ctid` AFTER E2.
            sqlx::query("UPDATE outbox_events SET retry_count = 1 WHERE id = $1")
                .bind(e1)
                .execute(pool)
                .await
                .unwrap();

            let ctid_inverted: bool = sqlx::query_scalar(
                "SELECT (SELECT ctid FROM outbox_events WHERE id = $1) > (SELECT ctid FROM outbox_events WHERE id = $2)",
            )
            .bind(e1)
            .bind(e2)
            .fetch_one(pool)
            .await
            .unwrap();
            assert!(
                ctid_inverted,
                "E1 physical ctid must be AFTER E2 prior to running migration 0008"
            );

            // Apply migration 0008: must backfill `seq` for pending rows strictly by `(created_at, id)`
            // via nextval on the real sequence despite E1's inverted ctid.
            let mig_0008 = include_str!("../migrations/0008_outbox_lease_and_fee_config_index.sql");
            pool.execute(mig_0008).await.expect("apply migration 0008");

            let seq1: i64 = sqlx::query_scalar("SELECT seq FROM outbox_events WHERE id = $1")
                .bind(e1)
                .fetch_one(pool)
                .await
                .unwrap();
            let seq2: i64 = sqlx::query_scalar("SELECT seq FROM outbox_events WHERE id = $1")
                .bind(e2)
                .fetch_one(pool)
                .await
                .unwrap();
            assert!(
                seq1 < seq2,
                "migration 0008 must backfill E1.seq < E2.seq despite E1.ctid > E2.ctid (got seq1={seq1}, seq2={seq2})"
            );

            // Insert a post-migration event E3: sequence must have advanced past seq2 so seq3 > seq2.
            let (e3, seq3): (Uuid, i64) = sqlx::query_as(
                r#"
                INSERT INTO outbox_events (event_type, aggregate_id, trace_id, payload, created_at)
                VALUES ('payment.refund_settled', 'agg-mig', '0123456789abcdef0123456789abcdef', '{"agg":"agg-mig","step":3}'::jsonb, $1)
                RETURNING id, seq
                "#,
            )
            .bind(t0 + chrono::Duration::seconds(20))
            .fetch_one(pool)
            .await
            .unwrap();
            assert!(
                seq2 < seq3,
                "post-migration INSERT must receive seq > backfilled max (got seq2={seq2}, seq3={seq3})"
            );

            // Relay must deliver strictly [E1, E2, E3] and leave E_sent untouched.
            let publisher = RecordingPublisher::new(pool.clone(), true);
            relay_outbox_batch_with_publisher(
                pool,
                &publisher,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .unwrap();
            assert_eq!(
                publisher
                    .records()
                    .iter()
                    .map(|r| r.event_id)
                    .collect::<Vec<_>>(),
                vec![e1, e2, e3],
                "migrated pending events must relay in original (created_at, id) order [E1, E2, E3]"
            );
            let sent_status: String =
                sqlx::query_scalar("SELECT status FROM outbox_events WHERE id = $1")
                    .bind(e_sent)
                    .fetch_one(pool)
                    .await
                    .unwrap();
            assert_eq!(sent_status, "sent");

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_p2_5_outbox_layer2_eligible_blocks_sibling_when_head_locked_uncommitted() {
            let Some(db) = OutboxTestDb::setup(
                "test_pg_p2_5_outbox_layer2_eligible_blocks_sibling_when_head_locked_uncommitted",
                4,
            )
            .await
            else {
                return;
            };

            let t0 = Utc::now() - chrono::Duration::seconds(10);
            let a1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-layer2",
                t0,
                serde_json::json!({"agg": "agg-layer2", "step": 1}),
            )
            .await;
            let a2 = insert_event(
                &db.pool,
                "payment.refunded",
                "agg-layer2",
                t0 + chrono::Duration::milliseconds(10),
                serde_json::json!({"agg": "agg-layer2", "step": 2}),
            )
            .await;
            let b1 = insert_event(
                &db.pool,
                "payment.succeeded",
                "agg-layer2-other",
                t0 + chrono::Duration::milliseconds(20),
                serde_json::json!({"agg": "agg-layer2-other", "step": 1}),
            )
            .await;

            // Hold an uncommitted FOR UPDATE lock on a1 while `a1.claimed_until IS NULL`.
            // Layer 1 (`prev.claimed_until > NOW()`) does NOT block a2 here; ONLY Layer 2
            // (`eligible` CTE checking earlier pending rows omitted from `locked`) prevents
            // `claim_outbox_batch` from claiming a2 while a1 was skipped by `SKIP LOCKED`!
            let mut tx_hold = db.pool.begin().await.unwrap();
            let _: Uuid =
                sqlx::query_scalar("SELECT id FROM outbox_events WHERE id = $1 FOR UPDATE")
                    .bind(a1)
                    .fetch_one(&mut *tx_hold)
                    .await
                    .unwrap();

            let (tok1, claimed1) = claim_outbox_batch(&db.pool, OUTBOX_LEASE_DURATION, 50)
                .await
                .unwrap();
            assert_eq!(
                claimed1.iter().map(|r| r.id).collect::<Vec<_>>(),
                vec![b1],
                "Layer 2 `eligible` CTE must skip a2 while a1 is row-locked (claimed_until IS NULL) and still claim independent b1"
            );

            assert_eq!(
                mark_outbox_sent_claimed(&db.pool, b1, tok1).await.unwrap(),
                1
            );
            tx_hold.rollback().await.unwrap();

            // Once tx_hold releases a1, both a1 and a2 claim in strict seq order.
            let (_tok2, claimed2) = claim_outbox_batch(&db.pool, OUTBOX_LEASE_DURATION, 50)
                .await
                .unwrap();
            assert_eq!(
                claimed2.iter().map(|r| r.id).collect::<Vec<_>>(),
                vec![a1, a2]
            );

            db.teardown().await;
        }

        #[tokio::test]
        async fn test_pg_p3_9_relay_classifies_db_error_separately_from_nats_error() {
            let Some(db) = OutboxTestDb::setup_with_migrations(
                "test_pg_p3_9_relay_classifies_db_error_separately_from_nats_error",
                2,
                false, // empty schema without outbox_events -> deterministic SQL DB error
            )
            .await
            else {
                return;
            };

            let publisher = RecordingPublisher::new(db.pool.clone(), false);
            let err = relay_outbox_batch_with_publisher(
                &db.pool,
                &publisher,
                OUTBOX_LEASE_DURATION,
                OUTBOX_PUBLISH_TIMEOUT,
                50,
            )
            .await
            .expect_err("missing table must surface as RelayBatchError::Db");
            assert!(
                matches!(err, RelayBatchError::Db(_)),
                "DB failure must return RelayBatchError::Db (preserving NATS connection), got: {err:?}"
            );

            db.teardown().await;
        }
    }
}
