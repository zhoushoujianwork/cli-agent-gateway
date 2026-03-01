from __future__ import annotations

import hashlib
import json
import os
import shlex
import subprocess
from typing import Any

from cli_agent_gateway.core.contracts import InboundMessage


def _sanitize(value: Any) -> str:
    if value is None:
        return ""
    return str(value).replace("\x00", "").strip()


class CommandChannelAdapter:
    channel_id = "command"

    def __init__(self, fetch_cmd: str, send_cmd: str, fetch_timeout_sec: int = 120, send_timeout_sec: int = 120):
        self.fetch_cmd = fetch_cmd
        self.send_cmd = send_cmd
        self.fetch_timeout_sec = fetch_timeout_sec
        self.send_timeout_sec = send_timeout_sec

    def _run(self, cmd: str, *, env: dict[str, str] | None = None, timeout_sec: int = 60) -> tuple[int, str, str]:
        proc = subprocess.run(
            cmd,
            shell=True,
            text=True,
            capture_output=True,
            env=env,
            timeout=timeout_sec,
        )
        return proc.returncode, proc.stdout, proc.stderr

    def fetch(self) -> list[InboundMessage]:
        rc, out, err = self._run(self.fetch_cmd, timeout_sec=self.fetch_timeout_sec)
        if rc != 0:
            raise RuntimeError(f"fetch failed rc={rc} err={err.strip()}")

        raw = out.strip()
        if not raw:
            return []

        payload: list[dict[str, Any]]
        try:
            parsed = json.loads(raw)
            if isinstance(parsed, list):
                payload = [x for x in parsed if isinstance(x, dict)]
            elif isinstance(parsed, dict):
                payload = [parsed]
            else:
                payload = []
        except Exception:
            payload = []
            for line in [ln.strip() for ln in raw.splitlines() if ln.strip()]:
                node = json.loads(line)
                if isinstance(node, dict):
                    payload.append(node)

        messages: list[InboundMessage] = []
        for node in payload:
            sender = _sanitize(node.get("from", ""))
            text = _sanitize(node.get("text", ""))
            ts = _sanitize(node.get("ts", ""))
            msg_id = _sanitize(node.get("id", ""))
            thread_id = _sanitize(node.get("thread_id", "")) or None
            if not msg_id:
                sig = f"{sender}|{text}|{ts}"
                msg_id = hashlib.sha256(sig.encode("utf-8")).hexdigest()[:24]
            if not text:
                continue
            messages.append(
                InboundMessage(
                    id=msg_id,
                    sender=sender,
                    text=text,
                    ts=ts,
                    channel=self.channel_id,
                    thread_id=thread_id,
                )
            )
        return messages

    def send(self, text: str, *, to: str, message_id: str, report_file: str = "") -> None:
        cmd_with_text = f"{self.send_cmd} {shlex.quote(text)}"
        env = os.environ.copy()
        env["SMS_TO"] = to
        env["SMS_MESSAGE_ID"] = message_id
        env["SMS_REPORT_FILE"] = report_file
        rc, _, err = self._run(cmd_with_text, env=env, timeout_sec=self.send_timeout_sec)
        if rc != 0:
            raise RuntimeError(f"send failed rc={rc} err={err.strip()}")
