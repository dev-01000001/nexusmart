"""The 5 NexusMart Chaos Engineering fault-injection scenarios (UC-OPS08).

Scenarios:
  1. slow-payment       - Latency injection on payment (tc netem -> docker pause/unpause pulse fallback)
  2. stop-order         - Stop nexus_ecom-order-1 container
  3. promotion-db-error - Isolate ONLY promotion_db in postgres (ALLOW_CONNECTIONS false + terminate)
  4. nats-outage        - Stop nexus_ecom-nats-1 container
  5. search-high-cpu    - Bounded CPU burn in nexus_ecom-search-1 or labeled e-test-search stand-in

Every scenario implements idempotent cleanup() and verify_recovered().
"""

from __future__ import annotations

import sys
import threading
import time
from dataclasses import dataclass, field
from typing import Any, Optional, Sequence, TextIO

try:
    from .aiops_client import (
        SCENARIO_RCA_EXPECTATIONS,
        TrafficTarget,
        http_request_status,
    )
    from .docker_ops import DockerOps
except ImportError:
    from aiops_client import (
        SCENARIO_RCA_EXPECTATIONS,
        TrafficTarget,
        http_request_status,
    )
    from docker_ops import DockerOps


@dataclass
class ChaosEndpoints:
    """Resolved endpoints and container names for the live stack."""

    aiops_url: str = "http://127.0.0.1:8092"
    gateway_url: str = "http://127.0.0.1:28080"
    payment_url: str = "http://127.0.0.1:8086"
    promotion_url: str = "http://127.0.0.1:8084"
    order_url: str = "http://127.0.0.1:8085"
    nats_monitor_url: str = "http://127.0.0.1:8222"
    postgres_container: str = "nexus_ecom-postgres-1"
    postgres_user: str = "nexusmart"
    netem_images: tuple[str, ...] = ("nicolaka/netshoot:latest", "nicolaka/netshoot")
    buyer_bearer_token: str = ""


@dataclass
class ScenarioRuntime:
    """Mutable state tracked during a single scenario execution."""

    target_container: str
    target_mode: str = "live_container"  # "live_container" | "stand_in"
    injection_method: str = ""
    details: dict[str, Any] = field(default_factory=dict)


class ChaosScenario:
    """Base class for a fault-injection scenario."""

    name: str = ""
    title: str = ""
    target_service: str = ""

    def __init__(
        self,
        docker: DockerOps,
        endpoints: ChaosEndpoints,
        *,
        log_stream: Optional[TextIO] = None,
    ) -> None:
        self.docker = docker
        self.endpoints = endpoints
        self.log_stream = log_stream if log_stream is not None else sys.stderr
        self.runtime = ScenarioRuntime(
            target_container=self.docker.container_name(self.target_service),
        )

    def _log(self, msg: str) -> None:
        if self.log_stream is not None:
            print(f"[chaos:{self.name}] {msg}", file=self.log_stream, flush=True)

    @property
    def expected_service(self) -> str:
        return SCENARIO_RCA_EXPECTATIONS[self.name].expected_service

    @property
    def expected_categories(self) -> list[str]:
        return list(SCENARIO_RCA_EXPECTATIONS[self.name].expected_categories)

    def prepare(self) -> None:
        """Inspect pre-fault target metadata without mutating the stack."""

    def traffic_targets(self) -> list[TrafficTarget]:
        """Return background HTTP load targets to run during the fault window."""
        return []

    def recovery_probe_urls(self) -> list[str]:
        """Optional healthy URLs to tick lightly during cooldown so Prometheus rates normalize."""
        return []

    def inject(self) -> None:
        """Inject the fault (must be reversible via cleanup())."""
        raise NotImplementedError

    def cleanup(self) -> None:
        """Idempotently restore the injected fault; safe to call multiple times or on partial inject."""
        raise NotImplementedError

    def verify_recovered(self, timeout: float = 90.0) -> bool:
        """Wait until the affected service/dependency returns to healthy state."""
        raise NotImplementedError

    def dry_run_simulated_incident(self, fault_started_iso: str) -> dict[str, Any]:
        """Synthetic incident payload used exclusively in --dry-run mode to exercise report logic."""
        expectation = SCENARIO_RCA_EXPECTATIONS[self.name]
        return {
            "id": f"dryrun-{self.name}",
            "service": self.target_service,
            "status": "open",
            "opened_at": fault_started_iso,
            "updated_at": fault_started_iso,
            "rca": {
                "root_cause_service": expectation.expected_service,
                "category": expectation.expected_categories[0],
                "summary": f"[DRY-RUN] Simulated AIOps detection for {self.name}",
                "confidence": 0.95,
                "insufficient_data": False,
            },
        }


