# Configuration and architecture

The README covers installation and the quick start. This document goes deeper. It's the full parameter reference, the guide to tuning and extending the trait, the reasoning behind the architecture, and the troubleshooting notes for the `autoscaling-keda` module and its `keda-based-scaling` trait.

## Parameter reference

All parameters live under `spec.traits[].parameters` on the component. The trait is named `keda-based-scaling`, and the samples reuse that same value for `instanceName`. Per-environment overrides in the `ReleaseBinding` are keyed by whatever `instanceName` you pick.

| Parameter | Type | Default | Description |
|---|---|---|---|
| `enabled` | boolean | `false` | Activates the trait. Nothing is rendered unless this is `true` and the data plane backend is `keda`. |
| `minReplicas` | integer | `0` | Minimum replica count. `0` enables scale-to-zero. |
| `maxReplicas` | integer | `10` | Maximum replica count KEDA will scale up to. |
| `cooldownPeriod` | integer | `300` | Seconds all metrics must stay at zero before KEDA scales down to `minReplicas`. |
| `trigger.type` | string | `""` | KEDA scaler type (e.g. `cron`, `kafka`). Empty string selects HTTP mode. |
| `trigger.metadata` | map[string]string | `{}` | Scaler-specific metadata passed verbatim to the `ScaledObject` trigger. |
| `interceptorNamespace` | string | `keda` | Namespace where the KEDA HTTP Add-on interceptor is installed. |
| `interceptorService` | string | `keda-add-ons-http-interceptor-proxy` | Service name of the interceptor proxy. |
| `interceptorPort` | integer | `8080` | Port on `interceptorService` the interceptor listens on. |
| `interceptorMultiportService` | string | `keda-add-ons-http-interceptor-multiport` | Multiport front Service used for in-cluster wake (the ExternalName target). |
| `wakeablePorts` | integer[] | `[80, 3000, 5000, 8000, 8080, 8081, 8090, 9000, 9090]` | Ports the multiport Service exposes. An HTTP-mode endpoint must use one of these. |
| `interceptorScalerService` | string | `keda-add-ons-http-external-scaler` | The HTTP Add-on's external scaler Service (the companion `ScaledObject` pulls metrics from it). |
| `interceptorScalerPort` | integer | `9090` | Port on `interceptorScalerService`. |
| `requestRateTargetValue` | integer | `1` | Target requests/second per replica. KEDA scales up when the rate goes over this value. |
| `requestRateWindow` | string | `"1m"` | Sliding window over which request rate is measured. |
| `requestRateGranularity` | string | `"1s"` | Bucket granularity inside the window. |
| `readinessTimeout` | string | `"120s"` | How long the interceptor holds the first request while a pod cold-starts. |

### Per-environment overrides

The trait's `environmentConfigs` schema exposes a subset of the parameters that platform engineers can override per environment from the `ReleaseBinding`, without touching the component definition:

```yaml
# environmentConfigs schema (minReplicas / maxReplicas / cooldownPeriod only)
environmentConfigs:
  openAPIV3Schema:
    type: object
    properties:
      minReplicas:
        type: integer
        minimum: 0
      maxReplicas:
        type: integer
        minimum: 1
      cooldownPeriod:
        type: integer
        minimum: 0
```

In the `ReleaseBinding`, set `traitEnvironmentConfigs` keyed by the trait `instanceName` (`keda-based-scaling`):

```yaml
spec:
  traitEnvironmentConfigs:
    keda-based-scaling:
      minReplicas: 1      # never scale to zero in production
      maxReplicas: 10
      cooldownPeriod: 600
```

When one of these fields is set, it wins over the component's `parameters` value for that environment. Anything that isn't in `environmentConfigs`, like `trigger` or the HTTP metric knobs, can't be overridden this way. Change those on the component itself.

## How HTTP scaling behaves

HTTP mode scales on request rate over a sliding window, not on instantaneous concurrency. The interceptor counts every request that arrived in the last `requestRateWindow` (default `1m`) at `requestRateGranularity` resolution (default `1s`), then divides by the window length to get a rate in requests per second.

KEDA scales up when that per-replica rate goes over `requestRateTargetValue` (default `1 req/s`). It only starts the scale-down countdown once the rate hits zero across the full window. So a service getting even sparse traffic, say one request every few seconds, keeps a non-zero rate across the window and won't scale to zero too early. It scales down only after no request has arrived for the entire window, and then only after `cooldownPeriod` more seconds of idle time.

To work through an example, one request per 10 seconds is 0.1 req/s. With `requestRateTargetValue: 1` that rate is below the target, so one replica is enough. The rate stays non-zero for a minute after the last request, then KEDA starts the `cooldownPeriod` countdown before dropping to zero. With the defaults (`window: 1m`, `cooldownPeriod: 300`), the service stays awake for roughly 6 minutes after the last request.

