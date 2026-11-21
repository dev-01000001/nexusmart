"""Atomic file-lock coordination for shared Docker Compose stack (.runs/stack.lock).

Coordinates Chaos Engineering runs with Swarm H E2E runs using POSIX O_CREAT|O_EXCL
atomic lock file creation so only one runner mutates the shared stack at a time.
"""

from __future__ import annotations

import errno
import os
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable, Optional, TextIO


class LockTimeoutError(TimeoutError):
    """Raised when the stack lock cannot be acquired within the configured timeout."""


def _pid_is_alive(pid: int) -> bool:
    """Return True if a local process with the given PID appears to exist."""
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        # Process exists but belongs to another user.
        return True


def parse_lock_metadata(content: str) -> dict[str, str]:
    """Parse key=value and owner header lines from a stack lock file."""
    meta: dict[str, str] = {}
    lines = [line.strip() for line in content.splitlines() if line.strip()]
    if lines and "=" not in lines[0]:
        meta["owner"] = lines[0]
        lines = lines[1:]
    for line in lines:
        if "=" in line:
            key, value = line.split("=", 1)
            meta[key.strip()] = value.strip()
    return meta


class StackLock:
    """Atomic cross-process stack lock backed by O_CREAT | O_EXCL file creation.

    Only the process that successfully created the lock file will remove it on
    release(). Releasing an unacquired lock or calling release() multiple times
    is a safe no-op.
    """

    def __init__(
        self,
        path: Path | str = Path(".runs/stack.lock"),
        *,
        owner: str = "E",
        timeout: float = 600.0,
        poll_interval: float = 5.0,
        break_stale_local: bool = False,
        log_stream: Optional[TextIO] = None,
        sleep_fn: Callable[[float], None] = time.sleep,
        monotonic_fn: Callable[[], float] = time.monotonic,
    ) -> None:
        self.path = Path(path)
        self.owner = owner
        self.timeout = max(0.0, float(timeout))
        self.poll_interval = max(0.01, float(poll_interval))
        self.break_stale_local = break_stale_local
        self.log_stream = log_stream if log_stream is not None else sys.stderr
        self._sleep = sleep_fn
        self._monotonic = monotonic_fn
        self._acquired = False
        self._token: Optional[str] = None

    @property
    def acquired(self) -> bool:
        return self._acquired

    def _log(self, msg: str) -> None:
        if self.log_stream is not None:
            print(f"[stack-lock] {msg}", file=self.log_stream, flush=True)

    def _format_payload(self) -> str:
        started = datetime.now(timezone.utc).isoformat(timespec="seconds")
        pid = os.getpid()
        # Include a unique monotonic nonce so release verifies ownership even if PIDs recycle.
        token = f"{pid}-{time.time_ns()}"
        self._token = token
        return f"{self.owner}\npid={pid}\nstarted={started}\ntoken={token}\n"

    def _try_create(self) -> bool:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        payload = self._format_payload().encode("utf-8")
        try:
            fd = os.open(
                str(self.path),
                os.O_CREAT | os.O_EXCL | os.O_WRONLY,
                0o644,
            )
        except OSError as exc:
            if exc.errno == errno.EEXIST:
                self._token = None
                return False
            raise
        try:
            os.write(fd, payload)
            os.fsync(fd)
        finally:
            os.close(fd)
        self._acquired = True
        return True

    def read_holder(self) -> tuple[str, dict[str, str]]:
        """Read current lock file content and parsed metadata (best-effort)."""
        try:
            raw = self.path.read_text(encoding="utf-8")
        except OSError:
            return "", {}
        return raw.strip(), parse_lock_metadata(raw)

    def _is_stale_local_lock(self, meta: dict[str, str]) -> bool:
        if not self.break_stale_local:
            return False
        pid_str = meta.get("pid", "")
        if not pid_str.isdigit():
            return False
        return not _pid_is_alive(int(pid_str))

    def acquire(self) -> "StackLock":
        """Atomically acquire the stack lock, waiting until available or timeout."""
        if self._acquired:
            return self

        deadline = self._monotonic() + self.timeout
        logged_wait = False

        while True:
            if self._try_create():
                self._log(f"acquired {self.path} (owner={self.owner}, pid={os.getpid()})")
                return self

            raw, meta = self.read_holder()
            holder_desc = raw.replace("\n", ", ") if raw else "unknown"

            if self._is_stale_local_lock(meta):
                self._log(
                    f"detected dead local PID in {self.path} ({holder_desc}); "
                    f"removing stale lock because break_stale_local=True"
                )
                try:
                    self.path.unlink()
                except FileNotFoundError:
                    pass
                continue

            remaining = deadline - self._monotonic()
            if remaining <= 0:
                raise LockTimeoutError(
                    f"Timed out after {self.timeout:.1f}s waiting for stack lock "
                    f"{self.path} (held by: {holder_desc})"
                )

            stale_hint = ""
            pid_str = meta.get("pid", "")
            if pid_str.isdigit() and not _pid_is_alive(int(pid_str)):
                stale_hint = " [note: local PID not running; pass --break-stale-lock if orphaned]"

            if not logged_wait:
                self._log(
                    f"waiting for {self.path} (held by: {holder_desc}; "
                    f"timeout={self.timeout:.0f}s){stale_hint}"
                )
                logged_wait = True
            else:
                self._log(
                    f"still waiting for {self.path} ({remaining:.0f}s left, "
                    f"held by: {holder_desc}){stale_hint}"
                )

            self._sleep(min(self.poll_interval, max(0.01, remaining)))

    def release(self) -> bool:
        """Release the lock file ONLY if this instance acquired and still owns it."""
        if not self._acquired:
            return False

        removed = False
        try:
            raw, meta = self.read_holder()
            if self._token and meta.get("token") and meta.get("token") != self._token:
                self._log(
                    f"skipping unlock of {self.path}: token mismatch "
                    f"(expected={self._token}, found={meta.get('token')})"
                )
            elif raw or self.path.exists():
                self.path.unlink()
                removed = True
                self._log(f"released {self.path}")
        except FileNotFoundError:
            pass
        finally:
            self._acquired = False
            self._token = None
        return removed

    def __enter__(self) -> "StackLock":
        return self.acquire()

    def __exit__(self, exc_type, exc_val, exc_tb) -> None:
        self.release()
