#!/usr/bin/env python3
from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import sys
import time
import urllib.parse
import urllib.request
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


def _csv_set(value: str) -> set[str]:
    return {x.strip() for x in _sanitize(value).split(",") if x.strip()}


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


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


def _normalize_fetch_message(node: dict[str, Any]) -> dict[str, str] | None:
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


def fetch_main() -> None:
    repo_root = _repo_root()
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
        normalized = _normalize_fetch_message(node)
        if normalized is not None:
            messages.append(normalized)

    print(json.dumps(messages, ensure_ascii=False))


def _request_json(url: str, payload: dict[str, Any] | None = None, timeout_sec: int = 10) -> dict[str, Any]:
    data = None
    headers = {"Content-Type": "application/json"}
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(url=url, data=data, headers=headers, method="POST" if payload is not None else "GET")
    with urllib.request.urlopen(req, timeout=timeout_sec) as resp:
        raw = resp.read().decode("utf-8", errors="replace")
    try:
        return json.loads(raw) if raw else {}
    except Exception:
        return {}


def _signed_webhook(base_url: str, secret: str) -> str:
    ts = str(int(time.time() * 1000))
    string_to_sign = f"{ts}\n{secret}"
    digest = hmac.new(secret.encode("utf-8"), string_to_sign.encode("utf-8"), hashlib.sha256).digest()
    sign = urllib.parse.quote_plus(base64.b64encode(digest))
    sep = "&" if "?" in base_url else "?"
    return f"{base_url}{sep}timestamp={ts}&sign={sign}"


def _send_webhook(text: str) -> None:
    webhook = os.getenv("DINGTALK_BOT_WEBHOOK", "").strip()
    if not webhook:
        raise RuntimeError("DINGTALK_BOT_WEBHOOK is required for webhook mode")
    secret = os.getenv("DINGTALK_BOT_SECRET", "").strip()
    timeout_sec = int(os.getenv("DINGTALK_SEND_TIMEOUT_SEC", "10"))

    url = _signed_webhook(webhook, secret) if secret else webhook
    payload = {"msgtype": "text", "text": {"content": text}}
    resp = _request_json(url, payload=payload, timeout_sec=timeout_sec)
    if int(resp.get("errcode", 0)) != 0:
        raise RuntimeError(f"webhook send failed: errcode={resp.get('errcode')} errmsg={resp.get('errmsg')}")


def _get_access_token(timeout_sec: int) -> str:
    app_key = os.getenv("DINGTALK_APP_KEY", "").strip()
    app_secret = os.getenv("DINGTALK_APP_SECRET", "").strip()
    if not app_key or not app_secret:
        raise RuntimeError("DINGTALK_APP_KEY and DINGTALK_APP_SECRET are required for api mode")

    token_url = os.getenv("DINGTALK_TOKEN_URL", "").strip()
    if token_url:
        node = _request_json(token_url, payload={"appKey": app_key, "appSecret": app_secret}, timeout_sec=timeout_sec)
        token = str(node.get("accessToken", node.get("access_token", ""))).strip()
        if token:
            return token

    legacy_url = "https://oapi.dingtalk.com/gettoken?" + urllib.parse.urlencode({"appkey": app_key, "appsecret": app_secret})
    node = _request_json(legacy_url, payload=None, timeout_sec=timeout_sec)
    if int(node.get("errcode", 0)) != 0:
        raise RuntimeError(f"gettoken failed: errcode={node.get('errcode')} errmsg={node.get('errmsg')}")
    token = str(node.get("access_token", "")).strip()
    if not token:
        raise RuntimeError("gettoken returned empty access_token")
    return token


def _send_api(text: str) -> None:
    timeout_sec = int(os.getenv("DINGTALK_SEND_TIMEOUT_SEC", "10"))
    agent_id = os.getenv("DINGTALK_AGENT_ID", "").strip()
    user_id = os.getenv("SMS_TO", "").strip() or os.getenv("DINGTALK_DEFAULT_TO_USER", "").strip()
    if not agent_id:
        raise RuntimeError("DINGTALK_AGENT_ID is required for api mode")
    if not user_id:
        raise RuntimeError("SMS_TO (or DINGTALK_DEFAULT_TO_USER) is required for api mode")

    token = _get_access_token(timeout_sec)
    send_base = os.getenv("DINGTALK_SEND_URL", "https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2").strip()
    send_url = f"{send_base}?access_token={urllib.parse.quote_plus(token)}"

    payload = {
        "agent_id": int(agent_id),
        "userid_list": user_id,
        "msg": {"msgtype": "text", "text": {"content": text}},
    }
    node = _request_json(send_url, payload=payload, timeout_sec=timeout_sec)
    if int(node.get("errcode", 0)) != 0:
        raise RuntimeError(f"api send failed: errcode={node.get('errcode')} errmsg={node.get('errmsg')}")


def send_main() -> None:
    if len(sys.argv) < 2:
        print("message text is required as $1", file=sys.stderr)
        raise SystemExit(2)

    repo_root = _repo_root()
    load_dotenv(repo_root / ".env")

    text = sys.argv[1].strip()
    if not text:
        print("message text is empty", file=sys.stderr)
        raise SystemExit(2)

    mode = os.getenv("DINGTALK_SEND_MODE", "api").strip().lower()
    try:
        if mode == "webhook":
            _send_webhook(text)
        else:
            _send_api(text)
    except Exception as exc:
        print(f"dingtalk send failed: {exc}", file=sys.stderr)
        raise SystemExit(1)


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


def _normalize_callback_message(node: dict[str, Any]) -> dict[str, Any] | None:
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

        challenge = node.get("challenge") if isinstance(node, dict) else None
        if isinstance(challenge, str) and challenge:
            self._json_response(200, {"challenge": challenge})
            return

        msg = _normalize_callback_message(node if isinstance(node, dict) else {})
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
        return


def callback_server_main() -> None:
    repo_root = _repo_root()
    load_dotenv(repo_root / ".env")

    host = os.getenv("DINGTALK_CALLBACK_HOST", "0.0.0.0").strip() or "0.0.0.0"
    port = int(os.getenv("DINGTALK_CALLBACK_PORT", "18580"))
    queue_path = _resolve_queue_path(os.getenv("DINGTALK_QUEUE_FILE", ".dingtalk_inbox.jsonl"), repo_root)

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


def main() -> None:
    if len(sys.argv) < 2 or sys.argv[1] not in {"fetch", "send", "callback-server"}:
        print("Usage: python3 src/channels/dingtalk.py <fetch|send|callback-server> [message_text]", file=sys.stderr)
        raise SystemExit(2)

    command = sys.argv[1]
    sys.argv = [sys.argv[0]] + sys.argv[2:]

    if command == "fetch":
        fetch_main()
        return
    if command == "send":
        send_main()
        return
    callback_server_main()


if __name__ == "__main__":
    main()
