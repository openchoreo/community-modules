# PostgreSQL (advanced)

The `postgres-crossplane-advanced` ClusterResourceType gives developers a PostgreSQL database that
can be maintained once it is provisioned: scaled, upgraded, tuned and opened to more networks. It
creates an `AdvancedPostgresInstance` in the cell namespace, and the backing installed on that data
plane builds it.

It sits next to [`postgres-crossplane`](../postgres/README.md), which it leaves unchanged. The
outputs are the same, so a Workload's `envBindings` work with either. Use `postgres-crossplane` when
a per-environment `size` is all you need, and this one to maintain the database once it is
provisioned. The two are separate kinds: changing a Resource from one to the other creates a new
database and does not move the data.

For installing the module, see the [module README](../../README.md).

## Backings

| Backing | Builds an `AdvancedPostgresInstance` as | Guide |
| :------ | :-------------------------------------- | :---- |
| Azure | Azure Database for PostgreSQL Flexible Server | [azure/README.md](../../azure/README.md#postgresql-advanced) |

AWS and GCP have no Composition for it yet. See [Implementing a backing](#implementing-a-backing).

## Maintaining a provisioned database

A change is made in one of two places:

- **On the Resource**, the same in every environment. A change cuts a new ResourceRelease, and each
  environment moves to it when its binding is promoted, so a version upgrade reaches development
  before production.
- **On the ResourceReleaseBinding**, under `resourceTypeEnvironmentConfigs`. A change applies to
  that environment only, straight away, without a new release.

| Operation | Setting | Set on | Interrupts connections |
| :-------- | :------ | :----- | :--------------------- |
| Scale CPU and memory | `capacity.size` | Binding | Yes, the server restarts |
| Grow storage | `capacity.storageGb` | Binding | No |
| Grow storage when it runs low | `capacity.storageAutoGrow` | Binding | No |
| Upgrade the major version | `version` | Resource | Yes, during the upgrade |
| Choose when minor versions and patches are applied | `maintenance.window` | Binding | Only inside the window |
| Allow more networks to connect | `network.firewallRules` | Binding | No |
| Tune server parameters | `configuration` | Binding | Only `connections.maxConnections` |
| Allow extensions | `extensions` | Resource | No |

How long each one takes on Azure, as measured, is in the
[Azure guide](../../azure/README.md#postgresql-advanced).

## Parameters

Set on the Resource, the same in every environment.

| Parameter | Default | Description |
| :-------- | :------ | :---------- |
| `database` | `appdb` | Name of the database created for the application |
| `version` | `"16"` | PostgreSQL major version: `"15"`, `"16"`, `"17"` or `"18"`. Raising it upgrades the server in place. It cannot be lowered |
| `extensions` | None | Extensions the application may create, such as `vector` or `postgis`. The application still runs `CREATE EXTENSION` |

## Per-environment settings

Set on the `ResourceReleaseBinding`, under `resourceTypeEnvironmentConfigs`. The settings are
grouped by operation, and every group is optional.
[`samples/resource-release-binding.yaml`](samples/resource-release-binding.yaml) sets all of them.

```yaml
spec:
  resourceTypeEnvironmentConfigs:
    capacity:
      size: medium
      storageGb: 128
      storageAutoGrow: true
    maintenance:
      window:
        dayOfWeek: 0
        startHour: 2
    network:
      firewallRules:
        - name: office
          startIpAddress: 203.0.113.10
          endIpAddress: 203.0.113.10
    configuration:
      connections:
        maxConnections: 200
      memory:
        workMemMb: 16
        maintenanceWorkMemMb: 128
      timeouts:
        statementTimeoutSeconds: 30
        idleInTransactionTimeoutSeconds: 60
        lockTimeoutSeconds: 10
      logging:
        slowQueryThresholdMs: 500
        logStatement: ddl
        queryStats: top
```

### `capacity`: CPU, memory and storage

| Setting | Default | Description |
| :------ | :------ | :---------- |
| `size` | `small` | `small`, `medium` or `large`. Each backing maps it to its own CPU and memory, and a default storage size. Changing it restarts the server |
| `storageGb` | Follows `size` | 32, 64, 128, 256, 512, 1024, 2048 or 4096. It can grow but not shrink, and cannot be unset once set. Lowering `size` keeps the current storage |
| `storageAutoGrow` | `false` | Grow storage automatically when it runs low |

### `maintenance`: minor version and patch updates

| Setting | Default | Description |
| :------ | :------ | :---------- |
| `window` | Backing chooses | Weekly window, in UTC: `dayOfWeek` (0 is Sunday), `startHour`, `startMinute` (default 0). Minor versions cannot be chosen; the provider applies them in this window. Major versions are the `version` parameter |

### `network`: who can connect

| Setting | Default | Description |
| :------ | :------ | :---------- |
| `firewallRules` | None | IPv4 ranges allowed to connect, on top of the data plane's own rules: `name`, `startIpAddress`, `endIpAddress`. A rule that admits every address is rejected |

### `configuration`: server parameters

Each setting is one PostgreSQL parameter. Unset ones keep the default, and removing one resets it.
Only `connections.maxConnections` restarts the server.

| Setting | PostgreSQL parameter | Description |
| :------ | :------------------- | :---------- |
| `connections.maxConnections` | `max_connections` | 25 to 5000. Changing it restarts the server |
| `memory.workMemMb` | `work_mem` | Memory, in MiB, for each sort or hash in a query |
| `memory.maintenanceWorkMemMb` | `maintenance_work_mem` | Memory, in MiB, for vacuum and index builds |
| `timeouts.statementTimeoutSeconds` | `statement_timeout` | Cancel statements that run longer. 0 turns it off |
| `timeouts.idleInTransactionTimeoutSeconds` | `idle_in_transaction_session_timeout` | Close sessions left idle inside a transaction. 0 turns it off |
| `timeouts.lockTimeoutSeconds` | `lock_timeout` | Stop waiting for a lock. 0 turns it off |
| `logging.slowQueryThresholdMs` | `log_min_duration_statement` | Log statements that run at least this long. 0 logs every statement, -1 none |
| `logging.logStatement` | `log_statement` | `none`, `ddl`, `mod` or `all` |
| `logging.queryStats` | `pg_stat_statements.track` | `none`, `top` or `all` |

## Outputs

| Output | Kind | Comes from |
| :----- | :--- | :--------- |
| `host` | `value` | `AdvancedPostgresInstance` `status.address` |
| `port` | `value` | `AdvancedPostgresInstance` `status.port` |
| `database` | `value` | `AdvancedPostgresInstance` `status.database` |
| `username` | `value` | `AdvancedPostgresInstance` `status.username` |
| `password` | `secretKeyRef` | Secret `<name>-conn`, key `password` |

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
    name: postgres-crossplane-advanced
  parameters:
    database: orders
    version: "16"
    extensions: [vector, uuid-ossp]
```

Its `production` binding, scaled to `medium` with more storage, from
[`samples/resource-release-binding.yaml`](samples/resource-release-binding.yaml):

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: ResourceReleaseBinding
metadata:
  name: orders-db-production
spec:
  owner:
    projectName: shop
    resourceName: orders-db
  environment: production
  resourceTypeEnvironmentConfigs:
    capacity:
      size: medium
      storageGb: 128
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

A Composition for `AdvancedPostgresInstance` ([`definition.yaml`](definition.yaml)) must, once the
database is reachable:

- set `status.address`, `status.port`, `status.database` and `status.username`
- write a Secret `<name>-conn` in the same namespace with the password under the key `password`
- map `spec.capacity.size` to the platform's CPU, memory and storage, and never ask for less storage
  than the database has, so lowering `size` keeps the current storage
- apply the other settings, or say in its backing's guide which ones it ignores

The Azure Composition,
[`azure/pg-advanced-composition.yaml`](../../azure/pg-advanced-composition.yaml), is the
working example.
