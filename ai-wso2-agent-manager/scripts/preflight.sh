#!/usr/bin/env bash
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
#
# Checks, before anything is installed, whether Agent Manager fits the
# OpenChoreo installation in the current kubectl context, including what other
# products on it already use. It changes nothing. Each finding is one of:
#
#   FAIL  the installation would fail, or would break something already there
#   WARN  the installation works, but changes something other products share
#   OK    nothing to do
#
# It renders the charts that create cluster-wide resources, adapted as
# prepare-charts.sh adapts them, and checks every object they would create, so
# its list of names follows the charts. Run it from the module tools folder. It
# exits with 1 when any check fails.

set -uo pipefail

AMP_VERSION="${AMP_VERSION:-1.0.0}"
HELM_CHART_REGISTRY="${HELM_CHART_REGISTRY:-ghcr.io/wso2}"
CONTROL_PLANE_NS="${CONTROL_PLANE_NS:-openchoreo-control-plane}"
DATA_PLANE_NS="${DATA_PLANE_NS:-openchoreo-data-plane}"
WORKFLOW_NS="${WORKFLOW_NS:-openchoreo-workflow-plane}"
OBSERVABILITY_NS="${OBSERVABILITY_NS:-openchoreo-observability-plane}"
DEFAULT_NS="${DEFAULT_NS:-default}"
THUNDER_NAMESPACE="${THUNDER_NAMESPACE:-thunder}"
GATEWAY_OPERATOR_VERSION="${GATEWAY_OPERATOR_VERSION:-0.11.0}"
AGENT_SANDBOX_VERSION="${AGENT_SANDBOX_VERSION:-0.1.1}"
AMP_RESOURCE_IDENTIFIER="urn:wso2:amp"

failures=0 warnings=0
ok()   { printf '  OK    %s\n' "$1"; }
warn() { printf '  WARN  %s\n' "$1"; warnings=$((warnings + 1)); }
fail() { printf '  FAIL  %s\n' "$1"; failures=$((failures + 1)); }
note() { printf '        %s\n' "$1"; }
step() { printf '\n=== %s ===\n' "$1"; }

for tool in kubectl helm curl python3; do
  command -v "${tool}" >/dev/null || { echo "❌ ${tool} is required" >&2; exit 1; }
done
kubectl version >/dev/null 2>&1 || { echo "❌ kubectl cannot reach the cluster" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
helm list -A --deployed --failed --pending -o json > "${WORK}/releases.json" 2>/dev/null || echo "[]" > "${WORK}/releases.json"

echo "Checking the cluster in context '$(kubectl config current-context)'. Nothing is changed."

# --- OpenChoreo ---------------------------------------------------------------
step "OpenChoreo"
release_chart() {  # release namespace -> chart name with version, or empty
  python3 -c '
import json, sys
for r in json.load(open(sys.argv[1])):
    if r["name"] == sys.argv[2] and r["namespace"] == sys.argv[3]:
        print(r["chart"]); break' "${WORK}/releases.json" "$1" "$2"
}
for plane in "control-plane ${CONTROL_PLANE_NS}" "data-plane ${DATA_PLANE_NS}" \
             "workflow-plane ${WORKFLOW_NS}" "observability-plane ${OBSERVABILITY_NS}"; do
  namespace="${plane#* }" plane="${plane%% *}"
  chart="$(release_chart "openchoreo-${plane}" "${namespace}")"
  case "${chart}" in
    *-1.3.*) ok "OpenChoreo ${plane} ${chart##*-}" ;;
    "") fail "OpenChoreo ${plane} release openchoreo-${plane} not found in ${namespace}" ;;
    *) fail "OpenChoreo ${plane} is ${chart##*-}; this module supports OpenChoreo 1.3.x" ;;
  esac
done
thunder_chart="$(release_chart thunder "${THUNDER_NAMESPACE}")"
case "${thunder_chart}" in
  thunderid-1.0.*) ok "ThunderID ${thunder_chart##*-}" ;;
  "") fail "ThunderID release thunder not found in ${THUNDER_NAMESPACE}" ;;
  *) fail "OpenChoreo's identity provider is ${thunder_chart}; Agent Manager ${AMP_VERSION} needs ThunderID 1.0.x" ;;
