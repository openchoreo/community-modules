# Agent Sandbox Module for OpenChoreo

This module installs the [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) controller on the data plane, enabling kernel-level isolation for AI agent workloads deployed through OpenChoreo.

> **Important:** The sandbox controller must run on every cluster where agent
> workloads are scheduled. See [What lands where](#what-lands-where) for how the
> chart's control-plane and data-plane resources are split.

## What it does

- Installs the upstream `kubernetes-sigs/agent-sandbox` controller and CRDs on the target cluster via a Helm pre-install hook
- Grants the data plane `cluster-agent` service account permissions to manage `SandboxTemplate`, `SandboxClaim`, `SandboxWarmPool`, and `Sandbox` resources
- Registers the `ai-agent` ClusterComponentType (`proxy/ai-agent`) that renders sandbox resources via the standard OpenChoreo pipeline; this module provides the upstream controller that fulfills them on the data plane
- Bundles agent-specific ClusterComponentTypes with custom portal scaffolder templates (see below)

## Bundled agent component types

In addition to the generic `ai-agent` type, this module ships agent-specific
`ClusterComponentType`s that reference a fixed, pre-built agent image and a
tailored Backstage scaffolder template. Each such CCT carries a
`scaffolder.openchoreo.dev/backstage-template-url` annotation; when the portal
detects it, it fetches that custom template (which omits the irrelevant Build &
Deploy steps) instead of auto-generating one.

The `ClusterComponentType` ships in the Helm chart (`helm/templates/`) and is
applied to the cluster on install. The scaffolder template lives under
`agent-sandbox/templates/<name>.yaml` (outside the chart) and is served raw over
HTTP via the annotation URL.

| Component type | Template | Notes |
|---|---|---|
| `ai-agent-openclaw` | `templates/create-ai-agent-openclaw.yaml` | Multi-provider OpenClaw agent on a fixed image, always kata-isolated. Prompts for an LLM provider + model, injects the provider's API key env var, requires a gateway token (`OPENCLAW_GATEWAY_TOKEN`) to gate access to the Control UI, and mounts `openclaw.json` (via `OPENCLAW_CONFIG_PATH`) to set the default model — usable without `openclaw setup`. |
| `ai-agent-claude` | `templates/create-ai-agent-claude.yaml` | Claude Code CLI on the fixed [`docker/sandbox-templates:claude-code`](https://docs.docker.com/ai/sandboxes/agents/claude-code/) image, always kata-isolated. Prompts for an Anthropic model + API key; injects `ANTHROPIC_MODEL` and `ANTHROPIC_API_KEY`. |
| `ai-agent-codex` | `templates/create-ai-agent-codex.yaml` | OpenAI Codex CLI on the fixed [`docker/sandbox-templates:codex`](https://docs.docker.com/ai/sandboxes/agents/codex/) image, always kata-isolated. Prompts for an OpenAI model + API key; injects `OPENAI_MODEL` and `OPENAI_API_KEY`. |
| `ai-agent-gemini` | `templates/create-ai-agent-gemini.yaml` | Gemini CLI on the fixed [`docker/sandbox-templates:gemini`](https://docs.docker.com/ai/sandboxes/agents/gemini/) image, always kata-isolated. Prompts for a Google model + API key; injects `GEMINI_MODEL` and `GEMINI_API_KEY`. |
| `ai-agent-opencode` | `templates/create-ai-agent-opencode.yaml` | Multi-provider OpenCode CLI on the fixed [`docker/sandbox-templates:opencode`](https://docs.docker.com/ai/sandboxes/agents/opencode/) image, always kata-isolated. Prompts for an LLM provider (Anthropic/OpenAI/OpenRouter) + model, injects the provider's API key env var, and mounts `opencode.json` (via `OPENCODE_CONFIG`) to set the default model. |
| `ai-agent-cursor` | `templates/create-ai-agent-cursor.yaml` | Cursor CLI on the fixed [`docker/sandbox-templates:cursor-agent`](https://docs.docker.com/ai/sandboxes/agents/cursor/) image, always kata-isolated. Prompts for a Cursor API key; injects `CURSOR_API_KEY`. The model is selected at runtime (`cursor-agent --model`), so there's no model parameter. |
| `ai-agent-copilot` | `templates/create-ai-agent-copilot.yaml` | GitHub Copilot CLI on the fixed [`docker/sandbox-templates:copilot`](https://docs.docker.com/ai/sandboxes/agents/copilot/) image, always kata-isolated. Prompts for a model + a GitHub token (a user-owned fine-grained PAT with the "Copilot Requests" permission; requires an active Copilot subscription); injects `COPILOT_MODEL` and `COPILOT_GITHUB_TOKEN`. |

## Upstream CRDs installed

| CRD | API Group | Description |
|---|---|---|
| `Sandbox` | `agents.x-k8s.io` | Stateful pod with stable identity |
| `SandboxTemplate` | `extensions.agents.x-k8s.io` | Pod spec + isolation config |
| `SandboxClaim` | `extensions.agents.x-k8s.io` | Claims a sandbox from a template/pool |
| `SandboxWarmPool` | `extensions.agents.x-k8s.io` | Pre-warmed sandbox pool |

## Prerequisites

- OpenChoreo installed and running
- `kubectl` configured to point at the target cluster
- `helm` v3.16+

### Node requirements

Sandboxes are scheduled onto nodes by isolation tier. **The cluster must be
prepared before agents will run** — the chart installs the controller, but it does
not install container runtimes, register `RuntimeClass`es, or label nodes. Pods
requesting a tier with no matching node stay `Pending`.

| Isolation tier | Requires `RuntimeClass` | Requires node label |
|---|---|---|
| `runc` (default) | — | — |
| `gvisor` | `gvisor` | `gvisor-enabled=true` |
| `kata` | `kata-qemu` | `kata-enabled=true` |

The bundled agent component types (`ai-agent-openclaw`, `ai-agent-claude`,
`ai-agent-codex`, `ai-agent-gemini`, `ai-agent-opencode`, `ai-agent-cursor`,
`ai-agent-copilot`) are **always** kata-isolated and expose no
`isolationTier` parameter, so they require `kata-qemu` and `kata-enabled=true`
nodes. They also tolerate the taint `sandbox=true:NoSchedule`, so you may
optionally taint sandbox nodes to keep other workloads off them.

Label a prepared node with:

```bash
kubectl label node <node> kata-enabled=true
# optional: reserve the node for sandboxes
kubectl taint node <node> sandbox=true:NoSchedule
```

Installing the runtimes themselves (gVisor/`runsc`, Kata Containers) and their
`RuntimeClass` objects is out of scope for this module; follow the
[gVisor](https://gvisor.dev/docs/user_guide/quick_start/kubernetes/) or
[Kata](https://github.com/kata-containers/kata-containers/blob/main/docs/install/README.md)
install guides for your distribution.

## Installation

```bash
helm upgrade --install agent-sandbox \
  oci://ghcr.io/openchoreo/helm-charts/agent-sandbox \
  --version 0.0.0-latest-dev \
  --namespace openchoreo-control-plane \
  --wait --timeout 10m
```

### What lands where

The chart targets a **single cluster** and installs three distinct groups of resources on it:

| Resource | Scope | Lands in |
|---|---|---|
| Upstream controller + CRDs | Cluster + namespaced | `agent-sandbox-system` (created by the pre-install Job) |
| `ClusterComponentType`s (`ai-agent`, …) | Cluster-scoped | The cluster's OpenChoreo **control plane** API |
| `openchoreo-agent-sandbox-access` RBAC | Cluster-scoped | Bound to the **data plane** SA `cluster-agent-dataplane` |

Everything except the pre-install Job and its ServiceAccount is cluster-scoped.
The Job runs in the release namespace (the `--namespace` you pass), which must
already exist on the target cluster.

### Multi-cluster installs

The command above assumes the default **single-cluster** OpenChoreo install, where
both planes coexist and every resource lands correctly.

On a **split control-plane/data-plane** topology the chart's resources belong to
two different clusters: `ClusterComponentType`s are control-plane resources (as in
the `ai-wso2-agent-manager` module, whose component types install against the
control plane context), while the upstream sandbox controller must run wherever
agent pods are scheduled, alongside the RBAC that grants the data plane
cluster-agent access. Install the chart once per cluster, using the toggles to
select each half:

```bash
# Control plane — register the component types only
helm upgrade --install agent-sandbox \
  oci://ghcr.io/openchoreo/helm-charts/agent-sandbox \
  --version 0.0.0-latest-dev \
  --kube-context <control-plane-ctx> \
  --namespace openchoreo-control-plane \
  --set upstream.install=false \
  --set rbac.create=false

# Each data plane — install the sandbox controller and RBAC
helm upgrade --install agent-sandbox \
  oci://ghcr.io/openchoreo/helm-charts/agent-sandbox \
  --version 0.0.0-latest-dev \
  --kube-context <data-plane-ctx> \
  --namespace openchoreo-data-plane \
  --set componentTypes.enabled=false \
  --wait --timeout 10m
```

Repeat the data plane install for every cluster where agent workloads run.

## Verify

```bash
# Upstream controller running
kubectl get pods -n agent-sandbox-system

# CRDs registered
kubectl get crd | grep agents.x-k8s.io

# Component types registered with OpenChoreo
kubectl get clustercomponenttype | grep ai-agent

# RBAC applied
kubectl get clusterrole openchoreo-agent-sandbox-access
```

If the pre-install Job failed, inspect it with (the Job runs in the release
namespace — use the `--namespace` you installed with):

```bash
kubectl logs -n openchoreo-control-plane job/agent-sandbox-upstream-install
```

## Configuration

| Value | Default | Description |
|---|---|---|
| `dataPlaneNamespace` | `openchoreo-data-plane` | Data plane namespace (for RBAC binding) |
| `dataPlaneServiceAccount` | `cluster-agent-dataplane` | Data plane SA for RBAC |
| `componentTypes.enabled` | `true` | Register the `ClusterComponentType`s (control plane). Set to `false` on data-plane-only installs |
| `rbac.create` | `true` | Create the `openchoreo-agent-sandbox-access` RBAC (data plane). Set to `false` on control-plane-only installs |
| `upstream.install` | `true` | Install upstream controller via pre-install Job |
| `upstream.version` | `v0.4.6` | Upstream release version |
| `upstream.manifestURL` | `""` | Override core manifest URL (auto-built from version if empty) |
| `upstream.extensionsManifestURL` | `""` | Override extensions manifest URL (auto-built from version if empty) |

## Uninstall

```bash
helm uninstall agent-sandbox -n openchoreo-control-plane
```

This removes only the resources Helm tracks: the `ClusterComponentType`s and the
`openchoreo-agent-sandbox-access` RBAC. On a split topology there are two releases
— also uninstall the data plane one (`helm uninstall agent-sandbox -n
openchoreo-data-plane --kube-context <data-plane-ctx>`) on each data plane cluster.

> **Important:** The upstream controller is applied by a pre-install Job using
> `kubectl apply --server-side`, so **Helm does not track any of it**. The
> controller, its namespace, and the CRDs all survive `helm uninstall` and must be
> removed manually.

Delete any remaining `Sandbox`/`SandboxClaim` resources first, then:

```bash
# Upstream controller, its Deployments, Service, SA and namespace
kubectl delete namespace agent-sandbox-system

# Cluster-scoped RBAC created by the upstream manifests
kubectl delete clusterrole agent-sandbox-controller agent-sandbox-controller-extensions
kubectl delete clusterrolebinding agent-sandbox-controller agent-sandbox-controller-extensions

# CRDs (deleting these deletes all remaining sandbox resources)
kubectl delete crd sandboxes.agents.x-k8s.io
kubectl delete crd sandboxclaims.extensions.agents.x-k8s.io
kubectl delete crd sandboxtemplates.extensions.agents.x-k8s.io
kubectl delete crd sandboxwarmpools.extensions.agents.x-k8s.io
```

## Compatibility

> **Note:** The Helm chart version specified in the installation command above is for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
| -------------- | ------------------ |
| v0.1.x         | v1.x.x             |
