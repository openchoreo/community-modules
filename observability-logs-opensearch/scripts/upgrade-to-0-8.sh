#!/usr/bin/env bash
# Add the 0.8.x release line fields to existing k8s-events-* and audit indices.
# Run AFTER `helm upgrade … observability-logs-opensearch --version 0.8.0`.
#
# 0.8.0 maps the release line labels of Kubernetes events and the release line of audit
# records. The index templates give these fields to indices created after the upgrade. This
# script adds them to the existing indices, so records written to them from now on can be
# filtered by release line. Adding fields needs no rebuild.
#
# Safe to re-run.

set -euo pipefail

NS="${NS:-openchoreo-observability-plane}"
OS_SECRET="${OS_SECRET:-opensearch-admin-credentials}"
OS_SERVICE="${OS_SERVICE:-svc/opensearch}"
LOCAL_PORT="${LOCAL_PORT:-9200}"
AUDIT_INDEX_PREFIX="${AUDIT_INDEX_PREFIX:-audit-logs-}"

C_BOLD=$'\033[1m'; C_DIM=$'\033[2m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_RESET=$'\033[0m'
step()  { printf '\n%s== %s ==%s\n' "$C_BOLD" "$1" "$C_RESET"; }
info()  { printf '  %s%s%s\n' "$C_DIM" "$1" "$C_RESET"; }
ok()    { printf '  %s✓%s %s\n' "$C_GREEN" "$C_RESET" "$1"; }
warn()  { printf '  %s!%s %s\n' "$C_YELLOW" "$C_RESET" "$1"; }
fail()  { printf '  %s✗%s %s\n' "$C_RED" "$C_RESET" "$1"; }

for bin in kubectl curl jq; do
  command -v "$bin" >/dev/null || { fail "$bin is required"; exit 1; }
done

PF_PID=""
cleanup() {
  [ -n "${PF_PID:-}" ] && kill "$PF_PID" 2>/dev/null || true
}
trap cleanup EXIT

step "Port-forwarding $OS_SERVICE -> localhost:$LOCAL_PORT"
kubectl port-forward -n "$NS" "$OS_SERVICE" "$LOCAL_PORT:9200" >/dev/null 2>&1 &
PF_PID=$!
PF_READY=0
for _ in $(seq 1 30); do
  # A port-forward that exited (e.g. LOCAL_PORT already in use) must not let another
  # listener on that port pass as OpenSearch and receive the credentials.
  if ! kill -0 "$PF_PID" 2>/dev/null; then
    fail "port-forward exited; is localhost:$LOCAL_PORT already in use? set LOCAL_PORT to a free port"
    exit 1
  fi
  if curl -sS -k "https://localhost:$LOCAL_PORT" >/dev/null 2>&1; then PF_READY=1; break; fi
  sleep 1
done
[ "$PF_READY" -eq 1 ] || { fail "port-forward to $OS_SERVICE never became ready"; exit 1; }
ok "tunnel ready (pid $PF_PID)"

OS_USER=$(kubectl get secret -n "$NS" "$OS_SECRET" -o jsonpath='{.data.username}' | base64 -d)
OS_PASS=$(kubectl get secret -n "$NS" "$OS_SECRET" -o jsonpath='{.data.password}' | base64 -d)
BASE="https://localhost:$LOCAL_PORT"
OS() { curl -sS -k -u "$OS_USER:$OS_PASS" -H 'Content-Type: application/json' "$@"; }

expect_ack() {
  local what="$1" resp="$2"
  if [ "$(echo "$resp" | jq -r '.acknowledged // false')" != "true" ]; then
    fail "$what failed: $resp"
    exit 1
  fi
}

step "k8s-events-*"
expect_ack "adding the release line labels" "$(OS -XPUT "$BASE/k8s-events-*/_mapping?allow_no_indices=true" -d '
{
  "properties": {
    "resource": {
      "properties": {
        "k8s": {
          "properties": {
            "object": {
              "properties": {
                "label": {
                  "properties": {
                    "openchoreo": {
                      "properties": {
                        "dev/release-line": {
                          "type": "keyword"
                        },
                        "dev/release-line-uid": {
                          "type": "keyword"
                        },
                        "dev/release-line-component-uid": {
                          "type": "keyword"
                        }
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}')"
ok "release line labels added"

step "${AUDIT_INDEX_PREFIX}*"
expect_ack "adding resource.release_line" "$(OS -XPUT "$BASE/${AUDIT_INDEX_PREFIX}*/_mapping?allow_no_indices=true" -d '
{
  "properties": {
    "resource": {
      "properties": {
        "release_line": {
          "type": "keyword"
        }
      }
    }
  }
}')"
ok "resource.release_line added"

step "Done"
