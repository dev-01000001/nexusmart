use std::sync::Arc;
use std::time::Instant;

use axum::extract::{MatchedPath, Request, State};
use axum::middleware::Next;
use axum::response::Response;
use prometheus::{
    Encoder, Gauge, HistogramOpts, HistogramVec, IntCounterVec, Opts, Registry, TextEncoder,
};

#[derive(Clone)]
pub struct Metrics {
    registry: Arc<Registry>,
    pub http_requests_total: IntCounterVec,
    pub http_errors_total: IntCounterVec,
    pub http_request_duration_seconds: HistogramVec,
    pub grpc_requests_total: IntCounterVec,
    pub grpc_errors_total: IntCounterVec,
    pub grpc_request_duration_seconds: HistogramVec,
    pub webhooks_total: IntCounterVec,
    pub ledger_transactions_total: IntCounterVec,
    process_cpu_seconds_total: Gauge,
    process_resident_memory_bytes: Gauge,
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
            Opts::new("http_errors_total", "Total HTTP 5xx server error responses"),
            &["service", "method", "route", "status"],
        )
        .expect("http_errors_total metric");

        let http_request_duration_seconds = HistogramVec::new(
            HistogramOpts::new(
                "http_request_duration_seconds",
                "HTTP request duration in seconds",
            )
            .buckets(vec![
                0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0,
            ]),
            &["service", "method", "route"],
        )
        .expect("http_request_duration_seconds metric");

        let grpc_requests_total = IntCounterVec::new(
            Opts::new("grpc_requests_total", "Total gRPC requests processed"),
            &["service", "method", "code"],
        )
        .expect("grpc_requests_total metric");

        let grpc_errors_total = IntCounterVec::new(
            Opts::new("grpc_errors_total", "Total gRPC requests returning non-OK"),
            &["service", "method", "code"],
        )
        .expect("grpc_errors_total metric");

        let grpc_request_duration_seconds = HistogramVec::new(
            HistogramOpts::new(
                "grpc_request_duration_seconds",
                "gRPC request duration in seconds",
            )
            .buckets(vec![
                0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0,
            ]),
            &["service", "method"],
        )
        .expect("grpc_request_duration_seconds metric");

        let webhooks_total = IntCounterVec::new(
            Opts::new("payment_webhooks_total", "Total payment webhooks processed"),
            &["result"],
        )
        .expect("payment_webhooks_total metric");

        let ledger_transactions_total = IntCounterVec::new(
            Opts::new(
                "payment_ledger_transactions_total",
                "Total double-entry ledger transactions posted",
            ),
            &["reference_type"],
        )
        .expect("payment_ledger_transactions_total metric");

        let process_cpu_seconds_total = Gauge::with_opts(Opts::new(
            "process_cpu_seconds_total",
            "Total user and system CPU time spent in seconds.",
        ))
        .expect("process_cpu_seconds_total metric");

        let process_resident_memory_bytes = Gauge::with_opts(Opts::new(
            "process_resident_memory_bytes",
            "Resident memory size in bytes.",
        ))
        .expect("process_resident_memory_bytes metric");

        registry
            .register(Box::new(http_requests_total.clone()))
            .expect("register http_requests_total");
        registry
            .register(Box::new(http_errors_total.clone()))
            .expect("register http_errors_total");
        registry
            .register(Box::new(http_request_duration_seconds.clone()))
            .expect("register http_request_duration_seconds");
        registry
            .register(Box::new(grpc_requests_total.clone()))
            .expect("register grpc_requests_total");
        registry
            .register(Box::new(grpc_errors_total.clone()))
            .expect("register grpc_errors_total");
        registry
            .register(Box::new(grpc_request_duration_seconds.clone()))
            .expect("register grpc_request_duration_seconds");
        registry
            .register(Box::new(webhooks_total.clone()))
            .expect("register payment_webhooks_total");
        registry
            .register(Box::new(ledger_transactions_total.clone()))
            .expect("register payment_ledger_transactions_total");
        registry
            .register(Box::new(process_cpu_seconds_total.clone()))
            .expect("register process_cpu_seconds_total");
        registry
            .register(Box::new(process_resident_memory_bytes.clone()))
            .expect("register process_resident_memory_bytes");

        Self {
            registry: Arc::new(registry),
            http_requests_total,
            http_errors_total,
            http_request_duration_seconds,
            grpc_requests_total,
            grpc_errors_total,
            grpc_request_duration_seconds,
            webhooks_total,
            ledger_transactions_total,
            process_cpu_seconds_total,
            process_resident_memory_bytes,
        }
    }

    pub fn record_grpc(&self, method: &str, code: &str, is_error: bool, duration_secs: f64) {
        self.grpc_requests_total
            .with_label_values(&["payment", method, code])
            .inc();
        if is_error {
            self.grpc_errors_total
                .with_label_values(&["payment", method, code])
                .inc();
        }
        self.grpc_request_duration_seconds
            .with_label_values(&["payment", method])
            .observe(duration_secs);
    }

    pub fn record_webhook(&self, result: &str) {
        self.webhooks_total.with_label_values(&[result]).inc();
    }

    pub fn record_ledger_tx(&self, reference_type: &str) {
        self.ledger_transactions_total
            .with_label_values(&[reference_type])
            .inc();
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

    fn refresh_process_metrics(&self) {
        if let Some(cpu_secs) = read_proc_cpu_seconds() {
            self.process_cpu_seconds_total.set(cpu_secs);
        }
        if let Some(rss_bytes) = read_proc_rss_bytes() {
            self.process_resident_memory_bytes.set(rss_bytes as f64);
        }
    }
}

