# Observability Tracing Module for AWS X-Ray

|               |           |
| ------------- | --------- |
| Code coverage | [![Codecov](https://codecov.io/gh/openchoreo/community-modules/branch/main/graph/badge.svg?component=observability_tracing_aws_xray)](https://codecov.io/gh/openchoreo/community-modules) |

This module supports both:

- **EKS clusters** using **EKS Pod Identity**.
- **Non-EKS Kubernetes clusters** such as k3d, kind, or Kubernetes
  running outside AWS, using static AWS credentials.

## Table of contents

1. [Architecture](#architecture)
2. [Choose a deployment topology](#choose-a-deployment-topology)
3. [Prerequisites](#prerequisites)
4. [IAM permissions](#iam-permissions)
5. [Installation on EKS with Pod Identity](#installation-on-eks-with-pod-identity)
6. [Installation on non-EKS clusters with static credentials](#installation-on-non-eks-clusters-with-static-credentials)
7. [Verify trace ingestion and querying](#verify-trace-ingestion-and-querying)
8. [Troubleshooting](#troubleshooting)
9. [Configuration reference](#configuration-reference)

## Architecture

The chart deploys two workloads in the OpenChoreo observability plane:

1. An **OpenTelemetry Collector** Deployment that receives traces via
   OTLP (gRPC on port 4317 and HTTP on port 4318), enriches them with
   Kubernetes pod labels via the `k8sattributes` processor, applies
   tail sampling for rate limiting, and exports them to AWS X-Ray via
   the `awsxray` exporter.
2. A Go **X-Ray Tracing Adapter** Deployment that implements the
   OpenChoreo Tracing Adapter API and queries traces from X-Ray.

The collector runs in **Deployment mode** (not DaemonSet) because traces
are push-based — instrumented applications send OTLP traces to the
collector's Service endpoint.

The `awsxray` exporter is configured with `indexed_attributes` so that
OpenChoreo labels are stored as filterable X-Ray annotations:

| OpenChoreo label | X-Ray annotation key |
| --- | --- |
| `openchoreo.dev/namespace` | `openchoreo_dev_namespace` |
| `openchoreo.dev/component-uid` | `openchoreo_dev_component_uid` |
| `openchoreo.dev/project-uid` | `openchoreo_dev_project_uid` |
| `openchoreo.dev/environment-uid` | `openchoreo_dev_environment_uid` |

The adapter queries X-Ray using these annotations as filter expressions,
enabling scope-based trace retrieval.

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1alpha1/traces/query` | Queries X-Ray `GetTraceSummaries` for traces matching the search scope. |
| `POST /api/v1alpha1/traces/{traceId}/spans/query` | Fetches all spans (segments + subsegments) for a trace via `BatchGetTraces` and flattens the segment tree. |
| `GET /api/v1alpha1/traces/{traceId}/spans/{spanId}` | Returns full detail for a specific span within a trace, including attributes and resource attributes. |
| `GET /healthz` | Readiness and liveness check. Returns `200` once the adapter is ready. |

## Choose a deployment topology

Choose the deployment topology first, then choose the AWS authentication model for each cluster.

| Topology | Install location | Purpose | Required Helm values |
| --- | --- | --- | --- |
| Single cluster | The OpenChoreo cluster where the observability plane and workloads run together. | Deploys the adapter and OpenTelemetry collector. | Defaults. |
| Observability plane cluster | The cluster where the OpenChoreo observability plane is installed. | Deploys only the X-Ray Tracing Adapter. | `opentelemetry-collector.enabled=false` |
| Data-plane cluster | Each cluster that runs OpenChoreo workloads. | Deploys only the OpenTelemetry collector. | `adapter.enabled=false` |

For one OpenChoreo installation, keep these values identical across all participating clusters:

- `region`

AWS X-Ray is the shared managed backend. Remote workload clusters write directly to X-Ray and do not need network connectivity back to a self-hosted tracing datastore. All clusters that belong to one OpenChoreo installation write to the same X-Ray service, and the observability-plane adapter reads from X-Ray.

## Prerequisites

Before installing this module, make sure the following are available.

### OpenChoreo prerequisites

- OpenChoreo is installed.
- The `openchoreo-observability-plane` Helm chart is installed.
- Workload pods include OpenChoreo labels such as:
  - `openchoreo.dev/namespace`
  - `openchoreo.dev/component-uid`
  - `openchoreo.dev/environment-uid`
  - `openchoreo.dev/project-uid`
- Applications are instrumented with OpenTelemetry and configured to send
  OTLP traces to the collector endpoint.

See the [OpenChoreo documentation](https://openchoreo.dev/docs) for the base
installation steps.

### Local tooling

Install the following tools on your machine:

- `helm`
- `kubectl`
- `jq`
- `aws` CLI v2

### Cluster prerequisites

The OpenTelemetry collector expects:

- Kubernetes API access to pods and replicasets (for
  the `k8sattributes` processor).

### AWS prerequisites

You need:

- An AWS account.
- An AWS region, for example `eu-north-1`.
- An IAM principal with the permissions described in [IAM permissions](#iam-permissions).

For EKS, use IAM roles with **EKS Pod Identity** or IRSA. For non-EKS clusters
such as k3d or kind, use an IAM user with access keys.

## IAM permissions

The X-Ray tracing adapter needs permissions for two paths:

1. Startup identity check.
2. X-Ray trace queries.

The OpenTelemetry collector needs permission to write trace segments to X-Ray.

Use these policies based on the credential model:

- **EKS Pod Identity or IRSA:** keep the adapter and OpenTelemetry collector policies
  separate and attach them to separate roles. This keeps each ServiceAccount
  least-privileged.
- **Static credentials:** use one IAM user and attach the
  [combined static-credentials IAM policy](#combined-static-credentials-iam-policy),
  because the same Kubernetes Secret is shared by the adapter and OpenTelemetry
  collector.

### Adapter IAM policy

Create the following custom IAM policy and attach it to the adapter IAM
principal when using separate EKS identities.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Startup",
      "Effect": "Allow",
      "Action": "sts:GetCallerIdentity",
      "Resource": "*"
    },
    {
      "Sid": "XRayRead",
      "Effect": "Allow",
      "Action": [
        "xray:GetTraceSummaries",
        "xray:BatchGetTraces",
        "xray:GetTraceGraph"
      ],
      "Resource": "*"
    }
  ]
}
```

### OpenTelemetry collector IAM policy

Create the following custom IAM policy and attach it to the OpenTelemetry
collector IAM principal when using separate EKS identities.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "XRayWrite",
      "Effect": "Allow",
      "Action": [
        "xray:PutTraceSegments",
        "xray:PutTelemetryRecords",
        "xray:GetSamplingRules",
        "xray:GetSamplingTargets",
        "xray:GetSamplingStatisticSummaries"
      ],
      "Resource": "*"
    }
  ]
}
```

### Combined static-credentials IAM policy

Use this policy for non-EKS clusters where one IAM user backs the shared static
AWS credentials Secret.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Startup",
      "Effect": "Allow",
      "Action": "sts:GetCallerIdentity",
      "Resource": "*"
    },
    {
      "Sid": "XRayReadAndWrite",
      "Effect": "Allow",
      "Action": [
        "xray:GetTraceSummaries",
        "xray:BatchGetTraces",
        "xray:GetTraceGraph",
        "xray:PutTraceSegments",
        "xray:PutTelemetryRecords",
        "xray:GetSamplingRules",
        "xray:GetSamplingTargets",
        "xray:GetSamplingStatisticSummaries"
      ],
      "Resource": "*"
    }
  ]
}
```

## Installation on EKS with Pod Identity

This is the recommended installation path for EKS clusters.

### Step 1 — Export shared values

```bash
export AWS_REGION=<your-aws-region>
export NS=openchoreo-observability-plane
```

Make sure your `kubectl` context points to the target EKS cluster:

```bash
kubectl config current-context
```

Also verify that the EKS Pod Identity Agent add-on is installed:

```bash
kubectl -n kube-system get ds eks-pod-identity-agent
```

Pod Identity credentials are injected only when the Pod Identity Agent is
running.

### Step 2 — Create IAM roles

Create an IAM role for the adapter, for example:

```text
OpenChoreoXRayTracingRoleForAdapter
```

Attach the custom [Adapter IAM policy](#adapter-iam-policy).

Create another IAM role for the OpenTelemetry collector, for example:

```text
OpenChoreoXRayTracingRoleForCollector
```

Attach the custom [OpenTelemetry collector IAM policy](#opentelemetry-collector-iam-policy).

Use the following trust policy for both roles when using EKS Pod Identity:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Service": "pods.eks.amazonaws.com"
      },
      "Action": [
        "sts:AssumeRole",
        "sts:TagSession"
      ]
    }
  ]
}
```

To create the roles and attach policies using the AWS CLI:

```bash
export AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

POD_IDENTITY_TRUST_POLICY='{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Service": "pods.eks.amazonaws.com"
      },
      "Action": [
        "sts:AssumeRole",
        "sts:TagSession"
      ]
    }
  ]
}'

# Create the adapter role
aws iam create-role \
  --role-name OpenChoreoXRayTracingRoleForAdapter \
  --assume-role-policy-document "$POD_IDENTITY_TRUST_POLICY"

# Create the adapter policy
aws iam create-policy \
  --policy-name OpenChoreoXRayTracingAdapterPolicy \
  --policy-document "$(cat <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Startup",
      "Effect": "Allow",
      "Action": "sts:GetCallerIdentity",
      "Resource": "*"
    },
    {
      "Sid": "XRayRead",
      "Effect": "Allow",
      "Action": [
        "xray:GetTraceSummaries",
        "xray:BatchGetTraces",
        "xray:GetTraceGraph"
      ],
      "Resource": "*"
    }
  ]
}
EOF
)"

