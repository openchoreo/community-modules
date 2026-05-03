# Observability Logs Module for AWS CloudWatch

This module ships OpenChoreo workload logs to **AWS CloudWatch Logs** and lets
the OpenChoreo Observer query them through the standard Logs Adapter API.

## Before you start

You need:

- an OpenChoreo cluster with the **observability plane** installed
- `helm`, `kubectl`, `jq`, and `aws` CLI v2
- an AWS region and cluster name
- AWS permissions for:
  - the **agent**: `CloudWatchAgentServerPolicy`
  - the **adapter**: the minimal policy below

If you are installing on EKS with IRSA or Pod Identity, use
[README-eks.md](./README-eks.md). The steps below are the simplest path for
single-cluster `k3d` / `kind` style installs with static AWS credentials.

## Adapter IAM policy

Attach this policy to the IAM principal used by the adapter. Replace
`<region>`, `<account-id>`, and `<cluster-name>`.

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

## Install the module

Set the values used throughout the guide:

```bash
export NS=openchoreo-observability-plane
export CLUSTER_NAME=openchoreo-dev
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
```

Install the chart:

```bash
helm upgrade --install observability-logs-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-cloudwatch \
  --create-namespace \
  --namespace $NS \
  --version 0.1.0 \
  --set clusterName=$CLUSTER_NAME \
  --set region=$AWS_REGION \
  --set amazon-cloudwatch-observability.clusterName=$CLUSTER_NAME \
  --set amazon-cloudwatch-observability.region=$AWS_REGION \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY"
```

Then wire the OpenChoreo Observer to this adapter:

```bash
helm upgrade openchoreo-observability-plane \
  oci://ghcr.io/openchoreo/helm-charts/openchoreo-observability-plane \
  --reuse-values \
  --namespace $NS \
  --set observer.logsAdapter.enabled=true \
  --set observer.logsAdapter.url=http://observability-logs-cloudwatch-adapter:9098
```

## Verify the install

Wait for the adapter:

```bash
kubectl -n $NS rollout status deploy/logs-adapter-cloudwatch
kubectl -n $NS get pods
```

Check health:

```bash
kubectl -n $NS port-forward svc/observability-logs-cloudwatch-adapter 9098:9098 &
curl -sf http://localhost:9098/healthz | jq .
```

Expected:

```json
{"status":"healthy"}
```

## Smoke test: write logs and query them back

Create a short-lived pod that writes a few log lines. It includes the labels
the adapter uses to scope component logs.

```bash
kubectl run cloudwatch-smoke-test --rm -i --restart=Never \
  --labels='openchoreo.dev/namespace=default,openchoreo.dev/component-uid=smoke-test' \
  --image=busybox:1.36 \
  -- sh -c 'for i in 1 2 3 4 5; do echo "smoke-test line $i $(date -Iseconds)"; sleep 1; done'
```

Wait about a minute for Fluent Bit to ship the logs, then query them through
the adapter:

```bash
sleep 60

curl -s http://localhost:9098/api/v1/logs/query \
  -H 'Content-Type: application/json' \
  -d '{
    "startTime": "'"$(date -u -v-15M +%FT%TZ 2>/dev/null || date -u -d '-15 minutes' +%FT%TZ)"'",
    "endTime": "'"$(date -u +%FT%TZ)"'",
    "limit": 20,
    "sortOrder": "desc",
    "searchScope": {
      "namespace": "default",
      "componentUid": "smoke-test"
    }
  }' | jq '{total, tookMs, firstLog: (.logs[0] // null)}'
```

You should see:

- `total` greater than `0`
- a recent log entry under `firstLog`

If nothing comes back, inspect:

```bash
kubectl -n $NS logs ds/fluent-bit --tail=200
kubectl -n $NS logs deploy/logs-adapter-cloudwatch --tail=200
kubectl -n $NS logs job/cloudwatch-agent-post-install --tail=200
```

## Optional: enable log alerting

To create CloudWatch-backed log alerts through the adapter:

```bash
helm upgrade observability-logs-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-cloudwatch \
  --namespace $NS \
  --version 0.1.0 \
  --reuse-values \
  --set adapter.alerting.enabled=true \
  --set adapter.alerting.observerUrl=http://observer-internal:8081
```

If you want AWS to call the adapter webhook through **EventBridge** or a
**Lambda forwarder**, also enable webhook auth:

```bash
export WEBHOOK_SHARED_SECRET="$(openssl rand -base64 32)"

helm upgrade observability-logs-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-cloudwatch \
  --namespace $NS \
  --version 0.1.0 \
  --reuse-values \
  --set adapter.alerting.enabled=true \
  --set adapter.alerting.observerUrl=http://observer-internal:8081 \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

Keep the adapter service private. If you expose it publicly, expose only:

- `/api/v1alpha1/alerts/webhook`

Do not expose:

- `/api/v1/logs/query`
- `/api/v1alpha1/alerts/rules/*`
- `/healthz`
- `/livez`

For a full alerting walkthrough, use:

- [README-eks.md](./README-eks.md)
- [TASKS/aws-logs-alarm/TEST-e2e.md](../TASKS/aws-logs-alarm/TEST-e2e.md)

## Notes

- `clusterName` and `region` must be passed both to this chart and to the
  `amazon-cloudwatch-observability` subchart.
- On `k3d` / `kind`, this chart already includes the compatibility overrides
  needed for the upstream CloudWatch agent and Fluent Bit setup.
- On upgrade, be careful with `--reuse-values` if you rely on values like
  `adapter.alerting.observerUrl` or `adapter.alerting.webhookAuth.sharedSecret`.
