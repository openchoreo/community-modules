# Observability Tracing Module for OpenObserve

[![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?flag=observability-tracing-openobserve)](https://app.codecov.io/gh/openchoreo/community-modules?flags%5B0%5D=observability-tracing-openobserve)

This module collects distributed traces using [OpenTelemetry collector](https://opentelemetry.io) and stores them in [OpenObserve](https://openobserve.ai).

> **Note:** The commands in this README install the latest module version. Refer to the [Compatibility](#compatibility) table below for the module version compatible with your OpenChoreo version.

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work. Deploy the `openchoreo-observability-plane` helm chart with the helm value `observer.tracingAdapter.enabled="true"` to enable the observer to fetch data from this tracing module.

## Installation

### Pre-requisites

1. OpenObserve credentials are required to configure it during installation and to access it. OpenChoreo uses the External Secrets Operator to manage secrets. Add your OpenObserve credentials (`ZO_ROOT_USER_EMAIL` and `ZO_ROOT_USER_PASSWORD`) to a secret store and use an `ExternalSecret` resource to generate a Kubernetes secret named `openobserve-admin-credentials` from it.
   Refer to the [secret management guide](https://openchoreo.dev/docs/platform-engineer-guide/secret-management/) for more details.

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
  oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev
```

To switch to HA mode, disable the standalone chart and enable the distributed chart:

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --reset-then-reuse-values \
  --set openobserve-standalone.enabled=false \
  --set openobserve.enabled=true
```

Refer to the [openobserve Helm chart documentation](https://github.com/openobserve/openobserve-helm-chart/tree/main/charts/openobserve) to configure the distributed deployment.

> **Note:** If OpenObserve is already installed by another module (e.g., `observability-logs-openobserve`), disable it to avoid conflicts:
>
> ```bash
> helm upgrade --install observability-tracing-openobserve \
>  oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-openobserve \
>  --create-namespace \
>  --namespace openchoreo-observability-plane \
>  --version 0.0.0-latest-dev \
>  --set openobserve-standalone.enabled=false
> ```

## Enable trace collection

### Installation modes

This chart supports three `global.installationMode` values:

- **`singleCluster`**: Deploy everything (OpenTelemetry Collector + OpenObserve) into a single cluster (used when the data plane and observability plane are in the same cluster).
- **`multiClusterReceiver`**: Deploy the OpenTelemetry Collector as a central receiver into the observability plane cluster. It accepts OTLP from remote clusters and writes traces to OpenObserve.
- **`multiClusterExporter`**: Deploy the OpenTelemetry Collector as an exporter into each data-plane cluster. It receives OTLP from in-cluster workloads and exports it to the receiver in the observability plane cluster over OTLP.

#### Single-cluster topology

In a **single-cluster topology**, where the observability plane runs in the same cluster
as the data-plane / workflow-plane clusters, the default `singleCluster` install (above) already
collects traces and writes them to the in-cluster OpenObserve.

#### Multi-cluster topology

In a **multi-cluster topology**, where the observability plane runs in a separate cluster
from the data-plane / workflow-plane clusters, trace data flows from the exporter collectors on
the remote clusters, through the observability-plane gateway (`gateway-default`), into the receiver
collector, which writes to OpenObserve. You typically install:

- **Receiver (observability plane cluster)**: `global.installationMode=multiClusterReceiver`
- **Exporter (each data-plane cluster)**: `global.installationMode=multiClusterExporter`

##### 1) Install the receiver (observability plane cluster)

Install the chart in the observability plane cluster. Set `opentelemetryCollectorCustomizations.http.hostnames` to the gateway hostname that
remote exporters will target, so the receiver's `HTTPRoute` is created on `gateway-default`:

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set global.installationMode="multiClusterReceiver" \
  --set-json opentelemetryCollectorCustomizations.http.hostnames='["opentelemetry.<OBS_BASE_DOMAIN>"]'
```

> **Note:** If OpenObserve is already installed by another module (e.g., `observability-logs-openobserve`), add `--set openobserve-standalone.enabled=false` to avoid conflicts.

##### 2) Install an exporter (each data-plane cluster)

Install the chart in each data-plane cluster. The exporter does **not** need OpenObserve, the adapter, or
the `openobserve-admin-credentials` secret — those credentials live only on the receiver. It only needs to
export OTLP to the receiver.

Set `opentelemetryCollectorCustomizations.http.observabilityPlaneUrl` to the receiver endpoint exposed
through the observability-plane gateway (for example: `http://opentelemetry.<OBS_BASE_DOMAIN>:<port>`).
If the gateway is exposed over a TLS/HTTPS listener, use the `https://` scheme instead. Also set
`opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost` when `observabilityPlaneUrl`
differs from the gateway hostname.

```bash
helm upgrade --install observability-tracing-openobserve \
  oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-tracing-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set global.installationMode="multiClusterExporter" \
  --set openobserve-standalone.enabled=false \
  --set openobserve.enabled=false \
  --set adapter.enabled=false \
  --set-json opentelemetry-collector.extraEnvs='[]' \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneUrl="http://opentelemetry.<OBS_BASE_DOMAIN>:<port>" \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost="opentelemetry.<OBS_BASE_DOMAIN>"
```

## Features

| Feature                | Module version  | Setup                                  |
| ---------------------- | --------------- | -------------------------------------- |
| Trace and span queries | All             | None                                   |
| Release line filtering | 0.5.0 and later | None                                   |
| Raw trace queries      | 0.5.0 and later | The raw query user's Secret. See below |

### Raw trace queries

The adapter serves raw trace queries (`POST /api/v1alpha1/traces/raw-query` on the OpenChoreo Observer). The request
body is an OpenObserve search body, sent as `application/json`, and the response is OpenObserve's own search response.
OpenObserve requires `start_time` and `end_time`, in microseconds. Span attributes are columns, with `.` in their names
replaced by `_`. For example:

```json
{
  "query": {
    "sql": "SELECT trace_id, operation_name, duration FROM \"default\" WHERE db_system_name = 'postgresql' AND span_status = 'ERROR' ORDER BY duration DESC LIMIT 50",
    "start_time": 1792483200000000,
    "end_time": 1792486800000000
  }
}
```

How a raw query runs:

- **Traces only:** the body runs only through OpenObserve's search API, in the adapter's organization, with the
  search type set to traces, so it can read trace streams and nothing else; a query that names a logs or metrics
  stream fails as if the stream didn't exist. Every trace stream in that organization is reachable, not only the
  configured one. OpenObserve's search only runs queries, so SQL that would change data is rejected.
- **Reviewed request keys only:** the body may use `query` (with `sql`, `start_time`, `end_time`, `from`, `size`,
  `quick_mode`, `query_type`, `track_total_hits`, `skip_wal`, `sampling_ratio`, `histogram_interval` and `timezone`),
  `encoding`, `use_cache` and `clear_cache`. Anything else, such as VRL functions (`query_fn`), actions or other
  clusters and regions, is rejected. A body that isn't JSON gets 415.
- **Request as sent:** the body runs as written, including its SQL, its time range and its row limit (`LIMIT` or
  `size`). The adapter only sets the search `timeout`.
- **Limits:** a query timeout (default `30s`, `adapter.rawQuery.timeout`) and a maximum response size (default 10 MiB,
  `adapter.rawQuery.maxResponseBytes`). OpenObserve stops a search at the timeout, and the query gets 504; a larger
  result is rejected rather than returned partially. OpenObserve doesn't stop a search when its caller goes away, so a
  canceled query still runs until it ends or reaches the timeout.
- **Errors:** a query OpenObserve rejects gets 400 with OpenObserve's message. So does a search OpenObserve answers
  only in part, for example because its time range is longer than the stream allows, with OpenObserve's reason; a
  partial result is never returned. When OpenObserve refuses the raw query user itself (401 or 403), the query gets
  500 and the adapter logs why: the user's credentials are wrong or out of date, so check the raw query user's Secret.

#### Raw query user

Raw queries run as an OpenObserve user of their own, never as the root user. You provide its credentials in a Secret
of their own, `openobserve-traces-users` by default (`adapter.rawQuery.credentialsSecretName`), apart from the admin
credentials:

- `rawQueryUsername`: the user's email, for example `traces-reader@example.com`. OpenObserve keeps emails trimmed and
  in lower case, so the module does the same. It must not be the root user, and should be an email no one else uses:
  the setup job sets the password and the admin role of the user with that email, whoever it is.
- `rawQueryPassword`: its password, 8 to 128 characters with a lowercase letter, an uppercase letter, a digit and a
  special character, as OpenObserve requires

The setup job creates that user in the organization through OpenObserve's users API, using the root credentials, and
sets the password again on every upgrade. If the setup job is disabled (`openObserveSetup.enabled`), create the user
yourself.

Open-source OpenObserve has no read-only role: its users are organization admins. The user is kept apart from the root
user, so it can't reach other organizations, and the adapter only sends it searches of trace streams, but anyone with
its credentials can do what an organization admin can. Keep the Secret as close as the admin credentials.

To sync the Secret from a secret store, add the two entries to the store and an ExternalSecret for them, for example:

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: openobserve-traces-users
  namespace: openchoreo-observability-plane
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: openobserve-traces-users
  data:
    - secretKey: rawQueryUsername
      remoteRef: { key: openobserve-raw-query-username, property: value }
    - secretKey: rawQueryPassword
      remoteRef: { key: openobserve-raw-query-password, property: value }
```

Without the Secret, or without both of its keys, the module installs and works as before, and the adapter answers raw
queries with 501. With only one of the two keys, the adapter doesn't start, and the setup job skips the user. After
providing them or changing the password, run `helm upgrade` so the setup job creates the user or sets the new password,
then restart the adapter so it reads them.

## Dependencies

Bundled upstream Helm charts:

| Chart | Repository |
| ----- | ---------- |
| opentelemetry-collector | https://open-telemetry.github.io/opentelemetry-helm-charts |
| openobserve-standalone | https://charts.openobserve.ai |
| openobserve | https://charts.openobserve.ai |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
|----------------|--------------------|
| >= v0.5.0      | >= v1.4.0          |
| >= v0.3.0      | >= v1.2.0          |
| >= v0.2.x      | >= v1.0.x          |
