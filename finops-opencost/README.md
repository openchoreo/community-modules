# FinOps Module with OpenCost

This module provides FinOps capabilities for OpenChoreo using [OpenCost](https://opencost.io/), an open source cost monitoring tool for Kubernetes.

This OpenCost based FinOps module integrates with OpenChoreo through the FinOps agent by making use of the Model Context Protocol (MCP).

## Prerequisites

- A running Kubernetes cluster
- [Helm](https://helm.sh/) v3+
- [OpenChoreo](https://openchoreo.dev) installed with the **observability plane** enabled (for Prometheus integration)
- An OpenChoreo metrics module - E.g. [Prometheus metrics module](https://github.com/openchoreo/community-modules/tree/main/observability-metrics-prometheus) 

## Installation

OpenCost is installed via its official Helm chart. For full documentation, refer to the [OpenCost Helm installation guide](https://opencost.io/docs/installation/helm).

### Add the OpenCost Helm repository

```bash
helm repo add opencost https://opencost.github.io/opencost-helm-chart
helm repo update
```

### Install OpenCost

```bash
helm upgrade --install opencost opencost/opencost \
  --namespace opencost \
  --create-namespace \
  --version 2.5.14 \
  -f values.yaml
```

## Sample values file

A sample [`values.yaml`](./values.yaml) is provided with this module. If you are following the [OpenChoreo try-it-out guide](https://openchoreo.dev/docs/category/try-it-out/), you can use this sample values file as-is to install OpenCost integrated with the OpenChoreo observability plane.

The sample values configure OpenCost to:

- Use the OpenChoreo observability plane Prometheus (`openchoreo-observability-prometheus` in the `openchoreo-observability-plane` namespace) as its metrics source.
- Use `k3d-local-cluster` as the default cluster ID (matching the try-it-out cluster).
- Apply a custom pricing model.
- Expose metrics via a `ServiceMonitor` for Prometheus scraping.
- Disable the OpenCost UI (the FinOps agent consumes data via MCP).

## Verification

Verify that OpenCost is running:

```bash
kubectl get pods -n openchoreo-observability-plane | grep opencost
```
