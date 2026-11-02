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
9. [Audit logs](#audit-logs)
10. [Shared webhook secret](#shared-webhook-secret)
11. [Troubleshooting](#troubleshooting)
12. [Configuration reference](#configuration-reference)
13. [Compatibility](#compatibility)

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
  `openchoreo.dev/environment-uid`, and on a release line's pods
  `openchoreo.dev/release-line-uid` and
  `openchoreo.dev/release-line-component-uid`)

A component scope covers the component's main line: pods with `openchoreo.dev/component-uid` and no `openchoreo.dev/release-line-uid`. `allReleaseLines` covers every line of the component, matching `openchoreo.dev/component-uid` or `openchoreo.dev/release-line-component-uid`, which release line pods carry instead. `releaseLineUid` covers one line, matched on `openchoreo.dev/release-line-uid` alone, and takes precedence. Component log and event queries and alert rules answer with `OpenChoreo-Release-Line-Applied: true`. The agent collects every pod label, so the release line labels need no extra configuration.

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
  oci://cr.openchoreo.dev/openchoreo/helm-charts/observability-logs-azure-loganalytics \
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

A rule without a release line evaluates its component's main line, and a rule with one evaluates that line alone. A `source.query` that is already a full KQL statement is sent as is, so it is not scoped to a line. A main line rule with one is created without `OpenChoreo-Release-Line-Applied`. A release line rule with one is rejected with `400`, so give a release line rule a search phrase instead.

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

The collector copies every label of the object by default. If its `enrichment.labels.include` list is narrowed, keep `openchoreo.dev/release-line-uid` and `openchoreo.dev/release-line-component-uid` in it, or a release line's events match no component scope.

Queries follow the adapter contract:

- **Component scope** matches the `openchoreo.dev/namespace` label and,
  when given, the project and environment UID labels and the component or
  release line, as described under [Architecture](#architecture). An
  event of a release line's object reports the component UID it carries
  as `openchoreo.dev/release-line-component-uid`.
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

## Audit logs

OpenChoreo's audit trail — who did what, from where, and whether it was
allowed — is written by `openchoreo-api` and `observer` to their container
logs. This adapter reads it back from a **custom table of its own**, not from
`ContainerLogV2`.

That separation is the whole point, and it is not stylistic. In Azure both
retention and read access are properties of a *table*:

- **Retention.** The trail is a compliance record, kept far longer than
  operational logs. Records sitting in `ContainerLogV2` inherit container-log
  retention, and one table cannot have two.
- **Access.** Anyone granted read on `ContainerLogV2` — every developer using
  the logs feature — would otherwise be able to read the full audit trail,
  including the `category: access` records about who read the audit trail.

Audit is off until `adapter.audit.workspaceId` is set. Until then both audit
endpoints answer `501`, which the Observer surfaces so the UI can say the
backend does not serve audit — as opposed to an empty page, which reads as
"nothing happened".

The audit table has no release line column yet. A query that filters on `resource.release_line`, and a filter-values request for `resource.release_line`, answer `501` rather than every line's records.

### Turning it on

Four steps. The sections after this are the reference detail behind them.

**1. Emit the records.** Both producers default to off:

```bash
helm upgrade ... --set openchoreoApi.config.audit.enabled=true   # control plane
helm upgrade ... --set observer.audit.enabled=true               # observability plane
```

**2. Create the table and route the records**, once per cluster that runs a
producer:

```bash
scripts/setup-audit.sh --cluster <aks-name> --resource-group <rg>           # dry run
scripts/setup-audit.sh --cluster <aks-name> --resource-group <rg> --apply
```

**3. Grant the adapter read** on the audit workspace, if it is not the workspace
the container logs already live in. Restricting *other* principals is
[Access](#access), and that is the part that makes the separation worth having.

**4. Point the adapter at the table:**

```bash
helm upgrade observability-logs-azure-loganalytics <chart> \
  --namespace openchoreo-observability-plane --reuse-values \
  --set adapter.audit.workspaceId=<workspace-customerId-guid> \
  --set adapter.audit.table=OpenChoreoAuditLog_CL
```

The adapter logs `audit table reachable` at boot when all four are done.

### The table

Create a custom table with the schema below, on the **Analytics** plan. The
adapter reads the table directly over whatever window the caller asks for, and
Basic caps a query at the last 30 days. For how the plans otherwise differ, see
[Query data in Basic and Auxiliary tables](https://learn.microsoft.com/en-us/azure/azure-monitor/logs/basic-logs-query).

| Column                                                                                | Type       | From                                |
| ------------------------------------------------------------------------------------- | ---------- | ----------------------------------- |
| `TimeGenerated`                                                                       | `datetime` | the record's `event_time`           |
| `EventTime`                                                                           | `datetime` | `event_time`, kept verbatim         |
| `CollectedTime`                                                                       | `datetime` | the collector's arrival time        |
| `SchemaVersion`, `EventId`, `Action`, `Category`, `Result`                            | `string`   | same-named record fields            |
| `RequestId`, `SourceIP`, `UserAgent`, `Producer`, `Surface`, `OperationId`            | `string`   | same-named record fields            |
| `ActorType`, `ActorId`, `ActorIssuer`, `ActorSessionId`                               | `string`   | the record's `actor` group          |
| `ActorEntitlements`                                                                   | `dynamic`  | `actor.entitlements`, as emitted    |
| `HttpMethod`, `HttpPath`                                                              | `string`   | the record's `http` group           |
| `ResourceType`, `ResourceNamespace`, `ResourceProject`                                | `string`   | the record's `resource` group       |
| `ResourceEnvironment`                                                                 | `string`   | `{namespace}/{name}` — see below    |
| `ResourceComponent`, `ResourceResource`, `ResourceUid`, `ResourceName`                | `string`   | the record's `resource` group       |
| `ResourceMetadata`, `Metadata`                                                        | `dynamic`  | the metadata objects                |
| `CollectorNamespace`, `CollectorPod`, `CollectorContainer`                            | `string`   | stamped by the collector            |
| `ClusterInstance`                                                                     | `string`   | a per-cluster literal               |
| `RawRecord`                                                                           | `string`   | the producer's line, as emitted     |

Four of these are load-bearing and easy to get wrong:

- **`TimeGenerated` must carry `event_time`,** not the arrival time. It is the
  table's partition column, so every query ranges on it; filling it with the
  collector's clock would make each audit query scan the whole retention window.
  Note that Azure **replaces** a `TimeGenerated` more than two days old (or more
  than a day ahead) with the arrival time, which a replay after a collector
  outage can trigger. `EventTime` is kept separately for exactly that reason:
  it is never rewritten, and `TimeGenerated != EventTime` identifies a record
  this happened to.
- **`ActorEntitlements` is stored as emitted**, the whole `actor.entitlements`
  object, with no flattened copy alongside it. Claim names vary by subject, so
  the adapter walks the object with `bag_keys` at query time rather than naming
  a key. Do not try to flatten it in the transform: transformations run per
  record and have neither `mv-expand` nor `bag_unpack`.
- **`ResourceEnvironment` is dual-scoped**: the contract's environment filter
  sends `{namespace}/{name}` (`acme/dev`), and a bare name does not match. The
  record already carries that form in `resource.environment`, so copy it rather
  than rebuilding it from the parts. Store a bare name and the column looks
  populated while the environment filter silently returns nothing.
- **`RawRecord`** is what `searchPhrase` scans, and it is the insurance against
  Azure's closed table schema — a field a later `schema_version` adds is dropped
  at ingestion rather than stored unindexed, and this is where it survives.

Azure reserves `Type`, `id`, `TenantId`, `UniqueId`, `Title`, `_ResourceId` and
`_SubscriptionId`, which is why `actor.type` and `resource.type` are stored
under prefixed names.

One command creates the table with its retention:

```bash
az monitor log-analytics workspace table create \
  --resource-group "$RG" --workspace-name "$WS" \
  --name OpenChoreoAuditLog_CL \
  --plan Analytics \
  --retention-time 365 --total-retention-time 365 \
  --columns \
    TimeGenerated=datetime EventTime=datetime CollectedTime=datetime \
    SchemaVersion=string EventId=string Action=string Category=string Result=string \
    RequestId=string SourceIP=string UserAgent=string Producer=string \
    Surface=string OperationId=string \
    ActorType=string ActorId=string ActorIssuer=string ActorSessionId=string \
    ActorEntitlements=dynamic \
    HttpMethod=string HttpPath=string \
    ResourceType=string ResourceNamespace=string ResourceEnvironment=string \
    ResourceProject=string ResourceComponent=string ResourceResource=string \
    ResourceUid=string ResourceName=string ResourceMetadata=dynamic \
    Metadata=dynamic \
    CollectorNamespace=string CollectorPod=string CollectorContainer=string \
    ClusterInstance=string RawRecord=string
```

The name must end in `_CL`, and `TimeGenerated` must be declared explicitly.

### Retention

The create above sets it. To change it later, on the table rather than the
workspace:

```bash
az monitor log-analytics workspace table update \
  --resource-group "$RG" --workspace-name "$WS" \
  --name OpenChoreoAuditLog_CL \
  --retention-time 365 --total-retention-time 365
```

`--retention-time` (4–730) is the window this adapter can serve.
`--total-retention-time` may go further, up to 12 years, but data past the
interactive window is reachable only through an asynchronous **search job** —
which needs `workspaces/tables/write`, does not support `summarize`, and is not
covered by granular RBAC. The adapter therefore never runs one. Treat anything
beyond interactive retention as an operator-run forensic retrieval, not as
something the audit UI can page through. The Azure CLI caps
`--total-retention-time` at 7 years; use the REST API for 12.

### Getting records into the table

Not in this chart's scope, the same as container logs. The records are stdout
lines from `openchoreo-api` and `observer` containing the marker `AUDIT-LOG`
followed by JSON, and something has to route them.

With Container Insights already collecting those namespaces, the least-moving-
parts option is a second data flow on the `Microsoft-ContainerLogV2` stream in
the cluster's data collection rule, with a `transformKql` that selects the audit
lines, parses them into the columns above and sends them to the audit table via
`outputStream: Custom-OpenChoreoAuditLog_CL`. Two things to get right:

- The default `ContainerLogV2` data flow needs the **complementary** filter, so
  audit lines leave `ContainerLogV2` rather than being copied into both. If the
  two drift you get silent duplication or silent loss.
- Match on `PodNamespace` and `ContainerName`, which the agent stamps from the
  kubelet, plus the marker. This is a **security boundary**, and what it rests on
  is set out under [What the producer filter does and does not
  guarantee](#what-the-producer-filter-does-and-does-not-guarantee).

`ClusterInstance` is a literal in each cluster's transform. `openchoreo-api` and
`observer` commonly run in different clusters, so every producer cluster needs
its own data flow, and an unlabelled record cannot be attributed afterwards.
Those clusters share one audit table only if they share one Log Analytics
workspace, for the reason under [Why not a separate
workspace](#why-not-a-separate-workspace). Producer clusters on separate
workspaces get a table each, which is not one audit trail.

#### Applying it

`scripts/setup-audit.sh` does all of this. It creates the table, finds the DCR,
builds both filters from one producer list and writes the data flows. Without
`--apply` it leaves the rule alone, though it still creates the table, since the
rule cannot reference a table that does not exist:

```bash
scripts/setup-audit.sh --cluster <aks-name> --resource-group <rg>
scripts/setup-audit.sh --cluster <aks-name> --resource-group <rg> --apply
```

Run it once per cluster that runs a producer, and check the workspace it prints
matches on every run. `--producer NS:CONTAINER` (repeatable) replaces the default
allowlist. Every run keeps its pre-edit rule under a timestamp in the work
directory, and the earliest of those is the rollback to the original.

It is operator-run rather than a chart Job deliberately: it needs rights to
create tables and edit data collection rules, and those are exactly the rights
that let a principal write into the audit table. Giving them to the module's own
identity would defeat the isolation.

What it does, and the decisions worth knowing about:

- Finds the DCR **by association** rather than by the `MSCI-<region>-<cluster>`
  name. The region in that name follows the workspace, not the cluster.
- Stops if the rule collects `Microsoft-ContainerInsights-Group-Default`.
  Transformations cannot attach to a group stream, and this is the step that is
  easy to miss on a cluster left on the default cost preset.
- **Subtracts** both container log stream names, `Microsoft-ContainerLogV2` and
  `Microsoft-ContainerLogV2-HighScale`, from the existing data flows rather than
  dropping those flows. Subtracting rather than dropping matters because a
  Container Insights DCR commonly lists every stream in one flow, so removing the
  entry would silently stop collecting `KubeEvents` and the inventory streams,
  including the events this same adapter serves. Taking both names matters on a
  re-run after high scale mode, where an earlier run's flows carry the old name
  and would otherwise survive beside the new ones.
- Generates the audit predicate **and its complement** from one producer list.
  Transformations have no `not()`, so the complement is De Morgan: each pair's
  `and` becomes an `or` of negations, joined with `and`. Deriving both is what
  stops them drifting.
- Matches on `PodNamespace` and `ContainerName` paired per producer. Two separate
  `in` lists would admit any container named `observer` in either namespace. Note
  the container is `api-server`; `openchoreo-api` is the producer name recorded in
  the record, not the container the kubelet reports.
- Matches the marker as `tostring(LogMessage.msg) == 'AUDIT-LOG'`. `LogMessage` is
  `dynamic`, so the record arrives already parsed and needs no `parse_json`, and
  the exact comparison separates a real record from the `AUDIT-LOG-RENDER-FAILED`
  line a producer emits when it cannot marshal one.

The transform itself is `scripts/audit-transform.kql`. Two things in it are easy
to reverse and silent when wrong: `CollectedTime` is captured **before**
`TimeGenerated` is overwritten, and `resource.environment` is already
`{namespace}/{name}` in the record so it is copied rather than rebuilt.

Audit records land in the audit table **only**. The two flows partition the
stream: each filter excludes exactly what the other selects, so nothing reaches
both `ContainerLogV2` and the audit table. The producers' ordinary application
logs are untouched and keep going to `ContainerLogV2`.

Confirm both halves, not just the first: audit records reaching the new table,
**and** none left behind in `ContainerLogV2`.

```kusto
OpenChoreoAuditLog_CL
| where TimeGenerated > ago(2h)
| extend Ingested = ingestion_time()
| project TimeGenerated, Ingested, Producer, Action, ActorId
| sort by Ingested desc
| take 10

ContainerLogV2
| where TimeGenerated > ago(2h)
| where (PodNamespace == 'openchoreo-control-plane' and ContainerName == 'api-server')
     or (PodNamespace == 'openchoreo-observability-plane' and ContainerName == 'observer')
| where tostring(LogMessage.msg) == 'AUDIT-LOG'
| count
```

The second must be zero. A non-zero count means the complementary filter does
not match the audit filter, and those records are being stored twice and billed
twice.

It matches the producer pairs and not the marker alone. A pod outside those
namespaces printing `AUDIT-LOG` is sent to `ContainerLogV2` deliberately, as
[What the producer filter does and does not
guarantee](#what-the-producer-filter-does-and-does-not-guarantee) describes, so
counting the marker on its own reports drift that is not there.

Keep the window generous. `TimeGenerated` carries `event_time`, but a record is
queryable only once ingestion completes, which took seven minutes on the table's
first write and under a minute after. A tight `ago()` window therefore returns
zero on a healthy pipeline, once the records' own timestamps fall outside it.

#### What the producer filter does and does not guarantee

`PodNamespace` and `ContainerName` come from the container runtime by way of the
kubelet, not from the log line. A workload cannot state its own namespace, so a
pod in `default` printing a byte-perfect audit record is excluded, and the
`Collector*` columns record which pod each stored record actually came from.
`ClusterInstance` is a literal in the transform rather than a field read from the
record, so it cannot be forged either. The transform lives in one cluster's DCR,
so a second cluster cannot write into the table until its own DCR is edited.

What the filter reduces to is **who can run a pod in the producer namespaces**.
The container name is chosen freely in a pod spec, so anyone able to create a pod
named `observer` in `openchoreo-observability-plane` can write into the audit
trail. That is a Kubernetes RBAC property of those two namespaces, not something
the transform can enforce, and it is the assumption worth checking before relying
on any of this.

Two things it does not address at all:

- **Anything running inside a genuine producer container** shares that
  container's stdout and can emit records that are indistinguishable from real
  ones. This is inherent to collecting the trail from container logs, and is
  equally true of the OpenSearch and OpenObserve modules.
- **The table is not write-protected by the transform.** Ingestion into
  `Custom-OpenChoreoAuditLog_CL` is reachable from any data collection rule that
  declares that output stream, by any principal holding `Monitoring Metrics
  Publisher` on such a rule. The filter guards the Container Insights path, not
  the table. Restricting who can create or edit data collection rules in the
  subscription is what closes that, and it belongs with the role assignments in
  [Access](#access).

### Access

The adapter's managed identity needs read on the audit workspace, in addition to
its existing grant on the logs workspace. If the audit table lives in the same
workspace, it already has it.

Isolating the trail is about **every other principal**, not the adapter: a
platform engineer, a CI service principal, another workload. Use a granular-RBAC
condition on `Microsoft.OperationalInsights/workspaces/tables/data/read`:

```
Microsoft.OperationalInsights/workspaces/tables:name StringEquals 'OpenChoreoAuditLog_CL'
```

#### Why not a separate workspace

Because the DCR split cannot reach one. Azure's rule is that [one stream can only
send to one Log Analytics workspace in a
DCR](https://learn.microsoft.com/en-us/azure/azure-monitor/data-collection/data-collection-rule-structure#destinations):
several `dataFlows` may share a stream only when they write to different tables
in the **same** workspace, which is exactly the shape
[Getting records into the table](#getting-records-into-the-table) builds. Sending
one stream to two workspaces needs a second DCR, and a second Container Insights
DCR on the same cluster would collect `ContainerLogV2` twice.

This costs less than it sounds. Retention is a property of the **table**, not the
workspace, so the retention half of the requirement is met either way. Only the
access half depends on the condition above.

The adapter itself is indifferent: `adapter.audit.workspaceId` may equal
`logAnalytics.workspaceId` or not, and nothing in the query path changes. Genuine
workspace separation needs a different ingestion path, under [Clusters on
separate workspaces](#clusters-on-separate-workspaces).

If you take the condition route, know that **Azure RBAC is additive with no
deny**: a principal holding a broader `*/read` on the same scope keeps full
access and the condition never binds. Audit the existing assignments before
relying on it, and watch `LAQueryLogs.ConditionalDataAccess`, which records
whether a query actually ran under a condition.

One role deliberately goes unassigned: `Data Purger`. Ingestion through a data
collection rule is append-only — there is no update or delete — so leaving
`Microsoft.OperationalInsights/workspaces/purge/action` unassigned is what makes
the trail tamper-evident. The adapter never needs it.

#### Clusters on separate workspaces

An observability plane serves platform logs from its own workspace, so it cannot
redirect its container logs to the audit workspace the way a control-plane or
data-plane cluster can. Splitting its Container Insights DCR therefore keeps its
audit records local, and several such clusters end up with a table each.

Azure's mechanism for this is [multitenant managed
logging](https://learn.microsoft.com/en-us/azure/azure-monitor/containers/container-insights-multitenant):
extra `ContainerLogV2Extension` DCRs, each covering a set of namespaces with its
own workspace destination and its own `transformKql`.

Routing is **either/or, not additive**. A namespace covered by an extension DCR
is processed by that DCR alone and stops reaching the default workspace, and
`disable_fallback_ingestion` governs only namespaces that are *not* covered. So
one extension DCR over `openchoreo-observability-plane` would take the observer's
ordinary application logs to the audit workspace along with its audit records,
out of the workspace this module serves platform logs from.

The arrangement that works is therefore two extension DCRs over the same
namespace, one transforming audit records into the audit workspace and one
carrying the complement back to the local one. The docs call two DCRs over one
namespace *multihoming* and describe it as sending the same logs to several
workspaces, but each DCR applies its own `transformKql` before routing, so
complementary filters partition the namespace rather than copying it.

Do the following on each observability plane.

**1. Enable [high scale
mode](https://learn.microsoft.com/en-us/azure/azure-monitor/containers/container-insights-high-scale)**,
on the addon and in the agent's ConfigMap. Both halves are needed; either alone
does nothing. An existing cluster has to be disabled and re-enabled, as there is
no automatic migration, and the audit data flows do not survive it.

```bash
az aks enable-addons -a monitoring -g <rg> -n <cluster> \
  --workspace-resource-id <workspace-resource-id> --enable-high-log-scale-mode
```

The same ConfigMap carries multitenancy. `schema-version` is mandatory: without
it the agent discards the whole map silently and neither setting takes effect.
`disable_fallback_ingestion` stays false so namespaces without an extension DCR
keep reaching the local workspace.

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: container-azm-ms-agentconfig
  namespace: kube-system
data:
  schema-version: v1
  config-version: ver1
  agent-settings: |-
    [agent_settings.high_log_scale]
      enabled = true
  log-data-collection-settings: |-
    [log_collection_settings]
      [log_collection_settings.multi_tenancy]
        enabled = true
        disable_fallback_ingestion = false
```

Apply it, then restart the agent, which reads the map only at container start:

```bash
kubectl apply -f container-azm-ms-agentconfig.yaml
kubectl -n kube-system rollout restart daemonset/ama-logs
```

Confirm both took, because a rejected map is silent:

```bash
POD=$(kubectl -n kube-system get pods -o name | grep -m1 'pod/ama-logs-[a-z0-9]*$' | cut -d/ -f2)
kubectl -n kube-system logs "$POD" -c ama-logs | grep -i 'schema version\|high log scale\|multi_tenancy'
```

Settings errors surface in the `KubeMonAgentEvents` table in the local workspace.

**2. Create a data collection endpoint**, which every extension DCR needs.

```bash
az monitor data-collection endpoint create \
  -g <rg> -n <dce-name> -l <region> --kind Linux --public-network-access Enabled
```

**3. Create two extension DCRs** over the producer namespace. The first sends
audit records to the audit workspace:

```json
{
  "location": "<region>",
  "kind": "Linux",
  "properties": {
    "dataCollectionEndpointId": "<dce-resource-id>",
    "dataSources": {
      "extensions": [{
        "name": "ContainerLogV2Extension",
        "extensionName": "ContainerLogV2Extension",
        "streams": ["Microsoft-ContainerLogV2-HighScale"],
        "extensionSettings": {
          "dataCollectionSettings": {
            "namespaces": ["openchoreo-observability-plane"]
          }
        }
      }]
    },
    "destinations": {
      "logAnalytics": [{
        "workspaceResourceId": "<audit-workspace-resource-id>",
        "name": "ciworkspace"
      }]
    },
    "dataFlows": [{
      "streams": ["Microsoft-ContainerLogV2-HighScale"],
      "destinations": ["ciworkspace"],
      "transformKql": "<scripts/audit-transform.kql, placeholders substituted>",
      "outputStream": "Custom-OpenChoreoAuditLog_CL"
    }]
  }
}
```

The second is the same document with the **local** workspace as its destination,
the complementary predicate as its whole transform, and no `outputStream`, so its
records land in the local `ContainerLogV2`:

```json
"transformKql": "source | where <complement of the audit predicate>"
```

Derive both predicates from one producer list rather than writing them
separately, the way `scripts/setup-audit.sh` does. Transformations have no
`not()`, so the complement is written out by De Morgan, and the two drift apart
if maintained by hand.

```bash
az monitor data-collection rule create -g <rg> -n <name> --rule-file <file>.json
```

**4. Associate both with the cluster.** A cluster supports 30 extension DCR
associations.

```bash
az monitor data-collection rule association create \
  --name <name>-dcra --resource <aks-resource-id> --rule-id <dcr-resource-id>
```

**5. Re-run `scripts/setup-audit.sh --apply --skip-preflight`** against the
cluster's default Container Insights DCR. High scale mode renames the stream to
`Microsoft-ContainerLogV2-HighScale`, and a rule still naming
`Microsoft-ContainerLogV2` duplicates data. The script reads the stream off the
cluster's extension and replaces flows under either name, so the re-run is the
whole fix. `--skip-preflight` is needed because the preflight looks for audit
records in `ContainerLogV2`, and once the flows exist they are not there.

The handover is not immediate. The agent refetches DCRs every five minutes from
container start, and until it does the namespace is still handled by the default
Container Insights DCR, so audit records emitted in that window land in the
cluster's own `ContainerLogV2`. Ingestion is append-only, so they cannot be moved
afterwards. Either accept that gap or keep the producers quiet until the agent has
the new rules, which `grep -l ContainerLogV2Extension
/etc/mdsd.d/config-cache/configchunks/*.json` inside the agent confirms.

On cost, the high scale mode documentation names no separate charge, ingestion is
billed as before, and the data collection endpoint it requires has no standalone
cost. That is the absence of a pricing statement rather than a documented
exemption, so confirm against current pricing before committing to it.

High scale mode is unavailable with an HTTP proxy using a trusted certificate,
through portal onboarding, through **Monitor Settings** in AKS Insights, and
through Bicep, Terraform or Azure Policy on Arc-enabled clusters. Where it is
unavailable, the remaining option is to stop relying on the Azure Monitor agent
for this signal and ship audit records with a collector of your own, such as
Fluent Bit or an OpenTelemetry collector, writing to the audit workspace
directly.

### Querying the table directly

```kusto
OpenChoreoAuditLog_CL
| where isnotempty(EventId)
| project TimeGenerated, EventTime, ActorId, Action, Result, ResourceProject
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

### Audit queries answer `501`

The adapter declines audit whenever it has no audit table to read. Either
`adapter.audit.workspaceId` is unset, or the table named by
`adapter.audit.table` does not exist in that workspace yet. The boot log says
which:

```
audit logs are not configured; audit queries will answer 501
audit table probe failed
```

The first always means `501`. The second means it only when the table is
missing. Nothing creates that table implicitly — `scripts/setup-audit.sh` makes
it before it touches ingestion, and the manual path creates it by hand — so a
missing table means setup has not run, or the configured name or workspace does
not match the one it made. A probe that failed on a timeout or a denied role
instead leaves audit queries answering `500`; the log line's `error` says which
it was.

### The audit table looks empty after setup

Check arrival before concluding anything is wrong:

```kusto
OpenChoreoAuditLog_CL
| extend Ingested = ingestion_time()
| summarize Records = count(), First = min(Ingested), Last = max(Ingested)
```

Records returned means ingestion works and the empty result came from the query
window. None means nothing has arrived, so confirm a producer emitted after the
data flow was applied, since a transformation runs at ingestion time only and
records emitted before the DCR was edited stay in `ContainerLogV2` for good.

```bash
kubectl -n openchoreo-control-plane logs <api-server-pod> -c api-server \
  --since=10m | grep AUDIT-LOG
```

The agent's own configuration is not where to look. Transformations and
`outputStream` are evaluated after the data leaves the cluster, so neither
appears in the AMA config chunks under `/etc/mdsd.d/config-cache/configchunks/`
even when routing works, and restarting `ama-logs` changes nothing.

### Audit queries return nothing, but the table is filling

The adapter drops rows with an empty `EventId`, and it skips records missing
`schema_version`, `actor.type`, `actor.id`, `result` or `event_time` — a record
with no actor would otherwise read as a real finding about an anonymous action.
A transform that writes rows without those fields therefore produces a table
that looks populated and queries that return nothing.

Check what actually landed:

```kusto
OpenChoreoAuditLog_CL
| summarize Total = count(), Usable = countif(isnotempty(EventId))
```

If `Usable` is far below `Total`, the DCR transform is matching lines it cannot
parse — most often because the marker filter is wider than intended. Adapter
logs name each skipped record with the field it was missing.

### Audit records have the wrong timestamps after an outage

Azure replaces a submitted `TimeGenerated` more than two days old with the
arrival time, so a collector replaying a long backlog writes records that sort by
when they were recovered rather than when they happened. The original survives in
`EventTime`:

```kusto
OpenChoreoAuditLog_CL
| where TimeGenerated != EventTime
| project EventTime, TimeGenerated, EventId
```

Nothing is lost, but paging and the timeline use `TimeGenerated` while the
record reports `EventTime`, and the two disagree for a clamped record. A caller
that resumes from the `event_time` it was last given sets a bound that is
compared against `TimeGenerated`, so it can skip the span between them. After a
long replay, re-read the affected window by `TimeGenerated` rather than paging
through it.

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
| `adapter.image.repository`                      | `cr.openchoreo.dev/openchoreo/observability-logs-azure-loganalytics-adapter`               | Adapter container image.                                                                                                                                                                                               |
| `adapter.image.tag`                             | Chart `appVersion`                                                               | Image tag.                                                                                                                                                                                                             |
| `adapter.service.port`                          | `8080`                                                                           | HTTP listener port.                                                                                                                                                                                                    |
| `adapter.observerUrl`                           | `http://observer-internal.openchoreo-observability-plane.svc.cluster.local:8081` | Observer base URL. Fired alerts are forwarded to `${observerUrl}/api/v1alpha1/alerts/webhook`. The alert-webhook endpoint lives on the Observer's internal service (`observer-internal:8081`), not the public `:8080`. |
| `adapter.queryTimeoutSeconds`                   | `30`                                                                             | Upper bound for a single Log Analytics query.                                                                                                                                                                          |
| `adapter.logLevel`                              | `INFO`                                                                           | `DEBUG` \| `INFO` \| `WARN` \| `ERROR`.                                                                                                                                                                                |
| `adapter.alertRuleDefaults.evaluationFrequency` | `PT5M`                                                                           | ISO 8601 duration used when an alert rule request omits one.                                                                                                                                                           |
| `adapter.alertRuleDefaults.windowSize`          | `PT5M`                                                                           | ISO 8601 duration used when an alert rule request omits one.                                                                                                                                                           |
| `adapter.events.table`                          | `OTelLogs`                                                                       | Table Kubernetes events are read from. Must be a plain table name.                                                                                                                                                     |
| `adapter.events.scopeName`                      | k8s events receiver scope                                                        | Instrumentation scope that marks a record in `adapter.events.table` as a Kubernetes event.                                                                                                                             |
| `adapter.audit.workspaceId`                     | `""`                                                                             | Workspace `customerId` (GUID) holding the audit table. Empty turns audit off and both audit endpoints answer `501`. May be the same GUID as `logAnalytics.workspaceId`.                                                |
| `adapter.audit.table`                           | `OpenChoreoAuditLog_CL`                                                          | Custom table audit records are read from. Must be a plain table name.                                                                                                                                                  |
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
