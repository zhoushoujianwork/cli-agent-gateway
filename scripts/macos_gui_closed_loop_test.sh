#!/bin/zsh
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/macos_gui_closed_loop_test.sh [options]

Closed-loop macOS GUI test:
1) Build app (optional)
2) Launch app
3) Run Start/Stop/Restart GUI checks
4) Capture screenshots for each state
5) Emit pass/fail report

Options:
  --repo-root <dir>    Repository root (default: auto-detected)
  --app-name <name>    App bundle display name (default: CLI Agent Gateway GUI)
  --temp-root <dir>    Artifact root (default: /tmp/cli-agent-gateway-macos-gui-loop)
  --skip-build         Skip app build and use existing .app in temp root
  --help               Show help
EOF
}

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "[ERROR] macOS is required."
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
if [[ ! -d "$REPO_ROOT/src" ]]; then
  echo "[ERROR] Invalid repo root (missing src/): $REPO_ROOT"
  exit 2
fi

BUILD_SCRIPT="$REPO_ROOT/macos/CLIApp/scripts/build_macos_gui_app.sh"
APP_DIR="$TEMP_ROOT/app"
APP_PATH="$APP_DIR/$APP_NAME.app"
EXEC_PATTERN="/Contents/MacOS/CLI Agent Gateway GUI"
RUN_ID="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="$TEMP_ROOT/runs/$RUN_ID"
REPORT_PATH="$RUN_DIR/report.txt"
SUMMARY_PATH="$RUN_DIR/summary.md"
SCREEN_DIR="$RUN_DIR/screenshots"
LATEST_DIR="$TEMP_ROOT/latest"

mkdir -p "$APP_DIR" "$RUN_DIR" "$SCREEN_DIR" "$LATEST_DIR"

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

shell_status() {
  cd "$REPO_ROOT/src"
  go run ./cmd/gateway-cli status 2>/dev/null || true
}

gateway_running() {
  local out
  out="$(shell_status)"
  [[ "$out" == RUNNING* ]]
}

wait_gateway_state() {
  local want="$1"
  local timeout_sec="${2:-20}"
  local deadline=$((SECONDS + timeout_sec))
  while (( SECONDS < deadline )); do
    if [[ "$want" == "running" ]] && gateway_running; then
      return 0
    fi
    if [[ "$want" == "stopped" ]] && ! gateway_running; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

activate_app() {
  osascript -e "tell application \"$APP_NAME\" to activate" >/dev/null 2>&1 || true
}

ensure_app_window() {
  local rows
  activate_app
  for _ in {1..10}; do
    rows="$(topbar_button_rows)"
    if [[ -n "$rows" ]]; then
      return 0
    fi
    sleep 0.4
  done

  open "$APP_PATH"
  activate_app
  for _ in {1..15}; do
    rows="$(topbar_button_rows)"
    if [[ -n "$rows" ]]; then
      return 0
    fi
    sleep 0.4
  done
  return 1
}

topbar_button_rows() {
  local raw
  raw="$(
    osascript <<APPLESCRIPT 2>/dev/null || true
tell application "System Events"
  if not (exists process "$APP_NAME") then
    return ""
  end if
  tell process "$APP_NAME"
    if (count of windows) is 0 then
      return ""
    end if
    tell front window
      set elems to entire contents
      set out to {}
      repeat with e in elems
        try
          if role of e is "AXButton" then
            set p to position of e
            set s to size of e
            set en to enabled of e
            set ds to ""
            try
              set ds to description of e as text
            end try
            set end of out to ("pos=" & (item 1 of p) & ":" & (item 2 of p) & ",size=" & (item 1 of s) & "x" & (item 2 of s) & ",enabled=" & en & ",desc=" & ds)
          end if
        end try
      end repeat
      return out as text
    end tell
  end tell
end tell
APPLESCRIPT
  )"
  printf '%s' "$raw" \
    | perl -ne 'while(/pos=(-?\d+):(-?\d+),size=(\d+)x(\d+),enabled=(true|false),desc=button/g){print "$1,$2,$3,$4,$5\n"}' \
    | awk -F, '$2>0 && $3>=50 && $3<=90 && $4>=20 && $4<=30' \
    | sort -t, -k2,2n -k1,1n
}

click_topbar_button() {
  local order="$1"
  local rows miny row x y w h enabled cx cy
  ensure_app_window >/dev/null 2>&1 || true
  rows="$(topbar_button_rows)"
  if [[ -z "$rows" ]]; then
    echo "NO_WINDOW"
    return 0
  fi

  miny="$(printf '%s\n' "$rows" | awk -F, 'NR==1 || $2<min {min=$2} END {if (NR>0) print min}')"
  row="$(
    printf '%s\n' "$rows" \
      | awk -F, -v miny="$miny" '$2 <= (miny + 10)' \
      | sort -t, -k1,1n \
      | sed -n "${order}p"
  )"
  if [[ -z "$row" ]]; then
    echo "MISSING"
    return 0
  fi

  IFS=',' read -r x y w h enabled <<<"$row"
  if [[ "$enabled" != "true" ]]; then
    echo "DISABLED"
    return 0
  fi

  cx=$((x + w / 2))
  cy=$((y + h / 2))
  if osascript -e "tell application \"System Events\" to click at {$cx, $cy}" >/dev/null 2>&1; then
    echo "CLICKED"
  else
    echo "CLICK_FAILED"
  fi
}

