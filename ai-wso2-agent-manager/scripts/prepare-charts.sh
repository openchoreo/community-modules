#!/usr/bin/env bash
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
#
# Pulls the two Agent Manager charts that cannot be installed as published on an
# OpenChoreo that already has resources, and adapts them:
#
#   wso2-agent-manager                     uses the OpenChoreo namespace DEFAULT_NS
#                                          (default: `default`); the published chart
#                                          has no setting for it
#   wso2-amp-platform-resources-extension  creates its Environment, DeploymentPipeline,
#                                          and namespace label in DEFAULT_NS; names its
#                                          build templates amp-*, not OpenChoreo's
#                                          names; leaves out the default Environment,
#                                          Project, DeploymentPipeline, and
#                                          horizontal-pod-autoscaler trait when they
#                                          already exist, and uses those instead
#
# Install and upgrade both releases from the folder this writes. Run it again
# before every upgrade: it checks the cluster each time, and is safe to repeat.
#
# Usage: bash scripts/prepare-charts.sh [output folder, default: charts]

set -euo pipefail

AMP_VERSION="${AMP_VERSION:?Export the README Configuration Variables first (AMP_VERSION is not set)}"
HELM_CHART_REGISTRY="${HELM_CHART_REGISTRY:-ghcr.io/wso2}"
DEFAULT_NS="${DEFAULT_NS:-default}"
OUT="${1:-charts}"
CORE="${OUT}/wso2-agent-manager"
PLATFORM="${OUT}/wso2-amp-platform-resources-extension"
# The build templates OpenChoreo's getting-started resources also define.
TEMPLATES="checkout-source publish-image containerfile-build gcp-buildpacks-build ballerina-buildpack-build"

for tool in helm kubectl python3; do
  command -v "${tool}" >/dev/null || { echo "❌ ${tool} is required" >&2; exit 1; }
done
info() { printf '  %s\n' "$1"; }

mkdir -p "${OUT}"
rm -rf "${CORE}" "${PLATFORM}"
helm pull "oci://${HELM_CHART_REGISTRY}/wso2-agent-manager" --version "${AMP_VERSION}" --untar --untardir "${OUT}"
helm pull "oci://${HELM_CHART_REGISTRY}/wso2-amp-platform-resources-extension" --version "${AMP_VERSION}" \
  --untar --untardir "${OUT}"

# Existing objects that Agent Manager uses instead of its own copy, unless its
# own release created them; leaving them out of an upgrade would delete them.
# Removes the partly prepared charts, so a failed run leaves nothing to install.
abort() { rm -rf "${CORE}" "${PLATFORM}"; exit 1; }
# Prints "missing", "amp" (created by Agent Manager's platform resources
# release), or "other". Stops the script when the object cannot be read: a
# wrong answer here would drop a template that an upgrade then deletes.
object_state() {  # resource [namespace]
  local args=(get "$1" -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}/{.metadata.annotations.meta\.helm\.sh/release-namespace}')
  local out
  [ -n "${2:-}" ] && args+=(-n "$2")
  if ! out="$(kubectl "${args[@]}" 2>&1)"; then
    if grep -q "NotFound" <<<"${out}"; then echo missing; return 0; fi
    echo "❌ Could not read $1${2:+ in $2}: ${out}" >&2
    echo "   Nothing was prepared; fix cluster access and run the script again." >&2
    exit 1
  fi
  if [ "${out}" = "amp-platform-resources/${DEFAULT_NS}" ]; then echo amp; else echo other; fi
}

echo "Adapting the charts for OpenChoreo namespace '${DEFAULT_NS}':"
CORE="${CORE}" PLATFORM="${PLATFORM}" DEFAULT_NS="${DEFAULT_NS}" TEMPLATES="${TEMPLATES}" python3 - <<'EOF'
import glob, os, re, sys

core, platform, ns = os.environ["CORE"], os.environ["PLATFORM"], os.environ["DEFAULT_NS"]
templates = os.environ["TEMPLATES"].split()

def edit(path, old, new, count=1, regex=False):
    text = open(path).read()
    if regex:
        result, n = re.subn(old, new, text, flags=re.M)
    else:
        n = text.count(old)
        result = text.replace(old, new)
    if n != count:
        sys.exit(f"❌ {path}: expected {count} match(es) for {old!r}, found {n}; the chart changed")
    open(path, "w").write(result)

