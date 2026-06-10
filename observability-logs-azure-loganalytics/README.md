# Observability Logs Module for Azure Log Analytics

This module exposes Azure Log Analytics as an OpenChoreo logs backend. It
queries `ContainerLogV2` (populated by the Azure Monitor Agent through the
AKS Container Insights addon) and manages alert rules via Azure Monitor
`scheduledQueryRules` with delivery through pre-existing Action Groups.

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
7. [Shared webhook secret](#shared-webhook-secret)
8. [Troubleshooting](#troubleshooting)
9. [Configuration reference](#configuration-reference)
10. [Compatibility](#compatibility)

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

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/logs/query` | Runs a KQL query against `ContainerLogV2`, scoped by OpenChoreo namespace label plus optional component/project/environment UIDs. |
| `POST /api/v1alpha1/alerts/rules` | Creates an Azure Monitor scheduled query rule wired to the configured Action Group. |
| `GET /api/v1alpha1/alerts/rules/{ruleName}` | Looks the rule up by its `openchoreo-rule-name` tag. |
| `PUT /api/v1alpha1/alerts/rules/{ruleName}` | Updates the rule (CreateOrUpdate semantics). |
| `DELETE /api/v1alpha1/alerts/rules/{ruleName}` | Deletes the rule. |
| `POST /api/v1alpha1/alerts/webhook` | Receives Common Alert Schema payloads from the Action Group and forwards a normalised alert to the Observer. |
| `GET /health` | Readiness/liveness check. |

## Choose a deployment topology

Choose the deployment topology first, then choose the workload identity
model.

| Topology | Install location | Purpose | Required Helm values |
| --- | --- | --- | --- |
| Single cluster | The OpenChoreo cluster where the observability plane and workloads run together. | Deploys the adapter that queries the shared Log Analytics workspace and manages alert rules. | Defaults. |
| Observability plane cluster | The cluster where the OpenChoreo observability plane is installed. | Deploys only the adapter. | Defaults. |
| Data-plane / workflow-plane cluster | Each cluster that runs OpenChoreo workloads. | No install. Workload clusters write to Log Analytics via the AKS Container Insights addon directly. | N/A |

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

### Local tooling

Install the following tools on your machine:

- `helm`
- `kubectl`
- `az` CLI

### Azure prerequisites

You need:

- An Azure subscription and an Azure region (for example `eastus2`).
- An AKS cluster with:
  - **OIDC issuer** and **Workload Identity** enabled.
  - The **Azure Monitor metrics + Container Insights** addon enabled,
    configured to use the `ContainerLogV2` schema. Set
    `containerlog_schema_version = "v2"` in the
    `container-azm-ms-agentconfig` ConfigMap in `kube-system`, and
    enable pod label collection so the adapter can filter by
    `openchoreo.dev/*` labels (add the labels under
    `[log_collection_settings.metadata_collection]` →
    `include_fields`).
- A **Log Analytics workspace** on the **Analytics** table plan (the
  default). `ContainerLogV2` on the Basic plan is not supported — the
  adapter uses the `azlogs` SDK which targets `/query`, and Basic
  tables require `/search`.
- A pre-existing **Action Group** in the same subscription with a
  **Webhook** receiver pointed at the adapter's
  `/api/v1alpha1/alerts/webhook` endpoint and `useCommonAlertSchema=true`
  on that receiver. See [Log alerting](#log-alerting) for details.
- A **User-Assigned Managed Identity** federated to the adapter's
  ServiceAccount, with the role assignments described in
  [Azure role assignments](#azure-role-assignments).

## Azure role assignments

The adapter needs three role assignments on the User-Assigned Managed
Identity it runs as:

| Scope | Role | Why |
| --- | --- | --- |
| Log Analytics workspace | **Log Analytics Reader** | Run KQL queries against `ContainerLogV2`. |
| Resource group holding the rules | **Monitoring Contributor** | Create, update, delete, and list `scheduledQueryRules`. |
| Action Group | **Reader** | Boot-time `verifyActionGroup` reachability check. |

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

```bash
helm upgrade --install observability-logs-azure-loganalytics \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-azure-loganalytics \
  --namespace openchoreo-observability-plane --create-namespace \
  --version 0.1.2 \
  --set azure.subscriptionId="$AZURE_SUBSCRIPTION_ID" \
  --set azure.resourceGroup="$AZURE_RESOURCE_GROUP" \
  --set azure.region="$AZURE_REGION" \
  --set logAnalytics.workspaceId="$WORKSPACE_CUSTOMER_ID" \
  --set logAnalytics.workspaceResourceId="$WORKSPACE_ARM_ID" \
  --set actionGroup.id="$ACTION_GROUP_ARM_ID" \
  --set adapter.observerUrl="http://observer.openchoreo-observability-plane.svc.cluster.local:8080" \
  --set adapter.webhookAuth.sharedSecret="$WEBHOOK_TOKEN" \
  --set adapter.serviceAccount.annotations."azure\.workload\.identity/client-id"="$UAMI_CLIENT_ID"
```

The chart's `templates/validate.yaml` fails the install up front with a
readable message when any of these values are missing. Once the install
succeeds, the adapter boots, pings the workspace, and verifies the
Action Group is reachable. Either failure exits non-zero so the pod
crash-loops loudly instead of silently serving misconfigured queries.

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
query rules. The flow is:

1. The OpenChoreo alert controller POSTs an alert rule definition to the
   adapter (`/api/v1alpha1/alerts/rules`).
2. The adapter wraps the search phrase into a KQL `count` query against
   `ContainerLogV2`, then `CreateOrUpdate`s an Azure scheduled query
   rule scoped to the configured workspace, wired to the configured
   Action Group, and tagged with `openchoreo-rule-name` and
   `openchoreo-namespace` so the adapter can find the rule again on
   subsequent GET/DELETE calls.
3. When the rule fires, the Action Group POSTs a Common Alert Schema
   payload to the adapter's `/api/v1alpha1/alerts/webhook` endpoint.
4. The adapter validates the shared secret, parses the V2 payload
   (`alertContext.condition.allOf[].metricValue` and `searchQuery`,
   emitted by `scheduledQueryRules` API `2021-08-01` and later), and
   forwards a normalised alert to the Observer's webhook endpoint. The
   legacy V1 envelope from the `2018-04-16` API is not supported — the
   adapter only ever creates V2 rules.

The OpenChoreo identity round-trips via Azure's
`actions.customProperties` (`openchoreo-namespace`,
`openchoreo-rule-name`), which Common Alert Schema surfaces back as
`data.customProperties` in the firing payload. If those are missing,
the adapter falls back to parsing `essentials.alertRuleId`.

### Configure the Action Group webhook receiver

The Action Group ARM ID passed via `actionGroup.id` must already exist
and contain a `webhookReceivers` entry that:

- Has `useCommonAlertSchema: true`.
- Points its `serviceUri` at the adapter's webhook endpoint.

Azure's plain Webhook receiver schema has no `headers` field, so the
shared secret cannot ride in `X-OpenChoreo-Webhook-Token` directly from
the Action Group. Two options:

- **Direct webhook**: append the secret as a URL query parameter
  (`?token=...`). The adapter accepts the secret from either the header
  or the query parameter, which makes this work, but URLs get logged by
  intermediaries.
- **Logic App forwarder (recommended)**: front the adapter with a Logic
  App that pulls the secret from Key Vault and injects the
  `X-OpenChoreo-Webhook-Token` header. The Action Group's `serviceUri`
  points at the Logic App; the Logic App points at the adapter.

A **Secure Webhook** receiver (`useAadAuth: true`) avoids shared secrets
entirely by validating an Entra ID-issued OAuth token at the adapter.
That path requires registering an Entra ID app for the adapter and
running the
[`AzNS AAD Webhook`](https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/action-groups#configure-authentication-for-secure-webhook)
service principal grant script. The adapter does not validate AAD
tokens today; this would require additional work.

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

## Configuration reference

| Value | Default | Description |
| --- | --- | --- |
| `azure.subscriptionId` | Required | Subscription that hosts the scheduled query rules and Action Group. |
| `azure.resourceGroup` | Required | Resource group that holds the scheduled query rules. |
| `azure.region` | Required | Azure region for newly created rules. Must match the workspace region. |
| `logAnalytics.workspaceId` | Required | Workspace `customerId` (GUID), not the ARM ID. Used for the `/query` API. |
| `logAnalytics.workspaceResourceId` | Required | Full ARM ID of the Log Analytics workspace. Used as the rule scope. |
| `actionGroup.id` | Required | ARM ID of a pre-existing Action Group with a webhook receiver pointed at the adapter. |
| `adapter.enabled` | `true` | Toggle the adapter Deployment. |
| `adapter.replicas` | `1` | Adapter replica count. |
| `adapter.image.repository` | `ghcr.io/openchoreo/observability-logs-azure-loganalytics-adapter` | Adapter container image. |
| `adapter.image.tag` | Chart `appVersion` | Image tag. |
| `adapter.service.port` | `8080` | HTTP listener port. |
| `adapter.observerUrl` | `http://observer.openchoreo-observability-plane.svc.cluster.local:8080` | Observer base URL. Fired alerts are forwarded to `${observerUrl}/api/v1alpha1/alerts/webhook`. |
| `adapter.queryTimeoutSeconds` | `30` | Upper bound for a single Log Analytics query. |
| `adapter.logLevel` | `INFO` | `DEBUG` \| `INFO` \| `WARN` \| `ERROR`. |
| `adapter.alertRuleDefaults.evaluationFrequency` | `PT5M` | ISO 8601 duration used when an alert rule request omits one. |
| `adapter.alertRuleDefaults.windowSize` | `PT5M` | ISO 8601 duration used when an alert rule request omits one. |
| `adapter.serviceAccount.annotations` | `{}` | Annotations applied to the adapter ServiceAccount. Use `azure.workload.identity/client-id: <uami-client-id>` to bind a Managed Identity. |
| `adapter.webhookAuth.enabled` | `true` | Reject webhook calls without the shared secret. |
| `adapter.webhookAuth.sharedSecret` | `""` | Inline secret value. Chart creates a Secret; min 16 characters. |
| `adapter.webhookAuth.sharedSecretRef.name` | `""` | Reference an existing Secret instead of supplying the value inline. |
| `adapter.webhookAuth.sharedSecretRef.key` | `token` | Key inside the referenced Secret. |
| `adapter.webhookRoute.enabled` | `false` | Render a Gateway API HTTPRoute exposing only `/api/v1alpha1/alerts/webhook`. |
| `adapter.webhookRoute.parentRef.name` | `gateway-default` | Gateway to attach to. |
| `adapter.webhookRoute.parentRef.namespace` | `""` | Gateway namespace; defaults to the release namespace. |
| `adapter.webhookRoute.parentRef.sectionName` | `""` | Optional Gateway listener name. |
| `adapter.webhookRoute.hostnames` | `[]` | Optional hostnames matched at the route level. |
| `adapter.networkPolicy.enabled` | `false` | Render a NetworkPolicy restricting ingress to the adapter Pod. |
| `adapter.networkPolicy.observerNamespaceLabels` | `{kubernetes.io/metadata.name: openchoreo-observability-plane}` | Namespace labels selecting the Observer's namespace. |
| `adapter.networkPolicy.observerPodLabels` | `{}` | Pod labels selecting the Observer Pod. Required when the policy is enabled. |
| `adapter.networkPolicy.gatewayNamespaceLabels` | `{}` | Namespace labels selecting the Gateway data-plane that proxies the webhook. |
| `adapter.networkPolicy.allowProbeIPBlock` | `""` | Optional CIDR allowed through ingress for liveness/readiness probes. |
| `adapter.resources` | `200m/256Mi limits, 50m/128Mi requests` | Standard resource requests/limits. |

## Compatibility

> **Note:** The Helm chart versions specified in the installation
> commands above are for the latest module version compatible with the
> development version of OpenChoreo. Refer to the compatibility table
> below to determine the appropriate module version for your OpenChoreo
> installation.

| Module Version | OpenChoreo Version |
|----------------|--------------------|
| v0.1.x         | v1.1.x             |
