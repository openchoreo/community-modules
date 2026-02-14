# Observability Tracing Module for OpenSearch

This module collects traces using OpenTelemetry Collector and stores them in OpenSearch.

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.

## Installation

```bash
helm install observability-tracing-opensearch \
  oci://ghcr.io/openchoreo/charts/observability-tracing-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0
```

> **Note:** If OpenSearch is already installed by another module (e.g., `observability-logs-opensearch`), disable it to avoid conflicts:
>
> ```bash
> helm install observability-tracing-opensearch \
>   oci://ghcr.io/openchoreo/charts/observability-tracing-opensearch \
>   --create-namespace \
>   --namespace openchoreo-observability-plane \
>   --version 0.1.0 \
>   --set openSearch.enabled=false
> ```
