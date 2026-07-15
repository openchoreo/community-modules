# Observability Metrics Module for AWS CloudWatch

|               |           |
| ------------- | --------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_metrics_aws_cloudwatch)](https://codecov.io/gh/openchoreo/community-modules) |

This module supports both:

- **EKS clusters** using **EKS Pod Identity**.
- **Non-EKS Kubernetes clusters** such as k3d, kind, or Kubernetes running outside AWS, using static AWS credentials.

## Table of contents

1. [Architecture](#architecture)
2. [Choose a deployment topology](#choose-a-deployment-topology)
3. [Prerequisites](#prerequisites)
4. [IAM permissions](#iam-permissions)
5. [Installation on EKS with Pod Identity](#installation-on-eks-with-pod-identity)
6. [Installation on non-EKS clusters with static credentials](#installation-on-non-eks-clusters-with-static-credentials)
7. [Metric alerting](#metric-alerting)
8. [Expose the alert webhook through EventBridge](#expose-the-alert-webhook-through-eventbridge)
9. [Shared webhook secret](#shared-webhook-secret)
10. [Troubleshooting](#troubleshooting)
11. [Configuration reference](#configuration-reference)

## Architecture

This module has two main responsibilities:

1. **Metric ingestion and query**
2. **Alerting**

In the default single-cluster topology, the chart deploys two workloads:

1. An **OpenTelemetry collector** DaemonSet that collects resource and Cilium Hubble HTTP metrics, associates them with OpenChoreo components, and publishes them to CloudWatch.
2. A Go **CloudWatch Metrics Adapter** Deployment that implements the OpenChoreo Metrics Adapter API.

The OpenTelemetry collector writes Embedded Metric Format events to this CloudWatch Logs log group:

```text
/aws/openchoreo/metrics
```

Each collector DaemonSet pod writes to a node-named log stream such as `ip-10-0-3-168.us-east-1.compute.internal`.

CloudWatch then promotes those EMF records into metrics under this namespace:

```text
OpenChoreo/Metrics
```

The intended metric dimensions are:

- `ComponentUID`
- `EnvironmentUID`
- `Namespace`

All participating clusters write to the same configured EMF log group and metric namespace.

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/metrics/query` | Returns resource metrics or per-component HTTP RED metrics. |
| `POST /api/v1alpha1/metrics/runtime-topology` | Returns observed component-to-component HTTP traffic edges. |
| `POST /api/v1alpha1/alerts/rules` | Creates a CloudWatch metric math alarm evaluating `(usage / limit) * 100` against the threshold percentage. |
| `GET /api/v1alpha1/alerts/rules/{ruleName}` | Gets the alert rule identified by `{ruleName}`. |
| `PUT /api/v1alpha1/alerts/rules/{ruleName}` | Updates the alert rule identified by `{ruleName}`. |
| `DELETE /api/v1alpha1/alerts/rules/{ruleName}` | Deletes the CloudWatch metric math alarm for the alert rule identified by `{ruleName}`. |
| `POST /api/v1alpha1/alerts/webhook` | Receives forwarded CloudWatch alarm events from EventBridge and forwards them to the Observer. |
| `GET /healthz` | Readiness check. Returns `200` once the adapter is ready. |
| `GET /livez` | Liveness check. Does not call AWS, so transient AWS or DNS issues do not crash-loop the pod. |

### HTTP RED metrics (Cilium Hubble)

`POST /api/v1/metrics/query` with `metric: http` returns per-component HTTP RED
time series from Cilium Hubble traffic data:

- `requestCount`, `successfulRequestCount` (1xx/2xx/3xx), and
  `unsuccessfulRequestCount` (4xx/5xx) are returned as per-second rates.
- `meanLatency` is returned in seconds.
- `latencyP50`, `latencyP90`, and `latencyP99` are returned when Hubble latency
  histogram data is available.
- The minimum supported `step` is one minute. Sub-minute values are rounded up.

### Runtime topology (Cilium Hubble)

The `POST /api/v1alpha1/metrics/runtime-topology` endpoint returns the live HTTP
traffic graph as component-to-component edges. It is powered by **Cilium Hubble**
L7 metrics rather than the CloudWatch metric path:

- The collector enriches Hubble traffic data with OpenChoreo component identity
  and writes it to CloudWatch EMF logs.
- The adapter queries those logs with CloudWatch Logs Insights to return
  component-to-component edges and HTTP RED time series.

Prerequisites:

- Cilium installed with Hubble metrics enabled, including the **`httpV2`** metric
  and source/destination namespace and IP labels.
- The workloads whose traffic should appear must have **Layer-7 visibility**
  (OpenChoreo components get this from their generated `CiliumNetworkPolicy`).
  Traffic between pods without L7 visibility is not captured.

Reported per edge: `requestCount`, `unsuccessfulRequestCount` (4xx/5xx),
`meanLatency`, and latency `p50`/`p90`/`p99` when latency histogram data is
available. The current scope is component-to-component edges. Gateway,
external-service, and per-node aggregates are not included.

#### Latency percentiles

Latency percentiles are derived from Hubble HTTP latency histograms for both
runtime topology and `metric: http`. Percentile accuracy is limited by Hubble's
histogram bucket resolution, so very fast requests may appear rounded to the
nearest available bucket.

## Choose a deployment topology

Choose the deployment topology first, then choose the AWS authentication model for each cluster.

| Topology | Install location | Purpose | Required Helm values |
| --- | --- | --- | --- |
| Single cluster | The OpenChoreo cluster where the observability plane and workloads run together. | Deploys the adapter, OpenTelemetry collector, kube-state-metrics, and retention Job. | Defaults. |
| Observability plane cluster | The cluster where the OpenChoreo observability plane is installed. | Deploys only the CloudWatch Metrics Adapter. | `opentelemetry-collector.enabled=false`, `kubeStateMetrics.enabled=false`, `metrics.retention.enabled=false` |
| Data-plane cluster | Each cluster that runs OpenChoreo workloads. | Deploys only metric ingestion components: OpenTelemetry collector, kube-state-metrics, and retention Job. | `adapter.enabled=false` |

For one OpenChoreo installation, keep these values identical across all participating clusters:

- `region`
- `metrics.namespace`

CloudWatch Metrics is the shared managed backend. Remote workload clusters write directly to CloudWatch and do not need network connectivity back to a self-hosted metrics datastore. All clusters that belong to one OpenChoreo installation write to the same EMF log group, and the observability-plane adapter reads from the shared metric namespace.

## Prerequisites

Before installing this module, make sure the following are available.

### OpenChoreo prerequisites

- OpenChoreo is installed.
- The `openchoreo-observability-plane` Helm chart is installed.

See the [OpenChoreo documentation](https://openchoreo.dev/docs) for the base installation steps.

### Local tooling

Install the following tools on your machine:

- `helm`
- `kubectl`
- `jq`
- `aws` CLI v2

### AWS prerequisites

You need:

- An AWS account.
- An AWS region, for example `us-east-1`.
- An IAM principal with the permissions described in [IAM permissions](#iam-permissions).

For EKS, use an IAM role with **EKS Pod Identity**. For non-EKS clusters such as k3d or kind, use an IAM user with access keys.

### kube-state-metrics

The chart installs kube-state-metrics by default with the required `metricLabelsAllowlist` for the OpenChoreo UID labels. If kube-state-metrics is already running in the cluster, disable the bundled instance when installing the Helm chart:

```bash
--set kubeStateMetrics.enabled=false
```

When using your own instance, ensure the OpenChoreo pod labels are allowlisted:

```bash
--set 'metricLabelsAllowlist={pods=[openchoreo.dev/component-uid\,openchoreo.dev/environment-uid\,openchoreo.dev/project-uid]}'
```

## IAM permissions

The CloudWatch metrics adapter needs permissions for three paths:

1. Startup identity check.
2. CloudWatch metric queries.
3. CloudWatch alarm management.

The OpenTelemetry collector needs permission to write EMF records to CloudWatch Logs.

Use these policies based on the credential model:

- **EKS Pod Identity or IRSA:** keep the adapter and OpenTelemetry collector policies separate and attach them to separate roles. This keeps each ServiceAccount least-privileged.
- **Static credentials:** use one IAM user and attach the [combined static-credentials IAM policy](#combined-static-credentials-iam-policy), because the same Kubernetes Secret is shared by the adapter and OpenTelemetry collector.

### Adapter IAM policy

Create the following custom IAM policy and attach it to the adapter IAM principal.

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
      "Sid": "RuntimeTopologyStartQuery",
      "Effect": "Allow",
      "Action": "logs:StartQuery",
      "Resource": "arn:aws:logs:<region>:<account-id>:log-group:/aws/openchoreo/metrics:*"
    },
    {
      "Sid": "RuntimeTopologyQueryResults",
      "Effect": "Allow",
      "Action": [
        "logs:GetQueryResults",
        "logs:StopQuery"
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

### OpenTelemetry collector and retention IAM policy

Create the following custom IAM policy and attach it to the OpenTelemetry collector IAM principal when using separate EKS identities. If `metrics.retention.enabled=true`, also attach this policy to the retention Job IAM principal configured through `metrics.retention.serviceAccount.annotations`.
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
      "Resource": "arn:aws:logs:<region>:<account-id>:log-group:/aws/openchoreo/metrics:*"
    }
  ]
}
```

### Combined static-credentials IAM policy

Use this policy for non-EKS clusters where one IAM user backs the shared static AWS credentials Secret.

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
      "Sid": "RuntimeTopologyStartQuery",
      "Effect": "Allow",
      "Action": "logs:StartQuery",
      "Resource": "arn:aws:logs:<region>:<account-id>:log-group:/aws/openchoreo/metrics:*"
    },
    {
      "Sid": "RuntimeTopologyQueryResults",
      "Effect": "Allow",
      "Action": [
        "logs:GetQueryResults",
        "logs:StopQuery"
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
      "Resource": "arn:aws:logs:<region>:<account-id>:log-group:/aws/openchoreo/metrics:*"
    }
  ]
}
```

## Installation on EKS with Pod Identity

This is the recommended installation path for EKS clusters.

### Step 1 — Export shared values

```bash
export AWS_REGION=<your-aws-region>
export NS=openchoreo-observability-plane
export WEBHOOK_SHARED_SECRET="$(openssl rand -base64 32)"
```

Make sure your `kubectl` context points to the target EKS cluster:

```bash
kubectl config current-context
```

Also verify that the EKS Pod Identity Agent add-on is installed:

```bash
kubectl -n kube-system get daemonset eks-pod-identity-agent
```

Pod Identity credentials are injected only when the Pod Identity Agent is running.

### Step 2 — Create IAM roles

Create an IAM role for the adapter, for example:

```text
OpenChoreoCloudWatchMetricsRoleForAdapter
```

Attach the custom [Adapter IAM policy](#adapter-iam-policy).

Create another IAM role for the OpenTelemetry collector, for example:

```text
OpenChoreoCloudWatchMetricsRoleForCollector
```

Attach the custom [OpenTelemetry collector and retention IAM policy](#opentelemetry-collector-and-retention-iam-policy).

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

To create the roles and attach policies using the AWS CLI:

```bash
export AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

POD_IDENTITY_TRUST_POLICY='{
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
}'

# Create the adapter role
aws iam create-role \
  --role-name OpenChoreoCloudWatchMetricsRoleForAdapter \
  --assume-role-policy-document "$POD_IDENTITY_TRUST_POLICY"

# Create the adapter policy
aws iam create-policy \
  --policy-name OpenChoreoCloudWatchMetricsAdapterPolicy \
  --policy-document "$(cat <<EOF
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
      "Sid": "RuntimeTopologyStartQuery",
      "Effect": "Allow",
      "Action": "logs:StartQuery",
      "Resource": "arn:aws:logs:${AWS_REGION}:${AWS_ACCOUNT_ID}:log-group:/aws/openchoreo/metrics:*"
    },
    {
      "Sid": "RuntimeTopologyQueryResults",
      "Effect": "Allow",
      "Action": [
        "logs:GetQueryResults",
        "logs:StopQuery"
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
EOF
)"

aws iam attach-role-policy \
  --role-name OpenChoreoCloudWatchMetricsRoleForAdapter \
  --policy-arn "arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OpenChoreoCloudWatchMetricsAdapterPolicy"

# Create the collector role
aws iam create-role \
  --role-name OpenChoreoCloudWatchMetricsRoleForCollector \
  --assume-role-policy-document "$POD_IDENTITY_TRUST_POLICY"

# Create the collector and retention policy
aws iam create-policy \
  --policy-name OpenChoreoCloudWatchMetricsCollectorPolicy \
  --policy-document "$(cat <<EOF
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
      "Resource": "arn:aws:logs:${AWS_REGION}:${AWS_ACCOUNT_ID}:log-group:/aws/openchoreo/metrics:*"
    }
  ]
}
EOF
)"

aws iam attach-role-policy \
  --role-name OpenChoreoCloudWatchMetricsRoleForCollector \
  --policy-arn "arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OpenChoreoCloudWatchMetricsCollectorPolicy"
```

### Step 3 — Install the module

Use the command that matches the cluster's topology. The following Helm commands will time out or the pods will enter CrashLoopBackOff since the Pod Identity associations are not created yet. Everything will work after Step 5 is completed.

#### Single-cluster install

Deploy the adapter, OpenTelemetry collector, kube-state-metrics, and retention Job in one cluster:

```bash
helm upgrade --install observability-metrics-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.3.0 \
  --set region="$AWS_REGION" \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

#### Observability plane install

Deploy only the adapter in the observability plane cluster:

```bash
helm upgrade --install observability-metrics-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.3.0 \
  --set region="$AWS_REGION" \
  --set opentelemetry-collector.enabled=false \
  --set kubeStateMetrics.enabled=false \
  --set metrics.retention.enabled=false \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

#### Data-plane install

Deploy only the OpenTelemetry collector, kube-state-metrics, and retention Job in each workload cluster:

```bash
helm upgrade --install observability-metrics-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.3.0 \
  --set region="$AWS_REGION" \
  --set adapter.enabled=false
```

### Step 4 — Create Pod Identity associations

EKS Pod Identity links a Kubernetes ServiceAccount to an IAM role. Each association is scoped to a single EKS cluster, namespace, and ServiceAccount. You must create these associations on every EKS cluster that participates in the install.

#### Single-cluster topology

Create three Pod Identity associations on the EKS cluster, all in the `$NS` namespace:

| ServiceAccount | Used by | IAM role to associate |
| --- | --- | --- |
| `metrics-adapter-aws-cloudwatch` | Adapter metric queries, alert CRUD, and webhook handling. | The role with the [Adapter IAM policy](#adapter-iam-policy) attached. |
| `metrics-opentelemetry-collector` | OpenTelemetry collector metric export to CloudWatch Logs. | The role with the [OpenTelemetry collector and retention IAM policy](#opentelemetry-collector-and-retention-iam-policy) attached. |
| `metrics-aws-cloudwatch-retention` | Helm post-install/post-upgrade Job that creates the EMF log group and applies `metrics.retentionDays`. | The role with the [OpenTelemetry collector and retention IAM policy](#opentelemetry-collector-and-retention-iam-policy) attached. |

#### Multi-cluster topology

In a multi-cluster setup, each EKS cluster only runs a subset of the components. Create Pod Identity associations only for the ServiceAccounts that exist in that cluster.

**Observability plane cluster** (runs only the adapter):

| ServiceAccount | IAM role to associate |
| --- | --- |
| `metrics-adapter-aws-cloudwatch` | The role with the [Adapter IAM policy](#adapter-iam-policy) attached. |

The `metrics-opentelemetry-collector` and `metrics-aws-cloudwatch-retention` ServiceAccounts do not exist in this cluster because `opentelemetry-collector.enabled=false` and `metrics.retention.enabled=false`.

**Each data-plane cluster** (runs the OpenTelemetry collector, kube-state-metrics, and retention Job):

| ServiceAccount | IAM role to associate |
| --- | --- |
| `metrics-opentelemetry-collector` | The role with the [OpenTelemetry collector and retention IAM policy](#opentelemetry-collector-and-retention-iam-policy) attached. |
| `metrics-aws-cloudwatch-retention` | The role with the [OpenTelemetry collector and retention IAM policy](#opentelemetry-collector-and-retention-iam-policy) attached. |

The `metrics-adapter-aws-cloudwatch` ServiceAccount does not exist in these clusters because `adapter.enabled=false`.

#### How to create a Pod Identity association

You can create associations from the AWS Console:

```text
EKS → Cluster → Access → Pod Identity associations → Create
```

For each association, fill in:

- **Namespace**: the namespace where the module is installed (for example, `openchoreo-observability-plane`).
- **Service Account**: the ServiceAccount name from the tables above.
- **IAM Role**: the ARN of the corresponding IAM role.

Alternatively, use the AWS CLI. Export the role ARNs from Step 2:

```bash
export ADAPTER_ROLE_ARN="arn:aws:iam::${AWS_ACCOUNT_ID}:role/OpenChoreoCloudWatchMetricsRoleForAdapter"
export COLLECTOR_ROLE_ARN="arn:aws:iam::${AWS_ACCOUNT_ID}:role/OpenChoreoCloudWatchMetricsRoleForCollector"
```

**Single-cluster topology** — create all three associations:

```bash
export EKS_CLUSTER_NAME=<your-eks-cluster-name>

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account metrics-adapter-aws-cloudwatch \
  --role-arn "$ADAPTER_ROLE_ARN"

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account metrics-opentelemetry-collector \
  --role-arn "$COLLECTOR_ROLE_ARN"

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account metrics-aws-cloudwatch-retention \
  --role-arn "$COLLECTOR_ROLE_ARN"
```

**Observability plane cluster** — adapter only:

```bash
export EKS_CLUSTER_NAME=<your-obs-plane-cluster-name>

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account metrics-adapter-aws-cloudwatch \
  --role-arn "$ADAPTER_ROLE_ARN"
```

**Each data-plane cluster** — OpenTelemetry collector and retention Job only. Repeat for each cluster:

```bash
export EKS_CLUSTER_NAME=<your-data-plane-cluster-name>

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account metrics-opentelemetry-collector \
  --role-arn "$COLLECTOR_ROLE_ARN"

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account metrics-aws-cloudwatch-retention \
  --role-arn "$COLLECTOR_ROLE_ARN"
```

### Step 5 — Restart workloads on each cluster

EKS Pod Identity injects credentials only at pod creation time.

So, you will see errors such as:

- `AccessDeniedException` from `assumed-role/<node-role>` in OpenTelemetry collector logs.
- `Unable to locate credentials` in the `metrics-aws-cloudwatch-retention` Job.

Recreate the workloads so new pods receive Pod Identity credentials. Run the commands that match your topology on each cluster.

**Single-cluster topology:**

```bash
kubectl -n "$NS" rollout restart daemonset/metrics-opentelemetry-collector-agent
kubectl -n "$NS" rollout restart deployment/metrics-adapter-aws-cloudwatch

# Re-trigger the retention Helm hook (it ran once at install time)
kubectl -n "$NS" delete job metrics-aws-cloudwatch-retention --ignore-not-found
helm upgrade observability-metrics-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-aws-cloudwatch \
  --namespace "$NS" --version 0.3.0 --reuse-values
```

**Observability plane cluster** — restart only the adapter:

```bash
kubectl -n "$NS" rollout restart deployment/metrics-adapter-aws-cloudwatch
```

**Each data-plane cluster** — restart the DaemonSet and re-trigger the retention Job:

```bash
kubectl -n "$NS" rollout restart daemonset/metrics-opentelemetry-collector-agent

kubectl -n "$NS" delete job metrics-aws-cloudwatch-retention --ignore-not-found
helm upgrade observability-metrics-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-aws-cloudwatch \
  --namespace "$NS" --version 0.3.0 --reuse-values
```

#### Verify Pod Identity injection

Verify that Pod Identity was injected by checking a pod that runs in your topology.

On clusters that run the **adapter** (single-cluster or observability plane):

```bash
kubectl -n "$NS" get pod -l app=metrics-adapter-aws-cloudwatch -o name | head -1 \
  | xargs -I {} kubectl -n "$NS" get {} -o yaml \
  | grep -E "AWS_CONTAINER|eks-pod-identity-token"
```

On clusters that run the **OpenTelemetry collector** (single-cluster or data-plane):

```bash
kubectl -n "$NS" get pod -l app.kubernetes.io/name=opentelemetry-collector -o name | head -1 \
  | xargs -I {} kubectl -n "$NS" get {} -o yaml \
  | grep -E "AWS_CONTAINER|eks-pod-identity-token"
```

If these values are missing, check that the namespace and ServiceAccount names in the Pod Identity associations exactly match the table above.

## Installation on non-EKS clusters with static credentials

Use this path for:

- k3d
- kind
- Kubernetes clusters outside AWS
- Kubernetes clusters where Pod Identity or IRSA is not available

In this mode, the chart creates a Kubernetes Secret containing AWS credentials. The adapter reads this Secret automatically. The OpenTelemetry collector must also be pointed at the same Secret through `opentelemetry-collector.extraEnvsFrom`.

### Step 1 — Export shared values

```bash
export AWS_REGION=<your-aws-region>
export NS=openchoreo-observability-plane
export WEBHOOK_SHARED_SECRET="$(openssl rand -base64 32)"
export AWS_ACCESS_KEY_ID=<your-access-key-id>
export AWS_SECRET_ACCESS_KEY=<your-secret-access-key>
```

### Step 2 — Create an IAM user

Create an IAM user and attach the custom [combined static-credentials IAM policy](#combined-static-credentials-iam-policy).

Create access keys for this IAM user and export them as shown above.

To create the IAM user and attach policies using the AWS CLI:

```bash
export AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

# Create the IAM user
aws iam create-user --user-name OpenChoreoCloudWatchMetricsUser

# Create the combined static-credentials policy
aws iam create-policy \
  --policy-name OpenChoreoCloudWatchMetricsCombinedPolicy \
  --policy-document "$(cat <<EOF
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
      "Sid": "RuntimeTopologyStartQuery",
      "Effect": "Allow",
      "Action": "logs:StartQuery",
      "Resource": "arn:aws:logs:${AWS_REGION}:${AWS_ACCOUNT_ID}:log-group:/aws/openchoreo/metrics:*"
    },
    {
      "Sid": "RuntimeTopologyQueryResults",
      "Effect": "Allow",
      "Action": [
        "logs:GetQueryResults",
        "logs:StopQuery"
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
      "Resource": "arn:aws:logs:${AWS_REGION}:${AWS_ACCOUNT_ID}:log-group:/aws/openchoreo/metrics:*"
    }
  ]
}
EOF
)"

aws iam attach-user-policy \
  --user-name OpenChoreoCloudWatchMetricsUser \
  --policy-arn "arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OpenChoreoCloudWatchMetricsCombinedPolicy"

# Create access keys
ACCESS_KEY_OUTPUT=$(aws iam create-access-key --user-name OpenChoreoCloudWatchMetricsUser)
export AWS_ACCESS_KEY_ID=$(echo "$ACCESS_KEY_OUTPUT" | jq -r '.AccessKey.AccessKeyId')
export AWS_SECRET_ACCESS_KEY=$(echo "$ACCESS_KEY_OUTPUT" | jq -r '.AccessKey.SecretAccessKey')
```

### Step 3 — Install the module

Use the command that matches the cluster's topology.

#### Single-cluster install

Deploy the adapter, OpenTelemetry collector, kube-state-metrics, and retention Job in one cluster:

```bash
helm upgrade --install observability-metrics-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.3.0 \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=metrics-aws-cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set "opentelemetry-collector.extraEnvsFrom[0].configMapRef.name=metrics-aws-cloudwatch-cluster-env" \
  --set "opentelemetry-collector.extraEnvsFrom[1].secretRef.name=metrics-aws-cloudwatch-aws-credentials" \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

This enables the static-credentials path:

- The chart creates a Kubernetes Secret.
- The adapter reads credentials from that Secret.
- The OpenTelemetry collector reads credentials from the same Secret through `opentelemetry-collector.extraEnvsFrom`.

You do not need to restart workloads after installation because credentials are injected during install.

#### Observability plane install

Deploy only the adapter in the observability plane cluster:

```bash
helm upgrade --install observability-metrics-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.3.0 \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=metrics-aws-cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set opentelemetry-collector.enabled=false \
  --set kubeStateMetrics.enabled=false \
  --set metrics.retention.enabled=false \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

#### Data-plane install

Deploy only the OpenTelemetry collector, kube-state-metrics, and retention Job in each workload cluster:

```bash
helm upgrade --install observability-metrics-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.3.0 \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=metrics-aws-cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set "opentelemetry-collector.extraEnvsFrom[0].configMapRef.name=metrics-aws-cloudwatch-cluster-env" \
  --set "opentelemetry-collector.extraEnvsFrom[1].secretRef.name=metrics-aws-cloudwatch-aws-credentials" \
  --set adapter.enabled=false
```

In an observability-plane-only install, the collector is disabled, so the created Secret is used only by the adapter.

## Metric alerting

Metric alerting is enabled by default when the adapter is installed. The chart injects `OBSERVER_URL` into the adapter so forwarded CloudWatch alarm events can be sent to the Observer.

Webhook exposure remains opt-in. To receive CloudWatch alarm state-change events from outside the cluster, expose the webhook through EventBridge and configure webhook authentication as described below.

If `adapter.alerting.webhookAuth.enabled=true` and `adapter.alerting.webhookAuth.sharedSecret` were set during installation, the adapter now requires the following header on alert webhook calls:

```text
X-OpenChoreo-Webhook-Token
```

### Alerting behavior

The module implements metric alerts using native CloudWatch resources:

1. A CloudWatch metric math alarm evaluating `(usage / limit) * 100` against the threshold percentage.
2. An EventBridge rule that forwards CloudWatch alarm state changes to the adapter webhook.

Important constraints:

- Threshold is a percentage (0-100) of the pod's CPU or memory limit.
- When the pod has no limit, the limit series is missing, the math expression returns no data, and the alarm sits in `INSUFFICIENT_DATA` (with `TreatMissingData=notBreaching` it does not fire).

### Alert identity mapping

The adapter stores the logical OpenChoreo rule identity in CloudWatch alarm tags:

- `openchoreo.rule.name`
- `openchoreo.rule.namespace`

The adapter also encodes the rule identity into the alarm name for fast lookup.

Managed alarm names use this format:

```text
oc-metrics-alert-ns.<namespace>.rn.<name>.<hash>
```

`<namespace>` and `<name>` are base64url-encoded without padding. `<hash>` is the first 12 hex characters of `sha256(namespace + "\x00" + name)`.

### Test alerting

For an end-to-end OpenChoreo alert and incident flow, see the [Component Alerts and Incidents tutorial](https://openchoreo.dev/docs/tutorials/component-alerts-and-incidents/).

## Expose the alert webhook through EventBridge

CloudWatch alarms cannot directly send HTTP requests to the adapter. To deliver alarm events, create an EventBridge rule that matches CloudWatch alarm state changes and routes them to an API destination that points at the adapter's webhook endpoint.

The end-to-end flow:

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

### Expose only the webhook endpoint

For production, expose only the alert webhook endpoint through your ingress:

```text
/api/v1alpha1/alerts/webhook
```

Do **not** publicly expose:

- `/api/v1/metrics/query`
- `/api/v1alpha1/alerts/rules/*`
- `/healthz`
- `/livez`

Use `adapter.alerting.webhookRoute` to create a Gateway API `HTTPRoute` that exposes only the webhook path through your existing Gateway. TLS termination, rate limiting, and any WAF / auth rules belong on the parent Gateway listener.

### Step 1 — Create an EventBridge connection

The connection stores the authentication credentials that EventBridge uses when calling the adapter webhook.

1. Open the Amazon EventBridge console and navigate to **Integration** → **Connections**.
2. Choose **Create connection**.
3. Enter a connection name, for example `openchoreo-metrics-webhook-connection`.
4. For **Authorization type**, select **API Key**.
5. Set **API key name** to `X-OpenChoreo-Webhook-Token`.
6. Set **Value** to the same shared secret you configured during Helm installation (the value of `adapter.alerting.webhookAuth.sharedSecret` or the contents of the Kubernetes Secret referenced by `adapter.alerting.webhookAuth.sharedSecretRef`).
7. Choose **Create**.

Alternatively, use the AWS CLI:

```bash
aws events create-connection \
  --name openchoreo-metrics-webhook-connection \
  --authorization-type API_KEY \
  --auth-parameters '{"ApiKeyAuthParameters":{"ApiKeyName":"X-OpenChoreo-Webhook-Token","ApiKeyValue":"'"$WEBHOOK_SHARED_SECRET"'"}}'
```

### Step 2 — Create an EventBridge API destination

The API destination defines the HTTP endpoint that EventBridge calls when a matching event arrives.

1. In the EventBridge console, navigate to **Integration** → **API destinations**.
2. Choose **Create API destination**.
3. Enter a name, for example `openchoreo-metrics-webhook`.
4. For **API destination endpoint**, enter the publicly reachable URL of the adapter webhook. This is the external URL exposed through your Gateway or ingress, ending with `/api/v1alpha1/alerts/webhook`. For example: `https://metrics-webhook.example.com/api/v1alpha1/alerts/webhook`.
5. For **HTTP method**, select **POST**.
6. For **Connection**, select the connection created in Step 1 (`openchoreo-metrics-webhook-connection`).
7. Optionally set an **Invocation rate limit** to protect the adapter from bursts. A value of 10 per second is a reasonable starting point.
8. Choose **Create**.

Alternatively, use the AWS CLI:

```bash
export CONNECTION_ARN=$(aws events describe-connection \
  --name openchoreo-metrics-webhook-connection \
  --query ConnectionArn --output text)

aws events create-api-destination \
  --name openchoreo-metrics-webhook \
  --connection-arn "$CONNECTION_ARN" \
  --invocation-endpoint "<your-webhook-url>/api/v1alpha1/alerts/webhook" \
  --http-method POST \
  --invocation-rate-limit-per-second 10
```

Replace `<your-webhook-url>` with the publicly reachable base URL of the adapter exposed through your Gateway or ingress.

### Step 3 — Create an EventBridge rule

The rule matches CloudWatch alarm state-change events and routes them to the API destination.

1. In the EventBridge console, navigate to **Buses** → **Rules** on the **default** event bus.
2. Choose **Create rule**.
3. Enter a rule name, for example `openchoreo-metrics-alarm-to-webhook`.
4. For **Event bus**, keep **default**.
5. For **Rule type**, select **Rule with an event pattern**.
6. Choose **Next**.
7. For **Event source**, select **AWS events or EventBridge partner events**.
8. Under **Event pattern**, choose **Custom patterns (JSON editor)** and paste the following pattern:

```json
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

This pattern matches only `ALARM` state transitions for alarms whose name starts with `oc-metrics-alert`. The prefix filter ensures that log-module alarms (which use the `oc-logs-alert` prefix) are not routed to the metrics adapter.

9. Choose **Next**.
10. For **Target**, select **EventBridge API destination**.
11. Select the API destination created in Step 2 (`openchoreo-metrics-webhook`).
12. Leave the input transformer at the default (full event) unless you have a specific reason to modify it.
13. Choose **Next**, review the rule, and choose **Create rule**.

Alternatively, use the AWS CLI:

```bash
# Create the EventBridge rule
aws events put-rule \
  --name openchoreo-metrics-alarm-to-webhook \
  --event-bus-name default \
  --event-pattern '{
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
  }'

# Create an IAM role that allows EventBridge to invoke the API destination
aws iam create-role \
  --role-name OpenChoreoMetricsEventBridgeInvokeRole \
  --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [
      {
        "Effect": "Allow",
        "Principal": {
          "Service": "events.amazonaws.com"
        },
        "Action": "sts:AssumeRole"
      }
    ]
  }'

export API_DEST_ARN=$(aws events describe-api-destination \
  --name openchoreo-metrics-webhook \
  --query ApiDestinationArn --output text)

aws iam create-policy \
  --policy-name OpenChoreoMetricsEventBridgeInvokePolicy \
  --policy-document "$(cat <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "events:InvokeApiDestination",
      "Resource": "$API_DEST_ARN"
    }
  ]
}
EOF
)"

aws iam attach-role-policy \
  --role-name OpenChoreoMetricsEventBridgeInvokeRole \
  --policy-arn "arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OpenChoreoMetricsEventBridgeInvokePolicy"

export EVENTBRIDGE_ROLE_ARN=$(aws iam get-role \
  --role-name OpenChoreoMetricsEventBridgeInvokeRole \
  --query Role.Arn --output text)

# Add the API destination as a target for the rule
aws events put-targets \
  --rule openchoreo-metrics-alarm-to-webhook \
  --event-bus-name default \
  --targets '[{
    "Id": "openchoreo-metrics-webhook",
    "Arn": "'"$API_DEST_ARN"'",
    "RoleArn": "'"$EVENTBRIDGE_ROLE_ARN"'"
  }]'
```

### Test alert delivery

Follow the Component Alerts and Incidents tutorial to trigger a metric alert:

```text
https://openchoreo.dev/docs/tutorials/component-alerts-and-incidents/
```

Then check the adapter logs:

```bash
kubectl -n "$NS" logs deployment/metrics-adapter-aws-cloudwatch --tail=100 | grep -Ei 'webhook|forward'
```

Expected log messages should show that the webhook was received and forwarded to the Observer.

## Shared webhook secret

When webhook authentication is enabled, the adapter rejects webhook requests that do not include the configured token in this header:

```text
X-OpenChoreo-Webhook-Token
```

The same token must be configured in the EventBridge connection created in [Step 1](#step-1--create-an-eventbridge-connection) as the API Key value.

## Troubleshooting

### Start with these logs

```bash
kubectl -n "$NS" logs daemonset/metrics-opentelemetry-collector-agent --tail=200
kubectl -n "$NS" logs deployment/metrics-adapter-aws-cloudwatch --tail=200
kubectl -n "$NS" logs -l job-name=metrics-aws-cloudwatch-retention --tail=200
```

### Common issues

| Symptom | Likely cause | What to check |
| --- | --- | --- |
| Adapter pod does not start | Missing or invalid AWS credentials | Check Pod Identity association or static Secret values. |
| OpenTelemetry collector shows `AccessDeniedException` | Pod is using the node IAM role instead of Pod Identity role | Restart the collector after creating Pod Identity associations. |
| Retention Job shows `Unable to locate credentials` | Pod Identity association missing or static Secret not configured | Check the `metrics-aws-cloudwatch-retention` ServiceAccount association or static credentials values. |
| Query returns empty series | Metrics not published to CloudWatch, or dimensions are missing | Check OpenTelemetry collector logs and verify that OpenChoreo pod labels are present. |
| Webhook returns unauthorized | Missing or incorrect `X-OpenChoreo-Webhook-Token` | Check EventBridge connection header and chart webhook secret values. |
| Alerts do not fire for pods without limits | CloudWatch math expression returns no data when limit is missing | This is expected; the alarm stays in `INSUFFICIENT_DATA` with `TreatMissingData=notBreaching`. |
| Retention Job fails after creating Pod Identity associations | Pod Identity association was created after the first Helm install | Delete the failed Job and re-run `helm upgrade` after confirming the association is attached. |

### Rerun a failed retention Job

If the retention Job failed because its Pod Identity association was created after the first Helm install, rerun it manually. `helm upgrade` re-fires the post-upgrade hook, but the failed Job has a fixed name and a new one cannot be created until it is deleted.

Confirm the Pod Identity association for `metrics-aws-cloudwatch-retention` is attached before rerunning the upgrade. Otherwise the new Job pod will fail with the same credential error and the upgrade can fail again with `BackoffLimitExceeded`.

```bash
# 1. Delete the failed Job so a new one can be created with the same name.
kubectl -n "$NS" delete job metrics-aws-cloudwatch-retention --ignore-not-found

# 2. Re-fire the post-upgrade hook.
helm upgrade observability-metrics-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-aws-cloudwatch \
  --version 0.3.0 --namespace "$NS" --reuse-values

# 3. Watch the new Job complete.
kubectl -n "$NS" get job metrics-aws-cloudwatch-retention -w
```

If the new Job pod fails again, inspect its logs:

```bash
kubectl -n "$NS" logs -l job-name=metrics-aws-cloudwatch-retention --tail=100
```

## Configuration reference

| Value | Default | Description |
| --- | --- | --- |
| `region` | Required | AWS region for CloudWatch Logs, CloudWatch Metrics, and API calls. |
| `awsCredentials.create` | `false` | Creates a static AWS credentials Secret. Keep `false` for Pod Identity, IRSA, or instance-profile based auth. Set to `true` for k3d, kind, or non-EKS clusters. |
| `awsCredentials.name` | `""` | Name of the AWS credentials Secret. Required when `awsCredentials.create=true`. |
| `awsCredentials.accessKeyId` | Required if `create=true` | AWS access key ID. |
| `awsCredentials.secretAccessKey` | Required if `create=true` | AWS secret access key. |
| `kubeStateMetrics.enabled` | `true` | Installs the bundled kube-state-metrics subchart with the required OpenChoreo label allowlist. Set to `false` when kube-state-metrics is already running in the cluster or on an observability-plane-only install. |
| `metrics.namespace` | `OpenChoreo/Metrics` | CloudWatch metric namespace used by the adapter. Keep this consistent across clusters in one multi-cluster installation. |
| `metrics.logGroup` | `""` | CloudWatch Logs log group used by the OpenTelemetry EMF exporter. Empty defaults to `/aws/openchoreo/metrics`. |
| `metrics.retentionDays` | `7` | CloudWatch Logs retention period for the EMF log group. Must be one of the retention values supported by CloudWatch Logs. |
| `metrics.retention.enabled` | `true` | Runs a Helm post-install/post-upgrade Job that creates the EMF log group if needed and applies `metrics.retentionDays`. Set to `false` on an observability-plane-only install. |
| `metrics.retention.serviceAccount.create` | `true` | Creates a ServiceAccount for the retention Job. |
| `metrics.retention.serviceAccount.name` | `""` | ServiceAccount name for the retention Job. Defaults to `metrics-aws-cloudwatch-retention` when `create=true`. Required when `create=false`. |
| `metrics.retention.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations used by the retention Job. |
| `metrics.retention.image.repository` | `public.ecr.aws/aws-cli/aws-cli` | AWS CLI image used by the retention Job. |
| `metrics.retention.image.tag` | `2.34.44` | AWS CLI image tag used by the retention Job. |
| `opentelemetry-collector.fullnameOverride` | `metrics-opentelemetry-collector` | Overrides the subchart resource name prefix. |
| `opentelemetry-collector.enabled` | `true` | Enables the OpenTelemetry collector subchart. Set to `false` on an observability-plane-only install. |
| `opentelemetry-collector.mode` | `daemonset` | Runs one collector per node. |
| `opentelemetry-collector.image.repository` | `otel/opentelemetry-collector-contrib` | Collector image repository. The contrib image includes the receivers and exporters required by this module. |
| `opentelemetry-collector.image.tag` | `0.149.0` | Collector image tag. |
| `opentelemetry-collector.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `opentelemetry-collector.extraEnvsFrom` | `[{configMapRef: {name: metrics-aws-cloudwatch-cluster-env}}]` | Extra `envFrom` entries for the OpenTelemetry collector. The default ConfigMap supplies `AWS_REGION` and `EMF_LOG_GROUP_NAME`; append the static AWS credentials Secret at index `1` on non-EKS clusters. |
| `adapter.enabled` | `true` | Deploys the CloudWatch Metrics Adapter Deployment and Service. Set to `false` on data-plane installs. |
| `adapter.replicas` | `1` | Number of adapter replicas. |
| `adapter.image.repository` | `ghcr.io/openchoreo/observability-metrics-aws-cloudwatch-adapter` | Adapter image repository. |
| `adapter.image.tag` | `""` | Adapter image tag. Empty defaults to chart `appVersion`. |
| `adapter.service.port` | `9099` | Adapter HTTP port. |
| `adapter.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `adapter.logLevel` | `INFO` | Adapter log level. Supported values include `DEBUG`, `INFO`, `WARN`, and `ERROR`. |
| `adapter.queryTimeoutSeconds` | `30` | CloudWatch Logs Insights query timeout used by runtime-topology queries. |
| `adapter.alerting.enabled` | `true` | Enables alert rule CRUD and webhook forwarding configuration. |
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
| `adapter.networkPolicy.gatewayPodLabels` | `{}` | Pod labels of the Gateway data-plane pods allowed to proxy the webhook. When set, restricts gateway ingress to matching pods instead of the entire namespace. |
| `adapter.networkPolicy.allowProbeIPBlock` | `""` | Optional node CIDR for kubelet probes when required by the CNI. |

If you override `metrics.logGroup`, update the CloudWatch Logs IAM policy resource ARN to match that log group.

## Dependencies

Bundled upstream Helm charts:

| Chart | Repository |
| ----- | ---------- |
| opentelemetry-collector | https://open-telemetry.github.io/opentelemetry-helm-charts |
| kube-state-metrics | https://prometheus-community.github.io/helm-charts |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
|----------------|--------------------|
| v0.2.x         | v1.1.x             |
