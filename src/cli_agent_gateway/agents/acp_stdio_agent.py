from __future__ import annotations

import time
from dataclasses import dataclass
from typing import Any

from cli_agent_gateway.agents.base import PermissionHandler, ProgressCallback
from cli_agent_gateway.core.contracts import TaskRequest, TaskResult
from cli_agent_gateway.infra.jsonrpc_stdio import JsonRpcNotification, JsonRpcStdioClient


@dataclass
class ACPMethodConfig:
    initialize: str = "initialize"
    session_new: str = "session/new"
    session_prompt: str = "session/prompt"


class ACPStdioAgentAdapter:
    def __init__(
        self,
        command: str,
        cwd: str,
        timeout_sec: int,
        permission_policy: str = "auto_allow",
        methods: ACPMethodConfig | None = None,
    ):
        self.command = command
        self.cwd = cwd
        self.timeout_sec = timeout_sec
        self.permission_policy = permission_policy
        self.methods = methods or ACPMethodConfig()
        self.client = JsonRpcStdioClient(command=self.command, cwd=self.cwd)
        self._initialized = False

    def _ensure_ready(self) -> None:
        self.client.start()
        if self._initialized:
            return
        self.client.send_request(
            self.methods.initialize,
            {
                "client": {
                    "name": "cli-agent-gateway",
                    "version": "0.2.0",
                }
            },
            timeout_sec=30,
        )
        self._initialized = True

    def _permission_response(self, method: str, params: dict[str, Any]) -> dict[str, Any]:
        if self.permission_policy == "auto_deny":
            return {"decision": "deny", "reason": "policy:auto_deny"}
        return {"decision": "allow", "reason": "policy:auto_allow"}

    def _extract_text(self, payload: dict[str, Any]) -> str:
        for key in ("summary", "message", "text", "output", "content"):
            value = payload.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
        return ""

    def _is_terminal(self, payload: dict[str, Any]) -> bool:
        status = str(payload.get("status", payload.get("state", ""))).lower().strip()
        if status in {"completed", "done", "success", "failed", "error", "cancelled"}:
            return True
        return bool(payload.get("is_terminal", False))

    def _status_from_payload(self, payload: dict[str, Any]) -> str:
        status = str(payload.get("status", payload.get("state", ""))).lower().strip()
        if status in {"completed", "done", "success"}:
            return "ok"
        if status in {"cancelled"}:
            return "cancelled"
        if status in {"failed", "error"}:
            return "error"
        return "ok"

    def execute(self, request: TaskRequest, on_progress: ProgressCallback | None = None) -> TaskResult:
        self._ensure_ready()
        start = time.time()
        deadline = start + self.timeout_sec

        session_id = request.session_id
        if not session_id:
            created = self.client.send_request(
                self.methods.session_new,
                {
                    "session": {
                        "idempotency_key": request.session_key,
                        "metadata": {
                            "channel": request.channel,
                            "sender": request.sender,
                            "thread_id": request.thread_id or "",
                        },
                    }
                },
                timeout_sec=30,
            )
            if isinstance(created, dict):
                session_id = str(created.get("session_id", created.get("id", ""))).strip() or None

        prompt_result = self.client.send_request(
            self.methods.session_prompt,
            {
                "session_id": session_id,
                "prompt": request.user_text,
                "metadata": request.metadata,
            },
            timeout_sec=min(60, self.timeout_sec),
        )

        raw_events: list[dict[str, Any]] = []
        aggregated_output: list[str] = []
        if isinstance(prompt_result, dict):
            text = self._extract_text(prompt_result)
            if text:
                aggregated_output.append(text)
            if self._is_terminal(prompt_result):
                elapsed = int(time.time() - start)
                summary = text or "任务已处理完成。"
                return TaskResult(
                    trace_id=request.trace_id,
                    status=self._status_from_payload(prompt_result),
                    summary=summary,
                    elapsed_sec=elapsed,
                    session_id=session_id,
                    output_text="\n".join(aggregated_output).strip(),
                    raw_events=raw_events,
                )

        final_status = "timeout"
        final_summary = "任务超时，未收到终态事件。"

        while time.time() < deadline:
            server_req = self.client.pop_request(timeout_sec=0.1)
            if server_req is not None:
                try:
                    if "request_permission" in server_req.method.lower():
                        result = self._permission_response(server_req.method, server_req.params)
                        self.client.send_response(server_req.id, result=result)
                    else:
                        self.client.send_response(
                            server_req.id,
                            error={"code": -32601, "message": f"unsupported method {server_req.method}"},
                        )
                except Exception as exc:
                    self.client.send_response(server_req.id, error={"code": -32000, "message": str(exc)})
                continue

            event = self.client.pop_notification(timeout_sec=0.1)
            if event is None:
                continue

            raw_events.append({"method": event.method, "params": event.params})
            text = self._extract_text(event.params)
            if text:
                aggregated_output.append(text)
                if on_progress and not self._is_terminal(event.params):
                    on_progress(text)

            if self._is_terminal(event.params):
                final_status = self._status_from_payload(event.params)
                final_summary = text or "任务已处理完成。"
                break

        elapsed = int(time.time() - start)
        if final_status == "timeout":
            stderr_lines = self.client.drain_stderr()
            if stderr_lines:
                final_summary = f"任务超时，agent stderr: {stderr_lines[-1]}"

        return TaskResult(
            trace_id=request.trace_id,
            status=final_status,
            summary=final_summary,
            elapsed_sec=elapsed,
            session_id=session_id,
            output_text="\n".join(aggregated_output).strip(),
            raw_events=raw_events,
        )

    def close(self) -> None:
        self.client.close()
