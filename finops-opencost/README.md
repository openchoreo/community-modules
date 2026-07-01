# FinOps Module with OpenCost

This module provides FinOps capabilities for OpenChoreo using [OpenCost](https://opencost.io/), an open source cost monitoring tool for Kubernetes.

It bundles OpenCost together with an adapter — a Go service that the OpenChoreo Observer calls to retrieve per-component cost records and right-sizing recommendations. The adapter queries OpenCost's allocation API for cost data and the Observer's metrics API for resource usage.

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work.
- [Prometheus metrics module](https://github.com/openchoreo/community-modules/tree/main/observability-metrics-prometheus) must be installed to provide the metrics that OpenCost and the Observer consume.

## Deploy Helm chart

The chart deploys OpenCost and the cost insights adapter into the observability plane namespace:

```bash
helm upgrade --install finops-opencost \
  oci://ghcr.io/openchoreo/helm-charts/finops-opencost \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev
```

The default values configure the module to:

- Use the OpenChoreo observability plane Prometheus (`openchoreo-observability-prometheus` in the `openchoreo-observability-plane` namespace) as OpenCost's metrics source.
- Use `openchoreo` as the default cluster ID.
- Apply a custom pricing model.
- Expose OpenCost metrics via a `ServiceMonitor` for Prometheus scraping.
- Run the adapter, pointed at the in-cluster `opencost` and `observer` services.

> **Note:** To install OpenCost without the adapter (or vice versa), toggle `--set adapter.enabled=false` or `--set opencost.enabled=false`.

## Adapter

The adapter implements the [OpenChoreo Cost Insights Adapter API](https://raw.githubusercontent.com/openchoreo/openchoreo/refs/heads/main/openapi/finops-adapter-api.yaml) and listens on port `9101` (Service `finops-adapter`).


### Configuration

The adapter reads its configuration from environment variables, surfaced as `adapter.*` Helm values:

| Environment variable | Helm value | Default | Description |
| -------------------- | ---------- | ------- | ----------- |
| `SERVER_PORT` | `adapter.port` | `9101` | Port the adapter listens on. |
| `OPENCOST_URL` | `adapter.openCostUrl` | `http://opencost:9003` | OpenCost allocation API base URL. |
| `OBSERVER_URL` | `adapter.observerUrl` | `http://observer:8080` | Observer API base URL for the metrics callback. |
| `METRICS_STEP` | `adapter.metricsStep` | `5m` | Step for the Observer metrics query. |
| `RECOMMENDATION_CPU_PERCENTILE` | `adapter.recommendationCpuPercentile` | `95` | Usage percentile used to size the CPU request. |
| `RECOMMENDATION_MEMORY_PERCENTILE` | `adapter.recommendationMemoryPercentile` | `95` | Usage percentile used to size the memory request. |
| `RECOMMENDATION_CPU_HEADROOM` | `adapter.recommendationCpuHeadroom` | `0.2` | Fractional headroom added on top of the CPU percentile. |
| `RECOMMENDATION_MEMORY_HEADROOM` | `adapter.recommendationMemoryHeadroom` | `0.2` | Fractional headroom added on top of the memory percentile. |
| `RECOMMENDATION_CPU_MIN_REQUEST_MILLICORES` | `adapter.recommendationCpuMinRequestMillicores` | `1` | Floor for the recommended CPU request (millicores), so idle workloads are not sized to zero. |
| `RECOMMENDATION_MEMORY_MIN_REQUEST_MI` | `adapter.recommendationMemoryMinRequestMi` | `5` | Floor for the recommended memory request (mebibytes). |
| `LOG_LEVEL` | `adapter.logLevel` | `INFO` | Log level (`DEBUG`/`INFO`/`WARN`/`ERROR`). |

## Dependencies

Bundled upstream Helm charts:

| Chart | Repository |
| ----- | ---------- |
| opencost | https://opencost.github.io/opencost-helm-chart |

## Compatibility

> **Note:** The Helm chart version specified in the installation command above is for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
| -------------- | ------------------ |
| v0.1.x         | v1.1.x             |
