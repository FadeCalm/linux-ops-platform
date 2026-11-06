#!/usr/bin/env bash
# One-shot installer for a single Linux amd64 server (backend + agent + MySQL/MinIO).
#
#   sudo deploy/.env           # first: cp deploy/.env.example deploy/.env and edit
#   sudo scripts/install.sh [--lan-ip 10.0.0.10] [--skip-compose]
#
# Requirements: Go 1.22+, Node 18+ (for the frontend build), Docker + Compose
# (unless --skip-compose and you point DB_DSN at an existing MySQL).
#
# SECURITY: this phase has no authentication. By default the backend binds to
# 127.0.0.1. Pass --lan-ip <private-ip> to expose it on a management network,
# and restrict that port with firewall/security-group rules to the admin subnet.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_DIR="${INSTALL_DIR:-/opt/linux-ops-platform}"
CONF_DIR="${CONF_DIR:-/etc/linux-ops}"
DATA_DIR="${DATA_DIR:-/var/lib/linux-ops}"
SERVICE_USER="${SERVICE_USER:-linuxops}"
LAN_IP=""
SKIP_COMPOSE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --lan-ip) LAN_IP="${2:-}"; shift 2 ;;
    --skip-compose) SKIP_COMPOSE=1; shift ;;
    *) echo "unknown argument: $1"; exit 2 ;;
  esac
done

if [[ $EUID -ne 0 ]]; then
  echo "please run as root (sudo)"; exit 1
fi

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
die() { printf '\n\033[1;31mERROR: %s\033[0m\n' "$*"; exit 1; }

command -v go >/dev/null 2>&1 || die "Go 1.22+ is required (https://go.dev/dl/)"
command -v npm >/dev/null 2>&1 || echo "!! npm not found: frontend build will be skipped"

# ---------------------------------------------------------------------------
# MySQL / MinIO
# ---------------------------------------------------------------------------
if [[ "$SKIP_COMPOSE" == "0" ]]; then
  command -v docker >/dev/null 2>&1 || die "docker is required (or pass --skip-compose)"
  docker compose version >/dev/null 2>&1 || die "docker compose plugin is required"
  [[ -f "$ROOT/deploy/.env" ]] || die "copy deploy/.env.example to deploy/.env and set strong secrets first"
  log "starting MySQL 8 and MinIO via docker compose"
  (cd "$ROOT/deploy" && docker compose up -d)
else
  log "skipping docker compose (expecting an external MySQL)"
fi

# ---------------------------------------------------------------------------
# build
# ---------------------------------------------------------------------------
log "building backend, agent and frontend"
bash "$ROOT/scripts/build.sh"

# ---------------------------------------------------------------------------
# system user & directories
# ---------------------------------------------------------------------------
if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  log "creating system user $SERVICE_USER"
  useradd --system --home "$INSTALL_DIR" --shell /sbin/nologin "$SERVICE_USER"
fi

log "installing files under $INSTALL_DIR"
install -d -m 0755 "$INSTALL_DIR"/{bin,frontend,docs}
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR/logs"
install -d -m 0755 "$CONF_DIR"