### Cold starts and the 120s timeout

A cold start on a warmed cluster takes 2 to 4 seconds. The interceptor holds the first request in-flight until a pod passes readiness, bounded by `readinessTimeout` (default `120s`). The trait also patches the external `HTTPRoute` to set a `request` timeout of `120s`, matching the interceptor's hold time so the gateway doesn't drop the request first. Don't set `readinessTimeout` longer than your gateway's absolute route timeout.

The first cold start after a component is deployed can be slower. KEDA registers the new scaler against the `InterceptorRoute` during this window, and if the `ScaledObject` reconciles before the `InterceptorRoute` exists, KEDA may briefly fall back to a CPU metric. Retry once, later cold starts are fast. See Troubleshooting for the fix if it sticks around.

### Tuning cooldownPeriod

Lower values cut idle cost but mean more frequent cold starts. For a service with a long start time, raise `readinessTimeout` and `cooldownPeriod` together so light traffic doesn't push it into a scale-down/immediate-scale-up cycle.

## Extending the module

### Concurrency-based scaling

HTTP mode scales on `requestRate` (requests per second over a window) by default. The KEDA HTTP Add-on's `InterceptorRoute` also supports `concurrency` as a `scalingMetric`, which targets concurrent in-flight requests per replica. To switch, edit the `InterceptorRoute` template in `keda-based-scaling-trait.yaml` and replace the `requestRate` block under `scalingMetric` with a `concurrency` block. See the add-on's `InterceptorRoute` reference: https://github.com/kedacore/http-add-on/blob/main/docs/ref/v0.14.0/interceptor_route.md

Concurrency helps when requests are long-lived, like streaming or WebSocket connections, where rate under-counts the actual load.

### Any KEDA scaler for workers and event-driven services

`trigger.type` and `trigger.metadata` pass straight through to the `ScaledObject`, so every scaler in the KEDA catalog works: cron, kafka, rabbitmq, prometheus, aws-sqs, azure-servicebus, and 70+ others. The full list is at https://keda.sh/docs/latest/scalers/

Here's a RabbitMQ worker:

```yaml
parameters:
  enabled: true
  minReplicas: 0
  maxReplicas: 5
  trigger:
    type: rabbitmq
    metadata:
      queueName: jobs
      hostFromEnv: RABBITMQ_URL   # injected by a connection binding
```

For brokered scalers, use `hostFromEnv` pointing at an env var your workload connection already injects. That keeps broker credentials out of the component definition and out of git.

### Authenticated triggers

KEDA scalers reference credentials through `triggers[].authenticationRef`, which points at a `TriggerAuthentication` or `ClusterTriggerAuthentication` object. The trait doesn't expose `authenticationRef` as a parameter yet. To use it, extend the trigger-mode `ScaledObject` template in `keda-based-scaling-trait.yaml`. The module's RBAC already lets the cluster-agent manage `triggerauthentications` and `clustertriggerauthentications`, so you don't need extra permissions. See the KEDA authentication docs: https://keda.sh/docs/latest/concepts/authentication/

Where you can, prefer the `hostFromEnv`-style metadata above over authenticated triggers, since it needs no extra objects.

### Wakeable ports

The default wakeable ports are `[80, 3000, 5000, 8000, 8080, 8081, 8090, 9000, 9090]`. An HTTP-mode endpoint has to listen on one of these because the component's Service is turned into an ExternalName DNS CNAME, and a CNAME can't remap ports. So the caller-facing port must be a port the interceptor already answers on.

To add a port:

1. Add it to `keda-interceptor-multiport.yaml` (the `ports` list on the Service) and re-apply.
2. Add it to the `wakeablePorts` default in `keda-based-scaling-trait.yaml` and re-apply the trait.

Keep the two in sync. The trait's validation rule checks `ep.port in parameters.wakeablePorts` and rejects a component at render time if the endpoint port isn't in the list.

### Interceptor installed elsewhere

If the KEDA HTTP Add-on is installed in a namespace other than `keda`, or with non-default Service names or ports, set the matching parameters on the trait attachment:

```yaml
parameters:
  interceptorNamespace: my-keda
  interceptorService: my-interceptor-proxy
  interceptorPort: 8080
  interceptorMultiportService: my-interceptor-multiport
  interceptorScalerService: my-external-scaler
  interceptorScalerPort: 9090
```

These values are per-component, so different components in the same cluster can point at different interceptor installations.

### Other component types

The HTTP path needs a specific shape from the component type:

- A Deployment named `${metadata.name}`
- A Service named `${metadata.componentName}`
- Exactly one external `HTTPRoute` carrying the `openchoreo.dev/endpoint-visibility: external` label
- For service-style path routing, a `URLRewrite` filter is expected. For web-application-style host routing, the trait adds a hostname-only `URLRewrite` filter.

Any `ClusterComponentType` that produces this shape can allow the trait by adding `keda-based-scaling` to its `allowedTraits` list (delete and recreate, as described in the README's Install step 3).

The trigger/worker path is simpler. It only needs the Deployment, so any deployment-based component type works.

### Other gateways

The HTTP path is kgateway-specific. The trait routes to the interceptor through a `gateway.kgateway.dev/Backend` in the component's namespace. This Backend needs no cross-namespace `ReferenceGrant` because it's local to the workload namespace.

On another Gateway API implementation, adapt the `creates` entry for the Backend resource and the HTTPRoute patch. The equivalent is a cross-namespace Service `backendRef` pointing at the interceptor, plus a `ReferenceGrant` in the interceptor namespace that permits it.

### Advanced ScaledObject tuning

The trait doesn't expose every `ScaledObject` field as a parameter. Fields like `fallback` (behavior when the scaler is unavailable) and `advanced.horizontalPodAutoscalerConfig.behavior` (scale-up/scale-down stabilization windows) can be added by editing the `ScaledObject` templates directly in the trait. Full `ScaledObject` spec: https://keda.sh/docs/latest/reference/scaledobject-spec/

## Architecture

### Backend annotation contract

The trait reads `dataplane.annotations["openchoreo.dev/keda-based-scaling-backend"]` from the render context (available from OpenChoreo 1.2 onward). When the value is `keda`, the trait renders KEDA objects. When the annotation is absent or set to anything else, the trait is completely inert and the component runs at its configured static replica count. There's no per-environment forking to manage. The same component definition runs normally on a plane without KEDA and scales to zero on a plane that advertises `keda`.

Annotate the data plane to activate it:

```bash
kubectl annotate clusterdataplane default \
  openchoreo.dev/keda-based-scaling-backend=keda --overwrite
```

### Rendering modes

| Mode | Condition | Renders |
|---|---|---|
| **HTTP** | `enabled: true`, backend `keda`, `trigger.type == ""`, one external HTTP/GraphQL/WebSocket endpoint | `InterceptorRoute` + companion `ScaledObject`, kgateway `Backend`, pod-backing Service, patches to ExternalName the component's Service for in-cluster wake, and repoint the `HTTPRoute` at the Backend for gateway traffic |
| **Trigger** | `enabled: true`, backend `keda`, `trigger.type != ""` | `ScaledObject` with the given trigger |

Both modes patch the Deployment to remove `spec.replicas`, handing replica ownership to KEDA. If `spec.replicas` stuck around, server-side apply would reset it on every render and fight the autoscaler.

### In-cluster wake: the ExternalName alias mechanism

OpenChoreo connection bindings inject the callee's in-cluster Service URL (`http://<component>.<ns>.svc.cluster.local:<port>`). Left alone, that URL goes straight to the Deployment pods and gets refused while the service sleeps.

In HTTP mode the trait closes that gap:

1. The component's own Service (named `${metadata.componentName}`) is patched to `type: ExternalName`, with `externalName` pointing at `keda-add-ons-http-interceptor-multiport.<interceptorNamespace>.svc.cluster.local`. An in-cluster call to the injected URL now resolves to the interceptor.
2. A separate ClusterIP Service (`${componentName}-keda-upstream`) is created with the original pod selector and ports. The `InterceptorRoute` forwards here once a pod is ready.
3. The `InterceptorRoute` registers exactly one `rules[].hosts` entry: the component's FQDN (`<componentName>.<namespace>.svc.cluster.local`). The HTTPRoute patch adds a `urlRewrite.hostname` filter that rewrites the outbound Host header to this FQDN, so gateway traffic and in-cluster traffic match the same interceptor rule.

The Service is born as ExternalName on first render, so there's no ClusterIP-to-ExternalName mutation to trip over. If you convert a long-lived component and the data-plane apply rejects the in-place Service type change, redeploy the component so the Service is recreated from scratch.

### Why exactly one HTTP endpoint

Two constraints force the single-endpoint shape.

The first is one Service, one Host. OpenChoreo gives a component a single Service (one DNS name, possibly many ports), and connection bindings inject that one name. Every endpoint on the component is reached as the same host (`<component>.<ns>.svc.cluster.local`), differing only by port. The KEDA interceptor routes purely by the `Host` header and strips the port first, so it can't tell two endpoints on the same component apart. A second endpoint would be forwarded to the wrong port without warning. So there can be only one endpoint.

The second is that a CNAME can't remap ports. An ExternalName Service is a DNS CNAME, so a caller hitting the component on its endpoint port lands on the interceptor on that same port. The endpoint port has to be one the interceptor already answers on. The multiport Service (`keda-interceptor-multiport.yaml`) fronts the interceptor on a set of common ports for exactly this reason, so you're not pinned to a single port. That's why the endpoint port has to be in `wakeablePorts`.

A service that doesn't fit (multiple endpoints, or a port outside `wakeablePorts`) has three ways out:

- Split the extra endpoints into a separate component, each with its own trait attachment.
- Add the port to `keda-interceptor-multiport.yaml` and `wakeablePorts`.
- Keep `minReplicas >= 1` on the component, which skips scale-to-zero and the ExternalName takeover.

## Limitations

- Exactly one external HTTP endpoint per service in HTTP mode. The interceptor routes by `Host` only (port stripped), and the ExternalName alias is a DNS CNAME that can't remap ports. See the Architecture section for the full reasoning and the escape hatches.
- The HTTP path is kgateway-specific. The trait routes to the interceptor through a `gateway.kgateway.dev/Backend`. On a different Gateway API implementation, adapt the Backend resource and the HTTPRoute patch (a cross-namespace Service backendRef plus a `ReferenceGrant` works).
- The committed component types are a snapshot. `componenttypes/service.yaml`, `componenttypes/webapp.yaml`, and `componenttypes/worker.yaml` mirror the stock OpenChoreo defaults. If your platform customizes those types, regenerate from your live types instead of applying the committed copies.
- It's mutually exclusive with HPA-style traits. Both claim ownership of the Deployment's replica count, so don't attach an HPA-style trait and `keda-based-scaling` to the same component.
- The Cilium-signal backend (`backend: cilium`) is a future path and isn't implemented.

## Troubleshooting

Nothing scales, or no KEDA objects get rendered. Confirm the data plane annotation:

```bash
kubectl get clusterdataplane default -o jsonpath='{.metadata.annotations}'
```

Without `openchoreo.dev/keda-based-scaling-backend=keda` the trait is inert on purpose. Also confirm the trait is attached under `spec.traits` and that the component type allows it:

```bash
kubectl get clustercomponenttype service -o jsonpath='{.spec.allowedTraits[*].name}'
```

The first request returns a 504 or hangs for about 60 seconds. This is usually the KEDA scaling pipeline warming up for a newly-created `InterceptorRoute`/`ScaledObject`, or the interceptor and scaler pods not being Ready yet. Wait for the `keda-add-ons-http-*` rollouts and retry, later cold starts finish in a few seconds.

The HTTP service never wakes, scale-up doesn't work, and the HPA shows a `cpu` metric. The companion `ScaledObject` reconciled before its `InterceptorRoute` existed, so the external scaler returned an empty metric spec and KEDA fell back to CPU. The trait renders the `InterceptorRoute` first, but if you hit this, re-apply the binding (or delete the `ScaledObject`) so KEDA re-reads the route's metric. Confirm with:

```bash
kubectl get scaledobject -n <wl-ns> -o jsonpath='{.items[0].spec.triggers}'
```

Every request 504s after about 60 seconds, even though the pod wakes and is Ready. The interceptor pods are missing the `openchoreo.dev/system-component` label, so the component's NetworkPolicy drops the interceptor's forwards. Re-install the HTTP add-on with the `additionalLabels` flag from the README and check:

```bash
kubectl get pods -n keda --show-labels
```

A request returns 404 or 503 after scaling to zero. Check that the kgateway `Backend` exists in the workload namespace and the `HTTPRoute` backendRef was repointed at it:

```bash
kubectl get backends.gateway.kgateway.dev,httproute -n <wl-ns> -o yaml
```

The agent can't create ScaledObjects (RBAC forbidden in the cluster-agent logs). Confirm the RBAC was applied and bound to the right ServiceAccount:

```bash
kubectl get clusterrolebinding openchoreo-cluster-agent-keda -o yaml
```

In-cluster (service-to-service) calls don't wake the service. Confirm the callee's Service became an ExternalName:

```bash
kubectl get svc <component> -n <wl-ns> -o jsonpath='{.spec.type}'   # should print ExternalName
```

Confirm the in-cluster FQDN shows up in the `InterceptorRoute` `rules[].hosts`. Then check that the multiport front actually selects the interceptor pods:

```bash
kubectl get endpoints keda-add-ons-http-interceptor-multiport -n keda
```

If that's empty, the selector in `keda-interceptor-multiport.yaml` doesn't match your add-on's interceptor pod labels. Compare with:

```bash
kubectl get svc keda-add-ons-http-interceptor-proxy -n keda -o jsonpath='{.spec.selector}'
```
