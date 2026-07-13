# WSO2 API Platform Gateway Module for OpenChoreo Data Plane

This document provides comprehensive documentation for integrating WSO2 API Platform for Kubernetes as the API management layer in the OpenChoreo data plane, running alongside the default kgateway (Envoy-based) implementation.

## Table of Contents

- [Overview](#overview)
- [High-Level Architecture](#high-level-architecture)
- [Installation](#installation)
- [Running at the Edge](#running-at-the-edge)
- [Connecting to WSO2 API Manager](#connecting-to-wso2-api-manager)
- [Configuration](#configuration)
- [Maintenance](#maintenance)
- [Customization](#customization)

---

## Overview

OpenChoreo uses the [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) as the standard API for exposing component endpoints to public or internal networks. The [WSO2 API Platform](https://github.com/wso2/api-platform) provides enterprise API management capabilities, including rate limiting, authentication, and API lifecycle management.

The WSO2 API Platform gateway can run in **two modes**:

- **Behind kgateway (default):** kgateway stays the Kubernetes Gateway API controller at the edge, and WSO2 API Platform sits behind it to provide API management capabilities like rate limiting, authentication, and API lifecycle management to applications running as OpenChoreo components. Traffic to web application components is routed directly to the applications; components that opt into the `api-management` trait are routed through the WSO2 API Platform Gateway for policy enforcement. This is the recommended setup and the one documented throughout most of this guide.

- **At the edge:** WSO2 API Platform Gateway runs as the Kubernetes Gateway API controller itself. The default kgateway is removed from the data plane and the WSO2 API Platform gateway terminates client traffic directly. Some endpoint types (such as gRPC and WebSocket) and certain TLS capabilities are not yet supported in this mode, due to the current state of WSO2 API Platform Kubernetes Gateway API implementation. See `Running at the Edge` section.

### Key Design Decisions

- **kgateway remains the Gateway API controller**: The default kgateway handles all `Gateway` and `HTTPRoute` resources. WSO2 API Platform does not replace kgateway, it sits behind it as an API management layer.
- **Traffic routing via kgateway Backend**: The trait creates a kgateway `Backend` (static type) pointing to the WSO2 API Platform router, and patches the HTTPRoute's `backendRef` to route through the WSO2 router instead of directly to the component's Service.
- **WSO2 RestApi CRD for API management**: The trait creates a `RestApi` resource that defines the API's context path, version, upstream, operations, and policies. WSO2's operator reconciles this into its internal gateway configuration.
- **No control plane changes required**: The rendering pipeline, endpoint resolution, and release controllers work unchanged. Only a ClusterTrait is added to inject the WSO2 resources.
- **Separate Helm installation**: The WSO2 API Platform operator and gateway are installed via their own Helm charts, independent of the OpenChoreo data plane chart.

---

## High-Level Architecture

![WSO2 API Platform Gateway Module — Architecture](wso2-api-platform-api-management.svg)

### Gateway Integration in OpenChoreo

```
┌─────────────────────────────────────────────────────────────┐
│                     CONTROL PLANE                           │
│                                                             │
│   Renders component templates and applies resources         │
│   (Deployment, Service, HTTPRoute, RestApi, Backend)        │
│   to the data plane                                         │
│                                                             │
└─────────────────────────┬───────────────────────────────────┘
                          │
                 applies resources
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                     DATA PLANE                              │
│                                                             │
│              ┌──────────────────────────────┐               │
│              │   Component Resources        │               │
│              │                              │               │
│              │   ┌────────────┐             │               │
│              │   │ Deployment │             │               │
│              │   └────────────┘             │               │
│              │   ┌────────────┐             │               │
│              │   │  Service   │             │               │
│              │   └─────┬──────┘             │               │
│              │         │ upstream           │               │
│              │   ┌─────┴──────┐             │               │
│              │   │  RestApi   │─────────────┼───────┐       │
│              │   │  (WSO2)    │             │       │       │
│              │   └────────────┘             │       │       │
│              │                              │       │       │
│              │   ┌────────────┐             │       │       │
│              │   │  Backend   │─────────┐   │       │       │
│              │   │(kgateway)  │         │   │       │       │
│              │   └────────────┘         │   │       │       │
│              │         ▲                │   │       │       │
│              │         │ backendRef     │   │       │       │
│              │   ┌─────┴──────┐         │   │       │       │
│              │   │ HTTPRoute  │         │   │       │       │
│              │   │ (patched)  │         │   │       │       │
│              │   │ parentRef ─┼────┐    │   │       │       │
│              │   └────────────┘    │    │   │       │       │
│              └──────────────────── ┼─── ┼───┘       │       │
│                                    │    │           │       │
│              ┌─────────────────────┴─── ┴─────────┐ │       │
│              │   Gateway CR                       │ │       │
│              │   name: gateway-default            │ │       │
│              │   gatewayClassName: kgateway       │ │       │
│              │                                    │ │       │
│              │   listeners:                       │ │       │
│              │     - http  (port 19080)           │ │       │
│              │     - https (port 19443, TLS)      │ │       │
│              └───────────────┬─────────────────── ┘ │       │
│                              │ watches              │       │
│              ┌───────────────┴───────────────────┐  │       │
│              │   kgateway Controller             │  │       │
│              │                                   │  │       │
│              │   - Watches Gateway, HTTPRoute    │  │       │
│              │   - Resolves Backend refs         │  │       │
│              │   - Routes to WSO2 router         │  │       │
│              └───────────────┬───────────────────┘  │       │
│                              │                      │       │
│                              ▼                      │       │
│              ┌───────────────────────────────────┐  │       │
│              │   WSO2 API Platform Router        │◄─┘       │
│              │                                   │          │
│              │   - Applies API policies          │          │
│              │   - Rate limiting                 │          │
│              │   - Authentication                │          │
│              │   - Routes to upstream Service    │◄─────────┘
│              └───────────────┬───────────────────┘
│                              │
│                         LoadBalancer
│                         :19080 (HTTP)
│                         :19443 (HTTPS)
└─────────────────────────────────────────────────────────────┘
                              │
                          Client Traffic
```

### Component Breakdown

| Component                      | Role                                                                                                                            |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------- |
| **kgateway Controller**        | Watches Gateway API resources (Gateway, HTTPRoute, Backend), routes traffic. Remains the Gateway API implementation             |
| **WSO2 API Platform Operator** | Watches `RestApi` and `APIGateway` (WSO2) CRDs, reconciles API configurations into the WSO2 router                              |
| **WSO2 API Platform Router**   | Processes API traffic, enforces policies (rate limiting, auth), and routes to upstream backend services                         |
| **Gateway CR**                 | Kubernetes Gateway API resource that defines listeners (ports, protocols, TLS). Managed by kgateway as usual                    |
| **HTTPRoute**                  | Gateway API route resource. Created by OpenChoreo release pipeline per component. Patched by the trait to route via WSO2 router |
| **Backend (kgateway)**         | kgateway-specific CRD pointing to the WSO2 API Platform router as a static upstream. Created by the trait                       |
| **RestApi (WSO2)**             | WSO2 API Platform CRD defining API context, version, upstream service, operations, and policies. Created by the trait           |

### How Endpoint URLs Are Resolved

The ReleaseBinding controller resolves endpoint URLs by inspecting rendered HTTPRoutes:

1. Extracts `backendRef` port from the HTTPRoute (matches to workload endpoint)
2. Extracts `hostname` from the HTTPRoute spec
3. Looks up the Gateway referenced in `parentRefs`
4. Resolves the HTTPS port from DataPlane/Environment gateway configuration
5. Constructs the invoke URL: `https://<hostname>[:<port>]/<path>`

This resolution is gateway-implementation-agnostic — it only reads standard Gateway API fields. The HTTPRoute's `backendRef` is replaced to point to the kgateway Backend (WSO2 router), but the URL resolution logic remains unchanged.

### Traffic Flow

```
Client
  │
  ▼
LoadBalancer (:19443)
  │
  ▼
kgateway (TLS termination)
  │
  ├─ Match HTTPRoute rules (hostname + path)
  ├─ Resolve Backend ref → WSO2 router
  │
  ▼
WSO2 API Platform Router (:8080)
  │
  ├─ Match RestApi context path
  ├─ Apply policies (rate limiting, auth)
  ├─ Route to upstream Service
  │
  ▼
Service (ClusterIP)
  │
  ▼
Pod (application container)
```

---

## Installation

### Prerequisites

- An existing OpenChoreo deployment with kgateway installed (default)
- Helm 3.x
- kubectl configured with cluster access

### Step 1: Install the WSO2 API Platform Operator

Install the WSO2 API Platform gateway operator using its Helm chart. The operator watches `RestApi` and `APIGateway` (WSO2) CRDs, and deploys the gateway components (router, policy engine) based on the `gateway.helm.*` values:

```bash
helm install api-platform-operator \
    oci://ghcr.io/wso2/api-platform/helm-charts/gateway-operator \
    --version 0.8.0 \
    --namespace openchoreo-data-plane \
    --set gatewayApi.installStandardCRDs=false
```

Wait for the operator pod to be ready:

```bash
kubectl wait --for=condition=ready pod \
  -l app.kubernetes.io/name=gateway-operator \
  -n openchoreo-data-plane \
  --timeout=300s
```

### Step 2: Apply the Gateway Configuration and Create the WSO2 APIGateway CR

The operator is now running but no gateway instance exists yet. First, apply the gateway configuration ConfigMap that defines the gateway's runtime settings (router, policy engine, TLS, logging, etc.):

```bash
kubectl apply -f gateway-configuration.yaml
```

Then, create an `APIGateway` CR (WSO2's CRD — `apigateways.gateway.api-platform.wso2.com`, not the Kubernetes Gateway API `Gateway`) to instruct the operator to deploy the gateway components (router, policy engine). The CR references the ConfigMap created above via `configRef`:

```bash
kubectl apply -n openchoreo-data-plane -f - <<EOF
apiVersion: gateway.api-platform.wso2.com/v1alpha1
kind: APIGateway
metadata:
  name: api-platform-default
spec:
  apiSelector:
    scope: Cluster

  infrastructure:
    replicas: 1
    resources:
      requests:
        cpu: "500m"
        memory: "1Gi"
      limits:
        cpu: "2"
        memory: "4Gi"

  storage:
    type: sqlite
  configRef:
    name: api-platform-operator-gateway-values
EOF
```

**APIGateway spec fields:**

| Field            | Required | Description                                                                                                          |
| ---------------- | -------- | -------------------------------------------------------------------------------------------------------------------- |
| `apiSelector`    | Yes      | Determines API selection strategy. `scope: Cluster` selects RestApis across all namespaces                           |
| `infrastructure` | No       | Deployment configuration: `replicas`, `resources`, `image`, `routerImage`, `nodeSelector`, `affinity`, `tolerations` |
| `storage`        | No       | Storage backend: `sqlite` (default), `postgres`, or `mysql`. For postgres/mysql, set `connectionSecretRef`           |
| `configRef`      | No       | References a ConfigMap (key `values.yaml`) with custom Helm values. Must match `gateway.configRefName` from Step 1   |
| `controlPlane`   | No       | Control plane connection settings (`host`, `tls`, `tokenSecretRef`) for managed deployments                          |

The operator reconciles this CR and deploys the gateway Helm chart (`oci://ghcr.io/wso2/api-platform/helm-charts/gateway` version `1.1.0`) with the referenced ConfigMap values.

Wait for the gateway pods to be ready:

```bash
kubectl wait --for=condition=ready pod \
  -l app.kubernetes.io/instance=api-platform-default-gateway \
  -n openchoreo-data-plane \
  --timeout=300s
```

### Step 3: Verify the Installation

Confirm all WSO2 API Platform components are operational:

```bash
kubectl get pods -n openchoreo-data-plane \
  --selector="app.kubernetes.io/instance=api-platform-default-gateway"
```

Expected pods:

| Pod                                              | Role                                                                                              |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------- |
| `api-platform-default-gateway-controller-*`      | WSO2 API Platform controller — manages API configurations, xDS server, REST API                   |
| `api-platform-default-gateway-gateway-runtime-*` | WSO2 API Platform gateway runtime — combines the router (Envoy) and policy engine in a single pod |

### Step 4: Grant RBAC for WSO2 API Platform CRDs

The data plane service account needs permissions to manage WSO2 API Platform and kgateway Backend resources. Create a dedicated ClusterRole and bind it to the data plane service account:

```bash
kubectl apply -f - <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: wso2-api-platform-gateway-module
rules:
  - apiGroups: ["gateway.api-platform.wso2.com"]
    resources: ["restapis", "apigateways"]
    verbs: ["*"]
  - apiGroups: ["gateway.kgateway.dev"]
    resources: ["backends"]
    verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: wso2-api-platform-gateway-module
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: wso2-api-platform-gateway-module
subjects:
  - kind: ServiceAccount
    name: cluster-agent-dataplane
    namespace: openchoreo-data-plane
EOF
```

> **Note:** Without these permissions, the Release controller will fail to apply RestApi and Backend resources to the data plane with a "forbidden" error. To remove these permissions later, simply delete the ClusterRole and ClusterRoleBinding:
>
> ```bash
> kubectl delete clusterrole wso2-api-platform-gateway-module
> kubectl delete clusterrolebinding wso2-api-platform-gateway-module
> ```

### Step 5: Deploy and Invoke the Greeting Service

Deploy the sample greeting service to verify end-to-end traffic flow through WSO2 API Platform, including the `api-management` ClusterTrait.

**Apply the ClusterTrait:**

```bash
kubectl apply -f wso2-api-platform-api-configuration-trait.yaml
```

**Update the ClusterComponentType to allow the trait:**

The `api-management` trait must be listed in the ComponentType's `allowedTraits` before components can use it. Patch the `ClusterComponentType/deployment/service` (or whichever ComponentType your components use) to add the trait:

```bash
kubectl patch clustercomponenttype service --type='json' -p='[
  {"op": "add", "path": "/spec/allowedTraits/-", "value": {"name": "api-management", "kind": "ClusterTrait"}}
]'
```

Alternatively, edit the ComponentType YAML directly and re-apply it:

```yaml
spec:
  allowedTraits:
    - name: api-configuration
    - name: observability-alert-rule
    - name: api-management # Add this line
      kind: ClusterTrait # Required since it's a ClusterTrait
```

> **Note:** Without this entry, the Component webhook will reject any Component that references the `api-management` trait with a validation error.

**Apply the Component and Workload:**

```bash
kubectl apply -f component.yaml
```

> **Note:** The greeting service Component uses `componentType: ClusterComponentType/deployment/service` and attaches the `api-management` ClusterTrait. The trait automatically derives all API values (displayName, context, version, upstream URL, operations) from the component metadata and workload endpoints. Optional policies (JWT auth, rate limiting, custom headers) can be enabled via trait parameters.

**Wait for the deployment to roll out:**

```bash
# Check that the release pipeline has completed
kubectl get componentrelease

# Check the release status
kubectl get release

# Wait for the greeting pod to be ready
kubectl get pods -A

# Verify RestApi resources are created
kubectl get restapi -A

# Verify Backend resources are created
kubectl get backend.gateway.kgateway.dev -A
```

**Invoke the greeting service through the WSO2 API Platform:**

```bash
curl http://development-default.openchoreoapis.localhost:19080/greeting-service-http/greet?name=OpenChoreo -v
```

Expected response:

```
Hello, OpenChoreo!
```

**Cleanup:**

```bash
kubectl delete component greeting-service -n default
kubectl delete workload greeting-service-workload -n default
```

### API Management ClusterTrait

The `api-management` ClusterTrait provides declarative API management for components routed through WSO2 API Platform. It creates a kgateway `Backend` (static) pointing to the WSO2 router, a WSO2 `RestApi` resource defining the API, and patches the HTTPRoute to route traffic through the WSO2 router.

#### Derived Values (automatic)

The following values are automatically derived from component metadata and workload endpoints — they are not user-configurable:

| Value         | Pattern                                                      | Example                                 |
| ------------- | ------------------------------------------------------------ | --------------------------------------- |
| `displayName` | `<environment>-<namespace>-<componentName>`                  | `development-default-greeting-service`  |
| `context`     | `/<environment>-<namespace>-<componentName>`                 | `/development-default-greeting-service` |
| `version`     | `v1.0`                                                       | `v1.0`                                  |
| `upstream`    | `http://<componentName>.<namespace>:<first endpoint port>`   | `http://greeting-service.default:9090`  |
| `operations`  | All methods (GET, POST, PUT, PATCH, DELETE, OPTIONS) on `/*` | —                                       |

#### Trait Parameters

Optional policies that can be enabled via trait parameters (all disabled by default):

| Parameter    | Type   | Description                                                                                                                                                                                                                                 |
| ------------ | ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `jwtAuth`    | object | JWT authentication. Properties: `enabled` (boolean, default: `false`)                                                                                                                                                                       |
| `rateLimit`  | object | Request rate limiting. Properties: `enabled` (boolean, default: `false`), `limits` (array of `{requests: integer, duration: string}`, default: `[]`)                                                                                        |
| `addHeaders` | object | Custom request/response headers. Properties: `enabled` (boolean, default: `false`), `requestHeaders` (array of `{name: string, value: string}`, default: `[]`), `responseHeaders` (array of `{name: string, value: string}`, default: `[]`) |

> **Note:** A default CORS policy (`allowedOrigins: ["*"], allowedMethods: ["*"], allowedHeaders: ["*"]`) is always included in the RestApi regardless of trait parameters. JWT auth key manager configuration is set in the `gateway-configuration.yaml` ConfigMap.

#### How It Works

The trait uses OpenChoreo's template rendering pipeline to:

1. **Create a kgateway Backend** — A `Backend` resource of type `Static` pointing to the WSO2 API Platform router (`api-platform-default-gateway-gateway-runtime.openchoreo-data-plane:8080`). This allows the HTTPRoute to reference the WSO2 router as a backend.

2. **Create a WSO2 RestApi** — A `RestApi` resource with automatically derived displayName, context, version, upstream URL, and operations. Policies include a default CORS policy plus any enabled optional policies (JWT auth, rate limiting, custom headers). WSO2's operator reconciles this into the router's configuration.

3. **Patch the HTTPRoute backendRef** — Replaces the component's Service in the HTTPRoute `backendRef` with the kgateway `Backend` pointing to the WSO2 router. This redirects all traffic through the WSO2 API Platform for policy enforcement.

4. **Patch the HTTPRoute URL rewrite** — Updates the `URLRewrite` filter's `replacePrefixMatch` to use the derived API context path (`/<environment>-<namespace>-<componentName>`), ensuring the WSO2 router receives the correct path prefix to match the `RestApi` configuration.

#### Composing with KEDA scale-to-zero

The `api-management` trait can share a component with the `keda-scaling` trait (the `autoscaling-keda` module), giving a service both WSO2 API management and scale-to-zero. Because both traits repoint the HTTPRoute `backendRef`, list `api-management` **before** `keda-scaling` in `spec.traits`: `api-management` takes the edge route through the WSO2 router, and `keda-scaling` detects it no longer owns the route, skips its own edge patch, and wires its interceptor behind the component's Service so the WSO2 router's upstream call wakes the pod. Traffic flows kgateway -> WSO2 router -> component ExternalName -> KEDA interceptor -> pod, and the API policies (rate limiting, auth, headers) still apply. See the autoscaling-keda module's `CONFIGURATION.md` ("Composing with an API gateway trait") for the details.

#### Key Differences from Other Gateway Modules

| Feature           | Kong                    | Envoy Gateway                    | Traefik                       | WSO2 API Platform                                                                            |
| ----------------- | ----------------------- | -------------------------------- | ----------------------------- | -------------------------------------------------------------------------------------------- |
| Replaces kgateway | Yes                     | Yes                              | Yes                           | **No** — runs alongside kgateway                                                             |
| Gateway API       | Native support          | Native support                   | Native support                | **Limited** — Gateway + HTTPRoute ([edge mode](#running-at-the-edge)); default uses own CRDs |
| Rate limiting     | `KongPlugin` annotation | `BackendTrafficPolicy` targetRef | `Middleware` + `ExtensionRef` | `RestApi` policies                                                                           |
| Authentication    | `KongPlugin` annotation | `SecurityPolicy` targetRef       | `Middleware` + `ExtensionRef` | `RestApi` policies                                                                           |
| Traffic routing   | Direct (Gateway API)    | Direct (Gateway API)             | Direct (Gateway API)          | Via kgateway `Backend` → WSO2 router                                                         |

#### Example Usage

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Component
metadata:
  name: my-service
  namespace: default
spec:
  owner:
    projectName: default
  autoDeploy: true
  componentType:
    kind: ClusterComponentType
    name: deployment/service
  traits:
    - name: api-management
      instanceName: my-api
      kind: ClusterTrait
      parameters:
        jwtAuth:
          enabled: true
        rateLimit:
          enabled: true
          limits:
            - requests: 100
              duration: "1m"
        addHeaders:
          enabled: true
          requestHeaders:
            - name: X-Gateway
              value: wso2-api-platform
          responseHeaders:
            - name: X-Powered-By
              value: OpenChoreo
```

---

## Running at the Edge

By default WSO2 API Platform runs **behind kgateway**. kgateway stays the Kubernetes Gateway API controller, and the `api-management` trait routes traffic through the WSO2 router via a kgateway `Backend` and a `RestApi`. This is the default, recommended setup.

With the release of WSO2 API Platform 1.1.0, **Kubernetes Gateway API** is supported. Currently only the `Gateway` and `HTTPRoute` resources are supported. This lets you **remove kgateway from the data plane** and run the WSO2 API Platform gateway directly at the edge, where it terminates client traffic and routes straight to your component Services.

What changes in this mode:

- WSO2 operator reconciles a standard `Gateway` (through a WSO2 `GatewayClass`) and the `HTTPRoute`s attached to it. There is **no kgateway, no kgateway `Backend`, and no `RestApi`**.
- The **standard OpenChoreo ComponentTypes work unchanged** — the `HTTPRoute` they render (with the component `Service` as its `backendRef`) is consumed directly by WSO2 API Platform Gateway. **The `api-management` trait is not used.**
- The edge gateway is swapped in through the data plane Helm chart. The same way you would switch to Kong, Traefik, or Envoy Gateway.

> Use this mode when you want WSO2 API Platform Gateway to be the only data plane gateway. If you need the `RestApi`/trait-driven API management features, keep the default kgateway-fronted setup described above.

### Prerequisites

- The WSO2 API Platform **operator** installed in the data plane ([Installation → Step 1](#step-1-install-the-wso2-api-platform-operator)). You do **not** need the `APIGateway` CR or the `api-management` trait for this mode.
- Helm 3.x and `kubectl`.

### Step 1: Create the WSO2 API Platform GatewayClass

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: wso2-api-platform
spec:
  controllerName: gateway.api-platform.wso2.com/gateway-operator
EOF

kubectl get gatewayclass wso2-api-platform   # ACCEPTED should be True
```

> **Note:** The operator selects the GatewayClass by **name**, from its `gateway_api.gateway_class_names` allowlist (default `wso2-api-platform`). To use a different name, update that operator value. The `controllerName` is recorded on the class and is immutable once set.

### Step 2: Remove the default data plane gateway

Delete the existing data plane `Gateway` CR so it can be recreated with the WSO2 class:

```bash
kubectl delete gateway gateway-default -n openchoreo-data-plane
```

If you previously created the WSO2 `APIGateway` CR for the kgateway-fronted mode, delete it as well — edge mode uses a `Gateway` instead, and a leftover `APIGateway` would run a second, unused gateway runtime:

```bash
kubectl delete apigateway api-platform-default -n openchoreo-data-plane
```

> **Single cluster mode:** Do not remove the kgateway controller or its GatewayClass — the control and observability planes still depend on it. Only the **data plane** `gateway-default` is being swapped.

### Step 3: Point the data plane gateway at the WSO2 GatewayClass

Create a values overlay for the data plane Helm chart:

```yaml
# wso2-edge-values.yaml
gateway:
  gatewayClassName: wso2-api-platform
  httpPort: 19080
  # metadata.annotations on the Gateway. Tells the operator which ConfigMap of
  # Helm values to use for the gateway it deploys (reuses gateway-configuration.yaml).
  annotations:
    gateway.api-platform.wso2.com/helm-values-configmap: api-platform-operator-gateway-values
  # spec.infrastructure is propagated to the WSO2 gateway-runtime Service.
  infrastructure:
    labels:
      openchoreo.dev/system-component: gateway
    annotations:
      # Expose the runtime as a LoadBalancer so it owns the edge port.
      gateway.api-platform.wso2.com/service-type: LoadBalancer
```

Apply it with a Helm upgrade of the data plane chart (use the same chart version as your existing install):

```bash
helm upgrade openchoreo-data-plane \
  oci://ghcr.io/openchoreo/helm-charts/openchoreo-data-plane \
  --version 0.0.0-latest-dev \
  --namespace openchoreo-data-plane \
  --reuse-values \
  -f wso2-edge-values.yaml
```

This re-creates the `gateway-default` Gateway CR referencing `wso2-api-platform`:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: gateway-default
  namespace: openchoreo-data-plane
  annotations:
    gateway.api-platform.wso2.com/helm-values-configmap: api-platform-operator-gateway-values
spec:
  gatewayClassName: wso2-api-platform
  infrastructure:
    annotations:
      gateway.api-platform.wso2.com/service-type: LoadBalancer
  listeners:
    - name: http
      port: 19080
      protocol: HTTP
      allowedRoutes:
        namespaces:
          from: All
```

The operator reconciles it and installs a per Gateway runtime Helm release (`gateway-default-gateway`), deploying the WSO2 controller + gateway-runtime (Envoy) and exposing the runtime as a LoadBalancer on the listener port.

> **About the annotations:**
>
> - `gateway.api-platform.wso2.com/helm-values-configmap` — the operator merges this ConfigMap's values (your `gateway-configuration.yaml`) into the gateway it deploys. Omit it to use operator defaults.
> - `gateway.api-platform.wso2.com/service-type: LoadBalancer` — makes the gateway-runtime Service a LoadBalancer so it can serve external traffic directly. Use `ClusterIP` if another load balancer or ingress fronts it.

### Step 4: Verify the gateway is programmed

```bash
kubectl get gatewayclass wso2-api-platform
kubectl get gateway gateway-default -n openchoreo-data-plane   # ACCEPTED + PROGRAMMED = True
kubectl get pods,svc -n openchoreo-data-plane \
  -l app.kubernetes.io/instance=gateway-default-gateway
```

Expected:

| Resource                                          | Description                                      |
| ------------------------------------------------- | ------------------------------------------------ |
| `gateway-default-gateway-controller-*` pod        | WSO2 controller — manages API config, xDS server |
| `gateway-default-gateway-gateway-runtime-*` pod   | WSO2 gateway runtime (Envoy + policy engine)     |
| `gateway-default-gateway-gateway-runtime` Service | `LoadBalancer` exposing the HTTP listener port   |

### Step 5: Deploy and invoke a component (no trait)

Because WSO2 consumes the standard HTTPRoute directly, **do not attach the `api-management` trait**. Deploy any component that uses a default ComponentType and exposes an external HTTP endpoint, for example:

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Component
metadata:
  name: greeter-service
  namespace: default
spec:
  owner:
    projectName: default
  autoDeploy: true
  componentType:
    kind: ClusterComponentType
    name: deployment/service
---
apiVersion: openchoreo.dev/v1alpha1
kind: Workload
metadata:
  name: greeter-service-workload
  namespace: default
spec:
  owner:
    projectName: default
    componentName: greeter-service
  endpoints:
    http:
      type: HTTP
      port: 9090
      visibility: [external]
  container:
    image: ghcr.io/openchoreo/samples/greeter-service:latest
    args: ["--port", "9090"]
```

The OpenChoreo pipeline renders an `HTTPRoute` whose `backendRef` is the component `Service`. Confirm WSO2 API Platform accepted it:

```bash
kubectl get httproute -A
kubectl get httproute <name> -n <dp-namespace> \
  -o jsonpath='{.status.parents[0].conditions}'
# Accepted=True      ("Route accepted by platform gateway operator")
# ResolvedRefs=True  ("API deployed to platform gateway")
```

Invoke it through the WSO2 edge gateway:

```bash
curl http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greeter/greet -v
```

A `server: WSO2 API Platform` response header confirms traffic is being served by WSO2 at the edge.

### Endpoint URL resolution

Because the Gateway keeps the same name (`gateway-default`), namespace, and listener port (`19080`), the control plane's endpoint URL resolution and the ClusterDataPlane/DataPlane gateway config keep working unchanged. If you change the gateway name or port, update `spec.gateway.ingress` on the ClusterDataPlane/DataPlane CR to match.

### Reverting to kgateway

Delete the WSO2 `Gateway`, then re-run the Helm upgrade with the defaults (`gateway.gatewayClassName=kgateway`) to restore the kgateway-fronted setup.

---

## Connecting to WSO2 API Manager

By default, the WSO2 API Platform gateway operates standalone — APIs are managed directly through `RestApi` CRDs (e.g. via the `api-management` ClusterTrait). For deployments that need centralized API governance, lifecycle management, and developer portal capabilities, the gateway can be connected to a **WSO2 API Manager (APIM) control plane** running anywhere reachable from the cluster.

Once connected, the gateway controller:

- Registers itself with APIM as a managed gateway (using a gateway registration token)
- Subscribes to API deployment events from APIM over a WebSocket channel
- Pushes locally created APIs (e.g. from `RestApi` CRs) back to APIM via its Publisher REST API (using OAuth2 client credentials)
- Receives policy and subscription state from APIM

This section assumes you already have a running WSO2 API Manager 4.7.x (or later) instance — installation of APIM itself is out of scope.

### Prerequisites

- A running WSO2 API Manager instance, reachable from the `openchoreo-data-plane` namespace.
  - **In-cluster:** any namespace, exposed as a `Service` (e.g. `wso2am-acp-service.openchoreo-control-plane:9443`).
  - **External:** a publicly resolvable hostname / IP with port `9443` (or whichever Management port APIM exposes).
- APIM admin credentials and access to the Admin Portal (`https://<apim-host>:9443/admin`).
- The WSO2 API Platform operator and gateway already installed (see [Installation](#installation)).

### Step 1: Generate a Gateway Registration Token in APIM

1. Open the APIM Admin Portal: `https://<apim-host>:9443/admin`. Login with admin credentials.
2. Navigate to **Gateways → Add Gateway**.
3. Fill in a name (e.g. `oc-platform-gateway`) and any required fields, then save.
4. Copy the **Gateway Registration Token** shown after creation. It looks like:
   ```
   019e5e87-9cba-730a-af90-99b5bdb88eb8.G9wPihxNkvpK1Vl3cH4YoOlrytlo9jkRV17RSAjM4eI
   ```

> **Note:** The token authenticates the gateway's WebSocket connection to APIM. Keep it secret. It is bound to the gateway name you chose; the same name must be used in the gateway configuration in Step 3.

### Step 2: Obtain OAuth2 Client Credentials for APIM REST API

The gateway also needs OAuth2 `client_id`/`client_secret` to call APIM's Publisher REST API (used to push locally created APIs back to APIM).

Obtain a `clientId` and `clientSecret` for a SaaS application in APIM with the grant types `client_credentials`, `password`, and `refresh_token`. This is typically done by registering an OAuth2 client via APIM's Dynamic Client Registration (DCR) endpoint or through the Admin Portal — refer to the [APIM documentation](https://apim.docs.wso2.com/en/latest/reference/product-apis/devops-apis/dynamic-client-registration-api/) for the exact procedure for your version.

You will use these credentials together with the APIM admin `username` and `password` in Step 3.

### Step 3: Update the Gateway Configuration

Edit the `gateway-configuration.yaml` ConfigMap (the one referenced by `APIGateway.spec.configRef`) and set the control plane fields under `gateway.config.controller.controlplane` and `gateway.controller.controlPlane`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: api-platform-operator-gateway-values
  namespace: openchoreo-data-plane
data:
  values.yaml: |
    gateway:
      # ... other settings unchanged ...

      config:
        controller:
          # ... other settings unchanged ...
          controlplane:
            # Skip TLS verification for the WebSocket connection.
            # Set to false in production and provide a CA bundle via upstreamCerts.
            insecure_skip_verify: true

            # OAuth2 client for APIM Publisher REST API (from Step 2)
            apim_oauth2_client_id:     "<your-clientId>"
            apim_oauth2_client_secret: "<your-clientSecret>"
            apim_oauth2_username:      "admin"
            apim_oauth2_password:      "admin"

            # Must match the gateway name created in APIM in Step 1
            gateway_name: "oc-platform-gateway"

      controller:
        controlPlane:
          # APIM Management host:port reachable from this cluster.
          # In-cluster example:
          host: wso2am-acp-service.openchoreo-control-plane:9443
          # External example:
          # host: apim.example.com:9443
          port: 9443

          # Gateway Registration Token (from Step 1).
          # Either embed the value directly:
          token:
            value: "019e5e87-9cba-730a-af90-99b5bdb88eb8.G9wPihxNkvpK1Vl3cH4YoOlrytlo9jkRV17RSAjM4eI"
            key: token
          # Or reference an existing Secret instead:
          # token:
          #   secretName: gateway-registration-token
          #   key: token
```

**Field reference:**

| Field                                                      | Required | Description                                                                                        |
| ---------------------------------------------------------- | -------- | -------------------------------------------------------------------------------------------------- |
| `config.controller.controlplane.gateway_name`              | Yes      | Must match the gateway name created in APIM (Step 1).                                              |
| `config.controller.controlplane.apim_oauth2_client_id`     | Yes      | OAuth2 client ID for APIM Publisher REST API (Step 2).                                             |
| `config.controller.controlplane.apim_oauth2_client_secret` | Yes      | OAuth2 client secret (Step 2).                                                                     |
| `config.controller.controlplane.apim_oauth2_username`      | Yes      | APIM user with publisher permissions (default `admin`).                                            |
| `config.controller.controlplane.apim_oauth2_password`      | Yes      | Password for the above user.                                                                       |
| `config.controller.controlplane.insecure_skip_verify`      | No       | Skip TLS verification for the control-plane WebSocket. Default `true`. Set `false` for production. |
| `controller.controlPlane.host`                             | Yes      | APIM Management host:port (in-cluster Service DNS or external FQDN).                               |
| `controller.controlPlane.port`                             | Yes      | APIM Management HTTPS port (typically `9443`).                                                     |
| `controller.controlPlane.token.value`                      | One of   | Gateway registration token inline (Step 1).                                                        |
| `controller.controlPlane.token.secretName` / `.key`        | One of   | Or a reference to an existing Secret holding the token under the given key.                        |

### Step 4: Apply the Changes

```bash
kubectl apply -f gateway-configuration.yaml

# Trigger the operator to reconcile and roll the controller deployment
kubectl annotate apigateway -n openchoreo-data-plane api-platform-default \
  reconcile-trigger="$(date +%s)" --overwrite

kubectl rollout status deployment -n openchoreo-data-plane \
  api-platform-default-gateway-controller --timeout=120s
```

The operator picks up the new ConfigMap values, regenerates the deployment, and rolls the controller pod.

### Step 5: Verify the Connection

Tail the gateway controller logs and look for a successful handshake:

```bash
kubectl logs -n openchoreo-data-plane \
  -l app.kubernetes.io/component=controller,app.kubernetes.io/instance=api-platform-default-gateway \
  --tail=50 -f
```

Expected log lines on success:

```
"Connecting to control plane","url":"wss://<apim-host>:9443/internal/data/v1/ws/gateways/connect"
"Received connection acknowledgment","gateway_id":"...","connection_id":"..."
"Connection state changed","from":"connecting","to":"connected"
"Control plane connection established"
"Starting deployment sync"
```

In the APIM Admin Portal under **Gateways**, the gateway should now show as **Connected**.

### Troubleshooting

**`websocket: close 4401: Invalid or expired API key`**

The registration token doesn't match what APIM expects. Common causes:

- Token was copied incorrectly.
- The gateway was deleted from APIM (or its database was reset) since the token was issued — regenerate the token and re-apply Step 3.
- `gateway_name` in the ConfigMap doesn't match the gateway name in APIM — the token is bound to that name.

**TLS handshake errors**

The gateway's WebSocket client can't validate APIM's TLS certificate. For non-production setups, leave `insecure_skip_verify: true`. For production, mount a CA bundle via `controller.upstreamCerts.secretName` / `configMapName` and set `insecure_skip_verify: false`.

**Controller pod can't resolve `host`**

- For in-cluster APIM: confirm the Service exists (`kubectl get svc -n <apim-namespace>`) and that `host` includes the namespace (`<svc>.<ns>:9443`).
- For external APIM: confirm DNS resolution from inside a pod in `openchoreo-data-plane` (e.g. `kubectl run -i --rm --restart=Never --image=busybox -- nslookup <apim-host>`) and check egress NetworkPolicies if any are in place.

**APIs created locally don't appear in APIM**

The OAuth2 client credentials or admin user/password are wrong, or the user lacks publisher permissions. Check controller logs for `401` or `403` responses against the APIM REST API and verify the values from Step 2 / Step 3.

---

## Configuration

### Helm Charts Reference

WSO2 API Platform is installed via the operator Helm chart, which in turn deploys the gateway:

| Chart                                                          | Version | Description                                                                       |
| -------------------------------------------------------------- | ------- | --------------------------------------------------------------------------------- |
| `oci://ghcr.io/wso2/api-platform/helm-charts/gateway-operator` | `0.8.0` | WSO2 API Platform operator — watches RestApi/APIGateway CRDs, deploys the gateway |
| `oci://ghcr.io/wso2/api-platform/helm-charts/gateway`          | `1.1.0` | WSO2 API Platform gateway — deployed by the operator via `gateway.helm.*`         |

**Operator Helm values:**

| Value                       | Type   | Default                                                 | Description                                               |
| --------------------------- | ------ | ------------------------------------------------------- | --------------------------------------------------------- |
| `gateway.helm.chartName`    | string | `"oci://ghcr.io/wso2/api-platform/helm-charts/gateway"` | OCI chart reference for the gateway sub-chart             |
| `gateway.helm.chartVersion` | string | `"1.1.0"`                                               | Version of the gateway chart deployed by the operator     |
| `gateway.configRefName`     | string | `"api-platform-default-gateway-values"`                 | ConfigMap name referenced by the Gateway CR's `configRef` |

The standard gateway values continue to apply to kgateway:

| Value                         | Type   | Default                        | Description                              |
| ----------------------------- | ------ | ------------------------------ | ---------------------------------------- |
| `gateway.gatewayClassName`    | string | `"kgateway"`                   | GatewayClass name (keep as `kgateway`)   |
| `gateway.httpPort`            | int    | `9080`                         | HTTP listener port                       |
| `gateway.httpsPort`           | int    | `9443`                         | HTTPS listener port                      |
| `gateway.tls.hostname`        | string | `"*.openchoreoapis.localhost"` | Wildcard hostname for TLS certificate    |
| `gateway.tls.certificateRefs` | string | `"openchoreo-gateway-tls"`     | Secret name for the TLS certificate      |
| `gateway.infrastructure`      | object | `{}`                           | Cloud provider load balancer annotations |

### WSO2 API Platform Router Configuration

The WSO2 API Platform router listens on port 8080 by default within the cluster. The kgateway `Backend` resource created by the trait points to:

```
api-platform-default-gateway-gateway-runtime.openchoreo-data-plane:8080
```

This is an internal cluster address. External traffic reaches the WSO2 router via kgateway, which handles TLS termination and hostname-based routing.

### WSO2 RestApi Policies

Policies are configured via the `api-management` ClusterTrait parameters and rendered into the `RestApi` resource's `policies` field. The trait always includes a default CORS policy and conditionally adds other policies based on the enabled flags.

**Rendered policies example** (with all policies enabled):

```yaml
policies:
  - name: cors
    version: v0
    params:
      allowedOrigins: ["*"]
      allowedMethods: ["*"]
      allowedHeaders: ["*"]
  - name: jwt-auth
    version: v0
  - name: basic-ratelimit
    version: v0
    params:
      limits:
        - requests: 5
          duration: "1m"
  - name: add-headers
    version: v0
    params:
      requestHeaders:
        - name: X-Gateway
          value: wso2-api-platform
      responseHeaders:
        - name: X-Powered-By
          value: OpenChoreo
```

> **Note:** JWT auth key manager configuration (issuer, JWKS URI, allowed algorithms, etc.) is set in the `gateway-configuration.yaml` ConfigMap under `policy_configurations.jwtauth_v0`, not in the trait parameters.

Refer to the [WSO2 API Platform policy definitions](https://github.com/wso2/gateway-controllers/tree/main/policies) for the full list of supported policies and their schemas.

---

## Maintenance

### Monitoring WSO2 API Platform Health

```bash
# Check WSO2 API Platform pod status
kubectl get pods -n openchoreo-data-plane \
  --selector="app.kubernetes.io/instance=api-platform-default-gateway"

# Check RestApi resources
kubectl get restapi -A

# View WSO2 operator logs
kubectl logs -n openchoreo-data-plane \
  -l app.kubernetes.io/component=operator,app.kubernetes.io/instance=api-platform-default-gateway -f

# View WSO2 router logs
kubectl logs -n openchoreo-data-plane \
  -l app.kubernetes.io/component=router,app.kubernetes.io/instance=api-platform-default-gateway -f

# Check kgateway Backend resources
kubectl get backend.gateway.kgateway.dev -A
```

### Monitoring kgateway Health

Since kgateway remains the Gateway API controller, monitor it as usual:

```bash
# Check Gateway CR programmed status
kubectl get gateway gateway-default -n openchoreo-data-plane

# Check HTTPRoute status
kubectl get httproute -A

# Check Gateway listeners
kubectl describe gateway gateway-default -n openchoreo-data-plane
```

### Upgrading WSO2 API Platform

Upgrade the operator chart. To also upgrade the gateway, update the `gateway.helm.chartVersion` value:

```bash
helm upgrade api-platform-operator \
  oci://ghcr.io/wso2/api-platform/helm-charts/gateway-operator \
  --version <new-operator-version> \
  --namespace openchoreo-data-plane \
  --reuse-values \
  --set gateway.helm.chartVersion=<new-gateway-version>
```

### TLS Certificate Renewal

TLS is handled by kgateway and cert-manager. The WSO2 API Platform router receives plaintext HTTP traffic from kgateway within the cluster. Certificate management is unchanged:

```bash
# Check certificate status
kubectl get certificate -n openchoreo-data-plane

# Check secret expiry
kubectl get secret openchoreo-gateway-tls -n openchoreo-data-plane \
  -o jsonpath='{.metadata.annotations}'
```

### Troubleshooting

**WSO2 API Platform pods not running**

```bash
kubectl describe pods -n openchoreo-data-plane \
  --selector="app.kubernetes.io/instance=api-platform-default-gateway"
```

Common causes:

- Operator or gateway Helm chart not installed. Verify with `helm list -n openchoreo-data-plane`.
- CRDs not installed. Verify `kubectl get crd restapis.gateway.api-platform.wso2.com apigateways.gateway.api-platform.wso2.com`.
- Image pull issues. Check pod events for image pull errors.

**RestApi not reconciled**

```bash
kubectl describe restapi <name> -n <namespace>
```

Common causes:

- WSO2 operator not running. Check operator pod logs.
- Invalid RestApi spec (incorrect context path format, missing required fields).

**Traffic not reaching the upstream service**

```bash
# Check the HTTPRoute is patched correctly
kubectl get httproute <name> -n <namespace> -o yaml

# Verify the Backend resource exists
kubectl get backend.gateway.kgateway.dev -n <namespace>

# Check the RestApi upstream URL matches the component's Service
kubectl get restapi <name> -n <namespace> -o yaml | grep -A 5 upstream
```

Common causes:

- HTTPRoute `backendRef` not pointing to the kgateway Backend. Check the trait's patch is applied.
- RestApi upstream URL has wrong service name or port.
- WSO2 router cannot reach the upstream Service (network policy or DNS issue).
- Missing RBAC for the cluster agent to create `gateway.api-platform.wso2.com` or `gateway.kgateway.dev` resources (see Step 4).

**Endpoint URLs not resolving**

Verify the DataPlane CR gateway config matches the actual Gateway CR:

```bash
kubectl get dataplane default -o yaml | grep -A 10 gateway
```

Ensure `publicGatewayName` and `publicGatewayNamespace` match the Gateway CR's name and namespace.

---

## Customization

### Removing WSO2 API Platform

To remove the WSO2 API Platform module, uninstall the operator Helm release (which also removes the gateway it deployed):

```bash
helm uninstall api-platform-operator -n openchoreo-data-plane
```

> **Note:** Existing `RestApi` and `Backend` resources created by traits will remain. Components using the `api-management` trait should be updated to remove the trait before uninstalling the module. To clean up CRDs manually:
>
> ```bash
> kubectl delete crd restapis.gateway.api-platform.wso2.com apigateways.gateway.api-platform.wso2.com
> ```

### Enabling Rate Limiting

Enable request rate limiting with configurable time windows:

```yaml
traits:
  - name: api-management
    instanceName: my-api
    kind: ClusterTrait
    parameters:
      rateLimit:
        enabled: true
        limits:
          - requests: 100
            duration: "1m"
          - requests: 1000
            duration: "1h"
```

### Enabling JWT Authentication

Enable JWT authentication (key manager configuration is set in the `gateway-configuration.yaml` ConfigMap):

```yaml
traits:
  - name: api-management
    instanceName: my-api
    kind: ClusterTrait
    parameters:
      jwtAuth:
        enabled: true
```

### Adding Custom Headers

Add custom headers to requests and/or responses:

```yaml
traits:
  - name: api-management
    instanceName: my-api
    kind: ClusterTrait
    parameters:
      addHeaders:
        enabled: true
        requestHeaders:
          - name: X-Gateway
            value: wso2-api-platform
        responseHeaders:
          - name: X-Powered-By
            value: OpenChoreo
```

### Cloud Provider Load Balancer Configuration

Load balancer configuration is handled by kgateway. Use the `gateway.infrastructure` value in the data plane Helm chart:

```yaml
gateway:
  infrastructure:
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: "external"
      service.beta.kubernetes.io/aws-load-balancer-nlb-target-type: "ip"
      service.beta.kubernetes.io/aws-load-balancer-scheme: "internet-facing"
```
