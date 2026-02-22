# Observability Logs Module for OpenObserve

This module collects container logs using Fluent Bit and stores them in OpenObserve.

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.

## Installation

Before installing, create Kubernetes Secrets with the OpenObserve admin credentials:

```bash
kubectl create secret generic openobserve-admin-credentials \
  --namespace openchoreo-observability-plane \
  --from-literal=username='root@example.com' \
  --from-literal=password='YOUR_PASSWORD'
```

Install this module in your OpenChoreo cluster using:

```bash
helm upgrade --install observability-logs-openobserve \
  oci://ghcr.io/openchoreo/charts/observability-logs-openobserve \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0
```

## Enable log collection

Enable Fluent Bit to start collecting logs from the cluster and publish to OpenObserve:

```bash
helm upgrade observability-logs-openobserve \
  oci://ghcr.io/openchoreo/charts/observability-logs-openobserve \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  --reuse-values \
  --set fluent-bit.enabled=true
```
