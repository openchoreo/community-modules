# Observability Tracing Module for Azure Application Insights

This module collects distributed traces using [OpenTelemetry collector](https://opentelemetry.io) and stores them in [Azure Application Insights](https://learn.microsoft.com/azure/azure-monitor/app/app-insights-overview).

Spans are exported to a workspace-based Application Insights resource. An adapter implements the OpenChoreo Observability Tracing Adapter API and answers Observer trace queries by running KQL against the backing Log Analytics workspace.

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

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work. Deploy the `openchoreo-observability-plane` helm chart with the helm value `observer.tracingAdapter.enabled="true"` to enable the observer to fetch data from this tracing module.

### Azure prerequisites

1. A Log Analytics workspace.
2. A workspace-based Application Insights resource pointed at that workspace:

   ```bash
   az monitor app-insights component create \
     --app <name> --resource-group <rg> --location <region> \
     --workspace <workspace ARM ID>
   ```

3. A Kubernetes Secret with its connection string (used by the collector's `azuremonitor` exporter):

   ```bash
   kubectl create secret generic appinsights-conn \
     --namespace openchoreo-observability-plane \
     --from-literal=connection-string="$(az monitor app-insights component show \
        --app <name> -g <rg> --query connectionString -o tsv)"
   ```

4. An identity with the **Log Analytics Reader** role on the workspace, used by the adapter (query side). On AKS, use Workload Identity: a User-Assigned Managed Identity with a federated identity credential for the `tracing-adapter-azure-appinsights` ServiceAccount, and pass its client ID through `adapter.serviceAccount.annotations`.

## Installation

### Installation modes

This chart supports three `global.installationMode` values:

- **`singleCluster`**: Deploy the OpenTelemetry Collector and adapter into a single cluster. The collector receives OTLP, enriches spans with pod labels, and exports to Application Insights (uses when the dataplane and observability plane are in the same cluster).
- **`multiClusterReceiver`**: Deploy the OpenTelemetry Collector as a central receiver into the observability plane cluster. It accepts forwarded OTLP from remote clusters over a Gateway API HTTPRoute and exports to Application Insights.
- **`multiClusterExporter`**: Deploy the OpenTelemetry Collector as an exporter into each dataplane cluster. It receives OTLP from in-cluster workloads, enriches locally, and forwards OTLP to the receiver in the observability plane cluster.

#### Single-cluster topology

Install the chart into the observability plane cluster/namespace:

```bash
helm upgrade --install observability-tracing-azure-appinsights \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-azure-appinsights \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  --set logAnalytics.workspaceId="<workspace customerId GUID>" \
  --set adapter.serviceAccount.annotations."azure\.workload\.identity/client-id"="<uami-client-id>"
```

`logAnalytics.workspaceId` is the workspace `customerId` (a GUID), not the ARM resource ID. The collector reads the connection string from the `appinsights-conn` secret created in the prerequisites.

#### Multi-cluster topology

In multi-cluster mode you typically install:

- **Receiver (observability plane cluster)**: `global.installationMode=multiClusterReceiver`
- **Exporter (each dataplane cluster)**: `global.installationMode=multiClusterExporter`

##### 1) Install the receiver (observability plane cluster)

Install the chart in the observability plane cluster/namespace (the cluster that exports to Application Insights and serves Observer queries):

```bash
helm upgrade --install observability-tracing-azure-appinsights \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-azure-appinsights \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  --set global.installationMode="multiClusterReceiver" \
  --set logAnalytics.workspaceId="<workspace customerId GUID>" \
  --set adapter.serviceAccount.annotations."azure\.workload\.identity/client-id"="<uami-client-id>"
```

##### 2) Install an exporter (each dataplane cluster)

Install the chart in each dataplane cluster. The exporter does **not** export to Application Insights or run the adapter; it only forwards OTLP to the receiver.

Set `opentelemetryCollectorCustomizations.http.observabilityPlaneUrl` to the receiver endpoint (for example: `http://opentelemetry.<gateway-domain>:<port>`).
If the observability plane gateway is exposed over a TLS/HTTPS listener, use the `https://` scheme instead (for example: `https://opentelemetry.<gateway-domain>:<port>`).
Also set `opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost` if `observabilityPlaneUrl` differs from the gateway hostname.

```bash
helm upgrade --install observability-tracing-azure-appinsights \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-azure-appinsights \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  --set global.installationMode="multiClusterExporter" \
  --set adapter.enabled=false \
  --set-json opentelemetry-collector.extraEnvs="[]" \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneUrl="http://opentelemetry.<gateway-domain>:<port>" \
  --set opentelemetryCollectorCustomizations.http.observabilityPlaneVirtualHost="opentelemetry.<gateway-domain>"
```

## Adapter configuration

The adapter is configured through these environment variables (set by the Helm chart):

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `LOG_ANALYTICS_WORKSPACE_ID` | yes | — | workspace `customerId` (GUID) |
| `SERVER_PORT` | no | `9100` | listen port |
| `QUERY_TIMEOUT_SECONDS` | no | `30` | per-query KQL timeout |
| `LOG_LEVEL` | no | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR` |

Credentials come from `azidentity.NewDefaultAzureCredential`: Workload Identity in-cluster, the `az` CLI locally.

## Local development

```bash
az login
export LOG_ANALYTICS_WORKSPACE_ID=<customerId>
go run .
curl -s localhost:9100/healthz
```

## Behavior notes

- **Ingestion latency**: App Insights ingestion takes up to a few minutes; spans are not queryable immediately after being emitted.
- **Span kind fidelity**: App Insights stores spans in two tables; the adapter reports `SERVER` for `AppRequests` rows and `CLIENT` for `AppDependencies` rows. The original `CLIENT`/`PRODUCER`/`INTERNAL` distinction is not preserved by the exporter.
- **Root spans**: the `azuremonitor` exporter writes `ParentId == OperationId` for root spans; the adapter normalizes this to an empty `parentSpanId` in responses, matching the sibling adapters.
- **Durations**: App Insights stores `DurationMs` (float milliseconds); `durationNs` values are converted and carry no sub-100ns precision.
- **Attributes**: the exporter flattens resource and span attributes into one `Properties` bag; the adapter splits them back by key prefix (`openchoreo.dev/`, `k8s.`, `service.`, ... are reported as resource attributes).

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
|----------------|--------------------|
| v0.1.x         | v1.1.x             |
