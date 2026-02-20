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
  - secretKey: password
    remoteRef:
      key: opensearch-password
EOF
```

2. If you wish to use the Kubernetes operator-based OpenSearch version included with this Helm chart, install the operator as follows
```bash
helm repo add opensearch-operator https://opensearch-project.github.io/opensearch-k8s-operator/
helm repo update
helm install opensearch-operator opensearch-operator/opensearch-operator \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 2.8.0
```

## Deploy Helm chart

> **Note:** If you wish to use the Kubernetes operator-based OpenSearch version, add `--set openSearch.enabled=false --set openSearchCluster.enabled=true` flags when installing the Helm chart

```bash
helm upgrade --install observability-logs-opensearch \
  oci://ghcr.io/openchoreo/charts/observability-logs-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.3.1 \
  --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
```

> **Note:** If OpenSearch is already installed by another module (e.g., `observability-tracing-opensearch`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-logs-opensearch \
>   oci://ghcr.io/openchoreo/charts/observability-logs-opensearch \
>   --create-namespace \
>   --namespace openchoreo-observability-plane \
>   --version 0.3.1 \
>   --set openSearch.enabled=false \
>   --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
> ```

## Enable log collection

Enable Fluent Bit to start collecting logs from the cluster and publish to OpenSearch

```bash
helm upgrade observability-logs-opensearch \
  oci://ghcr.io/openchoreo/charts/observability-logs-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.3.1 \
  --reuse-values \
  --set fluent-bit.enabled=true
```
