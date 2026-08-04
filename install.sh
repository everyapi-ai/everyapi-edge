#!/usr/bin/env bash
# EveryAPI edge agent — one-shot installer for the BYO-GPU bundle.
#
# Usage:
#
#   curl -fsSL https://dl.everyapi.ai/edge/install.sh | bash -s -- \
#     --node-id 5 --token edgert_... [--gateway https://api.everyapi.ai] [--name home-rtx4090] [--gpu nvidia|rocm|macos] [--model qwen2.5:7b]
#
# Or interactive (prompts for the values it doesn't get on the CLI):
#
#   curl -fsSL https://dl.everyapi.ai/edge/install.sh | bash
#
# The same command upgrades an installer-managed node. Its verified checkout,
# node metadata, and persisted Ed25519 identity are reused; a consumed one-time
# registration token is neither needed nor requested.
#
# What it does:
#
#   1. Detects the GPU (nvidia-smi / rocminfo / Darwin host) and picks
#      the matching docker-compose variant.
#   2. Pulls the latest bundle source from the public mirror (or the
#      monorepo fallback) into $HOME/everyapi-edge (override with --dir).
#      Does NOT modify anything outside that directory, and refuses to
#      install into a git working tree so the agent's credentials never
#      land in a source checkout.
#   3. Writes .env with the new or recovered node metadata plus host-detected
#      GPU metadata and the model storage location used by the Control Room.
#   4. Detects available accelerator memory, pulls a conservatively-sized
#      Ollama model, and verifies a real local inference request.
#   5. Starts the agent and waits for its gateway Welcome before declaring
#      success. The one-time registration token is then removed from .env.
#
# The script is idempotent: an existing verified EveryAPI Edge checkout is
# fast-forwarded and its .env is atomically refreshed. It never recursively
# deletes a caller-provided path.

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
GATEWAY_EXPLICIT=0
NODE_ID=""
TOKEN=""
NODE_NAME=""
GPU=""
MODEL=""
MODEL_EXPLICIT=0
FORCE=0
# A node is a long-lived service, so it belongs at a stable path rather than
# wherever the operator happened to be standing. The old "./everyapi-edge"
# default planted a running agent — .env with node credentials, data/ with the
# Ed25519 identity — into the current directory, which for anyone running the
# documented `curl … | bash` one-liner is unpredictable. Falls back to the old
# relative default only when HOME is unset (containers, some CI shells).
INSTALL_DIR="${HOME:+$HOME/everyapi-edge}"
INSTALL_DIR="${INSTALL_DIR:-./everyapi-edge}"
BUNDLE_SOURCE="https://github.com/everyapi-ai/everyapi-edge"
GPU_MODEL=""
VRAM_GB=""
HOST_PLATFORM=""
COUNTRY=""
WORKLOADS=""
CONSOLE_PORT=""
EXISTING_INSTALL=0
UPGRADE_MODE=0

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
    --gateway)  GATEWAY="$2"; GATEWAY_EXPLICIT=1; shift 2 ;;
    --name)     NODE_NAME="$2"; shift 2 ;;
    --gpu)      GPU="$2"; shift 2 ;;
    --model)    MODEL="$2"; MODEL_EXPLICIT=1; shift 2 ;;
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

# ----- Validate untrusted CLI values ----------------------------------------

# Values below are written to a Docker Compose .env file. Reject line breaks
# before any filesystem or Docker operation so an argument cannot add a second
# dotenv key (for example COMPOSE_FILE or a substituted image tag).
validate_no_newlines() {
  local _name="$1" _value="$2"
  case "$_value" in
    *$'\n'*|*$'\r'*) err "$_name must not contain line breaks"; exit 1 ;;
  esac
}
validate_no_newlines "gateway" "$GATEWAY"
validate_no_newlines "node name" "$NODE_NAME"
validate_no_newlines "registration token" "$TOKEN"
validate_no_newlines "install directory" "$INSTALL_DIR"
validate_no_newlines "model" "$MODEL"

