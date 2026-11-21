use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use async_trait::async_trait;
use chrono::{Duration as ChronoDuration, NaiveDate, Utc};
use serde::{Deserialize, Serialize};
use thiserror::Error;
use tracing::warn;

use crate::config::{Config, LlmMode};
use crate::detector::{
    METRIC_CPU_CORES, METRIC_DEPENDENCY_UP, METRIC_ERROR_RATE, METRIC_HTTP_503_RATE,
    METRIC_LATENCY_P95, METRIC_MEMORY_BYTES, METRIC_SERVICE_UP,
};
use crate::http_client::ResilientHttpClient;
use crate::models::{AnomalySignal, GatheredContext, Incident, RcaResult, RecommendedAction};

#[derive(Debug, Clone, Serialize)]
pub struct RcaRequest {
    pub service: String,
    pub anomalies: Vec<AnomalySignal>,
    pub metrics_snapshot: HashMap<String, f64>,
    pub context: GatheredContext,
}

#[derive(Debug, Clone)]
pub struct RcaOutcome {
    pub result: RcaResult,
    pub cached: bool,
    pub estimated_cost_usd: f64,
}

#[derive(Debug, Error)]
pub enum LlmError {
    #[error("daily LLM budget or call limit exceeded ({0})")]
    BudgetExceeded(String),
    #[error("missing ANTHROPIC_API_KEY in anthropic mode")]
    MissingApiKey,
    #[error("LLM provider request failed: {0}")]
    Upstream(String),
    #[error("failed to parse LLM RCA JSON: {0}")]
    Parse(String),
}

#[async_trait]
pub trait LlmProvider: Send + Sync {
    fn mode(&self) -> &'static str;
    async fn analyze(&self, req: &RcaRequest) -> Result<RcaOutcome, LlmError>;
}

pub fn build_llm_provider(config: &Config, http: ResilientHttpClient) -> Arc<dyn LlmProvider> {
    match config.llm_mode {
        LlmMode::Anthropic => Arc::new(AnthropicProvider::new(config.clone(), http)),
        LlmMode::Mock => Arc::new(MockRcaProvider::new()),
    }
}

/// Deterministic, evidence-driven RCA analyzer using gathered Prometheus metrics,
/// Loki error logs, Tempo slow traces, deploy history, and dependency probes.
#[derive(Default)]
pub struct MockRcaProvider;

impl MockRcaProvider {
    pub fn new() -> Self {
        Self
    }
}

#[async_trait]
impl LlmProvider for MockRcaProvider {
    fn mode(&self) -> &'static str {
        "mock"
    }

    async fn analyze(&self, req: &RcaRequest) -> Result<RcaOutcome, LlmError> {
        let result = classify_from_evidence(req);
        Ok(RcaOutcome {
            result,
            cached: false,
            estimated_cost_usd: 0.0,
        })
    }
}

