from __future__ import annotations

import time
from dataclasses import dataclass
from typing import Any

from agents.base import DebugCallback, PermissionHandler, ProgressCallback
from core.contracts import TaskRequest, TaskResult
from infra.jsonrpc_stdio import JsonRpcNotification, JsonRpcStdioClient


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
        initialize_timeout_sec: int = 30,
        session_new_timeout_sec: int = 30,
        session_new_retries: int = 0,
        session_new_retry_backoff_sec: float = 1.0,
        debug_trace_enabled: bool = False,
        debug_acp_event_details: bool = False,
        debug_acp_log_chunks: bool = False,
        debug_payload_chars: int = 280,
    ):
        self.command = command
        self.cwd = cwd
        self.timeout_sec = timeout_sec
        self.permission_policy = permission_policy
        self.methods = methods or ACPMethodConfig()
        self.client = JsonRpcStdioClient(command=self.command, cwd=self.cwd)
        self._initialized = False
        self.idle_finish_sec = 12
        self.prompt_recover_retries = 1
        self.initialize_timeout_sec = max(1, int(initialize_timeout_sec))
        self.session_new_timeout_sec = max(1, int(session_new_timeout_sec))
        self.session_new_retries = max(0, int(session_new_retries))
        self.session_new_retry_backoff_sec = max(0.0, float(session_new_retry_backoff_sec))
        self.debug_trace_enabled = bool(debug_trace_enabled)
        self.debug_acp_event_details = bool(debug_acp_event_details)
        self.debug_acp_log_chunks = bool(debug_acp_log_chunks)
        self.debug_payload_chars = max(80, int(debug_payload_chars))

    def _normalize_text(self, text: str) -> str:
        return " ".join(text.replace("\r", " ").replace("\n", " ").split()).strip()

    def _debug(self, on_debug: DebugCallback | None, event: str, **data: Any) -> None:
        if on_debug is None:
            return
        if not self.debug_trace_enabled and event in {
            "initialize_start",
            "initialize_ok",
            "session_new_start",
            "session_new_ok",
            "session_new_timeout",
            "execute_start",
            "prompt_start",
            "prompt_error",
            "prompt_retry_with_min_metadata",
            "prompt_restart",
            "server_request",
            "client_restart_start",
            "client_restart_ok",
            "execute_done",
        }:
            return
        safe: dict[str, Any] = {}
        for k, v in data.items():
            if isinstance(v, str):
                safe[k] = v if len(v) <= self.debug_payload_chars else v[: self.debug_payload_chars - 3] + "..."
            else:
                safe[k] = v
        on_debug(event, safe)

    def _create_session(self, request: TaskRequest, on_debug: DebugCallback | None = None) -> str | None:
        payload = {
            "cwd": self.cwd,
            "mcpServers": [],
            "session": {
                "idempotency_key": request.session_key,
                "metadata": {
                    "channel": request.channel,
                    "sender": request.sender,
                    "thread_id": request.thread_id or "",
                },
            },
        }
        last_exc: Exception | None = None
        for attempt in range(self.session_new_retries + 1):
            try:
                self._debug(
                    on_debug,
                    "session_new_start",
                    attempt=attempt + 1,
                    max_attempts=self.session_new_retries + 1,
                    timeout_sec=self.session_new_timeout_sec,
                    session_key=request.session_key,
                )
                created = self.client.send_request(
                    self.methods.session_new,
                    payload,
                    timeout_sec=self.session_new_timeout_sec,
                )
                if isinstance(created, dict):
                    session_id = str(created.get("sessionId", created.get("session_id", created.get("id", "")))).strip() or None
                    self._debug(on_debug, "session_new_ok", session_id=session_id or "", attempt=attempt + 1)
                    return session_id
                self._debug(on_debug, "session_new_ok", session_id="", attempt=attempt + 1)
                return None
            except TimeoutError as exc:
                last_exc = exc
                self._debug(on_debug, "session_new_timeout", attempt=attempt + 1, error=str(exc))
                if attempt >= self.session_new_retries:
                    break
                if self.session_new_retry_backoff_sec > 0:
                    time.sleep(self.session_new_retry_backoff_sec * (2**attempt))
        if last_exc is not None:
            raise last_exc
        return None

    def _ensure_ready(self, on_debug: DebugCallback | None = None) -> None:
        self.client.start()
        if self._initialized:
            return
        self._debug(on_debug, "initialize_start", timeout_sec=self.initialize_timeout_sec)
        self.client.send_request(
            self.methods.initialize,
            {
                "protocolVersion": "0.2",
                "clientCapabilities": {},
                "clientInfo": {
                    "name": "cli-agent-gateway",
                    "version": "0.2.0",
                },
            },
            timeout_sec=self.initialize_timeout_sec,
        )
        self._initialized = True
        self._debug(on_debug, "initialize_ok")

    def _restart_client(self, on_debug: DebugCallback | None = None) -> None:
        self._debug(on_debug, "client_restart_start")
        self.client.close()
        self.client = JsonRpcStdioClient(command=self.command, cwd=self.cwd)
        self._initialized = False
        self._ensure_ready(on_debug=on_debug)
        self._debug(on_debug, "client_restart_ok")

    def _permission_response(self, method: str, params: dict[str, Any]) -> dict[str, Any]:
        if self.permission_policy == "auto_deny":
            return {"decision": "deny", "reason": "policy:auto_deny"}
        return {"decision": "allow", "reason": "policy:auto_allow"}

    def _extract_text(self, payload: dict[str, Any]) -> str:
        update = payload.get("update")
        if isinstance(update, dict):
            content = update.get("content")
            if isinstance(content, dict):
                text = content.get("text")
                if isinstance(text, str) and text.strip():
                    return text
            for key in ("summary", "message", "text", "output"):
                value = update.get(key)
                if isinstance(value, str) and value.strip():
                    return value.strip()
        for key in ("summary", "message", "text", "output", "content"):
            value = payload.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
        return ""

    def _is_terminal(self, payload: dict[str, Any]) -> bool:
        update = payload.get("update")
        if isinstance(update, dict):
            session_update = str(update.get("sessionUpdate", "")).lower().strip()
            if session_update in {"turn_complete", "agent_turn_complete", "task_complete", "completed"}:
                return True
        status = str(payload.get("status", payload.get("state", ""))).lower().strip()
        if status in {"completed", "done", "success", "failed", "error", "cancelled"}:
            return True
        return bool(payload.get("is_terminal", False))

    def _status_from_payload(self, payload: dict[str, Any]) -> str:
        update = payload.get("update")
        if isinstance(update, dict):
            session_update = str(update.get("sessionUpdate", "")).lower().strip()
            if session_update in {"turn_complete", "agent_turn_complete", "task_complete", "completed"}:
                return "ok"
        status = str(payload.get("status", payload.get("state", ""))).lower().strip()
        if status in {"completed", "done", "success"}:
            return "ok"
        if status in {"cancelled"}:
            return "cancelled"
        if status in {"failed", "error"}:
            return "error"
        return "ok"

    def execute(
        self,
        request: TaskRequest,
        on_progress: ProgressCallback | None = None,
        on_debug: DebugCallback | None = None,
    ) -> TaskResult:
        self._ensure_ready(on_debug=on_debug)
        start = time.time()
        deadline = start + self.timeout_sec

        session_id = request.session_id
        if not session_id:
            session_id = self._create_session(request, on_debug=on_debug)
        self._debug(
            on_debug,
            "execute_start",
            trace_id=request.trace_id,
            session_key=request.session_key,
            session_id=session_id or "",
            timeout_sec=self.timeout_sec,
        )

        raw_events: list[dict[str, Any]] = []
        highlights: list[str] = []
        message_chunks: list[str] = []
        progress_buffer = ""
        last_progress_text = ""
        prompt_params = {
            "sessionId": session_id,
            "prompt": [
                {
                    "type": "text",
                    "text": request.user_text,
                }
            ],
            "metadata": request.metadata,
        }
        prompt_request_id = self.client.start_request(self.methods.session_prompt, prompt_params)
        self._debug(on_debug, "prompt_start", session_id=session_id or "", request_id=prompt_request_id)
        prompt_recover_count = 0
        prompt_metadata_fallback_used = False

        final_status = "timeout"
        final_summary = "任务超时，未收到终态事件。"
        last_event_at = time.time()
        saw_tool_completed = False
        saw_agent_message = False

        while time.time() < deadline:
            prompt_response = self.client.poll_response(prompt_request_id, timeout_sec=0.05)
            if prompt_response is not None:
                last_event_at = time.time()
                if prompt_response.error is not None:
                    err_text = str(prompt_response.error)
                    recoverable = (
                        "resource not found" in err_text.lower()
                        or "failed to deserialize response" in err_text.lower()
                    )
                    self._debug(
                        on_debug,
                        "prompt_error",
                        request_id=prompt_request_id,
                        recoverable=recoverable,
                        error=err_text,
                    )
                    if recoverable and prompt_recover_count < self.prompt_recover_retries:
                        prompt_recover_count += 1
                        self._restart_client(on_debug=on_debug)
                        if not prompt_metadata_fallback_used:
                            prompt_params["metadata"] = {
                                "received_ts": request.metadata.get("received_ts", ""),
                                "user_profile": request.metadata.get("user_profile", {}),
                            }
                            prompt_metadata_fallback_used = True
                        self._debug(on_debug, "prompt_retry_with_min_metadata", retry=prompt_recover_count)
                        session_id = self._create_session(request, on_debug=on_debug)
                        prompt_params["sessionId"] = session_id
                        prompt_request_id = self.client.start_request(self.methods.session_prompt, prompt_params)
                        self._debug(on_debug, "prompt_restart", request_id=prompt_request_id, session_id=session_id or "")
                        continue
                    stderr_lines = self.client.drain_stderr()
                    stderr_tail = stderr_lines[-1] if stderr_lines else ""
                    if stderr_tail:
                        raise RuntimeError(
                            f"jsonrpc error method={self.methods.session_prompt} error={prompt_response.error} "
                            f"agent_stderr={stderr_tail}"
                        )
                    raise RuntimeError(f"jsonrpc error method={self.methods.session_prompt} error={prompt_response.error}")
                if isinstance(prompt_response.result, dict):
                    text = self._extract_text(prompt_response.result)
                    if text:
                        clean = self._normalize_text(text)
                        if clean and clean not in highlights:
                            highlights.append(clean)
                    if self._is_terminal(prompt_response.result):
                        final_status = self._status_from_payload(prompt_response.result)
                        final_summary = text or "任务已处理完成。"
                        break

            server_req = self.client.pop_request(timeout_sec=0.1)
            if server_req is not None:
                last_event_at = time.time()
                try:
                    self._debug(on_debug, "server_request", method=server_req.method, request_id=server_req.id)
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
                if (
                    (time.time() - last_event_at) >= self.idle_finish_sec
                    and (message_chunks or highlights)
                    and (saw_tool_completed or saw_agent_message)
                ):
                    final_status = "ok"
                    normalized = self._normalize_text("".join(message_chunks))
                    if normalized:
                        final_summary = normalized[-300:]
                    elif highlights:
                        final_summary = highlights[-1][-300:]
                    else:
                        final_summary = "任务已处理完成。"
                    break
                continue
            last_event_at = time.time()

            raw_events.append({"method": event.method, "params": event.params})
            update = event.params.get("update", {}) if isinstance(event.params, dict) else {}
            if isinstance(update, dict):
                su = str(update.get("sessionUpdate", "")).lower().strip()
                should_emit_session_update = (
                    su in {"tool_call", "tool_call_update"} or (
                        self.debug_acp_event_details and (self.debug_acp_log_chunks or su != "agent_message_chunk")
                    )
                )
                if should_emit_session_update:
                    self._debug(
                        on_debug,
                        "session_update",
                        method=event.method,
                        session_update=su,
                        status=str(update.get("status", "")),
                        tool_call_id=str(update.get("toolCallId", "")),
                        title=str(update.get("title", "")),
                    )
                if su == "tool_call_update" and str(update.get("status", "")).lower().strip() == "completed":
                    saw_tool_completed = True
                if su == "agent_message_chunk":
                    saw_agent_message = True
                    content = update.get("content")
                    if isinstance(content, dict):
                        chunk_text = content.get("text")
                        if isinstance(chunk_text, str) and chunk_text:
                            message_chunks.append(chunk_text)
                            progress_buffer += chunk_text
                            normalized_progress = self._normalize_text(progress_buffer)
                            if (
                                on_progress
                                and normalized_progress
                                and normalized_progress != last_progress_text
                                and (len(normalized_progress) >= 24 or progress_buffer.endswith(("。", "！", "？", ".", "!", "?")))
                            ):
                                on_progress(normalized_progress[-120:])
                                last_progress_text = normalized_progress
                                progress_buffer = ""
            text = self._extract_text(event.params)
            if text:
                clean = self._normalize_text(text)
                if clean and clean not in highlights:
                    highlights.append(clean)

            if self._is_terminal(event.params):
                final_status = self._status_from_payload(event.params)
                final_summary = text or "任务已处理完成。"
                break

        elapsed = int(time.time() - start)
        if final_status == "timeout":
            stderr_lines = self.client.drain_stderr()
            if stderr_lines:
                final_summary = f"任务超时，agent stderr: {stderr_lines[-1]}"
        self._debug(
            on_debug,
            "execute_done",
            trace_id=request.trace_id,
            status=final_status,
            elapsed_sec=elapsed,
            session_id=session_id or "",
            raw_events=len(raw_events),
        )

        return TaskResult(
            trace_id=request.trace_id,
            status=final_status,
            summary=final_summary,
            elapsed_sec=elapsed,
            session_id=session_id,
            output_text=self._normalize_text("".join(message_chunks)) or "\n".join(highlights[-6:]).strip(),
            raw_events=raw_events,
        )

    def close(self) -> None:
        self.client.close()
