# Observability Metrics Module with Prometheus

|               |                                                                                                                                                                                             |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_metrics_prometheus)](https://codecov.io/gh/openchoreo/community-modules) |

This module collects and stores metrics using [Prometheus](https://prometheus.io).

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work.

## Installation

### Installation modes

This chart supports three `global.installationMode` values:

- **`singleCluster`**: Deploy everything (full Prometheus stack + Metrics Adapter) into a single cluster (use when the dataplane and observability plane are in the same cluster).
- **`multiClusterReceiver`**: Deploy the full Prometheus stack as a central receiver in the observability plane cluster. Exposes a remote write endpoint that exporter clusters push metrics to.
- **`multiClusterExporter`**: Deploy a PrometheusAgent in each dataplane cluster. Scrapes metrics locally and remote-writes them to the central receiver.

### Single-cluster topology

```bash
helm upgrade --install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-prometheus \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.3.0
```

### Multi-cluster topology

#### 1) Install the receiver (observability plane cluster)

```bash
helm upgrade --install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-prometheus \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.3.0 \
  --set global.installationMode="multiClusterReceiver" \
  --set-json 'prometheusCustomizations.http.hostnames=["prometheus.observability.example.com"]'
```

#### 2) Install an exporter (each dataplane cluster)

Set `prometheusCustomizations.http.observabilityPlaneUrl` to the receiver endpoint. The `Host` header is derived automatically from the URL hostname, so ensure the URL uses the hostname that the gateway routes on.

```bash
helm upgrade --install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-prometheus \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.3.0 \
  --set global.installationMode="multiClusterExporter" \
  --set prometheusCustomizations.http.observabilityPlaneUrl=http://prometheus.observability.example.com:9091/api/v1/write \
  --set kube-prometheus-stack.prometheus.enabled=false \
  --set kube-prometheus-stack.alertmanager.enabled=false
```

#### Exporter configuration options

| Option                                                | Default    | Description                                                                           |
| ----------------------------------------------------- | ---------- | ------------------------------------------------------------------------------------- |
| `prometheusCustomizations.http.observabilityPlaneUrl` | (required) | Central receiver URL for remote write. The URL hostname is used as the `Host` header. |

## Verification

### Receiver cluster verification

Verify that the Prometheus receiver is running and accessible:

```bash
# Check Prometheus pod
kubectl get pods -n openchoreo-observability-plane -l app.kubernetes.io/name=prometheus

# Port-forward to access Prometheus UI
kubectl port-forward -n openchoreo-observability-plane svc/openchoreo-observability-prometheus 9091:9091

# Access http://localhost:9091 in your browser
```

### Exporter cluster verification

Verify that the PrometheusAgent is running and successfully writing metrics:

```bash
# Check PrometheusAgent pod
kubectl get pods -n openchoreo-observability-plane -l app.kubernetes.io/name=prometheus-agent

# Check PrometheusAgent logs for remote write activity
kubectl logs -n openchoreo-observability-plane -l app.kubernetes.io/name=prometheus-agent -f | grep -i remote_write

# Verify remote write metrics are increasing in central cluster
# Access central Prometheus UI and query: rate(prometheus_remote_storage_samples_total[5m])
```

## Troubleshooting

### Remote write connection issues

If remote write from exporter to receiver is failing:

1. Verify network connectivity between clusters
2. Check the remote write URL configuration is correct
3. Review PrometheusAgent logs for connection errors: `kubectl logs -n openchoreo-observability-plane -l app.kubernetes.io/name=prometheus-agent`
4. Verify the central Prometheus is accepting remote writes: check the receiver HTTPRoute and gateway configuration

### Metrics not appearing in central cluster

1. Check that exporter Prometheus is scraping metrics locally (port-forward and check `/api/v1/targets`)
2. Verify remote write configuration in exporter logs
3. Check that queue capacity and batch settings are appropriate for your metrics volume
4. Monitor central Prometheus for import errors: `rate(prometheus_tsdb_symbol_table_size_bytes[5m])`
