#!/usr/bin/env python3
"""NexusMart Chaos Engineering Runner & Measurement Harness (UC-OPS08).

Executes the 5 controlled fault-injection scenarios against the Docker Compose stack,
coordinates shared stack access via `.runs/stack.lock`, polls AIOps (`:8092`) for
anomaly detection & RCA accuracy, guarantees idempotent cleanup on EXIT/INT/TERM,
and emits structured JSON + Markdown summary reports.
"""

from __future__ import annotations

import argparse
import os
import signal
import sys
import threading
import time
from pathlib import Path
from typing import Any, Optional, Sequence, TextIO

# Allow running directly (`python3 tools/chaos/run_chaos.py`) or as module (`python3 -m tools.chaos.run_chaos`).
if __package__ in (None, ""):
    _THIS_DIR = str(Path(__file__).resolve().parent)
    if _THIS_DIR not in sys.path:
        sys.path.insert(0, _THIS_DIR)
    from aiops_client import (
        AiopsClient,
        LoadGenerator,
        evaluate_rca_match,
        extract_incident_fields,
        http_request_status,
        is_incident_open,
        select_new_incident,
        utc_now_iso,
    )
    from docker_ops import DockerOps
    from lock import StackLock
    from report import ScenarioResult, build_json_report, write_reports
    from scenarios import (
        ALL_SCENARIO_NAMES,
        ChaosEndpoints,
        ChaosScenario,
        build_scenarios,
    )
else:
    from .aiops_client import (
        AiopsClient,
        LoadGenerator,
        evaluate_rca_match,
        extract_incident_fields,
        http_request_status,
        is_incident_open,
        select_new_incident,
        utc_now_iso,
    )
    from .docker_ops import DockerOps
    from .lock import StackLock
    from .report import ScenarioResult, build_json_report, write_reports
    from .scenarios import (
        ALL_SCENARIO_NAMES,
        ChaosEndpoints,
        ChaosScenario,
        build_scenarios,
    )


class ChaosInterrupted(BaseException):
    """Raised on SIGINT/SIGTERM so finally blocks always clean up faults and stack.lock."""


def _log(msg: str, stream: Optional[TextIO] = None) -> None:
    out = stream if stream is not None else sys.stderr
    print(f"[chaos] {msg}", file=out, flush=True)


def _snapshot_incidents(client: AiopsClient) -> dict[str, tuple[str, str]]:
    """Capture current incident IDs -> (status, updated_at) before fault injection."""
    snapshots: dict[str, tuple[str, str]] = {}
    for raw in client.list_incidents():
        norm = extract_incident_fields(raw)
        if norm["id"]:
            snapshots[norm["id"]] = (norm["status"], norm["updated_at"])
    return snapshots


def _resolve_endpoints(args: argparse.Namespace, docker: DockerOps) -> ChaosEndpoints:
    aiops_port = os.environ.get("AIOPS_HTTP_PORT", "8092")
    gw_port = os.environ.get("GATEWAY_PORT", "28080")
    pay_port = os.environ.get("PAYMENT_HTTP_PORT", "8086")
    promo_port = os.environ.get("PROMOTION_HTTP_PORT", "8084")
    order_port = os.environ.get("ORDER_HTTP_PORT", "8085")
    nats_port = os.environ.get("NATS_MONITOR_PORT", "8222")

    aiops_url = args.aiops_url or os.environ.get("AIOPS_URL") or docker.resolve_service_url(
        docker.container_name("aiops"), 8092, f"http://127.0.0.1:{aiops_port}"
    )
    gateway_url = args.gateway_url or os.environ.get("GATEWAY_URL") or docker.resolve_service_url(
        docker.container_name("gateway"), 8080, f"http://127.0.0.1:{gw_port}"
    )
    payment_url = args.payment_url or docker.resolve_service_url(
        docker.container_name("payment"), 8086, f"http://127.0.0.1:{pay_port}"
    )
    promotion_url = args.promotion_url or docker.resolve_service_url(
        docker.container_name("promotion"), 8084, f"http://127.0.0.1:{promo_port}"
    )
    order_url = args.order_url or docker.resolve_service_url(
        docker.container_name("order"), 8085, f"http://127.0.0.1:{order_port}"
    )
    nats_monitor_url = args.nats_monitor_url or docker.resolve_service_url(
        docker.container_name("nats"), 8222, f"http://127.0.0.1:{nats_port}"
    )

    netem_images = (args.netem_image,) if args.netem_image else (
        "nicolaka/netshoot:latest",
        "nicolaka/netshoot",
    )

    return ChaosEndpoints(
        aiops_url=aiops_url,
        gateway_url=gateway_url,
        payment_url=payment_url,
        promotion_url=promotion_url,
        order_url=order_url,
        nats_monitor_url=nats_monitor_url,
        postgres_container=args.postgres_container or docker.container_name("postgres"),
        postgres_user=args.postgres_user or os.environ.get("POSTGRES_USER", "nexusmart"),
        netem_images=netem_images,
        buyer_bearer_token=args.buyer_token or os.environ.get("CHAOS_BUYER_TOKEN", ""),
    )


