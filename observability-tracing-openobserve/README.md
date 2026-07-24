# Observability Tracing Module for OpenObserve

|               |                                                                                                                                                                                              |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_tracing_openobserve)](https://codecov.io/gh/openchoreo/community-modules) |

This module collects distributed traces using [OpenTelemetry collector](https://opentelemetry.io) and stores them in [OpenObserve](https://openobserve.ai).

> **Note:** The commands in this README install the latest module version. Refer to the [Compatibility](#compatibility) table below for the module version compatible with your OpenChoreo version.

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work. Deploy the `openchoreo-observability-plane` helm chart with the helm value `observer.tracingAdapter.enabled="true"` to enable the observer to fetch data from this tracing module.

## Installation

### Pre-requisites

1. OpenObserve credentials are required to configure it during installation and to access it. OpenChoreo uses the External Secrets Operator to manage secrets. Add your OpenObserve credentials (`ZO_ROOT_USER_EMAIL` and `ZO_ROOT_USER_PASSWORD`) to a secret store and use an `ExternalSecret` resource to generate a Kubernetes secret named `openobserve-admin-credentials` from it.
   Refer to the [secret management guide](https://openchoreo.dev/docs/platform-engineer-guide/secret-management/) for more details.

For example, the commands below add the secrets to OpenBao and pull them from the `ClusterSecretStore` created earlier in the [OpenChoreo installation guide](https://openchoreo.dev/docs).

```bash
kubectl exec -it -n openbao openbao-0 -- \
    bao kv put secret/openobserve-admin-credentials \
    ZO_ROOT_USER_EMAIL='YOUR_USERNAME' \
    ZO_ROOT_USER_PASSWORD='YOUR_PASSWORD'
```

```bash
kubectl apply -f - <<EOF
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: openobserve-admin-credentials
  namespace: openchoreo-observability-plane
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: openobserve-admin-credentials
  data:
    - secretKey: ZO_ROOT_USER_EMAIL
      remoteRef:
        key: openobserve-admin-credentials
        property: ZO_ROOT_USER_EMAIL
    - secretKey: ZO_ROOT_USER_PASSWORD
      remoteRef:
        key: openobserve-admin-credentials
        property: ZO_ROOT_USER_PASSWORD
EOF
```

## OpenObserve deployment modes

This chart includes two OpenObserve Helm chart dependencies:

- **`openobserve-standalone`** — A single-node deployment that uses local disk storage. This is enabled by default and suitable for most use cases.
- **`openobserve`** — A distributed, high-availability (HA) deployment with separate components (router, ingester, querier, etc.) that requires object storage (e.g. S3, MinIO). This is disabled by default.

Install this module in your OpenChoreo cluster using:

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev
```

To switch to HA mode, disable the standalone chart and enable the distributed chart:

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --reuse-values \
  --set openobserve-standalone.enabled=false \
  --set openobserve.enabled=true
```

Refer to the [openobserve Helm chart documentation](https://github.com/openobserve/openobserve-helm-chart/tree/main/charts/openobserve) to configure the distributed deployment.

> **Note:** If OpenObserve is already installed by another module (e.g., `observability-logs-openobserve`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-tracing-openobserve \
>  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-openobserve \
>  --create-namespace \
>  --namespace openchoreo-observability-plane \
>  --version 0.0.0-latest-dev \
>  --set openobserve-standalone.enabled=false
> ```

## Enable trace collection

### Installation modes

This chart supports three `global.installationMode` values:

- **`singleCluster`**: Deploy everything (OpenTelemetry Collector + OpenObserve) into a single cluster (used when the data plane and observability plane are in the same cluster).
- **`multiClusterReceiver`**: Deploy the OpenTelemetry Collector as a central receiver into the observability plane cluster. It accepts OTLP from remote clusters and writes traces to OpenObserve.
- **`multiClusterExporter`**: Deploy the OpenTelemetry Collector as an exporter into each data-plane cluster. It receives OTLP from in-cluster workloads and exports it to the receiver in the observability plane cluster over OTLP.

#### Single-cluster topology

In a **single-cluster topology**, where the observability plane runs in the same cluster
as the data-plane / workflow-plane clusters, the default `singleCluster` install (above) already
collects traces and writes them to the in-cluster OpenObserve.

#### Multi-cluster topology

In a **multi-cluster topology**, where the observability plane runs in a separate cluster
from the data-plane / workflow-plane clusters, trace data flows from the exporter collectors on
the remote clusters, through the observability-plane gateway (`gateway-default`), into the receiver
collector, which writes to OpenObserve. You typically install:

- **Receiver (observability plane cluster)**: `global.installationMode=multiClusterReceiver`
- **Exporter (each data-plane cluster)**: `global.installationMode=multiClusterExporter`

##### 1) Install the receiver (observability plane cluster)

Install the chart in the observability plane cluster. Set `opentelemetryCollectorCustomizations.http.hostnames` to the gateway hostname that
remote exporters will target, so the receiver's `HTTPRoute` is created on `gateway-default`:

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set global.installationMode="multiClusterReceiver" \
  --set-json opentelemetryCollectorCustomizations.http.hostnames='["opentelemetry.<OBS_BASE_DOMAIN>"]'
```

> **Note:** If OpenObserve is already installed by another module (e.g., `observability-logs-openobserve`), add `--set openobserve-standalone.enabled=false` to avoid conflicts.

##### 2) Install an exporter (each data-plane cluster)

Install the chart in each data-plane cluster. The exporter does **not** need OpenObserve, the adapter, or
the `openobserve-admin-credentials` secret — those credentials live only on the receiver. It only needs to
export OTLP to the receiver.

Set `opentelemetryCollectorCustomizations.http.observabilityPlaneUrl` to the receiver endpoint exposed
through the observability-plane gateway (for example: `http://opentelemetry.<OBS_BASE_DOMAIN>:<port>`).
If the gateway is exposed over a TLS/HTTPS listener, use the `https://` scheme instead. Also set
`opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost` when `observabilityPlaneUrl`
differs from the gateway hostname.

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set global.installationMode="multiClusterExporter" \
  --set openobserve-standalone.enabled=false \
  --set openobserve.enabled=false \
  --set adapter.enabled=false \
  --set-json opentelemetry-collector.extraEnvs='[]' \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneUrl="http://opentelemetry.<OBS_BASE_DOMAIN>:<port>" \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost="opentelemetry.<OBS_BASE_DOMAIN>"
```

## Dependencies

Bundled upstream Helm charts:

| Chart | Repository |
| ----- | ---------- |
| opentelemetry-collector | https://open-telemetry.github.io/opentelemetry-helm-charts |
| openobserve-standalone | https://charts.openobserve.ai |
| openobserve | https://charts.openobserve.ai |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
|----------------|--------------------|
| >= v0.3.0      | >= v1.2.0          |
| >= v0.2.x      | >= v1.0.x          |