aws iam attach-role-policy \
  --role-name OpenChoreoXRayTracingRoleForAdapter \
  --policy-arn "arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OpenChoreoXRayTracingAdapterPolicy"

# Create the collector role
aws iam create-role \
  --role-name OpenChoreoXRayTracingRoleForCollector \
  --assume-role-policy-document "$POD_IDENTITY_TRUST_POLICY"

# Create the collector policy
aws iam create-policy \
  --policy-name OpenChoreoXRayTracingCollectorPolicy \
  --policy-document "$(cat <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "XRayWrite",
      "Effect": "Allow",
      "Action": [
        "xray:PutTraceSegments",
        "xray:PutTelemetryRecords",
        "xray:GetSamplingRules",
        "xray:GetSamplingTargets",
        "xray:GetSamplingStatisticSummaries"
      ],
      "Resource": "*"
    }
  ]
}
EOF
)"

aws iam attach-role-policy \
  --role-name OpenChoreoXRayTracingRoleForCollector \
  --policy-arn "arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OpenChoreoXRayTracingCollectorPolicy"
```

### Step 3 — Install the module

Use the command that matches the cluster's topology. The following Helm commands will time out or the pods will enter CrashLoopBackOff since the Pod Identity associations are not created yet. Everything will work after Step 5 is completed.

#### Single-cluster install

Deploy the adapter and OpenTelemetry collector in one cluster:

```bash
helm upgrade --install observability-tracing-aws-xray \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-aws-xray \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION"
```

#### Observability plane install

Deploy only the adapter in the observability plane cluster:

```bash
helm upgrade --install observability-tracing-aws-xray \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-aws-xray \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set opentelemetry-collector.enabled=false
```

#### Data-plane install

Deploy only the OpenTelemetry collector in each workload cluster:

```bash
helm upgrade --install observability-tracing-aws-xray \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-aws-xray \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set adapter.enabled=false
```

### Step 4 — Create Pod Identity associations

EKS Pod Identity links a Kubernetes ServiceAccount to an IAM role. Each association is scoped to a single EKS cluster, namespace, and ServiceAccount. You must create these associations on every EKS cluster that participates in the install.

#### Single-cluster topology

Create two Pod Identity associations on the EKS cluster, all in the `$NS` namespace:

| ServiceAccount | Used by | IAM role to associate |
| --- | --- | --- |
| `tracing-adapter-aws-xray` | Adapter trace queries and STS startup check. | The role with the [Adapter IAM policy](#adapter-iam-policy) attached. |
| `opentelemetry-collector` | OpenTelemetry collector trace export to X-Ray. | The role with the [OpenTelemetry collector IAM policy](#opentelemetry-collector-iam-policy) attached. |

#### Multi-cluster topology

In a multi-cluster setup, each EKS cluster only runs a subset of the components. Create Pod Identity associations only for the ServiceAccounts that exist in that cluster.

**Observability plane cluster** (runs only the adapter):

| ServiceAccount | IAM role to associate |
| --- | --- |
| `tracing-adapter-aws-xray` | The role with the [Adapter IAM policy](#adapter-iam-policy) attached. |

The `opentelemetry-collector` ServiceAccount does not exist in this cluster because `opentelemetry-collector.enabled=false`.

**Each data-plane cluster** (runs only the OpenTelemetry collector):

| ServiceAccount | IAM role to associate |
| --- | --- |
| `opentelemetry-collector` | The role with the [OpenTelemetry collector IAM policy](#opentelemetry-collector-iam-policy) attached. |

The `tracing-adapter-aws-xray` ServiceAccount does not exist in these clusters because `adapter.enabled=false`.

#### How to create a Pod Identity association

You can create associations from the AWS Console:

```text
EKS → Cluster → Access → Pod Identity associations → Create
```

For each association, fill in:

- **Namespace**: the namespace where the module is installed (for example, `openchoreo-observability-plane`).
- **Service Account**: the ServiceAccount name from the tables above.
- **IAM Role**: the ARN of the corresponding IAM role.

Alternatively, use the AWS CLI. Export the role ARNs from Step 2:

```bash
export ADAPTER_ROLE_ARN="arn:aws:iam::${AWS_ACCOUNT_ID}:role/OpenChoreoXRayTracingRoleForAdapter"
export COLLECTOR_ROLE_ARN="arn:aws:iam::${AWS_ACCOUNT_ID}:role/OpenChoreoXRayTracingRoleForCollector"
```

**Single-cluster topology** — create both associations:

```bash
export EKS_CLUSTER_NAME=<your-eks-cluster-name>

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account tracing-adapter-aws-xray \
  --role-arn "$ADAPTER_ROLE_ARN"

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account opentelemetry-collector \
  --role-arn "$COLLECTOR_ROLE_ARN"
