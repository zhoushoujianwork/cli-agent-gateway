from __future__ import annotations

import json
import queue
import shlex
import subprocess
import threading
import time
from dataclasses import dataclass
from typing import Any, Optional


@dataclass
class JsonRpcResponse:
    id: int
    result: Any = None
    error: Any = None


@dataclass
class JsonRpcRequest:
    id: int
    method: str
    params: dict[str, Any]


@dataclass
class JsonRpcNotification:
    method: str
    params: dict[str, Any]


class JsonRpcStdioClient:
    def __init__(self, command: str, cwd: str | None = None):
        self.command = command
        self.cwd = cwd
        self.proc: Optional[subprocess.Popen[str]] = None
        self._writer_lock = threading.Lock()
        self._next_id = 1
        self._pending: dict[int, queue.Queue[JsonRpcResponse]] = {}
        self._notifications: queue.Queue[JsonRpcNotification] = queue.Queue()
        self._requests: queue.Queue[JsonRpcRequest] = queue.Queue()
        self._stderr_lines: queue.Queue[str] = queue.Queue()
        self._closed = False

    def start(self) -> None:
        if self.proc is not None:
            return
        self.proc = subprocess.Popen(
            shlex.split(self.command),
            cwd=self.cwd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )
        threading.Thread(target=self._stdout_loop, daemon=True).start()
        threading.Thread(target=self._stderr_loop, daemon=True).start()

    def close(self) -> None:
        self._closed = True
        if self.proc is None:
            return
        if self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        self.proc = None

    def _write(self, payload: dict[str, Any]) -> None:
        if self.proc is None or self.proc.stdin is None:
            raise RuntimeError("jsonrpc client not started")
        line = json.dumps(payload, ensure_ascii=False)
        with self._writer_lock:
            self.proc.stdin.write(line + "\n")
            self.proc.stdin.flush()

    def send_request(self, method: str, params: dict[str, Any] | None = None, timeout_sec: int = 30) -> Any:
        request_id = self._next_id
        self._next_id += 1
        response_queue: queue.Queue[JsonRpcResponse] = queue.Queue(maxsize=1)
        self._pending[request_id] = response_queue
        self._write(
            {
                "jsonrpc": "2.0",
                "id": request_id,
                "method": method,
                "params": params or {},
            }
        )
        try:
            response = response_queue.get(timeout=timeout_sec)
        except queue.Empty as exc:
            self._pending.pop(request_id, None)
            raise TimeoutError(f"request timeout method={method}") from exc
        if response.error is not None:
            raise RuntimeError(f"jsonrpc error method={method} error={response.error}")
        return response.result

    def send_response(self, request_id: int, *, result: Any = None, error: Any = None) -> None:
        payload: dict[str, Any] = {"jsonrpc": "2.0", "id": request_id}
        if error is not None:
            payload["error"] = error
        else:
            payload["result"] = result if result is not None else {}
        self._write(payload)

    def pop_notification(self, timeout_sec: float = 0.0) -> JsonRpcNotification | None:
        try:
            return self._notifications.get(timeout=timeout_sec)
        except queue.Empty:
            return None

    def pop_request(self, timeout_sec: float = 0.0) -> JsonRpcRequest | None:
        try:
            return self._requests.get(timeout=timeout_sec)
        except queue.Empty:
            return None

    def drain_stderr(self) -> list[str]:
        lines: list[str] = []
        while True:
            try:
                lines.append(self._stderr_lines.get_nowait())
            except queue.Empty:
                return lines

    def _stdout_loop(self) -> None:
        assert self.proc is not None
        assert self.proc.stdout is not None
        for raw in self.proc.stdout:
            line = raw.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except Exception:
                continue

            if "id" in msg and "method" not in msg:
                response = JsonRpcResponse(id=int(msg["id"]), result=msg.get("result"), error=msg.get("error"))
                pending = self._pending.pop(response.id, None)
                if pending is not None:
                    pending.put(response)
                continue

            if "method" in msg and "id" in msg:
                self._requests.put(
                    JsonRpcRequest(
                        id=int(msg["id"]),
                        method=str(msg["method"]),
                        params=msg.get("params", {}) or {},
                    )
                )
                continue

            if "method" in msg:
                self._notifications.put(
                    JsonRpcNotification(
                        method=str(msg["method"]),
                        params=msg.get("params", {}) or {},
                    )
                )

    def _stderr_loop(self) -> None:
        assert self.proc is not None
        assert self.proc.stderr is not None
        for raw in self.proc.stderr:
            text = raw.strip()
            if text:
                self._stderr_lines.put(text)
