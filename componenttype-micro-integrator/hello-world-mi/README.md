# Hello World MI

A minimal REST API that responds with `{"Hello": "World"}` — a good starting point for deploying WSO2 Micro Integrator projects on OpenChoreo.

**Source:** [wso2/choreo-samples — hello-world-mi](https://github.com/wso2/choreo-samples/tree/main/hello-world-mi)

## Endpoint

```
GET /HelloWorld  →  {"Hello": "World"}
```

## Prerequisites

- OpenChoreo cluster running with control plane, data plane, and workflow plane installed.
- The `deployment/micro-integrator` component type and `micro-integrator-builder` workflow applied from the parent directory:

```bash
kubectl apply \
  -f ../micro-integrator-build.yaml \
  -f ../micro-integrator-builder.yaml \
  -f ../micro-integrator.yaml
```

## Deploy

### Option A: Backstage Portal

1. Navigate to your project in the Backstage portal and click **Create Component** from the Project Overview.

2. Choose **Micro Integrator** from the component templates.

   ![Choose Micro Integrator template](./step-02-choose-template.png)

3. Complete the required fields in the create form: enter a **Component Name**, and optionally a display name and description.

   ![Create component form](./step-03-create-form.png)

4. Set the deployment source to **Build from Source**, select **wso2-micro-integrator** as the build workflow, then provide the Git repository URL, branch, and application path.
   - Repository URL: `https://github.com/wso2/choreo-samples`
   - Branch: `main`
   - Application path: `./micro-integrator/hello-world-mi`

   ![Build from source configuration](./step-04-build-source.png)

5. Review the provided information and click **Create**.

6. From the component overview page, click **Build**. Once the build succeeds, click **Deploy**.

---

### Option B: kubectl

#### 1. Create the Component and trigger a build

```bash
kubectl apply -f - <<EOF
apiVersion: openchoreo.dev/v1alpha1
kind: Component
metadata:
  name: hello-world-mi
  namespace: default
spec:
  owner:
    projectName: default
  componentType:
    kind: ClusterComponentType
    name: deployment/micro-integrator
  autoDeploy: true
  workflow:
    kind: ClusterWorkflow
    name: micro-integrator-builder
    parameters:
      repository:
        url: "https://github.com/wso2/choreo-samples"
        revision:
          branch: "main"
        appPath: "./micro-integrator/hello-world-mi"
---
apiVersion: openchoreo.dev/v1alpha1
kind: WorkflowRun
metadata:
  name: hello-world-mi-build-01
  labels:
    openchoreo.dev/project: "default"
    openchoreo.dev/component: "hello-world-mi"
spec:
  workflow:
    kind: ClusterWorkflow
    name: micro-integrator-builder
    parameters:
      repository:
        url: "https://github.com/wso2/choreo-samples"
        revision:
          branch: "main"
        appPath: "./micro-integrator/hello-world-mi"
EOF
```

#### 2. Watch the build

```bash
kubectl get workflow hello-world-mi-build-01 -n workflows-default --watch
```

The build runs four steps: `checkout-source` → `build-image` → `publish-image` → `generate-workload-cr`. The `build-image` step downloads Maven dependencies and pulls `wso2/wso2mi:4.4.0` — expect 3–5 minutes on first run.

#### 3. Verify the deployment

```bash
kubectl get deployment -A -l openchoreo.dev/component=hello-world-mi
```

#### 4. Get the URL and invoke

```bash
HOSTNAME=$(kubectl get releasebinding -n default \
  -l openchoreo.dev/component=hello-world-mi \
  -o jsonpath='{.items[0].status.endpoints[0].externalURLs.http.host}')

PATH_PREFIX=$(kubectl get releasebinding -n default \
  -l openchoreo.dev/component=hello-world-mi \
  -o jsonpath='{.items[0].status.endpoints[0].externalURLs.http.path}')

curl "http://${HOSTNAME}:19080${PATH_PREFIX}/HelloWorld"
```

Expected response:

```json
{ "Hello": "World" }
```

## Clean up

```bash
kubectl delete component hello-world-mi -n default
kubectl delete workload hello-world-mi-workload -n default
```
