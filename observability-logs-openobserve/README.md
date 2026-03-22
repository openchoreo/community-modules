# Observability Logs Module for OpenObserve

|               |           |
| ------------- |-----------|
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_logs_openobserve)](https://codecov.io/gh/openchoreo/community-modules) |

This module collects container logs using [Fluent Bit](https://fluentbit.io) and stores them in [OpenObserve](https://openobserve.ai).

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
  --version 0.4.2
```

To switch to HA mode, disable the standalone chart and enable the distributed chart:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.4.2 \
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
  --version 0.4.2 \
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
  --version 0.4.2 \
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
