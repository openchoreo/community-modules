#!/usr/bin/env bash
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
#
# Removes everything this module installs on top of OpenChoreo, in the reverse
# order of the README installation steps:
#
#   1. Agents, their projects, Agent Manager's deployment pipelines, and the
#      environments added for Agent Manager, through the Agent Manager API
#      while Agent Manager is still running
#   2. The default environment's Thunder and API Platform Gateway
#   3. The Agent Manager Helm releases and namespace, Agent Sandbox, and the
#      Gateway Operator
#   4. Shared configuration: the tracing collector and the OpenBao entries
#   5. The Agent Manager identities in OpenChoreo's Thunder
#
# Every step skips what is already gone, and a failed removal is reported and
# skipped, so the script can be re-run. OpenChoreo itself, its Thunder,
# OpenBao, and the observability modules are kept, as are OpenChoreo projects,
# environments, and pipelines that Agent Manager did not create.
#
# Run it from a checkout of this module, with the README's Configuration
# Variables exported. See the README's Uninstallation section for the inputs.

set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSUME_YES=false
[ "${1:-}" = "--yes" ] && ASSUME_YES=true

: "${AMP_RAW:?Export the README Configuration Variables first (AMP_RAW is not set)}"
: "${AMP_API_URL:?Export the README Configuration Variables first (AMP_API_URL is not set)}"
: "${THUNDER_PUBLIC_URL:?Export the README Configuration Variables first (THUNDER_PUBLIC_URL is not set)}"
AMP_NS="${AMP_NS:-wso2-amp}"
DATA_PLANE_NS="${DATA_PLANE_NS:-openchoreo-data-plane}"
WORKFLOW_NS="${WORKFLOW_NS:-openchoreo-workflow-plane}"
OBSERVABILITY_NS="${OBSERVABILITY_NS:-openchoreo-observability-plane}"
DEFAULT_NS="${DEFAULT_NS:-default}"
# Agent Manager's organization is its OpenChoreo namespace: it finds each
# environment's ThunderID at amp-thunder-<namespace>-<env>.
ORG_NAME="${ORG_NAME:-${DEFAULT_NS}}"
# Where OpenChoreo keeps the Secrets it pushes to OpenBao for the namespace.
KV_NS="${KV_NS:-openchoreo-kv-${DEFAULT_NS}}"
SCRIPT_BASE_URL="${AMP_RAW}/deployments/scripts"
# Shared components other modules may use. Set to true to keep them. Without
# it, they are still kept while resources of their types remain.
KEEP_AGENT_SANDBOX="${KEEP_AGENT_SANDBOX:-false}"
KEEP_GATEWAY_OPERATOR="${KEEP_GATEWAY_OPERATOR:-false}"
# The tracing module's own collector ConfigMap, restored in step 4.
TRACING_COLLECTOR_CONFIGMAP="${TRACING_COLLECTOR_CONFIGMAP:-opentelemetry-collector}"
TRACING_CHART="observability-tracing-opensearch"

# Use only tokens this script requests; an inherited one may be expired or
# lack the permissions a step needs.
unset AGENT_MANAGER_TOKEN

for tool in kubectl helm curl python3; do
  command -v "$tool" >/dev/null || { echo "❌ $tool is required" >&2; exit 1; }
