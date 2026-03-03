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
from urllib.error import HTTPError
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


def _normalize_fetch_message(node: dict[str, Any]) -> dict[str, Any] | None:
    text = _sanitize(node.get("text", node.get("content", node.get("msg", ""))))
    if not text:
        return None

    sender = _sanitize(node.get("senderStaffId", node.get("senderId", node.get("from", ""))))
    sender_name = _sanitize(
        node.get("senderName", node.get("senderNick", node.get("fromName", node.get("sender_name", ""))))
    )
    sender_union_id = _sanitize(node.get("senderUnionId", node.get("sender_union_id", "")))
    ts = _sanitize(node.get("ts", node.get("createAt", node.get("timestamp", ""))))
    conversation_id = _sanitize(node.get("conversationId", node.get("cid", "")))
    chat_type = _sanitize(node.get("chatType", node.get("conversationType", "")))
    thread_id = _sanitize(node.get("threadId", ""))
    thread_key = f"{conversation_id}:{thread_id}" if thread_id else conversation_id
    is_group = chat_type.lower() in {"group", "groupchat", "2"} or bool(node.get("isGroup", False))
    is_at_bot = bool(node.get("isAtBot", node.get("atBot", False)))

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
        "sender_staff_id": sender,
        "sender_name": sender_name,
        "sender_union_id": sender_union_id,
        "conversation_id": conversation_id,
        "chat_type": chat_type,
        "is_group": is_group,
        "is_at_bot": is_at_bot,
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


def _request_json(
    url: str,
    payload: dict[str, Any] | None = None,
    timeout_sec: int = 10,
    headers: dict[str, str] | None = None,
    method: str | None = None,
) -> dict[str, Any]:
    data = None
    req_headers = {"Content-Type": "application/json"}
    if headers:
        req_headers.update(headers)
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req_method = method or ("POST" if payload is not None else "GET")
    req = urllib.request.Request(url=url, data=data, headers=req_headers, method=req_method)
    try:
        with urllib.request.urlopen(req, timeout=timeout_sec) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
    except HTTPError as exc:
        body = ""
        try:
            body = exc.read().decode("utf-8", errors="replace")
        except Exception:
            body = ""
        raise RuntimeError(f"http_error status={exc.code} body={body[:400]}") from exc
    try:
        return json.loads(raw) if raw else {}
    except Exception:
        return {}


def _card_api_log_file() -> Path:
    raw = _sanitize(os.getenv("DINGTALK_CARD_API_LOG", "logs/dingtalk_card_api.jsonl"))
    path = Path(raw).expanduser()
    if path.is_absolute():
        return path
    return (_repo_root() / path).resolve()


def _append_card_api_log(record: dict[str, Any]) -> None:
    path = _card_api_log_file()
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = {"ts": utc_now(), **record}
    with path.open("a", encoding="utf-8") as f:
        f.write(json.dumps(payload, ensure_ascii=False) + "\n")


def _short_text(value: Any, limit: int = 300) -> str:
    text = _sanitize(value)
    if len(text) <= limit:
        return text
    return text[: limit - 3] + "..."


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
    payload = _build_webhook_payload(text)
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
    msgtype = _sanitize(os.getenv("DINGTALK_SEND_MSGTYPE", "markdown")).lower()
    if msgtype == "card":
        try:
            _send_api_card(text=text, user_id=user_id, access_token=token, timeout_sec=timeout_sec)
            return
        except Exception as exc:
            if os.getenv("DINGTALK_CARD_FALLBACK_MARKDOWN", "1").strip() == "0":
                raise
            print(f"dingtalk card send failed, fallback to markdown: {exc}", file=sys.stderr)
    send_base = os.getenv("DINGTALK_SEND_URL", "https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2").strip()
    send_url = f"{send_base}?access_token={urllib.parse.quote_plus(token)}"

    payload = {
        "agent_id": int(agent_id),
        "userid_list": user_id,
        "msg": _build_api_msg(text),
    }
    node = _request_json(send_url, payload=payload, timeout_sec=timeout_sec)
    if int(node.get("errcode", 0)) != 0:
        raise RuntimeError(f"api send failed: errcode={node.get('errcode')} errmsg={node.get('errmsg')}")


