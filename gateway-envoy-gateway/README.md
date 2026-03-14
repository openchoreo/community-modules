# Envoy Gateway Module for OpenChoreo Data Plane

This document provides comprehensive documentation for integrating Envoy Gateway as the API gateway in the OpenChoreo data plane, replacing the default kgateway (Envoy-based) implementation.

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

The Envoy Gateway module replaces the default kgateway with [Envoy Gateway](https://gateway.envoyproxy.io/), an open-source implementation of the Kubernetes Gateway API built on top of [Envoy Proxy](https://www.envoyproxy.io/). It provides advanced API management capabilities such as rate limiting, JWT authentication, response header injection, and observability — all through Kubernetes-native CRDs that extend the standard Gateway API.

### Key Design Decisions

- **Standard Gateway API as the contract**: OpenChoreo components create `HTTPRoute` resources that reference a `Gateway` by name. The gateway implementation is transparent to the control plane.
- **Helm-driven configuration**: The `gatewayClassName` in the data plane Helm chart determines which gateway controller processes the `Gateway` CR and its routes.
- **No control plane changes required**: Switching gateways only requires data plane reconfiguration. The rendering pipeline, endpoint resolution, and release controllers work unchanged.
- **Policy attachment model**: Unlike Kong's annotation-driven plugin system, Envoy Gateway uses `BackendTrafficPolicy` and `SecurityPolicy` resources that reference HTTPRoutes via `targetRefs`. No annotations are required on HTTPRoutes.

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
│              │   │ parentRef ─┼────┐        │               │
│              │   └────────────┘    │        │               │
│              │         ▲           │        │               │
│              │         │ targetRef │        │               │
│              │   ┌─────┴──────┐   │        │               │
│              │   │  Backend-  │   │        │               │
│              │   │  Traffic-  │   │        │               │
│              │   │  Policy    │   │        │               │
│              │   └────────────┘   │        │               │
│              │   ┌────────────┐   │        │               │
│              │   │  Security- │   │        │               │
│              │   │  Policy    │   │        │               │
│              │   └────────────┘   │        │               │
│              └────────────────────┼────────┘               │
│                                   │                        │
│              ┌────────────────────┴──────────┐             │
│              │   Gateway CR                  │             │
│              │   name: gateway-default       │             │
│              │   gatewayClassName:           │             │
│              │     envoy-gateway  ◄──── Configurable       │
│              │                               │             │
│              │   listeners:                  │             │
│              │     - http  (port 19080)      │             │
│              │     - https (port 19443, TLS) │             │
│              └───────────────┬───────────────┘             │
│                              │ watches                     │
│              ┌───────────────┴────────────────┐            │
│              │   Envoy Gateway Controller     │            │
│              │                                │            │
│              │   - Watches Gateway, HTTPRoute │            │
│              │   - Watches BackendTrafficPolicy│            │
│              │   - Watches SecurityPolicy     │            │
│              │   - Generates xDS config       │            │
│              │   - Programs Envoy proxies     │            │
│              └───────────────┬────────────────┘            │
│                              │ configures (xDS)            │
│              ┌───────────────┴────────────────┐            │
│              │   Envoy Proxy                  │            │
│              │                                │            │
│              │   - Processes traffic          │            │
│              │   - TLS termination            │            │
│              │   - Rate limiting              │            │
│              │   - JWT validation             │            │
│              │   - Routes to backends         │            │
│              └───────────────┬────────────────┘            │
│                              │                             │
│                         LoadBalancer                       │
│                         :19080 (HTTP)                      │
│                         :19443 (HTTPS)                     │
└─────────────────────────────────────────────────────────────┘
                              │
                          Client Traffic
```

### Component Breakdown

| Component                    | Role                                                                                                                           |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| **Envoy Gateway Controller** | Watches Gateway API resources and Envoy Gateway extension CRDs, translates them into xDS configuration for Envoy proxies       |
| **Envoy Proxy**              | Processes ingress traffic, terminates TLS, enforces rate limits and JWT auth, and routes to backend services                   |
| **Gateway CR**               | Kubernetes Gateway API resource that defines listeners (ports, protocols, TLS). Created by Helm during data plane installation |
| **GatewayClass**             | Declares that `gateway.envoyproxy.io/gatewayclass-controller` handles Gateway CRs with class `envoy-gateway`                   |
| **HTTPRoute**                | Gateway API route resource. Created by OpenChoreo release pipeline per component. References the Gateway CR via `parentRefs`   |
| **BackendTrafficPolicy**     | Envoy Gateway CRD for traffic management (rate limiting, circuit breaking, health checks). Targets HTTPRoutes via `targetRefs` |
| **SecurityPolicy**           | Envoy Gateway CRD for authentication/authorization (JWT, OIDC, ExtAuth). Targets HTTPRoutes via `targetRefs`                   |
| **Backend**                  | Envoy Gateway CRD for defining external backend endpoints (e.g., JWKS endpoint for JWT validation)                             |
| **BackendTLSPolicy**         | Gateway API resource for configuring TLS settings (SNI, CA certificates) for Backend connections                                |

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
Envoy Proxy (TLS termination)
  │
  ├─ Validate JWT (via SecurityPolicy)
  ├─ Check rate limit (via BackendTrafficPolicy)
  ├─ Match HTTPRoute rules (hostname + path)
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

### Step 1: Remove kgateway (if currently installed)

If the data plane was previously deployed with kgateway, remove the existing Gateway CR so it can be recreated with the Envoy Gateway GatewayClass:

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

### Step 2: Install Envoy Gateway

```bash
# Install Envoy Gateway using Helm
helm install envoy-gateway oci://docker.io/envoyproxy/gateway-helm \
  --version v1.7.0 \
  --namespace openchoreo-data-plane \
  --create-namespace \
  --set config.envoyGateway.extensionApis.enableBackend=true

# Wait for Envoy Gateway to be ready
kubectl wait --for=condition=available deployment/envoy-gateway \
  -n openchoreo-data-plane \
  --timeout=300s
```

> **Note:** The `enableBackend=true` flag is required to enable the Backend extension API. Without it, SecurityPolicy resources that reference Backend CRDs for JWKS fetching will report "backend not found".

### Step 3: Create the Envoy Gateway GatewayClass

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: envoy-gateway
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
EOF
```

Verify:

```bash
kubectl get gatewayclass envoy-gateway
# ACCEPTED should be True
```

### Step 4: Deploy the Data Plane with Envoy Gateway

Install or upgrade the OpenChoreo data plane Helm chart with the Envoy Gateway `gatewayClassName`:

```bash
helm upgrade openchoreo-data-plane oci://ghcr.io/openchoreo/helm-charts/openchoreo-data-plane \
  --version 0.0.0-latest-dev --namespace openchoreo-data-plane \
  --set gateway.gatewayClassName=envoy-gateway \
  --set gateway.httpPort=19080 --reuse-values
```

This creates the `gateway-default` Gateway CR referencing the `envoy-gateway` GatewayClass instead of `kgateway`.

### Step 5: Verify the Installation

```bash
# Check Envoy Gateway controller pod
kubectl get pods -n openchoreo-data-plane -l app.kubernetes.io/instance=envoy-gateway

# Check the Gateway CR status (PROGRAMMED should be True)
kubectl get gateway gateway-default -n openchoreo-data-plane

# Check Gateway listeners
kubectl describe gateway gateway-default -n openchoreo-data-plane

# Check the Envoy proxy pod deployed for the Gateway
kubectl get pods -n openchoreo-data-plane -l gateway.envoyproxy.io/owning-gateway-name=gateway-default
```

Expected pods in `openchoreo-data-plane`:

| Pod                                             | Role                                                       |
| ----------------------------------------------- | ---------------------------------------------------------- |
| `envoy-gateway-*`                               | Envoy Gateway controller — watches Gateway API and EG CRDs |
| `envoy-openchoreo-data-plane-gateway-default-*`  | Envoy proxy instance managed by Envoy Gateway              |

### Step 6: Grant RBAC for Envoy Gateway CRDs

The data plane service account needs permissions to manage Envoy Gateway policy resources. Create a dedicated ClusterRole and bind it to the data plane service account:

```bash
kubectl apply -f - <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: envoy-gateway-module
rules:
  - apiGroups: ["gateway.envoyproxy.io"]
    resources: ["backendtrafficpolicies","securitypolicies","clienttrafficpolicies","envoyproxies","backends"]
    verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: envoy-gateway-module
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: envoy-gateway-module
subjects:
  - kind: ServiceAccount
    name: cluster-agent-dataplane
    namespace: openchoreo-data-plane
EOF
```

> **Note:** Without these permissions, the Release controller will fail to apply BackendTrafficPolicy and SecurityPolicy resources to the data plane with a "forbidden" error. To remove these permissions later, simply delete the ClusterRole and ClusterRoleBinding:
>
> ```bash
> kubectl delete clusterrole envoy-gateway-module
> kubectl delete clusterrolebinding envoy-gateway-module
> ```

### Step 7: Deploy and Invoke the Greeter Service

Deploy the sample greeter service to verify end-to-end traffic flow through Envoy Gateway, including the `envoy-gateway-api-configuration` trait for API management policies.

**Apply the ClusterTrait and example Component:**

```bash
# Apply the ClusterTrait (update JWT provider config in the file first if enabling JWT)
kubectl apply -f envoy-gateway-api-configuration-trait.yaml

# Add the trait to the service ComponentType's allowedTraits
kubectl patch clustercomponenttype service --type=json \
  -p '[{"op":"add","path":"/spec/allowedTraits/-","value":{"kind":"ClusterTrait","name":"envoy-gateway-api-configuration"}}]'

# Apply the example Component and Workload
kubectl apply -f component.yaml
```

**Wait for the deployment to roll out:**

```bash
# Check that the release pipeline has completed
kubectl get componentrelease

# Check the release status
kubectl get release

# Wait for the greeter pod to be ready
kubectl get pods -A

# Verify BackendTrafficPolicy and SecurityPolicy are accepted
kubectl get backendtrafficpolicy -A
kubectl get securitypolicy -A
```

**Invoke the greeter service through Envoy Gateway (no auth):**

If `jwt.enabled` is `false`:

```bash
curl http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greeter/greet?name=OpenChoreo -v
```

Expected response:

```
Hello, OpenChoreo!
```

The response headers will include Envoy proxy metadata such as `server: envoy` and any custom response headers configured via `addResponseHeaders`.

**Invoke with JWT authentication (when `jwt.enabled: true`):**

Obtain a JWT token from your identity provider and pass it in the Authorization header:

```bash
TOKEN=$(curl -s -X POST https://your-idp.example.com/token \
  -d "grant_type=client_credentials&client_id=...&client_secret=..." \
  | jq -r '.access_token')

curl http://development-default.openchoreoapis.localhost:19080/greeter-service-http/greeter/greet?name=OpenChoreo \
  -H "Authorization: Bearer $TOKEN" -v
```

**Cleanup:**

```bash
kubectl delete component greeter-service -n default
kubectl delete workload greeter-service-workload -n default
```

### Envoy Gateway API Configuration Trait

The `envoy-gateway-api-configuration` ClusterTrait provides declarative API management for components routed through Envoy Gateway. It creates `BackendTrafficPolicy`, `SecurityPolicy`, `Backend`, and `BackendTLSPolicy` CRDs targeting the component's HTTPRoute, and patches the HTTPRoute with response header modification filters.

#### JWT Identity Provider Configuration

The JWT provider details (issuer, JWKS URL, JWKS host) are configured once in the trait file before applying it to the cluster — not as component parameters. When any component enables JWT, the trait creates:

1. A **Backend** pointing to the IdP's JWKS endpoint
2. A **BackendTLSPolicy** setting the SNI hostname for TLS connections to the JWKS endpoint
3. A **SecurityPolicy** enforcing JWT validation on the HTTPRoute

Update the following fields in `envoy-gateway-api-configuration-trait.yaml` with your identity provider's details before applying:

| Resource         | Field to update                          | Example value                                          |
| ---------------- | ---------------------------------------- | ------------------------------------------------------ |
| Backend          | `spec.endpoints[0].fqdn.hostname`       | `api.asgardeo.io`                                      |
| Backend          | `spec.endpoints[0].fqdn.port`           | `443`                                                  |
| BackendTLSPolicy | `spec.validation.hostname`               | `api.asgardeo.io` (must match Backend host)            |
| SecurityPolicy   | `spec.jwt.providers[0].issuer`           | `https://api.asgardeo.io/t/myorg/oauth2/token`        |
| SecurityPolicy   | `spec.jwt.providers[0].remoteJWKS.uri`   | `https://api.asgardeo.io/t/myorg/oauth2/jwks`         |

#### Trait Schema

**Parameters:**

| Parameter                        | Type                          | Default    | Description                                                |
| -------------------------------- | ----------------------------- | ---------- | ---------------------------------------------------------- |
| `endpointName`                   | string                        | (required) | Workload endpoint name this trait targets                  |
| `jwt.enabled`                    | boolean                       | `false`    | Enable JWT authentication via SecurityPolicy               |
| `rateLimiting.enabled`           | boolean                       | `true`     | Enable rate limiting via BackendTrafficPolicy              |
| `rateLimiting.unit`              | string                        | `"Minute"` | Rate limit time unit (`Second`, `Minute`, `Hour`)          |
| `rateLimiting.requestsPerUnit`   | integer                       | `60`       | Rate limit threshold per time unit                         |
| `addResponseHeaders.enabled`     | boolean                       | `false`    | Enable response header injection                           |
| `addResponseHeaders.headers`     | array\<{name, value}\>        | `[]`       | Headers to add to responses                                |

**Environment Overrides (configurable per environment):**

The `rateLimiting.requestsPerUnit` parameter can be overridden per environment via ReleaseBinding `traitOverrides`.

#### How It Works

The trait uses OpenChoreo's template rendering pipeline to:

1. **Create Backend** — Conditionally created when `jwt.enabled` is true. Defines the external JWKS endpoint for the identity provider.

2. **Create BackendTLSPolicy** — Conditionally created when `jwt.enabled` is true. Sets the SNI hostname for TLS connections to the JWKS Backend. Without this, Envoy connects to the static IP without SNI, causing CDN/WAF-fronted IdPs to reject the request.

3. **Create SecurityPolicy** — Conditionally created when `jwt.enabled` is true. Targets the component's HTTPRoute via `targetRefs` and validates JWT tokens against the JWKS endpoint via the Backend resource. The `remoteJWKS.uri` field is required by CRD validation even when using `backendRefs`.

4. **Create BackendTrafficPolicy** — Conditionally created when `rateLimiting.enabled` is true. Targets the component's HTTPRoute via `targetRefs` and applies a local rate limit. No annotations on the HTTPRoute are needed.

5. **Patch the HTTPRoute** — Adds a `ResponseHeaderModifier` filter to the HTTPRoute's first rule, injecting custom headers into every response. The patch is skipped when `addResponseHeaders.enabled` is false.

#### Key Difference from Kong

In Kong, plugins are applied to HTTPRoutes via annotations (`konghq.com/plugins`). In Envoy Gateway, policies attach directly to HTTPRoutes via `targetRefs` — no annotation modification of the HTTPRoute is needed for rate limiting or security. This makes the policy attachment more explicit and easier to audit.

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
    - instanceName: my-api
      name: envoy-gateway-api-configuration
      kind: ClusterTrait
      parameters:
        endpointName: http
        jwt:
          enabled: true
        rateLimiting:
          enabled: true
          unit: Minute
          requestsPerUnit: 100
        addResponseHeaders:
          enabled: true
          headers:
            - name: X-Gateway
              value: Envoy
            - name: X-Managed-By
              value: OpenChoreo
```

The rate limit can be overridden per environment via ReleaseBinding `traitOverrides`:

```yaml
traitOverrides:
  my-api:
    rateLimiting:
      requestsPerUnit: 600 # Higher limit for production
```

---

## Configuration

### Helm Values Reference

The following values control gateway behavior in the data plane Helm chart:

| Value                         | Type   | Default                        | Description                                                                     |
| ----------------------------- | ------ | ------------------------------ | ------------------------------------------------------------------------------- |
| `gateway.gatewayClassName`    | string | `"kgateway"`                   | GatewayClass name referenced by the Gateway CR. Set to `"envoy-gateway"` for EG |
| `gateway.httpPort`            | int    | `9080`                         | HTTP listener port                                                              |
| `gateway.httpsPort`           | int    | `9443`                         | HTTPS listener port                                                             |
| `gateway.tls.hostname`        | string | `"*.openchoreoapis.localhost"` | Wildcard hostname for TLS certificate                                           |
| `gateway.tls.certificateRefs` | string | `"openchoreo-gateway-tls"`     | Secret name for the TLS certificate                                             |
| `gateway.infrastructure`      | object | `{}`                           | Cloud provider load balancer annotations                                        |

### Envoy Gateway Helm Values

The following value must be set when installing Envoy Gateway:

| Value                                                | Type    | Default | Description                                              |
| ---------------------------------------------------- | ------- | ------- | -------------------------------------------------------- |
| `config.envoyGateway.extensionApis.enableBackend`    | boolean | `false` | Enable Backend CRD support (required for JWT JWKS fetch) |

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

The Envoy proxy managed by Envoy Gateway listens on ports defined in the Gateway CR listeners. The port mapping must be consistent across all layers:

| Layer                     | HTTP  | HTTPS | Configured Via                                           |
| ------------------------- | ----- | ----- | -------------------------------------------------------- |
| Gateway CR listeners      | 19080 | 19443 | Data plane Helm `gateway.httpPort` / `gateway.httpsPort` |
| Envoy proxy Service ports | 19080 | 19443 | Managed automatically by Envoy Gateway                   |
| DataPlane CR              | 19080 | 19443 | `spec.gateway.publicHTTPPort` / `publicHTTPSPort`        |

Unlike Kong, Envoy Gateway automatically manages the Service and port configuration for the Envoy proxy pods based on the Gateway CR listeners — no manual Helm configuration of proxy listen ports is needed.

### Envoy Gateway-Specific Policy Configuration

Policies are applied to HTTPRoutes via `targetRefs`. Define policies as separate CRDs in the same namespace as the HTTPRoute:

```yaml
# Rate limiting: 100 requests per minute (local)
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: my-rate-limit
  namespace: <component-namespace>
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: <httproute-name>
  rateLimit:
    type: Local
    local:
      rules:
        - limit:
            requests: 100
            unit: Minute
```

```yaml
# JWT authentication with Backend for JWKS
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: my-jwt-auth
  namespace: <component-namespace>
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: <httproute-name>
  jwt:
    providers:
      - name: my-idp
        issuer: https://idp.example.com
        remoteJWKS:
          uri: https://idp.example.com/.well-known/jwks.json
          backendRefs:
            - group: gateway.envoyproxy.io
              kind: Backend
              name: my-jwks-backend
              port: 443
```

---

## Maintenance

### Monitoring Envoy Gateway Health

```bash
# Check Envoy Gateway controller pod
kubectl get pods -n openchoreo-data-plane -l app.kubernetes.io/instance=envoy-gateway

# Check Gateway CR programmed status
kubectl get gateway gateway-default -n openchoreo-data-plane

# Check Envoy proxy pods (created per Gateway)
kubectl get pods -n openchoreo-data-plane -l gateway.envoyproxy.io/owning-gateway-name=gateway-default

# View Envoy Gateway controller logs
kubectl logs -n openchoreo-data-plane deployment/envoy-gateway -f

# View Envoy proxy logs
kubectl logs -n openchoreo-data-plane \
  -l gateway.envoyproxy.io/owning-gateway-name=gateway-default -f
```

### Accessing the Envoy Admin Interface

The Envoy admin interface provides runtime visibility into proxy configuration:

```bash
# Port-forward to Envoy admin interface (port 19000 by default)
kubectl port-forward -n openchoreo-data-plane \
  $(kubectl get pod -n openchoreo-data-plane \
    -l gateway.envoyproxy.io/owning-gateway-name=gateway-default \
    -o jsonpath='{.items[0].metadata.name}') 19000:19000 &

# View active listeners
curl http://localhost:19000/listeners

# View active clusters
curl http://localhost:19000/clusters

# View active routes
curl http://localhost:19000/config_dump | jq '.configs[] | select(.["@type"] | contains("RouteConfiguration"))'

# Check stats
curl http://localhost:19000/stats
```

### Checking Policy Status

```bash
# Check BackendTrafficPolicy status (ACCEPTED should be True)
kubectl get backendtrafficpolicy -A

# Describe a specific policy
kubectl describe backendtrafficpolicy <name> -n <namespace>

# Check SecurityPolicy status
kubectl get securitypolicy -A

# Describe a specific security policy
kubectl describe securitypolicy <name> -n <namespace>

# Check Backend resources
kubectl get backend -A
```

### Upgrading Envoy Gateway

```bash
# Upgrade Envoy Gateway release
helm upgrade envoy-gateway oci://docker.io/envoyproxy/gateway-helm \
  --version v1.7.0 \
  --namespace openchoreo-data-plane \
  --reuse-values

# Verify controller is restarted
kubectl rollout status deployment/envoy-gateway -n openchoreo-data-plane

# Verify Envoy proxy pods are restarted
kubectl rollout status deployment \
  -l gateway.envoyproxy.io/owning-gateway-name=gateway-default \
  -n openchoreo-data-plane
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

To enable debug-level logging on the Envoy proxy managed by Envoy Gateway, create an `EnvoyProxy` CR with the desired log level and reference it from the `gateway-default` Gateway CR via `spec.infrastructure.parametersRef`.

**Step 1: Create the EnvoyProxy CR**

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: gateway-default-proxy-config
  namespace: openchoreo-data-plane
spec:
  logging:
    level:
      default: debug
EOF
```

Available log levels: `debug`, `info`, `warn`, `error`. The `default` key sets the level for all Envoy components. To target specific components, use component-level keys such as `admin`, `filter`, `router`, or `http`.

**Step 2: Reference the EnvoyProxy in the Gateway**

Patch the `gateway-default` Gateway CR to add the `infrastructure.parametersRef`:

```bash
kubectl patch gateway gateway-default -n openchoreo-data-plane --type=merge -p '{
  "spec": {
    "infrastructure": {
      "parametersRef": {
        "group": "gateway.envoyproxy.io",
        "kind": "EnvoyProxy",
        "name": "gateway-default-proxy-config"
      }
    }
  }
}'
```

> **Note:** The `EnvoyProxy` CR must be in the same namespace as the Gateway CR (`openchoreo-data-plane`).

Envoy Gateway will reconcile the proxy deployment within seconds. Verify the change took effect:

```bash
# Check that the EnvoyProxy CR is accepted
kubectl get envoyproxy gateway-default-proxy-config -n openchoreo-data-plane