fn classify_from_evidence(req: &RcaRequest) -> RcaResult {
    let mut evidence: Vec<String> = Vec::new();

    // Helper checks over gathered Loki logs (already redacted).
    let nats_log_services: HashSet<&str> = req
        .context
        .logs
        .iter()
        .filter(|l| contains_any_ci(&l.line, &["nats", "jetstream", "no servers available"]))
        .map(|l| l.service.as_str())
        .collect();
    let nats_log_count = req
        .context
        .logs
        .iter()
        .filter(|l| contains_any_ci(&l.line, &["nats", "jetstream", "no servers available"]))
        .count();

    // 1. Message broker outage (NATS)
    let nats_probe_down = req.context.dependencies.get("nats") == Some(&false)
        || req
            .anomalies
            .iter()
            .any(|a| a.service == "nats" && a.metric == METRIC_DEPENDENCY_UP && a.value < 0.5);
    if nats_probe_down || nats_log_services.len() >= 2 || nats_log_count >= 3 {
        if nats_probe_down {
            evidence.push("NATS health probe (nexusmart_dependency_up{dependency=\"nats\"}) is 0 (DOWN)".to_string());
        }
        if nats_log_count > 0 {
            evidence.push(format!(
                "Observed {nats_log_count} NATS/JetStream connection error log(s) across {} service(s)",
                nats_log_services.len().max(1)
            ));
        }
        return RcaResult {
            root_cause_service: "nats".to_string(),
            category: "message_broker_outage".to_string(),
            summary: "NATS JetStream message broker is unreachable, causing asynchronous event publishing and consumer disruptions across dependent services.".to_string(),
            confidence: if nats_probe_down { 0.96 } else { 0.84 },
            evidence,
            recommended_action: Some(RecommendedAction {
                action: "manual_investigation".to_string(),
                service: "nats".to_string(),
                replicas: None,
                auto_executable: false,
                reason: "NATS is stateful infrastructure outside the auto-remediation allowlist; inspect broker logs and restore NATS connectivity.".to_string(),
            }),
            insufficient_data: false,
        };
    }

    // 2. Service unavailable (up == 0), prioritizing non-gateway backend service
    let down_anomaly = req
        .anomalies
        .iter()
        .find(|a| a.metric == METRIC_SERVICE_UP && a.value < 0.5 && a.service != "gateway")
        .or_else(|| {
            req.anomalies
                .iter()
                .find(|a| a.metric == METRIC_SERVICE_UP && a.value < 0.5)
        });
    let down_from_map = req
        .context
        .service_up
        .iter()
        .find(|(svc, up)| !**up && svc.as_str() != "gateway" && mentions_service_in_evidence(req, svc))
        .map(|(svc, _)| svc.clone());

    if let Some(target_svc) = down_anomaly
        .map(|a| a.service.clone())
        .or(down_from_map)
    {
        evidence.push(format!("Prometheus scrape target up{{service=\"{target_svc}\"}} == 0"));
        if req
            .anomalies
            .iter()
            .any(|a| a.service == "gateway" && a.metric == METRIC_ERROR_RATE)
        {
            evidence.push(format!(
                "Gateway error_rate elevated concurrently due to upstream '{target_svc}' unavailability"
            ));
        }
        for log in req
            .context
            .logs
            .iter()
            .filter(|l| {
                contains_any_ci(
                    &l.line,
                    &["unavailable", "connection refused", "dial tcp", "eof", &target_svc],
                )
            })
            .take(3)
        {
            evidence.push(format!("Loki [{}]: {}", log.service, truncate(&log.line, 160)));
        }
        return RcaResult {
            root_cause_service: target_svc.clone(),
            category: "service_unavailable".to_string(),
            summary: format!(
                "Service '{target_svc}' is down (up=0), causing upstream failures and client errors."
            ),
            confidence: 0.95,
            evidence,
            recommended_action: Some(RecommendedAction {
                action: "restart".to_string(),
                service: target_svc.clone(),
                replicas: None,
                auto_executable: true,
                reason: format!("Restart unhealthy '{target_svc}' container/deployment via allowlisted runbook."),
            }),
            insufficient_data: false,
        };
    }

    // Determine primary candidate service (prefer non-gateway backend if present alongside gateway)
    let primary_service = select_primary_service(req);

    // 3. Database error (PostgreSQL / SQL / connection errors + error_rate or 503 readiness errors)
    let db_logs: Vec<&crate::models::LokiLogEntry> = req
        .context
        .logs
        .iter()
        .filter(|l| {
            contains_any_ci(
                &l.line,
                &[
                    "not currently accepting connections",
                    "sqlstate",
                    "pgx",
                    "postgres",
                    "database",
                    "db ping",
                    "too many clients",
                    "pool exhausted",
                    "failed to connect to db",
                    "5432",
                    "readyz",
                ],
            )
        })
        .collect();
    let has_error_anomaly = req.anomalies.iter().any(|a| {
        a.service == primary_service
            && (a.metric == METRIC_ERROR_RATE || a.metric == METRIC_HTTP_503_RATE)
    });
    let has_503_signal = req
        .metrics_snapshot
        .get(METRIC_HTTP_503_RATE)
        .copied()
        .unwrap_or(0.0)
        > 0.0
        || req
            .anomalies
            .iter()
            .any(|a| a.service == primary_service && a.metric == METRIC_HTTP_503_RATE);

    let service_is_up = req
        .context
        .service_up
        .get(&primary_service)
        .copied()
        .unwrap_or(true);

    if has_error_anomaly && (!db_logs.is_empty() || (has_503_signal && service_is_up && primary_service != "gateway")) {
        for a in req.anomalies.iter().filter(|a| a.service == primary_service) {
            evidence.push(format!("Metric anomaly on '{primary_service}': {}", a.reason));
        }
        if has_503_signal {
            evidence.push(format!(
                "Service '{primary_service}' is returning HTTP 503 responses (readiness/DB connectivity failure)"
            ));
        }
        for log in db_logs.iter().take(3) {
            evidence.push(format!("Loki [{}]: {}", log.service, truncate(&log.line, 160)));
        }
        let confidence = if !db_logs.is_empty() { 0.93 } else { 0.80 };
        return RcaResult {
            root_cause_service: primary_service.clone(),
            category: "database_error".to_string(),
            summary: format!(
                "Database connectivity or query failure on '{primary_service}' ({primary_service}-db) is causing elevated 5xx/503 error rates."
            ),
            confidence,
            evidence,
            recommended_action: Some(RecommendedAction {
                action: "manual_investigation".to_string(),
                service: primary_service.clone(),
                replicas: None,
                auto_executable: false,
                reason: format!(
                    "Verify PostgreSQL '{primary_service}_db' connection status and pool limits before restarting '{primary_service}'."
                ),
            }),
            insufficient_data: false,
        };
    }

    // 4. Post-deploy regression (within 15 minutes of a recorded deploy)
    let deploy_cutoff = Utc::now() - ChronoDuration::minutes(15);
    if let Some(deploy) = req
        .context
        .recent_deploys
        .iter()
        .find(|d| d.service == primary_service && d.deployed_at >= deploy_cutoff)
    {
        if has_error_anomaly
            || req
                .anomalies
                .iter()
                .any(|a| a.service == primary_service && a.metric == METRIC_LATENCY_P95)
        {
            for a in req.anomalies.iter().filter(|a| a.service == primary_service) {
                evidence.push(format!("Metric anomaly on '{primary_service}': {}", a.reason));
            }
            evidence.push(format!(
                "Recent deployment version '{}' (image '{}', has_db_migration={}) occurred at {}",
                deploy.version,
                deploy.image,
                deploy.has_db_migration,
                deploy.deployed_at.to_rfc3339()
            ));
            let reason = if deploy.has_db_migration {
                "Recent deploy includes a database migration; automatic rollback is prohibited (UC-OPS06) — perform manual forward-fix or DB-safe rollback.".to_string()
            } else {
                format!(
                    "Deployment '{}' correlates closely with error/latency jump; consider rolling back '{}' to previous stable image.",
                    deploy.version, primary_service
                )
            };
            return RcaResult {
                root_cause_service: primary_service.clone(),
                category: "post_deploy_regression".to_string(),
                summary: format!(
                    "Regression detected on '{primary_service}' shortly after deploying version '{}'{}",
                    deploy.version,
                    if deploy.has_db_migration {
                        " (contains DB migration; auto-rollback blocked)"
                    } else {
                        ""
                    }
                ),
                confidence: 0.89,
                evidence,
                recommended_action: Some(RecommendedAction {
                    action: "manual_rollback".to_string(),
                    service: primary_service,
                    replicas: None,
                    auto_executable: false,
                    reason,
                }),
                insufficient_data: false,
            };
        }
    }

    // 5. Latency degradation (p95 spike + optional Tempo slow traces)
    if let Some(lat_anomaly) = req
        .anomalies
        .iter()
        .find(|a| a.metric == METRIC_LATENCY_P95 && a.service != "gateway")
        .or_else(|| req.anomalies.iter().find(|a| a.metric == METRIC_LATENCY_P95))
    {
        let svc = lat_anomaly.service.clone();
        evidence.push(format!("Latency p95 anomaly on '{svc}': {}", lat_anomaly.reason));
        let matching_traces: Vec<_> = req
            .context
            .slow_traces
            .iter()
            .filter(|t| t.root_service_name == svc || t.duration_ms >= 200)
            .take(3)
            .collect();
        for tr in &matching_traces {
            evidence.push(format!(
                "Tempo slow trace {} ({} / {}): {}ms",
                tr.trace_id, tr.root_service_name, tr.root_trace_name, tr.duration_ms
            ));
        }
        return RcaResult {
            root_cause_service: svc.clone(),
            category: "latency_degradation".to_string(),
            summary: format!(
                "Elevated p95 latency ({:.0}ms) detected on '{svc}', indicating downstream slowdown, network delay, or contention.",
                lat_anomaly.value * 1000.0
            ),
            confidence: if !matching_traces.is_empty() { 0.92 } else { 0.87 },
            evidence,
            recommended_action: Some(RecommendedAction {
                action: "scale".to_string(),
                service: svc,
                replicas: Some(2),
                auto_executable: true,
                reason: "Scale replicas or restart affected service to relieve latency degradation.".to_string(),
            }),
            insufficient_data: false,
        };
    }

    // 6. CPU saturation
    if let Some(cpu_anomaly) = req.anomalies.iter().find(|a| a.metric == METRIC_CPU_CORES) {
        let svc = cpu_anomaly.service.clone();
        evidence.push(format!("CPU anomaly on '{svc}': {}", cpu_anomaly.reason));
        return RcaResult {
            root_cause_service: svc.clone(),
            category: "cpu_saturation".to_string(),
            summary: format!(
                "High CPU utilization ({:.2} cores) detected on '{svc}', indicating compute saturation or runaway loop.",
                cpu_anomaly.value
            ),
            confidence: 0.91,
            evidence,
            recommended_action: Some(RecommendedAction {
                action: "scale".to_string(),
                service: svc,
                replicas: Some(2),
                auto_executable: true,
                reason: "Scale out service replicas to distribute CPU load within allowlisted bounds.".to_string(),
            }),
            insufficient_data: false,
        };
    }

    // 7. Memory pressure
    if let Some(mem_anomaly) = req
        .anomalies
        .iter()
        .find(|a| a.metric == METRIC_MEMORY_BYTES)
    {
        let svc = mem_anomaly.service.clone();
        let mib = mem_anomaly.value / (1024.0 * 1024.0);
        evidence.push(format!(
            "Memory anomaly on '{svc}' ({mib:.1} MiB): {}",
            mem_anomaly.reason
        ));
        return RcaResult {
            root_cause_service: svc.clone(),
            category: "memory_pressure".to_string(),
            summary: format!(
                "High memory consumption ({mib:.1} MiB) detected on '{svc}', risking OOM kill."
            ),
            confidence: 0.89,
            evidence,
            recommended_action: Some(RecommendedAction {
                action: "restart".to_string(),
                service: svc,
                replicas: None,
                auto_executable: true,
                reason: "Restart service to reclaim leaked memory before OOM termination.".to_string(),
            }),
            insufficient_data: false,
        };
    }

    // 8. Insufficient data / unknown (UC-OPS03)
    for a in req.anomalies.iter().take(3) {
        evidence.push(format!("Observed signal on '{}': {}", a.service, a.reason));
    }
    if evidence.is_empty() {
        evidence.push("No metric anomalies, error logs, or slow traces matched known failure signatures.".to_string());
    }
    RcaResult {
        root_cause_service: primary_service,
        category: "unknown".to_string(),
        summary: "Insufficient corroborating log, trace, or dependency evidence to conclusively determine root cause (UC-OPS03).".to_string(),
        confidence: 0.25,
        evidence,
        recommended_action: None,
        insufficient_data: true,
    }
}

