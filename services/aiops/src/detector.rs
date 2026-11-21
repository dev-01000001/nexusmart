use std::collections::{HashMap, HashSet, VecDeque};
use std::time::Duration;

use chrono::Utc;
use serde::Deserialize;
use tokio::net::TcpStream;
use tracing::warn;
use url::Url;

use crate::config::Config;
use crate::http_client::ResilientHttpClient;
use crate::metrics::Metrics;
use crate::models::AnomalySignal;

pub const METRIC_SERVICE_UP: &str = "service_up";
pub const METRIC_DEPENDENCY_UP: &str = "dependency_up";
pub const METRIC_ERROR_RATE: &str = "error_rate";
pub const METRIC_HTTP_503_RATE: &str = "http_503_rate";
pub const METRIC_LATENCY_P95: &str = "latency_p95";
pub const METRIC_CPU_CORES: &str = "cpu_cores";
pub const METRIC_MEMORY_BYTES: &str = "memory_bytes";

const MIB: f64 = 1024.0 * 1024.0;

#[derive(Debug, Clone)]
pub struct SeriesStats {
    pub mean: f64,
    pub std_dev: f64,
    pub z_score: f64,
    pub prev_ewma: f64,
    pub ewma: f64,
    pub ewma_deviation: f64,
    pub prior_count: usize,
}

#[derive(Debug, Clone)]
pub struct RollingSeries {
    samples: VecDeque<f64>,
    max_len: usize,
    alpha: f64,
    ewma: Option<f64>,
}

impl RollingSeries {
    pub fn new(max_len: usize, alpha: f64) -> Self {
        Self {
            samples: VecDeque::with_capacity(max_len),
            max_len: max_len.max(5),
            alpha: alpha.clamp(0.01, 1.0),
            ewma: None,
        }
    }

    /// Evaluates `value` against prior rolling mean/std_dev and EWMA, then updates state.
    pub fn observe(&mut self, value: f64, min_std_floor: f64, ewma_floor: f64) -> SeriesStats {
        let prior_count = self.samples.len();
        let (mean, std_dev) = if prior_count == 0 {
            (value, 0.0)
        } else {
            let sum: f64 = self.samples.iter().sum();
            let mean = sum / (prior_count as f64);
            let var = self
                .samples
                .iter()
                .map(|x| {
                    let d = x - mean;
                    d * d
                })
                .sum::<f64>()
                / (prior_count as f64);
            (mean, var.sqrt())
        };

        let z_score = if prior_count == 0 {
            0.0
        } else {
            (value - mean) / std_dev.max(min_std_floor)
        };

        let prev_ewma = self.ewma.unwrap_or(value);
        let ewma_deviation = if self.ewma.is_none() {
            0.0
        } else {
            (value - prev_ewma) / prev_ewma.abs().max(ewma_floor)
        };

        let next_ewma = match self.ewma {
            Some(prev) => self.alpha * value + (1.0 - self.alpha) * prev,
            None => value,
        };
        self.ewma = Some(next_ewma);

        if self.samples.len() >= self.max_len {
            self.samples.pop_front();
        }
        self.samples.push_back(value);

        SeriesStats {
            mean,
            std_dev,
            z_score,
            prev_ewma,
            ewma: next_ewma,
            ewma_deviation,
            prior_count,
        }
    }

    pub fn current_ewma(&self) -> Option<f64> {
        self.ewma
    }

    pub fn len(&self) -> usize {
        self.samples.len()
    }

    pub fn is_empty(&self) -> bool {
        self.samples.is_empty()
    }
}

#[derive(Debug, Clone, Default)]
pub struct PollSnapshot {
    pub service_up: HashMap<String, bool>,
    pub dependencies: HashMap<String, bool>,
    pub service_metrics: HashMap<String, HashMap<String, f64>>,
    pub anomalies: Vec<AnomalySignal>,
    pub prometheus_ok: bool,
    pub errors: Vec<String>,
}

#[derive(Debug, Deserialize)]
struct PromQueryResponse {
    status: String,
    data: Option<PromData>,
}

