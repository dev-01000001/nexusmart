use std::collections::{HashMap, VecDeque};
use std::sync::{Arc, Mutex, RwLock};
use std::time::{Duration, Instant};

use chrono::Utc;
use thiserror::Error;
use tracing::{info, warn};
use uuid::Uuid;

use crate::adapter::{RuntimeAdapter, MAX_REPLICAS, MIN_REPLICAS};
use crate::config::Config;
use crate::metrics::Metrics;
use crate::models::{AuditEntry, RemediationRecord};

const RATE_WINDOW: Duration = Duration::from_secs(3600);
const MAX_AUDIT_ENTRIES: usize = 500;

#[derive(Debug, Error)]
pub enum RemediationPolicyError {
    #[error("blocked by allowlist: {0}")]
    BlockedAllowlist(String),
    #[error("invalid remediation input: {0}")]
    BlockedInvalidInput(String),
    #[error("blocked by hourly rate limit: {0}")]
    BlockedRateLimit(String),
    #[error("remediation execution failed: {0}")]
    ExecutionFailed(String),
}

#[derive(Default)]
struct RateLimitState {
    global: VecDeque<Instant>,
    per_service: HashMap<String, VecDeque<Instant>>,
}

impl RateLimitState {
    fn prune(queue: &mut VecDeque<Instant>, now: Instant) {
        while let Some(&front) = queue.front() {
            if now.saturating_duration_since(front) >= RATE_WINDOW {
                queue.pop_front();
            } else {
                break;
            }
        }
    }

    fn check_and_record(&mut self, service: &str, max_per_hour: u32) -> Result<(), String> {
        let now = Instant::now();
        Self::prune(&mut self.global, now);
        let svc_q = self.per_service.entry(service.to_string()).or_default();
        Self::prune(svc_q, now);

        let limit = max_per_hour as usize;
        if svc_q.len() >= limit {
            return Err(format!(
                "service '{service}' reached hourly remediation limit ({}/{limit})",
                svc_q.len()
            ));
        }
        if self.global.len() >= limit {
            return Err(format!(
                "global hourly remediation limit reached ({}/{limit})",
                self.global.len()
            ));
        }

        svc_q.push_back(now);
        self.global.push_back(now);
        Ok(())
    }
}

pub struct Remediator {
    config: Config,
    adapter: Arc<dyn RuntimeAdapter>,
    metrics: Metrics,
    rate_state: Mutex<RateLimitState>,
    audit: RwLock<VecDeque<AuditEntry>>,
}

impl Remediator {
    pub fn new(config: Config, adapter: Arc<dyn RuntimeAdapter>, metrics: Metrics) -> Self {
        Self {
            config,
            adapter,
            metrics,
            rate_state: Mutex::new(RateLimitState::default()),
            audit: RwLock::new(VecDeque::new()),
        }
    }

    pub fn is_dry_run(&self) -> bool {
        self.config.dry_run
    }