esac
if kubectl get clustersecretstore default >/dev/null 2>&1; then
  provider="$(kubectl get clustersecretstore default -o jsonpath='{.spec.provider}' | python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)))' 2>/dev/null)"
  case "${provider}" in
    vault) ok "The default ClusterSecretStore uses a Vault-compatible store" ;;
    *) warn "The default ClusterSecretStore uses '${provider}'; Agent Manager also needs a Vault-compatible store (see README, Using a secret store other than OpenBao)" ;;
  esac
else
  fail "The default ClusterSecretStore is missing"
fi
for module in observability-logs-opensearch observability-tracing-opensearch; do
  if python3 -c '
import json, sys
sys.exit(0 if any(r["chart"].startswith(sys.argv[2] + "-") for r in json.load(open(sys.argv[1]))) else 1)' \
    "${WORK}/releases.json" "${module}"; then
    ok "Module ${module} is installed"
  else
    fail "Module ${module} is not installed"
  fi
done

# --- Data plane gateway -------------------------------------------------------
step "Data plane gateway"
gateway_ref="$(kubectl get clusterdataplane default -o jsonpath='{.spec.gateway.ingress.external.namespace}/{.spec.gateway.ingress.external.name}' 2>/dev/null)"
if [ -z "${gateway_ref#/}" ]; then
  fail "The default ClusterDataPlane has no external gateway"
else
  class="$(kubectl get gateway.gateway.networking.k8s.io "${gateway_ref#*/}" -n "${gateway_ref%%/*}" -o jsonpath='{.spec.gatewayClassName}' 2>/dev/null)"
  controller="$(kubectl get gatewayclass "${class}" -o jsonpath='{.spec.controllerName}' 2>/dev/null)"
  if [ "${controller}" = "kgateway.dev/kgateway" ] && kubectl get crd backends.gateway.kgateway.dev >/dev/null 2>&1; then
    ok "The data plane gateway ${gateway_ref} runs on kgateway"
  else
    fail "The data plane gateway ${gateway_ref} uses '${controller:-unknown}'; Agent Manager's agent APIs need kgateway"
    note "Agent Manager routes each agent through kgateway Backend resources, so another gateway leaves agent APIs unreachable."
  fi
fi

# --- Agent Manager's OpenChoreo namespace --------------------------------------
# Agent Manager v1.0.0 shows every project in its namespace, and every
# component in them, as its own agents, and can delete them.
step "Agent Manager's namespace ${DEFAULT_NS}"
if ! kubectl get namespace "${DEFAULT_NS}" >/dev/null 2>&1; then
  fail "Namespace ${DEFAULT_NS} does not exist; create it as an OpenChoreo namespace first"
else
  if [ "$(kubectl get namespace "${DEFAULT_NS}" -o jsonpath='{.metadata.labels.openchoreo\.dev/control-plane}')" = true ]; then
    ok "Namespace ${DEFAULT_NS} is an OpenChoreo namespace"
  else
    warn "Namespace ${DEFAULT_NS} is not labelled as an OpenChoreo namespace yet; Step 6 labels it"
  fi
  others="$(kubectl get components.openchoreo.dev -n "${DEFAULT_NS}" -o json 2>/dev/null | python3 -c '
import json, sys
for c in json.load(sys.stdin)["items"]:
    spec = c.get("spec") or {}
    t = spec.get("componentType") or {}
    if not (t.get("kind") == "ComponentType" and (t.get("name") or "").split("/")[-1] in ("agent-api", "external-agent-api")):
        print((spec.get("owner") or {}).get("projectName", "?") + "/" + c["metadata"]["name"])' 2>/dev/null || true)"
  if [ -n "${others}" ]; then
    warn "Namespace ${DEFAULT_NS} holds components that are not Agent Manager agents: $(echo ${others})"
    note "The Console will list them as agents, and deleting one there deletes the component. The uninstall leaves them alone."
  else
    ok "Namespace ${DEFAULT_NS} holds no other products' components"
  fi
fi

# --- Components other modules may already provide -----------------------------
step "Shared components"
shared() {  # chart-prefix expected-release expected-namespace description
  python3 -c '
import json, sys
prefix, name, ns, what = sys.argv[2:6]
found = [r for r in json.load(open(sys.argv[1])) if r["chart"].startswith(prefix + "-")]
mine = [r for r in found if r["name"] == name and r["namespace"] == ns]
others = [r for r in found if r not in mine]
for r in others:
    print("FAIL", what + " is already installed as release " + r["name"] + " in " + r["namespace"]
          + " (" + r["chart"] + "); installing Agent Manager would add a second copy")
for r in mine:
    print("OK", what + " release " + name + " is already installed by Agent Manager (" + r["chart"] + ")")
if not found:
    print("OK", f"{what} is not installed yet")' "${WORK}/releases.json" "$1" "$2" "$3" "$4"
}
while read -r level message; do
  case "${level}" in FAIL) fail "${message}" ;; OK) ok "${message}" ;; esac