done
kubectl version >/dev/null 2>&1 || { echo "❌ kubectl cannot reach the cluster" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

step() { printf '\n=== %s ===\n' "$1"; }
info() { printf '  %s\n' "$1"; }
warn() { printf '  ⚠️  %s\n' "$1"; }
release_exists() { helm status "$1" -n "$2" >/dev/null 2>&1; }
uninstall_release() {
  if ! release_exists "$1" "$2"; then
    info "Release $1 ($2) not found, skipping"
  elif helm uninstall "$1" -n "$2" --wait --timeout 10m >/dev/null; then
    info "Uninstalled $1 ($2)"
  else
    warn "Could not uninstall $1 ($2)"
  fi
}
json() { python3 -c "import json,sys; d=json.load(sys.stdin); $1"; }
# Quotes a value for a curl config file. Secrets go to curl through its config
# on stdin, so they never appear in the process list.
curl_quote() { local v="${1//\\/\\\\}"; v="${v//\"/\\\"}"; printf '"%s"' "$v"; }
# Prints the names of the resources of the given types that still exist.
# Fails when a type that exists cannot be listed, so a denied or failed request
# is never taken to mean that nothing remains.
remaining() {
  local type out
  for type in "$@"; do
    if ! out="$(kubectl get "${type}" -A --no-headers -o custom-columns=NAME:.metadata.name 2>&1)"; then
      grep -q "the server doesn't have a resource type" <<<"${out}" && continue
      return 1
    fi
    [ -n "${out}" ] && sed "s|^|${type%%.*}/|" <<<"${out}"
  done
  return 0
}
# Waits up to two minutes for the resources of the given types to be deleted,
# then prints what remains.
wait_until_unused() {
  local left=""
  for _ in $(seq 1 24); do
    if ! left="$(remaining "$@")"; then
      printf '(could not list them; check access to %s)' "$*"
      return 0
    fi
    [ -z "${left}" ] && return 0
    sleep 5
  done
  printf '%s' "${left}"
}

echo "This removes WSO2 Agent Manager, its agents, environments, and identities from"
echo "the cluster in context '$(kubectl config current-context)'. OpenChoreo is kept."
if [ "$ASSUME_YES" != true ]; then
  read -r -p "Type 'uninstall' to continue: " answer
  [ "$answer" = "uninstall" ] || { echo "Aborted."; exit 1; }
fi

if [ -z "${AMP_API_CLIENT_SECRET:-}" ]; then
  AMP_API_CLIENT_SECRET="$(kubectl get secret gateway-idp-credentials -n "${DATA_PLANE_NS}" \
    -o jsonpath='{.data.client-secret}' 2>/dev/null | base64 -d || true)"
fi
# Environments added for Agent Manager have an environment Thunder namespace or
# a gateway release named after them.
helm_releases="$(helm list -A --deployed --failed --pending -q 2>/dev/null || true)"
amp_environment() {
  kubectl get namespace "amp-thunder-${ORG_NAME}-$1" >/dev/null 2>&1 \
    || grep -qx "api-platform-${ORG_NAME}-$1" <<<"${helm_releases}"
}
# Environments that existed in OpenChoreo before Agent Manager, labelled by
# configure-environments.sh. They are kept; step 2 removes only their ThunderID
# and gateway.
ADOPTED_LABEL="amp.wso2.com/adopted"
adopted_environments="$(kubectl get environments.openchoreo.dev -n "${DEFAULT_NS}" -l "${ADOPTED_LABEL}=true" \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)"
is_adopted() { grep -qx "$1" <<<"${adopted_environments}"; }

# Agent Manager's agents: the components of its component types, as
# project/name. Other products' components are never included.
agent_components() {
  kubectl get components.openchoreo.dev -n "${DEFAULT_NS}" -o json 2>/dev/null | python3 -c '
import json, sys
for c in json.load(sys.stdin)["items"]:
    spec = c.get("spec") or {}
    t = spec.get("componentType") or {}
    if t.get("kind") == "ComponentType" and (t.get("name") or "").split("/")[-1] in ("agent-api", "external-agent-api"):
        print((spec.get("owner") or {}).get("projectName", "") + "/" + c["metadata"]["name"])' 2>/dev/null || true
}
# Read before anything is deleted: step 4 recognizes an agent's leftover
# secrets by these names.
known_agents="$(agent_components | sed 's|.*/||')"
known_environments="$(kubectl get environments.openchoreo.dev -n "${DEFAULT_NS}" -o name 2>/dev/null | sed 's|.*/||' || true)"

# --- 1. Agents, projects, pipelines, and additional environments -------------
step "1. Agents, projects, pipelines, and additional environments"
AGENT_MANAGER_TOKEN=""
agents=""
if curl -fsS -o /dev/null --max-time 10 "${AMP_API_URL}/healthz" 2>/dev/null && [ -n "${AMP_API_CLIENT_SECRET}" ]; then
  SCOPES="amp:project:read amp:project:delete amp:agent:read amp:agent:delete amp:deployment-pipeline:read"
  SCOPES="${SCOPES} amp:deployment-pipeline:update amp:deployment-pipeline:delete amp:environment:read amp:environment:delete"
  # The environment Thunder script also deletes Agent Manager's stored credential for it.
  SCOPES="${SCOPES} amp:org:manage-service-account"
  if ! AGENT_MANAGER_TOKEN="$(printf 'user = %s\n' "$(curl_quote "amp-api-client:${AMP_API_CLIENT_SECRET}")" \
    | curl -fsS --max-time 30 -K - -X POST "${THUNDER_PUBLIC_URL}/oauth2/token" \
      -d grant_type=client_credentials --data-urlencode "scope=${SCOPES}" \
    | json 'print(d["access_token"])')"; then
    AGENT_MANAGER_TOKEN=""
    warn "Could not get an Agent Manager token from ${THUNDER_PUBLIC_URL}"
  fi
fi
if [ -n "${AGENT_MANAGER_TOKEN}" ]; then
  export AGENT_MANAGER_TOKEN
  API="${AMP_API_URL}/api/v1/orgs/${ORG_NAME}"
  call() {
    printf 'header = %s\n' "$(curl_quote "Authorization: Bearer ${AGENT_MANAGER_TOKEN}")" \
      | curl -fsS --max-time 60 -K - -H "Content-Type: application/json" "$@"
  }
  # Prints every item of a paginated list as one JSON document per line. KEY is
  # the response field that holds the items, or empty for a bare array.
  list_all() {
    local path="$1" key="$2" limit="$3" offset=0 page count
    : > "${WORK}/items"
    for _ in $(seq 1 100); do
      page="$(call "${API}${path}?limit=${limit}&offset=${offset}")" || return 1
      count="$(printf '%s' "${page}" | KEY="${key}" OUT="${WORK}/items" python3 -c '
import json, os, sys
d = json.load(sys.stdin)
items = (d.get(os.environ["KEY"]) or []) if os.environ["KEY"] else d
with open(os.environ["OUT"], "a") as out:
    for item in items:
        out.write(json.dumps(item) + "\n")
print(len(items))')" || return 1
      [ "${count}" -lt "${limit}" ] && break
      offset=$((offset + limit))
    done
    cat "${WORK}/items"
  }

  # Agents first: deleting one removes its deployments in every environment.
  # Agent Manager v1.0.0 lists every component of a project as an agent,
  # including other products' components in a shared project, so take the
  # agents from OpenChoreo: the components of Agent Manager's component types.
  agents="$(agent_components)"
  for agent in ${agents}; do
    deleted=false code=""
    for attempt in 1 2 3 4 5 6; do
      code="$(printf 'header = %s\n' "$(curl_quote "Authorization: Bearer ${AGENT_MANAGER_TOKEN}")" \
        | curl -sS --max-time 60 -K - -o "${WORK}/response" -w '%{http_code}' -X DELETE \
          "${API}/projects/${agent%%/*}/agents/${agent##*/}")" || true
      case "${code}" in
        2??|404) deleted=true; break ;;
        # A build or other operation still holds the agent; it usually clears quickly.
        409) [ "${attempt}" -lt 6 ] && sleep 10 ;;
        # Agent Manager v1.0.0 answers its first call after its cached OpenChoreo
        # token expires with HTTP 401, then fetches a new token.
        401) [ "${attempt}" -lt 6 ] && sleep 3 ;;
        *) break ;;
      esac
    done
    if [ "${deleted}" = true ]; then
      info "Deleted agent ${agent}"
    else
      warn "Could not delete agent ${agent} (HTTP ${code:-none}): $(head -c 300 "${WORK}/response" 2>/dev/null || true)"
      warn "Step 3 still removes its workloads; delete it from the Console first to remove it cleanly."
    fi
  done
  if [ -n "${agents}" ]; then
    info "Waiting for the agent components to be removed..."
    for _ in $(seq 1 60); do
      pending=false
      for agent in ${agents}; do
        kubectl get components.openchoreo.dev -n "${DEFAULT_NS}" "${agent##*/}" >/dev/null 2>&1 && pending=true
      done
      [ "${pending}" = false ] && break
      sleep 5
    done
  fi

  # Projects that held agents. The default project belongs to the platform
  # resources release (step 3); other projects are reported and kept.
  agent_projects="$(printf '%s\n' ${agents} | sed -n 's|/.*||p' | sort -u | grep -vx default || true)"
  if projects="$(list_all /projects projects 50 | python3 -c '