fn read_proc_cpu_seconds() -> Option<f64> {
    let raw = std::fs::read_to_string("/proc/self/stat").ok()?;
    let rparen = raw.rfind(')')?;
    let fields: Vec<&str> = raw[rparen + 1..].split_whitespace().collect();
    // After '(comm)', utime is field index 11 and stime is index 12 (POSIX Linux USER_HZ=100).
    let utime: u64 = fields.get(11)?.parse().ok()?;
    let stime: u64 = fields.get(12)?.parse().ok()?;
    Some((utime.saturating_add(stime)) as f64 / 100.0)
}

fn read_proc_rss_bytes() -> Option<u64> {
    let raw = std::fs::read_to_string("/proc/self/status").ok()?;
    for line in raw.lines() {
        if let Some(rest) = line.strip_prefix("VmRSS:") {
            let kb: u64 = rest.split_whitespace().next()?.parse().ok()?;
            return Some(kb.saturating_mul(1024));
        }
    }
    None
}

impl Default for Metrics {
    fn default() -> Self {
        Self::new()
    }
}

/// Axum middleware recording RED metrics (Rate, Errors, Duration) per matched route.
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
        .with_label_values(&["payment", &method, &route, &status_str])
        .inc();

    if status.is_server_error() {
        metrics
            .http_errors_total
            .with_label_values(&["payment", &method, &route, &status_str])
            .inc();
    }

    metrics
        .http_request_duration_seconds
        .with_label_values(&["payment", &method, &route])
        .observe(elapsed);

    response
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::Body;
    use axum::http::{Request, StatusCode};
    use axum::middleware;
    use axum::routing::get;
    use axum::Router;
    use tower::ServiceExt;

    #[tokio::test]
    async fn test_metrics_recording_and_red_middleware() {
        let metrics = Metrics::default();
        metrics.record_grpc("CreatePayment", "OK", false, 0.015);
        metrics.record_grpc("CreatePayment", "InvalidArgument", true, 0.004);
        metrics.record_webhook("confirmed");
        metrics.record_ledger_tx("payment_zalopay");

        let app = Router::new()
            .route("/ok", get(|| async { StatusCode::OK }))
            .route("/unauth", get(|| async { StatusCode::UNAUTHORIZED }))
            .route("/fail", get(|| async { StatusCode::INTERNAL_SERVER_ERROR }))
            .layer(middleware::from_fn_with_state(
                metrics.clone(),
                red_metrics_middleware,
            ));

        let ok_resp = app
            .clone()
            .oneshot(Request::builder().uri("/ok").body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(ok_resp.status(), StatusCode::OK);

        let unauth_resp = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/unauth")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(unauth_resp.status(), StatusCode::UNAUTHORIZED);

        let err_resp = app
            .oneshot(Request::builder().uri("/fail").body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(err_resp.status(), StatusCode::INTERNAL_SERVER_ERROR);

        let prom = metrics.render_prometheus();
        assert!(prom.contains("http_requests_total"));
        assert!(prom.contains("http_errors_total"));
        assert!(!prom.contains(r#"http_errors_total{method="GET",route="/unauth""#));
        assert!(prom.contains("http_request_duration_seconds"));
        assert!(prom.contains(r#"le="10""#));
        assert!(prom.contains("process_cpu_seconds_total"));
        assert!(prom.contains("process_resident_memory_bytes"));
        assert!(prom.contains("grpc_requests_total"));
        assert!(prom.contains("grpc_errors_total"));
        assert!(prom.contains("payment_webhooks_total"));
        assert!(prom.contains("payment_ledger_transactions_total"));
        assert!(prom.contains("payment_zalopay"));
    }
}