# Refuse broad or ambiguous targets. In particular, the old --force path ran
# rm -rf directly on --dir; values such as /, HOME, ., or a symlinked spelling
# of one of those could destroy unrelated data. Resolve the parent/target now
# and later accept an existing directory only when it is the expected checkout.
case "$INSTALL_DIR" in
  ""|/|.|..|-) err "refusing unsafe install directory: $INSTALL_DIR"; exit 1 ;;
  -*) err "refusing unsafe install directory beginning with '-': $INSTALL_DIR"; exit 1 ;;
esac
INSTALL_PARENT=$(dirname -- "$INSTALL_DIR")
INSTALL_LEAF=$(basename -- "$INSTALL_DIR")
case "$INSTALL_LEAF" in
  ""|.|..) err "refusing unsafe install directory: $INSTALL_DIR"; exit 1 ;;
esac
if [ ! -d "$INSTALL_PARENT" ]; then
  err "install directory parent does not exist: $INSTALL_PARENT"
  exit 1
fi
CANON_PARENT=$(cd "$INSTALL_PARENT" && pwd -P)
if [ -d "$INSTALL_DIR" ]; then
  CANON_TARGET=$(cd "$INSTALL_DIR" && pwd -P)
else
  CANON_TARGET="$CANON_PARENT/$INSTALL_LEAF"
fi
CURRENT_DIR=$(pwd -P)
case "$CANON_TARGET" in
  /|"${HOME:-}"|"$CURRENT_DIR")
    err "refusing unsafe install directory: $CANON_TARGET"
    exit 1
    ;;
esac

# Refuse to plant a running service inside somebody's source checkout. The
# checks above only reject the cwd ITSELF, so `cd ~/src/some-repo && curl … |
# bash` sailed through and left an agent — .env with node credentials, data/
# with the Ed25519 identity — sitting in the repo's working tree, showing up in
# git status and one careless `git add` away from being committed.
#
# A HOME that is itself a git worktree is the dotfiles pattern, not a source
# checkout, so it stays allowed — otherwise the new default would refuse to run
# for everyone who manages their home directory that way.
if command -v git >/dev/null 2>&1; then
  GIT_WORKTREE_ROOT=$(cd "$CANON_PARENT" && git rev-parse --show-toplevel 2>/dev/null || true)
  # Compare canonicalised paths on both sides: git reports a resolved toplevel
  # while $HOME may still carry a symlinked spelling (/var vs /private/var on
  # macOS), and a mismatch there would fire the guard on a dotfiles HOME.
  CANON_HOME=""
  if [ -n "${HOME:-}" ] && [ -d "$HOME" ]; then
    CANON_HOME=$(cd "$HOME" && pwd -P)
  fi
  if [ -n "$GIT_WORKTREE_ROOT" ] && [ "$GIT_WORKTREE_ROOT" != "$CANON_HOME" ]; then
    err "refusing to install inside the git working tree at $GIT_WORKTREE_ROOT"
    err "the agent stores credentials (.env) and its identity (data/) — keep them out of a source checkout"
    err "pass --dir with a path outside that repository, for example --dir \"\$HOME/everyapi-edge\""
    exit 1
  fi
fi

# ----- Recover an existing node before prompting ----------------------------

# Never source a persisted dotenv file: it is data, not shell code. Read only
# the identity fields that this installer owns, and only after the checkout's
# origin has been verified as the official Edge bundle.
read_existing_env_value() {
  local key="$1" line
  [ -f "$INSTALL_DIR/.env" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      "$key="*) printf '%s' "${line#*=}"; return 0 ;;
    esac
  done <"$INSTALL_DIR/.env"
}

