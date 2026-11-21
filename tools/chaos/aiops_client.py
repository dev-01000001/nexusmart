"""AIOps HTTP client, background HTTP traffic generator, and RCA evaluation logic."""

from __future__ import annotations

import json
import threading
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Callable, Optional, Sequence


def utc_now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def parse_iso_timestamp(value: Optional[str]) -> Optional[float]:
    """Parse an ISO-8601 timestamp string into a UTC UNIX epoch float."""
    if not value or not isinstance(value, str):
        return None
    text = value.strip()
    if text.endswith("Z"):
        text = text[:-1] + "+00:00"
    try:
        dt = datetime.fromisoformat(text)
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.timestamp()


def http_request_status(
    url: str,
    *,
    method: str = "GET",
    headers: Optional[dict[str, str]] = None,
    body: Optional[bytes] = None,
    timeout: float = 5.0,
) -> tuple[Optional[int], bytes, Optional[str]]:
    """Perform a stdlib HTTP request returning (status_code, body_bytes, error_str)."""
    req = urllib.request.Request(url, data=body, method=method)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return int(resp.status), resp.read(), None
    except urllib.error.HTTPError as exc:
        try:
            payload = exc.read()
        except Exception:
            payload = b""
        return int(exc.code), payload, None
    except Exception as exc:
        return None, b"", str(exc)