```

**Observability plane cluster** — adapter only:

```bash
export EKS_CLUSTER_NAME=<your-obs-plane-cluster-name>

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account tracing-adapter-aws-xray \
  --role-arn "$ADAPTER_ROLE_ARN"
```

**Each data-plane cluster** — OpenTelemetry collector only. Repeat for each cluster:

```bash
export EKS_CLUSTER_NAME=<your-data-plane-cluster-name>

aws eks create-pod-identity-association \
  --cluster-name "$EKS_CLUSTER_NAME" \
  --namespace "$NS" \
  --service-account opentelemetry-collector \
  --role-arn "$COLLECTOR_ROLE_ARN"
```

### Step 5 — Restart workloads on each cluster

EKS Pod Identity injects credentials only at pod creation time.

So, you will see errors such as:

- `AccessDeniedException` from `assumed-role/<node-role>` in OpenTelemetry collector logs.
- `NoCredentialProviders` or `failed to retrieve credentials` in adapter logs.

Recreate the workloads so new pods receive Pod Identity credentials. Run the commands that match your topology on each cluster.

**Single-cluster topology:**

```bash
kubectl -n "$NS" rollout restart deployment/tracing-adapter-aws-xray
kubectl -n "$NS" rollout restart deployment/opentelemetry-collector
```

**Observability plane cluster** — restart only the adapter:

```bash
kubectl -n "$NS" rollout restart deployment/tracing-adapter-aws-xray
```

**Each data-plane cluster** — restart only the OpenTelemetry collector:

```bash
kubectl -n "$NS" rollout restart deployment/opentelemetry-collector
```

#### Verify Pod Identity injection

Verify that Pod Identity was injected by checking a pod that runs in your topology.

On clusters that run the **adapter** (single-cluster or observability plane):

```bash
kubectl -n "$NS" get pod -l app=tracing-adapter-aws-xray -o name | head -1 \
  | xargs -I {} kubectl -n "$NS" get {} -o yaml \
  | grep -E "AWS_CONTAINER|eks-pod-identity-token"
