# OpenChoreo Observability Tracing Module — Azure Application Insights

Tracing adapter that implements the OpenChoreo Observability Tracing Adapter
API on top of Azure Application Insights. Spans are collected by an
OpenTelemetry Collector (contrib) and exported to a workspace-based
Application Insights resource; the adapter answers Observer trace queries by
running KQL against the backing Log Analytics workspace.

```
app (OTel SDK) --OTLP--> OTel Collector (contrib)
                           k8sattributes + tail_sampling
                           azuremonitor exporter (connection string)
                              |
                              v
                  Application Insights (workspace-based)
                    AppRequests      <- SERVER spans
                    AppDependencies  <- CLIENT/INTERNAL spans
                              ^
                              | KQL via azlogs + DefaultAzureCredential
                  tracing-adapter :9100  <--- Observer (TRACING_ADAPTER_URL)
```

Note: the `AppTraces` table is Azure's LOG table despite its name; this
module never reads it. Distributed-trace spans live in `AppRequests` and
`AppDependencies`.

## Prerequisites

1. A Log Analytics workspace.
2. A workspace-based Application Insights resource pointed at that workspace:

   ```bash
   az monitor app-insights component create \
     --app <name> --resource-group <rg> --location <region> \
     --workspace <workspace ARM ID>
   ```

3. A Kubernetes Secret with its connection string (collector side):

   ```bash
   kubectl create secret generic appinsights-conn \
     --from-literal=connection-string="$(az monitor app-insights component show \
        --app <name> -g <rg> --query connectionString -o tsv)"
   ```

4. For the adapter (query side), an identity with **Log Analytics Reader**
   on the workspace. On AKS, use Workload Identity: a User-Assigned Managed
   Identity with a federated identity credential for the
   `tracing-adapter-azure-appinsights` ServiceAccount, and pass its client
   ID via `adapter.serviceAccount.annotations`.

## Install

```bash
helm dependency build ./helm
helm install tracing-azure ./helm \
  --set logAnalytics.workspaceId=<workspace customerId GUID> \
  --set adapter.serviceAccount.annotations."azure\.workload\.identity/client-id"=<uami-client-id>
```

Wire the Observer:

```
TRACING_ADAPTER_ENABLED=true
TRACING_ADAPTER_URL=http://tracing-adapter.<namespace>.svc.cluster.local:9100
```

### Installation modes

| `global.installationMode` | Collector behavior |
|---|---|
| `singleCluster` (default) | receive OTLP, enrich with pod labels, export to App Insights |
| `multiClusterExporter` | data-plane collector: enrich locally, forward OTLP to the observability plane (`opentelemetryCollectorCustomizations.http.observabilityPlaneUrl`) |
| `multiClusterReceiver` | observability-plane collector: receive forwarded OTLP via Gateway API HTTPRoute, export to App Insights |

## Adapter configuration (environment variables)

| Var | Required | Default | Purpose |
|---|---|---|---|
| `LOG_ANALYTICS_WORKSPACE_ID` | yes | — | workspace customerId (GUID) |
| `SERVER_PORT` | no | `9100` | listen port |
| `QUERY_TIMEOUT_SECONDS` | no | `30` | per-query KQL timeout |
| `LOG_LEVEL` | no | `INFO` | DEBUG, INFO, WARN, ERROR |

Credentials come from `azidentity.NewDefaultAzureCredential`: Workload
Identity in-cluster, the az CLI locally.

## Local development

```bash
az login
export LOG_ANALYTICS_WORKSPACE_ID=<customerId>
go run .
curl -s localhost:9100/healthz
```

## Behavior notes

- **Ingestion latency**: App Insights ingestion takes up to a few minutes;
  spans are not queryable immediately after being emitted.
- **Span kind fidelity**: App Insights stores spans in two tables; the
  adapter reports `SERVER` for `AppRequests` rows and `CLIENT` for
  `AppDependencies` rows. The original CLIENT/PRODUCER/INTERNAL distinction
  is not preserved by the exporter.
- **Root spans**: the azuremonitor exporter writes `ParentId == OperationId`
  for root spans; the adapter normalizes this to an empty `parentSpanId` in
  responses, matching the sibling adapters.
- **Durations**: App Insights stores `DurationMs` (float milliseconds);
  `durationNs` values are converted and carry no sub-100ns precision.
- **Attributes**: the exporter flattens resource and span attributes into
  one `Properties` bag; the adapter splits them back by key prefix
  (`openchoreo.dev/`, `k8s.`, `service.`, ... are reported as resource
  attributes).
