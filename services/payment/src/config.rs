use std::env;
use std::fmt;
use std::time::Duration;

use crate::domain::{PROVIDER_ZALOPAY_MOCK, PROVIDER_ZALOPAY_SANDBOX};
use crate::telemetry::mask_secret;

pub const DEFAULT_ZALOPAY_SANDBOX_BASE_URL: &str = "https://sb-openapi.zalopay.vn";
pub const DEFAULT_ZALOPAY_HTTP_TIMEOUT_SECS: u64 = 10;
/// Dev-only fallback signing keys permitted strictly when `ZALOPAY_MODE=mock` and env keys are
/// unset, allowing QA/E2E tools to sign mock callbacks deterministically. Never used in `sandbox`.
pub const DEV_ONLY_ZALOPAY_MOCK_KEY1: &str = "dev-only-zalopay-mock-key1";
pub const DEV_ONLY_ZALOPAY_MOCK_KEY2: &str = "dev-only-zalopay-mock-key2";
pub const DEFAULT_ZALOPAY_MOCK_APP_ID: i32 = 1;
pub const DEFAULT_LARGE_REFUND_APPROVAL_VND: i64 = 5_000_000;
pub const DEFAULT_ZALOPAY_REFUND_POLL_INTERVAL_SECS: u64 = 15;
pub const DEFAULT_ZALOPAY_LATE_SWEEP_WINDOW_HOURS: u64 = 24;
pub const DEFAULT_ZALOPAY_LATE_SWEEP_BATCH_LIMIT: i64 = 25;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ZaloPayMode {
    Mock,
    Sandbox,
}

impl ZaloPayMode {
    /// Parses `ZALOPAY_MODE`. Only `mock` (default when empty) and `sandbox` are permitted (F1).
    pub fn try_parse(raw: &str) -> Result<Self, String> {
        let trimmed = raw.trim().to_ascii_lowercase();
        match trimmed.as_str() {
            "" | "mock" => Ok(Self::Mock),
            "sandbox" => Ok(Self::Sandbox),
            other => Err(format!(
                "invalid ZALOPAY_MODE '{other}': only 'mock' and 'sandbox' are supported"
            )),
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::Mock => "mock",
            Self::Sandbox => "sandbox",
        }
    }

    pub fn provider_name(self) -> &'static str {
        match self {
            Self::Mock => PROVIDER_ZALOPAY_MOCK,
            Self::Sandbox => PROVIDER_ZALOPAY_SANDBOX,
        }
    }
}

#[derive(Clone)]
pub struct ZaloPayConfig {
    pub mode: ZaloPayMode,
    pub base_url: String,
    pub app_id: Option<i32>,
    pub key1: String,
    pub key2: String,
    pub callback_url: String,
    pub redirect_url: String,
    pub http_timeout: Duration,
    pub max_retries: u32,
    parse_error: Option<String>,
}

impl fmt::Debug for ZaloPayConfig {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ZaloPayConfig")
            .field("mode", &self.mode)
            .field("base_url", &self.base_url)
            .field("app_id", &self.app_id)
            .field("key1", &mask_secret(&self.key1))
            .field("key2", &mask_secret(&self.key2))
            .field("callback_url", &self.callback_url)
            .field("redirect_url", &self.redirect_url)
            .field("http_timeout", &self.http_timeout)
            .field("max_retries", &self.max_retries)
            .field("credentials_configured", &self.credentials_configured())
            .finish()
    }
}

impl ZaloPayConfig {
    /// Reads ZaloPay configuration from process environment variables and validates it.
    pub fn from_env() -> Result<Self, String> {
        Self::from_lookup(|k| env::var(k).ok())
    }

    /// Reads ZaloPay configuration from a custom lookup closure (used by unit tests without
    /// mutating process-global environment variables).
    pub fn from_lookup<F>(lookup: F) -> Result<Self, String>
    where
        F: Fn(&str) -> Option<String>,
    {
        let cfg = Self::from_lookup_unchecked(lookup);
        cfg.validate()?;
        Ok(cfg)
    }