done < <(shared gateway-operator gateway-operator "${DATA_PLANE_NS}" "The WSO2 API Platform Gateway Operator"
         shared agent-sandbox agent-sandbox "${DATA_PLANE_NS}" "Agent Sandbox")

# --- Resources the charts create ----------------------------------------------
step "Resources Agent Manager creates"
kubectl api-resources --no-headers 2>/dev/null > "${WORK}/api-resources"
render() {  # release namespace chart version [args...]
  local release="$1" namespace="$2" chart="$3" version="$4"; shift 4
  if helm template "${release}" "${chart}" --version "${version}" --namespace "${namespace}" --include-crds "$@" \
      > "${WORK}/${release}.yaml" 2> "${WORK}/${release}.err"; then
    echo "${release} ${namespace}" >> "${WORK}/rendered"
  else
    warn "Could not render ${chart} ${version}, so its resources were not checked: $(tail -1 "${WORK}/${release}.err")"
  fi
}
: > "${WORK}/rendered"
# The platform resources chart as Step 4 adapts it for this cluster.
if AMP_VERSION="${AMP_VERSION}" HELM_CHART_REGISTRY="${HELM_CHART_REGISTRY}" DEFAULT_NS="${DEFAULT_NS}" \
    bash "$(dirname "$0")/prepare-charts.sh" "${WORK}/charts" > "${WORK}/prepare.log" 2>&1; then
  render amp-platform-resources "${DEFAULT_NS}" "${WORK}/charts/wso2-amp-platform-resources-extension" "${AMP_VERSION}" \
    --set apiPlatformGateway.namespace="${DATA_PLANE_NS}" --set openchoreoNamespace="${DEFAULT_NS}" \
    --set global.defaultResources.namespace="${DEFAULT_NS}"
  sed -n 's/^  \(.*: left out.*\)$/\1/p' "${WORK}/prepare.log" | while read -r line; do ok "${line}"; done
else
  fail "prepare-charts.sh could not adapt the charts: $(grep -v '^ *$' "${WORK}/prepare.log" | tail -2 | tr '\n' ' ')"
fi
render amp-evaluation-extension "${WORKFLOW_NS}" "oci://${HELM_CHART_REGISTRY}/wso2-amp-evaluation-extension" "${AMP_VERSION}" \
  --set ampEvaluation.workflowNamespace="${DEFAULT_NS}"
render agent-sandbox "${DATA_PLANE_NS}" oci://cr.openchoreo.dev/openchoreo/helm-charts/agent-sandbox "${AGENT_SANDBOX_VERSION}" \
  --set namespace="${CONTROL_PLANE_NS}" --set dataPlaneNamespace="${DATA_PLANE_NS}" \
  --set dataPlaneServiceAccount=cluster-agent-dataplane
render gateway-operator "${DATA_PLANE_NS}" oci://ghcr.io/wso2/api-platform/helm-charts/gateway-operator "${GATEWAY_OPERATOR_VERSION}" \
  --set gatewayApi.installStandardCRDs=false

# One line per object that already exists: level, kind, name, and why.
while read -r release namespace; do
  WORK="${WORK}" RELEASE="${release}" NAMESPACE="${namespace}" DEFAULT_NS="${DEFAULT_NS}" python3 -c '
