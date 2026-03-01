#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_ENV="$SCRIPT_DIR/../.env"
LOCAL_ENV="$SCRIPT_DIR/.env"
if [[ -f "$ROOT_ENV" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "$ROOT_ENV"
  set +a
fi
if [[ -f "$LOCAL_ENV" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "$LOCAL_ENV"
  set +a
fi

CHAT_ID="${IMSG_CHAT_ID:?IMSG_CHAT_ID is required}"
LIMIT="${IMSG_FETCH_LIMIT:-30}"
INCLUDE_FROM_ME="${IMSG_INCLUDE_FROM_ME:-0}"

RAW="$(imsg history --chat-id "$CHAT_ID" --limit "$LIMIT" --json 2>&1)" || {
  echo "imsg history failed for chat_id=$CHAT_ID: $RAW" >&2
  exit 1
}

if ! printf '%s\n' "$RAW" | jq -e . >/dev/null 2>&1; then
  PREVIEW="$(printf '%s' "$RAW" | head -c 240)"
  echo "imsg history did not return JSON for chat_id=$CHAT_ID. preview: $PREVIEW" >&2
  echo "hint: grant Full Disk Access to terminal/iTerm and imsg for ~/Library/Messages/chat.db" >&2
  exit 1
fi

# 兼容 imsg 返回 array 或 NDJSON，忽略非对象 token。
printf '%s\n' "$RAW" | jq -cs --arg include_from_me "$INCLUDE_FROM_ME" '
  [
    .[]
    | (if type == "array" then .[] else . end)
    | select(type == "object")
    | select(($include_from_me == "1") or ((.is_from_me // false) == false))
    | {
        id: (.id|tostring),
        from: (.sender // ""),
        text: (.text // ""),
        ts: (.created_at // "")
      }
  ]
'