import json, sys
print("\n".join(json.loads(line)["name"] for line in sys.stdin if line.strip()))')"; then
    for project in ${projects}; do
      [ "${project}" = "default" ] && continue
      # Deleting a project deletes all of its components, so a project that
      # also holds other products' components is kept.
      others="$(kubectl get components.openchoreo.dev -n "${DEFAULT_NS}" -o json 2>/dev/null | PROJECT="${project}" python3 -c '
import json, os, sys
print(sum(1 for c in json.load(sys.stdin)["items"]
          if ((c.get("spec") or {}).get("owner") or {}).get("projectName") == os.environ["PROJECT"]))' 2>/dev/null || echo unknown)"
      if grep -qx "${project}" <<<"${agent_projects}" && [ "${others}" != 0 ]; then
        info "Keeping project ${project}: it also holds components that are not Agent Manager agents"
      elif grep -qx "${project}" <<<"${agent_projects}"; then
        if call -X DELETE "${API}/projects/${project}" -o /dev/null; then
          info "Deleted project ${project}"
        else
          warn "Could not delete project ${project}"
        fi
      else
        info "Keeping project ${project}: it held no Agent Manager agents. Delete it from the Console if you no longer need it."
      fi
    done
  else
    warn "Could not list projects; delete the projects you added from the Console"
  fi

  environments=""
  if all_environments="$(list_all /environments "" 100 | python3 -c '
import json, sys
print("\n".join(json.loads(line)["name"] for line in sys.stdin if line.strip()))')"; then
    for environment in ${all_environments}; do
      [ "${environment}" = "default" ] && continue
      if is_adopted "${environment}"; then
        info "Keeping environment ${environment}: it existed before Agent Manager (step 2 removes its ThunderID and gateway)"
      elif amp_environment "${environment}"; then
        environments="${environments} ${environment}"
      else
        info "Keeping environment ${environment}: Agent Manager did not create it"
      fi
    done
  else
    warn "Could not list environments; remove the environments you added with remove-environment.sh"
  fi

  # An environment cannot be deleted while a pipeline references it. Delete the
  # pipelines that reference Agent Manager's environments and no others, besides
  # adopted ones, and return the default pipeline to its installed state. A
  # pipeline of adopted environments only belongs to OpenChoreo and is kept.
  # The default pipeline is Agent Manager's only when its release created it.
  default_pipeline_owner="$(kubectl get deploymentpipelines.openchoreo.dev default -n "${DEFAULT_NS}" \
    -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}' 2>/dev/null || true)"
  # Likewise the default environment: an existing one that Agent Manager only
  # adopted belongs to OpenChoreo, and so do the pipelines that use it.
  default_environment_owner="$(kubectl get environments.openchoreo.dev default -n "${DEFAULT_NS}" \
    -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}' 2>/dev/null || true)"
  amp_environments="${environments}"
  [ "${default_environment_owner}" = amp-platform-resources ] && amp_environments="default ${environments}"
  if pipelines="$(list_all /deployment-pipelines deploymentPipelines 50 | AMP_ENVIRONMENTS="${amp_environments}" \
    ADOPTED_ENVIRONMENTS="${adopted_environments}" CREATED_ENVIRONMENTS="${environments}" \
    DEFAULT_OWNED="$([ "${default_pipeline_owner}" = amp-platform-resources ] && echo true || echo false)" python3 -c '
import json, os, sys
amp = set(os.environ["AMP_ENVIRONMENTS"].split())
adopted = set(os.environ["ADOPTED_ENVIRONMENTS"].split())
for line in sys.stdin:
    if not line.strip():
        continue
    p = json.loads(line)
    # References are environment names, or objects with a name.
    name = lambda ref: ref.get("name") if isinstance(ref, dict) else ref
    refs = set()
    for path in p.get("promotionPaths") or []:
        refs.add(name(path.get("sourceEnvironmentRef")))
        refs.update(name(ref) for ref in path.get("targetEnvironmentRefs") or [])
    refs.discard(None)
    if p["name"] == "default" and os.environ["DEFAULT_OWNED"] == "true":
        print("reset default")
    elif p["name"] == "default" or not (refs <= amp | adopted and refs & amp):
        # Another product'"'"'s pipeline: drop only the environments Agent Manager
        # created, which are deleted below.
        created = set(os.environ["CREATED_ENVIRONMENTS"].split())
        paths = []
        for path in p.get("promotionPaths") or []:
            source = name(path.get("sourceEnvironmentRef"))
            if source in created:
                continue
            targets = [{"name": name(ref)} for ref in path.get("targetEnvironmentRefs") or [] if name(ref) not in created]
            paths.append({"sourceEnvironmentRef": source, "targetEnvironmentRefs": targets})
        if refs & created and paths:
            body = {k: p[k] for k in ("displayName", "description") if p.get(k)}
            body["promotionPaths"] = paths
            print("strip " + p["name"] + " " + json.dumps(body, separators=(",", ":")))
        elif refs & created:
            print("emptied " + p["name"])
        else:
            print("keep " + p["name"])
    elif refs:
        print("delete " + p["name"])
    else:
        print("keep " + p["name"])')"; then
    while read -r action pipeline body; do
      case "${action}" in
        reset)
          if call -X PUT "${API}/deployment-pipelines/default" -o /dev/null \
            -d '{"promotionPaths":[{"sourceEnvironmentRef":"default","targetEnvironmentRefs":[]}]}'; then
            info "Reset the default pipeline to the default environment only"
          else
            warn "Could not reset the default pipeline"
          fi ;;
        delete)
          if call -X DELETE "${API}/deployment-pipelines/${pipeline}" -o /dev/null; then
            info "Deleted pipeline ${pipeline}"
          else
            warn "Could not delete pipeline ${pipeline}"
          fi ;;
        strip)
          if call -X PUT "${API}/deployment-pipelines/${pipeline}" -o /dev/null -d "${body}"; then
            info "Removed Agent Manager's environments from pipeline ${pipeline}"
          else
            warn "Could not remove Agent Manager's environments from pipeline ${pipeline}"
          fi ;;
        emptied) warn "Pipeline ${pipeline} uses only environments Agent Manager created, but another product may use it; remove them from it, then re-run" ;;
        keep) info "Keeping pipeline ${pipeline}: it references environments Agent Manager did not create" ;;
      esac
    done <<<"${pipelines}"
  else
    warn "Could not list deployment pipelines"
  fi

  # Additional environments: the environment, its Thunder, and its gateway.
  # GATEWAY_NAMESPACE is where the README installs every environment's gateway;
  # remove-environment.sh deletes a namespace only when it is named <org>-<env>.
  if [ -z "${environments// /}" ]; then
    info "No additional Agent Manager environments"
  elif curl -fsSL --max-time 60 "${SCRIPT_BASE_URL}/remove-environment.sh" -o "${WORK}/remove-environment.sh"; then
    for environment in ${environments}; do
      ENV_NAME="${environment}" ORG_NAME="${ORG_NAME}" GATEWAY_NAMESPACE="${DATA_PLANE_NS}" \
      AGENT_MANAGER_URL="${AMP_API_URL}" SCRIPT_BASE_URL="${SCRIPT_BASE_URL}" \
      bash "${WORK}/remove-environment.sh" || warn "Could not fully remove environment ${environment}"
    done
  else
    warn "Could not download remove-environment.sh; additional environments were not removed"
  fi
