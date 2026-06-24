# Traefik Gateway Module for OpenChoreo Data Plane

This document provides comprehensive documentation for integrating Traefik as the API gateway in the OpenChoreo data plane, replacing the default kgateway (Envoy-based) implementation.

## Table of Contents

- [Overview](#overview)
- [Compatibility](#compatibility)
- [High-Level Architecture](#high-level-architecture)
- [Installation](#installation)
- [Configuration](#configuration)
- [Maintenance](#maintenance)
- [Customization](#customization)

---

## Overview

OpenChoreo uses the [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) as the standard API for exposing component endpoints to public or internal networks. Because the Gateway API is a vendor-neutral Kubernetes standard, the gateway layer is easily pluggable and extensible across vendors — any Gateway API-compliant controller can serve as the ingress layer without changes to the control plane or the OpenChoreo ComponentTypes.

The Traefik Gateway module replaces the default kgateway (Envoy) with [Traefik Proxy v3](https://doc.traefik.io/traefik/), a cloud-native reverse proxy and load balancer with native Kubernetes Gateway API support. Traefik provides API management capabilities such as rate limiting, authentication, request/response header manipulation, and circuit breaking — all through Kubernetes-native `Middleware` CRDs.

### Key Design Decisions

- **Standard Gateway API as the contract**: OpenChoreo components create `HTTPRoute` resources that reference a `Gateway` by name. The gateway implementation is transparent to the control plane.
- **Helm-driven configuration**: The `gatewayClassName` in the data plane Helm chart determines which gateway controller processes the `Gateway` CR and its routes.
- **No control plane changes required**: Switching gateways only requires data plane reconfiguration. The rendering pipeline, endpoint resolution, and release controllers work unchanged.
- **Middleware via ExtensionRef**: Traefik attaches Middlewares to HTTPRoutes via Gateway API `ExtensionRef` filters in the HTTPRoute spec. The `traefik.io/router.middlewares` annotation is **not** read by the `kubernetesgateway` provider and is not used by this module (see [Key Differences](#key-differences-from-other-gateway-modules)).

---

## Compatibility

Traefik is a pluggable replacement for the default kgateway in the OpenChoreo data plane. This module installs Traefik independently via the `traefik/traefik` Helm chart. Traefik's Kubernetes Gateway API provider targets a **specific** Gateway API version per release, so the Traefik version must match the Gateway API version your OpenChoreo release ships with its data plane:

| OpenChoreo version | Kubernetes Gateway API | Traefik | Helm chart |
| ------------------ | ---------------------- | ------- | ------------------- |
| v0.x.x             | v1.4.1                 | v3.6.x  | 39.0.x              |
| v1.0.x             | v1.4.1                 | v3.6.x  | 39.0.x              |
| v1.1.x             | v1.4.1                 | v3.6.x  | 39.0.x              |
| v1.2.x             | v1.5.1                 | v3.7.x  | 41.0.0 (app v3.7.5) |

> **Note:** Traefik's Gateway API provider pins one Gateway API version per release — **v3.6.x targets Gateway API v1.4.0** and **v3.7.0+ targets v1.5.1** ([Traefik v3 migration notes](https://doc.traefik.io/traefik/migrate/v3/)). Installing a Traefik version whose target Gateway API differs from the CRDs OpenChoreo installs can cause the Gateway provider to reject or ignore HTTPRoutes. This module was verified end-to-end on **OpenChoreo v1.2.x (Gateway API v1.5.1) with Traefik v3.7.5** (chart 41.0.0). The trait uses the stable `traefik.io/v1alpha1` Middleware API, which is unaffected by Gateway API version differences.

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
└─────────────────────────────┬───────────────────────────────┘
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
│              │         │ backendRef         │               │
│              │   ┌─────┴──────┐             │               │
│              │   │ HTTPRoute  │             │               │
│              │   │            │             │               │
│              │   │ annotation ◄──────────── │ ──────────┐   │
│              │   │ (middlewares)            │           │   │
│              │   │ parentRef ─┼────┐        │           │   │
│              │   └────────────┘    │        │           │   │
│              │                     │        │           │   │
│              │   ┌────────────┐    │        │           │   │
│              │   │ Middleware │ ───┘        │           │   │
│              │   │ (rate-limit│             │           │   │
│              │   │  auth,     │             │           │   │
│              │   │  headers)  │             │           │   │
│              │   └────────────┘             │           │   │
│              └──────────────────────────────┘           │   │
│                                    │                    │   │
│              ┌─────────────────────┴──────────┐         │   │
│              │   Gateway CR                   │         │   │
│              │   name: gateway-default        │         │   │
│              │   gatewayClassName: traefik ◄── Configurable  │
│              │                                │             │
│              │   listeners:                   │             │
│              │     - http  (port 19080)       │             │
│              │     - https (port 19443, TLS)  │             │
│              └───────────────┬────────────────┘             │
│                              │ watches                      │
│              ┌───────────────┴────────────────┐             │
│              │   Traefik Controller           │             │
│              │                                │             │
│              │   - Watches Gateway, HTTPRoute │             │
│              │   - Watches Middleware CRDs    │             │
│              │   - Builds dynamic config      │             │
│              │   - Routes and applies policy  │             │
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

| Component              | Role                                                                                                                                |
| ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| **Traefik Controller** | Watches Gateway API resources (Gateway, HTTPRoute) and Traefik Middleware CRDs, builds Traefik's dynamic routing configuration      |
| **Traefik Proxy**      | Processes ingress traffic, terminates TLS, applies middleware chains (rate limiting, auth, headers), and routes to backend services |
| **Gateway CR**         | Kubernetes Gateway API resource that defines listeners (ports, protocols, TLS). Created by Helm during data plane installation      |
| **GatewayClass**       | Declares that `traefik.io/gateway-controller` handles Gateway CRs with class `traefik`                                              |
| **HTTPRoute**          | Gateway API route resource. Created by OpenChoreo release pipeline per component. References the Gateway CR via `parentRefs`        |
| **Middleware**         | Traefik-specific CRD for applying API management policies (rate limiting, ForwardAuth, headers) to routes via annotations           |

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
Traefik Proxy (TLS termination)
  │
  ├─ Check rate limit (via RateLimit Middleware)
  ├─ Verify auth (via BasicAuth Middleware)
  ├─ Inject request headers (via Headers Middleware)
  ├─ Match HTTPRoute rules (hostname + path)
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

### Step 1: Remove kgateway (if currently installed)

If the data plane was previously deployed with kgateway, remove the existing Gateway CR so it can be recreated with the Traefik GatewayClass:

```bash
# Delete the existing data plane Gateway CR
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

### Step 2: Install Traefik

```bash
# Add the Traefik Helm repository
helm repo add traefik https://traefik.github.io/charts
helm repo update

# Install Traefik v3 with Gateway API support and custom ports.
# --version pins the chart to one whose appVersion matches your OpenChoreo release's
#   Gateway API version (see Compatibility). 41.0.0 → Traefik v3.7.5 → Gateway API v1.5.1
#   (OpenChoreo v1.2.x). For OpenChoreo v0.x–v1.1.x (Gateway API v1.4.1), use a 39.0.x chart
#   (Traefik v3.6.x).
# --set-string deployment.podLabels... adds the gateway identity label REQUIRED by the
#   OpenChoreo data plane NetworkPolicy. Without it, the policy blocks Traefik→workload
#   traffic and every request returns 502 Bad Gateway. The key contains a dot, so it is
#   single-quoted and passed via --set-string (an unquoted --set splits on the dot).
# --set gateway.enabled=false disables Traefik's own Gateway CR creation;
#   OpenChoreo manages the Gateway CR via its data plane Helm chart instead.
helm install traefik traefik/traefik \
    --version 41.0.0 \
    --namespace openchoreo-data-plane \
    --set-string 'deployment.podLabels.openchoreo\.dev/system-component=gateway' \
    --set ports.web.port=19080 \
    --set ports.web.expose.default=true \
    --set ports.web.exposedPort=19080 \
    --set ports.websecure.port=19443 \
    --set ports.websecure.expose.default=true \
    --set ports.websecure.exposedPort=19443 \
    --set service.type=LoadBalancer \
    --set providers.kubernetesGateway.enabled=true \
    --set gateway.enabled=false

# Wait for Traefik to be ready
kubectl wait --for=condition=ready pod \
  -l app.kubernetes.io/name=traefik \
  -n openchoreo-data-plane \
  --timeout=300s
```

### Step 3: Create the Traefik GatewayClass

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: traefik
spec:
  controllerName: traefik.io/gateway-controller
EOF
```

Verify:

```bash
kubectl get gatewayclass traefik
# ACCEPTED should be True
```

### Step 4: Deploy the Data Plane with Traefik

Install or upgrade the OpenChoreo data plane Helm chart with the Traefik `gatewayClassName`:

```bash
helm upgrade openchoreo-data-plane oci://ghcr.io/openchoreo/helm-charts/openchoreo-data-plane \
  --version 0.0.0-latest-dev --namespace openchoreo-data-plane \
  --set gateway.gatewayClassName=traefik \
  --set gateway.httpPort=19080 --reuse-values
```

This creates the `gateway-default` Gateway CR referencing the `traefik` GatewayClass instead of `kgateway`.

### Step 5: Verify the Installation

```bash
# Check Traefik pod status
  kubectl get pods -n openchoreo-data-plane -l app.kubernetes.io/name=traefik

# Check the Gateway CR status (PROGRAMMED should be True)
kubectl get gateway gateway-default -n openchoreo-data-plane

# Check Gateway listeners
kubectl describe gateway gateway-default -n openchoreo-data-plane
```

Expected pods:

| Pod         | Role                                                                             |
| ----------- | -------------------------------------------------------------------------------- |
| `traefik-*` | Traefik proxy and controller — watches Gateway API resources and Middleware CRDs |

### Step 6: Grant RBAC for Traefik CRDs

The data plane service account needs permissions to manage Traefik Middleware resources. Patch the ClusterRole:

```bash
kubectl patch clusterrole cluster-agent-dataplane-openchoreo-data-plane --type=json \
  -p '[{"op":"add","path":"/rules/-","value":{
    "apiGroups":["traefik.io"],
    "resources":["middlewares","middlewaretcps","ingressroutes","ingressroutetcps","ingressrouteudps","tlsoptions","tlsstores","traefikservices","serverstransports"],
    "verbs":["*"]
  }}]'
```

> **Note:** Without this, the Release controller will fail to apply Middleware resources to the data plane with a "forbidden" error.

### Step 7: Deploy and Invoke the Greeter Service

Deploy the sample greeter service to verify end-to-end traffic flow through Traefik, including the `traefik-api-configuration` trait for API management.

**Apply the ClusterTrait and add it to the ComponentType:**

```bash
# Apply the traefik-api-configuration ClusterTrait
kubectl apply -f traefik-api-configuration-trait.yaml

# Allow the trait on the built-in service ClusterComponentType
kubectl patch clustercomponenttype service --type=json \
  -p '[{"op":"add","path":"/spec/allowedTraits/-","value":{"kind":"ClusterTrait","name":"traefik-api-configuration"}}]'
```

**Deploy the greeter Component and Workload:**

```bash
kubectl apply -f component.yaml
```

> **Note:** `component.yaml` references the built-in `deployment/service` ClusterComponentType and attaches the `traefik-api-configuration` trait with `security.secretName: greeter-api-basic-auth`.

**Create the BasicAuth secret in the component's data plane namespace:**

The trait's security feature renders a Traefik `BasicAuth` Middleware **into the component's data plane namespace** (e.g. `dp-default-default-development-<hash>`), and Traefik looks for the referenced Secret in that **same namespace — not in `default`**. Create it there after the component is deployed. Traefik reconciles once the Secret appears; until then, BasicAuth requests fail and Traefik drops the router (every request returns 404/401 with `secret ... not found` in the Traefik logs).

```bash
# htpasswd comes from apache2-utils (Debian/Ubuntu) or httpd-tools (RHEL/CentOS).
# No htpasswd? Use: users="api-user:$(openssl passwd -apr1 openchoreo-secret)"

# Find the component's data plane namespace
DP_NS=$(kubectl get httproute -A -l openchoreo.dev/component=greeter-service \
  -o jsonpath='{.items[0].metadata.namespace}')
echo "Data plane namespace: $DP_NS"

# Create the htpasswd Secret in that namespace
kubectl create secret generic greeter-api-basic-auth \
  --from-literal=users="$(htpasswd -nbm api-user openchoreo-secret)" \
  -n "$DP_NS"
```

> **Multiple users:** append more `user:hash` lines to the `users` value (one per line).

**Wait for the deployment to roll out:**

```bash
# Check that the release pipeline has completed
kubectl get componentrelease

# Check the release status
kubectl get release

# Wait for the greeter pod to be ready
kubectl get pods -A

# Verify Middleware resources are created
kubectl get middleware -A
```

**Invoke the greeter service through Traefik:**

```bash
# Without auth (returns 401 if security.enabled=true)
curl http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greeter/greet?name=OpenChoreo -v

# With BasicAuth credentials
curl -u api-user:openchoreo-secret \
  http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greeter/greet?name=OpenChoreo -v

# Or with an explicit Authorization header
curl -H "Authorization: Basic $(echo -n 'api-user:openchoreo-secret' | base64)" \
  http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greeter/greet?name=OpenChoreo -v
```

Expected response:

```
Hello, OpenChoreo!
```

The response headers will include Traefik proxy metadata (`server: traefik`) and any custom headers added by the `Headers` Middleware (e.g., `X-Gateway: Traefik`).

**Cleanup:**

```bash
kubectl delete component greeter-service -n default
kubectl delete workload greeter-service-workload -n default
```

### Traefik API Configuration Trait

The `traefik-api-configuration` trait provides declarative API management for components routed through Traefik. It creates `Middleware` CRDs and attaches them to the HTTPRoute via Gateway API `ExtensionRef` filters automatically.

#### Trait Schema

**Parameters (static across environments):**

| Parameter               | Type            | Default | Description                                                                    |
| ----------------------- | --------------- | ------- | ------------------------------------------------------------------------------ |
| `rateLimiting.enabled`  | boolean         | `true`  | Enable rate limiting middleware                                                 |
| `rateLimiting.burst`    | integer         | `200`   | Burst size: max requests allowed above the average rate in a short window       |
| `security.enabled`      | boolean         | `false` | Enable BasicAuth middleware for API key authentication                          |
| `security.secretName`   | string          | `""`    | Name of an existing Kubernetes Secret containing htpasswd-formatted credentials |
| `addHeaders.enabled`          | boolean              | `false` | Enable header injection middleware                                              |
| `addHeaders.requestHeaders`   | map\<string,string\> | `{}`    | Headers to add to upstream requests (e.g. `{"X-Gateway": "Traefik"}`)         |
| `addHeaders.responseHeaders`  | map\<string,string\> | `{}`    | Headers to add to downstream responses (e.g. `{"X-Powered-By": "OpenChoreo"}`) |

**Environment Overrides (configurable per environment):**

| Override               | Type    | Default | Description                          |
| ---------------------- | ------- | ------- | ------------------------------------ |
| `rateLimiting.average` | integer | `100`   | Average requests allowed per second  |

#### How It Works

The trait uses OpenChoreo's template rendering pipeline to:

1. **Create Traefik Middleware CRDs** — one for each enabled policy feature (rate-limiting, security). Each Middleware is conditionally created based on its `enabled` flag.

2. **Patch the HTTPRoute filters** — appends `ExtensionRef` filters to the HTTPRoute rules for each enabled Middleware (rate limiting, security). The `ExtensionRef` filter type is the correct mechanism for attaching Traefik Middlewares when using the `kubernetesgateway` provider. The `traefik.io/router.middlewares` annotation is **ignored** by the Gateway provider and must not be used.

3. **Patch the HTTPRoute filters (headers)** — when `addHeaders` is enabled, appends a standard Gateway API `RequestHeaderModifier` filter. No Traefik Middleware is created for this feature.

#### Key Differences from Other Gateway Modules

| Feature | Kong | Envoy Gateway | Traefik |
| ------- | ---- | ------------- | ------- |
| Rate limiting | `KongPlugin` annotation | `BackendTrafficPolicy` targetRef | `Middleware` + `ExtensionRef` filter |
| Authentication (BasicAuth) | `KongPlugin` annotation | `SecurityPolicy` targetRef | `Middleware` (BasicAuth + Secret ref) + `ExtensionRef` filter |
| Header injection | `KongPlugin` annotation | `RequestHeaderModifier` filter patch | `RequestHeaderModifier` filter patch |

> **Why not the `traefik.io/router.middlewares` annotation?** Traefik has two Kubernetes providers: `kubernetesCRD` (reads IngressRoute, Middleware, etc.) and `kubernetesgateway` (reads Gateway API resources). HTTPRoutes are processed exclusively by the `kubernetesgateway` provider, which does **not** read the `traefik.io/router.middlewares` annotation. Middleware attachment from the Gateway provider requires `ExtensionRef` filters in the HTTPRoute spec.

#### Example Usage

Before applying, create the BasicAuth secret:

```bash
kubectl create secret generic my-api-basic-auth \
  --from-literal=users="$(htpasswd -nbm api-user mysecretkey)" \
  -n default
```

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
      name: traefik-api-configuration
      kind: ClusterTrait
      parameters:
        rateLimiting:
          enabled: true
          burst: 200
        security:
          enabled: true
          secretName: my-api-basic-auth  # Pre-created Kubernetes Secret with htpasswd users
        addHeaders:
          enabled: true
          requestHeaders:
            - "X-Gateway:Traefik"
            - "X-Managed-By:OpenChoreo"
          responseHeaders:
            - "X-Powered-By:OpenChoreo"
```

The rate limit can be overridden per environment via ReleaseBinding `traitEnvironmentConfigs`:

```yaml
traitEnvironmentConfigs:
  my-api:
    rateLimiting:
      average: 200  # requests per minute (the trait uses period 1m)
```

---

## Configuration

### Helm Values Reference

The following values control gateway behavior in the data plane Helm chart:

| Value                         | Type   | Default                        | Description                                                                    |
| ----------------------------- | ------ | ------------------------------ | ------------------------------------------------------------------------------ |
| `gateway.gatewayClassName`    | string | `"kgateway"`                   | GatewayClass name referenced by the Gateway CR. Set to `"traefik"` for Traefik |
| `gateway.httpPort`            | int    | `9080`                         | HTTP listener port                                                             |
| `gateway.httpsPort`           | int    | `9443`                         | HTTPS listener port                                                            |
| `gateway.tls.hostname`        | string | `"*.openchoreoapis.localhost"` | Wildcard hostname for TLS certificate                                          |
| `gateway.tls.certificateRefs` | string | `"openchoreo-gateway-tls"`     | Secret name for the TLS certificate                                            |
| `gateway.infrastructure`      | object | `{}`                           | Cloud provider load balancer annotations                                       |

### ClusterDataPlane/DataPlane CR Gateway Configuration

After the Traefik Gateway CR is created, register it as an ingress gateway on the ClusterDataPlane/DataPlane CR so the control plane knows how to resolve endpoint URLs and route traffic.

The gateway configuration uses `spec.gateway.ingress` with named gateway slots (`external`, `internal`, etc.). Each slot references a Gateway CR and specifies the HTTP listener details:

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

| Field                  | Description                                                                                             |
| ---------------------- | ------------------------------------------------------------------------------------------------------- |
| `name`                 | Name of the Gateway CR. Must match the Gateway resource created in the data plane                       |
| `namespace`            | Namespace where the Gateway CR is deployed                                                              |
| `http.host`            | Hostname used for routing. For ClusterIP gateways, use `<service-name>.<namespace>` (in-cluster DNS)    |
| `http.listenerName`    | Named listener on the Gateway CR (e.g., `http`)                                                         |
| `http.port`            | Port the gateway service listens on                                                                     |

> **Note:** For internal (ClusterIP) gateways, set `http.host` to the in-cluster DNS name of the gateway service (e.g., `gateway-internal.openchoreo-data-plane`). For LoadBalancer gateways, use the external hostname or IP assigned by the cloud provider.

### Environment-Level Overrides

Environments can override the ClusterDataPlane/DataPlane gateway configuration with dedicated gateway resources. This is useful when different environments (e.g., production) need their own gateway with separate listeners, ports, or hostnames.

**1. Create a dedicated Gateway CR for the environment:**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: gateway-production
  namespace: openchoreo-data-plane
  labels:
    app.kubernetes.io/component: gateway
    app.kubernetes.io/part-of: openchoreo
spec:
  gatewayClassName: traefik
  listeners:
    - name: http
      port: 19081
      protocol: HTTP
      allowedRoutes:
        namespaces:
          from: All
```

**2. Patch the Environment CR to reference the dedicated gateway:**

```bash
kubectl patch environment production -n default --type merge -p '{
  "spec": {
    "gateway": {
      "ingress": {
        "external": {
          "name": "gateway-production",
          "namespace": "openchoreo-data-plane",
          "http": {
            "host": "openchoreoapis.localhost",
            "listenerName": "http",
            "port": 19081
          }
        }
      }
    }
  }
}'
```

When gateway configuration is set on an Environment, it takes full precedence over the ClusterDataPlane/DataPlane gateway config for that environment.

### Port Configuration

Traefik must listen on the same ports that the Gateway CR declares. The port mapping must be consistent across:

| Layer                 | HTTP  | HTTPS | Configured Via                                                       |
| --------------------- | ----- | ----- | -------------------------------------------------------------------- |
| Traefik entrypoints   | 19080 | 19443 | Traefik Helm `ports.web.port` / `ports.websecure.port`               |
| Traefik Service ports | 19080 | 19443 | Traefik Helm `ports.web.exposedPort` / `ports.websecure.exposedPort` |
| Gateway CR listeners  | 19080 | 19443 | Data plane Helm `gateway.httpPort` / `gateway.httpsPort`             |
| DataPlane CR          | 19080 | 19443 | `spec.gateway.ingress.external.http.port`                            |

A mismatch at any layer will cause listener errors or broken endpoint URLs.

### Traefik-Specific Middleware Configuration

Middlewares are attached to HTTPRoutes via Gateway API `ExtensionRef` filters. Define Middlewares as CRDs and reference them from the HTTPRoute's `rules[].filters`:

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: rate-limit
  namespace: <component-namespace>
spec:
  rateLimit:
    average: 100
    burst: 200
    period: 1m
```

Apply to an HTTPRoute via an `ExtensionRef` filter (required when using the `kubernetesgateway` provider):

```yaml
spec:
  rules:
    - filters:
        - type: ExtensionRef
          extensionRef:
            group: traefik.io
            kind: Middleware
            name: rate-limit
```

For multiple middlewares, add multiple `ExtensionRef` filter entries:

```yaml
spec:
  rules:
    - filters:
        - type: ExtensionRef
          extensionRef:
            group: traefik.io
            kind: Middleware
            name: rate-limit
        - type: ExtensionRef
          extensionRef:
            group: traefik.io
            kind: Middleware
            name: basic-auth
```

> **Note:** The `traefik.io/router.middlewares` annotation is only read by the `kubernetesCRD` provider (for IngressRoute resources). It is **not** processed by the `kubernetesgateway` provider that handles HTTPRoutes. Always use `ExtensionRef` filters when attaching Traefik Middlewares to Gateway API HTTPRoutes.

---

## Maintenance

### Monitoring Traefik Health

```bash
# Check Traefik pod status
kubectl get pods -n openchoreo-data-plane -l app.kubernetes.io/name=traefik

# Check Gateway CR programmed status
kubectl get gateway gateway-default -n openchoreo-data-plane

# View Traefik logs
kubectl logs -n openchoreo-data-plane -l app.kubernetes.io/name=traefik -f

# Check Middleware status
kubectl get middleware -A
```

### Accessing the Traefik Dashboard

The Traefik dashboard provides runtime visibility into routers, services, and middleware configuration:

```bash
# Port-forward to the Traefik dashboard (port 9000 by default)
kubectl port-forward -n openchoreo-data-plane \
  $(kubectl get pod -n openchoreo-data-plane -l app.kubernetes.io/name=traefik \
    -o jsonpath='{.items[0].metadata.name}') 9000:9000 &

# Open dashboard in browser
open http://localhost:9000/dashboard/
```

Alternatively, use the Traefik API:

```bash
# List all routers
curl http://localhost:9000/api/http/routers

# List all services
curl http://localhost:9000/api/http/services

# List all middlewares
curl http://localhost:9000/api/http/middlewares

# Check Traefik status
curl http://localhost:9000/api/overview
```

> **Note:** The Traefik dashboard is disabled by default in the Helm chart for security reasons. Enable it with `--set dashboard.enabled=true` when installing Traefik for development use. Do not expose it publicly in production.

### Upgrading Traefik

```bash
# Update Helm repo
helm repo update traefik

# Upgrade Traefik release
helm upgrade traefik traefik/traefik \
  --namespace openchoreo-data-plane \
  --reuse-values

# Verify pod is restarted
kubectl rollout status deployment/traefik -n openchoreo-data-plane
```

### TLS Certificate Renewal

If using cert-manager, certificates are renewed automatically. To check certificate status:

```bash
# Check certificate status
kubectl get certificate -n openchoreo-data-plane

# Check secret expiry
kubectl get secret openchoreo-gateway-tls -n openchoreo-data-plane \
  -o jsonpath='{.metadata.annotations}'
```

For manual certificate rotation:

```bash
# Delete the secret to trigger re-issuance (cert-manager)
kubectl delete secret openchoreo-gateway-tls -n openchoreo-data-plane

# Or update the secret directly
kubectl create secret tls openchoreo-gateway-tls \
  --cert=tls.crt --key=tls.key \
  -n openchoreo-data-plane --dry-run=client -o yaml | kubectl apply -f -
```

### Enabling Debug Logs

To enable debug-level logging for Traefik, set the log level via the Helm values:

```bash
helm upgrade traefik traefik/traefik \
  --namespace openchoreo-data-plane \
  --set logs.general.level=DEBUG \
  --reuse-values
```

Available log levels: `DEBUG`, `INFO`, `WARN`, `ERROR`.

To enable access logging (logs every HTTP request):

```bash
helm upgrade traefik traefik/traefik \
  --namespace openchoreo-data-plane \
  --set logs.access.enabled=true \
  --reuse-values
```

### Troubleshooting

**Gateway not PROGRAMMED**

```bash
kubectl describe gateway gateway-default -n openchoreo-data-plane
```

Common causes:

- GatewayClass not found or not accepted. Verify `kubectl get gatewayclass traefik`.
- Traefik not running. Check `kubectl get pods -n openchoreo-data-plane -l app.kubernetes.io/name=traefik`.
- Gateway API provider not enabled. Ensure `providers.kubernetesGateway.enabled=true` in the Traefik Helm values.

**HTTPRoutes not taking effect**

```bash
# Check HTTPRoute status
kubectl get httproute -A

# Describe a specific route
kubectl describe httproute <name> -n <namespace>
```

Common causes:

- HTTPRoute `parentRef` name/namespace does not match the Gateway CR.
- Cross-namespace routing not allowed (Gateway must have `allowedRoutes.namespaces.from: All`).
- Backend service not found or port mismatch.

**Middleware not applied**

```bash
# Check Middleware resources exist in the correct namespace
kubectl get middleware -A

# Describe the Middleware
kubectl describe middleware <name> -n <namespace>

# Check the HTTPRoute annotation
kubectl get httproute <name> -n <namespace> -o yaml | grep -A 5 annotations
```

Common causes:

- Middleware annotation format incorrect. Must be `<namespace>-<middleware-name>@kubernetescrd` (note the namespace prefix).
- Middleware is in a different namespace than the HTTPRoute. Traefik requires Middlewares to be in the same namespace or use a `MiddlewareTCP` cross-namespace reference.
- Missing RBAC for the cluster agent to create `traefik.io` resources (see Step 6).

**Rate limiting not enforced**

Verify the RateLimit Middleware configuration:

```bash
kubectl describe middleware <name>-rate-limiting -n <namespace>
```

Check that `average` is set correctly. Traefik's `rateLimit.average` is requests per `period`. The trait sets `period: 1m`, so `average` means requests per minute. Override via `traitEnvironmentConfigs.<instanceName>.rateLimiting.average` in the ReleaseBinding.

**Endpoint URLs not resolving**

Verify the ClusterDataPlane/DataPlane CR gateway config matches the actual Gateway CR:

```bash
kubectl get clusterdataplane default -o yaml | grep -A 15 gateway
```

Ensure `spec.gateway.ingress.external.name` and `namespace` match the Gateway CR's name and namespace.

---

## Customization

### Adding Traefik Middlewares to ComponentType Templates

To apply Traefik Middlewares to all instances of a ComponentType, add `ExtensionRef` filters in the HTTPRoute template (the Middleware must already exist in the route's namespace):

```yaml
# In ComponentType spec.resources
- id: httproute
  template:
    apiVersion: gateway.networking.k8s.io/v1
    kind: HTTPRoute
    metadata:
      name: ${metadata.name}
      namespace: ${metadata.namespace}
    spec:
      parentRefs:
        - name: gateway-default
          namespace: openchoreo-data-plane
      hostnames:
        - ${environment.publicVirtualHost}
      rules:
        - filters:
            - type: ExtensionRef
              extensionRef:
                group: traefik.io
                kind: Middleware
                name: global-rate-limit
          backendRefs:
            - name: ${metadata.name}
              port: 80
```

> **Note:** `ExtensionRef` filters that reference Traefik Middlewares create a dependency on Traefik as the gateway implementation. Standard Gateway API fields remain portable. The `traefik.io/router.middlewares` annotation is **not** used — it is ignored by the `kubernetesgateway` provider.

### Custom Listener Ports

To use non-default ports (e.g., standard 80/443):

1. Configure Traefik to use the desired ports:

```bash
helm upgrade traefik traefik/traefik \
  --namespace openchoreo-data-plane \
  --set ports.web.port=80 \
  --set ports.web.exposedPort=80 \
  --set ports.websecure.port=443 \
  --set ports.websecure.exposedPort=443 \
  --reuse-values
```

2. Update the data plane Helm values:

```bash
--set gateway.httpPort=80
--set gateway.httpsPort=443
```

3. Update the ClusterDataPlane/DataPlane CR:

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
            "port": 80
          }
        }
      }
    }
  }
}'
```

### Cloud Provider Load Balancer Configuration

Use the `gateway.infrastructure` value to add cloud-specific annotations to the Gateway's generated Service, or configure Traefik's service annotations directly:

```bash
helm upgrade traefik traefik/traefik \
  --namespace openchoreo-data-plane \
  --set service.annotations."service\.beta\.kubernetes\.io/aws-load-balancer-type"=external \
  --set service.annotations."service\.beta\.kubernetes\.io/aws-load-balancer-nlb-target-type"=ip \
  --reuse-values
```

Alternatively, use the `gateway.infrastructure` Helm value in the OpenChoreo data plane chart:

```yaml
gateway:
  infrastructure:
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: "external"
      service.beta.kubernetes.io/aws-load-balancer-nlb-target-type: "ip"
      service.beta.kubernetes.io/aws-load-balancer-scheme: "internet-facing"
```

### Scaling Traefik for Production

```bash
# Scale the Traefik deployment
kubectl scale deployment traefik -n openchoreo-data-plane --replicas=3
```

For production Helm values:

```yaml
# traefik-production-values.yaml
deployment:
  replicas: 3
resources:
  requests:
    cpu: "100m"
    memory: "128Mi"
  limits:
    cpu: "500m"
    memory: "512Mi"
```

Apply with:

```bash
helm upgrade traefik traefik/traefik \
  --namespace openchoreo-data-plane \
  --values traefik-production-values.yaml \
  --reuse-values
```

### Per-IP Rate Limiting

For rate limiting based on client IP (rather than per-router), extend the `RateLimit` Middleware with `sourceCriterion`:

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: per-ip-rate-limit
spec:
  rateLimit:
    average: 100
    burst: 200
    period: 1m
    sourceCriterion:
      ipStrategy:
        depth: 1 # Use the first IP in X-Forwarded-For
```

To expose this in the trait, extend the `rateLimiting` schema section with a `perClientIP` boolean parameter and add the `sourceCriterion` block conditionally to the Middleware template.

### Enabling Traefik Enterprise Features

For production deployments requiring advanced API management:

- **Traefik Hub**: API management, developer portal, and analytics via [Traefik Hub](https://traefik.io/traefik-hub/)
- **Circuit Breaker**: Use the `circuitBreaker` Middleware for fault tolerance
- **Retry**: Use the `retry` Middleware to retry failed requests
- **Compress**: Use the `compress` Middleware for gzip/brotli response compression

See the [Traefik documentation](https://doc.traefik.io/traefik/) for full Middleware reference.
