use chrono::{DateTime, Utc};
use hmac::{Hmac, Mac};
use serde::{Deserialize, Serialize};
use sha2::Sha256;
use uuid::Uuid;

use crate::config::is_placeholder_secret;
use crate::error::{PaymentError, Result};

type HmacSha256 = Hmac<Sha256>;

/// Rejects empty secrets everywhere, and rejects any placeholder or repo-committed
/// dev/mock secret when running in production.
pub fn ensure_webhook_secret_usable(secret: &str, is_production: bool) -> Result<()> {
    if is_production && is_placeholder_secret(secret) {
        return Err(PaymentError::ServiceUnavailable(
            "webhook verification disabled: production secret is missing or placeholder"
                .to_string(),
        ));
    }
    if secret.trim().is_empty() {
        return Err(PaymentError::Unauthorized(
            "webhook verification secret is not configured".to_string(),
        ));
    }
    Ok(())
}

/// Computes lowercase hex HMAC-SHA256 digest of `payload` using `secret`.
pub fn compute_hmac_hex(secret: &str, payload: &[u8]) -> String {
    let mut mac =
        HmacSha256::new_from_slice(secret.as_bytes()).expect("HMAC accepts key of any length");
    mac.update(payload);
    hex::encode(mac.finalize().into_bytes())
}

/// Constant-time HMAC-SHA256 signature verification using `hmac::Mac::verify_slice`.
/// Supports either raw hex (`<hex>`) or prefixed (`sha256=<hex>`).
/// Strictly requires `X-Timestamp` (per `docs/contracts.md:92`) and rejects requests whose
/// timestamp skew exceeds `replay_window_secs` (default 300s) to prevent replay attacks.
/// Supports signing raw body OR `"{timestamp}.{raw_body}"`.
pub fn verify_webhook_request(
    secret: &str,
    raw_body: &[u8],
    signature_header: Option<&str>,
    timestamp_header: Option<&str>,
    replay_window_secs: i64,
    now: DateTime<Utc>,
) -> Result<()> {
    if secret.trim().is_empty() {
        return Err(PaymentError::Unauthorized(
            "webhook verification secret is not configured".to_string(),
        ));
    }

    let raw_sig = signature_header
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .ok_or_else(|| PaymentError::Unauthorized("missing X-Signature header".to_string()))?;

    let hex_sig = raw_sig
        .strip_prefix("sha256=")
        .or_else(|| raw_sig.strip_prefix("SHA256="))
        .unwrap_or(raw_sig);

    let provided_bytes = hex::decode(hex_sig)
        .map_err(|_| PaymentError::Unauthorized("malformed X-Signature hex".to_string()))?;

    // Strict replay window check: X-Timestamp is REQUIRED so attackers cannot bypass
    // replay protection by omitting the header.
    let ts_raw = timestamp_header
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .ok_or_else(|| PaymentError::Unauthorized("missing X-Timestamp header".to_string()))?;

    let ts = parse_timestamp(ts_raw)?;
    let drift_secs = (now - ts).num_seconds().abs();
    if drift_secs > replay_window_secs {
        return Err(PaymentError::Unauthorized(format!(
            "stale or skewed X-Timestamp (drift={drift_secs}s, max={replay_window_secs}s)"
        )));
    }

    // Primary check: HMAC-SHA256(secret, raw_body) using constant-time verify_slice
    let mut mac_body =
        HmacSha256::new_from_slice(secret.as_bytes()).expect("HMAC accepts key of any length");
    mac_body.update(raw_body);
    if mac_body.verify_slice(&provided_bytes).is_ok() {
        return Ok(());
    }

    // Secondary check: HMAC-SHA256(secret, "{ts}.{raw_body}")
    let mut mac_ts =
        HmacSha256::new_from_slice(secret.as_bytes()).expect("HMAC accepts key of any length");
    mac_ts.update(ts_raw.as_bytes());
    mac_ts.update(b".");
    mac_ts.update(raw_body);
    if mac_ts.verify_slice(&provided_bytes).is_ok() {
        return Ok(());
    }

    Err(PaymentError::Unauthorized(
        "invalid webhook HMAC signature".to_string(),
    ))
}

fn parse_timestamp(raw: &str) -> Result<DateTime<Utc>> {
    if let Ok(secs) = raw.parse::<i64>() {
        if secs <= 0 {
            return Err(PaymentError::Unauthorized(
                "invalid non-positive X-Timestamp value".to_string(),
            ));
        }
        // Handle both Unix seconds and Unix milliseconds
        let dt = if secs > 10_000_000_000 {
            DateTime::from_timestamp_millis(secs)
        } else {
            DateTime::from_timestamp(secs, 0)
        };
        return dt.ok_or_else(|| {
            PaymentError::Unauthorized("out-of-range X-Timestamp value".to_string())
        });
    }
    DateTime::parse_from_rfc3339(raw)
        .map(|dt| dt.with_timezone(&Utc))
        .map_err(|_| PaymentError::Unauthorized("invalid X-Timestamp format".to_string()))
}