# View debug logs from the Envoy proxy pod
kubectl logs -n openchoreo-data-plane \
  -l gateway.envoyproxy.io/owning-gateway-name=gateway-default -f
```

**Restoring to info level**

When debug logging is no longer needed, patch the EnvoyProxy CR back to `info`:

```bash
kubectl patch envoyproxy gateway-default-proxy-config -n openchoreo-data-plane \
  --type=merge -p '{"spec":{"logging":{"level":{"default":"info"}}}}'
```

Or delete the `EnvoyProxy` CR entirely and remove the `parametersRef` from the Gateway to restore default behaviour.

---

### Troubleshooting

**Gateway not PROGRAMMED**

```bash
kubectl describe gateway gateway-default -n openchoreo-data-plane
```

Common causes:

- GatewayClass not found or not accepted. Verify `kubectl get gatewayclass envoy-gateway`.
- Envoy Gateway controller not running. Check `kubectl get pods -n openchoreo-data-plane -l app.kubernetes.io/instance=envoy-gateway`.
- Missing permissions for Envoy Gateway controller to create resources in the data plane namespace.

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

**BackendTrafficPolicy or SecurityPolicy not processed (no status)**

If policies are created but show no status conditions, the Envoy Gateway controller is not reconciling them. Common causes:

- The policy's `targetRefs` name does not match any HTTPRoute watched by the controller. Verify the HTTPRoute name: `kubectl get httproute -A`.
- The Backend extension API is not enabled. Ensure `config.envoyGateway.extensionApis.enableBackend=true` was set during Helm installation.

**SecurityPolicy reports "backend not found"**

```bash
kubectl describe securitypolicy <name> -n <namespace>
```

Common causes:

- Backend extension API not enabled. Upgrade the Envoy Gateway Helm release with `--set config.envoyGateway.extensionApis.enableBackend=true` and restart the controller: `kubectl rollout restart deployment/envoy-gateway -n openchoreo-data-plane`.
- Backend resource does not exist in the same namespace as the SecurityPolicy.
- Missing RBAC for the cluster agent to create `gateway.envoyproxy.io` Backend resources (see Step 6).

**JWT validation failing (401 Unauthorized)**

```bash
# Check SecurityPolicy status
kubectl describe securitypolicy <name> -n <namespace>