    fn from_lookup_unchecked<F>(lookup: F) -> Self
    where
        F: Fn(&str) -> Option<String>,
    {
        let mut parse_error: Option<String> = None;

        let raw_mode = lookup("ZALOPAY_MODE").unwrap_or_default();
        let mode = match ZaloPayMode::try_parse(&raw_mode) {
            Ok(m) => m,
            Err(e) => {
                parse_error = Some(e);
                ZaloPayMode::Mock
            }
        };

        let raw_app_id = lookup("ZALOPAY_APP_ID")
            .map(|v| v.trim().to_string())
            .unwrap_or_default();
        let app_id = if raw_app_id.is_empty() {
            None
        } else {
            match raw_app_id.parse::<i32>() {
                Ok(id) if id > 0 => Some(id),
                _ => {
                    if parse_error.is_none() {
                        parse_error = Some(
                            "invalid ZALOPAY_APP_ID: must be a positive 32-bit integer".to_string(),
                        );
                    }
                    None
                }
            }
        };

        let base_url = lookup("ZALOPAY_BASE_URL")
            .map(|v| v.trim().to_string())
            .filter(|v| !v.is_empty())
            .unwrap_or_else(|| DEFAULT_ZALOPAY_SANDBOX_BASE_URL.to_string());

        let key1 = lookup("ZALOPAY_KEY1")
            .map(|v| v.trim().to_string())
            .unwrap_or_default();
        let key2 = lookup("ZALOPAY_KEY2")
            .map(|v| v.trim().to_string())
            .unwrap_or_default();
        let callback_url = lookup("ZALOPAY_CALLBACK_URL")
            .map(|v| v.trim().to_string())
            .unwrap_or_default();
        let redirect_url = lookup("ZALOPAY_REDIRECT_URL")
            .map(|v| v.trim().to_string())
            .unwrap_or_default();

        let timeout_secs = lookup("ZALOPAY_HTTP_TIMEOUT_SECS")
            .and_then(|v| v.trim().parse::<u64>().ok())
            .filter(|&v| v > 0)
            .unwrap_or(DEFAULT_ZALOPAY_HTTP_TIMEOUT_SECS);
        let max_retries = lookup("EXTERNAL_MAX_RETRIES")
            .and_then(|v| v.trim().parse::<u32>().ok())
            .unwrap_or(3);

        Self {
            mode,
            base_url,
            app_id,
            key1,
            key2,
            callback_url,
            redirect_url,
            http_timeout: Duration::from_secs(timeout_secs),
            max_retries,
            parse_error,
        }
    }

    /// Validates configuration invariants. In `sandbox` mode, `ZALOPAY_APP_ID`, `ZALOPAY_KEY1`,
    /// `ZALOPAY_KEY2`, and an `http(s)://` `ZALOPAY_BASE_URL` are mandatory (F1).
    pub fn validate(&self) -> Result<(), String> {
        if let Some(ref err) = self.parse_error {
            return Err(err.clone());
        }
        if self.mode == ZaloPayMode::Sandbox {
            let mut missing = Vec::new();
            if !self.app_id.is_some_and(|id| id > 0) {
                missing.push("ZALOPAY_APP_ID");
            }
            if self.key1.trim().is_empty() {
                missing.push("ZALOPAY_KEY1");
            }
            if self.key2.trim().is_empty() {
                missing.push("ZALOPAY_KEY2");
            }
            if !missing.is_empty() {
                return Err(format!(
                    "ZALOPAY_MODE=sandbox requires environment variables: {}",
                    missing.join(", ")
                ));
            }
            let base = self.base_url.trim();
            if !(base.starts_with("https://") || base.starts_with("http://")) {
                return Err(
                    "ZALOPAY_BASE_URL must start with https:// or http:// when ZALOPAY_MODE=sandbox"
                        .to_string(),
                );
            }
        }
        Ok(())
    }

    /// Returns true when all explicit environment credentials (`app_id`, `key1`, `key2`) are populated.
    pub fn credentials_configured(&self) -> bool {
        self.app_id.is_some_and(|id| id > 0)
            && !self.key1.trim().is_empty()
            && !self.key2.trim().is_empty()
    }

    /// Returns the effective `app_id`: configured positive `ZALOPAY_APP_ID`, or the deterministic
    /// mock fallback strictly in `ZaloPayMode::Mock`. Never falls back in `sandbox`.
    pub fn effective_app_id(&self) -> Option<i32> {
        match self.app_id {
            Some(id) if id > 0 => Some(id),
            _ if self.mode == ZaloPayMode::Mock => Some(DEFAULT_ZALOPAY_MOCK_APP_ID),
            _ => None,
        }
    }

    /// Returns the effective `key1` for outbound MAC signing: configured `ZALOPAY_KEY1`, or
    /// `dev-only-zalopay-mock-key1` strictly when `ZALOPAY_MODE=mock` and env is empty.
    pub fn effective_key1(&self) -> &str {
        if !self.key1.trim().is_empty() {
            self.key1.as_str()
        } else if self.mode == ZaloPayMode::Mock {
            DEV_ONLY_ZALOPAY_MOCK_KEY1
        } else {
            ""
        }
    }

    /// Returns the effective `key2` for callback MAC verification: configured `ZALOPAY_KEY2`, or
    /// `dev-only-zalopay-mock-key2` strictly when `ZALOPAY_MODE=mock` and env is empty.
    pub fn effective_key2(&self) -> &str {
        if !self.key2.trim().is_empty() {
            self.key2.as_str()
        } else if self.mode == ZaloPayMode::Mock {
            DEV_ONLY_ZALOPAY_MOCK_KEY2
        } else {
            ""
        }
    }

    /// Returns a safe, masked credential summary suitable for admin views/logs.
    pub fn credential_preview(&self) -> String {
        if !self.credentials_configured() {
            return String::new();
        }
        format!(
            "app_id={} key1={} key2={}",
            self.app_id.unwrap_or_default(),
            mask_secret(&self.key1),
            mask_secret(&self.key2)
        )
    }

