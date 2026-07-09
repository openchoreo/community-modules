# OpenChoreo Metrics Adapter for GCP Cloud Monitoring

An OpenChoreo observability metrics adapter backed by **GCP Cloud Monitoring**.
It serves per-component CPU/memory time series by querying the GKE system
metrics that Google's built-in agent publishes for every GKE cluster — nothing
extra has to be deployed in the data plane.

## Status / milestones

| Milestone | Scope | Status |
| --- | --- | --- |
| 1 | Resource metrics query (`POST /api/v1/metrics/query`) | ✅ implemented |
| 2 | Alert rule management + webhook forwarding (Cloud Monitoring alert policies) | ✅ implemented |
| 3 | Helm chart | ⏳ planned |

HTTP RED metrics return an empty series with the
`X-OpenChoreo-Adapter-Notice: http-metrics-not-implemented` response header
(same behavior as the Azure Monitor and AWS CloudWatch siblings). Runtime
topology returns `501` — GKE system metrics carry no pod-to-pod traffic data.

Alert rule management is enabled only when both `OBSERVER_URL` and
`NOTIFICATION_CHANNEL_ID` are configured (see below); otherwise the alert-rule
endpoints answer `500` with error code `OBS-V1-M-GCP-501`.

## Alert rules (milestone 2)

The adapter maps each OpenChoreo alert rule to a Cloud Monitoring
**AlertPolicy** with a metric-threshold condition over the same GKE system
metric the resource query uses, scoped by the same
`metadata.user_labels."openchoreo.dev/*"` identity labels:

| API field | Maps to |
| --- | --- |
| `source.metric` (`cpu_usage`\|`memory_usage`) | usage÷limit **ratio** MetricThreshold: numerator `cpu/core_usage_time` (ALIGN_RATE) or `memory/used_bytes` (ALIGN_MEAN), denominator `cpu/limit_cores` or `memory/limit_bytes` |
| `condition.operator` (`gt`\|`gte`\|`lt`\|`lte`\|`eq`\|`neq`) | `COMPARISON_GT/GE/LT/LE/EQ/NE` (all six supported natively) |
| `condition.threshold` | **percent of the pod's limit** (e.g. `80` = "usage > 80% of limit"), converted to a fraction (0.80) and compared against the usage÷limit ratio — matching the OpenChoreo alert semantics used by the Azure/AWS siblings |
| `condition.interval` | condition `duration` (default 60s) |
| `condition.window` | alignment period (default 5m) |
| `condition.enabled` | policy `enabled` |

Managed policies carry `user_labels` (`managed_by=openchoreo`,
`openchoreo_namespace`, `openchoreo_rule_name`, and a stable
`openchoreo_rule_hash`) so a rule can be found, updated in place, and deleted
by its `(namespace, name)` identity — Cloud Monitoring policy display names are
not unique.

Endpoint semantics: create `201` (or `409` on duplicate), get/update/delete
`200` (or `404` when the rule is unknown), validation failures `400`.

### Webhook forwarding

The pre-configured notification channel POSTs fired incidents to the adapter's
`POST /api/v1alpha1/alerts/webhook`. The adapter parses the Cloud Monitoring
incident payload, recovers the OpenChoreo identity from
`incident.policy_user_labels`, and forwards firing alerts to the Observer's
webhook. It always answers `200` (never fails an alert back to the notification
provider, which would trigger retries) and forwards asynchronously; resolved
(`closed`) incidents are acknowledged but not forwarded.

## How it works

The adapter implements the OpenChoreo metrics adapter API
(`observability-metrics-adapter.yaml`) and translates each resource-metrics
query into six parallel Cloud Monitoring `ListTimeSeries` calls against
`resource.type = "k8s_container"`:

| API field | GCP metric type | Aligner |
| --- | --- | --- |
| `cpuUsage` | `kubernetes.io/container/cpu/core_usage_time` | `ALIGN_RATE` (cumulative seconds → cores) |
| `cpuRequests` | `kubernetes.io/container/cpu/request_cores` | `ALIGN_MEAN` |
| `cpuLimits` | `kubernetes.io/container/cpu/limit_cores` | `ALIGN_MEAN` |
| `memoryUsage` | `kubernetes.io/container/memory/used_bytes` (`memory_type="non-evictable"` ≈ working set) | `ALIGN_MEAN` |
| `memoryRequests` | `kubernetes.io/container/memory/request_bytes` | `ALIGN_MEAN` |
| `memoryLimits` | `kubernetes.io/container/memory/limit_bytes` | `ALIGN_MEAN` |

Every query aligns to the request `step` (default 5m, clamped to GCP's 1m
minimum) and applies `REDUCE_SUM` to collapse the per-container series into a
single series per metric. CPU values are cores, memory values are bytes,
matching the other OpenChoreo metrics adapters.

### Scoping by OpenChoreo identity

