use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use reqwest::{Client, Method};
use serde::de::DeserializeOwned;
use thiserror::Error;
use tracing::warn;

#[derive(Debug, Error)]
pub enum HttpError {
    #[error("circuit breaker open for dependency '{0}'")]
    CircuitOpen(String),
    #[error("request failed for '{dependency}': {message}")]
    Request { dependency: String, message: String },
    #[error("upstream '{dependency}' returned HTTP {status}: {body}")]
    HttpStatus {
        dependency: String,
        status: u16,
        body: String,
    },
    #[error("failed to decode JSON from '{dependency}': {message}")]
    Decode { dependency: String, message: String },
}

#[derive(Debug, Clone)]
struct CircuitState {
    consecutive_failures: u32,
    opened_at: Option<Instant>,
}

impl Default for CircuitState {
    fn default() -> Self {
        Self {
            consecutive_failures: 0,
            opened_at: None,
        }
    }
}

#[derive(Clone)]
pub struct ResilientHttpClient {
    client: Client,
    probe_client: Client,
    max_retries: usize,
    base_backoff: Duration,
    failure_threshold: u32,
    reset_timeout: Duration,
    breakers: Arc<Mutex<HashMap<String, CircuitState>>>,
}

impl ResilientHttpClient {
    pub fn new(timeout: Duration) -> Self {
        let client = Client::builder()
            .connect_timeout(timeout.min(Duration::from_secs(3)))
            .timeout(timeout)
            .build()
            .expect("build reqwest client");
        let probe_client = Client::builder()
            .connect_timeout(Duration::from_secs(2))
            .timeout(Duration::from_secs(2))
            .build()
            .expect("build probe reqwest client");

        Self {
            client,
            probe_client,
            max_retries: 2,
            base_backoff: Duration::from_millis(100),
            failure_threshold: 5,
            reset_timeout: Duration::from_secs(15),
            breakers: Arc::new(Mutex::new(HashMap::new())),
        }
    }

    pub fn with_policy(
        timeout: Duration,
        max_retries: usize,
        base_backoff: Duration,
        failure_threshold: u32,
        reset_timeout: Duration,
    ) -> Self {
        let mut s = Self::new(timeout);
        s.max_retries = max_retries;
        s.base_backoff = base_backoff;
        s.failure_threshold = failure_threshold.max(1);
        s.reset_timeout = reset_timeout;
        s
    }

    fn check_circuit(&self, dep: &str) -> Result<(), HttpError> {
        let mut map = self.breakers.lock().expect("breaker lock");
        let entry = map.entry(dep.to_string()).or_default();
        if let Some(opened) = entry.opened_at {
            if opened.elapsed() < self.reset_timeout {
                return Err(HttpError::CircuitOpen(dep.to_string()));
            }
            // Half-open: allow one probe attempt.
            entry.opened_at = None;
        }
        Ok(())
    }

    fn record_success(&self, dep: &str) {
        let mut map = self.breakers.lock().expect("breaker lock");
        if let Some(entry) = map.get_mut(dep) {
            entry.consecutive_failures = 0;
            entry.opened_at = None;
        }
    }

    fn record_failure(&self, dep: &str) {
        let mut map = self.breakers.lock().expect("breaker lock");
        let entry = map.entry(dep.to_string()).or_default();
        entry.consecutive_failures = entry.consecutive_failures.saturating_add(1);
        if entry.consecutive_failures >= self.failure_threshold {
            entry.opened_at = Some(Instant::now());
            warn!(
                service = "aiops",
                dependency = dep,
                failures = entry.consecutive_failures,
                "circuit breaker opened for upstream dependency"
            );
        }
    }

