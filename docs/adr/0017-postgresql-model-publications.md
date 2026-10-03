# ADR 0017: PostgreSQL model publications

- Status: Proposed
- Date: 2026-10-03

## Context

A running PostgreSQL Cluster can support several data models, but its application Secret does
not establish a JSONB publication or an AGE graph capability. A controller observation must also
reject stale source identities, stale publication generations and unsupported runtime images.
The data plane owns records, database roles, extension setup and network access.

## Decision

Keep SQL on the native `cnpg/v1` adapter. Select Document and Graph explicitly through
`cnpg-hybrid/v1`, using the existing default-off engine and source gates. Hybrid observation
accepts a supported immutable image only when the Cluster reports that same current image and
the existing CloudNativePG readiness contract holds. Graph additionally requires the owned AGE
image and its declared preload configuration.

Read only the named same-namespace Cluster and the metadata of a dedicated reader Secret.
Hybrid publications identify the current Cluster UID and generation, a bounded database and
reader role, and either the JSONB schema/table/column or the AGE graph and capability version.
Reject bootstrap, superuser, replication and server Secrets before any external read. An
independent publisher verifies the live query and grants before publishing capability metadata.
Neither publication nor observation transfers credentials to the controller.

A required acceptance job installs the pinned CloudNativePG operator in a disposable cluster.
It verifies the signed owned AGE artifact and exercises native SQL, JSONB and AGE through
separate authenticated TLS reader workloads. Real reads bracket specific authorization denials.
The same job checks enforced network isolation, source-generation rebinding, actual database
hibernation, password rotation, retained source recreation and independent controller gates.

## Consequences

- A metadata publication describes publisher intent; it does not replace ongoing query or grant
  monitoring in the independently operated data plane.
- Image catalogs and other mutable image indirection are unsupported by this observer because
  its bounded exact-name reads cannot establish their effective artifact.
- The acceptance fixture is an independent Go module with database clients. The controller
  remains free of database-client dependencies and never executes SQL, JSONB or Cypher.
- Descriptor deletion and disabled gates retain the independently owned source and workloads.
- Disposable acceptance and artifact publication do not activate production features or define
  production sizing, availability, backups or database licensing policy.

## References

- [Engine provider contract](../engine-providers.md)
- [Real PostgreSQL acceptance](../real-postgresql-acceptance.md)
- [Owned AGE image](../postgresql-age-image.md)