def _wait_http_200(url: str, *, timeout: float = 60.0, poll_interval: float = 2.0) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status, _, err = http_request_status(url, timeout=4.0)
        if err is None and status == 200:
            return True
        time.sleep(min(poll_interval, max(0.1, deadline - time.monotonic())))
    return False


class SlowPaymentScenario(ChaosScenario):
    """Scenario 1: Inject latency into payment (port 8086, nexus_ecom-payment-1).

    Primary strategy: `tc netem delay 1200ms 100ms` via ephemeral NET_ADMIN container sharing
    payment's network namespace (if a local image with `tc` is cached and supported).
    Automatic fallback: synchronized `docker pause`/`docker unpause` pulsing (pause 1.2s /
    unpause 0.25s) paired with concurrent `/readyz` & `/healthz` bursts so payment's server-side
    `http_request_duration_seconds` p95 spikes > 0.8s while keeping Prometheus `up == 1`.
    """

    name = "slow-payment"
    title = "Làm chậm payment"
    target_service = "payment"

    def __init__(
        self,
        docker: DockerOps,
        endpoints: ChaosEndpoints,
        *,
        pause_secs: float = 1.2,
        unpause_secs: float = 0.25,
        netem_delay_ms: int = 1200,
        log_stream: Optional[TextIO] = None,
    ) -> None:
        super().__init__(docker, endpoints, log_stream=log_stream)
        self.pause_secs = pause_secs
        self.unpause_secs = unpause_secs
        self.netem_delay_ms = netem_delay_ms
        self._netem_image: Optional[str] = None
        self._pulse_stop = threading.Event()
        self._pulse_thread: Optional[threading.Thread] = None
        self._pulse_cycles = 0

    def prepare(self) -> None:
        self.runtime.target_container = self.docker.container_name("payment")
        self.runtime.target_mode = "live_container"

    def traffic_targets(self) -> list[TrafficTarget]:
        base = self.endpoints.payment_url.rstrip("/")
        return [
            TrafficTarget(url=f"{base}/readyz", interval_secs=0.35, workers=2, timeout_secs=5.0),
            TrafficTarget(url=f"{base}/healthz", interval_secs=0.75, workers=1, timeout_secs=5.0),
        ]

    def recovery_probe_urls(self) -> list[str]:
        base = self.endpoints.payment_url.rstrip("/")
        return [f"{base}/readyz", f"{base}/healthz"]

    def _fire_readyz_wave(self, count: int = 8) -> None:
        """Fire a tight staggered wave of /readyz requests right across the pause boundary."""
        url = f"{self.endpoints.payment_url.rstrip('/')}/readyz"

        def _hit(delay_s: float) -> None:
            if delay_s > 0:
                time.sleep(delay_s)
            if not self._pulse_stop.is_set():
                http_request_status(url, timeout=max(4.0, self.pause_secs + 2.5))

        for i in range(count):
            th = threading.Thread(target=_hit, args=(i * 0.001,), daemon=True)
            th.start()

    def _pulse_loop(self) -> None:
        container = self.runtime.target_container
        while not self._pulse_stop.is_set():
            # Unpause window so Prometheus scrape + health probes complete cleanly.
            self.docker.unpause_container(container)
            lead_unpause = max(0.05, self.unpause_secs - 0.02)
            if self._pulse_stop.wait(lead_unpause):
                break

            # Launch a staggered burst of /readyz calls (SELECT 1 to Postgres) right as we freeze
            # the container so multiple in-flight requests record >= pause_secs inside Axum.
            if not self.docker.dry_run:
                self._fire_readyz_wave(count=8)
                time.sleep(0.002)

            if self._pulse_stop.is_set():
                break
            self.docker.pause_container(container)
            self._pulse_cycles += 1
            if self._pulse_stop.wait(self.pause_secs):
                break

        # Guarantee unpaused before exiting the loop.
        self.docker.unpause_container(container)

    def inject(self) -> None:
        container = self.runtime.target_container
        netem_img = self.docker.try_apply_tc_netem(
            container,
            delay_ms=self.netem_delay_ms,
            jitter_ms=100,
            candidate_images=self.endpoints.netem_images,
        )
        if netem_img:
            self._netem_image = netem_img
            self.runtime.injection_method = "tc_netem"
            self.runtime.details["netem_image"] = netem_img
            self.runtime.details["delay_ms"] = self.netem_delay_ms
            return

        self.runtime.injection_method = "docker_pause_pulse"
        self.runtime.details["pause_secs"] = self.pause_secs
        self.runtime.details["unpause_secs"] = self.unpause_secs
        self._log(
            f"tc netem unavailable; using docker pause/unpause pulsing "
            f"(pause={self.pause_secs}s, unpause={self.unpause_secs}s) on {container}"
        )
        self._pulse_stop.clear()
        if not self.docker.dry_run:
            self._pulse_thread = threading.Thread(
                target=self._pulse_loop,
                daemon=True,
                name="chaos-payment-pulse",
            )
            self._pulse_thread.start()

    def cleanup(self) -> None:
        container = self.runtime.target_container
        self._pulse_stop.set()
        if self._pulse_thread is not None and self._pulse_thread.is_alive():
            self._pulse_thread.join(timeout=self.pause_secs + 4.0)
        self._pulse_thread = None

        if self._netem_image:
            self.docker.remove_tc_netem(container, self._netem_image)
            self._netem_image = None

        # Always unpause idempotently so payment can never be left frozen.
        self.docker.unpause_container(container)
        self.runtime.details["pulse_cycles"] = self._pulse_cycles

    def verify_recovered(self, timeout: float = 90.0) -> bool:
        if self.docker.dry_run:
            return True
        if not self.docker.wait_container_healthy(self.runtime.target_container, timeout=timeout):
            return False
        base = self.endpoints.payment_url.rstrip("/")
        return _wait_http_200(f"{base}/readyz", timeout=min(timeout, 30.0), poll_interval=1.5)


