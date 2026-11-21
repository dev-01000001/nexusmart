use std::sync::{Arc, Mutex};
use std::time::Duration;

use chrono::{DateTime, Utc};
use hmac::{Hmac, Mac};
use reqwest::Client;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tokio::time::sleep;
use tracing::{info, warn};
use uuid::Uuid;

pub use crate::config::ZaloPayMode;
use crate::config::{is_placeholder_secret, Config};
use crate::error::{PaymentError, Result};
use crate::vietqr::{CircuitBreaker, ProviderTransaction, VietQrAdapter};
use crate::webhook_security::{compute_hmac_hex, IncomingWebhookPayload};

type HmacSha256 = Hmac<Sha256>;

const DEFAULT_MOCK_KEY1: &str = "zalopay-mock-key1-dev";
const DEFAULT_MOCK_KEY2: &str = "zalopay-mock-key2-dev";

/// ZaloPay v2 create-order intent returned to callers and persisted on `payments`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ZaloPayIntent {
    pub provider: String,
    pub app_trans_id: String,
    pub zp_trans_token: String,
    pub order_url: String,
}

/// Verified ZaloPay IPN callback mapped to canonical `IncomingWebhookPayload`.
#[derive(Debug, Clone)]
pub struct VerifiedZaloPayCallback {
    pub payload: IncomingWebhookPayload,
    pub raw_json: serde_json::Value,
    pub app_trans_id: String,
    pub zp_trans_id: String,
    pub amount_vnd: i64,
    pub server_time: DateTime<Utc>,
}

#[derive(Debug, Clone, Deserialize)]
struct ZaloPayCallbackData {
    #[serde(default)]
    #[allow(dead_code)]
    app_id: Option<i64>,
    app_trans_id: String,
    amount: i64,
    #[serde(default)]
    embed_data: Option<serde_json::Value>,
    zp_trans_id: serde_json::Value,
    #[serde(default)]
    server_time: Option<i64>,
}

/// Computes ZaloPay v2 `/v2/create` MAC:
/// `HMAC-SHA256(key1, "{app_id}|{app_trans_id}|{app_user}|{amount}|{app_time}|{embed_data}|{item}")`
#[allow(clippy::too_many_arguments)]
pub fn compute_create_mac(
    key1: &str,
    app_id: i64,
    app_trans_id: &str,
    app_user: &str,
    amount_vnd: i64,
    app_time_ms: i64,
    embed_data: &str,
    item: &str,
) -> String {
    let raw = format!(
        "{app_id}|{app_trans_id}|{app_user}|{amount_vnd}|{app_time_ms}|{embed_data}|{item}"
    );
    compute_hmac_hex(key1, raw.as_bytes())
}

/// Computes ZaloPay v2 `/v2/query` MAC:
/// `HMAC-SHA256(key1, "{app_id}|{app_trans_id}|{key1}")`
pub fn compute_query_mac(key1: &str, app_id: i64, app_trans_id: &str) -> String {
    let raw = format!("{app_id}|{app_trans_id}|{key1}");
    compute_hmac_hex(key1, raw.as_bytes())
}

/// Computes ZaloPay v2 `/v2/refund` MAC:
/// `HMAC-SHA256(key1, "{app_id}|{zp_trans_id}|{amount}|{description}|{timestamp}")`
pub fn compute_refund_mac(
    key1: &str,
    app_id: i64,
    zp_trans_id: &str,
    amount_vnd: i64,
    description: &str,
    timestamp_ms: i64,
) -> String {
    let raw = format!("{app_id}|{zp_trans_id}|{amount_vnd}|{description}|{timestamp_ms}");
    compute_hmac_hex(key1, raw.as_bytes())
}

/// Generates ZaloPay `app_trans_id` in standard `YYMMDD_<order_code>` format.
pub fn generate_app_trans_id(payment_id: Uuid, order_id: &str, now: DateTime<Utc>) -> String {
    let yymmdd = now.format("%y%m%d");
    let order_code = VietQrAdapter::generate_order_code(payment_id, order_id);
    format!("{yymmdd}_{order_code}")
}

