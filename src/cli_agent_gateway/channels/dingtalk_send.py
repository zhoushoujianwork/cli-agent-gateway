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

    # Prefer explicit token endpoint. Default uses legacy-compatible endpoint.
    token_url = os.getenv("DINGTALK_TOKEN_URL", "").strip()
    if token_url:
        node = _request_json(token_url, payload={"appKey": app_key, "appSecret": app_secret}, timeout_sec=timeout_sec)
        token = str(node.get("accessToken", node.get("access_token", ""))).strip()
        if token:
            return token

    legacy_url = (
        "https://oapi.dingtalk.com/gettoken?"
        + urllib.parse.urlencode({"appkey": app_key, "appsecret": app_secret})
    )
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

    # Legacy endpoint is broadly available and suitable for enterprise app message push.
    send_base = os.getenv(
        "DINGTALK_SEND_URL",
        "https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2",
    ).strip()
    send_url = f"{send_base}?access_token={urllib.parse.quote_plus(token)}"

    payload = {
        "agent_id": int(agent_id),
        "userid_list": user_id,
        "msg": {"msgtype": "text", "text": {"content": text}},
    }
    node = _request_json(send_url, payload=payload, timeout_sec=timeout_sec)
    if int(node.get("errcode", 0)) != 0:
        raise RuntimeError(f"api send failed: errcode={node.get('errcode')} errmsg={node.get('errmsg')}")


def main() -> None:
    if len(sys.argv) < 2:
        print("message text is required as $1", file=sys.stderr)
        raise SystemExit(2)

    repo_root = Path(__file__).resolve().parents[3]
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


if __name__ == "__main__":
    main()
