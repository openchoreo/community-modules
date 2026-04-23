# Installing `observability-logs-cloudwatch` on EKS

End-to-end install + verification recipe for a real Amazon EKS cluster. This
covers both the **query path** (logs land in CloudWatch, Observer can read them
back) and the **alerting path** (POST an alert rule, watch the metric filter +
metric alarm get created, trip it, receive the forwarded webhook).

For laptop-scale k3d / kind testing use the existing guides:

- Query path: `TASKS/aws-logss/TEST.md`
- Alerting path: `TASKS/aws-logs-alarm/TEST.md`

This document assumes EKS-specific things (IRSA, managed node groups, public
endpoint or VPC-reachable Observer) that those don't.

## 0. Prerequisites

- EKS cluster, Kubernetes ≥ 1.27, with the **OIDC provider enabled**:
  ```bash
  eksctl utils associate-iam-oidc-provider \
    --cluster $CLUSTER_NAME --region $AWS_REGION --approve
  ```
- Local tools: `aws` CLI v2, `kubectl`, `helm` ≥ 3.12, `eksctl`, `jq`.
- OpenChoreo observability plane already installed in namespace
  `openchoreo-observability-plane` (same prerequisite as k3d — see
  `README.md` §"Installing the observability plane first").
- Exported env vars:
  ```bash
  export AWS_REGION=us-east-1
  export CLUSTER_NAME=openchoreo-prod-eks      # pick your name
  export AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
  export NS=openchoreo-observability-plane
  ```

## 1. IAM: one policy, two roles (IRSA)

On EKS use **IRSA** rather than static access keys. That requires two IAM roles:

1. **Agent role** — attached to the CloudWatch Agent + Fluent Bit
   ServiceAccount. Write-only on log groups. AWS-managed `CloudWatchAgentServerPolicy`
   is sufficient.
2. **Adapter role** — attached to the adapter ServiceAccount. Reads logs (for
   Insights queries) and, when alerting is on, manages metric filters + metric
   alarms.

### 1.1 Create the adapter policy

```bash
cat > /tmp/oc-logs-adapter-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "LogsRead",
      "Effect": "Allow",
      "Action": [
        "logs:DescribeLogGroups",
        "logs:StartQuery",
        "logs:StopQuery",
        "logs:GetQueryResults",
        "logs:TestMetricFilter"
      ],
      "Resource": "arn:aws:logs:${AWS_REGION}:${AWS_ACCOUNT_ID}:log-group:/aws/containerinsights/${CLUSTER_NAME}/*"
    },
    {
      "Sid": "MetricFilters",
      "Effect": "Allow",
      "Action": [
        "logs:PutMetricFilter",
        "logs:DescribeMetricFilters",
        "logs:DeleteMetricFilter"
      ],
      "Resource": "arn:aws:logs:${AWS_REGION}:${AWS_ACCOUNT_ID}:log-group:/aws/containerinsights/${CLUSTER_NAME}/application:*"
    },
    {
      "Sid": "MetricAlarms",
      "Effect": "Allow",
      "Action": [
        "cloudwatch:PutMetricAlarm",
        "cloudwatch:DescribeAlarms",
        "cloudwatch:DeleteAlarms",
        "cloudwatch:TagResource",
        "cloudwatch:UntagResource",
        "cloudwatch:ListTagsForResource"
      ],
      "Resource": "*"
    },
    {
      "Sid": "Identity",
      "Effect": "Allow",
      "Action": ["sts:GetCallerIdentity"],
      "Resource": "*"
    }
  ]
}
EOF

aws iam create-policy \
  --policy-name oc-logs-cloudwatch-adapter \
  --policy-document file:///tmp/oc-logs-adapter-policy.json
```

Leave off the `MetricFilters` + `MetricAlarms` statements if you don't plan to
turn alerting on.

### 1.2 Create the adapter IRSA role

