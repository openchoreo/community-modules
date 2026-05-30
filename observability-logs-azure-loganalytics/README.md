# observability-logs-azure-loganalytics

An OpenChoreo logs adapter backed by Azure Log Analytics (`ContainerLogV2`
populated by Azure Monitor Agent via the AKS Container Insights addon).

Status: Phase 1 — `Health` and `QueryLogs` implemented. Alert endpoints
return `500 Not Implemented`.

## Prerequisites

- AKS cluster with the **Container Insights** addon enabled and configured for
  the `ContainerLogV2` schema (set via the `container-azm-ms-agentconfig`
  ConfigMap in `kube-system` with `containerlog_schema_version = "v2"`).
- A Log Analytics workspace on the Analytics table plan (the default).
  `ContainerLogV2` on the Basic plan is not supported in Phase 1 — the
  adapter uses the official `azlogs` SDK which targets `/query`, and Basic
  tables require `/search`.
- For in-cluster deployment: AKS OIDC issuer + Workload Identity enabled,
  and a User-Assigned Managed Identity federated to the adapter's
  ServiceAccount with the `Log Analytics Reader` role on the workspace.
- For local development: an `az login` session and an Azure account with
  read access to the workspace.

## Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LOG_ANALYTICS_WORKSPACE_ID` | **yes** | — | Workspace `customerId` (GUID), not the ARM ID. |
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

| Method | Path | Status |
|--------|------|--------|
| `GET`  | `/health` | implemented |
| `POST` | `/api/v1/logs/query` | implemented |
| `POST` | `/api/v1alpha1/alerts/rules` | 500 Not Implemented (Phase 2) |
| `GET`  | `/api/v1alpha1/alerts/rules/{ruleName}` | 500 Not Implemented (Phase 2) |
| `PUT`  | `/api/v1alpha1/alerts/rules/{ruleName}` | 500 Not Implemented (Phase 2) |
| `DELETE` | `/api/v1alpha1/alerts/rules/{ruleName}` | 500 Not Implemented (Phase 2) |
| `POST` | `/api/v1alpha1/alerts/webhook` | 500 Not Implemented (Phase 2) |

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

## Not in Phase 1

- Alert rule CRUD and webhook handling (Phase 2 — Azure Monitor Scheduled
  Query Alert Rules and Action Group webhooks via the Common Alert Schema).
- Helm chart (Phase 3 — adapter Deployment, Service, ServiceAccount with
  Workload Identity annotation, HTTPRoute, NetworkPolicy).
- Support for Basic-plan `ContainerLogV2` (would require the `/search`
  endpoint, not exposed by the `azlogs` SDK).
- Fallback log shipper (this module assumes AMA via Container Insights;
  Fluent Bit support could be added if non-AKS deployment matters).
