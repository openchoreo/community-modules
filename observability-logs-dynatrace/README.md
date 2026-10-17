# Observability Logs Module for Dynatrace

This module collects container logs using [Fluent Bit](https://fluentbit.io), ships them to [Dynatrace](https://www.dynatrace.com) through the Log Ingest API, and serves them back to the OpenChoreo Observer from [Grail](https://docs.dynatrace.com/docs/platform/grail) with DQL.

It has two parts:

- **Fluent Bit** (a DaemonSet) tails container logs, flattens their Kubernetes metadata into Grail fields with a Lua filter, and posts them to `/api/v2/logs/ingest`.
- **The logs adapter** (a Deployment) implements the [logging adapter API](https://github.com/openchoreo/openchoreo/blob/main/openapi/observability-logs-adapter-api.yaml) by running DQL against the Grail query API.

| Capability                          | Supported                                                         |
| ----------------------------------- | ----------------------------------------------------------------- |
| Component and workflow logs         | Yes                                                               |
| Platform logs and filter values     | Yes                                                               |
| Audit logs, filter values, timeline | Yes, with `auditLogs.enabled=true`                                |
| Kubernetes events                   | Yes, shipped by `observability-events-otel-collector` (see below) |
| Log alert rules                     | No, the alert endpoints answer `501 Not Implemented`              |

> **Note:** The commands in this README install the latest module version. Refer to the [Compatibility](#compatibility) table below for the module version compatible with your OpenChoreo version.

## Prerequisites

- [OpenChoreo](https://openchoreo.dev) must be installed with the **observability plane** enabled for this module to work. The observer reaches this module's adapter at `http://logs-adapter:9098`, which is the default value of `observer.logsAdapter.url` in the `openchoreo-observability-plane` helm chart, so no change to the observability plane is needed unless that value was overridden.
- A Dynatrace SaaS environment with Grail and OpenPipeline (SaaS 1.295 or later). On older environments that route logs to the Classic pipeline, attribute keys are lowercased and arrays are stringified, which breaks the label and entitlement filters.

## Dynatrace credentials

Create these in Dynatrace:

| Credential                                                                                                                          | Used by    | Scopes                                      |
| ----------------------------------------------------------------------------------------------------------------------------------- | ---------- | ------------------------------------------- |
| [Access token](https://docs.dynatrace.com/docs/manage/identity-access-management/access-tokens-and-oauth-clients/access-tokens)     | Fluent Bit | `logs.ingest`                               |
| [Platform token](https://docs.dynatrace.com/docs/manage/identity-access-management/access-tokens-and-oauth-clients/platform-tokens) | Adapter    | `storage:logs:read`, `storage:buckets:read` |

Instead of the access token, Fluent Bit can send a platform token with `openpipeline:logs:ingest` by setting `dynatrace.ingest.authScheme=Bearer`. Instead of the platform token, the adapter can use an OAuth client (`adapter.authMode=oauth`) with the same read scopes.

OpenChoreo uses the External Secrets Operator to manage secrets. Add the tokens to a secret store and use an `ExternalSecret` to generate a Kubernetes secret named `dynatrace-credentials` in `openchoreo-observability-plane`. Refer to the [secret management guide](https://openchoreo.dev/docs/platform-engineer-guide/secret-management/) for more details.

For example, the commands below add the tokens to OpenBao and pull them from the `ClusterSecretStore` created earlier in the [OpenChoreo installation guide](https://openchoreo.dev/docs).

```bash
kubectl exec -it -n openbao openbao-0 -- \
    bao kv put secret/dynatrace-credentials \
    DT_INGEST_TOKEN='dt0c01.XXXX' \
    DT_PLATFORM_TOKEN='dt0s16.XXXX'
```

```bash
kubectl apply -f - <<EOF
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: dynatrace-credentials
  namespace: openchoreo-observability-plane
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: dynatrace-credentials
  data:
    - secretKey: DT_INGEST_TOKEN
      remoteRef:
        key: dynatrace-credentials
        property: DT_INGEST_TOKEN
    - secretKey: DT_PLATFORM_TOKEN
      remoteRef:
        key: dynatrace-credentials
        property: DT_PLATFORM_TOKEN
EOF
```

For OAuth, store `DT_OAUTH_CLIENT_ID` and `DT_OAUTH_CLIENT_SECRET` in the same secret instead of `DT_PLATFORM_TOKEN`.

## Installation

### Single-cluster topology

```bash
helm upgrade --install observability-logs-dynatrace \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-dynatrace \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set dynatrace.platformUrl=https://<env-id>.apps.dynatrace.com \
  --set fluentBitCustomizations.clusterInstance=<cluster-name>
```

The log ingest URL defaults to `https://<env-id>.live.dynatrace.com`, derived from `dynatrace.platformUrl`. Set `dynatrace.ingestUrl` to ship through an ActiveGate instead, e.g. `https://<activegate>:9999/e/<env-id>`.

> **Note:** `fluentBitCustomizations.clusterInstance` is required. It names the cluster the records were collected from and is stamped on every record as `k8s.cluster.name`, so pick a value that is unique across the clusters reporting to this Dynatrace environment.

### Multi-cluster topology

Each cluster ships straight to Dynatrace, so no ingress into the observability plane is needed. On the observability plane cluster, install the chart as above. On each remote cluster (data plane, workflow plane, control plane), install it with only Fluent Bit:

```bash
helm upgrade --install observability-logs-dynatrace \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-dynatrace \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --set adapter.enabled=false \
  --set dynatrace.ingestUrl=https://<env-id>.live.dynatrace.com \
  --set fluentBitCustomizations.clusterInstance=<cluster-name>
```

The `dynatrace-credentials` secret must exist on every remote cluster, but only needs `DT_INGEST_TOKEN` there.

## Kubernetes events

Events are collected by the [`observability-events-otel-collector`](../observability-events-otel-collector) module, which exports them to Dynatrace's OTLP endpoint. The adapter reads them back by their `k8s.event.reason` and `k8s.object.label.openchoreo.dev/*` attributes.

```bash
helm upgrade --install observability-events-otel-collector \
  oci://ghcr.io/openchoreo/helm-charts/observability-events-otel-collector \
  --namespace openchoreo-observability-plane --version 0.2.0 \
  -f - <<'EOF'
collector:
  extraEnv:
    - name: DT_INGEST_TOKEN
      valueFrom:
        secretKeyRef:
          name: dynatrace-credentials
          key: DT_INGEST_TOKEN
exporters:
  otlphttp/dynatrace:
    endpoint: "https://<env-id>.live.dynatrace.com/api/v2/otlp"
    headers:
      Authorization: "Api-Token ${env:DT_INGEST_TOKEN}"
pipelineExporters:
  - otlphttp/dynatrace
EOF
```

## Enable audit log collection

OpenChoreo's audit trail is written by `openchoreo-api` and `observer` to their container logs. With `auditLogs.enabled=true`, Fluent Bit routes those records to their own `log.source` (`openchoreo-audit-logs` by default) and lifts their filterable fields to `audit.*` Grail fields.

```bash
helm upgrade observability-logs-dynatrace \
  oci://ghcr.io/openchoreo/helm-charts/observability-logs-dynatrace \
  --namespace openchoreo-observability-plane \
  --version 0.0.0-latest-dev \
  --reuse-values \
  --set auditLogs.enabled=true
```

In a multi-cluster topology, set `auditLogs.enabled` on the cluster running `openchoreo-api` (the control plane) and on the one running `observer` (the observability plane).

### Trusted producers

`auditLogs.producers` is an allowlist. **Each entry grants a workload the right to write into the audit trail.** Entries match on the container log filename the kubelet writes, never on the content of the log line, so a pod outside the list that prints an audit-shaped line still lands in the container logs.

```yaml
auditLogs:
  producers:
    - producer: openchoreo-api
      namespace: openchoreo-control-plane
      container: api-server
    - producer: observer
      namespace: openchoreo-observability-plane
      container: observer
```

If the control plane or observability plane is installed into a non-default namespace, edit these entries, or audit collection silently stops.

### Audit retention

Grail keeps logs in the `default_logs` bucket for 35 days. To keep the audit trail for a year:

1. Create a bucket in **Settings > Storage management > Bucket storage management**, e.g. `openchoreo_audit_logs`, with table type `logs` and a retention of 365 days.
2. In **Settings > Process and contextualize > OpenPipeline > Logs > Pipelines**, add a *Bucket assignment* processor with the matching condition `log.source == "openchoreo-audit-logs"` that stores into that bucket.
3. Set `auditLogs.bucket=openchoreo_audit_logs`, so the adapter reads audit records from that bucket only.

### Configuration

| Value                       | Default                 | Purpose                                           |
| --------------------------- | ----------------------- | ------------------------------------------------- |
| `auditLogs.enabled`         | `false`                 | Route audit records to their own `log.source`     |
| `auditLogs.producers`       | the two above           | The trusted-producer allowlist                    |
| `auditLogs.bucket`          | `""`                    | Grail bucket the adapter reads audit records from |
| `dynatrace.auditLogsSource` | `openchoreo-audit-logs` | `log.source` stamped on audit records             |

## How records are stored

The adapter queries fields written by the module's own Fluent Bit Lua filter, so it only ever reads records this module shipped (`log.source == dynatrace.containerLogsSource`):

| Grail field                                                                                                                                 | Source                                         |
| ------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------- |
| `content`, `timestamp`                                                                                                                      | The log line and its CRI timestamp             |
| `k8s.cluster.name`                                                                                                                          | `fluentBitCustomizations.clusterInstance`      |
| `k8s.namespace.name`, `k8s.pod.name`, `k8s.container.name`, `k8s.node.name`, `k8s.pod.ip`, `k8s.pod.uid`, `container.image.name`            | Kubernetes metadata                            |
| `k8s.pod.labels`                                                                                                                            | Every pod label, as a `key=value` string array |
| `openchoreo.namespace`, `openchoreo.project[_uid]`, `openchoreo.component[_uid]`, `openchoreo.environment[_uid]`, `openchoreo.workflow_run` | OpenChoreo pod labels                          |

Log levels are derived from the message text, as the OpenSearch and OpenObserve modules do.

## Applying Fluent Bit configuration changes

Fluent Bit reads its configuration from the `fluent-bit` ConfigMap rendered by this chart, and only at startup. A `helm upgrade` that changes it does not restart the Fluent Bit pods. Restart the DaemonSet after such an upgrade, in every cluster where the release was upgraded:

```bash
kubectl rollout restart daemonset/fluent-bit -n openchoreo-observability-plane
```

The same applies after the `dynatrace-credentials` secret changes. The adapter restarts on its own when its configuration changes.

## Limitations

- **Alerting** is not implemented: create, update, get and delete of alert rules answer `501`. Configure log alerting in Dynatrace with anomaly detectors and workflows.
- **Record age:** Dynatrace rejects records older than 24 hours. On first install Fluent Bit reads existing log files from the start (`fluentBitCustomizations.readFromHead`), so older lines on a long-running node are dropped.
- **Array size:** Grail keeps at most 32 values per attribute, so a pod with more than 32 labels, or an audit actor with more than 32 entitlement values, has the rest dropped from `k8s.pod.labels` or `audit.actor.entitlements`.
- **Renaming the secret:** Fluent Bit's environment comes from the `fluent-bit` subchart's values. If you change `dynatrace.credentials.name`, also override `fluent-bit.env` to point at it.

## Troubleshooting

Start with the adapter and Fluent Bit logs:

```bash
kubectl -n openchoreo-observability-plane logs deploy/logs-adapter-dynatrace --tail=50
kubectl -n openchoreo-observability-plane logs ds/fluent-bit --tail=50 | grep 'output:http'
```

A healthy adapter logs `Connected to Dynatrace Grail` at startup, and every Fluent Bit flush reports `HTTP status=204`.

### Adapter is in `CrashLoopBackOff`

The adapter queries Grail at startup and exits if it cannot.

- `Failed to query Dynatrace Grail` with a `401` or `403` in the error: the platform token is wrong, lacks `storage:logs:read` or `storage:buckets:read`, or its user has no policy that grants reading the buckets involved.
- `DT_PLATFORM_URL must be an http(s) URL`: set `dynatrace.platformUrl` to the **apps** URL with its scheme, `https://<env-id>.apps.dynatrace.com`.

### Fluent Bit reports a status other than `204`

| Status       | Meaning                                                                                                                |
| ------------ | ---------------------------------------------------------------------------------------------------------------------- |
| `200`        | Partial success: some records were rejected, usually because they are older than 24 hours                              |
| `400`        | The whole batch was rejected. On first install this is normally old log lines, and it stops once Fluent Bit catches up |
| `401`, `403` | The ingest token is wrong or lacks the `logs.ingest` scope                                                             |

If `400` persists, set `Log_Level debug` in the Fluent Bit configuration to see the response body.

### Fluent Bit pod is stuck in `ContainerCreating` on k3d

The pod events show `/etc/machine-id is not a file`. k3d nodes have no machine ID. Create one on each node:

```bash
docker exec k3d-<cluster>-server-0 sh -c "cat /proc/sys/kernel/random/uuid | tr -d '-' > /etc/machine-id"
```

### Records are in Dynatrace but the Observer returns nothing

- Check that the Observer reaches this module's adapter: the `logs-adapter` Service must select `app=logs-adapter-dynatrace`. If another logs module was installed before, its adapter may still own the Service.
- If platform logs work but component logs are empty, the pods lack the `openchoreo.dev/*` labels, or the query uses the wrong component or environment UID.
- Set `adapter.logLevel=debug` to log every DQL query the adapter runs, and run one in a Dynatrace Notebook to see what Grail returns.

### `k8s.pod.labels` is a single string and label filters match nothing

The environment routes logs through the Classic pipeline, which lowercases attribute keys and stringifies arrays. Enable OpenPipeline for logs.

### Kubernetes events are empty

Check the events collector logs for `Exporting failed`. Its endpoint must be `https://<env-id>.live.dynatrace.com/api/v2/otlp`, not the `apps` URL.

## Configuration reference

| Value                                     | Default                                                   | Description                                                                                                                                        |
| ----------------------------------------- | --------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `dynatrace.platformUrl`                   | Required                                                  | Grail API base URL, `https://<env-id>.apps.dynatrace.com`. Required when `adapter.enabled=true`.                                                   |
| `dynatrace.ingestUrl`                     | Derived from `platformUrl`                                | Log ingest base URL. Set it to ship through an ActiveGate, or on clusters that run only Fluent Bit.                                                |
| `dynatrace.ingest.authScheme`             | `Api-Token`                                               | `Api-Token` for an access token, `Bearer` for a platform or OAuth token with `openpipeline:logs:ingest`.                                           |
| `dynatrace.ingest.tlsVerify`              | `true`                                                    | Verify the ingest endpoint's TLS certificate.                                                                                                      |
| `dynatrace.containerLogsSource`           | `openchoreo-container-logs`                               | `log.source` stamped on container logs. Must match on every cluster reporting to one environment.                                                  |
| `dynatrace.auditLogsSource`               | `openchoreo-audit-logs`                                   | `log.source` stamped on audit records. Must differ from `containerLogsSource`.                                                                     |
| `dynatrace.credentials.name`              | `dynatrace-credentials`                                   | Secret holding the Dynatrace credentials, in the release namespace.                                                                                |
| `dynatrace.credentials.create`            | `false`                                                   | Have the chart create the Secret from the inline values below. For trials only.                                                                    |
| `auditLogs.enabled`                       | `false`                                                   | Route audit records to their own `log.source`.                                                                                                     |
| `auditLogs.producers`                     | `openchoreo-api`, `observer`                              | The trusted-producer allowlist. See [Trusted producers](#trusted-producers).                                                                       |
| `auditLogs.bucket`                        | `""`                                                      | Grail bucket the adapter reads audit records from. Empty reads every bucket the token can see.                                                     |
| `fluent-bit.enabled`                      | `true`                                                    | Toggle the Fluent Bit DaemonSet.                                                                                                                   |
| `fluentBitCustomizations.clusterInstance` | Required                                                  | Cluster name stamped on every record as `k8s.cluster.name`. Required when Fluent Bit is enabled.                                                   |
| `fluentBitCustomizations.excludePaths`    | `/var/log/containers/*_kube-system_*.log`                 | Container log files that are not shipped. Fluent Bit's own logs are always excluded.                                                               |
| `fluentBitCustomizations.readFromHead`    | `true`                                                    | Read container log files from the beginning on first start.                                                                                        |
| `adapter.enabled`                         | `true`                                                    | Toggle the adapter Deployment.                                                                                                                     |
| `adapter.authMode`                        | `platformToken`                                           | `platformToken` or `oauth`.                                                                                                                        |
| `adapter.oauth.tokenUrl`                  | `https://sso.dynatrace.com/sso/oauth2/token`              | OAuth token endpoint, when `authMode=oauth`.                                                                                                       |
| `adapter.oauth.scope`                     | `storage:logs:read storage:buckets:read`                  | OAuth scopes requested.                                                                                                                            |
| `adapter.oauth.resource`                  | `""`                                                      | Optional resource URN, e.g. `urn:dtaccount:<account-uuid>`.                                                                                        |
| `adapter.queryTimeout`                    | `30s`                                                     | Upper bound for one Grail query, including polling.                                                                                                |
| `adapter.allowInsecureHttp`               | `false`                                                   | Accept `http://` for `dynatrace.platformUrl` and `adapter.oauth.tokenUrl`. Credentials then travel in cleartext, so use it only for a test double. |
| `adapter.logLevel`                        | `info`                                                    | `debug`, `info`, `warn` or `error`.                                                                                                                |
| `adapter.image.repository`                | `ghcr.io/openchoreo/observability-logs-dynatrace-adapter` | Adapter container image.                                                                                                                           |
| `adapter.image.tag`                       | Chart `appVersion`                                        | Image tag.                                                                                                                                         |
| `adapter.image.pullPolicy`                | `IfNotPresent`                                            | Image pull policy.                                                                                                                                 |
| `adapter.resources`                       | `200m/256Mi` limits, `50m/128Mi` requests                 | Adapter resource requests and limits.                                                                                                              |

## Dependencies

Bundled upstream Helm charts:

| Chart      | Repository                           |
| ---------- | ------------------------------------ |
| fluent-bit | https://fluent.github.io/helm-charts |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| OpenChoreo Version | Module Version |
| ------------------ | -------------- |
| v1.3.0 and later   | 0.1.x          |