fn select_primary_service(req: &RcaRequest) -> String {
    if let Some(a) = req.anomalies.iter().find(|a| a.service != "gateway") {
        return a.service.clone();
    }
    if let Some(a) = req.anomalies.first() {
        return a.service.clone();
    }
    req.service.clone()
}

fn mentions_service_in_evidence(req: &RcaRequest, svc: &str) -> bool {
    req.service == svc
        || req.anomalies.iter().any(|a| a.service == svc)
        || req
            .context
            .logs
            .iter()
            .any(|l| l.service == svc || l.line.to_ascii_lowercase().contains(&svc.to_ascii_lowercase()))
}

fn contains_any_ci(haystack: &str, needles: &[&str]) -> bool {
    let lower = haystack.to_ascii_lowercase();
    needles.iter().any(|n| lower.contains(&n.to_ascii_lowercase()))
}

fn truncate(s: &str, max_chars: usize) -> String {
    let mut out: String = s.chars().take(max_chars).collect();
    if s.chars().count() > max_chars {
        out.push_str("...");
    }
    out
}

#[derive(Debug)]
struct DailyUsage {
    day: NaiveDate,
    calls: u32,
    cost_usd: f64,
}

impl DailyUsage {
    fn today() -> Self {
        Self {
            day: Utc::now().date_naive(),
            calls: 0,
            cost_usd: 0.0,
        }
    }

