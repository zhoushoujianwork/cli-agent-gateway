#!/bin/zsh
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: macos/CLIApp/scripts/build_macos_gui_app.sh [options]

Build a native macOS GUI app (.app) for cli-agent-gateway.

Options:
  --output-dir <dir>   Output directory for the .app (default: ~/Desktop)
  --app-name <name>    App bundle name (default: CLI Agent Gateway GUI)
  --repo-root <dir>    Repository root (default: auto-detected)
  --workdir <dir>      Workdir passed to gateway (default: CODEX_WORKDIR from .env or repo root)
  --icon-svg <path>    SVG file used to build macOS app icon (.icns)
  --no-open            Do not auto-open app after successful build
  --no-kill-old        Do not kill previous running app process before build
  --help               Show this help
EOF
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEFAULT_REPO_ROOT="$(cd "$APP_ROOT/../.." && pwd)"

OUTPUT_DIR="$HOME/Desktop"
APP_NAME="CLI Agent Gateway GUI"
REPO_ROOT="$DEFAULT_REPO_ROOT"
WORKDIR=""
OPEN_AFTER_BUILD=1
KILL_OLD=1
ICON_SVG=""

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
    --repo-root)
      REPO_ROOT="$2"
      shift 2
      ;;
    --workdir)
      WORKDIR="$2"
      shift 2
      ;;
    --icon-svg)
      ICON_SVG="$2"
      shift 2
      ;;
    --no-open)
      OPEN_AFTER_BUILD=0
      shift
      ;;
    --no-kill-old)
      KILL_OLD=0
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
  echo "[ERROR] Invalid repo root (missing src/): $REPO_ROOT" >&2
  exit 2
fi

ENV_FILE="$REPO_ROOT/.env"
if [[ -z "$WORKDIR" && -f "$ENV_FILE" ]]; then
  WORKDIR="$(awk -F= '/^CODEX_WORKDIR=/{print $2; exit}' "$ENV_FILE" | tr -d '"' | tr -d "'")"
fi
if [[ -z "$WORKDIR" ]]; then
  WORKDIR="$REPO_ROOT"
fi
if [[ -z "$ICON_SVG" ]]; then
  ICON_SVG="$APP_ROOT/Assets/AppLogo.svg"
fi

# If this is a git worktree without local .env, try to reuse main-repo .env.
if [[ ! -f "$ENV_FILE" ]]; then
  GIT_COMMON_DIR="$(git -C "$REPO_ROOT" rev-parse --git-common-dir 2>/dev/null || true)"
  if [[ -n "$GIT_COMMON_DIR" ]]; then
    MAIN_REPO_ROOT="$(cd "$GIT_COMMON_DIR/.." && pwd)"
    if [[ -f "$MAIN_REPO_ROOT/.env" ]]; then
      cp -p "$MAIN_REPO_ROOT/.env" "$ENV_FILE"
      echo "[INFO] synced_env_from=$MAIN_REPO_ROOT/.env"
    fi
  fi
fi

LOCK_FILE_RAW=""
STATE_FILE_RAW=""
INTERACTION_LOG_RAW=""
if [[ -f "$ENV_FILE" ]]; then
  LOCK_FILE_RAW="$(awk -F= '/^LOCK_FILE=/{print $2; exit}' "$ENV_FILE" | tr -d '"' | tr -d "'")"
  STATE_FILE_RAW="$(awk -F= '/^STATE_FILE=/{print $2; exit}' "$ENV_FILE" | tr -d '"' | tr -d "'")"
  INTERACTION_LOG_RAW="$(awk -F= '/^INTERACTION_LOG_FILE=/{print $2; exit}' "$ENV_FILE" | tr -d '"' | tr -d "'")"
fi
if [[ -z "$LOCK_FILE_RAW" ]]; then
  LOCK_FILE="$REPO_ROOT/.cli_agent_gateway.lock"
