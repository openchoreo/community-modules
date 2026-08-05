# WSO2 Agentic Engineer Module for OpenChoreo

[WSO2 Agentic Engineer](https://github.com/wso2/labs-agentic-engineer) is a spec-driven, AI-enhanced software development lifecycle platform. This module installs it on an existing OpenChoreo installation, wiring it into OpenChoreo's control plane, data plane, and workflow plane.

## Table of Contents

- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Identity Provider Configuration](#identity-provider-configuration)
- [Secret Management](#secret-management)
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
- External Secrets Operator (ESO) installed — the chart creates the `ClusterSecretStore` automatically
- OpenBao installed (used as the secret store backend; must be reachable at `openbao.openbao.svc.cluster.local:8200` by default)
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

## Secret Management

Secrets are stored in OpenBao and synced into the cluster via ESO — they never appear as plain values in the pod spec or Helm release manifest. For **local and dev setups**, values can be provided in `asdlc-platform.yaml` for convenience; the chart seeds them into OpenBao automatically. For **production**, secrets must be pre-seeded in OpenBao directly and must not be set in the values file at all.

### How it works

```
values.yaml (wso2-ae-platform.secrets.*)
       │
       ▼
K8s Secret "asdlc-api-seed"        (staging area, created by chart)
       │
       ▼
PushSecret → OpenBao               (ESO pushes values into OpenBao)
       │
       ▼
ExternalSecret → "asdlc-api-secrets"  (ESO reads back from OpenBao every 15s)
       │
       ▼
asdlc-api Pod (env vars via secretKeyRef)
```

The chart creates the `ClusterSecretStore "default"` automatically if one does not already exist in the cluster.

### Per-secret `forceDefaultOverride` policy

Each entry in `wso2-ae-platform.secrets` has a `forceDefaultOverride` field that controls how the chart interacts with OpenBao:

| Value | Behaviour |
|-------|-----------|
| `""` (default) | Push to OpenBao only if the key is absent (`IfNotExists`). Seeds on first install, never overwrites on upgrade. |
| `"true"` | Always overwrite OpenBao with the value from `values.yaml` on every `helm install/upgrade`. |
| `"false"` | Do not push at all. Relies on a value already set in OpenBao manually. Use this for production secrets pre-seeded out of band. |

### Dev vs production pattern

**Dev (default):** Leave `forceDefaultOverride: ""`. Values in `wso2-ae-platform.secrets` are seeded into OpenBao on first install and never overwritten on upgrade.

**Production:** Pre-seed each secret path in OpenBao before running `helm install`, then set `forceDefaultOverride: "false"` so the chart never touches them:

```bash
kubectl exec -n openbao openbao-0 -- sh -c \
  'BAO_ADDR=http://127.0.0.1:8200 BAO_TOKEN=<token> \
   bao kv put secret/asdlc/platform/github/webhook-secret value="<real-value>"'
```

Repeat for all secret paths. Then set `forceDefaultOverride: "false"` for each in your values file.

### Rotating a secret

Update the value directly in OpenBao — no `helm upgrade` needed. ESO re-reads every 15 seconds:

```bash
kubectl exec -n openbao openbao-0 -- sh -c \
  'BAO_ADDR=http://127.0.0.1:8200 BAO_TOKEN=<token> \
   bao kv put secret/asdlc/platform/github/webhook-secret value="<new-value>"'
```

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

Set `github.appId` and `github.clientId` to empty strings and leave `wso2-ae-platform.secrets.githubClientSecret.value` empty. Configure a PAT with `repo` and `workflow` scopes via the Agentic Engineer settings UI after install.

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

**Non-sensitive configuration** — safe to set in values.yaml:

| Field | Description |
|-------|-------------|
| `thunder.publicURL` | Browser-facing Thunder URL (`THUNDER_PUBLIC_URL`) |
| `bff.publicURL` | Public URL for the BFF, reachable from GitHub and coding-agent pods |
| `console.publicURL` | Public URL for the console |
| `wso2-ae-platform.asdlcApi.hostname` | Hostname for the BFF HTTPRoute (typically the host part of `bff.publicURL`) |
| `wso2-ae-platform.console.hostname` | Hostname for the console HTTPRoute (typically the host part of `console.publicURL`) |
| `wso2-ae-platform.console.thunderPublicURL` | Set to `THUNDER_PUBLIC_URL` |
| `github.appId` | GitHub App ID |
| `github.clientId` | GitHub App OAuth client ID |
| `github.appSlug` | GitHub App slug (URL-safe name) |
| `wso2-ae-platform.idp.issuer` | Set to `THUNDER_PUBLIC_URL` |
| `wso2-ae-platform.idp.jwksURL` | Set to `${THUNDER_PUBLIC_URL}/oauth2/jwks` |

**Secrets** — how you set these depends on your environment:

- **Local / dev:** Set `wso2-ae-platform.secrets.<name>.value` in `asdlc-platform.yaml`. The chart pushes them into OpenBao on first install (`IfNotExists` — never overwritten on upgrade). Values will appear in Helm release history, which is acceptable for non-production use.
- **Production:** Pre-seed each path in OpenBao before `helm install` (see the [Secret Management](#secret-management) section), leave `value: ""` in the values file, and set `forceDefaultOverride: "false"` so the chart never writes to your store.

| Secret field | Description |
|---|---|
| `wso2-ae-platform.secrets.thunderSystemClientSecret` | Secret for the ASDLC system client registered in Thunder |
| `wso2-ae-platform.secrets.githubClientSecret` | GitHub App OAuth client secret |
| `wso2-ae-platform.secrets.githubWebhookSecret` | Random secret registered in your GitHub App webhook settings |
| `wso2-ae-platform.secrets.oauthStateKey` | Exactly 32-character random string for OAuth state HMAC |
| `wso2-ae-platform.secrets.serviceAuthClientSecret` | BFF service identity client secret |
| `wso2-ae-platform.secrets.serviceAuthGitClientSecret` | BFF → git-service JWT secret |
| `wso2-ae-platform.secrets.serviceAuthAgentsClientSecret` | BFF → agents-service JWT secret |

> **`postgres.auth.password`** — only applies to the bundled dev StatefulSet. For production use an external database via `postgres.url` and leave `postgres.auth.password` unset.

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

> **Note:** The BFF task signing key (RSA-2048 for JWT signing) is auto-generated by a Helm hook Job on first install and stored in a Kubernetes Secret. The key is reused on subsequent Helm upgrades, ensuring that in-flight task JWTs remain valid. For production deployments with a custom signing key, pass `--set-file wso2-ae-platform.taskSigning.privateKeyPem=./your-key.pem`.

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

# Secrets synced from OpenBao
kubectl get externalsecret asdlc-api-secrets -n ${AE_NS}
kubectl get pushsecret -n ${AE_NS}
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
