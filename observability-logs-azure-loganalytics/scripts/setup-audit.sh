#!/usr/bin/env bash
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
#
# Create the OpenChoreo audit table and route audit records into it, for one AKS
# cluster. Run this before pointing the adapter at the table with
# `--set adapter.audit.workspaceId=<workspace customerId>`.
#
# This is operator-run rather than a chart Job on purpose. It needs rights to
# create tables and edit data collection rules, and anyone holding those rights
# can write records into the audit table (see the README's "What the producer
# filter does and does not guarantee"). Granting them to the module's own
# identity would defeat the isolation the table exists for.
#
# What it does:
#   1. Resolves the cluster's Log Analytics workspace from the monitoring addon.
#   2. Checks the producers' audit lines are reaching ContainerLogV2 at all.
#   3. Creates the custom table with its schema and retention.
#   4. Finds the Container Insights DCR by association, not by name convention.
#   5. Rewrites its Microsoft-ContainerLogV2 data flow into two complementary
#      flows: audit records to the audit table, everything else unchanged.
#
# Dry run by default for the DCR: nothing is written to it without --apply. The
# table in step 3 is created either way, since the rule cannot reference a table
# that does not exist.
#
# Things to know before running:
# - The DCR governs ALL log collection for the cluster, and step 5 replaces the
#   whole rule. Each run's pre-edit document is kept under its own timestamp in
#   <workdir>, and the earliest is the rollback to the original. Restore with
#     az monitor data-collection rule create -g <rg> -n <dcr> --rule-file <file>
# - A cluster on the default cost preset collects the
#   Microsoft-ContainerInsights-Group-Default group stream. Transformations
#   cannot attach to a group stream, so the script stops and tells you to switch
#   to individual streams first.
# - The Container Insights DCR is solution-managed. Manual edits have been
#   reported to be reconciled away; if the data flows disappear later, clone the
#   DCR, re-associate the cluster to the clone, and run this against that.
# - Each producer cluster needs its own run. ClusterInstance is stamped from
#   --cluster, and an unlabelled record cannot be attributed afterwards.
# - Records land in the workspace the cluster's container logs already go to,
#   because one stream reaches one workspace per DCR. Several clusters therefore
#   share one audit trail only if they share one workspace. Run against clusters
#   on separate workspaces and you get an audit table in each, which is the split
#   trail the table exists to avoid. The script prints the workspace it resolved;
#   check it matches on every run.
# - Re-run with --apply --skip-preflight after enabling high scale mode on the
#   cluster. It renames the stream to Microsoft-ContainerLogV2-HighScale, and a
#   rule still naming Microsoft-ContainerLogV2 duplicates data. The stream is
#   read off the cluster's own extension, and flows under either name are
#   replaced, so the re-run is all that is needed. --skip-preflight is required
#   because audit records no longer reach ContainerLogV2 once the flows exist,
#   which is what the preflight looks for.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TRANSFORM_SRC="$SCRIPT_DIR/audit-transform.kql"

CLUSTER=""
RESOURCE_GROUP=""
TABLE="OpenChoreoAuditLog_CL"
RETENTION_DAYS=365
TOTAL_RETENTION_DAYS=365
# Under $HOME rather than the working directory. The README invokes this as
# scripts/setup-audit.sh from the module directory, so a relative default would
# write the exported rules into a checkout, and those carry subscription,
# workspace and resource group IDs. It is not a temporary directory either: the
# pre-edit rules kept here are the rollback for a destructive edit.
WORKDIR="$HOME/oc-audit-azure"
APPLY=false
SKIP_PREFLIGHT=false

# namespace:container, one per producer. Namespace and container are paired: two
# separate lists would admit any container named `observer` in either namespace.
PRODUCERS=(
  "openchoreo-control-plane:api-server"
  "openchoreo-observability-plane:observer"
)

usage() {
  cat <<EOF
Usage: $(basename "$0") --cluster <aks-name> --resource-group <rg> [options]

  --cluster NAME           AKS cluster name. Also stamped as ClusterInstance.
  --resource-group NAME    Resource group holding the cluster.
  --table NAME             Audit table. Default: $TABLE
  --retention-days N       Interactive retention, 4-730. Default: $RETENTION_DAYS
  --total-retention-days N Total retention. Default: $TOTAL_RETENTION_DAYS
  --producer NS:CONTAINER  Replace the producer allowlist. Repeatable.
  --workdir DIR            Where the DCR documents are written. Default: $WORKDIR
  --apply                  Write the DCR. Without it, the plan is printed only.
  --skip-preflight         Skip the ContainerLogV2 check.
EOF
}

