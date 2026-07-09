# Observability Metrics Module for GCP Cloud Monitoring

This module exposes GCP Cloud Monitoring as an OpenChoreo metrics backend. It
serves per-component CPU/memory time series from the GKE system metrics that
Google's built-in agent publishes for every GKE cluster
(`resource.type="k8s_container"`), and manages alert rules as Cloud Monitoring
alert policies, with delivery back into OpenChoreo through a pre-existing
notification channel. Nothing extra has to be deployed in the data plane.

It targets GKE clusters with Workload Identity. Authentication uses Google
Application Default Credentials (ADC) against a Google service account bound to
the adapter's Kubernetes ServiceAccount.

## Table of contents

1. [Architecture](#architecture)
2. [Choose a deployment topology](#choose-a-deployment-topology)
3. [Prerequisites](#prerequisites)
4. [GCP IAM roles](#gcp-iam-roles)
5. [Installation on GKE](#installation-on-gke)
6. [Metric alerting](#metric-alerting)
7. [Shared webhook secret](#shared-webhook-secret)
8. [Troubleshooting](#troubleshooting)
9. [Configuration reference](#configuration-reference)
10. [Building and testing](#building-and-testing)
11. [Compatibility](#compatibility)

## Architecture

This module has two main responsibilities:

1. **Resource-metrics query** against Cloud Monitoring.
2. **Alerting** through Cloud Monitoring alert policies.

Metric collection is **not** in scope for this chart — GKE's built-in metrics
agent writes container CPU/memory metrics to Cloud Monitoring automatically.
This module reads from that metric store.

The chart deploys:

1. A Go **Cloud Monitoring Adapter** Deployment that implements the OpenChoreo
   Metrics Adapter API.
2. A Service, ServiceAccount (with a Workload Identity annotation), ConfigMap,
   and — optionally — a webhook Secret, a Gateway API HTTPRoute, and a
   NetworkPolicy.

Metrics are read from `k8s_container` time series. Cloud Monitoring attaches
the OpenChoreo pod labels as **system metadata**, filterable as
`metadata.user_labels."openchoreo.dev/*"`. Unlike Cloud Logging — where the
GKE agent surfaces pod labels under a `k8s-pod/` prefix with dots replaced by
underscores — Monitoring metadata keeps the raw label keys verbatim.

Component-, project-, and environment-scoped queries and alerts filter on the
three **UID** labels (`component-uid` / `project-uid` / `environment-uid`)
only, mirroring the Prometheus and Azure Monitor siblings. The rule's
`namespace` is deliberately **not** a metric filter: the control plane sends
the data-plane runtime namespace (`dp-<project>-<env>-…`) as the rule
namespace, whereas the pod's `openchoreo.dev/namespace` metadata label carries
the *control-plane* namespace, so filtering on it would match zero series.

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/metrics/query` (`metric: resource`) | Six parallel `ListTimeSeries` calls (CPU/memory usage, requests, limits) scoped by UID; returns per-metric aggregated series. |
| `POST /api/v1/metrics/query` (`metric: http`) | HTTP RED metrics not implemented; returns an empty series with the `X-OpenChoreo-Adapter-Notice: http-metrics-not-implemented` header (same as the Azure Monitor and AWS CloudWatch siblings). |
| `POST /api/v1alpha1/metrics/runtime-topology` | Not supported — GKE system metrics carry no pod-to-pod traffic data; returns `501` with error code `OBS-V1-M-GCP-501`. |
| `POST /api/v1alpha1/alerts/rules` | Creates a Cloud Monitoring alert policy (a usage÷limit ratio MetricThreshold) wired to the configured notification channel. Returns `409` if a rule with the same identity already exists (use `PUT` to replace). |
| `GET /api/v1alpha1/alerts/rules/{ruleName}` | Looks the policy up by its `openchoreo_rule_name` user label. |
| `PUT /api/v1alpha1/alerts/rules/{ruleName}` | Updates the rule's policy in place. Creates it if absent. |
| `DELETE /api/v1alpha1/alerts/rules/{ruleName}` | Deletes the alert policy. |
| `POST /api/v1alpha1/alerts/webhook` | Receives a fired-alert payload from the notification channel and forwards a normalised alert to the Observer. |
| `GET /healthz` | Readiness/liveness check. |

### How a fired alert flows back into OpenChoreo

Cloud Monitoring alert policies cannot call OpenChoreo controllers directly.
The path is:

```
GKE system metrics (k8s_container CPU/memory)
  → alert policy: usage÷limit ratio over the window, compare to threshold
  → GCP notification channel (webhook_basicauth) POSTs the incident
  → adapter /api/v1alpha1/alerts/webhook (Basic-auth password checked)
  → adapter forwards to the Observer's INTERNAL endpoint (:8081)
  → Observer correlates the ObservabilityAlertRule and dispatches the
    user-facing notification (email / Slack / webhook)
```

The notification channel is pure transport back into the cluster; the
user-facing delivery is configured separately via an
`ObservabilityAlertsNotificationChannel` resource.

## Choose a deployment topology

| Topology | Install location | Purpose | Required Helm values |
| --- | --- | --- | --- |
| Single cluster | The OpenChoreo cluster where the observability plane and workloads run together. | Deploys the adapter that queries Cloud Monitoring and manages alert rules. | Defaults. |
| Observability plane cluster | The cluster where the OpenChoreo observability plane is installed. | Deploys only the adapter. | Defaults. |
| Data-plane / workflow-plane cluster | Each cluster that runs OpenChoreo workloads. | No install. Workload clusters export system metrics to Cloud Monitoring via the GKE metrics agent directly. | N/A |

Cloud Monitoring is the shared managed backend. Remote workload clusters export
to the same project via the GKE metrics agent and do not need network
connectivity back to the observability plane. The adapter only runs where the
Observer needs to query metrics and manage rules.

## Prerequisites

### OpenChoreo prerequisites

- OpenChoreo is installed.
- The `openchoreo-observability-plane` Helm chart is installed.

See the [OpenChoreo documentation](https://openchoreo.dev/docs) for the base
installation steps.

### Local tooling

- `go` (1.26+)
- `gcloud` CLI, authenticated (`gcloud auth login`) with the target project set
  (`gcloud config set project <project>`)
- `helm` and `kubectl`

### GCP prerequisites

You need:

- A GCP project (`gcp.projectId`).
- A GKE cluster with **Cloud Monitoring / system metrics enabled** (the
  default), exporting container metrics to the project.
- The OpenChoreo controllers stamping the `openchoreo.dev/*` labels onto
  workload pods so the adapter can filter by component/project/environment UID.
- **Workload Identity** enabled on the cluster and node pool, with a Google
  service account bound to the adapter's Kubernetes ServiceAccount.
- For alerting: a **Cloud Monitoring notification channel** of type
  `webhook_basicauth` whose URL points back at the adapter's webhook endpoint.
  See [Metric alerting](#metric-alerting).

#### Enable Workload Identity

```bash
PROJECT_ID="<your-gcp-project>"
CLUSTER="<your-gke-cluster>"
ZONE="<cluster-zone>"

# Cluster workload pool.
gcloud container clusters update "$CLUSTER" --zone "$ZONE" \
  --workload-pool="${PROJECT_ID}.svc.id.goog"

# Node pool metadata server (each pool the adapter may schedule onto).
gcloud container node-pools update <node-pool> \
  --cluster "$CLUSTER" --zone "$ZONE" \
  --workload-metadata=GKE_METADATA
```

#### Create the Google service account and bind it

```bash
GSA="metrics-adapter-gcp"
NS="openchoreo-observability-plane"
KSA="metrics-adapter-gcp-cloudmonitoring"   # the ServiceAccount this chart creates

gcloud iam service-accounts create "$GSA" \
  --project "$PROJECT_ID" \
  --display-name "OpenChoreo GCP Cloud Monitoring adapter"

# Grant the roles from the table below (repeat --role per role):
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member "serviceAccount:${GSA}@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/monitoring.viewer

# Only needed for alerting.
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member "serviceAccount:${GSA}@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/monitoring.editor

# Bind the KSA to the GSA via Workload Identity.
gcloud iam service-accounts add-iam-policy-binding \
  "${GSA}@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/iam.workloadIdentityUser \
  --member "serviceAccount:${PROJECT_ID}.svc.id.goog[${NS}/${KSA}]"
```

Pass the GSA email to the chart via `adapter.serviceAccount.annotations` so the
KSA is annotated for Workload Identity (see [Installation](#installation-on-gke)).

## GCP IAM roles

The Google service account the adapter runs as needs:

| Role | Why |
| --- | --- |
| `roles/monitoring.viewer` | Read time series via the Cloud Monitoring API (metrics query and the boot-time ping). |
| `roles/monitoring.editor` | Create, update, delete, and list Cloud Monitoring alert policies, and read the notification channel at boot. Only required when alerting is enabled. |

A metrics-query-only install needs just `roles/monitoring.viewer`.

On non-GKE clusters that cannot use Workload Identity, mount a static
service-account key and set `GOOGLE_APPLICATION_CREDENTIALS` to its path
instead of annotating the ServiceAccount.

## Installation on GKE

The install command below reads its values from shell variables:

```bash
PROJECT_ID="<your-gcp-project>"

# GSA email bound to the adapter's ServiceAccount via Workload Identity.
GSA_EMAIL="metrics-adapter-gcp@${PROJECT_ID}.iam.gserviceaccount.com"

# Resource name of the pre-existing webhook_basicauth notification channel.
# See "Metric alerting" for how to create it. Leave empty for query-only.
NOTIFICATION_CHANNEL_ID="projects/${PROJECT_ID}/notificationChannels/<id>"

# Shared secret guarding the adapter's webhook endpoint (any strong value,
# >= 16 characters). Must equal the password on the notification channel.
WEBHOOK_TOKEN="<your-webhook-shared-secret>"
```

```bash
helm upgrade --install observability-metrics-gcp-cloudmonitoring \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-gcp-cloudmonitoring \
  --namespace openchoreo-observability-plane --create-namespace \
  --version <chart-version> \
  --set gcp.projectId="$PROJECT_ID" \
  --set notificationChannel.id="$NOTIFICATION_CHANNEL_ID" \
  --set adapter.observerUrl="http://observer-internal.openchoreo-observability-plane.svc.cluster.local:8081" \
  --set adapter.webhookAuth.sharedSecret="$WEBHOOK_TOKEN" \
  --set adapter.serviceAccount.annotations."iam\.gke\.io/gcp-service-account"="$GSA_EMAIL"
```

For a **query-only** install, omit `notificationChannel.id`,
`adapter.webhookAuth.sharedSecret`, and the `monitoring.editor` role; the
alert-rule endpoints then report not-implemented.

The chart's `templates/validate.yaml` fails the install up front with a
readable message when `gcp.projectId` is missing, or when alerting is
configured without a webhook secret. Once the install succeeds, the adapter
boots, pings Cloud Monitoring, and — when alerting is enabled — verifies the
notification channel is reachable.

### Point the Observer at the adapter

The Observer resolves the metrics adapter at the URL configured by
`METRICS_ADAPTER_URL`, which defaults to `http://metrics-adapter:9099`. This
chart names its Service `metrics-adapter-gcp-cloudmonitoring` and serves on
port `9099`, so set the Observer to match:

```bash
--set observer.metricsAdapter.url=http://metrics-adapter-gcp-cloudmonitoring:9099
```

(on the observability-plane install). If you prefer to keep the Observer's
default, override `adapter.service.port` and expose the Service under the name
the Observer expects instead.

### Expose the webhook through a Gateway

A GCP notification channel POSTs firing alerts from outside the cluster, so the
webhook path must be reachable. To carve it out through an existing Gateway API
Gateway:

```bash
--set adapter.webhookRoute.enabled=true \
--set adapter.webhookRoute.parentRef.name=gateway-default \
--set adapter.webhookRoute.hostnames[0]=metrics-adapter.<your-domain>
```

The chart guards against exposing the webhook without auth: enabling
`webhookRoute` while `webhookAuth.enabled=false` is rejected by
`validate.yaml`.

> **TLS note:** GCP silently drops webhook deliveries to endpoints presenting a
> self-signed or otherwise untrusted certificate. Route the channel through a
> plain-HTTP listener, or a listener terminating TLS with a publicly-trusted
> certificate. The adapter enforces the shared secret regardless of transport.

## Metric alerting

The adapter implements metric alerting on top of a Cloud Monitoring **alert
policy**:

- On rule create, the adapter builds a MetricThreshold condition that compares
  the **usage÷limit ratio** to the threshold, using GCP's native denominator
  filter (numerator = the usage metric, denominator = the limit metric). The
  `source.metric` maps as follows, and the `threshold` is a **percentage of the
  pod's limit** (e.g. `80` = "usage > 80% of limit"), converted to a fraction:

  | `source.metric` | Numerator (ALIGN) | Denominator |
  | --- | --- | --- |
  | `cpu_usage` | `cpu/core_usage_time` (RATE) | `cpu/limit_cores` |
  | `memory_usage` | `memory/used_bytes` (MEAN) | `memory/limit_bytes` |

  Operators `gt`/`gte`/`lt`/`lte`/`eq`/`neq` map to Cloud Monitoring's
  `COMPARISON_GT/GE/LT/LE/EQ/NE` (all six supported natively). `condition.interval`
  becomes the condition duration and `condition.window` the alignment period.
- Managed policies carry `user_labels` (`managed_by=openchoreo`,
  `openchoreo_namespace`, `openchoreo_rule_name`, and a stable
  `openchoreo_rule_hash`) so a rule is found, updated in place, and deleted by
  its `(namespace, name)` identity — Cloud Monitoring policy display names are
  not unique.
- On fire, the policy notifies the configured notification channel, which POSTs
  the incident to the adapter's webhook. Metric metadata labels attach to new
  time series with a delay of a few minutes after pod start, so freshly
  scheduled pods may briefly be invisible to scoped queries and alerts.

### Create the notification channel

The channel passed via `notificationChannel.id` must already exist. Create a
`webhook_basicauth` channel whose URL points at the adapter's webhook endpoint
and whose password equals the shared secret:

```bash
PROJECT_ID="<your-gcp-project>"
WEBHOOK_URL="http://metrics-adapter.<your-domain>/api/v1alpha1/alerts/webhook"
WEBHOOK_TOKEN="<your-webhook-shared-secret>"

cat > channel.json <<EOF
{
  "type": "webhook_basicauth",
  "displayName": "openchoreo-metrics-adapter",
  "labels": { "url": "${WEBHOOK_URL}", "username": "openchoreo" },
  "sensitiveLabels": { "password": "${WEBHOOK_TOKEN}" }
}
EOF

gcloud monitoring channels create --channel-content-from-file=channel.json \
  --project "$PROJECT_ID"
```

The command prints the channel resource name
(`projects/<project>/notificationChannels/<id>`) — pass it as
`notificationChannel.id`.

The notification channel is only transport back into the cluster. The
user-facing delivery (email, Slack, webhook, with templated content) is defined
by an `ObservabilityAlertsNotificationChannel` resource referenced from the
`ObservabilityAlertRule`; see the OpenChoreo `samples/component-alerts`.

## Shared webhook secret

When `adapter.webhookAuth.enabled` is `true` (the default), the adapter rejects
webhook requests that do not carry the configured token. The adapter looks for
the token in this order:

1. The **HTTP Basic-auth password** — sent by a GCP `webhook_basicauth`
   notification channel as `Authorization: Basic base64(user:pass)`. This is
   the preferred, header-based path.
2. The `X-OpenChoreo-Webhook-Token` HTTP header — for a forwarder that injects
   custom headers.
3. The `token` URL query parameter — fallback for receivers that cannot set
   headers.

The comparison runs in constant time. The token must be at least 16
characters; shorter values are rejected at install time by `validate.yaml`.

Two ways to provide the secret:

- Inline via `adapter.webhookAuth.sharedSecret`. The chart creates a Secret
  named `metrics-adapter-gcp-cloudmonitoring-webhook-token` and the Deployment
  mounts it via `secretKeyRef`. The Secret carries `helm.sh/resource-policy:
  keep` so it survives a `helm uninstall`.
- External reference via `adapter.webhookAuth.sharedSecretRef.name`. The chart
  does not create the Secret; the named one must exist in the release
  namespace.

Whichever value you use, it must equal the `password` on the notification
channel.

## Troubleshooting

### `Cloud Monitoring ping failed at boot`

The adapter's startup health check failed against the Cloud Monitoring API.
Check the boot logs:

```bash
kubectl -n openchoreo-observability-plane logs \
  deploy/metrics-adapter-gcp-cloudmonitoring --tail=100
```

Common causes:

- The GSA lacks `roles/monitoring.viewer` on the project.
- Workload Identity is not wired end-to-end: the cluster workload pool, the
  node pool `GKE_METADATA` mode, the GSA↔KSA binding, and the KSA annotation
  must all be in place. Verify the KSA annotation resolves to the right GSA:
  ```bash
  kubectl -n openchoreo-observability-plane get sa \
    metrics-adapter-gcp-cloudmonitoring -o jsonpath='{.metadata.annotations}'
  ```
- `gcp.projectId` points at the wrong project.

An empty ping result only logs a warning — the project may simply have no GKE
workloads yet.

### `notification channel verification failed at boot`

The adapter could not read the notification channel. Most often the GSA lacks
`roles/monitoring.editor`, or `notificationChannel.id` names a channel that
does not exist. The error message includes the resource name it tried. Leave
`notificationChannel.id` empty to run metrics-query only without alerting.

### Alert fires in GCP but no webhook arrives

- If the channel URL is `https://...` and the gateway uses a self-signed
  certificate, GCP drops the delivery silently. Switch to a plain-HTTP
  listener or a publicly-trusted certificate.
- GCP fires the webhook on incident **state transitions** (open/close), not
  continuously while firing. A still-open incident will not re-send.
- Confirm the HTTPRoute is exposing the webhook path and the hostname matches
  the channel URL:
  ```bash
  kubectl -n openchoreo-observability-plane get httproute \
    metrics-adapter-gcp-cloudmonitoring-webhook -o yaml
  ```

### Webhook returns 401 `unauthorized`

The Basic-auth password (or header/query token) did not match
`WEBHOOK_SHARED_SECRET`. Compare the two:

```bash
kubectl -n openchoreo-observability-plane get secret \
  metrics-adapter-gcp-cloudmonitoring-webhook-token \
  -o jsonpath='{.data.token}' | base64 -d

gcloud monitoring channels describe <channel-name> \
  --format='value(labels.url)'
```

The channel's `password` sensitive label must match the Secret value
character-for-character.

### Webhook arrives but the Observer returns an error

The adapter forwards to `${adapter.observerUrl}/api/v1alpha1/alerts/webhook`.
That endpoint is registered on the Observer's **internal** server (port
`8081`), not the public `8080` one — so `adapter.observerUrl` must point at
`observer-internal:8081`. A "rule not found" from the Observer means the
incident's identity did not match an `ObservabilityAlertRule` (for example a
rule created out-of-band rather than through the CR).

### Alert rule matches nothing / never fires

- Scoping is by the UID labels only. Confirm the workload pods carry the
  `openchoreo.dev/component-uid` / `-project-uid` / `-environment-uid` metadata
  labels the policy filters on:
  ```bash
  gcloud monitoring time-series list \
    --filter='metric.type="kubernetes.io/container/cpu/core_usage_time"' \
    --format='value(metadata.userLabels)' 2>/dev/null | head
  ```
- The threshold is a **percentage of the pod's limit** (usage÷limit). A rule
  never fires if the workload has no CPU/memory *limit* set, because the ratio
  denominator is absent. Confirm the pods declare resource limits.
- Metric metadata labels attach a few minutes after pod start; freshly
  scheduled pods are briefly invisible.

## Configuration reference

| Value | Default | Description |
| --- | --- | --- |
| `gcp.projectId` | Required | Project whose time series are queried and where alert policies are created. |
| `notificationChannel.id` | `""` | Resource name of a pre-existing `webhook_basicauth` notification channel pointed at the adapter. Empty disables alerting. |
| `adapter.enabled` | `true` | Toggle the adapter Deployment. |
| `adapter.replicas` | `1` | Adapter replica count. |
| `adapter.image.repository` | `ghcr.io/openchoreo/observability-metrics-gcp-cloudmonitoring-adapter` | Adapter container image. |
| `adapter.image.tag` | Chart `appVersion` | Image tag. |
| `adapter.image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `adapter.service.port` | `9099` | HTTP listener + Service port. Must match the Observer's `METRICS_ADAPTER_URL`. |
| `adapter.queryTimeout` | `30s` | Upper bound for a single metrics query; the six per-metric sub-queries share it (Go duration). |
| `adapter.logLevel` | `INFO` | `DEBUG` \| `INFO` \| `WARN` \| `ERROR`. |
| `adapter.alertEvaluationInterval` | `60s` | Default alert condition duration when a rule omits `condition.interval`. |
| `adapter.alertWindow` | `300s` | Default alignment period when a rule omits `condition.window`. |
| `adapter.observerUrl` | `http://observer-internal.openchoreo-observability-plane.svc.cluster.local:8081` | Observer base URL. Fired alerts are forwarded to `${observerUrl}/api/v1alpha1/alerts/webhook` — must target the Observer's internal server (`:8081`). Alerting is enabled only when this and `notificationChannel.id` are set. |
| `adapter.serviceAccount.annotations` | `{}` | Annotations applied to the adapter ServiceAccount. Use `iam.gke.io/gcp-service-account: <gsa-email>` to bind a Google service account via Workload Identity. |
| `adapter.webhookAuth.enabled` | `true` | Reject webhook calls without the shared secret. |
| `adapter.webhookAuth.sharedSecret` | `""` | Inline secret value. Chart creates a Secret; min 16 characters. |
| `adapter.webhookAuth.sharedSecretRef.name` | `""` | Reference an existing Secret instead of supplying the value inline. |
| `adapter.webhookAuth.sharedSecretRef.key` | `token` | Key inside the referenced Secret. |
| `adapter.webhookRoute.enabled` | `false` | Render a Gateway API HTTPRoute exposing only `/api/v1alpha1/alerts/webhook`. |
| `adapter.webhookRoute.parentRef.name` | `gateway-default` | Gateway to attach to. |
| `adapter.webhookRoute.parentRef.namespace` | `""` | Gateway namespace; defaults to the release namespace. |
| `adapter.webhookRoute.parentRef.sectionName` | `""` | Optional Gateway listener name. |
| `adapter.webhookRoute.hostnames` | `[]` | Optional hostnames matched at the route level. |
| `adapter.networkPolicy.enabled` | `false` | Render a NetworkPolicy restricting ingress to the adapter Pod. |
| `adapter.networkPolicy.observerNamespaceLabels` | `{kubernetes.io/metadata.name: openchoreo-observability-plane}` | Namespace labels selecting the Observer's namespace. |
| `adapter.networkPolicy.observerPodLabels` | `{}` | Pod labels selecting the Observer Pod. Required when the policy is enabled. |
| `adapter.networkPolicy.gatewayNamespaceLabels` | `{}` | Namespace labels selecting the Gateway data-plane that proxies the webhook. |
| `adapter.networkPolicy.allowProbeIPBlock` | `""` | Optional CIDR allowed through ingress for liveness/readiness probes. |
| `adapter.resources` | `200m/256Mi limits, 50m/128Mi requests` | Standard resource requests/limits. |

## Building and testing

```bash
# Regenerate the API stubs from the shared OpenChoreo spec.
make openapi-codegen

# Run unit tests with coverage.
make unit-test

# Build the adapter binary.
make build
```

For a quick test deploy without Helm, `deploy/gke-test.yaml` contains minimal
manifests (ServiceAccount + Deployment + Service, plus optional webhook Secret
and HTTPRoute) for a Workload Identity-enabled GKE cluster:

```bash
kubectl apply -f deploy/gke-test.yaml
```

## Design decisions

- **GKE system metrics over Managed Service for Prometheus**: the system
  metrics are free, enabled by default, and require no collector, whereas a
  GMP-based approach would need managed collection plus kube-state-metrics with
  pod-label allowlists. The trade-off is GKE-only support.
- **No collector in the data plane** (unlike the AWS CloudWatch sibling, which
  ships an OTel DaemonSet): Google's agent already exports everything needed.
- **`REDUCE_SUM` after per-series alignment** mirrors the "avg per instance per
  bin, then sum across instances" aggregation used by the Azure and AWS
  adapters.

## Compatibility

> **Note:** The Helm chart version specified in the installation command above
> is for the latest module version compatible with the development version of
> OpenChoreo. Refer to the compatibility table below to determine the
> appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
|----------------|--------------------|
| v0.1.x         | v1.1.x             |
