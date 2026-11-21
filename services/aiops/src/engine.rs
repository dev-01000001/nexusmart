use std::collections::{HashMap, HashSet};
use std::sync::{Arc, RwLock};
use std::time::Duration;

use chrono::{Duration as ChronoDuration, Utc};
use tokio::sync::{watch, Mutex as AsyncMutex};
use tracing::{info, info_span, warn, Instrument};
use uuid::Uuid;

use crate::adapter::build_runtime_adapter;
use crate::config::Config;
use crate::context::ContextCollector;
use crate::detector::{
    Detector, PollSnapshot, METRIC_DEPENDENCY_UP, METRIC_ERROR_RATE, METRIC_HTTP_503_RATE,
    METRIC_LATENCY_P95, METRIC_SERVICE_UP,
};
use crate::http_client::ResilientHttpClient;
use crate::llm::{build_llm_provider, notify_telegram_async, LlmError, LlmProvider, RcaRequest};
use crate::metrics::Metrics;
use crate::models::{
    AlertmanagerWebhook, AnomalySignal, AuditEntry, CorrelatedAlert, DeployRecord, FeedbackRecord,
    Incident, IncidentStatus, MaintenanceWindow, RemediationRecord, ScanResult,
};
use crate::remediator::{RemediationPolicyError, Remediator};

const MAX_INCIDENTS_RETAINED: usize = 500;
const HEALTHY_POLLS_TO_RESOLVE: u32 = 2;

#[derive(Clone)]
pub struct AiopsEngine {
    pub config: Config,
    pub metrics: Metrics,
    http: ResilientHttpClient,
    detector: Arc<AsyncMutex<Detector>>,
    collector: ContextCollector,
    llm: Arc<dyn LlmProvider>,
    remediator: Arc<Remediator>,
    incidents: Arc<RwLock<Vec<Incident>>>,
    deploys: Arc<RwLock<Vec<DeployRecord>>>,
    maintenance: Arc<RwLock<HashMap<String, MaintenanceWindow>>>,
}

impl AiopsEngine {
    pub fn new(config: Config, metrics: Metrics) -> Self {
        let http = ResilientHttpClient::new(Duration::from_secs(config.http_timeout_secs));
        let llm = build_llm_provider(&config, http.clone());
        let adapter = build_runtime_adapter(&config, http.clone());
        Self::with_components(config, metrics, http, llm, adapter)
    }

    pub fn with_components(
        config: Config,
        metrics: Metrics,
        http: ResilientHttpClient,
        llm: Arc<dyn LlmProvider>,
        adapter: Arc<dyn crate::adapter::RuntimeAdapter>,
    ) -> Self {
        let detector = Detector::new(config.clone(), http.clone(), metrics.clone());
        let collector = ContextCollector::new(config.clone(), http.clone());
        let remediator = Arc::new(Remediator::new(config.clone(), adapter, metrics.clone()));

        Self {
            config,
            metrics,
            http,
            detector: Arc::new(AsyncMutex::new(detector)),
            collector,
            llm,
            remediator,
            incidents: Arc::new(RwLock::new(Vec::new())),
            deploys: Arc::new(RwLock::new(Vec::new())),
            maintenance: Arc::new(RwLock::new(HashMap::new())),
        }
    }

    pub async fn check_ready(&self) -> Result<(), String> {
        let det = self.detector.lock().await;
        det.check_prometheus_ready().await
    }

    /// Runs the periodic background polling loop until shutdown is signaled.
    pub async fn run_background_poller(self, mut shutdown: watch::Receiver<bool>) {
        let interval_secs = self.config.poll_interval_secs.max(1);
        info!(
            service = "aiops",
            poll_interval_secs = interval_secs,
            "starting AIOps background anomaly detector loop"
        );
        let mut ticker = tokio::time::interval(Duration::from_secs(interval_secs));
        ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);

