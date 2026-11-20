use std::collections::HashMap;

use opentelemetry::global;
use opentelemetry::trace::TracerProvider as _;
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::trace::TracerProvider;
use opentelemetry_sdk::Resource;
use tracing_subscriber::layer::SubscriberExt;
use tracing_subscriber::util::SubscriberInitExt;
use tracing_subscriber::EnvFilter;
use uuid::Uuid;

/// Initializes JSON structured logging and OpenTelemetry tracing.
/// If OTLP endpoint is configured, attempts to wire OTLP gRPC exporter with graceful fallback.
pub fn init_telemetry(service_name: &str, otel_endpoint: Option<&str>) -> Option<TracerProvider> {
    global::set_text_map_propagator(TraceContextPropagator::new());

    let env_filter = EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| EnvFilter::new("info,nexusmart_payment=debug,sqlx=warn"));

    let fmt_layer = tracing_subscriber::fmt::layer()
        .json()
        .with_current_span(true)
        .with_span_list(false)
        .with_target(true)
        .flatten_event(true);

    let provider = otel_endpoint.and_then(|endpoint| {
        let normalized = if endpoint.starts_with("http://") || endpoint.starts_with("https://") {
            endpoint.to_string()
        } else {
            format!("http://{endpoint}")
        };
        match opentelemetry_otlp::SpanExporter::builder()
            .with_tonic()
            .with_endpoint(normalized)
            .build()
        {
            Ok(exporter) => {
                let resource = Resource::new(vec![opentelemetry::KeyValue::new(
                    "service.name",
                    service_name.to_string(),
                )]);
                let tp = TracerProvider::builder()
                    .with_batch_exporter(exporter, opentelemetry_sdk::runtime::Tokio)
                    .with_resource(resource)
                    .build();
                global::set_tracer_provider(tp.clone());
                Some(tp)
            }
            Err(_) => None,
        }
    });

    if let Some(ref tp) = provider {
        let tracer = tp.tracer(service_name.to_string());
        let otel_layer = tracing_opentelemetry::layer().with_tracer(tracer);
        let _ = tracing_subscriber::registry()
            .with(env_filter)
            .with(fmt_layer)
            .with(otel_layer)
            .try_init();
    } else {
        let _ = tracing_subscriber::registry()
            .with(env_filter)
            .with(fmt_layer)
            .try_init();
    }

    provider
}

/// Extracts a trace ID from W3C `traceparent` or `x-trace-id` header, generating a new 32-hex ID if absent.
pub fn extract_or_generate_trace_id(traceparent: Option<&str>, x_trace_id: Option<&str>) -> String {
    if let Some(tp) = traceparent {
        // W3C traceparent: 00-<32-hex-trace-id>-<16-hex-parent-id>-<2-hex-flags>
        let parts: Vec<&str> = tp.trim().split('-').collect();
        if parts.len() == 4
            && parts[1].len() == 32
            && parts[1].chars().all(|c| c.is_ascii_hexdigit())
        {
            return parts[1].to_ascii_lowercase();
        }
    }
    if let Some(tid) = x_trace_id {
        let trimmed = tid.trim();
        if !trimmed.is_empty() && trimmed.len() <= 64 {
            return trimmed.to_string();
        }
    }
    Uuid::new_v4().simple().to_string()
}

/// Builds a W3C `traceparent` header value from a trace ID.
pub fn format_traceparent(trace_id: &str) -> String {
    let normalized = if trace_id.len() == 32 && trace_id.chars().all(|c| c.is_ascii_hexdigit()) {
        trace_id.to_ascii_lowercase()
    } else {
        Uuid::new_v4().simple().to_string()
    };
    let span_id = &Uuid::new_v4().simple().to_string()[..16];
    format!("00-{normalized}-{span_id}-01")
}

/// Masks secrets/signatures/tokens before logging. Never logs full secrets.
/// UTF-8 safe (uses Unicode scalar values rather than raw byte offsets).
pub fn mask_secret(value: &str) -> String {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return "[EMPTY]".to_string();
    }
    let char_count = trimmed.chars().count();
    if char_count <= 8 {
        return "[REDACTED]".to_string();
    }
    let prefix: String = trimmed.chars().take(4).collect();
    format!("{prefix}***[REDACTED]")
}

/// Masks phone number (PII) keeping only last 3 digits.
pub fn mask_phone(phone: &str) -> String {
    let digits: String = phone.chars().filter(|c| c.is_ascii_digit()).collect();
    if digits.len() <= 3 {
        return "***".to_string();
    }
    format!("***{}", &digits[digits.len() - 3..])
}

/// Masks email (PII) keeping first character and domain.
pub fn mask_email(email: &str) -> String {
    if let Some((local, domain)) = email.split_once('@') {
        let first = local.chars().next().unwrap_or('*');
        format!("{first}***@{domain}")
    } else {
        "[REDACTED]".to_string()
    }
}

/// Masks bank account number keeping last 4 characters (UTF-8 safe).
pub fn mask_bank_account(account: &str) -> String {
    let trimmed = account.trim();
    let chars: Vec<char> = trimmed.chars().collect();
    if chars.len() <= 4 {
        return "****".to_string();
    }
    let suffix: String = chars[chars.len() - 4..].iter().collect();
    format!("****{suffix}")
}

/// Sanitizes HTTP header map for safe logging by redacting sensitive header values.
pub fn sanitize_headers(headers: &HashMap<String, String>) -> HashMap<String, String> {
    const SENSITIVE_KEYS: &[&str] = &[
        "authorization",
        "x-signature",
        "x-api-key",
        "x-client-id",
        "cookie",
        "set-cookie",
        "x-webhook-secret",
        "x-auth-token",
    ];
    headers
        .iter()
        .map(|(k, v)| {
            let lower = k.to_ascii_lowercase();
            let redacted = if SENSITIVE_KEYS.iter().any(|s| lower.contains(s)) {
                mask_secret(v)
            } else {
                v.clone()
            };
            (k.clone(), redacted)
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_trace_id_from_w3c_traceparent() {
        let tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";
        assert_eq!(
            extract_or_generate_trace_id(Some(tp), None),
            "4bf92f3577b34da6a3ce929d0e0e4736"
        );
    }

    #[test]
    fn test_masking_pii_and_secrets() {
        assert_eq!(mask_secret("supersecretvalue123"), "supe***[REDACTED]");
        assert_eq!(mask_secret("short"), "[REDACTED]");
        assert_eq!(mask_secret("MậtKhẩuSiêuBíMật"), "MậtK***[REDACTED]");
        assert_eq!(mask_phone("0901234567"), "***567");
        assert_eq!(mask_email("buyer@nexusmart.vn"), "b***@nexusmart.vn");
        assert_eq!(mask_bank_account("19034567890123"), "****0123");
        assert_eq!(mask_bank_account("NgânHàngViệt"), "****Việt");

        let mut hdrs = HashMap::new();
        hdrs.insert("Authorization".into(), "Bearer secret-jwt-token".into());
        hdrs.insert("X-Signature".into(), "abcdef1234567890".into());
        hdrs.insert("Content-Type".into(), "application/json".into());
        let safe = sanitize_headers(&hdrs);
        assert!(safe["Authorization"].contains("[REDACTED]"));
        assert!(safe["X-Signature"].contains("[REDACTED]"));
        assert_eq!(safe["Content-Type"], "application/json");
    }
}