class StopOrderScenario(ChaosScenario):
    """Scenario 2: Stop nexus_ecom-order-1 while traffic hits gateway /api/v1/orders and order /healthz."""

    name = "stop-order"
    title = "Tắt order"
    target_service = "order"

    def prepare(self) -> None:
        self.runtime.target_container = self.docker.container_name("order")
        self.runtime.target_mode = "live_container"
        self.runtime.injection_method = "docker_stop"

    def traffic_targets(self) -> list[TrafficTarget]:
        gw_base = self.endpoints.gateway_url.rstrip("/")
        order_base = self.endpoints.order_url.rstrip("/")
        headers: dict[str, str] = {}
        if self.endpoints.buyer_bearer_token:
            headers["Authorization"] = f"Bearer {self.endpoints.buyer_bearer_token}"
        return [
            TrafficTarget(
                url=f"{gw_base}/api/v1/orders",
                interval_secs=1.0,
                workers=1,
                timeout_secs=4.0,
                headers=headers,
            ),
            TrafficTarget(
                url=f"{order_base}/healthz",
                interval_secs=0.75,
                workers=1,
                timeout_secs=3.0,
            ),
        ]

    def recovery_probe_urls(self) -> list[str]:
        return [f"{self.endpoints.order_url.rstrip('/')}/healthz"]

    def inject(self) -> None:
        self.docker.stop_container(self.runtime.target_container)

    def cleanup(self) -> None:
        if self.docker.dry_run:
            self.docker.start_container(self.runtime.target_container)
            return
        if not self.docker.container_is_running(self.runtime.target_container):
            self.docker.start_container(self.runtime.target_container)

    def verify_recovered(self, timeout: float = 90.0) -> bool:
        if self.docker.dry_run:
            return True
        if not self.docker.wait_container_healthy(self.runtime.target_container, timeout=timeout):
            return False
        order_base = self.endpoints.order_url.rstrip("/")
        return _wait_http_200(f"{order_base}/healthz", timeout=min(timeout, 30.0), poll_interval=1.5)


