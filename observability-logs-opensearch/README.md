# Observability Logs Module for OpenSearch

This module collects logs using [Fluent Bit](https://fluentbit.io) and stores them in [OpenSearch](https://opensearch.org).

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work.

## Installation

### Pre-requisites

1. OpenSearch setup scripts in this helm chart need admin credentials to connect to OpenSearch and configure it. OpenChoreo uses the External Secrets Operator to manage secrets. Add your OpenSearch credentials (username and password) to a secret store and use an `ExternalSecret` resource to generate a Kubernetes secret from it.
   Refer to the [secret management guide](https://openchoreo.dev/docs/platform-engineer-guide/secret-management/) for more details.

For example, the command below pulls values from the `ClusterSecretStore` created earlier in the [OpenChoreo installation guide](https://openchoreo.dev/docs).

```bash
kubectl apply -f - <<EOF
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: opensearch-admin-credentials
  namespace: openchoreo-observability-plane
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: opensearch-admin-credentials
  data:
  - secretKey: username
    remoteRef:
      key: opensearch-username
      property: value
  - secretKey: password
    remoteRef:
      key: opensearch-password
      property: value
EOF
```

2. If you wish to use the Kubernetes operator-based OpenSearch version included with this Helm chart, install the operator as follows

```bash
helm repo add opensearch-operator https://opensearch-project.github.io/opensearch-k8s-operator/
helm repo update
helm upgrade --install opensearch-operator opensearch-operator/opensearch-operator \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 2.8.0 \
  --set kubeRbacProxy.image.repository=quay.io/brancz/kube-rbac-proxy \
  --set kubeRbacProxy.image.tag=v0.15.0
```

## Deploy Helm chart

> **Note:** If you wish to use the Kubernetes operator-based OpenSearch version, add `--set openSearch.enabled=false --set openSearchCluster.enabled=true --set openSearchCluster.credentialsSecretName="opensearch-admin-credentials"` flags when installing the Helm chart. The admin password will be read from the credentials secret at install time.

```bash
helm upgrade --install observability-logs-opensearch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.5.5 \
  --set adapter.openSearchSecretName="opensearch-admin-credentials" \
  --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
```

> **Note:** If OpenSearch is already installed by another module (e.g., `observability-tracing-opensearch`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-logs-opensearch \
>   oci://ghcr.io/openchoreo/helm-charts/observability-logs-opensearch \
>   --create-namespace \
>   --namespace openchoreo-observability-plane \
>   --version 0.5.5 \
>   --set adapter.openSearchSecretName="opensearch-admin-credentials" \
>   --set openSearch.enabled=false \
>   --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
> ```

## Enable log collection

### Single-cluster topology

In a **single-cluster topology**, where the observability plane runs in the same cluster
as the data-plane / workflow-plane clusters, enable Fluent Bit in the already installed Helm chart
to start collecting logs from the cluster and publish them to OpenSearch:

```bash
helm upgrade observability-logs-opensearch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.5.5 \
  --reset-then-reuse-values \
  --set fluent-bit.enabled=true
```

### Multi-cluster topology

In a **multi-cluster topology**, where the observability plane runs in a separate cluster
from the data-plane / workflow-plane clusters, you need two things:

1. **On the observability plane cluster**: expose OpenSearch through the gateway via TLS passthrough so remote fluent-bit instances can reach it.
2. **On each remote cluster**: install this chart with only fluent-bit enabled, pointed at the obs cluster's OpenSearch endpoint.

#### Observability plane cluster setup

The recommended approach is the **OpenSearch Operator** (`openSearchCluster.enabled=true`), which automatically creates the TLSRoute needed for gateway passthrough. Install the operator first (see [Prerequisites](#pre-requisites)), then install the chart with:

```bash
helm upgrade --install observability-logs-opensearch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.5.5 \
  --set adapter.openSearchSecretName="opensearch-admin-credentials" \
  --set openSearch.enabled=false \
  --set openSearchCluster.enabled=true \
  --set openSearchCluster.credentialsSecretName="opensearch-admin-credentials" \
  --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
```

You also need TLS passthrough enabled on the observability plane gateway. When installing the `openchoreo-observability-plane` chart, include:

```yaml
gateway:
  tlsPassthrough:
    enabled: true
    hostname: "opensearch.<OBS_BASE_DOMAIN>"
```

> **Note:** If you use the helm subchart OpenSearch (`openSearch.enabled=true`) instead of the operator, the TLSRoute is not auto-generated and the `BackendConfigPolicy` on the default `opensearch` Service conflicts with TLS passthrough (causes double-TLS). You would need to create a separate passthrough Service and TLSRoute manually. The operator approach avoids this complexity.

#### Remote cluster setup (data-plane / workflow-plane clusters)

Install the chart with only fluent-bit enabled:

```bash
helm upgrade --install observability-logs-opensearch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.5.5 \
  --set adapter.enabled=false \
  --set openSearch.enabled=false \
  --set openSearchCluster.enabled=false \
  --set openSearchSetup.enabled=false \
  --set fluent-bit.enabled=true \
  --set fluent-bit.openSearchHost=opensearch.<OBS_BASE_DOMAIN> \
  --set fluent-bit.openSearchPort=<gateway-tls-passthrough-port> \
  --set fluent-bit.openSearchVHost=opensearch.<OBS_BASE_DOMAIN>
```

> **Note:**
>
> - The `opensearch-admin-credentials` secret must exist on the remote cluster. If you don't have a shared secret backend, create it manually (see the [Multi-Cluster Connectivity](https://openchoreo.dev/docs/platform-engineer-guide/multi-cluster-connectivity/) guide).
> - `fluent-bit.openSearchHost` and `fluent-bit.openSearchVHost` should match the TLS passthrough hostname on the obs gateway.
> - `fluent-bit.openSearchPort` should match the passthrough listener port (commonly `11443` if the obs gateway uses non-standard ports).
> - The adapter and setup job are disabled because they only need to run on the observability plane cluster.

## Upgrading to 0.5.5

0.5.5 upgrades the bundled OpenSearch from 3.3.0 to 3.9.0 and Fluent Bit from 4.2.3 to 5.1.5 (fluent-bit chart 0.58.3). Before upgrading:

- **Take a snapshot of OpenSearch.** Once OpenSearch 3.9.0 has opened the data, it cannot be rolled back to 3.3.0; `helm rollback` succeeds but OpenSearch fails to start. Restoring a [snapshot](https://docs.opensearch.org/latest/tuning-your-cluster/availability-and-recovery/snapshots/index/) is the only way back. For the same reason, when moving on to OpenChoreo v1.3 or later, upgrade to 0.7.1, 0.8.1 or later, not to 0.6.x, 0.7.0 or 0.8.0, which bundle OpenSearch 3.3.0.
- **Expect OpenSearch to restart.** With the subchart OpenSearch (`openSearch.enabled=true`), its single pod restarts, and logs written while it is down (about 20 seconds) are dropped. With the operator (`openSearchCluster.enabled=true`), the operator upgrades the nodes one at a time and then restarts the data nodes once more. OpenSearch 3.9.0 uses about twice the CPU of 3.3.0 while starting, so upgrade when the nodes have spare CPU.

Upgrade with `--reset-then-reuse-values` (Helm 3.14 or later), in every cluster where the release is installed:

```bash
helm upgrade observability-logs-opensearch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-opensearch \
  --namespace openchoreo-observability-plane \
  --version 0.5.5 \
  --reset-then-reuse-values
```

> **Note:**
>
> - Do not use `--reuse-values`. It keeps the previous chart's defaults, so OpenSearch stays on 3.3.0 and Fluent Bit 5.x is probed on a readiness path it no longer serves. The chart refuses to render in that case.
> - With Helm 4 and the operator-based OpenSearch, add `--server-side=false`. The operator updates fields of the `OpenSearchCluster` resource after it is created, and Helm 4's server-side apply rejects any upgrade that sets them again.
> - Indices created before the upgrade keep their mappings. New daily indices use the same mappings as in 0.5.4.

## Troubleshooting

### Observer returns no logs

If Fluent Bit is shipping and `container-logs-*` is filling but Observer queries come back empty, the index was likely created before `openSearchSetup` applied its template — so it has dynamic mappings that don't match what the adapter queries. Delete the index and let Fluent Bit recreate it:

```bash
kubectl exec -n openchoreo-observability-plane opensearch-master-0 \
  -- curl -ksu admin:<password> -X DELETE 'https://localhost:9200/container-logs-*'
```

Only logs written after the deletion will appear (Fluent Bit's tail cursor persists at `/var/lib/fluent-bit/db/tail-container-logs.db`). Generate fresh traffic, or remove that DB and restart the DaemonSet to backfill.

## Dependencies

Bundled upstream Helm charts:

| Chart | Repository |
| ----- | ---------- |
| opensearch | https://opensearch-project.github.io/helm-charts/ |
| fluent-bit | https://fluent.github.io/helm-charts |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
| -------------- | ------------------ |
| v0.5.x         | v1.2.x             |
| v0.4.x         | v1.1.x             |
| v0.3.x         | v1.0.x             |