def _warmup_aiops(
    client: AiopsClient,
    *,
    dry_run: bool,
    warmup_scans: int,
    warmup_interval: float,
    log_stream: Optional[TextIO] = None,
) -> None:
    if dry_run:
        _log(f"dry-run: skipping live AIOps warmup ({warmup_scans} baseline scans simulated)", log_stream)
        return

    ok, body = client.healthz()
    if not ok:
        raise RuntimeError(f"AIOps health check failed at {client.base_url}/healthz: {body}")
    _log(f"AIOps healthy at {client.base_url}/healthz", log_stream)

    for idx in range(max(0, warmup_scans)):
        scan_ok, resp = client.trigger_scan()
        _log(
            f"warmup baseline scan {idx + 1}/{warmup_scans}: {'ok' if scan_ok else f'warn ({resp})'}",
            log_stream,
        )
        if idx + 1 < warmup_scans:
            time.sleep(max(0.5, warmup_interval))


def _cooldown_and_wait_resolve(
    client: AiopsClient,
    scenario: ChaosScenario,
    incident_id: Optional[str],
    *,
    cooldown_secs: float,
    dry_run: bool,
    trigger_scan: bool,
    log_stream: Optional[TextIO] = None,
) -> None:
    if dry_run or cooldown_secs <= 0:
        return

    _log(f"cooldown {cooldown_secs:.0f}s after {scenario.name}...", log_stream)
    probe_urls = scenario.recovery_probe_urls()
    deadline = time.monotonic() + cooldown_secs
    last_probe = 0.0
    last_check = 0.0

    while time.monotonic() < deadline:
        now = time.monotonic()
        if probe_urls and (now - last_probe) >= 2.0:
            for url in probe_urls:
                http_request_status(url, timeout=3.0)
            last_probe = now

        if incident_id and (now - last_check) >= 5.0:
            last_check = now
            try:
                if trigger_scan:
                    client.trigger_scan()
                incidents = client.list_incidents()
                for raw in incidents:
                    norm = extract_incident_fields(raw)
                    if norm["id"] == incident_id and not is_incident_open(norm["status"]):
                        _log(f"incident {incident_id} resolved during cooldown", log_stream)
                        return
            except Exception as exc:
                _log(f"cooldown poll warning: {exc}", log_stream)

        time.sleep(min(1.0, max(0.05, deadline - time.monotonic())))