/// Incoming webhook JSON payload aligned with `docs/contracts.md` section 4.9,
/// plus payOS / SePay field aliases.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IncomingWebhookPayload {
    #[serde(alias = "transaction_id", alias = "tx_id")]
    pub provider_tx_id: String,
    #[serde(default)]
    pub payment_id: Option<String>,
    #[serde(default)]
    pub order_group_id: Option<String>,
    #[serde(default, alias = "order_ref", alias = "orderCode")]
    pub order_id: Option<String>,
    #[serde(default, alias = "order_code")]
    pub provider_order_code: Option<String>,
    #[serde(default, alias = "content", alias = "description")]
    pub reference: Option<String>,
    #[serde(default, alias = "amount", alias = "transferAmount")]
    pub amount_vnd: i64,
    #[serde(default = "default_succeeded")]
    pub status: String,
    #[serde(default)]
    pub provider: Option<String>,
    #[serde(default)]
    pub occurred_at: Option<String>,
}

fn default_succeeded() -> String {
    "SUCCEEDED".to_string()
}

impl IncomingWebhookPayload {
    pub fn validate(&self) -> Result<()> {
        if self.provider_tx_id.trim().is_empty() {
            return Err(PaymentError::Validation(
                "provider_tx_id is required".to_string(),
            ));
        }
        if !self.is_failed_event() && self.amount_vnd <= 0 {
            return Err(PaymentError::Validation(
                "amount_vnd must be positive integer VND".to_string(),
            ));
        }
        if self.amount_vnd < 0 {
            return Err(PaymentError::Validation(
                "amount_vnd must not be negative".to_string(),
            ));
        }
        Ok(())
    }

    pub fn parsed_payment_id(&self) -> Option<Uuid> {
        self.payment_id
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .and_then(|s| Uuid::parse_str(s).ok())
    }

    /// Returns the preferred order reference (`order_id` or `order_group_id`).
    pub fn effective_order_ref(&self) -> Option<&str> {
        self.order_id
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .or_else(|| {
                self.order_group_id
                    .as_deref()
                    .map(str::trim)
                    .filter(|s| !s.is_empty())
            })
    }

    /// Extracts NexusMart provider order code (`NM<HEX>`) from explicit field or bank transfer reference.
    pub fn extracted_order_code(&self) -> Option<String> {
        if let Some(ref code) = self.provider_order_code {
            let trimmed = code.trim();
            if !trimmed.is_empty() {
                return Some(trimmed.to_string());
            }
        }
        if let Some(ref content) = self.reference {
            for token in content.split(|c: char| !c.is_ascii_alphanumeric()) {
                let upper = token.to_ascii_uppercase();
                if upper.starts_with("NM") && upper.len() >= 8 {
                    return Some(upper);
                }
            }
        }
        None
    }

    pub fn is_failed_event(&self) -> bool {
        matches!(
            self.status.trim().to_ascii_lowercase().as_str(),
            "failed" | "cancelled" | "canceled" | "expired"
        )
    }
}

