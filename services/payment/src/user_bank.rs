//! Internal gRPC client to `services/user` for fetching verified shop bank accounts
//! (`UserService.GetShopBankAccountForPayout`) using a signed service token.
//! Never logs or persists raw bank account numbers.

use std::time::Duration;

use chrono::{DateTime, Utc};
use tokio::time::sleep;
use tonic::metadata::MetadataValue;
use tonic::transport::Endpoint;
use tonic::{Code, Request};
use tracing::warn;

use crate::auth::sign_service_token;
use crate::error::{PaymentError, Result};
use crate::telemetry::mask_bank_account;
use crate::vietqr::CircuitBreaker;

#[allow(clippy::result_large_err)]
pub mod user_pb {
    tonic::include_proto!("nexusmart.user.v1");
}

use user_pb::user_service_client::UserServiceClient;
use user_pb::{GetShopBankAccountForPayoutRequest, ShopBankAccountStatus};

const USER_BANK_CONNECT_TIMEOUT: Duration = Duration::from_secs(3);
const USER_BANK_RPC_TIMEOUT: Duration = Duration::from_secs(5);
const USER_BANK_MAX_RETRIES: u32 = 2;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct VerifiedPayoutBankAccount {
    pub bank_name: String,
    pub bank_account_masked: String,
    pub account_holder_name: String,
    pub payout_locked_until: Option<DateTime<Utc>>,
}

#[tonic::async_trait]
pub trait PayoutBankAccountProvider: Send + Sync {
    async fn get_verified_bank_account(&self, shop_id: &str) -> Result<VerifiedPayoutBankAccount>;
}

/// Evaluates whether a fetched bank account is verified and outside any 24h security lock.
pub fn validate_bank_account_ready(
    account: &VerifiedPayoutBankAccount,
    now: DateTime<Utc>,
) -> Result<()> {
    if account.bank_account_masked.trim().is_empty() {
        return Err(PaymentError::BankAccountNotReady(
            "shop bank account metadata is incomplete".to_string(),
        ));
    }
    if let Some(locked_until) = account.payout_locked_until {
        if locked_until > now {
            return Err(PaymentError::BankAccountNotReady(format!(
                "payout locked for 24h after bank account change until {}",
                locked_until.to_rfc3339()
            )));
        }
    }
    Ok(())
}

/// Production gRPC provider calling `UserService.GetShopBankAccountForPayout`
/// with timeout, bounded exponential-backoff retry on transient errors, and circuit breaker.
#[derive(Clone)]
pub struct GrpcUserBankProvider {
    endpoint: String,
    service_jwt_secret: String,
    breaker: CircuitBreaker,
}

impl GrpcUserBankProvider {
    pub fn new(endpoint: impl Into<String>, service_jwt_secret: impl Into<String>) -> Self {
        Self {
            endpoint: endpoint.into(),
            service_jwt_secret: service_jwt_secret.into(),
            breaker: CircuitBreaker::new(5, Duration::from_secs(30)),
        }
    }

    #[allow(clippy::result_large_err)]
    async fn fetch_once(
        &self,
        shop_id: &str,
    ) -> std::result::Result<VerifiedPayoutBankAccount, tonic::Status> {
        let endpoint = Endpoint::from_shared(self.endpoint.clone())
            .map_err(|e| tonic::Status::internal(format!("invalid user grpc endpoint: {e}")))?
            .connect_timeout(USER_BANK_CONNECT_TIMEOUT)
            .timeout(USER_BANK_RPC_TIMEOUT);

        let channel = endpoint
            .connect()
            .await
            .map_err(|e| tonic::Status::unavailable(format!("connect user service failed: {e}")))?;

        let token = sign_service_token(&self.service_jwt_secret, "payment-service")
            .map_err(|e| tonic::Status::internal(e.to_string()))?;

        let mut req = Request::new(GetShopBankAccountForPayoutRequest {
            shop_id: shop_id.to_string(),
        });
        req.set_timeout(USER_BANK_RPC_TIMEOUT);
        let auth_val = MetadataValue::try_from(format!("Bearer {token}"))
            .map_err(|_| tonic::Status::internal("invalid authorization metadata"))?;
        req.metadata_mut().insert("authorization", auth_val);

        let mut client = UserServiceClient::new(channel);
        let resp = client
            .get_shop_bank_account_for_payout(req)
            .await?
            .into_inner();
        let Some(acc) = resp.bank_account else {
            return Err(tonic::Status::not_found(
                "verified shop bank account not found",
            ));
        };

        let status = ShopBankAccountStatus::try_from(acc.status)
            .unwrap_or(ShopBankAccountStatus::Unspecified);
        if status != ShopBankAccountStatus::Verified {
            return Err(tonic::Status::failed_precondition(
                "shop bank account is not verified",
            ));
        }

        let masked = if !acc.account_number_masked.trim().is_empty() {
            acc.account_number_masked.trim().to_string()
        } else {
            mask_bank_account(&acc.account_number)
        };
        let bank_name = if !acc.bank_name.trim().is_empty() {
            acc.bank_name.trim().to_string()
        } else {
            acc.bank_code.trim().to_string()
        };
        let payout_locked_until = acc
            .payout_locked_until
            .and_then(|ts| DateTime::<Utc>::from_timestamp(ts.seconds, ts.nanos.max(0) as u32));

        Ok(VerifiedPayoutBankAccount {
            bank_name,
            bank_account_masked: masked,
            account_holder_name: acc.account_holder_name.trim().to_string(),
            payout_locked_until,
        })
    }
}

