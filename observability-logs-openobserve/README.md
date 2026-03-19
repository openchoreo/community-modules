# Observability Logs Module for OpenObserve

|               |           |
| ------------- |-----------|
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_logs_openobserve)](https://codecov.io/gh/openchoreo/community-modules) |

This module collects container logs using Fluent Bit and stores them in [OpenObserve](https://openobserve.ai).

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work. Deploy the `openchoreo-observability-plane` helm chart with the helm value `observer.logsAdapter.enabled="true"` to enable the observer to fetch data from this logs module.


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
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.4.1
```

To switch to HA mode, disable the standalone chart and enable the distributed chart:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.4.1 \
  --reuse-values \
  --set openobserve-standalone.enabled=false \
  --set openobserve.enabled=true
```

Refer to the [openobserve Helm chart documentation](https://github.com/openobserve/openobserve-helm-chart/tree/main/charts/openobserve) to configure the distributed deployment.

## Enable log collection

### Single-cluster topology
In a **single-cluster topology**, where the observability plane runs in the same cluster
as the data-plane / workflow-plane clusters, enable Fluent Bit in the already installed Helm chart
to start collecting logs from the cluster and publish them to OpenObserve:

```bash
helm upgrade observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.4.1 \
  --reuse-values \
  --set fluent-bit.enabled=true
```

### Multi-cluster topology
In a **multi-cluster topology**, where the observability plane runs in a separate cluster
from the data-plane / workflow-plane clusters, install the Helm chart in those clusters with Fluent Bit enabled and OpenObserve components disabled
to start collecting logs from the cluster and publish them to the observability plane cluster's OpenObserve endpoint.

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.4.1 \
  --set fluent-bit.enabled=true \
  --set openobserve-standalone.enabled=false \
  --set openObserveSetup.enabled=false \
  --set adapter.enabled=false
```
> **Note:**
>
> Make sure the `openobserve-admin-credentials` secret is available in the data-plane / workflow-plane clusters as well,
> and `fluent-bit.openObserveHost` and `fluent-bit.openObservePort` values are set to the OpenObserve endpoint exposed from the observability plane cluster,
> while `common.openObserveOrg` and `common.openObserveStream` match the organization and stream configured in the observability plane cluster.
