# WSO2 Agentic Engineer Module for OpenChoreo

[WSO2 Agentic Engineer](https://github.com/wso2/labs-agentic-engineer) is a spec-driven, AI-enhanced software development lifecycle platform. This module installs it on an existing OpenChoreo installation, wiring it into OpenChoreo's control plane, data plane, and workflow plane.

## Table of Contents

- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Identity Provider Configuration](#identity-provider-configuration)
- [Quick Start (Local k3d)](#quick-start-local-k3d)
- [Installation](#installation)
  - [Step 1: Set Up GitHub Integration](#step-1-set-up-github-integration)
  - [Step 2: Install Agentic Engineer](#step-2-install-agentic-engineer)
- [Verification](#verification)
- [Access](#access)
- [Uninstallation](#uninstallation)

---

## Architecture

```text
┌─── Control Plane ───────────────────────────────────────┐
│ ns: openchoreo-control-plane                            │
│   OpenChoreo Control Plane + Thunder IdP                │
│                                                         │
│ ns: wso2-ae                                             │
│   asdlc-api (BFF)          port 9090                    │
│   agents-service           port 3400                    │
│   asdlc-console (nginx)    port 3000                    │
│   PostgreSQL               port 5432                    │
└─────────────────────────────────────────────────────────┘

┌─── Workflow Plane ──────────────────────────────────────┐
│ ns: workflows-default                                   │
│   app-factory-coding-agent  (one-shot pod per task)     │
│   dockerfile-builder        (one-shot pod per build)    │
└─────────────────────────────────────────────────────────┘
```

The BFF and console are exposed through the OC control-plane gateway. The coding agent runs as one-shot Argo Workflow pods in the workflow plane, dispatched by the BFF.

---

## Prerequisites

- OpenChoreo installed with control, data, workflow, and observability planes
- Thunder identity provider already configured with OpenChoreo
- cert-manager installed in the cluster
- External Secrets Operator installed with a `ClusterSecretStore` named `default`
- OpenBao installed (used by the coding-agent runner to retrieve the Anthropic API key at runtime)
- `helm` v3.12+, `kubectl` v1.32+
- A GitHub App (or Personal Access Token) for repository provisioning

### Cluster context

All resources deployed by this module (the `wso2-ae` namespace, HTTPRoutes, ReferenceGrant, and post-install jobs) must land on the **control plane cluster**. The Helm chart has no cluster selector — it deploys to whichever cluster your active `kubectl` context points to.

Set your context to the control plane cluster before running any commands in this guide:

```bash
kubectl config use-context <control-plane-context>
```

In a single-cluster deployment all planes share one context, so no switching is needed.

### Set environment variables

```bash
export AE_NS="wso2-ae"
export HELM_CHART_REGISTRY="ghcr.io/wso2"
export MODULE_RAW="https://raw.githubusercontent.com/openchoreo/community-modules/main/wso2-agentic-engineer"

export AE_VERSION="v0.5.2"
```

---

## Identity Provider Configuration

Agentic Engineer reuses the Thunder instance already installed with OpenChoreo. Before proceeding, collect these endpoints:

| Variable | Description | Default (Thunder) |
|----------|-------------|-------------------|
| `THUNDER_PUBLIC_URL` | Browser-facing Thunder base URL | `http://thunder.openchoreo.localhost:8080` |
| `THUNDER_ADMIN_URL` | In-cluster Thunder admin URL | `http://thunder-service.thunder.svc.cluster.local:8090` |
| `THUNDER_JWKS_URL` | Public JWKS endpoint | `${THUNDER_PUBLIC_URL}/oauth2/jwks` |

The post-install Thunder bootstrap job handles all Thunder configuration automatically:

- Patches Thunder's `cors.allowedOrigins` to include the console URL and restarts Thunder
- Registers the `asdlc-console-client` PKCE client (used by the browser for login)
- Registers the `asdlc-api-client` confidential client (used by the BFF to call OC APIs)
- Registers the `asdlc-system-client` confidential client (used by the BFF to provision per-project OAuth apps)

No manual Thunder configuration is required.

The chart's default values for Thunder already match a standard OpenChoreo installation (`namespace: thunder`, `configMapName: thunder-config-map`, `deploymentName: thunder-deployment`). Override `thunder.namespace`, `thunder.configMapName`, and `thunder.deploymentName` in your values file only if your setup differs.

---

## Quick Start (Local k3d)

If you are running the standard OpenChoreo k3d setup, all chart defaults match your cluster out of the box. No values file is needed — install with a single command and configure everything via the console UI afterward:

```bash
helm install wso2-ae \
  oci://${HELM_CHART_REGISTRY}/wso2-agentic-engineer-bundle \
  --version ${AE_VERSION} \
  --namespace ${AE_NS} \
  --create-namespace \
  --timeout 600s
```

Log in at `http://asdlc.openchoreo.localhost:8080` with `admin` / `admin` and complete setup from the Settings page.

---

## Installation

### Step 1: Set Up GitHub Integration

Agentic Engineer uses GitHub to provision repositories, manage branches, and dispatch the coding agent via pull requests.

#### Option A: GitHub App (recommended)

1. Create a GitHub App with these permissions:
   - **Repository:** Contents (read/write), Pull requests (read/write), Issues (read/write), Workflows (read/write), Webhooks (read/write)
   - **Organization:** Members (read)
2. Set the webhook URL to your BFF's public URL: `<ASDLC_API_PUBLIC_URL>/webhooks/github`
3. Install the app on the organizations or repositories you want Agentic Engineer to manage.
4. Note the **App ID**, **Client ID**, **Client Secret**, and download the **private key PEM**.

#### Option B: Personal Access Token

Set `github.appId`, `github.clientId`, and `github.clientSecret` to empty strings and configure a PAT with `repo` and `workflow` scopes via the Agentic Engineer settings UI after install.

#### Webhook relay for local clusters

If your cluster cannot receive inbound traffic from GitHub (e.g. a local k3d cluster), use a smee.io relay:

1. Create a channel at https://smee.io/new — copy the URL.
2. Set `wso2-ae-platform.smee.url` in your values file to the smee.io channel URL.
3. Set your GitHub App webhook URL to the same smee.io channel URL.

---

### Step 2: Install Agentic Engineer

#### Configure values

```bash
curl -sOL ${MODULE_RAW}/values/asdlc-platform.yaml
```

Open `asdlc-platform.yaml` and fill in every `<PLACEHOLDER>`:

| Field | Description |
|-------|-------------|
| `thunder.publicURL` | Browser-facing Thunder URL (`THUNDER_PUBLIC_URL`) |
| `thunder.systemClientSecret` | Secret for the ASDLC system client registered in Thunder — change for production |
| `bff.publicURL` | Public URL for the BFF, reachable from GitHub and coding-agent pods |
| `console.publicURL` | Public URL for the console |
| `wso2-ae-platform.asdlcApi.hostname` | Hostname for the BFF HTTPRoute (typically the host part of `bff.publicURL`) |
| `wso2-ae-platform.console.hostname` | Hostname for the console HTTPRoute (typically the host part of `console.publicURL`) |
| `wso2-ae-platform.console.thunderPublicURL` | Set to `THUNDER_PUBLIC_URL` |
| `github.appId` | GitHub App ID |
| `github.clientId` | GitHub App OAuth client ID |
| `github.clientSecret` | GitHub App OAuth client secret |
| `github.appSlug` | GitHub App slug (URL-safe name) |
| `github.webhookSecret` | Random secret registered in your GitHub App webhook settings |
| `github.oauthStateKey` | Exactly 32-character random string for OAuth state HMAC |
| `anthropic.apiKey` | Anthropic API key (platform-level fallback for the coding agent) |
| `postgres.auth.password` | PostgreSQL password — change from the default |
| `openbao.token` | OpenBao root/service token |
| `wso2-ae-platform.idp.issuer` | Set to `THUNDER_PUBLIC_URL` |
| `wso2-ae-platform.idp.jwksURL` | Set to `${THUNDER_PUBLIC_URL}/oauth2/jwks` |

#### Install

```bash
helm install wso2-ae \
  oci://${HELM_CHART_REGISTRY}/wso2-agentic-engineer-bundle \
  --version ${AE_VERSION} \
  --namespace ${AE_NS} \
  --create-namespace \
  --timeout 600s \
  -f asdlc-platform.yaml \
  --set-file wso2-ae-platform.github.appPrivateKeyPem=./github-app.pem  # omit if using PAT
```

> **Note:** The BFF task signing key (RSA-2048 for JWT signing) is auto-generated by a Helm hook Job on first install and stored in a Kubernetes ConfigMap. The key is reused on subsequent Helm upgrades, ensuring that in-flight task JWTs remain valid. For production deployments with a custom signing key, pass `--set-file wso2-ae-platform.taskSigning.privateKeyPem=./your-key.pem`.

The install runs two post-install jobs:
- **Thunder bootstrap** — patches Thunder's `cors.allowedOrigins` to include the console URL (required for the browser PKCE token exchange), triggers a rolling restart of Thunder, waits for it to be ready, then registers the ASDLC OAuth clients. If the CORS patch fails, check the job logs: `kubectl logs -n ${AE_NS} -l app.kubernetes.io/component=thunder-bootstrap`. The job assumes Thunder is deployed to the `thunder` namespace as `thunder-deployment` with ConfigMap `thunder-config-map` — the defaults for a standard OpenChoreo installation. If your setup differs, override `thunder.namespace`, `thunder.deploymentName`, and `thunder.configMapName` in your values file.
- **Gateway TLS patch** — adds an HTTPS listener to the OC data-plane gateway using a self-signed cert-manager certificate.

Wait for all pods to be ready:

```bash
kubectl wait --for=condition=Available deployment/asdlc-api \
  -n ${AE_NS} --timeout=300s
kubectl wait --for=condition=Available deployment/agents-service \
  -n ${AE_NS} --timeout=300s
kubectl wait --for=condition=Available deployment/asdlc-console \
  -n ${AE_NS} --timeout=300s
```

---

## Verification

```bash
# All pods running
kubectl get pods -n ${AE_NS}

# HTTPRoutes accepted by the gateway
kubectl get httproute -n openchoreo-control-plane | grep asdlc

# Post-install jobs completed
kubectl get jobs -n ${AE_NS}
```

---

## Access

| Service | URL |
|---------|-----|
| Console | `<ASDLC_CONSOLE_PUBLIC_URL>` |
| BFF API | `<ASDLC_API_PUBLIC_URL>` |

Log in with the Thunder admin credentials (`admin` / `admin` by default on a standard OpenChoreo installation).

> **Warning:** The default `admin` / `admin` credentials are for local and development environments only. For any production deployment, rotate and change all Thunder admin credentials before exposing the installation externally.

---

## Uninstallation

```bash
helm uninstall wso2-ae -n ${AE_NS}
kubectl delete namespace ${AE_NS}
```

