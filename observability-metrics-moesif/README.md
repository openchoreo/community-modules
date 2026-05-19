# Observability Metrics Module for Moesif

This module collects Ballerina application metrics from pod stdout using a
Fluent Bit DaemonSet and publishes them as Action events to the Moesif
collector API (`/v1/actions/batch`).

## Prerequisites

- A running Kubernetes cluster
- Ballerina applications deployed with `metricsLogsEnabled = true` in their
  `Config.toml` (the Ballerina runtime emits metric log lines to stdout when
  this is set)
- A Moesif account with a **master key** and **Collector Application ID**
  from [Moesif](https://www.moesif.com/)

## How It Works

Each Ballerina pod writes structured metric log lines to stdout whenever it
handles an HTTP request or an FTP file event. Each line is tagged with
`logger="metrics"` and contains fields such as `http.method`, `http.url`,
`http.status_code_group`, and `response_time_seconds`.

The Fluent Bit DaemonSet deployed by this chart:

1. **Tails** `/var/log/containers/*.log` on every node
2. **Enriches** records with pod metadata (labels and annotations) via the
   Kubernetes filter
3. **Filters** records, keeping only lines that contain `logger="metrics"`
4. **Transforms** each line using a Lua script that parses the logfmt fields
   and builds a Moesif Actions event (`action_name`, `request`, `metadata`)
5. **Routes** each record by rewriting the Fluent Bit tag to the pod's
   `moesif-app-id` annotation value (the Collector Application ID JWT), so
   that `header_tag` can set `X-Moesif-Application-Id` dynamically per pod
6. **Publishes** events in JSON batch format to `POST /v1/actions/batch`

### Authentication headers sent per request

| Header | Source | Purpose |
|--------|--------|---------|
| `X-Api-Token` | `moesif.masterKey` value | Cluster-level master key |
| `X-Moesif-Org-Id` | `moesif.orgId` value | Org identifier |
| `X-Moesif-App-Id` | `moesif.appId` value | App identifier |
| `X-Moesif-Application-Id` | Pod annotation `moesif-app-id` | Per-pod collector auth (JWT); set dynamically via Fluent Bit `header_tag` |

## Application Pod Requirements

Each Ballerina pod that should have its metrics collected must carry two
annotations in its pod template:

```yaml
metadata:
  annotations:
    moesif-org-id: "<YOUR_ORG_ID>"            # numeric, e.g. "586:761"
    moesif-app-id: "<COLLECTOR_APP_ID_JWT>"   # Moesif Collector Application ID
```

> **Why annotations and not labels?**
> Kubernetes label values are limited to 63 characters and may not contain
> `:`. The Moesif Collector Application ID is a ~134-character JWT that
> contains `:`, so it must be stored as an annotation.

## Installation

### Step 1 — Create a Kubernetes Secret for the master key (recommended)

```bash
kubectl create secret generic moesif-master-key-values \
  --from-literal=masterKey="<YOUR_MASTER_KEY>" \
  --namespace openchoreo-observability-plane
```

You can then reference the secret value at install time with
`--set moesif.masterKey="$(kubectl get secret ...)"`, or supply it directly
via `--set` (see Step 2).

### Step 2 — Install the Helm chart

```bash
helm upgrade --install observability-metrics-moesif \
  ./observability-metrics-moesif/helm \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --set moesif.masterKey="<YOUR_MASTER_KEY>" \
  --set moesif.orgId="<YOUR_ORG_ID>" \
  --set moesif.appId="<YOUR_APP_ID>"
```

### Step 3 — Annotate your application pods

Add the two required annotations to each Ballerina deployment's pod template:

```bash
kubectl patch deployment <your-deployment> \
  --type=json \
  -p='[
    {"op":"add","path":"/spec/template/metadata/annotations/moesif-org-id","value":"<ORG_ID>"},
    {"op":"add","path":"/spec/template/metadata/annotations/moesif-app-id","value":"<COLLECTOR_APP_ID_JWT>"}
  ]'
```

Or declare them directly in the pod template:

```yaml
# your-deployment.yaml
spec:
  template:
    metadata:
      annotations:
        moesif-org-id: "586:761"
        moesif-app-id: "eyJhcHAiOiI0ODc6OTkxIi..."
```

## Configuration Options

Create a `values.yaml` file for repeatable installs:

```yaml
# values.yaml
moesif:
  masterKey: "<YOUR_MASTER_KEY>"
  orgId: "586:761"
  appId: "487:991"
  host: api.moesif.net
  uri: /v1/actions/batch
  flush: 5

fluentBit:
  image:
    repository: fluent/fluent-bit
    tag: latest
```

Then install with:

```bash
helm upgrade --install observability-metrics-moesif \
  ./observability-metrics-moesif/helm \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  -f values.yaml
```

### Configuration Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `namespace` | Namespace where Fluent Bit is deployed | `openchoreo-observability-plane` |
| `moesif.masterKey` | Cluster-level master key sent as `X-Api-Token` | `""` |
| `moesif.orgId` | Org ID sent as `X-Moesif-Org-Id` | `""` |
| `moesif.appId` | App ID sent as `X-Moesif-App-Id` | `""` |
| `moesif.host` | Moesif API host | `api.moesif.net` |
| `moesif.port` | Moesif API port | `443` |
| `moesif.uri` | Collector endpoint path | `/v1/actions/batch` |
| `moesif.flush` | Fluent Bit flush interval in seconds | `5` |
| `fluentBit.image.repository` | Fluent Bit image repository | `fluent/fluent-bit` |
| `fluentBit.image.tag` | Fluent Bit image tag | `latest` |
| `fluentBit.resources` | CPU/memory resource requests and limits | see `values.yaml` |

## Troubleshooting

### Check Fluent Bit logs

```bash
kubectl -n openchoreo-observability-plane logs -f ds/fluent-bit
```

A healthy pipeline shows:

```
[info] [filter:kubernetes:kubernetes.0] connectivity OK
[info] [input:emitter:moesif_emitter] initializing
[info] [output:http:http.1] api.moesif.net:443, HTTP status=201
```

### Check that metric lines appear in pod stdout

```bash
kubectl logs -l app=<your-app> | grep 'logger="metrics"'
```

### Check that pod annotations are set correctly

```bash
kubectl get pods -l app=<your-app> \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.annotations.moesif-app-id}{"\n"}{end}'
```

If `moesif-app-id` is empty, Fluent Bit's `rewrite_tag` will not fire for
that pod's records and they will be silently dropped by the `null` output.

### Common HTTP status codes from Moesif

| Status | Meaning |
|--------|---------|
| `201` | Events accepted |
| `400` | Payload shape wrong — check the Lua transform output |
| `401` | `X-Moesif-Application-Id` missing or invalid — check the `moesif-app-id` annotation |

### Enable debug logging

Set `log_level: debug` in the Fluent Bit service config to see detailed
record processing. Edit the `moesif-master-key` Secret's `fluent-bit.yaml`
key and restart the DaemonSet:

```bash
kubectl rollout restart daemonset/fluent-bit \
  -n openchoreo-observability-plane
```

## Uninstalling

```bash
helm uninstall observability-metrics-moesif \
  --namespace openchoreo-observability-plane

# Remove the namespace (not deleted automatically by Helm):
kubectl delete namespace openchoreo-observability-plane
```
