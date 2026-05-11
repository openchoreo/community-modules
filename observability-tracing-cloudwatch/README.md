# Observability Tracing Module for AWS X-Ray (CloudWatch)

The **Observability Tracing Module for AWS X-Ray** collects application
traces via an **OpenTelemetry Collector** and stores them in **AWS X-Ray**.
A Go adapter service implements the **OpenChoreo Tracing Adapter API** to
query traces back from X-Ray for the OpenChoreo Observer.

This module supports both:

- **EKS clusters** using **EKS Pod Identity** or IRSA, recommended for production.
- **Non-EKS Kubernetes clusters** such as **k3d**, **kind**, or Kubernetes
  running outside AWS, using static AWS credentials.

## Table of contents

1. [Architecture](#architecture)
2. [Prerequisites](#prerequisites)
3. [IAM permissions](#iam-permissions)
4. [Installation on EKS with Pod Identity](#installation-on-eks-with-pod-identity)
5. [Installation on non-EKS clusters with static credentials](#installation-on-non-eks-clusters-with-static-credentials)
6. [Wire the Observer to the adapter](#wire-the-observer-to-the-adapter)
7. [Verify trace ingestion and querying](#verify-trace-ingestion-and-querying)
8. [Configuration reference](#configuration-reference)
9. [k3d and kind compatibility](#k3d-and-kind-compatibility)
10. [Limitations](#limitations)
11. [Troubleshooting](#troubleshooting)

## Architecture

The chart deploys two workloads in the OpenChoreo observability plane:

1. An **OpenTelemetry Collector** Deployment that receives traces via
   OTLP (gRPC on port 4317 and HTTP on port 4318), enriches them with
   Kubernetes pod labels via the `k8sattributes` processor, applies
   tail sampling for rate limiting, and exports them to AWS X-Ray via
   the `awsxray` exporter.
2. A Go **X-Ray Tracing Adapter** Deployment that implements the
   OpenChoreo Tracing Adapter API and queries traces from X-Ray.

The collector runs in **Deployment mode** (not DaemonSet) because traces
are push-based — instrumented applications send OTLP traces to the
collector's Service endpoint.

The `awsxray` exporter is configured with `indexed_attributes` so that
OpenChoreo labels are stored as filterable X-Ray annotations:

| OpenChoreo label | X-Ray annotation key |
| --- | --- |
| `openchoreo.dev/namespace` | `openchoreo_dev_namespace` |
| `openchoreo.dev/component-uid` | `openchoreo_dev_component_uid` |
| `openchoreo.dev/project-uid` | `openchoreo_dev_project_uid` |
| `openchoreo.dev/environment-uid` | `openchoreo_dev_environment_uid` |

The adapter queries X-Ray using these annotations as filter expressions,
enabling scope-based trace retrieval.

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1alpha1/traces/query` | Queries X-Ray `GetTraceSummaries` for traces matching the search scope. |
| `POST /api/v1alpha1/traces/{traceId}/spans/query` | Fetches all spans (segments + subsegments) for a trace via `BatchGetTraces` and flattens the segment tree. |
| `GET /api/v1alpha1/traces/{traceId}/spans/{spanId}` | Returns full detail for a specific span within a trace, including attributes and resource attributes. |
| `GET /healthz` | Readiness and liveness check. Returns `200` once the adapter is ready. |

## Prerequisites

Before installing this module, make sure the following are available.

### OpenChoreo prerequisites

- OpenChoreo is installed.
- The `openchoreo-observability-plane` Helm chart is installed.
- Workload pods include OpenChoreo labels such as:
  - `openchoreo.dev/namespace`
  - `openchoreo.dev/component-uid`
  - `openchoreo.dev/environment-uid`
  - `openchoreo.dev/project-uid`
- Applications are instrumented with OpenTelemetry and configured to send
  OTLP traces to the collector endpoint.

See the [OpenChoreo documentation](https://openchoreo.dev/docs) for the base
installation steps.

### Local tooling

Install the following tools on your machine:

- `helm`
- `kubectl`
- `jq`
- `aws` CLI v2

### Cluster prerequisites

The OpenTelemetry collector expects:

- Kubernetes API access to pods, replicasets, namespaces, and nodes (for
  the `k8sattributes` processor in `singleCluster` mode).

### AWS prerequisites

You need:

- An AWS account.
- An AWS region, for example `eu-north-1`.
- An OpenChoreo instance name, for example `openchoreo-dev`.
  This is the identifier for this OpenChoreo installation and is exported to
  X-Ray as the `openchoreo-instance-name` annotation.
- An IAM principal with the permissions described in [IAM permissions](#iam-permissions).

For EKS, use IAM roles with **EKS Pod Identity** or IRSA. For non-EKS clusters
such as k3d or kind, use an IAM user with access keys.

## IAM permissions

The X-Ray tracing adapter needs permissions for two paths:

1. Startup identity check.
2. X-Ray trace queries.

The OpenTelemetry collector needs permission to write trace segments to X-Ray.

Use these policies based on the credential model:

- **EKS Pod Identity or IRSA:** keep the adapter and OpenTelemetry collector policies
  separate and attach them to separate roles. This keeps each ServiceAccount
  least-privileged.
- **Static credentials:** use one IAM user and attach the
  [combined static-credentials IAM policy](#combined-static-credentials-iam-policy),
  because the same Kubernetes Secret is shared by the adapter and OpenTelemetry
  collector.

### Adapter IAM policy

Create the following custom IAM policy and attach it to the adapter IAM
principal when using separate EKS identities.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Startup",
      "Effect": "Allow",
      "Action": "sts:GetCallerIdentity",
      "Resource": "*"
    },
    {
      "Sid": "XRayRead",
      "Effect": "Allow",
      "Action": [
        "xray:GetTraceSummaries",
        "xray:BatchGetTraces",
        "xray:GetTraceGraph"
      ],
      "Resource": "*"
    }
  ]
}
```

### OpenTelemetry collector IAM policy

Create the following custom IAM policy and attach it to the OpenTelemetry
collector IAM principal when using separate EKS identities.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "XRayWrite",
      "Effect": "Allow",
      "Action": [
        "xray:PutTraceSegments",
        "xray:PutTelemetryRecords",
        "xray:GetSamplingRules",
        "xray:GetSamplingTargets",
        "xray:GetSamplingStatisticSummaries"
      ],
      "Resource": "*"
    }
  ]
}
```

### Combined static-credentials IAM policy

Use this policy for non-EKS clusters where one IAM user backs the shared static
AWS credentials Secret.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Startup",
      "Effect": "Allow",
      "Action": "sts:GetCallerIdentity",
      "Resource": "*"
    },
    {
      "Sid": "XRayReadAndWrite",
      "Effect": "Allow",
      "Action": [
        "xray:GetTraceSummaries",
        "xray:BatchGetTraces",
        "xray:GetTraceGraph",
        "xray:PutTraceSegments",
        "xray:PutTelemetryRecords",
        "xray:GetSamplingRules",
        "xray:GetSamplingTargets",
        "xray:GetSamplingStatisticSummaries"
      ],
      "Resource": "*"
    }
  ]
}
```

## Installation on EKS with Pod Identity

This is the recommended installation path for EKS clusters.

### Step 1 - Export shared values

```bash
export AWS_REGION=eu-north-1
export INSTANCE_NAME=openchoreo-dev
export NS=openchoreo-observability-plane
```

Make sure your `kubectl` context points to the target EKS cluster:

```bash
kubectl config current-context
```

Also verify that the EKS Pod Identity Agent add-on is installed:

```bash
kubectl -n kube-system get ds eks-pod-identity-agent
```

Pod Identity credentials are injected only when the Pod Identity Agent is
running.

### Step 2 - Create IAM roles

Create an IAM role for the adapter, for example:

```text
OpenChoreoXRayTracingRoleForAdapter
```

Attach the custom [Adapter IAM policy](#adapter-iam-policy).

Create another IAM role for the OpenTelemetry collector, for example:

```text
OpenChoreoXRayTracingRoleForCollector
```

Attach the custom [OpenTelemetry collector IAM policy](#opentelemetry-collector-iam-policy).

Use the following trust policy for both roles when using EKS Pod Identity:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Service": "pods.eks.amazonaws.com"
      },
      "Action": [
        "sts:AssumeRole",
        "sts:TagSession"
      ]
    }
  ]
}
```

### Step 3 - Install the module

```bash
helm upgrade --install observability-tracing-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.1.0 \
  --set instanceName="$INSTANCE_NAME" \
  --set region="$AWS_REGION"
```

### Step 4 - Create Pod Identity associations

Create two Pod Identity associations in the `$NS` namespace.

| ServiceAccount | Used by | IAM policy |
| --- | --- | --- |
| `tracing-adapter-cloudwatch` | Adapter trace queries and STS startup check. | [Adapter IAM policy](#adapter-iam-policy) |
| `tracing-cloudwatch-collector` | OpenTelemetry collector trace export to X-Ray. | [OpenTelemetry collector IAM policy](#opentelemetry-collector-iam-policy) |

All service account names must match the rendered Helm release. If you
install with a release name other than `observability-tracing-cloudwatch`,
render the chart and confirm the ServiceAccount names:

```bash
helm template observability-tracing-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-cloudwatch \
  --namespace "$NS" \
  --version 0.1.0 \
  --set instanceName="$INSTANCE_NAME" \
  --set region="$AWS_REGION" \
  | grep -A5 'kind: ServiceAccount'
```

You can create these associations from the AWS Console:

```text
EKS -> Cluster -> Access -> Pod Identity associations -> Create
```

### Step 5 - Restart workloads

EKS Pod Identity injects credentials only at pod creation time.

Recreate the workloads so new pods receive Pod Identity credentials:

```bash
kubectl -n "$NS" rollout restart deploy/tracing-adapter-cloudwatch
kubectl -n "$NS" rollout restart deploy/opentelemetry-collector
```

If the collector Deployment name differs because of your Helm release name,
inspect it first:

```bash
kubectl -n "$NS" get deploy
```

Verify that Pod Identity was injected into a new adapter pod:

```bash
kubectl -n "$NS" get pod -l app=tracing-adapter-cloudwatch -o name | head -1 \
  | xargs -I {} kubectl -n "$NS" get {} -o yaml \
  | grep -E "AWS_CONTAINER|eks-pod-identity-token"
```

If these values are missing, check that the namespace and ServiceAccount names
in the Pod Identity associations exactly match the table above.

## Installation on non-EKS clusters with static credentials

Use this path for:

- k3d
- kind
- Kubernetes clusters outside AWS
- Kubernetes clusters where Pod Identity or IRSA is not available

In this mode, the chart creates a Kubernetes Secret containing AWS credentials.
The adapter reads this Secret automatically. The OpenTelemetry collector must also be
pointed at the same Secret through `opentelemetry-collector.extraEnvsFrom`.

### Step 1 - Export shared values

```bash
export AWS_REGION=eu-north-1
export INSTANCE_NAME=openchoreo-dev
export NS=openchoreo-observability-plane
export AWS_ACCESS_KEY_ID="AKIA..."
export AWS_SECRET_ACCESS_KEY="..."
```

### Step 2 - Create an IAM user

Create an IAM user and attach the custom
[combined IAM policy](#combined-static-credentials-iam-policy).

Create access keys for this IAM user and export them as shown above.

### Step 3 - Install the module

```bash
helm upgrade --install observability-tracing-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.1.0 \
  --set instanceName="$INSTANCE_NAME" \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=tracing-cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set "opentelemetry-collector.extraEnvsFrom[0].configMapRef.name=tracing-cloudwatch-instance-env" \
  --set "opentelemetry-collector.extraEnvsFrom[1].secretRef.name=tracing-cloudwatch-aws-credentials"
```

## Wire the Observer to the adapter

After installing the CloudWatch tracing module, configure the OpenChoreo
Observer to call this adapter.

```bash
helm upgrade --install openchoreo-observability-plane \
  oci://ghcr.io/openchoreo/helm-charts/openchoreo-observability-plane \
  --namespace "$NS" \
  --reuse-values \
  --set observer.tracingAdapter.enabled=true
```

The adapter service inside the observability namespace is:

```text
http://tracing-adapter:9100
```

After this step, the OpenChoreo Observer uses the CloudWatch adapter for
tracing queries.

## Verify trace ingestion and querying

### Step 1 - Check pod status

```bash
kubectl -n "$NS" rollout status deploy/tracing-adapter-cloudwatch
kubectl -n "$NS" get pods
```

Confirm that the following workloads are running:

- `tracing-adapter-cloudwatch`
- The OpenTelemetry collector Deployment from the subchart.

### Step 2 - Check adapter health

```bash
kubectl -n "$NS" port-forward svc/tracing-adapter 9100:9100 &
curl -sf http://localhost:9100/healthz | jq .
```

Expected response:

```json
{
  "status": "healthy"
}
```

AWS credentials are checked during adapter startup. If the adapter starts
successfully, most credential or STS issues have already been caught.

### Step 3 - Send test traces

Configure an instrumented application to send OTLP traces to the collector.
Point the OTLP exporter at the collector's in-cluster Service:

```text
http://opentelemetry-collector.openchoreo-observability-plane.svc.cluster.local:4318   (HTTP)
opentelemetry-collector.openchoreo-observability-plane.svc.cluster.local:4317          (gRPC)
```

Alternatively, port-forward the collector and send synthetic traces with
realistic OpenChoreo resource attributes:

```bash
kubectl -n "$NS" port-forward svc/opentelemetry-collector 4318:4318 &
```

```bash
TRACE_ID=$(openssl rand -hex 16)
SPAN_ID=$(openssl rand -hex 8)
NOW_NS=$(date +%s)000000000

curl -s http://localhost:4318/v1/traces \
  -H 'Content-Type: application/json' \
  -d '{
  "resourceSpans": [{
    "resource": {
      "attributes": [
        {"key": "service.name", "value": {"stringValue": "snip-api-service"}},
        {"key": "openchoreo.dev/namespace", "value": {"stringValue": "default"}},
        {"key": "openchoreo.dev/component-uid", "value": {"stringValue": "ce2e8126-595a-4402-afc5-8017c4ac9f69"}},
        {"key": "openchoreo.dev/project-uid", "value": {"stringValue": "3d473b0a-5116-4b99-ae2b-c6ac3dc3e747"}},
        {"key": "openchoreo.dev/environment-uid", "value": {"stringValue": "1e228fd5-5f40-488b-aecb-ff0d5f68fa87"}}
      ]
    },
    "scopeSpans": [{
      "scope": {"name": "test"},
      "spans": [{
        "traceId": "'"$TRACE_ID"'",
        "spanId": "'"$SPAN_ID"'",
        "name": "GET /api/v1/urls",
        "kind": 2,
        "startTimeUnixNano": "'"$NOW_NS"'",
        "endTimeUnixNano": "'"$(( $(date +%s) + 1 ))"'000000000",
        "status": {"code": 1}
      }]
    }]
  }]
}'
```

Wait for traces to be exported to X-Ray:

```bash
sleep 30
```

Check X-Ray directly:

```bash
aws xray get-trace-summaries \
  --region "$AWS_REGION" \
  --start-time "$(date -u -v-10M +%FT%TZ 2>/dev/null || date -u -d '-10 minutes' +%FT%TZ)" \
  --end-time "$(date -u +%FT%TZ)" \
  | jq '.TraceSummaries | length'
```

You should see at least one trace summary from UI.

### Step 4 - Query the adapter

```bash
curl -s http://localhost:9100/api/v1alpha1/traces/query \
  -H 'Content-Type: application/json' \
  -d '{
    "startTime": "'"$(date -u -v-30M +%FT%TZ 2>/dev/null || date -u -d '-30 minutes' +%FT%TZ)"'",
    "endTime": "'"$(date -u +%FT%TZ)"'",
    "searchScope": {
      "namespace": "default"
    }
  }' | jq .
```

Expected result:

```json
{
  "traces": [
    {
      "traceId": "5759e988bd862e3fe1be46a994272793",
      "traceName": "GET /api/v1/resource",
      "durationNs": 500000000,
      "startTime": "...",
      "endTime": "...",
      "hasErrors": false
    }
  ],
  "total": 1,
  "tookMs": 123
}
```

The exact values will vary.

## Configuration reference

| Value | Default | Description |
| --- | --- | --- |
| `instanceName` | Required | Identifier for this OpenChoreo installation. Propagated to the OpenTelemetry collector and exported to X-Ray as the `openchoreo-instance-name` annotation. |
| `region` | Required | AWS region for X-Ray API calls. |
| `awsCredentials.create` | `false` | Creates a static AWS credentials Secret. Keep `false` for Pod Identity, IRSA, or instance-profile based auth. Set to `true` for k3d, kind, or non-EKS clusters. |
| `awsCredentials.name` | `""` | Name of the AWS credentials Secret. Required when `awsCredentials.create=true`. |
| `awsCredentials.accessKeyId` | Required if `create=true` | AWS access key ID. |
| `awsCredentials.secretAccessKey` | Required if `create=true` | AWS secret access key. |
| `opentelemetry-collector.enabled` | `true` | Enables the OpenTelemetry collector subchart. |
| `opentelemetry-collector.mode` | `deployment` | Runs the collector as a Deployment (push-based trace collection). |
| `opentelemetry-collector.image.repository` | `otel/opentelemetry-collector-contrib` | Collector image repository. The contrib image includes `awsxray` exporter and `k8sattributes` processor. |
| `opentelemetry-collector.image.tag` | `0.151.0` | Collector image tag. |
| `opentelemetry-collector.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `opentelemetry-collector.extraEnvsFrom` | `[{configMapRef: {name: tracing-cloudwatch-instance-env}}]` | Extra `envFrom` entries for the collector. The default ConfigMap supplies `INSTANCE_NAME` and `AWS_REGION`. Append the static AWS credentials Secret at index `1` on non-EKS clusters. |
| `adapter.enabled` | `true` | Deploys the X-Ray Tracing Adapter Deployment and Service. |
| `adapter.image.repository` | `ghcr.io/openchoreo/observability-tracing-cloudwatch-adapter` | Adapter image repository. |
| `adapter.image.tag` | `""` | Adapter image tag. Empty defaults to chart `appVersion`. |
| `adapter.image.pullPolicy` | `IfNotPresent` | Adapter image pull policy. |
| `adapter.service.port` | `9100` | Adapter HTTP port. |
| `adapter.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `adapter.logLevel` | `INFO` | Adapter log level. Supported values include `DEBUG`, `INFO`, `WARN`, and `ERROR`. |
| `adapter.resources.limits.cpu` | `200m` | CPU limit for the adapter. |
| `adapter.resources.limits.memory` | `256Mi` | Memory limit for the adapter. |
| `adapter.resources.requests.cpu` | `50m` | CPU request for the adapter. |
| `adapter.resources.requests.memory` | `128Mi` | Memory request for the adapter. |
| `tracingCollectorCustomizations.installationMode` | `singleCluster` | Collector installation mode. `singleCluster` enables the `k8sattributes` processor for label enrichment. |
| `tracingCollectorCustomizations.tailSampling.enabled` | `true` | Enables the `tail_sampling` processor for rate limiting. |
| `tracingCollectorCustomizations.tailSampling.decisionWait` | `10s` | Time to wait before making a sampling decision. |
| `tracingCollectorCustomizations.tailSampling.numTraces` | `100` | Maximum number of traces to keep in memory during the decision wait. |
| `tracingCollectorCustomizations.tailSampling.expectedNewTracesPerSec` | `10` | Expected rate of new traces per second. |
| `tracingCollectorCustomizations.tailSampling.decisionCache.sampledCacheSize` | `10000` | Size of the sampled decisions cache. |
| `tracingCollectorCustomizations.tailSampling.decisionCache.nonSampledCacheSize` | `1000` | Size of the non-sampled decisions cache. |
| `tracingCollectorCustomizations.tailSampling.spansPerSecond` | `10` | Maximum spans per second for the rate limiting policy. |

Unlike the logs and metrics CloudWatch modules, this tracing module does not
expose a retention value. AWS X-Ray trace retention is service-managed and fixed
at 30 days; it is not backed by a customer-managed CloudWatch Logs log group
with a configurable retention policy.
