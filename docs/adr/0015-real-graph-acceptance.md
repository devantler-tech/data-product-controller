# ADR 0015: Real Graph query and lifecycle acceptance

Status: Accepted

## Context

Operator-reported readiness and declared grants cannot prove that an application can traverse a
graph or that its credential denies mutations. An immutable database image also needs to remain
compatible with the observer's supported release profile.

## Decision

A required hosted acceptance job installs ArangoDB operator 1.4.5 and the official ArangoDB 3.12.12 image in
an isolated KSail Kubernetes 1.34 cluster. The chart is checksum-pinned; operator and database
images use verified immutable digests. The observer accepts the official release index only and
requires its accepted, desired and member image declarations to agree. Single/count-one is a
development profile with no high availability.

An independent bootstrap workload creates the database, named graph and separate application
users. A writer creates three vertices and two edges. A separate query workload consumes only the
reader's projected password and public database CA, and serves a fixed two-hop traversal through
HTTPS and OpenAPI. No database client or bootstrap credential enters the controller process.
Actual successful reads bracket numeric authorization denials; authentication and transport
failures cannot satisfy the denied-write assertion.

Acceptance checks operator-written current-spec readiness, publication ownership, enforced
consumer isolation, outage recovery, same-name credential rotation, source recreation and
retained PVC identities. Source recovery follows the operator's documented maintenance procedure:
restore only previously observed member IDs and PVC names, then let the real operator recompute
readiness and image/spec evidence. It never fabricates Ready conditions or spec checksums.

Both controller gates independently withdraw readiness and perform zero audited source/Secret
GETs while the independent query remains available. Descriptor deletion leaves external source,
password, storage and query behavior intact. A 40-minute job bounds setup, 33 minutes of assertions
and three minutes of cleanup; failed assertions or cleanup fail the job.

## Consequences

This development profile supplies real native Graph behavior evidence. It does not establish
production high availability, backup recovery, certified Kubernetes distribution support or
license approval. Community binary terms apply independently. Production activation and released
provider-gate retirement require their separate rollout evidence.

## References

- [Native Graph observation](0013-native-graph-observation.md)
- [ArangoDB operator recovery](https://arangodb.github.io/kube-arangodb/docs/how-to/recovery.html)
- [ArangoDB user permissions API](https://docs.arango.ai/arangodb/3.12/develop/http-api/users/)
- [Community binary terms](https://arangodb.com/community-license/)