```bash
eksctl create iamserviceaccount \
  --cluster $CLUSTER_NAME --region $AWS_REGION \
  --namespace $NS \
  --name logs-adapter-cloudwatch \
  --attach-policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/oc-logs-cloudwatch-adapter \
  --role-name oc-logs-cloudwatch-adapter \
  --override-existing-serviceaccounts \
  --approve
```

This creates the SA in `$NS` with the IRSA annotation pre-applied. The Helm
chart's SA template is idempotent and will not overwrite the annotation on
upgrade.

### 1.3 Create the agent IRSA role

```bash
eksctl create iamserviceaccount \
  --cluster $CLUSTER_NAME --region $AWS_REGION \
  --namespace amazon-cloudwatch \
  --name cloudwatch-agent \
  --attach-policy-arn arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy \
  --role-name oc-cloudwatch-agent \
  --override-existing-serviceaccounts \
  --approve
```

The CloudWatch Agent + Fluent Bit DaemonSets in the upstream
`amazon-cloudwatch-observability` chart both use the `cloudwatch-agent` SA in
the `amazon-cloudwatch` namespace.

## 2. EKS-flavoured Helm values

Create `values-eks.yaml`:

```yaml
clusterName: openchoreo-prod-eks      # must match $CLUSTER_NAME
region: us-east-1                     # must match $AWS_REGION

# No static credentials — adapter picks identity up via IRSA.
awsCredentials:
  create: false
  name: ""

cloudWatchAgent:
  enabled: true
  # On EKS the upstream chart installs cleanly into `amazon-cloudwatch` with
  # its own Service; the bridge Service added for k3d is harmless but redundant.
  bridgeService:
    enabled: false
  # Fluent Bit gets creds via IRSA on EKS, not via the credentials-patch hook.
  injectAwsCredentials:
    enabled: false

amazon-cloudwatch-observability:
  clusterName: openchoreo-prod-eks
  region: us-east-1
  containerLogs:
    enabled: true
    # EKS nodes have a systemd journal, so dataplane + host log inputs work.
    # Leave the upstream defaults in place by NOT setting extraFiles here — the
    # parent chart's override is only needed for k3d/kind. But do keep the
    # application-log.conf override that turns Labels and Annotations ON (the
    # adapter queries by openchoreo.dev/{component,environment,project}-uid
    # labels and the workflows.argoproj.io/node-name annotation).
    fluentBit:
      config:
        extraFiles:
          application-log.conf: |
            [INPUT]
              Name                tail
              Tag                 application.*
              Exclude_Path        /var/log/containers/cloudwatch-agent*, /var/log/containers/fluent-bit*, /var/log/containers/aws-node*, /var/log/containers/kube-proxy*
              Path                /var/log/containers/*.log
              multiline.parser    docker, cri
              DB                  /var/fluent-bit/state/flb_container.db
              Mem_Buf_Limit       50MB
              Skip_Long_Lines     On
              Refresh_Interval    10
              Rotate_Wait         30
              storage.type        filesystem
              Read_from_Head      ${READ_FROM_HEAD}

            [FILTER]
              Name                aws
              Match               application.*
              az                  false
              ec2_instance_id     false
              Enable_Entity       true

            [FILTER]
              Name                kubernetes
              Match               application.*
              Kube_URL            https://kubernetes.default.svc:443
              Kube_Tag_Prefix     application.var.log.containers.
              Merge_Log           On
              Merge_Log_Key       log_processed
              K8S-Logging.Parser  On
              K8S-Logging.Exclude Off
              Labels              On
              Annotations         On
              Use_Kubelet         On
              Kubelet_Port        10250
              Buffer_Size         0
              Use_Pod_Association On

            [OUTPUT]
              Name                cloudwatch_logs
              Match               application.*
              region              ${AWS_REGION}
              log_group_name      /aws/containerinsights/${CLUSTER_NAME}/application
              log_stream_prefix   ${HOST_NAME}-
              auto_create_group   true
              extra_user_agent    container-insights
              add_entity          true

adapter:
  enabled: true
  logLevel: INFO
  replicas: 1
  # Alerting (CloudWatch metric filter + metric alarm).
  alerting:
    enabled: true
    metricNamespace: OpenChoreo/Logs
    # Filled in after §4.
    alarmActionArns: []
    okActionArns: []
    insufficientDataActionArns: []
    observerUrl: http://observer-internal.openchoreo-observability-plane:8081
    snsAllowSubscribeConfirm: false
    forwardRecovery: false

setup:
  enabled: true
```

