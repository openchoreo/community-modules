# Observability Tracing Module for Google Cloud Trace

This module integrates [Google Cloud Trace](https://cloud.google.com/trace) with OpenChoreo as the distributed-tracing backend for the observability plane.

It ships two components in one Helm chart:

- **OpenTelemetry Collector** (contrib distribution) — receives OTLP spans from workloads, stamps them with the OpenChoreo pod labels, and exports them to Cloud Trace through the `googlecloud` exporter.
- **Tracing adapter** — a Go service that implements the [OpenChoreo tracing adapter API](https://github.com/openchoreo/openchoreo/blob/main/openapi/observability-tracing-adapter-api.yaml) and answers Observer queries through the Cloud Trace v1 read API (`ListTraces` / `GetTrace`).

## Architecture

```mermaid
flowchart TD
  app["Workload pods (OTel SDK)"] -->|OTLP 4317/4318| col["OTel Collector (contrib)<br/>k8sattributes (pod labels &rarr; resource attrs)<br/>transform (openchoreo.dev/* &rarr; span attrs)<br/>tail_sampling (rate limiting)"]
  col -->|googlecloud exporter<br/>Workload Identity: roles/cloudtrace.agent| ct["Google Cloud Trace"]
  ct -->|ListTraces / GetTrace (v1 read API)<br/>Workload Identity: roles/cloudtrace.user| adapter["tracing-adapter :9100"]
  observer["Observer"] -->|POST /api/v1alpha1/traces/query| adapter
```

The collector promotes the four OpenChoreo pod labels to **span attributes** (`openchoreo.dev/namespace`, `openchoreo.dev/component-uid`, `openchoreo.dev/project-uid`, `openchoreo.dev/environment-uid`). This step is load-bearing: the `googlecloud` exporter does not write resource attributes onto Cloud Trace spans, and the adapter scopes every query with exact-match label filters such as:

```
+openchoreo.dev/namespace:default +openchoreo.dev/component-uid:<uid>
```

## Prerequisites

### GCP prerequisites

- A GCP project with the **Cloud Trace API** enabled:

  ```bash
  gcloud services enable cloudtrace.googleapis.com --project "$PROJECT_ID"
  ```

- A **GKE cluster with Workload Identity** enabled:

  ```bash
  gcloud container clusters update "$CLUSTER" --zone "$ZONE" \
    --workload-pool="${PROJECT_ID}.svc.id.goog"
  gcloud container node-pools update "$POOL" --cluster "$CLUSTER" --zone "$ZONE" \
    --workload-metadata=GKE_METADATA
  ```

### Google Service Accounts and IAM roles

Two identities are needed (one GSA holding both roles also works):

| Component | Role | Purpose |
|---|---|---|
| Adapter | `roles/cloudtrace.user` | `cloudtrace.traces.list` / `cloudtrace.traces.get` through the v1 read API |
| Collector | `roles/cloudtrace.agent` | write spans (`cloudtrace.traces.patch`, `telemetry.traces.write`) |

```bash
NS=openchoreo-observability-plane

# Adapter GSA (read)
gcloud iam service-accounts create tracing-adapter-cloudtrace --project "$PROJECT_ID"
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member "serviceAccount:tracing-adapter-cloudtrace@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/cloudtrace.user
gcloud iam service-accounts add-iam-policy-binding \
  "tracing-adapter-cloudtrace@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/iam.workloadIdentityUser \
  --member "serviceAccount:${PROJECT_ID}.svc.id.goog[${NS}/tracing-adapter-gcp-cloudtrace]"

# Collector GSA (write)
gcloud iam service-accounts create otel-collector-cloudtrace --project "$PROJECT_ID"
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member "serviceAccount:otel-collector-cloudtrace@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/cloudtrace.agent
gcloud iam service-accounts add-iam-policy-binding \
  "otel-collector-cloudtrace@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role roles/iam.workloadIdentityUser \
  --member "serviceAccount:${PROJECT_ID}.svc.id.goog[${NS}/otel-collector-gcp]"
```

Both components authenticate with Application Default Credentials — no key files. On non-GKE clusters that cannot use Workload Identity, mount a static service-account key and set `GOOGLE_APPLICATION_CREDENTIALS`.

## Installation

```bash
helm upgrade --install observability-tracing-gcp-cloudtrace \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-gcp-cloudtrace \
  --namespace openchoreo-observability-plane --create-namespace \
  --set gcp.projectId="$PROJECT_ID" \
  --set adapter.serviceAccount.annotations."iam\.gke\.io/gcp-service-account"="tracing-adapter-cloudtrace@${PROJECT_ID}.iam.gserviceaccount.com" \
  --set opentelemetry-collector.serviceAccount.annotations."iam\.gke\.io/gcp-service-account"="otel-collector-cloudtrace@${PROJECT_ID}.iam.gserviceaccount.com"
```

Point the Observer at the adapter service:

```bash
--set observer.tracingAdapter.url=http://tracing-adapter:9100
```

Workloads send OTLP spans to the collector service (`otel-collector-gcp:4317` gRPC / `:4318` HTTP).

### Deployment topologies

- **Single cluster** — install the chart as-is; collector and adapter run side by side in the observability-plane namespace.
- **Multi-cluster** — install the adapter only (`--set opentelemetry-collector.enabled=false`) next to the Observer, and the collector only (`--set adapter.enabled=false`) on each data-plane cluster. All collectors export to the same GCP project; the adapter reads it back.

## Adapter configuration

| Env var | Helm value | Default | Description |
|---|---|---|---|
| `GCP_PROJECT_ID` | `gcp.projectId` | — (required) | Project traces are read from |
| `SERVER_PORT` | `adapter.service.port` | `9100` | Adapter HTTP port |
| `QUERY_TIMEOUT_SECONDS` | `adapter.queryTimeoutSeconds` | `30` | Upper bound per Cloud Trace query |
| `LOG_LEVEL` | `adapter.logLevel` | `INFO` | DEBUG, INFO, WARN, ERROR |

## Behavior notes

- **Read API**: only Cloud Trace API v1 supports reads; v2 is write-only. Trace lists use `ListTraces` with the `COMPLETE` view so span count, root span, and error status can be computed without a per-trace follow-up call.
- **Span IDs** are converted between the v1 `fixed64` form and the 16-char hex convention used by OTLP and the sibling adapters. Trace IDs pass through unchanged (32-char hex on both sides).
- **Span status** is derived from Cloud Trace labels in order: `g.co/status/code` (0 = ok), `otel.status_code`, the `error` flag, then `/http/status_code` (≥ 500 = error); otherwise `unset`.
- **Tenancy**: `GetTrace` has no filter parameter, so the spans endpoint re-checks the search scope against span labels and reports out-of-scope traces as empty — the same outcome `ListTraces` filtering produces.
- **Retention and limits**: Cloud Trace retains spans for **30 days**; queries beyond that return nothing. Spans are capped at **32 labels** — the four promoted OpenChoreo labels count toward this budget.

## Building

```bash
make openapi-codegen   # regenerate API models/server from the shared spec
go build ./...
make unit-test
docker build -t observability-tracing-gcp-cloudtrace-adapter .
```

## Compatibility

| Module version | OpenChoreo version |
|---|---|
| 0.1.x | v1.1.x |
