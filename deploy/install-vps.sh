#!/bin/sh
# enowx-rag VPS installer: PostgreSQL + pgvector, the enowx-rag server
# (dashboard, API, MCP) and optional HTTPS, all with Docker.
#
#   curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/deploy/install-vps.sh | sudo sh
#
# Non-interactive (every value optional):
#   curl -fsSL .../install-vps.sh | sudo ENOWX_DOMAIN=rag.example.com VOYAGE_API_KEY=pa-... sh
#
# Options (env vars):
#   ENOWX_DOMAIN     DNS name of this server → HTTPS via Caddy (Let's Encrypt)
#   VOYAGE_API_KEY   Voyage AI key (otherwise add it in the dashboard)
#   ENOWX_DIR        install directory (default /opt/enowx-rag)
#   ENOWX_REF        git branch or tag to install (default main)
#   ENOWX_PORT       dashboard port without a domain (default 7777)
#
# Re-running it updates the code and restarts the stack; .env (secrets) is kept.
set -eu

REPO="https://github.com/enowdev/enowx-rag"
DIR="${ENOWX_DIR:-/opt/enowx-rag}"
REF="${ENOWX_REF:-main}"
PORT="${ENOWX_PORT:-7777}"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
ok()   { printf '\033[1;32m ✓\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m !\033[0m %s\n' "$1"; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$1" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || err "this installer is for a Linux VPS (on macOS/Windows use docker compose -f docker-compose.vps.yml up -d)"
if [ "$(id -u)" -ne 0 ]; then
  command -v sudo >/dev/null 2>&1 || err "run as root"
  SUDO=sudo
else
  SUDO=
fi

# ask VAR "question" — reads from the terminal even under curl | sh; keeps
# the variable as is when it is already set or there is no terminal
# (cloud-init, CI: /dev/tty exists but can't be opened).
ask() {
  eval "cur=\${$1:-}"
  [ -n "$cur" ] && return 0
  if (: < /dev/tty) 2>/dev/null; then
    printf '%s ' "$2" > /dev/tty 2>/dev/null || return 0
    read -r ans < /dev/tty || ans=""
    eval "$1=\$ans"
  fi
  return 0
}

randhex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$1"
  else
    head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# --- Docker ---
if ! command -v docker >/dev/null 2>&1; then
  info "Installing Docker (get.docker.com)…"
  curl -fsSL https://get.docker.com | $SUDO sh
fi
$SUDO docker compose version >/dev/null 2>&1 || err "docker compose v2 is missing (install the docker-compose-plugin package)"
ok "Docker $($SUDO docker version --format '{{.Server.Version}}' 2>/dev/null || echo ready)"

# --- Code ---
if [ -f "./docker-compose.vps.yml" ] && [ -d "./mcp-server" ]; then
  DIR="$(pwd)"
  info "Using the checkout in $DIR"
elif [ -d "$DIR/.git" ]; then
  info "Updating $DIR ($REF)…"
  $SUDO git -C "$DIR" fetch --depth 1 origin "$REF" && $SUDO git -C "$DIR" checkout -q FETCH_HEAD
else
  command -v git >/dev/null 2>&1 || { info "Installing git…"; (apt-get update -qq && apt-get install -y -qq git) >/dev/null 2>&1 || dnf install -y -q git || yum install -y -q git || apk add --no-cache git; }
  info "Downloading enowx-rag ($REF) into $DIR…"
  $SUDO git clone --depth 1 --branch "$REF" "$REPO" "$DIR"
fi
cd "$DIR"

# --- Settings (.env) ---
if [ -f .env ]; then
  ok "Keeping the existing .env (secrets unchanged)"
  # shellcheck disable=SC1091
  . ./.env
else
  echo
  ask ENOWX_DOMAIN "Domain for HTTPS (DNS must point here; empty = http://SERVER-IP:$PORT):"
  ask VOYAGE_API_KEY "Voyage AI API key (pa-…; empty = add it later in the dashboard):"
  POSTGRES_PASSWORD="$(randhex 24)"
  RAG_ADMIN_TOKEN="$(randhex 32)"
  if [ -n "${ENOWX_DOMAIN:-}" ]; then BIND=127.0.0.1; else BIND=0.0.0.0; fi
  # Small VPS: embed the committed dashboard instead of running npm.
  mem_kb=$(awk '/MemTotal/ {print $2}' /proc/meminfo 2>/dev/null || echo 0)
  if [ "$mem_kb" -gt 0 ] && [ "$mem_kb" -lt 2000000 ]; then WEB=prebuilt; else WEB=build; fi
  umask 077
  cat > .env <<ENV
POSTGRES_PASSWORD=$POSTGRES_PASSWORD
RAG_ADMIN_TOKEN=$RAG_ADMIN_TOKEN
RAG_VOYAGE_API_KEY=${VOYAGE_API_KEY:-}
ENOWX_DOMAIN=${ENOWX_DOMAIN:-}
ENOWX_BIND=$BIND
ENOWX_PORT=$PORT
ENOWX_WEB_BUILD=$WEB
ENV
  ok "Wrote $DIR/.env (mode 600) with generated secrets"
fi

PROFILE=""
if [ -n "${ENOWX_DOMAIN:-}" ]; then
  PROFILE="--profile https"
  URL="https://$ENOWX_DOMAIN"
else
  ip=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I 2>/dev/null | awk '{print $1}')
  URL="http://${ip:-SERVER-IP}:${ENOWX_PORT:-$PORT}"