## 3. Install

```bash
helm repo update
helm upgrade --install observability-logs-cloudwatch \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-cloudwatch \
  --namespace $NS \
  --values values-eks.yaml

kubectl -n $NS rollout status deploy/logs-adapter-cloudwatch
kubectl -n $NS rollout status daemonset/fluent-bit -n amazon-cloudwatch
```

Confirm the adapter picked up IRSA (no AWS env vars injected from a Secret):

```bash
kubectl -n $NS exec deploy/logs-adapter-cloudwatch -- env | \
  grep -E 'AWS_ROLE_ARN|AWS_WEB_IDENTITY_TOKEN_FILE'
```

Both variables must be present.

## 4. Query-path verification

### 4.1 Health

```bash
kubectl -n $NS port-forward svc/observability-logs-cloudwatch-adapter 9098:9098 &
curl -sf http://localhost:9098/healthz | jq .
# → {"status":"healthy"}
```

If `/healthz` returns 503, check `kubectl -n $NS logs deploy/logs-adapter-cloudwatch`
for an IRSA error. Common failure: the adapter SA annotation was overwritten by
`helm install --force` — re-run §1.2.

### 4.2 Drive real logs

Deploy any OpenChoreo component (the component walk-through from the query
TEST.md works unchanged). Wait 2–3 minutes for logs to land.

```bash
aws logs describe-log-streams --region $AWS_REGION \
  --log-group-name /aws/containerinsights/${CLUSTER_NAME}/application \
  --max-items 3 | jq '.logStreams[].logStreamName'
```

### 4.3 Query through the adapter

```bash
export PROJECT_UID=$(kubectl -n default get project.openchoreo.dev <proj> -o jsonpath='{.metadata.uid}')
export ENV_UID=$(kubectl -n default get environment.openchoreo.dev <env> -o jsonpath='{.metadata.uid}')
export COMPONENT_UID=$(kubectl -n default get component.openchoreo.dev <comp> -o jsonpath='{.metadata.uid}')

curl -s -X POST http://localhost:9098/api/v1/logs/query \
  -H 'Content-Type: application/json' \
  -d "{
    \"searchScope\": {
      \"namespace\": \"default\",
      \"projectUid\": \"$PROJECT_UID\",
      \"environmentUid\": \"$ENV_UID\",
      \"componentUid\": \"$COMPONENT_UID\"
    },
    \"startTime\": \"$(date -u -d '-15 minutes' +%FT%TZ)\",
    \"endTime\":   \"$(date -u +%FT%TZ)\",
    \"limit\": 50
  }" | jq '.total, (.logs[0] // "no results")'
```

Expect `total > 0`. If zero, the most likely cause on EKS is Fluent Bit not
flattening labels the way the adapter expects — confirm against a raw sample:

```bash
aws logs filter-log-events --region $AWS_REGION \
  --log-group-name /aws/containerinsights/${CLUSTER_NAME}/application \
  --limit 1 --filter-pattern '{ $.kubernetes.labels.openchoreo_dev_component_uid = "*" }' \
  | jq '.events[0].message | fromjson | .kubernetes.labels'
```

If the keys come back as `openchoreo.dev/component-uid` rather than
`openchoreo_dev_component_uid`, the chart's `application-log.conf` override in
§2 didn't land — re-check the `kubernetes` filter `Labels On`.

## 5. Alerting-path verification

### 5.1 Pick a webhook delivery mode

CloudWatch alarms can't POST directly to the adapter, so an AWS resource has
to carry the transition. Three supported modes (plan detail in `TASKS/aws-logs-alarm/TASK copy.md` §3.4):

