use std::convert::Infallible;
use std::time::{Duration, Instant};

use axum::body::Bytes;
use axum::extract::{MatchedPath, Path, Request, State};
use axum::http::{header, HeaderMap, HeaderValue, StatusCode};
use axum::middleware::{self, Next};
use axum::response::sse::{Event, KeepAlive, Sse};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Extension, Json, Router};
use chrono::Utc;
use futures_util::stream::{self, Stream, StreamExt};
use serde_json::json;
use tokio::sync::broadcast::error::RecvError;
use tracing::{debug, error, info, warn, Instrument};
use uuid::Uuid;

use crate::auth::{
    resolve_payment_read_scope, seller_scoped_amount_vnd, seller_scoped_order_id, Caller, GrpcAuth,
    PaymentViewScope,
};
use crate::domain::{vn_today, PaymentSseEvent, PaymentWithAllocations};
use crate::error::PaymentError;
use crate::metrics::red_metrics_middleware;
use crate::service::PaymentService;
use crate::telemetry::{
    attach_parent_context, extract_context_from_http, extract_or_generate_trace_id, mask_secret,
};
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
        .layer(middleware::from_fn(http_trace_middleware))
        .with_state(state)
}

#[derive(Clone, Debug)]
struct RequestTraceId(String);

async fn http_trace_middleware(mut req: Request, next: Next) -> Response {
    let start = Instant::now();
    let method = req.method().as_str().to_string();
    let route = req
        .extensions()
        .get::<MatchedPath>()
        .map(|mp| mp.as_str().to_string())
        .unwrap_or_else(|| req.uri().path().to_string());

    let parent_cx = extract_context_from_http(req.headers());
    let x_trace_id = header_str(req.headers(), "x-trace-id").map(ToString::to_string);

    let span = tracing::info_span!(
        "http.request",
        otel.name = %format!("{method} {route}"),
        otel.kind = "server",
        http.method = %method,
        http.route = %route,
        trace_id = tracing::field::Empty,
    );
    let trace_id = attach_parent_context(&span, parent_cx, x_trace_id.as_deref());
    span.record("trace_id", trace_id.as_str());
    req.extensions_mut()
        .insert(RequestTraceId(trace_id.clone()));

    let mut resp = next.run(req).instrument(span.clone()).await;

    if let Ok(val) = HeaderValue::from_str(&trace_id) {
        resp.headers_mut().insert("x-trace-id", val);
    }

    let status = resp.status();
    let duration_ms = (start.elapsed().as_secs_f64() * 1000.0 * 1000.0).round() / 1000.0;
    let is_ops_probe = matches!(route.as_str(), "/healthz" | "/readyz" | "/metrics");

    span.in_scope(|| {
        if is_ops_probe && status.is_success() {
            debug!(
                service = "payment",
                method = %method,
                route = %route,
                status = status.as_u16(),
                duration_ms = duration_ms,
                trace_id = %trace_id,
                "http request"
            );
        } else if status.is_server_error() {
            error!(
                service = "payment",
                method = %method,
                route = %route,
                status = status.as_u16(),
                duration_ms = duration_ms,
                trace_id = %trace_id,
                "http request"
            );
        } else if status.is_client_error() {
            warn!(
                service = "payment",
                method = %method,
                route = %route,
                status = status.as_u16(),
                duration_ms = duration_ms,
                trace_id = %trace_id,
                "http request"
            );
        } else {
            info!(
                service = "payment",
                method = %method,
                route = %route,
                status = status.as_u16(),
                duration_ms = duration_ms,
                trace_id = %trace_id,
                "http request"
            );
        }
    });

    resp
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
    trace_ext: Option<Extension<RequestTraceId>>,
    headers: HeaderMap,
    body: Bytes,
) -> Result<impl IntoResponse, PaymentError> {
    process_webhook_http(state, trace_ext, headers, body, "payment_gateway").await
}

