use opentelemetry::global;
use opentelemetry::propagation::Extractor;
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
/// If OTLP endpoint is configured, wires OTLP gRPC exporter with graceful fallback.
pub fn init_telemetry(service_name: &str, otel_endpoint: Option<&str>) -> Option<TracerProvider> {
    global::set_text_map_propagator(TraceContextPropagator::new());

    let env_filter = EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| EnvFilter::new("info,nexusmart_aiops=info"));

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

/// Extracts W3C OpenTelemetry parent context from HTTP headers.
pub fn extract_otel_context(headers: &axum::http::HeaderMap) -> opentelemetry::Context {
    struct HeaderExtractor<'a>(&'a axum::http::HeaderMap);

    impl<'a> Extractor for HeaderExtractor<'a> {
        fn get(&self, key: &str) -> Option<&str> {
            self.0.get(key).and_then(|v| v.to_str().ok())
        }

        fn keys(&self) -> Vec<&str> {
            self.0.keys().map(|k| k.as_str()).collect()
        }
    }

    global::get_text_map_propagator(|prop| prop.extract(&HeaderExtractor(headers)))
}

/// Extracts a trace ID from W3C `traceparent` or `x-trace-id` header, generating a new 32-hex ID if absent.
pub fn extract_or_generate_trace_id(traceparent: Option<&str>, x_trace_id: Option<&str>) -> String {
    if let Some(tp) = traceparent {
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

/// Masks secrets/tokens before logging. UTF-8 safe.
pub fn mask_secret(value: &str) -> String {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return "[EMPTY]".to_string();
    }
    if trimmed.chars().count() <= 8 {
        return "[REDACTED]".to_string();
    }
    let prefix: String = trimmed.chars().take(4).collect();
    format!("{prefix}***[REDACTED]")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_trace_id() {
        let tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";
        assert_eq!(
            extract_or_generate_trace_id(Some(tp), None),
            "4bf92f3577b34da6a3ce929d0e0e4736"
        );
        assert_eq!(
            extract_or_generate_trace_id(None, Some("custom-trace-123")),
            "custom-trace-123"
        );
        assert_eq!(extract_or_generate_trace_id(None, None).len(), 32);
    }

    #[test]
    fn test_mask_secret() {
        assert_eq!(mask_secret(""), "[EMPTY]");
        assert_eq!(mask_secret("short"), "[REDACTED]");
        assert_eq!(mask_secret("sk-ant-api03-secretvalue"), "sk-a***[REDACTED]");
    }
}
