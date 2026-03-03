#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import hmac
import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
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


def utc_now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def _sanitize(value: Any) -> str:
    if value is None:
        return ""
    return str(value).replace("\x00", "").strip()


def _resolve_queue_path(repo_root: Path) -> Path:
    raw = _sanitize(os.getenv("DINGTALK_QUEUE_FILE", ".dingtalk_inbox.jsonl"))
    if not raw:
        return repo_root / ".dingtalk_inbox.jsonl"
    path = Path(raw).expanduser()
    if path.is_absolute():
        return path
    return (repo_root / path).resolve()


def _verify_signature(handler: BaseHTTPRequestHandler, body: bytes) -> bool:
    secret = os.getenv("DINGTALK_CALLBACK_SECRET", "").strip()
    if not secret:
        return True

    timestamp = handler.headers.get("X-Timestamp", "")
    signature = handler.headers.get("X-Signature", "")
    if not timestamp or not signature:
        return False

    text = f"{timestamp}\n".encode("utf-8") + body
    digest = hmac.new(secret.encode("utf-8"), text, hashlib.sha256).hexdigest()
    return hmac.compare_digest(digest, signature)


def _normalize_message(node: dict[str, Any]) -> dict[str, Any] | None:
    msg = node.get("message") if isinstance(node.get("message"), dict) else node
    if not isinstance(msg, dict):
        return None

    text = _sanitize(
        msg.get("text")
        or (msg.get("text", {}) if isinstance(msg.get("text"), dict) else "")
        or msg.get("content")
        or msg.get("msg")
    )
    if not text and isinstance(msg.get("text"), dict):
        text = _sanitize(msg.get("text", {}).get("content", ""))
    if not text:
        return None

    return {
        "messageId": _sanitize(msg.get("messageId", msg.get("msgId", msg.get("id", "")))),
        "conversationId": _sanitize(msg.get("conversationId", msg.get("cid", ""))),
        "threadId": _sanitize(msg.get("threadId", "")),
        "senderStaffId": _sanitize(msg.get("senderStaffId", msg.get("senderId", msg.get("from", "")))),
        "chatType": _sanitize(msg.get("chatType", msg.get("conversationType", ""))),
        "isAtBot": bool(msg.get("isAtBot", msg.get("atBot", False))),
        "text": text,
        "ts": _sanitize(msg.get("ts", msg.get("createAt", msg.get("timestamp", utc_now())))),
        "raw": msg,
    }


class DingTalkCallbackHandler(BaseHTTPRequestHandler):
    server_version = "DingTalkCallback/0.1"

    def _json_response(self, status: int, payload: dict[str, Any]) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self) -> None:  # noqa: N802
        content_len = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(content_len) if content_len > 0 else b""

        if not _verify_signature(self, body):
            self._json_response(401, {"ok": False, "error": "invalid_signature"})
            return

        try:
            node = json.loads(body.decode("utf-8")) if body else {}
        except Exception:
            self._json_response(400, {"ok": False, "error": "invalid_json"})
            return

        # URL validation compatibility
        challenge = node.get("challenge") if isinstance(node, dict) else None
        if isinstance(challenge, str) and challenge:
            self._json_response(200, {"challenge": challenge})
            return

        msg = _normalize_message(node if isinstance(node, dict) else {})
        if msg is None:
            self._json_response(200, {"ok": True, "skipped": True})
            return

        queue_path: Path = self.server.queue_path  # type: ignore[attr-defined]
        queue_path.parent.mkdir(parents=True, exist_ok=True)
        with queue_path.open("a", encoding="utf-8") as f:
            f.write(json.dumps(msg, ensure_ascii=False) + "\n")

        print(f"[{utc_now()}] dingtalk inbound queued id={msg.get('messageId')} from={msg.get('senderStaffId')}")
        self._json_response(200, {"ok": True})

    def log_message(self, format: str, *args: Any) -> None:  # noqa: A003
        # quiet stdlib logs; gateway has explicit logs.
        return


def main() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    load_dotenv(repo_root / ".env")

    host = os.getenv("DINGTALK_CALLBACK_HOST", "0.0.0.0").strip() or "0.0.0.0"
    port = int(os.getenv("DINGTALK_CALLBACK_PORT", "18580"))
    queue_path = _resolve_queue_path(repo_root)

    httpd = ThreadingHTTPServer((host, port), DingTalkCallbackHandler)
    httpd.queue_path = queue_path  # type: ignore[attr-defined]
    print(f"[{utc_now()}] dingtalk callback server started host={host} port={port} queue={queue_path}")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        httpd.server_close()
        print(f"[{utc_now()}] dingtalk callback server stopped")


if __name__ == "__main__":
    main()
