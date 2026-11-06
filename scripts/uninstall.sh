#!/usr/bin/env bash
# Stop and remove the platform services. Data (MySQL volume, logs) is NOT
# deleted unless you pass --purge-data.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_DIR="${INSTALL_DIR:-/opt/linux-ops-platform}"
PURGE=0
for arg in "$@"; do
  [[ "$arg" == "--purge-data" ]] && PURGE=1
done

if [[ $EUID -ne 0 ]]; then echo "please run as root (sudo)"; exit 1; fi

systemctl disable --now linux-ops-backend linux-ops-agent 2>/dev/null || true
rm -f /etc/systemd/system/linux-ops-backend.service /etc/systemd/system/linux-ops-agent.service
systemctl daemon-reload

if [[ "$PURGE" == "1" ]]; then
  (cd "$ROOT/deploy" && docker compose down -v) 2>/dev/null || true
  rm -rf /var/lib/linux-ops
  echo "purged data (MySQL volume, logs)"
else
  (cd "$ROOT/deploy" && docker compose down) 2>/dev/null || true
  echo "stopped services; data kept (use --purge-data to delete)"
fi

echo "binaries remain at $INSTALL_DIR (remove manually if desired)"
