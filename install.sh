#!/usr/bin/env bash
# EveryAPI edge agent — one-shot installer for the BYO-GPU bundle.
#
# Usage:
#
#   curl -fsSL https://everyapi.ai/edge/install.sh | bash -s -- \
#     --node-id 5 --token edgert_... [--gateway https://api.everyapi.ai] [--name home-rtx4090] [--gpu nvidia|rocm|macos]
#
# Or interactive (prompts for the values it doesn't get on the CLI):
#
#   curl -fsSL https://everyapi.ai/edge/install.sh | bash
#
# What it does:
#
#   1. Detects the GPU (nvidia-smi / rocminfo / Darwin host) and picks
#      the matching docker-compose variant.
#   2. Pulls the latest bundle source from the public mirror (or the
#      monorepo fallback). Does NOT modify anything outside ./everyapi-edge/.
#   3. Writes .env with the supplied node id + token + GPU metadata.
#   4. docker compose pull && docker compose up -d
#   5. Tails the agent logs for 15s so the supplier sees the "connected"
#      line without having to look it up.
#
# The script is idempotent: re-running with the same args is a no-op
# (existing directory + .env are preserved unless --force).

set -euo pipefail

# Wrap the entire installer in a function and invoke it as the very
# last line. `curl … | bash` reads bytes through a pipe; if the
# connection is cut mid-stream bash may execute whatever lines it
# already received. With the body in a function, a truncated download
# defines a partial function and exits cleanly when bash hits EOF
# without ever calling `main` — no partial side effects on disk,
# no half-written .env, no half-pulled images.
main() {

# ----- Defaults --------------------------------------------------------------

GATEWAY="https://api.everyapi.ai"
NODE_ID=""
TOKEN=""
NODE_NAME=""
GPU=""
FORCE=0
INSTALL_DIR="./everyapi-edge"
BUNDLE_SOURCE="https://github.com/everyapi-ai/everyapi-edge"

# ----- Pretty print ----------------------------------------------------------

if [ -t 1 ]; then
  BOLD=$(printf '\033[1m'); GREEN=$(printf '\033[32m')
  YELLOW=$(printf '\033[33m'); RED=$(printf '\033[31m')
  RESET=$(printf '\033[0m')
else
  BOLD=""; GREEN=""; YELLOW=""; RED=""; RESET=""
fi
info() { printf '%b▶%b %s\n' "$BOLD" "$RESET" "$*" >&2; }
ok()   { printf '%b✓%b %s\n' "$GREEN" "$RESET" "$*" >&2; }
warn() { printf '%b!%b %s\n' "$YELLOW" "$RESET" "$*" >&2; }
err()  { printf '%b✗%b %s\n' "$RED" "$RESET" "$*" >&2; }

# ----- Args ------------------------------------------------------------------

while [ $# -gt 0 ]; do
  case "$1" in
    --node-id)  NODE_ID="$2"; shift 2 ;;
    --token)    TOKEN="$2"; shift 2 ;;
    --gateway)  GATEWAY="$2"; shift 2 ;;
    --name)     NODE_NAME="$2"; shift 2 ;;
    --gpu)      GPU="$2"; shift 2 ;;
    --dir)      INSTALL_DIR="$2"; shift 2 ;;
    --force)    FORCE=1; shift ;;
    -h|--help)
      sed -n '2,/^set -e/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      err "unknown arg: $1"
      exit 1
      ;;
  esac
done

# ----- Prerequisites ---------------------------------------------------------

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "$1 is required but not installed"
    return 1
  fi
}

info "checking prerequisites…"
require docker || exit 1
if ! docker compose version >/dev/null 2>&1; then
  err "docker compose (v2) is required — install Docker Desktop or docker-compose-plugin"
  exit 1
fi
require curl || exit 1
ok "docker $(docker --version | awk '{print $3}' | tr -d ,) + compose found"

# ----- GPU detection ---------------------------------------------------------

if [ -z "$GPU" ]; then
  info "auto-detecting GPU…"
  if [ "$(uname -s)" = "Darwin" ]; then
    GPU="macos"
  elif command -v nvidia-smi >/dev/null 2>&1; then
    GPU="nvidia"
  elif command -v rocminfo >/dev/null 2>&1; then
    GPU="rocm"
  else
    warn "no GPU detected — falling back to nvidia config (will run CPU-only)"
    GPU="nvidia"
  fi
