use std::sync::Arc;
use std::time::Instant;

use axum::extract::{MatchedPath, Request, State};
use axum::middleware::Next;
use axum::response::Response;
use prometheus::{
    Encoder, HistogramOpts, HistogramVec, IntCounterVec, Opts, Registry, TextEncoder,
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
            Opts::new(
                "http_errors_total",
                "Total HTTP error responses (4xx and 5xx)",
            ),
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
                0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0,
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

    if status.is_client_error() || status.is_server_error() {
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
