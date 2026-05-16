# Observability Metrics Module for AWS CloudWatch

|               |           |
| ------------- | --------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_metrics_cloudwatch)](https://codecov.io/gh/openchoreo/community-modules) |

The **Observability Metrics Module for AWS CloudWatch** sends OpenChoreo resource metrics to **AWS CloudWatch Metrics** and exposes them back to the OpenChoreo Observer through the standard **OpenChoreo Metrics Adapter API**. It also supports metric-based alerting by translating OpenChoreo alert rules into **CloudWatch metric math alarms** that evaluate `(usage / limit) * 100` against a percentage threshold, so an OpenChoreo threshold of `80` for `cpu_usage` fires when the pod uses more than 80% of its CPU limit.

This module supports both:

- **EKS clusters** using **EKS Pod Identity** — recommended for production.
- **Non-EKS Kubernetes clusters** such as **k3d**, **kind**, or Kubernetes running outside AWS, using static AWS credentials.

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

1. An **OpenTelemetry collector** DaemonSet that scrapes pod CPU and memory usage from kubelet, scrapes pod requests and limits from `kube-state-metrics`, enriches series with OpenChoreo pod labels, and publishes metrics to CloudWatch through the AWS EMF exporter.
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
| `POST /api/v1/metrics/query` | Runs a CloudWatch `GetMetricData` request for resource metrics. |
| `POST /api/v1alpha1/alerts/rules` | Creates a CloudWatch metric math alarm evaluating `(usage / limit) * 100` against the threshold percentage. |
| `GET /api/v1alpha1/alerts/rules/{ruleName}` | Gets the alert rule identified by `{ruleName}`. |
| `PUT /api/v1alpha1/alerts/rules/{ruleName}` | Updates the alert rule identified by `{ruleName}`. |
| `DELETE /api/v1alpha1/alerts/rules/{ruleName}` | Deletes the CloudWatch metric math alarm for the alert rule identified by `{ruleName}`. |
| `POST /api/v1alpha1/alerts/webhook` | Receives forwarded CloudWatch alarm events from EventBridge and forwards them to the Observer. |
| `GET /healthz` | Readiness check. Returns `200` once the adapter is ready. |
| `GET /livez` | Liveness check. Does not call AWS, so transient AWS or DNS issues do not crash-loop the pod. |

## Choose a deployment topology

Choose the deployment topology first, then choose the AWS authentication model for each cluster.

| Topology | Install location | Purpose | Required Helm values |
| --- | --- | --- | --- |
| Single cluster | The OpenChoreo cluster where the observability plane and workloads run together. | Deploys the adapter, OpenTelemetry collector, kube-state-metrics, and retention Job. | Defaults. |
| Observability plane cluster | The cluster where the OpenChoreo observability plane is installed. | Deploys only the CloudWatch Metrics Adapter. | `adotcollector.enabled=false`, `kubeStateMetrics.enabled=false`, `metrics.retention.enabled=false` |
| Data-plane / workflow-plane cluster | Each cluster that runs OpenChoreo workloads. | Deploys only metric ingestion components: OpenTelemetry collector, kube-state-metrics, and retention Job. | `adapter.enabled=false` |

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
OpenChoreoCloudWatchMetricsRoleForAdot
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

### Step 3 — Install the module

Use the command that matches the cluster's topology.

#### Single-cluster install

Deploy the adapter, OpenTelemetry collector, kube-state-metrics, and retention Job in one cluster:

```bash
helm upgrade --install observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

#### Observability plane install

Deploy only the adapter in the observability plane cluster:

```bash
helm upgrade --install observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set adotcollector.enabled=false \
  --set kubeStateMetrics.enabled=false \
  --set metrics.retention.enabled=false \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

#### Data-plane / workflow-plane install

Deploy only the OpenTelemetry collector, kube-state-metrics, and retention Job in each workload cluster:

```bash
helm upgrade --install observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set adapter.enabled=false
```

### Step 4 — Create Pod Identity associations

EKS Pod Identity links a Kubernetes ServiceAccount to an IAM role. Each association is scoped to a single EKS cluster, namespace, and ServiceAccount. You must create these associations on every EKS cluster that participates in the install.

#### Single-cluster topology

Create three Pod Identity associations on the EKS cluster, all in the `$NS` namespace:

| ServiceAccount | Used by | IAM role to associate |
| --- | --- | --- |
| `metrics-adapter-cloudwatch` | Adapter metric queries, alert CRUD, and webhook handling. | The role with the [Adapter IAM policy](#adapter-iam-policy) attached. |
| `observability-metrics-cloudwatch-adotcollector` | OpenTelemetry collector metric export to CloudWatch Logs. | The role with the [OpenTelemetry collector and retention IAM policy](#opentelemetry-collector-and-retention-iam-policy) attached. |
| `metrics-cloudwatch-retention` | Helm post-install/post-upgrade Job that creates the EMF log group and applies `metrics.retentionDays`. | The role with the [OpenTelemetry collector and retention IAM policy](#opentelemetry-collector-and-retention-iam-policy) attached. |

#### Multi-cluster topology

In a multi-cluster setup, each EKS cluster only runs a subset of the components. Create Pod Identity associations only for the ServiceAccounts that exist in that cluster.

**Observability plane cluster** (runs only the adapter):

| ServiceAccount | IAM role to associate |
| --- | --- |
| `metrics-adapter-cloudwatch` | The role with the [Adapter IAM policy](#adapter-iam-policy) attached. |

The `observability-metrics-cloudwatch-adotcollector` and `metrics-cloudwatch-retention` ServiceAccounts do not exist in this cluster because `adotcollector.enabled=false` and `metrics.retention.enabled=false`.

**Each data-plane / workflow-plane cluster** (runs the OpenTelemetry collector, kube-state-metrics, and retention Job):

| ServiceAccount | IAM role to associate |
| --- | --- |
| `observability-metrics-cloudwatch-adotcollector` | The role with the [OpenTelemetry collector and retention IAM policy](#opentelemetry-collector-and-retention-iam-policy) attached. |
| `metrics-cloudwatch-retention` | The role with the [OpenTelemetry collector and retention IAM policy](#opentelemetry-collector-and-retention-iam-policy) attached. |

The `metrics-adapter-cloudwatch` ServiceAccount does not exist in these clusters because `adapter.enabled=false`.

#### How to create a Pod Identity association

You can create associations from the AWS Console:

```text
EKS → Cluster → Access → Pod Identity associations → Create
```

For each association, fill in:

- **Namespace**: the namespace where the module is installed (for example, `openchoreo-observability-plane`).
- **Service Account**: the ServiceAccount name from the tables above.
- **IAM Role**: the ARN of the corresponding IAM role.

Alternatively, use the AWS CLI:

```bash
aws eks create-pod-identity-association \
  --cluster-name <your-eks-cluster-name> \
  --namespace "$NS" \
  --service-account <service-account-name> \
  --role-arn <iam-role-arn>
```

Repeat this command for each ServiceAccount that needs an association on the given cluster. For multi-cluster installs, run the appropriate commands against each EKS cluster.

### Step 5 — Restart workloads

EKS Pod Identity injects credentials only at pod creation time.

So, you will see errors such as:

- `AccessDeniedException` from `assumed-role/<node-role>` in OpenTelemetry collector logs.
- `Unable to locate credentials` in the `metrics-cloudwatch-retention` Job.

Recreate the workloads so new pods receive Pod Identity credentials.
Only workloads present in your topology need restarting:

```bash
# Restart the adapter if it exists (observability-plane installs)
kubectl -n "$NS" get deployment metrics-adapter-cloudwatch &>/dev/null \
  && kubectl -n "$NS" rollout restart deployment/metrics-adapter-cloudwatch

# Restart the collector DaemonSet if it exists (data-plane installs)
kubectl -n "$NS" get daemonset observability-metrics-cloudwatch-adotcollector-agent &>/dev/null \
  && kubectl -n "$NS" rollout restart daemonset/observability-metrics-cloudwatch-adotcollector-agent
```

If the OpenTelemetry collector DaemonSet name differs because of your Helm release name, inspect it first:

```bash
kubectl -n "$NS" get daemonset
```

Re-trigger the retention Helm hook (it ran once at install time):

```bash
kubectl -n "$NS" delete job metrics-cloudwatch-retention --ignore-not-found
helm upgrade observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --namespace "$NS" --version 0.2.0 --reuse-values
```

Verify that Pod Identity was injected into a new adapter pod:

```bash
kubectl -n "$NS" get pod -l app=metrics-adapter-cloudwatch -o name | head -1 \
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

In this mode, the chart creates a Kubernetes Secret containing AWS credentials. The adapter reads this Secret automatically. The OpenTelemetry collector must also be pointed at the same Secret through `adotcollector.extraEnvsFrom`.

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

### Step 3 — Install the module

Use the command that matches the cluster's topology.

#### Single-cluster install

Deploy the adapter, OpenTelemetry collector, kube-state-metrics, and retention Job in one cluster:

```bash
helm upgrade --install observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
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
- The OpenTelemetry collector reads credentials from the same Secret through `adotcollector.extraEnvsFrom`.

You do not need to restart workloads after installation because credentials are injected during install.

#### Observability plane install

Deploy only the adapter in the observability plane cluster:

```bash
helm upgrade --install observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=metrics-cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set adotcollector.enabled=false \
  --set kubeStateMetrics.enabled=false \
  --set metrics.retention.enabled=false \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

#### Data-plane / workflow-plane install

Deploy only the OpenTelemetry collector, kube-state-metrics, and retention Job in each workload cluster:

```bash
helm upgrade --install observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=metrics-cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set "adotcollector.extraEnvsFrom[0].configMapRef.name=metrics-cloudwatch-cluster-env" \
  --set "adotcollector.extraEnvsFrom[1].secretRef.name=metrics-cloudwatch-aws-credentials" \
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

### Test alert delivery

Follow the Component Alerts and Incidents tutorial to trigger a metric alert:

```text
https://openchoreo.dev/docs/tutorials/component-alerts-and-incidents/
```

Then check the adapter logs:

```bash
kubectl -n "$NS" logs deployment/metrics-adapter-cloudwatch --tail=100 | grep -Ei 'webhook|forward'
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
kubectl -n "$NS" logs daemonset/observability-metrics-cloudwatch-adotcollector-agent --tail=200
kubectl -n "$NS" logs deployment/metrics-adapter-cloudwatch --tail=200
kubectl -n "$NS" logs -l job-name=metrics-cloudwatch-retention --tail=200
```

### Common issues

| Symptom | Likely cause | What to check |
| --- | --- | --- |
| Adapter pod does not start | Missing or invalid AWS credentials | Check Pod Identity association or static Secret values. |
| OpenTelemetry collector shows `AccessDeniedException` | Pod is using the node IAM role instead of Pod Identity role | Restart the collector after creating Pod Identity associations. |
| Retention Job shows `Unable to locate credentials` | Pod Identity association missing or static Secret not configured | Check the `metrics-cloudwatch-retention` ServiceAccount association or static credentials values. |
| Query returns empty series | Metrics not published to CloudWatch, or dimensions are missing | Check OpenTelemetry collector logs and verify that OpenChoreo pod labels are present. |
| Webhook returns unauthorized | Missing or incorrect `X-OpenChoreo-Webhook-Token` | Check EventBridge connection header and chart webhook secret values. |
| Alerts do not fire for pods without limits | CloudWatch math expression returns no data when limit is missing | This is expected; the alarm stays in `INSUFFICIENT_DATA` with `TreatMissingData=notBreaching`. |
| Retention Job fails after creating Pod Identity associations | Pod Identity association was created after the first Helm install | Delete the failed Job and re-run `helm upgrade` after confirming the association is attached. |

### Rerun a failed retention Job

If the retention Job failed because its Pod Identity association was created after the first Helm install, rerun it manually. `helm upgrade` re-fires the post-upgrade hook, but the failed Job has a fixed name and a new one cannot be created until it is deleted.

Confirm the Pod Identity association for `metrics-cloudwatch-retention` is attached before rerunning the upgrade. Otherwise the new Job pod will fail with the same credential error and the upgrade can fail again with `BackoffLimitExceeded`.

```bash
# 1. Delete the failed Job so a new one can be created with the same name.
kubectl -n "$NS" delete job metrics-cloudwatch-retention --ignore-not-found

# 2. Re-fire the post-upgrade hook.
helm upgrade observability-metrics-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-cloudwatch \
  --version 0.2.0 --namespace "$NS" --reuse-values

# 3. Watch the new Job complete.
kubectl -n "$NS" get job metrics-cloudwatch-retention -w
```

If the new Job pod fails again, inspect its logs:

```bash
kubectl -n "$NS" logs -l job-name=metrics-cloudwatch-retention --tail=100
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
| `metrics.retention.serviceAccount.name` | `""` | ServiceAccount name for the retention Job. Defaults to `metrics-cloudwatch-retention` when `create=true`. Required when `create=false`. |
| `metrics.retention.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations used by the retention Job. |
| `metrics.retention.image.repository` | `public.ecr.aws/aws-cli/aws-cli` | AWS CLI image used by the retention Job. |
| `metrics.retention.image.tag` | `2.34.44` | AWS CLI image tag used by the retention Job. |
| `adotcollector.enabled` | `true` | Enables the ADOT subchart. Set to `false` on an observability-plane-only install. |
| `adotcollector.mode` | `daemonset` | Runs one collector per node. |
| `adotcollector.image.repository` | `otel/opentelemetry-collector-contrib` | Collector image repository. The contrib image includes kubeletstats and awsemfexporter. |
| `adotcollector.image.tag` | `0.149.0` | Collector image tag. |
| `adotcollector.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `adotcollector.extraEnvsFrom` | `[{configMapRef: {name: metrics-cloudwatch-cluster-env}}]` | Extra `envFrom` entries for the OpenTelemetry collector. The default ConfigMap supplies `AWS_REGION` and `EMF_LOG_GROUP_NAME`; append the static AWS credentials Secret at index `1` on non-EKS clusters. |
| `adapter.enabled` | `true` | Deploys the CloudWatch Metrics Adapter Deployment and Service. Set to `false` on data-plane / workflow-plane installs. |
| `adapter.replicas` | `1` | Number of adapter replicas. |
| `adapter.image.repository` | `ghcr.io/openchoreo/observability-metrics-cloudwatch-adapter` | Adapter image repository. |
| `adapter.image.tag` | `""` | Adapter image tag. Empty defaults to chart `appVersion`. |
| `adapter.service.port` | `9099` | Adapter HTTP port. |
| `adapter.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `adapter.logLevel` | `INFO` | Adapter log level. Supported values include `DEBUG`, `INFO`, `WARN`, and `ERROR`. |
| `adapter.queryTimeoutSeconds` | `30` | Reserved query timeout setting. |
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
