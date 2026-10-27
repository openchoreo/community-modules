#!/usr/bin/env bash
# Add the 0.9.0 release line fields to existing trace indices
# Run AFTER `helm upgrade … observability-tracing-opensearch 0.9.0`

set -euo pipefail

NS=openchoreo-observability-plane
OS_SECRET=opensearch-admin-credentials
LOCAL_PORT=9200

C_BOLD=$'\033[1m'; C_DIM=$'\033[2m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_RESET=$'\033[0m'
step()  { printf '\n%s== %s ==%s\n' "$C_BOLD" "$1" "$C_RESET"; }
info()  { printf '  %s%s%s\n' "$C_DIM" "$1" "$C_RESET"; }
ok()    { printf '  %s✓%s %s\n' "$C_GREEN" "$C_RESET" "$1"; }
warn()  { printf '  %s!%s %s\n' "$C_YELLOW" "$C_RESET" "$1"; }
fail()  { printf '  %s✗%s %s\n' "$C_RED" "$C_RESET" "$1"; }

PF_PID=""
cleanup() {
  if [ -n "${PF_PID:-}" ]; then
    kill "$PF_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

step "Port-forwarding svc/opensearch -> localhost:$LOCAL_PORT"
# If the port is taken, the port-forward fails and the probe below would reach that listener instead.
if curl -sS -k "https://localhost:$LOCAL_PORT" >/dev/null 2>&1; then
  fail "localhost:$LOCAL_PORT is already in use"
  exit 1
fi
kubectl port-forward -n "$NS" svc/opensearch "$LOCAL_PORT:9200" >/dev/null 2>&1 &
PF_PID=$!
PF_READY=0
for _ in {1..30}; do
  if curl -sS -k "https://localhost:$LOCAL_PORT" >/dev/null 2>&1; then
    PF_READY=1
    break
  fi
  sleep 1
done
if [ "$PF_READY" -ne 1 ]; then
  fail "port-forward to svc/opensearch never became ready"
  exit 1
fi
ok "tunnel ready (pid $PF_PID)"

PASS=$(kubectl get secret -n "$NS" "$OS_SECRET" -o jsonpath='{.data.password}' | base64 -d)
OS() { curl -sS -k -u "admin:$PASS" -H 'Content-Type: application/json' "$@"; }

step "Adding the release line fields to otel-traces-* indices"
resp=$(OS -XPUT "https://localhost:$LOCAL_PORT/otel-traces-*/_mapping?allow_no_indices=true" -d '
{
  "properties": {
    "resource": {
      "properties": {
        "openchoreo.dev/release-line": {
          "type": "keyword"
        },
        "openchoreo.dev/release-line-uid": {
          "type": "keyword"
        },
        "openchoreo.dev/release-line-component-uid": {
          "type": "keyword"
        }
      }
    }
  }
}')
if printf '%s' "$resp" | grep -q '"acknowledged":true'; then
  ok "fields added"
else
  fail "add fields -> $resp"
  exit 1
fi

step "Closing port-forward"
if kill "$PF_PID" 2>/dev/null; then
  ok "stopped pid $PF_PID"
fi
PF_PID=""

trap - EXIT
printf '\n%sDone.%s\n' "$C_BOLD" "$C_RESET"
