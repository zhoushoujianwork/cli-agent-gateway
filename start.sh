#!/usr/bin/env sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
ENV_FILE="$SCRIPT_DIR/.env"

if [ -f "$ENV_FILE" ]; then
  set -a
  . "$ENV_FILE"
  set +a
fi

SMS_FETCH_CMD="${SMS_FETCH_CMD:-IMSG_CHAT_ID=98 $SCRIPT_DIR/adapters/imsg_fetch.sh}"
SMS_SEND_CMD="${SMS_SEND_CMD:-$SCRIPT_DIR/adapters/imsg_send.sh}"
IMSG_SEND_CHAT_ID="${IMSG_SEND_CHAT_ID:-98}"
PROCESS_ONLY_LATEST="${PROCESS_ONLY_LATEST:-1}"
CODEX_WORKDIR="${CODEX_WORKDIR:-/Volumes/1TB/github/daboluocc}"

export SMS_FETCH_CMD SMS_SEND_CMD IMSG_SEND_CHAT_ID PROCESS_ONLY_LATEST

python3 "$SCRIPT_DIR/run.py" "$CODEX_WORKDIR"
