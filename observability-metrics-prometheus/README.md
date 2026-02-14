# Observability Metrics Module with Prometheus

This module collects and stores metrics using Prometheus.

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.

## Installation

Install this module in your OpenChoreo cluster using:

```bash
helm install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/charts/observability-metrics-prometheus \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0
```
