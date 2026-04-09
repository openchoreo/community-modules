# Observability Tracing Module for OpenSearch

This module collects traces using [OpenTelemetry collector](https://opentelemetry.io) and stores them in [OpenSearch](https://opensearch.org).

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

### Deploy Helm chart

### Installation modes

This chart supports three `global.installationMode` values:

- **`singleCluster`**: Deploy everything (OpenTelemetry Collector + OpenSearch) into a single cluster (uses when dataplane and observability plane are in the same cluster).
- **`multiClusterReceiver`**: Deploy OpenTelemetry Collector as a central receiver into the observability plane cluster. It accepts OTLP from remote clusters and writes traces to OpenSearch.
- **`multiClusterExporter`**: Deploy OpenTelemetry Collector as an exporter into each dataplane cluster. It receives OTLP from in-cluster workloads and exports to the receiver in the observability plane cluster using OTLP.

#### Single-cluster topology

> **Note:** If you wish to use the Kubernetes operator-based OpenSearch version, add `--set openSearch.enabled=false --set openSearchCluster.enabled=true --set openSearchCluster.credentialsSecretName="opensearch-admin-credentials"` flags when installing the Helm chart. The admin password will be read from the credentials secret at install time.

```bash
helm upgrade --install observability-tracing-opensearch \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.3.11 \
  --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
```

> **Note:** If OpenSearch is already installed by another module (e.g., `observability-logs-opensearch`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-tracing-opensearch \
>   oci://ghcr.io/openchoreo/helm-charts/observability-tracing-opensearch \
>   --create-namespace \
>   --namespace openchoreo-observability-plane \
>   --version 0.3.11 \
>   --set openSearch.enabled=false \
>   --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
> ```

#### Multi-cluster topology

In multi-cluster mode you typically install:

- **Receiver (observability plane cluster)**: `global.installationMode=multiClusterReceiver`
- **Exporter (each dataplane cluster)**: `global.installationMode=multiClusterExporter`

### 1) Install the receiver (observability plane cluster)

Install the chart in the observability plane cluster/namespace (this is the cluster that has OpenSearch and will store traces):

```bash
helm upgrade --install observability-tracing-opensearch \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.3.11 \
  --set global.installationMode="multiClusterReceiver" \
  --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
```

### 2) Install an exporter (each dataplane cluster)

Install the chart in each dataplane cluster. The exporter does **not** need OpenSearch or the OpenSearch setup job; it only needs to export OTLP to the receiver.

Set `opentelemetryCollectorCustomizations.http.observabilityPlaneUrl` to the receiver endpoint (for example: `http://opentelemetry.<gateway-domain>:<port>`).
Also set `opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost` if observabilityPlaneUrl differs from the gateway hostname.

```bash
helm upgrade --install observability-tracing-opensearch \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.3.11 \
  --set global.installationMode="multiClusterExporter" \
  --set openSearch.enabled=false \
  --set openSearchCluster.enabled=false \
  --set openSearchSetup.enabled=false \
  --set opentelemetry-collector.extraEnvs=[] \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneUrl="http://opentelemetry.<gateway-domain>:<port>" \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost="opentelemetry.<gateway-domain>"
```
