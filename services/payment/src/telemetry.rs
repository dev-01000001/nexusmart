use std::collections::HashMap;
use std::time::Duration;

use opentelemetry::global;
use opentelemetry::propagation::{Extractor, Injector, TextMapPropagator};
use opentelemetry::trace::{TraceContextExt, TracerProvider as _};
use opentelemetry::Context as OtelContext;
use opentelemetry_otlp::WithTonicConfig;
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::trace::TracerProvider;
use opentelemetry_sdk::Resource;
use tracing_opentelemetry::OpenTelemetrySpanExt;
use tracing_subscriber::layer::SubscriberExt;
use tracing_subscriber::util::SubscriberInitExt;
use tracing_subscriber::EnvFilter;
use uuid::Uuid;

/// Normalizes an OTLP gRPC endpoint for Tonic (`http://` or `https://` scheme required).
pub fn normalize_otlp_endpoint(raw: &str) -> Result<String, String> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Err("OTLP endpoint is empty".to_string());
    }
    let normalized = if trimmed.contains("://") {
        trimmed.to_string()
    } else {
        format!("http://{trimmed}")
    };
    let uri: tonic::transport::Uri = normalized
        .parse()
        .map_err(|_| "invalid OTLP endpoint URI".to_string())?;
    match uri.scheme_str() {
        Some("http") | Some("https") => {}
        other => {
            return Err(format!(
                "unsupported OTLP endpoint scheme: {}",
                other.unwrap_or("<none>")
            ));
        }
    }
    if uri.host().is_none() {
        return Err("OTLP endpoint URI missing host".to_string());
    }
    Ok(normalized)
}

/// Builds the Tonic OTLP span exporter using an explicit normalized `tonic::transport::Channel`
/// via `WithTonicConfig::with_channel`. This avoids `opentelemetry-otlp 0.27`'s `resolve_endpoint`
/// preferring a schemeless `OTEL_EXPORTER_OTLP_ENDPOINT` (used by Go services) over `with_endpoint`,
/// which otherwise fails every batch flush with `tonic::transport::Error(InvalidUri)`.
fn build_otlp_span_exporter(
    raw_endpoint: &str,
) -> Result<opentelemetry_otlp::SpanExporter, String> {
    let normalized = normalize_otlp_endpoint(raw_endpoint)?;
    let timeout_secs = std::env::var(opentelemetry_otlp::OTEL_EXPORTER_OTLP_TRACES_TIMEOUT)
        .ok()
        .or_else(|| std::env::var(opentelemetry_otlp::OTEL_EXPORTER_OTLP_TIMEOUT).ok())
        .and_then(|v| v.trim().parse::<u64>().ok())
        .filter(|&s| s > 0)
        .unwrap_or(10);
    let channel = tonic::transport::Channel::from_shared(normalized)
        .map_err(|_| "failed to construct OTLP tonic endpoint".to_string())?
        .timeout(Duration::from_secs(timeout_secs))
        .connect_lazy();
    opentelemetry_otlp::SpanExporter::builder()
        .with_tonic()
        .with_channel(channel)
        .build()
        .map_err(|e| format!("failed to build OTLP span exporter: {e}"))
}

/// Initializes JSON structured logging and OpenTelemetry tracing.
/// If OTLP endpoint is configured, attempts to wire OTLP gRPC exporter with graceful fallback
/// to a local TracerProvider so W3C parent-child spans remain active even without a collector.
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

    let resource = Resource::new(vec![opentelemetry::KeyValue::new(
        "service.name",
        service_name.to_string(),
    )]);

    let (exporter, exporter_err) = match otel_endpoint {
        Some(endpoint) => match build_otlp_span_exporter(endpoint) {
            Ok(exp) => (Some(exp), None),
            Err(err) => (None, Some(err)),
        },
        None => (None, None),
    };

    let mut builder = TracerProvider::builder().with_resource(resource);
    if let Some(exp) = exporter {
        builder = builder.with_batch_exporter(exp, opentelemetry_sdk::runtime::Tokio);
    }
    let tp = builder.build();
    global::set_tracer_provider(tp.clone());

    let tracer = tp.tracer(service_name.to_string());
    let otel_layer = tracing_opentelemetry::layer().with_tracer(tracer);
    let _ = tracing_subscriber::registry()
        .with(env_filter)
        .with(fmt_layer)
        .with(otel_layer)
        .try_init();

    if let Some(err) = exporter_err {
        tracing::warn!(
            service = service_name,
            error = %err,
            "failed to build OTLP span exporter; continuing with local tracer provider"
        );
    }

    Some(tp)
}

