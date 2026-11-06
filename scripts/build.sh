#!/usr/bin/env bash
# Build backend, agent and frontend for the current host (Linux amd64 target).
# Usage: scripts/build.sh [--skip-frontend]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKIP_FRONTEND=0
for arg in "$@"; do
  case "$arg" in
    --skip-frontend) SKIP_FRONTEND=1 ;;
    *) echo "unknown argument: $arg"; exit 2 ;;
  esac
done

mkdir -p "$ROOT/bin"

echo "==> building backend"
(cd "$ROOT/backend" && go build -o "$ROOT/bin/backend" .)

echo "==> building agent"
(cd "$ROOT/agent" && CGO_ENABLED=0 go build \
  -ldflags "-X main.version=${AGENT_VERSION:-0.1.0}" \
  -o "$ROOT/bin/agent" .)

if [[ "$SKIP_FRONTEND" == "0" ]]; then
  if command -v npm >/dev/null 2>&1; then
    echo "==> building frontend"
    (cd "$ROOT/frontend" && (npm ci --no-audit --no-fund 2>/dev/null || npm install --no-audit --no-fund) && npm run build)
  else
    echo "!! npm not found; skipping frontend build"
  fi
fi

echo
echo "Binaries:"
ls -lh "$ROOT/bin" 2>/dev/null || true
[[ -f "$ROOT/frontend/dist/index.html" ]] && echo "Frontend: $ROOT/frontend/dist"
