# WSO2 Agentic Engineer Platform Module for OpenChoreo

[WSO2 Agentic Engineer](https://github.com/wso2/labs-agentic-engineer) is a spec-driven AI SDLC platform that runs on an existing OpenChoreo cluster. It takes feature requests, collaborates in real time to write an implementation spec, and dispatches AI coding agents as ephemeral OpenChoreo Job Components to implement and validate the work. This module documents the recommended installation path using the `aectl` CLI.

## Prerequisites

- OpenChoreo v1.1.1+ with control, data, and workflow (build) planes running
- Thunder (OpenChoreo's built-in IdP) configured
- OpenBao and External Secrets Operator (included with OpenChoreo)
- `kubectl` pointing at the target cluster, `helm` v3.12+
- An Anthropic API key
- A GitHub Personal Access Token with `repo` and `admin:repo_hook` scopes

## Install aectl

`aectl` is the CLI that drives the full Agentic Engineer installation. Download the binary for your platform from the [GitHub releases page](https://github.com/wso2/labs-agentic-engineer/releases?q=ctl%2F).

**Linux (amd64)**

```bash
VERSION=0.1.0
curl -L https://github.com/wso2/labs-agentic-engineer/releases/download/ctl%2Fv${VERSION}/aectl-linux-amd64 \
  -o /tmp/aectl && sudo install -m 755 /tmp/aectl /usr/local/bin/aectl
```

**macOS (Apple Silicon)**

```bash
VERSION=0.1.0
curl -L https://github.com/wso2/labs-agentic-engineer/releases/download/ctl%2Fv${VERSION}/aectl-darwin-arm64 \
  -o /tmp/aectl && sudo install -m 755 /tmp/aectl /usr/local/bin/aectl
```

**macOS (Intel)**

```bash
VERSION=0.1.0
curl -L https://github.com/wso2/labs-agentic-engineer/releases/download/ctl%2Fv${VERSION}/aectl-darwin-amd64 \
  -o /tmp/aectl && sudo install -m 755 /tmp/aectl /usr/local/bin/aectl
```

**Windows (amd64):** Download `aectl-windows-amd64.exe` from the [releases page](https://github.com/wso2/labs-agentic-engineer/releases?q=ctl%2F) and add it to your `PATH`.

## Configure

Create a config file with your cluster's values. The defaults below match a standard OpenChoreo single-cluster install — adjust `thunder.admin_client_id` and the public URLs for your environment.

```yaml
thunder:
  namespace:        "thunder"
  url:             "http://thunder-service.thunder.svc.cluster.local:8090"
  config_map:      "thunder-config-map"
  deployment:      "thunder-deployment"
  admin_client_id: "<your-thunder-admin-client-id>"
  public_url:      "http://thunder.<your-base-domain>"

oc:
  api_url:          "http://openchoreo-api.openchoreo-control-plane.svc.cluster.local:8080"
  system_namespace: "openchoreo-control-plane"

gateway:
  hostname: "<your-data-plane-hostname>"  # e.g. openchoreoapis.localhost for local dev

webhook:
  delivery_url: "https://aep-api.<your-base-domain>/api/v1/webhooks/github"

openbao:
  addr: "http://openbao.openbao.svc.cluster.local:8200"

codingagent:
  openbao_direct:
    enabled: true  # required for OSS installs; false in managed environments
```

> **Sensitive values are never stored in this file.** `aectl platform install` prompts interactively for the Thunder admin client secret and the Anthropic API key. You can also provide them via the `AEP_THUNDER_ADMIN_CLIENT_SECRET` and `ANTHROPIC_API_KEY` environment variables.

Upload the config to the cluster:

```bash
aectl platform config import --config aectl-config.yaml
```

## Install

```bash
aectl platform install \
  --console-url "https://aep.<your-base-domain>" \
  --api-url     "https://aep-api.<your-base-domain>"
```

`aectl platform install` performs the following steps in order:

1. Validates prerequisites — OpenChoreo v1.1.1+, build registry reachable
2. Seeds all platform secrets into OpenChoreo's built-in OpenBao instance; ESO syncs them into Kubernetes Secrets consumed by the platform services
3. Installs or upgrades the `aep-platform` Helm chart from `oci://ghcr.io/wso2/aep/charts/aep-platform`
4. Waits for all platform pods to become ready
5. Registers Agentic Engineer OAuth clients in Thunder

The command is idempotent — re-running it upgrades in place. Pass `--reuse-secrets` on upgrades to skip the secret prompts.

To pin to a specific release:

```bash
aectl platform install \
  --console-url      "https://aep.<your-base-domain>" \
  --api-url          "https://aep-api.<your-base-domain>" \
  --platform-version 0.1.0
```

| Flag | Default | Description |
|------|---------|-------------|
| `--platform-version` | `latest` | Agentic Engineer platform version to install |
| `--console-url` | `http://console.openchoreo.localhost:8080` | Public URL of the Agentic Engineer console |
| `--api-url` | `http://api.openchoreo.localhost:8080` | Public URL of the Agentic Engineer API |
| `--namespace` | `wso2-aep` | Namespace for AEP services |
| `--build-plane-namespace` | `openchoreo-workflow-plane` | OC workflow/build plane namespace |
| `--openbao-direct` | `false` | Direct OpenBao secret delivery (OSS installs) |
| `--reuse-secrets` | `false` | Skip secret prompts on upgrade |

## Verify

```bash
kubectl get pods -n wso2-aep
aectl platform status
```

## Access

The Agentic Engineer console is available at `https://aep.<your-base-domain>`.

Log in with your Thunder credentials. Once in, configure GitHub integration at **Settings → GitHub Integration** with a PAT that has `repo` and `admin:repo_hook` scopes.

## Uninstall

```bash
aectl platform uninstall
```

This removes the `aep-platform` Helm release and its PersistentVolumeClaims. OpenBao secrets and ESO-synced Kubernetes Secrets are not touched. To clean those up permanently:

```bash
# Permanently delete all Agentic Engineer secrets from OpenBao
# (KV v2: bao kv metadata delete removes all versions and history, not just soft-deletes the current one)
kubectl exec -n openbao openbao-0 -- sh -c '
  for key in anthropic-api-key postgres-password task-signing-key oauth-state-key \
      agents-jwt-secret webhook-secret opensearch-username opensearch-password; do
    bao kv metadata delete "secret/aep/$key"
  done
  for key in client-id client-secret; do
    bao kv metadata delete "secret/aep/thunder-admin/$key"
  done
  for key in oc-workload-publisher oc-observer-reader aep-api-client \
      bff-git-service bff-remote-worker local-dev-seeder system-client; do
    bao kv metadata delete "secret/aep/thunder-clients/$key"
  done
'

# Remove any ESO-synced Secrets remaining in the platform namespace
kubectl delete secret -n wso2-aep -l app.kubernetes.io/managed-by=external-secrets --ignore-not-found
```

## Compatibility

> **Note:** The module version below is compatible with the development version of OpenChoreo. Refer to the table for released versions.

| Module Version | OpenChoreo Version |
|---------------|-------------------|
| v0.1.x | v1.1.x+ |
