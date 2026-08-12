# Kubewarden Integration for OpenChoreo Data Plane

This integration enforces security policies on OpenChoreo workloads using
[Kubewarden](https://kubewarden.io/), with policies from
[Artifact Hub](https://artifacthub.io/packages/search?kind=13).

Kubewarden runs in the data plane and validates the Kubernetes resources OpenChoreo applies there.
When a policy rejects a resource, the deployment fails and the reason appears on the component's
`ReleaseBinding`.

## Features

- Enforce admission policies on the Kubernetes resources OpenChoreo deploys to the data plane
- Scope policies by namespace, project, environment, or a platform-engineer-defined label, using
  labels OpenChoreo already sets on cell namespaces
- Attach a baseline policy to a `ProjectType` so it is promoted between environments with the
  project

## Install

Install the Kubewarden admission controller into the cluster running the OpenChoreo **data plane**.

From version 1.37 Kubewarden ships as a single chart, `kubewarden/admission-controller`. The older
`kubewarden-controller`, `kubewarden-crds`, and `kubewarden-defaults` charts are still published but
stop at appVersion 1.36. Use the single chart.

```bash
helm repo add kubewarden https://charts.kubewarden.io
helm repo update kubewarden

helm upgrade --install kubewarden kubewarden/admission-controller \
  --namespace kubewarden --create-namespace \
  --wait
```

This installs the controller, a `PolicyServer` named `default`, and an audit scanner CronJob that
re-evaluates running workloads hourly. Confirm the policy server is running before creating
policies:

```bash
kubectl get policyservers
kubectl get pods -n kubewarden
```

## Scoping policies to OpenChoreo workloads

> [!WARNING]
> A `ClusterAdmissionPolicy` applies to the whole cluster unless you scope it. The data plane
> cluster runs more than your workloads: OpenChoreo's own components live in
> `openchoreo-data-plane`, and in a single-cluster setup such as the k3d quick start, the control
> plane, identity provider, and observability plane run there too. An unscoped policy governs all of
> them, and a restrictive one can stop OpenChoreo from working.

Scope every policy to the cell namespaces that hold your workloads.

Projects live in a namespace on the control plane. For each (namespace, project, environment),
OpenChoreo creates a **cell namespace** in the data plane and labels it:

| Label | Value |
| :---- | :---- |
| `openchoreo.dev/managed-by` | `renderedrelease-controller` |
| `openchoreo.dev/namespace` | Namespace the project belongs to |
| `openchoreo.dev/project` | Project name |
| `openchoreo.dev/environment` | Environment name |

A `ClusterAdmissionPolicy` selects cell namespaces with these labels, using `matchExpressions`.

After creating a policy, check that its webhook carries the selector you expect:

```bash
kubectl get validatingwebhookconfiguration clusterwide-<policy-name> \
  -o jsonpath='{.webhooks[0].namespaceSelector}'
```

Common scopes:

```yaml
# All OpenChoreo cell namespaces, every project and environment
namespaceSelector:
  matchExpressions:
    - key: openchoreo.dev/managed-by
      operator: In
      values: ["renderedrelease-controller"]
```

```yaml
# Production only
namespaceSelector:
  matchExpressions:
    - key: openchoreo.dev/managed-by
      operator: In
      values: ["renderedrelease-controller"]
    - key: openchoreo.dev/environment
      operator: In
      values: ["production"]
```

```yaml
# One project, all environments
namespaceSelector:
  matchExpressions:
    - key: openchoreo.dev/managed-by
      operator: In
      values: ["renderedrelease-controller"]
    - key: openchoreo.dev/namespace
      operator: In
      values: ["acme"]
    - key: openchoreo.dev/project
      operator: In
      values: ["checkout"]
```

`openchoreo.dev/project` holds the project *name*, which is unique within a namespace but not across
namespaces. Pair it with `openchoreo.dev/namespace` when more than one namespace is in use.

Keep `openchoreo.dev/managed-by` in every selector, so a policy only applies to namespaces
OpenChoreo created.

### Scoping by a custom label

Namespace, project, and environment do not always describe which policies a workload needs. Some
projects have to meet stricter rules than the rest of the platform, and they will not always share a
namespace or an environment.

The default `ClusterProjectType` merges `environmentConfigs.namespaceLabels` into the cell
namespace, so you can label the cells that need the stricter rules and scope a policy to that label.

Label the cells, per environment, on the `ProjectReleaseBinding`:

```yaml
spec:
  environmentConfigs:
    namespaceLabels:
      compliance-tier: strict
```

Then scope the policy to the label instead of an environment name:

```yaml
namespaceSelector:
  matchExpressions:
    - key: openchoreo.dev/managed-by
      operator: In
      values: ["renderedrelease-controller"]
    - key: compliance-tier
      operator: In
      values: ["strict"]
```

Because the label is set per binding, the same project can carry the stricter policy in production
and not in development.

## Applying policies directly

The [`policies/`](policies) directory has three examples, each wrapping a policy from Artifact Hub
and scoped to OpenChoreo cell namespaces. Edit the settings to match your own rules before applying.

| File | Enforces |
| :--- | :------- |
| [`no-latest-tag.yaml`](policies/no-latest-tag.yaml) | Rejects the mutable `latest` tag, all cell namespaces |
| [`trusted-registry.yaml`](policies/trusted-registry.yaml) | Restricts images to an approved registry list |
| [`production-only.yaml`](policies/production-only.yaml) | Same tag rule, scoped to the `production` environment only |

Each starts in `monitor` mode, where the policy evaluates every matching resource but never blocks
it.

```bash
kubectl apply -f policies/no-latest-tag.yaml

# A policy takes up to about a minute to become active while its module is pulled.
# It evaluates nothing during that window.
kubectl get clusteradmissionpolicy openchoreo-no-latest-tag -o wide
```

A policy only sees a workload when something is applied, so a component that is already running
produces nothing until its next deployment. To check what a new policy would do to workloads
already on the cluster, run the audit scanner instead of waiting for its hourly schedule:

```bash
kubectl create job -n kubewarden audit-now --from=cronjob/audit-scanner
kubectl wait --for=condition=complete job/audit-now -n kubewarden --timeout=5m
```

Read the results as described in [Where policy results appear](#where-policy-results-appear), then
switch to enforcement once only the workloads you intend to block are reported:

```bash
kubectl patch clusteradmissionpolicy openchoreo-no-latest-tag \
  --type=merge -p '{"spec":{"mode":"protect"}}'
```

Enforcement begins once the policy server finishes rolling a new pod, which takes a few moments
after the patch. Note that `protect` cannot be changed back to `monitor`; relaxing a policy means
deleting and recreating it.

## Delivering policies through a ProjectType

The policies above are `ClusterAdmissionPolicy` resources that a platform engineer applies directly
to the data plane. A second option is to declare a namespaced `AdmissionPolicy` inside a
`ProjectType`, so every project built on that type receives the policy in each of its cell
namespaces.

The two approaches serve different purposes and can be used together.

| | `ClusterAdmissionPolicy` | ProjectType `AdmissionPolicy` |
| :--- | :--- | :--- |
| Applies to | Any namespace, including ones OpenChoreo does not manage | Cell namespaces of projects using that ProjectType |
| Rollout | Applied directly, takes effect immediately | Cut as a new ProjectRelease, promoted per environment |
| Scoping | `namespaceSelector` | Inherent to the namespace, nothing to configure |

Use cluster-wide policies for platform guardrails that must hold everywhere, and ProjectType
policies for a baseline that belongs to the project definition and should move through environments
with it.

> [!IMPORTANT]
> `Project.spec.type` is immutable. An existing project cannot be moved to a different ProjectType,
> so to add a policy to projects that already exist you must edit the ProjectType they already
> reference. Applying a new ProjectType only affects projects created against it afterwards.

To use the ProjectType approach:

1. Grant the data-plane agent permission to manage Kubewarden policy resources. It has none by
   default, and without this the ProjectReleaseBinding reports an apply failure.

   ```bash
   kubectl apply -f cluster-agent-kubewarden-rbac.yaml
   ```

2. Add the policy to a ProjectType.

   **For projects that already exist**, add this resource to the `spec.resources` of the
   ProjectType they reference, alongside the existing `cell-namespace` entry:

   ```yaml
   - id: cell-baseline-policy
     template:
       apiVersion: policies.kubewarden.io/v1
       kind: AdmissionPolicy
       metadata:
         name: cell-trusted-registry
         namespace: ${metadata.namespace}
       spec:
         policyServer: default
         module: ghcr.io/kubewarden/policies/trusted-repos:v2.1.3
         mode: monitor
         mutating: false
         rules:
           - apiGroups: ["apps"]
             apiVersions: ["v1"]
             resources: ["deployments"]
             operations: ["CREATE", "UPDATE"]
         settings:
           registries:
             allow: ["ghcr.io"]
   ```

   **For new projects**, apply
   [`samples/projecttype-with-baseline-policy.yaml`](samples/projecttype-with-baseline-policy.yaml),
   a complete ProjectType that provisions the cell namespace and this policy, then set
   `spec.type` on new Projects to reference it.

   Either way, the Project controller produces a `ProjectRelease` for the updated ProjectType.

3. Point each `ProjectReleaseBinding` at the new `ProjectRelease` when you are ready for that
   environment to pick up the policy.

   ```bash
   kubectl get projectreleases -n <namespace>
   kubectl patch projectreleasebinding <project>-<environment> -n <namespace> \
     --type=merge -p '{"spec":{"projectRelease":"<new-release>"}}'
   ```

A namespaced `AdmissionPolicy` only evaluates resources in its own namespace, so it needs no
selector.

When removing this setup, revert the ProjectType and let the bindings finish cleaning up **before**
deleting the RBAC. If the agent loses `delete` permission while policies still exist, cleanup fails
silently and leaves an enforcing policy that OpenChoreo can no longer remove.

## What a rejection looks like

When a policy rejects a manifest, the apply fails and the message reaches the `ReleaseBinding` for
the affected component and environment:

```bash
kubectl get releasebinding <component>-<environment> -n <namespace> \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
```

```
Failed to apply resources to target plane: failed to apply resource
deployment-checkout-development-a43512ee: admission webhook
"clusterwide-openchoreo-no-latest-tag.kubewarden.admission" denied the request:
not allowed, reported errors: tags not allowed: latest
```

In the OpenChoreo portal the environment shows a failed state on the component's **Deploy** tab,
and **View details** shows the full message including the policy that rejected it. The **Runtime
Health** card stays green, because the previous version is still serving.

Two things to know about the resulting state:

- The rejected resource keeps its previous version, so a running workload continues to serve while
  a new deployment is blocked.
- Resources are applied in order and the apply stops at the first rejection, so resources earlier in
  the release may already be updated. Correct the violation and the next reconcile applies the
  release in full.

## Where policy results appear

A `protect` rejection reaches OpenChoreo, because the apply fails. A `monitor` violation does not,
because the apply succeeds. To see what a policy is flagging, read the audit scanner's reports on
the data plane:

```bash
kubectl get reports.openreports.io -A -o jsonpath=\
'{range .items[*]}{.scope.kind}/{.scope.name}{"\n"}{range .results[?(@.result=="fail")]}    {.properties.policy-name}: {.message}{"\n"}{end}{end}'
```

```text
Deployment/echo-websocket-service-development-a43512ee
    openchoreo-trusted-registry: not allowed, reported errors: registries not allowed: docker.io
```

The scanner rewrites these on each run, hourly by default, so reports lag a change by up to that
interval and there are none at all until the first run. Trigger a scan when you do not want to wait:

```bash
kubectl create job -n kubewarden audit-now --from=cronjob/audit-scanner
```

The policy server log has the individual admission decisions:

```bash
kubectl logs -n kubewarden -l kubewarden/policy-server=default
```

Reports cover every namespace on the cluster, including ones OpenChoreo does not manage, and reading
them needs data plane access rather than anything surfaced through OpenChoreo. Kubewarden also ships
[Policy Reporter](https://kyverno.github.io/policy-reporter/), a dashboard over the same reports,
which is not installed by default (`auditScanner.policyReporter`). The reports above carry
everything it displays, so it is optional. See the
[Kubewarden audit scanner documentation](https://docs.kubewarden.io/admission-controller/1.37/en/howtos/audit-scanner.html).

## Choosing policies

[Artifact Hub](https://artifacthub.io/packages/search?kind=13) hosts the Kubewarden policy library.
Useful starting points for OpenChoreo workloads:

| Policy | Enforces |
| :----- | :------- |
| [trusted-repos](https://artifacthub.io/packages/kubewarden/kubewarden-policy-library/trusted-repos) | Images come from approved registries, or reject mutable tags such as `latest` |
| [pod-privileged-policy](https://artifacthub.io/packages/kubewarden/kubewarden-policy-library/pod-privileged-policy) | No privileged containers |
| [host-namespaces-psp](https://artifacthub.io/packages/kubewarden/kubewarden-policy-library/host-namespaces-psp) | No `hostNetwork`, `hostPID`, or `hostIPC` |
| [hostpaths-psp](https://artifacthub.io/packages/kubewarden/kubewarden-policy-library/hostpaths-psp) | No `hostPath` volumes |
| [env-variable-secrets-scanner](https://artifacthub.io/packages/kubewarden/kubewarden-policy-library/env-variable-secrets-scanner) | No secrets pasted into environment variables |

These all target what a developer controls through a Workload: the image, and what the container
asks of the node.

> [!NOTE]
> The built-in `deployment/service` ComponentType sets no `securityContext`. A policy requiring
> `runAsNonRoot`, `allowPrivilegeEscalation: false`, or a seccomp profile therefore rejects every
> component built with it. Set those fields in the ComponentType, or use a mutating policy to add
> them.

## Uninstall

Delete the policies first, then the controller. Removing the chart while policies still exist leaves
webhooks that reject all matching requests.

If you used the ProjectType approach, revert the ProjectType and let the bindings finish cleaning up
first. The agent needs its RBAC grant to delete the policies it created, so removing that grant too
early strands them.

```bash
# 1. If used: remove the policy from your ProjectType and re-pin each binding to the
#    ProjectRelease that matches the updated type. An existing release is reused when the
#    content matches, so a new one does not always appear. Confirm the policies are gone:
kubectl get projectreleases -n <namespace>
kubectl get admissionpolicies.policies.kubewarden.io -A

# 2. Remove this integration's policies
kubectl delete clusteradmissionpolicies -l app.kubernetes.io/name=security-kubewarden

# 3. Remove the agent RBAC grant, if it was applied
kubectl delete -f cluster-agent-kubewarden-rbac.yaml --ignore-not-found

# 4. Remove Kubewarden
helm uninstall kubewarden -n kubewarden
kubectl delete namespace kubewarden
```

`helm uninstall` keeps the Kubewarden CRDs, so a later reinstall finds them already in place.

## Compatibility

| Component | Compatible version | Notes |
| :-------- | :----------------- | :---- |
| **Kubewarden** | `1.37.x` | Chart `kubewarden/admission-controller` 6.x. Earlier versions use three separate charts. |
| **OpenChoreo** | `1.2.x` | Verified against 1.2.2. Requires the `openchoreo.dev/*` labels on cell namespaces. |
