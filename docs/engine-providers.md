# Select an independently operated engine

A product can wait for an externally owned SQL or Document engine and its published application credentials.
The controller observes readiness; the database operator creates, upgrades, backs up and deletes
the source. Product workloads consume the connection Secret directly.

Enable both `provisionedSources.enabled=true` and `engineProviders.enabled=true` in the Helm chart.
The manager settings are `PROVISIONED_SOURCES_ENABLED=true` and `ENGINE_PROVIDERS_ENABLED=true`.
The OpenFeature flags `provisioned-sources` and `engine-providers` default off. A declared engine
stays unready while either gate is off, and disabled gates perform no source or Secret reads.
Apply the release's CRD before upgrading an existing chart installation, as described in the README.

## Supported selection

`spec.source.engine` uses the `engine-provider/v1` contract. Admission binds each supported selection
to one adapter and resource API. The runtime resolver repeats that check before any reads.

| Selection             | Adapter              | Referenced resource                           | Connection publication                                                      |
|-----------------------|----------------------|-----------------------------------------------|-----------------------------------------------------------------------------|
| Engine omitted        | `crossplane/v1`      | Namespaced custom resource                    | Matching `writeConnectionSecretToRef`, owned by that resource               |
| `sql` / `native`      | `cnpg/v1`            | `postgresql.cnpg.io/v1` `Cluster`             | Operator-generated `<cluster>-app` Secret, owned by the current Cluster UID |
| `document` / `native` | `percona-mongodb/v1` | `psmdb.percona.com/v1` `PerconaServerMongoDB` | Explicit custom-user password Secret, published with the current source UID |

Graph and `cnpg-hybrid` selections are rejected until their adapters and admission rules are delivered.
Unknown versions, contradictory adapters and other resource APIs are rejected at admission. SQL
requires its generated application Secret name; Document publication receives additional runtime
checks against the externally owned source. Existing untyped Crossplane products retain their contract.

The [SQL example](examples/sql-provider-product.yaml) refers to a Cluster named `warehouse` in
`products`. Install and configure CloudNativePG independently, including storage, database ownership,
network access, backups and retention. Deploy the product's query workload and public contract
independently; declaring output URLs does not create them.

## CloudNativePG observation contract

The `cnpg/v1` adapter requires:

- exactly one `Ready=True` condition;
- positive `spec.instances`, with matching `status.instances` and `status.readyInstances`;
- matching, nonempty `status.currentPrimary` and `status.targetPrimary`;
- current explicit observed generations; an omitted generation follows CNPG's condition contract;
- a non-deleting generated application Secret whose owner reference matches the current Cluster's
  UID, name, API group and kind.

The adapter accepts the operator-generated application Secret only. A supplied
`spec.bootstrap.initdb.secret`, `spec.bootstrap.recovery.secret` or
`spec.bootstrap.pg_basebackup.secret` is outside this publication contract and reports
`ConnectionPublicationUnsupported`. Application workloads use the generated credentials directly;
the controller never requests their values. See CloudNativePG's [application connection guide](https://cloudnative-pg.io/docs/1.28/applications/)
and [Cluster API reference](https://cloudnative-pg.io/docs/1.28/cloudnative-pg.v1/).

`SourceReady` refreshes independently of other dependencies. Missing sources, publication ownership
mismatches, lost permissions, unready replicas and deletion produce stable reasons without copying
provider messages into product status. Aggregate readiness also requires the product's other
declared dependencies. Typed observation uses a fixed resource mapping, fresh exact-name reads, a five-second deadline and
30-second polling; unchanged observations do not rewrite status.

CloudNativePG may omit observed generations. In that case the observer cannot establish that status
reflects the latest spec. Control-plane readiness and Secret ownership do not prove Secret contents,
database authentication, query availability, backups or extension support. The registry publishes
neither source references nor credentials.

## Grant narrow observation access

Apply the [observer Role example](examples/sql-provider-observer-rbac.yaml) in the product namespace.
Adjust its controller ServiceAccount and namespace for your installation. It grants `get` only on
`warehouse` and `warehouse-app`; the chart does not grant database or Secret access.

The manager requests `PartialObjectMetadata` for the Secret and bypasses caches. Kubernetes RBAC
authorizes a whole Secret `get`, even when the request negotiates metadata only, so keep resource
names and namespaces explicit. No list, watch or mutation grant is needed for external sources.

## Percona MongoDB observation contract

The [Document product](examples/document-provider-product.yaml) selects the `percona-mongodb/v1`
adapter. Install Percona Operator for MongoDB 1.23.0 independently and use `spec.crVersion: 1.23.0`.
That declaration does not verify the installed operator image. The initial supported profile is
one managed, unpaused, unsharded replica set, with positive size and no arbiter, non-voting, hidden
or external members.
`status.state` must be `ready`, and `status.size` and `status.ready` must equal the requested size.
An explicit `status.observedGeneration` must be current. Percona's published status contract may
omit it; in that case the observer cannot establish that status reflects the latest spec.

The referenced password Secret must match exactly one explicit `spec.users[].passwordSecretRef.name`.
The selected custom user must declare only built-in `read` roles on non-system databases. Its
authentication database may be `admin`; `$external` authentication is unsupported. For example:

```yaml
spec:
  crVersion: 1.23.0
  replsets:
    - name: rs0
      size: 3
  users:
    - name: catalog-reader
      db: admin
      passwordSecretRef:
        name: documents-reader
        key: password
      roles:
        - name: read
          db: catalog
```

This fragment shows the observation fields, not a complete database installation. An independent
publisher must bind `documents-reader` to the current `PerconaServerMongoDB` through an owner
reference with API `psmdb.percona.com/v1`, kind, name and UID. This is a controller publication
requirement; Percona documentation does not promise that ownership automatically. Do not modify
operator-managed system or connection-string Secrets to satisfy it. The configured `spec.secrets.users`,
default system Secret, system accounts, duplicate usernames, generated passwords, multiple users sharing the password
Secret, custom roles and privileged roles report `ConnectionPublicationUnsupported`.

Apply the [Document observer Role](examples/document-provider-observer-rbac.yaml), adjusting its
ServiceAccount for your installation. It grants only the named source and password Secret GETs.
The application workload consumes its credentials independently; the controller requests only
Secret metadata. Declared read roles and publication ownership do not prove effective privileges,
password validity, database access or query availability. See Percona's
[custom-user guide](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/app-users.html),
[status contract](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/cr-statuses.html) and
[system-user guidance](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/system-users.html).

## Lifecycle and rollout

Creation and deletion stay with the operator. A missing source reports `SourceNotFound`; deleting
one reports `SourceDeleting`. Recreating it changes its UID, so an old Secret cannot satisfy
publication ownership. Credential rotation at the same Secret name does not require a DataProduct
change or copy values into status. Removing the source declaration or deleting the DataProduct
leaves the source and Secret independently owned; the controller adds no finalizers or owner references.

The hosted Kubernetes acceptance suite exercises admission, scoped permissions, both gates,
readiness loss/recovery, publication ownership and retention using synthetic SQL and Document status fixtures.
It does not install database operators or prove database availability. Real operator acceptance remains
in [#38](https://github.com/devantler-tech/data-product-controller/issues/38), and released deployment
acceptance is required before retiring the gate in [#128](https://github.com/devantler-tech/data-product-controller/issues/128).
