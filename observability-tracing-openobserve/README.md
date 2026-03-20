# Observability Tracing Module for OpenObserve

|               |           |
| ------------- |-----------|
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_tracing_openobserve)](https://codecov.io/gh/openchoreo/community-modules) |

This module collects distributed traces using OpenTelemetry collector and stores them in [OpenObserve](https://openobserve.ai).

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work. Deploy the `openchoreo-observability-plane` helm chart with the helm value `observer.tracingAdapter.enabled="true"` to enable the observer to fetch data from this tracing module.

## Installation

Before installing, create Kubernetes Secrets with the OpenObserve admin credentials:

> ⚠️ **Important:** Replace `YOUR_PASSWORD` with a strong, unique password.

```bash
kubectl create secret generic openobserve-admin-credentials \
  --namespace openchoreo-observability-plane \
  --from-literal=ZO_ROOT_USER_EMAIL='root@example.com' \
  --from-literal=ZO_ROOT_USER_PASSWORD='YOUR_PASSWORD'
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
  --version 0.2.1
```

To switch to HA mode, disable the standalone chart and enable the distributed chart:

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.2.1 \
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
>  --version 0.2.1 \
>  --set openobserve-standalone.enabled=false
>```
