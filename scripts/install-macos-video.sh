#!/usr/bin/env bash
set -euo pipefail

MODEL_ROOT="${1:?video model cache path required}"
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P)
BUNDLE_DIR=$(cd "$SCRIPT_DIR/.." && pwd -P)
RUNTIME_DIR="$BUNDLE_DIR/.video-runtime"
VENV_DIR="$RUNTIME_DIR/venv"
PLIST_PATH="$HOME/Library/LaunchAgents/com.everyapi.edge-video.plist"
LOG_DIR="$HOME/Library/Logs/EveryAPI"

if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
  echo "native video requires Apple Silicon macOS" >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "Python 3 is required for the native macOS video runtime" >&2
  exit 1
fi
if ! command -v ffmpeg >/dev/null 2>&1; then
  if ! command -v brew >/dev/null 2>&1; then
    echo "Homebrew is required to install ffmpeg" >&2
    exit 1
  fi
  brew install ffmpeg
fi

mkdir -p "$RUNTIME_DIR" "$MODEL_ROOT" "$(dirname "$PLIST_PATH")" "$LOG_DIR"
if [ ! -x "$VENV_DIR/bin/python" ]; then
  python3 -m venv "$VENV_DIR"
fi
"$VENV_DIR/bin/pip" install --disable-pip-version-check -r "$BUNDLE_DIR/video/requirements-macos.txt"

TMP_PLIST=$(mktemp "$PLIST_PATH.XXXXXX")
trap 'rm -f "$TMP_PLIST"' EXIT
cp "$SCRIPT_DIR/com.everyapi.edge-video.plist.in" "$TMP_PLIST"
plutil -replace ProgramArguments.0 -string "$VENV_DIR/bin/fastapi" "$TMP_PLIST"
plutil -replace WorkingDirectory -string "$BUNDLE_DIR/video" "$TMP_PLIST"
plutil -replace EnvironmentVariables.HF_HOME -string "$MODEL_ROOT" "$TMP_PLIST"
plutil -replace EnvironmentVariables.EVERYAPI_VIDEO_DATA_PATH -string "$MODEL_ROOT/jobs" "$TMP_PLIST"
plutil -replace StandardOutPath -string "$LOG_DIR/video.log" "$TMP_PLIST"
plutil -replace StandardErrorPath -string "$LOG_DIR/video-error.log" "$TMP_PLIST"
plutil -lint "$TMP_PLIST" >/dev/null
mv "$TMP_PLIST" "$PLIST_PATH"

DOMAIN="gui/$(id -u)"
launchctl bootout "$DOMAIN" "$PLIST_PATH" >/dev/null 2>&1 || true
launchctl bootstrap "$DOMAIN" "$PLIST_PATH"
launchctl kickstart -k "$DOMAIN/com.everyapi.edge-video"

deadline=$(( $(date +%s) + 1200 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if curl -fsS --connect-timeout 2 --max-time 3 http://127.0.0.1:8191/health >/dev/null; then
    exit 0
  fi
  sleep 1
done
echo "Video runtime did not become ready; inspect $LOG_DIR/video-error.log" >&2
exit 1
