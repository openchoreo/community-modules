# Observability Logs Module for OpenSearch

This module collects logs using Fluent Bit and stores them in OpenSearch.

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.

## Installation

```bash
helm install observability-logs-opensearch \
  oci://ghcr.io/openchoreo/charts/observability-logs-opensearch \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.2.1
```

> **Note:** If OpenSearch is already installed by another module (e.g., `observability-tracing-opensearch`), disable it to avoid conflicts:
>
> ```bash
> helm install observability-logs-opensearch \
>   oci://ghcr.io/openchoreo/charts/observability-logs-opensearch \
>   --create-namespace \
>   --namespace openchoreo-observability-plane \
>   --version 0.2.1 \
>   --set openSearch.enabled=false
> ```