async fn handle_vietqr_webhook(
    State(state): State<HttpState>,
    trace_ext: Option<Extension<RequestTraceId>>,
    headers: HeaderMap,
    body: Bytes,
) -> Result<impl IntoResponse, PaymentError> {
    let default_provider = state.service.vietqr().mode().as_str();
    process_webhook_http(state, trace_ext, headers, body, default_provider).await
}

async fn process_webhook_http(
    state: HttpState,
    trace_ext: Option<Extension<RequestTraceId>>,
    headers: HeaderMap,
    body: Bytes,
    default_provider: &str,
) -> Result<impl IntoResponse, PaymentError> {
    let trace_id = trace_ext
        .map(|Extension(RequestTraceId(id))| id)
        .unwrap_or_else(|| {
            extract_or_generate_trace_id(
                header_str(&headers, "traceparent"),
                header_str(&headers, "x-trace-id"),
            )
        });

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

    let payment_id = Uuid::parse_str(id.trim())
        .map_err(|_| PaymentError::Validation("invalid payment_id: expected UUID".to_string()))?;
    // P3-8: Subscribe BEFORE reading the DB snapshot so a status transition committed between
    // `get_payment` and stream setup cannot be missed.
    let rx = state.service.subscribe_sse();
    let pwa = state
        .service
        .get_payment(Some(payment_id), None, None)
        .await?;

    let scope = resolve_payment_read_scope(&caller, &pwa)?;
    let sse_projection = SseEventProjection::from_scope(&scope, &pwa);

    let p = pwa.payment;
    let payment_id = p.id;
    let initial_terminal = is_sse_terminal_status(&p.status);
    let initial_evt = sse_projection.project(PaymentSseEvent {
        payment_id: p.id,
        order_group_id: p.order_group_id,
        order_id: p.order_id,
        status: p.status,
        amount_vnd: p.amount_vnd,
        provider_tx_id: p.provider_tx_id,
        updated_at: p.updated_at,
    });
    let event_name = status_to_sse_event_name(&initial_evt.status);
    let initial_data = serde_json::to_string(&initial_evt).unwrap_or_default();
    let initial_event = Event::default().event(event_name).data(initial_data);

    let jwt_deadline = caller.exp.map(|exp| {
        let remaining_secs = (exp - Utc::now().timestamp()).max(0) as u64;
        tokio::time::Instant::now() + Duration::from_secs(remaining_secs)
    });

    let live_stream = stream::unfold((rx, initial_terminal), move |(mut rx, finished)| {
        let sse_projection = sse_projection.clone();
        async move {
            if finished {
                return None;
            }
            loop {
                let recv_res = if let Some(deadline) = jwt_deadline {
                    tokio::select! {
                        _ = tokio::time::sleep_until(deadline) => return None,
                        res = rx.recv() => res,
                    }
                } else {
                    rx.recv().await
                };
                match recv_res {
                    Ok(evt) if evt.payment_id == payment_id => {
                        let done_after = is_sse_terminal_status(&evt.status);
                        let projected = sse_projection.project(evt);
                        let event_name = status_to_sse_event_name(&projected.status);
                        let data = serde_json::to_string(&projected).unwrap_or_default();
                        let frame =
                            Ok::<Event, Infallible>(Event::default().event(event_name).data(data));
                        return Some((frame, (rx, done_after)));
                    }
                    Ok(_) => continue,
                    // Close cleanly on channel close or lag so clients reconnect and refresh snapshot.
                    Err(RecvError::Closed) | Err(RecvError::Lagged(_)) => return None,
                }
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

#[derive(Clone, Debug)]
struct SseEventProjection {
    seller_scope: Option<(i64, String)>,
}

impl SseEventProjection {
    fn from_scope(scope: &PaymentViewScope, pwa: &PaymentWithAllocations) -> Self {
        match scope {
            PaymentViewScope::Full | PaymentViewScope::BuyerOwner => Self { seller_scope: None },
            PaymentViewScope::SellerAllocation { shop_id } => Self {
                seller_scope: Some((
                    seller_scoped_amount_vnd(pwa, shop_id),
                    seller_scoped_order_id(pwa, shop_id),
                )),
            },
        }
    }

    fn project(&self, mut evt: PaymentSseEvent) -> PaymentSseEvent {
        if let Some((shop_amount_vnd, scoped_order_id)) = &self.seller_scope {
            evt.amount_vnd = *shop_amount_vnd;
            evt.order_id = scoped_order_id.clone();
            evt.provider_tx_id = None;
        }
        evt
    }
}

#[cfg(test)]
fn project_sse_event_for_caller(
    evt: PaymentSseEvent,
    caller: &Caller,
    pwa: &PaymentWithAllocations,
) -> PaymentSseEvent {
    let scope = resolve_payment_read_scope(caller, pwa).expect("caller must have read access");
    SseEventProjection::from_scope(&scope, pwa).project(evt)
}

fn is_sse_terminal_status(status: &str) -> bool {
    matches!(
        status,
        "succeeded" | "expired" | "failed" | "cancelled" | "superseded" | "refunded"
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
        crate::telemetry::init_test_tracing();
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

    #[tokio::test]
    async fn test_http_trace_middleware_echoes_x_trace_id_from_traceparent() {
        let (app, _) = test_router();
        const TRACE_ID: &str = "aa11bb22cc33dd44ee55ff6677889900";
        let tp = format!("00-{TRACE_ID}-1122334455667788-01");

        let resp = app
            .oneshot(
                Request::builder()
                    .uri("/healthz")
                    .header("traceparent", tp)
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        assert_eq!(
            resp.headers()
                .get("x-trace-id")
                .and_then(|v| v.to_str().ok()),
            Some(TRACE_ID)
        );
    }

    #[tokio::test]
    async fn test_w2b_r3_webhook_without_traceparent_reuses_middleware_trace_id() {
        crate::telemetry::init_test_tracing();

        use std::io;
        use std::sync::{Arc, Mutex};
        use tracing::instrument::WithSubscriber;
        use tracing_subscriber::layer::SubscriberExt;

        #[derive(Clone, Default)]
        struct BufWriter(Arc<Mutex<Vec<u8>>>);

        struct BufGuard(Arc<Mutex<Vec<u8>>>);
        impl io::Write for BufGuard {
            fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
                self.0.lock().unwrap().extend_from_slice(buf);
                Ok(buf.len())
            }
            fn flush(&mut self) -> io::Result<()> {
                Ok(())
            }
        }
        impl<'a> tracing_subscriber::fmt::MakeWriter<'a> for BufWriter {
            type Writer = BufGuard;
            fn make_writer(&'a self) -> Self::Writer {
                BufGuard(self.0.clone())
            }
        }

        let buf = BufWriter::default();
        let captured = buf.0.clone();
        let subscriber = tracing_subscriber::registry().with(
            tracing_subscriber::fmt::layer()
                .json()
                .flatten_event(true)
                .with_writer(buf),
        );

        let (app, _) = test_router();
        // No traceparent and no x-trace-id -> middleware generates one; webhook handler MUST reuse it.
        let resp = app
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/webhooks/vietqr")
                    .header("content-type", "application/json")
                    .body(Body::from(r#"{"provider_tx_id":"TX-TRACE"}"#))
                    .unwrap(),
            )
            .with_subscriber(subscriber)
            .await
            .unwrap();

        assert_eq!(resp.status(), StatusCode::UNAUTHORIZED);
        let resp_tid = resp
            .headers()
            .get("x-trace-id")
            .and_then(|v| v.to_str().ok())
            .expect("middleware must echo x-trace-id")
            .to_string();
        assert!(!resp_tid.is_empty());

        let raw_logs = String::from_utf8(captured.lock().unwrap().clone()).unwrap();
        let webhook_tid = raw_logs
            .lines()
            .filter_map(|line| serde_json::from_str::<serde_json::Value>(line).ok())
            .find(|v| {
                v.get("message")
                    .and_then(|m| m.as_str())
                    .map(|m| m.contains("rejected webhook"))
                    .unwrap_or(false)
            })
            .and_then(|v| v.get("trace_id").and_then(|t| t.as_str()).map(String::from))
            .expect("expected webhook rejection log line with trace_id");

        assert_eq!(
            webhook_tid, resp_tid,
            "process_webhook_http must reuse http_trace_middleware trace_id"
        );
    }

    #[tokio::test]
    async fn test_w2b_r3_http_errors_total_counts_only_5xx_not_4xx() {
        let (app, _) = test_router();

        // Trigger 401 client errors (must NOT increment http_errors_total)
        let r401 = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/internal/ledger/summary")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(r401.status(), StatusCode::UNAUTHORIZED);

        // Trigger 503 server error (/readyz against unreachable lazy DB -> MUST increment http_errors_total)
        let r503 = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/readyz")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(r503.status(), StatusCode::SERVICE_UNAVAILABLE);

        let metrics_resp = app
            .oneshot(
                Request::builder()
                    .uri("/metrics")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        let bytes = metrics_resp.into_body().collect().await.unwrap().to_bytes();
        let text = String::from_utf8_lossy(&bytes);

        assert!(
            text.contains(r#"http_requests_total{method="GET",route="/internal/ledger/summary",service="payment",status="401"} 1"#),
            "401 should be counted in http_requests_total:\n{text}"
        );
        assert!(
            !text.contains(r#"http_errors_total{method="GET",route="/internal/ledger/summary",service="payment",status="401"}"#),
            "4xx must NOT be counted in http_errors_total:\n{text}"
        );
        assert!(
            text.contains(r#"http_errors_total{method="GET",route="/readyz",service="payment",status="503"} 1"#),
            "5xx MUST be counted in http_errors_total:\n{text}"
        );
    }

    #[test]
    fn test_w2b_r4_sse_event_projection_redacts_seller_allocation_only() {
        use crate::domain::{
            PaymentRecord, PaymentSseEvent, PaymentWithAllocations, ShopAllocationRecord,
        };

        let now = Utc::now();
        let payment_id = Uuid::new_v4();
        let pwa = PaymentWithAllocations {
            payment: PaymentRecord {
                id: payment_id,
                order_group_id: "grp-sse-1".into(),
                order_id: "ord-shop-b".into(),
                user_id: "buyer-owner".into(),
                method: "VIETQR".into(),
                provider: "vietqr_mock".into(),
                amount_vnd: 740_000,
                status: "succeeded".into(),
                provider_tx_id: Some("TX-SSE-SECRET".into()),
                provider_order_code: "NM-SSE-1".into(),
                qr_code: None,
                qr_url: None,
                idempotency_key: None,
                expires_at: None,
                paid_at: Some(now),
                trace_id: "trace-sse".into(),
                created_at: now,
                updated_at: now,
            },
            allocations: vec![
                ShopAllocationRecord {
                    id: Uuid::new_v4(),
                    payment_id,
                    order_id: "ord-shop-a".into(),
                    shop_id: "shop-a".into(),
                    subtotal_vnd: 200_000,
                    commission_vnd: 12_000,
                    shipping_fee_vnd: 20_000,
                    platform_discount_vnd: 10_000,
                    escrow_status: "held".into(),
                    settled_at: Some(now),
                    released_at: None,
                    created_at: now,
                },
                ShopAllocationRecord {
                    id: Uuid::new_v4(),
                    payment_id,
                    order_id: "ord-shop-b".into(),
                    shop_id: "shop-b".into(),
                    subtotal_vnd: 500_000,
                    commission_vnd: 35_000,
                    shipping_fee_vnd: 30_000,
                    platform_discount_vnd: 0,
                    escrow_status: "held".into(),
                    settled_at: Some(now),
                    released_at: None,
                    created_at: now,
                },
            ],
        };
        let base_evt = PaymentSseEvent {
            payment_id,
            order_group_id: "grp-sse-1".into(),
            order_id: "ord-shop-b".into(),
            status: "succeeded".into(),
            amount_vnd: 740_000,
            provider_tx_id: Some("TX-SSE-SECRET".into()),
            updated_at: now,
        };

        // Admin, buyer owner, and seller-as-buyer see full SSE payload.
        for caller in [
            Caller {
                role: "admin".into(),
                user_id: "admin-1".into(),
                ..Default::default()
            },
            Caller {
                role: "buyer".into(),
                user_id: "buyer-owner".into(),
                ..Default::default()
            },
            Caller {
                role: "seller".into(),
                user_id: "buyer-owner".into(),
                shop_id: "shop-other".into(),
                ..Default::default()
            },
        ] {
            let out = project_sse_event_for_caller(base_evt.clone(), &caller, &pwa);
            assert_eq!(out.amount_vnd, 740_000);
            assert_eq!(out.provider_tx_id.as_deref(), Some("TX-SSE-SECRET"));
            assert_eq!(out.order_id, "ord-shop-b");
        }

        // Seller owning only shop-a allocation MUST NOT receive provider_tx_id,
        // cross-shop total amount_vnd, or shop-b's order_id.
        let seller_a = Caller {
            role: "seller".into(),
            user_id: "seller-u1".into(),
            shop_id: "shop-a".into(),
            ..Default::default()
        };
        let out_seller = project_sse_event_for_caller(base_evt, &seller_a, &pwa);
        assert_eq!(out_seller.amount_vnd, 210_000);
        assert!(out_seller.provider_tx_id.is_none());
        assert_eq!(out_seller.order_id, "ord-shop-a");
    }

    #[tokio::test]
    async fn test_w3a_sse_non_uuid_returns_400_without_db_query() {
        let (app, _) = test_router();
        let now = Utc::now().timestamp();
        let buyer_tok = sign_hs256(
            TEST_USER_JWT_SECRET,
            json!({"sub": "buyer-1", "role": "buyer", "exp": now + 60}),
        );
        let admin_tok = sign_hs256(
            TEST_USER_JWT_SECRET,
            json!({"sub": "admin-1", "role": "admin", "exp": now + 60}),
        );

        // Missing auth still gets 401 even for non-UUID id
        let unauth_resp = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/payments/not-a-uuid/events")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(unauth_resp.status(), StatusCode::UNAUTHORIZED);

        // Authenticated buyer & admin with non-UUID ids MUST get 400 (not 500 from lazy DB!)
        for bad_id in ["not-a-uuid", "12345", "ord-sub-1", "zzzz-invalid"] {
            for (role, tok) in [("buyer", &buyer_tok), ("admin", &admin_tok)] {
                let resp = app
                    .clone()
                    .oneshot(
                        Request::builder()
                            .uri(format!("/payments/{bad_id}/events"))
                            .header(header::AUTHORIZATION, format!("Bearer {tok}"))
                            .body(Body::empty())
                            .unwrap(),
                    )
                    .await
                    .unwrap();
                let status = resp.status();
                let body = resp.into_body().collect().await.unwrap().to_bytes();
                let body_str = String::from_utf8_lossy(&body);
                assert_eq!(
                    status,
                    StatusCode::BAD_REQUEST,
                    "role={role} bad_id={bad_id} body={body_str}"
                );
                let parsed: serde_json::Value =
                    serde_json::from_slice(&body).expect("clean JSON 400 body");
                assert_eq!(parsed["error"], "VALIDATION_ERROR");
            }
        }
    }
}