struct MetadataExtractor<'a>(&'a tonic::metadata::MetadataMap);

impl Extractor for MetadataExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.0.get(key).and_then(|v| v.to_str().ok())
    }

    fn keys(&self) -> Vec<&str> {
        self.0
            .keys()
            .filter_map(|k| match k {
                tonic::metadata::KeyRef::Ascii(a) => Some(a.as_str()),
                tonic::metadata::KeyRef::Binary(_) => None,
            })
            .collect()
    }
}

struct HeaderMapExtractor<'a>(&'a axum::http::HeaderMap);

impl Extractor for HeaderMapExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.0.get(key).and_then(|v| v.to_str().ok())
    }

    fn keys(&self) -> Vec<&str> {
        self.0.keys().map(|k| k.as_str()).collect()
    }
}

struct NatsHeaderExtractor<'a>(&'a async_nats::HeaderMap);

impl Extractor for NatsHeaderExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        if let Some(v) = self.0.get(key) {
            return Some(v.as_str());
        }
        self.0
            .iter()
            .find(|(k, _)| AsRef::<str>::as_ref(*k).eq_ignore_ascii_case(key))
            .and_then(|(_, vals)| vals.first().map(|v| v.as_str()))
    }

    fn keys(&self) -> Vec<&str> {
        self.0
            .iter()
            .map(|(k, _)| AsRef::<str>::as_ref(k))
            .collect()
    }
}

struct NatsHeaderInjector<'a>(&'a mut async_nats::HeaderMap);

impl Injector for NatsHeaderInjector<'_> {
    fn set(&mut self, key: &str, value: String) {
        self.0.insert(key, value.as_str());
    }
}

/// Extracts W3C `traceparent`/`tracestate` from incoming Tonic gRPC metadata.
pub fn extract_context_from_grpc(metadata: &tonic::metadata::MetadataMap) -> OtelContext {
    TraceContextPropagator::new().extract(&MetadataExtractor(metadata))
}

/// Extracts W3C `traceparent`/`tracestate` from incoming Axum HTTP headers.
pub fn extract_context_from_http(headers: &axum::http::HeaderMap) -> OtelContext {
    TraceContextPropagator::new().extract(&HeaderMapExtractor(headers))
}

/// Builds a remote OTel parent context from a bare 32-hex trace ID (e.g. outbox or envelope fallback).
pub fn extract_context_from_trace_id(trace_id: &str) -> OtelContext {
    let trimmed = trace_id.trim();
    if trimmed.len() == 32 && trimmed.chars().all(|c| c.is_ascii_hexdigit()) {
        let mut carrier = HashMap::with_capacity(1);
        carrier.insert("traceparent".to_string(), format_traceparent(trimmed));
        return TraceContextPropagator::new().extract(&carrier);
    }
    OtelContext::new()
}

/// Extracts W3C `traceparent`/`tracestate` from incoming NATS message headers, falling back
/// to a bare 32-hex `fallback_trace_id` from the event envelope when headers are absent.
pub fn extract_context_from_nats(
    headers: Option<&async_nats::HeaderMap>,
    fallback_trace_id: Option<&str>,
) -> OtelContext {
    if let Some(hdrs) = headers {
        let cx = TraceContextPropagator::new().extract(&NatsHeaderExtractor(hdrs));
        if cx.span().span_context().is_valid() {
            return cx;
        }
    }
    if let Some(tid) = fallback_trace_id {
        return extract_context_from_trace_id(tid);
    }
    OtelContext::new()
}

/// Attaches an extracted W3C OTel parent context to `span` via `OpenTelemetrySpanExt::set_parent`
/// and returns the resolved trace ID string for structured JSON logging and response headers.
pub fn attach_parent_context(
    span: &tracing::Span,
    parent_cx: OtelContext,
    x_trace_id: Option<&str>,
) -> String {
    let parent_tid = {
        let span_ref = parent_cx.span();
        let sc = span_ref.span_context();
        if sc.is_valid() {
            Some(sc.trace_id().to_string())
        } else {
            None
        }
    };

    if parent_tid.is_some() {
        span.set_parent(parent_cx);
    }

    let active_sc = span.context().span().span_context().clone();
    if active_sc.is_valid() {
        return active_sc.trace_id().to_string();
    }
    if let Some(tid) = parent_tid {
        return tid;
    }
    extract_or_generate_trace_id(None, x_trace_id)
}