/// Verifies a ZaloPay v2 callback JSON (`{"data": "<json-string>", "mac": "<hex>", "type": 1}`):
/// 1. Constant-time HMAC-SHA256 verification of `data` using `key2` (`hmac::Mac::verify_slice`).
/// 2. Strict anti-replay timestamp check using `data.server_time` (ms or s) with `X-Timestamp` fallback;
///    rejects missing or stale/skewed timestamps outside `replay_window_secs` (default 300s).
/// 3. Extracts `zp_trans_id` (unique `provider_tx_id`), `app_trans_id`, integer VND `amount`,
///    and `embed_data` (`payment_id`, `order_group_id`, `order_id`).
pub fn verify_zalopay_callback(
    key2: &str,
    raw_body: &[u8],
    timestamp_header: Option<&str>,
    replay_window_secs: i64,
    now: DateTime<Utc>,
) -> Result<VerifiedZaloPayCallback> {
    let raw_json: serde_json::Value = serde_json::from_slice(raw_body).map_err(|e| {
        PaymentError::Validation(format!("invalid JSON ZaloPay callback body: {e}"))
    })?;

    let raw_mac = raw_json
        .get("mac")
        .and_then(|v| v.as_str())
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .ok_or_else(|| PaymentError::Unauthorized("missing ZaloPay callback mac".to_string()))?;

    let provided_bytes = hex::decode(raw_mac).map_err(|_| {
        PaymentError::Unauthorized("malformed ZaloPay callback mac hex".to_string())
    })?;

    let data_str = raw_json
        .get("data")
        .and_then(|v| v.as_str())
        .filter(|s| !s.trim().is_empty())
        .ok_or_else(|| PaymentError::Validation("missing ZaloPay callback data".to_string()))?;

    // Constant-time HMAC-SHA256(key2, data_str)
    let mut mac =
        HmacSha256::new_from_slice(key2.as_bytes()).expect("HMAC accepts key of any length");
    mac.update(data_str.as_bytes());
    mac.verify_slice(&provided_bytes).map_err(|_| {
        PaymentError::Unauthorized("invalid ZaloPay callback HMAC signature".to_string())
    })?;

    let parsed: ZaloPayCallbackData = serde_json::from_str(data_str).map_err(|e| {
        PaymentError::Validation(format!("invalid ZaloPay callback data JSON: {e}"))
    })?;

    // Strict anti-replay check: server_time (ms or s) from data JSON, or X-Timestamp header fallback.
    let server_time = if let Some(st) = parsed.server_time {
        parse_unix_timestamp(st)?
    } else if let Some(ts_hdr) = timestamp_header.map(str::trim).filter(|s| !s.is_empty()) {
        parse_header_timestamp(ts_hdr)?
    } else {
        return Err(PaymentError::Unauthorized(
            "missing server_time or X-Timestamp for replay protection".to_string(),
        ));
    };

    let drift_secs = (now - server_time).num_seconds().abs();
    if drift_secs > replay_window_secs {
        return Err(PaymentError::Unauthorized(format!(
            "stale or skewed ZaloPay callback timestamp (drift={drift_secs}s, max={replay_window_secs}s)"
        )));
    }

    let zp_trans_id = extract_json_id(&parsed.zp_trans_id).ok_or_else(|| {
        PaymentError::Validation("zp_trans_id is required in ZaloPay callback".to_string())
    })?;

    let app_trans_id = parsed.app_trans_id.trim().to_string();
    if app_trans_id.is_empty() {
        return Err(PaymentError::Validation(
            "app_trans_id is required in ZaloPay callback".to_string(),
        ));
    }

    if parsed.amount <= 0 {
        return Err(PaymentError::Validation(
            "ZaloPay callback amount must be positive integer VND".to_string(),
        ));
    }

    let (payment_id, order_group_id, order_id) = parse_embed_data(parsed.embed_data.as_ref());

    let payload = IncomingWebhookPayload {
        provider_tx_id: zp_trans_id.clone(),
        payment_id,
        order_group_id,
        order_id,
        provider_order_code: Some(app_trans_id.clone()),
        reference: Some(app_trans_id.clone()),
        amount_vnd: parsed.amount,
        status: "SUCCEEDED".to_string(),
        provider: None,
        occurred_at: Some(server_time.to_rfc3339()),
    };

    Ok(VerifiedZaloPayCallback {
        payload,
        raw_json,
        app_trans_id,
        zp_trans_id,
        amount_vnd: parsed.amount,
        server_time,
    })
}

fn parse_unix_timestamp(raw: i64) -> Result<DateTime<Utc>> {
    if raw <= 0 {
        return Err(PaymentError::Unauthorized(
            "invalid non-positive ZaloPay server_time".to_string(),
        ));
    }
    let dt = if raw > 10_000_000_000 {
        DateTime::from_timestamp_millis(raw)
    } else {
        DateTime::from_timestamp(raw, 0)
    };
    dt.ok_or_else(|| {
        PaymentError::Unauthorized("out-of-range ZaloPay server_time value".to_string())
    })
}

fn parse_header_timestamp(raw: &str) -> Result<DateTime<Utc>> {
    if let Ok(num) = raw.parse::<i64>() {
        return parse_unix_timestamp(num);
    }
    DateTime::parse_from_rfc3339(raw)
        .map(|dt| dt.with_timezone(&Utc))
        .map_err(|_| PaymentError::Unauthorized("invalid X-Timestamp format".to_string()))
}

fn extract_json_id(val: &serde_json::Value) -> Option<String> {
    match val {
        serde_json::Value::String(s) => {
            let trimmed = s.trim();
            if trimmed.is_empty() {
                None
            } else {
                Some(trimmed.to_string())
            }
        }
        serde_json::Value::Number(n) => Some(n.to_string()),
        _ => None,
    }
}

fn parse_embed_data(
    raw: Option<&serde_json::Value>,
) -> (Option<String>, Option<String>, Option<String>) {
    let obj_owned: Option<serde_json::Value> = match raw {
        Some(serde_json::Value::String(s)) => serde_json::from_str::<serde_json::Value>(s).ok(),
        Some(v @ serde_json::Value::Object(_)) => Some(v.clone()),
        _ => None,
    };

    let Some(obj) = obj_owned else {
        return (None, None, None);
    };

    let get_non_empty = |k: &str| {
        obj.get(k)
            .and_then(|v| v.as_str())
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(ToString::to_string)
    };

    (
        get_non_empty("payment_id"),
        get_non_empty("order_group_id"),
        get_non_empty("order_id"),
    )
}