def _message_phase(message_id: str) -> str:
    mid = _sanitize(message_id).lower()
    if mid.startswith("ack-"):
        return "ack"
    if mid.startswith("progress-"):
        return "progress"
    if mid.startswith("error-"):
        return "error"
    return "final"


def _phase_cn_label(phase: str) -> str:
    if phase == "ack":
        return "已接收"
    if phase == "progress":
        return "处理中"
    if phase == "error":
        return "处理失败"
    return "处理完成"


def _build_markdown_body(text: str) -> tuple[str, str]:
    message_id = _sanitize(os.getenv("SMS_MESSAGE_ID", ""))
    phase = _message_phase(message_id)
    pretty = os.getenv("DINGTALK_PRETTY_STATUS", "1").strip() != "0"
    show_meta = os.getenv("DINGTALK_SHOW_META", "0").strip() != "0"
    base_title = _sanitize(os.getenv("DINGTALK_MARKDOWN_TITLE", "CLI Agent Gateway")) or "CLI Agent Gateway"
    title = f"{base_title} - {_phase_cn_label(phase)}"
    if not pretty:
        return title, text
    content = f"{_phase_cn_label(phase)}\n\n{text}"
    if show_meta:
        ts = utc_now()
        content += f"\n\n> message_id: {message_id or '(none)'}\n> ts: {ts}"
    return title, content


def _build_api_msg(text: str) -> dict[str, Any]:
    msgtype = _sanitize(os.getenv("DINGTALK_SEND_MSGTYPE", "markdown")).lower()
    if msgtype == "text":
        return {"msgtype": "text", "text": {"content": text}}
    title, body = _build_markdown_body(text)
    return {"msgtype": "markdown", "markdown": {"title": title, "text": body}}


def _build_webhook_payload(text: str) -> dict[str, Any]:
    msgtype = _sanitize(os.getenv("DINGTALK_SEND_MSGTYPE", "markdown")).lower()
    if msgtype == "text":
        return {"msgtype": "text", "text": {"content": text}}
    title, body = _build_markdown_body(text)
    return {"msgtype": "markdown", "markdown": {"title": title, "text": body}}


def _build_card_data(*, phase: str, text: str, root_id: str, history: list[dict[str, Any]]) -> dict[str, Any]:
    title, final_body = _build_markdown_body(text)
    status = _phase_cn_label(phase)
    simple_lines = [status, "", _sanitize(text)]
    final_body = "\n".join(simple_lines)
    try:
        from dingtalk_stream.interactive_card import generate_multi_text_line_card_data  # type: ignore

        return generate_multi_text_line_card_data(title=title, logo="", texts=[final_body])
    except Exception:
        # Fallback structure for StandardCard
        return {
            "header": {"title": {"type": "text", "text": title}},
            "contents": [{"type": "markdown", "text": final_body}],
        }


