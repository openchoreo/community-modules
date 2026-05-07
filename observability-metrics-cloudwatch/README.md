# Observability Metrics Module for AWS CloudWatch

|               |           |
| ------------- | --------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_metrics_cloudwatch)](https://codecov.io/gh/openchoreo/community-modules) |

The **Observability Metrics Module for AWS CloudWatch** sends OpenChoreo
resource metrics to **AWS CloudWatch Metrics** and exposes them back to the
OpenChoreo Observer through the standard **OpenChoreo Metrics Adapter API**.
It also supports metric-based alerting by translating OpenChoreo alert rules
into **CloudWatch metric math alarms** that evaluate
`(usage / limit) * 100` against a percentage threshold, so an OpenChoreo
threshold of `80` for `cpu_usage` fires when the pod uses more than 80% of
its CPU limit.

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
7. [Verify metric ingestion and querying](#verify-metric-ingestion-and-querying)
8. [Enable metric alerting](#enable-metric-alerting)
9. [Expose the alert webhook through EventBridge](#expose-the-alert-webhook-through-eventbridge)
10. [Alerting behavior](#alerting-behavior)
11. [Shared webhook secret](#shared-webhook-secret)
12. [Configuration reference](#configuration-reference)
13. [k3d and kind compatibility](#k3d-and-kind-compatibility)
14. [Limitations](#limitations)
15. [Troubleshooting](#troubleshooting)

## Architecture

This module has two main responsibilities:

1. **Metric ingestion and query**
2. **Alerting**

The chart deploys two workloads in the OpenChoreo observability plane:

1. An **AWS Distro for OpenTelemetry (ADOT) collector** DaemonSet that scrapes
   pod CPU and memory usage from kubelet, is intended to scrape pod requests
   and limits from `kube-state-metrics`, enriches series with OpenChoreo pod
   labels, and publishes metrics to CloudWatch through the EMF exporter.
2. A Go **CloudWatch Metrics Adapter** Deployment that implements the
   OpenChoreo Metrics Adapter API.

The OpenTelemetry collector writes Embedded Metric Format events to this CloudWatch Logs
log group:

```text
/aws/openchoreo/<clusterName>/metrics
```

Each collector DaemonSet pod writes to a node-named log stream such as
`ip-10-0-3-168.us-east-1.compute.internal`.

CloudWatch then promotes those EMF records into metrics under this namespace:

```text
OpenChoreo/Metrics
```

The intended metric dimensions are:

- `ComponentUID`
- `EnvironmentUID`
- `Namespace`

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/metrics/query` | Runs a CloudWatch `GetMetricData` request for resource metrics. |
| `POST /api/v1alpha1/alerts/rules` | Creates a CloudWatch metric math alarm evaluating `(usage / limit) * 100` against the threshold percentage. |
| `GET /api/v1alpha1/alerts/rules/{ruleName}` | Gets the alert rule identified by `{ruleName}`. |
| `PUT /api/v1alpha1/alerts/rules/{ruleName}` | Updates the alert rule identified by `{ruleName}`. |
| `DELETE /api/v1alpha1/alerts/rules/{ruleName}` | Deletes the CloudWatch metric math alarm for the alert rule identified by `{ruleName}`. |
| `POST /api/v1alpha1/alerts/webhook` | Receives forwarded CloudWatch alarm events from EventBridge and forwards them to the Observer. |
| `GET /healthz` | Readiness check. Returns `200` once the adapter is ready. |
| `GET /livez` | Liveness check. Does not call AWS, so transient AWS or DNS issues do not crash-loop the pod. |

## Prerequisites

Before installing this module, make sure the following are available.

### OpenChoreo prerequisites

- OpenChoreo is installed.
- The `openchoreo-observability-plane` Helm chart is installed.
- Workload pods include OpenChoreo labels such as:
  - `openchoreo.dev/component-uid`
  - `openchoreo.dev/environment-uid`
  - `openchoreo.dev/project-uid`

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

- Kubernetes API access to pods, services, endpoints, namespaces, nodes, and
  replicasets.
- Kubelet stats access through the node kubelet.
- A running `kube-state-metrics` Service. The bundled scrape config keeps any
  endpoint port named `http` or `http-metrics`, so the upstream
  [`prometheus-community/kube-state-metrics`](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-state-metrics)
  chart works out of the box.

If `kube-state-metrics` is not already installed, install it before deploying
this module. To make the OpenChoreo UID labels available on the
`kube_pod_container_resource_*` series, allowlist them on `kube_pod_labels`:

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install kube-state-metrics prometheus-community/kube-state-metrics \
  --namespace kube-system \
  --set 'metricLabelsAllowlist={pods=[openchoreo.dev/component-uid\,openchoreo.dev/environment-uid\,openchoreo.dev/project-uid]}'
```

### AWS prerequisites

You need:

- An AWS account.
- An AWS region, for example `us-east-1`.
- A cluster name, for example `openchoreo-dev`.
- An IAM principal with the permissions described in [IAM permissions](#iam-permissions).

For EKS, use IAM roles with **EKS Pod Identity** or IRSA. For non-EKS clusters
such as k3d or kind, use an IAM user with access keys.

## IAM permissions

The CloudWatch metrics adapter needs permissions for three paths:

1. Startup identity check.
2. CloudWatch metric queries.
3. CloudWatch alarm management.

The OpenTelemetry collector needs permission to write EMF records to CloudWatch Logs.

Use these policies based on the credential model:

- **EKS Pod Identity or IRSA:** keep the adapter and OpenTelemetry collector policies
  separate and attach them to separate roles. This keeps each ServiceAccount
  least-privileged.
- **Static credentials:** use one IAM user and attach the
  [combined static-credentials IAM policy](#combined-static-credentials-iam-policy),
  because the same Kubernetes Secret is shared by the adapter and OpenTelemetry collector
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
      "Sid": "MetricsQuery",
      "Effect": "Allow",
      "Action": [
        "cloudwatch:GetMetricData",
        "cloudwatch:ListMetrics"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MetricAlarms",
      "Effect": "Allow",
      "Action": [
        "cloudwatch:PutMetricAlarm",
        "cloudwatch:DescribeAlarms",
        "cloudwatch:DeleteAlarms",
        "cloudwatch:TagResource",
        "cloudwatch:ListTagsForResource"
      ],
      "Resource": "*"
    }
  ]
}
```

### OpenTelemetry collector and log-retention IAM policy

Create the following custom IAM policy and attach it to the OpenTelemetry
collector IAM principal when using separate EKS identities. If
`metrics.logRetention.enabled=true`, also attach this policy to the
log-retention Job IAM principal configured through
`metrics.logRetention.serviceAccount.annotations`.

`logs:PutRetentionPolicy` is required on the **collector** policy because
the awsemfexporter is configured with `log_retention` and reapplies
retention each time it (re)creates the EMF log group — including after a
manual `delete-log-group`. Without this permission, an out-of-band
deletion would result in a recreated log group with `Never Expire`.

Replace:

- `<region>` with your AWS region.
- `<account-id>` with your AWS account ID.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "WriteEMFLogs",
      "Effect": "Allow",
      "Action": [
        "logs:PutLogEvents",
        "logs:CreateLogGroup",
        "logs:CreateLogStream",
        "logs:DescribeLogGroups",
        "logs:DescribeLogStreams",
        "logs:PutRetentionPolicy"
      ],
      "Resource": "arn:aws:logs:ap-southeast-1:447777059444:log-group:/aws/openchoreo/*/metrics:*"
    }
  ]
}
```

### Combined static-credentials IAM policy

Use this policy for non-EKS clusters where one IAM user backs the shared static
AWS credentials Secret.

Replace:

- `<region>` with your AWS region.
- `<account-id>` with your AWS account ID.

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
      "Sid": "MetricsQueryAndAlarms",
      "Effect": "Allow",
      "Action": [
        "cloudwatch:GetMetricData",
        "cloudwatch:ListMetrics",
        "cloudwatch:PutMetricAlarm",
        "cloudwatch:DescribeAlarms",
        "cloudwatch:DeleteAlarms",
        "cloudwatch:TagResource",
        "cloudwatch:ListTagsForResource"
      ],
      "Resource": "*"
    },
    {
      "Sid": "WriteEMFLogs",
      "Effect": "Allow",
      "Action": [
        "logs:PutLogEvents",
        "logs:CreateLogGroup",
        "logs:CreateLogStream",
        "logs:DescribeLogGroups",
        "logs:DescribeLogStreams",
        "logs:PutRetentionPolicy"
      ],
      "Resource": "arn:aws:logs:<region>:<account-id>:log-group:/aws/openchoreo/*/metrics:*"
    }
  ]
}
```

## Installation on EKS with Pod Identity

This is the recommended installation path for EKS clusters.

### Step 1 - Export shared values

```bash
export AWS_REGION=ap-southeast-1
export CLUSTER_NAME=openchoreo-metric-test
export NS=openchoreo-observability-plane
export WEBHOOK_SHARED_SECRET="$(openssl rand -base64 32)"
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
OpenChoreoCloudWatchMetricsRoleForAdapter
```

Attach the custom [Adapter IAM policy](#adapter-iam-policy).

Create another IAM role for the OpenTelemetry collector, for example:

```text
OpenChoreoCloudWatchMetricsRoleForAdot
```

Attach the custom [OpenTelemetry collector IAM policy](#adot-collector-iam-policy).

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
helm upgrade --install observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.1.0 \
  --set clusterName="$CLUSTER_NAME" \
  --set region="$AWS_REGION" \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

### Step 4 - Create Pod Identity associations

Create three Pod Identity associations in the `$NS` namespace.

| ServiceAccount | Used by | IAM policy |
| --- | --- | --- |
| `metrics-adapter-cloudwatch` | Adapter metric queries, alert CRUD, and webhook handling. | [Adapter IAM policy](#adapter-iam-policy) |
| `observability-metrics-cloudwatch-adotcollector` | OpenTelemetry collector metric export to CloudWatch Logs. | [OpenTelemetry collector and log-retention IAM policy](#opentelemetry-collector-and-log-retention-iam-policy) |
| `metrics-cloudwatch-log-retention` | Helm post-install/post-upgrade Job that creates the EMF log group and applies `metrics.logRetentionDays`. | [OpenTelemetry collector and log-retention IAM policy](#opentelemetry-collector-and-log-retention-iam-policy) |

All three service account names must match the rendered Helm release. If you
install with a release name other than `observability-metrics-cloudwatch`,
render the chart and confirm the OpenTelemetry collector ServiceAccount name:

```bash
helm template observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --namespace "$NS" \
  --version 0.1.0 \
  --set clusterName="$CLUSTER_NAME" \
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
kubectl -n "$NS" rollout restart deploy/metrics-adapter-cloudwatch
kubectl -n "$NS" rollout restart ds/observability-metrics-cloudwatch-adotcollector-agent
```

If the log-retention Job already failed because its Pod Identity association
was created late, you must rerun it manually — `helm upgrade` re-fires the
post-upgrade hook, but the failed Job has a fixed name and a new one cannot
be created until it is deleted.

Confirm the Pod Identity association for
`metrics-cloudwatch-log-retention` is attached **before** rerunning the
upgrade. Otherwise the new Job pod will fail with the same
`no EC2 IMDS role found` error and the upgrade will fail again with
`BackoffLimitExceeded`.

```bash
# 1. Delete the failed Job so a new one can be created with the same name.
kubectl -n "$NS" delete job metrics-cloudwatch-log-retention --ignore-not-found

# 2. Re-fire the post-upgrade hook.
helm upgrade observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --namespace "$NS" --reset-then-reuse-values

# 3. Watch the new Job complete (Ctrl+C once COMPLETIONS shows 1/1).
kubectl -n "$NS" get job metrics-cloudwatch-log-retention -w
```

If the new Job pod fails again, inspect its logs to confirm credentials are
being injected:

```bash
kubectl -n "$NS" logs -l job-name=metrics-cloudwatch-log-retention --tail=100
```

If the OpenTelemetry collector DaemonSet name differs because of your Helm release name, inspect
it first:

```bash
kubectl -n "$NS" get ds
```

Verify that Pod Identity was injected into a new adapter pod:

```bash
kubectl -n "$NS" get pod -l app=metrics-adapter-cloudwatch -o name | head -1 \
  | xargs -I {} kubectl -n "$NS" get {} -o yaml \
  | grep -E "AWS_CONTAINER|eks-pod-identity-token"
```

If these values are missing, check that the namespace and ServiceAccount names
in the Pod Identity associations exactly match the table above.


### Wire the Observer to the adapter

After installing the CloudWatch metrics module, configure the OpenChoreo
Observer to call this adapter.

```bash
helm upgrade --install openchoreo-observability-plane \
  oci://ghcr.io/openchoreo/helm-charts/openchoreo-observability-plane \
  --version 1.0.1-hotfix.1 \
  --namespace "$NS" \
  --reuse-values \
  --set observer.metricsAdapter.enabled=true
```

The adapter service inside the observability namespace is:

```text
http://metrics-adapter:9099
```

After this step, the OpenChoreo Observer uses the CloudWatch adapter for
metrics queries.

## Installation on non-EKS clusters with static credentials

Use this path for:

- k3d
- kind
- Kubernetes clusters outside AWS
- Kubernetes clusters where Pod Identity or IRSA is not available

In this mode, the chart creates a Kubernetes Secret containing AWS credentials.
The adapter reads this Secret automatically. The OpenTelemetry collector must also be
pointed at the same Secret through `adotcollector.extraEnvsFrom`.

### Step 1 - Export shared values

```bash
export AWS_REGION=us-east-1
export CLUSTER_NAME=openchoreo-dev
export NS=openchoreo-observability-plane
export WEBHOOK_SHARED_SECRET="$(openssl rand -base64 32)"
export AWS_ACCESS_KEY_ID="AKIA..."
export AWS_SECRET_ACCESS_KEY="..."
```

### Step 2 - Create an IAM user

Create an IAM user and attach the custom
[combined static-credentials IAM policy](#combined-static-credentials-iam-policy).

Create access keys for this IAM user and export them as shown above.

### Step 3 - Install the module

```bash
helm upgrade --install observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.1.0 \
  --set clusterName="$CLUSTER_NAME" \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=metrics-cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set "adotcollector.extraEnvsFrom[0].configMapRef.name=metrics-cloudwatch-cluster-env" \
  --set "adotcollector.extraEnvsFrom[1].secretRef.name=metrics-cloudwatch-aws-credentials" \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

This enables the static-credentials path:

- The chart creates a Kubernetes Secret.
- The adapter reads credentials from that Secret.
- The OpenTelemetry collector reads credentials from the same Secret through
  `adotcollector.extraEnvsFrom`.

## Wire the Observer to the adapter

After installing the CloudWatch metrics module, configure the OpenChoreo
Observer to call this adapter.

```bash
helm upgrade --install openchoreo-observability-plane \
  oci://ghcr.io/openchoreo/helm-charts/openchoreo-observability-plane \
  --version 1.0.1-hotfix.1 \
  --namespace "$NS" \
  --reuse-values \
  --set observer.metricsAdapter.enabled=true
```

The adapter service inside the observability namespace is:

```text
http://metrics-adapter:9099
```

After this step, the OpenChoreo Observer uses the CloudWatch adapter for
metrics queries.

## Verify metric ingestion and querying

### Step 1 - Check pod status

```bash
kubectl -n "$NS" rollout status deploy/metrics-adapter-cloudwatch
kubectl -n "$NS" get pods
```

Confirm that the following workloads are running:

- `metrics-adapter-cloudwatch`
- The OpenTelemetry collector DaemonSet from the `adotcollector` subchart.

### Step 2 - Check adapter health

```bash
kubectl -n "$NS" port-forward svc/metrics-adapter 9099:9099 &
curl -sf http://localhost:9099/healthz | jq .
```

Expected response:

```json
{
  "status": "healthy"
}
```

AWS credentials are checked during adapter startup. If the adapter starts
successfully, most credential or STS issues have already been caught.

### Step 3 - Run a smoke test pod

Create a temporary pod with synthetic OpenChoreo labels and CPU activity:

```bash
kubectl run metrics-cloudwatch-smoke-test --restart=Never \
  --namespace default \
  --labels='openchoreo.dev/namespace=default,openchoreo.dev/component-uid=smoke-comp-1,openchoreo.dev/environment-uid=smoke-env-1,openchoreo.dev/project-uid=smoke-proj-1' \
  --image=busybox:1.36 \
  -- sh -c 'while true; do :; done'
```

Wait for OpenTelemetry collector to scrape and CloudWatch to promote EMF metrics:

```bash
sleep 120
```

Check CloudWatch directly:

```bash
aws cloudwatch list-metrics \
  --region "$AWS_REGION" \
  --namespace OpenChoreo/Metrics \
  --dimensions Name=ComponentUID,Value=smoke-comp-1
```

You should see metrics such as:

- `pod_cpu_usage`
- `pod_memory_usage`

The request and limit metrics may remain absent in v0 until the
kube-state-metrics request/limit transform is completed.

### Step 4 - Query the adapter

```bash
curl -s http://localhost:9099/api/v1/metrics/query \
  -H 'Content-Type: application/json' \
  -d '{
    "metric": "resource",
    "startTime": "'"$(date -u -v-30M +%FT%TZ 2>/dev/null || date -u -d '-30 minutes' +%FT%TZ)"'",
    "endTime": "'"$(date -u +%FT%TZ)"'",
    "step": "1m",
    "searchScope": {
      "namespace": "default",
      "componentUid": "smoke-comp-1",
      "environmentUid": "smoke-env-1"
    }
  }' | jq .
```

Expected result:

```json
{
  "cpuUsage": [
    {
      "timestamp": "...",
      "value": 0.12
    }
  ],
  "cpuRequests": [],
  "cpuLimits": [],
  "memoryUsage": [
    {
      "timestamp": "...",
      "value": 12345678
    }
  ],
  "memoryRequests": [],
  "memoryLimits": []
}
```

The exact values will vary.

#### Step and CloudWatch period

The `step` field is accepted as an Observer/UI display interval, but the
CloudWatch adapter must query with a period that CloudWatch supports for the
stored metric resolution.

OpenChoreo resource metrics exported through this module are regular
CloudWatch metrics. Recent datapoints are available at a minimum granularity
of one minute, and older datapoints follow CloudWatch retention tiers. The
adapter normalizes the requested step before calling `GetMetricData`:

| Query age from `startTime` | Requested `step` | CloudWatch `Period` used |
| --- | --- | --- |
| Recent data | `15s` | `60` |
| Recent data | `30s` | `60` |
| Recent data | `1m` | `60` |
| Recent data | `61s` | `120` |
| Recent data | `15m` | `900` |
| Older than 15 days | `1m` | `300` |
| Older than 15 days | `15m` | `900` |
| Older than 63 days | `5m` | `3600` |
| Older than 63 days | `6h` | `21600` |

This lets the UI choose a step based on the selected time window without
causing CloudWatch validation errors. The response contains CloudWatch's actual
period-aligned datapoints; the adapter does not interpolate missing sub-minute
points.

Clean up the smoke pod:

```bash
kubectl -n default delete pod metrics-cloudwatch-smoke-test --ignore-not-found
```

If all arrays remain empty after waiting another few minutes, the problem is
usually in the ingestion path rather than the adapter. Check
[Troubleshooting](#troubleshooting).

## Enable metric alerting

Enable alerting after the base metric query path is working.

```bash
helm upgrade observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --namespace "$NS" \
  --reuse-values \
  --set adapter.alerting.enabled=true
```

The alert CRUD endpoints work even when `adapter.alerting.enabled=false`,
but the webhook handler silently drops every forwarded event in that mode
because `OBSERVER_URL` is not injected into the adapter pod. 

If `adapter.alerting.webhookAuth.enabled=true` and
`adapter.alerting.webhookAuth.sharedSecret` were set during installation, the
adapter now requires the following header on alert webhook calls:

```text
X-OpenChoreo-Webhook-Token
```

### Test alert CRUD

`condition.threshold` is interpreted as a **percentage of the pod's
resource limit** (0–100). The adapter creates a CloudWatch metric math
alarm that evaluates `(pod_<resource>_usage / pod_<resource>_limit) * 100`
against the threshold, so `"threshold": 80` with `"metric": "cpu_usage"`
fires when the pod's CPU usage exceeds 80% of its CPU limit.

```bash
curl -s http://localhost:9099/api/v1alpha1/alerts/rules \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {
      "name": "high-cpu-smoke",
      "namespace": "default",
      "projectUid": "11111111-1111-1111-1111-111111111111",
      "environmentUid": "22222222-2222-2222-2222-222222222222",
      "componentUid": "33333333-3333-3333-3333-333333333333"
    },
    "source": {
      "metric": "cpu_usage"
    },
    "condition": {
      "enabled": true,
      "window": "5m",
      "interval": "1m",
      "operator": "gt",
      "threshold": 80
    }
  }' | jq .
```

Verify the alarm exists:

```bash
aws cloudwatch describe-alarms \
  --region "$AWS_REGION" \
  --alarm-name-prefix oc-metrics-alert-
```

## Expose the alert webhook through EventBridge

CloudWatch alarms cannot directly send HTTP requests to the adapter. To deliver
alarm events, use EventBridge:

```text
CloudWatch Alarm State Change
    |
    v
EventBridge Rule
    |
    v
EventBridge API Destination
    |
    v
/api/v1alpha1/alerts/webhook
    |
    v
CloudWatch Metrics Adapter
    |
    v
OpenChoreo Observer
```

Setting up EventBridge requires three resources: a **connection** (carries the
authentication header), an **API destination** (the adapter webhook URL), and a
**rule** (matches CloudWatch alarm events and routes them to the destination).

#### Step 1 — Create an EventBridge connection

The connection stores the `X-OpenChoreo-Webhook-Token` header that the adapter
requires when `adapter.alerting.webhookAuth.enabled=true`.

#### Step 2 — Create an API destination

Point the destination at the adapter's public webhook URL. 

`$ADAPTER_WEBHOOK_PUBLIC_URL` must include the full path, for example:

```text
https://metrics-webhook.example.com/api/v1alpha1/alerts/webhook
```

#### Step 3 — Create an EventBridge rule

Use a custom event pattern so only managed metric alarms reach the adapter.
The `alarmName` prefix filter ensures log-module alarms (`oc-logs-alert`)
are not routed here.

```bash
{
  "source": ["aws.cloudwatch"],
  "detail-type": ["CloudWatch Alarm State Change"],
  "detail": {
    "state": {
      "value": ["ALARM"]
    },
    "alarmName": [{
      "prefix": "oc-metrics-alert"
    }]
  }
}
```

#### Step 4 — Attach the API destination as a target

EventBridge needs an IAM role that allows it to invoke the API destination.

### Development-only webhook test with port-forward and ngrok

For local testing, you can expose the adapter through a temporary public
tunnel using ngrok.
Docs: https://openchoreo.dev/docs/tutorials/component-alerts-and-incidents/

## Shared webhook secret

When webhook authentication is enabled, the adapter rejects webhook
requests that do not include the configured token in this header:

```text
X-OpenChoreo-Webhook-Token
```

The same token must be configured in the EventBridge API destination
connection.

### Option 1 - Inline secret

This is convenient for development:

```bash
--set adapter.alerting.webhookAuth.enabled=true \
--set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

However, the secret becomes visible in Helm release values. Anyone with access
to `helm get values` may be able to read it.

### Option 2 - Existing Kubernetes Secret

This is recommended for production.

Create the Secret:

```bash
kubectl -n "$NS" create secret generic openchoreo-metrics-webhook-token \
  --from-literal=token="$WEBHOOK_SHARED_SECRET"
```

Point the chart to the Secret:

```bash
helm upgrade observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --namespace "$NS" \
  --reuse-values \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="" \
  --set adapter.alerting.webhookAuth.sharedSecretRef.name=openchoreo-metrics-webhook-token \
  --set adapter.alerting.webhookAuth.sharedSecretRef.key=token
```

Pass `sharedSecret=""` when switching from inline secret to Secret reference.
Otherwise, the previous inline value may continue to shadow the Secret
reference.

## Configuration reference

| Value | Default | Description |
| --- | --- | --- |
| `clusterName` | Required | OpenChoreo or EKS cluster name. Propagated to the OpenTelemetry collector and adapter. |
| `region` | Required | AWS region for CloudWatch Logs, CloudWatch Metrics, and API calls. |
| `awsCredentials.create` | `false` | Creates a static AWS credentials Secret. Keep `false` for Pod Identity, IRSA, or instance-profile based auth. Set to `true` for k3d, kind, or non-EKS clusters. |
| `awsCredentials.name` | `""` | Name of the AWS credentials Secret. Required when `awsCredentials.create=true`. |
| `awsCredentials.accessKeyId` | Required if `create=true` | AWS access key ID. |
| `awsCredentials.secretAccessKey` | Required if `create=true` | AWS secret access key. |
| `metrics.namespace` | `OpenChoreo/Metrics` | CloudWatch metric namespace used by the adapter. |
| `metrics.logGroup` | `""` | CloudWatch Logs log group used by the OpenTelemetry EMF exporter. Empty defaults to `/aws/openchoreo/<clusterName>/metrics`. |
| `metrics.logRetentionDays` | `7` | CloudWatch Logs retention period for the EMF log group. Must be one of the retention values supported by CloudWatch Logs. |
| `metrics.logRetention.enabled` | `true` | Runs a Helm post-install/post-upgrade Job that creates the EMF log group if needed and applies `metrics.logRetentionDays`. |
| `metrics.logRetention.serviceAccount.create` | `true` | Creates a ServiceAccount for the log-retention Job. |
| `metrics.logRetention.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations used by the log-retention Job. |
| `metrics.logRetention.image.repository` | `public.ecr.aws/aws-cli/aws-cli` | AWS CLI image used by the log-retention Job. |
| `metrics.logRetention.image.tag` | `2.15.57` | AWS CLI image tag used by the log-retention Job. |
| `adotcollector.enabled` | `true` | Enables the ADOT subchart. |
| `adotcollector.mode` | `daemonset` | Runs one collector per node. |
| `adotcollector.image.repository` | `otel/opentelemetry-collector-contrib` | Collector image repository. The contrib image includes kubeletstats and awsemfexporter. |
| `adotcollector.image.tag` | `0.109.0` | Collector image tag. |
| `adotcollector.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `adotcollector.extraEnvsFrom` | `[{configMapRef: {name: metrics-cloudwatch-cluster-env}}]` | Extra `envFrom` entries for the OpenTelemetry collector. The default ConfigMap supplies `EMF_LOG_GROUP_NAME`; append the static AWS credentials Secret at index `1` on non-EKS clusters. |
| `adapter.enabled` | `true` | Deploys the CloudWatch Metrics Adapter Deployment and Service. |
| `adapter.replicas` | `1` | Number of adapter replicas. |
| `adapter.image.repository` | `ghcr.io/openchoreo/observability-metrics-cloudwatch-adapter` | Adapter image repository. |
| `adapter.image.tag` | `""` | Adapter image tag. Empty defaults to chart `appVersion`. |
| `adapter.service.port` | `9099` | Adapter HTTP port. |
| `adapter.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `adapter.logLevel` | `INFO` | Adapter log level. Supported values include `DEBUG`, `INFO`, `WARN`, and `ERROR`. |
| `adapter.queryTimeoutSeconds` | `30` | Reserved query timeout setting. |
| `adapter.alerting.enabled` | `false` | Enables alert rule CRUD and webhook forwarding configuration. |
| `adapter.alerting.alarmActionArns` | `[]` | Optional alarm action ARNs. Leave empty when using EventBridge. |
| `adapter.alerting.okActionArns` | `[]` | Optional OK-state action ARNs. |
| `adapter.alerting.insufficientDataActionArns` | `[]` | Optional insufficient-data action ARNs. |
| `adapter.alerting.observerUrl` | `http://observer-internal:8081` | Base URL of the Observer used when forwarding webhook events. |
| `adapter.alerting.snsAllowSubscribeConfirm` | `false` | Allows signed SNS subscription confirmation messages to be confirmed by the adapter. |
| `adapter.alerting.forwardRecovery` | `false` | Forward `OK` and `INSUFFICIENT_DATA` transitions in addition to `ALARM`. |
| `adapter.alerting.webhookAuth.enabled` | `false` | Requires the shared webhook token for non-SNS webhook requests. |
| `adapter.alerting.webhookAuth.sharedSecret` | `""` | Inline shared secret for webhook authentication. Suitable for development only. |
| `adapter.alerting.webhookAuth.sharedSecretRef.name` | `""` | Existing Kubernetes Secret name containing the webhook token. Recommended for production. |
| `adapter.alerting.webhookAuth.sharedSecretRef.key` | `token` | Key inside the existing Secret. |
| `adapter.alerting.webhookRoute.enabled` | `false` | Creates a Gateway API HTTPRoute exposing only `/api/v1alpha1/alerts/webhook`. |
| `adapter.alerting.webhookRoute.parentRef.name` | `gateway-default` | Gateway to attach to. Required when webhookRoute is enabled. |
| `adapter.alerting.webhookRoute.parentRef.namespace` | `""` | Namespace of the parent Gateway. Leave empty for the release namespace. |
| `adapter.alerting.webhookRoute.parentRef.sectionName` | `""` | Optional Gateway listener section name. |
| `adapter.alerting.webhookRoute.hostnames` | `[]` | Optional hostnames matched at the route level. Leave empty to inherit the Gateway listener's hostname. |
| `adapter.networkPolicy.enabled` | `false` | Creates a NetworkPolicy for adapter ingress traffic. |
| `adapter.networkPolicy.observerNamespaceLabels` | `{kubernetes.io/metadata.name: openchoreo-observability-plane}` | Namespace labels allowed to call the adapter from the Observer. |
| `adapter.networkPolicy.observerPodLabels` | `{}` | Pod labels allowed to call the adapter from the Observer. Tune per deployment. |
| `adapter.networkPolicy.gatewayNamespaceLabels` | `{}` | Namespace labels of the Gateway data-plane pods allowed to proxy the webhook. Set when webhookRoute is enabled. |
| `adapter.networkPolicy.allowProbeIPBlock` | `""` | Optional node CIDR for kubelet probes when required by the CNI. |

If you override `metrics.logGroup`, update the CloudWatch Logs IAM policy
resource ARN to match that log group. The examples above use
`/aws/openchoreo/*/metrics` so the default cluster-scoped groups are covered.