    pub async fn send(
        &self,
        dep: &str,
        method: Method,
        url: &str,
        headers: &[(String, String)],
        body: Option<&[u8]>,
    ) -> Result<(u16, Vec<u8>), HttpError> {
        self.check_circuit(dep)?;

        let mut last_err: Option<HttpError> = None;
        for attempt in 0..=self.max_retries {
            if attempt > 0 {
                let shift = (attempt - 1).min(4) as u32;
                let delay = self.base_backoff.saturating_mul(1u32 << shift);
                tokio::time::sleep(delay).await;
            }

            let mut req = self.client.request(method.clone(), url);
            for (k, v) in headers {
                req = req.header(k, v);
            }
            if let Some(bytes) = body {
                req = req.body(bytes.to_vec());
            }

            match req.send().await {
                Ok(resp) => {
                    let status = resp.status().as_u16();
                    let bytes = resp.bytes().await.map(|b| b.to_vec()).unwrap_or_default();
                    if status == 429 || status >= 500 {
                        let snippet = truncate_str(&String::from_utf8_lossy(&bytes), 240);
                        last_err = Some(HttpError::HttpStatus {
                            dependency: dep.to_string(),
                            status,
                            body: snippet,
                        });
                        continue;
                    }
                    self.record_success(dep);
                    return Ok((status, bytes));
                }
                Err(e) => {
                    last_err = Some(HttpError::Request {
                        dependency: dep.to_string(),
                        message: e.to_string(),
                    });
                }
            }
        }

        self.record_failure(dep);
        Err(last_err.unwrap_or_else(|| HttpError::Request {
            dependency: dep.to_string(),
            message: "unknown upstream error".to_string(),
        }))
    }

    pub async fn get_json<T: DeserializeOwned>(
        &self,
        dep: &str,
        url: &str,
        headers: &[(String, String)],
    ) -> Result<T, HttpError> {
        let (status, bytes) = self.send(dep, Method::GET, url, headers, None).await?;
        if !(200..300).contains(&status) {
            return Err(HttpError::HttpStatus {
                dependency: dep.to_string(),
                status,
                body: truncate_str(&String::from_utf8_lossy(&bytes), 240),
            });
        }
        serde_json::from_slice(&bytes).map_err(|e| HttpError::Decode {
            dependency: dep.to_string(),
            message: e.to_string(),
        })
    }

    pub async fn post_json_raw(
        &self,
        dep: &str,
        url: &str,
        headers: &[(String, String)],
        payload: &[u8],
    ) -> Result<(u16, Vec<u8>), HttpError> {
        let mut all_headers = Vec::with_capacity(headers.len() + 1);
        let has_ct = headers
            .iter()
            .any(|(k, _)| k.eq_ignore_ascii_case("content-type"));
        if !has_ct {
            all_headers.push(("content-type".to_string(), "application/json".to_string()));
        }
        all_headers.extend(headers.iter().cloned());
        self.send(dep, Method::POST, url, &all_headers, Some(payload))
            .await
    }

    pub async fn patch_raw(
        &self,
        dep: &str,
        url: &str,
        headers: &[(String, String)],
        payload: &[u8],
    ) -> Result<(u16, Vec<u8>), HttpError> {
        self.send(dep, Method::PATCH, url, headers, Some(payload))
            .await
    }

    /// Lightweight health probe (2s timeout, no circuit lockout so recovery is observed immediately).
    pub async fn probe_http(&self, url: &str) -> Result<bool, String> {
        match self.probe_client.get(url).send().await {
            Ok(resp) => Ok(resp.status().is_success()),
            Err(e) => Err(e.to_string()),
        }
    }
}

fn truncate_str(s: &str, max_chars: usize) -> String {
    let mut out: String = s.chars().take(max_chars).collect();
    if s.chars().count() > max_chars {
        out.push_str("...");
    }
    out
}

/// Minimal HTTP/1.1 request over a Unix domain socket (used for Docker Engine `/var/run/docker.sock`).
#[cfg(unix)]
pub async fn unix_http_request(
    socket_path: &str,
    method: &str,
    path_and_query: &str,
    content_type: Option<&str>,
    body: Option<&[u8]>,
    timeout: Duration,
) -> Result<(u16, Vec<u8>), HttpError> {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::UnixStream;

    let fut = async {
        let mut stream = UnixStream::connect(socket_path)
            .await
            .map_err(|e| HttpError::Request {
                dependency: "docker-socket".to_string(),
                message: format!("connect {socket_path}: {e}"),
            })?;

        let body_bytes = body.unwrap_or(&[]);
        let mut req = format!(
            "{method} {path_and_query} HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n"
        );
        if let Some(ct) = content_type {
            req.push_str(&format!("Content-Type: {ct}\r\n"));
        }
        if !body_bytes.is_empty() {
            req.push_str(&format!("Content-Length: {}\r\n", body_bytes.len()));
        }
        req.push_str("\r\n");

        stream
            .write_all(req.as_bytes())
            .await
            .map_err(|e| HttpError::Request {
                dependency: "docker-socket".to_string(),
                message: format!("write headers: {e}"),
            })?;
        if !body_bytes.is_empty() {
            stream
                .write_all(body_bytes)
                .await
                .map_err(|e| HttpError::Request {
                    dependency: "docker-socket".to_string(),
                    message: format!("write body: {e}"),
                })?;
        }
        let _ = stream.flush().await;

        let mut raw = Vec::new();
        stream
            .read_to_end(&mut raw)
            .await
            .map_err(|e| HttpError::Request {
                dependency: "docker-socket".to_string(),
                message: format!("read response: {e}"),
            })?;

        parse_http1_response(&raw)
    };

    tokio::time::timeout(timeout, fut)
        .await
        .map_err(|_| HttpError::Request {
            dependency: "docker-socket".to_string(),
            message: format!("timeout after {}ms", timeout.as_millis()),
        })?
}