    fn reset_if_new_day(&mut self) {
        let today = Utc::now().date_naive();
        if self.day != today {
            self.day = today;
            self.calls = 0;
            self.cost_usd = 0.0;
        }
    }
}

pub struct AnthropicProvider {
    config: Config,
    http: ResilientHttpClient,
    usage: Mutex<DailyUsage>,
    cache: Mutex<HashMap<String, (Instant, RcaResult)>>,
}

impl AnthropicProvider {
    pub fn new(config: Config, http: ResilientHttpClient) -> Self {
        Self {
            config,
            http,
            usage: Mutex::new(DailyUsage::today()),
            cache: Mutex::new(HashMap::new()),
        }
    }

    fn cache_key(req: &RcaRequest) -> String {
        let mut sigs: Vec<String> = req
            .anomalies
            .iter()
            .map(|a| format!("{}:{}", a.service, a.metric))
            .collect();
        sigs.sort();
        sigs.dedup();

        let mut deps: Vec<String> = req
            .context
            .dependencies
            .iter()
            .filter(|(_, up)| !**up)
            .map(|(d, _)| d.clone())
            .collect();
        deps.sort();

        let has_db_err = req.context.logs.iter().any(|l| {
            contains_any_ci(
                &l.line,
                &["postgres", "sqlstate", "pgx", "not currently accepting connections"],
            )
        });
        let has_deploy = !req.context.recent_deploys.is_empty();

        format!(
            "svc={}|anoms={}|deps_down={}|db={}|deploy={}",
            req.service,
            sigs.join(","),
            deps.join(","),
            has_db_err,
            has_deploy
        )
    }