| Mode | When to use | Extra AWS setup |
|---|---|---|
| `eventbridge` | Default recommendation on EKS. | EventBridge rule + API destination + IAM role. |
| `sns` | Adapter reachable via HTTPS from SNS (public ingress or VPC-resolvable URL). | SNS topic + HTTPS subscription. |
| `lambda` | You already run a webhook-forwarder Lambda. | Lambda function + alarm action = Lambda ARN. |
| `noop` | Alarms are created but not forwarded (smoke test). | None. |

Pick **`eventbridge`** for the first real test; the walk-through below assumes it.

### 5.2 Wire an EventBridge API destination

The adapter's webhook endpoint must be reachable from AWS. In-cluster:

- Expose `observability-logs-cloudwatch-adapter.$NS` via an internet-facing
  ALB (ingress) or, for private VPCs, use an EventBridge [API destination with
  a VPC connection](https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-api-destinations.html).
- Note the HTTPS URL as `$ADAPTER_PUBLIC_URL`. It must end with
  `/api/v1alpha1/alerts/webhook`.

Then:

```bash
# Connection + API destination
aws events create-connection --region $AWS_REGION \
  --name oc-logs-alerts-conn \
  --authorization-type API_KEY \
  --auth-parameters 'ApiKeyAuthParameters={ApiKeyName=X-OpenChoreo-Signature,ApiKeyValue=dev-replace-me}'

DEST_ARN=$(aws events create-api-destination --region $AWS_REGION \
  --name oc-logs-alerts-dest \
  --connection-arn "$(aws events describe-connection --name oc-logs-alerts-conn --region $AWS_REGION --query ConnectionArn --output text)" \
  --invocation-endpoint "$ADAPTER_PUBLIC_URL" \
  --http-method POST \
  --query ApiDestinationArn --output text)

# Role EventBridge uses to invoke the destination
cat > /tmp/eb-trust.json <<'EOF'
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"events.amazonaws.com"},"Action":"sts:AssumeRole"}]}
EOF
aws iam create-role --role-name oc-eventbridge-invoke \
  --assume-role-policy-document file:///tmp/eb-trust.json 2>/dev/null || true
aws iam put-role-policy --role-name oc-eventbridge-invoke \
  --policy-name invoke-api-destination \
  --policy-document "$(jq -n --arg arn "$DEST_ARN" \
    '{Version:"2012-10-17",Statement:[{Effect:"Allow",Action:"events:InvokeApiDestination",Resource:$arn}]}')"
ROLE_ARN=$(aws iam get-role --role-name oc-eventbridge-invoke --query Role.Arn --output text)

# Route CloudWatch alarm state-change events whose alarm name starts with
# "oc-logs-alert-" to the API destination.
aws events put-rule --region $AWS_REGION \
  --name oc-logs-alerts-rule \
  --event-pattern '{"source":["aws.cloudwatch"],"detail-type":["CloudWatch Alarm State Change"],"detail":{"alarmName":[{"prefix":"oc-logs-alert-"}]}}'

aws events put-targets --region $AWS_REGION \
  --rule oc-logs-alerts-rule \
  --targets "Id=api-destination,Arn=$DEST_ARN,RoleArn=$ROLE_ARN"
```

In `eventbridge` mode the alarm doesn't need an `AlarmActions` ARN — the rule
picks up the state-change event directly. Leave `alarmActionArns: []`.

### 5.3 Create an alert rule

```bash
curl -si -X POST http://localhost:9098/api/v1alpha1/alerts/rules \
  -H 'Content-Type: application/json' \
  -d "{
    \"metadata\": {
      \"name\": \"test-error-alert\",
      \"namespace\": \"default\",
      \"projectUid\": \"$PROJECT_UID\",
      \"environmentUid\": \"$ENV_UID\",
      \"componentUid\": \"$COMPONENT_UID\"
    },
    \"source\": { \"query\": \"ERROR\" },
    \"condition\": {
      \"enabled\": true,
      \"window\": \"5m\",
      \"interval\": \"1m\",
      \"operator\": \"gt\",
      \"threshold\": 2
    }
  }" | head -20
```