if [ -d "$INSTALL_DIR" ]; then
  if [ ! -d "$INSTALL_DIR/.git" ]; then
    err "refusing existing non-EveryAPI directory: $INSTALL_DIR"
    exit 1
  fi
  EXISTING_REMOTE=$(git -C "$INSTALL_DIR" remote get-url origin 2>/dev/null || true)
  case "$EXISTING_REMOTE" in
    "$BUNDLE_SOURCE"|"$BUNDLE_SOURCE.git") ;;
    *)
      err "refusing existing non-EveryAPI directory: $INSTALL_DIR"
      err "unexpected git origin: ${EXISTING_REMOTE:-<none>}"
      exit 1
      ;;
  esac
  EXISTING_INSTALL=1
  if [ -z "$NODE_ID" ]; then
    NODE_ID=$(read_existing_env_value EVERYAPI_NODE_ID)
  fi
  if [ -z "$NODE_NAME" ]; then
    NODE_NAME=$(read_existing_env_value EVERYAPI_NODE_NAME)
  fi
  if [ "$GATEWAY_EXPLICIT" -eq 0 ]; then
    EXISTING_GATEWAY=$(read_existing_env_value EVERYAPI_GATEWAY)
    if [ -n "$EXISTING_GATEWAY" ]; then
      GATEWAY="$EXISTING_GATEWAY"
    fi
  fi
  if [ -z "$TOKEN" ]; then
    TOKEN=$(read_existing_env_value EVERYAPI_REGISTRATION_TOKEN)
  fi
  COUNTRY=$(read_existing_env_value EVERYAPI_COUNTRY)
  WORKLOADS=$(read_existing_env_value EVERYAPI_WORKLOADS)
  CONSOLE_PORT=$(read_existing_env_value EVERYAPI_CONSOLE_PORT)
  if [ -s "$INSTALL_DIR/data/agent/identity.json" ]; then
    UPGRADE_MODE=1
    # A persisted identity is authoritative for reconnects. Older installs or
    # interrupted cleanup may leave a now-consumed token in .env; forwarding
    # it would incorrectly force the agent back through first registration.
    TOKEN=""
    info "found the existing node identity — no new registration token is required"
  fi
fi

# Revalidate the recovered allowlisted values before using or rewriting them.
COUNTRY=$(printf '%s' "$COUNTRY" | tr '[:lower:]' '[:upper:]')
WORKLOADS=$(printf '%s' "$WORKLOADS" | tr -d '[:space:]')
validate_no_newlines "gateway" "$GATEWAY"
validate_no_newlines "node name" "$NODE_NAME"
validate_no_newlines "registration token" "$TOKEN"
validate_no_newlines "country" "$COUNTRY"
validate_no_newlines "workloads" "$WORKLOADS"
validate_no_newlines "console port" "$CONSOLE_PORT"
if [ -n "$COUNTRY" ] && [[ ! "$COUNTRY" =~ ^[A-Z]{2}$ ]]; then
  err "country must be an uppercase ISO 3166-1 alpha-2 code"
  exit 1
fi
if [ -n "$WORKLOADS" ] && [[ ! "$WORKLOADS" =~ ^(chat|coding|image|video|audio|render|embedding)(,(chat|coding|image|video|audio|render|embedding))*$ ]]; then
  err "workloads contains an unsupported value"
  exit 1
fi
if [ -n "$CONSOLE_PORT" ]; then
  if [[ ! "$CONSOLE_PORT" =~ ^[0-9]+$ ]] || [ "$CONSOLE_PORT" -lt 1 ] || [ "$CONSOLE_PORT" -gt 65535 ]; then
    err "console port must be between 1 and 65535"
    exit 1
  fi
fi

# ----- Prerequisites ---------------------------------------------------------

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "$1 is required but not installed"
    return 1
  fi
}

ollama_api_ready() {
  curl -fsS --connect-timeout 3 --max-time 5 http://localhost:11434/api/tags >/dev/null 2>&1
}

