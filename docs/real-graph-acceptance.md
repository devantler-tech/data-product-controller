# Real native Graph acceptance

The required `Validate native Graph queries` job runs `bash tests/provider/arango.sh` against
ArangoDB operator 1.4.5 and the official ArangoDB 3.12.12 image. It uses an ephemeral KSail Kubernetes 1.34
cluster, one persistent Single server, authentication, verified TLS and enforced NetworkPolicy.
The checksum-pinned Helm package and immutable images are checked against actual running image IDs.
The server certificate explicitly includes the client's full Service hostname; trust and hostname
verification remain enabled.

The independent bootstrap workload creates the named `lineage` graph in `catalog`, a writer and
a reader. It grants the reader database access and read-only access to the named vertex/edge
collections, with database and collection wildcards denied. A separate existing `private`
collection contains a synthetic unpublished record and receives an explicit `none` grant;
the reader must receive an authorization denial when requesting it. A database `ro` grant
otherwise permits reads of collections with no specific grant, so the publisher must install
denials before adding unpublished collections. Its privileged password has no query
Service or incoming network access. The separate query workload receives no root password,
JWT secret, database server private key or Kubernetes token.

The application serves one fixed AQL traversal as `GET /api/lineage` and publishes OpenAPI 3.1.
Only the writer seeds the vertices and edges. Acceptance requires the exact two-hop result before
and after denied insert, update, delete, user-administration and privilege-escalation operations.
The server must report its default writable mode before and after those denials, and the separate
writer must still insert, update and delete a scratch record. This distinguishes per-reader denial
from global read-only mode or an unavailable collection. A transport error or invalid credential
does not prove a read-only grant. The consumer verifies
HTTPS and proves network isolation by losing its approved identity and recovering it.

The job then exercises released-baseline rejection of the immutable image, current-head observer
acceptance, current source ownership, a real server scheduling outage, recovery, password rotation
and stale-password rejection. Rotation retries the publisher together with the old-password
authentication check, so delayed Secret projection cannot finish publication early.
It recreates the source using the operator's documented retained
member recovery procedure, verifies a changed source UID and unchanged retained PVC/publication
UIDs, rejects stale publication ownership and rebinds the independent password publisher.
Scheduling recovery replaces the outage selector with the observed node's hostname, because
clearing the selector restores the operator's accepted outage configuration. Runtime cleanup
records unlabelled members through source ownership and observed member IDs, and binds each
Pod to its observed name and UID instead of guessing the operator's generated name. It tolerates
Pods already removed by the operator's deletion finalizer.
The recovery patch uses the live served CRD's status endpoint; an unknown serving contract fails
before any patch, and a plain status field does not receive a subresource request.

The last phase independently disables both observer gates, checks zero source/Secret GETs across
a polling interval, restores readiness and deletes only the product descriptor. The query and
external identities must survive. Controller output must contain neither projected passwords nor
fixture records. The complete source/Secret audit must contain observed GET requests for only
the declared namespace, API group, resource and object names, and no mutation attempts, including
denied requests. List or watch access also fails this scoped profile.
Cleanup removes only the run's disposable cluster and storage; cleanup failure
is a failed acceptance result.

Run the separate fixture tests explicitly:

```bash
cd tests/provider/fixture
go test -race -count=1 ./...
go vet ./...
```

Cluster execution requires amd64, Docker, KSail, kubectl, Helm, jq, yq, OpenSSL, GNU timeout, curl
and Cosign. The local unit suite does not establish operator behavior. Current-head hosted
acceptance must pass before merge. Single mode has no high availability; Kind development
evidence does not certify a production distribution, backup policy or Community license use.
Production rollout remains tracked separately in
[#128](https://github.com/devantler-tech/data-product-controller/issues/128).

See [ADR 0015](adr/0015-real-graph-acceptance.md) for the contract and its limits.