    pub fn adapter_name(&self) -> &'static str {
        self.adapter.name()
    }

    pub fn record_audit_event(
        &self,
        event_type: &str,
        service: &str,
        action: Option<&str>,
        mode: Option<&str>,
        outcome: &str,
        detail: impl Into<String>,
        incident_id: Option<Uuid>,
    ) {
        let entry = AuditEntry {
            id: Uuid::new_v4(),
            timestamp: Utc::now(),
            event_type: event_type.to_string(),
            service: service.to_string(),
            action: action.map(|s| s.to_string()),
            mode: mode.map(|s| s.to_string()),
            outcome: outcome.to_string(),
            detail: detail.into(),
            incident_id,
        };
        let mut guard = self.audit.write().expect("audit write lock");
        if guard.len() >= MAX_AUDIT_ENTRIES {
            guard.pop_back();
        }
        guard.push_front(entry);
    }

    pub fn list_audit(&self, service: Option<&str>, outcome: Option<&str>) -> Vec<AuditEntry> {
        let guard = self.audit.read().expect("audit read lock");
        guard
            .iter()
            .filter(|e| service.is_none_or(|s| e.service == s))
            .filter(|e| outcome.is_none_or(|o| e.outcome == o))
            .cloned()
            .collect()
    }

    pub async fn remediate(
        &self,
        incident_id: Option<Uuid>,
        service: &str,
        action: &str,
        replicas: Option<u32>,
    ) -> (RemediationRecord, Result<(), RemediationPolicyError>) {
        let mode = if self.config.dry_run {
            "dry_run"
        } else {
            "live"
        };
        let normalized_action = action.trim().to_ascii_lowercase();
        let normalized_service = service.trim().to_ascii_lowercase();

        // 1. Strict action allowlist
        if normalized_action != "restart" && normalized_action != "scale" {
            let msg = format!(
                "action '{action}' is not in remediation allowlist (allowed: restart, scale)"
            );
            warn!(service = "aiops", target = %normalized_service, action = %action, "remediation blocked by action allowlist");
            self.metrics
                .record_remediation(&normalized_service, action, mode, "blocked_allowlist");
            self.record_audit_event(
                "remediation",
                &normalized_service,
                Some(action),
                Some(mode),
                "blocked_allowlist",
                msg.clone(),
                incident_id,
            );
            let rec = self.build_record(
                incident_id,
                &normalized_service,
                action,
                replicas,
                mode,
                "blocked_allowlist",
                &msg,
            );
            return (rec, Err(RemediationPolicyError::BlockedAllowlist(msg)));
        }

        // 2. Strict service allowlist
        if !self.config.allowed_services.contains(&normalized_service) {
            let msg = format!(
                "service '{normalized_service}' is not in remediation allowlist"
            );
            warn!(service = "aiops", target = %normalized_service, action = %normalized_action, "remediation blocked by service allowlist");
            self.metrics.record_remediation(
                &normalized_service,
                &normalized_action,
                mode,
                "blocked_allowlist",
            );
            self.record_audit_event(
                "remediation",
                &normalized_service,
                Some(&normalized_action),
                Some(mode),
                "blocked_allowlist",
                msg.clone(),
                incident_id,
            );
            let rec = self.build_record(
                incident_id,
                &normalized_service,
                &normalized_action,
                replicas,
                mode,
                "blocked_allowlist",
                &msg,
            );
            return (rec, Err(RemediationPolicyError::BlockedAllowlist(msg)));
        }

        // 3. Scale replica bounds check
        let target_replicas = if normalized_action == "scale" {
            let r = replicas.unwrap_or(0);
            if !(MIN_REPLICAS..=MAX_REPLICAS).contains(&r) {
                let msg = format!(
                    "scale replicas must be within {MIN_REPLICAS}..={MAX_REPLICAS}, got {r}"
                );
                self.metrics.record_remediation(
                    &normalized_service,
                    &normalized_action,
                    mode,
                    "blocked_invalid_input",
                );
                self.record_audit_event(
                    "remediation",
                    &normalized_service,
                    Some(&normalized_action),
                    Some(mode),
                    "blocked_invalid_input",
                    msg.clone(),
                    incident_id,
                );
                let rec = self.build_record(
                    incident_id,
                    &normalized_service,
                    &normalized_action,
                    replicas,
                    mode,
                    "blocked_invalid_input",
                    &msg,
                );
                return (rec, Err(RemediationPolicyError::BlockedInvalidInput(msg)));
            }
            Some(r)
        } else {
            None
        };

        // 4. Sliding 1-hour rate limit (per-service + global)
        let rate_check = {
            let mut guard = self.rate_state.lock().expect("rate limit lock");
            guard.check_and_record(&normalized_service, self.config.max_actions_per_hour)
        };
        if let Err(msg) = rate_check {
            warn!(service = "aiops", target = %normalized_service, action = %normalized_action, "remediation blocked by hourly rate limit");
            self.metrics.record_remediation(
                &normalized_service,
                &normalized_action,
                mode,
                "blocked_rate_limit",
            );
            self.record_audit_event(
                "remediation",
                &normalized_service,
                Some(&normalized_action),
                Some(mode),
                "blocked_rate_limit",
                msg.clone(),
                incident_id,
            );
            let rec = self.build_record(
                incident_id,
                &normalized_service,
                &normalized_action,
                target_replicas,
                mode,
                "blocked_rate_limit",
                &msg,
            );
            return (rec, Err(RemediationPolicyError::BlockedRateLimit(msg)));
        }

        // 5. Dry-run check (default true)
        if self.config.dry_run {
            let msg = match target_replicas {
                Some(r) => format!(
                    "dry-run: would scale '{normalized_service}' to {r} replicas via {} adapter",
                    self.adapter.name()
                ),
                None => format!(
                    "dry-run: would restart '{normalized_service}' via {} adapter",
                    self.adapter.name()
                ),
            };
            info!(
                service = "aiops",
                target = %normalized_service,
                action = %normalized_action,
                mode = "dry_run",
                adapter = self.adapter.name(),
                "{msg}"
            );
            self.metrics
                .record_remediation(&normalized_service, &normalized_action, mode, "ok");
            self.record_audit_event(
                "remediation",
                &normalized_service,
                Some(&normalized_action),
                Some(mode),
                "dry_run_skipped",
                msg.clone(),
                incident_id,
            );
            let rec = self.build_record(
                incident_id,
                &normalized_service,
                &normalized_action,
                target_replicas,
                mode,
                "dry_run_skipped",
                &msg,
            );
            return (rec, Ok(()));
        }

        // 6. Live execution via selected RuntimeAdapter
        let exec_res = match normalized_action.as_str() {
            "restart" => self.adapter.restart(&normalized_service).await,
            "scale" => {
                self.adapter
                    .scale(&normalized_service, target_replicas.unwrap_or(2))
                    .await
            }
            _ => unreachable!(),
        };

        match exec_res {
            Ok(detail) => {
                info!(
                    service = "aiops",
                    target = %normalized_service,
                    action = %normalized_action,
                    mode = "live",
                    adapter = self.adapter.name(),
                    detail = %detail,
                    "remediation executed"
                );
                self.metrics
                    .record_remediation(&normalized_service, &normalized_action, mode, "ok");
                self.record_audit_event(
                    "remediation",
                    &normalized_service,
                    Some(&normalized_action),
                    Some(mode),
                    "executed",
                    detail.clone(),
                    incident_id,
                );
                let rec = self.build_record(
                    incident_id,
                    &normalized_service,
                    &normalized_action,
                    target_replicas,
                    mode,
                    "executed",
                    &detail,
                );
                (rec, Ok(()))
            }
            Err(e) => {
                let detail = e.to_string();
                warn!(
                    service = "aiops",
                    target = %normalized_service,
                    action = %normalized_action,
                    mode = "live",
                    adapter = self.adapter.name(),
                    error = %detail,
                    "remediation execution failed"
                );
                self.metrics
                    .record_remediation(&normalized_service, &normalized_action, mode, "error");
                self.record_audit_event(
                    "remediation",
                    &normalized_service,
                    Some(&normalized_action),
                    Some(mode),
                    "failed",
                    detail.clone(),
                    incident_id,
                );
                let rec = self.build_record(
                    incident_id,
                    &normalized_service,
                    &normalized_action,
                    target_replicas,
                    mode,
                    "failed",
                    &detail,
                );
                (rec, Err(RemediationPolicyError::ExecutionFailed(detail)))
            }
        }
    }

    #[allow(clippy::too_many_arguments)]
    fn build_record(
        &self,
        incident_id: Option<Uuid>,
        service: &str,
        action: &str,
        replicas: Option<u32>,
        mode: &str,
        outcome: &str,
        message: &str,
    ) -> RemediationRecord {
        RemediationRecord {
            id: Uuid::new_v4(),
            incident_id,
            service: service.to_string(),
            action: action.to_string(),
            replicas,
            mode: mode.to_string(),
            adapter: self.adapter.name().to_string(),
            outcome: outcome.to_string(),
            message: message.to_string(),
            executed_at: Utc::now(),
        }
    }
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicUsize, Ordering};

    use async_trait::async_trait;

    use super::*;
    use crate::adapter::AdapterError;

    struct CountingAdapter {
        restarts: AtomicUsize,
        scales: AtomicUsize,
    }

    impl CountingAdapter {
        fn new() -> Self {
            Self {
                restarts: AtomicUsize::new(0),
                scales: AtomicUsize::new(0),
            }
        }
    }

    #[async_trait]
    impl RuntimeAdapter for CountingAdapter {
        fn name(&self) -> &'static str {
            "mock-counting"
        }

        async fn restart(&self, service: &str) -> Result<String, AdapterError> {
            self.restarts.fetch_add(1, Ordering::SeqCst);
            Ok(format!("restarted {service}"))
        }

        async fn scale(&self, service: &str, replicas: u32) -> Result<String, AdapterError> {
            self.scales.fetch_add(1, Ordering::SeqCst);
            Ok(format!("scaled {service} to {replicas}"))
        }
    }

    #[tokio::test]
    async fn test_allowlist_rate_limit_and_dry_run_default() {
        let cfg = Config::default();
        assert!(cfg.dry_run, "dry_run must default to true");
        let adapter = Arc::new(CountingAdapter::new());
        let rem = Remediator::new(cfg, adapter.clone(), Metrics::new());

        // Blocked actions
        for bad_action in ["drop_db", "exec", "rollback_migration"] {
            let (rec, res) = rem.remediate(None, "order", bad_action, None).await;
            assert!(matches!(res, Err(RemediationPolicyError::BlockedAllowlist(_))));
            assert_eq!(rec.outcome, "blocked_allowlist");
        }

        // Blocked services
        for bad_svc in ["postgres", "nats", "redis"] {
            let (rec, res) = rem.remediate(None, bad_svc, "restart", None).await;
            assert!(matches!(res, Err(RemediationPolicyError::BlockedAllowlist(_))));
            assert_eq!(rec.outcome, "blocked_allowlist");
        }

        // Dry-run allows up to max_actions_per_hour (3), never invokes adapter
        for _ in 0..3 {
            let (rec, res) = rem.remediate(None, "order", "restart", None).await;
            assert!(res.is_ok());
            assert_eq!(rec.mode, "dry_run");
            assert_eq!(rec.outcome, "dry_run_skipped");
        }
        assert_eq!(adapter.restarts.load(Ordering::SeqCst), 0);

        // 4th attempt within the hour is rate-limited
        let (rec, res) = rem.remediate(None, "order", "restart", None).await;
        assert!(matches!(res, Err(RemediationPolicyError::BlockedRateLimit(_))));
        assert_eq!(rec.outcome, "blocked_rate_limit");

        // All attempts recorded in audit log
        let audit = rem.list_audit(None, None);
        assert_eq!(audit.len(), 10);
    }
}
