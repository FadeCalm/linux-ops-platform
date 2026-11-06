#!/usr/bin/env bash
# End-to-end smoke test against a running backend + agent.
# Requires: curl, python3. Run on the Linux server after scripts/install.sh.
#
#   tests/smoke.sh
#   BACKEND_URL=http://10.0.0.10:8080 tests/smoke.sh
set -euo pipefail

BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:8080}"
API="$BACKEND_URL/api/v1"
PASS=0
FAIL=0

say() { printf '%s\n' "$*"; }
ok()  { PASS=$((PASS+1)); printf '\033[1;32mPASS\033[0m %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '\033[1;31mFAIL\033[0m %s\n' "$*"; }

command -v curl >/dev/null || { echo "curl required"; exit 1; }
command -v python3 >/dev/null || { echo "python3 required"; exit 1; }

wrap_data() {
  # Extract a dotted path from the {code,data:{...}} envelope.
  python3 -c '
import sys, json
try:
    d = json.load(sys.stdin)
except Exception as e:
    print(""); sys.exit(0)
cur = d
for p in sys.argv[1].split("."):
    if isinstance(cur, list):
        cur = cur[int(p)]
    elif isinstance(cur, dict):
        cur = cur.get(p)
    else:
        cur = None
    if cur is None:
        break
print("" if cur is None else cur)
' "$1"
}

say "==> backend health"
if curl -fsS "$API/health" >/dev/null 2>&1; then ok "backend reachable at $BACKEND_URL"; else bad "backend unreachable"; exit 1; fi

say "==> selecting a server"
SERVERS="$(curl -fsS "$API/servers?page_size=200")"
SERVER_ID="$(echo "$SERVERS" | python3 -c '
import sys, json
d = json.load(sys.stdin).get("data", {})
items = d.get("items", [])
online = [s for s in items if s.get("status") == "online"]
print((online[0]["id"] if online else (items[0]["id"] if items else "")))
')"
if [[ -z "$SERVER_ID" ]]; then
  SERVER_ID="$(curl -fsS -X POST "$API/servers" -H 'Content-Type: application/json' \
    -d '{"name":"smoke-local","ip":"127.0.0.1","agent_port":19090}' | wrap_data 'data.id')"
  ok "created server id=$SERVER_ID"
else
  ok "using server id=$SERVER_ID"
fi

say "==> health check"
CHECK="$(curl -fsS -X POST "$API/servers/$SERVER_ID/check")"
ONLINE="$(echo "$CHECK" | wrap_data 'data.online')"
if [[ "$ONLINE" == "True" || "$ONLINE" == "true" ]]; then ok "agent online"; else bad "agent offline: $CHECK"; exit 1; fi

say "==> creating task"
CREATE="$(curl -fsS -X POST "$API/tasks" -H 'Content-Type: application/json' \
  -d "{\"command\":\"echo smoke-ok; uname -s; echo smoke-err >&2\",\"timeout_ms\":15000,\"server_ids\":[$SERVER_ID]}")"
TASK_ID="$(echo "$CREATE" | wrap_data 'data.task.id')"
TARGET_ID="$(echo "$CREATE" | wrap_data 'data.targets.0.id')"
[[ -n "$TASK_ID" ]] && ok "task created: $TASK_ID" || { bad "task creation failed: $CREATE"; exit 1; }

say "==> waiting for completion"
STATUS=""
for _ in $(seq 1 40); do
  STATUS="$(curl -fsS "$API/tasks/$TASK_ID" | wrap_data 'data.task.status')"
  case "$STATUS" in
    SUCCESS|FAILED|TIMEOUT|CANCELLED|UNKNOWN) break ;;
  esac
  sleep 0.5
done
if [[ "$STATUS" == "SUCCESS" ]]; then ok "task status SUCCESS"; else bad "task status $STATUS"; fi

say "==> reading stdout"
LOG="$(curl -fsS "$API/targets/$TARGET_ID/logs?stream=stdout&offset=0&limit=65536" | wrap_data 'data.data')"
STDOUT="$(printf '%s' "$LOG" | python3 -c 'import sys,base64; sys.stdout.write(base64.b64decode(sys.stdin.read().strip()).decode("utf-8","replace"))')"
if printf '%s' "$STDOUT" | grep -q 'smoke-ok'; then ok "stdout contains smoke-ok"; else bad "stdout missing smoke-ok: [$STDOUT]"; fi

say "==> reading stderr"
ELOG="$(curl -fsS "$API/targets/$TARGET_ID/logs?stream=stderr&offset=0&limit=65536" | wrap_data 'data.data')"
STDERR="$(printf '%s' "$ELOG" | python3 -c 'import sys,base64; sys.stdout.write(base64.b64decode(sys.stdin.read().strip()).decode("utf-8","replace"))')"
if printf '%s' "$STDERR" | grep -q 'smoke-err'; then ok "stderr contains smoke-err"; else bad "stderr missing smoke-err: [$STDERR]"; fi

say ""
say "passed=$PASS failed=$FAIL"
[[ "$FAIL" == "0" ]]
