#!/bin/zsh
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/create_macos_launcher.sh [options]

Create a clickable macOS .app launcher for cli-agent-gateway.

Options:
  --output-dir <dir>   Destination directory for the .app bundle (default: ~/Desktop)
  --app-name <name>    App bundle display name (default: CLI Agent Gateway)
  --ui-mode <mode>     Launcher mode: terminal|gui (default: terminal)
  --repo-root <dir>    Repository root path (default: auto-detected)
  --workdir <dir>      Workdir argument passed to app.main (default: CODEX_WORKDIR from .env or repo root)
  --help               Show this help
EOF
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEFAULT_REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

OUTPUT_DIR="$HOME/Desktop"
APP_NAME="CLI Agent Gateway"
UI_MODE="terminal"
REPO_ROOT="$DEFAULT_REPO_ROOT"
WORKDIR=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output-dir)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    --app-name)
      APP_NAME="$2"
      shift 2
      ;;
    --ui-mode)
      UI_MODE="$2"
      shift 2
      ;;
    --repo-root)
      REPO_ROOT="$2"
      shift 2
      ;;
    --workdir)
      WORKDIR="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "[ERROR] Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "$UI_MODE" != "terminal" && "$UI_MODE" != "gui" ]]; then
  echo "[ERROR] --ui-mode must be one of: terminal, gui" >&2
  exit 2
fi

REPO_ROOT="$(cd "$REPO_ROOT" && pwd)"
if [[ ! -d "$REPO_ROOT/src" ]]; then
  echo "[ERROR] Invalid repo root (missing src/): $REPO_ROOT" >&2
  exit 2
fi

if [[ -z "$WORKDIR" ]]; then
  ENV_FILE="$REPO_ROOT/.env"
  if [[ -f "$ENV_FILE" ]]; then
    WORKDIR="$(awk -F= '/^CODEX_WORKDIR=/{print $2; exit}' "$ENV_FILE" | tr -d '"' | tr -d "'")"
  fi
fi
if [[ -z "$WORKDIR" ]]; then
  WORKDIR="$REPO_ROOT"
fi

LOCK_FILE_RAW=""
ENV_FILE="$REPO_ROOT/.env"
if [[ -f "$ENV_FILE" ]]; then
  LOCK_FILE_RAW="$(awk -F= '/^LOCK_FILE=/{print $2; exit}' "$ENV_FILE" | tr -d '"' | tr -d "'")"
fi
if [[ -z "$LOCK_FILE_RAW" ]]; then
  LOCK_FILE="$REPO_ROOT/.cli_agent_gateway.lock"