Expect `HTTP/1.1 201 Created` with `action: "created"`, `status: "synced"`, a
`ruleBackendId` (the alarm ARN), and `lastSyncedAt`.

### 5.4 Verify the AWS resources

```bash
# Metric filter
aws logs describe-metric-filters --region $AWS_REGION \
  --log-group-name /aws/containerinsights/${CLUSTER_NAME}/application \
  --filter-name-prefix oc-logs-alert- \
  | jq '.metricFilters[] | {filterName, filterPattern, metricTransformations}'

# Metric alarm
aws cloudwatch describe-alarms --region $AWS_REGION \
  --alarm-name-prefix oc-logs-alert- \
  | jq '.MetricAlarms[] | {AlarmName,MetricName,ComparisonOperator,Threshold,Period,EvaluationPeriods,DatapointsToAlarm,Statistic,TreatMissingData,StateValue,ActionsEnabled}'

# Tags round-trip
ARN=$(aws cloudwatch describe-alarms --region $AWS_REGION \
  --alarm-name-prefix oc-logs-alert- --query 'MetricAlarms[0].AlarmArn' --output text)
aws cloudwatch list-tags-for-resource --region $AWS_REGION --resource-arn "$ARN" | jq .
```

Expect `ComparisonOperator=GreaterThanThreshold`, `Threshold=2`, `Period=60`,
`EvaluationPeriods=5`, `DatapointsToAlarm=5`, `Statistic=Sum`,
`TreatMissingData=notBreaching`, and all five `openchoreo.*` tags present.

### 5.5 GET / PUT / 409 / 404

```bash
curl -s http://localhost:9098/api/v1alpha1/alerts/rules/test-error-alert | jq .

curl -si -X PUT http://localhost:9098/api/v1alpha1/alerts/rules/test-error-alert \
  -H 'Content-Type: application/json' \
  -d "{\"metadata\":{\"name\":\"test-error-alert\",\"namespace\":\"default\",\"projectUid\":\"$PROJECT_UID\",\"environmentUid\":\"$ENV_UID\",\"componentUid\":\"$COMPONENT_UID\"},\"source\":{\"query\":\"ERROR\"},\"condition\":{\"enabled\":true,\"window\":\"5m\",\"interval\":\"1m\",\"operator\":\"gt\",\"threshold\":5}}" | head -1
# → HTTP/1.1 200 OK

curl -si http://localhost:9098/api/v1alpha1/alerts/rules/does-not-exist | head -1
# → HTTP/1.1 404 Not Found

# Duplicate POST
curl -si -X POST http://localhost:9098/api/v1alpha1/alerts/rules \
  -H 'Content-Type: application/json' \
  -d "{\"metadata\":{\"name\":\"test-error-alert\",\"namespace\":\"default\",\"projectUid\":\"$PROJECT_UID\",\"environmentUid\":\"$ENV_UID\",\"componentUid\":\"$COMPONENT_UID\"},\"source\":{\"query\":\"ERROR\"},\"condition\":{\"enabled\":true,\"window\":\"5m\",\"interval\":\"1m\",\"operator\":\"gt\",\"threshold\":5}}" | head -1
# → HTTP/1.1 409 Conflict
```

### 5.6 Validation errors