def _send_api_card(*, text: str, user_id: str, access_token: str, timeout_sec: int) -> None:
    robot_code = _sanitize(os.getenv("DINGTALK_ROBOT_CODE", os.getenv("DINGTALK_APP_KEY", "")))
    if not robot_code:
        raise RuntimeError("DINGTALK_ROBOT_CODE (or DINGTALK_APP_KEY) is required for card mode")
    card_template_id = _sanitize(os.getenv("DINGTALK_CARD_TEMPLATE_ID", "StandardCard")) or "StandardCard"
    msg_id = _sanitize(os.getenv("SMS_MESSAGE_ID", ""))
    phase = _message_phase(msg_id)
    root_id = _root_trace_id(msg_id)
    card_biz_id = _build_card_biz_id(root_id=root_id, user_id=user_id)
    os.environ["DINGTALK_CARD_BIZ_ID"] = card_biz_id
    os.environ["DINGTALK_CARD_ROOT_ID"] = root_id
    single_message_mode = os.getenv("DINGTALK_CARD_SINGLE_MESSAGE", "1").strip() != "0"
    update_fallback_send = os.getenv("DINGTALK_CARD_UPDATE_FALLBACK_SEND", "1").strip() != "0"
    old_history = _load_card_history(card_biz_id)
    new_history = _history_with_new_entry(old_history, phase=phase, text=text)

    if single_message_mode and phase in {"progress", "final", "error"}:
        retry_secs = _parse_retry_seconds(os.getenv("DINGTALK_CARD_UPDATE_RETRY_SEC", "1,2,4"))
        try:
            for idx, wait_sec in enumerate([0] + retry_secs):
                if wait_sec > 0:
                    time.sleep(wait_sec)
                try:
                    _update_api_card(
                        card_biz_id=card_biz_id,
                        card_data=_build_card_data(phase=phase, text=text, root_id=root_id, history=new_history),
                        access_token=access_token,
                        timeout_sec=timeout_sec,
                    )
                    _save_card_history(card_biz_id=card_biz_id, root_id=root_id, history=new_history)
                    return
                except Exception as update_exc:
                    if idx == len(retry_secs):
                        raise
                    if not _is_retryable_card_update_error(update_exc):
                        raise
        except Exception as exc:
            if not update_fallback_send:
                raise
            print(f"dingtalk card update failed, fallback to send: {exc}", file=sys.stderr)

    _send_api_card_new(
        phase=phase,
        text=text,
        root_id=root_id,
        history=new_history,
        user_id=user_id,
        robot_code=robot_code,
        card_template_id=card_template_id,
        card_biz_id=card_biz_id,
        access_token=access_token,
        timeout_sec=timeout_sec,
    )
    _save_card_history(card_biz_id=card_biz_id, root_id=root_id, history=new_history)


def _root_trace_id(message_id: str) -> str:
    mid = _sanitize(message_id)
    phase = _message_phase(mid)
    if phase == "ack" and mid.startswith("ack-"):
        return mid[4:]
    if phase == "error" and mid.startswith("error-"):
        return mid[6:]
    if phase == "progress" and mid.startswith("progress-"):
        body = mid[9:]
        base, sep, tail = body.rpartition("-")
        if sep and tail.isdigit() and base:
            return base
        return body
    return mid


def _build_card_biz_id(*, root_id: str, user_id: str) -> str:
    rid = root_id or f"no-root-{int(time.time())}"
    sig = hashlib.sha256(f"{user_id}|{rid}".encode("utf-8")).hexdigest()[:24]
    return f"cag-{sig}"


def _phase_status_line(phase: str) -> str:
    if phase == "final":
        return "最终状态：成功"
    if phase == "error":
        return "最终状态：失败"
    if phase == "progress":
        return "当前状态：处理中"
    return "当前状态：已接收"


def _card_title(phase: str) -> str:
    base = _sanitize(os.getenv("DINGTALK_MARKDOWN_TITLE", "CLI Agent Gateway")) or "CLI Agent Gateway"
    if phase == "final":
        return f"{base} - 成功"
    if phase == "error":
        return f"{base} - 失败"
    if phase == "progress":
        return f"{base} - 处理中"
    return f"{base} - 已接收"


def _card_state_file() -> Path:
    raw = _sanitize(os.getenv("DINGTALK_CARD_HISTORY_FILE", "logs/dingtalk_card_state.json"))
    path = Path(raw).expanduser()
    if path.is_absolute():
        return path
    return (_repo_root() / path).resolve()


def _history_max_entries() -> int:
    raw = _sanitize(os.getenv("DINGTALK_CARD_HISTORY_MAX", "12"))
    try:
        n = int(raw)
    except Exception:
        n = 12
    return max(1, min(50, n))


def _load_card_state() -> dict[str, Any]:
    path = _card_state_file()
    if not path.exists():
        return {}
    try:
        node = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return {}
    if not isinstance(node, dict):
        return {}
    return node


def _save_card_state(state: dict[str, Any]) -> None:
    path = _card_state_file()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(state, ensure_ascii=False, indent=2), encoding="utf-8")