wait_for_macos_ollama() {
  local deadline=$(( $(date +%s) + 30 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if ollama_api_ready; then
      return 0
    fi
    sleep 1
  done
  return 1
}

ensure_macos_ollama() {
  local model_root="$HOME/.everyapi/edge"
  mkdir -p "$model_root"
  if ! command -v ollama >/dev/null 2>&1; then
    if ! command -v brew >/dev/null 2>&1; then
      err "Ollama is required on macOS and Homebrew is not installed"
      err "Install Ollama from https://ollama.com/download, then run this command again"
      exit 1
    fi
    info "installing Ollama for native Apple Silicon inference…"
    brew install ollama
  fi

  if command -v brew >/dev/null 2>&1 && brew list --formula ollama >/dev/null 2>&1; then
    # Do this before checking readiness. Otherwise an existing service keeps
    # its old storage root and the bundle silently splits the model library.
    launchctl setenv OLLAMA_MODELS "$model_root" || true
    launchctl setenv OLLAMA_MAX_LOADED_MODELS "${OLLAMA_MAX_LOADED_MODELS:-1}" || true
    launchctl setenv OLLAMA_KEEP_ALIVE "${OLLAMA_KEEP_ALIVE:-5m}" || true
    info "starting local inference…"
    # A running Homebrew service does not inherit launchctl variables until
    # it is restarted. The one-model cap is the hard safety backstop for
    # independently arriving marketplace requests.
    brew services restart ollama >/dev/null 2>&1 || true
  elif ! ollama_api_ready; then
    info "starting local inference…"
      OLLAMA_MODELS="$model_root" OLLAMA_MAX_LOADED_MODELS="${OLLAMA_MAX_LOADED_MODELS:-1}" \
        OLLAMA_KEEP_ALIVE="${OLLAMA_KEEP_ALIVE:-5m}" \
        nohup ollama serve >"${TMPDIR:-/tmp}/everyapi-ollama.log" 2>&1 &
  fi

  if ! wait_for_macos_ollama; then
    err "Ollama did not become ready at http://localhost:11434"
    exit 1
  fi
  ok "native Ollama is ready"
}

detect_macos_memory_gb() {
  local bytes gib=$(( 1024 * 1024 * 1024 ))
  bytes=$(sysctl -n hw.memsize 2>/dev/null || true)
  if [[ "$bytes" =~ ^[0-9]+$ ]] && [ "$bytes" -gt 0 ]; then
    echo $(( (bytes + gib - 1) / gib ))
  else
    echo 0
  fi
}

# Returns a conservative usable memory budget in GiB. On multi-GPU Linux machines we
# deliberately use the smallest GPU: it is safer to select a model that runs
# on every advertised device than to assume the runtime will shard it.
detect_model_memory_gb() {
  case "$GPU" in
    macos)
      local physical_gb reserve_gb
      physical_gb=$(detect_macos_memory_gb)
      if [ "$physical_gb" -gt 0 ]; then
        reserve_gb=$(( physical_gb / 4 ))
        if [ "$reserve_gb" -lt 4 ]; then
          reserve_gb=4
        fi
        if [ "$physical_gb" -gt "$reserve_gb" ]; then
          echo $(( physical_gb - reserve_gb ))
        else
          echo 0
        fi
      else
        echo 0
      fi
      ;;
    nvidia)
      nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null \
        | awk 'BEGIN { min = 0 } /^[0-9]+$/ { if (min == 0 || $1 < min) min = $1 } END { print int(min / 1024) }'
      ;;
    rocm)
      rocm-smi --showmeminfo vram 2>/dev/null \
        | awk '/Total Memory/ { value = $NF; gsub(/[^0-9]/, "", value); if (value > 0 && (min == 0 || value < min)) min = value } END { print int(min / 1024 / 1024 / 1024) }'
      ;;
    *) echo 0 ;;
  esac
}

# Prints the candidate models from largest to smallest. Qwen's sizes are
# deliberately conservative: the selected model must leave capacity for the
# OS, Ollama's context cache, and concurrent marketplace requests.
model_candidates_for_memory() {
  local memory_gb="$1"
  if [ "$MODEL_EXPLICIT" -eq 1 ]; then
    printf '%s\n' "$MODEL"
  elif [ "$memory_gb" -ge 48 ]; then
    printf '%s\n' qwen2.5:32b qwen2.5:14b qwen2.5:7b qwen2.5:3b
  elif [ "$memory_gb" -ge 24 ]; then
    printf '%s\n' qwen2.5:14b qwen2.5:7b qwen2.5:3b
  elif [ "$memory_gb" -ge 12 ]; then
    printf '%s\n' qwen2.5:7b qwen2.5:3b
  else
    printf '%s\n' qwen2.5:3b
  fi
}

model_download_size_gb() {
  case "$1" in
    qwen2.5:3b)  echo 2 ;;
    qwen2.5:7b)  echo 5 ;;
    qwen2.5:14b) echo 10 ;;
    qwen2.5:32b) echo 21 ;;
    *)            echo 0 ;;
  esac
}

