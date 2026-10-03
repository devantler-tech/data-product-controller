# Select an independently operated engine

A product can wait for an externally owned SQL, Document or Graph engine and its published application credentials.
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

| Selection             | Adapter              | Referenced resource                           | Connection publication                                                                          |
|-----------------------|----------------------|-----------------------------------------------|-------------------------------------------------------------------------------------------------|
| Engine omitted        | `crossplane/v1`      | Namespaced custom resource                    | Matching `writeConnectionSecretToRef`, owned by that resource                                   |
| `sql` / `native`      | `cnpg/v1`            | `postgresql.cnpg.io/v1` `Cluster`             | Operator-generated `<cluster>-app` Secret, owned by the current Cluster UID                     |
| `document` / `native` | `percona-mongodb/v1` | `psmdb.percona.com/v1` `PerconaServerMongoDB` | Explicit custom-user password Secret, published with the current source UID                     |
| `graph` / `native`    | `arangodb/v1`        | `database.arangodb.com/v1` `ArangoDeployment` | Independently published read-only application password, with v1 metadata and current source UID |

`cnpg-hybrid` selections are rejected until their adapters and admission rules are delivered.
Unknown versions, contradictory adapters and other resource APIs are rejected at admission. SQL
requires its generated application Secret name; Document and Graph publication receive additional runtime
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

Engine and legacy source readers negotiate only single-object partial metadata for application Secrets.
Servers that cannot provide that representation fail observation with `SourceUnavailable`;
the client never negotiates a full-Secret fallback. Kubernetes still authorizes the complete
Secret GET, so exact-name RBAC remains required. Normal source-object reads retain their full
operator status representation.

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
operator-managed system or connection-string Secrets to satisfy it. Admission rejects the default
system Secret. Initial runtime validation also rejects it as `SourceInvalid`, before any reads.
The configured `spec.secrets.users`, internal system Secrets, operator-generated passwords,
connection-string Secrets, system accounts, duplicate usernames, multiple users sharing the password
Secret, custom roles and privileged roles report `ConnectionPublicationUnsupported`. Reserved publication names follow
Percona's [connection-Secret naming contract](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/connection-secrets.html#secret-names);
a manual password binding cannot reuse another declared user's generated connection-Secret name.

Apply the [Document observer Role](examples/document-provider-observer-rbac.yaml), adjusting its
ServiceAccount for your installation. It grants only the named source and password Secret GETs.
The application workload consumes its credentials independently; the controller requests only
Secret metadata. Declared read roles and publication ownership do not prove effective privileges,
password validity, database access or query availability. See Percona's
[custom-user guide](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/app-users.html),
[status contract](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/cr-statuses.html) and
[system-user guidance](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/system-users.html).

## ArangoDB Graph observation contract

