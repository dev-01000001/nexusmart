"""Unit tests for NexusMart Chaos Engineering harness (tools/chaos/).

Covers:
  - Atomic .runs/stack.lock acquire, contention/wait, timeout, release ownership, and stale handling
  - RCA evaluation logic across all 5 scenarios (positive, alias, negative, insufficient_data)
  - AIOps incident normalization and unbiased multi-incident selection
  - Docker port parsing and scenario cleanup-on-failure / search live vs stand-in selection
  - End-to-end --dry-run execution, JSON report schema, and Markdown table rendering without touching Docker
"""

from __future__ import annotations

import io
import json
import os
import subprocess
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

try:
    from .aiops_client import (
        AiopsClient,
        evaluate_rca_match,
        extract_incident_fields,
        select_new_incident,
        utc_now_iso,
    )
    from .docker_ops import DockerOps, parse_docker_port_output
    from .lock import LockTimeoutError, StackLock, parse_lock_metadata
    from .report import ScenarioResult, build_json_report, render_markdown_report
    from .run_chaos import main as chaos_main
    from .run_chaos import run_single_scenario
    from .scenarios import (
        ALL_SCENARIO_NAMES,
        ChaosEndpoints,
        PromotionDbErrorScenario,
        SearchHighCpuScenario,
        SlowPaymentScenario,
        StopOrderScenario,
    )
except ImportError:
    from aiops_client import (
        AiopsClient,
        evaluate_rca_match,
        extract_incident_fields,
        select_new_incident,
        utc_now_iso,
    )
    from docker_ops import DockerOps, parse_docker_port_output
    from lock import LockTimeoutError, StackLock, parse_lock_metadata
    from report import ScenarioResult, build_json_report, render_markdown_report
    from run_chaos import main as chaos_main
    from run_chaos import run_single_scenario
    from scenarios import (
        ALL_SCENARIO_NAMES,
        ChaosEndpoints,
        PromotionDbErrorScenario,
        SearchHighCpuScenario,
        SlowPaymentScenario,
        StopOrderScenario,
    )