def _append_card_history(*, card_biz_id: str, phase: str, text: str, root_id: str) -> None:
    if not card_biz_id:
        return
    state = _load_card_state()
    item = state.get(card_biz_id)
    if not isinstance(item, dict):
        item = {"root_id": root_id, "updated_at": utc_now(), "history": []}
    history = item.get("history")
    if not isinstance(history, list):
        history = []
    history.append(
        {
            "ts": utc_now(),
            "phase": phase,
            "text": _sanitize(text)[:500],
        }
    )
    max_entries = _history_max_entries()
    if len(history) > max_entries:
        history = history[-max_entries:]
    item["history"] = history
    item["updated_at"] = utc_now()
    item["root_id"] = root_id
    state[card_biz_id] = item
    _save_card_state(state)


def _history_with_new_entry(history: list[dict[str, Any]], *, phase: str, text: str) -> list[dict[str, Any]]:
    merged = [x for x in history if isinstance(x, dict)]
    merged.append({"ts": utc_now(), "phase": phase, "text": _sanitize(text)[:500]})
    max_entries = _history_max_entries()
    if len(merged) > max_entries:
        merged = merged[-max_entries:]
    return merged


def _save_card_history(*, card_biz_id: str, root_id: str, history: list[dict[str, Any]]) -> None:
    if not card_biz_id:
        return
    state = _load_card_state()
    state[card_biz_id] = {"root_id": root_id, "updated_at": utc_now(), "history": history[-_history_max_entries():]}
    _save_card_state(state)


def _load_card_history(card_biz_id: str) -> list[dict[str, Any]]:
    if not card_biz_id:
        return []
    state = _load_card_state()
    item = state.get(card_biz_id)
    if not isinstance(item, dict):
        return []
    history = item.get("history")
    if not isinstance(history, list):
        return []
    return [x for x in history if isinstance(x, dict)]


def _phase_icon(phase: str) -> str:
    if phase == "final":
        return "SUCCESS"
    if phase == "error":
        return "FAILED"
    return "RUNNING"


def _build_history_markdown(history: list[dict[str, Any]]) -> str:
    if not history:
        return ""
    lines = ["### 历史进度"]
    for h in history[-_history_max_entries():]:
        ts = _sanitize(h.get("ts", ""))
        phase = _sanitize(h.get("phase", ""))
        text = _sanitize(h.get("text", ""))
        if not text:
            continue
        lines.append(f"- {_phase_icon(phase)} `{ts}` {text}")
    return "\n".join(lines)


def _build_card_plain_body(*, phase: str, text: str, root_id: str, history: list[dict[str, Any]]) -> str:
    lines = [_phase_status_line(phase).replace("**", ""), f"内容: {_sanitize(text)}"]
    if root_id:
        lines.append(f"任务ID: {root_id}")
    if history:
        lines.append("历史进度:")
        for h in history[-_history_max_entries():]:
            ts = _sanitize(h.get("ts", ""))[-8:]
            h_phase = _sanitize(h.get("phase", ""))
            h_text = _sanitize(h.get("text", ""))
            if h_text:
                lines.append(f"{_phase_icon(h_phase)} {ts} {h_text}")
    return "\n".join(lines)


def _parse_retry_seconds(raw: str) -> list[float]:
    values: list[float] = []
    for seg in _sanitize(raw).split(","):
        if not seg:
            continue
        try:
            sec = float(seg)
        except Exception:
            continue
        if sec > 0:
            values.append(sec)
    return values


def _is_retryable_card_update_error(exc: Exception) -> bool:
    text = str(exc).lower()
    return (
        "create.cardinstance.failed" in text
        or "not found" in text
        or "notfound" in text
        or "status=404" in text
    )


