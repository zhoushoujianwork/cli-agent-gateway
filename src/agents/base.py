from __future__ import annotations

from typing import Any, Callable, Protocol

from core.contracts import TaskRequest, TaskResult

ProgressCallback = Callable[[str], None]
PermissionHandler = Callable[[str, dict], dict]
DebugCallback = Callable[[str, dict[str, Any]], None]


class AgentAdapter(Protocol):
    def execute(
        self,
        request: TaskRequest,
        on_progress: ProgressCallback | None = None,
        on_debug: DebugCallback | None = None,
    ) -> TaskResult:
        ...

    def close(self) -> None:
        ...