import json, os, re, subprocess
work, release, namespace = os.environ["WORK"], os.environ["RELEASE"], os.environ["NAMESPACE"]
scopes = {}
for line in open(os.path.join(work, "api-resources")):
    parts = line.split()
    if len(parts) >= 4:
        kind, namespaced, version = parts[-1], parts[-2] == "true", parts[-3]
        group = version.split("/")[0] if "/" in version else ""
        scopes[(kind, group)] = (parts[0] + ("." + group if group else ""), namespaced)
for doc in open(os.path.join(work, release + ".yaml")).read().split("\n---"):
    kind = re.search(r"^kind:\s*(\S+)", doc, re.M)
    version = re.search(r"^apiVersion:\s*(\S+)", doc, re.M)
    meta = re.search(r"^metadata:\s*\n((?:[ \t]+.*\n?)*)", doc, re.M)
    if not (kind and version and meta):
        continue
    if re.search(r"^\s+[\x22\x27]?helm\.sh/hook[\x22\x27]?:", meta.group(1), re.M):
        continue  # Helm deletes and recreates hook objects, so an existing one is not a conflict
    name = re.search(r"^  name:\s*\"?([^\s\"]+)", meta.group(1), re.M)
    ns = re.search(r"^  namespace:\s*\"?([^\s\"]+)", meta.group(1), re.M)
    group = version.group(1).split("/")[0] if "/" in version.group(1) else ""
    scope = scopes.get((kind.group(1), group))
    if not name or not scope:
        continue  # its API is not installed yet, so the object cannot exist
    resource, namespaced = scope
    args = ["kubectl", "get", resource, name.group(1), "-o", "json"]
    if namespaced:
        args += ["-n", ns.group(1) if ns else namespace]
    got = subprocess.run(args, capture_output=True, text=True)
    if got.returncode != 0:
        continue
    annotations = json.loads(got.stdout)["metadata"].get("annotations") or {}
    owner = annotations.get("meta.helm.sh/release-name")
    owner_ns = annotations.get("meta.helm.sh/release-namespace")
    label = kind.group(1) + "/" + name.group(1)
    if owner == release and owner_ns == namespace:
        print("OK", label, "already installed by Agent Manager")
    elif kind.group(1) == "Namespace" and release == "amp-evaluation-extension":
        print("OK", label, "exists; the evaluation chart creates it only when it is missing")
    elif kind.group(1) == "Environment" and name.group(1) == "default":
        print("OK", label, "exists; Step 6 uses it as Agent Manager'"'"'s default environment")
    elif kind.group(1) == "CustomResourceDefinition":
        print("CRD", label, release)
    else:
        print("FAIL", label, "already exists" + (f" (release {owner} in {owner_ns})" if owner else " (not created by Helm)"))
' || echo "ERROR ${release} could not be checked"
done < "${WORK}/rendered" > "${WORK}/existing"
if [ ! -s "${WORK}/existing" ]; then
  ok "None of the resources the charts create exist yet"
fi
while read -r level label reason; do
  case "${level}" in
    ERROR) fail "The resources of ${label} ${reason}; see the error above" ;;
    FAIL) fail "${label} ${reason}" ;;
    OK) ok "${label} ${reason}" ;;
  esac
done < "${WORK}/existing"
# CRDs carry no owning release. They are Agent Manager's when its own release
# of the chart that ships them is installed; otherwise another installation of
# that component created them, and Helm keeps their version.
for release in $(sed -n 's/^CRD [^ ]* //p' "${WORK}/existing" | sort -u); do
  count="$(grep -c " ${release}$" "${WORK}/existing")"
  group="$(sed -n "s|^CRD CustomResourceDefinition/[^.]*\.\(.*\) ${release}$|\1|p" "${WORK}/existing" | sort -u | tr '\n' ' ')"
  namespace="$(awk -v release="${release}" '$1 == release { print $2; exit }' "${WORK}/rendered")"
  if release_chart "${release}" "${namespace:-${DATA_PLANE_NS}}" | grep -q .; then
    ok "${count} CRDs (${group% }) belong to Agent Manager's ${release} release"
  else
    warn "${count} CRDs (${group% }) already exist from another installation; Helm keeps their current version"
  fi