elif [[ "$LOCK_FILE_RAW" = /* ]]; then
  LOCK_FILE="$LOCK_FILE_RAW"
else
  LOCK_FILE="$REPO_ROOT/$LOCK_FILE_RAW"
fi

mkdir -p "$OUTPUT_DIR"
APP_PATH="$OUTPUT_DIR/$APP_NAME.app"
CONTENTS_PATH="$APP_PATH/Contents"
MACOS_PATH="$CONTENTS_PATH/MacOS"

rm -rf "$APP_PATH"
mkdir -p "$MACOS_PATH"

LAUNCH_SCRIPT="$MACOS_PATH/launch_gateway"
if [[ "$UI_MODE" == "terminal" ]]; then
  RUN_CMD="cd \"$REPO_ROOT\" && PYTHONPATH=src python3 -m app.main \"$WORKDIR\""
  RUN_CMD="$RUN_CMD; echo; echo \"[launcher] Process exited. Press Enter to close this window.\"; read -r _"
  ESCAPED_CMD="${RUN_CMD//\\/\\\\}"
  ESCAPED_CMD="${ESCAPED_CMD//\"/\\\"}"

  cat >"$LAUNCH_SCRIPT" <<EOF
#!/bin/zsh
set -euo pipefail

osascript <<'APPLESCRIPT'
tell application "Terminal"
  activate
  do script "$ESCAPED_CMD"
end tell
APPLESCRIPT
EOF
else
  {
    echo '#!/bin/zsh'
    echo 'set -euo pipefail'
    printf 'REPO_ROOT=%q\n' "$REPO_ROOT"
    printf 'WORKDIR=%q\n' "$WORKDIR"
    printf 'LOCK_FILE=%q\n' "$LOCK_FILE"
    cat <<'EOF'
LOG_DIR="$HOME/Library/Logs/cli-agent-gateway"
LOG_FILE="$LOG_DIR/gateway.log"

mkdir -p "$LOG_DIR"
touch "$LOG_FILE"

get_lock_pid() {
  python3 - "$LOCK_FILE" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
if not path.exists():
    print("")
    raise SystemExit(0)
try:
    data = json.loads(path.read_text(encoding="utf-8"))
except Exception:
    print("")
    raise SystemExit(0)
pid = data.get("pid")
if isinstance(pid, int):
    print(pid)
else:
    print("")
PY
}

is_running() {
  local pid
  pid="$(get_lock_pid)"
  if [[ -z "$pid" ]]; then
    return 1
  fi
  kill -0 "$pid" >/dev/null 2>&1
}

show_ok() {
  local msg="$1"
  osascript -e "display dialog \"$msg\" buttons {\"OK\"} default button \"OK\" with title \"CLI Agent Gateway\"" >/dev/null
}

start_gateway() {
  if is_running; then
    show_ok "Gateway is already running."
    return 0
  fi
  (
    cd "$REPO_ROOT"
    nohup env PYTHONPATH=src python3 -m app.main "$WORKDIR" >>"$LOG_FILE" 2>&1 &
  )
  sleep 1
  if is_running; then
    osascript -e 'display notification "Gateway started in background." with title "CLI Agent Gateway"' >/dev/null
  else
    show_ok "Start failed. Check logs at $LOG_FILE"
  fi
}

stop_gateway() {
  local pid
  pid="$(get_lock_pid)"
  if [[ -z "$pid" ]] || ! kill -0 "$pid" >/dev/null 2>&1; then
    show_ok "Gateway is not running."
    return 0
  fi
  kill "$pid" >/dev/null 2>&1 || true
  sleep 1
  if kill -0 "$pid" >/dev/null 2>&1; then
    show_ok "Failed to stop PID $pid. Try stopping it manually."
  else
    osascript -e 'display notification "Gateway stopped." with title "CLI Agent Gateway"' >/dev/null
  fi
}

status_gateway() {
  local pid
  pid="$(get_lock_pid)"
  if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
    show_ok "Gateway is running (PID: $pid). Log: $LOG_FILE"
  else
    show_ok "Gateway is not running. Log: $LOG_FILE"
  fi
}

open_logs() {
  open -a TextEdit "$LOG_FILE"
}

ACTION="$(osascript <<'APPLESCRIPT'
set selected to button returned of (display dialog "Choose an action" buttons {"Cancel", "Status", "Stop", "Open Logs", "Start"} default button "Start" with title "CLI Agent Gateway")
return selected
APPLESCRIPT
)"

case "$ACTION" in
  "Start")
    start_gateway
    ;;
  "Stop")
    stop_gateway
    ;;
  "Status")
    status_gateway
    ;;
  "Open Logs")
    open_logs
    ;;
  *)
    exit 0
    ;;
esac
EOF
  } >"$LAUNCH_SCRIPT"
fi
chmod +x "$LAUNCH_SCRIPT"

cat >"$CONTENTS_PATH/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleExecutable</key>
  <string>launch_gateway</string>
  <key>CFBundleIdentifier</key>
  <string>com.cli-agent-gateway.launcher</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>$APP_NAME</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>1.0</string>
  <key>CFBundleVersion</key>
  <string>1</string>
  <key>LSMinimumSystemVersion</key>
  <string>12.0</string>
</dict>
</plist>
EOF

echo "[OK] Launcher created: $APP_PATH"
echo "[INFO] repo_root=$REPO_ROOT"
echo "[INFO] workdir=$WORKDIR"
echo "[INFO] ui_mode=$UI_MODE"
