use std::collections::{HashMap, VecDeque};
use std::fmt;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use chrono::{DateTime, Utc};
use hmac::{Hmac, Mac};
use reqwest::Client;
use serde::{Deserialize, Deserializer, Serialize};
use sha2::{Digest, Sha256};
use tracing::info;
use uuid::Uuid;

use crate::config::{Config, ZaloPayConfig, ZaloPayMode};
use crate::domain::{
    vn_offset, ZALOPAY_MAX_EXPIRE_DURATION_SECS, ZALOPAY_MIN_EXPIRE_DURATION_SECS,
};
use crate::error::{PaymentError, Result};
use crate::telemetry::mask_secret;
use crate::vietqr::{execute_http_json_with_retry, execute_with_retry, CircuitBreaker, RetryError};
use crate::webhook_security::compute_hmac_hex;

type HmacSha256 = Hmac<Sha256>;

// =============================================================================
// ZaloPay v2 Return Code & Protocol Constants
// Official sources verified on 2026-11-24:
// - https://docs.zalopay.vn/docs/developer-tools/knowledge-base/status-codes
// - https://docs.zalopay.vn/docs/developer-tools/knowledge-base/callback
// - https://docs.zalopay.vn/docs/specs/callback-api
// =============================================================================

/// ZaloPay general API return_code values (`status-codes` doc, verified 2026-11-24).
pub const ZALOPAY_RETURN_SUCCESS: i32 = 1;
pub const ZALOPAY_RETURN_FAIL: i32 = 2;
pub const ZALOPAY_RETURN_PROCESSING: i32 = 3;

/// Merchant response `return_code` values returned to ZaloPay callback server (F2-bis;
/// `https://docs.zalopay.vn/docs/developer-tools/knowledge-base/callback`, verified 2026-11-24).
pub const ZALOPAY_CALLBACK_RETURN_SUCCESS: i32 = 1;
pub const ZALOPAY_CALLBACK_RETURN_INVALID_MAC: i32 = -1;
pub const ZALOPAY_CALLBACK_RETURN_RETRY: i32 = 0;

/// Threshold separating Unix seconds (`<= 1e11`) from Unix milliseconds (`> 1e11`) per F2-bis.
/// ZaloPay docs disagree across endpoints/callback types (ms vs s), so timestamps are normalized.
pub const ZALOPAY_SERVER_TIME_MILLIS_THRESHOLD: i64 = 100_000_000_000;

const MAX_APP_USER_CHARS: usize = 50;
const MAX_APP_TRANS_ID_LEN: usize = 40;
const MAX_DESCRIPTION_CHARS: usize = 256;
const MAX_ITEM_CHARS: usize = 2048;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ZaloPayReturnCode {
    Success,
    Fail,
    Processing,
    Unknown(i32),
}

impl ZaloPayReturnCode {
    pub fn from_i32(code: i32) -> Self {
        match code {
            ZALOPAY_RETURN_SUCCESS => Self::Success,
            ZALOPAY_RETURN_FAIL => Self::Fail,
            ZALOPAY_RETURN_PROCESSING => Self::Processing,
            other => Self::Unknown(other),
        }
    }

    pub fn as_i32(self) -> i32 {
        match self {
            Self::Success => ZALOPAY_RETURN_SUCCESS,
            Self::Fail => ZALOPAY_RETURN_FAIL,
            Self::Processing => ZALOPAY_RETURN_PROCESSING,
            Self::Unknown(v) => v,
        }
    }
}

// =============================================================================
// Pure Formatting, Timestamp Normalization & MAC Helpers
// =============================================================================

/// Generates a deterministic ZaloPay `app_trans_id` formatted as `yyMMdd_<unique>` using
/// Vietnam timezone (GMT+7 / `Asia/Ho_Chi_Minh`), strictly `<= 40` characters.
///
/// Spec reference (verified 2026-11-24):
/// - `https://docs.zalopay.vn/docs/specs/order-create` (`app_trans_id string(40)`, `yymmdd` GMT+7).
pub fn generate_app_trans_id(payment_id: Uuid, now: DateTime<Utc>) -> String {
    let vn_prefix = now.with_timezone(&vn_offset()).format("%y%m%d");
    // 6 (`yyMMdd`) + 1 (`_`) + 32 (simple UUID hex) = 39 <= 40 chars.
    format!("{vn_prefix}_{}", payment_id.simple())
}

/// Generates a deterministic ZaloPay `m_refund_id` formatted as `yyMMdd_<app_id>_<unique>` using
/// Vietnam timezone (GMT+7 / `Asia/Ho_Chi_Minh`).
///
/// Spec reference (verified 2026-11-24):
/// - `https://docs.zalopay.vn/docs/specs/order-refund` (`m_refund_id`: `yymmdd_appid_refund identifier`).
pub fn generate_m_refund_id(app_id: i32, refund_id: Uuid, now: DateTime<Utc>) -> String {
    let vn_prefix = now.with_timezone(&vn_offset()).format("%y%m%d");
    let uid = refund_id.simple().to_string();
    format!("{vn_prefix}_{app_id}_{}", &uid[..16])
}

/// Returns true iff `m_refund_id` starts with the current GMT+7 (`Asia/Ho_Chi_Minh`) `yyMMdd_`
/// date prefix (F4 / p4 Item D).
pub fn m_refund_id_matches_vn_day(m_refund_id: &str, now: DateTime<Utc>) -> bool {
    let expected = format!("{}_", now.with_timezone(&vn_offset()).format("%y%m%d"));
    m_refund_id.trim().starts_with(&expected)
}

/// Generates a fresh `m_refund_id` for the current GMT+7 day when rolling past 00:00 GMT+7 (p4 Item D).
/// Guarantees a value distinct from `previous_m_refund_id` so the DB UNIQUE constraint never conflicts.
pub fn rotate_m_refund_id_for_vn_day(
    app_id: i32,
    refund_id: Uuid,
    previous_m_refund_id: &str,
    now: DateTime<Utc>,
) -> String {
    let candidate = generate_m_refund_id(app_id, refund_id, now);
    if candidate != previous_m_refund_id {
        candidate
    } else {
        let vn_prefix = now.with_timezone(&vn_offset()).format("%y%m%d");
        let fresh = Uuid::new_v4().simple().to_string();
        format!("{vn_prefix}_{app_id}_{}", &fresh[..16])
    }
}

/// Clamps `expire_duration_seconds` to the range accepted by ZaloPay `/v2/create` (`300..=2_592_000`).
///
/// Spec reference (verified 2026-11-24):
/// - `https://docs.zalopay.vn/docs/specs/order-create` (`expire_duration_seconds`: min 300, max 2592000).
pub fn clamp_expire_duration_seconds(secs: i64) -> i64 {
    secs.clamp(
        ZALOPAY_MIN_EXPIRE_DURATION_SECS,
        ZALOPAY_MAX_EXPIRE_DURATION_SECS,
    )
}

/// Normalizes a ZaloPay `server_time` value (Unix seconds OR Unix milliseconds) into `DateTime<Utc>`.
///
/// Per F2-bis (verified 2026-11-24 across `https://docs.zalopay.vn/docs/specs/callback-api` and
/// `https://docs.zalopay.vn/docs/developer-tools/knowledge-base/callback`, where `server_time` is
/// documented as milliseconds for order callbacks and seconds for other callback types):
/// values strictly greater than `1e11` (`100_000_000_000`) are interpreted as Unix milliseconds;
/// values `<= 1e11` are interpreted as Unix seconds.
pub fn normalize_server_time(raw: i64) -> Result<DateTime<Utc>> {
    if raw <= 0 {
        return Err(PaymentError::Validation(
            "zalopay server_time must be a positive unix timestamp".to_string(),
        ));
    }
    let dt = if raw > ZALOPAY_SERVER_TIME_MILLIS_THRESHOLD {
        DateTime::from_timestamp_millis(raw)
    } else {
        DateTime::from_timestamp(raw, 0)
    };
    dt.ok_or_else(|| {
        PaymentError::Validation("zalopay server_time is out of valid timestamp range".to_string())
    })
}

/// Result of normalizing `server_time` and evaluating it against the replay window (F2 / F2-bis).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ServerTimeWindowCheck {
    pub normalized_at: DateTime<Utc>,
    pub drift_secs: i64,
    pub within_window: bool,
}