#[derive(Debug, Deserialize)]
struct PromData {
    #[serde(default)]
    result: Vec<PromSample>,
}

#[derive(Debug, Deserialize)]
struct PromSample {
    metric: HashMap<String, String>,
    value: Option<(f64, String)>,
}

pub struct Detector {
    config: Config,
    http: ResilientHttpClient,
    metrics: Metrics,
    series: HashMap<(String, String), RollingSeries>,
    seen_up: HashSet<String>,
    initially_down: HashSet<String>,
    poll_count: u64,
}

impl Detector {
    pub fn new(config: Config, http: ResilientHttpClient, metrics: Metrics) -> Self {
        Self {
            config,
            http,
            metrics,
            series: HashMap::new(),
            seen_up: HashSet::new(),
            initially_down: HashSet::new(),
            poll_count: 0,
        }
    }

    pub async fn check_prometheus_ready(&self) -> Result<(), String> {
        let ready_url = format!("{}/-/ready", self.config.prometheus_url);
        match self.http.probe_http(&ready_url).await {
            Ok(true) => Ok(()),
            Ok(false) => {
                // Fallback for mock servers that only expose /api/v1/query.
                self.query_prom_instant("up")
                    .await
                    .map(|_| ())
                    .map_err(|e| format!("prometheus not ready: {e}"))
            }
            Err(e) => Err(e),
        }
    }

