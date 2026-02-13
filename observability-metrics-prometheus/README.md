# Observability Metrics Module with Prometheus

This module collects and stores metrics using Prometheus.

## Installation

Install this module in your OpenChoreo cluster using:

```bash
helm install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/charts/observability-metrics-prometheus \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0
```
