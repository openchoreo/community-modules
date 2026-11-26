# AWS Backing

This backing builds the module's resources on AWS, using the Crossplane providers from the
[AWS provider family](https://github.com/crossplane-contrib/provider-upjet-aws). The providers
authenticate with EKS Pod Identity, so no cloud credential is stored in the cluster. Databases and
caches are created in the data plane's VPC, in its private subnets, with no public endpoint.

For how the module fits together, see the [module README](../README.md).

| Resource | Built as | Provider | Composition |
| :------- | :------- | :------- | :---------- |
| PostgreSQL | Amazon RDS for PostgreSQL | `provider-aws-rds` | [`postgres-composition.yaml`](postgres-composition.yaml) |
| PostgreSQL (advanced) | Amazon RDS for PostgreSQL | `provider-aws-rds`, `provider-aws-ec2` | [`pg-advanced-composition.yaml`](pg-advanced-composition.yaml) |
| Redis | Amazon ElastiCache for Valkey | `provider-aws-elasticache` | [`redis-composition.yaml`](redis-composition.yaml) |

| File | What it is |
| :--- | :--------- |
| [`provider.yaml`](provider.yaml) | The providers, each with a fixed ServiceAccount name for Pod Identity |
| [`provider-config.yaml`](provider-config.yaml) | Tells the providers to use Pod Identity |
| [`environment-config.yaml`](environment-config.yaml) | This data plane's AWS settings, read by every Composition |
| `<resource>-composition.yaml` | One Composition per resource |
| `render/<resource>/` | Sample inputs for checking a Composition without a cluster |

## How the providers authenticate

Each provider runs as its own pod, under a ServiceAccount with a fixed name. A Pod Identity
association in EKS links that ServiceAccount to an IAM role. When the pod starts, EKS gives it a
token and the address of the Pod Identity agent on its node. The agent exchanges the token with EKS
for short-lived credentials for the role, and the role's policies decide what the provider can do.

The role's trust policy names only the EKS Pod Identity service, not a cluster or ServiceAccount, so
the association alone decides which pods get it.

```mermaid
flowchart TB
  subgraph eks["EKS data plane"]
    pod["Provider pod<br/>fixed ServiceAccount"]
    agent["Pod Identity agent<br/>on each node"]
  end
  subgraph aws["AWS"]
    auth["EKS Auth API"]
    assoc["Pod Identity association<br/>one per provider"]
    role["IAM role"]
  end
  svc[("RDS, EC2 or ElastiCache")]

  pod -->|presents its token| agent
  agent --> auth
  assoc -->|names the role for the ServiceAccount| auth
  auth -->|short-lived credentials for| role
  role -->|policy| svc
```

## Prerequisites

- Steps 1 to 4 of the [module install](../README.md#install) done on this data plane
- The OpenChoreo data plane running on EKS, with the EKS Pod Identity Agent add-on
  (`eks-pod-identity-agent`)
- Permission to create IAM roles and CloudFormation stacks (eksctl creates each role through a
  stack), Pod Identity associations, a security group and subnet groups
- The AWS CLI and [eksctl](https://eksctl.io)

## Install

Run these from the module's root directory, against the cluster running the OpenChoreo data plane.

```bash
CLUSTER=<EKS cluster name>
REGION=<region of the cluster>
```

### 1. Create an IAM role for each provider

Create one role per provider, with a Pod Identity association for the provider's ServiceAccount, as
described in [How the providers authenticate](#how-the-providers-authenticate). eksctl creates both
in one step:

```bash
cat > pod-identity.yaml <<EOF
apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig
metadata:
  name: $CLUSTER
  region: $REGION
iam:
  podIdentityAssociations:
    # Crossplane creates the ServiceAccounts, so eksctl must not.
    - namespace: crossplane-system
      serviceAccountName: provider-aws-rds
      createServiceAccount: false
      permissionPolicyARNs:
        - arn:aws:iam::aws:policy/AmazonRDSFullAccess
    # PostgreSQL (advanced) only: a security group for each database
    - namespace: crossplane-system
      serviceAccountName: provider-aws-ec2
      createServiceAccount: false
      permissionPolicy:
        Version: "2012-10-17"
        Statement:
          - Effect: Allow
            Action:
              - ec2:CreateSecurityGroup
              - ec2:DeleteSecurityGroup
              - ec2:DescribeSecurityGroups
              - ec2:DescribeSecurityGroupRules
              - ec2:AuthorizeSecurityGroupIngress
              - ec2:RevokeSecurityGroupIngress
              - ec2:AuthorizeSecurityGroupEgress
              - ec2:RevokeSecurityGroupEgress
              - ec2:ModifySecurityGroupRules
              - ec2:UpdateSecurityGroupRuleDescriptionsIngress
              - ec2:CreateTags
              - ec2:DeleteTags
              - ec2:DescribeTags
              - ec2:DescribeVpcs
              - ec2:DescribeNetworkInterfaces
            Resource: "*"
            Condition:
              StringEquals:
                aws:RequestedRegion: $REGION
    - namespace: crossplane-system
      serviceAccountName: provider-aws-elasticache
      createServiceAccount: false
      permissionPolicyARNs:
        - arn:aws:iam::aws:policy/AmazonElastiCacheFullAccess
EOF

eksctl create podidentityassociation -f pod-identity.yaml
```

Leave out the providers for resources this data plane does not offer. Keep `pod-identity.yaml` for
the [uninstall](#uninstall).

The AWS managed policies cover every region of the account, and grant more than the providers use.
`AmazonRDSFullAccess` allows every RDS action on every RDS resource, and also reading any CloudWatch
Logs log events, publishing to any SNS topic, and managing CloudWatch alarms and Application Auto
Scaling. `AmazonElastiCacheFullAccess` allows every ElastiCache action, and creating the VPC
endpoints that ElastiCache Serverless uses. Use an account that holds only resources this backing
creates, or replace them with narrower policies. The EC2 provider's inline policy is already narrow:
it manages security groups and their rules in the data plane's region, and nothing else in EC2.

For each entry, eksctl creates a CloudFormation stack with an IAM role whose trust policy allows the
service `pods.eks.amazonaws.com` the actions `sts:AssumeRole` and `sts:TagSession`, attaches the
policy to it, and creates the association. Other tooling can create the same.

### 2. Install the providers

Create the associations before the providers. EKS gives credentials only to pods started after an
association exists. A provider pod started earlier has none, and one that was running when its
association or role was recreated keeps the credentials it cached for the old role, which AWS
rejects with `InvalidClientTokenId`. In either case, delete the provider pod so it starts again.

```bash
kubectl apply -f aws/provider.yaml
kubectl wait providers.pkg.crossplane.io --all --for=condition=Healthy --timeout=10m
```

Installing a provider from the family also installs `provider-family-aws`, which owns the
ProviderConfig types and needs no AWS access. Each provider's ServiceAccount name is fixed in its
`DeploymentRuntimeConfig`, so the association from step 1 keeps matching across provider upgrades.

Check that EKS gave each provider pod its credentials:

```bash
for POD in $(kubectl get pods -n crossplane-system -o name | grep -E 'provider-aws-(rds|ec2|elasticache)'); do
  echo "$POD: $(kubectl get -n crossplane-system "$POD" -o jsonpath='{.spec.containers[0].env[*].name}')"
done
```

The output should include `AWS_CONTAINER_CREDENTIALS_FULL_URI` and
`AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`. If it does not, check the association's namespace and
ServiceAccount name, then delete the provider pod so it starts again. Step 4 ends with a check that
the credentials work.

### 3. Configure the provider credentials

Apply [`provider-config.yaml`](provider-config.yaml). It has nothing to edit: each provider acts as
the role its association names.

```bash
kubectl apply -f aws/provider-config.yaml
```

AWS resources in any namespace use the `ClusterProviderConfig` named `default` unless they say
otherwise, so this one object covers every cell namespace. It also means that anyone who can create
AWS managed resources (`*.aws.m.upbound.io`) in any namespace on the data plane acts with the
providers' IAM roles. Keep that permission to platform engineers.

### 4. Create the networking

Databases and caches go into the data plane's VPC, in its private subnets. Create, once per data
plane, a security group that admits the cluster's pods, and a subnet group for each service:

```bash
VPC_ID=$(aws eks describe-cluster --name "$CLUSTER" --region "$REGION" \
  --query cluster.resourcesVpcConfig.vpcId --output text)
CLUSTER_SG=$(aws eks describe-cluster --name "$CLUSTER" --region "$REGION" \
  --query cluster.resourcesVpcConfig.clusterSecurityGroupId --output text)

# Pick the VPC's private subnets, in at least two availability zones
aws ec2 describe-subnets --region "$REGION" --filters "Name=vpc-id,Values=$VPC_ID" \
  --query 'Subnets[].[SubnetId,AvailabilityZone,MapPublicIpOnLaunch]' --output table
PRIVATE_SUBNETS=(<subnet-id> <subnet-id>)

SG=$(aws ec2 create-security-group --region "$REGION" --vpc-id "$VPC_ID" \
  --group-name openchoreo-crossplane \
  --description "RDS and ElastiCache created by Crossplane for OpenChoreo" \
  --query GroupId --output text)
aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG" --ip-permissions \
  "IpProtocol=tcp,FromPort=5432,ToPort=5432,UserIdGroupPairs=[{GroupId=$CLUSTER_SG}]" \
  "IpProtocol=tcp,FromPort=6379,ToPort=6379,UserIdGroupPairs=[{GroupId=$CLUSTER_SG}]" \
  --query 'SecurityGroupRules[].SecurityGroupRuleId' --output text

aws rds create-db-subnet-group --region "$REGION" \
  --db-subnet-group-name openchoreo-crossplane \
  --db-subnet-group-description "Private subnets for RDS created by Crossplane for OpenChoreo" \
  --subnet-ids "${PRIVATE_SUBNETS[@]}" --query DBSubnetGroup.SubnetGroupStatus --output text
aws elasticache create-cache-subnet-group --region "$REGION" \
  --cache-subnet-group-name openchoreo-crossplane \
  --cache-subnet-group-description "Private subnets for ElastiCache created by Crossplane for OpenChoreo" \
  --subnet-ids "${PRIVATE_SUBNETS[@]}" --query CacheSubnetGroup.CacheSubnetGroupName --output text

echo "$VPC_ID $SG"
```

The rules admit the EKS cluster security group. In an EKS managed node group without security groups
of its own, every node carries that group, and with the VPC CNI's default setup pod traffic carries
the node's groups. If the cluster uses security groups for pods, or nodes with other security
groups, admit those instead.

The security group's rules reference the cluster security group. Delete it before deleting the EKS
cluster, or the cluster's security group, and with it the VPC, cannot be deleted.

Check that the providers' credentials work, without creating anything, by observing the two subnet
groups. `managementPolicies: ["Observe"]` only reads; deleting the objects leaves AWS untouched.

```bash
kubectl apply -f - <<EOF
apiVersion: rds.aws.m.upbound.io/v1beta1
kind: SubnetGroup
metadata:
  name: credentials-check
  namespace: default
  annotations:
    crossplane.io/external-name: openchoreo-crossplane
spec:
  managementPolicies: ["Observe"]
  forProvider:
    region: $REGION
---
apiVersion: elasticache.aws.m.upbound.io/v1beta1
kind: SubnetGroup
metadata:
  name: credentials-check
  namespace: default
  annotations:
    crossplane.io/external-name: openchoreo-crossplane
spec:
  managementPolicies: ["Observe"]
  forProvider:
    region: $REGION
EOF
kubectl wait -n default subnetgroups.rds.aws.m.upbound.io/credentials-check \
  subnetgroups.elasticache.aws.m.upbound.io/credentials-check --for=condition=Ready --timeout=2m
kubectl delete -n default subnetgroups.rds.aws.m.upbound.io/credentials-check \
  subnetgroups.elasticache.aws.m.upbound.io/credentials-check
```

If they stay not ready, `kubectl describe` on them shows the error from AWS.

### 5. Configure this data plane's settings

Edit [`environment-config.yaml`](environment-config.yaml) with the region, the VPC, the security
group and the subnet groups from step 4 (see [Settings](#settings)), then apply it.

```bash
kubectl apply -f aws/environment-config.yaml
```

### 6. Install the Compositions

Apply the Composition for each resource this data plane should offer:

```bash
kubectl apply -f aws/postgres-composition.yaml
kubectl apply -f aws/pg-advanced-composition.yaml
kubectl apply -f aws/redis-composition.yaml
```

Then return to the [module install](../README.md#6-add-the-resourcetypes) to add the ResourceTypes.

## Settings

Every AWS Composition reads the `aws` EnvironmentConfig on its data plane. These settings are shared
by all of them. Each resource's settings are under its own key, described in that resource's section
below.

| Field | Purpose |
| :---- | :------ |
| `region` | AWS region for new resources: the region of the data plane's VPC |
| `vpcId` | The data plane's VPC. Only the [PostgreSQL (advanced)](#postgresql-advanced) Composition reads it, to create a security group for each database |
| `namePrefix` | Prepended to the name of every AWS resource the Compositions create. Lowercase letters, digits and hyphens, starting with a letter |
| `securityGroupIds` | Security groups attached to every database and cache. Together they must admit the data plane's pods on each resource's port |
| `tags` | Optional. Tags added to every AWS resource the Compositions create. Values are written as strings |

The provider also tags each resource with `crossplane-kind`, `crossplane-name` and
`crossplane-providerconfig`, which lead from the AWS console back to the managed resource.

> [!WARNING]
> The provider finds an ElastiCache replication group by its ID. If a new cache's ID matches one
> Crossplane did not create, it takes that cache over, and deleting the Resource deletes it. Keep
> `namePrefix` distinct from anything else in the account and region.
>
> The Compositions only reference the subnet groups (by name) and the security groups (by ID) and
> never manage them, so deleting a Resource never touches the networking.

## Checking a Composition

`crossplane composition render` runs a Composition's functions locally, in Docker, and prints what
Crossplane would create, without a cluster. Each resource has sample inputs under
`render/<resource>/`: a composite resource, and the AWS resource as it looks once AWS has finished.
Run the commands in each resource's section from the module's root directory.

With Colima, point the CLI at its socket first:
`export DOCKER_HOST=unix://$HOME/.colima/default/docker.sock`.

## PostgreSQL

[`postgres-composition.yaml`](postgres-composition.yaml) builds a `PostgresInstance` as an Amazon
RDS for PostgreSQL instance.

### What it creates

For each `PostgresInstance`, in the same namespace:

| Resource | Purpose |
| :------- | :------ |
| `Instance` | The database instance, with the application database created with it. Encrypted gp3 storage, not publicly accessible. The provider generates the master password into the Secret `<name>-conn`, under `password`, and deletes the Secret with the instance. The Secret holds nothing else |

### Outputs

| [Output](../apis/postgres/README.md#outputs) | Filled from |
| :----- | :---------- |
| `host` | The instance's endpoint address |
| `port` | `5432` |
| `database` | The database it created |
| `username` | `postgres.username` |
| `password` | The Secret `<name>-conn` |

### Settings

Under `postgres` in the `aws` EnvironmentConfig:

| Field | Purpose |
| :---- | :------ |
| `subnetGroupName` | Existing DB subnet group over the VPC's private subnets |
| `username` | Master user for new instances. RDS reserves some names, such as `rdsadmin` |

Instance identifiers are unique per account and region, limited to 63 letters, digits and hyphens,
with no two hyphens in a row. Each identifier is `namePrefix`, the `PostgresInstance` name and a
short hash of its namespace, so Resources with the same name in different OpenChoreo namespaces or
projects get different instances. Identifiers that would be too long, or that had hyphens to
collapse, are shortened, with a hash of the full name appended. An instance keeps its identifier
once created.

On RDS the `database` parameter must start with a letter.

### Sizes

| `size` | Instance class | Storage |
| :----- | :------------- | :------ |
| `small` | `db.t4g.small` | 32 GiB |
| `medium` | `db.m7g.large` | 64 GiB |
| `large` | `db.m7g.xlarge` | 128 GiB |

Every size runs in a single availability zone. Only `small` has been tested against AWS; `medium`
and `large` have been checked against the provider's schema.

The Composition sets the major version only, and RDS picks its default minor version (16.13 for
`"16"` at the time of testing).

Changing an existing database's size or version has not been tested. RDS cannot reduce an instance's
allocated storage, so a smaller size does not apply to an existing database, and a new major version
needs `allowMajorVersionUpgrade`, which the Composition does not set.

> [!WARNING]
> Deleting the binding deletes the database and its automated backups, with no final snapshot. Take
> a snapshot first if the data matters.

### Networking

The instance has no public endpoint. It accepts connections only from sources the security groups
in `securityGroupIds` admit. Connections use TLS; clients should connect with `sslmode=require`.

### What to expect

- **A new database takes about eleven minutes to reach the Workload.** In testing (`small`,
  us-east-1), AWS had the instance available, and the `PostgresInstance` Ready, after about five
  and a half minutes. The binding followed about five minutes later, and a dependent component
  connected straight after.
- **Deletion is quick.** AWS removed the instance under two minutes after the binding was deleted,
  and `<name>-conn` went with it. No snapshot or backup was left behind.

### Checking the Composition

```bash
# First reconcile: nothing exists in AWS yet
crossplane composition render aws/render/postgres/xr.yaml aws/postgres-composition.yaml functions.yaml \
  --xrd apis/postgres/definition.yaml \
  --required-resources aws/environment-config.yaml

# A later reconcile: the PostgresInstance gets its address and becomes Ready
crossplane composition render aws/render/postgres/xr.yaml aws/postgres-composition.yaml functions.yaml \
  --xrd apis/postgres/definition.yaml \
  --required-resources aws/environment-config.yaml \
  --observed-resources aws/render/postgres/observed.yaml
```

## PostgreSQL (advanced)

[`pg-advanced-composition.yaml`](pg-advanced-composition.yaml) builds an `AdvancedPostgresInstance`
as an Amazon RDS for PostgreSQL instance, and applies the
[operations that maintain it](../apis/pg-advanced/README.md#maintaining-a-provisioned-database). It
is separate from the [PostgreSQL](#postgresql) Composition, which it leaves unchanged. Besides
`provider-aws-rds`, it needs `provider-aws-ec2`, for the security groups.

### What it creates

For each `AdvancedPostgresInstance`, in the same namespace:

| Resource | Purpose |
| :------- | :------ |
| `Instance` | The database instance, with the application database created with it: instance class and storage from `capacity`, maintenance window from `maintenance`. Encrypted gp3 storage, not publicly accessible. The provider generates the master password into the Secret `<name>-conn`, under `password`, as for the [PostgreSQL](#what-it-creates) Composition |
| `ParameterGroup` | Server parameters from `configuration`, and `rds.allowed_extensions` from the `extensions` parameter. One per major version, named `<identifier>-pg<version>`, since a parameter group serves one version |
| `SecurityGroup` | Attached to the instance next to the data plane's `securityGroupIds`, holding this environment's rules only. Named after the instance |
| `SecurityGroupIngressRule` | One per distinct CIDR block in `network.firewallRules`, on port 5432, named after the block |

The instance is created once its parameter group and security group exist, so it starts with both.

### Outputs

The same as for the [PostgreSQL](#outputs) Composition: `host` is the instance's endpoint address.

### Settings

It reads the same `postgres` settings as the [PostgreSQL](#postgresql) Composition, so both can run
on one data plane with one EnvironmentConfig. It also reads `vpcId`, for the security groups, and
two settings of its own under `postgres`:

| Field | Purpose |
| :---- | :------ |
| `sizes` | Optional. Overrides the instance class (`instanceClass`) or default storage (`storageGb`) behind a size on this data plane. Fields left out keep the defaults below |
| `storageAutoGrowLimitGb` | Optional. The most storage, in GiB, that `capacity.storageAutoGrow` may grow a database to. Default 1024. It may not exceed the instance class's maximum: 6144 for `db.t4g.micro`, 16384 for `db.t4g.small`, 32768 for `db.t4g.medium`, 65536 for most larger classes |

For example, to give `medium` more memory:

```yaml
postgres:
  sizes:
    medium:
      instanceClass: db.r7g.large
```

Instances are named the same way as for the PostgreSQL Composition.

### Sizes

| `capacity.size` | Instance class | vCPUs | Memory | Default storage |
| :-------------- | :------------- | :---- | :----- | :-------------- |
| `small` | `db.t4g.small` | 2 | 2 GiB | 32 GiB |
| `medium` | `db.m7g.large` | 2 | 8 GiB | 64 GiB |
| `large` | `db.m7g.xlarge` | 4 | 16 GiB | 128 GiB |

`capacity.storageGb` overrides the default storage. Every size runs in a single availability zone,
with automated backups off, so RDS takes no snapshot before a major version upgrade either.

### How each setting is applied

| Setting | On RDS |
| :------ | :----- |
| `version` | The instance's major version. RDS picks its default minor version and applies minor updates in the maintenance window. Raising it upgrades the instance in place, moving it to a parameter group for the new version in the same change. The Composition creates that group first, and deletes the old one once the instance has left it |
| `extensions` | The `rds.allowed_extensions` parameter. `CREATE EXTENSION` for an extension not on the list fails. Unset or empty, RDS allows every extension it supports |
| `capacity.size` | The instance class |
| `capacity.storageGb` | The allocated gp3 storage. The Composition never asks for less than the instance has |
| `capacity.storageAutoGrow` | Storage autoscaling, up to `postgres.storageAutoGrowLimitGb`, or 1024 GiB. A `storageGb` above the limit raises it, since RDS wants it more than 10% above the storage. It also stays more than 10% above storage the instance keeps from a larger size, so lowering `size` is not refused and does not stop autoscaling |
| `maintenance.window` | The weekly maintenance window, one hour long. RDS refuses a backup window that overlaps it, so the Composition also sets the daily backup window: half an hour, starting an hour after the maintenance window ends. Removing `window` keeps both windows as they are |
| `network.firewallRules` | Ingress rules in the instance's own security group. An ingress rule takes one CIDR block, so a range that is not one block becomes the fewest blocks that cover it exactly. Each block becomes one ingress rule, described with the names of the rules that cover it; a block several rules cover is created once, since AWS refuses a duplicate. A range that starts after it ends stops the Composition with an error naming the rule. A security group holds 60 inbound rules unless the account's quota is raised |
| `configuration` | One parameter per setting in the instance's parameter group, named after its PostgreSQL parameter, except `timeouts.lockTimeoutSeconds`: `lock_timeout` is not an RDS parameter, so it is not used. Set it per database instead, with `ALTER DATABASE <database> SET lock_timeout = '10s'` |

### Networking

As for the [PostgreSQL](#networking) Composition, the instance has no public endpoint, and Workloads
on the data plane connect through the security groups in `securityGroupIds`. `network.firewallRules`
adds rules to the instance's own security group, apart from the data plane's. Since the instance has
no public endpoint, a rule only helps addresses that reach the VPC, such as other subnets, peered
VPCs, or networks connected by VPN or Direct Connect. An office on the internet cannot connect.

Connections must be encrypted: RDS requires TLS by default from PostgreSQL 15, the oldest
`version` allowed, and refused a client with `sslmode=disable` in testing.

### Maintaining a provisioned database

Each environment's settings go on its `ResourceReleaseBinding`. See the
[PostgreSQL (advanced) guide](../apis/pg-advanced/README.md#per-environment-settings) for the
fields. Measured on a `small` instance in eu-north-1, from the binding change, with a client in the
VPC connecting every five seconds:

| Change | What happens on RDS |
| :----- | :------------------ |
| `capacity.size` up (`small` to `medium`) | Changed the instance class in about three and a half minutes. Connections were refused for about two minutes, then for about ten seconds more in a second restart. Parameters set in `configuration` kept their values |
| `capacity.size` down (`medium` to `small`) | Changed the instance class in about four minutes, with about two minutes of refused connections and a second, shorter restart. Storage stays as it is: the Composition never asks for less storage than the instance has |
| `capacity.storageGb` up (32 to 64) | Grew online in about four and a half minutes, with no restart and no refused connections. RDS then optimized the storage for about another 14 minutes, during which other changes still applied |
| `capacity.storageGb` down, or removed | Rejected before anything reaches AWS. The binding reports `storage can grow but not shrink` or `storageGb cannot be unset once set` |
| `capacity.storageAutoGrow` | Turned on in under a minute, with no restart, up to 1024 GiB |
| `maintenance.window` | Applied in about ten seconds, with no restart, together with the backup window. Removing it kept both windows on RDS |
| `network.firewallRules` | An address in the VPC could connect three seconds after its rule was added, and was refused again within six seconds of its removal. The data plane's connections were not affected |
| `configuration.connections.maxConnections` | Stored in the parameter group within about two minutes, but RDS applies it only at the instance's next restart, and does not restart for it. Restart it with `aws rds reboot-db-instance --db-instance-identifier <identifier>`: connections were refused for under ten seconds. A `capacity.size` change restarts it too, and applied a pending value in testing |
| Other `configuration` settings | Applied live, with no restart, about ten seconds after the binding changed. Every parameter goes in one update. Removing a setting resets it to the RDS default |
| `extensions` (a parameter) | Sets `rds.allowed_extensions` about ten seconds after the binding is promoted, with no restart. `CREATE EXTENSION` worked for `vector` and `uuid-ossp`, and failed for `hstore` with `This extension is not specified in "rds.allowed_extensions"` |
| `version` (a parameter) | An in-place upgrade once the binding is promoted to the new release. The parameter group for 17 existed within seconds, and the instance moved to it in the same change that upgraded it. 16 to 17 took about six minutes, with about two minutes of refused connections; the instance answered as 17.9 about three and a half minutes after the promotion. Parameters and extensions carried over. The old parameter group was deleted about six and a half minutes after the promotion, once the provider saw the instance on the new one |
| Lowering `version` | Rejected before anything reaches AWS. The binding reports `version can be raised but not lowered` |

### What to expect

Creating and deleting an instance behave as for the [PostgreSQL](#what-to-expect) Composition. In
testing (`small`, eu-north-1), the instance was available about six minutes after the binding was
created, and the `AdvancedPostgresInstance` Ready half a minute later. The binding followed about
four and a half minutes after that. Deleting the binding removed the instance, its parameter group
and `<name>-conn` in about two minutes, and the security group about forty seconds later, once RDS
had released it. No snapshot or backup was left behind. In addition:

- **Static parameters wait for a restart.** RDS stores `connections.maxConnections` in the
  parameter group and reports it as `pending-reboot` until the instance restarts. Every other
  setting in `configuration` is dynamic and applies straight away.
- **One parameter RDS does not know fails the whole update.** RDS rejects the update with
  `Could not find parameter with name`, and none of the other parameters are applied. This is why
  `timeouts.lockTimeoutSeconds` is not used.
- **Storage changes are spaced out.** After a storage change, RDS optimizes the storage: about 14
  minutes for 32 to 64 GiB in testing. RDS documents that it refuses another storage change for six
  hours, or until the optimization ends if that takes longer, and that storage autoscaling follows
  the same rule and grows storage when less than 10% is free. An instance class change and parameter
  changes made during the optimization were applied.
- **A new resource briefly holds the binding.** A new firewall rule, or the new parameter group of
  a version upgrade, is not Ready for its first few seconds, and neither is the
  `AdvancedPostgresInstance`. The binding reported `ResourcesProgressing` for about ten seconds
  after a firewall rule was added, and for about five minutes after the version upgrade's
  promotion, although the `AdvancedPostgresInstance` was Ready again within seconds. Its outputs did
  not change.
- **The provider does not copy RDS's choices back into the instance's spec.** Otherwise it would
  hold the instance to values RDS picked for the first size, such as the gp3 IOPS and throughput,
  which RDS rejects once storage passes 400 GiB. AWS assigns the IDs of the instance, the security
  group and its rules, so the Composition, or the provider, records each ID as the resource's
  external name, and a restarted provider finds the resources it created.

> [!WARNING]
> Deleting the binding deletes the database, with no final snapshot and no automated backups to
> restore from. Take a snapshot first if the data matters.

### Checking the Composition

[`render/pg-advanced/xr.yaml`](render/pg-advanced/xr.yaml) sets every setting, including a firewall
rule that renders as several CIDR blocks. Without observed resources, the render shows only the
parameter group and the security group: the instance follows once both exist. In
[`render/pg-advanced/observed.yaml`](render/pg-advanced/observed.yaml), the instance has 100 GiB,
more than the 64 GiB asked for, so the later render keeps 100 GiB.

```bash
# First reconcile: nothing exists in AWS yet
crossplane composition render aws/render/pg-advanced/xr.yaml aws/pg-advanced-composition.yaml functions.yaml \
  --xrd apis/pg-advanced/definition.yaml \
  --required-resources aws/environment-config.yaml

# A later reconcile: the instance is created, and the AdvancedPostgresInstance gets its address and
# becomes Ready
crossplane composition render aws/render/pg-advanced/xr.yaml aws/pg-advanced-composition.yaml functions.yaml \
  --xrd apis/pg-advanced/definition.yaml \
  --required-resources aws/environment-config.yaml \
  --observed-resources aws/render/pg-advanced/observed.yaml
```

## Redis

[`redis-composition.yaml`](redis-composition.yaml) builds a `RedisInstance` as an Amazon
ElastiCache for Valkey replication group. Valkey is an open-source fork of Redis that speaks the
same protocol; it reports itself to clients as Redis 7.2.4.

### What it creates

For each `RedisInstance`, in the same namespace:

| Resource | Purpose |
| :------- | :------ |
| `ReplicationGroup` | The cache, with cluster mode off, so clients connect to a single primary endpoint. TLS-only connections, encryption at rest and an auth token. The provider generates the auth token into the Secret `<name>-conn`, under `password`, and deletes the Secret with the cache. The Secret holds nothing else |

The Composition sets no engine version, so AWS uses its default at creation (Valkey 9.1 at the time
of testing).

### Outputs

| [Output](../apis/redis/README.md#outputs) | Filled from |
| :----- | :---------- |
| `host` | The primary endpoint address |
| `port` | `6379` |
| `password` | The Secret `<name>-conn` |

Clients connect with TLS and authenticate with the token alone, with no username.

### Settings

Under `redis` in the `aws` EnvironmentConfig:

| Field | Purpose |
| :---- | :------ |
| `subnetGroupName` | Existing ElastiCache subnet group over the VPC's private subnets |

Replication group IDs are unique per account and region, limited to 40 letters, digits and hyphens,
with no two hyphens in a row. Each ID is `namePrefix`, the `RedisInstance` name and a short hash of
its namespace, so Resources with the same name in different OpenChoreo namespaces or projects get
different caches. IDs that would be too long, which most are, or that had hyphens to collapse, are
shortened, with a hash of the full name appended. A cache keeps its ID once created.

### Sizes

| `size` | Node type | Nodes |
| :----- | :-------- | :---- |
| `small` | `cache.t4g.micro` | 1 |
| `medium` | `cache.t4g.small` | 2, in different zones, with automatic failover |
| `large` | `cache.t4g.medium` | 2, in different zones, with automatic failover |

Only `small` has been tested against AWS; `medium` and `large` have been checked against the
provider's schema. Changing an existing cache's size has not been tested.

### Networking

The cache has no public endpoint. It accepts connections only from sources the security groups in
`securityGroupIds` admit.

### What to expect

- **A new cache takes about twelve minutes to reach the Workload.** In testing (`small`,
  us-east-1), AWS had the cache available, and the `RedisInstance` Ready, after about six minutes.
  The binding followed about five minutes later, and a dependent component connected straight
  after.
- **Deletion takes a few minutes.** AWS removed the cache about seven minutes after the binding was
  deleted, and `<name>-conn` went with it.
- **AWS will not list a cache's tags until it is available.**

### Checking the Composition

```bash
# First reconcile: nothing exists in AWS yet
crossplane composition render aws/render/redis/xr.yaml aws/redis-composition.yaml functions.yaml \
  --xrd apis/redis/definition.yaml \
  --required-resources aws/environment-config.yaml

# A later reconcile: the RedisInstance gets its address and port, and becomes Ready
crossplane composition render aws/render/redis/xr.yaml aws/redis-composition.yaml functions.yaml \
  --xrd apis/redis/definition.yaml \
  --required-resources aws/environment-config.yaml \
  --observed-resources aws/render/redis/observed.yaml
```

## Uninstall

Delete every binding of a Resource built by this backing first, and wait until no AWS managed
resource is left. If a provider is removed while its resources still exist, they keep running in
AWS with nothing left to delete them.

```bash
# 1. Confirm no managed resources are left
kubectl get managed -A

# 2. Remove the Compositions, the settings, the credentials and the providers
kubectl delete --ignore-not-found -f aws/postgres-composition.yaml -f aws/pg-advanced-composition.yaml \
  -f aws/redis-composition.yaml
kubectl delete -f aws/environment-config.yaml
kubectl delete -f aws/provider-config.yaml
kubectl delete -f aws/provider.yaml
kubectl delete providers.pkg.crossplane.io crossplane-contrib-provider-family-aws

# 3. In AWS: the networking, then the Pod Identity associations and their roles
VPC_ID=$(aws eks describe-cluster --name "$CLUSTER" --region "$REGION" \
  --query cluster.resourcesVpcConfig.vpcId --output text)
SG=$(aws ec2 describe-security-groups --region "$REGION" \
  --filters "Name=vpc-id,Values=$VPC_ID" Name=group-name,Values=openchoreo-crossplane \
  --query 'SecurityGroups[0].GroupId' --output text)
aws elasticache delete-cache-subnet-group --region "$REGION" --cache-subnet-group-name openchoreo-crossplane
aws rds delete-db-subnet-group --region "$REGION" --db-subnet-group-name openchoreo-crossplane
aws ec2 delete-security-group --region "$REGION" --group-id "$SG"
eksctl delete podidentityassociation -f pod-identity.yaml
```

Then continue with the [module uninstall](../README.md#uninstall).

## Compatibility

| Component | Compatible version | Notes |
| :-------- | :----------------- | :---- |
| **provider-aws-rds** | `v2.9.x` | Verified against v2.9.0, from `xpkg.crossplane.io/crossplane-contrib`. Uses the namespaced `*.aws.m.upbound.io` API groups. The PostgreSQL (advanced) Composition was verified with PostgreSQL 16 and 17, from a k3d cluster authenticating through an IAM OIDC identity provider (WebIdentity) rather than on EKS. |
| **provider-aws-ec2** | `v2.9.x` | Verified against v2.9.0, from `xpkg.crossplane.io/crossplane-contrib`, for the PostgreSQL (advanced) Composition. |
| **provider-aws-elasticache** | `v2.9.x` | Verified against v2.9.0, from `xpkg.crossplane.io/crossplane-contrib`. |
| **EKS** | `1.36` | Verified on EKS 1.36.4 with the Pod Identity agent add-on v1.3.10. |
