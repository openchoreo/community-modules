# Observability Events Collector (OpenTelemetry)

|               |                                                                                                                                                                                                |
| ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_events_otel_collector)](https://codecov.io/gh/openchoreo/community-modules) |

This module deploys a purpose-built [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/)
distribution that collects **Kubernetes events** cluster-wide and enriches each
event with metadata of the object that triggered it — its **labels**,
**annotations**, and **controlling owner reference** — before shipping them to a
backend of your choice.

Unlike container logs, events are fired by many kinds of objects (Pods,
Deployments, Jobs, …), so they cannot be enriched with workload metadata by
pod-association alone. The bundled custom [`k8seventenrich`](k8seventenrichprocessor/README.md)
processor closes that gap: it keeps cluster-wide in-memory informer caches of
workload objects and, for each event, looks up the involved object and copies
its metadata onto the event. Enrichment is served entirely from cache, so it
adds no API-server calls on the event path.

Enriched keys land on the event's resource attributes:

| Source      | Attribute keys                     |
| ----------- | ---------------------------------- |
| Labels      | `k8s.object.label.<key>`           |
| Annotations | `k8s.object.annotation.<key>`      |
| Owner ref   | `k8s.object.owner.{kind,name,uid}` |

The default pipeline is `k8s_events → k8seventenrich → batch → exporter`; extra
processors can be inserted into it — see
[Customizing the pipeline](#customizing-the-pipeline). For the full event enrichment processor
reference, see [`k8seventenrichprocessor/README.md`](k8seventenrichprocessor/README.md).

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) installed with the **observability plane**
  enabled.
- A log/event backend to export to (e.g. an `observability-logs-*` module such
  as OpenSearch or OpenObserve). The collector is **backend-agnostic** and ships
  with a `debug` exporter by default — see [Choosing a backend](#choosing-a-backend).

## Installation

Install the chart into the observability plane namespace:

```bash
helm upgrade --install observability-events-otel-collector \
  oci://ghcr.io/openchoreo/helm-charts/observability-events-otel-collector \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.2.0
```

Out of the box the collector enriches events and prints them to its pod log via
the `debug` exporter (nothing is stored durably yet). Watch it run:

```bash
kubectl -n openchoreo-observability-plane logs -f deploy/events-collector
```

## Choosing a backend

The chart is backend-agnostic. Point it at a real backend by overriding
`exporters` (the exporter definition) and `pipelineExporters` (which exporters
are active in the pipeline). Credentials go in `collector.extraEnv`; exporter
auth helpers go in `extraExtensions`. The distribution bundles the OpenSearch,
OTLP (gRPC + HTTP), AWS CloudWatch Logs, and debug exporters.

### OpenSearch

Compatible with `observability-logs-opensearch` community module (>= version 0.6.1):

```bash
helm upgrade --install observability-events-otel-collector \
  oci://ghcr.io/openchoreo/helm-charts/observability-events-otel-collector \
  --namespace openchoreo-observability-plane --version 0.2.0 \
  -f - <<'EOF'
collector:
  extraEnv:
    - name: OPENSEARCH_USERNAME
      valueFrom:
        secretKeyRef:
          name: opensearch-admin-credentials
          key: username
    - name: OPENSEARCH_PASSWORD
      valueFrom:
        secretKeyRef:
          name: opensearch-admin-credentials
          key: password
extraExtensions:
  basicauth/opensearch:
    client_auth:
      username: ${env:OPENSEARCH_USERNAME}
      password: ${env:OPENSEARCH_PASSWORD}
exporters:
  opensearch:
    logs_index: "k8s-events"
    logs_index_time_format: "yyyy-MM-dd"
    http:
      endpoint: "https://opensearch:9200"
      tls:
        insecure_skip_verify: true
      auth:
        authenticator: basicauth/opensearch
pipelineExporters:
  - opensearch
EOF
```

### OpenObserve (native OTLP/HTTP)

Compatible with `observability-logs-openobserve` community module (>= version 0.5.0):

```bash
helm upgrade --install observability-events-otel-collector \
  oci://ghcr.io/openchoreo/helm-charts/observability-events-otel-collector \
  --namespace openchoreo-observability-plane --create-namespace --version 0.2.0 \
  -f - <<'EOF'
collector:
  extraEnv:
    - name: OPENOBSERVE_USERNAME
      valueFrom:
        secretKeyRef:
          name: openobserve-admin-credentials
          key: ZO_ROOT_USER_EMAIL
    - name: OPENOBSERVE_PASSWORD
      valueFrom:
        secretKeyRef:
          name: openobserve-admin-credentials
          key: ZO_ROOT_USER_PASSWORD

extraExtensions:
  basicauth/openobserve:
    client_auth:
      username: ${env:OPENOBSERVE_USERNAME}
      password: ${env:OPENOBSERVE_PASSWORD}

exporters:
  otlphttp/openobserve:
    endpoint: "http://openobserve:5080/api/default"
    auth:
      authenticator: basicauth/openobserve
    headers:
      stream-name: "k8s-events"

pipelineExporters:
  - otlphttp/openobserve
EOF
```

### AWS CloudWatch Logs

Compatible with `observability-logs-aws-cloudwatch`. By default the adapter's
setup job provisions the events log group with retention
(`events.provisionLogGroup=true`).

```yaml
exporters:
  awscloudwatchlogs:
    region: "us-east-1"
    log_group_name: "/aws/containerinsights/events"
    log_stream_name: "events"
pipelineExporters:
  - awscloudwatchlogs
```

> If you need a pipeline the structured values don't cover, set `configOverride` to a
> raw collector config and it replaces the rendered one entirely.

## Customizing the pipeline

Extra processors go in `extraProcessors` (the definitions) and are ordered by
`pipelineProcessors` (the chain), mirroring `exporters` / `pipelineExporters`.

The distribution is a curated build with the following upstream processor types.
They can be used alongside the built-in `k8seventenrich` and `batch`:

| Type       | Use                                                                                                                       |
| ---------- | ------------------------------------------------------------------------------------------------------------------------- |
| `resource` | Add/rewrite **resource** attributes — stamp origin metadata on every event.                                               |
| `filter`   | Drop events matching an [OTTL](https://opentelemetry.io/docs/collector/transforming-telemetry/) condition, before export. |

Order matters. **`batch` must be last** (if included) so batching happens
after all other processing — the chart fails the render if it isn't.
**`k8seventenrich` is required** — the render fails without it — and must come
**before** anything that reads its output (`k8s.object.label.*`,
`k8s.object.annotation.*`, `k8s.object.owner.*`).

> `pipelineProcessors` is a full **replacement** list, not a merge. To add one
> processor you must re-list `k8seventenrich` (mandatory) and `batch` (if needed).

### Stamping origin attributes (e.g. multi-cluster / multi-plane)

When several collectors fan into one backend, events need an origin attribute to
stay distinguishable at query time. Env values come from `collector.extraEnv` —
no separate mechanism is needed for `${env:...}`:

```bash
helm upgrade --install observability-events-otel-collector \
  oci://ghcr.io/openchoreo/helm-charts/observability-events-otel-collector \
  --namespace openchoreo-observability-plane --version 0.2.0 \
  -f - <<'EOF'
collector:
  extraEnv:
    - name: REGION
      value: us-east-1
    - name: PLANE_KIND
      value: dataplane
    - name: PLANE_NAME
      value: prod

extraProcessors:
  resource/cluster_identity:
    attributes:
      - { key: cloud.region, value: "${env:REGION}", action: upsert }
      - { key: openchoreo.plane_kind, value: "${env:PLANE_KIND}", action: upsert }
      - { key: openchoreo.plane_name, value: "${env:PLANE_NAME}", action: upsert }

pipelineProcessors:
  - k8seventenrich
  - resource/cluster_identity
  - batch
EOF
```

### Dropping noisy events

Events are high-volume; `filter` trims them before they cost anything downstream.
Conditions are OTTL and drop a record when they evaluate **true**:

```yaml
extraProcessors:
  filter/drop_kube_system:
    error_mode: ignore
    logs:
      log_record:
        - 'attributes["k8s.namespace.name"] == "kube-system"'

pipelineProcessors:
  - k8seventenrich
  - filter/drop_kube_system
  - batch
```

Filter on the event's own fields with `k8s.event.reason` /
`k8s.event.reporting_controller`, or on the enriched resource attributes that
`k8seventenrich` adds. Note the receiver emits no event-type _attribute_ — the
event's `Normal` / `Warning` type is carried as the log record's severity. To keep
only warnings and above:

```yaml
extraProcessors:
  filter/warnings_only:
    error_mode: ignore
    logs:
      log_record:
        - 'severity_text == "Normal"'

pipelineProcessors:
  - k8seventenrich
  - filter/warnings_only
  - batch
```

## Tuning enrichment

The `enrichment` value maps 1:1 to the `k8seventenrich` processor config.
**Labels** and **owner references** are enabled by default; **annotations** are
disabled (they are noisy and may carry sensitive values). Enable annotations
explicitly — ideally with an `include` allow-list:

```yaml
enrichment:
  labels:
    enabled: true
    include: # optional: only these labels (empty = all)
      - openchoreo.dev/component
      - openchoreo.dev/project
      - openchoreo.dev/environment
  annotations:
    enabled: true # off by default
    include:
      - prometheus.io/scrape
  # cache_sync_timeout: 2m         # raise for large/slow clusters (also raise
  # the deployment's startupProbe if > ~5m)
```

See the [processor reference](k8seventenrichprocessor/README.md#configuration)
for every option.

## De-duplication & restart safety

A pod restart could otherwise re-emit recently-seen events. Persistence is
**disabled by default** for a zero-dependency install, so a restart may re-emit
recent events if your backend doesn't tolerate occasional duplicates.

To make restarts replay-safe, enable persistence — the receiver then persists
its watch `resourceVersion` on a PVC (via the `file_storage` extension) and
resumes from it. This requires a `storageClassName` (or a cluster default
StorageClass):

```yaml
persistence:
  enabled: true
  size: 1Gi
  storageClassName: "" # set this, or rely on the cluster's default StorageClass
```

```bash
helm upgrade --install observability-events-otel-collector \
  oci://ghcr.io/openchoreo/helm-charts/observability-events-otel-collector \
  --namespace openchoreo-observability-plane --version 0.2.0 --reuse-values \
  --set persistence.enabled=true \
  --set persistence.storageClassName=<your-storage-class>
```

## Caveats

- **Single replica only.** The `k8seventsreceiver` is not horizontally scalable
  and the informer caches assume one collector owns the event stream. Do not
  raise `collector.replicaCount`.
- **Memory** scales with the number of objects across all watched kinds; raise
  `collector.resources.limits.memory` for large clusters, or trim watched kinds.
- **RBAC.** The chart grants cluster-wide `get/list/watch` on events and the
  watched workload kinds. If you manage RBAC externally (`rbac.create=false`),
  grant the same set or the collector will fail readiness at startup.

## Building the image locally

The image is a custom OCB distribution (see `builder-config.yaml`). CI builds
and publishes it on a chart-version bump, but to build locally:

```bash
make build         # OCB build → ./dist/otelcol-k8s-events
make unit-test     # processor unit tests
make docker-build  # container image (override DOCKER=... for a nerdctl wrapper)
```

## Compatibility

> **Note:** The chart version in the install commands targets the development
> version of OpenChoreo. Refer to the table below for the version matching your
> OpenChoreo installation.

| Module Version | OpenChoreo Version |
| -------------- | ------------------ |
| >= v0.1.x      | >= v1.2.x          |
