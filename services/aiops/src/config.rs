use std::collections::HashSet;
use std::env;
use std::fmt;

use crate::telemetry::mask_secret;

pub const DEFAULT_MONITORED_SERVICES: &[&str] = &[
    "gateway",
    "user",
    "product",
    "cart",
    "promotion",
    "order",
    "payment",
    "shipping",
    "notification",
    "search",
    "chat",
    "assistant",
];

#[derive(Clone, PartialEq, Eq)]
pub enum LlmMode {
    Mock,
    Anthropic,
}

impl fmt::Debug for LlmMode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Mock => write!(f, "mock"),
            Self::Anthropic => write!(f, "anthropic"),
        }
    }
}

#[derive(Clone, PartialEq, Eq)]
pub enum AdapterKind {
    Docker,
    Kubernetes,
}

impl fmt::Debug for AdapterKind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Docker => write!(f, "docker"),
            Self::Kubernetes => write!(f, "kubernetes"),
        }
    }
}

#[derive(Clone)]
pub struct Config {
    pub service_name: String,
    pub http_port: u16,
    pub prometheus_url: String,
    pub loki_url: String,
    pub tempo_url: String,
    pub nats_monitor_url: Option<String>,
    pub postgres_tcp_addr: Option<String>,
    pub redis_tcp_addr: Option<String>,
    pub telegram_webhook_url: Option<String>,
    pub otel_endpoint: Option<String>,

    pub poll_interval_secs: u64,
    pub monitored_services: Vec<String>,
    pub zscore_threshold: f64,
    pub ewma_alpha: f64,
    pub ewma_dev_threshold: f64,
    pub rolling_window: usize,
    pub warmup_samples: usize,
    pub ignore_initially_down: bool,
    pub http_timeout_secs: u64,

    pub llm_mode: LlmMode,
    pub llm_model: String,
    pub anthropic_api_key: Option<String>,
    pub anthropic_base_url: String,
    pub llm_daily_max_calls: u32,
    pub llm_daily_budget_usd: f64,
    pub llm_cache_ttl_secs: u64,

    pub dry_run: bool,
    pub auto_remediate: bool,
    pub adapter_kind: AdapterKind,
    pub allowed_services: HashSet<String>,
    pub max_actions_per_hour: u32,

    pub docker_socket: String,
    pub docker_host: Option<String>,
    pub docker_swarm_mode: bool,

    pub kube_api_url: String,
    pub kube_token: Option<String>,
    pub kube_namespace: String,
}

impl fmt::Debug for Config {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Config")
            .field("service_name", &self.service_name)
            .field("http_port", &self.http_port)
            .field("prometheus_url", &self.prometheus_url)
            .field("loki_url", &self.loki_url)
            .field("tempo_url", &self.tempo_url)
            .field("nats_monitor_url", &self.nats_monitor_url)
            .field("postgres_tcp_addr", &self.postgres_tcp_addr)
            .field("redis_tcp_addr", &self.redis_tcp_addr)
            .field("telegram_webhook_url", &self.telegram_webhook_url)
            .field("otel_endpoint", &self.otel_endpoint)
            .field("poll_interval_secs", &self.poll_interval_secs)
            .field("monitored_services", &self.monitored_services)
            .field("zscore_threshold", &self.zscore_threshold)
            .field("ewma_alpha", &self.ewma_alpha)
            .field("ewma_dev_threshold", &self.ewma_dev_threshold)
            .field("rolling_window", &self.rolling_window)
            .field("warmup_samples", &self.warmup_samples)
            .field("ignore_initially_down", &self.ignore_initially_down)
            .field("http_timeout_secs", &self.http_timeout_secs)
            .field("llm_mode", &self.llm_mode)
            .field("llm_model", &self.llm_model)
            .field(
                "anthropic_api_key",
                &self.anthropic_api_key.as_deref().map(mask_secret),
            )
            .field("anthropic_base_url", &self.anthropic_base_url)
            .field("llm_daily_max_calls", &self.llm_daily_max_calls)
            .field("llm_daily_budget_usd", &self.llm_daily_budget_usd)
            .field("llm_cache_ttl_secs", &self.llm_cache_ttl_secs)
            .field("dry_run", &self.dry_run)
            .field("auto_remediate", &self.auto_remediate)
            .field("adapter_kind", &self.adapter_kind)
            .field("allowed_services", &self.allowed_services)
            .field("max_actions_per_hour", &self.max_actions_per_hour)
            .field("docker_socket", &self.docker_socket)
            .field("docker_host", &self.docker_host)
            .field("docker_swarm_mode", &self.docker_swarm_mode)
            .field("kube_api_url", &self.kube_api_url)
            .field("kube_token", &self.kube_token.as_deref().map(mask_secret))
            .field("kube_namespace", &self.kube_namespace)
            .finish()
    }
}

