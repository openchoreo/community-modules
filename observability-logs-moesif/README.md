# Observability Logs Module for Moesif

This module collects container logs using [Fluent Bit](https://fluentbit.io) and exports them to [Moesif](https://www.moesif.com) via [OpenTelemetry Collector](https://opentelemetry.io).

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
kubectl create secret generic moesif-logs-collector-secret \
  --from-literal=development="YOUR_DEV_COLLECTOR_APP_ID" \
  --from-literal=production="YOUR_PROD_COLLECTOR_APP_ID" \
  --namespace openchoreo-observability-plane
```

### (Optional) Create a Search API Secret for Built-in Dashboards

> **Note:** This step is **optional** and only required if you want to populate the built-in dashboards with log data from Moesif.
> The Management API key generation is a **paid feature** of Moesif. Configure this only if your Moesif plan supports it.

To generate an API key in Moesif:

1. Go to your Moesif dashboard and navigate to the **Management API Keys** section.
2. Create a new API key and select scopes under the **Analytics** section with **read** permission.
3. Create one key per environment, or use a single organization-level key.

Create the search secret with a bearer token per environment:

```bash
kubectl create secret generic moesif-logs-search-secret \
  --from-literal=development="YOUR_DEV_MANAGEMENT_API_BEARER_TOKEN" \
  --from-literal=production="YOUR_PROD_MANAGEMENT_API_BEARER_TOKEN" \
  --namespace openchoreo-observability-plane
```

### Install the Helm Chart

```bash
helm upgrade --install observability-logs-moesif \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-moesif \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0
```

### Configuration Options

For easier configuration management, create a `values.yaml` file:

```yaml
# values.yaml

moesif:
  # List of environment names to collect logs from.
  # These must match the openchoreo.dev/environment label on your resources.
  environments:
    - development
    - production

  # Moesif adapter configuration
  adapter:
    enabled: true

opentelemetryCollectorCustomizations:
  debug:
    enabled: false  # Enable debug exporter for troubleshooting
```

Then install with:

```bash
helm upgrade --install observability-logs-moesif \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-moesif \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  -f moesif-logs-values.yaml
```

#### Configuration Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `moesif.environments` | List of environment names to collect logs from | `[development, production]` |
| `moesif.endpoint` | (Optional) Moesif API endpoint URL | `https://api.moesif.net` |
| `moesif.adapter.enabled` | Enable the Moesif adapter for log search | `true` |
| `moesif.adapter.searchEndpoint` | Moesif search API endpoint | `https://api.moesif.com` |
| `moesif.adapter.searchSecretName` | Secret name for Moesif search credentials | `moesif-search-credentials` |
| `opentelemetryCollectorCustomizations.debug.enabled` | Enable debug exporter for troubleshooting | `false` |

## How It Works

This module deploys two main components:

1. **Fluent Bit**: Collects logs from all containers in the cluster and forwards them to the OpenTelemetry Collector.
2. **OpenTelemetry Collector**: Receives logs from Fluent Bit, processes them, and routes them to the correct Moesif application based on the `openchoreo.dev/environment` resource attribute.

The module uses the Moesif Collector Application ID stored in the `moesif-logs-secret` Kubernetes secret to authenticate with the Moesif API.

## Troubleshooting

### Check OpenTelemetry Collector logs

```bash
kubectl -n openchoreo-observability-plane logs -f deploy/moesif-logs-collector
```

### Check Fluent Bit logs

```bash
kubectl -n openchoreo-observability-plane logs -f ds/fluent-bit
```

### Verify the secret exists

```bash
kubectl -n openchoreo-observability-plane get secret moesif-logs-secret
```

### Check pod health

```bash
kubectl -n openchoreo-observability-plane get pods
```

## Uninstalling

```bash
helm uninstall observability-logs-moesif \
  --namespace openchoreo-observability-plane
```

To also remove the secret:

```bash
kubectl delete secret moesif-logs-collector-secret \
  --namespace openchoreo-observability-plane
kubectl delete secret moesif-logs-search-secret \
  --namespace openchoreo-observability-plane  
  
```

