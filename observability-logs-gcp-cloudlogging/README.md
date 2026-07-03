# Observability Logs Module for GCP Cloud Logging

This module exposes Google Cloud Logging as an OpenChoreo logs backend. It
queries GKE container logs (`resource.type="k8s_container"`, populated
automatically by GKE's logging agent) and manages alert rules as Cloud Logging
log-based metrics plus Cloud Monitoring alert policies, with delivery back into
OpenChoreo through a pre-existing notification channel.

It targets GKE clusters with Workload Identity. Authentication uses Google
Application Default Credentials (ADC) against a Google service account bound to
the adapter's Kubernetes ServiceAccount.

## Table of contents

1. [Architecture](#architecture)
2. [Choose a deployment topology](#choose-a-deployment-topology)
3. [Prerequisites](#prerequisites)
4. [GCP IAM roles](#gcp-iam-roles)
5. [Installation on GKE](#installation-on-gke)
6. [Log alerting](#log-alerting)
7. [Shared webhook secret](#shared-webhook-secret)
8. [Troubleshooting](#troubleshooting)
9. [Configuration reference](#configuration-reference)
10. [Building and testing](#building-and-testing)
11. [Compatibility](#compatibility)

## Architecture

This module has two main responsibilities:

1. **Log query** against Cloud Logging.
2. **Alerting** through Cloud Logging log-based metrics and Cloud Monitoring
   alert policies.

Log shipping is **not** in scope for this chart — GKE's managed logging agent
writes container logs to Cloud Logging automatically. This module reads from
that log store.

The chart deploys:

1. A Go **Cloud Logging Adapter** Deployment that implements the OpenChoreo
   Logs Adapter API.
2. A Service, ServiceAccount (with a Workload Identity annotation), ConfigMap,
   and — optionally — a webhook Secret, a Gateway API HTTPRoute, and a
   NetworkPolicy.

Logs are read from `k8s_container` entries. Each log record carries Kubernetes
metadata:

- The monitored-resource labels `namespace_name`, `pod_name`, `container_name`.
- The OpenChoreo pod labels, surfaced by GKE's managed logging agent under the
  `k8s-pod/` prefix. **The agent replaces dots in the label key with
  underscores**, so `openchoreo.dev/component-uid` appears as
  `labels."k8s-pod/openchoreo_dev/component-uid"` (slashes and hyphens are
  preserved). The adapter applies this substitution when building filters; it
  is configurable via `adapter.sanitizePodLabelDots` for clusters running an
  agent that preserves dots.

Component-, project-, and environment-scoped queries filter on these pod
labels. The **logical** OpenChoreo namespace lives in the
`k8s-pod/openchoreo_dev/namespace` label (e.g. `default`) and is what the
adapter scopes on — not the synthesised Kubernetes namespace
(`resource.labels.namespace_name`, e.g. `dp-default-development-4b8b4fdc`).

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/logs/query` | Runs a Cloud Logging filter against `k8s_container` logs, scoped by OpenChoreo namespace label plus optional component/project/environment UIDs. |
| `POST /api/v1/events/query` | Not implemented — this adapter is a logs backend and does not serve Kubernetes events; returns `500` with error code `OBS-V1-L-GCP-501`. |
| `POST /api/v1alpha1/alerts/rules` | Creates a log-based metric and a Cloud Monitoring alert policy wired to the configured notification channel. Returns `409` if a rule with the same identity already exists (use `PUT` to replace). |
| `GET /api/v1alpha1/alerts/rules/{ruleName}` | Looks the policy up by its `openchoreo-rule-name` user label. |
| `PUT /api/v1alpha1/alerts/rules/{ruleName}` | Updates the rule's metric and policy in place. Returns `404` if the rule does not exist. |
| `DELETE /api/v1alpha1/alerts/rules/{ruleName}` | Deletes the alert policy and its log-based metric. |
| `POST /api/v1alpha1/alerts/webhook` | Receives a fired-alert payload from the notification channel and forwards a normalised alert to the Observer. |
| `GET /health` | Readiness/liveness check. |

### How a fired alert flows back into OpenChoreo

Cloud Monitoring alert policies cannot call OpenChoreo controllers directly.
The path is:

```
Cloud Logging (matching entries)
  → log-based metric (a DELTA counter of matches)
  → alert policy: SUM the counter over the window, compare to threshold
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
| Single cluster | The OpenChoreo cluster where the observability plane and workloads run together. | Deploys the adapter that queries Cloud Logging and manages alert rules. | Defaults. |
| Observability plane cluster | The cluster where the OpenChoreo observability plane is installed. | Deploys only the adapter. | Defaults. |
| Data-plane / workflow-plane cluster | Each cluster that runs OpenChoreo workloads. | No install. Workload clusters write to Cloud Logging via the GKE logging agent directly. | N/A |

Cloud Logging is the shared managed backend. Remote workload clusters write to
the same project via the GKE logging agent and do not need network
connectivity back to the observability plane. The adapter only runs where the
Observer needs to query logs and manage rules.

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
- A GKE cluster with **Cloud Logging enabled** (the default), writing container
  logs to the project's `_Default` log bucket.
- The OpenChoreo controllers stamping the `openchoreo.dev/*` labels onto
  workload pods so the adapter can filter by component/project/environment UID.
- **Workload Identity** enabled on the cluster and node pool, with a Google
  service account bound to the adapter's Kubernetes ServiceAccount.
- For alerting: a **Cloud Monitoring notification channel** of type
  `webhook_basicauth` whose URL points back at the adapter's webhook endpoint.
  See [Log alerting](#log-alerting).

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
GSA="logs-adapter-gcp"
NS="openchoreo-observability-plane"
KSA="logs-adapter-gcp-cloudlogging"   # the ServiceAccount this chart creates

gcloud iam service-accounts create "$GSA" \
  --project "$PROJECT_ID" \
  --display-name "OpenChoreo GCP Cloud Logging adapter"

# Grant the roles from the table below (repeat --role per role):
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member "serviceAccount:${GSA}@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/logging.viewer

gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member "serviceAccount:${GSA}@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/logging.configWriter

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
| `roles/logging.viewer` | Read log entries via the Cloud Logging API (log query and the boot-time ping). |
| `roles/logging.configWriter` | Create, update, and delete the log-based metrics that back alert rules. |
| `roles/monitoring.editor` | Create, update, delete, and list Cloud Monitoring alert policies, and read the notification channel at boot. |

On non-GKE clusters that cannot use Workload Identity, mount a static
service-account key and set `GOOGLE_APPLICATION_CREDENTIALS` to its path
instead of annotating the ServiceAccount.

## Installation on GKE

The install command below reads its values from shell variables:

```bash
PROJECT_ID="<your-gcp-project>"

# GSA email bound to the adapter's ServiceAccount via Workload Identity.
GSA_EMAIL="logs-adapter-gcp@${PROJECT_ID}.iam.gserviceaccount.com"

# Resource name of the pre-existing webhook_basicauth notification channel.
# See "Log alerting" for how to create it.
NOTIFICATION_CHANNEL_ID="projects/${PROJECT_ID}/notificationChannels/<id>"

# Shared secret guarding the adapter's webhook endpoint (any strong value,
# >= 16 characters). Must equal the password on the notification channel.
WEBHOOK_TOKEN="<your-webhook-shared-secret>"
```

```bash
helm upgrade --install observability-logs-gcp-cloudlogging \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-gcp-cloudlogging \
  --namespace openchoreo-observability-plane --create-namespace \
  --version <chart-version> \
  --set gcp.projectId="$PROJECT_ID" \
  --set notificationChannel.id="$NOTIFICATION_CHANNEL_ID" \
  --set adapter.observerUrl="http://observer-internal.openchoreo-observability-plane.svc.cluster.local:8081" \
  --set adapter.webhookAuth.sharedSecret="$WEBHOOK_TOKEN" \
  --set adapter.serviceAccount.annotations."iam\.gke\.io/gcp-service-account"="$GSA_EMAIL"
```

The chart's `templates/validate.yaml` fails the install up front with a
readable message when `gcp.projectId`, `adapter.observerUrl`, or a webhook
secret is missing. Once the install succeeds, the adapter boots, pings Cloud
Logging, and verifies the notification channel is reachable.

### Point the Observer at the adapter

The Observer resolves the logs adapter at the URL configured on the
`openchoreo-observability-plane` chart (`observer.logsAdapter.url`), which
defaults to `http://logs-adapter:9098`. This chart names its Service
`logs-adapter-gcp-cloudlogging` and serves on port `9098`, so set the Observer
to match:

```bash
--set observer.logsAdapter.url=http://logs-adapter-gcp-cloudlogging:9098
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
--set adapter.webhookRoute.hostnames[0]=logs-adapter.<your-domain>
```

The chart guards against exposing the webhook without auth: enabling
`webhookRoute` while `webhookAuth.enabled=false` is rejected by
`validate.yaml`.

> **TLS note:** GCP silently drops webhook deliveries to endpoints presenting a
> self-signed or otherwise untrusted certificate. Route the channel through a
> plain-HTTP listener, or a listener terminating TLS with a publicly-trusted
> certificate. The adapter enforces the shared secret regardless of transport.

## Log alerting

The adapter implements log alerting on top of a Cloud Logging **log-based
metric** and a Cloud Monitoring **alert policy**:

- On rule create, the adapter defines a counter log-based metric whose filter
  matches the rule's query (scoped to the component/project/environment), then
  creates an alert policy that **sums** that counter over the rolling window
  and compares the total to the threshold. The metric can take a minute or two
  to become queryable after creation; the adapter retries the policy create
  until the metric is ready.
- On fire, the policy notifies the configured notification channel, which POSTs
  the incident to the adapter's webhook.

### Create the notification channel

The channel passed via `notificationChannel.id` must already exist. Create a
`webhook_basicauth` channel whose URL points at the adapter's webhook endpoint
and whose password equals the shared secret:

```bash
PROJECT_ID="<your-gcp-project>"
WEBHOOK_URL="http://logs-adapter.<your-domain>/api/v1alpha1/alerts/webhook"
WEBHOOK_TOKEN="<your-webhook-shared-secret>"

cat > channel.json <<EOF
{
  "type": "webhook_basicauth",
  "displayName": "openchoreo-logs-adapter",
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
  named `logs-adapter-gcp-cloudlogging-webhook-token` and the Deployment mounts
  it via `secretKeyRef`. The Secret carries `helm.sh/resource-policy: keep` so
  it survives a `helm uninstall`.
- External reference via `adapter.webhookAuth.sharedSecretRef.name`. The chart
  does not create the Secret; the named one must exist in the release
  namespace.

Whichever value you use, it must equal the `password` on the notification
channel.

## Troubleshooting

### `Cloud Logging ping failed at boot`

The adapter's startup health check failed against the Cloud Logging API. Check
the boot logs:

```bash
kubectl -n openchoreo-observability-plane logs \
  deploy/logs-adapter-gcp-cloudlogging --tail=100
```

Common causes:

- The GSA lacks `roles/logging.viewer` on the project.
- Workload Identity is not wired end-to-end: the cluster workload pool, the
  node pool `GKE_METADATA` mode, the GSA↔KSA binding, and the KSA annotation
  must all be in place. Verify the KSA annotation resolves to the right GSA:
  ```bash
  kubectl -n openchoreo-observability-plane get sa \
    logs-adapter-gcp-cloudlogging -o jsonpath='{.metadata.annotations}'
  ```
- `gcp.projectId` points at the wrong project.

### `notification channel verification failed at boot`

The adapter could not read the notification channel. Most often the GSA lacks
`roles/monitoring.editor`, or `notificationChannel.id` names a channel that
does not exist. The error message includes the resource name it tried. Leave
`notificationChannel.id` empty to run log-query only without alerting.

### Alert rule creates but the policy briefly 500s / retries

A freshly created log-based metric is not immediately queryable; Cloud
Monitoring returns `NotFound: Cannot find metric ...` for up to a few minutes.
The adapter retries the policy create until the metric is ready, so this
resolves on its own. If it never resolves, confirm the GSA has
`roles/logging.configWriter` (to create the metric).

### Alert fires in GCP but no webhook arrives

- If the channel URL is `https://...` and the gateway uses a self-signed
  certificate, GCP drops the delivery silently. Switch to a plain-HTTP
  listener or a publicly-trusted certificate.
- Confirm the HTTPRoute is exposing the webhook path and the hostname matches
  the channel URL:
  ```bash
  kubectl -n openchoreo-observability-plane get httproute \
    logs-adapter-gcp-cloudlogging-webhook -o yaml
  ```

### Webhook returns 401 `unauthorized`

The Basic-auth password (or header/query token) did not match
`WEBHOOK_SHARED_SECRET`. Compare the two:

```bash
kubectl -n openchoreo-observability-plane get secret \
  logs-adapter-gcp-cloudlogging-webhook-token \
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
incident's identity labels did not match an `ObservabilityAlertRule` (for
example a rule created out-of-band rather than through the CR).

### Alert rule matches nothing / never fires

- The query filter scopes on the OpenChoreo pod labels. If the GKE agent on
  your cluster does not replace dots with underscores in label keys, set
  `adapter.sanitizePodLabelDots=false`. Inspect a raw entry to see the surfaced
  key:
  ```bash
  gcloud logging read 'resource.type="k8s_container"' --limit=1 --format=json \
    | grep -i openchoreo
  ```
- The policy sums the log-based counter over the window (`ALIGN_SUM`) and fires
  as soon as the total breaches the threshold. Confirm the metric actually has
  data points in Metrics Explorer for the metric
  `logging.googleapis.com/user/<metric-id>`.

## Configuration reference

| Value | Default | Description |
| --- | --- | --- |
| `gcp.projectId` | Required | Project that owns the logs, log-based metrics, and alert policies. |
| `notificationChannel.id` | `""` | Resource name of a pre-existing `webhook_basicauth` notification channel pointed at the adapter. Empty disables alert delivery. |
| `adapter.enabled` | `true` | Toggle the adapter Deployment. |
| `adapter.replicas` | `1` | Adapter replica count. |
| `adapter.image.repository` | `ghcr.io/openchoreo/observability-logs-gcp-cloudlogging-adapter` | Adapter container image. |
| `adapter.image.tag` | Chart `appVersion` | Image tag. |
| `adapter.image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `adapter.service.port` | `9098` | HTTP listener + Service port. Must match the Observer's `logs.adapter.url`. |
| `adapter.queryTimeout` | `30s` | Upper bound for a single Cloud Logging query (Go duration). |
| `adapter.logLevel` | `INFO` | `DEBUG` \| `INFO` \| `WARN` \| `ERROR`. |
| `adapter.sanitizePodLabelDots` | `true` | Replace dots with underscores in OpenChoreo pod-label keys when building filters (matches the GKE managed logging agent). |
| `adapter.observerUrl` | `http://observer-internal.openchoreo-observability-plane.svc.cluster.local:8081` | Observer base URL. Fired alerts are forwarded to `${observerUrl}/api/v1alpha1/alerts/webhook` — must target the Observer's internal server (`:8081`). |
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

## Compatibility

> **Note:** The Helm chart version specified in the installation command above
> is for the latest module version compatible with the development version of
> OpenChoreo. Refer to the compatibility table below to determine the
> appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
|----------------|--------------------|
| v0.1.x         | v1.1.x             |