    pub async fn poll_once(&mut self) -> PollSnapshot {
        self.poll_count = self.poll_count.saturating_add(1);
        let mut snapshot = PollSnapshot::default();

        // 1. Direct dependency probes (NATS HTTP healthz, optional Postgres/Redis TCP)
        let nats_probe = self.probe_nats().await;
        if let Some(up) = nats_probe {
            snapshot.dependencies.insert("nats".to_string(), up);
            self.metrics.set_dependency_up("nats", up);
        }
        if let Some(ref addr) = self.config.postgres_tcp_addr {
            let up = probe_tcp(addr).await;
            snapshot.dependencies.insert("postgres".to_string(), up);
            self.metrics.set_dependency_up("postgres", up);
        }
        if let Some(ref addr) = self.config.redis_tcp_addr {
            let up = probe_tcp(addr).await;
            snapshot.dependencies.insert("redis".to_string(), up);
            self.metrics.set_dependency_up("redis", up);
        }

        // 2. Query Prometheus concurrently
        let q_up = r#"up{job="nexusmart-services"}"#;
        let q_http_err = r#"sum by (service) (rate(http_requests_total{status=~"5.."}[1m])) / clamp_min(sum by (service) (rate(http_requests_total[1m])), 0.001)"#;
        let q_grpc_err = r#"sum by (service) (rate(grpc_errors_total{code=~"Internal|Unavailable|DeadlineExceeded|Unknown|Aborted|ResourceExhausted"}[1m])) / clamp_min(sum by (service) (rate(grpc_requests_total[1m])), 0.001)"#;
        let q_http_503 = r#"sum by (service) (rate(http_requests_total{status="503"}[1m]))"#;
        let q_http_p95 = r#"max by (service) (histogram_quantile(0.95, sum by (service, le) (rate(http_request_duration_seconds_bucket[1m]))))"#;
        let q_grpc_p95 = r#"max by (service) (histogram_quantile(0.95, sum by (service, le) (rate(grpc_request_duration_seconds_bucket[1m]))))"#;
        let q_cpu_cadv = r#"sum by (service) (rate(container_cpu_usage_seconds_total[1m]))"#;
        let q_cpu_proc = r#"sum by (service) (rate(process_cpu_seconds_total[1m]))"#;
        let q_mem_cadv = r#"sum by (service) (container_memory_working_set_bytes)"#;
        let q_mem_proc = r#"sum by (service) (process_resident_memory_bytes)"#;
        let q_dep_up = r#"nexusmart_dependency_up"#;

        let (
            r_up,
            r_http_err,
            r_grpc_err,
            r_http_503,
            r_http_p95,
            r_grpc_p95,
            r_cpu_cadv,
            r_cpu_proc,
            r_mem_cadv,
            r_mem_proc,
            r_dep_up,
        ) = tokio::join!(
            self.query_by_label(q_up, "service"),
            self.query_by_label(q_http_err, "service"),
            self.query_by_label(q_grpc_err, "service"),
            self.query_by_label(q_http_503, "service"),
            self.query_by_label(q_http_p95, "service"),
            self.query_by_label(q_grpc_p95, "service"),
            self.query_by_label(q_cpu_cadv, "service"),
            self.query_by_label(q_cpu_proc, "service"),
            self.query_by_label(q_mem_cadv, "service"),
            self.query_by_label(q_mem_proc, "service"),
            self.query_by_label(q_dep_up, "dependency"),
        );

        let mut prom_succeeded = false;
        let up_map = unwrap_query("up", r_up, &mut prom_succeeded, &mut snapshot.errors);
        let http_err_map = unwrap_query(
            "http_error_rate",
            r_http_err,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        let grpc_err_map = unwrap_query(
            "grpc_error_rate",
            r_grpc_err,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        let http_503_map = unwrap_query(
            "http_503_rate",
            r_http_503,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        let http_p95_map = unwrap_query(
            "http_latency_p95",
            r_http_p95,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        let grpc_p95_map = unwrap_query(
            "grpc_latency_p95",
            r_grpc_p95,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        let mut cpu_map = unwrap_query(
            "container_cpu",
            r_cpu_cadv,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        let cpu_proc_map = unwrap_query(
            "process_cpu",
            r_cpu_proc,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        for (svc, val) in cpu_proc_map {
            cpu_map.entry(svc).or_insert(val);
        }

        let mut mem_map = unwrap_query(
            "container_memory",
            r_mem_cadv,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        let mem_proc_map = unwrap_query(
            "process_memory",
            r_mem_proc,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        for (svc, val) in mem_proc_map {
            mem_map.entry(svc).or_insert(val);
        }

        let prom_dep_map = unwrap_query(
            "dependency_up",
            r_dep_up,
            &mut prom_succeeded,
            &mut snapshot.errors,
        );
        for (dep, val) in prom_dep_map {
            snapshot.dependencies.entry(dep).or_insert(val >= 0.5);
        }

        snapshot.prometheus_ok = prom_succeeded;

        // 3. Evaluate dependency anomalies (e.g. NATS outage)
        let now = Utc::now();
        for (dep, is_up) in &snapshot.dependencies {
            let val = if *is_up { 1.0 } else { 0.0 };
            snapshot
                .service_metrics
                .entry(dep.clone())
                .or_default()
                .insert(METRIC_DEPENDENCY_UP.to_string(), val);
            if !*is_up {
                self.metrics.record_anomaly(dep, METRIC_DEPENDENCY_UP);
                snapshot.anomalies.push(AnomalySignal {
                    service: dep.clone(),
                    metric: METRIC_DEPENDENCY_UP.to_string(),
                    value: 0.0,
                    mean: 1.0,
                    std_dev: 0.0,
                    z_score: -10.0,
                    ewma: 0.0,
                    ewma_deviation: -1.0,
                    threshold_breached: true,
                    statistical_breached: false,
                    reason: format!("dependency '{dep}' health probe is DOWN (up=0)"),
                    observed_at: now,
                });
            }
        }

        // 4. Evaluate each monitored service
        let services = self.config.monitored_services.clone();
        for svc in &services {
            let entry = snapshot.service_metrics.entry(svc.clone()).or_default();

            // 4a. Service availability (up)
            if let Some(&up_val) = up_map.get(svc) {
                let is_up = up_val >= 0.5;
                entry.insert(METRIC_SERVICE_UP.to_string(), up_val);

                if is_up {
                    snapshot.service_up.insert(svc.clone(), true);
                    self.seen_up.insert(svc.clone());
                    self.initially_down.remove(svc);
                } else {
                    let should_ignore = self.config.ignore_initially_down
                        && !self.seen_up.contains(svc)
                        && (self.poll_count == 1 || self.initially_down.contains(svc));
                    if should_ignore {
                        if self.initially_down.insert(svc.clone()) {
                            warn!(
                                service = "aiops",
                                target = svc,
                                "monitored target is down at startup; deferring service_up alert until target is observed healthy"
                            );
                        }
                    } else {
                        snapshot.service_up.insert(svc.clone(), false);
                        self.metrics.record_anomaly(svc, METRIC_SERVICE_UP);
                        snapshot.anomalies.push(AnomalySignal {
                            service: svc.clone(),
                            metric: METRIC_SERVICE_UP.to_string(),
                            value: 0.0,
                            mean: 1.0,
                            std_dev: 0.0,
                            z_score: -10.0,
                            ewma: 0.0,
                            ewma_deviation: -1.0,
                            threshold_breached: true,
                            statistical_breached: false,
                            reason: format!("service '{svc}' scrape target is DOWN (up=0)"),
                            observed_at: now,
                        });
                    }
                }
            }

            // 4b. Error rate (max of HTTP 5xx ratio and gRPC server error ratio)
            let http_err = http_err_map
                .get(svc)
                .copied()
                .unwrap_or(0.0)
                .clamp(0.0, 1.0);
            let grpc_err = grpc_err_map
                .get(svc)
                .copied()
                .unwrap_or(0.0)
                .clamp(0.0, 1.0);
            let err_rate = http_err.max(grpc_err);
            let http_503 = http_503_map.get(svc).copied().unwrap_or(0.0).max(0.0);

            if http_err_map.contains_key(svc)
                || grpc_err_map.contains_key(svc)
                || up_map.get(svc).copied().unwrap_or(0.0) >= 0.5
            {
                entry.insert("http_error_rate".to_string(), http_err);
                entry.insert("grpc_error_rate".to_string(), grpc_err);
                entry.insert(METRIC_ERROR_RATE.to_string(), err_rate);
                if let Some(sig) = self.evaluate_metric(
                    svc,
                    METRIC_ERROR_RATE,
                    err_rate,
                    0.01,
                    0.01,
                    0.05,
                    0.20,
                    now,
                ) {
                    snapshot.anomalies.push(sig);
                }
            }

            if http_503 > 0.0 || http_503_map.contains_key(svc) {
                entry.insert(METRIC_HTTP_503_RATE.to_string(), http_503);
                if let Some(sig) = self.evaluate_metric(
                    svc,
                    METRIC_HTTP_503_RATE,
                    http_503,
                    0.01,
                    0.01,
                    0.03,
                    0.05,
                    now,
                ) {
                    snapshot.anomalies.push(sig);
                }
            }

            // 4c. Latency p95 (max of HTTP p95 and gRPC p95)
            let http_p95 = http_p95_map.get(svc).copied();
            let grpc_p95 = grpc_p95_map.get(svc).copied();
            if let Some(v) = http_p95 {
                entry.insert("http_latency_p95".to_string(), v);
            }
            if let Some(v) = grpc_p95 {
                entry.insert("grpc_latency_p95".to_string(), v);
            }
            let combined_p95 = match (http_p95, grpc_p95) {
                (Some(a), Some(b)) => Some(a.max(b)),
                (Some(a), None) => Some(a),
                (None, Some(b)) => Some(b),
                (None, None) => None,
            };
            if let Some(p95) = combined_p95 {
                entry.insert(METRIC_LATENCY_P95.to_string(), p95);
                if let Some(sig) =
                    self.evaluate_metric(svc, METRIC_LATENCY_P95, p95, 0.02, 0.05, 0.15, 0.50, now)
                {
                    snapshot.anomalies.push(sig);
                }
            }

            // 4d. CPU cores
            if let Some(&cpu) = cpu_map.get(svc) {
                entry.insert(METRIC_CPU_CORES.to_string(), cpu);
                if let Some(sig) =
                    self.evaluate_metric(svc, METRIC_CPU_CORES, cpu, 0.05, 0.05, 0.30, 0.70, now)
                {
                    snapshot.anomalies.push(sig);
                }
            }

            // 4e. Memory bytes
            if let Some(&mem) = mem_map.get(svc) {
                entry.insert(METRIC_MEMORY_BYTES.to_string(), mem);
                if let Some(sig) = self.evaluate_metric(
                    svc,
                    METRIC_MEMORY_BYTES,
                    mem,
                    16.0 * MIB,
                    32.0 * MIB,
                    256.0 * MIB,
                    512.0 * MIB,
                    now,
                ) {
                    snapshot.anomalies.push(sig);
                }
            }
        }

        snapshot
    }

    #[allow(clippy::too_many_arguments)]
    pub fn evaluate_metric(
        &mut self,
        service: &str,
        metric: &str,
        value: f64,
        min_std_floor: f64,
        ewma_floor: f64,
        min_stat_value: f64,
        hard_threshold: f64,
        observed_at: chrono::DateTime<Utc>,
    ) -> Option<AnomalySignal> {
        if !value.is_finite() {
            return None;
        }
        let key = (service.to_string(), metric.to_string());
        let rolling = self.series.entry(key).or_insert_with(|| {
            RollingSeries::new(self.config.rolling_window, self.config.ewma_alpha)
        });
        let stats = rolling.observe(value, min_std_floor, ewma_floor);

        let threshold_breached = value >= hard_threshold;
        let statistical_breached = stats.prior_count >= self.config.warmup_samples
            && value >= min_stat_value
            && (stats.z_score >= self.config.zscore_threshold
                || stats.ewma_deviation >= self.config.ewma_dev_threshold);

        if threshold_breached || statistical_breached {
            self.metrics.record_anomaly(service, metric);
            let reason = match (threshold_breached, statistical_breached) {
                (true, true) => format!(
                    "{metric}={value:.4} breached hard threshold ({hard_threshold:.4}) and statistical baseline (z={:.2}, ewma_dev={:.2})",
                    stats.z_score, stats.ewma_deviation
                ),
                (true, false) => format!(
                    "{metric}={value:.4} breached safety threshold ({hard_threshold:.4})"
                ),
                (false, true) => format!(
                    "{metric}={value:.4} breached statistical baseline (z={:.2}, ewma_dev={:.2}, mean={:.4}, prev_ewma={:.4})",
                    stats.z_score, stats.ewma_deviation, stats.mean, stats.prev_ewma
                ),
                (false, false) => unreachable!(),
            };
            Some(AnomalySignal {
                service: service.to_string(),
                metric: metric.to_string(),
                value,
                mean: stats.mean,
                std_dev: stats.std_dev,
                z_score: stats.z_score,
                ewma: stats.ewma,
                ewma_deviation: stats.ewma_deviation,
                threshold_breached,
                statistical_breached,
                reason,
                observed_at,
            })
        } else {
            None
        }
    }

    async fn probe_nats(&self) -> Option<bool> {
        let url = self.config.nats_monitor_url.as_deref()?;
        match self.http.probe_http(url).await {
            Ok(up) => Some(up),
            Err(_) => Some(false),
        }
    }

    async fn query_prom_instant(&self, promql: &str) -> Result<Vec<PromSample>, String> {
        let base = format!("{}/api/v1/query", self.config.prometheus_url);
        let url = Url::parse_with_params(&base, &[("query", promql)])
            .map_err(|e| e.to_string())?
            .to_string();
        let resp: PromQueryResponse = self
            .http
            .get_json("prometheus", &url, &[])
            .await
            .map_err(|e| e.to_string())?;
        if resp.status != "success" {
            return Err(format!("prometheus returned status '{}'", resp.status));
        }
        Ok(resp.data.map(|d| d.result).unwrap_or_default())
    }

    async fn query_by_label(
        &self,
        promql: &str,
        label: &str,
    ) -> Result<HashMap<String, f64>, String> {
        let samples = self.query_prom_instant(promql).await?;
        let mut out = HashMap::new();
        for s in samples {
            let Some(lbl) = s.metric.get(label) else {
                continue;
            };
            let Some((_, raw_val)) = s.value else {
                continue;
            };
            if let Ok(val) = raw_val.parse::<f64>() {
                if val.is_finite() {
                    out.insert(lbl.clone(), val);
                }
            }
        }
        Ok(out)
    }
}

fn unwrap_query(
    name: &str,
    res: Result<HashMap<String, f64>, String>,
    prom_ok: &mut bool,
    errors: &mut Vec<String>,
) -> HashMap<String, f64> {
    match res {
        Ok(map) => {
            *prom_ok = true;
            map
        }
        Err(e) => {
            errors.push(format!("prometheus query '{name}': {e}"));
            HashMap::new()
        }
    }
}

async fn probe_tcp(addr: &str) -> bool {
    tokio::time::timeout(Duration::from_secs(2), TcpStream::connect(addr))
        .await
        .map(|r| r.is_ok())
        .unwrap_or(false)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_rolling_series_zscore_and_ewma_math() {
        let mut series = RollingSeries::new(60, 0.3);
        let baseline = [0.02, 0.022, 0.018, 0.021, 0.019];
        for v in baseline {
            let st = series.observe(v, 0.02, 0.05);
            assert!(st.z_score.abs() < 1.0);
        }
        let prev_ewma = series.current_ewma().unwrap();
        let spike = 0.35;
        let st = series.observe(spike, 0.02, 0.05);
        // Mean of baseline is 0.02; std_dev ~ 0.0014 < min_floor 0.02 -> z = (0.35 - 0.02) / 0.02 = 16.5
        assert!((st.mean - 0.02).abs() < 1e-6);
        assert!((st.z_score - 16.5).abs() < 1e-3);
        let expected_ewma = 0.3 * spike + 0.7 * prev_ewma;
        assert!((st.ewma - expected_ewma).abs() < 1e-6);
        let expected_dev = (spike - prev_ewma) / prev_ewma.abs().max(0.05);
        assert!((st.ewma_deviation - expected_dev).abs() < 1e-6);
    }

    #[test]
    fn test_detector_statistical_and_threshold_triggers() {
        let cfg = Config::default();
        let http = ResilientHttpClient::new(Duration::from_secs(2));
        let metrics = Metrics::new();
        let mut det = Detector::new(cfg, http, metrics);
        let now = Utc::now();

        // Warmup with healthy latency 20ms
        for _ in 0..4 {
            assert!(det
                .evaluate_metric(
                    "payment",
                    METRIC_LATENCY_P95,
                    0.02,
                    0.02,
                    0.05,
                    0.15,
                    0.50,
                    now
                )
                .is_none());
        }
        // Moderate spike 0.25s (< hard threshold 0.50s) triggers statistically
        let sig = det
            .evaluate_metric(
                "payment",
                METRIC_LATENCY_P95,
                0.25,
                0.02,
                0.05,
                0.15,
                0.50,
                now,
            )
            .expect("statistical anomaly expected");
        assert!(sig.statistical_breached);
        assert!(!sig.threshold_breached);
        assert!(sig.z_score > 3.0);

        // Cold-start hard threshold breach for CPU (0.85 cores >= 0.70) triggers on sample #1
        let cpu_sig = det
            .evaluate_metric(
                "search",
                METRIC_CPU_CORES,
                0.85,
                0.05,
                0.05,
                0.30,
                0.70,
                now,
            )
            .expect("hard threshold anomaly expected");
        assert!(cpu_sig.threshold_breached);

        // Memory pressure breach (600 MiB >= 512 MiB)
        let mem_sig = det
            .evaluate_metric(
                "order",
                METRIC_MEMORY_BYTES,
                600.0 * MIB,
                16.0 * MIB,
                32.0 * MIB,
                256.0 * MIB,
                512.0 * MIB,
                now,
            )
            .expect("memory anomaly expected");
        assert!(mem_sig.threshold_breached);
    }
}
