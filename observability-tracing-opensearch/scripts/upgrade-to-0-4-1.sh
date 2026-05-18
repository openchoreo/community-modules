# Reindex existing trace indices onto the 0.4.1 mapping
# Run AFTER `helm upgrade … observability-tracing-opensearch 0.4.1`

set -u

NS=openchoreo-observability-plane
OS_SECRET=opensearch-admin-credentials
LOCAL_PORT=9200

C_BOLD=$'\033[1m'; C_DIM=$'\033[2m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_RESET=$'\033[0m'
step()  { printf '\n%s== %s ==%s\n' "$C_BOLD" "$1" "$C_RESET"; }
info()  { printf '  %s%s%s\n' "$C_DIM" "$1" "$C_RESET"; }
ok()    { printf '  %s✓%s %s\n' "$C_GREEN" "$C_RESET" "$1"; }
warn()  { printf '  %s!%s %s\n' "$C_YELLOW" "$C_RESET" "$1"; }
fail()  { printf '  %s✗%s %s\n' "$C_RED" "$C_RESET" "$1"; }

# Extract a numeric field from a JSON blob without depending on jq.
json_num() { printf '%s' "$1" | sed -n 's/.*"'"$2"'":\([0-9]*\).*/\1/p' | head -1; }

step "Stopping trace ingestion"
kubectl scale deployment/opentelemetry-collector -n "$NS" --replicas=0 >/dev/null
kubectl rollout status deployment/opentelemetry-collector -n "$NS" --timeout=60s >/dev/null
ok "opentelemetry-collector scaled to 0"

step "Port-forwarding svc/opensearch -> localhost:$LOCAL_PORT"
kubectl port-forward -n "$NS" svc/opensearch "$LOCAL_PORT:9200" >/dev/null 2>&1 &
PF_PID=$!
trap 'kill "$PF_PID" 2>/dev/null' EXIT
for _ in {1..30}; do
  curl -sS -k "https://localhost:$LOCAL_PORT" >/dev/null 2>&1 && break
  sleep 1
done
ok "tunnel ready (pid $PF_PID)"

PASS=$(kubectl get secret -n "$NS" "$OS_SECRET" -o jsonpath='{.data.password}' | base64 -d)
OS() { curl -sS -k -u "admin:$PASS" -H 'Content-Type: application/json' "$@"; }

reindex() {
  local src=$1 dst=$2
  local resp
  resp=$(OS -XPOST "https://localhost:$LOCAL_PORT/_reindex?wait_for_completion=true&refresh=true" \
              -d "{\"source\":{\"index\":\"$src\"},\"dest\":{\"index\":\"$dst\"}}")
  local total created failures
  total=$(json_num "$resp" total)
  created=$(json_num "$resp" created)
  failures=$(printf '%s' "$resp" | grep -o '"failures":\[[^]]*\]' | grep -o '{' | wc -l | tr -d ' ')
  if [ "${failures:-0}" != "0" ]; then
    fail "$src -> $dst  ($failures failures)"
    printf '%s\n' "$resp"
    return 1
  fi
  ok "$src -> $dst  (${created:-0}/${total:-0} docs)"
}

delete_idx() {
  local idx=$1
  local resp
  resp=$(OS -XDELETE "https://localhost:$LOCAL_PORT/$idx")
  if printf '%s' "$resp" | grep -q '"acknowledged":true'; then
    ok "deleted $idx"
  else
    warn "delete $idx -> $resp"
  fi
}

step "Listing trace indices"
mapfile -t INDICES < <(OS "https://localhost:$LOCAL_PORT/_cat/indices/otel-traces-*?h=index" | awk '{print $1}' | grep -v -- '-reindex-tmp$')
if [ "${#INDICES[@]}" -eq 0 ]; then
  warn "no otel-traces-* indices found, nothing to do"
else
  info "${#INDICES[@]} index/indices to migrate: ${INDICES[*]}"
fi

for IDX in "${INDICES[@]}"; do
  TMP="${IDX}-reindex-tmp"
  step "Migrating $IDX"
  OS -XDELETE "https://localhost:$LOCAL_PORT/$TMP" >/dev/null
  reindex "$IDX" "$TMP" || continue
  delete_idx "$IDX"
  reindex "$TMP" "$IDX" || continue
  delete_idx "$TMP"
done

step "Closing port-forward"
kill "$PF_PID" 2>/dev/null && ok "stopped pid $PF_PID"
trap - EXIT

step "Resuming trace ingestion"
kubectl scale deployment/opentelemetry-collector -n "$NS" --replicas=1 >/dev/null
kubectl rollout status deployment/opentelemetry-collector -n "$NS" --timeout=120s >/dev/null
ok "opentelemetry-collector back to 1 replica"

printf '\n%sDone.%s\n' "$C_BOLD" "$C_RESET"
