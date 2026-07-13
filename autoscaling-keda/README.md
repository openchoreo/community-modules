# Autoscaling with KEDA

This module scales OpenChoreo components on demand with [KEDA](https://keda.sh). An HTTP service can drop to zero replicas while it's idle and wake up on the first request. The KEDA HTTP Add-on interceptor holds that request while a pod starts, so nothing gets dropped, and in-cluster service-to-service calls wake the service the same way. Workers scale on cron or queue triggers instead. You get all of this by attaching the `keda-scaling` trait to a plain service or worker component and setting a few parameters.

The module has three pieces:

- `keda-scaling-trait.yaml` holds the `ClusterTrait` that renders the right KEDA objects for each data plane.
- `cluster-agent-keda-rbac.yaml` gives the data-plane cluster-agent permission to manage KEDA objects.
- `keda-interceptor-multiport.yaml` is a multi-port Service that fronts the interceptor so in-cluster calls can wake a service.

To use it, add `keda-scaling` to the `allowedTraits` of your existing component types (a one-line patch, see Install step 3) and attach the trait to a component. The module ships no component types of its own; it works with whatever `service`/`web-application`/`worker` types your platform already runs.

KEDA core and the KEDA HTTP Add-on come from their own upstream Helm charts (see Install below). This module is only the OpenChoreo glue.

## How it works

Attach the `keda-scaling` trait to a component and it renders the right KEDA objects; detach it and they're gone. Attaching is the on/off switch, there's no separate flag to set. The trait assumes every data plane it targets runs KEDA. Mixed fleets, where only some data planes run KEDA, are a separate design still being worked out; for now keep the data planes a component promotes across identical.

| Mode | When | What KEDA creates |
|------|------|-------------------|
| **HTTP** | service or web-application with one external HTTP endpoint, no `trigger.type` | `InterceptorRoute` + companion `ScaledObject`; gateway and in-cluster traffic both wake the component |
| **Trigger** | any component with `trigger.type` set | `ScaledObject` with that trigger (cron, kafka, rabbitmq, …) |

You need OpenChoreo 1.2 or later. For the architecture, tuning, and extension details, see [CONFIGURATION.md](./CONFIGURATION.md).

## Prerequisites

- An OpenChoreo 1.2+ control plane and data plane. For a fresh local cluster, follow the [single-cluster k3d guide](https://github.com/openchoreo/openchoreo/blob/main/install/k3d/single-cluster/README.md) through step 5.
- `kubectl`, `helm`, `jq`, `yq`, and access to the cluster.
- The data-plane gateway needs to be kgateway, which is the OpenChoreo default. For other Gateway API implementations, see [CONFIGURATION.md](./CONFIGURATION.md).

## Install

### 1. Install KEDA core, then the HTTP Add-on

KEDA's CRDs have to be established before the HTTP Add-on's resources are applied, so install them as two separate releases.

```bash
helm repo add kedacore https://kedacore.github.io/charts && helm repo update kedacore

# KEDA core
helm upgrade --install keda kedacore/keda --version 2.20.1 \
  --namespace keda --create-namespace
kubectl wait --for=condition=Established \
  crd/scaledobjects.keda.sh crd/triggerauthentications.keda.sh --timeout=120s
kubectl wait --for=condition=Available deployment/keda-operator -n keda --timeout=180s

# KEDA HTTP Add-on (interceptor + scaler)
helm upgrade --install keda-add-ons-http kedacore/keda-add-ons-http --version 0.14.1 \
  --namespace keda \
  --set 'additionalLabels.openchoreo\.dev/system-component=keda-http-addon'
kubectl wait --for=condition=Established crd/interceptorroutes.http.keda.sh --timeout=120s
```

> The `additionalLabels` flag matters. OpenChoreo renders a NetworkPolicy for each component that only admits traffic from the same namespace or from pods carrying the `openchoreo.dev/system-component` label. Leave the flag out and, on any cluster whose CNI enforces NetworkPolicy (k3s and k3d included), the interceptor's forwarded requests get dropped silently. Every request then times out with a 504, even though the wake-up itself worked.

### 2. Apply the trait and data-plane resources

```bash
kubectl apply --server-side -f https://raw.githubusercontent.com/openchoreo/community-modules/main/autoscaling-keda/keda-scaling-trait.yaml

kubectl apply \
  -f https://raw.githubusercontent.com/openchoreo/community-modules/main/autoscaling-keda/cluster-agent-keda-rbac.yaml \
  -f https://raw.githubusercontent.com/openchoreo/community-modules/main/autoscaling-keda/keda-interceptor-multiport.yaml
```

### 3. Enable the trait on your component types

Add `keda-scaling` to `allowedTraits` on the component types you use. You don't need to delete or recreate any components, the change takes effect right away:

```bash
kubectl patch clustercomponenttype <your-type> --type=json \
  -p '[{"op":"add","path":"/spec/allowedTraits/-","value":{"kind":"ClusterTrait","name":"keda-scaling"}}]'
```

The patch adds the trait to whatever your live type already is, so it never drifts from your platform's defaults. [CONFIGURATION.md](./CONFIGURATION.md) covers the shape a component type has to have for the HTTP scaling path to work.

### 4. Verify

```bash
kubectl rollout status deploy/keda-add-ons-http-interceptor -n keda --timeout=180s
kubectl rollout status deploy/keda-add-ons-http-external-scaler -n keda --timeout=180s

kubectl get clustertrait keda-scaling
kubectl get clustercomponenttype service web-application worker \
  -o jsonpath='{range .items[*]}{.metadata.name}{": "}{.spec.allowedTraits[*].name}{"\n"}{end}'
kubectl get clusterrolebinding openchoreo-cluster-agent-keda

# Non-empty Endpoints means the interceptor multiport Service is working:
kubectl get endpoints keda-add-ons-http-interceptor-multiport -n keda
```

## Quick start

### HTTP service that scales to zero

```bash
kubectl apply -f https://raw.githubusercontent.com/openchoreo/community-modules/main/autoscaling-keda/samples/http-service.yaml
```

Find the workload namespace OpenChoreo created. Re-run it if the output is empty, it shows up once the ReleaseBinding has rendered:

```bash
WL_NS=$(kubectl get ns -o name | sed 's|namespace/||' | grep '^dp-default-default-development')
echo "$WL_NS"
```

Watch it scale to zero after about 30 seconds of no traffic:

```bash
kubectl get deploy -n "$WL_NS" -w        # replicas -> 0, then Ctrl-C
```

Hit it, and the interceptor holds the request while it wakes a pod:

```bash
curl http://development-default.openchoreoapis.localhost:19080/greeter-http/
# Hello from OpenChoreo Autoscaling with KEDA!
```

```bash
kubectl get deploy -n "$WL_NS"           # replicas back to 1 right after the request
```

The very first cold start after you deploy can be slow, or return a 504 while KEDA registers the new scaler. Just retry once. After that, cold starts take about 2 to 4 seconds, and the trait sets a 120s route timeout so the gateway holds the request while the pod comes up.

### HTTP web application that scales to zero

```bash
kubectl apply -f https://raw.githubusercontent.com/openchoreo/community-modules/main/autoscaling-keda/samples/http-webapp.yaml
```

Find its generated hostname and call the root path:

```bash
WL_NS=$(kubectl get ns -o name | sed 's|namespace/||' | grep '^dp-default-default-development')
WEBAPP_HOST=$(kubectl get httproute -n "$WL_NS" \
  -l openchoreo.dev/component=keda-webapp,openchoreo.dev/endpoint-visibility=external \
  -o jsonpath='{.items[0].spec.hostnames[0]}')
curl "http://$WEBAPP_HOST:19080/"
```

### Cron worker that scales to zero

```bash
kubectl apply -f https://raw.githubusercontent.com/openchoreo/community-modules/main/autoscaling-keda/samples/cron-worker.yaml
```

The worker runs at 1 replica inside its cron window (`03:00–04:00 UTC` in the sample) and sits at zero the other 23 hours. So right after you apply it, you see the scaled-to-zero state:

```bash
WL_NS=$(kubectl get ns -o name | sed 's|namespace/||' | grep '^dp-default-default-development')
kubectl get scaledobject -n "$WL_NS"
kubectl get deploy -n "$WL_NS"
```

To prove scaling quickly, edit the component's `trigger.metadata.start`/`end` to a window that starts a couple of minutes from now and watch the Deployment go `0 -> 1 -> 0`. Swap `type`/`metadata` for any [KEDA scaler](https://keda.sh/docs/latest/scalers/) you like (`rabbitmq`, `kafka`, `prometheus`, and so on).

## Usage

Attach the trait under `spec.traits` on your component:

```yaml
spec:
  componentType:
    kind: ClusterComponentType
    name: deployment/service          # or deployment/worker
  traits:
    - kind: ClusterTrait
      name: keda-scaling
      instanceName: keda-scaling
      parameters:
        minReplicas: 0
        maxReplicas: 5
        cooldownPeriod: 300           # seconds idle before scaling down
        trigger:                      # omit for HTTP services; required for workers
          type: cron
          metadata:
            timezone: "Etc/UTC"
            start: "0 8 * * *"
            end: "0 20 * * *"
            desiredReplicas: "1"
```

Platform engineers can floor the bounds per environment from the `ReleaseBinding`:

```yaml
spec:
  traitEnvironmentConfigs:
    keda-scaling:
      minReplicas: 1        # never scale to zero in production
      maxReplicas: 10
```

The full parameter reference, tuning notes (request rate vs. concurrency), extension points, architecture, and troubleshooting all live in [CONFIGURATION.md](./CONFIGURATION.md).

## Composing with an API gateway trait

`keda-scaling` can share a component with an edge API-management trait such as `api-management` (from the `gateway-wso2-api-platform` module), so one service gets both scale-to-zero and API management. Both traits repoint the external `HTTPRoute`, so two rules apply:

- **Order matters.** List the API-gateway trait *before* `keda-scaling` in `spec.traits`. The gateway trait takes over the edge route, and `keda-scaling` then sees it no longer owns that route and skips its own edge-route patches, keeping just the in-cluster ExternalName wiring. Reverse the order and the gateway trait fails to render.
- **The chain.** Traffic flows edge gateway -> API gateway (e.g. WSO2) -> the component's ExternalName Service -> the KEDA interceptor -> the pod, and the interceptor wakes the pod on the first request. Idle scale-to-zero and the API gateway's own policies (rate limiting, auth, headers) both keep working.

```yaml
traits:
  - name: api-management        # edge trait first: it owns the external route
    instanceName: my-api
    kind: ClusterTrait
    parameters:
      rateLimit:
        enabled: true
        limits:
          - requests: 100
            duration: "1m"
  - name: keda-scaling          # keda second: defers the edge route, keeps scale-to-zero
    instanceName: keda-scaling
    kind: ClusterTrait
    parameters:
      minReplicas: 0
```

See [CONFIGURATION.md](./CONFIGURATION.md) for how the deferral works and why the interceptor accepts the API gateway's upstream host.

## Limitations

- One external HTTP endpoint per service. The interceptor routes by `Host` only (it strips the port first), and an ExternalName alias can't remap ports. See CONFIGURATION.md for the reasoning.
- The HTTP path is kgateway-specific. It uses `gateway.kgateway.dev/Backend`, so you'll need to adapt it for other Gateway API implementations per CONFIGURATION.md.
- It doesn't mix with HPA traits. Both want to own the Deployment's replica count, so don't attach both to the same component.

## Uninstall

```bash
# Detach the trait from your components first (remove the keda-scaling entry from
# spec.traits), then:
kubectl delete \
  -f https://raw.githubusercontent.com/openchoreo/community-modules/main/autoscaling-keda/keda-interceptor-multiport.yaml \
  -f https://raw.githubusercontent.com/openchoreo/community-modules/main/autoscaling-keda/cluster-agent-keda-rbac.yaml \
  -f https://raw.githubusercontent.com/openchoreo/community-modules/main/autoscaling-keda/keda-scaling-trait.yaml
# To revert the component types, remove keda-scaling from allowedTraits:
#   kubectl patch clustercomponenttype service web-application worker --type=json \
#     -p '[{"op":"remove","path":"/spec/allowedTraits/<index>"}]'
#   (find the index with: kubectl get clustercomponenttype service -o jsonpath='{.spec.allowedTraits}')
# optionally:
# helm uninstall keda-add-ons-http keda -n keda
```
