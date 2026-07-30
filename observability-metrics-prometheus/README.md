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
  --version 0.7.0
```

### Multi-cluster topology

#### 1) Install the receiver (observability plane cluster)

```bash
helm upgrade --install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-prometheus \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.7.0 \
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
  --version 0.7.0 \
  --set global.installationMode="multiClusterExporter" \
  --set prometheusCustomizations.http.observabilityPlaneUrl=http://prometheus.observability.example.com:9091/api/v1/write \
  --set kube-prometheus-stack.prometheus.enabled=false \
  --set kube-prometheus-stack.alertmanager.enabled=false
```

> **Note:** If the observability plane gateway uses self-signed certificates, remote write will fail with `x509: certificate signed by unknown authority`. Add `--set prometheusCustomizations.remoteWrite.tlsInsecureSkipVerify=true` to skip TLS verification.

#### Exporter configuration options

| Option                                                       | Default    | Description                                                                           |
| ------------------------------------------------------------ | ---------- | ------------------------------------------------------------------------------------- |
| `prometheusCustomizations.http.observabilityPlaneUrl`        | (required) | Central receiver URL for remote write. The URL hostname is used as the `Host` header. |
| `prometheusCustomizations.remoteWrite.tlsInsecureSkipVerify` | `false`    | Skip TLS certificate verification for self-signed certs on the receiver.              |

## Storage and retention

In `singleCluster` and `multiClusterReceiver` modes, the Prometheus server stores its TimeSeries DataBase (TSDB) on a PersistentVolumeClaim by default, so metrics survive the pod being replaced by a restart, an eviction, a node drain or a chart upgrade. Alertmanager likewise persists its silences and notification log.

| Option                                                                                                            | Default   | Description                                                                             |
| ----------------------------------------------------------------------------------------------------------------- | --------- | --------------------------------------------------------------------------------------- |
| `kube-prometheus-stack.prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.resources.requests.storage` | `10Gi`    | Size of the Prometheus data volume.                                                     |
| `kube-prometheus-stack.prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.storageClassName`           | _(unset)_ | StorageClass for the Prometheus volume. Unset means the cluster's default StorageClass. |
| `kube-prometheus-stack.prometheus.prometheusSpec.retention`                                                       | `10d`     | Time-based retention.                                                                   |
| `kube-prometheus-stack.prometheus.prometheusSpec.retentionSize`                                                   | `8GiB`    | Size-based retention, ~80% of the volume. See [Sizing the volume](#sizing-the-volume).  |
| `kube-prometheus-stack.alertmanager.alertmanagerSpec.storage.volumeClaimTemplate.spec.resources.requests.storage` | `1Gi`     | Size of the Alertmanager data volume (silences and notification log).                   |

> **A default StorageClass is required.** With no default StorageClass and no explicit `storageClassName`, the PVCs stay `Pending` and the pods never start. Check with `kubectl get storageclass`.

### Using a specific StorageClass

```bash
helm upgrade --install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-prometheus \
  --namespace openchoreo-observability-plane --reuse-values \
  --set kube-prometheus-stack.prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.storageClassName=<your-storage-class> \
  --set kube-prometheus-stack.alertmanager.alertmanagerSpec.storage.volumeClaimTemplate.spec.storageClassName=<your-storage-class>
```

### Sizing the volume

`retention` and `retentionSize` both apply, and whichever is reached first evicts the oldest data. `retentionSize` must stay below the volume size, because Prometheus only deletes completed blocks to honour it, while the write-ahead log, the in-memory head chunks and compaction scratch space also occupy the volume and cannot be reclaimed. Leaving `retentionSize` unbounded on a fixed-size volume lets the TSDB fill the disk, after which writes fail and Prometheus crash-loops.

**Change both together**, keeping `retentionSize` at roughly 80% of the requested storage:

```bash
helm upgrade --install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-prometheus \
  --namespace openchoreo-observability-plane --reuse-values \
  --set kube-prometheus-stack.prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.resources.requests.storage=50Gi \
  --set kube-prometheus-stack.prometheus.prometheusSpec.retentionSize=40GiB