fi
case "$GPU" in
  nvidia) COMPOSE_FILE="docker-compose.yml" ;;
  rocm)   COMPOSE_FILE="docker-compose.rocm.yml" ;;
  macos)
    COMPOSE_FILE="docker-compose.macos.yml"
    if ! command -v ollama >/dev/null 2>&1; then
      warn "ollama is not installed on the host"
      echo "  Install it before running the agent (or it will have nothing to forward to):"
      echo "    brew install ollama && brew services start ollama"
    fi
    ;;
  *)
    err "unsupported --gpu value: $GPU (expected nvidia / rocm / macos)"
    exit 1
    ;;
esac
ok "GPU profile: $GPU ($COMPOSE_FILE)"

# ----- Prompts (only if not given on CLI) ------------------------------------

if [ -z "$NODE_ID" ]; then
  printf '%bEVERYAPI_NODE_ID%b (from the dashboard /seller/edge → New node): ' "$BOLD" "$RESET"
  read -r NODE_ID
fi
if [ -z "$TOKEN" ]; then
  printf '%bEVERYAPI_REGISTRATION_TOKEN%b (one-time, from the same dashboard step): ' "$BOLD" "$RESET"
  read -r TOKEN
fi
if [ -z "$NODE_NAME" ]; then
  NODE_NAME="$(hostname 2>/dev/null || echo node-$NODE_ID)"
fi

case "$NODE_ID" in
  ''|*[!0-9]*) err "node id must be a positive integer (got: $NODE_ID)"; exit 1 ;;
esac
case "$TOKEN" in
  edgert_*) ;;
  *) err "token must start with 'edgert_' (got: $TOKEN)"; exit 1 ;;
esac

# ----- Materialise bundle ----------------------------------------------------

if [ -d "$INSTALL_DIR" ] && [ "$FORCE" -ne 1 ]; then
  info "bundle dir $INSTALL_DIR already exists — pulling latest"
  cd "$INSTALL_DIR"
  git pull --ff-only || warn "git pull failed; continuing with on-disk version"
else
  info "cloning bundle to $INSTALL_DIR"
  rm -rf "$INSTALL_DIR"
  git clone --depth 1 "$BUNDLE_SOURCE" "$INSTALL_DIR" || {
    err "failed to clone $BUNDLE_SOURCE"
    err "if the public mirror isn't live yet, set BUNDLE_SOURCE to your own fork URL"
    exit 1
  }
  cd "$INSTALL_DIR"
fi

# ----- Write .env (idempotent + atomic) --------------------------------------

# Write to a temp file then mv into place. If something interrupts
# the write (curl cut, disk full, signal), the existing .env is
# untouched — docker compose either reads the previous good values
# or fails to start (no half-populated .env that leaves the agent
# unable to auth but containers nonetheless up and looping).
info "writing .env"
TMP_ENV="$(mktemp .env.XXXXXX)"
cat > "$TMP_ENV" <<EOF
EVERYAPI_GATEWAY=$GATEWAY
EVERYAPI_NODE_ID=$NODE_ID
EVERYAPI_REGISTRATION_TOKEN=$TOKEN
EVERYAPI_NODE_NAME=$NODE_NAME
EOF
chmod 600 "$TMP_ENV"
mv "$TMP_ENV" .env
ok "wrote .env (mode 0600)"

# ----- Bring up --------------------------------------------------------------

info "pulling images…"
docker compose -f "$COMPOSE_FILE" pull

info "starting bundle…"
docker compose -f "$COMPOSE_FILE" up -d

# ----- Smoke wait ------------------------------------------------------------

info "waiting up to 15s for the agent to connect…"
DEADLINE=$(( $(date +%s) + 15 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  if docker compose -f "$COMPOSE_FILE" logs --tail=200 agent 2>/dev/null \
     | grep -q "identity loaded"; then
    ok "agent started and loaded identity"
    break
  fi
  sleep 1
done

ok "done"
echo
echo "Next steps:"
echo "  • Open the EveryAPI dashboard → Seller → Edge nodes"
echo "    The row should flip to Online within 30s of agent startup."
echo "  • Pull the models you want to serve (inside this dir):"
echo "      docker compose -f $COMPOSE_FILE exec ollama ollama pull llama3.1:8b"
echo "  • Logs:"
echo "      docker compose -f $COMPOSE_FILE logs -f agent"

} # end main

main "$@"
