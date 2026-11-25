use std::collections::HashMap;

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum IncidentStatus {
    Open,
    Resolved,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AnomalySignal {
    pub service: String,
    pub metric: String,
    pub value: f64,
    pub mean: f64,
    pub std_dev: f64,
    pub z_score: f64,
    pub ewma: f64,
    pub ewma_deviation: f64,
    pub threshold_breached: bool,
    pub statistical_breached: bool,
    pub reason: String,
    pub observed_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LokiLogEntry {
    pub timestamp: String,
    pub service: String,
    pub line: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TempoTraceSummary {
    pub trace_id: String,
    pub root_service_name: String,
    pub root_trace_name: String,
    pub duration_ms: u64,
    pub start_time_unix_nano: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DeployRecord {
    pub id: Uuid,
    pub service: String,
    pub version: String,
    pub image: String,
    pub deployed_at: DateTime<Utc>,
    #[serde(default)]
    pub commit: Option<String>,
    #[serde(default)]
    pub has_db_migration: bool,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct GatheredContext {
    pub logs: Vec<LokiLogEntry>,
    pub slow_traces: Vec<TempoTraceSummary>,
    pub recent_deploys: Vec<DeployRecord>,
    pub dependencies: HashMap<String, bool>,
    pub service_up: HashMap<String, bool>,
    pub errors: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RecommendedAction {
    pub action: String,
    pub service: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub replicas: Option<u32>,
    pub auto_executable: bool,
    pub reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RcaResult {
    pub root_cause_service: String,
    pub category: String,
    pub summary: String,
    pub confidence: f64,
    pub evidence: Vec<String>,
    pub recommended_action: Option<RecommendedAction>,
    pub insufficient_data: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FeedbackRecord {
    pub correct: bool,
    pub note: String,
    pub submitted_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RemediationRecord {
    pub id: Uuid,
    pub incident_id: Option<Uuid>,
    pub service: String,
    pub action: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub replicas: Option<u32>,
    pub mode: String,
    pub adapter: String,
    pub outcome: String,
    pub message: String,
    pub executed_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AuditEntry {
    pub id: Uuid,
    pub timestamp: DateTime<Utc>,
    pub event_type: String,
    pub service: String,
    pub action: Option<String>,
    pub mode: Option<String>,
    pub outcome: String,
    pub detail: String,
    pub incident_id: Option<Uuid>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MaintenanceWindow {
    pub service: String,
    pub active: bool,
    pub reason: String,
    pub set_at: DateTime<Utc>,
    pub until: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CorrelatedAlert {
    pub alertname: String,
    pub status: String,
    pub fingerprint: Option<String>,
    pub summary: Option<String>,
    pub received_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Incident {
    pub id: Uuid,
    pub service: String,
    pub root_cause_service: String,
    pub category: String,
    pub status: IncidentStatus,
    pub opened_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub resolved_at: Option<DateTime<Utc>>,
    pub anomalies: Vec<AnomalySignal>,
    pub metrics_snapshot: HashMap<String, f64>,
    pub context: GatheredContext,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub rca: Option<RcaResult>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub rca_error: Option<String>,
    pub feedback: Vec<FeedbackRecord>,
    pub remediations: Vec<RemediationRecord>,
    pub correlated_alerts: Vec<CorrelatedAlert>,
    #[serde(skip)]
    pub healthy_polls: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AlertmanagerWebhook {
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub alerts: Vec<AlertmanagerAlert>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AlertmanagerAlert {
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub labels: HashMap<String, String>,
    #[serde(default)]
    pub annotations: HashMap<String, String>,
    #[serde(rename = "startsAt", default)]
    pub starts_at: Option<String>,
    #[serde(rename = "endsAt", default)]
    pub ends_at: Option<String>,
    #[serde(default)]
    pub fingerprint: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ScanResult {
    pub scanned_at: DateTime<Utc>,
    pub anomalies_detected: usize,
    pub dependencies: HashMap<String, bool>,
    pub incidents: Vec<Incident>,
}
