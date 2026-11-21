use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use chrono::Utc;
use reqwest::Method;
use serde::Deserialize;
use thiserror::Error;
use url::form_urlencoded;

use crate::config::{AdapterKind, Config};
use crate::http_client::{unix_http_request, ResilientHttpClient};

pub const MIN_REPLICAS: u32 = 1;
pub const MAX_REPLICAS: u32 = 10;

#[derive(Debug, Error)]
pub enum AdapterError {
    #[error("replicas must be within {MIN_REPLICAS}..={MAX_REPLICAS}, got {0}")]
    InvalidReplicas(u32),
    #[error("no container found for docker compose service '{0}'")]
    ContainerNotFound(String),
    #[error("scale({service}, {replicas}) is unsupported in single-host Docker Compose without Swarm mode")]
    UnsupportedInSingleCompose { service: String, replicas: u32 },
    #[error("kubernetes service account token not configured or unreadable")]
    MissingToken,
    #[error("runtime adapter upstream error: {0}")]
    Upstream(String),
}

#[async_trait]
pub trait RuntimeAdapter: Send + Sync {
    fn name(&self) -> &'static str;
    async fn restart(&self, service: &str) -> Result<String, AdapterError>;
    async fn scale(&self, service: &str, replicas: u32) -> Result<String, AdapterError>;
}

pub fn build_runtime_adapter(
    config: &Config,
    http: ResilientHttpClient,
) -> Arc<dyn RuntimeAdapter> {
    match config.adapter_kind {
        AdapterKind::Docker => Arc::new(DockerComposeAdapter::new(config.clone(), http)),
        AdapterKind::Kubernetes => Arc::new(KubernetesAdapter::new(config.clone(), http)),
    }
}

#[derive(Debug, Deserialize)]
struct DockerContainer {
    #[serde(rename = "Id")]
    id: String,
}

#[derive(Debug, Deserialize)]
struct DockerSwarmService {
    #[serde(rename = "Version")]
    version: DockerVersion,
    #[serde(rename = "Spec")]
    spec: serde_json::Value,
}

#[derive(Debug, Deserialize)]
struct DockerVersion {
    #[serde(rename = "Index")]
    index: u64,
}

pub struct DockerComposeAdapter {
    socket_path: String,
    docker_host: Option<String>,
    swarm_mode: bool,
    timeout: Duration,
    http: ResilientHttpClient,
}

impl DockerComposeAdapter {
    pub fn new(config: Config, http: ResilientHttpClient) -> Self {
        Self {
            socket_path: config.docker_socket,
            docker_host: config.docker_host,
            swarm_mode: config.docker_swarm_mode,
            timeout: Duration::from_secs(config.http_timeout_secs),
            http,
        }
    }

    async fn docker_request(
        &self,
        method: Method,
        path_and_query: &str,
        content_type: Option<&str>,
        body: Option<&[u8]>,
    ) -> Result<(u16, Vec<u8>), AdapterError> {
        if let Some(ref host) = self.docker_host {
            let url = format!("{host}{path_and_query}");
            let mut headers = Vec::new();
            if let Some(ct) = content_type {
                headers.push(("content-type".to_string(), ct.to_string()));
            }
            self.http
                .send("docker", method, &url, &headers, body)
                .await
                .map_err(|e| AdapterError::Upstream(e.to_string()))
        } else {
            unix_http_request(
                &self.socket_path,
                method.as_str(),
                path_and_query,
                content_type,
                body,
                self.timeout,
            )
            .await
            .map_err(|e| AdapterError::Upstream(e.to_string()))
        }
    }
}

#[async_trait]
impl RuntimeAdapter for DockerComposeAdapter {
    fn name(&self) -> &'static str {
        "docker"
    }

    async fn restart(&self, service: &str) -> Result<String, AdapterError> {
        let filters_json = serde_json::json!({
            "label": [format!("com.docker.compose.service={service}")]
        })
        .to_string();
        let encoded_filters: String =
            form_urlencoded::byte_serialize(filters_json.as_bytes()).collect();
        let list_path = format!("/containers/json?all=1&filters={encoded_filters}");

        let (status, body) = self
            .docker_request(Method::GET, &list_path, None, None)
            .await?;
        if !(200..300).contains(&status) {
            return Err(AdapterError::Upstream(format!(
                "GET {list_path} returned HTTP {status}: {}",
                String::from_utf8_lossy(&body)
            )));
        }

        let containers: Vec<DockerContainer> = serde_json::from_slice(&body)
            .map_err(|e| AdapterError::Upstream(format!("decode containers list: {e}")))?;
        if containers.is_empty() {
            return Err(AdapterError::ContainerNotFound(service.to_string()));
        }

        let mut restarted = Vec::new();
        for c in &containers {
            let restart_path = format!("/containers/{}/restart?t=10", c.id);
            let (r_status, r_body) = self
                .docker_request(Method::POST, &restart_path, None, None)
                .await?;
            if !(200..300).contains(&r_status) {
                return Err(AdapterError::Upstream(format!(
                    "POST {restart_path} returned HTTP {r_status}: {}",
                    String::from_utf8_lossy(&r_body)
                )));
            }
            let short_id: String = c.id.chars().take(12).collect();
            restarted.push(short_id);
        }

        Ok(format!(
            "restarted {} docker container(s) for service '{service}': {}",
            restarted.len(),
            restarted.join(", ")
        ))
    }

    async fn scale(&self, service: &str, replicas: u32) -> Result<String, AdapterError> {
        if !(MIN_REPLICAS..=MAX_REPLICAS).contains(&replicas) {
            return Err(AdapterError::InvalidReplicas(replicas));
        }
        if !self.swarm_mode {
            return Err(AdapterError::UnsupportedInSingleCompose {
                service: service.to_string(),
                replicas,
            });
        }

        let inspect_path = format!("/services/{service}");
        let (status, body) = self
            .docker_request(Method::GET, &inspect_path, None, None)
            .await?;
        if !(200..300).contains(&status) {
            return Err(AdapterError::Upstream(format!(
                "GET {inspect_path} returned HTTP {status}: {}",
                String::from_utf8_lossy(&body)
            )));
        }

        let mut svc: DockerSwarmService = serde_json::from_slice(&body)
            .map_err(|e| AdapterError::Upstream(format!("decode swarm service: {e}")))?;
        if let Some(obj) = svc.spec.as_object_mut() {
            obj.insert(
                "Mode".to_string(),
                serde_json::json!({ "Replicated": { "Replicas": replicas } }),
            );
        }

        let update_path = format!("/services/{service}/update?version={}", svc.version.index);
        let payload = serde_json::to_vec(&svc.spec)
            .map_err(|e| AdapterError::Upstream(format!("encode swarm spec: {e}")))?;
        let (u_status, u_body) = self
            .docker_request(
                Method::POST,
                &update_path,
                Some("application/json"),
                Some(&payload),
            )
            .await?;
        if !(200..300).contains(&u_status) {
            return Err(AdapterError::Upstream(format!(
                "POST {update_path} returned HTTP {u_status}: {}",
                String::from_utf8_lossy(&u_body)
            )));
        }

        Ok(format!(
            "scaled docker swarm service '{service}' to {replicas} replicas"
        ))
    }
}

