#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   bash adapters/imsg_notify.sh --text "处理中..." [--to "+861995..."] [--log-file "/abs/path.jsonl"] [--message-id "id"]

TO=""
TEXT=""
LOG_FILE="${INTERACTION_LOG_FILE:-}"
MESSAGE_ID=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --to)
      TO="${2:-}"
      shift 2
      ;;
    --text)
      TEXT="${2:-}"
      shift 2
      ;;
    --log-file)
      LOG_FILE="${2:-}"
      shift 2
      ;;
    --message-id)
      MESSAGE_ID="${2:-}"
      shift 2
      ;;
    *)
      echo "unknown arg: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "$TEXT" ]]; then
  echo "--text is required" >&2
  exit 2
fi

if [[ -z "$TO" ]]; then
  TO="${REMOTE_USER_ID:-<USER_IMESSAGE_ID>}"
fi

if [[ -z "$MESSAGE_ID" ]]; then
  MESSAGE_ID="agent-$(date +%s)"
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SEND_SCRIPT="$SCRIPT_DIR/imsg_send.sh"

export SMS_TO="$TO"
export SMS_REPORT_FILE=""
export SMS_MESSAGE_ID="$MESSAGE_ID"
bash "$SEND_SCRIPT" "$TEXT"

if [[ -n "$LOG_FILE" ]]; then
  mkdir -p "$(dirname "$LOG_FILE")"
  python3 - "$LOG_FILE" "$TO" "$TEXT" "$MESSAGE_ID" <<'PY'
import json
import sys
from datetime import datetime, timezone

log_file, to, text, message_id = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
rec = {
    "time": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "kind": "agent_notify_sent",
    "to": to,
    "text": text,
    "message_id": message_id,
}
with open(log_file, "a", encoding="utf-8") as f:
    f.write(json.dumps(rec, ensure_ascii=False) + "\n")
PY
fi