done
grep -q "^FAIL" "${WORK}/existing" 2>/dev/null && \
  note "Agent Manager uses these exact names. Install it on an OpenChoreo installation where they are free (see README, Resource Conflicts)."

# Workflows that already reference the build templates Agent Manager ships:
# they would run Agent Manager's versions, and uninstalling it would delete them.
templates="$(sed -n '/^kind: ClusterWorkflowTemplate/,/^---/p' "${WORK}/amp-platform-resources.yaml" 2>/dev/null \
  | sed -n 's/^  name: *//p' | tr '\n' ' ')"
if [ -n "${templates// /}" ]; then
  users="$(for type in clusterworkflows.openchoreo.dev workflows.openchoreo.dev clusterworkflowtemplates.argoproj.io workflowtemplates.argoproj.io; do
      kubectl get "${type}" -A -o json 2>/dev/null
    done | TEMPLATES="${templates}" python3 -c '
import json, os, sys
templates = set(os.environ["TEMPLATES"].split())
decoder, text, pos = json.JSONDecoder(), sys.stdin.read(), 0
while pos < len(text):
    while pos < len(text) and text[pos].isspace():
        pos += 1
    if pos >= len(text):
        break
    doc, pos = decoder.raw_decode(text, pos)
    for item in doc.get("items", []):
        meta = item["metadata"]
        if (meta.get("annotations") or {}).get("meta.helm.sh/release-name") == "amp-platform-resources":
            continue
        serialized = json.dumps(item)
        own = meta["name"] if item["kind"] == "ClusterWorkflowTemplate" else None
        used = sorted(t for t in templates if t != own and json.dumps({"name": t})[1:-1] in serialized)
        if used:
            where = (meta.get("namespace") + "/") if meta.get("namespace") else ""
            print(item["kind"] + " " + where + meta["name"] + " uses " + ", ".join(used))')"
  if [ -n "${users}" ]; then
    while read -r line; do
      fail "${line}"
    done <<<"${users}"
    note "Agent Manager replaces these build templates with its own, and uninstalling it deletes them."
  else
    ok "No other workflow uses the build templates Agent Manager ships"
  fi
fi

# --- Environments -------------------------------------------------------------
step "Environments in ${DEFAULT_NS}"
envs="$(kubectl get environments.openchoreo.dev -n "${DEFAULT_NS}" -o json 2>/dev/null | python3 -c '
import json, sys
for e in json.load(sys.stdin)["items"]:
    ref = (e["spec"].get("dataPlaneRef") or {})
    owner = (e["metadata"].get("annotations") or {}).get("meta.helm.sh/release-name") or "-"
    print(e["metadata"]["name"], ref.get("kind", "-"), ref.get("name", "-"), owner)' 2>/dev/null)"
if [ -z "${envs}" ]; then
  ok "No environments yet; Step 6 creates the default environment"
else
  while read -r name ref_kind ref_name owner; do
    if [ "${ref_kind}/${ref_name}" != "ClusterDataPlane/default" ]; then
      fail "Environment ${name} uses ${ref_kind}/${ref_name}; Step 10 installs gateways only on the default ClusterDataPlane"
    elif [ "${owner}" = amp-platform-resources ]; then
      ok "Environment ${name} belongs to Agent Manager"
    else
      warn "Environment ${name} exists; Step 10 gives it an environment Thunder and a gateway, and uninstall keeps it"
    fi
  done <<<"${envs}"
  note "To configure only some environments in Step 10, set ENVIRONMENTS to their names."
fi

# --- Settings Agent Manager changes for everyone ------------------------------
step "Shared settings"
cp_values="$(helm get values openchoreo-control-plane -n "${CONTROL_PLANE_NS}" -a -o json 2>/dev/null || echo '{}')"
if [ "$(printf '%s' "${cp_values}" | python3 -c 'import json,sys; print(((json.load(sys.stdin).get("features") or {}).get("secretManagement") or {}).get("enabled"))' 2>/dev/null)" = True ]; then
  ok "OpenChoreo's secret API is already enabled"
else
  warn "Step 2 enables OpenChoreo's secret API with a control plane upgrade, which restarts the OpenChoreo API"
