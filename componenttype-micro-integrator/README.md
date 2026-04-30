# WSO2 Micro Integrator — OpenChoreo Platform Resources

This directory contains the OpenChoreo platform resources for building and deploying WSO2 Micro Integrator projects from source. Apply these once per cluster before deploying any MI component.

## Platform resources

| File | Kind | Description |
|---|---|---|
| `micro-integrator.yaml` | `ClusterComponentType` | Defines the `deployment/micro-integrator` component type |
| `micro-integrator-builder.yaml` | `ClusterWorkflow` | Build pipeline: checkout → build → publish → generate workload |
| `micro-integrator-build.yaml` | `ClusterWorkflowTemplate` | Argo template: compiles the Maven project and packages the `.car` into `wso2/wso2mi` |

### Install

```bash
kubectl apply \
  -f micro-integrator-build.yaml \
  -f micro-integrator-builder.yaml \
  -f micro-integrator.yaml
```

## How the build pipeline works

1. **checkout-source** — clones the Git repository at the specified branch/commit.
2. **build-image** — detects the MI version from `project.runtime.version` in `pom.xml` (defaults to `4.4.0`), runs `mvn clean install`, copies the `.car` into `wso2/wso2mi:<version>` from Docker Hub.
3. **publish-image** — pushes the image to the in-cluster registry.
4. **generate-workload** — creates a `Workload` CR with the built image.

### Supported MI versions

`1.1.0` · `1.2.0` · `4.0.0` · `4.1.0` · `4.2.0` · `4.3.0` · `4.4.0` · `4.5.0`

### Source repository requirements

- A `pom.xml` at the `appPath` root.
- Maven build must produce at least one `.car` file.
- Optionally place `libs/`, `dropins/`, or `deployment.toml` alongside `pom.xml` to customise the MI installation.

## Environment configuration

The `deployment/micro-integrator` component type exposes these per-environment settings:

| Parameter | Default | Description |
|---|---|---|
| `replicas` | `1` | Number of pod replicas |
| `resources.requests.cpu` | `500m` | CPU request |
| `resources.requests.memory` | `1Gi` | Memory request |
| `imagePullPolicy` | `IfNotPresent` | Kubernetes image pull policy |
| `mode` | `DEFAULT` | `DEFAULT` runs as a long-lived server; `TASK` runs a single CAR and exits |
| `carFile` | `""` | CAR file passed to `--car` when `mode=TASK` |

## Examples

| Directory | Description |
|---|---|
| [`hello-world-mi/`](./hello-world-mi/) | Simple REST API returning `{"Hello": "World"}` |
