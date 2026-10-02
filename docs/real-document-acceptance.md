# Real Document acceptance

The required native Document job runs the Percona 1.23.0 operator, MongoDB 8.0.26-11 and the
controller in a disposable KSail Kubernetes 1.34.0 cluster. It is separate from the synthetic
observer regressions in `tests/source/`: no operator status is fabricated.

Run `bash tests/provider/percona.sh` on an amd64 Linux host with Docker, KSail 7.182.6, kubectl,
Helm, jq, yq, OpenSSL and GNU timeout. The hosted job installs the checksum-verified KSail build.
It checks the CPU features required by Percona's UBI10 images and installs checksum-pinned
operator/CRD charts with immutable operator, database and Kubernetes image digests. It records
the actual installed image identities, source UID, Kubernetes version, storage policy and elapsed
phase times in the run log. A local host without Docker cannot supply this real-cluster evidence.

Publication is exercised first with the signed released v1.14.0 controller image, verified against
its exact release workflow, tag and source revision, and then with the current PR build. The
current build runs the remaining lifecycle assertions. Both image identities are recorded;
passing the released baseline cannot clear a failed assertion against the current build.

The independent query fixture has its own Go module; MongoDB dependencies stay outside the
controller module. Its reader uses a verified TLS connection and a projected password, serves
one fixed HTTPS GET, and publishes an OpenAPI 3.1 contract. A separate writer seeds one synthetic
document. The job verifies effective insert, update, delete and privilege-management denial by
MongoDB error code, then proves the original document remains unchanged. NetworkPolicy limits
query consumers and database access; removing and restoring the same consumer label exercises
the policy rather than assuming that its installation enforces it.

The lifecycle checks require current publication ownership and real operator readiness. They
pause/resume the source, recover the same persistent document, rotate the reader password
without replacing the query Pod or Secret, reject the stale password, and recreate the source
with retained PVCs. Orphan deletion preserves the independent publication, and its owner must
be rebound to the new source UID. The database uses the orderly-pod deletion finalizer and omits
the PVC-deletion finalizer. Deleting a DataProduct is a separate check that preserves source,
publication and PVC identities and queryable records.

Both provider gates are tested independently. An API-server audit policy records only request
metadata from the controller's source and Secret reads. Enabled observation must produce audit
events; after each disabled rollout, there must be zero such reads across a full polling interval.
Fixed synthetic passwords and the document payload must be absent from controller logs, product
status and the registry response. Neither the audit log nor diagnostics dump Secret values.

The job has a 40-minute outer limit. The harness has a shared 33-minute assertion deadline,
with individual setup, startup, publication, pause, recovery and gate budgets, plus a three-minute
cleanup reserve. Every phase uses the smaller remaining deadline; partial output from a failed
observation is a failure. Cleanup deletes only the generated test cluster and its storage.

This acceptance establishes the native Document row of the provider matrix. It does not establish
Graph, hybrid, backups, production high availability or production rollout. Both release gates
remain default-off. See [ADR 0014](adr/0014-real-document-acceptance.md), [engine providers](engine-providers.md)
and the separate [source observation suite](source-integration-tests.md).
