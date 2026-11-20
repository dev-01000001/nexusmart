#!/usr/bin/env python3
"""Trang web nội bộ theo dõi các tiến trình và swarm agy đang chạy trên máy.

Chạy:
  python3 tools/agent-monitor/monitor.py          # nghe tại http://127.0.0.1:4777
  python3 tools/agent-monitor/monitor.py --check  # in JSON trạng thái một lần rồi thoát 0
"""

from __future__ import annotations

import argparse
import datetime as dt
import glob
import json
import os
import re
import signal
import subprocess
import sys
import time
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

HOST = "127.0.0.1"
DEFAULT_PORT = 4777
WORKSPACE_ROOT = Path(__file__).resolve().parents[2]
AGY_HOME = Path.home() / ".gemini" / "antigravity-cli"
RECENT_WINDOW_SEC = 600  # 10 phút
IGNORED_DIRS = {
    "node_modules",
    ".next",
    "target",
    ".git",
    "__pycache__",
    ".turbo",
    ".cache",
}
PRIORITY_GROUPS = ["web/", "services/", "proto/", "docs/"]
MAX_FILES_PER_GROUP = 40
MAX_LOG_LINES = 80
MAX_LOG_BYTES = 16384

SENSITIVE_PATH_PATTERNS = (
    "oauth",
    "secret",
    "credential",
    "antigravity-oauth",
    ".env",
    ".pem",
    ".key",
    "id_rsa",
    "id_ed25519",
)