elif [[ "$LOCK_FILE_RAW" = /* ]]; then
  LOCK_FILE="$LOCK_FILE_RAW"
else
  LOCK_FILE="$REPO_ROOT/$LOCK_FILE_RAW"
fi
if [[ -z "$STATE_FILE_RAW" ]]; then
  STATE_FILE="$REPO_ROOT/.agent_gateway_state.json"
elif [[ "$STATE_FILE_RAW" = /* ]]; then
  STATE_FILE="$STATE_FILE_RAW"
else
  STATE_FILE="$REPO_ROOT/$STATE_FILE_RAW"
fi
if [[ -z "$INTERACTION_LOG_RAW" ]]; then
  INTERACTION_LOG_FILE="$REPO_ROOT/.agent_gateway_interactions.jsonl"
elif [[ "$INTERACTION_LOG_RAW" = /* ]]; then
  INTERACTION_LOG_FILE="$INTERACTION_LOG_RAW"
else
  INTERACTION_LOG_FILE="$REPO_ROOT/$INTERACTION_LOG_RAW"
fi

LOG_FILE="$HOME/Library/Logs/cli-agent-gateway/gateway.log"

mkdir -p "$OUTPUT_DIR"
APP_PATH="$OUTPUT_DIR/$APP_NAME.app"
CONTENTS="$APP_PATH/Contents"
MACOS_DIR="$CONTENTS/MacOS"
RESOURCES_DIR="$CONTENTS/Resources"
BIN_PATH="$MACOS_DIR/CLI Agent Gateway GUI"
SRC_FILE="$APP_ROOT/Sources/main.swift"
PLIST_PATH="$CONTENTS/Info.plist"
CONFIG_JSON="$RESOURCES_DIR/gateway_config.json"
EXEC_NAME="CLI Agent Gateway GUI"
ICON_NAME="AppIcon"
ICON_ICNS="$RESOURCES_DIR/$ICON_NAME.icns"
EXEC_PATTERN="/Contents/MacOS/$EXEC_NAME"

if [[ "$KILL_OLD" == "1" ]]; then
  # Best-effort: close previously running GUI app instance before replacing bundle.
  osascript -e 'tell application id "com.cli-agent-gateway.gui" to quit' >/dev/null 2>&1 || true
  osascript -e "tell application \"$APP_NAME\" to quit" >/dev/null 2>&1 || true
  pkill -f "$EXEC_PATTERN" >/dev/null 2>&1 || true
  # Wait up to 5 seconds for existing process to exit.
  for _ in {1..10}; do
    if ! pgrep -f "$EXEC_PATTERN" >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
  done
  if pgrep -f "$EXEC_PATTERN" >/dev/null 2>&1; then
    pkill -9 -f "$EXEC_PATTERN" >/dev/null 2>&1 || true
  fi
  sleep 0.5
fi

rm -rf "$APP_PATH"
mkdir -p "$MACOS_DIR" "$RESOURCES_DIR"

cat >"$PLIST_PATH" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleExecutable</key>
  <string>$EXEC_NAME</string>
  <key>CFBundleIdentifier</key>
  <string>com.cli-agent-gateway.gui</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleIconFile</key>
  <string>$ICON_NAME</string>
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
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
EOF

cat >"$CONFIG_JSON" <<EOF
{
  "repoRoot": "$REPO_ROOT",
  "workdir": "$WORKDIR",
  "lockFile": "$LOCK_FILE",
  "logFile": "$LOG_FILE",
  "stateFile": "$STATE_FILE",
  "interactionLogFile": "$INTERACTION_LOG_FILE"
}
EOF

if [[ -f "$ICON_SVG" ]]; then
  ICON_TMP="$(mktemp -d)"
  ICONSET="$ICON_TMP/$ICON_NAME.iconset"
  mkdir -p "$ICONSET"

  render_png() {
    local size="$1"
    local output="$2"
    if command -v rsvg-convert >/dev/null 2>&1; then
      rsvg-convert -w "$size" -h "$size" "$ICON_SVG" -o "$output"
      return 0
    fi
    if command -v qlmanage >/dev/null 2>&1; then
      local ql_tmp="$ICON_TMP/ql"
      mkdir -p "$ql_tmp"
      qlmanage -t -s "$size" -o "$ql_tmp" "$ICON_SVG" >/dev/null 2>&1 || return 1
      local rendered="$ql_tmp/$(basename "$ICON_SVG").png"
      [[ -f "$rendered" ]] || return 1
      mv "$rendered" "$output"
      return 0
    fi
    return 1
  }

  MASTER="$ICON_TMP/master_1024.png"
  if render_png 1024 "$MASTER"; then
    sips -z 16 16 "$MASTER" --out "$ICONSET/icon_16x16.png" >/dev/null
    sips -z 32 32 "$MASTER" --out "$ICONSET/icon_16x16@2x.png" >/dev/null
    sips -z 32 32 "$MASTER" --out "$ICONSET/icon_32x32.png" >/dev/null
    sips -z 64 64 "$MASTER" --out "$ICONSET/icon_32x32@2x.png" >/dev/null
    sips -z 128 128 "$MASTER" --out "$ICONSET/icon_128x128.png" >/dev/null
    sips -z 256 256 "$MASTER" --out "$ICONSET/icon_128x128@2x.png" >/dev/null
    sips -z 256 256 "$MASTER" --out "$ICONSET/icon_256x256.png" >/dev/null
    sips -z 512 512 "$MASTER" --out "$ICONSET/icon_256x256@2x.png" >/dev/null
    sips -z 512 512 "$MASTER" --out "$ICONSET/icon_512x512.png" >/dev/null
    cp "$MASTER" "$ICONSET/icon_512x512@2x.png"
    iconutil -c icns "$ICONSET" -o "$ICON_ICNS"
    echo "[INFO] icon_svg=$ICON_SVG"
    echo "[INFO] icon_icns=$ICON_ICNS"
  else
    echo "[WARN] Failed to render SVG icon, building app without custom icon: $ICON_SVG"
  fi

  rm -rf "$ICON_TMP"
else
  echo "[WARN] SVG icon not found, building app without custom icon: $ICON_SVG"
fi

swiftc \
  -O \
  -parse-as-library \
  -framework SwiftUI \
  -framework AppKit \
  -framework Foundation \
  "$SRC_FILE" \
  -o "$BIN_PATH"

chmod +x "$BIN_PATH"

echo "[OK] GUI app built: $APP_PATH"
echo "[INFO] repo_root=$REPO_ROOT"
echo "[INFO] workdir=$WORKDIR"
echo "[INFO] lock_file=$LOCK_FILE"
echo "[INFO] log_file=$LOG_FILE"
echo "[INFO] state_file=$STATE_FILE"
echo "[INFO] interaction_log_file=$INTERACTION_LOG_FILE"
if [[ "$OPEN_AFTER_BUILD" == "1" ]]; then
  open -n "$APP_PATH"
  echo "[INFO] opened_app=1"
fi
