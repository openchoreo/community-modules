# Observability Tracing Module for OpenSearch

[![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?flag=observability-tracing-opensearch)](https://app.codecov.io/gh/openchoreo/community-modules?flags%5B0%5D=observability-tracing-opensearch)

This module collects traces using [OpenTelemetry collector](https://opentelemetry.io) and stores them in [OpenSearch](https://opensearch.org).

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work.

## Installation

### Pre-requisites

1. OpenSearch setup scripts in this helm chart need admin credentials to connect to OpenSearch and configure it. OpenChoreo uses the External Secrets Operator to manage secrets. Add your OpenSearch credentials (username and password) to a secret store and use an `ExternalSecret` resource to generate a Kubernetes secret from it.
   Refer to the [secret management guide](https://openchoreo.dev/docs/platform-engineer-guide/secret-management/) for more details.

For example, the command below pulls values from the `ClusterSecretStore` created earlier in the [OpenChoreo installation guide](https://openchoreo.dev/docs).

```bash
kubectl apply -f - <<EOF
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: opensearch-admin-credentials
  namespace: openchoreo-observability-plane
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: opensearch-admin-credentials
  data:
  - secretKey: username
    remoteRef:
      key: opensearch-username
      property: value
  - secretKey: password
    remoteRef:
      key: opensearch-password
      property: value
EOF
```

2. If you wish to use the Kubernetes operator-based OpenSearch version included with this Helm chart, install the operator as follows

```bash
helm repo add opensearch-operator https://opensearch-project.github.io/opensearch-k8s-operator/
helm repo update
helm upgrade --install opensearch-operator opensearch-operator/opensearch-operator \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 2.8.0 \
  --set kubeRbacProxy.image.repository=quay.io/brancz/kube-rbac-proxy \
  --set kubeRbacProxy.image.tag=v0.15.0
```

### Deploy Helm chart

### Installation modes

This chart supports three `global.installationMode` values:

- **`singleCluster`**: Deploy everything (OpenTelemetry Collector + OpenSearch) into a single cluster (uses when dataplane and observability plane are in the same cluster).
- **`multiClusterReceiver`**: Deploy OpenTelemetry Collector as a central receiver into the observability plane cluster. It accepts OTLP from remote clusters and writes traces to OpenSearch.
- **`multiClusterExporter`**: Deploy OpenTelemetry Collector as an exporter into each dataplane cluster. It receives OTLP from in-cluster workloads and exports to the receiver in the observability plane cluster using OTLP.

#### Single-cluster topology

> **Note:** If you wish to use the Kubernetes operator-based OpenSearch version, add `--set openSearch.enabled=false --set openSearchCluster.enabled=true --set openSearchCluster.credentialsSecretName="opensearch-admin-credentials"` flags when installing the Helm chart. The admin password will be read from the credentials secret at install time.

```bash
helm upgrade --install observability-tracing-opensearch \
  oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
```

> **Note:** If OpenSearch is already installed by another module (e.g., `observability-logs-opensearch`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-tracing-opensearch \
>   oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-opensearch \
>   --create-namespace \
>   --namespace openchoreo-observability-plane \
>   --version 0.0.0-latest-dev \
>   --set openSearch.enabled=false \
>   --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
> ```

#### Multi-cluster topology

In multi-cluster mode you typically install:

- **Receiver (observability plane cluster)**: `global.installationMode=multiClusterReceiver`
- **Exporter (each dataplane cluster)**: `global.installationMode=multiClusterExporter`

### 1) Install the receiver (observability plane cluster)

Install the chart in the observability plane cluster/namespace (this is the cluster that has OpenSearch and will store traces):

```bash
helm upgrade --install observability-tracing-opensearch \
  oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set global.installationMode="multiClusterReceiver" \
  --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
```

> **Note:** If OpenSearch is already installed by another module (e.g., `observability-logs-opensearch`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-tracing-opensearch \
>   oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-opensearch \
>   --create-namespace \
>   --namespace openchoreo-observability-plane \
>   --version 0.0.0-latest-dev \
>   --set openSearch.enabled=false \
>   --set global.installationMode="multiClusterReceiver" \
>   --set openSearchSetup.openSearchSecretName="opensearch-admin-credentials"
> ```

### 2) Install an exporter (each dataplane cluster)

Install the chart in each dataplane cluster. The exporter does **not** need OpenSearch or the OpenSearch setup job; it only needs to export OTLP to the receiver.

Set `opentelemetryCollectorCustomizations.http.observabilityPlaneUrl` to the receiver endpoint (for example: `http://opentelemetry.<gateway-domain>:<port>`).
If the observability plane gateway is exposed over a TLS/HTTPS listener, use the `https://` scheme instead (for example: `https://opentelemetry.<gateway-domain>:<port>`).
Also set `opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost` if observabilityPlaneUrl differs from the gateway hostname.

```bash
helm upgrade --install observability-tracing-opensearch \
  oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set global.installationMode="multiClusterExporter" \
  --set openSearch.enabled=false \
  --set openSearchCluster.enabled=false \
  --set openSearchSetup.enabled=false \
  --set adapter.enabled=false \
  --set-json opentelemetry-collector.extraEnvs="[]" \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneUrl="http://opentelemetry.<gateway-domain>:<port>" \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost="opentelemetry.<gateway-domain>"
```

## Features

| Feature                | Module version  | Setup                                                                                    |
| ---------------------- | --------------- | ---------------------------------------------------------------------------------------- |
| Trace and span queries | All             | None                                                                                     |
| Release line filtering | 0.9.0 and later | When upgrading from 0.8.x, see Upgrades                                                  |
| Raw trace queries      | 0.9.2 and later | The read-only user's Secret; optionally, `openSearchSetup.traceIndexMappings`. See below |

### Raw trace queries

The adapter serves raw trace queries (`POST /api/v1alpha1/traces/raw-query` on the OpenChoreo Observer). The request
body is an OpenSearch search body, sent as `application/json`, and the response is OpenSearch's own search response.
For example:

```json
{
  "size": 0,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "startTime": {
              "gte": "2026-10-20T08:00:00Z",
              "lte": "2026-10-20T09:00:00Z"
            }
          }
        },
        { "term": { "status.code": "Error" } }
      ]
    }
  },
  "aggs": {
    "by_service": { "terms": { "field": "resource.service.name", "size": 20 } }
  }
}
```

How a raw query runs:

- **Read-only, trace data only:** the body runs through the search API only, against the trace indices
  (`otel-traces-*`), as a read-only OpenSearch user whose role can only read those indices. Bodies that read other
  indices (terms lookups, `more_like_this` documents, indexed shapes, percolate) are rejected, and so are `global`
  aggregations. A body that isn't JSON gets 415.
- **Request as sent:** the body runs as written, including its own time range and `size`. Without a time range, the
  query reads every trace the indices hold. The adapter only sets the search `timeout`.
- **Limits:** a query timeout (default `30s`, `adapter.rawQuery.timeout`) and a maximum response size (default 10 MiB,
  `adapter.rawQuery.maxResponseBytes`). A query that runs past the timeout, including one OpenSearch stops with
  partial results, gets 504; a larger result is rejected rather than returned partially.

#### Read-only user

Raw queries run as a read-only OpenSearch user whose credentials you provide in a Secret of their own,
`opensearch-traces-users` by default (`adapter.rawQuery.credentialsSecretName`), apart from the admin credentials:

- `rawQueryUsername`: the user name, for example `openchoreo-traces-reader`
- `rawQueryPassword`: its password, which must meet OpenSearch's password rules

The setup job creates that user in OpenSearch through the security REST API, using the admin credentials, with a
role, `openchoreo_traces_reader`, that can only read the trace indices, and sets the password again on every upgrade.
If the setup job is disabled, create the user and role yourself.

To sync the Secret from a secret store, add the two entries to the store and an ExternalSecret for them, for example:

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: opensearch-traces-users
  namespace: openchoreo-observability-plane
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: opensearch-traces-users
  data:
    - secretKey: rawQueryUsername
      remoteRef: { key: opensearch-raw-query-username, property: value }
    - secretKey: rawQueryPassword
      remoteRef: { key: opensearch-raw-query-password, property: value }
```

Without the Secret or its keys, the module installs and works as before, and the adapter answers raw queries with 501. After providing them, run `helm upgrade` so the setup job creates the user, then restart the adapter so it reads
them.

#### Searchable span attributes

The trace index template maps only the fields the adapter itself queries. Span attributes are stored, but a raw
query can't match on them until they are mapped. Add mappings with `openSearchSetup.traceIndexMappings`:

```yaml
openSearchSetup:
  traceIndexMappings:
    attributes.db.system.name: { type: keyword }
    attributes.http.route: { type: keyword }
```

The setup job adds them to the index template, so new trace indices get them, and to the mappings of the existing
trace indices, so spans written from then on are searchable on them. It never reindexes: spans stored before a field
was mapped can't be matched on it until you reindex them yourself.

Reindexing rewrites every span in an index, so do it one index at a time, as a background task, when the cluster has
spare capacity:

```text
# List the trace indices
GET _cat/indices/otel-traces-*?h=index

# Reindex one in place; the response has a task ID
POST <index>/_update_by_query?conflicts=proceed&wait_for_completion=false

# Follow its progress
GET _tasks/<task-id>
```

The spans are rewritten in place, so if a task stops partway, run it again for that index.

## Upgrades

If you are upgrading from a version before 0.4.0, reindex the older tracing data to work with newer queries

```bash
curl -fsSL https://raw.githubusercontent.com/openchoreo/community-modules/refs/heads/main/observability-tracing-opensearch/scripts/upgrade-to-0-4-1.sh | bash
```

If you are upgrading from 0.8.x, add the release line fields to the existing trace indices, so spans written to them can be filtered by release line

```bash
curl -fsSL https://raw.githubusercontent.com/openchoreo/community-modules/refs/heads/main/observability-tracing-opensearch/scripts/upgrade-to-0-9.sh | bash
```

### Upgrading to OpenSearch 3.9.0

Module versions 0.8.1, 0.9.1 and later bundle OpenSearch 3.9.0. Earlier versions, including 0.8.0 and 0.9.0, bundle 3.3.0. This applies when the module runs its own OpenSearch (`openSearch.enabled=true`, the default, or `openSearchCluster.enabled=true`). When traces are stored in an OpenSearch installed by another module, such as `observability-logs-opensearch`, follow that module's upgrade notes instead. Before the first upgrade to a version with 3.9.0:

- **Take a snapshot of OpenSearch.** Once OpenSearch 3.9.0 has opened the data, it cannot be rolled back to 3.3.0; `helm rollback` succeeds but OpenSearch fails to start. Restoring a [snapshot](https://docs.opensearch.org/latest/tuning-your-cluster/availability-and-recovery/snapshots/index/) is the only way back. For the same reason, never move from a version with 3.9.0 to one with 3.3.0: from 0.8.1, upgrade to 0.9.1 or later, not to 0.9.0.
- **Expect OpenSearch to restart.** With the subchart OpenSearch (`openSearch.enabled=true`), its single pod restarts. With the operator (`openSearchCluster.enabled=true`), the operator upgrades the nodes one at a time and then restarts the data nodes once more. OpenSearch 3.9.0 uses about twice the CPU of 3.3.0 while starting, so upgrade when the nodes have spare CPU.

> **Note:**
>
> - Upgrade with `--reset-then-reuse-values` (Helm 3.14 or later). `--reuse-values` keeps the previous chart's defaults, so OpenSearch stays on 3.3.0.
> - With Helm 4 and the operator-based OpenSearch, add `--server-side=false`. The operator updates fields of the `OpenSearchCluster` resource after it is created, and Helm 4's server-side apply rejects any upgrade that sets them again.

## Dependencies

Bundled upstream Helm charts:

| Chart                   | Repository                                                 |
| ----------------------- | ---------------------------------------------------------- |
| opensearch              | https://opensearch-project.github.io/helm-charts/          |
| opentelemetry-collector | https://open-telemetry.github.io/opentelemetry-helm-charts |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| OpenChoreo Version | Module Version |
| ------------------ | -------------- |
| 1.4.0 and later    | 0.9.x          |
| 1.2.x - 1.3.x      | 0.6.x, 0.8.x   |
| 1.1.x              | 0.4.x          |
| 1.0.x              | 0.3.x          |