else
  info "Agent Manager API is not reachable at ${AMP_API_URL}, or no amp-api-client secret or token is available."
  info "Skipping agents, projects, pipelines, and additional environments; remove them manually if any remain."
fi

# --- 2. Default and adopted environments -------------------------------------
# Their ThunderID and gateway. The default environment itself goes with the
# platform resources release in step 3; adopted environments are kept.
step "2. Default and adopted environments"
thunder_script=""
for environment in $(printf '%s\n' default ${adopted_environments} | awk 'NF && !seen[$0]++'); do
  if kubectl get namespace "amp-thunder-${ORG_NAME}-${environment}" >/dev/null 2>&1; then
    if [ -z "${AMP_API_CLIENT_SECRET}" ]; then
      warn "No amp-api-client secret; the ${environment} environment ThunderID was not removed"
    elif [ -z "${thunder_script}" ] && ! curl -fsSL --max-time 60 "${SCRIPT_BASE_URL}/remove-environment-thunder.sh" \
        -o "${WORK}/remove-environment-thunder.sh"; then
      warn "Could not download remove-environment-thunder.sh; the ${environment} environment ThunderID was not removed"
    else
      thunder_script="${WORK}/remove-environment-thunder.sh"
      ENV_NAME="${environment}" ORG_NAME="${ORG_NAME}" SCRIPT_BASE_URL="${SCRIPT_BASE_URL}" \
      AMP_API_URL="${AMP_API_URL}/api/v1" IDP_TOKEN_URL="${THUNDER_PUBLIC_URL}/oauth2/token" \
      IDP_CLIENT_ID=amp-api-client IDP_CLIENT_SECRET="${AMP_API_CLIENT_SECRET}" \
      bash "${thunder_script}" >/dev/null 2>&1 && info "Removed the ${environment} environment ThunderID" \
        || warn "Could not fully remove the ${environment} environment ThunderID"
    fi
  else
    info "The ${environment} environment ThunderID was not found, skipping"
  fi
  uninstall_release "api-platform-${ORG_NAME}-${environment}" "${DATA_PLANE_NS}"
  if is_adopted "${environment}"; then
    # The label is what keeps a re-run from treating the environment as one
    # Agent Manager created, so it stays until both are confirmed gone.
    if kubectl get namespace "amp-thunder-${ORG_NAME}-${environment}" >/dev/null 2>&1 \
        || release_exists "api-platform-${ORG_NAME}-${environment}" "${DATA_PLANE_NS}"; then
      warn "Kept environment ${environment} and its ${ADOPTED_LABEL} label: its ThunderID or gateway is still there; re-run to finish"
    else
      kubectl label environment.openchoreo.dev "${environment}" -n "${DEFAULT_NS}" "${ADOPTED_LABEL}-" >/dev/null 2>&1 \
        && info "Kept environment ${environment}"
    fi
  fi
done
kubectl delete secret gateway-idp-credentials -n "${DATA_PLANE_NS}" --ignore-not-found >/dev/null
kubectl label namespace "${DATA_PLANE_NS}" amp.wso2.com/api-platform-gateway- >/dev/null 2>&1 || true

# --- 3. Agent Manager components ---------------------------------------------
step "3. Agent Manager components"
uninstall_release amp-evaluation-extension "${WORKFLOW_NS}"
# The evaluation chart creates workflows-<namespace> when it is missing and keeps
# it on uninstall. Remove the Helm ownership it leaves on the namespace, so the
# namespace no longer appears to belong to the removed release.
if [ "$(kubectl get namespace "workflows-${DEFAULT_NS}" \
      -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}' 2>/dev/null)" = amp-evaluation-extension ] \
    && ! helm status amp-evaluation-extension -n "${WORKFLOW_NS}" >/dev/null 2>&1; then
  kubectl annotate namespace "workflows-${DEFAULT_NS}" meta.helm.sh/release-name- meta.helm.sh/release-namespace- \
    helm.sh/resource-policy- >/dev/null 2>&1 \
    && kubectl label namespace "workflows-${DEFAULT_NS}" app.kubernetes.io/managed-by- helm.sh/chart- \
      app.kubernetes.io/version- >/dev/null 2>&1 || true
