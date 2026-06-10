# WSO2 API Platform AI Gateway Module for OpenChoreo Data Plane

This module integrates the [WSO2 API Platform](https://github.com/wso2/api-platform)
as an **AI gateway** in the OpenChoreo data plane, providing AI-native API
management capabilities — model-aware routing, LLM token/cost control, and
provider abstraction for components that consume LLM APIs.

It runs on the **same runtime** as the
[WSO2 API Platform Gateway module](https://openchoreo.dev/ecosystem/item/?id=wso2-api-platform-gateway): the
same operator and the same gateway components. The difference is the set of **Traits** layered on
top. Where the gateway module exposes the `api-management` trait for standard
REST API management, this module adds **AI gateway traits**, such as **model-aware routing**, **guardrails**, **MCP gateway** etc.

---

## Overview

OpenChoreo uses the [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/)
as the standard API for exposing component endpoints. The WSO2 API Platform
gateway runs behind the default kgateway and provides enterprise API management,
including AI-specific governance for traffic to Large Language Model (LLM)
providers.

The AI gateway sits between your components and one or more LLM backends
(OpenAI, Anthropic, Azure OpenAI, Mistral, or any OpenAI-compatible API). AI
gateway traffic is **east-west**: agents and services running inside the cluster
call the API Platform Gateway directly. Apps talk to a single managed in-cluster endpoint,
and the gateway applies routing and policies:

- **LLM proxy (app-selected model)** — The application chooses the model via the
  request `model` field; the gateway forwards it unchanged to the provider and
  manages credentials and access. The app can request any model the provider's
  upstream serves. (`ai-llm-proxy` trait)
- **Model-aware routing (gateway-selected model)** — The gateway distributes each
  request across a fixed set of models (round-robin or weighted), replacing the
  app's `model`. Adding, removing, or reweighting models is a gateway
  configuration change, not an application change. Failed models (5xx/429) are
  auto-suspended and retried later. (`ai-model-routing` trait)
- **Token & cost control** — The gateway tracks LLM token usage and cost per
  request (the runtime ships with a bundled `model_prices.json` consumed by the
  `llm-cost` policy) and enforces token-based and budget-based rate limits.
  (`ai-token-cost-control` trait)
- **Provider abstraction** — Application code targets one endpoint and one API
  contract; backend credentials and provider endpoints are managed at the
  gateway.

Because the runtime is identical to the WSO2 API Platform gateway module, this
module reuses the same operator install, the same `APIGateway` CR, and the same
`gateway-configuration.yaml`. Only the **AI gateway Traits** are new.

### Key Design Decisions

- **Same runtime as the WSO2 API Platform gateway module** — No separate
  operator or gateway install. If you already installed the
  [WSO2 API Platform Gateway module](../gateway-wso2-api-platform/README.md),
  the runtime is already in place and you can skip straight to the AI traits.
- **East-west traffic, no ingress** — Components call the gateway-runtime
  Service directly in the cluster. AI gateway traits do **not** create a
  kgateway `Backend` or patch an `HTTPRoute`; they only inject the in-cluster
  gateway endpoint into the component.
- **Provider/proxy persona split** — A shared `LlmProvider` (upstream endpoint +
  credentials + access control) is created **once** by a platform engineer. The
  AI gateway traits create a per-component `LlmProxy` that references the shared
  provider by name and carries the component's own context and policies (model
  routing). Credentials never live in component namespaces. Wire-format
  `LlmProviderTemplate`s are built-in and auto-loaded by the gateway.
- **No control plane changes required** — The rendering pipeline and release
  controllers work unchanged. Only ClusterTraits are added to inject the WSO2
  AI resources.

---

## AI Gateway Features

| Trait                   | Status    | Description                                                                                                                  |
| ----------------------- | --------- | ---------------------------------------------------------------------------------------------------------------------------- |
| `ai-llm-proxy`          | Available | LLM passthrough proxy — the **app** selects the model; the gateway forwards to the provider with managed credentials.        |
| `ai-model-routing`      | Available | Model-aware routing — the **gateway** distributes requests across multiple models (round-robin or weighted), with failover.  |
| `ai-token-cost-control` | Available | Governed passthrough — per-request cost tracking plus token-based and budget (USD) rate limiting; the app selects the model. |

---

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     CONTROL PLANE                           │
│                                                             │
│   Renders component templates and applies resources         │
│   (Deployment, Service, LlmProxy) to the data plane         │
│                                                             │
└─────────────────────────┬───────────────────────────────────┘
                          │ applies resources
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                     DATA PLANE                              │
│                                                             │
│   ┌──────────────┐                                          │
│   │ Component    │  OPENAI_BASE_URL =                       │
│   │ (agent/svc)  │  http://<gw-runtime>:8080/<proxy ctx>    │
│   └──────┬───────┘                                          │
│          │ east-west (in-cluster), OpenAI SDK               │
│          ▼                                                  │
│   ┌───────────────────────────────────────────────┐        │
│   │   WSO2 API Platform gateway-runtime (Envoy)    │        │
│   │                                                 │        │
│   │   LlmProxy (per component)                      │        │
│   │     - context: /<env>-<ns>-<comp>-<inst>        │        │
│   │     - model-routing policy (reads `model`,      │        │
│   │       rewrites to selected model)               │        │
│   │            │ provider.id                        │        │
│   │            ▼                                    │        │
│   │   LlmProvider (shared, install-once)            │        │
│   │     - upstream URL + credential + accessControl │        │
│   └───────────────────────┬─────────────────────────┘        │
│                           │                                  │
└────────────────────────────┼─────────────────────────────────┘
                            ▼
             LLM providers (OpenAI, Anthropic,
             Azure OpenAI, Mistral, ...)
```

There is no public ingress, kgateway `Backend`, or `HTTPRoute` in this path —
components call the gateway-runtime Service directly inside the cluster. The
trait injects `OPENAI_BASE_URL` pointing at the component's `LlmProxy` context.
Depending on the trait, the proxy either forwards the request unchanged
(`ai-llm-proxy`) or applies a model-routing policy (`ai-model-routing`) before
forwarding through the shared `LlmProvider` to the upstream. (The underlying
gateway runtime is the same one the
[WSO2 API Platform gateway module](https://openchoreo.dev/ecosystem/item/?id=wso2-api-platform-gateway)
installs.)

---

## Installation

The installation is identical to the
[WSO2 API Platform Gateway module](https://openchoreo.dev/ecosystem/item/?id=wso2-api-platform-gateway).
If that module is already installed in your data plane, the runtime is ready —
skip to [Using the `ai-model-routing` Trait](#using-the-ai-model-routing-trait).

### Prerequisites

- An existing OpenChoreo deployment with kgateway installed (default)
- Helm 3.x
- kubectl configured with cluster access

### Step 1: Install the WSO2 API Platform Operator

Install the WSO2 API Platform gateway operator using its Helm chart. The
operator watches `RestApi` and `APIGateway` (WSO2) CRDs, and deploys the gateway
components (router, policy engine) based on the `gateway.helm.*` values:

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

> **Already installed the WSO2 API Platform gateway module?** The operator is
> shared. Skip this step.

### Step 2: Apply the Gateway Configuration and Create the WSO2 APIGateway CR

The operator is now running but no gateway instance exists yet. First, apply the
gateway configuration ConfigMap that defines the gateway's runtime settings
(router, policy engine, TLS, logging, and LLM pricing data for AI policies):

```bash
kubectl apply -f gateway-configuration.yaml
```

> This is the **same `gateway-configuration.yaml`** used by the WSO2 API
> Platform gateway module. It already enables the LLM pricing data
> (`gateway.gatewayRuntime.policies.llmPricing`) consumed by the AI cost/token
> policies.

Then, create an `APIGateway` CR (WSO2's CRD —
`apigateways.gateway.api-platform.wso2.com`, not the Kubernetes Gateway API
`Gateway`) to instruct the operator to deploy the gateway components. The CR
references the ConfigMap created above via `configRef`:

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

The operator reconciles this CR and deploys the gateway Helm chart
(`oci://ghcr.io/wso2/api-platform/helm-charts/gateway` version `1.1.0`) with the
referenced ConfigMap values.

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

The AI gateway traits (`ai-llm-proxy`, `ai-model-routing`) each create a
`LlmProxy` (`gateway.api-platform.wso2.com`) per component, so the data plane
service account needs permission to manage that resource for the Release
controller to apply it. (The traits also patch the component `Deployment`, which
is already covered by OpenChoreo's base RBAC — no extra rule needed.) Create a
dedicated ClusterRole and bind it to the data plane service account:

```bash
kubectl apply -f - <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: wso2-api-platform-ai-gateway-module
rules:
  # AI gateway traits create a LlmProxy per component.
  - apiGroups: ["gateway.api-platform.wso2.com"]
    resources: ["llmproxies"]
    verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: wso2-api-platform-ai-gateway-module
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: wso2-api-platform-ai-gateway-module
subjects:
  - kind: ServiceAccount
    name: cluster-agent-dataplane
    namespace: openchoreo-data-plane
EOF
```

> **Note:** Without these permissions, the Release controller will fail to apply
> the AI gateway resources to the data plane with a "forbidden" error. If you
> already created the equivalent ClusterRole for the WSO2 API Platform gateway
> module, this step is redundant. To remove these permissions later:
>
> ```bash
> kubectl delete clusterrole wso2-api-platform-ai-gateway-module
> kubectl delete clusterrolebinding wso2-api-platform-ai-gateway-module
> ```

### Step 5: Create the Shared LLM Provider

The `LlmProvider` is a platform-managed backend created **once**: it holds the
upstream LLM endpoint, the credential, and access control. Per-component
`LlmProxy` resources (created by the AI gateway traits) reference it by
name. It uses a built-in `LlmProviderTemplate` (`openai`, `anthropic`, `gemini`,
`mistralai`, `azure-openai`, `azureai-foundry`, `awsbedrock`) — these are
auto-loaded by the gateway, no setup required.

First create the credential Secret, then apply the provider
([`llm-provider.yaml`](llm-provider.yaml)):

```bash
kubectl create secret generic openai-provider-auth \
  -n openchoreo-data-plane \
  --from-literal=authorization="Bearer sk-..."

kubectl apply -f llm-provider.yaml
```

> Add more providers (Anthropic, Gemini, …) by applying additional `LlmProvider`
> resources with different `template` and `metadata.name` values; components
> select one via the trait's `providerRef`.

---

## Using the `ai-llm-proxy` Trait

`ai-llm-proxy` is the **passthrough** trait: it creates a per-component
`LlmProxy` with **no routing policy**, so the application chooses the model (via
the request `model` field) and the gateway forwards it unchanged to the shared
provider's upstream. Use it when the **app** decides the model; use
`ai-model-routing` when the **platform** should distribute across a fixed set. A
component uses one or the other per endpoint.

Installation (Steps 1–5) is shared — same `LlmProvider`, same `llmproxies` RBAC.

**1. Apply the trait and allow it on the ComponentType:**

```bash
kubectl apply -f ai-llm-proxy-trait.yaml
kubectl patch clustercomponenttype service --type='json' -p='[
  {"op": "add", "path": "/spec/allowedTraits/-", "value": {"name": "ai-llm-proxy", "kind": "ClusterTrait"}}
]'
```

**2. Attach it to a component:**

```yaml
traits:
  - kind: ClusterTrait
    name: ai-llm-proxy
    instanceName: llm
    parameters:
      providerRef: openai-provider
```

The app then calls `OPENAI_BASE_URL` with any model the provider's upstream
serves — e.g. `{"model":"gpt-4o-mini",...}` is served by `gpt-4o-mini` and
`{"model":"gpt-4o",...}` by `gpt-4o`.

### Trait Parameters

| Parameter                 | Type   | Default                                        | Description                       |
| ------------------------- | ------ | ---------------------------------------------- | --------------------------------- |
| `gatewayRuntimeName`      | string | `api-platform-default-gateway-gateway-runtime` | gateway-runtime Service name      |
| `gatewayRuntimeNamespace` | string | `openchoreo-data-plane`                        | gateway-runtime Service namespace |
| `gatewayRuntimePort`      | int    | `8080`                                         | gateway-runtime HTTP port         |
| `providerRef`             | string | `openai-provider`                              | Name of the shared `LlmProvider`  |

It injects the same `OPENAI_BASE_URL` / `OPENAI_API_KEY` env vars as
`ai-model-routing`. The shared [`samples/ai-chat`](samples/ai-chat) sample works
with this trait too — see [Testing the Traits](#testing-the-traits).

---

## Using the `ai-model-routing` Trait

The trait creates a per-component `LlmProxy` that fronts the shared provider,
applies a model-routing policy, and injects the gateway endpoint into the
component. The app uses the standard OpenAI SDK against `OPENAI_BASE_URL`; the
gateway reads the `model` field and rewrites it to the selected model.

**1. Apply the trait to the control plane:**

```bash
kubectl apply -f ai-model-routing-trait.yaml
```

**2. Allow the trait on the ComponentType:**

```bash
kubectl patch clustercomponenttype service --type='json' -p='[
  {"op": "add", "path": "/spec/allowedTraits/-", "value": {"name": "ai-model-routing", "kind": "ClusterTrait"}}
]'
```

**3. Attach it to a component:**

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Component
metadata:
  name: my-ai-agent
  namespace: default
spec:
  owner:
    projectName: default
  componentType:
    kind: ClusterComponentType
    name: deployment/service
  traits:
    - kind: ClusterTrait
      name: ai-model-routing
      instanceName: llm
      parameters:
        providerRef: openai-provider
        routing:
          strategy: weighted # or round-robin
          models:
            - { model: gpt-4o, weight: 3 }
            - { model: gpt-4o-mini, weight: 1 }
```

**Per-environment tuning** via `ReleaseBinding.spec.traitEnvironmentConfigs`:

```yaml
traitEnvironmentConfigs:
  llm:
    suspendDuration: 120 # seconds to suspend a model after a 5xx/429
```

### Trait Parameters

| Parameter                         | Type   | Default                                        | Description                                                                    |
| --------------------------------- | ------ | ---------------------------------------------- | ------------------------------------------------------------------------------ |
| `gatewayRuntimeName`              | string | `api-platform-default-gateway-gateway-runtime` | gateway-runtime Service name                                                   |
| `gatewayRuntimeNamespace`         | string | `openchoreo-data-plane`                        | gateway-runtime Service namespace                                              |
| `gatewayRuntimePort`              | int    | `8080`                                         | gateway-runtime HTTP port                                                      |
| `providerRef`                     | string | `openai-provider`                              | Name of the shared `LlmProvider` to front                                      |
| `apiPath`                         | string | `/v1/chat/completions`                         | API path matched by the routing policy (the OpenAI wire path the client sends) |
| `routing.strategy`                | string | `round-robin`                                  | `round-robin` (even) or `weighted` (proportional)                              |
| `routing.models[].model`          | string | —                                              | Model name                                                                     |
| `routing.models[].weight`         | int    | `1`                                            | Relative weight (weighted strategy only)                                       |
| `routing.requestModel.location`   | string | `payload`                                      | Where the model name is: `payload`/`header`/`queryParam`/`pathParam`           |
| `routing.requestModel.identifier` | string | `$.model`                                      | JSONPath / header name / query name / regex                                    |

**Environment configs** (per-environment via ReleaseBinding):

| Parameter         | Type | Default | Description                                                      |
| ----------------- | ---- | ------- | ---------------------------------------------------------------- |
| `suspendDuration` | int  | `60`    | Seconds to suspend a model after a 5xx/429 (`0` = don't persist) |

> **Path convention (avoid a `/v1` 404):** OpenAI-compatible clients send the
> full wire path `/v1/chat/completions` (they append it to `OPENAI_BASE_URL`).
> So `apiPath` defaults to `/v1/chat/completions` **and** the shared
> `LlmProvider` must use a **host-root** upstream (`https://api.openai.com`, not
> `.../v1`) with that path allowed in `accessControl` — otherwise the upstream
> path doubles to `/v1/v1/...` or the provider 404s. The provided
> [`llm-provider.yaml`](llm-provider.yaml) is already configured this way.

### Injected Environment Variables

| Variable          | Value                                                                   |
| ----------------- | ----------------------------------------------------------------------- |
| `OPENAI_BASE_URL` | `http://<gatewayRuntimeName>.<ns>:<port>/<env>-<ns>-<component>-<inst>` |
| `OPENAI_API_KEY`  | `managed-by-gateway` (placeholder; the real key lives on the provider)  |

---

## Using the `ai-token-cost-control` Trait

`ai-token-cost-control` is a **governed passthrough**: like `ai-llm-proxy` the
application chooses the model, but the gateway also attaches:

- **`llm-cost`** (always) — computes each request's cost from the provider
  template's token paths and the gateway's bundled `model_prices.json`.
- **`token-based-ratelimit`** (when token limits are set) — caps prompt /
  completion / total tokens per time window.
- **`llm-cost-based-ratelimit`** (when a budget is set) — caps spend (USD) per
  time window.

When a limit is exceeded the gateway returns **HTTP 429**. Limits are
**per-environment** (set via `ReleaseBinding.spec.traitEnvironmentConfigs`); with
no limits set, the trait still attaches `llm-cost` so usage/cost is tracked.

Installation (Steps 1–5) is shared — same `LlmProvider`, same `llmproxies` RBAC.

**1. Apply the trait and allow it on the ComponentType:**

```bash
kubectl apply -f ai-token-cost-control-trait.yaml
kubectl patch clustercomponenttype service --type='json' -p='[
  {"op": "add", "path": "/spec/allowedTraits/-", "value": {"name": "ai-token-cost-control", "kind": "ClusterTrait"}}
]'
```

**2. Attach it to a component:**

```yaml
traits:
  - kind: ClusterTrait
    name: ai-token-cost-control
    instanceName: llm
    parameters:
      providerRef: openai-provider
```

**3. Set limits per environment** via `ReleaseBinding.spec.traitEnvironmentConfigs`:

```yaml
traitEnvironmentConfigs:
  llm: # = trait instanceName
    totalTokenLimits:
      - { count: 100000, duration: "1h" }
    promptTokenLimits: [] # optional
    completionTokenLimits: [] # optional
    costBudgets:
      - { amount: 5.00, duration: "24h" } # USD per window
```

### Trait Parameters

| Parameter                 | Type   | Default                                        | Description                                    |
| ------------------------- | ------ | ---------------------------------------------- | ---------------------------------------------- |
| `gatewayRuntimeName`      | string | `api-platform-default-gateway-gateway-runtime` | gateway-runtime Service name                   |
| `gatewayRuntimeNamespace` | string | `openchoreo-data-plane`                        | gateway-runtime Service namespace              |
| `gatewayRuntimePort`      | int    | `8080`                                         | gateway-runtime HTTP port                      |
| `providerRef`             | string | `openai-provider`                              | Name of the shared `LlmProvider`               |
| `apiPath`                 | string | `/v1/chat/completions`                         | API path the policies match (OpenAI wire path) |

**Environment configs** (per-environment via ReleaseBinding):

| Parameter               | Type   | Default        | Description                                             |
| ----------------------- | ------ | -------------- | ------------------------------------------------------- |
| `promptTokenLimits`     | array  | `[]`           | `[{count, duration}]` — limits on prompt (input) tokens |
| `completionTokenLimits` | array  | `[]`           | `[{count, duration}]` — limits on completion tokens     |
| `totalTokenLimits`      | array  | `[]`           | `[{count, duration}]` — limits on total tokens          |
| `costBudgets`           | array  | `[]`           | `[{amount, duration}]` — spend budgets (USD) per window |
| `algorithm`             | string | `fixed-window` | Rate-limit algorithm for token-based limits             |
| `backend`               | string | `memory`       | Counter backend for token-based limits                  |

It injects the same `OPENAI_BASE_URL` / `OPENAI_API_KEY` env vars as the other
traits. The shared [`samples/ai-chat`](samples/ai-chat) sample works with this
trait too — see [Testing the Traits](#testing-the-traits).

---

## Testing the Traits

A single sample — [`samples/ai-chat`](samples/ai-chat) — exercises **all three**
traits. It deploys the gateway-agnostic chat app
([`service-go-ai-chat`](https://github.com/openchoreo/sample-workloads/tree/main/service-go-ai-chat)),
which calls `OPENAI_BASE_URL` with the OpenAI SDK and whose `POST /chat` accepts
an optional `model` field it forwards to the gateway. Swap the trait in
`component.yaml` to compare behaviors — the app and the public URL stay the same.

### 1. Deploy the sample

```bash
# Prereqs: Installation Steps 1-5, the chosen trait applied + allowed on the ComponentType
kubectl apply -f samples/ai-chat/component.yaml
kubectl apply -f samples/ai-chat/releasebinding.yaml   # optional (ai-model-routing tuning)

# Wait for the source build + deployment to complete
kubectl get workflowrun -w
```

### 2. Verify the resources rendered

```bash
# The trait creates a LlmProxy, Accepted + Programmed by the gateway
kubectl get llmproxy -A -o jsonpath='{range .items[*]}{.metadata.name}: {range .status.conditions[*]}{.type}={.status} {end}{"\n"}{end}'

# The trait injected the gateway endpoint into the component Deployment
DPNS=$(kubectl get ns -o name | grep -o 'dp-default-default-development[^ ]*')
kubectl get deploy -n ${DPNS#namespace/} \
  -o jsonpath='{range .items[*].spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}' | grep OPENAI
```

The app is reached over its **own component endpoint** (public ingress, `:19080`);
the LLM call it makes internally goes **east-west** through the AI gateway via
the injected `OPENAI_BASE_URL`. The public path is `/ai-chat-service-chat-api/chat`.

### 3a. Test `ai-model-routing` — the gateway picks the model

The component ships with this trait (weighted `gpt-4o`:3, `gpt-4o-mini`:1). The
`model` you send is **ignored and overwritten**; you observe routing by the
`model` in the responses across many calls:

```bash
for i in $(seq 1 12); do
  curl -sS -X POST http://development-default.openchoreoapis.localhost:19080/ai-chat-service-chat-api/chat \
    -H "Content-Type: application/json" \
    -d '{"message":"hi"}' | grep -o '"model":"[^"]*"'
done | sort | uniq -c
```

Expected (weighted 3:1):

```
   9 "model":"gpt-4o-2024-08-06"
   3 "model":"gpt-4o-mini-2024-07-18"
```

Even if you explicitly request a model, it is overwritten — the response comes
back as `gpt-4o` / `gpt-4o-mini` regardless:

```bash
curl -sS -X POST http://development-default.openchoreoapis.localhost:19080/ai-chat-service-chat-api/chat \
  -H "Content-Type: application/json" \
  -d '{"message":"hi","model":"gpt-3.5-turbo"}'   # served as gpt-4o or gpt-4o-mini
```

> Switch `strategy: round-robin` or change the weights/models in `component.yaml`
> and re-apply to see the distribution change.

### 3b. Test `ai-llm-proxy` — the app picks the model

Swap the trait: in `samples/ai-chat/component.yaml` replace the `ai-model-routing`
block with the `ai-llm-proxy` block (shown in the file's header), make sure
`ai-llm-proxy` is applied and allowed on the ComponentType, then re-apply and
wait for the rollout. Now the `model` you send is **honored**:

```bash
# gpt-4o-mini
curl -X POST http://development-default.openchoreoapis.localhost:19080/ai-chat-service-chat-api/chat \
  -H "Content-Type: application/json" \
  -d '{"message":"What is Kubernetes?","model":"gpt-4o-mini"}'
# -> "model":"gpt-4o-mini-2024-07-18"

# gpt-3.5-turbo
curl -X POST http://development-default.openchoreoapis.localhost:19080/ai-chat-service-chat-api/chat \
  -H "Content-Type: application/json" \
  -d '{"message":"What is Kubernetes?","model":"gpt-3.5-turbo"}'
# -> "model":"gpt-3.5-turbo-0125"
```

Each response echoes the model you requested — the difference between the
passthrough and routing traits, visible from the same public URL.

### 3c. Test `ai-token-cost-control` — cost tracking + limits

Swap the trait to `ai-token-cost-control` (apply + allow it, then set the trait
on the component) and set a small limit in the ReleaseBinding to see enforcement
quickly:

```yaml
# samples/ai-chat/releasebinding.yaml (development)
traitEnvironmentConfigs:
  llm:
    totalTokenLimits:
      - { count: 60, duration: "1h" }
    costBudgets:
      - { amount: 0.01, duration: "1h" }
```

Re-apply, then send a few requests — the first few succeed (each returns its
`usage`), and once the token budget for the window is crossed the gateway
returns **HTTP 429**:

```bash
URL=http://development-default.openchoreoapis.localhost:19080/ai-chat-service-chat-api/chat
for i in $(seq 1 6); do
  curl -sS -o /tmp/r -w "req $i -> HTTP %{http_code}\n" -X POST "$URL" \
    -H "Content-Type: application/json" -d '{"message":"Reply with exactly: hello"}'
done
```

Expected — a few `200`s (each `{"reply":"hello","usage":{"total_tokens":29}}`)
then `502`s (the app surfaces the gateway's 429 as an upstream error). Calling
the `LlmProxy` directly (the Tip below) shows the raw response:
`HTTP 429 {"error":"Too Many Requests","message":"Rate limit exceeded ..."}`.

> Token limits and cost budgets are independent — set either or both. With
> neither set, only `llm-cost` is attached (usage/cost tracking, no limiting).

> **Tip — test the gateway in isolation (bypass the app):** curl the `LlmProxy`
> context directly from inside the cluster (path = `OPENAI_BASE_URL` +
> `/v1/chat/completions`). Under `ai-model-routing` a bogus `model` is silently
> rewritten and succeeds; under `ai-llm-proxy` it is forwarded as-is and the
> upstream rejects it:
>
> ```bash
> kubectl run llm-test --rm -i --restart=Never -n openchoreo-data-plane \
>   --image=curlimages/curl:8.5.0 -- \
>   curl -sS -X POST \
>   'http://api-platform-default-gateway-gateway-runtime.openchoreo-data-plane:8080/development-default-ai-chat-service-llm/v1/chat/completions' \
>   -H 'Content-Type: application/json' \
>   -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}'
> ```

---

See the
[WSO2 API Platform Gateway module](https://openchoreo.dev/ecosystem/item/?id=wso2-api-platform-gateway) for
the underlying runtime, optional WSO2 API Manager connectivity, and edge-mode
deployment — all of which apply unchanged to this AI gateway module.
