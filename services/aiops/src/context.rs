use std::collections::HashMap;
use std::sync::OnceLock;

use chrono::{Duration as ChronoDuration, Utc};
use regex::Regex;
use serde::Deserialize;
use url::Url;

use crate::config::Config;
use crate::http_client::ResilientHttpClient;
use crate::models::{DeployRecord, GatheredContext, LokiLogEntry, TempoTraceSummary};

/// Redacts PII and secrets (emails, phone numbers, JWTs, Bearer/Basic tokens,
/// DSN credentials, API keys, password/secret key-value fields, bank/ID PII)
/// from log lines before storing or forwarding to any LLM.
pub fn redact_pii_and_secrets(input: &str) -> String {
    static JWT_RE: OnceLock<Regex> = OnceLock::new();
    static BEARER_RE: OnceLock<Regex> = OnceLock::new();
    static DSN_RE: OnceLock<Regex> = OnceLock::new();
    static API_KEY_RE: OnceLock<Regex> = OnceLock::new();
    static SECRET_KV_RE: OnceLock<Regex> = OnceLock::new();
    static PHONE_KV_RE: OnceLock<Regex> = OnceLock::new();
    static PII_KV_RE: OnceLock<Regex> = OnceLock::new();
    static EMAIL_RE: OnceLock<Regex> = OnceLock::new();
    static PHONE_RE: OnceLock<Regex> = OnceLock::new();

    let jwt = JWT_RE.get_or_init(|| {
        Regex::new(r"eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}")
            .expect("jwt regex")
    });
    let bearer = BEARER_RE.get_or_init(|| {
        Regex::new(r"(?i)(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{8,}").expect("bearer regex")
    });
    let dsn = DSN_RE.get_or_init(|| {
        Regex::new(r"(?i)(postgres(?:ql)?|mysql|redis|amqp|nats|mongodb|https?)://[^:\s/@]+:[^@\s/]+@")
            .expect("dsn regex")
    });
    let api_key = API_KEY_RE.get_or_init(|| {
        Regex::new(r"(?:sk-ant-[A-Za-z0-9_-]{8,}|sk-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,})")
            .expect("api key regex")
    });
    let secret_kv = SECRET_KV_RE.get_or_init(|| {
        Regex::new(r#"(?i)("?(?:password|passwd|pwd|secret|api[_-]?key|access[_-]?token|refresh[_-]?token|auth[_-]?token|webhook[_-]?secret|client[_-]?secret|private[_-]?key|x-signature|signature|x-api-key|cookie|authorization)"?\s*[:=]\s*"?)([^"\s,;}{]+)"#)
            .expect("secret kv regex")
    });
    let phone_kv = PHONE_KV_RE.get_or_init(|| {
        Regex::new(r#"(?i)("?(?:phone|recipient_phone|mobile|tel)"?\s*[:=]\s*"?)([+\d][\d\s().-]{6,18})"#)
            .expect("phone kv regex")
    });
    let pii_kv = PII_KV_RE.get_or_init(|| {
        Regex::new(r#"(?i)("?(?:bank_account|card_number|id_card|cccd|cmnd|tax_code)"?\s*[:=]\s*"?)([A-Za-z0-9-]{6,24})"#)
            .expect("pii kv regex")
    });
    let email = EMAIL_RE.get_or_init(|| {
        Regex::new(r"(?i)\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b").expect("email regex")
    });
    let phone = PHONE_RE.get_or_init(|| {
        Regex::new(
            r"(?i)(?:(?:\+84|84|0)(?:3|5|7|8|9)\d(?:[-.\s]?\d){7}\b|\+\d{1,3}[-.\s]?\(?\d{2,4}\)?[-.\s]?\d{3,4}[-.\s]?\d{3,4}\b)",
        )
        .expect("phone regex")
    });

    let s = jwt.replace_all(input, "[REDACTED_JWT]");
    let s = bearer.replace_all(&s, "$1 [REDACTED_TOKEN]");
    let s = dsn.replace_all(&s, "$1://[REDACTED_CREDENTIALS]@");
    let s = api_key.replace_all(&s, "[REDACTED_API_KEY]");
    let s = secret_kv.replace_all(&s, "${1}[REDACTED]");
    let s = phone_kv.replace_all(&s, "${1}[REDACTED_PHONE]");
    let s = pii_kv.replace_all(&s, "${1}[REDACTED_PII]");
    let s = email.replace_all(&s, "[REDACTED_EMAIL]");
    let s = phone.replace_all(&s, "[REDACTED_PHONE]");
    s.into_owned()
}

#[derive(Debug, Deserialize)]
struct LokiQueryResponse {
    data: Option<LokiData>,
}

#[derive(Debug, Deserialize)]
struct LokiData {
    #[serde(default)]
    result: Vec<LokiStream>,
}

#[derive(Debug, Deserialize)]
struct LokiStream {
    #[serde(default)]
    stream: HashMap<String, String>,
    #[serde(default)]
    values: Vec<(String, String)>,
}

#[derive(Debug, Deserialize)]
struct TempoSearchResponse {
    #[serde(default)]
    traces: Vec<TempoTraceRaw>,
}

#[derive(Debug, Deserialize)]
struct TempoTraceRaw {
    #[serde(rename = "traceID", default)]
    trace_id: String,
    #[serde(rename = "rootServiceName", default)]
    root_service_name: String,
    #[serde(rename = "rootTraceName", default)]
    root_trace_name: String,
    #[serde(rename = "durationMs", default)]
    duration_ms: u64,
    #[serde(rename = "startTimeUnixNano", default)]
    start_time_unix_nano: Option<String>,
}

#[derive(Clone)]
pub struct ContextCollector {
    config: Config,
    http: ResilientHttpClient,
}

impl ContextCollector {
    pub fn new(config: Config, http: ResilientHttpClient) -> Self {
        Self { config, http }
    }

    pub async fn gather(
        &self,
        service: &str,
        all_deploys: &[DeployRecord],
        dependencies: HashMap<String, bool>,
        service_up: HashMap<String, bool>,
    ) -> GatheredContext {
        let mut ctx = GatheredContext {
            dependencies,
            service_up,
            ..Default::default()
        };

        let cutoff = Utc::now() - ChronoDuration::minutes(30);
        ctx.recent_deploys = all_deploys
            .iter()
            .filter(|d| d.service == service && d.deployed_at >= cutoff)
            .cloned()
            .collect();

        let (logs_res, traces_res) =
            tokio::join!(self.fetch_loki_logs(service), self.fetch_tempo_traces());

        match logs_res {
            Ok(logs) => ctx.logs = logs,
            Err(e) => ctx.errors.push(format!("loki: {e}")),
        }

        match traces_res {
            Ok(mut traces) => {
                // Prioritize traces involving the affected service, keeping up to 10.
                traces.sort_by_key(|t| {
                    let matches_svc = t.root_service_name == service;
                    (!matches_svc, std::cmp::Reverse(t.duration_ms))
                });
                traces.truncate(10);
                ctx.slow_traces = traces;
            }
            Err(e) => ctx.errors.push(format!("tempo: {e}")),
        }

        ctx
    }

    async fn fetch_loki_logs(&self, service: &str) -> Result<Vec<LokiLogEntry>, String> {
        let now = Utc::now();
        let start_ns = (now - ChronoDuration::minutes(5))
            .timestamp_nanos_opt()
            .unwrap_or(0);
        let end_ns = now.timestamp_nanos_opt().unwrap_or(0);

        let sanitized_svc: String = service
            .chars()
            .filter(|c| c.is_ascii_alphanumeric() || *c == '-' || *c == '_')
            .collect();
        let logql = if sanitized_svc == "nats" || sanitized_svc.is_empty() {
            r#"{service=~".+"} |~ "(?i)(nats|jetstream|disconnect|broker|error|fail)""#.to_string()
        } else {
            format!(
                r#"{{service=~"{sanitized_svc}|gateway"}} |~ "(?i)(error|warn|fail|panic|timeout|disconnect|refused|503|unavailable|postgres|sqlstate|pgx|database|nats)""#
            )
        };

        let base = format!("{}/loki/api/v1/query_range", self.config.loki_url);
        let url = Url::parse_with_params(
            &base,
            &[
                ("query", logql.as_str()),
                ("start", &start_ns.to_string()),
                ("end", &end_ns.to_string()),
                ("limit", "30"),
                ("direction", "backward"),
            ],
        )
        .map_err(|e| e.to_string())?
        .to_string();

        let resp: LokiQueryResponse = self
            .http
            .get_json("loki", &url, &[])
            .await
            .map_err(|e| e.to_string())?;

        let mut entries = Vec::new();
        if let Some(data) = resp.data {
            for stream in data.result {
                let svc = stream
                    .stream
                    .get("service")
                    .cloned()
                    .unwrap_or_else(|| sanitized_svc.clone());
                for (ts, line) in stream.values {
                    entries.push(LokiLogEntry {
                        timestamp: ts,
                        service: svc.clone(),
                        line: redact_pii_and_secrets(&line),
                    });
                    if entries.len() >= 30 {
                        return Ok(entries);
                    }
                }
            }
        }
        Ok(entries)
    }

    async fn fetch_tempo_traces(&self) -> Result<Vec<TempoTraceSummary>, String> {
        let now = Utc::now();
        let start = (now - ChronoDuration::minutes(5)).timestamp();
        let end = now.timestamp();

        let base = format!("{}/api/search", self.config.tempo_url);
        let url = Url::parse_with_params(
            &base,
            &[
                ("minDuration", "200ms"),
                ("limit", "10"),
                ("start", &start.to_string()),
                ("end", &end.to_string()),
            ],
        )
        .map_err(|e| e.to_string())?
        .to_string();

        let resp: TempoSearchResponse = self
            .http
            .get_json("tempo", &url, &[])
            .await
            .map_err(|e| e.to_string())?;

        Ok(resp
            .traces
            .into_iter()
            .map(|t| TempoTraceSummary {
                trace_id: t.trace_id,
                root_service_name: t.root_service_name,
                root_trace_name: t.root_trace_name,
                duration_ms: t.duration_ms,
                start_time_unix_nano: t.start_time_unix_nano,
            })
            .collect())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_redact_pii_and_secrets_comprehensive() {
        let raw = r#"{"time":"2026-11-21T14:51:12Z","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","msg":"failed auth for buyer.test@nexusmart.vn phone=0901234567 mobile=+84912345678 Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c dsn=postgres://nexus:SuperPass123@postgres:5432/promotion_db api_key=sk-ant-api03-abcdef1234567890 password=mySecret99 bank_account=19034567890123 host=172.18.0.5:8084"}"#;

        let clean = redact_pii_and_secrets(raw);
        assert!(!clean.contains("buyer.test@nexusmart.vn"));
        assert!(!clean.contains("0901234567"));
        assert!(!clean.contains("+84912345678"));
        assert!(!clean.contains("eyJhbGciOi"));
        assert!(!clean.contains("SuperPass123"));
        assert!(!clean.contains("sk-ant-api03-abcdef1234567890"));
        assert!(!clean.contains("mySecret99"));
        assert!(!clean.contains("19034567890123"));

        // Ensure operational metadata (timestamp, trace_id, host:port) is preserved.
        assert!(clean.contains("2026-11-21T14:51:12Z"));
        assert!(clean.contains("4bf92f3577b34da6a3ce929d0e0e4736"));
        assert!(clean.contains("172.18.0.5:8084"));
        assert!(clean.contains("[REDACTED_EMAIL]"));
        assert!(clean.contains("[REDACTED_PHONE]"));
        assert!(clean.contains("[REDACTED_JWT]"));
        assert!(clean.contains("postgres://[REDACTED_CREDENTIALS]@postgres:5432/promotion_db"));
    }
}