fi
# Helm never deletes hook resources. The evaluation template is a post-install
# hook, and each gateway release leaves its pre-install bootstrap RBAC behind.
kubectl delete clusterworkflowtemplate.argoproj.io amp-monitor-evaluation --ignore-not-found >/dev/null
# The platform resources chart's namespace-label hook keeps its RBAC.
kubectl delete clusterrolebinding label-default-namespace-rolebinding --ignore-not-found >/dev/null
kubectl delete clusterrole label-default-namespace-role --ignore-not-found >/dev/null
kubectl delete serviceaccount label-default-namespace-sa -n "${DEFAULT_NS}" --ignore-not-found >/dev/null
for kind in rolebinding role serviceaccount; do
  kubectl get "${kind}" -n "${DATA_PLANE_NS}" -o name 2>/dev/null \
    | grep -E "/api-platform-${ORG_NAME}-[a-z0-9-]+-bootstrap-(role|rolebinding|sa)$" \
    | xargs -r kubectl delete -n "${DATA_PLANE_NS}" --ignore-not-found >/dev/null || true
done
# Each removed gateway also leaves its bootstrap Job (a hook), the registration
# token Secret the Job writes with kubectl, and the controller TLS Secret that
# cert-manager keeps after its Certificate is gone. Skip gateways whose release
# still exists, for example after a failed environment removal.
gateway_releases="$(helm list -n "${DATA_PLANE_NS}" -q 2>/dev/null || true)"
# The Gateway Operator removes each gateway's Certificate after its release is
# gone; until then cert-manager re-issues a deleted TLS Secret. Wait for them.
for _ in $(seq 1 24); do
  kubectl get certificates.cert-manager.io -n "${DATA_PLANE_NS}" -o name 2>/dev/null \
    | grep -qE "/api-platform-${ORG_NAME}-[a-z0-9-]+-gw-gateway-controller-tls$" || break
  sleep 5
done
kubectl get job,secret -n "${DATA_PLANE_NS}" -o name 2>/dev/null \
  | grep -E "/api-platform-${ORG_NAME}-[a-z0-9-]+-(bootstrap|token|gw-gateway-controller-tls)$" \
  | while read -r resource; do
      release="$(sed -E 's#^[^/]+/##; s#-(bootstrap|token|gw-gateway-controller-tls)$##' <<<"${resource}")"
      grep -qx "${release}" <<<"${gateway_releases}" && continue
      kubectl delete -n "${DATA_PLANE_NS}" "${resource}" --ignore-not-found >/dev/null || true
    done || true
info "Deleted Helm hook and gateway leftovers (evaluation template, namespace-label RBAC, bootstrap Jobs and RBAC, token and TLS Secrets)"
# Agents that step 1 could not delete through the API. Other products'
# components are not touched.
leftover_agents="$(agent_components | sed 's|.*/||')"
if [ -n "${leftover_agents}" ]; then
  echo "${leftover_agents}" | xargs kubectl delete components.openchoreo.dev -n "${DEFAULT_NS}" \
    --ignore-not-found --timeout=5m >/dev/null 2>&1 \
    && info "Deleted $(echo "${leftover_agents}" | wc -l | tr -d ' ') agent components" \
    || warn "Some agent components are still being removed; re-run to check"
fi
# Runs of Agent Manager's build and monitor workflows. Deleting one lets
# OpenChoreo remove its workflow-plane Secrets, ExternalSecrets, and Argo runs.
amp_runs="$(kubectl get workflowruns.openchoreo.dev -n "${DEFAULT_NS}" -o json 2>/dev/null | python3 -c '
import json, sys
workflows = {"amp-docker", "amp-google-cloud-buildpacks", "amp-ballerina-buildpack", "monitor-evaluation-workflow"}
for r in json.load(sys.stdin)["items"]:
    if (r.get("spec", {}).get("workflow") or {}).get("name") in workflows:
        print(r["metadata"]["name"])' 2>/dev/null || true)"
if [ -n "${amp_runs}" ]; then
  echo "${amp_runs}" | xargs kubectl delete workflowruns.openchoreo.dev -n "${DEFAULT_NS}" \
    --ignore-not-found --timeout=3m >/dev/null 2>&1 \
    && info "Deleted $(echo "${amp_runs}" | wc -l | tr -d ' ') build and monitor runs" \
    || warn "Some build and monitor runs are still being removed; re-run to check"
fi
# Deleting a run does not always remove what it created in the workflow plane,
# and deleting an agent leaves its build runs' objects too. Remove the objects
# of Agent Manager's runs in ${DEFAULT_NS} that no longer exist; Argo deletes the
# pods and the ExternalSecrets their Secrets. A run is Agent Manager's when its
# Argo workflow uses Agent Manager's amp-* workflow templates. Other products'
# runs share the namespace, so nothing is removed unless both lookups succeed.
orphans=0
run_selector="openchoreo.dev/managed-by=workflowrun-controller,openchoreo.dev/workflowrun-namespace=${DEFAULT_NS}"
if ! live_runs="$(kubectl get workflowruns.openchoreo.dev -n "${DEFAULT_NS}" -o name 2>/dev/null | sed 's|.*/||')" \
    || ! amp_runs="$(kubectl get workflows.argoproj.io -A -o json -l "${run_selector}" 2>/dev/null | python3 -c '
import json, sys
def refs(node):
    if isinstance(node, dict):
        ref = node.get("templateRef")
        if isinstance(ref, dict) and str(ref.get("name", "")).startswith("amp-"):
            yield True
        for value in node.values():
            yield from refs(value)
    elif isinstance(node, list):
        for value in node:
            yield from refs(value)
for item in json.load(sys.stdin)["items"]:
    run = (item["metadata"].get("labels") or {}).get("openchoreo.dev/workflowrun")
    if run and any(refs(item)):
        print(run)')"; then
  warn "Could not list the workflow runs in ${DEFAULT_NS}; their workflow-plane objects were not checked"