SECRET_REGEXES: list[tuple[re.Pattern[str], str]] = [
    (
        re.compile(
            r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----"
        ),
        "[ĐÃ CHE KHÓA RIÊNG]",
    ),
    (
        re.compile(r"(?i)\b(authorization\s*[:=]\s*)(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}"),
        r"\1\2 [ĐÃ CHE]",
    ),
    (re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{12,}"), "Bearer [ĐÃ CHE]"),
    (re.compile(r"\bya29\.[A-Za-z0-9._-]{16,}\b"), "[ĐÃ CHE OAUTH]"),
    (re.compile(r"\b1//[A-Za-z0-9._-]{16,}\b"), "[ĐÃ CHE REFRESH_TOKEN]"),
    (re.compile(r"\bAIza[0-9A-Za-z_-]{20,}\b"), "[ĐÃ CHE API_KEY]"),
    (re.compile(r"\bsk-(?:proj-|live-|test-)?[0-9A-Za-z_-]{16,}\b"), "[ĐÃ CHE API_KEY]"),
    (re.compile(r"\b(?:ghp|gho|ghu|ghs|ghr)_[0-9A-Za-z]{20,}\b"), "[ĐÃ CHE GH_TOKEN]"),
    (re.compile(r"\bgithub_pat_[0-9A-Za-z_]{20,}\b"), "[ĐÃ CHE GH_TOKEN]"),
    (re.compile(r"\bxox[baprs]-[0-9A-Za-z-]{16,}\b"), "[ĐÃ CHE SLACK_TOKEN]"),
    (
        re.compile(r"\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b"),
        "[ĐÃ CHE JWT]",
    ),
    (
        re.compile(
            r"""(?i)\b((?:api|access|refresh|oauth|auth|client|session|private|secret)[_-]?(?:key|token|secret)|password|passwd)\b(\s*[:=]\s*["']?)([^\s"'`,;}{]{8,})"""
        ),
        r"\1\2[ĐÃ CHE]",
    ),
]


def is_sensitive_path(path: Path) -> bool:
    low = str(path).lower()
    name = path.name.lower()
    if name.startswith(".env"):
        return True
    return any(pat in low for pat in SENSITIVE_PATH_PATTERNS)


def redact_secrets(text: str) -> str:
    if not text:
        return ""
    out = text
    for pattern, repl in SECRET_REGEXES:
        out = pattern.sub(repl, out)
    return out


def compact_text(text: str, limit: int = 180) -> str:
    cleaned = " ".join(redact_secrets(text).split())
    if len(cleaned) <= limit:
        return cleaned
    return cleaned[: limit - 1].rstrip() + "…"


def fmt_duration(seconds: int | float | None) -> str:
    if seconds is None or seconds < 0:
        return "—"
    s = int(seconds)
    if s < 60:
        return f"{s}s"
    m, sec = divmod(s, 60)
    if m < 60:
        return f"{m}p {sec:02d}s"
    h, m = divmod(m, 60)
    if h < 24:
        return f"{h}g {m:02d}p"
    d, h = divmod(h, 24)
    return f"{d}n {h}g"


def iso_to_epoch(ts: str | None) -> float | None:
    if not ts:
        return None
    s = ts.strip()
    try:
        if s.endswith("Z"):
            s = s[:-1] + "+00:00"
        parsed = dt.datetime.fromisoformat(s)
        if parsed.tzinfo is None:
            parsed = parsed.replace(tzinfo=dt.datetime.now().astimezone().tzinfo)
        return parsed.timestamp()
    except ValueError:
        return None


def epoch_to_local_str(ts: float | None) -> str:
    if ts is None:
        return "—"
    return dt.datetime.fromtimestamp(ts).astimezone().strftime("%H:%M:%S %d/%m")


def read_tail_text(path: Path, max_bytes: int = MAX_LOG_BYTES, max_lines: int = MAX_LOG_LINES) -> str:
    if is_sensitive_path(path) or not path.is_file():
        return ""
    try:
        size = path.stat().st_size
        if size == 0:
            return ""
        with path.open("rb") as fh:
            if size > max_bytes:
                fh.seek(size - max_bytes)
                raw = fh.read()
                nl = raw.find(b"\n")
                if nl != -1 and nl + 1 < len(raw):
                    raw = raw[nl + 1 :]
            else:
                raw = fh.read()
        text = raw.decode("utf-8", errors="replace")
        lines = text.splitlines()
        if len(lines) > max_lines:
            lines = lines[-max_lines:]
        return redact_secrets("\n".join(lines))
    except OSError:
        return ""


def read_tail_jsonl(path: Path, initial_bytes: int = 49152) -> list[dict[str, Any]]:
    if is_sensitive_path(path) or not path.is_file():
        return []
    try:
        size = path.stat().st_size
    except OSError:
        return []
    if size == 0:
        return []

    for window in (initial_bytes, 262144):
        try:
            with path.open("rb") as fh:
                truncated = size > window
                if truncated:
                    fh.seek(size - window)
                raw = fh.read()
        except OSError:
            return []
        lines = raw.decode("utf-8", errors="replace").splitlines()
        if truncated and lines:
            lines = lines[1:]
        items: list[dict[str, Any]] = []
        for line in lines:
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
                if isinstance(obj, dict):
                    items.append(obj)
            except json.JSONDecodeError:
                continue
        if items or not truncated:
            return items
    return []


def unquote_arg(val: Any) -> str:
    if val is None:
        return ""
    if isinstance(val, str):
        s = val.strip()
        if len(s) >= 2 and s[0] == '"' and s[-1] == '"':
            try:
                decoded = json.loads(s)
                if isinstance(decoded, str):
                    return decoded
            except json.JSONDecodeError:
                pass
        return s
    return str(val)


def shorten_path(raw: str) -> str:
    s = unquote_arg(raw)
    ws = str(WORKSPACE_ROOT)
    if s.startswith(ws + os.sep):
        return s[len(ws) + 1 :]
    home = str(Path.home())
    if s.startswith(home + os.sep):
        return "~/" + s[len(home) + 1 :]
    return s


def summarize_tool_calls(tool_calls: list[dict[str, Any]]) -> str:
    if not tool_calls:
        return ""
    parts: list[str] = []
    for tc in tool_calls[:2]:
        name = str(tc.get("name") or "tool")
        args = tc.get("args") if isinstance(tc.get("args"), dict) else {}
        detail = ""
        if name in ("read_file", "write_file", "edit_file") and "Path" in args:
            detail = shorten_path(args["Path"])
        elif name == "run_command":
            detail = unquote_arg(args.get("toolSummary") or args.get("CommandLine") or "")
        elif name == "swarm":
            act = unquote_arg(args.get("action") or "")
            nm = unquote_arg(args.get("name") or "")
            detail = f"{act} {nm}".strip()
        elif name in ("post_board", "read_board", "send_message"):
            detail = unquote_arg(args.get("Recipient") or args.get("seqs") or args.get("Message") or "")
        else:
            for key in ("Path", "path", "query", "name", "action", "Role", "TypeName"):
                if key in args:
                    detail = shorten_path(args[key]) if "path" in key.lower() else unquote_arg(args[key])
                    break
        detail = compact_text(detail, 70)
        parts.append(f"{name}({detail})" if detail else name)
    if len(tool_calls) > 2:
        parts.append(f"+{len(tool_calls) - 2} lệnh")
    return ", ".join(parts)


def read_transcript_activity(conv_id: str, now: float) -> dict[str, Any]:
    tpath = AGY_HOME / "brain" / conv_id / ".system_generated" / "logs" / "transcript.jsonl"
    items = read_tail_jsonl(tpath)
    if not items:
        return {
            "has_transcript": False,
            "step_count": None,
            "last_activity_age_sec": None,
            "last_activity_at": "—",
            "model": None,
            "activity": "",
            "recent_steps": [],
        }

    step_indices = [
        o["step_index"] for o in items if isinstance(o.get("step_index"), int)
    ]
    step_count = (max(step_indices) + 1) if step_indices else len(items)

    last_epoch: float | None = None
    for o in reversed(items):
        ep = iso_to_epoch(o.get("created_at"))
        if ep is not None:
            last_epoch = ep
            break

    model: str | None = None
    for o in reversed(items):
        if o.get("model"):
            model = str(o["model"])
            break

    activity = ""
    recent_steps: list[str] = []
    for o in reversed(items):
        stype = str(o.get("type") or "")
        status = str(o.get("status") or "")
        tcs = o.get("tool_calls") or []
        content = (o.get("content") or "").strip()
        line_desc = ""
        if stype == "PLANNER_RESPONSE":
            if tcs:
                line_desc = summarize_tool_calls(tcs)
            elif content:
                line_desc = compact_text(content, 130)
        elif stype == "USER_INPUT" and content and not recent_steps:
            line_desc = "Nhận yêu cầu: " + compact_text(content, 100)
        if line_desc:
            if not activity:
                activity = line_desc
            ts_ep = iso_to_epoch(o.get("created_at"))
            ts_lbl = dt.datetime.fromtimestamp(ts_ep).astimezone().strftime("%H:%M:%S") if ts_ep else "—"
            idx = o.get("step_index")
            step_lbl = f"B{idx}" if isinstance(idx, int) else "B?"
            st_tag = f" [{status}]" if status and status != "DONE" else ""
            recent_steps.append(f"[{ts_lbl}] {step_lbl}{st_tag}: {line_desc}")
            if len(recent_steps) >= 8:
                break

    recent_steps.reverse()
    age = max(0, int(now - last_epoch)) if last_epoch is not None else None
    return {
        "has_transcript": True,
        "step_count": step_count,
        "last_activity_age_sec": age,
        "last_activity_at": epoch_to_local_str(last_epoch),
        "model": model,
        "activity": activity,
        "recent_steps": recent_steps,
    }


def parse_agy_argv(argv: list[str]) -> dict[str, Any]:
    model = ""
    agent_name = ""
    effort = ""
    mode = ""
    prompt = ""
    has_print = False
    is_hub = False
    conversation_arg = ""

    i = 1
    n = len(argv)
    while i < n:
        arg = argv[i]
        nxt = argv[i + 1] if i + 1 < n else ""
        if arg == "--hub":
            is_hub = True
        elif arg == "--model" and nxt:
            model = nxt
            i += 1
        elif arg.startswith("--model="):
            model = arg.split("=", 1)[1]
        elif arg == "--agent" and nxt:
            agent_name = nxt
            i += 1
        elif arg.startswith("--agent="):
            agent_name = arg.split("=", 1)[1]
        elif arg == "--effort" and nxt:
            effort = nxt
            i += 1
        elif arg.startswith("--effort="):
            effort = arg.split("=", 1)[1]
        elif arg == "--mode" and nxt:
            mode = nxt
            i += 1
        elif arg.startswith("--mode="):
            mode = arg.split("=", 1)[1]
        elif arg == "--conversation" and nxt:
            conversation_arg = nxt
            i += 1
        elif arg.startswith("--conversation="):
            conversation_arg = arg.split("=", 1)[1]
        elif arg in ("-p", "--print", "--prompt"):
            has_print = True
            if nxt and not nxt.startswith("-"):
                prompt = nxt
                i += 1
        elif arg.startswith("--print=") or arg.startswith("--prompt="):
            has_print = True
            prompt = arg.split("=", 1)[1]
        elif arg in ("-i", "--prompt-interactive"):
            if nxt and not nxt.startswith("-"):
                prompt = nxt
                i += 1
        elif arg.startswith("--prompt-interactive="):
            prompt = arg.split("=", 1)[1]
        i += 1

    return {
        "model": model,
        "agent_name": agent_name,
        "effort": effort,
        "mode": mode,
        "prompt": prompt,
        "has_print": has_print,
        "is_hub": is_hub,
        "conversation_arg": conversation_arg,
    }


def inspect_proc_fds(pid: int) -> dict[str, Any]:
    presence_ids: list[str] = []
    conv_db_ids: list[str] = []
    run_logs: list[str] = []
    cli_log = ""
    ws_prefix = str(WORKSPACE_ROOT) + os.sep
    agy_log_prefix = str(AGY_HOME / "log") + os.sep

    fd_dir = Path(f"/proc/{pid}/fd")
    try:
        entries = list(fd_dir.iterdir())
    except OSError:
        return {
            "presence_ids": [],
            "conv_db_ids": [],
            "run_logs": [],
            "cli_log": "",
        }

    for entry in entries:
        try:
            target = os.readlink(entry)
        except OSError:
            continue
        if "/presence/" in target and target.endswith(".lock"):
            cid = os.path.basename(target)[:-5]
            if cid and cid not in presence_ids:
                presence_ids.append(cid)
        elif "/conversations/" in target and target.endswith(".db"):
            cid = os.path.basename(target)[:-3]
            if cid and cid not in conv_db_ids:
                conv_db_ids.append(cid)
        elif target.startswith(ws_prefix) and target.endswith(".log"):
            rel = target[len(ws_prefix) :]
            if rel != ".runs/monitor.log" and rel not in run_logs:
                run_logs.append(rel)
        elif target.startswith(agy_log_prefix) and target.endswith(".log"):
            cli_log = os.path.basename(target)

    return {
        "presence_ids": presence_ids,
        "conv_db_ids": conv_db_ids,
        "run_logs": sorted(run_logs),
        "cli_log": cli_log,
    }


def collect_processes(now: float) -> list[dict[str, Any]]:
    try:
        out = subprocess.check_output(
            ["ps", "-eo", "pid=,ppid=,etimes=,stat=,comm="],
            text=True,
            stderr=subprocess.DEVNULL,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError):
        return []

    procs: list[dict[str, Any]] = []
    ws_str = str(WORKSPACE_ROOT)
    for line in out.splitlines():
        parts = line.split(None, 4)
        if len(parts) < 5:
            continue
        pid_s, ppid_s, etime_s, stat, comm = parts
        if comm.strip() != "agy":
            continue
        try:
            pid = int(pid_s)
            ppid = int(ppid_s)
            etimes = int(etime_s)
        except ValueError:
            continue

        try:
            raw_cmd = Path(f"/proc/{pid}/cmdline").read_bytes()
            argv = [a.decode("utf-8", errors="replace") for a in raw_cmd.split(b"\x00") if a]
        except OSError:
            argv = []
        if not argv or os.path.basename(argv[0]) != "agy":
            continue

        try:
            cwd = os.readlink(f"/proc/{pid}/cwd")
        except OSError:
            cwd = ""

        parsed = parse_agy_argv(argv)
        fds = inspect_proc_fds(pid)

        conv_id = ""
        if not parsed["is_hub"]:
            if fds["presence_ids"]:
                conv_id = fds["presence_ids"][0]
            elif parsed["conversation_arg"]:
                conv_id = parsed["conversation_arg"]
            elif len(fds["conv_db_ids"]) == 1:
                conv_id = fds["conv_db_ids"][0]

        has_swarm_dir = bool(
            conv_id
            and (AGY_HOME / "brain" / conv_id / ".system_generated" / "swarm" / "members.json").is_file()
        )
        prompt_raw = parsed["prompt"].strip()
        is_swarm = has_swarm_dir or prompt_raw.startswith("/swarm")

        if parsed["is_hub"]:
            kind = "hub"
            kind_label = "Dịch vụ nền (hub)"
        elif is_swarm:
            kind = "swarm"
            kind_label = "Swarm"
        elif parsed["agent_name"]:
            kind = "agent"
            kind_label = f"Agent: {parsed['agent_name']}"
        elif parsed["has_print"]:
            kind = "single"
            kind_label = "Phiên thường (một lượt)"
        else:
            kind = "interactive"
            kind_label = "Phiên thường (tương tác)"

        transcript = read_transcript_activity(conv_id, now) if conv_id else {}
        model = parsed["model"] or transcript.get("model") or "mặc định"

        if prompt_raw:
            prompt_display = compact_text(prompt_raw, 180)
        elif kind == "hub":
            prompt_display = "Tiến trình nền hub của agy (không có prompt)"
        elif kind == "interactive":
            prompt_display = "Phiên tương tác trên terminal (không truyền prompt qua cờ -p)"
        else:
            prompt_display = "Không có prompt trên dòng lệnh"

        in_workspace = cwd == ws_str or cwd.startswith(ws_str + os.sep)
        procs.append(
            {
                "pid": pid,
                "ppid": ppid,
                "stat": stat,
                "elapsed_sec": etimes,
                "elapsed_human": fmt_duration(etimes),
                "model": model,
                "effort": parsed["effort"] or None,
                "kind": kind,
                "kind_label": kind_label,
                "agent_name": parsed["agent_name"] or None,
                "prompt_summary": prompt_display,
                "cwd": cwd,
                "workspace_name": os.path.basename(cwd) if cwd else "—",
                "in_current_workspace": in_workspace,
                "conversation_id": conv_id or None,
                "open_conv_db_count": len(fds["conv_db_ids"]),
                "run_log": fds["run_logs"][0] if fds["run_logs"] else None,
                "cli_log": fds["cli_log"] or None,
                "step_count": transcript.get("step_count"),
                "latest_activity": transcript.get("activity") or "",
                "last_activity_age_sec": transcript.get("last_activity_age_sec"),
                "recent_steps": transcript.get("recent_steps") or [],
            }
        )

    order = {"swarm": 0, "agent": 1, "single": 2, "interactive": 3, "hub": 4}
    procs.sort(
        key=lambda p: (
            0 if p["in_current_workspace"] else 1,
            order.get(p["kind"], 9),
            -p["elapsed_sec"],
        )
    )
    return procs


def extract_board_info(board_path: Path) -> dict[str, Any]:
    briefs: dict[str, tuple[int, str]] = {}
    last_post_by_agent: dict[str, dict[str, Any]] = {}
    recent_posts: list[dict[str, Any]] = []
    mentions_workspace = False
    ws_str = str(WORKSPACE_ROOT)

    if is_sensitive_path(board_path) or not board_path.is_file():
        return {
            "briefs": briefs,
            "last_post_by_agent": last_post_by_agent,
            "recent_posts": recent_posts,
            "total_messages": 0,
            "mentions_workspace": False,
        }

    total = 0
    try:
        with board_path.open("r", encoding="utf-8", errors="replace") as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    obj = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if not isinstance(obj, dict):
                    continue
                total += 1
                seq = obj.get("seq", total)
                agent = str(obj.get("agent") or "?")
                text = str(obj.get("text") or "")
                kind = obj.get("kind")
                tm = str(obj.get("time") or "")

                if ws_str in text or WORKSPACE_ROOT.name in text:
                    mentions_workspace = True

                lines = [ln.strip() for ln in text.splitlines() if ln.strip()]
                first_line = lines[0] if lines else ""

                if kind == "brief" and first_line.startswith("@"):
                    target = first_line.split()[0].lstrip("@").rstrip(":,")
                    body = first_line[len(target) + 1 :].strip(" :-—")
                    if not body and len(lines) > 1:
                        body = lines[1]
                    briefs[target] = (seq if isinstance(seq, int) else total, compact_text(body, 150))

                is_done = (kind != "brief") and ("[done]" in text.lower())
                last_post_by_agent[agent] = {
                    "seq": seq if isinstance(seq, int) else total,
                    "time": tm,
                    "summary": compact_text(first_line, 150),
                    "is_done": is_done,
                }
                recent_posts.append(
                    {
                        "seq": seq,
                        "agent": agent,
                        "kind": kind or "message",
                        "time": tm,
                        "summary": compact_text(" ".join(lines[:2]), 220),
                    }
                )
    except OSError:
        pass

    return {
        "briefs": briefs,
        "last_post_by_agent": last_post_by_agent,
        "recent_posts": recent_posts[-8:],
        "total_messages": total,
        "mentions_workspace": mentions_workspace,
    }


def classify_worker_status(
    name: str,
    member: dict[str, Any],
    swarm_running: bool,
    brief_seq: int,
    last_post: dict[str, Any] | None,
) -> tuple[str, str]:
    if member.get("killed"):
        return "killed", "Đã dừng (killed)"
    if not swarm_running:
        return "stopped", "Swarm đã kết thúc"
    phase = str(member.get("phase") or "")
    wake = str(member.get("wake_mode") or "")
    role = str(member.get("role") or "worker")
    if phase and phase != "RUNNING":
        return "stopped", f"Tạm dừng ({phase})"
    if wake == "working":
        if role == "coordinator":
            return "working", "Đang điều phối"
        return "working", "Đang làm việc"
    if wake == "mentions":
        if role == "coordinator":
            return "waiting", "Đang điều phối / chờ worker"
        if last_post and last_post.get("is_done") and int(last_post.get("seq") or 0) > brief_seq:
            return "done", "Đã báo xong · chờ lệnh"
        if name == "monitor":
            return "waiting", "Đang canh stall (chờ chu kỳ)"
        return "waiting", "Đang chờ (idle)"
    return "unknown", f"{phase or '?'} / {wake or '?'}"


def build_swarm_entry(
    swarm_id: str | None,
    proc: dict[str, Any] | None,
    now: float,
) -> dict[str, Any]:
    swarm_running = proc is not None
    sdir = (
        AGY_HOME / "brain" / swarm_id / ".system_generated" / "swarm"
        if swarm_id
        else None
    )
    members_file = sdir / "members.json" if sdir else None
    board_file = sdir / "board.jsonl" if sdir else None
    swarm_meta_file = sdir / "swarm.json" if sdir else None

    members_data: dict[str, Any] | None = None
    if members_file and members_file.is_file() and not is_sensitive_path(members_file):
        try:
            members_data = json.loads(members_file.read_text(encoding="utf-8", errors="replace"))
        except (OSError, json.JSONDecodeError):
            members_data = None

    created_at = ""
    if swarm_meta_file and swarm_meta_file.is_file():
        try:
            meta = json.loads(swarm_meta_file.read_text(encoding="utf-8", errors="replace"))
            created_at = str(meta.get("created_at") or "")
        except (OSError, json.JSONDecodeError):
            pass

    run_log = proc.get("run_log") if proc else None
    coord_log_tail = ""
    if run_log:
        coord_log_tail = read_tail_text(WORKSPACE_ROOT / run_log, max_bytes=8192, max_lines=30)

    if not members_data or not isinstance(members_data.get("members"), dict):
        coord_steps = proc.get("recent_steps") if proc else []
        return {
            "swarm_id": swarm_id,
            "title": compact_text((proc or {}).get("prompt_summary") or "Swarm đang khởi tạo", 90),
            "running": swarm_running,
            "pid": proc["pid"] if proc else None,
            "elapsed_human": proc["elapsed_human"] if proc else "—",
            "workspace_name": proc["workspace_name"] if proc else WORKSPACE_ROOT.name,
            "run_log": run_log,
            "created_at": created_at,
            "has_worker_state": False,
            "limitation": (
                "Chưa đọc được tệp trạng thái worker (members.json) — swarm có thể đang khởi tạo "
                "chưa sinh worker hoặc chạy ở chế độ khác. Đang hiển thị log/hoạt động của coordinator."
            ),
            "counts": {"total": 0, "working": 0, "waiting": 0, "done": 0, "stopped": 0},
            "workers": [],
            "board_recent": [],
            "board_total": 0,
            "coordinator_log_tail": coord_log_tail,
            "coordinator_steps": coord_steps or [],
        }

    board = extract_board_info(board_file) if board_file else {
        "briefs": {},
        "last_post_by_agent": {},
        "recent_posts": [],
        "total_messages": 0,
        "mentions_workspace": False,
    }

    workers: list[dict[str, Any]] = []
    counts = {"total": 0, "working": 0, "waiting": 0, "done": 0, "stopped": 0}

    for name, m in members_data["members"].items():
        if not isinstance(m, dict):
            continue
        cid = str(m.get("conversation_id") or "")
        role = str(m.get("role") or "worker")
        brief_seq, brief_text = board["briefs"].get(name, (0, ""))
        last_post = board["last_post_by_agent"].get(name)
        status_code, status_label = classify_worker_status(
            name, m, swarm_running, brief_seq, last_post
        )

        tr = read_transcript_activity(cid, now) if cid else {}
        activity = tr.get("activity") or ""
        if not activity and last_post:
            activity = f"Board: {last_post['summary']}"
        if not activity and brief_text:
            activity = f"Được giao: {brief_text}"
        if not activity:
            activity = "Chưa có hoạt động ghi nhận trong transcript"

        if role != "coordinator":
            counts["total"] += 1
            if status_code == "working":
                counts["working"] += 1
            elif status_code == "done":
                counts["done"] += 1
            elif status_code in ("killed", "stopped"):
                counts["stopped"] += 1
            else:
                counts["waiting"] += 1

        workers.append(
            {
                "name": name,
                "role": role,
                "conversation_id": cid[:8] if cid else "—",
                "model": str(m.get("applied_model") or m.get("model") or "—"),
                "workspace_mode": str(m.get("workspace_mode") or "inherit"),
                "status": status_code,
                "status_label": status_label,
                "raw_phase": str(m.get("phase") or ""),
                "raw_wake_mode": str(m.get("wake_mode") or ""),
                "step_count": tr.get("step_count"),
                "last_activity_age_sec": tr.get("last_activity_age_sec"),
                "last_activity_human": fmt_duration(tr.get("last_activity_age_sec")),
                "assignment": brief_text,
                "current_activity": activity,
                "last_board_post": last_post["summary"] if last_post else "",
            }
        )

    status_rank = {"working": 1, "waiting": 2, "done": 3, "unknown": 4, "stopped": 5, "killed": 6}
    workers.sort(
        key=lambda w: (
            0 if w["role"] == "coordinator" else 1,
            status_rank.get(w["status"], 9),
            w["name"],
        )
    )

    title = str(members_data.get("title") or "").strip()
    if not title:
        title = compact_text((proc or {}).get("prompt_summary") or f"Swarm {swarm_id[:8]}", 80)

    return {
        "swarm_id": swarm_id,
        "title": redact_secrets(title),
        "running": swarm_running,
        "pid": proc["pid"] if proc else None,
        "elapsed_human": proc["elapsed_human"] if proc else "—",
        "workspace_name": proc["workspace_name"] if proc else WORKSPACE_ROOT.name,
        "run_log": run_log,
        "created_at": created_at,
        "has_worker_state": True,
        "limitation": None,
        "counts": counts,
        "workers": workers,
        "board_recent": board["recent_posts"],
        "board_total": board["total_messages"],
        "coordinator_log_tail": coord_log_tail,
        "coordinator_steps": (proc or {}).get("recent_steps") or [],
    }


def collect_swarms(procs: list[dict[str, Any]], now: float) -> list[dict[str, Any]]:
    swarms: list[dict[str, Any]] = []
    seen_ids: set[str] = set()

    for p in procs:
        if p["kind"] != "swarm":
            continue
        cid = p.get("conversation_id")
        if cid:
            seen_ids.add(cid)
        swarms.append(build_swarm_entry(cid, p, now))

    # Gồm thêm swarm thuộc workspace này vừa chạy xong trong 2 giờ gần nhất
    brain_dir = AGY_HOME / "brain"
    if brain_dir.is_dir():
        for mp_str in glob.glob(str(brain_dir / "*" / ".system_generated" / "swarm" / "members.json")):
            mp = Path(mp_str)
            try:
                mtime = mp.stat().st_mtime
            except OSError:
                continue
            if now - mtime > 7200:
                continue
            cid = mp.parents[2].name
            if cid in seen_ids:
                continue
            board_info = extract_board_info(mp.parent / "board.jsonl")
            if not board_info["mentions_workspace"]:
                continue
            seen_ids.add(cid)
            swarms.append(build_swarm_entry(cid, None, now))

    swarms.sort(key=lambda s: (0 if s["running"] else 1, s["title"]))
    return swarms


def collect_logs(procs: list[dict[str, Any]], now: float) -> list[dict[str, Any]]:
    active_by_log: dict[str, dict[str, Any]] = {}
    for p in procs:
        if p.get("run_log"):
            active_by_log[p["run_log"]] = p

    candidates: list[Path] = []
    fixed = WORKSPACE_ROOT / ".taste-skill" / "swarm-run.log"
    if fixed.is_file():
        candidates.append(fixed)
    for pattern in (".taste-skill/research/*.log", ".runs/*.log"):
        for match in sorted(WORKSPACE_ROOT.glob(pattern)):
            if match.is_file() and match.name != "monitor.log":
                candidates.append(match)

    seen: set[Path] = set()
    logs: list[dict[str, Any]] = []
    for path in candidates:
        resolved = path.resolve()
        if resolved in seen or is_sensitive_path(path):
            continue
        seen.add(resolved)
        try:
            st = path.stat()
        except OSError:
            continue

        rel = str(path.relative_to(WORKSPACE_ROOT))
        proc = active_by_log.get(rel)
        tail = read_tail_text(path)
        fallback_note = ""
        transcript_tail = ""
        if not tail and proc:
            fallback_note = (
                "Tệp log đang 0 B — agy -p thường đệm stdout đến cuối lượt. "
                "Hiển thị các bước mới nhất từ transcript của tiến trình này."
            )
            steps = proc.get("recent_steps") or []
            if steps:
                transcript_tail = "\n".join(steps)

        logs.append(
            {
                "path": rel,
                "size_bytes": st.st_size,
                "mtime_epoch": st.st_mtime,
                "mtime_human": epoch_to_local_str(st.st_mtime),
                "age_sec": max(0, int(now - st.st_mtime)),
                "age_human": fmt_duration(max(0, int(now - st.st_mtime))),
                "active_pid": proc["pid"] if proc else None,
                "active_kind": proc["kind_label"] if proc else None,
                "tail": tail,
                "fallback_note": fallback_note,
                "transcript_tail": transcript_tail,
            }
        )

    logs.sort(key=lambda x: (0 if x["active_pid"] else 1, -x["mtime_epoch"]))
    return logs


def collect_recent_files(now: float) -> dict[str, Any]:
    grouped: dict[str, list[dict[str, Any]]] = {}
    cutoff = now - RECENT_WINDOW_SEC
    ws_str = str(WORKSPACE_ROOT)

    for root, dirs, files in os.walk(ws_str):
        dirs[:] = [d for d in dirs if d not in IGNORED_DIRS]
        for fname in files:
            fpath = os.path.join(root, fname)
            rel = os.path.relpath(fpath, ws_str)
            if rel == ".runs/monitor.log":
                continue
            try:
                st = os.lstat(fpath)
            except OSError:
                continue
            if st.st_mtime < cutoff:
                continue
            top = rel.split(os.sep, 1)[0] + "/" if os.sep in rel else "(gốc workspace)"
            grouped.setdefault(top, []).append(
                {
                    "path": rel,
                    "size_bytes": st.st_size,
                    "mtime_epoch": st.st_mtime,
                    "mtime_human": epoch_to_local_str(st.st_mtime),
                    "age_sec": max(0, int(now - st.st_mtime)),
                    "age_human": fmt_duration(max(0, int(now - st.st_mtime))),
                }
            )

    ordered_keys: list[str] = []
    for key in PRIORITY_GROUPS:
        if key in grouped:
            ordered_keys.append(key)
    for key in sorted(grouped.keys()):
        if key not in ordered_keys:
            ordered_keys.append(key)

    groups_out: list[dict[str, Any]] = []
    total_files = 0
    for key in ordered_keys:
        items = grouped[key]
        items.sort(key=lambda x: -x["mtime_epoch"])
        total_files += len(items)
        groups_out.append(
            {
                "group": key,
                "total_count": len(items),
                "shown_count": min(len(items), MAX_FILES_PER_GROUP),
                "files": items[:MAX_FILES_PER_GROUP],
            }
        )

    priority_summary = [
        {"group": key, "count": len(grouped.get(key, []))} for key in PRIORITY_GROUPS
    ]
    return {
        "window_minutes": RECENT_WINDOW_SEC // 60,
        "total_count": total_files,
        "priority_summary": priority_summary,
        "groups": groups_out,
    }


def collect_status() -> dict[str, Any]:
    now = time.time()
    procs = collect_processes(now)
    swarms = collect_swarms(procs, now)
    logs = collect_logs(procs, now)
    recent_files = collect_recent_files(now)

    active_workers = sum(s["counts"]["working"] for s in swarms if s["running"])
    total_workers = sum(s["counts"]["total"] for s in swarms if s["running"])
    running_swarms = sum(1 for s in swarms if s["running"])

    return {
        "generated_at": dt.datetime.now().astimezone().isoformat(timespec="seconds"),
        "generated_at_human": dt.datetime.now().astimezone().strftime("%H:%M:%S %d/%m/%Y"),
        "workspace": str(WORKSPACE_ROOT),
        "workspace_name": WORKSPACE_ROOT.name,
        "refresh_interval_sec": 5,
        "summary": {
            "process_count": len(procs),
            "workspace_process_count": sum(1 for p in procs if p["in_current_workspace"]),
            "running_swarm_count": running_swarms,
            "working_worker_count": active_workers,
            "total_worker_count": total_workers,
            "recent_file_count": recent_files["total_count"],
            "log_count": len(logs),
        },
        "processes": procs,
        "swarms": swarms,
        "logs": logs,
        "recent_files": recent_files,
        "limitations": [
            "Chỉ đọc từ ps, /proc/<pid>, log workspace (.taste-skill/, .runs/) và ~/.gemini/antigravity-cli/brain/ (members.json, board.jsonl, transcript.jsonl). Không ghi vào ~/.gemini và không đọc file oauth/token.",
            "Các chuỗi giống token/API key/JWT trong log hoặc prompt được tự động che bằng [ĐÃ CHE]; mọi nội dung hiển thị trên trang đều escape HTML.",
            "Tiến trình agy chạy chế độ tương tác hoặc dịch vụ nền (--hub) không luôn giữ khóa presence/*.lock khi đang rảnh nên có thể chưa liên kết tới một conversation cụ thể.",
            "Các lệnh agy -p > file.log thường đệm stdout đến khi kết thúc lượt, nên tệp log có thể là 0 B trong lúc đang chạy; khi đó bảng log sẽ bổ sung đuôi bước từ transcript.",
        ],
    }


HTML_PAGE = r"""<!doctype html>
<html lang="vi">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Theo dõi agent agy — NexusMart</title>
  <style>
    :root {
      --bg: #F6F7F5;
      --surface: #FFFFFF;
      --surface-alt: #F1F4F2;
      --border: #DCE2DE;
      --border-strong: #C5CFC9;
      --ink: #161B19;
      --muted: #58645F;
      --accent: #0F6A57;
      --accent-soft: #E6F3EF;
      --warn: #8A6116;
      --warn-soft: #FBF4E4;
      --done: #24567F;
      --done-soft: #EAF2F9;
      --danger: #A63232;
      --danger-soft: #FCECEC;
      --mono: "JetBrains Mono", "Fira Code", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      --sans: "Inter", "Segoe UI", system-ui, -apple-system, sans-serif;
    }

    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--bg);
      color: var(--ink);
      font-family: var(--sans);
      font-size: 14px;
      line-height: 1.5;
    }

    .wrap {
      max-width: 1360px;
      margin: 0 auto;
      padding: 20px 24px 48px;
    }

    header.topbar {
      display: flex;
      flex-wrap: wrap;
      align-items: flex-end;
      justify-content: space-between;
      gap: 16px;
      padding-bottom: 16px;
      border-bottom: 2px solid var(--ink);
      margin-bottom: 20px;
    }

    .brand h1 {
      margin: 0;
      font-size: 22px;
      font-weight: 700;
      letter-spacing: -0.02em;
      line-height: 1.2;
    }
    .brand .sub {
      margin-top: 4px;
      color: var(--muted);
      font-size: 13px;
    }
    .brand code {
      font-family: var(--mono);
      font-size: 12px;
      background: var(--surface-alt);
      padding: 1px 6px;
      border-radius: 3px;
    }

    .controls {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 10px;
    }

    .live-pill {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      padding: 6px 11px;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 4px;
      font-size: 13px;
      color: var(--ink);
    }
    .dot {
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: var(--accent);
      flex: 0 0 auto;
    }
    .dot.live { animation: pulse 2s ease-in-out infinite; }
    .dot.paused { background: var(--warn); animation: none; }
    .dot.error { background: var(--danger); animation: none; }
    @keyframes pulse {
      0%, 100% { opacity: 1; }
      50% { opacity: 0.35; }
    }
    @media (prefers-reduced-motion: reduce) {
      .dot.live { animation: none; }
    }

    button {
      font-family: var(--sans);
      font-size: 13px;
      font-weight: 500;
      padding: 6px 12px;
      border-radius: 4px;
      border: 1px solid var(--border-strong);
      background: var(--surface);
      color: var(--ink);
      cursor: pointer;
    }
    button:hover { background: var(--surface-alt); }
    button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
    button.primary {
      background: var(--accent);
      border-color: var(--accent);
      color: #fff;
    }
    button.primary:hover { background: #0B5546; }

    .metrics {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 12px;
      margin-bottom: 24px;
    }
    @media (max-width: 860px) {
      .metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    }
    .metric {
      background: var(--surface);
      border: 1px solid var(--border);
      border-left: 3px solid var(--border-strong);
      padding: 12px 14px;
    }
    .metric.active { border-left-color: var(--accent); }
    .metric .label {
      color: var(--muted);
      font-size: 12.5px;
    }
    .metric .val {
      font-size: 24px;
      font-weight: 700;
      letter-spacing: -0.02em;
      line-height: 1.25;
      margin-top: 2px;
    }
    .metric .note {
      font-size: 12px;
      color: var(--muted);
      margin-top: 2px;
    }

    .error-banner {
      display: none;
      background: var(--danger-soft);
      border: 1px solid #E5B4B4;
      color: var(--danger);
      padding: 10px 14px;
      margin-bottom: 18px;
      font-weight: 500;
    }
    .error-banner.show { display: block; }

    section {
      margin-bottom: 28px;
    }
    .sec-head {
      display: flex;
      flex-wrap: wrap;
      align-items: baseline;
      justify-content: space-between;
      gap: 8px;
      margin-bottom: 10px;
    }
    .sec-head h2 {
      margin: 0;
      font-size: 16.5px;
      font-weight: 700;
      letter-spacing: -0.01em;
    }
    .sec-head .hint {
      font-size: 12.5px;
      color: var(--muted);
    }

    .panel {
      background: var(--surface);
      border: 1px solid var(--border);
    }

    .table-wrap {
      overflow-x: auto;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      text-align: left;
      font-size: 13.5px;
    }
    thead th {
      background: var(--surface-alt);
      border-bottom: 1px solid var(--border);
      padding: 8px 12px;
      font-weight: 600;
      color: var(--muted);
      white-space: nowrap;
      font-size: 12.5px;
    }
    tbody td {
      padding: 9px 12px;
      border-bottom: 1px solid var(--border);
      vertical-align: top;
    }
    tbody tr:last-child td { border-bottom: none; }

    .mono { font-family: var(--mono); font-size: 12.5px; }
    .muted { color: var(--muted); }
    .nowrap { white-space: nowrap; }

    .badge {
      display: inline-block;
      padding: 2px 7px;
      border-radius: 3px;
      font-size: 12px;
      font-weight: 600;
      white-space: nowrap;
    }
    .b-working { background: var(--accent-soft); color: var(--accent); }
    .b-waiting { background: var(--warn-soft); color: var(--warn); }
    .b-done { background: var(--done-soft); color: var(--done); }
    .b-stopped { background: var(--danger-soft); color: var(--danger); }
    .b-neutral { background: var(--surface-alt); color: var(--muted); }

    .prompt-text {
      max-width: 560px;
      word-break: break-word;
    }
    .activity-sub {
      margin-top: 4px;
      font-size: 12px;
      color: var(--muted);
      font-family: var(--mono);
      word-break: break-word;
    }

    /* Swarm blocks */
    .swarm-stack {
      display: flex;
      flex-direction: column;
      gap: 16px;
    }
    .swarm-header {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
      padding: 12px 14px;
      background: var(--surface-alt);
      border-bottom: 1px solid var(--border);
    }
    .swarm-title {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 8px;
    }
    .swarm-title h3 {
      margin: 0;
      font-size: 15px;
      font-weight: 700;
    }
    .swarm-meta {
      font-size: 12.5px;
      color: var(--muted);
      display: flex;
      flex-wrap: wrap;
      gap: 12px;
    }

    .limitation-box {
      padding: 10px 14px;
      background: var(--warn-soft);
      border-bottom: 1px solid #EBD7A7;
      color: var(--warn);
      font-size: 13px;
    }

    .board-details {
      border-top: 1px solid var(--border);
      padding: 8px 14px;
      background: #FAFBF9;
    }
    .board-details summary {
      cursor: pointer;
      font-size: 13px;
      font-weight: 600;
      color: var(--ink);
      user-select: none;
    }
    .board-list {
      margin: 8px 0 4px;
      padding: 0;
      list-style: none;
      display: flex;
      flex-direction: column;
      gap: 6px;
    }
    .board-list li {
      font-size: 12.5px;
      padding: 6px 8px;
      background: var(--surface);
      border: 1px solid var(--border);
      word-break: break-word;
    }

    /* Logs */
    .log-layout {
      display: grid;
      grid-template-columns: 290px minmax(0, 1fr);
      min-height: 300px;
    }
    @media (max-width: 900px) {
      .log-layout { grid-template-columns: 1fr; }
    }
    .log-list {
      border-right: 1px solid var(--border);
      background: #FAFBF9;
    }
    @media (max-width: 900px) {
      .log-list { border-right: none; border-bottom: 1px solid var(--border); }
    }
    .log-item {
      display: block;
      width: 100%;
      text-align: left;
      border: none;
      border-bottom: 1px solid var(--border);
      border-radius: 0;
      background: transparent;
      padding: 10px 12px;
      cursor: pointer;
    }
    .log-item:hover { background: var(--surface-alt); }
    .log-item.active {
      background: var(--surface);
      box-shadow: inset 3px 0 0 var(--accent);
    }
    .log-item .path {
      font-family: var(--mono);
      font-size: 12.5px;
      font-weight: 600;
      word-break: break-all;
      color: var(--ink);
    }
    .log-item .meta {
      margin-top: 3px;
      font-size: 12px;
      color: var(--muted);
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      align-items: center;
    }

    .log-viewer {
      display: flex;
      flex-direction: column;
      min-width: 0;
    }
    .log-viewer-head {
      padding: 10px 14px;
      border-bottom: 1px solid var(--border);
      display: flex;
      flex-wrap: wrap;
      justify-content: space-between;
      align-items: center;
      gap: 8px;
      background: var(--surface);
    }
    pre.log-pre {
      margin: 0;
      padding: 12px 14px;
      font-family: var(--mono);
      font-size: 12px;
      line-height: 1.5;
      background: #1B211F;
      color: #E7ECE9;
      overflow: auto;
      max-height: 380px;
      white-space: pre-wrap;
      word-break: break-word;
      flex: 1;
    }

    /* Recent files */
    .dir-pills {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      margin-bottom: 12px;
    }
    .dir-pill {
      background: var(--surface);
      border: 1px solid var(--border);
      padding: 5px 10px;
      font-size: 13px;
    }
    .dir-pill.has-files { border-color: var(--accent); background: var(--accent-soft); }

    .files-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
      gap: 14px;
    }
    .file-group-head {
      padding: 9px 12px;
      background: var(--surface-alt);
      border-bottom: 1px solid var(--border);
      display: flex;
      justify-content: space-between;
      align-items: center;
      font-weight: 600;
    }
    .file-list {
      list-style: none;
      margin: 0;
      padding: 0;
      max-height: 320px;
      overflow-y: auto;
    }
    .file-list li {
      display: flex;
      justify-content: space-between;
      gap: 10px;
      padding: 6px 12px;
      border-bottom: 1px solid var(--border);
      font-size: 12.5px;
    }
    .file-list li:last-child { border-bottom: none; }
    .file-path {
      font-family: var(--mono);
      word-break: break-all;
    }

    .empty-state {
      padding: 18px 14px;
      color: var(--muted);
      font-size: 13.5px;
    }

    footer.limits {
      margin-top: 28px;
      padding: 14px 16px;
      background: var(--surface);
      border: 1px solid var(--border);
      font-size: 12.5px;
      color: var(--muted);
    }
    footer.limits h3 {
      margin: 0 0 6px;
      font-size: 13.5px;
      color: var(--ink);
    }
    footer.limits ul {
      margin: 0;
      padding-left: 18px;
    }
    footer.limits li { margin-bottom: 4px; }
  </style>
</head>
<body>
  <div class="wrap">
    <header class="topbar">
      <div class="brand">
        <h1>Theo dõi agent agy</h1>
        <div class="sub">
          Workspace: <code id="ws-path">—</code> · Chỉ nghe tại <code>127.0.0.1:4777</code>
        </div>
      </div>
      <div class="controls">
        <div class="live-pill">
          <span class="dot live" id="live-dot"></span>
          <span id="live-text">Đang tải...</span>
        </div>
        <button id="btn-pause" type="button">Tạm dừng</button>
        <button id="btn-refresh" class="primary" type="button">Làm mới ngay</button>
      </div>
    </header>

    <div id="err-banner" class="error-banner" role="alert"></div>

    <div class="metrics" id="metrics-bar">
      <div class="metric"><div class="label">Tiến trình agy</div><div class="val">—</div><div class="note">Đang quét</div></div>
      <div class="metric"><div class="label">Swarm đang chạy</div><div class="val">—</div><div class="note">Đang quét</div></div>
      <div class="metric"><div class="label">Worker đang làm việc</div><div class="val">—</div><div class="note">Đang quét</div></div>
      <div class="metric"><div class="label">File đổi trong 10 phút</div><div class="val">—</div><div class="note">Đang quét</div></div>
    </div>

    <section aria-labelledby="h-procs">
      <div class="sec-head">
        <h2 id="h-procs">Tiến trình agy đang chạy</h2>
        <span class="hint" id="procs-hint">Nguồn: ps &amp; /proc/&lt;pid&gt;</span>
      </div>
      <div class="panel table-wrap" id="procs-body">
        <div class="empty-state">Đang tải danh sách tiến trình...</div>
      </div>
    </section>

    <section aria-labelledby="h-swarms">
      <div class="sec-head">
        <h2 id="h-swarms">Swarm và trạng thái worker</h2>
        <span class="hint">Nguồn: ~/.gemini/antigravity-cli/brain/&lt;id&gt;/.system_generated/swarm/ &amp; transcript.jsonl</span>
      </div>
      <div class="swarm-stack" id="swarms-body">
        <div class="panel empty-state">Đang tải dữ liệu swarm...</div>
      </div>
    </section>

    <section aria-labelledby="h-logs">
      <div class="sec-head">
        <h2 id="h-logs">Đuôi log mới nhất của từng lần chạy</h2>
        <span class="hint">Nguồn: .taste-skill/swarm-run.log, .taste-skill/research/*.log, .runs/*.log</span>
      </div>
      <div class="panel" id="logs-body">
        <div class="empty-state">Đang tải danh sách log...</div>
      </div>
    </section>

    <section aria-labelledby="h-files">
      <div class="sec-head">
        <h2 id="h-files">File trong workspace thay đổi trong 10 phút gần nhất</h2>
        <span class="hint">Bỏ qua node_modules, .next, target, .git</span>
      </div>
      <div class="dir-pills" id="dir-pills"></div>
      <div id="files-body">
        <div class="panel empty-state">Đang quét thư mục workspace...</div>
      </div>
    </section>

    <footer class="limits">
      <h3>Nguồn dữ liệu và giới hạn</h3>
      <ul id="limits-list">
        <li>Đang tải...</li>
      </ul>
    </footer>
  </div>

  <script>
    const state = {
      paused: false,
      timer: null,
      selectedLogPath: null,
      openBoards: new Set(),
      lastData: null,
    };

    function esc(v) {
      if (v === null || v === undefined) return "";
      return String(v)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#39;");
    }

    function fmtBytes(n) {
      if (n === null || n === undefined) return "—";
      if (n < 1024) return n + " B";
      if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
      return (n / (1024 * 1024)).toFixed(1) + " MB";
    }

    function statusBadge(code, label) {
      let cls = "b-neutral";
      if (code === "working") cls = "b-working";
      else if (code === "waiting") cls = "b-waiting";
      else if (code === "done") cls = "b-done";
      else if (code === "killed" || code === "stopped") cls = "b-stopped";
      return `<span class="badge ${cls}">${esc(label)}</span>`;
    }

    function kindBadge(kind, label) {
      let cls = "b-neutral";
      if (kind === "swarm") cls = "b-working";
      else if (kind === "agent") cls = "b-done";
      else if (kind === "single") cls = "b-waiting";
      return `<span class="badge ${cls}">${esc(label)}</span>`;
    }

    function renderMetrics(data) {
      const s = data.summary || {};
      const bar = document.getElementById("metrics-bar");
      bar.innerHTML = `
        <div class="metric ${s.process_count > 0 ? "active" : ""}">
          <div class="label">Tiến trình agy đang chạy</div>
          <div class="val">${esc(s.process_count)}</div>
          <div class="note">${esc(s.workspace_process_count)} thuộc ${esc(data.workspace_name)}</div>
        </div>
        <div class="metric ${s.running_swarm_count > 0 ? "active" : ""}">
          <div class="label">Swarm đang hoạt động</div>
          <div class="val">${esc(s.running_swarm_count)}</div>
          <div class="note">${esc((data.swarms || []).length)} swarm hiển thị</div>
        </div>
        <div class="metric ${s.working_worker_count > 0 ? "active" : ""}">
          <div class="label">Worker đang làm việc</div>
          <div class="val">${esc(s.working_worker_count)} / ${esc(s.total_worker_count)}</div>
          <div class="note">Không tính coordinator</div>
        </div>
        <div class="metric ${s.recent_file_count > 0 ? "active" : ""}">
          <div class="label">File đổi trong ${esc(data.recent_files?.window_minutes || 10)} phút</div>
          <div class="val">${esc(s.recent_file_count)}</div>
          <div class="note">${esc(s.log_count)} tệp log được theo dõi</div>
        </div>
      `;
    }

    function renderProcesses(procs) {
      const el = document.getElementById("procs-body");
      if (!procs || procs.length === 0) {
        el.innerHTML = `<div class="empty-state">Hiện không có tiến trình <code>agy</code> nào đang chạy trên máy này.</div>`;
        return;
      }
      const rows = procs.map((p) => {
        const wsTag = p.in_current_workspace
          ? `<span class="badge b-working">${esc(p.workspace_name)}</span>`
          : `<span class="badge b-neutral">${esc(p.workspace_name)}</span>`;
        const actLine = p.latest_activity
          ? `<div class="activity-sub">Bước ${esc(p.step_count ?? "?")} · ${esc(p.latest_activity)}</div>`
          : "";
        const logLine = p.run_log
          ? `<div class="activity-sub">Log: ${esc(p.run_log)}</div>`
          : "";
        return `
          <tr>
            <td class="mono nowrap">${esc(p.pid)}</td>
            <td class="mono nowrap">${esc(p.elapsed_human)}</td>
            <td class="nowrap">${kindBadge(p.kind, p.kind_label)}</td>
            <td class="mono nowrap">${esc(p.model)}</td>
            <td class="nowrap">${wsTag}</td>
            <td class="prompt-text">
              <div>${esc(p.prompt_summary)}</div>
              ${actLine}${logLine}
            </td>
          </tr>
        `;
      }).join("");

      el.innerHTML = `
        <table>
          <thead>
            <tr>
              <th>PID</th>
              <th>Đã chạy</th>
              <th>Loại</th>
              <th>Model</th>
              <th>Workspace</th>
              <th>Prompt rút gọn &amp; hoạt động gần nhất</th>
            </tr>
          </thead>
          <tbody>${rows}</tbody>
        </table>
      `;
    }

    function renderSwarms(swarms) {
      const el = document.getElementById("swarms-body");
      if (!swarms || swarms.length === 0) {
        el.innerHTML = `<div class="panel empty-state">Chưa phát hiện swarm nào đang chạy hoặc vừa kết thúc trong workspace này.</div>`;
        return;
      }

      el.innerHTML = swarms.map((sw, idx) => {
        const key = sw.swarm_id || ("idx-" + idx);
        const runBadge = sw.running
          ? `<span class="badge b-working">Đang chạy · PID ${esc(sw.pid)}</span>`
          : `<span class="badge b-neutral">Đã kết thúc</span>`;
        const c = sw.counts || {};
        const countMeta = sw.has_worker_state
          ? `<span>Worker: <strong>${esc(c.working)}</strong> đang làm, ${esc(c.done)} đã báo xong, ${esc(c.waiting)} đang chờ</span>`
          : `<span>Chưa có bảng worker</span>`;
        const logMeta = sw.run_log ? `<span>Log: <code class="mono">${esc(sw.run_log)}</code></span>` : "";

        let bodyHtml = "";
        if (sw.limitation) {
          bodyHtml += `<div class="limitation-box">${esc(sw.limitation)}</div>`;
        }

        if (sw.has_worker_state && sw.workers && sw.workers.length > 0) {
          const wRows = sw.workers.map((w) => {
            const roleTag = w.role === "coordinator" ? ` <span class="muted">(điều phối)</span>` : "";
            const stepMeta = w.step_count !== null && w.step_count !== undefined
              ? `B${esc(w.step_count)} · ${esc(w.last_activity_human)} trước`
              : "Chưa có bước";
            const assignHtml = w.assignment
              ? `<div class="muted" style="font-size:12px;margin-top:3px">Giao việc: ${esc(w.assignment)}</div>`
              : "";
            return `
              <tr>
                <td class="mono nowrap"><strong>${esc(w.name)}</strong>${roleTag}</td>
                <td class="nowrap">${statusBadge(w.status, w.status_label)}</td>
                <td class="mono nowrap">${stepMeta}</td>
                <td class="mono nowrap">${esc(w.model)}</td>
                <td class="prompt-text">
                  <div class="mono" style="font-size:12.5px">${esc(w.current_activity)}</div>
                  ${assignHtml}
                </td>
              </tr>
            `;
          }).join("");
          bodyHtml += `
            <div class="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Thành viên</th>
                    <th>Trạng thái</th>
                    <th>Tiến độ</th>
                    <th>Model</th>
                    <th>Việc đang làm &amp; nhiệm vụ</th>
                  </tr>
                </thead>
                <tbody>${wRows}</tbody>
              </table>
            </div>
          `;
        } else {
          const fallbackText = sw.coordinator_log_tail || (sw.coordinator_steps || []).join("\n") || "Chưa có dòng log nào từ coordinator.";
          bodyHtml += `<pre class="log-pre" style="max-height:220px">${esc(fallbackText)}</pre>`;
        }

        if (sw.board_recent && sw.board_recent.length > 0) {
          const isOpen = state.openBoards.has(key) ? " open" : "";
          const posts = sw.board_recent.slice().reverse().map((m) => `
            <li>
              <span class="mono muted">#${esc(m.seq)} [${esc(m.time)}]</span>
              <strong class="mono">${esc(m.agent)}</strong>: ${esc(m.summary)}
            </li>
          `).join("");
          bodyHtml += `
            <details class="board-details" data-board-key="${esc(key)}"${isOpen}>
              <summary>Bảng tin swarm gần nhất (${esc(sw.board_recent.length)} / ${esc(sw.board_total)} tin)</summary>
              <ul class="board-list">${posts}</ul>
            </details>
          `;
        }

        return `
          <div class="panel">
            <div class="swarm-header">
              <div class="swarm-title">
                ${runBadge}
                <h3>${esc(sw.title)}</h3>
              </div>
              <div class="swarm-meta">
                <span>Đã chạy: <strong class="mono">${esc(sw.elapsed_human)}</strong></span>
                ${countMeta}
                ${logMeta}
              </div>
            </div>
            ${bodyHtml}
          </div>
        `;
      }).join("");

      el.querySelectorAll("details.board-details").forEach((det) => {
        det.addEventListener("toggle", () => {
          const k = det.getAttribute("data-board-key");
          if (!k) return;
          if (det.open) state.openBoards.add(k);
          else state.openBoards.delete(k);
        });
      });
    }

    function renderLogs(logs) {
      const el = document.getElementById("logs-body");
      if (!logs || logs.length === 0) {
        el.innerHTML = `<div class="empty-state">Chưa tìm thấy tệp log nào trong .taste-skill/ hoặc .runs/.</div>`;
        return;
      }

      if (!state.selectedLogPath || !logs.some((l) => l.path === state.selectedLogPath)) {
        state.selectedLogPath = logs[0].path;
      }
      const selected = logs.find((l) => l.path === state.selectedLogPath) || logs[0];

      const listHtml = logs.map((l) => {
        const activeCls = l.path === selected.path ? " active" : "";
        const liveTag = l.active_pid
          ? `<span class="badge b-working">PID ${esc(l.active_pid)}</span>`
          : "";
        return `
          <button type="button" class="log-item${activeCls}" data-log-path="${esc(l.path)}">
            <div class="path">${esc(l.path)}</div>
            <div class="meta">
              ${liveTag}
              <span>${esc(fmtBytes(l.size_bytes))}</span>
              <span>${esc(l.age_human)} trước</span>
            </div>
          </button>
        `;
      }).join("");

      const noteHtml = selected.fallback_note
        ? `<div class="limitation-box">${esc(selected.fallback_note)}</div>`
        : "";
      const content = selected.tail || selected.transcript_tail || "(Tệp log đang trống)";

      el.innerHTML = `
        <div class="log-layout">
          <div class="log-list">${listHtml}</div>
          <div class="log-viewer">
            <div class="log-viewer-head">
              <strong class="mono">${esc(selected.path)}</strong>
              <span class="muted" style="font-size:12.5px">
                ${esc(fmtBytes(selected.size_bytes))} · cập nhật ${esc(selected.mtime_human)}
              </span>
            </div>
            ${noteHtml}
            <pre class="log-pre">${esc(content)}</pre>
          </div>
        </div>
      `;

      el.querySelectorAll(".log-item").forEach((btn) => {
        btn.addEventListener("click", () => {
          state.selectedLogPath = btn.getAttribute("data-log-path");
          if (state.lastData) renderLogs(state.lastData.logs);
        });
      });
    }

    function renderRecentFiles(rf) {
      const pillsEl = document.getElementById("dir-pills");
      const bodyEl = document.getElementById("files-body");
      if (!rf) return;

      pillsEl.innerHTML = (rf.priority_summary || []).map((p) => `
        <div class="dir-pill ${p.count > 0 ? "has-files" : ""}">
          <span class="mono">${esc(p.group)}</span>: <strong>${esc(p.count)}</strong> file
        </div>
      `).join("");

      const groups = rf.groups || [];
      if (groups.length === 0) {
        bodyEl.innerHTML = `<div class="panel empty-state">Không có tệp nào thay đổi trong ${esc(rf.window_minutes)} phút gần nhất.</div>`;
        return;
      }

      bodyEl.innerHTML = `<div class="files-grid">` + groups.map((g) => {
        const countText = g.total_count > g.shown_count
          ? `hiển thị ${esc(g.shown_count)} / ${esc(g.total_count)} file`
          : `${esc(g.total_count)} file`;
        const rows = g.files.map((f) => `
          <li>
            <span class="file-path">${esc(f.path)}</span>
            <span class="muted nowrap">${esc(f.age_human)} trước</span>
          </li>
        `).join("");
        return `
          <div class="panel">
            <div class="file-group-head">
              <span class="mono">${esc(g.group)}</span>
              <span class="muted" style="font-weight:400;font-size:12.5px">${countText}</span>
            </div>
            <ul class="file-list">${rows}</ul>
          </div>
        `;
      }).join("") + `</div>`;
    }

    function renderLimitations(list) {
      const el = document.getElementById("limits-list");
      el.innerHTML = (list || []).map((item) => `<li>${esc(item)}</li>`).join("");
    }

    async function fetchStatus() {
      const errEl = document.getElementById("err-banner");
      const dot = document.getElementById("live-dot");
      const txt = document.getElementById("live-text");
      try {
        const res = await fetch("/api/status", { cache: "no-store" });
        if (!res.ok) throw new Error("HTTP " + res.status);
        const data = await res.json();
        state.lastData = data;
        errEl.classList.remove("show");
        document.getElementById("ws-path").textContent = data.workspace || "—";
        renderMetrics(data);
        renderProcesses(data.processes);
        renderSwarms(data.swarms);
        renderLogs(data.logs);
        renderRecentFiles(data.recent_files);
        renderLimitations(data.limitations);
        dot.className = state.paused ? "dot paused" : "dot live";
        txt.textContent = (state.paused ? "Đã tạm dừng · " : "Tự làm mới 5s · ") + (data.generated_at_human || "");
      } catch (err) {
        errEl.textContent = "Không tải được /api/status: " + (err && err.message ? err.message : String(err));
        errEl.classList.add("show");
        dot.className = "dot error";
        txt.textContent = "Mất kết nối tới server cục bộ";
      }
    }

    function resetTimer() {
      if (state.timer) clearInterval(state.timer);
      if (!state.paused) {
        state.timer = setInterval(fetchStatus, 5000);
      }
    }

    document.getElementById("btn-refresh").addEventListener("click", fetchStatus);
    document.getElementById("btn-pause").addEventListener("click", (e) => {
      state.paused = !state.paused;
      e.target.textContent = state.paused ? "Tiếp tục" : "Tạm dừng";
      const dot = document.getElementById("live-dot");
      dot.className = state.paused ? "dot paused" : "dot live";
      resetTimer();
      if (!state.paused) fetchStatus();
    });

    fetchStatus();
    resetTimer();
  </script>
</body>
</html>
"""


class MonitorHandler(BaseHTTPRequestHandler):
    def log_message(self, format: str, *args: Any) -> None:
        # Chỉ ghi lỗi 4xx/5xx để tránh làm đầy .runs/monitor.log mỗi 5 giây poll
        if len(args) >= 2 and str(args[1]).startswith(("4", "5")):
            super().log_message(format, *args)

    def _send_bytes(self, status: int, content_type: str, body: bytes) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Content-Security-Policy", "default-src 'self' 'unsafe-inline'")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:
        path = urlparse(self.path).path
        if path in ("/", "/index.html"):
            self._send_bytes(
                HTTPStatus.OK,
                "text/html; charset=utf-8",
                HTML_PAGE.encode("utf-8"),
            )
            return
        if path == "/api/status":
            try:
                payload = json.dumps(collect_status(), ensure_ascii=False).encode("utf-8")
                self._send_bytes(
                    HTTPStatus.OK,
                    "application/json; charset=utf-8",
                    payload,
                )
            except Exception as exc:  # pragma: no cover - phòng lỗi runtime bất ngờ
                err = json.dumps(
                    {"error": redact_secrets(str(exc))}, ensure_ascii=False
                ).encode("utf-8")
                self._send_bytes(
                    HTTPStatus.INTERNAL_SERVER_ERROR,
                    "application/json; charset=utf-8",
                    err,
                )
            return
        self._send_bytes(
            HTTPStatus.NOT_FOUND,
            "application/json; charset=utf-8",
            b'{"error":"not found"}',
        )


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Trang web nội bộ theo dõi agent agy (chỉ localhost)."
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="In JSON trạng thái một lần ra stdout rồi thoát với mã 0.",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=DEFAULT_PORT,
        help=f"Cổng lắng nghe trên {HOST} (mặc định: {DEFAULT_PORT}).",
    )
    args = parser.parse_args()

    if args.check:
        print(json.dumps(collect_status(), ensure_ascii=False, indent=2))
        return 0

    ThreadingHTTPServer.allow_reuse_address = True
    server = ThreadingHTTPServer((HOST, args.port), MonitorHandler)

    def _shutdown(_signum: int, _frame: Any) -> None:
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, _shutdown)
    print(
        f"[agent-monitor] Đang chạy tại http://{HOST}:{args.port} (workspace: {WORKSPACE_ROOT})",
        flush=True,
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
