use std::convert::Infallible;
use std::time::Duration;

use axum::body::Bytes;
use axum::extract::{Path, State};
use axum::http::{header, HeaderMap, StatusCode};
use axum::middleware;
use axum::response::sse::{Event, KeepAlive, Sse};
use axum::response::IntoResponse;
use axum::routing::{get, post};
use axum::{Json, Router};
use chrono::Utc;
use futures_util::stream::{self, Stream, StreamExt};
use serde_json::json;
use tokio_stream::wrappers::BroadcastStream;
use tracing::{info, warn};
use uuid::Uuid;

use crate::error::PaymentError;
use crate::metrics::red_metrics_middleware;
use crate::service::PaymentService;
use crate::telemetry::{extract_or_generate_trace_id, mask_secret};
use crate::webhook_security::{verify_webhook_request, IncomingWebhookPayload};

#[derive(Clone)]
pub struct HttpState {
    pub service: PaymentService,
}

pub fn build_router(service: PaymentService) -> Router {
    let metrics = service.metrics().clone();
    let state = HttpState { service };

    Router::new()
        .route("/healthz", get(healthz))
        .route("/readyz", get(readyz))
        .route("/metrics", get(metrics_handler))
        .route("/webhooks/payment", post(handle_payment_webhook))
        .route("/webhooks/vietqr", post(handle_vietqr_webhook))
        .route("/payments/{id}/events", get(payment_events_sse))
        .route("/internal/ledger/summary", get(internal_ledger_summary))
        .route(
            "/internal/reconciliation/queue",
            get(internal_reconciliation_queue),
        )
        .route(
            "/internal/reconciliation/run",
            post(internal_reconciliation_run),
        )
        .layer(middleware::from_fn_with_state(
            metrics,
            red_metrics_middleware,
        ))
        .with_state(state)
}

async fn healthz() -> impl IntoResponse {
    (
        StatusCode::OK,
        Json(json!({
            "status": "ok",
            "service": "payment"
        })),
    )
}

async fn readyz(State(state): State<HttpState>) -> impl IntoResponse {
    match state.service.check_ready().await {
        Ok(()) => (
            StatusCode::OK,
            Json(json!({
                "status": "ready",
                "service": "payment"
            })),
        ),
        Err(_) => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({
                "status": "not_ready",
                "service": "payment"
            })),
        ),
    }
}

async fn metrics_handler(State(state): State<HttpState>) -> impl IntoResponse {
    let body = state.service.metrics().render_prometheus();
    (
        StatusCode::OK,
        [(
            header::CONTENT_TYPE,
            "text/plain; version=0.0.4; charset=utf-8",
        )],
        body,
    )
}

async fn handle_payment_webhook(
    State(state): State<HttpState>,
    headers: HeaderMap,
    body: Bytes,
) -> Result<impl IntoResponse, PaymentError> {
    process_webhook_http(state, headers, body, "payment_gateway").await
}

async fn handle_vietqr_webhook(
    State(state): State<HttpState>,
    headers: HeaderMap,
    body: Bytes,
) -> Result<impl IntoResponse, PaymentError> {
    let default_provider = state.service.vietqr().mode().as_str();
    process_webhook_http(state, headers, body, default_provider).await
}

