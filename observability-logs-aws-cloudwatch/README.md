# Observability Logs Module for AWS CloudWatch

|               |           |
| ------------- | --------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_logs_cloudwatch)](https://codecov.io/gh/openchoreo/community-modules) |

The **Observability Logs Module for AWS CloudWatch** sends OpenChoreo container logs to **AWS CloudWatch Logs** and exposes them back to the OpenChoreo Observer through the standard **OpenChoreo CloudWatch Adapter API**. It also supports log-based alerting by translating OpenChoreo alert rules into **CloudWatch Logs metric filters** and **CloudWatch metric alarms**.

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
7. [Log alerting](#log-alerting)
8. [Expose the alert webhook through EventBridge](#expose-the-alert-webhook-through-eventbridge)
9. [Shared webhook secret](#shared-webhook-secret)
10. [Troubleshooting](#troubleshooting)
11. [Configuration reference](#configuration-reference)

## Architecture

This module has two main responsibilities:

1. **Log ingestion and query**
2. **Alerting**

In the default single-cluster topology, the chart deploys two workload groups:

1. The upstream [`amazon-cloudwatch-observability`](https://github.com/aws-observability/helm-charts) chart deploys the **CloudWatch Agent** and **Fluent Bit** cluster-wide, shipping container logs to CloudWatch.
2. A Go **CloudWatch Logs Adapter** Deployment that implements the OpenChoreo Logs Adapter API.

Application logs are written to the following CloudWatch log group:

```text
/aws/containerinsights/application
```

Each log record includes Kubernetes metadata such as:

- Namespace
- Pod name
- Container name
- Labels

All participating clusters write to the same configured application log group.

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/logs/query` | Runs a CloudWatch Logs Insights query and filters logs by OpenChoreo scope labels. |
| `POST /api/v1alpha1/alerts/rules` | Creates a CloudWatch Logs metric filter and CloudWatch metric alarm. |
| `GET /api/v1alpha1/alerts/rules/{ruleName}` | Gets the alert rule identified by `{ruleName}`. |
| `PUT /api/v1alpha1/alerts/rules/{ruleName}` | Updates the alert rule identified by `{ruleName}`. |
| `DELETE /api/v1alpha1/alerts/rules/{ruleName}` | Deletes the metric filter and alarm for the alert rule identified by `{ruleName}`. |
| `POST /api/v1alpha1/alerts/webhook` | Receives forwarded CloudWatch alarm events from EventBridge and forwards them to the Observer. |
| `GET /healthz` | Readiness check. Returns `200` once the adapter is ready. |
| `GET /livez` | Liveness check. Does not call AWS, so transient AWS or DNS issues do not crash-loop the pod. |

## Choose a deployment topology

Choose the deployment topology first, then choose the AWS authentication model for each cluster.

| Topology | Install location | Purpose | Required Helm values |
| --- | --- | --- | --- |
| Single cluster | The OpenChoreo cluster where the observability plane and workloads run together. | Deploys the adapter, CloudWatch Agent, Fluent Bit, and setup Job. | Defaults. |
| Observability plane cluster | The cluster where the OpenChoreo observability plane is installed. | Deploys only the CloudWatch Logs Adapter. | `cloudWatchAgent.enabled=false`, `setup.enabled=false` |
| Data-plane / workflow-plane cluster | Each cluster that runs OpenChoreo workloads. | Deploys only the CloudWatch Agent, Fluent Bit, and setup Job. | `adapter.enabled=false` |

For one OpenChoreo installation, keep these values identical across all participating clusters:

- `amazon-cloudwatch-observability.region`
- `global.logGroupPrefix`

CloudWatch Logs is the shared managed backend. Remote workload clusters write directly to CloudWatch Logs and do not need network connectivity back to a self-hosted logging datastore. All clusters that belong to one OpenChoreo installation write to the same application log group, and the observability-plane adapter reads from that group.

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

## IAM permissions

The CloudWatch adapter needs permissions for three paths:

1. Startup identity check.
2. Log query and metric-filter management.
3. CloudWatch alarm management.

Use these policies based on the credential model:

- **EKS Pod Identity or IRSA:** keep the adapter and ingestion policies separate and attach them to separate roles. This keeps each ServiceAccount least-privileged.
- **Static credentials:** use one IAM user and attach both the adapter policy and `CloudWatchAgentServerPolicy`, because the same Kubernetes Secret is shared by all components.

### Fluent Bit IAM policy

The CloudWatch Agent and Fluent Bit  need permission to write logs to CloudWatch. Attach the AWS-managed policy below:

```text
CloudWatchAgentServerPolicy
```

### Adapter IAM policy

Create the following custom IAM policy and attach it to the adapter IAM principal.

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
      "Sid": "LogsScoped",
      "Effect": "Allow",
      "Action": [
        "logs:StartQuery",
        "logs:PutMetricFilter",
        "logs:DescribeMetricFilters",
        "logs:DeleteMetricFilter"
      ],
      "Resource": "arn:aws:logs:<region>:<account-id>:log-group:/aws/containerinsights/application:*"
    },
    {
      "Sid": "LogsUnscoped",
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

Notes:

- `logs:GetQueryResults` and `logs:StopQuery` do not support resource-level permissions, so they must use `"Resource": "*"`.
- `cloudwatch:TagResource` is required because the adapter adds tags when creating alarms.
- `cloudwatch:UntagResource` is not required because the adapter does not remove tags.
- CloudWatch alarm actions use `"Resource": "*"` because alarm ARNs are only known after the first `PutMetricAlarm` call.
- Leave `adapter.alerting.alarmActionArns` empty when using EventBridge to forward alarm state-change events.

### Setup Job IAM policy

The setup Job creates CloudWatch log groups and applies retention. When using separate EKS identities, create the following policy and attach it to the setup Job IAM principal.

Replace:

- `<region>` with your AWS region.
- `<account-id>` with your AWS account ID.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "CreateLogGroups",
      "Effect": "Allow",
      "Action": [
        "logs:CreateLogGroup",
        "logs:PutRetentionPolicy"
      ],
      "Resource": "arn:aws:logs:<region>:<account-id>:log-group:/aws/containerinsights/application:*"
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
OpenChoreoCloudWatchLogsRoleForAdapter
```

Attach the custom [Adapter IAM policy](#adapter-iam-policy).

Create another IAM role for the ingestion path, for example:

```text
OpenChoreoCloudWatchLogsRoleForIngestion
```

Attach the [Setup Job IAM policy](#setup-job-iam-policy) and the AWS-managed `CloudWatchAgentServerPolicy`.

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

Deploy the adapter, CloudWatch Agent, Fluent Bit, and setup Job in one cluster:

```bash
helm upgrade --install observability-logs-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set amazon-cloudwatch-observability.region="$AWS_REGION" \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

#### Observability plane install

Deploy only the adapter in the observability plane cluster:

```bash
helm upgrade --install observability-logs-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set cloudWatchAgent.enabled=false \
  --set setup.enabled=false \
  --set amazon-cloudwatch-observability.region="$AWS_REGION" \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

#### Data-plane / workflow-plane install

Deploy only the CloudWatch Agent, Fluent Bit, and setup Job in each workload cluster:

```bash
helm upgrade --install observability-logs-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set amazon-cloudwatch-observability.region="$AWS_REGION" \
  --set adapter.enabled=false
```

### Step 4 — Create Pod Identity associations

EKS Pod Identity links a Kubernetes ServiceAccount to an IAM role. Each association is scoped to a single EKS cluster, namespace, and ServiceAccount. You must create these associations on every EKS cluster that participates in the install.

#### Single-cluster topology

Create three Pod Identity associations on the EKS cluster, all in the `$NS` namespace:

| ServiceAccount | Used by | IAM role to associate |
| --- | --- | --- |
| `logs-adapter-aws-cloudwatch` | Adapter queries, alerting CRUD, and webhook handling. | The role with the [Adapter IAM policy](#adapter-iam-policy) attached. |
| `cloudwatch-setup` | Setup Job that creates log groups and applies retention. | The role with the [Setup Job IAM policy](#setup-job-iam-policy) and `CloudWatchAgentServerPolicy` attached. |
| `cloudwatch-agent` | CloudWatch Agent and Fluent Bit DaemonSets. | The role with `CloudWatchAgentServerPolicy` attached. |

#### Multi-cluster topology

In a multi-cluster setup, each EKS cluster only runs a subset of the components. Create Pod Identity associations only for the ServiceAccounts that exist in that cluster.

**Observability plane cluster** (runs only the adapter):

| ServiceAccount | IAM role to associate |
| --- | --- |
| `logs-adapter-aws-cloudwatch` | The role with the [Adapter IAM policy](#adapter-iam-policy) attached. |

The `cloudwatch-setup` and `cloudwatch-agent` ServiceAccounts do not exist in this cluster because `cloudWatchAgent.enabled=false` and `setup.enabled=false`.

**Each data-plane / workflow-plane cluster** (runs the CloudWatch Agent, Fluent Bit, and setup Job):

| ServiceAccount | IAM role to associate |
| --- | --- |
| `cloudwatch-setup` | The role with the [Setup Job IAM policy](#setup-job-iam-policy) and `CloudWatchAgentServerPolicy` attached. |
| `cloudwatch-agent` | The role with `CloudWatchAgentServerPolicy` attached. |

The `logs-adapter-aws-cloudwatch` ServiceAccount does not exist in these clusters because `adapter.enabled=false`.

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

- `AccessDeniedException` from `assumed-role/<node-role>` in Fluent Bit or CloudWatch Agent logs.
- `Unable to locate credentials` in the `cloudwatch-setup-logs` Job.

Recreate the workloads so new pods receive Pod Identity credentials:

```bash
kubectl -n "$NS" rollout restart daemonset/cloudwatch-agent
kubectl -n "$NS" rollout restart daemonset/fluent-bit
kubectl -n "$NS" rollout restart deployment/logs-adapter-aws-cloudwatch

# Re-trigger the setup Helm hook (it ran once at install time)
kubectl -n $NS delete job cloudwatch-setup-logs --ignore-not-found
helm upgrade observability-logs-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-aws-cloudwatch \
  --namespace $NS --reuse-values
```

Verify that Pod Identity was injected into a new Fluent Bit pod:

```bash
kubectl -n "$NS" get pod -l k8s-app=fluent-bit -o name | head -1 \
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

In this mode, the chart creates a Kubernetes Secret containing AWS credentials. The adapter reads this Secret, and a post-install hook patches Fluent Bit to consume the same credentials.

### Step 1 — Export shared values

```bash
export AWS_REGION=<your-aws-region>
export NS=openchoreo-observability-plane
export WEBHOOK_SHARED_SECRET="$(openssl rand -base64 32)"
export AWS_ACCESS_KEY_ID=<your-access-key-id>
export AWS_SECRET_ACCESS_KEY=<your-secret-access-key>
```

### Step 2 — Create an IAM user

Create an IAM user and attach both:

- The custom [Adapter IAM policy](#adapter-iam-policy).
- The AWS-managed `CloudWatchAgentServerPolicy`.

Create access keys for this IAM user and export them as shown above.

### Step 3 — Install the module

Use the command that matches the cluster's topology.

#### Single-cluster install

Deploy the adapter, CloudWatch Agent, Fluent Bit, and setup Job in one cluster:

```bash
helm upgrade --install observability-logs-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set amazon-cloudwatch-observability.region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set cloudWatchAgent.injectAwsCredentials.enabled=true \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

This enables the static-credentials path:

- The chart creates a Kubernetes Secret.
- The adapter reads credentials from that Secret.
- The post-install hook patches the upstream Fluent Bit DaemonSet to use the same Secret.

You do not need to restart workloads after installation because credentials are injected during install.

#### Observability plane install

Deploy only the adapter in the observability plane cluster:

```bash
helm upgrade --install observability-logs-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set cloudWatchAgent.enabled=false \
  --set setup.enabled=false \
  --set amazon-cloudwatch-observability.region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

#### Data-plane / workflow-plane install

Deploy only the CloudWatch Agent, Fluent Bit, and setup Job in each workload cluster:

```bash
helm upgrade --install observability-logs-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-aws-cloudwatch \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set amazon-cloudwatch-observability.region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set cloudWatchAgent.injectAwsCredentials.enabled=true \
  --set adapter.enabled=false
```

In an observability-plane-only install, the CloudWatch Agent is disabled, so the created Secret is used only by the adapter.

## Log alerting

If `adapter.alerting.webhookAuth.enabled=true` and `adapter.alerting.webhookAuth.sharedSecret` were set during installation, the adapter now requires the following header on alert webhook calls:

```text
X-OpenChoreo-Webhook-Token
```

### Alerting behavior

The module implements log alerts using native CloudWatch resources:

1. A CloudWatch Logs metric filter on `/aws/containerinsights/application`.
2. A custom CloudWatch metric in `adapter.alerting.metricNamespace`.
3. A CloudWatch metric alarm for that custom metric.
4. An EventBridge rule that forwards CloudWatch alarm state changes to the adapter webhook.

Important constraints:

- Metric filters evaluate only newly ingested log events. They do not backfill historical logs.
- `source.query` in the trait uses **CloudWatch Logs filter-pattern syntax**.

### Alert identity mapping

The adapter stores the logical OpenChoreo rule identity in CloudWatch alarm tags:

- `openchoreo.rule.name`
- `openchoreo.rule.namespace`

The adapter also encodes the rule identity into the alarm name for fast lookup.

Managed alarm names use this format:

```text
oc-logs-alert-ns.<namespace>.rn.<name>.<hash>
```

`<namespace>` and `<name>` are base64url-encoded without padding. `<hash>` is the first 12 hex characters of `sha256(namespace + "\x00" + name)`.

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
CloudWatch Logs Adapter
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

- `/api/v1/logs/query`
- `/api/v1alpha1/alerts/rules/*`
- `/healthz`
- `/livez`

Use `adapter.alerting.webhookRoute` to create a Gateway API `HTTPRoute` that exposes only the webhook path through your existing Gateway. TLS termination, rate limiting, and any WAF / auth rules belong on the parent Gateway listener.

### Step 1 — Create an EventBridge connection

The connection stores the authentication credentials that EventBridge uses when calling the adapter webhook.

1. Open the Amazon EventBridge console and navigate to **Integration** → **Connections**.
2. Choose **Create connection**.
3. Enter a connection name, for example `openchoreo-logs-webhook-connection`.
4. For **Authorization type**, select **API Key**.
5. Set **API key name** to `X-OpenChoreo-Webhook-Token`.
6. Set **Value** to the same shared secret you configured during Helm installation (the value of `adapter.alerting.webhookAuth.sharedSecret` or the contents of the Kubernetes Secret referenced by `adapter.alerting.webhookAuth.sharedSecretRef`).
7. Choose **Create**.

### Step 2 — Create an EventBridge API destination

The API destination defines the HTTP endpoint that EventBridge calls when a matching event arrives.

1. In the EventBridge console, navigate to **Integration** → **API destinations**.
2. Choose **Create API destination**.
3. Enter a name, for example `openchoreo-logs-webhook`.
4. For **API destination endpoint**, enter the publicly reachable URL of the adapter webhook. This is the external URL exposed through your Gateway or ingress, ending with `/api/v1alpha1/alerts/webhook`. For example: `https://alerts.example.com/api/v1alpha1/alerts/webhook`.
5. For **HTTP method**, select **POST**.
6. For **Connection**, select the connection created in Step 1 (`openchoreo-logs-webhook-connection`).
7. Optionally set an **Invocation rate limit** to protect the adapter from bursts. A value of 10 per second is a reasonable starting point.
8. Choose **Create**.

### Step 3 — Create an EventBridge rule

The rule matches CloudWatch alarm state-change events and routes them to the API destination.

1. In the EventBridge console, navigate to **Buses** → **Rules** on the **default** event bus.
2. Choose **Create rule**.
3. Enter a rule name, for example `openchoreo-logs-alarm-to-webhook`.
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
      "prefix": "oc-logs-alert"
    }]
  }
}
```

This pattern matches only `ALARM` state transitions for alarms whose name starts with `oc-logs-alert`. The prefix filter ensures that metric-module alarms (which use the `oc-metrics-alert` prefix) are not routed to the logs adapter.

9. Choose **Next**.
10. For **Target**, select **EventBridge API destination**.
11. Select the API destination created in Step 2 (`openchoreo-logs-webhook`).
12. Leave the input transformer at the default (full event) unless you have a specific reason to modify it.
13. Choose **Next**, review the rule, and choose **Create rule**.

### Test alert delivery

Follow the URL Shortener sample to generate alert-worthy logs:

```text
https://github.com/openchoreo/openchoreo/tree/main/samples/from-image/url-shortener
```

Then check the adapter logs:

```bash
kubectl -n "$NS" logs deployment/logs-adapter-aws-cloudwatch --tail=100 | grep -Ei 'webhook|forward'
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
kubectl -n "$NS" logs daemonset/fluent-bit --tail=200
kubectl -n "$NS" logs deployment/logs-adapter-aws-cloudwatch --tail=200
kubectl -n "$NS" logs job/cloudwatch-agent-post-install --tail=200
```

### Common issues

| Symptom | Likely cause | What to check |
| --- | --- | --- |
| Adapter pod does not start | Missing or invalid AWS credentials | Check Pod Identity association or static Secret values. |
| Fluent Bit shows `AccessDeniedException` | Pod is using the node IAM role instead of Pod Identity role | Restart Fluent Bit after creating Pod Identity associations. |
| Setup Job shows `Unable to locate credentials` | Pod Identity association missing or static Secret not configured | Check the `cloudwatch-setup` ServiceAccount association or static credentials values. |
| Query returns `total: 0` | Logs not shipped to CloudWatch, or labels are missing | Check Fluent Bit logs and verify that labels are enabled in log records. |
| Fluent Bit logs `no upstream connections available` | Pod Association was re-enabled without the upstream bridge Service | Keep this module's default Fluent Bit override, or set `cloudWatchAgent.bridgeService.enabled=true`. |
| Fluent Bit is healthy but no logs arrive | IMDS/entity enrichment timeout on k3d/kind | Confirm that Application Signals entity enrichment is disabled for non-EKS clusters. |
| Webhook returns unauthorized | Missing or incorrect `X-OpenChoreo-Webhook-Token` | Check EventBridge connection header and chart webhook secret values. |
| Alerts do not fire for old logs | CloudWatch metric filters do not backfill | Generate new matching logs after creating the rule. |
| Setup Job fails after creating Pod Identity associations | Pod Identity association was created after the first Helm install | Delete the failed Job and re-run `helm upgrade` after confirming the association is attached. |

### Rerun a failed setup Job

If the setup Job failed because its Pod Identity association was created after the first Helm install, rerun it manually. `helm upgrade` re-fires the post-upgrade hook, but the failed Job has a fixed name and a new one cannot be created until it is deleted.

Confirm the Pod Identity association for `cloudwatch-setup` is attached before rerunning the upgrade. Otherwise the new Job pod will fail with the same credential error and the upgrade can fail again with `BackoffLimitExceeded`.

```bash
# 1. Delete the failed Job so a new one can be created with the same name.
kubectl -n "$NS" delete job cloudwatch-setup-logs --ignore-not-found

# 2. Re-fire the post-upgrade hook.
helm upgrade observability-logs-aws-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-aws-cloudwatch \
  --namespace "$NS" --reuse-values

# 3. Watch the new Job complete.
kubectl -n "$NS" get job cloudwatch-setup-logs -w
```

If the new Job pod fails again, inspect its logs:

```bash
kubectl -n "$NS" logs -l job-name=cloudwatch-setup-logs --tail=100
```

## Configuration reference

| Value | Default | Description |
| --- | --- | --- |
| `global.logGroupPrefix` | `/aws/containerinsights` | Prefix used by Fluent Bit, the adapter, and the setup Job for the shared application log group. |
| `amazon-cloudwatch-observability.clusterName` | `openchoreo` | Upstream Container Insights chart value required to render the dependency. This module does not use it for log group naming. |
| `amazon-cloudwatch-observability.region` | Required | AWS region for CloudWatch log groups and API calls. |
| `awsCredentials.create` | `false` | Creates a static AWS credentials Secret. Keep `false` for Pod Identity, IRSA, or instance-profile based auth. Set to `true` for k3d, kind, or non-EKS clusters. |
| `awsCredentials.name` | `""` | Name of the AWS credentials Secret. Required when `awsCredentials.create=true`. |
| `awsCredentials.accessKeyId` | Required if `create=true` | AWS access key ID. |
| `awsCredentials.secretAccessKey` | Required if `create=true` | AWS secret access key. |
| `containerLogs.retentionDays` | `7` | Retention period applied to log groups created by the setup Job. Must be one of the retention values supported by CloudWatch Logs. |
| `cloudWatchAgent.enabled` | `true` | Enables the upstream `amazon-cloudwatch-observability` subchart. Set to `false` on an observability-plane-only install. |
| `cloudWatchAgent.bridgeService.enabled` | `false` | Optional compatibility bridge from `amazon-cloudwatch/cloudwatch-agent` to the real Service. Only needed if Pod Association is re-enabled in Fluent Bit. |
| `cloudWatchAgent.injectAwsCredentials.enabled` | `false` | Patches Fluent Bit to consume static AWS credentials. Enable this with `awsCredentials.create=true` for non-EKS clusters. |
| `cloudWatchAgent.hookImage.repository` | `alpine/k8s` | Image used by the post-install hook Job. |
| `cloudWatchAgent.hookImage.tag` | `1.30.0` | Tag used by the post-install hook Job. |
| `setup.enabled` | `true` | Runs the setup Job that creates log groups and applies retention. Set to `false` on an observability-plane-only install. |
| `adapter.enabled` | `true` | Deploys the CloudWatch Logs Adapter Deployment and Service. Set to `false` on data-plane / workflow-plane installs. |
| `adapter.queryTimeoutSeconds` | `30` | Maximum duration for each CloudWatch Logs Insights query. |
| `adapter.queryPollMilliseconds` | `500` | Poll interval for `get_query_results`. |
| `adapter.logLevel` | `INFO` | Adapter log level. Supported values include `DEBUG`, `INFO`, `WARN`, and `ERROR`. |
| `adapter.alerting.enabled` | `true` | Enables alert rule CRUD and webhook forwarding. |
| `adapter.alerting.metricNamespace` | `OpenChoreo/Logs` | CloudWatch metric namespace for metrics emitted from metric filters. |
| `adapter.alerting.alarmActionArns` | `[]` | Optional alarm action ARNs. Leave empty when using EventBridge. |
| `adapter.alerting.okActionArns` | `[]` | Optional OK-state action ARNs. |
| `adapter.alerting.insufficientDataActionArns` | `[]` | Optional insufficient-data action ARNs. |
| `adapter.alerting.observerUrl` | `http://observer-internal:8081` | Base URL of the Observer used when forwarding webhook events. |
| `adapter.alerting.snsAllowSubscribeConfirm` | `false` | Allows signed SNS subscription confirmation messages to be confirmed by the adapter. |
| `adapter.alerting.forwardRecovery` | `false` | Forward `OK` and `INSUFFICIENT_DATA` transitions in addition to `ALARM`. |
| `adapter.alerting.webhookAuth.enabled` | `false` | Requires the shared webhook token. |
| `adapter.alerting.webhookAuth.sharedSecret` | `""` | Inline shared secret for webhook authentication. Suitable for development only. |
| `adapter.alerting.webhookAuth.sharedSecretRef.name` | `""` | Existing Kubernetes Secret name containing the webhook token. Recommended for production. |
| `adapter.alerting.webhookAuth.sharedSecretRef.key` | `token` | Key inside the existing Secret. |
| `adapter.alerting.webhookRoute.enabled` | `false` | Creates a Gateway API `HTTPRoute` exposing only `/api/v1alpha1/alerts/webhook`. |
| `adapter.alerting.webhookRoute.parentRef.name` | `gateway-default` | Name of the Gateway to attach to. |
| `adapter.alerting.webhookRoute.parentRef.namespace` | `""` | Namespace of the Gateway. Empty defaults to the release namespace. |
| `adapter.alerting.webhookRoute.parentRef.sectionName` | `""` | Optional listener `sectionName` on the Gateway. |
| `adapter.alerting.webhookRoute.hostnames` | `[]` | Optional hostnames matched at the route level. Empty inherits the listener hostname. |
| `adapter.networkPolicy.enabled` | `false` | Creates a NetworkPolicy for adapter ingress traffic. |
| `adapter.networkPolicy.observerNamespaceLabels` | `{kubernetes.io/metadata.name: openchoreo-observability-plane}` | Namespace labels allowed to call the adapter from the Observer. |
| `adapter.networkPolicy.observerPodLabels` | `{}` | Pod labels allowed to call the adapter from the Observer. Tune per deployment. |
| `adapter.networkPolicy.gatewayNamespaceLabels` | `{}` | Namespace labels of the Gateway data-plane pods allowed to proxy the webhook. Set when `webhookRoute` is enabled. |
| `adapter.networkPolicy.allowProbeIPBlock` | `""` | Optional node CIDR for kubelet probes when required by the CNI. |