class AiopsClient:
    """Client for NexusMart AIOps Engine HTTP API (default http://127.0.0.1:8092)."""

    def __init__(self, base_url: str = "http://127.0.0.1:8092", *, timeout: float = 10.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout

    def healthz(self) -> tuple[bool, dict[str, Any]]:
        status, body, err = http_request_status(f"{self.base_url}/healthz", timeout=self.timeout)
        if err or status is None:
            return False, {"error": err or "unreachable"}
        try:
            parsed = json.loads(body.decode("utf-8")) if body else {}
        except json.JSONDecodeError:
            parsed = {"raw": body.decode("utf-8", errors="replace")}
        return status == 200, parsed

    def trigger_scan(self) -> tuple[bool, Any]:
        status, body, err = http_request_status(
            f"{self.base_url}/api/v1/scan",
            method="POST",
            headers={"Content-Type": "application/json"},
            body=b"{}",
            timeout=max(self.timeout, 15.0),
        )
        if err or status is None:
            return False, {"error": err or "unreachable"}
        try:
            parsed = json.loads(body.decode("utf-8")) if body else {}
        except json.JSONDecodeError:
            parsed = {"raw": body.decode("utf-8", errors="replace")}
        return 200 <= status < 300, parsed

    def list_incidents(
        self,
        *,
        service: Optional[str] = None,
        status_filter: Optional[str] = None,
    ) -> list[dict[str, Any]]:
        params: list[str] = []
        if service:
            params.append(f"service={service}")
        if status_filter:
            params.append(f"status={status_filter}")
        query = f"?{'&'.join(params)}" if params else ""
        status, body, err = http_request_status(
            f"{self.base_url}/api/v1/incidents{query}",
            timeout=self.timeout,
        )
        if err or status is None or status >= 400:
            raise RuntimeError(
                f"GET {self.base_url}/api/v1/incidents failed: status={status} err={err}"
            )
        if not body:
            return []
        data = json.loads(body.decode("utf-8"))
        if isinstance(data, list):
            return [item for item in data if isinstance(item, dict)]
        if isinstance(data, dict):
            for key in ("incidents", "items", "data"):
                val = data.get(key)
                if isinstance(val, list):
                    return [item for item in val if isinstance(item, dict)]
        return []


def extract_incident_fields(incident: dict[str, Any]) -> dict[str, Any]:
    """Normalize incident JSON from AIOps into a canonical flat dictionary."""
    rca = incident.get("rca") if isinstance(incident.get("rca"), dict) else {}
    if not rca and isinstance(incident.get("root_cause"), dict):
        rca = incident["root_cause"]

    inc_id = str(incident.get("id") or incident.get("incident_id") or "")
    service = str(incident.get("service") or "").strip().lower()
    status = str(incident.get("status") or "open").strip().lower()

    root_cause_service = str(
        rca.get("root_cause_service")
        or incident.get("root_cause_service")
        or ""
    ).strip().lower()
    # Fall back to top-level service ONLY if RCA did not specify root_cause_service
    # and RCA is not explicitly flagged insufficient_data.
    insufficient_data = bool(
        rca.get("insufficient_data", incident.get("insufficient_data", False))
    )
    if not root_cause_service and not insufficient_data:
        root_cause_service = service

    category = str(
        rca.get("category")
        or incident.get("category")
        or ""
    ).strip().lower()
    summary = str(
        rca.get("summary")
        or incident.get("summary")
        or ""
    ).strip()

    raw_conf = rca.get("confidence", incident.get("confidence", 0.0))
    try:
        confidence = float(raw_conf)
    except (TypeError, ValueError):
        confidence = 0.0

    opened_at = (
        incident.get("opened_at")
        or incident.get("created_at")
        or incident.get("detected_at")
        or ""
    )
    updated_at = incident.get("updated_at") or opened_at or ""

    return {
        "id": inc_id,
        "service": service,
        "status": status,
        "root_cause_service": root_cause_service,
        "category": category,
        "summary": summary,
        "confidence": confidence,
        "insufficient_data": insufficient_data,
        "opened_at": str(opened_at),
        "updated_at": str(updated_at),
    }


def is_incident_open(status: str) -> bool:
    return status.strip().lower() not in ("resolved", "closed", "suppressed")


_CATEGORY_SPECIFICITY = {
    "message_broker_outage": 5,
    "broker_outage": 5,
    "service_unavailable": 4,
    "service_down": 4,
    "database_error": 4,
    "db_error": 4,
    "latency_degradation": 3,
    "high_latency": 3,
    "cpu_saturation": 3,
    "high_cpu": 3,
    "memory_pressure": 3,
    "post_deploy_regression": 3,
    "high_error_rate": 2,
    "unknown": 0,
}


def select_new_incident(
    incidents: Sequence[dict[str, Any]],
    *,
    baseline_snapshots: dict[str, tuple[str, str]],
    fault_started_epoch: float,
) -> tuple[Optional[dict[str, Any]], list[str]]:
    """Find newly opened (or freshly re-triggered) active incidents without ground-truth bias.

    `baseline_snapshots` maps incident_id -> (status, updated_at) recorded right before fault injection.
    Returns (selected_normalized_incident, all_new_incident_ids).
    """
    candidates: list[dict[str, Any]] = []
    for raw in incidents:
        norm = extract_incident_fields(raw)
        inc_id = norm["id"]
        if not inc_id or not is_incident_open(norm["status"]):
            continue

        prev = baseline_snapshots.get(inc_id)
        opened_epoch = parse_iso_timestamp(norm["opened_at"])
        updated_epoch = parse_iso_timestamp(norm["updated_at"])

        is_new = False
        if prev is None:
            # Brand-new incident ID; ensure it wasn't an old open incident missed in baseline.
            if opened_epoch is None or opened_epoch >= (fault_started_epoch - 5.0):
                is_new = True
            elif updated_epoch is not None and updated_epoch >= (fault_started_epoch - 2.0):
                is_new = True
        else:
            prev_status, _prev_updated = prev
            # Only treat a baseline incident as newly triggered if it was previously resolved
            # and re-opened; an incident already open before fault injection is stale carry-over.
            if not is_incident_open(prev_status):
                is_new = True

        if is_new:
            candidates.append(norm)

    if not candidates:
        return None, []

    all_ids = [c["id"] for c in candidates]

    def _rank_key(item: dict[str, Any]) -> tuple[int, float, int, float]:
        has_conclusion = 0 if item["insufficient_data"] else 1
        specificity = _CATEGORY_SPECIFICITY.get(item["category"], 1)
        opened_ts = parse_iso_timestamp(item["opened_at"]) or 0.0
        # Prefer conclusive RCA, higher confidence, more specific root-cause category, earliest detection.
        return (has_conclusion, item["confidence"], specificity, -opened_ts)

    selected = max(candidates, key=_rank_key)
    return selected, all_ids


@dataclass(frozen=True)
class RcaExpectation:
    """Ground-truth RCA criteria for a chaos scenario."""

    expected_service: str
    accepted_services: tuple[str, ...]
    expected_categories: tuple[str, ...]
    allow_empty_category: bool = True


SCENARIO_RCA_EXPECTATIONS: dict[str, RcaExpectation] = {
    "slow-payment": RcaExpectation(
        expected_service="payment",
        accepted_services=("payment", "payment-service", "nexus_ecom-payment-1"),
        expected_categories=(
            "latency_degradation",
            "high_latency",
            "latency",
            "slow_response",
            "performance_degradation",
        ),
    ),
    "stop-order": RcaExpectation(
        expected_service="order",
        accepted_services=("order", "order-service", "nexus_ecom-order-1"),
        expected_categories=(
            "service_unavailable",
            "service_down",
            "instance_down",
            "availability",
            "outage",
        ),
    ),
    "promotion-db-error": RcaExpectation(
        expected_service="promotion",
        accepted_services=(
            "promotion",
            "promotion-db",
            "promotion_db",
            "promotion-service",
            "nexus_ecom-promotion-1",
        ),
        expected_categories=(
            "database_error",
            "db_error",
            "db_connection_error",
            "dependency_error",
            "high_error_rate",
            "error_rate",
        ),
    ),
    "nats-outage": RcaExpectation(
        expected_service="nats",
        accepted_services=("nats", "nats-broker", "message-broker", "nexus_ecom-nats-1"),
        expected_categories=(
            "message_broker_outage",
            "broker_outage",
            "dependency_down",
            "dependency_outage",
            "service_unavailable",
            "service_down",
        ),
    ),
    "search-high-cpu": RcaExpectation(
        expected_service="search",
        accepted_services=("search", "search-service", "e-test-search", "nexus_ecom-search-1"),
        expected_categories=(
            "cpu_saturation",
            "high_cpu",
            "cpu_exhaustion",
            "resource_saturation",
        ),
    ),
}


@dataclass
class RcaEvaluation:
    expected_service: str
    expected_categories: list[str]
    predicted_service: str
    predicted_category: str
    service_match: bool
    category_match: bool
    rca_match: bool
    reason: str


def evaluate_rca_match(
    scenario_name: str,
    predicted_service: Optional[str],
    predicted_category: Optional[str],
    *,
    insufficient_data: bool = False,
) -> RcaEvaluation:
    """Strictly evaluate whether an AIOps RCA prediction matches scenario ground truth."""
    expectation = SCENARIO_RCA_EXPECTATIONS.get(scenario_name)
    if expectation is None:
        raise ValueError(f"Unknown scenario for RCA evaluation: {scenario_name}")

    pred_svc = (predicted_service or "").strip().lower()
    pred_cat = (predicted_category or "").strip().lower()

    if insufficient_data:
        return RcaEvaluation(
            expected_service=expectation.expected_service,
            expected_categories=list(expectation.expected_categories),
            predicted_service=pred_svc,
            predicted_category=pred_cat,
            service_match=False,
            category_match=False,
            rca_match=False,
            reason="AIOps flagged insufficient_data=true",
        )

    service_match = pred_svc in expectation.accepted_services
    if pred_cat:
        category_match = pred_cat in expectation.expected_categories
    else:
        category_match = expectation.allow_empty_category

    rca_match = service_match and category_match
    if rca_match:
        reason = "matched expected service and failure category"
    elif not service_match and not category_match:
        reason = (
            f"service mismatch (got '{pred_svc or 'none'}', expected '{expectation.expected_service}') "
            f"and category mismatch (got '{pred_cat or 'none'}')"
        )
    elif not service_match:
        reason = f"service mismatch (got '{pred_svc or 'none'}', expected '{expectation.expected_service}')"
    else:
        reason = (
            f"category mismatch (got '{pred_cat}', "
            f"expected one of {list(expectation.expected_categories)})"
        )

    return RcaEvaluation(
        expected_service=expectation.expected_service,
        expected_categories=list(expectation.expected_categories),
        predicted_service=pred_svc,
        predicted_category=pred_cat,
        service_match=service_match,
        category_match=category_match,
        rca_match=rca_match,
        reason=reason,
    )


@dataclass
class TrafficTarget:
    url: str
    method: str = "GET"
    interval_secs: float = 0.5
    workers: int = 1
    timeout_secs: float = 4.0
    headers: dict[str, str] = field(default_factory=dict)


class LoadGenerator:
    """Thread-based stdlib background HTTP traffic generator with clean stop + stats."""

    def __init__(
        self,
        targets: Sequence[TrafficTarget],
        *,
        dry_run: bool = False,
        request_fn: Callable[..., tuple[Optional[int], bytes, Optional[str]]] = http_request_status,
    ) -> None:
        self.targets = list(targets)
        self.dry_run = dry_run
        self._request_fn = request_fn
        self._stop_event = threading.Event()
        self._threads: list[threading.Thread] = []
        self._lock = threading.Lock()
        self._total = 0
        self._errors = 0
        self._status_codes: dict[str, int] = {}

    def _record(self, status: Optional[int], err: Optional[str]) -> None:
        with self._lock:
            self._total += 1
            if err or status is None or status >= 500:
                self._errors += 1
            key = str(status) if status is not None else "network_error"
            self._status_codes[key] = self._status_codes.get(key, 0) + 1

    def _worker_loop(self, target: TrafficTarget, initial_delay: float) -> None:
        if initial_delay > 0 and self._stop_event.wait(initial_delay):
            return
        while not self._stop_event.is_set():
            if self.dry_run:
                self._record(200, None)
            else:
                status, _, err = self._request_fn(
                    target.url,
                    method=target.method,
                    headers=target.headers,
                    timeout=target.timeout_secs,
                )
                self._record(status, err)
            if self._stop_event.wait(max(0.02, target.interval_secs)):
                break

    def start(self) -> None:
        self._stop_event.clear()
        for target in self.targets:
            worker_count = max(1, target.workers)
            for idx in range(worker_count):
                stagger = (target.interval_secs / worker_count) * idx
                th = threading.Thread(
                    target=self._worker_loop,
                    args=(target, stagger),
                    daemon=True,
                    name=f"chaos-load-{idx}",
                )
                th.start()
                self._threads.append(th)

    def stop(self, join_timeout: float = 6.0) -> dict[str, Any]:
        self._stop_event.set()
        deadline = time.monotonic() + join_timeout
        for th in self._threads:
            remaining = max(0.05, deadline - time.monotonic())
            th.join(timeout=remaining)
        self._threads.clear()
        return self.stats()

    def stats(self) -> dict[str, Any]:
        with self._lock:
            return {
                "requests": self._total,
                "errors": self._errors,
                "status_codes": dict(self._status_codes),
            }
