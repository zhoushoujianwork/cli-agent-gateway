from __future__ import annotations

import hashlib
import json
import os
import shlex
import subprocess
import threading
import time
from collections import deque
from typing import Any

from core.contracts import InboundMessage


def _sanitize(value: Any) -> str:
    if value is None:
        return ""
    return str(value).replace("\x00", "").strip()


def _csv_set(value: str) -> set[str]:
    return {x.strip() for x in _sanitize(value).split(",") if x.strip()}


def _extract_text(node: dict[str, Any]) -> str:
    text = node.get("text")
    if isinstance(text, dict):
        return _sanitize(text.get("content", ""))
    return _sanitize(text or node.get("content") or node.get("msg") or "")


def _jsonable(value: Any) -> Any:
    if isinstance(value, (str, int, float, bool)) or value is None:
        return value
    if isinstance(value, dict):
        return {str(k): _jsonable(v) for k, v in value.items()}
    if isinstance(value, list):
        return [_jsonable(v) for v in value]
    if hasattr(value, "to_dict"):
        try:
            return _jsonable(value.to_dict())
        except Exception:
            pass
    if hasattr(value, "__dict__"):
        try:
            return _jsonable(vars(value))
        except Exception:
            pass
    return str(value)


def _drop_reason(node: dict[str, Any]) -> str | None:
    dm_policy = _sanitize(os.getenv("DINGTALK_DM_POLICY", "allowlist")).lower()
    group_policy = _sanitize(os.getenv("DINGTALK_GROUP_POLICY", "allowlist")).lower()
    allowed_from = _csv_set(os.getenv("DINGTALK_ALLOWED_FROM", ""))
    group_allowlist = _csv_set(os.getenv("DINGTALK_GROUP_ALLOWLIST", ""))
    require_mention = os.getenv("DINGTALK_REQUIRE_MENTION_IN_GROUP", "0").strip() != "0"

    chat_type = _sanitize(node.get("chatType", node.get("conversationType", ""))).lower()
    conversation_id = _sanitize(node.get("conversationId", node.get("cid", "")))
    sender = _sanitize(node.get("senderStaffId", node.get("senderId", node.get("from", ""))))
    is_group = chat_type in {"group", "groupchat", "2"} or bool(node.get("isGroup", False))

    if is_group:
        if group_policy == "disabled":
            return "group_policy_disabled"
        if group_policy == "allowlist" and conversation_id and group_allowlist and conversation_id not in group_allowlist:
            return "group_not_in_allowlist"
        if require_mention:
            if not bool(node.get("isAtBot", node.get("atBot", False))):
                return "group_requires_mention"
    else:
        if dm_policy == "disabled":
            return "dm_policy_disabled"
        if dm_policy == "allowlist" and allowed_from and sender not in allowed_from:
            return "sender_not_in_allowlist"
    return None


def _should_keep(node: dict[str, Any]) -> bool:
    return _drop_reason(node) is None


