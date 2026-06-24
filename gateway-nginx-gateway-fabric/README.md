# NGINX Gateway Fabric Module for OpenChoreo Data Plane

This document provides comprehensive documentation for integrating [NGINX Gateway Fabric](https://docs.nginx.com/nginx-gateway-fabric/) (NGF) as the API gateway in the OpenChoreo data plane, replacing the default kgateway (Envoy-based) implementation.

## Table of Contents

- [Overview](#overview)
- [Compatibility](#compatibility)
- [High-Level Architecture](#high-level-architecture)
- [Installation](#installation)
- [NGF API Configuration Trait](#ngf-api-configuration-trait)
- [Configuration](#configuration)
- [Maintenance](#maintenance)
- [Customization](#customization)

---

## Overview

OpenChoreo uses the [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) as the standard API for exposing component endpoints to public or internal networks. Because the Gateway API is a vendor-neutral Kubernetes standard, the gateway layer is easily pluggable and extensible across vendors — any Gateway API-compliant controller can serve as the ingress layer without changes to the control plane or the OpenChoreo ComponentTypes.

The NGINX Gateway Fabric module replaces the default kgateway (Envoy) with [NGINX Gateway Fabric](https://docs.nginx.com/nginx-gateway-fabric/), NGINX's official implementation of the Kubernetes Gateway API built on NGINX. It provides API management capabilities such as rate limiting, authentication, and response header injection — through first-class Kubernetes CRDs (`RateLimitPolicy`, `AuthenticationFilter`) and native Gateway API filters.

### Key Design Decisions

- **Standard Gateway API as the contract**: OpenChoreo components create `HTTPRoute` resources that reference a `Gateway` by name. The gateway implementation is transparent to the control plane.
- **First-class CRDs over snippets**: NGF 2.x ships native `RateLimitPolicy` and `AuthenticationFilter` CRDs. This module uses those rather than `SnippetsFilter` (raw NGINX config injection), so there is no need to enable snippets at install time, and the policies work on NGINX OSS (no NGINX Plus required).
- **Policy attachment + native filters (like Envoy Gateway, not Kong/APISIX annotations)**: Rate limiting attaches to the HTTPRoute via `RateLimitPolicy.spec.targetRefs`. Authentication attaches via a Gateway API `ExtensionRef` filter. Response headers use the native `ResponseHeaderModifier` filter. No annotations on the HTTPRoute.
- **Helm-driven configuration**: The `gatewayClassName` in the data plane Helm chart determines which gateway controller processes the `Gateway` CR and its routes.
- **No control plane changes required**: Switching gateways only requires data plane reconfiguration. The rendering pipeline, endpoint resolution, and release controllers work unchanged.

---

## Compatibility

NGINX Gateway Fabric is a pluggable replacement for the default kgateway in the OpenChoreo data plane. It is built on the [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/), and **each NGF release supports a specific Gateway API version**. Because OpenChoreo installs its own Gateway API CRDs, install the NGF version whose supported Gateway API matches the version shipped by your OpenChoreo release.

| OpenChoreo version | Kubernetes Gateway API | NGINX Gateway Fabric |
| ------------------ | ---------------------- | -------------------- |
| v0.x.x             | v1.4.1                 | 2.4.2                |
| v1.0.x             | v1.4.1                 | 2.4.2                |
| v1.1.x             | v1.4.1                 | 2.4.2                |
| v1.2.x             | v1.5.1                 | 2.6.5                |

> **Note:** The Gateway API version is the one OpenChoreo installs with its data plane; the NGF version is the one this module installs (see [Installation](#installation)). This mapping follows the official [NGF technical specifications](https://docs.nginx.com/nginx-gateway-fabric/overview/technical-specifications/) — NGF 2.4.x supports Gateway API **v1.4.1** and NGF 2.6.x supports **v1.5.1**. Each NGF release supports exactly one Gateway API version; installing a mismatched NGF version is unsupported.
>
> **Trait feature availability:** The `nginx-gateway-fabric-api-configuration` trait uses `RateLimitPolicy` and Basic-auth `AuthenticationFilter`, **both introduced in NGF 2.4.0** and both available on NGINX OSS. All rows above therefore support the trait. JWT and OIDC authentication were added in NGF 2.5.0 and require **NGINX Plus** (see [Customization](#customization)).
>
> **Do not re-install Gateway API CRDs.** The NGF Helm chart does **not** bundle the upstream Gateway API CRDs — normally you install them as a prerequisite. In OpenChoreo they are **already installed** at the matching version, and OpenChoreo enforces a `ValidatingAdmissionPolicy` (`safe-upgrades.gateway.networking.k8s.io`) that rejects installing Gateway API CRDs older than the running version. Skip NGF's "install Gateway API CRDs" step entirely.

---

## High-Level Architecture

### Gateway Integration in OpenChoreo

```
┌─────────────────────────────────────────────────────────────┐
│                     CONTROL PLANE                           │
│                                                             │
│   Renders component templates and applies resources         │
│   (Deployment, Service, HTTPRoute) to the data plane        │
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
│              │   ┌────────────┐             │               │
│              │   │ Deployment │             │               │
│              │   └────────────┘             │               │
│              │   ┌────────────┐             │               │
│              │   │  Service   │             │               │
│              │   └─────┬──────┘             │               │
│              │         │ backendRef         │               │
│              │   ┌─────┴──────┐             │               │
│              │   │ HTTPRoute  │             │               │
│              │   │ parentRef ─┼────┐        │               │
│              │   │ + ExtRef ──┼──┐ │        │               │
│              │   └────────────┘  │ │        │               │
│              │         ▲         │ │        │               │
│              │         │ targetRef (RateLimitPolicy)        │
│              │   ┌─────┴──────┐  │ │        │               │
│              │   │ RateLimit- │  │ │        │               │
│              │   │ Policy     │  │ │        │               │
│              │   └────────────┘  │ │        │               │
│              │   ┌────────────┐  │ │        │               │
│              │   │ Authentic- │◄─┘ │ (ExtensionRef filter)  │
│              │   │ ationFilter│    │        │               │
│              │   └────────────┘    │        │               │
│              └─────────────────────┼────────┘               │
│                                    │                        │
│              ┌─────────────────────┴──────────┐             │
│              │   Gateway CR                   │             │
│              │   name: gateway-default        │             │
│              │   gatewayClassName: nginx ◄──── Configurable │
│              │   (GatewayClass → NginxProxy:  │             │
│              │    data plane pod labels)      │             │
│              │   listeners: http/https        │             │
│              └───────────────┬────────────────┘             │
│                              │ watches                      │
│              ┌───────────────┴────────────────┐             │
│              │   NGF Control Plane            │             │
│              │   - Watches Gateway, HTTPRoute │             │
│              │   - Watches NGF CRDs           │             │
│              │   - Provisions NGINX data plane│             │
│              │   - Generates nginx.conf       │             │
│              └───────────────┬────────────────┘             │
│                              │ provisions + configures      │
│              ┌───────────────┴────────────────┐             │
│              │   NGINX Data Plane (per Gateway)│             │
│              │   - Processes traffic          │             │
│              │   - TLS termination            │             │
│              │   - limit_req / auth_basic     │             │
│              │   - Routes to backends         │             │
│              └───────────────┬────────────────┘             │
│                              │                              │
│                         LoadBalancer                        │
│                         :19080 (HTTP)                       │
│                         :19443 (HTTPS)                      │
└─────────────────────────────────────────────────────────────┘
                              │
                          Client Traffic
```

### Component Breakdown

| Component               | Role                                                                                                                                |
| ----------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| **NGF Control Plane**   | Watches Gateway API resources and NGF CRDs, provisions an NGINX data plane Deployment + Service per Gateway, and renders nginx.conf |
| **NGINX Data Plane**    | The provisioned NGINX pod(s) that process ingress traffic, terminate TLS, enforce rate limits / Basic auth, and route to backends   |
| **Gateway CR**          | Kubernetes Gateway API resource that defines listeners (ports, protocols, TLS). Created by Helm during data plane installation       |
| **GatewayClass**        | Declares that `gateway.nginx.org/nginx-gateway-controller` handles Gateway CRs with class `nginx`. Created by the NGF chart          |
| **NginxProxy**          | NGF CRD (`gateway.nginx.org/v1alpha2`) that configures the provisioned NGINX data plane (here: pod labels, applied via a Deployment `patches` entry). Auto-created by the NGF chart as `ngf-proxy-config` and referenced from the `nginx` GatewayClass via `spec.parametersRef` |
| **HTTPRoute**           | Gateway API route resource. Created by OpenChoreo per component. References the Gateway CR via `parentRefs`                          |
| **RateLimitPolicy**     | NGF CRD (`gateway.nginx.org/v1alpha1`) for local rate limiting. Attaches to the HTTPRoute via `targetRefs` (policy attachment)       |
| **AuthenticationFilter**| NGF CRD (`gateway.nginx.org/v1alpha1`) for Basic/JWT/OIDC auth. Attaches to an HTTPRoute rule via an `ExtensionRef` filter           |

### How Endpoint URLs Are Resolved

The ReleaseBinding controller resolves endpoint URLs by inspecting rendered HTTPRoutes:

1. Extracts `backendRef` port from the HTTPRoute (matches to workload endpoint)
2. Extracts `hostname` from the HTTPRoute spec
3. Looks up the Gateway referenced in `parentRefs`
4. Resolves the HTTPS port from DataPlane/Environment gateway configuration
5. Constructs the invoke URL: `https://<hostname>[:<port>]/<path>`

This resolution is gateway-implementation-agnostic — it only reads standard Gateway API fields.

### Traffic Flow

```
Client
  │
  ▼
LoadBalancer (:19443)
  │
  ▼
NGINX Data Plane (TLS termination)
  │
  ├─ Match HTTPRoute rules (hostname + path)
  ├─ Enforce RateLimitPolicy (limit_req)
  ├─ Enforce AuthenticationFilter (auth_basic)
  ├─ Inject response headers (ResponseHeaderModifier)
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

- An existing OpenChoreo deployment, with or without the default kgateway installed
- Helm 3.x
- kubectl configured with cluster access
- cert-manager installed (for TLS certificate management)
- `htpasswd` (from `apache2-utils` / `httpd-tools`) for creating Basic-auth credentials

> **NGF version:** These steps use **NGF 2.6.5** (Gateway API v1.5.1), matching OpenChoreo v1.2.x. For OpenChoreo v0.x–v1.1.x (Gateway API v1.4.1), use **NGF 2.4.2** instead — see the [Compatibility](#compatibility) matrix. RateLimitPolicy and Basic-auth AuthenticationFilter require NGF ≥ 2.4.0.

### Step 1: Remove kgateway (if currently installed)

If the data plane was previously deployed with kgateway, remove the existing Gateway CR so it can be recreated with the NGF GatewayClass:

```bash
kubectl delete gateway gateway-default -n openchoreo-data-plane
```

> **Single cluster mode:** Do not remove the kgateway controller, GatewayClass, or its deployments. The control plane and observability plane gateways depend on kgateway. Only the data plane gateway is pluggable.

In multi-cluster deployments where the data plane runs on a separate cluster, kgateway can be fully removed:

```bash
# Multi-cluster only: remove kgateway entirely from the data plane cluster
kubectl delete gatewayclass kgateway
kubectl delete deployment -l app.kubernetes.io/name=kgateway -n openchoreo-data-plane
kubectl delete svc -l app.kubernetes.io/name=kgateway -n openchoreo-data-plane
```

### Step 2: Install NGINX Gateway Fabric

The NGF Helm chart installs the NGF control plane and NGF's own CRDs (`NginxProxy`, `RateLimitPolicy`, `AuthenticationFilter`, etc.). It does **not** install the upstream Gateway API CRDs — and you must **not** install them here, because OpenChoreo already provides them at the matching version (see the [Compatibility](#compatibility) note).

```bash
# The chart version is pinned for reproducibility (2.6.5 supports Gateway API v1.5.1).
# The NGINX data plane Service is a LoadBalancer so :19080/:19443 are reachable.
helm install ngf oci://ghcr.io/nginx/charts/nginx-gateway-fabric \
  --version 2.6.5 \
  --namespace openchoreo-data-plane \
  --set nginx.service.type=LoadBalancer

# Wait for the NGF control plane to be ready
kubectl wait --for=condition=available deployment/ngf-nginx-gateway-fabric \
  -n openchoreo-data-plane \
  --timeout=300s
```

> **Note:** The NGF control plane provisions a separate NGINX **data plane** Deployment + Service only once a `Gateway` CR exists (Step 5). Until then you will see only the control plane pod.

### Step 3: Verify the GatewayClass

The NGF chart creates a `nginx` GatewayClass with the correct `controllerName`. Verify it is accepted (apply it manually only if it is missing):

```bash
kubectl get gatewayclass nginx
# ACCEPTED should be True
```

For reference, the GatewayClass is:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: nginx
spec:
  controllerName: gateway.nginx.org/nginx-gateway-controller
```

### Step 4: Configure the NGINX data plane pod labels

The data plane NetworkPolicy only admits traffic to component pods from pods carrying the `openchoreo.dev/system-component=gateway` label. NGF provisions the NGINX data plane pods dynamically, so the label is set declaratively through an `NginxProxy` CR.

The NGF chart already creates an `NginxProxy` named `ngf-proxy-config` (named after the `ngf` release) and wires it to the `nginx` GatewayClass via `spec.parametersRef`, so its config applies to every Gateway of that class. Patch that existing CR rather than creating a new one.

> **Why a patch, not `pod.labels`:** NGF 2.6.x's `NginxProxy` schema has no field for arbitrary pod labels. Apply them through a `spec.kubernetes.deployment.patches` entry (`StrategicMerge`) that merges the label into the provisioned Deployment's pod template:

```bash
kubectl patch nginxproxy ngf-proxy-config -n openchoreo-data-plane --type merge -p '{
  "spec": {
    "kubernetes": {
      "deployment": {
        "patches": [
          {
            "type": "StrategicMerge",
            "value": {
              "spec": {
                "template": {
                  "metadata": {
                    "labels": {
                      "openchoreo.dev/system-component": "gateway"
                    }
                  }
                }
              }
            }
          }
        ]
      }
    }
  }
}'
```

> **Note:** Without this label, requests reach the NGINX data plane but are dropped on the way to component pods (connection reset / timeout) even though routing is otherwise correct. The patch is preserved across `helm upgrade ngf` (Helm's 3-way merge leaves fields it does not manage untouched).

### Step 5: Deploy the Data Plane with NGF

Install or upgrade the OpenChoreo data plane Helm chart with the NGF `gatewayClassName`:

```bash
helm upgrade openchoreo-data-plane oci://ghcr.io/openchoreo/helm-charts/openchoreo-data-plane \
  --version 0.0.0-latest-dev --namespace openchoreo-data-plane \
  --set gateway.gatewayClassName=nginx \
  --set gateway.httpPort=19080 \
  --set gateway.httpsPort=19443 --reuse-values
```

This creates the `gateway-default` Gateway CR referencing the `nginx` GatewayClass. NGF then provisions an NGINX data plane Deployment + Service for it, listening on the Gateway's listener ports (19080/19443) — no manual proxy port configuration is needed.

### Step 6: Verify the Gateway and data plane pod

Because the `NginxProxy` is already attached at the GatewayClass level (Step 4), no per-Gateway wiring is required — NGF applies the config to every Gateway of the `nginx` class automatically. Verify the Gateway is programmed and the provisioned data plane pod carries the label:

```bash
kubectl get gateway gateway-default -n openchoreo-data-plane
# PROGRAMMED should be True

kubectl get pods -n openchoreo-data-plane -l openchoreo.dev/system-component=gateway
# the provisioned NGINX data plane pod should be listed
```

### Step 7: Grant RBAC for NGF CRDs

The data plane service account needs permission to manage the NGF CRDs the trait applies. Create a dedicated ClusterRole and bind it to the data plane service account:

```bash
kubectl apply -f - <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: nginx-gateway-module
rules:
  - apiGroups: ["gateway.nginx.org"]
    resources: ["ratelimitpolicies", "authenticationfilters", "nginxproxies"]
    verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: nginx-gateway-module
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: nginx-gateway-module
subjects:
  - kind: ServiceAccount
    name: cluster-agent-dataplane
    namespace: openchoreo-data-plane
EOF
```

> **Note:** Without these permissions, the Release controller will fail to apply RateLimitPolicy and AuthenticationFilter resources to the data plane with a "forbidden" error. To remove these permissions later, delete the ClusterRole and ClusterRoleBinding.

### Step 8: Allow the NGF Trait on ComponentTypes

To use the `nginx-gateway-fabric-api-configuration` trait with a ComponentType or ClusterComponentType, add it to the resource's `allowedTraits`. For example, to allow it on the built-in `service` ClusterComponentType:

```bash
kubectl patch clustercomponenttype service --type=json \
  -p '[{"op":"add","path":"/spec/allowedTraits/-","value":{"kind":"ClusterTrait","name":"nginx-gateway-fabric-api-configuration"}}]'
```

### Step 9: Deploy and Invoke the Greeter Service

Apply the trait and the sample greeter Component to verify end-to-end traffic flow through NGF, including the `nginx-gateway-fabric-api-configuration` trait for API management.

```bash
kubectl apply -f nginx-gateway-fabric-api-configuration-trait.yaml
kubectl apply -f component.yaml
```

> **Note:** The greeter Component (in `component.yaml`) uses the built-in `deployment/service` ClusterComponentType and attaches the `nginx-gateway-fabric-api-configuration` trait. See [NGF API Configuration Trait](#ngf-api-configuration-trait) below for details.

Wait for the deployment to roll out:

```bash
kubectl get componentrelease
kubectl get pods -A
```

The trait renders a `RateLimitPolicy` (targeting the HTTPRoute) and, when `security` is enabled, an `AuthenticationFilter` referenced from the HTTPRoute. Verify:

```bash
DP_NS=$(kubectl get httproute -A -l openchoreo.dev/component=greeter-service \
  -o jsonpath='{.items[0].metadata.namespace}')
echo "Data plane namespace: $DP_NS"

kubectl get ratelimitpolicy -n "$DP_NS"
kubectl get authenticationfilter -n "$DP_NS"
```

**Create the Basic-auth Secret (required when `security` is enabled):**

The `AuthenticationFilter` references a Secret of type `nginx.org/htpasswd` (key `auth`) holding htpasswd-formatted users. It must live in the **same namespace as the HTTPRoute** (the data plane namespace), because NGF resolves the secretRef there.

```bash
# Generate an htpasswd file (user1 / password1)
htpasswd -bc auth user1 password1

# Create the Secret in the data plane namespace, with the NGF-required type and key
kubectl create secret generic greeter-api-basic-auth \
  -n "$DP_NS" \
  --type=nginx.org/htpasswd \
  --from-file=auth=auth
```

> **Important:** The Secret name must match `security.secretName` in the Component (`greeter-api-basic-auth`), the type must be `nginx.org/htpasswd`, and the data key must be `auth`. NGF reloads automatically once the Secret exists.

**Invoke the greeter service through NGF:**

```bash
curl "http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greeter/greet?name=OpenChoreo" \
  -u user1:password1 -v
```

Expected response:

```
Hello, OpenChoreo!
```

The response includes the `X-Gateway: Nginx` / `X-Managed-By: OpenChoreo` headers added by the `ResponseHeaderModifier` filter. Requests without valid credentials are rejected with `401 Unauthorized`; once the per-minute rate (plus burst) is exceeded, NGINX returns `429 Too Many Requests`.

**Cleanup:**

> **Order matters:** delete the Component (and its rendered resources) **before** uninstalling NGF. The Release controller cleans up the rendered RateLimitPolicy/AuthenticationFilter by listing `gateway.nginx.org` resources; if those CRDs/RBAC are gone first, finalization blocks on a "forbidden" / "no matches for kind" error and the Component gets stuck deleting.

```bash
kubectl delete component greeter-service -n default
kubectl delete secret greeter-api-basic-auth -n "$DP_NS"
```

---

## NGF API Configuration Trait

The `nginx-gateway-fabric-api-configuration` trait provides declarative API management for components routed through NGF. It creates a `RateLimitPolicy` (attached via `targetRefs`) and an `AuthenticationFilter` (attached via an `ExtensionRef` filter), and patches the HTTPRoute with a native `ResponseHeaderModifier` filter.

### Trait Schema

**Parameters (static across environments):**

| Parameter                    | Type            | Default      | Description                                                                       |
| ---------------------------- | --------------- | ------------ | -------------------------------------------------------------------------------- |
| `endpointName`               | string          | (required)   | Workload endpoint name this trait targets (resolves the HTTPRoute for `targetRefs`) |
| `rateLimiting.enabled`       | boolean         | `true`       | Enable rate limiting via `RateLimitPolicy`                                       |
| `rateLimiting.burst`         | integer         | `10`         | Burst size allowed above the rate before NGINX returns 429                       |
| `security.enabled`           | boolean         | `false`      | Enable Basic authentication via `AuthenticationFilter`                           |
| `security.secretName`        | string          | `""`         | Name of a pre-created `nginx.org/htpasswd` Secret (key `auth`) in the data plane namespace |
| `addResponseHeaders.enabled` | boolean         | `false`      | Enable response header injection (native `ResponseHeaderModifier`)              |
| `addResponseHeaders.headers` | array\<string\> | `[]`         | Headers to add to responses (format: `"Header-Name:value"`)                      |

**Environment Overrides (configurable per environment):**

| Override                         | Type    | Default | Description                                                  |
| -------------------------------- | ------- | ------- | ------------------------------------------------------------ |
| `rateLimiting.requestsPerMinute` | integer | `60`    | Rate limit threshold, rendered as the NGINX rate `<n>r/m`    |

### How It Works

The trait uses OpenChoreo's template rendering pipeline to:

1. **Create a `RateLimitPolicy`** (when `rateLimiting.enabled`) — attaches to the component's HTTPRoute via `spec.targetRefs`. NGF translates it into `limit_req_zone` (http context) + `limit_req` (location). The rate uses `r/m` so it tracks the `requestsPerMinute` override; `key: $binary_remote_addr` limits per client IP; excess beyond `burst` is rejected with `429`.

2. **Create an `AuthenticationFilter`** (when `security.enabled`) — type `Basic`, referencing the htpasswd Secret named by `security.secretName`.

3. **Patch the HTTPRoute** — adds an `ExtensionRef` filter pointing at the AuthenticationFilter (only when `security.enabled`) and a `ResponseHeaderModifier` filter (only when `addResponseHeaders.enabled`). Both patches use `oc_omit()` so disabled features add no filter entry (a null filter entry would fail HTTPRoute validation).

> **Authentication requires a Secret.** Enabling `security` attaches the filter but does not create credentials. Create the `nginx.org/htpasswd` Secret (see [Step 9](#step-9-deploy-and-invoke-the-greeter-service)); until it exists the route returns `401`/`503`.

> **Why `endpointName` is required.** Unlike Kong/APISIX (which patch the HTTPRoute by kind), NGF's `RateLimitPolicy` is a policy attachment that references the route by name. `endpointName` lets the trait resolve the generated HTTPRoute name via `oc_generate_name`.

### Example Usage

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
    - instanceName: my-api
      name: nginx-gateway-fabric-api-configuration
      kind: ClusterTrait
      parameters:
        endpointName: http
        rateLimiting:
          enabled: true
          burst: 20
        security:
          enabled: true
          secretName: my-api-basic-auth
        addResponseHeaders:
          enabled: true
          headers:
            - "X-Gateway:Nginx"
            - "X-Managed-By:OpenChoreo"
```

The rate limit can be overridden per environment via ReleaseBinding `traitEnvironmentConfigs`:

```yaml
traitEnvironmentConfigs:
  my-api:
    rateLimiting:
      requestsPerMinute: 600 # Higher limit for production
```

---

## Configuration

### Helm Values Reference

The following values control gateway behavior in the OpenChoreo data plane Helm chart:

| Value                         | Type   | Default                        | Description                                                                |
| ----------------------------- | ------ | ------------------------------ | -------------------------------------------------------------------------- |
| `gateway.gatewayClassName`    | string | `"kgateway"`                   | GatewayClass name referenced by the Gateway CR. Set to `"nginx"` for NGF   |
| `gateway.httpPort`            | int    | `9080`                         | HTTP listener port                                                         |
| `gateway.httpsPort`           | int    | `9443`                         | HTTPS listener port                                                        |
| `gateway.tls.hostname`        | string | `"*.openchoreoapis.localhost"` | Wildcard hostname for TLS certificate                                      |
| `gateway.tls.certificateRefs` | string | `"openchoreo-gateway-tls"`     | Secret name for the TLS certificate                                        |
| `gateway.infrastructure`      | object | `{}`                           | Gateway infrastructure config (labels/annotations on the Gateway CR). NGF reads data plane config from the GatewayClass-level NginxProxy, not from this field |

### NGF Helm Values

The following NGF chart values are relevant to this module:

| Value                            | Type    | Default        | Description                                                              |
| -------------------------------- | ------- | -------------- | ---------------------------------------------------------------------- |
| `nginx.service.type`             | string  | `LoadBalancer` | Service type for the provisioned NGINX data plane (use `NodePort` etc.) |
| `nginx.plus`                     | boolean | `false`        | Use NGINX Plus (required for JWT/OIDC AuthenticationFilter)            |
| `nginxGateway.snippets.enable`   | boolean | `false`        | Enable `SnippetsFilter`/`SnippetsPolicy` (not needed by this module)   |

### ClusterDataPlane/DataPlane CR Gateway Configuration

After the NGF Gateway CR is created, register it as an ingress gateway on the ClusterDataPlane/DataPlane CR so the control plane knows how to resolve endpoint URLs and route traffic:

```bash
kubectl patch clusterdataplane default --type merge -p '{
  "spec": {
    "gateway": {
      "ingress": {
        "external": {
          "name": "gateway-default",
          "namespace": "openchoreo-data-plane",
          "http": {
            "host": "openchoreoapis.localhost",
            "listenerName": "http",
            "port": 19080
          }
        }
      }
    }
  }
}'
```

| Field               | Description                                                                       |
| ------------------- | -------------------------------------------------------------------------------- |
| `name`              | Name of the Gateway CR. Must match the Gateway resource created in the data plane |
| `namespace`         | Namespace where the Gateway CR is deployed                                        |
| `http.host`         | Hostname used for routing                                                         |
| `http.listenerName` | Named listener on the Gateway CR (e.g., `http`)                                   |
| `http.port`         | Port the gateway service listens on                                              |

### Port Configuration

NGF provisions the NGINX data plane Service to listen on the ports declared in the Gateway CR listeners. The port mapping must be consistent across:

| Layer                | HTTP  | HTTPS | Configured Via                                          |
| -------------------- | ----- | ----- | ------------------------------------------------------- |
| Gateway CR listeners | 19080 | 19443 | Data plane Helm `gateway.httpPort` / `gateway.httpsPort` |
| NGINX Service ports  | 19080 | 19443 | Managed automatically by NGF from the Gateway listeners  |
| DataPlane CR         | 19080 | 19443 | `spec.gateway.ingress.external.http.port`               |

Unlike Kong, NGF automatically manages the data plane Service and port configuration based on the Gateway CR listeners — no manual proxy listen-port configuration is needed.

### NGF Policy Configuration

Rate limiting attaches via policy attachment; auth attaches via an ExtensionRef filter. Define them as CRDs in the same namespace as the HTTPRoute:

```yaml
# Rate limiting: 60 requests per minute, burst 10, per client IP
apiVersion: gateway.nginx.org/v1alpha1
kind: RateLimitPolicy
metadata:
  name: my-rate-limit
  namespace: <component-namespace>
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: <httproute-name>
  rateLimit:
    local:
      rules:
        - zoneSize: 10m
          key: "$binary_remote_addr"
          rate: 60r/m
          burst: 10
          noDelay: true
    rejectCode: 429
```

```yaml
# Basic authentication via AuthenticationFilter (attach via ExtensionRef on the route rule)
apiVersion: gateway.nginx.org/v1alpha1
kind: AuthenticationFilter
metadata:
  name: my-auth
  namespace: <component-namespace>
spec:
  type: Basic
  basic:
    secretRef:
      name: my-basic-auth   # Secret type nginx.org/htpasswd, key `auth`
    realm: "Restricted"
```

> Native Gateway API filters (`RequestHeaderModifier`, `ResponseHeaderModifier`, `URLRewrite`, `RequestRedirect`, `HTTPCORSFilter`) are supported directly on `spec.rules[].filters[]`. NGF uses only the first filter of each type per rule if duplicates exist.

---

## Maintenance

### Monitoring NGF Health

```bash
# Check the NGF control plane pod
kubectl get pods -n openchoreo-data-plane -l app.kubernetes.io/name=nginx-gateway-fabric

# Check the provisioned NGINX data plane pod(s)
kubectl get pods -n openchoreo-data-plane -l openchoreo.dev/system-component=gateway

# Check the Gateway CR programmed status
kubectl get gateway gateway-default -n openchoreo-data-plane

# View NGF control plane logs
kubectl logs -n openchoreo-data-plane deployment/ngf-nginx-gateway-fabric -f

# View NGINX data plane logs
kubectl logs -n openchoreo-data-plane -l openchoreo.dev/system-component=gateway -f
```

### Inspecting the Generated NGINX Config

```bash
# Print the generated nginx.conf from the data plane pod
POD=$(kubectl get pod -n openchoreo-data-plane \
  -l openchoreo.dev/system-component=gateway \
  -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n openchoreo-data-plane "$POD" -c nginx -- nginx -T | less
```

Look for `limit_req_zone` / `limit_req` (rate limiting) and `auth_basic` (Basic auth) to confirm the policies were applied.

### Checking Policy Status

```bash
# RateLimitPolicy status (ancestors should show Accepted=True)
kubectl describe ratelimitpolicy -n <namespace>

# AuthenticationFilter — confirm it exists and the HTTPRoute references it
kubectl get authenticationfilter -n <namespace>
kubectl get httproute <name> -n <namespace> -o yaml | grep -A4 ExtensionRef
```

### Upgrading NGF

```bash
helm upgrade ngf oci://ghcr.io/nginx/charts/nginx-gateway-fabric \
  --version 2.6.5 \
  --namespace openchoreo-data-plane \
  --reuse-values

kubectl rollout status deployment/ngf-nginx-gateway-fabric -n openchoreo-data-plane
```

> **Note:** When upgrading across a Gateway API version boundary (e.g. NGF 2.4.x → 2.6.x), make sure the OpenChoreo data plane's Gateway API version matches the target NGF release's supported version first.

### TLS Certificate Renewal

If using cert-manager, certificates are renewed automatically. To check certificate status:

```bash
kubectl get certificate -n openchoreo-data-plane
kubectl get secret openchoreo-gateway-tls -n openchoreo-data-plane -o jsonpath='{.metadata.annotations}'
```

### Troubleshooting

**Gateway not PROGRAMMED**

```bash
kubectl describe gateway gateway-default -n openchoreo-data-plane
```

Common causes:

- GatewayClass not accepted. Verify `kubectl get gatewayclass nginx` shows `ACCEPTED=True`.
- NGF control plane not running. Check `kubectl get pods -n openchoreo-data-plane -l app.kubernetes.io/name=nginx-gateway-fabric`.
- The `NginxProxy` referenced by the GatewayClass `parametersRef` does not exist in the same namespace.

**502 / connection reset reaching the backend**

The NGINX data plane pod is missing the `openchoreo.dev/system-component=gateway` label, so the data plane NetworkPolicy drops its traffic to component pods. Verify the pod is labeled and the NginxProxy carries the label patch:

```bash
kubectl get pods -n openchoreo-data-plane -l openchoreo.dev/system-component=gateway
kubectl get nginxproxy ngf-proxy-config -n openchoreo-data-plane -o jsonpath='{.spec.kubernetes.deployment.patches}'
```

If the pod has no such label, re-apply Step 4 (the patch may have been overwritten) and let NGF re-provision the data plane pod.

**HTTPRoutes not taking effect**

```bash
kubectl get httproute -A
kubectl describe httproute <name> -n <namespace>
```

Common causes:

- HTTPRoute `parentRef` name/namespace does not match the Gateway CR.
- Cross-namespace routing not allowed (Gateway must have `allowedRoutes.namespaces.from: All`).
- Backend service not found or port mismatch.

**RateLimitPolicy / AuthenticationFilter not applied**

- Confirm the resource was created in the data plane namespace (`kubectl get ratelimitpolicy,authenticationfilter -n <ns>`).
- Confirm the data plane service account has RBAC for `gateway.nginx.org` (Step 7).
- For `RateLimitPolicy`, confirm `targetRefs.name` matches the generated HTTPRoute name (`kubectl get httproute -n <ns>`).
- For auth, confirm the HTTPRoute rule carries the `ExtensionRef` filter and the htpasswd Secret exists in the same namespace.

**401 Unauthorized unexpectedly**

The route has Basic auth enabled but the htpasswd Secret is missing, in the wrong namespace, the wrong type (`nginx.org/htpasswd`), or uses the wrong data key (`auth`). Recreate it per [Step 9](#step-9-deploy-and-invoke-the-greeter-service), or disable `security`.

---

## Customization

### Selective Feature Use

Enable only the features you need — each is independently toggleable:

```yaml
traits:
  - name: nginx-gateway-fabric-api-configuration
    instanceName: my-api
    kind: ClusterTrait
    parameters:
      endpointName: http
      rateLimiting:
        enabled: true       # Only rate limiting
      security:
        enabled: false      # No auth
      addResponseHeaders:
        enabled: false      # No header injection
```

### Shared / Global Rate Limits

`RateLimitPolicy` rate limiting is local to each NGINX data plane pod (NGINX `limit_req` uses a per-instance shared memory zone). With multiple data plane replicas the effective limit is multiplied by the replica count. For a single global limit, keep the data plane at one replica, or front it with an external shared rate limiter.

### JWT / OIDC Authentication (NGINX Plus)

The trait ships Basic authentication, which works on NGINX OSS. NGF also supports JWT and OIDC via `AuthenticationFilter` (`spec.type: JWT` / `OIDC`), but these were added in NGF 2.5.0 and require **NGINX Plus** (`--set nginx.plus=true` at install). To use them, extend the trait's `SecurityConfig` schema with the JWT/OIDC fields and add the corresponding `AuthenticationFilter` spec. See the NGF [JWT](https://docs.nginx.com/nginx-gateway-fabric/traffic-security/jwt-authentication/) and [OIDC](https://docs.nginx.com/nginx-gateway-fabric/traffic-security/oidc-authentication/) docs.

### Cloud Provider Load Balancer Configuration

Add cloud-specific annotations to the NGINX data plane Service via the `ngf-proxy-config` `NginxProxy` CR. The 2.6.x schema exposes neither arbitrary pod labels nor service annotations as first-class fields, so both are applied through `patches` (`StrategicMerge`) — pod labels on `deployment.patches`, service annotations on `service.patches`:

```yaml
apiVersion: gateway.nginx.org/v1alpha2
kind: NginxProxy
metadata:
  name: ngf-proxy-config
  namespace: openchoreo-data-plane
spec:
  kubernetes:
    deployment:
      patches:
        - type: StrategicMerge
          value:
            spec:
              template:
                metadata:
                  labels:
                    openchoreo.dev/system-component: gateway
    service:
      type: LoadBalancer
      patches:
        - type: StrategicMerge
          value:
            metadata:
              annotations:
                service.beta.kubernetes.io/aws-load-balancer-type: "external"
                service.beta.kubernetes.io/aws-load-balancer-nlb-target-type: "ip"
                service.beta.kubernetes.io/aws-load-balancer-scheme: "internet-facing"
```

### Scaling the NGINX Data Plane

Set the data plane replica count via the `NginxProxy` CR:

```yaml
spec:
  kubernetes:
    deployment:
      replicas: 3
```

For production, also account for the per-replica nature of local rate limiting (see [Shared / Global Rate Limits](#shared--global-rate-limits)).

### Advanced NGINX Tuning via Snippets

For NGINX directives not exposed by the first-class CRDs, NGF supports `SnippetsFilter` (route-level) and `SnippetsPolicy` (Gateway-level). These require installing NGF with `--set nginxGateway.snippets.enable=true`. Snippets are an escape hatch — prefer `RateLimitPolicy` / `AuthenticationFilter` where they suffice. See the NGF [snippets docs](https://docs.nginx.com/nginx-gateway-fabric/traffic-management/snippets/).
