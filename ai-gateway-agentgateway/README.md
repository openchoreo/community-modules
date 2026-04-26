# AI Gateway — Agent Gateway Module for OpenChoreo

This module integrates [Agent Gateway](https://agentgateway.dev/) into OpenChoreo,
providing AI-native gateway capabilities for components.

## Use Cases

### 1. Unified LLM Routing (`ai-llm-routing` ClusterTrait)

Route LLM API calls through a single OpenAI-compatible endpoint to multiple
providers — OpenAI, Anthropic, Vertex AI/Gemini, Groq, or any OpenAI-compatible
API.

- **Single endpoint**: All providers share one `OPENAI_BASE_URL`; apps use the standard OpenAI SDK
- **Provider switching**: Change the `model` field in the request — no code changes, no redeployment
- **Cost control**: Per-environment rate limiting (requests/minute) via ReleaseBinding
- **PII guardrails**: Block/mask credit cards, SSNs, emails in requests and responses
- **API key management**: Keys stored in OpenBao, injected via External Secrets Operator

### 2. MCP Federation (`ai-mcp-federation` ClusterTrait)

Aggregate multiple MCP servers behind a single endpoint so AI agents can discover
and call tools from many backends without knowing where each tool lives.

- **Tool federation**: Multiple remote MCP servers merged behind one `MCP_GATEWAY_URL`
- **Centralized auth**: JWT authentication configured once, applies to all tool access
- **Rate limiting**: Per-environment request limits via ReleaseBinding

> **Note**: Agent Gateway supports OpenAPI-to-MCP translation in its static
> configuration, but this feature is not yet exposed in the Kubernetes CRD
> (v1.1.0). Only native MCP server targets (`mcpTargets`) are supported.

## Architecture

```text
  Data Plane
  ┌─────────────────────────────────────────────────────────────┐
  │                                                             │
  │  ┌──────────────────┐       ┌───────────────────────────┐   │
  │  │  Component Pod    │       │  Agent Gateway Proxy      │   │
  │  │                  │       │  (agentgateway-proxy)     │   │
  │  │  OPENAI_BASE_URL ├──────▶│                           │   │
  │  │  = http://gw:80  │  LLM  │  ┌─ OpenAI ──▶ api.openai│   │
  │  │                  │       │  ├─ Anthropic ▶ api.anthr │   │
  │  │  MCP_GATEWAY_URL ├──────▶│  └─ Groq ────▶ api.groq  │   │
  │  │  = http://gw:3000│  MCP  │                           │   │
  │  │                  │       │  ┌─ GitHub MCP server     │   │
  │  │                  │       │  └─ Slack MCP server      │   │
  │  └──────────────────┘       └───────────────────────────┘   │
  │                                                             │
  │  Per-component routing:                                     │
  │   - x-openchoreo-component header routes to correct backend │
  │   - Per-component guardrails + rate limits applied          │
  └─────────────────────────────────────────────────────────────┘
```

## Installation

### Step 1: Install Agent Gateway CRDs and Controller

```bash
# Install CRDs
helm upgrade -i --create-namespace \
  --namespace openchoreo-data-plane \
  --version v1.1.0 agentgateway-crds oci://cr.agentgateway.dev/charts/agentgateway-crds

# Install the controller
helm upgrade -i -n openchoreo-data-plane agentgateway oci://cr.agentgateway.dev/charts/agentgateway \
--version v1.1.0
```

### Step 2: Create the Shared Gateway

Apply the Gateway and ReferenceGrant resources to create the shared AI gateway
proxy that all components route through:

```bash
kubectl apply -f - <<'EOF'
# Gateway — shared AI gateway proxy with LLM and MCP listeners.
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: agentgateway-proxy
  namespace: openchoreo-data-plane
  labels:
    app.kubernetes.io/name: ai-gateway-agentgateway
    app.kubernetes.io/part-of: openchoreo
spec:
  gatewayClassName: agentgateway
  infrastructure:
    labels:
      openchoreo.dev/system-component: "true"
  listeners:
    # LLM listener — serves OpenAI-compatible API requests.
    - name: llm
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: All
    # MCP listener — serves MCP protocol requests.
    - name: mcp
      protocol: HTTP
      port: 3000
      allowedRoutes:
        namespaces:
          from: All
EOF
```

This creates a `Gateway` resource named `agentgateway-proxy` with two listeners:
- Port **80**: LLM traffic (OpenAI-compatible API)
- Port **3000**: MCP traffic (Model Context Protocol)

Cross-namespace references from HTTPRoutes to this Gateway are allowed by
`allowedRoutes.namespaces.from: All` on each listener. No `ReferenceGrant` is
needed because the traits create both the HTTPRoute and AgentgatewayBackend in
the same namespace (the component's namespace).

### Step 3: Verify the Gateway Proxy

```bash
kubectl get gateway agentgateway-proxy -n openchoreo-data-plane

kubectl get pods -n openchoreo-data-plane \
  -l gateway.networking.k8s.io/gateway-name=agentgateway-proxy
```

### Step 4: Apply the ClusterTraits

```bash
kubectl apply -f ai-llm-routing-trait.yaml
kubectl apply -f ai-mcp-federation-trait.yaml
```

### Step 5: Store LLM API Keys in OpenBao

Store your provider API keys in OpenBao so the trait's ExternalSecret can pull
them. Refer to the [secret management guide](https://openchoreo.dev/docs/platform-engineer-guide/secret-management/)
for details.

```bash
# OpenAI
kubectl exec -it -n openbao openbao-0 -- \
    bao kv put secret/openai-api-key value='sk-...'

# Anthropic
kubectl exec -it -n openbao openbao-0 -- \
    bao kv put secret/anthropic-api-key value='sk-ant-...'

# Groq (or any other provider)
kubectl exec -it -n openbao openbao-0 -- \
    bao kv put secret/groq-api-key value='gsk_...'
```

The `apiKeyRef` parameter in the trait (e.g., `apiKeyRef: openai-api-key`)
maps to the OpenBao secret path `secret/<apiKeyRef>` with property `value`.

> [!TIP]
> The samples use the default `deployment/service` ClusterComponentType. To use
> the custom traits from this module, you need to add `ai-llm-routing` and
> `ai-mcp-federation` to the `allowedTraits` list in that ClusterComponentType.

<!-- -->

> [!TIP]
> When a component with these traits is deployed, the cluster-agent in the data
> plane needs permissions to create Agent Gateway CRs (`AgentgatewayBackend`,
> `AgentgatewayPolicy`, `HTTPRoute`, etc.). Follow the
> [cluster-agent RBAC guide](https://openchoreo.dev/docs/platform-engineer-guide/cluster-agent-rbac/)
> to add the required permissions.

## Samples

### 1. AI Chat Service — Unified LLM Routing

**Source code:** [`service-go-ai-chat`](https://github.com/openchoreo/sample-workloads/tree/main/service-go-ai-chat)
| **Component YAML:** [`samples/llm-routing/component.yaml`](samples/llm-routing/component.yaml)

A Go service that exposes a `POST /chat` endpoint. It proxies user messages to
an LLM and returns the response. The app uses the standard OpenAI SDK format —
it has no awareness of which provider is actually serving the request.

**Why the trait matters:**

Without the `ai-llm-routing` trait, the app would need to:

- Embed API keys in the container or manage secrets itself
- Implement provider-specific SDK integrations for each LLM vendor
- Build its own PII filtering to avoid leaking credit cards, SSNs, or emails
  to third-party LLM providers
- Implement per-environment rate limiting logic in application code
- Redeploy every time the team wants to switch providers or models

With the trait, all of this becomes a platform concern. The app is a simple
HTTP proxy — it sends requests to `OPENAI_BASE_URL` using the standard OpenAI
format and gets responses back. Everything else happens at the gateway layer:

- **API key management** — Keys are stored in OpenBao and pulled via
  ExternalSecret. The app never sees or handles credentials.
- **Provider switching** — Changing from OpenAI to Anthropic to Groq is a
  configuration change on the trait. At request time, Agent Gateway reads the
  `model` field to route to the correct provider — no code changes, no
  redeployment.
- **PII guardrails** — Credit card numbers, SSNs, and emails are automatically
  blocked in requests and masked in responses at the gateway, before they reach
  the LLM provider. The app doesn't need to implement this.
- **Rate limiting** — Per-environment limits are set via ReleaseBinding.
  Production can have stricter limits than development without any app changes.

**Quick start:**

```bash
# 1. Store an API key (Groq used here — replace with your provider)
kubectl exec -it -n openbao openbao-0 -- \
    bao kv put secret/groq-api-key value='gsk_...'

# 2. Apply the Component and WorkflowRun (builds from source)
kubectl apply -f samples/llm-routing/component.yaml

# 3. Wait for the build and deployment to complete
kubectl get workflowrun -w

# 4. Test the chat endpoint
curl -X POST http://development-default.openchoreoapis.localhost:19080/ai-chat-service-chat-api/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "What is Kubernetes?"}'
```

> **Tip:** Edit `component.yaml` to enable additional providers (set
> `enabled: true` for `openai`, `anthropic`, or `vertex`) and store their API
> keys in OpenBao. Once deployed, switch providers at request time by changing
> the `model` field in the request body.

---

### 2. AI Reading Assistant — MCP Federation

**Source code:**
[`service-go-reading-notes-mcp`](https://github.com/openchoreo/sample-workloads/tree/main/service-go-reading-notes-mcp),
[`service-go-ai-reading-assistant`](https://github.com/openchoreo/sample-workloads/tree/main/service-go-ai-reading-assistant)
| **Component YAML:** [`samples/mcp-federation/component.yaml`](samples/mcp-federation/component.yaml)

This sample deploys two components that work together:

| Component                | Description                                                                                                                                                            |
|--------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **reading-notes-mcp**    | A native MCP server (Streamable HTTP) that exposes three tools: `add_note`, `list_notes`, and `search_notes`. No traits — it's a plain service.                        |
| **ai-reading-assistant** | An AI agent with a tool-calling loop. On startup it connects to `MCP_GATEWAY_URL`, discovers available tools via `tools/list`, and uses them to answer user questions. |

**Why the traits matter:**

Without the traits, the agent would need to:

- Know the address of every MCP server it wants to call and manage those
  connections itself
- Implement its own tool discovery and aggregation logic across multiple servers
- Handle LLM provider credentials and switching in application code
- Build authentication and rate limiting into the agent itself

With the dual-trait setup, the agent's code is simple — it talks to two
endpoints (`OPENAI_BASE_URL` for reasoning, `MCP_GATEWAY_URL` for tools) and
the platform handles everything else:

- **`ai-llm-routing`** — Provides LLM access for reasoning and tool selection.
  Same benefits as the chat sample: managed API keys, provider switching, PII
  guardrails, and rate limiting.

- **`ai-mcp-federation`** — Federates multiple MCP servers behind a single
  endpoint. The agent calls `tools/list` once and gets a merged view of all
  tools from all registered servers. Adding a new MCP server is a configuration
  change on the trait (add an entry to `mcpTargets`) — the agent discovers it
  automatically on next `tools/list` call, with no code changes or redeployment.
  JWT authentication and rate limiting are applied centrally at the gateway.

This dual-trait composition shows that traits are stackable — a single
component can combine multiple platform capabilities without the app needing to
know about the underlying infrastructure.

**Quick start:**

```bash
# 1. Store the LLM API key
kubectl exec -it -n openbao openbao-0 -- \
    bao kv put secret/groq-api-key value='gsk_...'

# 2. Apply both components and their WorkflowRuns
kubectl apply -f samples/mcp-federation/component.yaml

# 3. Wait for builds and deployments
kubectl get workflowrun -A -w

# 4. Refresh the agent's tool list (calls /tools/list to discover tools from the MCP server)
curl -s -X POST http://development-default.openchoreoapis.localhost:19080/ai-reading-assistant-chat-api/refresh-tools
# Sample response:
# {"tools":["add_note","list_notes","search_notes"]}

# 5. Test adding a note via the agent
curl -s -X POST http://development-default.openchoreoapis.localhost:19080/ai-reading-assistant-chat-api/chat \
    -H 'Content-Type: application/json' \
    -d '{"message": "Add a note for the book Dune saying it has amazing worldbuilding"}' | jq .
# Sample response:
# {
#   "reply": "Your note for the book \"Dune\" has been added. The note is: \"The book has amazing worldbuilding.\"",
#   "tools_used": [
#     "add_note"
#   ]
# }


# 6. Test searching for a note via the agent
curl -s -X POST http://development-default.openchoreoapis.localhost:19080/ai-reading-assistant-chat-api/chat \
    -H "Content-Type: application/json" \
    -d '{"message": "Search my notes for worldbuilding"}' | jq .
# Sample response:
# {
#   "reply": "Here are the search results for the term \"worldbuilding\" in your reading notes:\n\n* Book: Dune\n  id: 1\n  Note: The book has amazing worldbuilding.",
#   "tools_used": [
#     "search_notes"
#   ]
# }
```

> **Note:** The `mcpTargets[].host` in `component.yaml` must be the full
> Kubernetes service FQDN of the MCP server in the data plane namespace, in the
> form `<service>.<namespace>.svc.cluster.local`. OpenChoreo data plane
> namespaces follow the pattern `dp-<org>-<project>-<env>-<hash>` (for example,
> `dp-default-default-development-f8e58905`), so the host for the sample MCP
> component resolves to
> `reading-notes-mcp.dp-default-default-development-f8e58905.svc.cluster.local`
> as used in `samples/mcp-federation/component.yaml`. Update the org, project,
> environment, and hash segments to match your deployment.

## Usage

### Attaching the LLM Routing Trait

Add the `ai-llm-routing` trait to your Component spec:

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Component
metadata:
  name: my-ai-app
  namespace: default
spec:
  owner:
    projectName: default
  componentType:
    kind: ClusterComponentType
    name: deployment/service
  traits:
    - kind: ClusterTrait
      name: ai-llm-routing
      instanceName: llm
      parameters:
        openai:
          enabled: true
          model: gpt-4o
          apiKeyRef: openai-api-key
        anthropic:
          enabled: true
          model: claude-sonnet-4-20250514
          apiKeyRef: anthropic-api-key
```

Your app receives `OPENAI_BASE_URL` and calls it with the standard OpenAI SDK.
To switch providers, just change the `model` in the request body.

### Attaching the MCP Federation Trait

```yaml
traits:
  - kind: ClusterTrait
    name: ai-mcp-federation
    instanceName: tools
    parameters:
      mcpTargets:
        - name: github
          host: github-mcp.dp-default-default-development.svc
          port: 8080
          path: /mcp
        - name: slack
          host: slack-mcp.dp-default-default-development.svc
          port: 8080
          path: /mcp
          protocol: SSE    # default is StreamableHTTP
      authentication:
        enabled: true
        issuer: https://auth.example.com
        audiences: ["my-app"]
        jwksUrl: https://auth.example.com/.well-known/jwks.json
```

Your app receives `MCP_GATEWAY_URL` and connects via MCP protocol to discover
tools from all federated servers.

### Per-Environment Overrides

Use `ReleaseBinding.spec.traitEnvironmentConfigs` to set per-environment limits:

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: ReleaseBinding
metadata:
  name: my-ai-app-prod
spec:
  owner:
    projectName: default
    componentName: my-ai-app
  environment: production
  traitEnvironmentConfigs:
    llm:
      guardrails:
        enabled: true
        builtins: [CreditCard, Ssn, Email, PhoneNumber]
      rateLimit:
        requestsPerMinute: 30
    tools:
      rateLimit:
        requestsPerMinute: 100
```

## Trait Parameters Reference

### `ai-llm-routing`

| Parameter                     | Type   | Default                    | Description                              |
|-------------------------------|--------|----------------------------|------------------------------------------|
| `gatewayName`                 | string | `agentgateway-proxy`       | Agent Gateway resource name              |
| `gatewayNamespace`            | string | `openchoreo-data-plane`    | Agent Gateway namespace                  |
| `openai.enabled`              | bool   | `false`                    | Enable OpenAI provider                   |
| `openai.model`                | string | `gpt-4o`                   | Default model                            |
| `openai.apiKeyRef`            | string | —                          | OpenBao secret key name                  |
| `anthropic.enabled`           | bool   | `false`                    | Enable Anthropic provider                |
| `anthropic.model`             | string | `claude-sonnet-4-20250514` | Default model                            |
| `anthropic.apiKeyRef`         | string | —                          | OpenBao secret key name                  |
| `vertex.enabled`              | bool   | `false`                    | Enable Vertex AI / Gemini                |
| `vertex.model`                | string | `gemini-2.0-flash`         | Default model                            |
| `vertex.apiKeyRef`            | string | —                          | OpenBao secret key name                  |
| `openaiCompatible.enabled`    | bool   | `false`                    | Enable custom OpenAI-compatible provider |
| `openaiCompatible.name`       | string | `custom`                   | Provider identifier                      |
| `openaiCompatible.model`      | string | —                          | Model identifier                         |
| `openaiCompatible.host`       | string | —                          | API host (e.g., `api.groq.com`)          |
| `openaiCompatible.port`       | int    | `443`                      | API port                                 |
| `openaiCompatible.pathPrefix` | string | `/openai/v1`               | API path prefix                          |
| `openaiCompatible.apiKeyRef`  | string | —                          | OpenBao secret key name                  |

**Environment Configs** (per-environment via ReleaseBinding):

| Parameter                     | Type     | Default                    | Description                                                                                                                                                               |
|-------------------------------|----------|----------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `guardrails.enabled`          | bool     | `true`                     | Enable PII guardrails                                                                                                                                                     |
| `guardrails.builtins`         | string[] | `[CreditCard, Ssn, Email]` | Built-in PII detectors                                                                                                                                                    |
| `rateLimit.requestsPerMinute` | int      | `60`                       | Max requests/minute per component (HTTPRoute-scoped, applied across all providers via the `AgentgatewayPolicy` in `ai-llm-routing-trait.yaml` that targets the HTTPRoute) |

### `ai-mcp-federation`

| Parameter                  | Type     | Default                 | Description                                    |
|----------------------------|----------|-------------------------|------------------------------------------------|
| `gatewayName`              | string   | `agentgateway-proxy`    | Agent Gateway resource name                    |
| `gatewayNamespace`         | string   | `openchoreo-data-plane` | Agent Gateway namespace                        |
| `gatewayListenerName`      | string   | `mcp`                   | Gateway listener for MCP traffic               |
| `mcpTargets[].name`        | string   | —                       | MCP server identifier (required)               |
| `mcpTargets[].host`        | string   | —                       | MCP server hostname (required)                 |
| `mcpTargets[].port`        | int      | `8080`                  | MCP server port                                |
| `mcpTargets[].path`        | string   | `/mcp`                  | MCP endpoint path                              |
| `mcpTargets[].protocol`    | string   | `StreamableHTTP`        | Transport protocol (`StreamableHTTP` or `SSE`) |
| `authentication.enabled`   | bool     | `false`                 | Enable JWT auth for MCP clients                |
| `authentication.issuer`    | string   | —                       | JWT issuer URL                                 |
| `authentication.audiences` | string[] | `[]`                    | Accepted JWT audiences                         |
| `authentication.jwksUrl`   | string   | —                       | JWKS endpoint URL                              |

**Environment Configs** (per-environment via ReleaseBinding):

| Parameter                     | Type | Default | Description             |
|-------------------------------|------|---------|-------------------------|
| `rateLimit.requestsPerMinute` | int  | `120`   | Max MCP requests/minute |

## Resources Created by Traits

### `ai-llm-routing` (per enabled provider)

| Resource                   | Purpose                                                     |
|----------------------------|-------------------------------------------------------------|
| `ExternalSecret`           | Pulls API key from OpenBao, formats as Authorization header |
| `AgentgatewayBackend`      | Configures the LLM provider (model, auth)                   |
| `HTTPRoute`                | Routes component traffic to the provider backend            |
| `AgentgatewayPolicy`       | Applies rate limiting + PII guardrails to the route         |
| `AgentgatewayPolicy` (TLS) | TLS to upstream (openaiCompatible providers only)           |

### `ai-mcp-federation`

| Resource                    | Purpose                                         |
|-----------------------------|-------------------------------------------------|
| `AgentgatewayBackend`       | Defines all MCP targets in one backend          |
| `HTTPRoute`                 | Routes MCP traffic to the federated backend     |
| `AgentgatewayPolicy` (auth) | JWT authentication for MCP clients (if enabled) |
| `AgentgatewayPolicy` (rate) | Rate limiting for MCP requests                  |

## Injected Environment Variables

| Variable                 | Trait               | Value                                          |
|--------------------------|---------------------|------------------------------------------------|
| `OPENAI_BASE_URL`        | `ai-llm-routing`    | `http://<gatewayName>.<gatewayNamespace>`      |
| `MCP_GATEWAY_URL`        | `ai-mcp-federation` | `http://<gatewayName>.<gatewayNamespace>:3000` |
| `X_OPENCHOREO_COMPONENT` | Both                | Component name (for per-component routing)     |
