use std::collections::HashMap;

use async_nats::HeaderMap as NatsHeaders;
use axum::http::HeaderMap as HttpHeaders;
use opentelemetry::propagation::{Extractor, Injector};
use opentelemetry::trace::{TraceContextExt, TracerProvider as _};
use opentelemetry::{global, Context};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::trace::TracerProvider;
use opentelemetry_sdk::Resource;
use tonic::metadata::MetadataMap;
use tracing_opentelemetry::OpenTelemetrySpanExt;
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

struct HttpHeaderExtractor<'a>(&'a HttpHeaders);

impl Extractor for HttpHeaderExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.0.get(key).and_then(|v| v.to_str().ok())
    }

    fn keys(&self) -> Vec<&str> {
        self.0.keys().map(|k| k.as_str()).collect()
    }
}

struct GrpcMetadataExtractor<'a>(&'a MetadataMap);

impl Extractor for GrpcMetadataExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.0.get(key).and_then(|v| v.to_str().ok())
    }

    fn keys(&self) -> Vec<&str> {
        self.0
            .keys()
            .filter_map(|k| match k {
                tonic::metadata::KeyRef::Ascii(name) => Some(name.as_str()),
                tonic::metadata::KeyRef::Binary(_) => None,
            })
            .collect()
    }
}

struct NatsHeaderExtractor<'a>(&'a NatsHeaders);

impl Extractor for NatsHeaderExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.0
            .get(key)
            .or_else(|| {
                let lower = key.to_ascii_lowercase();
                self.0.get(lower.as_str())
            })
            .map(|v| v.as_str())
    }

    fn keys(&self) -> Vec<&str> {
        self.0.iter().map(|(k, _)| k.as_ref()).collect()
    }
}

struct NatsHeaderInjector<'a>(&'a mut NatsHeaders);

impl Injector for NatsHeaderInjector<'_> {
    fn set(&mut self, key: &str, value: String) {
        self.0.insert(key, value.as_str());
    }
}

/// Extracts a W3C OpenTelemetry parent context from incoming Axum HTTP headers.
pub fn extract_http_context(headers: &HttpHeaders) -> Context {
    global::get_text_map_propagator(|p| p.extract(&HttpHeaderExtractor(headers)))
}

/// Extracts a W3C OpenTelemetry parent context from incoming Tonic gRPC metadata.
pub fn extract_grpc_context(metadata: &MetadataMap) -> Context {
    global::get_text_map_propagator(|p| p.extract(&GrpcMetadataExtractor(metadata)))
}

/// Extracts a W3C OpenTelemetry parent context from incoming NATS message headers.
pub fn extract_nats_context(headers: Option<&NatsHeaders>) -> Context {
    match headers {
        Some(h) => global::get_text_map_propagator(|p| p.extract(&NatsHeaderExtractor(h))),
        None => Context::new(),
    }
}

/// Injects the current tracing/OTel span context into NATS headers; falls back to
/// synthesizing a W3C `traceparent` from `fallback_trace_id` when no active OTel span exists.
pub fn inject_nats_context(headers: &mut NatsHeaders, fallback_trace_id: &str) {
    let cx = tracing::Span::current().context();
    if cx.span().span_context().is_valid() {
        global::get_text_map_propagator(|p| {
            p.inject_context(&cx, &mut NatsHeaderInjector(headers))
        });
    }
    if headers.get("traceparent").is_none() && !fallback_trace_id.trim().is_empty() {
        let tp = format_traceparent(fallback_trace_id);
        headers.insert("traceparent", tp.as_str());
    }
}

/// Returns the active OpenTelemetry trace ID (32 lowercase hex) if a valid span is current,
/// otherwise returns an empty string.
pub fn current_trace_id() -> String {
    let cx = tracing::Span::current().context();
    let sc = cx.span().span_context().clone();
    if sc.is_valid() {
        sc.trace_id().to_string()
    } else {
        String::new()
    }
}