```

On clusters that run the **OpenTelemetry collector** (single-cluster or data-plane):

```bash
kubectl -n "$NS" get pod -l app.kubernetes.io/name=opentelemetry-collector -o name | head -1 \
  | xargs -I {} kubectl -n "$NS" get {} -o yaml \
  | grep -E "AWS_CONTAINER|eks-pod-identity-token"
```

If these values are missing, check that the namespace and ServiceAccount names in the Pod Identity associations exactly match the table above.

## Installation on non-EKS clusters with static credentials

Use this path for:

- k3d
- kind
- Kubernetes clusters outside AWS
- Kubernetes clusters where Pod Identity or IRSA is not available

In this mode, the chart creates a Kubernetes Secret containing AWS credentials.
The adapter reads this Secret automatically. The OpenTelemetry collector must also be
pointed at the same Secret through `opentelemetry-collector.extraEnvsFrom`.

### Step 1 — Export shared values

```bash
export AWS_REGION=<your-aws-region>
export NS=openchoreo-observability-plane
export AWS_ACCESS_KEY_ID=<your-access-key-id>
export AWS_SECRET_ACCESS_KEY=<your-secret-access-key>
```

### Step 2 — Create an IAM user

Create an IAM user and attach the custom
[combined IAM policy](#combined-static-credentials-iam-policy).

Create access keys for this IAM user and export them as shown above.

To create the IAM user and attach policies using the AWS CLI:

```bash
export AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

