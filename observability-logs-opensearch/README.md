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
  --version 0.4.1 \
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
>   --version 0.4.1 \
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
  --version 0.4.1 \
  --reuse-values \
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
  --version 0.4.1 \
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
  --version 0.4.1 \
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

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
|----------------|--------------------|
| v0.4.x         | v1.1.x             |
| v0.3.x         | v1.0.x             |
