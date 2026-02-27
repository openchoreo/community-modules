# Observability Tracing Module for OpenObserve

This module collects distributed traces using Opentelemetry collector and stores them in OpenObserve.

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.

## Installation

Before installing, create Kubernetes Secrets with the OpenObserve admin credentials:

```bash
kubectl create secret generic openobserve-admin-credentials \
  --namespace openchoreo-observability-plane \
  --from-literal=username='root@example.com' \
  --from-literal=password='YOUR_PASSWORD'
```

Install this module in your OpenChoreo cluster using:

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://ghcr.io/openchoreo/charts/observability-tracing-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0
```

> **Note:** If OpenObserve is already installed by another module (e.g., `observability-logs-openobserve`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-tracing-openobserve \
>  oci://ghcr.io/openchoreo/charts/observability-tracing-openobserve \
>  --create-namespace \
>  --namespace openchoreo-observability-plane \
>  --version 0.1.0 \
>  --set openObserve.enabled=false
>```