# Create the IAM user
aws iam create-user --user-name OpenChoreoXRayTracingUser

# Create the combined policy
aws iam create-policy \
  --policy-name OpenChoreoXRayTracingCombinedPolicy \
  --policy-document "$(cat <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Startup",
      "Effect": "Allow",
      "Action": "sts:GetCallerIdentity",
      "Resource": "*"
    },
    {
      "Sid": "XRayReadAndWrite",
      "Effect": "Allow",
      "Action": [
        "xray:GetTraceSummaries",
        "xray:BatchGetTraces",
        "xray:GetTraceGraph",
        "xray:PutTraceSegments",
        "xray:PutTelemetryRecords",
        "xray:GetSamplingRules",
        "xray:GetSamplingTargets",
        "xray:GetSamplingStatisticSummaries"
      ],
      "Resource": "*"
    }
  ]
}
EOF
)"

aws iam attach-user-policy \
  --user-name OpenChoreoXRayTracingUser \
  --policy-arn "arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OpenChoreoXRayTracingCombinedPolicy"

# Create access keys
ACCESS_KEY_OUTPUT=$(aws iam create-access-key --user-name OpenChoreoXRayTracingUser)
export AWS_ACCESS_KEY_ID=$(echo "$ACCESS_KEY_OUTPUT" | jq -r '.AccessKey.AccessKeyId')
export AWS_SECRET_ACCESS_KEY=$(echo "$ACCESS_KEY_OUTPUT" | jq -r '.AccessKey.SecretAccessKey')
```

### Step 3 — Install the module

Use the command that matches the cluster's topology.

#### Single-cluster install

Deploy the adapter and OpenTelemetry collector in one cluster:

```bash
helm upgrade --install observability-tracing-aws-xray \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-aws-xray \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=tracing-aws-xray-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set "opentelemetry-collector.extraEnvsFrom[0].configMapRef.name=tracing-aws-xray-collector-env" \
  --set "opentelemetry-collector.extraEnvsFrom[1].secretRef.name=tracing-aws-xray-aws-credentials"
