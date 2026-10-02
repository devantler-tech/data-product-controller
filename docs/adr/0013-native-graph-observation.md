# ADR 0013: Native Graph observation through ArangoDB

Status: Accepted

## Context

Graph products need an independently operated graph source and an application credential
publication. Source readiness must describe the current configuration without making the
controller a database client or lifecycle owner.

## Decision

`engine-provider/v1` Graph/native selects `arangodb/v1` and a same-namespace
`database.arangodb.com/v1` `ArangoDeployment`. The observation profile uses the operator
1.4.5 API, the official ArangoDB 3.12.12 Community image and an explicit Single server count
of one. The upstream typed API computes the checksum of the live specification without
applying defaults. Both accepted and applied versions must match it. Current runtime,
successful bootstrap and exactly one ready member are required; historical propagation
and condition timestamps do not establish freshness.

An independent publisher creates a dedicated read-only application user and publishes its
password Secret. Bootstrap passwords in this operator release are root-only. The selected
Secret must carry a current-source owner reference and a bounded `v1` publication declaration
naming its application user, database, graph and collections. The declaration promises no
system or wildcard access, read-only access to the application database and the explicitly
listed collections, and no other collection grants. DPC rejects operator and administrator
publications and requests only Secret metadata.

The existing default-off source and engine gates apply. Observation uses fixed REST
mappings, exact-name uncached GETs and a shared five-second deadline. The external operator,
publisher and query workload retain lifecycle, credentials and database access.

## Consequences

Specification changes withdraw readiness until the operator applies the new configuration.
Source recreation rejects a retained publication owned by the previous UID. Product removal
and flag rollback retain independently owned resources.

The upstream API dependency preserves the released checksum serializer and defaulting
semantics. Only its API is used; no operator reconciler or database client is instantiated.
Publication metadata is publisher intent. It does not establish effective permissions,
password validity, graph contents or AQL availability. These require real operator and
application acceptance under #157; #36 and released rollout #128 remain open.

This profile does not verify the installed operator version, license suitability or vendor
support for a Kubernetes distribution. Community binary terms restrict deployment uses;
database operation and licensing remain with the external operator's owner.

## References

- [Engine-provider boundary](0010-versioned-engine-provider-observation.md)
- [Pinned specification checksum](https://github.com/arangodb/kube-arangodb/blob/c8ddcb3ff018436f5641607e778b4960cf1794b9/pkg/apis/deployment/v1/deployment_spec.go#L677)
- [Pinned freshness predicates](https://github.com/arangodb/kube-arangodb/blob/c8ddcb3ff018436f5641607e778b4960cf1794b9/pkg/apis/deployment/v1/deployment.go#L124)
- [Root-only bootstrap](https://github.com/arangodb/kube-arangodb/blob/c8ddcb3ff018436f5641607e778b4960cf1794b9/pkg/apis/deployment/v1/bootstrap.go#L87)
- [Application permissions](https://docs.arango.ai/arangodb/stable/operations/administration/user-management/)
- [Community license](https://arangodb.com/community-license/)