class PromotionDbErrorScenario(ChaosScenario):
    """Scenario 3: Isolate ONLY promotion_db in nexus_ecom-postgres-1 (never touch other DBs)."""

    name = "promotion-db-error"
    title = "Lỗi DB promotion"
    target_service = "promotion"

    _DISABLE_CONNECT_SQL = "ALTER DATABASE promotion_db WITH ALLOW_CONNECTIONS false;"
    _TERMINATE_BACKENDS_SQL = (
        "SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
        "WHERE datname = 'promotion_db' AND pid <> pg_backend_pid();"
    )
    _RESTORE_SQL = "ALTER DATABASE promotion_db WITH ALLOW_CONNECTIONS true;"

    def prepare(self) -> None:
        self.runtime.target_container = self.docker.container_name("promotion")
        self.runtime.target_mode = "live_container"
        self.runtime.injection_method = "postgres_disallow_promotion_db"
        self.runtime.details["postgres_container"] = self.endpoints.postgres_container
        self.runtime.details["isolated_database"] = "promotion_db"

    def traffic_targets(self) -> list[TrafficTarget]:
        promo_base = self.endpoints.promotion_url.rstrip("/")
        gw_base = self.endpoints.gateway_url.rstrip("/")
        return [
            # /readyz runs pgStore.Ping() -> HTTP 503 + http_requests_total{service="promotion",status="503"}.
            TrafficTarget(url=f"{promo_base}/readyz", interval_secs=0.5, workers=2, timeout_secs=4.0),
            # Public gateway routes hitting promotion gRPC (paced safely under 60/min flash-sale rate limit).
            TrafficTarget(url=f"{gw_base}/api/v1/flash-sales", interval_secs=2.0, workers=1, timeout_secs=4.0),
            TrafficTarget(url=f"{gw_base}/api/v1/vouchers", interval_secs=1.5, workers=1, timeout_secs=4.0),
        ]

    def recovery_probe_urls(self) -> list[str]:
        return [f"{self.endpoints.promotion_url.rstrip('/')}/readyz"]

    def inject(self) -> None:
        # Execute as two separate psql commands because PostgreSQL forbids ALTER DATABASE
        # inside a multi-statement implicit transaction block (`psql -c "s1; s2;"`).
        self.docker.exec_psql(
            self.endpoints.postgres_container,
            user=self.endpoints.postgres_user,
            database="postgres",
            sql=self._DISABLE_CONNECT_SQL,
        )
        self.docker.exec_psql(
            self.endpoints.postgres_container,
            user=self.endpoints.postgres_user,
            database="postgres",
            sql=self._TERMINATE_BACKENDS_SQL,
        )

    def cleanup(self) -> None:
        last_err: Optional[Exception] = None
        for attempt in range(3):
            try:
                self.docker.exec_psql(
                    self.endpoints.postgres_container,
                    user=self.endpoints.postgres_user,
                    database="postgres",
                    sql=self._RESTORE_SQL,
                )
                return
            except Exception as exc:
                last_err = exc
                self._log(f"retry {attempt + 1}/3 restoring promotion_db connections: {exc}")
                time.sleep(1.0)
        if last_err is not None:
            raise last_err

    def verify_recovered(self, timeout: float = 90.0) -> bool:
        if self.docker.dry_run:
            return True
        promo_base = self.endpoints.promotion_url.rstrip("/")
        return _wait_http_200(f"{promo_base}/readyz", timeout=timeout, poll_interval=2.0)


class NatsOutageScenario(ChaosScenario):
    """Scenario 4: Stop nexus_ecom-nats-1 cleanly so AIOps NATS probe and services detect broker loss."""

    name = "nats-outage"
    title = "Mất NATS"
    target_service = "nats"

    def prepare(self) -> None:
        self.runtime.target_container = self.docker.container_name("nats")
        self.runtime.target_mode = "live_container"
        self.runtime.injection_method = "docker_stop"

    def traffic_targets(self) -> list[TrafficTarget]:
        nats_base = self.endpoints.nats_monitor_url.rstrip("/")
        gw_base = self.endpoints.gateway_url.rstrip("/")
        return [
            TrafficTarget(url=f"{nats_base}/healthz", interval_secs=1.0, workers=1, timeout_secs=3.0),
            TrafficTarget(url=f"{gw_base}/healthz", interval_secs=2.0, workers=1, timeout_secs=3.0),
        ]

    def recovery_probe_urls(self) -> list[str]:
        return [
            f"{self.endpoints.nats_monitor_url.rstrip('/')}/healthz",
            f"{self.endpoints.promotion_url.rstrip('/')}/readyz",
        ]

    def inject(self) -> None:
        self.docker.stop_container(self.runtime.target_container)

    def cleanup(self) -> None:
        if self.docker.dry_run:
            self.docker.start_container(self.runtime.target_container)
            return
        if not self.docker.container_is_running(self.runtime.target_container):
            self.docker.start_container(self.runtime.target_container)

    def verify_recovered(self, timeout: float = 90.0) -> bool:
        if self.docker.dry_run:
            return True
        if not self.docker.wait_container_healthy(self.runtime.target_container, timeout=timeout):
            return False
        nats_base = self.endpoints.nats_monitor_url.rstrip("/")
        if not _wait_http_200(f"{nats_base}/healthz", timeout=min(timeout, 30.0), poll_interval=1.5):
            return False
        # Also verify a NATS-connected service (promotion) reports ready again.
        promo_base = self.endpoints.promotion_url.rstrip("/")
        return _wait_http_200(f"{promo_base}/readyz", timeout=min(timeout, 30.0), poll_interval=1.5)