async fn process_webhook_http(
    state: HttpState,
    headers: HeaderMap,
    body: Bytes,
    default_provider: &str,
) -> Result<impl IntoResponse, PaymentError> {
    let trace_id = extract_or_generate_trace_id(
        header_str(&headers, "traceparent"),
        header_str(&headers, "x-trace-id"),
    );

    let signature = header_str(&headers, "x-signature");
    let timestamp = header_str(&headers, "x-timestamp");

    if let Err(err) = verify_webhook_request(
        &state.service.config().webhook_secret,
        &body,
        signature,
        timestamp,
        state.service.config().webhook_replay_window_secs,
        Utc::now(),
    ) {
        state.service.metrics().record_webhook("rejected_signature");
        warn!(
            trace_id = %trace_id,
            signature_masked = %mask_secret(signature.unwrap_or("")),
            has_timestamp = timestamp.is_some(),
            reason = %err,
            "rejected webhook due to invalid signature or replay check"
        );
        return Err(err);
    }

    let raw_json: serde_json::Value = serde_json::from_slice(&body)
        .map_err(|e| PaymentError::Validation(format!("invalid JSON webhook body: {e}")))?;

    let payload: IncomingWebhookPayload = serde_json::from_value(raw_json.clone())
        .map_err(|e| PaymentError::Validation(format!("invalid webhook payload schema: {e}")))?;

    let result = state
        .service
        .process_verified_webhook(payload, raw_json, default_provider, &trace_id)
        .await?;

    Ok((StatusCode::OK, Json(result)))
}

/// Server-Sent Events stream for checkout payment status updates (UC-PAY01).
async fn payment_events_sse(
    State(state): State<HttpState>,
    Path(id): Path<String>,
) -> Sse<impl Stream<Item = Result<Event, Infallible>>> {
    let parsed_uuid = Uuid::parse_str(&id).ok();

    let initial_payment = if let Some(pid) = parsed_uuid {
        state.service.get_payment(Some(pid), None, None).await.ok()
    } else {
        state
            .service
            .get_payment(None, Some(&id), Some(&id))
            .await
            .ok()
    };

    let initial_event = match initial_payment {
        Some(pwa) => {
            let p = pwa.payment;
            let event_name = status_to_sse_event_name(&p.status);
            let data = json!({
                "payment_id": p.id,
                "order_group_id": p.order_group_id,
                "order_id": p.order_id,
                "status": p.status,
                "amount_vnd": p.amount_vnd,
                "provider_tx_id": p.provider_tx_id,
                "updated_at": p.updated_at,
            });
            Event::default().event(event_name).data(data.to_string())
        }
        None => Event::default()
            .event("payment.status")
            .data(json!({ "id": id, "status": "unknown" }).to_string()),
    };

    let rx = state.service.subscribe_sse();
    let filter_id = id.clone();

    let live_stream = BroadcastStream::new(rx).filter_map(move |msg| {
        let filter_id = filter_id.clone();
        async move {
            match msg {
                Ok(evt) => {
                    let matches_uuid = parsed_uuid.is_some_and(|u| u == evt.payment_id);
                    let matches_order =
                        evt.order_id == filter_id || evt.order_group_id == filter_id;
                    if matches_uuid || matches_order {
                        let event_name = status_to_sse_event_name(&evt.status);
                        let data = serde_json::to_string(&evt).unwrap_or_default();
                        Some(Ok::<Event, Infallible>(
                            Event::default().event(event_name).data(data),
                        ))
                    } else {
                        None
                    }
                }
                Err(_) => None,
            }
        }
    });

    info!(payment_ref = %id, "client subscribed to payment status SSE");

    let combined =
        stream::once(async move { Ok::<Event, Infallible>(initial_event) }).chain(live_stream);

    Sse::new(combined).keep_alive(
        KeepAlive::new()
            .interval(Duration::from_secs(15))
            .text("keep-alive"),
    )
}

fn status_to_sse_event_name(status: &str) -> &'static str {
    match status {
        "succeeded" => "payment.succeeded",
        "expired" => "payment.expired",
        "failed" | "cancelled" | "superseded" => "payment.failed",
        "refunded" => "payment.refunded",
        _ => "payment.pending",
    }
}

async fn internal_ledger_summary(
    State(state): State<HttpState>,
) -> Result<impl IntoResponse, PaymentError> {
    let summary = state.service.get_ledger_summary().await?;
    Ok((StatusCode::OK, Json(summary)))
}

