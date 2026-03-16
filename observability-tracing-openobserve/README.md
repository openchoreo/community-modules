[![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_tracing_openobserve)](https://codecov.io/gh/openchoreo/community-modules)

# Observability Tracing Module for OpenObserve

This module collects distributed traces using OpenTelemetry collector and stores them in OpenObserve.

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work. Deploy the `openchoreo-observability-plane` helm chart with the helm value `observer.tracingAdapter.enabled="true"` to enable the observer to fetch data from this tracing module.

## Installation

Before installing, create Kubernetes Secrets with the OpenObserve admin credentials:

> ⚠️ **Important:** Replace `YOUR_PASSWORD` with a strong, unique password.

```bash
kubectl create secret generic openobserve-admin-credentials \
  --namespace openchoreo-observability-plane \
  --from-literal=username='root@example.com' \
  --from-literal=password='YOUR_PASSWORD'
```

Install this module in your OpenChoreo cluster using:

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.4
```

> **Note:** If OpenObserve is already installed by another module (e.g., `observability-logs-openobserve`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-tracing-openobserve \
>  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-openobserve \
>  --create-namespace \
>  --namespace openchoreo-observability-plane \
>  --version 0.1.4 \
>  --set openObserve.enabled=false
>```
