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
            | Self::InsufficientBalance { .. }
            | Self::RefundExceedsPaid { .. }
            | Self::ProviderUnsupported(_) => StatusCode::CONFLICT,
            Self::Unauthorized(_) => StatusCode::UNAUTHORIZED,
            Self::Forbidden(_) => StatusCode::FORBIDDEN,
            Self::Provider(_) | Self::CircuitOpen => StatusCode::BAD_GATEWAY,
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
            Self::Unauthorized(_) => "UNAUTHORIZED",
            Self::Forbidden(_) => "FORBIDDEN",
            Self::LedgerInvariant(_) => "LEDGER_INVARIANT_ERROR",
            Self::InsufficientBalance { .. } => "INSUFFICIENT_BALANCE",
            Self::RefundExceedsPaid { .. } => "REFUND_EXCEEDS_PAID",
            Self::Provider(_) => "PROVIDER_ERROR",
            Self::ProviderUnsupported(_) => "PROVIDER_UNSUPPORTED",
            Self::CircuitOpen => "PROVIDER_CIRCUIT_OPEN",
            Self::Database(_) => "DATABASE_ERROR",
            Self::Internal(_) => "INTERNAL_ERROR",
        }
    }

    pub fn to_tonic_status(&self) -> tonic::Status {
        match self {
            Self::Validation(m) => tonic::Status::invalid_argument(m.clone()),
            Self::NotFound(m) => tonic::Status::not_found(m.clone()),
            Self::Conflict(m) => tonic::Status::already_exists(m.clone()),
            Self::Unauthorized(m) => tonic::Status::unauthenticated(m.clone()),
            Self::Forbidden(m) => tonic::Status::permission_denied(m.clone()),
            Self::InsufficientBalance { .. } | Self::RefundExceedsPaid { .. } => {
                tonic::Status::failed_precondition(self.to_string())
            }
            Self::Provider(m) => tonic::Status::unavailable(m.clone()),
            Self::ProviderUnsupported(m) => tonic::Status::failed_precondition(m.clone()),
            Self::CircuitOpen => tonic::Status::unavailable("payment provider circuit open"),
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
