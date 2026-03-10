# Deploying and Invoking an API with WSO2 API Platform

This guide walks through deploying a sample greeter service as an API managed by the WSO2 API Platform gateway, using the `api-configuration` trait.

> **Prerequisites:** Complete the [Installation](README.md#installation) steps (Steps 1–4) before proceeding.

## Option 1: YAML (kubectl apply)

### Step 1: Apply the Trait and Component

Apply the trait definition, then the component with its workload and release bindings:

```bash
kubectl apply -f wso2-api-platform-api-configuration-trait.yaml
kubectl apply -f component.yaml
```

The `component.yaml` includes:
- A `Component` with the `api-configuration` trait (JWT auth, rate limiting, custom headers)
- A `Workload` with the greeter service container and HTTP endpoint
- `ReleaseBinding` resources for development and production environments

### Step 2: Wait for the Deployment

```bash
# Check that the release pipeline has completed
kubectl get componentrelease

# Check the release status
kubectl get release

# Wait for the greeter pod to be ready
kubectl get pods -A

# Verify RestApi and Backend resources are created
kubectl get restapi -A
kubectl get backend.gateway.kgateway.dev -A
```

### Step 3: Get a Token

Obtain a JWT token from the identity provider configured in the gateway:

```bash
TOKEN=$(curl -s -X POST http://thunder.openchoreo.localhost:8080/oauth2/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=client_credentials&client_id=<client-id>&client_secret=<client-secret>" \
  | jq -r '.access_token')
```

### Step 4: Invoke the API

```bash
curl http://development-default.openchoreoapis.localhost:19080/greeter/greet?name=OpenChoreo \
  -H "Authorization: Bearer $TOKEN" \
  -v
```

Expected response:

```
Hello, OpenChoreo!
```

### Cleanup

```bash
kubectl delete -f component.yaml
```

---

## Option 2: UI

### Step 1: Create the Component

_TODO: UI flow — create a component without policies, then add policies via the UI._

### Step 2: Add API Policies via the UI

_TODO: UI flow for adding jwt-auth, rate limiting, and custom headers._

### Step 3: Get a Token and Invoke

Follow [Step 3](#step-3-get-a-token) and [Step 4](#step-4-invoke-the-api) from the YAML flow above.