The [Graph product](examples/graph-provider-product.yaml) selects `arangodb/v1`. Its query URL
and OpenAPI document describe a separately operated workload; DPC does not create that workload
or send AQL queries. Install the ArangoDB operator independently, using the
[1.4.5 API](https://github.com/arangodb/kube-arangodb/tree/c8ddcb3ff018436f5641607e778b4960cf1794b9/pkg/apis/deployment/v1).
The real-provider acceptance profile requires `spec.mode: Single`, explicit `spec.single.count: 1`,
enabled authentication and the verified release index
`sha256:4bc086d5050ca7ea11c6d00a36d8b910c838bb54ad553f8c1b715769d3499bcf`.
The index is accepted with the `3.12.12` tag or without a tag, using `arangodb` or
`docker.io/library/arangodb`. The original tag-only declaration `arangodb:3.12.12` retains its
previous non-enterprise binary-metadata requirement; it does not identify the immutable profile
tested here. Other digests, registries, image repositories and contradictory tags are unsupported.
Single mode provides no high availability.

The live typed specification checksum must match both `status.acceptedSpecVersion` and
`status.appliedVersion`. DPC does not apply defaults before hashing: the operator hashes the raw
spec and separately stores defaulted `status.accepted-spec`. The accepted specification must also
retain exactly the declared image, Single/count-one profile and a resolved authentication Secret;
contradictory status does not establish readiness. `Ready`, `SpecAccepted`, `UpToDate`,
`BootstrapCompleted` and upstream's misspelled `BootstrapSucceded` conditions must be True.
Deployment phase must be Running, with exactly one Created and Ready Single member, a modern
Pod name/UID, matching image declarations and reported desired/running image IDs, and ArangoDB
3.12.12 versions. The verified official immutable index reports an Enterprise binary marker;
both current and member observations must match that actual binary profile. The marker does not
establish license entitlement. Update,
upgrade, Secret-change, pending update and member-restart states withdraw readiness. Missing,
unknown, malformed or duplicate conditions cannot establish readiness. Condition hashes,
transition timestamps, historical SpecPropagated and Pod-spec checksums are not freshness markers.

An independent publisher owns application setup and the password Secret. The pinned bootstrap
validator accepts only root accounts. The publisher must create a dedicated non-administrator
user, deny `_system` and database wildcard access, grant `ro` on the application database, deny
collection wildcard access and grant `ro` on every named vertex/edge collection. Assign explicit
`none` grants to all other application collections. ArangoDB's database `ro` grant otherwise
supplies read access when no specific collection grant exists; a collection wildcard `none`
does not override it. The publisher must install a specific denial before adding an unpublished
collection, and keep system collections outside the published query contract. It publishes the
password for consumption directly by the query workload. DPC sees only this public metadata:

```yaml
metadata:
  name: lineage-reader
  namespace: products
  annotations:
    data.devantler.tech/arango-publication: v1
    data.devantler.tech/arango-user: catalog-reader
    data.devantler.tech/arango-database: catalog
    data.devantler.tech/arango-graph: lineage
    data.devantler.tech/arango-access: read-only
    data.devantler.tech/arango-collections: products,relations
  ownerReferences:
    - apiVersion: database.arangodb.com/v1
      kind: ArangoDeployment
      name: lineage
      uid: <current-source-uid>
```

Under v1, `read-only` declares this grant profile, including no other positive collection grants
and explicit denials for unpublished application collections. This is publisher intent, not
proof that future collections are automatically isolated. See the upstream
[permission resolution rules](https://docs.arango.ai/arangodb/3.12/operations/administration/user-management/#permission-resolution).
Identifiers start with an ASCII letter followed by at most 63 ASCII letters, digits,
underscores or hyphens. Collections form a comma-separated list of 1–64 unique identifiers
without whitespace or wildcards. Root, operator, internal and backup users are rejected in any
letter case. System names, unsupported versions and writable declarations are rejected.
The selected Secret must match the current source owner. Default/configured JWT, root-password
and operator credential publications are unsupported. Do not relabel operator Secrets as application publications.

Apply the [Graph observer Role](examples/graph-provider-observer-rbac.yaml), adjusting its
ServiceAccount binding. It permits only exact-name GETs on `lineage` and `lineage-reader` in
`products`. Kubernetes authorizes a whole-Secret GET despite metadata negotiation.

These checks establish operator-reported current-spec readiness and publisher intent. They do
not verify the installed operator binary, immutable running image, credential values, effective
permissions, graph existence, queries, backups or distribution support. ArangoDB's
[Community binary terms](https://arangodb.com/community-license/) restrict deployment uses;
this adapter neither deploys nor licenses the database. Independently verify the applicable
edition and terms. The required [real Graph acceptance](real-graph-acceptance.md) separately
exercises the pinned operator and authenticated traversal; its current-head run must pass.

## Lifecycle and rollout

Creation and deletion stay with the operator. A missing source reports `SourceNotFound`; deleting
one reports `SourceDeleting`. Recreating it changes its UID, so an old Secret cannot satisfy
publication ownership. Credential rotation at the same Secret name does not require a DataProduct
change or copy values into status. Removing the source declaration or deleting the DataProduct
leaves the source and Secret independently owned; the controller adds no finalizers or owner references.

The hosted source-observation suite exercises admission, scoped permissions, both gates,
readiness loss/recovery, publication ownership and retention using synthetic SQL, Document and Graph status fixtures.
It does not install database operators or prove database availability. The separate required
[real Document acceptance](real-document-acceptance.md) installs Percona and exercises authenticated queries,
effective privileges, rotation, outage recovery and retained data. Its current-head run must pass;
synthetic observer results cannot replace that evidence. The required
[real Graph acceptance](real-graph-acceptance.md) applies the same boundary to ArangoDB traversal,
effective grants and retained source recovery. The remaining real provider matrix is tracked
in [#38](https://github.com/devantler-tech/data-product-controller/issues/38), and released deployment
acceptance is required before retiring the gate in [#128](https://github.com/devantler-tech/data-product-controller/issues/128).