`k8s_container` metric labels do not carry pod labels, but Cloud Monitoring
attaches pod labels as **system metadata**, filterable as
`metadata.user_labels."openchoreo.dev/component-uid" = "..."` (plus the
`project-uid` / `environment-uid` clauses). Unlike Cloud Logging — where the
GKE agent surfaces pod labels under a `k8s-pod/` prefix with dots replaced by
underscores — Monitoring metadata keeps the raw label keys verbatim.

Scoping is by **UID only** (component / project / environment), matching the
Prometheus and Azure Monitor siblings. The rule's `namespace` is deliberately
**not** a metric filter: the control plane sends the data-plane runtime
namespace (`dp-<project>-<env>-…`) as the rule namespace, whereas the pod's
`openchoreo.dev/namespace` metadata label carries the *control-plane*
namespace, so filtering on it would match zero series. Namespace is retained
only as alert-policy identity/dedup metadata.

Note: metadata labels attach to new time series with a delay of a few minutes
after pod start; freshly scheduled pods may briefly be invisible to scoped
queries.

## Configuration

| Env var | Required | Default | Description |
| --- | --- | --- | --- |
| `GCP_PROJECT_ID` | yes | — | GCP project whose Cloud Monitoring time series are queried |
| `SERVER_PORT` | no | `9099` | HTTP listener port |
| `LOG_LEVEL` | no | `info` | `debug` \| `info` \| `warn` \| `error` |
| `QUERY_TIMEOUT` | no | `30s` | Go duration capping one metrics query (all six sub-queries share it) |
| `OBSERVER_URL` | no¹ | — | Base URL of the OpenChoreo Observer that fired alerts are forwarded to |
| `NOTIFICATION_CHANNEL_ID` | no¹ | — | Pre-configured Cloud Monitoring notification channel (`projects/<id>/notificationChannels/<n>`) attached to every managed alert policy; verified at boot |
| `ALERT_EVALUATION_INTERVAL` | no | `60s` | Default condition duration when a rule omits `condition.interval` |
| `ALERT_WINDOW` | no | `5m` | Default alignment period when a rule omits `condition.window` |

¹ Alert rule management is enabled only when **both** `OBSERVER_URL` and
`NOTIFICATION_CHANNEL_ID` are set. When either is unset the alert-rule
endpoints report not-implemented.

## Authentication / IAM

The adapter uses **Application Default Credentials**. On GKE, bind the
Kubernetes ServiceAccount to a GCP service account via Workload Identity:

```bash
PROJECT_ID=<your-project>
GSA=metrics-adapter-gcp

gcloud iam service-accounts create "$GSA" --project "$PROJECT_ID"

# Metrics query only needs the viewer role (milestone 1).
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member "serviceAccount:${GSA}@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/monitoring.viewer

# Alert rule management (milestone 2) additionally needs the editor role to
# create/update/delete alert policies and read the notification channel.
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member "serviceAccount:${GSA}@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/monitoring.editor

# Allow the KSA to impersonate the GSA.
gcloud iam service-accounts add-iam-policy-binding \
  "${GSA}@${PROJECT_ID}.iam.gserviceaccount.com" \
  --project "$PROJECT_ID" \
  --role roles/iam.workloadIdentityUser \
  --member "serviceAccount:${PROJECT_ID}.svc.id.goog[openchoreo-observability-plane/metrics-adapter-gcp-cloudmonitoring]"
```

Off-GKE, mount a service-account key and set
`GOOGLE_APPLICATION_CREDENTIALS`; locally, `gcloud auth application-default
login` suffices.

At boot the adapter fail-fast pings Cloud Monitoring (one header-only page of
container CPU series over the last hour) and exits non-zero if credentials or
API access are broken. An empty result only logs a warning — the project may
simply have no GKE workloads yet.

## Deploying to a test cluster

`deploy/gke-test.yaml` contains minimal manifests (ServiceAccount +
Deployment + Service) for a Workload Identity-enabled GKE cluster. The
Service is named `metrics-adapter` on port 9099 to satisfy the Observer's
`METRICS_ADAPTER_URL` convention.

```bash
kubectl apply -f deploy/gke-test.yaml
```

## Development

```bash
make openapi-codegen   # regenerate internal/api/gen from the upstream spec
make build             # static binary at bin/adapter
make unit-test         # go test with coverage
```

Example query:

```bash
curl -s localhost:9099/api/v1/metrics/query \
  -H 'Content-Type: application/json' \
  -d '{
    "metric": "resource",
    "searchScope": {"namespace": "default"},
    "startTime": "2026-07-07T05:00:00Z",
    "endTime": "2026-07-07T06:00:00Z",
    "step": "5m"
  }'
```

## Design decisions

- **GKE system metrics over Managed Service for Prometheus**: the system
  metrics are free, enabled by default, and require no collector, whereas a
  GMP-based approach would need managed collection plus kube-state-metrics
  with pod-label allowlists. The trade-off is GKE-only support.
- **No collector in the data plane** (unlike the AWS CloudWatch sibling,
  which ships an OTel DaemonSet): Google's agent already exports everything
  needed.
- **`REDUCE_SUM` after per-series alignment** mirrors the "avg per instance
  per bin, then sum across instances" aggregation used by the Azure and AWS
  adapters.