/// Normalizes `server_time` (seconds or milliseconds) and checks whether `|now - server_time|`
/// is within `max_skew_secs` (300s per AGENTS.md / F2).
///
/// Comparison is performed at millisecond precision so `300s + 1ms` is NOT silently rounded down
/// to `300s`.
pub fn check_server_time_window(
    raw_server_time: i64,
    now: DateTime<Utc>,
    max_skew_secs: i64,
) -> Result<ServerTimeWindowCheck> {
    if max_skew_secs < 0 {
        return Err(PaymentError::Validation(
            "max_skew_secs must be non-negative".to_string(),
        ));
    }
    let normalized_at = normalize_server_time(raw_server_time)?;
    let diff = now - normalized_at;
    let abs_ms = diff.num_milliseconds().abs();
    let max_ms = max_skew_secs.saturating_mul(1_000);
    let within_window = abs_ms <= max_ms;
    let drift_secs = diff.num_seconds().abs();
    Ok(ServerTimeWindowCheck {
        normalized_at,
        drift_secs,
        within_window,
    })
}

/// Computes `mac` for ZaloPay Create Order (`POST /v2/create`).
///
/// Official specification (verified 2026-11-24):
/// - URL: `https://docs.zalopay.vn/docs/specs/order-create`
/// - Algorithm: `HMAC-SHA256(key1, hmac_input)` encoded as lowercase hex
/// - Formula: `hmac_input = app_id + "|" + app_trans_id + "|" + app_user + "|" + amount + "|" + app_time + "|" + embed_data + "|" + item`
#[allow(clippy::too_many_arguments)]
pub fn compute_create_order_mac(
    key1: &str,
    app_id: i32,
    app_trans_id: &str,
    app_user: &str,
    amount_vnd: i64,
    app_time_ms: i64,
    embed_data: &str,
    item: &str,
) -> String {
    let input = format!(
        "{app_id}|{app_trans_id}|{app_user}|{amount_vnd}|{app_time_ms}|{embed_data}|{item}"
    );
    compute_hmac_hex(key1, input.as_bytes())
}

/// Computes `mac` for ZaloPay Query Order Status (`POST /v2/query`).
///
/// Official specification (verified 2026-11-24):
/// - URL: `https://docs.zalopay.vn/docs/specs/order-query`
/// - Algorithm: `HMAC-SHA256(key1, hmacinput)` encoded as lowercase hex
/// - Formula: `hmacinput = app_id + "|" + app_trans_id + "|" + key1`
pub fn compute_query_order_mac(key1: &str, app_id: i32, app_trans_id: &str) -> String {
    let input = format!("{app_id}|{app_trans_id}|{key1}");
    compute_hmac_hex(key1, input.as_bytes())
}

/// Computes `mac` for ZaloPay Refund Order (`POST /v2/refund`).
///
/// Official specification (verified 2026-11-24):
/// - URL: `https://docs.zalopay.vn/docs/specs/order-refund`
/// - Algorithm: `HMAC-SHA256(key1, hmacinput)` encoded as lowercase hex
/// - Formula (no refund fee):
///   `hmacinput = app_id + "|" + zp_trans_id + "|" + amount + "|" + description + "|" + timestamp`
/// - Formula (with `refund_fee_amount`):
///   `hmacinput = app_id + "|" + zp_trans_id + "|" + amount + "|" + refund_fee_amount + "|" + description + "|" + timestamp`
pub fn compute_refund_mac(
    key1: &str,
    app_id: i32,
    zp_trans_id: &str,
    amount_vnd: i64,
    refund_fee_amount_vnd: Option<i64>,
    description: &str,
    timestamp_ms: i64,
) -> String {
    let input = match refund_fee_amount_vnd {
        Some(fee) => {
            format!("{app_id}|{zp_trans_id}|{amount_vnd}|{fee}|{description}|{timestamp_ms}")
        }
        None => format!("{app_id}|{zp_trans_id}|{amount_vnd}|{description}|{timestamp_ms}"),
    };
    compute_hmac_hex(key1, input.as_bytes())
}

/// Computes `mac` for ZaloPay Query Refund Status (`POST /v2/query_refund`).
///
/// Official specification (verified 2026-11-24):
/// - URL: `https://docs.zalopay.vn/docs/specs/order-query-refund`
/// - Algorithm: `HMAC-SHA256(key1, hmacinput)` encoded as lowercase hex
/// - Formula: `hmacinput = app_id + "|" + m_refund_id + "|" + timestamp`
pub fn compute_query_refund_mac(
    key1: &str,
    app_id: i32,
    m_refund_id: &str,
    timestamp_ms: i64,
) -> String {
    let input = format!("{app_id}|{m_refund_id}|{timestamp_ms}");
    compute_hmac_hex(key1, input.as_bytes())
}

/// Computes `mac` for ZaloPay Callback / IPN payloads.
///
/// Official specification (verified 2026-11-24):
/// - URL: `https://docs.zalopay.vn/docs/specs/callback-api`
/// - URL: `https://docs.zalopay.vn/docs/developer-tools/knowledge-base/callback`
/// - Algorithm: `HMAC-SHA256(key2, raw_data)` encoded as lowercase hex
/// - Formula: `hmacinput = data` (exact raw UTF-8 string bytes before JSON parsing)
pub fn compute_callback_mac(key2: &str, raw_data: &str) -> String {
    compute_hmac_hex(key2, raw_data.as_bytes())
}

/// Constant-time HMAC-SHA256 verification of a ZaloPay Callback / IPN request.
///
/// Official specification (verified 2026-11-24):
/// - URL: `https://docs.zalopay.vn/docs/specs/callback-api`
/// - URL: `https://docs.zalopay.vn/docs/developer-tools/knowledge-base/callback`
/// - Formula: `HMAC-SHA256(key2, raw_data) == mac`
///
/// Security properties:
/// - Rejects empty/whitespace `key2` (never verifies with an unconfigured secret).
/// - Rejects empty/whitespace `mac`, non-hex strings, or non-32-byte digests.
/// - Accepts both lowercase and uppercase valid 64-character hex digests (comparing decoded
///   32-byte MAC values in constant time via `hmac::Mac::verify_slice`).
/// - Never logs `key2`, full `mac`, or `raw_data`.
pub fn verify_callback(raw_data: &str, mac: &str, key2: &str) -> Result<()> {
    if key2.trim().is_empty() {
        return Err(PaymentError::Unauthorized(
            "zalopay callback key2 is not configured".to_string(),
        ));
    }
    let trimmed_mac = mac.trim();
    if trimmed_mac.is_empty() {
        return Err(PaymentError::Unauthorized(
            "missing zalopay callback mac".to_string(),
        ));
    }
    let provided_bytes = hex::decode(trimmed_mac).map_err(|_| {
        PaymentError::Unauthorized("malformed zalopay callback mac hex".to_string())
    })?;
    if provided_bytes.len() != 32 {
        return Err(PaymentError::Unauthorized(
            "invalid zalopay callback mac length".to_string(),
        ));
    }

    let mut verifier =
        HmacSha256::new_from_slice(key2.as_bytes()).expect("HMAC accepts key of any length");
    verifier.update(raw_data.as_bytes());
    verifier
        .verify_slice(&provided_bytes)
        .map_err(|_| PaymentError::Unauthorized("invalid zalopay callback mac".to_string()))
}

// =============================================================================
// API Request / Response & Callback DTOs
// =============================================================================

