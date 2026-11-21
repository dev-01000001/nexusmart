use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use axum::body::Body;
use axum::extract::{Path, Query, State};
use axum::http::{HeaderMap, Request, StatusCode};
use axum::routing::{get, patch, post};
use axum::{Json, Router};
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tokio::net::TcpListener;
use tower::ServiceExt;

use nexusmart_aiops::adapter::{
    AdapterError, DockerComposeAdapter, KubernetesAdapter, RuntimeAdapter,
};
use nexusmart_aiops::config::{AdapterKind, Config, LlmMode};
use nexusmart_aiops::detector::METRIC_ERROR_RATE;
use nexusmart_aiops::engine::AiopsEngine;
use nexusmart_aiops::http::build_router;
use nexusmart_aiops::http_client::ResilientHttpClient;
use nexusmart_aiops::llm::{AnthropicProvider, LlmError, LlmProvider, RcaRequest};
use nexusmart_aiops::metrics::Metrics;
use nexusmart_aiops::models::{AnomalySignal, GatheredContext, IncidentStatus};

#[derive(Default)]
struct MockObsState {
    prom_ready: AtomicBool,
    nats_up: AtomicBool,
    up_values: Mutex<HashMap<String, f64>>,
    http_err_values: Mutex<HashMap<String, f64>>,
    http_503_values: Mutex<HashMap<String, f64>>,
    http_p95_values: Mutex<HashMap<String, f64>>,
    cpu_values: Mutex<HashMap<String, f64>>,
    mem_values: Mutex<HashMap<String, f64>>,
    loki_logs: Mutex<Vec<(String, String)>>,
    tempo_traces: Mutex<Vec<Value>>,
    telegram_received: Mutex<Vec<Value>>,
}

impl MockObsState {
    fn healthy_baseline() -> Arc<Self> {
        let mut up = HashMap::new();
        for svc in [
            "gateway",
            "user",
            "product",
            "cart",
            "promotion",
            "order",
            "payment",
            "shipping",
            "notification",
        ] {
            up.insert(svc.to_string(), 1.0);
        }
        // Simulate undeployed optional services at startup (like search/chat/assistant before Swarm A/B/C)
        for svc in ["search", "chat", "assistant"] {
            up.insert(svc.to_string(), 0.0);
        }

        let mut p95 = HashMap::new();
        p95.insert("payment".to_string(), 0.025);
        p95.insert("gateway".to_string(), 0.030);

        let mut cpu = HashMap::new();
        cpu.insert("search".to_string(), 0.02);
        cpu.insert("payment".to_string(), 0.05);

        Arc::new(Self {
            prom_ready: AtomicBool::new(true),
            nats_up: AtomicBool::new(true),
            up_values: Mutex::new(up),
            http_err_values: Mutex::new(HashMap::new()),
            http_503_values: Mutex::new(HashMap::new()),
            http_p95_values: Mutex::new(p95),
            cpu_values: Mutex::new(cpu),
            mem_values: Mutex::new(HashMap::new()),
            loki_logs: Mutex::new(Vec::new()),
            tempo_traces: Mutex::new(Vec::new()),
            telegram_received: Mutex::new(Vec::new()),
        })
    }
}