#[tonic::async_trait]
impl PayoutBankAccountProvider for GrpcUserBankProvider {
    async fn get_verified_bank_account(&self, shop_id: &str) -> Result<VerifiedPayoutBankAccount> {
        self.breaker.allow_request()?;

        let mut last_err: Option<tonic::Status> = None;
        for attempt in 0..=USER_BANK_MAX_RETRIES {
            match self.fetch_once(shop_id).await {
                Ok(acc) => {
                    self.breaker.record_success();
                    validate_bank_account_ready(&acc, Utc::now())?;
                    return Ok(acc);
                }
                Err(status) => match status.code() {
                    Code::NotFound | Code::FailedPrecondition => {
                        // Definitive domain result (bank account missing or unverified): do not trip breaker.
                        self.breaker.record_success();
                        return Err(PaymentError::BankAccountNotReady(
                            "shop does not have a verified bank account".to_string(),
                        ));
                    }
                    Code::Unavailable | Code::DeadlineExceeded => {
                        self.breaker.record_failure();
                        warn!(
                            shop_id = %shop_id,
                            attempt = attempt + 1,
                            code = ?status.code(),
                            "transient error fetching shop bank account from user service"
                        );
                        last_err = Some(status);
                        if attempt < USER_BANK_MAX_RETRIES {
                            let backoff_ms = 100_u64.saturating_mul(1_u64 << attempt);
                            sleep(Duration::from_millis(backoff_ms)).await;
                        }
                    }
                    _ => {
                        self.breaker.record_failure();
                        return Err(PaymentError::ServiceUnavailable(
                            "failed to verify shop bank account".to_string(),
                        ));
                    }
                },
            }
        }

        let _ = last_err;
        Err(PaymentError::ServiceUnavailable(
            "user service unavailable while verifying shop bank account".to_string(),
        ))
    }
}

/// Deterministic in-process provider for unit and DB integration tests.
#[derive(Clone)]
pub struct StaticPayoutBankProvider {
    outcome: std::result::Result<VerifiedPayoutBankAccount, String>,
}

impl StaticPayoutBankProvider {
    pub fn ready(
        bank_name: impl Into<String>,
        bank_account_masked: impl Into<String>,
        account_holder_name: impl Into<String>,
    ) -> Self {
        Self {
            outcome: Ok(VerifiedPayoutBankAccount {
                bank_name: bank_name.into(),
                bank_account_masked: bank_account_masked.into(),
                account_holder_name: account_holder_name.into(),
                payout_locked_until: None,
            }),
        }
    }

    pub fn locked(
        bank_name: impl Into<String>,
        bank_account_masked: impl Into<String>,
        account_holder_name: impl Into<String>,
        locked_until: DateTime<Utc>,
    ) -> Self {
        Self {
            outcome: Ok(VerifiedPayoutBankAccount {
                bank_name: bank_name.into(),
                bank_account_masked: bank_account_masked.into(),
                account_holder_name: account_holder_name.into(),
                payout_locked_until: Some(locked_until),
            }),
        }
    }

    pub fn unverified(reason: impl Into<String>) -> Self {
        Self {
            outcome: Err(reason.into()),
        }
    }
}

#[tonic::async_trait]
impl PayoutBankAccountProvider for StaticPayoutBankProvider {
    async fn get_verified_bank_account(&self, _shop_id: &str) -> Result<VerifiedPayoutBankAccount> {
        match &self.outcome {
            Ok(acc) => {
                validate_bank_account_ready(acc, Utc::now())?;
                Ok(acc.clone())
            }
            Err(reason) => Err(PaymentError::BankAccountNotReady(reason.clone())),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::Duration as ChronoDuration;

    #[test]
    fn test_validate_bank_account_ready_checks_24h_lock() {
        let now = Utc::now();
        let ready = VerifiedPayoutBankAccount {
            bank_name: "Vietcombank".into(),
            bank_account_masked: "****0123".into(),
            account_holder_name: "NGUYEN VAN A".into(),
            payout_locked_until: None,
        };
        assert!(validate_bank_account_ready(&ready, now).is_ok());

        let unlocked = VerifiedPayoutBankAccount {
            payout_locked_until: Some(now - ChronoDuration::seconds(1)),
            ..ready.clone()
        };
        assert!(validate_bank_account_ready(&unlocked, now).is_ok());

        let locked = VerifiedPayoutBankAccount {
            payout_locked_until: Some(now + ChronoDuration::hours(24)),
            ..ready
        };
        let err = validate_bank_account_ready(&locked, now).unwrap_err();
        assert!(matches!(err, PaymentError::BankAccountNotReady(_)));
    }
}