#[derive(Clone)]
pub struct ZaloPayAdapter {
    mode: ZaloPayMode,
    app_id: i64,
    key1: String,
    key2: String,
    base_url: String,
    callback_url: String,
    max_retries: u32,
    http_client: Client,
    breaker: CircuitBreaker,
    mock_transactions: Arc<Mutex<Vec<ProviderTransaction>>>,
}

impl ZaloPayAdapter {
    pub fn from_config(cfg: &Config) -> Self {
        let http_client = Client::builder()
            .timeout(cfg.external_http_timeout)
            .build()
            .expect("reqwest client build");

        let effective_mode = if cfg.zalopay_mode == ZaloPayMode::Sandbox
            && !is_placeholder_secret(&cfg.zalopay_key1)
            && !is_placeholder_secret(&cfg.zalopay_key2)
        {
            ZaloPayMode::Sandbox
        } else {
            ZaloPayMode::Mock
        };

        let key1 = if cfg.zalopay_key1.trim().is_empty() {
            DEFAULT_MOCK_KEY1.to_string()
        } else {
            cfg.zalopay_key1.clone()
        };
        let key2 = if cfg.zalopay_key2.trim().is_empty() {
            DEFAULT_MOCK_KEY2.to_string()
        } else {
            cfg.zalopay_key2.clone()
        };

        Self {
            mode: effective_mode,
            app_id: cfg.zalopay_app_id,
            key1,
            key2,
            base_url: cfg.zalopay_base_url.clone(),
            callback_url: cfg.zalopay_callback_url.clone(),
            max_retries: cfg.external_max_retries,
            http_client,
            breaker: CircuitBreaker::new(5, Duration::from_secs(30)),
            mock_transactions: Arc::new(Mutex::new(Vec::new())),
        }
    }

    pub fn mode(&self) -> ZaloPayMode {
        self.mode
    }

    pub fn app_id(&self) -> i64 {
        self.app_id
    }

    pub fn circuit_breaker(&self) -> &CircuitBreaker {
        &self.breaker
    }

    /// Creates a ZaloPay v2 payment order (deterministic mock intent in Mock mode,
    /// HTTP POST `/v2/create` with circuit breaker + timeout + backoff retry in Sandbox mode).
    pub async fn create_order(
        &self,
        payment_id: Uuid,
        order_group_id: &str,
        order_id: &str,
        user_id: &str,
        amount_vnd: i64,
        expires_at: DateTime<Utc>,
    ) -> Result<ZaloPayIntent> {
        if amount_vnd <= 0 {
            return Err(PaymentError::Validation(
                "ZaloPay amount_vnd must be positive".to_string(),
            ));
        }

        let now = Utc::now();
        let app_trans_id = generate_app_trans_id(payment_id, order_id, now);
        let app_time_ms = now.timestamp_millis();
        let app_user = if user_id.trim().is_empty() {
            "nexusmart_buyer"
        } else {
            user_id.trim()
        };

        let embed_data = serde_json::json!({
            "payment_id": payment_id.to_string(),
            "order_group_id": order_group_id,
            "order_id": order_id,
            "redirecturl": "https://nexusmart.local/checkout/success",
        })
        .to_string();
        let item = "[]";
        let mac = compute_create_mac(
            &self.key1,
            self.app_id,
            &app_trans_id,
            app_user,
            amount_vnd,
            app_time_ms,
            &embed_data,
            item,
        );

        match self.mode {
            ZaloPayMode::Mock => {
                let zp_trans_token = Self::deterministic_mock_token(payment_id, &app_trans_id);
                let order_url = format!(
                    "https://sbgateway.zalopay.vn/openinapp?order={zp_trans_token}&app_trans_id={app_trans_id}&amount={amount_vnd}"
                );
                Ok(ZaloPayIntent {
                    provider: ZaloPayMode::Mock.as_str().to_string(),
                    app_trans_id,
                    zp_trans_token,
                    order_url,
                })
            }
            ZaloPayMode::Sandbox => {
                let expire_duration_seconds = (expires_at - now).num_seconds().clamp(60, 86_400);
                let url = format!("{}/v2/create", self.base_url.trim_end_matches('/'));
                let body = serde_json::json!({
                    "app_id": self.app_id,
                    "app_user": app_user,
                    "app_time": app_time_ms,
                    "amount": amount_vnd,
                    "app_trans_id": app_trans_id,
                    "embed_data": embed_data,
                    "item": item,
                    "bank_code": "",
                    "description": format!("NexusMart thanh toan don hang {order_id}"),
                    "callback_url": self.callback_url,
                    "expire_duration_seconds": expire_duration_seconds,
                    "mac": mac,
                });

                let resp = self
                    .execute_with_retry("zalopay_create_order", || {
                        self.http_client.post(&url).json(&body)
                    })
                    .await?;

                let return_code = resp
                    .get("return_code")
                    .and_then(|v| v.as_i64())
                    .unwrap_or(0);
                if return_code != 1 {
                    let msg = resp
                        .get("return_message")
                        .and_then(|v| v.as_str())
                        .unwrap_or("unknown ZaloPay error");
                    return Err(PaymentError::Provider(format!(
                        "ZaloPay /v2/create failed (return_code={return_code}): {msg}"
                    )));
                }

                let zp_trans_token = resp
                    .get("zp_trans_token")
                    .and_then(|v| v.as_str())
                    .filter(|s| !s.trim().is_empty())
                    .map(ToString::to_string)
                    .ok_or_else(|| {
                        PaymentError::Provider(
                            "ZaloPay /v2/create missing zp_trans_token".to_string(),
                        )
                    })?;

                let order_url = resp
                    .get("order_url")
                    .and_then(|v| v.as_str())
                    .filter(|s| !s.trim().is_empty())
                    .map(ToString::to_string)
                    .ok_or_else(|| {
                        PaymentError::Provider("ZaloPay /v2/create missing order_url".to_string())
                    })?;

                Ok(ZaloPayIntent {
                    provider: ZaloPayMode::Sandbox.as_str().to_string(),
                    app_trans_id,
                    zp_trans_token,
                    order_url,
                })
            }
        }
    }

