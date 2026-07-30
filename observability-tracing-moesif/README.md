# Observability Tracing Module for Moesif

This module collects traces using [OpenTelemetry Collector](https://opentelemetry.io) and exports them to [Moesif](https://www.moesif.com).

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.
- A Moesif account and a **Collector Application ID** for each environment from [Moesif](https://www.moesif.com/).

## Installation

### Create a Kubernetes Secret

Create a Kubernetes secret containing your Moesif Collector Application IDs, with one key per environment.

> **Note:**
> - Use the environment name as the key (e.g., `development`, `production`).
> - For environment names that contain hyphens (e.g., `my-env`), replace hyphens with underscores in the secret key (e.g., `my_env`).

```bash
kubectl create secret generic moesif-tracing-secret \
  --from-literal=development="YOUR_DEV_COLLECTOR_APP_ID" \
  --from-literal=production="YOUR_PROD_COLLECTOR_APP_ID" \
  --namespace openchoreo-observability-plane
```

### Configuration Options

For easier configuration management, create a `moesif-tracing-values.yaml` file:

```yaml
# moesif-tracing-values.yaml

moesif:
  # List of environment names to collect traces from.
  # These must match the openchoreo.dev/environment label on your resources.
  environments:
    - development
    - production

  # (Optional) Moesif API endpoint. Defaults to https://api.moesif.net
  # endpoint: "https://api.moesif.net"

opentelemetryCollectorCustomizations:
  tailSampling:
    enabled: false  # Enable tail-based sampling if needed
```

Then install with:

```bash
helm upgrade --install observability-tracing-moesif \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-moesif \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  -f moesif-tracing-values.yaml
```

#### Configuration Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `moesif.environments` | List of environment names to collect traces from | `[development, production]` |
| `moesif.endpoint` | (Optional) Moesif API endpoint URL | `https://api.moesif.net` |
| `opentelemetryCollectorCustomizations.tailSampling.enabled` | Enable tail-based sampling | `false` |

## How It Works

This module deploys an **OpenTelemetry Collector** that:

1. Receives OTLP traces (gRPC on port `4317`, HTTP on port `4318`) from instrumented workloads.
2. Enriches spans with Kubernetes metadata (pod name, deployment, namespace, etc.) using the `k8sattributes` processor.
3. Routes traces to the correct Moesif application based on the `openchoreo.dev/environment` resource attribute.
4. Exports traces to Moesif using the Moesif Collector Application ID stored in the `moesif-tracing-secret` Kubernetes secret.

## Troubleshooting

### Check OpenTelemetry Collector logs

```bash
kubectl -n openchoreo-observability-plane logs -f deploy/moesif-tracing-collector
```

### Verify the secret exists

```bash
kubectl -n openchoreo-observability-plane get secret moesif-tracing-secret
```

### Check pod health

```bash
kubectl -n openchoreo-observability-plane get pods
```

## Uninstalling

```bash
helm uninstall observability-tracing-moesif \
  --namespace openchoreo-observability-plane
```

To also remove the secret:

```bash
kubectl delete secret moesif-tracing-secret \
  --namespace openchoreo-observability-plane
```
