# Real PostgreSQL acceptance

`tests/provider/postgres.sh` exercises native SQL, hybrid JSONB and hybrid AGE in a disposable
KSail Kubernetes 1.34 cluster with enforced Cilium NetworkPolicy. The job installs the pinned
CloudNativePG 1.30.1 operator from a checksum-verified Helm package. PostgreSQL uses the owned
signed AGE 1.7 image; acceptance verifies its digest, source revision, release tag and immutable
signing workflow, then checks the operator and database images actually running in the cluster.
The independent owner also checks effective server preloading and the installed AGE version
over the instance's local socket, before initial queries and after retained-source recovery.
That check returns only a boolean; query readers gain no server-settings privilege.

Three independently operated query workloads expose fixed HTTPS GET contracts:

| Model | Database read | Authorization proof |
|---|---|---|
| Native SQL | A retained row through a dedicated SQL reader | INSERT, UPDATE and DELETE each fail with PostgreSQL `42501` |
| Hybrid Document | JSONB records filtered by the published marker | INSERT, UPDATE and DELETE each fail with `42501`; private records stay excluded |
| Hybrid Graph | Actual AGE traversal across one and two hops | Cypher CREATE, SET and DETACH DELETE each fail with `42501` |

All readers also reject privileged role creation, schema creation and assuming the writer role.
Successful reads before and after these checks distinguish denied privileges from an unavailable
database. Read-only transaction failures are insufficient evidence. TLS verifies the fixed database
hostname using the mounted CA certificate. The query workloads reread bounded password projections
for each connection; consumers and the controller receive no database credentials.

The job checks each HTTPS contract and rejects an unapproved consumer through TCP isolation.
It first publishes the actual SQL source through the signed released controller, then requires
the current controller to publish all three source profiles and registry entries. Hybrid
publications bind to the observed Cluster UID and generation. A real resource change withdraws
stale publications until an independent query verification and metadata rebind restore readiness.

CloudNativePG hibernation must withdraw current readiness and make every query report the actual
database outage. Resuming the same source must restore the persisted records and retained object
identities. For every reader, password rotation must produce an actual `28P01` rejection for the
old password, recover through the new projected password and preserve the query Pod and Secret UIDs.
An orphaned source recreation must retain storage, reject stale hybrid ownership and recover after
binding to the new source identity.

Both default-off controller gates are tested independently. The running Kubernetes API server must
use the exact audit policy and demonstrate enabled controller reads before unchanged audit counts
can prove zero reads across a polling interval. Queries must keep working while observation is
disabled. Deleting all descriptors must remove their registry entries while retaining the source,
publications, persistent volumes and query paths. Full controller logs and public metadata are
checked for fixture passwords and distinctive data-plane content. The complete source/Secret
audit also rejects every mutation attempt, including denied requests, and broader list/watch access.

The fixture is a separate Go module at `tests/provider/fixture`, with bounded PostgreSQL connections
and query responses. No database clients enter the controller module. The hosted job has an absolute
33-minute assertion budget and a separate three-minute cleanup allowance, within a 40-minute limit.
It removes only its own disposable cluster and does not upload database records or credentials.

Run with Docker, verified KSail, Cosign, kubectl, Helm, yq, jq, OpenSSL and Go available:

```bash
bash tests/provider/postgres.sh
```

This profile establishes real query, grant and retained lifecycle behavior in disposable Kubernetes.
Production activation and deployed artifact readback remain separate platform work. A single-instance
test profile makes no high-availability, backup or production capacity claim.
