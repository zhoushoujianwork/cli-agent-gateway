#!/bin/zsh
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/macos_gui_launch_and_capture.sh [options]

Build (optional), launch macOS GUI app, and capture one screenshot to a fixed temp location.

Options:
  --repo-root <dir>      Repository root (default: auto-detected)
  --app-name <name>      App bundle display name (default: CLI Agent Gateway GUI)
  --temp-root <dir>      Fixed temp root for artifacts (default: /tmp/cli-agent-gateway-macos-gui-loop)
  --skip-build           Skip app build and only launch existing app bundle
  --help                 Show this help
EOF
}

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "[ERROR] This script only supports macOS."
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
APP_NAME="CLI Agent Gateway GUI"
TEMP_ROOT="/tmp/cli-agent-gateway-macos-gui-loop"
SKIP_BUILD=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-root)
      REPO_ROOT="$2"
      shift 2
      ;;
    --app-name)
      APP_NAME="$2"
      shift 2
      ;;
    --temp-root)
      TEMP_ROOT="$2"
      shift 2
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
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

REPO_ROOT="$(cd "$REPO_ROOT" && pwd)"
BUILD_SCRIPT="$REPO_ROOT/macos/CLIApp/scripts/build_macos_gui_app.sh"
APP_DIR="$TEMP_ROOT/app"
APP_PATH="$APP_DIR/$APP_NAME.app"
EXEC_PATTERN="/Contents/MacOS/CLI Agent Gateway GUI"

if [[ ! -d "$REPO_ROOT/src" ]]; then
  echo "[ERROR] Invalid repo root (missing src/): $REPO_ROOT"
  exit 2
fi
if [[ "$SKIP_BUILD" == "0" && ! -x "$BUILD_SCRIPT" ]]; then
  echo "[ERROR] Build script is missing or not executable: $BUILD_SCRIPT"
  exit 2
fi

RUN_ID="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="$TEMP_ROOT/runs/$RUN_ID"
SCREENSHOT_PATH="$RUN_DIR/macos-gui.png"
LATEST_SCREENSHOT="$TEMP_ROOT/macos-gui-latest.png"
REPORT_PATH="$RUN_DIR/report.txt"
CAPTURE_MODE="full_screen_fallback"

mkdir -p "$APP_DIR" "$RUN_DIR"

if [[ "$SKIP_BUILD" == "0" ]]; then
  "$BUILD_SCRIPT" \
    --repo-root "$REPO_ROOT" \
    --output-dir "$APP_DIR" \
    --app-name "$APP_NAME" \
    --no-open
fi

if [[ ! -d "$APP_PATH" ]]; then
  echo "[ERROR] App bundle not found: $APP_PATH"
  exit 3
fi

restart_gui_app() {
  if ! pgrep -f "$EXEC_PATTERN" >/dev/null 2>&1; then
    return 0
  fi
  echo "[INFO] existing_gui_detected=1"
  osascript -e 'tell application id "com.cli-agent-gateway.gui" to quit' >/dev/null 2>&1 || true
  osascript -e "tell application \"$APP_NAME\" to quit" >/dev/null 2>&1 || true
  for _ in {1..20}; do
    if ! pgrep -f "$EXEC_PATTERN" >/dev/null 2>&1; then
      break
    fi
    sleep 0.25
  done
  if pgrep -f "$EXEC_PATTERN" >/dev/null 2>&1; then
    pkill -f "$EXEC_PATTERN" >/dev/null 2>&1 || true
    sleep 0.5
  fi
  if pgrep -f "$EXEC_PATTERN" >/dev/null 2>&1; then
    pkill -9 -f "$EXEC_PATTERN" >/dev/null 2>&1 || true
    sleep 0.5
  fi
}

restart_gui_app
open "$APP_PATH"

for _ in {1..30}; do
  if pgrep -f "$EXEC_PATTERN" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

if ! pgrep -f "$EXEC_PATTERN" >/dev/null 2>&1; then
  echo "[ERROR] App process did not appear after launch."
  exit 4
fi

osascript -e "tell application \"$APP_NAME\" to activate" >/dev/null 2>&1 || true
sleep 1

WINDOW_REGION=""
for _ in {1..10}; do
  WINDOW_REGION="$(
    osascript <<APPLESCRIPT 2>/dev/null || true
tell application "System Events"
  if not (exists process "$APP_NAME") then
    return ""
  end if
  tell process "$APP_NAME"
    if (count of windows) is 0 then
      return ""
    end if
    set p to position of front window
    set s to size of front window
    return (item 1 of p as text) & "," & (item 2 of p as text) & "," & (item 1 of s as text) & "," & (item 2 of s as text)
  end tell
end tell
APPLESCRIPT
  )"
  if [[ -n "$WINDOW_REGION" && "$WINDOW_REGION" == *,*,*,* ]]; then
    break
  fi
  sleep 0.5
done

if [[ -n "$WINDOW_REGION" && "$WINDOW_REGION" == *,*,*,* ]]; then
  screencapture -x -R"$WINDOW_REGION" "$SCREENSHOT_PATH"
  CAPTURE_MODE="window_region"
else
  screencapture -x "$SCREENSHOT_PATH"
fi

if [[ ! -s "$SCREENSHOT_PATH" ]]; then
  echo "[ERROR] Screenshot capture failed: $SCREENSHOT_PATH"
  exit 5
fi

cp -f "$SCREENSHOT_PATH" "$LATEST_SCREENSHOT"

cat >"$REPORT_PATH" <<EOF
run_id=$RUN_ID
repo_root=$REPO_ROOT
app_path=$APP_PATH
screenshot=$SCREENSHOT_PATH
screenshot_latest=$LATEST_SCREENSHOT
status=ok
capture_mode=$CAPTURE_MODE
timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
EOF

echo "[OK] app_path=$APP_PATH"
echo "[OK] screenshot=$SCREENSHOT_PATH"
echo "[OK] latest_screenshot=$LATEST_SCREENSHOT"
echo "[OK] report=$REPORT_PATH"