#[cfg(not(unix))]
pub async fn unix_http_request(
    _socket_path: &str,
    _method: &str,
    _path_and_query: &str,
    _content_type: Option<&str>,
    _body: Option<&[u8]>,
    _timeout: Duration,
) -> Result<(u16, Vec<u8>), HttpError> {
    Err(HttpError::Request {
        dependency: "docker-socket".to_string(),
        message: "unix domain sockets are not supported on this platform".to_string(),
    })
}

fn parse_http1_response(raw: &[u8]) -> Result<(u16, Vec<u8>), HttpError> {
    let header_end = raw
        .windows(4)
        .position(|w| w == b"\r\n\r\n")
        .ok_or_else(|| HttpError::Decode {
            dependency: "docker-socket".to_string(),
            message: "malformed HTTP/1.1 response (missing header terminator)".to_string(),
        })?;

    let header_str = String::from_utf8_lossy(&raw[..header_end]);
    let mut lines = header_str.lines();
    let status_line = lines.next().unwrap_or_default();
    let status: u16 = status_line
        .split_whitespace()
        .nth(1)
        .and_then(|s| s.parse().ok())
        .ok_or_else(|| HttpError::Decode {
            dependency: "docker-socket".to_string(),
            message: format!("invalid status line: {status_line}"),
        })?;

    let is_chunked = lines.any(|line| {
        let lower = line.to_ascii_lowercase();
        lower.starts_with("transfer-encoding:") && lower.contains("chunked")
    });

    let body_slice = &raw[header_end + 4..];
    let body = if is_chunked {
        decode_chunked(body_slice)?
    } else {
        body_slice.to_vec()
    };

    Ok((status, body))
}

fn decode_chunked(mut data: &[u8]) -> Result<Vec<u8>, HttpError> {
    let mut out = Vec::new();
    loop {
        let Some(crlf) = data.windows(2).position(|w| w == b"\r\n") else {
            break;
        };
        let size_line = String::from_utf8_lossy(&data[..crlf]);
        let size_hex = size_line.split(';').next().unwrap_or("").trim();
        let size = usize::from_str_radix(size_hex, 16).map_err(|e| HttpError::Decode {
            dependency: "docker-socket".to_string(),
            message: format!("invalid chunk size '{size_hex}': {e}"),
        })?;
        data = &data[crlf + 2..];
        if size == 0 {
            break;
        }
        if data.len() < size {
            out.extend_from_slice(data);
            break;
        }
        out.extend_from_slice(&data[..size]);
        data = &data[size..];
        if data.starts_with(b"\r\n") {
            data = &data[2..];
        }
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_decode_chunked_http1_response() {
        let raw = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n";
        let (status, body) = parse_http1_response(raw).expect("parse ok");
        assert_eq!(status, 200);
        assert_eq!(body, b"hello world");
    }

    #[test]
    fn test_circuit_breaker_opens_and_resets() {
        let client = ResilientHttpClient::with_policy(
            Duration::from_secs(1),
            0,
            Duration::from_millis(1),
            2,
            Duration::from_millis(20),
        );
        assert!(client.check_circuit("prom").is_ok());
        client.record_failure("prom");
        assert!(client.check_circuit("prom").is_ok());
        client.record_failure("prom");
        assert!(matches!(
            client.check_circuit("prom"),
            Err(HttpError::CircuitOpen(_))
        ));
        std::thread::sleep(Duration::from_millis(25));
        assert!(client.check_circuit("prom").is_ok());
        client.record_success("prom");
        assert!(client.check_circuit("prom").is_ok());
    }
}
