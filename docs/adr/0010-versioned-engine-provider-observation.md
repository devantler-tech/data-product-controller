# ADR 0010: Versioned engine-provider observation

- Status: Accepted
- Date: 2026-09-30

## Context

The multi-engine roadmap needs a stable interface for SQL, document and graph providers. Product
authors must identify supported engine strategies without putting storage configuration or
credentials into the product descriptor. ADR 0002 establishes external source lifecycle ownership.

## Decision

Keep delegated ownership. `internal/provider/v1.Provider` observes creation, readiness and connection
publication; it never creates, adopts, modifies or deletes resources. The versioned registry selects
one implementation from the declared engine type, provider and adapter. Reconciliation depends on
this interface and contains no database-specific logic.

An optional `spec.source.engine` uses `engine-provider/v1`. Kubernetes CEL admission and the runtime
resolver enforce the same supported matrix. Untyped `crossplane/v1` references preserve ADR 0002.
`sql/native` selects `cnpg/v1`, references a same-namespace CloudNativePG Cluster and requires its
generated application Secret. `document/native` selects `percona-mongodb/v1` and a Percona MongoDB
source with an explicitly published application reader. `graph/native` selects `arangodb/v1` and
an ArangoDB source with a bounded read-only graph publication. `document/cnpg-hybrid` and
`graph/cnpg-hybrid` select `cnpg-hybrid/v1`, a supported immutable PostgreSQL image and dedicated
JSONB or AGE reader publications. Unsupported type/provider/adapter/resource combinations are
rejected before observation.

The CNPG adapter observes readiness conditions, requested/observed/ready instance counts and primary
agreement. Explicit observed generations must be current; absent generations cannot establish spec
freshness. Uncached metadata-only Secret requests verify publication ownership against the current
Cluster UID. Hybrid publications also bind the current source generation and declare their selected
JSONB or AGE capability. Graph requires the owned AGE image and the operator's declared preload
configuration. Independent publishers establish effective grants and queryability; observation
neither installs extensions nor connects to a database. Custom bootstrap credentials require a separate publication contract. Neither provider
messages nor credential values are copied to product status or the registry.

Typed `SourceReady` refreshes independently of composition and other dependencies. Each source
engine observation uses fixed API mapping, a five-second deadline and a 30-second poll. Legacy
Crossplane observation retains its separately configured discovery behavior. The `engine-providers`
OpenFeature gate is default-off alongside `provisioned-sources`; both disabled states prevent reads.
Its rollout and retirement are tracked in #128.

## Consequences

- #3 remains the delivered, foundational provisioner-reference contract; it is not superseded by
  in-controller database provisioning. Storage sizing, creation, backups, rotation and deletion
  stay with external operators. DataProduct deletion retains sources and connection publications.
- Provider adapters extend the supported combinations without altering reconciliation. JSONB and
  AGE interfaces remain distinct from native MongoDB and ArangoDB query protocols.
- Required disposable-cluster acceptance proves real operator behavior, queries, effective grant
  denials, rotation and retained recovery for all five supported combinations. Synthetic Kubernetes
  acceptance separately proves admission and observation paths. Neither establishes production rollout.
- Operators explicitly install and authorize providers. The chart adds no database or Secret grants.
- A declaration of document or graph capability needs its own provider/extension evidence; SQL
  Cluster readiness cannot establish JSONB or AGE support.