# Check Envoy proxy logs for JWKS fetch errors
kubectl logs -n openchoreo-data-plane \
  -l gateway.envoyproxy.io/owning-gateway-name=gateway-default --tail=50 | grep -i jwks
```

Common causes:

- JWKS remote fetch failing. Check that the Backend and BackendTLSPolicy are configured with the correct IdP hostname. Without the BackendTLSPolicy, Envoy connects without SNI and CDN/WAF-fronted IdPs reject the request.
- `issuer` in the SecurityPolicy does not match the `iss` claim in the JWT token.
- JWT token has expired (`exp` claim in the past).
- Authorization header format incorrect. Must be `Authorization: Bearer <token>`.

**Endpoint URLs not resolving**

Verify the DataPlane CR gateway config matches the actual Gateway CR:

```bash
kubectl get dataplane default -o yaml | grep -A 10 gateway
```

Ensure `publicGatewayName` and `publicGatewayNamespace` match the Gateway CR's name and namespace.

---

## Customization

### Adding Envoy Gateway Policies to ComponentType Templates

To apply policies to all instances of a ComponentType, create policy resources in the ComponentType template (using separate `creates` entries in a trait, or by embedding them in the ComponentType resources):

```yaml
# In a trait's creates section (recommended approach)
creates:
  - includeWhen: ${parameters.rateLimiting.enabled}
    template:
      apiVersion: gateway.envoyproxy.io/v1alpha1
      kind: BackendTrafficPolicy
      metadata:
        name: ${metadata.name}-rate-limit
        namespace: ${metadata.namespace}
      spec:
        targetRefs:
          - group: gateway.networking.k8s.io
            kind: HTTPRoute
            name: ${oc_generate_name(metadata.componentName, parameters.endpointName)}
        rateLimit:
          type: Local
          local:
            rules:
              - limit:
                  requests: ${envOverrides.rateLimiting.requestsPerUnit}
                  unit: Minute
