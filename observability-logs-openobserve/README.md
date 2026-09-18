# Observability Logs Module for OpenObserve

|               |                                                                                                                                                                                           |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_logs_openobserve)](https://codecov.io/gh/openchoreo/community-modules) |

This module collects container logs using [Fluent Bit](https://fluentbit.io) and stores them in [OpenObserve](https://openobserve.ai).

> **Note:** The commands in this README install the latest module version. Refer to the [Compatibility](#compatibility) table below for the module version compatible with your OpenChoreo version.

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work. The observer reaches this module's adapter at `http://logs-adapter:9098`, which is the default value of `observer.logsAdapter.url` in the `openchoreo-observability-plane` helm chart, so no change to the observability plane is needed unless that value was overridden.

## Installation

### Pre-requisites

OpenObserve credentials are required to configure it during installation and to access it. OpenChoreo uses the External Secrets Operator to manage secrets. Add your OpenObserve credentials (`ZO_ROOT_USER_EMAIL` and `ZO_ROOT_USER_PASSWORD`) to a secret store and use an `ExternalSecret` resource to generate a Kubernetes secret named `openobserve-admin-credentials` from it.
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
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev
```

To switch to HA mode, disable the standalone chart and enable the distributed chart:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --reuse-values \
  --set openobserve-standalone.enabled=false \
  --set openobserve.enabled=true
```

Refer to the [openobserve Helm chart documentation](https://github.com/openobserve/openobserve-helm-chart/tree/main/charts/openobserve) to configure the distributed deployment.

## Enable log collection

### Single-cluster topology

In a **single-cluster topology**, where the observability plane runs in the same cluster
as the data-plane / workflow-plane clusters, enable Fluent Bit in the already installed Helm chart
to start collecting logs from the cluster and publish them to OpenObserve:

```bash
helm upgrade observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --reuse-values \
  --set fluent-bit.enabled=true \
  --set fluentBitCustomizations.clusterInstance=<cluster-name>
```

> **Note:** `fluentBitCustomizations.clusterInstance` is required. It names the cluster the
> records were collected from and is stamped on every log record, so pick a value that is
> unique across the clusters reporting to this observability plane.

### Multi-cluster topology

In a **multi-cluster topology**, where the observability plane runs in a separate cluster
from the data-plane / workflow-plane clusters, log data flows from the remote Fluent Bit
instances, through the observability-plane gateway (`gateway-default`), into OpenObserve.
You need two things:

1. **On the observability plane cluster**: expose the OpenObserve ingest endpoint through the gateway so remote Fluent Bit instances can reach it.
2. **On each remote cluster**: install this chart with only Fluent Bit enabled, pointed at the obs cluster's gateway endpoint.

#### Observability plane cluster setup

Install the chart normally, and set `common.httpRouteHostnames` to the gateway
hostname that remote Fluent Bit instances will target. This creates an `HTTPRoute` on
`gateway-default` that routes the OpenObserve ingest path (`/api/<org>/<stream>/_json`) to the
in-cluster `openobserve` Service:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set-json common.httpRouteHostnames='["openobserve.<OBS_BASE_DOMAIN>"]'
```

> **Note:** The observability plane gateway needs an HTTP/HTTPS listener whose hostname matches
> `openobserve.<OBS_BASE_DOMAIN>`.

#### Remote cluster setup (data-plane / workflow-plane clusters)

Install the chart with only Fluent Bit enabled and the OpenObserve components disabled, pointed at
the gateway endpoint:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set fluent-bit.enabled=true \
  --set fluentBitCustomizations.clusterInstance=<cluster-name> \
  --set openobserve-standalone.enabled=false \
  --set openobserve.enabled=false \
  --set openObserveSetup.enabled=false \
  --set adapter.enabled=false \
  --set common.openObserveHost=openobserve.<OBS_BASE_DOMAIN> \
  --set common.openObservePort=<gateway-port> \
  --set common.openObserveTlsEnabled=true
```

> **Note:**
>
> - `fluentBitCustomizations.clusterInstance` is required and must be unique per remote cluster — it is stamped on every log record and is what the platform logs API filters on to tell the clusters apart.
> - The `openobserve-admin-credentials` secret must exist on the remote clusters as well, because Fluent Bit basic-authenticates directly to OpenObserve. If you don't have a shared secret backend, create it manually (see the [Multi-Cluster Connectivity](https://openchoreo.dev/docs/platform-engineer-guide/multi-cluster-connectivity/) guide).
> - `common.openObserveHost` and `common.openObservePort` must point at the gateway endpoint exposed from the observability plane cluster, and `common.openObserveHost` **must be the same hostname** listed in `common.httpRouteHostnames` on the observability plane release — Fluent Bit sends this value both as the connection address and as the HTTP `Host` header, so the two have to match for the `HTTPRoute` to select this module's route instead of another one on the same gateway (Fluent Bit's HTTP output has no option to send a different `Host` header than the one it connects to).
> - Set `common.openObserveTlsEnabled=true` if the obs gateway listener is HTTPS, or `false` if it is plain HTTP.
> - `common.openObserveOrg` and `common.openObserveStream` must match the organization and stream configured in the observability plane cluster.
> - The adapter and setup job are disabled because they only need to run on the observability plane cluster.
> - On the **control plane** cluster, add `--set auditLogs.enabled=true` to also collect the audit trail. See [Enable audit log collection](#enable-audit-log-collection).

## Enable audit log collection

OpenChoreo's audit trail is written by `openchoreo-api` and `observer` to their container
logs. This module can route those records to a stream of their own, `audit_logs`, kept
under its own retention:

```bash
helm upgrade observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --reuse-values \
  --set fluent-bit.enabled=true \
  --set fluentBitCustomizations.clusterInstance=<cluster-name> \
  --set auditLogs.enabled=true
```

In a multi-cluster topology, set `auditLogs.enabled` on the cluster running `openchoreo-api`
and `observer`, which is not necessarily the one running OpenObserve.

### Multi-cluster audit collection

`observer` runs in the observability plane cluster and `openchoreo-api` in the control plane cluster, so both need Fluent Bit with audit enabled. Data plane and workflow plane clusters run neither producer and do not need `auditLogs.enabled`.

On the **observability plane cluster**, Fluent Bit ships to the in-cluster OpenObserve:

```bash
helm upgrade observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --reuse-values \
  --set fluent-bit.enabled=true \
  --set fluentBitCustomizations.clusterInstance=<op-cluster-name> \
  --set auditLogs.enabled=true
```

On the **control plane cluster**, install the chart with only Fluent Bit enabled, as in [Remote cluster setup](#remote-cluster-setup-data-plane--workflow-plane-clusters), with audit enabled:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set fluent-bit.enabled=true \
  --set fluentBitCustomizations.clusterInstance=<cp-cluster-name> \
  --set openobserve-standalone.enabled=false \
  --set openobserve.enabled=false \
  --set openObserveSetup.enabled=false \
  --set adapter.enabled=false \
  --set common.openObserveHost=openobserve.<OBS_BASE_DOMAIN> \
  --set common.openObservePort=<gateway-port> \
  --set common.openObserveTlsEnabled=true \
  --set auditLogs.enabled=true
```

The `HTTPRoute` on the observability plane routes the audit stream's ingest path as well as the container logs stream's, so no extra gateway configuration is needed.

`auditLogs.producers` needs no per-cluster change: it matches on namespace and container name, not on the cluster. Each record carries the `openchoreo_cluster_instance` of the cluster it was collected in.

### Trusted producers

`auditLogs.producers` is an allowlist. **Each entry grants a workload the right to write into
the audit trail.** Entries match on the container log filename the kubelet writes, so a pod
outside the list that prints an audit-shaped line still lands in the container logs stream.

```yaml
auditLogs:
  producers:
    - producer: openchoreo-api
      namespace: openchoreo-control-plane
      container: api-server
    - producer: observer
      namespace: openchoreo-observability-plane
      container: observer
```

If the control plane or observability plane is installed into a non-default namespace, edit
these entries, or audit collection silently stops.

### Configuration

| Value                                     | Default                        | Purpose                                                                   |
| ----------------------------------------- | ------------------------------ | ------------------------------------------------------------------------- |
| `auditLogs.enabled`                       | `false`                        | Route audit records to their own stream                                   |
| `auditLogs.producers`                     | the two above                  | The trusted-producer allowlist                                            |
| `common.openObserveAuditStream`           | `audit_logs`                   | Stream name; lowercase letters, digits and `_` only                       |
| `openObserveSetup.auditLogsRetentionDays` | `365`                          | Audit stream retention in days, at least 3                                |
| `auditLogs.output.host`                   | `""`                           | OpenObserve to ship audit records to; empty uses `common.openObserveHost` |
| `auditLogs.output.port`                   | `common.openObservePort`       | Port of `auditLogs.output.host`                                           |
| `auditLogs.output.org`                    | `common.openObserveOrg`        | Organization on `auditLogs.output.host`                                   |
| `auditLogs.output.tlsEnabled`             | `common.openObserveTlsEnabled` | Use TLS to reach `auditLogs.output.host`                                  |

The setup job creates the audit stream with its retention on every install, and changing
the retention and upgrading applies it to the existing stream.

#### Shipping audit records to a separate OpenObserve

By default audit records go to the same OpenObserve as the container logs. To keep the audit trail elsewhere, for example one OpenObserve that several observability planes report their audit records to, set `auditLogs.output.host`:

```bash
--set auditLogs.output.host=openobserve-audit.<BASE_DOMAIN> \
--set auditLogs.output.port=<port> \
--set auditLogs.output.org=<org> \
--set auditLogs.output.tlsEnabled=true
```

Port, organization and TLS fall back to their `common.*` values when unset, and are ignored unless the host is set. The stream is still `common.openObserveAuditStream`. Setting the host also switches Fluent Bit to the `openobserve-audit-credentials` Secret, even if the host names the same instance, so it must exist in the release namespace with `ZO_ROOT_USER_EMAIL` and `ZO_ROOT_USER_PASSWORD` keys. If it is missing, Fluent Bit logs `variable ${AUDIT_OPENOBSERVE_USERNAME} is used but not set` and the destination rejects the writes. Container logs are unaffected.

Records from each cluster keep their `openchoreo_cluster_instance`, so a shared destination can still tell the clusters apart.

This chart only ships records to that OpenObserve. It does not configure it, and the adapter keeps reading audit logs from `common.openObserveHost`'s OpenObserve. On the destination, create the audit stream with its retention and set `ZO_INGEST_ALLOWED_UPTO` as described under [Limitations](#limitations). If the destination is another release of this chart reached through its gateway, `auditLogs.output.org` and `common.openObserveAuditStream` must match that release's `common.openObserveOrg` and `common.openObserveAuditStream`, because its `HTTPRoute` only routes that path.

### Limitations

- A record's time in OpenObserve is its `event_time`, and OpenObserve discards records older
  than `ZO_INGEST_ALLOWED_UPTO` hours. The bundled OpenObserve charts set it to `8760` to match
  the default retention. If you raise `auditLogsRetentionDays`, or ship to an OpenObserve this
  chart does not install, set it accordingly, or audit records delivered late are dropped.
- `resource.metadata` and `metadata` are returned in full, but OpenObserve flattens them into
  columns on ingest, so each distinct key adds a column to the stream.

## Applying Fluent Bit configuration changes

Fluent Bit reads its configuration from the `fluent-bit` ConfigMap rendered by this chart, and only
at startup. A `helm upgrade` that changes it, for example setting `auditLogs.enabled`, editing
`auditLogs.producers` or changing the `common.*` output settings, updates the ConfigMap but does not
restart the Fluent Bit pods, so they keep running the previous configuration. Restart the DaemonSet
after such an upgrade, in every cluster where the release was upgraded:

```bash
kubectl rollout restart daemonset/fluent-bit -n openchoreo-observability-plane
```

The same applies after the `openobserve-admin-credentials` Secret changes, because Fluent Bit reads
the credentials into its environment at startup. The adapter restarts on its own when its
configuration changes.

## Dependencies

Bundled upstream Helm charts:

| Chart                  | Repository                           |
| ---------------------- | ------------------------------------ |
| fluent-bit             | https://fluent.github.io/helm-charts |
| openobserve-standalone | https://charts.openobserve.ai        |
| openobserve            | https://charts.openobserve.ai        |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| OpenChoreo Version | Module Version |
| ------------------ | -------------- |
| v1.3.0 and later   | 0.7.x          |
| v1.2.x             | 0.6.x          |
| v1.0.x - v1.1.x    | 0.4.x - 0.5.x  |
