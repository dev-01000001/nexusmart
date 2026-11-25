use std::sync::Arc;
use std::time::Instant;

use axum::extract::{MatchedPath, Request, State};
use axum::middleware::Next;
use axum::response::Response;
use prometheus::{
    Encoder, Gauge, GaugeVec, HistogramOpts, HistogramVec, IntCounterVec, IntGauge, Opts, Registry,
    TextEncoder,
};

#[derive(Clone)]
pub struct Metrics {
    registry: Arc<Registry>,
    pub http_requests_total: IntCounterVec,
    pub http_errors_total: IntCounterVec,
    pub http_request_duration_seconds: HistogramVec,
    pub process_resident_memory_bytes: Gauge,
    pub process_cpu_seconds_total: Gauge,
    pub aiops_anomalies_detected_total: IntCounterVec,
    pub aiops_incidents_open: IntGauge,
    pub aiops_remediations_total: IntCounterVec,
    pub aiops_llm_calls_total: IntCounterVec,
    pub nexusmart_dependency_up: GaugeVec,
}

impl Metrics {
    pub fn new() -> Self {
        let registry = Registry::new();

        let http_requests_total = IntCounterVec::new(
            Opts::new("http_requests_total", "Total HTTP requests processed"),
            &["service", "method", "route", "status"],
        )
        .expect("http_requests_total metric");

        let http_errors_total = IntCounterVec::new(
            Opts::new("http_errors_total", "Total HTTP 5xx error responses"),
            &["service", "method", "route", "status"],
        )
        .expect("http_errors_total metric");

        let http_request_duration_seconds = HistogramVec::new(
            HistogramOpts::new(
                "http_request_duration_seconds",
                "HTTP request duration in seconds",
            )
            .buckets(vec![
                0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0,
            ]),
            &["service", "method", "route"],
        )
        .expect("http_request_duration_seconds metric");

        let process_resident_memory_bytes = Gauge::with_opts(Opts::new(
            "process_resident_memory_bytes",
            "Resident memory size in bytes",
        ))
        .expect("process_resident_memory_bytes metric");

        let process_cpu_seconds_total = Gauge::with_opts(Opts::new(
            "process_cpu_seconds_total",
            "Total user and system CPU time spent in seconds",
        ))
        .expect("process_cpu_seconds_total metric");

        let aiops_anomalies_detected_total = IntCounterVec::new(
            Opts::new(
                "aiops_anomalies_detected_total",
                "Total statistical or threshold anomalies detected by AIOps",
            ),
            &["service", "metric"],
        )
        .expect("aiops_anomalies_detected_total metric");

        let aiops_incidents_open = IntGauge::with_opts(Opts::new(
            "aiops_incidents_open",
            "Current number of open incidents tracked by AIOps",
        ))
        .expect("aiops_incidents_open metric");

        let aiops_remediations_total = IntCounterVec::new(
            Opts::new(
                "aiops_remediations_total",
                "Total remediation evaluations and executions",
            ),
            &["service", "action", "mode", "outcome"],
        )
        .expect("aiops_remediations_total metric");

        let aiops_llm_calls_total = IntCounterVec::new(
            Opts::new("aiops_llm_calls_total", "Total LLM RCA invocations"),
            &["mode", "cached", "outcome"],
        )
        .expect("aiops_llm_calls_total metric");

        let nexusmart_dependency_up = GaugeVec::new(
            Opts::new(
                "nexusmart_dependency_up",
                "Dependency health probe status (1 = up, 0 = down)",
            ),
            &["dependency"],
        )
        .expect("nexusmart_dependency_up metric");

        for collector in [
            Box::new(http_requests_total.clone()) as Box<dyn prometheus::core::Collector>,
            Box::new(http_errors_total.clone()),
            Box::new(http_request_duration_seconds.clone()),
            Box::new(process_resident_memory_bytes.clone()),
            Box::new(process_cpu_seconds_total.clone()),
            Box::new(aiops_anomalies_detected_total.clone()),
            Box::new(aiops_incidents_open.clone()),
            Box::new(aiops_remediations_total.clone()),
            Box::new(aiops_llm_calls_total.clone()),
            Box::new(nexusmart_dependency_up.clone()),
        ] {
            registry.register(collector).expect("register metric");
        }

        Self {
            registry: Arc::new(registry),
            http_requests_total,
            http_errors_total,
            http_request_duration_seconds,
            process_resident_memory_bytes,
            process_cpu_seconds_total,
            aiops_anomalies_detected_total,
            aiops_incidents_open,
            aiops_remediations_total,
            aiops_llm_calls_total,
            nexusmart_dependency_up,
        }
    }

