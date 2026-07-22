# Observability Logs Module for OpenObserve

|               |                                                                                                                                                                                           |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_logs_openobserve)](https://codecov.io/gh/openchoreo/community-modules) |

This module collects container logs using [Fluent Bit](https://fluentbit.io) and stores them in [OpenObserve](https://openobserve.ai).

> **Note:** The commands in this README install the latest module version. Refer to the [Compatibility](#compatibility) table below for the module version compatible with your OpenChoreo version.

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work. Deploy the `openchoreo-observability-plane` helm chart with the helm value `observer.logsAdapter.enabled="true"` to enable the observer to fetch data from this logs module.

## Installation

### Pre-requisites

OpenObserve credentials are required to configure it during installation and to access it. OpenChoreo uses the External Secrets Operator to manage secrets. Add your OpenObserve credentials (`ZO_ROOT_USER_EMAIL` and `ZO_ROOT_USER_PASSWORD`) to a secret store and use an `ExternalSecret` resource to generate a Kubernetes secret named `openobserve-admin-credentials` from it.
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
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev
```

To switch to HA mode, disable the standalone chart and enable the distributed chart:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
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
  --version 0.0.0-latest-dev \
  --reuse-values \
  --set fluent-bit.enabled=true
```

### Multi-cluster topology

In a **multi-cluster topology**, where the observability plane runs in a separate cluster
from the data-plane / workflow-plane clusters, log data flows from the remote Fluent Bit
instances, through the observability-plane gateway (`gateway-default`), into OpenObserve.
You need two things:

1. **On the observability plane cluster**: expose the OpenObserve ingest endpoint through the gateway so remote Fluent Bit instances can reach it.
2. **On each remote cluster**: install this chart with only Fluent Bit enabled, pointed at the obs cluster's gateway endpoint.

#### Observability plane cluster setup

Install the chart normally, and set `common.httpRouteHostnames` to the gateway
hostname that remote Fluent Bit instances will target. This creates an `HTTPRoute` on
`gateway-default` that routes the OpenObserve ingest path (`/api/<org>/<stream>/_json`) to the
in-cluster `openobserve` Service:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set-json common.httpRouteHostnames='["openobserve.<OBS_BASE_DOMAIN>"]'
```

> **Note:** The observability plane gateway needs an HTTP/HTTPS listener whose hostname matches
> `openobserve.<OBS_BASE_DOMAIN>`.

#### Remote cluster setup (data-plane / workflow-plane clusters)

Install the chart with only Fluent Bit enabled and the OpenObserve components disabled, pointed at
the gateway endpoint:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set fluent-bit.enabled=true \
  --set openobserve-standalone.enabled=false \
  --set openobserve.enabled=false \
  --set openObserveSetup.enabled=false \
  --set adapter.enabled=false \
  --set common.openObserveHost=openobserve.<OBS_BASE_DOMAIN> \
  --set common.openObservePort=<gateway-port> \
  --set common.openObserveTls=On
```

> **Note:**
>
> - The `openobserve-admin-credentials` secret must exist on the remote clusters as well, because Fluent Bit basic-authenticates directly to OpenObserve. If you don't have a shared secret backend, create it manually (see the [Multi-Cluster Connectivity](https://openchoreo.dev/docs/platform-engineer-guide/multi-cluster-connectivity/) guide).
> - `common.openObserveHost` and `common.openObservePort` must point at the gateway endpoint exposed from the observability plane cluster, and `common.openObserveHost` should match the gateway hostname (`openobserve.<OBS_BASE_DOMAIN>`).
> - Set `common.openObserveTls=On` if the obs gateway listener is HTTPS, or `Off` if it is plain HTTP.
> - `common.openObserveOrg` and `common.openObserveStream` must match the organization and stream configured in the observability plane cluster.
> - The adapter and setup job are disabled because they only need to run on the observability plane cluster.

## Dependencies

Bundled upstream Helm charts:

| Chart | Repository |
| ----- | ---------- |
| fluent-bit | https://fluent.github.io/helm-charts |
| openobserve-standalone | https://charts.openobserve.ai |
| openobserve | https://charts.openobserve.ai |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
| -------------- | ------------------ |
| >= v0.5.x      | >= v1.2.x          |
| >= v0.4.x      | >= v1.0.x          |
