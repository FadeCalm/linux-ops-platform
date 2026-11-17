#!/usr/bin/env bash
# curl examples for the Agent and management backend.
#
#   scripts/curl-examples.sh          # print the commands
#   scripts/curl-examples.sh --run    # execute a read-only + echo demo
#
# The backend requires authentication. Browser-style (cookie):
#   curl -c /tmp/lop.cookie -X POST http://127.0.0.1:8089/api/v1/auth/login \
#     -H 'Content-Type: application/json' -d '{"username":"admin","password":"..."}'
# then add -b /tmp/lop.cookie to every request below.
# Script-style (short-lived JWT):
#   curl -X POST .../auth/token -H 'Content-Type: application/json' \
#     -d '{"username":"admin","password":"..."}'
# then send -H "Authorization: Bearer <access_token>". Long-lived API tokens
# (lop_...) are created in the UI under 「令牌」 and used the same way.
# The agent port has no request authentication; it only accepts source IPs in
# `allowed_ips` (configured by agent-install.sh). An allowlist rejection
# returns 403, so run these from the backend host. For self-signed HTTPS use
# `curl --cacert /path/ca.crt` — never -k.
set -euo pipefail

AGENT_URL="${AGENT_URL:-http://127.0.0.1:19090}"
BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:8089}"
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

# exchange credentials for a JWT, then use it as a Bearer token
curl -sS -X POST ${BACKEND_URL}/api/v1/auth/token \\
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"..."}'
curl -sS -H 'Authorization: Bearer <jwt-or-lop-token>' ${BACKEND_URL}/api/v1/servers

# manage long-lived API tokens (cookie or Bearer auth)
curl -sS -b /tmp/lop.cookie -X POST ${BACKEND_URL}/api/v1/auth/tokens \\
  -H 'Content-Type: application/json' -d '{"name":"script","expires_in_days":90}'
curl -sS -b /tmp/lop.cookie ${BACKEND_URL}/api/v1/auth/tokens

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