def run_single_scenario(
    scenario: ChaosScenario,
    client: AiopsClient,
    *,
    dry_run: bool,
    detect_timeout: float,
    poll_interval: float,
    recovery_timeout: float,
    trigger_scan: bool,
    log_stream: Optional[TextIO] = None,
) -> ScenarioResult:
    """Execute one chaos scenario end-to-end with guaranteed idempotent cleanup."""
    scenario.prepare()
    _log(
        f"=== Starting scenario '{scenario.name}' ({scenario.title}) "
        f"target={scenario.runtime.target_container} mode={scenario.runtime.target_mode} ===",
        log_stream,
    )

    baseline_snapshots: dict[str, tuple[str, str]] = {}
    if not dry_run:
        try:
            baseline_snapshots = _snapshot_incidents(client)
        except Exception as exc:
            _log(f"warning: failed to snapshot baseline incidents: {exc}", log_stream)

    load_gen = LoadGenerator(scenario.traffic_targets(), dry_run=dry_run)
    fault_started_iso = utc_now_iso()
    fault_started_epoch = time.time()
    fault_started_mono = time.monotonic()

    detected_at_iso: Optional[str] = None
    detection_secs: Optional[float] = None
    matched_incident: Optional[dict[str, Any]] = None
    all_new_ids: list[str] = []
    scenario_error: Optional[str] = None
    cleanup_ok = True
    recovered_ok = True
    fault_ended_iso = fault_started_iso
    recovered_at_iso: Optional[str] = None
    recovery_secs: Optional[float] = None
    traffic_stats: dict[str, Any] = {}

    try:
        load_gen.start()
        scenario.inject()

        if dry_run:
            # Exercise the exact incident selection & RCA evaluation pipeline on synthetic data.
            sim_inc = scenario.dry_run_simulated_incident(fault_started_iso)
            matched_incident, all_new_ids = select_new_incident(
                [sim_inc],
                baseline_snapshots={},
                fault_started_epoch=fault_started_epoch,
            )
            detected_at_iso = utc_now_iso()
            detection_secs = round(time.monotonic() - fault_started_mono, 3)
        else:
            deadline = fault_started_mono + detect_timeout
            while time.monotonic() < deadline:
                if trigger_scan:
                    client.trigger_scan()
                try:
                    incidents = client.list_incidents()
                    selected, cand_ids = select_new_incident(
                        incidents,
                        baseline_snapshots=baseline_snapshots,
                        fault_started_epoch=fault_started_epoch,
                    )
                    if selected is not None:
                        matched_incident = selected
                        all_new_ids = cand_ids
                        detected_at_iso = utc_now_iso()
                        detection_secs = round(time.monotonic() - fault_started_mono, 2)
                        _log(
                            f"detected incident id={selected['id']} in {detection_secs:.1f}s: "
                            f"root_cause={selected['root_cause_service']} category={selected['category']}",
                            log_stream,
                        )
                        break
                except Exception as exc:
                    _log(f"poll error during {scenario.name}: {exc}", log_stream)

                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    break
                time.sleep(min(poll_interval, max(0.1, remaining)))
    except ChaosInterrupted:
        scenario_error = "interrupted by signal"
        raise
    except Exception as exc:
        scenario_error = str(exc)
        _log(f"scenario {scenario.name} error: {exc}", log_stream)
    finally:
        # Shield cleanup from a second SIGINT/SIGTERM so injected faults are ALWAYS restored.
        in_main_thread = threading.current_thread() is threading.main_thread()
        saved_int = signal.getsignal(signal.SIGINT) if in_main_thread else None
        saved_term = signal.getsignal(signal.SIGTERM) if in_main_thread else None
        if in_main_thread:
            signal.signal(signal.SIGINT, signal.SIG_IGN)
            signal.signal(signal.SIGTERM, signal.SIG_IGN)
        try:
            traffic_stats = load_gen.stop()
            fault_ended_mono = time.monotonic()
            fault_ended_iso = utc_now_iso()
            try:
                scenario.cleanup()
            except Exception as cleanup_exc:
                cleanup_ok = False
                _log(f"CLEANUP FAILED for {scenario.name}: {cleanup_exc}", log_stream)
                if scenario_error is None:
                    scenario_error = f"cleanup failed: {cleanup_exc}"

            verify_limit = (
                min(recovery_timeout, 30.0)
                if scenario_error == "interrupted by signal"
                else recovery_timeout
            )
            try:
                recovered_ok = scenario.verify_recovered(timeout=verify_limit)
                if recovered_ok:
                    recovered_at_iso = utc_now_iso()
                    recovery_secs = round(time.monotonic() - fault_ended_mono, 2)
                    _log(f"recovered {scenario.name} in {recovery_secs:.1f}s", log_stream)
                else:
                    _log(f"recovery verification timed out for {scenario.name}", log_stream)
            except Exception as rec_exc:
                recovered_ok = False
                _log(f"recovery check error for {scenario.name}: {rec_exc}", log_stream)
        finally:
            if in_main_thread and saved_int is not None and saved_term is not None:
                signal.signal(signal.SIGINT, saved_int)
                signal.signal(signal.SIGTERM, saved_term)

    detected = matched_incident is not None
    detected_under_120s = bool(detected and detection_secs is not None and detection_secs < detect_timeout)

    pred_service = matched_incident["root_cause_service"] if matched_incident else ""
    pred_category = matched_incident["category"] if matched_incident else ""
    pred_confidence = matched_incident["confidence"] if matched_incident else 0.0
    pred_summary = matched_incident["summary"] if matched_incident else ""
    insufficient = matched_incident["insufficient_data"] if matched_incident else False

    if detected:
        rca_eval = evaluate_rca_match(
            scenario.name,
            pred_service,
            pred_category,
            insufficient_data=insufficient,
        )
    else:
        rca_eval = evaluate_rca_match(
            scenario.name,
            "",
            "",
            insufficient_data=False,
        )
        rca_eval.rca_match = False
        rca_eval.service_match = False
        rca_eval.category_match = False
        rca_eval.reason = f"no incident detected within {detect_timeout:.0f}s"

    return ScenarioResult(
        scenario=scenario.name,
        title=scenario.title,
        target_service=scenario.target_service,
        target_container=scenario.runtime.target_container,
        target_mode=scenario.runtime.target_mode,
        injection_method=scenario.runtime.injection_method,
        fault_started_at=fault_started_iso,
        detected_at=detected_at_iso,
        fault_ended_at=fault_ended_iso,
        recovered_at=recovered_at_iso,
        detection_seconds=detection_secs,
        detected=detected,
        detected_under_120s=detected_under_120s,
        recovery_seconds=recovery_secs,
        incident_id=matched_incident["id"] if matched_incident else None,
        all_new_incident_ids=all_new_ids,
        predicted_root_cause_service=pred_service,
        predicted_category=pred_category,
        predicted_confidence=pred_confidence,
        predicted_summary=pred_summary,
        insufficient_data=insufficient,
        expected_root_cause_service=rca_eval.expected_service,
        expected_categories=rca_eval.expected_categories,
        service_match=rca_eval.service_match,
        category_match=rca_eval.category_match,
        rca_match=rca_eval.rca_match,
        rca_reason=rca_eval.reason,
        traffic_stats=traffic_stats,
        details=dict(scenario.runtime.details),
        cleanup_ok=cleanup_ok,
        recovered_ok=recovered_ok,
        error=scenario_error,
    )


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="run_chaos.py",
        description="NexusMart Chaos Engineering fault-injection & AIOps measurement harness (UC-OPS08).",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Simulate the full runner, stack lock, scenario flow, and reports without touching Docker or live services.",
    )
    parser.add_argument(
        "--scenario",
        action="append",
        dest="scenarios",
        default=None,
        help=(
            "Scenario(s) to run (repeatable or comma-separated). "
            f"Valid: {', '.join(ALL_SCENARIO_NAMES)}. Default: all 5."
        ),
    )
    parser.add_argument(
        "--lock-file",
        default=".runs/stack.lock",
        help="Path to shared stack coordination lock file (default: .runs/stack.lock).",
    )
    parser.add_argument(
        "--lock-timeout",
        type=float,
        default=600.0,
        help="Max seconds to wait for .runs/stack.lock before failing (default: 600).",
    )
    parser.add_argument(
        "--lock-poll",
        type=float,
        default=5.0,
        help="Polling interval in seconds while waiting for stack lock (default: 5).",
    )
    parser.add_argument(
        "--break-stale-lock",
        action="store_true",
        help="Remove stack lock automatically ONLY if its recorded local PID is dead (off by default).",
    )
    parser.add_argument(
        "--no-lock",
        action="store_true",
        help="Skip acquiring stack lock (use only if caller already holds .runs/stack.lock).",
    )
    parser.add_argument(
        "--output",
        default=".runs/chaos-results.json",
        help="Path to write JSON report (default: .runs/chaos-results.json).",
    )
    parser.add_argument(
        "--markdown-output",
        default=None,
        help="Optional path to write Markdown table (default: sibling .md beside --output).",
    )
    parser.add_argument(
        "--detect-timeout",
        type=float,
        default=120.0,
        help="Max seconds to wait for AIOps detection per scenario (default: 120).",
    )
    parser.add_argument(
        "--poll-interval",
        type=float,
        default=5.0,
        help="Seconds between AIOps incident polls during a fault (default: 5).",
    )
    parser.add_argument(
        "--recovery-timeout",
        type=float,
        default=90.0,
        help="Max seconds to wait for service health recovery during cleanup (default: 90).",
    )
    parser.add_argument(
        "--cooldown",
        type=float,
        default=25.0,
        help="Cooldown seconds between scenarios while incidents resolve (default: 25).",
    )
    parser.add_argument(
        "--warmup-scans",
        type=int,
        default=3,
        help="Baseline scans to trigger against AIOps before scenario 1 (default: 3).",
    )
    parser.add_argument(
        "--warmup-interval",
        type=float,
        default=2.0,
        help="Seconds between warmup scans (default: 2).",
    )
    parser.add_argument(
        "--trigger-scan",
        action="store_true",
        help="Trigger POST /api/v1/scan on each poll (default: off; let AIOps background poller detect naturally).",
    )
    parser.add_argument("--aiops-url", default=None, help="AIOps base URL (default: http://127.0.0.1:8092).")
    parser.add_argument("--gateway-url", default=None, help="Gateway base URL (auto-discovered or 127.0.0.1:28080).")
    parser.add_argument("--payment-url", default=None, help="Payment base URL (auto-discovered or 127.0.0.1:8086).")
    parser.add_argument("--promotion-url", default=None, help="Promotion base URL (auto-discovered or 127.0.0.1:8084).")
    parser.add_argument("--order-url", default=None, help="Order base URL (auto-discovered or 127.0.0.1:8085).")
    parser.add_argument("--nats-monitor-url", default=None, help="NATS monitor URL (default: 127.0.0.1:8222).")
    parser.add_argument("--postgres-container", default=None, help="Postgres container name (default: nexus_ecom-postgres-1).")
    parser.add_argument("--postgres-user", default=None, help="Postgres user (default: $POSTGRES_USER or nexusmart).")
    parser.add_argument("--compose-project", default="nexus_ecom", help="Docker Compose project name (default: nexus_ecom).")
    parser.add_argument("--compose-network", default="nexus_ecom_default", help="Docker Compose network name (default: nexus_ecom_default).")
    parser.add_argument("--netem-image", default=None, help="Optional cached Docker image with `tc` for netem latency injection.")
    parser.add_argument("--buyer-token", default=None, help="Optional buyer JWT for authenticated gateway /api/v1/orders probes.")
    return parser.parse_args(argv)