async fn internal_reconciliation_queue(
    State(state): State<HttpState>,
) -> Result<impl IntoResponse, PaymentError> {
    let items = state.service.list_reconciliation_queue().await?;
    Ok((StatusCode::OK, Json(json!({ "items": items }))))
}

async fn internal_reconciliation_run(
    State(state): State<HttpState>,
) -> Result<impl IntoResponse, PaymentError> {
    let today = Utc::now().date_naive();
    let report = state.service.run_reconciliation(today).await?;
    Ok((StatusCode::OK, Json(report)))
}

fn header_str<'a>(headers: &'a HeaderMap, key: &str) -> Option<&'a str> {
    headers.get(key).and_then(|v| v.to_str().ok())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::Config;
    use crate::metrics::Metrics;
    use crate::webhook_security::compute_hmac_hex;
    use axum::body::Body;
    use axum::http::{Method, Request};
    use http_body_util::BodyExt;
    use sqlx::postgres::PgPoolOptions;
    use tower::ServiceExt;

    fn test_router() -> (Router, Config) {
        let mut cfg = Config::from_env();
        cfg.webhook_secret = "unit-test-secret".to_string();
        let pool = PgPoolOptions::new()
            .connect_lazy(&cfg.database_url)
            .expect("lazy pool");
        let svc = PaymentService::new(pool, cfg.clone(), Metrics::new());
        (build_router(svc), cfg)
    }

    #[tokio::test]
    async fn test_healthz_and_red_metrics_no_permissive_cors() {
        let (app, _) = test_router();

        let resp = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/healthz")
                    .header("origin", "https://evil.example")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        assert!(resp.headers().get("access-control-allow-origin").is_none());

        let metrics_resp = app
            .oneshot(
                Request::builder()
                    .uri("/metrics")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(metrics_resp.status(), StatusCode::OK);
        let bytes = metrics_resp.into_body().collect().await.unwrap().to_bytes();
        let text = String::from_utf8_lossy(&bytes);
        assert!(text.contains("http_requests_total"));
        assert!(text.contains("http_request_duration_seconds"));
        assert!(text.contains("route=\"/healthz\""));
    }

    #[tokio::test]
    async fn test_webhook_rejects_missing_sig_missing_timestamp_invalid_sig_and_stale_ts() {
        let (app, _cfg) = test_router();
        let body = r#"{"provider_tx_id":"TX1","order_group_id":"grp-1","amount_vnd":100000,"status":"SUCCEEDED"}"#;
        let fresh_ts = Utc::now().timestamp().to_string();
        let valid_sig = compute_hmac_hex("unit-test-secret", body.as_bytes());

        // Missing X-Signature -> 401
        let resp_missing = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/webhooks/payment")
                    .header("content-type", "application/json")
                    .header("x-timestamp", &fresh_ts)
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp_missing.status(), StatusCode::UNAUTHORIZED);

        // Valid X-Signature but missing X-Timestamp -> 401 (prevents replay bypass)
        let resp_missing_ts = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/webhooks/payment")
                    .header("content-type", "application/json")
                    .header("x-signature", &valid_sig)
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp_missing_ts.status(), StatusCode::UNAUTHORIZED);

        // Wrong X-Signature -> 401
        let wrong_sig = compute_hmac_hex("wrong-secret", body.as_bytes());
        let resp_bad = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/webhooks/vietqr")
                    .header("content-type", "application/json")
                    .header("x-signature", wrong_sig)
                    .header("x-timestamp", &fresh_ts)
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp_bad.status(), StatusCode::UNAUTHORIZED);

        // Valid signature but stale X-Timestamp (> 5 min) -> 401
        let stale_ts = (Utc::now() - chrono::Duration::seconds(600))
            .timestamp()
            .to_string();
        let resp_stale = app
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/webhooks/payment")
                    .header("content-type", "application/json")
                    .header("x-signature", valid_sig)
                    .header("x-timestamp", stale_ts)
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp_stale.status(), StatusCode::UNAUTHORIZED);
    }
}