has_model_disk_space() {
  local model="$1" estimate_gb available_kb required_kb storage_path
  estimate_gb=$(model_download_size_gb "$model")
  if [ "$estimate_gb" -eq 0 ]; then
    return 0
  fi
  if [ "$GPU" = "macos" ]; then
    storage_path="$HOME/.everyapi/edge"
  else
    storage_path="./data/ollama"
    mkdir -p "$storage_path"
  fi
  available_kb=$(df -Pk "$storage_path" | awk 'NR == 2 { print $4 }')
  required_kb=$(( estimate_gb * 1024 * 1024 ))
  if [[ ! "$available_kb" =~ ^[0-9]+$ ]] || [ "$available_kb" -lt "$required_kb" ]; then
    warn "not enough free disk for $model (needs about ${estimate_gb}GB)"
    return 1
  fi
  return 0
}

ollama_command() {
  if [ "$GPU" = "macos" ]; then
    ollama "$@"
  else
    docker compose -f "$COMPOSE_FILE" exec -T ollama ollama "$@"
  fi
}

verify_selected_model() {
  local model="$1"
  if [ "$GPU" = "macos" ]; then
    curl -fsS --connect-timeout 5 --max-time 120 \
      -H 'Content-Type: application/json' \
      --data "{\"model\":\"$model\",\"prompt\":\"Reply with OK.\",\"stream\":false,\"options\":{\"num_predict\":1}}" \
      http://localhost:11434/api/generate >/dev/null
  else
    ollama_command run "$model" 'Reply with OK.' >/dev/null
  fi
}

prepare_model() {
  local memory_gb candidate
  memory_gb=$(detect_model_memory_gb)
  if [[ ! "$memory_gb" =~ ^[0-9]+$ ]]; then
    memory_gb=0
  fi
  if [ "$MODEL_EXPLICIT" -eq 1 ]; then
    info "using requested model: $MODEL"
  else
    info "detected ${memory_gb}GiB accelerator memory; selecting a conservative model"
  fi

  while IFS= read -r candidate; do
    [ -n "$candidate" ] || continue
    if ! has_model_disk_space "$candidate"; then
      if [ "$MODEL_EXPLICIT" -eq 1 ]; then
        err "free disk space is insufficient for requested model $candidate"
        return 1
      fi
      continue
    fi
    info "pulling model $candidate…"
    if ! ollama_command pull "$candidate"; then
      if [ "$MODEL_EXPLICIT" -eq 1 ]; then
        err "could not pull requested model $candidate"
        return 1
      fi
      warn "could not pull $candidate; trying a smaller model"
      continue
    fi
    info "verifying local inference with $candidate…"
    if verify_selected_model "$candidate"; then
      SELECTED_MODEL="$candidate"
      ok "model $candidate is ready"
      return 0
    fi
    if [ "$MODEL_EXPLICIT" -eq 1 ]; then
      err "requested model $candidate could not complete a local inference request"
      return 1
    fi
    warn "$candidate did not run successfully; trying a smaller model"
  done < <(model_candidates_for_memory "$memory_gb")

  err "no model could be prepared for this machine"
  return 1
}

clear_consumed_registration_token() {
  [ -n "$TOKEN" ] || return 0
  local tmp_env
  tmp_env=$(mktemp .env.XXXXXX)
  grep -v '^EVERYAPI_REGISTRATION_TOKEN=' .env >"$tmp_env" || true
  chmod 600 "$tmp_env"
  mv "$tmp_env" .env
  TOKEN=""
  ok "stored the node identity; removed the consumed registration token"
}