def _expand_scenario_names(raw_list: Optional[list[str]]) -> list[str]:
    if not raw_list:
        return list(ALL_SCENARIO_NAMES)
    names: list[str] = []
    for item in raw_list:
        for part in item.split(","):
            cleaned = part.strip()
            if cleaned and cleaned not in names:
                names.append(cleaned)
    return names


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = parse_args(argv)
    scenario_names = _expand_scenario_names(args.scenarios)

    docker = DockerOps(
        dry_run=args.dry_run,
        compose_project=args.compose_project,
        compose_network=args.compose_network,
    )
    endpoints = _resolve_endpoints(args, docker)
    scenarios = build_scenarios(scenario_names, docker, endpoints)
    client = AiopsClient(endpoints.aiops_url)

    lock: Optional[StackLock] = None
    if not args.no_lock:
        lock = StackLock(
            args.lock_file,
            owner="E",
            timeout=args.lock_timeout,
            poll_interval=args.lock_poll,
            break_stale_local=args.break_stale_lock,
        )

    in_main_thread = threading.current_thread() is threading.main_thread()
    prev_sigint = signal.getsignal(signal.SIGINT) if in_main_thread else None
    prev_sigterm = signal.getsignal(signal.SIGTERM) if in_main_thread else None

    def _handle_signal(signum: int, _frame: Any) -> None:
        sig_name = signal.Signals(signum).name
        _log(f"received {sig_name}; aborting and running cleanup traps...")
        raise ChaosInterrupted(f"Interrupted by {sig_name}")

    if in_main_thread:
        signal.signal(signal.SIGINT, _handle_signal)
        signal.signal(signal.SIGTERM, _handle_signal)

    run_started_iso = utc_now_iso()
    run_started_mono = time.monotonic()
    results: list[ScenarioResult] = []
    exit_code = 0

    try:
        if lock is not None:
            lock.acquire()

        _warmup_aiops(
            client,
            dry_run=args.dry_run,
            warmup_scans=args.warmup_scans,
            warmup_interval=args.warmup_interval,
        )

        for idx, scenario in enumerate(scenarios):
            res = run_single_scenario(
                scenario,
                client,
                dry_run=args.dry_run,
                detect_timeout=args.detect_timeout,
                poll_interval=args.poll_interval,
                recovery_timeout=args.recovery_timeout,
                trigger_scan=args.trigger_scan,
            )
            results.append(res)

            if idx + 1 < len(scenarios):
                _cooldown_and_wait_resolve(
                    client,
                    scenario,
                    res.incident_id,
                    cooldown_secs=args.cooldown,
                    dry_run=args.dry_run,
                    trigger_scan=args.trigger_scan,
                )

    except ChaosInterrupted as exc:
        _log(str(exc))
        exit_code = 130
    except Exception as exc:
        _log(f"fatal error: {exc}")
        exit_code = 1
    finally:
        if lock is not None:
            lock.release()
        if in_main_thread and prev_sigint is not None and prev_sigterm is not None:
            signal.signal(signal.SIGINT, prev_sigint)
            signal.signal(signal.SIGTERM, prev_sigterm)

    if results:
        run_finished_iso = utc_now_iso()
        duration_secs = time.monotonic() - run_started_mono
        report = build_json_report(
            results,
            mode="dry_run" if args.dry_run else "live",
            started_at=run_started_iso,
            finished_at=run_finished_iso,
            duration_seconds=duration_secs,
            aiops_url=endpoints.aiops_url,
            config={
                "scenarios": scenario_names,
                "detect_timeout": args.detect_timeout,
                "poll_interval": args.poll_interval,
                "cooldown": args.cooldown,
                "trigger_scan": args.trigger_scan,
                "lock_file": str(args.lock_file) if not args.no_lock else None,
            },
            detect_limit_secs=args.detect_timeout,
        )
        jpath, mpath, md_text = write_reports(
            report,
            json_path=args.output,
            markdown_path=args.markdown_output,
        )
        print(md_text)
        _log(f"wrote JSON report: {jpath}")
        _log(f"wrote Markdown report: {mpath}")
        if exit_code == 0 and not report["summary"]["overall_gate_passed"]:
            exit_code = 2

    return exit_code


if __name__ == "__main__":
    sys.exit(main())
