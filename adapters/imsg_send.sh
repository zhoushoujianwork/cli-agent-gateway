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

TEXT="${1:?message text is required as \$1}"
SERVICE="${IMSG_SEND_SERVICE:-auto}"
CHAT_ID="${IMSG_SEND_CHAT_ID:-}"

# 强制 UTF-8，确保中文和 emoji 透传
export LANG="${LANG:-en_US.UTF-8}"
export LC_ALL="${LC_ALL:-en_US.UTF-8}"

if [[ -n "$CHAT_ID" ]]; then
  imsg send --chat-id "$CHAT_ID" --text "$TEXT" --service "$SERVICE" >/dev/null
else
  TO="${SMS_TO:?SMS_TO is required when IMSG_SEND_CHAT_ID is empty}"
  imsg send --to "$TO" --text "$TEXT" --service "$SERVICE" >/dev/null
fi
