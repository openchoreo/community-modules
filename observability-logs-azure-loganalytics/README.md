# observability-logs-azure-loganalytics

An OpenChoreo logs adapter backed by Azure Log Analytics (`ContainerLogV2`
populated by Azure Monitor Agent via the AKS Container Insights addon).
Implements log queries, alert rule CRUD via Azure Monitor scheduled query
rules, and webhook ingress for alerts delivered through Action Groups in
the Common Alert Schema.

## Prerequisites

- AKS cluster with the **Container Insights** addon enabled and configured for
  the `ContainerLogV2` schema (set via the `container-azm-ms-agentconfig`
  ConfigMap in `kube-system` with `containerlog_schema_version = "v2"`).
- A Log Analytics workspace on the Analytics table plan (the default).
  `ContainerLogV2` on the Basic plan is not supported — the adapter uses
  the official `azlogs` SDK which targets `/query`, and Basic tables
  require `/search`.
- An Action Group in the same subscription with `useCommonAlertSchema=true`
  on its webhook receiver, pointed at this adapter's `/api/v1alpha1/alerts/webhook`.
- For in-cluster deployment: AKS OIDC issuer + Workload Identity enabled,
  and a User-Assigned Managed Identity federated to the adapter's
  ServiceAccount with `Log Analytics Reader` on the workspace and
  `Monitoring Contributor` on the resource group holding the scheduled
  query rules.
- For local development: an `az login` session and an Azure account with
  the same role grants.

## Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LOG_ANALYTICS_WORKSPACE_ID` | **yes** | — | Workspace `customerId` (GUID), not the ARM ID. |
| `AZURE_SUBSCRIPTION_ID` | **yes** | — | Subscription that hosts the scheduled query rules and Action Group. |
| `AZURE_RESOURCE_GROUP` | **yes** | — | Resource group that holds the rules and Action Group. |
| `WORKSPACE_RESOURCE_ID` | **yes** | — | Fully-qualified ARM ID of the Log Analytics workspace (used as the rule scope). |
| `ACTION_GROUP_ID` | **yes** | — | ARM ID of the Action Group that rules invoke when they fire. |
| `OBSERVER_URL` | **yes** | — | Observer base URL; fired alerts are forwarded to `${OBSERVER_URL}/api/v1alpha1/alerts/webhook`. |
| `WEBHOOK_SHARED_SECRET` | **yes** when `WEBHOOK_AUTH_ENABLED=true` | — | Bearer token compared in constant time against the `X-OpenChoreo-Webhook-Token` header or `?token=` query parameter. Min 16 bytes. |
| `WEBHOOK_AUTH_ENABLED` | no | `true` | Set to `false` to disable webhook auth (testing only). |
| `AZURE_REGION` | no | `eastus2` | Region for newly created rules. Must match the workspace region. |
| `DEFAULT_EVALUATION_FREQUENCY` | no | `PT5M` | ISO 8601 duration used when a request omits one. |
| `DEFAULT_WINDOW_SIZE` | no | `PT5M` | ISO 8601 duration used when a request omits one. |
| `SERVER_PORT` | no | `8080` | HTTP listener port. |
| `LOG_LEVEL` | no | `info` | `debug` \| `info` \| `warn` \| `error`. |
| `QUERY_TIMEOUT` | no | `30s` | Per-query timeout (Go duration string). |

Authentication uses `azidentity.DefaultAzureCredential`, which walks
environment variables, Workload Identity, Managed Identity, and the
Azure CLI session in that order. No extra config needed; the same binary
works locally with `az login` and in-cluster with Workload Identity.

## Local development

```bash
# 1. Sign in as a user with "Log Analytics Reader" on the workspace
az login

# 2. Set the workspace customerId (GUID) — example for the OpenChoreo R&D test cluster
export LOG_ANALYTICS_WORKSPACE_ID=fd571cb2-d6ff-4752-afd7-f5d23021a474
export LOG_LEVEL=debug

# 3. Build and run
make build
./bin/adapter

# 4. From another terminal:
curl -s http://localhost:8080/health
# {"status":"healthy"}

curl -s -X POST http://localhost:8080/api/v1/logs/query \
  -H 'Content-Type: application/json' \
  -d '{
    "startTime": "2026-05-26T08:00:00Z",
    "endTime":   "2026-05-26T09:00:00Z",
    "limit":     10,
    "searchScope": {
      "namespace": "openchoreo-control-plane"
    }
  }'
```

## Endpoints

| Method | Path |
|--------|------|
| `GET`  | `/health` |
| `POST` | `/api/v1/logs/query` |
| `POST` | `/api/v1alpha1/alerts/rules` |
| `GET`  | `/api/v1alpha1/alerts/rules/{ruleName}` |
| `PUT`  | `/api/v1alpha1/alerts/rules/{ruleName}` |
| `DELETE` | `/api/v1alpha1/alerts/rules/{ruleName}` |
| `POST` | `/api/v1alpha1/alerts/webhook` |

The OpenAPI contract is vendored from
https://openchoreo.dev/api-specs/observability-logs-adapter-api.yaml
and generated into `internal/api/gen/` with `oapi-codegen v2.5.1`.

## Make targets

```
make openapi-codegen   # re-generate internal/api/gen/* from the upstream spec
make build             # produce bin/adapter
make run               # build and run the binary
make unit-test         # go test ./... with coverage
```

## Pod labels expected on workloads

The adapter scopes queries by these labels, which OpenChoreo's rendering
pipeline adds to every workload pod:

- `openchoreo.dev/component-uid`
- `openchoreo.dev/project-uid`
- `openchoreo.dev/environment-uid`
- `openchoreo.dev/namespace`

These labels land in the `ContainerLogV2.PodLabels` JSON column. Queries
extract them with `tostring(PodLabels["openchoreo.dev/component-uid"])`.

## Workflow logs

Workflow pods are expected to live in namespaces prefixed with `workflows-`
(matching Argo Workflows convention as used by the OpenChoreo workflow
plane). When the request's `searchScope` is a `WorkflowSearchScope` with a
`workflowRunName`, the adapter queries
`PodNamespace == "workflows-" + namespace` and filters out the Argo infra
containers (`init`, `wait`).

## Not yet covered

- Helm chart (adapter Deployment, Service, ServiceAccount with Workload
  Identity annotation, HTTPRoute, NetworkPolicy).
- Support for Basic-plan `ContainerLogV2` (would require the `/search`
  endpoint, not exposed by the `azlogs` SDK).
- Fallback log shipper (this module assumes AMA via Container Insights;
  Fluent Bit support could be added if non-AKS deployment matters).