producers_given=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --cluster) CLUSTER="$2"; shift 2 ;;
    --resource-group) RESOURCE_GROUP="$2"; shift 2 ;;
    --table) TABLE="$2"; shift 2 ;;
    --retention-days) RETENTION_DAYS="$2"; shift 2 ;;
    --total-retention-days) TOTAL_RETENTION_DAYS="$2"; shift 2 ;;
    --workdir) WORKDIR="$2"; shift 2 ;;
    --apply) APPLY=true; shift ;;
    --skip-preflight) SKIP_PREFLIGHT=true; shift ;;
    --producer)
      if [[ "$producers_given" == false ]]; then PRODUCERS=(); producers_given=true; fi
      PRODUCERS+=("$2"); shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "$CLUSTER" && -n "$RESOURCE_GROUP" ]] || { usage >&2; exit 2; }
[[ -f "$TRANSFORM_SRC" ]] || { echo "missing $TRANSFORM_SRC" >&2; exit 1; }
for cmd in az jq; do
  command -v "$cmd" >/dev/null || { echo "$cmd is required" >&2; exit 1; }
done

mkdir -p "$WORKDIR"

say() { printf '\n== %s\n' "$*"; }

# The audit predicate and its complement are generated from one list. Written by
# hand they drift, and a mismatch means records stored twice or lost entirely.
# Transformations have no not(), so the complement is De Morgan: each pair's
# `and` becomes `or`, and the clauses join with `and`.
audit_pairs=""
other_pairs=""
for p in "${PRODUCERS[@]}"; do
  ns="${p%%:*}"
  ct="${p##*:}"
  if [[ "$ns" == "$p" || -z "$ct" ]]; then
    echo "producer must be NAMESPACE:CONTAINER, got '$p'" >&2
    exit 2
  fi
  if [[ -n "$audit_pairs" ]]; then
    audit_pairs+=" or "
    other_pairs+=" and "
  fi
  audit_pairs+="(PodNamespace == '$ns' and ContainerName == '$ct')"
  other_pairs+="(PodNamespace != '$ns' or ContainerName != '$ct')"
done

MARKER="tostring(LogMessage.msg) == 'AUDIT-LOG'"
AUDIT_PRED="($audit_pairs) and $MARKER"
OTHER_PRED="($other_pairs) or tostring(LogMessage.msg) != 'AUDIT-LOG'"

say "Resolving the workspace"
WS_ARM=$(az aks show -g "$RESOURCE_GROUP" -n "$CLUSTER" \
  --query "addonProfiles.omsagent.config.logAnalyticsWorkspaceResourceID" -o tsv)
[[ -n "$WS_ARM" && "$WS_ARM" != "null" ]] || {
  echo "the cluster has no Container Insights workspace; enable monitoring first" >&2
  exit 1
}
WS_RG=$(cut -d/ -f5 <<<"$WS_ARM")
WS=$(cut -d/ -f9 <<<"$WS_ARM")
WS_GUID=$(az monitor log-analytics workspace show -g "$WS_RG" -n "$WS" --query customerId -o tsv)
echo "workspace $WS (rg $WS_RG) customerId $WS_GUID"