```

> **Note:** Envoy Gateway policies use `targetRefs` to attach to HTTPRoutes — no modification of the HTTPRoute itself is needed for rate limiting or security. This keeps the HTTPRoute template portable.

### Custom Listener Ports

To use non-default ports (e.g., standard 80/443):

1. Update the data plane Helm values:

```bash
--set gateway.httpPort=80
--set gateway.httpsPort=443
```

2. Update the DataPlane CR:

```yaml
spec:
  gateway:
    publicHTTPPort: 80
    publicHTTPSPort: 443
```

Envoy Gateway automatically configures the Envoy proxy to listen on the ports declared in the Gateway CR listeners — no additional configuration is required.

### Cloud Provider Load Balancer Configuration

Use the `gateway.infrastructure` value to add cloud-specific annotations to the Gateway's generated Service:

```yaml
gateway:
  infrastructure:
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: "external"
      service.beta.kubernetes.io/aws-load-balancer-nlb-target-type: "ip"
      service.beta.kubernetes.io/aws-load-balancer-scheme: "internet-facing"
```

Alternatively, configure the Envoy proxy infrastructure directly using the `EnvoyProxy` CRD:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: custom-proxy-config
  namespace: openchoreo-data-plane
spec:
  provider:
    type: Kubernetes
    kubernetes:
      envoyService:
        annotations:
          service.beta.kubernetes.io/aws-load-balancer-type: "external"
```

