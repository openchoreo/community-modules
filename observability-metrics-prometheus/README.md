# Observability Metrics Module with Prometheus

|               |           |
| ------------- |-----------|
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_metrics_prometheus)](https://codecov.io/gh/openchoreo/community-modules) |

This module collects and stores metrics using [Prometheus](https://prometheus.io).

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work.

## Installation

Install this module in your OpenChoreo cluster using:

```bash
helm install observability-metrics-prometheus \
  oci://ghcr.io/openchoreo/helm-charts/observability-metrics-prometheus \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.2.5
```
