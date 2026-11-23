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

use crate::auth::{require_payment_read_access, Caller, GrpcAuth};
use crate::domain::vn_today;
use crate::error::PaymentError;
use crate::metrics::red_metrics_middleware;
use crate::service::PaymentService;
use crate::telemetry::{extract_or_generate_trace_id, mask_secret};
use crate::webhook_security::{verify_webhook_request, IncomingWebhookPayload};

#[derive(Clone)]
pub struct HttpState {
    pub service: PaymentService,
    pub auth: GrpcAuth,
}

pub fn build_router(service: PaymentService, auth: GrpcAuth) -> Router {
    let metrics = service.metrics().clone();
    let state = HttpState { service, auth };

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
    headers: HeaderMap,
    Path(id): Path<String>,
) -> Result<Sse<impl Stream<Item = Result<Event, Infallible>>>, PaymentError> {
    let auth_header = headers
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok());
    let caller = state
        .auth
        .authenticate_header(auth_header, "PaymentEventsSse")?;

    let parsed_uuid = Uuid::parse_str(&id).ok();
    let pwa = if let Some(pid) = parsed_uuid {
        state.service.get_payment(Some(pid), None, None).await?
    } else {
        state
            .service
            .get_payment(None, Some(&id), Some(&id))
            .await?
    };

    require_payment_read_access(&caller, &pwa)?;

    let p = pwa.payment;
    let payment_id = p.id;
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
    let initial_event = Event::default().event(event_name).data(data.to_string());

    let rx = state.service.subscribe_sse();
    let filter_id = id.clone();

    let live_stream = BroadcastStream::new(rx).filter_map(move |msg| {
        let filter_id = filter_id.clone();
        async move {
            match msg {
                Ok(evt) => {
                    let matches_payment = evt.payment_id == payment_id;
                    let matches_order =
                        evt.order_id == filter_id || evt.order_group_id == filter_id;
                    if matches_payment || matches_order {
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

    info!(
        payment_ref = %id,
        user_id = %caller.user_id,
        "client subscribed to payment status SSE"
    );

    let combined =
        stream::once(async move { Ok::<Event, Infallible>(initial_event) }).chain(live_stream);

    Ok(Sse::new(combined).keep_alive(
        KeepAlive::new()
            .interval(Duration::from_secs(15))
            .text("keep-alive"),
    ))
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

fn require_admin(
    state: &HttpState,
    headers: &HeaderMap,
    method: &str,
) -> Result<Caller, PaymentError> {
    let auth_header = headers
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok());
    let caller = state.auth.authenticate_header(auth_header, method)?;
    if !caller.is_admin() {
        return Err(PaymentError::Forbidden(format!(
            "{method} requires admin role"
        )));
    }
    Ok(caller)
}

async fn internal_ledger_summary(
    State(state): State<HttpState>,
    headers: HeaderMap,
) -> Result<impl IntoResponse, PaymentError> {
    require_admin(&state, &headers, "InternalLedgerSummary")?;
    let summary = state.service.get_ledger_summary().await?;
    Ok((StatusCode::OK, Json(summary)))
}

async fn internal_reconciliation_queue(
    State(state): State<HttpState>,
    headers: HeaderMap,
) -> Result<impl IntoResponse, PaymentError> {
    require_admin(&state, &headers, "InternalReconciliationQueue")?;
    let res = state
        .service
        .list_reconciliation_queue(None, 1, 100)
        .await?;
    Ok((
        StatusCode::OK,
        Json(json!({ "items": res.items, "total": res.total })),
    ))
}

async fn internal_reconciliation_run(
    State(state): State<HttpState>,
    headers: HeaderMap,
) -> Result<impl IntoResponse, PaymentError> {
    let caller = require_admin(&state, &headers, "InternalReconciliationRun")?;
    info!(
        user_id = %caller.user_id,
        "admin triggered payment reconciliation"
    );
    let today = vn_today(Utc::now());
    let report = state.service.run_reconciliation(today).await?;
    Ok((StatusCode::OK, Json(report)))
}

fn header_str<'a>(headers: &'a HeaderMap, key: &str) -> Option<&'a str> {
    headers.get(key).and_then(|v| v.to_str().ok())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::sign_hs256;
    use crate::config::{Config, VietQrMode};
    use crate::metrics::Metrics;
    use crate::webhook_security::compute_hmac_hex;
    use axum::body::Body;
    use axum::http::{Method, Request};
    use http_body_util::BodyExt;
    use sqlx::postgres::PgPoolOptions;
    use tower::ServiceExt;

    const TEST_USER_JWT_SECRET: &str = "http-unit-test-user-jwt-secret";
    const TEST_SERVICE_JWT_SECRET: &str = "http-unit-test-service-jwt-secret";

    fn test_router() -> (Router, Config) {
        let mut cfg = Config::from_env();
        cfg.webhook_secret = "unit-test-secret".to_string();
        cfg.vietqr_mode = VietQrMode::Mock;
        cfg.database_url = "postgres://unit:unit@127.0.0.1:1/unit_test_db".to_string();
        let pool = PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(200))
            .connect_lazy(&cfg.database_url)
            .expect("lazy pool");
        let svc = PaymentService::new(pool, cfg.clone(), Metrics::new());
        let auth =
            GrpcAuth::new(TEST_USER_JWT_SECRET, TEST_SERVICE_JWT_SECRET).expect("test GrpcAuth");
        (build_router(svc, auth), cfg)
    }

    #[tokio::test]
    async fn test_internal_routes_require_admin_jwt() {
        let (app, _) = test_router();
        let now = Utc::now().timestamp();

        let buyer_tok = sign_hs256(
            TEST_USER_JWT_SECRET,
            json!({"sub": "buyer-1", "role": "buyer", "exp": now + 60}),
        );
        let seller_tok = sign_hs256(
            TEST_USER_JWT_SECRET,
            json!({"sub": "seller-1", "role": "seller", "shop_id": "shop-1", "exp": now + 60}),
        );
        let expired_admin_tok = sign_hs256(
            TEST_USER_JWT_SECRET,
            json!({"sub": "admin-1", "role": "admin", "exp": now - 60}),
        );
        let svc_tok = sign_hs256(
            TEST_SERVICE_JWT_SECRET,
            json!({"sub": "order-service", "role": "admin", "service": "order-service", "exp": now + 60}),
        );
        let admin_tok = sign_hs256(
            TEST_USER_JWT_SECRET,
            json!({"sub": "admin-1", "role": "admin", "exp": now + 60}),
        );
        let super_admin_tok = sign_hs256(
            TEST_USER_JWT_SECRET,
            json!({"sub": "sadmin-1", "role": "super_admin", "exp": now + 60}),
        );

        let routes = [
            (Method::GET, "/internal/ledger/summary"),
            (Method::GET, "/internal/reconciliation/queue"),
            (Method::POST, "/internal/reconciliation/run"),
        ];

        for (method, path) in routes {
            // Missing Authorization header (even with spoofed x-user-role) -> 401
            let resp_no_tok = app
                .clone()
                .oneshot(
                    Request::builder()
                        .method(method.clone())
                        .uri(path)
                        .header("x-user-role", "admin")
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(resp_no_tok.status(), StatusCode::UNAUTHORIZED, "{path}");

            // Expired admin JWT -> 401
            let resp_expired = app
                .clone()
                .oneshot(
                    Request::builder()
                        .method(method.clone())
                        .uri(path)
                        .header(header::AUTHORIZATION, format!("Bearer {expired_admin_tok}"))
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(resp_expired.status(), StatusCode::UNAUTHORIZED, "{path}");

            // Valid buyer / seller JWT -> 403
            for (role, tok) in [("buyer", &buyer_tok), ("seller", &seller_tok)] {
                let resp = app
                    .clone()
                    .oneshot(
                        Request::builder()
                            .method(method.clone())
                            .uri(path)
                            .header(header::AUTHORIZATION, format!("Bearer {tok}"))
                            .body(Body::empty())
                            .unwrap(),
                    )
                    .await
                    .unwrap();
                assert_eq!(resp.status(), StatusCode::FORBIDDEN, "{path} role={role}");
            }

            // Valid service token -> 403
            let resp_svc = app
                .clone()
                .oneshot(
                    Request::builder()
                        .method(method.clone())
                        .uri(path)
                        .header(header::AUTHORIZATION, format!("Bearer {svc_tok}"))
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(resp_svc.status(), StatusCode::FORBIDDEN, "{path} service");

            // Valid admin & super_admin JWT -> passes auth (hits lazy DB -> not 401/403)
            for (role, tok) in [("admin", &admin_tok), ("super_admin", &super_admin_tok)] {
                let resp = app
                    .clone()
                    .oneshot(
                        Request::builder()
                            .method(method.clone())
                            .uri(path)
                            .header(header::AUTHORIZATION, format!("Bearer {tok}"))
                            .body(Body::empty())
                            .unwrap(),
                    )
                    .await
                    .unwrap();
                assert_ne!(
                    resp.status(),
                    StatusCode::UNAUTHORIZED,
                    "{path} role={role}"
                );
                assert_ne!(resp.status(), StatusCode::FORBIDDEN, "{path} role={role}");
            }
        }
    }

    #[tokio::test]
    async fn test_sse_rejects_missing_invalid_expired_garbage_and_service_tokens() {
        let (app, _) = test_router();
        let uri = format!("/payments/{}/events", Uuid::new_v4());
        let now = Utc::now().timestamp();

        // Missing Authorization header (even with spoofed x-user-* headers) -> 401
        let resp_missing = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(&uri)
                    .header("x-user-id", "attacker")
                    .header("x-user-role", "admin")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp_missing.status(), StatusCode::UNAUTHORIZED);

        // Wrong signing key -> 401
        let wrong_key_tok = sign_hs256(
            "wrong-signing-key",
            json!({"sub": "buyer-1", "role": "buyer", "exp": now + 60}),
        );
        // Expired user JWT -> 401
        let expired_tok = sign_hs256(
            TEST_USER_JWT_SECRET,
            json!({"sub": "buyer-1", "role": "buyer", "exp": now - 60}),
        );
        // Garbage token -> 401
        for (case, auth_val) in [
            ("wrong key", format!("Bearer {wrong_key_tok}")),
            ("expired", format!("Bearer {expired_tok}")),
            ("garbage", "Bearer not-a-valid.jwt.token".to_string()),
            ("non-bearer", "Basic dXNlcjpwYXNz".to_string()),
        ] {
            let resp = app
                .clone()
                .oneshot(
                    Request::builder()
                        .uri(&uri)
                        .header(header::AUTHORIZATION, auth_val)
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(resp.status(), StatusCode::UNAUTHORIZED, "case={case}");
        }

        // Valid internal service token -> 403 (PaymentEventsSse is not in SERVICE_METHODS)
        let svc_tok = sign_hs256(
            TEST_SERVICE_JWT_SECRET,
            json!({
                "sub": "order-service",
                "role": "admin",
                "service": "order-service",
                "exp": now + 60,
            }),
        );
        let resp_svc = app
            .oneshot(
                Request::builder()
                    .uri(&uri)
                    .header(header::AUTHORIZATION, format!("Bearer {svc_tok}"))
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp_svc.status(), StatusCode::FORBIDDEN);
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
