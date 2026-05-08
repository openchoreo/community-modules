# Agent Sandbox Module for OpenChoreo

This module installs the [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) controller on the data plane, enabling kernel-level isolation for AI agent workloads deployed through OpenChoreo.

> **Important:** This module must be installed on each data plane cluster where agent workloads will run. In a multi-cluster setup, install it on every data plane cluster separately.

## What it does

- Installs the upstream `kubernetes-sigs/agent-sandbox` controller and CRDs on the data plane cluster via a Helm pre-install hook
- Grants the data plane `cluster-agent` service account permissions to manage `SandboxTemplate`, `SandboxClaim`, `SandboxWarmPool`, and `Sandbox` resources
- The core OpenChoreo `agent` ClusterComponentType renders the sandbox resources; this module provides the upstream controller that fulfills them on the data plane

## Upstream CRDs installed

| CRD | API Group | Description |
|---|---|---|
| `Sandbox` | `agents.x-k8s.io` | Stateful pod with stable identity |
| `SandboxTemplate` | `extensions.agents.x-k8s.io` | Pod spec + isolation config |
| `SandboxClaim` | `extensions.agents.x-k8s.io` | Claims a sandbox from a template/pool |
| `SandboxWarmPool` | `extensions.agents.x-k8s.io` | Pre-warmed sandbox pool |

## Prerequisites

- OpenChoreo installed and running
- `kubectl` configured to point at the **data plane cluster**
- `helm` v3.16+

## Installation

Install on each data plane cluster:

```bash
helm repo add openchoreo-community https://openchoreo.github.io/community-modules
helm repo update openchoreo-community

# Point kubectl at your data plane cluster, then:
helm upgrade --install agent-sandbox \
  openchoreo-community/agent-sandbox \
  --namespace openchoreo-data-plane \
  --wait --timeout 10m
```

For multi-cluster setups, repeat for each data plane cluster:
```bash
# Switch context to each data plane cluster
kubectl config use-context <data-plane-cluster>

helm upgrade --install agent-sandbox \
  openchoreo-community/agent-sandbox \
  --namespace openchoreo-data-plane \
  --wait --timeout 10m
```

## Verify

```bash
# Upstream controller running on the data plane
kubectl get pods -n agent-sandbox-system

# CRDs registered
kubectl get crd | grep agents.x-k8s.io

# RBAC applied
kubectl get clusterrole openchoreo-agent-sandbox-access
```

## Configuration

| Value | Default | Description |
|---|---|---|
| `namespace` | `openchoreo-control-plane` | Namespace for the installer Job |
| `dataPlaneNamespace` | `openchoreo-data-plane` | Data plane namespace (for RBAC binding) |
| `dataPlaneServiceAccount` | `cluster-agent-dataplane` | Data plane SA for RBAC |
| `upstream.install` | `true` | Install upstream controller via pre-install Job |
| `upstream.version` | `v0.4.3` | Upstream release version |
| `upstream.manifestURL` | `""` | Override manifest URL (auto-built from version if empty) |

## Uninstall

```bash
helm uninstall agent-sandbox -n openchoreo-data-plane
```

Note: Helm does not delete CRDs on uninstall. To fully remove:
```bash
kubectl delete crd sandboxes.agents.x-k8s.io
kubectl delete crd sandboxclaims.extensions.agents.x-k8s.io
kubectl delete crd sandboxtemplates.extensions.agents.x-k8s.io
kubectl delete crd sandboxwarmpools.extensions.agents.x-k8s.io
```