else
  for type in externalsecrets.external-secrets.io workflows.argoproj.io secrets; do
    while read -r namespace names; do
      [ -n "${names:-}" ] || continue
      kubectl delete "${type}" -n "${namespace}" ${names} --ignore-not-found >/dev/null 2>&1 || true
      orphans=$((orphans + $(wc -w <<<"${names}")))
    done < <(kubectl get "${type}" -A -o json -l "${run_selector}" 2>/dev/null \
      | LIVE="${live_runs}" AMP_RUNS="${amp_runs}" python3 -c '
import collections, json, os, sys
live, amp = set(os.environ["LIVE"].split()), set(os.environ["AMP_RUNS"].split())
orphans = collections.defaultdict(list)
for item in json.load(sys.stdin)["items"]:
    meta = item["metadata"]
    run = meta["labels"].get("openchoreo.dev/workflowrun") or ""
    # Only the run'"'"'s own objects, which are named after it. Shared ones, such
    # as the workflow service account token, carry the label of the last run
    # that applied them.
    if run in amp and run not in live and (meta["name"] == run or meta["name"].startswith(run + "-")):
        orphans[meta["namespace"]].append(meta["name"])
for namespace, names in orphans.items():
    print(namespace, " ".join(names))' 2>/dev/null || true)
  done
fi
[ "${orphans}" -gt 0 ] && info "Deleted ${orphans} workflow-plane objects of deleted runs"
# Agent Manager grants its runtime clients OpenChoreo access at run time.
kubectl get clusterauthzrolebindings.openchoreo.dev -o json 2>/dev/null | python3 -c '
import json, re, sys
for b in json.load(sys.stdin)["items"]:
    value = ((b.get("spec") or {}).get("entitlement") or {}).get("value") or ""
    if re.match(r"amp-(publisher|scheduler)-", value):
        print(b["metadata"]["name"])' 2>/dev/null \
  | xargs -r kubectl delete clusterauthzrolebindings.openchoreo.dev --ignore-not-found >/dev/null 2>&1 || true
kubectl delete -n "${OBSERVABILITY_NS}" -f "${MODULE_DIR}/resources/amp-observer-ingress.yaml" --ignore-not-found >/dev/null
uninstall_release amp-observability-traces "${OBSERVABILITY_NS}"
uninstall_release amp-platform-resources "${DEFAULT_NS}"
uninstall_release amp "${AMP_NS}"
if kubectl get namespace "${AMP_NS}" >/dev/null 2>&1; then
  if kubectl delete namespace "${AMP_NS}" --timeout=5m >/dev/null; then
    info "Deleted namespace ${AMP_NS}"
  else
    warn "Namespace ${AMP_NS} is still terminating"
  fi
fi

# Agent Sandbox and the Gateway Operator can serve other modules. Removing
# their CRDs deletes every resource of those types, so both are kept while any
# such resource remains after Agent Manager's own are gone.
SANDBOX_TYPES="sandboxes.agents.x-k8s.io sandboxclaims.extensions.agents.x-k8s.io sandboxtemplates.extensions.agents.x-k8s.io sandboxwarmpools.extensions.agents.x-k8s.io"
if [ "${KEEP_AGENT_SANDBOX}" = true ]; then
  info "Keeping Agent Sandbox (KEEP_AGENT_SANDBOX=true)"
elif in_use="$(wait_until_unused ${SANDBOX_TYPES})" && [ -n "${in_use}" ]; then
  warn "Keeping Agent Sandbox: these sandbox resources remain: $(echo ${in_use})"
  warn "Remove them, or set KEEP_AGENT_SANDBOX=true, then re-run."
else
  uninstall_release agent-sandbox "${DATA_PLANE_NS}"
  # The chart applies the upstream controller with a Job, so Helm does not track
  # it. Remove it as the agent-sandbox module describes.
  if kubectl get namespace agent-sandbox-system >/dev/null 2>&1; then
    kubectl delete namespace agent-sandbox-system --timeout=5m >/dev/null && info "Deleted namespace agent-sandbox-system"
  fi
  kubectl delete clusterrole agent-sandbox-controller agent-sandbox-controller-extensions --ignore-not-found >/dev/null
  kubectl delete clusterrolebinding agent-sandbox-controller agent-sandbox-controller-extensions --ignore-not-found >/dev/null
  kubectl delete crd ${SANDBOX_TYPES} --ignore-not-found >/dev/null
  info "Deleted the Agent Sandbox controller, RBAC, and CRDs"
fi
GATEWAY_TYPES="$(kubectl get crd -o name 2>/dev/null | sed 's|.*/||' | grep '\.gateway\.api-platform\.wso2\.com$' || true)"
if [ "${KEEP_GATEWAY_OPERATOR}" = true ]; then
  info "Keeping Gateway Operator (KEEP_GATEWAY_OPERATOR=true)"
elif [ -n "${GATEWAY_TYPES}" ] && in_use="$(wait_until_unused ${GATEWAY_TYPES})" && [ -n "${in_use}" ]; then
  warn "Keeping the Gateway Operator: these gateway resources remain: $(echo ${in_use})"
  warn "Remove them, or set KEEP_GATEWAY_OPERATOR=true, then re-run."
else
  kubectl delete -f "${MODULE_DIR}/resources/rbac.yaml" --ignore-not-found >/dev/null
  uninstall_release gateway-operator "${DATA_PLANE_NS}"
  kubectl delete secret gateway-encryption-keys -n "${DATA_PLANE_NS}" --ignore-not-found >/dev/null
  kubectl get lease -n "${DATA_PLANE_NS}" -o name 2>/dev/null | grep '\.gateway\.api-platform\.wso2\.com$' \
    | xargs -r kubectl delete -n "${DATA_PLANE_NS}" --ignore-not-found >/dev/null || true
  # Helm never deletes a chart's CRDs.
  if [ -n "${GATEWAY_TYPES}" ]; then
    kubectl delete crd ${GATEWAY_TYPES} --ignore-not-found >/dev/null && info "Deleted the API Platform Gateway CRDs"
  fi
fi

# --- 4. Shared configuration -------------------------------------------------
step "4. Shared configuration"
if kubectl get configmap amp-opentelemetry-collector-config -n "${OBSERVABILITY_NS}" >/dev/null 2>&1; then
  # Find the tracing module release and version instead of asking for them.
  tracing_release="" tracing_version=""
  read -r tracing_release tracing_version < <(helm list -n "${OBSERVABILITY_NS}" --deployed --failed --pending -o json 2>/dev/null \
    | CHART="${TRACING_CHART}" python3 -c '
import json, os, sys
prefix = os.environ["CHART"] + "-"
for r in json.load(sys.stdin):
    if r["chart"].startswith(prefix):
        print(r["name"], r["chart"][len(prefix):]); break' || true) || true
  if [ -z "${tracing_release}" ]; then
    warn "The ${TRACING_CHART} release was not found in ${OBSERVABILITY_NS}; keeping the merged collector ConfigMap"
  elif helm upgrade "${tracing_release}" "oci://cr.openchoreo.dev/openchoreo/helm-charts/${TRACING_CHART}" \
      --version "${tracing_version}" --namespace "${OBSERVABILITY_NS}" --reuse-values \
      --set opentelemetry-collector.configMap.existingName="${TRACING_COLLECTOR_CONFIGMAP}" >/dev/null; then
    kubectl rollout restart deployment/opentelemetry-collector -n "${OBSERVABILITY_NS}" >/dev/null || true
    info "Pointed ${tracing_release} back at ${TRACING_COLLECTOR_CONFIGMAP}"
    # Delete the merged copy only once the collector no longer uses it.
    kubectl delete configmap amp-opentelemetry-collector-config -n "${OBSERVABILITY_NS}" >/dev/null
    info "Deleted the merged collector ConfigMap"
  else
    warn "Could not point ${tracing_release} back at ${TRACING_COLLECTOR_CONFIGMAP}; keeping the merged collector ConfigMap"
  fi
else
  info "Merged collector ConfigMap not found, skipping"
fi
if kubectl get pod openbao-0 -n openbao >/dev/null 2>&1; then
  # Keep secret/workflow-plane-oauth-client-secret: OpenChoreo's builds use it.
  # The token reaches the pod on stdin, so it never appears in a process list.
  bao() {
    printf '%s\n' "${BAO_TOKEN:-root}" | kubectl exec -i -n openbao openbao-0 -- \
      sh -c 'read -r BAO_TOKEN && export BAO_TOKEN && exec bao "$@"' sh "$@"
  }
  for entry in amp-publisher-client-secret amp-system-client-secret; do
    # A delete reports success for a missing entry, so check first.
    if ! check="$(bao kv metadata get "secret/${entry}" 2>&1 >/dev/null)"; then
      if printf '%s' "${check}" | grep -q 'No value found at'; then
        info "secret/${entry} not found in OpenBao, skipping"
      else
        warn "Could not read secret/${entry} from OpenBao; check BAO_TOKEN"
      fi
    elif bao kv metadata delete "secret/${entry}" >/dev/null 2>&1; then
      info "Deleted secret/${entry} from OpenBao"
    else
      warn "Could not delete secret/${entry} from OpenBao; check BAO_TOKEN"
    fi
  done
fi

# Secrets Agent Manager stored through OpenChoreo's secret management: agent API
# keys and identities, agent environment variables, MCP and LLM proxy keys, and
# monitor credentials. Each has a SecretReference in the namespace, a Secret and
# PushSecret in ${KV_NS}, and an OpenBao key. Other products keep theirs in the
# same places, so only what is shown to be Agent Manager's is deleted:
#   - SecretReferences labelled managed-by=amp-agent-manager, which Agent
#     Manager sets on every SecretReference it creates, and
#   - leftovers without a SecretReference, from deleted agents and proxies,
#     whose names follow a format only Agent Manager uses.
# Anything else that looks like Agent Manager's is kept and reported.
bao_keys=""
if kubectl get pod openbao-0 -n openbao >/dev/null 2>&1; then
  for prefix in "secret/${DEFAULT_NS}/generic" "secret/secret/${DEFAULT_NS}/generic"; do
    bao_keys="${bao_keys}$(bao kv list -format=json "${prefix}" 2>/dev/null | PREFIX="${prefix}" python3 -c '
import json, os, sys
for key in json.load(sys.stdin):
    if not key.endswith("/"):
        print(os.environ["PREFIX"] + "/" + key)' 2>/dev/null || true)"$'\n'
  done
fi
# Ownership comes from these two lists; without them nothing is deleted.
references_listed=true
labelled="$(kubectl get secretreferences.openchoreo.dev -n "${DEFAULT_NS}" -l managed-by=amp-agent-manager \
  -o name 2>/dev/null | sed 's|.*/||')" || references_listed=false
all_references="$(kubectl get secretreferences.openchoreo.dev -n "${DEFAULT_NS}" -o name 2>/dev/null | sed 's|.*/||')" \
  || references_listed=false
candidates="$( { echo "${labelled}"
  kubectl get pushsecrets.external-secrets.io -n "${KV_NS}" -o name 2>/dev/null | sed 's|.*/||'
  echo "${bao_keys}" | sed 's|.*/||'; } | sort -u)"
