# Observability Tracing Module for Moesif

This module collects traces using [OpenTelemetry Collector](https://opentelemetry.io) and exports them to [Moesif](https://www.moesif.com).

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.
- A Moesif account and a **Collector Application ID** for each environment from [Moesif](https://www.moesif.com/).

## Installation

### Create a Kubernetes Secret

Create a Kubernetes secret containing your Moesif Collector Application IDs, with one key per environment.

First, get the environment names and their UIDs:

```bash
kubectl get environments -o custom-columns="NAME:.metadata.name,UID:.metadata.uid"
```

Use the environment **UID** as the secret key and the Moesif **Collector Application ID** as the value.

For example, if the output is:

```
NAME          UID
development   a1b2c3d4-e5f6-7890-abcd-ef1234567890
production    f9e8d7c6-b5a4-3210-fedc-ba0987654321
```

Create the secret using the UIDs as keys:

```bash
kubectl create secret generic moesif-tracing-collector-secret \
  --from-literal=a1b2c3d4-e5f6-7890-abcd-ef1234567890="YOUR_DEV_COLLECTOR_APP_ID" \
  --from-literal=f9e8d7c6-b5a4-3210-fedc-ba0987654321="YOUR_PROD_COLLECTOR_APP_ID" \
  --namespace openchoreo-observability-plane
```

### (Optional) Create a Search API Secret for Built-in Dashboards

> **Note:** This step is **optional** and only required if you want to populate the built-in dashboards with trace data from Moesif.
> The Management API key generation is a **paid feature** of Moesif. Configure this only if your Moesif plan supports it.

To generate an API key in Moesif:

1. Go to your Moesif dashboard and navigate to the **Management API Keys** section.
2. Create a new API key and select scopes under the **Analytics** section with **read** permission.
3. Create one key per environment, or use a single organization-level key.

Create the search secret using the environment **UID** as the key and the bearer token as the value:

```bash
kubectl create secret generic moesif-trace-search-secret \
  --from-literal=a1b2c3d4-e5f6-7890-abcd-ef1234567890="YOUR_DEV_MANAGEMENT_API_BEARER_TOKEN" \
  --from-literal=f9e8d7c6-b5a4-3210-fedc-ba0987654321="YOUR_PROD_MANAGEMENT_API_BEARER_TOKEN" \
  --namespace openchoreo-observability-plane
```

### Install the Helm Chart

```bash
helm upgrade --install observability-tracing-moesif \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-moesif \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0
```


### Configuration Options

For easier configuration management, create a `values.yaml` file:

```yaml
# values.yaml

moesif:
  # List of environments to collect traces from.
  # Get name and id by running: kubectl get environments -o custom-columns="NAME:.metadata.name,UID:.metadata.uid"
  environments:
    - name: development
      id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    - name: production
      id: f9e8d7c6-b5a4-3210-fedc-ba0987654321

  # Moesif adapter configuration
  adapter:
    enabled: true

opentelemetryCollectorCustomizations:
  debug:
    enabled: false  # Enable debug exporter for troubleshooting

  tailSampling:
    enabled: true  # Enable tail-based sampling
    decisionWait: 10s
    numTraces: 100
    expectedNewTracesPerSec: 10
    decisionCache:
      sampledCacheSize: 10000
      nonSampledCacheSize: 1000
    spansPerSecond: 10
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
| `moesif.environments` | List of environments with `name` and `id` (UID) to collect traces from | `[]` |
| `moesif.endpoint` | (Optional) Moesif API endpoint URL | `https://api.moesif.net` |
| `moesif.adapter.enabled` | Enable the Moesif adapter for trace search | `true` |
| `moesif.adapter.searchEndpoint` | Moesif search API endpoint | `https://api.moesif.com` |
| `moesif.adapter.searchSecretName` | Secret name for Moesif search credentials | `moesif-search-credentials` |
| `opentelemetryCollectorCustomizations.debug.enabled` | Enable debug exporter for troubleshooting | `false` |
| `opentelemetryCollectorCustomizations.tailSampling.enabled` | Enable tail-based sampling | `true` |
| `opentelemetryCollectorCustomizations.tailSampling.decisionWait` | Wait time before making a sampling decision | `10s` |
| `opentelemetryCollectorCustomizations.tailSampling.numTraces` | Number of traces kept in memory | `100` |
| `opentelemetryCollectorCustomizations.tailSampling.expectedNewTracesPerSec` | Expected number of new traces per second | `10` |
| `opentelemetryCollectorCustomizations.tailSampling.decisionCache.sampledCacheSize` | Size of sampled decision cache | `10000` |
| `opentelemetryCollectorCustomizations.tailSampling.decisionCache.nonSampledCacheSize` | Size of non-sampled decision cache | `1000` |
| `opentelemetryCollectorCustomizations.tailSampling.spansPerSecond` | Rate limit for spans per second | `10` |

## How It Works

This module deploys an **OpenTelemetry Collector** that:

1. Receives OTLP traces (gRPC on port `4317`, HTTP on port `4318`) from instrumented workloads.
2. Enriches spans with Kubernetes metadata (pod name, deployment, namespace, etc.) using the `k8sattributes` processor.
3. Routes traces to the correct Moesif application based on the environment UID.
4. Exports traces to Moesif using the Moesif Collector Application ID stored in the `moesif-tracing-collector-secret` Kubernetes secret.

## Troubleshooting

### Check OpenTelemetry Collector logs

```bash
kubectl -n openchoreo-observability-plane logs -f deploy/moesif-tracing-collector
```

### Verify the secret exists

```bash
kubectl -n openchoreo-observability-plane get secret moesif-tracing-collector-secret
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
kubectl delete secret moesif-tracing-collector-secret \
  --namespace openchoreo-observability-plane
```
