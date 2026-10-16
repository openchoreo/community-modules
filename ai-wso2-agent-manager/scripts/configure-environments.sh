#!/usr/bin/env bash
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
#
# Gives every OpenChoreo environment in ${DEFAULT_NS} what Agent Manager needs
# to run agents there, in this order:
#
#   1. An environment ThunderID, which issues agent identities and OAuth tokens
#   2. An API Platform Gateway registered for the environment, with that
#      ThunderID as its key manager and identity provider
#
# This covers the default environment that the platform resources release
# creates and any environment that already existed in OpenChoreo. Whatever is
# already in place is left as it is, so the script can be re-run, for example
# after creating another environment. Environments that Agent Manager did not
# create are labelled amp.wso2.com/adopted=true, so uninstall.sh removes only
# their ThunderID and gateway and keeps the environment.
#
# Run it from the module tools folder, with the README's Configuration
# Variables exported. Set ENVIRONMENTS to a space-separated list to configure
# only those environments. Set GATEWAY_SCHEME=http to register each gateway at
# http://<host>:${AGENTS_HTTP_PORT} instead of https://<host>. That helps only
# where the gateway's HTTP port is 19080, as on OpenChoreo's local k3d setup: an
# agent's sandbox allows ports 443 and 80 only to public addresses, and port
# 19080 to any address. Agent Manager keeps the address from the first
# registration.

set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for name in AMP_RAW AMP_VERSION HELM_CHART_REGISTRY AMP_API_URL THUNDER_PUBLIC_URL THUNDER_INTERNAL_URL \
            CP_BASE_DOMAIN DP_DOMAIN; do
  [ -n "${!name:-}" ] || { echo "❌ Export the README Configuration Variables first (${name} is not set)" >&2; exit 1; }
done
AMP_NS="${AMP_NS:-wso2-amp}"
DATA_PLANE_NS="${DATA_PLANE_NS:-openchoreo-data-plane}"
DEFAULT_NS="${DEFAULT_NS:-default}"
# Agent Manager's organization is its OpenChoreo namespace: it finds each
# environment's ThunderID at amp-thunder-<namespace>-<env>.
ORG_NAME="${ORG_NAME:-${DEFAULT_NS}}"
ENVIRONMENTS="${ENVIRONMENTS:-}"
GATEWAY_SCHEME="${GATEWAY_SCHEME:-https}"
AGENTS_HTTP_PORT="${AGENTS_HTTP_PORT:-80}"
if [ "${GATEWAY_SCHEME}" = http ] && [ "${AGENTS_HTTP_PORT}" != 19080 ]; then
  echo "⚠️  GATEWAY_SCHEME=http registers gateways on port ${AGENTS_HTTP_PORT}. Agent sandboxes allow ports 80 and" >&2
  echo "   443 only to public addresses, so this helps only where the gateway's HTTP port is 19080." >&2
fi
THUNDER_CHART_VERSION="${THUNDER_CHART_VERSION:-1.0.0}"
PLATFORM_THUNDER_JWKS_URL="${PLATFORM_THUNDER_JWKS_URL:-${THUNDER_PUBLIC_URL}/oauth2/jwks}"
VALUES_FILE="${MODULE_DIR}/values/api-platform-gateway-extension.yaml"
ADOPTED_LABEL="amp.wso2.com/adopted"

for tool in kubectl helm curl python3; do
  command -v "${tool}" >/dev/null || { echo "❌ ${tool} is required" >&2; exit 1; }
done
[ -f "${VALUES_FILE}" ] || { echo "❌ ${VALUES_FILE} is missing; download the module tools first" >&2; exit 1; }

step() { printf '\n=== %s ===\n' "$1"; }
info() { printf '  %s\n' "$1"; }
warn() { printf '  ⚠️  %s\n' "$1"; }