    /// Queries final order status at ZaloPay (`/v2/query`) for expiry watchdog & reconciliation.
    /// Returns `Ok(Some(tx))` if paid (`return_code == 1`), `Ok(None)` if unpaid/processing.
    pub async fn query_order_status(
        &self,
        app_trans_id: &str,
    ) -> Result<Option<ProviderTransaction>> {
        match self.mode {
            ZaloPayMode::Mock => {
                let guard = self.mock_transactions.lock().expect("mock tx lock");
                Ok(guard
                    .iter()
                    .find(|t| t.provider_order_code == app_trans_id && t.status == "succeeded")
                    .cloned())
            }
            ZaloPayMode::Sandbox => {
                let mac = compute_query_mac(&self.key1, self.app_id, app_trans_id);
                let url = format!("{}/v2/query", self.base_url.trim_end_matches('/'));
                let body = serde_json::json!({
                    "app_id": self.app_id,
                    "app_trans_id": app_trans_id,
                    "mac": mac,
                });

                let resp = self
                    .execute_with_retry("zalopay_query_order", || {
                        self.http_client.post(&url).json(&body)
                    })
                    .await?;

                let return_code = resp
                    .get("return_code")
                    .and_then(|v| v.as_i64())
                    .unwrap_or(0);
                if return_code == 1 {
                    let amount_vnd = resp.get("amount").and_then(|v| v.as_i64()).unwrap_or(0);
                    let zp_tx_id = resp
                        .get("zp_trans_id")
                        .and_then(extract_json_id)
                        .unwrap_or_else(|| format!("zp-{app_trans_id}"));
                    return Ok(Some(ProviderTransaction {
                        provider_tx_id: zp_tx_id,
                        provider_order_code: app_trans_id.to_string(),
                        amount_vnd,
                        status: "succeeded".to_string(),
                        occurred_at: Utc::now(),
                    }));
                }
                Ok(None)
            }
        }
    }

    /// Initiates a ZaloPay refund (deterministic mock refund ID in Mock mode,
    /// HTTP POST `/v2/refund` with circuit breaker + timeout + backoff retry in Sandbox mode).
    pub async fn create_refund(
        &self,
        zp_trans_id: &str,
        amount_vnd: i64,
        description: &str,
    ) -> Result<String> {
        let zp_tx = zp_trans_id.trim();
        if zp_tx.is_empty() {
            return Err(PaymentError::Validation(
                "zp_trans_id is required for ZaloPay refund".to_string(),
            ));
        }
        if amount_vnd <= 0 {
            return Err(PaymentError::Validation(
                "ZaloPay refund amount_vnd must be positive".to_string(),
            ));
        }

        let desc = if description.trim().is_empty() {
            "NexusMart refund"
        } else {
            description.trim()
        };

        match self.mode {
            ZaloPayMode::Mock => Ok(Self::deterministic_mock_refund_id(
                self.app_id,
                zp_tx,
                amount_vnd,
                desc,
            )),
            ZaloPayMode::Sandbox => {
                let now = Utc::now();
                let timestamp_ms = now.timestamp_millis();
                let m_refund_id = format!(
                    "{}_{}_{}",
                    now.format("%y%m%d"),
                    self.app_id,
                    &Uuid::new_v4().simple().to_string()[..10]
                );
                let mac = compute_refund_mac(
                    &self.key1,
                    self.app_id,
                    zp_tx,
                    amount_vnd,
                    desc,
                    timestamp_ms,
                );
                let url = format!("{}/v2/refund", self.base_url.trim_end_matches('/'));
                let body = serde_json::json!({
                    "app_id": self.app_id,
                    "m_refund_id": m_refund_id,
                    "zp_trans_id": zp_tx,
                    "amount": amount_vnd,
                    "timestamp": timestamp_ms,
                    "description": desc,
                    "mac": mac,
                });

                let resp = self
                    .execute_with_retry("zalopay_create_refund", || {
                        self.http_client.post(&url).json(&body)
                    })
                    .await?;

                let return_code = resp
                    .get("return_code")
                    .and_then(|v| v.as_i64())
                    .unwrap_or(0);
                // ZaloPay /v2/refund returns 1 (success) or 3 (processing/accepted)
                if return_code != 1 && return_code != 3 {
                    let msg = resp
                        .get("return_message")
                        .and_then(|v| v.as_str())
                        .unwrap_or("unknown ZaloPay refund error");
                    return Err(PaymentError::Provider(format!(
                        "ZaloPay /v2/refund failed (return_code={return_code}): {msg}"
                    )));
                }

                let refund_id = resp
                    .get("refund_id")
                    .and_then(extract_json_id)
                    .unwrap_or(m_refund_id);
                Ok(refund_id)
            }
        }
    }

