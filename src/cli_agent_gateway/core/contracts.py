from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, List, Literal, Optional


TaskStatus = Literal["ok", "error", "timeout", "cancelled"]


def utc_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


@dataclass
class InboundMessage:
    id: str
    sender: str
    text: str
    ts: str
    channel: str = "command"
    thread_id: Optional[str] = None


@dataclass
class TaskRequest:
    trace_id: str
    session_key: str
    user_text: str
    sender: str
    channel: str
    thread_id: Optional[str] = None
    session_id: Optional[str] = None
    metadata: Dict[str, Any] = field(default_factory=dict)


@dataclass
class TaskResult:
    trace_id: str
    status: TaskStatus
    summary: str
    elapsed_sec: int
    session_id: Optional[str] = None
    output_text: str = ""
    error_text: str = ""
    raw_events: List[Dict[str, Any]] = field(default_factory=list)
