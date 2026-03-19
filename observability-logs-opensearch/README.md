# Observability Logs Module for OpenSearch

This module collects logs using Fluent Bit and stores them in OpenSearch.

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.

## Installation

### Pre-requisites

1. OpenSearch setup scripts in this helm chart need admin credentials to connect to OpenSearch and configure it. The command below pulls values from the `ClusterSecretStore` created earlier in the [OpenChoreo installation guide](https://openchoreo.dev/docs)

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
  --version 0.3.10 \
  --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
```

> **Note:** If OpenSearch is already installed by another module (e.g., `observability-tracing-opensearch`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-logs-opensearch \
>   oci://ghcr.io/openchoreo/helm-charts/observability-logs-opensearch \
>   --create-namespace \
>   --namespace openchoreo-observability-plane \
>   --version 0.3.10 \
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
  --version 0.3.10 \
  --reuse-values \
  --set fluent-bit.enabled=true
```

### Multi-cluster topology
In a **multi-cluster topology**, where the observability plane runs in a separate cluster
from the data-plane / workflow-plane clusters, install the Helm chart in those clusters with Fluent Bit enabled and OpenSearch disabled
to start collecting logs from the cluster and publish them to the observability plane cluster's OpenSearch endpoint.

```bash
helm upgrade --install observability-logs-opensearch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.3.10 \
  --set fluent-bit.enabled=true \
  --set openSearch.enabled=false \
  --set openSearchCluster.enabled=false \
  --set openSearchSetup.enabled=false
```
> **Note:**
>
> Make sure the `opensearch-admin-credentials` secret is available in the data-plane / workflow-plane clusters as well,
> and `fluent-bit.openSearchHost`, `fluent-bit.openSearchPort` and `fluent-bit.openSearchVHost` values are set to the OpenSearch endpoint exposed from the observability plane cluster.