```

### Persisting the exporter agent WAL

In `multiClusterExporter` mode there is no TSDB — the PrometheusAgent only forwards samples — but it does buffer samples the receiver has not yet acknowledged in a WAL. That buffer is ephemeral by default, so samples are lost if the agent pod restarts while the receiver is unreachable. Enable a PVC for it on clusters where that matters:

| Option                                                        | Default         | Description                                                  |
| ------------------------------------------------------------- | --------------- | ------------------------------------------------------------ |
| `prometheusCustomizations.agent.persistence.enabled`          | `false`         | Persist the PrometheusAgent WAL on a PVC.                    |
| `prometheusCustomizations.agent.persistence.size`             | `2Gi`           | WAL volume size.                                             |
| `prometheusCustomizations.agent.persistence.storageClassName` | `""`            | StorageClass. Empty uses the cluster's default StorageClass. |
| `prometheusCustomizations.agent.persistence.accessMode`       | `ReadWriteOnce` | Access mode for the WAL volume.                              |

```bash
helm upgrade --install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-prometheus \
  --namespace openchoreo-observability-plane --reuse-values \
  --set prometheusCustomizations.agent.persistence.enabled=true \
  --set prometheusCustomizations.agent.persistence.storageClassName=<your-storage-class>
```

### Running without persistence

To use ephemeral storage — metrics are then lost on every pod restart — hand the operator an `emptyDir`:

```bash
helm upgrade --install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-prometheus \
  --namespace openchoreo-observability-plane --reuse-values \
  --set-json 'kube-prometheus-stack.prometheus.prometheusSpec.storageSpec.emptyDir={}' \
  --set-json 'kube-prometheus-stack.alertmanager.alertmanagerSpec.storage.emptyDir={}'
```

### Removing the data volumes

`helm uninstall` does **not** delete the PVCs — they are owned by the StatefulSets rather than by Helm, and the retention policy is `Retain`. A later reinstall reuses them along with their existing metrics. Note also that a retained PVC keeps its original size, so a reinstall with a larger `storage` value will not grow it. To discard the data explicitly:

```bash
kubectl get pvc -n openchoreo-observability-plane \
  -l 'app.kubernetes.io/instance=openchoreo-observability,app.kubernetes.io/name in (prometheus,alertmanager)'

kubectl delete pvc -n openchoreo-observability-plane \
  -l 'app.kubernetes.io/instance=openchoreo-observability,app.kubernetes.io/name in (prometheus,alertmanager)'
```

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

### Prometheus pod stuck in Pending

The data volume could not be provisioned. Inspect the PVC and its events:

```bash
kubectl get pvc -n openchoreo-observability-plane
kubectl describe pvc -n openchoreo-observability-plane \
  prometheus-openchoreo-observability-db-prometheus-openchoreo-observability-0
```

- `no persistent volumes available for this claim and no storage class is set` — the cluster has no default StorageClass. Set one explicitly, see [Using a specific StorageClass](#using-a-specific-storageclass).
- `waiting for first consumer to be created before binding` — expected for StorageClasses with `volumeBindingMode: WaitForFirstConsumer`, including k3s/k3d `local-path`. The PVC binds once the pod is scheduled, so check the pod's own events instead.

### Prometheus crash-looping with "no space left on device"

The TSDB filled its volume. Either grow the volume or lower `retentionSize` / `retention` — see [Sizing the volume](#sizing-the-volume). Note that `local-path` volumes are backed by node disk and do not enforce the requested size, so on k3d the TSDB can grow past `10Gi` and fill the node's disk; `retentionSize` is the effective cap there.

## Dependencies

Bundled upstream Helm charts:

| Chart                 | Repository                                         |
| --------------------- | -------------------------------------------------- |
| kube-prometheus-stack | https://prometheus-community.github.io/helm-charts |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above reflect the latest module version and is compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
| -------------- | ------------------ |
| >= v0.4.x      | v1.1.x             |
| v0.3.x         | v1.0.x             |