```

#### Observability plane install

Deploy only the adapter in the observability plane cluster:

```bash
helm upgrade --install observability-tracing-aws-xray \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-aws-xray \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=tracing-aws-xray-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set opentelemetry-collector.enabled=false
```

#### Data-plane install

Deploy only the OpenTelemetry collector in each workload cluster:

```bash
helm upgrade --install observability-tracing-aws-xray \
  oci://ghcr.io/openchoreo/helm-charts/observability-tracing-aws-xray \
  --create-namespace \
  --namespace "$NS" \
  --version 0.2.0 \
  --set region="$AWS_REGION" \
  --set awsCredentials.create=true \
  --set awsCredentials.name=tracing-aws-xray-aws-credentials \
  --set awsCredentials.accessKeyId="$AWS_ACCESS_KEY_ID" \
  --set awsCredentials.secretAccessKey="$AWS_SECRET_ACCESS_KEY" \
  --set "opentelemetry-collector.extraEnvsFrom[0].configMapRef.name=tracing-aws-xray-collector-env" \
  --set "opentelemetry-collector.extraEnvsFrom[1].secretRef.name=tracing-aws-xray-aws-credentials" \
  --set adapter.enabled=false
```

In an observability-plane-only install, the collector is disabled, so the created Secret is used only by the adapter.

## Trace ingestion and querying

Deploy the [URL Shortener sample](https://github.com/openchoreo/openchoreo/tree/main/samples/from-image/url-shortener)
to generate distributed traces across multiple services. The sample includes a
frontend, API service, analytics service, PostgreSQL, and Redis — all
instrumented with OpenTelemetry.

Once the pods are running, send a few requests to generate traces. After about 30 seconds, traces should appear in the AWS X-Ray console and OpenChoreo UI.

## Troubleshooting

### Start with these logs

```bash
kubectl -n "$NS" logs deployment/tracing-adapter-aws-xray --tail=200
kubectl -n "$NS" logs deployment/opentelemetry-collector --tail=200
```

### Common issues

| Symptom | Likely cause | What to check |
| --- | --- | --- |
| Adapter pod does not start | Missing or invalid AWS credentials | Check Pod Identity association or static Secret values. |
| Collector shows `AccessDeniedException` | Pod is using the node IAM role instead of Pod Identity role | Restart the collector after creating Pod Identity associations. |
| Query returns `traces: []` | Traces not exported to X-Ray, or labels are missing | Check collector logs and verify that OpenChoreo labels are present on workload pods. |
| X-Ray shows traces but adapter returns empty | Indexed attributes not searchable yet | Wait up to 30 seconds for X-Ray indexing, then retry the adapter query. |
| Pod Identity not injected | Pod Identity Agent not running or association mismatch | Verify the `eks-pod-identity-agent` DaemonSet is running and the namespace/ServiceAccount in the association match the installed chart. |

## Configuration reference

| Value | Default | Description |
| --- | --- | --- |
| `region` | Required | AWS region for X-Ray API calls. |
| `awsCredentials.create` | `false` | Creates a static AWS credentials Secret. Keep `false` for Pod Identity, IRSA, or instance-profile based auth. Set to `true` for k3d, kind, or non-EKS clusters. |
| `awsCredentials.name` | `""` | Name of the AWS credentials Secret. Required when `awsCredentials.create=true`. |
| `awsCredentials.accessKeyId` | Required if `create=true` | AWS access key ID. |
| `awsCredentials.secretAccessKey` | Required if `create=true` | AWS secret access key. |
| `opentelemetry-collector.enabled` | `true` | Enables the OpenTelemetry collector subchart. Set to `false` on the observability plane cluster in a multi-cluster topology. |
| `opentelemetry-collector.mode` | `deployment` | Runs the collector as a Deployment (push-based trace collection). |
| `opentelemetry-collector.image.repository` | `otel/opentelemetry-collector-contrib` | Collector image repository. The contrib image includes `awsxray` exporter and `k8sattributes` processor. |
| `opentelemetry-collector.image.tag` | `0.151.0` | Collector image tag. |
| `opentelemetry-collector.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `opentelemetry-collector.extraEnvsFrom` | `[{configMapRef: {name: tracing-aws-xray-collector-env}}]` | Extra `envFrom` entries for the collector. The default ConfigMap supplies `AWS_REGION`. Append the static AWS credentials Secret at index `1` on non-EKS clusters. |
| `adapter.enabled` | `true` | Deploys the X-Ray Tracing Adapter Deployment and Service. Set to `false` on data-plane clusters in a multi-cluster topology. |
| `adapter.image.repository` | `ghcr.io/openchoreo/observability-tracing-aws-xray-adapter` | Adapter image repository. |
| `adapter.image.tag` | `""` | Adapter image tag. Empty defaults to chart `appVersion`. |
| `adapter.image.pullPolicy` | `IfNotPresent` | Adapter image pull policy. |
| `adapter.service.port` | `9100` | Adapter HTTP port. |
| `adapter.serviceAccount.annotations` | `{}` | ServiceAccount annotations for IRSA or other identity integrations. |
| `adapter.logLevel` | `INFO` | Adapter log level. Supported values include `DEBUG`, `INFO`, `WARN`, and `ERROR`. |
| `adapter.resources.limits.cpu` | `200m` | CPU limit for the adapter. |
| `adapter.resources.limits.memory` | `256Mi` | Memory limit for the adapter. |
| `adapter.resources.requests.cpu` | `50m` | CPU request for the adapter. |
| `adapter.resources.requests.memory` | `128Mi` | Memory request for the adapter. |
| `tracingCollectorCustomizations.tailSampling.enabled` | `true` | Enables the `tail_sampling` processor for rate limiting. |
| `tracingCollectorCustomizations.tailSampling.decisionWait` | `10s` | Time to wait before making a sampling decision. |
| `tracingCollectorCustomizations.tailSampling.numTraces` | `100` | Maximum number of traces to keep in memory during the decision wait. |
| `tracingCollectorCustomizations.tailSampling.expectedNewTracesPerSec` | `10` | Expected rate of new traces per second. |
| `tracingCollectorCustomizations.tailSampling.decisionCache.sampledCacheSize` | `10000` | Size of the sampled decisions cache. |
| `tracingCollectorCustomizations.tailSampling.decisionCache.nonSampledCacheSize` | `1000` | Size of the non-sampled decisions cache. |
| `tracingCollectorCustomizations.tailSampling.spansPerSecond` | `10` | Maximum spans per second for the rate limiting policy. |

Unlike the logs and metrics CloudWatch modules, this tracing module does not
expose a retention value. AWS X-Ray trace retention is service-managed and fixed
at 30 days; it is not backed by a customer-managed CloudWatch Logs log group
with a configurable retention policy.

## Dependencies

Bundled upstream Helm charts:

| Chart | Repository |
| ----- | ---------- |
| opentelemetry-collector | https://open-telemetry.github.io/opentelemetry-helm-charts |

## Compatibility

> **Note:** The Helm chart versions specified in the installation commands above are for the latest module version compatible with the development version of OpenChoreo. Refer to the compatibility table below to determine the appropriate module version for your OpenChoreo installation.

| Module Version | OpenChoreo Version |
|----------------|--------------------|
| v0.2.x         | v1.1.x             |