install -m 0755 "$ROOT/bin/backend" "$INSTALL_DIR/bin/backend"
install -m 0755 "$ROOT/bin/agent" "$INSTALL_DIR/bin/agent"
if [[ -f "$ROOT/frontend/dist/index.html" ]]; then
  rm -rf "$INSTALL_DIR/frontend"/*
  cp -r "$ROOT/frontend/dist/." "$INSTALL_DIR/frontend/"
fi
cp -r "$ROOT/docs/." "$INSTALL_DIR/docs/" 2>/dev/null || true
cp -r "$ROOT/scripts" "$INSTALL_DIR/scripts" 2>/dev/null || true
cp -r "$ROOT/tests" "$INSTALL_DIR/tests" 2>/dev/null || true
chmod +x "$INSTALL_DIR"/scripts/*.sh "$INSTALL_DIR"/tests/*.sh 2>/dev/null || true

# ---------------------------------------------------------------------------
# configs (secrets via EnvironmentFile, never in the YAML)
# ---------------------------------------------------------------------------
install -m 0644 "$ROOT/configs/backend.yaml" "$CONF_DIR/backend.yaml"
install -m 0644 "$ROOT/configs/agent.yaml" "$CONF_DIR/agent.yaml"

DB_PASSWORD_VALUE=""
if [[ -f "$ROOT/deploy/.env" ]]; then
  DB_PASSWORD_VALUE="$(grep -E '^DB_PASSWORD=' "$ROOT/deploy/.env" | head -1 | cut -d= -f2-)"
  # Strip optional surrounding quotes the same way docker compose does.
  DB_PASSWORD_VALUE="${DB_PASSWORD_VALUE%\"}"; DB_PASSWORD_VALUE="${DB_PASSWORD_VALUE#\"}"
  DB_PASSWORD_VALUE="${DB_PASSWORD_VALUE%\'}"; DB_PASSWORD_VALUE="${DB_PASSWORD_VALUE#\'}"
fi
if [[ -z "$DB_PASSWORD_VALUE" ]]; then
  command -v openssl >/dev/null 2>&1 || die "set DB_PASSWORD in deploy/.env (openssl not found to generate one)"
  DB_PASSWORD_VALUE="$(openssl rand -hex 16)"
fi

BIND_ADDR="127.0.0.1:8080"
if [[ -n "$LAN_IP" ]]; then
  BIND_ADDR="${LAN_IP}:8080"
fi

log "writing $CONF_DIR/backend.env (mode 600)"
umask 077
cat > "$CONF_DIR/backend.env" <<EOF
DB_PASSWORD=${DB_PASSWORD_VALUE}
LOG_DIR=${DATA_DIR}/logs
FRONTEND_DIR=${INSTALL_DIR}/frontend
LISTEN_ADDR=${BIND_ADDR}
EOF
chmod 600 "$CONF_DIR/backend.env"
umask 022

# agent.env is optional; keep an empty placeholder so the unit's optional file exists.
[[ -f "$CONF_DIR/agent.env" ]] || : > "$CONF_DIR/agent.env"
chmod 600 "$CONF_DIR/agent.env"

# ---------------------------------------------------------------------------
# systemd
# ---------------------------------------------------------------------------
log "installing systemd units"
install -m 0644 "$ROOT/deploy/systemd/linux-ops-backend.service" /etc/systemd/system/
install -m 0644 "$ROOT/deploy/systemd/linux-ops-agent.service" /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now linux-ops-agent linux-ops-backend

log "waiting for backend health"
ok=0
for _ in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:8080/api/v1/health" >/dev/null 2>&1; then ok=1; break; fi
  sleep 1
done

echo
if [[ "$ok" == "1" ]]; then
  echo "backend is healthy. Access the UI at:"
  if [[ -n "$LAN_IP" ]]; then
    echo "  http://${LAN_IP}:8080/"
    echo "  (restrict port 8080 to your admin subnet in the firewall now)"
  else
    echo "  http://127.0.0.1:8080/  (local only)"
    echo "  To access from another machine, set LISTEN_ADDR in $CONF_DIR/backend.env"
    echo "  to this server's PRIVATE IP, restart the service, and restrict the port."
  fi
else
  echo "backend did not become healthy in time. Check:"
  echo "  journalctl -u linux-ops-backend -n 100 --no-pager"
fi
echo
echo "Agent listens on 127.0.0.1:19090 (config: $CONF_DIR/agent.yaml)."
echo "Smoke test:  $INSTALL_DIR/tests/smoke.sh"
echo
echo "REMINDER: phase 1 has NO authentication. Keep it on an isolated network."
echo "Do not expose this service to the internet."