capture_window() {
  local output="$1"
  local region
  region="$(
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
  if [[ -n "$region" && "$region" == *,*,*,* ]]; then
    screencapture -x -R"$region" "$output"
    echo "window_region"
    return 0
  fi
  screencapture -x "$output"
  echo "full_screen_fallback"
}

CHECK_NAMES=()
CHECK_RESULTS=()
CHECK_DETAILS=()

record_check() {
  CHECK_NAMES+=("$1")
  CHECK_RESULTS+=("$2")
  CHECK_DETAILS+=("$3")
}

restart_gui_app
open "$APP_PATH"
activate_app
sleep 1

if ! wait_gateway_state "stopped" 8; then
  # App can auto-start from previous state. Try to force stop if possible.
  stop_click="$(click_topbar_button 4)"
  sleep 1
  wait_gateway_state "stopped" 10 || true
  record_check "initial_stopped" "warn" "initial state not stopped, stop_click=$stop_click"
else
  record_check "initial_stopped" "pass" "gateway is stopped before start test"
fi

capture_mode_idle="$(capture_window "$SCREEN_DIR/idle.png")"
record_check "capture_idle" "pass" "mode=$capture_mode_idle"

start_ok=0
start_click="$(click_topbar_button 3)"
if [[ "$start_click" == "CLICKED" ]] && wait_gateway_state "running" 20; then
  start_ok=1
  record_check "start_action" "pass" "start_click=$start_click and gateway running"
else
  record_check "start_action" "fail" "start_click=$start_click status=$(shell_status)"
fi

capture_mode_running="$(capture_window "$SCREEN_DIR/running.png")"
record_check "capture_running" "pass" "mode=$capture_mode_running"

if [[ "$start_ok" == "1" ]]; then
  restart_click="$(click_topbar_button 5)"
  if [[ "$restart_click" == "CLICKED" ]] && wait_gateway_state "running" 20; then
    record_check "restart_action" "pass" "restart_click=$restart_click and gateway running"
  else
    record_check "restart_action" "fail" "restart_click=$restart_click status=$(shell_status)"
  fi
else
  record_check "restart_action" "skip" "skipped because start_action failed"
fi

capture_mode_restart="$(capture_window "$SCREEN_DIR/restarted.png")"
record_check "capture_restarted" "pass" "mode=$capture_mode_restart"

if gateway_running; then
  stop_click="$(click_topbar_button 4)"
else
  stop_click="ALREADY_STOPPED"
fi
if [[ "$stop_click" == "ALREADY_STOPPED" ]]; then
  record_check "stop_action" "pass" "gateway already stopped"
elif [[ "$stop_click" == "CLICKED" ]] && wait_gateway_state "stopped" 20; then
  record_check "stop_action" "pass" "stop_click=$stop_click and gateway stopped"
else
  record_check "stop_action" "fail" "stop_click=$stop_click status=$(shell_status)"
fi

capture_mode_stopped="$(capture_window "$SCREEN_DIR/stopped.png")"
record_check "capture_stopped" "pass" "mode=$capture_mode_stopped"

cp -f "$SCREEN_DIR/stopped.png" "$LATEST_DIR/macos-gui-latest.png"
cp -f "$SCREEN_DIR/idle.png" "$LATEST_DIR/macos-gui-idle.png"
cp -f "$SCREEN_DIR/running.png" "$LATEST_DIR/macos-gui-running.png"
cp -f "$SCREEN_DIR/restarted.png" "$LATEST_DIR/macos-gui-restarted.png"
cp -f "$SCREEN_DIR/stopped.png" "$LATEST_DIR/macos-gui-stopped.png"

{
  echo "run_id=$RUN_ID"
  echo "repo_root=$REPO_ROOT"
  echo "app_path=$APP_PATH"
  echo "status_snapshot=$(shell_status | tr '\n' ' ' | sed 's/[[:space:]]\\+/ /g')"
  echo "screenshot_idle=$SCREEN_DIR/idle.png"
  echo "screenshot_running=$SCREEN_DIR/running.png"
  echo "screenshot_restarted=$SCREEN_DIR/restarted.png"
  echo "screenshot_stopped=$SCREEN_DIR/stopped.png"
  echo "latest_dir=$LATEST_DIR"
  echo "timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  for ((i=1; i<=${#CHECK_NAMES[@]}; i++)); do
    echo "check_${CHECK_NAMES[$i]}=${CHECK_RESULTS[$i]} :: ${CHECK_DETAILS[$i]}"
  done
} >"$REPORT_PATH"

{
  echo "# macOS GUI Closed Loop Summary"
  echo
  echo "- run_id: \`$RUN_ID\`"
  echo "- app: \`$APP_PATH\`"
  echo "- screenshots: \`$SCREEN_DIR\`"
  echo "- latest: \`$LATEST_DIR\`"
  echo
  echo "| check | result | detail |"
  echo "|---|---|---|"
  for ((i=1; i<=${#CHECK_NAMES[@]}; i++)); do
    echo "| ${CHECK_NAMES[$i]} | ${CHECK_RESULTS[$i]} | ${CHECK_DETAILS[$i]} |"
  done
} >"$SUMMARY_PATH"

echo "[OK] report=$REPORT_PATH"
echo "[OK] summary=$SUMMARY_PATH"
echo "[OK] screenshots=$SCREEN_DIR"
echo "[OK] latest_dir=$LATEST_DIR"
