use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde_json::json;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum PaymentError {
    #[error("validation error: {0}")]
    Validation(String),

    #[error("not found: {0}")]
    NotFound(String),

    #[error("conflict: {0}")]
    Conflict(String),

    #[error("failed precondition: {0}")]
    FailedPrecondition(String),

    #[error("unauthorized: {0}")]
    Unauthorized(String),

    #[error("forbidden: {0}")]
    Forbidden(String),

    #[error("ledger invariant violation: {0}")]
    LedgerInvariant(String),

    #[error("insufficient balance: available={available_vnd} requested={requested_vnd}")]
    InsufficientBalance {
        available_vnd: i64,
        requested_vnd: i64,
    },

    #[error("payout pending exists: {0}")]
    PayoutPendingExists(String),

    #[error("bank account not ready: {0}")]
    BankAccountNotReady(String),

    #[error("refund exceeds paid amount: remaining={remaining_vnd} requested={requested_vnd}")]
    RefundExceedsPaid {
        remaining_vnd: i64,
        requested_vnd: i64,
    },

    #[error("external provider error: {0}")]
    Provider(String),

    #[error("{0}")]
    ProviderUnsupported(String),

    #[error("provider circuit breaker is open")]
    CircuitOpen,

    #[error("service unavailable: {0}")]
    ServiceUnavailable(String),

    #[error("database error: {0}")]
    Database(#[from] sqlx::Error),

    #[error("internal error: {0}")]
    Internal(String),
}

impl PaymentError {
    pub fn http_status(&self) -> StatusCode {
        match self {
            Self::Validation(_) => StatusCode::BAD_REQUEST,
            Self::NotFound(_) => StatusCode::NOT_FOUND,
            Self::Conflict(_)
            | Self::FailedPrecondition(_)
            | Self::InsufficientBalance { .. }
            | Self::PayoutPendingExists(_)
            | Self::RefundExceedsPaid { .. }
            | Self::ProviderUnsupported(_) => StatusCode::CONFLICT,
            Self::BankAccountNotReady(_) => StatusCode::PRECONDITION_FAILED,
            Self::Unauthorized(_) => StatusCode::UNAUTHORIZED,
            Self::Forbidden(_) => StatusCode::FORBIDDEN,
            Self::Provider(_) | Self::CircuitOpen => StatusCode::BAD_GATEWAY,
            Self::ServiceUnavailable(_) => StatusCode::SERVICE_UNAVAILABLE,
            Self::LedgerInvariant(_) | Self::Database(_) | Self::Internal(_) => {
                StatusCode::INTERNAL_SERVER_ERROR
            }
        }
    }

    pub fn error_code(&self) -> &'static str {
        match self {
            Self::Validation(_) => "VALIDATION_ERROR",
            Self::NotFound(_) => "NOT_FOUND",
            Self::Conflict(_) => "CONFLICT",
            Self::FailedPrecondition(_) => "FAILED_PRECONDITION",
            Self::Unauthorized(_) => "UNAUTHORIZED",
            Self::Forbidden(_) => "FORBIDDEN",
            Self::LedgerInvariant(_) => "LEDGER_INVARIANT_ERROR",
            Self::InsufficientBalance { .. } => "INSUFFICIENT_BALANCE",
            Self::PayoutPendingExists(_) => "PAYOUT_PENDING_EXISTS",
            Self::BankAccountNotReady(_) => "BANK_ACCOUNT_NOT_READY",
            Self::RefundExceedsPaid { .. } => "REFUND_EXCEEDS_PAID",
            Self::Provider(_) => "PROVIDER_ERROR",
            Self::ProviderUnsupported(_) => "PROVIDER_UNSUPPORTED",
            Self::CircuitOpen => "PROVIDER_CIRCUIT_OPEN",
            Self::ServiceUnavailable(_) => "SERVICE_UNAVAILABLE",
            Self::Database(_) => "DATABASE_ERROR",
            Self::Internal(_) => "INTERNAL_ERROR",
        }
    }

    pub fn to_tonic_status(&self) -> tonic::Status {
        match self {
            Self::Validation(m) => tonic::Status::invalid_argument(m.clone()),
            Self::NotFound(m) => tonic::Status::not_found(m.clone()),
            Self::Conflict(m) => tonic::Status::already_exists(m.clone()),
            Self::FailedPrecondition(m) => tonic::Status::failed_precondition(m.clone()),
            Self::Unauthorized(m) => tonic::Status::unauthenticated(m.clone()),
            Self::Forbidden(m) => tonic::Status::permission_denied(m.clone()),
            Self::InsufficientBalance {
                available_vnd,
                requested_vnd,
            } => tonic::Status::failed_precondition(format!(
                "INSUFFICIENT_BALANCE: available_vnd={available_vnd} requested_vnd={requested_vnd}"
            )),
            Self::PayoutPendingExists(m) => {
                tonic::Status::failed_precondition(format!("PAYOUT_PENDING_EXISTS: {m}"))
            }
            Self::BankAccountNotReady(m) => {
                tonic::Status::failed_precondition(format!("BANK_ACCOUNT_NOT_READY: {m}"))
            }
            Self::RefundExceedsPaid { .. } => tonic::Status::failed_precondition(self.to_string()),
            Self::Provider(m) => tonic::Status::unavailable(m.clone()),
            Self::ProviderUnsupported(m) => tonic::Status::failed_precondition(m.clone()),
            Self::CircuitOpen => tonic::Status::unavailable("payment provider circuit open"),
            Self::ServiceUnavailable(m) => tonic::Status::unavailable(m.clone()),
            Self::LedgerInvariant(m) => tonic::Status::internal(m.clone()),
            Self::Database(_) => tonic::Status::internal("database error"),
            Self::Internal(m) => tonic::Status::internal(m.clone()),
        }
    }
}

