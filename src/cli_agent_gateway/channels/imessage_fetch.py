#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any


def load_dotenv(path: Path) -> None:
    if not path.exists():
        return
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key:
            os.environ.setdefault(key, value)


def _sanitize(value: Any) -> str:
    if value is None:
        return ""
    return str(value).replace("\x00", "").strip()


def _flatten_json_payload(raw: str) -> list[dict[str, Any]]:
    raw = raw.strip()
    if not raw:
        return []

    try:
        parsed = json.loads(raw)
        if isinstance(parsed, list):
            nodes: list[dict[str, Any]] = []
            for item in parsed:
                if isinstance(item, dict):
                    nodes.append(item)
                elif isinstance(item, list):
                    nodes.extend(x for x in item if isinstance(x, dict))
            return nodes
        if isinstance(parsed, dict):
            return [parsed]
    except Exception:
        pass

    nodes: list[dict[str, Any]] = []
    for line in [ln.strip() for ln in raw.splitlines() if ln.strip()]:
        try:
            node = json.loads(line)
        except Exception:
            continue
        if isinstance(node, dict):
            nodes.append(node)
        elif isinstance(node, list):
            nodes.extend(x for x in node if isinstance(x, dict))
    return nodes


def main() -> None:
    repo_root = Path(__file__).resolve().parents[3]
    load_dotenv(repo_root / ".env")

    chat_id = os.getenv("IMSG_CHAT_ID", "").strip()
    if not chat_id:
        print("IMSG_CHAT_ID is required", file=sys.stderr)
        raise SystemExit(2)

    limit = int(os.getenv("IMSG_FETCH_LIMIT", "30"))
    include_from_me = os.getenv("IMSG_INCLUDE_FROM_ME", "0").strip() == "1"
    imsg_bin = os.getenv("IMSG_BIN", "imsg").strip() or "imsg"

    cmd = [imsg_bin, "history", "--chat-id", chat_id, "--limit", str(limit), "--json"]
    proc = subprocess.run(cmd, text=True, capture_output=True)
    if proc.returncode != 0:
        err = proc.stderr.strip() or proc.stdout.strip()
        print(f"imsg history failed: {err}", file=sys.stderr)
        raise SystemExit(proc.returncode)

    raw_nodes = _flatten_json_payload(proc.stdout)
    messages: list[dict[str, str]] = []
    for node in raw_nodes:
        if not include_from_me and bool(node.get("is_from_me", False)):
            continue
        text = _sanitize(node.get("text", ""))
        if not text:
            continue
        msg_id = _sanitize(node.get("id", ""))
        sender = _sanitize(node.get("sender", node.get("from", "")))
        ts = _sanitize(node.get("created_at", node.get("ts", "")))
        messages.append(
            {
                "id": msg_id,
                "from": sender,
                "text": text,
                "ts": ts,
                "thread_id": chat_id,
            }
        )

    print(json.dumps(messages, ensure_ascii=False))


if __name__ == "__main__":
    main()