/// Pure decision engine for webhook reconciliation rules so every edge case is unit-testable.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WebhookDecision {
    QueueReconciliation { reason: &'static str },
    MarkFailed,
    ConfirmPayment,
    LatePaymentRefund { reason: &'static str },
}

pub fn decide_webhook_action(
    payment_status: Option<&str>,
    expected_amount_vnd: Option<i64>,
    paid_amount_vnd: i64,
    is_failed_webhook: bool,
    order_already_has_succeeded_payment: bool,
) -> WebhookDecision {
    let (Some(status), Some(expected)) = (payment_status, expected_amount_vnd) else {
        return WebhookDecision::QueueReconciliation {
            reason: "order_not_found",
        };
    };

    if is_failed_webhook {
        return WebhookDecision::MarkFailed;
    }

    // UC-PAY01 / UC-A14: amount mismatch MUST NOT auto-confirm; send to reconciliation queue
    if paid_amount_vnd != expected {
        return WebhookDecision::QueueReconciliation {
            reason: "amount_mismatch",
        };
    }

    // UC-PAY01 / UC-B25: payment expired/cancelled/superseded or order already succeeded -> auto-refund
    match status {
        "expired" | "cancelled" | "superseded" => WebhookDecision::LatePaymentRefund {
            reason: "late_payment_after_expired_or_cancelled",
        },
        "succeeded" | "refunded" => WebhookDecision::LatePaymentRefund {
            reason: "duplicate_payment_for_already_succeeded_intent",
        },
        "pending" if order_already_has_succeeded_payment => WebhookDecision::LatePaymentRefund {
            reason: "order_already_has_succeeded_payment",
        },
        "pending" => WebhookDecision::ConfirmPayment,
        _ => WebhookDecision::QueueReconciliation {
            reason: "unexpected_payment_status",
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::Duration;
    use std::collections::HashSet;

    #[test]
    fn test_contracts_canonical_webhook_payload_deserializes() {
        let json_str = r#"{
            "provider": "vietqr_mock",
            "provider_tx_id": "TXN-20261120-0001",
            "payment_id": "11111111-2222-3333-4444-555555555555",
            "order_group_id": "grp-uuid",
            "amount_vnd": 250000,
            "status": "SUCCEEDED",
            "reference": "NEXUSMART GRP-UUID",
            "occurred_at": "2026-11-20T02:35:00Z"
        }"#;
        let payload: IncomingWebhookPayload = serde_json::from_str(json_str).unwrap();
        assert_eq!(payload.provider_tx_id, "TXN-20261120-0001");
        assert_eq!(payload.amount_vnd, 250_000);
        assert_eq!(payload.effective_order_ref(), Some("grp-uuid"));
        assert!(payload.parsed_payment_id().is_some());
        assert!(!payload.is_failed_event());
    }

    #[test]
    fn test_hmac_verification_valid_and_invalid() {
        let secret = "test-webhook-secret-999";
        let body = br#"{"provider_tx_id":"TX001","order_id":"ord-1","amount_vnd":150000}"#;
        let sig = compute_hmac_hex(secret, body);
        let now = Utc::now();
        let ts = now.timestamp().to_string();

        assert!(verify_webhook_request(secret, body, Some(&sig), Some(&ts), 300, now).is_ok());
        let prefixed = format!("sha256={sig}");
        assert!(verify_webhook_request(secret, body, Some(&prefixed), Some(&ts), 300, now).is_ok());
        // Missing X-Timestamp MUST fail (prevents replay bypass)
        assert!(matches!(
            verify_webhook_request(secret, body, Some(&sig), None, 300, now),
            Err(PaymentError::Unauthorized(_))
        ));
        assert!(matches!(
            verify_webhook_request(secret, body, Some(&sig), Some("  "), 300, now),
            Err(PaymentError::Unauthorized(_))
        ));
        let bad_sig = compute_hmac_hex("wrong-secret", body);
        assert!(matches!(
            verify_webhook_request(secret, body, Some(&bad_sig), Some(&ts), 300, now),
            Err(PaymentError::Unauthorized(_))
        ));
        assert!(matches!(
            verify_webhook_request(secret, body, None, Some(&ts), 300, now),
            Err(PaymentError::Unauthorized(_))
        ));
        let tampered = br#"{"provider_tx_id":"TX001","order_id":"ord-1","amount_vnd":1}"#;
        assert!(matches!(
            verify_webhook_request(secret, tampered, Some(&sig), Some(&ts), 300, now),
            Err(PaymentError::Unauthorized(_))
        ));
    }

    #[test]
    fn test_replay_protection_timestamp_window() {
        let secret = "test-secret";
        let body = br#"{"provider_tx_id":"TX002","amount_vnd":50000}"#;
        let now = Utc::now();

        let fresh_ts = (now - Duration::seconds(10)).timestamp().to_string();
        let signed_payload = format!("{fresh_ts}.{}", String::from_utf8_lossy(body));
        let sig_ts = compute_hmac_hex(secret, signed_payload.as_bytes());
        assert!(
            verify_webhook_request(secret, body, Some(&sig_ts), Some(&fresh_ts), 300, now).is_ok()
        );

        let sig_body = compute_hmac_hex(secret, body);
        assert!(
            verify_webhook_request(secret, body, Some(&sig_body), Some(&fresh_ts), 300, now)
                .is_ok()
        );

        let stale_ts = (now - Duration::seconds(301)).timestamp().to_string();
        assert!(matches!(
            verify_webhook_request(secret, body, Some(&sig_body), Some(&stale_ts), 300, now),
            Err(PaymentError::Unauthorized(_))
        ));

        let future_ts = (now + Duration::seconds(305)).timestamp().to_string();
        assert!(matches!(
            verify_webhook_request(secret, body, Some(&sig_body), Some(&future_ts), 300, now),
            Err(PaymentError::Unauthorized(_))
        ));
    }

    #[test]
    fn test_webhook_10x_idempotency_simulation() {
        let mut processed_tx_ids: HashSet<String> = HashSet::new();
        let mut ledger_post_count = 0;

        for _ in 0..10 {
            let tx_id = "SEPAY-TX-99887766".to_string();
            if processed_tx_ids.insert(tx_id) {
                let decision =
                    decide_webhook_action(Some("pending"), Some(250_000), 250_000, false, false);
                assert_eq!(decision, WebhookDecision::ConfirmPayment);
                ledger_post_count += 1;
            }
        }
        assert_eq!(ledger_post_count, 1);
    }

    #[test]
    fn test_webhook_amount_mismatch_and_late_payment_decisions() {
        assert_eq!(
            decide_webhook_action(Some("pending"), Some(100_000), 99_000, false, false),
            WebhookDecision::QueueReconciliation {
                reason: "amount_mismatch"
            }
        );
        assert_eq!(
            decide_webhook_action(Some("pending"), Some(100_000), 150_000, false, false),
            WebhookDecision::QueueReconciliation {
                reason: "amount_mismatch"
            }
        );
        assert_eq!(
            decide_webhook_action(None, None, 100_000, false, false),
            WebhookDecision::QueueReconciliation {
                reason: "order_not_found"
            }
        );
        assert_eq!(
            decide_webhook_action(Some("expired"), Some(100_000), 100_000, false, false),
            WebhookDecision::LatePaymentRefund {
                reason: "late_payment_after_expired_or_cancelled"
            }
        );
        assert_eq!(
            decide_webhook_action(Some("superseded"), Some(100_000), 100_000, false, true),
            WebhookDecision::LatePaymentRefund {
                reason: "late_payment_after_expired_or_cancelled"
            }
        );
        // Another intent already succeeded for same order -> automatic refund
        assert_eq!(
            decide_webhook_action(Some("pending"), Some(100_000), 100_000, false, true),
            WebhookDecision::LatePaymentRefund {
                reason: "order_already_has_succeeded_payment"
            }
        );
        // Failed webhook (even with amount_vnd=0) marks payment failed for saga compensation
        assert_eq!(
            decide_webhook_action(Some("pending"), Some(100_000), 0, true, false),
            WebhookDecision::MarkFailed
        );
    }

    #[test]
    fn test_extract_order_code_from_sepay_transfer_content() {
        let payload = IncomingWebhookPayload {
            provider_tx_id: "SP-1001".into(),
            payment_id: None,
            order_group_id: None,
            order_id: None,
            provider_order_code: None,
            reference: Some("CHUYEN KHOAN THANH TOAN NM1A2B3C4D5E NEXUSMART".into()),
            amount_vnd: 120_000,
            status: "SUCCEEDED".into(),
            provider: Some("sepay".into()),
            occurred_at: None,
        };
        assert_eq!(
            payload.extracted_order_code().as_deref(),
            Some("NM1A2B3C4D5E")
        );
    }

    #[test]
    fn test_empty_secret_and_production_placeholder_guard() {
        let body = br#"{"provider_tx_id":"TX-EMPTY","amount_vnd":50000}"#;
        let now = Utc::now();
        let ts = now.timestamp().to_string();
        let empty_sig = compute_hmac_hex("", body);

        assert!(matches!(
            verify_webhook_request("", body, Some(&empty_sig), Some(&ts), 300, now),
            Err(PaymentError::Unauthorized(_))
        ));
        assert!(matches!(
            verify_webhook_request("   ", body, Some(&empty_sig), Some(&ts), 300, now),
            Err(PaymentError::Unauthorized(_))
        ));

        for repo_secret in [
            "",
            "dev-webhook-secret-change-me",
            "dev-only-payment-webhook-hmac-secret",
            "zalopay-mock-key2-dev",
            "sandbox-zalopay-key2-placeholder",
        ] {
            assert!(
                matches!(
                    ensure_webhook_secret_usable(repo_secret, true),
                    Err(PaymentError::ServiceUnavailable(_))
                ),
                "production must reject {repo_secret:?}"
            );
        }

        // Dev still permits non-empty dev secrets, while empty secret is always rejected.
        assert!(matches!(
            ensure_webhook_secret_usable("", false),
            Err(PaymentError::Unauthorized(_))
        ));
        assert!(
            ensure_webhook_secret_usable("dev-only-payment-webhook-hmac-secret", false).is_ok()
        );
        assert!(ensure_webhook_secret_usable("zalopay-mock-key2-dev", false).is_ok());
        assert!(ensure_webhook_secret_usable("real-prod-hmac-secret-9f8e7d6c", true).is_ok());
    }
}