```bash
# eq is not supported by CloudWatch metric alarms
curl -si -X POST http://localhost:9098/api/v1alpha1/alerts/rules \
  -H 'Content-Type: application/json' \
  -d "{\"metadata\":{\"name\":\"bad-eq\",\"namespace\":\"default\",\"projectUid\":\"$PROJECT_UID\",\"environmentUid\":\"$ENV_UID\",\"componentUid\":\"$COMPONENT_UID\"},\"source\":{\"query\":\"ERROR\"},\"condition\":{\"enabled\":true,\"window\":\"5m\",\"interval\":\"1m\",\"operator\":\"eq\",\"threshold\":1}}" | head -1
# → HTTP/1.1 400 Bad Request

# Logs Insights syntax is not filter-pattern syntax
curl -si -X POST http://localhost:9098/api/v1alpha1/alerts/rules \
  -H 'Content-Type: application/json' \
  -d "{\"metadata\":{\"name\":\"bad-insights\",\"namespace\":\"default\",\"projectUid\":\"$PROJECT_UID\",\"environmentUid\":\"$ENV_UID\",\"componentUid\":\"$COMPONENT_UID\"},\"source\":{\"query\":\"| filter @message like /ERROR/\"},\"condition\":{\"enabled\":true,\"window\":\"5m\",\"interval\":\"1m\",\"operator\":\"gt\",\"threshold\":1}}" | head -1
# → HTTP/1.1 400 Bad Request
```

### 5.7 Drive the alarm to ALARM

Option A — wait for real matches. Emit >2 ERROR log lines per minute for five
minutes from the component:

```bash
kubectl -n default exec deploy/<workload> -- sh -c \
  'while :; do echo "ERROR synthetic $(date)"; sleep 10; done' &
```

Watch the metric:

```bash
METRIC_NAME=$(aws cloudwatch describe-alarms --region $AWS_REGION \
  --alarm-name-prefix oc-logs-alert- --query 'MetricAlarms[0].MetricName' --output text)

aws cloudwatch get-metric-statistics --region $AWS_REGION \
  --namespace OpenChoreo/Logs --metric-name "$METRIC_NAME" \
  --start-time "$(date -u -d '-10 minutes' +%FT%TZ)" \
  --end-time   "$(date -u +%FT%TZ)" \
  --period 60 --statistics Sum | jq .
```

Option B — force the state transition (short-circuits the 5-minute wait):

```bash
aws cloudwatch set-alarm-state --region $AWS_REGION \
  --alarm-name "$(aws cloudwatch describe-alarms --region $AWS_REGION \
    --alarm-name-prefix oc-logs-alert- --query 'MetricAlarms[0].AlarmName' --output text)" \
  --state-value ALARM \
  --state-reason "Manual EKS test trigger"
```

### 5.8 Confirm the adapter received the webhook and forwarded

```bash
kubectl -n $NS logs deploy/logs-adapter-cloudwatch --tail=100 \
  | grep -Ei 'webhook|forward'
```

Expect lines containing `alert webhook received successfully` followed by
`Forward alert to Observer …` (Observer forward is fire-and-forget; a 5xx on
the Observer side is logged but not re-attempted). If the webhook line is
present but no `Forward` follows, check `OBSERVER_URL` is reachable from the
adapter pod:

```bash
kubectl -n $NS exec deploy/logs-adapter-cloudwatch -- \
  wget -qO- http://observer-internal.openchoreo-observability-plane:8081/healthz
```

### 5.9 Idempotent delete

```bash
curl -si -X DELETE http://localhost:9098/api/v1alpha1/alerts/rules/test-error-alert | head -1
# → HTTP/1.1 200 OK

aws cloudwatch describe-alarms --region $AWS_REGION \
  --alarm-name-prefix oc-logs-alert- --query 'MetricAlarms | length(@)'
# → 0

aws logs describe-metric-filters --region $AWS_REGION \
  --log-group-name /aws/containerinsights/${CLUSTER_NAME}/application \
  --filter-name-prefix oc-logs-alert- --query 'metricFilters | length(@)'
# → 0

# DELETE again → 404 (idempotent at the API level)
curl -si -X DELETE http://localhost:9098/api/v1alpha1/alerts/rules/test-error-alert | head -1
# → HTTP/1.1 404 Not Found
```

## 6. Common EKS failure modes

