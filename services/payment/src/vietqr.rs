use std::collections::HashSet;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use chrono::{DateTime, NaiveDate, NaiveDateTime, TimeZone, Utc};
use reqwest::Client;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tokio::time::sleep;
use tracing::{info, warn};
use uuid::Uuid;

use crate::config::{Config, VietQrMode};
use crate::domain::{normalize_provider_tx_id, vn_offset, vn_today};
use crate::error::{PaymentError, Result};
use crate::webhook_security::{compute_hmac_hex, IncomingWebhookPayload};

const SEPAY_PAGE_LIMIT: usize = 100;
const SEPAY_RECON_MAX_PAGES: usize = 50;

// =============================================================================
// Circuit Breaker (Closed -> Open -> HalfOpen)
// =============================================================================

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CircuitState {
    Closed,
    Open,
    HalfOpen,
}

#[derive(Debug)]
struct CircuitInner {
    state: CircuitState,
    consecutive_failures: u32,
    opened_at: Option<Instant>,
}

#[derive(Debug, Clone)]
pub struct CircuitBreaker {
    failure_threshold: u32,
    open_duration: Duration,
    inner: Arc<Mutex<CircuitInner>>,
}

impl CircuitBreaker {
    pub fn new(failure_threshold: u32, open_duration: Duration) -> Self {
        Self {
            failure_threshold: failure_threshold.max(1),
            open_duration,
            inner: Arc::new(Mutex::new(CircuitInner {
                state: CircuitState::Closed,
                consecutive_failures: 0,
                opened_at: None,
            })),
        }
    }

    pub fn allow_request(&self) -> Result<()> {
        let mut guard = self.inner.lock().expect("circuit breaker lock");
        match guard.state {
            CircuitState::Closed | CircuitState::HalfOpen => Ok(()),
            CircuitState::Open => {
                if let Some(opened_at) = guard.opened_at {
                    if opened_at.elapsed() >= self.open_duration {
                        guard.state = CircuitState::HalfOpen;
                        return Ok(());
                    }
                }
                Err(PaymentError::CircuitOpen)
            }
        }
    }

    pub fn record_success(&self) {
        let mut guard = self.inner.lock().expect("circuit breaker lock");
        guard.state = CircuitState::Closed;
        guard.consecutive_failures = 0;
        guard.opened_at = None;
    }

    pub fn record_failure(&self) {
        let mut guard = self.inner.lock().expect("circuit breaker lock");
        guard.consecutive_failures += 1;
        if guard.consecutive_failures >= self.failure_threshold
            || guard.state == CircuitState::HalfOpen
        {
            guard.state = CircuitState::Open;
            guard.opened_at = Some(Instant::now());
        }
    }

    pub fn state(&self) -> CircuitState {
        let guard = self.inner.lock().expect("circuit breaker lock");
        guard.state
    }
}