/// Injects the current active OTel span context into outgoing NATS headers (`traceparent` + `x-trace-id`),
/// falling back to `fallback_trace_id` if no OTel subscriber is active.
pub fn inject_current_span_to_nats(headers: &mut async_nats::HeaderMap, fallback_trace_id: &str) {
    let cx = tracing::Span::current().context();
    if cx.span().span_context().is_valid() {
        TraceContextPropagator::new().inject_context(&cx, &mut NatsHeaderInjector(headers));
        let tid = cx.span().span_context().trace_id().to_string();
        headers.insert("x-trace-id", tid.as_str());
        return;
    }
    if !fallback_trace_id.is_empty() {
        let tp = format_traceparent(fallback_trace_id);
        headers.insert("traceparent", tp.as_str());
        headers.insert("x-trace-id", fallback_trace_id);
    }
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

/// Installs a process-wide no-op `Registry` subscriber once per test binary so `tracing-core`'s
/// `Rebuilder::JustOne` fast path never caches `Interest::never()` on callsites touched by
/// parallel tests running without a local subscriber.
#[cfg(test)]
pub(crate) fn init_test_tracing() {
    static ONCE: std::sync::Once = std::sync::Once::new();
    ONCE.call_once(|| {
        let _ = tracing::subscriber::set_global_default(tracing_subscriber::registry());
    });
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
    fn test_w3c_context_sets_parent_span_across_grpc_http_and_nats() {
        init_test_tracing();

        use std::sync::{Arc, Mutex};

        use opentelemetry::trace::TraceContextExt;
        use opentelemetry_sdk::export::trace::SpanData;
        use opentelemetry_sdk::trace::SpanProcessor;
        use tracing_opentelemetry::OpenTelemetrySpanExt;

        #[derive(Debug, Default, Clone)]
        struct CaptureProcessor {
            spans: Arc<Mutex<Vec<SpanData>>>,
        }

        impl SpanProcessor for CaptureProcessor {
            fn on_start(
                &self,
                _span: &mut opentelemetry_sdk::trace::Span,
                _cx: &opentelemetry::Context,
            ) {
            }
            fn on_end(&self, span: SpanData) {
                self.spans.lock().unwrap().push(span);
            }
            fn force_flush(&self) -> opentelemetry::trace::TraceResult<()> {
                Ok(())
            }
            fn shutdown(&self) -> opentelemetry::trace::TraceResult<()> {
                Ok(())
            }
        }

        let processor = CaptureProcessor::default();
        let captured = processor.spans.clone();
        let tp = TracerProvider::builder()
            .with_span_processor(processor)
            .build();
        let tracer = tp.tracer("payment-unit-test");
        let subscriber =
            tracing_subscriber::registry().with(tracing_opentelemetry::layer().with_tracer(tracer));

        const TRACE_ID: &str = "4bf92f3577b34da6a3ce929d0e0e4736";
        const PARENT_SPAN_ID: &str = "00f067aa0ba902b7";
        let traceparent = format!("00-{TRACE_ID}-{PARENT_SPAN_ID}-01");

        tracing::subscriber::with_default(subscriber, || {
            // 1. gRPC metadata extraction -> child span
            let mut grpc_md = tonic::metadata::MetadataMap::new();
            grpc_md.insert("traceparent", traceparent.parse().unwrap());
            let grpc_cx = extract_context_from_grpc(&grpc_md);
            let grpc_span = tracing::info_span!("grpc.test");
            let grpc_tid = attach_parent_context(&grpc_span, grpc_cx, None);
            assert_eq!(grpc_tid, TRACE_ID);
            {
                let _guard = grpc_span.enter();
                let sc = tracing::Span::current()
                    .context()
                    .span()
                    .span_context()
                    .clone();
                assert_eq!(sc.trace_id().to_string(), TRACE_ID);
                assert_ne!(sc.span_id().to_string(), PARENT_SPAN_ID);
            }
            drop(grpc_span);

            // 2. HTTP headers extraction -> child span
            let mut http_hdrs = axum::http::HeaderMap::new();
            http_hdrs.insert("traceparent", traceparent.parse().unwrap());
            let http_cx = extract_context_from_http(&http_hdrs);
            let http_span = tracing::info_span!("http.test");
            let http_tid = attach_parent_context(&http_span, http_cx, None);
            assert_eq!(http_tid, TRACE_ID);
            drop(http_span);

            // 3. NATS headers extraction -> child span + inject back to NATS headers
            let mut nats_hdrs = async_nats::HeaderMap::new();
            nats_hdrs.insert("traceparent", traceparent.as_str());
            let nats_cx = extract_context_from_nats(Some(&nats_hdrs), None);
            let nats_span = tracing::info_span!("nats.test");
            let nats_tid = attach_parent_context(&nats_span, nats_cx, None);
            assert_eq!(nats_tid, TRACE_ID);
            {
                let _guard = nats_span.enter();
                let mut out_hdrs = async_nats::HeaderMap::new();
                inject_current_span_to_nats(&mut out_hdrs, &nats_tid);
                let injected_tp = out_hdrs.get("traceparent").unwrap().as_str();
                assert!(injected_tp.starts_with(&format!("00-{TRACE_ID}-")));
                assert!(!injected_tp.contains(PARENT_SPAN_ID));
            }
            drop(nats_span);
        });

        let spans = captured.lock().unwrap();
        assert_eq!(
            spans.len(),
            3,
            "expected 3 exported spans (grpc, http, nats)"
        );
        for sd in spans.iter() {
            assert_eq!(sd.span_context.trace_id().to_string(), TRACE_ID);
            assert_eq!(sd.parent_span_id.to_string(), PARENT_SPAN_ID);
            assert_ne!(sd.span_context.span_id().to_string(), PARENT_SPAN_ID);
        }
    }

    #[tokio::test]
    async fn test_otlp_endpoint_normalization_and_schemeless_env_override() {
        use opentelemetry_sdk::export::trace::SpanExporter as _;

        // 1. Normalization cases: with scheme, without scheme, whitespace, invalid inputs
        assert_eq!(
            normalize_otlp_endpoint("otel-collector:4317").unwrap(),
            "http://otel-collector:4317"
        );
        assert_eq!(
            normalize_otlp_endpoint("  127.0.0.1:4317  ").unwrap(),
            "http://127.0.0.1:4317"
        );
        assert_eq!(
            normalize_otlp_endpoint("http://otel-collector:4317").unwrap(),
            "http://otel-collector:4317"
        );
        assert_eq!(
            normalize_otlp_endpoint("https://otel.example.com:4317").unwrap(),
            "https://otel.example.com:4317"
        );
        assert!(normalize_otlp_endpoint("").is_err());
        assert!(normalize_otlp_endpoint("   ").is_err());
        assert!(normalize_otlp_endpoint("ftp://otel-collector:4317").is_err());
        assert!(normalize_otlp_endpoint("http://").is_err());
        assert!(normalize_otlp_endpoint("http://bad host:4317").is_err());

        // 2. Regression test: when OTEL_EXPORTER_OTLP_ENDPOINT is set WITHOUT scheme (as in
        //    docker-compose.yml for Go services), the exporter MUST still dial the normalized
        //    http:// target and NEVER fail with tonic InvalidUri.
        struct EnvGuard(&'static str, Option<String>);
        impl Drop for EnvGuard {
            fn drop(&mut self) {
                match &self.1 {
                    Some(v) => std::env::set_var(self.0, v),
                    None => std::env::remove_var(self.0),
                }
            }
        }

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let schemeless = format!("127.0.0.1:{}", addr.port());

        let prev = std::env::var(opentelemetry_otlp::OTEL_EXPORTER_OTLP_ENDPOINT).ok();
        let _guard = EnvGuard(opentelemetry_otlp::OTEL_EXPORTER_OTLP_ENDPOINT, prev);
        std::env::set_var(opentelemetry_otlp::OTEL_EXPORTER_OTLP_ENDPOINT, &schemeless);

        let mut exporter = build_otlp_span_exporter(&schemeless).unwrap();
        let accept_task = tokio::spawn(async move {
            match tokio::time::timeout(Duration::from_secs(2), listener.accept()).await {
                Ok(Ok((sock, _))) => {
                    drop(sock);
                    true
                }
                _ => false,
            }
        });

        let export_res = exporter.export(vec![]).await;
        let accepted = accept_task.await.unwrap();
        let err_str = match &export_res {
            Ok(()) => String::new(),
            Err(e) => format!("{e:?}"),
        };
        assert!(
            !err_str.contains("InvalidUri"),
            "exporter hit InvalidUri from schemeless OTEL_EXPORTER_OTLP_ENDPOINT: {err_str}"
        );
        assert!(
            accepted,
            "exporter did not connect to normalized http://{schemeless} (export_res={export_res:?})"
        );
    }
}
