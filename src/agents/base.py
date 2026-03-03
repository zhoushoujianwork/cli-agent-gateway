from __future__ import annotations

from typing import Callable, Protocol

from core.contracts import TaskRequest, TaskResult

ProgressCallback = Callable[[str], None]
PermissionHandler = Callable[[str, dict], dict]


class AgentAdapter(Protocol):
    def execute(self, request: TaskRequest, on_progress: ProgressCallback | None = None) -> TaskResult:
        ...

    def close(self) -> None:
        ...