# The service reads its OpenChoreo namespace from OPEN_CHOREO_DEFAULT_NAMESPACE,
# default "default"; the chart neither sets it nor accepts extra variables.
edit(f"{core}/templates/agent-manager-service/configmap.yaml",
     r"^(\s*)(OPEN_CHOREO_BASE_URL: .*)$", r"\1\2" + "\n" + r"\1OPEN_CHOREO_DEFAULT_NAMESPACE: " + f'"{ns}"', regex=True)
print(f"  Agent Manager API: OpenChoreo namespace {ns}")

# Two templates hard-code `default`; the others use openchoreoNamespace.
for name in ("environment.yaml", "deployment-pipeline.yaml"):
    edit(f"{platform}/templates/{name}", "\n  namespace: default\n", "\n  namespace: {{ .Values.openchoreoNamespace }}\n")
job = f"{platform}/templates/label-default-namespace-job.yaml"
edit(job, "patch namespaces default", "patch namespaces {{ .Values.openchoreoNamespace }}")
edit(job, "kubectl label namespace default", "kubectl label namespace {{ .Values.openchoreoNamespace }}")
print(f"  Platform resources: Environment, DeploymentPipeline, and namespace label in {ns}")

# Build templates: rename each one and every templateRef to it. Step names,
# which the API reads build progress from, stay as they are.
names = "|".join(map(re.escape, templates))
for path in glob.glob(f"{platform}/templates/cluster-workflow-templates/*.yaml"):
    text = open(path).read()
    text = re.sub(r"(^kind: ClusterWorkflowTemplate\nmetadata:\n  name: )(" + names + r")$", r"\1amp-\2", text, flags=re.M)
    open(path, "w").write(text)
refs = 0
for path in glob.glob(f"{platform}/templates/**/*.yaml", recursive=True):
    text = open(path).read()
    text, n = re.subn(r"(templateRef:\n(\s+)name: )(" + names + r")$", r"\1amp-\3", text, flags=re.M)
    refs += n
    open(path, "w").write(text)
defined = 0
for path in glob.glob(f"{platform}/templates/cluster-workflow-templates/*.yaml"):
    defined += len(re.findall(r"^  name: amp-(?:" + names + r")$", open(path).read(), re.M))
if defined != len(templates) or refs == 0:
    sys.exit(f"❌ Renamed {defined} of {len(templates)} build templates and {refs} references; the chart changed")
print(f"  Build templates: renamed to amp-{', amp-'.join(templates)} ({refs} references)")
EOF

state="$(object_state clustertrait.openchoreo.dev/horizontal-pod-autoscaler)" || abort
if [ "${state}" = other ]; then
  # Agent Manager v1.0.0 never uses this trait: its agent component type
  # creates its own HorizontalPodAutoscaler.
  rm "${PLATFORM}/templates/component-traits/hpa-trait.yaml"
  info "ClusterTrait horizontal-pod-autoscaler: left out; the existing one is kept"
fi
state="$(object_state environment.openchoreo.dev/default "${DEFAULT_NS}")" || abort
if [ "${state}" = other ]; then
  rm "${PLATFORM}/templates/environment.yaml"
  info "Environment default: left out; Agent Manager uses the existing one"
fi
# OpenChoreo's getting-started resources include a default Project and
# DeploymentPipeline. Agent Manager's agents then go into that project and follow
# that pipeline, starting at its first environment.
state="$(object_state project.openchoreo.dev/default "${DEFAULT_NS}")" || abort
if [ "${state}" = other ]; then
  rm "${PLATFORM}/templates/project.yaml"
  info "Project default: left out; Agent Manager uses the existing one"
fi
state="$(object_state deploymentpipeline.openchoreo.dev/default "${DEFAULT_NS}")" || abort
if [ "${state}" = other ]; then
  rm "${PLATFORM}/templates/deployment-pipeline.yaml"
  info "DeploymentPipeline default: left out; Agent Manager uses the existing one"
fi
echo "Charts ready in ${OUT}/"
