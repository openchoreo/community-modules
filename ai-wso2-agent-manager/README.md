# WSO2 Agent Manager Module for OpenChoreo

[WSO2 Agent Manager](https://github.com/wso2/agent-manager) is an open control plane for deploying, managing, and governing AI agents at scale. This module installs Agent Manager v0.10.3 on an existing OpenChoreo v1.0.0 installation and wires it into OpenChoreo's data, workflow, and observability planes.

## Table of Contents

- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Identity Provider Configuration](#identity-provider-configuration)
- [Step 1: Install Gateway Operator](#step-1-install-gateway-operator)
- [Step 2: Install Agent Manager Core](#step-2-install-agent-manager-core)
- [Step 3: Install Platform Resources](#step-3-install-platform-resources)
- [Step 4: Install Observability Extension (Optional)](#step-4-install-observability-extension-optional)
- [Step 5: Install Build Extension (Optional)](#step-5-install-build-extension-optional)
- [Step 6: Install Evaluation Extension (Optional)](#step-6-install-evaluation-extension-optional)
- [Step 7: Install AI Gateway Extension (Optional)](#step-7-install-ai-gateway-extension-optional)
- [Verification](#verification)
- [Access](#access)
- [Uninstallation](#uninstallation)

---

## Architecture

OpenChoreo planes can run as namespaces within a single cluster or as separate clusters in a multi-cluster deployment. Agent Manager components are distributed across these planes as shown below. Each plane can be a namespace or a separate cluster depending on your deployment topology.

```text
┌─── Control Plane ───────────────────────┐   ┌─── Data Plane ────────────────────────┐
│ ns: openchoreo-control-plane            │   │ ns: openchoreo-data-plane             │
│   OpenChoreo Control Plane              │   │   OpenChoreo Data Plane               │
│                                         │   │   + Gateway Operator        (Step 1)  │
│                                         │   │   + AI Gateway Extension    (Step 7)  │
│ ns: wso2-amp                            │   └───────────────────────────────────────┘
│   Agent Manager Core          (Step 2)  │
│                                         │   ┌─── Workflow Plane ─────────────────────┐
└─────────────────────────────────────────┘   │ ns: openchoreo-workflow-plane             │
                                              │   OpenChoreo Workflow Plane            │
                                              │   + Build Extension         (Step 5)   │
                                              │   + Evaluation Extension    (Step 6)   │
┌─── Observability Plane ─────────────────┐   └────────────────────────────────────────┘
│ ns: openchoreo-observability-plane      │
│   OpenChoreo Observability Plane        │
│                                         │
│   + Observability Extension   (Step 4)  │
└─────────────────────────────────────────┘
```

### Component Summary

| Component | Namespace | Cluster (multi) | Required |
|-----------|-----------|-----------------|----------|
| Gateway Operator | `openchoreo-data-plane` | Data Plane | Yes |
| Agent Manager Core | `wso2-amp` | Control Plane | Yes |
| Platform Resources | `default` | Control Plane | Yes |
| Observability Extension | `openchoreo-observability-plane` | Observability Plane | No |
| Build Extension | `openchoreo-workflow-plane` | Workflow Plane | No |
| Evaluation Extension | `openchoreo-workflow-plane` | Workflow Plane | No |
| AI Gateway Extension | `openchoreo-data-plane` | Data Plane | No |

All Helm charts are pulled from `oci://ghcr.io/wso2` at version `0.10.3` unless noted otherwise.

---

## Prerequisites

- OpenChoreo installed with control plane, data plane, workflow plane (build plane), and observability plane running
- An identity provider already configured with OpenChoreo (Thunder or external — see [Identity Provider Configuration](#identity-provider-configuration))
- OpenSearch deployed in `openchoreo-observability-plane` (installed with the `observability-tracing-opensearch` community module)
- External Secrets Operator installed and a `ClusterSecretStore` named `default` configured in each cluster that requires it
- `helm` v3.12+, `kubectl` v1.32+

### Cluster Contexts (Multi-Cluster Only)

In a multi-cluster deployment, each step in this guide must be run against the correct cluster. Set your kubectl context before running the commands in each step:

```bash
# Examples — replace with your actual context names
export CTX_CONTROL_PLANE="kubectl --context=control-plane-ctx"
export CTX_DATA_PLANE="kubectl --context=data-plane-ctx"
export CTX_WORKFLOW_PLANE="kubectl --context=workflow-plane-ctx"
export CTX_OBSERVABILITY_PLANE="kubectl --context=observability-plane-ctx"
```

Each step in this guide notes which cluster it targets. In a single-cluster deployment, all commands use the same context and these variables are not needed.

### Namespace Variables

Set these before running any commands in this guide:

```bash
export AMP_NS="wso2-amp"
export DATA_PLANE_NS="openchoreo-data-plane"
export OBSERVABILITY_NS="openchoreo-observability-plane"
export WORKFLOW_NS="openchoreo-workflow-plane"
export HELM_CHART_REGISTRY="ghcr.io/wso2"
export MODULE_RAW="https://raw.githubusercontent.com/openchoreo/community-modules/main/ai-wso2-agent-manager"

# Agent Manager version to install
export AMP_VERSION="0.10.3"
```

---

## Identity Provider Configuration

OpenChoreo ships with Thunder as its built-in identity provider, but can be configured to use any OIDC-compliant provider. Agent Manager must use the **same identity provider** already configured with your OpenChoreo installation.

Before proceeding, collect these endpoints from your IdP and keep them handy — they are referenced throughout this guide.

| Variable | Description | Thunder (default) | External IdP |
|----------|-------------|-------------------|--------------|
| `IDP_ISSUER` | Issuer claim in tokens (`iss`) | `http://thunder.<base-domain>` | Your IdP issuer URL |
| `IDP_WELL_KNOWN` | OIDC discovery endpoint | `http://thunder.<base-domain>/.well-known/openid-configuration` | `https://idp.example.com/.well-known/openid-configuration` |
| `IDP_JWKS_URL` | JWKS endpoint for token validation | `http://thunder.<base-domain>/oauth2/jwks` | `https://idp.example.com/oauth2/jwks` |
| `IDP_AUTH_URL` | Authorization endpoint (browser flows) | `http://thunder.<base-domain>/oauth2/authorize` | `https://idp.example.com/oauth2/authorize` |
| `IDP_TOKEN_URL` | Token endpoint | `http://thunder.<base-domain>/oauth2/token` | `https://idp.example.com/oauth2/token` |

> **Finding Thunder endpoints:** Replace `<base-domain>` with the base domain configured in your OpenChoreo installation (e.g. `thunder.openchoreo.example.com`). The Thunder public URL is set during OpenChoreo installation and is available in the control plane Helm values under the Thunder ingress/hostname configuration.

OAuth application registrations are covered inline at the step where each application is first needed:

- **Console SPA + API Client** — [Step 2: Install Agent Manager Core](#step-2-install-agent-manager-core)
- **AI Gateway Bootstrap Client** — [Step 7: Install AI Gateway Extension](#step-7-install-ai-gateway-extension-optional)

---

## Step 1: Install Gateway Operator

> **Cluster:** Data Plane

The WSO2 API Platform Gateway Operator manages the observability gateway that secures the OTEL trace ingestion endpoint with JWT authentication.

> **If you already have the WSO2 API Platform Gateway Operator installed** (e.g. from the `gateway-wso2-api-platform` community module), skip the `helm install` and proceed directly to [Grant RBAC](#grant-rbac-to-the-data-plane-service-account). Check with:
> ```bash
> helm list -n ${DATA_PLANE_NS} | grep operator
> ```

```bash
# Multi-cluster: switch to data plane context before running
helm install gateway-operator \
  oci://ghcr.io/wso2/api-platform/helm-charts/gateway-operator \
  --version 0.4.0 \
  --namespace ${DATA_PLANE_NS} \
  --timeout 600s \
  --set logging.level=info \
  --set gateway.helm.chartVersion="0.9.0"

kubectl wait --for=condition=Available deployment \
  -l app.kubernetes.io/name=gateway-operator \
  -n ${DATA_PLANE_NS} --timeout=300s
```

### Grant RBAC to the Data Plane Service Account

The OpenChoreo data plane service account needs permission to manage WSO2 API Platform CRDs:

```bash
kubectl apply -f ${MODULE_RAW}/resources/rbac.yaml
```

### Apply the Observability Gateway

Download `resources/obs-gateway-configmap.yaml` from this module and fill in the two required placeholders before applying:
- `<IDP_ISSUER>` — issuer claim from your identity provider
- `<IDP_JWKS_URL>` — JWKS endpoint of your identity provider

> The `agent-manager-service` key manager JWKS URL (`http://amp-api.wso2-amp.svc.cluster.local:9000/auth/external/jwks.json`) is already set in the configmap. In a multi-cluster deployment, replace it with the externally reachable address of `amp-api` after Step 2.

Then apply all gateway resources:

```bash
# Download and edit obs-gateway-configmap.yaml before applying
curl -sOL ${MODULE_RAW}/resources/obs-gateway-configmap.yaml
# Edit obs-gateway-configmap.yaml to fill in placeholders, then:
kubectl apply -f obs-gateway-configmap.yaml

kubectl apply -f ${MODULE_RAW}/resources/obs-gateway.yaml
kubectl wait --for=condition=Programmed apigateway/obs-gateway \
  -n ${DATA_PLANE_NS} --timeout=180s

kubectl patch deployment obs-gateway-gateway-gateway-runtime \
  -n ${DATA_PLANE_NS} \
  --type merge \
  -p '{"spec":{"template":{"metadata":{"labels":{"openchoreo.dev/system-component":"true"}}}}}'

curl -sOL ${MODULE_RAW}/resources/otel-rest-api.yaml
# Edit otel-rest-api.yaml: set the upstream URL to the OpenTelemetry Collector address
#   Single-cluster:  http://opentelemetry-collector.openchoreo-observability-plane.svc.cluster.local:4318
#   Multi-cluster:   http://<otel-collector-external-address>:4318
kubectl apply -f otel-rest-api.yaml
kubectl wait --for=condition=Programmed restapi/traces-api-secure \
  -n ${DATA_PLANE_NS} --timeout=120s
```

---

## Step 2: Install Agent Manager Core

> **Cluster:** Control Plane

Installs PostgreSQL, the Agent Manager API (`amp-api`), and the Agent Manager Console (`amp-console`).

### DNS and TLS Configuration

If your OpenChoreo deployment uses a custom domain with HTTPS, Agent Manager should be exposed the same way so that the IdP redirect URI and OTEL instrumentation URL work correctly from the browser.

**DNS records to create** (pointing to the ingress controller or load balancer of the control plane cluster):

| Hostname | Purpose |
|----------|---------|
| `amp.<base-domain>` | Agent Manager Console |
| `amp-api.<base-domain>` | Agent Manager API (accessed by the Console SPA from the browser) |

The obs-gateway OTEL ingest endpoint (`<obs-gateway-external-ip>:22893`) is exposed via the LoadBalancer service created in Step 1 on the data plane cluster — no additional DNS record is required unless you want a named hostname for it.

**TLS:** If OpenChoreo uses cert-manager, configure the same certificate issuer for the Agent Manager console ingress. Add the following to your local copy of `values/agent-manager.yaml`. For example, if you use Let's Encrypt:

```yaml
console:
  ingress:
    enabled: true
    hostname: "amp.<base-domain>"
    tls:
      enabled: true
      certManager:
        issuerRef:
          name: letsencrypt-prod   # match your existing cert-manager issuer name
          kind: ClusterIssuer
```

> Refer to the [Agent Manager Helm chart documentation](https://github.com/wso2/agent-manager) for the full ingress values schema.

### Register OAuth Applications

Register the following two applications in your identity provider before configuring Agent Manager.

#### Agent Manager Console (Single-Page Application)

| Field | Value |
|-------|-------|
| Application type | Public / SPA (no client secret) |
| Grant types | Authorization Code with PKCE |
| Redirect URIs | `https://amp.<base-domain>/login` |
| Scopes | `openid`, `profile`, `email` |

> **If using Thunder as your IdP:** Add `https://amp.<base-domain>` to Thunder's allowed origins. In your Thunder Helm values, append the new hostname to the existing `allowedOrigins` list and upgrade the release:
>
> ```yaml
> configuration:
>   cors:
>     allowedOrigins:
>       - "https://amp.<base-domain>"   # add this
> ```

Note the Client ID — you will need it when configuring `values/agent-manager.yaml` below.

#### Agent Manager API Client (Confidential Client)

Used by `amp-api` to authenticate with the identity provider for machine-to-machine flows.

| Field | Value |
|-------|-------|
| Application type | Confidential (client secret) |
| Grant types | Client Credentials |

Note the Client ID and Client Secret — you will need them when configuring `values/agent-manager.yaml` below.

### Configure and Install

Download `values/agent-manager.yaml`:

```bash
curl -sOL ${MODULE_RAW}/values/agent-manager.yaml
```

Retrieve the obs-gateway LoadBalancer address (created in Step 1) to use as the `instrumentationUrl`:

```bash
# Multi-cluster: run against the data plane context
OBS_GATEWAY_HOST=$(kubectl get svc obs-gateway-gateway-gateway-runtime \
  -n ${DATA_PLANE_NS} \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
if [ -z "$OBS_GATEWAY_HOST" ]; then
  OBS_GATEWAY_HOST=$(kubectl get svc obs-gateway-gateway-gateway-runtime \
    -n ${DATA_PLANE_NS} \
    -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
fi
echo "instrumentationUrl: http://${OBS_GATEWAY_HOST}:22893/otel"
```

> In multi-cluster deployments this address must be browser-reachable (not a cluster-internal address). For local testing only, use `http://localhost:22893/otel` with the port-forward from the [Access](#access) section.

Open `agent-manager.yaml` and set the following:

| Field | Value |
|-------|-------|
| `console.config.apiBaseUrl` | Public URL of the Agent Manager API, e.g. `https://amp-api.<base-domain>` — must be reachable from the browser |
| `console.config.auth.clientId` | Console SPA Client ID (registered above) |
| `console.config.auth.baseUrl` | Base URL of your IdP (e.g. `http://thunder.<base-domain>`) |
| `console.config.auth.signInRedirectURL` | `https://amp.<base-domain>/login` — must match the redirect URI registered in the IdP |
| `console.config.auth.signOutRedirectURL` | `https://amp.<base-domain>/login` |
| `console.config.instrumentationUrl` | `http://${OBS_GATEWAY_IP}:22893/otel` from the command above |
| `agentManagerService.config.oidc.tokenUrl` | `IDP_TOKEN_URL` — token endpoint of your IdP |
| `agentManagerService.config.oidc.clientId` | API Client ID (registered above) |
| `agentManagerService.config.oidc.clientSecret` | API Client Secret (registered above) |
| `agentManagerService.config.keyManager.issuer` | `IDP_ISSUER` — must match the issuer claim in tokens from your IdP |
| `agentManagerService.config.keyManager.jwksUrl` | `IDP_JWKS_URL` — JWKS endpoint of your IdP for token signature verification |
| `agentManagerService.config.keyManager.audience` | Console SPA Client ID (registered above) — AI Gateway Bootstrap Client ID will be added in Step 7 if installing that extension |
| `agentManagerService.config.publisherApiKey.value` | A unique API key shared with the Evaluation Extension — must match `ampEvaluation.publisher.apiKey` in `evaluation-extension.yaml` |

```bash
# Multi-cluster: switch to control plane context before running
helm install amp \
  oci://${HELM_CHART_REGISTRY}/wso2-agent-manager \
  --version ${AMP_VERSION} \
  --namespace ${AMP_NS} \
  --create-namespace \
  --timeout=1800s \
  -f agent-manager.yaml

kubectl wait --for=jsonpath='{.status.readyReplicas}'=1 \
  statefulset/amp-postgresql -n ${AMP_NS} --timeout=600s
kubectl wait --for=condition=Available deployment/amp-api \
  -n ${AMP_NS} --timeout=600s
kubectl wait --for=condition=Available deployment/amp-console \
  -n ${AMP_NS} --timeout=600s
```

### Expose Agent Manager via Gateway

Apply the HTTPRoutes to make the console and API accessible through the OpenChoreo gateway. Set the hostnames you created in [DNS and TLS Configuration](#dns-and-tls-configuration):

```bash
export AMP_CONSOLE_HOSTNAME="amp.<base-domain>"
export AMP_API_HOSTNAME="amp-api.<base-domain>"

curl -sL ${MODULE_RAW}/resources/httproute-amp-console.yaml | envsubst | kubectl apply -f -
curl -sL ${MODULE_RAW}/resources/httproute-amp-api.yaml | envsubst | kubectl apply -f -
```

---

## Step 3: Install Platform Resources

> **Cluster:** Control Plane

Creates a `ClusterAuthzRoleBinding` that grants the Agent Manager API client the `admin` role in OpenChoreo, enabling it to call OpenChoreo APIs. Also seeds a default Organization, Project, Environment, and Deployment Pipeline.

Set `AM_API_CLIENT_ID` to the Client ID of the Agent Manager API Client registered in [Identity Provider Configuration](#agent-manager-api-client-confidential-client):

```bash
# Multi-cluster: switch to control plane context before running
helm install amp-platform-resources \
  oci://ghcr.io/openchoreo/helm-charts/amp-platform-resources \
  --version 0.1.0 \
  --namespace default \
  --create-namespace \
  --timeout 300s \
  --set authz.ampApiClient.clientId="<AM_API_CLIENT_ID>"
```

---

## Step 4: Install Observability Extension (Optional)

> **Cluster:** Observability Plane

Deploys the `amp-traces-observer` service that serves trace data from OpenSearch to the Agent Manager console. OpenSearch must be available in `${OBSERVABILITY_NS}` before installing this extension.

### Apply OTel Collector Config

Apply the OpenTelemetry Collector ConfigMap to the observability plane. Replace `<OPENSEARCH_USERNAME>` and `<OPENSEARCH_PASSWORD>` before applying:

```bash
# Multi-cluster: switch to observability plane context before running
curl -sL ${MODULE_RAW}/resources/oc-collector-configmap.yaml \
  | sed 's/<OPENSEARCH_USERNAME>/'"$OPENSEARCH_USERNAME"'/g; s/<OPENSEARCH_PASSWORD>/'"$OPENSEARCH_PASSWORD"'/g' \
  | kubectl apply -f -
```

### Install the Extension

Download `values/observability-extension.yaml`:

```bash
curl -sOL ${MODULE_RAW}/values/observability-extension.yaml

# Multi-cluster: switch to observability plane context before running
helm install amp-observability-traces \
  oci://${HELM_CHART_REGISTRY}/wso2-amp-observability-extension \
  --version ${AMP_VERSION} \
  --namespace ${OBSERVABILITY_NS} \
  --timeout=1800s \
  -f observability-extension.yaml

kubectl wait --for=condition=Available deployment/amp-traces-observer \
  -n ${OBSERVABILITY_NS} --timeout=600s
```

---

## Step 5: Install Build Extension (Optional)

> **Cluster:** Workflow Plane

Installs Argo Workflow templates that enable Agent Manager to trigger container image builds via OpenChoreo's workflow plane.

The build extension requires the client secret of the OpenChoreo workload publisher OAuth client to be present in the workflow plane's secret store. Retrieve the secret from your OpenChoreo installation and store it:

```bash
# Multi-cluster: switch to workflow plane context before running
kubectl exec -it -n openbao openbao-0 -- \
  bao kv put secret/workflow-plane-oauth-client-secret \
  value="openchoreo-workload-publisher-secret"
```

Set the following values before installing:

| Value | Description |
|-------|-------------|
| `global.oauth.hostHeader` | Host header sent with OAuth token requests to your IdP (e.g. `thunder.<base-domain>`) |
| `global.oauth.clientId` | Client ID of the OpenChoreo workload publisher OAuth client — pre-installed with OpenChoreo |
| `global.registry.endpoint` | Container registry endpoint configured in the OpenChoreo workflow plane |

```bash
export REGISTRY_ENDPOINT="host.k3d.internal:10082"
export IDP_HOST_HEADER="thunder.<base-domain>"
export WORKLOAD_PUBLISHER_CLIENT_ID="openchoreo-workload-publisher-client"

# Multi-cluster: switch to workflow plane context before running
helm install build-workflow-extensions \
  oci://${HELM_CHART_REGISTRY}/wso2-amp-build-extension \
  --version ${AMP_VERSION} \
  --namespace ${WORKFLOW_NS} \
  --timeout=1800s \
  --set global.oauth.hostHeader="${IDP_HOST_HEADER}" \
  --set global.oauth.clientId="${WORKLOAD_PUBLISHER_CLIENT_ID}" \
  --set global.registry.endpoint="${REGISTRY_ENDPOINT}"
```

---

## Step 6: Install Evaluation Extension (Optional)

> **Cluster:** Workflow Plane

Installs automation workflows for agent evaluation jobs.

Download `values/evaluation-extension.yaml` from this module and ensure `ampEvaluation.publisher.apiKey` matches `agentManagerService.config.publisherApiKey.value` set in Step 2.

```bash
curl -sOL ${MODULE_RAW}/values/evaluation-extension.yaml
# Edit evaluation-extension.yaml to set the apiKey, then:

# Multi-cluster: switch to workflow plane context before running
helm install amp-evaluation-extension \
  oci://${HELM_CHART_REGISTRY}/wso2-amp-evaluation-extension \
  --version ${AMP_VERSION} \
  --namespace ${WORKFLOW_NS} \
  --timeout=1800s \
  -f evaluation-extension.yaml
```

---

## Step 7: Install AI Gateway Extension (Optional)

> **Cluster:** Data Plane

Connects the WSO2 API Platform Gateway to the Agent Manager control plane for LLM provider registration and AI-specific API policies.

### Register OAuth Application

Register the following application in your identity provider.

#### AI Gateway Bootstrap Client (Confidential Client)

Used by the extension's one-time bootstrap job to authenticate with the Agent Manager API.

| Field | Value |
|-------|-------|
| Application type | Confidential (client secret) |
| Grant types | Client Credentials |

Note the Client ID and Client Secret — you will need them in the steps below.

### Store Bootstrap Credentials

Store the credentials in your secret store:

```bash
# Example using OpenBao (adjust for your secret store)
kubectl exec -it -n openbao openbao-0 -- \
  bao kv put secret/amp-ai-gateway-bootstrap \
  client-id="<AI_GW_CLIENT_ID>" \
  client-secret="<AI_GW_CLIENT_SECRET>"
```

Then create the ExternalSecret:

```bash
# Multi-cluster: switch to data plane context before running
kubectl apply -f - <<EOF
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: amp-ai-gateway-bootstrap
  namespace: ${DATA_PLANE_NS}
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: amp-ai-gateway-bootstrap
  data:
    - secretKey: client-id
      remoteRef:
        key: amp-ai-gateway-bootstrap
        property: client-id
    - secretKey: client-secret
      remoteRef:
        key: amp-ai-gateway-bootstrap
        property: client-secret
EOF
```

### Update Agent Manager Audience

Update `agent-manager.yaml` to add the AI Gateway Bootstrap Client ID to `agentManagerService.config.keyManager.audience`, then upgrade the Agent Manager core deployment:

```bash
# Multi-cluster: switch to control plane context before running
helm upgrade amp \
  oci://${HELM_CHART_REGISTRY}/wso2-agent-manager \
  --version ${AMP_VERSION} \
  --namespace ${AMP_NS} \
  --timeout=1800s \
  -f agent-manager.yaml
```

### Configure and Install

Download `values/ai-gateway-extension.yaml`:

```bash
curl -sOL ${MODULE_RAW}/values/ai-gateway-extension.yaml
```

Open `ai-gateway-extension.yaml` and set the following:

| Field | Value |
|-------|-------|
| `agentManager.idp.tokenUrl` | `IDP_TOKEN_URL` — token endpoint of your IdP |
| `agentManager.idp.existingSecret` | Name of the Kubernetes secret created above (`amp-ai-gateway-bootstrap`) |
| `agentManager.apiUrl` | In multi-cluster: externally reachable address of `amp-api` (the default cluster-internal address is not resolvable from the data plane cluster) |

```bash
# Multi-cluster: switch to data plane context before running
helm install amp-ai-gateway \
  oci://${HELM_CHART_REGISTRY}/wso2-amp-ai-gateway-extension \
  --version ${AMP_VERSION} \
  --namespace ${DATA_PLANE_NS} \
  --timeout=1800s \
  -f ai-gateway-extension.yaml

kubectl wait --for=condition=complete job/amp-gateway-bootstrap \
  -n ${DATA_PLANE_NS} --timeout=300s
```

---

## Verification

Run each block against the appropriate cluster context.

```bash
# Control Plane cluster — Agent Manager core
kubectl get pods -n ${AMP_NS}
kubectl get statefulset amp-postgresql -n ${AMP_NS}

# Data Plane cluster — Gateway Operator and observability gateway
kubectl get pods -n ${DATA_PLANE_NS} -l app.kubernetes.io/name=gateway-operator
kubectl get apigateway obs-gateway -n ${DATA_PLANE_NS}
kubectl get restapi traces-api-secure -n ${DATA_PLANE_NS}

# Observability Plane cluster — Observability Extension (if installed)
kubectl get deployment amp-traces-observer -n ${OBSERVABILITY_NS}
kubectl get externalsecret -n ${OBSERVABILITY_NS}

# Workflow Plane cluster — Build / Evaluation extensions (if installed)
kubectl get pods -n ${WORKFLOW_NS}
```

---

## Access

Once the HTTPRoutes applied in Step 2 are accepted by the gateway, the services are available at:

| Service | URL |
|---------|-----|
| Agent Manager Console | `https://amp.<base-domain>` |
| Agent Manager API | `https://amp-api.<base-domain>` |

---

## Uninstallation

Remove extensions in reverse order, switching to the correct cluster context for each command.

```bash
# Data Plane cluster
# NOTE: Skip the gateway-operator and CRD removal if the Gateway Operator is shared
# with other modules (it was installed in Step 1 — only remove if this module owns it).
helm uninstall amp-ai-gateway -n ${DATA_PLANE_NS}
kubectl delete -f ${MODULE_RAW}/resources/rbac.yaml
kubectl delete crd restapis.gateway.api-platform.wso2.com \
  apigateways.gateway.api-platform.wso2.com
helm uninstall gateway-operator -n ${DATA_PLANE_NS}

# Workflow Plane cluster
helm uninstall amp-evaluation-extension -n ${WORKFLOW_NS}
helm uninstall build-workflow-extensions -n ${WORKFLOW_NS}

# Observability Plane cluster
helm uninstall amp-observability-traces -n ${OBSERVABILITY_NS}

# Control Plane cluster
kubectl delete httproute amp-console amp-api -n ${AMP_NS}
helm uninstall amp-platform-resources --namespace default
helm uninstall amp -n ${AMP_NS}
kubectl delete namespace ${AMP_NS}
```

