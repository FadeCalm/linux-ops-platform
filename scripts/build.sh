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

# Default to the version hardcoded in agent/main.go so the binary and the
# release package name never drift apart.
AGENT_VERSION="${AGENT_VERSION:-$(sed -n 's/^var version = "\(.*\)"$/\1/p' "$ROOT/agent/main.go")}"
AGENT_VERSION="${AGENT_VERSION:-0.0.0}"

echo "==> building agent ($AGENT_VERSION)"
(cd "$ROOT/agent" && CGO_ENABLED=0 go build \
  -ldflags "-X main.version=${AGENT_VERSION}" \
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
