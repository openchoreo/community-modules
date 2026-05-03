# Observability Logs Module for AWS CloudWatch

|               |           |
| ------------- |-----------|
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_logs_cloudwatch)](https://codecov.io/gh/openchoreo/community-modules) |

This module ships OpenChoreo workload logs to AWS CloudWatch Logs and exposes them back to the OpenChoreo Observer through the standard logs adapter API. It also supports log-based alerts by translating OpenChoreo alert rules into CloudWatch Logs metric filters and CloudWatch metric alarms.

It is designed for both:

- EKS clusters using Pod Identity or other AWS SDK default credential sources
- Non-EKS clusters such as `k3d` and `kind` using static AWS credentials

## What this module includes

- A Helm chart that installs the upstream `amazon-cloudwatch-observability` stack for log shipping
- A Go adapter service that implements the OpenChoreo logs query and alerting APIs
- A setup job that creates required CloudWatch log groups and retention settings
- Optional webhook exposure for CloudWatch alarm delivery through EventBridge

## How it works

1. The upstream CloudWatch agent and Fluent Bit DaemonSets collect container logs and write them to `/aws/containerinsights/<cluster-name>/application`.
2. The adapter serves OpenChoreo log queries by translating them into CloudWatch Logs Insights queries.
3. When alerting is enabled, the adapter creates and manages CloudWatch metric filters and metric alarms for log-based alert rules.
4. CloudWatch alarm state changes are forwarded to the adapter webhook through EventBridge, and the adapter relays them to the OpenChoreo Observer.

The adapter exposes:

- `POST /api/v1/logs/query`
- `POST/GET/PUT/DELETE /api/v1alpha1/alerts/rules`
- `POST /api/v1alpha1/alerts/webhook`
- `GET /healthz`
- `GET /livez`

## Prerequisites

- OpenChoreo with the observability plane installed
- `helm`, `kubectl`, `aws`, and `jq`
- An AWS region and a cluster name
- AWS permissions for both log ingestion and adapter query/alerting operations

### Adapter IAM policy

Attach the following policy to the IAM principal used by the adapter. Replace `<region>`, `<account-id>`, and `<cluster-name>`.

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
      "Resource": "arn:aws:logs:<region>:<account-id>:log-group:/aws/containerinsights/<cluster-name>/application:*"
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

The log shipping side also needs the AWS-managed `CloudWatchAgentServerPolicy`.

## Installation

### Option A: EKS with Pod Identity

This is the recommended path.

Export the values used by the installation:

```bash
export AWS_REGION=us-east-1
export CLUSTER_NAME=openchoreo-dev
export NS=openchoreo-observability-plane
export WEBHOOK_SHARED_SECRET="$(openssl rand -base64 32)"
```

Create one IAM role with:

- The adapter IAM policy above
- `CloudWatchAgentServerPolicy`

Use this trust policy for EKS Pod Identity:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Service": "pods.eks.amazonaws.com" },
      "Action": ["sts:AssumeRole", "sts:TagSession"]
    }
  ]
}
```

Create Pod Identity associations in namespace `$NS` for these ServiceAccounts:

- `logs-adapter-cloudwatch`
- `cloudwatch-setup`
- `cloudwatch-agent`

Install the chart:

```bash
helm upgrade --install observability-logs-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-cloudwatch \
  --create-namespace \
  --namespace $NS \
  --version 0.1.0 \
  --set amazon-cloudwatch-observability.clusterName=$CLUSTER_NAME \
  --set amazon-cloudwatch-observability.region=$AWS_REGION \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

Wire the OpenChoreo Observer to the adapter:

```bash
helm upgrade --install openchoreo-observability-plane \
  oci://ghcr.io/openchoreo/helm-charts/openchoreo-observability-plane \
  --namespace $NS \
  --reuse-values \
  --set observer.logsAdapter.enabled=true \
  --set observer.logsAdapter.url=http://observability-logs-cloudwatch-adapter:9098
```

If Pod Identity associations were created after the first install, restart the adapter and DaemonSets so new pods receive credentials.

Detailed EKS notes: [EKS-SETUP.md](EKS-SETUP.md)

### Option B: Non-EKS with static credentials

For `k3d`, `kind`, or other non-EKS clusters, create an IAM user with:

- The adapter IAM policy above
- `CloudWatchAgentServerPolicy`

