#!/usr/bin/env python3
from __future__ import annotations

import os
import subprocess
import sys
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


def main() -> None:
    if len(sys.argv) < 2:
        print("message text is required as $1", file=sys.stderr)
        raise SystemExit(2)

    repo_root = Path(__file__).resolve().parents[3]
    load_dotenv(repo_root / ".env")

    text = sys.argv[1]
    service = os.getenv("IMSG_SEND_SERVICE", "auto").strip() or "auto"
    chat_id = os.getenv("IMSG_SEND_CHAT_ID", "").strip()
    to = os.getenv("SMS_TO", "").strip()
    imsg_bin = os.getenv("IMSG_BIN", "imsg").strip() or "imsg"

    cmd = [imsg_bin, "send"]
    if chat_id:
        cmd.extend(["--chat-id", chat_id])
    else:
        if not to:
            print("SMS_TO is required when IMSG_SEND_CHAT_ID is empty", file=sys.stderr)
            raise SystemExit(2)
        cmd.extend(["--to", to])

    cmd.extend(["--text", text, "--service", service])
    proc = subprocess.run(cmd, text=True, capture_output=True)
    if proc.returncode != 0:
        err = proc.stderr.strip() or proc.stdout.strip()
        print(f"imsg send failed: {err}", file=sys.stderr)
        raise SystemExit(proc.returncode)


if __name__ == "__main__":
    main()