wait_for_agent_connection() {
  local agent_id="$1" log_since="$2" require_models="${3:-0}"
  local deadline=$(( $(date +%s) + 30 )) logs
  while [ "$(date +%s)" -lt "$deadline" ]; do
    logs=$(docker logs --since "$log_since" "$agent_id" 2>&1 || true)
    if printf '%s\n' "$logs" | grep -q 'connected to gateway'; then
      if [ "$require_models" -eq 1 ] && ! printf '%s\n' "$logs" | grep -Eq 'connected to gateway with [1-9][0-9]* models'; then
        printf '%s\n' "$logs" >&2
        return 1
      fi
      return 0
    fi
    if printf '%s\n' "$logs" | grep -q 'session ended:\|fatal:\|authentication failed\|node revoked'; then
      printf '%s\n' "$logs" >&2
      return 1
    fi
    sleep 1
  done
  return 1
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
  nvidia)
    COMPOSE_FILE="docker-compose.yml"
    GPU_MODEL="$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -n1 | tr -d '\r\n')"
    VRAM_MIB="$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -n1 | tr -d '[:space:]')"
    case "$VRAM_MIB" in
      ''|*[!0-9]*) ;;
      *) VRAM_GB=$(( (VRAM_MIB + 1023) / 1024 )) ;;
    esac
    ;;
  rocm)
    COMPOSE_FILE="docker-compose.rocm.yml"
    GPU_MODEL="$(rocminfo 2>/dev/null | awk -F: '/^[[:space:]]*Name:/ {gsub(/^[[:space:]]+|[[:space:]]+$/, "", $2); print $2; exit}')"
    ;;
  macos)
    COMPOSE_FILE="docker-compose.macos.yml"
    GPU_MODEL="Apple Silicon"
    VRAM_GB="$(detect_macos_memory_gb)"
    HOST_PLATFORM="darwin/arm64"
    ;;
  *)
    err "unsupported --gpu value: $GPU (expected nvidia / rocm / macos)"
    exit 1
    ;;
esac
ok "GPU profile: $GPU ($COMPOSE_FILE)"
if [ -n "$VRAM_GB" ]; then
  ok "model budget: ${VRAM_GB} GiB (${GPU_MODEL:-detected GPU})"
else
  warn "could not determine model memory budget; the Control Room will ask before suggesting a model"
fi

# ----- Prompts (only if not given on CLI) ------------------------------------

if [ -z "$NODE_ID" ]; then
  printf '%bEVERYAPI_NODE_ID%b (from the dashboard /seller/edge → New node): ' "$BOLD" "$RESET"
  read -r NODE_ID
fi
if [ -z "$TOKEN" ] && [ "$UPGRADE_MODE" -eq 0 ]; then
  printf '%bEVERYAPI_REGISTRATION_TOKEN%b (one-time, from the same dashboard step): ' "$BOLD" "$RESET"
  read -r TOKEN
fi
if [ -z "$NODE_NAME" ]; then
  NODE_NAME="$(hostname 2>/dev/null || echo "node-$NODE_ID")"
fi

case "$NODE_ID" in
  ''|*[!0-9]*) err "node id must be a positive integer (got: $NODE_ID)"; exit 1 ;;
esac
case "$TOKEN" in
  '')
    if [ "$UPGRADE_MODE" -eq 0 ]; then
      err "registration token has an invalid format"
      exit 1
    fi
    ;;
  edgert_*)
    if [[ ! "$TOKEN" =~ ^edgert_[A-Za-z0-9_-]+$ ]]; then
      err "token contains invalid characters"
      exit 1
    fi
    ;;
  *) err "registration token has an invalid format"; exit 1 ;;
esac
if [[ ! "$NODE_NAME" =~ ^[A-Za-z0-9._\ -]{1,128}$ ]]; then
  err "node name may only contain letters, numbers, spaces, dot, underscore, and hyphen"
  exit 1
fi
if [ -n "$MODEL" ] && [[ ! "$MODEL" =~ ^[A-Za-z0-9._:/-]+$ ]]; then
  err "model may only contain letters, numbers, dot, underscore, colon, slash, and hyphen"
  exit 1