class StackLockTests(unittest.TestCase):
    def test_acquire_writes_metadata_and_release_removes_lock(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            lock_path = Path(tmp) / ".runs" / "stack.lock"
            log_buf = io.StringIO()
            lock = StackLock(lock_path, owner="E", timeout=2.0, poll_interval=0.05, log_stream=log_buf)

            self.assertFalse(lock.acquired)
            with lock:
                self.assertTrue(lock.acquired)
                self.assertTrue(lock_path.exists())
                content = lock_path.read_text(encoding="utf-8")
                meta = parse_lock_metadata(content)
                self.assertEqual(meta["owner"], "E")
                self.assertEqual(meta["pid"], str(os.getpid()))
                self.assertIn("started", meta)
                self.assertIn("token", meta)

            self.assertFalse(lock.acquired)
            self.assertFalse(lock_path.exists())
            # Double release is safe no-op.
            self.assertFalse(lock.release())

    def test_contention_waits_until_other_holder_releases(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            lock_path = Path(tmp) / "stack.lock"
            lock_path.write_text("H\npid=999999\nstarted=2026-11-21T07:00:00+00:00\n", encoding="utf-8")

            def _release_later() -> None:
                time.sleep(0.15)
                try:
                    lock_path.unlink()
                except FileNotFoundError:
                    pass

            releaser = threading.Thread(target=_release_later, daemon=True)
            releaser.start()

            log_buf = io.StringIO()
            lock = StackLock(lock_path, owner="E", timeout=3.0, poll_interval=0.05, log_stream=log_buf)
            lock.acquire()
            try:
                self.assertTrue(lock.acquired)
                self.assertIn("waiting for", log_buf.getvalue())
                self.assertIn("H", log_buf.getvalue())
            finally:
                lock.release()
                releaser.join(timeout=2.0)

            self.assertFalse(lock_path.exists())

    def test_timeout_does_not_delete_foreign_lock(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            lock_path = Path(tmp) / "stack.lock"
            foreign_content = f"H\npid={os.getpid()}\nstarted=2026-11-21T07:00:00+00:00\n"
            lock_path.write_text(foreign_content, encoding="utf-8")

            log_buf = io.StringIO()
            lock = StackLock(lock_path, owner="E", timeout=0.12, poll_interval=0.04, log_stream=log_buf)
            with self.assertRaises(LockTimeoutError):
                lock.acquire()

            self.assertFalse(lock.acquired)
            # Calling release on an unacquired lock MUST NOT touch the foreign lock!
            self.assertFalse(lock.release())
            self.assertTrue(lock_path.exists())
            self.assertEqual(lock_path.read_text(encoding="utf-8"), foreign_content)

    def test_stale_local_lock_behavior(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            lock_path = Path(tmp) / "stack.lock"
            # PID 99999999 is non-existent on Linux.
            lock_path.write_text("E\npid=99999999\nstarted=2026-11-20T00:00:00+00:00\n", encoding="utf-8")

            # Default break_stale_local=False MUST NOT auto-steal lock.
            safe_buf = io.StringIO()
            safe_lock = StackLock(
                lock_path, owner="E", timeout=0.08, poll_interval=0.03,
                break_stale_local=False, log_stream=safe_buf,
            )
            with self.assertRaises(LockTimeoutError):
                safe_lock.acquire()
            self.assertTrue(lock_path.exists())
            self.assertIn("--break-stale-lock", safe_buf.getvalue())

            # With break_stale_local=True, dead local PID lock is cleaned and acquired.
            break_buf = io.StringIO()
            break_lock = StackLock(
                lock_path, owner="E", timeout=1.0, poll_interval=0.03,
                break_stale_local=True, log_stream=break_buf,
            )
            with break_lock:
                self.assertTrue(break_lock.acquired)
                self.assertIn("removing stale lock", break_buf.getvalue())
            self.assertFalse(lock_path.exists())


class RcaEvaluationTests(unittest.TestCase):
    def test_exact_matches_for_all_five_scenarios(self) -> None:
        cases = [
            ("slow-payment", "payment", "latency_degradation"),
            ("stop-order", "order", "service_unavailable"),
            ("promotion-db-error", "promotion", "database_error"),
            ("promotion-db-error", "promotion-db", "database_error"),
            ("nats-outage", "nats", "message_broker_outage"),
            ("search-high-cpu", "search", "cpu_saturation"),
            ("search-high-cpu", "e-test-search", "high_cpu"),
        ]
        for scenario, svc, cat in cases:
            with self.subTest(scenario=scenario, svc=svc, cat=cat):
                ev = evaluate_rca_match(scenario, svc, cat)
                self.assertTrue(ev.service_match)
                self.assertTrue(ev.category_match)
                self.assertTrue(ev.rca_match)

    def test_negative_mismatches_and_insufficient_data(self) -> None:
        # Wrong service (e.g. blaming gateway instead of order during stop-order).
        ev_wrong_svc = evaluate_rca_match("stop-order", "gateway", "service_unavailable")
        self.assertFalse(ev_wrong_svc.service_match)
        self.assertFalse(ev_wrong_svc.rca_match)

        # Right service, wrong category (e.g. blaming cpu_saturation on slow-payment).
        ev_wrong_cat = evaluate_rca_match("slow-payment", "payment", "cpu_saturation")
        self.assertTrue(ev_wrong_cat.service_match)
        self.assertFalse(ev_wrong_cat.category_match)
        self.assertFalse(ev_wrong_cat.rca_match)

        # Insufficient data MUST fail RCA match even if service label matches.
        ev_insuff = evaluate_rca_match(
            "nats-outage", "nats", "message_broker_outage", insufficient_data=True
        )
        self.assertFalse(ev_insuff.rca_match)
        self.assertIn("insufficient_data", ev_insuff.reason)

    def test_select_new_incident_ignores_baseline_and_ranks_unbiased(self) -> None:
        fault_epoch = time.time()
        now_iso = utc_now_iso()
        baseline = {
            "inc-old": ("open", "2026-11-20T00:00:00+00:00"),
        }
        incidents = [
            {
                "id": "inc-old",
                "service": "cart",
                "status": "open",
                "opened_at": "2026-11-20T00:00:00+00:00",
                "updated_at": "2026-11-20T00:00:00+00:00",
                "rca": {"root_cause_service": "cart", "category": "high_error_rate", "confidence": 0.99},
            },
            {
                "id": "inc-weak",
                "service": "gateway",
                "status": "open",
                "opened_at": now_iso,
                "updated_at": now_iso,
                "rca": {"root_cause_service": "gateway", "category": "high_error_rate", "confidence": 0.55},
            },
            {
                "id": "inc-primary",
                "service": "order",
                "status": "open",
                "opened_at": now_iso,
                "updated_at": now_iso,
                "rca": {"root_cause_service": "order", "category": "service_unavailable", "confidence": 0.92},
            },
        ]
        selected, all_new = select_new_incident(
            incidents, baseline_snapshots=baseline, fault_started_epoch=fault_epoch
        )
        self.assertIsNotNone(selected)
        assert selected is not None
        self.assertEqual(selected["id"], "inc-primary")
        self.assertEqual(selected["root_cause_service"], "order")
        self.assertEqual(set(all_new), {"inc-weak", "inc-primary"})

    def test_extract_incident_fields_handles_flat_and_nested_shapes(self) -> None:
        flat = extract_incident_fields(
            {
                "incident_id": "i-1",
                "service": "promotion",
                "status": "OPEN",
                "root_cause_service": "promotion-db",
                "category": "database_error",
                "confidence": "0.88",
            }
        )
        self.assertEqual(flat["id"], "i-1")
        self.assertEqual(flat["root_cause_service"], "promotion-db")
        self.assertEqual(flat["category"], "database_error")
        self.assertAlmostEqual(flat["confidence"], 0.88)


class DockerAndScenarioUnitTests(unittest.TestCase):
    def test_parse_docker_port_output(self) -> None:
        self.assertEqual(
            parse_docker_port_output("0.0.0.0:28080\n[::]:28080\n"),
            "http://127.0.0.1:28080",
        )
        self.assertEqual(
            parse_docker_port_output("127.0.0.1:28085\n"),
            "http://127.0.0.1:28085",
        )
        self.assertEqual(
            parse_docker_port_output("[::]:8086\n"),
            "http://127.0.0.1:8086",
        )
        self.assertIsNone(parse_docker_port_output(""))

    def test_promotion_db_error_executes_two_separate_psql_statements_and_restores(self) -> None:
        recorded_cmds: list[list[str]] = []

        def fake_run(cmd, **kwargs):
            recorded_cmds.append(list(cmd))
            return subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")

        docker = DockerOps(dry_run=False, run_fn=fake_run, log_stream=io.StringIO())
        endpoints = ChaosEndpoints(postgres_container="nexus_ecom-postgres-1", postgres_user="nexusmart")
        scenario = PromotionDbErrorScenario(docker, endpoints, log_stream=io.StringIO())
        scenario.prepare()
        scenario.inject()
        scenario.cleanup()

        # Must run ALTER DATABASE and pg_terminate_backend in separate psql invocations,
        # never touching any DB other than promotion_db.
        self.assertEqual(len(recorded_cmds), 3)
        self.assertIn("ALTER DATABASE promotion_db WITH ALLOW_CONNECTIONS false;", recorded_cmds[0][-1])
        self.assertIn("pg_terminate_backend", recorded_cmds[1][-1])
        self.assertIn("promotion_db", recorded_cmds[1][-1])
        self.assertIn("ALTER DATABASE promotion_db WITH ALLOW_CONNECTIONS true;", recorded_cmds[2][-1])

    def test_search_high_cpu_selects_live_or_standin_accurately(self) -> None:
        # Case 1: live search container running with /bin/sh
        docker_live = DockerOps(dry_run=False, run_fn=mock.MagicMock(), log_stream=io.StringIO())
        docker_live.container_is_running = mock.MagicMock(return_value=True)
        docker_live.has_shell = mock.MagicMock(return_value=True)
        docker_live.exec_detached_cpu_burn = mock.MagicMock()
        docker_live.stop_cpu_burn = mock.MagicMock()

        sc_live = SearchHighCpuScenario(docker_live, ChaosEndpoints(), log_stream=io.StringIO())
        sc_live.prepare()
        self.assertEqual(sc_live.runtime.target_container, "nexus_ecom-search-1")
        self.assertEqual(sc_live.runtime.target_mode, "live_container")
        sc_live.inject()
        docker_live.exec_detached_cpu_burn.assert_called_once()
        sc_live.cleanup()
        docker_live.stop_cpu_burn.assert_called_once()

        # Case 2: search container missing -> stand-in e-test-search
        docker_standin = DockerOps(dry_run=False, run_fn=mock.MagicMock(), log_stream=io.StringIO())
        docker_standin.container_is_running = mock.MagicMock(return_value=False)
        docker_standin.start_search_standin = mock.MagicMock()
        docker_standin.remove_container_force = mock.MagicMock()

        sc_standin = SearchHighCpuScenario(docker_standin, ChaosEndpoints(), log_stream=io.StringIO())
        sc_standin.prepare()
        self.assertEqual(sc_standin.runtime.target_container, "e-test-search")
        self.assertEqual(sc_standin.runtime.target_mode, "stand_in")
        sc_standin.inject()
        docker_standin.start_search_standin.assert_called_once()
        sc_standin.cleanup()
        docker_standin.remove_container_force.assert_called_once_with("e-test-search")

    def test_cleanup_runs_even_when_inject_raises(self) -> None:
        docker = DockerOps(dry_run=True, log_stream=io.StringIO())
        scenario = StopOrderScenario(docker, ChaosEndpoints(), log_stream=io.StringIO())
        cleanup_called = False

        def failing_inject() -> None:
            raise RuntimeError("simulated docker error")

        def track_cleanup() -> None:
            nonlocal cleanup_called
            cleanup_called = True

        scenario.inject = failing_inject  # type: ignore[method-assign]
        scenario.cleanup = track_cleanup  # type: ignore[method-assign]
        scenario.verify_recovered = lambda timeout=90.0: True  # type: ignore[method-assign]

        result = run_single_scenario(
            scenario,
            AiopsClient("http://127.0.0.1:8092"),
            dry_run=True,
            detect_timeout=10.0,
            poll_interval=0.1,
            recovery_timeout=5.0,
            trigger_scan=False,
            log_stream=io.StringIO(),
        )
        self.assertTrue(cleanup_called)
        self.assertIn("simulated docker error", result.error or "")

    def test_slow_payment_fallback_always_unpauses_on_cleanup(self) -> None:
        docker = DockerOps(dry_run=False, run_fn=mock.MagicMock(), log_stream=io.StringIO())
        docker.try_apply_tc_netem = mock.MagicMock(return_value=None)
        docker.unpause_container = mock.MagicMock(return_value=True)
        docker.pause_container = mock.MagicMock(return_value=True)

        scenario = SlowPaymentScenario(
            docker,
            ChaosEndpoints(),
            pause_secs=0.05,
            unpause_secs=0.05,
            log_stream=io.StringIO(),
        )
        scenario.prepare()
        scenario._fire_readyz_wave = mock.MagicMock()  # type: ignore[method-assign]
        scenario.inject()
        time.sleep(0.08)
        scenario.cleanup()

        self.assertEqual(scenario.runtime.injection_method, "docker_pause_pulse")
        docker.unpause_container.assert_called_with("nexus_ecom-payment-1")


class DryRunEndToEndTests(unittest.TestCase):
    def test_dry_run_all_five_scenarios_generates_json_and_markdown_without_docker(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            lock_path = Path(tmp) / "dryrun.lock"
            out_json = Path(tmp) / "results.json"
            out_md = Path(tmp) / "results.md"

            # Guard against accidental subprocess.run (Docker) calls during --dry-run!
            with mock.patch("subprocess.run", side_effect=AssertionError("docker must not be called in dry-run")):
                rc = chaos_main(
                    [
                        "--dry-run",
                        "--lock-file",
                        str(lock_path),
                        "--output",
                        str(out_json),
                        "--cooldown",
                        "0",
                    ]
                )

            self.assertEqual(rc, 0)
            self.assertFalse(lock_path.exists(), "stack lock must be released after dry-run")
            self.assertTrue(out_json.exists())
            self.assertTrue(out_md.exists())

            data = json.loads(out_json.read_text(encoding="utf-8"))
            self.assertEqual(data["mode"], "dry_run")
            self.assertEqual(data["summary"]["total_scenarios"], 5)
            self.assertEqual(data["summary"]["detected_under_120s_count"], 5)
            self.assertEqual(data["summary"]["rca_match_count"], 5)
            self.assertEqual(data["summary"]["rca_required_min"], 4)
            self.assertTrue(data["summary"]["overall_gate_passed"])

            scenario_names = [row["scenario"] for row in data["scenarios"]]
            self.assertEqual(scenario_names, list(ALL_SCENARIO_NAMES))
            search_row = next(r for r in data["scenarios"] if r["scenario"] == "search-high-cpu")
            self.assertEqual(search_row["target_mode"], "stand_in")
            self.assertEqual(search_row["target_container"], "e-test-search")

            md = out_md.read_text(encoding="utf-8")
            self.assertIn("DRY-RUN", md)
            self.assertIn("| Scenario | Target | Detection Time (s) | < 120s? | Predicted Root Cause | Expected Root Cause | RCA Match? | Recovery Time (s) |", md)
            for name in ALL_SCENARIO_NAMES:
                self.assertIn(f"`{name}`", md)
            self.assertIn("Overall Gate", md)

    def test_gate_calculation_4_of_5_passes_and_3_of_5_fails(self) -> None:
        def _make_row(name: str, svc: str, rca_ok: bool, under_120: bool = True) -> ScenarioResult:
            return ScenarioResult(
                scenario=name,
                title=name,
                target_service=svc,
                target_container=f"nexus_ecom-{svc}-1",
                target_mode="live_container",
                injection_method="test",
                fault_started_at="2026-11-21T08:00:00+00:00",
                detected_at="2026-11-21T08:00:20+00:00",
                fault_ended_at="2026-11-21T08:00:21+00:00",
                recovered_at="2026-11-21T08:00:25+00:00",
                detection_seconds=20.0 if under_120 else 125.0,
                detected=True,
                detected_under_120s=under_120,
                recovery_seconds=4.0,
                incident_id=f"inc-{name}",
                all_new_incident_ids=[f"inc-{name}"],
                predicted_root_cause_service=svc if rca_ok else "gateway",
                predicted_category="test",
                predicted_confidence=0.9,
                predicted_summary="ok",
                insufficient_data=False,
                expected_root_cause_service=svc,
                expected_categories=["test"],
                service_match=rca_ok,
                category_match=True,
                rca_match=rca_ok,
                rca_reason="ok" if rca_ok else "mismatch",
            )

        rows_4_of_5 = [
            _make_row("slow-payment", "payment", True),
            _make_row("stop-order", "order", True),
            _make_row("promotion-db-error", "promotion", True),
            _make_row("nats-outage", "nats", True),
            _make_row("search-high-cpu", "search", False),
        ]
        rep_pass = build_json_report(
            rows_4_of_5,
            mode="live",
            started_at="2026-11-21T08:00:00+00:00",
            finished_at="2026-11-21T08:05:00+00:00",
            duration_seconds=300.0,
            aiops_url="http://127.0.0.1:8092",
            config={},
        )
        self.assertTrue(rep_pass["summary"]["overall_gate_passed"])
        self.assertIn("**PASS**", render_markdown_report(rep_pass))

        rows_3_of_5 = [
            _make_row("slow-payment", "payment", True),
            _make_row("stop-order", "order", True),
            _make_row("promotion-db-error", "promotion", True),
            _make_row("nats-outage", "nats", False),
            _make_row("search-high-cpu", "search", False),
        ]
        rep_fail = build_json_report(
            rows_3_of_5,
            mode="live",
            started_at="2026-11-21T08:00:00+00:00",
            finished_at="2026-11-21T08:05:00+00:00",
            duration_seconds=300.0,
            aiops_url="http://127.0.0.1:8092",
            config={},
        )
        self.assertFalse(rep_fail["summary"]["overall_gate_passed"])
        self.assertIn("**FAIL**", render_markdown_report(rep_fail))


if __name__ == "__main__":
    unittest.main()