    /// Lists provider transactions for daily reconciliation (UC-PAY07).
    pub async fn list_provider_transactions(&self) -> Result<Vec<ProviderTransaction>> {
        match self.mode {
            ZaloPayMode::Mock => {
                let guard = self.mock_transactions.lock().expect("mock tx lock");
                Ok(guard.clone())
            }
            ZaloPayMode::Sandbox => Ok(Vec::new()),
        }
    }

    /// Records a mock ZaloPay settlement (used in Mock mode when callback confirms or in tests).
    pub fn record_mock_transaction(&self, tx: ProviderTransaction) {
        if self.mode == ZaloPayMode::Mock {
            let mut guard = self.mock_transactions.lock().expect("mock tx lock");
            if !guard.iter().any(|t| t.provider_tx_id == tx.provider_tx_id) {
                guard.push(tx);
            }
        }
    }

    /// Verifies an incoming ZaloPay callback using this adapter's `key2`.
    pub fn verify_callback(
        &self,
        raw_body: &[u8],
        timestamp_header: Option<&str>,
        replay_window_secs: i64,
        now: DateTime<Utc>,
    ) -> Result<VerifiedZaloPayCallback> {
        verify_zalopay_callback(
            &self.key2,
            raw_body,
            timestamp_header,
            replay_window_secs,
            now,
        )
    }

    /// Builds a validly HMAC-SHA256 signed ZaloPay callback JSON body for unit/integration tests.
    #[allow(clippy::too_many_arguments)]
    pub fn build_mock_callback_signed(
        &self,
        payment_id: Option<Uuid>,
        order_group_id: Option<String>,
        order_id: Option<String>,
        app_trans_id: &str,
        zp_trans_id: &str,
        amount_vnd: i64,
        server_time_ms: i64,
    ) -> Vec<u8> {
        let embed_data = serde_json::json!({
            "payment_id": payment_id.map(|u| u.to_string()).unwrap_or_default(),
            "order_group_id": order_group_id.unwrap_or_default(),
            "order_id": order_id.unwrap_or_default(),
        })
        .to_string();

        let data_str = serde_json::json!({
            "app_id": self.app_id,
            "app_trans_id": app_trans_id,
            "app_time": server_time_ms,
            "app_user": "nexusmart_buyer",
            "amount": amount_vnd,
            "embed_data": embed_data,
            "item": "[]",
            "zp_trans_id": zp_trans_id,
            "server_time": server_time_ms,
            "channel": 38,
            "merchant_user_id": "mock_user",
            "user_fee_amount": 0,
            "discount_amount": 0,
        })
        .to_string();

        let mac = compute_hmac_hex(&self.key2, data_str.as_bytes());
        serde_json::to_vec(&serde_json::json!({
            "data": data_str,
            "mac": mac,
            "type": 1,
        }))
        .expect("serialize mock ZaloPay callback")
    }

    fn deterministic_mock_token(payment_id: Uuid, app_trans_id: &str) -> String {
        let mut hasher = Sha256::new();
        hasher.update(payment_id.as_bytes());
        hasher.update(app_trans_id.as_bytes());
        let digest = hex::encode_upper(hasher.finalize());
        format!("ZPTOK{}", &digest[..16])
    }

    fn deterministic_mock_refund_id(
        app_id: i64,
        zp_trans_id: &str,
        amount_vnd: i64,
        description: &str,
    ) -> String {
        let mut hasher = Sha256::new();
        hasher.update(app_id.to_be_bytes());
        hasher.update(zp_trans_id.as_bytes());
        hasher.update(amount_vnd.to_be_bytes());
        hasher.update(description.as_bytes());
        let digest = hex::encode_upper(hasher.finalize());
        format!("zpmock_ref_{app_id}_{}", &digest[..12])
    }