    /// Builds a default in-process mock configuration with no real/public keys.
    pub fn mock_default() -> Self {
        Self::from_lookup_unchecked(|_| None)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum VietQrMode {
    Mock,
    PayOs,
    Sepay,
}

impl VietQrMode {
    pub fn parse(raw: &str) -> Self {
        match raw.trim().to_ascii_lowercase().as_str() {
            "payos" => Self::PayOs,
            "sepay" => Self::Sepay,
            _ => Self::Mock,
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::Mock => "vietqr_mock",
            Self::PayOs => "payos",
            Self::Sepay => "sepay",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AppEnv {
    Development,
    Staging,
    Production,
}

impl AppEnv {
    pub fn parse(raw: &str) -> Self {
        match raw.trim().to_ascii_lowercase().as_str() {
            "production" | "prod" => Self::Production,
            "staging" | "stage" => Self::Staging,
            _ => Self::Development,
        }
    }

    pub fn is_production(self) -> bool {
        self == Self::Production
    }
}

pub const DEFAULT_WEBHOOK_SECRET: &str = "dev-webhook-secret-change-me";
pub const DEFAULT_ZALOPAY_MOCK_KEY1: &str = "zalopay-mock-key1-dev";
pub const DEFAULT_ZALOPAY_MOCK_KEY2: &str = "zalopay-mock-key2-dev";

/// Known non-secret dev/mock/example constants committed in this repository
/// (`.env.example`, `docker-compose.yml`, and default mock fallbacks).
const KNOWN_REPO_SECRETS: &[&str] = &[
    DEFAULT_WEBHOOK_SECRET,
    "dev-only-payment-webhook-hmac-secret",
    DEFAULT_ZALOPAY_MOCK_KEY1,
    DEFAULT_ZALOPAY_MOCK_KEY2,
    "sandbox-client-id",
    "sandbox-api-key",
    "sandbox-checksum-key",
    "sandbox-sepay-token",
];

/// Returns true when a secret/key is empty, clearly a template placeholder,
/// or a known dev/mock constant committed in this repository.
pub fn is_placeholder_secret(raw: &str) -> bool {
    let lower = raw.trim().to_ascii_lowercase();
    lower.is_empty()
        || KNOWN_REPO_SECRETS.contains(&lower.as_str())
        || lower.starts_with("dev-only-")
        || lower.contains("placeholder")
        || lower.contains("change-me")
        || lower.contains("changeme")
        || lower.starts_with("your_")
        || lower.starts_with("your-")
        || lower.starts_with('<')
}

#[derive(Clone)]
pub struct Config {
    pub app_env: AppEnv,
    pub service_name: String,
    pub http_port: u16,
    pub grpc_port: u16,
    pub database_url: String,
    pub user_grpc_addr: String,
    pub nats_url: String,
    pub nats_stream: String,
    pub otel_endpoint: Option<String>,
    pub webhook_secret: String,
    /// HS256 key of end-user access tokens (shared with the Go services).
    pub jwt_secret: String,
    /// HS256 key of internal service tokens (distinct from jwt_secret).
    pub service_jwt_secret: String,
    pub webhook_replay_window_secs: i64,
    pub vietqr_mode: VietQrMode,
    pub vietqr_expiry_secs: i64,
    pub vietqr_bank_bin: String,
    pub vietqr_account_no: String,
    pub vietqr_account_name: String,
    pub payos_base_url: String,
    pub payos_client_id: String,
    pub payos_api_key: String,
    pub payos_checksum_key: String,
    pub sepay_base_url: String,
    pub sepay_api_token: String,
    pub sepay_bank_code: String,
    pub external_http_timeout: Duration,
    pub external_max_retries: u32,
    pub expiration_scan_interval: Duration,
    pub outbox_poll_interval: Duration,
    pub reconciliation_interval: Duration,
    pub seller_hold_hours: i64,
    pub escrow_hold_scan_interval: Duration,
    pub large_refund_approval_vnd: i64,
    pub zalopay_refund_poll_interval: Duration,
    pub zalopay_late_sweep_window: Duration,
    pub zalopay_late_sweep_batch_limit: i64,
    pub zalopay: ZaloPayConfig,
}

/// Custom Debug implementation that redacts all secrets and credentials.
impl fmt::Debug for Config {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Config")
            .field("app_env", &self.app_env)
            .field("service_name", &self.service_name)
            .field("http_port", &self.http_port)
            .field("grpc_port", &self.grpc_port)
            .field("database_url", &"[REDACTED]")
            .field("user_grpc_addr", &self.user_grpc_addr)
            .field(
                "nats_url",
                &crate::telemetry::redact_url_credentials(&self.nats_url),
            )
            .field("nats_stream", &self.nats_stream)
            .field("otel_endpoint", &self.otel_endpoint)
            .field("webhook_secret", &"[REDACTED]")
            .field("jwt_secret", &"[REDACTED]")
            .field("service_jwt_secret", &"[REDACTED]")
            .field(
                "webhook_replay_window_secs",
                &self.webhook_replay_window_secs,
            )
            .field("vietqr_mode", &self.vietqr_mode)
            .field("vietqr_expiry_secs", &self.vietqr_expiry_secs)
            .field("payos_base_url", &self.payos_base_url)
            .field("payos_client_id", &"[REDACTED]")
            .field("payos_api_key", &"[REDACTED]")
            .field("payos_checksum_key", &"[REDACTED]")
            .field("sepay_base_url", &self.sepay_base_url)
            .field("sepay_api_token", &"[REDACTED]")
            .field("external_http_timeout", &self.external_http_timeout)
            .field("external_max_retries", &self.external_max_retries)
            .field("expiration_scan_interval", &self.expiration_scan_interval)
            .field("outbox_poll_interval", &self.outbox_poll_interval)
            .field("reconciliation_interval", &self.reconciliation_interval)
            .field("seller_hold_hours", &self.seller_hold_hours)
            .field("escrow_hold_scan_interval", &self.escrow_hold_scan_interval)
            .field("large_refund_approval_vnd", &self.large_refund_approval_vnd)
            .field(
                "zalopay_refund_poll_interval",
                &self.zalopay_refund_poll_interval,
            )
            .field("zalopay_late_sweep_window", &self.zalopay_late_sweep_window)
            .field(
                "zalopay_late_sweep_batch_limit",
                &self.zalopay_late_sweep_batch_limit,
            )
            .field("zalopay", &self.zalopay)
            .finish()
    }
}

impl Config {
    pub fn from_env() -> Self {
        let app_env = AppEnv::parse(&env_string("APP_ENV", "development"));
        let webhook_secret = if app_env.is_production() {
            env_string("PAYMENT_WEBHOOK_SECRET", "")
        } else {
            env_string("PAYMENT_WEBHOOK_SECRET", DEFAULT_WEBHOOK_SECRET)
        };

        Self {
            app_env,
            service_name: env_string("SERVICE_NAME", "payment"),
            http_port: env_u16("PAYMENT_HTTP_PORT", env_u16("HTTP_PORT", 8086)),
            grpc_port: env_u16("PAYMENT_GRPC_PORT", env_u16("GRPC_PORT", 50056)),
            database_url: env_string(
                "DATABASE_URL",
                "postgres://nexusmart:nexusmart@localhost:5432/payment_db",
            ),
            user_grpc_addr: normalize_grpc_endpoint(&env_string(
                "USER_GRPC_ADDR",
                "http://user:50051",
            )),
            nats_url: env_string("NATS_URL", "nats://localhost:4222"),
            nats_stream: env_string("NATS_STREAM", "NEXUSMART"),
            otel_endpoint: env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            webhook_secret,
            jwt_secret: env_string("JWT_SECRET", ""),
            service_jwt_secret: env_string("SERVICE_JWT_SECRET", ""),
            webhook_replay_window_secs: env_i64("WEBHOOK_REPLAY_WINDOW_SECS", 300),
            vietqr_mode: VietQrMode::parse(&env_string("VIETQR_MODE", "mock")),
            vietqr_expiry_secs: env::var("VIETQR_EXPIRY_SECS")
                .ok()
                .and_then(|v| v.parse::<i64>().ok())
                .unwrap_or_else(|| env_i64("PAYMENT_EXPIRY_MINUTES", 15).saturating_mul(60)),
            vietqr_bank_bin: env_string("VIETQR_BANK_BIN", "970422"),
            vietqr_account_no: env_string("VIETQR_ACCOUNT_NO", "0001234567890"),
            vietqr_account_name: env_string("VIETQR_ACCOUNT_NAME", "NEXUSMART SANDBOX"),
            payos_base_url: env_string("PAYOS_BASE_URL", "https://api-merchant.payos.vn"),
            payos_client_id: env_string("PAYOS_CLIENT_ID", ""),
            payos_api_key: env_string("PAYOS_API_KEY", ""),
            payos_checksum_key: env_string("PAYOS_CHECKSUM_KEY", ""),
            sepay_base_url: env_string("SEPAY_BASE_URL", "https://my.sepay.vn"),
            sepay_api_token: env_string("SEPAY_API_TOKEN", ""),
            sepay_bank_code: env_string("SEPAY_BANK_CODE", "MBBank"),
            external_http_timeout: Duration::from_secs(env_u64("EXTERNAL_HTTP_TIMEOUT_SECS", 5)),
            external_max_retries: env_u32("EXTERNAL_MAX_RETRIES", 3),
            expiration_scan_interval: Duration::from_secs(env_u64(
                "EXPIRATION_SCAN_INTERVAL_SECS",
                15,
            )),
            outbox_poll_interval: Duration::from_millis(env_u64("OUTBOX_POLL_INTERVAL_MS", 500)),
            reconciliation_interval: Duration::from_secs(env_u64(
                "RECONCILIATION_INTERVAL_SECS",
                86_400,
            )),
            seller_hold_hours: env_i64("PAYMENT_SELLER_HOLD_HOURS", 72).max(0),
            escrow_hold_scan_interval: Duration::from_secs(
                env_u64("ESCROW_HOLD_SCAN_INTERVAL_SECS", 60).max(1),
            ),
            large_refund_approval_vnd: env_i64(
                "PAYMENT_LARGE_REFUND_APPROVAL_VND",
                DEFAULT_LARGE_REFUND_APPROVAL_VND,
            )
            .max(1),
            zalopay_refund_poll_interval: Duration::from_secs(
                env_u64(
                    "ZALOPAY_REFUND_POLL_INTERVAL_SECS",
                    DEFAULT_ZALOPAY_REFUND_POLL_INTERVAL_SECS,
                )
                .max(1),
            ),
            zalopay_late_sweep_window: Duration::from_secs(
                env_u64(
                    "ZALOPAY_LATE_SWEEP_WINDOW_HOURS",
                    DEFAULT_ZALOPAY_LATE_SWEEP_WINDOW_HOURS,
                )
                .max(1)
                .saturating_mul(3600),
            ),
            zalopay_late_sweep_batch_limit: env_i64(
                "ZALOPAY_LATE_SWEEP_BATCH_LIMIT",
                DEFAULT_ZALOPAY_LATE_SWEEP_BATCH_LIMIT,
            )
            .clamp(1, 200),
            zalopay: ZaloPayConfig::from_lookup_unchecked(|k| env::var(k).ok()),
        }
    }

    /// Reads configuration from environment variables and validates mandatory startup invariants
    /// (ZaloPay sandbox requirements and fail-fast production guards).
    pub fn try_from_env() -> Result<Self, String> {
        let cfg = Self::from_env();
        cfg.validate()?;
        Ok(cfg)
    }

    pub fn is_production(&self) -> bool {
        self.app_env.is_production()
    }

    /// Enforces startup configuration invariants:
    /// 1. Always validates `ZaloPayConfig` (`ZALOPAY_MODE=sandbox` requires `app_id`, `key1`, `key2`, `base_url`).
    /// 2. When `APP_ENV=production`, forbids mock modes, placeholder/repo-committed secrets, and
    ///    reused `JWT_SECRET == SERVICE_JWT_SECRET`.
    pub fn validate(&self) -> Result<(), String> {
        self.zalopay.validate()?;

        if !self.is_production() {
            return Ok(());
        }

        if self.zalopay.mode == ZaloPayMode::Mock {
            return Err(
                "ZALOPAY_MODE must be 'sandbox' (mock mode is forbidden in production)".to_string(),
            );
        }
        if is_placeholder_secret(&self.zalopay.key1) {
            return Err(
                "ZALOPAY_KEY1 must be set to a non-placeholder secret in production".to_string(),
            );
        }
        if is_placeholder_secret(&self.zalopay.key2) {
            return Err(
                "ZALOPAY_KEY2 must be set to a non-placeholder secret in production".to_string(),
            );
        }

        if is_placeholder_secret(&self.webhook_secret) {
            return Err(
                "PAYMENT_WEBHOOK_SECRET must be set to a non-placeholder secret in production"
                    .to_string(),
            );
        }
        match self.vietqr_mode {
            VietQrMode::Mock => {
                return Err(
                    "VIETQR_MODE must be 'payos' or 'sepay' (mock mode is forbidden in production)"
                        .to_string(),
                );
            }
            VietQrMode::PayOs => {
                if is_placeholder_secret(&self.payos_client_id)
                    || is_placeholder_secret(&self.payos_api_key)
                    || is_placeholder_secret(&self.payos_checksum_key)
                {
                    return Err(
                        "PAYOS_CLIENT_ID, PAYOS_API_KEY, and PAYOS_CHECKSUM_KEY must be non-placeholder secrets in production"
                            .to_string(),
                    );
                }
            }
            VietQrMode::Sepay => {
                if is_placeholder_secret(&self.sepay_api_token) {
                    return Err(
                        "SEPAY_API_TOKEN must be set to a non-placeholder secret in production"
                            .to_string(),
                    );
                }
            }
        }

        if is_placeholder_secret(&self.jwt_secret)
            || is_placeholder_secret(&self.service_jwt_secret)
        {
            return Err(
                "JWT_SECRET and SERVICE_JWT_SECRET must be non-placeholder secrets in production"
                    .to_string(),
            );
        }
        if self.jwt_secret == self.service_jwt_secret {
            return Err("SERVICE_JWT_SECRET must differ from JWT_SECRET in production".to_string());
        }

        Ok(())
    }
}

fn normalize_grpc_endpoint(raw: &str) -> String {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return "http://user:50051".to_string();
    }
    if trimmed.starts_with("http://") || trimmed.starts_with("https://") {
        trimmed.to_string()
    } else {
        format!("http://{trimmed}")
    }
}

fn env_string(key: &str, default: &str) -> String {
    env::var(key)
        .ok()
        .filter(|v| !v.trim().is_empty())
        .unwrap_or_else(|| default.to_string())
}

fn env_u16(key: &str, default: u16) -> u16 {
    env::var(key)
        .ok()
        .and_then(|v| v.parse::<u16>().ok())
        .unwrap_or(default)
}

fn env_u32(key: &str, default: u32) -> u32 {
    env::var(key)
        .ok()
        .and_then(|v| v.parse::<u32>().ok())
        .unwrap_or(default)
}

fn env_u64(key: &str, default: u64) -> u64 {
    env::var(key)
        .ok()
        .and_then(|v| v.parse::<u64>().ok())
        .unwrap_or(default)
}

fn env_i64(key: &str, default: i64) -> i64 {
    env::var(key)
        .ok()
        .and_then(|v| v.parse::<i64>().ok())
        .unwrap_or(default)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    fn lookup_from(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<String> {
        let map: HashMap<String, String> = pairs
            .iter()
            .map(|(k, v)| ((*k).to_string(), (*v).to_string()))
            .collect();
        move |k: &str| map.get(k).cloned()
    }

    #[test]
    fn test_zalopay_config_defaults_to_mock_without_keys() {
        let cfg = ZaloPayConfig::from_lookup(lookup_from(&[])).expect("default mock config");
        assert_eq!(cfg.mode, ZaloPayMode::Mock);
        assert_eq!(cfg.base_url, DEFAULT_ZALOPAY_SANDBOX_BASE_URL);
        assert_eq!(cfg.app_id, None);
        assert!(cfg.key1.is_empty());
        assert!(cfg.key2.is_empty());
        assert_eq!(
            cfg.http_timeout,
            Duration::from_secs(DEFAULT_ZALOPAY_HTTP_TIMEOUT_SECS)
        );
        assert!(!cfg.credentials_configured());
        assert!(cfg.credential_preview().is_empty());
        assert_eq!(cfg.effective_app_id(), Some(DEFAULT_ZALOPAY_MOCK_APP_ID));
        assert_eq!(cfg.effective_key1(), DEV_ONLY_ZALOPAY_MOCK_KEY1);
        assert_eq!(cfg.effective_key2(), DEV_ONLY_ZALOPAY_MOCK_KEY2);

        // Explicit env keys override dev-only fallbacks in mock mode.
        let cfg_override = ZaloPayConfig::from_lookup(lookup_from(&[
            ("ZALOPAY_MODE", "mock"),
            ("ZALOPAY_APP_ID", "42"),
            ("ZALOPAY_KEY1", "custom-mock-k1"),
            ("ZALOPAY_KEY2", "custom-mock-k2"),
        ]))
        .expect("mock with explicit keys");
        assert_eq!(cfg_override.effective_app_id(), Some(42));
        assert_eq!(cfg_override.effective_key1(), "custom-mock-k1");
        assert_eq!(cfg_override.effective_key2(), "custom-mock-k2");
        assert!(cfg_override.credentials_configured());
    }

    #[test]
    fn test_zalopay_config_sandbox_missing_keys_returns_clear_startup_error() {
        let err = ZaloPayConfig::from_lookup(lookup_from(&[("ZALOPAY_MODE", "sandbox")]))
            .expect_err("sandbox without keys must fail");
        assert!(err.contains("ZALOPAY_APP_ID"), "got: {err}");
        assert!(err.contains("ZALOPAY_KEY1"), "got: {err}");
        assert!(err.contains("ZALOPAY_KEY2"), "got: {err}");

        let err_missing_key2 = ZaloPayConfig::from_lookup(lookup_from(&[
            ("ZALOPAY_MODE", "sandbox"),
            ("ZALOPAY_APP_ID", "100001"),
            ("ZALOPAY_KEY1", "synthetic-key1-placeholder"),
        ]))
        .expect_err("sandbox without key2 must fail");
        assert!(err_missing_key2.contains("ZALOPAY_KEY2"));
        assert!(!err_missing_key2.contains("synthetic-key1-placeholder"));

        let err_bad_mode =
            ZaloPayConfig::from_lookup(lookup_from(&[("ZALOPAY_MODE", "production")]))
                .expect_err("unsupported mode must fail");
        assert!(err_bad_mode.contains("ZALOPAY_MODE"));

        let err_bad_app_id = ZaloPayConfig::from_lookup(lookup_from(&[
            ("ZALOPAY_MODE", "sandbox"),
            ("ZALOPAY_APP_ID", "not-a-number"),
            ("ZALOPAY_KEY1", "synthetic-key1-placeholder"),
            ("ZALOPAY_KEY2", "synthetic-key2-placeholder"),
        ]))
        .expect_err("non-numeric app_id must fail");
        assert!(err_bad_app_id.contains("ZALOPAY_APP_ID"));
    }

    #[test]
    fn test_zalopay_config_sandbox_valid_and_debug_masks_all_secrets() {
        let secret_k1 = "synthetic-key1-super-secret-value";
        let secret_k2 = "synthetic-key2-super-secret-value";
        let cfg = ZaloPayConfig::from_lookup(lookup_from(&[
            ("ZALOPAY_MODE", "sandbox"),
            ("ZALOPAY_BASE_URL", "https://sb-openapi.zalopay.vn"),
            ("ZALOPAY_APP_ID", "100001"),
            ("ZALOPAY_KEY1", secret_k1),
            ("ZALOPAY_KEY2", secret_k2),
            (
                "ZALOPAY_CALLBACK_URL",
                "https://example.local/webhooks/zalopay",
            ),
            (
                "ZALOPAY_REDIRECT_URL",
                "https://example.local/thanh-toan/ket-qua",
            ),
            ("ZALOPAY_HTTP_TIMEOUT_SECS", "12"),
        ]))
        .expect("valid sandbox config");

        assert_eq!(cfg.mode, ZaloPayMode::Sandbox);
        assert_eq!(cfg.app_id, Some(100_001));
        assert_eq!(cfg.http_timeout, Duration::from_secs(12));
        assert!(cfg.credentials_configured());

        let dbg = format!("{cfg:?}");
        assert!(!dbg.contains(secret_k1), "Debug must not leak key1: {dbg}");
        assert!(!dbg.contains(secret_k2), "Debug must not leak key2: {dbg}");
        assert!(dbg.contains("[REDACTED]"), "Debug must mask secrets: {dbg}");

        let preview = cfg.credential_preview();
        assert!(!preview.contains(secret_k1));
        assert!(!preview.contains(secret_k2));
        assert!(preview.contains("app_id=100001"));
        assert!(preview.contains("[REDACTED]"));
    }

    #[test]
    fn test_vietqr_and_zalopay_mode_parsing() {
        assert_eq!(VietQrMode::parse("payos"), VietQrMode::PayOs);
        assert_eq!(VietQrMode::parse("  SEPAY "), VietQrMode::Sepay);
        assert_eq!(VietQrMode::parse("mock"), VietQrMode::Mock);
        assert_eq!(VietQrMode::parse("unknown"), VietQrMode::Mock);
        assert_eq!(VietQrMode::Mock.as_str(), "vietqr_mock");
        assert_eq!(VietQrMode::PayOs.as_str(), "payos");
        assert_eq!(VietQrMode::Sepay.as_str(), "sepay");

        assert_eq!(
            ZaloPayMode::try_parse("sandbox").unwrap(),
            ZaloPayMode::Sandbox
        );
        assert_eq!(
            ZaloPayMode::try_parse("  SANDBOX ").unwrap(),
            ZaloPayMode::Sandbox
        );
        assert_eq!(ZaloPayMode::try_parse("mock").unwrap(), ZaloPayMode::Mock);
        assert_eq!(ZaloPayMode::try_parse("").unwrap(), ZaloPayMode::Mock);
        assert!(ZaloPayMode::try_parse("other").is_err());
        assert_eq!(ZaloPayMode::Mock.as_str(), "mock");
        assert_eq!(ZaloPayMode::Sandbox.as_str(), "sandbox");
        assert_eq!(ZaloPayMode::Mock.provider_name(), PROVIDER_ZALOPAY_MOCK);
        assert_eq!(
            ZaloPayMode::Sandbox.provider_name(),
            PROVIDER_ZALOPAY_SANDBOX
        );
    }

    #[test]
    fn test_is_placeholder_secret() {
        assert!(is_placeholder_secret(""));
        assert!(is_placeholder_secret("   "));
        assert!(is_placeholder_secret("my-placeholder-key"));
        assert!(is_placeholder_secret("dev-secret-change-me"));
        assert!(is_placeholder_secret("CHANGEME"));
        assert!(is_placeholder_secret("your_api_key"));
        assert!(is_placeholder_secret("your-secret"));
        assert!(is_placeholder_secret("<insert-key>"));
        assert!(is_placeholder_secret(DEFAULT_WEBHOOK_SECRET));
        assert!(is_placeholder_secret(
            "dev-only-payment-webhook-hmac-secret"
        ));
        assert!(is_placeholder_secret(DEV_ONLY_ZALOPAY_MOCK_KEY1));
        assert!(is_placeholder_secret(DEV_ONLY_ZALOPAY_MOCK_KEY2));
        assert!(is_placeholder_secret("DEV-ONLY-CUSTOM-SECRET"));
        assert!(is_placeholder_secret(DEFAULT_ZALOPAY_MOCK_KEY1));
        assert!(is_placeholder_secret(DEFAULT_ZALOPAY_MOCK_KEY2));
        assert!(is_placeholder_secret("sandbox-zalopay-key2-placeholder"));
        assert!(is_placeholder_secret("sandbox-checksum-key"));
        assert!(is_placeholder_secret("sandbox-sepay-token"));
        assert!(!is_placeholder_secret("real-zalopay-key-987654"));
    }

    #[test]
    fn test_app_env_and_production_config_validation() {
        assert_eq!(AppEnv::parse("production"), AppEnv::Production);
        assert_eq!(AppEnv::parse("  PROD "), AppEnv::Production);
        assert_eq!(AppEnv::parse("staging"), AppEnv::Staging);
        assert_eq!(AppEnv::parse("development"), AppEnv::Development);
        assert_eq!(AppEnv::parse(""), AppEnv::Development);

        // Dev/staging defaults remain valid so compose/e2e/mock work out of the box.
        let mut dev_cfg = Config::from_env();
        dev_cfg.app_env = AppEnv::Development;
        dev_cfg.zalopay = ZaloPayConfig::mock_default();
        assert!(dev_cfg.validate().is_ok());

        let mut staging_cfg = dev_cfg.clone();
        staging_cfg.app_env = AppEnv::Staging;
        assert!(staging_cfg.validate().is_ok());

        // Helper for a fully valid production configuration (payOS + ZaloPay sandbox).
        let valid_prod = || {
            let mut c = Config::from_env();
            c.app_env = AppEnv::Production;
            c.zalopay = ZaloPayConfig::mock_default();
            c.zalopay.mode = ZaloPayMode::Sandbox;
            c.zalopay.app_id = Some(2554);
            c.zalopay.key1 = "prod-zalopay-key1-98a76b54c321".to_string();
            c.zalopay.key2 = "prod-zalopay-key2-12d34e56f789".to_string();
            c.vietqr_mode = VietQrMode::PayOs;
            c.webhook_secret = "prod-webhook-hmac-secret-77f88e99a001".to_string();
            c.payos_client_id = "prod-payos-cid-123456".to_string();
            c.payos_api_key = "prod-payos-api-key-123456".to_string();
            c.payos_checksum_key = "prod-payos-checksum-123456".to_string();
            c.jwt_secret = "prod-jwt-secret-aaa111bbb222".to_string();
            c.service_jwt_secret = "prod-service-jwt-secret-ccc333ddd444".to_string();
            c
        };
        assert!(valid_prod().validate().is_ok());

        // Valid production with SePay also passes.
        let mut sepay_prod = valid_prod();
        sepay_prod.vietqr_mode = VietQrMode::Sepay;
        sepay_prod.sepay_api_token = "prod-sepay-token-998877".to_string();
        assert!(sepay_prod.validate().is_ok());

        // Reject mock ZaloPay mode in production.
        let mut bad = valid_prod();
        bad.zalopay.mode = ZaloPayMode::Mock;
        assert!(bad.validate().is_err());

        // Reject empty / repo-default / placeholder ZaloPay key1 & key2 in production.
        for bad_key in [
            "",
            DEV_ONLY_ZALOPAY_MOCK_KEY1,
            DEV_ONLY_ZALOPAY_MOCK_KEY2,
            DEFAULT_ZALOPAY_MOCK_KEY1,
            DEFAULT_ZALOPAY_MOCK_KEY2,
            "sandbox-zalopay-key2-placeholder",
            "change-me-key2",
        ] {
            let mut c1 = valid_prod();
            c1.zalopay.key1 = bad_key.to_string();
            assert!(c1.validate().is_err(), "key1={bad_key}");

            let mut c2 = valid_prod();
            c2.zalopay.key2 = bad_key.to_string();
            assert!(c2.validate().is_err(), "key2={bad_key}");
        }

        // Reject VietQR mock mode (including invalid "sandbox" string parsed as Mock).
        let mut bad_vq = valid_prod();
        bad_vq.vietqr_mode = VietQrMode::parse("sandbox");
        assert_eq!(bad_vq.vietqr_mode, VietQrMode::Mock);
        assert!(bad_vq.validate().is_err());

        // Reject empty / repo-default / placeholder PAYMENT_WEBHOOK_SECRET in production.
        for bad_wh in [
            "",
            DEFAULT_WEBHOOK_SECRET,
            "dev-only-payment-webhook-hmac-secret",
            "placeholder-webhook-secret",
        ] {
            let mut c = valid_prod();
            c.webhook_secret = bad_wh.to_string();
            assert!(c.validate().is_err(), "webhook_secret={bad_wh}");
        }

        // Reject placeholder payOS / SePay credentials in production.
        let mut bad_payos = valid_prod();
        bad_payos.payos_checksum_key = "sandbox-checksum-key".to_string();
        assert!(bad_payos.validate().is_err());

        let mut bad_sepay = sepay_prod.clone();
        bad_sepay.sepay_api_token = "sandbox-sepay-token".to_string();
        assert!(bad_sepay.validate().is_err());

        // Reject placeholder or duplicate JWT secrets in production.
        let mut bad_jwt = valid_prod();
        bad_jwt.jwt_secret = "dev-only-nexusmart-hs256-secret-change-me".to_string();
        assert!(bad_jwt.validate().is_err());

        let mut dup_jwt = valid_prod();
        dup_jwt.service_jwt_secret = dup_jwt.jwt_secret.clone();
        assert!(dup_jwt.validate().is_err());
    }

    #[test]
    fn test_config_debug_redacts_all_secrets_including_zalopay_keys() {
        let mut cfg = Config::from_env();
        cfg.database_url = "postgres://user:super_db_pass@db:5432/pay".to_string();
        cfg.nats_url = "nats://svc:super_nats_pass@nats:4222".to_string();
        cfg.webhook_secret = "super-webhook-secret-xyz".to_string();
        cfg.jwt_secret = "super-jwt-secret-xyz".to_string();
        cfg.service_jwt_secret = "super-svc-jwt-secret-xyz".to_string();
        cfg.payos_client_id = "payos-client-secret".to_string();
        cfg.payos_api_key = "payos-api-secret".to_string();
        cfg.payos_checksum_key = "payos-checksum-secret".to_string();
        cfg.sepay_api_token = "sepay-token-secret".to_string();
        cfg.zalopay.key1 = "zalopay-super-secret-key1".to_string();
        cfg.zalopay.key2 = "zalopay-super-secret-key2".to_string();

        let dbg = format!("{cfg:?}");
        assert!(dbg.contains("[REDACTED]"));
        assert!(dbg.contains("zalopay"));
        assert!(dbg.contains("app_id"));
        assert!(dbg.contains("base_url"));
        assert!(dbg.contains("callback_url"));
        for secret in [
            "super_db_pass",
            "super_nats_pass",
            "super-webhook-secret-xyz",
            "super-jwt-secret-xyz",
            "super-svc-jwt-secret-xyz",
            "payos-client-secret",
            "payos-api-secret",
            "payos-checksum-secret",
            "sepay-token-secret",
            "zalopay-super-secret-key1",
            "zalopay-super-secret-key2",
        ] {
            assert!(
                !dbg.contains(secret),
                "Config Debug leaked secret '{secret}': {dbg}"
            );
        }
    }
}
