# PostgreSQL

The `postgres-crossplane` ClusterResourceType gives developers a PostgreSQL database. It creates a
`PostgresInstance` in the cell namespace, and the backing installed on that data plane builds it.

For installing the module, see the [module README](../../README.md).

## Backings

| Backing | Builds a `PostgresInstance` as | Guide |
| :------ | :----------------------------- | :---- |
| Azure | Azure Database for PostgreSQL Flexible Server | [azure/README.md](../../azure/README.md#postgresql) |
| AWS | Amazon RDS for PostgreSQL | [aws/README.md](../../aws/README.md#postgresql) |
| GCP | Cloud SQL for PostgreSQL | [gcp/README.md](../../gcp/README.md#postgresql) |

## Parameters

Set on the Resource, the same in every environment.

| Parameter | Default | Description |
| :-------- | :------ | :---------- |
| `database` | `appdb` | Name of the database created for the application |
| `version` | `"16"` | PostgreSQL major version: `"15"`, `"16"` or `"17"` |

## Per-environment settings

Set on the `ResourceReleaseBinding`, under `resourceTypeEnvironmentConfigs`.

| Setting | Default | Description |
| :------ | :------ | :---------- |
| `size` | `small` | `small`, `medium` or `large`. Each backing maps it to its own compute and storage |

```yaml
spec:
  resourceTypeEnvironmentConfigs:
    size: medium
```

## Outputs

| Output | Kind | Comes from |
| :----- | :--- | :--------- |
| `host` | `value` | `PostgresInstance` `status.address` |
| `port` | `value` | `PostgresInstance` `status.port` |
| `database` | `value` | `PostgresInstance` `status.database` |
| `username` | `value` | `PostgresInstance` `status.username` |
| `password` | `secretKeyRef` | Secret `<PostgresInstance name>-conn`, key `password` |

## Example

A Resource, from [`samples/resource.yaml`](samples/resource.yaml):

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Resource
metadata:
  name: orders-db
spec:
  owner:
    projectName: shop
  type:
    kind: ClusterResourceType
    name: postgres-crossplane
  parameters:
    database: orders
    version: "16"
```

A Workload that uses it, from [`samples/workload.yaml`](samples/workload.yaml):

```yaml
spec:
  dependencies:
    resources:
      - ref: orders-db
        envBindings:
          host: DB_HOST
          port: DB_PORT
          database: DB_NAME
          username: DB_USER
          password: DB_PASSWORD
```

## Implementing a backing

A Composition for `PostgresInstance` ([`definition.yaml`](definition.yaml)) must, once the database
is reachable:

- set `status.address`, `status.port`, `status.database` and `status.username`
- write a Secret named `<PostgresInstance name>-conn` in the same namespace, with the password under
  the key `password`
- map `spec.size` to the platform's compute and storage

The Azure Composition,
[`azure/postgres-composition.yaml`](../../azure/postgres-composition.yaml), the AWS one,
[`aws/postgres-composition.yaml`](../../aws/postgres-composition.yaml), and the GCP one,
[`gcp/postgres-composition.yaml`](../../gcp/postgres-composition.yaml), are working examples. The
AWS one has the provider generate the password straight into that Secret. The GCP one generates the
password itself, for a provider that cannot, and keeps it in that Secret.