pub struct KubernetesAdapter {
    api_url: String,
    token: Option<String>,
    namespace: String,
    http: ResilientHttpClient,
}

impl KubernetesAdapter {
    pub fn new(config: Config, http: ResilientHttpClient) -> Self {
        Self {
            api_url: config.kube_api_url,
            token: config.kube_token,
            namespace: config.kube_namespace,
            http,
        }
    }

    fn resolve_token(&self) -> Result<String, AdapterError> {
        if let Some(ref t) = self.token {
            if !t.trim().is_empty() {
                return Ok(t.trim().to_string());
            }
        }
        std::fs::read_to_string("/var/run/secrets/kubernetes.io/serviceaccount/token")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .ok_or(AdapterError::MissingToken)
    }
}

#[async_trait]
impl RuntimeAdapter for KubernetesAdapter {
    fn name(&self) -> &'static str {
        "kubernetes"
    }

    async fn restart(&self, service: &str) -> Result<String, AdapterError> {
        let token = self.resolve_token()?;
        let url = format!(
            "{}/apis/apps/v1/namespaces/{}/deployments/{}",
            self.api_url, self.namespace, service
        );
        let restarted_at = Utc::now().to_rfc3339();
        let patch = serde_json::json!({
            "spec": {
                "template": {
                    "metadata": {
                        "annotations": {
                            "kubectl.kubernetes.io/restartedAt": restarted_at
                        }
                    }
                }
            }
        });
        let body = serde_json::to_vec(&patch)
            .map_err(|e| AdapterError::Upstream(format!("encode patch: {e}")))?;
        let headers = vec![
            ("authorization".to_string(), format!("Bearer {token}")),
            (
                "content-type".to_string(),
                "application/strategic-merge-patch+json".to_string(),
            ),
        ];

        let (status, resp_body) = self
            .http
            .patch_raw("kubernetes", &url, &headers, &body)
            .await
            .map_err(|e| AdapterError::Upstream(e.to_string()))?;
        if !(200..300).contains(&status) {
            return Err(AdapterError::Upstream(format!(
                "PATCH deployment '{service}' returned HTTP {status}: {}",
                String::from_utf8_lossy(&resp_body)
            )));
        }

        Ok(format!(
            "triggered rolling restart for kubernetes deployment '{}/{}'",
            self.namespace, service
        ))
    }

    async fn scale(&self, service: &str, replicas: u32) -> Result<String, AdapterError> {
        if !(MIN_REPLICAS..=MAX_REPLICAS).contains(&replicas) {
            return Err(AdapterError::InvalidReplicas(replicas));
        }
        let token = self.resolve_token()?;
        let url = format!(
            "{}/apis/apps/v1/namespaces/{}/deployments/{}/scale",
            self.api_url, self.namespace, service
        );
        let patch = serde_json::json!({
            "spec": {
                "replicas": replicas
            }
        });
        let body = serde_json::to_vec(&patch)
            .map_err(|e| AdapterError::Upstream(format!("encode scale patch: {e}")))?;
        let headers = vec![
            ("authorization".to_string(), format!("Bearer {token}")),
            (
                "content-type".to_string(),
                "application/merge-patch+json".to_string(),
            ),
        ];

        let (status, resp_body) = self
            .http
            .patch_raw("kubernetes", &url, &headers, &body)
            .await
            .map_err(|e| AdapterError::Upstream(e.to_string()))?;
        if !(200..300).contains(&status) {
            return Err(AdapterError::Upstream(format!(
                "PATCH deployment '{service}' scale returned HTTP {status}: {}",
                String::from_utf8_lossy(&resp_body)
            )));
        }

        Ok(format!(
            "scaled kubernetes deployment '{}/{}' to {replicas} replicas",
            self.namespace, service
        ))
    }
}
