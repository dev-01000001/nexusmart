"""Docker CLI and Docker Engine Unix socket helpers for chaos fault injection.

Uses only Python 3 standard library. In dry_run mode, zero Docker commands or
socket calls are executed; all actions are logged and simulated safely.
"""

from __future__ import annotations

import http.client
import json
import os
import socket
import subprocess
import sys
import time
from dataclasses import dataclass
from typing import Callable, Optional, Sequence, TextIO


class DockerCommandError(RuntimeError):
    """Raised when a required Docker CLI or API operation fails."""


@dataclass
class CommandResult:
    returncode: int
    stdout: str
    stderr: str


class _UnixHTTPConnection(http.client.HTTPConnection):
    """Minimal stdlib HTTPConnection speaking over a local Unix domain socket."""

    def __init__(self, socket_path: str, timeout: float = 5.0) -> None:
        super().__init__("localhost", timeout=timeout)
        self._socket_path = socket_path

    def connect(self) -> None:
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(self.timeout)
        sock.connect(self._socket_path)
        self.sock = sock


def parse_docker_port_output(raw: str) -> Optional[str]:
    """Parse `docker port` output (e.g. `0.0.0.0:28080` or `127.0.0.1:28085`) into a base URL."""
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        # Prefer IPv4 mappings over IPv6 `[::]:port` when both appear.
        if line.startswith("[::]"):
            continue
        if ":" in line:
            host, port = line.rsplit(":", 1)
            host = host.strip()
            port = port.strip()
            if not port.isdigit():
                continue
            if host in ("0.0.0.0", "", "*"):
                host = "127.0.0.1"
            return f"http://{host}:{port}"
    # Fallback to IPv6 line if that was the only mapping.
    for line in raw.splitlines():
        line = line.strip()
        if line.startswith("[::]:"):
            port = line.split("]:", 1)[1].strip()
            if port.isdigit():
                return f"http://127.0.0.1:{port}"
    return None


