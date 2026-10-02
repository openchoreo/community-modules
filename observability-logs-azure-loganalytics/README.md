# Observability Logs Module for Azure Log Analytics

[![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?flag=observability-logs-azure-loganalytics)](https://app.codecov.io/gh/openchoreo/community-modules?flags%5B0%5D=observability-logs-azure-loganalytics)

This module exposes Azure Log Analytics as an OpenChoreo logs backend. It
queries `ContainerLogV2` (populated by the Azure Monitor Agent through the
AKS Container Insights addon), serves Kubernetes events from `OTelLogs`
(shipped by the `observability-events-otel-collector` module), and manages
alert rules via Azure Monitor `scheduledQueryRules` with delivery through
pre-existing Action Groups.

It targets AKS clusters with Workload Identity. Authentication uses
`DefaultAzureCredential` against a User-Assigned Managed Identity
federated to the adapter's ServiceAccount.

## Table of contents

1. [Architecture](#architecture)
2. [Choose a deployment topology](#choose-a-deployment-topology)
3. [Prerequisites](#prerequisites)
4. [Azure role assignments](#azure-role-assignments)
5. [Installation on AKS](#installation-on-aks)
6. [Log alerting](#log-alerting)
7. [Platform logs](#platform-logs)
8. [Kubernetes events](#kubernetes-events)
9. [Shared webhook secret](#shared-webhook-secret)
10. [Troubleshooting](#troubleshooting)
11. [Configuration reference](#configuration-reference)
12. [Compatibility](#compatibility)

## Architecture

This module has two main responsibilities:

1. **Log query** against Log Analytics.
2. **Alerting** through Azure Monitor scheduled query rules.

Log shipping is **not** in scope for this chart — the AKS Container
Insights addon installs the Azure Monitor Agent and writes container logs
to a Log Analytics workspace. This module reads from that workspace.

The chart deploys:

1. A Go **Log Analytics Adapter** Deployment that implements the
   OpenChoreo Logs Adapter API.
2. Optional Service, ServiceAccount (with Workload Identity annotation),
   ConfigMap, webhook Secret, Gateway API HTTPRoute, and NetworkPolicy.

Logs are read from `ContainerLogV2`. Each log record carries Kubernetes
metadata through `KubernetesMetadata.podLabels`:

- `kubernetes.namespace_name` (mapped from `PodNamespace`)
- Pod name (`PodName`)
- Container name (`ContainerName`)
- The OpenChoreo pod labels (`openchoreo.dev/namespace`,
  `openchoreo.dev/component-uid`, `openchoreo.dev/project-uid`,
  `openchoreo.dev/environment-uid`)

| Endpoint                                         | Purpose                                                                                                                                                            |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `POST /api/v1/logs/query`                        | Runs a KQL query against `ContainerLogV2`, scoped by OpenChoreo namespace label plus optional component/project/environment UIDs.                                  |
| `POST /api/v1/events/query`                      | Queries Kubernetes events in `OTelLogs`, scoped to a component or workflow run, or swept across namespaces by reason. See [Kubernetes events](#kubernetes-events). |
| `POST /api/v1alpha1/platform-logs/query`         | Queries any pod log the observability plane collects, by raw Kubernetes coordinates. See [Platform logs](#platform-logs).                                          |
| `POST /api/v1alpha1/platform-logs/filter-values` | Lists the distinct values one platform-logs filter can take, to drive the filter pickers.                                                                          |
| `POST /api/v1alpha1/alerts/rules`                | Creates an Azure Monitor scheduled query rule wired to the configured Action Group.                                                                                |
| `GET /api/v1alpha1/alerts/rules/{ruleName}`      | Looks the rule up by its `openchoreo-rule-name` tag.                                                                                                               |
| `PUT /api/v1alpha1/alerts/rules/{ruleName}`      | Updates the rule (CreateOrUpdate semantics).                                                                                                                       |
| `DELETE /api/v1alpha1/alerts/rules/{ruleName}`   | Deletes the rule.                                                                                                                                                  |
| `POST /api/v1alpha1/alerts/webhook`              | Receives Common Alert Schema payloads from the Action Group and forwards a normalised alert to the Observer.                                                       |
| `GET /health`                                    | Readiness/liveness check.                                                                                                                                          |

## Choose a deployment topology

Choose the deployment topology first, then choose the workload identity
model.

| Topology                                            | Install location                                                                 | Purpose                                                                                             | Required Helm values |
| --------------------------------------------------- | -------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- | -------------------- |
| Single cluster                                      | The OpenChoreo cluster where the observability plane and workloads run together. | Deploys the adapter that queries the shared Log Analytics workspace and manages alert rules.        | Defaults.            |
| Observability plane cluster                         | The cluster where the OpenChoreo observability plane is installed.               | Deploys only the adapter.                                                                           | Defaults.            |
| Data-plane / workflow-plane / control-plane cluster | Each cluster that runs OpenChoreo user workloads or system workloads.            | No install. Workload clusters write to Log Analytics via the AKS Container Insights addon directly. | N/A                  |

Log Analytics is the shared managed backend. Remote workload clusters
write to the same workspace via Container Insights and do not need
network connectivity back to the observability plane. The adapter only
runs where the Observer needs to query logs and manage rules.

## Prerequisites

Before installing this module, make sure the following are available.

### OpenChoreo prerequisites

- OpenChoreo is installed.
- The `openchoreo-observability-plane` Helm chart is installed.

See the [OpenChoreo documentation](https://openchoreo.dev/docs) for the
base installation steps.

### Azure prerequisites

The commands below assume `az` is logged in (`az login`) and a few
shared variables are exported:

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

`ContainerLogV2` on the Basic plan is not supported — the adapter uses
the `azlogs` SDK which targets `/query`, and Basic tables require
`/search`. Create a workspace (Analytics is the default plan) and
capture its resource ID:

```bash
az monitor log-analytics workspace create \
  -g "$RG" -n "$WORKSPACE_NAME" -l "$LOCATION"

WORKSPACE_ARM_ID=$(az monitor log-analytics workspace show \
  -g "$RG" -n "$WORKSPACE_NAME" --query id -o tsv)
```

#### Azure Monitor metrics + Container Insights addon

Enable the addon against the workspace above. This installs the Azure
Monitor Agent that ships container logs into the workspace:

```bash
az aks enable-addons -g "$RG" -n "$AKS_NAME" \
  --addons monitoring \
  --workspace-resource-id "$WORKSPACE_ARM_ID"

az aks get-credentials -g "$RG" -n "$AKS_NAME"   # for the kubectl steps below
```

Configure the agent to use the `ContainerLogV2` schema and collect the
OpenChoreo pod labels so the adapter can filter by them. Apply the
`container-azm-ms-agentconfig` ConfigMap to `kube-system`:

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
    [log_collection_settings.schema]
      containerlog_schema_version = "v2"
    [log_collection_settings.metadata_collection]
      enabled = true
      include_fields = ["podLabels", "podAnnotations", "podUid", "image", "imageID", "imageRepo", "imageTag"]
EOF
```

This ConfigMap is also what makes [platform logs](#platform-logs) work:
plane attribution is read out of `KubernetesMetadata.podLabels`, so
`metadata_collection` is required for it, and nothing further needs to be
configured on the agent.

The Azure Monitor Agent pods in `kube-system` pick up the change within
a few minutes and restart; confirm with:

```bash
kubectl -n kube-system get pods -l component=ama-logs-agent
```

#### Action Group

A pre-existing **Action Group** in the same subscription with a
**Webhook** receiver pointed at the adapter's
`/api/v1alpha1/alerts/webhook` endpoint and `useCommonAlertSchema=true`
on that receiver. Capture its ARM ID:

```bash
ACTION_GROUP_ARM_ID=$(az monitor action-group show \
  -g "$RG" -n "<action-group-name>" --query id -o tsv)
```

See [Log alerting](#log-alerting) for how to configure the receiver.

#### User-Assigned Managed Identity

A **User-Assigned Managed Identity** federated to the adapter's
ServiceAccount, with the role assignments described in
[Azure role assignments](#azure-role-assignments). Create it and capture
its `clientId`:

```bash
az identity create -g "$RG" -n "<uami-name>" -l "$LOCATION"

UAMI_CLIENT_ID=$(az identity show \
  -g "$RG" -n "<uami-name>" --query clientId -o tsv)
```

## Azure role assignments

The adapter needs three role assignments on the User-Assigned Managed
Identity it runs as:

| Scope                            | Role                       | Why                                                     |
| -------------------------------- | -------------------------- | ------------------------------------------------------- |
| Log Analytics workspace          | **Log Analytics Reader**   | Run KQL queries against `ContainerLogV2`.               |
| Resource group holding the rules | **Monitoring Contributor** | Create, update, delete, and list `scheduledQueryRules`. |
| Action Group                     | **Reader**                 | Boot-time `verifyActionGroup` reachability check.       |

Federate the UAMI to the adapter's ServiceAccount once the chart is
installed:

```bash
az identity federated-credential create \
  --name logs-adapter \
  --identity-name "$UAMI_NAME" \
  --resource-group "$UAMI_RG" \
  --issuer "$(az aks show -n $AKS_NAME -g $AKS_RG --query oidcIssuerProfile.issuerUrl -o tsv)" \
  --subject "system:serviceaccount:openchoreo-observability-plane:logs-adapter-azure-loganalytics" \
  --audience api://AzureADTokenExchange
```

Pass the UAMI's `clientId` to the chart via
`adapter.serviceAccount.annotations` so the Workload Identity webhook
projects the federated token (see
[Installation](#installation-on-aks)).

## Installation on AKS

The install command below reads its values from shell variables. Export
them first — each is resolved from Azure as follows (replace the
`<...>` placeholders with your own resource names):

```bash
# Subscription, resource group, and region of the AKS cluster.
AZURE_SUBSCRIPTION_ID=$(az account show --query id -o tsv)
AZURE_RESOURCE_GROUP="<your-resource-group>"
AZURE_REGION=$(az group show -n "$AZURE_RESOURCE_GROUP" --query location -o tsv)

# Log Analytics workspace: customer (GUID) ID and full ARM resource ID.
WORKSPACE_CUSTOMER_ID=$(az monitor log-analytics workspace show \
  -g "$AZURE_RESOURCE_GROUP" -n "<workspace-name>" --query customerId -o tsv)
WORKSPACE_ARM_ID=$(az monitor log-analytics workspace show \
  -g "$AZURE_RESOURCE_GROUP" -n "<workspace-name>" --query id -o tsv)

# Action Group ARM ID (must already exist, see "Configure the Action Group").
ACTION_GROUP_ARM_ID=$(az monitor action-group show \
  -g "$AZURE_RESOURCE_GROUP" -n "<action-group-name>" --query id -o tsv)

# User-Assigned Managed Identity clientId the adapter runs as.
UAMI_CLIENT_ID=$(az identity show \
  -g "$AZURE_RESOURCE_GROUP" -n "<uami-name>" --query clientId -o tsv)

# Shared secret guarding the adapter's webhook endpoint (any strong value).
WEBHOOK_TOKEN="<your-webhook-shared-secret>"
```

```bash
helm upgrade --install observability-logs-azure-loganalytics \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-azure-loganalytics \
  --namespace openchoreo-observability-plane --create-namespace \
  --version 0.1.3 \
  --set azure.subscriptionId="$AZURE_SUBSCRIPTION_ID" \
  --set azure.resourceGroup="$AZURE_RESOURCE_GROUP" \
  --set azure.region="$AZURE_REGION" \
  --set logAnalytics.workspaceId="$WORKSPACE_CUSTOMER_ID" \
  --set logAnalytics.workspaceResourceId="$WORKSPACE_ARM_ID" \
  --set actionGroup.id="$ACTION_GROUP_ARM_ID" \
  --set adapter.observerUrl="http://observer-internal.openchoreo-observability-plane.svc.cluster.local:8081" \
  --set adapter.webhookAuth.sharedSecret="$WEBHOOK_TOKEN" \
  --set adapter.serviceAccount.annotations."azure\.workload\.identity/client-id"="$UAMI_CLIENT_ID"
```

The chart's `templates/validate.yaml` fails the install up front with a
readable message when any of these values are missing. Once the install
succeeds, the adapter boots, pings the workspace, and verifies the
Action Group is reachable.

To expose the public webhook path through a Gateway API HTTPRoute (for
example when the Action Group's webhook URL must traverse a public
gateway):

```bash
--set adapter.webhookRoute.enabled=true \
--set adapter.webhookRoute.parentRef.name=gateway-default
```

The chart guards against exposing the webhook without auth: enabling
`webhookRoute` while `webhookAuth.enabled=false` is rejected by
`validate.yaml`.

## Log alerting

The adapter implements log alerting on top of Azure Monitor scheduled
query rules.

### Configure the Action Group webhook receiver

The Action Group ARM ID passed via `actionGroup.id` must already exist
and contain a `webhookReceivers` entry that:

- Has `useCommonAlertSchema: true`.
- Points its `serviceUri` at the adapter's webhook endpoint.

The Action Group's plain Webhook receiver cannot set custom headers, so
the shared secret has to reach the adapter another way. Two options:

- **Direct webhook**: append the secret as a URL query parameter
  (`?token=...`). Simple, but the secret ends up in URLs that
  intermediaries may log.
- **Logic App forwarder (recommended)**: front the adapter with a Logic
  App that holds the secret and forwards requests with it set as a
  header. The Action Group points at the Logic App; the Logic App points
  at the adapter.

## Platform logs

These two endpoints back `GET /api/v1alpha1/platform-logs` on the
Observer, which returns pod logs addressed by **raw Kubernetes
coordinates** — cluster, namespace, pod, container, pod label — across
everything the observability plane collects. That is OpenChoreo's own
components, the charts OpenChoreo depends on but does not ship
(cert-manager, external-secrets, OpenBao, Thunder,...), and user workloads
alike.

Platform logs are not a different set of records from `POST /api/v1/logs/query`,
and not a different store — both read `ContainerLogV2`. Two things differ:

- **How a record is addressed.** `/api/v1/logs/query` takes a project,
  component and environment, and resolves them to the pod labels
  OpenChoreo stamps on workload pods. Platform logs take the Kubernetes
  coordinates directly, so they can reach pods carrying no OpenChoreo
  identity at all — which is the only way to see the platform itself, or
  anything installed alongside it.
- **Who may ask.** `/api/v1/logs/query` is ownership-checked against the
  project named in the query. Platform logs are authorized once, cluster
  wide, by `platformlogs:view`. That is an operator's view rather than a
  tenant's, which is what lets one query span every namespace.

Because the records are not separated by store, `platformlogs:view` also
reads user workload logs. It is a cluster-wide grant and should be
treated as one.

The Observer settles both of those before a query reaches this adapter;
the adapter's job is the translation to KQL and back.

**Cluster identity.** `ContainerLogV2._ResourceId` holds the AKS cluster's
ARM resource ID, and the `clusterInstance` filter matches its last path
segment — the cluster's resource name. This matters because remote
workload clusters all write to the same workspace (see [Choose a
deployment topology](#choose-a-deployment-topology)), so their records
would otherwise be indistinguishable. Azure writes `_ResourceId` itself,
so unlike a chart value it cannot be misconfigured, left at a default or
forged. Two consequences worth knowing:

- Azure lower-cases `_ResourceId`, so cluster names come back lower-case.
  The filter compares case-insensitively, so either spelling matches.
- Two clusters with the same resource name in different resource groups
  would collide. Name them distinctly if you run that topology.

**`kube-system` is excluded.** The Azure Monitor Agent excludes
`kube-system` and `gatekeeper-system` from container log collection by
default: CoreDNS, kube-proxy, the CNI and the API server sit a
layer below OpenChoreo, static pods cannot be labelled, and managed
providers reconcile those namespaces anyway.

That is a collection default, not an API boundary. An operator who enables
`collect_system_pod_logs` for a system container will find those records
returned by platform-logs queries like any others; nothing here prevents
it. It is simply outside the coverage OpenChoreo takes responsibility
for.

### Plane attribution

Which plane a record belongs to is carried on pod labels, set by the Helm
chart that creates the pod, and filtered through the `labels` field:

| Label                     | Value                                                                               |
| ------------------------- | ----------------------------------------------------------------------------------- |
| `openchoreo.dev/plane`    | `controlplane`, `dataplane`, `workflowplane` or `observabilityplane`                |
| `openchoreo.dev/plane-id` | the install's `planeID`, on every plane but the control plane, which is a singleton |

Components OpenChoreo depends on but does not ship — cert-manager,
external-secrets, OpenBao, Thunder, etc. — carry no `openchoreo.dev/plane`
label, so they are reached by namespace or by their own labels rather
than by plane. User workload pods carry no plane label either; they carry
the OpenChoreo identity labels that `POST /api/v1/logs/query` resolves.
So a plane filter is what narrows a query to the platform — the absence
of one is not a restriction, it is the whole cluster.

Log Analytics stores pod labels as JSON with their keys untouched, so a
key comes back spelled exactly as Kubernetes spells it and can be sent
straight back as a filter. A malformed label key is rejected with a `400`
rather than silently dropped, because dropping a filter widens the query.

### Fields not returned

`podIp` is always absent. `ContainerLogV2` has no pod-IP column and
`KubernetesMetadata` does not carry one, so there is nothing to report.
It is optional in the adapter contract.

`containerImage` is reassembled from three metadata fields, because the
agent stores an image split across them: `ghcr.io/openchoreo/controller:1.3.0`
arrives as `imageRepo` = `ghcr.io`, `image` = `openchoreo/controller` and
`imageTag` = `1.3.0`. If an operator narrows `include_fields` and drops
`imageRepo` or `imageTag`, the value degrades to whichever parts were
collected rather than failing — so keep all four image fields in the
ConfigMap if you want the full reference the other backends return.

## Kubernetes events

Kubernetes events are shipped to the workspace by the
[`observability-events-otel-collector`](../observability-events-otel-collector/README.md#azure-log-analytics-native-otlp-ingestion)
module, which enriches each event with the labels of the object it
involves and sends it over Azure Monitor's native OTLP ingestion into the
built-in `OTelLogs` table. Container Insights' own `KubeEvents` table is not
used: it does not carry the object's labels, so its events cannot be tied to
an OpenChoreo component. Follow that module's Azure section to create the
Data Collection Endpoint, Data Collection Rule and collector identity; this
chart needs no extra values and the adapter's identity no extra role, since
**Log Analytics Reader** on the workspace already covers `OTelLogs`.

`OTelLogs` is shared by every OTLP log source routed to the workspace, so the
adapter selects events by the instrumentation scope of the k8s events
receiver (`adapter.events.scopeName`). A record maps onto the adapter
contract as follows:

| Event field                  | `OTelLogs` source                                                                                          |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `timestamp`                  | `TimeGenerated`                                                                                            |
| `message`                    | `Body`                                                                                                     |
| `type`                       | `SeverityText`                                                                                             |
| `reason`                     | `Attributes["k8s.event.reason"]`                                                                           |
| `metadata.objectNamespace`   | `Attributes["k8s.namespace.name"]`                                                                         |
| `metadata.objectKind`/`Name` | `ResourceAttributes["k8s.object.kind"]` / `["k8s.object.name"]`                                            |
| OpenChoreo names and UIDs    | `ResourceAttributes["k8s.object.label.openchoreo.dev/<namespace\|component\|project\|environment>[-uid]"]` |

Queries follow the adapter contract:

- **Component scope** matches the `openchoreo.dev/namespace` label and,
  when given, the project, component and environment UID labels.
- **Workflow scope** matches events in `workflows-<namespace>` whose object
  name starts with the workflow run name, and contains the task name when
  one is given.
- **Unscoped sweeps** (no `searchScope`, `reasons` required) are supported and
  filter by reason alone across every namespace. A request with neither a
  scope nor reasons is rejected with `400`.
- The window is `[startTime, endTime)`. A page is never cut between events
  sharing one timestamp: it is extended to include the whole group, so it can
  exceed `limit`. `total` is the exact number of matching events in the
  window, so `total` greater than the number of events returned means the
  read stopped short.
- Each query is one round trip to Log Analytics, which allows only five
  concurrent queries per identity.

`OTelLogs` is a built-in table, so events queries simply return no events
until the collector ships its first one. If `adapter.events.table` names a
custom table that does not exist yet, the adapter logs a warning at boot and
answers events queries with no events rather than an error. Confirm events
are arriving with:

```kusto
OTelLogs
| where ScopeName == "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/k8seventsreceiver"
| project TimeGenerated, Body, SeverityText, Attributes, ResourceAttributes
| take 5
```

## Shared webhook secret

When `adapter.webhookAuth.enabled` is `true` (the default), the adapter
rejects webhook requests that do not carry the configured token. The
adapter looks for the token in this order:

1. The `X-OpenChoreo-Webhook-Token` HTTP header (preferred — used when a
   Logic App forwarder fronts the adapter).
2. The `token` URL query parameter (fallback — required when the Action
   Group's plain Webhook receiver POSTs the adapter directly, since the
   receiver cannot set custom headers).

The comparison runs in constant time. The token must be at least 16
characters; shorter values are rejected at install time by
`validate.yaml`.

Two ways to provide the secret:

- Inline via `adapter.webhookAuth.sharedSecret`. The chart creates a
  Secret named `logs-adapter-azure-loganalytics-webhook-token` and the
  Deployment mounts it via `secretKeyRef`. The Secret carries
  `helm.sh/resource-policy: keep` so it survives a `helm uninstall`.
- External reference via `adapter.webhookAuth.sharedSecretRef.name`.
  The chart does not create the Secret; the named one must exist in the
  release namespace.

## Troubleshooting

### `Log Analytics ping failed at boot`

The adapter's startup health check failed against
`api.loganalytics.io`. Check the boot logs:

```bash
kubectl -n openchoreo-observability-plane logs deploy/logs-adapter-azure-loganalytics --tail=100
```

Common causes:

- The UAMI does not have **Log Analytics Reader** on the workspace.
- The workspace `customerId` GUID (`logAnalytics.workspaceId`) does not
  match the ARM ID (`logAnalytics.workspaceResourceId`) — they refer to
  different workspaces.
- Workload Identity is not federated to the adapter's ServiceAccount.
  Check the federated credential subject is exactly
  `system:serviceaccount:<release-namespace>:logs-adapter-azure-loganalytics`.

### `action group verification failed at boot`

The adapter could not GET the Action Group. Most often:

- The UAMI does not have **Reader** on the Action Group.
- `actionGroup.id` points at a different resource group than
  `azure.resourceGroup` — the adapter creates rules in
  `azure.resourceGroup`, but the Action Group can live elsewhere as
  long as it is reachable. The error message includes the ARM ID it
  tried.

### Alert fires in Azure but no webhook arrives

Check the Action Group's webhook receiver:

```bash
az monitor action-group show \
  --resource-group $AZURE_RESOURCE_GROUP \
  --name $ACTION_GROUP_NAME \
  --query "webhookReceivers"
```

`useCommonAlertSchema` must be `true`. If it shows `false`, recreate
the receiver via REST (the Azure CLI silently drops the flag on
`update`):

```bash
az rest --method put \
  --uri "https://management.azure.com/subscriptions/$SUB/resourceGroups/$RG/providers/microsoft.insights/actionGroups/$AG?api-version=2024-10-01-preview" \
  --body @action-group-body.json
```

If the URI in the receiver is `https://...:9443/...` and the gateway
uses a self-signed certificate, Azure rejects the TLS handshake
silently. Switch to plain HTTP via the gateway data-plane port, or
front the adapter with a Logic App that terminates TLS with a
publicly-trusted certificate.

### Webhook returns 401 `unauthorized`

The shared secret in the URL/header did not match
`WEBHOOK_SHARED_SECRET`. Verify both:

```bash
kubectl -n openchoreo-observability-plane get secret \
  logs-adapter-azure-loganalytics-webhook-token \
  -o jsonpath='{.data.token}' | base64 -d
```

```bash
az monitor action-group show \
  --resource-group $AZURE_RESOURCE_GROUP \
  --name $ACTION_GROUP_NAME \
  --query "webhookReceivers[].serviceUri" -o tsv
```

Make sure the `?token=...` portion of the URI matches the Secret value
character-for-character.

### Alert rule shows zero matches in Azure but the search phrase is correct

The KQL filter scopes by `PodNamespace`, which is the synthesised DP
namespace the OpenChoreo controller passes through (for example
`dp-default-gcp-microserv-development-4b8b4fdc`). Run the rule's KQL
manually in the Log Analytics workspace and confirm `PodNamespace` is
what you expect. If the AMA's `metadata_collection` is not configured
to capture pod labels, the UID filters (`openchoreo.dev/*`) will not
match either; re-check the `container-azm-ms-agentconfig` ConfigMap.

### Platform logs return records but every plane filter is empty

Records arrive because `ContainerLogV2` is populated regardless, but the
`labels` filter reads `KubernetesMetadata.podLabels`, which only exists
when `metadata_collection` is enabled. Confirm the column is present:

```kusto
ContainerLogV2
| where isnotempty(KubernetesMetadata)
| take 1
```

If that returns nothing, re-apply the `container-azm-ms-agentconfig`
ConfigMap from [Azure prerequisites](#azure-prerequisites) and wait for
the agent pods to restart. The column appears on newly ingested records
only — records already in the workspace do not gain it retroactively.

A plane whose pods carry no `openchoreo.dev/plane` label at all is a
different fault: the label comes from the plane's own Helm chart, not
from this module, so check the chart version deployed on that cluster.

### Platform logs from two clusters look like one cluster

The `clusterInstance` filter derives from `_ResourceId`'s last path
segment, so two AKS clusters sharing a resource name in different
resource groups are indistinguishable. Confirm what the workspace
actually holds:

```kusto
ContainerLogV2
| summarize count() by _ResourceId
```

### Events queries return no events

1. Run the verification query in [Kubernetes events](#kubernetes-events). If
   it returns no rows, nothing has been ingested yet: check the
   events collector's logs for exporter `401`/`403` errors (the identity's
   **Monitoring Metrics Publisher** assignment on the DCR can take a few
   minutes to propagate) or `404` errors (wrong DCE host, DCR immutable ID or
   stream in the `logs_endpoint` URL).
2. If `OTelLogs` has rows but none match the scope name, the events were
   shipped by a different receiver or through a custom DCR transformation;
   set `adapter.events.scopeName` (and `adapter.events.table` for a custom
   table) to match.
3. With a custom `adapter.events.table`, look for
   `events table does not exist in the workspace yet` in the adapter logs: the
   adapter answers with no events until that table is created.

## Configuration reference

| Value                                           | Default                                                                          | Description                                                                                                                                                                                                            |
| ----------------------------------------------- | -------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `azure.subscriptionId`                          | Required                                                                         | Subscription that hosts the scheduled query rules and Action Group.                                                                                                                                                    |
| `azure.resourceGroup`                           | Required                                                                         | Resource group that holds the scheduled query rules.                                                                                                                                                                   |
| `azure.region`                                  | Required                                                                         | Azure region for newly created rules. Must match the workspace region.                                                                                                                                                 |
| `logAnalytics.workspaceId`                      | Required                                                                         | Workspace `customerId` (GUID), not the ARM ID. Used for the `/query` API.                                                                                                                                              |
| `logAnalytics.workspaceResourceId`              | Required                                                                         | Full ARM ID of the Log Analytics workspace. Used as the rule scope.                                                                                                                                                    |
| `actionGroup.id`                                | Required                                                                         | ARM ID of a pre-existing Action Group with a webhook receiver pointed at the adapter.                                                                                                                                  |
| `adapter.enabled`                               | `true`                                                                           | Toggle the adapter Deployment.                                                                                                                                                                                         |
| `adapter.replicas`                              | `1`                                                                              | Adapter replica count.                                                                                                                                                                                                 |
| `adapter.image.repository`                      | `ghcr.io/openchoreo/observability-logs-azure-loganalytics-adapter`               | Adapter container image.                                                                                                                                                                                               |
| `adapter.image.tag`                             | Chart `appVersion`                                                               | Image tag.                                                                                                                                                                                                             |
| `adapter.service.port`                          | `8080`                                                                           | HTTP listener port.                                                                                                                                                                                                    |
| `adapter.observerUrl`                           | `http://observer-internal.openchoreo-observability-plane.svc.cluster.local:8081` | Observer base URL. Fired alerts are forwarded to `${observerUrl}/api/v1alpha1/alerts/webhook`. The alert-webhook endpoint lives on the Observer's internal service (`observer-internal:8081`), not the public `:8080`. |
| `adapter.queryTimeoutSeconds`                   | `30`                                                                             | Upper bound for a single Log Analytics query.                                                                                                                                                                          |
| `adapter.logLevel`                              | `INFO`                                                                           | `DEBUG` \| `INFO` \| `WARN` \| `ERROR`.                                                                                                                                                                                |
| `adapter.alertRuleDefaults.evaluationFrequency` | `PT5M`                                                                           | ISO 8601 duration used when an alert rule request omits one.                                                                                                                                                           |
| `adapter.alertRuleDefaults.windowSize`          | `PT5M`                                                                           | ISO 8601 duration used when an alert rule request omits one.                                                                                                                                                           |
| `adapter.events.table`                          | `OTelLogs`                                                                       | Table Kubernetes events are read from. Must be a plain table name.                                                                                                                                                     |
| `adapter.events.scopeName`                      | k8s events receiver scope                                                        | Instrumentation scope that marks a record in `adapter.events.table` as a Kubernetes event.                                                                                                                             |
| `adapter.serviceAccount.annotations`            | `{}`                                                                             | Annotations applied to the adapter ServiceAccount. Use `azure.workload.identity/client-id: <uami-client-id>` to bind a Managed Identity.                                                                               |
| `adapter.webhookAuth.enabled`                   | `true`                                                                           | Reject webhook calls without the shared secret.                                                                                                                                                                        |
| `adapter.webhookAuth.sharedSecret`              | `""`                                                                             | Inline secret value. Chart creates a Secret; min 16 characters.                                                                                                                                                        |
| `adapter.webhookAuth.sharedSecretRef.name`      | `""`                                                                             | Reference an existing Secret instead of supplying the value inline.                                                                                                                                                    |
| `adapter.webhookAuth.sharedSecretRef.key`       | `token`                                                                          | Key inside the referenced Secret.                                                                                                                                                                                      |
| `adapter.webhookRoute.enabled`                  | `false`                                                                          | Render a Gateway API HTTPRoute exposing only `/api/v1alpha1/alerts/webhook`.                                                                                                                                           |
| `adapter.webhookRoute.parentRef.name`           | `gateway-default`                                                                | Gateway to attach to.                                                                                                                                                                                                  |
| `adapter.webhookRoute.parentRef.namespace`      | `""`                                                                             | Gateway namespace; defaults to the release namespace.                                                                                                                                                                  |
| `adapter.webhookRoute.parentRef.sectionName`    | `""`                                                                             | Optional Gateway listener name.                                                                                                                                                                                        |
| `adapter.webhookRoute.hostnames`                | `[]`                                                                             | Optional hostnames matched at the route level.                                                                                                                                                                         |
| `adapter.networkPolicy.enabled`                 | `false`                                                                          | Render a NetworkPolicy restricting ingress to the adapter Pod.                                                                                                                                                         |
| `adapter.networkPolicy.observerNamespaceLabels` | `{kubernetes.io/metadata.name: openchoreo-observability-plane}`                  | Namespace labels selecting the Observer's namespace.                                                                                                                                                                   |
| `adapter.networkPolicy.observerPodLabels`       | `{}`                                                                             | Pod labels selecting the Observer Pod. Required when the policy is enabled.                                                                                                                                            |
| `adapter.networkPolicy.gatewayNamespaceLabels`  | `{}`                                                                             | Namespace labels selecting the Gateway data-plane that proxies the webhook.                                                                                                                                            |
| `adapter.networkPolicy.allowProbeIPBlock`       | `""`                                                                             | Optional CIDR allowed through ingress for liveness/readiness probes.                                                                                                                                                   |
| `adapter.resources`                             | `200m/256Mi limits, 50m/128Mi requests`                                          | Standard resource requests/limits.                                                                                                                                                                                     |

## Compatibility

> **Note:** The Helm chart versions specified in the installation
> commands above are for the latest module version compatible with the
> development version of OpenChoreo. Refer to the compatibility table
> below to determine the appropriate module version for your OpenChoreo
> installation.

| OpenChoreo Version | Module Version |
| ------------------ | -------------- |
| v1.1.x             | 0.1.x          |
