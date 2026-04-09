# GitOps Module for Flux CD

This module provides [Flux CD](https://fluxcd.io/) resource manifests for syncing OpenChoreo CRDs from a Git repository to the control plane cluster. Flux CD is a CNCF graduated project that keeps Kubernetes clusters in sync with configuration stored in Git repositories.

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [Repository Layout](#repository-layout)
- [Multi-Repository Setup](#multi-repository-setup)
- [Multi-Namespace Setup](#multi-namespace-setup)
- [Private Repositories](#private-repositories)
- [Verifying the Installation](#verifying-the-installation)
- [Monitoring](#monitoring)
- [Removing the Module](#removing-the-module)

---

## Overview

OpenChoreo is built with GitOps compatibility in mind. All control plane resources — Components, ReleaseBindings, Environments, DeploymentPipelines, and more — are declarative Kubernetes custom resources that can be stored in a Git repository and synced to the cluster using any GitOps tool.

This module provides the Flux CD resources needed to sync OpenChoreo CRDs from a Git repository to the control plane cluster.

### What Flux CD Provides

- **Automatic sync**: Pulls changes from Git and applies them to the cluster
- **Drift detection**: Detects and corrects configuration drift
- **Pruning**: Removes resources that are deleted from Git
- **Dependency ordering**: Ensures resources are applied in the correct order
- **Health assessment**: Reports sync status via Kustomization conditions

### Key Design Decisions

- **Control plane only**: Flux runs in the control plane cluster, not in data planes
- **Minimal footprint**: Only source-controller and kustomize-controller are required. Helm and notification controllers are not needed since OpenChoreo manages deployments through its own CRDs

---

## Prerequisites

- An existing OpenChoreo control plane cluster
- `kubectl` configured with cluster access

### Install Flux CD

Install Flux CD using the [official Helm chart](https://github.com/fluxcd-community/helm-charts). Only the source-controller and kustomize-controller are required for OpenChoreo:

```bash
helm install flux2 oci://ghcr.io/fluxcd-community/charts/flux2 \
  --version 2.14.1 \
  --namespace flux-system \
  --create-namespace \
  --set helmController.create=false \
  --set notificationController.create=false \
  --set imageAutomationController.create=false \
  --set imageReflectionController.create=false
```

---

## Installation

This module provides raw Kubernetes manifests that you customize and apply directly.

> [!NOTE]
> The manifests below assume a single-repository structure where both platform resources and developer resources are maintained in the same Git repository. For a working example, see the [sample-gitops](https://github.com/openchoreo/sample-gitops) repository. For other repository organization patterns, see the [GitOps documentation](https://openchoreo.dev/docs/next/category/gitops/).

### Step 1: Apply RBAC

Grant Flux's kustomize-controller permissions to manage OpenChoreo CRDs:

```bash
kubectl apply -f rbac.yaml
```

### Step 2: Configure the GitRepository

Edit [`gitrepository.yaml`](gitrepository.yaml) and update `spec.url` to point to your GitOps repository:

```yaml
spec:
  url: https://github.com/your-org/your-gitops-repo
  ref:
    branch: main
```

### Step 3: Configure Kustomization Paths

Edit the Kustomization files to match your repository structure. At minimum, update the `path`, `targetNamespace` fields in:

- [`namespaces-kustomization.yaml`](namespaces-kustomization.yaml) — set `path` (e.g., `./namespaces`)
- [`platform-shared-kustomization.yaml`](platform-shared-kustomization.yaml) — set `path` (e.g., `./platform-shared`)
- [`platform-kustomization.yaml`](platform-kustomization.yaml) — set `path` and `targetNamespace` (e.g., `./namespaces/default/platform`)
- [`projects-kustomization.yaml`](projects-kustomization.yaml) — set `path` and `targetNamespace` (e.g., `./namespaces/default/projects`)

The `sourceRef.name` in each Kustomization must match the `metadata.name` of your GitRepository resource.

### Step 4: Apply All Resources

```bash
kubectl apply -f gitrepository.yaml \
  -f namespaces-kustomization.yaml \
  -f platform-shared-kustomization.yaml \
  -f platform-kustomization.yaml \
  -f projects-kustomization.yaml
```

This creates the following resources:

| Resource                                | Purpose                                                             |
|-----------------------------------------|---------------------------------------------------------------------|
| **GitRepository** (`openchoreo-gitops`) | Monitors the Git repository for changes                             |
| **Kustomization** (`namespaces`)        | Syncs the `namespaces/` directory                                   |
| **Kustomization** (`platform-shared`)   | Syncs the `platform-shared/` directory                              |
| **Kustomization** (`platform`)          | Syncs platform resources; depends on namespaces and platform-shared |
| **Kustomization** (`projects`)          | Syncs application resources; depends on platform                    |

---

## Configuration

### Resources Provided

| File                                                                       | Resource                         | Description                                                                       |
|----------------------------------------------------------------------------|----------------------------------|-----------------------------------------------------------------------------------|
| [`rbac.yaml`](rbac.yaml)                                                   | ClusterRole + ClusterRoleBinding | Grants Flux permissions for OpenChoreo and Argo Workflow CRDs                     |
| [`gitrepository.yaml`](gitrepository.yaml)                                 | GitRepository                    | Flux source pointing to your GitOps repository                                    |
| [`namespaces-kustomization.yaml`](namespaces-kustomization.yaml)           | Kustomization                    | Syncs namespace definitions                                                       |
| [`platform-shared-kustomization.yaml`](platform-shared-kustomization.yaml) | Kustomization                    | Syncs cluster-scoped resources (ClusterComponentType, ClusterTrait, etc.)         |
| [`platform-kustomization.yaml`](platform-kustomization.yaml)               | Kustomization                    | Syncs namespace-scoped platform resources (Environment, DeploymentPipeline, etc.) |
| [`projects-kustomization.yaml`](projects-kustomization.yaml)               | Kustomization                    | Syncs application resources (Project, Component, ReleaseBinding, etc.)            |

### Common Customizations

**Change the polling interval** — edit `spec.interval` in `gitrepository.yaml`:

```yaml
spec:
  interval: 5m  # default: 1m
```

**Change the reconciliation interval** — edit `spec.interval` in the Kustomization files:

```yaml
spec:
  interval: 10m  # default: 5m
```

**Disable pruning** — set `spec.prune: false` in the Kustomization files to prevent Flux from deleting resources removed from Git.

---

## Repository Layout

This module works with the standard OpenChoreo GitOps repository layout. See the [GitOps overview](https://openchoreo.dev/docs/platform-engineer-guide/gitops/overview/) for full details.

### Mono Repository

```text
.
├── platform-shared/           # Cluster-scoped resources (ClusterComponentType, etc.)
├── namespaces/
│   └── <namespace>/
│       ├── namespace.yaml
│       ├── platform/          # Platform resources (Environment, etc.)
│       └── projects/          # Application resources (Component, etc.)
└── ...
```

---

## Multi-Repository Setup

Since each resource is a standalone manifest, supporting multiple Git repositories is straightforward — create additional GitRepository and Kustomization files for each repository.

For example, with separate platform and application repositories:

```yaml
# platform-gitrepository.yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: platform-config
  namespace: flux-system
spec:
  interval: 1m
  url: https://github.com/your-org/platform-config
  ref:
    branch: main
---
# apps-gitrepository.yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: team-apps
  namespace: flux-system
spec:
  interval: 1m
  url: https://github.com/your-org/team-apps
  ref:
    branch: main
```

Then create Kustomization resources that reference the appropriate GitRepository:

```yaml
# platform-shared from the platform-config repository
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: platform-shared
  namespace: flux-system
spec:
  interval: 5m
  path: ./platform-shared
  prune: true
  sourceRef:
    kind: GitRepository
    name: platform-config
---
# projects from the team-apps repository
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: projects
  namespace: flux-system
spec:
  interval: 5m
  path: ./namespaces/default/projects
  prune: true
  targetNamespace: default
  sourceRef:
    kind: GitRepository
    name: team-apps
  dependsOn:
    - name: platform-shared
```

---

## Multi-Namespace Setup

For multiple namespaces, duplicate the platform and projects Kustomization files for each namespace:

```yaml
# team-a-platform-kustomization.yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: team-a-platform
  namespace: flux-system
spec:
  interval: 5m
  path: ./namespaces/team-a/platform
  prune: true
  targetNamespace: team-a
  sourceRef:
    kind: GitRepository
    name: openchoreo-gitops
  dependsOn:
    - name: namespaces
    - name: platform-shared
---
# team-a-projects-kustomization.yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: team-a-projects
  namespace: flux-system
spec:
  interval: 5m
  path: ./namespaces/team-a/projects
  prune: true
  targetNamespace: team-a
  sourceRef:
    kind: GitRepository
    name: openchoreo-gitops
  dependsOn:
    - name: team-a-platform
```

---

## Private Repositories

OpenChoreo uses the External Secrets Operator to manage secrets. Store your Git credentials in OpenBao and use an `ExternalSecret` to generate the Kubernetes secret that Flux references. Refer to the [secret management guide](https://openchoreo.dev/docs/platform-engineer-guide/secret-management/) for more details.

### HTTPS with Personal Access Token

Add your credentials to OpenBao:

```bash
kubectl exec -it -n openbao openbao-0 -- \
    bao kv put secret/gitops-repo-credentials \
    username='git' \
    password='<personal-access-token>'
```

Create an `ExternalSecret` to generate the `gitops-repo-credentials` secret in the `flux-system` namespace:

```bash
kubectl apply -f - <<EOF
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: gitops-repo-credentials
  namespace: flux-system
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: gitops-repo-credentials
  data:
    - secretKey: username
      remoteRef:
        key: gitops-repo-credentials
        property: username
    - secretKey: password
      remoteRef:
        key: gitops-repo-credentials
        property: password
EOF
```

### SSH Key

Add your SSH key to OpenBao:

```bash
kubectl exec -it -n openbao openbao-0 -- \
    bao kv put secret/gitops-repo-credentials \
    identity=@./id_ed25519 \
    identity.pub=@./id_ed25519.pub \
    known_hosts=@./known_hosts
```

Create an `ExternalSecret` to generate the secret:

```bash
kubectl apply -f - <<EOF
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: gitops-repo-credentials
  namespace: flux-system
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: gitops-repo-credentials
  data:
    - secretKey: identity
      remoteRef:
        key: gitops-repo-credentials
        property: identity
    - secretKey: identity.pub
      remoteRef:
        key: gitops-repo-credentials
        property: identity.pub
    - secretKey: known_hosts
      remoteRef:
        key: gitops-repo-credentials
        property: known_hosts
EOF
```

When using SSH, set the Git URL to the SSH format:

```yaml
spec:
  url: ssh://git@github.com/your-org/your-repo.git
```

### Enable the Secret Reference

Uncomment the `secretRef` in `gitrepository.yaml`:

```yaml
spec:
  secretRef:
    name: gitops-repo-credentials
```

---

## Verifying the Installation

### Check Flux Controllers

```bash
# Verify Flux pods are running
kubectl get pods -n flux-system

# Check Flux controller versions
kubectl get deployments -n flux-system -o wide
```

### Check GitRepository Status

```bash
# Verify the Git source is synced
kubectl get gitrepository -n flux-system

# Get detailed status
kubectl describe gitrepository openchoreo-gitops -n flux-system
```

### Check Kustomization Status

```bash
# Verify all Kustomizations are ready
kubectl get kustomizations -n flux-system

# Get detailed status for a specific Kustomization
kubectl describe kustomization projects -n flux-system
```

### Check OpenChoreo Resources

```bash
# Verify OpenChoreo resources are synced from Git
kubectl get components -A
kubectl get releasebindings -A
kubectl get environments -A
```

### Trigger Immediate Sync

```bash
kubectl annotate gitrepository -n flux-system openchoreo-gitops \
  reconcile.fluxcd.io/requestedAt="$(date +%s)" --overwrite
```

---

## Monitoring

Flux CD provides built-in monitoring capabilities:

- **Kustomization conditions**: Each Kustomization reports `Ready` condition with sync status
- **Events**: Flux emits Kubernetes events for sync operations
- **Metrics**: Flux controllers expose Prometheus metrics on port 8080

For detailed monitoring setup, see the [Flux CD Monitoring documentation](https://fluxcd.io/flux/monitoring/).

### Common Status Checks

```bash
# Quick status overview
kubectl get kustomizations -n flux-system -o wide

# Check for reconciliation errors
kubectl get kustomizations -n flux-system \
  -o jsonpath='{range .items[*]}{.metadata.name}{": "}{.status.conditions[?(@.type=="Ready")].message}{"\n"}{end}'
```

---

## Removing the Module

Remove the Flux resources:

```bash
kubectl delete -f projects-kustomization.yaml \
  -f platform-kustomization.yaml \
  -f platform-shared-kustomization.yaml \
  -f namespaces-kustomization.yaml \
  -f gitrepository.yaml \
  -f rbac.yaml
```

> [!WARNING]
> Removing Flux Kustomization resources with `prune: true` will cause Flux to delete the managed OpenChoreo resources from the cluster. To keep the resources, set `prune: false` before deleting the Kustomizations, or delete only the GitRepository (which stops syncing without pruning).
