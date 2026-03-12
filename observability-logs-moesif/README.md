# Observability Logs Module for Moesif

This module collects container logs using Fluent Bit and exports them to Moesif via OpenTelemetry Collector.

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.
- A Moesif account and Application ID from [Moesif](https://www.moesif.com/)

## Installation

### Create a Kubernetes Secret (Optional)

If you want to store your Moesif Application IDs in a Kubernetes Secret, create one with your credentials for each environment:

```bash
kubectl create secret generic moesif-app-secret \
  --from-literal=development="YOUR_DEV_APP_ID" \
  --from-literal=production="YOUR_PROD_APP_ID" \
  --namespace openchoreo-observability-plane
```

> **Note:** 
> - Create separate keys for each environment (e.g., `development`, `production`)
> - For environment names with hyphens (e.g., "my-env"), replace hyphens with underscores in the secret key name (e.g., "my_env")

### Install the Helm Chart

Install this module in your OpenChoreo cluster:

```bash
helm upgrade --install observability-logs-moesif \
  oci://ghcr.io/openchoreo/charts/observability-logs-moesif \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0
```

### Configuration Options

You can configure multiple environments and customize the Moesif endpoint:

#### Using a values.yaml file

For easier configuration management, create a `moesif-values.yaml` file:

```yaml
# moesif-values.yaml
# Configuration for Moesif log collection

moesif:
  # List of environment names to collect logs from
  # These must match the openchoreo.dev/environment label on your resources
  environments:
    - development
    - production
    - staging
  
  # (Optional) Moesif API endpoint
  # Uncomment to override the default endpoint
  # endpoint: "https://api.moesif.net"

# (Optional) Reference a pre-existing secret containing Moesif Application IDs
# Uncomment if you created the moesif-app-secret
# opentelemetry-collector:
#   extraEnvsFrom:
#     - secretRef:
#         name: moesif-app-secret
```

Then install with:

```bash
helm upgrade --install observability-logs-moesif \
  oci://ghcr.io/openchoreo/charts/observability-logs-moesif \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  -f moesif-values.yaml
```

#### Configuration Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `moesif.environments` | List of environment names to collect logs from | `[]` |
| `moesif.endpoint` | (Optional) Moesif API endpoint URL | `https://api.moesif.net` |

## How It Works

This module deploys two main components:

1. **Fluent Bit**: Collects logs from all containers in the cluster
2. **OpenTelemetry Collector**: Receives logs from Fluent Bit, processes them, and routes them to the appropriate Moesif application based on environment labels

The module uses the `openchoreo.dev/environment` label on your resources to route logs to the correct Moesif application.

## Troubleshooting

### Check OpenTelemetry Collector logs

```bash
kubectl -n openchoreo-observability-plane logs -f deploy/opentelemetry-collector
```

### Check Fluent Bit logs

```bash
kubectl -n openchoreo-observability-plane logs -f ds/fluent-bit
```

### Verify Secret Configuration

```bash
kubectl -n openchoreo-observability-plane get secret moesif-app-secret -o yaml
```

## Uninstalling

To remove this module:

```bash
helm uninstall observability-logs-moesif \
  --namespace openchoreo-observability-plane
```

To also remove the secret:

```bash
kubectl delete secret moesif-app-secret \
  --namespace openchoreo-observability-plane
```