fi

# --- Start ---
info "Building and starting (first build takes a few minutes)…"
# shellcheck disable=SC2086
$SUDO docker compose -f docker-compose.vps.yml $PROFILE up -d --build

info "Waiting for the server…"
# Up when /api/health answers on the published port, or when Docker's own
# health check (same endpoint, inside the container) says healthy.
healthy() {
  curl -fsS "http://127.0.0.1:${ENOWX_PORT:-$PORT}/api/health" >/dev/null 2>&1 && return 0
  cid=$($SUDO docker compose -f docker-compose.vps.yml ps -q enowx-rag 2>/dev/null)
  [ -n "$cid" ] && [ "$($SUDO docker inspect -f '{{.State.Health.Status}}' "$cid" 2>/dev/null)" = "healthy" ]
}
i=0
until healthy; do
  i=$((i + 1))
  if [ "$i" -gt 90 ]; then
    $SUDO docker compose -f docker-compose.vps.yml logs --tail 40 enowx-rag || true
    err "enowx-rag did not come up — see the logs above"
  fi
  sleep 2
done
ok "enowx-rag is up"

cat <<DONE

  Dashboard:   $URL
  Admin token: $RAG_ADMIN_TOKEN
               (also in $DIR/.env — keep it secret)

  On every machine where you code:
    curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/install.sh | sh
    enowx-rag connect --remote $URL --token $RAG_ADMIN_TOKEN
    enowx-rag push    --remote $URL --token $RAG_ADMIN_TOKEN   # existing local memory

  Manage:  cd $DIR && docker compose -f docker-compose.vps.yml $PROFILE logs -f enowx-rag
  Update:  re-run this installer
DONE
if [ -z "${RAG_VOYAGE_API_KEY:-}${VOYAGE_API_KEY:-}" ]; then
  warn "No Voyage key yet: the dashboard's setup asks for it (and lets you pick the models)."
fi
if [ -z "${ENOWX_DOMAIN:-}" ]; then
  warn "Plain HTTP: the token travels unencrypted. Set ENOWX_DOMAIN and re-run for HTTPS."
  warn "Docker publishes port ${ENOWX_PORT:-$PORT} past ufw/firewalld; restrict it at your provider's firewall if needed."
fi
