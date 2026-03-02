from __future__ import annotations

import errno
import fcntl
import json
import os
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def _utc_now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def _read_json(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {}
    try:
        raw = path.read_text(encoding="utf-8").strip()
        if not raw:
            return {}
        node = json.loads(raw)
        if isinstance(node, dict):
            return node
        return {}
    except Exception:
        return {}


def _pid_running(pid: int) -> bool:
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError as exc:
        return exc.errno != errno.ESRCH


@dataclass
class LockStatus:
    locked: bool
    owner_pid: int | None
    owner_started_at: str | None
    metadata: dict[str, Any]


class ProcessLock:
    def __init__(self, path: Path):
        self.path = path
        self._fd: int | None = None

    def acquire(self) -> bool:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(str(self.path), os.O_RDWR | os.O_CREAT, 0o600)
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            os.close(fd)
            return False
        self._fd = fd
        return True

    def write_metadata(self, metadata: dict[str, Any]) -> None:
        if self._fd is None:
            raise RuntimeError("lock is not acquired")
        payload = dict(metadata)
        payload.setdefault("pid", os.getpid())
        payload.setdefault("started_at", _utc_now_iso())
        data = (json.dumps(payload, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
        os.ftruncate(self._fd, 0)
        os.lseek(self._fd, 0, os.SEEK_SET)
        os.write(self._fd, data)
        os.fsync(self._fd)

    def close(self) -> None:
        if self._fd is None:
            return
        try:
            fcntl.flock(self._fd, fcntl.LOCK_UN)
        finally:
            os.close(self._fd)
            self._fd = None

    def __enter__(self) -> "ProcessLock":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()


def inspect_lock(path: Path) -> LockStatus:
    metadata = _read_json(path)
    owner_pid: int | None = None
    owner_started_at: str | None = None
    if isinstance(metadata.get("pid"), int):
        owner_pid = int(metadata["pid"])
    if isinstance(metadata.get("started_at"), str):
        owner_started_at = metadata["started_at"]

    probe = ProcessLock(path)
    acquired = probe.acquire()
    if acquired:
        probe.close()
        if owner_pid is not None and not _pid_running(owner_pid):
            owner_pid = None
            owner_started_at = None
        return LockStatus(locked=False, owner_pid=owner_pid, owner_started_at=owner_started_at, metadata=metadata)

    return LockStatus(locked=True, owner_pid=owner_pid, owner_started_at=owner_started_at, metadata=metadata)