async fn spawn_mock_obs(state: Arc<MockObsState>) -> String {
    async fn prom_ready(State(st): State<Arc<MockObsState>>) -> StatusCode {
        if st.prom_ready.load(Ordering::SeqCst) {
            StatusCode::OK
        } else {
            StatusCode::SERVICE_UNAVAILABLE
        }
    }

    async fn prom_query(
        State(st): State<Arc<MockObsState>>,
        Query(params): Query<HashMap<String, String>>,
    ) -> Json<Value> {
        let q = params.get("query").map(String::as_str).unwrap_or("");
        let map: HashMap<String, f64> = if q.starts_with("up{") || q == "up" {
            st.up_values.lock().unwrap().clone()
        } else if q.contains("status=\"503\"") {
            st.http_503_values.lock().unwrap().clone()
        } else if q.contains("http_requests_total{status=~\"5..\"}") {
            st.http_err_values.lock().unwrap().clone()
        } else if q.contains("http_request_duration_seconds_bucket") {
            st.http_p95_values.lock().unwrap().clone()
        } else if q.contains("container_cpu_usage_seconds_total") {
            st.cpu_values.lock().unwrap().clone()
        } else if q.contains("container_memory_working_set_bytes") {
            st.mem_values.lock().unwrap().clone()
        } else {
            HashMap::new()
        };

        let result: Vec<Value> = map
            .into_iter()
            .map(|(svc, val)| {
                json!({
                    "metric": { "service": svc },
                    "value": [1700000000.0, format!("{val}")]
                })
            })
            .collect();

        Json(json!({
            "status": "success",
            "data": {
                "resultType": "vector",
                "result": result
            }
        }))
    }

    async fn loki_query(State(st): State<Arc<MockObsState>>) -> Json<Value> {
        let logs = st.loki_logs.lock().unwrap().clone();
        let streams: Vec<Value> = logs
            .into_iter()
            .enumerate()
            .map(|(idx, (svc, line))| {
                json!({
                    "stream": { "service": svc },
                    "values": [[format!("{}", 1700000000000000000u64 + idx as u64), line]]
                })
            })
            .collect();
        Json(json!({
            "status": "success",
            "data": {
                "resultType": "streams",
                "result": streams
            }
        }))
    }

    async fn tempo_search(State(st): State<Arc<MockObsState>>) -> Json<Value> {
        let traces = st.tempo_traces.lock().unwrap().clone();
        Json(json!({ "traces": traces }))
    }

    async fn nats_healthz(State(st): State<Arc<MockObsState>>) -> StatusCode {
        if st.nats_up.load(Ordering::SeqCst) {
            StatusCode::OK
        } else {
            StatusCode::SERVICE_UNAVAILABLE
        }
    }

    async fn telegram_webhook(
        State(st): State<Arc<MockObsState>>,
        Json(body): Json<Value>,
    ) -> StatusCode {
        st.telegram_received.lock().unwrap().push(body);
        StatusCode::OK
    }

    let app = Router::new()
        .route("/-/ready", get(prom_ready))
        .route("/api/v1/query", get(prom_query))
        .route("/loki/api/v1/query_range", get(loki_query))
        .route("/api/search", get(tempo_search))
        .route("/nats/healthz", get(nats_healthz))
        .route("/telegram/webhook", post(telegram_webhook))
        .with_state(state);

    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        let _ = axum::serve(listener, app).await;
    });
    format!("http://{addr}")
}

async fn send_json(app: &Router, req: Request<Body>) -> (StatusCode, Value) {
    let resp = app.clone().oneshot(req).await.unwrap();
    let status = resp.status();
    let bytes = resp.into_body().collect().await.unwrap().to_bytes();
    let val = serde_json::from_slice(&bytes)
        .unwrap_or_else(|_| json!({ "raw": String::from_utf8_lossy(&bytes) }));
    (status, val)
}

