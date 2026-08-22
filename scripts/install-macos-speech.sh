#!/usr/bin/env bash
set -euo pipefail

MODEL_ROOT="${1:?speech model cache path required}"
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P)
BUNDLE_DIR=$(cd "$SCRIPT_DIR/.." && pwd -P)
RUNTIME_DIR="$BUNDLE_DIR/.speech-runtime"
VENV_DIR="$RUNTIME_DIR/venv"
PLIST_PATH="$HOME/Library/LaunchAgents/com.everyapi.edge-speech.plist"
LOG_DIR="$HOME/Library/Logs/EveryAPI"

if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
  echo "native speech requires Apple Silicon macOS" >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "Python 3 is required for the native macOS speech runtime" >&2
  exit 1
fi
if [ "$(python3 -c 'import platform; print(platform.machine())')" != "arm64" ]; then
  echo "an arm64 Python build is required for Apple MPS" >&2
  exit 1
fi
if ! command -v espeak-ng >/dev/null 2>&1; then
  if ! command -v brew >/dev/null 2>&1; then
    echo "Homebrew is required to install the speech runtime phonemiser" >&2
    exit 1
  fi
  brew install espeak-ng
fi

mkdir -p "$RUNTIME_DIR" "$MODEL_ROOT" "$(dirname "$PLIST_PATH")" "$LOG_DIR"
if [ ! -x "$VENV_DIR/bin/python" ]; then
  python3 -m venv "$VENV_DIR"
fi
"$VENV_DIR/bin/pip" install --disable-pip-version-check -r "$BUNDLE_DIR/speech/requirements-macos.txt"
if ! "$VENV_DIR/bin/python" -c 'import en_core_web_sm' >/dev/null 2>&1; then
  "$VENV_DIR/bin/python" -m spacy download en_core_web_sm
fi

TMP_PLIST=$(mktemp "$PLIST_PATH.XXXXXX")
trap 'rm -f "$TMP_PLIST"' EXIT
cp "$SCRIPT_DIR/com.everyapi.edge-speech.plist.in" "$TMP_PLIST"
plutil -replace ProgramArguments.0 -string "$VENV_DIR/bin/fastapi" "$TMP_PLIST"
plutil -replace WorkingDirectory -string "$BUNDLE_DIR/speech" "$TMP_PLIST"
plutil -replace EnvironmentVariables.HF_HOME -string "$MODEL_ROOT" "$TMP_PLIST"
plutil -replace StandardOutPath -string "$LOG_DIR/speech.log" "$TMP_PLIST"
plutil -replace StandardErrorPath -string "$LOG_DIR/speech-error.log" "$TMP_PLIST"
plutil -lint "$TMP_PLIST" >/dev/null
mv "$TMP_PLIST" "$PLIST_PATH"

DOMAIN="gui/$(id -u)"
launchctl bootout "$DOMAIN" "$PLIST_PATH" >/dev/null 2>&1 || true
launchctl bootstrap "$DOMAIN" "$PLIST_PATH"
launchctl kickstart -k "$DOMAIN/com.everyapi.edge-speech"

deadline=$(( $(date +%s) + 1200 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if curl -fsS --connect-timeout 2 --max-time 3 http://127.0.0.1:8189/health >/dev/null; then
    exit 0
  fi
  sleep 1
done
echo "Speech runtime did not become ready; inspect $LOG_DIR/speech-error.log" >&2
exit 1
