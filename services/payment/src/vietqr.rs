use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use chrono::{DateTime, Utc};
use reqwest::Client;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tokio::time::sleep;
use tracing::{info, warn};
use uuid::Uuid;

use crate::config::{Config, VietQrMode};
use crate::error::{PaymentError, Result};
use crate::webhook_security::{compute_hmac_hex, IncomingWebhookPayload};

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
    mock_transactions: Arc<Mutex<Vec<ProviderTransaction>>>,
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
            mock_transactions: Arc::new(Mutex::new(Vec::new())),
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

    /// Checks final transaction status at provider before expiring a pending payment (UC-PAY06).
    /// Returns `Ok(Some(tx))` if provider confirms paid, `Ok(None)` if unpaid.
    pub async fn check_payment_status(
        &self,
        provider_order_code: &str,
    ) -> Result<Option<ProviderTransaction>> {
        match self.mode {
            VietQrMode::Mock => {
                let guard = self.mock_transactions.lock().expect("mock tx lock");
                Ok(guard
                    .iter()
                    .find(|t| {
                        t.provider_order_code == provider_order_code && t.status == "succeeded"
                    })
                    .cloned())
            }
            VietQrMode::PayOs => self.query_payos_status(provider_order_code).await,
            VietQrMode::Sepay => {
                let list = self.fetch_sepay_transactions().await?;
                Ok(list
                    .into_iter()
                    .find(|t| t.provider_order_code == provider_order_code))
            }
        }
    }

    /// Fetches provider transactions for reconciliation (UC-PAY07).
    pub async fn list_provider_transactions(&self) -> Result<Vec<ProviderTransaction>> {
        match self.mode {
            VietQrMode::Mock => {
                let guard = self.mock_transactions.lock().expect("mock tx lock");
                Ok(guard.clone())
            }
            VietQrMode::PayOs => Ok(Vec::new()),
            VietQrMode::Sepay => self.fetch_sepay_transactions().await,
        }
    }

    /// Records a mock provider settlement (used in mock mode when webhook confirms or in tests).
    pub fn record_mock_transaction(&self, tx: ProviderTransaction) {
        if self.mode == VietQrMode::Mock {
            let mut guard = self.mock_transactions.lock().expect("mock tx lock");
            if !guard.iter().any(|t| t.provider_tx_id == tx.provider_tx_id) {
                guard.push(tx);
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
                .map(ToString::to_string)
                .unwrap_or_else(|| format!("payos-{numeric_order_code}"));
            return Ok(Some(ProviderTransaction {
                provider_tx_id: tx_id,
                provider_order_code: provider_order_code.to_string(),
                amount_vnd,
                status: "succeeded".to_string(),
                occurred_at: Utc::now(),
            }));
        }

        Ok(None)
    }

    async fn fetch_sepay_transactions(&self) -> Result<Vec<ProviderTransaction>> {
        let url = format!(
            "{}/userapi/transactions/list?limit=100",
            self.sepay_base_url.trim_end_matches('/')
        );

        let response: serde_json::Value = self
            .execute_with_retry("sepay_list_transactions", || {
                self.http_client
                    .get(&url)
                    .bearer_auth(&self.sepay_api_token)
            })
            .await?;

        let mut out = Vec::new();
        if let Some(items) = response.get("transactions").and_then(|v| v.as_array()) {
            for item in items {
                let amount_in = item
                    .get("amount_in")
                    .and_then(|v| {
                        v.as_i64()
                            .or_else(|| v.as_str().and_then(|s| s.parse::<i64>().ok()))
                    })
                    .unwrap_or(0);
                if amount_in <= 0 {
                    continue;
                }
                let tx_id = item
                    .get("id")
                    .and_then(|v| {
                        v.as_str()
                            .map(ToString::to_string)
                            .or_else(|| v.as_i64().map(|n| n.to_string()))
                    })
                    .unwrap_or_default();
                let content = item
                    .get("transaction_content")
                    .and_then(|v| v.as_str())
                    .unwrap_or("");
                let order_code = content
                    .split(|c: char| !c.is_ascii_alphanumeric())
                    .find(|tok| tok.to_ascii_uppercase().starts_with("NM") && tok.len() >= 8)
                    .map(|s| s.to_ascii_uppercase())
                    .unwrap_or_default();

                if !tx_id.is_empty() {
                    out.push(ProviderTransaction {
                        provider_tx_id: tx_id,
                        provider_order_code: order_code,
                        amount_vnd: amount_in,
                        status: "succeeded".to_string(),
                        occurred_at: Utc::now(),
                    });
                }
            }
        }
        Ok(out)
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
                                    "external provider call succeeded"
                                );
                                return Ok(val);
                            }
                            Err(e) => {
                                last_err = format!("invalid provider JSON response: {e}");
                            }
                        }
                    } else if status.is_client_error() && status.as_u16() != 429 {
                        // Do not retry non-429 4xx client errors
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
                "external provider call failed"
            );

            if attempt + 1 < total_attempts {
                let backoff_ms = 200_u64.saturating_mul(1_u64 << attempt.min(5));
                sleep(Duration::from_millis(backoff_ms)).await;
            }
        }

        Err(PaymentError::Provider(last_err))
    }
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
    }
}
