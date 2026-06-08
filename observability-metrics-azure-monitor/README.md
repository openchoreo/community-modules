# Observability Metrics Module for Azure Monitor

This module exposes **Azure Container Insights** as an OpenChoreo metrics
backend. It serves per-pod CPU and memory time series by querying the `Perf`
and `KubePodInventory` tables in the Log Analytics workspace that the Azure
Monitor Agent (AMA) already populates on an AKS cluster.

It targets AKS clusters with Workload Identity. Authentication uses
`DefaultAzureCredential` against a User-Assigned Managed Identity federated to
the adapter's ServiceAccount — the same model as the sibling
`observability-logs-azure-loganalytics` module.

> **Status:** Phases 1–2 implemented — resource-metrics query, health, and
> alert-rule CRUD + webhook (via Azure Monitor `scheduledQueryRules`). The
> runtime-topology graph is not supported by this backend; see
> [Limitations](#limitations).

## Table of contents

1. [Architecture](#architecture)
2. [Why the Perf table](#why-the-perf-table)
3. [Prerequisites](#prerequisites)
4. [Azure role assignments](#azure-role-assignments)
5. [Install with Helm](#install-with-helm)
6. [Local development](#local-development)
7. [Configuration reference](#configuration-reference)
8. [Limitations](#limitations)
9. [Compatibility](#compatibility)

## Architecture

The adapter has two responsibilities: **resource-metric query** against Azure
Container Insights, and **alerting** through Azure Monitor scheduled query
rules.

Metric collection is **not** in scope for this module — the AKS Container
Insights addon installs the Azure Monitor Agent, which writes per-container
CPU/memory counters to the `Perf` table and pod inventory (including pod
labels) to `KubePodInventory`. This module reads from those tables.

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/metrics/query` | `metric: resource` runs a `Perf ⋈ KubePodInventory` query and returns the six CPU/memory series, scoped by the OpenChoreo namespace label plus optional component/project/environment UIDs. `metric: http` returns empty series (see [Limitations](#limitations)). |
| `POST /api/v1alpha1/metrics/runtime-topology` | **Not supported by this backend.** Returns an empty graph + populated summary window with the `X-OpenChoreo-Adapter-Notice: runtime-topology-not-supported` header — Log Analytics has no pod-to-pod traffic data to build a graph from. |
| `POST /api/v1alpha1/alerts/rules` | Creates an Azure Monitor `scheduledQueryRule` that thresholds the `cpu_usage`/`memory_usage` Perf counter (as a percentage of the pod's limit) over the scoped pods, wired to the configured Action Group. |
| `GET/PUT/DELETE /api/v1alpha1/alerts/rules/{ruleName}` | Look up by `openchoreo-rule-name` tag / CreateOrUpdate / delete the rule. |
| `POST /api/v1alpha1/alerts/webhook` | Receives a Common Alert Schema payload from the Action Group and forwards a normalized alert to the Observer. Protected by the webhook shared-secret. |
| `GET /healthz` | Readiness/liveness check. |

Alerting is always wired (matching the AWS CloudWatch and Prometheus metrics
adapters), so the Azure Monitor alert configuration below is required at boot.

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

## Why the Perf table

Azure offers two metric backends; this module uses the first:

- **Container Insights `Perf` table (this module).** Same Log Analytics
  workspace as the logs adapter, the same `azlogs` SDK, the same UAMI role,
  and **no extra Azure resources or cluster collector**. Container Insights is
  already enabled on the standard AKS setup.
- **Azure Monitor managed Prometheus (Azure Monitor Workspace).** A
  cloud-native Prometheus path, but it adds a second backend (AMW), a second
  collector (`ama-metrics`), and a separate alerting subsystem. Deferred.

## Prerequisites

### OpenChoreo prerequisites

- OpenChoreo is installed.
- The `openchoreo-observability-plane` Helm chart is installed.

### Local tooling

- `go` (1.26+)
- `az` CLI
- `kubectl`, `helm` (for the in-cluster deployment in [Install with Helm](#install-with-helm))

### Azure prerequisites

- An Azure subscription and region (for example `eastus2`).
- An AKS cluster with:
  - **OIDC issuer** and **Workload Identity** enabled.
  - The **Container Insights** addon enabled (`--enable-addons monitoring
    --enable-msi-auth-for-monitoring`). This ships the Azure Monitor Agent
    that writes `Perf` and `KubePodInventory`.
  - **Performance data collection left enabled** in the Container Insights
    DCR. A cost-optimization DCR that disables performance counters empties
    the `Perf` table and every query returns nothing. The adapter logs a
    warning at boot if `Perf` has no `K8SContainer` rows.
  - **Pod label collection enabled** so `KubePodInventory.PodLabel` carries
    the `openchoreo.dev/*` labels.
- A **Log Analytics workspace** on the **Analytics** table plan (the default).
- A **User-Assigned Managed Identity** federated to the adapter's
  ServiceAccount, with the role assignment described below.

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
helm install metrics-adapter-azure-monitor ./helm \
  --namespace openchoreo-observability-plane \
  --set region=eastus2 \
  --set workspace.id=<workspace customerId GUID> \
  --set workspace.resourceId=<workspace ARM resource ID> \
  --set azure.subscriptionId=<subscription id> \
  --set azure.resourceGroup=<resource group> \
  --set adapter.serviceAccount.clientId=<UAMI client id> \
  --set adapter.alerting.actionGroupId=<action group ARM id> \
  --set adapter.alerting.observerUrl=http://observer-internal.openchoreo-observability-plane:8081 \
  --set adapter.alerting.webhookAuth.sharedSecret=<>=16-byte secret>
```

To expose the webhook through the gateway, also set
`adapter.alerting.webhookRoute.enabled=true`,
`adapter.alerting.webhookRoute.parentRef.name=<gateway>`, and a hostname under
`adapter.alerting.webhookRoute.hostnames`.

The chart fails fast at render time if any required value (region, workspace,
subscription/resource group, action group, observer URL, or the SA `client-id`
when it creates the ServiceAccount) is missing.

Point the Observer at the adapter by setting its `METRICS_ADAPTER_URL` to
`http://metrics-adapter.openchoreo-observability-plane.svc.cluster.local:9099`.

## Local development

With no cluster, `DefaultAzureCredential` falls back to your `az login`
session — no code change required.

```bash
az login
export LOG_ANALYTICS_WORKSPACE_ID="<workspace customerId GUID>"
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

The adapter pings the workspace at boot and exits non-zero if the workspace is
unreachable, so a misconfigured pod crash-loops loudly instead of silently
serving empty results.

## Configuration reference

```
Required
  LOG_ANALYTICS_WORKSPACE_ID     the workspace customerId GUID
  AZURE_SUBSCRIPTION_ID
  AZURE_RESOURCE_GROUP
  WORKSPACE_RESOURCE_ID
  ACTION_GROUP_ID
  OBSERVER_URL
  WEBHOOK_SHARED_SECRET          ≥16 bytes when WEBHOOK_AUTH_ENABLED=true

Optional
  SERVER_PORT                    default 9099
  LOG_LEVEL                      debug | info | warn | error   default info
  QUERY_TIMEOUT                  default 30s
  AZURE_REGION                   default eastus2
  WEBHOOK_AUTH_ENABLED           default true

Auth (no env needed — set by the Workload Identity webhook in-cluster)
  AZURE_CLIENT_ID
  AZURE_FEDERATED_TOKEN_FILE
  AZURE_AUTHORITY_HOST
```

## Limitations

- **HTTP RED metrics** (`metric: http`) are not implemented. The endpoint
  returns empty series and sets the
  `X-OpenChoreo-Adapter-Notice: http-metrics-not-implemented` response header.
- **Runtime topology is not supported** by the Log Analytics backend. A
  topology graph is built from pod-to-pod L7 traffic (request counts and
  latencies), which lives in traces or L7/RED metrics — not in Container
  Insights' `Perf` / `KubePodInventory`. The endpoint returns a well-formed
  empty graph with the `X-OpenChoreo-Adapter-Notice: runtime-topology-not-supported`
  header so the Observer does not error. Populated topology on Azure would
  require a different backend (managed Prometheus fed by Cilium/Hubble L7
  metrics, or Application Insights traces).
- **Only `cpu_usage` and `memory_usage` alert sources** are supported. The
  `budget` source (FinOps) returns a `400`.
- **Alert evaluation floor.** Azure Monitor `scheduledQueryRules` evaluate at a
  minimum 5-minute frequency, so sub-5-minute `interval` values are effectively
  clamped by Azure.
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
