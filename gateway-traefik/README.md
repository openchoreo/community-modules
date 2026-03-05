# Traefik Gateway Module for OpenChoreo Data Plane

This document provides comprehensive documentation for integrating Traefik as the API gateway in the OpenChoreo data plane, replacing the default kgateway (Envoy-based) implementation.

## Table of Contents

- [Overview](#overview)
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
- **Middleware annotation model**: Unlike Envoy Gateway's policy `targetRefs`, Traefik attaches Middlewares to HTTPRoutes via a `traefik.io/router.middlewares` annotation. The annotation lists middleware names in `<namespace>-<name>@kubernetescrd` format.

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
# --set gateway.enabled=false disables Traefik's own Gateway CR creation;
# OpenChoreo manages the Gateway CR via its data plane Helm chart instead.
# This also avoids a type-comparison bug in traefik/templates/gateway.yaml
# that appears when providers.kubernetesGateway.enabled=true and gateway.enabled=true.
helm install traefik traefik/traefik \
    --namespace openchoreo-data-plane \
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

**Create the BasicAuth secret (required before applying the trait):**

The `traefik-api-configuration` trait's security feature references an existing Kubernetes Secret containing htpasswd credentials. Create it before deploying the component:

```bash
# Install htpasswd if not already available (apache2-utils on Debian/Ubuntu, httpd-tools on RHEL/CentOS)
# apt-get install apache2-utils  OR  yum install httpd-tools

# Create the secret with a single user
kubectl create secret generic greeter-api-basic-auth \
  --from-literal=users="$(htpasswd -nbm api-user openchoreo-secret)" \
  -n default

# Verify the secret was created
kubectl get secret greeter-api-basic-auth -n default
```

To add multiple users, create a file first:

```bash
# Generate hashes for each user and write to a file
htpasswd -cbm auth-users api-user openchoreo-secret
htpasswd -bm  auth-users consumer2 another-secret

# Create the secret from the file
kubectl create secret generic greeter-api-basic-auth \
  --from-file=users=auth-users \
  -n default
```

**Apply the ComponentType, Trait, Component, and Workload:**

```bash
kubectl apply -f traefik-api-configuration-trait.yaml
```

> **Note:** The greeter service Component uses `componentType: deployment/service` and attaches the `traefik-api-configuration` trait with `security.secretName: greeter-api-basic-auth`. The Secret must exist in the same namespace before the Middleware is reconciled.

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
curl http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greet?name=OpenChoreo -v

# With BasicAuth credentials
curl -u api-user:openchoreo-secret \
  http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greet?name=OpenChoreo -v

# Or with an explicit Authorization header
curl -H "Authorization: Basic $(echo -n 'api-user:openchoreo-secret' | base64)" \
  http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greet?name=OpenChoreo -v
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

The `traefik-api-configuration` trait provides declarative API management for components routed through Traefik. It creates `Middleware` CRDs and patches the HTTPRoute with the `traefik.io/router.middlewares` annotation automatically.

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
    kind: ComponentType
    name: deployment/service
  traits:
    - instanceName: my-api
      name: traefik-api-configuration
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

The rate limit can be overridden per environment via ReleaseBinding `traitOverrides`:

```yaml
traitOverrides:
  my-api:
    rateLimiting:
      average: 200  # Higher limit for development (requests per second)
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

### DataPlane CR Gateway Configuration

The DataPlane CR defines gateway metadata used by the control plane for endpoint URL resolution:

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: DataPlane
metadata:
  name: default
spec:
  gateway:
    publicVirtualHost: "example.com" # Domain for public endpoints
    publicHTTPSPort: 19443 # Port included in endpoint URLs
    publicHTTPPort: 19080
    publicGatewayName: "gateway-default" # Must match the Gateway CR name
    publicGatewayNamespace: "openchoreo-data-plane"
    organizationVirtualHost: "org.example.com" # Optional: org-scoped domain
    organizationHTTPSPort: 19444
```

### Environment-Level Overrides

Environments can override gateway configuration from the DataPlane:

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  gateway:
    publicVirtualHost: "prod.example.com" # Overrides DataPlane value
```

If `publicVirtualHost` is set on the Environment, its gateway config takes full precedence over the DataPlane config.

### Port Configuration

Traefik must listen on the same ports that the Gateway CR declares. The port mapping must be consistent across:

| Layer                 | HTTP  | HTTPS | Configured Via                                                       |
| --------------------- | ----- | ----- | -------------------------------------------------------------------- |
| Traefik entrypoints   | 19080 | 19443 | Traefik Helm `ports.web.port` / `ports.websecure.port`               |
| Traefik Service ports | 19080 | 19443 | Traefik Helm `ports.web.exposedPort` / `ports.websecure.exposedPort` |
| Gateway CR listeners  | 19080 | 19443 | Data plane Helm `gateway.httpPort` / `gateway.httpsPort`             |
| DataPlane CR          | 19080 | 19443 | `spec.gateway.publicHTTPPort` / `publicHTTPSPort`                    |

A mismatch at any layer will cause listener errors or broken endpoint URLs.

### Traefik-Specific Middleware Configuration

Middlewares are applied to HTTPRoutes via annotations. Define Middlewares as CRDs and reference them via the `traefik.io/router.middlewares` annotation:

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

Check that `average` is set correctly. Traefik's `rateLimit.average` is requests per `period`. The trait sets `period: 1m`, so `average` means requests per minute. Override via `traitOverrides.<instanceName>.rateLimiting.average` in the ReleaseBinding.

**Endpoint URLs not resolving**

Verify the DataPlane CR gateway config matches the actual Gateway CR:

```bash
kubectl get dataplane default -o yaml | grep -A 10 gateway
```

Ensure `publicGatewayName` and `publicGatewayNamespace` match the Gateway CR's name and namespace.

---

## Customization

### Adding Traefik Middlewares to ComponentType Templates

To apply Traefik Middlewares to all instances of a ComponentType, add annotations in the HTTPRoute template:

```yaml
# In ComponentType spec.resources
- id: httproute
  template:
    apiVersion: gateway.networking.k8s.io/v1
    kind: HTTPRoute
    metadata:
      name: ${metadata.name}
      namespace: ${metadata.namespace}
      annotations:
        traefik.io/router.middlewares: default-global-rate-limit@kubernetescrd
    spec:
      parentRefs:
        - name: gateway-default
          namespace: openchoreo-data-plane
      hostnames:
        - ${environment.publicVirtualHost}
      rules:
        - backendRefs:
            - name: ${metadata.name}
              port: 80
```

> **Note:** Traefik Middleware annotations on HTTPRoutes create a dependency on Traefik as the gateway implementation. Standard Gateway API fields remain portable.

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

3. Update the DataPlane CR:

```yaml
spec:
  gateway:
    publicHTTPPort: 80
    publicHTTPSPort: 443
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
