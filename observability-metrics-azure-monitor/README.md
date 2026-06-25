# Observability Metrics Module for Azure Monitor

This module exposes **Azure Container Insights** as an OpenChoreo metrics
backend. It serves per-pod CPU and memory time series by querying the `Perf`
and `KubePodInventory` tables in the Log Analytics workspace that the Azure
Monitor Agent (AMA) already populates on an AKS cluster, and it manages Azure
Monitor `scheduledQueryRules` for metric alerting.

Authentication uses `DefaultAzureCredential`. In-cluster this resolves the
Workload Identity federated token of a User-Assigned Managed Identity bound to
the adapter's ServiceAccount — the same model as the sibling
`observability-logs-azure-loganalytics` module.

## Table of contents

1. [Architecture](#architecture)
2. [How the resource query maps to Container Insights](#how-the-resource-query-maps-to-container-insights)
3. [Prerequisites](#prerequisites)
4. [Azure role assignments](#azure-role-assignments)
5. [Install with Helm](#install-with-helm)
6. [Metric alerting](#metric-alerting)
7. [Shared webhook secret](#shared-webhook-secret)
8. [Local development](#local-development)
9. [Troubleshooting](#troubleshooting)
10. [Configuration reference](#configuration-reference)
11. [Limitations](#limitations)
12. [Compatibility](#compatibility)

## Architecture

This module has two main responsibilities:

1. **Metric query** against Azure Container Insights.
2. **Alerting** through Azure Monitor scheduled query rules.

Metric **collection** is not in scope for this module. The AKS Container
Insights addon installs the Azure Monitor Agent, which writes per-container
CPU/memory counters to the `Perf` table and pod inventory (including pod
labels) to `KubePodInventory`. The adapter reads from those tables; there is no
collector to deploy.

The adapter is a single Go Deployment that implements the OpenChoreo Metrics
Adapter API:

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/metrics/query` | `metric: resource` runs a `Perf ⋈ KubePodInventory` query and returns the six CPU/memory series, scoped by the OpenChoreo component/project/environment UID pod labels. `metric: http` returns empty series with an `X-OpenChoreo-Adapter-Notice: http-metrics-not-implemented` header. |
| `POST /api/v1alpha1/metrics/runtime-topology` | Not supported by this backend. Returns `501 Not Implemented` (error code `OBS-V1-M-AZURE-MON-501`) — Log Analytics has no pod-to-pod traffic data to build a graph from. |
| `POST /api/v1alpha1/alerts/rules` | Creates an Azure Monitor `scheduledQueryRule` evaluating `(usage / limit) * 100` against the threshold percentage, wired to the configured Action Group. |
| `GET /api/v1alpha1/alerts/rules/{ruleName}` | Gets the alert rule identified by `{ruleName}`. |
| `PUT /api/v1alpha1/alerts/rules/{ruleName}` | Updates the alert rule identified by `{ruleName}`. |
| `DELETE /api/v1alpha1/alerts/rules/{ruleName}` | Deletes the `scheduledQueryRule` for the alert rule identified by `{ruleName}`. |
| `POST /api/v1alpha1/alerts/webhook` | Receives a Common Alert Schema payload from the Action Group and forwards a normalized alert to the Observer. Protected by the webhook shared-secret. |
| `GET /healthz` | Readiness/liveness check. |

### How the resource query maps to Container Insights

The six `ResourceMetricsTimeSeries` fields come from `Perf` rows where
`ObjectName == 'K8SContainer'`:

| API field | `Perf.CounterName` | Unit |
| --- | --- | --- |
| `cpuUsage` | `cpuUsageNanoCores` | nanocores ÷ 1e9 → cores |
| `cpuRequests` | `cpuRequestNanoCores` | nanocores ÷ 1e9 → cores |
| `cpuLimits` | `cpuLimitNanoCores` | nanocores ÷ 1e9 → cores |
| `memoryUsage` | `memoryWorkingSetBytes` | bytes |
| `memoryRequests` | `memoryRequestBytes` | bytes |
| `memoryLimits` | `memoryLimitBytes` | bytes |

`Perf` carries no labels, so the adapter first filters `KubePodInventory` by
the OpenChoreo pod labels, derives the Perf join key
(`InstanceName = strcat(ClusterId, '/', ContainerName)`), and joins. Values are
summed across a pod's containers per time bin. The pod labels live inside the
`KubePodInventory.PodLabel` JSON array, so the adapter parses it
(`parse_json(PodLabel)[0]["openchoreo.dev/..."]`) rather than substring
matching — the stored JSON escapes the `/` in the label keys.

## Prerequisites

Before installing this module, make sure the following are available.

### OpenChoreo prerequisites

- OpenChoreo is installed.
- The `openchoreo-observability-plane` Helm chart is installed.

The commands below assume `az` is logged in (`az login`) and a few shared
variables are exported:

```bash
RG="<your-resource-group>"
LOCATION="eastus2"
AKS_NAME="<your-aks-cluster>"
WORKSPACE_NAME="<your-log-analytics-workspace>"
```

#### Azure subscription and region

Confirm the active subscription and pick a region:

```bash
az account show --query "{name:name, id:id}" -o table
az account list-locations --query "[].name" -o tsv   # list valid regions
```

#### AKS cluster with OIDC issuer and Workload Identity

Enable both on an existing cluster (or pass the same flags to
`az aks create`):

```bash
az aks update -g "$RG" -n "$AKS_NAME" \
  --enable-oidc-issuer \
  --enable-workload-identity
```

Verify they are on:

```bash
az aks show -g "$RG" -n "$AKS_NAME" \
  --query "{oidc:oidcIssuerProfile.enabled, wi:securityProfile.workloadIdentity.enabled}" -o table
```

#### Log Analytics workspace (Analytics table plan)

Create a workspace (Analytics is the default plan) and capture its
resource ID:

```bash
az monitor log-analytics workspace create \
  -g "$RG" -n "$WORKSPACE_NAME" -l "$LOCATION"

WORKSPACE_ARM_ID=$(az monitor log-analytics workspace show \
  -g "$RG" -n "$WORKSPACE_NAME" --query id -o tsv)
```

#### Container Insights addon

Enable the addon against the workspace above. This ships the Azure
Monitor Agent that writes the `Perf` and `KubePodInventory` tables:

```bash
az aks enable-addons -g "$RG" -n "$AKS_NAME" \
  --addons monitoring \
  --enable-msi-auth-for-monitoring \
  --workspace-resource-id "$WORKSPACE_ARM_ID"

az aks get-credentials -g "$RG" -n "$AKS_NAME"   # for the kubectl steps below
```

Two collection settings must stay enabled, or queries return nothing:

- **Performance data collection.** A cost-optimization DCR that disables
  performance counters empties the `Perf` table.
- **Pod label collection** so `KubePodInventory.PodLabel` carries the
  `openchoreo.dev/*` labels the adapter filters by.

Apply the `container-azm-ms-agentconfig` ConfigMap to `kube-system` to
keep both on and collect the OpenChoreo pod labels:

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: container-azm-ms-agentconfig
  namespace: kube-system
data:
  schema-version: v1
  config-version: openchoreo
  log-data-collection-settings: |-
    [log_collection_settings.env_var]
      enabled = true
    [log_collection_settings.metadata_collection]
      enabled = true
      include_fields = ["openchoreo.dev/namespace", "openchoreo.dev/component-uid", "openchoreo.dev/project-uid", "openchoreo.dev/environment-uid"]
EOF
```

The Azure Monitor Agent pods in `kube-system` pick up the change within
a few minutes and restart; confirm with:

```bash
kubectl -n kube-system get pods -l dsName=ama-logs-agent
```

#### User-Assigned Managed Identity

A **User-Assigned Managed Identity** federated to the adapter's
ServiceAccount, with the role assignments described below. Create it and
capture its `clientId`:

```bash
az identity create -g "$RG" -n "<uami-name>" -l "$LOCATION"

UAMI_CLIENT_ID=$(az identity show \
  -g "$RG" -n "<uami-name>" --query clientId -o tsv)
```

#### Action Group

A pre-existing **Action Group** the alert rules notify. Capture its
ARM ID:

```bash
ACTION_GROUP_ARM_ID=$(az monitor action-group show \
  -g "$RG" -n "<action-group-name>" --query id -o tsv)
```

## Azure role assignments

The adapter's UAMI needs these role assignments (the same set the logs adapter
uses, so a UAMI provisioned for the logs module can be reused — only an extra
federated credential for the metrics adapter's ServiceAccount is needed):

| Scope | Role | Why |
| --- | --- | --- |
| Log Analytics workspace | **Log Analytics Reader** | Run KQL queries against `Perf` / `KubePodInventory`. |
| Resource group holding the rules | **Monitoring Contributor** | Create/update/delete/list `scheduledQueryRules`. |
| Action Group | **Reader** | Boot-time Action Group reachability check. |

All three roles are required: the adapter queries metrics and manages alert
rules in a single deployment.

```bash
az identity federated-credential create \
  --name metrics-adapter \
  --identity-name "$UAMI_NAME" \
  --resource-group "$UAMI_RG" \
  --issuer "$(az aks show -n $AKS_NAME -g $AKS_RG --query oidcIssuerProfile.issuerUrl -o tsv)" \
  --subject "system:serviceaccount:openchoreo-observability-plane:metrics-adapter-azure-monitor" \
  --audience api://AzureADTokenExchange
```

The federated-credential `--subject` must match the namespace and ServiceAccount
the chart deploys (`metrics-adapter-azure-monitor` by default).

## Install with Helm

The chart deploys the adapter with Workload Identity (no static credentials): a
Deployment with the `azure.workload.identity/use: "true"` pod label, a
ServiceAccount annotated with the UAMI `client-id`, the Service, and the webhook
Secret. Optionally it renders an HTTPRoute so the Action Group can reach the
webhook through the observability-plane gateway.

```bash
helm upgrade --install metrics-adapter-azure-monitor \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-azure-monitor \
  --namespace openchoreo-observability-plane --create-namespace \
  --version 0.1.0 \
  --set region=eastus2 \
  --set workspace.id=<workspace customerId GUID> \
  --set workspace.resourceId=<workspace ARM resource ID> \
  --set azure.subscriptionId=<subscription id> \
  --set azure.resourceGroup=<resource group> \
  --set adapter.serviceAccount.clientId=<UAMI client id> \
  --set adapter.alerting.actionGroupId=<action group ARM id> \
  --set adapter.alerting.observerUrl=http://observer-internal.openchoreo-observability-plane:8081 \
  --set adapter.alerting.webhookAuth.sharedSecret=<at-least-16-byte secret>
```

The chart fails fast at render time if any required value (region, workspace,
subscription/resource group, action group, observer URL, or the SA `client-id`
when it creates the ServiceAccount) is missing.

Point the Observer at the adapter by setting its `METRICS_ADAPTER_URL` to
`http://metrics-adapter.openchoreo-observability-plane.svc.cluster.local:9099`.

## Metric alerting

Metric alerting is enabled by default when the adapter is installed. The chart
injects `OBSERVER_URL` so forwarded alert events reach the Observer, and the
adapter verifies the Action Group is reachable at boot.

The module implements metric alerts using native Azure Monitor resources:

1. A `scheduledQueryRule` that thresholds the scoped pods' resource usage
   against the configured percentage.
2. The configured **Action Group**, which POSTs a Common Alert Schema payload
   to the adapter's `/api/v1alpha1/alerts/webhook` when the rule fires.

Important constraints:

- **Threshold is a percentage (0–100) of the pod's CPU or memory limit.** Only
  `cpu_usage` and `memory_usage` sources are supported; `budget` returns `400`.
- **WindowSize is snapped up** to the nearest Azure-supported granularity
  (1, 5, 10, 15, 30, 45, 60, … minutes) at rule-create time, so a window like
  `2m` becomes `5m` instead of being rejected.
- **Minimum 5-minute evaluation frequency.** Azure clamps sub-5-minute
  `interval` values.

### Webhook exposure

The Action Group reaches the adapter webhook from outside the cluster. To
expose it through the observability-plane gateway, set
`adapter.alerting.webhookRoute.enabled=true`,
`adapter.alerting.webhookRoute.parentRef.name=<gateway>`, and a hostname under
`adapter.alerting.webhookRoute.hostnames`.

### Test alerting

For an end-to-end OpenChoreo alert and incident flow, see the
[Component Alerts and Incidents tutorial](https://openchoreo.dev/docs/tutorials/component-alerts-and-incidents/).

## Shared webhook secret

When webhook authentication is enabled (`adapter.alerting.webhookAuth.enabled=true`,
the default), the adapter rejects webhook requests that do not include the
configured token. The token is accepted either in the header:

```text
X-OpenChoreo-Webhook-Token
```

or as a `?token=` URL query parameter (used when the Action Group posts
directly to the gateway HTTPRoute). The same value must be configured on the
Action Group's webhook receiver URL.

## Local development

With no cluster, `DefaultAzureCredential` falls back to your `az login`
session — no code change required.

```bash
az login
export LOG_ANALYTICS_WORKSPACE_ID="<workspace customerId GUID>"
export AZURE_SUBSCRIPTION_ID="<subscription id>"
export AZURE_RESOURCE_GROUP="<resource group>"
export WORKSPACE_RESOURCE_ID="<workspace ARM resource ID>"
export ACTION_GROUP_ID="<action group ARM id>"
export OBSERVER_URL="http://localhost:8081"
export WEBHOOK_AUTH_ENABLED=false
make run            # builds ./bin/adapter and runs it on :9099
```

In another terminal:

```bash
curl -s localhost:9099/healthz
# {"status":"healthy"}

START=$(date -u -v-1H '+%Y-%m-%dT%H:%M:%SZ'); END=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
curl -s -X POST localhost:9099/api/v1/metrics/query \
  -H 'content-type: application/json' \
  -d "{\"metric\":\"resource\",\"step\":\"5m\",
       \"startTime\":\"$START\",\"endTime\":\"$END\",
       \"searchScope\":{\"namespace\":\"default\",
         \"componentUid\":\"<component-uid>\"}}"
```

The adapter pings the workspace and verifies the Action Group at boot, exiting
non-zero if either is unreachable, so a misconfigured pod crash-loops loudly
instead of silently serving empty results.

## Troubleshooting

### Start with these logs

```bash
kubectl -n openchoreo-observability-plane logs deployment/metrics-adapter-azure-monitor --tail=200
```

### Common issues

| Symptom | Likely cause | What to check |
| --- | --- | --- |
| Adapter pod crash-loops at boot | Workspace or Action Group unreachable, or missing role assignment | Check the boot logs for the ping / Action Group verification error; confirm the UAMI role assignments. |
| `403 InvalidTokenError` on queries | Federated token not projected, or UAMI lacks Log Analytics Reader | Confirm the pod has the `azure.workload.identity/use: "true"` label and the SA `client-id` annotation; check the role assignment. |
| Query returns empty series | No matching pods, or Container Insights performance collection disabled | Verify `Perf` has `K8SContainer` rows and `KubePodInventory.PodLabel` carries `openchoreo.dev/*` labels for the queried UID. |
| Alert create returns `400` | Unsupported metric source | Only `cpu_usage` and `memory_usage` are supported; `budget` is rejected. |
| Alert create returns `500` with `InvalidRequestContent` | Window not a supported Azure granularity | The adapter snaps windows up automatically; if seen, confirm the running image includes the snapping fix. |
| Webhook returns unauthorized | Missing or incorrect `X-OpenChoreo-Webhook-Token` / `?token=` | Check the Action Group webhook URL and the chart webhook secret. |

## Configuration reference

```
Required
  LOG_ANALYTICS_WORKSPACE_ID     the workspace customerId GUID
  AZURE_SUBSCRIPTION_ID
  AZURE_RESOURCE_GROUP
  WORKSPACE_RESOURCE_ID
  ACTION_GROUP_ID
  OBSERVER_URL
  WEBHOOK_SHARED_SECRET          >=16 bytes when WEBHOOK_AUTH_ENABLED=true

Optional
  SERVER_PORT                    default 9099
  LOG_LEVEL                      debug | info | warn | error   default info
  QUERY_TIMEOUT                  default 30s
  AZURE_REGION                   default eastus2
  WEBHOOK_AUTH_ENABLED           default true
  DEFAULT_EVALUATION_FREQUENCY   default PT5M
  DEFAULT_WINDOW_SIZE            default PT5M

Auth (no env needed — set by the Workload Identity webhook in-cluster)
  AZURE_CLIENT_ID
  AZURE_FEDERATED_TOKEN_FILE
  AZURE_AUTHORITY_HOST
```

## Limitations

- **HTTP RED metrics** (`metric: http`) are not implemented. The endpoint
  returns empty series with the
  `X-OpenChoreo-Adapter-Notice: http-metrics-not-implemented` header.
- **Runtime topology is not supported** by the Log Analytics backend. A
  topology graph is built from pod-to-pod L7 traffic, which lives in traces or
  L7/RED metrics — not in Container Insights' `Perf` / `KubePodInventory`. The
  endpoint returns `501 Not Implemented` (error code
  `OBS-V1-M-AZURE-MON-501`). Populated topology on Azure would require a
  different backend (managed Prometheus fed by Cilium/Hubble L7 metrics, or
  Application Insights traces).
- **Only `cpu_usage` and `memory_usage` alert sources** are supported; `budget`
  (FinOps) returns a `400`.
- **Alert evaluation floor.** Azure Monitor `scheduledQueryRules` evaluate at a
  minimum 5-minute frequency, so sub-5-minute `interval` values are clamped.
- **Metric latency.** `Perf` lands at ~1-minute cadence with a few minutes of
  ingestion lag — fine for dashboards and 5-minute alerts, not for sub-minute
  SLOs.

## Compatibility

| | |
| --- | --- |
| OpenChoreo metrics adapter API | `observability-metrics-adapter.yaml` (same spec as the AWS CloudWatch and Prometheus metrics adapters) |
| Azure backend | Container Insights (`Perf`, `KubePodInventory`) on a Log Analytics **Analytics**-plan workspace |
| Cluster | AKS with Container Insights + Workload Identity |
| Go SDK | `github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs` |