    /// Executes an outbound HTTP request with circuit breaker + timeout + exponential backoff retry.
    async fn execute_with_retry<F>(&self, op_name: &str, build_req: F) -> Result<serde_json::Value>
    where
        F: Fn() -> reqwest::RequestBuilder,
    {
        self.breaker.allow_request()?;

        let mut last_err = String::new();
        let total_attempts = self.max_retries.saturating_add(1);

        for attempt in 0..total_attempts {
            match build_req().send().await {
                Ok(resp) => {
                    let status = resp.status();
                    if status.is_success() {
                        match resp.json::<serde_json::Value>().await {
                            Ok(val) => {
                                self.breaker.record_success();
                                info!(
                                    op = op_name,
                                    attempt = attempt + 1,
                                    "ZaloPay provider call succeeded"
                                );
                                return Ok(val);
                            }
                            Err(e) => {
                                last_err = format!("invalid ZaloPay JSON response: {e}");
                            }
                        }
                    } else if status.is_client_error() && status.as_u16() != 429 {
                        self.breaker.record_failure();
                        return Err(PaymentError::Provider(format!(
                            "{op_name} rejected with HTTP {}",
                            status.as_u16()
                        )));
                    } else {
                        last_err = format!("{op_name} returned HTTP {}", status.as_u16());
                    }
                }
                Err(e) => {
                    last_err = format!("{op_name} transport error: {e}");
                }
            }

            self.breaker.record_failure();
            warn!(
                op = op_name,
                attempt = attempt + 1,
                max_attempts = total_attempts,
                error = %last_err,
                "ZaloPay provider call failed"
            );

            if attempt + 1 < total_attempts {
                let backoff_ms = 50_u64.saturating_mul(1_u64 << attempt.min(5));
                sleep(Duration::from_millis(backoff_ms)).await;
            }
        }

        Err(PaymentError::Provider(last_err))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::routing::post;
    use axum::{Json, Router};
    use chrono::Duration as ChronoDuration;
    use std::collections::HashSet;
    use std::sync::atomic::{AtomicU32, Ordering};
    use tokio::net::TcpListener;

    fn mock_config() -> Config {
        let mut cfg = Config::from_env();
        cfg.zalopay_mode = ZaloPayMode::Mock;
        cfg.zalopay_app_id = 2554;
        cfg.zalopay_key1 = "test-key1-secret".to_string();
        cfg.zalopay_key2 = "test-key2-secret".to_string();
        cfg.external_max_retries = 2;
        cfg.external_http_timeout = Duration::from_secs(2);
        cfg
    }

    #[test]
    fn test_mode_falls_back_to_mock_when_keys_empty_or_placeholder() {
        let mut cfg = mock_config();
        cfg.zalopay_mode = ZaloPayMode::Sandbox;
        cfg.zalopay_key1 = "".to_string();
        assert_eq!(ZaloPayAdapter::from_config(&cfg).mode(), ZaloPayMode::Mock);

        cfg.zalopay_key1 = "dev-key1-placeholder".to_string();
        cfg.zalopay_key2 = "real-key2-abc123".to_string();
        assert_eq!(ZaloPayAdapter::from_config(&cfg).mode(), ZaloPayMode::Mock);

        cfg.zalopay_key1 = "real-key1-abc123".to_string();
        cfg.zalopay_key2 = "change-me-key2".to_string();
        assert_eq!(ZaloPayAdapter::from_config(&cfg).mode(), ZaloPayMode::Mock);

        cfg.zalopay_key1 = "real-key1-abc123".to_string();
        cfg.zalopay_key2 = "real-key2-xyz789".to_string();
        assert_eq!(
            ZaloPayAdapter::from_config(&cfg).mode(),
            ZaloPayMode::Sandbox
        );
    }

    #[test]
    fn test_mac_signing_helpers_match_zalopay_v2_spec() {
        let create_mac = compute_create_mac(
            "k1",
            2554,
            "261121_NM1234567890",
            "buyer-1",
            250_000,
            1_700_000_000_000,
            "{}",
            "[]",
        );
        let expected_create = compute_hmac_hex(
            "k1",
            b"2554|261121_NM1234567890|buyer-1|250000|1700000000000|{}|[]",
        );
        assert_eq!(create_mac, expected_create);

        let query_mac = compute_query_mac("k1", 2554, "261121_NM1234567890");
        assert_eq!(
            query_mac,
            compute_hmac_hex("k1", b"2554|261121_NM1234567890|k1")
        );

        let refund_mac = compute_refund_mac(
            "k1",
            2554,
            "261121000001",
            100_000,
            "buyer return",
            1_700_000_001_000,
        );
        assert_eq!(
            refund_mac,
            compute_hmac_hex("k1", b"2554|261121000001|100000|buyer return|1700000001000")
        );
    }

    #[tokio::test]
    async fn test_mock_create_query_refund_and_reconciliation_transactions() {
        let cfg = mock_config();
        let adapter = ZaloPayAdapter::from_config(&cfg);
        assert_eq!(adapter.mode(), ZaloPayMode::Mock);
        assert_eq!(adapter.app_id(), 2554);

        let payment_id = Uuid::parse_str("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee").unwrap();
        let exp = Utc::now() + ChronoDuration::minutes(15);

        let intent1 = adapter
            .create_order(payment_id, "grp-1", "ord-1", "buyer-1", 350_000, exp)
            .await
            .unwrap();
        let intent2 = adapter
            .create_order(payment_id, "grp-1", "ord-1", "buyer-1", 350_000, exp)
            .await
            .unwrap();

        assert_eq!(intent1.provider, "zalopay_mock");
        assert_eq!(intent1.app_trans_id, intent2.app_trans_id);
        assert!(intent1.app_trans_id.contains("_NM"));
        assert_eq!(intent1.zp_trans_token, intent2.zp_trans_token);
        assert_eq!(intent1.order_url, intent2.order_url);
        assert!(intent1.order_url.contains("amount=350000"));

        // Invalid amount rejected
        assert!(matches!(
            adapter
                .create_order(payment_id, "grp-1", "ord-1", "buyer-1", 0, exp)
                .await,
            Err(PaymentError::Validation(_))
        ));

        // Before payment recorded -> query returns None
        assert!(adapter
            .query_order_status(&intent1.app_trans_id)
            .await
            .unwrap()
            .is_none());

        // Record mock tx (deduplicated by provider_tx_id)
        let tx = ProviderTransaction {
            provider_tx_id: "ZP-TX-1001".to_string(),
            provider_order_code: intent1.app_trans_id.clone(),
            amount_vnd: 350_000,
            status: "succeeded".to_string(),
            occurred_at: Utc::now(),
        };
        adapter.record_mock_transaction(tx.clone());
        adapter.record_mock_transaction(tx);

        let found = adapter
            .query_order_status(&intent1.app_trans_id)
            .await
            .unwrap()
            .expect("should find recorded mock tx");
        assert_eq!(found.provider_tx_id, "ZP-TX-1001");
        assert_eq!(found.amount_vnd, 350_000);
        assert_eq!(adapter.list_provider_transactions().await.unwrap().len(), 1);

        // Deterministic mock refund ID
        let ref1 = adapter
            .create_refund("ZP-TX-1001", 150_000, "item returned")
            .await
            .unwrap();
        let ref2 = adapter
            .create_refund("ZP-TX-1001", 150_000, "item returned")
            .await
            .unwrap();
        assert_eq!(ref1, ref2);
        assert!(ref1.starts_with("zpmock_ref_2554_"));

        assert!(adapter.create_refund("", 10_000, "bad").await.is_err());
        assert!(adapter.create_refund("ZP-TX-1001", 0, "bad").await.is_err());
    }

    #[test]
    fn test_callback_hmac_verification_replay_protection_and_10x_dedup() {
        let cfg = mock_config();
        let adapter = ZaloPayAdapter::from_config(&cfg);
        let now = Utc::now();
        let pid = Uuid::new_v4();

        // 1. Valid signed callback with fresh server_time (ms)
        let body = adapter.build_mock_callback_signed(
            Some(pid),
            Some("grp-zp-1".into()),
            Some("ord-zp-1".into()),
            "261121_NMABCDEF1234",
            "26112100009999",
            420_000,
            now.timestamp_millis(),
        );
        let verified = adapter.verify_callback(&body, None, 300, now).unwrap();
        assert_eq!(verified.zp_trans_id, "26112100009999");
        assert_eq!(verified.app_trans_id, "261121_NMABCDEF1234");
        assert_eq!(verified.amount_vnd, 420_000);
        assert_eq!(verified.payload.provider_tx_id, "26112100009999");
        assert_eq!(verified.payload.parsed_payment_id(), Some(pid));
        assert_eq!(verified.payload.order_group_id.as_deref(), Some("grp-zp-1"));
        assert_eq!(verified.payload.order_id.as_deref(), Some("ord-zp-1"));

        // 2. Tampered MAC / wrong key2 -> 401 Unauthorized
        let err_wrong_key =
            verify_zalopay_callback("wrong-key2", &body, None, 300, now).unwrap_err();
        assert!(matches!(err_wrong_key, PaymentError::Unauthorized(_)));

        let bad_mac_body = br#"{"data":"{\"app_trans_id\":\"x\",\"amount\":1000,\"zp_trans_id\":1}","mac":"not-hex","type":1}"#;
        assert!(matches!(
            adapter.verify_callback(bad_mac_body, None, 300, now),
            Err(PaymentError::Unauthorized(_))
        ));

        let missing_mac_body = br#"{"data":"{}","type":1}"#;
        assert!(matches!(
            adapter.verify_callback(missing_mac_body, None, 300, now),
            Err(PaymentError::Unauthorized(_))
        ));

        // 3. Stale server_time (> 300s) -> 401 Unauthorized
        let stale_body = adapter.build_mock_callback_signed(
            Some(pid),
            None,
            None,
            "261121_NMABCDEF1234",
            "26112100009999",
            420_000,
            (now - ChronoDuration::seconds(301)).timestamp_millis(),
        );
        assert!(matches!(
            adapter.verify_callback(&stale_body, None, 300, now),
            Err(PaymentError::Unauthorized(_))
        ));

        // 4. Future skewed server_time (> 300s) -> 401 Unauthorized
        let future_body = adapter.build_mock_callback_signed(
            Some(pid),
            None,
            None,
            "261121_NMABCDEF1234",
            "26112100009999",
            420_000,
            (now + ChronoDuration::seconds(305)).timestamp_millis(),
        );
        assert!(matches!(
            adapter.verify_callback(&future_body, None, 300, now),
            Err(PaymentError::Unauthorized(_))
        ));

        // 5. Missing server_time AND missing X-Timestamp -> 401 Unauthorized; with fresh X-Timestamp -> Ok
        let no_time_data = serde_json::json!({
            "app_trans_id": "261121_NM1122334455",
            "amount": 100_000,
            "zp_trans_id": 99887766_i64,
            "embed_data": {"order_id": "ord-fallback"}
        })
        .to_string();
        let no_time_mac = compute_hmac_hex(&cfg.zalopay_key2, no_time_data.as_bytes());
        let no_time_body = serde_json::to_vec(&serde_json::json!({
            "data": no_time_data,
            "mac": no_time_mac,
            "type": 1,
        }))
        .unwrap();

        assert!(matches!(
            adapter.verify_callback(&no_time_body, None, 300, now),
            Err(PaymentError::Unauthorized(_))
        ));
        let fresh_ts = now.timestamp().to_string();
        let verified_hdr = adapter
            .verify_callback(&no_time_body, Some(&fresh_ts), 300, now)
            .unwrap();
        assert_eq!(verified_hdr.zp_trans_id, "99887766");
        assert_eq!(
            verified_hdr.payload.order_id.as_deref(),
            Some("ord-fallback")
        );

        // 6. 10x duplicate callback simulation -> processed ONCE by zp_trans_id
        let mut seen = HashSet::new();
        let mut ledger_posts = 0;
        for _ in 0..10 {
            let v = adapter.verify_callback(&body, None, 300, now).unwrap();
            if seen.insert(v.payload.provider_tx_id) {
                ledger_posts += 1;
            }
        }
        assert_eq!(ledger_posts, 1);
    }

    #[tokio::test]
    async fn test_sandbox_http_create_query_refund_retry_and_circuit_breaker() {
        let create_attempts = Arc::new(AtomicU32::new(0));
        let create_counter = create_attempts.clone();

        let app = Router::new()
            .route(
                "/v2/create",
                post(move |Json(payload): Json<serde_json::Value>| {
                    let c = create_counter.clone();
                    async move {
                        let n = c.fetch_add(1, Ordering::SeqCst);
                        // First call fails with 503 to exercise backoff retry!
                        if n == 0 {
                            return (
                                axum::http::StatusCode::SERVICE_UNAVAILABLE,
                                Json(serde_json::json!({"return_code": 0})),
                            );
                        }
                        let app_trans_id = payload["app_trans_id"].as_str().unwrap_or("");
                        assert!(!payload["mac"].as_str().unwrap_or("").is_empty());
                        (
                            axum::http::StatusCode::OK,
                            Json(serde_json::json!({
                                "return_code": 1,
                                "return_message": "success",
                                "zp_trans_token": format!("sb_tok_{app_trans_id}"),
                                "order_url": format!("https://sbgateway.zalopay.vn/pay/{app_trans_id}"),
                            })),
                        )
                    }
                }),
            )
            .route(
                "/v2/query",
                post(|Json(payload): Json<serde_json::Value>| async move {
                    let app_trans_id = payload["app_trans_id"].as_str().unwrap_or("");
                    if app_trans_id.ends_with("_UNPAID") {
                        Json(serde_json::json!({
                            "return_code": 3,
                            "return_message": "processing"
                        }))
                    } else {
                        Json(serde_json::json!({
                            "return_code": 1,
                            "return_message": "success",
                            "amount": 250_000,
                            "zp_trans_id": 26112100001234_i64
                        }))
                    }
                }),
            )
            .route(
                "/v2/refund",
                post(|Json(payload): Json<serde_json::Value>| async move {
                    let amount = payload["amount"].as_i64().unwrap_or(0);
                    if amount == 999_999 {
                        Json(serde_json::json!({
                            "return_code": 2,
                            "return_message": "refund rejected"
                        }))
                    } else {
                        Json(serde_json::json!({
                            "return_code": 1,
                            "return_message": "success",
                            "refund_id": 9000123456_i64
                        }))
                    }
                }),
            );

        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server_handle = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });

        let mut cfg = mock_config();
        cfg.zalopay_mode = ZaloPayMode::Sandbox;
        cfg.zalopay_key1 = "real-sb-key1-987654".to_string();
        cfg.zalopay_key2 = "real-sb-key2-987654".to_string();
        cfg.zalopay_base_url = format!("http://{addr}");
        cfg.external_max_retries = 2;

        let adapter = ZaloPayAdapter::from_config(&cfg);
        assert_eq!(adapter.mode(), ZaloPayMode::Sandbox);

        let pid = Uuid::new_v4();
        let exp = Utc::now() + ChronoDuration::minutes(15);
        let intent = adapter
            .create_order(pid, "grp-sb", "ord-sb", "buyer-sb", 250_000, exp)
            .await
            .unwrap();
        assert_eq!(intent.provider, "zalopay");
        assert!(intent.zp_trans_token.starts_with("sb_tok_"));
        assert!(intent.order_url.contains(&intent.app_trans_id));
        assert_eq!(create_attempts.load(Ordering::SeqCst), 2);

        // Query paid order
        let paid = adapter
            .query_order_status(&intent.app_trans_id)
            .await
            .unwrap()
            .expect("paid order");
        assert_eq!(paid.provider_tx_id, "26112100001234");
        assert_eq!(paid.amount_vnd, 250_000);

        // Query unpaid/processing order -> Ok(None)
        assert!(adapter
            .query_order_status("261121_UNPAID")
            .await
            .unwrap()
            .is_none());

        // Refund succeeds
        let refund_id = adapter
            .create_refund("26112100001234", 100_000, "partial return")
            .await
            .unwrap();
        assert_eq!(refund_id, "9000123456");

        // Refund rejected by provider -> PaymentError::Provider
        let err = adapter
            .create_refund("26112100001234", 999_999, "bad")
            .await
            .unwrap_err();
        assert!(matches!(err, PaymentError::Provider(_)));

        // Sandbox list_provider_transactions returns empty vec cleanly
        assert!(adapter
            .list_provider_transactions()
            .await
            .unwrap()
            .is_empty());

        server_handle.abort();
    }
}