impl Default for Config {
    fn default() -> Self {
        let services: Vec<String> = DEFAULT_MONITORED_SERVICES
            .iter()
            .map(|s| (*s).to_string())
            .collect();
        let allowed_services: HashSet<String> = services.iter().cloned().collect();
        Self {
            service_name: "aiops".to_string(),
            http_port: 8092,
            prometheus_url: "http://prometheus:9090".to_string(),
            loki_url: "http://loki:3100".to_string(),
            tempo_url: "http://tempo:3200".to_string(),
            nats_monitor_url: Some("http://nats:8222/healthz".to_string()),
            postgres_tcp_addr: None,
            redis_tcp_addr: None,
            telegram_webhook_url: Some("http://telegram-relay:8099/webhook".to_string()),
            otel_endpoint: None,

            poll_interval_secs: 10,
            monitored_services: services,
            zscore_threshold: 3.0,
            ewma_alpha: 0.3,
            ewma_dev_threshold: 2.5,
            rolling_window: 60,
            warmup_samples: 3,
            ignore_initially_down: true,
            http_timeout_secs: 5,

            llm_mode: LlmMode::Mock,
            llm_model: "claude-opus-5-5".to_string(),
            anthropic_api_key: None,
            anthropic_base_url: "https://api.anthropic.com".to_string(),
            llm_daily_max_calls: 100,
            llm_daily_budget_usd: 5.0,
            llm_cache_ttl_secs: 300,

            dry_run: true,
            auto_remediate: false,
            adapter_kind: AdapterKind::Docker,
            allowed_services,
            max_actions_per_hour: 3,

            docker_socket: "/var/run/docker.sock".to_string(),
            docker_host: None,
            docker_swarm_mode: false,

            kube_api_url: "https://kubernetes.default.svc".to_string(),
            kube_token: None,
            kube_namespace: "default".to_string(),
        }
    }
}