        loop {
            tokio::select! {
                _ = ticker.tick() => {
                    let span = info_span!("aiops_poll_cycle", service = "aiops");
                    let _ = self.scan_once().instrument(span).await;
                }
                _ = shutdown.changed() => {
                    if *shutdown.borrow() {
                        info!(service = "aiops", "stopping AIOps background poller");
                        break;
                    }
                }
            }
        }
    }

    /// Runs a single poll + anomaly detection + context gathering + RCA + auto-resolve cycle.
    pub async fn scan_once(&self) -> ScanResult {
        let scanned_at = Utc::now();
        let snapshot = {
            let mut det = self.detector.lock().await;
            det.poll_once().await
        };

        let raw_anomaly_count = snapshot.anomalies.len();
        let mut touched_ids: Vec<Uuid> = Vec::new();
        let mut active_services: HashSet<String> = HashSet::new();

        // Filter maintenance-suppressed services
        let mut filtered_anomalies: Vec<AnomalySignal> = Vec::new();
        let mut suppressed_services: HashSet<String> = HashSet::new();
        for sig in &snapshot.anomalies {
            if self.is_in_maintenance(&sig.service) {
                if suppressed_services.insert(sig.service.clone()) {
                    self.remediator.record_audit_event(
                        "maintenance_suppressed",
                        &sig.service,
                        None,
                        None,
                        "suppressed",
                        format!(
                            "suppressed anomaly {}={:.4} during maintenance window",
                            sig.metric, sig.value
                        ),
                        None,
                    );
                }
                continue;
            }
            filtered_anomalies.push(sig.clone());
        }

        // Correlate anomalies into root-cause candidate buckets
        let buckets = correlate_anomalies(&filtered_anomalies);
        let all_deploys = self.list_deploys(None);

        for (bucket_svc, signals) in buckets {
            active_services.insert(bucket_svc.clone());
            let mut metrics_snap = snapshot
                .service_metrics
                .get(&bucket_svc)
                .cloned()
                .unwrap_or_default();
            for sig in &signals {
                metrics_snap
                    .entry(format!("{}.{}", sig.service, sig.metric))
                    .or_insert(sig.value);
                if sig.service == bucket_svc {
                    metrics_snap.insert(sig.metric.clone(), sig.value);
                }
            }

            // Check if an open incident already exists for this bucket service
            let existing = self.find_open_incident_for(&bucket_svc, &signals);
            if let Some((existing_id, needs_reanalysis)) = existing {
                if needs_reanalysis {
                    let context = self
                        .collector
                        .gather(
                            &bucket_svc,
                            &all_deploys,
                            snapshot.dependencies.clone(),
                            snapshot.service_up.clone(),
                        )
                        .await;
                    let rca_req = RcaRequest {
                        service: bucket_svc.clone(),
                        anomalies: signals.clone(),
                        metrics_snapshot: metrics_snap.clone(),
                        context: context.clone(),
                    };
                    let rca_eval = self.run_rca(&rca_req).await;
                    let mut guard = self.incidents.write().expect("incidents write lock");
                    if let Some(inc) = guard.iter_mut().find(|i| i.id == existing_id) {
                        inc.updated_at = scanned_at;
                        inc.healthy_polls = 0;
                        inc.anomalies = signals;
                        inc.metrics_snapshot.extend(metrics_snap);
                        inc.context = context;
                        match rca_eval {
                            Ok(rca) => {
                                inc.root_cause_service = rca.root_cause_service.clone();
                                inc.category = rca.category.clone();
                                inc.rca = Some(rca);
                                inc.rca_error = None;
                            }
                            Err(err) => {
                                inc.rca_error = Some(err);
                            }
                        }
                        active_services.insert(inc.root_cause_service.clone());
                    }
                } else {
                    let mut guard = self.incidents.write().expect("incidents write lock");
                    if let Some(inc) = guard.iter_mut().find(|i| i.id == existing_id) {
                        inc.updated_at = scanned_at;
                        inc.healthy_polls = 0;
                        inc.anomalies = signals;
                        inc.metrics_snapshot.extend(metrics_snap);
                        active_services.insert(inc.root_cause_service.clone());
                    }
                }
                touched_ids.push(existing_id);
                continue;
            }

            // Gather full context + run RCA for new incident
            let context = self
                .collector
                .gather(
                    &bucket_svc,
                    &all_deploys,
                    snapshot.dependencies.clone(),
                    snapshot.service_up.clone(),
                )
                .await;
            let rca_req = RcaRequest {
                service: bucket_svc.clone(),
                anomalies: signals.clone(),
                metrics_snapshot: metrics_snap.clone(),
                context: context.clone(),
            };

            let (rca_opt, rca_err, root_cause_service, category) =
                match self.run_rca(&rca_req).await {
                    Ok(rca) => {
                        let rc_svc = rca.root_cause_service.clone();
                        let cat = rca.category.clone();
                        (Some(rca), None, rc_svc, cat)
                    }
                    Err(err) => (None, Some(err), bucket_svc.clone(), "rca_error".to_string()),
                };

            active_services.insert(root_cause_service.clone());

            // Secondary deduplication if RCA resolved to a root_cause_service that already has an open incident
            if let Some((dedup_id, _)) = self.find_open_incident_for(&root_cause_service, &signals)
            {
                let mut guard = self.incidents.write().expect("incidents write lock");
                if let Some(inc) = guard.iter_mut().find(|i| i.id == dedup_id) {
                    inc.updated_at = scanned_at;
                    inc.healthy_polls = 0;
                    for s in signals {
                        if !inc
                            .anomalies
                            .iter()
                            .any(|e| e.service == s.service && e.metric == s.metric)
                        {
                            inc.anomalies.push(s);
                        }
                    }
                    inc.metrics_snapshot.extend(metrics_snap);
                }
                touched_ids.push(dedup_id);
                continue;
            }

            let incident_id = Uuid::new_v4();
            let auto_action = if self.config.auto_remediate {
                rca_opt
                    .as_ref()
                    .and_then(|r| r.recommended_action.as_ref())
                    .filter(|a| a.auto_executable)
                    .cloned()
            } else {
                None
            };

            let mut incident = Incident {
                id: incident_id,
                service: bucket_svc.clone(),
                root_cause_service: root_cause_service.clone(),
                category: category.clone(),
                status: IncidentStatus::Open,
                opened_at: scanned_at,
                updated_at: scanned_at,
                resolved_at: None,
                anomalies: signals,
                metrics_snapshot: metrics_snap,
                context,
                rca: rca_opt,
                rca_error: rca_err,
                feedback: Vec::new(),
                remediations: Vec::new(),
                correlated_alerts: Vec::new(),
                healthy_polls: 0,
            };

            if let Some(rec_act) = auto_action {
                let (record, _) = self
                    .remediator
                    .remediate(
                        Some(incident_id),
                        &rec_act.service,
                        &rec_act.action,
                        rec_act.replicas,
                    )
                    .await;
                incident.remediations.push(record);
            }

            info!(
                service = "aiops",
                incident_id = %incident.id,
                target_service = %incident.service,
                root_cause_service = %incident.root_cause_service,
                category = %incident.category,
                "opened new AIOps incident"
            );

            notify_telegram_async(
                self.config.telegram_webhook_url.clone(),
                self.http.clone(),
                incident.clone(),
            );

            {
                let mut guard = self.incidents.write().expect("incidents write lock");
                guard.push(incident);
                trim_incidents(&mut guard);
            }
            touched_ids.push(incident_id);
        }

        // Auto-resolve open incidents whose services remained healthy for 2 consecutive polls
        self.evaluate_auto_resolve(&snapshot, &active_services, scanned_at);
        self.refresh_open_incidents_metric();

        let touched_incidents = {
            let guard = self.incidents.read().expect("incidents read lock");
            touched_ids
                .iter()
                .filter_map(|id| guard.iter().find(|i| i.id == *id).cloned())
                .collect()
        };

        ScanResult {
            scanned_at,
            anomalies_detected: raw_anomaly_count,
            dependencies: snapshot.dependencies,
            incidents: touched_incidents,
        }
    }

    async fn run_rca(&self, req: &RcaRequest) -> Result<crate::models::RcaResult, String> {
        let mode = self.llm.mode();
        match self.llm.analyze(req).await {
            Ok(outcome) => {
                self.metrics.record_llm_call(mode, outcome.cached, "ok");
                Ok(outcome.result)
            }
            Err(LlmError::BudgetExceeded(msg)) => {
                self.metrics.record_llm_call(mode, false, "budget_exceeded");
                warn!(service = "aiops", mode, error = %msg, "LLM daily budget exceeded; preserving raw incident data");
                Err(format!("LLM budget exceeded: {msg}"))
            }
            Err(e) => {
                self.metrics.record_llm_call(mode, false, "error");
                warn!(service = "aiops", mode, error = %e, "LLM RCA failed; preserving raw incident data");
                Err(e.to_string())
            }
        }
    }

    fn evaluate_auto_resolve(
        &self,
        snapshot: &PollSnapshot,
        active_services: &HashSet<String>,
        now: chrono::DateTime<Utc>,
    ) {
        let mut guard = self.incidents.write().expect("incidents write lock");
        for inc in guard.iter_mut() {
            if inc.status != IncidentStatus::Open {
                continue;
            }
            let still_anomalous = active_services.contains(&inc.service)
                || active_services.contains(&inc.root_cause_service);
            let dep_still_down = snapshot
                .dependencies
                .get(&inc.root_cause_service)
                .is_some_and(|up| !*up);
            let svc_still_down = snapshot
                .service_up
                .get(&inc.root_cause_service)
                .is_some_and(|up| !*up);

            if still_anomalous || dep_still_down || svc_still_down {
                inc.healthy_polls = 0;
            } else {
                inc.healthy_polls = inc.healthy_polls.saturating_add(1);
                if inc.healthy_polls >= HEALTHY_POLLS_TO_RESOLVE {
                    inc.status = IncidentStatus::Resolved;
                    inc.resolved_at = Some(now);
                    inc.updated_at = now;
                    info!(
                        service = "aiops",
                        incident_id = %inc.id,
                        root_cause_service = %inc.root_cause_service,
                        "auto-resolved incident after consecutive healthy polls"
                    );
                }
            }
        }
    }

    fn find_open_incident_for(
        &self,
        service: &str,
        incoming_signals: &[AnomalySignal],
    ) -> Option<(Uuid, bool)> {
        let guard = self.incidents.read().expect("incidents read lock");
        guard
            .iter()
            .rev()
            .find(|i| {
                i.status == IncidentStatus::Open
                    && (i.service == service || i.root_cause_service == service)
            })
            .map(|i| {
                let has_new_metric = incoming_signals.iter().any(|s| {
                    !i.anomalies
                        .iter()
                        .any(|e| e.service == s.service && e.metric == s.metric)
                });
                let needs_reanalysis = has_new_metric
                    || i.rca.is_none()
                    || i.rca.as_ref().is_some_and(|r| r.insufficient_data)
                    || i.category == "unknown"
                    || i.category == "rca_error";
                (i.id, needs_reanalysis)
            })
    }

    fn refresh_open_incidents_metric(&self) {
        let guard = self.incidents.read().expect("incidents read lock");
        let open = guard
            .iter()
            .filter(|i| i.status == IncidentStatus::Open)
            .count() as i64;
        self.metrics.set_open_incidents(open);
    }

    pub fn list_incidents(&self, service: Option<&str>, status: Option<&str>) -> Vec<Incident> {
        let guard = self.incidents.read().expect("incidents read lock");
        let mut items: Vec<Incident> = guard
            .iter()
            .filter(|i| {
                service.is_none_or(|s| {
                    i.service.eq_ignore_ascii_case(s)
                        || i.root_cause_service.eq_ignore_ascii_case(s)
                })
            })
            .filter(|i| {
                status.is_none_or(|st| match st.to_ascii_lowercase().as_str() {
                    "open" => i.status == IncidentStatus::Open,
                    "resolved" => i.status == IncidentStatus::Resolved,
                    _ => true,
                })
            })
            .cloned()
            .collect();
        items.sort_by_key(|a| std::cmp::Reverse(a.opened_at));
        items
    }

    pub fn get_incident(&self, id: Uuid) -> Option<Incident> {
        let guard = self.incidents.read().expect("incidents read lock");
        guard.iter().find(|i| i.id == id).cloned()
    }

    pub fn submit_feedback(&self, id: Uuid, correct: bool, note: String) -> Option<Incident> {
        let mut guard = self.incidents.write().expect("incidents write lock");
        let inc = guard.iter_mut().find(|i| i.id == id)?;
        let now = Utc::now();
        inc.feedback.push(FeedbackRecord {
            correct,
            note: note.clone(),
            submitted_at: now,
        });
        inc.updated_at = now;
        let cloned = inc.clone();
        drop(guard);

        self.remediator.record_audit_event(
            "rca_feedback",
            &cloned.root_cause_service,
            None,
            None,
            if correct {
                "confirmed_correct"
            } else {
                "marked_incorrect"
            },
            note,
            Some(id),
        );
        Some(cloned)
    }

    pub async fn remediate_incident(
        &self,
        id: Uuid,
        service_override: Option<String>,
        action: &str,
        replicas: Option<u32>,
    ) -> Option<(RemediationRecord, Result<(), RemediationPolicyError>)> {
        let target_service = {
            let guard = self.incidents.read().expect("incidents read lock");
            let inc = guard.iter().find(|i| i.id == id)?;
            service_override.unwrap_or_else(|| inc.root_cause_service.clone())
        };

        let (record, res) = self
            .remediator
            .remediate(Some(id), &target_service, action, replicas)
            .await;

        {
            let mut guard = self.incidents.write().expect("incidents write lock");
            if let Some(inc) = guard.iter_mut().find(|i| i.id == id) {
                inc.remediations.push(record.clone());
                inc.updated_at = Utc::now();
            }
        }

        Some((record, res))
    }

    pub fn record_deploy(&self, deploy: DeployRecord) -> DeployRecord {
        let mut guard = self.deploys.write().expect("deploys write lock");
        guard.push(deploy.clone());
        if guard.len() > 500 {
            let excess = guard.len() - 500;
            guard.drain(0..excess);
        }
        drop(guard);
        self.remediator.record_audit_event(
            "deploy_recorded",
            &deploy.service,
            None,
            None,
            "recorded",
            format!(
                "version={} image={} has_db_migration={}",
                deploy.version, deploy.image, deploy.has_db_migration
            ),
            None,
        );
        deploy
    }

    pub fn list_deploys(&self, service: Option<&str>) -> Vec<DeployRecord> {
        let guard = self.deploys.read().expect("deploys read lock");
        let mut out: Vec<DeployRecord> = guard
            .iter()
            .filter(|d| service.is_none_or(|s| d.service.eq_ignore_ascii_case(s)))
            .cloned()
            .collect();
        out.sort_by_key(|a| std::cmp::Reverse(a.deployed_at));
        out
    }

    pub fn list_audit(&self, service: Option<&str>, outcome: Option<&str>) -> Vec<AuditEntry> {
        self.remediator.list_audit(service, outcome)
    }

    pub fn set_maintenance(
        &self,
        service: String,
        active: bool,
        reason: String,
        duration_minutes: Option<i64>,
        until: Option<chrono::DateTime<Utc>>,
    ) -> MaintenanceWindow {
        let now = Utc::now();
        let effective_until = if active {
            until
                .or_else(|| duration_minutes.map(|mins| now + ChronoDuration::minutes(mins.max(1))))
        } else {
            None
        };
        let window = MaintenanceWindow {
            service: service.trim().to_ascii_lowercase(),
            active,
            reason: reason.clone(),
            set_at: now,
            until: effective_until,
        };
        {
            let mut guard = self.maintenance.write().expect("maintenance write lock");
            if active {
                guard.insert(window.service.clone(), window.clone());
            } else {
                guard.remove(&window.service);
            }
        }
        self.remediator.record_audit_event(
            "maintenance_window",
            &window.service,
            None,
            None,
            if active { "enabled" } else { "cleared" },
            reason,
            None,
        );
        window
    }

    pub fn list_maintenance(&self) -> Vec<MaintenanceWindow> {
        let now = Utc::now();
        let mut guard = self.maintenance.write().expect("maintenance write lock");
        guard.retain(|_, w| w.active && w.until.is_none_or(|u| u > now));
        guard.values().cloned().collect()
    }

    pub fn is_in_maintenance(&self, service: &str) -> bool {
        let key = service.trim().to_ascii_lowercase();
        let now = Utc::now();
        let guard = self.maintenance.read().expect("maintenance read lock");
        guard
            .get(&key)
            .is_some_and(|w| w.active && w.until.is_none_or(|u| u > now))
    }

    pub async fn handle_alertmanager_webhook(
        &self,
        payload: AlertmanagerWebhook,
    ) -> (usize, Vec<Uuid>) {
        let now = Utc::now();
        let mut processed = 0usize;
        let mut incident_ids = Vec::new();

        for alert in payload.alerts {
            let alert_status = if alert.status.is_empty() {
                payload.status.clone()
            } else {
                alert.status.clone()
            };
            let service = alert
                .labels
                .get("service")
                .or_else(|| alert.labels.get("dependency"))
                .cloned()
                .unwrap_or_else(|| "unknown".to_string())
                .to_ascii_lowercase();
            let alertname = alert
                .labels
                .get("alertname")
                .cloned()
                .unwrap_or_else(|| "AlertmanagerAlert".to_string());
            let summary = alert
                .annotations
                .get("summary")
                .or_else(|| alert.annotations.get("description"))
                .cloned();

            if alert_status.eq_ignore_ascii_case("firing") && self.is_in_maintenance(&service) {
                self.remediator.record_audit_event(
                    "maintenance_suppressed",
                    &service,
                    None,
                    None,
                    "suppressed",
                    format!("suppressed Alertmanager alert '{alertname}' during maintenance"),
                    None,
                );
                continue;
            }

            let correlated = CorrelatedAlert {
                alertname: alertname.clone(),
                status: alert_status.clone(),
                fingerprint: alert.fingerprint.clone(),
                summary: summary.clone(),
                received_at: now,
            };

            processed += 1;

            if alert_status.eq_ignore_ascii_case("resolved") {
                let mut guard = self.incidents.write().expect("incidents write lock");
                if let Some(inc) = guard.iter_mut().rev().find(|i| {
                    i.status == IncidentStatus::Open
                        && (i.service == service || i.root_cause_service == service)
                }) {
                    inc.correlated_alerts.push(correlated);
                    inc.updated_at = now;
                    if inc.anomalies.is_empty() {
                        inc.status = IncidentStatus::Resolved;
                        inc.resolved_at = Some(now);
                    }
                    incident_ids.push(inc.id);
                }
                continue;
            }

            // Firing alert: correlate with existing open incident; do not open empty
            // `anomalies: []` phantom incidents without verified metric evidence.
            if let Some((existing_id, _)) = self.find_open_incident_for(&service, &[]) {
                let mut guard = self.incidents.write().expect("incidents write lock");
                if let Some(inc) = guard.iter_mut().find(|i| i.id == existing_id) {
                    inc.correlated_alerts.push(correlated);
                    inc.updated_at = now;
                    inc.healthy_polls = 0;
                }
                incident_ids.push(existing_id);
                continue;
            }

            self.remediator.record_audit_event(
                "alertmanager_webhook",
                &service,
                None,
                None,
                "recorded_no_open_incident",
                format!(
                    "alert={} status={} summary={}",
                    alertname,
                    alert_status,
                    summary.as_deref().unwrap_or("")
                ),
                None,
            );
        }

        self.refresh_open_incidents_metric();
        (processed, incident_ids)
    }
}