| Symptom | First place to look |
|---|---|
| Adapter pod `CrashLoopBackOff` with `WebIdentityErr` | Adapter SA is missing the IRSA annotation. `kubectl -n $NS get sa logs-adapter-cloudwatch -o yaml` — re-run §1.2 if blank. |
| `/healthz` returns 503 with `AccessDenied sts:GetCallerIdentity` | Trust policy on the adapter role is wrong. Verify the role's trust includes the OIDC provider ARN and `system:serviceaccount:$NS:logs-adapter-cloudwatch` as the subject. |
| POST /rules returns 500 with `AccessDenied logs:PutMetricFilter` | Adapter policy (§1.1) is missing `MetricFilters`/`MetricAlarms` statements. |
| 201 but `StateValue` stays `INSUFFICIENT_DATA` indefinitely | Fluent Bit is dropping the scope labels. Re-verify §4.3. |
| EventBridge rule fires but adapter logs show nothing | API destination connection is off. `aws events list-targets-by-rule --rule oc-logs-alerts-rule` and check the CloudWatch metric `events.FailedInvocations`. |
| Agent logs show `no credentials found` | Agent SA is missing IRSA annotation (§1.3). The upstream chart does annotate the SA it creates, but only when `amazon-cloudwatch-observability.agent.serviceAccount.*` is configured — check with `kubectl -n amazon-cloudwatch get sa cloudwatch-agent -o yaml`. |

## 7. Tear-down

```bash
# Rules
for name in $(curl -s http://localhost:9098/api/v1alpha1/alerts/rules 2>/dev/null | jq -r '.[]?.metadata.name // empty'); do
  curl -sX DELETE "http://localhost:9098/api/v1alpha1/alerts/rules/$name"
done

# EventBridge
aws events remove-targets --region $AWS_REGION --rule oc-logs-alerts-rule --ids api-destination || true
aws events delete-rule          --region $AWS_REGION --name oc-logs-alerts-rule || true
aws events delete-api-destination --region $AWS_REGION --name oc-logs-alerts-dest || true
aws events delete-connection    --region $AWS_REGION --name oc-logs-alerts-conn || true
aws iam delete-role-policy      --role-name oc-eventbridge-invoke --policy-name invoke-api-destination || true
aws iam delete-role             --role-name oc-eventbridge-invoke || true

# Helm release + IRSA
helm uninstall observability-logs-cloudwatch -n $NS
eksctl delete iamserviceaccount --cluster $CLUSTER_NAME --region $AWS_REGION --namespace $NS --name logs-adapter-cloudwatch
eksctl delete iamserviceaccount --cluster $CLUSTER_NAME --region $AWS_REGION --namespace amazon-cloudwatch --name cloudwatch-agent
aws iam delete-policy --policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/oc-logs-cloudwatch-adapter

# Log groups (retain by default — delete only if you want a clean slate)
aws logs delete-log-group --region $AWS_REGION \
  --log-group-name /aws/containerinsights/${CLUSTER_NAME}/application || true
```

## 8. Known EKS-specific gaps

- **Adapter SA IRSA annotation is not yet a chart value.** §1.2 attaches it
  out-of-band via `eksctl`; a future chart change should expose
  `adapter.serviceAccount.annotations` so the annotation survives
  `helm uninstall`/`reinstall` without re-running `eksctl`.
- **Agent SA IRSA annotation** depends on the upstream
  `amazon-cloudwatch-observability` subchart exposing the right values path.
  If the agent does not pick up IRSA, fall back to `kubectl annotate sa -n
  amazon-cloudwatch cloudwatch-agent eks.amazonaws.com/role-arn=...`.
- **Webhook public reachability.** EventBridge API destinations invoke a
  public HTTPS URL by default. Private VPC delivery requires an EventBridge
  VPC connection or proxying via an internal ALB. Chart-side automation for
  that is out of scope in v1.
- **Regex filter patterns** are capped at 5 per log group in CloudWatch; since
  all rules share `/aws/containerinsights/${CLUSTER_NAME}/application`, avoid
  regex `%…%` fragments unless you know the per-log-group budget.
