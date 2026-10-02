# ADR 0012: Native Document observation through Percona

Status: Accepted

## Context

Document products need independently operated MongoDB sources. The engine-provider boundary
keeps database lifecycle, credentials and data outside the controller. A supported source must
have a bounded observation contract and a dedicated application credential publication.

## Decision

`engine-provider/v1` Document/native selects `percona-mongodb/v1` and a same-namespace
`psmdb.percona.com/v1` `PerconaServerMongoDB`. Its declared `spec.crVersion` is `1.23.0`.
The initial profile supports one managed, unsharded replica set without arbiters, external or non-voting
members. Readiness requires `status.state=ready` and both reported counts equal the positive
requested replica count. An explicit observed generation must be current; its absence cannot
prove status freshness.

The product references a password Secret explicitly bound to exactly one custom application
user. Every declared role must be built-in `read` on a non-system database. System accounts,
generated passwords and shared user bindings are outside this profile. An independent publisher
must give the Secret an owner reference matching the current source's API, name and UID.
Percona's documentation does not establish that it adds this owner reference automatically.

The observer uses fresh exact-name GETs with a fixed REST mapping, a shared five-second deadline
and metadata-only Secret negotiation. The existing default-off source and engine gates apply.
No database client, source mutation, adoption, credential read or lifecycle ownership is added.

## Consequences

The same reconciliation and independent `SourceReady` behavior serves SQL and Document products.
The narrow profile rejects unsupported topology, publication and privilege declarations before
reading Secret metadata. Scoped RBAC still authorizes the whole Secret GET, so deployments must
grant only the referenced names in the product namespace.

Control-plane observations do not establish actual operator version, credential contents,
effective database privileges, authentication, queries or backup health. Synthetic Kubernetes
acceptance verifies this boundary; real operator lifecycle and workload acceptance remain #38.

## References

- [Engine-provider boundary](0010-versioned-engine-provider-observation.md)
- [Percona 1.23 API](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/api.html)
- [Percona custom users](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/app-users.html)
- [Percona status contract](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/cr-statuses.html)