    pub fn record_anomaly(&self, service: &str, metric: &str) {
        self.aiops_anomalies_detected_total
            .with_label_values(&[service, metric])
            .inc();
    }

    pub fn set_open_incidents(&self, count: i64) {
        self.aiops_incidents_open.set(count);
    }

    pub fn record_remediation(&self, service: &str, action: &str, mode: &str, outcome: &str) {
        self.aiops_remediations_total
            .with_label_values(&[service, action, mode, outcome])
            .inc();
    }

    pub fn record_llm_call(&self, mode: &str, cached: bool, outcome: &str) {
        self.aiops_llm_calls_total
            .with_label_values(&[mode, if cached { "true" } else { "false" }, outcome])
            .inc();
    }

    pub fn set_dependency_up(&self, dependency: &str, up: bool) {
        self.nexusmart_dependency_up
            .with_label_values(&[dependency])
            .set(if up { 1.0 } else { 0.0 });
    }

    fn refresh_process_metrics(&self) {
        #[cfg(target_os = "linux")]
        {
            if let Ok(statm) = std::fs::read_to_string("/proc/self/statm") {
                if let Some(rss_pages) = statm
                    .split_whitespace()
                    .nth(1)
                    .and_then(|s| s.parse::<f64>().ok())
                {
                    self.process_resident_memory_bytes.set(rss_pages * 4096.0);
                }
            }
            if let Ok(stat) = std::fs::read_to_string("/proc/self/stat") {
                if let Some(after_comm) = stat.rsplit_once(')').map(|(_, rest)| rest) {
                    let fields: Vec<&str> = after_comm.split_whitespace().collect();
                    // Fields after comm start at state (index 0): utime is index 11, stime is index 12.
                    if let (Some(utime), Some(stime)) = (
                        fields.get(11).and_then(|s| s.parse::<f64>().ok()),
                        fields.get(12).and_then(|s| s.parse::<f64>().ok()),
                    ) {
                        // Standard Linux USER_HZ = 100 ticks/sec.
                        self.process_cpu_seconds_total.set((utime + stime) / 100.0);
                    }
                }
            }
        }
    }

    pub fn render_prometheus(&self) -> String {
        self.refresh_process_metrics();
        let encoder = TextEncoder::new();
        let metric_families = self.registry.gather();
        let mut buffer = Vec::new();
        if encoder.encode(&metric_families, &mut buffer).is_ok() {
            String::from_utf8(buffer).unwrap_or_default()
        } else {
            String::new()
        }
    }
}

impl Default for Metrics {
    fn default() -> Self {
        Self::new()
    }
}

/// Axum middleware recording RED metrics (Rate, Errors 5xx only, Duration) per matched route.
pub async fn red_metrics_middleware(
    State(metrics): State<Metrics>,
    req: Request,
    next: Next,
) -> Response {
    let method = req.method().as_str().to_string();
    let route = req
        .extensions()
        .get::<MatchedPath>()
        .map(|mp| mp.as_str().to_string())
        .unwrap_or_else(|| req.uri().path().to_string());

    let start = Instant::now();
    let response = next.run(req).await;
    let elapsed = start.elapsed().as_secs_f64();
    let status = response.status();
    let status_str = status.as_u16().to_string();

    metrics
        .http_requests_total
        .with_label_values(&["aiops", &method, &route, &status_str])
        .inc();

    if status.is_server_error() {
        metrics
            .http_errors_total
            .with_label_values(&["aiops", &method, &route, &status_str])
            .inc();
    }

    metrics
        .http_request_duration_seconds
        .with_label_values(&["aiops", &method, &route])
        .observe(elapsed);

    response
}
