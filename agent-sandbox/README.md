# Agent Sandbox Module for OpenChoreo

This module extends OpenChoreo with kernel-level isolation and network egress controls for AI agent workloads. It introduces the `agent` ClusterComponentType which runs workloads inside hardened sandboxes — [runc](https://github.com/opencontainers/runc) (`runc`), [gVisor](https://gvisor.dev) (`gvisor`), or [Kata Containers](https://katacontainers.io) (`kata`) — and enforces outbound network policy via the `SandboxPolicy` CRD.

## How it works

The module ships two controllers that run alongside the OpenChoreo control plane:

- **agent controller** — watches `Component` resources whose `componentType` resolves to the `agent` ClusterComponentType. It drives the standard OpenChoreo release pipeline (creates `ComponentRelease` and `ReleaseBinding`) and wires the `SandboxPolicy` network label onto each workload.
- **policy controller** — watches `SandboxPolicy` resources and generates Kubernetes `NetworkPolicy` objects that enforce the declared egress rules on agent pods.

The module also installs the upstream [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) controller (via a pre-install Helm hook) which manages `Sandbox`, `SandboxClaim`, `SandboxTemplate`, and `SandboxWarmPool` resources on the data plane — providing stable pod identity and pre-warmed sandbox pools for fast agent startup.

### Isolation tiers

| Tier | Runtime | `runtimeClassName` | Use case |
|---|---|---|---|
| `runc` | runc | — (omitted) | Default — standard Linux namespace isolation |
| `gvisor` | gVisor | `gvisor` | Syscall interception via user-space kernel |
| `kata` | Kata Containers | `kata` | Full VM isolation via Firecracker/QEMU |

### What gets installed

| Resource | Description |
|---|---|
| `SandboxPolicy` CRD | Network egress policy CRD (`agent.openchoreo.dev/v1alpha1`) |
| `agent-sandbox-controller` Deployment | Module controller (agent + policy reconcilers) |
| `sandboxpolicies/development` | Default policy: wide egress allowed (PyPI, npm, GitHub, LLM APIs) |
| `sandboxpolicies/production` | Default policy: deny-all egress, empty allowedHosts |
| `SandboxTemplate/openchoreo-agent-default` | Default warm pool template (2 pre-warmed sandboxes) |
| `kubernetes-sigs/agent-sandbox` controller | Upstream controller installed via pre-install Job |

---

## Prerequisites

- OpenChoreo **v1.1.0 or later** installed and running
- `kubectl` configured to point at your cluster
- `helm` v3.16+
- For `gvisor` tier: gVisor RuntimeClass installed on data-plane nodes (`runtimeClassName: gvisor`)
- For `kata` tier: Kata Containers RuntimeClass installed on data-plane nodes (`runtimeClassName: kata`)

---

## Installation

### Step 1 — Add the Helm repository

```bash
helm repo add openchoreo-community https://openchoreo.github.io/community-modules
helm repo update openchoreo-community
```

### Step 2 — Install the module

The `namespace` value must match your OpenChoreo control plane namespace (default: `openchoreo-control-plane`).

```bash
helm upgrade --install agent-sandbox \
  openchoreo-community/agent-sandbox \
  --namespace openchoreo-control-plane \
  --wait --timeout 10m
```

This single command:
1. Applies the `SandboxPolicy` CRD (from `crds/`)
2. Runs a pre-install Job that installs `kubernetes-sigs/agent-sandbox` v0.4.3 on the cluster
3. Deploys the `agent-sandbox-controller`
4. Creates the `development` and `production` default `SandboxPolicy` resources
5. Registers the `agent` `ClusterComponentType` in OpenChoreo

### Step 3 — Verify

```bash
# Controller is running
kubectl get deployment agent-sandbox-controller -n openchoreo-control-plane

# Sentinel CRD is registered (core controller watches for this)
kubectl get crd sandboxpolicies.agent.openchoreo.dev

# Default policies are present
kubectl get sandboxpolicy -n openchoreo-control-plane

# Upstream agent-sandbox controller is running
kubectl get pods -n agents-system

# Upstream CRDs are registered
kubectl get crd | grep agents.x-k8s.io
```

Expected output:
```
NAME                          READY   UP-TO-DATE   AVAILABLE
agent-sandbox-controller      1/1     1            1

NAME                                    CREATED AT
sandboxpolicies.agent.openchoreo.dev    2025-...

NAME          DEFAULTEGRESS   AGE
development   allow           30s
production    deny            30s

NAME                                          READY   STATUS
agent-sandbox-controller-manager-xxxx-yyyy    1/1     Running

NAME
sandboxes.agents.x-k8s.io
sandboxclaims.agents.x-k8s.io
sandboxtemplates.agents.x-k8s.io
sandboxwarmpools.agents.x-k8s.io
```

---

## Deploy an agent Component

Once the module is installed, create a `Component` using the `agent` ClusterComponentType:

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Component
metadata:
  name: my-agent
  namespace: default
spec:
  owner:
    projectName: default

  componentType:
    kind: ClusterComponentType
    name: agent

  autoDeploy: true

  parameters:
    isolationTier: runc            # runc | gvisor | kata
    sandboxPolicyRef: production  # name of the SandboxPolicy to apply

  workflow:
    kind: ClusterWorkflow
    name: gcp-buildpacks-builder
    parameters:
      repository:
        url: "https://github.com/my-org/my-agent"
        revision:
          branch: "main"
        appPath: "/"
```

```bash
kubectl apply -f my-agent.yaml
```

Watch the reconciliation:

```bash
# Core delegates to module (Ready=True/DelegatedToModule then Ready=True/ComponentReleaseReady)
kubectl get component my-agent -n default -w

# Module creates ComponentRelease + ReleaseBinding
kubectl get componentrelease,releasebinding -n default

# Policy controller generates NetworkPolicy from the SandboxPolicy
kubectl get networkpolicy -n openchoreo-control-plane \
  -l agent.openchoreo.dev/sandbox-policy=production
```

---

## Define a custom SandboxPolicy

The `production` default policy denies all egress. Add entries for each external service your agent needs to reach:

```yaml
apiVersion: agent.openchoreo.dev/v1alpha1
kind: SandboxPolicy
metadata:
  name: my-agent-policy
  namespace: openchoreo-control-plane
spec:
  defaultEgress: deny

  allowedHosts:
    - host: api.openai.com
      ports: [443]
    - host: api.github.com
      ports: [443]
    - host: 10.0.0.0/8      # internal CIDR — uses NetworkPolicy IPBlock
      ports: [8080, 9090]

  allowedMCPServers:
    - url: https://mcp.example.com
      scopes: [read, write]
```

Reference the policy in your Component:

```yaml
parameters:
  sandboxPolicyRef: my-agent-policy
```

---

## Configuration reference

| Value | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/openchoreo/agent-sandbox-controller` | Controller image repository |
| `image.tag` | `0.1.0` | Controller image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `replicaCount` | `1` | Number of controller replicas |
| `namespace` | `openchoreo-control-plane` | Must match the OpenChoreo control plane namespace |
| `leaderElection` | `false` | Enable leader election for HA (set `replicaCount > 1`) |
| `resources.requests.cpu` | `100m` | CPU request |
| `resources.requests.memory` | `64Mi` | Memory request |
| `resources.limits.cpu` | `500m` | CPU limit |
| `resources.limits.memory` | `256Mi` | Memory limit |
| `installDefaultPolicies` | `true` | Create `development` and `production` SandboxPolicies |
| `installAgentClusterComponentType` | `true` | Apply the `agent` ClusterComponentType |
| `runtimeClasses` | `[gvisor, kata]` | Isolation tiers whose RuntimeClass is installed on data-plane nodes |
| `upstream.install` | `true` | Run pre-install Job to install `kubernetes-sigs/agent-sandbox` |
| `upstream.version` | `v0.4.3` | `kubernetes-sigs/agent-sandbox` release version |
| `upstream.manifestURL` | `""` | Override manifest URL (auto-built from `upstream.version` if empty) |

### Example: production cluster with Kata only

```yaml
# values-prod.yaml
namespace: openchoreo-control-plane
replicaCount: 2
leaderElection: true

runtimeClasses:
  - kata          # only kata nodes are provisioned in this cluster

upstream:
  install: true
  version: "v0.4.3"

installDefaultPolicies: true
```

```bash
helm upgrade --install agent-sandbox \
  openchoreo-community/agent-sandbox \
  --namespace openchoreo-control-plane \
  --values values-prod.yaml \
  --wait --timeout 10m
```

### Example: skip upstream controller (already installed separately)

```yaml
upstream:
  install: false
```

---

## Data plane node requirements

| Isolation tier | Node requirement | RuntimeClass name |
|---|---|---|
| `standard` | Any node | — |
| `gvisor` | gVisor installed ([setup guide](https://gvisor.dev/docs/user_guide/quick_start/kubernetes/)) | `gvisor` |
| `kata` | Kata Containers installed ([setup guide](https://katacontainers.io/docs/)) | `kata` |

For EKS, use `c5.metal` or `i3.metal` instance types for `kata` tier (hardware virtualisation required). Install Kata via the [kata-deploy DaemonSet](https://github.com/kata-containers/kata-containers/tree/main/tools/packaging/kata-deploy).

---

## Uninstall

```bash
helm uninstall agent-sandbox -n openchoreo-control-plane
```

> **Note:** Helm does not delete CRDs on uninstall to protect existing data. To fully remove the `SandboxPolicy` CRD and all policies:
>
> ```bash
> kubectl delete crd sandboxpolicies.agent.openchoreo.dev
> ```
>
> Any `Component` using the `agent` ClusterComponentType will revert to `Ready=False / ModuleNotInstalled` in the core controller until the module is reinstalled.

---

## Troubleshooting

### Component stuck at `Ready=False / ModuleNotInstalled`

The core controller checks for the `sandboxpolicies.agent.openchoreo.dev` CRD on every reconcile. If the CRD is missing, the Component waits.

```bash
# Check the CRD is present
kubectl get crd sandboxpolicies.agent.openchoreo.dev

# If missing, reinstall the module
helm upgrade --install agent-sandbox openchoreo-community/agent-sandbox \
  --namespace openchoreo-control-plane --wait
```

### Upstream pre-install Job failed

```bash
# Check Job status
kubectl get job agent-sandbox-upstream-install -n openchoreo-control-plane

# Read the Job logs
kubectl logs -n openchoreo-control-plane \
  -l job-name=agent-sandbox-upstream-install
```

Common cause: the cluster cannot reach `github.com` to fetch the upstream manifest. Use `upstream.manifestURL` to point at a pre-downloaded or mirrored copy:

```yaml
upstream:
  install: true
  manifestURL: "https://my-mirror.example.com/agent-sandbox-v0.4.3/manifest.yaml"
```

### NetworkPolicy not generated

```bash
# Check policy controller logs
kubectl logs -n openchoreo-control-plane \
  deployment/agent-sandbox-controller

# Confirm the SandboxPolicy exists
kubectl get sandboxpolicy <name> -n openchoreo-control-plane -o yaml

# Check the generated NetworkPolicy
kubectl get networkpolicy -n openchoreo-control-plane \
  -l agent.openchoreo.dev/sandbox-policy=<name>
```

### Controller logs

```bash
kubectl logs -n openchoreo-control-plane \
  deployment/agent-sandbox-controller --follow
```