impl IntoResponse for PaymentError {
    fn into_response(self) -> Response {
        let status = self.http_status();
        let code = self.error_code();
        // Never leak raw database internals to external clients.
        let message = match &self {
            Self::Database(_) => "internal database error".to_string(),
            other => other.to_string(),
        };
        let body = Json(json!({
            "error": code,
            "message": message,
        }));
        (status, body).into_response()
    }
}

pub type Result<T> = std::result::Result<T, PaymentError>;

#[cfg(test)]
mod tests {
    use super::*;
    use http_body_util::BodyExt;

    #[tokio::test]
    async fn test_error_http_status_codes_tonic_mapping_and_db_redaction() {
        let cases: Vec<(PaymentError, StatusCode, &str, tonic::Code)> = vec![
            (
                PaymentError::Validation("bad field".into()),
                StatusCode::BAD_REQUEST,
                "VALIDATION_ERROR",
                tonic::Code::InvalidArgument,
            ),
            (
                PaymentError::NotFound("missing".into()),
                StatusCode::NOT_FOUND,
                "NOT_FOUND",
                tonic::Code::NotFound,
            ),
            (
                PaymentError::Conflict("dup".into()),
                StatusCode::CONFLICT,
                "CONFLICT",
                tonic::Code::AlreadyExists,
            ),
            (
                PaymentError::FailedPrecondition("version mismatch".into()),
                StatusCode::CONFLICT,
                "FAILED_PRECONDITION",
                tonic::Code::FailedPrecondition,
            ),
            (
                PaymentError::ProviderUnsupported("payos list unsupported".into()),
                StatusCode::CONFLICT,
                "PROVIDER_UNSUPPORTED",
                tonic::Code::FailedPrecondition,
            ),
            (
                PaymentError::Unauthorized("bad mac".into()),
                StatusCode::UNAUTHORIZED,
                "UNAUTHORIZED",
                tonic::Code::Unauthenticated,
            ),
            (
                PaymentError::Forbidden("admin only".into()),
                StatusCode::FORBIDDEN,
                "FORBIDDEN",
                tonic::Code::PermissionDenied,
            ),
            (
                PaymentError::LedgerInvariant("unbalanced".into()),
                StatusCode::INTERNAL_SERVER_ERROR,
                "LEDGER_INVARIANT_ERROR",
                tonic::Code::Internal,
            ),
            (
                PaymentError::InsufficientBalance {
                    available_vnd: 100,
                    requested_vnd: 200,
                },
                StatusCode::CONFLICT,
                "INSUFFICIENT_BALANCE",
                tonic::Code::FailedPrecondition,
            ),
            (
                PaymentError::PayoutPendingExists("one pending per shop".into()),
                StatusCode::CONFLICT,
                "PAYOUT_PENDING_EXISTS",
                tonic::Code::FailedPrecondition,
            ),
            (
                PaymentError::BankAccountNotReady("no verified account".into()),
                StatusCode::PRECONDITION_FAILED,
                "BANK_ACCOUNT_NOT_READY",
                tonic::Code::FailedPrecondition,
            ),
            (
                PaymentError::RefundExceedsPaid {
                    remaining_vnd: 50,
                    requested_vnd: 100,
                },
                StatusCode::CONFLICT,
                "REFUND_EXCEEDS_PAID",
                tonic::Code::FailedPrecondition,
            ),
            (
                PaymentError::Provider("upstream 503".into()),
                StatusCode::BAD_GATEWAY,
                "PROVIDER_ERROR",
                tonic::Code::Unavailable,
            ),
            (
                PaymentError::CircuitOpen,
                StatusCode::BAD_GATEWAY,
                "PROVIDER_CIRCUIT_OPEN",
                tonic::Code::Unavailable,
            ),
            (
                PaymentError::ServiceUnavailable("webhook disabled".into()),
                StatusCode::SERVICE_UNAVAILABLE,
                "SERVICE_UNAVAILABLE",
                tonic::Code::Unavailable,
            ),
            (
                PaymentError::Database(sqlx::Error::RowNotFound),
                StatusCode::INTERNAL_SERVER_ERROR,
                "DATABASE_ERROR",
                tonic::Code::Internal,
            ),
            (
                PaymentError::Internal("boom".into()),
                StatusCode::INTERNAL_SERVER_ERROR,
                "INTERNAL_ERROR",
                tonic::Code::Internal,
            ),
        ];

        for (err, expected_http, expected_code, expected_tonic) in cases {
            assert_eq!(err.http_status(), expected_http);
            assert_eq!(err.error_code(), expected_code);
            assert_eq!(err.to_tonic_status().code(), expected_tonic);

            let is_db = matches!(err, PaymentError::Database(_));
            let resp = err.into_response();
            assert_eq!(resp.status(), expected_http);
            let bytes = resp.into_body().collect().await.unwrap().to_bytes();
            let body: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
            assert_eq!(body["error"], expected_code);
            if is_db {
                assert_eq!(body["message"], "internal database error");
            }
        }
    }
}