Reference the `EnvoyProxy` in the GatewayClass:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: envoy-gateway
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
  parametersRef:
    group: gateway.envoyproxy.io
    kind: EnvoyProxy
    name: custom-proxy-config
    namespace: openchoreo-data-plane
```

### Proxy Log Level

The `EnvoyProxy` CR also controls the Envoy proxy log verbosity. See [Enabling Debug Logs](#enabling-debug-logs) in the Maintenance section for step-by-step instructions.

### Scaling Envoy Gateway for Production

```bash
# Scale the Envoy Gateway controller (supports multiple replicas for HA)
kubectl scale deployment envoy-gateway -n openchoreo-data-plane --replicas=2

# Scale the Envoy proxy (managed via EnvoyProxy CRD)
kubectl patch envoyproxy custom-proxy-config -n openchoreo-data-plane \
  --type=merge -p '{"spec":{"provider":{"kubernetes":{"envoyDeployment":{"replicas":3}}}}}'
```

For production Helm values:

```yaml
# envoy-gateway-production-values.yaml
deployment:
  replicas: 2
  resources:
    requests:
      cpu: "100m"
      memory: "256Mi"
    limits:
      cpu: "500m"
      memory: "1Gi"
```

### Per-Client Rate Limiting

For rate limiting based on client identity (e.g., per JWT subject or per IP), use `clientSelectors` in the BackendTrafficPolicy:

```yaml
spec:
  rateLimit:
    type: Global
    global:
      rules:
        - clientSelectors:
            - headers:
                - name: x-user-id
                  type: Distinct # Rate limit per unique x-user-id header value
          limit:
            requests: 100
            unit: Minute
```

This can be expressed in the trait schema by adding additional parameters for client selector configuration.

### Enabling OIDC Authentication

For OIDC-based authentication (in addition to raw JWT), extend the SecurityPolicy:

```yaml
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: my-route
  oidc:
    provider:
      issuer: https://accounts.google.com
    clientID: my-client-id
    clientSecret:
      name: oidc-client-secret
      namespace: <namespace>
```

The `envoy-gateway-api-configuration` trait currently supports JWT. To add OIDC support, extend the trait schema with an `oidc` section and add a corresponding `SecurityPolicy` template in the `creates` section.