#[derive(Debug, Clone)]
pub struct ZaloPayCreateOrderInput {
    pub app_trans_id: String,
    pub app_user: String,
    pub amount_vnd: i64,
    pub description: String,
    pub embed_data: Option<String>,
    pub item: Option<String>,
    pub callback_url: Option<String>,
    pub expire_duration_seconds: Option<i64>,
    pub bank_code: Option<String>,
    pub sub_app_id: Option<String>,
    pub app_time: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ZaloPayCreateOrderResponse {
    pub return_code: i32,
    #[serde(default)]
    pub return_message: String,
    #[serde(default)]
    pub sub_return_code: i32,
    #[serde(default)]
    pub sub_return_message: String,
    #[serde(default)]
    pub zp_trans_token: String,
    #[serde(default)]
    pub order_url: String,
    #[serde(default)]
    pub order_token: String,
    #[serde(default)]
    pub qr_code: String,
}

impl ZaloPayCreateOrderResponse {
    pub fn status(&self) -> ZaloPayReturnCode {
        ZaloPayReturnCode::from_i32(self.return_code)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ZaloPayQueryOrderResponse {
    pub return_code: i32,
    #[serde(default)]
    pub return_message: String,
    #[serde(default)]
    pub sub_return_code: i32,
    #[serde(default)]
    pub sub_return_message: String,
    #[serde(default)]
    pub is_processing: bool,
    #[serde(default, deserialize_with = "deserialize_i64_or_string")]
    pub amount: i64,
    #[serde(default, deserialize_with = "deserialize_i64_or_string")]
    pub zp_trans_id: i64,
    #[serde(default, deserialize_with = "deserialize_i64_or_string")]
    pub server_time: i64,
    #[serde(default, deserialize_with = "deserialize_i64_or_string")]
    pub discount_amount: i64,
}

impl ZaloPayQueryOrderResponse {
    pub fn status(&self) -> ZaloPayReturnCode {
        ZaloPayReturnCode::from_i32(self.return_code)
    }

    /// Returns the normalized UTC timestamp for `server_time` if present (`> 0`).
    pub fn normalized_server_time(&self) -> Result<Option<DateTime<Utc>>> {
        if self.server_time <= 0 {
            return Ok(None);
        }
        normalize_server_time(self.server_time).map(Some)
    }
}

#[derive(Debug, Clone)]
pub struct ZaloPayRefundInput {
    pub m_refund_id: String,
    pub zp_trans_id: String,
    pub amount_vnd: i64,
    pub refund_fee_amount_vnd: Option<i64>,
    pub description: String,
    pub timestamp: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ZaloPayRefundResponse {
    pub return_code: i32,
    #[serde(default)]
    pub return_message: String,
    #[serde(default)]
    pub sub_return_code: i32,
    #[serde(default)]
    pub sub_return_message: String,
    #[serde(default, deserialize_with = "deserialize_i64_or_string")]
    pub refund_id: i64,
}

impl ZaloPayRefundResponse {
    pub fn status(&self) -> ZaloPayReturnCode {
        ZaloPayReturnCode::from_i32(self.return_code)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ZaloPayQueryRefundResponse {
    pub return_code: i32,
    #[serde(default)]
    pub return_message: String,
    #[serde(default)]
    pub sub_return_code: i32,
    #[serde(default)]
    pub sub_return_message: String,
}

impl ZaloPayQueryRefundResponse {
    pub fn status(&self) -> ZaloPayReturnCode {
        ZaloPayReturnCode::from_i32(self.return_code)
    }
}

/// Incoming ZaloPay Callback / IPN JSON envelope (`POST /webhooks/zalopay`).
///
/// Custom `Debug` redacts raw `data` and masks `mac` so sensitive callback content is never logged.
#[derive(Clone, Serialize, Deserialize)]
pub struct ZaloPayCallbackRequest {
    pub data: String,
    pub mac: String,
    #[serde(rename = "type", default = "default_callback_type")]
    pub callback_type: i32,
}

fn default_callback_type() -> i32 {
    1
}

impl fmt::Debug for ZaloPayCallbackRequest {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ZaloPayCallbackRequest")
            .field("data", &"[REDACTED]")
            .field("data_len", &self.data.len())
            .field("mac", &mask_secret(&self.mac))
            .field("callback_type", &self.callback_type)
            .finish()
    }
}

/// Parsed JSON fields inside `ZaloPayCallbackRequest.data`.
///
/// Spec reference (verified 2026-11-24):
/// - `https://docs.zalopay.vn/docs/specs/callback-api`
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ZaloPayCallbackData {
    pub app_id: i32,
    pub app_trans_id: String,
    #[serde(default, deserialize_with = "deserialize_i64_or_string")]
    pub app_time: i64,
    #[serde(default)]
    pub app_user: String,
    #[serde(deserialize_with = "deserialize_i64_or_string")]
    pub amount: i64,
    #[serde(default)]
    pub embed_data: String,
    #[serde(default)]
    pub item: String,
    #[serde(deserialize_with = "deserialize_i64_or_string")]
    pub zp_trans_id: i64,
    #[serde(deserialize_with = "deserialize_i64_or_string")]
    pub server_time: i64,
    #[serde(default)]
    pub channel: i32,
    #[serde(default)]
    pub merchant_user_id: String,
    #[serde(default, deserialize_with = "deserialize_i64_or_string")]
    pub user_fee_amount: i64,
    #[serde(default, deserialize_with = "deserialize_i64_or_string")]
    pub discount_amount: i64,
}

impl ZaloPayCallbackData {
    pub fn zp_trans_id_string(&self) -> String {
        self.zp_trans_id.to_string()
    }
}

/// Verified callback result produced by [`verify_and_parse_callback`].
#[derive(Debug, Clone)]
pub struct VerifiedZaloPayCallback {
    pub data: ZaloPayCallbackData,
    pub callback_type: i32,
    pub server_time: ServerTimeWindowCheck,
}

/// Verifies `req.mac` in constant time against raw `req.data` using `key2`, then parses `data`
/// and checks `server_time` against `replay_window_secs` (F2 / F2-bis).
///
/// Note: When `server_time.within_window == false`, callers MUST NOT confirm directly from the
/// callback body and MUST query `/v2/query` instead (F2).
pub fn verify_and_parse_callback(
    req: &ZaloPayCallbackRequest,
    key2: &str,
    now: DateTime<Utc>,
    replay_window_secs: i64,
) -> Result<VerifiedZaloPayCallback> {
    verify_callback(&req.data, &req.mac, key2)?;

    let data: ZaloPayCallbackData = serde_json::from_str(&req.data)
        .map_err(|_| PaymentError::Validation("invalid zalopay callback data json".to_string()))?;

    if data.app_id <= 0 {
        return Err(PaymentError::Validation(
            "zalopay callback app_id must be positive".to_string(),
        ));
    }
    if data.app_trans_id.trim().is_empty() {
        return Err(PaymentError::Validation(
            "zalopay callback app_trans_id is required".to_string(),
        ));
    }
    if data.amount <= 0 {
        return Err(PaymentError::Validation(
            "zalopay callback amount must be positive integer VND".to_string(),
        ));
    }
    if data.zp_trans_id <= 0 {
        return Err(PaymentError::Validation(
            "zalopay callback zp_trans_id must be positive".to_string(),
        ));
    }

    let server_time = check_server_time_window(data.server_time, now, replay_window_secs)?;
    Ok(VerifiedZaloPayCallback {
        data,
        callback_type: req.callback_type,
        server_time,
    })
}

/// Wire request structs serialized directly to ZaloPay v2 JSON endpoints.
#[derive(Serialize)]
struct WireCreateOrderRequest {
    app_id: i32,
    app_user: String,
    app_trans_id: String,
    app_time: i64,
    amount: i64,
    description: String,
    item: String,
    embed_data: String,
    bank_code: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    callback_url: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    expire_duration_seconds: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    sub_app_id: Option<String>,
    mac: String,
}

#[derive(Serialize)]
struct WireQueryOrderRequest {
    app_id: i32,
    app_trans_id: String,
    mac: String,
}

#[derive(Serialize)]
struct WireRefundRequest {
    app_id: i32,
    m_refund_id: String,
    zp_trans_id: String,
    amount: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    refund_fee_amount: Option<i64>,
    timestamp: i64,
    description: String,
    mac: String,
}

#[derive(Serialize)]
struct WireQueryRefundRequest {
    app_id: i32,
    m_refund_id: String,
    timestamp: i64,
    mac: String,
}

// =============================================================================
// In-Process Deterministic MockZaloPay (F1 / F2-bis)
// =============================================================================

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum MockOperation {
    CreateOrder,
    QueryOrder,
    Refund,
    QueryRefund,
}

/// Injectable behavior for [`MockZaloPay`] allowing unit/integration tests to force return codes
/// `1` (success), `2` (fail), `3` (processing), or simulated transient network errors.
#[derive(Debug, Clone)]
pub enum MockBehavior {
    ReturnCode {
        return_code: i32,
        sub_return_code: i32,
        return_message: String,
        amount_vnd: Option<i64>,
        zp_trans_id: Option<i64>,
        server_time: Option<i64>,
        is_processing: Option<bool>,
    },
    NetworkError(String),
}

impl MockBehavior {
    pub fn return_code(code: i32) -> Self {
        let (sub_code, msg) = match code {
            ZALOPAY_RETURN_SUCCESS => (1, "success"),
            ZALOPAY_RETURN_FAIL => (-1, "failed"),
            ZALOPAY_RETURN_PROCESSING => (3, "processing"),
            _ => (code, "custom"),
        };
        Self::ReturnCode {
            return_code: code,
            sub_return_code: sub_code,
            return_message: msg.to_string(),
            amount_vnd: None,
            zp_trans_id: None,
            server_time: None,
            is_processing: None,
        }
    }

    /// Unpaid/abandoned order (`return_code = 3, is_processing = false`).
    pub fn unpaid() -> Self {
        Self::ReturnCode {
            return_code: ZALOPAY_RETURN_PROCESSING,
            sub_return_code: -49,
            return_message: "Đơn hàng chưa thanh toán".to_string(),
            amount_vnd: None,
            zp_trans_id: None,
            server_time: None,
            is_processing: Some(false),
        }
    }

    /// Actively processing order (`return_code = 3, is_processing = true`).
    pub fn processing() -> Self {
        Self::ReturnCode {
            return_code: ZALOPAY_RETURN_PROCESSING,
            sub_return_code: 3,
            return_message: "processing".to_string(),
            amount_vnd: None,
            zp_trans_id: None,
            server_time: None,
            is_processing: Some(true),
        }
    }

    pub fn network_error(msg: impl Into<String>) -> Self {
        Self::NetworkError(msg.into())
    }
}

#[derive(Debug, Clone)]
struct MockOrderRecord {
    app_id: i32,
    amount_vnd: i64,
    zp_trans_id: i64,
    created_at_ms: i64,
}

#[derive(Debug, Clone)]
struct MockRefundRecord {
    return_code: i32,
    sub_return_code: i32,
    return_message: String,
}

type MockOperationGate = (Arc<tokio::sync::Notify>, Arc<tokio::sync::Notify>);

#[derive(Debug, Default)]
struct MockZaloPayState {
    orders: HashMap<String, MockOrderRecord>,
    refunds: HashMap<String, MockRefundRecord>,
    order_outcomes: HashMap<String, MockBehavior>,
    refund_outcomes: HashMap<String, MockBehavior>,
    query_refund_outcomes: HashMap<String, MockBehavior>,
    queued_ops: HashMap<MockOperation, VecDeque<MockBehavior>>,
    query_order_counts: HashMap<String, usize>,
    refund_counts: HashMap<String, usize>,
    query_refund_counts: HashMap<String, usize>,
    operation_gates: HashMap<MockOperation, VecDeque<MockOperationGate>>,
}

/// Deterministic in-process ZaloPay mock engine shared across clones of [`ZaloPayAdapter`].
#[derive(Debug, Clone, Default)]
pub struct MockZaloPay {
    state: Arc<Mutex<MockZaloPayState>>,
}

impl MockZaloPay {
    pub fn new() -> Self {
        Self::default()
    }

    /// Enqueues a one-shot behavior (FIFO) for the next invocation of `op`.
    pub fn push_behavior(&self, op: MockOperation, behavior: MockBehavior) {
        let mut guard = self.state.lock().expect("mock zalopay lock");
        guard.queued_ops.entry(op).or_default().push_back(behavior);
    }

    /// Sets a sticky query outcome for a specific `app_trans_id` (e.g. `1`, `2`, `3`, or network error).
    pub fn set_order_outcome(&self, app_trans_id: impl Into<String>, behavior: MockBehavior) {
        let mut guard = self.state.lock().expect("mock zalopay lock");
        guard.order_outcomes.insert(app_trans_id.into(), behavior);
    }

    /// Sets a sticky `/v2/refund` outcome for a specific `m_refund_id`.
    pub fn set_refund_outcome(&self, m_refund_id: impl Into<String>, behavior: MockBehavior) {
        let mut guard = self.state.lock().expect("mock zalopay lock");
        guard.refund_outcomes.insert(m_refund_id.into(), behavior);
    }

    /// Sets a sticky `/v2/query_refund` outcome for a specific `m_refund_id`.
    pub fn set_query_refund_outcome(&self, m_refund_id: impl Into<String>, behavior: MockBehavior) {
        let mut guard = self.state.lock().expect("mock zalopay lock");
        guard
            .query_refund_outcomes
            .insert(m_refund_id.into(), behavior);
    }

    /// Returns how many times `/v2/query` was invoked on this mock for `app_trans_id`.
    pub fn query_order_calls(&self, app_trans_id: &str) -> usize {
        let guard = self.state.lock().expect("mock zalopay lock");
        guard
            .query_order_counts
            .get(app_trans_id)
            .copied()
            .unwrap_or(0)
    }

    /// Returns how many times `/v2/refund` was invoked on this mock for `m_refund_id`.
    pub fn refund_calls(&self, m_refund_id: &str) -> usize {
        let guard = self.state.lock().expect("mock zalopay lock");
        guard.refund_counts.get(m_refund_id).copied().unwrap_or(0)
    }

    /// Returns how many times `/v2/query_refund` was invoked on this mock for `m_refund_id`.
    pub fn query_refund_calls(&self, m_refund_id: &str) -> usize {
        let guard = self.state.lock().expect("mock zalopay lock");
        guard
            .query_refund_counts
            .get(m_refund_id)
            .copied()
            .unwrap_or(0)
    }

    /// Enqueues a one-shot deterministic async pause gate for `op` (used in concurrency race tests):
    /// notifies `entered` right after executing the mock operation and waits for `proceed` before returning.
    pub fn push_operation_gate(
        &self,
        op: MockOperation,
        entered: Arc<tokio::sync::Notify>,
        proceed: Arc<tokio::sync::Notify>,
    ) {
        let mut guard = self.state.lock().expect("mock zalopay lock");
        guard
            .operation_gates
            .entry(op)
            .or_default()
            .push_back((entered, proceed));
    }

    fn pop_operation_gate(
        &self,
        op: MockOperation,
    ) -> Option<(Arc<tokio::sync::Notify>, Arc<tokio::sync::Notify>)> {
        let mut guard = self.state.lock().expect("mock zalopay lock");
        guard
            .operation_gates
            .get_mut(&op)
            .and_then(VecDeque::pop_front)
    }

    async fn await_gate_if_armed(&self, op: MockOperation) {
        if let Some((entered, proceed)) = self.pop_operation_gate(op) {
            entered.notify_one();
            proceed.notified().await;
        }
    }

    /// Clears all queued and sticky overrides (keeping recorded orders/refunds).
    pub fn clear_overrides(&self) {
        let mut guard = self.state.lock().expect("mock zalopay lock");
        guard.order_outcomes.clear();
        guard.refund_outcomes.clear();
        guard.query_refund_outcomes.clear();
        guard.queued_ops.clear();
    }

    /// Deterministic mock `zp_trans_id` derived from `app_trans_id`.
    pub fn deterministic_zp_trans_id(app_trans_id: &str) -> i64 {
        deterministic_positive_id("zalopay_mock_tx:", app_trans_id)
    }

    /// Deterministic mock `refund_id` derived from `m_refund_id`.
    pub fn deterministic_refund_id(m_refund_id: &str) -> i64 {
        deterministic_positive_id("zalopay_mock_refund:", m_refund_id)
    }

    /// Builds a valid signed ZaloPay callback envelope (`{"data": "...", "mac": "...", "type": 1}`)
    /// using the verified callback MAC formula `HMAC-SHA256(key2, raw_data)`.
    pub fn build_signed_callback(
        key2: &str,
        payload: &ZaloPayCallbackData,
    ) -> Result<ZaloPayCallbackRequest> {
        if key2.trim().is_empty() {
            return Err(PaymentError::Validation(
                "callback key2 must not be empty when signing mock callback".to_string(),
            ));
        }
        let raw_data = serde_json::to_string(payload)
            .map_err(|e| PaymentError::Internal(format!("serialize callback data: {e}")))?;
        let mac = compute_callback_mac(key2, &raw_data);
        Ok(ZaloPayCallbackRequest {
            data: raw_data,
            mac,
            callback_type: 1,
        })
    }

    fn pop_queued(&self, op: MockOperation) -> Option<MockBehavior> {
        let mut guard = self.state.lock().expect("mock zalopay lock");
        guard.queued_ops.get_mut(&op).and_then(VecDeque::pop_front)
    }

    fn handle_create_order(
        &self,
        req: &WireCreateOrderRequest,
        key1: &str,
    ) -> std::result::Result<ZaloPayCreateOrderResponse, RetryError> {
        if let Some(behavior) = self.pop_queued(MockOperation::CreateOrder) {
            return match behavior {
                MockBehavior::NetworkError(msg) => {
                    Err(RetryError::Retryable(PaymentError::Provider(msg)))
                }
                MockBehavior::ReturnCode {
                    return_code,
                    sub_return_code,
                    return_message,
                    ..
                } => Ok(self.build_create_response(
                    req,
                    return_code,
                    sub_return_code,
                    return_message,
                )),
            };
        }

        let expected_mac = compute_create_order_mac(
            key1,
            req.app_id,
            &req.app_trans_id,
            &req.app_user,
            req.amount,
            req.app_time,
            &req.embed_data,
            &req.item,
        );
        if !constant_time_hex_eq(&expected_mac, &req.mac) {
            return Ok(ZaloPayCreateOrderResponse {
                return_code: ZALOPAY_RETURN_FAIL,
                return_message: "invalid mac".to_string(),
                sub_return_code: -402,
                sub_return_message: "ILLEGAL_APP/SIGNATURE_REQUEST".to_string(),
                zp_trans_token: String::new(),
                order_url: String::new(),
                order_token: String::new(),
                qr_code: String::new(),
            });
        }

        Ok(self.build_create_response(
            req,
            ZALOPAY_RETURN_SUCCESS,
            1,
            "Giao dịch thành công".to_string(),
        ))
    }

    fn build_create_response(
        &self,
        req: &WireCreateOrderRequest,
        return_code: i32,
        sub_return_code: i32,
        return_message: String,
    ) -> ZaloPayCreateOrderResponse {
        let zp_trans_id = Self::deterministic_zp_trans_id(&req.app_trans_id);
        let token = deterministic_token("zalopay_mock_token:", &req.app_trans_id);
        if return_code == ZALOPAY_RETURN_SUCCESS {
            let mut guard = self.state.lock().expect("mock zalopay lock");
            guard.orders.insert(
                req.app_trans_id.clone(),
                MockOrderRecord {
                    app_id: req.app_id,
                    amount_vnd: req.amount,
                    zp_trans_id,
                    created_at_ms: req.app_time,
                },
            );
            // Same-origin relative mock page URL per F8 ("mock dùng trang mô phỏng cùng origin").
            let order_url = format!(
                "/thanh-toan/mock-zalopay?app_trans_id={}&amount={}",
                req.app_trans_id, req.amount
            );
            ZaloPayCreateOrderResponse {
                return_code,
                return_message,
                sub_return_code,
                sub_return_message: String::new(),
                zp_trans_token: format!("zpt_{token}"),
                order_url,
                order_token: format!("ord_{token}"),
                qr_code: format!("ZALOPAY_MOCK_QR:{}:{}", req.app_trans_id, req.amount),
            }
        } else {
            ZaloPayCreateOrderResponse {
                return_code,
                return_message,
                sub_return_code,
                sub_return_message: String::new(),
                zp_trans_token: String::new(),
                order_url: String::new(),
                order_token: String::new(),
                qr_code: String::new(),
            }
        }
    }

    fn handle_query_order(
        &self,
        req: &WireQueryOrderRequest,
        key1: &str,
    ) -> std::result::Result<ZaloPayQueryOrderResponse, RetryError> {
        let expected_mac = compute_query_order_mac(key1, req.app_id, &req.app_trans_id);
        if !constant_time_hex_eq(&expected_mac, &req.mac) {
            return Ok(ZaloPayQueryOrderResponse {
                return_code: ZALOPAY_RETURN_FAIL,
                return_message: "invalid mac".to_string(),
                sub_return_code: -402,
                sub_return_message: "ILLEGAL_APP/SIGNATURE_REQUEST".to_string(),
                is_processing: false,
                amount: 0,
                zp_trans_id: 0,
                server_time: 0,
                discount_amount: 0,
            });
        }

        {
            let mut guard = self.state.lock().expect("mock zalopay lock");
            *guard
                .query_order_counts
                .entry(req.app_trans_id.clone())
                .or_insert(0) += 1;
        }

        let behavior = self.pop_queued(MockOperation::QueryOrder).or_else(|| {
            let guard = self.state.lock().expect("mock zalopay lock");
            guard.order_outcomes.get(&req.app_trans_id).cloned()
        });

        let existing = {
            let guard = self.state.lock().expect("mock zalopay lock");
            guard.orders.get(&req.app_trans_id).cloned()
        };

        if let Some(b) = behavior {
            return match b {
                MockBehavior::NetworkError(msg) => {
                    Err(RetryError::Retryable(PaymentError::Provider(msg)))
                }
                MockBehavior::ReturnCode {
                    return_code,
                    sub_return_code,
                    return_message,
                    amount_vnd,
                    zp_trans_id,
                    server_time,
                    is_processing,
                } => {
                    let is_success = return_code == ZALOPAY_RETURN_SUCCESS;
                    let is_processing =
                        is_processing.unwrap_or(return_code == ZALOPAY_RETURN_PROCESSING);
                    let resolved_amount = amount_vnd
                        .or_else(|| existing.as_ref().map(|o| o.amount_vnd))
                        .unwrap_or(0);
                    let resolved_tx_id = zp_trans_id
                        .or_else(|| existing.as_ref().map(|o| o.zp_trans_id))
                        .unwrap_or_else(|| Self::deterministic_zp_trans_id(&req.app_trans_id));
                    let resolved_time = server_time
                        .or_else(|| existing.as_ref().map(|o| o.created_at_ms))
                        .unwrap_or_else(|| Utc::now().timestamp_millis());
                    Ok(ZaloPayQueryOrderResponse {
                        return_code,
                        return_message,
                        sub_return_code,
                        sub_return_message: String::new(),
                        is_processing,
                        amount: if is_success { resolved_amount } else { 0 },
                        zp_trans_id: if is_success { resolved_tx_id } else { 0 },
                        server_time: resolved_time,
                        discount_amount: 0,
                    })
                }
            };
        }

        match existing {
            Some(order) if order.app_id == req.app_id => Ok(ZaloPayQueryOrderResponse {
                return_code: ZALOPAY_RETURN_PROCESSING,
                return_message: "Đơn hàng đang chờ thanh toán".to_string(),
                sub_return_code: -49,
                sub_return_message: String::new(),
                is_processing: false,
                amount: 0,
                zp_trans_id: 0,
                server_time: order.created_at_ms,
                discount_amount: 0,
            }),
            _ => Ok(ZaloPayQueryOrderResponse {
                return_code: ZALOPAY_RETURN_FAIL,
                return_message: "order not found".to_string(),
                sub_return_code: -101,
                sub_return_message: "ORDER_NOT_EXIST".to_string(),
                is_processing: false,
                amount: 0,
                zp_trans_id: 0,
                server_time: 0,
                discount_amount: 0,
            }),
        }
    }

    fn handle_refund(
        &self,
        req: &WireRefundRequest,
        key1: &str,
    ) -> std::result::Result<ZaloPayRefundResponse, RetryError> {
        let expected_mac = compute_refund_mac(
            key1,
            req.app_id,
            &req.zp_trans_id,
            req.amount,
            req.refund_fee_amount,
            &req.description,
            req.timestamp,
        );
        if !constant_time_hex_eq(&expected_mac, &req.mac) {
            return Ok(ZaloPayRefundResponse {
                return_code: ZALOPAY_RETURN_FAIL,
                return_message: "invalid mac".to_string(),
                sub_return_code: -402,
                sub_return_message: "ILLEGAL_APP/SIGNATURE_REQUEST".to_string(),
                refund_id: 0,
            });
        }

        {
            let mut guard = self.state.lock().expect("mock zalopay lock");
            *guard
                .refund_counts
                .entry(req.m_refund_id.clone())
                .or_insert(0) += 1;
        }

        let behavior = self.pop_queued(MockOperation::Refund).or_else(|| {
            let guard = self.state.lock().expect("mock zalopay lock");
            guard.refund_outcomes.get(&req.m_refund_id).cloned()
        });

        let refund_id = Self::deterministic_refund_id(&req.m_refund_id);
        let (return_code, sub_return_code, return_message) = match behavior {
            Some(MockBehavior::NetworkError(msg)) => {
                return Err(RetryError::Retryable(PaymentError::Provider(msg)));
            }
            Some(MockBehavior::ReturnCode {
                return_code,
                sub_return_code,
                return_message,
                ..
            }) => (return_code, sub_return_code, return_message),
            None => (
                ZALOPAY_RETURN_SUCCESS,
                1,
                "Hoàn tiền thành công".to_string(),
            ),
        };

        {
            let mut guard = self.state.lock().expect("mock zalopay lock");
            guard.refunds.insert(
                req.m_refund_id.clone(),
                MockRefundRecord {
                    return_code,
                    sub_return_code,
                    return_message: return_message.clone(),
                },
            );
        }

        Ok(ZaloPayRefundResponse {
            return_code,
            return_message,
            sub_return_code,
            sub_return_message: String::new(),
            refund_id: if return_code == ZALOPAY_RETURN_FAIL {
                0
            } else {
                refund_id
            },
        })
    }

    fn handle_query_refund(
        &self,
        req: &WireQueryRefundRequest,
        key1: &str,
    ) -> std::result::Result<ZaloPayQueryRefundResponse, RetryError> {
        let expected_mac =
            compute_query_refund_mac(key1, req.app_id, &req.m_refund_id, req.timestamp);
        if !constant_time_hex_eq(&expected_mac, &req.mac) {
            return Ok(ZaloPayQueryRefundResponse {
                return_code: ZALOPAY_RETURN_FAIL,
                return_message: "invalid mac".to_string(),
                sub_return_code: -402,
                sub_return_message: "ILLEGAL_APP/SIGNATURE_REQUEST".to_string(),
            });
        }

        {
            let mut guard = self.state.lock().expect("mock zalopay lock");
            *guard
                .query_refund_counts
                .entry(req.m_refund_id.clone())
                .or_insert(0) += 1;
        }

        let behavior = self.pop_queued(MockOperation::QueryRefund).or_else(|| {
            let guard = self.state.lock().expect("mock zalopay lock");
            guard.query_refund_outcomes.get(&req.m_refund_id).cloned()
        });

        if let Some(b) = behavior {
            return match b {
                MockBehavior::NetworkError(msg) => {
                    Err(RetryError::Retryable(PaymentError::Provider(msg)))
                }
                MockBehavior::ReturnCode {
                    return_code,
                    sub_return_code,
                    return_message,
                    ..
                } => Ok(ZaloPayQueryRefundResponse {
                    return_code,
                    return_message,
                    sub_return_code,
                    sub_return_message: String::new(),
                }),
            };
        }

        let guard = self.state.lock().expect("mock zalopay lock");
        match guard.refunds.get(&req.m_refund_id) {
            Some(rec) => Ok(ZaloPayQueryRefundResponse {
                return_code: rec.return_code,
                return_message: rec.return_message.clone(),
                sub_return_code: rec.sub_return_code,
                sub_return_message: String::new(),
            }),
            None => Ok(ZaloPayQueryRefundResponse {
                return_code: ZALOPAY_RETURN_FAIL,
                return_message: "refund not found".to_string(),
                sub_return_code: -101,
                sub_return_message: "M_REFUND_ID_NOT_FOUND".to_string(),
            }),
        }
    }
}

// =============================================================================
// ZaloPayAdapter (Mock | Sandbox) — reuses CircuitBreaker & execute_with_retry
// =============================================================================

#[derive(Clone)]
pub struct ZaloPayAdapter {
    config: ZaloPayConfig,
    http_client: Client,
    breaker: CircuitBreaker,
    mock: Option<MockZaloPay>,
    mock_backoff: Duration,
}

impl fmt::Debug for ZaloPayAdapter {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ZaloPayAdapter")
            .field("config", &self.config)
            .field("circuit_state", &self.breaker.state())
            .field("has_mock", &self.mock.is_some())
            .finish()
    }
}

impl ZaloPayAdapter {
    pub fn from_config(cfg: &Config) -> Result<Self> {
        Self::try_from_zalopay_config(&cfg.zalopay)
    }

    pub fn try_from_zalopay_config(cfg: &ZaloPayConfig) -> Result<Self> {
        cfg.validate().map_err(PaymentError::Validation)?;
        let http_client = Client::builder()
            .timeout(cfg.http_timeout)
            .build()
            .map_err(|e| PaymentError::Internal(format!("zalopay reqwest client build: {e}")))?;

        let mock = match cfg.mode {
            ZaloPayMode::Mock => Some(MockZaloPay::new()),
            ZaloPayMode::Sandbox => None,
        };

        Ok(Self {
            config: cfg.clone(),
            http_client,
            breaker: CircuitBreaker::new(5, Duration::from_secs(30)),
            mock,
            mock_backoff: Duration::from_millis(1),
        })
    }

    /// Creates a mock-mode adapter backed by the provided [`MockZaloPay`] instance.
    pub fn new_mock(mock: MockZaloPay) -> Self {
        let cfg = ZaloPayConfig::mock_default();
        let http_client = Client::builder()
            .timeout(cfg.http_timeout)
            .build()
            .expect("reqwest client build");
        Self {
            config: cfg,
            http_client,
            breaker: CircuitBreaker::new(5, Duration::from_secs(30)),
            mock: Some(mock),
            mock_backoff: Duration::from_millis(1),
        }
    }

    /// Replaces the circuit breaker (useful in tests with short open durations).
    pub fn with_circuit_breaker(mut self, breaker: CircuitBreaker) -> Self {
        self.breaker = breaker;
        self
    }

    /// Overrides the base retry backoff in mock mode (defaults to 1 ms for fast tests).
    pub fn with_mock_backoff(mut self, backoff: Duration) -> Self {
        self.mock_backoff = backoff;
        self
    }

    pub fn mode(&self) -> ZaloPayMode {
        self.config.mode
    }

    pub fn config(&self) -> &ZaloPayConfig {
        &self.config
    }

    pub fn breaker(&self) -> &CircuitBreaker {
        &self.breaker
    }

    pub fn mock(&self) -> Option<&MockZaloPay> {
        self.mock.as_ref()
    }

    /// Returns the effective `app_id` (required in sandbox; falls back to local mock ID in mock).
    pub fn effective_app_id(&self) -> Result<i32> {
        self.config.effective_app_id().ok_or_else(|| {
            PaymentError::Validation("ZALOPAY_APP_ID is required in sandbox mode".to_string())
        })
    }

    /// Returns the effective `key1` (`dev-only-zalopay-mock-key1` fallback strictly in `mock` mode).
    pub fn effective_key1(&self) -> &str {
        self.config.effective_key1()
    }

    /// Returns the effective `key2` (`dev-only-zalopay-mock-key2` fallback strictly in `mock` mode).
    pub fn effective_key2(&self) -> &str {
        self.config.effective_key2()
    }

    /// Calls ZaloPay Create Order (`POST {base_url}/v2/create`).
    ///
    /// Official specification: `https://docs.zalopay.vn/docs/specs/order-create` (verified 2026-11-24).
    pub async fn create_order(
        &self,
        input: &ZaloPayCreateOrderInput,
    ) -> Result<ZaloPayCreateOrderResponse> {
        let app_trans_id = input.app_trans_id.trim();
        if app_trans_id.is_empty() || app_trans_id.len() > MAX_APP_TRANS_ID_LEN {
            return Err(PaymentError::Validation(format!(
                "zalopay app_trans_id must be 1..={MAX_APP_TRANS_ID_LEN} characters"
            )));
        }
        if input.amount_vnd <= 0 {
            return Err(PaymentError::Validation(
                "zalopay amount_vnd must be positive integer VND".to_string(),
            ));
        }

        let app_id = self.effective_app_id()?;
        let app_user_trimmed = input.app_user.trim();
        let app_user = if app_user_trimmed.is_empty() {
            "nexusmart_user".to_string()
        } else {
            truncate_chars(app_user_trimmed, MAX_APP_USER_CHARS)
        };
        let desc_trimmed = input.description.trim();
        let description = if desc_trimmed.is_empty() {
            format!("NexusMart {app_trans_id}")
        } else {
            truncate_chars(desc_trimmed, MAX_DESCRIPTION_CHARS)
        };
        let embed_data = match input
            .embed_data
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty())
        {
            Some(v) => v.to_string(),
            None if !self.config.redirect_url.is_empty() => {
                serde_json::json!({ "redirecturl": self.config.redirect_url }).to_string()
            }
            None => "{}".to_string(),
        };
        let item = input
            .item
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(|s| truncate_chars(s, MAX_ITEM_CHARS))
            .unwrap_or_else(|| "[]".to_string());
        let callback_url = input
            .callback_url
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(ToString::to_string)
            .or_else(|| {
                let cfg_cb = self.config.callback_url.trim();
                if cfg_cb.is_empty() {
                    None
                } else {
                    Some(cfg_cb.to_string())
                }
            });
        let expire_duration_seconds = input
            .expire_duration_seconds
            .map(clamp_expire_duration_seconds);
        let bank_code = input
            .bank_code
            .as_deref()
            .map(str::trim)
            .unwrap_or("")
            .to_string();
        let sub_app_id = input
            .sub_app_id
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(ToString::to_string);
        let app_time_ms = input.app_time.unwrap_or_else(Utc::now).timestamp_millis();

        let key1 = self.effective_key1();
        let mac = compute_create_order_mac(
            key1,
            app_id,
            app_trans_id,
            &app_user,
            input.amount_vnd,
            app_time_ms,
            &embed_data,
            &item,
        );

        let wire = WireCreateOrderRequest {
            app_id,
            app_user,
            app_trans_id: app_trans_id.to_string(),
            app_time: app_time_ms,
            amount: input.amount_vnd,
            description,
            item,
            embed_data,
            bank_code,
            callback_url,
            expire_duration_seconds,
            sub_app_id,
            mac,
        };

        let resp: ZaloPayCreateOrderResponse = match self.config.mode {
            ZaloPayMode::Mock => {
                let mock = self
                    .mock
                    .as_ref()
                    .expect("mock engine present in mock mode");
                execute_with_retry(
                    &self.breaker,
                    self.config.max_retries,
                    self.mock_backoff,
                    "zalopay_create_order",
                    || async { mock.handle_create_order(&wire, key1) },
                )
                .await?
            }
            ZaloPayMode::Sandbox => {
                let url = format!("{}/v2/create", self.config.base_url.trim_end_matches('/'));
                let val = execute_http_json_with_retry(
                    &self.breaker,
                    self.config.max_retries,
                    "zalopay_create_order",
                    || self.http_client.post(&url).json(&wire),
                )
                .await?;
                serde_json::from_value(val).map_err(|e| {
                    PaymentError::Provider(format!("invalid zalopay create_order response: {e}"))
                })?
            }
        };

        if resp.return_code == ZALOPAY_RETURN_SUCCESS {
            let _ = validate_zalopay_pay_url(&resp.order_url, self.config.mode)?;
        }

        info!(
            op = "zalopay_create_order",
            mode = self.config.mode.as_str(),
            app_trans_id = app_trans_id,
            return_code = resp.return_code,
            sub_return_code = resp.sub_return_code,
            "zalopay create_order completed"
        );
        Ok(resp)
    }

    /// Calls ZaloPay Query Order Status (`POST {base_url}/v2/query`).
    ///
    /// Official specification: `https://docs.zalopay.vn/docs/specs/order-query` (verified 2026-11-24).
    pub async fn query_order(&self, app_trans_id: &str) -> Result<ZaloPayQueryOrderResponse> {
        let app_trans_id = app_trans_id.trim();
        if app_trans_id.is_empty() {
            return Err(PaymentError::Validation(
                "zalopay app_trans_id is required".to_string(),
            ));
        }

        let app_id = self.effective_app_id()?;
        let key1 = self.effective_key1();
        let mac = compute_query_order_mac(key1, app_id, app_trans_id);
        let wire = WireQueryOrderRequest {
            app_id,
            app_trans_id: app_trans_id.to_string(),
            mac,
        };

        let resp: ZaloPayQueryOrderResponse = match self.config.mode {
            ZaloPayMode::Mock => {
                let mock = self
                    .mock
                    .as_ref()
                    .expect("mock engine present in mock mode");
                let out = execute_with_retry(
                    &self.breaker,
                    self.config.max_retries,
                    self.mock_backoff,
                    "zalopay_query_order",
                    || async { mock.handle_query_order(&wire, key1) },
                )
                .await?;
                mock.await_gate_if_armed(MockOperation::QueryOrder).await;
                out
            }
            ZaloPayMode::Sandbox => {
                let url = format!("{}/v2/query", self.config.base_url.trim_end_matches('/'));
                let val = execute_http_json_with_retry(
                    &self.breaker,
                    self.config.max_retries,
                    "zalopay_query_order",
                    || self.http_client.post(&url).json(&wire),
                )
                .await?;
                serde_json::from_value(val).map_err(|e| {
                    PaymentError::Provider(format!("invalid zalopay query_order response: {e}"))
                })?
            }
        };

        info!(
            op = "zalopay_query_order",
            mode = self.config.mode.as_str(),
            app_trans_id = app_trans_id,
            return_code = resp.return_code,
            sub_return_code = resp.sub_return_code,
            is_processing = resp.is_processing,
            "zalopay query_order completed"
        );
        Ok(resp)
    }

    /// Calls ZaloPay Refund Order (`POST {base_url}/v2/refund`).
    ///
    /// Official specification: `https://docs.zalopay.vn/docs/specs/order-refund` (verified 2026-11-24).
    /// Note: Per ZaloPay docs and F4, `refund` only confirms receipt; callers should persist
    /// `m_refund_id` before calling and poll [`Self::query_refund`] when `return_code == 3`.
    pub async fn refund(&self, input: &ZaloPayRefundInput) -> Result<ZaloPayRefundResponse> {
        let m_refund_id = input.m_refund_id.trim();
        let zp_trans_id = input.zp_trans_id.trim();
        if m_refund_id.is_empty() {
            return Err(PaymentError::Validation(
                "zalopay m_refund_id is required".to_string(),
            ));
        }
        if zp_trans_id.is_empty() {
            return Err(PaymentError::Validation(
                "zalopay zp_trans_id is required".to_string(),
            ));
        }
        if input.amount_vnd <= 0 {
            return Err(PaymentError::Validation(
                "zalopay refund amount_vnd must be positive integer VND".to_string(),
            ));
        }
        if let Some(fee) = input.refund_fee_amount_vnd {
            if fee < 0 || fee > input.amount_vnd {
                return Err(PaymentError::Validation(
                    "zalopay refund_fee_amount_vnd must be within 0..=amount_vnd".to_string(),
                ));
            }
        }

        let app_id = self.effective_app_id()?;
        let desc_trimmed = input.description.trim();
        let description = if desc_trimmed.is_empty() {
            format!("Refund {m_refund_id}")
        } else {
            truncate_chars(desc_trimmed, MAX_DESCRIPTION_CHARS)
        };
        let timestamp_ms = input.timestamp.unwrap_or_else(Utc::now).timestamp_millis();

        let key1 = self.effective_key1();
        let mac = compute_refund_mac(
            key1,
            app_id,
            zp_trans_id,
            input.amount_vnd,
            input.refund_fee_amount_vnd,
            &description,
            timestamp_ms,
        );

        let wire = WireRefundRequest {
            app_id,
            m_refund_id: m_refund_id.to_string(),
            zp_trans_id: zp_trans_id.to_string(),
            amount: input.amount_vnd,
            refund_fee_amount: input.refund_fee_amount_vnd,
            timestamp: timestamp_ms,
            description,
            mac,
        };

        let resp: ZaloPayRefundResponse = match self.config.mode {
            ZaloPayMode::Mock => {
                let mock = self
                    .mock
                    .as_ref()
                    .expect("mock engine present in mock mode");
                let out = execute_with_retry(
                    &self.breaker,
                    self.config.max_retries,
                    self.mock_backoff,
                    "zalopay_refund",
                    || async { mock.handle_refund(&wire, key1) },
                )
                .await?;
                mock.await_gate_if_armed(MockOperation::Refund).await;
                out
            }
            ZaloPayMode::Sandbox => {
                let url = format!("{}/v2/refund", self.config.base_url.trim_end_matches('/'));
                let val = execute_http_json_with_retry(
                    &self.breaker,
                    self.config.max_retries,
                    "zalopay_refund",
                    || self.http_client.post(&url).json(&wire),
                )
                .await?;
                serde_json::from_value(val).map_err(|e| {
                    PaymentError::Provider(format!("invalid zalopay refund response: {e}"))
                })?
            }
        };

        info!(
            op = "zalopay_refund",
            mode = self.config.mode.as_str(),
            m_refund_id = m_refund_id,
            return_code = resp.return_code,
            sub_return_code = resp.sub_return_code,
            "zalopay refund completed"
        );
        Ok(resp)
    }

    /// Calls ZaloPay Query Refund Status (`POST {base_url}/v2/query_refund`).
    ///
    /// Official specification: `https://docs.zalopay.vn/docs/specs/order-query-refund` (verified 2026-11-24).
    pub async fn query_refund(
        &self,
        m_refund_id: &str,
        timestamp: Option<DateTime<Utc>>,
    ) -> Result<ZaloPayQueryRefundResponse> {
        let m_refund_id = m_refund_id.trim();
        if m_refund_id.is_empty() {
            return Err(PaymentError::Validation(
                "zalopay m_refund_id is required".to_string(),
            ));
        }

        let app_id = self.effective_app_id()?;
        let key1 = self.effective_key1();
        let timestamp_ms = timestamp.unwrap_or_else(Utc::now).timestamp_millis();
        let mac = compute_query_refund_mac(key1, app_id, m_refund_id, timestamp_ms);
        let wire = WireQueryRefundRequest {
            app_id,
            m_refund_id: m_refund_id.to_string(),
            timestamp: timestamp_ms,
            mac,
        };

        let resp: ZaloPayQueryRefundResponse = match self.config.mode {
            ZaloPayMode::Mock => {
                let mock = self
                    .mock
                    .as_ref()
                    .expect("mock engine present in mock mode");
                let out = execute_with_retry(
                    &self.breaker,
                    self.config.max_retries,
                    self.mock_backoff,
                    "zalopay_query_refund",
                    || async { mock.handle_query_refund(&wire, key1) },
                )
                .await?;
                mock.await_gate_if_armed(MockOperation::QueryRefund).await;
                out
            }
            ZaloPayMode::Sandbox => {
                let url = format!(
                    "{}/v2/query_refund",
                    self.config.base_url.trim_end_matches('/')
                );
                let val = execute_http_json_with_retry(
                    &self.breaker,
                    self.config.max_retries,
                    "zalopay_query_refund",
                    || self.http_client.post(&url).json(&wire),
                )
                .await?;
                serde_json::from_value(val).map_err(|e| {
                    PaymentError::Provider(format!("invalid zalopay query_refund response: {e}"))
                })?
            }
        };

        info!(
            op = "zalopay_query_refund",
            mode = self.config.mode.as_str(),
            m_refund_id = m_refund_id,
            return_code = resp.return_code,
            sub_return_code = resp.sub_return_code,
            "zalopay query_refund completed"
        );
        Ok(resp)
    }

    /// Convenience wrapper verifying an incoming callback using this adapter's effective `key2`.
    pub fn verify_callback_request(
        &self,
        req: &ZaloPayCallbackRequest,
        now: DateTime<Utc>,
        replay_window_secs: i64,
    ) -> Result<VerifiedZaloPayCallback> {
        verify_and_parse_callback(req, self.effective_key2(), now, replay_window_secs)
    }
}

/// Validates a ZaloPay `order_url` / `pay_url` against the F8 redirect host allowlist so a
/// compromised or misconfigured response can never send buyers to an arbitrary external URL or
/// `javascript:` / `http:` URI.
/// - `ZaloPayMode::Mock`: allows same-origin relative path `/thanh-toan/...` (no `//`, `..`, `\`,
///   or control chars) OR an allowlisted `https://*.zalopay.vn` URL.
/// - `ZaloPayMode::Sandbox`: requires `https://` with empty userinfo and host `zalopay.vn` or
///   `*.zalopay.vn`.
pub fn validate_zalopay_pay_url(raw: &str, mode: ZaloPayMode) -> Result<String> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Err(PaymentError::Provider(
            "zalopay returned empty pay_url".to_string(),
        ));
    }
    if trimmed.chars().any(|c| c.is_control()) {
        return Err(PaymentError::Provider(
            "zalopay pay_url contains disallowed control characters".to_string(),
        ));
    }
    if mode == ZaloPayMode::Mock && trimmed.starts_with("/thanh-toan/") {
        if trimmed.starts_with("//") || trimmed.contains('\\') || trimmed.contains("..") {
            return Err(PaymentError::Provider(
                "zalopay mock pay_url contains disallowed path sequence".to_string(),
            ));
        }
        return Ok(trimmed.to_string());
    }
    let parsed = reqwest::Url::parse(trimmed)
        .map_err(|_| PaymentError::Provider("zalopay pay_url is not a valid URL".to_string()))?;
    if parsed.scheme() != "https" {
        return Err(PaymentError::Provider(
            "zalopay pay_url must use https scheme".to_string(),
        ));
    }
    if !parsed.username().is_empty() || parsed.password().is_some() {
        return Err(PaymentError::Provider(
            "zalopay pay_url must not contain userinfo".to_string(),
        ));
    }
    let host = parsed
        .host_str()
        .map(str::to_ascii_lowercase)
        .ok_or_else(|| PaymentError::Provider("zalopay pay_url missing host".to_string()))?;
    if host != "zalopay.vn" && !host.ends_with(".zalopay.vn") {
        return Err(PaymentError::Provider(format!(
            "zalopay pay_url host '{host}' is outside the zalopay.vn allowlist"
        )));
    }
    Ok(trimmed.to_string())
}

/// Classifies whether a ZaloPay refund `sub_return_code` (when `return_code == 2`) represents a
/// transient provider/system error that should be retried later rather than escalated immediately
/// to `manual_review` (F4).
pub fn is_zalopay_refund_transient_sub_code(sub_return_code: i32) -> bool {
    matches!(sub_return_code, 0 | -1 | -16 | -429 | -500 | -999)
}

// =============================================================================
// Internal Utilities
// =============================================================================

fn truncate_chars(input: &str, max_chars: usize) -> String {
    input.chars().take(max_chars).collect()
}

fn deterministic_positive_id(prefix: &str, seed: &str) -> i64 {
    let mut hasher = Sha256::new();
    hasher.update(prefix.as_bytes());
    hasher.update(seed.as_bytes());
    let digest = hasher.finalize();
    let raw = u64::from_be_bytes(digest[..8].try_into().expect("8-byte slice"));
    // Keep strictly positive in [100_000_000_000, 999_999_999_999] (12 digits).
    100_000_000_000_i64 + (raw % 900_000_000_000_u64) as i64
}

fn deterministic_token(prefix: &str, seed: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(prefix.as_bytes());
    hasher.update(seed.as_bytes());
    let digest = hex::encode(hasher.finalize());
    digest[..16].to_string()
}

fn constant_time_hex_eq(expected_hex: &str, actual_hex: &str) -> bool {
    let Ok(expected) = hex::decode(expected_hex) else {
        return false;
    };
    let Ok(actual) = hex::decode(actual_hex.trim()) else {
        return false;
    };
    if expected.len() != actual.len() {
        return false;
    }
    let mut diff = 0_u8;
    for (&a, &b) in expected.iter().zip(actual.iter()) {
        diff |= a ^ b;
    }
    diff == 0
}

fn deserialize_i64_or_string<'de, D>(deserializer: D) -> std::result::Result<i64, D::Error>
where
    D: Deserializer<'de>,
{
    #[derive(Deserialize)]
    #[serde(untagged)]
    enum IntOrString {
        Int(i64),
        Uint(u64),
        Str(String),
    }

    match IntOrString::deserialize(deserializer)? {
        IntOrString::Int(v) => Ok(v),
        IntOrString::Uint(v) => i64::try_from(v).map_err(serde::de::Error::custom),
        IntOrString::Str(s) => {
            let trimmed = s.trim();
            if trimmed.is_empty() {
                Ok(0)
            } else {
                trimmed.parse::<i64>().map_err(serde::de::Error::custom)
            }
        }
    }
}

#[cfg(test)]
#[path = "zalopay_tests.rs"]
mod tests;