class SearchHighCpuScenario(ChaosScenario):
    """Scenario 5: High CPU on search (live nexus_ecom-search-1 or labeled e-test-search stand-in)."""

    name = "search-high-cpu"
    title = "CPU cao ở search"
    target_service = "search"
    standin_name = "e-test-search"
    standin_image = "alpine:3.20"
    pid_file = "/tmp/chaos-cpu-burn.pids"

    def __init__(
        self,
        docker: DockerOps,
        endpoints: ChaosEndpoints,
        *,
        cpu_workers: int = 2,
        standin_cpus: str = "1.5",
        log_stream: Optional[TextIO] = None,
    ) -> None:
        super().__init__(docker, endpoints, log_stream=log_stream)
        self.cpu_workers = max(1, cpu_workers)
        self.standin_cpus = standin_cpus

    def prepare(self) -> None:
        live_name = self.docker.container_name("search")
        if (
            not self.docker.dry_run
            and self.docker.container_is_running(live_name)
            and self.docker.has_shell(live_name)
        ):
            self.runtime.target_container = live_name
            self.runtime.target_mode = "live_container"
            self.runtime.injection_method = "docker_exec_cpu_burn"
        else:
            self.runtime.target_container = self.standin_name
            self.runtime.target_mode = "stand_in"
            self.runtime.injection_method = "stand_in_alpine_cpu_burn"
        self.runtime.details["cpu_workers"] = self.cpu_workers
        self.runtime.details["target_mode"] = self.runtime.target_mode
        self.runtime.details["target_container"] = self.runtime.target_container

    def traffic_targets(self) -> list[TrafficTarget]:
        # Keep light gateway traffic so overall system metrics stay active.
        gw_base = self.endpoints.gateway_url.rstrip("/")
        return [
            TrafficTarget(url=f"{gw_base}/healthz", interval_secs=2.0, workers=1, timeout_secs=3.0),
        ]

    def inject(self) -> None:
        if self.runtime.target_mode == "live_container":
            self.docker.exec_detached_cpu_burn(
                self.runtime.target_container,
                workers=self.cpu_workers,
                pid_file=self.pid_file,
            )
        else:
            self.docker.start_search_standin(
                name=self.standin_name,
                image=self.standin_image,
                workers=self.cpu_workers,
                cpus=self.standin_cpus,
            )

    def cleanup(self) -> None:
        if self.runtime.target_mode == "live_container":
            self.docker.stop_cpu_burn(self.runtime.target_container, pid_file=self.pid_file)
        else:
            self.docker.remove_container_force(self.standin_name)

    def verify_recovered(self, timeout: float = 90.0) -> bool:
        if self.docker.dry_run:
            return True
        if self.runtime.target_mode == "stand_in":
            # Stand-in container must be completely removed.
            deadline = time.monotonic() + min(timeout, 30.0)
            while time.monotonic() < deadline:
                if not self.docker.container_exists(self.standin_name):
                    return True
                self.docker.remove_container_force(self.standin_name)
                time.sleep(1.0)
            return not self.docker.container_exists(self.standin_name)
        return self.docker.wait_container_healthy(self.runtime.target_container, timeout=timeout)


ALL_SCENARIO_NAMES: tuple[str, ...] = (
    "slow-payment",
    "stop-order",
    "promotion-db-error",
    "nats-outage",
    "search-high-cpu",
)

SCENARIO_REGISTRY: dict[str, type[ChaosScenario]] = {
    "slow-payment": SlowPaymentScenario,
    "stop-order": StopOrderScenario,
    "promotion-db-error": PromotionDbErrorScenario,
    "nats-outage": NatsOutageScenario,
    "search-high-cpu": SearchHighCpuScenario,
}


def build_scenarios(
    names: Sequence[str],
    docker: DockerOps,
    endpoints: ChaosEndpoints,
    *,
    log_stream: Optional[TextIO] = None,
) -> list[ChaosScenario]:
    selected: list[ChaosScenario] = []
    for raw in names:
        key = raw.strip().lower()
        if not key:
            continue
        cls = SCENARIO_REGISTRY.get(key)
        if cls is None:
            valid = ", ".join(ALL_SCENARIO_NAMES)
            raise ValueError(f"Unknown scenario '{raw}'. Valid scenarios: {valid}")
        selected.append(cls(docker, endpoints, log_stream=log_stream))
    return selected
