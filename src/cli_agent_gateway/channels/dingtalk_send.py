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


def _signed_webhook(base_url: str, secret: str) -> str:
    ts = str(int(time.time() * 1000))
    string_to_sign = f"{ts}\n{secret}"
    digest = hmac.new(secret.encode("utf-8"), string_to_sign.encode("utf-8"), hashlib.sha256).digest()
    sign = urllib.parse.quote_plus(base64.b64encode(digest))

    sep = "&" if "?" in base_url else "?"
    return f"{base_url}{sep}timestamp={ts}&sign={sign}"


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

    webhook = os.getenv("DINGTALK_BOT_WEBHOOK", "").strip()
    if not webhook:
        print("DINGTALK_BOT_WEBHOOK is required", file=sys.stderr)
        raise SystemExit(2)

    secret = os.getenv("DINGTALK_BOT_SECRET", "").strip()
    timeout_sec = int(os.getenv("DINGTALK_SEND_TIMEOUT_SEC", "10"))

    url = _signed_webhook(webhook, secret) if secret else webhook
    payload = {
        "msgtype": "text",
        "text": {"content": text},
    }
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(
        url=url,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )

    try:
        with urllib.request.urlopen(req, timeout=timeout_sec) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
    except Exception as exc:
        print(f"dingtalk send failed: {exc}", file=sys.stderr)
        raise SystemExit(1)

    try:
        node = json.loads(raw) if raw else {}
    except Exception:
        node = {}

    # 钉钉成功通常为 errcode=0
    errcode = node.get("errcode", 0)
    if int(errcode) != 0:
        errmsg = node.get("errmsg", "unknown error")
        print(f"dingtalk send failed: errcode={errcode} errmsg={errmsg}", file=sys.stderr)
        raise SystemExit(1)


if __name__ == "__main__":
    main()
