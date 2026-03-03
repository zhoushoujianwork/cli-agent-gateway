from __future__ import annotations

import hashlib


def build_session_key(channel: str, sender: str, thread_id: str | None) -> str:
    raw = f"{channel}|{sender}|{thread_id or '-'}"
    digest = hashlib.sha256(raw.encode("utf-8")).hexdigest()[:24]
    return f"sess_{digest}"
