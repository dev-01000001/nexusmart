"""Structured JSON and Markdown report generation for NexusMart Chaos runs (UC-OPS08)."""

from __future__ import annotations

import json
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any, Optional, Sequence


@dataclass
class ScenarioResult:
    scenario: str
    title: str
    target_service: str
    target_container: str
    target_mode: str
    injection_method: str
    fault_started_at: str
    detected_at: Optional[str]
    fault_ended_at: str
    recovered_at: Optional[str]
    detection_seconds: Optional[float]
    detected: bool
    detected_under_120s: bool
    recovery_seconds: Optional[float]
    incident_id: Optional[str]
    all_new_incident_ids: list[str]
    predicted_root_cause_service: str
    predicted_category: str
    predicted_confidence: float
    predicted_summary: str
    insufficient_data: bool
    expected_root_cause_service: str
    expected_categories: list[str]
    service_match: bool
    category_match: bool
    rca_match: bool
    rca_reason: str
    traffic_stats: dict[str, Any] = field(default_factory=dict)
    details: dict[str, Any] = field(default_factory=dict)
    cleanup_ok: bool = True
    recovered_ok: bool = True
    error: Optional[str] = None


def compute_summary(results: Sequence[ScenarioResult], *, detect_limit_secs: float = 120.0) -> dict[str, Any]:
    total = len(results)
    detected_count = sum(1 for r in results if r.detected)
    detected_under_limit = sum(1 for r in results if r.detected_under_120s)
    rca_match_count = sum(1 for r in results if r.rca_match)
    cleanup_all_ok = all(r.cleanup_ok and r.recovered_ok for r in results)

    # Spec UC-OPS08 gate: detection < 120s AND correct root cause >= 4/5 (proportional if subset).
    if total == 5:
        rca_required_min = 4
    elif total == 0:
        rca_required_min = 0
    else:
        rca_required_min = max(1, (total * 4 + 4) // 5)

    all_detected_under_120s = total > 0 and detected_under_limit == total
    rca_gate_passed = total > 0 and rca_match_count >= rca_required_min
    overall_gate_passed = all_detected_under_120s and rca_gate_passed and cleanup_all_ok

    return {
        "total_scenarios": total,
        "detect_limit_seconds": detect_limit_secs,
        "detected_count": detected_count,
        "detected_under_120s_count": detected_under_limit,
        "rca_match_count": rca_match_count,
        "rca_required_min": rca_required_min,
        "all_detected_under_120s": all_detected_under_120s,
        "rca_gate_passed": rca_gate_passed,
        "cleanup_all_ok": cleanup_all_ok,
        "overall_gate_passed": overall_gate_passed,
    }


def build_json_report(
    results: Sequence[ScenarioResult],
    *,
    mode: str,
    started_at: str,
    finished_at: str,
    duration_seconds: float,
    aiops_url: str,
    config: dict[str, Any],
    detect_limit_secs: float = 120.0,
) -> dict[str, Any]:
    summary = compute_summary(results, detect_limit_secs=detect_limit_secs)
    return {
        "schema_version": 1,
        "mode": mode,
        "started_at": started_at,
        "finished_at": finished_at,
        "duration_seconds": round(duration_seconds, 2),
        "aiops_url": aiops_url,
        "config": config,
        "summary": summary,
        "scenarios": [asdict(r) for r in results],
    }


def _fmt_seconds(val: Optional[float]) -> str:
    if val is None:
        return "N/A"
    return f"{val:.1f}"


def _fmt_bool(flag: bool) -> str:
    return "PASS" if flag else "FAIL"


def _fmt_target(r: ScenarioResult) -> str:
    if r.target_mode == "stand_in":
        return f"{r.target_service} (`{r.target_container}` stand-in)"
    return f"{r.target_service} (`{r.target_container}`)"


def _fmt_predicted_rca(r: ScenarioResult) -> str:
    if not r.detected:
        return "_not detected_"
    if r.insufficient_data:
        return "insufficient_data"
    svc = r.predicted_root_cause_service or "unknown"
    cat = r.predicted_category
    return f"`{svc}` ({cat})" if cat else f"`{svc}`"


def render_markdown_report(report: dict[str, Any]) -> str:
    """Render a clean GitHub-flavored Markdown summary table and gate status."""
    mode = report.get("mode", "live")
    mode_badge = " [DRY-RUN — simulated, no live containers touched]" if mode == "dry_run" else ""
    summary = report["summary"]
    scenarios = report["scenarios"]

    lines: list[str] = [
        f"# NexusMart Chaos Engineering Report (UC-OPS08){mode_badge}",
        "",
        f"- **Mode**: `{mode}`",
        f"- **Started**: `{report.get('started_at', '')}`",
        f"- **Finished**: `{report.get('finished_at', '')}` ({report.get('duration_seconds', 0)}s)",
        f"- **AIOps URL**: `{report.get('aiops_url', '')}`",
        "",
        "| Scenario | Target | Detection Time (s) | < 120s? | Predicted Root Cause | Expected Root Cause | RCA Match? | Recovery Time (s) |",
        "|---|---|---:|:---:|---|---|:---:|---:|",
    ]

    for row in scenarios:
        res = ScenarioResult(**row) if isinstance(row, dict) else row
        expected_cat = res.expected_categories[0] if res.expected_categories else ""
        expected_str = (
            f"`{res.expected_root_cause_service}` ({expected_cat})"
            if expected_cat
            else f"`{res.expected_root_cause_service}`"
        )
        lines.append(
            f"| `{res.scenario}` | {_fmt_target(res)} | {_fmt_seconds(res.detection_seconds)} | "
            f"{_fmt_bool(res.detected_under_120s)} | {_fmt_predicted_rca(res)} | "
            f"{expected_str} | {_fmt_bool(res.rca_match)} | {_fmt_seconds(res.recovery_seconds)} |"
        )

    total = summary["total_scenarios"]
    det_ok = summary["detected_under_120s_count"]
    rca_ok = summary["rca_match_count"]
    rca_min = summary["rca_required_min"]
    gate = "PASS" if summary["overall_gate_passed"] else "FAIL"
    gate_suffix = " (dry-run simulation)" if mode == "dry_run" else ""

    lines.extend(
        [
            "",
            "## Overall Gate",
            "",
            f"- **Detection < 120s**: `{det_ok}/{total}` ({_fmt_bool(summary['all_detected_under_120s'])})",
            f"- **RCA Accuracy**: `{rca_ok}/{total}` (required `>= {rca_min}/{total}`, {_fmt_bool(summary['rca_gate_passed'])})",
            f"- **Cleanup & Recovery**: {_fmt_bool(summary['cleanup_all_ok'])}",
            f"- **Overall Gate**: **{gate}**{gate_suffix}",
            "",
        ]
    )

    return "\n".join(lines)


def write_reports(
    report: dict[str, Any],
    *,
    json_path: Path | str,
    markdown_path: Optional[Path | str] = None,
) -> tuple[Path, Path, str]:
    """Write JSON report and sibling Markdown report; return (json_path, md_path, markdown_text)."""
    jpath = Path(json_path)
    jpath.parent.mkdir(parents=True, exist_ok=True)
    jpath.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

    mpath = Path(markdown_path) if markdown_path else jpath.with_suffix(".md")
    mpath.parent.mkdir(parents=True, exist_ok=True)
    md_text = render_markdown_report(report)
    mpath.write_text(md_text, encoding="utf-8")
    return jpath, mpath, md_text
