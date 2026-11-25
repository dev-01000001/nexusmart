use axum::extract::{Path, Query, Request, State};
use axum::http::{HeaderValue, StatusCode};
use axum::middleware::{self, Next};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use chrono::{DateTime, Utc};
use serde::Deserialize;
use tracing::{info_span, Instrument};
use tracing_opentelemetry::OpenTelemetrySpanExt;
use uuid::Uuid;

use crate::engine::AiopsEngine;
use crate::metrics::red_metrics_middleware;
use crate::models::{AlertmanagerWebhook, DeployRecord};
use crate::remediator::RemediationPolicyError;
use crate::telemetry::{extract_or_generate_trace_id, extract_otel_context};

pub fn build_router(engine: AiopsEngine) -> Router {
    let metrics = engine.metrics.clone();

    Router::new()
        .route("/healthz", get(healthz))
        .route("/readyz", get(readyz))
        .route("/metrics", get(metrics_handler))
        .route("/api/v1/incidents", get(list_incidents))
        .route("/api/v1/incidents/{id}", get(get_incident))
        .route("/api/v1/incidents/{id}/feedback", post(submit_feedback))
        .route("/api/v1/incidents/{id}/remediate", post(remediate_incident))
        .route("/api/v1/scan", post(trigger_scan))
        .route("/api/v1/deploys", get(list_deploys).post(create_deploy))
        .route("/api/v1/audit", get(list_audit))
        .route("/api/v1/alerts/webhook", post(alertmanager_webhook))
        .route(
            "/api/v1/maintenance",
            get(list_maintenance).post(set_maintenance),
        )
        .layer(middleware::from_fn_with_state(
            metrics,
            red_metrics_middleware,
        ))
        .layer(middleware::from_fn(trace_context_middleware))
        .with_state(engine)
}

async fn trace_context_middleware(req: Request, next: Next) -> Response {
    let headers = req.headers();
    let traceparent = headers.get("traceparent").and_then(|v| v.to_str().ok());
    let x_trace_id = headers.get("x-trace-id").and_then(|v| v.to_str().ok());
    let trace_id = extract_or_generate_trace_id(traceparent, x_trace_id);
    let parent_cx = extract_otel_context(headers);

    let span = info_span!(
        "http_request",
        service = "aiops",
        trace_id = %trace_id,
        method = %req.method(),
        path = %req.uri().path()
    );
    span.set_parent(parent_cx);

    let mut resp = next.run(req).instrument(span).await;
    if let Ok(val) = HeaderValue::from_str(&trace_id) {
        resp.headers_mut().insert("x-trace-id", val);
    }
    resp
}

fn json_error(status: StatusCode, code: &str, message: impl Into<String>) -> Response {
    let body = serde_json::json!({
        "error": {
            "code": code,
            "message": message.into(),
        }
    });
    (status, Json(body)).into_response()
}

async fn healthz() -> impl IntoResponse {
    Json(serde_json::json!({
        "status": "ok",
        "service": "aiops"
    }))
}

async fn readyz(State(engine): State<AiopsEngine>) -> impl IntoResponse {
    match engine.check_ready().await {
        Ok(()) => (
            StatusCode::OK,
            Json(serde_json::json!({
                "status": "ready",
                "service": "aiops",
                "prometheus": {
                    "reachable": true,
                    "url": engine.config.prometheus_url,
                }
            })),
        ),
        Err(err) => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(serde_json::json!({
                "status": "degraded",
                "service": "aiops",
                "prometheus": {
                    "reachable": false,
                    "url": engine.config.prometheus_url,
                    "error": err,
                }
            })),
        ),
    }
}

async fn metrics_handler(State(engine): State<AiopsEngine>) -> impl IntoResponse {
    let body = engine.metrics.render_prometheus();
    (
        StatusCode::OK,
        [(
            axum::http::header::CONTENT_TYPE,
            "text/plain; version=0.0.4; charset=utf-8",
        )],
        body,
    )
}

#[derive(Debug, Deserialize)]
struct IncidentQuery {
    service: Option<String>,
    status: Option<String>,
}

async fn list_incidents(
    State(engine): State<AiopsEngine>,
    Query(q): Query<IncidentQuery>,
) -> impl IntoResponse {
    let incidents = engine.list_incidents(q.service.as_deref(), q.status.as_deref());
    let total = incidents.len();
    Json(serde_json::json!({
        "incidents": incidents,
        "total": total,
    }))
}

async fn get_incident(State(engine): State<AiopsEngine>, Path(id): Path<Uuid>) -> Response {
    match engine.get_incident(id) {
        Some(inc) => (StatusCode::OK, Json(inc)).into_response(),
        None => json_error(
            StatusCode::NOT_FOUND,
            "not_found",
            format!("incident '{id}' not found"),
        ),
    }
}

#[derive(Debug, Deserialize)]
struct FeedbackBody {
    correct: bool,
    #[serde(default)]
    note: String,
}

async fn submit_feedback(
    State(engine): State<AiopsEngine>,
    Path(id): Path<Uuid>,
    Json(body): Json<FeedbackBody>,
) -> Response {
    match engine.submit_feedback(id, body.correct, body.note) {
        Some(inc) => (StatusCode::OK, Json(inc)).into_response(),
        None => json_error(
            StatusCode::NOT_FOUND,
            "not_found",
            format!("incident '{id}' not found"),
        ),
    }
}

#[derive(Debug, Deserialize)]
struct RemediateBody {
    action: String,
    #[serde(default)]
    replicas: Option<u32>,
    #[serde(default)]
    service: Option<String>,
}

