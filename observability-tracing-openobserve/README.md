# Observability Tracing Module for OpenObserve

|               |           |
| ------------- |-----------|
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_tracing_openobserve)](https://codecov.io/gh/openchoreo/community-modules) |

This module collects distributed traces using [OpenTelemetry collector](https://opentelemetry.io) and stores them in [OpenObserve](https://openobserve.ai).

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work. Deploy the `openchoreo-observability-plane` helm chart with the helm value `observer.tracingAdapter.enabled="true"` to enable the observer to fetch data from this tracing module.

## Installation

### Pre-requisites

1. OpenObserve credentials are required to configure it during installation and to access it. OpenChoreo uses the External Secrets Operator to manage secrets. Add your OpenObserve credentials (`ZO_ROOT_USER_EMAIL` and `ZO_ROOT_USER_PASSWORD`) to a secret store and use an `ExternalSecret` resource to generate a Kubernetes secret named `openobserve-admin-credentials` from it.
Refer to the [secret management guide](https://openchoreo.dev/docs/operations/secret-management/) for more details.

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