    fn get_cached(&self, key: &str) -> Option<RcaResult> {
        let ttl = Duration::from_secs(self.config.llm_cache_ttl_secs);
        if ttl.is_zero() {
            return None;
        }
        let mut cache = self.cache.lock().expect("llm cache lock");
        if let Some((inserted_at, result)) = cache.get(key) {
            if inserted_at.elapsed() < ttl {
                return Some(result.clone());
            }
            cache.remove(key);
        }
        None
    }

    fn put_cached(&self, key: String, result: RcaResult) {
        if self.config.llm_cache_ttl_secs == 0 {
            return;
        }
        let mut cache = self.cache.lock().expect("llm cache lock");
        if cache.len() >= 256 {
            let ttl = Duration::from_secs(self.config.llm_cache_ttl_secs);
            cache.retain(|_, (ts, _)| ts.elapsed() < ttl);
        }
        cache.insert(key, (Instant::now(), result));
    }
}

#[derive(Debug, Deserialize)]
struct AnthropicResponse {
    #[serde(default)]
    content: Vec<AnthropicBlock>,
    #[serde(default)]
    usage: Option<AnthropicUsage>,
}

#[derive(Debug, Deserialize)]
struct AnthropicBlock {
    #[serde(rename = "type", default)]
    kind: String,
    #[serde(default)]
    text: String,
}