// =============================================================================
// VietQR Adapter (Mock | payOS | SePay)
// =============================================================================

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VietQrIntent {
    pub provider: String,
    pub provider_order_code: String,
    pub qr_code: String,
    pub qr_url: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProviderTransaction {
    pub provider_tx_id: String,
    pub provider_order_code: String,
    pub amount_vnd: i64,
    pub status: String,
    pub occurred_at: DateTime<Utc>,
}

impl ProviderTransaction {
    /// Canonical constructor normalizing `provider_tx_id` (trim + reject empty) once at ingress (P0-1).
    pub fn new(
        provider_tx_id: impl AsRef<str>,
        provider_order_code: String,
        amount_vnd: i64,
        status: String,
        occurred_at: DateTime<Utc>,
    ) -> Result<Self> {
        let normalized_tx_id = normalize_provider_tx_id(provider_tx_id.as_ref())?;
        Ok(Self {
            provider_tx_id: normalized_tx_id,
            provider_order_code: provider_order_code.trim().to_string(),
            amount_vnd,
            status,
            occurred_at,
        })
    }
}

#[derive(Clone)]
pub struct VietQrAdapter {
    mode: VietQrMode,
    bank_bin: String,
    account_no: String,
    account_name: String,
    payos_base_url: String,
    payos_client_id: String,
    payos_api_key: String,
    payos_checksum_key: String,
    sepay_base_url: String,
    sepay_api_token: String,
    sepay_bank_code: String,
    webhook_secret: String,
    max_retries: u32,
    http_client: Client,
    breaker: CircuitBreaker,
}

impl VietQrAdapter {
    pub fn from_config(cfg: &Config) -> Self {
        let http_client = Client::builder()
            .timeout(cfg.external_http_timeout)
            .build()
            .expect("reqwest client build");

        Self {
            mode: cfg.vietqr_mode,
            bank_bin: cfg.vietqr_bank_bin.clone(),
            account_no: cfg.vietqr_account_no.clone(),
            account_name: cfg.vietqr_account_name.clone(),
            payos_base_url: cfg.payos_base_url.clone(),
            payos_client_id: cfg.payos_client_id.clone(),
            payos_api_key: cfg.payos_api_key.clone(),
            payos_checksum_key: cfg.payos_checksum_key.clone(),
            sepay_base_url: cfg.sepay_base_url.clone(),
            sepay_api_token: cfg.sepay_api_token.clone(),
            sepay_bank_code: cfg.sepay_bank_code.clone(),
            webhook_secret: cfg.webhook_secret.clone(),
            max_retries: cfg.external_max_retries,
            http_client,
            breaker: CircuitBreaker::new(5, Duration::from_secs(30)),
        }
    }

    pub fn mode(&self) -> VietQrMode {
        self.mode
    }

    /// Generates a deterministic, compact order code (`NM` + 10 uppercase hex chars) suitable for bank transfer memos.
    pub fn generate_order_code(payment_id: Uuid, order_id: &str) -> String {
        let mut hasher = Sha256::new();
        hasher.update(payment_id.as_bytes());
        hasher.update(order_id.as_bytes());
        let digest = hex::encode_upper(hasher.finalize());
        format!("NM{}", &digest[..10])
    }

    /// Creates a VietQR payment link / EMVCo QR payload.
    pub async fn create_qr_intent(
        &self,
        payment_id: Uuid,
        order_id: &str,
        amount_vnd: i64,
        expires_at: DateTime<Utc>,
    ) -> Result<VietQrIntent> {
        let order_code = Self::generate_order_code(payment_id, order_id);

        match self.mode {
            VietQrMode::Mock => Ok(self.build_deterministic_mock_qr(&order_code, amount_vnd)),
            VietQrMode::Sepay => Ok(VietQrIntent {
                provider: "sepay".to_string(),
                provider_order_code: order_code.clone(),
                qr_code: self.build_emvco_payload(&order_code, amount_vnd),
                qr_url: format!(
                    "https://qr.sepay.vn/img?acc={}&bank={}&amount={}&des={}",
                    self.account_no, self.sepay_bank_code, amount_vnd, order_code
                ),
            }),
            VietQrMode::PayOs => {
                self.create_payos_link(&order_code, amount_vnd, expires_at)
                    .await
            }
        }
    }

    /// Fetches a single batch feed for `scan_expired_payments` so SePay makes ONE feed fetch
    /// per scanner tick instead of N+1 requests (review2 P1-2).
    /// - `Mock`: `Ok(Some(vec![]))`
    /// - `Sepay`: fetches recent incoming feed (since yesterday 00:00 VN) once -> `Ok(Some(txs))`
    /// - `PayOs`: `Ok(None)` (no list endpoint; caller uses `check_payment_status` per candidate)
    pub async fn list_recent_transactions_for_expiry(
        &self,
    ) -> Result<Option<Vec<ProviderTransaction>>> {
        match self.mode {
            VietQrMode::Mock => Ok(Some(Vec::new())),
            VietQrMode::PayOs => Ok(None),
            VietQrMode::Sepay => {
                let min_date = vn_today(Utc::now())
                    .pred_opt()
                    .unwrap_or_else(|| vn_today(Utc::now()));
                let min_vn = format!("{} 00:00:00", min_date.format("%Y-%m-%d"));
                let list = self
                    .fetch_sepay_transactions_paginated(
                        Some(min_vn.as_str()),
                        None,
                        SEPAY_PAGE_LIMIT,
                        SEPAY_RECON_MAX_PAGES,
                    )
                    .await?;
                Ok(Some(list))
            }
        }
    }

    /// Checks final transaction status at provider before expiring a pending payment (UC-PAY06).
    /// Used directly for PayOS (per-order status API); SePay uses `list_recent_transactions_for_expiry`.
    pub async fn check_payment_status(
        &self,
        provider_order_code: &str,
    ) -> Result<Option<ProviderTransaction>> {
        match self.mode {
            VietQrMode::Mock => Ok(None),
            VietQrMode::PayOs => self.query_payos_status(provider_order_code).await,
            VietQrMode::Sepay => {
                let list = self
                    .list_recent_transactions_for_expiry()
                    .await?
                    .unwrap_or_default();
                Ok(list
                    .into_iter()
                    .find(|t| t.provider_order_code == provider_order_code))
            }
        }
    }

    /// Fetches provider transactions for reconciliation on a VN calendar date (UC-PAY07 / P1-2 / P2-3(b)).
    pub async fn list_provider_transactions(
        &self,
        run_date: NaiveDate,
    ) -> Result<Vec<ProviderTransaction>> {
        match self.mode {
            VietQrMode::Mock => Ok(Vec::new()),
            VietQrMode::PayOs => Err(PaymentError::ProviderUnsupported(
                "cổng chưa có dữ liệu: payOS chưa hỗ trợ API liệt kê giao dịch để đối soát"
                    .to_string(),
            )),
            VietQrMode::Sepay => {
                let min_vn = format!("{} 00:00:00", run_date.format("%Y-%m-%d"));
                let max_vn = format!("{} 23:59:59", run_date.format("%Y-%m-%d"));
                self.fetch_sepay_transactions_paginated(
                    Some(min_vn.as_str()),
                    Some(max_vn.as_str()),
                    SEPAY_PAGE_LIMIT,
                    SEPAY_RECON_MAX_PAGES,
                )
                .await
            }
        }
    }

    /// Builds a valid mock webhook JSON body + HMAC-SHA256 signature for mock/test simulation.
    pub fn build_mock_webhook_signed(
        &self,
        payment_id: Option<Uuid>,
        order_id: Option<String>,
        provider_order_code: Option<String>,
        provider_tx_id: String,
        amount_vnd: i64,
    ) -> (Vec<u8>, String) {
        let payload = IncomingWebhookPayload {
            provider_tx_id,
            payment_id: payment_id.map(|u| u.to_string()),
            order_group_id: order_id.clone(),
            order_id,
            provider_order_code,
            reference: None,
            amount_vnd,
            status: "SUCCEEDED".to_string(),
            provider: Some(self.mode.as_str().to_string()),
            occurred_at: Some(Utc::now().to_rfc3339()),
        };
        let body = serde_json::to_vec(&payload).expect("serialize mock webhook");
        let signature = compute_hmac_hex(&self.webhook_secret, &body);
        (body, signature)
    }

    fn build_deterministic_mock_qr(&self, order_code: &str, amount_vnd: i64) -> VietQrIntent {
        let qr_code = self.build_emvco_payload(order_code, amount_vnd);
        let qr_url = format!(
            "https://img.vietqr.io/image/{}-{}-compact2.png?amount={}&addInfo={}&accountName={}",
            self.bank_bin,
            self.account_no,
            amount_vnd,
            order_code,
            self.account_name.replace(' ', "%20")
        );
        VietQrIntent {
            provider: "vietqr_mock".to_string(),
            provider_order_code: order_code.to_string(),
            qr_code,
            qr_url,
        }
    }

    fn build_emvco_payload(&self, order_code: &str, amount_vnd: i64) -> String {
        // Deterministic VietQR EMVCo-style payload representation
        let amount_str = amount_vnd.to_string();
        let purpose = format!("08{:02}{order_code}", order_code.len());
        format!(
            "00020101021238570010A00000072701270006{}01{}0208QRIBFTTA530370454{:02}{}5802VN62{:02}{}",
            self.bank_bin,
            self.account_no,
            amount_str.len(),
            amount_str,
            purpose.len(),
            purpose
        )
    }

    async fn create_payos_link(
        &self,
        order_code: &str,
        amount_vnd: i64,
        expires_at: DateTime<Utc>,
    ) -> Result<VietQrIntent> {
        let numeric_order_code: i64 = i64::from_str_radix(&order_code[2..10], 16)
            .unwrap_or(100_000)
            .abs();
        let cancel_url = "https://nexusmart.local/checkout/cancel";
        let return_url = "https://nexusmart.local/checkout/success";
        let sign_data = format!(
            "amount={amount_vnd}&cancelUrl={cancel_url}&description={order_code}&orderCode={numeric_order_code}&returnUrl={return_url}"
        );
        let signature = compute_hmac_hex(&self.payos_checksum_key, sign_data.as_bytes());

        let url = format!(
            "{}/v2/payment-requests",
            self.payos_base_url.trim_end_matches('/')
        );
        let body = serde_json::json!({
            "orderCode": numeric_order_code,
            "amount": amount_vnd,
            "description": order_code,
            "cancelUrl": cancel_url,
            "returnUrl": return_url,
            "expiredAt": expires_at.timestamp(),
            "signature": signature,
        });

        let response: serde_json::Value = self
            .execute_with_retry("payos_create_link", || {
                self.http_client
                    .post(&url)
                    .header("x-client-id", &self.payos_client_id)
                    .header("x-api-key", &self.payos_api_key)
                    .json(&body)
            })
            .await?;

        let qr_code = response
            .pointer("/data/qrCode")
            .and_then(|v| v.as_str())
            .map(ToString::to_string)
            .unwrap_or_else(|| self.build_emvco_payload(order_code, amount_vnd));
        let qr_url = response
            .pointer("/data/checkoutUrl")
            .and_then(|v| v.as_str())
            .map(ToString::to_string)
            .unwrap_or_else(|| format!("{}/web/{order_code}", self.payos_base_url));

        Ok(VietQrIntent {
            provider: "payos".to_string(),
            provider_order_code: order_code.to_string(),
            qr_code,
            qr_url,
        })
    }

    async fn query_payos_status(
        &self,
        provider_order_code: &str,
    ) -> Result<Option<ProviderTransaction>> {
        let numeric_order_code: i64 = i64::from_str_radix(
            &provider_order_code[2..10.min(provider_order_code.len())],
            16,
        )
        .unwrap_or(0)
        .abs();
        let url = format!(
            "{}/v2/payment-requests/{numeric_order_code}",
            self.payos_base_url.trim_end_matches('/')
        );

        let response: serde_json::Value = self
            .execute_with_retry("payos_get_status", || {
                self.http_client
                    .get(&url)
                    .header("x-client-id", &self.payos_client_id)
                    .header("x-api-key", &self.payos_api_key)
            })
            .await?;

        let status = response
            .pointer("/data/status")
            .and_then(|v| v.as_str())
            .unwrap_or("");
        if status.eq_ignore_ascii_case("PAID") {
            let amount_vnd = response
                .pointer("/data/amountPaid")
                .and_then(|v| v.as_i64())
                .unwrap_or(0);
            let tx_id = response
                .pointer("/data/id")
                .and_then(|v| v.as_str())
                .map(str::trim)
                .filter(|s| !s.is_empty())
                .map(ToString::to_string)
                .unwrap_or_else(|| format!("payos-{numeric_order_code}"));
            let occurred_at = [
                "/data/transactions/0/transactionDateTime",
                "/data/createdAt",
            ]
            .iter()
            .find_map(|ptr| {
                response
                    .pointer(ptr)
                    .and_then(|v| v.as_str())
                    .and_then(parse_provider_timestamp)
            })
            .unwrap_or_else(Utc::now);
            return Ok(Some(ProviderTransaction::new(
                tx_id,
                provider_order_code.to_string(),
                amount_vnd,
                "succeeded".to_string(),
                occurred_at,
            )?));
        }

        Ok(None)
    }

    async fn fetch_sepay_transactions_paginated(
        &self,
        transaction_date_min: Option<&str>,
        transaction_date_max: Option<&str>,
        page_limit: usize,
        max_pages: usize,
    ) -> Result<Vec<ProviderTransaction>> {
        let base_url = format!(
            "{}/userapi/transactions/list",
            self.sepay_base_url.trim_end_matches('/')
        );
        let limit = page_limit.max(1);
        let cap = max_pages.max(1);
        // P1-2: Cap counts ONLY incoming (amount_in > 0) transactions; allow probing past an
        // exact cap multiple and past outgoing-only pages, with a hard HTTP-page safety ceiling.
        let max_incoming_cap = cap.saturating_mul(limit);
        let max_http_pages = cap.saturating_mul(4).max(cap.saturating_add(1));

        let mut all = Vec::new();
        let mut seen_raw_ids: HashSet<String> = HashSet::new();
        let mut seen_incoming_ids: HashSet<String> = HashSet::new();

        for page in 1..=max_http_pages {
            let mut params: Vec<(&str, String)> =
                vec![("limit", limit.to_string()), ("page", page.to_string())];
            if let Some(min_dt) = transaction_date_min {
                params.push(("transaction_date_min", min_dt.to_string()));
            }
            if let Some(max_dt) = transaction_date_max {
                params.push(("transaction_date_max", max_dt.to_string()));
            }

            let response: serde_json::Value = self
                .execute_with_retry("sepay_list_transactions", || {
                    self.http_client
                        .get(&base_url)
                        .bearer_auth(&self.sepay_api_token)
                        .query(&params)
                })
                .await?;

            let raw_items = response
                .get("transactions")
                .and_then(|v| v.as_array())
                .map(Vec::as_slice)
                .unwrap_or(&[]);
            let raw_count = raw_items.len();
            if raw_count == 0 {
                return Ok(all);
            }

            // Stop immediately if server ignores `page` and returns zero unseen transactions.
            let mut new_raw_ids = 0usize;
            for item in raw_items {
                if let Some(raw_id) = extract_sepay_raw_id(item) {
                    if seen_raw_ids.insert(raw_id) {
                        new_raw_ids += 1;
                    }
                }
            }
            if new_raw_ids == 0 {
                return Ok(all);
            }

            for tx in parse_sepay_transactions_response(&response) {
                if seen_incoming_ids.insert(tx.provider_tx_id.clone()) {
                    all.push(tx);
                    if all.len() > max_incoming_cap {
                        return Err(PaymentError::Provider(format!(
                            "sepay transaction feed exceeded pagination cap ({max_incoming_cap} incoming items); aborting to prevent false missing_provider"
                        )));
                    }
                }
            }

            if raw_count < limit {
                return Ok(all);
            }
            if page == max_http_pages {
                return Err(PaymentError::Provider(format!(
                    "sepay transaction feed exceeded HTTP pagination safety ceiling ({max_http_pages} pages); aborting to prevent false missing_provider"
                )));
            }
        }

        Ok(all)
    }

    /// Executes an outbound HTTP request with circuit breaker + timeout + exponential backoff retry.
    async fn execute_with_retry<F>(&self, op_name: &str, build_req: F) -> Result<serde_json::Value>
    where
        F: Fn() -> reqwest::RequestBuilder,
    {
        execute_http_json_with_retry(&self.breaker, self.max_retries, op_name, build_req).await
    }
}

/// Retry classification for [`execute_with_retry`].
pub enum RetryError {
    Retryable(PaymentError),
    NonRetryable(PaymentError),
}

/// Shared circuit-breaker + exponential backoff retry runner reused across external payment
/// adapters (`VietQrAdapter`, `ZaloPayAdapter`).
pub async fn execute_with_retry<T, F, Fut>(
    breaker: &CircuitBreaker,
    max_retries: u32,
    base_backoff: Duration,
    op_name: &str,
    mut attempt_fn: F,
) -> Result<T>
where
    F: FnMut() -> Fut,
    Fut: std::future::Future<Output = std::result::Result<T, RetryError>>,
{
    breaker.allow_request()?;

    let mut last_err: Option<PaymentError> = None;
    let total_attempts = max_retries.saturating_add(1);

    for attempt in 0..total_attempts {
        match attempt_fn().await {
            Ok(val) => {
                breaker.record_success();
                info!(
                    op = op_name,
                    attempt = attempt + 1,
                    "external provider call succeeded"
                );
                return Ok(val);
            }
            Err(RetryError::NonRetryable(err)) => {
                breaker.record_failure();
                warn!(
                    op = op_name,
                    attempt = attempt + 1,
                    max_attempts = total_attempts,
                    error = %err,
                    "external provider call rejected without retry"
                );
                return Err(err);
            }
            Err(RetryError::Retryable(err)) => {
                breaker.record_failure();
                warn!(
                    op = op_name,
                    attempt = attempt + 1,
                    max_attempts = total_attempts,
                    error = %err,
                    "external provider call failed"
                );
                last_err = Some(err);
            }
        }

        if attempt + 1 < total_attempts && !base_backoff.is_zero() {
            let multiplier = 1_u32 << attempt.min(5);
            sleep(base_backoff.saturating_mul(multiplier)).await;
        }
    }

    Err(last_err.unwrap_or_else(|| PaymentError::Provider(format!("{op_name} failed"))))
}

/// Executes an outbound HTTP JSON request with the shared circuit breaker, `reqwest` client
/// timeout, and exponential backoff retry (no retry on non-429 4xx client errors).
pub async fn execute_http_json_with_retry<F>(
    breaker: &CircuitBreaker,
    max_retries: u32,
    op_name: &str,
    build_req: F,
) -> Result<serde_json::Value>
where
    F: Fn() -> reqwest::RequestBuilder,
{
    execute_with_retry(
        breaker,
        max_retries,
        Duration::from_millis(200),
        op_name,
        || async {
            match build_req().send().await {
                Ok(resp) => {
                    let status = resp.status();
                    if status.is_success() {
                        match resp.json::<serde_json::Value>().await {
                            Ok(val) => Ok(val),
                            Err(e) => Err(RetryError::Retryable(PaymentError::Provider(format!(
                                "invalid provider JSON response: {e}"
                            )))),
                        }
                    } else if status.is_client_error() && status.as_u16() != 429 {
                        Err(RetryError::NonRetryable(PaymentError::Provider(format!(
                            "{op_name} rejected with HTTP {}",
                            status.as_u16()
                        ))))
                    } else {
                        Err(RetryError::Retryable(PaymentError::Provider(format!(
                            "{op_name} returned HTTP {}",
                            status.as_u16()
                        ))))
                    }
                }
                Err(e) => Err(RetryError::Retryable(PaymentError::Provider(format!(
                    "{op_name} transport error: {e}"
                )))),
            }
        },
    )
    .await
}

/// Parses provider/webhook timestamps in RFC3339 or Vietnam local time (`Asia/Ho_Chi_Minh`, +07:00).
pub fn parse_provider_timestamp(raw: &str) -> Option<DateTime<Utc>> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return None;
    }
    if let Ok(dt) = DateTime::parse_from_rfc3339(trimmed) {
        return Some(dt.with_timezone(&Utc));
    }
    let vn = vn_offset();
    for fmt in ["%Y-%m-%d %H:%M:%S", "%Y-%m-%dT%H:%M:%S"] {
        if let Ok(naive) = NaiveDateTime::parse_from_str(trimmed, fmt) {
            if let Some(dt) = vn.from_local_datetime(&naive).single() {
                return Some(dt.with_timezone(&Utc));
            }
        }
    }
    if let Ok(d) = NaiveDate::parse_from_str(trimmed, "%Y-%m-%d") {
        if let Some(naive) = d.and_hms_opt(0, 0, 0) {
            if let Some(dt) = vn.from_local_datetime(&naive).single() {
                return Some(dt.with_timezone(&Utc));
            }
        }
    }
    None
}