/// Groups raw anomalies from a single poll cycle by likely root-cause service,
/// preventing gateway cascade symptoms from splitting away from backend faults.
fn correlate_anomalies(anomalies: &[AnomalySignal]) -> Vec<(String, Vec<AnomalySignal>)> {
    if anomalies.is_empty() {
        return Vec::new();
    }

    // Priority 1: NATS broker outage correlates downstream error/disconnect symptoms
    let has_nats_down = anomalies
        .iter()
        .any(|a| a.service == "nats" && a.metric == METRIC_DEPENDENCY_UP && a.value < 0.5);
    if has_nats_down {
        let mut nats_bucket = Vec::new();
        let mut others: HashMap<String, Vec<AnomalySignal>> = HashMap::new();
        for a in anomalies {
            if a.service == "nats"
                || a.metric == METRIC_ERROR_RATE
                || a.metric == METRIC_HTTP_503_RATE
            {
                nats_bucket.push(a.clone());
            } else {
                others.entry(a.service.clone()).or_default().push(a.clone());
            }
        }
        let mut res = vec![("nats".to_string(), nats_bucket)];
        for (svc, list) in others {
            res.push((svc, list));
        }
        return res;
    }

    // Separate gateway anomalies from backend anomalies
    let mut backend_buckets: HashMap<String, Vec<AnomalySignal>> = HashMap::new();
    let mut gateway_anomalies: Vec<AnomalySignal> = Vec::new();

    for a in anomalies {
        if a.service == "gateway" {
            gateway_anomalies.push(a.clone());
        } else {
            backend_buckets
                .entry(a.service.clone())
                .or_default()
                .push(a.clone());
        }
    }

    if !gateway_anomalies.is_empty() && !backend_buckets.is_empty() {
        // Prefer attaching gateway cascade errors/latency to a down backend service first.
        let down_backend = backend_buckets
            .iter()
            .find(|(_, list)| {
                list.iter()
                    .any(|a| a.metric == METRIC_SERVICE_UP && a.value < 0.5)
            })
            .map(|(svc, _)| svc.clone());

        let target = down_backend.or_else(|| {
            // Otherwise if there is a single backend experiencing errors or latency, correlate gateway with it.
            let err_or_lat_backends: Vec<String> = backend_buckets
                .iter()
                .filter(|(_, list)| {
                    list.iter().any(|a| {
                        matches!(
                            a.metric.as_str(),
                            METRIC_ERROR_RATE | METRIC_HTTP_503_RATE | METRIC_LATENCY_P95
                        )
                    })
                })
                .map(|(svc, _)| svc.clone())
                .collect();
            if err_or_lat_backends.len() == 1 {
                Some(err_or_lat_backends[0].clone())
            } else {
                None
            }
        });

        if let Some(svc) = target {
            if let Some(list) = backend_buckets.get_mut(&svc) {
                list.extend(gateway_anomalies);
                gateway_anomalies = Vec::new();
            }
        }
    }

    let mut out: Vec<(String, Vec<AnomalySignal>)> = backend_buckets.into_iter().collect();
    if !gateway_anomalies.is_empty() {
        out.push(("gateway".to_string(), gateway_anomalies));
    }
    // Sort deterministically so results are predictable
    out.sort_by(|a, b| a.0.cmp(&b.0));
    out
}

fn trim_incidents(incidents: &mut Vec<Incident>) {
    if incidents.len() <= MAX_INCIDENTS_RETAINED {
        return;
    }
    let excess = incidents.len() - MAX_INCIDENTS_RETAINED;
    let mut removed = 0usize;
    incidents.retain(|inc| {
        if removed < excess && inc.status == IncidentStatus::Resolved {
            removed += 1;
            false
        } else {
            true
        }
    });
}