#[derive(Debug, Deserialize)]
struct AnthropicUsage {
    #[serde(default)]
    input_tokens: u64,
    #[serde(default)]
    output_tokens: u64,
}

#[async_trait]
impl LlmProvider for AnthropicProvider {
    fn mode(&self) -> &'static str {
        "anthropic"
    }

    async fn analyze(&self, req: &RcaRequest) -> Result<RcaOutcome, LlmError> {
        let key = Self::cache_key(req);
        if let Some(cached) = self.get_cached(&key) {
            return Ok(RcaOutcome {
                result: cached,
                cached: true,
                estimated_cost_usd: 0.0,
            });
        }

        let api_key = self
            .config
            .anthropic_api_key
            .as_deref()
            .ok_or(LlmError::MissingApiKey)?;

        {
            let mut usage = self.usage.lock().expect("usage lock");
            usage.reset_if_new_day();
            if usage.calls >= self.config.llm_daily_max_calls {
                return Err(LlmError::BudgetExceeded(format!(
                    "calls {}/{}",
                    usage.calls, self.config.llm_daily_max_calls
                )));
            }
            if usage.cost_usd >= self.config.llm_daily_budget_usd {
                return Err(LlmError::BudgetExceeded(format!(
                    "cost ${:.4}/${:.4}",
                    usage.cost_usd, self.config.llm_daily_budget_usd
                )));
            }
        }

        let system_prompt = "You are NexusMart AIOps Root Cause Analyzer. Analyze the provided redacted metrics, Loki logs, Tempo traces, deploy records, and dependency health. Respond ONLY with a single JSON object matching: {\"root_cause_service\": string, \"category\": \"message_broker_outage|service_unavailable|database_error|latency_degradation|cpu_saturation|memory_pressure|post_deploy_regression|unknown\", \"summary\": string, \"confidence\": number, \"evidence\": [string], \"recommended_action\": null | {\"action\": string, \"service\": string, \"replicas\": null|number, \"auto_executable\": boolean, \"reason\": string}, \"insufficient_data\": boolean}. Never recommend automatic rollback if has_db_migration is true. Set insufficient_data=true when evidence is ambiguous.";

        let user_content = serde_json::to_string(req).map_err(|e| LlmError::Parse(e.to_string()))?;
        let payload = serde_json::json!({
            "model": self.config.llm_model,
            "max_tokens": 1024,
            "system": system_prompt,
            "messages": [
                {"role": "user", "content": user_content}
            ]
        });
        let payload_bytes =
            serde_json::to_vec(&payload).map_err(|e| LlmError::Parse(e.to_string()))?;

        let url = format!("{}/v1/messages", self.config.anthropic_base_url);
        let headers = vec![
            ("x-api-key".to_string(), api_key.to_string()),
            ("anthropic-version".to_string(), "2023-06-01".to_string()),
        ];

        let (status, bytes) = self
            .http
            .post_json_raw("anthropic", &url, &headers, &payload_bytes)
            .await
            .map_err(|e| LlmError::Upstream(e.to_string()))?;

        if !(200..300).contains(&status) {
            return Err(LlmError::Upstream(format!(
                "HTTP {status}: {}",
                truncate(&String::from_utf8_lossy(&bytes), 200)
            )));
        }

        let parsed: AnthropicResponse =
            serde_json::from_slice(&bytes).map_err(|e| LlmError::Parse(e.to_string()))?;

        let cost_usd = parsed
            .usage
            .as_ref()
            .map(|u| {
                (u.input_tokens as f64) * (15.0 / 1_000_000.0)
                    + (u.output_tokens as f64) * (75.0 / 1_000_000.0)
            })
            .unwrap_or(0.01)
            .max(0.001);

        {
            let mut usage = self.usage.lock().expect("usage lock");
            usage.reset_if_new_day();
            usage.calls = usage.calls.saturating_add(1);
            usage.cost_usd += cost_usd;
        }

        let text = parsed
            .content
            .iter()
            .find(|b| b.kind == "text" || !b.text.is_empty())
            .map(|b| b.text.as_str())
            .ok_or_else(|| LlmError::Parse("empty content in Anthropic response".to_string()))?;

        let json_str = extract_json_object(text);
        let result: RcaResult =
            serde_json::from_str(json_str).map_err(|e| LlmError::Parse(e.to_string()))?;

        self.put_cached(key, result.clone());

        Ok(RcaOutcome {
            result,
            cached: false,
            estimated_cost_usd: cost_usd,
        })
    }
}