fi
# Argo mounts the ConfigMaps in its workflow defaults, such as a registry CA,
# from the namespace each build runs in.
for cm in $(kubectl get configmap argo-workflow-controller-configmap -n "${WORKFLOW_NS}" -o jsonpath='{.data.config}' 2>/dev/null \
    | python3 -c 'import re, sys; print(" ".join(sorted(set(re.findall(r"configMap:\s*\n\s*name:\s*([\w.-]+)", sys.stdin.read())))))' 2>/dev/null); do
  if ! kubectl get configmap "${cm}" -n "workflows-${DEFAULT_NS}" >/dev/null 2>&1; then
    warn "Argo's workflow defaults mount ConfigMap ${cm}, which workflows-${DEFAULT_NS} does not have; create it there before the first build (README, Step 6)"
  fi
done
warn "Step 2 points the tracing collector at a merged copy of its configuration and raises tail sampling to 500 spans per second for all traces"
note "Later upgrades of observability-tracing-opensearch must keep opentelemetry-collector.configMap.existingName."
issuer="$(printf '%s' "${cp_values}" | python3 -c 'import json,sys; print(((json.load(sys.stdin).get("security") or {}).get("oidc") or {}).get("issuer") or "")' 2>/dev/null)"
token="${THUNDER_ADMIN_TOKEN:-}"
ca_args=()
if [ -n "${issuer}" ] && [ -z "${token}" ]; then
  secret="${OPENCHOREO_SYSTEM_APP_SECRET:-$(helm get values thunder -n "${THUNDER_NAMESPACE}" -a -o json 2>/dev/null | python3 -c '
import json, re, sys
for text in ((json.load(sys.stdin).get("bootstrap") or {}).get("scripts") or {}).values():
    m = re.search(r"clientId:\s*openchoreo-system-app\s*\n\s*clientSecret:\s*\"?([^\s\"]+)", text)
    if m:
        print(m.group(1)); break' 2>/dev/null)}"
  if kubectl get secret openchoreo-ca-secret -n cert-manager >/dev/null 2>&1; then
    kubectl get secret openchoreo-ca-secret -n cert-manager -o jsonpath='{.data.ca\.crt}' | base64 -d > "${WORK}/ca.crt"
    ca_args=(--cacert "${WORK}/ca.crt")
  fi
  if [ -n "${secret}" ]; then
    token="$(printf '%s' "${secret}" | curl -fsS --max-time 20 "${ca_args[@]+"${ca_args[@]}"}" -X POST "${issuer}/oauth2/token" \
      -d grant_type=client_credentials -d client_id=openchoreo-system-app --data-urlencode client_secret@- \
      -d scope=system --data-urlencode "resource=${issuer}/mcp" 2>/dev/null \
      | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])' 2>/dev/null || true)"
  fi
fi
if [ -n "${token}" ]; then
  thunder_get() {
    printf 'header = "Authorization: Bearer %s"\n' "${token}" \
      | curl -fsS --max-time 20 "${ca_args[@]+"${ca_args[@]}"}" -K - "${issuer}$1" 2>/dev/null
  }
  default_rs="$(thunder_get /server-config/defaultResourceServer | python3 -c 'import json,sys; print((json.load(sys.stdin).get("merged") or {}).get("resourceServerId") or "")' 2>/dev/null)"
  if [ -z "${default_rs}" ]; then
    ok "Thunder has no default resource server; IDENTITY.md sets Agent Manager's"
  elif [ "$(thunder_get "/resource-servers/${default_rs}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("identifier"))' 2>/dev/null)" = "${AMP_RESOURCE_IDENTIFIER}" ]; then
    ok "Thunder's default resource server is already Agent Manager's"
  else
    fail "Thunder already has another default resource server; IDENTITY.md refuses to replace it, and Agent Manager needs it"
  fi
else
  note "Thunder's default resource server was not checked: set THUNDER_ADMIN_TOKEN, or OPENCHOREO_SYSTEM_APP_SECRET, to include it."
fi

step "Result"
if [ "${failures}" -gt 0 ]; then
  echo "  ${failures} check(s) failed and ${warnings} warning(s). Resolve the failures before installing."
  exit 1
fi
echo "  No blocking problems; ${warnings} warning(s) to review."
exit 0