classified=""
if [ "${references_listed}" != true ]; then
  warn "Could not list the SecretReferences in ${DEFAULT_NS}, so Agent Manager's secrets were not removed; re-run to retry"
elif [ -f "${MODULE_DIR}/scripts/amp-secret-names.py" ]; then
  classified="$(echo "${candidates}" | LABELLED="${labelled}" REFERENCES="${all_references}" \
    AGENTS="${known_agents}" ENVIRONMENTS="${known_environments}" python3 "${MODULE_DIR}/scripts/amp-secret-names.py")"
else
  warn "scripts/amp-secret-names.py is missing, so Agent Manager's secrets were not removed; download the module tools again"
fi
amp_secrets="$(sed -n 's/^delete //p' <<<"${classified}")"
unconfirmed="$(sed -n 's/^unconfirmed //p' <<<"${classified}")"
if [ -n "${unconfirmed}" ]; then
  warn "Kept $(echo "${unconfirmed}" | wc -l | tr -d ' ') secrets that look like Agent Manager's but could not be confirmed as its own:"
  warn "  $(echo ${unconfirmed})"
fi
if [ -n "${amp_secrets}" ]; then
  # A PushSecret deletes its OpenBao key when it is deleted.
  echo "${amp_secrets}" | xargs kubectl delete secretreferences.openchoreo.dev -n "${DEFAULT_NS}" \
    --ignore-not-found >/dev/null 2>&1 || true
  echo "${amp_secrets}" | xargs kubectl delete pushsecrets.external-secrets.io -n "${KV_NS}" \
    --ignore-not-found --timeout=2m >/dev/null 2>&1 || warn "Some PushSecrets in ${KV_NS} are still being removed"
  echo "${amp_secrets}" | xargs kubectl delete secret -n "${KV_NS}" --ignore-not-found >/dev/null 2>&1 || true
  deleted_keys=0
  while read -r key; do
    [ -n "${key}" ] || continue
    grep -qx "${key##*/}" <<<"${amp_secrets}" || continue
    bao kv metadata delete "${key}" >/dev/null 2>&1 && deleted_keys=$((deleted_keys + 1))
  done <<<"${bao_keys}"
  info "Deleted $(echo "${amp_secrets}" | wc -l | tr -d ' ') Agent Manager secrets ($deleted_keys OpenBao keys)"
