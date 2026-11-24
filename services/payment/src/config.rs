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

#[derive(Clone)]
pub struct Config {
    pub service_name: String,
    pub http_port: u16,
    pub grpc_port: u16,
    pub database_url: String,
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
            .field("service_name", &self.service_name)
            .field("http_port", &self.http_port)
            .field("grpc_port", &self.grpc_port)
            .field("database_url", &"[REDACTED]")
            .field("nats_url", &self.nats_url)
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
        Self {
            service_name: env_string("SERVICE_NAME", "payment"),
            http_port: env_u16("PAYMENT_HTTP_PORT", env_u16("HTTP_PORT", 8086)),
            grpc_port: env_u16("PAYMENT_GRPC_PORT", env_u16("GRPC_PORT", 50056)),
            database_url: env_string(
                "DATABASE_URL",
                "postgres://nexusmart:nexusmart@localhost:5432/payment_db",
            ),
            nats_url: env_string("NATS_URL", "nats://localhost:4222"),
            nats_stream: env_string("NATS_STREAM", "NEXUSMART"),
            otel_endpoint: env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            webhook_secret: env_string("PAYMENT_WEBHOOK_SECRET", "dev-webhook-secret-change-me"),
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
    /// (such as required ZaloPay credentials when `ZALOPAY_MODE=sandbox`).
    pub fn try_from_env() -> Result<Self, String> {
        let cfg = Self::from_env();
        cfg.validate()?;
        Ok(cfg)
    }

    pub fn validate(&self) -> Result<(), String> {
        self.zalopay.validate()
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
}