#[tokio::test]
async fn test_full_chaos_scenarios_end_to_end_with_mock_stack() {
    let state = MockObsState::healthy_baseline();
    let base = spawn_mock_obs(state.clone()).await;

    let cfg = Config {
        prometheus_url: base.clone(),
        loki_url: base.clone(),
        tempo_url: base.clone(),
        nats_monitor_url: Some(format!("{base}/nats/healthz")),
        telegram_webhook_url: Some(format!("{base}/telegram/webhook")),
        ignore_initially_down: true,
        http_timeout_secs: 2,
        ..Default::default()
    };

    let engine = AiopsEngine::new(cfg, Metrics::new());
    let app = build_router(engine.clone());

    // /healthz & /readyz
    let (st, body) = send_json(
        &app,
        Request::builder()
            .uri("/healthz")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(body["service"], "aiops");

    let (st, body) = send_json(
        &app,
        Request::builder()
            .uri("/readyz")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(body["status"], "ready");

    // 3 baseline healthy polls: search/chat/assistant up==0 at startup must NOT open false incidents
    for _ in 0..3 {
        let scan = engine.scan_once().await;
        assert_eq!(scan.anomalies_detected, 0);
        assert!(scan.incidents.is_empty());
    }

    // --- Scenario 1: slow-payment ---
    state
        .http_p95_values
        .lock()
        .unwrap()
        .insert("payment".into(), 1.35);
    state.tempo_traces.lock().unwrap().push(json!({
        "traceID": "trace-pay-slow-1",
        "rootServiceName": "payment",
        "rootTraceName": "GET /readyz",
        "durationMs": 1350
    }));
    let scan = engine.scan_once().await;
    assert_eq!(scan.incidents.len(), 1);
    let pay_inc = &scan.incidents[0];
    assert_eq!(pay_inc.root_cause_service, "payment");
    assert_eq!(pay_inc.category, "latency_degradation");
    assert_eq!(pay_inc.status, IncidentStatus::Open);

    // Deduplication: scanning again while still slow updates same incident instead of creating a 2nd one
    let scan2 = engine.scan_once().await;
    assert_eq!(scan2.incidents.len(), 1);
    assert_eq!(scan2.incidents[0].id, pay_inc.id);
    assert_eq!(
        engine.list_incidents(Some("payment"), Some("open")).len(),
        1
    );

    // Feedback (UC-OPS03)
    let (st, fb_body) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri(format!("/api/v1/incidents/{}/feedback", pay_inc.id))
            .header("content-type", "application/json")
            .body(Body::from(
                r#"{"correct":true,"note":"confirmed slow payment"}"#,
            ))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(fb_body["feedback"][0]["correct"], true);

    // Recover payment -> 2 healthy polls auto-resolve
    state
        .http_p95_values
        .lock()
        .unwrap()
        .insert("payment".into(), 0.025);
    state.tempo_traces.lock().unwrap().clear();
    let _ = engine.scan_once().await;
    assert_eq!(
        engine.get_incident(pay_inc.id).unwrap().status,
        IncidentStatus::Open
    );
    let _ = engine.scan_once().await;
    assert_eq!(
        engine.get_incident(pay_inc.id).unwrap().status,
        IncidentStatus::Resolved
    );

    // --- Scenario 2: stop-order (order up==0 + gateway 5xx cascade) ---
    state.up_values.lock().unwrap().insert("order".into(), 0.0);
    state
        .http_err_values
        .lock()
        .unwrap()
        .insert("gateway".into(), 0.72);
    let scan = engine.scan_once().await;
    assert_eq!(
        scan.incidents.len(),
        1,
        "gateway cascade must correlate into order incident"
    );
    assert_eq!(scan.incidents[0].root_cause_service, "order");
    assert_eq!(scan.incidents[0].category, "service_unavailable");

    // Restore order
    state.up_values.lock().unwrap().insert("order".into(), 1.0);
    state.http_err_values.lock().unwrap().clear();
    let _ = engine.scan_once().await;
    let _ = engine.scan_once().await;
    assert_eq!(engine.list_incidents(Some("order"), Some("open")).len(), 0);

    // --- Scenario 3: promotion-db-error (with PII in Loki logs + gateway cascade) ---
    state
        .http_err_values
        .lock()
        .unwrap()
        .insert("promotion".into(), 0.90);
    state
        .http_503_values
        .lock()
        .unwrap()
        .insert("promotion".into(), 0.50);
    state
        .http_err_values
        .lock()
        .unwrap()
        .insert("gateway".into(), 0.45);
    state.loki_logs.lock().unwrap().push((
        "promotion".into(),
        "ERROR db ping failed for user=buyer@nexusmart.vn phone=0901234567 dsn=postgres://user:secretpw@postgres:5432/promotion_db: database is not currently accepting connections (SQLSTATE 55000)".into(),
    ));
    let scan = engine.scan_once().await;
    assert_eq!(scan.incidents.len(), 1);
    let promo_inc = &scan.incidents[0];
    assert_eq!(promo_inc.root_cause_service, "promotion");
    assert_eq!(promo_inc.category, "database_error");
    assert!(!promo_inc.context.logs.is_empty());
    let stored_log = &promo_inc.context.logs[0].line;
    assert!(
        !stored_log.contains("buyer@nexusmart.vn"),
        "email must be redacted"
    );
    assert!(!stored_log.contains("0901234567"), "phone must be redacted");
    assert!(
        !stored_log.contains("secretpw"),
        "DSN password must be redacted"
    );
    assert!(stored_log.contains("[REDACTED_EMAIL]"));

    // Restore promotion
    state.http_err_values.lock().unwrap().clear();
    state.http_503_values.lock().unwrap().clear();
    state.loki_logs.lock().unwrap().clear();
    let _ = engine.scan_once().await;
    let _ = engine.scan_once().await;

    // --- Scenario 4: nats-outage ---
    state.nats_up.store(false, Ordering::SeqCst);
    let scan = engine.scan_once().await;
    assert_eq!(scan.incidents.len(), 1);
    assert_eq!(scan.incidents[0].root_cause_service, "nats");
    assert_eq!(scan.incidents[0].category, "message_broker_outage");

    // Verify /metrics exposes nexusmart_dependency_up{dependency="nats"} 0 and AIOps metrics
    let resp = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/metrics")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);
    let metrics_text = String::from_utf8(
        resp.into_body()
            .collect()
            .await
            .unwrap()
            .to_bytes()
            .to_vec(),
    )
    .unwrap();
    assert!(metrics_text.contains(r#"nexusmart_dependency_up{dependency="nats"} 0"#));
    assert!(metrics_text.contains("aiops_anomalies_detected_total"));
    assert!(metrics_text.contains("aiops_incidents_open"));
    assert!(metrics_text.contains("aiops_llm_calls_total"));

    // Restore NATS
    state.nats_up.store(true, Ordering::SeqCst);
    let _ = engine.scan_once().await;
    let _ = engine.scan_once().await;

    // --- Scenario 5: search-high-cpu (even while search up==0 from startup) ---
    state
        .cpu_values
        .lock()
        .unwrap()
        .insert("search".into(), 1.25);
    let scan = engine.scan_once().await;
    assert_eq!(scan.incidents.len(), 1);
    assert_eq!(scan.incidents[0].root_cause_service, "search");
    assert_eq!(scan.incidents[0].category, "cpu_saturation");

    // Allow Telegram background tasks a brief moment to deliver
    tokio::time::sleep(Duration::from_millis(80)).await;
    assert!(
        state.telegram_received.lock().unwrap().len() >= 5,
        "expected Telegram notifications for all 5 opened incidents"
    );

    // Check /readyz returns 503 degraded when Prometheus is down
    state.prom_ready.store(false, Ordering::SeqCst);
    let (st, body) = send_json(
        &app,
        Request::builder()
            .uri("/readyz")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    // Fallback query still hits /api/v1/query in this mock; point to dead port to verify 503 degraded:
    let _ = (st, body);
    let dead_cfg = Config {
        prometheus_url: "http://127.0.0.1:1".to_string(),
        http_timeout_secs: 1,
        ..Default::default()
    };
    let dead_app = build_router(AiopsEngine::new(dead_cfg, Metrics::new()));
    let (st_dead, dead_body) = send_json(
        &dead_app,
        Request::builder()
            .uri("/readyz")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(st_dead, StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(dead_body["status"], "degraded");
}

#[tokio::test]
async fn test_maintenance_window_and_alertmanager_and_deploys_and_remediation_http() {
    let state = MockObsState::healthy_baseline();
    let base = spawn_mock_obs(state.clone()).await;

    let cfg = Config {
        prometheus_url: base.clone(),
        loki_url: base.clone(),
        tempo_url: base.clone(),
        nats_monitor_url: Some(format!("{base}/nats/healthz")),
        telegram_webhook_url: None,
        dry_run: true,
        max_actions_per_hour: 2,
        ..Default::default()
    };

    let engine = AiopsEngine::new(cfg, Metrics::new());
    let app = build_router(engine.clone());

    // Baseline scan
    let _ = engine.scan_once().await;

    // Enable maintenance window on order
    let (st, _) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri("/api/v1/maintenance")
            .header("content-type", "application/json")
            .body(Body::from(
                r#"{"service":"order","active":true,"reason":"scheduled rollout","duration_minutes":15}"#,
            ))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);

    // Order goes down during maintenance -> MUST be suppressed
    state.up_values.lock().unwrap().insert("order".into(), 0.0);
    let scan = engine.scan_once().await;
    assert!(
        scan.incidents.is_empty(),
        "maintenance window must suppress new incidents"
    );

    // Alertmanager webhook for order during maintenance -> suppressed
    let (st, am_res) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri("/api/v1/alerts/webhook")
            .header("content-type", "application/json")
            .body(Body::from(
                r#"{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"NexusMartServiceDown","service":"order"},"annotations":{"summary":"order down"}}]}"#,
            ))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(am_res["processed"], 0);

    // Clear maintenance -> now detected
    let (st, _) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri("/api/v1/maintenance")
            .header("content-type", "application/json")
            .body(Body::from(
                r#"{"service":"order","active":false,"reason":"done"}"#,
            ))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);

    let (st, scan_body) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri("/api/v1/scan")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);
    let inc_id = scan_body["incidents"][0]["id"]
        .as_str()
        .unwrap()
        .to_string();

    // Correlate Alertmanager alert to existing open order incident
    let (st, am_res) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri("/api/v1/alerts/webhook")
            .header("content-type", "application/json")
            .body(Body::from(
                r#"{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"NexusMartServiceDown","service":"order"},"fingerprint":"fp-1"}]}"#,
            ))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(am_res["processed"], 1);
    assert_eq!(am_res["incident_ids"][0], inc_id);

    // Test remediation allowlist rejection via HTTP (403)
    let (st, _) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri(format!("/api/v1/incidents/{inc_id}/remediate"))
            .header("content-type", "application/json")
            .body(Body::from(r#"{"action":"drop_db"}"#))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::FORBIDDEN);

    let (st, _) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri(format!("/api/v1/incidents/{inc_id}/remediate"))
            .header("content-type", "application/json")
            .body(Body::from(r#"{"action":"restart","service":"postgres"}"#))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::FORBIDDEN);

    // Allowed dry-run remediation (limit=2/hour)
    for _ in 0..2 {
        let (st, rem_body) = send_json(
            &app,
            Request::builder()
                .method("POST")
                .uri(format!("/api/v1/incidents/{inc_id}/remediate"))
                .header("content-type", "application/json")
                .body(Body::from(r#"{"action":"restart"}"#))
                .unwrap(),
        )
        .await;
        assert_eq!(st, StatusCode::OK);
        assert_eq!(rem_body["mode"], "dry_run");
        assert_eq!(rem_body["outcome"], "dry_run_skipped");
    }

    // 3rd attempt hits hourly rate limit (429)
    let (st, _) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri(format!("/api/v1/incidents/{inc_id}/remediate"))
            .header("content-type", "application/json")
            .body(Body::from(r#"{"action":"scale","replicas":2}"#))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::TOO_MANY_REQUESTS);

    // Audit trail contains maintenance, blocked_allowlist, dry_run_skipped, blocked_rate_limit
    let (st, audit_body) = send_json(
        &app,
        Request::builder()
            .uri("/api/v1/audit")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);
    let outcomes: Vec<&str> = audit_body["entries"]
        .as_array()
        .unwrap()
        .iter()
        .filter_map(|e| e["outcome"].as_str())
        .collect();
    assert!(outcomes.contains(&"suppressed"));
    assert!(outcomes.contains(&"blocked_allowlist"));
    assert!(outcomes.contains(&"dry_run_skipped"));
    assert!(outcomes.contains(&"blocked_rate_limit"));

    // Deploy record POST + GET + post-deploy regression with DB migration
    let (st, _) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri("/api/v1/deploys")
            .header("content-type", "application/json")
            .body(Body::from(
                r#"{"service":"product","version":"v1.9.0","image":"nexusmart/product:v1.9.0","commit":"abc123","has_db_migration":true}"#,
            ))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::CREATED);

    let (st, deploys_body) = send_json(
        &app,
        Request::builder()
            .uri("/api/v1/deploys?service=product")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(deploys_body["total"], 1);
    assert_eq!(deploys_body["deploys"][0]["has_db_migration"], true);

    // Verify 4xx errors do NOT increment http_errors_total (5xx only!)
    let (st, _) = send_json(
        &app,
        Request::builder()
            .uri(format!("/api/v1/incidents/{}", uuid::Uuid::new_v4()))
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::NOT_FOUND);
    let metrics_text = engine.metrics.render_prometheus();
    assert!(
        !metrics_text.contains(r#"http_errors_total{method="GET",route="/api/v1/incidents/{id}",service="aiops",status="404"}"#),
        "404 must not be counted in 5xx-only http_errors_total"
    );
}

#[tokio::test]
async fn test_anthropic_provider_http_cache_and_daily_budget() {
    let call_count = Arc::new(AtomicUsize::new(0));
    let count_clone = call_count.clone();

    let mock_anthropic = Router::new().route(
        "/v1/messages",
        post(move |headers: HeaderMap, Json(body): Json<Value>| {
            let counter = count_clone.clone();
            async move {
                assert_eq!(
                    headers.get("x-api-key").and_then(|v| v.to_str().ok()),
                    Some("test-secret-anthropic-key")
                );
                assert_eq!(
                    headers
                        .get("anthropic-version")
                        .and_then(|v| v.to_str().ok()),
                    Some("2023-06-01")
                );
                assert_eq!(body["model"], "claude-opus-5-5");
                counter.fetch_add(1, Ordering::SeqCst);

                let rca_json = json!({
                    "root_cause_service": "payment",
                    "category": "latency_degradation",
                    "summary": "Payment latency spike confirmed by Anthropic mock",
                    "confidence": 0.94,
                    "evidence": ["p95 > 1.2s"],
                    "recommended_action": {
                        "action": "scale",
                        "service": "payment",
                        "replicas": 2,
                        "auto_executable": true,
                        "reason": "Scale payment replicas"
                    },
                    "insufficient_data": false
                });

                Json(json!({
                    "id": "msg_test_1",
                    "content": [{ "type": "text", "text": format!("```json\n{}\n```", rca_json) }],
                    "usage": { "input_tokens": 500, "output_tokens": 150 }
                }))
            }
        }),
    );

    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        let _ = axum::serve(listener, mock_anthropic).await;
    });

    let cfg = Config {
        llm_mode: LlmMode::Anthropic,
        anthropic_base_url: format!("http://{addr}"),
        anthropic_api_key: Some("test-secret-anthropic-key".to_string()),
        llm_daily_max_calls: 1,
        llm_daily_budget_usd: 5.0,
        llm_cache_ttl_secs: 60,
        ..Default::default()
    };

    // Ensure Debug output never leaks the API key
    let dbg_str = format!("{cfg:?}");
    assert!(!dbg_str.contains("test-secret-anthropic-key"));
    assert!(dbg_str.contains("[REDACTED]"));

    let provider = AnthropicProvider::new(cfg, ResilientHttpClient::new(Duration::from_secs(2)));

    let make_req = |svc: &str| RcaRequest {
        service: svc.to_string(),
        anomalies: vec![AnomalySignal {
            service: svc.to_string(),
            metric: METRIC_ERROR_RATE.to_string(),
            value: 0.5,
            mean: 0.0,
            std_dev: 0.0,
            z_score: 5.0,
            ewma: 0.2,
            ewma_deviation: 4.0,
            threshold_breached: true,
            statistical_breached: true,
            reason: "spike".to_string(),
            observed_at: chrono::Utc::now(),
        }],
        metrics_snapshot: HashMap::new(),
        context: GatheredContext::default(),
    };

    // Call 1: hits HTTP server
    let res1 = provider.analyze(&make_req("payment")).await.unwrap();
    assert!(!res1.cached);
    assert_eq!(res1.result.root_cause_service, "payment");
    assert_eq!(call_count.load(Ordering::SeqCst), 1);

    // Call 2 with same normalized signature: served from TTL cache without hitting API or consuming quota!
    let res2 = provider.analyze(&make_req("payment")).await.unwrap();
    assert!(res2.cached);
    assert_eq!(call_count.load(Ordering::SeqCst), 1);

    // Call 3 with different signature: exceeds daily max calls (1) -> BudgetExceeded
    let err = provider.analyze(&make_req("order")).await.unwrap_err();
    assert!(matches!(err, LlmError::BudgetExceeded(_)));
    assert_eq!(call_count.load(Ordering::SeqCst), 1);
}

#[tokio::test]
async fn test_docker_compose_adapter_http_and_unix_socket() {
    let restarted_ids: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
    let scaled_spec: Arc<Mutex<Option<Value>>> = Arc::new(Mutex::new(None));

    let r_clone = restarted_ids.clone();
    let s_clone = scaled_spec.clone();

    let mock_docker = Router::new()
        .route(
            "/containers/json",
            get(|Query(q): Query<HashMap<String, String>>| async move {
                let filters = q.get("filters").cloned().unwrap_or_default();
                assert!(filters.contains("com.docker.compose.service=order"));
                Json(json!([{ "Id": "abcdef1234567890" }]))
            }),
        )
        .route(
            "/containers/{id}/restart",
            post(move |Path(id): Path<String>| {
                let store = r_clone.clone();
                async move {
                    store.lock().unwrap().push(id);
                    StatusCode::NO_CONTENT
                }
            }),
        )
        .route(
            "/services/{name}",
            get(|Path(name): Path<String>| async move {
                Json(json!({
                    "Version": { "Index": 42 },
                    "Spec": {
                        "Name": name,
                        "Mode": { "Replicated": { "Replicas": 1 } }
                    }
                }))
            }),
        )
        .route(
            "/services/{name}/update",
            post(
                move |Query(q): Query<HashMap<String, String>>, Json(body): Json<Value>| {
                    let store = s_clone.clone();
                    async move {
                        assert_eq!(q.get("version").map(String::as_str), Some("42"));
                        *store.lock().unwrap() = Some(body);
                        StatusCode::OK
                    }
                },
            ),
        );

    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        let _ = axum::serve(listener, mock_docker).await;
    });

    // Single-host compose mode (restart succeeds, scale returns UnsupportedInSingleCompose)
    let mut cfg = Config {
        adapter_kind: AdapterKind::Docker,
        docker_host: Some(format!("http://{addr}")),
        docker_swarm_mode: false,
        ..Default::default()
    };
    let adapter = DockerComposeAdapter::new(
        cfg.clone(),
        ResilientHttpClient::new(Duration::from_secs(2)),
    );

    let msg = adapter.restart("order").await.unwrap();
    assert!(msg.contains("abcdef123456"));
    assert_eq!(
        restarted_ids.lock().unwrap().as_slice(),
        &["abcdef1234567890"]
    );

    let scale_err = adapter.scale("order", 3).await.unwrap_err();
    assert!(matches!(
        scale_err,
        AdapterError::UnsupportedInSingleCompose { .. }
    ));

    // Swarm mode scale succeeds
    cfg.docker_swarm_mode = true;
    let swarm_adapter =
        DockerComposeAdapter::new(cfg, ResilientHttpClient::new(Duration::from_secs(2)));
    let scale_msg = swarm_adapter.scale("order", 3).await.unwrap();
    assert!(scale_msg.contains("3 replicas"));
    let updated = scaled_spec.lock().unwrap().clone().unwrap();
    assert_eq!(updated["Mode"]["Replicated"]["Replicas"], 3);

    // Unix socket transport smoke test
    #[cfg(unix)]
    {
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        use tokio::net::UnixListener;

        let sock_path = format!("/tmp/nexusmart-aiops-test-{}.sock", uuid::Uuid::new_v4());
        let _ = std::fs::remove_file(&sock_path);
        let u_listener = UnixListener::bind(&sock_path).unwrap();
        let sock_path_bg = sock_path.clone();
        tokio::spawn(async move {
            for _ in 0..2 {
                if let Ok((mut stream, _)) = u_listener.accept().await {
                    let mut buf = [0u8; 2048];
                    let n = stream.read(&mut buf).await.unwrap_or(0);
                    let req = String::from_utf8_lossy(&buf[..n]);
                    if req.starts_with("GET /containers/json") {
                        let body = r#"[{"Id":"unixcontainer123"}]"#;
                        let resp = format!(
                            "HTTP/1.1 200 OK\r\nContent-Length: {}\r\n\r\n{}",
                            body.len(),
                            body
                        );
                        let _ = stream.write_all(resp.as_bytes()).await;
                    } else if req.starts_with("POST /containers/unixcontainer123/restart") {
                        let _ = stream
                            .write_all(b"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n")
                            .await;
                    }
                }
            }
            let _ = std::fs::remove_file(&sock_path_bg);
        });

        let u_cfg = Config {
            docker_host: None,
            docker_socket: sock_path.clone(),
            ..Default::default()
        };
        let u_adapter =
            DockerComposeAdapter::new(u_cfg, ResilientHttpClient::new(Duration::from_secs(2)));
        let u_msg = u_adapter.restart("payment").await.unwrap();
        assert!(u_msg.contains("unixcontaine"));
        let _ = std::fs::remove_file(&sock_path);
    }
}

#[tokio::test]
async fn test_kubernetes_adapter_restart_and_scale() {
    let restart_patch: Arc<Mutex<Option<Value>>> = Arc::new(Mutex::new(None));
    let scale_patch: Arc<Mutex<Option<Value>>> = Arc::new(Mutex::new(None));

    let r_clone = restart_patch.clone();
    let s_clone = scale_patch.clone();

    let mock_k8s = Router::new()
        .route(
            "/apis/apps/v1/namespaces/{ns}/deployments/{name}",
            patch(
                move |Path((ns, name)): Path<(String, String)>,
                      headers: HeaderMap,
                      body: axum::body::Bytes| {
                    let store = r_clone.clone();
                    async move {
                        assert_eq!(ns, "nexusmart");
                        assert_eq!(name, "payment");
                        assert_eq!(
                            headers.get("authorization").and_then(|v| v.to_str().ok()),
                            Some("Bearer k8s-test-token")
                        );
                        assert_eq!(
                            headers.get("content-type").and_then(|v| v.to_str().ok()),
                            Some("application/strategic-merge-patch+json")
                        );
                        let parsed: Value = serde_json::from_slice(&body).unwrap();
                        *store.lock().unwrap() = Some(parsed);
                        StatusCode::OK
                    }
                },
            ),
        )
        .route(
            "/apis/apps/v1/namespaces/{ns}/deployments/{name}/scale",
            patch(
                move |Path((ns, name)): Path<(String, String)>,
                      headers: HeaderMap,
                      body: axum::body::Bytes| {
                    let store = s_clone.clone();
                    async move {
                        assert_eq!(ns, "nexusmart");
                        assert_eq!(name, "search");
                        assert_eq!(
                            headers.get("authorization").and_then(|v| v.to_str().ok()),
                            Some("Bearer k8s-test-token")
                        );
                        assert_eq!(
                            headers.get("content-type").and_then(|v| v.to_str().ok()),
                            Some("application/merge-patch+json")
                        );
                        let parsed: Value = serde_json::from_slice(&body).unwrap();
                        *store.lock().unwrap() = Some(parsed);
                        StatusCode::OK
                    }
                },
            ),
        );

    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        let _ = axum::serve(listener, mock_k8s).await;
    });

    let cfg = Config {
        adapter_kind: AdapterKind::Kubernetes,
        kube_api_url: format!("http://{addr}"),
        kube_token: Some("k8s-test-token".to_string()),
        kube_namespace: "nexusmart".to_string(),
        ..Default::default()
    };

    let adapter = KubernetesAdapter::new(cfg, ResilientHttpClient::new(Duration::from_secs(2)));

    let r_msg = adapter.restart("payment").await.unwrap();
    assert!(r_msg.contains("nexusmart/payment"));
    let rp = restart_patch.lock().unwrap().clone().unwrap();
    assert!(
        rp["spec"]["template"]["metadata"]["annotations"]["kubectl.kubernetes.io/restartedAt"]
            .as_str()
            .is_some()
    );

    let s_msg = adapter.scale("search", 3).await.unwrap();
    assert!(s_msg.contains("3 replicas"));
    let sp = scale_patch.lock().unwrap().clone().unwrap();
    assert_eq!(sp["spec"]["replicas"], 3);

    // Out of bounds replicas rejected
    assert!(matches!(
        adapter.scale("search", 0).await,
        Err(AdapterError::InvalidReplicas(0))
    ));
    assert!(matches!(
        adapter.scale("search", 11).await,
        Err(AdapterError::InvalidReplicas(11))
    ));
}

#[tokio::test]
async fn test_llm_error_preserves_raw_incident_data_and_live_remediation() {
    let state = MockObsState::healthy_baseline();
    let base = spawn_mock_obs(state.clone()).await;

    let restarted: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
    let r_clone = restarted.clone();
    let mock_docker = Router::new()
        .route(
            "/containers/json",
            get(|| async { Json(json!([{ "Id": "livecontainer999" }])) }),
        )
        .route(
            "/containers/{id}/restart",
            post(move |Path(id): Path<String>| {
                let store = r_clone.clone();
                async move {
                    store.lock().unwrap().push(id);
                    StatusCode::NO_CONTENT
                }
            }),
        );
    let d_listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let d_addr = d_listener.local_addr().unwrap();
    tokio::spawn(async move {
        let _ = axum::serve(d_listener, mock_docker).await;
    });

    // Anthropic mode with 0 max calls -> guaranteed budget failure (UC-OPS03 raw context preservation)
    let cfg = Config {
        prometheus_url: base.clone(),
        loki_url: base.clone(),
        tempo_url: base.clone(),
        nats_monitor_url: Some(format!("{base}/nats/healthz")),
        telegram_webhook_url: None,
        llm_mode: LlmMode::Anthropic,
        anthropic_api_key: Some("dummy-key".to_string()),
        llm_daily_max_calls: 0,
        dry_run: false,
        adapter_kind: AdapterKind::Docker,
        docker_host: Some(format!("http://{d_addr}")),
        ..Default::default()
    };

    let engine = AiopsEngine::new(cfg, Metrics::new());
    let app = build_router(engine.clone());
    let _ = engine.scan_once().await;

    state.up_values.lock().unwrap().insert("order".into(), 0.0);
    state
        .loki_logs
        .lock()
        .unwrap()
        .push(("order".into(), "FATAL panic in order service".into()));

    let scan = engine.scan_once().await;
    assert_eq!(scan.incidents.len(), 1);
    let inc = &scan.incidents[0];
    assert!(inc.rca.is_none());
    assert!(inc.rca_error.as_deref().unwrap_or("").contains("budget"));
    assert!(!inc.anomalies.is_empty());
    assert!(!inc.context.logs.is_empty());

    // Live remediation via HTTP actually hits Docker mock adapter
    let (st, rem_body) = send_json(
        &app,
        Request::builder()
            .method("POST")
            .uri(format!("/api/v1/incidents/{}/remediate", inc.id))
            .header("content-type", "application/json")
            .body(Body::from(r#"{"action":"restart"}"#))
            .unwrap(),
    )
    .await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(rem_body["mode"], "live");
    assert_eq!(rem_body["outcome"], "executed");
    assert_eq!(restarted.lock().unwrap().as_slice(), &["livecontainer999"]);
}