fi
if [[ ! "$GATEWAY" =~ ^https://[A-Za-z0-9._:-]+(/[A-Za-z0-9._~:/?@%+=,-]*)?$ ]] &&
   [[ ! "$GATEWAY" =~ ^http://(localhost|127\.0\.0\.1)(:[0-9]+)?(/[A-Za-z0-9._~:/?@%+=,-]*)?$ ]]; then
  err "gateway must be HTTPS (plain HTTP is allowed only for localhost)"
  exit 1
fi

# ----- Materialise bundle ----------------------------------------------------

if [ "$EXISTING_INSTALL" -eq 1 ]; then
  if [ "$FORCE" -eq 1 ]; then
    info "--force requested; refreshing the verified checkout without deleting it"
  fi
  info "verified existing EveryAPI Edge checkout — pulling latest"
  cd "$INSTALL_DIR"
  if ! git pull --ff-only; then
    err "git pull failed — refusing to continue with an unknown or stale checkout"
    exit 1
  fi
else
  info "cloning bundle to $INSTALL_DIR"
  git clone --depth 1 -- "$BUNDLE_SOURCE" "$INSTALL_DIR" || {
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
mkdir -p "$HOME/.everyapi/edge"
TMP_ENV="$(mktemp .env.XXXXXX)"
cat > "$TMP_ENV" <<EOF
EVERYAPI_GATEWAY=$GATEWAY
EVERYAPI_NODE_ID=$NODE_ID
EVERYAPI_REGISTRATION_TOKEN=$TOKEN
EVERYAPI_NODE_NAME=$NODE_NAME
EVERYAPI_GPU_MODEL=$GPU_MODEL
EVERYAPI_VRAM_GB=$VRAM_GB
EVERYAPI_PLATFORM=$HOST_PLATFORM
EVERYAPI_MODEL_PATH=$HOME/.everyapi/edge
EVERYAPI_COUNTRY=$COUNTRY
EVERYAPI_WORKLOADS=$WORKLOADS
EVERYAPI_CONSOLE_PORT=$CONSOLE_PORT
EOF
chmod 600 "$TMP_ENV"
mv "$TMP_ENV" .env
ok "wrote .env (mode 0600)"

# ----- Bring up --------------------------------------------------------------

if [ "$GPU" = "macos" ]; then
  ensure_macos_ollama
fi

info "pulling images…"
docker compose -f "$COMPOSE_FILE" pull

info "starting bundle…"
AGENT_LOG_SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
docker compose -f "$COMPOSE_FILE" up -d
AGENT_CONTAINER_ID=$(docker compose -f "$COMPOSE_FILE" ps -q agent)
if [ -z "$AGENT_CONTAINER_ID" ]; then
  err "agent container was not created"
  exit 1
fi

# ----- Connect, prepare a model, then reconnect with durable identity --------

info "waiting for the agent to authenticate with the gateway…"
if ! wait_for_agent_connection "$AGENT_CONTAINER_ID" "$AGENT_LOG_SINCE"; then
  err "agent did not connect to the gateway; registration token was kept for recovery"
  exit 1
fi
clear_consumed_registration_token

if [ "$UPGRADE_MODE" -eq 1 ] && [ "$MODEL_EXPLICIT" -eq 0 ]; then
  ok "existing node upgraded; its persisted model library is unchanged"
  ok "done"
  echo
  echo "Next steps:"
  echo "  • Open the EveryAPI dashboard → Seller → Edge nodes"
  echo "    The existing node is Online with refreshed host hardware metadata"
  echo "  • Logs:"
  echo "      docker compose -f $COMPOSE_FILE logs -f agent"
  return 0
fi

if ! prepare_model; then
  err "agent remains running, but no verified model is available"
  exit 1
fi

info "restarting the agent so the gateway receives model $SELECTED_MODEL…"
AGENT_LOG_SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
docker compose -f "$COMPOSE_FILE" up -d --force-recreate agent
AGENT_CONTAINER_ID=$(docker compose -f "$COMPOSE_FILE" ps -q agent)
if [ -z "$AGENT_CONTAINER_ID" ] || ! wait_for_agent_connection "$AGENT_CONTAINER_ID" "$AGENT_LOG_SINCE" 1; then
  err "agent did not reconnect with its persisted identity"
  exit 1
fi
ok "agent connected and model $SELECTED_MODEL is ready"

ok "done"
echo
echo "Next steps:"
echo "  • Open the EveryAPI dashboard → Seller → Edge nodes"
echo "    The node is Online and reports model: $SELECTED_MODEL"
echo "  • To choose a different model on a later install, add:"
echo "      --model qwen2.5:7b"
echo "  • Open your local Edge Control Room (models, load, requests and logs):"
echo "      http://<this-node-LAN-IP>:8421"
echo "    (or http://127.0.0.1:8421 on this node; use only on a trusted LAN)"
echo "  • Logs:"
echo "      docker compose -f $COMPOSE_FILE logs -f agent"

} # end main

main "$@"