class DockerOps:
    """Safe wrapper around Docker Engine operations with dry-run support."""

    def __init__(
        self,
        *,
        dry_run: bool = False,
        docker_socket: str = "/var/run/docker.sock",
        compose_project: str = "nexus_ecom",
        compose_network: str = "nexus_ecom_default",
        log_stream: Optional[TextIO] = None,
        run_fn: Optional[Callable[..., subprocess.CompletedProcess[str]]] = None,
    ) -> None:
        self.dry_run = dry_run
        self.docker_socket = docker_socket
        self.compose_project = compose_project
        self.compose_network = compose_network
        self.log_stream = log_stream if log_stream is not None else sys.stderr
        self._run_fn = run_fn or subprocess.run
        self._socket_available = (
            not dry_run
            and not os.environ.get("DOCKER_HOST")
            and os.path.exists(docker_socket)
        )

    def _log(self, msg: str) -> None:
        if self.log_stream is not None:
            prefix = "[docker:dry-run]" if self.dry_run else "[docker]"
            print(f"{prefix} {msg}", file=self.log_stream, flush=True)

    def run_cli(
        self,
        args: Sequence[str],
        *,
        check: bool = True,
        timeout: float = 60.0,
        input_text: Optional[str] = None,
    ) -> CommandResult:
        cmd = ["docker", *args]
        if self.dry_run:
            self._log(f"would run: {' '.join(cmd)}")
            return CommandResult(returncode=0, stdout="", stderr="")

        proc = self._run_fn(
            cmd,
            input=input_text,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
        res = CommandResult(
            returncode=proc.returncode,
            stdout=(proc.stdout or "").strip(),
            stderr=(proc.stderr or "").strip(),
        )
        if check and res.returncode != 0:
            raise DockerCommandError(
                f"Command failed (rc={res.returncode}): {' '.join(cmd)}\n"
                f"stderr: {res.stderr}\nstdout: {res.stdout}"
            )
        return res

    def _socket_post(self, path: str, timeout: float = 5.0) -> tuple[int, str]:
        conn = _UnixHTTPConnection(self.docker_socket, timeout=timeout)
        try:
            conn.request("POST", path, headers={"Host": "localhost"})
            resp = conn.getresponse()
            body = resp.read().decode("utf-8", errors="replace")
            return resp.status, body
        finally:
            conn.close()

    def container_name(self, service: str) -> str:
        return f"{self.compose_project}-{service}-1"

    def resolve_service_url(
        self,
        container: str,
        container_port: int,
        fallback_url: str,
    ) -> str:
        """Discover host URL for container:port via `docker port`, else return fallback."""
        if self.dry_run:
            return fallback_url
        res = self.run_cli(["port", container, f"{container_port}/tcp"], check=False, timeout=10.0)
        if res.returncode == 0 and res.stdout:
            parsed = parse_docker_port_output(res.stdout)
            if parsed:
                return parsed
        return fallback_url

    def container_exists(self, container: str) -> bool:
        if self.dry_run:
            return True
        res = self.run_cli(["inspect", "--format", "{{.Id}}", container], check=False)
        return res.returncode == 0 and bool(res.stdout)

    def container_is_running(self, container: str) -> bool:
        if self.dry_run:
            return True
        res = self.run_cli(
            ["inspect", "--format", "{{.State.Running}}", container],
            check=False,
        )
        return res.returncode == 0 and res.stdout.strip().lower() == "true"

    def container_health_status(self, container: str) -> str:
        """Return Docker health status ('healthy', 'starting', 'unhealthy') or State.Status."""
        if self.dry_run:
            return "healthy"
        res = self.run_cli(
            [
                "inspect",
                "--format",
                "{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}",
                container,
            ],
            check=False,
        )
        if res.returncode != 0:
            return "missing"
        return res.stdout.strip().lower()

    def container_id(self, container: str) -> Optional[str]:
        if self.dry_run:
            return f"dryrun-{container}"
        res = self.run_cli(["inspect", "--format", "{{.Id}}", container], check=False)
        if res.returncode != 0 or not res.stdout:
            return None
        return res.stdout.strip()

    def image_exists_locally(self, image: str) -> bool:
        if self.dry_run:
            return False
        res = self.run_cli(["image", "inspect", image], check=False, timeout=10.0)
        return res.returncode == 0

    def has_shell(self, container: str) -> bool:
        if self.dry_run:
            return False
        res = self.run_cli(
            ["exec", container, "/bin/sh", "-c", "echo ok"],
            check=False,
            timeout=10.0,
        )
        return res.returncode == 0 and "ok" in res.stdout

    def stop_container(self, container: str, *, timeout_secs: int = 10) -> None:
        self._log(f"stopping container {container}")
        self.run_cli(["stop", "-t", str(timeout_secs), container], check=True, timeout=timeout_secs + 50.0)

    def start_container(self, container: str) -> None:
        self._log(f"starting container {container}")
        self.run_cli(["start", container], check=True, timeout=60.0)

    def remove_container_force(self, container: str) -> None:
        self._log(f"removing container {container}")
        self.run_cli(["rm", "-f", container], check=False, timeout=60.0)

    def pause_container(self, container: str) -> bool:
        """Pause a running container (uses Docker Unix socket for low-latency pulsing when available)."""
        if self.dry_run:
            self._log(f"would pause container {container}")
            return True
        if self._socket_available:
            try:
                status, body = self._socket_post(f"/containers/{container}/pause", timeout=5.0)
                if status in (204, 304):
                    return True
                if status == 409 and "already paused" in body.lower():
                    return True
            except OSError:
                self._socket_available = False
        res = self.run_cli(["pause", container], check=False, timeout=10.0)
        return res.returncode == 0 or "already paused" in res.stderr.lower()

    def unpause_container(self, container: str) -> bool:
        """Idempotently unpause a container."""
        if self.dry_run:
            self._log(f"would unpause container {container}")
            return True
        if self._socket_available:
            try:
                status, body = self._socket_post(f"/containers/{container}/unpause", timeout=5.0)
                if status in (204, 304):
                    return True
                if status == 409 and "not paused" in body.lower():
                    return True
            except OSError:
                self._socket_available = False
        res = self.run_cli(["unpause", container], check=False, timeout=10.0)
        return res.returncode == 0 or "not paused" in res.stderr.lower()

    def try_apply_tc_netem(
        self,
        container: str,
        *,
        delay_ms: int = 1200,
        jitter_ms: int = 100,
        candidate_images: Sequence[str] = (
            "nicolaka/netshoot:latest",
            "nicolaka/netshoot",
        ),
    ) -> Optional[str]:
        """Try injecting tc netem delay via a locally-cached NET_ADMIN sidecar container.

        Never pulls remote images. Returns the image name used on success, or None if
        unsupported / no local image with `tc` is available.
        """
        if self.dry_run:
            self._log(
                f"would probe tc netem delay {delay_ms}ms on {container} "
                f"(falling back to pause/unpause pulsing if unavailable)"
            )
            return None

        cid = self.container_id(container)
        if not cid:
            return None

        for img in candidate_images:
            if not img or not self.image_exists_locally(img):
                continue
            res = self.run_cli(
                [
                    "run",
                    "--rm",
                    "--pull=never",
                    f"--net=container:{cid}",
                    "--cap-add=NET_ADMIN",
                    "--entrypoint",
                    "sh",
                    img,
                    "-c",
                    f"command -v tc >/dev/null 2>&1 && "
                    f"tc qdisc replace dev eth0 root netem delay {delay_ms}ms {jitter_ms}ms",
                ],
                check=False,
                timeout=15.0,
            )
            if res.returncode == 0:
                self._log(f"applied tc netem delay {delay_ms}ms on {container} via {img}")
                return img
            self._log(f"tc netem via {img} unavailable: {res.stderr or res.stdout}")
        return None

    def remove_tc_netem(self, container: str, netem_image: str) -> bool:
        """Idempotently remove root netem qdisc from container's eth0."""
        if self.dry_run:
            self._log(f"would remove tc netem qdisc on {container}")
            return True
        cid = self.container_id(container)
        if not cid or not netem_image:
            return True
        res = self.run_cli(
            [
                "run",
                "--rm",
                "--pull=never",
                f"--net=container:{cid}",
                "--cap-add=NET_ADMIN",
                "--entrypoint",
                "sh",
                netem_image,
                "-c",
                "tc qdisc del dev eth0 root 2>/dev/null || true",
            ],
            check=False,
            timeout=15.0,
        )
        return res.returncode == 0

    def exec_psql(
        self,
        postgres_container: str,
        *,
        user: str,
        database: str,
        sql: str,
    ) -> CommandResult:
        """Execute a SQL statement via psql inside the postgres container."""
        self._log(f"psql ({postgres_container}, db={database}): {sql}")
        return self.run_cli(
            [
                "exec",
                postgres_container,
                "psql",
                "-v",
                "ON_ERROR_STOP=1",
                "-U",
                user,
                "-d",
                database,
                "-c",
                sql,
            ],
            check=True,
            timeout=20.0,
        )

    def exec_detached_cpu_burn(
        self,
        container: str,
        *,
        workers: int = 2,
        pid_file: str = "/tmp/chaos-cpu-burn.pids",
    ) -> None:
        """Start bounded busy-loop CPU workers inside a container and save PIDs."""
        script = (
            f"rm -f {pid_file}; "
            f"for i in $(seq 1 {workers}); do "
            f"(while :; do :; done) & "
            f"echo $! >> {pid_file}; "
            f"done; wait"
        )
        self._log(f"starting {workers} CPU burn workers in {container} (pids -> {pid_file})")
        self.run_cli(["exec", "-d", container, "/bin/sh", "-c", script], check=True, timeout=15.0)

    def stop_cpu_burn(
        self,
        container: str,
        *,
        pid_file: str = "/tmp/chaos-cpu-burn.pids",
    ) -> None:
        """Kill saved CPU burn worker PIDs inside a container (idempotent)."""
        script = (
            f"if [ -f {pid_file} ]; then "
            f"kill $(cat {pid_file}) 2>/dev/null || true; "
            f"rm -f {pid_file}; "
            f"fi"
        )
        self._log(f"stopping CPU burn workers in {container}")
        self.run_cli(["exec", container, "/bin/sh", "-c", script], check=False, timeout=15.0)

    def start_search_standin(
        self,
        *,
        name: str = "e-test-search",
        image: str = "alpine:3.20",
        workers: int = 2,
        cpus: str = "1.5",
    ) -> None:
        """Spin up a labeled stand-in container for search CPU spike testing."""
        self.remove_container_force(name)
        script = f"for i in $(seq 1 {workers}); do (while :; do :; done) & done; wait"
        self._log(
            f"starting labeled stand-in {name} (image={image}, service=search, "
            f"project={self.compose_project}, network={self.compose_network}, cpus={cpus})"
        )
        args = [
            "run",
            "-d",
            "--pull=never",
            "--name",
            name,
            f"--cpus={cpus}",
            "--label",
            "com.docker.compose.service=search",
            "--label",
            f"com.docker.compose.project={self.compose_project}",
        ]
        if self.compose_network:
            args.extend(["--network", self.compose_network])
        args.extend([image, "/bin/sh", "-c", script])
        self.run_cli(args, check=True, timeout=60.0)

    def wait_container_healthy(
        self,
        container: str,
        *,
        timeout: float = 90.0,
        poll_interval: float = 2.0,
    ) -> bool:
        """Wait until Docker reports container health as 'healthy' or 'running' (no healthcheck)."""
        if self.dry_run:
            return True
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            status = self.container_health_status(container)
            if status in ("healthy", "running"):
                # If container has a healthcheck, Docker returns starting/healthy/unhealthy;
                # "running" is only returned when no healthcheck is defined.
                return True
            time.sleep(min(poll_interval, max(0.1, deadline - time.monotonic())))
        return False

    def inspect_json(self, container: str) -> Optional[dict]:
        if self.dry_run:
            return None
        res = self.run_cli(["inspect", container], check=False, timeout=10.0)
        if res.returncode != 0 or not res.stdout:
            return None
        try:
            items = json.loads(res.stdout)
            return items[0] if isinstance(items, list) and items else None
        except json.JSONDecodeError:
            return None