def _assert_ok_response(resp: dict[str, Any], *, action: str) -> None:
    if int(resp.get("errcode", 0)) != 0:
        raise RuntimeError(f"card {action} failed: errcode={resp.get('errcode')} errmsg={resp.get('errmsg')}")
    if isinstance(resp.get("success"), bool) and not resp.get("success"):
        raise RuntimeError(f"card {action} failed: success=false resp={json.dumps(resp, ensure_ascii=False)[:300]}")
    if str(resp.get("code", "")).strip() not in {"", "0"}:
        raise RuntimeError(f"card {action} failed: code={resp.get('code')} msg={resp.get('message')}")


def _send_api_card_new(
    *,
    phase: str,
    text: str,
    root_id: str,
    history: list[dict[str, Any]],
    user_id: str,
    robot_code: str,
    card_template_id: str,
    card_biz_id: str,
    access_token: str,
    timeout_sec: int,
) -> None:
    payload = {
        "cardTemplateId": card_template_id,
        "robotCode": robot_code,
        "cardData": json.dumps(_build_card_data(phase=phase, text=text, root_id=root_id, history=history), ensure_ascii=False),
        "sendOptions": {"atAll": False},
        "cardBizId": card_biz_id,
        "singleChatReceiver": json.dumps({"userId": user_id}, ensure_ascii=False),
    }
    url = _sanitize(os.getenv("DINGTALK_CARD_SEND_URL", "https://api.dingtalk.com/v1.0/im/v1.0/robot/interactiveCards/send"))
    req_summary = {
        "action": "card_send",
        "url": url,
        "cardBizId": card_biz_id,
        "cardTemplateId": card_template_id,
        "userId": user_id,
        "payload_keys": sorted(payload.keys()),
        "cardData_len": len(_sanitize(payload.get("cardData", ""))),
    }
    try:
        resp = _request_json(
            url,
            payload=payload,
            timeout_sec=timeout_sec,
            headers={"x-acs-dingtalk-access-token": access_token},
        )
        if isinstance(resp, dict):
            _assert_ok_response(resp, action="send")
            _append_card_api_log(
                {
                    **req_summary,
                    "ok": True,
                    "resp_code": _short_text(resp.get("code", "")),
                    "resp_message": _short_text(resp.get("message", "")),
                    "resp_errcode": _short_text(resp.get("errcode", "")),
                    "resp_errmsg": _short_text(resp.get("errmsg", "")),
                }
            )
    except Exception as exc:
        _append_card_api_log(
            {
                **req_summary,
                "ok": False,
                "error": _short_text(str(exc), 500),
            }
        )
        raise


def _update_api_card(
    *,
    card_biz_id: str,
    card_data: dict[str, Any],
    access_token: str,
    timeout_sec: int,
) -> None:
    payload = {
        "cardBizId": card_biz_id,
        "cardData": json.dumps(card_data, ensure_ascii=False),
    }
    url = _sanitize(os.getenv("DINGTALK_CARD_UPDATE_URL", "https://api.dingtalk.com/v1.0/im/robots/interactiveCards"))
    req_summary = {
        "action": "card_update",
        "url": url,
        "cardBizId": card_biz_id,
        "payload_keys": sorted(payload.keys()),
        "cardData_len": len(_sanitize(payload.get("cardData", ""))),
    }
    try:
        resp = _request_json(
            url,
            payload=payload,
            timeout_sec=timeout_sec,
            headers={"x-acs-dingtalk-access-token": access_token},
            method="PUT",
        )
        if isinstance(resp, dict):
            _assert_ok_response(resp, action="update")
            _append_card_api_log(
                {
                    **req_summary,
                    "ok": True,
                    "resp_code": _short_text(resp.get("code", "")),
                    "resp_message": _short_text(resp.get("message", "")),
                    "resp_errcode": _short_text(resp.get("errcode", "")),
                    "resp_errmsg": _short_text(resp.get("errmsg", "")),
                }
            )
    except Exception as exc:
        _append_card_api_log(
            {
                **req_summary,
                "ok": False,
                "error": _short_text(str(exc), 500),
            }
        )
        raise


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
        "senderName": _sanitize(msg.get("senderName", msg.get("senderNick", msg.get("fromName", "")))),
        "senderUnionId": _sanitize(msg.get("senderUnionId", "")),
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