if [[ "$SKIP_PREFLIGHT" == false ]]; then
  say "Checking the producers' audit lines reach ContainerLogV2"
  # Nothing here means the lines are dropped on the node by
  # container-azm-ms-agentconfig, which no DCR edit can recover.
  # stderr goes to a file rather than into the capture: this is a preview
  # command and several CLI versions print an experimental-command warning, which
  # merged into the output would make the count compare unequal to 0 and pass the
  # check on a cluster publishing nothing.
  preflight_err=$(mktemp)
  if ! found=$(az monitor log-analytics query --workspace "$WS_GUID" --analytics-query "
ContainerLogV2
| where TimeGenerated > ago(1h)
| where $AUDIT_PRED
| summarize c = count()
| project c" -o tsv 2>"$preflight_err"); then
    echo "the preflight query failed, so audit publishing was not checked:" >&2
    cat "$preflight_err" >&2
    rm -f "$preflight_err"
    echo "Re-run with --skip-preflight to continue anyway." >&2
    exit 1
  fi
  rm -f "$preflight_err"
  echo "matching records in the last hour: ${found:-0}"
  if [[ "${found:-0}" == "0" ]]; then
    echo "none found. Check audit publishing is on, and that the producer" >&2
    echo "namespaces are not excluded in container-azm-ms-agentconfig." >&2
    echo >&2
    echo "On a re-run against a cluster whose flows are already in place this is" >&2
    echo "expected: audit records go to the audit table now, not ContainerLogV2." >&2
    echo "Re-run with --skip-preflight to continue anyway." >&2
    exit 1
  fi
fi

say "Creating table $TABLE"
az monitor log-analytics workspace table create \
  --resource-group "$WS_RG" --workspace-name "$WS" \
  --name "$TABLE" \
  --plan Analytics \
  --retention-time "$RETENTION_DAYS" --total-retention-time "$TOTAL_RETENTION_DAYS" \
  --columns \
    TimeGenerated=datetime EventTime=datetime CollectedTime=datetime \
    SchemaVersion=string EventId=string Action=string Category=string Result=string \
    RequestId=string SourceIP=string UserAgent=string Producer=string \
    Surface=string OperationId=string \
    ActorType=string ActorId=string ActorIssuer=string ActorSessionId=string \
    ActorEntitlements=dynamic \
    HttpMethod=string HttpPath=string \
    ResourceType=string ResourceNamespace=string ResourceEnvironment=string \
    ResourceProject=string ResourceComponent=string ResourceResource=string \
    ResourceUid=string ResourceName=string ResourceMetadata=dynamic \
    Metadata=dynamic \
    CollectorNamespace=string CollectorPod=string CollectorContainer=string \
    ClusterInstance=string RawRecord=string \
  --output none
echo "$TABLE ready"

say "Finding the Container Insights DCR"
# By association: the region in an MSCI name follows the workspace, not the
# cluster, so the name convention misleads in a cross-region setup.
AKS_ID=$(az aks show -g "$RESOURCE_GROUP" -n "$CLUSTER" --query id -o tsv)
DCR_ID=$(az monitor data-collection rule association list-by-resource --resource "$AKS_ID" \
  --query "[?contains(dataCollectionRuleId, 'MSCI-')].dataCollectionRuleId | [0]" -o tsv)
[[ -n "$DCR_ID" && "$DCR_ID" != "null" ]] || {
  echo "no MSCI data collection rule is associated with the cluster" >&2
  exit 1
}
DCR_RG=$(cut -d/ -f5 <<<"$DCR_ID")
DCR=$(basename "$DCR_ID")
echo "$DCR (rg $DCR_RG)"

say "Exporting the rule"
az monitor data-collection rule show --ids "$DCR_ID" > "$WORKDIR/dcr.raw.json"
# `show` returns properties flattened to the top level while --rule-file expects
# them nested, so normalise before doing anything else. Read-only fields are
# dropped here too.
jq '{location: (.location // .properties.location),
     kind: (.kind // .properties.kind),
     properties: (.properties //
       del(.id, .name, .type, .etag, .systemData, .location, .kind,
           .tags, .identity, .provisioningState, .immutableId, .resourceGroup))}
    | with_entries(select(.value != null))' \
  "$WORKDIR/dcr.raw.json" > "$WORKDIR/dcr.backup.json"
# dcr.backup.json is this run's pre-edit rule, and the patch is built from it so
# that unrelated changes survive. On a second run it is therefore the already
# patched rule, not the pristine one, so every run is also kept under its own
# timestamp. The earliest of those is the rollback to the original.
stamp=$(date -u +%Y%m%dT%H%M%SZ)
cp "$WORKDIR/dcr.backup.json" "$WORKDIR/dcr.backup.$stamp.json"
echo "backup: $WORKDIR/dcr.backup.json, plus a timestamped copy"

if jq -e '[.properties.dataSources.extensions[]?.streams[]?]
          | index("Microsoft-ContainerInsights-Group-Default")' \
     "$WORKDIR/dcr.backup.json" >/dev/null; then
  cat >&2 <<EOF

The rule collects Microsoft-ContainerInsights-Group-Default. Transformations
cannot attach to a group stream. Replace that entry in
$WORKDIR/dcr.backup.json with the individual streams you collect (at least
Microsoft-ContainerLogV2, plus Microsoft-KubeEvents if you use the events
endpoint), apply it, then run this again.
EOF
  exit 1
fi

say "Building the data flows"
DEST=$(jq -r '.properties.destinations.logAnalytics[0].name' "$WORKDIR/dcr.backup.json")
[[ -n "$DEST" && "$DEST" != "null" ]] || { echo "the rule has no Log Analytics destination" >&2; exit 1; }

# High scale mode renames the container log stream to
# Microsoft-ContainerLogV2-HighScale. A data flow on the old name under high
# scale duplicates data rather than failing, so the name is read from the rule
# instead of assumed. A half-finished migration can list both, in which case the
# high scale name wins: that is the one the agent emits once the mode is on.
STREAM=$(jq -r '[.properties.dataSources.extensions[]?.streams[]?] as $s
  | if ($s | index("Microsoft-ContainerLogV2-HighScale")) then
      "Microsoft-ContainerLogV2-HighScale"
    elif ($s | index("Microsoft-ContainerLogV2")) then
      "Microsoft-ContainerLogV2"
    else empty end' "$WORKDIR/dcr.backup.json")
[[ -n "$STREAM" ]] || {
  echo "the rule collects no container log stream; is Container Insights enabled?" >&2
  exit 1
}
echo "container log stream: $STREAM"

# The // header documents the file, not the rule, and is dropped rather than
# stored in every cluster's DCR.
sed -e '/^\/\//d' -e "s/<CLUSTER_INSTANCE>/$CLUSTER/" "$TRANSFORM_SRC" \
  > "$WORKDIR/transform.kql"
grep -q "ClusterInstance = '$CLUSTER'" "$WORKDIR/transform.kql" || {
  echo "the ClusterInstance placeholder was not substituted" >&2
  exit 1
}

# Both container log stream names are subtracted, not just the one in use. After
# high scale mode renames the stream, an earlier run's flows still name
# Microsoft-ContainerLogV2 and would otherwise survive beside the new ones, which
# is the duplication the rename is supposed to avoid.
#
# Flows are subtracted from rather than dropped because a Container Insights DCR
# commonly lists every stream in one flow, and removing the entry would silently
# stop collecting KubeEvents with it.
jq --arg audit "$AUDIT_PRED" --arg other "$OTHER_PRED" --arg ws "$DEST" \
   --arg out "Custom-$TABLE" --arg stream "$STREAM" \
   --rawfile kql "$WORKDIR/transform.kql" '
  .properties.dataFlows = (
    [.properties.dataFlows[]
      | .streams -= ["Microsoft-ContainerLogV2", "Microsoft-ContainerLogV2-HighScale"]
      | select(.streams | length > 0)]
    + [
      { streams: [$stream], destinations: [$ws],
        transformKql: ("source | where " + $other) },
      { streams: [$stream], destinations: [$ws],
        transformKql: ($kql | sub("<AUDIT>"; $audit)),
        outputStream: $out }
    ])' "$WORKDIR/dcr.backup.json" > "$WORKDIR/dcr-patched.json"

echo
echo "Data flows after the edit:"
jq -r '.properties.dataFlows[]
       | "  \(.streams | join(",")) -> \(.outputStream // "ContainerLogV2")"' \
  "$WORKDIR/dcr-patched.json"

if [[ "$APPLY" == false ]]; then
  cat <<EOF

Dry run. Nothing was written to $DCR.
Review $WORKDIR/dcr-patched.json, then re-run with --apply.
EOF
  exit 0
fi

say "Applying"
az monitor data-collection rule create \
  --resource-group "$DCR_RG" --name "$DCR" --rule-file "$WORKDIR/dcr-patched.json" \
  --output none

cat <<EOF

Done. Point the adapter at the table:

  --set adapter.audit.workspaceId=$WS_GUID
  --set adapter.audit.table=$TABLE

Then confirm the split, allowing a few minutes for records to arrive. The second
query must return zero; anything else means the two filters disagree and records
are being stored and billed twice. It matches the whole producer predicate
rather than the marker alone, because a pod outside the producer namespaces
printing the marker is sent to ContainerLogV2 deliberately, and counting those
would report drift that is not there.

The window is wide because TimeGenerated carries event_time while a record is
queryable only once ingestion completes, so a tight ago() reads empty on a
healthy pipeline.

  az monitor log-analytics query --workspace $WS_GUID --analytics-query "
  $TABLE | where TimeGenerated > ago(2h) | count" -o table

  az monitor log-analytics query --workspace $WS_GUID --analytics-query "
  ContainerLogV2 | where TimeGenerated > ago(2h) | where $AUDIT_PRED | count" -o table

An empty ActorId or Action with a populated RawRecord means a field path in
audit-transform.kql is wrong, and RawRecord shows the shape actually arriving.
EOF
