#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT_DIR/.env"

CLOUDX_ID="${CLOUDX_ID:-cloudx@example.com}"
TIMEOUT_SEC="${AUTOCONFIG_TIMEOUT_SEC:-180}"
POLL_SEC=3

if ! command -v imsg >/dev/null 2>&1; then
  echo "[FATAL] imsg not found. Install imsg first." >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "[FATAL] jq not found. Install jq first." >&2
  exit 2
fi

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cloud-id)
      CLOUDX_ID="${2:-}"
      shift 2
      ;;
    --timeout)
      TIMEOUT_SEC="${2:-180}"
      shift 2
      ;;
    *)
      echo "unknown arg: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "$CLOUDX_ID" ]]; then
  echo "[FATAL] CLOUDX_ID is empty. Use --cloud-id <email_or_id>." >&2
  exit 2
fi

PAIR_CODE="CXPAIR-$(date +%H%M%S)-$((RANDOM % 900 + 100))"

echo "== sms-codex-loop iMessage Auto Config =="
echo "Cloud X endpoint ID: $CLOUDX_ID"
echo
echo "请在手机端发送以下内容到 Cloud X 的 iMessage 会话："
echo "  $PAIR_CODE"
echo
echo "发送后按回车开始自动匹配..."
read -r _

start_ts="$(date +%s)"
detected_chat_id=""
detected_sender=""

while true; do
  now_ts="$(date +%s)"
  if (( now_ts - start_ts > TIMEOUT_SEC )); then
    echo "[FATAL] auto match timeout (${TIMEOUT_SEC}s)." >&2
    exit 1
  fi

  while IFS= read -r chat_line; do
    [[ -z "$chat_line" ]] && continue
    service="$(printf '%s' "$chat_line" | jq -r '.service // ""')"
    chat_id="$(printf '%s' "$chat_line" | jq -r '.id // empty')"
    [[ "$service" != "iMessage" || -z "$chat_id" ]] && continue

    hist_raw="$(imsg history --chat-id "$chat_id" --limit 20 --json 2>/dev/null || true)"
    [[ -z "$hist_raw" ]] && continue

    match="$(printf '%s\n' "$hist_raw" | jq -cs --arg code "$PAIR_CODE" '
      [
        .[]
        | (if type == "array" then .[] else . end)
        | select(type == "object")
        | select(((.text // "") | tostring | contains($code)))
      ] | last // empty
    ')"

    if [[ -n "$match" && "$match" != "null" ]]; then
      detected_chat_id="$chat_id"
      detected_sender="$(printf '%s' "$match" | jq -r '.sender // ""')"
      break 2
    fi
  done < <(imsg chats --json 2>/dev/null)

  sleep "$POLL_SEC"
done

if [[ -z "$detected_chat_id" || -z "$detected_sender" ]]; then
  echo "[FATAL] auto match failed." >&2
  exit 1
fi

upsert_env() {
  local key="$1"
  local value="$2"
  if [[ ! -f "$ENV_FILE" ]]; then
    touch "$ENV_FILE"
  fi
  if grep -qE "^${key}=" "$ENV_FILE"; then
    perl -0777 -pe "s#^${key}=.*\$#${key}=${value}#m" -i "$ENV_FILE"
  else
    printf '\n%s=%s\n' "$key" "$value" >> "$ENV_FILE"
  fi
}

upsert_env "IMSG_CHAT_ID" "$detected_chat_id"
upsert_env "IMSG_SEND_CHAT_ID" "$detected_chat_id"
upsert_env "REMOTE_USER_ID" "$detected_sender"
upsert_env "STARTUP_NOTIFY_TO" "$detected_sender"
upsert_env "SMS_FETCH_CMD" "\"IMSG_CHAT_ID=$detected_chat_id $ROOT_DIR/adapters/imsg_fetch.sh\""
upsert_env "SMS_SEND_CMD" "\"$ROOT_DIR/adapters/imsg_send.sh\""

echo
echo "[OK] auto config done."
echo "Detected chat_id   : $detected_chat_id"
echo "Detected user_id   : $detected_sender"
echo "Updated .env file  : $ENV_FILE"
echo
echo "Next:"
echo "  cd $ROOT_DIR"
echo "  sh start.sh"