/// Redacts embedded credentials (`scheme://user:pass@host`) from URLs such as `NATS_URL`.
pub fn redact_url_credentials(raw: &str) -> String {
    let trimmed = raw.trim();
    if let Some(scheme_pos) = trimmed.find("://") {
        let after_scheme = &trimmed[scheme_pos + 3..];
        if let Some(at_pos) = after_scheme.find('@') {
            let host_and_rest = &after_scheme[at_pos + 1..];
            return format!("{}://[REDACTED]@{host_and_rest}", &trimmed[..scheme_pos]);
        }
    }
    trimmed.to_string()
}

/// Masks secrets/signatures/tokens before logging. Never logs any characters of a non-empty secret.
pub fn mask_secret(value: &str) -> String {
    if value.trim().is_empty() {
        return "[EMPTY]".to_string();
    }
    "[REDACTED]".to_string()
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
        assert_eq!(mask_secret(""), "[EMPTY]");
        assert_eq!(mask_secret("supersecretvalue123"), "[REDACTED]");
        assert_eq!(mask_secret("short"), "[REDACTED]");
        assert_eq!(mask_secret("MậtKhẩuSiêuBíMật"), "[REDACTED]");
        assert_eq!(
            redact_url_credentials("nats://svc:SecretPass@nats:4222"),
            "nats://[REDACTED]@nats:4222"
        );
        assert_eq!(
            redact_url_credentials("nats://nats:4222"),
            "nats://nats:4222"
        );
        assert_eq!(mask_phone("0901234567"), "***567");
        assert_eq!(mask_email("buyer@nexusmart.vn"), "b***@nexusmart.vn");
        assert_eq!(mask_bank_account("19034567890123"), "****0123");
        assert_eq!(mask_bank_account("NgânHàngViệt"), "****Việt");

        let mut hdrs = HashMap::new();
        hdrs.insert("Authorization".into(), "Bearer secret-jwt-token".into());
        hdrs.insert("X-Signature".into(), "abcdef1234567890".into());
        hdrs.insert("Content-Type".into(), "application/json".into());
        let safe = sanitize_headers(&hdrs);
        assert_eq!(safe["Authorization"], "[REDACTED]");
        assert_eq!(safe["X-Signature"], "[REDACTED]");
        assert_eq!(safe["Content-Type"], "application/json");
    }

    #[test]
    fn test_otel_extract_and_nats_inject_context() {
        global::set_text_map_propagator(TraceContextPropagator::new());
        let tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";

        let mut http_hdrs = HttpHeaders::new();
        http_hdrs.insert("traceparent", tp.parse().unwrap());
        let http_cx = extract_http_context(&http_hdrs);
        assert_eq!(
            http_cx.span().span_context().trace_id().to_string(),
            "4bf92f3577b34da6a3ce929d0e0e4736"
        );

        let mut grpc_md = MetadataMap::new();
        grpc_md.insert("traceparent", tp.parse().unwrap());
        let grpc_cx = extract_grpc_context(&grpc_md);
        assert_eq!(
            grpc_cx.span().span_context().trace_id().to_string(),
            "4bf92f3577b34da6a3ce929d0e0e4736"
        );

        let mut nats_hdrs = NatsHeaders::new();
        nats_hdrs.insert("traceparent", tp);
        let nats_cx = extract_nats_context(Some(&nats_hdrs));
        assert_eq!(
            nats_cx.span().span_context().trace_id().to_string(),
            "4bf92f3577b34da6a3ce929d0e0e4736"
        );

        let mut out_hdrs = NatsHeaders::new();
        inject_nats_context(&mut out_hdrs, "4bf92f3577b34da6a3ce929d0e0e4736");
        assert!(out_hdrs
            .get("traceparent")
            .unwrap()
            .as_str()
            .starts_with("00-4bf92f3577b34da6a3ce929d0e0e4736-"));
    }
}