Then install with static credentials enabled:

```bash
export AWS_REGION=us-east-1
export CLUSTER_NAME=openchoreo-dev
export NS=openchoreo-observability-plane
export AWS_ACCESS_KEY_ID="AKIA..."
export AWS_SECRET_ACCESS_KEY="..."
export WEBHOOK_SHARED_SECRET="$(openssl rand -base64 32)"

helm upgrade --install observability-logs-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-cloudwatch \
  --create-namespace \
  --namespace $NS \
  --version 0.1.0 \
  --set amazon-cloudwatch-observability.clusterName=$CLUSTER_NAME \
  --set amazon-cloudwatch-observability.region=$AWS_REGION \
  --set awsCredentials.create=true \
  --set awsCredentials.name=cloudwatch-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set cloudWatchAgent.injectAwsCredentials.enabled=true \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

## Verification

Check that the adapter is healthy:

```bash
kubectl -n $NS rollout status deploy/logs-adapter-cloudwatch
kubectl -n $NS port-forward svc/observability-logs-cloudwatch-adapter 9098:9098 &
curl -sf http://localhost:9098/healthz
```

If the adapter fails to start, the most common cause is missing AWS credentials or missing values for `clusterName` and `region`.

## Log alerting

Enable alerting:

```bash
helm upgrade observability-logs-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-cloudwatch \
  --namespace $NS \
  --reuse-values \
  --set adapter.alerting.enabled=true
```

Important behavior:

- Alert rules are implemented using CloudWatch Logs metric filters and CloudWatch metric alarms.
- `source.query` must be a CloudWatch Logs filter pattern, not a Logs Insights query.
- Metric filters only evaluate newly ingested logs after the rule is created.
- If the webhook is exposed publicly, expose only `/api/v1alpha1/alerts/webhook` and keep `adapter.alerting.webhookAuth.enabled=true`.

For public webhook exposure, the chart supports `adapter.alerting.webhookIngress` so EventBridge can call the adapter over HTTPS.

## Configuration highlights

| Value | Default | Description |
| --- | --- | --- |
| `amazon-cloudwatch-observability.clusterName` | required | Cluster name used in CloudWatch log group paths |
| `amazon-cloudwatch-observability.region` | required | AWS region used by the subchart and adapter |
| `logGroupPrefix` | `/aws/containerinsights` | Base prefix for CloudWatch log groups |
| `containerLogs.retentionDays` | `7` | Retention period for created log groups |
| `awsCredentials.create` | `false` | Create a Secret for static AWS credentials |
| `cloudWatchAgent.injectAwsCredentials.enabled` | `false` | Patch Fluent Bit to consume the static credentials Secret |
| `adapter.queryTimeoutSeconds` | `30` | Maximum CloudWatch Logs Insights query duration |
| `adapter.queryPollMilliseconds` | `500` | Poll interval for query results |
| `adapter.alerting.enabled` | `false` | Enable alert CRUD and webhook forwarding |
| `adapter.alerting.metricNamespace` | `OpenChoreo/Logs` | Namespace for custom CloudWatch metrics |
| `adapter.alerting.webhookAuth.enabled` | `false` | Require `X-OpenChoreo-Webhook-Token` on the webhook |
| `adapter.alerting.webhookIngress.enabled` | `false` | Create an Ingress exposing only the webhook path |
| `adapter.networkPolicy.enabled` | `false` | Restrict adapter ingress traffic with NetworkPolicy |

For the full configuration surface, see [helm/values.yaml](helm/values.yaml).

## Notes and limitations

- The chart is optimized for EKS first, but includes compatibility defaults for non-EKS clusters.
- The non-EKS static-credentials patch targets Fluent Bit. It does not fully wire CloudWatch agent metrics collection for every topology.
- The adapter validates AWS credentials at startup, so credential issues usually appear as pod startup failures instead of runtime query errors.

## Troubleshooting

- Check adapter logs: `kubectl -n $NS logs deploy/logs-adapter-cloudwatch --tail=200`
- Check Fluent Bit logs: `kubectl -n $NS logs ds/fluent-bit --tail=200`
- Check setup job logs: `kubectl -n $NS logs job/cloudwatch-setup-logs --tail=200`

If logs are not queryable, verify first that they are reaching the CloudWatch log group `/aws/containerinsights/<cluster-name>/application`.