else
  info "No Agent Manager secrets found"
fi

# --- 5. Identities -----------------------------------------------------------
step "5. Identities"
AMP_IDENTITY_CONFIG="${AMP_IDENTITY_CONFIG:-${AMP_IDENTITY_WORK:+${AMP_IDENTITY_WORK}/configuration.yaml}}"
if [ -n "${OPENCHOREO_SYSTEM_APP_SECRET:-}" ]; then
  # The same system-scoped token IDENTITY.md requests. Always request a new one:
  # an exported token from an earlier session has usually expired. The secret
  # reaches curl on stdin, so it never appears in a process list.
  if ! THUNDER_ADMIN_TOKEN="$(printf '%s' "${OPENCHOREO_SYSTEM_APP_SECRET}" \
    | curl -fsS --max-time 30 -X POST "${THUNDER_PUBLIC_URL}/oauth2/token" \
      -d grant_type=client_credentials -d client_id=openchoreo-system-app \
      --data-urlencode client_secret@- -d scope=system \
      --data-urlencode "resource=${THUNDER_PUBLIC_URL}/mcp" | json 'print(d["access_token"])')"; then
    THUNDER_ADMIN_TOKEN=""
    warn "Could not get a Thunder administration token with OPENCHOREO_SYSTEM_APP_SECRET"
  fi
fi
export THUNDER_ADMIN_TOKEN="${THUNDER_ADMIN_TOKEN:-}"
if [ -n "${THUNDER_ADMIN_TOKEN}" ] && [ -n "${AMP_IDENTITY_CONFIG}" ] && [ -f "${AMP_IDENTITY_CONFIG}" ]; then
  python="${MODULE_DIR}/.venv/bin/python"
  [ -x "${python}" ] || { make -C "${MODULE_DIR}" .venv/bin/python >/dev/null 2>&1 || true; }
  [ -x "${python}" ] || python=python3
  args=(--config "${AMP_IDENTITY_CONFIG}" --thunder-url "${THUNDER_ADMIN_URL:-${THUNDER_PUBLIC_URL}}" --apply)
  [ -n "${OPENCHOREO_CA_FILE:-}" ] && args+=(--ca-file "${OPENCHOREO_CA_FILE}")
  [ "${THUNDER_PUBLIC_URL%%:*}" = "http" ] && args+=(--allow-http)
  "${python}" "${MODULE_DIR}/scripts/provision-thunder-identities.py" remove "${args[@]}" \
    || warn "Could not remove every identity; re-run, or follow IDENTITY.md (Remove the identities)"
else
  info "Skipped: set OPENCHOREO_SYSTEM_APP_SECRET (or THUNDER_ADMIN_TOKEN) and AMP_IDENTITY_CONFIG"
  info "(or AMP_IDENTITY_WORK) to remove the identities,"
  info "or follow IDENTITY.md (Remove the identities). A new installation requires it."
fi

# --- Summary -----------------------------------------------------------------
step "Remaining Agent Manager resources"
leftover="$(helm list -A --deployed --failed --pending -o json 2>/dev/null | json '
names=[r["namespace"]+"/"+r["name"] for r in d if r["name"].startswith(("amp", "api-platform-")) or r["name"] in ("agent-sandbox", "gateway-operator")]
print("\n".join(names))' || true)"
namespaces="$(kubectl get namespaces --no-headers -o custom-columns=NAME:.metadata.name \
  | grep -E "^(${AMP_NS}|amp-thunder-${ORG_NAME}-.*)$" || true)"
crds="$(kubectl get crd -o name | grep -E '\.gateway\.api-platform\.wso2\.com$|\.agents\.x-k8s\.io$' || true)"
[ "${KEEP_AGENT_SANDBOX}" = true ] && crds="$(echo "${crds}" | grep -v 'agents\.x-k8s\.io' || true)"
[ "${KEEP_GATEWAY_OPERATOR}" = true ] && crds="$(echo "${crds}" | grep -v 'api-platform' || true)"
[ "${KEEP_AGENT_SANDBOX}" != true ] && kubectl get namespace agent-sandbox-system >/dev/null 2>&1 \
  && namespaces="${namespaces} agent-sandbox-system"
[ -n "${leftover}" ] && info "Helm releases: $(echo ${leftover})"
[ -n "${crds}" ] && info "CRDs: $(echo ${crds})"
[ -n "${namespaces// /}" ] && info "Namespaces: $(echo ${namespaces})"
[ -z "${leftover}${namespaces// /}${crds}" ] && info "None. Agent Manager is removed."
exit 0