fn extract_json_object(raw: &str) -> &str {
    let trimmed = raw.trim();
    if let Some(start) = trimmed.find('{') {
        if let Some(end) = trimmed.rfind('}') {
            if end >= start {
                return &trimmed[start..=end];
            }
        }
    }
    trimmed
}

/// Sends a best-effort non-blocking notification to the Telegram relay webhook when an incident opens.
pub fn notify_telegram_async(
    webhook_url: Option<String>,
    http: ResilientHttpClient,
    incident: Incident,
) {
    let Some(url) = webhook_url else {
        return;
    };
    tokio::spawn(async move {
        let summary = incident
            .rca
            .as_ref()
            .map(|r| r.summary.clone())
            .unwrap_or_else(|| {
                format!(
                    "Incident opened on '{}' ({})",
                    incident.root_cause_service, incident.category
                )
            });
        let payload = serde_json::json!({
            "status": "firing",
            "alerts": [
                {
                    "status": "firing",
                    "labels": {
                        "alertname": format!("AIOps_{}", incident.category),
                        "service": incident.root_cause_service,
                        "severity": "critical",
                        "incident_id": incident.id.to_string(),
                    },
                    "annotations": {
                        "summary": summary,
                        "category": incident.category,
                    },
                    "startsAt": incident.opened_at.to_rfc3339(),
                }
            ]
        });
        if let Ok(bytes) = serde_json::to_vec(&payload) {
            if let Err(e) = http.post_json_raw("telegram", &url, &[], &bytes).await {
                warn!(
                    service = "aiops",
                    error = %e,
                    incident_id = %incident.id,
                    "failed sending Telegram webhook notification (best-effort)"
                );
            }
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::{DeployRecord, LokiLogEntry, TempoTraceSummary};
    use uuid::Uuid;

    fn signal(service: &str, metric: &str, value: f64) -> AnomalySignal {
        AnomalySignal {
            service: service.to_string(),
            metric: metric.to_string(),
            value,
            mean: 0.0,
            std_dev: 0.0,
            z_score: 5.0,
            ewma: value,
            ewma_deviation: 3.0,
            threshold_breached: true,
            statistical_breached: true,
            reason: format!("{metric}={value}"),
            observed_at: Utc::now(),
        }
    }

    #[tokio::test]
    async fn test_mock_rca_all_five_chaos_scenarios_and_edge_cases() {
        let provider = MockRcaProvider::new();

        // 1. NATS outage
        let mut ctx = GatheredContext::default();
        ctx.dependencies.insert("nats".into(), false);
        let res = provider
            .analyze(&RcaRequest {
                service: "nats".into(),
                anomalies: vec![signal("nats", METRIC_DEPENDENCY_UP, 0.0)],
                metrics_snapshot: HashMap::new(),
                context: ctx,
            })
            .await
            .unwrap();
        assert_eq!(res.result.root_cause_service, "nats");
        assert_eq!(res.result.category, "message_broker_outage");
        assert!(!res.result.insufficient_data);

        // 2. Stop order (order up==0 + gateway cascade error_rate)
        let mut ctx = GatheredContext::default();
        ctx.service_up.insert("order".into(), false);
        ctx.service_up.insert("gateway".into(), true);
        let res = provider
            .analyze(&RcaRequest {
                service: "order".into(),
                anomalies: vec![
                    signal("gateway", METRIC_ERROR_RATE, 0.65),
                    signal("order", METRIC_SERVICE_UP, 0.0),
                ],
                metrics_snapshot: HashMap::new(),
                context: ctx,
            })
            .await
            .unwrap();
        assert_eq!(res.result.root_cause_service, "order");
        assert_eq!(res.result.category, "service_unavailable");

        // 3. Promotion DB error
        let mut ctx = GatheredContext::default();
        ctx.service_up.insert("promotion".into(), true);
        ctx.logs.push(LokiLogEntry {
            timestamp: "1".into(),
            service: "promotion".into(),
            line: "ERROR failed to query promotion_db: database is not currently accepting connections (SQLSTATE 55000)".into(),
        });
        let mut snap = HashMap::new();
        snap.insert(METRIC_HTTP_503_RATE.into(), 0.5);
        let res = provider
            .analyze(&RcaRequest {
                service: "promotion".into(),
                anomalies: vec![signal("promotion", METRIC_ERROR_RATE, 0.85)],
                metrics_snapshot: snap,
                context: ctx,
            })
            .await
            .unwrap();
        assert_eq!(res.result.root_cause_service, "promotion");
        assert_eq!(res.result.category, "database_error");

        // 4. Slow payment
        let mut ctx = GatheredContext::default();
        ctx.slow_traces.push(TempoTraceSummary {
            trace_id: "abc123".into(),
            root_service_name: "payment".into(),
            root_trace_name: "GET /readyz".into(),
            duration_ms: 1250,
            start_time_unix_nano: None,
        });
        let res = provider
            .analyze(&RcaRequest {
                service: "payment".into(),
                anomalies: vec![signal("payment", METRIC_LATENCY_P95, 1.25)],
                metrics_snapshot: HashMap::new(),
                context: ctx,
            })
            .await
            .unwrap();
        assert_eq!(res.result.root_cause_service, "payment");
        assert_eq!(res.result.category, "latency_degradation");

        // 5. Search high CPU
        let res = provider
            .analyze(&RcaRequest {
                service: "search".into(),
                anomalies: vec![signal("search", METRIC_CPU_CORES, 1.45)],
                metrics_snapshot: HashMap::new(),
                context: GatheredContext::default(),
            })
            .await
            .unwrap();
        assert_eq!(res.result.root_cause_service, "search");
        assert_eq!(res.result.category, "cpu_saturation");

        // 6. Post-deploy regression with DB migration prohibits auto-rollback
        let mut ctx = GatheredContext::default();
        ctx.recent_deploys.push(DeployRecord {
            id: Uuid::new_v4(),
            service: "product".into(),
            version: "v2.4.0".into(),
            image: "nexusmart/product:v2.4.0".into(),
            deployed_at: Utc::now() - ChronoDuration::minutes(3),
            commit: Some("deadbeef".into()),
            has_db_migration: true,
        });
        let res = provider
            .analyze(&RcaRequest {
                service: "product".into(),
                anomalies: vec![signal("product", METRIC_ERROR_RATE, 0.40)],
                metrics_snapshot: HashMap::new(),
                context: ctx,
            })
            .await
            .unwrap();
        assert_eq!(res.result.category, "post_deploy_regression");
        let rec = res.result.recommended_action.unwrap();
        assert!(!rec.auto_executable);
        assert!(rec.reason.contains("migration"));

        // 7. Unknown / insufficient data (UC-OPS03)
        let res = provider
            .analyze(&RcaRequest {
                service: "cart".into(),
                anomalies: vec![signal("cart", METRIC_ERROR_RATE, 0.25)],
                metrics_snapshot: HashMap::new(),
                context: GatheredContext::default(),
            })
            .await
            .unwrap();
        assert_eq!(res.result.category, "unknown");
        assert!(res.result.insufficient_data);
    }
}