class DingTalkStreamChannelAdapter:
    channel_id = "dingtalk"

    def __init__(self, send_cmd: str, send_timeout_sec: int = 120):
        self.send_cmd = send_cmd
        self.send_timeout_sec = send_timeout_sec
        self._inbox: deque[InboundMessage] = deque()
        self._lock = threading.Lock()
        self._started = False
        self._start_error: str | None = None
        self._stream_thread: threading.Thread | None = None
        self._start_stream()

    def _start_stream(self) -> None:
        try:
            import dingtalk_stream  # type: ignore
        except Exception as exc:
            self._start_error = (
                f"dingtalk-stream SDK not installed: {exc}. "
                "Install with: pip install dingtalk-stream"
            )
            return

        app_key = _sanitize(os.getenv("DINGTALK_APP_KEY", ""))
        app_secret = _sanitize(os.getenv("DINGTALK_APP_SECRET", ""))
        if not app_key or not app_secret:
            self._start_error = "DINGTALK_APP_KEY and DINGTALK_APP_SECRET are required for stream channel"
            return

        credential = dingtalk_stream.Credential(app_key, app_secret)
        client = dingtalk_stream.DingTalkStreamClient(credential)

        def push_raw_message(raw: dict[str, Any]) -> None:
            debug = os.getenv("DINGTALK_STREAM_DEBUG", "1").strip() != "0"
            text = _extract_text(raw)
            sender_dbg = _sanitize(raw.get("senderStaffId", raw.get("senderId", raw.get("from", "")))) or "(empty)"
            ctype_dbg = _sanitize(raw.get("chatType", raw.get("conversationType", ""))) or "(empty)"
            if debug:
                raw_preview = json.dumps(raw, ensure_ascii=False)[:500]
                print(f"[dingtalk-stream] raw sender={sender_dbg} chat_type={ctype_dbg} payload={raw_preview}")
            if not text:
                if debug:
                    print("[dingtalk-stream] drop reason=empty_text")
                return
            normalized = {
                "messageId": _sanitize(raw.get("messageId", raw.get("msgId", raw.get("id", "")))),
                "conversationId": _sanitize(raw.get("conversationId", raw.get("cid", ""))),
                "threadId": _sanitize(raw.get("threadId", "")),
                "senderStaffId": _sanitize(raw.get("senderStaffId", raw.get("senderId", raw.get("from", "")))),
                "senderName": _sanitize(raw.get("senderName", raw.get("senderNick", raw.get("fromName", "")))),
                "senderUnionId": _sanitize(raw.get("senderUnionId", "")),
                "chatType": _sanitize(raw.get("chatType", raw.get("conversationType", ""))),
                "isAtBot": bool(raw.get("isAtBot", raw.get("atBot", False))),
                "isGroup": bool(raw.get("isGroup", False)),
                "text": text,
                "ts": _sanitize(raw.get("ts", raw.get("createAt", raw.get("timestamp", "")))),
            }
            drop_reason = _drop_reason(normalized)
            if drop_reason is not None:
                if debug:
                    sender_dbg = normalized["senderStaffId"] or "(empty)"
                    chat_dbg = normalized["chatType"] or "(empty)"
                    print(
                        f"[dingtalk-stream] drop reason={drop_reason} sender={sender_dbg} "
                        f"chat_type={chat_dbg} text={text[:60]}"
                    )
                return
            if debug:
                sender_dbg = normalized["senderStaffId"] or "(empty)"
                chat_dbg = normalized["chatType"] or "(empty)"
                print(f"[dingtalk-stream] recv sender={sender_dbg} chat_type={chat_dbg} text={text[:60]}")
            sender = normalized["senderStaffId"]
            conversation_id = normalized["conversationId"]
            thread_id = normalized["threadId"]
            thread_key = f"{conversation_id}:{thread_id}" if thread_id else conversation_id
            msg_id = normalized["messageId"]
            if not msg_id:
                sig = f"{sender}|{text}|{normalized['ts']}|{thread_key}"
                msg_id = hashlib.sha256(sig.encode("utf-8")).hexdigest()[:24]
            inbound = InboundMessage(
                id=msg_id,
                sender=sender,
                text=text,
                ts=normalized["ts"] or time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                channel=self.channel_id,
                thread_id=thread_key or None,
                metadata={
                    "user_profile": {
                        "sender_id": sender,
                        "sender_name": normalized["senderName"],
                        "sender_union_id": normalized["senderUnionId"],
                    },
                    "dingtalk": {
                        "conversation_id": normalized["conversationId"],
                        "chat_type": normalized["chatType"],
                        "is_group": bool(normalized["isGroup"]),
                        "is_at_bot": bool(normalized["isAtBot"]),
                    },
                    "raw_callback": raw,
                },
            )
            with self._lock:
                self._inbox.append(inbound)

        class StreamBotHandler(dingtalk_stream.chatbot.ChatbotHandler):
            async def process(self, callback):  # pragma: no cover - callback signature from sdk
                try:
                    data: Any = getattr(callback, "data", callback)
                    normalized_data = _jsonable(data)
                    if not isinstance(normalized_data, dict):
                        normalized_data = {"raw": normalized_data}
                    push_raw_message(normalized_data)
                except Exception as exc:
                    print(f"[dingtalk-stream] callback_error err={exc}")
                return dingtalk_stream.AckMessage.STATUS_OK, "OK"

        topic = dingtalk_stream.chatbot.ChatbotMessage.TOPIC
        client.register_callback_handler(topic, StreamBotHandler())

        def run_forever() -> None:
            self._started = True
            client.start_forever()

        self._stream_thread = threading.Thread(target=run_forever, daemon=True)
        self._stream_thread.start()

    def _ensure_ready(self) -> None:
        if self._start_error:
            raise RuntimeError(self._start_error)
        if not self._started:
            time.sleep(0.1)

    def fetch(self) -> list[InboundMessage]:
        self._ensure_ready()
        with self._lock:
            items = list(self._inbox)
            self._inbox.clear()
        return items

    def _run(self, cmd: str, *, env: dict[str, str] | None = None, timeout_sec: int = 60) -> tuple[int, str, str]:
        proc = subprocess.run(
            cmd,
            shell=True,
            text=True,
            capture_output=True,
            env=env,
            timeout=timeout_sec,
        )
        return proc.returncode, proc.stdout, proc.stderr

    def send(self, text: str, *, to: str, message_id: str, report_file: str = "") -> None:
        cmd_with_text = f"{self.send_cmd} {shlex.quote(text)}"
        env = os.environ.copy()
        env["SMS_TO"] = to
        env["SMS_MESSAGE_ID"] = message_id
        env["SMS_REPORT_FILE"] = report_file
        rc, _, err = self._run(cmd_with_text, env=env, timeout_sec=self.send_timeout_sec)
        if rc != 0:
            raise RuntimeError(f"send failed rc={rc} err={err.strip()}")