/// Parses an integer VND amount from JSON without floating-point arithmetic (P2-8):
/// accepts JSON integers (`255000`), integer strings (`"255000"`), and zero-fraction decimal
/// strings (`"255000.00"`, `"255000.0000"`). Strictly rejects non-zero fractions (`"255000.50"`).
pub fn parse_integer_vnd_amount(val: &serde_json::Value) -> Option<i64> {
    if let Some(n) = val.as_i64() {
        return Some(n);
    }
    if let Some(u) = val.as_u64() {
        return i64::try_from(u).ok();
    }
    let raw = val.as_str()?.trim();
    if raw.is_empty() {
        return None;
    }
    if let Some((int_part, frac_part)) = raw.split_once('.') {
        if int_part.is_empty() || frac_part.is_empty() || !frac_part.bytes().all(|b| b == b'0') {
            return None;
        }
        return int_part.parse::<i64>().ok();
    }
    raw.parse::<i64>().ok()
}

/// Extracts a transaction timestamp from a webhook or provider JSON object across canonical and
/// SePay/PayOS field aliases (`occurred_at`, `transactionDate`, `transaction_date`,
/// `transactionDateTime`, `created_at`) (review2 P3-2).
pub fn extract_occurred_at_from_json(raw: &serde_json::Value) -> Option<DateTime<Utc>> {
    [
        "occurred_at",
        "transactionDate",
        "transaction_date",
        "transactionDateTime",
        "created_at",
    ]
    .iter()
    .find_map(|field| {
        raw.get(*field)
            .and_then(|v| v.as_str())
            .and_then(parse_provider_timestamp)
    })
}