# The environment ThunderIDs are served the same way as OpenChoreo's Thunder
# and trust the CA that signed its certificate.
if [ "${THUNDER_PUBLIC_URL%%:*}" = "https" ]; then TLS_ENABLED=true; else TLS_ENABLED=false; fi
if [ -n "${OPENCHOREO_CA_FILE:-}" ]; then
  PLATFORM_THUNDER_CA_PEM="$(cat "${OPENCHOREO_CA_FILE}")" SKIP_CA_BUNDLE_TRUST=false
else
  PLATFORM_THUNDER_CA_PEM="" SKIP_CA_BUNDLE_TRUST=true
fi

# The gateway bootstrap and the ThunderID registration authenticate to the
# Agent Manager API as amp-api-client; Step 9 stores its secret in the cluster.
if [ -z "${AMP_API_CLIENT_SECRET:-}" ]; then
  AMP_API_CLIENT_SECRET="$(kubectl get secret gateway-idp-credentials -n "${DATA_PLANE_NS}" \
    -o jsonpath='{.data.client-secret}' 2>/dev/null | base64 -d || true)"
fi
[ -n "${AMP_API_CLIENT_SECRET}" ] || {
  echo "❌ The gateway-idp-credentials Secret is missing in ${DATA_PLANE_NS}; complete Step 9 first" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
curl -fsSL "${AMP_RAW}/deployments/scripts/add-environment-thunder.sh" -o "${WORK}/add-environment-thunder.sh"

# Environments to configure, with whether the platform resources release owns them.
environments="$(kubectl get environments.openchoreo.dev -n "${DEFAULT_NS}" -o json | ONLY="${ENVIRONMENTS}" python3 -c '
import json, os, sys
only = set(os.environ["ONLY"].split())
for env in json.load(sys.stdin)["items"]:
    meta = env["metadata"]
    if only and meta["name"] not in only:
        continue
    annotations = meta.get("annotations") or {}
    owned = annotations.get("meta.helm.sh/release-name") == "amp-platform-resources"
    display = annotations.get("openchoreo.dev/display-name") or meta["name"]
    print(meta["name"], "amp" if owned else "adopted", display, sep="\t")')"
[ -n "${environments}" ] || { echo "❌ No environments found in ${DEFAULT_NS}${ENVIRONMENTS:+ named ${ENVIRONMENTS}}" >&2; exit 1; }

summary=""
while IFS=$'\t' read -r env origin display_name; do
  step "Environment ${env}"
  thunder_release="amp-thunder-${ORG_NAME}-${env}"
  gateway_release="api-platform-${ORG_NAME}-${env}"
  # OpenChoreo's own host pattern for the namespace. Components in the same
  # environment and namespace share the host, each under its own path prefix.
  gateway_host="${env}-${DEFAULT_NS}.${DP_DOMAIN}"
  case "${GATEWAY_SCHEME}" in
    http) gateway_url="http://${gateway_host}$([ "${AGENTS_HTTP_PORT}" = 80 ] || echo ":${AGENTS_HTTP_PORT}")" ;;
    *) gateway_url="https://${gateway_host}" ;;
  esac
  if [ "${TLS_ENABLED}" = true ]; then
    thunder_issuer="https://${env}-idp.${CP_BASE_DOMAIN}"
  else
    thunder_issuer="http://${env}-idp.${CP_BASE_DOMAIN}:8080"
  fi
  thunder_jwks="http://${thunder_release}-service.${thunder_release}.svc.cluster.local:8090/oauth2/jwks"

  if [ "${origin}" = adopted ]; then
    kubectl label environment.openchoreo.dev "${env}" -n "${DEFAULT_NS}" "${ADOPTED_LABEL}=true" --overwrite >/dev/null
    info "Existing OpenChoreo environment; labelled ${ADOPTED_LABEL}=true"
  fi

  # 1. Environment ThunderID
  if [ "$(helm status "${thunder_release}" -n "${thunder_release}" -o json 2>/dev/null \
        | python3 -c 'import json,sys; print(json.load(sys.stdin)["info"]["status"])' 2>/dev/null)" = deployed ]; then
    info "ThunderID ${thunder_release} is installed, skipping"
  else
    info "Installing ThunderID ${thunder_release}..."
    # Agent Manager v1.0.0 answers its first call after its cached OpenChoreo
    # token expires with HTTP 401, then recovers. The script calls Agent Manager
    # before it installs anything, so a second attempt is safe.
    for attempt in 1 2; do
      ENV_NAME="${env}" DISPLAY_NAME="${display_name}" ORG_NAME="${ORG_NAME}" THUNDER_HANDLE="${env}-idp" \
      WAIT_TIMEOUT=300s CHART_VERSION="${THUNDER_CHART_VERSION}" SCRIPT_BASE_URL="${AMP_RAW}/deployments/scripts" \
      AMP_API_URL="${AMP_API_URL}/api/v1" IDP_TOKEN_URL="${THUNDER_PUBLIC_URL}/oauth2/token" \
      IDP_CLIENT_ID=amp-api-client IDP_CLIENT_SECRET="${AMP_API_CLIENT_SECRET}" AGENT_MANAGER_TOKEN="" \
      PLATFORM_THUNDER_ISSUER="${THUNDER_PUBLIC_URL}" PLATFORM_THUNDER_JWKS_URL="${PLATFORM_THUNDER_JWKS_URL}" \
      PLATFORM_THUNDER_CA_PEM="${PLATFORM_THUNDER_CA_PEM}" SKIP_CA_BUNDLE_TRUST="${SKIP_CA_BUNDLE_TRUST}" \
      THUNDER_HOST_BASE_DOMAIN="${CP_BASE_DOMAIN}" TLS_ENABLED="${TLS_ENABLED}" \
      bash "${WORK}/add-environment-thunder.sh" > "${WORK}/thunder-${env}.log" 2>&1 && break
      [ "${attempt}" = 1 ] && grep -q "(HTTP 401)" "${WORK}/thunder-${env}.log" || { attempt=failed; break; }
      sleep 3
    done
    if [ "${attempt}" = failed ]; then
      warn "ThunderID install failed; last lines of its output:"
      tail -15 "${WORK}/thunder-${env}.log" | grep -viE "password" | sed 's/^/     /'
      summary="${summary}${env}: ThunderID failed"$'\n'
      continue
    fi
    info "ThunderID ready at ${thunder_issuer}"
  fi

  # 2. API Platform Gateway, pointed at the environment ThunderID
  current="$(helm get values "${gateway_release}" -n "${DATA_PLANE_NS}" -o json 2>/dev/null | ISSUER="${thunder_issuer}" ENV="${env}" python3 -c '
