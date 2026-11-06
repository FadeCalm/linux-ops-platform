#!/usr/bin/env bash
# curl examples for the Agent and management backend.
#
#   scripts/curl-examples.sh          # print the commands
#   scripts/curl-examples.sh --run    # execute a read-only + echo demo
#
# The backend requires a login cookie when auth.access_password is set:
#   curl -c /tmp/lop.cookie -X POST http://127.0.0.1:8080/api/v1/auth/login \
#     -H 'Content-Type: application/json' -d '{"password":"..."}'
# then add -b /tmp/lop.cookie to every request below. The agent port has no
# authentication at all; keep everything on an isolated network.
# For self-signed HTTPS use `curl --cacert /path/ca.crt` — never default to -k.
set -euo pipefail

AGENT_URL="${AGENT_URL:-http://127.0.0.1:19090}"
BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:8080}"
RUN=0
[[ "${1:-}" == "--run" ]] && RUN=1

cat <<EOF
# ---- Agent API ----
curl -sS ${AGENT_URL}/api/v1/health
curl -sS ${AGENT_URL}/api/v1/info
curl -sS ${AGENT_URL}/api/v1/version

# submit a task (idempotent by task_id)
curl -sS -X POST ${AGENT_URL}/api/v1/tasks \\
  -H 'Content-Type: application/json' \\
  -d '{"task_id":"demo-1","command":"echo hello; uname -a","timeout_ms":10000}'

# query status
curl -sS ${AGENT_URL}/api/v1/tasks/demo-1

# pull log chunks by cursor (base64 encoded)
curl -sS '${AGENT_URL}/api/v1/tasks/demo-1/logs?stdout_offset=0&stderr_offset=0'

# cancel
curl -sS -X POST ${AGENT_URL}/api/v1/tasks/demo-1/cancel

# ---- Backend API ----
curl -sS ${BACKEND_URL}/api/v1/health
curl -sS ${BACKEND_URL}/api/v1/servers
curl -sS -X POST ${BACKEND_URL}/api/v1/tasks \\
  -H 'Content-Type: application/json' \\
  -d '{"command":"echo hello","timeout_ms":10000,"server_ids":[1]}'
curl -sS ${BACKEND_URL}/api/v1/tasks
curl -sS '${BACKEND_URL}/api/v1/targets/<target-id>/logs?stream=stdout&offset=0&limit=65536'
EOF

if [[ "$RUN" == "1" ]]; then
  echo
  echo "==> running round-trip demo"
  TASK_ID="demo-$(date +%s)"
  curl -sS "${AGENT_URL}/api/v1/health"; echo
  curl -sS -X POST "${AGENT_URL}/api/v1/tasks" \
    -H 'Content-Type: application/json' \
    -d "{\"task_id\":\"${TASK_ID}\",\"command\":\"echo hello from agent\",\"timeout_ms\":10000}"; echo
  sleep 1
  curl -sS "${AGENT_URL}/api/v1/tasks/${TASK_ID}"; echo
  curl -sS "${AGENT_URL}/api/v1/tasks/${TASK_ID}/logs?stdout_offset=0&stderr_offset=0"; echo
  echo "task id: ${TASK_ID}"
fi