impl Config {
    pub fn from_env() -> Self {
        let mut cfg = Self::default();

        if let Some(v) = env_non_empty("SERVICE_NAME") {
            cfg.service_name = v;
        }
        if let Some(v) = env_non_empty("AIOPS_HTTP_PORT").or_else(|| env_non_empty("HTTP_PORT")) {
            if let Ok(port) = v.parse() {
                cfg.http_port = port;
            }
        }
        if let Some(v) = env_non_empty("PROMETHEUS_URL") {
            cfg.prometheus_url = v.trim_end_matches('/').to_string();
        }
        if let Some(v) = env_non_empty("LOKI_URL") {
            cfg.loki_url = v.trim_end_matches('/').to_string();
        }
        if let Some(v) = env_non_empty("TEMPO_URL") {
            cfg.tempo_url = v.trim_end_matches('/').to_string();
        }
        if let Ok(v) = env::var("NATS_MONITOR_URL") {
            let trimmed = v.trim();
            cfg.nats_monitor_url = if trimmed.is_empty() {
                None
            } else {
                Some(trimmed.to_string())
            };
        }
        cfg.postgres_tcp_addr = env_non_empty("POSTGRES_TCP_ADDR");
        cfg.redis_tcp_addr = env_non_empty("REDIS_TCP_ADDR");
        if let Ok(v) = env::var("TELEGRAM_WEBHOOK_URL").or_else(|_| env::var("TELEGRAM_RELAY_URL"))
        {
            let trimmed = v.trim();
            cfg.telegram_webhook_url = if trimmed.is_empty() {
                None
            } else {
                Some(trimmed.to_string())
            };
        }
        cfg.otel_endpoint = env_non_empty("OTEL_EXPORTER_OTLP_ENDPOINT");

        if let Some(v) = env_parse::<u64>("AIOPS_POLL_INTERVAL_SECS") {
            cfg.poll_interval_secs = v.max(1);
        }
        if let Some(v) = env_non_empty("AIOPS_MONITORED_SERVICES") {
            let list = parse_csv(&v);
            if !list.is_empty() {
                cfg.monitored_services = list;
            }
        }
        if let Some(v) = env_parse::<f64>("AIOPS_ZSCORE_THRESHOLD") {
            if v.is_finite() && v > 0.0 {
                cfg.zscore_threshold = v;
            }
        }
        if let Some(v) = env_parse::<f64>("AIOPS_EWMA_ALPHA") {
            if v.is_finite() && v > 0.0 && v <= 1.0 {
                cfg.ewma_alpha = v;
            }
        }
        if let Some(v) = env_parse::<f64>("AIOPS_EWMA_DEV_THRESHOLD") {
            if v.is_finite() && v > 0.0 {
                cfg.ewma_dev_threshold = v;
            }
        }
        if let Some(v) = env_parse::<usize>("AIOPS_ROLLING_WINDOW") {
            cfg.rolling_window = v.clamp(5, 1000);
        }
        if let Some(v) = env_parse::<usize>("AIOPS_WARMUP_SAMPLES") {
            cfg.warmup_samples = v.clamp(1, 30);
        }
        if let Some(v) = env_bool("AIOPS_IGNORE_INITIALLY_DOWN") {
            cfg.ignore_initially_down = v;
        }
        if let Some(v) = env_parse::<u64>("AIOPS_HTTP_TIMEOUT_SECS") {
            cfg.http_timeout_secs = v.clamp(1, 60);
        }

        if let Some(v) = env_non_empty("LLM_MODE") {
            cfg.llm_mode = match v.to_ascii_lowercase().as_str() {
                "anthropic" => LlmMode::Anthropic,
                _ => LlmMode::Mock,
            };
        }
        if let Some(v) = env_non_empty("LLM_MODEL") {
            cfg.llm_model = v;
        }
        cfg.anthropic_api_key = env_non_empty("ANTHROPIC_API_KEY");
        if let Some(v) = env_non_empty("ANTHROPIC_BASE_URL") {
            cfg.anthropic_base_url = v.trim_end_matches('/').to_string();
        }
        if let Some(v) = env_parse::<u32>("LLM_DAILY_MAX_CALLS") {
            cfg.llm_daily_max_calls = v;
        }
        if let Some(v) = env_parse::<f64>("LLM_DAILY_BUDGET_USD") {
            if v.is_finite() && v >= 0.0 {
                cfg.llm_daily_budget_usd = v;
            }
        }
        if let Some(v) = env_parse::<u64>("LLM_CACHE_TTL_SECS") {
            cfg.llm_cache_ttl_secs = v;
        }

        if let Some(v) = env_bool("AIOPS_DRY_RUN") {
            cfg.dry_run = v;
        }
        if let Some(v) = env_bool("AIOPS_AUTO_REMEDIATE") {
            cfg.auto_remediate = v;
        }
        if let Some(v) = env_non_empty("AIOPS_ADAPTER") {
            cfg.adapter_kind = match v.to_ascii_lowercase().as_str() {
                "kubernetes" | "k8s" => AdapterKind::Kubernetes,
                _ => AdapterKind::Docker,
            };
        }
        if let Some(v) = env_non_empty("AIOPS_ALLOWED_SERVICES") {
            cfg.allowed_services = parse_csv(&v).into_iter().collect();
        }
        if let Some(v) = env_parse::<u32>("AIOPS_MAX_ACTIONS_PER_HOUR") {
            cfg.max_actions_per_hour = v;
        }

        if let Some(v) = env_non_empty("DOCKER_SOCKET") {
            cfg.docker_socket = v;
        }
        cfg.docker_host = env_non_empty("DOCKER_HOST")
            .filter(|h| h.starts_with("http://") || h.starts_with("https://"))
            .map(|h| h.trim_end_matches('/').to_string());
        if let Some(v) = env_bool("DOCKER_SWARM_MODE") {
            cfg.docker_swarm_mode = v;
        }

        if let Some(v) = env_non_empty("KUBE_API_URL") {
            cfg.kube_api_url = v.trim_end_matches('/').to_string();
        }
        cfg.kube_token = env_non_empty("KUBE_TOKEN");
        if let Some(v) = env_non_empty("KUBE_NAMESPACE") {
            cfg.kube_namespace = v;
        }

        cfg
    }
}

fn env_non_empty(key: &str) -> Option<String> {
    env::var(key)
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
}

fn env_parse<T: std::str::FromStr>(key: &str) -> Option<T> {
    env_non_empty(key).and_then(|s| s.parse::<T>().ok())
}

fn env_bool(key: &str) -> Option<bool> {
    env_non_empty(key).and_then(|s| match s.to_ascii_lowercase().as_str() {
        "1" | "true" | "yes" | "on" => Some(true),
        "0" | "false" | "no" | "off" => Some(false),
        _ => None,
    })
}

fn parse_csv(raw: &str) -> Vec<String> {
    raw.split(',')
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .collect()
}
