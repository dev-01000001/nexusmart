use std::env;
use std::fmt;
use std::time::Duration;

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
pub enum ZaloPayMode {
    Mock,
    Sandbox,
}

impl ZaloPayMode {
    pub fn parse(raw: &str) -> Self {
        match raw.trim().to_ascii_lowercase().as_str() {
            "sandbox" => Self::Sandbox,
            _ => Self::Mock,
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::Mock => "zalopay_mock",
            Self::Sandbox => "zalopay",
        }
    }
}

/// Returns true when a secret/key is empty or clearly a template placeholder.
pub fn is_placeholder_secret(raw: &str) -> bool {
    let lower = raw.trim().to_ascii_lowercase();
    lower.is_empty()
        || lower.contains("placeholder")
        || lower.contains("change-me")
        || lower.contains("changeme")
        || lower.starts_with("your_")
        || lower.starts_with("your-")
        || lower.starts_with('<')
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
    pub zalopay_mode: ZaloPayMode,
    pub zalopay_app_id: i64,
    pub zalopay_key1: String,
    pub zalopay_key2: String,
    pub zalopay_base_url: String,
    pub zalopay_callback_url: String,
    pub external_http_timeout: Duration,
    pub external_max_retries: u32,
    pub expiration_scan_interval: Duration,
    pub outbox_poll_interval: Duration,
    pub reconciliation_interval: Duration,
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
            .field("zalopay_mode", &self.zalopay_mode)
            .field("zalopay_app_id", &self.zalopay_app_id)
            .field("zalopay_key1", &"[REDACTED]")
            .field("zalopay_key2", &"[REDACTED]")
            .field("zalopay_base_url", &self.zalopay_base_url)
            .field("zalopay_callback_url", &self.zalopay_callback_url)
            .field("external_http_timeout", &self.external_http_timeout)
            .field("external_max_retries", &self.external_max_retries)
            .field("expiration_scan_interval", &self.expiration_scan_interval)
            .field("outbox_poll_interval", &self.outbox_poll_interval)
            .field("reconciliation_interval", &self.reconciliation_interval)
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
            zalopay_mode: ZaloPayMode::parse(&env_string("ZALOPAY_MODE", "mock")),
            zalopay_app_id: env_i64("ZALOPAY_APP_ID", 2554),
            zalopay_key1: env_string("ZALOPAY_KEY1", ""),
            zalopay_key2: env_string("ZALOPAY_KEY2", ""),
            zalopay_base_url: env_string("ZALOPAY_BASE_URL", "https://sb-openapi.zalopay.vn"),
            zalopay_callback_url: env_string(
                "ZALOPAY_CALLBACK_URL",
                "http://localhost:8086/webhooks/zalopay",
            ),
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
        }
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

    #[test]
    fn test_vietqr_and_zalopay_mode_parsing() {
        assert_eq!(VietQrMode::parse("payos"), VietQrMode::PayOs);
        assert_eq!(VietQrMode::parse("  SEPAY "), VietQrMode::Sepay);
        assert_eq!(VietQrMode::parse("mock"), VietQrMode::Mock);
        assert_eq!(VietQrMode::parse("unknown"), VietQrMode::Mock);
        assert_eq!(VietQrMode::Mock.as_str(), "vietqr_mock");
        assert_eq!(VietQrMode::PayOs.as_str(), "payos");
        assert_eq!(VietQrMode::Sepay.as_str(), "sepay");

        assert_eq!(ZaloPayMode::parse("sandbox"), ZaloPayMode::Sandbox);
        assert_eq!(ZaloPayMode::parse("  SANDBOX "), ZaloPayMode::Sandbox);
        assert_eq!(ZaloPayMode::parse("mock"), ZaloPayMode::Mock);
        assert_eq!(ZaloPayMode::parse("other"), ZaloPayMode::Mock);
        assert_eq!(ZaloPayMode::Mock.as_str(), "zalopay_mock");
        assert_eq!(ZaloPayMode::Sandbox.as_str(), "zalopay");
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
        assert!(!is_placeholder_secret("real-zalopay-key-987654"));
    }

    #[test]
    fn test_config_debug_redacts_all_secrets_including_zalopay_keys() {
        let mut cfg = Config::from_env();
        cfg.database_url = "postgres://user:super_db_pass@db:5432/pay".to_string();
        cfg.webhook_secret = "super-webhook-secret-xyz".to_string();
        cfg.jwt_secret = "super-jwt-secret-xyz".to_string();
        cfg.service_jwt_secret = "super-svc-jwt-secret-xyz".to_string();
        cfg.payos_client_id = "payos-client-secret".to_string();
        cfg.payos_api_key = "payos-api-secret".to_string();
        cfg.payos_checksum_key = "payos-checksum-secret".to_string();
        cfg.sepay_api_token = "sepay-token-secret".to_string();
        cfg.zalopay_key1 = "zalopay-super-secret-key1".to_string();
        cfg.zalopay_key2 = "zalopay-super-secret-key2".to_string();

        let dbg = format!("{cfg:?}");
        assert!(dbg.contains("[REDACTED]"));
        assert!(dbg.contains("zalopay_mode"));
        assert!(dbg.contains("zalopay_app_id"));
        assert!(dbg.contains("zalopay_base_url"));
        assert!(dbg.contains("zalopay_callback_url"));
        for secret in [
            "super_db_pass",
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