async fn remediate_incident(
    State(engine): State<AiopsEngine>,
    Path(id): Path<Uuid>,
    Json(body): Json<RemediateBody>,
) -> Response {
    let Some((record, result)) = engine
        .remediate_incident(id, body.service, &body.action, body.replicas)
        .await
    else {
        return json_error(
            StatusCode::NOT_FOUND,
            "not_found",
            format!("incident '{id}' not found"),
        );
    };

    match result {
        Ok(()) => (StatusCode::OK, Json(record)).into_response(),
        Err(RemediationPolicyError::BlockedAllowlist(msg)) => (
            StatusCode::FORBIDDEN,
            Json(serde_json::json!({
                "error": {
                    "code": "blocked_allowlist",
                    "message": msg,
                },
                "remediation": record,
            })),
        )
            .into_response(),
        Err(RemediationPolicyError::BlockedInvalidInput(msg)) => (
            StatusCode::BAD_REQUEST,
            Json(serde_json::json!({
                "error": {
                    "code": "invalid_input",
                    "message": msg,
                },
                "remediation": record,
            })),
        )
            .into_response(),
        Err(RemediationPolicyError::BlockedRateLimit(msg)) => (
            StatusCode::TOO_MANY_REQUESTS,
            Json(serde_json::json!({
                "error": {
                    "code": "rate_limited",
                    "message": msg,
                },
                "remediation": record,
            })),
        )
            .into_response(),
        Err(RemediationPolicyError::ExecutionFailed(msg)) => (
            StatusCode::BAD_GATEWAY,
            Json(serde_json::json!({
                "error": {
                    "code": "execution_failed",
                    "message": msg,
                },
                "remediation": record,
            })),
        )
            .into_response(),
    }
}

async fn trigger_scan(State(engine): State<AiopsEngine>) -> impl IntoResponse {
    let result = engine.scan_once().await;
    (StatusCode::OK, Json(result))
}

#[derive(Debug, Deserialize)]
struct DeployQuery {
    service: Option<String>,
}

async fn list_deploys(
    State(engine): State<AiopsEngine>,
    Query(q): Query<DeployQuery>,
) -> impl IntoResponse {
    let deploys = engine.list_deploys(q.service.as_deref());
    let total = deploys.len();
    Json(serde_json::json!({
        "deploys": deploys,
        "total": total,
    }))
}

#[derive(Debug, Deserialize)]
struct CreateDeployBody {
    service: String,
    version: String,
    image: String,
    #[serde(default)]
    deployed_at: Option<DateTime<Utc>>,
    #[serde(default)]
    commit: Option<String>,
    #[serde(default)]
    has_db_migration: bool,
}

async fn create_deploy(
    State(engine): State<AiopsEngine>,
    Json(body): Json<CreateDeployBody>,
) -> Response {
    let svc = body.service.trim().to_ascii_lowercase();
    if svc.is_empty() || body.version.trim().is_empty() || body.image.trim().is_empty() {
        return json_error(
            StatusCode::BAD_REQUEST,
            "invalid_input",
            "service, version, and image are required",
        );
    }
    let rec = DeployRecord {
        id: Uuid::new_v4(),
        service: svc,
        version: body.version.trim().to_string(),
        image: body.image.trim().to_string(),
        deployed_at: body.deployed_at.unwrap_or_else(Utc::now),
        commit: body.commit,
        has_db_migration: body.has_db_migration,
    };
    let saved = engine.record_deploy(rec);
    (StatusCode::CREATED, Json(saved)).into_response()
}

#[derive(Debug, Deserialize)]
struct AuditQuery {
    service: Option<String>,
    outcome: Option<String>,
}

async fn list_audit(
    State(engine): State<AiopsEngine>,
    Query(q): Query<AuditQuery>,
) -> impl IntoResponse {
    let entries = engine.list_audit(q.service.as_deref(), q.outcome.as_deref());
    let total = entries.len();
    Json(serde_json::json!({
        "entries": entries,
        "total": total,
    }))
}

async fn alertmanager_webhook(
    State(engine): State<AiopsEngine>,
    Json(payload): Json<AlertmanagerWebhook>,
) -> impl IntoResponse {
    let (processed, incident_ids) = engine.handle_alertmanager_webhook(payload).await;
    (
        StatusCode::OK,
        Json(serde_json::json!({
            "status": "ok",
            "processed": processed,
            "incident_ids": incident_ids,
        })),
    )
}

#[derive(Debug, Deserialize)]
struct MaintenanceBody {
    service: String,
    #[serde(default = "default_true")]
    active: bool,
    #[serde(default)]
    reason: String,
    #[serde(default)]
    duration_minutes: Option<i64>,
    #[serde(default)]
    until: Option<DateTime<Utc>>,
}

fn default_true() -> bool {
    true
}

async fn set_maintenance(
    State(engine): State<AiopsEngine>,
    Json(body): Json<MaintenanceBody>,
) -> Response {
    if body.service.trim().is_empty() {
        return json_error(
            StatusCode::BAD_REQUEST,
            "invalid_input",
            "service is required",
        );
    }
    let window = engine.set_maintenance(
        body.service,
        body.active,
        body.reason,
        body.duration_minutes,
        body.until,
    );
    (StatusCode::OK, Json(window)).into_response()
}

async fn list_maintenance(State(engine): State<AiopsEngine>) -> impl IntoResponse {
    let windows = engine.list_maintenance();
    let total = windows.len();
    Json(serde_json::json!({
        "windows": windows,
        "total": total,
    }))
}
