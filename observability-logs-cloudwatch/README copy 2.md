# Observability Logs Module for AWS CloudWatch

|               |           |
| ------------- |-----------|
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_logs_cloudwatch)](https://codecov.io/gh/openchoreo/community-modules) |

This module ships OpenChoreo container logs to **AWS CloudWatch Logs** and lets
the OpenChoreo Observer query them back through the standard Logs Adapter API.
It also supports log-based alert rules by translating them into CloudWatch Logs
metric filters and CloudWatch metric alarms.

## How it works

- The upstream [`amazon-cloudwatch-observability`](https://github.com/aws-observability/helm-charts)
  chart deploys the **CloudWatch Agent + Fluent Bit DaemonSet** cluster-wide.
  Application logs land in `/aws/containerinsights/<clusterName>/application`.
  Records carry the Kubernetes namespace, pod/container names, labels
  (including `openchoreo.dev/{component,environment,project}-uid`) and
  annotations (including `workflows.argoproj.io/node-name`).
- The **adapter** (a small Go service in this module) implements the
  [OpenChoreo Logs Adapter API](https://openchoreo.dev/docs/platform-engineer-guide/modules/observability-logging-adapter-api/):
  - `POST /api/v1/logs/query` — translated to a CloudWatch Logs Insights
    `start_query` / `get_query_results` call, filtered by the scope-specific
    labels.
  - `POST/GET/PUT/DELETE /api/v1alpha1/alerts/rules` — translated to a
    CloudWatch Logs metric filter plus a CloudWatch metric alarm against the
    application log group.
  - `POST /api/v1alpha1/alerts/webhook` — accepts forwarded CloudWatch alarm
    notifications from EventBridge and relays them to the Observer when
    configured.
  - `GET /healthz` — readiness probe; returns 200 once the process is up.
  - `GET /livez` — liveness probe; cheap process-up check that never touches
    AWS so a transient DNS/STS hiccup cannot crashloop the pod.

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the
  **observability plane** enabled. Follow the
  [OpenChoreo install guide](https://openchoreo.dev/docs) and the
  `openchoreo-observability-plane` Helm chart before installing this module.
- `helm`, `kubectl`, `jq`, and `aws` CLI v2 must be available on your machine.
- An **AWS account** with an IAM principal (user for k3d/kind; role for
  EC2/EKS) carrying:
    - Agent: AWS-managed `CloudWatchAgentServerPolicy` (write path).
    - Adapter: the custom policy in
      [Adapter IAM policy](#adapter-iam-policy) below — covers the
      startup ping, query path, and (when `adapter.alerting.enabled=true`)
      the alerting CRUD + webhook tag lookup.
- A **cluster name** that will appear in every log group path, e.g. `openchoreo-acme-dev`.
- An **AWS region**, e.g. `us-east-1`.

### Adapter IAM policy

Apply this policy to the adapter's IAM principal (user/role). Substitute `<region>`,
`<account-id>`, and `<cluster-name>` to match your install. The single log-
group ARN is the application log group the adapter both queries and attaches
metric filters to:

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

Notes on the action choices:

- `logs:GetQueryResults` and `logs:StopQuery` do not support resource-level
  permissions, so they sit in their own `*`-scoped statement.
- `cloudwatch:TagResource` is required because the adapter passes `Tags:` to
  `PutMetricAlarm` at create time. `UntagResource` is **not** in the list —
  the adapter never strips tags.
- CloudWatch metric-alarm actions stay on `"Resource": "*"` because alarm
  resource ARNs only resolve after the first `PutMetricAlarm`, which would
  break first-time creates if scoped.
- `adapter.alerting.alarmActionArns` is left empty for the EventBridge
  delivery path (the rule consumes state-change events directly off the
  AWS event bus, no alarm action needed).

## Installation

The walkthrough below takes a cluster from zero to logs flowing into CloudWatch
and queryable through the OpenChoreo Observer. It uses a static-credentials
Secret created by this chart from install-time values. 

Export the values you'll reuse across the steps:

```bash
export AWS_REGION=us-east-1
export CLUSTER_NAME=openchoreo-dev
export NS=openchoreo-observability-plane
export AWS_ACCESS_KEY_ID="AKIA..."
export AWS_SECRET_ACCESS_KEY="..."
# Shared secret EventBridge will send on every alert webhook. Generate a
# strong one and keep it out of shell history / version control.
export WEBHOOK_SHARED_SECRET="$(openssl rand -base64 32)"
```

### Step 1 — Install this module

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
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set adapter.alerting.webhookAuth.enabled=true \
  --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
```

The `webhookAuth` flags configure the shared secret EventBridge sends on
every alert webhook (`X-OpenChoreo-Webhook-Token` header). The chart only
*reads* this secret when `adapter.alerting.enabled=true` (Step 4 below), so
it is inert until alerting is turned on. To pull the
secret from an existing Kubernetes Secret instead of inlining it, see
[Shared webhook secret](#shared-webhook-secret) under "Log alerting".

### Step 2 — Wire the Observer to this adapter

```bash
helm upgrade --install openchoreo-observability-plane oci://ghcr.io/openchoreo/helm-charts/openchoreo-observability-plane \
--version 1.0.1-hotfix.1 \
--namespace openchoreo-observability-plane \
  --reuse-values \
  --namespace $NS \
  --set observer.logsAdapter.enabled=true \
  --set observer.logsAdapter.url=http://observability-logs-cloudwatch-adapter:9098
```

### Step 3 — Verify logs are flowing

Wait for the adapter rollout to complete and confirm the agent and Fluent Bit
pods are also `Running`:

```bash
kubectl -n $NS rollout status deploy/logs-adapter-cloudwatch
kubectl -n $NS get pods
```

The adapter's readiness endpoint should return `healthy`. AWS credentials are
verified at startup, so credential / STS problems surface as pod startup
failures rather than a long-lived unhealthy endpoint:

```bash
kubectl -n $NS port-forward svc/observability-logs-cloudwatch-adapter 9098:9098 &
curl -sf http://localhost:9098/healthz | jq .
# {"status":"healthy"}
```

**Smoke-test the full ingest + query path.** Drive a few synthetic log
lines into CloudWatch from a one-shot pod in `default` and pull them back
through the adapter. The pod is hand-labeled with synthetic
`openchoreo.dev/namespace` and `openchoreo.dev/component-uid` values so
the adapter's scope filter matches it.

```bash
kubectl run cloudwatch-smoke-test --rm -i --restart=Never \
  --labels='openchoreo.dev/namespace=default,openchoreo.dev/component-uid=smoke-test' \
  --image=busybox:1.36 \
  -- sh -c 'for i in 1 2 3 4 5 6 7 8 9 10; do echo "smoke-test line $i $(date -Iseconds)"; sleep 1; done'

# Wait ~30–60s for Fluent Bit to batch and ship.
sleep 60

curl -s http://localhost:9098/api/v1/logs/query \
  -H 'Content-Type: application/json' \
  -d '{
    "startTime": "'"$(date -u -v-15M +%FT%TZ 2>/dev/null || date -u -d '-15 minutes' +%FT%TZ)"'",
    "endTime":   "'"$(date -u +%FT%TZ)"'",
    "limit": 20,
    "sortOrder": "desc",
    "searchScope": {
      "namespace": "default",
      "componentUid": "smoke-test"
    }
  }' | jq '{total, tookMs, firstLog: (.logs[0] // null)}'
```

Expected:

- `total: 10`
- `tookMs` in the low hundreds or low seconds

If `total` is `0` after waiting another minute or two, jump to
[Troubleshooting](#troubleshooting) — the failure is almost always on the
ingest path (Fluent Bit / cloudwatch-agent / credentials) rather
than inside the adapter.

Once the smoke test passes, the OpenChoreo console will display logs for
any deployed component now that the Observer fronts this adapter (wired
in Step 2).

### Step 4 — Enable log alerting

Run the following command to turn on alerting.

```bash
helm upgrade observability-logs-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-cloudwatch \
  --namespace $NS \
  --reuse-values \
  --set adapter.alerting.enabled=true
```

`webhookAuth.enabled` and `webhookAuth.sharedSecret` are already set from
Step 1, so the adapter starts requiring `X-OpenChoreo-Webhook-Token` on
`/api/v1alpha1/alerts/webhook`.

### Step 5 — Configure AWS EventBridge to send the alert to the adapter

See below example to see how it is configured.

## Log alerting

This module implements log alerts using native CloudWatch resources:

1. A CloudWatch Logs metric filter on
   `/aws/containerinsights/<clusterName>/application`
2. A custom metric in `adapter.alerting.metricNamespace`
3. A CloudWatch metric alarm over that custom metric
4. An EventBridge rule that matches `CloudWatch Alarm State Change` events
   and forwards them to the adapter webhook through an API destination

Important CloudWatch constraints:

- Metric filters evaluate only newly ingested log events after the rule is
  created. They do not backfill against historical logs.
- `source.query` for alert rules is **CloudWatch Logs filter-pattern syntax**,
  not Logs Insights SQL. A rule that works in OpenObserve may need to be
  rewritten for CloudWatch.
- The current implementation supports single tokens such as `ERROR`, quoted
  phrases such as `"payment failed"`, simple `%...%` regex fragments, and JSON
  equality fragments such as `$.log = "timeout"`.
- `eq` and `neq` operators are rejected because CloudWatch metric alarms do not
  support equality comparisons directly.
- All rules share the application log group, and CloudWatch limits metric
  filters per log group.

Alert identity mapping:

- The adapter stores the logical rule identity in CloudWatch alarm tags
  (`openchoreo.rule.name`, `openchoreo.rule.namespace`) and also encodes it
  into the alarm name as a fast path using `base64url` without padding.
- Managed alarm names now follow `oc-logs-alert-ns.<namespace>.rn.<name>.<hash>`
  where `<namespace>` and `<name>` are `base64url`-encoded segments.

### Webhook delivery via EventBridge

CloudWatch alarms cannot POST directly to the adapter. Route
`CloudWatch Alarm State Change` events through an **EventBridge** rule to an
API Destination targeting `/api/v1alpha1/alerts/webhook`. The connection
must send `X-OpenChoreo-Webhook-Token`, and the chart must set
`adapter.alerting.webhookAuth.enabled=true`.

### Shared webhook secret

When `adapter.alerting.webhookAuth.enabled=true`, the adapter rejects any
POST to `/api/v1alpha1/alerts/webhook` that doesn't carry the configured
secret in the `X-OpenChoreo-Webhook-Token` header. The same secret must be
configured on the EventBridge connection's API key (see
[Webhook delivery via EventBridge](#webhook-delivery-via-eventbridge) for
how the EventBridge side consumes it).

**Two ways to provide it to the chart.**

1. **Inline (`sharedSecret`)** — convenient for development and the path
   used by the [Quick start](#quick-start) above:

   ```bash
   --set adapter.alerting.webhookAuth.enabled=true \
   --set adapter.alerting.webhookAuth.sharedSecret="$WEBHOOK_SHARED_SECRET"
   ```

   The secret lands in the Helm release values and the rendered
   ConfigMap/Secret. Anyone with `helm get values` access can read it.

2. **Existing Kubernetes Secret (`sharedSecretRef`)** — recommended for
   production. Create the Secret out of band, then point the chart at it:

   ```bash
   kubectl -n $NS create secret generic openchoreo-webhook-token \
     --from-literal=token="$WEBHOOK_SHARED_SECRET"

   helm upgrade observability-logs-cloudwatch \
     oci://ghcr.io/openchoreo/helm-charts/observability-logs-cloudwatch \
     --namespace $NS --reuse-values \
     --set adapter.alerting.webhookAuth.enabled=true \
     --set adapter.alerting.webhookAuth.sharedSecret="" \
     --set adapter.alerting.webhookAuth.sharedSecretRef.name=openchoreo-webhook-token \
     --set adapter.alerting.webhookAuth.sharedSecretRef.key=token
   ```

   Pass `sharedSecret=""` explicitly when you switch from inline to the
   ref form so the previous inline value doesn't shadow the Secret.

### Public exposure

Keep the adapter Service private and expose only
`/api/v1alpha1/alerts/webhook` publicly when a cloud-side caller must reach
it. Do not expose `/api/v1/logs/query`, `/api/v1alpha1/alerts/rules/*`,
`/healthz`, or `/livez` through a public ingress or load balancer.

For production, prefer the chart's `adapter.alerting.webhookIngress` over an
ad hoc tunnel. Pair it with:

- `adapter.alerting.webhookAuth.enabled=true` for the EventBridge caller.
- `adapter.networkPolicy.enabled=true` when your CNI enforces
  NetworkPolicy, with selectors tuned for your Observer and ingress
  controller namespaces.

## Try Log Alerting — expose the adapter and wire EventBridge

**Step A — port-forward the adapter Service.** Pick any local port; the
adapter listens on `9098` in-cluster.

```bash
kubectl -n $NS port-forward svc/observability-logs-cloudwatch-adapter 19098:9098 &
```

**Step B — open a public HTTPS tunnel to that port.** 
ngrok example:

```bash
ngrok http 19098                                # prints a URL like https://abcd-1234.ngrok-free.app
ADAPTER_WEBHOOK_PUBLIC_URL=https://<that-host>/api/v1alpha1/alerts/webhook
```

**Step C — create the EventBridge connection, API destination, and rule.**

Create eventbridge rule

Create API Destination

Create Connection with Authorization type API Key.

> **Free-tier ngrok URLs rotate every restart.** When the tunnel URL
> changes, also update the API destination's `--invocation-endpoint`
> (Step C below) — otherwise EventBridge silently 404s and alerts never
> reach the adapter.

**Step D — turn alerting on.** Was enabled in Step 4.

**Step E — Test alerting using the following sample.**

[URL Shortner](https://github.com/openchoreo/openchoreo/tree/main/samples/from-image/url-shortener)

## Configuration reference

| Value                                       | Default                      | Description                                                                 |
| ------------------------------------------- | ---------------------------- | --------------------------------------------------------------------------- |
| `clusterName`                               | _(required)_                 | Cluster segment in the CloudWatch log group path.                           |
| `region`                                    | _(required)_                 | AWS region for log groups and API calls.                                    |
| `logGroupPrefix`                            | `/aws/containerinsights`     | Prefix shared by application/dataplane/host log groups.                     |
| `awsCredentials.create`                     | `true`                       | Create a static-credentials Secret from the values below.                   |
| `awsCredentials.name`                       | `cloudwatch-aws-credentials` | Name of the Secret. Must exist (created here or out of band) unless using IRSA/instance-profile. |
| `awsCredentials.accessKeyId`                | _(required if `create=true`)_ | AWS access key ID for both agent and adapter.                              |
| `awsCredentials.secretAccessKey`            | _(required if `create=true`)_ | AWS secret access key for both agent and adapter.                          |
| `containerLogs.retentionDays`               | `7`                          | Retention applied to the log groups the setup Job creates.                  |
| `cloudWatchAgent.enabled`                   | `true`                       | Gate for the `amazon-cloudwatch-observability` subchart.                    |
| `cloudWatchAgent.bridgeService.enabled`     | `true`                       | Create `amazon-cloudwatch/cloudwatch-agent` ExternalName forwarding to the real Service. Required when the chart is installed in any namespace other than `amazon-cloudwatch`. |
| `cloudWatchAgent.injectAwsCredentials.enabled` | `true`                    | Run a post-install Job that patches the upstream Fluent Bit DaemonSet to `envFrom` the credentials Secret. Auto-skipped when `awsCredentials.name` is empty (IRSA / Pod Identity / instance-profile path). |
| `cloudWatchAgent.hookImage.{repository,tag}`| `alpine/k8s:1.30.0`          | Image used by the post-install hook Job. Must contain `kubectl` and a POSIX shell at `/bin/sh`. |
| `setup.enabled`                             | `true`                       | Run the post-install Job that ensures log groups + retention exist.         |
| `adapter.enabled`                           | `true`                       | Run the Observer-facing adapter Deployment + Service.                       |
| `adapter.queryTimeoutSeconds`               | `30`                         | Upper bound per Logs Insights query.                                        |
| `adapter.queryPollMilliseconds`             | `500`                        | Poll cadence between `get_query_results` calls.                             |
| `adapter.alerting.enabled`                  | `false`                      | Enable alert CRUD and webhook forwarding configuration.                     |
| `adapter.alerting.metricNamespace`          | `OpenChoreo/Logs`            | Namespace for custom metrics emitted by metric filters.                     |
| `adapter.alerting.alarmActionArns`          | `[]`                         | Alarm-action ARNs (0-5). Leave empty for the EventBridge delivery path.     |
| `adapter.alerting.okActionArns`             | `[]`                         | Optional OK-state action ARNs.                                              |
| `adapter.alerting.insufficientDataActionArns` | `[]`                       | Optional insufficient-data action ARNs.                                     |
| `adapter.alerting.observerUrl`              | `http://observer-internal:8081` | Base URL of the Observer for webhook forwarding. Default points at the in-cluster Observer Service. |
| `adapter.alerting.forwardRecovery`          | `false`                      | Forward `OK` / `INSUFFICIENT_DATA` transitions in addition to `ALARM`.      |
| `adapter.alerting.webhookAuth.enabled`      | `false`                      | Require a shared secret on the webhook (EventBridge caller).                |
| `adapter.alerting.webhookAuth.sharedSecret` | `""`                         | Shared secret value for `X-OpenChoreo-Webhook-Token`.                       |
| `adapter.alerting.webhookAuth.sharedSecretRef.name` | `""`                 | Existing Secret name to read the webhook token from.                        |
| `adapter.alerting.webhookAuth.sharedSecretRef.key` | `"token"`             | Key within the existing Secret that stores the webhook token.               |
| `adapter.alerting.webhookIngress.enabled`   | `false`                      | Create an Ingress that exposes only `/api/v1alpha1/alerts/webhook`.         |
| `adapter.alerting.webhookIngress.className` | `nginx`                      | Ingress class to use for the public webhook path.                           |
| `adapter.alerting.webhookIngress.host`      | `""`                         | Public hostname for the webhook Ingress. Required when enabled.             |
| `adapter.alerting.webhookIngress.annotations` | `{}`                       | Optional ingress annotations (cert-manager, controller-specific settings).  |
| `adapter.alerting.webhookIngress.tls.secretName` | `""`                    | TLS Secret name for the webhook Ingress. Required when enabled.             |
| `adapter.networkPolicy.enabled`             | `false`                      | Create an ingress-only NetworkPolicy for the adapter pod.                   |
| `adapter.networkPolicy.observerNamespaceLabels` | `{kubernetes.io/metadata.name: openchoreo-observability-plane}` | Namespace labels used to allow Observer traffic. |
| `adapter.networkPolicy.observerPodLabels`   | `{}`                         | Pod labels used to allow Observer traffic. Tune per observability-plane deployment. |
| `adapter.networkPolicy.ingressNamespaceLabels` | `{kubernetes.io/metadata.name: ingress-nginx}` | Namespace labels used to allow ingress-controller traffic. |
| `adapter.networkPolicy.allowProbeIPBlock`   | `""`                         | Optional node CIDR to allow kubelet probe traffic when required by the CNI. |

## k3d / kind compatibility

The upstream `amazon-cloudwatch-observability` chart targets EKS by default,
which means out of the box it makes three assumptions that don't hold on
k3d / kind:

1. **The chart runs in namespace `amazon-cloudwatch`.** Fluent Bit's Pod
   Association filter resolves `cloudwatch-agent.amazon-cloudwatch:4311`. When
   this chart is installed elsewhere (the OpenChoreo convention is
   `openchoreo-observability-plane`), Fluent Bit logs steady "no upstream
   connections available" errors. → mitigated by the
   `cloudWatchAgent.bridgeService` ExternalName Service this chart ships.
2. **Fluent Bit gets AWS credentials via IRSA / instance-profile.** The
   upstream DaemonSet has no env-injection knob, so static credentials cannot
   reach the `cloudwatch_logs` output plugin. → mitigated by the
   `cloudWatchAgent.injectAwsCredentials` post-install Job, which patches the
   DaemonSet to `envFrom` the same Secret the adapter consumes.
3. **Nodes have a systemd journal at `/var/log/journal`.** k3d / kind nodes do
   not, and the upstream Fluent Bit DaemonSet crashloops trying to tail it.
   → mitigated by overriding
   `amazon-cloudwatch-observability.containerLogs.fluentBit.config.extraFiles.{dataplane-log.conf,host-log.conf}`
   with empty no-ops in `helm/values.yaml`.
4. **Pod labels and annotations are stripped from log records.** The upstream
   `application-log.conf` sets `Labels Off` / `Annotations Off` on the Fluent
   Bit kubernetes filter, but the adapter scopes queries by
   `openchoreo.dev/{component,environment,project}-uid` pod labels
   (ComponentSearchScope) and the `workflows.argoproj.io/node-name` annotation
   (WorkflowSearchScope). → mitigated by re-shipping the upstream
   `application-log.conf` verbatim in `helm/values.yaml` with `Labels` flipped
   to `On`. Refresh this block on every subchart bump.
5. **Fluent Bit is wedged calling EC2 IMDS.** The upstream
   `application-log.conf` enables the AWS Application Signals "Entity"
   enrichment path: a `[FILTER] aws … Enable_Entity true`, the kubernetes
   filter's `Use_Pod_Association On`, and the `cloudwatch_logs` output's
   `add_entity true`. All three call IMDS at `169.254.169.254` and/or the
   cloudwatch-agent on port 4311. On k3d / kind there is no IMDS endpoint,
   each call times out for ~10s, and **no logs ever reach CloudWatch**
   (the DaemonSet otherwise looks healthy). → mitigated by the same
   `application-log.conf` override: drop the `[FILTER] aws` block, set
   `Use_Pod_Association Off`, and `add_entity false`. EKS operators who
   want entity enrichment back can re-enable these by reverting that block
   to the v3.1.0 upstream snippet.

All four mitigations are on by default. EKS users with IRSA can disable the
credentials patcher (`cloudWatchAgent.injectAwsCredentials.enabled=false`),
and operators who want the dataplane and host log streams back can override
the two `extraFiles` entries with the upstream defaults from the
`amazon-cloudwatch-observability` chart's `values.yaml`. The bridge Service is
harmless in any topology and can be left enabled.

> **Known gap — cloudwatch-agent (OTel collector) credentials.** The
> `injectAwsCredentials` post-install hook patches the `fluent-bit`
> DaemonSet but **not** the `cloudwatch-agent` DaemonSet. On k3d / kind
> with static credentials the agent therefore logs steady
> `SharedCredsLoad: failed to load shared credentials file` and rejects
> every Container Insights metric batch. This does **not** affect the
> log query or alerting paths (those go through fluent-bit and the
> adapter), so the chart still functions for the v1 scope. If you need
> Container Insights metrics on a non-IRSA cluster, patch the
> `AmazonCloudWatchAgent` CR's `env` to inject the same Secret.

> **Helm upgrade gotcha.** `helm upgrade --reuse-values -f values.yaml`
> applies the values file *on top of* re-used values, so any flag whose
> default in `values.yaml` is empty (e.g.
> `adapter.alerting.webhookAuth.sharedSecret`) will silently revert.
> On `helm upgrade`, either drop `--reuse-values` and pass the full set
> of `--set` overrides, or omit `-f values.yaml`.

### Troubleshooting

If logs do not appear in CloudWatch or the adapter query returns no results,
inspect the agent, adapter, and credential-injection hook logs:

```bash
kubectl -n $NS logs ds/fluent-bit --tail=200
kubectl -n $NS logs deploy/logs-adapter-cloudwatch --tail=200
kubectl -n $NS logs job/cloudwatch-agent-post-install --tail=200
```
