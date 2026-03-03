#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import json
import os
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


def _csv_set(value: str) -> set[str]:
    return {x.strip() for x in _sanitize(value).split(",") if x.strip()}


def _resolve_queue_path(path_value: str, repo_root: Path) -> Path:
    raw = _sanitize(path_value)
    if not raw:
        return repo_root / ".dingtalk_inbox.jsonl"
    path = Path(raw).expanduser()
    if path.is_absolute():
        return path
    return (repo_root / path).resolve()


def _should_keep(node: dict[str, Any]) -> bool:
    dm_policy = _sanitize(os.getenv("DINGTALK_DM_POLICY", "allowlist")).lower()
    group_policy = _sanitize(os.getenv("DINGTALK_GROUP_POLICY", "allowlist")).lower()
    allowed_from = _csv_set(os.getenv("DINGTALK_ALLOWED_FROM", ""))
    group_allowlist = _csv_set(os.getenv("DINGTALK_GROUP_ALLOWLIST", ""))
    require_mention = os.getenv("DINGTALK_REQUIRE_MENTION_IN_GROUP", "1").strip() != "0"

    chat_type = _sanitize(node.get("chatType", node.get("conversationType", ""))).lower()
    conversation_id = _sanitize(node.get("conversationId", node.get("cid", "")))
    sender = _sanitize(node.get("senderStaffId", node.get("senderId", node.get("from", ""))))
    is_group = chat_type in {"group", "groupchat", "2"} or bool(node.get("isGroup", False))

    if is_group:
        if group_policy == "disabled":
            return False
        if group_policy == "allowlist" and conversation_id and group_allowlist and conversation_id not in group_allowlist:
            return False
        if require_mention:
            is_at_bot = bool(node.get("isAtBot", node.get("atBot", False)))
            if not is_at_bot:
                return False
    else:
        if dm_policy == "disabled":
            return False
        if dm_policy == "allowlist" and allowed_from and sender not in allowed_from:
            return False

    return True


def _normalize(node: dict[str, Any]) -> dict[str, str] | None:
    text = _sanitize(node.get("text", node.get("content", node.get("msg", ""))))
    if not text:
        return None

    sender = _sanitize(node.get("senderStaffId", node.get("senderId", node.get("from", ""))))
    ts = _sanitize(node.get("ts", node.get("createAt", node.get("timestamp", ""))))
    conversation_id = _sanitize(node.get("conversationId", node.get("cid", "")))
    thread_id = _sanitize(node.get("threadId", ""))
    thread_key = f"{conversation_id}:{thread_id}" if thread_id else conversation_id

    msg_id = _sanitize(node.get("messageId", node.get("id", "")))
    if not msg_id:
        sig = f"{sender}|{text}|{ts}|{thread_key}"
        msg_id = hashlib.sha256(sig.encode("utf-8")).hexdigest()[:24]

    return {
        "id": msg_id,
        "from": sender,
        "text": text,
        "ts": ts,
        "thread_id": thread_key,
    }


def main() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    load_dotenv(repo_root / ".env")

    queue_path = _resolve_queue_path(os.getenv("DINGTALK_QUEUE_FILE", ".dingtalk_inbox.jsonl"), repo_root)
    max_events = int(os.getenv("DINGTALK_FETCH_MAX_EVENTS", "30"))

    if not queue_path.exists():
        print("[]")
        return

    lines = queue_path.read_text(encoding="utf-8").splitlines()
    if max_events > 0:
        lines = lines[-max_events:]

    messages: list[dict[str, str]] = []
    for line in lines:
        raw = line.strip()
        if not raw:
            continue
        try:
            node = json.loads(raw)
        except Exception:
            continue
        if not isinstance(node, dict):
            continue
        if not _should_keep(node):
            continue
        normalized = _normalize(node)
        if normalized is not None:
            messages.append(normalized)

    print(json.dumps(messages, ensure_ascii=False))


if __name__ == "__main__":
    main()