import json, os, sys
v = json.load(sys.stdin) or {}
kms = (((v.get("apiGateway") or {}).get("config") or {}).get("policyConfigurations") or {}).get("jwtauth_v1", {}).get("keymanagers") or []
idps = (v.get("bootstrap") or {}).get("identityProviders") or []
ok = ((v.get("gateway") or {}).get("environment") == os.environ["ENV"]
      and [k.get("name") for k in kms] == ["agent-manager-service", "ThunderKeyManager"]
      and kms[1].get("issuer") == os.environ["ISSUER"]
      and any(i.get("issuer") == os.environ["ISSUER"] for i in idps)
      and ((v.get("agentManager") or {}).get("idp") or {}).get("existingSecret"))
print("configured" if ok else "update")' 2>/dev/null || echo missing)"
  # Values alone do not show that the last install worked: a failed bootstrap
  # leaves them in place. Skip only a deployed release whose gateway is up.
  if [ "${current}" = configured ]; then
    release_status="$(helm status "${gateway_release}" -n "${DATA_PLANE_NS}" -o json 2>/dev/null \
      | python3 -c 'import json,sys; print(json.load(sys.stdin)["info"]["status"])' 2>/dev/null || true)"
    programmed="$(kubectl get apigateway "${gateway_release}" -n "${DATA_PLANE_NS}" \
      -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}' 2>/dev/null || true)"
    [ "${release_status}" = deployed ] && [ "${programmed}" = True ] || current=update
  fi
  if [ "${current}" = configured ]; then
    info "Gateway ${gateway_release} is configured, skipping"
  else
    info "$([ "${current}" = missing ] && echo Installing || echo Updating) gateway ${gateway_release}..."
    KM=apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers
    if ! helm upgrade --install "${gateway_release}" \
      "oci://${HELM_CHART_REGISTRY}/wso2-amp-api-platform-gateway-extension" \
      --version "${AMP_VERSION}" --namespace "${DATA_PLANE_NS}" --values "${VALUES_FILE}" \
      --set gateway.environment="${env}" \
      --set agentManager.orgName="${ORG_NAME}" \
      --set agentManager.idp.tokenUrl="${THUNDER_INTERNAL_URL}/oauth2/token" \
      --set agentManager.apiUrl="http://amp-api.${AMP_NS}.svc.cluster.local:9000/api/v1" \
      --set apiGateway.controlPlane.host="amp-api-gateway-manager.${AMP_NS}.svc.cluster.local:9243" \
      --set gateway.vhost="${gateway_url}" \
      --set gateway.hostname="${gateway_host}" \
      --set apiGateway.namespace="${DATA_PLANE_NS}" \
      --set "${KM}[0].name=agent-manager-service" \
      --set "${KM}[0].issuer=agent-manager-service" \
      --set "${KM}[0].jwks.remote.uri=http://amp-api.${AMP_NS}.svc.cluster.local:9000/auth/external/jwks.json" \
      --set "${KM}[0].jwks.remote.skipTlsVerify=true" \
      --set "${KM}[1].name=ThunderKeyManager" \
      --set "${KM}[1].issuer=${thunder_issuer}" \
      --set "${KM}[1].jwks.remote.uri=${thunder_jwks}" \
      --set "${KM}[1].jwks.remote.skipTlsVerify=false" \
      --set "bootstrap.identityProviders[0].name=ThunderKeyManager" \
      --set "bootstrap.identityProviders[0].issuer=${thunder_issuer}" \
      --set "bootstrap.identityProviders[0].jwksUri=${thunder_jwks}" \
      --set "bootstrap.identityProviders[0].skipTlsVerify=false" \
      --timeout 1800s > "${WORK}/gateway-${env}.log" 2>&1; then
      warn "Gateway install failed; last lines of its output:"
      tail -10 "${WORK}/gateway-${env}.log" | sed 's/^/     /'
      summary="${summary}${env}: gateway failed"$'\n'
      continue
    fi
    if ! kubectl wait --for=condition=complete "job/${gateway_release}-bootstrap" \
      -n "${DATA_PLANE_NS}" --timeout=300s >/dev/null 2>&1; then
      warn "The gateway bootstrap did not complete; check: kubectl logs job/${gateway_release}-bootstrap -n ${DATA_PLANE_NS}"
      summary="${summary}${env}: gateway bootstrap failed"$'\n'
      continue
    fi
  fi

  status=""
  for _ in $(seq 1 36); do
    status="$(kubectl get apigateway "${gateway_release}" -n "${DATA_PLANE_NS}" \
      -o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}' 2>/dev/null || true)"
    case "${status}" in *Programmed=True*) break ;; esac
    sleep 5
  done
  case "${status}" in
    *Programmed=True*) info "Gateway ready: ${gateway_url}"
                       summary="${summary}${env}: ready (gateway ${gateway_url}, ThunderID ${thunder_issuer})"$'\n' ;;
    *) warn "Gateway not programmed yet (${status:-no status}); check: kubectl get apigateway ${gateway_release} -n ${DATA_PLANE_NS}"
       summary="${summary}${env}: gateway not programmed yet"$'\n' ;;
  esac
done <<<"${environments}"

step "Summary"
printf '%s' "${summary}" | sed 's/^/  /'
printf '%s' "${summary}" | grep -qv ': ready' && exit 1
exit 0