fn extract_sepay_raw_id(item: &serde_json::Value) -> Option<String> {
    item.get("id")
        .and_then(|v| {
            v.as_str()
                .map(|s| s.trim().to_string())
                .or_else(|| v.as_i64().map(|n| n.to_string()))
        })
        .filter(|s| !s.is_empty())
}

/// Parses SePay `/userapi/transactions/list` JSON response using the real transaction timestamp
/// (`occurred_at` / `transactionDate` / `transaction_date` / `transactionDateTime` / `created_at`)
/// and canonical `ProviderTransaction::new` ingress normalization (PAY-B8 / P0-1 / P3-2).
pub fn parse_sepay_transactions_response(response: &serde_json::Value) -> Vec<ProviderTransaction> {
    let mut out = Vec::new();
    let Some(items) = response.get("transactions").and_then(|v| v.as_array()) else {
        return out;
    };

    for item in items {
        let amount_in = item
            .get("amount_in")
            .and_then(parse_integer_vnd_amount)
            .unwrap_or(0);
        if amount_in <= 0 {
            continue;
        }
        let Some(tx_id) = extract_sepay_raw_id(item) else {
            continue;
        };
        let Some(occurred_at) = extract_occurred_at_from_json(item) else {
            warn!(
                provider_tx_id = %tx_id,
                "skipping SePay transaction missing valid transaction_date"
            );
            continue;
        };
        let content = item
            .get("transaction_content")
            .and_then(|v| v.as_str())
            .unwrap_or("");
        let order_code = content
            .split(|c: char| !c.is_ascii_alphanumeric())
            .find(|tok| tok.to_ascii_uppercase().starts_with("NM") && tok.len() >= 8)
            .map(|s| s.to_ascii_uppercase())
            .unwrap_or_default();

        if let Ok(ptx) = ProviderTransaction::new(
            tx_id,
            order_code,
            amount_in,
            "succeeded".to_string(),
            occurred_at,
        ) {
            out.push(ptx);
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::webhook_security::verify_webhook_request;

    #[test]
    fn test_circuit_breaker_transitions() {
        let cb = CircuitBreaker::new(3, Duration::from_millis(25));
        assert_eq!(cb.state(), CircuitState::Closed);
        assert!(cb.allow_request().is_ok());

        cb.record_failure();
        cb.record_failure();
        assert_eq!(cb.state(), CircuitState::Closed);

        // 3rd failure trips circuit to Open
        cb.record_failure();
        assert_eq!(cb.state(), CircuitState::Open);
        assert!(matches!(cb.allow_request(), Err(PaymentError::CircuitOpen)));

        // After open duration elapses, moves to HalfOpen and allows probe
        std::thread::sleep(Duration::from_millis(30));
        assert!(cb.allow_request().is_ok());
        assert_eq!(cb.state(), CircuitState::HalfOpen);

        // Success closes circuit again
        cb.record_success();
        assert_eq!(cb.state(), CircuitState::Closed);
    }

    #[tokio::test]
    async fn test_vietqr_mock_deterministic_payload_and_valid_hmac() {
        let cfg = Config::from_env();
        let adapter = VietQrAdapter::from_config(&cfg);
        assert_eq!(adapter.mode(), VietQrMode::Mock);

        let payment_id = Uuid::parse_str("11111111-2222-3333-4444-555555555555").unwrap();
        let exp = Utc::now() + chrono::Duration::minutes(15);

        let qr1 = adapter
            .create_qr_intent(payment_id, "order-abc-123", 250_000, exp)
            .await
            .unwrap();
        let qr2 = adapter
            .create_qr_intent(payment_id, "order-abc-123", 250_000, exp)
            .await
            .unwrap();

        assert_eq!(qr1.provider, "vietqr_mock");
        assert_eq!(qr1.provider_order_code, qr2.provider_order_code);
        assert!(qr1.provider_order_code.starts_with("NM"));
        assert_eq!(qr1.qr_code, qr2.qr_code);
        assert_eq!(qr1.qr_url, qr2.qr_url);
        assert!(qr1.qr_url.contains("amount=250000"));
        assert!(qr1.qr_url.contains(&qr1.provider_order_code));

        // Mock webhook builder produces valid HMAC verifiable by webhook_security
        let (body, sig) = adapter.build_mock_webhook_signed(
            Some(payment_id),
            Some("order-abc-123".to_string()),
            Some(qr1.provider_order_code.clone()),
            "MOCK-TX-001".to_string(),
            250_000,
        );
        let now = Utc::now();
        let ts = now.timestamp().to_string();
        assert!(verify_webhook_request(
            &cfg.webhook_secret,
            &body,
            Some(&sig),
            Some(&ts),
            300,
            now
        )
        .is_ok());

        // In Mock mode, VietQrAdapter delegates durable mock provider transactions to Postgres
        // `webhook_events` (PAY-B8 / P3-1), so adapter-level check/list return empty without DB.
        assert!(adapter
            .check_payment_status(&qr1.provider_order_code)
            .await
            .unwrap()
            .is_none());
        let run_date = crate::domain::utc_to_vn_date(now);
        assert!(adapter
            .list_provider_transactions(run_date)
            .await
            .unwrap()
            .is_empty());
    }

    #[tokio::test]
    async fn test_payos_and_sepay_http_adapters_with_mock_server() {
        use axum::routing::{get, post};
        use axum::{Json, Router};
        use tokio::net::TcpListener;

        let app = Router::new()
            .route(
                "/v2/payment-requests",
                post(|| async {
                    Json(serde_json::json!({
                        "code": "00",
                        "data": {
                            "qrCode": "000201010212PAYOSQR",
                            "checkoutUrl": "https://pay.payos.vn/web/abc123"
                        }
                    }))
                }),
            )
            .route(
                "/v2/payment-requests/{id}",
                get(|| async {
                    Json(serde_json::json!({
                        "code": "00",
                        "data": {
                            "id": "payos-tx-888",
                            "status": "PAID",
                            "amountPaid": 180_000
                        }
                    }))
                }),
            )
            .route(
                "/userapi/transactions/list",
                get(|| async {
                    Json(serde_json::json!({
                        "transactions": [
                            {
                                "id": "sepay-tx-1",
                                "amount_in": 180_000,
                                "transaction_content": "CK THANH TOAN NM1A2B3C4D5E NEXUSMART",
                                "transaction_date": "2026-11-23 10:00:00"
                            },
                            {
                                "id": "sepay-tx-zero",
                                "amount_in": 0,
                                "transaction_content": "OUTGOING",
                                "transaction_date": "2026-11-23 10:01:00"
                            }
                        ]
                    }))
                }),
            );

        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let handle = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });

        let mut payos_cfg = Config::from_env();
        payos_cfg.vietqr_mode = VietQrMode::PayOs;
        payos_cfg.payos_base_url = format!("http://{addr}");
        payos_cfg.payos_client_id = "cid".into();
        payos_cfg.payos_api_key = "akey".into();
        payos_cfg.payos_checksum_key = "ckey".into();
        payos_cfg.external_max_retries = 1;
        let payos = VietQrAdapter::from_config(&payos_cfg);

        let pid = Uuid::new_v4();
        let exp = Utc::now() + chrono::Duration::minutes(15);
        let run_date = NaiveDate::from_ymd_opt(2026, 11, 23).unwrap();
        let qr = payos
            .create_qr_intent(pid, "ord-payos", 180_000, exp)
            .await
            .unwrap();
        assert_eq!(qr.provider, "payos");
        assert_eq!(qr.qr_code, "000201010212PAYOSQR");
        assert_eq!(qr.qr_url, "https://pay.payos.vn/web/abc123");

        let paid = payos
            .check_payment_status(&qr.provider_order_code)
            .await
            .unwrap()
            .expect("payos PAID");
        assert_eq!(paid.provider_tx_id, "payos-tx-888");
        assert_eq!(paid.amount_vnd, 180_000);
        assert!(matches!(
            payos.list_provider_transactions(run_date).await,
            Err(PaymentError::ProviderUnsupported(_))
        ));

        let mut sepay_cfg = Config::from_env();
        sepay_cfg.vietqr_mode = VietQrMode::Sepay;
        sepay_cfg.sepay_base_url = format!("http://{addr}");
        sepay_cfg.sepay_api_token = "tok".into();
        let sepay = VietQrAdapter::from_config(&sepay_cfg);

        let sqr = sepay
            .create_qr_intent(pid, "ord-sepay", 180_000, exp)
            .await
            .unwrap();
        assert_eq!(sqr.provider, "sepay");
        assert!(sqr.qr_url.contains("qr.sepay.vn/img"));

        let txs = sepay.list_provider_transactions(run_date).await.unwrap();
        assert_eq!(txs.len(), 1);
        assert_eq!(txs[0].provider_tx_id, "sepay-tx-1");
        assert_eq!(txs[0].provider_order_code, "NM1A2B3C4D5E");

        let found = sepay
            .check_payment_status("NM1A2B3C4D5E")
            .await
            .unwrap()
            .expect("sepay found");
        assert_eq!(found.amount_vnd, 180_000);

        handle.abort();
    }

    #[tokio::test]
    async fn test_payos_list_provider_transactions_returns_clear_error() {
        let mut cfg = Config::from_env();
        cfg.vietqr_mode = VietQrMode::PayOs;
        let adapter = VietQrAdapter::from_config(&cfg);
        let run_date = NaiveDate::from_ymd_opt(2026, 11, 23).unwrap();
        let err = adapter
            .list_provider_transactions(run_date)
            .await
            .unwrap_err();
        let status = err.to_tonic_status();
        assert_eq!(
            status.code(),
            tonic::Code::FailedPrecondition,
            "P2-2: PayOS unsupported reconciliation must map to FailedPrecondition (409) so Gateway & Web BFF preserve message, got {status:?}"
        );
        assert_eq!(err.http_status(), axum::http::StatusCode::CONFLICT);
        assert!(
            status.message().contains("cổng chưa có dữ liệu"),
            "expected tonic status message mentioning 'cổng chưa có dữ liệu', got {:?}",
            status.message()
        );
    }

    #[tokio::test]
    async fn test_p1_2_and_p0_1_sepay_pagination_edge_cases_and_tx_id_trim() {
        use axum::{extract::Query, routing::get, Json, Router};
        use std::collections::HashMap;

        // P0-1: ProviderTransaction ingress trims provider_tx_id and drops blank IDs
        let trim_payload = serde_json::json!({
            "transactions": [
                {"id": "  SP-TRIM-1  ", "amount_in": "100000.00", "transaction_content": "NM1111111111", "transaction_date": "2026-11-23 09:00:00"},
                {"id": "   ", "amount_in": "100000.00", "transaction_content": "NM2222222222", "transaction_date": "2026-11-23 09:01:00"}
            ]
        });
        let parsed = parse_sepay_transactions_response(&trim_payload);
        assert_eq!(parsed.len(), 1, "P0-1: blank tx id must be dropped");
        assert_eq!(
            parsed[0].provider_tx_id, "SP-TRIM-1",
            "P0-1: provider_tx_id must be trimmed at ProviderTransaction ingress"
        );

        let app = Router::new().route(
            "/userapi/transactions/list",
            get(|Query(params): Query<HashMap<String, String>>| async move {
                let page: usize = params
                    .get("page")
                    .and_then(|s| s.parse().ok())
                    .unwrap_or(1);
                let mode = params.get("transaction_date_min").map(String::as_str).unwrap_or("");
                let txs = match (mode, page) {
                    // Case A: Exact multiple of cap (limit=2, cap=2 -> 4 incoming across pages 1..2, page 3 empty)
                    ("exact", 1) => serde_json::json!([
                        {"id": "EX-1", "amount_in": "100000", "transaction_content": "NM1111111111", "transaction_date": "2026-11-23 09:00:00"},
                        {"id": "EX-2", "amount_in": "100000", "transaction_content": "NM2222222222", "transaction_date": "2026-11-23 09:01:00"}
                    ]),
                    ("exact", 2) => serde_json::json!([
                        {"id": "EX-3", "amount_in": "100000", "transaction_content": "NM3333333333", "transaction_date": "2026-11-23 09:02:00"},
                        {"id": "EX-4", "amount_in": "100000", "transaction_content": "NM4444444444", "transaction_date": "2026-11-23 09:03:00"}
                    ]),
                    // Case B: Server ignores `page` and always returns same 2 records
                    ("repeat", _) => serde_json::json!([
                        {"id": "REP-1", "amount_in": "100000", "transaction_content": "NM1111111111", "transaction_date": "2026-11-23 09:00:00"},
                        {"id": "REP-2", "amount_in": "100000", "transaction_content": "NM2222222222", "transaction_date": "2026-11-23 09:01:00"}
                    ]),
                    // Case C: Pages 1 & 2 are outgoing-only (amount_in=0); page 3 has 2 incoming; page 4 empty
                    ("outgoing", 1) => serde_json::json!([
                        {"id": "OUT-1", "amount_in": "0.00", "transaction_content": "OUT", "transaction_date": "2026-11-23 09:00:00"},
                        {"id": "OUT-2", "amount_in": "0.00", "transaction_content": "OUT", "transaction_date": "2026-11-23 09:01:00"}
                    ]),
                    ("outgoing", 2) => serde_json::json!([
                        {"id": "OUT-3", "amount_in": "0.00", "transaction_content": "OUT", "transaction_date": "2026-11-23 09:02:00"},
                        {"id": "OUT-4", "amount_in": "0.00", "transaction_content": "OUT", "transaction_date": "2026-11-23 09:03:00"}
                    ]),
                    ("outgoing", 3) => serde_json::json!([
                        {"id": "IN-1", "amount_in": "200000.00", "transaction_content": "NM5555555555", "transaction_date": "2026-11-23 09:04:00"},
                        {"id": "IN-2", "amount_in": "200000.00", "transaction_content": "NM6666666666", "transaction_date": "2026-11-23 09:05:00"}
                    ]),
                    // Case D: Genuine overflow (5 incoming > cap*limit=4)
                    ("overflow", 1) => serde_json::json!([
                        {"id": "OV-1", "amount_in": "100000", "transaction_content": "NM1111111111", "transaction_date": "2026-11-23 09:00:00"},
                        {"id": "OV-2", "amount_in": "100000", "transaction_content": "NM2222222222", "transaction_date": "2026-11-23 09:01:00"}
                    ]),
                    ("overflow", 2) => serde_json::json!([
                        {"id": "OV-3", "amount_in": "100000", "transaction_content": "NM3333333333", "transaction_date": "2026-11-23 09:02:00"},
                        {"id": "OV-4", "amount_in": "100000", "transaction_content": "NM4444444444", "transaction_date": "2026-11-23 09:03:00"}
                    ]),
                    ("overflow", 3) => serde_json::json!([
                        {"id": "OV-5", "amount_in": "100000", "transaction_content": "NM5555555555", "transaction_date": "2026-11-23 09:04:00"}
                    ]),
                    _ => serde_json::json!([]),
                };
                Json(serde_json::json!({ "transactions": txs }))
            }),
        );

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let handle = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });

        let mut cfg = Config::from_env();
        cfg.vietqr_mode = VietQrMode::Sepay;
        cfg.sepay_base_url = format!("http://{addr}");
        cfg.sepay_api_token = "tok".to_string();
        let adapter = VietQrAdapter::from_config(&cfg);

        // Case A: Exact cap multiple -> Ok(4)
        let exact = adapter
            .fetch_sepay_transactions_paginated(Some("exact"), None, 2, 2)
            .await
            .expect("P1-2: exact multiple of cap must NOT fail");
        assert_eq!(exact.len(), 4);

        // Case B: Repeated page (0 new txs on page 2) -> stops and returns Ok(2)
        let repeated = adapter
            .fetch_sepay_transactions_paginated(Some("repeat"), None, 2, 2)
            .await
            .expect("P1-2: repeated page must stop cleanly instead of hitting cap");
        assert_eq!(repeated.len(), 2);

        // Case C: Outgoing transactions do NOT consume incoming cap -> Ok(2)
        let outgoing = adapter
            .fetch_sepay_transactions_paginated(Some("outgoing"), None, 2, 2)
            .await
            .expect("P1-2: outgoing transactions must not trigger incoming pagination cap");
        assert_eq!(outgoing.len(), 2);

        // Case D: 5 incoming > 4 cap -> Err(Provider("...pagination cap..."))
        let ov_err = adapter
            .fetch_sepay_transactions_paginated(Some("overflow"), None, 2, 2)
            .await
            .unwrap_err();
        assert!(
            matches!(ov_err, PaymentError::Provider(ref m) if m.contains("pagination cap")),
            "P1-2: genuine incoming overflow must fail clearly, got {ov_err:?}"
        );

        handle.abort();
    }

    #[tokio::test]
    async fn test_sepay_date_filter_pagination_and_page_cap_error() {
        use axum::{extract::Query, routing::get, Json, Router};
        use std::collections::HashMap;

        let captured_queries: Arc<Mutex<Vec<HashMap<String, String>>>> =
            Arc::new(Mutex::new(Vec::new()));
        let captured_clone = captured_queries.clone();

        let app = Router::new().route(
            "/userapi/transactions/list",
            get(move |Query(params): Query<HashMap<String, String>>| {
                let cap = captured_clone.clone();
                async move {
                    let page: usize = params
                        .get("page")
                        .and_then(|s| s.parse().ok())
                        .unwrap_or(1);
                    let limit: usize = params
                        .get("limit")
                        .and_then(|s| s.parse().ok())
                        .unwrap_or(100);
                    cap.lock().unwrap().push(params);

                    // Serve 2 items on page 1, 2 items on page 2, 1 item on page 3 (with limit=2).
                    let txs = match (limit, page) {
                        (2, 1) => serde_json::json!([
                            {"id": "SP-P1-1", "amount_in": "100000.00", "transaction_content": "NM1111111111", "transaction_date": "2026-11-23 09:00:00"},
                            {"id": "SP-P1-OUT", "amount_in": "0.00", "transaction_content": "OUTGOING", "transaction_date": "2026-11-23 09:01:00"}
                        ]),
                        (2, 2) => serde_json::json!([
                            {"id": "SP-P2-1", "amount_in": "200000.00", "transaction_content": "NM2222222222", "transaction_date": "2026-11-23 10:00:00"},
                            {"id": "SP-P1-1", "amount_in": "100000.00", "transaction_content": "NM1111111111", "transaction_date": "2026-11-23 09:00:00"}
                        ]),
                        (2, 3) => serde_json::json!([
                            {"id": "SP-P3-1", "amount_in": "300000", "transaction_content": "NM3333333333", "transaction_date": "2026-11-23 11:00:00"}
                        ]),
                        _ => serde_json::json!([]),
                    };
                    Json(serde_json::json!({ "transactions": txs }))
                }
            }),
        );

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server_handle = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });

        let mut cfg = Config::from_env();
        cfg.vietqr_mode = VietQrMode::Sepay;
        cfg.sepay_base_url = format!("http://{addr}");
        cfg.sepay_api_token = "test-sepay-token".to_string();
        let adapter = VietQrAdapter::from_config(&cfg);

        // 1. Multi-page fetch (limit=2, max_pages=5) retrieves all 3 pages, dedups SP-P1-1, ignores 0.00 outgoing
        let txs = adapter
            .fetch_sepay_transactions_paginated(
                Some("2026-11-23 00:00:00"),
                Some("2026-11-23 23:59:59"),
                2,
                5,
            )
            .await
            .unwrap();
        assert_eq!(txs.len(), 3);
        assert_eq!(txs[0].provider_tx_id, "SP-P1-1");
        assert_eq!(txs[1].provider_tx_id, "SP-P2-1");
        assert_eq!(txs[2].provider_tx_id, "SP-P3-1");

        let recorded = captured_queries.lock().unwrap().clone();
        assert_eq!(recorded.len(), 3);
        assert_eq!(
            recorded[0].get("transaction_date_min").map(String::as_str),
            Some("2026-11-23 00:00:00")
        );
        assert_eq!(
            recorded[0].get("transaction_date_max").map(String::as_str),
            Some("2026-11-23 23:59:59")
        );
        assert_eq!(recorded[2].get("page").map(String::as_str), Some("3"));

        // 2. Exceeding incoming cap (limit=2, max_pages=1 -> cap 2 incoming, while feed has 3 incoming)
        // MUST fail loudly instead of returning truncated data that would cause false missing_provider!
        let cap_err = adapter
            .fetch_sepay_transactions_paginated(
                Some("2026-11-23 00:00:00"),
                Some("2026-11-23 23:59:59"),
                2,
                1,
            )
            .await
            .unwrap_err();
        assert!(
            matches!(cap_err, PaymentError::Provider(ref m) if m.contains("pagination cap")),
            "P2-3(b): expected explicit pagination cap error, got {cap_err:?}"
        );

        server_handle.abort();
    }

    #[test]
    fn test_sepay_decimal_zero_fraction_accepted_nonzero_rejected() {
        let payload = serde_json::json!({
            "transactions": [
                {
                    "id": "SP-DEC-ZERO",
                    "amount_in": "255000.00",
                    "transaction_content": "NM1111111111",
                    "transaction_date": "2026-11-23 10:00:00"
                },
                {
                    "id": "SP-DEC-ZERO4",
                    "amount_in": "100000.0000",
                    "transaction_content": "NM2222222222",
                    "transaction_date": "2026-11-23 10:05:00"
                },
                {
                    "id": "SP-DEC-NONZERO",
                    "amount_in": "255000.50",
                    "transaction_content": "NM3333333333",
                    "transaction_date": "2026-11-23 10:10:00"
                },
                {
                    "id": "SP-DEC-CENT",
                    "amount_in": "255000.01",
                    "transaction_content": "NM4444444444",
                    "transaction_date": "2026-11-23 10:15:00"
                }
            ]
        });

        let txs = parse_sepay_transactions_response(&payload);
        assert_eq!(
            txs.len(),
            2,
            "P2-8: .00/.0000 must be accepted and non-zero fractions rejected, got {txs:?}"
        );
        assert_eq!(txs[0].provider_tx_id, "SP-DEC-ZERO");
        assert_eq!(txs[0].amount_vnd, 255_000);
        assert_eq!(txs[1].provider_tx_id, "SP-DEC-ZERO4");
        assert_eq!(txs[1].amount_vnd, 100_000);
    }

    #[test]
    fn test_sepay_parses_real_transaction_date_across_vn_midnight_boundary() {
        use crate::domain::utc_to_vn_date;

        let payload = serde_json::json!({
            "transactions": [
                {
                    "id": "SP-1",
                    "amount_in": "255000",
                    "transaction_content": "THANH TOAN NM1234567890",
                    "transaction_date": "2026-11-23 01:22:15"
                },
                {
                    "id": "SP-2",
                    "amount_in": 150000,
                    "transaction_content": "NMABCDEF1234",
                    "transaction_date": "2026-11-22T16:59:59Z"
                },
                {
                    "id": "SP-BAD-DATE",
                    "amount_in": 100000,
                    "transaction_content": "NM9999999999",
                    "transaction_date": "invalid"
                }
            ]
        });

        let txs = parse_sepay_transactions_response(&payload);
        assert_eq!(txs.len(), 2);

        // 2026-11-23 01:22:15 +07:00 == 2026-11-22 18:22:15 UTC -> VN date 2026-11-23
        assert_eq!(txs[0].provider_tx_id, "SP-1");
        assert_eq!(txs[0].provider_order_code, "NM1234567890");
        assert_eq!(txs[0].amount_vnd, 255_000);
        assert_eq!(txs[0].occurred_at.to_rfc3339(), "2026-11-22T18:22:15+00:00");
        assert_eq!(
            utc_to_vn_date(txs[0].occurred_at),
            NaiveDate::from_ymd_opt(2026, 11, 23).unwrap()
        );

        // 2026-11-22 16:59:59 UTC == 2026-11-22 23:59:59 +07:00 -> VN date 2026-11-22
        assert_eq!(txs[1].provider_tx_id, "SP-2");
        assert_eq!(
            utc_to_vn_date(txs[1].occurred_at),
            NaiveDate::from_ymd_opt(2026, 11, 22).unwrap()
        );

        // P3-2: extract_occurred_at_from_json supports SePay/PayOS aliases on raw_payload
        let sepay_raw = serde_json::json!({"transactionDate": "2026-11-23 00:05:00"});
        assert_eq!(
            utc_to_vn_date(extract_occurred_at_from_json(&sepay_raw).unwrap()),
            NaiveDate::from_ymd_opt(2026, 11, 23).unwrap()
        );
        let payos_raw = serde_json::json!({"transactionDateTime": "2026-11-22T23:59:00+07:00"});
        assert_eq!(
            utc_to_vn_date(extract_occurred_at_from_json(&payos_raw).unwrap()),
            NaiveDate::from_ymd_opt(2026, 11, 22).unwrap()
        );
    }
}
